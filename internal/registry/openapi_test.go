package registry

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// parsedDoc is a decoded OpenAPI document with its component schemas.
type parsedDoc struct {
	Raw        []byte
	Document   map[string]any
	Paths      map[string]any
	Components map[string]any
}

func parseOpenAPI(t *testing.T, reg *Registry) parsedDoc {
	t.Helper()
	raw, err := reg.OpenAPI()
	if err != nil {
		t.Fatalf("OpenAPI() failed: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("OpenAPI document is not valid JSON: %v", err)
	}
	paths, _ := doc["paths"].(map[string]any)
	if paths == nil {
		t.Fatal("OpenAPI document has no paths object")
	}
	components, _ := doc["components"].(map[string]any)
	if components == nil {
		t.Fatal("OpenAPI document has no components object")
	}
	return parsedDoc{Raw: raw, Document: doc, Paths: paths, Components: components}
}

// collectRefs walks a decoded document and returns every $ref value.
func collectRefs(node any, into map[string]bool) {
	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok {
			into[ref] = true
		}
		for _, v := range n {
			collectRefs(v, into)
		}
	case []any:
		for _, v := range n {
			collectRefs(v, into)
		}
	}
}

func TestOpenAPIIsDeterministic(t *testing.T) {
	reg := mustRegistry(t)
	first, err := reg.OpenAPI()
	if err != nil {
		t.Fatalf("OpenAPI() failed: %v", err)
	}
	for i := 0; i < 3; i++ {
		again, err := reg.OpenAPI()
		if err != nil {
			t.Fatalf("OpenAPI() failed: %v", err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("OpenAPI() is not deterministic (call %d differs)", i)
		}
	}
	// A freshly assembled registry over the same surface emits identical
	// bytes: the document derives from typed state, not assembly order.
	other, err := newFakeRegistry()
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}
	otherDoc, err := other.OpenAPI()
	if err != nil {
		t.Fatalf("OpenAPI() failed: %v", err)
	}
	if !bytes.Equal(first, otherDoc) {
		t.Fatal("two registries over the same surface emit different documents")
	}
}

func TestOpenAPIDescribesEveryPublicOperation(t *testing.T) {
	reg := mustRegistry(t)
	doc := parseOpenAPI(t, reg)

	if doc.Document["openapi"] != "3.1.0" {
		t.Fatalf("openapi version %v, want 3.1.0", doc.Document["openapi"])
	}
	public := reg.Public()
	if len(doc.Paths) != len(public) {
		t.Fatalf("document has %d paths, want one per %d public operations", len(doc.Paths), len(public))
	}
	for _, d := range public {
		item, ok := doc.Paths["/v1/operations/"+d.ID]
		if !ok {
			t.Fatalf("operation %s has no path", d.ID)
		}
		post, ok := item.(map[string]any)["post"].(map[string]any)
		if !ok {
			t.Fatalf("operation %s has no POST operation", d.ID)
		}
		if post["operationId"] != d.ID {
			t.Fatalf("operation %s has operationId %v", d.ID, post["operationId"])
		}
	}
}

func TestOpenAPIIsClosedUnderItsOwnSchemas(t *testing.T) {
	reg := mustRegistry(t)
	doc := parseOpenAPI(t, reg)

	schemas, ok := doc.Components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("components.schemas is missing")
	}
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("catalog does not load: %v", err)
	}
	// Every shared definition plus one Output component per public
	// operation; nothing else, nothing missing.
	if want := len(cat.sharedDefs) + len(reg.publicList); len(schemas) != want {
		t.Fatalf("components.schemas holds %d schemas, want %d", len(schemas), want)
	}
	collected := map[string]bool{}
	collectRefs(doc.Document, collected)
	for ref := range collected {
		if !strings.HasPrefix(ref, "#/components/schemas/") {
			t.Fatalf("document references %q outside components.schemas", ref)
		}
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		if _, ok := schemas[name]; !ok {
			t.Fatalf("document references missing component schema %q", name)
		}
	}
}

