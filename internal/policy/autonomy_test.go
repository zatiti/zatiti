package policy

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Behavioral tests for earned autonomy: proposals, deterministic evaluation,
// the self-approval fences (Z05.agent_self_grant, Z19.self_promotion),
// restriction/demotion and dependency invalidation.

// createRuleDef activates one promotion rule from a custom definition and
// returns the wire view pinned at version 1.
func (e *testEnv) createRuleDef(def ruleDefinitionInput) wirePromotionRule {
	e.t.Helper()
	id := e.ids.New()
	full := ruleResource(id, 1, def)
	e.activateChanges(wireChange{
		Kind: kindAutonomyRule, Action: changeCreate, ID: id, ExpectedVersion: 0, Definition: rawDef(full),
	})
	return full
}

// ruleRef builds the proposal pin for one rule version.
func ruleRef(rule wirePromotionRule) wireRef {
	return wireRef{ID: rule.ID, Version: rule.Version}
}

// invalidate runs _policy.invalidate and returns the affected ids.
func (e *testEnv) invalidate(refs []wireRef, reason string) []contract.ID {
	e.t.Helper()
	payload := e.mustOK(opInvalidate, invalidateInput{ChangedDependencies: refs, Reason: reason})
	var out qualificationIDsBody
	e.decode(payload.Data, &out)
	return out.QualificationIDs
}

// sameIDSet compares two id lists ignoring order.
func sameIDSet(t *testing.T, got, want []contract.ID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("id sets differ: got %v want %v", got, want)
	}
	seen := make(map[contract.ID]bool, len(want))
	for _, id := range want {
		seen[id] = true
	}
	for _, id := range got {
		if !seen[id] {
			t.Fatalf("unexpected id %s in %v", id, got)
		}
	}
}

func TestAutonomyProposeFences(t *testing.T) {
	e := newEnv(t)
	rule := e.createRule(e.scope, "deploy.render", e.ceiling)
	ref := ruleRef(rule)

	// A worker may propose only for itself.
	w1 := e.ids.New()
	w2 := e.ids.New()
	e.bindWorker(w1, "model-w")
	f := e.expectFaultAs(e.workerActor(w1), e.scope, opAutonomyPropose, autonomyProposeInput{
		Scope: e.scope, WorkerID: w2, Rule: ref, EvidenceIDs: []contract.ID{},
	}, contract.CodePermissionDenied)
	if !contains(f.Message, "a worker principal may propose a promotion only for itself") {
		t.Fatalf("self-proposal fence: %s", f.Message)
	}

	// A client agent is fenced the same way.
	f = e.expectFaultAs(e.clientAgentActor(w1), e.scope, opAutonomyPropose, autonomyProposeInput{
		Scope: e.scope, WorkerID: w2, Rule: ref, EvidenceIDs: []contract.ID{},
	}, contract.CodePermissionDenied)
	if !contains(f.Message, "a client_agent principal may propose a promotion only for itself") {
		t.Fatalf("client agent self-proposal fence: %s", f.Message)
	}

	// The subject worker must be bound in the current configuration.
	unbound := e.ids.New()
	f = e.expectFault(opAutonomyPropose, autonomyProposeInput{
		Scope: e.scope, WorkerID: unbound, Rule: ref, EvidenceIDs: []contract.ID{},
	}, contract.CodeNotFound)
	if !contains(f.Message, "is not bound in the current configuration") {
		t.Fatalf("worker binding fence: %s", f.Message)
	}

	// The pinned rule must exist.
	f = e.expectFault(opAutonomyPropose, autonomyProposeInput{
		Scope: e.scope, WorkerID: w1, Rule: wireRef{ID: e.ids.New(), Version: 1}, EvidenceIDs: []contract.ID{},
	}, contract.CodeNotFound)
	if !contains(f.Message, "is unknown in this installation") {
		t.Fatalf("unknown rule fence: %s", f.Message)
	}

	// A rule homed in another organization is outside the request scope.
	foreign := e.scope
	foreign.OrganizationID = e.ids.New()
	other := e.createRule(foreign, "deploy.render", e.ceiling)
	scoped := e.scope
	scoped.OrganizationID = e.org
	f = e.expectFault(opAutonomyPropose, autonomyProposeInput{
		Scope: scoped, WorkerID: w1, Rule: ruleRef(other), EvidenceIDs: []contract.ID{},
	}, contract.CodePermissionDenied)
	if !contains(f.Message, "is outside the request scope") {
		t.Fatalf("rule scope fence: %s", f.Message)
	}

	// An archived rule cannot receive proposals.
	archived := e.createRule(e.scope, "deploy.render", e.ceiling)
	e.archiveRule(archived.ID, 1)
	f = e.expectFault(opAutonomyPropose, autonomyProposeInput{
		Scope: e.scope, WorkerID: w1, Rule: ruleRef(archived), EvidenceIDs: []contract.ID{},
	}, contract.CodeConflict)
	if !contains(f.Message, "is archived and cannot receive proposals") {
		t.Fatalf("archived rule fence: %s", f.Message)
	}

	// The pin must match the rule's current version.
	f = e.expectFault(opAutonomyPropose, autonomyProposeInput{
		Scope: e.scope, WorkerID: w1, Rule: wireRef{ID: rule.ID, Version: 2}, EvidenceIDs: []contract.ID{},
	}, contract.CodeStaleVersion)
	if !contains(f.Message, "is at version 1, not the proposed 2") {
		t.Fatalf("stale pin fence: %s", f.Message)
	}

	// A worker proposing for itself is accepted and pinned to the exact
	// rule version and evidence window.
	e.setTask(e.ids.New(), "succeeded", w1, 1)
	t2 := e.ids.New()
	e.setTask(t2, "succeeded", w1, 1)
	q := e.propose(e.workerActor(w1), w1, ref, []contract.ID{t2, t2, t2})
	if q.State != stateProposed || q.Version != 1 {
		t.Fatalf("proposed qualification: %+v", q)
	}
	if q.WorkerID != w1 || q.Capability != "deploy.render" || q.Rule.ID != rule.ID || q.Rule.Version != 1 {
		t.Fatalf("qualification pin: %+v", q)
	}
	if len(q.EvidenceIDs) != 1 || q.EvidenceIDs[0] != t2 {
		t.Fatalf("evidence dedupe: %v", q.EvidenceIDs)
	}
	if !q.WindowEnd.After(e.clock.Now()) {
		t.Fatalf("window end %v is not in the future", q.WindowEnd)
	}
}

