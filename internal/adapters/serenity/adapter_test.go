package serenity

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func emptyObservation(o contract.Observation) bool {
	return o.Disposition == "" && o.ProviderReference == "" && o.Evidence == nil && o.Usage == nil && o.ConfirmedAt == nil
}

func TestNameAndContract(t *testing.T) {
	a, _ := newTestAdapter(t, profileJSON(t, nil))
	if a.Name() != "serenity" {
		t.Fatalf("Name = %q", a.Name())
	}
	var doc struct {
		Schema           string           `json:"schema"`
		ProfileSchema    json.RawMessage  `json:"profile_schema"`
		ParametersSchema json.RawMessage  `json:"parameters_schema"`
		EvidenceSchema   json.RawMessage  `json:"evidence_schema"`
		CapabilityReport capabilityReport `json:"capability_report"`
	}
	if err := json.Unmarshal(a.Contract(), &doc); err != nil {
		t.Fatalf("Contract is not JSON: %v", err)
	}
	if doc.Schema != "zatiti.serenity.contract/v1" {
		t.Fatalf("contract schema = %q", doc.Schema)
	}
	// The published schemas are the ones the adapter itself enforces.
	if err := contract.ValidateSchema(doc.ProfileSchema, profileJSON(t, nil)); err != nil {
		t.Fatalf("published profile schema rejects a loadable profile: %v", err)
	}
	for kind, act := range allKinds(testBrainID) {
		raw, _ := json.Marshal(act)
		if err := contract.ValidateSchema(doc.ParametersSchema, raw); err != nil {
			t.Fatalf("published parameters schema rejects %s: %v", kind, err)
		}
	}

	report := doc.CapabilityReport
	want := upstreamPin{Module: pinnedModule, Commit: pinnedCommit, Describe: pinnedDescribe, ProtocolRevision: pinnedProtocolRevision}
	if report.Upstream != want {
		t.Fatalf("report upstream = %+v, want %+v", report.Upstream, want)
	}
	if report.PhysicalCalls != "none" || report.CommandLookup != "unsupported" || report.LostAcknowledgement != "retained_unknown" {
		t.Fatalf("report semantics = %+v", report)
	}
	for _, g := range []string{gapCommandIdentity, gapCommandStatusLookup, gapIdempotentReplay, gapCostBound, gapDisclosureDestinations, gapSingleWriterEnforced} {
		if !slices.Contains(report.UnsupportedGuarantees, g) {
			t.Fatalf("report does not declare %q unsupported", g)
		}
	}
}

// TestWriterCapabilityReportIsTruthful guards the single source of truth:
// every frozen action kind has exactly one row, none is supported, and each
// names the gap that blocks all of them. Marking a kind supported without a
// dispatch path must fail here, not at run time.
func TestWriterCapabilityReportIsTruthful(t *testing.T) {
	ops := pinnedOperations()
	kinds := allKinds(testBrainID)
	if len(ops) != len(kinds) {
		t.Fatalf("report has %d rows for %d frozen kinds", len(ops), len(kinds))
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if _, ok := kinds[op.Kind]; !ok || seen[op.Kind] {
			t.Fatalf("report row %q is unknown or duplicated", op.Kind)
		}
		seen[op.Kind] = true
		if op.Supported {
			t.Fatalf("%s is marked supported, but this build has no dispatch path and PROTOCOL.md proves none", op.Kind)
		}
		if !slices.Contains(op.Missing, gapSingleRequestCallPath) {
			t.Fatalf("%s does not name %s", op.Kind, gapSingleRequestCallPath)
		}
	}
	for _, write := range []string{kindRemember, kindPromote, kindRetract} {
		op, _ := capabilityFor(write)
		if !slices.Contains(op.Missing, gapCommandIdentity) || !slices.Contains(op.Missing, gapCommandStatusLookup) {
			t.Fatalf("write kind %s must name the command identity and lookup gaps: %v", write, op.Missing)
		}
	}
	if op, _ := capabilityFor(kindRecall); !slices.Contains(op.Missing, gapCostBound) || !slices.Contains(op.Missing, gapDisclosureDestinations) {
		t.Fatalf("recall may disclose and incur charges upstream; its row must name both bound gaps: %v", op.Missing)
	}
}

