package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// ---------- fakes ----------

// fakeClock advances by one millisecond on every call, so tests never
// depend on wall-clock time but StartedAt/FinishedAt are never equal.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
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

// put seeds an object directly, returning its digest, for tests that need
// an ArtifactRef pointing at known content before any Stage call happens.
func (b *fakeBlobStore) put(data []byte) contract.Digest {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := contract.Hash(data)
	b.objects[d] = data
	return d
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
	if offset > int64(len(data)) {
		return nil, &contract.Fault{Code: contract.CodeArtifactFault, Message: "artifact read range is outside the referenced artifact"}
	}
	end := int64(len(data))
	if length > 0 && offset+length < end {
		end = offset + length
	}
	return io.NopCloser(bytes.NewReader(data[offset:end])), nil
}

func (b *fakeBlobStore) RemoveStaged(_ context.Context, _ string) error { return nil }

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// countingTransport wraps a RoundTripper and counts calls, so tests can
// assert exactly one physical call happened (no hidden retry).
type countingTransport struct {
	mu    sync.Mutex
	calls int
	fn    func(*http.Request) (*http.Response, error)
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.fn(r)
}

func (c *countingTransport) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// jsonResponse builds a canned *http.Response carrying body as JSON.
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// ---------- profile/action builders ----------

func testRepo() wireRepository { return wireRepository{Owner: "acme", Name: "widgets"} }

func permissiveAutomation() wireAutomationConstraints {
	return wireAutomationConstraints{
		AllowedWorkflows:              []string{"ci.yml"},
		AllowedDeploymentEnvironments: []string{"staging"},
		AllowExternalNotifications:    true,
		AllowAutomaticMerge:           true,
		UnknownAutomation:             "deny",
		Evidence:                      []wireArtifactRef{},
	}
}

func restrictiveAutomation() wireAutomationConstraints {
	return wireAutomationConstraints{
		AllowedWorkflows:              []string{},
		AllowedDeploymentEnvironments: []string{},
		AllowExternalNotifications:    false,
		AllowAutomaticMerge:           false,
		UnknownAutomation:             "deny",
		Evidence:                      []wireArtifactRef{},
	}
}

func testCapabilityArtifact() wireArtifactRef {
	return wireArtifactRef{ID: contract.NewID(), Digest: contract.Hash([]byte("github-adapter-qualification"))}
}

// buildProfileJSON builds a schema-valid zatiti.github/v1 profile document
// whose capability_evidence.profile_digest correctly binds to the rest of
// the document, exactly as loadProfile requires.
func buildProfileJSON(t *testing.T, apiBase string, repos []wireRepository, actions []string, automation wireAutomationConstraints) json.RawMessage {
	t.Helper()
	w := wireGitHubProfile{
		Schema:              "zatiti.github/v1",
		APIBase:             apiBase,
		AllowedRepositories: repos,
		AllowedActions:      actions,
		MaxResponseBytes:    1 << 20,
		TimeoutSeconds:      30,
		IdempotencyProfile: wireGitHubIdempotencyProfile{
			Mode:              "none",
			RetentionSeconds:  0,
			EquivalenceFields: []string{},
			Evidence:          []wireArtifactRef{},
		},
		AutomationConstraints: automation,
		CapabilityEvidence: wireCapabilityEvidence{
			Artifact:         testCapabilityArtifact(),
			AdapterVersion:   "test-1",
			SourceRevision:   "test-rev",
			ProtocolRevision: "2022-11-28",
			QualifiedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Capabilities:     []string{"read_repository"},
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

// defaultProfileJSON is a profile permitting all five actions on testRepo
// with permissive automation bounds.
func defaultProfileJSON(t *testing.T) json.RawMessage {
	t.Helper()
	return buildProfileJSON(t, "https://api.github.com", []wireRepository{testRepo()},
		[]string{kindReadRepository, kindCreateBranch, kindPushCommit, kindOpenPullRequest, kindMergePullRequest},
		permissiveAutomation())
}

const testCredentialRef = "cred-1"
const testToken = "ghp_test_token_secret_value"

// newTestAdapter constructs a *github.Adapter (as contract.Adapter) wired
// to transport and the given blob store, with a valid default profile.
func newTestAdapter(t *testing.T, transport http.RoundTripper, blobs contract.BlobStore, profile json.RawMessage) contract.Adapter {
	t.Helper()
	deps := contract.AdapterDependencies{
		HTTP:    &http.Client{Transport: transport},
		Secrets: newFakeSecrets(testCredentialRef, []byte(testToken)),
		Clock:   newFakeClock(),
		Blobs:   blobs,
	}
	a, err := New(deps, profile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func testDispatch(t *testing.T, action any) contract.Dispatch {
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
		Deadline:      time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC),
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
