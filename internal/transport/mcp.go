// internal/transport/mcp.go
//
// T1.7: MCP stdio adapter. One JSON-RPC 2.0 message per stdio frame
// (newline-delimited), per the MCP stdio convention. Tool listings are
// derived from the operations registry; there is no second declaration
// of tool names or input shapes. Methods that would expose unimplemented
// behavior return JSON-RPC errors, never stub results.

package transport

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"github.com/zatiti/zatiti/internal/operations"
)

// MCPServer serves the MCP protocol over one stdin/stdout pair.
// MCPServer (revised struct): authenticator, sessionLookup, and
// executor are injected by the app root — transport owns the wire,
// roots own the meaning.
type MCPServer struct {
	reg           *operations.Registry
	in            io.Reader
	out           io.Writer
	protocol      string // negotiated protocol version offered to clients
	authenticator func(principal string, nonce, sig []byte) (sessionID string, err error)
	sessionLookup func(ctx context.Context, sessionID string) (principalID string, err error)
	executor      func(ctx context.Context, principalID, op string, args map[string]any) (string, error)
}

// SetAuthenticator / SetSessionLookup / SetExecutor wire the roots.
// Nil injection leaves the corresponding path refused (fail-closed).
func (s *MCPServer) SetAuthenticator(f func(string, []byte, []byte) (string, error)) {
	s.authenticator = f
}

func (s *MCPServer) SetSessionLookup(f func(context.Context, string) (string, error)) {
	s.sessionLookup = f
}

func (s *MCPServer) SetExecutor(f func(context.Context, string, string, map[string]any) (string, error)) {
	s.executor = f
}

// NewMCPServer builds a server over in/out using the frozen registry.
func NewMCPServer(reg *operations.Registry, in io.Reader, out io.Writer) *MCPServer {
	return &MCPServer{reg: reg, in: in, out: out, protocol: "2024-11-05"}
}

// jsonrpcReq is an inbound JSON-RPC 2.0 request.
type jsonrpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResp is an outbound response.
type jsonrpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC error codes used here.
const (
	errParse     = -32700
	errMethodNF  = -32601
	errInvalidRe = -32602
	errInternal  = -32603
)

// Serve reads requests until stdin closes or the context is canceled.
func (s *MCPServer) Serve(ctx context.Context) error {
	sc := bufio.NewScanner(s.in)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !sc.Scan() {
			return sc.Err() // nil on clean EOF
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		resp := s.handle(ctx, line)
		if resp == nil {
			continue // notification: no response
		}
		b, err := json.Marshal(resp)
		if err != nil {
			b = mustErrorResp(resp.ID, errInternal, "encode failure")
			b, _ = json.Marshal(b)
		}
		if _, err := fmt.Fprintf(s.out, "%s\n", b); err != nil {
			return fmt.Errorf("mcp: write response: %w", err)
		}
	}
}

// handle routes one request line to a response.
func (s *MCPServer) handle(ctx context.Context, line []byte) *jsonrpcResp {
	var req jsonrpcReq
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResp(nil, errParse, "parse error")
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return errorResp(req.ID, errInvalidRe, "invalid request")
	}

	switch req.Method {
	case "initialize":
		return okResp(req.ID, map[string]any{
			"protocolVersion": s.protocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "zatiti", "version": "0.0.0-scaffold"},
		})
	case "notifications/initialized":
		return nil // notification, no response
	case "tools/list":
		return okResp(req.ID, map[string]any{"tools": s.tools()})
	case "session/authenticate":
		return s.authenticate(req.ID, req.Params)
	case "tools/call":
		return s.callTool(ctx, req.ID, req.Params)
	default:
		return errorResp(req.ID, errMethodNF, fmt.Sprintf("method %q not found", req.Method))
	}
}

