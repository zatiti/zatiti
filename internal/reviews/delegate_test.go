package reviews

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// review.delegate: single-hop delegation inside the eligible class.

func TestDelegateRecordsSingleHop(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	delegate := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	updated, fault := e.delegateAs(e.actorFor(human), wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: delegate,
	})
	if fault != nil {
		t.Fatalf("delegate: %v", fault)
	}
	if updated.Version != review.Version+1 || updated.State != statePending {
		t.Fatalf("review after delegation = version %d state %s, want version %d pending",
			updated.Version, updated.State, review.Version+1)
	}
	if updated.DecisionID != nil {
		t.Fatalf("delegated review carries decision %s", *updated.DecisionID)
	}

	evs := e.eventsOfKind(eventReviewDelegated)
	if len(evs) != 1 {
		t.Fatalf("delegated events = %d, want 1", len(evs))
	}
	var data eventDelegatedData
	if err := jsonUnmarshalForTest(t, evs[0].Data, &data); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	if data.DelegatorID != human || data.DelegateeID != delegate {
		t.Fatalf("delegated event data = %+v", data)
	}
}

func jsonUnmarshalForTest(t *testing.T, raw []byte, out any) error {
	t.Helper()
	return json.Unmarshal(raw, out)
}

func TestDelegateSelfRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	_ = e.expectFaultAs(e.actorFor(human), opDelegate, wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: human,
	}, contract.CodeInvalidInput)
}

func TestDelegateRequiresEligibleDelegator(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	outsider := e.principal(contract.KindHuman)
	target := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	// An ineligible principal cannot delegate, and its own delegation to
	// itself is refused earlier as self-delegation.
	f := e.expectFaultAs(e.actorFor(outsider), opDelegate, wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: target,
	}, contract.CodePermissionDenied)
	if !strings.Contains(f.Message, "eligible") {
		t.Fatalf("fault message %q does not name eligibility", f.Message)
	}
}

func TestDelegateChainRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	first := e.principal(contract.KindHuman)
	second := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	if _, fault := e.delegateAs(e.actorFor(human), wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: first,
	}); fault != nil {
		t.Fatalf("first delegation: %v", fault)
	}

	// The delegatee cannot re-delegate: delegation is single-hop by
	// construction because the delegator must appear in the snapshot.
	_ = e.expectFaultAs(e.actorFor(first), opDelegate, wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version + 1, PrincipalID: second,
	}, contract.CodePermissionDenied)
}

func TestDelegateUnknownPrincipalInvalid(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	_ = e.expectFaultAs(e.actorFor(human), opDelegate, wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: e.ids.New(),
	}, contract.CodeInvalidInput)
}

func TestDelegateRevokedDelegateeRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	delegate := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	e.ports.revoke(delegate)
	_ = e.expectFaultAs(e.actorFor(human), opDelegate, wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: delegate,
	}, contract.CodePermissionDenied)
}

func TestDelegateAgentDelegateeRefusedForHumanRequired(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	agent := e.principal(contract.KindClientAgent)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	_ = e.expectFaultAs(e.actorFor(human), opDelegate, wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: agent,
	}, contract.CodePermissionDenied)
}

func TestDelegateProposerDelegateeRefusedForSeparation(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.principal(contract.KindHuman)
	req := requirementFixture([]contract.ID{human}, true)
	req.SeparateProposer = true
	proposerActor := e.actorFor(proposer)
	in := ensureInputFor(t, e.scope, actionFixture(e.scope, "https://api.example.com/v1/deploy"), req)
	review, fault := e.ensureFor(proposerActor, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}

	_ = e.expectFaultAs(e.actorFor(human), opDelegate, wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: proposer,
	}, contract.CodePermissionDenied)
}

func TestDelegateDuplicateIsNoVersionBump(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	delegate := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))
	in := wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: delegate,
	}
	if _, fault := e.delegateAs(e.actorFor(human), in); fault != nil {
		t.Fatalf("first delegation: %v", fault)
	}

	// Repeat with the new expected version: no-op, same version, no event.
	in.ExpectedVersion = review.Version + 1
	again, fault := e.delegateAs(e.actorFor(human), in)
	if fault != nil {
		t.Fatalf("duplicate delegation: %v", fault)
	}
	if again.Version != review.Version+1 {
		t.Fatalf("duplicate delegation bumped version to %d", again.Version)
	}
	if got := len(e.eventsOfKind(eventReviewDelegated)); got != 1 {
		t.Fatalf("delegated events = %d, want 1", got)
	}
}

func TestDelegateStaleVersionRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	delegate := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	_ = e.expectFaultAs(e.actorFor(human), opDelegate, wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version + 3, PrincipalID: delegate,
	}, contract.CodeStaleVersion)
}

func TestDelegateDecidedReviewRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	delegate := e.principal(contract.KindHuman)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))
	if _, fault := e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "ok",
	}); fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	_ = e.expectFaultAs(e.actorFor(human), opDelegate, wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version + 1, PrincipalID: delegate,
	}, contract.CodeConflict)
}

func TestDelegateProposerCannotDelegate(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.principal(contract.KindHuman)
	req := requirementFixture([]contract.ID{proposer, human}, true)
	req.SeparateProposer = true
	proposerActor := e.actorFor(proposer)
	in := ensureInputFor(t, e.scope, actionFixture(e.scope, "https://api.example.com/v1/deploy"), req)
	review, fault := e.ensureFor(proposerActor, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}

	// The proposer is in the eligible class but separation forbids the
	// proposer from acting as reviewer, delegation included.
	_ = e.expectFaultAs(proposerActor, opDelegate, wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: human,
	}, contract.CodePermissionDenied)
}
