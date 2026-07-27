// Package store persists the one thing this program owns: who may log in.
//
// Everything else the panel displays belongs to a vlessvmore node and is fetched from
// it on demand. There is no cache, no local mirror of the user lists, and no database —
// a panel that kept its own copy of somebody else's state would be a second source of
// truth to be wrong.
//
// The file is plain, pretty-printed JSON. It is small, it changes rarely, and an
// operator can read or repair it with a text editor, which matters when something has
// gone wrong at 3am.
//
// admins.json is written *only* by the CLI and read by the daemon. `serve` never writes
// it, so `docker exec … users add` and a running panel cannot race for that file; see
// Admins.ReloadIfChanged for how the daemon notices the edit.
//
// sessions.json is the mirror image — written only by the daemon, never by the CLI — so
// the two never contend either. It holds hashes rather than cookie values, which is what
// makes writing it acceptable; see the session package.
package store

import (
	"errors"
	"path/filepath"
)

// DefaultDir is the data directory inside the container. It should be a volume.
const DefaultDir = "/var/lib/vlessvmorectl"

// DirEnv overrides DefaultDir.
const DirEnv = "VLESSVMORECTL_DATA_DIR"

// AdminsFile is the only file in the data directory.
const AdminsFile = "admins.json"

// The error classes handlers map onto status codes, matching the sibling's vocabulary.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
	ErrInvalid  = errors.New("invalid")
)

// Store aggregates the backing stores. There is one today; the type exists so adding
// a second does not change every call site.
type Store struct {
	dir    string
	Admins *Admins
}

// Open prepares dir. A missing admins.json is treated as an empty list, so a fresh
// volume needs no seeding.
func Open(dir string) (*Store, error) {
	admins, err := OpenAdmins(filepath.Join(dir, AdminsFile))
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, Admins: admins}, nil
}

// Dir returns the data directory.
func (s *Store) Dir() string { return s.dir }
