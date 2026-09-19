package responses

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Fixtures below are shaped by the OpenAPI document PROTOCOL.md pins. They
// prove the adapter's translation, not the live endpoint's behaviour.

const (
	openaiEndpoint      = "https://api.example.test/v1/responses"
	openaiConversations = "https://api.example.test/v1/conversations"
)

// openaiProfile is the default profile re-pointed at the qualified
// protocol with an origin-wide destination grant, as PROTOCOL.md advises.
func openaiProfile(p *wireResponsesProfile) {
	p.Endpoint = openaiEndpoint
	p.Enforcement.ProviderDestinations = []string{"https://api.example.test"}
	p.CapabilityEvidence.ProtocolRevision = openaiProtocolRevision
	p.CapabilityEvidence.Capabilities = []string{capInputTokenBoundBytes}
	p.MaxInputTokens = openaiFlatTierMaxInputTokens
}

const openaiConversationBody = `{"id":"conv_abc123","object":"conversation","created_at":1700000000,"metadata":{}}`

const openaiCompletedBody = `{"id":"resp_001","object":"response","created_at":1700000001,"status":"completed","error":null,"incomplete_details":null,
"instructions":null,"model":"synthetic-model-a","tools":[],"tool_choice":"auto","parallel_tool_calls":true,"metadata":{},"temperature":1,"top_p":1,
"conversation":{"id":"conv_abc123"},"service_tier":"default",
"output":[{"type":"reasoning","id":"rs_1","summary":[]},{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"A cited brief.","annotations":[],"logprobs":[]},{"type":"output_text","text":"A second part.","annotations":[],"logprobs":[]}]}],
"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":123}}`

// openaiHarness builds the qualified adapter over the real protocol with
// a transport that answers the conversations and responses resources.
func openaiHarness(t *testing.T, conversationStatus int, conversationBody string, stepStatus int, stepBody string, opts ...harnessOption) *harness {
	t.Helper()
	fn := func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == openaiConversations {
			return respond(conversationStatus, conversationBody)(r)
		}
		return respond(stepStatus, stepBody)(r)
	}
	opts = append([]harnessOption{withProfile(openaiProfile)}, opts...)
	h := newHarness(t, fn, opts...)
	// Replace the synthetic protocol with the qualified registry: New
	// selects openaiProtocol by the profile's protocol_revision.
	deps := contract.AdapterDependencies{HTTP: &http.Client{Transport: h.transport}, Secrets: h.secrets, Clock: newFakeClock(), Blobs: h.blobs}
	a, err := New(deps, bindProfile(t, h.profile))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.adapter = a.(*Adapter)
	return h
}

func TestOpenAIProfileValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*wireResponsesProfile)
		want string
	}{
		{"input ceiling above the flat tier", func(p *wireResponsesProfile) { p.MaxInputTokens = openaiFlatTierMaxInputTokens + 1 }, "flat pricing tier"},
		{"endpoint is not the responses resource", func(p *wireResponsesProfile) {
			p.Endpoint = "https://api.example.test/v1/chat"
			p.Enforcement.ProviderDestinations = []string{"https://api.example.test"}
		}, "conversations resource cannot be derived"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := defaultProfile()
			openaiProfile(&p)
			tc.edit(&p)
			_, err := New(testDeps(), bindProfile(t, p))
			f := mustFault(t, err, contract.CodeCapabilityUnsupported)
			if !strings.Contains(f.Message, tc.want) {
				t.Fatalf("message %q does not mention %q", f.Message, tc.want)
			}
		})
	}
	p := defaultProfile()
	openaiProfile(&p)
	if _, err := New(testDeps(), bindProfile(t, p)); err != nil {
		t.Fatalf("New rejects the pinned profile shape: %v", err)
	}
}

func TestOpenAIConversationsURL(t *testing.T) {
	t.Parallel()
	got, err := openaiConversationsURL("https://api.openai.com/v1/responses")
	if err != nil || got != "https://api.openai.com/v1/conversations" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := openaiConversationsURL("https://api.openai.com/v1/responses/"); err == nil {
		t.Fatal("trailing slash accepted")
	}
}

