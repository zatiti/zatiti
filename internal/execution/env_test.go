package execution

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
// clock, identities and peer ports, and helpers that drive every operation
// through Service.Handle inside storage write transactions. The fake ports
// serve the peer fixtures execution consumes — configuration snapshots,
// task contracts, accounting reservations, artifact metadata and effects
// operations — and record every call for assertions.

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

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
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

// recordedTransition is one _tasks.transition call.
type recordedTransition struct {
	TaskID    contract.ID
	State     string
	Manual    bool
	Evidence  []contract.ID
	WaitingRe string
}

// recordedSettle is one _accounting.settle call.
type recordedSettle struct {
	ReservationID contract.ID
	Usage         wireUsage
	Nonexec       bool
}

// fakePorts serves the peer fixtures per installation and task, records the
// calls handlers make, and carries injectable faults and raw errors.
type fakePorts struct {
	mu          sync.Mutex
	snapshots   map[contract.ID]*peerScopeSnapshot
	tasks       map[contract.ID]*wireTask
	artifacts   map[contract.Digest]wireArtifact
	fail        map[string]*contract.Fault
	rawFail     map[string]error
	transitions []recordedTransition
	settles     []recordedSettle
	prepared    []contract.ID
	seq         int
}

func newFakePorts() *fakePorts {
	return &fakePorts{
		snapshots: map[contract.ID]*peerScopeSnapshot{},
		tasks:     map[contract.ID]*wireTask{},
		artifacts: map[contract.Digest]wireArtifact{},
		fail:      map[string]*contract.Fault{},
		rawFail:   map[string]error{},
	}
}

func (p *fakePorts) nextID() contract.ID {
	p.seq++
	return contract.ID(fmt.Sprintf("00000000-0000-4000-8000-%012d", 900000+p.seq))
}

func (p *fakePorts) setSnapshot(install contract.ID, snap *peerScopeSnapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshots[install] = snap
}

func (p *fakePorts) setTask(task *wireTask) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tasks[task.ID] = task
}

func (p *fakePorts) setFault(op string, f *contract.Fault) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail[op] = f
}

func (p *fakePorts) setRawError(op string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rawFail[op] = err
}

func (p *fakePorts) Calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.transitions))
	for _, t := range p.transitions {
		out = append(out, string(t.TaskID)+":"+t.State)
	}
	return out
}

func (p *fakePorts) SettleCalls() []recordedSettle {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedSettle(nil), p.settles...)
}

func (p *fakePorts) PreparedOps() []contract.ID {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]contract.ID(nil), p.prepared...)
}

