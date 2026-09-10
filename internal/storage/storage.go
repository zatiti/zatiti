package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Default bounds from the shared contract. A config that sets BusyTimeout to
// zero gets the default; explicit negative values are rejected.
const (
	defaultBusyTimeout = 5 * time.Second
	defaultEventPage   = 100
	maxEventPage       = 500
)

// Compile-time checks that storage satisfies the shared contract.
var (
	_ contract.Database = (*database)(nil)
	_ contract.Unit     = (*unit)(nil)
)

// Config selects the database file and the bounded busy wait. BusyTimeout
// values below one millisecond are rounded up to one millisecond; zero
// selects the five second default.
type Config struct {
	Path        string
	BusyTimeout time.Duration
}

// database is the storage implementation of contract.Database. All access
// funnels through one *sql.DB pool; writes, migrations and backups serialize
// on wmu so the database has one ordered writer.
type database struct {
	db     *sql.DB
	path   string
	busy   time.Duration
	wmu    sync.Mutex
	closed atomic.Bool
}

// Open connects to the SQLite database at cfg.Path and prepares the storage
// namespace (generation row, migration metadata, event outbox). The caller
// must hold the platform installation lock; Open never locks by itself.
//
// Every connection is opened in WAL mode with foreign keys on, synchronous
// FULL and the configured busy timeout; Open fails if the driver cannot
// guarantee those settings. A path containing '?' cannot be expressed in the
// driver DSN form and is rejected.
func Open(ctx context.Context, cfg Config) (contract.Database, error) {
	if cfg.Path == "" {
		return nil, errors.New("storage: open requires a database path")
	}
	if cfg.BusyTimeout < 0 {
		return nil, fmt.Errorf("storage: busy timeout %s must not be negative", cfg.BusyTimeout)
	}
	busy := cfg.BusyTimeout
	if busy == 0 {
		busy = defaultBusyTimeout
	}
	dsn, err := buildDSN(cfg.Path, busy)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", cfg.Path, err)
	}
	d := &database{db: db, path: cfg.Path, busy: busy}
	if err := d.bootstrap(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return d, nil
}

// buildDSN appends the driver connection parameters. Each connection gets
// WAL, foreign keys, synchronous FULL and the busy timeout; the shorthand
// keys are validated by the driver before any statement runs.
func buildDSN(path string, busy time.Duration) (string, error) {
	if containsRune(path, '?') {
		return "", fmt.Errorf("storage: database path %q contains '?' and cannot be used in a driver DSN", path)
	}
	ms := (busy + time.Millisecond - 1) / time.Millisecond
	if ms < 1 {
		ms = 1
	}
	q := url.Values{}
	q.Set("_busy_timeout", strconv.FormatInt(int64(ms), 10))
	q.Set("_foreign_keys", "on")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", "FULL")
	return path + "?" + q.Encode(), nil
}

func containsRune(s string, r byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == r {
			return true
		}
	}
	return false
}

// bootstrapDDL creates the storage-owned tables. Storage owns installation
// generation, migration metadata and the event outbox; no domain schema
// lives in this package.
const bootstrapDDL = `
CREATE TABLE IF NOT EXISTS storage_migrations (
	owner      TEXT NOT NULL,
	version    INTEGER NOT NULL CHECK (version >= 1),
	sha256     TEXT NOT NULL,
	applied_at TEXT NOT NULL,
	PRIMARY KEY (owner, version)
);
CREATE TABLE IF NOT EXISTS storage_generation (
	id         INTEGER PRIMARY KEY CHECK (id = 1),
	generation INTEGER NOT NULL CHECK (generation >= 1)
);
CREATE TABLE IF NOT EXISTS storage_events (
	id               TEXT PRIMARY KEY,
	sequence         INTEGER NOT NULL UNIQUE CHECK (sequence >= 1),
	at               TEXT NOT NULL,
	installation_id  TEXT NOT NULL,
	organization_id  TEXT NOT NULL DEFAULT '',
	project_id       TEXT NOT NULL DEFAULT '',
	worker_id        TEXT NOT NULL DEFAULT '',
	task_id          TEXT NOT NULL DEFAULT '',
	kind             TEXT NOT NULL,
	resource_id      TEXT NOT NULL,
	resource_version INTEGER NOT NULL CHECK (resource_version >= 1),
	data             BLOB
);
`