func TestOpenAIInvokeSendsTheDocumentedShapes(t *testing.T) {
	t.Parallel()
	h := openaiHarness(t, 200, openaiConversationBody, 200, openaiCompletedBody)
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if h.transport.count() != 2 {
		t.Fatalf("physical calls = %d, want conversation then response", h.transport.count())
	}

	// 1. POST /v1/conversations with the attempt bound in metadata.
	conv := h.transport.requests[0]
	if conv.Method != http.MethodPost || conv.URL.String() != openaiConversations || conv.Header.Get("Authorization") != "Bearer "+testToken || conv.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("conversation request = %s %s %v", conv.Method, conv.URL, conv.Header)
	}
	var convBody map[string]map[string]string
	if err := json.Unmarshal(h.transport.bodies[0], &convBody); err != nil {
		t.Fatalf("conversation body: %v", err)
	}
	if convBody["metadata"]["zatiti_operation_id"] != string(h.dispatch.OperationID) || convBody["metadata"]["zatiti_attempt_id"] != string(h.dispatch.AttemptID) {
		t.Fatalf("conversation metadata = %v", convBody)
	}

	// 2. POST /v1/responses naming that conversation.
	step := h.transport.requests[1]
	if step.Method != http.MethodPost || step.URL.String() != openaiEndpoint || step.Header.Get("Authorization") != "Bearer "+testToken {
		t.Fatalf("step request = %s %s %v", step.Method, step.URL, step.Header)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(h.transport.bodies[1], &body); err != nil {
		t.Fatalf("step body: %v", err)
	}
	want := map[string]string{
		"model":                `"synthetic-model-a"`,
		"conversation":         `"conv_abc123"`,
		"max_output_tokens":    `1000`,
		"store":                `true`,
		"background":           `false`,
		"stream":               `false`,
		"service_tier":         `"default"`,
		"prompt_cache_options": `{"mode":"explicit"}`,
		"tool_choice":          `"auto"`,
	}
	for k, v := range want {
		if string(body[k]) != v {
			t.Fatalf("step body %s = %s, want %s", k, body[k], v)
		}
	}
	for _, absent := range []string{"previous_response_id", "instructions", "truncation", "temperature", "include"} {
		if _, ok := body[absent]; ok {
			t.Fatalf("step body carries %s, which is deliberately not sent", absent)
		}
	}
	wantInput := `[{"type":"message","role":"system","content":[{"type":"input_text","text":"You are a careful researcher."}]},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"Summarize the source."}]}]`
	if string(body["input"]) != wantInput {
		t.Fatalf("step input = %s", body["input"])
	}
	wantTools := `[{"type":"function","name":"fetch_source","description":"Fetch one public source.","parameters":{"type":"object"},"strict":false}]`
	if string(body["tools"]) != wantTools {
		t.Fatalf("step tools = %s", body["tools"])
	}

	// Outcome: succeeded, keyed by the conversation, priced from usage.
	if obs.Disposition != contract.DispositionSucceeded || obs.ProviderReference != "conv_abc123" {
		t.Fatalf("disposition %q, provider reference %q", obs.Disposition, obs.ProviderReference)
	}
	ev := decodeEvidence(t, obs)
	if ev.ResponseID != "resp_001" || ev.PhysicalCall.ProviderReference != "resp_001" || ev.Output.ContinuationReference != "conv_abc123" {
		t.Fatalf("references = %q / %q / %q", ev.ResponseID, ev.PhysicalCall.ProviderReference, ev.Output.ContinuationReference)
	}
	if ev.Output.FinishReason != "completed" || len(ev.Output.TextOutputs) != 2 || ev.PhysicalCall.ErrorCode != "" {
		t.Fatalf("output = %+v, physical %+v", ev.Output, ev.PhysicalCall)
	}
	// 120 * 1/5 ... no: the test profile prices 2 per input token and
	// 7/2 per output token: 240 + 11.
	if u := ev.Output.Usage; u.Billing != "observed" || u.Accounting.Spent != 251 || *u.InputTokens != 120 || *u.OutputTokens != 3 {
		t.Fatalf("usage = %+v", u)
	}
	assertNoSecret(t, h, obs, nil)
}

