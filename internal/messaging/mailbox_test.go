package messaging

import (
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Mailbox behavior: receipt, read marker and the acknowledged transition.

func TestMailboxAckPrincipalFence(t *testing.T) {
	env := newEnv(t)
	conv := env.createGroup(env.owner, env.third)
	messageID := env.sendToConversation(conv, env.ids.New(), "ack me", nil, nil).ID

	// Only the recipient may acknowledge: the owner addressing the message
	// with third as the claimed recipient is permission_denied, never an
	// acknowledged row.
	env.asOwner()
	_ = env.expectFault("mailbox.ack", map[string]any{
		"scope": env.scope, "message_id": messageID, "recipient_id": env.third,
		"expected_version": int64(1),
	}, contract.CodePermissionDenied)

	// The real recipient acknowledges: the one inbox row moves to
	// acknowledged, the message transitions, and the receipt lands.
	env.asThird()
	wire := env.ack(messageID, env.third, 1)
	if wire.State != messageStateAcknowledged || wire.Version != 2 {
		t.Fatalf("acknowledged message state/version %s/%d, want acknowledged/2", wire.State, wire.Version)
	}
}

func TestMailboxAckRefusals(t *testing.T) {
	env := newEnv(t)
	env.createGroup(env.owner, env.chief)
	env.asChief()

	// Absent message is not_found.
	_ = env.expectFault("mailbox.ack", map[string]any{
		"scope": env.scope, "message_id": env.ids.New(), "recipient_id": env.chief,
		"expected_version": int64(1),
	}, contract.CodeNotFound)

	// A delivered message outside the chief's inbox is not_found, never an
	// acknowledged row.
	env.asThird()
	other := env.mailboxSend(env.ids.New(), env.owner, "owner mail", nil, nil)
	env.asChief()
	_ = env.expectFault("mailbox.ack", map[string]any{
		"scope": env.scope, "message_id": other.ID, "recipient_id": env.chief,
		"expected_version": int64(1),
	}, contract.CodeNotFound)
}

func TestMailboxAckStaleVersion(t *testing.T) {
	env := newEnv(t)
	conv := env.createGroup(env.owner, env.chief, env.third)
	messageID := env.sendToConversation(conv, env.ids.New(), "ack me", nil, nil).ID

	// The chief claims version 99: stale_version, never an acknowledged row.
	env.asThird()
	_ = env.expectFault("mailbox.ack", map[string]any{
		"scope": env.scope, "message_id": messageID, "recipient_id": env.third,
		"expected_version": int64(99),
	}, contract.CodeStaleVersion)
}

func TestMailboxAckIdempotentReplay(t *testing.T) {
	env := newEnv(t)
	conv := env.createGroup(env.owner, env.chief)
	messageID := env.sendToConversation(conv, env.ids.New(), "ack me", nil, nil).ID

	// Replay with the current version is completed: acknowledged state, no
	// second receipt or read marker, never a re-transition.
	env.asChief()
	first := env.ack(messageID, env.chief, 1)
	second := env.ack(messageID, env.chief, 2)
	if second.State != messageStateAcknowledged || second.Version != 2 {
		t.Fatalf("replayed message state/version %s/%d, want acknowledged/2", second.State, second.Version)
	}
	if first.Version != second.Version {
		t.Fatalf("replay changed the version %d -> %d", first.Version, second.Version)
	}
}

func TestMailboxAckLastRecipientTransition(t *testing.T) {
	env := newEnv(t)
	conv := env.createGroup(env.owner, env.chief, env.third)
	messageID := env.sendToConversation(conv, env.ids.New(), "last ack", nil, nil).ID

	// With one recipient unacknowledged the message stays admitted.
	env.asChief()
	first := env.ack(messageID, env.chief, 1)
	if first.State != messageStateAdmitted || first.Version != 1 {
		t.Fatalf("partial ack state/version %s/%d, want admitted/1", first.State, first.Version)
	}

	// The last recipient acknowledging transitions the message: acknowledged
	// with a version bump and the acknowledged event, receipt on the row.
	env.asThird()
	last := env.ack(messageID, env.third, 1)
	if last.State != messageStateAcknowledged || last.Version != 2 {
		t.Fatalf("last ack state/version %s/%d, want acknowledged/2", last.State, last.Version)
	}
	events := env.events()
	found := false
	for _, ev := range events {
		if ev.Kind == eventMessageAcknowledged && ev.ResourceID == messageID {
			found = true
		}
	}
	if !found {
		t.Fatalf("acknowledged event missing for %s over %d events", messageID, len(events))
	}
	// The acknowledgment stamps the recipient's read marker.
	if marker, ok := env.readMarker(conv, env.third); !ok || marker != messageID {
		t.Fatalf("third read marker %s ok=%v, want %s", marker, ok, messageID)
	}
}

func TestMailboxListPrivacyFence(t *testing.T) {
	env := newEnv(t)
	env.createGroup(env.owner, env.chief)
	env.asChief()

	// recipient_id is required: absent recipient is invalid input.
	_ = env.expectFault("mailbox.list", map[string]any{"scope": env.scope},
		contract.CodeInvalidInput)

	// Mail is private per principal: listing another's inbox is
	// permission_denied even with the recipient named in the input.
	_ = env.expectFault("mailbox.list", map[string]any{
		"scope": env.scope, "recipient_id": env.owner,
	}, contract.CodePermissionDenied)
}

func TestMailboxListFilters(t *testing.T) {
	env := newEnv(t)
	env.createGroup(env.owner, env.chief)
	env.asOwner()
	first := env.mailboxSend(env.ids.New(), env.chief, "one", nil, nil)
	second := env.mailboxSend(env.ids.New(), env.chief, "two", nil, []contract.ID{env.ids.New()})
	env.asChief()

	// The unfiltered inbox lists both admitted rows.
	items, _ := env.listMailboxPage(env.chief, map[string]any{})
	if len(items) != 2 {
		t.Fatalf("inbox items %d, want 2", len(items))
	}

	// The state filter separates unacknowledged from acknowledged mail.
	env.ack(first.ID, env.chief, 1)
	items, _ = env.listMailboxPage(env.chief, map[string]any{
		"filter": map[string]any{"state": recipientAdmitted},
	})
	if len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("admitted filter items %+v, want only the second message", items)
	}
	items, _ = env.listMailboxPage(env.chief, map[string]any{
		"filter": map[string]any{"state": recipientAcknowledged},
	})
	if len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("acknowledged filter items %+v, want only the first message", items)
	}

	// The task filter narrows to the one message carrying the reference.
	items, _ = env.listMailboxPage(env.chief, map[string]any{
		"filter": map[string]any{"task_id": second.TaskIDs[0]},
	})
	if len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("task filter items %+v, want only the second message", items)
	}

	// The organization filter narrows to one organization's mail: the
	// messages carry no organization, so a foreign organization lists none.
	items, _ = env.listMailboxPage(env.chief, map[string]any{
		"filter": map[string]any{"organization_id": env.ids.New()},
	})
	if len(items) != 0 {
		t.Fatalf("foreign organization items %+v, want 0", items)
	}
}

