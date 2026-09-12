package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Test harness: a real storage database per test, deterministic fakes for
// clock, identities, peer ports and the blob store, and helpers that drive
// every operation (including the local IO seam) through the same paths the
// application layer uses.

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

// fakePorts serves _execution.job.create with a synthetic job and records
// every call for assertions.
type fakePorts struct {
	mu    sync.Mutex
	calls []contract.Invocation
	fail  *contract.Fault
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, inv)
	if p.fail != nil {
		return contract.Payload{}, p.fail
	}
	switch inv.Operation {
	case peerExecutionJobCreate:
		var in executionJobCreateInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		job := wireJob{
			ID:      contract.ID(fmt.Sprintf("00000000-0000-4000-9000-%012d", len(p.calls))),
			Version: 1, Kind: "operation", State: "pending",
			Requirements: []wireRequirement{}, Owner: in.Owner, Operation: in.Operation,
		}
		raw, err := json.Marshal(jobOutput{Resource: job})
		if err != nil {
			return contract.Payload{}, err
		}
		return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
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

// fakeBlobs is an in-memory content-addressed blob store. Open only serves
// published content: this mirrors the documented contract (Stage persists
// and hashes; Publish makes a staged blob addressable) and is the exact
// behavior internal/skills's own fake relies on.
type fakeBlobs struct {
	mu        sync.Mutex
	staged    map[string][]byte
	published map[contract.Digest][]byte
	corrupt   map[contract.Digest]bool
	next      int
	publishN  int
}

func newFakeBlobs() *fakeBlobs {
	return &fakeBlobs{staged: map[string][]byte{}, published: map[contract.Digest][]byte{}, corrupt: map[contract.Digest]bool{}}
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
	sum := sha256.Sum256(raw)
	return ref, contract.Digest(fmt.Sprintf("%x", sum)), int64(len(raw)), nil
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
	b.publishN++
	return nil
}

func (b *fakeBlobs) Open(ctx context.Context, digest contract.Digest, offset, length int64) (io.ReadCloser, error) {
	b.mu.Lock()
	raw, ok := b.published[digest]
	corrupt := b.corrupt[digest]
	b.mu.Unlock()
	if !ok {
		return nil, errors.New("fake blobs: unknown digest " + string(digest))
	}
	if corrupt {
		return nil, errors.New("fake blobs: digest " + string(digest) + " is corrupt")
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

// markCorrupt makes a published digest fail every future Open, simulating
// bytes that went missing or were corrupted after commit.
func (b *fakeBlobs) markCorrupt(digest contract.Digest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.corrupt[digest] = true
}

func (b *fakeBlobs) stagedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.staged)
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
	scope   wireScope
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "artifacts-test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env := &testEnv{
		t: t, ctx: ctx, db: db,
		ports: &fakePorts{},
		blobs: newFakeBlobs(),
		clock: &fakeClock{now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		ids:   &seqIDs{},
	}
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids, Ports: env.ports, Blobs: env.blobs})
	if err != nil {
		t.Fatalf("artifacts.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate artifacts: %v", err)
	}
	env.install = env.ids.New()
	owner := env.ids.New()
	env.actor = contract.Actor{PrincipalID: owner, Kind: contract.KindService}
	env.scope = wireScope{InstallationID: env.install}
	return env
}

// call runs one non-local-IO operation inside a write transaction and
// returns its payload.
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
	return payload
}

// expectFault runs a mutation and requires a fault with the exact code.
func (e *testEnv) expectFault(op string, in any, code string) *contract.Fault {
	e.t.Helper()
	payload, err := e.call(op, in)
	return e.assertFault(op, payload, err, code)
}

// expectQueryFault runs a query and requires a fault with the exact code.
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

// driveLocalIO runs Prepare (write tx) -> Perform (no tx) -> Finish (tx of
// the given kind) exactly as the application layer's invokeIOMutation /
// invokeIOQuery do, and returns the final delivered payload.
func (e *testEnv) driveLocalIO(op string, in any, mutation bool) (contract.Payload, error) {
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
		err = e.db.Write(e.ctx, e.actor, e.scope.toContract(), txFn)
	} else {
		err = e.db.Read(e.ctx, e.actor, e.scope.toContract(), txFn)
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
		err = e.db.Write(e.ctx, e.actor, e.scope.toContract(), finishFn)
	} else {
		err = e.db.Read(e.ctx, e.actor, e.scope.toContract(), finishFn)
	}
	return payload, err
}

func (e *testEnv) mustLocalOK(op string, in any, mutation bool) contract.Payload {
	e.t.Helper()
	payload, err := e.driveLocalIO(op, in, mutation)
	if err != nil {
		e.t.Fatalf("%s failed: %v", op, err)
	}
	if payload.Status == contract.StatusFailed {
		e.t.Fatalf("%s returned failed payload: %v", op, payload.Error)
	}
	return payload
}

func (e *testEnv) expectLocalFault(op string, in any, mutation bool, code string) *contract.Fault {
	e.t.Helper()
	payload, err := e.driveLocalIO(op, in, mutation)
	return e.assertFault(op, payload, err, code)
}

