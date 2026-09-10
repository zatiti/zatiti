package tasks

import (
	"context"
	"database/sql"
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
// clock, ids and the eight peer owner operations, and helpers that drive
// every operation through Service.Handle inside storage transactions.

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

// peerRun is the fake execution run record for one enqueue.
type peerRun struct {
	ID      contract.ID
	Version int64
	TaskID  contract.ID
	State   string
}

// fakePorts serves the eight declared peer operations with in-memory
// registries and injectable decisions and faults, recording every call.
type fakePorts struct {
	mu sync.Mutex

	calls []contract.Invocation
	next  int

	// _configuration.snapshot: worker home registry. A worker absent from
	// the map is active by default; missingWorkers forces a not-found home.
	workers       map[contract.ID]string
	missingWorker bool

	// _artifacts.metadata: artifact registry. An unregistered reference
	// echoes the requested digest as an available artifact.
	artifacts map[contract.ID]peerArtifact

	// _accounting: intersected envelope, usage and reservation record.
	inspectLimits wireLimits
	inspectUsage  struct {
		Currency string
		Spent    int64
		Reserved int64
	}
	reserveFault *contract.Fault
	reserveCalls []peerReserveIn
	inspectFault *contract.Fault

	// _policy.check and _policy.invalidate.
	policyDecision string
	policyReasons  []string
	invalidations  []peerPolicyInvalidateIn

	// _reviews.check eligibility.
	reviewsEligible bool

	// peerFaults injects a fault payload for one peer operation, decoded
	// exactly as a real peer fault would be.
	peerFaults map[string]*contract.Fault

	// _execution.enqueue: run registry keyed by task/version.
	enqueueRuns map[string]*peerRun
}

func newFakePorts() *fakePorts {
	return &fakePorts{
		workers:         map[contract.ID]string{},
		artifacts:       map[contract.ID]peerArtifact{},
		policyDecision:  "allow",
		reviewsEligible: true,
		peerFaults:      map[string]*contract.Fault{},
		enqueueRuns:     map[string]*peerRun{},
		inspectLimits: wireLimits{
			Currency: "USD", SpendMicroUnits: 1_000_000, Concurrency: 8,
			ModelSteps: 1000, ChildCount: 8, DelegationDepth: 8, AttemptSeconds: 3600,
			RootDeadline: "2027-09-10T12:00:00Z",
		},
	}
}

func (p *fakePorts) Call(_ context.Context, _ contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, inv)
	if f, ok := p.peerFaults[inv.Operation]; ok {
		return contract.Payload{Error: f}, nil
	}
	var body any
	switch inv.Operation {
	case "_configuration.snapshot":
		var in peerSnapshotIn
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = p.snapshotBody(in.Scope)
	case "_artifacts.metadata":
		var in peerMetadataIn
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = p.metadataBody(in)
	case "_accounting.inspect":
		if p.inspectFault != nil {
			return contract.Payload{}, p.inspectFault
		}
		body = peerInspectOut{Limits: p.inspectLimits, Usage: struct {
			Currency  string `json:"currency"`
			Spent     int64  `json:"spent"`
			Reserved  int64  `json:"reserved"`
			Estimated int64  `json:"estimated"`
			Unknown   int64  `json:"unknown"`
			Advisory  bool   `json:"advisory"`
		}{Currency: p.inspectUsage.Currency, Spent: p.inspectUsage.Spent, Reserved: p.inspectUsage.Reserved}}
	case "_accounting.reserve":
		var in peerReserveIn
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		if p.reserveFault != nil {
			return contract.Payload{}, p.reserveFault
		}
		p.reserveCalls = append(p.reserveCalls, in)
		p.next++
		body = peerReserveOut{Resource: struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
			State   string      `json:"state"`
		}{ID: contract.ID(fmt.Sprintf("00000000-0000-4000-8000-f%010d", p.next)), Version: 1, State: "reserved"}}
	case "_policy.check":
		body = peerPolicyCheckOut{Resource: struct {
			Decision     string   `json:"decision"`
			Reasons      []string `json:"reasons"`
			Requirements []any    `json:"requirements"`
		}{Decision: p.policyDecision, Reasons: p.policyReasons}}
	case "_policy.invalidate":
		var in peerPolicyInvalidateIn
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		p.invalidations = append(p.invalidations, in)
		body = map[string]any{}
	case "_reviews.check":
		body = peerReviewsCheckOut{Eligible: p.reviewsEligible}
	case "_execution.enqueue":
		var in peerEnqueueIn
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		key := fmt.Sprintf("%s/%d", in.Task.ID, in.Task.Version)
		run, ok := p.enqueueRuns[key]
		if !ok {
			p.next++
			run = &peerRun{
				ID:      contract.ID(fmt.Sprintf("00000000-0000-4000-8000-e%010d", p.next)),
				Version: int64(in.Task.Version),
				TaskID:  in.Task.ID,
				State:   "queued",
			}
			p.enqueueRuns[key] = run
		}
		body = peerEnqueueOut{Resource: struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
			TaskID  contract.ID `json:"task_id"`
			State   string      `json:"state"`
		}{ID: run.ID, Version: run.Version, TaskID: run.TaskID, State: run.State}}
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

