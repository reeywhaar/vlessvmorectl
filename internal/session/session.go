// Package session holds logged-in operators.
//
// Sessions are kept in memory and mirrored to disk, so a container restart does not sign
// everybody out. Nothing replayable is written: the table is keyed by sha256 of the
// cookie value and that hash is what gets persisted, exactly as the sibling project
// stores token hashes rather than tokens. Somebody who reads the file needs a 256-bit
// preimage before it is worth anything to them.
//
// The write rate is low by construction — a login, a logout, a sweep, and a sliding
// refresh throttled to once an hour per session — so this does not turn a read-heavy
// panel into a write-heavy one.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"vlessvmorectl/internal/store"
)

// CookieName is the session cookie.
const CookieName = "vlessvmore_auth"

const (
	// Idle is the sliding window: a session dies this long after its last use.
	// Ten days, so an operator who checks the panel on Monday is still logged in the
	// following Wednesday.
	Idle = 10 * 24 * time.Hour

	// Refresh throttles the slide. Without it a SPA polling every ten seconds would
	// rewrite the record and emit a Set-Cookie on every single request, for a window
	// measured in days. Same trick, same reasoning, as the sibling's
	// store.LastUsedResolution.
	Refresh = time.Hour

	// SweepInterval is how often the janitor collects expired records. They are
	// already unusable by then — this only reclaims the memory.
	SweepInterval = 10 * time.Minute

	// MaxSessions is a backstop, not a policy. Only a successful login creates a
	// session, so reaching this means something is very wrong; evicting the oldest is
	// better than growing without bound.
	MaxSessions = 4096

	// idBytes is the entropy in a session id. 32 bytes is 256 bits.
	idBytes = 32
)

