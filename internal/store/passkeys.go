package store

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// PasskeysFile holds WebAuthn credentials, grouped by administrator.
const PasskeysFile = "passkeys.json"

// ErrPasskeysReadOnly mirrors ErrReadOnly: this file is written only by the daemon.
var ErrPasskeysReadOnly = errors.New("passkeys.json is written only by the running panel")

const (
	// MaxPasskeysPerAdmin covers a laptop, a phone, a hardware key and a spare, and caps
	// what one authenticated caller can write.
	MaxPasskeysPerAdmin = 8
	MaxPasskeyLabelLen  = 64

	handleBytes = 32
	// Spec caps a credential id at 1023 bytes.
	maxCredentialIDLen = 1023
	maxPublicKeyLen    = 1024
)

// PasskeyOwner is one administrator's credentials, named by the id that never changes.
//
// Note what is *not* here: no username, and no "was this username recreated?" guard. A
// deleted administrator's replacement gets a fresh random id, so an entry left behind can
// never resolve to them.
type PasskeyOwner struct {
	AdminID string `json:"admin_id"`

	// Handle is the WebAuthn user handle: handleBytes of randomness, base64url.
	//
	// Deliberately not AdminID. A user handle is handed to the authenticator and stored on
	// the device, and the spec is explicit that it must not carry personal data — but for
	// an administrator who predates ids, AdminID *is* their username.
	//
	// It must also be stable per administrator: authenticators key on rpId + user.id, so a
	// second enrolment on a device already used replaces the old credential instead of
	// leaving two panel entries in somebody's keychain.
	Handle string `json:"handle"`

	Credentials []PasskeyCredential `json:"credentials"`
}

// PasskeyCredential is one enrolled authenticator.
//
// Every field here is public information — a COSE public key, an identifier and some
// labels. Unlike subscribers.json this file holds no capability: somebody who reads it
// cannot sign an assertion, because the private key never left the authenticator.
type PasskeyCredential struct {
	// ID is this panel's own short handle, the same shape as a subscriber id. It is what
	// appears in PATCH/DELETE /api/passkeys/{id}, so a credential id never reaches a URL
	// bar, browser history or a screen recording. Same argument as config.originID.
	ID string `json:"id"`

	// CredentialID is what the authenticator calls itself, base64url.
	CredentialID string `json:"credential_id"`

	Label string `json:"label"`

	// PublicKey is the COSE key, base64url. Verification needs nothing else.
	PublicKey string `json:"public_key"`

	// Algorithm is the COSE identifier, kept for display and diagnostics. Never used to
	// choose a verifier — the algorithm inside the COSE key is what governs that.
	Algorithm int `json:"algorithm"`

	AttestationType string   `json:"attestation_type"`
	Transports      []string `json:"transports,omitempty"`
	AAGUID          string   `json:"aaguid,omitempty"`

	SignCount uint32 `json:"sign_count"`

	// BackupState is "this credential is synced to a keychain", which is what the UI shows
	// as Synced rather than This device.
	BackupEligible bool `json:"backup_eligible"`
	BackupState    bool `json:"backup_state"`
	UserVerified   bool `json:"user_verified"`

	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
}

type passkeysDoc struct {
	Version int            `json:"version"`
	Owners  []PasskeyOwner `json:"owners"`
}

// Passkeys is the JSON-backed credential store.
type Passkeys struct {
	path     string
	writable bool

	mu   sync.RWMutex
	list []PasskeyOwner

	// Rebuilt wholesale by reindexLocked after every mutation, for the reason
	// Subscribers gives: a half-updated index here resolves one person's credential to
	// another person's account.
	byAdminID map[string]int
	byHandle  map[string]int
	byCredID  map[string]passkeyRef

	loadedSize    int64
	loadedModTime time.Time
}

// passkeyRef locates one credential: which owner, and which of their credentials.
type passkeyRef struct{ owner, cred int }

