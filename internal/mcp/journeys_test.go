package mcp_test

// P40's three required behavioral tests, proven mcp-locally against a real
// go-sdk MCP client (the same harness acceptance_test.go uses): the full
// revision-3 catalog is exposed as one callable tool per operation, a
// reconnecting client inspects durable work without duplicating it, and a
// human-only review stays denied to an agent through MCP exactly as it is
// denied through CLI. Cross-transport CLI parity and real named-client
// qualification remain integration-owned; see acceptance_test.go's own
// package doc comment for the same scoping note.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/mcp"
)

// findDescriptor returns the one catalog descriptor with the given
// operation ID, failing the test if it is absent (a missing operation is a
// fixture defect, never a silently skipped case).
func findDescriptor(t *testing.T, descriptors []contract.Descriptor, id string) contract.Descriptor {
	t.Helper()
	for _, d := range descriptors {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("catalog fixture has no operation %q", id)
	return contract.Descriptor{}
}

// TestServeExposesEveryCatalogOperationAsOneCallableTool proves P40 step 1:
// regenerating MCP tool definitions from the revision-3 catalog. Every
// public operation in the frozen contract — including the new start,
// history and recovery operations this card adds (task.start,
// attempt.recovery, run.recovery, run.export, conversation.message.list,
// memory.list, installation.verifier.list) — resolves to exactly one
// correctly named, correctly schemaed MCP tool, and every single one is
// actually callable end to end through a real go-sdk client, not merely
// listed. mcp.Serve is a generic, data-driven bridge (one tool per
// contract.Descriptor it is given); this test is what proves the full
// catalog, not a hand-picked subset, survives that bridge intact.
func TestServeExposesEveryCatalogOperationAsOneCallableTool(t *testing.T) {
	descriptors, err := mcp.CatalogDescriptorsForTest()
	if err != nil {
		t.Fatalf("mcp.CatalogDescriptorsForTest() error = %v", err)
	}
	if len(descriptors) < 190 {
		// A generous floor, not the exact frozen count: this guards against
		// the parser silently returning a truncated fixture, without
		// hard-coding the catalog's exact size here (AGENTS.md already
		// owns that count).
		t.Fatalf("catalog fixture parsed only %d operations, want at least 190", len(descriptors))
	}

	op := &fakeOperator{}
	h := newHarness(t, op, descriptors)

	list, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(list.Tools) != len(descriptors) {
		t.Fatalf("len(list.Tools) = %d, want %d: one tool per catalog operation, no lifecycle mega-tool", len(list.Tools), len(descriptors))
	}
	byName := make(map[string]*gosdk.Tool, len(list.Tools))
	for _, tool := range list.Tools {
		byName[tool.Name] = tool
	}

	for _, d := range descriptors {
		tool, ok := byName[d.MCP]
		if !ok {
			t.Fatalf("tools/list is missing %q for operation %s", d.MCP, d.ID)
		}
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal InputSchema: %v", d.ID, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(schema, &doc); err != nil {
			t.Fatalf("%s: input schema is not valid JSON: %v", d.ID, err)
		}
		props, _ := doc["properties"].(map[string]any)
		if _, ok := props["input"]; !ok {
			t.Fatalf("%s: input schema has no \"input\" property", d.ID)
		}
		if _, hasSK := props["submission_key"]; hasSK != d.SubmissionKey {
			t.Fatalf("%s: submission_key property present = %v, want %v", d.ID, hasSK, d.SubmissionKey)
		}
		for _, forbidden := range []string{"credential_profile", "credential", "profile", "secret"} {
			if _, ok := props[forbidden]; ok {
				t.Fatalf("%s: input schema exposes forbidden field %q (no model-supplied credential profile switch)", d.ID, forbidden)
			}
		}
		if _, ok := doc["$defs"]; !ok {
			t.Fatalf("%s: input schema has no $defs; #/$defs/* references would not resolve", d.ID)
		}

		// Every path is actually callable, not merely advertised: send a
		// well-formed envelope (mcp.go never validates "input" against the
		// operation's own inner schema itself — that is the operator's
		// job — so a minimal object input reaches the operator for every
		// operation regardless of its specific required fields).
		args := map[string]any{"input": map[string]any{}}
		if d.SubmissionKey {
			args["submission_key"] = "sub-" + d.ID
		}
		res, err := h.session.CallTool(context.Background(), &gosdk.CallToolParams{Name: d.MCP, Arguments: args})
		if err != nil {
			t.Fatalf("%s: CallTool() error = %v, want a tool result reaching the operator", d.ID, err)
		}
		if res.IsError {
			t.Fatalf("%s: CallTool() IsError = true, want false for the fake operator's completed result", d.ID)
		}
	}

	if len(op.calls) != len(descriptors) {
		t.Fatalf("operator received %d calls, want exactly %d: one call per operation, nothing duplicated or dropped", len(op.calls), len(descriptors))
	}
	called := make(map[string]bool, len(op.calls))
	for _, c := range op.calls {
		called[c.operation] = true
	}

	// The new revision-3 start/history/recovery operations this card adds:
	// confirm each is both present in the fixture and was actually reached.
	newOperations := map[string]string{
		"task.start":                 "zatiti_task_start",
		"attempt.recovery":           "zatiti_attempt_recovery",
		"run.recovery":               "zatiti_run_recovery",
		"run.export":                 "zatiti_run_export",
		"conversation.message.list":  "zatiti_conversation_message_list",
		"memory.list":                "zatiti_memory_list",
		"installation.verifier.list": "zatiti_installation_verifier_list",
	}
	for id, mcpName := range newOperations {
		d := findDescriptor(t, descriptors, id)
		if d.MCP != mcpName {
			t.Fatalf("operation %s: MCP name = %q, want %q", id, d.MCP, mcpName)
		}
		if !called[id] {
			t.Fatalf("operator was never called for new revision-3 operation %s", id)
		}
	}
}