// TestProtocolDocumentMatchesPin keeps PROTOCOL.md and the compiled pin from
// drifting apart, and keeps local paths out of the committed document.
func TestProtocolDocumentMatchesPin(t *testing.T) {
	raw, err := os.ReadFile("PROTOCOL.md")
	if err != nil {
		t.Fatalf("PROTOCOL.md must ship with the package: %v", err)
	}
	doc := string(raw)
	for _, want := range []string{pinnedModule, pinnedCommit, pinnedDescribe, "MEMORY_VERBS v1", "2025-11-25"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("PROTOCOL.md does not record %q", want)
		}
	}
	for _, banned := range []string{"/Users/", "/home/", "~/"} {
		if strings.Contains(doc, banned) {
			t.Fatalf("PROTOCOL.md contains a local path fragment %q", banned)
		}
	}
}

func TestInvokeRefusesEveryKindWithoutAnyPhysicalCall(t *testing.T) {
	a, p := newTestAdapter(t, profileJSON(t, nil))
	for kind, act := range allKinds(testBrainID) {
		t.Run(kind, func(t *testing.T) {
			obs, err := a.Invoke(context.Background(), testDispatch(t, act))
			f := mustFault(t, err, contract.CodeCapabilityUnsupported)
			if f.Retryable {
				t.Fatal("an unsupported capability is not retryable")
			}
			if !emptyObservation(obs) {
				t.Fatalf("a refusal must not carry an observation: %+v", obs)
			}
			var details unsupportedDetails
			if err := json.Unmarshal(f.Details, &details); err != nil {
				t.Fatalf("fault details: %v", err)
			}
			op, _ := capabilityFor(kind)
			if details.Kind != kind || details.UpstreamCommit != pinnedCommit || details.UpstreamModule != pinnedModule ||
				!slices.Equal(details.Missing, op.Missing) {
				t.Fatalf("fault details = %+v", details)
			}
			if !strings.Contains(f.Message, kind) || !strings.Contains(f.Message, gapSingleRequestCallPath) {
				t.Fatalf("fault message does not name the kind and gap: %s", f.Message)
			}
		})
	}
	if n := p.total(); n != 0 {
		t.Fatalf("adapter reached outside itself %d times (http=%d secrets=%d blobs=%d)", n, p.http, p.secrets, p.blobs)
	}
}

// TestUnauthorizedBrainNotQueried covers Z18.unauthorized_brain_not_queried
// at the adapter seam: a brain the profile does not map receives nothing.
func TestUnauthorizedBrainNotQueried(t *testing.T) {
	a, p := newTestAdapter(t, profileJSON(t, nil))
	for kind, act := range allKinds(testUnmappedBrain) {
		t.Run(kind, func(t *testing.T) {
			d := testDispatch(t, act)
			_, err := a.Invoke(context.Background(), d)
			requireFault(t, err, contract.CodePermissionDenied)
			_, err = a.Reconcile(context.Background(), d)
			requireFault(t, err, contract.CodePermissionDenied)
		})
	}
	if n := p.total(); n != 0 {
		t.Fatalf("unmapped brain caused %d outward calls", n)
	}
}

func TestForeignWriterOwnerRefused(t *testing.T) {
	a, _ := newTestAdapter(t, profileJSON(t, nil))
	remember := rememberAction(testBrainID)
	remember.WriterOwner = "writer:someone-else"
	promote := promoteAction(testBrainID)
	promote.WriterOwner = "writer:someone-else"
	retract := retractAction(testBrainID)
	retract.WriterOwner = "writer:someone-else"
	for kind, act := range map[string]any{kindRemember: remember, kindPromote: promote, kindRetract: retract} {
		t.Run(kind, func(t *testing.T) {
			_, err := a.Invoke(context.Background(), testDispatch(t, act))
			f := mustFault(t, err, contract.CodePermissionDenied)
			if !strings.Contains(f.Message, "exactly one canonical writer") {
				t.Fatalf("fault does not state the single-writer rule: %s", f.Message)
			}
		})
	}
}

func TestBoundsNoWiderThanProfile(t *testing.T) {
	a, _ := newTestAdapter(t, profileJSON(t, nil))
	cases := map[string]func(*wireRecall){
		"currency":       func(w *wireRecall) { w.MaximumCost.Currency = "EUR" },
		"cost":           func(w *wireRecall) { w.MaximumCost.MicroUnits = 5_000_001 },
		"destination":    func(w *wireRecall) { w.AllowedProviderDestinations = []string{"https://other.example.test/v1"} },
		"classification": func(w *wireRecall) { w.Classification = "restricted" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			act := recallAction(testBrainID)
			mutate(&act)
			_, err := a.Invoke(context.Background(), testDispatch(t, act))
			requireFault(t, err, contract.CodePermissionDenied)
		})
	}

	// A bound equal to the profile ceiling is inside it: the action passes
	// admission and meets the capability refusal instead.
	act := recallAction(testBrainID)
	act.MaximumCost.MicroUnits = 5_000_000
	_, err := a.Invoke(context.Background(), testDispatch(t, act))
	requireFault(t, err, contract.CodeCapabilityUnsupported)
}

