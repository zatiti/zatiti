package serenity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// ---------- fakes ----------

// probes counts every way the adapter could reach outside itself: HTTP
// round trips, secret reads and blob store calls. The adapter must leave all
// of them at zero.
type probes struct {
	mu      sync.Mutex
	http    int
	secrets int
	blobs   int
}

func (p *probes) bump(n *int) {
	p.mu.Lock()
	*n++
	p.mu.Unlock()
}

func (p *probes) total() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.http + p.secrets + p.blobs
}

type probeTransport struct{ p *probes }

func (t probeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.p.bump(&t.p.http)
	return nil, errors.New("the serenity adapter must not send a request")
}

type probeSecrets struct{ p *probes }

func (s probeSecrets) Put(context.Context, string, []byte) (string, error) {
	s.p.bump(&s.p.secrets)
	return "", errors.New("unexpected secret write")
}

func (s probeSecrets) Get(context.Context, string) ([]byte, error) {
	s.p.bump(&s.p.secrets)
	return []byte(testToken), nil
}

func (s probeSecrets) Delete(context.Context, string) error {
	s.p.bump(&s.p.secrets)
	return errors.New("unexpected secret delete")
}

type probeBlobs struct{ p *probes }

func (b probeBlobs) Stage(context.Context, io.Reader, int64) (string, contract.Digest, int64, error) {
	b.p.bump(&b.p.blobs)
	return "", "", 0, errors.New("unexpected blob stage")
}

func (b probeBlobs) Publish(context.Context, string, contract.Digest) error {
	b.p.bump(&b.p.blobs)
	return errors.New("unexpected blob publish")
}

func (b probeBlobs) Open(context.Context, contract.Digest, int64, int64) (io.ReadCloser, error) {
	b.p.bump(&b.p.blobs)
	return nil, errors.New("unexpected blob open")
}

func (b probeBlobs) RemoveStaged(context.Context, string) error {
	b.p.bump(&b.p.blobs)
	return errors.New("unexpected blob removal")
}

// ---------- profile builders ----------

const (
	testCredentialRef = "cred-serenity-1"
	testToken         = "srn_test_token_secret_value"
	testWriterOwner   = "writer:controller-1"
	testEndpoint      = "http://127.0.0.1:54217/mcp"
	testDestination   = "https://models.example.test/v1"
)

var (
	testBrainID       = contract.ID("7b0c1f0e-3c1e-4f4e-9a57-0d8a3a1b2c01")
	testOtherBrainID  = contract.ID("7b0c1f0e-3c1e-4f4e-9a57-0d8a3a1b2c02")
	testUnmappedBrain = contract.ID("7b0c1f0e-3c1e-4f4e-9a57-0d8a3a1b2c03")
)

func testCapabilityArtifact() wireArtifactRef {
	return wireArtifactRef{
		ID:     contract.ID("7b0c1f0e-3c1e-4f4e-9a57-0d8a3a1b2cff"),
		Digest: contract.Hash([]byte("serenity-adapter-qualification")),
	}
}

func testCapabilityEvidence() wireCapabilityEvidence {
	return wireCapabilityEvidence{
		Artifact:         testCapabilityArtifact(),
		AdapterVersion:   adapterVersion,
		SourceRevision:   pinnedCommit,
		ProtocolRevision: pinnedProtocolRevision,
		QualifiedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Capabilities:     []string{},
		Limitations:      []string{"no operation is dispatchable at this pin; see PROTOCOL.md"},
	}
}

// testEnforcementEvidence is the nested enforcement qualification record.
// The frozen schema requires its profile_digest, but it sits inside the
// digested region, so it cannot bind the enclosing profile; the adapter does
// not read it and refuses every "enforced" claim it could back.
func testEnforcementEvidence() wireCapabilityEvidence {
	e := testCapabilityEvidence()
	e.ProfileDigest = contract.Hash([]byte("serenity-enforcement-qualification"))
	return e
}

// truthfulProfile is the only shape of profile the pinned upstream can back:
// no supported operation and every guarantee unsupported.
func truthfulProfile() wireSerenityProfile {
	return wireSerenityProfile{
		Schema:  "zatiti.serenity/v1",
		Version: pinnedDescribe,
		Commit:  pinnedCommit,
		BrainMappings: []wireBrainMapping{
			{BrainID: testBrainID, Endpoint: testEndpoint, RootRef: "brain:worker-1",
				WriterOwner: testWriterOwner, Classification: "internal"},
		},
		SupportedOperations: []string{},
		Enforcement: wireBoundEnforcement{
			Cost: "unsupported", Disclosure: "unsupported",
			MaximumCost:          wireMoney{Currency: "USD", MicroUnits: 5_000_000},
			ProviderDestinations: []string{testDestination},
			Classifications:      []string{"public", "internal"},
			Evidence:             testEnforcementEvidence(),
		},
		CommandStatusLookup: wireLookupSemantics{Mode: "unsupported", Evidence: []wireArtifactRef{}},
		Freshness:           wireFreshnessCapability{ReadFacade: "unsupported", Evidence: []wireArtifactRef{}},
		TimeoutSeconds:      30,
		MaxBytes:            1 << 20,
		BackupRevisionProtocol: wireBackupProtocol{
			Mode: "unsupported", ProtocolProfile: "none", Evidence: []wireArtifactRef{},
		},
		CapabilityEvidence: testCapabilityEvidence(),
	}
}

