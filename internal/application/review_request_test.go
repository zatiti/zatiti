package application

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestReviewRequiredRefusalPersistsThePendingReview: the policy gate ensures
// the pending review inside the refused mutation's transaction, which rolls
// back; the application ensures it again in its own transaction so the
// eligible owner can decide it, and the approved retry then proceeds. No
// command is retained for the refusal, so the same key retries.
func TestReviewRequiredRefusalPersistsThePendingReview(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	e.policy.setRule(ctx, e.db, e.install, "business.apply", "review", []string{"needs a human"})
	input := map[string]any{"name": "reviewed", "expected_version": 0}

	_, err := e.invoke(t, e.actor(), "business.apply", "review-persist", input)
	f := requireFault(t, err, contract.CodeReviewRequired)
	digest := requirementDigest(t, f)
	if state := e.policy.reviewState(ctx, e.db, e.install, digest); state != "pending" {
		t.Fatalf("review %s after the refusal is %q, want pending (ensured outside the rolled-back transaction)", digest, state)
	}
	if cmd := e.commandGet(t, "business.apply", "review-persist"); cmd != nil {
		t.Fatalf("review_required was retained as %+v; the approved retry could not reuse the key", cmd)
	}
	if n := e.business.count(ctx, e.db, e.install); n != 0 {
		t.Fatalf("a refused apply left %d business items", n)
	}

	e.policy.decideReview(ctx, e.db, e.install, digest, "approve")
	res, err := e.invoke(t, e.actor(), "business.apply", "review-persist", input)
	if err != nil || res.Status != contract.StatusCompleted {
		t.Fatalf("approved retry: %v (status %q)", err, res.Status)
	}
	if n := e.business.count(ctx, e.db, e.install); n != 1 {
		t.Fatalf("approved apply left %d business items, want 1", n)
	}
}

// requirementDigest reads the single requirement digest out of a
// review_required fault's details.
func requirementDigest(t *testing.T, f *contract.Fault) string {
	t.Helper()
	var details struct {
		Requirements []fakeRequirement `json:"requirements"`
	}
	if err := json.Unmarshal(f.Details, &details); err != nil || len(details.Requirements) != 1 {
		t.Fatalf("review details %s: %v", f.Details, err)
	}
	return details.Requirements[0].ActionDigest
}

// count reports the business items on disk.
func (m *businessModule) count(ctx context.Context, db contract.Database, installation contract.ID) int {
	var n int
	_ = db.Read(ctx, contract.Actor{PrincipalID: "seeder", Kind: contract.KindService},
		contract.Scope{InstallationID: installation}, func(u contract.Unit) error {
			return u.QueryRowContext(ctx, "SELECT COUNT(*) FROM business_items").Scan(&n)
		})
	return n
}
