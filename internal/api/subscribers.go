package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"vlessvmorectl/internal/config"
	"vlessvmorectl/internal/store"
)

// The operator's view of a subscriber.
//
// Unlike accessResponse this one *does* carry the share token: the whole point of storing
// it in the clear is that an operator can come back in October and copy the link again
// for somebody who lost it. It also carries the note, which is theirs.
type subscriberView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note,omitempty"`

	Token string `json:"token"`

	// AccessPath is relative, and must stay relative.
	//
	// This service is reached through a reverse proxy and knows nothing reliable about
	// its own public origin: Host and X-Forwarded-Host are both client-supplied, so
	// building a share link out of a request header is how somebody ends up emailing a
	// link to https://evil.test/access/<a real token>. The browser already knows the
	// right origin; let it do the join.
	AccessPath string `json:"access_path"`

	Disabled  bool                  `json:"disabled"`
	Entries   []subscriberEntryView `json:"entries"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}

type subscriberEntryView struct {
	ID          string    `json:"id"`
	ServerID    string    `json:"server_id"`
	VlessUserID string    `json:"vless_user_id"`
	Label       string    `json:"label,omitempty"`
	AddedAt     time.Time `json:"added_at"`

	// ServerConfigured reports whether server_id is still in VLESSVMORE_SERVERS.
	//
	// Computed on every read, never stored. It exists so the drawer can mark an orphaned
	// entry — the state an operator lands in after changing a node's URL, since the id is
	// derived from the origin — without the UI having to cross-reference /api/servers
	// itself and get the join subtly wrong.
	ServerConfigured bool `json:"server_configured"`
}

func (s *Server) subscriberView(sub *store.Subscriber) subscriberView {
	entries := make([]subscriberEntryView, 0, len(sub.Entries))
	for _, e := range sub.Entries {
		_, configured := s.cfg.ServerByID(e.ServerID)
		entries = append(entries, subscriberEntryView{
			ID:               e.ID,
			ServerID:         e.ServerID,
			VlessUserID:      e.VlessUserID,
			Label:            e.Label,
			AddedAt:          e.AddedAt,
			ServerConfigured: configured,
		})
	}
	return subscriberView{
		ID:         sub.ID,
		Name:       sub.Name,
		Note:       sub.Note,
		Token:      sub.Token,
		AccessPath: AccessPath + sub.Token,
		Disabled:   sub.Disabled,
		Entries:    entries,
		CreatedAt:  sub.CreatedAt,
		UpdatedAt:  sub.UpdatedAt,
	}
}

// noStoreSubscriber marks a response that carries share tokens.
//
// listServers already argues that the set of VPN hostnames an operator manages should not
// sit in a shared cache. These bodies carry the capability itself, so the same reasoning
// applies with more force.
func noStoreSubscriber(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	h.Set("Vary", "Cookie")
}

func (s *Server) listSubscribers(w http.ResponseWriter, r *http.Request) {
	noStoreSubscriber(w)
	list := s.store.Subscribers.List()
	out := make([]subscriberView, 0, len(list))
	for i := range list {
		out = append(out, s.subscriberView(&list[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscribers": out})
}

func (s *Server) getSubscriber(w http.ResponseWriter, r *http.Request) {
	noStoreSubscriber(w)
	sub, err := s.store.Subscribers.Get(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.subscriberView(sub))
}

// CreateSubscriberRequest is the body of POST /api/subscribers.
type CreateSubscriberRequest struct {
	Name string `json:"name"`
	Note string `json:"note"`
}

func (s *Server) createSubscriber(w http.ResponseWriter, r *http.Request) {
	var req CreateSubscriberRequest
	if !decode(w, r, &req) {
		return
	}
	sub, err := s.store.Subscribers.Create(req.Name, req.Note, s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.logChange(r, "created a subscriber", "subscriber", sub.ID)
	noStoreSubscriber(w)
	writeJSON(w, http.StatusCreated, s.subscriberView(sub))
}

// PatchSubscriberRequest is the body of PATCH /api/subscribers/{id}.
//
// Pointers throughout, for the reason web/src/api/patch.ts documents on the other side of
// the wire: a plain string cannot distinguish "leave the note alone" from "clear the
// note", and decode() rejects unknown fields, so an absent key is the only way to say the
// former.
type PatchSubscriberRequest struct {
	Name     *string `json:"name"`
	Note     *string `json:"note"`
	Disabled *bool   `json:"disabled"`
}

func (s *Server) patchSubscriber(w http.ResponseWriter, r *http.Request) {
	var req PatchSubscriberRequest
	if !decode(w, r, &req) {
		return
	}
	sub, err := s.store.Subscribers.Update(r.PathValue("id"), store.SubscriberUpdate{
		Name:     req.Name,
		Note:     req.Note,
		Disabled: req.Disabled,
	}, s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.logChange(r, "changed a subscriber", "subscriber", sub.ID)
	noStoreSubscriber(w)
	writeJSON(w, http.StatusOK, s.subscriberView(sub))
}

func (s *Server) deleteSubscriber(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Subscribers.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	s.logChange(r, "deleted a subscriber", "subscriber", id)
	w.WriteHeader(http.StatusNoContent)
}

// AttachEntryRequest is the body of POST /api/subscribers/{id}/entries.
type AttachEntryRequest struct {
	ServerID    string `json:"server_id"`
	VlessUserID string `json:"vless_user_id"`
	Label       string `json:"label"`
}

// attachEntry verifies the reference against the node, and keeps nothing from the answer.
//
// The verification is worth the round trip because a mistyped id is otherwise invisible
// until somebody opens their share link and finds an account missing — by which time the
// operator is not looking. Catching it here means catching it while they can still fix it.
//
// What it must not become is a mirror. Nothing from the node's response is stored: not the
// name, not the uuid, not the quota. A snapshot would be a second source of truth, and it
// would be wrong exactly when it mattered — after a rename, or after a delete.
//
// A node that is down does not block the attach. An operator has to be able to hand
// somebody a link during the incident that made them ask for it.
func (s *Server) attachEntry(w http.ResponseWriter, r *http.Request) {
	var req AttachEntryRequest
	if !decode(w, r, &req) {
		return
	}

	var warning string
	if srv, ok := s.cfg.ServerByID(req.ServerID); ok {
		ctx, cancel := context.WithTimeout(r.Context(), config.HTTPTimeout)
		defer cancel()

		sem := make(chan struct{}, 1)
		_, status, ok := s.nodeGetStatus(ctx, srv, "/api/users/"+url.PathEscape(req.VlessUserID), sem)
		switch {
		case ok:
			// Verified. Nothing kept.
		case status == http.StatusNotFound:
			writeError(w, http.StatusBadRequest, "that node has no account with this id")
			return
		default:
			warning = "the node could not be reached, so this reference was not verified"
		}
	} else {
		warning = "this panel does not currently manage that server, so the account will show as unavailable"
	}

	sub, err := s.store.Subscribers.Attach(r.PathValue("id"), store.NewEntry{
		ServerID:    req.ServerID,
		VlessUserID: req.VlessUserID,
		Label:       req.Label,
	}, s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.logChange(r, "attached an account to a subscriber",
		"subscriber", sub.ID, "server", req.ServerID)

	noStoreSubscriber(w)
	body := map[string]any{"subscriber": s.subscriberView(sub)}
	if warning != "" {
		body["warning"] = warning
	}
	writeJSON(w, http.StatusOK, body)
}

// PatchEntryRequest is the body of PATCH /api/subscribers/{id}/entries/{entryID}.
//
// Only the label. Re-pointing an entry at a different account is a detach and an attach,
// which is honest about the fact that it is a different account — and keeps the audit
// line in the log truthful.
type PatchEntryRequest struct {
	Label *string `json:"label"`
}

func (s *Server) patchEntry(w http.ResponseWriter, r *http.Request) {
	var req PatchEntryRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Label == nil {
		writeError(w, http.StatusBadRequest, "nothing to change")
		return
	}
	sub, err := s.store.Subscribers.UpdateEntry(r.PathValue("id"), r.PathValue("entryID"), *req.Label, s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	noStoreSubscriber(w)
	writeJSON(w, http.StatusOK, s.subscriberView(sub))
}

func (s *Server) detachEntry(w http.ResponseWriter, r *http.Request) {
	sub, err := s.store.Subscribers.Detach(r.PathValue("id"), r.PathValue("entryID"), s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.logChange(r, "detached an account from a subscriber",
		"subscriber", sub.ID, "entry", r.PathValue("entryID"))
	noStoreSubscriber(w)
	writeJSON(w, http.StatusOK, s.subscriberView(sub))
}

// logChange records who did something, matching what the proxy logs for node changes.
//
// The node's own log sees one bearer token shared by every operator, and this file has no
// log of its own at all, so for a subscriber this is the only record that a particular
// person made the change.
func (s *Server) logChange(r *http.Request, msg string, args ...any) {
	operator := "unknown"
	if rec, ok := sessionFrom(r.Context()); ok {
		operator = rec.Username
	}
	s.log.Info(msg, append([]any{"operator", operator}, args...)...)
}

// writeStoreError maps the store's error vocabulary onto status codes, in one place, so
// that no handler gets to decide on its own that a conflict is a 400.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrReadOnly):
		// Unreachable through the daemon, which opens the store writable. If it ever
		// fires, the wiring in serve.go regressed and the message should say so rather
		// than presenting as a mysterious 500.
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
