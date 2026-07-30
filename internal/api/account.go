package api

import (
	"net/http"

	"vlessvmorectl/internal/store"
)

// Self-service credential changes.
//
// Two endpoints rather than one PATCH, because the side effects are not comparable: a new
// password moves the fingerprint every session was issued against and so ends all of them,
// while a new username moves nothing at all.
//
// Only the password change re-checks the current password, and the asymmetry is deliberate.
// That one can lock the real owner out of their own account, so proving you are not just
// holding a borrowed session is worth the friction. A username is not a secret and grants
// nothing — the session already says who you are — so asking again would be ceremony.

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type changeUsernameRequest struct {
	Username string `json:"username"`
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decode(w, r, &req) {
		return
	}
	admin, ok := s.reauthenticate(w, r, req.CurrentPassword)
	if !ok {
		return
	}

	updated, err := s.store.Admins.SetPasswordByID(admin.ID, req.NewPassword, s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// The fingerprint just moved, so every session for this admin is already dead as far
	// as authenticate is concerned. Dropping them here rather than leaving them to be
	// noticed one request at a time is what makes "signed out everywhere" immediate.
	s.sessions.DeleteAdmin(admin.ID)

	// Then hand this request a new one. Signing the operator out of the tab they just used
	// to type both passwords correctly would be punishing the wrong person.
	id, _, err := s.sessions.Issue(updated.ID, updated.Username, updated.Fingerprint(), s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the password was changed, but a new session could not be started: "+err.Error())
		return
	}
	s.setSessionCookie(w, r, id)

	s.log.Info("an administrator changed their own password", "user", updated.Username)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) changeUsername(w http.ResponseWriter, r *http.Request) {
	var req changeUsernameRequest
	if !decode(w, r, &req) {
		return
	}
	admin, ok := s.currentAdmin(w, r)
	if !ok {
		return
	}

	updated, err := s.store.Admins.SetUsername(admin.ID, req.Username, s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// No session work. The id did not move and neither did the password hash, so every
	// session this admin holds stays valid and simply starts reporting the new name.
	s.log.Info("an administrator changed their own username", "from", admin.Username, "to", updated.Username)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"username": updated.Username})
}

// currentAdmin resolves the caller against a freshly read admins.json.
//
// The reload is not optional: both handlers are about to write that file, and saveLocked
// refuses on a copy that has gone stale, so re-reading first is what stops a concurrent
// `users passwd` from turning into a confusing failure.
func (s *Server) currentAdmin(w http.ResponseWriter, r *http.Request) (*store.Admin, bool) {
	rec, ok := sessionFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return nil, false
	}

	if err := s.store.Admins.Reload(); err != nil {
		s.log.Error("reloading admins.json failed; refusing to write over it",
			"path", s.store.Admins.Path(), "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the administrator list")
		return nil, false
	}

	admin, err := s.store.Admins.GetByID(rec.AdminID)
	if err != nil {
		// Deleted from a shell between requireSession and here.
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return nil, false
	}
	return admin, true
}

// reauthenticate is currentAdmin plus proof that the caller knows the password they are
// about to replace.
func (s *Server) reauthenticate(w http.ResponseWriter, r *http.Request, password string) (*store.Admin, bool) {
	admin, ok := s.currentAdmin(w, r)
	if !ok {
		return nil, false
	}

	// Same bucket as the login endpoint, keyed on the id. bcrypt at cost 12 behind a
	// session is still a CPU amplifier, and a stolen session should not get unlimited
	// guesses at the password it needs to change the account.
	now := s.now()
	if !s.limiter.allow(admin.ID, now) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many attempts; wait a minute and try again")
		return nil, false
	}
	if _, err := s.store.Admins.Verify(admin.Username, password); err != nil {
		s.limiter.fail(admin.ID, now)
		writeError(w, http.StatusForbidden, "that is not your current password")
		return nil, false
	}
	s.limiter.succeed(admin.ID)
	return admin, true
}