// snapshotBody resolves the worker home for the requested scope.
func (p *fakePorts) snapshotBody(scope wireScope) peerScopeSnapshot {
	snap := peerScopeSnapshot{Resource: struct {
		Scope     wireScope `json:"scope"`
		Revision  int64     `json:"revision"`
		Ancestors []struct {
			ID       string `json:"id"`
			ParentID string `json:"parent_id"`
		} `json:"ancestors"`
		Worker *struct {
			ID             string `json:"id"`
			OrganizationID string `json:"organization_id"`
			State          string `json:"state"`
		} `json:"worker"`
		Project *struct {
			ID             string `json:"id"`
			OrganizationID string `json:"organization_id"`
			State          string `json:"state"`
		} `json:"project"`
	}{Scope: scope, Revision: 1}}
	workerID := scope.WorkerID
	if workerID == "" || p.missingWorker {
		return snap
	}
	state := p.workers[workerID]
	if state == "" {
		state = "active"
	}
	snap.Resource.Worker = &struct {
		ID             string `json:"id"`
		OrganizationID string `json:"organization_id"`
		State          string `json:"state"`
	}{ID: string(workerID), State: state}
	return snap
}

// metadataBody resolves artifact references. Registered artifacts return
// their stored metadata; unregistered ones echo the requested reference as
// an available artifact in the requesting scope.
func (p *fakePorts) metadataBody(in peerMetadataIn) peerMetadataOut {
	out := peerMetadataOut{Artifacts: []peerArtifact{}}
	for _, ref := range in.Artifacts {
		if a, ok := p.artifacts[ref.ID]; ok {
			out.Artifacts = append(out.Artifacts, a)
			continue
		}
		out.Artifacts = append(out.Artifacts, peerArtifact{
			ID:        ref.ID,
			Version:   1,
			Scope:     in.Scope,
			Digest:    string(ref.Digest),
			Size:      128,
			MediaType: "application/octet-stream",
			State:     "available",
		})
	}
	return out
}

// registerArtifact pins an artifact in the fake registry.
func (p *fakePorts) registerArtifact(id contract.ID, scope wireScope, digest string, mediaType, state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	if state == "" {
		state = "available"
	}
	p.artifacts[id] = peerArtifact{
		ID: id, Version: 1, Scope: scope, Digest: digest, Size: 256,
		MediaType: mediaType, State: state,
	}
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

// reservesOf returns a copy of the recorded reservations.
func (p *fakePorts) reservesOf() []peerReserveIn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]peerReserveIn(nil), p.reserveCalls...)
}

// invalidationsOf returns a copy of the recorded policy invalidations.
func (p *fakePorts) invalidationsOf() []peerPolicyInvalidateIn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]peerPolicyInvalidateIn(nil), p.invalidations...)
}

// setPolicy injects a policy decision for subsequent checks.
func (p *fakePorts) setPolicy(decision string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.policyDecision = decision
}

// setReviewsEligible injects the review eligibility outcome.
func (p *fakePorts) setReviewsEligible(ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reviewsEligible = ok
}

// setWorkerState pins a non-default worker home state.
func (p *fakePorts) setWorkerState(id contract.ID, state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.workers[id] = state
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
	worker  contract.ID
	scope   wireScope
}

// newEnv opens a fresh database, migrates the tasks owner and wires the
// service with deterministic fakes.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "tasks-test.db")})
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
		t.Fatalf("tasks.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate tasks: %v", err)
	}
	env.install = env.ids.New()
	env.owner = env.ids.New()
	env.worker = env.ids.New()
	env.actor = contract.Actor{PrincipalID: env.owner, Kind: contract.KindService}
	env.scope = wireScope{InstallationID: env.install}
	return env
}

// callAs runs one operation inside a write transaction on the given scope.
func (e *testEnv) callAs(scope wireScope, op string, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Write(e.ctx, e.actor, scope.toContract(), func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		payload = p
		return err
	})
	return payload, err
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

// expectFault runs an operation and requires a fault with the exact code.
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

