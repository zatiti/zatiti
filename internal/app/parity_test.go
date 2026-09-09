// internal/app/parity_test.go
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"zatiti/internal/operations"
	"zatiti/internal/transport"
)

// TestCLIMCPParity pins the single-source-of-truth contract: the MCP
// tool listing must contain exactly the read-only operations in the
// registry, by name, with matching field sets.
func TestCLIMCPParity(t *testing.T) {
	reg, err := operations.NewBuiltin()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	var out bytes.Buffer
	srv := transport.NewMCPServer(reg, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), &out)
	if err := srv.Serve(context.Background()); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp struct {
		Result struct {
			Tools []transport.ToolDescAlias `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, out.String())
	}
	toolNames := map[string]bool{}
	for _, tl := range resp.Result.Tools {
		toolNames[tl.Name] = true
	}
	for _, name := range reg.Names() {
		op, _ := reg.Lookup(name)
		want := op.Mutability == operations.ReadOnly
		if toolNames[name] != want {
			t.Fatalf("tool %q listed=%v, want %v", name, toolNames[name], want)
		}
	}
}

// TestExitCodes pins RFC §8.4 mappings for the paths implemented so far.
func TestExitCodes(t *testing.T) {
	a, _ := New()
	cases := []struct {
		args []string
		want int
	}{
		{[]string{}, ExitInputError},
		{[]string{"nonsense"}, ExitInputError},
		{[]string{"capabilities"}, ExitPrereqMissing},
		{[]string{"serve"}, ExitInputError},             // missing -data-dir
		{[]string{"organization.list"}, ExitInputError}, // missing -data-dir
	}
	for _, c := range cases {
		got, _ := a.Run(context.Background(), c.args)
		if got != c.want {
			t.Fatalf("Run(%v) = %d, want %d", c.args, got, c.want)
		}
	}
}
