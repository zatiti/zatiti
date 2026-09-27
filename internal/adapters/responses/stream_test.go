package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

type publishedPreview struct{ id, text, state string }

// recordingStreams records every Publish so tests can see exactly what a
// provider stream disclosed, in order.
type recordingStreams struct {
	mu        sync.Mutex
	published []publishedPreview
	routes    map[contract.Digest]contract.ReplyRoute
}

func (r *recordingStreams) Register(contract.Digest, contract.ReplyRoute) {}
func (r *recordingStreams) Commit(contract.ID, string)                    {}
func (r *recordingStreams) Route(d contract.Digest) (contract.ReplyRoute, bool) {
	route, ok := r.routes[d]
	return route, ok
}
func (r *recordingStreams) Publish(_ contract.ReplyRoute, id, text, state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.published = append(r.published, publishedPreview{id, text, state})
}
func (r *recordingStreams) Watch(contract.ID, contract.ID, contract.ID) (<-chan struct{}, func()) {
	return nil, func() {}
}
func (r *recordingStreams) Snapshot(contract.ID, contract.ID, contract.ID) []contract.ReplyPreview {
	return nil
}
func (r *recordingStreams) last() publishedPreview {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.published) == 0 {
		return publishedPreview{}
	}
	return r.published[len(r.published)-1]
}

func sse(events ...any) string {
	var b strings.Builder
	for _, e := range events {
		raw, _ := json.Marshal(e)
		fmt.Fprintf(&b, "data: %s\n\n", raw)
	}
	return b.String()
}

func added(id, name string) map[string]any {
	return map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": id, "type": "function_call", "name": name, "arguments": ""}}
}
func delta(id, d string) map[string]any {
	return map[string]any{"type": "response.function_call_arguments.delta", "item_id": id, "delta": d}
}
func terminalEvent(kind, id, name, args string) map[string]any {
	return map[string]any{"type": kind, "response": map[string]any{
		"id": "resp_1", "status": strings.TrimPrefix(kind, "response."),
		"output": []any{map[string]any{"id": id, "type": "function_call", "name": name, "arguments": args}},
	}}
}

var streamRoute = contract.ReplyRoute{Recipient: "human-1", Conversation: "conv-1", Turn: "turn-1"}

func TestReplyStreamDisclosesOnlyReplyTextThenTheFinalReply(t *testing.T) {
	hub := &recordingStreams{}
	stream := sse(
		map[string]any{"type": "response.output_text.delta", "delta": "free prose must stay private"},
		added("fc_other", "search"),
		delta("fc_other", `{"query":"secret`),
		added("fc_1", contract.LocalDecisionToolReply),
		delta("fc_1", `{"text":"Hel`),
		delta("fc_1", "lo \\u00e9"),
		delta("fc_1", `. More`),
		terminalEvent("response.completed", "fc_1", contract.LocalDecisionToolReply, `{"text":"Hello é. More text."}`),
	)
	body, wire, err := readReplyStream(strings.NewReader(stream), 1<<20, hub, streamRoute, "att")
	if err != nil {
		t.Fatalf("readReplyStream: %v", err)
	}
	if string(wire) != stream {
		t.Fatal("wire evidence is not the exact provider stream")
	}
	var resp struct{ ID string }
	if json.Unmarshal(body, &resp) != nil || resp.ID != "resp_1" {
		t.Fatalf("terminal body = %s, want the completed response", body)
	}
	for _, p := range hub.published {
		if p.id != "att:fc_1" || strings.Contains(p.text, "secret") || strings.Contains(p.text, "prose") {
			t.Fatalf("disclosed %+v, want only the reply tool text", p)
		}
	}
	if got := hub.last(); got.state != "generated" || got.text != "Hello é. More text." {
		t.Fatalf("final preview = %+v, want the generated final reply", got)
	}
}

func TestReplyStreamWithoutTerminalIsInterruptedAndAReadError(t *testing.T) {
	hub := &recordingStreams{}
	stream := sse(added("fc_1", contract.LocalDecisionToolReply), delta("fc_1", `{"text":"Partial answer. And`))
	if _, _, err := readReplyStream(strings.NewReader(stream), 1<<20, hub, streamRoute, "att"); err == nil {
		t.Fatal("a stream cut before its terminal response must be a read error")
	}
	if got := hub.last(); got.state != "interrupted" || got.text != "Partial answer. And" {
		t.Fatalf("final preview = %+v, want interrupted at the disclosed prefix", got)
	}
}

