package effects

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
// exact bodies this package decodes (identity, configuration, policy,
// reviews, accounting, connections, tasks, artifacts, execution jobs) with
// injectable state and faults, recording every invocation for behavioral
// assertions.

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

// fakePorts serves every peer operation the handlers call, with injectable
// state and faults, recording every invocation.
type fakePorts struct {
	mu    sync.Mutex
	ids   *seqIDs
	calls []contract.Invocation
	fail  map[string]*contract.Fault

	principalRevoked   bool
	restrictions       []string
	configRevision     int64
	policyDecision     string
	policyReasons      []string
	policyRequirements []wireDecisionRequirement
	reviewEligible     bool
	reviewDecision     *wireDecision
	connState          string
	connValidUntil     *time.Time
	taskRootID         *contract.ID
	artifactStates     map[contract.ID]string
	artifactDigests    map[contract.ID]contract.Digest
	artifactsOmit      map[contract.ID]bool
	reservationOp      *contract.ID
}

func newFakePorts(ids *seqIDs) *fakePorts {
	return &fakePorts{
		ids:             ids,
		fail:            map[string]*contract.Fault{},
		configRevision:  7,
		policyDecision:  policyAllow,
		connState:       connStateValid,
		artifactStates:  map[contract.ID]string{},
		artifactDigests: map[contract.ID]contract.Digest{},
		artifactsOmit:   map[contract.ID]bool{},
	}
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	p.calls = append(p.calls, inv)
	injected := p.fail[inv.Operation]
	revoked, restrictions := p.principalRevoked, p.restrictions
	revision, decision := p.configRevision, p.policyDecision
	reasons := append([]string{}, p.policyReasons...)
	requirements := append([]wireDecisionRequirement{}, p.policyRequirements...)
	eligible, approved := p.reviewEligible, p.reviewDecision
	connState, connValidUntil := p.connState, p.connValidUntil
	taskRoot := p.taskRootID
	artifactStates := make(map[contract.ID]string, len(p.artifactStates))
	for id, st := range p.artifactStates {
		artifactStates[id] = st
	}
	artifactDigests := make(map[contract.ID]contract.Digest, len(p.artifactDigests))
	for id, d := range p.artifactDigests {
		artifactDigests[id] = d
	}
	artifactsOmit := make(map[contract.ID]bool, len(p.artifactsOmit))
	for id, v := range p.artifactsOmit {
		artifactsOmit[id] = v
	}
	reservationOp := p.reservationOp
	p.mu.Unlock()
	if injected != nil {
		return contract.Payload{}, injected
	}

	var body any
	switch inv.Operation {
	case opIdentityAuthority:
		var in identityAuthorityInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = authorityBody{Resource: wireAuthority{
			Principal:    wirePrincipal{ID: in.PrincipalID, Revoked: revoked},
			Restrictions: restrictions,
		}}
	case opConfigSnapshot:
		var in configurationSnapshotInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		snap := snapshotBody{}
		snap.Resource.Scope = in.Scope
		snap.Resource.Revision = revision
		body = snap
	case opPolicyCheck:
		body = policyResultBody{Resource: wirePolicyResult{
			Decision: decision, Reasons: reasons, Requirements: requirements,
		}}
	case opReviewsEnsure:
		var in reviewsEnsureInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = reviewResourceBody{Resource: wireReview{
			ID: p.ids.New(), Version: 1, Scope: in.Scope, ActionDigest: in.Requirement.ActionDigest,
			Preview: in.Action, Requirement: in.Requirement, ProposerID: p.ids.New(),
			State: "pending",
		}}
	case opReviewsCheck:
		body = reviewsCheckBody{Eligible: eligible, Decision: approved}
	case opAccountingInspect:
		var in accountingInspectInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = inspectBody{
			Limits: wireLimits{
				Currency: "USD", SpendMicroUnits: 1000000, Concurrency: 1, ModelSteps: 100,
				ChildCount: 10, DelegationDepth: 2, AttemptSeconds: 600,
				RootDeadline: time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC),
			},
			Usage: wireUsage{Currency: "USD"},
		}
	case opAccountingReserve:
		var in accountingReserveInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		operationID := in.OperationID
		if reservationOp != nil {
			operationID = *reservationOp
		}
		body = reservationResourceBody{Resource: wireReservation{
			ID: p.ids.New(), Version: 1, Scope: in.Scope, RootTaskID: in.RootTaskID,
			OperationID: operationID, Amount: in.Amount, State: "reserved",
		}}
	case opAccountingSettle:
		var in accountingSettleInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = reservationResourceBody{Resource: wireReservation{
			ID: in.ReservationID, Version: in.ExpectedVersion + 1, State: "settled",
		}}
	case opConnectionsResolve:
		var in connectionsResolveInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = resolveBody{
			Connection: wireConnection{
				ID: in.Connection.ID, Version: in.Connection.Version,
				CredentialRef: "conn-ref-test", ValidationState: connState,
				ValidUntil: connValidUntil,
			},
			Tool: wireTool{
				ID: in.Tool.ID, Version: in.Tool.Version, Name: "test-tool",
				Effect: "external_mutation", CostBound: wireMoney{Currency: "USD", MicroUnits: 100},
				TimeoutSeconds: 60, Idempotency: "authoritative_nonexecution", Adapter: "test-adapter",
			},
		}
	case opConnectionsValidationRecord:
		var in connectionsValidationRecordInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = connectionResourceBody{Resource: wireConnection{
			ID: in.ConnectionID, Version: in.ExpectedVersion + 1,
			CredentialRef: "conn-ref-test", ValidationState: connState,
		}}
	case opTasksSnapshot:
		var in tasksSnapshotInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		task := taskBody{}
		task.Resource.ID = in.ID
		task.Resource.Version = 3
		task.Resource.Scope = in.Scope
		task.Resource.RootID = taskRoot
		task.Resource.State = "running"
		body = task
	case opArtifactsMetadata:
		var in artifactsMetadataInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		artifacts := make([]wireArtifactState, 0, len(in.Artifacts))
		for _, ref := range in.Artifacts {
			if artifactsOmit[ref.ID] {
				continue
			}
			state, ok := artifactStates[ref.ID]
			if !ok {
				state = artifactStateAvailable
			}
			digest := ref.Digest
			if override, ok := artifactDigests[ref.ID]; ok {
				digest = override
			}
			artifacts = append(artifacts, wireArtifactState{ID: ref.ID, Digest: digest, State: state})
		}
		body = artifactsMetadataBody{Artifacts: artifacts}
	case opExecutionJobCreate:
		var in executionJobCreateInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = jobResourceBody{Resource: wireJob{
			ID: p.ids.New(), Version: 1, Kind: "reconcile", State: "pending",
			Requirements: []wireRequirement{}, Owner: in.Owner, Operation: in.Operation,
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

// opsCalled returns every peer operation in call order.
func (p *fakePorts) opsCalled() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.calls))
	for _, c := range p.calls {
		out = append(out, c.Operation)
	}
	return out
}

