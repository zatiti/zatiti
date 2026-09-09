// internal/state/purge.go
//
// The one destructive mutation in the scaffold. Deletes the
// organization and its memberships. Principals are NOT deleted
// (they may hold memberships elsewhere); their audit rows persist —
// audit is append-only and outlives the org it describes.

package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PurgeOrganization deletes orgID by name-resolution done by the caller.
// Refuses the LAST organization (an installation must retain its root).
func PurgeOrganization(ctx context.Context, tx *sql.Tx, orgID string) (string, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&n); err != nil {
		return "", fmt.Errorf("state: org count: %w", err)
	}
	if n <= 1 {
		return "", fmt.Errorf("refusing to purge the last organization")
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM organizations WHERE id = ?`, orgID)
	if err != nil {
		return "", fmt.Errorf("state: purge org: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", ErrNotFound
	}
	// Memberships go via ON DELETE CASCADE if declared; the scaffold's
	// schema does not declare cascades, so delete explicitly.
	if _, err := tx.ExecContext(ctx, `DELETE FROM memberships WHERE org_id = ?`, orgID); err != nil {
		return "", fmt.Errorf("state: purge memberships: %w", err)
	}
	return fmt.Sprintf("purged organization %s", orgID), nil
}

// OrgIDByNameTx resolves a name within the execute transaction, so the
// purge acts on the same snapshot it validated against.
func OrgIDByNameTx(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: no organization %q", ErrNotFound, name)
	}
	return id, err
}
