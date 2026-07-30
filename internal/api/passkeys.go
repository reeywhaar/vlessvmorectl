package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"vlessvmorectl/internal/config"
	"vlessvmorectl/internal/store"
)

// Passkeys: a second way to sign in, not a second factor.
//
// A passkey stands alongside the password rather than on top of it, which is why user
// verification is "preferred" and not "required" — a hardware key with no PIN is a fine
// answer here, exactly as it would be for the password it replaces.
//
// Every endpoint in this file operates only on the caller's own administrator id. There is
// no way to see or remove somebody else's passkey; revoking another administrator is
// `users rm`. That removes an authorization surface rather than guarding one.

// newRelyingParty builds the library's configuration from the validated origin.
func newRelyingParty(p *config.Passkey) (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{
		RPID:          p.RPID,
		RPDisplayName: config.RPDisplayName,
		// Verbatim, and exactly one: the library compares clientDataJSON.origin against
		// this by equality. A pattern here is how an origin check rots.
		RPOrigins: []string{p.Origin},

		// "none". There is no trust anchor to check an attestation statement against, and
		// no metadata service to consult, so asking for one would be theatre. The library
		// still parses the attestation object to extract the COSE public key, which is the
		// part that matters.
		AttestationPreference: protocol.PreferNoAttestation,

		AuthenticatorSelection: protocol.AuthenticatorSelection{
			// Discoverable, so signing in needs no username: the browser hands back a
			// credential and its user handle, and that names the administrator.
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationPreferred,
			// AuthenticatorAttachment deliberately unset: Touch ID and a YubiKey are both
			// acceptable answers.
		},
	})
}

