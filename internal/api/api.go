// Package api serves the panel: a login, a server list, a proxy to the managed
// vlessvmore nodes, and the SPA itself.
//
// # This is not vlessvmore, and refuses differently
//
// The sibling project collapses every refusal — bad token, unknown path, wrong method —
// into one timing-padded 404, because the hostname it answers on has to pass for an
// ordinary static site or Reality's handshake disguise falls apart. None of that
// applies here. This service serves a login page at /; it announces what it is by
// existing. Pretending otherwise would buy nothing and would make the SPA's error
// handling guesswork, so this package returns honest 401s, 403s and 405s.
//
// # There is no CORS middleware, deliberately
//
// The browser only ever talks to this origin — every call to a managed node goes
// through /api/proxy. Adding CORS here would weaken two of the three CSRF defences in
// csrfGuard, so its absence is load-bearing rather than an oversight. There is a test
// asserting no Access-Control-Allow-Origin is ever emitted.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"vlessvmorectl/internal/config"
	"vlessvmorectl/internal/session"
	"vlessvmorectl/internal/store"
)

// Server holds everything the handlers need.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	sessions *session.Table
	spa      *SPA
	limiter  *loginLimiter
	access   *accessLimiter
	proxy    *proxyClient
	log      *slog.Logger
	now      func() time.Time

	// Both nil unless a passkey origin is configured, and that nil is the switch: the
	// routes are not registered without it.
	webauthn   *webauthn.WebAuthn
	challenges *challengeStore
}

// New builds a server. now is injectable so tests can drive expiry without sleeping.
//
// It returns an error so a relying party the library refuses stops the process, rather than
// leaving passkeys quietly switched off with no way to notice. config has already checked
// everything it can, so an error here means a programming mistake.
func New(cfg *config.Config, st *store.Store, sessions *session.Table, spa *SPA, log *slog.Logger, now func() time.Time) (*Server, error) {
	if now == nil {
		now = time.Now
	}
	s := &Server{
		cfg:      cfg,
		store:    st,
		sessions: sessions,
		spa:      spa,
		limiter:  newLoginLimiter(),
		access:   newAccessLimiter(),
		proxy:    newProxyClient(),
		log:      log,
		now:      now,
	}
	if cfg.Passkey != nil {
		rp, err := newRelyingParty(cfg.Passkey)
		if err != nil {
			return nil, fmt.Errorf("passkeys: %w", err)
		}
		s.webauthn, s.challenges = rp, newChallengeStore()
	}
	return s, nil
}

// passkeysEnabled is the one predicate; see Server.webauthn.
func (s *Server) passkeysEnabled() bool { return s.webauthn != nil }

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Unauthenticated. This block is the complete list of what a stranger can reach, and
	// it is kept as one block for exactly that reason.
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.me)
	// A subscriber's own list of accounts, selected by a capability in the path. Unlike
	// /api/proxy this takes nothing from the caller that reaches an outbound request;
	// see accessHandler's doc comment, which is worth reading before changing this line.
	mux.HandleFunc("GET /api/access/{token}", s.accessHandler)

	// Authenticated.
	mux.Handle("POST /api/account/password", s.requireSession(s.changePassword))
	mux.Handle("POST /api/account/username", s.requireSession(s.changeUsername))

	// Registered only when a passkey origin is configured. Without one these paths fall
	// through to the JSON 404 below, so the endpoints do not exist rather than existing and
	// refusing — and the unauthenticated block above stays the complete list of what a
	// stranger can reach, except for the two login routes added here.
	if s.passkeysEnabled() {
		mux.Handle("GET /api/passkeys", s.requireSession(s.listPasskeys))
		mux.Handle("POST /api/passkeys/register/begin", s.requireSession(s.beginRegisterPasskey))
		mux.Handle("POST /api/passkeys/register/finish", s.requireSession(s.finishRegisterPasskey))
		mux.Handle("PATCH /api/passkeys/{id}", s.requireSession(s.renamePasskey))
		mux.Handle("DELETE /api/passkeys/{id}", s.requireSession(s.deletePasskey))

		// Unauthenticated, necessarily: this is how you sign in.
		mux.HandleFunc("POST /api/passkeys/login/begin", s.beginPasskeyLogin)
		mux.HandleFunc("POST /api/passkeys/login/finish", s.finishPasskeyLogin)
	}

	mux.Handle("GET /api/servers", s.requireSession(s.listServers))
	mux.Handle("/api/proxy", s.requireSession(s.proxyHandler))

	mux.Handle("GET /api/subscribers", s.requireSession(s.listSubscribers))
	mux.Handle("POST /api/subscribers", s.requireSession(s.createSubscriber))
	mux.Handle("GET /api/subscribers/{id}", s.requireSession(s.getSubscriber))
	mux.Handle("PATCH /api/subscribers/{id}", s.requireSession(s.patchSubscriber))
	mux.Handle("DELETE /api/subscribers/{id}", s.requireSession(s.deleteSubscriber))
	mux.Handle("POST /api/subscribers/{id}/entries", s.requireSession(s.attachEntry))
	mux.Handle("PATCH /api/subscribers/{id}/entries/{entryID}", s.requireSession(s.patchEntry))
	mux.Handle("DELETE /api/subscribers/{id}/entries/{entryID}", s.requireSession(s.detachEntry))

	// Anything else under /api is a 404 in JSON, never the SPA. An SPA fetch that
	// receives 200 text/html for a typo'd endpoint fails with a JSON parse error three
	// layers away from the cause.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint: "+r.URL.Path)
	})

	// Everything else is the SPA.
	mux.Handle("/", s.spa)

	return s.logRequests(s.securityHeaders(s.csrfGuard(mux)))
}