// OpenPasskeys loads passkeys.json, treating a missing file as empty.
//
// Fatal on a corrupt file, for the reason OpenSubscribers gives: a lenient load would be
// followed by a whole-file rewrite, and the recoverable file would be gone.
func OpenPasskeys(path string, writable bool) (*Passkeys, error) {
	p := &Passkeys{path: path, writable: writable}
	if err := p.load(); err != nil {
		return nil, err
	}
	return p, nil
}

// load reads and validates the whole file before swapping it in. Caller must not hold the lock.
func (p *Passkeys) load() error {
	var doc passkeysDoc
	found, err := readJSON(p.path, &doc)
	if err != nil {
		return err
	}
	if found && doc.Version != jsonVersion {
		return fmt.Errorf("%s: unsupported version %d, this build understands %d", p.path, doc.Version, jsonVersion)
	}

	admins := make(map[string]bool, len(doc.Owners))
	handles := make(map[string]bool, len(doc.Owners))
	// Credential ids and panel ids are unique across the *whole file*, not per owner: a
	// duplicate would make a lookup by either ambiguous.
	credIDs := make(map[string]bool)
	panelIDs := make(map[string]bool)

	for i, o := range doc.Owners {
		where := fmt.Sprintf("%s: owners[%d]", p.path, i)
		if o.AdminID == "" {
			return fmt.Errorf("%s: admin_id is empty", where)
		}
		if admins[o.AdminID] {
			return fmt.Errorf("%s: duplicate admin_id %q", where, o.AdminID)
		}
		admins[o.AdminID] = true

		if err := checkBase64Len(o.Handle, handleBytes, handleBytes); err != nil {
			return fmt.Errorf("%s: handle: %w", where, err)
		}
		if handles[o.Handle] {
			return fmt.Errorf("%s: duplicate handle", where)
		}
		handles[o.Handle] = true

		if len(o.Credentials) > MaxPasskeysPerAdmin {
			return fmt.Errorf("%s: %d credentials, more than the %d allowed", where, len(o.Credentials), MaxPasskeysPerAdmin)
		}
		for j, c := range o.Credentials {
			cw := fmt.Sprintf("%s: credentials[%d]", where, j)
			if c.ID == "" {
				return fmt.Errorf("%s: id is empty", cw)
			}
			if panelIDs[c.ID] {
				return fmt.Errorf("%s: duplicate id %q", cw, c.ID)
			}
			panelIDs[c.ID] = true

			if err := checkBase64Len(c.CredentialID, 1, maxCredentialIDLen); err != nil {
				return fmt.Errorf("%s: credential_id: %w", cw, err)
			}
			if credIDs[c.CredentialID] {
				return fmt.Errorf("%s: duplicate credential_id", cw)
			}
			credIDs[c.CredentialID] = true

			if err := checkBase64Len(c.PublicKey, 1, maxPublicKeyLen); err != nil {
				return fmt.Errorf("%s: public_key: %w", cw, err)
			}
			if c.Algorithm == 0 {
				return fmt.Errorf("%s: algorithm is missing", cw)
			}
			if err := validatePasskeyLabel(c.Label); err != nil {
				return fmt.Errorf("%s: %w", cw, err)
			}
			if c.CreatedAt.IsZero() {
				return fmt.Errorf("%s: created_at is missing", cw)
			}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.list = doc.Owners
	p.reindexLocked()
	p.stampLoadedLocked()
	return nil
}

// Path returns the backing file.
func (p *Passkeys) Path() string { return p.path }

// Count is the number of credentials, not owners — "how many passkeys exist here".
func (p *Passkeys) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	n := 0
	for _, o := range p.list {
		n += len(o.Credentials)
	}
	return n
}

// List returns one administrator's credentials, oldest first.
func (p *Passkeys) List(adminID string) []PasskeyCredential {
	p.mu.RLock()
	defer p.mu.RUnlock()
	i, ok := p.byAdminID[adminID]
	if !ok {
		return nil
	}
	return slices.Clone(p.list[i].Credentials)
}

// Owners returns every entry, for the CLI's listing.
func (p *Passkeys) Owners() []PasskeyOwner {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]PasskeyOwner, 0, len(p.list))
	for _, o := range p.list {
		o.Credentials = slices.Clone(o.Credentials)
		out = append(out, o)
	}
	return out
}

