package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"vlessvmorectl/internal/session"
)

const newPassword = "brandnewpassword"

// cookieFrom pulls the session cookie out of a response, or nil when there is none.
func cookieFrom(rec interface{ Result() *http.Response }) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName {
			return c
		}
	}
	return nil
}

// A password change signs the admin out everywhere, then hands *this* request a working
// session back — otherwise the person who just typed both passwords correctly is the one
// punished for it.
func TestChangePasswordKeepsThisTabAndDropsTheRest(t *testing.T) {
	h := newHarness(t, "")
	phone := h.login()
	laptop := h.login()

	rec := h.do(http.MethodPost, "/api/account/password", laptop,
		`{"current_password":"`+testPassword+`","new_password":"`+newPassword+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204 (%s)", rec.Code, rec.Body.String())
	}

	fresh := cookieFrom(rec)
	if fresh == nil {
		t.Fatal("no replacement cookie, so the acting tab was signed out")
	}
	if fresh.Value == laptop.Value {
		t.Error("the same session id was re-sent; a credential change must mint a new one")
	}
	if got := h.do(http.MethodGet, "/api/me", fresh, ""); got.Code != http.StatusOK {
		t.Errorf("the replacement cookie does not authenticate: %d (%s)", got.Code, got.Body.String())
	}

	// Every other device is out.
	if got := h.do(http.MethodGet, "/api/me", phone, ""); got.Code != http.StatusUnauthorized {
		t.Errorf("the other session survived: %d", got.Code)
	}
	// Including the cookie that made the change.
	if got := h.do(http.MethodGet, "/api/me", laptop, ""); got.Code != http.StatusUnauthorized {
		t.Errorf("the old cookie still works: %d", got.Code)
	}

	// And the new password is the one that works now.
	if _, err := h.store.Admins.Verify("alice", newPassword); err != nil {
		t.Errorf("the new password does not verify: %v", err)
	}
	if _, err := h.store.Admins.Verify("alice", testPassword); err == nil {
		t.Error("the old password still verifies")
	}
}

func TestChangePasswordRejections(t *testing.T) {
	tests := []struct {
		name, body string
		want       int
	}{
		{"wrong current password", `{"current_password":"notitnotit","new_password":"` + newPassword + `"}`, http.StatusForbidden},
		{"new password too short", `{"current_password":"` + testPassword + `","new_password":"short"}`, http.StatusBadRequest},
		{"unknown field", `{"current_password":"x","new_password":"y","extra":1}`, http.StatusBadRequest},
		{"no body", ``, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "")
			cookie := h.login()
			rec := h.do(http.MethodPost, "/api/account/password", cookie, tc.body)
			if rec.Code != tc.want {
				t.Errorf("got %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			// A refused change leaves the old password working and the session alive.
			if _, err := h.store.Admins.Verify("alice", testPassword); err != nil {
				t.Errorf("the original password stopped working: %v", err)
			}
			if got := h.do(http.MethodGet, "/api/me", cookie, ""); got.Code != http.StatusOK {
				t.Errorf("a refused change signed the caller out: %d", got.Code)
			}
		})
	}
}

