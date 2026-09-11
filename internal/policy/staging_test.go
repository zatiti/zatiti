package policy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Behavioral tests for public definition staging (policy.create/update/
// archive, autonomy.rule.create), the internal validate diagnostics and the
// activate boundary fences: governance (Z05.agent_self_grant), the ceiling
// grant fence (Z04.old_authority) and optimistic version fencing.

func TestPolicyStageGovernance(t *testing.T) {
	e := newEnv(t)

	// A worker principal may not stage standing policy at all.
	worker := e.ids.New()
	e.bindWorker(worker, "model-x")
	f := e.expectFaultAs(e.workerActor(worker), e.scope, opPolicyCreate,
		policyCreateInput{Scope: e.scope, Definition: policyDef(e.scope)}, contract.CodePermissionDenied)
	if !contains(f.Message, "administrative act") {
		t.Fatalf("governance fence: %s", f.Message)
	}

	// A client agent may not stage promotion rules either.
	f = e.expectFaultAs(e.clientAgentActor(worker), e.scope, opRuleCreate,
		ruleCreateInput{Scope: e.scope, Definition: ruleDef(e.scope, "deploy.render", e.ceiling)},
		contract.CodePermissionDenied)
	if !contains(f.Message, "administrative act") {
		t.Fatalf("client agent governance fence: %s", f.Message)
	}

	// A service actor stages a valid policy; the sealed change carries
	// kind, action, expected version 0 and the assigned definition.
	in := policyCreateInput{Scope: e.scope, Definition: policyDef(e.scope,
		standingRule("deploy.render", decisionAllow, false, nil))}
	staged := e.stagePolicy(e.actor, in)
	if staged.Resource.Version != 1 || staged.Resource.Scope.InstallationID != e.install {
		t.Fatalf("staged resource: %+v", staged.Resource)
	}
	if len(staged.Resource.Rules) != 1 || staged.Resource.Rules[0].Capability != "deploy.render" {
		t.Fatalf("staged rules: %+v", staged.Resource.Rules)
	}
	if staged.Draft.ID == "" || staged.Draft.Version != 1 {
		t.Fatalf("staged draft: %+v", staged.Draft)
	}
	calls := e.ports.callsOf("_configuration.stage")
	if len(calls) != 1 {
		t.Fatalf("stage calls: %d", len(calls))
	}
	var call stageCallInput
	e.decode(calls[0].Input, &call)
	if call.Change.Kind != kindPolicy || call.Change.Action != changeCreate || call.Change.ExpectedVersion != 0 {
		t.Fatalf("staged change: %+v", call.Change)
	}

	// A definition homed in another installation is refused.
	foreign := e.scope
	foreign.InstallationID = e.ids.New()
	f = e.expectFault(opPolicyCreate, policyCreateInput{
		Scope: e.scope, Definition: policyDef(foreign),
	}, contract.CodeInvalidInput)
	if !contains(f.Message, "does not match the transaction scope") {
		t.Fatalf("installation fence: %s", f.Message)
	}

	// A definition with an unsupported condition is rejected at staging.
	f = e.expectFault(opPolicyCreate, policyCreateInput{
		Scope: e.scope, Definition: policyDef(e.scope, standingRule(
			"deploy.render", decisionAllow, false, json0(`{"astral_projection":true}`))),
	}, contract.CodeInvalidInput)
	if !contains(f.Message, "policy definition rejected") {
		t.Fatalf("condition fence: %s", f.Message)
	}

	// Updates fence on the stored row: unknown, stale and archived.
	unknown := e.ids.New()
	f = e.expectFault(opPolicyUpdate, policyUpdateInput{
		Scope: e.scope, ID: unknown, ExpectedVersion: 1, Definition: policyDef(e.scope),
	}, contract.CodeNotFound)
	if !contains(f.Message, "is unknown in this installation") {
		t.Fatalf("unknown update: %s", f.Message)
	}

	row := e.createPolicy(e.scope)
	f = e.expectFault(opPolicyUpdate, policyUpdateInput{
		Scope: e.scope, ID: row.ID, ExpectedVersion: 5, Definition: policyDef(e.scope),
	}, contract.CodeStaleVersion)
	if !contains(f.Message, "is at version 1, not the expected 5") {
		t.Fatalf("stale update: %s", f.Message)
	}

	e.archivePolicy(row.ID, 1)
	f = e.expectFault(opPolicyUpdate, policyUpdateInput{
		Scope: e.scope, ID: row.ID, ExpectedVersion: 2, Definition: policyDef(e.scope),
	}, contract.CodeConflict)
	if !contains(f.Message, "is archived and cannot be updated") {
		t.Fatalf("archived update: %s", f.Message)
	}

	// policy.update also stages a sealed change when everything holds.
	second := e.createPolicy(e.scope)
	payload := e.mustOKAs(e.actor, e.scope, opPolicyUpdate, policyUpdateInput{
		Scope: e.scope, ID: second.ID, ExpectedVersion: 1, Definition: policyDef(e.scope),
	})
	var updated stagedPolicyBody
	e.decode(payload.Data, &updated)
	if updated.Resource.Version != 2 || updated.Resource.ID != second.ID {
		t.Fatalf("updated resource: %+v", updated.Resource)
	}
}

// json0 is a tiny helper naming raw condition JSON inline.
func json0(raw string) json.RawMessage { return json.RawMessage(raw) }

func TestRuleCeilingFence(t *testing.T) {
	e := newEnv(t)

	// The default staging authority holds one wildcard ceiling grant.
	staged := e.stageRule(e.actor, ruleCreateInput{
		Scope: e.scope, Definition: ruleDef(e.scope, "deploy.render", e.ceiling),
	})
	if staged.Resource.Version != 1 || staged.Resource.CeilingGrantID != e.ceiling {
		t.Fatalf("staged rule: %+v", staged.Resource)
	}
	calls := e.ports.callsOf("_configuration.stage")
	if len(calls) != 1 {
		t.Fatalf("stage calls: %d", len(calls))
	}
	var call stageCallInput
	e.decode(calls[0].Input, &call)
	if call.Change.Kind != kindAutonomyRule || call.Change.Action != changeCreate {
		t.Fatalf("staged change: %+v", call.Change)
	}

	// A ceiling grant id the principal does not hold is refused.
	missing := e.expectFault(opRuleCreate, ruleCreateInput{
		Scope: e.scope, Definition: ruleDef(e.scope, "deploy.render", e.ids.New()),
	}, contract.CodePermissionDenied)
	if !contains(missing.Message, "is not current authority of the staging principal") {
		t.Fatalf("missing ceiling: %s", missing.Message)
	}

	// A ceiling grant without capability coverage is refused.
	e.ports.setAuthority(e.grantsOf(e.owner, contract.KindService, []peerGrant{
		allowGrant(e.ceiling, e.owner, contract.Scope{}, []string{"other.thing"}, nil),
	}))
	nocap := e.expectFault(opRuleCreate, ruleCreateInput{
		Scope: e.scope, Definition: ruleDef(e.scope, "deploy.render", e.ceiling),
	}, contract.CodePermissionDenied)
	if !contains(nocap.Message, "not current authority") {
		t.Fatalf("capability ceiling: %s", nocap.Message)
	}

	// A ceiling grant that does not reach the rule destinations is
	// refused.
	e.ports.setAuthority(e.grantsOf(e.owner, contract.KindService, []peerGrant{
		allowGrant(e.ceiling, e.owner, contract.Scope{}, []string{capWildcard}, []string{"other.example"}),
	}))
	narrow := ruleDef(e.scope, "deploy.render", e.ceiling)
	narrow.Destinations = []string{"prod.example"}
	nodest := e.expectFault(opRuleCreate, ruleCreateInput{Scope: e.scope, Definition: narrow},
		contract.CodePermissionDenied)
	if !contains(nodest.Message, "not current authority") {
		t.Fatalf("destination ceiling: %s", nodest.Message)
	}

	// An expired ceiling grant is not current authority.
	expired := e.clock.Now().Add(-time.Hour)
	e.ports.setAuthority(authorityResource{
		Principal: peerPrincipal{ID: e.owner, Version: 1, Kind: contract.KindService, Scope: e.scope},
		Grants: []peerGrant{{
			ID: e.ceiling, Version: 1, PrincipalID: e.owner, Scope: contract.Scope{},
			Capabilities: []string{capWildcard}, Destinations: nil, Denied: false,
			ExpiresAt: &expired,
		}},
	})
	lapsed := e.expectFault(opRuleCreate, ruleCreateInput{
		Scope: e.scope, Definition: ruleDef(e.scope, "deploy.render", e.ceiling),
	}, contract.CodePermissionDenied)
	if !contains(lapsed.Message, "not current authority") {
		t.Fatalf("expired ceiling: %s", lapsed.Message)
	}

	// A staging principal with no authority at all cannot establish a
	// ceiling.
	e.ports.failOp("_identity.authority", &contract.Fault{Code: contract.CodeNotFound, Message: "no principal"})
	noauth := e.expectFault(opRuleCreate, ruleCreateInput{
		Scope: e.scope, Definition: ruleDef(e.scope, "deploy.render", e.ceiling),
	}, contract.CodePermissionDenied)
	if !contains(noauth.Message, "holds no authority") {
		t.Fatalf("no authority: %s", noauth.Message)
	}
	e.ports.failOp("_identity.authority", nil)

	// A rule definition without evidence kinds is rejected before the
	// ceiling runs.
	bad := ruleDef(e.scope, "deploy.render", e.ceiling)
	bad.RequiredEvidence = []string{}
	f := e.expectFault(opRuleCreate, ruleCreateInput{Scope: e.scope, Definition: bad}, contract.CodeInvalidInput)
	if !contains(f.Message, "promotion rule definition rejected") {
		t.Fatalf("evidence kinds: %s", f.Message)
	}
}

func TestValidateDiagnostics(t *testing.T) {
	e := newEnv(t)

	// A valid create slice produces no diagnostics.
	created := e.validate(policyChangeCreate(e.ids.New(), e.scope))
	if len(created.Diagnostics) != 0 {
		t.Fatalf("valid create diagnostics: %+v", created.Diagnostics)
	}
	if len(created.Requirements) != 0 || len(created.Dependencies) != 0 {
		t.Fatalf("valid create extras: %+v %+v", created.Requirements, created.Dependencies)
	}

	// The same resource twice in one candidate is a duplicate.
	id := e.ids.New()
	twice := e.validate(
		policyChangeCreate(id, e.scope),
		policyChangeCreate(id, e.scope),
	)
	if got := diagCodes(twice); len(got) != 1 || got[0] != "duplicate_change" {
		t.Fatalf("duplicate change diagnostics: %v", got)
	}

	// An unsupported action is a diagnostic, not a fault.
	deleted := policyChangeCreate(e.ids.New(), e.scope)
	deleted.Action = "delete"
	if got := diagCodes(e.validate(deleted)); len(got) != 1 || got[0] != "unsupported_action" {
		t.Fatalf("unsupported action diagnostics: %v", got)
	}

	// Updating a resource that does not exist is an unknown reference.
	if got := diagCodes(e.validate(policyChangeUpdate(e.ids.New(), 1, e.scope))); len(got) != 1 || got[0] != "unknown_reference" {
		t.Fatalf("unknown reference diagnostics: %v", got)
	}

	// A stale expected version and a wrong post-apply version are
	// separate diagnostics.
	row := e.createPolicy(e.scope)
	stale := policyChangeUpdate(row.ID, 7, e.scope)
	if got := diagCodes(e.validate(stale)); len(got) != 1 || got[0] != "stale_version" {
		t.Fatalf("stale update diagnostics: %v", got)
	}
	wrongVersion := policyChangeUpdate(row.ID, 1, e.scope)
	wrongVersion.Definition = rawDef(wirePolicy{ID: row.ID, Version: 9, Scope: e.scope, Rules: []wireRule{}})
	if got := diagCodes(e.validate(wrongVersion)); len(got) != 1 || got[0] != "version" {
		t.Fatalf("wrong post-apply version diagnostics: %v", got)
	}

	// A create with a nonzero expected version or a definition version
	// other than 1 is refused by diagnostics.
	badCreate := policyChangeCreate(e.ids.New(), e.scope)
	badCreate.ExpectedVersion = 3
	if got := diagCodes(e.validate(badCreate)); len(got) != 1 || got[0] != "expected_version" {
		t.Fatalf("create expected version diagnostics: %v", got)
	}
	mislabeled := policyChangeCreate(e.ids.New(), e.scope)
	mislabeled.Definition = rawDef(wirePolicy{ID: mislabeled.ID, Version: 4, Scope: e.scope, Rules: []wireRule{}})
	if got := diagCodes(e.validate(mislabeled)); len(got) != 1 || got[0] != "identity_mismatch" {
		t.Fatalf("create definition version diagnostics: %v", got)
	}

	// A foreign installation is an installation mismatch.
	foreign := e.scope
	foreign.InstallationID = e.ids.New()
	misplaced := policyChangeCreate(e.ids.New(), foreign)
	if got := diagCodes(e.validate(misplaced)); len(got) != 1 || got[0] != "installation_mismatch" {
		t.Fatalf("installation mismatch diagnostics: %v", got)
	}

	// A promotion rule names its ceiling grant as a prerequisite the
	// apply flow must confirm; an empty evidence list is a definition
	// diagnostic.
	ruleID := e.ids.New()
	def := ruleDef(e.scope, "deploy.render", e.ceiling)
	def.RequiredEvidence = []string{}
	rule := ruleChangeCreate(ruleID, def)
	v := e.validate(rule)
	if got := diagCodes(v); len(got) != 1 || got[0] != "definition" {
		t.Fatalf("rule definition diagnostics: %v", got)
	}
	if len(v.Requirements) != 1 || v.Requirements[0].Code != contract.CodePrerequisiteMissing ||
		v.Requirements[0].ResourceID != e.ceiling {
		t.Fatalf("ceiling requirement: %+v", v.Requirements)
	}

	// A required_tool_version condition becomes a sorted dependency
	// identity.
	t1 := wireRef{ID: e.ids.New(), Version: 3}
	t2 := wireRef{ID: e.ids.New(), Version: 2}
	pol := policyChangeCreate(e.ids.New(), e.scope, standingRule("deploy.render", decisionAllow, false,
		mustJSON(t, map[string]any{"required_tool_version": map[string]any{"id": string(t2.ID), "version": 2}})),
		standingRule("document.read", decisionAllow, false,
			mustJSON(t, map[string]any{"required_tool_version": map[string]any{"id": string(t1.ID), "version": 3}})))
	v = e.validate(pol)
	if len(v.Dependencies) != 2 || v.Dependencies[0].ID != t1.ID || v.Dependencies[1].ID != t2.ID {
		t.Fatalf("condition dependencies: %+v", v.Dependencies)
	}

	// An archive produces a restrictive-side-effect warning.
	row2 := e.createPolicy(e.scope)
	warned := e.validate(wireChange{
		Kind: kindPolicy, Action: changeArchive, ID: row2.ID, ExpectedVersion: 1,
		Definition: rawDef(wirePolicy{ID: row2.ID, Version: 2, Scope: row2.Scope, Rules: row2.Rules}),
	})
	if len(warned.Diagnostics) != 1 || warned.Diagnostics[0].Code != "restrictive_side_effect" ||
		warned.Diagnostics[0].Severity != "warning" {
		t.Fatalf("archive warning: %+v", warned.Diagnostics)
	}
}

// policyChangeCreate builds one sealed policy create change.
func policyChangeCreate(id contract.ID, scope contract.Scope, rules ...wireRule) wireChange {
	if rules == nil {
		rules = []wireRule{}
	}
	return wireChange{
		Kind: kindPolicy, Action: changeCreate, ID: id, ExpectedVersion: 0,
		Definition: rawDef(wirePolicy{ID: id, Version: 1, Scope: scope, Rules: rules}),
	}
}

// policyChangeUpdate builds one sealed policy update change.
func policyChangeUpdate(id contract.ID, expected int64, scope contract.Scope, rules ...wireRule) wireChange {
	if rules == nil {
		rules = []wireRule{}
	}
	return wireChange{
		Kind: kindPolicy, Action: changeUpdate, ID: id, ExpectedVersion: expected,
		Definition: rawDef(wirePolicy{ID: id, Version: contract.Version(expected + 1), Scope: scope, Rules: rules}),
	}
}

// ruleChangeCreate builds one sealed promotion-rule create change.
func ruleChangeCreate(id contract.ID, def ruleDefinitionInput) wireChange {
	return wireChange{
		Kind: kindAutonomyRule, Action: changeCreate, ID: id, ExpectedVersion: 0,
		Definition: rawDef(ruleResource(id, 1, def)),
	}
}

func TestActivateFences(t *testing.T) {
	e := newEnv(t)

	// A create applies at version 1 and the resource resolves.
	id := e.ids.New()
	e.activateChanges(policyChangeCreate(id, e.scope, standingRule("deploy.render", decisionAllow, false, nil)))
	got := e.getPolicy(e.scope, id)
	if got.Version != 1 || len(got.Rules) != 1 {
		t.Fatalf("activated policy: %+v", got)
	}

	// An update applies at the post-apply version.
	e.activateChanges(policyChangeUpdate(id, 1, e.scope, standingRule("document.read", decisionAllow, false, nil)))
	got = e.getPolicy(e.scope, id)
	if got.Version != 2 || got.Rules[0].Capability != "document.read" {
		t.Fatalf("updated policy: %+v", got)
	}

	// A stale expected version faults and rolls back.
	f := e.expectFault(opActivate, candidateEnvelope{Candidate: candidateInput{
		PlanID: e.ids.New(), BaseRevision: 1, CandidateDigest: candidateDigest("stale"),
		Changes: []wireChange{policyChangeUpdate(id, 1, e.scope)}, Dependencies: []wireRef{},
	}}, contract.CodeStaleVersion)
	if !contains(f.Message, "is at version 2, not the expected 1") {
		t.Fatalf("stale activate: %s", f.Message)
	}
	got = e.getPolicy(e.scope, id)
	if got.Version != 2 {
		t.Fatalf("rolled-back policy moved: %+v", got)
	}

	// An update definition must carry the post-apply version.
	f = e.expectFault(opActivate, candidateEnvelope{Candidate: candidateInput{
		PlanID: e.ids.New(), BaseRevision: 1, CandidateDigest: candidateDigest("ver"),
		Changes: []wireChange{{
			Kind: kindPolicy, Action: changeUpdate, ID: id, ExpectedVersion: 2,
			Definition: rawDef(wirePolicy{ID: id, Version: 9, Scope: e.scope, Rules: []wireRule{}}),
		}}, Dependencies: []wireRef{},
	}}, contract.CodeInvalidInput)
	if !contains(f.Message, "must carry the post-apply version 3") {
		t.Fatalf("post-apply version: %s", f.Message)
	}

	// A create must carry expected_version 0 and definition version 1.
	fencedID := e.ids.New()
	f = e.expectFault(opActivate, candidateEnvelope{Candidate: candidateInput{
		PlanID: e.ids.New(), BaseRevision: 1, CandidateDigest: candidateDigest("create"),
		Changes: []wireChange{{
			Kind: kindPolicy, Action: changeCreate, ID: fencedID, ExpectedVersion: 4,
			Definition: rawDef(wirePolicy{ID: fencedID, Version: 1, Scope: e.scope, Rules: []wireRule{}}),
		}}, Dependencies: []wireRef{},
	}}, contract.CodeInvalidInput)
	if !contains(f.Message, "must carry expected_version 0 and definition version 1") {
		t.Fatalf("create fence: %s", f.Message)
	}

	// A definition homed in another installation faults.
	foreign := e.scope
	foreign.InstallationID = e.ids.New()
	f = e.expectFault(opActivate, candidateEnvelope{Candidate: candidateInput{
		PlanID: e.ids.New(), BaseRevision: 1, CandidateDigest: candidateDigest("foreign"),
		Changes: []wireChange{policyChangeCreate(e.ids.New(), foreign)}, Dependencies: []wireRef{},
	}}, contract.CodeInvalidInput)
	if !contains(f.Message, "homed in another installation") {
		t.Fatalf("foreign installation: %s", f.Message)
	}

	// An archived resource refuses changes.
	e.archivePolicy(id, 2)
	f = e.expectFault(opActivate, candidateEnvelope{Candidate: candidateInput{
		PlanID: e.ids.New(), BaseRevision: 1, CandidateDigest: candidateDigest("arch"),
		Changes: []wireChange{policyChangeUpdate(id, 3, e.scope)}, Dependencies: []wireRef{},
	}}, contract.CodeConflict)
	if !contains(f.Message, "is archived and refuses changes") {
		t.Fatalf("archived activate: %s", f.Message)
	}
}
