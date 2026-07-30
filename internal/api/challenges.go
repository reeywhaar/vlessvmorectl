package api

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// Pending WebAuthn ceremonies.
//
// In memory, single-use and capped, in the style of ratelimit.go. Nothing here is worth
// persisting: a challenge outlives its usefulness in minutes, and losing one across a
// restart costs a retry.
const (
	passkeyChallengeTTL = 3 * time.Minute

	// Conditional mediation starts a login ceremony on every load of the sign-in page,
	// before the visitor does anything, so this table is the one thing an anonymous caller
	// can fill. Oldest-first eviction means a flood costs a legitimate operator a retry
	// rather than the feature.
	maxPasskeyChallenges = 512

	stateBytes = 16
)

type pendingChallenge struct {
	session webauthn.SessionData

	// adminID is empty for a login, which is by definition not yet authenticated. For a
	// registration it is who began it, and take refuses a mismatch — so one signed-in
	// administrator cannot finish another's enrolment.
	adminID string

	// handle is the user handle a first-time registration will store if it succeeds. Held
	// here rather than written at begin, so an abandoned ceremony leaves nothing behind.
	handle string

	expiresAt time.Time
}

type challengeStore struct {
	mu sync.Mutex
	m  map[string]pendingChallenge
}

func newChallengeStore() *challengeStore {
	return &challengeStore{m: make(map[string]pendingChallenge)}
}

// issue stores a ceremony and returns the opaque state the client echoes back.
func (c *challengeStore) issue(sd *webauthn.SessionData, adminID, handle string, now time.Time) (string, error) {
	buf := make([]byte, stateBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate challenge state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(buf)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.reapLocked(now)
	if len(c.m) >= maxPasskeyChallenges {
		c.evictOldestLocked()
	}
	c.m[state] = pendingChallenge{
		session:   *sd,
		adminID:   adminID,
		handle:    handle,
		expiresAt: now.Add(passkeyChallengeTTL),
	}
	return state, nil
}

// take consumes a ceremony. It deletes on every path, so a replayed finish fails here
// before any signature is checked.
func (c *challengeStore) take(state, adminID string, now time.Time) (pendingChallenge, bool) {
	if state == "" {
		return pendingChallenge{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.m[state]
	if !ok {
		return pendingChallenge{}, false
	}
	delete(c.m, state)
	if !now.Before(p.expiresAt) || p.adminID != adminID {
		return pendingChallenge{}, false
	}
	return p, true
}

func (c *challengeStore) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func (c *challengeStore) reapLocked(now time.Time) {
	for k, p := range c.m {
		if !now.Before(p.expiresAt) {
			delete(c.m, k)
		}
	}
}

func (c *challengeStore) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, p := range c.m {
		if first || p.expiresAt.Before(oldest) {
			oldestKey, oldest, first = k, p.expiresAt, false
		}
	}
	if !first {
		delete(c.m, oldestKey)
	}
}
