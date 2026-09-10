package reviews

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Named acceptance cases (local reviews slices). Each test names its case
// and asserts the exact expected observable from the brief. Cross-package
// journeys (CLI/MCP parity, desktop, task acceptance labels) are owned by
// integration; the reviews-side properties they depend on are proven here.

// Z07.exact_action_changes — every material change invalidates the
// digest-bound approval; dispatch waits for a new decision over the exact
// changed preview.
func TestZ07ExactActionChanges(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))

	base := actionFixture(e.scope, "https://api.example.com/v1/deploy")
	req := requirementFixture([]contract.ID{human}, true)
	review, fault := e.ensureFor(proposer, e.scope, ensureInputFor(t, e.scope, base, req))
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}
	if _, fault = e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "exact preview reviewed",
	}); fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	// Independently change each material field; each changed action is a
	// different digest with no standing approval, while the original
	// approval stays bound to the original digest.
	changes := map[string]func(a wireAction) wireAction{
		"content": func(a wireAction) wireAction {
			a.Content = []wireArtifactRef{{ID: eToolID, Digest: strings.Repeat("2", 64)}}
			return a
		},
		"destination": func(a wireAction) wireAction {
			a.Destination = "https://api.example.com/v2/deploy"
			return a
		},
		"timing": func(a wireAction) wireAction {
			a.NotBefore = a.NotBefore.Add(3 * time.Hour)
			return a
		},
		"repository head": func(a wireAction) wireAction {
			a.Preconditions = json.RawMessage(`{"repository_head":"deadbeef"}`)
			return a
		},
		"account": func(a wireAction) wireAction {
			a.AccountIdentity = "acct_9876543210"
			return a
		},
		"configuration": func(a wireAction) wireAction {
			a.ConfigurationRevision = 8
			return a
		},
	}
	for name, mutate := range changes {
		changed := mutate(base)
		digest, err := actionDigest(changed)
		if err != nil {
			t.Fatalf("digest for %s change: %v", name, err)
		}
		out, fault := e.checkFor(e.scope, digest)
		if fault != nil {
			t.Fatalf("check after %s change: %v", name, fault)
		}
		if out.Eligible {
			t.Fatalf("%s change inherited the approval; dispatch would run unreviewed", name)
		}
		// The original digest still holds its approval: history is not
		// rewritten by the change.
		orig, fault := e.checkFor(e.scope, review.ActionDigest)
		if fault != nil {
			t.Fatalf("check original after %s change: %v", name, fault)
		}
		if !orig.Eligible {
			t.Fatalf("original approval lost after %s change", name)
		}
	}
}

// Z07.agent_impersonates_human — an agent credential cannot satisfy a
// human-required review regardless of payload or transport.
func TestZ07AgentImpersonatesHuman(t *testing.T) {
	e := newEnv(t)
	agent := e.principal(contract.KindClientAgent)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{e.principal(contract.KindHuman)}, true))

	// The assertion field cannot even reach the handler: strict decoding
	// rejects unknown input fields on both transports, which run the same
	// Handle path.
	_ = e.expectFaultAs(e.actorFor(agent), opDecide, map[string]any{
		"scope":             e.scope,
		"id":                review.ID,
		"expected_version":  review.Version,
		"action_digest":     review.ActionDigest,
		"decision":          decideApprove,
		"reason":            "approved_by_human: yes",
		"approved_by_human": true,
	}, contract.CodeInvalidInput)

	// Without the assertion, the credential kind gate refuses the agent.
	_ = e.expectFaultAs(e.actorFor(agent), opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "trust me",
	}, contract.CodePermissionDenied)

	// Nothing was committed: the review is still pending at v1 with no
	// decision, and check refuses.
	got, fault := e.getReview(review.ID)
	if fault != nil {
		t.Fatalf("get review: %v", fault)
	}
	if got.State != statePending || got.Version != review.Version || got.DecisionID != nil {
		t.Fatalf("review mutated by agent attempt: state %s version %d", got.State, got.Version)
	}
	out, fault := e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check: %v", fault)
	}
	if out.Eligible {
		t.Fatal("agent impersonation produced eligibility")
	}
}

