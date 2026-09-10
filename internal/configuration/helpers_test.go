package configuration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Test harness: a real storage database per test, deterministic fakes for
// clock, ids, peer ports and blobs, and helpers that drive every operation
// through Service.Handle inside storage write transactions.

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

// fakePorts serves the peer validate/activate bodies and the policy and
// review checks with injectable decisions and faults, recording every call.
type fakePorts struct {
	mu sync.Mutex

	calls     []contract.Invocation
	decision  string                    // _policy.check decision (default allow)
	reasons   []string                  // policy reasons
	decReqs   []wireDecisionRequirement // decision requirements on review
	approveOK bool                      // _reviews.check approval outcome
	fail      map[string]*contract.Fault
}

func newFakePorts() *fakePorts {
	return &fakePorts{decision: "allow", approveOK: true, fail: map[string]*contract.Fault{}}
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	p.calls = append(p.calls, inv)
	decision, reasons, decReqs, approveOK := p.decision, p.reasons, p.decReqs, p.approveOK
	injected := p.fail[inv.Operation]
	p.mu.Unlock()
	if injected != nil {
		return contract.Payload{}, injected
	}
	var body any
	switch {
	case strings.HasSuffix(inv.Operation, ".validate"):
		body = validateOutputBody{Resource: wireValidation{
			Diagnostics:  []wireDiagnostic{},
			Requirements: []wireRequirement{},
			Dependencies: []wireRef{},
		}}
	case strings.HasSuffix(inv.Operation, ".activate"):
		body = versionsOutput{Versions: []wireRef{}}
	case inv.Operation == "_policy.check":
		body = policyCheckBody{Resource: policyResult{
			Decision: decision, Reasons: reasons, Requirements: decReqs,
		}}
	case inv.Operation == "_reviews.check":
		if !approveOK {
			body = reviewsCheckBody{Eligible: false}
		} else {
			body = reviewsCheckBody{Eligible: true, Decision: &reviewDecision{Decision: "approve"}}
		}
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

// callsOf returns a snapshot of the recorded peer invocations.
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

// fakeBlobs is an in-memory content-addressed blob store for export/import.
type fakeBlobs struct {
	mu        sync.Mutex
	staged    map[string][]byte
	published map[contract.Digest][]byte
	next      int
}

func newFakeBlobs() *fakeBlobs {
	return &fakeBlobs{staged: map[string][]byte{}, published: map[contract.Digest][]byte{}}
}

func (b *fakeBlobs) Stage(ctx context.Context, r io.Reader, size int64) (string, contract.Digest, int64, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	ref := fmt.Sprintf("staging-%d", b.next)
	b.staged[ref] = raw
	return ref, contract.Hash(raw), int64(len(raw)), nil
}

func (b *fakeBlobs) Publish(ctx context.Context, stagingRef string, digest contract.Digest) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, ok := b.staged[stagingRef]
	if !ok {
		return errors.New("fake blobs: unknown staging reference " + stagingRef)
	}
	b.published[digest] = raw
	delete(b.staged, stagingRef)
	return nil
}

func (b *fakeBlobs) Open(ctx context.Context, digest contract.Digest, offset, length int64) (io.ReadCloser, error) {
	b.mu.Lock()
	raw, ok := b.published[digest]
	b.mu.Unlock()
	if !ok {
		return nil, errors.New("fake blobs: unknown digest " + string(digest))
	}
	data := raw
	if offset > 0 {
		if offset > int64(len(data)) {
			return nil, errors.New("fake blobs: offset past end")
		}
		data = data[offset:]
	}
	if length >= 0 && length < int64(len(data)) {
		data = data[:length]
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (b *fakeBlobs) RemoveStaged(ctx context.Context, stagingRef string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.staged, stagingRef)
	return nil
}

// testEnv is one installation wired to a real storage database.
type testEnv struct {
	t       *testing.T
	ctx     context.Context
	db      contract.Database
	svc     *Service
	ports   *fakePorts
	blobs   *fakeBlobs
	clock   *fakeClock
	ids     *seqIDs
	actor   contract.Actor
	install contract.ID
	owner   contract.ID
	org     contract.ID // bootstrap root organization
	chief   contract.ID // bootstrap root chief
	scope   wireScope
}

// newEnv opens a fresh database, migrates the configuration owner and
// bootstraps one installation with its root organization and chief.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "configuration-test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env := &testEnv{
		t: t, ctx: ctx, db: db,
		ports: newFakePorts(), blobs: newFakeBlobs(),
		clock: &fakeClock{now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		ids:   &seqIDs{},
	}
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids, Ports: env.ports, Blobs: env.blobs})
	if err != nil {
		t.Fatalf("configuration.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate configuration: %v", err)
	}
	env.install = env.ids.New()
	env.owner = env.ids.New()
	env.actor = contract.Actor{PrincipalID: env.owner, Kind: contract.KindService}
	env.scope = wireScope{InstallationID: env.install}
	env.bootstrap()
	return env
}

