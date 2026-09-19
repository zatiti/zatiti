package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// The exact review flow, walked through the real modules. Adopted from the
// edge-seams lane's probe, which found two defects the package fakes had
// hidden (a strict one-field Decision decode in configuration, and the
// pending review rolling back with the refused mutation).

// reviewRow is the public projection of a review request.
type reviewRow struct {
	ID           contract.ID `json:"id"`
	Version      int64       `json:"version"`
	ActionDigest string      `json:"action_digest"`
	State        string      `json:"state"`
	ProposerID   contract.ID `json:"proposer_id"`
	Requirement  struct {
		EligiblePrincipals []contract.ID `json:"eligible_principals"`
		HumanRequired      bool          `json:"human_required"`
		SeparateProposer   bool          `json:"separate_proposer"`
	} `json:"requirement"`
}

// findReview returns the review bound to an exact action digest.
func (f *fixture) findReview(digest string) reviewRow {
	f.t.Helper()
	res := f.must(f.owner, "review.list", "", map[string]any{"scope": f.scope()})
	var out struct {
		Items []reviewRow `json:"items"`
	}
	decode(f.t, res.Data, &out)
	for _, r := range out.Items {
		if r.ActionDigest == digest {
			return r
		}
	}
	f.t.Fatalf("no review bound to digest %s among %d reviews: %s", digest, len(out.Items), res.Data)
	return reviewRow{}
}

// approve decides a review as the owner.
func (f *fixture) approve(r reviewRow, key string) {
	f.t.Helper()
	f.must(f.owner, "review.decide", key, map[string]any{
		"scope": f.scope(), "id": r.ID, "expected_version": r.Version, "action_digest": r.ActionDigest,
		"decision": "approve", "reason": "integration fixture: the owner approves its own exact request",
	})
}

