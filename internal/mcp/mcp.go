package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zatiti/zatiti/internal/contract"
)

// serverName and serverVersion identify this adapter to MCP clients during
// initialize. They are display metadata only; no acceptance case depends on
// their exact value.
const (
	serverName    = "zatiti"
	serverVersion = "0.1.0"
)

// discoverMethod is SEP-2575's stateless "server/discover" RPC (mcp/protocol.go
// unexports its own methodDiscover constant with this same value). A modern
// client tries this negotiation before ever sending a legacy initialize
// request, and it only completes when the server supports protocol revision
// 2026-07-28 or later; left alone, this SDK version's negotiation would
// silently drift this pinned adapter forward to whatever protocol revision
// the vendored SDK happens to ship as its own latest.
//
// Rejecting the method outright (rather than reporting a narrower supported
// list via gosdk.ProtocolVersionSupporter) is deliberate: gosdk.Server's own
// discover handler unconditionally records the caller's requested version
// as the session's InitializeParams as a side effect of merely being asked,
// even when the client's own negotiation logic decides no mutually
// supported version exists and falls back to a legacy initialize — so a
// narrowed-but-still-answered discover response causes that fallback
// initialize to be refused as a duplicate. Refusing the method before the
// SDK's handler ever runs avoids that side effect entirely and reliably
// pins every real client at 2025-11-25 (the "handwritten adapter for MCP
// 2025-11-25" the shared contract requires) through the classic initialize
// handshake instead.
const discoverMethod = "server/discover"

// rejectDiscoverMiddleware refuses SEP-2575 discovery so every client
// negotiates through the classic initialize handshake this adapter
// implements. See discoverMethod's doc comment for why this cannot be done
// by narrowing the reported supported-version list instead.
func rejectDiscoverMiddleware(next gosdk.MethodHandler) gosdk.MethodHandler {
	return func(ctx context.Context, method string, req gosdk.Request) (gosdk.Result, error) {
		if method == discoverMethod {
			return nil, &jsonrpc.Error{
				Code:    jsonrpc.CodeMethodNotFound,
				Message: "server/discover is not supported: this adapter is pinned to MCP protocol revision 2025-11-25",
			}
		}
		return next(ctx, method, req)
	}
}

// Serve runs the stdio MCP adapter over in/out until ctx is cancelled or the
// peer closes the connection: it returns ctx.Err() on a cancelled shutdown
// and nil (or the session's own error) otherwise, matching
// internal/server.Server.Serve's convention for the HTTP transport. It
// registers one typed MCP tool per descriptor in descriptors and dispatches
// every call through op. Diagnostics (SDK and session logging) are written
// to diagnostics, never to out: out carries protocol frames only. Serve
// starts no controller, scheduler or execution authority of its own; a
// descriptor whose call cannot reach a controller surfaces as a
// controller_unavailable tool result rather than being retried or worked
// around here.
func Serve(ctx context.Context, in io.Reader, out io.Writer, diagnostics io.Writer, op contract.Operator, descriptors []contract.Descriptor) error {
	server, err := newServer(op, descriptors, diagnostics)
	if err != nil {
		return err
	}
	transport := &gosdk.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}}
	return server.Run(ctx, transport)
}

// nopWriteCloser adapts Serve's out io.Writer to the io.WriteCloser the SDK
// transport requires. out is owned by Serve's caller (typically os.Stdout)
// and must never be closed by this package.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// newServer builds the MCP server and registers one tool per descriptor.
// It registers no resources, prompts, sampling or elicitation handlers and
// explicitly clears the SDK's default logging capability, so initialize
// advertises only the "tools" capability inferred from AddTool — the
// baseline surface (initialize, tools/list, tools/call) the shared contract
// requires during qualification.
func newServer(op contract.Operator, descriptors []contract.Descriptor, diagnostics io.Writer) (*gosdk.Server, error) {
	defs, err := loadSharedDefs()
	if err != nil {
		return nil, err
	}
	logger := slog.New(slog.NewTextHandler(diagnostics, nil))
	server := gosdk.NewServer(&gosdk.Implementation{Name: serverName, Version: serverVersion}, &gosdk.ServerOptions{
		Logger:       logger,
		Capabilities: &gosdk.ServerCapabilities{},
	})
	server.AddReceivingMiddleware(rejectDiscoverMiddleware)

	registered := make(map[string]string, len(descriptors)) // MCP tool name -> operation ID
	for _, d := range descriptors {
		if err := validateDescriptor(d); err != nil {
			return nil, err
		}
		if existing, ok := registered[d.MCP]; ok {
			return nil, fmt.Errorf("mcp: tool %q maps to both %s and %s", d.MCP, existing, d.ID)
		}
		registered[d.MCP] = d.ID

		inputSchema, err := wrapInputSchema(d, defs)
		if err != nil {
			return nil, fmt.Errorf("mcp: %w", err)
		}
		outputSchema, err := wrapOutputSchema(d, defs)
		if err != nil {
			return nil, fmt.Errorf("mcp: %w", err)
		}

		binding := &toolBinding{descriptor: d, operator: op}
		server.AddTool(&gosdk.Tool{
			Name:         d.MCP,
			Description:  fmt.Sprintf("%s (%s/%s)", d.ID, d.Mode, d.Effect),
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
		}, binding.call)
	}
	return server, nil
}

