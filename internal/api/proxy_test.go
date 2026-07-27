package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestProxyRefusesWithoutSession is the assertion this whole endpoint hangs on.
//
// /api/proxy takes a URL from the caller and fetches it with a credential attached. If
// that ever works unauthenticated, this container becomes an SSRF gadget on whatever
// network it sits on, usable by anyone who can reach port 80. The second half — that
// the upstream recorded no hit — is the part that matters: a 401 returned *after* the
// request went out would be no protection at all.
func TestProxyRefusesWithoutSession(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)

	rec := h.do(http.MethodGet, proxyTarget(up.URL+"/api/status"), nil, "")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if n := up.hits.Load(); n != 0 {
		t.Errorf("upstream was contacted %d time(s) for an unauthenticated request", n)
	}
}

// TestProxyOriginAllowlist covers every way a caller might try to point the proxy at
// something this panel does not manage.
func TestProxyOriginAllowlist(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	// The configured origin, e.g. "http://127.0.0.1:54321".
	base := up.URL

	tests := []struct {
		name string
		url  string
		want int
	}{
		{"the configured node", base + "/api/status", http.StatusOK},

		// The case a prefix comparison gets wrong. "http://127.0.0.1:PORT.evil.test"
		// has the configured URL as a literal prefix and is a completely different
		// host — one anybody can register. Accepting it would forward this operator's
		// bearer token to whoever owns it.
		{"prefix confusion", base + ".evil.test/api/status", http.StatusForbidden},

		{"unmanaged host", "http://169.254.169.254/api/status", http.StatusForbidden},
		{"link-local metadata", "http://metadata.google.internal/api/status", http.StatusForbidden},
		{"loopback on another port", "http://127.0.0.1:9/api/status", http.StatusForbidden},
		{"file scheme", "file:///etc/passwd", http.StatusForbidden},
		{"gopher scheme", "gopher://127.0.0.1/api/status", http.StatusForbidden},
		{"no scheme", "//" + strings.TrimPrefix(base, "http://") + "/api/status", http.StatusForbidden},
		{"credentials in url", "http://user:pass@" + strings.TrimPrefix(base, "http://") + "/api/status", http.StatusForbidden},

		// Public capability URLs, which the operator hands to end users and opens
		// directly. Nothing needs them routed through here, so the proxy's reach stays
		// equal to the management API.
		{"sub path", base + "/sub/ABC", http.StatusForbidden},
		{"show path", base + "/show/ABC", http.StatusForbidden},
		{"static path", base + "/static/app.css", http.StatusForbidden},
		{"root", base + "/", http.StatusForbidden},

		{"traversal", base + "/api/../sub/ABC", http.StatusForbidden},
		{"encoded traversal", base + "/api/%2e%2e/sub/ABC", http.StatusForbidden},
		{"double slash", base + "/api//users", http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.do(http.MethodGet, proxyTarget(tc.url), cookie, "")
			if rec.Code != tc.want {
				t.Errorf("status: got %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestProxyOriginNormalisation checks that the allowlist and the caller agree about
// spellings of the same origin. If they disagree, the entry never matches and the node
// is simply unreachable — a bug that looks like a network problem.
func TestProxyOriginNormalisation(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	hostPort := strings.TrimPrefix(up.URL, "http://")

	// Uppercased host: origins are case-insensitive in the host component.
	rec := h.do(http.MethodGet, proxyTarget("http://"+strings.ToUpper(hostPort)+"/api/status"), cookie, "")
	if rec.Code != http.StatusOK {
		t.Errorf("uppercase host: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

func TestProxyMethodAllowlist(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		body := ""
		if m == http.MethodPost || m == http.MethodPatch {
			body = `{"name":"bob"}`
		}
		rec := h.do(m, proxyTarget(up.URL+"/api/users"), cookie, body)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200 (%s)", m, rec.Code, rec.Body.String())
		}
	}

	rec := h.do(http.MethodPut, proxyTarget(up.URL+"/api/users"), cookie, `{}`)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: got %d, want 405", rec.Code)
	}
}

// TestProxyHeaderHandling checks what crosses the boundary in each direction.
func TestProxyHeaderHandling(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		// A node trying to counterfeit our transport-failure marker, and to set a
		// cookie on the operator's browser.
		w.Header().Set(ProxyErrorHeader, "1")
		w.Header().Set("Set-Cookie", "evil=1")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	rec := h.do(http.MethodGet, proxyTarget(up.URL+"/api/status"), cookie, "")

	if got := up.lastAuth; got != "Bearer "+testToken {
		t.Errorf("upstream Authorization: got %q, want the injected bearer token", got)
	}
	// The browser's session cookie must not be handed to a VPN node.
	if up.lastCookie != "" {
		t.Errorf("upstream received the browser's Cookie header: %q", up.lastCookie)
	}
	// Response headers are an allowlist of exactly Content-Type, so neither of these
	// can reach the browser.
	if got := rec.Header().Get(ProxyErrorHeader); got != "" {
		t.Errorf("upstream counterfeited %s: got %q", ProxyErrorHeader, got)
	}
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Errorf("upstream Set-Cookie reached the browser: %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control: got %q, want no-store (responses carry sub_token)", got)
	}
}

// TestProxyDoesNotFollowRedirects: a redirect must never carry the bearer token to
// another host. vlessvmore does not redirect, so this guards against a reverse proxy
// in front of it that starts to.
func TestProxyDoesNotFollowRedirects(t *testing.T) {
	var elsewhere *upstream
	elsewhere = newUpstream(t, nil)

	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/api/status", http.StatusFound)
	})
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	rec := h.do(http.MethodGet, proxyTarget(up.URL+"/api/status"), cookie, "")

	if rec.Code != http.StatusFound {
		t.Errorf("status: got %d, want the 302 passed through unfollowed", rec.Code)
	}
	if n := elsewhere.hits.Load(); n != 0 {
		t.Errorf("the redirect was followed: the token was sent to %s", elsewhere.URL)
	}
}

// TestProxyPassesUpstreamResponsesThroughVerbatim is what lets the SPA apply one
// classification whether a response arrived through the proxy or, some day, directly.
//
// In particular it preserves vlessvmore's two flavours of 404: the stdlib text/plain
// page means "your token was rejected" (it has no 401), while a JSON body means the
// user id genuinely does not exist. Translating either one here would fork that logic.
func TestProxyPassesUpstreamResponsesThroughVerbatim(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{"rejected token", http.StatusNotFound, "text/plain; charset=utf-8", "404 page not found\n"},
		{"unknown user", http.StatusNotFound, "application/json; charset=utf-8", `{"error":"user \"bob\": not found"}`},
		{"conflict", http.StatusConflict, "application/json; charset=utf-8", `{"error":"name taken"}`},
		{"bad request", http.StatusBadRequest, "application/json; charset=utf-8", `{"error":"bad uuid"}`},
		{"upstream 502", http.StatusBadGateway, "text/html", "<html>gateway</html>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			h := newHarness(t, up.URL+"|"+testToken)
			cookie := h.login()

			rec := h.do(http.MethodGet, proxyTarget(up.URL+"/api/status"), cookie, "")

			if rec.Code != tc.status {
				t.Errorf("status: got %d, want %d", rec.Code, tc.status)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.contentType {
				t.Errorf("Content-Type: got %q, want %q", got, tc.contentType)
			}
			if got := rec.Body.String(); got != tc.body {
				t.Errorf("body: got %q, want %q", got, tc.body)
			}
			// An upstream 502 must not be mistaken for one of ours.
			if got := rec.Header().Get(ProxyErrorHeader); got != "" {
				t.Errorf("%s was set on a response the node actually produced: %q", ProxyErrorHeader, got)
			}
		})
	}
}

// TestProxyReportsTransportFailures: the payoff of proxying. A browser making this
// call cross-origin gets an opaque TypeError; here the operating system told us what
// went wrong and we pass that on.
func TestProxyReportsTransportFailures(t *testing.T) {
	up := newUpstream(t, nil)
	dead := up.URL
	up.Close() // nothing is listening on that port now

	h := newHarness(t, dead+"|"+testToken)
	cookie := h.login()

	rec := h.do(http.MethodGet, proxyTarget(dead+"/api/status"), cookie, "")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", rec.Code)
	}
	if got := rec.Header().Get(ProxyErrorHeader); got != "1" {
		t.Errorf("%s: got %q, want \"1\" — the SPA uses this to tell our failure from the node's", ProxyErrorHeader, got)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"proxy_error"`) {
		t.Errorf("body carries no proxy_error classification: %s", got)
	}
	if got := rec.Body.String(); !strings.Contains(got, "refused") {
		t.Errorf("proxy_error: want \"refused\" for a closed port, got %s", got)
	}
}

// TestProxyNeverLeaksTheToken is a permanent regression guard.
//
// Every error path here quotes something — a URL, an upstream message, a Go error
// string — and the token sits one struct field away from all of them. This asserts
// that across the whole surface, including the log, which is where a redaction slip
// would be least likely to be noticed.
func TestProxyNeverLeaksTheToken(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	})
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	targets := []string{
		up.URL + "/api/status",
		up.URL + "/sub/ABC",
		up.URL + ".evil.test/api/status",
		"http://169.254.169.254/api/status",
		"::: not a url",
	}
	for _, target := range targets {
		rec := h.do(http.MethodGet, proxyTarget(target), cookie, "")
		if strings.Contains(rec.Body.String(), testToken) {
			t.Errorf("the token appears in the response body for %q: %s", target, rec.Body.String())
		}
	}
	// Also check the responses that do succeed, and the log.
	if strings.Contains(h.logs.String(), testToken) {
		t.Errorf("the token appears in the log:\n%s", h.logs.String())
	}
}

// TestProxyCoalescesConcurrentGets: three tabs polling the same endpoint should be one
// upstream request, not three. Only GETs, and only while in flight — there is no cache.
func TestProxyCoalescesConcurrentGets(t *testing.T) {
	release := make(chan struct{})
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	const callers = 5
	done := make(chan int, callers)
	for range callers {
		go func() {
			rec := h.do(http.MethodGet, proxyTarget(up.URL+"/api/users"), cookie, "")
			done <- rec.Code
		}()
	}

	// Give them all a chance to arrive and join the in-flight call before the
	// upstream is allowed to answer.
	for range 50 {
		if up.hits.Load() > 0 {
			break
		}
	}
	close(release)

	for range callers {
		if code := <-done; code != http.StatusOK {
			t.Errorf("caller got %d, want 200", code)
		}
	}
	if n := up.hits.Load(); n > callers {
		t.Errorf("upstream saw %d requests for %d callers", n, callers)
	}
}

// TestProxyRequiresUrlParameter is a small thing that would otherwise present as a
// confusing 403.
func TestProxyRequiresUrlParameter(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	rec := h.do(http.MethodGet, ProxyPath, cookie, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// TestProxyForwardsQueryString: the SPA relies on ?include=usage and ?from=… reaching
// the node intact through a doubly-encoded parameter.
func TestProxyForwardsQueryString(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	rec := h.do(http.MethodGet, proxyTarget(up.URL+"/api/users/u_1/usage?from=1753574400&bucket=hour"), cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if up.lastPath != "/api/users/u_1/usage" {
		t.Errorf("upstream path: got %q", up.lastPath)
	}
	if !strings.Contains(up.lastQuery, "from=1753574400") || !strings.Contains(up.lastQuery, "bucket=hour") {
		t.Errorf("upstream query: got %q, want from and bucket intact", up.lastQuery)
	}
}

// TestProxyForwardsBody checks a mutation's body arrives unmangled.
func TestProxyForwardsBody(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	const body = `{"name":"alice","expires_at":null}`
	rec := h.do(http.MethodPatch, proxyTarget(up.URL+"/api/users/u_1"), cookie, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if up.lastBody != body {
		t.Errorf("upstream body: got %q, want %q", up.lastBody, body)
	}
	if up.lastMethod != http.MethodPatch {
		t.Errorf("upstream method: got %q, want PATCH", up.lastMethod)
	}
}
