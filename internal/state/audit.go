// internal/state/audit.go
//
// Audit journal: append-only. No method in this package may UPDATE or
// DELETE from audit. RecordAudit is exported for mutation transactions
// to include inside their own tx; the standalone form is for
// denied-request journaling, which has no mutation to bundle with.

package state

import (
	"context"
	"database/sql"
	"fmt"
)

// AuditEntry is one journal fact.
type AuditEntry struct {
	ActorID string
	OrgID   string // may be empty for installation-scoped ops
	Op      string
	Payload []byte // canonical JSON of the request
	Result  string // "ok" | "denied" | "error"
	Detail  string
}

// RecordAuditTx writes an audit row inside the caller's transaction.
// Mutation methods MUST journal in the same transaction as the change.
func RecordAuditTx(ctx context.Context, tx *sql.Tx, e AuditEntry) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO audit (ts, actor_id, org_id, op, payload, result, detail)
		VALUES (strftime('%s','now'), ?, ?, ?, ?, ?, ?)`,
		e.ActorID, nullableText(e.OrgID), e.Op, e.Payload, e.Result, nullableText(e.Detail))
	if err != nil {
		return fmt.Errorf("state: audit write: %w", err)
	}
	return nil
}

// RecordAudit is the standalone form for journaling denials and errors
// that produced no mutation.
func RecordAudit(ctx context.Context, db *sql.DB, e AuditEntry) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO audit (ts, actor_id, org_id, op, payload, result, detail)
		VALUES (strftime('%s','now'), ?, ?, ?, ?, ?, ?)`,
		e.ActorID, nullableText(e.OrgID), e.Op, e.Payload, e.Result, nullableText(e.Detail))
	if err != nil {
		return fmt.Errorf("state: audit write: %w", err)
	}
	return nil
}

func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
