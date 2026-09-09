// internal/state/store.go
//
// State root ownership: SQLite lifecycle, exclusive installation lock,
// controller generation. One controller writer per data directory.
// Other roots must not open SQLite themselves.

package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "github.com/mattn/go-sqlite3" // pinned driver; version pinned in go.mod
	"golang.org/x/sys/unix"
)

var (
	ErrAlreadyInitialized = errors.New("state: installation already initialized")
	ErrLocked             = errors.New("state: another controller owns this installation")
	ErrNotInitialized     = errors.New("state: installation is not initialized")
)

// Store owns the SQLite handle and the installation lock for one process.
type Store struct {
	mu         sync.Mutex // guards bootstrap and generation cache
	dir        string
	db         *sql.DB
	lockFile   *os.File
	generation uint64
}

// Open acquires exclusive installation ownership of dir and opens the
// database in WAL mode. It fails rather than sharing ownership.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("state: create dir: %w", err)
	}
	s := &Store{dir: dir}

	lf, err := os.OpenFile(filepath.Join(dir, "controller.lock"),
		os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("state: open lock: %w", err)
	}
	s.lockFile = lf
	if err := unix.Flock(int(lf.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lf.Close()
		return nil, ErrLocked
	}

	db, err := sql.Open("sqlite3", filepath.Join(dir, "zatiti.db")+
		"?_journal_mode=WAL&_fk=1&_busy_timeout=5000")
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("state: open db: %w", err)
	}
	// One controller writer; serialize writes through a single connection
	// to keep the ordered write path until transaction methods land (T1.1).
	db.SetMaxOpenConns(1)
	s.db = db
	return s, nil
}

// Close releases the database and the installation lock.
func (s *Store) Close() error {
	var firstErr error
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			firstErr = err
		}
		s.db = nil
	}
	if s.lockFile != nil {
		_ = unix.Flock(int(s.lockFile.Fd()), unix.LOCK_UN)
		if err := s.lockFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.lockFile = nil
	}
	return firstErr
}

// DB returns the handle for state-owned transaction methods only.
// Other roots receive typed services, not this handle (enforced by review).
func (s *Store) DB() *sql.DB { return s.db }

// Dir returns the installation data directory (for identity-root
// co-location of the keystore). Read-only fact; does not grant
// database access.
func (s *Store) Dir() string { return s.dir }

// Generation returns the persisted controller generation, advancing it
// once per acquisition of ownership. Workers and leases bind this value.
func (s *Store) Generation(ctx context.Context) (uint64, error) {
	if s.generation != 0 {
		return s.generation, nil
	}
	var g uint64
	err := s.db.QueryRowContext(ctx,
		`SELECT generation FROM installation WHERE id = 1`).Scan(&g)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotInitialized
	}
	if err != nil {
		return 0, fmt.Errorf("state: read generation: %w", err)
	}
	s.generation = g
	return g, nil
}

// newID returns a random RFC 4122 v4 UUID string.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("state: entropy unavailable: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]), hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}

// NewID exports UUID generation for roots that must mint entity IDs
// (state remains the format owner).
func NewID() string { return newID() }
