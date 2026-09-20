package mcp_test

// Named acceptance cases from AGENTS.md, proven mcp-locally: a real go-sdk
// MCP client drives internal/mcp.Serve over an in-memory, full-duplex
// connection (net.Pipe), so every case observes genuine JSON-RPC/MCP wire
// behavior rather than internal Go function calls. Cross-transport parity
// with the CLI (Z16.*) and real named-client qualification
// (QUALIFICATION.named_agent_clients) are integration-owned; this package
// proves the mcp-local half of each case: protocol framing, schema/error
// mapping, controller-unavailable handling, and the absence of any
// credential-profile or optional-capability surface.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/mcp"
)

// harness wires internal/mcp.Serve to a real go-sdk client over an
// in-memory, full-duplex net.Pipe connection and tears both down on
// cleanup.
type harness struct {
	t           *testing.T
	session     *gosdk.ClientSession
	diagnostics *syncBuffer
	serveErr    chan error
	cancel      context.CancelFunc
	serverConn  net.Conn
	clientConn  net.Conn
	closeOnce   sync.Once
}

func newHarness(t *testing.T, op contract.Operator, descriptors []contract.Descriptor) *harness {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	diagnostics := &syncBuffer{}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- mcp.Serve(ctx, serverConn, serverConn, diagnostics, op, descriptors)
	}()

	c := gosdk.NewClient(&gosdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := c.Connect(ctx, &gosdk.IOTransport{Reader: clientConn, Writer: clientConn}, nil)
	if err != nil {
		cancel()
		t.Fatalf("client.Connect() error = %v", err)
	}

	h := &harness{
		t:           t,
		session:     session,
		diagnostics: diagnostics,
		serveErr:    serveErr,
		cancel:      cancel,
		serverConn:  serverConn,
		clientConn:  clientConn,
	}
	t.Cleanup(h.close)
	return h
}

// close tears the harness down: it is safe to call explicitly (for example,
// to simulate a client disconnecting mid-test) as well as from t.Cleanup,
// since closeOnce makes only the first call actually wait on serveErr — a
// later, Cleanup-driven call is then a no-op instead of blocking for
// serveErr's 5-second timeout on a channel nothing will ever fill again.
func (h *harness) close() {
	h.closeOnce.Do(func() {
		_ = h.session.Close()
		h.cancel()
		_ = h.serverConn.Close()
		_ = h.clientConn.Close()
		select {
		case <-h.serveErr:
		case <-time.After(5 * time.Second):
			h.t.Fatal("mcp.Serve did not return after the session closed and ctx was cancelled")
		}
	})
}

// syncBuffer is a concurrency-safe io.Writer: the SDK's logger and the
// test's assertions both touch it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func TestServeBaselineProtocolAndCapabilities(t *testing.T) {
	// QUALIFICATION.baseline_mcp: a pinned protocol client disables
	// resources, prompts, sampling, elicitation and task extensions;
	// discovery and operation must still work under the pinned revision.
	h := newHarness(t, &fakeOperator{}, []contract.Descriptor{
		widgetDescriptor("widget.create", "zatiti_widget_create", contract.ModeMutation, true),
	})

	init := h.session.InitializeResult()
	if init == nil {
		t.Fatal("InitializeResult() = nil")
	}
	if init.ProtocolVersion != "2025-11-25" {
		t.Fatalf("negotiated protocol version = %q, want 2025-11-25", init.ProtocolVersion)
	}
	if init.Capabilities.Tools == nil {
		t.Fatal("server capabilities has no tools capability")
	}
	if init.Capabilities.Resources != nil {
		t.Fatalf("server capabilities advertises resources = %+v, want none (baseline only)", init.Capabilities.Resources)
	}
	if init.Capabilities.Prompts != nil {
		t.Fatalf("server capabilities advertises prompts = %+v, want none (baseline only)", init.Capabilities.Prompts)
	}
	//nolint:staticcheck // SA1019: intentionally asserting the deprecated
	// Logging capability is absent, to prove the baseline-only surface.
	if init.Capabilities.Logging != nil {
		t.Fatalf("server capabilities advertises logging = %+v, want none (baseline only)", init.Capabilities.Logging)
	}

	if h.diagnostics.Len() == 0 {
		t.Fatal("diagnostics writer received no server log output")
	}
}