func TestAutonomyEvaluateQualified(t *testing.T) {
	e := newEnv(t)
	w := e.ids.New()
	skill := e.skillRef(2)
	e.bindWorker(w, "model-w", skill)
	rule := e.createRule(e.scope, "deploy.render", e.ceiling)
	t1 := e.ids.New()
	e.setTask(t1, "succeeded", w, 3)
	q := e.propose(e.actor, w, ruleRef(rule), []contract.ID{t1})

	done := e.evaluate(e.actor, q.ID, 1)
	if done.State != stateQualified || done.Version != 2 {
		t.Fatalf("qualified qualification: %+v", done)
	}
	if done.Model != "model-w" {
		t.Fatalf("model binding: %q", done.Model)
	}
	if len(done.SkillVersions) != 1 || done.SkillVersions[0].ID != skill.ID || done.SkillVersions[0].Version != 2 {
		t.Fatalf("skill binding: %+v", done.SkillVersions)
	}
	if done.Capability != "deploy.render" || done.Rule.ID != rule.ID || done.Rule.Version != 1 {
		t.Fatalf("grant shape: %+v", done)
	}
	if !contains(done.Explanation, "qualified under promotion rule") ||
		!contains(done.Explanation, string(e.ceiling)) {
		t.Fatalf("explanation: %s", done.Explanation)
	}

	calls := e.promoteCalls()
	if len(calls) != 1 {
		t.Fatalf("promote calls: %d", len(calls))
	}
	call := calls[0]
	if call.PrincipalID != w || call.CeilingGrantID != e.ceiling {
		t.Fatalf("promote call: %+v", call)
	}
	if call.Qualification.State != stateQualified || call.Qualification.Version != 2 {
		t.Fatalf("promoted qualification: %+v", call.Qualification)
	}
}