// A malformed or rewritten preview withdraws only the preview; the
// terminal response still decides the model step's outcome.
func TestReplyPreviewFaultsNeverFailTheModelStep(t *testing.T) {
	for name, events := range map[string][]any{
		"invalid reply key": {
			added("fc_1", contract.LocalDecisionToolReply), delta("fc_1", `{"txt":"x"}`),
			terminalEvent("response.completed", "fc_1", contract.LocalDecisionToolReply, `{"text":"x"}`),
		},
		"final differs from preview": {
			added("fc_1", contract.LocalDecisionToolReply), delta("fc_1", `{"text":"Yes`),
			terminalEvent("response.completed", "fc_1", contract.LocalDecisionToolReply, `{"text":"No."}`),
		},
		"final missing the item": {
			added("fc_1", contract.LocalDecisionToolReply), delta("fc_1", `{"text":"Yes`),
			terminalEvent("response.completed", "fc_9", "search", `{}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			hub := &recordingStreams{}
			body, _, err := readReplyStream(strings.NewReader(sse(events...)), 1<<20, hub, streamRoute, "att")
			if err != nil || len(body) == 0 {
				t.Fatalf("err = %v, body = %q; want the terminal response with no read error", err, body)
			}
			if got := hub.last(); got.state != "interrupted" {
				t.Fatalf("final preview = %+v, want interrupted", got)
			}
			if got := hub.last(); name == "final differs from preview" && got.text != "Yes" {
				t.Fatalf("withdrawn preview text = %q, want the disclosed prefix only", got.text)
			}
		})
	}
}

func TestReplyStreamIncompleteResponseReachesTheDecoder(t *testing.T) {
	hub := &recordingStreams{}
	stream := sse(
		added("fc_1", contract.LocalDecisionToolReply), delta("fc_1", `{"text":"Cut`),
		terminalEvent("response.incomplete", "fc_1", contract.LocalDecisionToolReply, `{"text":"Cut"}`),
	)
	body, _, err := readReplyStream(strings.NewReader(stream), 1<<20, hub, streamRoute, "att")
	if err != nil || !strings.Contains(string(body), `"incomplete"`) {
		t.Fatalf("err = %v, body = %s; want the incomplete response for the existing decoder", err, body)
	}
	if got := hub.last(); got.state != "interrupted" {
		t.Fatalf("final preview = %+v, want interrupted", got)
	}
}

func TestReplyStreamBoundsAndDuplicateTerminal(t *testing.T) {
	hub := &recordingStreams{}
	stream := sse(terminalEvent("response.completed", "a", "b", "{}"), terminalEvent("response.completed", "a", "b", "{}"))
	if _, _, err := readReplyStream(strings.NewReader(stream), 1<<20, hub, streamRoute, "att"); err == nil {
		t.Fatal("duplicate terminal responses must be a read error")
	}
	big := sse(added("fc_1", contract.LocalDecisionToolReply), delta("fc_1", `{"text":"`+strings.Repeat("a", 4096)))
	if _, _, err := readReplyStream(strings.NewReader(big), 1024, hub, streamRoute, "att"); err == nil {
		t.Fatal("a stream beyond max_response_bytes must be a read error")
	}
}

func TestReplyPrefixDecodesPartialArguments(t *testing.T) {
	for raw, want := range map[string]string{
		``:                           "",
		`{`:                          "",
		`{"te`:                       "",
		`{"text":`:                   "",
		`{"text":"Hi \"you`:          `Hi "you`,
		`{"text":"caf\u00`:           "caf",
		"{\"text\":\"\\ud83d\\ude00": "😀",
		`{"text":"\ud83d`:            "",
		`{"text":"done"}`:            "done",
	} {
		got, err := replyPrefix(raw)
		if err != nil || got != want {
			t.Errorf("replyPrefix(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{`[`, `{"other":"x"}`, `{"text":1}`, `{"text":"a","extra":1}`} {
		if _, err := replyPrefix(raw); err == nil {
			t.Errorf("replyPrefix(%q) accepted a non-reply shape", raw)
		}
	}
}

// With the preview route gone, the stream still decodes to its terminal
// response and nothing is published.
func TestReplyStreamWithoutAHubStillDecodes(t *testing.T) {
	stream := sse(
		added("fc_1", contract.LocalDecisionToolReply), delta("fc_1", `{"text":"Hi`),
		terminalEvent("response.completed", "fc_1", contract.LocalDecisionToolReply, `{"text":"Hi."}`),
	)
	body, wire, err := readReplyStream(strings.NewReader(stream), 1<<20, nil, contract.ReplyRoute{}, "att")
	if err != nil || len(body) == 0 || string(wire) != stream {
		t.Fatalf("err = %v, body = %q; want the terminal response and exact wire evidence", err, body)
	}
}

const openaiReplyResponse = `{"id":"resp_002","object":"response","created_at":1700000001,"status":"completed","error":null,"incomplete_details":null,
"instructions":null,"model":"synthetic-model-a","tools":[],"tool_choice":"auto","parallel_tool_calls":true,"metadata":{},"temperature":1,"top_p":1,
"conversation":{"id":"conv_abc123"},"service_tier":"default",
"output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"reply","arguments":"{\"text\":\"Hello there. More.\"}","status":"completed"}],
"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":123}}`

// streamingOpenAIHarness is openaiHarness with a reply route registered for
// the step's context, so Invoke asks for a stream. The fake provider
// streams only when asked, exactly like the documented API.
func streamingOpenAIHarness(t *testing.T, hub *recordingStreams) *harness {
	t.Helper()
	sse := sse(
		added("fc_1", contract.LocalDecisionToolReply),
		delta("fc_1", `{"text":"Hello there.`),
		delta("fc_1", ` More."}`),
		map[string]any{"type": "response.completed", "response": json.RawMessage(openaiReplyResponse)},
	)
	fn := func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == openaiConversations {
			return respond(200, openaiConversationBody)(r)
		}
		if r.Header.Get("Accept") == "text/event-stream" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sse)),
				Header: http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}}}, nil
		}
		return respond(200, openaiReplyResponse)(r)
	}
	h := newHarness(t, fn, withProfile(openaiProfile))
	if hub != nil {
		hub.routes = map[contract.Digest]contract.ReplyRoute{h.action.ContextArtifact.Digest: streamRoute}
	}
	deps := contract.AdapterDependencies{HTTP: &http.Client{Transport: h.transport}, Secrets: h.secrets, Clock: newFakeClock(), Blobs: h.blobs}
	if hub != nil {
		deps.Streams = hub
	}
	a, err := New(deps, bindProfile(t, h.profile))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.adapter = a.(*Adapter)
	prep, err := h.adapter.Invoke(context.Background(), testDispatch(t, defaultPrepareSessionAction()))
	if err != nil || prep.Disposition != contract.DispositionSucceeded {
		t.Fatalf("prepare_session bootstrap: %v / %+v", err, prep)
	}
	h.action.SessionHandle = prep.ProviderReference
	h.dispatch = testDispatch(t, h.action)
	return h
}

