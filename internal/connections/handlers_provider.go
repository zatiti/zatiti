package connections

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

type modelProviderListIn struct {
	Scope wireScope `json:"scope"`
}

type modelProviderDescriptor struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name"`
	DefaultEndpoint string `json:"default_endpoint"`
	SessionMode     string `json:"session_mode"`
	CredentialSetup string `json:"credential_setup"`
}

func handleModelProviderList(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	if _, err := decodeInto[modelProviderListIn](s, inv.Operation, inv.Input); err != nil {
		return contract.Payload{}, err
	}
	items := []modelProviderDescriptor{
		{ID: "openai", DisplayName: "OpenAI", DefaultEndpoint: "https://api.openai.com/v1/responses", SessionMode: "provider_conversation", CredentialSetup: "api_key"},
		{ID: "openrouter", DisplayName: "OpenRouter", DefaultEndpoint: "https://openrouter.ai/api/v1/responses", SessionMode: "stateless", CredentialSetup: "api_key"},
		{ID: "experiential", DisplayName: "Experiential Labs", DefaultEndpoint: "https://api.experientiallabs.ai/v1/responses", SessionMode: "stateless", CredentialSetup: "api_key"},
	}
	return s.completed(struct {
		Items []modelProviderDescriptor `json:"items"`
	}{Items: items})
}
