package memory

import (
	"context"
	"encoding/json"
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
// Service.Handle inside storage read/write transactions. The fake ports
// serve the seven peer operations this package actually calls (policy,
// configuration snapshot/stage, artifacts metadata/publish, effects
// prepare, execution job create/record) with injectable state, recording
// every invocation for behavioral assertions.

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// seqIDs mints syntactically valid UUIDv4-shaped identities in sequence.
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

// fakePorts serves every peer operation the handlers call.
type fakePorts struct {
	mu    sync.Mutex
	ids   *seqIDs
	calls []contract.Invocation
	fail  map[string]*contract.Fault

	configRevision int64
	policyDecision string
	policyReasons  []string

	artifactStates map[contract.ID]string

	execJobVersion int64
}

func newFakePorts(ids *seqIDs) *fakePorts {
	return &fakePorts{
		ids: ids, fail: map[string]*contract.Fault{}, configRevision: 3,
		policyDecision: "allow", artifactStates: map[contract.ID]string{}, execJobVersion: 1,
	}
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	p.calls = append(p.calls, inv)
	injected := p.fail[inv.Operation]
	revision := p.configRevision
	decision := p.policyDecision
	reasons := append([]string{}, p.policyReasons...)
	artifactStates := make(map[contract.ID]string, len(p.artifactStates))
	for id, st := range p.artifactStates {
		artifactStates[id] = st
	}
	p.mu.Unlock()
	if injected != nil {
		return contract.Payload{}, injected
	}

	var body any
	switch inv.Operation {
	case opPolicyCheck:
		body = policyResultBody{Resource: wirePolicyResult{Decision: decision, Reasons: reasons, Requirements: []json.RawMessage{}}}
	case opConfigSnapshot:
		var in configurationSnapshotInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		snap := snapshotBody{}
		snap.Resource.Scope = in.Scope
		snap.Resource.Revision = revision
		snap.Resource.Ancestors = []wireOrganization{}
		snap.Resource.Bindings = []wireBinding{}
		body = snap
	case opConfigStage:
		var in configurationStageInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		draftID := p.ids.New()
		if in.DraftID != nil {
			draftID = *in.DraftID
		}
		body = draftResourceBody{Resource: wireDraft{ID: draftID, Version: 1, BaseRevision: revision,
			Changes: []json.RawMessage{in.Change}, Diagnostics: []wireDiagnostic{}}}
	case opArtifactsMetadata:
		var in artifactsMetadataInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		out := artifactsMetadataBody{}
		for _, ref := range in.Artifacts {
			state := artifactStates[ref.ID]
			if state == "" {
				state = "available"
			}
			out.Artifacts = append(out.Artifacts, struct {
				ID             contract.ID     `json:"id"`
				Version        int64           `json:"version"`
				Digest         contract.Digest `json:"digest"`
				State          string          `json:"state"`
				Classification string          `json:"classification"`
			}{ID: ref.ID, Version: 1, Digest: ref.Digest, State: state, Classification: "internal"})
		}
		body = out
	case opArtifactsPublish:
		var in artifactsPublishInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = artifactResourceBody{Resource: wireArtifact{ID: p.ids.New(), Version: 1, Scope: in.Scope,
			Digest: in.Digest, Size: in.Size, MediaType: in.MediaType, Classification: in.Classification,
			Encrypted: in.Encrypted, State: "available"}}
	case opEffectsPrepare:
		var in effectsPrepareInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = operationResourceBody{Resource: wireOperation{ID: p.ids.New(), Version: 1, Action: in.Action.Parameters,
			ActionDigest: fmt.Sprintf("%064x", 1), State: "prepared", AttemptIDs: []contract.ID{}}}
	case opExecutionJobCreate:
		var in executionJobCreateInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = jobResourceBody{Resource: wireJob{ID: p.ids.New(), Version: p.execJobVersion, Kind: "reconcile",
			State: "pending", Requirements: []wireRequirement{}, Owner: in.Owner, Operation: in.Operation}}
	case opExecutionJobRecord:
		var in executionJobRecordInput
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = jobResourceBody{Resource: wireJob{ID: in.JobID, Version: in.ExpectedVersion + 1, Kind: "reconcile",
			State: in.State, Requirements: []wireRequirement{}, Owner: ownerName, Operation: "memory.recall"}}
	default:
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: "fake ports: unexpected peer call " + inv.Operation}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}

func (p *fakePorts) opsCalled() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.calls))
	for _, c := range p.calls {
		out = append(out, c.Operation)
	}
	return out
}

