package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Test harness: a real storage database per test, deterministic fakes for
// clock, ids and peer ports, and helpers that drive every operation through
// Service.Handle inside storage write transactions. Peer fakes serve the
// exact bodies this package decodes (identity authority, configuration
// snapshot and stage, tasks snapshot, reviews check) with injectable state
// and faults, recording every invocation for behavioral assertions.

// fakeClock is a deterministic contract.Clock with a controllable instant.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the fake clock forward; Set pins it absolutely.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// seqIDs mints syntactically valid UUIDv4 identities in sequence.
type seqIDs struct {
	mu sync.Mutex
	n  int
}

func (s *seqIDs) New() contract.ID {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return contract.ID(fmt.Sprintf("00000000-0000-4000-8000-%012d", s.n))
}

// fakePorts serves the peer bodies policy decodes, with injectable state and
// faults, recording every call.
type fakePorts struct {
	mu    sync.Mutex
	ids   *seqIDs
	calls []contract.Invocation

	authority authorityResource
	ancestors []peerOrg
	bindings  []peerBinding
	project   *peerProject
	workers   map[contract.ID]peerWorker
	tasks     map[contract.ID]peerTask
	reviews   map[string]reviewsCheckBody

	promoteFault  *contract.Fault
	restrictFault *contract.Fault
	fail          map[string]*contract.Fault
}

func newFakePorts(ids *seqIDs) *fakePorts {
	return &fakePorts{
		ids:     ids,
		workers: map[contract.ID]peerWorker{},
		tasks:   map[contract.ID]peerTask{},
		reviews: map[string]reviewsCheckBody{},
		fail:    map[string]*contract.Fault{},
	}
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	p.calls = append(p.calls, inv)
	injected := p.fail[inv.Operation]
	authority := p.authority
	ancestors := p.ancestors
	bindings := p.bindings
	project := p.project
	tasks := make(map[contract.ID]peerTask, len(p.tasks))
	for id, t := range p.tasks {
		tasks[id] = t
	}
	reviews := make(map[string]reviewsCheckBody, len(p.reviews))
	for d, r := range p.reviews {
		reviews[d] = r
	}
	promoteFault, restrictFault := p.promoteFault, p.restrictFault
	p.mu.Unlock()
	if injected != nil {
		return contract.Payload{}, injected
	}

	var body any
	switch inv.Operation {
	case "_identity.authority":
		var in authorityCallInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		principal := authority.Principal
		principal.ID = in.PrincipalID
		body = authorityBody{Resource: authorityResource{
			Principal: principal, Grants: authority.Grants, Restrictions: authority.Restrictions,
		}}
	case "_configuration.snapshot":
		var in scopeCallInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		snap := scopeSnapshot{
			Scope:     in.Scope,
			Revision:  7,
			Ancestors: ancestors,
			Bindings:  bindings,
			Project:   project,
		}
		if snap.Bindings == nil {
			snap.Bindings = []peerBinding{}
		}
		if w, bound := p.workers[in.Scope.WorkerID]; bound {
			worker := w
			snap.Worker = &worker
		}
		body = snapshotBody{Resource: snap}
	case "_tasks.snapshot":
		var in taskCallInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		task, ok := tasks[in.ID]
		if !ok {
			return contract.Payload{}, &contract.Fault{
				Code: contract.CodeNotFound,
				Message: "task " + string(in.ID) +
					" is unknown in this installation",
			}
		}
		task.ID = in.ID
		task.Scope = in.Scope
		body = taskBody{Resource: task}
	case "_reviews.check":
		var in reviewsCheckCallInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = reviews[in.ActionDigest]
	case "_identity.promote":
		if promoteFault != nil {
			return contract.Payload{}, promoteFault
		}
		return contract.Payload{Status: contract.StatusCompleted}, nil
	case "_identity.restrict":
		if restrictFault != nil {
			return contract.Payload{}, restrictFault
		}
		return contract.Payload{Status: contract.StatusCompleted}, nil
	case "_configuration.stage":
		body = draftResource{Resource: wireDraft{
			ID: p.ids.New(), Version: 1, BaseRevision: 1,
			Changes: []json.RawMessage{}, Diagnostics: []wireDiagnostic{},
		}}
	default:
		return contract.Payload{}, &contract.Fault{
			Code: contract.CodeInternalError, Message: "fake ports: unexpected peer call " + inv.Operation,
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}

// callsOf returns a snapshot of the recorded peer invocations for one op.
func (p *fakePorts) callsOf(op string) []contract.Invocation {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []contract.Invocation
	for _, c := range p.calls {
		if c.Operation == op {
			out = append(out, c)
		}
	}
	return out
}

// countOf counts recorded calls for one op.
func (p *fakePorts) countOf(op string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.calls {
		if c.Operation == op {
			n++
		}
	}
	return n
}

// ---------- fake state mutation ----------

func (p *fakePorts) setAuthority(res authorityResource) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authority = res
}

func (p *fakePorts) setAncestors(orgs []peerOrg) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ancestors = orgs
}

