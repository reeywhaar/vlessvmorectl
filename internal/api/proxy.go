package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"vlessvmorectl/internal/config"
)

// ProxyPath is the one endpoint that reaches a managed node.
const ProxyPath = "/api/proxy"

// ProxyErrorHeader marks a 502 as *ours* — this service could not reach the node —
// rather than a 502 the node itself produced and we passed through.
//
// Without it the two are indistinguishable, and the SPA would tell an operator their
// node is unreachable when in fact it answered perfectly well with an upstream error.
// Upstream headers can never counterfeit it: forwardedResponseHeaders is an allowlist
// of exactly one header, so nothing the node sends reaches the browser except
// Content-Type.
const ProxyErrorHeader = "X-Proxy-Error"

// proxyResponseCap bounds what we will buffer from a node. The realistic maximum is a
// `users?include=usage` listing, which is a few hundred bytes per user.
const proxyResponseCap = 8 << 20

// proxyMethods is what vlessvmore's own API uses, and therefore all this needs to
// carry. Anything else is refused rather than forwarded and left for the node to reject
// with a 404 that would read, to the SPA, as a bad token.
var proxyMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPost:   true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// proxyHandler forwards a single request to a managed vlessvmore node.
//
// # Why this endpoint exists
//
// The browser holds no bearer tokens. It asks this service to make the call, and this
// service attaches the credential. That keeps a full-control token off every operator's
// laptop, removes the need to maintain cors_origins on every node, and — the reason it
// was chosen — means a node's management API need not be reachable from the public
// internet at all.
//
// # Why every check below is load-bearing
//
// This is, by construction, an authenticated SSRF gadget: it takes a URL from a client
// and fetches it with a credential attached. What keeps it from being a liability is
// that the URL must resolve, by exact match, to a node this operator already
// configured, and that the path must be one the token already grants. Read the whole
// function before changing any of it.
func (s *Server) proxyHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Session. Enforced by requireSession, which wraps this handler — but note the
	//    ordering, because it is the difference between a management tool and an open
	//    relay on whatever network this container sits on. Nothing below runs for an
	//    anonymous caller.

	if !proxyMethods[r.Method] {
		writeError(w, http.StatusMethodNotAllowed, "method not supported by the vlessvmore API: "+r.Method)
		return
	}

	target := r.URL.Query().Get("url")
	if target == "" {
		writeError(w, http.StatusBadRequest, "the url query parameter is required")
		return
	}

	srv, out, err := s.resolveTarget(target)
	if err != nil {
		// 403 rather than 404: the caller is authenticated and the answer is "not
		// through me", which is a different thing from "no such endpoint".
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	body, ok := s.readProxyBody(w, r)
	if !ok {
		return
	}

	// Who is doing this. For reads it is noise, but for a DELETE against somebody's
	// VPN account it is the only record that the panel was involved at all — the node's
	// own log sees one bearer token shared by every operator.
	if r.Method != http.MethodGet {
		operator := "unknown"
		if rec, ok := sessionFrom(r.Context()); ok {
			operator = rec.Username
		}
		s.log.Info("proxying a change",
			"operator", operator, "server", srv.URL, "method", r.Method, "path", out.Path)
	}

	res, perr := s.proxy.do(r.Context(), srv, r.Method, out, r.Header.Get("Content-Type"), body)
	if perr != nil {
		reason, detail := classifyTransportError(perr)
		s.log.Warn("proxy request failed",
			"server", srv.URL, "method", r.Method, "path", out.Path,
			"reason", reason, "error", detail)

		w.Header().Set(ProxyErrorHeader, "1")
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":       detail,
			"proxy_error": reason,
		})
		return
	}

	// A text/plain 404 from vlessvmore means it rejected our bearer token — it has no
	// 401, by design. That is invisible to the operator unless we say so here: the SPA
	// will show it, but only to whoever happens to be looking at that server's card.
	if res.status == http.StatusNotFound && isStdlibNotFound(res.contentType, res.body) {
		s.log.Warn("vlessvmore rejected our token (it answers 404, never 401); check the token in VLESSVMORE_SERVERS",
			"server", srv.URL, "path", out.Path)
	}

	h := w.Header()
	if res.contentType != "" {
		h.Set("Content-Type", res.contentType)
	}
	// Responses routinely carry sub_token and subscription URLs.
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(res.status)
	_, _ = w.Write(res.body)
}

