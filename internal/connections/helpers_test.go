package connections

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Test harness: a real storage database per test, deterministic fakes for
// clock, ids, peer ports and the secret store, and helpers that drive every
// operation through Service.Handle or the local IO seam inside storage
// transactions. The local IO driver mirrors the registry's phase order
// exactly: Prepare inside a write transaction, Perform outside transactions,
// Finish inside a second write transaction.

// seeded contract identities shipped with migrationV1.
var (
	toolModelRead   = contract.ID("0a000000-0000-4000-8000-0000000000c1")
	toolRESTRead    = contract.ID("0a000000-0000-4000-8000-0000000000c2")
	toolUnknownName = "ghost.example.test"
)

// markedSecret is synthetic secret material planted in the fake store for
// hygiene assertions: it must never appear in any payload, event or error.
const markedSecret = "raw-secret-material-8912-never-returns"

// helperReceiptKeyMaterial is the deterministic HMAC key the fake store holds
// under the receipt key reference. Test-only fixture material.
var helperReceiptKeyMaterial = []byte("test-helper-receipt-key-0123456789abcdef")

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

// Advance moves the clock forward deterministically.
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

// fakePorts serves the two peer calls connections makes — _configuration.stage
// and _execution.job.create — with injectable faults, recording every call.
// The stage fake mirrors configuration's contract: a draft carrying the
// echoed change. The job fake mirrors execution's: a pending governed job.
type fakePorts struct {
	mu    sync.Mutex
	calls []contract.Invocation
	next  int
	fail  map[string]*contract.Fault
}

func newFakePorts() *fakePorts {
	return &fakePorts{fail: map[string]*contract.Fault{}}
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	p.calls = append(p.calls, inv)
	injected := p.fail[inv.Operation]
	p.mu.Unlock()
	if injected != nil {
		return contract.Payload{}, injected
	}
	var body any
	switch inv.Operation {
	case "_configuration.stage":
		var in struct {
			Scope   wireScope    `json:"scope"`
			Change  wireChange   `json:"change"`
			DraftID *contract.ID `json:"draft_id,omitempty"`
		}
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: "fake stage: bad input"}
		}
		draftID := in.DraftID
		if draftID == nil {
			p.mu.Lock()
			p.next++
			minted := contract.ID(fmt.Sprintf("d0000000-0000-4000-8000-%012d", p.next))
			p.mu.Unlock()
			draftID = &minted
		}
		changeRaw, err := json.Marshal(in.Change)
		if err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: "fake stage: bad change"}
		}
		draft := wireDraft{
			ID: *draftID, Version: 1, BaseRevision: 5,
			Changes:     []json.RawMessage{changeRaw},
			Diagnostics: []wireDiagnostic{},
		}
		body = draftOut{Resource: draft}

	case "_execution.job.create":
		var in struct {
			Scope     wireScope       `json:"scope"`
			Owner     string          `json:"owner"`
			Operation string          `json:"operation"`
			Input     json.RawMessage `json:"input"`
			SourceID  contract.ID     `json:"source_id"`
		}
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: "fake job: bad input"}
		}
		p.mu.Lock()
		p.next++
		minted := contract.ID(fmt.Sprintf("e0000000-0000-4000-8000-%012d", p.next))
		p.mu.Unlock()
		body = jobOut{Resource: wireJob{
			ID: minted, Version: 1, Kind: "probe", State: "pending",
			Requirements: []wireRequirement{},
			Owner:        in.Owner, Operation: in.Operation,
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

// callsOf returns a snapshot of the recorded peer invocations for one
// operation.
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

// failOn injects a fault the fake returns for the next calls of op.
func (p *fakePorts) failOn(op string, f *contract.Fault) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail[op] = f
}

// fakeSecrets is an in-memory contract.SecretStore with injectable failures
// and a read log for custody assertions.
type fakeSecrets struct {
	mu    sync.Mutex
	store map[string][]byte
	gets  []string
	fail  map[string]error
}

func newFakeSecrets() *fakeSecrets {
	return &fakeSecrets{store: map[string][]byte{}, fail: map[string]error{}}
}

func (s *fakeSecrets) Put(ctx context.Context, reference string, secret []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail[reference]; err != nil {
		return "", err
	}
	s.store[reference] = append([]byte(nil), secret...)
	return reference, nil
}

func (s *fakeSecrets) Get(ctx context.Context, reference string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets = append(s.gets, reference)
	if err := s.fail[reference]; err != nil {
		return nil, err
	}
	raw, ok := s.store[reference]
	if !ok {
		return nil, fmt.Errorf("fake secrets: unknown reference %s", reference)
	}
	return append([]byte(nil), raw...), nil
}

func (s *fakeSecrets) Delete(ctx context.Context, reference string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store, reference)
	return nil
}

