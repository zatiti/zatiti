// internal/state/credential_bind.go
//
// Credential binding: pending marker → real public key. The WHERE
// clause pins the precondition (public_key still pending) so the
// bind is a compare-and-swap: exactly one issuance for a principal
// can ever commit.

package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// BindCredential swaps the pending marker for publicKey. The audit
// entry is written in the same transaction. Returns ErrConflict if the
// principal already holds a credential.
func BindCredential(ctx context.Context, db *sql.DB, principalID string, publicKey []byte, audit AuditEntry) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck — commit path below

	res, err := tx.ExecContext(ctx, `
		UPDATE principals SET public_key = ?
		WHERE id = ? AND kind = 'operator' AND public_key LIKE 'pending:%'`,
		publicKey, principalID)
	if err != nil {
		return fmt.Errorf("state: bind credential: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("state: bind credential: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("%w: principal %s is not pending or does not exist", ErrConflict, principalID)
	}
	if err := RecordAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

// PrincipalIDByRef resolves "-principal" input: accepts a full ID or,
// for convenience, the label suffix of a pending marker. Returns the
// canonical ID and whether the principal is credential-pending.
func PrincipalIDByRef(ctx context.Context, db *sql.DB, ref string) (id string, pending bool, err error) {
	// Exact ID match first.
	e := db.QueryRowContext(ctx,
		`SELECT id, public_key FROM principals WHERE id = ?`, ref)
	var pub []byte
	if err = e.Scan(&id, &pub); err == nil {
		return id, strings.HasPrefix(string(pub), "pending:"), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("state: principal lookup: %w", err)
	}
	// Label match among operators (label is stored in audit payloads;
	// the scaffold resolves operators by exact ID only to avoid
	// ambiguity — documented limitation).
	return "", false, fmt.Errorf("%w: no principal %q (exact ID required)", ErrNotFound, ref)
}
