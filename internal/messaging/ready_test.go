package messaging

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Revision 3: ready-recipient discovery and atomic turn admission/ack
// fences, keyed by message ID plus recipient ID.

// TestMessagingReadyProcessedLostAckAdmitsOneTurn drives the Z20-style lost
// acknowledgement/restart scenario end to end through _messaging.ready and
// _messaging.processed: a crashed caller retrying the exact same turn_id
// after a successful commit must never admit a second turn or fire a second
// acknowledgement, and a different turn_id for an already-linked pair is a
// surfaced conflict, never a silently accepted second link.
func TestMessagingReadyProcessedLostAckAdmitsOneTurn(t *testing.T) {
	env := newEnv(t)
	env.asOwner()
	conv := env.createGroup(env.owner, env.worker)
	messageID := env.ids.New()
	env.sendToConversation(conv, messageID, "assignment", nil, nil)

	// The admitted message is ready for turn admission.
	ready := env.readyItems(10)
	found := false
	for _, m := range ready {
		if m.ID == messageID {
			found = true
		}
	}
	if !found {
		t.Fatalf("newly admitted message missing from _messaging.ready: %+v", ready)
	}

	turnID := env.ids.New()
	before := len(env.events())
	first := env.processed(messageID, env.worker, turnID, nil)
	if first.State != messageStateAcknowledged {
		t.Fatalf("processed message state %q, want acknowledged (sole recipient)", first.State)
	}
	if rec := env.readRecipient(messageID, env.worker); rec.State != recipientAcknowledged {
		t.Fatalf("recipient row state %q, want acknowledged after processing", rec.State)
	}
	afterFirst := len(env.events())
	if afterFirst <= before {
		t.Fatalf("processed admission emitted no events")
	}

	// A crashed caller retrying the identical (message, recipient, turn_id)
	// is idempotent: same version, no second acknowledgement or turn event.
	retry := env.processed(messageID, env.worker, turnID, nil)
	if retry.Version != first.Version || retry.State != first.State {
		t.Fatalf("lost-ack retry changed state %s/%d, want %s/%d",
			retry.State, retry.Version, first.State, first.Version)
	}
	if got := len(env.events()); got != afterFirst {
		t.Fatalf("lost-ack retry emitted %d new events, want 0", got-afterFirst)
	}

	// The processed message no longer appears in a fresh ready scan.
	for _, m := range env.readyItems(10) {
		if m.ID == messageID {
			t.Fatalf("fully processed message stayed in _messaging.ready: %s", m.ID)
		}
	}

	// A different turn_id reported for the same already-linked pair is a
	// genuine conflict: the durable link is never silently overwritten.
	_ = env.expectFault("_messaging.processed", map[string]any{
		"message_id": messageID, "recipient_id": env.worker, "turn_id": env.ids.New(),
	}, contract.CodeSubmissionConflict)

	// The worker's reply is an ordinary durable, deduped send: a lost-ack
	// restart that resubmits the identical reply id delivers exactly once.
	env.asWorker()
	replyID := env.ids.New()
	reply1 := env.sendToConversation(conv, replyID, "done", nil, nil)
	reply2 := env.sendToConversation(conv, replyID, "done", nil, nil)
	if reply1.ID != reply2.ID || reply1.Version != reply2.Version {
		t.Fatalf("reply retry changed identity %s/%d -> %s/%d",
			reply1.ID, reply1.Version, reply2.ID, reply2.Version)
	}
	replies := 0
	for _, row := range recipientRows(env, env.owner) {
		if row.ID == replyID {
			replies++
		}
	}
	if replies != 1 {
		t.Fatalf("owner inbox holds %d copies of the reply, want exactly 1", replies)
	}
}

// TestMessagingReadyMultipleRecipientsEachGetOwnDelivery confirms message ID
// plus recipient ID is the real deduplication identity: one recipient's
// turn admission leaves a sibling recipient's own delivery, and the ready
// scan itself, untouched until it is separately processed.
func TestMessagingReadyMultipleRecipientsEachGetOwnDelivery(t *testing.T) {
	env := newEnv(t)
	env.asOwner()
	conv := env.createGroup(env.owner, env.worker, env.third)
	messageID := env.ids.New()
	env.sendToConversation(conv, messageID, "assignment", nil, nil)

	if rec := env.readRecipient(messageID, env.worker); rec == nil || rec.State != recipientAdmitted {
		t.Fatalf("worker recipient row missing or not admitted: %+v", rec)
	}
	if rec := env.readRecipient(messageID, env.third); rec == nil || rec.State != recipientAdmitted {
		t.Fatalf("third recipient row missing or not admitted: %+v", rec)
	}

	turnA := env.ids.New()
	afterWorker := env.processed(messageID, env.worker, turnA, nil)
	if afterWorker.State != messageStateAdmitted {
		t.Fatalf("message state %q after one of two recipients processed, want still admitted", afterWorker.State)
	}
	if rec := env.readRecipient(messageID, env.worker); rec.State != recipientAcknowledged {
		t.Fatalf("worker recipient row %q, want acknowledged", rec.State)
	}
	if rec := env.readRecipient(messageID, env.third); rec.State != recipientAdmitted {
		t.Fatalf("processing the worker's delivery touched third's own delivery: %q", rec.State)
	}

	// The message stays in the ready backlog: third is still unprocessed.
	stillReady := false
	for _, m := range env.readyItems(10) {
		if m.ID == messageID {
			stillReady = true
		}
	}
	if !stillReady {
		t.Fatalf("partially processed message left the ready backlog early")
	}

	// Third's own turn is independent: its own turn id, its own link.
	turnB := env.ids.New()
	final := env.processed(messageID, env.third, turnB, nil)
	if final.State != messageStateAcknowledged {
		t.Fatalf("message state %q after both recipients processed, want acknowledged", final.State)
	}
	for _, m := range env.readyItems(10) {
		if m.ID == messageID {
			t.Fatalf("fully processed message stayed in the ready backlog")
		}
	}
}

// TestMessagingProcessedRequiresAddressedRecipient refuses turn admission
// for a recipient the message was never actually delivered to: the durable
// link identity is scoped to real inbox admission, not an asserted pair.
func TestMessagingProcessedRequiresAddressedRecipient(t *testing.T) {
	env := newEnv(t)
	env.asOwner()

	_ = env.expectFault("_messaging.processed", map[string]any{
		"message_id": env.ids.New(), "recipient_id": env.worker, "turn_id": env.ids.New(),
	}, contract.CodeNotFound)

	conv := env.createGroup(env.owner, env.chief)
	messageID := env.ids.New()
	env.sendToConversation(conv, messageID, "hi", nil, nil)
	// worker was never a recipient of this conversation's message.
	_ = env.expectFault("_messaging.processed", map[string]any{
		"message_id": messageID, "recipient_id": env.worker, "turn_id": env.ids.New(),
	}, contract.CodeNotFound)
}