// seed puts material under a reference, failing the test on error.
func (s *fakeSecrets) seed(t *testing.T, reference string, material []byte) {
	t.Helper()
	if _, err := s.Put(context.Background(), reference, material); err != nil {
		t.Fatalf("seed secret %s: %v", reference, err)
	}
}

// getsOf returns the references read so far.
func (s *fakeSecrets) getsOf() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.gets...)
}

// mintReceipt signs a helper receipt with the same envelope the trusted
// helper produces: zatiti-helper/v1.<base64url(payload)>.<hex(hmac-sha256)>.
func mintReceipt(key []byte, p helperPayload) string {
	raw, err := json.Marshal(p)
	if err != nil {
		panic("mintReceipt: " + err.Error())
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	return helperReceiptPrefix + base64.RawURLEncoding.EncodeToString(raw) + "." + hex.EncodeToString(mac.Sum(nil))
}

// testEnv is one installation wired to a real storage database.
type testEnv struct {
	t       *testing.T
	ctx     context.Context
	db      contract.Database
	svc     *Service
	ports   *fakePorts
	secrets *fakeSecrets
	clock   *fakeClock
	ids     *seqIDs
	actor   contract.Actor // initiating client-agent principal
	other   contract.Actor // a second, unrelated principal
	install contract.ID
	scope   wireScope
}

// newEnv opens a fresh database and migrates the connections owner.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "connections-test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env := &testEnv{
		t: t, ctx: ctx, db: db,
		ports:   newFakePorts(),
		secrets: newFakeSecrets(),
		clock:   &fakeClock{now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		ids:     &seqIDs{},
	}
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids, Ports: env.ports, Secrets: env.secrets})
	if err != nil {
		t.Fatalf("connections.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate connections: %v", err)
	}
	env.install = env.ids.New()
	env.actor = contract.Actor{PrincipalID: env.ids.New(), Kind: contract.KindClientAgent}
	env.other = contract.Actor{PrincipalID: env.ids.New(), Kind: contract.KindClientAgent}
	env.scope = wireScope{InstallationID: env.install}
	return env
}

// callAs runs one operation inside a write transaction on the given scope and
// actor.
func (e *testEnv) callAs(scope wireScope, actor contract.Actor, op string, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Write(e.ctx, actor, scope.toContract(), func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		payload = p
		return err
	})
	return payload, err
}

func (e *testEnv) call(op string, in any) (contract.Payload, error) {
	return e.callAs(e.scope, e.actor, op, in)
}