func (p *fakePorts) resetCalls() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = nil
}

func (p *fakePorts) setPolicy(decision string, reasons ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.policyDecision, p.policyReasons = decision, reasons
}

func (p *fakePorts) setArtifactState(id contract.ID, state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.artifactStates[id] = state
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
	scope   contract.Scope
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "memory-test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env := &testEnv{t: t, ctx: ctx, db: db,
		clock: &fakeClock{now: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)}, ids: &seqIDs{}}
	env.ports = newFakePorts(env.ids)
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids, Ports: env.ports})
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate memory: %v", err)
	}
	if _, err := db.StartGeneration(ctx); err != nil {
		t.Fatalf("start generation: %v", err)
	}
	env.install = env.ids.New()
	owner := env.ids.New()
	env.actor = contract.Actor{PrincipalID: owner, Kind: contract.KindService}
	env.scope = contract.Scope{InstallationID: env.install}
	return env
}

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

// scopeAt builds a Scope in this environment's installation with the given
// organization/worker.
func (e *testEnv) scopeAt(org, worker contract.ID) contract.Scope {
	return contract.Scope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}
}

// mustMarshal marshals v to JSON, failing the test on error.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// jsonUnmarshal is a thin alias kept local to this package's tests so
// call sites read as domain vocabulary rather than a raw encoding import.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func decodeFault(err error) *contract.Fault {
	var f *contract.Fault
	if err == nil {
		return nil
	}
	if ff, ok := err.(*contract.Fault); ok {
		return ff
	}
	return f
}

// decodePayload strictly decodes one payload's data into v, failing the
// test on any error.
func (e *testEnv) decodePayload(payload contract.Payload, v any) {
	e.t.Helper()
	if payload.Error != nil {
		e.t.Fatalf("unexpected payload error: %v", payload.Error)
	}
	if err := json.Unmarshal(payload.Data, v); err != nil {
		e.t.Fatalf("decode payload: %v", err)
	}
}

// seedBrain writes one brain row directly, bypassing bootstrap, for tests
// that need a brain in a specific state (e.g. provisioned) that no public
// operation currently reaches.
func (e *testEnv) seedBrain(b *brainRow) {
	e.t.Helper()
	err := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		return insertBrain(e.ctx, unit, b)
	})
	if err != nil {
		e.t.Fatalf("seed brain: %v", err)
	}
}

// provisionedBrain returns a brain row with a bound connection/tool/writer,
// active and ready for dispatch.
func (e *testEnv) provisionedBrain(kind string, org, worker contract.ID) *brainRow {
	return &brainRow{
		ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker, Kind: kind,
		Classification: classificationInternal, State: brainActive, Endpoint: "serenity://test",
		WriterOwner: "test-writer", AllowedDestinations: []string{"serenity://test"},
		ConnectionRefID: e.ids.New(), ConnectionRefVersion: 1, ToolRefID: e.ids.New(), ToolRefVersion: 1,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now(),
	}
}

// seedBinding writes one binding row directly, bypassing the
// stage/activate lifecycle, for tests that only need an authorized binding
// in place.
func (e *testEnv) seedBinding(b *bindingRow) {
	e.t.Helper()
	err := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		return insertBinding(e.ctx, unit, b)
	})
	if err != nil {
		e.t.Fatalf("seed binding: %v", err)
	}
}

// seedClaim writes one claim row directly, for tests that exercise
// promote/retract/inspect without first running a full recall.
func (e *testEnv) seedClaim(c *claimRow) {
	e.t.Helper()
	err := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		return insertClaim(e.ctx, unit, c)
	})
	if err != nil {
		e.t.Fatalf("seed claim: %v", err)
	}
}
