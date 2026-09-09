// internal/state/audit_query.go
//
// Journal read services. Reads never bypass the append-only contract;
// there is deliberately no "update audit" or "delete audit" anywhere
// in this package, and `go vet`-style review enforces it.

package state

import (
	"context"
	"database/sql"
	"fmt"
)

// AuditRow is one readable journal fact.
type AuditRow struct {
	Seq     int64
	TS      int64
	ActorID string
	OrgID   string // empty when installation-scoped
	Op      string
	Payload []byte
	Result  string
	Detail  string
}

// ListAudit returns up to limit entries for orgID, newest first.
// orgID empty means installation-scoped entries (audit rows with NULL
// org_id); pass only after role checks — the caller owns authorization.
func ListAudit(ctx context.Context, db *sql.DB, orgID string, limit uint64) ([]AuditRow, error) {
	if limit == 0 || limit > 1000 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `
		SELECT seq, ts, actor_id, COALESCE(org_id,''), op, payload, result, COALESCE(detail,'')
		FROM audit WHERE org_id = ? ORDER BY seq DESC LIMIT ?`, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list audit: %w", err)
	}
	defer rows.Close()

	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.Seq, &r.TS, &r.ActorID, &r.OrgID,
			&r.Op, &r.Payload, &r.Result, &r.Detail); err != nil {
			return nil, fmt.Errorf("state: scan audit: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