// callReadAs runs one operation inside a read snapshot.
func (e *testEnv) callReadAs(op string, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
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

// ioRun records one full local IO drive: the prepared plan and the final
// disposition payload.
type ioRun struct {
	Plan    contract.IOPlan
	Payload contract.Payload
}

// ioAs drives one local IO mutation exactly as the registry does: Prepare
// inside a write transaction, Perform outside transactions, Finish inside a
// second write transaction. A Perform error folds into a faulted result the
// way the registry folds panics and errors.
func (e *testEnv) ioAs(scope wireScope, actor contract.Actor, op string, in any) (ioRun, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	inv := contract.Invocation{Operation: op, Version: 1, Input: raw}
	var plan contract.IOPlan
	if err := e.db.Write(e.ctx, actor, scope.toContract(), func(unit contract.Unit) error {
		p, perr := e.svc.Prepare(e.ctx, unit, inv)
		if perr != nil {
			return perr
		}
		plan = p
		return nil
	}); err != nil {
		return ioRun{}, err
	}
	ioResult, perr := e.svc.Perform(e.ctx, plan)
	if perr != nil {
		ioResult = contract.IOResult{Fault: fault(contract.CodeInternalError, "perform errored: %v", perr)}
	}
	var payload contract.Payload
	err = e.db.Write(e.ctx, actor, scope.toContract(), func(unit contract.Unit) error {
		p, ferr := e.svc.Finish(e.ctx, unit, plan, ioResult)
		if ferr != nil {
			return ferr
		}
		if ioResult.Fault != nil {
			payload = contract.Payload{Status: contract.StatusFailed, Data: ioResult.Data, Error: ioResult.Fault}
			return nil
		}
		payload = p
		return nil
	})
	return ioRun{Plan: plan, Payload: payload}, err
}

// mustIO drives one local IO mutation and requires a completed disposition.
func (e *testEnv) mustIO(op string, in any) ioRun {
	e.t.Helper()
	run, err := e.ioAs(e.scope, e.actor, op, in)
	if err != nil {
		e.t.Fatalf("%s failed: %v", op, err)
	}
	if run.Payload.Status != contract.StatusCompleted || run.Payload.Error != nil {
		e.t.Fatalf("%s status %q, want completed (error %v)", op, run.Payload.Status, run.Payload.Error)
	}
	return run
}

// expectIOFault drives one local IO mutation and requires a faulted
// disposition with the exact code.
func (e *testEnv) expectIOFault(op string, in any, code string) *contract.Fault {
	e.t.Helper()
	run, err := e.ioAs(e.scope, e.actor, op, in)
	if err != nil {
		var f *contract.Fault
		if errors.As(err, &f) && f.Code == code {
			return f
		}
		e.t.Fatalf("%s: error %v, want %s fault", op, err, code)
	}
	if run.Payload.Error == nil {
		e.t.Fatalf("%s: expected %s fault, got %s", op, code, run.Payload.Status)
	}
	if run.Payload.Error.Code != code {
		e.t.Fatalf("%s: fault %s (%s), want %s", op, run.Payload.Error.Code, run.Payload.Error.Message, code)
	}
	return run.Payload.Error
}

func (e *testEnv) decode(raw json.RawMessage, out any) {
	e.t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		e.t.Fatalf("decode payload: %v", err)
	}
}

// events reads the outbox so tests can assert exact emissions. 500 is the
// storage maximum page.
func (e *testEnv) events(after int64) []contract.Event {
	e.t.Helper()
	evs, err := e.db.Events(e.ctx, after, 500)
	if err != nil {
		e.t.Fatalf("read events: %v", err)
	}
	return evs
}

// kindsOf returns the event kinds emitted so far.
func (e *testEnv) kindsOf() []string {
	var out []string
	for _, ev := range e.events(0) {
		out = append(out, ev.Kind)
	}
	return out
}

// assertNoSecretBytes fails when marked secret material appears anywhere in
// raw JSON — the Z01/Z13 custody property.
func assertNoSecretBytes(t *testing.T, raw json.RawMessage, secrets ...string) {
	t.Helper()
	text := string(raw)
	for _, s := range secrets {
		if s != "" && strings.Contains(text, s) {
			t.Fatalf("payload contains secret material %q: %s", s, text)
		}
	}
}

// ---------- activation fixtures ----------

// candidateBody mirrors the candidate envelope the compiler hands each owner.
type candidateBody struct {
	PlanID          contract.ID  `json:"plan_id"`
	BaseRevision    int64        `json:"base_revision"`
	CandidateDigest string       `json:"candidate_digest"`
	Changes         []wireChange `json:"changes"`
	Dependencies    []wireRef    `json:"dependencies"`
}

type candidateInBody struct {
	Candidate candidateBody `json:"candidate"`
}

// testDigest is a deterministic valid 64-hex candidate digest.
func testDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// connectionDef builds a create change for one connection definition.
func connectionDef(w wireConnection) wireChange {
	raw, err := marshalData(w)
	if err != nil {
		panic("connectionDef: " + err.Error())
	}
	return wireChange{Kind: kindConnection, Action: actionCreate, ID: w.ID, ExpectedVersion: 0, Definition: raw}
}