// toolDesc is the MCP tool description derived from an Op.
type toolDesc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// tools derives MCP tool descriptors from the registry. Only read-only
// operations are advertised until the write path lands (T3.x) — an
// tool the server refuses to execute is worse than absence.
func (s *MCPServer) tools() []toolDesc {
	var out []toolDesc
	for _, name := range s.reg.Names() {
		op, ok := s.reg.Lookup(name)
		if !ok || op.Mutability != operations.ReadOnly {
			continue
		}
		props := map[string]any{}
		var reqFields []string
		for _, f := range op.Fields {
			props[f.Name] = map[string]any{
				"type":        jsonType(f.Type),
				"description": f.Help,
			}
			if f.Required {
				reqFields = append(reqFields, f.Name)
			}
		}
		schema := map[string]any{
			"type":       "object",
			"properties": props,
		}
		if len(reqFields) > 0 {
			schema["required"] = reqFields
		}
		out = append(out, toolDesc{
			Name:        op.Name,
			Description: op.Summary,
			InputSchema: schema,
		})
	}
	return out
}

// authenticate binds a session to a principal via signed challenge.
// Params: {principal: string, nonce: base64, signature: base64}.
// The scaffold uses this extension method instead of MCP's OAuth flow;
// the deviation is documented in the RFC-facing notes.
func (s *MCPServer) authenticate(id json.RawMessage, params json.RawMessage) *jsonrpcResp {
	var p struct {
		Principal string `json:"principal"`
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Principal == "" ||
		p.Nonce == "" || p.Signature == "" {
		return errorResp(id, errInvalidRe, "authenticate requires principal, nonce, signature")
	}
	nonce, err := base64.StdEncoding.DecodeString(p.Nonce)
	if err != nil {
		return errorResp(id, errInvalidRe, "nonce is not base64")
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil {
		return errorResp(id, errInvalidRe, "signature is not base64")
	}
	if s.authenticator == nil {
		return errorResp(id, errInternal, "no authenticator wired")
	}
	result, err := s.authenticator(p.Principal, nonce, sig)
	if err != nil {
		return errorResp(id, errInvalidRe, "authentication failed")
	}
	return okResp(id, map[string]any{"session": result})
}

// callTool (final form, superseding the refusal stub): resolves the
// caller's session to a principal, then executes through the shared
// operation pipeline — the same authorization, audit, and exit-code
// semantics as the CLI, which is the point.
func (s *MCPServer) callTool(ctx context.Context, id json.RawMessage, params json.RawMessage) *jsonrpcResp {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
		Session   string         `json:"session"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return errorResp(id, errInvalidRe, "tools/call requires a tool name")
	}
	if p.Session == "" || s.sessionLookup == nil {
		return errorResp(id, errInvalidRe, "tools/call requires an authenticated session")
	}
	principalID, err := s.sessionLookup(ctx, p.Session)
	if err != nil {
		return errorResp(id, errInvalidRe, "session is not valid")
	}
	if s.executor == nil {
		return errorResp(id, errInternal, "no executor wired")
	}
	res, err := s.executor(ctx, principalID, p.Name, p.Arguments)
	if err != nil {
		return errorResp(id, errInternal, err.Error())
	}
	return okResp(id, map[string]any{
		"content": []map[string]any{{
			"type": "text", "text": res,
		}},
	})
}

func jsonType(t operations.FieldType) string {
	switch t {
	case operations.TUint:
		return "integer"
	case operations.TBool:
		return "boolean"
	default:
		return "string"
	}
}

func okResp(id json.RawMessage, result any) *jsonrpcResp {
	return &jsonrpcResp{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResp(id json.RawMessage, code int, msg string) *jsonrpcResp {
	return &jsonrpcResp{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: code, Message: msg}}
}

func mustErrorResp(id json.RawMessage, code int, msg string) []byte {
	b, _ := json.Marshal(errorResp(id, code, msg))
	return b
}

// ToolDescAlias exposes the tool descriptor shape for tests and future
// embedding; the wire format is identical.
type ToolDescAlias = toolDesc