// Handle returns an administrator's user handle, if they have enrolled anything.
func (p *Passkeys) Handle(adminID string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	i, ok := p.byAdminID[adminID]
	if !ok {
		return "", false
	}
	return p.list[i].Handle, true
}

// ByHandle resolves a WebAuthn user handle to its owner. This is the discoverable-login
// lookup: the browser sends the handle, and it names the administrator.
func (p *Passkeys) ByHandle(handle string) (PasskeyOwner, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	i, ok := p.byHandle[handle]
	if !ok {
		return PasskeyOwner{}, false
	}
	return p.cloneLocked(i), true
}

// ByCredentialID resolves an authenticator's credential id to its owner and credential.
func (p *Passkeys) ByCredentialID(credentialID string) (PasskeyOwner, PasskeyCredential, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ref, ok := p.byCredID[credentialID]
	if !ok {
		return PasskeyOwner{}, PasskeyCredential{}, false
	}
	return p.cloneLocked(ref.owner), p.list[ref.owner].Credentials[ref.cred], true
}

// NewHandle mints an unused user handle. It is not stored: registration carries it on the
// pending ceremony and only Add writes it, so an abandoned enrolment leaves nothing behind.
func (p *Passkeys) NewHandle() (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for range 8 {
		b := make([]byte, handleBytes)
		if _, err := rand.Read(b); err != nil {
			return "", fmt.Errorf("generate passkey handle: %w", err)
		}
		h := base64.RawURLEncoding.EncodeToString(b)
		if _, taken := p.byHandle[h]; !taken {
			return h, nil
		}
	}
	return "", errors.New("could not generate an unused passkey handle")
}

// Add attaches a credential, creating the owner entry when this is their first.
//
// handle is used only for a new entry. An existing entry with a different one means two
// first-time enrolments raced; that is a conflict, because the authenticator has already
// stored the handle it was given.
func (p *Passkeys) Add(adminID, handle string, cred PasskeyCredential, now time.Time) (*PasskeyCredential, error) {
	if !p.writable {
		return nil, ErrPasskeysReadOnly
	}
	if err := validatePasskeyLabel(cred.Label); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, dup := p.byCredID[cred.CredentialID]; dup {
		return nil, fmt.Errorf("that authenticator already has a passkey for this panel: %w", ErrConflict)
	}

	id, err := p.freshCredentialIDLocked()
	if err != nil {
		return nil, err
	}
	cred.ID = id
	cred.CreatedAt = now.UTC().Truncate(time.Second)
	cred.LastUsedAt = time.Time{}

	before := slices.Clone(p.list)
	i, existing := p.byAdminID[adminID]
	if existing {
		if p.list[i].Handle != handle {
			return nil, fmt.Errorf("another enrolment for this administrator finished first; try again: %w", ErrConflict)
		}
		if len(p.list[i].Credentials) >= MaxPasskeysPerAdmin {
			return nil, fmt.Errorf("%w: no more than %d passkeys per administrator", ErrInvalid, MaxPasskeysPerAdmin)
		}
		p.list[i].Credentials = append(slices.Clone(p.list[i].Credentials), cred)
	} else {
		if err := checkBase64Len(handle, handleBytes, handleBytes); err != nil {
			return nil, fmt.Errorf("%w: handle: %v", ErrInvalid, err)
		}
		p.list = append(p.list, PasskeyOwner{
			AdminID:     adminID,
			Handle:      handle,
			Credentials: []PasskeyCredential{cred},
		})
	}

	if err := p.commitLocked(); err != nil {
		p.list = before
		p.reindexLocked()
		return nil, err
	}
	out := cred
	return &out, nil
}

