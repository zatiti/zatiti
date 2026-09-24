package mcpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func init() {
	// This package's tests dial a real httptest.NewTLSServer; its
	// self-signed certificate is not in any real trust store. Production
	// code never sets testTLSConfig (see its doc comment in adapter.go).
	testTLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test-only, never wired into production code
}

// ---------- fakes ----------

// fakeClock starts from the real wall-clock time and advances by one
// millisecond on every call. Unlike a fixed-date fake, this package's
// tests dial a real in-process HTTP server, so the context deadlines
// adapter.go computes from this clock (clock.Now().Add(timeout)) must be
// genuinely in the future relative to real time, or Go's real transport
// cancels the request before it ever starts.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Now()}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.now
	c.now = c.now.Add(time.Millisecond)
	return t
}

// fakeSecrets is a minimal contract.SecretStore.
type fakeSecrets struct {
	mu     sync.Mutex
	values map[string][]byte
	getErr error
}

func newFakeSecrets(ref string, secret []byte) *fakeSecrets {
	return &fakeSecrets{values: map[string][]byte{ref: secret}}
}

func (s *fakeSecrets) Put(_ context.Context, ref string, secret []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[ref] = secret
	return ref, nil
}

func (s *fakeSecrets) Get(_ context.Context, ref string) ([]byte, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[ref]
	if !ok {
		return nil, &contract.Fault{Code: contract.CodeNotFound, Message: "credential not found"}
	}
	return v, nil
}

func (s *fakeSecrets) Delete(_ context.Context, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, ref)
	return nil
}

// fakeBlobStore is a minimal in-memory contract.BlobStore.
type fakeBlobStore struct {
	mu       sync.Mutex
	objects  map[contract.Digest][]byte
	staged   int
	stageErr error
	openErr  error
}

func newFakeBlobStore() *fakeBlobStore {
	return &fakeBlobStore{objects: map[contract.Digest][]byte{}}
}

func (b *fakeBlobStore) Stage(_ context.Context, r io.Reader, _ int64) (string, contract.Digest, int64, error) {
	if b.stageErr != nil {
		return "", "", 0, b.stageErr
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.staged++
	ref := fmt.Sprintf("staged-%d", b.staged)
	d := contract.Hash(data)
	b.objects[d] = data
	return ref, d, int64(len(data)), nil
}

func (b *fakeBlobStore) Publish(_ context.Context, _ string, _ contract.Digest) error { return nil }

func (b *fakeBlobStore) Open(_ context.Context, digest contract.Digest, offset, length int64) (io.ReadCloser, error) {
	if b.openErr != nil {
		return nil, b.openErr
	}
	b.mu.Lock()
	data, ok := b.objects[digest]
	b.mu.Unlock()
	if !ok {
		return nil, &contract.Fault{Code: contract.CodeArtifactFault, Message: "artifact bytes are unavailable"}
	}
	end := int64(len(data))
	if length > 0 && offset+length < end {
		end = offset + length
	}
	return io.NopCloser(bytes.NewReader(data[offset:end])), nil
}

func (b *fakeBlobStore) RemoveStaged(_ context.Context, _ string) error { return nil }

// staged returns every request/tool-result record this store has staged,
// decoded as raw JSON documents, for tests that need to inspect them (for
// example, to prove a credential never appears in a staged request
// record).
func (b *fakeBlobStore) stagedDocs() [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([][]byte, 0, len(b.objects))
	for _, v := range b.objects {
		out = append(out, v)
	}
	return out
}

// ---------- profile/action builders ----------

const testCredentialRef = "cred-1"
const testToken = "mcp_test_bearer_token_secret_value"

func testCapabilityArtifact() wireArtifactRef {
	return wireArtifactRef{ID: contract.NewID(), Digest: contract.Hash([]byte("mcpclient-adapter-qualification"))}
}

// buildProfileJSON builds a schema-valid zatiti.mcp/v1 profile document
// whose capability_evidence.profile_digest correctly binds to the rest of
// the document, exactly as loadProfile requires.
func buildProfileJSON(t *testing.T, endpoint string, allowPrivate bool, allowedTools, classifications []string, credentialKind string) json.RawMessage {
	t.Helper()
	transport, err := json.Marshal(wireStreamableHTTPTransport{
		Kind: transportStreamableHTTP, Endpoint: endpoint, AllowPrivateEndpoint: allowPrivate, MaxRedirects: 0,
	})
	if err != nil {
		t.Fatalf("marshal transport: %v", err)
	}
	w := wireMCPProfile{
		Schema:           "zatiti.mcp/v1",
		Transport:        transport,
		ProtocolVersion:  "2025-11-25",
		CredentialKind:   credentialKind,
		AllowedTools:     allowedTools,
		ToolCallCost:     wireMoney{Currency: "USD", MicroUnits: 0},
		MaxRequestBytes:  1 << 16,
		MaxResponseBytes: 1 << 16,
		TimeoutSeconds:   30,
		Classifications:  classifications,
		CapabilityEvidence: wireCapabilityEvidence{
			Artifact:         testCapabilityArtifact(),
			AdapterVersion:   "test-1",
			SourceRevision:   "test-rev",
			ProtocolRevision: "2025-11-25",
			QualifiedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Capabilities:     []string{"call_tool"},
			Limitations:      []string{},
		},
	}
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	digest, err := profileDigestWithoutCapabilityEvidence(raw)
	if err != nil {
		t.Fatalf("compute profile digest: %v", err)
	}
	w.CapabilityEvidence.ProfileDigest = digest
	raw, err = json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal profile with digest: %v", err)
	}
	return raw
}

// newTestAdapter constructs a *mcpclient.Adapter (as contract.Adapter)
// wired to blobs and secrets, with the given profile.
func newTestAdapter(t *testing.T, blobs contract.BlobStore, secrets contract.SecretStore, profile json.RawMessage) contract.Adapter {
	t.Helper()
	deps := contract.AdapterDependencies{
		HTTP:    &http.Client{},
		Secrets: secrets,
		Clock:   newFakeClock(),
		Blobs:   blobs,
	}
	a, err := New(deps, profile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// testDispatch builds a Dispatch for action, whose deadline is genuinely
// timeout in the future from real wall-clock time.
func testDispatch(t *testing.T, action any, timeout time.Duration) contract.Dispatch {
	t.Helper()
	raw, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	return contract.Dispatch{
		OperationID:   contract.NewID(),
		AttemptID:     contract.NewID(),
		Generation:    1,
		Adapter:       adapterName,
		Action:        raw,
		CredentialRef: testCredentialRef,
		Deadline:      time.Now().Add(timeout),
	}
}

// mustFault extracts the *contract.Fault an error must be.
func mustFault(t *testing.T, err error) *contract.Fault {
	t.Helper()
	f, ok := err.(*contract.Fault)
	if !ok {
		t.Fatalf("error is not *contract.Fault: %T: %v", err, err)
	}
	return f
}

// decodeEvidence strict-decodes an Observation's evidence into
// wireMCPEvidence for assertions.
func decodeEvidence(t *testing.T, obs contract.Observation) wireMCPEvidence {
	t.Helper()
	var ev wireMCPEvidence
	if err := json.Unmarshal(obs.Evidence, &ev); err != nil {
		t.Fatalf("decode evidence: %v\n%s", err, obs.Evidence)
	}
	return ev
}