func (p *fakePorts) Transitions() []recordedTransition {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedTransition(nil), p.transitions...)
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	if injected := p.fail[inv.Operation]; injected != nil {
		p.mu.Unlock()
		return contract.Payload{}, injected
	}
	if raw := p.rawFail[inv.Operation]; raw != nil {
		p.mu.Unlock()
		return contract.Payload{}, raw
	}
	var body any
	switch inv.Operation {
	case peerConfigSnapshot:
		var in struct {
			Scope contract.Scope `json:"scope"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		snap, ok := p.snapshots[in.Scope.InstallationID]
		if !ok {
			snap = &peerScopeSnapshot{Scope: in.Scope, Revision: 1}
		}
		body = struct {
			Resource peerScopeSnapshot `json:"resource"`
		}{*snap}
	case peerTasksSnapshot:
		var in struct {
			Scope contract.Scope `json:"scope"`
			ID    contract.ID    `json:"id"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		task, ok := p.tasks[in.ID]
		if !ok {
			p.mu.Unlock()
			return contract.Payload{}, notFound("task %s does not exist", in.ID)
		}
		body = struct {
			Resource wireTask `json:"resource"`
		}{*task}
	case peerTasksTransit:
		var in map[string]any
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		taskID := contract.ID(in["task_id"].(string))
		state := in["state"].(string)
		task, ok := p.tasks[taskID]
		if !ok {
			p.mu.Unlock()
			return contract.Payload{}, notFound("task %s does not exist", taskID)
		}
		task.State = state
		task.Version++
		rec := recordedTransition{TaskID: taskID, State: state}
		if manual, ok := in["manual"].(bool); ok {
			rec.Manual = manual
		}
		if reason, ok := in["waiting_reason"].(string); ok {
			rec.WaitingRe = reason
		}
		if evidence, ok := in["evidence_ids"].([]any); ok {
			for _, ev := range evidence {
				rec.Evidence = append(rec.Evidence, contract.ID(ev.(string)))
			}
		}
		p.transitions = append(p.transitions, rec)
		body = struct {
			Resource wireTask `json:"resource"`
		}{*task}
	case peerAccountReserve:
		p.mu.Unlock()
		res := peerReservation{ID: p.nextID(), Version: 1, State: "held"}
		p.mu.Lock()
		defer p.mu.Unlock()
		raw, err := json.Marshal(struct {
			Resource peerReservation `json:"resource"`
		}{res})
		if err != nil {
			return contract.Payload{}, err
		}
		return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
	case peerAccountSettle:
		var in map[string]any
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		rec := recordedSettle{ReservationID: contract.ID(in["reservation_id"].(string))}
		if nonexec, ok := in["authoritative_nonexecution"].(bool); ok {
			rec.Nonexec = nonexec
		}
		if usage, ok := in["usage"].(map[string]any); ok {
			rawUsage, _ := json.Marshal(usage)
			_ = json.Unmarshal(rawUsage, &rec.Usage)
		}
		p.settles = append(p.settles, rec)
		res := peerReservation{ID: rec.ReservationID, Version: 2, State: "settled"}
		body = struct {
			Resource peerReservation `json:"resource"`
		}{res}
	case peerArtifactsMeta:
		var in struct {
			Scope     contract.Scope    `json:"scope"`
			Artifacts []wireArtifactRef `json:"artifacts"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		found := []wireArtifact{}
		for _, ref := range in.Artifacts {
			if a, ok := p.artifacts[ref.Digest]; ok {
				found = append(found, a)
			}
		}
		body = map[string]any{"artifacts": found}
	case peerTasksReady:
		body = map[string]any{"items": []any{}}
	case "_effects.prepare":
		var in map[string]any
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		id := p.nextID()
		p.prepared = append(p.prepared, id)
		body = struct {
			Resource struct {
				ID contract.ID `json:"id"`
			} `json:"resource"`
		}{struct {
			ID contract.ID `json:"id"`
		}{id}}
	default:
		p.mu.Unlock()
		return contract.Payload{}, &contract.Fault{
			Code: contract.CodeInternalError, Message: "fake ports: unexpected peer call " + inv.Operation,
		}
	}
	p.mu.Unlock()
	raw, err := json.Marshal(body)
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
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
	other   contract.Actor
	install contract.ID
	org     contract.ID
	project contract.ID
	scope   contract.Scope
}

// newEnv opens a fresh database and migrates the execution owner.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "execution-test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env := &testEnv{
		t: t, ctx: ctx, db: db,
		ports: newFakePorts(),
		clock: &fakeClock{now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		ids:   &seqIDs{},
	}
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids, Ports: env.ports})
	if err != nil {
		t.Fatalf("execution.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate execution: %v", err)
	}
	env.install = env.ids.New()
	env.org = env.ids.New()
	env.project = env.ids.New()
	env.actor = contract.Actor{PrincipalID: env.ids.New(), Kind: contract.KindService}
	env.other = contract.Actor{PrincipalID: env.ids.New(), Kind: contract.KindService}
	env.scope = contract.Scope{InstallationID: env.install, OrganizationID: env.org, ProjectID: env.project}
	return env
}

// callAsActor runs one operation inside a write transaction as the given
// actor on the given scope.
func (e *testEnv) callAsActor(actor contract.Actor, scope contract.Scope, op string, in any) (contract.Payload, error) {
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

func (e *testEnv) callAs(scope contract.Scope, op string, in any) (contract.Payload, error) {
	return e.callAsActor(e.actor, scope, op, in)
}

func (e *testEnv) call(op string, in any) (contract.Payload, error) {
	return e.callAs(e.scope, op, in)
}

// mustOK runs an operation and requires a completed payload.
func (e *testEnv) mustOK(op string, in any) contract.Payload {
	e.t.Helper()
	payload, err := e.call(op, in)
	if err != nil {
		e.t.Fatalf("%s failed: %v", op, err)
	}
	if payload.Status != contract.StatusCompleted {
		e.t.Fatalf("%s status %q, want completed (error %v)", op, payload.Status, payload.Error)
	}
	if payload.Error != nil {
		e.t.Fatalf("%s returned fault %s: %s", op, payload.Error.Code, payload.Error.Message)
	}
	return payload
}

// expectFault runs an operation and requires a fault with the exact code,
// returned either as the payload error or as the handler error.
func (e *testEnv) expectFault(op string, in any, code string) *contract.Fault {
	e.t.Helper()
	payload, err := e.call(op, in)
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

// inWrite runs fn inside a write transaction and requires success.
func (e *testEnv) inWrite(fn func(unit contract.Unit) error) {
	e.t.Helper()
	if err := e.db.Write(e.ctx, e.actor, e.scope, fn); err != nil {
		e.t.Fatalf("write transaction: %v", err)
	}
}

// ---------- fixture builders ----------

// digestA is the evidence artifact digest referenced by the profile fixture.
const digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// digestB is a second syntactically valid digest for mismatch cases.
const digestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// verifierCode is the verifier's pinned code digest fixture.
const verifierCode = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

// fixtureContent is the artifact payload the default acceptance check pins;
// the test blob store serves exactly these bytes for fixtureDigest.
var fixtureContent = []byte("zatiti execution test artifact payload")

// fixtureDigest is the pinned expected artifact digest of the default
// acceptance check: the digest of fixtureContent.
var fixtureDigest = sha256Hex(fixtureContent)

// fixtureLimits builds a full wireLimits document; every field is required
// on the wire.
func fixtureLimits(modelSteps int64) wireLimits {
	return wireLimits{
		Currency:        "USD",
		SpendMicroUnits: 1000000,
		Concurrency:     2,
		ModelSteps:      modelSteps,
		ChildCount:      4,
		DelegationDepth: 2,
		AttemptSeconds:  120,
		RootDeadline:    "2027-01-01T00:00:00Z",
	}
}

// fixtureProfile builds the artifact verifier profile document pinned in an
// acceptance contract.
func fixtureProfile() json.RawMessage {
	profile := map[string]any{
		"schema":           "zatiti.verifier-profile/v1",
		"kind":             "artifact_contract",
		"id":               "verifier-core",
		"version":          "1.0.0",
		"code_digest":      verifierCode,
		"supported_checks": []string{"presence", "digest", "json_schema"},
		"max_bytes":        1048576,
		"timeout_seconds":  60,
		"capability_evidence": map[string]any{
			"artifact":          map[string]any{"id": "00000000-0000-4000-8000-000000000099", "digest": digestA},
			"adapter_version":   "1.0.0",
			"source_revision":   "rev-1",
			"protocol_revision": "proto-1",
			"profile_digest":    verifierCode,
			"qualified_at":      "2026-01-01T00:00:00Z",
			"capabilities":      []string{"artifact_digest"},
			"limitations":       []string{"no repository checks"},
		},
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		panic(fmt.Sprintf("fixture profile: %v", err))
	}
	return raw
}

// fixtureAcceptance builds an acceptance contract with one digest check.
func fixtureAcceptance(mode string) wireAcceptance {
	return wireAcceptance{
		VerifierID:      "verifier-core",
		VerifierVersion: "1.0.0",
		SealedInputs:    []wireArtifactRef{},
		ExpectedObservations: []wireExpectedObservation{{
			CheckID:        "out-digest",
			Kind:           "artifact_digest",
			Expected:       "pass",
			ArtifactName:   "result",
			ExpectedDigest: fixtureDigest,
		}},
		Mode:             mode,
		RequiredChildIDs: []contract.ID{},
		Profile:          fixtureProfile(),
	}
}

// fixtureTask builds a schema-valid ready task for one worker.
func (e *testEnv) fixtureTask(workerID contract.ID, modelSteps int64) wireTask {
	return wireTask{
		ID:              e.ids.New(),
		Version:         1,
		Scope:           contract.Scope{InstallationID: e.install, OrganizationID: e.org, ProjectID: e.project, WorkerID: workerID},
		OwnerID:         e.ids.New(),
		WorkerID:        workerID,
		Outcome:         "artifact",
		Inputs:          []wireArtifactRef{},
		RequiredOutputs: []string{"result"},
		Acceptance:      fixtureAcceptance("independent"),
		Limits:          fixtureLimits(modelSteps),
		Dependencies:    []contract.ID{},
		State:           "ready",
	}
}

// fixtureHostedProfile builds the hosted execution profile a worker snapshot
// carries.
func fixtureHostedProfile(workerID contract.ID) *wireExecutionProfile {
	return &wireExecutionProfile{
		ID:                  workerID,
		Version:             1,
		Executor:            "hosted",
		Model:               "test-model",
		ConnectionID:        "00000000-0000-4000-8000-000000000098",
		ProviderDestination: "https://provider.invalid/v1",
		Capabilities:        []string{"model.steps"},
		CostBound:           wireMoney{Currency: "USD", MicroUnits: 1000},
		Classification:      "internal",
		ContextCapture:      "complete",
	}
}

// installWorkerSnapshot pins the configuration snapshot the ports serve for
// the env installation: the hosted worker profile, revision 1.
func (e *testEnv) installWorkerSnapshot(workerID contract.ID, profile *wireExecutionProfile) {
	limits := fixtureLimits(4)
	e.ports.setSnapshot(e.install, &peerScopeSnapshot{
		Scope:    e.scope,
		Revision: 1,
		Worker: &wireWorker{
			ID: workerID, Version: 1, OrganizationID: e.org,
			Key: "worker", Name: "Worker",
			SkillVersions: []wireRef{}, Bindings: []contract.ID{},
			Profile: profile, Limits: &limits,
		},
	})
}

// enqueueTask installs the task fixture on the ports and enqueues it,
// returning the pinned run.
func (e *testEnv) enqueueTask(workerID contract.ID, mutate func(*wireTask)) wireRun {
	e.t.Helper()
	task := e.fixtureTask(workerID, 4)
	if mutate != nil {
		mutate(&task)
	}
	e.ports.setTask(&task)
	payload := e.mustOK(opEnqueue, enqueueInput{Task: task})
	var body runBody
	e.decode(payload.Data, &body)
	return body.Resource
}

// claimRun claims the run's single attempt as the worker and returns the
// claim envelope.
func (e *testEnv) claimRun(runID, workerID contract.ID) claimBody {
	e.t.Helper()
	run := e.readRun(runID)
	payload := e.mustOK(opRunClaim, runClaimInput{
		Scope:           e.scope,
		RunID:           runID,
		WorkerID:        workerID,
		ExpectedVersion: run.Version,
		Capabilities:    []string{"model.steps"},
	})
	var body claimBody
	e.decode(payload.Data, &body)
	return body
}

// pinContext pins the request context onto the claimed attempt through the
// controller context operation.
func (e *testEnv) pinContext(attemptID contract.ID, revision contract.Version) {
	e.t.Helper()
	e.mustOK(opContext, contextInput{
		AttemptID: attemptID,
		Context: wireContext{
			AttemptID:             attemptID,
			Artifact:              wireArtifactRef{ID: e.ids.New(), Digest: fixtureDigest},
			ConfigurationRevision: revision,
			SourceArtifacts:       []wireArtifactRef{},
			Capture:               "complete",
		},
	})
}

// readAttempt loads one attempt row directly.
func (e *testEnv) readAttempt(id contract.ID) *attemptRow {
	e.t.Helper()
	var a *attemptRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		a, err = loadAttempt(e.ctx, unit, id)
		return err
	})
	return a
}

// readRun loads one run row directly.
func (e *testEnv) readRun(id contract.ID) *runRow {
	e.t.Helper()
	var r *runRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		r, err = loadRun(e.ctx, unit, id)
		return err
	})
	return r
}

// readObligations reads a resource's unresolved obligations directly.
func (e *testEnv) readObligations(column string, id contract.ID) []wireRequirement {
	e.t.Helper()
	var out []wireRequirement
	e.inWrite(func(unit contract.Unit) error {
		var err error
		out, err = unresolvedObligations(e.ctx, unit, column, id)
		return err
	})
	return out
}

// readOpenOperation reads the attempt's newest open operation record, or nil.
func (e *testEnv) readOpenOperation(attemptID contract.ID) *operationRow {
	e.t.Helper()
	var o *operationRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		o, err = openOperationOf(e.ctx, unit, attemptID)
		return err
	})
	return o
}

// readOperation reads one operation record by id.
func (e *testEnv) readOperation(id contract.ID) *operationRow {
	e.t.Helper()
	var o *operationRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		o, err = loadOperationRecord(e.ctx, unit, id)
		return err
	})
	return o
}

// readVerificationJob reads one attempt's verification job directly.
func (e *testEnv) readVerificationJob(attemptID contract.ID) *verificationJobRow {
	e.t.Helper()
	var v *verificationJobRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		v, err = loadVerificationJob(e.ctx, unit, attemptID)
		return err
	})
	return v
}

// readLease reads one lease row directly.
func (e *testEnv) readLease(id contract.ID) *leaseRow {
	e.t.Helper()
	var l *leaseRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		l, err = loadLease(e.ctx, unit, id)
		return err
	})
	return l
}

// readGate reads one worker pause gate directly.
func (e *testEnv) readGate(workerID contract.ID) *gateRow {
	e.t.Helper()
	var g *gateRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		g, err = loadGate(e.ctx, unit, workerID)
		return err
	})
	return g
}

// eventRow is one emitted event read back from the storage outbox.
type eventRow struct {
	Kind            string
	ResourceID      string
	ResourceVersion int64
	Data            string
}

// readEvents reads every emitted event in sequence order.
func (e *testEnv) readEvents() []eventRow {
	e.t.Helper()
	var rows []eventRow
	e.inWrite(func(unit contract.Unit) error {
		rs, err := unit.QueryContext(e.ctx, `SELECT kind, resource_id, resource_version, data
			FROM storage_events ORDER BY sequence`)
		if err != nil {
			return err
		}
		defer func() { _ = rs.Close() }()
		for rs.Next() {
			var ev eventRow
			var data []byte
			if err := rs.Scan(&ev.Kind, &ev.ResourceID, &ev.ResourceVersion, &data); err != nil {
				return err
			}
			ev.Data = string(data)
			rows = append(rows, ev)
		}
		return rs.Err()
	})
	return rows
}

// faultCode returns the code of a contract fault error, or "" when err is
// not a fault.
func faultCode(err error) string {
	var f *contract.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

// mustMarshal marshals a test input or fails the test.
func mustMarshal(t *testing.T, in any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return raw
}