// resolveTarget validates the requested URL and returns the node it belongs to
// alongside the URL to actually fetch.
//
// The returned URL is rebuilt from the *configured* scheme and host rather than the
// caller's, and from the decoded path, so that no encoding trick in the input survives
// into the outbound request.
func (s *Server) resolveTarget(target string) (*config.Server, *url.URL, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, nil, errors.New("url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, nil, errors.New("url must be http or https")
	}
	if u.Host == "" {
		return nil, nil, errors.New("url has no host")
	}
	if u.User != nil {
		return nil, nil, errors.New("url must not contain credentials")
	}
	if u.Fragment != "" {
		return nil, nil, errors.New("url must not contain a fragment")
	}

	// 2. Origin allowlist, by exact map hit.
	//
	// NormalizeOrigin is the same function the configuration went through, so the two
	// sides cannot disagree about whether ":443" counts.
	//
	// This is a map lookup and must stay one. A prefix comparison against the
	// configured URL — which is the obvious-looking way to write this — accepts
	// "https://vpn.example.com.attacker.test", a domain anyone can register, and
	// hands it this operator's bearer token on the first poll.
	origin := config.NormalizeOrigin(u.Scheme, u.Host)
	srv, ok := s.cfg.LookupByOrigin(origin)
	if !ok {
		return nil, nil, errors.New("this panel does not manage " + origin)
	}

	// 3. Path allowlist.
	//
	// Only the management API. /sub/, /show/ and /static/ are public capability URLs
	// meant to be opened directly by the person they were issued to, so routing them
	// through here would add reach without adding value. Keeping the proxy's blast
	// radius equal to the management API is worth the four lines.
	if !strings.HasPrefix(u.Path, "/api/") {
		return nil, nil, errors.New("only /api/ paths are proxied")
	}
	// url.Parse has already decoded %2e%2e into "..", so this catches the encoded
	// form too. Rejecting rather than silently cleaning: a caller that sent a
	// traversal is not one whose intent we should guess at.
	if path.Clean(u.Path) != u.Path {
		return nil, nil, errors.New("url path must be normalised")
	}

	return srv, &url.URL{
		Scheme:   u.Scheme,
		Host:     u.Host,
		Path:     u.Path, // decoded; Go re-encodes canonically, dropping any RawPath trickery
		RawQuery: u.RawQuery,
	}, nil
}

func (s *Server) readProxyBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, true
	}
	defer r.Body.Close()
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "reading body: "+err.Error())
		return nil, false
	}
	return b, true
}

// isStdlibNotFound reports whether a 404 is Go's stdlib page — which is how vlessvmore
// spells every refusal, including a rejected token — rather than a real
// {"error": "..."} not-found.
func isStdlibNotFound(contentType string, body []byte) bool {
	if !strings.HasPrefix(contentType, "text/plain") {
		return false
	}
	return strings.TrimSpace(string(body)) == "404 page not found"
}

// ---- the outbound client ----

type proxyResponse struct {
	status      int
	contentType string
	body        []byte
}

type proxyClient struct {
	http *http.Client

	mu       sync.Mutex
	inflight map[string]*proxyCall
}

func newProxyClient() *proxyClient {
	return &proxyClient{
		http: &http.Client{
			Timeout: config.HTTPTimeout,
			// A redirect must never carry the bearer token to another host.
			// vlessvmore does not redirect; this is what keeps that true if a
			// misconfigured reverse proxy in front of it ever starts to.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				MaxIdleConns:          64,
				MaxIdleConnsPerHost:   8, // the 10s poll should reuse its TLS session
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: time.Second,
			},
		},
		inflight: make(map[string]*proxyCall),
	}
}