// readRow fetches the durable row for a task, bypassing the wire layer.
func (e *testEnv) readRow(id contract.ID) *taskRow {
	e.t.Helper()
	var row *taskRow
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		row, err = getTask(e.ctx, unit, id)
		return err
	}); err != nil {
		e.t.Fatalf("read task row: %v", err)
	}
	if row == nil {
		e.t.Fatalf("task %s not found", id)
	}
	return row
}

// execSQL runs one statement inside a transaction (tamper simulation).
func (e *testEnv) execSQL(query string, args ...any) {
	e.t.Helper()
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		_, err := unit.ExecContext(e.ctx, query, args...)
		return err
	}); err != nil {
		e.t.Fatalf("exec %q: %v", query, err)
	}
}

// queryOne runs a single-row SELECT inside a read transaction and hands the
// row to fn for scanning (raw inspection of sibling tables).
func (e *testEnv) queryOne(query string, args []any, fn func(row *sql.Row) error) {
	e.t.Helper()
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		return fn(unit.QueryRowContext(e.ctx, query, args...))
	}); err != nil {
		e.t.Fatalf("query %q: %v", query, err)
	}
}

// ---------- fixtures ----------

// testDeadline is a fixed future RFC3339 instant relative to the fake clock.
const testDeadline = "2026-09-11T12:00:00Z"

// testLimits is a finite, admitted-able envelope.
func testLimits() wireLimits {
	return wireLimits{
		Currency: "USD", SpendMicroUnits: 1000, Concurrency: 2, ModelSteps: 100,
		ChildCount: 2, DelegationDepth: 2, AttemptSeconds: 60, RootDeadline: testDeadline,
	}
}

// artifactFixture registers a content artifact with the fake artifacts
// owner and returns its pinned reference.
func (e *testEnv) artifactFixture(name string) wireArtifactRef {
	return e.artifactFixtureState(name, "available")
}

// artifactFixtureState registers a fixture artifact in an explicit state.
func (e *testEnv) artifactFixtureState(name, state string) wireArtifactRef {
	id := e.ids.New()
	digest := fmt.Sprintf("%064x", sha256sum(name))
	e.ports.registerArtifact(id, e.scope, digest, "application/octet-stream", state)
	return wireArtifactRef{ID: id, Digest: contract.Digest(digest)}
}

// setPeerFault injects a fault response for one peer operation.
func (e *testEnv) setPeerFault(op string, f *contract.Fault) {
	e.ports.mu.Lock()
	defer e.ports.mu.Unlock()
	e.ports.peerFaults[op] = f
}

// repositoryProfileFixture builds a verifier profile of the repository kind
// that passes the frozen Adapter_RepositoryVerifierProfile schema.
func repositoryProfileFixture() json.RawMessage {
	return json.RawMessage(`{` +
		`"schema":"zatiti.verifier-profile/v1","kind":"repository_patch",` +
		`"id":"zatiti-repo-verifier","version":"v0.9.0",` +
		`"code_digest":"` + fixtureCodeDigest + `",` +
		`"runner_profile":"runner-1","command_id":"apply-patch",` +
		`"command_digest":"` + fixtureProfileDigest + `",` +
		`"environment_profile":"env-1","network":"disabled",` +
		`"max_output_bytes":65536,"timeout_seconds":120,` +
		`"capability_evidence":{"artifact":{"id":"` + string(fixtureEvidenceID) + `",` +
		`"digest":"` + fixtureProfileDigest + `"},"adapter_version":"adapter-1",` +
		`"source_revision":"rev-1","protocol_revision":"proto-1",` +
		`"profile_digest":"` + fixtureProfileDigest + `",` +
		`"qualified_at":"2026-08-01T00:00:00Z","capabilities":[],"limitations":[]}}`)
}

// verifierArtifactFixture registers a verifier-produced result artifact.
func (e *testEnv) verifierArtifactFixture(name string) contract.ID {
	id := e.ids.New()
	digest := fmt.Sprintf("%064x", sha256sum(name))
	e.ports.registerArtifact(id, e.scope, digest, verificationMediaType, "available")
	return id
}

// presenceObservation builds a passing artifact_presence observation
// pinning the exact digest of a fixture artifact under a named output.
func presenceObservation(checkID, artifactName string, ref wireArtifactRef) wireExpectedObservation {
	return wireExpectedObservation{
		CheckID: checkID, Kind: obsArtifactPresence, Expected: observationExpectedPass,
		ArtifactName: artifactName, ExpectedDigest: ref.Digest,
	}
}