// bootstrap verifies the connection settings the contract requires and
// creates the storage-owned tables. It runs once from Open before the
// database is handed out.
func (d *database) bootstrap(ctx context.Context) error {
	if err := d.db.PingContext(ctx); err != nil {
		return fmt.Errorf("storage: connect %s: %w", d.path, err)
	}
	checks := []struct {
		pragma string
		want   string
	}{
		{"PRAGMA busy_timeout", strconv.FormatInt(int64((d.busy+time.Millisecond-1)/time.Millisecond), 10)},
		{"PRAGMA journal_mode", "wal"},
		{"PRAGMA foreign_keys", "1"},
		{"PRAGMA synchronous", "2"},
	}
	for _, c := range checks {
		var got any
		if err := d.db.QueryRowContext(ctx, c.pragma).Scan(&got); err != nil {
			return fmt.Errorf("storage: verify %s on %s: %w", c.pragma, d.path, err)
		}
		if fmt.Sprint(got) != c.want {
			return fmt.Errorf("storage: %s is %v on %s, want %s", c.pragma, got, d.path, c.want)
		}
	}
	if _, err := d.db.ExecContext(ctx, bootstrapDDL); err != nil {
		return fmt.Errorf("storage: bootstrap schema on %s: %w", d.path, err)
	}
	return nil
}

// Close closes the database. Close is idempotent. The controller must stop
// admission before closing; in-flight operations may observe errors during
// shutdown.
func (d *database) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	return d.db.Close()
}

// StartGeneration advances the persisted controller generation by one and
// returns the new value. Assembly calls it exactly once, after migrations
// and before serving, while holding the installation ownership. Workers,
// dispatch claims and leases bind the returned generation.
func (d *database) StartGeneration(ctx context.Context) (int64, error) {
	if d.closed.Load() {
		return 0, closedFault()
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()

	conn, err := d.db.Conn(ctx)
	if err != nil {
		return 0, storageFault("acquire connection", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, txFault("begin generation transaction", err)
	}
	var current int64
	err = conn.QueryRowContext(ctx, "SELECT generation FROM storage_generation WHERE id = 1").Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		current = 0
	} else if err != nil {
		_ = d.finishTxn(conn, false)
		return 0, storageFault("read generation", err)
	}
	next := current + 1
	if _, err := conn.ExecContext(ctx, `INSERT INTO storage_generation (id, generation) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET generation = excluded.generation`, next); err != nil {
		_ = d.finishTxn(conn, false)
		return 0, storageFault("advance generation", err)
	}
	if err := d.finishTxn(conn, true); err != nil {
		_ = d.finishTxn(conn, false)
		return 0, txFault("commit generation advance", err)
	}
	return next, nil
}

// Generation returns the persisted controller generation, or zero when
// StartGeneration has not run yet on this installation.
func (d *database) Generation(ctx context.Context) (int64, error) {
	if d.closed.Load() {
		return 0, closedFault()
	}
	var gen int64
	err := d.db.QueryRowContext(ctx, "SELECT generation FROM storage_generation WHERE id = 1").Scan(&gen)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("storage: read generation: %w", err)
	}
	return gen, nil
}

// loadGeneration reads the generation inside the caller's transaction or
// snapshot. A missing row means generation zero: StartGeneration has not
// advanced it yet.
func loadGeneration(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var gen int64
	err := q.QueryRowContext(ctx, "SELECT generation FROM storage_generation WHERE id = 1").Scan(&gen)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return gen, nil
}