// do performs the upstream request, coalescing identical concurrent GETs.
func (c *proxyClient) do(ctx context.Context, srv *config.Server, method string, u *url.URL, contentType string, body []byte) (*proxyResponse, error) {
	if method != http.MethodGet {
		return c.roundTrip(ctx, srv, method, u, contentType, body)
	}
	return c.singleFlightGet(ctx, srv, u)
}

// singleFlightGet shares one upstream call between identical concurrent GETs.
//
// The SPA polls every ten seconds, and an operator with the overview open in three tabs
// is three identical requests arriving together. Collapsing them is the difference
// between 18 and 6 upstream requests a minute, and it is the sort of load a small VPS
// running sing-box should not have to absorb for no reason.
//
// Strictly in-flight only — there is no cache and no TTL. A stale cache here would show
// wrong usage figures and wrong quota state, which is materially worse than a few extra
// requests.
func (c *proxyClient) singleFlightGet(ctx context.Context, srv *config.Server, u *url.URL) (*proxyResponse, error) {
	key := u.String()

	c.mu.Lock()
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-call.done:
			return call.res, call.err
		case <-ctx.Done():
			// This caller gave up; the leader carries on for whoever is left.
			return nil, ctx.Err()
		}
	}
	call := &proxyCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	// Deliberately not ctx: the leader must not be cancelled by whichever waiter
	// happens to disconnect first, or a browser closing a tab would fail the request
	// for the two tabs still watching. context.WithoutCancel keeps the values and
	// drops the cancellation, and the client's own Timeout still bounds it.
	call.res, call.err = c.roundTrip(context.WithoutCancel(ctx), srv, http.MethodGet, u, "", nil)

	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
	close(call.done)

	return call.res, call.err
}

type proxyCall struct {
	done chan struct{}
	res  *proxyResponse
	err  error
}

func (c *proxyClient) roundTrip(ctx context.Context, srv *config.Server, method string, u *url.URL, contentType string, body []byte) (*proxyResponse, error) {
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil, err
	}

	// 5. Forward, with an allowlist of request headers.
	//
	// An allowlist rather than "copy everything and delete a few": the browser's own
	// Authorization and Cookie headers must never reach a node, and a denylist is a
	// list somebody eventually forgets to extend.
	req.Header.Set("Authorization", "Bearer "+srv.Token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	b, err := io.ReadAll(io.LimitReader(res.Body, proxyResponseCap))
	if err != nil {
		return nil, err
	}

	// 6. Pass the answer through verbatim.
	//
	// The status and body are exactly what the node sent. This is not laziness: it is
	// what lets the SPA apply one classification to a response whether it arrived
	// through here or, some day, directly. Translating statuses in this function would
	// fork that logic in two and guarantee the halves drift.
	return &proxyResponse{
		status:      res.StatusCode,
		contentType: res.Header.Get("Content-Type"),
		body:        b,
	}, nil
}

// classifyTransportError turns a Go error chain into a short reason the SPA can render
// a real sentence from.
//
// This is the concrete payoff of proxying. A browser making the same request
// cross-origin gets an opaque TypeError and cannot tell a DNS failure from a refused
// connection from a CORS policy; here the operating system already told us which it
// was, and all we have to do is not throw that away.
func classifyTransportError(err error) (reason, detail string) {
	detail = err.Error()

	switch {
	case errors.Is(err, context.Canceled):
		return "canceled", detail
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "timeout", detail
	}

	// DNS before connection errors: a net.OpError wraps a DNSError, and reporting
	// "connection refused" for a hostname that does not resolve sends the operator to
	// the wrong machine.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns", detail
	}

	var certErr *tls.CertificateVerificationError
	var hostErr x509.HostnameError
	var authErr x509.UnknownAuthorityError
	var recErr tls.RecordHeaderError
	if errors.As(err, &certErr) || errors.As(err, &hostErr) || errors.As(err, &authErr) || errors.As(err, &recErr) {
		return "tls", detail
	}

	if errors.Is(err, syscall.ECONNREFUSED) {
		return "refused", detail
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return "refused", detail
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout", detail
	}
	return "unknown", detail
}