// resetCalls forgets every recorded invocation, so a test can bracket one
// handler call and assert exactly the peers it called.
func (p *fakePorts) resetCalls() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = nil
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

// failOp injects one fault the fake ports return for the next call of the
// named operation, so tests can drive peer-failure paths.
func (p *fakePorts) failOp(op string, f *contract.Fault) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail[op] = f
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
	worker  contract.ID
	scope   contract.Scope
}

// newEnv opens a fresh database, migrates the effects owner and wires
// default identity: one installation scope with a service actor and one
// started controller generation, so dispatch claims carry generation 1.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "effects-test.db")})
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
		t.Fatalf("effects.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate effects: %v", err)
	}
	if _, err := db.StartGeneration(ctx); err != nil {
		t.Fatalf("start generation: %v", err)
	}
	env.install = env.ids.New()
	env.owner = env.ids.New()
	env.org = env.ids.New()
	env.worker = env.ids.New()
	env.actor = contract.Actor{PrincipalID: env.owner, Kind: contract.KindService}
	env.scope = contract.Scope{InstallationID: env.install}
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

// callVersion drives one operation with an explicit protocol version.
func (e *testEnv) callVersion(op string, version int64, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: version, Input: raw})
		payload = p
		return err
	})
	return payload, err
}

// callReadOnly drives one operation against a read-only unit.
func (e *testEnv) callReadOnly(op string, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		payload = p
		return err
	})
	return payload, err
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

// faultFrom extracts a *contract.Fault from an error, if present.
func faultFrom(err error) *contract.Fault {
	var f *contract.Fault
	if errors.As(err, &f) {
		return f
	}
	return nil
}

// expectFault runs an operation and requires a fault with the exact code.
func (e *testEnv) expectFault(op string, in any, code string) *contract.Fault {
	e.t.Helper()
	return e.expectFaultAs(e.actor, e.scope, op, in, code)
}

