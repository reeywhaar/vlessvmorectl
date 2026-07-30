package store

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const adminA = "k7m2xa9v"
const adminB = "q4n8ptz3"

func newPasskeys(t *testing.T) *Passkeys {
	t.Helper()
	p, err := OpenPasskeys(filepath.Join(t.TempDir(), PasskeysFile), true)
	if err != nil {
		t.Fatalf("OpenPasskeys: %v", err)
	}
	return p
}

// b64 makes a plausible base64url blob of n bytes, distinguished by seed.
func b64(seed byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func aCredential(seed byte, label string) PasskeyCredential {
	return PasskeyCredential{
		CredentialID:    b64(seed, 32),
		Label:           label,
		PublicKey:       b64(seed+1, 77),
		Algorithm:       -7,
		AttestationType: "none",
		Transports:      []string{"internal"},
	}
}

func mustHandle(t *testing.T, p *Passkeys) string {
	t.Helper()
	h, err := p.NewHandle()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPasskeysAddListRemove(t *testing.T) {
	p := newPasskeys(t)
	now := time.Now()
	h := mustHandle(t, p)

	added, err := p.Add(adminA, h, aCredential(1, "iPhone"), now)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.ID == "" {
		t.Error("no panel id was minted")
	}
	if added.CreatedAt.IsZero() {
		t.Error("created_at was not stamped")
	}

	got := p.List(adminA)
	if len(got) != 1 || got[0].Label != "iPhone" {
		t.Fatalf("List = %+v", got)
	}
	// Scoped per administrator, so one cannot see another's.
	if len(p.List(adminB)) != 0 {
		t.Error("another administrator's list is not empty")
	}
	if p.Count() != 1 {
		t.Errorf("Count = %d, want 1", p.Count())
	}

	if err := p.Remove(adminA, added.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if p.Count() != 0 {
		t.Errorf("Count = %d after Remove, want 0", p.Count())
	}
	// Removing the last credential takes the owner entry with it, so no handle lingers.
	if _, ok := p.Handle(adminA); ok {
		t.Error("the owner entry survived its last credential")
	}
}

// The handle has to be stable per administrator, or an authenticator asked to enrol a
// second time adds a duplicate keychain entry instead of replacing the first.
func TestPasskeysHandleIsStablePerAdmin(t *testing.T) {
	p := newPasskeys(t)
	now := time.Now()
	h := mustHandle(t, p)

	if _, err := p.Add(adminA, h, aCredential(1, "iPhone"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Add(adminA, h, aCredential(9, "Laptop"), now); err != nil {
		t.Fatalf("second Add with the same handle: %v", err)
	}
	if len(p.List(adminA)) != 2 {
		t.Fatalf("List has %d credentials, want 2", len(p.List(adminA)))
	}

	// A different handle means two first-time enrolments raced. Refused, because the
	// authenticator has already stored whichever one it was given.
	other := mustHandle(t, p)
	if _, err := p.Add(adminA, other, aCredential(20, "Spare"), now); !errors.Is(err, ErrConflict) {
		t.Errorf("Add with a mismatched handle: got %v, want ErrConflict", err)
	}
}

func TestPasskeysRejectsADuplicateAuthenticator(t *testing.T) {
	p := newPasskeys(t)
	now := time.Now()
	if _, err := p.Add(adminA, mustHandle(t, p), aCredential(1, "iPhone"), now); err != nil {
		t.Fatal(err)
	}
	// Same credential id, different administrator: still a duplicate, because the lookup
	// on the login path is by credential id across the whole file.
	if _, err := p.Add(adminB, mustHandle(t, p), aCredential(1, "Theirs"), now); !errors.Is(err, ErrConflict) {
		t.Errorf("got %v, want ErrConflict", err)
	}
}

func TestPasskeysPerAdminCap(t *testing.T) {
	p := newPasskeys(t)
	now := time.Now()
	h := mustHandle(t, p)
	for i := range MaxPasskeysPerAdmin {
		if _, err := p.Add(adminA, h, aCredential(byte(10*i+1), "key"), now); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	_, err := p.Add(adminA, h, aCredential(250, "one too many"), now)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("past the cap: got %v, want ErrInvalid", err)
	}
}

func TestPasskeysLookups(t *testing.T) {
	p := newPasskeys(t)
	now := time.Now()
	h := mustHandle(t, p)
	cred := aCredential(1, "iPhone")
	if _, err := p.Add(adminA, h, cred, now); err != nil {
		t.Fatal(err)
	}

	owner, ok := p.ByHandle(h)
	if !ok || owner.AdminID != adminA {
		t.Fatalf("ByHandle = %+v, %v", owner, ok)
	}
	owner, got, ok := p.ByCredentialID(cred.CredentialID)
	if !ok || owner.AdminID != adminA || got.Label != "iPhone" {
		t.Fatalf("ByCredentialID = %+v, %+v, %v", owner, got, ok)
	}
	if _, ok := p.ByHandle("nosuchhandle"); ok {
		t.Error("ByHandle resolved an unknown handle")
	}
	if _, _, ok := p.ByCredentialID("nosuchcred"); ok {
		t.Error("ByCredentialID resolved an unknown credential")
	}
}

func TestPasskeysRenameAndScope(t *testing.T) {
	p := newPasskeys(t)
	now := time.Now()
	added, err := p.Add(adminA, mustHandle(t, p), aCredential(1, "iPhone"), now)
	if err != nil {
		t.Fatal(err)
	}

	renamed, err := p.Rename(adminA, added.ID, "  iPad  ", now)
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if renamed.Label != "iPad" {
		t.Errorf("Label = %q, want %q (trimmed)", renamed.Label, "iPad")
	}

	// Authorization is structural: the wrong administrator simply does not find it.
	if _, err := p.Rename(adminB, added.ID, "theirs", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-admin Rename: got %v, want ErrNotFound", err)
	}
	if err := p.Remove(adminB, added.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-admin Remove: got %v, want ErrNotFound", err)
	}
	if len(p.List(adminA)) != 1 {
		t.Error("the credential was removed by the wrong administrator")
	}
}

func TestPasskeysLabelValidation(t *testing.T) {
	p := newPasskeys(t)
	now := time.Now()
	for _, label := range []string{"", "   ", strings.Repeat("x", MaxPasskeyLabelLen+1), "bad\x00name", "line\nbreak"} {
		if _, err := p.Add(adminA, mustHandle(t, p), aCredential(1, label), now); !errors.Is(err, ErrInvalid) {
			t.Errorf("Add(label=%q): got %v, want ErrInvalid", label, err)
		}
	}
	if _, err := p.Add(adminA, mustHandle(t, p), aCredential(1, strings.Repeat("x", MaxPasskeyLabelLen)), now); err != nil {
		t.Errorf("a label at exactly the cap was refused: %v", err)
	}
}

func TestPasskeysRecordUse(t *testing.T) {
	p := newPasskeys(t)
	now := time.Now()
	cred := aCredential(1, "iPhone")
	if _, err := p.Add(adminA, mustHandle(t, p), cred, now); err != nil {
		t.Fatal(err)
	}
	if got := p.List(adminA)[0]; !got.LastUsedAt.IsZero() {
		t.Error("last_used_at is set before the credential has been used")
	}

	later := now.Add(time.Hour)
	if err := p.RecordUse(cred.CredentialID, 42, true, later); err != nil {
		t.Fatalf("RecordUse: %v", err)
	}
	got := p.List(adminA)[0]
	if got.SignCount != 42 {
		t.Errorf("SignCount = %d, want 42", got.SignCount)
	}
	if !got.BackupState {
		t.Error("BackupState was not updated")
	}
	if got.LastUsedAt.IsZero() {
		t.Error("last_used_at was not stamped")
	}

	// And it survives a reopen, which is the point of writing it down.
	reopened, err := OpenPasskeys(p.Path(), true)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.List(adminA)[0].SignCount != 42 {
		t.Error("the counter did not survive a round trip")
	}
}

func TestPasskeysPruneOrphans(t *testing.T) {
	p := newPasskeys(t)
	now := time.Now()
	if _, err := p.Add(adminA, mustHandle(t, p), aCredential(1, "mine"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Add(adminB, mustHandle(t, p), aCredential(30, "theirs"), now); err != nil {
		t.Fatal(err)
	}

	dropped, err := p.PruneOrphans(func(id string) bool { return id == adminA })
	if err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}
	if dropped != 1 {
		t.Errorf("dropped %d, want 1", dropped)
	}
	if len(p.List(adminB)) != 0 {
		t.Error("the orphan survived")
	}
	if len(p.List(adminA)) != 1 {
		t.Error("a live administrator's credential was pruned")
	}

	// Idempotent, and it must not rewrite the file when there is nothing to do.
	before, err := os.Stat(p.Path())
	if err != nil {
		t.Fatal(err)
	}
	if dropped, err := p.PruneOrphans(func(id string) bool { return id == adminA }); err != nil || dropped != 0 {
		t.Errorf("second PruneOrphans = (%d, %v), want (0, nil)", dropped, err)
	}
	after, err := os.Stat(p.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("a no-op prune rewrote the file")
	}
}

// The CLI's handle is read-only, so a second writer cannot exist by construction.
func TestPasskeysReadOnlyRefusesWrites(t *testing.T) {
	dir := t.TempDir()
	writable, err := OpenPasskeys(filepath.Join(dir, PasskeysFile), true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	added, err := writable.Add(adminA, mustHandle(t, writable), aCredential(1, "iPhone"), now)
	if err != nil {
		t.Fatal(err)
	}

	ro, err := OpenPasskeys(filepath.Join(dir, PasskeysFile), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ro.List(adminA)) != 1 {
		t.Fatal("the read-only handle cannot see the credential")
	}
	if _, err := ro.Add(adminB, mustHandle(t, ro), aCredential(30, "x"), now); !errors.Is(err, ErrPasskeysReadOnly) {
		t.Errorf("Add: got %v, want ErrPasskeysReadOnly", err)
	}
	if _, err := ro.Rename(adminA, added.ID, "x", now); !errors.Is(err, ErrPasskeysReadOnly) {
		t.Errorf("Rename: got %v, want ErrPasskeysReadOnly", err)
	}
	if err := ro.Remove(adminA, added.ID); !errors.Is(err, ErrPasskeysReadOnly) {
		t.Errorf("Remove: got %v, want ErrPasskeysReadOnly", err)
	}
	if err := ro.RecordUse(aCredential(1, "x").CredentialID, 1, false, now); !errors.Is(err, ErrPasskeysReadOnly) {
		t.Errorf("RecordUse: got %v, want ErrPasskeysReadOnly", err)
	}
	if _, err := ro.PruneOrphans(func(string) bool { return false }); !errors.Is(err, ErrPasskeysReadOnly) {
		t.Errorf("PruneOrphans: got %v, want ErrPasskeysReadOnly", err)
	}
}

func TestPasskeysSaveRefusesToClobberAnOutsideEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PasskeysFile)
	p, err := OpenPasskeys(path, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := p.Add(adminA, mustHandle(t, p), aCredential(1, "iPhone"), now); err != nil {
		t.Fatal(err)
	}

	// Somebody edits the file by hand while the panel is running.
	if err := os.WriteFile(path, []byte(`{"version":1,"owners":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Add(adminB, mustHandle(t, p), aCredential(30, "x"), now); err == nil {
		t.Fatal("want a refusal, got a write that would have discarded the hand edit")
	}
	// And memory is unchanged, so the panel keeps working.
	if len(p.List(adminA)) != 1 {
		t.Error("the failed save mutated memory")
	}
}

func TestPasskeysOpenRejectsBadFiles(t *testing.T) {
	good := b64(1, 32)
	tests := []struct{ name, content string }{
		{"unknown field", `{"version":1,"owners":[],"extra":true}`},
		{"future version", `{"version":2,"owners":[]}`},
		{"malformed", `{`},
		{"empty admin_id", `{"version":1,"owners":[{"admin_id":"","handle":"` + good + `"}]}`},
		{"short handle", `{"version":1,"owners":[{"admin_id":"a","handle":"` + b64(1, 8) + `"}]}`},
		{"handle is not base64url", `{"version":1,"owners":[{"admin_id":"a","handle":"not base64!!"}]}`},
		{"duplicate admin_id", `{"version":1,"owners":[
			{"admin_id":"a","handle":"` + b64(1, 32) + `"},
			{"admin_id":"a","handle":"` + b64(9, 32) + `"}]}`},
		{"duplicate handle", `{"version":1,"owners":[
			{"admin_id":"a","handle":"` + good + `"},
			{"admin_id":"b","handle":"` + good + `"}]}`},
		// A duplicate credential id makes the login lookup ambiguous, which is the one
		// thing this file must never be.
		{"duplicate credential_id", `{"version":1,"owners":[
			{"admin_id":"a","handle":"` + b64(1, 32) + `","credentials":[
				{"id":"c1","credential_id":"` + b64(5, 32) + `","label":"x","public_key":"` + b64(7, 77) + `","algorithm":-7,"created_at":"2026-01-02T03:04:05Z"}]},
			{"admin_id":"b","handle":"` + b64(9, 32) + `","credentials":[
				{"id":"c2","credential_id":"` + b64(5, 32) + `","label":"y","public_key":"` + b64(7, 77) + `","algorithm":-7,"created_at":"2026-01-02T03:04:05Z"}]}]}`},
		{"missing algorithm", `{"version":1,"owners":[
			{"admin_id":"a","handle":"` + good + `","credentials":[
				{"id":"c1","credential_id":"` + b64(5, 32) + `","label":"x","public_key":"` + b64(7, 77) + `","created_at":"2026-01-02T03:04:05Z"}]}]}`},
		{"missing created_at", `{"version":1,"owners":[
			{"admin_id":"a","handle":"` + good + `","credentials":[
				{"id":"c1","credential_id":"` + b64(5, 32) + `","label":"x","public_key":"` + b64(7, 77) + `","algorithm":-7}]}]}`},
		{"empty label", `{"version":1,"owners":[
			{"admin_id":"a","handle":"` + good + `","credentials":[
				{"id":"c1","credential_id":"` + b64(5, 32) + `","label":"","public_key":"` + b64(7, 77) + `","algorithm":-7,"created_at":"2026-01-02T03:04:05Z"}]}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), PasskeysFile)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenPasskeys(path, true); err == nil {
				t.Error("want an error")
			}
		})
	}
}

// A missing file is a fresh volume, not a problem.
func TestPasskeysFreshDirIsEmpty(t *testing.T) {
	p := newPasskeys(t)
	if p.Count() != 0 || len(p.Owners()) != 0 {
		t.Errorf("Count = %d, Owners = %d", p.Count(), len(p.Owners()))
	}
	if _, err := os.Stat(p.Path()); !os.IsNotExist(err) {
		t.Error("opening the store created the file; nothing was written yet")
	}
}

// This file is safe to read, which is the whole reason it can be written at all: it holds
// public keys, not capabilities.
func TestPasskeysFileHoldsNoPrivateKey(t *testing.T) {
	p := newPasskeys(t)
	if _, err := p.Add(adminA, mustHandle(t, p), aCredential(1, "iPhone"), time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private") {
		t.Errorf("the file mentions a private key:\n%s", raw)
	}
	fi, err := os.Stat(p.Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}
