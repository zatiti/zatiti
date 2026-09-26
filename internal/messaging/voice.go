package messaging

import (
	"context"
	"github.com/zatiti/zatiti/internal/contract"
)

func handleVoiceRead(ctx context.Context, s *Service, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope          wireScope   `json:"scope"`
		ConversationID contract.ID `json:"conversation_id"`
		MessageID      contract.ID `json:"message_id,omitempty"`
	}](s, inv.Operation, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err = checkUnitScope(u, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	conv, err := getConversation(ctx, u, in.ConversationID)
	if err != nil {
		return contract.Payload{}, err
	}
	if conv == nil || conv.InstallationID != u.Scope().InstallationID {
		return contract.Payload{}, notFound("conversation unavailable")
	}
	if err = checkStoredConversationScope(u, in.Scope, conv); err != nil {
		return contract.Payload{}, err
	}
	if err = conversationParticipantFence(u, conv); err != nil {
		return contract.Payload{}, err
	}
	if in.MessageID == "" {
		return s.completed(map[string]any{"text": ""})
	}
	// Query only disclosed rows. Never reveal an undisclosed pre-join message.
	rows, err := listConversationMessages(ctx, u, in.ConversationID, u.Actor().PrincipalID, 200, 0)
	if err != nil {
		return contract.Payload{}, err
	}
	for _, row := range rows {
		if row.ID == in.MessageID && row.SenderID != u.Actor().PrincipalID {
			return s.completed(map[string]any{"text": row.Body})
		}
	}
	return contract.Payload{}, notFound("reply is not in accessible recent conversation history")
}
