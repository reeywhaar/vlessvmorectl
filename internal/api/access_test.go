package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"vlessvmorectl/internal/store"
)

// decodeAccess parses a successful access response, failing the test if it cannot.
func decodeAccess(t *testing.T, body string) accessResponse {
	t.Helper()
	var got accessResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode access response: %v (%s)", err, body)
	}
	return got
}

func TestAccessServesASubscribersAccounts(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{
		ID: "u_alice", Name: "alice-phone", Enabled: true,
		QuotaBytes: 1000, WindowTotal: 250,
	})
	h := newHarness(t, up.URL+"|"+testToken)

	sub := h.subscriber("Ivan", store.NewEntry{
		ServerID: h.serverID(up.URL), VlessUserID: "u_alice", Label: "phone",
	})

	rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	got := decodeAccess(t, rec.Body.String())
	if got.Subscriber.Name != "Ivan" {
		t.Errorf("subscriber name = %q, want Ivan", got.Subscriber.Name)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(got.Entries))
	}
	e := got.Entries[0]
	if !e.Available {
		t.Fatalf("entry is not available: %+v", e)
	}
	if e.ServerLabel != "amsterdam" {
		t.Errorf("server_label = %q, want amsterdam", e.ServerLabel)
	}
	if e.Label != "phone" {
		t.Errorf("label = %q, want phone", e.Label)
	}
	if e.Name != "alice-phone" {
		t.Errorf("name = %q, want alice-phone", e.Name)
	}
	if !e.Enabled {
		t.Error("enabled = false, want true")
	}
	if e.QuotaBytes != 1000 {
		t.Errorf("quota_bytes = %d, want 1000", e.QuotaBytes)
	}
	if e.Usage == nil || e.Usage.WindowTotal != 250 {
		t.Errorf("usage = %+v, want window_total 250", e.Usage)
	}
	if !strings.HasPrefix(e.Link, "vless://") {
		t.Errorf("link = %q, want a vless:// URI", e.Link)
	}
	if e.SubscriptionQR == nil || e.QR == nil {
		t.Error("both QR matrices should be passed through")
	}
	if got.FetchedAt.IsZero() {
		t.Error("fetched_at should be set, so the page can state its own age")
	}
}

// The sibling of TestProxyRefusesWithoutSession, and the assertion that keeps the "this
// endpoint needs no cache" argument true: an unknown token is answered from memory, so a
// caller who does not hold a working link cannot make the panel touch a node at all.
func TestAccessUnknownTokenContactsNoNode(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{ID: "u_alice", Enabled: true})
	h := newHarness(t, up.URL+"|"+testToken)

	// A real subscriber exists, so the store is not trivially empty.
	h.subscriber("Ivan", store.NewEntry{ServerID: h.serverID(up.URL), VlessUserID: "u_alice"})

	before := up.hits.Load()
	rec := h.do(http.MethodGet, "/api/access/QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if after := up.hits.Load(); after != before {
		t.Fatalf("an unknown token reached the node %d times; it must reach it zero times",
			after-before)
	}
}

// Malformed, unknown and disabled must be indistinguishable. Each variation would be a
// small oracle for somebody probing.
func TestAccessBadTokensAreIndistinguishable(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{ID: "u_alice", Enabled: true})
	h := newHarness(t, up.URL+"|"+testToken)

	disabled := h.subscriber("Disabled", store.NewEntry{
		ServerID: h.serverID(up.URL), VlessUserID: "u_alice",
	})
	off := true
	if _, err := h.store.Subscribers.Update(disabled.ID, store.SubscriberUpdate{Disabled: &off}, h.server.now()); err != nil {
		t.Fatalf("disable: %v", err)
	}

	cases := map[string]string{
		"malformed": "nope",
		"unknown":   "QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ",
		"disabled":  disabled.Token,
	}

	var first string
	for name, token := range cases {
		rec := h.do(http.MethodGet, "/api/access/"+token, nil, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", name, rec.Code)
		}
		if first == "" {
			first = rec.Body.String()
			continue
		}
		if rec.Body.String() != first {
			t.Errorf("%s: body differs from the others.\n got %s\nwant %s",
				name, rec.Body.String(), first)
		}
	}
	if !strings.Contains(first, accessNotValid) {
		t.Errorf("body should be the fixed message, got %s", first)
	}
}