func TestOpenAIContextTranslation(t *testing.T) {
	t.Parallel()
	blobs := newFakeBlobStore()
	storeAttachment(blobs)
	resultDigest := blobs.put([]byte(`{"status":"ok","body":"source text"}`))
	c := defaultContext(t)
	proposal := wireModelToolProposal{
		ID: "call_1", Tool: testTool, OperationID: "artifact.read", OperationVersion: 1,
		Input: json.RawMessage(`{"url":"https://sources.example.test/a"}`), SourceContext: wireArtifactRef{ID: "eeeeeeee-0000-4000-8000-000000000001", Digest: contract.Hash([]byte("prior"))},
	}
	mustJSON := func(v any) json.RawMessage {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return raw
	}
	c.Messages = append(c.Messages,
		wireContextMessage{ID: "aaaaaaaa-0000-4000-8000-000000000003", Role: "assistant", Origin: "model_output", SourceArtifacts: []wireArtifactRef{},
			Parts: []json.RawMessage{textPart(t, "Fetching."), mustJSON(wireContextToolCall{Kind: partToolCall, Proposal: proposal})}},
		wireContextMessage{ID: "aaaaaaaa-0000-4000-8000-000000000004", Role: "tool", Origin: "tool_result", SourceArtifacts: []wireArtifactRef{},
			Parts: []json.RawMessage{mustJSON(wireContextToolResult{Kind: partToolResult, ProposalID: "call_1", OperationID: "ffffffff-0000-4000-8000-000000000001", Status: "completed",
				Artifact: wireArtifactRef{ID: "ffffffff-0000-4000-8000-000000000002", Digest: resultDigest}})}},
		wireContextMessage{ID: "aaaaaaaa-0000-4000-8000-000000000005", Role: "user", Origin: "memory_recall", SourceArtifacts: []wireArtifactRef{},
			Parts: []json.RawMessage{artifactPart(t, "internal"), mustJSON(wireContextMemoryExcerpt{Kind: partMemoryExcerpt, BrainID: "12121212-0000-4000-8000-000000000001", Claim: wireVersionRef{ID: "12121212-0000-4000-8000-000000000002", Version: 1},
				Text: "remembered", Sources: []wireArtifactRef{}, Confidence: 5, Freshness: c.CreatedAt, Scope: c.Scope,
				SelectedContext: wireArtifactRef{ID: "12121212-0000-4000-8000-000000000003", Digest: contract.Hash([]byte("selected"))}})}},
	)
	act := defaultAction(storeContext(t, blobs, c))
	profile := &responsesProfile{Classifications: map[string]bool{"internal": true}}
	doc, err := loadContext(context.Background(), blobs, profile, &act)
	if err != nil {
		t.Fatalf("loadContext: %v", err)
	}
	items, tools, err := openaiItems(doc)
	if err != nil {
		t.Fatalf("openaiItems: %v", err)
	}
	got, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"message","role":"system","content":[{"type":"input_text","text":"You are a careful researcher."}]},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"Summarize the source."}]},` +
		`{"type":"message","role":"assistant","content":[{"type":"input_text","text":"Fetching."}]},` +
		`{"type":"function_call","call_id":"call_1","name":"fetch_source","arguments":"{\"url\":\"https://sources.example.test/a\"}"},` +
		`{"type":"function_call_output","call_id":"call_1","output":"{\"status\":\"ok\",\"body\":\"source text\"}"},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"attachment"},{"type":"input_text","text":"remembered"}]}]`
	if string(got) != want {
		t.Fatalf("items =\n%s\nwant\n%s", got, want)
	}
	if len(tools) != 1 || tools[0].Name != "fetch_source" || tools[0].Strict {
		t.Fatalf("tools = %+v", tools)
	}

	// The byte bound is the documented formula over model-visible text:
	// 32 fixed; 5 messages x 8; text 29+21+9; the tool call 8+38+128; the
	// tool result 8+36; the artifact 8+10; the excerpt 10; the tool
	// 8+12+24+17 = 440.
	req := protocolRequest{Profile: protocolProfile{Capabilities: []string{capInputTokenBoundBytes}}, Context: doc}
	bound := (openaiProtocol{}).inputTokenBound(req)
	if bound == nil || *bound != 440 {
		t.Fatalf("input token bound = %v, want 440", bound)
	}
	if (openaiProtocol{}).inputTokenBound(protocolRequest{Context: doc}) != nil {
		t.Fatal("the bound was claimed without the qualification capability")
	}
}

func TestOpenAIRefusesPartsWithoutADocumentedShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*wireContextArtifact)
		want string
	}{
		{"binary artifact", func(c *wireContextArtifact) {
			c.Messages[1].Parts = append(c.Messages[1].Parts, json.RawMessage(strings.Replace(string(artifactPart(t, "internal")), `"text/plain"`, `"image/png"`, 1)))
		}, "only text/* artifacts"},
		{"tool role with text", func(c *wireContextArtifact) {
			c.Messages[1].Role = "tool"
			c.Messages[1].Origin = "tool_result"
		}, "role tool with text content"},
		{"continuation reference", nil, "continuation_reference"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := []harnessOption{}
			if tc.edit != nil {
				opts = append(opts, withContext(tc.edit))
			} else {
				opts = append(opts, withAction(func(a *wireResponsesParameters) { a.ContinuationReference = "resp_prev" }))
			}
			h := openaiHarness(t, 200, openaiConversationBody, 200, openaiCompletedBody, opts...)
			storeAttachment(h.blobs)
			_, err := h.adapter.Invoke(context.Background(), h.dispatch)
			f := mustFault(t, err, contract.CodeCapabilityUnsupported)
			if !strings.Contains(f.Message, tc.want) || h.transport.count() != 0 {
				t.Fatalf("message %q, calls %d", f.Message, h.transport.count())
			}
		})
	}
}

func TestOpenAIDecodeOutcomes(t *testing.T) {
	t.Parallel()
	response := func(status, extra string) string {
		return `{"id":"resp_x","object":"response","status":"` + status + `",` + extra + `"output":[],"usage":null}`
	}
	cases := []struct {
		name        string
		status      int
		body        string
		disposition string
		finish      string
		errorCode   string
		billing     string
		spent       int64
		texts       int
	}{
		{"completed", 200, openaiCompletedBody, contract.DispositionSucceeded, "completed", "", "observed", 251, 2},
		{"incomplete at the output ceiling", 200, `{"id":"resp_i","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":5,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":1000,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":1005}}`,
			contract.DispositionSucceeded, "length_limit", "", "observed", 3510, 0},
		{"incomplete by content filter", 200, response("incomplete", `"incomplete_details":{"reason":"content_filter"},`),
			contract.DispositionSucceeded, "refused", "", "unknown", 0, 0},
		{"refusal content", 200, `{"id":"resp_r","object":"response","status":"completed","output":[{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"I cannot help with that."}]}]}`,
			contract.DispositionSucceeded, "refused", "", "unknown", 0, 0},
		{"function call", 200, `{"id":"resp_f","object":"response","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_9","name":"fetch_source","arguments":"{\"url\":\"x\"}","status":"completed"}],"usage":{"input_tokens":5,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":10}}`,
			contract.DispositionSucceeded, "tool_calls", "tool_proposal_mapping_unspecified", "observed", 28, 0},
		{"failed with error", 200, response("failed", `"error":{"code":"server_error","message":"The model failed"},`),
			contract.DispositionFailed, "failed", "server_error", "unknown", 0, 0},
		{"cancelled", 200, response("cancelled", ``), contract.DispositionFailed, "failed", "cancelled", "unknown", 0, 0},
		{"queued is acceptance not completion", 200, response("queued", ``), contract.DispositionAccepted, "unknown", "", "unknown", 0, 0},
		{"cache activity cannot be priced", 200, `{"id":"resp_c","object":"response","status":"completed","output":[],"usage":{"input_tokens":2000,"input_tokens_details":{"cached_tokens":1024,"cache_write_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":2001}}`,
			contract.DispositionSucceeded, "completed", "usage_unpriceable", "unknown", 0, 0},
		{"another service tier cannot be priced", 200, `{"id":"resp_t","object":"response","status":"completed","service_tier":"priority","output":[],"usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":2}}`,
			contract.DispositionSucceeded, "completed", "usage_unpriceable", "unknown", 0, 0},
		{"undocumented status", 200, response("paused", ``), contract.DispositionUnknown, "unknown", "response_undecodable", "unknown", 0, 0},
		{"not a response object", 200, `{"id":"x","object":"conversation"}`, contract.DispositionUnknown, "unknown", "response_undecodable", "unknown", 0, 0},
		{"authentication rejected", 401, `{"error":{"message":"Incorrect API key provided: sk-***","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`,
			contract.DispositionFailed, "failed", "http_401", "unknown", 0, 0},
		{"rate limited", 429, `{"error":{"message":"Rate limit reached","type":"rate_limit_error","param":null,"code":"rate_limit_exceeded"}}`,
			contract.DispositionFailed, "failed", "http_429", "unknown", 0, 0},
		{"overloaded", 503, `{"error":{"message":"The model is overloaded","type":"service_unavailable_error","param":null,"code":"server_is_overloaded"}}`,
			contract.DispositionUnknown, "unknown", "http_503", "unknown", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := openaiHarness(t, 200, openaiConversationBody, tc.status, tc.body)
			obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if h.transport.count() != 2 {
				t.Fatalf("physical calls = %d", h.transport.count())
			}
			if obs.Disposition != tc.disposition {
				t.Fatalf("disposition = %q, want %q", obs.Disposition, tc.disposition)
			}
			ev := decodeEvidence(t, obs)
			if ev.Output.FinishReason != tc.finish || ev.PhysicalCall.ErrorCode != tc.errorCode || len(ev.Output.TextOutputs) != tc.texts {
				t.Fatalf("finish %q, error_code %q (%s), texts %d", ev.Output.FinishReason, ev.PhysicalCall.ErrorCode, ev.PhysicalCall.ErrorMessage, len(ev.Output.TextOutputs))
			}
			if u := ev.Output.Usage; u.Billing != tc.billing || u.Accounting.Spent != tc.spent {
				t.Fatalf("usage = %s %+v, want %s spent %d", u.Billing, u.Accounting, tc.billing, tc.spent)
			}
			assertNoSecret(t, h, obs, nil)
		})
	}
}

