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

// recordedReserve is one _accounting.reserve call: captures exactly what
// this package actually sent, not what the fake pretends to have received,
// so a test can assert against the real request shape (this fake never
// schema-validates its input, unlike the real internal/accounting service --
// see run_ops_test.go's TestClaimReservesBudgetWithAValidOperationID).
type recordedReserve struct {
	OperationID contract.ID
	RootTaskID  contract.ID
}

// recordedProcessed is one _messaging.processed call.
type recordedProcessed struct {
	MessageID   contract.ID
	RecipientID contract.ID
	TurnID      contract.ID
}

// recordedEvidence is one _tasks.evidence.record call: P18's real,
// non-fabricated binding of a trusted verdict to a task.
type recordedEvidence struct {
	TaskID               contract.ID
	AttemptID            contract.ID
	ExpectedVersion      contract.Version
	AcceptanceDigest     contract.Digest
	VerificationArtifact wireArtifactRef
	OutputBindings       []reportBindingProposal
	Verdict              string
}

// recordedPublish is one _artifacts.publish call.
type recordedPublish struct {
	Scope          contract.Scope
	Digest         contract.Digest
	Size           int64
	MediaType      string
	Classification string
	Encrypted      bool
}

// recordedEffectsPrepare is one full _effects.prepare call: the real
// dispatched action shape and callback route, not just the minted operation
// id PreparedOps() already exposes. Used to assert exactly which Responses
// action kind (prepare_session vs model_step) a real dispatch sent.
type recordedEffectsPrepare struct {
	ID            contract.ID
	Scope         contract.Scope
	SourceID      contract.ID
	Action        map[string]any
	CallbackRoute map[string]any
}

// ActionKind reads the "kind" discriminator of the dispatched action's own
// "parameters" field -- the ResponsesParameters oneOf discriminator
// (prepare_session vs model_step).
func (r recordedEffectsPrepare) ActionKind() string {
	params, _ := r.Action["parameters"].(map[string]any)
	kind, _ := params["kind"].(string)
	return kind
}

// fakePorts serves the peer fixtures per installation and task, records the
// calls handlers make, and carries injectable faults and raw errors.
type fakePorts struct {
	mu              sync.Mutex
	snapshots       map[contract.ID]*peerScopeSnapshot
	tasks           map[contract.ID]*wireTask
	artifacts       map[contract.Digest]wireArtifact
	messages        map[contract.ID][]wireMessage
	memoryBindings  map[contract.ID]wireMemoryBinding
	connections     map[contract.ID]wireConnection
	tools           map[contract.ID]wireTool
	fail            map[string]*contract.Fault
	rawFail         map[string]error
	transitions     []recordedTransition
	settles         []recordedSettle
	reserves        []recordedReserve
	prepared        []contract.ID
	effectsPrepared []recordedEffectsPrepare
	processed       []recordedProcessed
	evidence        []recordedEvidence
	published       []recordedPublish
	seq             int
}

func newFakePorts() *fakePorts {
	return &fakePorts{
		snapshots:      map[contract.ID]*peerScopeSnapshot{},
		tasks:          map[contract.ID]*wireTask{},
		artifacts:      map[contract.Digest]wireArtifact{},
		messages:       map[contract.ID][]wireMessage{},
		memoryBindings: map[contract.ID]wireMemoryBinding{},
		connections:    map[contract.ID]wireConnection{},
		tools:          map[contract.ID]wireTool{},
		fail:           map[string]*contract.Fault{},
		rawFail:        map[string]error{},
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

// setMessages installs the pending inbox _messaging.pending serves for one
// worker.
func (p *fakePorts) setMessages(workerID contract.ID, msgs []wireMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages[workerID] = msgs
}

// setConnection installs one connection _connections.resolve validates.
func (p *fakePorts) setConnection(c wireConnection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connections[c.ID] = c
}

// setTool installs one tool _connections.resolve validates.
func (p *fakePorts) setTool(t wireTool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tools[t.ID] = t
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

func (p *fakePorts) ReserveCalls() []recordedReserve {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedReserve(nil), p.reserves...)
}

func (p *fakePorts) PreparedOps() []contract.ID {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]contract.ID(nil), p.prepared...)
}

// EffectsPrepared returns every full _effects.prepare call observed, in
// order.
func (p *fakePorts) EffectsPrepared() []recordedEffectsPrepare {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedEffectsPrepare(nil), p.effectsPrepared...)
}

func (p *fakePorts) Processed() []recordedProcessed {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedProcessed(nil), p.processed...)
}

func (p *fakePorts) Transitions() []recordedTransition {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedTransition(nil), p.transitions...)
}

