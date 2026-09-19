package configuration

import (
	"errors"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// requireReview puts the fake policy owner into the default review class
// for configuration.apply: every check answers review with one
// human-required requirement bound to the candidate digest it was given,
// and reviews answer only from explicit decisions.
func requireReview(env *testEnv) {
	env.ports.mu.Lock()
	defer env.ports.mu.Unlock()
	env.ports.decision = "review"
	env.ports.decReqs = []wireDecisionRequirement{{HumanRequired: true, EligiblePrincipals: []contract.ID{env.owner}}}
	env.ports.approveOK = false
}

// TestPlanEnsuresReviewAndOwnerDecisionUnblocksApply walks the owner's
// flow: plan seals the candidate and ensures the exact review bound to the
// candidate digest; apply before a decision refuses review_required naming
// that digest; the owner's approval of exactly that review lets apply
// activate; the applied revision carries the same digest.
func TestPlanEnsuresReviewAndOwnerDecisionUnblocksApply(t *testing.T) {
	env := newEnv(t)
	requireReview(env)
	workerID := env.ids.New()
	plan := env.planDraft(env.stage(workerChange(workerID, env.org, "reviewed-worker")))

	ensured := env.ports.ensuredActions()
	review, ok := ensured[plan.CandidateDigest]
	if !ok || len(ensured) != 1 {
		t.Fatalf("plan ensured reviews for %v, want exactly one bound to the candidate digest %s", keysOf(ensured), plan.CandidateDigest)
	}
	if review.Scope != env.scope || review.Action.Scope != env.scope {
		t.Fatalf("review scope %+v / action scope %+v, want the plan scope %+v", review.Scope, review.Action.Scope, env.scope)
	}
	if review.Action.Destination != applyDestination || review.Action.ConfigurationRevision != plan.BaseRevision {
		t.Fatalf("review preview = %+v, want configuration.apply at base revision %d", review.Action, plan.BaseRevision)
	}
	if !strings.Contains(string(review.Action.Parameters), string(plan.ID)) ||
		!strings.Contains(string(review.Action.Parameters), string(workerID)) {
		t.Fatalf("review preview parameters %s do not name the plan and its staged change", review.Action.Parameters)
	}
	if len(plan.Decisions) != 1 || plan.Decisions[0].ActionDigest != plan.CandidateDigest {
		t.Fatalf("sealed decisions = %+v, want one bound to %s", plan.Decisions, plan.CandidateDigest)
	}

	// Undecided: apply refuses with the digest it waits on.
	apply := applyInput{Scope: env.scope, PlanID: plan.ID, BaseRevision: plan.BaseRevision, CandidateDigest: plan.CandidateDigest}
	fault := env.expectFault("configuration.apply", apply, contract.CodeReviewRequired)
	if !strings.Contains(fault.Message, plan.CandidateDigest) {
		t.Fatalf("review_required fault %q does not name the candidate digest", fault.Message)
	}
	if _, found := env.rowState(kindWorker, workerID); found {
		t.Fatalf("a refused apply left the worker effective")
	}

	// The eligible owner decides exactly that review; apply proceeds and
	// binds the same digest to the revision.
	env.ports.approve(t, plan.CandidateDigest)
	rev := env.apply(plan)
	if rev.CandidateDigest != plan.CandidateDigest || rev.PlanID != plan.ID {
		t.Fatalf("revision = %+v, want plan %s digest %s", rev, plan.ID, plan.CandidateDigest)
	}
	if state, found := env.rowState(kindWorker, workerID); !found || state != stateActive {
		t.Fatalf("applied plan left the worker %q (found %v), want active", state, found)
	}
	// The review was ensured once at plan time; apply consulted it, it did
	// not create another.
	if len(env.ports.ensuredActions()) != 1 {
		t.Fatalf("apply ensured additional reviews: %v", keysOf(env.ports.ensuredActions()))
	}
}

// TestApplyWithoutEligibleDecisionStaysReviewRequired: a decision over a
// different digest, or no decision at all, never unblocks apply; the fault
// carries the sealed decision requirements for the caller to act on.
func TestApplyWithoutEligibleDecisionStaysReviewRequired(t *testing.T) {
	env := newEnv(t)
	requireReview(env)
	first := env.planDraft(env.stage(workerChange(env.ids.New(), env.org, "first")))
	second := env.planDraft(env.stage(workerChange(env.ids.New(), env.org, "second")))
	if first.CandidateDigest == second.CandidateDigest {
		t.Fatalf("two plans share candidate digest %s", first.CandidateDigest)
	}
	env.ports.approve(t, first.CandidateDigest)

	fault := env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: second.ID, BaseRevision: second.BaseRevision, CandidateDigest: second.CandidateDigest,
	}, contract.CodeReviewRequired)
	if !strings.Contains(string(fault.Details), second.CandidateDigest) || !strings.Contains(string(fault.Details), "human_required") {
		t.Fatalf("review_required details %s must carry the sealed decision requirement for %s", fault.Details, second.CandidateDigest)
	}
	if env.head() != 1 {
		t.Fatalf("head = %d after a refused apply, want 1", env.head())
	}
}

// TestPlanFailsClosedWhenReviewCannotBeEnsured: if the reviews owner
// refuses the ensure, no plan is sealed.
func TestPlanFailsClosedWhenReviewCannotBeEnsured(t *testing.T) {
	env := newEnv(t)
	requireReview(env)
	env.ports.mu.Lock()
	env.ports.fail["_reviews.ensure"] = &contract.Fault{Code: contract.CodeControllerUnavailable, Message: "reviews unavailable", Retryable: true}
	env.ports.mu.Unlock()
	draft := env.stage(workerChange(env.ids.New(), env.org, "unensured"))
	_, err := env.tryPlan(draft)
	var fault *contract.Fault
	if !errors.As(err, &fault) || fault.Code != contract.CodeControllerUnavailable {
		t.Fatalf("plan error = %v, want the reviews owner's refusal", err)
	}
	var plans int
	if err := env.db.Read(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
		return unit.QueryRowContext(env.ctx, `SELECT COUNT(*) FROM configuration_plans`).Scan(&plans)
	}); err != nil {
		t.Fatalf("count plans: %v", err)
	}
	if plans != 0 {
		t.Fatalf("a failed plan left %d sealed plan rows", plans)
	}
}

func keysOf(m map[string]reviewsEnsureInput) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
