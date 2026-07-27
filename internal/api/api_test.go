package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vlessvmorectl/internal/session"
)

// TestListServersNeverReturnsTokens is the single most valuable assertion here.
//
// The whole reason this service proxies rather than handing credentials to the browser
// is that a full-control VPN token has no business in a laptop's memory. If this
// endpoint ever starts emitting one — a struct tag removed, a projection replaced with
// the storage type — that decision has been quietly reversed.
func TestListServersNeverReturnsTokens(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)

	// Anonymous first.
	rec := h.do(http.MethodGet, "/api/servers", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: got %d, want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), testToken) {
		t.Fatalf("the token appears in an anonymous response: %s", rec.Body.String())
	}

	// Then authenticated.
	cookie := h.login()
	rec = h.do(http.MethodGet, "/api/servers", cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), testToken) {
		t.Fatalf("the token appears in the server list: %s", rec.Body.String())
	}

	var body struct {
		Servers []struct {
			ID    string `json:"id"`
			URL   string `json:"url"`
			Token string `json:"token"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(body.Servers))
	}
	if body.Servers[0].Token != "" {
		t.Errorf("a token field came back populated: %q", body.Servers[0].Token)
	}
	if body.Servers[0].ID == "" || body.Servers[0].URL == "" {
		t.Errorf("id or url missing: %+v", body.Servers[0])
	}

	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Cookie") {
		t.Errorf("Vary: got %q, want Cookie — a shared cache must not cross-serve operators", got)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestListServersEmptyIsAnEmptyList(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()

	rec := h.do(http.MethodGet, "/api/servers", cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"servers": []`) {
		t.Errorf("want an empty list rather than null or an error: %s", rec.Body.String())
	}
}

// TestNoCORSEverEmitted is a permanent guard.
//
// vlessvmore needs a CORS middleware; this service must not have one. Its absence is
// what makes two of the three CSRF defences in csrfGuard work, so "let's just add CORS
// to debug this" is exactly the change that would silently undo them.
func TestNoCORSEverEmitted(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	paths := []string{"/", "/api/me", "/api/servers", "/healthz", proxyTarget(up.URL + "/api/status")}
	origins := []string{"https://evil.test", "null", "http://localhost:5173", "*"}

	for _, p := range paths {
		for _, o := range origins {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			req.Header.Set("Origin", o)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			h.handler.ServeHTTP(rec, req)

			for _, header := range []string{
				"Access-Control-Allow-Origin",
				"Access-Control-Allow-Credentials",
				"Access-Control-Allow-Methods",
				"Access-Control-Allow-Headers",
			} {
				if got := rec.Header().Get(header); got != "" {
					t.Errorf("%s %s (Origin: %s) emitted %s: %q", http.MethodGet, p, o, header, got)
				}
			}
		}
	}
}

func TestLoginCookieAttributes(t *testing.T) {
	h := newHarness(t, "")

	// Parameterised one attribute at a time, so a future edit cannot quietly drop one
	// while the test still passes on the others.
	req := httptest.NewRequest(http.MethodPost, "/api/login",
		strings.NewReader(`{"username":"alice","password":"`+testPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login: got %d, want 200", rec.Code)
	}
	var c *http.Cookie
	for _, got := range rec.Result().Cookies() {
		if got.Name == session.CookieName {
			c = got
		}
	}
	if c == nil {
		t.Fatalf("no %s cookie was set", session.CookieName)
	}

	if c.Name != "vlessvmore_auth" {
		t.Errorf("name: got %q, want vlessvmore_auth", c.Name)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly is not set: JavaScript can read the session")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite: got %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path: got %q, want /", c.Path)
	}
	if want := int((10 * 24 * time.Hour) / time.Second); c.MaxAge != want {
		t.Errorf("MaxAge: got %d, want %d (a 10-day sliding window)", c.MaxAge, want)
	}
	if c.Value == "" {
		t.Error("the cookie has no value")
	}
}

// TestLoginSecureFollowsTheScheme: Secure is conditional so that a plaintext
// deployment does not silently drop the cookie and produce a login loop with no error
// anywhere. Over TLS it must still be set.
func TestLoginSecureFollowsTheScheme(t *testing.T) {
	h := newHarness(t, "")

	plain := httptest.NewRequest(http.MethodPost, "/api/login",
		strings.NewReader(`{"username":"alice","password":"`+testPassword+`"}`))
	plain.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, plain)
	if c := cookieNamed(rec.Result().Cookies(), session.CookieName); c == nil || c.Secure {
		t.Errorf("over plain http the cookie should not be Secure, got %+v", c)
	}

	fwd := httptest.NewRequest(http.MethodPost, "/api/login",
		strings.NewReader(`{"username":"alice","password":"`+testPassword+`"}`))
	fwd.Header.Set("Content-Type", "application/json")
	fwd.Header.Set("X-Forwarded-Proto", "https")
	rec = httptest.NewRecorder()
	h.handler.ServeHTTP(rec, fwd)
	if c := cookieNamed(rec.Result().Cookies(), session.CookieName); c == nil || !c.Secure {
		t.Errorf("behind a TLS-terminating proxy the cookie must be Secure, got %+v", c)
	}
}

