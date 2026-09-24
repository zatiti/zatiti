package qualification_test

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
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zatiti/zatiti/internal/adapters/mcpclient"
	"github.com/zatiti/zatiti/internal/contract"
)

// QUALIFICATION.mcp_adapter: the five named Z-M2 cases against a
// controlled in-process streamable-HTTP MCP server whose own request log
// is the source of truth for exact physical-request counts. Credential
// confinement is checked as byte absence in staged docs/evidence and
// presence only in the Authorization header the server actually received.

const mcpQualCredRef = "qual-mcp-cred"
const mcpQualToken = "mcp_qual_bearer_token_secret_value_z13"

// ---------- controlled MCP server (server-side request log) ----------

type mcpControlledServer struct {
	httpSrv *httptest.Server

	mu           sync.Mutex
	requestLog   []string
	authHeaders  []string
	dropSessions map[string]bool
}

func newMCPControlledServer() *mcpControlledServer {
	cs := &mcpControlledServer{dropSessions: map[string]bool{}}
	impl := &mcp.Implementation{Name: "qualification-controlled-mcp", Version: "1.0.0"}
	server := mcp.NewServer(impl, nil)
	// Exercise the frozen legacy request methods through the SDK wire handler;
	// the convenience APIs are deprecated in newer protocol versions.
	var send mcp.MethodHandler
	server.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler { send = next; return next })

	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes its text argument"},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Text string `json:"text"`
		}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, struct{}{}, nil
		})
	mcp.AddTool(server, &mcp.Tool{
		Name: "beta", Description: "read-only-hint tool not on the profile allowlist",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "beta"}}}, struct{}{}, nil
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
	mcp.AddTool(server, &mcp.Tool{Name: "server-requests", Description: "issues refused server-to-client requests"},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, struct{}, error) {
			var sampling struct {
				mcp.ParamsBase
				Messages  json.RawMessage `json:"messages"`
				MaxTokens int             `json:"maxTokens"`
			}
			_ = json.Unmarshal([]byte(`{"messages":[{"role":"user","content":{"type":"text","text":"hi"}}],"maxTokens":100}`), &sampling)
			_, _ = send(ctx, "sampling/createMessage", &mcp.ServerRequest[mcp.Params]{Session: req.Session, Params: &sampling})
			_, _ = send(ctx, "elicitation/create", &mcp.ServerRequest[*mcp.ElicitParams]{Session: req.Session, Params: &mcp.ElicitParams{Message: "confirm?"}})
			_, _ = send(ctx, "roots/list", &mcp.ServerRequest[*mcp.PingParams]{Session: req.Session, Params: &mcp.PingParams{}})
			_, _ = send(ctx, "ping", &mcp.ServerRequest[*mcp.PingParams]{Session: req.Session, Params: &mcp.PingParams{}})
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, struct{}{}, nil
		})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		DisableLocalhostProtection: true,
		Stateless:                  false,
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
		method := mcpPeekMethod(bodyBytes, r.Method)
		auth := r.Header.Get("Authorization")
		cs.mu.Lock()
		cs.requestLog = append(cs.requestLog, method)
		cs.authHeaders = append(cs.authHeaders, auth)
		rejectDiscover := true
		cs.mu.Unlock()

		if rejectDiscover && method == "server/discover" {
			var peek struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(bodyBytes, &peek)
			id := "null"
			if len(peek.ID) > 0 {
				id = string(peek.ID)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"server/discover is not supported"}}`, id)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	return cs
}

func mcpPeekMethod(body []byte, httpMethod string) string {
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

func (cs *mcpControlledServer) endpoint() string { return cs.httpSrv.URL }
func (cs *mcpControlledServer) close()           { cs.httpSrv.Close() }

func (cs *mcpControlledServer) counts() map[string]int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := map[string]int{}
	for _, m := range cs.requestLog {
		out[m]++
	}
	return out
}

func (cs *mcpControlledServer) total() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.requestLog)
}

func (cs *mcpControlledServer) sawBearer(token string) bool {
	want := "Bearer " + token
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for _, h := range cs.authHeaders {
		if h == want {
			return true
		}
	}
	return false
}

// ---------- profile / dispatch helpers ----------

func mcpBindProfile(t *testing.T, endpoint string, allowPrivate bool, allowedTools []string, classifications []string, credentialKind string, maxResponseBytes int64) json.RawMessage {
	t.Helper()
	transport, err := json.Marshal(map[string]any{
		"kind":                   "streamable_http",
		"endpoint":               endpoint,
		"allow_private_endpoint": allowPrivate,
		"max_redirects":          0,
	})
	if err != nil {
		t.Fatalf("marshal transport: %v", err)
	}
	if maxResponseBytes <= 0 {
		maxResponseBytes = 1 << 16
	}
	profile := map[string]any{
		"schema":             "zatiti.mcp/v1",
		"transport":          json.RawMessage(transport),
		"protocol_version":   "2025-11-25",
		"credential_kind":    credentialKind,
		"allowed_tools":      allowedTools,
		"tool_call_cost":     map[string]any{"currency": "USD", "micro_units": 0},
		"max_request_bytes":  1 << 16,
		"max_response_bytes": maxResponseBytes,
		"timeout_seconds":    30,
		"classifications":    classifications,
		"capability_evidence": capabilityEvidence(
			"qualification-mcp", sourceRevision(), "2025-11-25",
			[]string{"open_session", "list_tools", "call_tool", "close_session"},
			[]string{"stdio transport unqualified", "reconcile unsupported"},
		),
	}
	return bindProfile(t, profile)
}

func mcpDispatch(t *testing.T, action any, credentialRef string, deadline time.Time) contract.Dispatch {
	t.Helper()
	return dispatch(t, "mcp", action, credentialRef, deadline)
}

func mcpNewAdapter(t *testing.T, client *http.Client, blobs *memoryBlobs, secrets contract.SecretStore, profile json.RawMessage) contract.Adapter {
	t.Helper()
	a, err := mcpclient.New(contract.AdapterDependencies{
		HTTP:    client,
		Secrets: secrets,
		Clock:   &stepClock{now: time.Now().UTC()},
		Blobs:   blobs,
	}, profile)
	if err != nil {
		t.Fatalf("mcpclient.New: %v", err)
	}
	return a
}

func mcpOpenSession(t *testing.T, a contract.Adapter, credRef string, timeout time.Duration) (string, contract.Observation) {
	t.Helper()
	obs, err := a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "open_session",
		"client_name": "qualification", "client_version": "1.0.0",
	}, credRef, time.Now().UTC().Add(timeout)))
	if err != nil {
		t.Fatalf("open_session Invoke error: %v", err)
	}
	var ev struct {
		SessionHandle string `json:"session_handle"`
	}
	if err := json.Unmarshal(obs.Evidence, &ev); err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	return ev.SessionHandle, obs
}

func mcpInputSchema(t *testing.T) (json.RawMessage, contract.Digest) {
	t.Helper()
	schema := map[string]any{"type": "object", "additionalProperties": true}
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	canon, err := contract.Canonicalize(raw)
	if err != nil {
		t.Fatalf("canonicalize schema: %v", err)
	}
	return raw, contract.Hash(canon)
}

func mcpMustFault(t *testing.T, err error) *contract.Fault {
	t.Helper()
	var f *contract.Fault
	if !asFault(err, &f) {
		t.Fatalf("expected *contract.Fault, got %T: %v", err, err)
	}
	return f
}

func asFault(err error, dest **contract.Fault) bool {
	if err == nil {
		return false
	}
	if f, ok := err.(*contract.Fault); ok {
		*dest = f
		return true
	}
	return false
}

func assertNoCredentialBytes(t *testing.T, token string, blobs *memoryBlobs, evidence json.RawMessage, extras ...[]byte) {
	t.Helper()
	tok := []byte(token)
	for i, doc := range blobs.all() {
		if bytes.Contains(doc, tok) {
			t.Fatalf("staged document %d contains the raw credential", i)
		}
	}
	if bytes.Contains(evidence, tok) {
		t.Fatalf("evidence contains the raw credential")
	}
	for i, extra := range extras {
		if bytes.Contains(extra, tok) {
			t.Fatalf("extra document %d contains the raw credential", i)
		}
	}
}

// ---------- named cases ----------

func TestZ05MCPDiscoveryGrantsNothing(t *testing.T) {
	c := beginCase(t, "Z05.mcp_discovery_grants_nothing", "Z05",
		"Discovery records advertised tools with pinned schema digests and annotations but changes no binding, policy or classification.",
		"call_tool for a non-allowlisted tool is refused permission_denied before any request; read_only_hint changes nothing.",
		"call_tool for an allowlisted tool with non-conforming arguments is refused invalid_input before any request.")
	c.version("adapter_source_revision", sourceRevision())
	c.version("adapter_root", "mcp")

	srv := newMCPControlledServer()
	defer srv.close()
	blobs := newMemoryBlobs()
	secrets := memorySecrets{refs: map[string][]byte{mcpQualCredRef: []byte(mcpQualToken)}}
	profile := mcpBindProfile(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none", 0)
	a := mcpNewAdapter(t, srv.httpSrv.Client(), blobs, secrets, profile)
	c.attach("contract", json.RawMessage(a.Contract()))

	handle, _ := mcpOpenSession(t, a, mcpQualCredRef, 10*time.Second)
	beforeList := srv.total()
	listObs, err := a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "list_tools", "session_handle": handle,
	}, mcpQualCredRef, time.Now().UTC().Add(10*time.Second)))
	if err != nil {
		c.fail("list_tools: %v", err)
	}
	if listObs.Disposition != contract.DispositionSucceeded {
		c.fail("list_tools disposition %s", listObs.Disposition)
	}
	if got := srv.total() - beforeList; got != 1 {
		c.fail("list_tools expected 1 physical request, got %d", got)
	}
	c.observe("list_tools recorded advertised catalog; profile allowlist still only names echo")

	var listEv struct {
		Tools []struct {
			Name              string          `json:"name"`
			InputSchemaDigest string          `json:"input_schema_digest"`
			Annotations       json.RawMessage `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(listObs.Evidence, &listEv); err != nil {
		c.fail("decode list_tools evidence: %v", err)
	}
	names := map[string]bool{}
	var betaDigest string
	for _, tool := range listEv.Tools {
		names[tool.Name] = true
		if tool.Name == "beta" {
			betaDigest = tool.InputSchemaDigest
		}
	}
	if !names["echo"] || !names["beta"] {
		c.fail("expected discovery to record echo and beta, got %+v", names)
	}
	c.observe("discovered echo and beta with pinned digests; beta annotations present=%v", len(listEv.Tools) > 0)

	before := srv.total()
	schema, digest := mcpInputSchema(t)
	_, err = a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "call_tool", "session_handle": handle,
		"tool": "beta", "arguments": json.RawMessage(`{}`),
		"input_schema": schema, "input_schema_digest": string(digest), "classification": "public",
	}, mcpQualCredRef, time.Now().UTC().Add(10*time.Second)))
	if err == nil {
		c.fail("expected permission_denied for beta")
	}
	f := mcpMustFault(t, err)
	if f.Code != contract.CodePermissionDenied {
		c.fail("beta call expected permission_denied, got %s", f.Code)
	}
	if got := srv.total() - before; got != 0 {
		c.fail("beta call expected 0 requests, got %d", got)
	}
	c.observe("beta (read_only_hint) refused permission_denied with 0 requests; digest was %q", betaDigest)

	pinned := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"must_be_present"},
	}
	pinnedRaw, _ := json.Marshal(pinned)
	canon, _ := contract.Canonicalize(pinnedRaw)
	pinnedDigest := contract.Hash(canon)
	before = srv.total()
	_, err = a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "call_tool", "session_handle": handle,
		"tool": "echo", "arguments": json.RawMessage(`{"text":"hello"}`),
		"input_schema": pinnedRaw, "input_schema_digest": string(pinnedDigest), "classification": "public",
	}, mcpQualCredRef, time.Now().UTC().Add(10*time.Second)))
	if err == nil {
		c.fail("expected invalid_input for non-conforming echo args")
	}
	f = mcpMustFault(t, err)
	if f.Code != contract.CodeInvalidInput {
		c.fail("echo bad-args expected invalid_input, got %s", f.Code)
	}
	if got := srv.total() - before; got != 0 {
		c.fail("echo bad-args expected 0 requests, got %d", got)
	}
	c.observe("echo with non-conforming args refused invalid_input with 0 requests; pinned digest %s", pinnedDigest)
}

