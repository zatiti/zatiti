package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zatiti/zatiti/internal/cli"
)

// TestEveryLandedOperationIsExposedInBothInterfaces (Z02 "every registry
// operation exposed in both"; R8.1-004): the real MCP server lists exactly
// one tool per landed public operation under its descriptor name, with the
// submission key required exactly where the descriptor requires it, and the
// real CLI tree carries one runnable leaf per operation at its descriptor
// path. No controller is contacted: exposure is derived from descriptors.
func TestEveryLandedOperationIsExposedInBothInterfaces(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	descs := f.catalog.Public()
	// An operator that is never called: the socket does not exist.
	op := newClient(t, filepath.Join(t.TempDir(), "absent.sock"), nil)

	mt := newMCPTransport(t, op, descs)
	listed, err := mt.session.ListTools(context.Background(), &gosdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	tools := map[string]*gosdk.Tool{}
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	if len(tools) != len(descs) {
		t.Errorf("mcp lists %d tools for %d public operations", len(tools), len(descs))
	}

	root, err := cli.New(op, descs, cli.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard})
	if err != nil {
		t.Fatalf("cli.New: %v", err)
	}
	for _, d := range descs {
		tool, ok := tools[d.MCP]
		if !ok {
			t.Errorf("operation %s has no MCP tool %q", d.ID, d.MCP)
			continue
		}
		if want := "zatiti_" + strings.ReplaceAll(d.ID, ".", "_"); d.MCP != want && d.ID != "capabilities.list" {
			t.Errorf("operation %s maps to MCP tool %q, want %q", d.ID, d.MCP, want)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		decode(t, mustJSON(tool.InputSchema), &schema)
		_, declares := schema.Properties["submission_key"]
		requires := false
		for _, r := range schema.Required {
			requires = requires || r == "submission_key"
		}
		if declares != d.SubmissionKey || requires != d.SubmissionKey {
			t.Errorf("operation %s: MCP tool input declares submission_key=%t required=%t, descriptor requires %t", d.ID, declares, requires, d.SubmissionKey)
		}

		cmd, rest, err := root.Find(d.CLI)
		if err != nil || len(rest) != 0 || cmd == root || !cmd.Runnable() {
			t.Errorf("operation %s has no runnable CLI command at %v (remaining %v, err %v)", d.ID, d.CLI, rest, err)
			continue
		}
		if hasKey := cmd.Flags().Lookup("submission-key") != nil; hasKey != d.SubmissionKey {
			t.Errorf("operation %s: CLI --submission-key present=%t, descriptor requires %t", d.ID, hasKey, d.SubmissionKey)
		}
	}
}
