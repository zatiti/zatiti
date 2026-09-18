package messaging

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
)

// frozenOperation is one entry of the frozen operation catalog of record,
// docs/implementation/operations.json.
type frozenOperation struct {
	ID               string          `json:"id"`
	Version          int64           `json:"version"`
	Owner            string          `json:"owner"`
	Visibility       string          `json:"visibility"`
	Mode             string          `json:"mode"`
	Effect           string          `json:"effect"`
	InputSchema      json.RawMessage `json:"input_schema"`
	OutputSchema     json.RawMessage `json:"output_schema"`
	CompletionSchema json.RawMessage `json:"completion_schema"`
	CLI              []string        `json:"cli"`
	MCP              string          `json:"mcp"`
	SubmissionKey    bool            `json:"submission_key"`
	ExpectedVersion  bool            `json:"expected_version"`
	ScopeRequired    []string        `json:"scope_required"`
	Callers          []string        `json:"callers"`
}

// frozenOperations loads every catalog entry this owner serves.
func frozenOperations(t *testing.T, owner string) map[string]frozenOperation {
	t.Helper()
	raw, err := os.ReadFile("../../docs/implementation/operations.json")
	if err != nil {
		t.Fatalf("read the frozen operation catalog: %v", err)
	}
	var doc struct {
		Operations []frozenOperation `json:"operations"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode the frozen operation catalog: %v", err)
	}
	out := map[string]frozenOperation{}
	for _, op := range doc.Operations {
		if op.Owner == owner {
			out[op.ID] = op
		}
	}
	if len(out) == 0 {
		t.Fatalf("the frozen catalog holds no %s operations", owner)
	}
	return out
}

// schemaBody decodes a schema and drops the merged shared $defs, leaving the
// operation's own body. An absent schema decodes to nil.
func schemaBody(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	if object, ok := body.(map[string]any); ok {
		delete(object, "$defs")
	}
	return body
}

// TestDescriptorsMatchFrozenCatalog pins every descriptor, public and
// internal, to its frozen catalog entry field by field: the registry refuses
// to assemble a module whose surface drifts from the catalog.
func TestDescriptorsMatchFrozenCatalog(t *testing.T) {
	svc := newEnv(t).svc
	frozen := frozenOperations(t, svc.Name())
	seen := map[string]bool{}
	for _, d := range svc.Descriptors() {
		want, ok := frozen[d.ID]
		if !ok {
			t.Errorf("%s: descriptor is not in the frozen catalog", d.ID)
			continue
		}
		seen[d.ID] = true
		if d.Visibility != want.Visibility {
			t.Errorf("%s: visibility %q, want %q", d.ID, d.Visibility, want.Visibility)
		}
		if d.Version != want.Version {
			t.Errorf("%s: version %d, want %d", d.ID, d.Version, want.Version)
		}
		if d.Owner != want.Owner {
			t.Errorf("%s: owner %q, want %q", d.ID, d.Owner, want.Owner)
		}
		if d.Mode != want.Mode {
			t.Errorf("%s: mode %q, want %q", d.ID, d.Mode, want.Mode)
		}
		if d.Effect != want.Effect {
			t.Errorf("%s: effect %q, want %q", d.ID, d.Effect, want.Effect)
		}
		if !slices.Equal(d.CLI, want.CLI) {
			t.Errorf("%s: CLI tokens %v, want %v", d.ID, d.CLI, want.CLI)
		}
		if d.MCP != want.MCP {
			t.Errorf("%s: MCP name %q, want %q", d.ID, d.MCP, want.MCP)
		}
		if d.SubmissionKey != want.SubmissionKey {
			t.Errorf("%s: submission key %t, want %t", d.ID, d.SubmissionKey, want.SubmissionKey)
		}
		if d.ExpectedVersion != want.ExpectedVersion {
			t.Errorf("%s: expected version %t, want %t", d.ID, d.ExpectedVersion, want.ExpectedVersion)
		}
		if !slices.Equal(d.ScopeRequired, want.ScopeRequired) {
			t.Errorf("%s: scope requirements %v, want %v", d.ID, d.ScopeRequired, want.ScopeRequired)
		}
		if d.Visibility == "public" {
			// The registry refuses a public operation with a caller allowlist.
			if len(d.Callers) != 0 {
				t.Errorf("%s: public descriptor declares callers %v", d.ID, d.Callers)
			}
		} else {
			gotCallers, wantCallers := slices.Clone(d.Callers), slices.Clone(want.Callers)
			slices.Sort(gotCallers)
			slices.Sort(wantCallers)
			if !slices.Equal(gotCallers, wantCallers) {
				t.Errorf("%s: callers %v, want %v", d.ID, d.Callers, want.Callers)
			}
		}
		if !reflect.DeepEqual(schemaBody(t, d.InputSchema), schemaBody(t, want.InputSchema)) {
			t.Errorf("%s: input schema differs from the frozen catalog", d.ID)
		}
		if !reflect.DeepEqual(schemaBody(t, d.OutputSchema), schemaBody(t, want.OutputSchema)) {
			t.Errorf("%s: output schema differs from the frozen catalog", d.ID)
		}
		if !reflect.DeepEqual(schemaBody(t, d.CompletionSchema), schemaBody(t, want.CompletionSchema)) {
			t.Errorf("%s: completion schema differs from the frozen catalog", d.ID)
		}
	}
	for id := range frozen {
		if !seen[id] {
			t.Errorf("%s: frozen operation has no descriptor", id)
		}
	}
}