// Rename relabels a credential. Scoped to one administrator, so authorization is
// structural rather than something a handler has to remember to check.
func (p *Passkeys) Rename(adminID, id, label string, now time.Time) (*PasskeyCredential, error) {
	if !p.writable {
		return nil, ErrPasskeysReadOnly
	}
	// Empty is allowed, and means "no name of its own" — the panel goes back to calling this
	// after the authenticator it lives in. Clearing the field is how somebody undoes a rename,
	// so refusing it would leave them typing the provider's name back in by hand.
	if err := validatePasskeyLabel(label); err != nil {
		return nil, err
	}
	return p.mutateCredential(adminID, id, func(c *PasskeyCredential) {
		c.Label = strings.TrimSpace(label)
	})
}

// Remove drops a credential, and the owner entry with it when it was the last one.
func (p *Passkeys) Remove(adminID, id string) error {
	if !p.writable {
		return ErrPasskeysReadOnly
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	oi, ok := p.byAdminID[adminID]
	if !ok {
		return fmt.Errorf("passkey %q: %w", id, ErrNotFound)
	}
	ci := slices.IndexFunc(p.list[oi].Credentials, func(c PasskeyCredential) bool { return c.ID == id })
	if ci < 0 {
		return fmt.Errorf("passkey %q: %w", id, ErrNotFound)
	}

	before := slices.Clone(p.list)
	creds := slices.Delete(slices.Clone(p.list[oi].Credentials), ci, ci+1)
	if len(creds) == 0 {
		// The handle goes with it. A later enrolment mints a fresh one, which is right:
		// there is nothing left for the old one to be stable for.
		p.list = slices.Delete(slices.Clone(p.list), oi, oi+1)
	} else {
		p.list = slices.Clone(p.list)
		p.list[oi].Credentials = creds
	}
	if err := p.commitLocked(); err != nil {
		p.list = before
		p.reindexLocked()
		return err
	}
	return nil
}

// RecordUse updates the counter and last_used_at after a verified assertion.
//
// Reached only by a valid signature, which costs a private key, so it needs no throttle.
func (p *Passkeys) RecordUse(credentialID string, signCount uint32, backupState bool, now time.Time) error {
	if !p.writable {
		return ErrPasskeysReadOnly
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	ref, ok := p.byCredID[credentialID]
	if !ok {
		return fmt.Errorf("passkey: %w", ErrNotFound)
	}
	before := slices.Clone(p.list)
	creds := slices.Clone(p.list[ref.owner].Credentials)
	creds[ref.cred].SignCount = signCount
	creds[ref.cred].BackupState = backupState
	creds[ref.cred].LastUsedAt = now.UTC().Truncate(time.Second)
	p.list = slices.Clone(p.list)
	p.list[ref.owner].Credentials = creds

	if err := p.commitLocked(); err != nil {
		p.list = before
		p.reindexLocked()
		return err
	}
	return nil
}

// PruneOrphans drops entries whose administrator no longer exists.
//
// Called at startup and from the authenticated listing, never from the login path: an
// unauthenticated request must not be able to make this process rewrite a file.
func (p *Passkeys) PruneOrphans(exists func(adminID string) bool) (int, error) {
	if !p.writable {
		return 0, ErrPasskeysReadOnly
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	kept := make([]PasskeyOwner, 0, len(p.list))
	dropped := 0
	for _, o := range p.list {
		if exists(o.AdminID) {
			kept = append(kept, o)
			continue
		}
		dropped += len(o.Credentials)
	}
	if dropped == 0 {
		return 0, nil
	}

	before := p.list
	p.list = kept
	if err := p.commitLocked(); err != nil {
		p.list = before
		p.reindexLocked()
		return 0, err
	}
	return dropped, nil
}

// mutateCredential applies fn to a copy and swaps it in only if the save succeeds.
func (p *Passkeys) mutateCredential(adminID, id string, fn func(*PasskeyCredential)) (*PasskeyCredential, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	oi, ok := p.byAdminID[adminID]
	if !ok {
		return nil, fmt.Errorf("passkey %q: %w", id, ErrNotFound)
	}
	ci := slices.IndexFunc(p.list[oi].Credentials, func(c PasskeyCredential) bool { return c.ID == id })
	if ci < 0 {
		return nil, fmt.Errorf("passkey %q: %w", id, ErrNotFound)
	}

	before := slices.Clone(p.list)
	creds := slices.Clone(p.list[oi].Credentials)
	fn(&creds[ci])
	p.list = slices.Clone(p.list)
	p.list[oi].Credentials = creds

	if err := p.commitLocked(); err != nil {
		p.list = before
		p.reindexLocked()
		return nil, err
	}
	out := creds[ci]
	return &out, nil
}

func (p *Passkeys) commitLocked() error {
	p.reindexLocked()
	return p.saveLocked()
}

// saveLocked rewrites the file, refusing to clobber an edit made underneath us. Same
// argument as Subscribers.saveLocked: this process's memory is authoritative, so a file
// that moved means somebody else is writing and merging would be guesswork.
func (p *Passkeys) saveLocked() error {
	if fi, err := os.Stat(p.path); err == nil {
		if fi.Size() != p.loadedSize || !fi.ModTime().Equal(p.loadedModTime) {
			return fmt.Errorf("%s changed on disk since this process read it; "+
				"restart the panel to pick that edit up (nothing was written)", p.path)
		}
	}
	if err := writeJSONAtomic(p.path, passkeysDoc{Version: jsonVersion, Owners: p.list}); err != nil {
		return err
	}
	p.stampLoadedLocked()
	return nil
}

func (p *Passkeys) reindexLocked() {
	p.byAdminID = make(map[string]int, len(p.list))
	p.byHandle = make(map[string]int, len(p.list))
	p.byCredID = make(map[string]passkeyRef, len(p.list))
	for i, o := range p.list {
		p.byAdminID[o.AdminID] = i
		p.byHandle[o.Handle] = i
		for j, c := range o.Credentials {
			p.byCredID[c.CredentialID] = passkeyRef{owner: i, cred: j}
		}
	}
}

func (p *Passkeys) stampLoadedLocked() {
	if fi, err := os.Stat(p.path); err == nil {
		p.loadedSize, p.loadedModTime = fi.Size(), fi.ModTime()
	} else {
		p.loadedSize, p.loadedModTime = 0, time.Time{}
	}
}

func (p *Passkeys) cloneLocked(i int) PasskeyOwner {
	o := p.list[i]
	o.Credentials = slices.Clone(o.Credentials)
	return o
}

// freshCredentialIDLocked mints a panel id no credential holds. Caller holds the lock.
func (p *Passkeys) freshCredentialIDLocked() (string, error) {
	taken := make(map[string]bool)
	for _, o := range p.list {
		for _, c := range o.Credentials {
			taken[c.ID] = true
		}
	}
	for range 8 {
		id, err := randomID()
		if err != nil {
			return "", err
		}
		if !taken[id] {
			return id, nil
		}
	}
	return "", errors.New("could not generate an unused passkey id")
}

// validatePasskeyLabel checks the shape of a name, and not whether there is one.
//
// Empty is a state rather than an error, at enrolment and at rename alike: a credential nobody
// has named has no name of its own, and the panel shows the authenticator's until somebody gives
// it one. Storing the enrolling authenticator's name instead would freeze it — a provider the
// upstream list later renames, or one we learn the name of after the fact, would be stuck with
// whatever we knew that day.
func validatePasskeyLabel(label string) error {
	label = strings.TrimSpace(label)
	if len(label) > MaxPasskeyLabelLen {
		return fmt.Errorf("%w: the name is longer than %d characters", ErrInvalid, MaxPasskeyLabelLen)
	}
	// Rendered in the panel and printed by `passkeys ls`; a control character in either
	// is somebody else's problem to debug.
	for _, r := range label {
		if r < ' ' || r == 0x7f {
			return fmt.Errorf("%w: the name must not contain control characters", ErrInvalid)
		}
	}
	return nil
}

// checkBase64Len decodes as base64url and bounds the result.
func checkBase64Len(s string, min, max int) error {
	if s == "" {
		return errors.New("is empty")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("is not base64url: %w", err)
	}
	if len(b) < min || len(b) > max {
		return fmt.Errorf("decodes to %d bytes, want between %d and %d", len(b), min, max)
	}
	return nil
}