func (e *testEnv) bootstrap() {
	e.t.Helper()
	org, chief := e.ids.New(), e.ids.New()
	e.mustOK("_configuration.bootstrap", bootstrapIn{
		InstallationID: e.install, OwnerID: e.owner, OrganizationID: org, ChiefID: chief,
	})
	e.org, e.chief = org, chief
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

// mustOKAs runs an operation on an arbitrary scope and requires success —
// the snapshot and cross-scope entry points.
func (e *testEnv) mustOKAs(scope wireScope, op string, in any) contract.Payload {
	e.t.Helper()
	payload, err := e.callAs(scope, op, in)
	if err != nil {
		e.t.Fatalf("%s failed: %v", op, err)
	}
	if payload.Status != contract.StatusCompleted || payload.Error != nil {
		e.t.Fatalf("%s did not complete: status %q error %v", op, payload.Status, payload.Error)
	}
	return payload
}

// tryPlan seals a draft without failing the test, returning the plan and the
// refusal (if any) so tests can assert diagnostics-only sealing.
func (e *testEnv) tryPlan(d wireDraft) (wirePlan, error) {
	e.t.Helper()
	payload, err := e.call("configuration.plan", planInput{
		Scope: e.scope, DraftID: d.ID, ExpectedVersion: d.Version,
	})
	if err != nil {
		return wirePlan{}, err
	}
	if payload.Error != nil {
		return wirePlan{}, payload.Error
	}
	var out struct {
		Resource wirePlan `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource, nil
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

// ---------- operation wrappers ----------

func (e *testEnv) stage(change wireChange) wireDraft {
	e.t.Helper()
	payload := e.mustOK("_configuration.stage", stageInput{Scope: e.scope, Change: change})
	var out struct {
		Resource wireDraft `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource
}

// stageInto stages a change into an existing draft and returns the update.
func (e *testEnv) stageInto(d wireDraft, change wireChange) wireDraft {
	e.t.Helper()
	id := d.ID
	payload := e.mustOK("_configuration.stage", stageInput{Scope: e.scope, Change: change, DraftID: &id})
	var out struct {
		Resource wireDraft `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource
}

func (e *testEnv) planDraft(d wireDraft) wirePlan {
	e.t.Helper()
	payload := e.mustOK("configuration.plan", planInput{Scope: e.scope, DraftID: d.ID, ExpectedVersion: d.Version})
	var out struct {
		Resource wirePlan `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource
}

func (e *testEnv) apply(p wirePlan) wireRevision {
	e.t.Helper()
	payload := e.mustOK("configuration.apply", applyInput{
		Scope: e.scope, PlanID: p.ID, BaseRevision: p.BaseRevision, CandidateDigest: p.CandidateDigest,
	})
	var out struct {
		Resource wireRevision `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource
}

// applyDraft plans and applies a staged draft.
func (e *testEnv) applyDraft(d wireDraft) wireRevision {
	e.t.Helper()
	return e.apply(e.planDraft(d))
}

// applyAll stages every change into one bundle and applies it, returning the
// sealed plan for assertions on dependencies and diagnostics.
func (e *testEnv) applyAll(changes ...wireChange) wirePlan {
	e.t.Helper()
	draft := e.stage(changes[0])
	for _, c := range changes[1:] {
		draft = e.stageInto(draft, c)
	}
	plan := e.planDraft(draft)
	e.apply(plan)
	return plan
}

// applyChange stages one change as its own bundle and applies it.
func (e *testEnv) applyChange(c wireChange) wirePlan {
	e.t.Helper()
	return e.applyAll(c)
}

// ---------- resource fixtures ----------

func orgChangeDef(id, parent *contract.ID, chief contract.ID, key string) wireChange {
	def := wireOrganization{ID: *id, Version: 1, Key: key, Name: "Org " + key, ChiefID: chief}
	if parent != nil {
		def.ParentID = parent
	}
	return createChange(kindOrganization, *id, def)
}

func newWorkerDef(id, org contract.ID, key string) wireWorker {
	return wireWorker{
		ID: id, Version: 1, OrganizationID: org, Key: key, Name: "Worker " + key,
		SkillVersions: []wireRef{}, Bindings: []contract.ID{}, Profile: nil, Limits: nil,
	}
}

func workerChange(id, org contract.ID, key string) wireChange {
	return createChange(kindWorker, id, newWorkerDef(id, org, key))
}

func newTeamDef(id, org contract.ID, key string, workers []contract.ID) wireTeam {
	return wireTeam{ID: id, Version: 1, OrganizationID: org, Key: key, Name: "Team " + key, WorkerIDs: idsOrEmpty(workers)}
}

func newProjectDef(id, org contract.ID, key string) wireProject {
	return wireProject{ID: id, Version: 1, OrganizationID: org, Key: key, Name: "Project " + key,
		Repositories: []string{"example/repo"}, Bindings: []contract.ID{}, Classification: "internal"}
}

func newBindingDef(id contract.ID, scope wireScope, target contract.ID) wireBinding {
	return wireBinding{ID: id, Version: 1, Scope: scope, Kind: "worker", TargetID: target,
		Permissions: []string{"configuration.read"}}
}

// createOrg stages and applies a child organization with its chief and
// returns both identities.
func (e *testEnv) createOrg(parent *contract.ID, key string) (contract.ID, contract.ID) {
	e.t.Helper()
	orgID, chiefID := e.ids.New(), e.ids.New()
	org := orgChangeDef(&orgID, parent, chiefID, key)
	chief := workerChange(chiefID, orgID, key+"-chief")
	e.applyAll(org, chief)
	return orgID, chiefID
}

// createWorker stages and applies one worker in the organization.
func (e *testEnv) createWorker(org contract.ID, key string) contract.ID {
	e.t.Helper()
	id := e.ids.New()
	e.applyChange(workerChange(id, org, key))
	return id
}

// createTeam stages and applies a team with the given members.
func (e *testEnv) createTeam(org contract.ID, key string, workers ...contract.ID) contract.ID {
	e.t.Helper()
	id := e.ids.New()
	e.applyChange(createChange(kindTeam, id, newTeamDef(id, org, key, workers)))
	return id
}

// createBinding stages and applies one worker binding scoped to org.
func (e *testEnv) createBinding(org, target contract.ID) contract.ID {
	e.t.Helper()
	id := e.ids.New()
	scope := e.scope
	scope.OrganizationID = org
	scope.WorkerID = target
	e.applyChange(createChange(kindBinding, id, newBindingDef(id, scope, target)))
	return id
}

// importRequest builds the organization.import input for an exported
// artifact, optionally rebinding opaque credential references.
func importRequest(e *testEnv, artifact *wireArtifactRef, rebindings []importRebinding) any {
	if rebindings == nil {
		rebindings = []importRebinding{}
	}
	return struct {
		Scope      wireScope         `json:"scope"`
		Artifact   wireArtifactRef   `json:"artifact"`
		Rebindings []importRebinding `json:"rebindings"`
	}{Scope: e.scope, Artifact: *artifact, Rebindings: rebindings}
}

// ---------- resource readers ----------

func (e *testEnv) getOrg(id contract.ID) wireOrganization {
	payload := e.mustOK("organization.get", getInput{Scope: e.scope, ID: id})
	var out struct {
		Resource wireOrganization `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource
}

func (e *testEnv) getWorker(id contract.ID) wireWorker {
	payload := e.mustOK("worker.get", getInput{Scope: e.scope, ID: id})
	var out struct {
		Resource wireWorker `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource
}

func (e *testEnv) getTeam(id contract.ID) wireTeam {
	payload := e.mustOK("team.get", getInput{Scope: e.scope, ID: id})
	var out struct {
		Resource wireTeam `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource
}

func (e *testEnv) getProject(id contract.ID) wireProject {
	payload := e.mustOK("project.get", getInput{Scope: e.scope, ID: id})
	var out struct {
		Resource wireProject `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource
}

func (e *testEnv) listItems(op string, in listInput) []json.RawMessage {
	e.t.Helper()
	payload := e.mustOK(op, in)
	var out struct {
		Items []json.RawMessage `json:"items"`
	}
	e.decode(payload.Data, &out)
	return out.Items
}

// head reads the current configuration revision inside one write transaction.
func (e *testEnv) head() int64 {
	e.t.Helper()
	var head int64
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		head, err = readHead(e.ctx, unit)
		return err
	}); err != nil {
		e.t.Fatalf("read head: %v", err)
	}
	return head
}

// createChange builds one create wireChange around a typed definition.
func createChange(kind string, id contract.ID, def any) wireChange {
	return wireChange{Kind: kind, Action: actionCreate, ID: id, ExpectedVersion: 0, Definition: rawDef(def)}
}

// rowState reads the live lifecycle state of one owned object directly from
// its row — the wire resource bodies carry definitions only.
func (e *testEnv) rowState(kind string, id contract.ID) (string, bool) {
	e.t.Helper()
	var state string
	var found bool
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		switch kind {
		case kindOrganization:
			var row *orgRow
			row, err = fetchOrgByID(e.ctx, unit, e.install, id)
			if row != nil {
				state, found = row.State, true
			}
		case kindWorker:
			var row *workerRow
			row, err = fetchWorkerByID(e.ctx, unit, e.install, id)
			if row != nil {
				state, found = row.State, true
			}
		case kindTeam:
			var row *teamRow
			row, err = fetchTeamByID(e.ctx, unit, e.install, id)
			if row != nil {
				state, found = row.State, true
			}
		case kindProject:
			var row *projectRow
			row, err = fetchProjectByID(e.ctx, unit, e.install, id)
			if row != nil {
				state, found = row.State, true
			}
		case kindBinding:
			var row *bindingRow
			row, err = fetchBindingByID(e.ctx, unit, e.install, id)
			if row != nil {
				state, found = row.State, true
			}
		}
		return err
	}); err != nil {
		e.t.Fatalf("read %s state: %v", kind, err)
	}
	return state, found
}