// expectFaultOnVersion runs one operation with an explicit protocol version
// and requires a fault with the exact code.
func (e *testEnv) expectFaultOnVersion(op string, version int64, in any, code string) *contract.Fault {
	e.t.Helper()
	payload, err := e.callVersion(op, version, in)
	var f *contract.Fault
	if err != nil {
		errors.As(err, &f)
	}
	if f == nil {
		f = payload.Error
	}
	if f == nil {
		e.t.Fatalf("%s v%d: expected %s fault, got completed payload", op, version, code)
	}
	if f.Code != code {
		e.t.Fatalf("%s v%d: fault %s (%s), want %s", op, version, f.Code, f.Message, code)
	}
	return f
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

// generation returns the persisted controller generation.
func (e *testEnv) generation() int64 {
	e.t.Helper()
	gen, err := e.db.Generation(e.ctx)
	if err != nil {
		e.t.Fatalf("generation: %v", err)
	}
	return gen
}

// write runs fn inside one write transaction on the env's scope.
func (e *testEnv) write(fn func(unit contract.Unit) error) error {
	return e.db.Write(e.ctx, e.actor, e.scope, fn)
}

// ---------- fixture builders ----------

// action builds one schema-valid action in the env's scope with a one-hour
// validity window around the pinned instant.
func (e *testEnv) action() wireAction {
	return wireAction{
		Scope:                 unitScope(e.scope),
		Tool:                  wireRef{ID: e.ids.New(), Version: 1},
		Connection:            wireRef{ID: e.ids.New(), Version: 1},
		AccountIdentity:       "acct-public-example",
		Destination:           "https://api.example.com/v1/items",
		Content:               []wireArtifactRef{},
		NotBefore:             e.clock.Now().Add(-time.Hour),
		ExpiresAt:             e.clock.Now().Add(time.Hour),
		Preconditions:         json.RawMessage(`{}`),
		ConfigurationRevision: 7,
		Parameters:            json.RawMessage(`{}`),
		CostBound:             wireMoney{Currency: "USD", MicroUnits: 1000},
	}
}

// observation builds one schema-valid observation with a disposition.
func (e *testEnv) observation(disposition string) wireObservation {
	return wireObservation{
		Disposition: disposition,
		Evidence:    json.RawMessage(`{"reason":"provider-reported"}`),
		Usage: wireUsage{
			Currency: "USD", Spent: 100, Reserved: 0, Estimated: 1000, Unknown: 0,
			Advisory: false,
		},
		ProviderReference: "provider-ref-1",
	}
}

// approvedDecision builds an approve decision bound to one action digest.
func approvedDecision(digest string) *wireDecision {
	return &wireDecision{
		ID:            "00000000-0000-4000-8000-0000000000aa",
		ReviewID:      "00000000-0000-4000-8000-0000000000ab",
		ReviewVersion: 1,
		ActionDigest:  digest,
		ReviewerID:    "00000000-0000-4000-8000-0000000000ac",
		Decision:      decisionApprove,
		At:            time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Reason:        "operator approved",
	}
}

// ---------- operation wrappers ----------

// prepareOp runs _effects.prepare and returns the wire operation.
func (e *testEnv) prepareOp(scope contract.Scope, action wireAction, source contract.ID) wireOperation {
	e.t.Helper()
	payload := e.mustOK(opPrepare, prepareInput{Scope: unitScope(scope), Action: action, SourceID: source})
	var out operationResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// proposeOp runs operation.propose and returns the wire operation.
func (e *testEnv) proposeOp(scope contract.Scope, action wireAction) wireOperation {
	e.t.Helper()
	payload := e.mustOK(opPropose, proposeInput{Scope: unitScope(scope), Action: action})
	var out operationResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// admitOp runs _effects.admit and returns the wire operation.
func (e *testEnv) admitOp(id contract.ID, version int64) wireOperation {
	e.t.Helper()
	payload := e.mustOK(opAdmit, admitInput{OperationID: id, ExpectedVersion: version})
	var out operationResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// claimOp runs _effects.claim and returns the dispatch intent.
func (e *testEnv) claimOp(opID, attemptID contract.ID, gen int64) wireDispatch {
	e.t.Helper()
	payload := e.mustOK(opClaim, claimInput{OperationID: opID, AttemptID: attemptID, Generation: gen})
	var out dispatchResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// recordOp runs _effects.record and returns the wire operation.
func (e *testEnv) recordOp(opID, attemptID contract.ID, gen int64, obs wireObservation) wireOperation {
	e.t.Helper()
	payload := e.mustOK(opRecord, recordInput{
		OperationID: opID, AttemptID: attemptID, Generation: gen, Observation: obs,
	})
	var out operationResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// getOp runs operation.get and returns the wire operation.
func (e *testEnv) getOp(scope contract.Scope, id contract.ID) wireOperation {
	e.t.Helper()
	payload := e.mustOK(opGet, getOperationInput{Scope: unitScope(scope), ID: id})
	var out operationResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// pendingOp runs _effects.pending and returns the listed operations.
func (e *testEnv) pendingOp(limit int64) []wireOperation {
	e.t.Helper()
	payload := e.mustOK(opPending, pendingInput{Limit: limit})
	var out pendingOutput
	e.decode(payload.Data, &out)
	return out.Operations
}

// listOps runs operation.list and returns the page and next cursor.
func (e *testEnv) listOps(scope contract.Scope, cursor *string, limit *int64, filter *operationFilter) ([]wireOperation, *string) {
	e.t.Helper()
	payload := e.mustOK(opList, listOperationsInput{Scope: unitScope(scope), Cursor: cursor, Limit: limit, Filter: filter})
	var out operationListOutput
	e.decode(payload.Data, &out)
	return out.Items, payload.NextCursor
}

// reconcileOp runs operation.reconcile and returns the job resource.
func (e *testEnv) reconcileOp(scope contract.Scope, id contract.ID, version int64) wireJob {
	e.t.Helper()
	payload := e.mustOK(opReconcile, reconcileInput{Scope: unitScope(scope), ID: id, ExpectedVersion: version})
	var out jobResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// linkedProposeOp runs operation.compensation.propose or
// operation.replacement.propose and returns the wire operation.
func (e *testEnv) linkedProposeOp(op string, scope contract.Scope, id contract.ID, version int64, action wireAction) wireOperation {
	e.t.Helper()
	payload := e.mustOK(op, linkedProposeInput{
		Scope: unitScope(scope), ID: id, ExpectedVersion: version, Action: action,
	})
	var out operationResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// ---------- flow helpers ----------

// staged prepares one allowed action and returns the wire operation.
func (e *testEnv) staged() wireOperation {
	e.t.Helper()
	return e.prepareOp(e.scope, e.action(), e.ids.New())
}

// admitted prepares and admits one allowed operation and returns the wire
// operation and its single attempt id.
func (e *testEnv) admitted() (wireOperation, contract.ID) {
	e.t.Helper()
	o := e.staged()
	o = e.admitOp(o.ID, o.Version)
	if len(o.AttemptIDs) != 1 {
		e.t.Fatalf("admitted operation %s carries %d attempts, want 1", o.ID, len(o.AttemptIDs))
	}
	return o, o.AttemptIDs[0]
}

// dispatching prepares, admits and claims one operation, returning the
// operation and dispatch intent.
func (e *testEnv) dispatched() (wireOperation, wireDispatch) {
	e.t.Helper()
	o, attempt := e.admitted()
	d := e.claimOp(o.ID, attempt, e.generation())
	return o, d
}

// settleCalls decodes every recorded _accounting.settle call.
func (e *testEnv) settleCalls() []accountingSettleInput {
	e.t.Helper()
	calls := e.ports.callsOf(opAccountingSettle)
	out := make([]accountingSettleInput, 0, len(calls))
	for _, c := range calls {
		var in accountingSettleInput
		if err := json.Unmarshal(c.Input, &in); err != nil {
			e.t.Fatalf("decode settle call: %v", err)
		}
		out = append(out, in)
	}
	return out
}

// reserveCalls decodes every recorded _accounting.reserve call.
func (e *testEnv) reserveCalls() []accountingReserveInput {
	e.t.Helper()
	calls := e.ports.callsOf(opAccountingReserve)
	out := make([]accountingReserveInput, 0, len(calls))
	for _, c := range calls {
		var in accountingReserveInput
		if err := json.Unmarshal(c.Input, &in); err != nil {
			e.t.Fatalf("decode reserve call: %v", err)
		}
		out = append(out, in)
	}
	return out
}

// jobCreateCalls decodes every recorded _execution.job.create call.
func (e *testEnv) jobCreateCalls() []executionJobCreateInput {
	e.t.Helper()
	calls := e.ports.callsOf(opExecutionJobCreate)
	out := make([]executionJobCreateInput, 0, len(calls))
	for _, c := range calls {
		var in executionJobCreateInput
		if err := json.Unmarshal(c.Input, &in); err != nil {
			e.t.Fatalf("decode job create call: %v", err)
		}
		out = append(out, in)
	}
	return out
}

// validationRecordCalls decodes every recorded _connections.validation.record
// call.
func (e *testEnv) validationRecordCalls() []connectionsValidationRecordInput {
	e.t.Helper()
	calls := e.ports.callsOf(opConnectionsValidationRecord)
	out := make([]connectionsValidationRecordInput, 0, len(calls))
	for _, c := range calls {
		var in connectionsValidationRecordInput
		if err := json.Unmarshal(c.Input, &in); err != nil {
			e.t.Fatalf("decode validation record call: %v", err)
		}
		out = append(out, in)
	}
	return out
}

// ---------- store-backed reads ----------

// mustFindOperation loads one operation row and requires it to exist.
func (e *testEnv) mustFindOperation(id contract.ID) *operationRow {
	e.t.Helper()
	var out *operationRow
	if err := e.write(func(unit contract.Unit) error {
		var err error
		out, err = loadOperation(e.ctx, unit, id)
		return err
	}); err != nil {
		e.t.Fatalf("load operation %s: %v", id, err)
	}
	if out == nil {
		e.t.Fatalf("operation %s not found", id)
	}
	return out
}

// attemptsOf lists the attempts of one operation.
func (e *testEnv) attemptsOf(operationID contract.ID) []*attemptRow {
	e.t.Helper()
	var out []*attemptRow
	if err := e.write(func(unit contract.Unit) error {
		var err error
		out, err = listAttempts(e.ctx, unit, operationID)
		return err
	}); err != nil {
		e.t.Fatalf("list attempts: %v", err)
	}
	return out
}

// claimOf loads one attempt's claim, or nil.
func (e *testEnv) claimOf(attemptID contract.ID) *claimRow {
	e.t.Helper()
	var out *claimRow
	if err := e.write(func(unit contract.Unit) error {
		var err error
		out, err = loadClaim(e.ctx, unit, attemptID)
		return err
	}); err != nil {
		e.t.Fatalf("load claim: %v", err)
	}
	return out
}

// observationsOf lists every recorded observation of one operation.
func (e *testEnv) observationsOf(operationID contract.ID) []*observationRow {
	e.t.Helper()
	var out []*observationRow
	if err := e.write(func(unit contract.Unit) error {
		var err error
		out, err = listObservations(e.ctx, unit, operationID)
		return err
	}); err != nil {
		e.t.Fatalf("list observations: %v", err)
	}
	return out
}

// obligationsOf lists every obligation of one operation, open or resolved.
func (e *testEnv) obligationsOf(operationID contract.ID) []*obligationRow {
	e.t.Helper()
	var out []*obligationRow
	if err := e.write(func(unit contract.Unit) error {
		rows, err := unit.QueryContext(e.ctx,
			`SELECT id, operation_id, kind, state, detail_json, created_at, resolved_at
			 FROM effects_obligations WHERE operation_id = ? ORDER BY created_at, id`, string(operationID))
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var ob obligationRow
			var detail, created string
			var resolved *string
			if err := rows.Scan(&ob.ID, &ob.OperationID, &ob.Kind, &ob.State, &detail, &created, &resolved); err != nil {
				return err
			}
			ob.DetailJSON = detail
			if created != "" {
				c, err := parseStamp(created)
				if err != nil {
					return err
				}
				ob.CreatedAt = c
			}
			if resolved != nil && *resolved != "" {
				r, err := parseStamp(*resolved)
				if err != nil {
					return err
				}
				ob.ResolvedAt = r
			}
			out = append(out, &ob)
		}
		return rows.Err()
	}); err != nil {
		e.t.Fatalf("list obligations: %v", err)
	}
	return out
}

// openObligations returns the open obligations of one operation.
func (e *testEnv) openObligations(operationID contract.ID) []*obligationRow {
	e.t.Helper()
	var out []*obligationRow
	if err := e.write(func(unit contract.Unit) error {
		var err error
		out, err = listOpenObligationsForOperation(e.ctx, unit, operationID)
		return err
	}); err != nil {
		e.t.Fatalf("list open obligations: %v", err)
	}
	return out
}

// events lists every emitted event.
func (e *testEnv) events() []contract.Event {
	e.t.Helper()
	evs, err := e.db.Events(e.ctx, 0, 500)
	if err != nil {
		e.t.Fatalf("events: %v", err)
	}
	return evs
}
