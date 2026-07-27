package api

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"time"

	"vlessvmorectl/internal/config"
	"vlessvmorectl/internal/store"
)

// AccessPath is the SPA route a share link points at. The API endpoint below backs it.
const AccessPath = "/access/"

const (
	// accessBudget bounds the whole page. Shorter than config.HTTPTimeout on purpose:
	// one unreachable node should cost the reader a spinner and a "temporarily
	// unavailable" card, not a browser connection held open until the transport gives up
	// on its own.
	accessBudget = 10 * time.Second

	// accessFanout caps concurrent upstream calls from one page render, so a subscriber
	// with thirty accounts is thirty requests spread over eight connections rather than
	// thirty landing simultaneously on two small VPSes.
	accessFanout = 8
)

// accessNotValid is the single answer to every refusal this endpoint makes.
//
// One constant string, and the handler must never interpolate anything into it. A
// malformed token, an unknown one, and a disabled subscriber are byte-identical: there is
// nothing useful the reader can do with the difference, and each variation would be a
// small oracle for somebody probing.
const accessNotValid = "this link is not valid"

// accessResponse is the entire public surface of a share link.
//
// A projection type, for exactly the reason serverEntry is one, and with more at stake.
// Everything it must not contain is one field away from the values it is assembled from:
// config.Server carries the node's bearer token, store.Subscriber carries the share token
// and the operator's private note, and the node's user object carries sub_token. None of
// them can appear here because there is no field for them. A leak would take somebody
// adding a field on purpose, which is a reviewable act; a leak through a forgotten
// json:"-" is not.
type accessResponse struct {
	Subscriber accessSubscriber `json:"subscriber"`
	Entries    []accessEntry    `json:"entries"`
	// FetchedAt lets the page say "as of 12:04" rather than implying it is live. It does
	// not poll, so stating its own age is the honest alternative.
	FetchedAt time.Time `json:"fetched_at"`
}

// accessSubscriber has one field. No id, no token, no note, no timestamps: the reader
// needs to recognise the link as theirs, and nothing else.
type accessSubscriber struct {
	Name string `json:"name"`
}

// Reasons an entry could not be shown. A closed set, and never an error string.
//
// Every failure path in this handler has something quotable in hand — a URL, a Go error,
// an upstream body — and none of it is the reader's business or their problem. "The
// server rejected this panel's token" is a sentence a subscriber must never see. The
// detail goes to the log, keyed by entry id.
const (
	// reasonUnavailable: the node did not answer, or refused our token. Retryable, and
	// says nothing about whether the person's VPN is actually working.
	reasonUnavailable = "unavailable"
	// reasonRemoved: the node answered, and has no such account any more.
	reasonRemoved = "removed"
	// reasonUnconfigured: this panel no longer manages that node at all, usually because
	// its URL changed in VLESSVMORE_SERVERS and with it its derived id.
	reasonUnconfigured = "unconfigured"
)

