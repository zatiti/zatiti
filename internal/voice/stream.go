package voice

import (
	"context"
	"encoding/json"
	"github.com/zatiti/zatiti/internal/contract"
)

func (s *Service) craft(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var in struct {
		Scope        contract.Scope `json:"scope"`
		Conversation contract.ID    `json:"conversation_id"`
		Recipient    contract.ID    `json:"recipient_id"`
	}
	if err := contract.ValidateSchema(s.inputSchemas[inv.Operation], inv.Input); err != nil {
		return contract.Payload{}, err
	}
	if err := contract.DecodeStrict(inv.Input, &in); err != nil {
		return contract.Payload{}, err
	}
	if in.Scope != u.Scope() {
		return contract.Payload{}, fault(contract.CodePermissionDenied, "scope mismatch")
	}
	rows, err := u.QueryContext(ctx, "SELECT scope_json,data FROM voice_sessions WHERE actor_id=? AND generation=?", in.Recipient, u.Generation())
	if err != nil {
		return contract.Payload{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var scopeRaw, data string
		if err := rows.Scan(&scopeRaw, &data); err != nil {
			return contract.Payload{}, err
		}
		var scope contract.Scope
		var sess Session
		if json.Unmarshal([]byte(scopeRaw), &scope) != nil || json.Unmarshal([]byte(data), &sess) != nil {
			continue
		}
		if scope.InstallationID == in.Scope.InstallationID && sess.Conversation == in.Conversation && sess.State == "active" && s.deps.Clock.Now().Before(sess.Expires) {
			return completed(map[string]any{"style": sess.Settings.Style, "session_id": sess.ID}), nil
		}
	}
	return completed(map[string]any{"style": "", "session_id": ""}), rows.Err()
}

func (s *Service) phrase(u contract.Unit, sess Session, in request) (string, error) {
	if s.deps.Streams != nil {
		for _, p := range s.deps.Streams.Snapshot(u.Actor().PrincipalID, u.Scope().InstallationID, sess.Conversation) {
			if p.ID != in.Stream || p.VoiceSession != sess.ID || p.State == "interrupted" {
				continue
			}
			phrases := p.Phrases
			if in.Phrase >= 0 && in.Phrase < len(phrases) {
				return phrases[in.Phrase], nil
			}
		}
	}
	return "", fault(contract.CodeNotFound, "spoken phrase is not available in this voice session")
}
