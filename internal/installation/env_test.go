package installation

import (
	"bytes"
	"context"
	"crypto/sha256"
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
// clock, identities, peer ports, the secret store and the blob store, and
// helpers that drive every operation (including the local IO seam) through
// the same paths the application layer uses.

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

// fakeSecrets is an in-memory SecretStore shaped like the real platform
// store: Put takes a NAME and returns an opaque reference, and Get resolves
// ONLY such a reference, never a name. A test double that resolved names
// would hide exactly the defect that once made every real backup
// unrestorable.
type fakeSecrets struct {
	mu   sync.Mutex
	m    map[string][]byte // reference -> secret
	next int
	err  error // when set, every call fails
}

const fakeRefPrefix = "fake1:"

func newFakeSecrets() *fakeSecrets { return &fakeSecrets{m: map[string][]byte{}} }

func (f *fakeSecrets) Put(_ context.Context, name string, secret []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	if name == "" || strings.HasPrefix(name, fakeRefPrefix) {
		return "", fmt.Errorf("fake secrets: Put takes a name, got %q", name)
	}
	f.next++
	ref := fmt.Sprintf("%s%032x", fakeRefPrefix, f.next)
	f.m[ref] = append([]byte(nil), secret...)
	return ref, nil
}

func (f *fakeSecrets) Get(_ context.Context, ref string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if !strings.HasPrefix(ref, fakeRefPrefix) {
		return nil, fmt.Errorf("fake secrets: credential reference is unknown (names do not resolve: %q)", ref)
	}
	secret, ok := f.m[ref]
	if !ok {
		return nil, fmt.Errorf("fake secrets: credential reference is unknown")
	}
	return append([]byte(nil), secret...), nil
}

func (f *fakeSecrets) Delete(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, ref)
	return nil
}

// fakeBlobs is an in-memory content-addressed blob store.
type fakeBlobs struct {
	mu        sync.Mutex
	staged    map[string][]byte
	published map[contract.Digest][]byte
	next      int
}

func newFakeBlobs() *fakeBlobs {
	return &fakeBlobs{staged: map[string][]byte{}, published: map[contract.Digest][]byte{}}
}

func (b *fakeBlobs) Stage(_ context.Context, r io.Reader, _ int64) (string, contract.Digest, int64, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	ref := fmt.Sprintf("staging-%d", b.next)
	b.staged[ref] = raw
	sum := sha256.Sum256(raw)
	return ref, contract.Digest(fmt.Sprintf("%x", sum)), int64(len(raw)), nil
}

