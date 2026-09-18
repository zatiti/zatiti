package responses

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func artifactPart(t *testing.T, classification string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(wireContextArtifactPart{
		Kind:           partArtifact,
		Artifact:       wireArtifactRef{ID: "dddddddd-0000-4000-8000-000000000001", Digest: contract.Hash([]byte("attachment"))},
		MediaType:      "text/plain",
		Classification: classification,
	})
	if err != nil {
		t.Fatalf("marshal artifact part: %v", err)
	}
	return raw
}

// Every refusal here happens before the wire boundary: no credential is
// resolved, nothing is staged and no physical call is made.
func TestInvokeRefusesBeforeAnyCall(t *testing.T) {
	t.Parallel()
	otherTool := wireVersionRef{ID: testTool.ID, Version: testTool.Version + 1}
	cases := []struct {
		name string
		opts []harnessOption
		code string
		want string
	}{
		{"partial capture", []harnessOption{withContext(func(c *wireContextArtifact) { c.Capture = "partial" })},
			contract.CodeInvalidInput, "complete capture"},
		{"context tool outside the pinned closure", []harnessOption{withAction(func(a *wireResponsesParameters) { a.ToolContractVersions = []wireVersionRef{otherTool} })},
			contract.CodeInvalidInput, "tool_contract_versions"},
		{"duplicate tool name", []harnessOption{withContext(func(c *wireContextArtifact) { c.Tools = append(c.Tools, c.Tools[0]) })},
			contract.CodeInvalidInput, "more than once"},
		{"tool pinned twice", []harnessOption{withAction(func(a *wireResponsesParameters) { a.ToolContractVersions = []wireVersionRef{testTool, otherTool} })},
			contract.CodeInvalidInput, "more than once"},
		{"restricted artifact not permitted for this provider", []harnessOption{withContext(func(c *wireContextArtifact) {
			c.Messages[1].Parts = append(c.Messages[1].Parts, artifactPart(t, "restricted"))
		})}, contract.CodePermissionDenied, "restricted"},
		{"context fails its schema", []harnessOption{withContext(func(c *wireContextArtifact) { c.Schema = "zatiti.context/v2" })},
			contract.CodeInvalidInput, "zatiti.context/v1"},
		{"output ceiling above the profile's", []harnessOption{withAction(func(a *wireResponsesParameters) { a.MaxOutputTokens = 4097 })},
			contract.CodeBudgetUnavailable, "max_output_tokens"},
		{"action fails its schema", []harnessOption{withAction(func(a *wireResponsesParameters) { a.Kind = "embedding" })},
			contract.CodeInvalidInput, "zatiti.responses.action/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, respond(200, `{}`), tc.opts...)
			obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
			f := mustFault(t, err, tc.code)
			if !strings.Contains(f.Message, tc.want) {
				t.Fatalf("message %q does not mention %q", f.Message, tc.want)
			}
			if obs.Disposition != "" || obs.Evidence != nil {
				t.Fatalf("a refusal returned an observation: %+v", obs)
			}
			if h.transport.count() != 0 || h.secrets.getCount() != 0 || h.blobs.stageCount() != 0 {
				t.Fatalf("refusal did work: calls=%d secret gets=%d stages=%d", h.transport.count(), h.secrets.getCount(), h.blobs.stageCount())
			}
		})
	}
}

func TestInvokeRefusesUntrustworthyContextBytes(t *testing.T) {
	t.Parallel()

	t.Run("bytes do not hash to the bound digest", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, respond(200, `{}`))
		h.blobs.putAs(h.action.ContextArtifact.Digest, []byte(`{"schema":"zatiti.context/v1"}`))
		_, err := h.adapter.Invoke(context.Background(), h.dispatch)
		f := mustFault(t, err, contract.CodeArtifactFault)
		if !strings.Contains(f.Message, "not the digest the action binds") {
			t.Fatalf("message = %q", f.Message)
		}
		if h.transport.count() != 0 {
			t.Fatalf("calls = %d", h.transport.count())
		}
	})

	t.Run("context artifact is absent", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, respond(200, `{}`))
		h.action.ContextArtifact.Digest = contract.Hash([]byte("never stored"))
		_, err := h.adapter.Invoke(context.Background(), testDispatch(t, h.action))
		assertFault(t, err, contract.CodeArtifactFault)
		if h.transport.count() != 0 {
			t.Fatalf("calls = %d", h.transport.count())
		}
	})

	t.Run("no blob store", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, respond(200, `{}`))
		h.adapter.deps.Blobs = nil
		_, err := h.adapter.Invoke(context.Background(), h.dispatch)
		assertFault(t, err, contract.CodePrerequisiteMissing)
	})
}

