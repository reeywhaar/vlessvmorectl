package api

import (
	"strings"
	"sync"
	"time"
)

// Login throttling.
//
// Two counters, both fixed one-minute windows, both in memory.
//
//   - Per username, to stop brute force. Reset on a successful login, so an operator
//     who mistypes three times and then gets it right is not left in a penalty box.
//   - Global, because bcrypt at cost 12 on an unauthenticated endpoint is a CPU
//     amplifier: ~250ms of work for a request that costs the caller nothing. The
//     global cap bounds this service to roughly a quarter of one core no matter how
//     hard it is hit.
//
// The global limit is deliberately far above the per-user one. Set them close together
// and an attacker can lock the real operator out by draining the global bucket, turning
// a brute-force defence into a denial of service.
const (
	perUserFailures = 5
	globalAttempts  = 60
	limitWindow     = time.Minute
)

// The remote address is deliberately not a limiter key.
//
// Behind caddy-docker-proxy every request arrives from the same container IP, so
// per-IP limiting would be one shared bucket — i.e. the global limit, but pretending
// to be something finer. Using X-Forwarded-For instead would be worse than useless:
// it is attacker-controlled, so a hostile client mints a fresh bucket per request and
// the limit disappears entirely, while an honest operator behind the same proxy keeps
// theirs. Username plus a global cap is the honest pair.
type loginLimiter struct {
	mu sync.Mutex

	users        map[string]*window
	global       window
	lastReapedAt time.Time
}

type window struct {
	count   int
	startAt time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{users: make(map[string]*window)}
}

// allow reports whether an attempt may proceed, and counts it against the global cap.
func (l *loginLimiter) allow(username string, now time.Time) bool {
	key := limiterKey(username)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.reapLocked(now)

	if l.global.roll(now) > globalAttempts {
		return false
	}
	if w, ok := l.users[key]; ok && w.within(now) && w.count >= perUserFailures {
		return false
	}
	return true
}

// fail records a failed attempt for a username.
func (l *loginLimiter) fail(username string, now time.Time) {
	key := limiterKey(username)

	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.users[key]
	if !ok {
		w = &window{}
		l.users[key] = w
	}
	w.roll(now)
}

// succeed clears a username's failure count.
func (l *loginLimiter) succeed(username string) {
	key := limiterKey(username)
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.users, key)
}

// reapLocked drops windows that have aged out, so a long-running process does not
// accumulate one map entry per username ever attempted. Caller holds the lock.
func (l *loginLimiter) reapLocked(now time.Time) {
	if now.Sub(l.lastReapedAt) < limitWindow {
		return
	}
	l.lastReapedAt = now
	for k, w := range l.users {
		if !w.within(now) {
			delete(l.users, k)
		}
	}
}

// roll advances the window if it has expired and returns the new count.
func (w *window) roll(now time.Time) int {
	if !w.within(now) {
		w.startAt, w.count = now, 0
	}
	w.count++
	return w.count
}

func (w *window) within(now time.Time) bool {
	return !w.startAt.IsZero() && now.Sub(w.startAt) < limitWindow
}

// limiterKey folds case so that "Alice" and "alice" share a bucket, matching how the
// store resolves usernames. Otherwise the limit is bypassed by varying capitalisation.
func limiterKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// Share-link throttling.
//
// The same two-bucket shape as loginLimiter, the same reasoning, a different scarce
// resource. There it was 250ms of bcrypt on this box; here it is somebody else's VPS,
// because one GET of a share link becomes roughly two credentialed calls per attached
// account against nodes that are, by design, small.
//
// What this is *not* for is worth stating, because it looks like a guessing defence and
// is not one. A share token is 160 bits; brute force is not reachable with or without a
// limiter, and an unknown token is answered from memory before any node is contacted, so
// spraying tokens costs the nodes nothing. This exists purely as an amplification ceiling
// on somebody who already holds a working link and is refreshing it in a loop.
//
// The global cap sits far above the per-link one, for the reason spelled out above
// loginLimiter: set them close and a single caller denies service to every other
// subscriber by draining the shared bucket.
const (
	accessPerTokenRequests = 20  // per minute: a page load plus impatient refreshes
	accessGlobalRequests   = 200 // per minute, every link together
)

// accessLimiter throttles GET /api/access/{token}.
//
// Buckets are created only by allowToken, which the handler calls *after* the store has
// resolved the token — see the comment there. Keying on presented tokens instead would
// let anyone mint an unbounded number of map entries by varying a path segment, turning a
// rate limiter into a memory leak.
type accessLimiter struct {
	mu sync.Mutex

	tokens       map[string]*window
	global       window
	lastReapedAt time.Time

	// loggedAt throttles the log line, not the limit. A per-request line on an endpoint
	// whose entire problem is request volume would make the log the amplifier.
	loggedAt time.Time
}

func newAccessLimiter() *accessLimiter {
	return &accessLimiter{tokens: make(map[string]*window)}
}

// allowGlobal counts a request against the endpoint-wide cap.
//
// Called before the store lookup, so that a flood of unknown tokens still cannot spin
// this handler without bound — but note it allocates nothing per token, which is the
// property that makes it safe to call on unauthenticated input.
func (l *accessLimiter) allowGlobal(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reapLocked(now)
	return l.global.roll(now) <= accessGlobalRequests
}

// allowToken counts a request against one link's bucket.
//
// The key is a fingerprint rather than the token itself: this map is long-lived, and a
// process holding a table of live credentials in memory for no reason is a needless place
// for them to be found.
func (l *accessLimiter) allowToken(fingerprint string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	w, ok := l.tokens[fingerprint]
	if !ok {
		w = &window{}
		l.tokens[fingerprint] = w
	}
	return w.roll(now) <= accessPerTokenRequests
}

// shouldLog reports whether this refusal is the first in its window.
func (l *accessLimiter) shouldLog(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.loggedAt) < limitWindow {
		return false
	}
	l.loggedAt = now
	return true
}

// reapLocked drops windows that have aged out. Caller holds the lock.
func (l *accessLimiter) reapLocked(now time.Time) {
	if now.Sub(l.lastReapedAt) < limitWindow {
		return
	}
	l.lastReapedAt = now
	for k, w := range l.tokens {
		if !w.within(now) {
			delete(l.tokens, k)
		}
	}
}