func (b *fakeBlobs) Publish(_ context.Context, stagingRef string, digest contract.Digest) error {
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

func (b *fakeBlobs) Open(_ context.Context, digest contract.Digest, offset, length int64) (io.ReadCloser, error) {
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

func (b *fakeBlobs) RemoveStaged(_ context.Context, stagingRef string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.staged, stagingRef)
	return nil
}

// publishBytes stages and publishes raw directly, as if a prior upload had
// already completed, and returns its digest.
func (b *fakeBlobs) publishBytes(raw []byte) contract.Digest {
	sum := sha256.Sum256(raw)
	digest := contract.Digest(fmt.Sprintf("%x", sum))
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published[digest] = raw
	return digest
}

// fakePorts serves every peer operation installation.go calls, with a
// sensible default per operation and per-test overrides for failure
// injection and assertions.
type fakePorts struct {
	mu       sync.Mutex
	calls    []contract.Invocation
	handlers map[string]func(contract.Invocation) (contract.Payload, error)
	ids      *seqIDs
}

func newFakePorts(ids *seqIDs) *fakePorts {
	return &fakePorts{handlers: map[string]func(contract.Invocation) (contract.Payload, error){}, ids: ids}
}

func (p *fakePorts) set(op string, h func(contract.Invocation) (contract.Payload, error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers[op] = h
}

func (p *fakePorts) Call(_ context.Context, _ contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	p.calls = append(p.calls, inv)
	h, ok := p.handlers[inv.Operation]
	p.mu.Unlock()
	if ok {
		return h(inv)
	}
	switch inv.Operation {
	case peerIdentityBootstrap:
		var in identityBootstrapInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		return okPayload(resourceOut[peerPrincipal]{Resource: peerPrincipal{
			ID: in.OwnerID, Version: 1, Kind: contract.KindHuman, Name: in.Name,
			Scope: wireScope{InstallationID: in.InstallationID}, Revoked: false,
		}})
	case peerConfigurationBootstrap:
		var in configurationBootstrapInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		return okPayload(configurationBootstrapOutput{
			Organization: peerOrganization{ID: in.OrganizationID, Version: 1, Key: "root", Name: "Root", ChiefID: in.ChiefID},
			Chief: peerWorker{
				ID: in.ChiefID, Version: 1, OrganizationID: in.OrganizationID, Key: "chief", Name: "Chief",
				Purpose: "", Instructions: "", SkillVersions: []wireRef{}, Bindings: []contract.ID{},
				Profile: json.RawMessage("null"), Limits: json.RawMessage("null"),
			},
		})
	case peerMemoryBootstrap:
		var in memoryBootstrapInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		return okPayload(memoryBootstrapOutput{Bindings: []peerMemoryBinding{
			{ID: p.ids.New(), Version: 1, Scope: wireScope{InstallationID: in.InstallationID},
				BrainID: p.ids.New(), Permissions: []string{"read", "write"}, Classification: "internal"},
		}})
	case peerMessagingBootstrap:
		var in messagingBootstrapInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		return okPayload(resourceOut[peerConversation]{Resource: peerConversation{
			ID: p.ids.New(), Version: 1, Scope: in.Scope, Kind: "direct",
			ParticipantIDs: []contract.ID{in.OwnerID, in.ChiefID}, Title: "Personal chief", Pinned: true,
		}})
	case peerEffectsPending:
		return okPayload(effectsPendingOutput{Operations: []peerOperation{}})
	case peerMemoryManifest:
		return okPayload(memoryManifestOutput{BrainRevisions: []wireRef{}, Obligations: []wireRequirement{}})
	case peerAccountingInspect:
		return okPayload(accountingInspectOutput{
			Limits: peerLimits{Currency: "USD", SpendMicroUnits: 1_000_000, Concurrency: 1, ModelSteps: 100,
				ChildCount: 8, DelegationDepth: 3, AttemptSeconds: 1800, RootDeadline: "2027-01-01T00:00:00Z"},
			Usage: peerUsage{Currency: "USD"},
		})
	case peerArtifactsMetadata:
		return okPayload(artifactsMetadataOutput{Artifacts: []peerArtifact{}})
	case peerArtifactsPublish:
		var in artifactsPublishInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		return okPayload(resourceOut[peerArtifact]{Resource: peerArtifact{
			ID: p.ids.New(), Version: 1, Scope: in.Scope, Digest: in.Digest, Size: in.Size,
			MediaType: in.MediaType, Classification: in.Classification, Encrypted: in.Encrypted,
			State: "available", CreatedAt: "2026-09-12T00:00:00Z",
		}})
	case peerExecutionJobCreate:
		var in executionJobCreateInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		return okPayload(resourceOut[wireJob]{Resource: wireJob{
			ID: p.ids.New(), Version: 1, Kind: "operation", State: "pending",
			Requirements: []wireRequirement{}, Owner: in.Owner, Operation: in.Operation,
		}})
	case peerExecutionJobRecord:
		var in executionJobRecordInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		return okPayload(resourceOut[wireJob]{Resource: wireJob{
			ID: in.JobID, Version: contract.Version(in.ExpectedVersion + 1), Kind: "operation",
			State: in.State, Requirements: []wireRequirement{}, Owner: owner, Operation: "",
		}})
	case peerExecutionFence:
		return okPayload(executionFenceOutput{AttemptIDs: []contract.ID{}})
	default:
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError,
			Message: "fake ports: unexpected peer call " + inv.Operation}
	}
}

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

func okPayload(data any) (contract.Payload, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}

func failPayload(code, msg string) (contract.Payload, error) {
	return contract.Payload{}, &contract.Fault{Code: code, Message: msg}
}

// testEnv is one installation service wired to a real storage database.
type testEnv struct {
	t       *testing.T
	ctx     context.Context
	db      contract.Database
	svc     *Service
	ports   *fakePorts
	secrets *fakeSecrets
	blobs   *fakeBlobs
	clock   *fakeClock
	ids     *seqIDs
	actor   contract.Actor
	install contract.ID
	scope   wireScope
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "installation-test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ids := &seqIDs{}
	env := &testEnv{
		t: t, ctx: ctx, db: db,
		ports:   newFakePorts(ids),
		secrets: newFakeSecrets(),
		blobs:   newFakeBlobs(),
		clock:   &fakeClock{now: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)},
		ids:     ids,
	}
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids, Ports: env.ports, Secrets: env.secrets, Blobs: env.blobs})
	if err != nil {
		t.Fatalf("installation.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate installation: %v", err)
	}
	env.install = env.ids.New()
	ownerPrincipal := env.ids.New()
	env.actor = contract.Actor{PrincipalID: ownerPrincipal, Kind: contract.KindService}
	env.scope = wireScope{InstallationID: env.install}
	return env
}