// A valid token with no entries is deliberately *not* a 404: an operator who creates a
// subscriber and sends the link before attaching anything should not have the person
// report the panel as broken.
func TestAccessValidTokenWithNoEntriesIsOK(t *testing.T) {
	h := newHarness(t, "")
	sub := h.subscriber("Fresh")

	rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeAccess(t, rec.Body.String()); len(got.Entries) != 0 {
		t.Errorf("got %d entries, want 0", len(got.Entries))
	}
}

// The projection is the security boundary. Assert structurally rather than by eye, so a
// field added later trips this rather than shipping.
func TestAccessProjectionOmitsEverythingElse(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{
		ID: "u_alice", Name: "alice", Enabled: true, QuotaBytes: 1000, WindowTotal: 1,
	})
	h := newHarness(t, up.URL+"|"+testToken)

	other := h.subscriber("Somebody Else", store.NewEntry{
		ServerID: h.serverID(up.URL), VlessUserID: "u_alice",
	})
	sub := h.subscriber("Ivan", store.NewEntry{
		ServerID: h.serverID(up.URL), VlessUserID: "u_alice",
	})
	if _, err := h.store.Subscribers.Update(sub.ID, store.SubscriberUpdate{
		Note: ptr("paid until August"),
	}, h.server.now()); err != nil {
		t.Fatalf("set note: %v", err)
	}

	rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	// Values that must never appear, each with the reason it would matter.
	//
	// Note what is deliberately absent from this list. The node's sub_token appears
	// inside subscription_url, and the account uuid inside the vless:// link, because
	// those strings *are* the credential the reader came to collect — that is the whole
	// point of the page. What must not appear is either of them as a field of its own,
	// which the key check below covers, or anything that widens the reader's reach past
	// their own accounts, which this list covers.
	forbidden := map[string]string{
		testToken:           "the node's bearer token",
		sub.Token:           "the subscriber's own share token",
		other.Token:         "another subscriber's share token",
		sub.ID:              "the panel's internal subscriber id",
		"paid until August": "the operator's private note",
		up.URL:              "the node's management URL",
		h.serverID(up.URL):  "the panel's internal server id",
		"PUBKEY-amsterdam":  "the node's Reality public key",
		"www.microsoft.com": "the node's Reality handshake target",
		"Somebody Else":     "another subscriber's name",
	}
	for needle, why := range forbidden {
		if strings.Contains(body, needle) {
			t.Errorf("the public response leaks %s (%q)", why, needle)
		}
	}
	for _, key := range []string{`"sub_token"`, `"uuid"`, `"server_id"`, `"vless_user_id"`, `"note"`, `"token"`} {
		if strings.Contains(body, key) {
			t.Errorf("the public response carries a %s field of its own", key)
		}
	}

	// And structurally: no unexpected keys on an entry.
	var raw struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	allowed := map[string]bool{
		"id": true, "server_label": true, "label": true, "available": true, "reason": true,
		"name": true, "link": true, "subscription_url": true, "install_url": true,
		"qr": true, "subscription_qr": true, "enabled": true, "disabled_reason": true,
		"expires_at": true, "usage": true, "quota_bytes": true,
	}
	for _, e := range raw.Entries {
		for k := range e {
			if !allowed[k] {
				t.Errorf("unexpected key %q in the public entry projection", k)
			}
		}
	}
}