func TestLoadContextDecodesEveryPartInOrderAndRaisesClassification(t *testing.T) {
	t.Parallel()
	blobs := newFakeBlobStore()
	c := defaultContext(t)
	proposal := wireModelToolProposal{
		ID: "call-1", Tool: testTool, OperationID: "artifact.read", OperationVersion: 1,
		Input: json.RawMessage(`{"q":"x"}`), SourceContext: wireArtifactRef{ID: "eeeeeeee-0000-4000-8000-000000000001", Digest: contract.Hash([]byte("prior"))},
	}
	mustJSON := func(v any) json.RawMessage {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return raw
	}
	c.Messages[1].Parts = []json.RawMessage{
		textPart(t, "first"),
		artifactPart(t, "restricted"),
		mustJSON(wireContextToolCall{Kind: partToolCall, Proposal: proposal}),
		mustJSON(wireContextToolResult{Kind: partToolResult, ProposalID: "call-1", OperationID: "ffffffff-0000-4000-8000-000000000001", Status: "completed",
			Artifact: wireArtifactRef{ID: "ffffffff-0000-4000-8000-000000000002", Digest: contract.Hash([]byte("result"))}}),
		mustJSON(wireContextMemoryExcerpt{Kind: partMemoryExcerpt, BrainID: "12121212-0000-4000-8000-000000000001", Claim: wireVersionRef{ID: "12121212-0000-4000-8000-000000000002", Version: 1},
			Text: "remembered", Sources: []wireArtifactRef{}, Confidence: 5, Freshness: c.CreatedAt, Scope: c.Scope,
			SelectedContext: wireArtifactRef{ID: "12121212-0000-4000-8000-000000000003", Digest: contract.Hash([]byte("selected"))}}),
		textPart(t, "last"),
	}
	act := defaultAction(storeContext(t, blobs, c))
	profile := &responsesProfile{Classifications: map[string]bool{"internal": true, "restricted": true}}

	doc, err := loadContext(context.Background(), blobs, profile, &act)
	if err != nil {
		t.Fatalf("loadContext: %v", err)
	}
	var kinds []string
	for _, p := range doc.Messages[1].DecodedParts {
		kinds = append(kinds, p.Kind)
	}
	want := "text,artifact,tool_call,tool_result,memory_excerpt,text"
	if got := strings.Join(kinds, ","); got != want {
		t.Fatalf("part order = %s, want %s", got, want)
	}
	parts := doc.Messages[1].DecodedParts
	if parts[0].Text.Text != "first" || parts[5].Text.Text != "last" || parts[2].ToolCall.Proposal.OperationID != "artifact.read" ||
		parts[3].ToolResult.ProposalID != "call-1" || parts[4].MemoryExcerpt.Text != "remembered" {
		t.Fatalf("parts decoded incorrectly: %+v", parts)
	}
	if doc.Classification != "restricted" {
		t.Fatalf("classification = %q, want restricted", doc.Classification)
	}
	if doc.Ref != act.ContextArtifact {
		t.Fatalf("ref = %+v", doc.Ref)
	}

	// Public-only content still stages as internal, the default floor.
	public := defaultContext(t)
	public.Messages[1].Parts = []json.RawMessage{artifactPart(t, "public")}
	act = defaultAction(storeContext(t, blobs, public))
	profile.Classifications["public"] = true
	doc, err = loadContext(context.Background(), blobs, profile, &act)
	if err != nil {
		t.Fatalf("loadContext: %v", err)
	}
	if doc.Classification != "internal" {
		t.Fatalf("classification = %q, want internal", doc.Classification)
	}
}
