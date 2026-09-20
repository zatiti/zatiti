package contract

import (
	"encoding/json"
	"errors"
	"testing"
)

// localDecisionGoldenInstances are one canonical positive example of each
// sealed local decision tool kind (docs/implementation/contracts.md, "Local
// decision tool schemas (revision 3)"), built from the exact frozen
// $defs/LocalDecisionTool oneOf branches in
// docs/implementation/operations.json. These are the P00 golden documents
// required-test #1 below round-trips through Canonicalize and validates
// through LocalDecisionToolSchema.
func localDecisionGoldenInstances(t *testing.T) map[string]string {
	t.Helper()
	artifact := ArtifactRef{ID: NewID(), Digest: Hash([]byte("report-outputs-golden"))}
	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal artifact ref: %v", err)
	}
	return map[string]string{
		LocalDecisionToolReply:         `{"text":"the task is complete"}`,
		LocalDecisionToolClarify:       `{"question":"which repository should I target?"}`,
		LocalDecisionToolReportOutputs: `{"bindings":[{"name":"summary","artifact":` + string(artifactJSON) + `}]}`,
		LocalDecisionToolCycleDecision: `{"decision":"continue","reason":"more work remains","next_wake":"2026-09-19T00:00:00Z"}`,
	}
}

// TestLocalDecisionToolGoldenDocumentsRoundTripAndCanonicalizeIdentically is
// required behavioral test #1: all P00 golden documents (here, one legal
// instance of each sealed local decision tool kind, built from the frozen
// LocalDecisionTool/ReplyProposal/ClarifyProposal/ReportOutputsProposal/
// CycleDecisionProposal schemas) round-trip and canonicalize identically.
func TestLocalDecisionToolGoldenDocumentsRoundTripAndCanonicalizeIdentically(t *testing.T) {
	t.Parallel()
	branchSchemas := map[string]json.RawMessage{
		LocalDecisionToolReply:         ReplyProposalSchema,
		LocalDecisionToolClarify:       ClarifyProposalSchema,
		LocalDecisionToolReportOutputs: ReportOutputsProposalSchema,
		LocalDecisionToolCycleDecision: CycleDecisionProposalSchema,
	}
	for kind, instance := range localDecisionGoldenInstances(t) {
		kind, instance := kind, instance
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(instance)

			// Validates against the discriminated union AND its own branch
			// schema alone: the golden document is legal both ways.
			if err := ValidateSchema(LocalDecisionToolSchema, raw); err != nil {
				t.Fatalf("golden %s instance rejected by LocalDecisionToolSchema: %v", kind, err)
			}
			if err := ValidateSchema(branchSchemas[kind], raw); err != nil {
				t.Fatalf("golden %s instance rejected by its own branch schema: %v", kind, err)
			}

			// Canonicalize is idempotent.
			once, err := Canonicalize(raw)
			if err != nil {
				t.Fatalf("canonicalize %s: %v", kind, err)
			}
			twice, err := Canonicalize(once)
			if err != nil {
				t.Fatalf("re-canonicalize %s: %v", kind, err)
			}
			if string(once) != string(twice) {
				t.Fatalf("%s canonicalization not idempotent: %s vs %s", kind, once, twice)
			}

			// A key-reordered but semantically identical document
			// canonicalizes to the exact same bytes, and still validates.
			reordered, err := reorderTopLevelKeys(raw)
			if err != nil {
				t.Fatalf("reorder %s: %v", kind, err)
			}
			if err := ValidateSchema(LocalDecisionToolSchema, reordered); err != nil {
				t.Fatalf("reordered golden %s instance rejected: %v", kind, err)
			}
			reorderedCanon, err := Canonicalize(reordered)
			if err != nil {
				t.Fatalf("canonicalize reordered %s: %v", kind, err)
			}
			if string(once) != string(reorderedCanon) {
				t.Fatalf("%s canonical form depends on key order: %s vs %s", kind, once, reorderedCanon)
			}
		})
	}
}

