package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestValidOperationID(t *testing.T) {
	valid := []string{"a", "artifact.get", "artifact.upload.chunk", "a.b_c.d", "execution_profile.create"}
	for _, id := range valid {
		if !validOperationID(id) {
			t.Fatalf("operation ID %q rejected", id)
		}
	}
	invalid := []string{"", ".a", "a.", "a..b", "Artifact.get", "a-b", "a b", "a.中"}
	for _, id := range invalid {
		if validOperationID(id) {
			t.Fatalf("operation ID %q accepted", id)
		}
	}
}

func TestValidOwnerName(t *testing.T) {
	valid := []string{"artifacts", "local_io", "a"}
	for _, name := range valid {
		if !validOwnerName(name) {
			t.Fatalf("owner name %q rejected", name)
		}
	}
	invalid := []string{"", "9lives", "Artifacts", "bad-name", "a b"}
	for _, name := range invalid {
		if validOwnerName(name) {
			t.Fatalf("owner name %q accepted", name)
		}
	}
}

func TestValidateDescriptorShape(t *testing.T) {
	valid := contract.Descriptor{
		ID: "tester.op", Version: 1, Owner: "tester",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	if err := validateDescriptorShape(&valid); err != nil {
		t.Fatalf("valid descriptor rejected: %v", err)
	}

	cases := map[string]func(d *contract.Descriptor){
		"empty id":             func(d *contract.Descriptor) { d.ID = "" },
		"uppercase id":         func(d *contract.Descriptor) { d.ID = "Tester.Op" },
		"double dot":           func(d *contract.Descriptor) { d.ID = "a..b" },
		"zero version":         func(d *contract.Descriptor) { d.Version = 0 },
		"negative version":     func(d *contract.Descriptor) { d.Version = -1 },
		"bad owner":            func(d *contract.Descriptor) { d.Owner = "Bad Owner" },
		"unknown visibility":   func(d *contract.Descriptor) { d.Visibility = "hidden" },
		"unknown mode":         func(d *contract.Descriptor) { d.Mode = "stream" },
		"unknown effect":       func(d *contract.Descriptor) { d.Effect = "magical" },
		"empty input schema":   func(d *contract.Descriptor) { d.InputSchema = nil },
		"empty output schema":  func(d *contract.Descriptor) { d.OutputSchema = nil },
		"query submission key": func(d *contract.Descriptor) { d.SubmissionKey = true },
		"bad caller":           func(d *contract.Descriptor) { d.Callers = []string{"Not.An.Id"} },
	}
	for name, mutate := range cases {
		d := valid
		d.SubmissionKey = false
		d.Callers = nil
		mutate(&d)
		if err := validateDescriptorShape(&d); err == nil {
			t.Fatalf("%s: descriptor shape accepted", name)
		}
	}

	// Mutations require submission keys, except the one-time init.
	mutation := valid
	mutation.Mode = contract.ModeMutation
	mutation.Effect = contract.EffectExternalMutation
	if err := validateDescriptorShape(&mutation); err == nil ||
		!strings.Contains(err.Error(), "requires a submission key") {
		t.Fatalf("mutation without submission key accepted: %v", err)
	}
	mutation.SubmissionKey = true
	if err := validateDescriptorShape(&mutation); err != nil {
		t.Fatalf("mutation with submission key rejected: %v", err)
	}
	init := mutation
	init.ID = "installation.init"
	init.SubmissionKey = true
	if err := validateDescriptorShape(&init); err == nil ||
		!strings.Contains(err.Error(), "one-time init") {
		t.Fatalf("init with submission key accepted: %v", err)
	}
	init.SubmissionKey = false
	if err := validateDescriptorShape(&init); err != nil {
		t.Fatalf("one-time init without submission key rejected: %v", err)
	}
}

func TestSameSchema(t *testing.T) {
	a := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	b := json.RawMessage("{\n  \"properties\": {\"x\": {\"type\": \"string\"}},\n  \"type\": \"object\"\n}")
	if err := sameSchema(a, b, "input"); err != nil {
		t.Fatalf("canonically identical schemas differ: %v", err)
	}
	if err := sameSchema(nil, nil, "input"); err != nil {
		t.Fatalf("both absent should match: %v", err)
	}
	if err := sameSchema(json.RawMessage(`{}`), json.RawMessage(" {} "), "input"); err != nil {
		t.Fatalf("schemas differing only in whitespace should match: %v", err)
	}
	if err := sameSchema(a, nil, "input"); err == nil {
		t.Fatal("presence mismatch not detected")
	}
	if err := sameSchema(a, json.RawMessage(`{"type":"string"}`), "input"); err == nil ||
		!strings.Contains(err.Error(), "input schema") {
		t.Fatalf("content mismatch not detected with the right label: %v", err)
	}
}

func TestCheckSchemaDocument(t *testing.T) {
	valid := json.RawMessage(`{
		"type": "object",
		"properties": {"a": {"$ref": "#/$defs/A"}, "b": {"$ref": "#/x"}},
		"allOf": [{"$ref": "#/$defs/A"}],
		"items": {"type": "string"},
		"additionalProperties": {"$ref": "#/$defs/A"},
		"not": {"$ref": "#/$defs/A"},
		"if": {"$ref": "#/$defs/A"},
		"then": {"$ref": "#/$defs/A"},
		"else": {"$ref": "#/$defs/A"},
		"propertyNames": {"$ref": "#/$defs/A"},
		"oneOf": [{"$ref": "#/$defs/A"}, {"type": "null"}],
		"prefixItems": [{"$ref": "#/$defs/A"}],
		"dependentSchemas": {"x": {"$ref": "#/$defs/A"}},
		"contains": {"$ref": "#/$defs/A"},
		"definitions-note": {"enum": ["#/$defs/Missing"]},
		"x": {"type": "string"},
		"$defs": {"A": {"type": "object", "properties": {"self": {"$ref": "#/$defs/A"}, "root": {"$ref": "#"}}}}
	}`)
	if err := checkSchemaDocument(valid); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}

	invalid := map[string]string{
		"unresolved $defs ref":   `{"properties":{"a":{"$ref":"#/$defs/Missing"}}}`,
		"unresolved sibling ref": `{"properties":{"a":{"$ref":"#/properties/nowhere"}}}`,
		"remote ref":             `{"$ref":"https://example.com/schema.json"}`,
		"other fragment":         `{"$ref":"other.json#/$defs/A"}`,
		"non-string ref":         `{"$ref":42}`,
	}
	for name, doc := range invalid {
		if err := checkSchemaDocument(json.RawMessage(doc)); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}

	// Data positions are never walked: an enum value that looks like a
	// pointer is data, not a schema reference.
	if err := checkSchemaDocument(json.RawMessage(`{"enum":["#/$defs/Missing"]}`)); err != nil {
		t.Fatalf("enum data walked as schema: %v", err)
	}
}