func (p *fakePorts) setBindings(bs []peerBinding) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bindings = bs
}

func (p *fakePorts) setProject(pr *peerProject) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.project = pr
}

func (p *fakePorts) bindWorker(w peerWorker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.workers[w.ID] = w
}

func (p *fakePorts) setTask(t peerTask) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tasks[t.ID] = t
}

func (p *fakePorts) setReview(digest string, body reviewsCheckBody) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reviews[digest] = body
}

func (p *fakePorts) failOp(op string, f *contract.Fault) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail[op] = f
}

func (p *fakePorts) setPromoteFault(f *contract.Fault) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.promoteFault = f
}

// testEnv is one installation wired to a real storage database.
type testEnv struct {
	t       *testing.T
	ctx     context.Context
	db      contract.Database
	svc     *Service
	ports   *fakePorts
	clock   *fakeClock
	ids     *seqIDs
	actor   contract.Actor
	install contract.ID
	owner   contract.ID
	org     contract.ID
	chief   contract.ID
	ceiling contract.ID // id of the default wildcard ceiling grant
	scope   contract.Scope
}

// newEnv opens a fresh database, migrates the policy owner and wires default
// peer state: one root organization with its chief and a wildcard ceiling
// grant for the service actor.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "policy-test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env := &testEnv{
		t: t, ctx: ctx, db: db,
		clock: &fakeClock{now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		ids:   &seqIDs{},
	}
	env.ports = newFakePorts(env.ids)
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids, Ports: env.ports})
	if err != nil {
		t.Fatalf("policy.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate policy: %v", err)
	}
	env.install = env.ids.New()
	env.owner = env.ids.New()
	env.org = env.ids.New()
	env.chief = env.ids.New()
	env.ceiling = env.ids.New()
	env.actor = contract.Actor{PrincipalID: env.owner, Kind: contract.KindService}
	env.scope = contract.Scope{InstallationID: env.install}
	env.ports.setAncestors([]peerOrg{{
		ID: env.org, Version: 1, Key: "root", ChiefID: env.chief,
	}})
	env.ports.setAuthority(authorityResource{
		Principal: peerPrincipal{
			ID: env.owner, Version: 1, Kind: contract.KindService,
			Name: "chief", Scope: env.scope, Revoked: false,
		},
		Grants: []peerGrant{{
			ID: env.ceiling, Version: 1, PrincipalID: env.owner,
			Scope: contract.Scope{}, Capabilities: []string{capWildcard},
			Destinations: []string{}, Denied: false,
		}},
		Restrictions: []string{},
	})
	return env
}

// ---------- call helpers ----------

