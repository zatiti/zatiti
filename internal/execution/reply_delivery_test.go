package execution

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// A message-triggered turn is bound at admission to the conversation of
// the message that triggered it, in the same transaction as the processed
// marker.
func TestMessageTurnAdmissionBindsTheTriggeringConversation(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	conversation := e.ids.New()
	e.ports.processedConversation = conversation
	e.ports.setMessages(worker, []wireMessage{{
		ID: e.ids.New(), Version: 1, SenderID: e.ids.New(), RecipientIDs: []contract.ID{worker},
		Scope: e.scope, TaskIDs: []contract.ID{}, Body: "hello", Attachments: []wireArtifactRef{},
		State: "admitted", CreatedAt: "2026-09-10T12:00:00.000000000Z", ConversationID: conversation,
	}})
	turn := e.admitClaimedTurn(worker)
	if turn.ConversationID != conversation {
		t.Fatalf("turn conversation %q, want %q", turn.ConversationID, conversation)
	}
}

// conversationTurnFixture is a hosted turn that answers in a conversation.
func conversationTurnFixture(t *testing.T) (*turnFixture, contract.ID) {
	t.Helper()
	f := newTurnFixture(t)
	conversation := f.e.ids.New()
	f.e.inWrite(func(u contract.Unit) error {
		_, err := u.ExecContext(f.e.ctx, `UPDATE execution_turns SET conversation_id = ? WHERE id = ?`, string(conversation), string(f.turnID))
		return err
	})
	return f, conversation
}

func replyProposal(t *testing.T, id, text string, source wireArtifactRef) wireModelToolProposal {
	return wireModelToolProposal{
		ID: id, Tool: wireRef{ID: contract.LocalDecisionToolID(contract.LocalDecisionToolReply), Version: 1},
		OperationID: contract.LocalDecisionOperationID(contract.LocalDecisionToolReply), OperationVersion: 1,
		Input: mustMarshal(t, map[string]string{"text": text}), SourceContext: source,
	}
}

// A reply in a conversation-bound turn is staged, not sent: execution
// prepares the exact conversation.message.send the worker's own actor must
// perform, and only recording that delivery completes the turn.
func TestConversationReplyIsStagedForWorkerAuthorityDelivery(t *testing.T) {
	f, conversation := conversationTurnFixture(t)
	e := f.e
	ctx1 := f.dispatchStep(t)
	f.deliver(t, buildModelOutput(t, ctx1, []wireModelToolProposal{replyProposal(t, "call-1", "Here is the answer.", ctx1)}))

	row := findProposalRowForTest(t, e, f.turnID, 0, "call-1")
	if row.State != "prepared" {
		t.Fatalf("reply proposal state %q, want prepared until delivered", row.State)
	}
	var np normalizedProposal
	if err := json.Unmarshal(row.NormalizedProposal, &np); err != nil {
		t.Fatal(err)
	}
	wantID := uuidFromDigest(sha256Hex([]byte("zatiti.reply-message/" + string(f.turnID) + "/0/call-1")))
	if np.Kind != "reply" || np.Text != "Here is the answer." || np.MessageID != wantID ||
		np.Operation != "conversation.message.send" || np.OperationVersion != 1 {
		t.Fatalf("normalized proposal = %+v", np)
	}
	var send struct {
		Scope          contract.Scope    `json:"scope"`
		ConversationID contract.ID       `json:"conversation_id"`
		MessageID      contract.ID       `json:"message_id"`
		Body           string            `json:"body"`
		Attachments    []wireArtifactRef `json:"attachments"`
		TaskIDs        []contract.ID     `json:"task_ids"`
	}
	if err := contract.DecodeStrict(np.Input, &send); err != nil {
		t.Fatalf("staged input %s: %v", np.Input, err)
	}
	if send.ConversationID != conversation || send.MessageID != wantID || send.Body != "Here is the answer." ||
		send.Scope.WorkerID != "" || send.Scope.TaskID != "" || send.Scope.InstallationID != e.scope.InstallationID ||
		send.Attachments == nil || send.TaskIDs == nil {
		t.Fatalf("staged conversation.message.send = %+v", send)
	}
	turn := e.readTurn(f.turnID)
	if turn.State != "proposal_pending" {
		t.Fatalf("turn state %q before delivery, want proposal_pending", turn.State)
	}

	// The prepared proposal replays as prepared (never as a finished turn)
	// for a caller that restarts before delivering it.
	prep := e.mustOK(opProposalPrepare, proposalPrepareInput{TurnID: f.turnID, StepIndex: 0, ProposalID: "call-1", ExpectedVersion: turn.Version})
	var prepared struct {
		Resource struct {
			State string `json:"state"`
		} `json:"resource"`
	}
	e.decode(prep.Data, &prepared)
	if prepared.Resource.State != "prepared" {
		t.Fatalf("proposal.prepare replay state %q, want prepared", prepared.Resource.State)
	}

	e.mustOK(opProposalRecord, proposalRecordInput{ProposalID: "call-1", ExpectedVersion: e.readTurn(f.turnID).Version, CommandID: e.ids.New()})
	if turn := e.readTurn(f.turnID); turn.State != "completed" {
		t.Fatalf("turn state %q after delivery, want completed", turn.State)
	}
	if row := findProposalRowForTest(t, e, f.turnID, 0, "call-1"); row.State != "recorded" {
		t.Fatalf("reply proposal state %q after delivery, want recorded", row.State)
	}
}

// A turn with no conversation (task work) records its reply inline: there
// is no conversation to deliver into.
func TestReplyWithoutConversationIsRecordedInline(t *testing.T) {
	f := newTurnFixture(t)
	ctx1 := f.dispatchStep(t)
	f.deliver(t, buildModelOutput(t, ctx1, []wireModelToolProposal{replyProposal(t, "call-1", "done", ctx1)}))
	row := findProposalRowForTest(t, f.e, f.turnID, 0, "call-1")
	var np normalizedProposal
	if err := json.Unmarshal(row.NormalizedProposal, &np); err != nil {
		t.Fatal(err)
	}
	if row.State != "recorded" || np.Operation != "" || np.MessageID != "" {
		t.Fatalf("proposal state %q, normalized %+v", row.State, np)
	}
	if turn := f.e.readTurn(f.turnID); turn.State != "completed" {
		t.Fatalf("turn state %q, want completed", turn.State)
	}
}
