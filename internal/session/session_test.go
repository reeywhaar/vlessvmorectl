package session

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vlessvmorectl/internal/store"
)

var fp = [8]byte{1, 2, 3, 4, 5, 6, 7, 8}

func TestIssueAndLookup(t *testing.T) {
	tbl := NewInMemory()
	now := time.Now()

	id, rec, err := tbl.Issue("alice", fp, now)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("Issue returned an empty id")
	}
	if rec.Username != "alice" || rec.Fingerprint != fp {
		t.Errorf("record: %+v", rec)
	}

	got, renewed, ok := tbl.Lookup(id, now)
	if !ok {
		t.Fatal("the freshly issued session does not resolve")
	}
	if got.Username != "alice" {
		t.Errorf("username: got %q", got.Username)
	}
	if renewed {
		t.Error("a session used immediately should not be renewed")
	}

	if _, _, ok := tbl.Lookup("not-a-session", now); ok {
		t.Error("an unknown id resolved")
	}
	if _, _, ok := tbl.Lookup("", now); ok {
		t.Error("an empty id resolved")
	}
}

// TestTheMapKeyIsNotTheReturnedID is the guard on the hashing.
//
// The table is keyed by sha256 of the id so that a heap dump, a core file or a swapped
// page never contains a value that can be replayed as a cookie. If someone later
// "simplifies" this to key by the id itself, the property is gone silently — nothing
// else would fail.
func TestTheMapKeyIsNotTheReturnedID(t *testing.T) {
	tbl := NewInMemory()
	id, _, err := tbl.Issue("alice", fp, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	tbl.mu.Lock()
	defer tbl.mu.Unlock()
	for k := range tbl.byID {
		if string(k[:]) == id {
			t.Fatal("the raw session id is the map key")
		}
		if k != sha256.Sum256([]byte(id)) {
			t.Fatal("the map key is not sha256 of the id")
		}
	}
}

func TestSlidingExpiry(t *testing.T) {
	tbl := NewInMemory()
	start := time.Now()

	id, _, err := tbl.Issue("alice", fp, start)
	if err != nil {
		t.Fatal(err)
	}

	// Inside the refresh window: still valid, but the record is left alone so the
	// browser is not handed a new Set-Cookie on every poll.
	at := start.Add(Refresh / 2)
	if _, renewed, ok := tbl.Lookup(id, at); !ok || renewed {
		t.Errorf("inside the refresh window: ok=%v renewed=%v, want true/false", ok, renewed)
	}

	// Past it: renewed, and the deadline moves.
	at = start.Add(Refresh + time.Minute)
	rec, renewed, ok := tbl.Lookup(id, at)
	if !ok || !renewed {
		t.Fatalf("past the refresh window: ok=%v renewed=%v, want true/true", ok, renewed)
	}
	if !rec.ExpiresAt.After(start.Add(Idle)) {
		t.Errorf("the deadline did not slide: %v", rec.ExpiresAt)
	}

	// Nine days of steady use keeps it alive well past the original ten-day mark.
	last := at
	for range 9 {
		last = last.Add(24 * time.Hour)
		if _, _, ok := tbl.Lookup(id, last); !ok {
			t.Fatalf("the session expired while in daily use, at %v", last)
		}
	}

	// Ten days of silence ends it.
	if _, _, ok := tbl.Lookup(id, last.Add(Idle+time.Minute)); ok {
		t.Error("an idle session outlived its window")
	}
}

func TestIdleIsTenDays(t *testing.T) {
	if Idle != 10*24*time.Hour {
		t.Errorf("Idle = %v, want 240h", Idle)
	}
}

func TestDeleteAndSweep(t *testing.T) {
	tbl := NewInMemory()
	now := time.Now()

	id, _, _ := tbl.Issue("alice", fp, now)
	tbl.Delete(id)
	if _, _, ok := tbl.Lookup(id, now); ok {
		t.Error("a deleted session still resolves")
	}

	a, _, _ := tbl.Issue("alice", fp, now)
	b, _, _ := tbl.Issue("bob", fp, now)
	if n := tbl.Sweep(now.Add(Idle + time.Hour)); n != 2 {
		t.Errorf("Sweep removed %d, want 2", n)
	}
	if tbl.Len() != 0 {
		t.Errorf("Len = %d after a full sweep, want 0", tbl.Len())
	}
	if _, _, ok := tbl.Lookup(a, now); ok {
		t.Error("a swept session resolves")
	}
	_ = b
}

func TestDeleteUser(t *testing.T) {
	tbl := NewInMemory()
	now := time.Now()

	a1, _, _ := tbl.Issue("alice", fp, now)
	a2, _, _ := tbl.Issue("alice", fp, now)
	b1, _, _ := tbl.Issue("bob", fp, now)

	if n := tbl.DeleteUser("alice"); n != 2 {
		t.Errorf("DeleteUser removed %d, want 2", n)
	}
	for _, id := range []string{a1, a2} {
		if _, _, ok := tbl.Lookup(id, now); ok {
			t.Error("one of alice's sessions survived")
		}
	}
	if _, _, ok := tbl.Lookup(b1, now); !ok {
		t.Error("bob's session was removed too")
	}
}

func TestMaxSessionsEvictsOldest(t *testing.T) {
	tbl := NewInMemory()
	base := time.Now()

	first, _, _ := tbl.Issue("alice", fp, base)
	for i := 1; i < MaxSessions; i++ {
		tbl.Issue("alice", fp, base.Add(time.Duration(i)*time.Second))
	}
	if tbl.Len() != MaxSessions {
		t.Fatalf("Len = %d, want %d", tbl.Len(), MaxSessions)
	}

	tbl.Issue("alice", fp, base.Add(time.Hour))
	if tbl.Len() > MaxSessions {
		t.Errorf("Len = %d, want at most %d", tbl.Len(), MaxSessions)
	}
	if _, _, ok := tbl.Lookup(first, base.Add(time.Hour)); ok {
		t.Error("the oldest session was not the one evicted")
	}
}

// TestIDsAreUnique catches a broken random source, which would be catastrophic and
// otherwise invisible.
func TestIDsAreUnique(t *testing.T) {
	tbl := NewInMemory()
	now := time.Now()
	seen := make(map[string]bool, 1000)
	for range 1000 {
		id, _, err := tbl.Issue("alice", fp, now)
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("duplicate session id: %s", id)
		}
		seen[id] = true
	}
}