// passkeyUser presents one administrator to the library.
//
// Not store.Admin: WebAuthnID has to be an opaque handle that never carries personal data,
// and an administrator's id may literally be their old username. The handle lives in
// passkeys.json instead; see store.PasskeyOwner.
type passkeyUser struct {
	handle      []byte
	username    string
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u *passkeyUser) WebAuthnName() string                       { return u.username }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.username }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// passkeyView is what the API exposes.
//
// A projection with no field for the credential id, the public key or the user handle —
// none of them is a secret, but none of them is the browser's business either, and a type
// with nowhere to put a value cannot leak it. Same argument as serverEntry.
type passkeyView struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Algorithm  string    `json:"algorithm"`
	Synced     bool      `json:"synced"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
}

func viewPasskey(c store.PasskeyCredential) passkeyView {
	return passkeyView{
		ID:         c.ID,
		Label:      c.Label,
		Algorithm:  algorithmName(c.Algorithm),
		Synced:     c.BackupState,
		CreatedAt:  c.CreatedAt,
		LastUsedAt: c.LastUsedAt,
	}
}

// algorithmName renders a COSE identifier for display. Never used to choose a verifier.
func algorithmName(alg int) string {
	switch alg {
	case -7:
		return "ES256"
	case -8:
		return "Ed25519"
	case -35:
		return "ES384"
	case -36:
		return "ES512"
	case -257:
		return "RS256"
	default:
		return "COSE " + strconv.Itoa(alg)
	}
}

// ---- conversions between the library's credential and ours ----

// toLibraryCredential rebuilds what verification needs. The attestation statement is
// deliberately not stored, so Attestation stays empty — nothing on the assertion path
// reads it.
func toLibraryCredential(c store.PasskeyCredential) (webauthn.Credential, error) {
	id, err := base64.RawURLEncoding.DecodeString(c.CredentialID)
	if err != nil {
		return webauthn.Credential{}, err
	}
	key, err := base64.RawURLEncoding.DecodeString(c.PublicKey)
	if err != nil {
		return webauthn.Credential{}, err
	}
	aaguid, err := base64.RawURLEncoding.DecodeString(c.AAGUID)
	if err != nil {
		aaguid = nil
	}
	transports := make([]protocol.AuthenticatorTransport, 0, len(c.Transports))
	for _, t := range c.Transports {
		transports = append(transports, protocol.AuthenticatorTransport(t))
	}
	return webauthn.Credential{
		ID:              id,
		PublicKey:       key,
		AttestationType: c.AttestationType,
		Transport:       transports,
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   c.UserVerified,
			BackupEligible: c.BackupEligible,
			BackupState:    c.BackupState,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:    aaguid,
			SignCount: c.SignCount,
		},
	}, nil
}

// fromLibraryCredential projects a freshly verified credential onto what we keep.
func fromLibraryCredential(c *webauthn.Credential, label string) store.PasskeyCredential {
	transports := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		if t != "" {
			transports = append(transports, string(t))
		}
	}
	out := store.PasskeyCredential{
		CredentialID:    base64.RawURLEncoding.EncodeToString(c.ID),
		Label:           strings.TrimSpace(label),
		PublicKey:       base64.RawURLEncoding.EncodeToString(c.PublicKey),
		Algorithm:       int(c.Attestation.PublicKeyAlgorithm),
		AttestationType: c.AttestationType,
		Transports:      transports,
		SignCount:       c.Authenticator.SignCount,
		BackupEligible:  c.Flags.BackupEligible,
		BackupState:     c.Flags.BackupState,
		UserVerified:    c.Flags.UserVerified,
	}
	if len(c.Authenticator.AAGUID) > 0 {
		out.AAGUID = base64.RawURLEncoding.EncodeToString(c.Authenticator.AAGUID)
	}
	// A COSE key always states its algorithm; the attestation object is where the library
	// reports it. ES256 is what every platform authenticator produces, and recording 0
	// would fail the store's validation for no good reason.
	if out.Algorithm == 0 {
		out.Algorithm = -7
	}
	return out
}

// ---- authenticated endpoints ----

// userFor builds the library's view of the caller, creating no state.
func (s *Server) userFor(admin *store.Admin, handle string) (*passkeyUser, error) {
	raw, err := base64.RawURLEncoding.DecodeString(handle)
	if err != nil {
		return nil, err
	}
	creds := []webauthn.Credential{}
	for _, c := range s.store.Passkeys.List(admin.ID) {
		lc, err := toLibraryCredential(c)
		if err != nil {
			// A row that will not decode cannot verify anything either. Skipping keeps
			// the rest usable; refusing would lock the administrator out over one bad row.
			s.log.Error("a stored passkey could not be decoded and was skipped",
				"passkey", c.ID, "error", err)
			continue
		}
		creds = append(creds, lc)
	}
	return &passkeyUser{handle: raw, username: admin.Username, credentials: creds}, nil
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	rec, _ := sessionFrom(r.Context())

	// An authenticated, human-triggered read is the right place to tidy up after a
	// `users rm`. Never on the login path, which is unauthenticated.
	if n, err := s.store.PrunePasskeys(); err != nil {
		s.log.Error("could not prune orphaned passkeys", "error", err)
	} else if n > 0 {
		s.log.Info("dropped passkeys belonging to deleted administrators", "passkeys", n)
	}

	creds := s.store.Passkeys.List(rec.AdminID)
	out := make([]passkeyView, 0, len(creds))
	for _, c := range creds {
		out = append(out, viewPasskey(c))
	}
	noStorePasskeys(w)
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": out})
}

func (s *Server) beginRegisterPasskey(w http.ResponseWriter, r *http.Request) {
	rec, _ := sessionFrom(r.Context())
	admin, err := s.store.Admins.GetByID(rec.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	existing := s.store.Passkeys.List(admin.ID)
	if len(existing) >= store.MaxPasskeysPerAdmin {
		writeError(w, http.StatusConflict,
			"you already have the maximum number of passkeys; remove one first")
		return
	}

	// Stable per administrator, so re-enrolling a device the operator already used replaces
	// its credential rather than adding a second keychain entry.
	handle, ok := s.store.Passkeys.Handle(admin.ID)
	if !ok {
		handle, err = s.store.Passkeys.NewHandle()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	user, err := s.userFor(admin, handle)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read your passkeys: "+err.Error())
		return
	}

	// Excluding what they already have is what makes the browser say "this device already
	// has a passkey" instead of silently creating a duplicate.
	exclude := make([]protocol.CredentialDescriptor, 0, len(user.credentials))
	for _, c := range user.credentials {
		exclude = append(exclude, c.Descriptor())
	}

	creation, sd, err := s.webauthn.BeginRegistration(user, webauthn.WithExclusions(exclude))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start enrolment: "+err.Error())
		return
	}
	state, err := s.challenges.issue(sd, admin.ID, handle, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	noStorePasskeys(w)
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "options": creation.Response})
}

type finishRegisterPasskeyRequest struct {
	State string `json:"state"`
	Label string `json:"label"`
	// Raw, so DisallowUnknownFields still guards our envelope while leaving the browser's
	// credential alone — it carries fields we do not enumerate, and browsers add more.
	Credential json.RawMessage `json:"credential"`
}

func (s *Server) finishRegisterPasskey(w http.ResponseWriter, r *http.Request) {
	rec, _ := sessionFrom(r.Context())

	var req finishRegisterPasskeyRequest
	if !decode(w, r, &req) {
		return
	}
	admin, err := s.store.Admins.GetByID(rec.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Keyed on the administrator, so one signed-in operator cannot finish another's.
	pending, ok := s.challenges.take(req.State, admin.ID, s.now())
	if !ok {
		writeError(w, http.StatusBadRequest,
			"that enrolment expired or was never started; press Add a passkey again")
		return
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
	if err != nil {
		// The caller is authenticated and debugging their own browser, so the library's
		// message is useful here rather than a disclosure.
		writeError(w, http.StatusBadRequest, "that is not a usable WebAuthn credential: "+err.Error())
		return
	}

	user, err := s.userFor(admin, pending.handle)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cred, err := s.webauthn.CreateCredential(user, pending.session, parsed)
	if err != nil {
		s.log.Warn("a passkey enrolment failed verification", "user", admin.Username, "error", err)
		writeError(w, http.StatusBadRequest, "that passkey could not be verified: "+err.Error())
		return
	}

	stored, err := s.store.Passkeys.Add(admin.ID, pending.handle, fromLibraryCredential(cred, req.Label), s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// The short id only. Never the credential id or the key.
	s.log.Info("an administrator enrolled a passkey",
		"user", admin.Username, "passkey", stored.ID, "algorithm", algorithmName(stored.Algorithm))
	noStorePasskeys(w)
	writeJSON(w, http.StatusCreated, map[string]any{"passkey": viewPasskey(*stored)})
}

type renamePasskeyRequest struct {
	Label string `json:"label"`
}

func (s *Server) renamePasskey(w http.ResponseWriter, r *http.Request) {
	rec, _ := sessionFrom(r.Context())
	var req renamePasskeyRequest
	if !decode(w, r, &req) {
		return
	}
	updated, err := s.store.Passkeys.Rename(rec.AdminID, r.PathValue("id"), req.Label, s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	noStorePasskeys(w)
	writeJSON(w, http.StatusOK, map[string]any{"passkey": viewPasskey(*updated)})
}

func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	rec, _ := sessionFrom(r.Context())
	id := r.PathValue("id")
	if err := s.store.Passkeys.Remove(rec.AdminID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	// No special case for the last one, or for the one this session was obtained with: the
	// password always works, and a session is independent of the credential that made it.
	s.log.Info("an administrator removed a passkey", "user", rec.Username, "passkey", id)
	w.WriteHeader(http.StatusNoContent)
}

// ---- unauthenticated endpoints ----

// passkeyLoginRefusal is the single answer to every authentication failure below.
//
// One message for an unknown credential, a bad signature and a deleted administrator alike,
// for the same reason login says "invalid username or password" to both halves.
const passkeyLoginRefusal = "that passkey was not accepted"

func (s *Server) beginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	if !s.limiter.allowGlobal(now) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many attempts; wait a minute and try again")
		return
	}

	// The most likely misconfiguration, and this is the one moment it is diagnosable: the
	// browser is about to refuse a ceremony whose rpId does not match where it is. Logged,
	// never acted on — Host is client-supplied and nothing here trusts it.
	s.warnOnHostMismatch(r, now)

	assertion, sd, err := s.webauthn.BeginDiscoverableMediatedLogin(protocol.MediationConditional)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start a passkey sign-in: "+err.Error())
		return
	}
	state, err := s.challenges.issue(sd, "", "", now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	noStorePasskeys(w)
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "options": assertion.Response})
}

type finishPasskeyLoginRequest struct {
	State      string          `json:"state"`
	Credential json.RawMessage `json:"credential"`
}

func (s *Server) finishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	if !s.limiter.allowGlobal(now) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many attempts; wait a minute and try again")
		return
	}

	var req finishPasskeyLoginRequest
	if !decode(w, r, &req) {
		return
	}
	pending, ok := s.challenges.take(req.State, "", now)
	if !ok {
		writeError(w, http.StatusUnauthorized, passkeyLoginRefusal)
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Credential)
	if err != nil {
		// No detail: this caller is anonymous.
		writeError(w, http.StatusBadRequest, "that is not a WebAuthn assertion")
		return
	}

	// So that a `users rm` from a shell takes effect here too, throttled the same way the
	// auth middleware throttles it.
	if err := s.store.Admins.ReloadIfChanged(now); err != nil {
		s.log.Error("reloading admins.json failed; continuing with the last good copy",
			"path", s.store.Admins.Path(), "error", err)
	}

	// resolved is set by the handler below, so the outer scope learns who this is even
	// though the library hands back its own User.
	var resolved *store.Admin
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		owner, ok := s.store.Passkeys.ByHandle(base64.RawURLEncoding.EncodeToString(userHandle))
		if !ok {
			return nil, errors.New("no such passkey handle")
		}
		// admins.json is the authority on whether the account exists, not the credential.
		// Without this, deleting an administrator would leave a working sign-in.
		admin, err := s.store.Admins.GetByID(owner.AdminID)
		if err != nil {
			return nil, errors.New("that passkey belongs to an administrator who no longer exists")
		}
		// The credential the browser presented has to be one of this owner's, so a
		// mismatched handle/credential pair fails here with a clear line in the log rather
		// than somewhere inside the library.
		wanted := base64.RawURLEncoding.EncodeToString(rawID)
		if !hasCredential(owner, wanted) {
			return nil, errors.New("that passkey does not belong to that handle")
		}
		resolved = admin
		return s.userFor(admin, owner.Handle)
	}

	_, cred, err := s.webauthn.ValidatePasskeyLogin(handler, pending.session, parsed)
	if err != nil {
		s.log.Warn("a passkey sign-in was refused", "error", err)
		writeError(w, http.StatusUnauthorized, passkeyLoginRefusal)
		return
	}
	if resolved == nil {
		// Unreachable: the library cannot verify without calling the handler.
		writeError(w, http.StatusUnauthorized, passkeyLoginRefusal)
		return
	}

	// A counter that went backwards. Logged, not refused: a credential synced across two
	// devices can regress it without being cloned, and turning that into an unexplained
	// sign-in failure would be worse than the risk it flags. There is a test pinning this,
	// so a library release that starts refusing on its own shows up as a red test rather
	// than as an operator who cannot get in.
	if cred.Authenticator.CloneWarning {
		s.log.Warn("a passkey's signature counter went backwards, which can mean a cloned authenticator",
			"user", resolved.Username, "sign_count", cred.Authenticator.SignCount)
	}

	if err := s.store.Passkeys.RecordUse(
		base64.RawURLEncoding.EncodeToString(cred.ID),
		cred.Authenticator.SignCount, cred.Flags.BackupState, now,
	); err != nil {
		// The signature already verified. Failing a good sign-in because the disk is full
		// is the wrong end of that trade; see session.Table.flushLocked.
		s.log.Error("could not record a passkey's use", "error", err)
	}

	// The ordinary session path, with the password-hash fingerprint. So every existing
	// revocation mechanism keeps working unchanged — including the surprising one: changing
	// the password still signs out sessions obtained with a passkey.
	id, _, err := s.sessions.Issue(resolved.ID, resolved.Username, resolved.Fingerprint(), now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start a session: "+err.Error())
		return
	}
	s.setSessionCookie(w, r, id)
	s.warnIfPlaintext(r, resolved.Username)

	s.log.Info("an administrator signed in with a passkey", "user", resolved.Username)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{"username": resolved.Username},
	})
}

func hasCredential(o store.PasskeyOwner, credentialID string) bool {
	for _, c := range o.Credentials {
		if c.CredentialID == credentialID {
			return true
		}
	}
	return false
}

// warnOnHostMismatch says the useful thing once per window when the panel is reached at a
// host the configured relying party does not cover.
func (s *Server) warnOnHostMismatch(r *http.Request, now time.Time) {
	if s.cfg.Passkey == nil || r.Host == "" {
		return
	}
	if strings.EqualFold(r.Host, s.cfg.Passkey.RPID) || config.NormalizeOrigin("https", r.Host) == s.cfg.Passkey.Origin {
		return
	}
	host, _, _ := strings.Cut(r.Host, ":")
	if strings.EqualFold(host, s.cfg.Passkey.RPID) {
		return
	}
	if s.access.shouldLog(now) {
		s.log.Warn("a passkey sign-in was started at a host the configured origin does not cover, so the browser will refuse it",
			"request_host", r.Host, "configured", s.cfg.Passkey.Origin, "variable", config.PasskeyOriginEnv)
	}
}

// noStorePasskeys applies the same cache hygiene the other credential-adjacent responses
// use. None of this is a secret, but none of it belongs in a shared cache either.
func noStorePasskeys(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	h.Set("Vary", "Cookie")
}
