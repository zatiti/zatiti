package responses

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func testDeps() contract.AdapterDependencies {
	return contract.AdapterDependencies{
		HTTP:    &http.Client{},
		Secrets: newFakeSecrets(testCredentialRef, []byte(testToken)),
		Clock:   newFakeClock(),
		Blobs:   newFakeBlobStore(),
	}
}

func TestNewAcceptsBoundProfile(t *testing.T) {
	t.Parallel()
	a, err := New(testDeps(), bindProfile(t, defaultProfile()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.Name() != "responses" {
		t.Fatalf("Name = %q", a.Name())
	}
}

func TestNewRejectsProfiles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*wireResponsesProfile)
		code string
		want string
	}{
		{"input rate unit", func(p *wireResponsesProfile) { p.InputRate.Unit = "request" }, contract.CodeInvalidInput, "input_rate.unit"},
		{"output rate unit", func(p *wireResponsesProfile) { p.OutputRate.Unit = "input_token" }, contract.CodeInvalidInput, "output_rate.unit"},
		{"currency disagreement", func(p *wireResponsesProfile) { p.Enforcement.MaximumCost.Currency = "EUR" }, contract.CodeInvalidInput, "currency"},
		{"cost bounds unsupported", func(p *wireResponsesProfile) { p.Enforcement.Cost = enforcementUnsupported }, contract.CodeCapabilityUnsupported, "cost bounds unsupported"},
		{"disclosure bounds unsupported", func(p *wireResponsesProfile) { p.Enforcement.Disclosure = enforcementUnsupported }, contract.CodeCapabilityUnsupported, "disclosure bounds unsupported"},
		{"endpoint not a declared destination", func(p *wireResponsesProfile) {
			p.Enforcement.ProviderDestinations = []string{"https://other.example.test"}
		}, contract.CodePermissionDenied, "provider_destinations"},
		{"no declared destination", func(p *wireResponsesProfile) { p.Enforcement.ProviderDestinations = []string{} }, contract.CodePermissionDenied, "provider_destinations"},
		{"destination on another port", func(p *wireResponsesProfile) {
			p.Enforcement.ProviderDestinations = []string{"https://models.example.test:8443"}
		}, contract.CodePermissionDenied, "provider_destinations"},
		{"destination with another path", func(p *wireResponsesProfile) {
			p.Enforcement.ProviderDestinations = []string{"https://models.example.test/v2/steps"}
		}, contract.CodePermissionDenied, "provider_destinations"},
		{"endpoint userinfo", func(p *wireResponsesProfile) {
			p.Endpoint = "https://user:pw@models.example.test/v1/steps"
		}, contract.CodeInvalidInput, "userinfo"},
		{"endpoint query", func(p *wireResponsesProfile) {
			p.Endpoint = "https://models.example.test/v1/steps?key=abc"
		}, contract.CodeInvalidInput, "query"},
		{"plain http endpoint", func(p *wireResponsesProfile) { p.Endpoint = "http://models.example.test/v1/steps" }, contract.CodeInvalidInput, "schema"},
		{"missing model", func(p *wireResponsesProfile) { p.Model = "" }, contract.CodeInvalidInput, "schema"},
		{"missing currency", func(p *wireResponsesProfile) { p.Currency = "" }, contract.CodeInvalidInput, "schema"},
		{"zero rate denominator", func(p *wireResponsesProfile) { p.OutputRate.DenominatorUnits = 0 }, contract.CodeInvalidInput, "schema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := defaultProfile()
			tc.edit(&p)
			_, err := New(testDeps(), bindProfile(t, p))
			f := mustFault(t, err, tc.code)
			if !strings.Contains(f.Message, tc.want) {
				t.Fatalf("message %q does not mention %q", f.Message, tc.want)
			}
		})
	}
}

func TestNewAcceptsOriginWideDestination(t *testing.T) {
	t.Parallel()
	p := defaultProfile()
	p.Enforcement.ProviderDestinations = []string{"https://other.example.test", "https://MODELS.example.test:443/"}
	if _, err := New(testDeps(), bindProfile(t, p)); err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestNewRejectsUnboundCapabilityEvidence(t *testing.T) {
	t.Parallel()
	raw := bindProfile(t, defaultProfile())

	// Any change to the profile after qualification -- here a cheaper
	// price -- breaks the digest binding.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["input_rate"] = json.RawMessage(`{"numerator_micro_units":1,"denominator_units":1,"unit":"input_token"}`)
	tampered, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(testDeps(), tampered)
	f := mustFault(t, err, contract.CodeInvalidInput)
	if !strings.Contains(f.Message, "does not bind this exact profile") {
		t.Fatalf("message = %q", f.Message)
	}
}

func TestNewRejectsUnknownFieldAndMissingDependencies(t *testing.T) {
	t.Parallel()
	raw := bindProfile(t, defaultProfile())
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["default_model"] = json.RawMessage(`"fallback"`)
	extra, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(testDeps(), extra)
	assertFault(t, err, contract.CodeInvalidInput)

	deps := testDeps()
	deps.HTTP = nil
	_, err = New(deps, raw)
	assertFault(t, err, contract.CodeInvalidInput)

	deps = testDeps()
	deps.Clock = nil
	_, err = New(deps, raw)
	assertFault(t, err, contract.CodeInvalidInput)
}

func TestContractDocument(t *testing.T) {
	t.Parallel()
	a, err := New(testDeps(), bindProfile(t, defaultProfile()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var doc contractDocument
	if err := json.Unmarshal(a.Contract(), &doc); err != nil {
		t.Fatalf("contract document: %v", err)
	}
	if doc.Schema != "zatiti.responses.contract/v1" {
		t.Fatalf("schema = %q", doc.Schema)
	}
	// Production qualifies exactly the revisions PROTOCOL.md pins.
	if len(doc.QualifiedProtocolRevisions) != 1 || doc.QualifiedProtocolRevisions[0] != openaiProtocolRevision {
		t.Fatalf("qualified_protocol_revisions = %v, want [%s]", doc.QualifiedProtocolRevisions, openaiProtocolRevision)
	}
	// The embedded profile schema is the one New enforces.
	if err := contract.ValidateSchema(doc.ProfileSchema, bindProfile(t, defaultProfile())); err != nil {
		t.Fatalf("profile schema rejects a valid profile: %v", err)
	}
	for name, s := range map[string]json.RawMessage{"parameters": doc.ParametersSchema, "evidence": doc.EvidenceSchema, "context": doc.ContextSchema} {
		if err := contract.ValidateSchema(s, json.RawMessage(`{}`)); err == nil {
			t.Fatalf("%s schema accepted an empty object", name)
		}
	}
}
