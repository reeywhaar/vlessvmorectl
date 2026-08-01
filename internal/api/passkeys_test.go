package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"vlessvmorectl/internal/store"
)

// The relying party the passkey-enabled harness is configured with.
const (
	testPasskeyOrigin = "https://panel.example.com"
	testPasskeyRPID   = "panel.example.com"
)

func newPasskeyHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t, "", withPasskeyOrigin(testPasskeyOrigin))
}

// beginRegistration starts an enrolment and returns the state plus the challenge the
// authenticator has to sign.
func (h *harness) beginRegistration(cookie *http.Cookie) (state, challenge string) {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/passkeys/register/begin", cookie, "")
	if rec.Code != http.StatusOK {
		h.t.Fatalf("register/begin: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		State   string `json:"state"`
		Options struct {
			Challenge string `json:"challenge"`
		} `json:"options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		h.t.Fatal(err)
	}
	if body.State == "" || body.Options.Challenge == "" {
		h.t.Fatalf("register/begin returned %s", rec.Body.String())
	}
	return body.State, body.Options.Challenge
}

func (h *harness) beginLogin() (state, challenge string) {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/passkeys/login/begin", nil, "")
	if rec.Code != http.StatusOK {
		h.t.Fatalf("login/begin: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		State   string `json:"state"`
		Options struct {
			Challenge string `json:"challenge"`
		} `json:"options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		h.t.Fatal(err)
	}
	return body.State, body.Options.Challenge
}

// enrol runs a whole successful registration and returns the created passkey's id.
//
// No label: enrolment names nothing, and a test that wants a particular name renames afterwards.
func (h *harness) enrol(cookie *http.Cookie, a *fakeAuthenticator) string {
	h.t.Helper()
	state, challenge := h.beginRegistration(cookie)
	body := mustJSON(h.t, map[string]any{
		"state":      state,
		"credential": json.RawMessage(a.register(h.t, "webauthn.create", challenge, testPasskeyOrigin, testPasskeyRPID)),
	})
	rec := h.do(http.MethodPost, "/api/passkeys/register/finish", cookie, body)
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("register/finish: got %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Passkey passkeyView `json:"passkey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		h.t.Fatal(err)
	}
	return out.Passkey.ID
}

// handleFor is the user handle the store minted for an administrator.
func (h *harness) handleFor(adminID string) []byte {
	h.t.Helper()
	handle, ok := h.store.Passkeys.Handle(adminID)
	if !ok {
		h.t.Fatal("no handle for that administrator")
	}
	raw, err := base64.RawURLEncoding.DecodeString(handle)
	if err != nil {
		h.t.Fatal(err)
	}
	return raw
}

func (h *harness) adminID(username string) string {
	h.t.Helper()
	admin, err := h.store.Admins.Get(username)
	if err != nil {
		h.t.Fatal(err)
	}
	return admin.ID
}

// signInWithPasskey runs a whole assertion and returns the response.
func (h *harness) signInWithPasskey(a *fakeAuthenticator, handle []byte) *httptest.ResponseRecorder {
	h.t.Helper()
	state, challenge := h.beginLogin()
	body := mustJSON(h.t, map[string]any{
		"state":      state,
		"credential": json.RawMessage(a.assertion(h.t, "webauthn.get", challenge, testPasskeyOrigin, testPasskeyRPID, handle, nil)),
	})
	return h.do(http.MethodPost, "/api/passkeys/login/finish", nil, body)
}

// Enrolment asks for no name and stores none, and the listing identifies the credential instead.
//
// The whole chain in one test, because each link is invisible on its own: the authenticator
// asserts an id, the store keeps it base64url, the view renders it as a UUID, and that resolves
// to a name and a logo URL. The fake authenticator's id is Chrome on Mac's real one, which is
// what makes the last step assertable.
func TestPasskeyEnrolmentNamesTheAuthenticatorRatherThanStoringALabel(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()

	h.enrol(cookie, newFakeAuthenticator(t))

	stored := h.store.Passkeys.List(h.adminID("alice"))
	if len(stored) != 1 {
		t.Fatalf("stored %d credentials, want 1", len(stored))
	}
	if stored[0].Label != "" {
		t.Errorf("Label = %q, want it left empty for the panel to fill in", stored[0].Label)
	}

	rec := h.do(http.MethodGet, "/api/passkeys", cookie, "")
	var body struct {
		Passkeys []passkeyView `json:"passkeys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Passkeys) != 1 {
		t.Fatalf("listed %d passkeys, want 1", len(body.Passkeys))
	}

	got := body.Passkeys[0]
	if got.Label != "" {
		t.Errorf("label = %q, want it empty on the wire too — the panel computes what to show", got.Label)
	}
	if got.AAGUID != "adce0002-35bc-c60a-648b-0b25f1f05503" {
		t.Errorf("aaguid = %q", got.AAGUID)
	}
	if got.Provider != "Chrome on Mac" {
		t.Errorf("provider = %q, want %q", got.Provider, "Chrome on Mac")
	}
	if !strings.HasPrefix(got.Logo, logoURLPrefix) {
		t.Errorf("logo = %q, want a URL under %s", got.Logo, logoURLPrefix)
	}
	// And that URL is one the panel can actually fetch.
	if img := h.doWithHeaders(http.MethodGet, got.Logo, nil); img.Code != http.StatusOK {
		t.Errorf("fetching %s: got %d, want 200", got.Logo, img.Code)
	}
}

// The whole point: enrol, then sign in with no cookie and no username.
func TestPasskeyRoundTrip(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)

	id := h.enrol(cookie, auth)
	if id == "" {
		t.Fatal("no passkey id was returned")
	}

	rec := h.signInWithPasskey(auth, h.handleFor(h.adminID("alice")))
	if rec.Code != http.StatusOK {
		t.Fatalf("login/finish: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		User struct{ Username string } `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.User.Username != "alice" {
		t.Errorf("username = %q", body.User.Username)
	}

	// The cookie it hands back is an ordinary session.
	fresh := cookieFrom(rec)
	if fresh == nil {
		t.Fatal("no session cookie was issued")
	}
	if got := h.do(http.MethodGet, "/api/me", fresh, ""); got.Code != http.StatusOK {
		t.Errorf("the passkey session does not authenticate: %d (%s)", got.Code, got.Body.String())
	}

	// And last_used_at is now recorded.
	list := h.store.Passkeys.List(h.adminID("alice"))
	if len(list) != 1 || list[0].LastUsedAt.IsZero() {
		t.Errorf("last_used_at was not recorded: %+v", list)
	}
}

// Every route must be absent, not merely refusing, when no origin is configured.
func TestPasskeyRoutesDoNotExistWhenDisabled(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/passkeys"},
		{http.MethodPost, "/api/passkeys/register/begin"},
		{http.MethodPost, "/api/passkeys/register/finish"},
		{http.MethodPatch, "/api/passkeys/abc"},
		{http.MethodDelete, "/api/passkeys/abc"},
		{http.MethodPost, "/api/passkeys/login/begin"},
		{http.MethodPost, "/api/passkeys/login/finish"},
	} {
		rec := h.do(tc.method, tc.path, cookie, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: got %d, want 404", tc.method, tc.path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "no such endpoint") {
			t.Errorf("%s %s: body is not the catch-all 404: %s", tc.method, tc.path, rec.Body.String())
		}
	}
}

// The login screen learns from the 401 body whether to offer the button at all.
func TestMeReportsWhetherPasskeysAreEnabled(t *testing.T) {
	off := newHarness(t, "")
	body := off.do(http.MethodGet, "/api/me", nil, "").Body.String()
	if strings.Contains(body, "passkeys_enabled") {
		t.Errorf("the disabled 401 body mentions passkeys: %s", body)
	}

	on := newPasskeyHarness(t)
	var anon map[string]any
	if err := json.Unmarshal(on.do(http.MethodGet, "/api/me", nil, "").Body.Bytes(), &anon); err != nil {
		t.Fatal(err)
	}
	if anon["passkeys_enabled"] != true {
		t.Errorf("the 401 body does not advertise passkeys: %v", anon)
	}
	// Configuration only. A count would be a different and needless disclosure.
	for k := range anon {
		if strings.Contains(k, "count") {
			t.Errorf("the anonymous body carries a count: %v", anon)
		}
	}

	cookie := on.login()
	var authed map[string]any
	if err := json.Unmarshal(on.do(http.MethodGet, "/api/me", cookie, "").Body.Bytes(), &authed); err != nil {
		t.Fatal(err)
	}
	if authed["passkeys_enabled"] != true {
		t.Errorf("the signed-in body does not advertise passkeys: %v", authed)
	}
}

func TestPasskeyRegistrationRequiresASession(t *testing.T) {
	h := newPasskeyHarness(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/passkeys"},
		{http.MethodPost, "/api/passkeys/register/begin"},
	} {
		if rec := h.do(tc.method, tc.path, nil, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: got %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// A challenge is bound to whoever began it, so one signed-in administrator cannot finish
// another's enrolment even holding their state string.
func TestPasskeyEnrolmentIsBoundToItsAdministrator(t *testing.T) {
	h := newPasskeyHarness(t)
	if _, err := h.store.Admins.Create("bob", testPassword, time.Now()); err != nil {
		t.Fatal(err)
	}
	alice := h.login()
	bob := h.loginAs("bob")

	state, challenge := h.beginRegistration(alice)
	auth := newFakeAuthenticator(t)
	body := mustJSON(t, map[string]any{
		"state":      state,
		"credential": json.RawMessage(auth.register(t, "webauthn.create", challenge, testPasskeyOrigin, testPasskeyRPID)),
	})

	if rec := h.do(http.MethodPost, "/api/passkeys/register/finish", bob, body); rec.Code != http.StatusBadRequest {
		t.Errorf("bob finished alice's enrolment: got %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if len(h.store.Passkeys.List(h.adminID("bob"))) != 0 {
		t.Error("bob acquired a passkey from alice's ceremony")
	}
}

// Single-use, checked before any crypto: a replayed finish cannot succeed twice.
func TestPasskeyChallengeIsSingleUse(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)

	state, challenge := h.beginRegistration(cookie)
	body := mustJSON(t, map[string]any{
		"state":      state,
		"credential": json.RawMessage(auth.register(t, "webauthn.create", challenge, testPasskeyOrigin, testPasskeyRPID)),
	})
	if rec := h.do(http.MethodPost, "/api/passkeys/register/finish", cookie, body); rec.Code != http.StatusCreated {
		t.Fatalf("first finish: got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := h.do(http.MethodPost, "/api/passkeys/register/finish", cookie, body); rec.Code != http.StatusBadRequest {
		t.Errorf("replayed finish: got %d, want 400", rec.Code)
	}
}

func TestPasskeyChallengeExpires(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)

	state, challenge := h.beginRegistration(cookie)
	// Drive the clock past the ceremony's life rather than sleeping.
	base := time.Now()
	h.server.now = func() time.Time { return base.Add(passkeyChallengeTTL + time.Minute) }

	body := mustJSON(t, map[string]any{
		"state":      state,
		"credential": json.RawMessage(auth.register(t, "webauthn.create", challenge, testPasskeyOrigin, testPasskeyRPID)),
	})
	if rec := h.do(http.MethodPost, "/api/passkeys/register/finish", cookie, body); rec.Code != http.StatusBadRequest {
		t.Errorf("expired challenge: got %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

// Each of these is a distinct thing the library is relied on to check, asserted by making
// the authenticator actually get it wrong.
func TestPasskeyRegistrationRejections(t *testing.T) {
	tests := []struct {
		name           string
		typ            string
		origin, rpID   string
		flags          byte
		wantStatusCode int
	}{
		{"wrong origin", "webauthn.create", "https://evil.test", testPasskeyRPID, flagUserPresent | flagUserVerified, http.StatusBadRequest},
		{"wrong rpId in authData", "webauthn.create", testPasskeyOrigin, "evil.test", flagUserPresent | flagUserVerified, http.StatusBadRequest},
		{"user-present flag clear", "webauthn.create", testPasskeyOrigin, testPasskeyRPID, 0, http.StatusBadRequest},
		{"assertion type posted to registration", "webauthn.get", testPasskeyOrigin, testPasskeyRPID, flagUserPresent | flagUserVerified, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newPasskeyHarness(t)
			cookie := h.login()
			auth := newFakeAuthenticator(t).withFlags(tc.flags)

			state, challenge := h.beginRegistration(cookie)
			body := mustJSON(t, map[string]any{
				"state":      state,
				"credential": json.RawMessage(auth.register(t, tc.typ, challenge, tc.origin, tc.rpID)),
			})
			rec := h.do(http.MethodPost, "/api/passkeys/register/finish", cookie, body)
			if rec.Code != tc.wantStatusCode {
				t.Errorf("got %d, want %d (%s)", rec.Code, tc.wantStatusCode, rec.Body.String())
			}
			if n := len(h.store.Passkeys.List(h.adminID("alice"))); n != 0 {
				t.Errorf("a rejected enrolment stored %d credentials", n)
			}
		})
	}
}

func TestPasskeyRegistrationRejectsABadChallenge(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)

	state, _ := h.beginRegistration(cookie)
	body := mustJSON(t, map[string]any{
		"state":      state,
		"credential": json.RawMessage(auth.register(t, "webauthn.create", "bm90LXRoZS1jaGFsbGVuZ2U", testPasskeyOrigin, testPasskeyRPID)),
	})
	if rec := h.do(http.MethodPost, "/api/passkeys/register/finish", cookie, body); rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestPasskeyPerAdminCap(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	for range store.MaxPasskeysPerAdmin {
		h.enrol(cookie, newFakeAuthenticator(t))
	}
	if rec := h.do(http.MethodPost, "/api/passkeys/register/begin", cookie, ""); rec.Code != http.StatusConflict {
		t.Errorf("past the cap: got %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

// Every one of these is an authentication failure, so every one is the same flat 401 with
// the same message: never an oracle for which part was wrong.
func TestPasskeyLoginRejections(t *testing.T) {
	tests := []struct {
		name         string
		typ          string
		origin, rpID string
		mangle       func(t *testing.T, m map[string]any)
		wrongHandle  bool
	}{
		{name: "wrong origin", typ: "webauthn.get", origin: "https://evil.test", rpID: testPasskeyRPID},
		{name: "wrong rpId", typ: "webauthn.get", origin: testPasskeyOrigin, rpID: "evil.test"},
		{name: "registration type posted to login", typ: "webauthn.create", origin: testPasskeyOrigin, rpID: testPasskeyRPID},
		{
			name: "corrupted signature", typ: "webauthn.get", origin: testPasskeyOrigin, rpID: testPasskeyRPID,
			mangle: func(t *testing.T, m map[string]any) { flipLastByte(t, m, "signature") },
		},
		{
			// Proves the counter really is inside the signed bytes rather than trusted.
			name: "corrupted authenticator data", typ: "webauthn.get", origin: testPasskeyOrigin, rpID: testPasskeyRPID,
			mangle: func(t *testing.T, m map[string]any) { flipLastByte(t, m, "authenticatorData") },
		},
		{name: "unknown user handle", typ: "webauthn.get", origin: testPasskeyOrigin, rpID: testPasskeyRPID, wrongHandle: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newPasskeyHarness(t)
			cookie := h.login()
			auth := newFakeAuthenticator(t)
			h.enrol(cookie, auth)

			handle := h.handleFor(h.adminID("alice"))
			if tc.wrongHandle {
				handle = []byte("this-is-not-a-registered-handle!")
			}

			state, challenge := h.beginLogin()
			var mangle func(map[string]any)
			if tc.mangle != nil {
				mangle = func(m map[string]any) { tc.mangle(t, m) }
			}
			body := mustJSON(t, map[string]any{
				"state":      state,
				"credential": json.RawMessage(auth.assertion(t, tc.typ, challenge, tc.origin, tc.rpID, handle, mangle)),
			})
			rec := h.do(http.MethodPost, "/api/passkeys/login/finish", nil, body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want 401 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), passkeyLoginRefusal) {
				t.Errorf("body is not the generic refusal: %s", rec.Body.String())
			}
			if cookieFrom(rec) != nil {
				t.Error("a refused sign-in issued a cookie")
			}
		})
	}
}

func TestPasskeyLoginChallengeIsSingleUse(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)
	h.enrol(cookie, auth)
	handle := h.handleFor(h.adminID("alice"))

	state, challenge := h.beginLogin()
	body := mustJSON(t, map[string]any{
		"state":      state,
		"credential": json.RawMessage(auth.assertion(t, "webauthn.get", challenge, testPasskeyOrigin, testPasskeyRPID, handle, nil)),
	})
	if rec := h.do(http.MethodPost, "/api/passkeys/login/finish", nil, body); rec.Code != http.StatusOK {
		t.Fatalf("first sign-in: got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := h.do(http.MethodPost, "/api/passkeys/login/finish", nil, body); rec.Code != http.StatusUnauthorized {
		t.Errorf("replayed assertion: got %d, want 401", rec.Code)
	}
}

// admins.json is the authority on whether an account exists, not the credential. Without
// that check, deleting an administrator would leave a working sign-in.
func TestPasskeyOfADeletedAdminIsRefusedAndWritesNothing(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)
	h.enrol(cookie, auth)
	handle := h.handleFor(h.adminID("alice"))

	if err := h.store.Admins.Delete("alice"); err != nil {
		t.Fatal(err)
	}

	before, err := os.Stat(h.store.Passkeys.Path())
	if err != nil {
		t.Fatal(err)
	}
	if rec := h.signInWithPasskey(auth, handle); rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 (%s)", rec.Code, rec.Body.String())
	}
	after, err := os.Stat(h.store.Passkeys.Path())
	if err != nil {
		t.Fatal(err)
	}
	// An unauthenticated request must not be able to make this process rewrite a file.
	if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Error("a refused sign-in rewrote passkeys.json")
	}
}

// The reason administrators have a permanent id: a recreated username is a different
// person and inherits nothing.
func TestPasskeyDoesNotSurviveARecreatedUsername(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)
	h.enrol(cookie, auth)
	handle := h.handleFor(h.adminID("alice"))

	if err := h.store.Admins.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Admins.Create("alice", testPassword, time.Now()); err != nil {
		t.Fatal(err)
	}

	if rec := h.signInWithPasskey(auth, handle); rec.Code != http.StatusUnauthorized {
		t.Errorf("the old passkey signed in as the new alice: got %d", rec.Code)
	}
	if n := len(h.store.Passkeys.List(h.adminID("alice"))); n != 0 {
		t.Errorf("the new alice inherited %d passkeys", n)
	}
}

// A counter that goes backwards is logged, not refused. A credential synced across two
// devices can regress it without being cloned, and refusing would be an unexplained
// sign-in failure with no recovery but a shell.
//
// This test pins the behaviour so a library release that starts refusing on its own shows
// up here rather than as an operator locked out.
func TestPasskeyCloneWarningIsLoggedNotRefused(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)
	h.enrol(cookie, auth)
	handle := h.handleFor(h.adminID("alice"))

	// Sign in at a high counter, then again at a lower one.
	if rec := h.signInWithPasskey(auth.withCounter(10), handle); rec.Code != http.StatusOK {
		t.Fatalf("first sign-in: got %d (%s)", rec.Code, rec.Body.String())
	}
	rec := h.signInWithPasskey(auth.withCounter(3), handle)
	if rec.Code != http.StatusOK {
		t.Fatalf("a counter regression refused the sign-in: got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(h.logs.String(), "counter went backwards") {
		t.Error("the clone warning was not logged")
	}
}

func TestPasskeySignCountPersists(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)
	h.enrol(cookie, auth)
	handle := h.handleFor(h.adminID("alice"))

	if rec := h.signInWithPasskey(auth.withCounter(7), handle); rec.Code != http.StatusOK {
		t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
	}
	list := h.store.Passkeys.List(h.adminID("alice"))
	if len(list) != 1 || list[0].SignCount != 7 {
		t.Errorf("sign count not persisted: %+v", list)
	}
}

// The one interaction between the two credential types that is genuinely surprising, and so
// worth pinning: a passkey session dies with a password change, because it carries the same
// fingerprint an ordinary login does.
func TestPasswordChangeInvalidatesAPasskeySession(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)
	h.enrol(cookie, auth)

	rec := h.signInWithPasskey(auth, h.handleFor(h.adminID("alice")))
	passkeySession := cookieFrom(rec)
	if passkeySession == nil {
		t.Fatal("no cookie from the passkey sign-in")
	}

	if _, err := h.store.Admins.SetPassword("alice", "brandnewpassword", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := h.do(http.MethodGet, "/api/me", passkeySession, ""); got.Code != http.StatusUnauthorized {
		t.Errorf("the passkey session survived a password change: %d", got.Code)
	}
	// But the passkey itself still works, so signing back in is one tap.
	if again := h.signInWithPasskey(auth, h.handleFor(h.adminID("alice"))); again.Code != http.StatusOK {
		t.Errorf("the passkey stopped working after a password change: %d (%s)", again.Code, again.Body.String())
	}
}

// One administrator may not see or touch another's passkeys. Structural, not a check that
// has to be remembered.
func TestPasskeysAreScopedToTheirOwner(t *testing.T) {
	h := newPasskeyHarness(t)
	if _, err := h.store.Admins.Create("bob", testPassword, time.Now()); err != nil {
		t.Fatal(err)
	}
	alice := h.login()
	bob := h.loginAs("bob")

	id := h.enrol(alice, newFakeAuthenticator(t))

	var list struct {
		Passkeys []passkeyView `json:"passkeys"`
	}
	rec := h.do(http.MethodGet, "/api/passkeys", bob, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Passkeys) != 0 {
		t.Errorf("bob can see alice's passkeys: %+v", list.Passkeys)
	}

	if got := h.do(http.MethodPatch, "/api/passkeys/"+id, bob, `{"label":"mine now"}`); got.Code != http.StatusNotFound {
		t.Errorf("bob renamed alice's passkey: got %d", got.Code)
	}
	if got := h.do(http.MethodDelete, "/api/passkeys/"+id, bob, ""); got.Code != http.StatusNotFound {
		t.Errorf("bob deleted alice's passkey: got %d", got.Code)
	}
	if len(h.store.Passkeys.List(h.adminID("alice"))) != 1 {
		t.Error("alice's passkey was affected")
	}
}

func TestPasskeyRenameAndDelete(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)
	id := h.enrol(cookie, auth)

	rec := h.do(http.MethodPatch, "/api/passkeys/"+id, cookie, `{"label":"iPad"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: got %d (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Passkey passkeyView `json:"passkey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Passkey.Label != "iPad" {
		t.Errorf("label = %q", out.Passkey.Label)
	}
	if out.Passkey.Algorithm != "ES256" {
		t.Errorf("algorithm = %q, want ES256", out.Passkey.Algorithm)
	}

	// Clearing it is how a rename is undone, not a rejected edit: the credential goes back to
	// having no name of its own, and the panel calls it after its authenticator again.
	rec = h.do(http.MethodPatch, "/api/passkeys/"+id, cookie, `{"label":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clearing the label: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Passkey.Label != "" {
		t.Errorf("label = %q after clearing, want empty", out.Passkey.Label)
	}
	// A name it could never have meant is still refused.
	if bad := h.do(http.MethodPatch, "/api/passkeys/"+id, cookie, `{"label":"line\nbreak"}`); bad.Code != http.StatusBadRequest {
		t.Errorf("a control character in the label: got %d, want 400", bad.Code)
	}
	if rec := h.do(http.MethodDelete, "/api/passkeys/"+id, cookie, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d (%s)", rec.Code, rec.Body.String())
	}
	// Deleting the last one is fine: the password always works.
	if len(h.store.Passkeys.List(h.adminID("alice"))) != 0 {
		t.Error("the passkey was not removed")
	}
	// And it can no longer sign in.
	if rec := h.signInWithPasskey(auth, []byte("dead-handle-000000000000000000000")); rec.Code != http.StatusUnauthorized {
		t.Errorf("a removed passkey still signs in: %d", rec.Code)
	}
}

func TestPasskeyLoginIsRateLimited(t *testing.T) {
	h := newPasskeyHarness(t)
	var last *httptest.ResponseRecorder
	for range globalAttempts + 1 {
		last = h.do(http.MethodPost, "/api/passkeys/login/begin", nil, "")
	}
	if last.Code != http.StatusTooManyRequests {
		t.Errorf("after %d begins: got %d, want 429", globalAttempts+1, last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After on the 429")
	}
}

// None of these is a secret, but none of them belongs in a response or a log line either.
func TestPasskeyRepliesAndLogsCarryNoCredentialMaterial(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)

	bodies := []string{
		h.do(http.MethodPost, "/api/passkeys/register/begin", cookie, "").Body.String(),
	}
	id := h.enrol(cookie, auth)
	bodies = append(bodies,
		h.do(http.MethodGet, "/api/passkeys", cookie, "").Body.String(),
		h.do(http.MethodPatch, "/api/passkeys/"+id, cookie, `{"label":"iPad"}`).Body.String(),
		h.signInWithPasskey(auth, h.handleFor(h.adminID("alice"))).Body.String(),
	)

	stored := h.store.Passkeys.List(h.adminID("alice"))[0]
	handle, _ := h.store.Passkeys.Handle(h.adminID("alice"))
	secrets := map[string]string{
		"public key":    stored.PublicKey,
		"credential id": stored.CredentialID,
		"user handle":   handle,
	}
	for _, s := range append(bodies, h.logs.String()) {
		for name, secret := range secrets {
			if strings.Contains(s, secret) {
				t.Errorf("the %s appears in a response or log line", name)
			}
		}
	}
}

// The registration options have to actually ask for a discoverable credential, or
// usernameless sign-in silently does not work.
func TestPasskeyRegistrationAsksForADiscoverableCredential(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()

	rec := h.do(http.MethodPost, "/api/passkeys/register/begin", cookie, "")
	var body struct {
		Options struct {
			RP struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"rp"`
			AuthenticatorSelection struct {
				ResidentKey        string `json:"residentKey"`
				RequireResidentKey *bool  `json:"requireResidentKey"`
				UserVerification   string `json:"userVerification"`
			} `json:"authenticatorSelection"`
			Attestation string `json:"attestation"`
		} `json:"options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	o := body.Options
	if o.RP.ID != testPasskeyRPID {
		t.Errorf("rp.id = %q, want %q", o.RP.ID, testPasskeyRPID)
	}
	if o.AuthenticatorSelection.ResidentKey != "required" {
		t.Errorf("residentKey = %q, want required", o.AuthenticatorSelection.ResidentKey)
	}
	if o.AuthenticatorSelection.RequireResidentKey == nil || !*o.AuthenticatorSelection.RequireResidentKey {
		t.Error("requireResidentKey is not true")
	}
	// Preferred, not required: a passkey here is one factor, like the password beside it.
	if o.AuthenticatorSelection.UserVerification != "preferred" {
		t.Errorf("userVerification = %q, want preferred", o.AuthenticatorSelection.UserVerification)
	}
	if o.Attestation != "none" {
		t.Errorf("attestation = %q, want none", o.Attestation)
	}
}

// Re-enrolling has to exclude what the administrator already holds, or the browser silently
// creates a duplicate instead of saying "this device already has one".
func TestPasskeyRegistrationExcludesExistingCredentials(t *testing.T) {
	h := newPasskeyHarness(t)
	cookie := h.login()
	auth := newFakeAuthenticator(t)
	h.enrol(cookie, auth)

	rec := h.do(http.MethodPost, "/api/passkeys/register/begin", cookie, "")
	var body struct {
		Options struct {
			ExcludeCredentials []struct {
				ID string `json:"id"`
			} `json:"excludeCredentials"`
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Options.ExcludeCredentials) != 1 {
		t.Fatalf("excludeCredentials = %+v, want one entry", body.Options.ExcludeCredentials)
	}
	if body.Options.ExcludeCredentials[0].ID != base64.RawURLEncoding.EncodeToString(auth.credID) {
		t.Error("the excluded credential is not the enrolled one")
	}

	// And the handle is stable, so a second enrolment on the same device replaces rather
	// than duplicating.
	handle, _ := h.store.Passkeys.Handle(h.adminID("alice"))
	if body.Options.User.ID != handle {
		t.Errorf("user.id = %q, want the stored handle %q", body.Options.User.ID, handle)
	}
	// The handle must not be the administrator's id, which may be their old username.
	if body.Options.User.ID == h.adminID("alice") {
		t.Error("the user handle is the administrator id")
	}
}