func TestOpenAIErrorMessageIsCarriedAndBounded(t *testing.T) {
	t.Parallel()
	h := openaiHarness(t, 200, openaiConversationBody, 400, `{"error":{"message":"Invalid value for max_output_tokens","type":"invalid_request_error","param":"max_output_tokens","code":null}}`)
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	ev := decodeEvidence(t, obs)
	if ev.PhysicalCall.ErrorMessage != "Invalid value for max_output_tokens" || obs.Disposition != contract.DispositionFailed {
		t.Fatalf("physical = %+v", ev.PhysicalCall)
	}
}

func TestOpenAIConversationFailureLeavesTheStepUnsent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"unauthorized", 401, `{"error":{"message":"Incorrect API key","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`, "invalid_request_error/invalid_api_key: Incorrect API key"},
		{"not a conversation", 200, `{"id":"resp_1","object":"response"}`, "not a conversation object"},
		{"unsafe id", 200, `{"id":"../etc","object":"conversation"}`, "not a conversation object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := openaiHarness(t, tc.status, tc.body, 200, openaiCompletedBody)
			obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if h.transport.count() != 1 || obs.Disposition != contract.DispositionNotSent {
				t.Fatalf("calls %d, disposition %q", h.transport.count(), obs.Disposition)
			}
			ev := decodeEvidence(t, obs)
			if ev.PhysicalCall.ErrorCode != "prepare_rejected" || !strings.Contains(ev.PhysicalCall.ErrorMessage, tc.want) {
				t.Fatalf("physical = %+v", ev.PhysicalCall)
			}
		})
	}
}