// profileJSON marshals a profile whose capability_evidence.profile_digest
// correctly binds the rest of the document, after mutate has edited it.
func profileJSON(t *testing.T, mutate func(*wireSerenityProfile)) json.RawMessage {
	t.Helper()
	w := truthfulProfile()
	if mutate != nil {
		mutate(&w)
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

// newTestAdapter constructs the adapter with every outward dependency
// replaced by a probe.
func newTestAdapter(t *testing.T, profile json.RawMessage) (contract.Adapter, *probes) {
	t.Helper()
	p := &probes{}
	deps := contract.AdapterDependencies{
		HTTP:    &http.Client{Transport: probeTransport{p}},
		Secrets: probeSecrets{p},
		Blobs:   probeBlobs{p},
	}
	a, err := New(deps, profile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a, p
}

// ---------- action builders ----------

const actionSchema = "zatiti.serenity.action/v1"

var testCommandID = contract.ID("7b0c1f0e-3c1e-4f4e-9a57-0d8a3a1b2c10")

func testMoney(micro int64) wireMoney { return wireMoney{Currency: "USD", MicroUnits: micro} }

func testClaimRef() wireVersionRef {
	return wireVersionRef{ID: contract.ID("7b0c1f0e-3c1e-4f4e-9a57-0d8a3a1b2c20"), Version: 1}
}

func testFreshness() time.Time { return time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC) }

func recallAction(brain contract.ID) wireRecall {
	return wireRecall{Schema: actionSchema, BrainID: brain, AdapterCommandID: testCommandID, Kind: kindRecall,
		Query: "project priorities", MinimumFreshness: testFreshness(), MaxClaims: 10,
		MaximumCost: testMoney(1_000_000), AllowedProviderDestinations: []string{testDestination},
		Classification: "internal"}
}

func rememberAction(brain contract.ID) wireRemember {
	return wireRemember{Schema: actionSchema, BrainID: brain, AdapterCommandID: testCommandID, Kind: kindRemember,
		Text: "picked the staged rollout", Sources: []wireArtifactRef{}, WriterOwner: testWriterOwner,
		MaximumCost: testMoney(0), AllowedProviderDestinations: []string{}}
}

func inspectAction(brain contract.ID) wireInspect {
	return wireInspect{Schema: actionSchema, BrainID: brain, AdapterCommandID: testCommandID, Kind: kindInspect,
		Claim: testClaimRef(), MinimumFreshness: testFreshness()}
}

func promoteAction(brain contract.ID) wirePromote {
	return wirePromote{Schema: actionSchema, BrainID: brain, AdapterCommandID: testCommandID, Kind: kindPromote,
		SourceBrainID: testOtherBrainID, SourceClaim: testClaimRef(),
		SourceDisclosureEvidence: []wireArtifactRef{testCapabilityArtifact()},
		Text:                     "staged rollouts reduce incidents", Sources: []wireArtifactRef{},
		CuratorID:   contract.ID("7b0c1f0e-3c1e-4f4e-9a57-0d8a3a1b2c30"),
		WriterOwner: testWriterOwner, MaximumCost: testMoney(0), AllowedProviderDestinations: []string{}}
}

func retractAction(brain contract.ID) wireRetract {
	return wireRetract{Schema: actionSchema, BrainID: brain, AdapterCommandID: testCommandID, Kind: kindRetract,
		Claim: testClaimRef(), Reason: "superseded", WriterOwner: testWriterOwner, Removal: "active_recall"}
}

func exportAction(brain contract.ID) wireExportRevision {
	return wireExportRevision{Schema: actionSchema, BrainID: brain, AdapterCommandID: testCommandID, Kind: kindExportRevision,
		Revision: wireBrainRevision{BrainID: brain, Revision: "rev-1",
			Digest: contract.Hash([]byte("brain-revision")), ObservedAt: testFreshness()}}
}

// allKinds returns one schema-valid action per kind, targeting brain.
func allKinds(brain contract.ID) map[string]any {
	return map[string]any{
		kindRecall:         recallAction(brain),
		kindRemember:       rememberAction(brain),
		kindInspect:        inspectAction(brain),
		kindPromote:        promoteAction(brain),
		kindRetract:        retractAction(brain),
		kindExportRevision: exportAction(brain),
	}
}

func testDispatch(t *testing.T, action any) contract.Dispatch {
	t.Helper()
	raw, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	return contract.Dispatch{
		OperationID:   contract.ID("7b0c1f0e-3c1e-4f4e-9a57-0d8a3a1b2c40"),
		AttemptID:     contract.ID("7b0c1f0e-3c1e-4f4e-9a57-0d8a3a1b2c41"),
		Generation:    1,
		Adapter:       adapterName,
		Action:        raw,
		CredentialRef: testCredentialRef,
		Deadline:      time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC),
	}
}

// mustFault extracts the *contract.Fault an error must be, checking its code.
func mustFault(t *testing.T, err error, code string) *contract.Fault {
	t.Helper()
	var f *contract.Fault
	if !errors.As(err, &f) {
		t.Fatalf("error is not *contract.Fault: %T: %v", err, err)
	}
	if f.Code != code {
		t.Fatalf("fault code = %q, want %q (%s)", f.Code, code, f.Message)
	}
	return f
}

// requireFault asserts err is a *contract.Fault with code.
func requireFault(t *testing.T, err error, code string) {
	t.Helper()
	_ = mustFault(t, err, code)
}