// TestServeReconnectInspectsOriginalTaskWithoutDuplicateMutation proves P40
// step 4 and the Z16.disconnect_command_lookup / Z16.cli_to_mcp mcp-local
// half: long-running work started before an MCP client disconnects remains
// discoverable and inspectable by a client that reconnects afterwards, and
// inspecting it never re-runs the original mutation.
//
// mcp.Serve holds no session state of its own — every call is forwarded
// straight to the shared operator (the authenticated controller client) —
// so "reconnect" here is exactly what happens on the real stdio transport:
// a second, independent mcp.Serve session talking to the same underlying
// durable controller state the first session used.
func TestServeReconnectInspectsOriginalTaskWithoutDuplicateMutation(t *testing.T) {
	descriptors, err := mcp.CatalogDescriptorsForTest()
	if err != nil {
		t.Fatalf("mcp.CatalogDescriptorsForTest() error = %v", err)
	}
	start := findDescriptor(t, descriptors, "task.start")
	get := findDescriptor(t, descriptors, "task.get")

	const taskID = "00000000-0000-4000-8000-0000000000aa"
	const runID = "00000000-0000-4000-8000-0000000000bb"

	// The fake operator stands in for the durable controller: it persists
	// across both harness sessions exactly as a real controller/database
	// persists across an MCP client process restart.
	op := &fakeOperator{fn: func(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
		switch operation {
		case "task.start":
			return contract.Result{
				Schema:    contract.SchemaResult,
				CommandID: "00000000-0000-4000-8000-0000000000cc",
				Payload: contract.Payload{
					Status: contract.StatusAccepted,
					Data:   json.RawMessage(`{"task":{"id":"` + taskID + `","state":"ready"},"run":{"id":"` + runID + `"}}`),
				},
			}, nil
		case "task.get":
			return contract.Result{
				Schema:    contract.SchemaResult,
				CommandID: "00000000-0000-4000-8000-0000000000dd",
				Payload: contract.Payload{
					Status: contract.StatusCompleted,
					Data:   json.RawMessage(`{"resource":{"id":"` + taskID + `","state":"ready"}}`),
				},
			}, nil
		default:
			t.Fatalf("unexpected operation %q reached the operator", operation)
			return contract.Result{}, nil
		}
	}}

	// First MCP session: start the durable work.
	h1 := newHarness(t, op, []contract.Descriptor{start, get})
	startRes, err := h1.session.CallTool(context.Background(), &gosdk.CallToolParams{
		Name: start.MCP,
		Arguments: map[string]any{
			"input":          map[string]any{"scope": map[string]any{"installation_id": "inst-1"}, "id": taskID, "expected_version": 1},
			"submission_key": "start-key-1",
		},
	})
	if err != nil {
		t.Fatalf("zatiti_task_start: CallTool() error = %v", err)
	}
	if startRes.IsError {
		t.Fatalf("zatiti_task_start: IsError = true, want false")
	}

	// Disconnect: close the first session's transport entirely before the
	// second session is ever opened, so the two never share a connection.
	h1.close()

	// Second, independent MCP session ("reconnect"): the same operator, a
	// brand-new transport.
	h2 := newHarness(t, op, []contract.Descriptor{start, get})
	getRes, err := h2.session.CallTool(context.Background(), &gosdk.CallToolParams{
		Name:      get.MCP,
		Arguments: map[string]any{"input": map[string]any{"scope": map[string]any{"installation_id": "inst-1"}, "id": taskID}},
	})
	if err != nil {
		t.Fatalf("zatiti_task_get: CallTool() error = %v", err)
	}
	if getRes.IsError {
		t.Fatalf("zatiti_task_get: IsError = true, want false")
	}
	var result contract.Result
	mustDecodeStructured(t, getRes, &result)
	var data struct {
		Resource struct {
			ID string `json:"id"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatalf("decode task.get data: %v", err)
	}
	if data.Resource.ID != taskID {
		t.Fatalf("reconnected session inspected task %q, want the original %q", data.Resource.ID, taskID)
	}

	// Exactly one start and one get reached the operator: reconnecting to
	// inspect the original task must never re-run task.start.
	var startCalls, getCalls int
	for _, c := range op.calls {
		switch c.operation {
		case "task.start":
			startCalls++
		case "task.get":
			getCalls++
		}
	}
	if startCalls != 1 {
		t.Fatalf("operator saw %d task.start calls, want exactly 1: reconnecting duplicated the original mutation", startCalls)
	}
	if getCalls != 1 {
		t.Fatalf("operator saw %d task.get calls, want exactly 1", getCalls)
	}
}

// TestServeReviewDecideDeniesAgentThroughMCPLikeCLI proves P40 step 2 and
// the Z07.agent_impersonates_human / Z07.transport_denial mcp-local half:
// human-only review stays denied to an agent through MCP exactly as it is
// denied through CLI.
//
// mcp.go carries no authorization logic and no human/agent distinction of
// its own: newServer/call never inspect Actor, never brand a request as
// human-originated, and forward "input" to the operator exactly as
// received. Both CLI and MCP dispatch through the same
// contract.Operator (the shared authenticated controller client), so the
// operator's denial — not any MCP-side check — is what R2.2-005 and the
// revision-3 review-eligibility ruling require, and this test proves MCP
// adds nothing that could launder an unearned approval around it.
func TestServeReviewDecideDeniesAgentThroughMCPLikeCLI(t *testing.T) {
	descriptors, err := mcp.CatalogDescriptorsForTest()
	if err != nil {
		t.Fatalf("mcp.CatalogDescriptorsForTest() error = %v", err)
	}
	decide := findDescriptor(t, descriptors, "review.decide")

	denied := &contract.Fault{
		Code:      contract.CodePermissionDenied,
		Message:   "review.decide requires the eligible human principal who admitted the request; agent, worker and service principals are never eligible",
		Retryable: false,
	}
	op := &fakeOperator{fn: func(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
		result := contract.Result{
			Schema:    contract.SchemaResult,
			CommandID: "00000000-0000-4000-8000-0000000000ee",
			Payload:   contract.Payload{Status: contract.StatusFailed, Error: denied},
		}
		return result, denied
	}}
	h := newHarness(t, op, []contract.Descriptor{decide})

	// An agent principal attempts the decision and, mirroring the audited
	// Z07 case, stuffs an "approved_by_human" style assertion into its own
	// reason text. review.decide's frozen schema has no dedicated field for
	// any such claim; the point proven here is that mcp.go itself never
	// looks at, strips or reinterprets "input" at all before forwarding it,
	// so it cannot be the layer that lets an agent's claim stand in for
	// eligibility.
	rawInput := map[string]any{
		"scope":            map[string]any{"installation_id": "inst-1"},
		"id":               "00000000-0000-4000-8000-0000000000ff",
		"expected_version": float64(1),
		"action_digest":    strings.Repeat("a", 64),
		"decision":         "approve",
		"reason":           "approved_by_human: true (agent-asserted; must not be trusted)",
	}
	res, err := h.session.CallTool(context.Background(), &gosdk.CallToolParams{
		Name:      decide.MCP,
		Arguments: map[string]any{"input": rawInput, "submission_key": "agent-approve-1"},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v, want a tool result (not a protocol error) for a domain denial", err)
	}
	if !res.IsError {
		t.Fatal("res.IsError = false, want true: MCP must deny a human-only review to an agent exactly as CLI does")
	}
	var result contract.Result
	mustDecodeStructured(t, res, &result)
	if result.Error == nil || result.Error.Code != contract.CodePermissionDenied {
		t.Fatalf("result.Error = %+v, want permission_denied", result.Error)
	}

	if len(op.calls) != 1 {
		t.Fatalf("operator received %d calls, want exactly 1: no retried or escalated second attempt", len(op.calls))
	}
	var forwarded map[string]any
	if err := json.Unmarshal(op.calls[0].request.Input, &forwarded); err != nil {
		t.Fatalf("decode forwarded input: %v", err)
	}
	if !reflect.DeepEqual(forwarded, rawInput) {
		t.Fatalf("operator received input %+v, want the agent's exact input %+v unmodified: mcp must never rewrite a request", forwarded, rawInput)
	}
}