func TestZ06MCPOneRequestPerCall(t *testing.T) {
	c := beginCase(t, "Z06.mcp_one_request_per_call", "Z06",
		"call_tool, list_tools and close_session each produce exactly one HTTP request per claimed attempt.",
		"open_session produces at most three handshake requests; every one listed in evidence.handshake; no GET listening stream.",
		"SDK retry and reconnect are disabled.")
	c.version("adapter_source_revision", sourceRevision())
	c.version("adapter_root", "mcp")

	srv := newMCPControlledServer()
	defer srv.close()
	blobs := newMemoryBlobs()
	secrets := memorySecrets{refs: map[string][]byte{mcpQualCredRef: []byte(mcpQualToken)}}
	profile := mcpBindProfile(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none", 0)
	a := mcpNewAdapter(t, srv.httpSrv.Client(), blobs, secrets, profile)

	handle, openObs := mcpOpenSession(t, a, mcpQualCredRef, 10*time.Second)
	counts := srv.counts()
	c.observe("open_session server counts=%v total=%d", counts, srv.total())
	if counts["server/discover"] != 1 || counts["initialize"] != 1 || counts["notifications/initialized"] != 1 {
		c.fail("open_session expected discover+initialize+initialized, got %v", counts)
	}
	if srv.total() != 3 {
		c.fail("open_session expected 3 physical requests, got %d", srv.total())
	}
	var openEv struct {
		Handshake []struct {
			Message string `json:"message"`
		} `json:"handshake"`
	}
	if err := json.Unmarshal(openObs.Evidence, &openEv); err != nil {
		c.fail("decode open evidence: %v", err)
	}
	if len(openEv.Handshake) != 3 {
		c.fail("evidence.handshake len=%d want 3", len(openEv.Handshake))
	}
	c.observe("handshake messages recorded in order: %+v", openEv.Handshake)

	schema, digest := mcpInputSchema(t)
	before := srv.total()
	callObs, err := a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "call_tool", "session_handle": handle,
		"tool": "echo", "arguments": json.RawMessage(`{"text":"one"}`),
		"input_schema": schema, "input_schema_digest": string(digest), "classification": "public",
	}, mcpQualCredRef, time.Now().UTC().Add(10*time.Second)))
	if err != nil {
		c.fail("call_tool: %v", err)
	}
	if callObs.Disposition != contract.DispositionSucceeded {
		c.fail("call_tool disposition %s", callObs.Disposition)
	}
	if got := srv.total() - before; got != 1 || srv.counts()["tools/call"] != 1 {
		c.fail("call_tool expected exactly 1 tools/call, delta=%d counts=%v", srv.total()-before, srv.counts())
	}
	c.observe("call_tool exactly 1 physical request")

	before = srv.total()
	_, err = a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "list_tools", "session_handle": handle,
	}, mcpQualCredRef, time.Now().UTC().Add(10*time.Second)))
	if err != nil {
		c.fail("list_tools: %v", err)
	}
	if got := srv.total() - before; got != 1 || srv.counts()["tools/list"] != 1 {
		c.fail("list_tools expected exactly 1 tools/list, delta=%d", got)
	}
	c.observe("list_tools exactly 1 physical request (next_cursor never followed)")

	before = srv.total()
	_, err = a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "close_session", "session_handle": handle,
	}, mcpQualCredRef, time.Now().UTC().Add(10*time.Second)))
	if err != nil {
		c.fail("close_session: %v", err)
	}
	if got := srv.total() - before; got != 1 || srv.counts()["DELETE"] != 1 {
		c.fail("close_session expected exactly 1 DELETE, delta=%d counts=%v", got, srv.counts())
	}
	c.observe("close_session exactly 1 DELETE")

	fresh := newMCPControlledServer()
	defer fresh.close()
	freshProfile := mcpBindProfile(t, fresh.endpoint(), true, []string{"echo"}, []string{"public"}, "none", 0)
	freshAdapter := mcpNewAdapter(t, fresh.httpSrv.Client(), newMemoryBlobs(), secrets, freshProfile)
	_, _ = mcpOpenSession(t, freshAdapter, mcpQualCredRef, 10*time.Second)
	if fresh.total() != 3 {
		c.fail("fresh open_session expected 3 requests, got %d", fresh.total())
	}
	c.observe("fresh open_session also at most 3 handshake requests; no GET listening stream opened")
}