// TestLookupReturnsACopy: a caller holding the returned pointer must not be able to
// mutate the live record after the lock has been released.
func TestLookupReturnsACopy(t *testing.T) {
	tbl := NewInMemory()
	now := time.Now()
	id, _, _ := tbl.Issue("alice", fp, now)

	got, _, _ := tbl.Lookup(id, now)
	got.Username = "mallory"

	again, _, _ := tbl.Lookup(id, now)
	if again.Username != "alice" {
		t.Errorf("the stored record was mutated through a returned pointer: %q", again.Username)
	}
}

func TestConcurrentAccess(t *testing.T) {
	tbl := NewInMemory()
	now := time.Now()

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(3)
		go func() { defer wg.Done(); tbl.Issue("alice", fp, now) }()
		go func() { defer wg.Done(); tbl.Lookup("whatever", now) }()
		go func() { defer wg.Done(); tbl.Sweep(now) }()
	}
	wg.Wait()
}

// TestSurvivesARestart is the behaviour this persistence exists for: `docker compose
// restart` must not sign every operator out.
func TestSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Now()

	before := New(store.OpenSessionFile(dir), log)
	id, _, err := before.Issue("alice", fp, now)
	if err != nil {
		t.Fatal(err)
	}

	// A new process against the same data directory.
	after := New(store.OpenSessionFile(dir), log)
	rec, _, ok := after.Lookup(id, now.Add(time.Minute))
	if !ok {
		t.Fatal("the session did not survive the restart")
	}
	if rec.Username != "alice" || rec.Fingerprint != fp {
		t.Errorf("restored record: %+v", rec)
	}
}

// TestPersistsOnlyHashes is the property that makes writing this file acceptable at all.
//
// The raw cookie value must never reach disk. If it did, sessions.json would be a list
// of replayable credentials rather than a list of digests, and the whole justification
// for persisting collapses.
func TestPersistsOnlyHashes(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	tbl := New(store.OpenSessionFile(dir), log)
	id, _, err := tbl.Issue("alice", fp, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, store.SessionsFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), id) {
		t.Fatalf("the raw session id was written to disk:\n%s", raw)
	}
	if !strings.Contains(string(raw), hex.EncodeToString(sha256Sum(id))) {
		t.Errorf("the hash is not in the file:\n%s", raw)
	}
	if perm := mustStat(t, filepath.Join(dir, store.SessionsFile)); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}

func sha256Sum(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func mustStat(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// TestLogoutSurvivesARestart: a revoked session must not come back from the dead.
func TestLogoutSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Now()

	before := New(store.OpenSessionFile(dir), log)
	id, _, _ := before.Issue("alice", fp, now)
	before.Delete(id)

	after := New(store.OpenSessionFile(dir), log)
	if _, _, ok := after.Lookup(id, now); ok {
		t.Error("a logged-out session was restored")
	}
}

// TestExpiredSessionsAreNotRestored: no point loading something the janitor would
// immediately sweep, and it keeps the file from growing without bound.
func TestExpiredSessionsAreNotRestored(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	long := time.Now().Add(-Idle - time.Hour)

	before := New(store.OpenSessionFile(dir), log)
	id, _, _ := before.Issue("alice", fp, long)

	after := New(store.OpenSessionFile(dir), log)
	if after.Len() != 0 {
		t.Errorf("Len = %d, want 0", after.Len())
	}
	if _, _, ok := after.Lookup(id, time.Now()); ok {
		t.Error("an expired session was restored")
	}
}

// TestCorruptFileDoesNotPreventStartup: losing sessions signs people out, which is
// recoverable. Refusing to start is not the right trade for a service whose job is to be
// reachable when something else has gone wrong.
func TestCorruptFileDoesNotPreventStartup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, store.SessionsFile), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	tbl := New(store.OpenSessionFile(dir), log)
	if tbl.Len() != 0 {
		t.Errorf("Len = %d, want 0", tbl.Len())
	}
	// And it still works from there.
	if _, _, err := tbl.Issue("alice", fp, time.Now()); err != nil {
		t.Errorf("could not issue after a corrupt load: %v", err)
	}
}