// ---- middleware ----

// securityHeaders applies the same set to every response.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	csp := s.contentSecurityPolicy()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		// Load-bearing for /access/{token}, not merely tidy: without it, a subscriber
		// tapping through to a node's install page would send their share token to that
		// origin in a Referer header.
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		// A panel should never be indexed, and a share link that reaches a crawler —
		// through a paste, a link preview, a browser extension — should never become a
		// search result.
		h.Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy builds the CSP once, at startup.
//
// connect-src is assembled from a slice rather than written as one literal string.
// Today it is just 'self', because the SPA talks to nothing else — every managed node
// is reached through /api/proxy. If a direct-from-browser mode is ever added, the
// configured origins get appended here and nowhere else; unpicking a string constant
// under time pressure is how a CSP ends up with connect-src *.
func (s *Server) contentSecurityPolicy() string {
	connect := []string{"'self'"}

	return strings.Join([]string{
		"default-src 'self'",
		"connect-src " + strings.Join(connect, " "),
		"img-src 'self' data:",
		// React sets element styles as attributes; Tailwind itself needs none of this.
		"style-src 'self' 'unsafe-inline'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"form-action 'none'",
	}, "; ")
}

// csrfGuard closes the cross-site request paths that SameSite=Lax alone does not.
//
// This matters more than it would for a read-only panel: /api/proxy carries POST, PATCH
// and DELETE straight through to a node's management API, so a forged request deletes
// real users.
//
//  1. A request with a body must be application/json. An HTML form can only ever send
//     urlencoded, multipart or text/plain, so this closes form-based CSRF outright,
//     and a cross-origin fetch with a JSON content type triggers a preflight that this
//     server never answers.
//  2. Sec-Fetch-Site: cross-site is refused on anything that is not a read. The header
//     is absent for curl and old browsers, which is allowed — it is a modern-browser
//     defence, not an authentication check.
//
// No CSRF token: a double-submit cookie plus the plumbing to check it would defend a
// surface these two rules already cover, and every additional moving part here is one
// more thing to get subtly wrong.
func (s *Server) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions

		if !safe {
			if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
				writeError(w, http.StatusForbidden, "cross-site requests are not accepted")
				return
			}
		}

		// Only when a body is actually present: a DELETE with no body legitimately
		// carries no Content-Type.
		if ct := r.Header.Get("Content-Type"); ct != "" || r.ContentLength > 0 {
			if !safe && !isJSONContentType(ct) {
				writeError(w, http.StatusUnsupportedMediaType, "request bodies must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isJSONContentType(ct string) bool {
	base, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(base), "application/json")
}

// contextKey namespaces the values this package puts on a request context.
type contextKey struct{ name string }

var sessionKey = &contextKey{"session"}

func contextWithSession(ctx context.Context, rec *session.Record) context.Context {
	return context.WithValue(ctx, sessionKey, rec)
}

// sessionFrom returns the authenticated session. Only meaningful inside a handler
// wrapped by requireSession.
func sessionFrom(ctx context.Context) (*session.Record, bool) {
	rec, ok := ctx.Value(sessionKey).(*session.Record)
	return rec, ok
}

// requireSession authenticates and, on the way, notices out-of-band changes to the
// admin list.
func (s *Server) requireSession(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec, id, ok := s.authenticate(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		// Re-issue the cookie only when the sliding window actually moved; see
		// session.Refresh.
		if rec.renewed {
			s.setSessionCookie(w, r, id)
		}
		next(w, r.WithContext(contextWithSession(r.Context(), rec.Record)))
	})
}

