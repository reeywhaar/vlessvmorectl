package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"vlessvmorectl/internal/config"
	"vlessvmorectl/internal/session"
	"vlessvmorectl/internal/store"
)

// The token every test uses. Assertions grep response bodies and logs for this exact
// string, so it must be distinctive enough that an accidental match is impossible.
const testToken = "TESTTOKEN7K2P8RTX4VYB6CFA3GS1WD"

const testPassword = "hunter2hunter2"

// upstream is a stand-in vlessvmore node that records what reached it.
type upstream struct {
	*httptest.Server
	hits atomic.Int64

	lastMethod string
	lastPath   string
	lastQuery  string
	lastAuth   string
	lastCookie string
	lastBody   string
	lastAccept string
}

// newUpstream starts a fake node. handler may be nil for a plain JSON 200.
func newUpstream(t *testing.T, handler http.HandlerFunc) *upstream {
	t.Helper()
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits.Add(1)
		u.lastMethod = r.Method
		u.lastPath = r.URL.Path
		u.lastQuery = r.URL.RawQuery
		u.lastAuth = r.Header.Get("Authorization")
		u.lastCookie = r.Header.Get("Cookie")
		u.lastAccept = r.Header.Get("Accept")
		if b, err := io.ReadAll(r.Body); err == nil {
			u.lastBody = string(b)
		}
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(u.Close)
	return u
}

type harness struct {
	t        *testing.T
	server   *Server
	handler  http.Handler
	store    *store.Store
	sessions *session.Table
	cfg      *config.Config
	logs     *strings.Builder
}

// harnessOpt tunes newHarness. Variadic so the ~30 existing call sites are untouched, and
// so the default harness stays passkey-less — which is the shape most tests want.
type harnessOpt func(*harnessConfig)

type harnessConfig struct{ passkeyOrigin string }

// withPasskeyOrigin turns passkeys on, as VLESSVMORE_PASSKEY_ORIGIN would.
func withPasskeyOrigin(origin string) harnessOpt {
	return func(c *harnessConfig) { c.passkeyOrigin = origin }
}