// fixture digest constants satisfy the 64-lowercase-hex digest pattern.
const (
	fixtureCodeDigest    = "1111111111111111111111111111111111111111111111111111111111111111"
	fixtureProfileDigest = "2222222222222222222222222222222222222222222222222222222222222222"
	altDigest            = "3333333333333333333333333333333333333333333333333333333333333333"
	fixtureEvidenceID    = contract.ID("00000000-0000-4000-8000-0000000000ce")
)

// artifactProfileFixture builds an artifact-contract verifier profile with
// an explicit supported_checks list; the schema requires one to three of
// presence, digest and json_schema.
func artifactProfileFixture(supportedChecks string) json.RawMessage {
	return json.RawMessage(`{` +
		`"schema":"zatiti.verifier-profile/v1","kind":"artifact_contract",` +
		`"id":"zatiti-verifier","version":"v1.2.3",` +
		`"code_digest":"` + fixtureCodeDigest + `",` +
		`"supported_checks":[` + supportedChecks + `],` +
		`"max_bytes":1048576,"timeout_seconds":300,` +
		`"capability_evidence":{"artifact":{"id":"` + string(fixtureEvidenceID) + `",` +
		`"digest":"` + fixtureProfileDigest + `"},"adapter_version":"adapter-1",` +
		`"source_revision":"rev-1","protocol_revision":"proto-1",` +
		`"profile_digest":"` + fixtureProfileDigest + `",` +
		`"qualified_at":"2026-08-01T00:00:00Z","capabilities":[],"limitations":[]}}`)
}

// acceptanceFixture builds an independent artifact-contract acceptance whose
// verifier profile passes the frozen Adapter_ArtifactVerifierProfile schema.
func acceptanceFixture(obs ...wireExpectedObservation) wireAcceptance {
	if obs == nil {
		obs = []wireExpectedObservation{}
	}
	return wireAcceptance{
		VerifierID: "zatiti-verifier", VerifierVersion: "v1.2.3",
		SealedInputs:         []wireArtifactRef{},
		ExpectedObservations: obs,
		Mode:                 acceptanceModeIndependent,
		RequiredChildIDs:     []contract.ID{},
		Profile:              artifactProfileFixture(`"presence","digest","json_schema"`),
	}
}

// taskDefInput is the public create/delegate definition body: the schema
// for definitions carries no id, version or state, but does allow parent
// and root attachment fields that only delegation may set.
type taskDefInput struct {
	Scope            wireScope         `json:"scope"`
	OwnerID          contract.ID       `json:"owner_id"`
	WorkerID         contract.ID       `json:"worker_id"`
	Outcome          string            `json:"outcome"`
	Inputs           []wireArtifactRef `json:"inputs"`
	RequiredOutputs  []string          `json:"required_outputs"`
	Acceptance       wireAcceptance    `json:"acceptance"`
	Limits           wireLimits        `json:"limits"`
	Dependencies     []contract.ID     `json:"dependencies"`
	ParentID         contract.ID       `json:"parent_id,omitempty"`
	RootID           contract.ID       `json:"root_id,omitempty"`
	ManualAcceptance bool              `json:"manual_acceptance,omitempty"`
}

// fullTaskInput is the internal _tasks.create task body: the frozen Task
// definition additionally requires id, version and state.
type fullTaskInput struct {
	ID              contract.ID       `json:"id"`
	Version         int64             `json:"version"`
	State           string            `json:"state"`
	Scope           wireScope         `json:"scope"`
	OwnerID         contract.ID       `json:"owner_id"`
	WorkerID        contract.ID       `json:"worker_id"`
	Outcome         string            `json:"outcome"`
	Inputs          []wireArtifactRef `json:"inputs"`
	RequiredOutputs []string          `json:"required_outputs"`
	Acceptance      wireAcceptance    `json:"acceptance"`
	Limits          wireLimits        `json:"limits"`
	Dependencies    []contract.ID     `json:"dependencies"`
}

// taskDef builds a definition for a direct (public) create. The default
// contract pins one required output whose presence is fenced by a digest
// observation over a registered fixture artifact.
func (e *testEnv) taskDef() taskDefInput {
	content := e.artifactFixture("default-output")
	return taskDefInput{
		Scope:           e.scope,
		OwnerID:         e.owner,
		WorkerID:        e.worker,
		Outcome:         "produce the analysis report",
		Inputs:          []wireArtifactRef{},
		RequiredOutputs: []string{"report.bin"},
		Acceptance:      acceptanceFixture(presenceObservation("output-present", "report.bin", content)),
		Limits:          testLimits(),
		Dependencies:    []contract.ID{},
	}
}