// callAs runs one operation inside a write transaction with an explicit
// actor and scope.
func (e *testEnv) callAs(actor contract.Actor, scope contract.Scope, op string, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Write(e.ctx, actor, scope, func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		payload = p
		return err
	})
	return payload, err
}

func (e *testEnv) call(op string, in any) (contract.Payload, error) {
	return e.callAs(e.actor, e.scope, op, in)
}

// mustOK runs an operation and requires a completed payload.
func (e *testEnv) mustOK(op string, in any) contract.Payload {
	e.t.Helper()
	payload, err := e.call(op, in)
	if err != nil {
		e.t.Fatalf("%s failed: %v", op, err)
	}
	if payload.Status != contract.StatusCompleted || payload.Error != nil {
		e.t.Fatalf("%s did not complete: status %q error %v", op, payload.Status, payload.Error)
	}
	return payload
}

// mustOKAs runs an operation as an explicit actor on an explicit scope.
func (e *testEnv) mustOKAs(actor contract.Actor, scope contract.Scope, op string, in any) contract.Payload {
	e.t.Helper()
	payload, err := e.callAs(actor, scope, op, in)
	if err != nil {
		e.t.Fatalf("%s failed: %v", op, err)
	}
	if payload.Status != contract.StatusCompleted || payload.Error != nil {
		e.t.Fatalf("%s did not complete: status %q error %v", op, payload.Status, payload.Error)
	}
	return payload
}

// expectFault runs an operation and requires a fault with the exact code.
func (e *testEnv) expectFault(op string, in any, code string) *contract.Fault {
	e.t.Helper()
	return e.expectFaultAs(e.actor, e.scope, op, in, code)
}

// expectFaultAs is expectFault with an explicit actor and scope.
func (e *testEnv) expectFaultAs(actor contract.Actor, scope contract.Scope, op string, in any, code string) *contract.Fault {
	e.t.Helper()
	payload, err := e.callAs(actor, scope, op, in)
	var f *contract.Fault
	if err != nil {
		errors.As(err, &f)
	}
	if f == nil {
		f = payload.Error
	}
	if f == nil {
		e.t.Fatalf("%s: expected %s fault, got completed payload %s", op, code, payload.Status)
	}
	if f.Code != code {
		e.t.Fatalf("%s: fault %s (%s), want %s", op, f.Code, f.Message, code)
	}
	return f
}

func (e *testEnv) decode(raw json.RawMessage, out any) {
	e.t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		e.t.Fatalf("decode payload: %v", err)
	}
}

// ---------- actors and scopes ----------

// workerActor mints the authenticated actor of a worker principal.
func (e *testEnv) workerActor(id contract.ID) contract.Actor {
	return contract.Actor{PrincipalID: id, Kind: "worker"}
}

// clientAgentActor mints the authenticated actor of a client-agent principal.
func (e *testEnv) clientAgentActor(id contract.ID) contract.Actor {
	return contract.Actor{PrincipalID: id, Kind: "client_agent"}
}

// orgScope narrows the installation scope to one organization.
func (e *testEnv) orgScope(org contract.ID) contract.Scope {
	s := e.scope
	s.OrganizationID = org
	return s
}

// workerScope narrows the installation scope to one worker.
func (e *testEnv) workerScope(worker contract.ID) contract.Scope {
	s := e.scope
	s.WorkerID = worker
	return s
}

// taskScope narrows the installation scope to one task.
func (e *testEnv) taskScope(task contract.ID) contract.Scope {
	s := e.scope
	s.TaskID = task
	return s
}

// ---------- fixture builders ----------

// candidateDigest is a schema-valid 64-hex candidate digest for tests.
func candidateDigest(seed string) string {
	return string(contract.Hash([]byte("policy-test-candidate:" + seed)))
}

// standingRule builds one schema-valid standing rule.
func standingRule(capability, decision string, humanRequired bool, conditions json.RawMessage) wireRule {
	if conditions == nil {
		conditions = json.RawMessage(`{}`)
	}
	return wireRule{
		Capability:    capability,
		Effect:        "local",
		Destinations:  []string{},
		Decision:      decision,
		HumanRequired: humanRequired,
		Conditions:    conditions,
	}
}

