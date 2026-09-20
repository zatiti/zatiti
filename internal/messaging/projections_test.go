package messaging

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Revision 3: the caller_unread_count/caller_last_read_marker projection on
// conversation.get/.list, and exact meaningful-activity ordering -- routine
// agent coordination must not reorder human chats or mark them unread.

// conversationGet runs conversation.get and returns the decoded resource.
func (e *testEnv) conversationGet(id contract.ID) *wireConversation {
	e.t.Helper()
	payload := e.mustOK("conversation.get", map[string]any{"scope": e.scope, "id": id})
	var out struct {
		Resource wireConversation `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return &out.Resource
}

// TestConversationUnreadAndOrderIgnoreQuietWorkerChatter proves the
// caller_unread_count/caller_last_read_marker projection and the
// last_meaningful_event ordering agree on the same rule: a meaningful send
// both reorders the conversation and counts as unread for its recipient,
// while quiet routine worker coordination does neither.
func TestConversationUnreadAndOrderIgnoreQuietWorkerChatter(t *testing.T) {
	env := newEnv(t)
	env.asOwner()
	conv := env.createGroup(env.owner, env.chief, env.worker)

	// A freshly created conversation starts with no unread and no marker for
	// a participant who has not sent or read anything yet.
	env.asChief()
	got := env.conversationGet(conv)
	if got.CallerUnreadCount != 0 || got.CallerLastReadMarker != "" {
		t.Fatalf("fresh conversation projection %+v, want zero unread and no marker", got)
	}

	// Quiet worker coordination: never meaningful, so it must not reorder
	// the chat and must not become unread for anyone.
	env.asWorker()
	env.sendToConversation(conv, env.ids.New(), "progress note", nil, nil)
	if row := env.readConversation(conv); row.LastMeaningfulEvent != "" {
		t.Fatalf("quiet worker message reordered the conversation: %s", row.LastMeaningfulEvent)
	}
	env.asChief()
	got = env.conversationGet(conv)
	if got.CallerUnreadCount != 0 {
		t.Fatalf("quiet worker message marked the chief unread: %+v", got)
	}
	items, _ := env.listConversationsPage(map[string]any{"scope": env.scope})
	for _, c := range items {
		if c.ID == conv && c.CallerUnreadCount != 0 {
			t.Fatalf("conversation.list unread projection %+v drifted for quiet chatter", c)
		}
	}

	// A meaningful human send does the opposite: it reorders the chat and
	// becomes unread for every other current participant. The quiet worker
	// message above also admitted a (non-meaningful) recipient row for the
	// chief, so the chief's admitted inbox holds two rows here; unread only
	// counts the meaningful one.
	env.asOwner()
	meaningful := env.sendToConversation(conv, env.ids.New(), "please review this", nil, nil)
	if row := env.readConversation(conv); row.LastMeaningfulEvent == "" {
		t.Fatalf("meaningful human message never reordered the conversation")
	}
	env.asChief()
	got = env.conversationGet(conv)
	if got.CallerUnreadCount != 1 {
		t.Fatalf("chief unread count %d after one meaningful send, want 1", got.CallerUnreadCount)
	}

	// Reading it (ack) clears the unread count and stamps the marker.
	env.ack(meaningful.ID, env.chief, int64(meaningful.Version))
	got = env.conversationGet(conv)
	if got.CallerUnreadCount != 0 {
		t.Fatalf("acknowledged message left unread count %d, want 0", got.CallerUnreadCount)
	}
	if got.CallerLastReadMarker == "" {
		t.Fatalf("acknowledging never stamped caller_last_read_marker")
	}
}