func TestOpenAPIEnvelopeConventions(t *testing.T) {
	reg := mustRegistry(t)
	doc := parseOpenAPI(t, reg)

	assertEnvelope := func(opID string, mutation bool) {
		t.Helper()
		item := doc.Paths["/v1/operations/"+opID].(map[string]any)
		post := item["post"].(map[string]any)

		body := post["requestBody"].(map[string]any)
		if body["required"] != true {
			t.Fatalf("%s: request body is not required", opID)
		}
		requestSchema := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		if got := requestSchema["properties"].(map[string]any)["schema"].(map[string]any)["const"]; got != contract.SchemaRequest {
			t.Fatalf("%s: request schema const %v", opID, got)
		}
		required := requestSchema["required"].([]any)
		hasKey := slices.Contains(required, any("submission_key"))
		if mutation && !hasKey {
			t.Fatalf("%s: mutation does not require a submission key", opID)
		}
		if !mutation && hasKey {
			t.Fatalf("%s: query requires a submission key", opID)
		}
		if mutation {
			keySchema := requestSchema["properties"].(map[string]any)["submission_key"].(map[string]any)
			if keySchema["maxLength"] != float64(128) {
				t.Fatalf("%s: submission key maxLength %v", opID, keySchema["maxLength"])
			}
		}

		responses := post["responses"].(map[string]any)
		if _, ok := responses["200"]; !ok {
			t.Fatalf("%s: no completed response", opID)
		}
		_, hasAccepted := responses["202"]
		if mutation && !hasAccepted {
			t.Fatalf("%s: mutation has no accepted response", opID)
		}
		if !mutation && hasAccepted {
			t.Fatalf("%s: query declares an accepted response", opID)
		}
		failed, ok := responses["default"].(map[string]any)
		if !ok {
			t.Fatalf("%s: no default failed response", opID)
		}
		failedSchema := failed["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		props := failedSchema["properties"].(map[string]any)
		if got := props["schema"].(map[string]any)["const"]; got != contract.SchemaResult {
			t.Fatalf("%s: result schema const %v", opID, got)
		}
		if got := props["status"].(map[string]any)["const"]; got != contract.StatusFailed {
			t.Fatalf("%s: failed status const %v", opID, got)
		}
		if got := props["data"].(map[string]any); got["type"] != "null" {
			t.Fatalf("%s: failed response carries data", opID)
		}
		if _, ok := props["error"]; !ok {
			t.Fatalf("%s: failed response carries no error mapping", opID)
		}
		if _, ok := props["next_cursor"]; !ok {
			t.Fatalf("%s: result envelope has no cursor position", opID)
		}
	}
	assertEnvelope("artifact.get", false)   // query, local
	assertEnvelope("artifact.export", true) // mutation with submission key
	assertEnvelope("task.create", true)     // mutation with async work
}

func TestOpenAPINamesThePinnedRevision(t *testing.T) {
	reg := mustRegistry(t)
	doc := parseOpenAPI(t, reg)
	info, _ := doc.Document["info"].(map[string]any)
	if info["version"] != "1.1.0" {
		t.Fatalf("document version %v, want 1.1.0 for frozen revision 1", info["version"])
	}
}

func TestOpenAPISharedSchemasAreAllPresent(t *testing.T) {
	reg := mustRegistry(t)
	doc := parseOpenAPI(t, reg)
	schemas := doc.Components["schemas"].(map[string]any)
	for name := range mustCatalog(t).sharedDefs {
		if _, ok := schemas[name]; !ok {
			t.Fatalf("shared schema %q missing from components", name)
		}
	}
	if _, ok := schemas["Fault"]; !ok {
		t.Fatal("the Fault schema is missing from components")
	}
}

func mustCatalog(t *testing.T) *catalog {
	t.Helper()
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("catalog does not load: %v", err)
	}
	return cat
}