func TestMailboxListUnsupportedFilters(t *testing.T) {
	env := newEnv(t)
	env.createGroup(env.owner, env.chief)
	env.asChief()
	for name, value := range map[string]any{
		"key":         "wake",
		"needs_you":   true,
		"parent_id":   env.ids.New(),
		"descendants": true,
	} {
		_ = env.expectFault("mailbox.list", map[string]any{
			"scope": env.scope, "recipient_id": env.chief,
			"filter": map[string]any{name: value},
		}, contract.CodeInvalidInput)
	}
}

func TestMailboxListCursorBinding(t *testing.T) {
	env := newEnv(t)
	env.createGroup(env.owner, env.chief)
	env.asOwner()
	env.mailboxSend(env.ids.New(), env.chief, "one", nil, nil)
	env.mailboxSend(env.ids.New(), env.chief, "two", nil, nil)
	env.mailboxSend(env.ids.New(), env.chief, "three", nil, nil)
	env.asChief()

	page1, next := env.listMailboxPage(env.chief, map[string]any{"limit": int64(2)})
	if len(page1) != 2 || next == "" {
		t.Fatalf("first page items %d next %q", len(page1), next)
	}
	page2, next2 := env.listMailboxPage(env.chief, map[string]any{
		"limit": int64(2), "cursor": next,
	})
	if len(page2) != 1 || next2 != "" {
		t.Fatalf("second page items %d next %q", len(page2), next2)
	}

	// A cursor minted for one filter fails under another: expired, never
	// replayed, and the refusal names the fresh snapshot.
	f := env.expectFault("mailbox.list", map[string]any{
		"scope": env.scope, "recipient_id": env.chief, "limit": int64(2),
		"cursor": next, "filter": map[string]any{"state": recipientAdmitted},
	}, contract.CodeCursorExpired)
	if !strings.Contains(string(f.Details), "snapshot_required") {
		t.Fatalf("cursor_expired details %s missing snapshot_required", f.Details)
	}
	// A forged signature is expired, never replayed.
	_ = env.expectFault("mailbox.list", map[string]any{
		"scope": env.scope, "recipient_id": env.chief, "limit": int64(2),
		"cursor": `{"offset":2,"sig":"deadbeef"}`,
	}, contract.CodeCursorExpired)
}