func cookieNamed(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestLoginRejections(t *testing.T) {
	h := newHarness(t, "")

	tests := []struct {
		name        string
		body        string
		contentType string
		want        int
	}{
		{"wrong password", `{"username":"alice","password":"wrongwrongwrong"}`, "application/json", http.StatusUnauthorized},
		{"unknown user", `{"username":"nobody","password":"` + testPassword + `"}`, "application/json", http.StatusUnauthorized},
		{"malformed json", `{`, "application/json", http.StatusBadRequest},
		{"unknown field", `{"username":"alice","password":"x","admin":true}`, "application/json", http.StatusBadRequest},
		{"empty body", ``, "application/json", http.StatusBadRequest},
		// An HTML form can only ever produce these content types, so refusing them
		// closes form-based CSRF outright.
		{"form encoded", `username=alice&password=x`, "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{"text plain", `{"username":"alice"}`, "text/plain", http.StatusUnsupportedMediaType},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var rdr *strings.Reader
			if tc.body != "" {
				rdr = strings.NewReader(tc.body)
			} else {
				rdr = strings.NewReader("")
			}
			req := httptest.NewRequest(http.MethodPost, "/api/login", rdr)
			req.Header.Set("Content-Type", tc.contentType)
			rec := httptest.NewRecorder()
			h.handler.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Errorf("got %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			if rec.Code != http.StatusOK && cookieNamed(rec.Result().Cookies(), session.CookieName) != nil {
				t.Error("a rejected login set a session cookie")
			}
		})
	}
}

// TestLoginDoesNotDistinguishUnknownFromWrong: saying which half was wrong turns this
// into a username oracle that no rate limit fully closes.
func TestLoginDoesNotDistinguishUnknownFromWrong(t *testing.T) {
	h := newHarness(t, "")

	wrong := h.do(http.MethodPost, "/api/login", nil, `{"username":"alice","password":"wrongwrongwrong"}`)
	unknown := h.do(http.MethodPost, "/api/login", nil, `{"username":"nobody","password":"wrongwrongwrong"}`)

	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("the two answers differ:\n  wrong user: %s\n  unknown:    %s", wrong.Body.String(), unknown.Body.String())
	}
}

