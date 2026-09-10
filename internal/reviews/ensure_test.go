package reviews

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// _reviews.ensure: creation, idempotence, requirement retention, expiry
// transitions, rejection veto and input gates.

func TestEnsureCreatesPendingReview(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/deploy"),
		requirementFixture([]contract.ID{human}, true))

	review, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}
	if review.State != statePending || review.Version != 1 {
		t.Fatalf("created review state/version = %s/%d, want pending/1", review.State, review.Version)
	}
	digest, err := actionDigest(in.Action)
	if err != nil {
		t.Fatalf("actionDigest: %v", err)
	}
	if review.ActionDigest != digest {
		t.Fatalf("review digest %s, want %s", review.ActionDigest, digest)
	}
	if review.ProposerID != proposer.PrincipalID {
		t.Fatalf("proposer %s, want %s", review.ProposerID, proposer.PrincipalID)
	}
	if review.DecisionID != nil {
		t.Fatalf("fresh review carries decision %s", *review.DecisionID)
	}
	// The preview is the exact action, round-tripped.
	if !actionsEqual(review.Preview, in.Action) {
		t.Fatalf("preview drift:\n got %+v\nwant %+v", review.Preview, in.Action)
	}
	// The requirement is retained exactly as submitted.
	if !review.Requirement.HumanRequired || len(review.Requirement.EligiblePrincipals) != 1 ||
		review.Requirement.EligiblePrincipals[0] != human {
		t.Fatalf("requirement not retained: %+v", review.Requirement)
	}
	// One request event with the review as its resource.
	evs := e.eventsOfKind(eventReviewRequested)
	if len(evs) != 1 {
		t.Fatalf("requested events = %d, want 1", len(evs))
	}
	if evs[0].ResourceID != review.ID || evs[0].ResourceVersion != 1 {
		t.Fatalf("event resource %s/%d, want %s/1", evs[0].ResourceID, evs[0].ResourceVersion, review.ID)
	}
	if evs[0].Scope.InstallationID != e.install {
		t.Fatalf("event scope installation %s, want %s", evs[0].Scope.InstallationID, e.install)
	}
}

// actionsEqual reports whether two wire actions are identical after
// canonicalization, so test comparisons share the digest's notion of
// equality.
func actionsEqual(a, b wireAction) bool {
	ra, err := canonicalActionJSON(a)
	if err != nil {
		return false
	}
	rb, err := canonicalActionJSON(b)
	if err != nil {
		return false
	}
	return ra == rb
}

func TestEnsureDigestIsCanonical(t *testing.T) {
	scope := contract.Scope{InstallationID: contract.ID("00000000-0000-4000-8000-000000000001")}
	a := actionFixture(scope, "https://api.example.com/v1/deploy")
	b := a
	// Same semantics, different spelling: reordered keys and padded
	// whitespace inside the inert parameters object.
	a.Parameters = json.RawMessage(`{"b":1,"a":[1,2]}`)
	b.Parameters = json.RawMessage(`{"a":[1,2],  "b": 1}`)
	da, err := actionDigest(a)
	if err != nil {
		t.Fatalf("digest a: %v", err)
	}
	db, err := actionDigest(b)
	if err != nil {
		t.Fatalf("digest b: %v", err)
	}
	if da != db {
		t.Fatalf("canonical digest drift: %s vs %s", da, db)
	}
	// A material change moves the digest.
	c := a
	c.Destination = "https://api.example.com/v2/deploy"
	dc, err := actionDigest(c)
	if err != nil {
		t.Fatalf("digest c: %v", err)
	}
	if da == dc {
		t.Fatal("changed destination produced the same digest")
	}
}

func TestEnsureRejectsMismatchedRequirementDigest(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/deploy"),
		requirementFixture([]contract.ID{human}, true))
	in.Requirement.ActionDigest = strings.Repeat("0", 64)

	if _, fault := e.ensureFor(proposer, e.scope, in); fault == nil || fault.Code != contract.CodeInvalidInput {
		t.Fatalf("mismatched requirement digest fault = %v, want invalid_input", fault)
	}
}