// archiveDef builds an archive change pinned at expected_version, carrying
// the unchanged definition the way connection.archive stages it.
func archiveDef(id contract.ID, expectedVersion int64, def wireConnection) wireChange {
	return wireChange{
		Kind: kindConnection, Action: actionArchive, ID: id, ExpectedVersion: expectedVersion,
		Definition: mustRaw(def),
	}
}

// activate applies changes through the real activation path and returns the
// produced versions.
func (e *testEnv) activate(changes ...wireChange) []wireRef {
	e.t.Helper()
	payload := e.mustOK("_connections.activate", candidateInBody{Candidate: candidateBody{
		PlanID:          e.ids.New(),
		BaseRevision:    1,
		CandidateDigest: testDigest(fmt.Sprint(changes)),
		Changes:         changes,
		Dependencies:    []wireRef{},
	}})
	var out activateOut
	e.decode(payload.Data, &out)
	return out.Versions
}

// seedConnection applies one active connection definition and returns it.
// state defaults to unverified when empty.
func (e *testEnv) seedConnection(mutate func(*wireConnection)) wireConnection {
	e.t.Helper()
	w := wireConnection{
		ID:              e.ids.New(),
		Version:         1,
		Scope:           e.scope,
		Provider:        "provider-example",
		AccountIdentity: "acct-example-1",
		CredentialRef:   "connections/credentials/" + string(e.install),
		Destinations:    []string{"api.github.com"},
		AllowedScopes:   []string{"repo:read"},
		ValidationState: connStateUnverified,
	}
	if mutate != nil {
		mutate(&w)
	}
	e.activate(connectionDef(w))
	return w
}

// seedExternalConnection applies one active connection with an organization-
// scoped and worker-scoped sibling for filter and scope-cover tests.
func (e *testEnv) seedExternalConnection(org, worker contract.ID) wireConnection {
	e.t.Helper()
	scope := e.scope
	scope.OrganizationID = org
	scope.WorkerID = worker
	return e.seedConnection(func(w *wireConnection) {
		w.Scope = scope
	})
}

// ---------- challenge fixtures ----------

// beginChallenge drives setup.begin for method and returns the accepted
// plan (which carries the challenge identity and private channel).
func (e *testEnv) beginChallenge(conn wireConnection, method string) ioRun {
	return e.mustIO("connection.setup.begin", beginInput{
		Scope: e.scope, ConnectionID: conn.ID, ExpectedVersion: conn.Version, Method: method,
	})
}

// challengeOf decodes the challenge resource from a begin/cancel/complete or
// status payload.
func (e *testEnv) challengeOf(payload contract.Payload) wireChallenge {
	e.t.Helper()
	var out resourceChallengeOut
	e.decode(payload.Data, &out)
	return out.Resource
}

// planChallenge decodes the challenge resource out of a prepared plan.
func planChallenge(plan contract.IOPlan) wireChallenge {
	var env ioEnvelope
	if err := json.Unmarshal(plan.Prepared, &env); err != nil {
		panic("planChallenge: " + err.Error())
	}
	return *env.Resource
}

// planPrivate decodes the private channel out of a prepared plan.
func planPrivate(plan contract.IOPlan) ioPrivate {
	return decodePrivate(plan.Prepared)
}

// cancelInput/completeInput mirror the setup.cancel/complete request bodies.
type cancelInput = struct {
	Scope           wireScope   `json:"scope"`
	ChallengeID     contract.ID `json:"challenge_id"`
	ExpectedVersion int64       `json:"expected_version"`
}

type completeInput = struct {
	Scope           wireScope   `json:"scope"`
	ChallengeID     contract.ID `json:"challenge_id"`
	ExpectedVersion int64       `json:"expected_version"`
	HelperRef       string      `json:"helper_ref"`
}

// seedBrowserCredential plants provider authorization metadata under the
// connection's credential reference, the way provisioning would.
func (e *testEnv) seedBrowserCredential(ref string) {
	e.t.Helper()
	e.secrets.seed(e.t, ref, []byte(`{
		"client_id": "client-example-public",
		"authorize_url": "https://auth.example.test/authorize",
		"redirect_uri": "https://connect.example.test/callback"
	}`))
}
