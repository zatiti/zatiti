package reviews

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// review.decide: immutable decisions behind current-authority gates.

func TestDecideRecordsImmutableApproval(t *testing.T) {
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
	if decision.Decision != decideApprove || decision.ReviewerID != human || decision.ReviewID != review.ID {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.ReviewVersion != review.Version {
		t.Fatalf("decision review_version = %d, want %d", decision.ReviewVersion, review.Version)
	}
	if decision.ActionDigest != review.ActionDigest {
		t.Fatalf("decision digest %s, want %s", decision.ActionDigest, review.ActionDigest)
	}
	if decision.Reason != "preview matches the task" {
		t.Fatalf("reason %q not recorded", decision.Reason)
	}

	// The review transitioned with a version bump and a decision pointer.
	got, fault := e.getReview(review.ID)
	if fault != nil {
		t.Fatalf("get review: %v", fault)
	}
	if got.State != stateApproved || got.Version != review.Version+1 || got.DecisionID == nil || *got.DecisionID != decision.ID {
		t.Fatalf("review after decide = state %s version %d decision %v", got.State, got.Version, got.DecisionID)
	}

	// One decided event with the decision payload.
	evs := e.eventsOfKind(eventReviewDecided)
	if len(evs) != 1 {
		t.Fatalf("decided events = %d, want 1", len(evs))
	}
	var data eventDecidedData
	if err := json.Unmarshal(evs[len(evs)-1].Data, &data); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	if data.DecisionID != decision.ID || data.Decision != decideApprove || data.ReviewerID != human {
		t.Fatalf("decided event data = %+v", data)
	}
}

func TestDecideIsIdempotentRefusal(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))
	in := wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "ok",
	}
	if _, fault := e.decideAs(e.actorFor(human), in); fault != nil {
		t.Fatalf("first decide: %v", fault)
	}

	// The same decision again is a conflict, not a second record; a
	// rejection of an approved review is also a conflict. The review is at
	// version 2 after the first decide, so a stale expected_version would
	// refuse earlier than the state gate.
	in.ExpectedVersion = review.Version + 1
	for _, want := range []string{decideApprove, decideReject} {
		in.Decision = want
		f := e.expectFaultAs(e.actorFor(human), opDecide, in, contract.CodeConflict)
		if !strings.Contains(f.Message, "already") {
			t.Fatalf("conflict message %q does not explain the decided review", f.Message)
		}
	}
	if got := len(e.eventsOfKind(eventReviewDecided)); got != 1 {
		t.Fatalf("decided events = %d, want 1 (immutability)", got)
	}
}

func TestDecideStaleVersionRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	f := e.expectFaultAs(e.actorFor(human), opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version + 5,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "ok",
	}, contract.CodeStaleVersion)
	if !strings.Contains(f.Message, string(review.ID)) {
		t.Fatalf("stale version message %q does not name the review", f.Message)
	}
}

func TestDecideDigestMismatchRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	_ = e.expectFaultAs(e.actorFor(human), opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: strings.Repeat("f", 64), Decision: decideApprove, Reason: "ok",
	}, contract.CodeInvalidInput)
}

func TestDecideUnknownReviewNotFound(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	_ = e.expectFaultAs(e.actorFor(human), opDecide, wireDecideInput{
		Scope: e.scope, ID: e.ids.New(), ExpectedVersion: 1,
		ActionDigest: strings.Repeat("a", 64), Decision: decideApprove, Reason: "ok",
	}, contract.CodeNotFound)
}

func TestDecideExpiredReviewRequiresRenewal(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	req := requirementFixture([]contract.ID{human}, true)
	req.ExpiresAt = e.clock.Now().Add(time.Hour)
	review, _ := pendingReview(t, e, req)

	// The pending review expires without a new ensure arriving.
	e.clock.advance(2 * time.Hour)
	f := e.expectFaultAs(e.actorFor(human), opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "late",
	}, contract.CodeReviewRequired)
	if !strings.Contains(f.Message, "expired") {
		t.Fatalf("fault message %q does not name expiry", f.Message)
	}

	// An explicitly expired review (transitioned by a fresh ensure of the
	// same digest) is equally refused.
	req2 := requirementFixture([]contract.ID{human}, true)
	req2.ExpiresAt = e.clock.Now().Add(time.Hour)
	review2, _ := pendingReview(t, e, req2)
	e.clock.advance(2 * time.Hour)
	req3 := requirementFixture([]contract.ID{human}, true)
	req3.ExpiresAt = e.clock.Now().Add(time.Hour)
	// Re-ensuring review2's exact action with a fresh requirement is what
	// transitions the stale pending review to expired.
	in3 := ensureInputFor(t, e.scope, review2.Preview, req3)
	if _, fault := e.ensureFor(e.actorFor(e.principal(contract.KindClientAgent)), e.scope, in3); fault != nil {
		t.Fatalf("ensure after expiry: %v", fault)
	}
	_ = e.expectFaultAs(e.actorFor(human), opDecide, wireDecideInput{
		Scope: e.scope, ID: review2.ID, ExpectedVersion: 2,
		ActionDigest: review2.ActionDigest, Decision: decideApprove, Reason: "ok",
	}, contract.CodeReviewRequired)
}

