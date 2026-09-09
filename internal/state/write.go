// internal/state/write.go
//
// Typed mutation services. Invariants: every mutation carries its audit
// row in the same transaction; name conflicts map to ErrConflict; no
// mutation bypasses the transaction boundary.

package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrConflict: the request collides with existing durable state.
var ErrConflict = errors.New("state: conflict with existing state")

// ErrDenied: the state layer refused on an invariant the caller holds
// (for example, a proposer approving their own review).
var ErrDenied = errors.New("state: denied")

// CreateOrganization creates an organization and journals the fact.
// createdBy must be a known principal (FK-enforced).
func CreateOrganization(ctx context.Context, db *sql.DB, id, name, createdBy string, audit AuditEntry) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck — commit path below

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO organizations (id, name, created_by, created_at)
		VALUES (?, ?, ?, strftime('%s','now'))`, id, name, createdBy); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: organization name %q exists", ErrConflict, name)
		}
		return fmt.Errorf("state: insert organization: %w", err)
	}
	if err := RecordAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit create organization: %w", err)
	}
	return nil
}

// isUniqueViolation detects SQLite UNIQUE/FK constraint failures via
// the driver's error code (SQLITE_CONSTRAINT = 19).
func isUniqueViolation(err error) bool {
	var sqliteErr interface{ Code() int }
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == 19
	}
	return false
}
