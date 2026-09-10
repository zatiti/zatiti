package reviews

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
// clock, ids and the identity authority port, and helpers that drive every
// operation through Service.Handle inside storage write transactions.

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

// advance moves the clock forward by d.
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

// authSpec is one principal's current identity state served by the fake
// authority port.
type authSpec struct {
	kind    string
	revoked bool
}

// fakePorts serves _identity.authority from a fixed principal table with
// injectable faults, recording every call. Any other operation is an
// internal error: reviews consults exactly one peer.
type fakePorts struct {
	mu       sync.Mutex
	calls    []contract.Invocation
	auth     map[contract.ID]authSpec
	fail     map[string]*contract.Fault
	notFound map[contract.ID]bool
}

func newFakePorts() *fakePorts {
	return &fakePorts{
		auth:     map[contract.ID]authSpec{},
		fail:     map[string]*contract.Fault{},
		notFound: map[contract.ID]bool{},
	}
}

// setAuthority registers or updates one principal's identity.
func (p *fakePorts) setAuthority(id contract.ID, kind string, revoked bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.auth[id] = authSpec{kind: kind, revoked: revoked}
}

// revoke flips one principal to revoked.
func (p *fakePorts) revoke(id contract.ID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	spec := p.auth[id]
	spec.revoked = true
	p.auth[id] = spec
}

// inject faults every call to op until cleared.
func (p *fakePorts) inject(op string, f *contract.Fault) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail[op] = f
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	p.calls = append(p.calls, inv)
	injected := p.fail[inv.Operation]
	p.mu.Unlock()
	if injected != nil {
		return contract.Payload{}, injected
	}
	if inv.Operation != opIdentityAuthority {
		return contract.Payload{}, &contract.Fault{
			Code: contract.CodeInternalError, Message: "fake ports: unexpected peer call " + inv.Operation,
		}
	}
	var in wireAuthorityInput
	if err := json.Unmarshal(inv.Input, &in); err != nil {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInvalidInput, Message: "fake ports: bad authority input"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.notFound[in.PrincipalID] {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeNotFound, Message: "principal is not registered"}
	}
	spec, ok := p.auth[in.PrincipalID]
	if !ok {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeNotFound, Message: "principal is not registered"}
	}
	body := resourceOut[wireAuthority]{Resource: wireAuthority{
		Principal: wireAuthorityPrincipal{
			ID:      in.PrincipalID,
			Version: 1,
			Kind:    spec.kind,
			Name:    "principal-" + string(in.PrincipalID),
			Scope:   in.Scope,
			Revoked: spec.revoked,
		},
		Grants:       []json.RawMessage{},
		Restrictions: []string{},
	}}
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
	install contract.ID
	scope   contract.Scope
}

// newEnv opens a fresh database, migrates the reviews owner and seeds a
// handful of principals with distinct identities and kinds.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "reviews-test.db")})
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
		t.Fatalf("reviews.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate reviews: %v", err)
	}
	env.install = env.ids.New()
	env.scope = contract.Scope{InstallationID: env.install}
	return env
}

// principal registers and returns one new principal of the given kind.
func (e *testEnv) principal(kind string) contract.ID {
	id := e.ids.New()
	e.ports.setAuthority(id, kind, false)
	return id
}

// actorFor builds an actor for one principal.
func (e *testEnv) actorFor(id contract.ID) contract.Actor {
	e.t.Helper()
	e.ports.mu.Lock()
	spec, ok := e.ports.auth[id]
	e.ports.mu.Unlock()
	if !ok {
		e.t.Fatalf("actorFor: principal %s is not registered", id)
	}
	return contract.Actor{PrincipalID: id, Kind: spec.kind, CredentialID: e.ids.New()}
}

// callAs runs one operation inside a write transaction on the given scope
// and actor.
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

// expectFaultAs runs an operation as a specific actor and requires a fault
// with the exact code.
func (e *testEnv) expectFaultAs(actor contract.Actor, op string, in any, code string) *contract.Fault {
	e.t.Helper()
	payload, err := e.callAs(actor, e.scope, op, in)
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

// ---------- fixtures ----------

// actionFixture builds one exact action bound to scope, with every field
// the digest covers populated.
func actionFixture(scope contract.Scope, destination string) wireAction {
	return wireAction{
		Scope: scope,
		Tool:  wireRef{ID: eToolID, Version: 3},
		Connection: wireRef{
			ID:      contract.ID("00000000-0000-4000-8000-00000000c0de"),
			Version: 1,
		},
		AccountIdentity: "acct_0123456789",
		Destination:     destination,
		Content: []wireArtifactRef{
			{ID: contract.ID("00000000-0000-4000-8000-00000000c0a1"), Digest: "0aa1f2043c9d3d2ea1a5c6b4db1f7e4b3d5f7a9c1e2d3f4b5a6c7d8e9f0a1b2c"},
			{ID: contract.ID("00000000-0000-4000-8000-00000000c0a2"), Digest: "1bb2f2043c9d3d2ea1a5c6b4db1f7e4b3d5f7a9c1e2d3f4b5a6c7d8e9f0a1b2c"},
		},
		NotBefore:             time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		ExpiresAt:             time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC),
		Preconditions:         json.RawMessage(`{"repository_head":"9a8b7c6d"}`),
		ConfigurationRevision: 7,
		Parameters:            json.RawMessage(`{"title":"update README"}`),
		CostBound:             wireMoney{Currency: "USD", MicroUnits: 250000},
	}
}