type authedSession struct {
	*session.Record
	renewed bool
}

// authenticate resolves the request's cookie to a live session, dropping it if the
// admin has since been deleted or had their password changed.
func (s *Server) authenticate(r *http.Request) (authedSession, string, bool) {
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return authedSession{}, "", false
	}

	// One stat per second, regardless of request rate. This is what lets
	// `docker exec … users add` take effect without restarting the daemon.
	if err := s.store.Admins.ReloadIfChanged(s.now()); err != nil {
		s.log.Error("reloading admins.json failed; continuing with the last good copy",
			"path", s.store.Admins.Path(), "error", err)
	}

	rec, renewed, ok := s.sessions.Lookup(c.Value, s.now())
	if !ok {
		return authedSession{}, "", false
	}

	admin, err := s.store.Admins.GetByID(rec.AdminID)
	if err != nil || admin.Fingerprint() != rec.Fingerprint {
		// Deleted, or the password changed from a shell. Either way this session is
		// over, right now, everywhere.
		s.sessions.Delete(c.Value)
		return authedSession{}, "", false
	}
	// The record's copy of the name is from login and may predate a rename. rec is
	// already a clone, so correcting it here costs nothing and keeps every caller
	// reporting the name the store currently holds.
	rec.Username = admin.Username
	return authedSession{Record: rec, renewed: renewed}, c.Value, true
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 400 {
			s.log.Warn("request failed",
				"method", r.Method, "path", redactPath(r.URL.Path), "status", rec.status)
		}
	})
}

// redactPath replaces a subscriber's share token with a fingerprint.
//
// The line above prints the path of every 4xx and 5xx, and /access/{token} carries a
// credential in a path segment — so without this, a mistyped or disabled link writes a
// working capability into the log. An operator holding the token can derive the same
// eight characters, so the line stays useful. Same shape as store.HashFingerprint.
//
// Not solved here: the reverse proxy logs the full request line. See the README.
func redactPath(p string) string {
	for _, prefix := range [...]string{"/api/access/", "/access/"} {
		rest, ok := strings.CutPrefix(p, prefix)
		if !ok {
			continue
		}
		// Only the first segment is the token; anything after it is a path the router
		// will not have matched anyway, and is safe to keep for diagnosis.
		token, tail, _ := strings.Cut(rest, "/")
		if token == "" {
			return p
		}
		out := prefix + tokenFingerprint(token)
		if tail != "" {
			out += "/" + tail
		}
		return out
	}
	return p
}

// tokenFingerprint is eight hex characters standing in for a share token.
func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:4])
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer, which the proxy
// needs in order to flush.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// ---- handlers ----

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	// Deliberately bare. vlessvmore hides its healthz on a unix socket because a JSON
	// health endpoint is a fingerprint no static site has; this service is an obvious
	// login page, so hiding it protects nothing. But it still says only "ok" — even
	// "2 servers configured" is disclosure to an unauthenticated caller, and the
	// interesting diagnostics belong in the startup log where an operator reads them.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// LoginRequest is the body of POST /api/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if !decode(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.Username)

	now := s.now()
	if !s.limiter.allow(username, now) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many attempts; wait a minute and try again")
		return
	}

	admin, err := s.store.Admins.Verify(username, req.Password)
	if err != nil {
		s.limiter.fail(username, now)
		// One message for both halves. Saying which was wrong turns this into a
		// username oracle that no rate limit can fully close.
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.limiter.succeed(username)

	id, _, err := s.sessions.Issue(admin.ID, admin.Username, admin.Fingerprint(), now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start a session: "+err.Error())
		return
	}
	s.setSessionCookie(w, r, id)

	s.warnIfPlaintext(r, admin.Username)

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{"username": admin.Username},
	})
}