// validateDescriptor rejects a descriptor that cannot become a well-formed
// MCP tool. Every descriptor Serve registers must be public (internal
// operations never appear in MCP, matching CLI and OpenAPI) and must carry
// an MCP name; wrapInputSchema/wrapOutputSchema separately require both
// schemas to be present.
func validateDescriptor(d contract.Descriptor) error {
	if d.ID == "" {
		return errors.New("mcp: descriptor has no operation ID")
	}
	if d.Visibility != contract.VisibilityPublic {
		return fmt.Errorf("mcp: operation %s is not public and cannot be registered as an MCP tool", d.ID)
	}
	if d.MCP == "" {
		return fmt.Errorf("mcp: operation %s declares no MCP mapping", d.ID)
	}
	return nil
}

// toolArgs is the tool-call envelope actually exposed to an MCP caller: the
// common request schema restricted to the fields a caller may set. Schema
// is never a caller-supplied field — call fills it with the frozen request
// schema constant — and no field ever selects a credential profile or
// carries raw secret material.
type toolArgs struct {
	Input         json.RawMessage `json:"input"`
	SubmissionKey string          `json:"submission_key,omitempty"`
}

// toolBinding dispatches one MCP tool call to its descriptor's operation
// through operator, the shared authenticated controller client.
type toolBinding struct {
	descriptor contract.Descriptor
	operator   contract.Operator
}

// call implements gosdk.ToolHandler. Arguments that do not match the
// declared envelope (missing "input", unknown fields, duplicate keys, wrong
// JSON types) are a malformed protocol message and produce a protocol error,
// never a tool result — the operation's own business validation of "input"
// happens exactly once, authoritatively, inside operator.Call. A domain
// failure (Payload.Status == failed, including controller_unavailable when
// the controller cannot be reached at all) becomes an isError tool result
// carrying the full result envelope as both structuredContent and
// equivalent JSON text, per the shared contract's MCP mapping.
func (b *toolBinding) call(ctx context.Context, req *gosdk.CallToolRequest) (*gosdk.CallToolResult, error) {
	args, err := decodeToolArgs(req.Params.Arguments)
	if err != nil {
		return nil, &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
			Message: fmt.Sprintf("%s: %v", b.descriptor.MCP, err),
		}
	}

	result, callErr := b.operator.Call(ctx, b.descriptor.ID, contract.Request{
		Schema:        contract.SchemaRequest,
		SubmissionKey: args.SubmissionKey,
		Input:         args.Input,
	})
	if callErr != nil {
		var fault *contract.Fault
		if !errors.As(callErr, &fault) {
			// No authoritative envelope exists: the call itself could not
			// complete (for example, the controller is unreachable).
			result = transportFailureResult(callErr)
		}
		// Otherwise result already carries the operator's own authoritative
		// failed envelope; callErr is just that envelope's Error field
		// surfaced as a Go error for errors.As callers.
	}

	body, err := json.Marshal(result)
	if err != nil {
		return nil, &jsonrpc.Error{
			Code:    jsonrpc.CodeInternalError,
			Message: fmt.Sprintf("%s: encoding result: %v", b.descriptor.MCP, err),
		}
	}
	return &gosdk.CallToolResult{
		StructuredContent: result,
		Content:           []gosdk.Content{&gosdk.TextContent{Text: string(body)}},
		IsError:           result.Status == contract.StatusFailed,
	}, nil
}

// decodeToolArgs strictly decodes one tool call's raw arguments into the
// frozen envelope: a required "input" value and an optional
// "submission_key" string, rejecting unknown fields, duplicate keys and a
// missing "input". Absent arguments decode as an empty object so a missing
// "input" is reported the same way either way.
func decodeToolArgs(raw json.RawMessage) (toolArgs, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	var args toolArgs
	if err := contract.DecodeStrict(raw, &args); err != nil {
		return toolArgs{}, fmt.Errorf("invalid tool arguments: %w", err)
	}
	if len(args.Input) == 0 {
		return toolArgs{}, errors.New(`"input" is required`)
	}
	return args, nil
}
