package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newSubscribers(t *testing.T) *Subscribers {
	t.Helper()
	s, err := OpenSubscribers(filepath.Join(t.TempDir(), SubscribersFile), true)
	if err != nil {
		t.Fatalf("OpenSubscribers: %v", err)
	}
	return s
}

func TestSubscribersFreshDirIsEmpty(t *testing.T) {
	s := newSubscribers(t)
	if s.Count() != 0 {
		t.Errorf("Count = %d, want 0 — a missing file is an empty list", s.Count())
	}
	if _, err := s.GetByToken("QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByToken on an empty store: %v, want ErrNotFound", err)
	}
}

func TestSubscribersCreateGetDelete(t *testing.T) {
	s := newSubscribers(t)
	now := time.Now()

	sub, err := s.Create("Ivan", "paid to August", now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sub.Name != "Ivan" || sub.Note != "paid to August" {
		t.Errorf("round trip lost fields: %+v", sub)
	}
	if sub.Entries == nil {
		t.Error("Entries should be an empty slice, not nil, so it marshals as []")
	}

	got, err := s.Get(sub.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Token != sub.Token {
		t.Error("Get returned a different token")
	}

	if err := s.Delete(sub.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(sub.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: %v, want ErrNotFound", err)
	}
	if _, err := s.GetByToken(sub.Token); !errors.Is(err, ErrNotFound) {
		t.Error("a deleted subscriber's token should stop resolving")
	}
}

func TestShareTokenShape(t *testing.T) {
	s := newSubscribers(t)
	seen := map[string]bool{}
	for i := range 25 {
		sub, err := s.Create("person", "", time.Now())
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if len(sub.Token) != 32 {
			t.Errorf("token %q is %d characters, want 32", sub.Token, len(sub.Token))
		}
		if !isCrockford(sub.Token) {
			t.Errorf("token %q leaves the share-token alphabet", sub.Token)
		}
		// The alphabet choice is load-bearing twice over: a dot would make the SPA's
		// navigation check treat the link as a file request, and a slash would break
		// route matching outright.
		if strings.ContainsAny(sub.Token, "./") {
			t.Errorf("token %q contains a character that breaks routing", sub.Token)
		}
		if seen[sub.Token] {
			t.Fatalf("token %q was minted twice", sub.Token)
		}
		seen[sub.Token] = true
	}
}

func TestSubscriberIDIsShortAndLowercase(t *testing.T) {
	s := newSubscribers(t)
	sub, err := s.Create("Ivan", "", time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sub.ID != strings.ToLower(sub.ID) {
		t.Errorf("id %q should be lowercase — it sits in a URL bar next to lowercase paths", sub.ID)
	}
	if len(sub.ID) != 8 {
		t.Errorf("id %q is %d characters, want 8", sub.ID, len(sub.ID))
	}
	if sub.ID == sub.Token {
		t.Error("the id and the token must be different values: one is safe to display, one is not")
	}
}

// Two subscribers created with identical inputs must not share a token — i.e. the token
// is not a function of anything observable.
func TestTokenIsNotDerivedFromTheInputs(t *testing.T) {
	s := newSubscribers(t)
	now := time.Unix(1750000000, 0)
	a, err := s.Create("Ivan", "note", now)
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	b, err := s.Create("Ivan", "note", now)
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	if a.Token == b.Token {
		t.Fatal("identical inputs produced identical tokens")
	}
	if a.ID == b.ID {
		t.Fatal("identical inputs produced identical ids")
	}
}

func TestGetByTokenNormalisesCase(t *testing.T) {
	s := newSubscribers(t)
	sub, _ := s.Create("Ivan", "", time.Now())

	// A messenger that lowercases a pasted link must not break it.
	got, err := s.GetByToken(strings.ToLower(sub.Token))
	if err != nil {
		t.Fatalf("lowercase token: %v", err)
	}
	if got.ID != sub.ID {
		t.Error("resolved the wrong subscriber")
	}
	if _, err := s.GetByToken("  " + sub.Token + "  "); err != nil {
		t.Errorf("surrounding whitespace should be tolerated: %v", err)
	}
	if _, err := s.GetByToken(""); !errors.Is(err, ErrNotFound) {
		t.Error("an empty token must never resolve")
	}
	if _, err := s.GetByToken("short"); !errors.Is(err, ErrNotFound) {
		t.Error("a malformed token must not resolve")
	}
}

// The highest-value load check: two records sharing a token is one link resolving to two
// people's accounts.
func TestOpenRejectsDuplicateTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SubscribersFile)
	const tok = "QK7M2XA9TESTTKEN0123456789ABCDEF"
	body := `{"version":1,"subscribers":[
		{"id":"aaaaaaaa","name":"A","token":"` + tok + `","entries":[],
		 "created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
		{"id":"bbbbbbbb","name":"B","token":"` + tok + `","entries":[],
		 "created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenSubscribers(path, true)
	if err == nil {
		t.Fatal("a file with two subscribers sharing a token must not load")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Errorf("the error should name the problem, got %v", err)
	}
}

func TestOpenSubscribersRejectsBadFiles(t *testing.T) {
	const stamps = `"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"`
	const goodToken = "QK7M2XA9TESTTKEN0123456789ABCDEF"

	cases := map[string]string{
		"future version":  `{"version":99,"subscribers":[]}`,
		"unknown field":   `{"version":1,"subscribers":[],"extra":1}`,
		"malformed":       `{"version":1,`,
		"empty id":        `{"version":1,"subscribers":[{"id":"","name":"A","token":"` + goodToken + `","entries":[],` + stamps + `}]}`,
		"duplicate id":    `{"version":1,"subscribers":[{"id":"a","name":"A","token":"` + goodToken + `","entries":[],` + stamps + `},{"id":"a","name":"B","token":"ZK7M2XA9TESTTKEN0123456789ABCDEF","entries":[],` + stamps + `}]}`,
		"empty name":      `{"version":1,"subscribers":[{"id":"a","name":"","token":"` + goodToken + `","entries":[],` + stamps + `}]}`,
		"short token":     `{"version":1,"subscribers":[{"id":"a","name":"A","token":"test","entries":[],` + stamps + `}]}`,
		"token off alpha": `{"version":1,"subscribers":[{"id":"a","name":"A","token":"qk7m2xa9-testtoken-0123456789abc","entries":[],` + stamps + `}]}`,
		"duplicate entry pair": `{"version":1,"subscribers":[{"id":"a","name":"A","token":"` + goodToken + `","entries":[` +
			`{"id":"e1","server_id":"s1","vless_user_id":"u1","added_at":"2026-01-01T00:00:00Z"},` +
			`{"id":"e2","server_id":"s1","vless_user_id":"u1","added_at":"2026-01-01T00:00:00Z"}],` + stamps + `}]}`,
		"duplicate entry id": `{"version":1,"subscribers":[{"id":"a","name":"A","token":"` + goodToken + `","entries":[` +
			`{"id":"e1","server_id":"s1","vless_user_id":"u1","added_at":"2026-01-01T00:00:00Z"},` +
			`{"id":"e1","server_id":"s2","vless_user_id":"u2","added_at":"2026-01-01T00:00:00Z"}],` + stamps + `}]}`,
		"entry id with a slash": `{"version":1,"subscribers":[{"id":"a","name":"A","token":"` + goodToken + `","entries":[` +
			`{"id":"e1","server_id":"s1","vless_user_id":"../../admin","added_at":"2026-01-01T00:00:00Z"}],` + stamps + `}]}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), SubscribersFile)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenSubscribers(path, true); err == nil {
				t.Error("loaded a file that should have been refused")
			}
		})
	}
}

