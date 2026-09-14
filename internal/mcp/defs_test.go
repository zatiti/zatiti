package mcp

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The embedded defs.json must never drift from the frozen implementation
// contract in AGENTS.md: this file re-parses the contract document's "Local
// schema definitions" block with the same shared $defs object every
// operation's "#/$defs/Name" reference resolves against, and asserts the
// committed defs.json is exactly that surface. Mirrors
// internal/registry/catalog_test.go's own drift check against the same
// contract text.
//
//go:embed AGENTS.md
var agentsMD []byte

func TestEmbeddedDefsMatchContract(t *testing.T) {
	lines := strings.Split(string(agentsMD), "\n")

	heading := -1
	for i, line := range lines {
		if line == "### Local schema definitions" {
			heading = i
			break
		}
	}
	if heading < 0 {
		t.Fatal("AGENTS.md: no \"### Local schema definitions\" heading found")
	}

	fence := -1
	for i := heading + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "```json") {
			fence = i
			break
		}
		if strings.HasPrefix(lines[i], "### ") {
			break
		}
	}
	if fence < 0 {
		t.Fatal("AGENTS.md: local schema definitions block has no json fence")
	}
	var block []string
	for i := fence + 1; i < len(lines) && !strings.HasPrefix(lines[i], "```"); i++ {
		block = append(block, lines[i])
	}
	if len(block) == 0 {
		t.Fatal("AGENTS.md: local schema definitions fence is empty")
	}
	raw := strings.Join(block, "\n")

	var contractWrapper map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&contractWrapper); err != nil {
		t.Fatalf("AGENTS.md: local schema definitions block is not valid JSON: %v", err)
	}
	contractDefs, ok := contractWrapper["$defs"].(map[string]any)
	if !ok {
		t.Fatal("AGENTS.md: local schema definitions block does not hold a $defs object")
	}

	var embeddedWrapper map[string]any
	dec = json.NewDecoder(strings.NewReader(string(defsFile)))
	dec.UseNumber()
	if err := dec.Decode(&embeddedWrapper); err != nil {
		t.Fatalf("defs.json: not valid JSON: %v", err)
	}
	embeddedDefs, ok := embeddedWrapper["$defs"].(map[string]any)
	if !ok {
		t.Fatal("defs.json: does not hold a $defs object")
	}

	if !reflect.DeepEqual(contractDefs, embeddedDefs) {
		t.Fatal("internal/mcp/defs.json has drifted from the \"Local schema definitions\" block in AGENTS.md")
	}
}

func TestLoadSharedDefsResolvesFault(t *testing.T) {
	defs, err := loadSharedDefs()
	if err != nil {
		t.Fatalf("loadSharedDefs() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(defs, &decoded); err != nil {
		t.Fatalf("loadSharedDefs() result is not valid JSON: %v", err)
	}
	if _, ok := decoded["Fault"]; !ok {
		t.Fatal(`loadSharedDefs() result has no "Fault" definition, which wrapOutputSchema references`)
	}
	if _, ok := decoded["Scope"]; !ok {
		t.Fatal(`loadSharedDefs() result has no "Scope" definition`)
	}
}
