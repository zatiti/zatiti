package scheduling

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
// exact bodies this package decodes (configuration stage and snapshot,
// tasks create and transition, execution enqueue) with injectable state and
// faults, recording every invocation for behavioral assertions.

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

// fakePorts serves the peer bodies scheduling decodes, with injectable
// faults, recording every call.
type fakePorts struct {
	mu    sync.Mutex
	ids   *seqIDs
	calls []contract.Invocation

	bindings []peerBinding
	workers  map[contract.ID]peerWorker
	tasks    map[contract.ID]wireTask

	fail map[string]*contract.Fault
}

func newFakePorts(ids *seqIDs) *fakePorts {
	return &fakePorts{
		ids:     ids,
		workers: map[contract.ID]peerWorker{},
		tasks:   map[contract.ID]wireTask{},
		fail:    map[string]*contract.Fault{},
	}
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	p.calls = append(p.calls, inv)
	injected := p.fail[inv.Operation]
	bindings := p.bindings
	workers := make(map[contract.ID]peerWorker, len(p.workers))
	for id, w := range p.workers {
		workers[id] = w
	}
	tasks := make(map[contract.ID]wireTask, len(p.tasks))
	for id, t := range p.tasks {
		tasks[id] = t
	}
	p.mu.Unlock()
	if injected != nil {
		return contract.Payload{}, injected
	}

	var body any
	switch inv.Operation {
	case "_configuration.stage":
		body = draftResource{Resource: wireDraft{
			ID: p.ids.New(), Version: 1, BaseRevision: 1,
			Changes: []json.RawMessage{}, Diagnostics: []wireDiagnostic{},
		}}
	case "_configuration.snapshot":
		var in snapshotCallInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		snap := snapshotResource{Scope: in.Scope, Revision: 7, Bindings: bindings}
		if w, bound := workers[in.Scope.WorkerID]; in.Scope.WorkerID != "" && bound {
			snap.Worker = &peerWorker{ID: w.ID, Version: w.Version}
		}
		body = snapshotBody{Resource: snap}
	case "_tasks.create":
		var in tasksCreateCallInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		task := in.Task
		task.Version = 1
		task.State = "draft"
		p.mu.Lock()
		p.tasks[task.ID] = task
		p.mu.Unlock()
		body = peerTaskBody{Resource: task}
	case "_tasks.transition":
		var in tasksTransitionCallInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		task, ok := tasks[in.TaskID]
		if !ok {
			return contract.Payload{}, &contract.Fault{
				Code: contract.CodeNotFound,
				Message: "task " + string(in.TaskID) +
					" is unknown in this installation",
			}
		}
		task.Version = in.ExpectedVersion + 1
		task.State = in.State
		body = peerTaskBody{Resource: task}
	case "_execution.enqueue":
		body = json.RawMessage(`{}`)
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

// failOp injects one fault the fake ports return for the next call of the
// named operation, so tests can drive peer-failure paths.
func (p *fakePorts) failOp(op string, f *contract.Fault) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail[op] = f
}

func (p *fakePorts) setBindings(bs []peerBinding) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bindings = bs
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

// newEnv opens a fresh database, migrates the scheduling owner and wires
// default identity: one installation scope with a service actor.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "scheduling-test.db")})
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
		t.Fatalf("scheduling.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate scheduling: %v", err)
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

// ---------- fixture builders ----------

// minimalAcceptance builds one schema-valid acceptance envelope with no
// required observations; verifiers own the checks, scheduling only carries
// the shape. The profile is a fully qualified artifact verifier contract —
// the wire schema rejects an empty verification profile.
func minimalAcceptance() wireAcceptance {
	digest := candidateDigest()
	profile, err := json.Marshal(map[string]any{
		"schema":           "zatiti.verifier-profile/v1",
		"kind":             "artifact_contract",
		"id":               "scheduling-test-verifier",
		"version":          "1",
		"code_digest":      digest,
		"supported_checks": []string{"presence"},
		"max_bytes":        1048576,
		"timeout_seconds":  60,
		"capability_evidence": map[string]any{
			"artifact":          map[string]any{"id": "00000000-0000-4000-8000-000000000001", "digest": digest},
			"adapter_version":   "1",
			"source_revision":   "1",
			"protocol_revision": "1",
			"profile_digest":    digest,
			"qualified_at":      "2026-09-10T12:00:00Z",
			"capabilities":      []string{},
			"limitations":       []string{},
		},
	})
	if err != nil {
		panic(fmt.Sprintf("marshal verifier profile: %v", err))
	}
	return wireAcceptance{
		VerifierID:           "verifier",
		VerifierVersion:      "1",
		SealedInputs:         []wireArtifactRef{},
		ExpectedObservations: []wireExpectedObservation{},
		Mode:                 "manual",
		RequiredChildIDs:     []contract.ID{},
		Profile:              profile,
	}
}

// boundedLimits builds a wire limits envelope; the root deadline is one
// attempt window from now, exactly what tasks will accept at admission.
func boundedLimits(now time.Time, spend int64) wireLimits {
	return wireLimits{
		Currency:        "USD",
		SpendMicroUnits: spend,
		Concurrency:     1,
		ModelSteps:      10,
		ChildCount:      0,
		DelegationDepth: 0,
		AttemptSeconds:  600,
		RootDeadline:    formatStamp(now.Add(600 * time.Second)),
	}
}

// taskTemplate builds one schema-valid task template pinned to scope.
func (e *testEnv) taskTemplate(scope contract.Scope, worker contract.ID) wireTask {
	return wireTask{
		ID:              e.ids.New(),
		Version:         1,
		Scope:           scope,
		OwnerID:         e.owner,
		WorkerID:        worker,
		Outcome:         "collect the report",
		Inputs:          []wireArtifactRef{},
		RequiredOutputs: []string{"report"},
		Acceptance:      minimalAcceptance(),
		Limits:          boundedLimits(e.clock.Now(), 10),
		Dependencies:    []contract.ID{},
		State:           "ready",
	}
}

// scheduleDef builds one schema-valid schedule definition: every-minute cron
// in UTC, coalesce misfire with a two-minute catch-up window.
func (e *testEnv) scheduleDef(scope contract.Scope, worker contract.ID) scheduleDefinitionInput {
	return scheduleDefinitionInput{
		Scope:          scope,
		TaskTemplate:   e.taskTemplate(scope, worker),
		Timezone:       "UTC",
		Expression:     "* * * * *",
		Misfire:        misfireCoalesce,
		CatchUpSeconds: 120,
		Paused:         false,
	}
}

// responsibilityDef builds one schema-valid responsibility definition: one
// bounded cycle every minimum interval, cycle spend inside the aggregate.
func responsibilityDef(scope contract.Scope, worker contract.ID, now time.Time) responsibilityDefinitionInput {
	cycle := boundedLimits(now, 10)
	aggregate := boundedLimits(now, 100)
	return responsibilityDefinitionInput{
		Scope:                scope,
		WorkerID:             worker,
		Outcome:              "keep the queue drained",
		Signals:              []string{},
		Triggers:             []string{},
		ReasoningPolicy:      "deliberate",
		MinIntervalSeconds:   60,
		CycleLimits:          cycle,
		AggregateLimits:      aggregate,
		PauseConditions:      []string{},
		EscalationConditions: []string{},
		Acceptance:           minimalAcceptance(),
		Paused:               false,
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

// scheduleChangeResource assembles the full schedule definition a change
// carries, mirroring scheduleResource.
func scheduleChangeResource(id contract.ID, version contract.Version, def scheduleDefinitionInput) wireSchedule {
	return scheduleResource(id, version, def)
}

// responsibilityChangeResource assembles the full responsibility definition
// a change carries, mirroring responsibilityResource.
func responsibilityChangeResource(id contract.ID, version contract.Version, def responsibilityDefinitionInput) wireResponsibility {
	return responsibilityResource(id, version, def)
}

// activateChanges applies one sealed candidate slice of scheduling-owned
// changes through the internal activation boundary and returns the versions.
func (e *testEnv) activateChanges(changes ...wireChange) []wireRef {
	e.t.Helper()
	payload := e.mustOK(opActivate, candidateEnvelope{Candidate: candidateInput{
		PlanID:          e.ids.New(),
		BaseRevision:    1,
		CandidateDigest: candidateDigest(),
		Changes:         changes,
		Dependencies:    []wireRef{},
	}})
	var out versionsBody
	e.decode(payload.Data, &out)
	return out.Versions
}

// createSchedule activates one schedule definition and returns its stored
// row view.
func (e *testEnv) createSchedule(def scheduleDefinitionInput) scheduleRow {
	e.t.Helper()
	id := e.ids.New()
	full := scheduleChangeResource(id, 1, def)
	e.activateChanges(wireChange{
		Kind: kindSchedule, Action: changeCreate, ID: id, ExpectedVersion: 0, Definition: rawDef(full),
	})
	row, found := e.mustFindSchedule(id)
	if !found {
		e.t.Fatalf("created schedule %s not found", id)
	}
	return row
}

// createResponsibility activates one responsibility definition and returns
// its row view.
func (e *testEnv) createResponsibility(def responsibilityDefinitionInput) responsibilityRow {
	e.t.Helper()
	id := e.ids.New()
	full := responsibilityChangeResource(id, 1, def)
	e.activateChanges(wireChange{
		Kind: kindResponsibility, Action: changeCreate, ID: id, ExpectedVersion: 0, Definition: rawDef(full),
	})
	row, found := e.mustFindResponsibility(id)
	if !found {
		e.t.Fatalf("created responsibility %s not found", id)
	}
	return row
}

// archiveSchedule activates the archive of one schedule.
func (e *testEnv) archiveSchedule(id contract.ID, expected int64) {
	e.t.Helper()
	row, found := e.mustFindSchedule(id)
	if !found {
		e.t.Fatalf("archive: schedule %s not found", id)
	}
	full, err := row.wire()
	if err != nil {
		e.t.Fatalf("render schedule: %v", err)
	}
	full.Version = contract.Version(expected + 1)
	e.activateChanges(wireChange{
		Kind: kindSchedule, Action: changeArchive, ID: id,
		ExpectedVersion: expected, Definition: rawDef(full),
	})
}

// archiveResponsibility activates the archive of one responsibility.
func (e *testEnv) archiveResponsibility(id contract.ID, expected int64) {
	e.t.Helper()
	row, found := e.mustFindResponsibility(id)
	if !found {
		e.t.Fatalf("archive: responsibility %s not found", id)
	}
	full, err := row.wire()
	if err != nil {
		e.t.Fatalf("render responsibility: %v", err)
	}
	full.Version = contract.Version(expected + 1)
	e.activateChanges(wireChange{
		Kind: kindResponsibility, Action: changeArchive, ID: id,
		ExpectedVersion: expected, Definition: rawDef(full),
	})
}

// candidateDigest is a schema-valid 64-hex candidate digest for tests.
func candidateDigest() string {
	return string(contract.Hash([]byte("scheduling-test-candidate")))
}

// ---------- store-backed fixtures and reads ----------

// insertWakeRow inserts one pending wake verbatim, for admission fixtures
// the cron arming would not produce (event wakes, shifted instants).
func (e *testEnv) insertWakeRow(scope contract.Scope, sourceKind string, sourceID contract.ID, occurrenceKey string, due time.Time, conditionVersion contract.Version) wakeRow {
	e.t.Helper()
	var row wakeRow
	if err := e.write(func(unit contract.Unit) error {
		if err := e.svc.insertSourceWake(e.ctx, unit, scope, sourceKind, sourceID, occurrenceKey, due, conditionVersion, e.clock.Now()); err != nil {
			return err
		}
		rows, err := listPendingWakes(e.ctx, unit, due, 1000)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.SourceID == sourceID && r.OccurrenceKey == occurrenceKey {
				row = r
				return nil
			}
		}
		return notFound("wake for %s/%s was not persisted", sourceID, occurrenceKey)
	}); err != nil {
		e.t.Fatalf("insert wake: %v", err)
	}
	return row
}

// mustFindSchedule reads one schedule row and requires it to exist.
func (e *testEnv) mustFindSchedule(id contract.ID) (scheduleRow, bool) {
	e.t.Helper()
	var (
		out   scheduleRow
		found bool
	)
	if err := e.write(func(unit contract.Unit) error {
		var err error
		out, found, err = loadSchedule(e.ctx, unit, id)
		return err
	}); err != nil {
		e.t.Fatalf("load schedule %s: %v", id, err)
	}
	return out, found
}

// mustFindResponsibility reads one responsibility row and requires it to
// exist.
func (e *testEnv) mustFindResponsibility(id contract.ID) (responsibilityRow, bool) {
	e.t.Helper()
	var (
		out   responsibilityRow
		found bool
	)
	if err := e.write(func(unit contract.Unit) error {
		var err error
		out, found, err = loadResponsibility(e.ctx, unit, id)
		return err
	}); err != nil {
		e.t.Fatalf("load responsibility %s: %v", id, err)
	}
	return out, found
}

// mustFindWake reads one wake row and requires it to exist.
func (e *testEnv) mustFindWake(id contract.ID) wakeRow {
	e.t.Helper()
	var out wakeRow
	if err := e.write(func(unit contract.Unit) error {
		var err error
		out, err = loadWake(e.ctx, unit, id)
		return err
	}); err != nil {
		e.t.Fatalf("load wake %s: %v", id, err)
	}
	return out
}

// pendingWakes lists every wake pending at the pinned instant.
func (e *testEnv) pendingWakes() []wakeRow {
	e.t.Helper()
	var out []wakeRow
	if err := e.write(func(unit contract.Unit) error {
		var err error
		out, err = listPendingWakes(e.ctx, unit, e.clock.Now(), 1000)
		return err
	}); err != nil {
		e.t.Fatalf("list pending wakes: %v", err)
	}
	return out
}

// pendingWakesFor lists every wake row still pending for one source,
// regardless of its due instant — armed-but-future wakes count.
func (e *testEnv) pendingWakesFor(sourceID contract.ID) []wakeRow {
	e.t.Helper()
	var out []wakeRow
	if err := e.write(func(unit contract.Unit) error {
		rows, err := listPendingWakes(e.ctx, unit, e.clock.Now().Add(24*time.Hour), 1000)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.SourceID == sourceID {
				out = append(out, r)
			}
		}
		return nil
	}); err != nil {
		e.t.Fatalf("list pending wakes for %s: %v", sourceID, err)
	}
	return out
}

// occurrencesOf counts the occurrence rows recorded for one source.
func (e *testEnv) occurrencesOf(sourceID contract.ID) []occurrenceRow {
	e.t.Helper()
	var out []occurrenceRow
	if err := e.write(func(unit contract.Unit) error {
		rows, err := unit.QueryContext(e.ctx,
			`SELECT id, installation_id, organization_id, project_id, worker_id, task_id, scope_json,
			 source_id, occurrence_key, task_ref, state, reason, decided_at, created_at
			 FROM scheduling_occurrences WHERE source_id = ?`, sourceID)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			r := occurrenceRow{}
			var scopeJSON, decided, created string
			if err := rows.Scan(&r.ID, &r.InstallationID, &r.OrganizationID, &r.ProjectID, &r.WorkerID,
				&r.TaskID, &scopeJSON, &r.SourceID, &r.OccurrenceKey, &r.TaskRef, &r.State, &r.Reason,
				&decided, &created); err != nil {
				return err
			}
			var err error
			if r.DecidedAt, err = parseStamp(decided); err != nil {
				return err
			}
			if r.CreatedAt, err = parseStamp(created); err != nil {
				return err
			}
			if r.Scope, err = decodeScope(scopeJSON); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	}); err != nil {
		e.t.Fatalf("load occurrences: %v", err)
	}
	return out
}

// cyclesOf lists every recorded cycle of one responsibility.
func (e *testEnv) cyclesOf(id contract.ID) []cycleRow {
	e.t.Helper()
	var out []cycleRow
	if err := e.write(func(unit contract.Unit) error {
		rows, err := unit.QueryContext(e.ctx,
			`SELECT id, responsibility_id, responsibility_version, scope_json, next_wake, outputs_json, task_ids_json, recorded_at
			 FROM scheduling_cycles WHERE responsibility_id = ?`, id)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			c := cycleRow{}
			var scopeJSON, outputsJSON, taskIDsJSON, nextWake, recorded string
			if err := rows.Scan(&c.ID, &c.ResponsibilityID, &c.ResponsibilityVersion, &scopeJSON,
				&nextWake, &outputsJSON, &taskIDsJSON, &recorded); err != nil {
				return err
			}
			var err error
			if c.NextWake, err = parseStamp(nextWake); err != nil {
				return err
			}
			if c.RecordedAt, err = parseStamp(recorded); err != nil {
				return err
			}
			if c.Scope, err = decodeScope(scopeJSON); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(outputsJSON), &c.Outputs); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(taskIDsJSON), &c.TaskIDs); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	}); err != nil {
		e.t.Fatalf("load cycles: %v", err)
	}
	return out
}

