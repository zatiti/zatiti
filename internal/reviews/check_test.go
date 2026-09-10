package reviews

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// _reviews.check: pure recheck. False is not permission; every answer but a
// currently standing approval is eligible=false.

// pendingReview is the common fixture: one pending review with the given
// requirement, proposed by a client agent, plus its input for decide. Each
// call mints a distinct action digest so several pending reviews can coexist
// in one environment.
func pendingReview(t *testing.T, e *testEnv, req wireRequirement) (wireReview, contract.Actor) {
	t.Helper()
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	action := actionFixture(e.scope, "https://api.example.com/v1/deploy?r="+string(e.ids.New()))
	in := ensureInputFor(t, e.scope, action, req)
	review, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}
	return review, proposer
}

func TestCheckUnknownDigestIsIneligible(t *testing.T) {
	e := newEnv(t)
	out, fault := e.checkFor(e.scope, "1111111111111111111111111111111111111111111111111111111111111111")
	if fault != nil {
		t.Fatalf("check: %v", fault)
	}
	if out.Eligible || out.Decision != nil {
		t.Fatalf("unknown digest = %+v, want ineligible without decision", out)
	}
}

func TestCheckPendingIsIneligible(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	out, fault := e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check: %v", fault)
	}
	if out.Eligible || out.Decision != nil {
		t.Fatalf("pending review = %+v, want ineligible", out)
	}
	// Queries never mutate: the pending review is untouched even past its
	// expiry.
	e.clock.advance(24 * time.Hour)
	got, fault := e.getReview(review.ID)
	if fault != nil {
		t.Fatalf("get review: %v", fault)
	}
	if got.State != statePending || got.Version != 1 {
		t.Fatalf("lazy expiry violated: state/version = %s/%d, want pending/1", got.State, got.Version)
	}
}

func TestCheckApprovedStands(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	decision, fault := e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "preview matches the task",
	})
	if fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	out, fault := e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check: %v", fault)
	}
	if !out.Eligible {
		t.Fatalf("standing approval = %+v, want eligible", out)
	}
	if out.Decision == nil || out.Decision.ID != decision.ID {
		t.Fatalf("check decision %+v, want %s", out.Decision, decision.ID)
	}
	// Still standing after time passes inside the window.
	e.clock.advance(2 * time.Hour)
	out, fault = e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check after advance: %v", fault)
	}
	if !out.Eligible {
		t.Fatal("approval inside its window stopped standing")
	}
}

func TestCheckApprovedPastExpiryIsIneligible(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))
	if _, fault := e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "ok",
	}); fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	e.clock.advance(4 * time.Hour)
	out, fault := e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check: %v", fault)
	}
	if out.Eligible {
		t.Fatal("approval stood past its requirement expiry")
	}
}

func TestCheckApprovedWithRevokedReviewerIsIneligible(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))
	if _, fault := e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "ok",
	}); fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	e.ports.revoke(human)
	out, fault := e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check: %v", fault)
	}
	if out.Eligible {
		t.Fatal("approval stood after the reviewer was revoked")
	}
}

func TestCheckApprovedWithAgentReviewerStandsWithoutHumanRequired(t *testing.T) {
	e := newEnv(t)
	agent := e.principal(contract.KindClientAgent)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{agent}, false))

	// The agent was the eligible snapshot at decide time and the
	// requirement does not demand a human, so the approval stands until
	// the requirement changes on a later review.
	if _, fault := e.decideAs(e.actorFor(agent), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "ok",
	}); fault != nil {
		t.Fatalf("decide: %v", fault)
	}
	out, fault := e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check: %v", fault)
	}
	if !out.Eligible {
		t.Fatalf("non-human-required approval by eligible agent = %+v, want eligible", out)
	}
}

func TestCheckRejectedCarriesDecision(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))
	decision, fault := e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideReject, Reason: "no",
	})
	if fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	out, fault := e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check: %v", fault)
	}
	if out.Eligible {
		t.Fatal("rejected digest checked eligible")
	}
	if out.Decision == nil || out.Decision.ID != decision.ID || out.Decision.Decision != decideReject {
		t.Fatalf("check decision %+v, want the rejection %s", out.Decision, decision.ID)
	}
}

func TestCheckPropagatesPortFailures(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))
	if _, fault := e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "ok",
	}); fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	// An authority transport failure must propagate as an error, never
	// silently degrade to eligible=false.
	e.ports.inject(opIdentityAuthority, &contract.Fault{Code: contract.CodeInternalError, Message: "identity unavailable"})
	actor := e.actorFor(e.principal(contract.KindService))
	payload, err := e.callAs(actor, e.scope, opCheck, wireCheckInput{Scope: e.scope, ActionDigest: review.ActionDigest})
	if err == nil && payload.Error == nil {
		t.Fatal("port failure during check: expected an error")
	}
}

func TestCheckRefusesForeignScope(t *testing.T) {
	e := newEnv(t)
	foreign := contract.Scope{InstallationID: e.ids.New()}
	actor := e.actorFor(e.principal(contract.KindService))
	_ = e.expectFaultAs(actor, opCheck, wireCheckInput{
		Scope: foreign, ActionDigest: "1111111111111111111111111111111111111111111111111111111111111111",
	}, contract.CodeInvalidInput)
}