func TestOpenAIReconcile(t *testing.T) {
	t.Parallel()
	itemsWithOutput := `{"object":"list","data":[{"type":"message","id":"msg_u","role":"user","status":"completed","content":[{"type":"input_text","text":"Summarize the source."}]},{"type":"message","id":"msg_a","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Recovered brief.","annotations":[],"logprobs":[]}]}],"first_id":"msg_u","last_id":"msg_a","has_more":false}`
	cases := []struct {
		name        string
		status      int
		body        string
		disposition string
		finish      string
		errorCode   string
		texts       int
	}{
		{"assistant output present", 200, itemsWithOutput, contract.DispositionSucceeded, "completed", "", 1},
		{"tool call present", 200, `{"object":"list","data":[{"type":"function_call","id":"fc","call_id":"c1","name":"fetch_source","arguments":"{}","status":"completed"}],"first_id":"fc","last_id":"fc","has_more":false}`,
			contract.DispositionSucceeded, "tool_calls", "tool_proposal_mapping_unspecified", 0},
		{"more pages than read", 200, strings.Replace(itemsWithOutput, `"has_more":false`, `"has_more":true`, 1), contract.DispositionSucceeded, "completed", "reconcile_items_truncated", 1},
		{"no items yet", 200, `{"object":"list","data":[],"first_id":"","last_id":"","has_more":false}`, contract.DispositionUnknown, "unknown", "reconcile_unresolved", 0},
		{"conversation not found", 404, `{"error":{"message":"No conversation found","type":"invalid_request_error","param":null,"code":null}}`, contract.DispositionUnknown, "unknown", "http_404", 0},
		{"not a list", 200, `{"object":"response"}`, contract.DispositionUnknown, "unknown", "response_undecodable", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := openaiHarness(t, 200, openaiConversationBody, tc.status, tc.body)
			h.dispatch.ProviderKey = "conv_abc123"
			obs, err := h.adapter.Reconcile(context.Background(), h.dispatch)
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if h.transport.count() != 1 {
				t.Fatalf("physical calls = %d", h.transport.count())
			}
			req := h.transport.requests[0]
			if req.Method != http.MethodGet || req.URL.String() != openaiConversations+"/conv_abc123/items?limit=100&order=asc" || req.Header.Get("Authorization") != "Bearer "+testToken {
				t.Fatalf("lookup = %s %s", req.Method, req.URL)
			}
			if obs.Disposition != tc.disposition || obs.ProviderReference != "conv_abc123" {
				t.Fatalf("disposition %q reference %q", obs.Disposition, obs.ProviderReference)
			}
			ev := decodeEvidence(t, obs)
			if ev.Output.FinishReason != tc.finish || ev.PhysicalCall.ErrorCode != tc.errorCode || len(ev.Output.TextOutputs) != tc.texts {
				t.Fatalf("finish %q, error_code %q, texts %d", ev.Output.FinishReason, ev.PhysicalCall.ErrorCode, len(ev.Output.TextOutputs))
			}
			if u := ev.Output.Usage; u.Billing != "unknown" || u.Accounting.Unknown == 0 {
				t.Fatalf("usage = %+v; a lookup never sees usage", u)
			}
		})
	}

	t.Run("unsafe provider key", func(t *testing.T) {
		t.Parallel()
		h := openaiHarness(t, 200, openaiConversationBody, 200, itemsWithOutput)
		h.dispatch.ProviderKey = "conv/../items"
		_, err := h.adapter.Reconcile(context.Background(), h.dispatch)
		assertFault(t, err, contract.CodeInvalidInput)
		if h.transport.count() != 0 {
			t.Fatalf("calls = %d", h.transport.count())
		}
	})
}