// policyDef builds a schema-valid policy definition payload.
func policyDef(scope contract.Scope, rules ...wireRule) policyDefinitionInput {
	if rules == nil {
		rules = []wireRule{}
	}
	return policyDefinitionInput{Scope: scope, Rules: rules}
}

// ruleDef builds a schema-valid promotion-rule definition payload pinned to
// one ceiling grant.
func ruleDef(scope contract.Scope, capability string, ceiling contract.ID) ruleDefinitionInput {
	return ruleDefinitionInput{
		Scope:                  scope,
		Capability:             capability,
		Destinations:           []string{},
		RequiredEvidence:       []string{evidenceKindTask},
		MinimumSuccesses:       1,
		EvidenceWindowSeconds:  3600,
		DisqualifyingEvents:    []string{},
		CeilingGrantID:         ceiling,
		HumanRequiredPreserved: true,
	}
}

// rawDef marshals a typed definition into a change definition.
func rawDef(def any) json.RawMessage {
	raw, err := json.Marshal(def)
	if err != nil {
		panic(fmt.Sprintf("marshal definition: %v", err))
	}
	return raw
}

// activateChanges applies one sealed candidate slice of policy-owned changes
// through the internal activation boundary and returns the produced versions.
func (e *testEnv) activateChanges(changes ...wireChange) []wireRef {
	e.t.Helper()
	payload := e.mustOK(opActivate, candidateEnvelope{Candidate: candidateInput{
		PlanID:          e.ids.New(),
		BaseRevision:    1,
		CandidateDigest: candidateDigest("activate"),
		Changes:         changes,
		Dependencies:    []wireRef{},
	}})
	var out versionsBody
	e.decode(payload.Data, &out)
	return out.Versions
}

// createPolicy activates one standing policy and returns its row view.
func (e *testEnv) createPolicy(scope contract.Scope, rules ...wireRule) policyRow {
	e.t.Helper()
	if rules == nil {
		rules = []wireRule{}
	}
	id := e.ids.New()
	def := wirePolicy{ID: id, Version: 1, Scope: scope, Rules: rules}
	e.activateChanges(wireChange{
		Kind: kindPolicy, Action: changeCreate, ID: id, ExpectedVersion: 0, Definition: rawDef(def),
	})
	row, found, err := e.rowOfPolicy(id)
	if err != nil || !found {
		e.t.Fatalf("created policy %s not found: %v", id, err)
	}
	return row
}

// rowOfPolicy reads one policy row inside a write transaction.
func (e *testEnv) rowOfPolicy(id contract.ID) (policyRow, bool, error) {
	var (
		out   policyRow
		found bool
		err   error
	)
	werr := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		out, found, err = e.svc.loadPolicyRow(e.ctx, unit, id)
		return err
	})
	if werr != nil {
		return policyRow{}, false, werr
	}
	return out, found, nil
}

// insertRawPolicy inserts one policy row verbatim through the store, for
// engine states staging would refuse to produce.
func (e *testEnv) insertRawPolicy(row policyRow) {
	e.t.Helper()
	if err := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		return e.svc.insertPolicy(e.ctx, unit, row)
	}); err != nil {
		e.t.Fatalf("insert policy: %v", err)
	}
}

// createRule activates one promotion rule and returns its wire view.
func (e *testEnv) createRule(scope contract.Scope, capability string, ceiling contract.ID) wirePromotionRule {
	e.t.Helper()
	id := e.ids.New()
	def := ruleDef(scope, capability, ceiling)
	full := ruleResource(id, 1, def)
	e.activateChanges(wireChange{
		Kind: kindAutonomyRule, Action: changeCreate, ID: id, ExpectedVersion: 0, Definition: rawDef(full),
	})
	return full
}