func TestEnsureRejectsActionScopeMismatch(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	action := actionFixture(e.scope, "https://api.example.com/v1/deploy")
	action.Scope = contract.Scope{InstallationID: e.install, OrganizationID: e.ids.New()}
	in := ensureInputFor(t, e.scope, action, requirementFixture([]contract.ID{human}, true))

	if _, fault := e.ensureFor(proposer, e.scope, in); fault == nil || fault.Code != contract.CodeInvalidInput {
		t.Fatalf("action scope mismatch fault = %v, want invalid_input", fault)
	}
}

func TestEnsureRequiresAuthentication(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/deploy"),
		requirementFixture([]contract.ID{human}, true))

	// An invocation without a principal is refused before any review work:
	// the execution session rejects it, and reviews' own permission-denied
	// admission gate remains as defense in depth behind that.
	f := e.expectFaultAs(contract.Actor{}, opEnsure, in, contract.CodeInvalidInput)
	if !strings.Contains(f.Message, "principal is required") {
		t.Fatalf("fault message %q does not name the missing principal", f.Message)
	}
}

func TestEnsureRefusesForeignScope(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	foreign := contract.Scope{InstallationID: e.ids.New()}
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/deploy"),
		requirementFixture([]contract.ID{human}, true))

	if _, fault := e.ensureFor(proposer, foreign, in); fault == nil || fault.Code != contract.CodeInvalidInput {
		t.Fatalf("foreign scope fault = %v, want invalid_input", fault)
	}
}

func TestEnsureRefusesExpiredRequirement(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	req := requirementFixture([]contract.ID{human}, true)
	req.ExpiresAt = e.clock.Now().Add(-time.Minute)
	in := ensureInputFor(t, e.scope, actionFixture(e.scope, "https://api.example.com/v1/deploy"), req)

	if _, fault := e.ensureFor(proposer, e.scope, in); fault == nil || fault.Code != contract.CodeInvalidInput {
		t.Fatalf("expired requirement fault = %v, want invalid_input", fault)
	}
}

func TestEnsureIsIdempotentForLivePendingReview(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/deploy"),
		requirementFixture([]contract.ID{human}, true))

	first, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("first ensure: %v", fault)
	}
	second, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("second ensure: %v", fault)
	}
	if second.ID != first.ID || second.Version != first.Version {
		t.Fatalf("re-ensure created %s/%d, want same %s/%d", second.ID, second.Version, first.ID, first.Version)
	}
	if got := len(e.eventsOfKind(eventReviewRequested)); got != 1 {
		t.Fatalf("requested events = %d, want 1 (re-ensure must not re-emit)", got)
	}
}

func TestEnsureRetainsOriginalRequirement(t *testing.T) {
	e := newEnv(t)
	h1 := e.principal(contract.KindHuman)
	h2 := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/deploy"),
		requirementFixture([]contract.ID{h1}, true))

	first, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("first ensure: %v", fault)
	}

	// A second ensure with a lighter requirement must not rewrite the
	// stored one.
	lighter := in.Requirement
	lighter.HumanRequired = false
	lighter.EligiblePrincipals = []contract.ID{h2}
	lighterIn := in
	lighterIn.Requirement = lighter
	second, fault := e.ensureFor(proposer, e.scope, lighterIn)
	if fault != nil {
		t.Fatalf("second ensure: %v", fault)
	}
	if second.ID != first.ID {
		t.Fatalf("re-ensure created %s, want existing %s", second.ID, first.ID)
	}
	if !second.Requirement.HumanRequired || second.Requirement.EligiblePrincipals[0] != h1 {
		t.Fatalf("requirement was rewritten: %+v", second.Requirement)
	}
}