func TestServeToolsListMatchesDescriptors(t *testing.T) {
	// Z02.registry_surface (mcp half): every descriptor Serve receives is
	// discoverable as an MCP tool at its declared name.
	descriptors := []contract.Descriptor{
		widgetDescriptor("widget.create", "zatiti_widget_create", contract.ModeMutation, true),
		widgetDescriptor("widget.get", "zatiti_widget_get", contract.ModeQuery, false),
	}
	h := newHarness(t, &fakeOperator{}, descriptors)

	list, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(list.Tools) != len(descriptors) {
		t.Fatalf("len(list.Tools) = %d, want %d", len(list.Tools), len(descriptors))
	}
	names := map[string]*gosdk.Tool{}
	for _, tool := range list.Tools {
		names[tool.Name] = tool
	}
	for _, d := range descriptors {
		tool, ok := names[d.MCP]
		if !ok {
			t.Fatalf("tools/list is missing %q", d.MCP)
		}
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal InputSchema: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(schema, &doc); err != nil {
			t.Fatalf("tool %s input schema is not valid JSON: %v", d.MCP, err)
		}
		props, _ := doc["properties"].(map[string]any)
		if _, ok := props["input"]; !ok {
			t.Fatalf("tool %s input schema has no \"input\" property", d.MCP)
		}
		_, hasSubmissionKey := props["submission_key"]
		if hasSubmissionKey != d.SubmissionKey {
			t.Fatalf("tool %s submission_key property present = %v, want %v", d.MCP, hasSubmissionKey, d.SubmissionKey)
		}
		if _, ok := doc["$defs"]; !ok {
			t.Fatalf("tool %s input schema has no $defs; #/$defs/* references would not resolve", d.MCP)
		}
		for _, forbidden := range []string{"credential_profile", "credential", "profile", "secret"} {
			if _, ok := props[forbidden]; ok {
				t.Fatalf("tool %s input schema exposes forbidden field %q", d.MCP, forbidden)
			}
		}
	}
}

