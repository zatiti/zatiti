package accounting

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
// serve _configuration.snapshot from per-installation fixtures and record
// every _configuration.stage call for assertions.

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

// fakePorts serves the configuration snapshot fixture per installation and
// records every stage call with an injectable response and faults.
type fakePorts struct {
	mu        sync.Mutex
	snapshots map[contract.ID]*snapshotScope
	stages    []stageInput
	stageBody func(in stageInput) draftResourceBody
	fail      map[string]*contract.Fault
}

func newFakePorts() *fakePorts {
	return &fakePorts{
		snapshots: map[contract.ID]*snapshotScope{},
		fail:      map[string]*contract.Fault{},
	}
}

// setSnapshot installs the configuration fixture one scope resolution
// returns for an installation.
func (p *fakePorts) setSnapshot(install contract.ID, snap *snapshotScope) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshots[install] = snap
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	if injected := p.fail[inv.Operation]; injected != nil {
		p.mu.Unlock()
		return contract.Payload{}, injected
	}
	var body any
	switch inv.Operation {
	case opConfigSnapshot:
		var in scopeInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		snap, ok := p.snapshots[in.Scope.InstallationID]
		if !ok {
			snap = &snapshotScope{Revision: 1, Ancestors: []snapshotOrg{}, Bindings: json.RawMessage("[]")}
		}
		body = snapshotScopeBody{Resource: *snap}
	case opConfigStage:
		var in stageInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		p.stages = append(p.stages, in)
		body = p.stageBody(in)
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

// stagesOf returns a copy of the recorded stage calls.
func (p *fakePorts) stagesOf() []stageInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]stageInput(nil), p.stages...)
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
	scope   wireScope
}

// newEnv opens a fresh database and migrates the accounting owner. Budgets
// and reservations are created per test through the real operations.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "accounting-test.db")})
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
		t.Fatalf("accounting.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate accounting: %v", err)
	}
	env.install = env.ids.New()
	env.owner = env.ids.New()
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

