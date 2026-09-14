package mcp_test

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// fakeCall records one Operator.Call invocation for inspection.
type fakeCall struct {
	operation string
	request   contract.Request
}

// fakeOperator is a local contract.Operator test double, mirroring
// internal/cli's own test double. When fn is nil it returns a completed
// result with an empty data object; tests that need a specific status,
// fault or transport-level error set fn.
type fakeOperator struct {
	calls []fakeCall
	fn    func(ctx context.Context, operation string, req contract.Request) (contract.Result, error)
}

func (f *fakeOperator) Call(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
	f.calls = append(f.calls, fakeCall{operation: operation, request: req})
	if f.fn != nil {
		return f.fn(ctx, operation, req)
	}
	return contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: "00000000-0000-4000-8000-000000000001",
		Payload: contract.Payload{
			Status: contract.StatusCompleted,
			Data:   json.RawMessage(`{}`),
		},
	}, nil
}

// widgetDescriptor builds a minimal, valid contract.Descriptor for tests: a
// well-formed object input/output schema and public visibility. Only the
// fields the test under discussion cares about vary.
func widgetDescriptor(id, mcpName string, mode string, submissionKey bool) contract.Descriptor {
	return contract.Descriptor{
		ID:            id,
		Version:       1,
		Owner:         "test",
		Visibility:    contract.VisibilityPublic,
		Mode:          mode,
		Effect:        contract.EffectLocal,
		InputSchema:   json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string"}},"required":["name"]}`),
		OutputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"ok":{"type":"boolean"}},"required":["ok"]}`),
		CLI:           []string{"widget", "create"},
		MCP:           mcpName,
		SubmissionKey: submissionKey,
	}
}
