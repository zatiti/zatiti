package connections

import (
	"context"
	"github.com/zatiti/zatiti/internal/contract"
)

func handleVoiceResolve(ctx context.Context, s *Service, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope       wireScope `json:"scope"`
		Connection  wireRef   `json:"connection"`
		Destination string    `json:"destination"`
	}](s, inv.Operation, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := s.loadConnectionChecked(ctx, u, in.Connection.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !scopeCovers(in.Scope.toContract(), row.Scope) || row.Scope.InstallationID != u.Scope().InstallationID {
		return contract.Payload{}, permissionDenied("voice connection outside scope")
	}
	if row.Version != in.Connection.Version {
		return contract.Payload{}, staleVersion("voice credential changed; start a new session")
	}
	if f := refuseInactive(row); f != nil {
		return contract.Payload{}, f
	}
	if row.Provider != "openrouter" || row.CredentialRef == "" || row.ValidationState == connStateRevoked || row.ValidationState == connStateInvalid || row.ValidationState == connStateExpired {
		return contract.Payload{}, prerequisiteMissing("dedicated OpenRouter voice credential unavailable")
	}
	if row.ValidUntil != nil && !row.ValidUntil.After(s.clock.Now()) {
		return contract.Payload{}, prerequisiteMissing("voice credential expired")
	}
	if !validDedicatedVoiceConnection(row.wire()) || !contains(row.Destinations, in.Destination) {
		return contract.Payload{}, permissionDenied("connection must explicitly allow voice and this speech destination")
	}
	for _, destination := range row.Destinations {
		if destination != "https://openrouter.ai/api/v1/audio/transcriptions" && destination != "https://openrouter.ai/api/v1/audio/speech" && destination != "https://openrouter.ai/api/v1/chat/completions" {
			return contract.Payload{}, permissionDenied("voice requires a separate connection without worker destinations")
		}
	}
	return s.completed(map[string]any{"credential_ref": row.CredentialRef})
}

func hasVoiceScope(c wireConnection) bool { return contains(c.AllowedScopes, "voice") }
func validDedicatedVoiceConnection(c wireConnection) bool {
	if c.Provider != "openrouter" || len(c.AllowedScopes) != 1 || !hasVoiceScope(c) || len(c.Destinations) != 3 {
		return false
	}
	for _, d := range c.Destinations {
		if d != "https://openrouter.ai/api/v1/audio/transcriptions" && d != "https://openrouter.ai/api/v1/audio/speech" && d != "https://openrouter.ai/api/v1/chat/completions" {
			return false
		}
	}
	return true
}