// write runs fn inside one write transaction on the env's scope.
func (e *testEnv) write(fn func(unit contract.Unit) error) error {
	return e.db.Write(e.ctx, e.actor, e.scope, fn)
}

// ---------- operation wrappers ----------

// stageSchedule runs schedule.create and returns the staged body.
func (e *testEnv) stageSchedule(in scheduleCreateInput) stagedScheduleBody {
	e.t.Helper()
	payload := e.mustOK(opScheduleCreate, in)
	var out stagedScheduleBody
	e.decode(payload.Data, &out)
	return out
}

// stageResponsibility runs responsibility.create and returns the staged body.
func (e *testEnv) stageResponsibility(in responsibilityCreateInput) stagedResponsibilityBody {
	e.t.Helper()
	payload := e.mustOK(opResponsibilityCreate, in)
	var out stagedResponsibilityBody
	e.decode(payload.Data, &out)
	return out
}

// getSchedule runs schedule.get and returns the wire resource.
func (e *testEnv) getSchedule(scope contract.Scope, id contract.ID) wireSchedule {
	e.t.Helper()
	payload := e.mustOK(opScheduleGet, scheduleGetInput{Scope: scope, ID: id})
	var out scheduleBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// listSchedules runs schedule.list and returns the page and next cursor.
func (e *testEnv) listSchedules(scope contract.Scope, cursor *string, limit *int64) ([]wireSchedule, *string) {
	e.t.Helper()
	payload, err := e.call(opScheduleList, listInput{Scope: scope, Cursor: cursor, Limit: limit})
	if err != nil {
		e.t.Fatalf("schedule.list failed: %v", err)
	}
	var out itemsOut[wireSchedule]
	e.decode(payload.Data, &out)
	return out.Items, payload.NextCursor
}

// wakeDueOp runs wake.due and returns the due wakes.
func (e *testEnv) wakeDueOp(now time.Time, limit int64) []wireWake {
	e.t.Helper()
	payload := e.mustOK(opWakeDue, wakeDueInput{Now: now, Limit: limit})
	var out wakeDueBody
	e.decode(payload.Data, &out)
	return out.Wakes
}

// admitWakeOp runs wake.admit and returns the decision body.
func (e *testEnv) admitWakeOp(w wakeRow) wakeAdmitBody {
	e.t.Helper()
	payload := e.mustOK(opWakeAdmit, wakeAdmitInput{Wake: wakeWire(w)})
	var out wakeAdmitBody
	e.decode(payload.Data, &out)
	return out
}

// recordCycleOp runs cycle.record and returns the wire resource.
func (e *testEnv) recordCycleOp(id contract.ID, expected contract.Version, next time.Time, outputs []wireArtifactRef, taskIDs []contract.ID) wireResponsibility {
	e.t.Helper()
	if outputs == nil {
		outputs = []wireArtifactRef{} // the wire schema rejects a null array
	}
	if taskIDs == nil {
		taskIDs = []contract.ID{}
	}
	payload := e.mustOK(opCycleRecord, cycleRecordInput{
		ResponsibilityID: id, ExpectedVersion: expected, NextWake: next,
		Outputs: outputs, TaskIDs: taskIDs,
	})
	var out responsibilityBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// wakeWire renders a stored wake row as its wire shape for admit inputs.
func wakeWire(r wakeRow) wireWake {
	return wireWake{
		ID:               r.ID,
		Scope:            r.Scope,
		SourceID:         r.SourceID,
		OccurrenceKey:    r.OccurrenceKey,
		DueAt:            r.DueAt,
		ConditionVersion: r.ConditionVersion,
	}
}

// tasksCreateCalls decodes every recorded _tasks.create call.
func (e *testEnv) tasksCreateCalls() []tasksCreateCallInput {
	e.t.Helper()
	calls := e.ports.callsOf("_tasks.create")
	out := make([]tasksCreateCallInput, 0, len(calls))
	for _, c := range calls {
		var in tasksCreateCallInput
		if err := json.Unmarshal(c.Input, &in); err != nil {
			e.t.Fatalf("decode tasks create call: %v", err)
		}
		out = append(out, in)
	}
	return out
}

// transitionCalls decodes every recorded _tasks.transition call.
func (e *testEnv) tasksTransitionCalls() []tasksTransitionCallInput {
	e.t.Helper()
	calls := e.ports.callsOf("_tasks.transition")
	out := make([]tasksTransitionCallInput, 0, len(calls))
	for _, c := range calls {
		var in tasksTransitionCallInput
		if err := json.Unmarshal(c.Input, &in); err != nil {
			e.t.Fatalf("decode tasks transition call: %v", err)
		}
		out = append(out, in)
	}
	return out
}

// enqueueCalls decodes every recorded _execution.enqueue call.
func (e *testEnv) enqueueCalls() []executionEnqueueCallInput {
	e.t.Helper()
	calls := e.ports.callsOf("_execution.enqueue")
	out := make([]executionEnqueueCallInput, 0, len(calls))
	for _, c := range calls {
		var in executionEnqueueCallInput
		if err := json.Unmarshal(c.Input, &in); err != nil {
			e.t.Fatalf("decode execution enqueue call: %v", err)
		}
		out = append(out, in)
	}
	return out
}

// validateOp runs _scheduling.validate over one sealed candidate slice.
func (e *testEnv) validateOp(changes ...wireChange) wireValidation {
	e.t.Helper()
	payload := e.mustOK(opValidate, candidateEnvelope{Candidate: candidateInput{
		PlanID:          e.ids.New(),
		BaseRevision:    1,
		CandidateDigest: candidateDigest(),
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
