// internal/state/query.go
//
// Typed read services over the state schema. Other roots call these,
// never formulate SQL. One function per question; results are plain
// structs owned by state.

package state

import (
	"context"
	"database/sql"
	"fmt"
)

// Organization is a public view of an organization row.
type Organization struct {
	ID        string
	Name      string
	CreatedBy string
	CreatedAt int64
}

// ListOrganizations returns all organizations, oldest first.
func ListOrganizations(ctx context.Context, db *sql.DB) ([]Organization, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, created_by, created_at
		FROM organizations ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("state: list organizations: %w", err)
	}
	defer rows.Close()

	var out []Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.CreatedBy, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("state: scan organization: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