func TestServeCallToolCompleted(t *testing.T) {
	op := &fakeOperator{fn: func(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
		return contract.Result{
			Schema:    contract.SchemaResult,
			CommandID: "00000000-0000-4000-8000-000000000042",
			Payload: contract.Payload{
				Status: contract.StatusCompleted,
				Data:   json.RawMessage(`{"ok":true}`),
			},
		}, nil
	}}
	d := widgetDescriptor("widget.create", "zatiti_widget_create", contract.ModeMutation, true)
	h := newHarness(t, op, []contract.Descriptor{d})

	res, err := h.session.CallTool(context.Background(), &gosdk.CallToolParams{
		Name:      d.MCP,
		Arguments: map[string]any{"input": map[string]any{"name": "a"}, "submission_key": "k1"},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.IsError {
		t.Fatalf("res.IsError = true, want false: %+v", res)
	}
	var result contract.Result
	mustDecodeStructured(t, res, &result)
	if result.CommandID != "00000000-0000-4000-8000-000000000042" {
		t.Fatalf("result.CommandID = %q, want the operator's command id", result.CommandID)
	}
	if result.Status != contract.StatusCompleted {
		t.Fatalf("result.Status = %q, want completed", result.Status)
	}

	if len(op.calls) != 1 {
		t.Fatalf("len(op.calls) = %d, want 1", len(op.calls))
	}
	if op.calls[0].operation != d.ID {
		t.Fatalf("operator called with operation %q, want %q (no shell/CLI wrapper in between)", op.calls[0].operation, d.ID)
	}
	if op.calls[0].request.Schema != contract.SchemaRequest {
		t.Fatalf("request.Schema = %q, want %q", op.calls[0].request.Schema, contract.SchemaRequest)
	}
	if op.calls[0].request.SubmissionKey != "k1" {
		t.Fatalf("request.SubmissionKey = %q, want k1", op.calls[0].request.SubmissionKey)
	}
}

func TestServeCallToolDomainFailureIsError(t *testing.T) {
	// Z02.wire_errors (mcp half): a domain failure maps to isError true with
	// the full result envelope as structuredContent and equivalent text.
	fault := &contract.Fault{Code: contract.CodeInvalidInput, Message: "name is required", Retryable: false}
	op := &fakeOperator{fn: func(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
		result := contract.Result{
			Schema:    contract.SchemaResult,
			CommandID: "00000000-0000-4000-8000-000000000099",
			Payload: contract.Payload{
				Status: contract.StatusFailed,
				Error:  fault,
			},
		}
		return result, fault
	}}
	d := widgetDescriptor("widget.create", "zatiti_widget_create", contract.ModeMutation, true)
	h := newHarness(t, op, []contract.Descriptor{d})

	res, err := h.session.CallTool(context.Background(), &gosdk.CallToolParams{
		Name:      d.MCP,
		Arguments: map[string]any{"input": map[string]any{}, "submission_key": "k1"},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v, want a tool result (not a protocol error) for a domain failure", err)
	}
	if !res.IsError {
		t.Fatalf("res.IsError = false, want true for a domain failure")
	}
	var result contract.Result
	mustDecodeStructured(t, res, &result)
	if result.Error == nil || result.Error.Code != contract.CodeInvalidInput {
		t.Fatalf("result.Error = %+v, want invalid_input", result.Error)
	}
	if result.CommandID != "00000000-0000-4000-8000-000000000099" {
		t.Fatalf("result.CommandID = %q, want the operator's authoritative command id preserved", result.CommandID)
	}
}

func TestServeCallToolControllerUnavailable(t *testing.T) {
	// Local proving focus: "missing controller named controller_unavailable,
	// never start another scheduler." A transport-level failure (no
	// authoritative envelope) still surfaces as a named, inspectable domain
	// result rather than a bare protocol error.
	op := &fakeOperator{fn: func(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
		return contract.Result{}, errors.Join(client.ErrControllerUnavailable, errors.New("dial unix: connect: no such file or directory"))
	}}
	d := widgetDescriptor("widget.create", "zatiti_widget_create", contract.ModeMutation, true)
	h := newHarness(t, op, []contract.Descriptor{d})

	res, err := h.session.CallTool(context.Background(), &gosdk.CallToolParams{
		Name:      d.MCP,
		Arguments: map[string]any{"input": map[string]any{"name": "a"}, "submission_key": "k1"},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v, want a tool result", err)
	}
	if !res.IsError {
		t.Fatal("res.IsError = false, want true for controller_unavailable")
	}
	var result contract.Result
	mustDecodeStructured(t, res, &result)
	if result.Error == nil || result.Error.Code != contract.CodeControllerUnavailable {
		t.Fatalf("result.Error = %+v, want controller_unavailable", result.Error)
	}
	if !result.Error.Retryable {
		t.Fatal("result.Error.Retryable = false, want true for controller_unavailable")
	}
	if result.CommandID != "" {
		t.Fatalf("result.CommandID = %q, want empty: no command was ever created", result.CommandID)
	}

	// Exactly one call reached the operator; nothing retried, no second
	// scheduler or controller was invented locally.
	if len(op.calls) != 1 {
		t.Fatalf("len(op.calls) = %d, want exactly 1: mcp must not retry or start another controller itself", len(op.calls))
	}
}

func TestServeCallToolUnknownOutcome(t *testing.T) {
	op := &fakeOperator{fn: func(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
		return contract.Result{}, &client.UnknownAckError{Operation: "widget.create", SubmissionKey: "k1", Err: errors.New("timeout")}
	}}
	d := widgetDescriptor("widget.create", "zatiti_widget_create", contract.ModeMutation, true)
	h := newHarness(t, op, []contract.Descriptor{d})

	res, err := h.session.CallTool(context.Background(), &gosdk.CallToolParams{
		Name:      d.MCP,
		Arguments: map[string]any{"input": map[string]any{"name": "a"}, "submission_key": "k1"},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v, want a tool result", err)
	}
	if !res.IsError {
		t.Fatal("res.IsError = false, want true for outcome_unknown")
	}
	var result contract.Result
	mustDecodeStructured(t, res, &result)
	if result.Error == nil || result.Error.Code != contract.CodeOutcomeUnknown {
		t.Fatalf("result.Error = %+v, want outcome_unknown", result.Error)
	}
}

func TestServeCallToolMalformedArgumentsIsProtocolError(t *testing.T) {
	// R8.3-003: malformed protocol messages use protocol errors, never a
	// tool result with isError.
	d := widgetDescriptor("widget.create", "zatiti_widget_create", contract.ModeMutation, true)
	op := &fakeOperator{}
	h := newHarness(t, op, []contract.Descriptor{d})

	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing input", map[string]any{"submission_key": "k1"}},
		{"unknown field", map[string]any{"input": map[string]any{"name": "a"}, "submission_key": "k1", "credential_profile": "admin"}},
		{"wrong type for submission_key", map[string]any{"input": map[string]any{"name": "a"}, "submission_key": 123}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.session.CallTool(context.Background(), &gosdk.CallToolParams{
				Name:      d.MCP,
				Arguments: tc.args,
			})
			if err == nil {
				t.Fatal("CallTool() error = nil, want a protocol error for malformed arguments")
			}
			var jerr *jsonrpc.Error
			if !errors.As(err, &jerr) {
				t.Fatalf("CallTool() error = %v (%T), want a *jsonrpc.Error", err, err)
			}
			if jerr.Code != jsonrpc.CodeInvalidParams {
				t.Fatalf("jerr.Code = %d, want %d (invalid params)", jerr.Code, jsonrpc.CodeInvalidParams)
			}
		})
	}
	if len(op.calls) != 0 {
		t.Fatalf("len(op.calls) = %d, want 0: malformed arguments must never reach the operator", len(op.calls))
	}
}

func TestServeRejectsDuplicateMCPName(t *testing.T) {
	descriptors := []contract.Descriptor{
		widgetDescriptor("widget.create", "zatiti_widget", contract.ModeMutation, true),
		widgetDescriptor("widget.update", "zatiti_widget", contract.ModeMutation, true),
	}
	err := mcp.Serve(context.Background(), bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}, &fakeOperator{}, descriptors)
	if err == nil {
		t.Fatal("Serve() error = nil, want an error for a duplicate MCP tool name")
	}
}

func TestServeRejectsInternalDescriptor(t *testing.T) {
	d := widgetDescriptor("widget.internal", "zatiti_widget_internal", contract.ModeMutation, true)
	d.Visibility = contract.VisibilityInternal
	err := mcp.Serve(context.Background(), bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}, &fakeOperator{}, []contract.Descriptor{d})
	if err == nil {
		t.Fatal("Serve() error = nil, want an error for an internal-visibility descriptor")
	}
}

func TestServeReturnsContextErrorOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	done := make(chan error, 1)
	go func() {
		done <- mcp.Serve(ctx, serverConn, serverConn, &bytes.Buffer{}, &fakeOperator{}, []contract.Descriptor{
			widgetDescriptor("widget.create", "zatiti_widget_create", contract.ModeMutation, true),
		})
	}()

	// Give the server a moment to start Run() before cancelling, so the
	// cancellation exercises the running session, not a race at startup.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not return after ctx was cancelled")
	}
	_ = serverConn.Close()
}

// mustDecodeStructured decodes structuredContent into out and asserts the
// single text content item is equivalent JSON to it. Both sides are
// compared as decoded values, not raw bytes: the client round-trips
// structuredContent through a generic map, which does not preserve the
// wire's field order, while the text item is compared byte-for-byte against
// what the server actually sent on the wire.
func mustDecodeStructured(t *testing.T, res *gosdk.CallToolResult, out *contract.Result) {
	t.Helper()
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal StructuredContent: %v", err)
	}
	if err := json.Unmarshal(structured, out); err != nil {
		t.Fatalf("structuredContent is not a result envelope: %v", err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("len(res.Content) = %d, want 1", len(res.Content))
	}
	text, ok := res.Content[0].(*gosdk.TextContent)
	if !ok {
		t.Fatalf("res.Content[0] type = %T, want *gosdk.TextContent", res.Content[0])
	}
	var fromText contract.Result
	if err := json.Unmarshal([]byte(text.Text), &fromText); err != nil {
		t.Fatalf("text content is not a result envelope: %v", err)
	}
	if !reflect.DeepEqual(fromText, *out) {
		t.Fatalf("text content decodes to %+v, want the same envelope as structuredContent %+v", fromText, *out)
	}
}