func TestAutonomyEvaluateRejections(t *testing.T) {
	e := newEnv(t)
	w := e.ids.New()
	other := e.ids.New()
	e.bindWorker(w, "model-w")

	// A missing task is unobserved evidence; an existing task in a
	// non-succeeded state is observed but not a success.
	rule := e.createRule(e.scope, "deploy.render", e.ceiling)
	missing := e.ids.New()
	pending := e.ids.New()
	e.setTask(pending, "running", w, 2)
	q := e.propose(e.actor, w, ruleRef(rule), []contract.ID{missing, pending})
	rejected := e.evaluate(e.actor, q.ID, 1)
	if rejected.State != stateRejected || rejected.Version != 2 {
		t.Fatalf("rejected qualification: %+v", rejected)
	}
	if !contains(rejected.Explanation, "insufficient evidence for rule") ||
		!contains(rejected.Explanation, "0 of 1 required successes observed") ||
		!contains(rejected.Explanation, "unobserved evidence: "+string(missing)) ||
		!contains(rejected.Explanation, "evidence of other workers: none") {
		t.Fatalf("insufficient explanation: %s", rejected.Explanation)
	}

	// A succeeded task owned by another worker is alien evidence.
	alien := e.ids.New()
	e.setTask(alien, "succeeded", other, 1)
	rule2 := e.createRule(e.scope, "deploy.render", e.ceiling)
	q = e.propose(e.actor, w, ruleRef(rule2), []contract.ID{alien})
	rejected = e.evaluate(e.actor, q.ID, 1)
	if !contains(rejected.Explanation, "evidence of other workers: "+string(alien)) {
		t.Fatalf("alien explanation: %s", rejected.Explanation)
	}

	// A disqualifying observed state rejects outright.
	disq := ruleDef(e.scope, "deploy.render", e.ceiling)
	disq.DisqualifyingEvents = []string{"task.failed"}
	rule3 := e.createRuleDef(disq)
	failed := e.ids.New()
	e.setTask(failed, "failed", w, 1)
	q = e.propose(e.actor, w, ruleRef(rule3), []contract.ID{failed})
	rejected = e.evaluate(e.actor, q.ID, 1)
	if rejected.State != stateRejected {
		t.Fatalf("disqualified state: %+v", rejected)
	}
	if !contains(rejected.Explanation, `matching disqualifying event "task.failed" of rule`) {
		t.Fatalf("disqualifier explanation: %s", rejected.Explanation)
	}
	links := e.mustFindEvidence(q.ID)
	if len(links) != 1 || links[0].Kind != evidenceKindTask || links[0].FirstState != "failed" {
		t.Fatalf("evidence links: %+v", links)
	}

	// The wildcard disqualifier matches any observed state.
	wild := ruleDef(e.scope, "deploy.render", e.ceiling)
	wild.DisqualifyingEvents = []string{"*"}
	rule4 := e.createRuleDef(wild)
	anyTask := e.ids.New()
	e.setTask(anyTask, "running", w, 1)
	q = e.propose(e.actor, w, ruleRef(rule4), []contract.ID{anyTask})
	rejected = e.evaluate(e.actor, q.ID, 1)
	if !contains(rejected.Explanation, `observed in state "running", matching disqualifying event "*"`) {
		t.Fatalf("wildcard explanation: %s", rejected.Explanation)
	}

	// An evidence kind this package cannot establish rejects without
	// consulting anything.
	unsupported := ruleDef(e.scope, "deploy.render", e.ceiling)
	unsupported.RequiredEvidence = []string{"model_summary"}
	rule5 := e.createRuleDef(unsupported)
	known := e.ids.New()
	e.setTask(known, "succeeded", w, 1)
	q = e.propose(e.actor, w, ruleRef(rule5), []contract.ID{known})
	rejected = e.evaluate(e.actor, q.ID, 1)
	if !contains(rejected.Explanation, `requires evidence kind "model_summary" which cannot be independently established`) {
		t.Fatalf("unsupported kind explanation: %s", rejected.Explanation)
	}

	// A evaluation after the window closed expires the qualification and
	// never consults task state.
	rule6 := e.createRule(e.scope, "deploy.render", e.ceiling)
	q = e.propose(e.actor, w, ruleRef(rule6), []contract.ID{})
	consults := e.ports.countOf("_tasks.snapshot")
	e.clock.Advance(3601 * time.Second)
	expired := e.evaluate(e.actor, q.ID, 1)
	if expired.State != stateExpired || expired.Version != 2 {
		t.Fatalf("expired qualification: %+v", expired)
	}
	if !contains(expired.Explanation, "the evidence window closed at") {
		t.Fatalf("expiry explanation: %s", expired.Explanation)
	}
	if n := e.ports.countOf("_tasks.snapshot"); n != consults {
		t.Fatalf("expired evaluation consulted tasks %d extra times", n-consults)
	}
}