func TestInvalidDispatchRefused(t *testing.T) {
	a, p := newTestAdapter(t, profileJSON(t, nil))
	valid := testDispatch(t, rememberAction(testBrainID))

	wrongAdapter := valid
	wrongAdapter.Adapter = "github"
	noCredential := valid
	noCredential.CredentialRef = ""

	raw := func(act any, mutate func(map[string]any)) contract.Dispatch {
		d := testDispatch(t, act)
		d.Action = editRaw(t, d.Action, mutate)
		return d
	}
	mismatched := exportAction(testBrainID)
	mismatched.Revision.BrainID = testOtherBrainID

	cases := map[string]contract.Dispatch{
		"wrong adapter": wrongAdapter,
		"no credential": noCredential,
		"unknown field": raw(rememberAction(testBrainID), func(m map[string]any) { m["endpoint"] = testEndpoint }),
		"unknown kind":  raw(rememberAction(testBrainID), func(m map[string]any) { m["kind"] = "synthesize" }),
		"wrong schema":  raw(rememberAction(testBrainID), func(m map[string]any) { m["schema"] = "zatiti.serenity.action/v2" }),
		"historical erasure requested": raw(retractAction(testBrainID), func(m map[string]any) {
			m["removal"] = "historical_erasure"
		}),
		"brain id not a uuid":     raw(recallAction(testBrainID), func(m map[string]any) { m["brain_id"] = "brain-one" }),
		"export revision foreign": testDispatch(t, mismatched),
		"action not an object":    {Adapter: adapterName, CredentialRef: testCredentialRef, Action: json.RawMessage(`[]`)},
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := a.Invoke(context.Background(), d)
			requireFault(t, err, contract.CodeInvalidInput)
			_, err = a.Reconcile(context.Background(), d)
			requireFault(t, err, contract.CodeInvalidInput)
		})
	}
	if n := p.total(); n != 0 {
		t.Fatalf("invalid dispatch caused %d outward calls", n)
	}
}

// TestReconcileRetainsUnknown covers the adapter half of
// Z18.memory_write_unknown: with no upstream command lookup, Reconcile
// builds nothing, sends nothing, repeats nothing, stages nothing, and says
// the original outcome stays unknown. It returns no Observation, so it makes
// no PhysicalCallEvidence and no request context for a request it never built.
func TestReconcileRetainsUnknown(t *testing.T) {
	a, p := newTestAdapter(t, profileJSON(t, nil))
	for kind, act := range allKinds(testBrainID) {
		t.Run(kind, func(t *testing.T) {
			obs, err := a.Reconcile(context.Background(), testDispatch(t, act))
			f := mustFault(t, err, contract.CodeCapabilityUnsupported)
			if f.Retryable {
				t.Fatal("a missing upstream lookup is not retryable")
			}
			if !emptyObservation(obs) {
				t.Fatalf("a reconcile that built no request must not carry an observation: %+v", obs)
			}
			var details unsupportedDetails
			if err := json.Unmarshal(f.Details, &details); err != nil {
				t.Fatalf("fault details: %v", err)
			}
			if details.Kind != kind || details.UpstreamCommit != pinnedCommit || details.OriginalOutcome != "retained_unknown" ||
				!slices.Equal(details.Missing, []string{gapCommandStatusLookup, gapCommandIdentity}) {
				t.Fatalf("fault details = %+v", details)
			}
			if !strings.Contains(f.Message, "stays unknown") || !strings.Contains(f.Message, gapCommandStatusLookup) {
				t.Fatalf("fault does not state the retained outcome and the gap: %s", f.Message)
			}
		})
	}

	// Reconciling again is still not a repeat of anything.
	d := testDispatch(t, rememberAction(testBrainID))
	for range 3 {
		_, err := a.Reconcile(context.Background(), d)
		requireFault(t, err, contract.CodeCapabilityUnsupported)
	}
	if n := p.total(); n != 0 {
		t.Fatalf("Reconcile reached outside the adapter %d times (http=%d secrets=%d blobs=%d)", n, p.http, p.secrets, p.blobs)
	}
}

