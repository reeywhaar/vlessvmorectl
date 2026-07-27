// Package store persists the few things this program owns: who may log in, who is
// currently logged in, and which node accounts belong to which person.
//
// Everything else the panel displays belongs to a vlessvmore node and is fetched from
// it on demand. There is no cache, no local mirror of the user lists, and no database —
// a panel that kept its own copy of somebody else's state would be a second source of
// truth to be wrong.
//
// subscribers.json does not bend that. A subscriber entry is a *reference* — a node id
// and a user id — plus labels an operator typed here. It never holds a copy of an
// account's name, uuid, quota or link, which is exactly why a subscriber record can be
// perfectly valid while the account it points at has been deleted, and why the renderer
// has to cope with that rather than trusting what it has.
//
// The files are plain, pretty-printed JSON. They are small, they change rarely, and an
// operator can read or repair them with a text editor, which matters when something has
// gone wrong at 3am.
//
// Each file has exactly one writer, so no two processes ever contend for one:
//
//	admins.json       written only by the CLI,    read by both
//	sessions.json     written only by the daemon, read by the daemon
//	subscribers.json  written only by the daemon, read by both
//
// `serve` never writes admins.json, so `docker exec … users add` and a running panel
// cannot race; see Admins.ReloadIfChanged for how the daemon notices the edit. The other
// two are the mirror image. sessions.json holds hashes rather than cookie values, which
// is what makes writing it acceptable; see the session package. subscribers.json holds
// share tokens in the clear because they must be re-readable, and enforces its
// single-writer rule structurally — see Open, OpenForDaemon and ErrReadOnly.
package store

import (
	"errors"
	"path/filepath"
)

// DefaultDir is the data directory inside the container. It should be a volume.
const DefaultDir = "/var/lib/vlessvmorectl"

// DirEnv overrides DefaultDir.
const DirEnv = "VLESSVMORECTL_DATA_DIR"

// AdminsFile holds the administrator list. See also SessionsFile and SubscribersFile.
const AdminsFile = "admins.json"

// The error classes handlers map onto status codes, matching the sibling's vocabulary.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
	ErrInvalid  = errors.New("invalid")
)

// Store aggregates the backing stores. The type exists so that adding one does not
// change every call site — which is exactly what happened when Subscribers arrived.
type Store struct {
	dir         string
	Admins      *Admins
	Subscribers *Subscribers
}

// Open prepares dir for a short-lived CLI process: subscribers.json is readable but not
// writable. A missing file of either kind is treated as an empty list, so a fresh volume
// needs no seeding.
//
// Every CLI call site gets this one. See OpenForDaemon for why the split exists.
func Open(dir string) (*Store, error) { return open(dir, false) }

// OpenForDaemon is Open with subscribers.json writable. `serve` is the only caller, and
// that is the whole invariant: one long-lived writer, whose in-memory copy is
// authoritative between requests. A second writer would have its edit silently rewritten
// by that copy on the next save; see Subscribers.saveLocked.
func OpenForDaemon(dir string) (*Store, error) { return open(dir, true) }

func open(dir string, writable bool) (*Store, error) {
	admins, err := OpenAdmins(filepath.Join(dir, AdminsFile))
	if err != nil {
		return nil, err
	}
	subscribers, err := OpenSubscribers(filepath.Join(dir, SubscribersFile), writable)
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, Admins: admins, Subscribers: subscribers}, nil
}

// Dir returns the data directory.
func (s *Store) Dir() string { return s.dir }