// ---------- fixtures ----------

func digestOf(data []byte) contract.Digest {
	sum := sha256.Sum256(data)
	return contract.Digest(fmt.Sprintf("%x", sum))
}

func b64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

// publishArtifact publishes size bytes of deterministic content through
// _artifacts.publish and returns the resulting artifact and raw bytes.
func (e *testEnv) publishArtifact(size int) (wireArtifact, []byte) {
	e.t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	digest := digestOf(data)
	// publishArtifact bypasses the upload flow: it seeds the blob store
	// directly (as a trusted LocalIO Perform would) and commits metadata
	// through _artifacts.publish, exactly like a peer owner's Finish would.
	ref, gotDigest, _, err := e.blobs.Stage(e.ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		e.t.Fatalf("stage fixture: %v", err)
	}
	if gotDigest != digest {
		e.t.Fatalf("fixture digest mismatch")
	}
	if err := e.blobs.Publish(e.ctx, ref, digest); err != nil {
		e.t.Fatalf("publish fixture: %v", err)
	}
	payload := e.mustOK(opPublish, publishInput{
		Scope: e.scope, Digest: digest, Size: int64(len(data)),
		MediaType: "application/octet-stream", Classification: "internal", Encrypted: true,
	})
	var out artifactOutput
	e.decode(payload.Data, &out)
	return out.Resource, data
}

// beginUpload runs artifact.upload.begin and returns the resulting upload.
func (e *testEnv) beginUpload(size int, digest contract.Digest) wireUpload {
	e.t.Helper()
	payload := e.mustOK(opUploadBegin, uploadBeginInput{
		Scope: e.scope, Size: int64(size), Digest: digest,
		MediaType: "application/octet-stream", Classification: "internal",
	})
	var out uploadOutput
	e.decode(payload.Data, &out)
	return out.Resource
}

// sendChunk drives one artifact.upload.chunk call end to end.
func (e *testEnv) sendChunk(uploadID contract.ID, offset int64, data []byte) (uploadChunkOutput, contract.Payload, error) {
	e.t.Helper()
	payload, err := e.driveLocalIO(opUploadChunk, chunkInput{
		Scope: e.scope, UploadID: uploadID, Offset: offset,
		BytesB64: b64(data), ChunkDigest: digestOf(data),
	}, true)
	var out uploadChunkOutput
	if err == nil && payload.Status == contract.StatusCompleted {
		e.decode(payload.Data, &out)
	}
	return out, payload, err
}

func (e *testEnv) mustSendChunk(uploadID contract.ID, offset int64, data []byte) wireUpload {
	e.t.Helper()
	out, payload, err := e.sendChunk(uploadID, offset, data)
	if err != nil {
		e.t.Fatalf("chunk at %d failed: %v", offset, err)
	}
	if payload.Status != contract.StatusCompleted {
		e.t.Fatalf("chunk at %d returned status %q: %v", offset, payload.Status, payload.Error)
	}
	if out.Resource == nil {
		e.t.Fatalf("chunk at %d returned no resource", offset)
	}
	return *out.Resource
}

// finishUpload drives artifact.upload.finish end to end.
func (e *testEnv) finishUpload(uploadID contract.ID, expectedVersion int64) (contract.Payload, error) {
	e.t.Helper()
	return e.driveLocalIO(opUploadFinish, uploadRefInput{
		Scope: e.scope, UploadID: uploadID, ExpectedVersion: expectedVersion,
	}, true)
}

func (e *testEnv) mustFinishUpload(uploadID contract.ID, expectedVersion int64) wireArtifact {
	e.t.Helper()
	payload, err := e.finishUpload(uploadID, expectedVersion)
	if err != nil {
		e.t.Fatalf("finish upload failed: %v", err)
	}
	if payload.Status != contract.StatusCompleted {
		e.t.Fatalf("finish upload status %q: %v", payload.Status, payload.Error)
	}
	var out uploadFinishOutput
	e.decode(payload.Data, &out)
	if out.Resource == nil {
		e.t.Fatalf("finish upload returned no resource")
	}
	return *out.Resource
}

// uploadFull begins an upload for data and drives it through every chunk and
// finish, returning the resulting artifact.
func (e *testEnv) uploadFull(data []byte, chunkSize int) wireArtifact {
	e.t.Helper()
	digest := digestOf(data)
	u := e.beginUpload(len(data), digest)
	version := int64(u.Version)
	var offset int64
	for offset < int64(len(data)) {
		end := offset + int64(chunkSize)
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		up := e.mustSendChunk(u.ID, offset, data[offset:end])
		version = int64(up.Version)
		offset = end
	}
	return e.mustFinishUpload(u.ID, version)
}

func (e *testEnv) readArtifact(id contract.ID, offset, length int64) (contract.Payload, error) {
	e.t.Helper()
	return e.driveLocalIO(opArtifactRead, readInput{Scope: e.scope, ID: id, Offset: offset, Length: length}, false)
}
