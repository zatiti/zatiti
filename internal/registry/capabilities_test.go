package registry

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// capabilitiesItems mirrors the frozen capabilities.list output envelope.
type capabilitiesItems struct {
	Items []descriptorDTO `json:"items"`
}

// invokeCapabilitiesList calls capabilities.list through the registry's own
// Module surface.
func invokeCapabilitiesList(t *testing.T, reg *Registry, unit contract.Unit) (contract.Payload, capabilitiesItems) {
	t.Helper()
	payload, err := reg.Handle(context.Background(), unit, contract.Invocation{
		Operation: "capabilities.list",
		Version:   1,
		Input:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("capabilities.list invocation failed: %v", err)
	}
	var out capabilitiesItems
	if err := contract.DecodeStrict(payload.Data, &out); err != nil {
		t.Fatalf("capabilities.list output is not the declared DTO shape: %v", err)
	}
	return payload, out
}

func TestCapabilitiesListEnumeratesThePublicSurface(t *testing.T) {
	reg := mustRegistry(t)
	payload, items := invokeCapabilitiesList(t, reg, &fakeUnit{})

	if payload.Status != contract.StatusCompleted {
		t.Fatalf("status %q, want completed", payload.Status)
	}
	public := reg.Public()
	if len(items.Items) != len(public) {
		t.Fatalf("capabilities.list holds %d items, Public() holds %d", len(items.Items), len(public))
	}
	for i, dto := range items.Items {
		d := public[i]
		if dto.ID != d.ID || dto.Version != d.Version || dto.Owner != d.Owner || dto.Effect != d.Effect ||
			dto.MCP != d.MCP || dto.SubmissionKey != d.SubmissionKey || dto.ExpectedVersion != d.ExpectedVersion ||
			!slices.Equal(dto.CLI, d.CLI) || !slices.Equal(dto.ScopeRequired, d.ScopeRequired) {
			t.Fatalf("item %d does not match descriptor %s", i, d.ID)
		}
		if err := sameSchema(dto.InputSchema, d.InputSchema, "input"); err != nil {
			t.Fatalf("operation %s: %v", d.ID, err)
		}
		if err := sameSchema(dto.OutputSchema, d.OutputSchema, "output"); err != nil {
			t.Fatalf("operation %s: %v", d.ID, err)
		}
	}
}

func TestCapabilitiesListMatchesTheFrozenDTOSchema(t *testing.T) {
	reg := mustRegistry(t)
	payload, _ := invokeCapabilitiesList(t, reg, &fakeUnit{})

	// The response validates against the operation's own merged output
	// schema — DTO and schema consistency is proven, not assumed.
	e, ok := reg.entries[versionKey{"capabilities.list", 1}]
	if !ok {
		t.Fatal("capabilities.list v1 is not registered")
	}
	if err := contract.ValidateSchema(e.mergedOutput, payload.Data); err != nil {
		t.Fatalf("capabilities.list output violates its frozen schema: %v", err)
	}

	// The DTO never leaks routing internals: visibility, mode and caller
	// allowlists are absent.
	var raw struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(payload.Data, &raw); err != nil {
		t.Fatalf("capabilities.list output does not decode: %v", err)
	}
	for _, item := range raw.Items {
		for _, forbidden := range []string{"visibility", "mode", "callers"} {
			if _, present := item[forbidden]; present {
				t.Fatalf("capabilities item leaks routing field %q", forbidden)
			}
		}
		// Array-valued fields marshal as [] not null.
		for _, arrayField := range []string{"scope_requirements", "cli"} {
			v, present := item[arrayField]
			if !present {
				t.Fatalf("capabilities item lacks %s", arrayField)
			}
			if _, isArray := v.([]any); !isArray {
				t.Fatalf("capabilities item field %s is %v, want an array", arrayField, v)
			}
		}
	}
}

func TestCapabilitiesListIsDiscoverableOnReadOnlyUnits(t *testing.T) {
	reg := mustRegistry(t)
	payload, items := invokeCapabilitiesList(t, reg, &fakeUnit{ro: true})
	if payload.Status != contract.StatusCompleted || len(items.Items) == 0 {
		t.Fatal("capabilities discovery must work on read-only units")
	}
}

func TestCapabilitiesListRejectsMalformedInput(t *testing.T) {
	reg := mustRegistry(t)
	if _, err := reg.Handle(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "capabilities.list",
		Version:   1,
		Input:     json.RawMessage(`{"scope":{},"extra":1}`),
	}); faultCodeOf(t, err) != contract.CodeInvalidInput {
		t.Fatal("unknown input fields must be invalid_input")
	}
}