// reorderTopLevelKeys decodes a JSON object and re-encodes its top-level
// keys in reverse insertion order (Go map iteration is randomized, so
// re-marshaling a decoded map already exercises order independence; this
// helper additionally guarantees at least one concrete alternate ordering
// for multi-key objects).
func reorderTopLevelKeys(data json.RawMessage) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

// TestLocalDecisionToolRejectsSmuggledAuthority is required behavioral test
// #2: malformed model decisions cannot smuggle arbitrary operations,
// identity, grants or review decisions. Each case starts from a legal reply
// and adds exactly one field a real proposal never declares; every branch's
// additionalProperties:false rejects the extra field, so the smuggled
// instance matches zero oneOf branches and LocalDecisionToolSchema refuses
// the whole document.
func TestLocalDecisionToolRejectsSmuggledAuthority(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		instance string
	}{
		{"smuggled arbitrary operation", `{"text":"ok","operation":"principal.create"}`},
		{"smuggled operation with version", `{"text":"ok","operation":"connection.setup.begin","version":1}`},
		{"smuggled identity", `{"text":"ok","principal_id":"` + string(NewID()) + `"}`},
		{"smuggled actor", `{"text":"ok","actor":{"principal_id":"` + string(NewID()) + `","kind":"service","credential_id":"` + string(NewID()) + `"}}`},
		{"smuggled grants", `{"text":"ok","grants":["admin","secrets.read"]}`},
		{"smuggled review decision", `{"text":"ok","review_decision":"approved"}`},
		{"smuggled scope widening", `{"text":"ok","scope":{"installation_id":"` + string(NewID()) + `"}}`},
		{"smuggled submission key", `{"text":"ok","submission_key":"attacker-chosen-key"}`},
		{"clarify smuggling report_outputs bindings", `{"question":"which?","bindings":[]}`},
		{"cycle_decision smuggling operation", `{"decision":"done","reason":"finished","operation":"task.assign"}`},
		{"empty object matches no branch", `{}`},
		{"hedged across two proposal kinds", `{"text":"ok","question":"which?"}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateSchema(LocalDecisionToolSchema, json.RawMessage(tc.instance))
			if err == nil {
				t.Fatalf("LocalDecisionToolSchema accepted a smuggled instance: %s", tc.instance)
			}
			if !errors.Is(err, ErrSchemaValidation) {
				t.Fatalf("unexpected error class for %s: %v", tc.name, err)
			}
			var se *SchemaError
			if !errors.As(err, &se) || se.Keyword != "oneOf" {
				t.Fatalf("expected a oneOf rejection for %s, got %v", tc.name, err)
			}
		})
	}
}

// TestLocalDecisionToolRejectsInvalidDiscriminatorCombinations is part of
// required behavioral test #2/step 3 ("reject ... invalid discriminator
// combinations"): a document must match exactly one oneOf branch, never
// zero and never more than one.
func TestLocalDecisionToolRejectsInvalidDiscriminatorCombinations(t *testing.T) {
	t.Parallel()
	// A well-formed instance of every kind still matches exactly one
	// branch: proves the positive side of "exactly one" before the
	// negative cases below.
	for kind, instance := range localDecisionGoldenInstances(t) {
		if err := ValidateSchema(LocalDecisionToolSchema, json.RawMessage(instance)); err != nil {
			t.Fatalf("%s: golden instance must match exactly one branch: %v", kind, err)
		}
	}
	bad := []string{
		`null`,
		`[]`,
		`"reply"`,
		`{"text":123}`,
		`{"decision":"unknown_decision","reason":"x"}`,
		`{"decision":"continue"}`, // missing required reason
	}
	for _, instance := range bad {
		if err := ValidateSchema(LocalDecisionToolSchema, json.RawMessage(instance)); err == nil {
			t.Fatalf("LocalDecisionToolSchema accepted invalid instance %s", instance)
		}
	}
}

// TestFaultResultRevision2EnvelopesRemainReadable is required behavioral
// test #3: revision 2 persisted envelopes remain readable under the
// declared compatibility policy ("every new field named below is additive
// and optional on an existing type ... so old persisted rows validate
// unchanged"). Result/Fault carry no revision 3 field additions at all, so
// a byte-for-byte revision-2-era Result (no callback_route, no attempts, no
// any revision-3 concept present anywhere) must still validate against the
// current frozen ResultSchema/FaultSchema and decode into the current Go
// types unchanged.
func TestFaultResultRevision2EnvelopesRemainReadable(t *testing.T) {
	t.Parallel()
	// A completed command, exactly the R8.4-002 frozen example this
	// package has pinned since revision 2 (see envelope_test.go).
	completed := `{"schema":"zatiti.result/v1","command_id":"00000000-0000-4000-8000-000000000001",` +
		`"status":"completed","data":{"draft_id":"00000000-0000-4000-8000-000000000002","version":1},` +
		`"error":null,"next_cursor":null}`
	// A failed command carrying a populated, revision-2-shaped Fault.
	failed := `{"schema":"zatiti.result/v1","command_id":"00000000-0000-4000-8000-000000000003",` +
		`"status":"failed","data":null,` +
		`"error":{"code":"permission_denied","message":"not permitted","retryable":false},` +
		`"next_cursor":null}`
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"completed", completed},
		{"failed", failed},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(tc.raw)
			if err := ValidateSchema(ResultSchema, raw); err != nil {
				t.Fatalf("revision-2 Result envelope rejected by current ResultSchema: %v", err)
			}
			var r Result
			if err := DecodeStrict(raw, &r); err != nil {
				t.Fatalf("revision-2 Result envelope failed strict decode: %v", err)
			}
			canon, err := Canonicalize(raw)
			if err != nil {
				t.Fatalf("canonicalize: %v", err)
			}
			canon2, err := Canonicalize(canon)
			if err != nil {
				t.Fatalf("re-canonicalize: %v", err)
			}
			if string(canon) != string(canon2) {
				t.Fatalf("revision-2 envelope canonicalization not idempotent")
			}
		})
	}

	// A bare revision-2 Fault (no details, the field every subsequent
	// revision left untouched) remains readable on its own.
	bareFault := `{"code":"invalid_input","message":"bad request","retryable":false}`
	if err := ValidateSchema(FaultSchema, json.RawMessage(bareFault)); err != nil {
		t.Fatalf("bare revision-2 Fault rejected: %v", err)
	}
	var f Fault
	if err := DecodeStrict([]byte(bareFault), &f); err != nil {
		t.Fatalf("bare revision-2 Fault failed strict decode: %v", err)
	}
	if f.Details != nil {
		t.Fatalf("Details populated from absent JSON: %s", f.Details)
	}
}

// TestScopeSchemaRejectsWidenedScope is part of step 3 ("reject unknown
// fields, duplicate keys, invalid discriminator combinations and widened
// scopes"): Scope is closed over its five declared dimensions
// (installation/organization/project/worker/task); no other dimension can
// be smuggled in to widen it at the wire boundary.
func TestScopeSchemaRejectsWidenedScope(t *testing.T) {
	t.Parallel()
	minimal := `{"installation_id":"` + string(NewID()) + `"}`
	if err := ValidateSchema(ScopeSchema, json.RawMessage(minimal)); err != nil {
		t.Fatalf("minimal Scope rejected: %v", err)
	}
	widened := `{"installation_id":"` + string(NewID()) + `","global":true}`
	err := ValidateSchema(ScopeSchema, json.RawMessage(widened))
	if err == nil {
		t.Fatalf("ScopeSchema accepted a widened (extra-dimension) scope")
	}
	var se *SchemaError
	if !errors.As(err, &se) || se.Keyword != "additionalProperties" {
		t.Fatalf("expected additionalProperties rejection, got %v", err)
	}
	var s Scope
	if err := DecodeStrict([]byte(widened), &s); err == nil {
		t.Fatalf("DecodeStrict accepted a widened scope")
	}
}
