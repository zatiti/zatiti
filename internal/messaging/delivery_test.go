package messaging

import (
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// The delivery pipeline: disclosure gating, durable admission, identity
// dedup, artifact validation and the meaningful/quiet projection.

func TestDisclosureGateChecksPolicy(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief)
	env.asOwner()
	env.sendToConversation(conv, env.ids.New(), "hello", nil, nil)

	calls := env.ports.policyCalls()
	if len(calls) != 1 {
		t.Fatalf("policy calls %d, want exactly one per delivery", len(calls))
	}
	if calls[0].Capability != disclosureCapability {
		t.Fatalf("policy capability %q, want %q", calls[0].Capability, disclosureCapability)
	}
	if calls[0].CandidateDigest == "" {
		t.Fatalf("policy check carried no candidate digest")
	}
}

func TestDisclosureGateDurablyAdmits(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief)
	env.asOwner()
	messageID := env.ids.New()
	wire := env.sendToConversation(conv, messageID, "hello", nil, nil)

	row := env.readMessage(messageID)
	if row.State != messageStateAdmitted || row.Version != 1 {
		t.Fatalf("message row drifted: %+v", row)
	}
	if row.ConversationID != conv || !row.Meaningful {
		t.Fatalf("human delivery drifted: %+v", row)
	}
	if len(wire.RecipientIDs) != 1 || wire.RecipientIDs[0] != env.chief {
		t.Fatalf("recipient derivation drifted: %+v", wire.RecipientIDs)
	}
	// The recipient inbox row is durable in the same transaction.
	if r := env.readRecipient(messageID, env.chief); r == nil || r.State != recipientAdmitted {
		t.Fatalf("recipient row drifted: %+v", r)
	}
	if env.readConversation(conv).LastMeaningfulEvent == "" {
		t.Fatalf("meaningful message never surfaced in the chat")
	}
	// The sender's own read marker advanced.
	if marker, ok := env.readMarker(conv, env.owner); !ok || marker != messageID {
		t.Fatalf("sender read marker %s/%v, want %s", marker, ok, messageID)
	}
	found := false
	for _, ev := range env.events() {
		if ev.Kind == eventMessageAdmitted && ev.ResourceID == messageID {
			found = true
		}
	}
	if !found {
		t.Fatalf("admitted event missing: %+v", env.events())
	}
}

func TestDeliveryClientAgentMeaningful(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief)

	// A client-agent send under its owner identity is a meaningful
	// human-facing event: it surfaces in the chat like a human send.
	env.asClientAgent()
	messageID := env.ids.New()
	env.sendToConversation(conv, messageID, "agent note", nil, nil)
	if row := env.readMessage(messageID); !row.Meaningful {
		t.Fatalf("client-agent delivery drifted: %+v", row)
	}
	if row := env.readConversation(conv); row.LastMeaningfulEvent == "" {
		t.Fatalf("client-agent send never surfaced in the chat")
	}
}

func TestDisclosureDedupDurablyAdmits(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief)
	env.asOwner()
	messageID := env.ids.New()
	env.sendToConversation(conv, messageID, "hello", nil, nil)
	callsBefore := len(env.ports.callsOf("_policy.check"))

	// The same content redelivered under the same id is idempotent.
	again := env.sendToConversation(conv, messageID, "hello", nil, nil)
	if again.ID != messageID || again.Version != 1 {
		t.Fatalf("redelivery drifted: %+v", again)
	}
	if calls := len(env.ports.callsOf("_policy.check")); calls != callsBefore {
		t.Fatalf("redelivery re-authorized content")
	}
	// The same id with different content is a surfaced conflict.
	_ = env.expectFault("conversation.message.send", map[string]any{
		"scope": env.scope, "conversation_id": conv, "message_id": messageID,
		"body": "changed", "attachments": []wireArtifactRef{}, "task_ids": []contract.ID{},
	}, contract.CodeSubmissionConflict)
	// Recipient order does not change content identity.
	conv2 := env.createGroup(env.owner, env.chief, env.worker)
	env.mustOK("conversation.message.send", map[string]any{
		"scope": env.scope, "conversation_id": conv2, "message_id": env.ids.New(),
		"body": "x", "attachments": []wireArtifactRef{},
		"task_ids": []contract.ID{},
	})
}

func TestDeliveryPolicyDecisions(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief)
	env.asOwner()

	for _, tc := range []struct {
		decision string
		code     string
	}{
		{"deny", contract.CodePermissionDenied},
		{"review", contract.CodeReviewRequired},
		{"prerequisite_missing", contract.CodePrerequisiteMissing},
		{"invented", contract.CodePermissionDenied},
	} {
		env.ports.setPolicy(tc.decision, "fixture reason")
		_ = env.expectFault("conversation.message.send", map[string]any{
			"scope": env.scope, "conversation_id": conv, "message_id": env.ids.New(),
			"body": "x", "attachments": []wireArtifactRef{}, "task_ids": []contract.ID{},
		}, tc.code)
	}
	env.ports.setPolicy("allow")
	// Reasons render into fault messages without changing their content.
	env.ports.setPolicy("deny", "no disclosure in scope")
	f := env.expectFault("conversation.message.send", map[string]any{
		"scope": env.scope, "conversation_id": conv, "message_id": env.ids.New(),
		"body": "x", "attachments": []wireArtifactRef{}, "task_ids": []contract.ID{},
	}, contract.CodePermissionDenied)
	if !strings.Contains(f.Message, "no disclosure in scope") {
		t.Fatalf("fault message %q drops the peer reasons", f.Message)
	}
	env.ports.setPolicy("allow")

	// A peer fault passes through with its own code.
	env.setPeerFault("_policy.check", &contract.Fault{
		Code: contract.CodeInternalError, Message: "policy owner unavailable",
	})
	_ = env.expectFault("conversation.message.send", map[string]any{
		"scope": env.scope, "conversation_id": conv, "message_id": env.ids.New(),
		"body": "x", "attachments": []wireArtifactRef{}, "task_ids": []contract.ID{},
	}, contract.CodeInternalError)
	env.setPeerFault("_policy.check", nil)
}