// faultCode returns the code of a contract fault error, or "" when err is
// not a fault.
func faultCode(err error) string {
	var f *contract.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

// ---------- accounting fixtures ----------

// limits builds a full wireLimits document. Every field is required on the
// wire and the Limits schema floors concurrency, model steps and attempt
// seconds at one, so the fields a case does not vary carry the shipped
// defaults; the caller decides which declarations matter.
func limits(currency string, spend, concurrency int64) wireLimits {
	return wireLimits{
		Currency:        currency,
		SpendMicroUnits: spend,
		Concurrency:     concurrency,
		ModelSteps:      defaultWorkerModelSteps,
		AttemptSeconds:  defaultWorkerAttemptSeconds,
		RootDeadline:    time.Time{},
	}
}

// limitsFixture describes one configured limit set in a chain fixture.
type limitsFixture struct {
	currency    string
	spend       int64
	concurrency int64
}

func (f *limitsFixture) wire() *wireLimits {
	if f == nil {
		return nil
	}
	// The Limits schema floors concurrency at one; a fixture without an
	// explicit declaration carries a high cap that never binds.
	concurrency := f.concurrency
	if concurrency == 0 {
		concurrency = 16
	}
	l := limits(f.currency, f.spend, concurrency)
	return &l
}

// chainSpec describes the fixture chain: ancestor depth and the per-level
// budget and snapshot-limit fixtures.
type chainSpec struct {
	orgDepth      int // 1 = one base organization, 2 = base under a root organization
	installBudget *limitsFixture
	rootOrgBudget *limitsFixture
	baseOrgBudget *limitsFixture
	projectBudget *limitsFixture
	workerBudget  *limitsFixture
	rootOrgLimits *limitsFixture
	baseOrgLimits *limitsFixture
	projectLimits *limitsFixture
	workerLimits  *limitsFixture
}

// chainIDs carries the identities of one fixture chain.
type chainIDs struct {
	rootOrg, baseOrg, project, worker contract.ID
}

// makeChain wires a fixture chain: optional budgets activated through
// _accounting.activate and the configuration snapshot installed on the fake
// ports. Ancestor orgs appear nearest first, matching the snapshot walk.
func (e *testEnv) makeChain(spec chainSpec) chainIDs {
	e.t.Helper()
	chain := chainIDs{rootOrg: e.ids.New(), baseOrg: e.ids.New(), project: e.ids.New(), worker: e.ids.New()}
	if spec.installBudget != nil {
		e.applyBudget(e.install, *spec.installBudget.wire())
	}
	if spec.rootOrgBudget != nil {
		e.applyBudget(chain.rootOrg, *spec.rootOrgBudget.wire())
	}
	if spec.baseOrgBudget != nil {
		e.applyBudget(chain.baseOrg, *spec.baseOrgBudget.wire())
	}
	if spec.projectBudget != nil {
		e.applyBudget(chain.project, *spec.projectBudget.wire())
	}
	if spec.workerBudget != nil {
		e.applyBudget(chain.worker, *spec.workerBudget.wire())
	}
	ancestors := []snapshotOrg{}
	if spec.orgDepth >= 2 {
		ancestors = append(ancestors, snapshotOrg{
			ID: chain.baseOrg, Version: 1, Key: "base", Name: "Base Org",
			ChiefID: e.ids.New(), ParentID: &chain.rootOrg, Limits: spec.baseOrgLimits.wire(),
		})
		ancestors = append(ancestors, snapshotOrg{
			ID: chain.rootOrg, Version: 1, Key: "root", Name: "Root Org",
			ChiefID: e.ids.New(), Limits: spec.rootOrgLimits.wire(),
		})
	} else {
		ancestors = append(ancestors, snapshotOrg{
			ID: chain.baseOrg, Version: 1, Key: "base", Name: "Base Org",
			ChiefID: e.ids.New(), Limits: spec.baseOrgLimits.wire(),
		})
		chain.rootOrg = ""
	}
	snap := &snapshotScope{
		Scope:     e.chainScope(chain),
		Revision:  1,
		Ancestors: ancestors,
		Bindings:  json.RawMessage("[]"),
		Project: &snapshotProject{
			ID: chain.project, Version: 1, OrganizationID: chain.baseOrg, Key: "proj", Name: "Project",
			Repositories: []string{"example/repo"}, Bindings: []contract.ID{}, Classification: "internal",
			Limits: spec.projectLimits.wire(),
		},
		Worker: &snapshotWorker{
			ID: chain.worker, Version: 1, OrganizationID: chain.baseOrg, Key: "worker", Name: "Worker",
			SkillVersions: []wireRef{}, Bindings: []contract.ID{}, Profile: json.RawMessage("null"),
			Limits: spec.workerLimits.wire(),
		},
	}
	e.ports.setSnapshot(e.install, snap)
	return chain
}

// chainScope names every dimension of the fixture chain.
func (e *testEnv) chainScope(chain chainIDs) wireScope {
	s := wireScope{InstallationID: e.install}
	if chain.baseOrg != "" {
		s.OrganizationID = chain.baseOrg
	}
	if chain.project != "" {
		s.ProjectID = chain.project
	}
	if chain.worker != "" {
		s.WorkerID = chain.worker
	}
	return s
}

// levelsFor resolves the reservation order for one scope on a write unit.
func (e *testEnv) levelsFor(scope wireScope) ([]level, error) {
	var out []level
	err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		levels, callErr := e.svc.levelsForScope(e.ctx, unit, scope)
		out = levels
		return callErr
	})
	return out, err
}

// budgetChange builds one budget change for the activate boundary.
func budgetChange(action string, id contract.ID, expectedVersion int64, l wireLimits) wireChange {
	def, err := canonicalJSON(l)
	if err != nil {
		panic(fmt.Sprintf("fixture limits: %v", err))
	}
	return wireChange{Kind: changeKindBudget, Action: action, ID: id, ExpectedVersion: expectedVersion, Definition: def}
}

// digest64 is a syntactically valid candidate digest: the Candidate schema
// pins candidate_digest to 64 lowercase hex characters.
const digest64 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// applyBudget activates one budget create through _accounting.activate and
// returns the assigned version.
func (e *testEnv) applyBudget(id contract.ID, l wireLimits) int64 {
	e.t.Helper()
	payload := e.mustOK(opActivate, candidateEnvelope{Candidate: wireCandidate{
		PlanID: e.ids.New(), BaseRevision: 1, CandidateDigest: digest64, Dependencies: []wireRef{},
		Changes: []wireChange{budgetChange(changeActionCreate, id, 0, l)},
	}})
	var out versionsOutput
	e.decode(payload.Data, &out)
	if len(out.Versions) != 1 {
		e.t.Fatalf("activate returned %d versions, want 1", len(out.Versions))
	}
	return out.Versions[0].Version
}