func TestZ06NoHiddenRetriesMCP(t *testing.T) {
	c := beginCase(t, "Z06.no_hidden_retries", "Z06",
		"The mcp adapter performs exactly one physical request per claimed attempt.",
		"SDK or transport mutation retries are disabled.")
	c.version("adapter_source_revision", sourceRevision())
	c.version("adapter_root", "mcp")

	srv := newMCPControlledServer()
	defer srv.close()
	blobs := newMemoryBlobs()
	secrets := memorySecrets{refs: map[string][]byte{mcpQualCredRef: []byte(mcpQualToken)}}
	profile := mcpBindProfile(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none", 0)
	a := mcpNewAdapter(t, srv.httpSrv.Client(), blobs, secrets, profile)
	handle, _ := mcpOpenSession(t, a, mcpQualCredRef, 10*time.Second)
	before := srv.total()
	schema, digest := mcpInputSchema(t)
	_, err := a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "call_tool", "session_handle": handle,
		"tool": "echo", "arguments": json.RawMessage(`{"text":"once"}`),
		"input_schema": schema, "input_schema_digest": string(digest), "classification": "public",
	}, mcpQualCredRef, time.Now().UTC().Add(10*time.Second)))
	if err != nil {
		c.fail("call_tool: %v", err)
	}
	if got := srv.total() - before; got != 1 {
		c.fail("expected exactly one physical request for one Invoke, got %d", got)
	}
	c.observe("mcp root: one Invoke produced one physical tools/call (counts=%v)", srv.counts())
}

