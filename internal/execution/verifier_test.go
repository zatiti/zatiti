package execution

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// The verifier is the only trusted completion path: it observes acceptance
// checks against a real blob store, never worker-provided evidence, and
// stages the exact request bytes for later audit.

// fakeBlobStore is the verifier's test blob store: content addressed by the
// real digest, recording every stage and publish.
type fakeBlobStore struct {
	mu        sync.Mutex
	objects   map[contract.Digest][]byte
	staged    []contract.Digest
	published []contract.Digest
}

func newFakeBlobStore(objects map[contract.Digest][]byte) *fakeBlobStore {
	if objects == nil {
		objects = map[contract.Digest][]byte{}
	}
	return &fakeBlobStore{objects: objects}
}

func (b *fakeBlobStore) Stage(ctx context.Context, r io.Reader, size int64) (string, contract.Digest, int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	digest := sha256Hex(data)
	b.mu.Lock()
	b.objects[digest] = data
	b.staged = append(b.staged, digest)
	b.mu.Unlock()
	return "staged-" + string(digest[:8]), digest, int64(len(data)), nil
}

func (b *fakeBlobStore) Publish(ctx context.Context, stagingRef string, digest contract.Digest) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, digest)
	return nil
}

func (b *fakeBlobStore) Open(ctx context.Context, digest contract.Digest, offset, length int64) (io.ReadCloser, error) {
	b.mu.Lock()
	data, ok := b.objects[digest]
	b.mu.Unlock()
	if !ok {
		return nil, io.EOF
	}
	if offset > int64(len(data)) {
		data = nil
	} else {
		data = data[offset:]
	}
	if length >= 0 && length < int64(len(data)) {
		data = data[:length]
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (b *fakeBlobStore) RemoveStaged(ctx context.Context, stagingRef string) error { return nil }

func (b *fakeBlobStore) Staged() []contract.Digest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]contract.Digest(nil), b.staged...)
}

func TestNewVerifierRequiresClockAndBlobs(t *testing.T) {
	if _, err := NewVerifier(contract.VerifierDependencies{}); err == nil || faultCode(err) != contract.CodeInternalError {
		t.Fatalf("NewVerifier without deps: %v, want internal_error", err)
	}
	if _, err := NewVerifier(contract.VerifierDependencies{Clock: &fakeClock{}}); err == nil || faultCode(err) != contract.CodeInternalError {
		t.Fatalf("NewVerifier without blobs: %v, want internal_error", err)
	}
}

func TestVerifierRejectsForeignRequestSchema(t *testing.T) {
	e := newEnv(t)
	v, err := NewVerifier(contract.VerifierDependencies{Clock: e.clock, Blobs: newFakeBlobStore(nil)})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	_, err = v.Verify(context.Background(), contract.Verification{
		Request: mustMarshal(t, map[string]any{"schema": "someone.else/v9"}),
	})
	if faultCode(err) != contract.CodeVerificationFailed {
		t.Fatalf("foreign schema: %v, want verification_failed", err)
	}
}

func TestVerifierStagesExactRequest(t *testing.T) {
	e := newEnv(t)
	blobs := newFakeBlobStore(map[contract.Digest][]byte{fixtureDigest: fixtureContent})
	v, err := NewVerifier(contract.VerifierDependencies{Clock: e.clock, Blobs: blobs})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	request := mustMarshal(t, wireVerificationRequest{
		Schema:           "zatiti.verification-request/v1",
		JobID:            e.ids.New(),
		TaskID:           e.ids.New(),
		AttemptID:        e.ids.New(),
		Scope:            e.scope,
		AcceptanceDigest: digestA,
		Profile:          fixtureProfile(),
		SealedInputs:     []wireArtifactRef{},
		Outputs:          []wireVerifierOutputRequirement{},
		ExpectedObservations: []wireExpectedObservation{{
			CheckID:        "out-digest",
			Kind:           "artifact_digest",
			Expected:       "present",
			ExpectedDigest: fixtureDigest,
		}},
	})
	res, err := v.Verify(context.Background(), contract.Verification{Request: request})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	// The staged artifact is the exact request bytes, addressable by digest.
	staged := blobs.Staged()
	if len(staged) != 1 || string(blobs.objects[staged[0]]) != string(request) {
		t.Fatalf("verifier staged %v, want exactly the request bytes", staged)
	}
	var result wireVerificationResult
	e.decode(res.Document, &result)
	if !result.Independent || result.Status != "passed" {
		t.Fatalf("verifier result status %q independent %v, want passed and independent",
			result.Status, result.Independent)
	}
	if len(result.Observations) != 1 || result.Observations[0].Status != "passed" {
		t.Fatalf("observations %+v, want one passed check", result.Observations)
	}
	if result.RequestArtifact.Digest != sha256Hex(request) {
		t.Fatalf("request artifact digest %s, want the request digest %s",
			result.RequestArtifact.Digest, sha256Hex(request))
	}
}