// TestInvokeRefusalIsNotAReconcileRefusal keeps the two refusals distinct:
// only Reconcile speaks about an original outcome.
func TestInvokeRefusalIsNotAReconcileRefusal(t *testing.T) {
	a, _ := newTestAdapter(t, profileJSON(t, nil))
	_, err := a.Invoke(context.Background(), testDispatch(t, rememberAction(testBrainID)))
	f := mustFault(t, err, contract.CodeCapabilityUnsupported)
	var details unsupportedDetails
	if err := json.Unmarshal(f.Details, &details); err != nil {
		t.Fatal(err)
	}
	if details.OriginalOutcome != "" {
		t.Fatalf("an Invoke refusal has no original outcome to retain: %+v", details)
	}
}

// TestEmbeddedSchemasMatchFrozenCatalog proves schema.go is a faithful
// transcription: every embedded definition deep-equals the definition of the
// same name in the frozen catalog committed in this package's AGENTS.md.
func TestEmbeddedSchemasMatchFrozenCatalog(t *testing.T) {
	raw, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("read assignment: %v", err)
	}
	var catalogLine string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(line, "{") && strings.Contains(line, `"adapter_mapping"`) {
			catalogLine = line
		}
	}
	if catalogLine == "" {
		t.Fatal("frozen schema catalog not found in AGENTS.md")
	}
	decode := func(doc string) map[string]any {
		dec := json.NewDecoder(strings.NewReader(doc))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode schema document: %v", err)
		}
		return m
	}
	frozen := decode(catalogLine)["$defs"].(map[string]any)

	embedded := decode(schemaDefs)["$defs"].(map[string]any)
	embedded["SerenityProfile"] = decode(schemaProfileBody)
	embedded["SerenityParameters"] = decode(schemaParametersBody)
	embedded["SerenityEvidence"] = decode(schemaEvidenceBody)
	if len(embedded) < 30 {
		t.Fatalf("only %d embedded definitions; the closure of the three documents is larger", len(embedded))
	}
	for name, def := range embedded {
		want, ok := frozen[name]
		if !ok {
			t.Fatalf("embedded definition %s is not in the frozen catalog", name)
		}
		if !reflect.DeepEqual(def, want) {
			t.Fatalf("embedded definition %s differs from the frozen catalog", name)
		}
	}
}

// TestPublishedEvidenceSchemaIsRevision2 proves the evidence schema this
// adapter publishes types physical_call.request_context as an
// ArtifactLocator: a staged locator validates and a bare ArtifactRef, the
// revision 1 shape, does not.
func TestPublishedEvidenceSchemaIsRevision2(t *testing.T) {
	schema, err := evidenceSchema()
	if err != nil {
		t.Fatal(err)
	}
	digest := string(contract.Hash([]byte("request-record")))
	evidence := func(requestContext string) json.RawMessage {
		return json.RawMessage(`{"schema":"zatiti.serenity.evidence/v1","kind":"recall",` +
			`"brain_id":"` + string(testBrainID) + `","adapter_command_id":"` + string(testCommandID) + `",` +
			`"command_status":"unknown","claims":[],"brain_revisions":[],"output_artifacts":[],` +
			`"usage":{"accounting":{"currency":"USD","spent":0,"reserved":0,"estimated":0,"unknown":0,"advisory":false},"billing":"unknown"},` +
			`"staged_outputs":[{"staging_ref":"stage-1","digest":"` + digest + `","size":1,"media_type":"application/json","classification":"internal","purpose":"context"}],` +
			`"physical_call":{"operation_id":"` + string(testCommandID) + `","attempt_id":"` + string(testCommandID) + `",` +
			`"account_identity":"credential:x","requested_destination":"d","resolved_destination":"d",` +
			`"profile_digest":"` + digest + `","capability_evidence":{"id":"` + string(testCommandID) + `","digest":"` + digest + `"},` +
			`"started_at":"2026-01-01T00:00:00Z","finished_at":"2026-01-01T00:00:00Z",` +
			`"request_sent":"unknown","confirmation":"unknown","request_context":` + requestContext + `}}`)
	}
	staged := `{"kind":"staged","staging_ref":"stage-1","digest":"` + digest + `"}`
	if err := contract.ValidateSchema(schema, evidence(staged)); err != nil {
		t.Fatalf("a staged request context must validate: %v", err)
	}
	bare := `{"id":"` + string(testCommandID) + `","digest":"` + digest + `"}`
	if err := contract.ValidateSchema(schema, evidence(bare)); err == nil {
		t.Fatal("a bare ArtifactRef request context is the revision 1 shape and must not validate")
	}
}
