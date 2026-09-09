// internal/state/migrations.go
//
// T1.1: ordered, recorded schema migrations. Contract:
//   - migrations are strictly ordered by Seq; gaps are rejected;
//   - each migration runs inside one transaction, together with its
//     schema_migrations bookkeeping row, so a crash never leaves a
//     half-applied step;
//   - an installation at a higher schema version than this binary
//     supports is refused (downgrade is not a supported operation).

package state

import (
	"context"
	"database/sql"
	"fmt"
)

// Migration is one ordered schema step.
type Migration struct {
	Seq  int
	Name string
	Stmt []string // executed in order within the migration transaction
}

// migrations is the registry. Append-only: existing entries are frozen
// history and must never be edited; new schema changes get new Seqs.
var migrations = []Migration{
	{
		Seq:  1,
		Name: "base: installation, principals, organizations, memberships",
		Stmt: []string{
			`CREATE TABLE IF NOT EXISTS installation (
				id             INTEGER PRIMARY KEY CHECK (id = 1),
				generation     INTEGER NOT NULL CHECK (generation >= 1),
				schema_version INTEGER NOT NULL,
				created_at     INTEGER NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS principals (
				id         TEXT PRIMARY KEY,
				kind       TEXT NOT NULL CHECK (kind IN ('owner','operator','worker','service')),
				public_key BLOB NOT NULL,
				created_at INTEGER NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS organizations (
				id         TEXT PRIMARY KEY,
				name       TEXT NOT NULL UNIQUE,
				created_by TEXT NOT NULL REFERENCES principals(id),
				created_at INTEGER NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS memberships (
				principal_id TEXT NOT NULL REFERENCES principals(id),
				org_id       TEXT NOT NULL REFERENCES organizations(id),
				role         TEXT NOT NULL CHECK (role IN ('owner','admin','operator','auditor')),
				PRIMARY KEY (principal_id, org_id)
			)`,
			`CREATE TABLE IF NOT EXISTS schema_migrations (
				seq        INTEGER PRIMARY KEY,
				name       TEXT NOT NULL,
				applied_at INTEGER NOT NULL
			)`,
		},
	},
	{
		Seq:  2,
		Name: "audit: append-only audit journal",
		Stmt: []string{
			`CREATE TABLE IF NOT EXISTS audit (
				seq         INTEGER PRIMARY KEY AUTOINCREMENT,
				ts          INTEGER NOT NULL,
				actor_id    TEXT NOT NULL,
				org_id      TEXT,
				op          TEXT NOT NULL,
				payload     BLOB NOT NULL,      -- canonical JSON of the request
				result      TEXT NOT NULL CHECK (result IN ('ok','denied','error')),
				detail      TEXT
			)`,
			// The audit table is append-only by construction: no UPDATE
			// or DELETE is ever issued against it by any state method.
			`CREATE INDEX IF NOT EXISTS audit_actor_ts ON audit(actor_id, ts)`,
			`CREATE INDEX IF NOT EXISTS audit_org_ts ON audit(org_id, ts)`,
		},
	},
	{
		Seq:  3,
		Name: "sessions: mcp session registry",
		Stmt: []string{
			`CREATE TABLE IF NOT EXISTS sessions (
				id           TEXT PRIMARY KEY,
				principal_id TEXT NOT NULL REFERENCES principals(id),
				created_at   INTEGER NOT NULL,
				expires_at   INTEGER NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expires_at)`,
		},
	},
	{
		Seq:  4,
		Name: "reviews: destructive operation proposals and approvals",
		Stmt: []string{
			`CREATE TABLE IF NOT EXISTS reviews (
				id           TEXT PRIMARY KEY,
				org_id       TEXT NOT NULL REFERENCES organizations(id),
				op           TEXT NOT NULL,
				payload      BLOB NOT NULL,      -- canonical JSON of proposed inputs
				state        TEXT NOT NULL CHECK (state IN ('pending','executed','rejected','expired')),
				proposer     TEXT NOT NULL REFERENCES principals(id),
				created_at   INTEGER NOT NULL,
				closed_at    INTEGER             -- set when state leaves pending
			)`,
			`CREATE TABLE IF NOT EXISTS review_approvals (
				review_id  TEXT NOT NULL REFERENCES reviews(id),
				principal_id TEXT NOT NULL REFERENCES principals(id),
				created_at INTEGER NOT NULL,
				PRIMARY KEY (review_id, principal_id)
			)`,
			`CREATE INDEX IF NOT EXISTS reviews_org_state ON reviews(org_id, state)`,
		},
	},
	{
		Seq:  5,
		Name: "leases: generation-bound work leases",
		Stmt: []string{
			`CREATE TABLE IF NOT EXISTS leases (
				resource    TEXT PRIMARY KEY,   -- the work being leased
				worker_id   TEXT NOT NULL,
				generation  INTEGER NOT NULL,   -- controller gen the lease lives under
				expires_at  INTEGER NOT NULL,   -- heartbeat deadline, unix secs
				acquired_at INTEGER NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS leases_expiry ON leases(expires_at)`,
		},
	},
}

// ErrTooNew: the data directory was written by a newer binary.
var ErrTooNew = fmt.Errorf("state: installation schema is newer than this binary")

// Migrate applies all pending migrations in order.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			seq        INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("state: create migration ledger: %w", err)
	}

	applied := map[int]string{}
	rows, err := db.QueryContext(ctx, `SELECT seq, name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("state: read migration ledger: %w", err)
	}
	for rows.Next() {
		var seq int
		var name string
		if err := rows.Scan(&seq, &name); err != nil {
			rows.Close()
			return fmt.Errorf("state: scan ledger: %w", err)
		}
		applied[seq] = name
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("state: read ledger: %w", err)
	}

	for i, m := range migrations {
		if m.Seq != i+1 {
			return fmt.Errorf("state: migration registry gap at index %d (seq %d)", i, m.Seq)
		}
		if prev, ok := applied[m.Seq]; ok {
			if prev != m.Name {
				return fmt.Errorf("state: migration %d history mismatch: recorded %q, registry %q",
					m.Seq, prev, m.Name)
			}
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return err
		}
	}

	// Refuse downgrades: check highest recorded seq against the registry.
	var maxSeq int
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0) FROM schema_migrations`).Scan(&maxSeq); err != nil {
		return fmt.Errorf("state: max seq: %w", err)
	}
	if len(migrations) > 0 && maxSeq > migrations[len(migrations)-1].Seq {
		return ErrTooNew
	}
	return nil
}

// applyMigration runs one migration and its ledger row in one transaction.
func applyMigration(ctx context.Context, db *sql.DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin migration %d: %w", m.Seq, err)
	}
	defer tx.Rollback() //nolint:errcheck — commit path below

	for i, q := range m.Stmt {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("state: migration %d (%s) stmt %d: %w", m.Seq, m.Name, i, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (seq, name, applied_at)
		 VALUES (?, ?, strftime('%s','now'))`, m.Seq, m.Name); err != nil {
		return fmt.Errorf("state: record migration %d: %w", m.Seq, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit migration %d: %w", m.Seq, err)
	}
	return nil
}