// Record is a live session.
type Record struct {
	// AdminID names the administrator. Username is filled in by the auth middleware from
	// the live record, so it is not persisted and is empty on a freshly restored session.
	AdminID  string
	Username string

	// Fingerprint is sha256(admin.PasswordHash)[:8] as of login.
	//
	// The CLI that changes a password runs in a different process and cannot reach
	// this table, so invalidation cannot be a push. Instead the auth middleware
	// re-derives the fingerprint from the store on each request and drops the session
	// when it moves. One map lookup and one sha256 of a 60-byte string per request,
	// in exchange for "changing the password logs them out everywhere" actually being
	// true.
	Fingerprint [8]byte

	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// Storage persists the table across restarts. A nil Storage keeps everything in memory,
// which is what the tests use.
type Storage interface {
	Load() ([]store.PersistedSession, error)
	Save([]store.PersistedSession) error
}

// Table is the session store.
//
// Keyed by sha256 of the session id rather than the id itself. A heap dump, a core file,
// a swapped page or the persisted file therefore never contains a value that can be
// replayed as a cookie, and the map probe is inherently timing-safe: distinguishing
// entries by timing would require finding a 256-bit preimage first.
type Table struct {
	mu      sync.Mutex
	byID    map[[32]byte]*Record
	storage Storage
	log     *slog.Logger
}

// New returns a table backed by storage, restoring whatever it holds.
//
// A storage failure is logged, not fatal. Losing sessions signs people out, which is
// recoverable in ten seconds; refusing to start because a cache file is unreadable is
// not the right trade for a service whose job is to be reachable when something else has
// gone wrong.
func New(storage Storage, log *slog.Logger) *Table {
	t := &Table{byID: make(map[[32]byte]*Record), storage: storage, log: log}
	if storage == nil {
		return t
	}

	list, err := storage.Load()
	if err != nil {
		log.Error("could not restore sessions; everyone will have to sign in again", "error", err)
		return t
	}

	now := time.Now()
	restored, dropped := 0, 0
	for _, p := range list {
		key, err := hex.DecodeString(p.Hash)
		if err != nil || len(key) != 32 {
			dropped++
			continue
		}
		// Expired on disk is expired: no point restoring it just to sweep it.
		if !now.Before(p.ExpiresAt) {
			dropped++
			continue
		}
		fp, err := hex.DecodeString(p.Fingerprint)
		if err != nil || len(fp) != 8 {
			dropped++
			continue
		}
		// A row with nobody to belong to. Nothing to resolve it against — this package has
		// no admin store on purpose — so it goes.
		if p.AdminID == "" {
			dropped++
			continue
		}
		t.byID[[32]byte(key)] = &Record{
			AdminID:     p.AdminID,
			Fingerprint: [8]byte(fp),
			CreatedAt:   p.CreatedAt,
			LastSeenAt:  p.LastSeenAt,
			ExpiresAt:   p.ExpiresAt,
		}
		restored++
	}
	if restored > 0 || dropped > 0 {
		log.Info("restored sessions", "restored", restored, "dropped", dropped)
	}
	return t
}

// NewInMemory is New with no persistence.
func NewInMemory() *Table {
	return &Table{byID: make(map[[32]byte]*Record), log: slog.New(discardHandler{})}
}

// flushLocked mirrors the table to storage. Caller holds the lock.
//
// A failed write is logged and otherwise ignored: the session is valid in memory
// regardless of whether we managed to record it, and failing the operator's login
// because the disk is full would be the wrong end of that trade. The cost is that a
// crash between here and the next successful write loses recent sessions.
func (t *Table) flushLocked() {
	if t.storage == nil {
		return
	}
	list := make([]store.PersistedSession, 0, len(t.byID))
	for key, r := range t.byID {
		list = append(list, store.PersistedSession{
			Hash:        hex.EncodeToString(key[:]),
			AdminID:     r.AdminID,
			Fingerprint: hex.EncodeToString(r.Fingerprint[:]),
			CreatedAt:   r.CreatedAt,
			LastSeenAt:  r.LastSeenAt,
			ExpiresAt:   r.ExpiresAt,
		})
	}
	if err := t.storage.Save(list); err != nil {
		t.log.Error("could not persist sessions; a restart will sign people out", "error", err)
	}
}

// discardHandler swallows logs, for NewInMemory.
type discardHandler struct{ slog.Handler }

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (d discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardHandler) WithGroup(string) slog.Handler           { return d }

// Issue mints a session and returns the id to put in the cookie. The id is the only
// time the raw value exists outside the caller's hands.
func (t *Table) Issue(adminID, username string, fingerprint [8]byte, now time.Time) (string, *Record, error) {
	buf := make([]byte, idBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	id := base64.RawURLEncoding.EncodeToString(buf)

	rec := &Record{
		AdminID:     adminID,
		Username:    username,
		Fingerprint: fingerprint,
		CreatedAt:   now,
		LastSeenAt:  now,
		ExpiresAt:   now.Add(Idle),
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.byID) >= MaxSessions {
		t.evictOldestLocked()
	}
	t.byID[sha256.Sum256([]byte(id))] = rec
	t.flushLocked()
	return id, rec.clone(), nil
}

// Lookup resolves a cookie value to a live session, sliding its expiry.
//
// The second return value reports whether the cookie should be re-sent: true at most
// once per Refresh, so the browser is not handed a new Set-Cookie on every poll.
func (t *Table) Lookup(id string, now time.Time) (rec *Record, renewed bool, ok bool) {
	if id == "" {
		return nil, false, false
	}
	key := sha256.Sum256([]byte(id))

	t.mu.Lock()
	defer t.mu.Unlock()

	r, found := t.byID[key]
	if !found {
		return nil, false, false
	}
	if !now.Before(r.ExpiresAt) {
		delete(t.byID, key)
		t.flushLocked()
		return nil, false, false
	}

	if now.Sub(r.LastSeenAt) >= Refresh {
		r.LastSeenAt = now
		r.ExpiresAt = now.Add(Idle)
		renewed = true
		// Throttled by the Refresh window above, so this is at most one write per
		// session per hour rather than one per request.
		t.flushLocked()
	}
	return r.clone(), renewed, true
}

// Delete drops a session. Used by logout, and by the auth middleware when an admin's
// credential has changed underneath a live session.
func (t *Table) Delete(id string) {
	if id == "" {
		return
	}
	key := sha256.Sum256([]byte(id))
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.byID[key]; ok {
		delete(t.byID, key)
		t.flushLocked()
	}
}

// DeleteAdmin drops every session belonging to one administrator.
func (t *Table) DeleteAdmin(adminID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for k, r := range t.byID {
		if r.AdminID == adminID {
			delete(t.byID, k)
			n++
		}
	}
	if n > 0 {
		t.flushLocked()
	}
	return n
}

// Sweep removes expired records and returns how many went.
func (t *Table) Sweep(now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for k, r := range t.byID {
		if !now.Before(r.ExpiresAt) {
			delete(t.byID, k)
			n++
		}
	}
	if n > 0 {
		t.flushLocked()
	}
	return n
}

// Len is the number of live records, for tests and logging.
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.byID)
}

// RunJanitor sweeps until done is closed.
func (t *Table) RunJanitor(done <-chan struct{}, now func() time.Time) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			t.Sweep(now())
		}
	}
}

// evictOldestLocked drops the least recently used record. Caller holds the lock.
func (t *Table) evictOldestLocked() {
	var oldestKey [32]byte
	var oldest time.Time
	first := true
	for k, r := range t.byID {
		if first || r.LastSeenAt.Before(oldest) {
			oldestKey, oldest, first = k, r.LastSeenAt, false
		}
	}
	if !first {
		delete(t.byID, oldestKey)
	}
}

// clone returns a copy, so a caller cannot mutate a live record by holding a pointer
// to it after the lock is released.
func (r *Record) clone() *Record {
	c := *r
	return &c
}
