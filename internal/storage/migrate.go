package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// ownerPattern is the shape of a table namespace owner: a lowercase
// identifier usable as `owner_` table prefix. The storage and sqlite
// prefixes are reserved.
var ownerPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// reservedOwnerPrefixes lists owner-name prefixes storage and SQLite own.
var reservedOwnerPrefixes = []string{"storage", "sqlite"}

// Migrate applies owner migrations in the order given. Every migration runs
// in its own short transaction together with its metadata row, so a crash
// leaves either a fully applied migration or none. Migrate must run under
// exclusive installation ownership before serving; storage does not acquire
// that lock itself.
//
// Validation, all of it before the first statement runs: each body must
// match its SHA-256 pin, versions must be monotonically increasing per
// owner, an already-applied migration must arrive with an identical body,
// and every table, index, view or trigger the bodies touch must live inside
// the owner's namespace.
func (d *database) Migrate(ctx context.Context, migrations []contract.Migration) error {
	if d.closed.Load() {
		return closedFault()
	}
	if err := validateMigrations(migrations); err != nil {
		return err
	}
	if len(migrations) == 0 {
		return nil
	}

	d.wmu.Lock()
	defer d.wmu.Unlock()

	conn, err := d.db.Conn(ctx)
	if err != nil {
		return storageFault("acquire connection", err)
	}
	defer func() { _ = conn.Close() }()

	for _, m := range migrations {
		if err := d.applyMigration(ctx, conn, m); err != nil {
			return fmt.Errorf("storage: migration %s/%d: %w", m.Owner, m.Version, err)
		}
	}
	return nil
}

// applyMigration applies one migration atomically with its metadata row.
// Re-running an applied migration with an identical body is a no-op.
func (d *database) applyMigration(ctx context.Context, conn *sql.Conn, m contract.Migration) error {
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return txFault("begin migration transaction", err)
	}

	var applied contract.Digest
	err := conn.QueryRowContext(ctx,
		"SELECT sha256 FROM storage_migrations WHERE owner = ? AND version = ?",
		m.Owner, m.Version).Scan(&applied)
	switch {
	case err == nil:
		if applied != m.SHA256 {
			_ = d.finishTxn(conn, false)
			return fmt.Errorf("applied migration changed: recorded sha256 %s, migration pins %s", applied, m.SHA256)
		}
		// Identical migration already applied: nothing to do.
		_ = d.finishTxn(conn, false)
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		_ = d.finishTxn(conn, false)
		return storageFault("read migration record", err)
	}

	var maxVersion int64
	if err := conn.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version), 0) FROM storage_migrations WHERE owner = ?", m.Owner).Scan(&maxVersion); err != nil {
		_ = d.finishTxn(conn, false)
		return storageFault("read migration versions", err)
	}
	if m.Version <= maxVersion {
		_ = d.finishTxn(conn, false)
		return fmt.Errorf("version %d is not above the highest applied version %d", m.Version, maxVersion)
	}

	if _, err := conn.ExecContext(ctx, m.SQL); err != nil {
		_ = d.finishTxn(conn, false)
		// The migration body failed; SQLite rolls its DDL back with the
		// transaction, so neither schema nor metadata survives.
		return txFault("apply migration SQL", err)
	}
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO storage_migrations (owner, version, sha256, applied_at) VALUES (?, ?, ?, ?)",
		m.Owner, m.Version, string(m.SHA256), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		_ = d.finishTxn(conn, false)
		return storageFault("record migration", err)
	}
	if err := d.finishTxn(conn, true); err != nil {
		_ = d.finishTxn(conn, false)
		return txFault("commit migration", err)
	}
	return nil
}

// validateMigrations checks a migration batch before any of it runs. The
// storage owner namespace is reserved; external owners never use it.
func validateMigrations(migrations []contract.Migration) error {
	seen := make(map[contract.Migration]bool, len(migrations))
	for i, m := range migrations {
		if m.Owner == "" {
			return invalidInputFault("migration %d has an empty owner", i)
		}
		if !ownerPattern.MatchString(m.Owner) {
			return invalidInputFault("migration owner %q must match %s", m.Owner, ownerPattern)
		}
		for _, prefix := range reservedOwnerPrefixes {
			if strings.HasPrefix(m.Owner, prefix) {
				return invalidInputFault("migration owner %q uses the reserved %q prefix", m.Owner, prefix)
			}
		}
		if m.Version < 1 {
			return invalidInputFault("migration %s/%d version must be at least 1", m.Owner, m.Version)
		}
		if strings.TrimSpace(m.SQL) == "" {
			return invalidInputFault("migration %s/%d has an empty body", m.Owner, m.Version)
		}
		if got := contract.Hash([]byte(m.SQL)); got != m.SHA256 {
			return invalidInputFault("migration %s/%d sha256 %s does not match its body (%s)", m.Owner, m.Version, m.SHA256, got)
		}
		if seen[m] {
			return invalidInputFault("duplicate migration %s/%d in batch", m.Owner, m.Version)
		}
		seen[m] = true
		if err := validateMigrationNamespace(m.Owner, m.SQL); err != nil {
			return invalidInputFault("migration %s/%d: %s", m.Owner, m.Version, err)
		}
	}
	return nil
}
