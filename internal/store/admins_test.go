package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const pw = "hunter2hunter2"

func newStore(t *testing.T) *Admins {
	t.Helper()
	a, err := OpenAdmins(filepath.Join(t.TempDir(), AdminsFile))
	if err != nil {
		t.Fatalf("OpenAdmins: %v", err)
	}
	return a
}

func TestFreshDirIsEmpty(t *testing.T) {
	a := newStore(t)
	if a.Count() != 0 {
		t.Errorf("Count = %d, want 0", a.Count())
	}
	if len(a.List()) != 0 {
		t.Errorf("List returned %d entries", len(a.List()))
	}
}

func TestCreateGetDelete(t *testing.T) {
	a := newStore(t)
	now := time.Now()

	if _, err := a.Create("alice", pw, now); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := a.Get("alice")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("Username = %q", got.Username)
	}

	// Case-insensitive, matching how login resolves a username.
	if _, err := a.Get("ALICE"); err != nil {
		t.Errorf("Get is case-sensitive: %v", err)
	}

	if err := a.Delete("alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := a.Get("alice"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete: got %v, want ErrNotFound", err)
	}
}

func TestCreateRejects(t *testing.T) {
	a := newStore(t)
	now := time.Now()
	if _, err := a.Create("alice", pw, now); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, user, pass string
		want             error
	}{
		{"duplicate", "alice", pw, ErrConflict},
		{"duplicate, other case", "ALICE", pw, ErrConflict},
		{"blank username", "  ", pw, ErrInvalid},
		{"username with a space", "al ice", pw, ErrInvalid},
		{"short password", "bob", "short", ErrInvalid},
		// bcrypt only hashes the first 72 bytes. Accepting a longer one would mean a
		// truncated version of the password also logs in.
		{"password over 72 bytes", "bob", strings.Repeat("x", 73), ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.Create(tc.user, tc.pass, now)
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}

	// Exactly 72 is fine.
	if _, err := a.Create("bob", strings.Repeat("x", 72), now); err != nil {
		t.Errorf("a 72-byte password should be accepted: %v", err)
	}
}

func TestVerify(t *testing.T) {
	a := newStore(t)
	if _, err := a.Create("alice", pw, time.Now()); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Verify("alice", pw); err != nil {
		t.Errorf("the correct password was rejected: %v", err)
	}
	if _, err := a.Verify("ALICE", pw); err != nil {
		t.Errorf("Verify is case-sensitive on the username: %v", err)
	}

	// The same error for both, so no caller can accidentally build a username oracle.
	wrong := errFrom(a.Verify("alice", "wrongwrongwrong"))
	unknown := errFrom(a.Verify("nobody", pw))
	if !errors.Is(wrong, ErrBadCredentials) || !errors.Is(unknown, ErrBadCredentials) {
		t.Errorf("wrong=%v unknown=%v, want ErrBadCredentials for both", wrong, unknown)
	}
	if wrong.Error() != unknown.Error() {
		t.Errorf("the two messages differ: %q vs %q", wrong, unknown)
	}
}

func errFrom(_ *Admin, err error) error { return err }

func TestStoredHashIsNotThePassword(t *testing.T) {
	a := newStore(t)
	if _, err := a.Create("alice", pw, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := a.Get("alice")

	if strings.Contains(got.PasswordHash, pw) {
		t.Fatal("the password appears in the stored hash")
	}
	if !strings.HasPrefix(got.PasswordHash, "$2") {
		t.Errorf("not a bcrypt hash: %q", got.PasswordHash)
	}

	// And the same on disk.
	raw, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), pw) {
		t.Fatalf("the password appears in %s", a.Path())
	}
}

