package github

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// The embedded schemas must be the frozen catalog's, not a paraphrase. The
// committed assignment carries that catalog on one line; every definition
// this package embeds is compared against it in canonical form.
func TestEmbeddedSchemasMatchTheFrozenCatalog(t *testing.T) {
	t.Parallel()
	spec, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("read assignment: %v", err)
	}
	var catalogLine string
	for _, line := range strings.Split(string(spec), "\n") {
		if strings.HasPrefix(line, `{"$defs":`) {
			catalogLine = line
			break
		}
	}
	if catalogLine == "" {
		t.Fatal("frozen catalog line not found in AGENTS.md")
	}
	var catalog struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(catalogLine), &catalog); err != nil {
		t.Fatalf("decode frozen catalog: %v", err)
	}
	var embedded struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(schemaDefs), &embedded); err != nil {
		t.Fatalf("decode embedded catalog: %v", err)
	}
	canon := func(raw json.RawMessage) []byte {
		out, err := contract.Canonicalize(raw)
		if err != nil {
			t.Fatalf("canonicalize: %v", err)
		}
		return out
	}
	if len(embedded.Defs) == 0 {
		t.Fatal("embedded catalog is empty")
	}
	for name, got := range embedded.Defs {
		want, ok := catalog.Defs[name]
		if !ok {
			t.Fatalf("embedded definition %s is not in the frozen catalog", name)
		}
		if !bytes.Equal(canon(got), canon(want)) {
			t.Fatalf("embedded definition %s differs from the frozen catalog", name)
		}
	}
	for name, body := range map[string]string{
		"GitHubProfile": schemaProfileBody, "GitHubParameters": schemaParametersBody, "GitHubEvidence": schemaEvidenceBody,
	} {
		if !bytes.Equal(canon(json.RawMessage(body)), canon(catalog.Defs[name])) {
			t.Fatalf("embedded %s schema differs from the frozen catalog", name)
		}
	}
}
