package voice

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// schemaRefs collects every local "#/$defs/..." reference in a schema.
func schemaRefs(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if r, ok := x["$ref"].(string); ok && strings.HasPrefix(r, "#/$defs/") {
			out[strings.TrimPrefix(r, "#/$defs/")] = true
		}
		for _, c := range x {
			schemaRefs(c, out)
		}
	case []any:
		for _, c := range x {
			schemaRefs(c, out)
		}
	}
}

// The embedded catalog is validated without the registry's shared $defs,
// so every voice schema must carry the definitions it references.
func TestCatalogSchemasAreSelfContained(t *testing.T) {
	var ops []map[string]any
	if err := json.Unmarshal(catalog, &ops); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		for _, key := range []string{"input_schema", "output_schema"} {
			schema, _ := op[key].(map[string]any)
			defs, _ := schema["$defs"].(map[string]any)
			refs := map[string]bool{}
			schemaRefs(schema, refs)
			for name := range refs {
				if _, ok := defs[name]; !ok {
					t.Errorf("%s %s references #/$defs/%s but does not embed it", op["id"], key, name)
				}
			}
		}
	}
}

// The embedded catalog is the voice slice of the generated operation
// catalog, apart from the embedded $defs.
func TestCatalogMatchesGeneratedOperations(t *testing.T) {
	raw, err := os.ReadFile("../../docs/implementation/operations.json")
	if err != nil {
		t.Fatal(err)
	}
	var src struct {
		Operations []map[string]any `json:"operations"`
	}
	if err = json.Unmarshal(raw, &src); err != nil {
		t.Fatal(err)
	}
	var ops []map[string]any
	if err = json.Unmarshal(catalog, &ops); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{}
	for _, o := range src.Operations {
		if o["owner"] == "voice" {
			want = append(want, o)
		}
	}
	if len(ops) != len(want) {
		t.Fatalf("embedded catalog has %d operations, generated catalog has %d voice operations", len(ops), len(want))
	}
	for i := range ops {
		got := ops[i]
		for _, key := range []string{"input_schema", "output_schema"} {
			s := map[string]any{}
			for k, v := range got[key].(map[string]any) {
				if k != "$defs" {
					s[k] = v
				}
			}
			got[key] = s
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("embedded %v differs from generated %v; regenerate internal/voice/catalog.json", got["id"], want[i]["id"])
		}
	}
}