// bootstrap runs installation.init to completion against a fresh scope and
// returns the resulting Status.
func (e *testEnv) bootstrap(scope contract.Scope, in initInput) (wireStatus, error) {
	payload, err := e.driveLocalIOScope(opInit, in, scope, true)
	if err != nil {
		return wireStatus{}, err
	}
	if payload.Status != contract.StatusCompleted {
		if payload.Error != nil {
			return wireStatus{}, payload.Error
		}
		return wireStatus{}, fmt.Errorf("bootstrap: unexpected status %q", payload.Status)
	}
	var out resourceOut[wireStatus]
	if err := json.Unmarshal(payload.Data, &out); err != nil {
		e.t.Fatalf("decode bootstrap status: %v", err)
	}
	return out.Resource, nil
}

// mustBootstrap bootstraps a fresh installation and wires env.install/scope
// to it, for tests that need an already-initialized installation.
func (e *testEnv) mustBootstrap() wireStatus {
	e.t.Helper()
	scope := contract.Scope{InstallationID: e.install}
	st, err := e.bootstrap(scope, initInput{CredentialStore: "os", OwnerName: "Ada Owner"})
	if err != nil {
		e.t.Fatalf("bootstrap: %v", err)
	}
	return st
}

// call runs one non-local-IO operation inside a write transaction.
func (e *testEnv) call(op string, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		payload = p
		return err
	})
	return payload, err
}

