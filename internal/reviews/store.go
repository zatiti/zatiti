package reviews

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// SQL access for the reviews_-prefixed tables. Every query is installation
// scoped: cross-installation identities never resolve here.

// insertReview stores one new pending review request together with its
// eligible-reviewer snapshot.
func insertReview(ctx context.Context, unit contract.Unit, r reviewRow, eligible []contract.ID) error {
	scopeJSON, err := encodeScope(r.Scope)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO reviews_requests
			(id, version, installation_id, organization_id, project_id, worker_id, task_id,
			 scope_json, action_digest, preview_json, requirement_json, proposer_id, state,
			 decision_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
		string(r.ID), r.Version, string(r.Scope.InstallationID),
		string(r.Scope.OrganizationID), string(r.Scope.ProjectID),
		string(r.Scope.WorkerID), string(r.Scope.TaskID),
		scopeJSON, r.ActionDigest, r.PreviewJSON, r.RequirementJSON,
		string(r.ProposerID), r.State, formatStamp(r.CreatedAt), formatStamp(r.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("reviews: insert review: %w", err)
	}
	now := formatStamp(r.CreatedAt)
	for _, p := range eligible {
		if _, err := unit.ExecContext(ctx, `
			INSERT INTO reviews_reviewers (review_id, principal_id, created_at)
			VALUES (?, ?, ?)`, string(r.ID), string(p), now); err != nil {
			return fmt.Errorf("reviews: insert eligible reviewer: %w", err)
		}
	}
	return nil
}

// fetchReviewByID loads one review inside the installation. A foreign
// installation's identifier does not resolve.
func fetchReviewByID(ctx context.Context, unit contract.Unit, install, id contract.ID) (*reviewRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, scope_json, action_digest, preview_json, requirement_json,
		       proposer_id, state, decision_id, created_at, updated_at
		FROM reviews_requests
		WHERE installation_id = ? AND id = ?`, string(install), string(id))
	r, err := scanReview(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reviews: fetch review: %w", err)
	}
	return &r, nil
}

// fetchLatestReviewByDigest loads the most recent review recorded for one
// exact action digest in the installation. History is ordered by
// (created_at, id); ties on created_at break on the identifier.
func fetchLatestReviewByDigest(ctx context.Context, unit contract.Unit, install contract.ID, digest string) (*reviewRow, error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT id, version, scope_json, action_digest, preview_json, requirement_json,
		       proposer_id, state, decision_id, created_at, updated_at
		FROM reviews_requests
		WHERE installation_id = ? AND action_digest = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, string(install), digest)
	if err != nil {
		return nil, fmt.Errorf("reviews: fetch review by digest: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("reviews: iterate reviews by digest: %w", err)
		}
		return nil, nil
	}
	r, err := scanReview(rows.Scan)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// updateReview transitions one review to a new version. The optimistic
// version predicate serializes concurrent mutations.
func updateReview(ctx context.Context, unit contract.Unit, r reviewRow, newVersion int64, state string, decisionID *contract.ID, now time.Time) error {
	var decision any
	if decisionID != nil {
		decision = string(*decisionID)
	}
	res, err := unit.ExecContext(ctx, `
		UPDATE reviews_requests
		SET version = ?, state = ?, decision_id = ?, updated_at = ?
		WHERE id = ? AND version = ? AND installation_id = ?`,
		newVersion, state, decision, formatStamp(now),
		string(r.ID), r.Version, string(r.Scope.InstallationID))
	if err != nil {
		return fmt.Errorf("reviews: update review: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reviews: review update result: %w", err)
	}
	if n != 1 {
		return staleVersion("review %s changed concurrently; retry with the current version", r.ID)
	}
	return nil
}

// insertDecision records one immutable decision row.
func insertDecision(ctx context.Context, unit contract.Unit, d decisionRow) error {
	_, err := unit.ExecContext(ctx, `
		INSERT INTO reviews_decisions
			(id, review_id, review_version, installation_id, action_digest, reviewer_id,
			 decision, decided_at, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(d.ID), string(d.ReviewID), d.ReviewVersion, string(d.Install),
		d.ActionDigest, string(d.ReviewerID), d.Decision,
		formatStamp(d.DecidedAt), d.Reason, formatStamp(d.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("reviews: insert decision: %w", err)
	}
	return nil
}

// fetchDecisionByID loads one decision row, or nil when absent.
func fetchDecisionByID(ctx context.Context, unit contract.Unit, install contract.ID, id contract.ID) (*decisionRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, review_id, review_version, installation_id, action_digest, reviewer_id,
		       decision, decided_at, reason, created_at
		FROM reviews_decisions
		WHERE installation_id = ? AND id = ?`, string(install), string(id))
	d, err := scanDecision(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reviews: fetch decision: %w", err)
	}
	return &d, nil
}

// insertDelegation records one delegation row.
func insertDelegation(ctx context.Context, unit contract.Unit, d delegationRow) error {
	_, err := unit.ExecContext(ctx, `
		INSERT INTO reviews_delegations
			(id, review_id, review_version, installation_id, delegator_id, delegatee_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(d.ID), string(d.ReviewID), d.ReviewVersion, string(d.Install),
		string(d.DelegatorID), string(d.DelegateeID), formatStamp(d.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("reviews: insert delegation: %w", err)
	}
	return nil
}

// fetchDelegation returns the delegation from one principal to another for
// one review, or nil when absent.
func fetchDelegation(ctx context.Context, unit contract.Unit, install contract.ID, reviewID, delegator, delegatee contract.ID) (*delegationRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, review_id, review_version, installation_id, delegator_id, delegatee_id, created_at
		FROM reviews_delegations
		WHERE installation_id = ? AND review_id = ? AND delegator_id = ? AND delegatee_id = ?`,
		string(install), string(reviewID), string(delegator), string(delegatee))
	d, err := scanDelegation(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reviews: fetch delegation: %w", err)
	}
	return &d, nil
}

// fetchDelegationTo returns one delegation to the given delegatee for a
// review, or nil when the delegatee holds no delegated authority.
func fetchDelegationTo(ctx context.Context, unit contract.Unit, install contract.ID, reviewID, delegatee contract.ID) (*delegationRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, review_id, review_version, installation_id, delegator_id, delegatee_id, created_at
		FROM reviews_delegations
		WHERE installation_id = ? AND review_id = ? AND delegatee_id = ?
		ORDER BY created_at, id
		LIMIT 1`, string(install), string(reviewID), string(delegatee))
	d, err := scanDelegation(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reviews: fetch delegation to principal: %w", err)
	}
	return &d, nil
}