func TestChangePasswordRequiresASession(t *testing.T) {
	h := newHarness(t, "")
	rec := h.do(http.MethodPost, "/api/account/password", nil,
		`{"current_password":"`+testPassword+`","new_password":"`+newPassword+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

// Guessing at the current password is throttled: a session lifted from an unlocked laptop
// should not buy unlimited attempts, and bcrypt at cost 12 is a CPU amplifier either way.
func TestChangePasswordIsRateLimited(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()
	body := `{"current_password":"wrongwrongwrong","new_password":"` + newPassword + `"}`

	var last int
	for range perUserFailures + 1 {
		last = h.do(http.MethodPost, "/api/account/password", cookie, body).Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("after %d wrong guesses: got %d, want 429", perUserFailures+1, last)
	}
}

// The point of the permanent id: a rename is invisible to every live session, including
// the ones on other devices.
func TestChangeUsernameSignsNobodyOut(t *testing.T) {
	h := newHarness(t, "")
	phone := h.login()
	laptop := h.login()

	rec := h.do(http.MethodPost, "/api/account/username", laptop,
		`{"current_password":"`+testPassword+`","username":"carol"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct{ Username string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Username != "carol" {
		t.Errorf("response username = %q", body.Username)
	}

	// No replacement cookie, because none was needed.
	if c := cookieFrom(rec); c != nil {
		t.Error("a rename re-issued the session cookie; nothing about it changed")
	}

	// Both sessions still work, and both now report the new name — the session's own copy
	// is from login and must not be what gets reported.
	for name, cookie := range map[string]*http.Cookie{"acting tab": laptop, "other device": phone} {
		got := h.do(http.MethodGet, "/api/me", cookie, "")
		if got.Code != http.StatusOK {
			t.Fatalf("%s was signed out by a rename: %d", name, got.Code)
		}
		var me struct{ Username string }
		if err := json.Unmarshal(got.Body.Bytes(), &me); err != nil {
			t.Fatal(err)
		}
		if me.Username != "carol" {
			t.Errorf("%s still reports %q", name, me.Username)
		}
	}

	// Signing in works under the new name, and not the old one.
	if _, err := h.store.Admins.Verify("carol", testPassword); err != nil {
		t.Errorf("the new username does not verify: %v", err)
	}
	if _, err := h.store.Admins.Verify("alice", testPassword); err == nil {
		t.Error("the old username still verifies")
	}
}

func TestChangeUsernameRejections(t *testing.T) {
	h := newHarness(t, "")
	if _, err := h.store.Admins.Create("bob", testPassword, time.Now()); err != nil {
		t.Fatal(err)
	}
	cookie := h.login()

	tests := []struct {
		name, body string
		want       int
	}{
		{"wrong current password", `{"current_password":"notitnotit","username":"carol"}`, http.StatusForbidden},
		// Case-folded, matching how every other username comparison works here.
		{"taken by somebody else", `{"current_password":"` + testPassword + `","username":"BOB"}`, http.StatusConflict},
		{"blank", `{"current_password":"` + testPassword + `","username":"   "}`, http.StatusBadRequest},
		{"contains a space", `{"current_password":"` + testPassword + `","username":"al ice"}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.do(http.MethodPost, "/api/account/username", cookie, tc.body)
			if rec.Code != tc.want {
				t.Errorf("got %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// Nothing above landed.
	if _, err := h.store.Admins.Get("alice"); err != nil {
		t.Errorf("alice was renamed by a rejected request: %v", err)
	}
}

// Re-capitalising your own name is not a conflict with yourself.
func TestChangeUsernameToADifferentCaseOfItself(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()
	rec := h.do(http.MethodPost, "/api/account/username", cookie,
		`{"current_password":"`+testPassword+`","username":"Alice"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got, err := h.store.Admins.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "Alice" {
		t.Errorf("Username = %q, want %q", got.Username, "Alice")
	}
}

// Neither endpoint may echo a hash, and neither may log one.
func TestAccountRepliesCarryNoSecrets(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()

	bodies := []string{
		h.do(http.MethodPost, "/api/account/username", cookie,
			`{"current_password":"`+testPassword+`","username":"carol"}`).Body.String(),
		h.do(http.MethodPost, "/api/account/password", cookie,
			`{"current_password":"`+testPassword+`","new_password":"`+newPassword+`"}`).Body.String(),
	}
	admin, err := h.store.Admins.Get("carol")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range append(bodies, h.logs.String()) {
		for _, secret := range []string{admin.PasswordHash, testPassword, newPassword} {
			if strings.Contains(s, secret) {
				t.Errorf("a secret appears in a response or log line: %q", s)
			}
		}
	}
}