// eToolID is a stable synthetic tool identifier for fixtures.
const eToolID = contract.ID("00000000-0000-4000-8000-00000000700c")

// requirementFixture builds a requirement whose digest must be refreshed
// with digestFor before use.
func requirementFixture(eligible []contract.ID, humanRequired bool) wireRequirement {
	return wireRequirement{
		ActionDigest:       "unbound",
		HumanRequired:      humanRequired,
		EligiblePrincipals: eligible,
		ExpiresAt:          time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC),
		SeparateProposer:   false,
	}
}

// ensureInputFor builds an _reviews.ensure input, computing the digest over
// the exact action.
func ensureInputFor(t *testing.T, scope contract.Scope, action wireAction, req wireRequirement) wireEnsureInput {
	t.Helper()
	digest, err := actionDigest(action)
	if err != nil {
		t.Fatalf("actionDigest fixture: %v", err)
	}
	req.ActionDigest = digest
	return wireEnsureInput{Scope: scope, Action: action, Requirement: req}
}

// ---------- operation wrappers ----------

// faultFrom extracts the fault a call failed with: handler faults arrive as
// errors from db.Write (the transaction rolls back), payload faults arrive
// on the payload itself.
func (e *testEnv) faultFrom(payload contract.Payload, err error) *contract.Fault {
	e.t.Helper()
	if err != nil {
		var f *contract.Fault
		if errors.As(err, &f) {
			return f
		}
		e.t.Fatalf("call failed without a fault: %v", err)
	}
	return payload.Error
}

// ensureFor runs _reviews.ensure as the proposer and returns the review.
func (e *testEnv) ensureFor(proposer contract.Actor, scope contract.Scope, in wireEnsureInput) (wireReview, *contract.Fault) {
	e.t.Helper()
	payload, err := e.callAs(proposer, scope, opEnsure, in)
	if f := e.faultFrom(payload, err); f != nil {
		return wireReview{}, f
	}
	var out resourceOut[wireReview]
	e.decode(payload.Data, &out)
	return out.Resource, nil
}

// checkFor runs _reviews.check and returns the raw output body.
func (e *testEnv) checkFor(scope contract.Scope, digest string) (wireCheckOutput, *contract.Fault) {
	e.t.Helper()
	actor := e.actorFor(e.principal(contract.KindService))
	payload, err := e.callAs(actor, scope, opCheck, wireCheckInput{Scope: scope, ActionDigest: digest})
	if f := e.faultFrom(payload, err); f != nil {
		return wireCheckOutput{}, f
	}
	var out wireCheckOutput
	e.decode(payload.Data, &out)
	return out, nil
}

// decideAs runs review.decide as the given reviewer.
func (e *testEnv) decideAs(reviewer contract.Actor, in wireDecideInput) (wireDecision, *contract.Fault) {
	e.t.Helper()
	payload, err := e.callAs(reviewer, e.scope, opDecide, in)
	if f := e.faultFrom(payload, err); f != nil {
		return wireDecision{}, f
	}
	var out resourceOut[wireDecision]
	e.decode(payload.Data, &out)
	return out.Resource, nil
}

// delegateAs runs review.delegate as the given delegator.
func (e *testEnv) delegateAs(delegator contract.Actor, in wireDelegateInput) (wireReview, *contract.Fault) {
	e.t.Helper()
	payload, err := e.callAs(delegator, e.scope, opDelegate, in)
	if f := e.faultFrom(payload, err); f != nil {
		return wireReview{}, f
	}
	var out resourceOut[wireReview]
	e.decode(payload.Data, &out)
	return out.Resource, nil
}

// getReview runs review.get as the service actor.
func (e *testEnv) getReview(id contract.ID) (wireReview, *contract.Fault) {
	e.t.Helper()
	actor := e.actorFor(e.principal(contract.KindService))
	payload, err := e.callAs(actor, e.scope, opGet, wireGetInput{Scope: e.scope, ID: id})
	if f := e.faultFrom(payload, err); f != nil {
		return wireReview{}, f
	}
	var out resourceOut[wireReview]
	e.decode(payload.Data, &out)
	return out.Resource, nil
}

// listReviews runs review.list and returns the page.
func (e *testEnv) listReviews(actor contract.Actor, in wireListInput) (listOut[wireReview], *string, *contract.Fault) {
	e.t.Helper()
	payload, err := e.callAs(actor, e.scope, opList, in)
	if f := e.faultFrom(payload, err); f != nil {
		return listOut[wireReview]{}, nil, f
	}
	var out listOut[wireReview]
	e.decode(payload.Data, &out)
	return out, payload.NextCursor, nil
}

// events reads the owner's emitted events from storage in sequence order.
func (e *testEnv) events() []contract.Event {
	e.t.Helper()
	events, err := e.db.Events(e.ctx, 0, 500)
	if err != nil {
		e.t.Fatalf("read events: %v", err)
	}
	return events
}

// eventsOfKind filters the owner's events to one kind.
func (e *testEnv) eventsOfKind(kind string) []contract.Event {
	e.t.Helper()
	var out []contract.Event
	for _, ev := range e.events() {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}
