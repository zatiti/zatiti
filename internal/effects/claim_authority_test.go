package effects

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestClaimRechecksCurrentPolicyWithoutConsuming(t *testing.T) {
	env := newEnv(t)
	operation, attempt := env.admitted()
	env.ports.policyDecision = policyDeny
	_ = env.expectFault(opClaim, claimInput{OperationID: operation.ID, AttemptID: attempt, Generation: env.generation()}, contract.CodePermissionDenied)
	if claim := env.claimOf(attempt); claim == nil || claim.Consumed {
		t.Fatal("refused dispatch consumed its one-use claim")
	}
	env.ports.policyDecision = policyAllow
	dispatch := env.claimOp(operation.ID, attempt, env.generation())
	if dispatch.AttemptID != attempt || len(env.reserveCalls()) != 1 {
		t.Fatal("renewed authority created another physical attempt or reservation")
	}
}

func TestClaimRechecksExactReviewWithoutConsuming(t *testing.T) {
	env := newEnv(t)
	operation, attempt := env.admitted()
	env.ports.policyDecision = policyReview
	env.ports.reviewEligible = false
	_ = env.expectFault(opClaim, claimInput{OperationID: operation.ID, AttemptID: attempt, Generation: env.generation()}, contract.CodeReviewRequired)
	if claim := env.claimOf(attempt); claim == nil || claim.Consumed {
		t.Fatal("missing current review consumed the dispatch claim")
	}
	env.ports.reviewEligible = true
	env.ports.reviewDecision = approvedDecision(operation.ActionDigest)
	dispatch := env.claimOp(operation.ID, attempt, env.generation())
	if dispatch.AttemptID != attempt {
		t.Fatal("exact current approval changed the attempt identity")
	}
}
