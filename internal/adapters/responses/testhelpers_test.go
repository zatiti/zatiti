package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// fakeSecrets is a minimal contract.SecretStore that counts resolutions.
type fakeSecrets struct {
	mu     sync.Mutex
	values map[string][]byte
	gets   int
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	if s.getErr != nil {
		return nil, s.getErr
	}
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

func (s *fakeSecrets) getCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

// fakeBlobStore is a minimal in-memory contract.BlobStore. failStageAt
// makes the Nth Stage call (1-based) fail, to exercise staging failures at
// a precise point.
type fakeBlobStore struct {
	mu          sync.Mutex
	objects     map[contract.Digest][]byte
	stagedRefs  map[string]contract.Digest
	stageCalls  int
	failStageAt int
	removed     []string
	openErr     error
}

func newFakeBlobStore() *fakeBlobStore {
	return &fakeBlobStore{objects: map[contract.Digest][]byte{}, stagedRefs: map[string]contract.Digest{}}
}

// put seeds an object directly, returning its digest.
func (b *fakeBlobStore) put(data []byte) contract.Digest {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := contract.Hash(data)
	b.objects[d] = data
	return d
}

// putAs seeds data under a digest that does not match it, simulating a
// store returning bytes other than the ones a reference binds.
func (b *fakeBlobStore) putAs(d contract.Digest, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[d] = data
}

func (b *fakeBlobStore) Stage(_ context.Context, r io.Reader, _ int64) (string, contract.Digest, int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stageCalls++
	if b.failStageAt == b.stageCalls {
		return "", "", 0, errors.New("disk full at /private/path")
	}
	ref := fmt.Sprintf("staged-%d", b.stageCalls)
	d := contract.Hash(data)
	b.objects[d] = data
	b.stagedRefs[ref] = d
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

func (b *fakeBlobStore) RemoveStaged(_ context.Context, ref string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removed = append(b.removed, ref)
	return nil
}

// stagedBytes returns the bytes staged under ref.
func (b *fakeBlobStore) stagedBytes(t *testing.T, ref string) []byte {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.stagedRefs[ref]
	if !ok {
		t.Fatalf("no staged object %q", ref)
	}
	return b.objects[d]
}

func (b *fakeBlobStore) stageCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stageCalls
}

// countingTransport wraps a RoundTripper function, counts physical calls,
// and retains a copy of every request it saw (body included).
type countingTransport struct {
	mu       sync.Mutex
	calls    int
	requests []*http.Request
	bodies   [][]byte
	getBody  []bool
	fn       func(*http.Request) (*http.Response, error)
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	c.mu.Lock()
	c.calls++
	c.requests = append(c.requests, r)
	c.bodies = append(c.bodies, body)
	c.getBody = append(c.getBody, r.GetBody != nil)
	c.mu.Unlock()
	return c.fn(r)
}

func (c *countingTransport) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// respond returns a transport function answering every request with a
// canned status and body.
func respond(status int, body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}
}

// ---------- synthetic wire protocol ----------

// testProtocolRevision names the synthetic protocol below. It is invented
// for these tests: it resembles no vendor's API and asserts nothing about
// one. It exists so the protocol-independent machinery (one physical call,
// bounds, accounting, evidence, redaction) can be proven without a guessed
// upstream contract.
const testProtocolRevision = "zatiti-synthetic-wire/1"

const testCredentialHeader = "X-Synthetic-Credential"

type testProtocol struct {
	lim        protocolLimits
	inputBound func(body []byte) *int64
	encodeErr  error
}

// newTestProtocol is a synthetic protocol that can enforce every bound: it
// claims an output ceiling and bounds input tokens at one per body byte.
func newTestProtocol() *testProtocol {
	return &testProtocol{
		lim: protocolLimits{BoundsOutputTokens: true, SupportsContinuation: true},
		inputBound: func(body []byte) *int64 {
			n := int64(len(body))
			return &n
		},
	}
}

type testWireTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type testWireRequest struct {
	Model  string         `json:"synthetic_model"`
	Limit  int64          `json:"synthetic_limit"`
	Turns  []testWireTurn `json:"synthetic_turns"`
	Tools  []string       `json:"synthetic_tools"`
	Resume string         `json:"synthetic_resume,omitempty"`
}

