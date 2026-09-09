// internal/state/principal_write.go
//
// Operator provisioning (state side). Scaffold limitation, made
// explicit: principals created here have no usable credential — the
// public_key column holds the literal "pending:<uuid>" marker, which
// is not a valid key encoding. Credential issuance (T3.x) replaces
// the marker. Authorization-relevant facts (membership, role) are
// real immediately; the marker exists so the row is honest about
// lacking a credential rather than carrying a fake one.

package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrBadRole: role not in the schema's CHECK constraint set.
var ErrBadRole = errors.New("state: invalid role")

// CreateOperator registers an operator principal with membership in
// orgID. Journals in the same transaction.
func CreateOperator(ctx context.Context, db *sql.DB, id, name, role, orgID, actorID string, audit AuditEntry) error {
	switch role {
	case "operator", "admin", "auditor":
	default:
		return fmt.Errorf("%w: %q", ErrBadRole, role)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck — commit path below

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO principals (id, kind, public_key, created_at)
		VALUES (?, 'operator', ?, strftime('%s','now'))`,
		id, []byte("pending:"+id)); err != nil {
		return fmt.Errorf("state: insert principal: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships (principal_id, org_id, role)
		VALUES (?, ?, ?)`, id, orgID, role); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: principal already member", ErrConflict)
		}
		return fmt.Errorf("state: insert membership: %w", err)
	}
	if err := RecordAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit create operator: %w", err)
	}
	return nil
}