// Z07.reviewer_eligibility — current identity gates every decision path;
// permitted delegation cannot bypass human-required or separation rules.
func TestZ07ReviewerEligibility(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)

	// Stale version.
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))
	_ = e.expectFaultAs(e.actorFor(human), opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version + 1,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "stale",
	}, contract.CodeStaleVersion)

	// Expired requirement: ensure refuses it upfront.
	expired := requirementFixture([]contract.ID{human}, true)
	expired.ExpiresAt = e.clock.Now().Add(-time.Minute)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	in := ensureInputFor(t, e.scope, actionFixture(e.scope, "https://api.example.com/v1/rotate"), expired)
	if _, fault := e.ensureFor(proposer, e.scope, in); fault == nil || fault.Code != contract.CodeInvalidInput {
		t.Fatalf("expired requirement accepted: %v", fault)
	}

	// Revoked reviewer.
	e.ports.revoke(human)
	_ = e.expectFaultAs(e.actorFor(human), opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "revoked",
	}, contract.CodePermissionDenied)
	e.ports.setAuthority(human, contract.KindHuman, false)

	// Prohibited proposer under separation.
	sepHuman := e.principal(contract.KindHuman)
	sepReq := requirementFixture([]contract.ID{sepHuman}, true)
	sepReq.SeparateProposer = true
	sepProposer := e.actorFor(sepHuman)
	sepReview, fault := e.ensureFor(sepProposer, e.scope,
		ensureInputFor(t, e.scope, actionFixture(e.scope, "https://api.example.com/v2/rotate"), sepReq))
	if fault != nil {
		t.Fatalf("ensure separated: %v", fault)
	}
	_ = e.expectFaultAs(sepProposer, opDecide, wireDecideInput{
		Scope: e.scope, ID: sepReview.ID, ExpectedVersion: sepReview.Version,
		ActionDigest: sepReview.ActionDigest, Decision: decideApprove, Reason: "self",
	}, contract.CodePermissionDenied)

	// A permitted delegation is scoped: it grants exactly one review to
	// exactly one principal, and cannot bypass the human-required rule.
	delegate := e.principal(contract.KindClientAgent)
	humanReview, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))
	if _, fault := e.delegateAs(e.actorFor(human), wireDelegateInput{
		Scope: e.scope, ID: humanReview.ID, ExpectedVersion: humanReview.Version, PrincipalID: delegate,
	}); fault == nil || fault.Code != contract.CodePermissionDenied {
		t.Fatalf("delegation to agent for human-required review accepted: %v", fault)
	}
}

// Z07.transport_denial — the decision path is transport-agnostic: an agent
// principal is refused identically no matter which interface drives the
// invocation, and an eligible human decides through either.
func TestZ07TransportDenial(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	agent := e.principal(contract.KindClientAgent)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{human}, true))

	// Both "transports" are modeled as identical invocations of the same
	// operation through Service.Handle — the denial must be identical, and
	// no interface-side shortcut exists.
	cliIn := wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "via cli",
	}
	mcpIn := cliIn
	mcpIn.Reason = "via mcp"
	for _, in := range []wireDecideInput{cliIn, mcpIn} {
		_ = e.expectFaultAs(e.actorFor(agent), opDecide, in, contract.CodePermissionDenied)
	}

	// An eligible human session decides through either interface.
	if _, fault := e.decideAs(e.actorFor(human), cliIn); fault != nil {
		t.Fatalf("human decide via cli: %v", fault)
	}
	// The review is decided now; a second transport attempt sees the
	// immutable decision, not a parallel path. The version moved to 2 with
	// the decision, so a current replay reaches the state gate.
	mcpIn.ExpectedVersion = review.Version + 1
	_ = e.expectFaultAs(e.actorFor(human), opDecide, mcpIn, contract.CodeConflict)
}

// Z19.human_review_preserved — qualification does not soften a
// human-required class; only the stored requirement governs.
func TestZ19HumanReviewPreserved(t *testing.T) {
	e := newEnv(t)
	// The worker earned a qualification: represented here by the agent
	// being added to the eligible class of its own review.
	agent := e.principal(contract.KindClientAgent)
	review, _ := pendingReview(t, e, requirementFixture([]contract.ID{agent}, true))

	// The human-required decision remains mandatory for the qualified
	// principal: kind is checked against current identity, not claims.
	_ = e.expectFaultAs(e.actorFor(agent), opDecide, wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "qualified now",
	}, contract.CodePermissionDenied)

	// The requirement is immutable: no operation in this package rewrites
	// a stored requirement; only a new review can carry a changed one.
	stored, fault := e.getReview(review.ID)
	if fault != nil {
		t.Fatalf("get: %v", fault)
	}
	if !stored.Requirement.HumanRequired {
		t.Fatal("requirement lost its human-required flag")
	}
}