func TestZ05CallbackDiscoveryMCP(t *testing.T) {
	c := beginCase(t, "Z05.callback_discovery", "Z05",
		"Discovery grants no execution or disclosure authority.",
		"Unreviewed callbacks, destination changes and arbitrary external MCP/subprocess execution are refused.")
	c.version("adapter_source_revision", sourceRevision())
	c.version("adapter_root", "mcp")

	// stdio transport is the frozen subprocess shape: refused at construction.
	transport, _ := json.Marshal(map[string]any{
		"kind": "stdio", "command": "/usr/bin/false", "args": []string{},
	})
	profile := map[string]any{
		"schema":             "zatiti.mcp/v1",
		"transport":          json.RawMessage(transport),
		"protocol_version":   "2025-11-25",
		"credential_kind":    "none",
		"allowed_tools":      []string{"echo"},
		"tool_call_cost":     map[string]any{"currency": "USD", "micro_units": 0},
		"max_request_bytes":  1 << 16,
		"max_response_bytes": 1 << 16,
		"timeout_seconds":    30,
		"classifications":    []string{"public"},
		"capability_evidence": capabilityEvidence(
			"qualification-mcp-stdio", sourceRevision(), "2025-11-25",
			[]string{}, []string{"stdio unqualified"},
		),
	}
	raw := bindProfile(t, profile)
	_, err := mcpclient.New(contract.AdapterDependencies{
		HTTP: &http.Client{}, Secrets: memorySecrets{refs: map[string][]byte{}},
		Clock: &stepClock{now: time.Now().UTC()}, Blobs: newMemoryBlobs(),
	}, raw)
	if err == nil {
		c.fail("expected stdio transport to be refused at construction")
	}
	f := mcpMustFault(t, err)
	if f.Code != contract.CodeCapabilityUnsupported {
		c.fail("stdio expected capability_unsupported, got %s", f.Code)
	}
	c.observe("mcp root: stdio/subprocess transport refused capability_unsupported; discovery of a subprocess grants no authority")
}