// callQuery runs one query operation inside a read transaction.
func (e *testEnv) callQuery(op string, in any) (contract.Payload, error) {
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

func (e *testEnv) mustOK(op string, in any) contract.Payload {
	e.t.Helper()
	payload, err := e.call(op, in)
	if err != nil {
		e.t.Fatalf("%s failed: %v", op, err)
	}
	if payload.Status != contract.StatusCompleted {
		e.t.Fatalf("%s status %q, want completed (error %v)", op, payload.Status, payload.Error)
	}
	return payload
}

func (e *testEnv) expectFault(op string, in any, code string) *contract.Fault {
	e.t.Helper()
	payload, err := e.call(op, in)
	return e.assertFault(op, payload, err, code)
}

func (e *testEnv) expectQueryFault(op string, in any, code string) *contract.Fault {
	e.t.Helper()
	payload, err := e.callQuery(op, in)
	return e.assertFault(op, payload, err, code)
}

func (e *testEnv) assertFault(op string, payload contract.Payload, err error, code string) *contract.Fault {
	e.t.Helper()
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

// ---------- local IO drive helpers ----------

// driveLocalIOScope runs Prepare (write or read tx) -> Perform (no tx) ->
// Finish (matching tx), exactly as application's invokeIOMutation/
// invokeIOQuery/runBootstrapLocalIO do, against an explicit scope (bootstrap
// mints a fresh one per call; ordinary operations reuse env.scope).
func (e *testEnv) driveLocalIOScope(op string, in any, scope contract.Scope, mutation bool) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	inv := contract.Invocation{Operation: op, Version: 1, Input: raw}

	var plan contract.IOPlan
	txFn := func(unit contract.Unit) error {
		p, err := e.svc.Prepare(e.ctx, unit, inv)
		if err != nil {
			return err
		}
		plan = p
		return nil
	}
	if mutation {
		err = e.db.Write(e.ctx, e.actor, scope, txFn)
	} else {
		err = e.db.Read(e.ctx, e.actor, scope, txFn)
	}
	if err != nil {
		return contract.Payload{}, err
	}

	result, perr := e.svc.Perform(e.ctx, plan)
	if perr != nil {
		return contract.Payload{}, perr
	}

	var payload contract.Payload
	finishFn := func(unit contract.Unit) error {
		p, err := e.svc.Finish(e.ctx, unit, plan, result)
		payload = p
		return err
	}
	if mutation {
		err = e.db.Write(e.ctx, e.actor, scope, finishFn)
	} else {
		err = e.db.Read(e.ctx, e.actor, scope, finishFn)
	}
	return payload, err
}

func (e *testEnv) driveLocalIO(op string, in any, mutation bool) (contract.Payload, error) {
	return e.driveLocalIOScope(op, in, e.scope.toContract(), mutation)
}

// backupOnly is the test's one-method DatabaseBackup over the real storage
// database, shaped exactly like the wrapper entrypoint assembly passes.
type backupOnly struct{ db contract.Database }

func (b backupOnly) Backup(ctx context.Context, w io.Writer) error { return b.db.Backup(ctx, w) }

// recordingBackup wraps a capability and remembers the digest of every image
// it streamed, so a test can check a manifest against the exact bytes the
// capability produced for that call.
type recordingBackup struct {
	inner   contract.DatabaseBackup
	mu      sync.Mutex
	digests []contract.Digest
}

func (r *recordingBackup) Backup(ctx context.Context, w io.Writer) error {
	hw := newHashingWriter(w)
	err := r.inner.Backup(ctx, hw)
	r.mu.Lock()
	r.digests = append(r.digests, hw.digest())
	r.mu.Unlock()
	return err
}

func (r *recordingBackup) last() contract.Digest {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.digests) == 0 {
		return ""
	}
	return r.digests[len(r.digests)-1]
}

// startGeneration advances the controller generation the way entrypoint
// assembly does before serving; manifests pin it.
func (e *testEnv) startGeneration() {
	e.t.Helper()
	if _, err := e.db.StartGeneration(e.ctx); err != nil {
		e.t.Fatalf("start generation: %v", err)
	}
}

// bindBackup rebuilds the service with the capability bound, keeping every
// other dependency.
func (e *testEnv) bindBackup(capability contract.DatabaseBackup) {
	e.t.Helper()
	svc, err := New(contract.Dependencies{Clock: e.clock, IDs: e.ids, Ports: e.ports, Secrets: e.secrets, Blobs: e.blobs}, WithDatabaseBackup(capability))
	if err != nil {
		e.t.Fatalf("installation.New with backup capability: %v", err)
	}
	e.svc = svc
}

// backupKeyRef reads the recorded backup key reference, "" if none.
func (e *testEnv) backupKeyRef() string {
	e.t.Helper()
	var ref string
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		ref, err = loadBackupKeyRef(e.ctx, unit, e.install)
		return err
	}); err != nil {
		e.t.Fatalf("backup key reference: %v", err)
	}
	return ref
}

// openArtifact splits a published bundle artifact into its key reference,
// the key it resolves to and the sealed frame.
func (e *testEnv) openArtifact(artifact []byte) (string, []byte, []byte) {
	e.t.Helper()
	ref, sealed, err := decodeArtifact(artifact)
	if err != nil {
		e.t.Fatalf("artifact header: %v", err)
	}
	key, err := e.secrets.Get(e.ctx, ref)
	if err != nil {
		e.t.Fatalf("key reference %q does not resolve: %v", ref, err)
	}
	return ref, key, sealed
}