// The enforced hard cap is only claimable once qualification recorded the
// byte bound; without the capability string the qualified protocol refuses
// an enforced profile and sends under an advisory one.
func TestOpenAIInputBoundIsAQualificationClaim(t *testing.T) {
	t.Parallel()
	unqualified := func(p *wireResponsesProfile) { p.CapabilityEvidence.Capabilities = []string{} }

	h := openaiHarness(t, 200, openaiConversationBody, 200, openaiCompletedBody, withProfile(unqualified))
	_, err := h.adapter.Invoke(context.Background(), h.dispatch)
	f := mustFault(t, err, contract.CodeCapabilityUnsupported)
	if !strings.Contains(f.Message, "cannot bound billed input tokens") || h.transport.count() != 0 {
		t.Fatalf("message %q, calls %d", f.Message, h.transport.count())
	}

	advisory := openaiHarness(t, 200, openaiConversationBody, 200, openaiCompletedBody, withProfile(unqualified),
		withProfile(func(p *wireResponsesProfile) { p.Enforcement.Cost = enforcementAdvisory }))
	obs, err := advisory.adapter.Invoke(context.Background(), advisory.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if u := decodeEvidence(t, obs).Output.Usage; obs.Disposition != contract.DispositionSucceeded || !u.Accounting.Advisory {
		t.Fatalf("disposition %q usage %+v", obs.Disposition, u)
	}
}
