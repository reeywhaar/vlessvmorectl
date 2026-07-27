package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ErrBadCredentials is the single answer to both "no such admin" and "wrong password".
//
// One error rather than two so that no caller can accidentally distinguish them and
// turn the login endpoint into a username oracle. Verify also spends the same time on
// both paths; see dummyHash.
var ErrBadCredentials = errors.New("invalid username or password")

// bcryptCost is deliberately above bcrypt.DefaultCost (10). A login here happens a
// handful of times a week by a human, so ~250ms is invisible, while each increment
// doubles an offline cracker's work against a stolen admins.json.
//
// It is also why POST /api/login is rate limited: a deliberately slow hash on an
// unauthenticated endpoint is a CPU amplifier if anyone can call it in a loop.
const bcryptCost = 12

// Password length bounds.
//
// The maximum is not arbitrary. bcrypt hashes only the first 72 bytes of its input;
// modern x/crypto returns an error rather than truncating, but we check first so the
// message can say *why*. Silently truncating would mean two different passwords
// authenticating the same account.
const (
	MinPasswordLen = 8
	MaxPasswordLen = 72
)

// Admin is one person who may log in to the panel.
type Admin struct {
	Username string `json:"username"`

	// PasswordHash is a bcrypt hash, which carries its own salt and cost, so raising
	// bcryptCost later does not invalidate existing rows.
	PasswordHash string `json:"password_hash"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// There is deliberately no LastLoginAt.
	//
	// Recording it would make `serve` a writer of this file, and `serve` runs in a
	// different process from the CLI that creates and edits admins. The sibling
	// project solves that by routing every CLI command through the daemon's unix
	// socket; reproducing that here would mean a socket listener, a client and a
	// dual-trust handler for the sake of four commands. Leaving the field out makes
	// the CLI the only writer and the daemon a pure reader, and the whole problem
	// disappears. See ReloadIfChanged.
}

// Fingerprint identifies this admin's *current* credential.
//
// Sessions capture it at login and the auth middleware re-checks it on every request,
// so changing or removing a password from a shell — a different process, with no way
// to reach the daemon's in-memory session table — logs that admin out everywhere
// immediately. That is the entire point of changing a password after a suspected
// compromise, and without this it would not happen until the session expired.
func (a *Admin) Fingerprint() [8]byte {
	sum := sha256.Sum256([]byte(a.PasswordHash))
	return [8]byte(sum[:8])
}

type adminsDoc struct {
	Version int     `json:"version"`
	Admins  []Admin `json:"admins"`
}

// Admins is the JSON-backed administrator list.
type Admins struct {
	path string
	mu   sync.RWMutex
	list []Admin

	// Stat of the file as of the last successful read, for ReloadIfChanged.
	loadedSize    int64
	loadedModTime time.Time
	lastChecked   time.Time
}

// OpenAdmins loads admins.json, treating a missing file as an empty list.
//
// A corrupt or wrong-version file here is fatal, unlike during a reload: at startup
// there is no last-known-good copy to fall back on, and coming up with a silently empty
// admin list would lock everybody out while looking like a fresh install.
func OpenAdmins(path string) (*Admins, error) {
	a := &Admins{path: path}
	if err := a.load(); err != nil {
		return nil, err
	}
	return a, nil
}

// load reads and validates the file into memory. Caller must not hold the lock.
func (a *Admins) load() error {
	var doc adminsDoc
	found, err := readJSON(a.path, &doc)
	if err != nil {
		return err
	}
	if found && doc.Version != jsonVersion {
		return fmt.Errorf("%s: unsupported version %d, this build understands %d", a.path, doc.Version, jsonVersion)
	}

	seen := make(map[string]bool, len(doc.Admins))
	for i, ad := range doc.Admins {
		if strings.TrimSpace(ad.Username) == "" {
			return fmt.Errorf("%s: admins[%d]: username is empty", a.path, i)
		}
		key := normalizeUsername(ad.Username)
		if seen[key] {
			return fmt.Errorf("%s: admins[%d]: duplicate username %q", a.path, i, ad.Username)
		}
		seen[key] = true
		if ad.PasswordHash == "" {
			return fmt.Errorf("%s: admins[%d] (%s): password_hash is empty", a.path, i, ad.Username)
		}
		// Catch a hand-edited file where somebody pasted a plaintext password into
		// the hash field, which would otherwise fail every login with no explanation.
		if _, err := bcrypt.Cost([]byte(ad.PasswordHash)); err != nil {
			return fmt.Errorf("%s: admins[%d] (%s): password_hash is not a bcrypt hash; set it with `vlessvmorectl users passwd %s`",
				a.path, i, ad.Username, ad.Username)
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.list = doc.Admins
	a.stampLoadedLocked()
	return nil
}

func (a *Admins) stampLoadedLocked() {
	if fi, err := os.Stat(a.path); err == nil {
		a.loadedSize, a.loadedModTime = fi.Size(), fi.ModTime()
	} else {
		a.loadedSize, a.loadedModTime = 0, time.Time{}
	}
}

// Path returns the backing file.
func (a *Admins) Path() string { return a.path }

// ReloadResolution is how often ReloadIfChanged actually touches the filesystem.
//
// The auth middleware calls it on every request; without the throttle a busy panel
// would stat the file thousands of times a minute for a file that changes when a human
// types a command. Same idiom, and the same reasoning, as the sibling's
// LastUsedResolution.
const ReloadResolution = time.Second

// ReloadIfChanged re-reads admins.json when its size or mtime has moved.
//
// This is how the daemon notices `docker exec … users add alice` without the CLI having
// to reach into its memory. Two failure cases are handled deliberately differently from
// startup:
//
//   - A *missing* file leaves the in-memory list alone. Losing admins.json under a
//     running server should not lock out everyone currently working. This follows the
//     sibling's IdentityStore.Reload, where the argument is the same.
//   - A *corrupt* file logs and keeps the last good copy, for the same reason: a bad
//     hand-edit should not end the operator's session mid-incident.
//
// Both return an error for the caller to log; neither disturbs the loaded list.
func (a *Admins) ReloadIfChanged(now time.Time) error {
	a.mu.Lock()
	if now.Sub(a.lastChecked) < ReloadResolution {
		a.mu.Unlock()
		return nil
	}
	a.lastChecked = now
	size, modTime := a.loadedSize, a.loadedModTime
	a.mu.Unlock()

	fi, err := os.Stat(a.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // keep what we have
		}
		return fmt.Errorf("stat %s: %w", a.path, err)
	}
	if fi.Size() == size && fi.ModTime().Equal(modTime) {
		return nil
	}
	// load() replaces the list only after the whole file validates, so a partial or
	// invalid file leaves the previous one in place.
	return a.load()
}

// List returns a copy of the admins.
func (a *Admins) List() []Admin {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return slices.Clone(a.list)
}

// Count is the number of administrators, which is what "can anyone log in?" reduces to.
func (a *Admins) Count() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.list)
}

// Get finds an admin by username, case-insensitively.
func (a *Admins) Get(username string) (*Admin, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	i := a.indexOfLocked(username)
	if i < 0 {
		return nil, fmt.Errorf("admin %q: %w", username, ErrNotFound)
	}
	ad := a.list[i]
	return &ad, nil
}

// Create adds an administrator.
func (a *Admins) Create(username, password string, now time.Time) (*Admin, error) {
	username = strings.TrimSpace(username)
	if err := validateUsername(username); err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.indexOfLocked(username) >= 0 {
		return nil, fmt.Errorf("admin %q: %w", username, ErrConflict)
	}

	at := now.UTC().Truncate(time.Second)
	ad := Admin{Username: username, PasswordHash: hash, CreatedAt: at, UpdatedAt: at}
	a.list = append(a.list, ad)
	if err := a.saveLocked(); err != nil {
		a.list = a.list[:len(a.list)-1]
		return nil, err
	}
	return &ad, nil
}

// SetPassword replaces an administrator's password, which invalidates their live
// sessions everywhere by way of Admin.Fingerprint.
func (a *Admins) SetPassword(username, password string, now time.Time) (*Admin, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	i := a.indexOfLocked(username)
	if i < 0 {
		return nil, fmt.Errorf("admin %q: %w", username, ErrNotFound)
	}
	before := a.list[i]
	a.list[i].PasswordHash = hash
	a.list[i].UpdatedAt = now.UTC().Truncate(time.Second)
	ad := a.list[i]
	if err := a.saveLocked(); err != nil {
		a.list[i] = before
		return nil, err
	}
	return &ad, nil
}

// Delete removes an administrator. Refusing to remove the last one is a policy the
// CLI enforces, not this: a caller with --force is entitled to empty the list.
func (a *Admins) Delete(username string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	i := a.indexOfLocked(username)
	if i < 0 {
		return fmt.Errorf("admin %q: %w", username, ErrNotFound)
	}
	removed := a.list[i]
	a.list = slices.Delete(a.list, i, i+1)
	if err := a.saveLocked(); err != nil {
		a.list = slices.Insert(a.list, i, removed)
		return err
	}
	return nil
}

// Verify checks a login. It returns ErrBadCredentials for a wrong password and for an
// unknown username alike, and takes the same time over both.
func (a *Admins) Verify(username, password string) (*Admin, error) {
	a.mu.RLock()
	i := a.indexOfLocked(username)
	var hash string
	var ad Admin
	if i >= 0 {
		ad = a.list[i]
		hash = ad.PasswordHash
	}
	a.mu.RUnlock()

	if hash == "" {
		// Compare against a real hash anyway. Without this an unknown username is
		// rejected in microseconds while a known one costs ~250ms, and the difference
		// enumerates the admin list from outside with no credentials at all.
		bcrypt.CompareHashAndPassword(dummyHash(), []byte(password))
		return nil, ErrBadCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, ErrBadCredentials
	}
	return &ad, nil
}

// dummyHash is the constant-time counterweight for an unknown username.
//
// Generated once on first use rather than written out as a literal, so it cannot drift
// away from bcryptCost — a hardcoded cost-10 hash would make an unknown username four
// times faster to reject than a real one, quietly reopening the oracle it exists to
// close. `serve` calls Warm at startup so the one-off generation never lands inside a
// login request.
var dummyHash = sync.OnceValue(func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("vlessvmorectl: no such administrator"), bcryptCost)
	if err != nil {
		// GenerateFromPassword only fails on an out-of-range cost, which is a
		// constant here. Falling back to a wrong-shaped hash is fine: CompareHash
		// will reject it, having spent the time we wanted spent.
		return []byte("$2a$12$" + strings.Repeat("x", 53))
	}
	return h
})

// Warm precomputes what the first failed login would otherwise compute inline.
func Warm() { _ = dummyHash() }

func (a *Admins) saveLocked() error {
	if err := writeJSONAtomic(a.path, adminsDoc{Version: jsonVersion, Admins: a.list}); err != nil {
		return err
	}
	a.stampLoadedLocked()
	return nil
}

// indexOfLocked matches a username case-insensitively. Caller holds the lock.
func (a *Admins) indexOfLocked(username string) int {
	key := normalizeUsername(username)
	for i := range a.list {
		if normalizeUsername(a.list[i].Username) == key {
			return i
		}
	}
	return -1
}

func normalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func validateUsername(username string) error {
	if username == "" {
		return fmt.Errorf("%w: username is required", ErrInvalid)
	}
	if len(username) > 64 {
		return fmt.Errorf("%w: username is longer than 64 characters", ErrInvalid)
	}
	for _, r := range username {
		// Printable, no spaces. A username with a trailing space is a support ticket.
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("%w: username must not contain spaces or control characters", ErrInvalid)
		}
	}
	return nil
}

func hashPassword(password string) (string, error) {
	if len(password) < MinPasswordLen {
		return "", fmt.Errorf("%w: password must be at least %d characters", ErrInvalid, MinPasswordLen)
	}
	if len(password) > MaxPasswordLen {
		return "", fmt.Errorf("%w: password must be at most %d bytes — bcrypt ignores anything beyond that, so a longer one would let a truncated version log in too",
			ErrInvalid, MaxPasswordLen)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// HashFingerprint is Admin.Fingerprint rendered for logs and tests.
func HashFingerprint(fp [8]byte) string { return hex.EncodeToString(fp[:]) }
