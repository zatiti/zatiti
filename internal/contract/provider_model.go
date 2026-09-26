package contract

import (
	"context"
	"encoding/json"
)

// ContextPlan is the immutable, transaction-prepared recipe for producing one
// complete model context. Refs and all referenced versions are resolved by
// execution; byte/token bounds are enforced before staged bytes are committed.
type ContextPlan struct {
	ID                    ID              `json:"id"`
	TurnID                ID              `json:"turn_id"`
	ExpectedVersion       Version         `json:"expected_version"`
	Generation            Version         `json:"generation"`
	Scope                 Scope           `json:"scope"`
	AttemptID             ID              `json:"attempt_id,omitempty"`
	Refs                  []ArtifactRef   `json:"refs"`
	ConfigurationRevision Version         `json:"configuration_revision"`
	ByteBound             int64           `json:"byte_bound"`
	TokenBound            int64           `json:"token_bound"`
	Recipe                json.RawMessage `json:"recipe"`
}

// ContextPerformer builds and validates the complete context outside a write
// Unit. It returns the canonical context document bytes; the controller stages
// them and asks artifacts to mint the published reference before execution's
// context.commit applies the authority and generation fence.
type ContextPerformer interface {
	PerformContext(ctx context.Context, plan ContextPlan) (json.RawMessage, error)
}

// ResponsesProfile is strict inert adapter configuration. V1 and V2 are
// retained as raw JSON at owner boundaries so historical operation snapshots
// remain decodable as schemas evolve.
type ResponsesProfile struct{ Document json.RawMessage }
type ResponsesAction struct{ Document json.RawMessage }
type ResponsesEvidence struct{ Document json.RawMessage }

// ProviderUsage preserves exact decimal cost evidence. SourceCostDecimal must
// be parsed using checked integer/rational arithmetic; it is never a float.
type ProviderUsage struct {
	RequestedModel     string `json:"requested_model,omitempty"`
	ServedModel        string `json:"served_model,omitempty"`
	ServingProvider    string `json:"serving_provider,omitempty"`
	ProviderRequestID  string `json:"provider_request_id,omitempty"`
	SourceCostDecimal  string `json:"source_cost_decimal,omitempty"`
	SourceCostCurrency string `json:"source_cost_currency,omitempty"`
	SourceCostKind     string `json:"source_cost_kind,omitempty"`
}