func TestCapabilitiesSchemaReturnsExactDescriptors(t *testing.T) {
	reg := mustRegistry(t)
	for _, want := range reg.Public() {
		input, err := json.Marshal(map[string]any{"operation": want.ID, "version": want.Version})
		if err != nil {
			t.Fatalf("input does not marshal: %v", err)
		}
		payload, err := reg.Handle(context.Background(), &fakeUnit{}, contract.Invocation{
			Operation: "capabilities.schema",
			Version:   1,
			Input:     input,
		})
		if err != nil {
			t.Fatalf("capabilities.schema(%s v%d) failed: %v", want.ID, want.Version, err)
		}
		var out struct {
			Resource descriptorDTO `json:"resource"`
		}
		if err := contract.DecodeStrict(payload.Data, &out); err != nil {
			t.Fatalf("capabilities.schema(%s) output does not decode: %v", want.ID, err)
		}
		if out.Resource.ID != want.ID || out.Resource.Version != want.Version {
			t.Fatalf("capabilities.schema returned %s v%d, want %s v%d",
				out.Resource.ID, out.Resource.Version, want.ID, want.Version)
		}
		if err := sameSchema(out.Resource.InputSchema, want.InputSchema, "input"); err != nil {
			t.Fatalf("operation %s: %v", want.ID, err)
		}
		if err := sameSchema(out.Resource.OutputSchema, want.OutputSchema, "output"); err != nil {
			t.Fatalf("operation %s: %v", want.ID, err)
		}
	}
}

func TestCapabilitiesSchemaHidesInternalOperations(t *testing.T) {
	reg, err := New(append(catalogModules(), internalModule("tester", "tester.hidden")))
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}
	input, err := json.Marshal(map[string]any{"operation": "tester.hidden", "version": int64(1)})
	if err != nil {
		t.Fatalf("input does not marshal: %v", err)
	}
	if _, err := reg.Handle(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "capabilities.schema",
		Version:   1,
		Input:     input,
	}); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatal("internal operations must not be discoverable")
	}
}

func TestCapabilitiesSchemaRejectsUnknownOperationsAndVersions(t *testing.T) {
	reg := mustRegistry(t)
	cases := []struct {
		operation string
		version   int64
	}{
		{"ghost.op", 1},
		{"artifact.get", 99},
		{"", 1},
	}
	for _, tc := range cases {
		input, err := json.Marshal(map[string]any{"operation": tc.operation, "version": tc.version})
		if err != nil {
			t.Fatalf("input does not marshal: %v", err)
		}
		if _, err := reg.Handle(context.Background(), &fakeUnit{}, contract.Invocation{
			Operation: "capabilities.schema",
			Version:   1,
			Input:     input,
		}); faultCodeOf(t, err) != contract.CodeNotFound {
			t.Fatalf("operation %q version %d must be not_found", tc.operation, tc.version)
		}
	}
}

func TestCapabilitiesSchemaValidatesItsInput(t *testing.T) {
	reg := mustRegistry(t)
	if _, err := reg.Handle(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "capabilities.schema",
		Version:   1,
		Input:     json.RawMessage(`{"operation":"artifact.get"}`),
	}); faultCodeOf(t, err) != contract.CodeInvalidInput {
		t.Fatal("schema lookup without version must be invalid_input")
	}
	if _, err := reg.Handle(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "capabilities.schema",
		Version:   1,
		Input:     json.RawMessage(`{"operation":"artifact.get","version":0}`),
	}); faultCodeOf(t, err) != contract.CodeInvalidInput {
		t.Fatal("version below one must be invalid_input")
	}
}