// The server list is environment. An operator who comments out a node for an afternoon
// must not find the panel refusing to start.
func TestOpenToleratesDanglingServerReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), SubscribersFile)
	body := `{"version":1,"subscribers":[{"id":"aaaaaaaa","name":"A",
		"token":"QK7M2XA9TESTTKEN0123456789ABCDEF","entries":[
		{"id":"e1","server_id":"a-node-that-is-gone","vless_user_id":"u1",
		 "added_at":"2026-01-01T00:00:00Z"}],
		"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSubscribers(path, true)
	if err != nil {
		t.Fatalf("a dangling reference must load fine: %v", err)
	}
	if s.Count() != 1 {
		t.Errorf("Count = %d, want 1", s.Count())
	}
}

// The CLI/daemon split, made executable. A future `subscribers rm` written without
// noticing would otherwise corrupt data while passing its own tests.
func TestReadOnlyStoreRefusesEveryMutator(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SubscribersFile)

	// Seed through a writable handle, then reopen read-only, as the CLI would.
	seed, err := OpenSubscribers(path, true)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := seed.Create("Ivan", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	ro, err := OpenSubscribers(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if ro.Writable() {
		t.Fatal("this handle should not be writable")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	mutators := map[string]error{
		"Create": func() error { _, err := ro.Create("Mallory", "", now); return err }(),
		"Update": func() error {
			_, err := ro.Update(sub.ID, SubscriberUpdate{Name: strPtr("X")}, now)
			return err
		}(),
		"Delete": ro.Delete(sub.ID),
		"Attach": func() error {
			_, err := ro.Attach(sub.ID, NewEntry{ServerID: "s", VlessUserID: "u"}, now)
			return err
		}(),
		"UpdateEntry": func() error { _, err := ro.UpdateEntry(sub.ID, "e", "x", now); return err }(),
		"Detach":      func() error { _, err := ro.Detach(sub.ID, "e", now); return err }(),
	}
	for name, err := range mutators {
		if !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s returned %v, want ErrReadOnly", name, err)
		}
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a read-only handle wrote to the file")
	}
	// Reading still works, which is the point of giving the CLI a handle at all.
	if _, err := ro.GetByToken(sub.Token); err != nil {
		t.Errorf("a read-only handle should still resolve tokens: %v", err)
	}
}

// The daemon holds the authoritative list in memory, so an outside edit would otherwise
// be silently rewritten by the next save.
func TestSaveRefusesToClobberAnOutsideEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SubscribersFile)
	s, err := OpenSubscribers(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Ivan", "", time.Now()); err != nil {
		t.Fatal(err)
	}

	// Somebody edits the file by hand. Sleep past the filesystem's mtime resolution so
	// the change is actually detectable.
	time.Sleep(10 * time.Millisecond)
	outside := `{"version":1,"subscribers":[]}`
	if err := os.WriteFile(path, []byte(outside), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = s.Create("Mallory", "", time.Now())
	if err == nil {
		t.Fatal("a save over an outside edit should be refused")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Errorf("the error should say what happened, got %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != outside {
		t.Error("the outside edit was overwritten anyway")
	}
}

func TestSubscribersFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SubscribersFile)
	s, err := OpenSubscribers(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Ivan", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600 — this file holds share tokens", perm)
	}
}

func TestAttachDetachAndTheEntryCap(t *testing.T) {
	s := newSubscribers(t)
	now := time.Now()
	sub, _ := s.Create("Ivan", "", now)

	sub, err := s.Attach(sub.ID, NewEntry{ServerID: "s1", VlessUserID: "u1", Label: "phone"}, now)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(sub.Entries) != 1 || sub.Entries[0].Label != "phone" {
		t.Fatalf("unexpected entries: %+v", sub.Entries)
	}
	entryID := sub.Entries[0].ID

	if _, err := s.Attach(sub.ID, NewEntry{ServerID: "s1", VlessUserID: "u1"}, now); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate attach: %v, want ErrConflict", err)
	}

	sub, err = s.UpdateEntry(sub.ID, entryID, "the laptop", now)
	if err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	if sub.Entries[0].Label != "the laptop" {
		t.Errorf("label = %q", sub.Entries[0].Label)
	}
	if _, err := s.UpdateEntry(sub.ID, "nope", "x", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateEntry on an unknown entry: %v, want ErrNotFound", err)
	}

	// Fill to the cap, which is what bounds the fan-out of an unauthenticated endpoint.
	for i := 1; i < MaxEntriesPerSubscriber; i++ {
		if _, err := s.Attach(sub.ID, NewEntry{ServerID: "s1", VlessUserID: userID(i)}, now); err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
	}
	if _, err := s.Attach(sub.ID, NewEntry{ServerID: "s1", VlessUserID: "one-too-many"}, now); !errors.Is(err, ErrInvalid) {
		t.Errorf("past the cap: %v, want ErrInvalid", err)
	}

	if _, err := s.Detach(sub.ID, entryID, now); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if _, err := s.Detach(sub.ID, entryID, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Detach: %v, want ErrNotFound", err)
	}
}

func TestUpdatePatchSemantics(t *testing.T) {
	s := newSubscribers(t)
	now := time.Now()
	sub, _ := s.Create("Ivan", "a note", now)

	// A nil field leaves the value alone.
	got, err := s.Update(sub.ID, SubscriberUpdate{Name: strPtr("Ivan P.")}, now)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name != "Ivan P." || got.Note != "a note" {
		t.Errorf("name=%q note=%q", got.Name, got.Note)
	}

	// An empty string clears.
	got, _ = s.Update(sub.ID, SubscriberUpdate{Note: strPtr("")}, now)
	if got.Note != "" {
		t.Errorf("note = %q, want cleared", got.Note)
	}

	got, _ = s.Update(sub.ID, SubscriberUpdate{Disabled: boolPtr(true)}, now)
	if !got.Disabled {
		t.Error("Disabled did not take")
	}

	// Nothing in the patch surface may reach the token.
	if got.Token != sub.Token {
		t.Error("an update changed the share token; it is minted once and never rotated")
	}

	if _, err := s.Update(sub.ID, SubscriberUpdate{Name: strPtr("  ")}, now); !errors.Is(err, ErrInvalid) {
		t.Error("a blank name should be refused")
	}
}

func TestSubscribersRoundTripThroughTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SubscribersFile)

	s, err := OpenSubscribers(path, true)
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := s.Create("Ivan", "note", time.Now())
	if _, err := s.Attach(sub.ID, NewEntry{ServerID: "s1", VlessUserID: "u1", Label: "phone"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSubscribers(path, true)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := reopened.GetByToken(sub.Token)
	if err != nil {
		t.Fatalf("the token should still resolve after a restart: %v", err)
	}
	if got.Name != "Ivan" || len(got.Entries) != 1 || got.Entries[0].Label != "phone" {
		t.Errorf("round trip lost data: %+v", got)
	}
}

// A caller must not be able to reach into the store through a returned slice.
func TestListReturnsACopy(t *testing.T) {
	s := newSubscribers(t)
	sub, _ := s.Create("Ivan", "", time.Now())
	if _, err := s.Attach(sub.ID, NewEntry{ServerID: "s1", VlessUserID: "u1"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	list := s.List()
	list[0].Name = "Mallory"
	list[0].Entries[0].VlessUserID = "u_hacked"

	got, _ := s.Get(sub.ID)
	if got.Name != "Ivan" || got.Entries[0].VlessUserID != "u1" {
		t.Errorf("mutating the returned slice reached the store: %+v", got)
	}
}

func TestListIsSortedByName(t *testing.T) {
	s := newSubscribers(t)
	for _, name := range []string{"Zoe", "alice", "Bob"} {
		if _, err := s.Create(name, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	got := s.List()
	want := []string{"alice", "Bob", "Zoe"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("position %d = %q, want %q", i, got[i].Name, w)
		}
	}
}

func TestTokenFingerprintIsStableAndNotTheToken(t *testing.T) {
	const tok = "QK7M2XA9TESTTKEN0123456789ABCDEF"
	fp := TokenFingerprint(tok)
	if len(fp) != 8 {
		t.Errorf("fingerprint %q is %d characters, want 8", fp, len(fp))
	}
	if strings.Contains(tok, fp) {
		t.Error("the fingerprint should not be a substring of the token")
	}
	// Derivable by an operator holding the token, including one they pasted in lowercase.
	if TokenFingerprint(strings.ToLower(tok)) != fp {
		t.Error("the fingerprint should survive case normalisation, as GetByToken does")
	}
	if TokenFingerprint("ZK7M2XA9TESTTKEN0123456789ABCDEF") == fp {
		t.Error("two tokens should not share a fingerprint")
	}
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func userID(i int) string { return "u" + string(rune('a'+i%26)) + string(rune('a'+i/26)) }