// EvidenceRecords returns every _tasks.evidence.record call observed.
func (p *fakePorts) EvidenceRecords() []recordedEvidence {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedEvidence(nil), p.evidence...)
}

// Published returns every _artifacts.publish call observed.
func (p *fakePorts) Published() []recordedPublish {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedPublish(nil), p.published...)
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
		var in map[string]any
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		rec := recordedReserve{}
		if opID, ok := in["operation_id"].(string); ok {
			rec.OperationID = contract.ID(opID)
		}
		if rootID, ok := in["root_task_id"].(string); ok {
			rec.RootTaskID = contract.ID(rootID)
		}
		p.reserves = append(p.reserves, rec)
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
	case peerMessagingProcessed:
		var in struct {
			MessageID   contract.ID `json:"message_id"`
			RecipientID contract.ID `json:"recipient_id"`
			TurnID      contract.ID `json:"turn_id"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		p.processed = append(p.processed, recordedProcessed{
			MessageID: in.MessageID, RecipientID: in.RecipientID, TurnID: in.TurnID,
		})
		body = map[string]any{"resource": map[string]any{
			"id": in.MessageID, "version": 2, "sender_id": p.nextID(),
			"recipient_ids": []contract.ID{in.RecipientID}, "scope": map[string]any{"installation_id": p.nextID()},
			"task_ids": []any{}, "body": "", "attachments": []any{}, "state": "acknowledged",
			"created_at": "2026-09-10T12:00:00.000000000Z",
		}}
	case peerMessagingPending:
		var in struct {
			WorkerID contract.ID `json:"worker_id"`
			Limit    int64       `json:"limit"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		items := p.messages[in.WorkerID]
		if items == nil {
			items = []wireMessage{}
		}
		body = map[string]any{"items": items}
	case peerMemorySelect:
		var in struct {
			Scope            contract.Scope `json:"scope"`
			BindingIDs       []contract.ID  `json:"binding_ids"`
			Permission       string         `json:"permission"`
			MinimumFreshness string         `json:"minimum_freshness"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		bindings := []wireMemoryBinding{}
		for _, id := range in.BindingIDs {
			b, ok := p.memoryBindings[id]
			if !ok {
				p.mu.Unlock()
				return contract.Payload{}, notFound("memory binding %s is unknown", id)
			}
			bindings = append(bindings, b)
		}
		body = map[string]any{"bindings": bindings}
	case peerConnectionsResolve:
		var in struct {
			Scope       contract.Scope `json:"scope"`
			Connection  wireRef        `json:"connection"`
			Tool        wireRef        `json:"tool"`
			Destination string         `json:"destination"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		conn, ok := p.connections[in.Connection.ID]
		if !ok {
			p.mu.Unlock()
			return contract.Payload{}, notFound("connection %s is unknown", in.Connection.ID)
		}
		if conn.Version != in.Connection.Version {
			p.mu.Unlock()
			return contract.Payload{}, staleVersion("connection %s is at a different version", in.Connection.ID)
		}
		tool, ok := p.tools[in.Tool.ID]
		if !ok {
			p.mu.Unlock()
			return contract.Payload{}, notFound("tool %s is unknown", in.Tool.ID)
		}
		if tool.Version != in.Tool.Version {
			p.mu.Unlock()
			return contract.Payload{}, staleVersion("tool %s is at a different version", in.Tool.ID)
		}
		body = map[string]any{"connection": conn, "tool": tool}
	case "_effects.prepare":
		var in map[string]any
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		id := p.nextID()
		p.prepared = append(p.prepared, id)
		rec := recordedEffectsPrepare{ID: id}
		if action, ok := in["action"].(map[string]any); ok {
			rec.Action = action
		}
		if sourceID, ok := in["source_id"].(string); ok {
			rec.SourceID = contract.ID(sourceID)
		}
		if route, ok := in["callback_route"].(map[string]any); ok {
			rec.CallbackRoute = route
		}
		if scopeRaw, ok := in["scope"]; ok {
			raw, err := json.Marshal(scopeRaw)
			if err == nil {
				_ = json.Unmarshal(raw, &rec.Scope)
			}
		}
		p.effectsPrepared = append(p.effectsPrepared, rec)
		body = struct {
			Resource struct {
				ID contract.ID `json:"id"`
			} `json:"resource"`
		}{struct {
			ID contract.ID `json:"id"`
		}{id}}
	case peerArtifactsPublish:
		var in struct {
			Scope          contract.Scope  `json:"scope"`
			Digest         contract.Digest `json:"digest"`
			Size           int64           `json:"size"`
			MediaType      string          `json:"media_type"`
			Classification string          `json:"classification"`
			Encrypted      bool            `json:"encrypted"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		p.published = append(p.published, recordedPublish{
			Scope: in.Scope, Digest: in.Digest, Size: in.Size,
			MediaType: in.MediaType, Classification: in.Classification, Encrypted: in.Encrypted,
		})
		id := p.nextID()
		art := wireArtifact{
			ID: id, Version: 1, Scope: in.Scope, Digest: in.Digest, Size: in.Size,
			MediaType: in.MediaType, Classification: in.Classification, Encrypted: in.Encrypted,
			State: "available", CreatedAt: "2026-09-10T12:00:00.000000000Z",
		}
		body = struct {
			Resource wireArtifact `json:"resource"`
		}{art}
	case peerTasksEvidenceRecord:
		var in struct {
			TaskID               contract.ID             `json:"task_id"`
			AttemptID            contract.ID             `json:"attempt_id"`
			ExpectedVersion      contract.Version        `json:"expected_version"`
			AcceptanceDigest     contract.Digest         `json:"acceptance_digest"`
			VerificationArtifact wireArtifactRef         `json:"verification_artifact"`
			OutputBindings       []reportBindingProposal `json:"output_bindings"`
			Verdict              string                  `json:"verdict"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			p.mu.Unlock()
			return contract.Payload{}, err
		}
		task, ok := p.tasks[in.TaskID]
		if !ok {
			p.mu.Unlock()
			return contract.Payload{}, notFound("task %s does not exist", in.TaskID)
		}
		if task.Version != in.ExpectedVersion {
			p.mu.Unlock()
			return contract.Payload{}, staleVersion("task %s is at a different version", in.TaskID)
		}
		p.evidence = append(p.evidence, recordedEvidence{
			TaskID: in.TaskID, AttemptID: in.AttemptID, ExpectedVersion: in.ExpectedVersion,
			AcceptanceDigest: in.AcceptanceDigest, VerificationArtifact: in.VerificationArtifact,
			OutputBindings: in.OutputBindings, Verdict: in.Verdict,
		})
		task.Version++
		body = struct {
			Resource wireTask `json:"resource"`
		}{*task}
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
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids, Ports: env.ports, Blobs: newFakeBlobStore(nil)})
	if err != nil {
		t.Fatalf("execution.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate execution: %v", err)
	}
	// Assembly starts the controller generation once before serving; attempts
	// and leases bind it, so the fixture starts generation 1.
	if _, err := db.StartGeneration(ctx); err != nil {
		t.Fatalf("start generation: %v", err)
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

// generation returns the persisted controller generation.
func (e *testEnv) generation() int64 {
	e.t.Helper()
	gen, err := e.db.Generation(e.ctx)
	if err != nil {
		e.t.Fatalf("generation: %v", err)
	}
	return gen
}

// advanceGeneration simulates a controller restart: assembly advances the
// persisted generation before the new controller fences and admits.
func (e *testEnv) advanceGeneration() int64 {
	e.t.Helper()
	gen, err := e.db.StartGeneration(e.ctx)
	if err != nil {
		e.t.Fatalf("start generation: %v", err)
	}
	return gen
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
// fixtureOutputRef mints a fresh ArtifactRef for the fixture acceptance's
// single named output slot ("result") and registers it as a published
// artifact _artifacts.metadata resolves -- reportAttempt's output-slot
// resolution (P18) requires exactly this, the same registration
// interpret_test.go's report_outputs fixtures already use for the model-
// driven path.
func (e *testEnv) fixtureOutputRef() wireArtifactRef {
	e.t.Helper()
	ref := wireArtifactRef{ID: e.ids.New(), Digest: fixtureDigest}
	e.registerArtifact(ref)
	return ref
}

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
// the env installation: the hosted worker profile, revision 1, non-empty
// instructions (P15's context builder refuses a worker with none) and no
// tool/memory bindings (the "empty bindings yield no tools" default).
func (e *testEnv) installWorkerSnapshot(workerID contract.ID, profile *wireExecutionProfile) {
	limits := fixtureLimits(4)
	e.ports.setSnapshot(e.install, &peerScopeSnapshot{
		Scope:    e.scope,
		Revision: 1,
		Worker: &wireWorker{
			ID: workerID, Version: 1, OrganizationID: e.org,
			Key: "worker", Name: "Worker", Instructions: "You are a test worker; follow the accepted task.",
			SkillVersions: []wireRef{}, Bindings: []contract.ID{},
			Profile: profile, Limits: &limits,
		},
		Bindings: []wireBinding{},
	})
}

// installModelToolBinding extends the installed worker snapshot with one
// authorized "tool" binding whose resolved Tool carries adapter "responses"
// -- the model-dispatch tool/connection identity buildResponsesModelStepAction
// requires. It must run after installWorkerSnapshot for the same worker.
func (e *testEnv) installModelToolBinding(workerID contract.ID, profile *wireExecutionProfile) (toolID, connectionID contract.ID) {
	e.t.Helper()
	snap := e.ports.snapshots[e.install]
	if snap == nil || snap.Worker == nil {
		e.t.Fatalf("installModelToolBinding: no worker snapshot installed for %s", workerID)
	}
	bindingID := e.ids.New()
	toolID = e.ids.New()
	connectionID = profile.ConnectionID
	snap.Worker.Bindings = append(snap.Worker.Bindings, bindingID)
	snap.Bindings = append(snap.Bindings, wireBinding{
		ID: bindingID, Version: 1, Scope: e.scope, Kind: "tool",
		TargetID: toolID, Permissions: []string{"invoke"},
		Destinations: []string{profile.ProviderDestination},
	})
	e.ports.setConnection(wireConnection{
		ID: connectionID, Version: defaultResolveVersion, Provider: "openai",
		AccountIdentity: "acct-test", Destinations: []string{profile.ProviderDestination},
	})
	e.ports.setTool(wireTool{
		ID: toolID, Version: defaultResolveVersion, Name: "model-responses",
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
		Effect: "external_mutation", Destinations: []string{profile.ProviderDestination},
		Adapter: "responses",
	})
	return toolID, connectionID
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

// readJob loads one durable job row directly.
func (e *testEnv) readJob(id contract.ID) *jobRow {
	e.t.Helper()
	var j *jobRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		j, err = loadJob(e.ctx, unit, id)
		return err
	})
	return j
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

// readContextPlan loads one context plan row directly.
func (e *testEnv) readContextPlan(id contract.ID) *contextPlanRow {
	e.t.Helper()
	var p *contextPlanRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		p, err = loadContextPlan(e.ctx, unit, id)
		return err
	})
	return p
}

