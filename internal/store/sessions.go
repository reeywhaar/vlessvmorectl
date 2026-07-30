package store

import (
	"fmt"
	"path/filepath"
	"time"
)

// SessionsFile holds live sessions, keyed by a hash of the cookie value.
const SessionsFile = "sessions.json"

// PersistedSession is one session as it sits on disk.
//
// Hash is hex sha256 of the cookie value, never the value itself — the same discipline
// as tokens.json in the sibling project, and the reason this file is safe to write at
// all. Somebody who reads it cannot replay any of it; they would need a preimage first.
type PersistedSession struct {
	Hash string `json:"hash"`

	// AdminID is who this session belongs to. Rows written before it existed are dropped
	// on restore rather than guessed at; see session.New.
	AdminID string `json:"admin_id"`

	// Username is a label for logs and hand-inspection only. AdminID is the identity.
	Username string `json:"username"`

	// Fingerprint is hex of sha256(admin.PasswordHash)[:8] as of login. Persisting it is
	// what makes "changing a password signs them out everywhere" survive a restart: on
	// the way back up the middleware compares this against the current admins.json, so a
	// password changed while the service was down still invalidates.
	Fingerprint string `json:"fingerprint"`

	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type sessionsDoc struct {
	Version  int                `json:"version"`
	Sessions []PersistedSession `json:"sessions"`
}

// SessionFile is a session.Storage backed by sessions.json.
type SessionFile struct {
	path string
}

// OpenSessionFile prepares the backing file. It is not read here; see Load.
func OpenSessionFile(dir string) *SessionFile {
	return &SessionFile{path: filepath.Join(dir, SessionsFile)}
}

// Path returns the backing file.
func (s *SessionFile) Path() string { return s.path }

// Load reads the stored sessions. A missing file is an empty list, which is what a fresh
// volume looks like.
func (s *SessionFile) Load() ([]PersistedSession, error) {
	var doc sessionsDoc
	found, err := readJSON(s.path, &doc)
	if err != nil {
		return nil, err
	}
	if found && doc.Version != jsonVersion {
		return nil, fmt.Errorf("%s: unsupported version %d, this build understands %d",
			s.path, doc.Version, jsonVersion)
	}
	return doc.Sessions, nil
}

// Save replaces the file.
//
// Whole-file rewrites are fine here because the write rate is low by construction: a
// login, a logout, a sweep, and a sliding refresh that is throttled to once an hour per
// session. A panel with five operators writes this a handful of times a day.
func (s *SessionFile) Save(list []PersistedSession) error {
	return writeJSONAtomic(s.path, sessionsDoc{Version: jsonVersion, Sessions: list})
}
