// internal/state/lookup.go
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// OwnerPrincipalID returns the owner principal's ID.
func OwnerPrincipalID(ctx context.Context, db *sql.DB) (string, error) {
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM principals WHERE kind = 'owner' ORDER BY created_at ASC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotInitialized
	}
	if err != nil {
		return "", fmt.Errorf("state: owner lookup: %w", err)
	}
	return id, nil
}

// RootOrgID returns the bootstrap organization's ID.
func RootOrgID(ctx context.Context, db *sql.DB) (string, error) {
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM organizations ORDER BY created_at ASC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotInitialized
	}
	if err != nil {
		return "", fmt.Errorf("state: root org lookup: %w", err)
	}
	return id, nil
}

// ErrNotFound: a named lookup missed.
var ErrNotFound = errors.New("state: not found")

// OrgIDByName resolves an organization name to its ID.
func OrgIDByName(ctx context.Context, db *sql.DB, name string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM organizations WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: org lookup: %w", err)
	}
	return id, nil
}