func TestEvaluateSelfApproval(t *testing.T) {
	e := newEnv(t)
	w := e.ids.New()
	e.bindWorker(w, "model-w")
	rule := e.createRule(e.scope, "deploy.render", e.ceiling)
	t1 := e.ids.New()
	e.setTask(t1, "succeeded", w, 1)
	q := e.propose(e.workerActor(w), w, ruleRef(rule), []contract.ID{t1})

	// The subject worker cannot approve its own evidence.
	f := e.expectFaultAs(e.workerActor(w), e.scope, opAutonomyEvaluate, autonomyEvaluateInput{
		Scope: e.scope, ID: q.ID, ExpectedVersion: 1,
	}, contract.CodePermissionDenied)
	if !contains(f.Message, "the subject worker cannot evaluate its own qualification") {
		t.Fatalf("self-approval fence: %s", f.Message)
	}

	// The same qualification evaluates cleanly under another actor: the
	// fence keys on the acting principal, not the qualification.
	done := e.evaluate(e.actor, q.ID, 1)
	if done.State != stateQualified {
		t.Fatalf("independent evaluation: %+v", done)
	}
}

func TestAutonomyEvaluateFences(t *testing.T) {
	e := newEnv(t)
	w := e.ids.New()
	e.bindWorker(w, "model-w")
	rule := e.createRule(e.scope, "deploy.render", e.ceiling)
	t1 := e.ids.New()
	e.setTask(t1, "succeeded", w, 1)

	// An unknown qualification is not found.
	f := e.expectFault(opAutonomyEvaluate, autonomyEvaluateInput{
		Scope: e.scope, ID: e.ids.New(), ExpectedVersion: 1,
	}, contract.CodeNotFound)
	if !contains(f.Message, "is unknown in this installation") {
		t.Fatalf("unknown qualification: %s", f.Message)
	}

	// A stale expected version refuses evaluation.
	q := e.propose(e.actor, w, ruleRef(rule), []contract.ID{t1})
	f = e.expectFault(opAutonomyEvaluate, autonomyEvaluateInput{
		Scope: e.scope, ID: q.ID, ExpectedVersion: 2,
	}, contract.CodeStaleVersion)
	if !contains(f.Message, "is at version 1, not the expected 2") {
		t.Fatalf("stale version: %s", f.Message)
	}

	// A superseded rule refuses evaluation: the proposal pins an exact
	// version.
	def := ruleDef(e.scope, "deploy.render", e.ceiling)
	e.updateRule(rule.ID, 1, def)
	f = e.expectFault(opAutonomyEvaluate, autonomyEvaluateInput{
		Scope: e.scope, ID: q.ID, ExpectedVersion: 1,
	}, contract.CodeStaleVersion)
	if !contains(f.Message, "moved to version 2; the proposal pins version 1") {
		t.Fatalf("superseded rule: %s", f.Message)
	}

	// An archived rule refuses evaluation.
	rule2 := e.createRule(e.scope, "deploy.render", e.ceiling)
	q2 := e.propose(e.actor, w, ruleRef(rule2), []contract.ID{t1})
	e.archiveRule(rule2.ID, 1)
	f = e.expectFault(opAutonomyEvaluate, autonomyEvaluateInput{
		Scope: e.scope, ID: q2.ID, ExpectedVersion: 1,
	}, contract.CodeConflict)
	if !contains(f.Message, "promotion rule "+string(rule2.ID)+" is archived") {
		t.Fatalf("archived rule: %s", f.Message)
	}

	// A qualification cannot be evaluated twice.
	rule3 := e.createRule(e.scope, "deploy.render", e.ceiling)
	q3 := e.propose(e.actor, w, ruleRef(rule3), []contract.ID{t1})
	done := e.evaluate(e.actor, q3.ID, 1)
	f = e.expectFault(opAutonomyEvaluate, autonomyEvaluateInput{
		Scope: e.scope, ID: q3.ID, ExpectedVersion: done.Version,
	}, contract.CodeConflict)
	if !contains(f.Message, "is qualified and cannot be evaluated again") {
		t.Fatalf("re-evaluation: %s", f.Message)
	}

	// A named identity refusal records a rejection instead of failing.
	promoteBefore := len(e.promoteCalls())
	e.ports.setPromoteFault(&contract.Fault{Code: contract.CodePermissionDenied, Message: "grant exceeds ceiling"})
	q4 := e.propose(e.actor, w, ruleRef(rule3), []contract.ID{t1})
	rejected := e.evaluate(e.actor, q4.ID, 1)
	if rejected.State != stateRejected || rejected.Version != 2 {
		t.Fatalf("refused qualification: %+v", rejected)
	}
	if rejected.Explanation != "identity refused promotion: grant exceeds ceiling" {
		t.Fatalf("refusal explanation: %s", rejected.Explanation)
	}
	if n := len(e.promoteCalls()); n != promoteBefore+1 {
		t.Fatalf("promote calls after refusal: %d", n)
	}
	e.ports.setPromoteFault(nil)

	// An infrastructure fault propagates and the row stays proposed.
	e.ports.setPromoteFault(&contract.Fault{Code: contract.CodeInternalError, Message: "storage down"})
	q5 := e.propose(e.actor, w, ruleRef(rule3), []contract.ID{t1})
	f = e.expectFault(opAutonomyEvaluate, autonomyEvaluateInput{
		Scope: e.scope, ID: q5.ID, ExpectedVersion: 1,
	}, contract.CodeInternalError)
	if !contains(f.Message, "storage down") {
		t.Fatalf("propagated fault: %s", f.Message)
	}
	row := e.mustFindQualification(q5.ID)
	if row.State != stateProposed || row.Version != 1 {
		t.Fatalf("rolled-back row: %+v", row)
	}
	e.ports.setPromoteFault(nil)
}