type testWireResponse struct {
	Ref    string   `json:"ref"`
	State  string   `json:"state"`
	Finish string   `json:"finish"`
	Texts  []string `json:"texts"`
	Calls  []struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"calls"`
	Tokens *struct {
		In  int64 `json:"in"`
		Out int64 `json:"out"`
	} `json:"tokens"`
	NoCharge bool   `json:"no_charge"`
	Refusal  string `json:"refusal"`
	Resume   string `json:"resume"`
	UsageRef string `json:"usage_ref"`
	Error    *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *testProtocol) limits() protocolLimits { return p.lim }

func (p *testProtocol) encode(in protocolRequest) (protocolCall, error) {
	if p.encodeErr != nil {
		return protocolCall{}, p.encodeErr
	}
	w := testWireRequest{Model: in.Model, Limit: in.MaxOutputTokens, Turns: []testWireTurn{}, Tools: []string{}, Resume: in.ContinuationReference}
	for _, msg := range in.Context.Messages {
		for _, part := range msg.DecodedParts {
			if part.Text != nil {
				w.Turns = append(w.Turns, testWireTurn{Role: msg.Role, Text: part.Text.Text})
			}
		}
	}
	for _, tool := range in.Context.Document.Tools {
		w.Tools = append(w.Tools, tool.Name)
	}
	body, err := json.Marshal(w)
	if err != nil {
		return protocolCall{}, err
	}
	call := protocolCall{
		Method: http.MethodPost,
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   body,
	}
	if p.inputBound != nil {
		call.InputTokenBound = p.inputBound(body)
	}
	return call, nil
}

func (p *testProtocol) authorize(h http.Header, secret []byte) {
	h.Set(testCredentialHeader, string(secret))
}

func (p *testProtocol) decode(_ int, _ http.Header, body []byte) (protocolResult, error) {
	var w testWireResponse
	if err := json.Unmarshal(body, &w); err != nil {
		return protocolResult{}, fmt.Errorf("synthetic response is not JSON: %w", err)
	}
	r := protocolResult{
		State: w.State, ResponseID: w.Ref, FinishReason: w.Finish, Texts: w.Texts,
		NoCharge: w.NoCharge, Refusal: w.Refusal, ContinuationReference: w.Resume, UsageReference: w.UsageRef,
	}
	for _, c := range w.Calls {
		r.ToolCalls = append(r.ToolCalls, protocolToolCall{ID: c.ID, Name: c.Name, Arguments: c.Args})
	}
	if w.Tokens != nil {
		r.Usage = &protocolTokenUsage{InputTokens: w.Tokens.In, OutputTokens: w.Tokens.Out}
	}
	if w.Error != nil {
		r.ErrorCode, r.ErrorMessage = w.Error.Code, w.Error.Message
	}
	return r, nil
}

// ---------- profile/action/context builders ----------

const (
	testEndpoint      = "https://models.example.test/v1/steps"
	testModel         = "synthetic-model-a"
	testCredentialRef = "cred-1"
	testToken         = "synthetic-credential-0123456789"
)

var testConnectionID = contract.ID("7b1f6c1e-2f65-4d0e-9a53-0c2f4f1f9a11")

func testCapabilityArtifact() wireArtifactRef {
	return wireArtifactRef{ID: "3c9d2b7a-6f41-4e8b-8a0d-5e1f2a3b4c5d", Digest: contract.Hash([]byte("responses-adapter-qualification"))}
}

func testCapabilityEvidence() wireCapabilityEvidence {
	return wireCapabilityEvidence{
		Artifact:         testCapabilityArtifact(),
		AdapterVersion:   "test-1",
		SourceRevision:   "test-rev",
		ProtocolRevision: testProtocolRevision,
		ProfileDigest:    contract.Hash([]byte("placeholder")),
		QualifiedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Capabilities:     []string{"model_step"},
		Limitations:      []string{},
	}
}

// defaultProfile is a fully enforced profile: 2 micro-units per input
// token, 7/2 micro-units per output token, and a ceiling comfortably above
// the default action's worst case.
func defaultProfile() wireResponsesProfile {
	return wireResponsesProfile{
		Schema:           "zatiti.responses/v1",
		Endpoint:         testEndpoint,
		Model:            testModel,
		ConnectionID:     testConnectionID,
		MaxInputTokens:   100000,
		MaxOutputTokens:  4096,
		MaxResponseBytes: 1 << 20,
		TimeoutSeconds:   30,
		Currency:         "USD",
		InputRate:        wireRationalRate{NumeratorMicroUnits: 2, DenominatorUnits: 1, Unit: "input_token"},
		OutputRate:       wireRationalRate{NumeratorMicroUnits: 7, DenominatorUnits: 2, Unit: "output_token"},
		Enforcement: wireBoundEnforcement{
			Cost:                 enforcementEnforced,
			Disclosure:           enforcementEnforced,
			MaximumCost:          wireMoney{Currency: "USD", MicroUnits: 1000000},
			ProviderDestinations: []string{testEndpoint},
			Classifications:      []string{"public", "internal"},
			Evidence:             testCapabilityEvidence(),
		},
		CapabilityEvidence: testCapabilityEvidence(),
	}
}

// bindProfile marshals w with a capability_evidence.profile_digest that
// correctly binds the rest of the document, exactly as loadProfile requires.
func bindProfile(t *testing.T, w wireResponsesProfile) json.RawMessage {
	t.Helper()
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

var testTool = wireVersionRef{ID: "0e8a4f52-91b3-4c67-a2d8-6b5c4d3e2f10", Version: 3}

func textPart(t *testing.T, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(wireContextText{Kind: partText, Text: text})
	if err != nil {
		t.Fatalf("marshal text part: %v", err)
	}
	return raw
}

// defaultContext is a complete-capture context with two text messages and
// one tool definition.
func defaultContext(t *testing.T) wireContextArtifact {
	t.Helper()
	return wireContextArtifact{
		Schema:                "zatiti.context/v1",
		AttemptID:             "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		Scope:                 contract.Scope{InstallationID: "f0e1d2c3-b4a5-4968-8776-655443322110"},
		ConfigurationRevision: 1,
		Worker:                wireVersionRef{ID: "11111111-2222-4333-8444-555555555555", Version: 1},
		ExecutionProfile:      wireVersionRef{ID: "66666666-7777-4888-8999-aaaaaaaaaaaa", Version: 1},
		SkillVersions:         []wireVersionRef{},
		Messages: []wireContextMessage{
			{ID: "aaaaaaaa-0000-4000-8000-000000000001", Role: "system", Origin: "effective_instruction",
				Parts: []json.RawMessage{textPart(t, "You are a careful researcher.")}, SourceArtifacts: []wireArtifactRef{}},
			{ID: "aaaaaaaa-0000-4000-8000-000000000002", Role: "user", Origin: "user_message",
				Parts: []json.RawMessage{textPart(t, "Summarize the source.")}, SourceArtifacts: []wireArtifactRef{}},
		},
		Tools: []wireContextToolDefinition{{
			Tool: testTool, Name: "fetch_source", Description: "Fetch one public source.",
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
			Effect: "external_read", Destinations: []string{"https://sources.example.test"},
			BindingID: "bbbbbbbb-0000-4000-8000-000000000001", SchemaDigest: contract.Hash([]byte("fetch_source schema")),
		}},
		SourceArtifacts: []wireArtifactRef{},
		Capture:         "complete",
		CreatedAt:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// storeContext persists c in blobs and returns the ArtifactRef binding it.
func storeContext(t *testing.T, blobs *fakeBlobStore, c wireContextArtifact) wireArtifactRef {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal context: %v", err)
	}
	return wireArtifactRef{ID: "cccccccc-0000-4000-8000-000000000001", Digest: blobs.put(raw)}
}

func defaultAction(ref wireArtifactRef) wireResponsesParameters {
	return wireResponsesParameters{
		Schema:               "zatiti.responses.action/v1",
		Kind:                 kindModelStep,
		ContextArtifact:      ref,
		MaxOutputTokens:      1000,
		ToolContractVersions: []wireVersionRef{testTool},
	}
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

// harness wires one adapter to its fakes.
type harness struct {
	adapter   *Adapter
	transport *countingTransport
	blobs     *fakeBlobStore
	secrets   *fakeSecrets
	protocol  *testProtocol
	dispatch  contract.Dispatch
	action    wireResponsesParameters
}

type harnessOption func(*harnessConfig)

type harnessConfig struct {
	client   *http.Client // when set, replaces the counting fake transport
	profile  wireResponsesProfile
	context  wireContextArtifact
	protocol *testProtocol
	action   func(*wireResponsesParameters)
}

func withProfile(edit func(*wireResponsesProfile)) harnessOption {
	return func(c *harnessConfig) { edit(&c.profile) }
}

// withServer points the profile at a live test server and uses its client
// (the real net/http transport) instead of the counting fake.
func withServer(client *http.Client, endpoint string) harnessOption {
	return func(c *harnessConfig) {
		c.client = client
		c.profile.Endpoint = endpoint
		c.profile.Enforcement.ProviderDestinations = []string{endpoint}
	}
}

func withContext(edit func(*wireContextArtifact)) harnessOption {
	return func(c *harnessConfig) { edit(&c.context) }
}

func withProtocol(edit func(*testProtocol)) harnessOption {
	return func(c *harnessConfig) { edit(c.protocol) }
}

func withAction(edit func(*wireResponsesParameters)) harnessOption {
	return func(c *harnessConfig) { c.action = edit }
}

// newHarness builds an adapter over the synthetic protocol whose transport
// is fn, with a stored default context and a ready dispatch.
func newHarness(t *testing.T, fn func(*http.Request) (*http.Response, error), opts ...harnessOption) *harness {
	t.Helper()
	cfg := &harnessConfig{profile: defaultProfile(), context: defaultContext(t), protocol: newTestProtocol()}
	for _, opt := range opts {
		opt(cfg)
	}
	h := &harness{
		transport: &countingTransport{fn: fn},
		blobs:     newFakeBlobStore(),
		secrets:   newFakeSecrets(testCredentialRef, []byte(testToken)),
		protocol:  cfg.protocol,
	}
	client := &http.Client{Transport: h.transport}
	if cfg.client != nil {
		client = cfg.client
	}
	deps := contract.AdapterDependencies{
		HTTP:    client,
		Secrets: h.secrets,
		Clock:   newFakeClock(),
		Blobs:   h.blobs,
	}
	a, err := newWithProtocols(deps, bindProfile(t, cfg.profile), map[string]wireProtocol{testProtocolRevision: cfg.protocol})
	if err != nil {
		t.Fatalf("newWithProtocols: %v", err)
	}
	h.adapter = a
	h.action = defaultAction(storeContext(t, h.blobs, cfg.context))
	if cfg.action != nil {
		cfg.action(&h.action)
	}
	h.dispatch = testDispatch(t, h.action)
	return h
}

// ---------- assertions ----------

// mustFault extracts the *contract.Fault an error must be.
func mustFault(t *testing.T, err error, code string) *contract.Fault {
	t.Helper()
	var f *contract.Fault
	if !errors.As(err, &f) || f == nil {
		t.Fatalf("error is not *contract.Fault: %T: %v", err, err)
	}
	if f.Code != code {
		t.Fatalf("fault code = %q, want %q (%s)", f.Code, code, f.Message)
	}
	return f
}

// assertFault is mustFault for callers that only need the code checked.
func assertFault(t *testing.T, err error, code string) {
	t.Helper()
	_ = mustFault(t, err, code)
}

// decodeEvidence schema-validates and strict-decodes an observation's
// evidence, and checks Observation.Usage is the evidence's own usage.
func decodeEvidence(t *testing.T, obs contract.Observation) wireResponsesEvidence {
	t.Helper()
	schema, err := evidenceSchema()
	if err != nil {
		t.Fatalf("evidenceSchema: %v", err)
	}
	if err := contract.ValidateSchema(schema, obs.Evidence); err != nil {
		t.Fatalf("evidence does not match zatiti.responses.evidence/v1: %v\n%s", err, obs.Evidence)
	}
	var ev wireResponsesEvidence
	if err := contract.DecodeStrict(obs.Evidence, &ev); err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	want, err := json.Marshal(ev.Output.Usage)
	if err != nil {
		t.Fatalf("marshal usage: %v", err)
	}
	if !bytes.Equal(want, obs.Usage) {
		t.Fatalf("Observation.Usage = %s, want the evidence usage %s", obs.Usage, want)
	}
	return ev
}

// assertNoSecret fails if the credential appears anywhere an observation or
// fault could carry it, including every staged blob.
func assertNoSecret(t *testing.T, h *harness, obs contract.Observation, err error) {
	t.Helper()
	haystacks := map[string]string{"evidence": string(obs.Evidence), "usage": string(obs.Usage), "provider_reference": obs.ProviderReference}
	if err != nil {
		haystacks["error"] = err.Error()
	}
	h.blobs.mu.Lock()
	for ref, d := range h.blobs.stagedRefs {
		haystacks["staged "+ref] = string(h.blobs.objects[d])
	}
	h.blobs.mu.Unlock()
	for where, s := range haystacks {
		if strings.Contains(s, testToken) {
			t.Fatalf("credential leaked into %s: %s", where, s)
		}
	}
}