// Z21.exact_decision — the card is the actual action; one eligible action
// is approved; the stale action cannot reuse approval; chat assertions are
// not approval authority.
func TestZ21ExactDecision(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))

	// Two unresolved human-required actions across two organizations and
	// one stale action whose window has passed.
	org1 := e.ids.New()
	org2 := e.ids.New()
	scope1 := contract.Scope{InstallationID: e.install, OrganizationID: org1}
	scope2 := contract.Scope{InstallationID: e.install, OrganizationID: org2}
	action1 := actionFixture(scope1, "https://api.example.com/v1/publish")
	action2 := actionFixture(scope2, "https://api.example.com/v2/publish")
	staleAction := actionFixture(e.scope, "https://api.example.com/v1/deploy")

	review1, fault := e.ensureFor(proposer, scope1, ensureInputFor(t, scope1, action1, requirementFixture([]contract.ID{human}, true)))
	if fault != nil {
		t.Fatalf("ensure action1: %v", fault)
	}
	review2, fault := e.ensureFor(proposer, scope2, ensureInputFor(t, scope2, action2, requirementFixture([]contract.ID{human}, true)))
	if fault != nil {
		t.Fatalf("ensure action2: %v", fault)
	}
	staleReq := requirementFixture([]contract.ID{human}, true)
	staleReq.ExpiresAt = e.clock.Now().Add(time.Hour)
	staleReview, fault := e.ensureFor(proposer, e.scope, ensureInputFor(t, e.scope, staleAction, staleReq))
	if fault != nil {
		t.Fatalf("ensure stale: %v", fault)
	}
	// The stale action's window passes; policy re-issues the requirement
	// and a fresh pending review replaces it while the old one expires.
	e.clock.advance(2 * time.Hour)
	reissued := requirementFixture([]contract.ID{human}, true)
	reissued.ExpiresAt = e.clock.Now().Add(time.Hour)
	fresh, fault := e.ensureFor(proposer, e.scope, ensureInputFor(t, e.scope, staleAction, reissued))
	if fault != nil {
		t.Fatalf("re-ensure stale: %v", fault)
	}

	// The Needs-you queue lists exactly the unresolved reviews the human
	// is eligible for.
	pendingState := statePending
	trueValue := true
	page, next, fault := e.listReviews(e.actorFor(human), wireListInput{
		Scope: e.scope, Filter: &wireListFilter{State: &pendingState, NeedsYou: &trueValue},
	})
	if fault != nil {
		t.Fatalf("needs you list: %v", fault)
	}
	if next != nil {
		t.Fatal("needs you list paginated unexpectedly")
	}
	if len(page.Items) != 3 {
		t.Fatalf("needs you shows %d cards, want 3", len(page.Items))
	}

	// Inspecting the exact card shows the actual action, not a summary.
	var card1 *wireReview
	for i := range page.Items {
		if page.Items[i].ID == review1.ID {
			card1 = &page.Items[i]
			break
		}
	}
	if card1 == nil {
		t.Fatal("action1 card missing from needs-you queue")
	}
	wantDigest, err := actionDigest(action1)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if card1.ActionDigest != wantDigest || card1.Preview.Destination != action1.Destination ||
		card1.Preview.AccountIdentity != action1.AccountIdentity ||
		string(card1.Preview.Preconditions) != string(action1.Preconditions) {
		t.Fatal("card does not show the exact action")
	}

	// Approve one eligible action.
	if _, fault = e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review1.ID, ExpectedVersion: review1.Version,
		ActionDigest: review1.ActionDigest, Decision: decideApprove, Reason: "verified against the task",
	}); fault != nil {
		t.Fatalf("approve: %v", fault)
	}
	_ = review2

	// The stale action cannot reuse approval: its original review expired
	// and the re-issued one is still pending.
	out, fault := e.checkFor(e.scope, staleReview.ActionDigest)
	if fault != nil {
		t.Fatalf("check stale: %v", fault)
	}
	if out.Eligible {
		t.Fatal("stale action reused approval")
	}

	// A chat assertion is not approval authority: there is no field that
	// could carry it, and an ineligible "approver" is refused regardless
	// of the claimed conversation.
	chatAgent := e.principal(contract.KindClientAgent)
	_ = e.expectFaultAs(e.actorFor(chatAgent), opDecide, wireDecideInput{
		Scope: e.scope, ID: fresh.ID, ExpectedVersion: fresh.Version,
		ActionDigest: fresh.ActionDigest, Decision: decideApprove, Reason: "the user said so in chat",
	}, contract.CodePermissionDenied)
}

