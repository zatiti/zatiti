package messaging

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Revision 3: durable worker replies, task links and group attachments,
// with cancellation/clarification correlation carried entirely by the
// structured task_ids field -- never by parsing free text into authority.

// TestMessagingWorkerReplyTaskLinkAndAttachmentDurable covers durable
// worker replies carrying task references (used to correlate a reply,
// cancellation or clarification with the task it concerns) and shared
// attachments, without any of that structure coming from parsing the
// message body: a body that merely contains words like "cancel" carries no
// authority on its own.
func TestMessagingWorkerReplyTaskLinkAndAttachmentDurable(t *testing.T) {
	env := newEnv(t)
	env.asOwner()
	conv := env.createGroup(env.owner, env.chief, env.worker)
	taskID := env.ids.New()
	env.asWorker()
	ref := env.artifactFixture("clarification-attachment")

	replyID := env.ids.New()
	reply := env.sendToConversation(conv, replyID, "cancel? I need clarification on scope",
		[]wireArtifactRef{ref}, []contract.ID{taskID})
	if len(reply.TaskIDs) != 1 || reply.TaskIDs[0] != taskID {
		t.Fatalf("reply task correlation %v, want [%s]", reply.TaskIDs, taskID)
	}
	if len(reply.Attachments) != 1 || reply.Attachments[0].ID != ref.ID {
		t.Fatalf("reply attachments %+v, want the shared attachment", reply.Attachments)
	}
	// The body text is never interpreted: no peer call reflects its content,
	// and delivery proceeds through the identical policy/artifact pipeline
	// as any other message.
	if calls := env.ports.callsOf("_tasks.create"); len(calls) != 0 {
		t.Fatalf("delivery invented task owner calls from message content")
	}

	// A lost-ack retry of the identical reply is durable and exactly once.
	retry := env.sendToConversation(conv, replyID, "cancel? I need clarification on scope",
		[]wireArtifactRef{ref}, []contract.ID{taskID})
	if retry.Version != reply.Version {
		t.Fatalf("retry of the identical reply changed its version to %d", retry.Version)
	}

	// The task correlation and attachment both survive into the group's
	// authorized conversation history for every current participant.
	env.asChief()
	items, _ := env.messageListPage(conv, nil)
	found := false
	for _, m := range items {
		if m.ID == replyID {
			found = true
			if len(m.TaskIDs) != 1 || m.TaskIDs[0] != taskID || len(m.Attachments) != 1 {
				t.Fatalf("group history dropped the durable correlation/attachment: %+v", m)
			}
		}
	}
	if !found {
		t.Fatalf("group participant never saw the durable worker reply")
	}
}