func TestEnsureExpiresStalePendingAndCreatesNew(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	req := requirementFixture([]contract.ID{human}, true)
	req.ExpiresAt = e.clock.Now().Add(time.Hour)
	in := ensureInputFor(t, e.scope, actionFixture(e.scope, "https://api.example.com/v1/deploy"), req)

	stale, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("first ensure: %v", fault)
	}
	e.clock.advance(2 * time.Hour)

	// Policy re-issues the requirement for the same exact action with a
	// fresh window; the stale pending review transitions to expired and a
	// new pending review is recorded under the new requirement.
	req2 := requirementFixture([]contract.ID{human}, true)
	req2.ExpiresAt = e.clock.Now().Add(time.Hour)
	in2 := ensureInputFor(t, e.scope, in.Action, req2)
	fresh, fault := e.ensureFor(proposer, e.scope, in2)
	if fault != nil {
		t.Fatalf("second ensure: %v", fault)
	}
	if fresh.ID == stale.ID {
		t.Fatal("ensure after expiry returned the stale review")
	}
	if fresh.State != statePending || fresh.Version != 1 {
		t.Fatalf("fresh review state/version = %s/%d, want pending/1", fresh.State, fresh.Version)
	}
	if !fresh.Requirement.ExpiresAt.Equal(req2.ExpiresAt) {
		t.Fatalf("fresh requirement window %+v, want the re-issued %s", fresh.Requirement.ExpiresAt, req2.ExpiresAt)
	}

	got, fault := e.getReview(stale.ID)
	if fault != nil {
		t.Fatalf("get stale review: %v", fault)
	}
	if got.State != stateExpired || got.Version != 2 {
		t.Fatalf("stale review state/version = %s/%d, want expired/2", got.State, got.Version)
	}
	if got.DecisionID != nil {
		t.Fatalf("expired review carries decision %s", *got.DecisionID)
	}
	if got := len(e.eventsOfKind(eventReviewExpired)); got != 1 {
		t.Fatalf("expired events = %d, want 1", got)
	}
	if got := len(e.eventsOfKind(eventReviewRequested)); got != 2 {
		t.Fatalf("requested events = %d, want 2", got)
	}
}

func TestEnsureReturnsStandingApproval(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/deploy"),
		requirementFixture([]contract.ID{human}, true))

	review, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}
	if _, fault = e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "checked the preview",
	}); fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	again, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure after approval: %v", fault)
	}
	if again.ID != review.ID || again.State != stateApproved {
		t.Fatalf("ensure after approval returned %s state %s, want same approved review", again.ID, again.State)
	}
}

func TestEnsureAfterApprovalExpiryCreatesNewReview(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/deploy"),
		requirementFixture([]contract.ID{human}, true))

	review, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}
	if _, fault = e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideApprove, Reason: "ok",
	}); fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	// Past the requirement's expiry the approval no longer stands; a new
	// ensure with a re-issued requirement admits a fresh pending request
	// while history keeps the old.
	e.clock.advance(4 * time.Hour)
	req2 := in.Requirement
	req2.ExpiresAt = e.clock.Now().Add(time.Hour)
	in2 := in
	in2.Requirement = req2
	fresh, fault := e.ensureFor(proposer, e.scope, in2)
	if fault != nil {
		t.Fatalf("ensure after approval expiry: %v", fault)
	}
	if fresh.ID == review.ID || fresh.State != statePending {
		t.Fatalf("ensure after expiry returned %s state %s, want a new pending review", fresh.ID, fresh.State)
	}
	still, fault := e.getReview(review.ID)
	if fault != nil {
		t.Fatalf("get old review: %v", fault)
	}
	if still.State != stateApproved {
		t.Fatalf("old approval state = %s, want approved (history preserved)", still.State)
	}
	// The old digest-bound approval no longer checks eligible.
	out, fault := e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check: %v", fault)
	}
	if out.Eligible {
		t.Fatal("check after expiry returned eligible")
	}
}

func TestEnsureAfterRejectionIsPermanentVeto(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/deploy"),
		requirementFixture([]contract.ID{human}, true))

	review, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure: %v", fault)
	}
	decision, fault := e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: review.ID, ExpectedVersion: review.Version,
		ActionDigest: review.ActionDigest, Decision: decideReject, Reason: "destination is outside policy",
	})
	if fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	// Re-ensure returns the rejected review; the exact digest is vetoed
	// permanently even though the requirement's window is still open.
	e.clock.advance(time.Hour)
	again, fault := e.ensureFor(proposer, e.scope, in)
	if fault != nil {
		t.Fatalf("ensure after rejection: %v", fault)
	}
	if again.ID != review.ID || again.State != stateRejected {
		t.Fatalf("ensure after rejection returned %s state %s, want same rejected review", again.ID, again.State)
	}

	out, fault := e.checkFor(e.scope, review.ActionDigest)
	if fault != nil {
		t.Fatalf("check: %v", fault)
	}
	if out.Eligible {
		t.Fatal("check after rejection returned eligible")
	}
	if out.Decision == nil || out.Decision.ID != decision.ID || out.Decision.Decision != decideReject {
		t.Fatalf("check decision = %+v, want the rejection", out.Decision)
	}
}