// newHarness wires a Server against the given nodes, with one administrator already
// created.
func newHarness(t *testing.T, serversEnv string, opts ...harnessOpt) *harness {
	t.Helper()

	var hc harnessConfig
	for _, o := range opts {
		o(&hc)
	}

	cfg, err := config.Load(func(k string) string {
		switch k {
		case config.ServersEnv:
			return serversEnv
		case config.PasskeyOriginEnv:
			return hc.passkeyOrigin
		}
		return ""
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// OpenForDaemon, matching serve.go: the handlers under test write subscribers.json,
	// and the CLI-shaped read-only handle would refuse them.
	st, err := store.OpenForDaemon(t.TempDir())
	if err != nil {
		t.Fatalf("store.OpenForDaemon: %v", err)
	}
	if _, err := st.Admins.Create("alice", testPassword, time.Now()); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	logs := &strings.Builder{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	spa, err := NewSPA(fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>panel</title>")},
		"assets/app-abc123.js": {Data: []byte("console.log(1)")},
	}, log)
	if err != nil {
		t.Fatalf("NewSPA: %v", err)
	}

	sessions := session.NewInMemory()
	srv, err := New(cfg, st, sessions, spa, log, time.Now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return &harness{
		t: t, server: srv, handler: srv.Handler(),
		store: st, sessions: sessions, cfg: cfg, logs: logs,
	}
}

// login returns a cookie for the seeded administrator.
func (h *harness) login() *http.Cookie { return h.loginAs("alice") }

// loginAs returns a cookie for any administrator the test has created, all of whom share
// testPassword.
func (h *harness) loginAs(username string) *http.Cookie {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/login",
		strings.NewReader(`{"username":"`+username+`","password":"`+testPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		h.t.Fatalf("login: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName {
			return c
		}
	}
	h.t.Fatal("login: no session cookie in the response")
	return nil
}

// do issues a request, attaching the cookie when one is given.
func (h *harness) do(method, target string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

// doWithHeaders issues an anonymous GET with extra headers, for the tests that try to
// steer the server with Host and X-Forwarded-Host.
func (h *harness) doWithHeaders(method, target string, headers map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

// proxyTarget builds a /api/proxy?url=… request target, encoding as the SPA does.
func proxyTarget(raw string) string {
	v := url.Values{}
	v.Set("url", raw)
	return ProxyPath + "?" + v.Encode()
}

// adminsFile is where the harness's store keeps its file, for tests that edit it
// behind the daemon's back.
func (h *harness) adminsFile() string {
	return filepath.Join(h.store.Dir(), store.AdminsFile)
}

// ---- subscriber and node helpers ----

// nodeUserFixture is one account on a stub node.
type nodeUserFixture struct {
	ID             string
	Name           string
	Enabled        bool
	DisabledReason string
	QuotaBytes     int64
	WindowTotal    int64
}

// newNodeUpstream starts a stub vlessvmore node that answers the three endpoints the
// access handler calls, plus a stdlib 404 for anything else — which is how the real node
// spells every refusal, including a rejected token.
//
// The fixtures deliberately carry a sub_token and a uuid in their JSON even though this
// service never decodes them: the projection tests assert those strings do *not* reach
// the public response, and an upstream that never sent them would make those tests pass
// for the wrong reason.
func newNodeUpstream(t *testing.T, label string, users ...nodeUserFixture) *upstream {
	t.Helper()
	byID := make(map[string]nodeUserFixture, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}

	return newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		writeNodeJSON := func(body string) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, body)
		}

		if r.URL.Path == "/api/server" {
			writeNodeJSON(`{"name":"` + label + `","host":"` + label + `.example.com",` +
				`"port":8443,"sni":"www.microsoft.com","public_key":"PUBKEY-` + label + `",` +
				`"short_id":"ab12","flow":"xtls-rprx-vision","fingerprint":"chrome",` +
				`"handshake":"www.microsoft.com:443"}`)
			return
		}

		rest, ok := strings.CutPrefix(r.URL.Path, "/api/users/")
		if !ok {
			http.NotFound(w, r) // stdlib text/plain, as the real node does
			return
		}
		id, tail, _ := strings.Cut(rest, "/")
		u, known := byID[id]
		if !known {
			// A genuine not-found is JSON; that is the only way to tell it from a
			// rejected token. See isStdlibNotFound.
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"no such user"}`)
			return
		}

		reason := ""
		if u.DisabledReason != "" {
			reason = `"disabled_reason":"` + u.DisabledReason + `",`
		}
		switch tail {
		case "":
			writeNodeJSON(`{"id":"` + u.ID + `","name":"` + u.Name + `",` +
				`"uuid":"uuid-of-` + u.ID + `","enabled":` + boolJSON(u.Enabled) + `,` +
				`"quota_bytes":` + itoa(u.QuotaBytes) + `,` + reason +
				`"sub_token":"SUBTOKENOF` + strings.ToUpper(u.ID) + `",` +
				`"usage_reset_at":"2026-07-01T00:00:00Z",` +
				`"created_at":"2026-06-01T00:00:00Z","updated_at":"2026-07-01T00:00:00Z",` +
				`"usage":{"up":1,"down":2,"total":3,"window_up":10,"window_down":` +
				itoa(u.WindowTotal-10) + `,"window_total":` + itoa(u.WindowTotal) + `,` +
				`"quota_bytes":` + itoa(u.QuotaBytes) + `,"quota_remaining":` +
				itoa(max(u.QuotaBytes-u.WindowTotal, 0)) + `}}`)
		case "link":
			writeNodeJSON(`{"user_id":"` + u.ID + `","name":"` + u.Name + `",` +
				`"link":"vless://uuid-of-` + u.ID + `@` + label + `.example.com:8443?type=tcp",` +
				`"subscription_url":"https://` + label + `.example.com/sub/SUBTOKENOF` +
				strings.ToUpper(u.ID) + `",` +
				`"install_url":"https://` + label + `.example.com/show/SUBTOKENOF` +
				strings.ToUpper(u.ID) + `",` +
				`"qr":{"size":2,"rows":["10","01"],"quiet_zone":4},` +
				`"subscription_qr":{"size":2,"rows":["11","00"],"quiet_zone":4}}`)
		default:
			http.NotFound(w, r)
		}
	})
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// subscriber creates one directly in the store, bypassing the API, so a test can set up
// state without asserting on the create path.
func (h *harness) subscriber(name string, entries ...store.NewEntry) *store.Subscriber {
	h.t.Helper()
	sub, err := h.store.Subscribers.Create(name, "", time.Now())
	if err != nil {
		h.t.Fatalf("create subscriber: %v", err)
	}
	for _, e := range entries {
		sub, err = h.store.Subscribers.Attach(sub.ID, e, time.Now())
		if err != nil {
			h.t.Fatalf("attach %s/%s: %v", e.ServerID, e.VlessUserID, err)
		}
	}
	return sub
}

// serverID is the derived id for a node URL, which is what an entry references.
func (h *harness) serverID(rawURL string) string {
	h.t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		h.t.Fatalf("parse %q: %v", rawURL, err)
	}
	srv, ok := h.cfg.LookupByOrigin(config.NormalizeOrigin(u.Scheme, u.Host))
	if !ok {
		h.t.Fatalf("%s is not a configured server", rawURL)
	}
	return srv.ID
}