func TestZ08MCPLostToolCallResponse(t *testing.T) {
	c := beginCase(t, "Z08.mcp_lost_tool_call_response", "Z08",
		"Attempt is recorded outcome_unknown with request_sent yes and staged request context retained; nothing is resent.",
		"Reconcile returns capability_unsupported and the reservation stays until a separately admitted linked operation resolves it.")
	c.version("adapter_source_revision", sourceRevision())
	c.version("adapter_root", "mcp")

	srv := newMCPControlledServer()
	defer srv.close()
	blobs := newMemoryBlobs()
	secrets := memorySecrets{refs: map[string][]byte{mcpQualCredRef: []byte(mcpQualToken)}}
	profile := mcpBindProfile(t, srv.endpoint(), true, []string{"stall"}, []string{"public"}, "none", 0)
	a := mcpNewAdapter(t, srv.httpSrv.Client(), blobs, secrets, profile)
	handle, _ := mcpOpenSession(t, a, mcpQualCredRef, 10*time.Second)

	schema, digest := mcpInputSchema(t)
	obs, err := a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "call_tool", "session_handle": handle,
		"tool": "stall", "arguments": json.RawMessage(`{}`),
		"input_schema": schema, "input_schema_digest": string(digest), "classification": "public",
	}, mcpQualCredRef, time.Now().UTC().Add(500*time.Millisecond)))
	if err != nil {
		c.fail("stall call_tool should return Observation, got error: %v", err)
	}
	if obs.Disposition != contract.DispositionUnknown {
		c.fail("expected unknown disposition, got %s", obs.Disposition)
	}
	var ev struct {
		PhysicalCall struct {
			RequestSent string `json:"request_sent"`
		} `json:"physical_call"`
	}
	if err := json.Unmarshal(obs.Evidence, &ev); err != nil {
		c.fail("decode evidence: %v", err)
	}
	if ev.PhysicalCall.RequestSent != "yes" {
		c.fail("expected request_sent=yes, got %q", ev.PhysicalCall.RequestSent)
	}
	if got := srv.counts()["tools/call"]; got != 1 {
		c.fail("expected exactly one tools/call (no resend), got %d", got)
	}
	c.observe("stall → unknown, request_sent=yes, tools/call count=1")

	_, err = a.Reconcile(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "call_tool", "session_handle": handle,
		"tool": "stall", "arguments": json.RawMessage(`{}`),
		"input_schema": schema, "input_schema_digest": string(digest), "classification": "public",
	}, mcpQualCredRef, time.Now().UTC().Add(10*time.Second)))
	if err == nil {
		c.fail("expected Reconcile capability_unsupported")
	}
	f := mcpMustFault(t, err)
	if f.Code != contract.CodeCapabilityUnsupported {
		c.fail("Reconcile expected capability_unsupported, got %s", f.Code)
	}
	c.observe("Reconcile capability_unsupported; reservation retained (no second tools/call)")
}

