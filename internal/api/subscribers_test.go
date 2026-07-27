package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vlessvmorectl/internal/store"
)

func decodeSubscriber(t *testing.T, body string) subscriberView {
	t.Helper()
	var got subscriberView
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode subscriber: %v (%s)", err, body)
	}
	return got
}

// Every route on this surface hands out share tokens or changes who can reach a VPN.
// None of them may answer an anonymous caller, and none may touch a node on the way to
// finding that out.
func TestSubscriberRoutesRequireASession(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{ID: "u_alice", Enabled: true})
	h := newHarness(t, up.URL+"|"+testToken)
	sub := h.subscriber("Ivan", store.NewEntry{
		ServerID: h.serverID(up.URL), VlessUserID: "u_alice",
	})
	entryID := sub.Entries[0].ID

	before := up.hits.Load()
	cases := []struct{ method, target, body string }{
		{http.MethodGet, "/api/subscribers", ""},
		{http.MethodPost, "/api/subscribers", `{"name":"Mallory","note":""}`},
		{http.MethodGet, "/api/subscribers/" + sub.ID, ""},
		{http.MethodPatch, "/api/subscribers/" + sub.ID, `{"name":"Mallory"}`},
		{http.MethodDelete, "/api/subscribers/" + sub.ID, ""},
		{http.MethodPost, "/api/subscribers/" + sub.ID + "/entries", `{"server_id":"x","vless_user_id":"y","label":""}`},
		{http.MethodPatch, "/api/subscribers/" + sub.ID + "/entries/" + entryID, `{"label":"x"}`},
		{http.MethodDelete, "/api/subscribers/" + sub.ID + "/entries/" + entryID, ""},
	}
	for _, c := range cases {
		rec := h.do(c.method, c.target, nil, c.body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: got %d, want 401", c.method, c.target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), sub.Token) {
			t.Errorf("%s %s: the 401 body carried the share token", c.method, c.target)
		}
	}
	if up.hits.Load() != before {
		t.Error("an anonymous request reached a node")
	}
}