// The invariant that makes this endpoint safe where /api/proxy needed an allowlist: the
// caller contributes zero bytes to any outbound URL.
func TestAccessTakesNoCallerSuppliedTarget(t *testing.T) {
	evil := newUpstream(t, nil)

	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{
		ID: "u_alice", Name: "alice", Enabled: true, QuotaBytes: 1, WindowTotal: 1,
	})
	h := newHarness(t, up.URL+"|"+testToken)
	sub := h.subscriber("Ivan", store.NewEntry{
		ServerID: h.serverID(up.URL), VlessUserID: "u_alice",
	})

	targets := []string{
		"/api/access/" + sub.Token + "?url=" + evil.URL,
		"/api/access/" + sub.Token + "?server=" + evil.URL + "&id=x",
		"/api/access/" + sub.Token + "/../../proxy",
	}
	for _, target := range targets {
		req := h.do(http.MethodGet, target, nil, "")
		_ = req // the status varies by shape; what matters is where the bytes went
	}

	// A forged Host and X-Forwarded-Host must not redirect the fan-out either.
	rec := h.doWithHeaders(http.MethodGet, "/api/access/"+sub.Token, map[string]string{
		"Host":              "evil.test",
		"X-Forwarded-Host":  "evil.test",
		"X-Forwarded-Proto": "https",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}

	if evil.hits.Load() != 0 {
		t.Fatalf("a caller-supplied host received %d requests; it must receive none",
			evil.hits.Load())
	}
	if !strings.HasPrefix(up.lastPath, "/api/") {
		t.Errorf("the upstream saw path %q; only /api/ paths should ever be built", up.lastPath)
	}
}

// One node rebooting must not blank a page whose other accounts are fine.
func TestAccessOneDeadNodeDoesNotFailThePage(t *testing.T) {
	alive := newNodeUpstream(t, "amsterdam", nodeUserFixture{
		ID: "u_alice", Name: "alice", Enabled: true, QuotaBytes: 100, WindowTotal: 1,
	})
	dead := newNodeUpstream(t, "berlin", nodeUserFixture{ID: "u_bob", Enabled: true})
	deadURL := dead.URL
	deadID := ""

	h := newHarness(t, alive.URL+"|"+testToken+","+deadURL+"|"+testToken)
	deadID = h.serverID(deadURL)
	dead.Close() // now nothing is listening there

	sub := h.subscriber("Ivan",
		store.NewEntry{ServerID: h.serverID(alive.URL), VlessUserID: "u_alice"},
		store.NewEntry{ServerID: deadID, VlessUserID: "u_bob"},
	)

	rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 — a dead node must not fail the page (%s)",
			rec.Code, rec.Body.String())
	}
	got := decodeAccess(t, rec.Body.String())
	if len(got.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(got.Entries))
	}

	var ok, bad *accessEntry
	for i := range got.Entries {
		if got.Entries[i].Available {
			ok = &got.Entries[i]
		} else {
			bad = &got.Entries[i]
		}
	}
	if ok == nil || bad == nil {
		t.Fatalf("want one available and one not, got %+v", got.Entries)
	}
	if bad.Reason != reasonUnavailable {
		t.Errorf("reason = %q, want %q", bad.Reason, reasonUnavailable)
	}

	// The reason must be a closed-set value, never a Go error and never a credential.
	//
	// Note what is deliberately *not* asserted: the node's host may appear, because an
	// unreachable node falls back to it as a display label. That is a considered trade —
	// an address grants nothing on its own and is already implied by the vless:// link —
	// whereas the bearer token below is a full-control credential and must never appear
	// under any circumstance.
	body := rec.Body.String()
	for _, secret := range []string{"connection refused", "dial tcp", testToken} {
		if strings.Contains(body, secret) {
			t.Errorf("the public response leaks %q", secret)
		}
	}
	// It still gets *a* label, so the card is not headed by a blank.
	if bad.ServerLabel == "" {
		t.Error("an unreachable node should still fall back to its configured host")
	}
}