func TestLoginRateLimit(t *testing.T) {
	h := newHarness(t, "")

	for i := range 5 {
		rec := h.do(http.MethodPost, "/api/login", nil, `{"username":"alice","password":"wrongwrongwrong"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401", i+1, rec.Code)
		}
	}
	rec := h.do(http.MethodPost, "/api/login", nil, `{"username":"alice","password":"wrongwrongwrong"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("the sixth attempt: got %d, want 429", rec.Code)
	}
	// Even the correct password is refused while the window is open, which is the
	// point — otherwise the limit is trivially bypassed.
	rec = h.do(http.MethodPost, "/api/login", nil, `{"username":"alice","password":"`+testPassword+`"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("a correct password during the penalty window: got %d, want 429", rec.Code)
	}
}

func TestLoginRateLimitIsCaseFolded(t *testing.T) {
	h := newHarness(t, "")
	for range 5 {
		h.do(http.MethodPost, "/api/login", nil, `{"username":"alice","password":"wrongwrongwrong"}`)
	}
	// Varying capitalisation must not mint a fresh bucket; the store resolves
	// usernames case-insensitively, so the limiter has to as well.
	rec := h.do(http.MethodPost, "/api/login", nil, `{"username":"ALICE","password":"wrongwrongwrong"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("got %d, want 429", rec.Code)
	}
}

func TestMe(t *testing.T) {
	h := newHarness(t, "")

	rec := h.do(http.MethodGet, "/api/me", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: got %d, want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "no_admins") {
		t.Error("no_admins should be absent when an administrator exists")
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}

	cookie := h.login()
	rec = h.do(http.MethodGet, "/api/me", cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated: got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"alice"`) {
		t.Errorf("body does not name the user: %s", rec.Body.String())
	}
}

// TestMeReportsNoAdmins drives the setup card. Without it an operator stares at a
// login form that can never succeed.
func TestMeReportsNoAdmins(t *testing.T) {
	h := newHarness(t, "")
	if err := h.store.Admins.Delete("alice"); err != nil {
		t.Fatal(err)
	}

	rec := h.do(http.MethodGet, "/api/me", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"no_admins": true`) {
		t.Errorf("want no_admins in the body, got %s", rec.Body.String())
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()

	rec := h.do(http.MethodPost, "/api/logout", cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", rec.Code)
	}
	cleared := cookieNamed(rec.Result().Cookies(), session.CookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("the clearing cookie is wrong: %+v", cleared)
	}
	// Attributes must match the cookie being cleared; browsers match on
	// name+domain+path.
	if cleared.Path != "/" || !cleared.HttpOnly || cleared.SameSite != http.SameSiteLaxMode {
		t.Errorf("clearing cookie attributes do not match the original: %+v", cleared)
	}

	// The record is gone server-side, not merely the cookie.
	if rec := h.do(http.MethodGet, "/api/me", cookie, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the session still works after logout: got %d", rec.Code)
	}

	// Logging out when already logged out is a success.
	if rec := h.do(http.MethodPost, "/api/logout", nil, ""); rec.Code != http.StatusNoContent {
		t.Errorf("logout with no session: got %d, want 204", rec.Code)
	}
}

// TestPasswordChangeInvalidatesLiveSessions is the cross-process invalidation that
// makes "change the password after a compromise" actually mean something. The CLI runs
// in a different process and cannot reach the session table, so this has to work by
// the fingerprint check on each request.
func TestPasswordChangeInvalidatesLiveSessions(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()

	if rec := h.do(http.MethodGet, "/api/me", cookie, ""); rec.Code != http.StatusOK {
		t.Fatalf("precondition: got %d, want 200", rec.Code)
	}

	if _, err := h.store.Admins.SetPassword("alice", "brandnewpassword", time.Now()); err != nil {
		t.Fatal(err)
	}

	if rec := h.do(http.MethodGet, "/api/me", cookie, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the session survived a password change: got %d", rec.Code)
	}
}

func TestDeletedAdminInvalidatesLiveSessions(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()

	if err := h.store.Admins.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	if rec := h.do(http.MethodGet, "/api/me", cookie, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the session survived the admin being deleted: got %d", rec.Code)
	}
}

func TestCSRFSecFetchSite(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	tests := []struct {
		site string
		want int
	}{
		{"cross-site", http.StatusForbidden},
		{"same-origin", http.StatusOK},
		{"same-site", http.StatusOK},
		{"", http.StatusOK}, // absent: curl and older browsers still work
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodDelete, proxyTarget(up.URL+"/api/users/u_1"), nil)
		if tc.site != "" {
			req.Header.Set("Sec-Fetch-Site", tc.site)
		}
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)

		if rec.Code != tc.want {
			t.Errorf("Sec-Fetch-Site %q: got %d, want %d", tc.site, rec.Code, tc.want)
		}
	}
}

// TestDeleteWithoutBodyIsAllowed: a DELETE legitimately carries no Content-Type, and
// the content-type guard must not reject it.
func TestDeleteWithoutBodyIsAllowed(t *testing.T) {
	up := newUpstream(t, nil)
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	req := httptest.NewRequest(http.MethodDelete, proxyTarget(up.URL+"/api/users/u_1"), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newHarness(t, "")
	rec := h.do(http.MethodGet, "/", nil, "")

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s: got %q, want %q", k, got, v)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	// connect-src 'self' and nothing else: the SPA reaches every node through
	// /api/proxy, so an XSS has nowhere to exfiltrate to.
	if !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("CSP has no connect-src 'self': %s", csp)
	}
	for _, forbidden := range []string{"connect-src *", "unsafe-eval", "script-src *"} {
		if strings.Contains(csp, forbidden) {
			t.Errorf("CSP contains %q: %s", forbidden, csp)
		}
	}
	for _, required := range []string{"frame-ancestors 'none'", "base-uri 'none'", "default-src 'self'"} {
		if !strings.Contains(csp, required) {
			t.Errorf("CSP is missing %q: %s", required, csp)
		}
	}
}

func TestHealthz(t *testing.T) {
	h := newHarness(t, "https://a.example.com|"+testToken)
	rec := h.do(http.MethodGet, "/healthz", nil, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok": true`) {
		t.Errorf("body: %s", rec.Body.String())
	}
	// Even "1 server configured" is disclosure to an unauthenticated caller.
	for _, leak := range []string{"a.example.com", "servers", "admins", testToken} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("healthz discloses %q to an anonymous caller: %s", leak, rec.Body.String())
		}
	}
}

// TestUnknownAPIPathIsJSON: an SPA fetch that receives 200 text/html for a typo'd
// endpoint fails with a JSON parse error three layers from the cause.
func TestUnknownAPIPathIsJSON(t *testing.T) {
	h := newHarness(t, "")
	rec := h.do(http.MethodGet, "/api/nope", nil, "")

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
	if strings.Contains(rec.Body.String(), "<!doctype") {
		t.Error("the SPA was served for an unknown /api path")
	}
}