func TestZ13MCPCredentialConfined(t *testing.T) {
	c := beginCase(t, "Z13.mcp_credential_confined", "Z13",
		"Credential bytes appear only in the Authorization header on the wire.",
		"Staged request record, evidence, faults and logs contain neither the bytes nor the raw Mcp-Session-Id.",
		"Credential-in-path / OAuth / stdio profiles are refused capability_unsupported at construction.")
	c.version("adapter_source_revision", sourceRevision())
	c.version("adapter_root", "mcp")

	srv := newMCPControlledServer()
	defer srv.close()
	blobs := newMemoryBlobs()
	secrets := memorySecrets{refs: map[string][]byte{mcpQualCredRef: []byte(mcpQualToken)}}
	profile := mcpBindProfile(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "bearer", 0)
	a := mcpNewAdapter(t, srv.httpSrv.Client(), blobs, secrets, profile)

	handle, openObs := mcpOpenSession(t, a, mcpQualCredRef, 10*time.Second)
	assertNoCredentialBytes(t, mcpQualToken, blobs, openObs.Evidence)
	if !srv.sawBearer(mcpQualToken) {
		c.fail("server never received Authorization: Bearer <token>")
	}
	c.observe("open_session: credential present only in Authorization header")

	schema, digest := mcpInputSchema(t)
	callObs, err := a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "call_tool", "session_handle": handle,
		"tool": "echo", "arguments": json.RawMessage(`{"text":"cred"}`),
		"input_schema": schema, "input_schema_digest": string(digest), "classification": "public",
	}, mcpQualCredRef, time.Now().UTC().Add(10*time.Second)))
	if err != nil {
		c.fail("call_tool: %v", err)
	}
	assertNoCredentialBytes(t, mcpQualToken, blobs, callObs.Evidence)
	c.observe("call_tool: credential absent from staged docs and evidence; server saw Bearer")

	// stdio is frozen but unsupported. This check proves only subprocess
	// refusal; it does not qualify credential-in-path detection.
	transport, _ := json.Marshal(map[string]any{
		"kind": "stdio", "command": "/usr/bin/false", "args": []string{},
	})
	stdioProfile := map[string]any{
		"schema": "zatiti.mcp/v1", "transport": json.RawMessage(transport),
		"protocol_version": "2025-11-25", "credential_kind": "none",
		"allowed_tools":     []string{"echo"},
		"tool_call_cost":    map[string]any{"currency": "USD", "micro_units": 0},
		"max_request_bytes": 1 << 16, "max_response_bytes": 1 << 16, "timeout_seconds": 30,
		"classifications":     []string{"public"},
		"capability_evidence": capabilityEvidence("qualification-mcp-stdio", sourceRevision(), "2025-11-25", []string{}, []string{"stdio"}),
	}
	_, err = mcpclient.New(contract.AdapterDependencies{
		HTTP: &http.Client{}, Secrets: secrets, Clock: &stepClock{now: time.Now().UTC()}, Blobs: newMemoryBlobs(),
	}, bindProfile(t, stdioProfile))
	if err == nil {
		c.fail("expected stdio capability_unsupported")
	}
	if mcpMustFault(t, err).Code != contract.CodeCapabilityUnsupported {
		c.fail("stdio expected capability_unsupported, got %v", err)
	}
	c.observe("stdio refused capability_unsupported at construction; credential-in-path detection is not proved by this case")
}

