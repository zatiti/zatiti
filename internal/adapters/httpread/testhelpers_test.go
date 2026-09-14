package httpread

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

// fakeBlobStore is a minimal in-memory contract.BlobStore.
type fakeBlobStore struct {
	mu       sync.Mutex
	objects  map[contract.Digest][]byte
	staged   int
	stageErr error
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

func (b *fakeBlobStore) stagedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.staged
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// countingTransport wraps a RoundTripper and counts calls, so tests can
// assert exactly one physical request happened (no hidden retry).
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

// fixedLookup is an ipLookupFunc that always resolves to the given
// addresses, regardless of host, so tests can steer resolution without a
// real DNS resolver.
func fixedLookup(addrs ...string) ipLookupFunc {
	return func(_ context.Context, _ string) ([]net.IPAddr, error) {
		out := make([]net.IPAddr, len(addrs))
		for i, a := range addrs {
			out[i] = net.IPAddr{IP: net.ParseIP(a)}
		}
		return out, nil
	}
}

// failingLookup is an ipLookupFunc that always fails with a DNS error.
func failingLookup(host string, notFound bool) ipLookupFunc {
	return func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: notFound}
	}
}

// ---------- profile/action builders ----------

func testCapabilityArtifact() wireArtifactRef {
	return wireArtifactRef{ID: contract.NewID(), Digest: contract.Hash([]byte("httpread-adapter-qualification"))}
}

// buildProfileJSON builds a schema-valid zatiti.httpread/v1 profile document
// whose capability_evidence.profile_digest correctly binds to the rest of
// the document, exactly as loadProfile requires.
func buildProfileJSON(t *testing.T, origins, mediaTypes []string, maxBytes int64) json.RawMessage {
	t.Helper()
	w := wireHTTPReadProfile{
		Schema:            "zatiti.httpread/v1",
		AllowedOrigins:    origins,
		MaxBytes:          maxBytes,
		TimeoutSeconds:    30,
		MaxRedirects:      0,
		AllowedMediaTypes: mediaTypes,
		CapabilityEvidence: wireCapabilityEvidence{
			Artifact:         testCapabilityArtifact(),
			AdapterVersion:   "test-1",
			SourceRevision:   "test-rev",
			ProtocolRevision: "http/1.1",
			QualifiedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Capabilities:     []string{"read"},
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

// defaultProfileJSON is a profile permitting origin and mediaType with a 1
// MiB body bound.
func defaultProfileJSON(t *testing.T, origin, mediaType string) json.RawMessage {
	t.Helper()
	return buildProfileJSON(t, []string{origin}, []string{mediaType}, 1<<20)
}

// newTestAdapter constructs an *Adapter directly (bypassing New's own
// dedicated safety-checking Transport) wired to transport, blobs and a
// fixed-address resolver, with the given profile. Behavior tests exercise
// the request/response handling this way, exactly as the resolution and
// dial-pinning safety net is exercised separately and directly in
// dial_test.go.
func newTestAdapter(t *testing.T, transport http.RoundTripper, blobs contract.BlobStore, lookup ipLookupFunc, profile json.RawMessage) *Adapter {
	t.Helper()
	p, err := loadProfile(profile)
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	contractDoc, err := buildContractDocument()
	if err != nil {
		t.Fatalf("buildContractDocument: %v", err)
	}
	return &Adapter{
		profile: p,
		deps: contract.AdapterDependencies{
			HTTP:  &http.Client{},
			Clock: newFakeClock(),
			Blobs: blobs,
		},
		client:     &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		schema:     contractDoc,
		resolveIPs: lookup,
	}
}

func testDispatch(t *testing.T, action any) contract.Dispatch {
	t.Helper()
	raw, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	return contract.Dispatch{
		OperationID: contract.NewID(),
		AttemptID:   contract.NewID(),
		Generation:  1,
		Adapter:     adapterName,
		Action:      raw,
		Deadline:    time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC),
	}
}

func readAction(url, expectedMediaType string, headers ...wireReadHeader) wireHTTPReadParameters {
	if headers == nil {
		headers = []wireReadHeader{}
	}
	return wireHTTPReadParameters{
		Schema:            "zatiti.httpread.action/v1",
		Kind:              kindRead,
		URL:               url,
		Method:            http.MethodGet,
		Headers:           headers,
		ExpectedMediaType: expectedMediaType,
	}
}

// jsonResponse builds a canned *http.Response carrying body with the given
// content type.
func jsonResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     http.Header{"Content-Type": []string{contentType}},
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

func validateEvidence(t *testing.T, doc json.RawMessage) {
	t.Helper()
	schema, err := evidenceSchema()
	if err != nil {
		t.Fatalf("evidenceSchema: %v", err)
	}
	if err := contract.ValidateSchema(schema, doc); err != nil {
		t.Errorf("evidence does not validate against zatiti.httpread.evidence/v1: %v\ndoc: %s", err, doc)
	}
}