func TestAccessDanglingReferences(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{
		ID: "u_alice", Name: "alice", Enabled: true, QuotaBytes: 1, WindowTotal: 1,
	})
	h := newHarness(t, up.URL+"|"+testToken)

	sub := h.subscriber("Ivan",
		// A node this panel no longer manages, e.g. after its URL changed.
		store.NewEntry{ServerID: "deadbeef1234", VlessUserID: "u_ghost"},
		// A node we do manage, but an account it has never heard of.
		store.NewEntry{ServerID: h.serverID(up.URL), VlessUserID: "u_gone"},
	)

	before := up.hits.Load()
	rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeAccess(t, rec.Body.String())

	byReason := map[string]bool{}
	for _, e := range got.Entries {
		if e.Available {
			t.Errorf("entry %s should not be available", e.ID)
		}
		byReason[e.Reason] = true
	}
	if !byReason[reasonUnconfigured] {
		t.Error("an entry naming an unknown server should read as unconfigured")
	}
	if !byReason[reasonRemoved] {
		t.Error("an entry the node 404s in JSON should read as removed")
	}

	// The unconfigured entry must cost nothing upstream: /api/server plus the two calls
	// for the one real node, and not a request more.
	if spent := up.hits.Load() - before; spent > 3 {
		t.Errorf("made %d upstream calls, want at most 3", spent)
	}
}

// A rejected bearer token must read as "temporarily unavailable", never as "your account
// was removed" — the two are indistinguishable to a reader and only one is their problem.
func TestAccessRejectedNodeTokenReadsAsUnavailable(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // stdlib text/plain, which is how vlessvmore refuses a token
	})
	h := newHarness(t, up.URL+"|"+testToken)
	sub := h.subscriber("Ivan", store.NewEntry{
		ServerID: h.serverID(up.URL), VlessUserID: "u_alice",
	})

	rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "")
	got := decodeAccess(t, rec.Body.String())
	if len(got.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(got.Entries))
	}
	if got.Entries[0].Reason != reasonUnavailable {
		t.Errorf("reason = %q, want %q", got.Entries[0].Reason, reasonUnavailable)
	}
	if !strings.Contains(h.logs.String(), "rejected our token") {
		t.Error("the operator should be told in the log that the node refused our token")
	}
}

func TestAccessIsNoStore(t *testing.T) {
	h := newHarness(t, "")
	sub := h.subscriber("Ivan")
	rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store — the body is a bundle of credentials", got)
	}
}

// An operator's cookie will be sent to this endpoint by the browser, since it is
// same-origin. Nothing may vary on it.
func TestAccessIgnoresAnOperatorCookie(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{
		ID: "u_alice", Name: "alice", Enabled: true, QuotaBytes: 1, WindowTotal: 1,
	})
	h := newHarness(t, up.URL+"|"+testToken)
	sub := h.subscriber("Ivan", store.NewEntry{
		ServerID: h.serverID(up.URL), VlessUserID: "u_alice",
	})

	anon := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "")
	authed := h.do(http.MethodGet, "/api/access/"+sub.Token, h.login(), "")

	if stripFetchedAt(anon.Body.String()) != stripFetchedAt(authed.Body.String()) {
		t.Error("the response differed for a signed-in caller; it must not depend on the cookie")
	}
}

func TestAccessRateLimit(t *testing.T) {
	h := newHarness(t, "")
	sub := h.subscriber("Ivan")

	for i := range accessPerTokenRequests {
		if rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, ""); rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i+1, rec.Code)
		}
	}
	rec := h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 429 should say when to come back")
	}
}

// Unknown tokens must not be able to mint limiter entries: that would turn a rate limiter
// into an unbounded map keyed by attacker input.
func TestAccessUnknownTokensDoNotGrowTheLimiter(t *testing.T) {
	h := newHarness(t, "")
	h.subscriber("Ivan")

	for i := range 50 {
		token := strings.Repeat("A", 31) + string(rune('B'+i%20))
		h.do(http.MethodGet, "/api/access/"+token, nil, "")
	}

	h.server.access.mu.Lock()
	n := len(h.server.access.tokens)
	h.server.access.mu.Unlock()
	if n != 0 {
		t.Errorf("the limiter holds %d buckets for tokens that never resolved; want 0", n)
	}
}