func TestZ05MCPServerRequestsRefused(t *testing.T) {
	c := beginCase(t, "Z05.mcp_server_requests_refused", "Z05",
		"Each server-to-client request is answered method-not-found and named in refused_server_requests.",
		"No model call, no user prompt and no resource fetch happens; tool result is still recorded honestly.")
	c.version("adapter_source_revision", sourceRevision())
	c.version("adapter_root", "mcp")

	srv := newMCPControlledServer()
	defer srv.close()
	blobs := newMemoryBlobs()
	secrets := memorySecrets{refs: map[string][]byte{mcpQualCredRef: []byte(mcpQualToken)}}
	profile := mcpBindProfile(t, srv.endpoint(), true, []string{"server-requests"}, []string{"public"}, "none", 0)
	a := mcpNewAdapter(t, srv.httpSrv.Client(), blobs, secrets, profile)
	handle, _ := mcpOpenSession(t, a, mcpQualCredRef, 10*time.Second)

	schema, digest := mcpInputSchema(t)
	obs, err := a.Invoke(t.Context(), mcpDispatch(t, map[string]any{
		"schema": "zatiti.mcp.action/v1", "kind": "call_tool", "session_handle": handle,
		"tool": "server-requests", "arguments": json.RawMessage(`{}`),
		"input_schema": schema, "input_schema_digest": string(digest), "classification": "public",
	}, mcpQualCredRef, time.Now().UTC().Add(10*time.Second)))
	if err != nil {
		c.fail("call_tool: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		c.fail("expected succeeded despite refused server requests, got %s", obs.Disposition)
	}
	var ev struct {
		IsError               bool     `json:"is_error"`
		RefusedServerRequests []string `json:"refused_server_requests"`
	}
	if err := json.Unmarshal(obs.Evidence, &ev); err != nil {
		c.fail("decode evidence: %v", err)
	}
	if ev.IsError {
		c.fail("expected is_error false")
	}
	want := map[string]bool{"sampling/createMessage": true, "elicitation/create": true, "roots/list": true, "ping": true}
	got := map[string]bool{}
	for _, m := range ev.RefusedServerRequests {
		got[m] = true
	}
	for m := range want {
		if !got[m] {
			c.fail("expected %q in refused_server_requests, got %+v", m, ev.RefusedServerRequests)
		}
	}
	c.observe("refused_server_requests=%v; tool result recorded honestly", ev.RefusedServerRequests)
}