func TestRestrictAndDemote(t *testing.T) {
	e := newEnv(t)
	w := e.ids.New()
	e.bindWorker(w, "model-w")
	rule := e.createRule(e.scope, "deploy.render", e.ceiling)
	t1 := e.ids.New()
	e.setTask(t1, "succeeded", w, 1)

	// Restricting a proposed qualification commits the restriction and
	// retains the cited evidence as incident links.
	q := e.propose(e.actor, w, ruleRef(rule), []contract.ID{t1})
	restricted := e.restrict(e.actor, q.ID, 1, "incident reason", []contract.ID{t1})
	if restricted.State != stateRestricted || restricted.Version != 2 || restricted.Explanation != "incident reason" {
		t.Fatalf("restricted qualification: %+v", restricted)
	}
	calls := e.restrictCalls()
	if len(calls) != 1 || calls[0].PrincipalID != w || calls[0].Capability != "deploy.render" ||
		calls[0].Reason != "incident reason" {
		t.Fatalf("restrict calls: %+v", calls)
	}
	links := e.mustFindEvidence(q.ID)
	if len(links) != 1 || links[0].Kind != "incident" || links[0].FirstState != "incident" {
		t.Fatalf("incident links: %+v", links)
	}

	// An already restricted qualification cannot be restricted again.
	f := e.expectFault(opAutonomyRestrict, autonomyRestrictInput{
		Scope: e.scope, ID: q.ID, ExpectedVersion: 2, Reason: "again", EvidenceIDs: []contract.ID{},
	}, contract.CodeConflict)
	if !contains(f.Message, "is already restricted") {
		t.Fatalf("re-restrict: %s", f.Message)
	}

	// A demotion requires an active earned grant.
	q2 := e.propose(e.actor, w, ruleRef(rule), []contract.ID{t1})
	f = e.expectFault(opAutonomyDemote, autonomyRestrictInput{
		Scope: e.scope, ID: q2.ID, ExpectedVersion: 1, Reason: "demote", EvidenceIDs: []contract.ID{},
	}, contract.CodeConflict)
	if !contains(f.Message, "is proposed; only an active earned grant can be demoted") {
		t.Fatalf("demote proposed: %s", f.Message)
	}

	// Demoting a qualified qualification restricts it.
	q3 := e.propose(e.actor, w, ruleRef(rule), []contract.ID{t1})
	qualified := e.evaluate(e.actor, q3.ID, 1)
	demoted := e.demote(q3.ID, qualified.Version, "demote reason", []contract.ID{})
	if demoted.State != stateRestricted || demoted.Version != 3 || demoted.Explanation != "demote reason" {
		t.Fatalf("demoted qualification: %+v", demoted)
	}
	if n := len(e.restrictCalls()); n != 2 {
		t.Fatalf("restrict calls after demote: %d", n)
	}

	// A rejected qualification carries no open grant to restrict.
	q4 := e.propose(e.actor, w, ruleRef(rule), []contract.ID{})
	rejected := e.evaluate(e.actor, q4.ID, 1)
	if rejected.State != stateRejected {
		t.Fatalf("expected rejection: %+v", rejected)
	}
	f = e.expectFault(opAutonomyRestrict, autonomyRestrictInput{
		Scope: e.scope, ID: q4.ID, ExpectedVersion: 2, Reason: "restrict rejected", EvidenceIDs: []contract.ID{},
	}, contract.CodeConflict)
	if !contains(f.Message, "is rejected and carries no open grant to restrict") {
		t.Fatalf("restrict rejected: %s", f.Message)
	}

	// A stale expected version refuses restriction.
	q5 := e.propose(e.actor, w, ruleRef(rule), []contract.ID{})
	f = e.expectFault(opAutonomyRestrict, autonomyRestrictInput{
		Scope: e.scope, ID: q5.ID, ExpectedVersion: 5, Reason: "stale", EvidenceIDs: []contract.ID{},
	}, contract.CodeStaleVersion)
	if !contains(f.Message, "is at version 1, not the expected 5") {
		t.Fatalf("stale restrict: %s", f.Message)
	}
}