func TestSetPasswordMovesTheFingerprint(t *testing.T) {
	a := newStore(t)
	if _, err := a.Create("alice", pw, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Get("alice")

	if _, err := a.SetPassword("alice", "brandnewpassword", time.Now()); err != nil {
		t.Fatal(err)
	}
	after, _ := a.Get("alice")

	// This is what invalidates live sessions across processes.
	if before.Fingerprint() == after.Fingerprint() {
		t.Error("the fingerprint did not change, so live sessions would survive")
	}
	if _, err := a.Verify("alice", "brandnewpassword"); err != nil {
		t.Errorf("the new password was rejected: %v", err)
	}
	if _, err := a.Verify("alice", pw); err == nil {
		t.Error("the old password still works")
	}
}

func TestRoundTripThroughTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AdminsFile)

	first, err := OpenAdmins(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Create("alice", pw, time.Now()); err != nil {
		t.Fatal(err)
	}

	second, err := OpenAdmins(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	if second.Count() != 1 {
		t.Fatalf("Count = %d after reopening, want 1", second.Count())
	}
	if _, err := second.Verify("alice", pw); err != nil {
		t.Errorf("the password does not verify after a round trip: %v", err)
	}
}

// TestSaveFailureLeavesMemoryUnchanged: memory and disk must never disagree. The
// sibling's stores restore the previous slice on a failed save for the same reason.
func TestSaveFailureLeavesMemoryUnchanged(t *testing.T) {
	dir := t.TempDir()
	a, err := OpenAdmins(filepath.Join(dir, AdminsFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Create("alice", pw, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make the directory read-only: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if _, err := a.Create("bob", pw, time.Now()); err == nil {
		t.Skip("the filesystem allowed the write anyway (running as root?)")
	}
	if a.Count() != 1 {
		t.Errorf("Count = %d after a failed save, want 1 — the rollback did not happen", a.Count())
	}
	if _, err := a.Get("bob"); err == nil {
		t.Error("bob is in memory but was never written to disk")
	}
}

func TestOpenRejectsBadFiles(t *testing.T) {
	tests := []struct{ name, content string }{
		{"unknown field", `{"version":1,"admins":[],"extra":true}`},
		{"future version", `{"version":3,"admins":[]}`},
		{"malformed", `{`},
		{"empty username", `{"version":1,"admins":[{"username":"","password_hash":"$2a$12$abc"}]}`},
		{"empty hash", `{"version":1,"admins":[{"username":"a","password_hash":""}]}`},
		// The obvious hand-edit mistake, which would otherwise fail every login with
		// no explanation at all.
		{"plaintext in the hash field", `{"version":1,"admins":[{"username":"a","password_hash":"hunter2"}]}`},
		{"duplicate usernames", `{"version":1,"admins":[
			{"username":"a","password_hash":"$2a$12$C6UzMDM.H6dfI/f/IKcEe.5Q0.2xUxKQ.LtWZDoO2ZQ5rZbLNFPTa"},
			{"username":"A","password_hash":"$2a$12$C6UzMDM.H6dfI/f/IKcEe.5Q0.2xUxKQ.LtWZDoO2ZQ5rZbLNFPTa"}]}`},
		// Version 2 promises every record has an id. Deriving one from the username would
		// be worse than refusing: if this id was random and somebody deleted the line, the
		// substitute would silently point that admin's sessions somewhere else.
		{"current version with no id", `{"version":2,"admins":[
			{"username":"a","password_hash":"$2a$12$C6UzMDM.H6dfI/f/IKcEe.5Q0.2xUxKQ.LtWZDoO2ZQ5rZbLNFPTa"}]}`},
		{"duplicate ids", `{"version":2,"admins":[
			{"id":"dup","username":"a","password_hash":"$2a$12$C6UzMDM.H6dfI/f/IKcEe.5Q0.2xUxKQ.LtWZDoO2ZQ5rZbLNFPTa"},
			{"id":"dup","username":"b","password_hash":"$2a$12$C6UzMDM.H6dfI/f/IKcEe.5Q0.2xUxKQ.LtWZDoO2ZQ5rZbLNFPTa"}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), AdminsFile)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			// Fatal at startup, deliberately: there is no last-known-good copy to fall
			// back on, and coming up with a silently empty admin list would lock
			// everyone out while looking like a fresh install.
			if _, err := OpenAdmins(path); err == nil {
				t.Error("want an error")
			}
		})
	}
}

// TestReloadIfChanged is how `docker exec … users add` reaches a running daemon.
func TestReloadIfChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AdminsFile)

	daemon, err := OpenAdmins(path)
	if err != nil {
		t.Fatal(err)
	}
	if daemon.Count() != 0 {
		t.Fatalf("Count = %d, want 0", daemon.Count())
	}

	// A different process writes the file.
	cli, err := OpenAdmins(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Create("alice", pw, time.Now()); err != nil {
		t.Fatal(err)
	}

	// The throttle means an immediate call does nothing; a later one picks it up.
	now := time.Now()
	if err := daemon.ReloadIfChanged(now); err != nil {
		t.Fatal(err)
	}
	if err := daemon.ReloadIfChanged(now.Add(2 * ReloadResolution)); err != nil {
		t.Fatal(err)
	}
	if daemon.Count() != 1 {
		t.Errorf("Count = %d after reload, want 1", daemon.Count())
	}
	if _, err := daemon.Verify("alice", pw); err != nil {
		t.Errorf("the reloaded admin does not verify: %v", err)
	}
}

// TestReloadKeepsTheLastGoodCopy: unlike startup, a bad file under a running server
// must not end the operator's session mid-incident.
func TestReloadKeepsTheLastGoodCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AdminsFile)

	a, err := OpenAdmins(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Create("alice", pw, time.Now()); err != nil {
		t.Fatal(err)
	}

	t.Run("corrupt", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := a.ReloadIfChanged(time.Now().Add(2 * ReloadResolution))
		if err == nil {
			t.Error("want an error for the caller to log")
		}
		if a.Count() != 1 {
			t.Errorf("Count = %d, want the previous list kept", a.Count())
		}
	})

	t.Run("missing", func(t *testing.T) {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := a.ReloadIfChanged(time.Now().Add(4 * ReloadResolution)); err != nil {
			t.Errorf("a missing file should not error: %v", err)
		}
		// Losing admins.json under a running server must not lock out everyone
		// currently working.
		if a.Count() != 1 {
			t.Errorf("Count = %d, want the in-memory list kept", a.Count())
		}
	})
}

func TestFilePermissions(t *testing.T) {
	a := newStore(t)
	if _, err := a.Create("alice", pw, time.Now()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(a.Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600 — this file holds password hashes", perm)
	}
}

// legacyFile is an admins.json as written before Admin.ID existed.
const legacyFile = `{"version":1,"admins":[
	{"username":"Alice","password_hash":"$2a$12$C6UzMDM.H6dfI/f/IKcEe.5Q0.2xUxKQ.LtWZDoO2ZQ5rZbLNFPTa","created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"},
	{"username":"bob","password_hash":"$2a$12$C6UzMDM.H6dfI/f/IKcEe.5Q0.2xUxKQ.LtWZDoO2ZQ5rZbLNFPTa","created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"}]}`

func writeLegacy(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), AdminsFile)
	if err := os.WriteFile(path, []byte(legacyFile), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A record predating ids takes its normalised username, and every process that reads the
// file agrees on that without anybody having written it back. The whole scheme rests on
// this: a random id would depend on which process wrote first.
func TestLegacyIDsAreTheUsernameAndAgreeAcrossReaders(t *testing.T) {
	path := writeLegacy(t)

	first, err := OpenAdmins(path)
	if err != nil {
		t.Fatalf("OpenAdmins: %v", err)
	}
	second, err := OpenAdmins(path)
	if err != nil {
		t.Fatalf("OpenAdmins: %v", err)
	}

	for _, a := range []*Admins{first, second} {
		alice, err := a.Get("alice")
		if err != nil {
			t.Fatal(err)
		}
		// Folded, so the id matches however the name was capitalised in the file.
		if alice.ID != "alice" {
			t.Errorf("id = %q, want %q", alice.ID, "alice")
		}
	}

	// Reading must not have written: load is called on every reload and every CLI
	// invocation, so it has no business touching the file.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != legacyFile {
		t.Error("opening the file rewrote it; only Migrate may do that")
	}
}

func TestMigrateStampsTheCurrentVersion(t *testing.T) {
	path := writeLegacy(t)
	a, err := OpenAdmins(path)
	if err != nil {
		t.Fatal(err)
	}

	n, err := a.Migrate()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if n != 2 {
		t.Errorf("Migrate reported %d records, want 2", n)
	}

	// Idempotent: a second call has nothing to do.
	if n, err := a.Migrate(); err != nil || n != 0 {
		t.Errorf("second Migrate = (%d, %v), want (0, nil)", n, err)
	}

	// And the file now says so, which is what stops a rollback from silently
	// misreading it.
	reopened, err := OpenAdmins(path)
	if err != nil {
		t.Fatalf("reopening a migrated file: %v", err)
	}
	alice, err := reopened.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	if alice.ID != "alice" {
		t.Errorf("id after migrating = %q", alice.ID)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"version": 2`) {
		t.Errorf("migrated file is not stamped version 2:\n%s", raw)
	}
}

// A new administrator gets a random id, never their username. That split is what makes a
// deleted-and-recreated name a different administrator, so nothing pointing at the old one
// can resolve to the new.
func TestCreateMintsARandomID(t *testing.T) {
	a := newStore(t)
	created, err := a.Create("alice", pw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.ID == "alice" {
		t.Fatalf("id = %q, want a random one", created.ID)
	}

	if err := a.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	again, err := a.Create("alice", pw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if again.ID == created.ID {
		t.Error("a recreated username reused the old id, so it would inherit the old identity")
	}
}

func TestSetUsername(t *testing.T) {
	a := newStore(t)
	now := time.Now()
	alice, err := a.Create("alice", pw, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Create("bob", pw, now); err != nil {
		t.Fatal(err)
	}

	renamed, err := a.SetUsername(alice.ID, "carol", now)
	if err != nil {
		t.Fatalf("SetUsername: %v", err)
	}
	if renamed.Username != "carol" {
		t.Errorf("Username = %q", renamed.Username)
	}
	// The point of the id: it does not move, so sessions and anything else naming it are
	// undisturbed. The fingerprint is unchanged too, so nobody is signed out.
	if renamed.ID != alice.ID {
		t.Errorf("id changed on rename: %q -> %q", alice.ID, renamed.ID)
	}
	if renamed.Fingerprint() != alice.Fingerprint() {
		t.Error("fingerprint changed on rename, which would sign the admin out")
	}

	// The old name is free again.
	if _, err := a.Get("alice"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old username still resolves: %v", err)
	}

	// Somebody else's name, case-folded like every other username comparison.
	if _, err := a.SetUsername(alice.ID, "BOB", now); !errors.Is(err, ErrConflict) {
		t.Errorf("renaming onto a taken name: got %v, want ErrConflict", err)
	}
	// Re-capitalising your own name is not a conflict with yourself.
	if _, err := a.SetUsername(alice.ID, "Carol", now); err != nil {
		t.Errorf("changing only the case of one's own name: %v", err)
	}
	if _, err := a.SetUsername("nosuchid", "dave", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: got %v, want ErrNotFound", err)
	}
}

func TestGetByID(t *testing.T) {
	a := newStore(t)
	alice, err := a.Create("alice", pw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.GetByID(alice.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("Username = %q", got.Username)
	}
	// Exact, not folded: nobody types an id.
	if _, err := a.GetByID(strings.ToUpper(alice.ID)); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID folded case: %v", err)
	}
}

// Two processes write this file now, and neither sees the other's lock. A write that would
// clobber an edit made underneath us is refused rather than merged.
func TestAdminsSaveRefusesToClobberAnOutsideEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AdminsFile)

	daemon, err := OpenAdmins(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.Create("alice", pw, time.Now()); err != nil {
		t.Fatal(err)
	}
	alice, err := daemon.Get("alice")
	if err != nil {
		t.Fatal(err)
	}

	// Somebody runs `users passwd` in another process.
	cli, err := OpenAdmins(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SetPassword("alice", pw+"x", time.Now()); err != nil {
		t.Fatal(err)
	}

	// The daemon's copy is now stale, so writing would lose that change.
	if _, err := daemon.SetUsername(alice.ID, "carol", time.Now()); err == nil {
		t.Fatal("want a refusal, got a write that would have clobbered the other process")
	}

	// Reload first, and it goes through.
	if err := daemon.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.SetUsername(alice.ID, "carol", time.Now()); err != nil {
		t.Errorf("after Reload: %v", err)
	}
}