// updateRule activates one promotion-rule version bump and returns the new
// version.
func (e *testEnv) updateRule(id contract.ID, expected int64, def ruleDefinitionInput) contract.Version {
	e.t.Helper()
	full := ruleResource(id, contract.Version(expected+1), def)
	versions := e.activateChanges(wireChange{
		Kind: kindAutonomyRule, Action: changeUpdate, ID: id,
		ExpectedVersion: expected, Definition: rawDef(full),
	})
	if len(versions) != 1 {
		e.t.Fatalf("rule update produced %d versions", len(versions))
	}
	return versions[0].Version
}

// archiveRule activates the archive of one promotion rule.
func (e *testEnv) archiveRule(id contract.ID, expected int64) {
	e.t.Helper()
	row, found := e.mustFindRule(id)
	if !found {
		e.t.Fatalf("archive: rule %s not found", id)
	}
	full := row.wire()
	full.Version = contract.Version(expected + 1)
	e.activateChanges(wireChange{
		Kind: kindAutonomyRule, Action: changeArchive, ID: id,
		ExpectedVersion: expected, Definition: rawDef(full),
	})
}

// archivePolicy activates the archive of one standing policy.
func (e *testEnv) archivePolicy(id contract.ID, expected int64) {
	e.t.Helper()
	row, found, err := e.rowOfPolicy(id)
	if err != nil || !found {
		e.t.Fatalf("archive: policy %s not found: %v", id, err)
	}
	full := row.wire()
	full.Version = contract.Version(expected + 1)
	e.activateChanges(wireChange{
		Kind: kindPolicy, Action: changeArchive, ID: id,
		ExpectedVersion: expected, Definition: rawDef(full),
	})
}

// mustFindRule reads one rule row and requires it to exist.
func (e *testEnv) mustFindRule(id contract.ID) (promotionRuleRow, bool) {
	e.t.Helper()
	var (
		out   promotionRuleRow
		found bool
	)
	if err := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		var err error
		out, found, err = e.svc.loadRuleRow(e.ctx, unit, id)
		return err
	}); err != nil {
		e.t.Fatalf("load rule %s: %v", id, err)
	}
	return out, found
}

// mustFindQualification reads one qualification row and requires it to exist.
func (e *testEnv) mustFindQualification(id contract.ID) qualificationRow {
	e.t.Helper()
	var (
		out   qualificationRow
		found bool
	)
	if err := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		var err error
		out, found, err = e.svc.loadQualificationRow(e.ctx, unit, id)
		return err
	}); err != nil {
		e.t.Fatalf("load qualification %s: %v", id, err)
	}
	if !found {
		e.t.Fatalf("qualification %s not found", id)
	}
	return out
}

// mustFindEvidence reads every evidence link of one qualification.
func (e *testEnv) mustFindEvidence(qualification contract.ID) []evidenceRow {
	e.t.Helper()
	var out []evidenceRow
	if err := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		var err error
		out, err = e.svc.loadEvidence(e.ctx, unit, qualification)
		return err
	}); err != nil {
		e.t.Fatalf("load evidence: %v", err)
	}
	return out
}

// ---------- peer-state fixtures ----------

// bindWorker registers one bound worker the snapshot fake serves.
func (e *testEnv) bindWorker(id contract.ID, model string, skills ...wireRef) {
	e.t.Helper()
	if skills == nil {
		skills = []wireRef{}
	}
	e.ports.bindWorker(peerWorker{
		ID: id, OrganizationID: e.org,
		SkillVersions: skills, Profile: &peerWorkerProfile{Model: model},
	})
}

// setTask registers one task the tasks fake serves.
func (e *testEnv) setTask(id contract.ID, state string, worker contract.ID, version contract.Version) {
	e.t.Helper()
	e.ports.setTask(peerTask{
		ID: id, Version: version, Scope: e.scope, WorkerID: worker, State: state,
	})
}