func TestInvalidateDependencyMatching(t *testing.T) {
	e := newEnv(t)
	w := e.ids.New()
	skill := e.skillRef(2)
	e.bindWorker(w, "model-w", skill)
	rule := e.createRule(e.scope, "deploy.render", e.ceiling)
	t1 := e.ids.New()
	e.setTask(t1, "succeeded", w, 1)

	// One qualified qualification with a bound skill and one proposed
	// qualification citing t1.
	q1 := e.propose(e.actor, w, ruleRef(rule), []contract.ID{t1})
	e.evaluate(e.actor, q1.ID, 1)
	q2 := e.propose(e.actor, w, ruleRef(rule), []contract.ID{t1})

	// A moved skill version restricts only the qualification bound to it.
	affected := e.invalidate([]wireRef{{ID: skill.ID, Version: 3}}, "skill rotated")
	sameIDSet(t, affected, []contract.ID{q1.ID})
	if row := e.mustFindQualification(q1.ID); row.State != stateRestricted || row.Version != 3 {
		t.Fatalf("q1 after invalidation: %+v", row)
	}
	if row := e.mustFindQualification(q2.ID); row.State != stateProposed {
		t.Fatalf("q2 after invalidation: %+v", row)
	}
	if n := len(e.restrictCalls()); n != 1 {
		t.Fatalf("restrict calls after skill invalidation: %d", n)
	}

	// A cited evidence id restricts the proposal citing it, at any
	// version.
	affected = e.invalidate([]wireRef{{ID: t1, Version: 9}}, "evidence compromised")
	sameIDSet(t, affected, []contract.ID{q2.ID})
	if row := e.mustFindQualification(q2.ID); row.State != stateRestricted {
		t.Fatalf("q2 after evidence invalidation: %+v", row)
	}

	// A reference at the current rule version and an unrelated id hit
	// nothing.
	affected = e.invalidate([]wireRef{
		{ID: rule.ID, Version: 1},
		{ID: e.ids.New(), Version: 1},
	}, "no dependency moved")
	if len(affected) != 0 {
		t.Fatalf("unexpected invalidations: %v", affected)
	}
	if n := len(e.restrictCalls()); n != 2 {
		t.Fatalf("restrict calls after no-op invalidation: %d", n)
	}
}
