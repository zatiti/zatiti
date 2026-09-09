// internal/state/session.go
//
// MCP session registry. A session authenticates exactly one principal
// for a bounded wall-clock window. Expiry is checked on every lookup;
// there is no reaper — an expired session row is inert and is purged
// opportunistically at creation time.

package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SessionTTL bounds a session's life. Short by design: a compromised
// client regains nothing after expiry without a fresh signed challenge.
const SessionTTL = 15 * time.Minute

// CreateSession inserts a session, first purging expired rows.
func CreateSession(ctx context.Context, db *sql.DB, id, principalID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin session tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck — commit path below

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < strftime('%s','now')`); err != nil {
		return fmt.Errorf("state: purge expired sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (id, principal_id, created_at, expires_at)
		VALUES (?, ?, strftime('%s','now'), strftime('%s','now') + ?)`,
		id, principalID, int(SessionTTL.Seconds())); err != nil {
		return fmt.Errorf("state: insert session: %w", err)
	}
	return tx.Commit()
}

// SessionPrincipal returns the authenticated principal for a live
// session, or ErrNotFound for unknown/expired sessions (same result —
// callers learn nothing about which).
func SessionPrincipal(ctx context.Context, db *sql.DB, sessionID string) (string, error) {
	var principalID string
	err := db.QueryRowContext(ctx, `
		SELECT principal_id FROM sessions
		WHERE id = ? AND expires_at >= strftime('%s','now')`,
		sessionID).Scan(&principalID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: session lookup: %w", err)
	}
	return principalID, nil
}