func TestDeliveryArtifacts(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief)
	env.asOwner()
	ref := env.artifactFixture("report")

	// A pinned available artifact delivers.
	env.sendToConversation(conv, env.ids.New(), "with report", []wireArtifactRef{ref}, nil)

	// A digest mismatch with the pinned reference is invalid input.
	tampered := ref
	tampered.Digest = contract.Digest(digestFixture("tampered"))
	_ = env.expectFault("conversation.message.send", map[string]any{
		"scope": env.scope, "conversation_id": conv, "message_id": env.ids.New(),
		"body": "x", "attachments": []wireArtifactRef{tampered}, "task_ids": []contract.ID{},
	}, contract.CodeInvalidInput)

	// An artifact in a foreign installation is permission denied.
	env.ports.registerArtifact(ref.ID, wireScope{InstallationID: env.ids.New()},
		string(ref.Digest), "available")
	_ = env.expectFault("conversation.message.send", map[string]any{
		"scope": env.scope, "conversation_id": conv, "message_id": env.ids.New(),
		"body": "x", "attachments": []wireArtifactRef{ref}, "task_ids": []contract.ID{},
	}, contract.CodePermissionDenied)

	// A faulted artifact is refused.
	env.ports.registerArtifact(ref.ID, env.scope, string(ref.Digest), "fault")
	_ = env.expectFault("conversation.message.send", map[string]any{
		"scope": env.scope, "conversation_id": conv, "message_id": env.ids.New(),
		"body": "x", "attachments": []wireArtifactRef{ref}, "task_ids": []contract.ID{},
	}, contract.CodeArtifactFault)
}

func TestDeliveryMembershipFences(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief)
	env.asOwner()

	// The sender must participate.
	env.asThird()
	_ = env.expectFault("conversation.message.send", map[string]any{
		"scope": env.scope, "conversation_id": conv, "message_id": env.ids.New(),
		"body": "x", "attachments": []wireArtifactRef{}, "task_ids": []contract.ID{},
	}, contract.CodePermissionDenied)

	// A message with no recipients besides the sender is invalid: the owner
	// addressing only themselves leaves no derived recipient.
	env.asOwner()
	_ = env.expectFault("mailbox.send", map[string]any{
		"scope": env.scope, "message_id": env.ids.New(), "recipient_id": env.owner,
		"body": "x", "attachments": []wireArtifactRef{}, "task_ids": []contract.ID{},
	}, contract.CodeInvalidInput)
}

func TestDeliveryRecipientFence(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief, env.worker)

	// The admit path carries recipients explicitly: a recipient outside the
	// conversation membership is invalid input, never a delivered row. The
	// sender participates, so the membership fence fires on the recipient.
	env.asWorker()
	msg := env.admitInput(env.ids.New(), env.worker, []contract.ID{env.worker, env.owner, env.third}, "x", conv)
	_ = env.expectFault("_messaging.admit", map[string]any{"message": msg}, contract.CodeInvalidInput)
	// The same message addressed only to participants delivers.
	msg.RecipientIDs = []contract.ID{env.worker, env.owner}
	msg.ID = env.ids.New()
	env.admit(msg)
}

func TestDeliveryRetroactiveDisclosure(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief)
	env.asOwner()
	env.sendToConversation(conv, env.ids.New(), "before joining", nil, nil)

	// A newly added participant receives no historical rows.
	env.mustOK("conversation.update", map[string]any{
		"scope": env.scope, "id": conv, "expected_version": int64(1),
		"participant_ids": []contract.ID{env.owner, env.chief, env.worker},
	})
	env.asWorker()
	items, _ := env.listMailboxPage(env.worker, map[string]any{})
	if len(items) != 0 {
		t.Fatalf("joined participant received %d historical rows", len(items))
	}
	payload := env.mustOK("_messaging.pending", map[string]any{
		"worker_id": env.worker, "limit": int64(50),
	})
	var out struct {
		Items []*wireMessage `json:"items"`
	}
	env.decode(payload.Data, &out)
	if len(out.Items) != 0 {
		t.Fatalf("joined participant received %d pending rows", len(out.Items))
	}
}

func TestDeliveryTaskReferencesAreDurableOnly(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief)
	env.asOwner()
	taskID := env.ids.New()
	// Task references ride along; no task owner call may occur.
	env.sendToConversation(conv, env.ids.New(), "references", nil, []contract.ID{taskID})
	if calls := env.ports.callsOf("_tasks.create"); len(calls) != 0 {
		t.Fatalf("delivery invented task owner calls")
	}
	if calls := env.ports.callsOf("task.assign"); len(calls) != 0 {
		t.Fatalf("delivery invented task assignment calls")
	}
	row := env.readMessage(recipientRows(env, env.chief)[0].ID)
	if !containsID(mustIDList(row.TaskIDsJSON), taskID) {
		t.Fatalf("task reference not stored: %s", row.TaskIDsJSON)
	}
}