// skillRef builds one versioned skill reference.
func (e *testEnv) skillRef(version contract.Version) wireRef {
	return wireRef{ID: e.ids.New(), Version: version}
}

// grantsOf builds an authority body whose principal is principal and whose
// effective grants are grants.
func (e *testEnv) grantsOf(principal contract.ID, kind string, grants []peerGrant) authorityResource {
	return authorityResource{
		Principal: peerPrincipal{
			ID: principal, Version: 1, Kind: kind, Name: "actor",
			Scope: e.scope, Revoked: false,
		},
		Grants:       grants,
		Restrictions: []string{},
	}
}

// allowGrant builds one allow grant.
func allowGrant(id contract.ID, principal contract.ID, scope contract.Scope, caps, dests []string) peerGrant {
	return peerGrant{
		ID: id, Version: 1, PrincipalID: principal, Scope: scope,
		Capabilities: caps, Destinations: dests, Denied: false,
	}
}

// denyGrant builds one deny grant.
func denyGrant(id contract.ID, principal contract.ID, scope contract.Scope, caps []string) peerGrant {
	return peerGrant{
		ID: id, Version: 1, PrincipalID: principal, Scope: scope,
		Capabilities: caps, Destinations: []string{}, Denied: true,
	}
}

// validAction builds one schema-valid exact action against scope.
func (e *testEnv) validAction(scope contract.Scope) wireAction {
	return wireAction{
		Scope:                 scope,
		Tool:                  wireRef{ID: e.ids.New(), Version: 1},
		Connection:            wireRef{ID: e.ids.New(), Version: 1},
		AccountIdentity:       "acct-1",
		Destination:           "prod.example",
		Content:               []wireArtifactRef{},
		NotBefore:             e.clock.Now(),
		ExpiresAt:             e.clock.Now().Add(time.Hour),
		Preconditions:         json.RawMessage(`{}`),
		ConfigurationRevision: 1,
		Parameters:            json.RawMessage(`{}`),
		CostBound:             wireMoney{Currency: "USD", MicroUnit: 0},
	}
}

// ---------- operation wrappers ----------