type accessEntry struct {
	// ID is the panel's entry id. Deliberately not server_id and not vless_user_id: the
	// page needs a stable key for a list, not a way to enumerate this panel's nodes.
	ID          string `json:"id"`
	ServerLabel string `json:"server_label"`
	Label       string `json:"label,omitempty"`

	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`

	Name            string    `json:"name,omitempty"`
	Link            string    `json:"link,omitempty"`
	SubscriptionURL string    `json:"subscription_url,omitempty"`
	InstallURL      string    `json:"install_url,omitempty"`
	QR              *qrMatrix `json:"qr,omitempty"`
	SubscriptionQR  *qrMatrix `json:"subscription_qr,omitempty"`

	Enabled        bool         `json:"enabled"`
	DisabledReason string       `json:"disabled_reason,omitempty"`
	ExpiresAt      *time.Time   `json:"expires_at,omitempty"`
	Usage          *accessUsage `json:"usage,omitempty"`
	QuotaBytes     int64        `json:"quota_bytes"`
}

// accessUsage is the quota window only. See nodeUsage for why the lifetime totals the
// node also sends are left out.
type accessUsage struct {
	WindowUp    int64 `json:"window_up"`
	WindowDown  int64 `json:"window_down"`
	WindowTotal int64 `json:"window_total"`
	// QuotaRemaining is 0 when unlimited, which is not "none left".
	QuotaRemaining int64 `json:"quota_remaining"`
}

// accessHandler serves a subscriber's own list of accounts, with no session.
//
// # Why this is not a second /api/proxy
//
// proxyHandler is, in its own words, an authenticated SSRF gadget: it takes a URL from a
// client and fetches it with a credential attached, and what holds it in check is an
// origin allowlist, a path allowlist and a session. This endpoint has none of those
// problems to solve, because **the caller contributes zero bytes to any outbound URL**.
// The scheme, host, path and query of every upstream request are built here from
// server-side state alone: a config.Server looked up by an id that came out of
// subscribers.json, and a vless_user_id that an authenticated operator put there. The
// token in the URL selects a *record*; it cannot reach the wire.
//
// That is the invariant to preserve if this function is ever edited. It is pinned by a
// test that replays the request with ?url=, extra path segments and a forged Host, and
// asserts the upstream saw only the configured node.
func (s *Server) accessHandler(w http.ResponseWriter, r *http.Request) {
	// Everything here is a capability bundle. Never a shared cache, matching the reason
	// the proxy sets the same header.
	w.Header().Set("Cache-Control", "no-store")

	now := s.now()
	token := r.PathValue("token")

	// Global bucket first: it allocates nothing per token, which is what makes it safe
	// to run on unauthenticated input ahead of any lookup.
	if !s.access.allowGlobal(now) {
		s.rejectAccess(w, token, now, "global")
		return
	}

	sub, err := s.store.Subscribers.GetByToken(token)
	if err != nil || sub.Disabled {
		// An unknown token is answered from memory and returns here, having contacted no
		// node at all. That is the whole reason this endpoint needs no cache: a caller
		// who does not already hold a working token cannot make the panel touch a single
		// upstream. There is a test asserting the upstream hit count is zero.
		//
		// A disabled subscriber takes the same exit, because the operator's act of
		// disabling means "this link stops working", and the least surprising behaviour
		// is that it stops working exactly as if it had been deleted.
		//
		// Timing is not padded, unlike the sibling's /sub/ endpoint. It cannot usefully
		// be: a valid token triggers seconds of upstream fan-out, so the two cases are
		// separated by orders of magnitude no padding could hide — and observing that
		// separation requires already holding a 160-bit token. This package's doc
		// already argues that this service refuses honestly rather than performatively.
		s.log.Warn("share link not recognised", "token_fp", store.TokenFingerprint(token))
		writeError(w, http.StatusNotFound, accessNotValid)
		return
	}

	if !s.access.allowToken(store.TokenFingerprint(sub.Token), now) {
		s.rejectAccess(w, sub.Token, now, "link")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), accessBudget)
	defer cancel()

	entries := s.collect(ctx, sub)

	unavailable := 0
	for _, e := range entries {
		if !e.Available {
			unavailable++
		}
	}
	s.log.Debug("served a share link",
		"subscriber", sub.ID, "entries", len(entries), "unavailable", unavailable)

	writeJSON(w, http.StatusOK, accessResponse{
		Subscriber: accessSubscriber{Name: sub.Name},
		Entries:    entries,
		FetchedAt:  now.UTC().Truncate(time.Second),
	})
}

func (s *Server) rejectAccess(w http.ResponseWriter, token string, now time.Time, scope string) {
	if s.access.shouldLog(now) {
		s.log.Warn("share link requests are being rate limited",
			"scope", scope, "token_fp", store.TokenFingerprint(token))
	}
	w.Header().Set("Retry-After", "60")
	writeError(w, http.StatusTooManyRequests, "too many requests; try again in a minute")
}

// collect resolves every entry a subscriber holds, concurrently and independently.
//
// Factored out of the handler because the aggregated-subscription endpoint, if it is ever
// built, needs exactly this and renders it differently.
//
// Error isolation is the point: one unreachable node marks its own entries and leaves the
// rest of the page intact. A person with accounts on three nodes should not get a blank
// page because one VPS is rebooting — and if *every* node fails this still returns a full
// list of unavailable entries rather than an error, because a 502 tells the reader
// nothing they can act on while "3 accounts, all temporarily unreachable" tells them to
// try later and not to message their operator.
func (s *Server) collect(ctx context.Context, sub *store.Subscriber) []accessEntry {
	out := make([]accessEntry, len(sub.Entries))
	sem := make(chan struct{}, accessFanout)

	// Phase 1: the node labels, one call per *distinct* node rather than per entry.
	labels := s.serverLabels(ctx, sub.Entries, sem)

	// Phase 2: two calls per entry, each writing only into its own slot — so there is no
	// shared mutable state here and no mutex.
	var wg sync.WaitGroup
	for i := range sub.Entries {
		e := sub.Entries[i]

		srv, ok := s.cfg.ServerByID(e.ServerID)
		if !ok {
			// Costs zero upstream calls, which is the useful half: an operator who
			// renames a node does not also get a fan-out of doomed requests.
			out[i] = accessEntry{
				ID: e.ID, Label: e.Label,
				ServerLabel: unknownServerLabel,
				Available:   false, Reason: reasonUnconfigured,
			}
			continue
		}

		out[i] = accessEntry{ID: e.ID, Label: e.Label, ServerLabel: labels[e.ServerID]}

		wg.Add(1)
		go func(i int, e store.SubscriberEntry, srv *config.Server) {
			defer wg.Done()
			s.fillEntry(ctx, &out[i], sub.ID, e, srv, sem)
		}(i, e, srv)
	}
	wg.Wait()
	return out
}

// unknownServerLabel heads a card whose node this panel no longer manages at all, and so
// cannot name even approximately.
const unknownServerLabel = "Unknown server"

// serverLabels fetches each distinct node's display name.
//
// A node that does not answer falls back to its configured host, so a card is still headed
// by something the reader recognises rather than a blank.
//
// That fallback does put the node's management host in front of an anonymous caller, and
// it is a deliberate trade rather than an oversight. What must never appear here is the
// node's *bearer token* — that is a full-control credential, and every path in this file
// is built so it cannot reach the projection. An address is not in that class: it is
// already implied by the vless:// link the reader is about to copy, and knowing it grants
// nothing without a credential to go with it.
func (s *Server) serverLabels(ctx context.Context, entries []store.SubscriberEntry, sem chan struct{}) map[string]string {
	ids := make(map[string]*config.Server)
	for _, e := range entries {
		if srv, ok := s.cfg.ServerByID(e.ServerID); ok {
			ids[e.ServerID] = srv
		}
	}

	var mu sync.Mutex
	labels := make(map[string]string, len(ids))
	var wg sync.WaitGroup
	for id, srv := range ids {
		wg.Add(1)
		go func(id string, srv *config.Server) {
			defer wg.Done()

			label := hostOf(srv)
			if body, ok := s.nodeGet(ctx, srv, "/api/server", sem); ok {
				var info nodeServerInfo
				if decodeNode(body, &info) == nil {
					if info.Name != "" {
						label = info.Name
					} else if info.Host != "" {
						label = info.Host
					}
				}
			}

			mu.Lock()
			labels[id] = label
			mu.Unlock()
		}(id, srv)
	}
	wg.Wait()
	return labels
}

// fillEntry fetches one account's status and credentials.
func (s *Server) fillEntry(ctx context.Context, dst *accessEntry, subID string, e store.SubscriberEntry, srv *config.Server, sem chan struct{}) {
	// url.PathEscape rather than string concatenation. The id is validated on the way
	// into the store, so this is belt and braces — but the belt is what keeps the "the
	// caller contributes nothing to the outbound URL" claim true even if that validation
	// is ever loosened.
	base := "/api/users/" + url.PathEscape(e.VlessUserID)

	var (
		wg          sync.WaitGroup
		userBody    []byte
		userOK      bool
		userMissing bool
		linkBody    []byte
		linkOK      bool
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		var status int
		userBody, status, userOK = s.nodeGetStatus(ctx, srv, base, sem)
		// A 404 that reaches this line is a *JSON* one — the node saying it has no such
		// account, which is the dangling-reference case. Its stdlib text/plain 404 means
		// something else entirely (our bearer token was refused), and nodeGetStatus has
		// already turned that into a transport failure with status 0, precisely so that
		// it cannot be reported to the reader as "your account was removed".
		userMissing = !userOK && status == http.StatusNotFound
	}()
	go func() {
		defer wg.Done()
		linkBody, linkOK = s.nodeGet(ctx, srv, base+"/link", sem)
	}()
	wg.Wait()

	if !userOK {
		reason := reasonUnavailable
		if userMissing {
			reason = reasonRemoved
		}
		dst.Available = false
		dst.Reason = reason
		s.log.Warn("a share link entry could not be resolved",
			"subscriber", subID, "entry", e.ID, "server", srv.URL, "reason", reason)
		return
	}

	var u nodeUser
	if err := decodeNode(userBody, &u); err != nil {
		dst.Available = false
		dst.Reason = reasonUnavailable
		s.log.Warn("a node's user response could not be parsed",
			"subscriber", subID, "entry", e.ID, "server", srv.URL, "error", err)
		return
	}

	dst.Available = true
	dst.Name = u.Name
	dst.Enabled = u.Enabled
	dst.DisabledReason = u.DisabledReason
	dst.ExpiresAt = u.ExpiresAt
	dst.QuotaBytes = u.QuotaBytes
	if u.Usage != nil {
		dst.Usage = &accessUsage{
			WindowUp:       u.Usage.WindowUp,
			WindowDown:     u.Usage.WindowDown,
			WindowTotal:    u.Usage.WindowTotal,
			QuotaRemaining: u.Usage.QuotaRemaining,
		}
		if dst.QuotaBytes == 0 {
			dst.QuotaBytes = u.Usage.QuotaBytes
		}
	}

	// A failed /link leaves the entry available with status and usage but no credentials.
	// Half a page beats none: somebody checking why they were cut off gets their answer
	// even when the QR is missing.
	if linkOK {
		var l nodeLink
		if decodeNode(linkBody, &l) == nil {
			dst.Link = l.Link
			dst.SubscriptionURL = l.SubscriptionURL
			dst.InstallURL = l.InstallURL
			dst.QR = l.QR
			dst.SubscriptionQR = l.SubscriptionQR
		}
	} else {
		s.log.Warn("a share link entry resolved but its credentials did not",
			"subscriber", subID, "entry", e.ID, "server", srv.URL)
	}
}

// nodeGet is nodeGetStatus without the status, for callers that only care whether it
// worked.
func (s *Server) nodeGet(ctx context.Context, srv *config.Server, path string, sem chan struct{}) ([]byte, bool) {
	b, _, ok := s.nodeGetStatus(ctx, srv, path, sem)
	return b, ok
}

// nodeGetStatus makes one credentialed GET against a node, through the fan-out semaphore.
//
// Note what it does *not* take: a URL. Only a path this file wrote as a literal, joined
// to an origin that came from configuration. See the handler's doc comment.
// hostOf is a node's host:port, used only as a display fallback. See serverLabels for why
// showing it is acceptable and showing srv.Token would not be.
func hostOf(srv *config.Server) string {
	if u, err := url.Parse(srv.URL); err == nil && u.Host != "" {
		return u.Host
	}
	return srv.URL
}

func (s *Server) nodeGetStatus(ctx context.Context, srv *config.Server, path string, sem chan struct{}) ([]byte, int, bool) {
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return nil, 0, false
	}

	target, err := url.Parse(srv.URL)
	if err != nil {
		return nil, 0, false
	}
	target.Path = path

	res, err := s.proxy.do(ctx, srv, http.MethodGet, target, "", nil)
	if err != nil {
		reason, detail := classifyTransportError(err)
		s.log.Warn("a node could not be reached while serving a share link",
			"server", srv.URL, "path", path, "reason", reason, "error", detail)
		return nil, 0, false
	}
	if res.status == http.StatusNotFound && isStdlibNotFound(res.contentType, res.body) {
		s.log.Warn("vlessvmore rejected our token while serving a share link (it answers 404, never 401); check the token in VLESSVMORE_SERVERS",
			"server", srv.URL, "path", path)
		// Reported as a transport failure, not as a 404, so the reader is told
		// "temporarily unavailable" rather than "your account was removed".
		return nil, 0, false
	}
	if res.status != http.StatusOK {
		return nil, res.status, false
	}
	return res.body, res.status, true
}
