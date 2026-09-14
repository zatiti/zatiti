package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/zatiti/zatiti/internal/cli"
	"github.com/zatiti/zatiti/internal/contract"
)

// fakeCall records one Operator.Call invocation for inspection.
type fakeCall struct {
	operation string
	request   contract.Request
}

// fakeOperator is a local contract.Operator test double. When fn is nil it
// returns a completed result with an empty data object; tests that need a
// specific status, fault or error set fn.
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
		Schema:    "zatiti.result/v1",
		CommandID: "00000000-0000-4000-8000-000000000001",
		Payload: contract.Payload{
			Status: contract.StatusCompleted,
			Data:   json.RawMessage(`{}`),
		},
	}, nil
}

// poisonReader panics on any Read: tests use it as stdin to prove the CLI
// never reads it unless --input is exactly "-".
type poisonReader struct{}

func (poisonReader) Read([]byte) (int, error) {
	panic("cli: stdin read without --input -")
}

// descriptor builds a minimal, valid contract.Descriptor for tests: a
// well-formed object input schema and a public visibility, since only
// CLI/ID/mode/submission-key ever affect the behavior under test.
func descriptor(id string, path []string, mode string, submissionKey bool) contract.Descriptor {
	return contract.Descriptor{
		ID:            id,
		Version:       1,
		Owner:         "test",
		Visibility:    contract.VisibilityPublic,
		Mode:          mode,
		Effect:        contract.EffectLocal,
		InputSchema:   json.RawMessage(`{"type":"object","additionalProperties":true}`),
		OutputSchema:  json.RawMessage(`{"type":"object"}`),
		CLI:           path,
		MCP:           "zatiti_test",
		SubmissionKey: submissionKey,
	}
}

// run executes the CLI in-process and captures its streams and exit code.
func run(t *testing.T, args []string, op contract.Operator, descriptors []contract.Descriptor, stdin io.Reader) (stdout, stderr string, code int) {
	t.Helper()
	if stdin == nil {
		stdin = bytes.NewReader(nil)
	}
	var out, errBuf bytes.Buffer
	code = cli.Execute(context.Background(), args, op, descriptors, cli.IO{In: stdin, Out: &out, Err: &errBuf})
	return out.String(), errBuf.String(), code
}
