package policy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Behavioral tests for the authority intersection: dispatch fences, the
// grant envelope, deny precision, the task/worker envelope, standing-policy
// shadowing, bounded conditions, exact review consultation and explain mode.
// Every fence here is asserted with its exact fault code and the message
// fragment that identifies the fence.

func expectDecision(t *testing.T, got wirePolicyResult, want string, reasonFragment string) {
	t.Helper()
	if got.Decision != want {
		t.Fatalf("decision %q (%v), want %q", got.Decision, got.Reasons, want)
	}
	if reasonFragment != "" {
		found := false
		for _, r := range got.Reasons {
			if contains(r, reasonFragment) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no reason contains %q; reasons: %v", reasonFragment, got.Reasons)
		}
	}
}

// rawCall dispatches a pre-encoded input body, for schema-fence tests that
// need control over the exact JSON.
func (e *testEnv) rawCall(op string, raw []byte) (contract.Payload, error) {
	var payload contract.Payload
	err := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		payload = p
		return err
	})
	return payload, err
}

// rawFault dispatches a pre-encoded body and returns the fault the operation
// failed with, from the returned error or the payload.
func (e *testEnv) rawFault(op string, raw []byte) *contract.Fault {
	e.t.Helper()
	payload, err := e.rawCall(op, raw)
	var f *contract.Fault
	if err != nil {
		if !errAs(err, &f) {
			e.t.Fatalf("%s: non-fault error: %v", op, err)
		}
		return f
	}
	if payload.Error == nil {
		e.t.Fatalf("%s: expected a fault, got completed payload", op)
	}
	return payload.Error
}

// errAs reports whether err carries a *contract.Fault.
func errAs(err error, target **contract.Fault) bool {
	if f, ok := err.(*contract.Fault); ok {
		*target = f
		return true
	}
	return false
}

// mustJSON marshals a test body, failing the test on error.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestDispatchFences(t *testing.T) {
	e := newEnv(t)

	// An empty operation id is refused before anything else.
	fault := e.rawFault("", []byte(`{}`))
	if fault.Code != contract.CodeInvalidInput || !contains(fault.Message, "operation is required") {
		t.Fatalf("empty operation: %s %s", fault.Code, fault.Message)
	}

	// An unknown operation is refused by name.
	fault = e.rawFault("_policy.teleport", []byte(`{}`))
	if fault.Code != contract.CodeInvalidInput || !contains(fault.Message, `unknown operation "_policy.teleport"`) {
		t.Fatalf("unknown operation: %s %s", fault.Code, fault.Message)
	}

	// Every descriptor serves exactly one version.
	input := checkInput{Scope: e.scope, Capability: "document.read"}
	raw := mustJSON(t, input)
	if payload, err := e.rawCall(opCheck, raw); err != nil || payload.Error != nil {
		t.Fatalf("version 1 check failed: %v %v", err, payload.Error)
	}
	fault = e.rawFaultVersion(opCheck, raw, 2)
	if fault.Code != contract.CodeInvalidInput || !contains(fault.Message, "version 2 is unsupported") {
		t.Fatalf("version 2 check: %s %s", fault.Code, fault.Message)
	}

	// Mutations refuse to run on a read snapshot.
	activateRaw := mustJSON(t, candidateEnvelope{Candidate: candidateInput{
		PlanID: e.ids.New(), BaseRevision: 1, CandidateDigest: candidateDigest("ro"),
		Changes: []wireChange{}, Dependencies: []wireRef{},
	}})
	err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		_, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: opActivate, Version: 1, Input: activateRaw})
		var f *contract.Fault
		if !errAs(err, &f) || f.Code != contract.CodeInvalidInput || !contains(f.Message, "cannot run on a read snapshot") {
			t.Fatalf("mutation on read snapshot: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read transaction: %v", err)
	}

	// The input schema rejects a check without its required capability.
	fault = e.rawFault(opCheck, mustJSON(t, map[string]any{"scope": e.scope}))
	if fault.Code != contract.CodeInvalidInput || !contains(fault.Message, "input rejected") {
		t.Fatalf("check without capability: %s %s", fault.Code, fault.Message)
	}

	// Strict decoding rejects unknown fields.
	fault = e.rawFault(opCheck, mustJSON(t, map[string]any{
		"scope": e.scope, "capability": "document.read", "smuggled": true,
	}))
	if fault.Code != contract.CodeInvalidInput || !contains(fault.Message, "input rejected") {
		t.Fatalf("unknown field: %s %s", fault.Code, fault.Message)
	}
}