// createTask admits one direct task and returns its row identity.
func (e *testEnv) createTask(def taskDefInput) contract.ID {
	e.t.Helper()
	payload := e.mustOK("task.create", struct {
		Scope      wireScope    `json:"scope"`
		Definition taskDefInput `json:"definition"`
	}{Scope: e.scope, Definition: def})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	e.decode(payload.Data, &out)
	if out.Resource.State != stateDraft {
		e.t.Fatalf("new task state %q, want draft", out.Resource.State)
	}
	return out.Resource.ID
}

// createDefaultTask admits one task with the default fixture definition.
func (e *testEnv) createDefaultTask() contract.ID {
	e.t.Helper()
	return e.createTask(e.taskDef())
}

// transition applies a state transition to a task.
func (e *testEnv) transition(id contract.ID, version int64, state string, evidence []contract.ID) *wireTask {
	e.t.Helper()
	if evidence == nil {
		evidence = []contract.ID{}
	}
	payload := e.mustOK("_tasks.transition", struct {
		TaskID          contract.ID   `json:"task_id"`
		ExpectedVersion int64         `json:"expected_version"`
		State           string        `json:"state"`
		EvidenceIDs     []contract.ID `json:"evidence_ids"`
		WaitingReason   string        `json:"waiting_reason"`
		Manual          bool          `json:"manual"`
	}{TaskID: id, ExpectedVersion: version, State: state, EvidenceIDs: evidence})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return &out.Resource
}

// transitionWait applies a waiting transition carrying an explicit reason.
func (e *testEnv) transitionWait(id contract.ID, version int64, reason string) *wireTask {
	e.t.Helper()
	payload := e.mustOK("_tasks.transition", struct {
		TaskID          contract.ID   `json:"task_id"`
		ExpectedVersion int64         `json:"expected_version"`
		State           string        `json:"state"`
		EvidenceIDs     []contract.ID `json:"evidence_ids"`
		WaitingReason   string        `json:"waiting_reason"`
		Manual          bool          `json:"manual"`
	}{TaskID: id, ExpectedVersion: version, State: stateWaiting, EvidenceIDs: []contract.ID{}, WaitingReason: reason})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return &out.Resource
}

// fullTaskDef builds the internal _tasks.create task body: the frozen Task
// definition requires id, version and state on top of the public shape.
func (e *testEnv) fullTaskDef() fullTaskInput {
	e.t.Helper()
	d := e.taskDef()
	return fullTaskInput{
		ID: e.ids.New(), Version: 1, State: stateDraft,
		Scope: d.Scope, OwnerID: d.OwnerID, WorkerID: d.WorkerID, Outcome: d.Outcome,
		Inputs: d.Inputs, RequiredOutputs: d.RequiredOutputs, Acceptance: d.Acceptance,
		Limits: d.Limits, Dependencies: d.Dependencies,
	}
}

// snapshot reads one task's wire resource.
func (e *testEnv) snapshot(id contract.ID) *wireTask {
	e.t.Helper()
	payload := e.mustOK("_tasks.snapshot", struct {
		Scope wireScope   `json:"scope"`
		ID    contract.ID `json:"id"`
	}{Scope: e.scope, ID: id})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return &out.Resource
}

// runToReady drives a freshly created task to ready (prerequisites empty).
func (e *testEnv) runToReady(id contract.ID) int64 {
	e.t.Helper()
	return int64(e.transition(id, 1, stateReady, nil).Version)
}

// runToRunning drives a task from ready to running at the given version.
func (e *testEnv) runToRunning(id contract.ID, version int64) int64 {
	e.t.Helper()
	return int64(e.transition(id, version, stateRunning, nil).Version)
}

// runToVerifying drives a task from running to verifying with evidence.
func (e *testEnv) runToVerifying(id contract.ID, version int64, evidence []contract.ID) int64 {
	e.t.Helper()
	return int64(e.transition(id, version, stateVerifying, evidence).Version)
}

// listTasksPage runs task.list and returns the decoded page.
func (e *testEnv) listTasksPage(in map[string]any) (items []*wireTask, next string) {
	e.t.Helper()
	payload := e.mustOK("task.list", in)
	var out struct {
		Items      []*wireTask `json:"items"`
		NextCursor string      `json:"next_cursor"`
	}
	e.decode(payload.Data, &out)
	return out.Items, out.NextCursor
}

// sha256sum is the deterministic content digest for fixture names.
func sha256sum(name string) uint64 {
	// FNV-1a is sufficient for fixture identity; the digest fence compares
	// pinned strings, not real cryptographic strength of fixtures.
	var h uint64 = 0xcbf29ce484222325
	for i := 0; i < len(name); i++ {
		h ^= uint64(name[i])
		h *= 0x100000001b3
	}
	return h
}