// readTurn loads one worker turn row directly.
func (e *testEnv) readTurn(id contract.ID) *turnRow {
	e.t.Helper()
	var row *turnRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		row, err = loadTurn(e.ctx, unit, id)
		return err
	})
	return row
}

// findTurnForSource looks up the turn admitted for one exact source
// identity directly, or nil.
func (e *testEnv) findTurnForSource(sourceKind string, sourceID contract.ID, sourceVersion contract.Version, workerID contract.ID) *turnRow {
	e.t.Helper()
	var row *turnRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		row, err = findTurnBySource(e.ctx, unit, e.install, sourceKind, sourceID, sourceVersion, workerID)
		return err
	})
	return row
}

// countLiveTurnsForWorker counts the worker's non-terminal turns directly.
func (e *testEnv) countLiveTurnsForWorker(workerID contract.ID) int {
	e.t.Helper()
	var rows []*turnRow
	e.inWrite(func(unit contract.Unit) error {
		var err error
		rows, err = listTurns(e.ctx, unit,
			[]string{"installation_id = ?", "worker_id = ?", "state NOT IN ('completed','failed','cancelled')"},
			[]any{e.install, workerID}, 4096)
		return err
	})
	return len(rows)
}

// seedProposal directly inserts a durable proposal record, simulating a
// model step's normalized evidence already persisted by the interpretation
// stage (P16) that _execution.proposal.prepare/.record build on.
func (e *testEnv) seedProposal(turnID contract.ID, stepIndex int64, proposalID string) *proposalRow {
	e.t.Helper()
	row := &proposalRow{
		ID:                  e.ids.New(),
		InstallationID:      e.install,
		TurnID:              turnID,
		StepIndex:           stepIndex,
		ProposalID:          proposalID,
		SourceContextDigest: fixtureDigest,
		NormalizedProposal:  json.RawMessage(`{"kind":"reply","text":"ok"}`),
		State:               "prepared",
		CreatedAt:           e.clock.Now(),
		UpdatedAt:           e.clock.Now(),
	}
	e.inWrite(func(unit contract.Unit) error {
		return insertProposal(e.ctx, unit, row)
	})
	return row
}

// fixtureCooperativeProfile builds a cooperative execution profile: never
// auto-claimed, always reachable only through the public run.claim path.
func fixtureCooperativeProfile(workerID contract.ID) *wireExecutionProfile {
	p := fixtureHostedProfile(workerID)
	p.Executor = "cooperative"
	return p
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