func TestDecideAgentCannotSatisfyHumanRequiredReview(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	agent := e.principal(contract.KindClientAgent)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	in := wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "approved_by_human",
	}
	// The agent is not even in the eligible class, which refuses first:
	// either gate holds, whichever sees the request.
	_ = e.expectFaultAs(e.actorFor(agent), opDecide, in, contract.CodePermissionDenied)

	// Put the agent in the eligible class: the human-required kind gate
	// alone must still refuse it, because the gate reads current identity.
	req := requirementFixture([]contract.ID{agent}, true)
	review2, _ := pendingReview(t, e, req)
	in2 := wireDecideInput{
		Scope: e.scope, ID: review2.ID, ExpectedVersion: review2.Version,
		ActionDigest: review2.ActionDigest, Decision: decideApprove, Reason: "impersonation",
	}
	_ = e.expectFaultAs(e.actorFor(agent), opDecide, in2, contract.CodePermissionDenied)

	// A principal whose registered kind is human passes the same gate.
	humanKind := e.principal(contract.KindHuman)
	reqH := requirementFixture([]contract.ID{humanKind}, true)
	reviewH, _ := pendingReview(t, e, reqH)
	if _, fault := e.decideAs(e.actorFor(humanKind), wireDecideInput{
		Scope: e.scope, ID: reviewH.ID, ExpectedVersion: reviewH.Version,
		ActionDigest: reviewH.ActionDigest, Decision: decideApprove, Reason: "legitimate",
	}); fault != nil {
		t.Fatalf("human-kind eligible reviewer must pass: %v", fault)
	}

	// And a worker kind in the eligible class is refused.
	worker := e.principal(contract.KindWorker)
	req3 := requirementFixture([]contract.ID{worker}, true)
	review3, _ := pendingReview(t, e, req3)
	_ = e.expectFaultAs(e.actorFor(worker), opDecide, wireDecideInput{
		Scope: e.scope, ID: review3.ID, ExpectedVersion: review3.Version,
		ActionDigest: review3.ActionDigest, Decision: decideApprove, Reason: "not human",
	}, contract.CodePermissionDenied)
}

func TestDecideProposerSeparationEnforced(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	req := requirementFixture([]contract.ID{human}, true)
	req.SeparateProposer = true
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	in := ensureInputFor(t, e.scope, actionFixture(e.scope, "https://api.example.com/v1/deploy"), req)
	review, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}

	// The proposer decides their own review: refused.
	_ = e.expectFaultAs(proposer, opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "self approval",
	}, contract.CodePermissionDenied)

	// An eligible human reviewer decides: accepted.
	if _, fault := e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "independent review",
	}); fault != nil {
		t.Fatalf("independent review: %v", fault)
	}
}

func TestDecideIneligiblePrincipalRefused(t *testing.T) {
	e := newEnv(t)
	eligible := e.principal(contract.KindHuman)
	outsider := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{eligible}, true))

	_ = e.expectFaultAs(e.actorFor(outsider), opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "not on the list",
	}, contract.CodePermissionDenied)
}

func TestDecideRevokedReviewerRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))
	reviewer := e.actorFor(human)

	e.ports.revoke(human)
	_ = e.expectFaultAs(reviewer, opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "ok",
	}, contract.CodePermissionDenied)
}

func TestDecideViaDelegation(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	delegate := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	// The eligible human delegates to the (initially ineligible) delegate.
	updated, fault := e.delegateAs(e.actorFor(human), wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: delegate,
	})
	if fault != nil {
		t.Fatalf("delegate: %v", fault)
	}
	if updated.Version != review.Version+1 || updated.State != statePending {
		t.Fatalf("review after delegation = version %d state %s", updated.Version, updated.State)
	}
	if got := len(e.eventsOfKind(eventReviewDelegated)); got != 1 {
		t.Fatalf("delegated events = %d, want 1", got)
	}

	// The delegatee now decides.
	decision, fault := e.decideAs(e.actorFor(delegate), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: updated.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "on delegated authority",
	})
	if fault != nil {
		t.Fatalf("decide by delegatee: %v", fault)
	}
	if decision.ReviewerID != delegate {
		t.Fatalf("decision reviewer %s, want the delegatee %s", decision.ReviewerID, delegate)
	}
}

func TestDecideViaRevokedDelegatorRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	delegate := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	if _, fault := e.delegateAs(e.actorFor(human), wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: delegate,
	}); fault != nil {
		t.Fatalf("delegate: %v", fault)
	}

	// The delegator's revocation withdraws the borrowed authority.
	e.ports.revoke(human)
	_ = e.expectFaultAs(e.actorFor(delegate), opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version + 1,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "borrowed",
	}, contract.CodePermissionDenied)
}