// Streaming changes how the body arrives, never the outcome: the observed
// disposition and usage match the same response delivered as JSON, and
// the exact stream is the staged evidence.
func TestOpenAIModelStepStreamsReplyPreviewWithoutChangingTheOutcome(t *testing.T) {
	t.Parallel()
	hub := &recordingStreams{}
	streamed := streamingOpenAIHarness(t, hub)
	got, err := streamed.adapter.Invoke(context.Background(), streamed.dispatch)
	if err != nil {
		t.Fatalf("Invoke (stream): %v", err)
	}
	plain := streamingOpenAIHarness(t, nil)
	want, err := plain.adapter.Invoke(context.Background(), plain.dispatch)
	if err != nil {
		t.Fatalf("Invoke (json): %v", err)
	}
	if got.Disposition != want.Disposition {
		t.Fatalf("stream disposition %q, json disposition %q", got.Disposition, want.Disposition)
	}
	gotEv, wantEv := decodeEvidence(t, got), decodeEvidence(t, want)
	if gotEv.Output.Usage.Billing != wantEv.Output.Usage.Billing || gotEv.PhysicalCall.ErrorCode != wantEv.PhysicalCall.ErrorCode {
		t.Fatalf("stream evidence %+v / %+v differs from json %+v / %+v", gotEv.Output.Usage, gotEv.PhysicalCall, wantEv.Output.Usage, wantEv.PhysicalCall)
	}
	var sent map[string]any
	if err := json.Unmarshal(streamed.transport.bodies[1], &sent); err != nil || sent["stream"] != true {
		t.Fatalf("model step body %s, want stream:true", streamed.transport.bodies[1])
	}
	sent = nil
	if err := json.Unmarshal(plain.transport.bodies[1], &sent); err != nil || sent["stream"] == true {
		t.Fatalf("unrouted model step body %s, want no stream request", plain.transport.bodies[1])
	}
	if last := hub.last(); last.state != "generated" || last.text != "Hello there. More." {
		t.Fatalf("final preview = %+v", last)
	}
	staged := gotEv.StagedOutputs[len(gotEv.StagedOutputs)-1]
	if staged.Purpose != "provider_response" || !strings.HasPrefix(staged.MediaType, "text/event-stream") ||
		!strings.Contains(string(streamed.blobs.stagedBytes(t, staged.StagingRef)), "response.function_call_arguments.delta") {
		t.Fatalf("staged response %+v, want the exact event stream", staged)
	}
}
