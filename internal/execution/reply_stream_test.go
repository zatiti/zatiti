package execution

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/narrate-it/narrate/narration"
	"github.com/zatiti/zatiti/internal/contract"
)

// routeRecorder is a ReplyStreams that only records context registrations.
type routeRecorder struct {
	routes map[contract.Digest]contract.ReplyRoute
}

func (r *routeRecorder) Register(d contract.Digest, route contract.ReplyRoute) { r.routes[d] = route }
func (r *routeRecorder) Commit(contract.ID, string)                            {}
func (r *routeRecorder) Route(d contract.Digest) (contract.ReplyRoute, bool) {
	route, ok := r.routes[d]
	return route, ok
}
func (r *routeRecorder) Publish(contract.ReplyRoute, string, string, string) {}
func (r *routeRecorder) Watch(contract.ID, contract.ID, contract.ID) (<-chan struct{}, func()) {
	return nil, func() {}
}
func (r *routeRecorder) Snapshot(contract.ID, contract.ID, contract.ID) []contract.ReplyPreview {
	return nil
}

// performConversationContext admits a message-triggered turn in a
// conversation, prepares its context and performs it as the controller
// would, returning the turn and the exact context bytes.
func performConversationContext(t *testing.T, e *testEnv) (*turnRow, contract.ID, json.RawMessage) {
	t.Helper()
	worker := e.ids.New()
	profile := fixtureHostedProfile(worker)
	e.installWorkerSnapshot(worker, profile)
	e.ports.setMessages(worker, []wireMessage{{
		ID: e.ids.New(), Version: 1, SenderID: e.ids.New(), RecipientIDs: []contract.ID{worker},
		Scope: e.scope, TaskIDs: []contract.ID{}, Body: "tell me about it",
		Attachments: []wireArtifactRef{}, State: "admitted", CreatedAt: "2026-09-10T12:00:00.000000000Z",
	}})
	turn := e.admitClaimedTurn(worker)
	conversation := e.ids.New()
	e.inWrite(func(u contract.Unit) error {
		_, err := u.ExecContext(e.ctx, `UPDATE execution_turns SET conversation_id = ? WHERE id = ?`, string(conversation), string(turn.ID))
		return err
	})
	turn = e.readTurn(turn.ID)
	payload := e.mustOK(opContextPrepare, contextPrepareInput{TurnID: turn.ID, ExpectedVersion: turn.Version, Generation: turn.Generation})
	var body contextPlanBody
	e.decode(payload.Data, &body)
	plan := body.Resource
	refs := make([]contract.ArtifactRef, len(plan.Refs))
	for i, r := range plan.Refs {
		refs[i] = contract.ArtifactRef{ID: r.ID, Digest: r.Digest}
	}
	raw, err := e.svc.PerformContext(e.ctx, contract.ContextPlan{
		ID: plan.ID, TurnID: plan.TurnID, ExpectedVersion: plan.ExpectedVersion, Generation: contract.Version(plan.Generation),
		Scope: plan.Scope, AttemptID: plan.AttemptID, Refs: refs, ConfigurationRevision: plan.ConfigurationRevision,
		ByteBound: plan.ByteBound, TokenBound: plan.TokenBound, Recipe: plan.Recipe,
	})
	if err != nil {
		t.Fatalf("PerformContext: %v", err)
	}
	return turn, conversation, raw
}

func developerTexts(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var doc wireContextArtifact
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	var b strings.Builder
	for _, m := range doc.Messages {
		if m.Role != "developer" {
			continue
		}
		for _, part := range m.Parts {
			b.Write(part)
		}
	}
	return b.String()
}

// In a voice session, Narrate's craft is pinned into the persisted model
// context itself, and the context's digest routes reply previews to the
// requester's voice session.
func TestVoiceSessionPinsNarrateCraftAndRoutesPreviews(t *testing.T) {
	e := newEnv(t)
	hub := &routeRecorder{routes: map[contract.Digest]contract.ReplyRoute{}}
	e.svc.deps.Streams = hub
	session := e.ids.New()
	e.ports.voiceCraft = map[string]string{"style": "conversational", "session_id": string(session)}

	turn, conversation, raw := performConversationContext(t, e)

	craft, err := narration.Assemble("conversational")
	if err != nil {
		t.Fatal(err)
	}
	text := developerTexts(t, raw)
	firstLine := strings.SplitN(craft, "\n", 2)[0]
	marshaled, _ := json.Marshal(firstLine)
	if !strings.Contains(text, strings.Trim(string(marshaled), `"`)) || !strings.Contains(text, "spoken script") {
		t.Fatalf("developer instructions do not carry the Narrate craft and spoken-script rule: %s", text)
	}
	route, ok := hub.Route(contract.Hash(raw))
	if !ok {
		t.Fatal("performed context was not registered for reply previews")
	}
	want := contract.ReplyRoute{Source: turn.Source.SourceID, Scope: turn.Scope, Recipient: turn.RequesterID,
		Conversation: conversation, Worker: turn.WorkerID, Turn: turn.ID, VoiceSession: session}
	if route != want {
		t.Fatalf("route = %+v, want %+v", route, want)
	}
	var craftIn struct {
		Recipient contract.ID `json:"recipient_id"`
	}
	if len(e.ports.crafts) != 1 || json.Unmarshal(e.ports.crafts[0], &craftIn) != nil || craftIn.Recipient != turn.RequesterID {
		t.Fatalf("_voice.craft calls = %s, want one for the requester", e.ports.crafts)
	}
}

func TestTextConversationStreamsWithoutVoiceCraft(t *testing.T) {
	e := newEnv(t)
	hub := &routeRecorder{routes: map[contract.Digest]contract.ReplyRoute{}}
	e.svc.deps.Streams = hub
	_, _, raw := performConversationContext(t, e)
	if strings.Contains(developerTexts(t, raw), "spoken script") {
		t.Fatal("a text conversation received voice craft instructions")
	}
	route, ok := hub.Route(contract.Hash(raw))
	if !ok || route.VoiceSession != "" {
		t.Fatalf("route = %+v, %v; want a text-only preview route", route, ok)
	}
}

func TestNoReplyStreamsMeansNoCraftLookup(t *testing.T) {
	e := newEnv(t)
	performConversationContext(t, e)
	if len(e.ports.crafts) != 0 {
		t.Fatalf("_voice.craft called %d times without a reply stream hub", len(e.ports.crafts))
	}
}