// Neither token may reach a response body or a log line, in any of the paths that can
// produce one.
func TestAccessNeverLeaksATokenAnywhere(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam", nodeUserFixture{
		ID: "u_alice", Name: "alice", Enabled: true, QuotaBytes: 1, WindowTotal: 1,
	})
	h := newHarness(t, up.URL+"|"+testToken)
	sub := h.subscriber("Ivan", store.NewEntry{
		ServerID: h.serverID(up.URL), VlessUserID: "u_alice",
	})

	var bodies strings.Builder

	// Success, unknown, malformed, and enough requests to trip the limiter.
	bodies.WriteString(h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "").Body.String())
	bodies.WriteString(h.do(http.MethodGet, "/api/access/QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ", nil, "").Body.String())
	bodies.WriteString(h.do(http.MethodGet, "/api/access/nope", nil, "").Body.String())
	for range accessPerTokenRequests + 2 {
		bodies.WriteString(h.do(http.MethodGet, "/api/access/"+sub.Token, nil, "").Body.String())
	}
	// And the SPA shell, which is served for the same token in the browser's URL.
	bodies.WriteString(h.do(http.MethodGet, "/access/"+sub.Token, nil, "").Body.String())

	for _, secret := range []struct{ value, what string }{
		{testToken, "the node's bearer token"},
		{sub.Token, "the subscriber's share token"},
	} {
		if strings.Contains(bodies.String(), secret.value) {
			t.Errorf("%s appeared in a response body", secret.what)
		}
		if strings.Contains(h.logs.String(), secret.value) {
			t.Errorf("%s appeared in a log line:\n%s", secret.what, h.logs.String())
		}
	}

	// And the fingerprint *is* there, so the log stayed useful rather than merely safe.
	if !strings.Contains(h.logs.String(), store.TokenFingerprint(sub.Token)) {
		t.Error("the log should identify the link by fingerprint")
	}
}

func TestRedactPath(t *testing.T) {
	const token = "QK7M2XA9TESTTOKEN0123456789ABCDE"
	fp := store.TokenFingerprint(token)

	cases := []struct{ in, want string }{
		{"/api/access/" + token, "/api/access/" + fp},
		{"/access/" + token, "/access/" + fp},
		{"/access/" + token + "/extra", "/access/" + fp + "/extra"},
		{"/access/", "/access/"},
		{"/api/login", "/api/login"},
		{"/servers/abc123", "/servers/abc123"},
	}
	for _, c := range cases {
		if got := redactPath(c.in); got != c.want {
			t.Errorf("redactPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// The fingerprint must be derivable by an operator holding the token, and must not be
	// the token itself.
	if strings.Contains(redactPath("/access/"+token), token) {
		t.Error("redactPath left the token in place")
	}
}

// POST to the access endpoint must land on the JSON 404, not a stdlib text/plain 405 —
// the SPA classifies a non-JSON 404 as a rejected token and would tell a subscriber about
// this panel's credentials.
func TestAccessWrongMethodIsJSON(t *testing.T) {
	up := newNodeUpstream(t, "amsterdam")
	h := newHarness(t, up.URL+"|"+testToken)
	sub := h.subscriber("Ivan")

	before := up.hits.Load()
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		rec := h.do(method, "/api/access/"+sub.Token, nil, "")
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: Content-Type = %q, want JSON", method, ct)
		}
		if rec.Code == http.StatusOK {
			t.Errorf("%s: got 200, want a refusal", method)
		}
	}
	if up.hits.Load() != before {
		t.Error("a wrong-method request reached a node")
	}
}

func TestAccessEmptyTokenSegmentIsJSON404(t *testing.T) {
	h := newHarness(t, "")
	rec := h.do(http.MethodGet, "/api/access/", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
}

func ptr[T any](v T) *T { return &v }

// stripFetchedAt removes the timestamp, which legitimately differs between two calls.
func stripFetchedAt(body string) string {
	i := strings.Index(body, `"fetched_at"`)
	if i < 0 {
		return body
	}
	return body[:i]
}
