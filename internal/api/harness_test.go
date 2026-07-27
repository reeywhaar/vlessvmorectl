package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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

// newHarness wires a Server against the given nodes, with one administrator already
// created.
func newHarness(t *testing.T, serversEnv string) *harness {
	t.Helper()

	cfg, err := config.Load(func(k string) string {
		if k == config.ServersEnv {
			return serversEnv
		}
		return ""
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
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
	srv := New(cfg, st, sessions, spa, log, time.Now)

	return &harness{
		t: t, server: srv, handler: srv.Handler(),
		store: st, sessions: sessions, cfg: cfg, logs: logs,
	}
}

// login returns a cookie for the seeded administrator.
func (h *harness) login() *http.Cookie {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/login",
		strings.NewReader(`{"username":"alice","password":"`+testPassword+`"}`))
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
