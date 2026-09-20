package messaging

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Revision 3: conversation.message.list -- stable paging, sender-inclusive
// history, current participant checks and the history-disclosure boundary
// for newly joined participants.

// TestConversationMessageListReopenedDesktopSeesSentAndReceived proves a
// reopened desktop reconstructs a conversation from durable state alone: no
// window/session memory, both the caller's own sent messages (which carry
// no recipient row for the sender) and the messages it received.
func TestConversationMessageListReopenedDesktopSeesSentAndReceived(t *testing.T) {
	env := newEnv(t)
	env.asOwner()
	conv := env.createGroup(env.owner, env.chief)
	sent := env.sendToConversation(conv, env.ids.New(), "hello", nil, nil).ID
	env.asChief()
	received := env.sendToConversation(conv, env.ids.New(), "hi back", nil, nil).ID
	env.asOwner()
	third := env.sendToConversation(conv, env.ids.New(), "anything else?", nil, nil).ID

	// The owner "reopens the desktop": a fresh call with no cursor sees the
	// message it sent, the message it received, and its own follow-up.
	items, next := env.messageListPage(conv, nil)
	if next != "" {
		t.Fatalf("unexpected next_cursor on an unpaged result: %q", next)
	}
	ids := map[contract.ID]bool{}
	for _, m := range items {
		ids[m.ID] = true
	}
	if !ids[sent] || !ids[received] || !ids[third] {
		t.Fatalf("reopened history %v missing one of sent=%s received=%s third=%s", ids, sent, received, third)
	}

	// Paging is stable: a bounded first page plus its cursor covers the
	// remainder without ever repeating or dropping a row. The last page
	// returns fewer than the limit, matching every other list operation's
	// end-of-page convention.
	page1, cursor := env.messageListPage(conv, map[string]any{"limit": int64(2)})
	if len(page1) != 2 || cursor == "" {
		t.Fatalf("first page %d items, cursor %q", len(page1), cursor)
	}
	page2, cursor2 := env.messageListPage(conv, map[string]any{"limit": int64(2), "cursor": cursor})
	if len(page2) != 1 || cursor2 != "" {
		t.Fatalf("second page %d items, cursor %q, want 1 item and no further cursor", len(page2), cursor2)
	}
	seen := map[contract.ID]bool{}
	for _, m := range append(append([]*wireMessage{}, page1...), page2...) {
		if seen[m.ID] {
			t.Fatalf("paged history repeated message %s", m.ID)
		}
		seen[m.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("paged history covered %d distinct messages, want 3", len(seen))
	}
}

// TestConversationMessageListDisclosureBoundaryForNewParticipant confirms
// conversation.message.list obeys the same no-retroactive-disclosure
// boundary as the mailbox: a participant added after messages were sent
// reads none of that earlier history, while a current participant continues
// to see it in full.
func TestConversationMessageListDisclosureBoundaryForNewParticipant(t *testing.T) {
	env := newEnv(t)
	env.asOwner()
	conv := env.createGroup(env.owner, env.chief)
	env.sendToConversation(conv, env.ids.New(), "before joining", nil, nil)

	env.mustOK("conversation.update", map[string]any{
		"scope": env.scope, "id": conv, "expected_version": int64(1),
		"participant_ids": []contract.ID{env.owner, env.chief, env.worker},
	})

	env.asWorker()
	items, _ := env.messageListPage(conv, nil)
	if len(items) != 0 {
		t.Fatalf("newly joined participant read %d undisclosed historical messages", len(items))
	}

	// A message sent after joining is visible to the new participant.
	env.asOwner()
	after := env.sendToConversation(conv, env.ids.New(), "after joining", nil, nil).ID
	env.asWorker()
	items, _ = env.messageListPage(conv, nil)
	if len(items) != 1 || items[0].ID != after {
		t.Fatalf("post-join history %+v, want exactly the post-join message", items)
	}

	// The current participant fence still applies: an outsider is refused.
	env.asThird()
	_ = env.expectFault("conversation.message.list", map[string]any{
		"scope": env.scope, "conversation_id": conv,
	}, contract.CodePermissionDenied)
}