func TestCLIMCPDerivation(t *testing.T) {
	if got := cliTokensFor("installation.init"); len(got) != 1 || got[0] != "init" {
		t.Fatalf("installation.init CLI %v, want [init]", got)
	}
	if got := cliTokensFor("capabilities.list"); len(got) != 1 || got[0] != "capabilities" {
		t.Fatalf("capabilities.list CLI %v, want [capabilities]", got)
	}
	if got := cliTokensFor("artifact.upload.chunk"); len(got) != 3 {
		t.Fatalf("artifact.upload.chunk CLI %v, want three tokens", got)
	}
	if got := mcpNameFor("installation.init"); got != "zatiti_installation_init" {
		t.Fatalf("installation.init MCP %q", got)
	}
	if got := mcpNameFor("capabilities.list"); got != "zatiti_capabilities" {
		t.Fatalf("capabilities.list MCP %q", got)
	}
	if got := mcpNameFor("artifact.get"); got != "zatiti_artifact_get" {
		t.Fatalf("artifact.get MCP %q", got)
	}
}

func TestCloneDescriptorIsDeep(t *testing.T) {
	d := contract.Descriptor{
		ID: "tester.op", Version: 1, Owner: "tester",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		CLI:           []string{"tester", "op"},
		ScopeRequired: []string{"installation_id"},
		Callers:       []string{"other.op"},
	}
	clone := cloneDescriptor(&d)
	clone.CLI[0] = "tampered"
	clone.ScopeRequired[0] = "tampered"
	clone.Callers[0] = "tampered"
	clone.InputSchema[0] = ' '
	if d.CLI[0] == "tampered" || d.ScopeRequired[0] == "tampered" || d.Callers[0] == "tampered" || d.InputSchema[0] == ' ' {
		t.Fatal("cloneDescriptor did not deep-copy mutable fields")
	}
}