// requiredDigest reads the action digest a review_required fault names.
func requiredDigest(t testing.TB, err error) string {
	t.Helper()
	fault := errAs(err)
	if fault == nil || fault.Code != contract.CodeReviewRequired {
		t.Fatalf("%v, want review_required", err)
	}
	var details struct {
		Requirements []struct {
			ActionDigest string `json:"action_digest"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(fault.Details, &details); err != nil || len(details.Requirements) != 1 {
		t.Fatalf("review_required details %s do not name exactly one requirement: %v", fault.Details, err)
	}
	return details.Requirements[0].ActionDigest
}

// activate stages one typed operation (input carries its top-level fields
// other than scope), seals the plan, applies it once to open the exact
// review, decides that review as the owner and applies again under a new
// key: the journey-1 "activate a plan" path.
func (f *fixture) activate(label, op string, input map[string]any) contract.Result {
	f.t.Helper()
	draft := f.stage(op, label+"-stage", input)
	plan := f.plan(label+"-plan", draft)
	_, err := f.apply(label+"-apply-1", plan)
	if digest := requiredDigest(f.t, err); digest != plan.CandidateDigest {
		f.t.Fatalf("%s: refusal names digest %s, the plan's candidate digest is %s", label, digest, plan.CandidateDigest)
	}
	f.approve(f.findReview(plan.CandidateDigest), label+"-decide")
	res, err := f.apply(label+"-apply-2", plan)
	if err != nil {
		f.t.Fatalf("%s: apply after the approved review: %v", label, err)
	}
	return res
}

// TestOwnerActivatesConfigurationThroughReview (Z04 exact atomic activation,
// Z07 exact eligible review; journey 1 "activate a plan"): the owner stages
// a team and seals the plan; apply is refused review_required naming the
// candidate digest; the pending review is bound to that digest, proposed
// by the owner, human-required, without a separate-proposer demand, and
// the owner is eligible; after the owner's approval a new apply completes
// with the revision carrying the digest, the team is effective, and the
// exact replay returns the original command.
func TestOwnerActivatesConfigurationThroughReview(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	org, _ := f.rootOrganization()
	draft := f.stage("team.create", "activate-stage", map[string]any{"definition": map[string]any{
		"organization_id": org, "key": "engineering", "name": "Engineering", "worker_ids": []string{}}})
	plan := f.plan("activate-plan", draft)
	if n := f.count("team.list", map[string]any{"scope": f.scope()}); n != 0 {
		t.Fatalf("a staged, unapplied team is already effective (%d teams)", n)
	}

	_, err := f.apply("activate-apply-1", plan)
	if digest := requiredDigest(t, err); digest != plan.CandidateDigest {
		t.Fatalf("refusal names digest %s, the plan's candidate digest is %s", digest, plan.CandidateDigest)
	}
	review := f.findReview(plan.CandidateDigest)
	if review.State != "pending" || review.ProposerID != f.owner.PrincipalID {
		t.Fatalf("review %+v, want pending and proposed by the owner %s", review, f.owner.PrincipalID)
	}
	if review.Requirement.SeparateProposer || !review.Requirement.HumanRequired {
		t.Fatalf("requirement %+v, want human_required without a separate proposer", review.Requirement)
	}
	eligible := false
	for _, id := range review.Requirement.EligiblePrincipals {
		eligible = eligible || id == f.owner.PrincipalID
	}
	if !eligible {
		t.Fatalf("owner %s is not among the eligible principals %v", f.owner.PrincipalID, review.Requirement.EligiblePrincipals)
	}
	if n := f.count("team.list", map[string]any{"scope": f.scope()}); n != 0 {
		t.Fatalf("the refused apply activated %d teams", n)
	}

	f.approve(review, "activate-decide")
	applied := f.must(f.owner, "configuration.apply", "activate-apply-2", map[string]any{
		"scope": f.scope(), "plan_id": plan.ID, "base_revision": plan.BaseRevision, "candidate_digest": plan.CandidateDigest,
	})
	if !strings.Contains(string(applied.Data), plan.CandidateDigest) {
		t.Fatalf("applied revision does not carry the candidate digest: %s", applied.Data)
	}
	if n := f.count("team.list", map[string]any{"scope": f.scope()}); n != 1 {
		t.Fatalf("applied plan left %d effective teams, want 1", n)
	}
	replay, err := f.apply("activate-apply-2", plan)
	if err != nil || replay.CommandID != applied.CommandID {
		t.Fatalf("exact replay returned command %s (err %v), original %s", replay.CommandID, err, applied.CommandID)
	}
	// The head moved: the same plan under a new key is stale.
	if _, err := f.apply("activate-apply-3", plan); faultCode(err) != contract.CodeStaleVersion && faultCode(err) != contract.CodeConflict {
		t.Fatalf("re-applying an applied plan under a new key: %v, want stale_version or conflict", err)
	}
}

// TestOwnerCreatesGrantThroughReview (Z05 agent self-grant, Z07 agent posing
// as human): grant.create is refused review_required naming an action
// digest; the owner decides that exact review and the retried grant.create
// creates the grant; the client agent can never grant itself anything.
func TestOwnerCreatesGrantThroughReview(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	agent := f.newAgent("granted", f.scope())
	grant := map[string]any{
		"scope": f.scope(), "definition": map[string]any{
			"principal_id": agent.id, "scope": f.scope(), "capabilities": []string{"task.get", "task.list"},
			"destinations": []string{}, "denied": false},
	}
	_, err := f.invoke(f.owner, "grant.create", "grant-1", grant)
	digest := requiredDigest(t, err)
	f.approve(f.findReview(digest), "grant-decide")
	created := f.must(f.owner, "grant.create", "grant-2", grant)
	if !strings.Contains(string(created.Data), string(agent.id)) {
		t.Fatalf("grant was not created for the agent: %s", created.Data)
	}
	if _, err := f.invoke(agent.actor, "grant.create", "grant-3", grant); faultCode(err) == "" {
		t.Fatal("a client agent granted itself capabilities")
	}
	// The grant is effective: the agent reads what it was granted and
	// nothing else.
	if _, err := f.invoke(agent.actor, "task.list", "", map[string]any{"scope": f.scope()}); err != nil {
		t.Fatalf("granted agent task.list: %v", err)
	}
	res, err := f.invoke(agent.actor, "event.list", "", map[string]any{"scope": f.scope()})
	refusedWithoutData(t, "agent granted task reads", "event.list", res, err)
}
