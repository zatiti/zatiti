package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// Test harness: a real storage database per test and a deterministic fake
// clock, driving every operation through Service.Handle inside storage
// write/read transactions — the same style internal/effects uses.

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

func (c *fakeClock) Advance(d time.Duration) {
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

// testEnv is one installation wired to a real storage database.
type testEnv struct {
	t       *testing.T
	ctx     context.Context
	db      contract.Database
	svc     *Service
	clock   *fakeClock
	ids     *seqIDs
	actor   contract.Actor
	install contract.ID
	scope   contract.Scope
}

// newEnv opens a fresh database, migrates the evidence owner and wires one
// installation scope with a service actor.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "evidence-test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env := &testEnv{
		t: t, ctx: ctx, db: db,
		clock: &fakeClock{now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		ids:   &seqIDs{},
	}
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids})
	if err != nil {
		t.Fatalf("evidence.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate evidence: %v", err)
	}
	if _, err := db.StartGeneration(ctx); err != nil {
		t.Fatalf("start generation: %v", err)
	}
	env.install = env.ids.New()
	principal := env.ids.New()
	env.actor = contract.Actor{PrincipalID: principal, Kind: contract.KindService}
	env.scope = contract.Scope{InstallationID: env.install}
	return env
}

// ---------- call helpers ----------

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

func (e *testEnv) callReadOnly(op string, in any) (contract.Payload, error) {
	return e.callReadOnlyAs(e.actor, e.scope, op, in)
}

func (e *testEnv) callReadOnlyAs(actor contract.Actor, scope contract.Scope, op string, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Read(e.ctx, actor, scope, func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		payload = p
		return err
	})
	return payload, err
}

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

// mustOK runs an operation and requires a completed payload.
func (e *testEnv) mustOK(op string, in any) contract.Payload {
	e.t.Helper()
	return e.mustOKAs(e.actor, e.scope, op, in)
}

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

func (e *testEnv) decode(raw json.RawMessage, out any) {
	e.t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		e.t.Fatalf("decode payload: %v", err)
	}
}

// ---------- fixture builders ----------

// digestOf returns the canonical request digest of an arbitrary payload, the
// same way application.requestDigest binds a mutation's input bytes.
func digestOf(t *testing.T, v any) contract.Digest {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal digest input: %v", err)
	}
	canonical, err := contract.Canonicalize(raw)
	if err != nil {
		t.Fatalf("canonicalize digest input: %v", err)
	}
	sum := sha256.Sum256(canonical)
	return contract.Digest(hex.EncodeToString(sum[:]))
}

// beginCommand runs _evidence.command.begin and decodes its output.
func (e *testEnv) beginCommand(principal contract.ID, operation string, version int64, key string, digest contract.Digest) commandBeginOutput {
	e.t.Helper()
	payload := e.mustOK(opCommandBegin, commandBeginInput{
		PrincipalID: principal, Operation: operation, OperationVersion: version,
		SubmissionKey: key, RequestDigest: digest,
	})
	var out commandBeginOutput
	e.decode(payload.Data, &out)
	return out
}

// finishCommandOp runs _evidence.command.finish and decodes its output.
func (e *testEnv) finishCommandOp(commandID contract.ID, result contract.Result) commandResourceBody {
	e.t.Helper()
	payload := e.mustOK(opCommandFinish, commandFinishInput{CommandID: commandID, Result: result})
	var out commandResourceBody
	e.decode(payload.Data, &out)
	return out
}

// completeResult builds a schema-valid completed Result for commandID.
func completeResult(commandID contract.ID, data json.RawMessage) contract.Result {
	return contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: commandID,
		Payload: contract.Payload{
			Status: contract.StatusCompleted,
			Data:   data,
		},
	}
}

// failedResult builds a schema-valid failed Result for commandID.
func failedResult(commandID contract.ID, code, message string) contract.Result {
	return contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: commandID,
		Payload: contract.Payload{
			Status: contract.StatusFailed,
			Error:  &contract.Fault{Code: code, Message: message},
		},
	}
}

// emitEvent emits one synthetic event under scope in its own write
// transaction, simulating another owner's state transition.
func (e *testEnv) emitEvent(scope contract.Scope, kind string, resourceID contract.ID) contract.Event {
	e.t.Helper()
	var emitted contract.Event
	err := e.db.Write(e.ctx, e.actor, scope, func(unit contract.Unit) error {
		return unit.Emit(e.ctx, contract.Event{Kind: kind, ResourceID: resourceID, ResourceVersion: 1})
	})
	if err != nil {
		e.t.Fatalf("emit event: %v", err)
	}
	evs, err := e.db.Events(e.ctx, 0, 500)
	if err != nil {
		e.t.Fatalf("list events: %v", err)
	}
	for _, ev := range evs {
		if ev.ResourceID == resourceID && ev.Kind == kind {
			emitted = ev
		}
	}
	if emitted.ID == "" {
		e.t.Fatalf("emitted event for %s/%s not found in storage", kind, resourceID)
	}
	return emitted
}

// emitEventWithData emits one synthetic event carrying an inert data
// payload, in its own write transaction under scope.
func (e *testEnv) emitEventWithData(scope contract.Scope, kind string, resourceID contract.ID, data json.RawMessage) contract.Event {
	e.t.Helper()
	err := e.db.Write(e.ctx, e.actor, scope, func(unit contract.Unit) error {
		return unit.Emit(e.ctx, contract.Event{Kind: kind, ResourceID: resourceID, ResourceVersion: 1, Data: data})
	})
	if err != nil {
		e.t.Fatalf("emit event: %v", err)
	}
	evs, err := e.db.Events(e.ctx, 0, 500)
	if err != nil {
		e.t.Fatalf("list events: %v", err)
	}
	for _, ev := range evs {
		if ev.ResourceID == resourceID && ev.Kind == kind {
			return ev
		}
	}
	e.t.Fatalf("emitted event for %s/%s not found in storage", kind, resourceID)
	return contract.Event{}
}

// getEvent runs event.get and decodes its output.
func (e *testEnv) getEvent(scope contract.Scope, id contract.ID) (contract.Event, error) {
	e.t.Helper()
	payload, err := e.callReadOnlyAs(e.actor, scope, opEventGet, eventGetInput{Scope: scope, ID: id})
	if err != nil {
		return contract.Event{}, err
	}
	if payload.Error != nil {
		return contract.Event{}, payload.Error
	}
	var out eventResourceBody
	e.decode(payload.Data, &out)
	return out.Resource, nil
}

// listEvents runs event.list and decodes its output and next cursor.
func (e *testEnv) listEvents(scope contract.Scope, cursor *string, limit *int64, filter *eventFilterInput) ([]contract.Event, *string) {
	e.t.Helper()
	payload := e.mustOKAs(e.actor, scope, opEventList, eventListInput{Scope: scope, Cursor: cursor, Limit: limit, Filter: filter})
	var out eventListOutput
	e.decode(payload.Data, &out)
	return out.Items, payload.NextCursor
}