// warnIfPlaintext says so when a session cookie was just issued over plain HTTP.
//
// Called from every path that issues one. The cookie has no Secure attribute in that case,
// because this request was plaintext; the alternative failure mode — an always-Secure cookie
// silently discarded by the browser — is a login loop with no error anywhere, and a
// genuinely horrible afternoon to debug.
func (s *Server) warnIfPlaintext(r *http.Request, username string) {
	if requestIsSecure(r) || config.HostIsLoopback(r.Host) {
		return
	}
	s.log.Warn("a sign-in succeeded over plain HTTP on a non-loopback host; the session cookie was issued without Secure and the credential is crossing the network in the clear. Put a TLS-terminating proxy in front of this service.",
		"host", r.Host, "user", username)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(session.CookieName); err == nil {
		// Delete the record first. Clearing only the cookie would leave a live,
		// replayable session id in the table for anyone who captured it.
		s.sessions.Delete(c.Value)
	}
	s.clearSessionCookie(w, r)
	// 204 even with no session: logging out when already logged out is a success.
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	rec, id, ok := s.authenticate(r)
	if !ok {
		body := map[string]any{"error": "not authenticated"}
		// Tells the SPA to render the setup card instead of a login form nobody can
		// satisfy. This does disclose "this panel is unclaimed" to an anonymous
		// visitor — but there is no web bootstrap to exploit (an admin can only be
		// created from a shell on the host), and the alternative is an operator
		// staring at a form that can never succeed.
		if s.store.Admins.Count() == 0 {
			body["no_admins"] = true
		}
		// Rides along on the 401 exactly as no_admins does, because this body is the only
		// thing the login screen receives and the passkey button has to know whether to
		// exist. Configuration only, never a count: "three passkeys are registered here"
		// would be a different and needless disclosure.
		if s.passkeysEnabled() {
			body["passkeys_enabled"] = true
		}
		writeJSON(w, http.StatusUnauthorized, body)
		return
	}
	if rec.renewed {
		s.setSessionCookie(w, r, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":         rec.Username,
		"expires_at":       rec.ExpiresAt.UTC().Format(time.RFC3339),
		"passkeys_enabled": s.passkeysEnabled(),
	})
}

// serverEntry is what GET /api/servers exposes.
//
// A separate type rather than marshalling config.Server directly. config.Server carries
// the bearer token, and while that field is tagged json:"-", relying on a struct tag as
// the only thing standing between a credential and the network is thin. Projecting into
// a type that has no token field at all means the token cannot be exposed here by
// accident, only by someone adding a field on purpose.
type serverEntry struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	out := make([]serverEntry, 0, len(s.cfg.Servers))
	for _, srv := range s.cfg.Servers {
		out = append(out, serverEntry{ID: srv.ID, URL: srv.URL})
	}
	h := w.Header()
	// The set of VPN hostnames an operator manages is not a credential, but it is not
	// something to leave in a shared cache either.
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	h.Set("Vary", "Cookie")
	// Empty configuration is an empty list, not an error: the panel comes up, the
	// operator logs in, and the UI explains which variable to set.
	writeJSON(w, http.StatusOK, map[string]any{"servers": out})
}

// ---- cookies ----

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(session.Idle / time.Second),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	// Name, Path, Secure and SameSite must match the cookie being cleared, because
	// browsers match on name+domain+path.
	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// requestIsSecure reports whether this request reached us over TLS.
//
// Secure is conditional rather than always-on, and that is a considered trade. Always-on
// is stricter, but with the listener hardcoded to :80 its failure mode is a browser
// silently discarding the cookie and an operator watching login "succeed" and then
// immediately 401 — a mystery with no error message on either side. The cost is that a
// plaintext deployment gets a plaintext cookie, which the login handler warns about in
// the log.
//
// X-Forwarded-Proto is trusted here. It is only ever used to make the cookie *more*
// restrictive, so a forged header cannot downgrade anything: the worst an attacker
// achieves by claiming https is a cookie the browser then refuses to send back over
// their plaintext connection.
func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	// A proxy chain may send a list; the first entry is the client-facing one.
	first, _, _ := strings.Cut(proto, ",")
	return strings.EqualFold(strings.TrimSpace(first), "https")
}

// ---- helpers ----

// maxBody caps request bodies. Every body here is a small JSON object; the limit exists
// so a malformed or hostile request cannot make us allocate without bound. Matches the
// sibling's cap so a proxied body that we accept is one vlessvmore will accept too.
const maxBody = 1 << 20

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "reading body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "a JSON body is required")
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}
