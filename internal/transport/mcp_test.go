// internal/transport/mcp_test.go
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/operations"
)

func newTestServer(t *testing.T) (*MCPServer, *bytes.Buffer) {
	t.Helper()
	reg, err := operations.NewBuiltin()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	var out bytes.Buffer
	return NewMCPServer(reg, strings.NewReader(""), &out), &out
}

func TestToolsListDerivedFromRegistry(t *testing.T) {
	s, out := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	s.in = strings.NewReader(req)
	if err := s.Serve(context.Background()); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp struct {
		Result struct {
			Tools []toolDesc `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, out.String())
	}
	// organization.list is the only implemented read-only op.
	if len(resp.Result.Tools) != 1 || resp.Result.Tools[0].Name != "organization.list" {
		t.Fatalf("tools = %+v, want only organization.list", resp.Result.Tools)
	}
}

func TestCallToolRefusedUntilIdentityLands(t *testing.T) {
	s, out := newTestServer(t)
	s.in = strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"organization.list","arguments":{}}}`)
	if err := s.Serve(context.Background()); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp jsonrpcResp
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("want error refusing execution, got result")
	}
}

func TestUnknownMethodIsMethodNotFound(t *testing.T) {
	s, out := newTestServer(t)
	s.in = strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"no/such"}`)
	_ = s.Serve(context.Background())
	if !strings.Contains(out.String(), "-32601") {
		t.Fatalf("want method-not-found, got %s", out.String())
	}
}

func TestInitializeHandshake(t *testing.T) {
	s, out := newTestServer(t)
	s.in = strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{}}`)
	_ = s.Serve(context.Background())
	if !strings.Contains(out.String(), "protocolVersion") {
		t.Fatalf("want initialize result, got %s", out.String())
	}
}