// rawFaultVersion dispatches with an explicit descriptor version.
func (e *testEnv) rawFaultVersion(op string, raw []byte, version int64) *contract.Fault {
	e.t.Helper()
	var payload contract.Payload
	err := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: version, Input: raw})
		payload = p
		return err
	})
	var f *contract.Fault
	if err != nil {
		if !errAs(err, &f) {
			e.t.Fatalf("%s: non-fault error: %v", op, err)
		}
		return f
	}
	if payload.Error == nil {
		e.t.Fatalf("%s: expected a fault, got completed payload", op)
	}
	return payload.Error
}

func TestCheckAuthorityBasics(t *testing.T) {
	e := newEnv(t)

	// The default wildcard grant admits any capability.
	got := e.check(e.scope, "document.read", nil, "")
	expectDecision(t, got, decisionAllow, "")
	if len(got.Requirements) != 0 {
		t.Fatalf("allow carries requirements: %v", got.Requirements)
	}

	// A revoked principal is denied outright.
	e.ports.setAuthority(authorityResource{
		Principal:    peerPrincipal{ID: e.owner, Version: 1, Kind: contract.KindService, Scope: e.scope, Revoked: true},
		Grants:       []peerGrant{allowGrant(e.ceiling, e.owner, contract.Scope{}, []string{capWildcard}, nil)},
		Restrictions: []string{},
	})
	got = e.check(e.scope, "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "the principal is revoked")

	// A missing principal is a deny, not a peer failure.
	e.ports.failOp("_identity.authority", &contract.Fault{Code: contract.CodeNotFound, Message: "unknown principal"})
	got = e.check(e.scope, "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "the principal holds no authority in this scope")
	e.ports.failOp("_identity.authority", nil)

	// Grants scoped to another organization do not cover this request.
	e.ports.setAuthority(e.grantsOf(e.owner, contract.KindService, []peerGrant{
		allowGrant(e.ceiling, e.owner, e.orgScope(e.ids.New()), []string{capWildcard}, nil),
	}))
	got = e.check(e.orgScope(e.org), "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "current grants do not cover the request scope")

	// A capability outside the grant set is refused.
	e.ports.setAuthority(e.grantsOf(e.owner, contract.KindService, []peerGrant{
		allowGrant(e.ceiling, e.owner, contract.Scope{}, []string{"other.thing"}, nil),
	}))
	got = e.check(e.scope, "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "do not include the requested capability")

	// A binding permission rescues an unheld capability.
	e.ports.setBindings([]peerBinding{{
		ID: e.ids.New(), Version: 1, Scope: contract.Scope{},
		Kind: "worker", TargetID: e.ids.New(), Permissions: []string{"document.read"},
	}})
	got = e.check(e.scope, "document.read", nil, "")
	expectDecision(t, got, decisionAllow, "")

	// An active restriction on the capability denies absolutely.
	e.ports.setBindings(nil)
	e.ports.setAuthority(authorityResource{
		Principal:    peerPrincipal{ID: e.owner, Version: 1, Kind: contract.KindService, Scope: e.scope},
		Grants:       []peerGrant{allowGrant(e.ceiling, e.owner, contract.Scope{}, []string{capWildcard}, nil)},
		Restrictions: []string{"document.read"},
	})
	got = e.check(e.scope, "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "an active restriction covers capability document.read")
}

func TestCheckDenyGrantPrecision(t *testing.T) {
	e := newEnv(t)

	// A deny grant subtracts the capability even with a matching allow.
	e.ports.setAuthority(e.grantsOf(e.owner, contract.KindService, []peerGrant{
		allowGrant(e.ceiling, e.owner, contract.Scope{}, []string{"document.read"}, nil),
		denyGrant(e.ids.New(), e.owner, contract.Scope{}, []string{"document.read"}),
	}))
	got := e.check(e.scope, "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "a deny grant covers capability document.read")

	// A wildcard deny grant denies every capability.
	e.ports.setAuthority(e.grantsOf(e.owner, contract.KindService, []peerGrant{
		allowGrant(e.ceiling, e.owner, contract.Scope{}, []string{capWildcard}, nil),
		denyGrant(e.ids.New(), e.owner, contract.Scope{}, []string{capWildcard}),
	}))
	got = e.check(e.scope, "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "a deny grant covers capability document.read")

	// A deny grant scoped to another organization never reaches this
	// request: the matching allow grant stands.
	e.ports.setAuthority(e.grantsOf(e.owner, contract.KindService, []peerGrant{
		allowGrant(e.ceiling, e.owner, contract.Scope{}, []string{"document.read"}, nil),
		denyGrant(e.ids.New(), e.owner, e.orgScope(e.ids.New()), []string{"document.read"}),
	}))
	got = e.check(e.orgScope(e.org), "document.read", nil, "")
	expectDecision(t, got, decisionAllow, "")
}

func TestCheckTaskAndWorkerEnvelope(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	task := e.ids.New()

	// A request scope pinned to a missing task is denied.
	scoped := e.taskScope(task)
	got := e.check(scoped, "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "the task in the request scope does not exist")

	// A cancelled task denies admission.
	e.setTask(task, "cancelled", worker, 1)
	got = e.check(scoped, "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "the task in the request scope is cancelled")

	// A task owned by another worker denies the request.
	other := e.ids.New()
	e.setTask(task, "running", other, 1)
	workerScoped := e.scope
	workerScoped.WorkerID = worker
	workerScoped.TaskID = task
	got = e.check(workerScoped, "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "belongs to a different worker")

	// A worker scope with no bound worker in configuration denies.
	taskless := e.scope
	taskless.WorkerID = worker
	got = e.check(taskless, "document.read", nil, "")
	expectDecision(t, got, decisionDeny, "the worker in the request scope is not bound in the current configuration")

	// A consistent worker+task envelope admits.
	e.bindWorker(worker, "model-x")
	e.setTask(task, "running", worker, 1)
	got = e.check(workerScoped, "document.read", nil, "")
	expectDecision(t, got, decisionAllow, "")
}

func TestStandingPolicyShadowing(t *testing.T) {
	e := newEnv(t)

	// A narrower org-scope allow rule shadows an install-scope review
	// rule: the specific standing policy governs its class.
	broad := e.createPolicy(e.scope, standingRule("deploy.render", decisionReview, true, nil))
	narrow := e.createPolicy(e.orgScope(e.org), standingRule("deploy.render", decisionAllow, false, nil))
	got := e.check(e.orgScope(e.org), "deploy.render", nil, "")
	expectDecision(t, got, decisionAllow, "")
	if len(got.Requirements) != 0 {
		t.Fatalf("narrow allow must not demand review: %v", got.Requirements)
	}
	_ = broad
	_ = narrow

	// A human-required rule yields a review requirement bound to the
	// capability digest, eligible chiefs and a 24h horizon. The org
	// allow policy does not cover an installation-wide request.
	got = e.check(e.scope, "deploy.render", nil, "")
	expectDecision(t, got, decisionReview, "policy "+string(broad.ID)+" marks capability deploy.render human required")
	if len(got.Requirements) != 1 {
		t.Fatalf("expected one requirement, got %v", got.Requirements)
	}
	req := got.Requirements[0]
	if !req.HumanRequired {
		t.Fatal("requirement is not human required")
	}
	if req.ActionDigest != actionDigest("deploy.render", e.scope) {
		t.Fatalf("digest %s, want %s", req.ActionDigest, actionDigest("deploy.render", e.scope))
	}
	if len(req.EligiblePrincipals) != 1 || req.EligiblePrincipals[0] != e.chief {
		t.Fatalf("eligible principals %v, want [%s]", req.EligiblePrincipals, e.chief)
	}
	if !req.ExpiresAt.After(e.clock.Now().Add(23 * time.Hour)) {
		t.Fatalf("requirement horizon %v is not ~24h", req.ExpiresAt)
	}
	if req.SeparateProposer {
		t.Fatal("service actors do not require a separate proposer")
	}

	// A worker actor demands a separate proposer: the actor kind, not
	// the request scope, drives the fence.
	worker := e.ids.New()
	e.bindWorker(worker, "model-x")
	e.ports.setAuthority(authorityResource{
		Principal: peerPrincipal{ID: worker, Version: 1, Kind: "worker", Scope: e.scope},
		Grants:    []peerGrant{allowGrant(e.ids.New(), worker, contract.Scope{}, []string{capWildcard}, nil)},
	})
	payload := e.mustOKAs(e.workerActor(worker), e.workerScope(worker), opCheck,
		checkInput{Scope: e.workerScope(worker), Capability: "deploy.render"})
	var body policyResultBody
	e.decode(payload.Data, &body)
	if body.Resource.Decision != decisionReview || len(body.Resource.Requirements) != 1 {
		t.Fatalf("worker check: %v %v", body.Resource.Decision, body.Resource.Requirements)
	}
	if !body.Resource.Requirements[0].SeparateProposer {
		t.Fatal("worker actor must demand a separate proposer")
	}

	// A deny rule wins from any covering scope, even over a more
	// specific allow rule.
	denyPolicy := e.createPolicy(e.scope, standingRule("deploy.render", decisionDeny, false, nil))
	got = e.check(e.orgScope(e.org), "deploy.render", nil, "")
	expectDecision(t, got, decisionDeny, "standing policy "+string(denyPolicy.ID)+" denies capability deploy.render")
}

func TestCheckConditions(t *testing.T) {
	e := newEnv(t)
	capability := "deploy.render"

	// max_cost over the bound denies; the policy id names the fence.
	action := e.validAction(e.scope)
	action.CostBound = wireMoney{Currency: "USD", MicroUnit: 100}
	over := e.createPolicy(e.scope, standingRule(capability, decisionAllow, false,
		json.RawMessage(`{"max_cost":{"currency":"USD","micro_units":99}}`)))
	got := e.check(e.scope, capability, &action, "")
	expectDecision(t, got, decisionDeny, "action cost bound exceeds policy "+string(over.ID)+" maximum")

	// max_cost without an exact action is a prerequisite decision, not a
	// denial.
	got = e.check(e.scope, capability, nil, "")
	expectDecision(t, got, decisionPrerequisiteMissing, "condition max_cost requires the exact action")
	e.archivePolicy(over.ID, 1)

	// A required tool version mismatch denies.
	mismatch := e.createPolicy(e.scope, standingRule(capability, decisionAllow, false,
		mustJSON(t, map[string]any{"required_tool_version": map[string]any{"id": string(action.Tool.ID), "version": 2}})))
	got = e.check(e.scope, capability, &action, "")
	expectDecision(t, got, decisionDeny, "does not satisfy policy "+string(mismatch.ID)+" required version")
	e.archivePolicy(mismatch.ID, 1)

	// The exact tool version admits.
	match := e.createPolicy(e.scope, standingRule(capability, decisionAllow, false,
		mustJSON(t, map[string]any{"required_tool_version": map[string]any{"id": string(action.Tool.ID), "version": 1}})))
	got = e.check(e.scope, capability, &action, "")
	expectDecision(t, got, decisionAllow, "")
	e.archivePolicy(match.ID, 1)

	// required_worker_id pins the capability to one worker.
	worker := e.ids.New()
	e.bindWorker(worker, "model-x")
	pinned := e.createPolicy(e.scope, standingRule(capability, decisionAllow, false,
		mustJSON(t, map[string]any{"required_worker_id": string(worker)})))
	got = e.check(e.scope, capability, nil, "")
	expectDecision(t, got, decisionDeny, "policy "+string(pinned.ID)+" restricts capability deploy.render to worker "+string(worker))
	e.archivePolicy(pinned.ID, 1)

	// not_before in the future denies until the clock passes it.
	windowed := e.createPolicy(e.scope, standingRule(capability, decisionAllow, false,
		mustJSON(t, map[string]any{"not_before": e.clock.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)})))
	got = e.check(e.scope, capability, nil, "")
	expectDecision(t, got, decisionDeny, "admits capability deploy.render only from")
	e.clock.Advance(2 * time.Hour)
	got = e.check(e.scope, capability, nil, "")
	expectDecision(t, got, decisionAllow, "")
	e.archivePolicy(windowed.ID, 1)

	// expires_at in the past denies admission.
	expired := e.createPolicy(e.scope, standingRule(capability, decisionAllow, false,
		mustJSON(t, map[string]any{"expires_at": e.clock.Now().Add(-time.Hour).UTC().Format(time.RFC3339)})))
	got = e.check(e.scope, capability, nil, "")
	expectDecision(t, got, decisionDeny, "admission for capability deploy.render expired")
	e.archivePolicy(expired.ID, 1)

	// required_review forces a review decision.
	forced := e.createPolicy(e.scope, standingRule(capability, decisionAllow, false,
		json.RawMessage(`{"required_review":true}`)))
	got = e.check(e.scope, capability, nil, "")
	expectDecision(t, got, decisionReview, "policy "+string(forced.ID)+" requires an exact review for capability deploy.render")
	e.archivePolicy(forced.ID, 1)

	// required_classification must match the project classification.
	e.ports.setProject(&peerProject{ID: e.ids.New(), OrganizationID: e.org, Classification: "internal"})
	mismatched := e.createPolicy(e.scope, standingRule(capability, decisionAllow, false,
		json.RawMessage(`{"required_classification":"restricted"}`)))
	got = e.check(e.scope, capability, nil, "")
	expectDecision(t, got, decisionDeny, "classification internal does not satisfy policy "+string(mismatched.ID)+" requirement restricted")
	e.archivePolicy(mismatched.ID, 1)

	e.createPolicy(e.scope, standingRule(capability, decisionAllow, false,
		json.RawMessage(`{"required_classification":"internal"}`)))
	got = e.check(e.scope, capability, nil, "")
	expectDecision(t, got, decisionAllow, "")
}

func TestUnsupportedConditionFailsClosed(t *testing.T) {
	e := newEnv(t)
	row := policyRow{
		ID: e.ids.New(), Version: 1, Scope: e.scope,
		Rules: []wireRule{standingRule("deploy.render", decisionAllow, false,
			json.RawMessage(`{"quantum_entanglement":true}`))},
	}
	e.insertRawPolicy(row)
	got := e.check(e.scope, "deploy.render", nil, "")
	expectDecision(t, got, decisionDeny, `uses unsupported condition "quantum_entanglement"`)
}

func TestExactReviewConsultation(t *testing.T) {
	e := newEnv(t)
	e.createPolicy(e.scope, standingRule("deploy.render", decisionAllow, true, nil))
	digest := candidateDigest("sealed-decision")

	// No recorded review: the requirement stands.
	got := e.check(e.scope, "deploy.render", nil, digest)
	expectDecision(t, got, decisionReview, "")
	if len(got.Requirements) != 1 || got.Requirements[0].ActionDigest != digest {
		t.Fatalf("pending review must keep the requirement: %v", got.Requirements)
	}

	// An approved exact review satisfies it.
	e.ports.setReview(digest, reviewsCheckBody{
		Eligible: true, Decision: &peerDecision{Decision: "approve"},
	})
	got = e.check(e.scope, "deploy.render", nil, digest)
	expectDecision(t, got, decisionAllow, "exact review "+digest+" is approved")
	if len(got.Requirements) != 0 {
		t.Fatalf("approved review must clear the requirement: %v", got.Requirements)
	}

	// A rejected exact review denies.
	e.ports.setReview(digest, reviewsCheckBody{
		Eligible: true, Decision: &peerDecision{Decision: "reject"},
	})
	got = e.check(e.scope, "deploy.render", nil, digest)
	expectDecision(t, got, decisionDeny, "exact review "+digest+" is rejected")

	// A recorded-but-ineligible review leaves the requirement standing.
	e.ports.setReview(digest, reviewsCheckBody{Eligible: false})
	got = e.check(e.scope, "deploy.render", nil, digest)
	expectDecision(t, got, decisionReview, "")
}

func TestExplainMode(t *testing.T) {
	e := newEnv(t)

	// A wildcard deny rule denies wildcard-matched actions.
	denyAll := e.createPolicy(e.scope, standingRule(capWildcard, decisionDeny, false, nil))
	got := e.check(e.scope, "deploy.render", nil, "")
	expectDecision(t, got, decisionDeny, "standing policy "+string(denyAll.ID)+" denies capability deploy.render")
	e.archivePolicy(denyAll.ID, 1)

	// A named-capability rule is invisible to a capability-less check;
	// the wildcard human-required rule answers.
	named := e.createPolicy(e.scope, standingRule("deploy.render", decisionDeny, false, nil))
	wildcard := e.createPolicy(e.scope, standingRule(capWildcard, decisionAllow, true, nil))
	got = e.check(e.scope, "", nil, "")
	expectDecision(t, got, decisionReview, "policy "+string(wildcard.ID)+" marks capability  human required")
	if len(got.Requirements) != 1 {
		t.Fatalf("expected one requirement, got %v", got.Requirements)
	}
	_ = named

	// With an exact action the requirement binds the action digest.
	action := e.validAction(e.scope)
	got = e.check(e.scope, "", &action, "")
	if len(got.Requirements) != 1 || got.Requirements[0].ActionDigest != exactActionDigest(action) {
		t.Fatalf("explain digest %v, want exact action digest %s",
			got.Requirements, exactActionDigest(action))
	}

	// The exact action still passes the action scope fence.
	mismatched := action
	mismatched.Scope = e.orgScope(e.ids.New())
	explained := e.explain(e.scope, mismatched)
	expectDecision(t, wirePolicyResult(explained), decisionDeny,
		"the action scope does not match the request scope")

	// A default review class requires review once no standing policy
	// governs the class any more.
	e.archivePolicy(wildcard.ID, 1)
	got = e.check(e.scope, "publication.post", nil, "")
	expectDecision(t, got, decisionReview, "capability publication.post is a default review class and no narrower standing policy governs it")

	// An ordinary capability without policy admits.
	got = e.check(e.scope, "document.read", nil, "")
	expectDecision(t, got, decisionAllow, "")
}