// check runs _policy.check and returns the full decision.
func (e *testEnv) check(scope contract.Scope, capability string, action *wireAction, digest string) wirePolicyResult {
	e.t.Helper()
	payload := e.mustOK(opCheck, checkInput{
		Scope: scope, Capability: capability, Action: action, CandidateDigest: digest,
	})
	var out policyResultBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// explain runs policy.explain for one exact action.
func (e *testEnv) explain(scope contract.Scope, action wireAction) explainBody {
	e.t.Helper()
	payload := e.mustOK(opPolicyExplain, explainInput{Scope: scope, Action: action})
	var out explainBody
	e.decode(payload.Data, &out)
	return out
}

// propose runs autonomy.propose and returns the qualification.
func (e *testEnv) propose(actor contract.Actor, worker contract.ID, rule wireRef, evidence []contract.ID) wireQualification {
	e.t.Helper()
	payload := e.mustOKAs(actor, e.scope, opAutonomyPropose, autonomyProposeInput{
		Scope: e.scope, WorkerID: worker, Rule: rule, EvidenceIDs: evidence,
	})
	var out qualificationBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// evaluate runs autonomy.evaluate and returns the qualification.
func (e *testEnv) evaluate(actor contract.Actor, id contract.ID, version contract.Version) wireQualification {
	e.t.Helper()
	payload := e.mustOKAs(actor, e.scope, opAutonomyEvaluate, autonomyEvaluateInput{
		Scope: e.scope, ID: id, ExpectedVersion: version,
	})
	var out qualificationBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// restrict runs autonomy.restrict and returns the qualification.
func (e *testEnv) restrict(actor contract.Actor, id contract.ID, version contract.Version, reason string, evidence []contract.ID) wireQualification {
	e.t.Helper()
	payload := e.mustOKAs(actor, e.scope, opAutonomyRestrict, autonomyRestrictInput{
		Scope: e.scope, ID: id, ExpectedVersion: version, Reason: reason, EvidenceIDs: evidence,
	})
	var out qualificationBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// demote runs autonomy.demote and returns the qualification.
func (e *testEnv) demote(id contract.ID, version contract.Version, reason string, evidence []contract.ID) wireQualification {
	e.t.Helper()
	payload := e.mustOK(opAutonomyDemote, autonomyRestrictInput{
		Scope: e.scope, ID: id, ExpectedVersion: version, Reason: reason, EvidenceIDs: evidence,
	})
	var out qualificationBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// getPolicy runs policy.get and returns the wire resource.
func (e *testEnv) getPolicy(scope contract.Scope, id contract.ID) wirePolicy {
	e.t.Helper()
	payload := e.mustOK(opPolicyGet, policyGetInput{Scope: scope, ID: id})
	var out policyBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// getQualification runs autonomy.qualification.get.
func (e *testEnv) getQualification(scope contract.Scope, id contract.ID) wireQualification {
	e.t.Helper()
	payload := e.mustOK(opAutonomyQualGet, qualificationGetInput{Scope: scope, ID: id})
	var out qualificationBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// stagePolicy runs policy.create and returns the staged body.
func (e *testEnv) stagePolicy(actor contract.Actor, in policyCreateInput) stagedPolicyBody {
	e.t.Helper()
	payload := e.mustOKAs(actor, e.scope, opPolicyCreate, in)
	var out stagedPolicyBody
	e.decode(payload.Data, &out)
	return out
}

// stageRule runs autonomy.rule.create and returns the staged body.
func (e *testEnv) stageRule(actor contract.Actor, in ruleCreateInput) stagedRuleBody {
	e.t.Helper()
	payload := e.mustOKAs(actor, e.scope, opRuleCreate, in)
	var out stagedRuleBody
	e.decode(payload.Data, &out)
	return out
}

// validate runs _policy.validate and returns the validation result.
func (e *testEnv) validate(changes ...wireChange) wireValidation {
	e.t.Helper()
	payload := e.mustOK(opValidate, candidateEnvelope{Candidate: candidateInput{
		PlanID:          e.ids.New(),
		BaseRevision:    1,
		CandidateDigest: candidateDigest("validate"),
		Changes:         changes,
		Dependencies:    []wireRef{},
	}})
	var out validationBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// diagCodes returns the diagnostic codes of one validation result.
func diagCodes(v wireValidation) []string {
	codes := make([]string, 0, len(v.Diagnostics))
	for _, d := range v.Diagnostics {
		codes = append(codes, d.Code)
	}
	return codes
}

// promoteCalls decodes every recorded _identity.promote call.
func (e *testEnv) promoteCalls() []promoteCallInput {
	e.t.Helper()
	calls := e.ports.callsOf("_identity.promote")
	out := make([]promoteCallInput, 0, len(calls))
	for _, c := range calls {
		var in promoteCallInput
		if err := json.Unmarshal(c.Input, &in); err != nil {
			e.t.Fatalf("decode promote call: %v", err)
		}
		out = append(out, in)
	}
	return out
}

// restrictCalls decodes every recorded _identity.restrict call.
func (e *testEnv) restrictCalls() []restrictCallInput {
	e.t.Helper()
	calls := e.ports.callsOf("_identity.restrict")
	out := make([]restrictCallInput, 0, len(calls))
	for _, c := range calls {
		var in restrictCallInput
		if err := json.Unmarshal(c.Input, &in); err != nil {
			e.t.Fatalf("decode restrict call: %v", err)
		}
		out = append(out, in)
	}
	return out
}

// contains reports whether substr occurs in s.
func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && indexOf(s, substr) >= 0)
}

// indexOf returns the first offset of substr in s, or -1.
func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
