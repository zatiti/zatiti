// internal/state/session_purge.go
package state

import (
	"context"
	"database/sql"
	"fmt"
)

// PurgeExpiredSessions deletes expired session rows; returns the count.
// Safe under lease: idempotent delete on an expiry predicate.
func PurgeExpiredSessions(ctx context.Context, db *sql.DB) (int64, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < strftime('%s','now')`)
	if err != nil {
		return 0, fmt.Errorf("state: purge sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("state: purge sessions: %w", err)
	}
	return n, nil
}
