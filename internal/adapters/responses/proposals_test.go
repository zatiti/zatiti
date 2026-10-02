package responses

import (
	"context"
	"encoding/json"
	"net/http"
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
		BindingID:        contract.LocalDecisionToolID(contract.LocalDecisionToolReply),
		SchemaDigest:     contract.Hash(contract.ReplyProposalSchema),
		OperationID:      contract.LocalDecisionOperationID(contract.LocalDecisionToolReply),
		OperationVersion: 1,
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
	if p.ID != "call-1" || p.Tool != replyTool().Tool || p.OperationID != "zatiti.local_decision.reply" ||
		p.OperationVersion != 1 || p.SourceContext != action.ContextArtifact {
		t.Fatalf("proposal = %+v", p)
	}
	var in struct{ Text string }
	if err := json.Unmarshal(p.Input, &in); err != nil || in.Text != "Hello there." {
		t.Fatalf("input = %s (%v)", p.Input, err)
	}
}

func TestToolCallBecomesTypedProposal(t *testing.T) {
	t.Parallel()
	body := `{"ref":"r6","state":"completed","finish":"tool_calls","calls":[{"id":"call-1","name":"fetch_source","args":{"url":"https://sources.example.test/a"}}],"tokens":{"in":5,"out":5}}`
	h := newHarness(t, respond(200, body), withContext(func(c *wireContextArtifact) {
		c.Tools[0].OperationID = contract.AdapterToolOperation
		c.Tools[0].OperationVersion = 1
	}))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatal(err)
	}
	ev := decodeEvidence(t, obs)
	if ev.PhysicalCall.ErrorCode != "" || len(ev.Output.ToolProposals) != 1 || h.transport.count() != 1 {
		t.Fatalf("evidence = %+v; calls = %d", ev, h.transport.count())
	}
	p := ev.Output.ToolProposals[0]
	if p.Tool != testTool || p.OperationID != contract.AdapterToolOperation || p.OperationVersion != 1 {
		t.Fatalf("proposal = %+v", p)
	}
	var action wireResponsesParameters
	if err := json.Unmarshal(h.dispatch.Action, &action); err != nil || p.SourceContext != action.ContextArtifact {
		t.Fatalf("proposal context = %+v; action = %+v (%v)", p.SourceContext, action, err)
	}
}

// Any unoffered, unmapped, or malformed sibling prevents a partially
// interpreted response from being acted on.
func TestToolCallMappingIsAllOrNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		calls  string
		forged bool
	}{
		{"reply beside an unmapped product tool", `[{"id":"c1","name":"reply","args":{"text":"hi"}},{"id":"c2","name":"fetch_source","args":{"url":"https://sources.example.test/a"}}]`, false},
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
			wantCode := "tool_proposal_unoffered"
			switch tc.name {
			case "reply beside an unmapped product tool":
				wantCode = "tool_proposal_mapping_unspecified"
			case "duplicate call ids", "missing call id":
				wantCode = "tool_proposal_duplicate_id"
			case "non-object arguments":
				wantCode = "tool_proposal_arguments_invalid"
			}
			if ev.PhysicalCall.ErrorCode != wantCode || !strings.Contains(ev.PhysicalCall.ErrorMessage, "no typed proposal") {
				t.Fatalf("physical = %+v", ev.PhysicalCall)
			}
		})
	}
}

func TestReconciledToolCallBecomesTypedProposal(t *testing.T) {
	t.Parallel()
	body := `{"ref":"recovered","state":"completed","finish":"tool_calls","calls":[{"id":"recovered-call","name":"fetch_source","args":{"url":"https://sources.example.test/a"}}]}`
	h := newHarness(t, respond(200, body),
		withProfile(func(p *wireResponsesProfile) {
			p.Enforcement.ProviderDestinations = []string{"https://models.example.test"}
		}),
		withContext(func(c *wireContextArtifact) {
			c.Tools[0].OperationID = contract.AdapterToolOperation
			c.Tools[0].OperationVersion = 1
		}))
	h.dispatch.ProviderKey = "handle-7"
	obs, err := h.adapter.Reconcile(context.Background(), h.dispatch)
	if err != nil {
		t.Fatal(err)
	}
	ev := decodeEvidence(t, obs)
	if obs.Disposition != contract.DispositionSucceeded || len(ev.Output.ToolProposals) != 1 || ev.PhysicalCall.ErrorCode != "" {
		t.Fatalf("reconciled evidence = %+v", ev)
	}
	proposal := ev.Output.ToolProposals[0]
	var action wireResponsesParameters
	if err := json.Unmarshal(h.dispatch.Action, &action); err != nil {
		t.Fatal(err)
	}
	if proposal.Tool != testTool || proposal.OperationID != contract.AdapterToolOperation || proposal.OperationVersion != 1 || proposal.SourceContext != action.ContextArtifact {
		t.Fatalf("reconciled proposal = %+v", proposal)
	}
	if h.transport.count() != 1 || h.transport.requests[0].Method != http.MethodGet {
		t.Fatalf("reconciliation resent a step: %+v", h.transport.requests)
	}
}

func TestContextToolMappingMustBePaired(t *testing.T) {
	t.Parallel()
	for _, versionOnly := range []bool{false, true} {
		h := newHarness(t, respond(200, completedBody), withContext(func(c *wireContextArtifact) {
			if versionOnly {
				c.Tools[0].OperationVersion = 1
			} else {
				c.Tools[0].OperationID = contract.AdapterToolOperation
			}
		}))
		if _, err := h.adapter.Invoke(context.Background(), h.dispatch); err == nil {
			t.Fatal("half-mapped context was admitted")
		}
		if h.transport.count() != 0 {
			t.Fatal("invalid context caused a provider request")
		}
	}
}
