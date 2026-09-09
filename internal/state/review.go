// internal/state/review.go
//
// Review lifecycle for destructive operations. Invariants:
//   - proposals and approvals are append-only facts; only reviews.state
//     transitions, and only via CAS on 'pending';
//   - quorum = 2 distinct approvers, proposer excluded;
//   - execution validates approvals against CURRENT membership at
//     execution time (authz is checked by the caller before Execute);
//   - Execute takes the mutation function so the state change, the
//     review closure, and the audit row are one transaction.

package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Review states.
const (
	ReviewPending  = "pending"
	ReviewExecuted = "executed"
	ReviewRejected = "rejected"
)

// QuorumApprovals is the scaffold's fixed quorum.
const QuorumApprovals = 2

// CreateReview opens a proposal.
func CreateReview(ctx context.Context, db *sql.DB, id, orgID, op, proposer string, payload []byte, audit AuditEntry) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reviews (id, org_id, op, payload, state, proposer, created_at)
		VALUES (?, ?, ?, ?, 'pending', ?, strftime('%s','now'))`,
		id, orgID, op, payload, proposer); err != nil {
		return fmt.Errorf("state: insert review: %w", err)
	}
	if err := RecordAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

// ApproveReview records an approval if the review is pending and the
// approver is not the proposer. Idempotent-refused: duplicate approval
// by the same principal is ErrConflict, not silence.
func ApproveReview(ctx context.Context, db *sql.DB, reviewID, approver string, audit AuditEntry) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var proposer string
	var st string
	err = tx.QueryRowContext(ctx,
		`SELECT proposer, state FROM reviews WHERE id = ?`, reviewID).Scan(&proposer, &st)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: review lookup: %w", err)
	}
	if st != ReviewPending {
		return fmt.Errorf("%w: review is %s", ErrConflict, st)
	}
	if proposer == approver {
		return fmt.Errorf("%w: proposer cannot approve own proposal", ErrDenied)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO review_approvals (review_id, principal_id, created_at)
		VALUES (?, ?, strftime('%s','now'))`, reviewID, approver); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: already approved by this principal", ErrConflict)
		}
		return fmt.Errorf("state: insert approval: %w", err)
	}
	if err := RecordAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrNotReady: quorum not yet met.
var ErrNotReady = errors.New("state: review quorum not met")

// ExecuteReview closes a pending review that has met quorum and runs
// mutation (which receives the transaction). The whole sequence —
// approval revalidation, closure, mutation, audit — is one transaction.
// mutate returns (result, error); its result is echoed to the caller.
func ExecuteReview(ctx context.Context, db *sql.DB, reviewID, executor string,
	mutate func(tx *sql.Tx) (string, error)) (string, error) {

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("state: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var orgID, op, st string
	var payload []byte
	err = tx.QueryRowContext(ctx, `
		SELECT org_id, op, payload, state FROM reviews WHERE id = ?`, reviewID).
		Scan(&orgID, &op, &payload, &st)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: review lookup: %w", err)
	}
	if st != ReviewPending {
		return "", fmt.Errorf("%w: review is %s", ErrConflict, st)
	}

	// Revalidate: every approver must CURRENTLY hold a role that
	// permits destructive ops. Approval by someone demoted since is void;
	// void approvals can drop quorum below the bar, failing the execute.
	rows, err := tx.QueryContext(ctx, `
		SELECT a.principal_id, m.role
		FROM review_approvals a
		LEFT JOIN memberships m
		  ON m.principal_id = a.principal_id AND m.org_id = ?
		WHERE a.review_id = ?`, orgID, reviewID)
	if err != nil {
		return "", fmt.Errorf("state: approvals query: %w", err)
	}
	defer rows.Close()
	valid := 0
	for rows.Next() {
		var pid string
		var role *string
		if err := rows.Scan(&pid, &role); err != nil {
			return "", fmt.Errorf("state: scan approval: %w", err)
		}
		if role != nil && (*role == "owner" || *role == "admin") {
			valid++
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	rows.Close()
	if valid < QuorumApprovals {
		return "", fmt.Errorf("%w: %d valid approvals, need %d", ErrNotReady, valid, QuorumApprovals)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE reviews SET state = 'executed', closed_at = strftime('%s','now')
		WHERE id = ? AND state = 'pending'`, reviewID); err != nil {
		return "", fmt.Errorf("state: close review: %w", err)
	}
	result, err := mutate(tx)
	if err != nil {
		return "", err
	}
	if err := RecordAuditTx(ctx, tx, AuditEntry{
		ActorID: executor, OrgID: orgID, Op: op + ".execute",
		Payload: payload, Result: "ok", Detail: "review:" + reviewID,
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("state: commit review execute: %w", err)
	}
	return result, nil
}