// applyBudgetUpdate stages one budget update through _accounting.activate
// under the version fence and returns the assigned version.
func (e *testEnv) applyBudgetUpdate(id contract.ID, expectedVersion int64, l wireLimits) int64 {
	e.t.Helper()
	payload := e.mustOK(opActivate, candidateEnvelope{Candidate: wireCandidate{
		PlanID: e.ids.New(), BaseRevision: 1, CandidateDigest: digest64, Dependencies: []wireRef{},
		Changes: []wireChange{budgetChange(changeActionUpdate, id, expectedVersion, l)},
	}})
	var out versionsOutput
	e.decode(payload.Data, &out)
	if len(out.Versions) != 1 {
		e.t.Fatalf("activate returned %d versions, want 1", len(out.Versions))
	}
	return out.Versions[0].Version
}

// reserveIn builds a reserve input for one scope with a caller-declared root
// spend ceiling that comfortably fits the amount.
func (e *testEnv) reserveIn(scope wireScope, operation contract.ID, currency string, amount int64) reserveInput {
	return reserveInput{
		Scope:       scope,
		OperationID: operation,
		Amount:      wireMoney{Currency: currency, MicroUnits: amount},
		Limits:      limits(currency, amount*2, 2),
	}
}

// mustReserve runs a reserve and returns the resource body.
func (e *testEnv) mustReserve(in reserveInput) wireReservation {
	e.t.Helper()
	payload := e.mustOK(opReserve, in)
	var out reservationResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// mustSettle runs a settle and returns the resource body.
func (e *testEnv) mustSettle(in settleInput) wireReservation {
	e.t.Helper()
	payload := e.mustOK(opSettle, in)
	var out reservationResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// usageGet runs usage.get and returns the resource body.
func (e *testEnv) usageGet(scope wireScope) wireUsage {
	e.t.Helper()
	payload := e.mustOK(opUsageGet, scopeInput{Scope: scope})
	var out usageResourceBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// ---------- direct storage readers ----------

// readPosition reads one position row directly; found=false when absent.
func (e *testEnv) readPosition(kind positionKind, ref contract.ID) (position, bool) {
	e.t.Helper()
	var p position
	var found bool
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		row, err := loadPosition(e.ctx, unit, kind, ref)
		if row != nil {
			p, found = *row, true
		}
		return err
	}); err != nil {
		e.t.Fatalf("read position %s/%s: %v", kind, ref, err)
	}
	return p, found
}

// readEntries reads every ledger entry of one reservation in id order.
func (e *testEnv) readEntries(reservation contract.ID) []entry {
	e.t.Helper()
	var rows []entry
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		rs, err := unit.QueryContext(e.ctx, `SELECT id, reservation_id, installation_id, kind, currency, amount, advisory, note
			FROM accounting_entries WHERE reservation_id = ? ORDER BY created_at, id`, string(reservation))
		if err != nil {
			return err
		}
		defer func() { _ = rs.Close() }()
		for rs.Next() {
			var en entry
			var advisory int
			if err := rs.Scan(&en.ID, &en.ReservationID, &en.InstallID, &en.Kind, &en.Currency, &en.Amount, &advisory, &en.Note); err != nil {
				return err
			}
			en.Advisory = advisory == 1
			rows = append(rows, en)
		}
		return rs.Err()
	}); err != nil {
		e.t.Fatalf("read entries: %v", err)
	}
	return rows
}

// readReservation loads one reservation row through the store path.
func (e *testEnv) readReservation(id contract.ID) *reservationRow {
	e.t.Helper()
	var row *reservationRow
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		row, err = loadReservation(e.ctx, unit, id)
		return err
	}); err != nil {
		e.t.Fatalf("read reservation %s: %v", id, err)
	}
	return row
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
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
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
	}); err != nil {
		e.t.Fatalf("read events: %v", err)
	}
	return rows
}
