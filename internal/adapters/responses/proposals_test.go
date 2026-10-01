package responses

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// replyTool is the sealed reply decision tool exactly as execution's
// context builder declares it.
func replyTool() wireContextToolDefinition {
	return wireContextToolDefinition{
		Tool: wireVersionRef{ID: contract.LocalDecisionToolID(contract.LocalDecisionToolReply), Version: 1},
		Name: contract.LocalDecisionToolReply, Description: "sealed local decision tool: reply",
		InputSchema: contract.ReplyProposalSchema, OutputSchema: json.RawMessage(`{}`),
		Effect: "local", Destinations: []string{},
		BindingID:    contract.LocalDecisionToolID(contract.LocalDecisionToolReply),
		SchemaDigest: contract.Hash(contract.ReplyProposalSchema),
	}
}

func withReplyTool(forgedID bool) []harnessOption {
	tool := replyTool()
	if forgedID {
		tool.Tool.ID = "dddddddd-0000-4000-8000-000000000009"
	}
	return []harnessOption{
		withContext(func(c *wireContextArtifact) { c.Tools = append(c.Tools, tool) }),
		withAction(func(a *wireResponsesParameters) {
			a.ToolContractVersions = append(a.ToolContractVersions, tool.Tool)
		}),
	}
}

// A sealed reply call becomes one typed proposal bound to the exact context
// that was sent; the adapter still executes nothing.
func TestSealedReplyCallBecomesTypedProposal(t *testing.T) {
	t.Parallel()
	body := `{"ref":"r4","state":"completed","finish":"tool_calls","calls":[{"id":"call-1","name":"reply","args":{"text":"Hello there."}}],"tokens":{"in":5,"out":5}}`
	h := newHarness(t, respond(200, body), withReplyTool(false)...)
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if h.transport.count() != 1 || obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("calls=%d disposition=%q", h.transport.count(), obs.Disposition)
	}
	ev := decodeEvidence(t, obs)
	if ev.PhysicalCall.ErrorCode != "" {
		t.Fatalf("unexpected flag: %+v", ev.PhysicalCall)
	}
	if len(ev.Output.ToolProposals) != 1 {
		t.Fatalf("proposals = %+v", ev.Output.ToolProposals)
	}
	p := ev.Output.ToolProposals[0]
	var action wireResponsesParameters
	if err := json.Unmarshal(h.dispatch.Action, &action); err != nil {
		t.Fatal(err)
	}
	if p.ID != "call-1" || p.Tool != replyTool().Tool || p.OperationID != "zatiti.local-decision/reply" ||
		p.OperationVersion != 1 || p.SourceContext != action.ContextArtifact {
		t.Fatalf("proposal = %+v", p)
	}
	var in struct{ Text string }
	if err := json.Unmarshal(p.Input, &in); err != nil || in.Text != "Hello there." {
		t.Fatalf("input = %s (%v)", p.Input, err)
	}
}

// Anything short of an all-sealed, well-formed set maps nothing.
func TestToolCallMappingIsAllOrNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		calls  string
		forged bool
	}{
		{"reply beside an unmapped product tool", `[{"id":"c1","name":"reply","args":{"text":"hi"}},{"id":"c2","name":"fetch_source","args":{"url":"https://sources.example.test/a"}}]`, false},
		{"reply name under a forged identity", `[{"id":"c1","name":"reply","args":{"text":"hi"}}]`, true},
		{"undeclared tool", `[{"id":"c1","name":"clarify","args":{"question":"?"}}]`, false},
		{"duplicate call ids", `[{"id":"c1","name":"reply","args":{"text":"a"}},{"id":"c1","name":"reply","args":{"text":"b"}}]`, false},
		{"missing call id", `[{"id":"","name":"reply","args":{"text":"hi"}}]`, false},
		{"non-object arguments", `[{"id":"c1","name":"reply","args":"hi"}]`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := `{"ref":"r5","state":"completed","finish":"tool_calls","calls":` + tc.calls + `,"tokens":{"in":5,"out":5}}`
			h := newHarness(t, respond(200, body), withReplyTool(tc.forged)...)
			obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			ev := decodeEvidence(t, obs)
			if len(ev.Output.ToolProposals) != 0 {
				t.Fatalf("proposals = %+v, want none", ev.Output.ToolProposals)
			}
			if ev.PhysicalCall.ErrorCode != "tool_proposal_mapping_unspecified" || !strings.Contains(ev.PhysicalCall.ErrorMessage, "no typed proposal") {
				t.Fatalf("physical = %+v", ev.PhysicalCall)
			}
		})
	}
}