// Z04.stale_dependencies (reviews slice) — a decision whose action digest
// changed does not renew; the new exact action requires a fresh decision.
func TestZ04StaleDependencies(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))

	base := actionFixture(e.scope, "https://api.example.com/v1/deploy")
	base.Preconditions = json.RawMessage(`{"repository_head":"9a8b7c6d","toolchain":"go1.26.2"}`)
	req := requirementFixture([]contract.ID{human}, true)
	review, fault := e.ensureFor(proposer, e.scope, ensureInputFor(t, e.scope, base, req))
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}
	if _, fault = e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "old plan",
	}); fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	// A pinned dependency changed (repository head moved): the plan
	// regenerates into a new exact action. Reusing the old decision is
	// impossible — the new digest has no approval.
	changed := base
	changed.Preconditions = json.RawMessage(`{"repository_head":"f00dcafe","toolchain":"go1.26.2"}`)
	newDigest, err := actionDigest(changed)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if newDigest == review.ActionDigest {
		t.Fatal("changed dependency did not change the digest")
	}
	out, fault := e.checkFor(e.scope, newDigest)
	if fault != nil {
		t.Fatalf("check regenerated plan: %v", fault)
	}
	if out.Eligible {
		t.Fatal("regenerated plan reused the old decision")
	}
	// The renewal path records a fresh review for the changed action.
	renewedIn := ensureInputFor(t, e.scope, changed, req)
	renewedIn.Requirement.ExpiresAt = e.clock.Now().Add(time.Hour)
	renewed, fault := e.ensureFor(proposer, e.scope, renewedIn)
	if fault != nil {
		t.Fatalf("ensure renewed plan: %v", fault)
	}
	if renewed.ID == review.ID {
		t.Fatal("renewed plan reused the original review")
	}
	if renewed.State != statePending || renewed.ActionDigest != newDigest {
		t.Fatalf("renewed review state/digest = %s/%s, want pending/%s", renewed.State, renewed.ActionDigest, newDigest)
	}
}

// JOURNEY slice (local to reviews) — ensure → check(false) → delegate →
// decide(approve) → check(true), with every state observable through the
// public reads.
func TestJourneyExactReviewLifecycle(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	delegate := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))

	// 1. An effect proposes an exact action: the review is recorded.
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/publish"),
		requirementFixture([]contract.ID{human}, true))
	review, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}

	// 2. Before any decision, check refuses dispatch.
	out, fault := e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check before decide: %v", fault)
	}
	if out.Eligible {
		t.Fatal("undecided review checked eligible")
	}

	// 3. The eligible human delegates inside their authority.
	updated, fault := e.delegateAs(e.actorFor(human), wireDelegateInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version, PrincipalID: delegate,
	})
	if fault != nil {
		t.Fatalf("delegate: %v", fault)
	}

	// 4. The delegatee approves the exact action.
	decision, fault := e.decideAs(e.actorFor(delegate), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: updated.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "delegated review",
	})
	if fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	// 5. Check now admits the exact digest and carries the evidence.
	out, fault = e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check after decide: %v", fault)
	}
	if !out.Eligible || out.Decision == nil || out.Decision.ID != decision.ID {
		t.Fatalf("check after decide = %+v, want eligible with the decision", out)
	}

	// The event log carries the full lifecycle.
	kinds := map[string]int{}
	for _, ev := range e.events() {
		kinds[ev.Kind]++
	}
	if kinds[eventReviewRequested] != 1 || kinds[eventReviewDelegated] != 1 || kinds[eventReviewDecided] != 1 {
		t.Fatalf("event lifecycle = %v", kinds)
	}
}
