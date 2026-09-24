package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// controlledServer is the in-process streamable-HTTP MCP server this
// package's tests exercise the adapter against. Every physical request is
// logged from the SERVER's own vantage point (never the adapter's internal
// counters), keyed by JSON-RPC method, so assertions on exact request
// counts are independent of anything the adapter under test claims about
// itself. Fault injection (dropping a session) happens in a thin wrapper
// in front of the real go-sdk server handler; every other fault (a slow
// tool, an oversize tool result, a tool that issues a server-initiated
// request) is a genuine tool handler on a real mcp.Server, so the wire
// traffic is real MCP protocol, not a simulation of it.
type controlledServer struct {
	httpSrv *httptest.Server

	mu             sync.Mutex
	requestLog     []string // JSON-RPC method per physical request, in arrival order (server's own log)
	dropSessions   map[string]bool
	rejectDiscover bool
}

// newControlledServer starts a stateful streamable-HTTP MCP server with a
// fixed tool catalog covering every fault this package's tests need:
//   - "echo": returns its single "text" argument unchanged.
//   - "stall": blocks until the request's context is done, then returns an
//     error; simulates a lost/timed-out response.
//   - "big": returns a large text content, for exceeding a small
//     max_response_bytes bound.
//   - "server-requests": issues sampling/createMessage, elicitation/create,
//     roots/list and ping back to the client before returning a normal
//     result, to prove each is refused and the tool result is still
//     recorded honestly.
//
// newControlledServer starts a server that rejects the SEP-2575
// server/discover RPC, matching a legacy MCP server (like Postiz, the
// first qualified server per docs/implementation/contracts.md) that only
// understands the 2025-11-25 initialize handshake -- the scenario the
// frozen contract's protocol_version const "2025-11-25" and its two-message
// MCPHandshakeExchange schema were written against. See
// newSEP2575AwareControlledServer for the contrasting case.
func newControlledServer() *controlledServer {
	return buildControlledServer(false, true)
}

// newMCPToolServer builds the shared tool catalog both controlled-server
// variants (stateful/legacy and stateless/SEP-2575-aware) serve.
func newMCPToolServer() *mcp.Server {
	impl := &mcp.Implementation{Name: "controlled-test-server", Version: "1.0.0"}
	server := mcp.NewServer(impl, nil)

	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes its text argument"},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Text string `json:"text"`
		}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, struct{}{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "stall", Description: "blocks until the caller gives up"},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, struct{}, error) {
			<-ctx.Done()
			return nil, struct{}{}, ctx.Err()
		})

	mcp.AddTool(server, &mcp.Tool{Name: "big", Description: "returns an oversize text content"},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Size int `json:"size"`
		}) (*mcp.CallToolResult, struct{}, error) {
			n := in.Size
			if n <= 0 {
				n = 1 << 20
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", n)}}}, struct{}{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "server-requests", Description: "issues refused server-to-client requests, then succeeds"},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, struct{}, error) {
			_, _ = req.Session.CreateMessage(ctx, &mcp.CreateMessageParams{
				Messages:  []*mcp.SamplingMessage{{Role: "user", Content: &mcp.TextContent{Text: "hi"}}},
				MaxTokens: 100,
			})
			_, _ = req.Session.Elicit(ctx, &mcp.ElicitParams{Message: "confirm?"})
			_, _ = req.Session.ListRoots(ctx, &mcp.ListRootsParams{})
			_ = req.Session.Ping(ctx, &mcp.PingParams{})
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, struct{}{}, nil
		})

	return server
}

// buildControlledServer wires newMCPToolServer's catalog behind a
// streamable-HTTP handler and the request-logging/fault-injection wrapper.
// stateless controls StreamableHTTPOptions.Stateless: per the go-sdk's own
// documented behavior (streamable.go, StreamableServerTransport.
// SupportsProtocolVersion), the SEP-2575 >= 2026-07-28 protocol -- and
// therefore a same-request server/discover success -- is only ever offered
// by a transport configured stateless; a stateful transport (the common
// case for a real long-lived MCP server) always declines 2026-07-28 in its
// discover response and forces the legacy fallback, regardless of whether
// the server's go-sdk build otherwise implements server/discover.
// rejectDiscover, independent of statelessness, simulates a legacy server
// that does not implement server/discover at all (like Postiz).
func buildControlledServer(stateless, rejectDiscover bool) *controlledServer {
	cs := &controlledServer{dropSessions: map[string]bool{}, rejectDiscover: rejectDiscover}
	server := newMCPToolServer()

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		DisableLocalhostProtection: true,
		Stateless:                  stateless,
	})

	cs.httpSrv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.Header.Get("Mcp-Session-Id")
		cs.mu.Lock()
		dropped := sessionID != "" && cs.dropSessions[sessionID]
		cs.mu.Unlock()
		if dropped {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}

		var bodyBytes []byte
		if r.Body != nil {
			bodyBytes, _ = io.ReadAll(r.Body)
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
		method := peekMethod(bodyBytes, r.Method)
		cs.mu.Lock()
		cs.requestLog = append(cs.requestLog, method)
		reject := cs.rejectDiscover
		cs.mu.Unlock()

		if reject && method == "server/discover" {
			var peek struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(bodyBytes, &peek)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"server/discover is not supported"}}`, orNull(peek.ID))
			return
		}

		handler.ServeHTTP(w, r)
	}))
	return cs
}

func orNull(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}

// newSEP2575AwareControlledServer starts a stateless controlled server that
// answers server/discover successfully, like the same go-sdk build used for
// this adapter would if it served MCP instead of consuming it. This is the
// second, sharper edge of the open_session contract-defect finding: this
// adapter's request count and negotiated protocol version depend entirely
// on the remote server's own SEP-2575 support AND statelessness (see
// buildControlledServer's doc comment), not on anything this adapter
// controls.
func newSEP2575AwareControlledServer() *controlledServer {
	return buildControlledServer(true, false)
}

func peekMethod(body []byte, httpMethod string) string {
	if httpMethod == http.MethodDelete {
		return "DELETE"
	}
	if len(body) == 0 {
		return httpMethod
	}
	var peek struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &peek); err != nil || peek.Method == "" {
		return fmt.Sprintf("%s(unparsed)", httpMethod)
	}
	return peek.Method
}

// endpoint is the server's URL, for a profile's transport.endpoint.
func (cs *controlledServer) endpoint() string { return cs.httpSrv.URL }

// close shuts the server down.
func (cs *controlledServer) close() { cs.httpSrv.Close() }

// counts returns how many physical requests the server itself logged, by
// JSON-RPC method (or DELETE for a session-termination request).
func (cs *controlledServer) counts() map[string]int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := map[string]int{}
	for _, m := range cs.requestLog {
		out[m]++
	}
	return out
}

// total returns the total number of physical requests the server logged.
func (cs *controlledServer) total() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.requestLog)
}

// dropSession makes every subsequent request carrying sessionID (the raw
// SDK Mcp-Session-Id, not this adapter's opaque handle) receive 404,
// simulating a server that has forgotten the session (restart/expiry).
func (cs *controlledServer) dropSession(sessionID string) {
	cs.mu.Lock()
	cs.dropSessions[sessionID] = true
	cs.mu.Unlock()
}

// alwaysRedirectServer is a bare HTTP server that answers every request
// with a 302 redirect, for testing max_redirects=0 refusal in isolation:
// no real MCP handshake is needed since the very first physical request
// (initialize) is what gets redirected.
func alwaysRedirectServer(location string) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, location, http.StatusFound)
	}))
}