func TestSubscriberCRUDRoundTrip(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{
		ID: "u_alice", Name: "alice", Enabled: true, QuotaBytes: 100, WindowTotal: 5,
	})
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	// Create.
	rec := h.do(http.MethodPost, "/api/subscribers", cookie, `{"name":"Ivan","note":"paid to August"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	sub := decodeSubscriber(t, rec.Body.String())
	if sub.Token == "" {
		t.Fatal("create should mint a share token the operator can read back")
	}
	if sub.AccessPath != AccessPath+sub.Token {
		t.Errorf("access_path = %q, want %q", sub.AccessPath, AccessPath+sub.Token)
	}
	// Relative, always: a share link built from a request header is a link to whatever
	// host the caller claimed.
	if strings.Contains(sub.AccessPath, "://") {
		t.Errorf("access_path must stay relative, got %q", sub.AccessPath)
	}
	// Short and lowercase, so it reads well in a URL bar.
	if sub.ID != strings.ToLower(sub.ID) || len(sub.ID) > 12 {
		t.Errorf("id = %q, want a short lowercase handle", sub.ID)
	}

	// Attach.
	rec = h.do(http.MethodPost, "/api/subscribers/"+sub.ID+"/entries", cookie,
		`{"server_id":"`+h.serverID(up.URL)+`","vless_user_id":"u_alice","label":"phone"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("attach: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var attached struct {
		Subscriber subscriberView `json:"subscriber"`
		Warning    string         `json:"warning"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &attached); err != nil {
		t.Fatalf("decode attach: %v", err)
	}
	if attached.Warning != "" {
		t.Errorf("a reachable node should attach without a warning, got %q", attached.Warning)
	}
	if len(attached.Subscriber.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(attached.Subscriber.Entries))
	}
	entry := attached.Subscriber.Entries[0]
	if !entry.ServerConfigured {
		t.Error("server_configured should be true for a node we manage")
	}

	// Attaching the same pair again is a conflict, not a silent duplicate.
	rec = h.do(http.MethodPost, "/api/subscribers/"+sub.ID+"/entries", cookie,
		`{"server_id":"`+h.serverID(up.URL)+`","vless_user_id":"u_alice","label":""}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("duplicate attach: got %d, want 409", rec.Code)
	}

	// Relabel.
	rec = h.do(http.MethodPatch, "/api/subscribers/"+sub.ID+"/entries/"+entry.ID, cookie, `{"label":"the laptop"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("relabel: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeSubscriber(t, rec.Body.String()).Entries[0].Label; got != "the laptop" {
		t.Errorf("label = %q, want \"the laptop\"", got)
	}

	// Patch semantics: an absent key changes nothing, an empty string clears.
	rec = h.do(http.MethodPatch, "/api/subscribers/"+sub.ID, cookie, `{"name":"Ivan P."}`)
	if got := decodeSubscriber(t, rec.Body.String()); got.Name != "Ivan P." || got.Note != "paid to August" {
		t.Errorf("after a name-only patch: name=%q note=%q", got.Name, got.Note)
	}
	rec = h.do(http.MethodPatch, "/api/subscribers/"+sub.ID, cookie, `{"note":""}`)
	if got := decodeSubscriber(t, rec.Body.String()); got.Note != "" {
		t.Errorf("an empty note should clear it, got %q", got.Note)
	}

	// The token survives every edit: it is minted once and nothing else may touch it.
	rec = h.do(http.MethodGet, "/api/subscribers/"+sub.ID, cookie, "")
	if got := decodeSubscriber(t, rec.Body.String()); got.Token != sub.Token {
		t.Error("the share token changed under an edit; it is minted once and never rotated")
	}

	// Disabling makes the link stop answering, which is the only revocation there is.
	h.do(http.MethodPatch, "/api/subscribers/"+sub.ID, cookie, `{"disabled":true}`)
	if rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, ""); rec.Code != http.StatusNotFound {
		t.Errorf("a disabled subscriber's link returned %d, want 404", rec.Code)
	}
	h.do(http.MethodPatch, "/api/subscribers/"+sub.ID, cookie, `{"disabled":false}`)
	if rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, ""); rec.Code != http.StatusOK {
		t.Errorf("re-enabling should bring the same link back, got %d", rec.Code)
	}

	// Detach, then delete.
	rec = h.do(http.MethodDelete, "/api/subscribers/"+sub.ID+"/entries/"+entry.ID, cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detach: got %d, want 200", rec.Code)
	}
	if got := decodeSubscriber(t, rec.Body.String()); len(got.Entries) != 0 {
		t.Errorf("got %d entries after detach, want 0", len(got.Entries))
	}
	if rec := h.do(http.MethodDelete, "/api/subscribers/"+sub.ID, cookie, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204", rec.Code)
	}
	if rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, ""); rec.Code != http.StatusNotFound {
		t.Error("a deleted subscriber's link should stop working")
	}
}

// The no-mirror invariant, made executable: attach looks at the node and keeps nothing.
func TestAttachStoresNothingFromTheNode(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{
		ID: "u_alice", Name: "alice-phone", Enabled: true, QuotaBytes: 999, WindowTotal: 7,
	})
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()

	rec := h.do(http.MethodPost, "/api/subscribers", cookie, `{"name":"Ivan","note":""}`)
	sub := decodeSubscriber(t, rec.Body.String())
	h.do(http.MethodPost, "/api/subscribers/"+sub.ID+"/entries", cookie,
		`{"server_id":"`+h.serverID(up.URL)+`","vless_user_id":"u_alice","label":"phone"}`)

	stored, err := h.store.Subscribers.Get(sub.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	blob, _ := json.Marshal(stored)
	for _, snapshot := range []string{"alice-phone", "999", "uuid-of-u_alice", "SUBTOKENOF"} {
		if strings.Contains(string(blob), snapshot) {
			t.Errorf("the stored record snapshotted %q from the node; it must hold only references", snapshot)
		}
	}
}

func TestAttachRejectsAnIDTheNodeDoesNotHave(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{ID: "u_alice", Enabled: true})
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()
	sub := h.subscriber("Ivan")

	rec := h.do(http.MethodPost, "/api/subscribers/"+sub.ID+"/entries", cookie,
		`{"server_id":"`+h.serverID(up.URL)+`","vless_user_id":"u_typo","label":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if got, _ := h.store.Subscribers.Get(sub.ID); len(got.Entries) != 0 {
		t.Error("a rejected attach should store nothing")
	}
}

// An operator has to be able to hand somebody a link during the incident that made them
// ask for it, so a node being down must not block the attach.
func TestAttachSucceedsWithAWarningWhenTheNodeIsDown(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{ID: "u_alice", Enabled: true})
	h := newHarness(t, up.URL+"|"+testToken)
	cookie := h.login()
	sub := h.subscriber("Ivan")
	serverID := h.serverID(up.URL)
	up.Close()

	rec := h.do(http.MethodPost, "/api/subscribers/"+sub.ID+"/entries", cookie,
		`{"server_id":"`+serverID+`","vless_user_id":"u_alice","label":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Warning string `json:"warning"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Warning == "" {
		t.Error("an unverified attach should say so")
	}
	if got, _ := h.store.Subscribers.Get(sub.ID); len(got.Entries) != 1 {
		t.Error("the entry should have been stored anyway")
	}
}

func TestSubscriberReadsAreNoStore(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()
	sub := h.subscriber("Ivan")

	for _, target := range []string{"/api/subscribers", "/api/subscribers/" + sub.ID} {
		rec := h.do(http.MethodGet, target, cookie, "")
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("%s: Cache-Control = %q, want no-store — these bodies carry share tokens", target, cc)
		}
		if v := rec.Header().Get("Vary"); v != "Cookie" {
			t.Errorf("%s: Vary = %q, want Cookie", target, v)
		}
	}
}

// The CSRF guard has to cover the new mutating surface too.
func TestSubscriberCSRF(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()

	req := httptest.NewRequest(http.MethodPost, "/api/subscribers",
		strings.NewReader(`{"name":"Mallory","note":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST: got %d, want 403", rec.Code)
	}

	// An HTML form can only ever send urlencoded, so refusing it closes form-based CSRF.
	req = httptest.NewRequest(http.MethodPost, "/api/subscribers", strings.NewReader("name=Mallory"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("form-encoded POST: got %d, want 415", rec.Code)
	}
}
