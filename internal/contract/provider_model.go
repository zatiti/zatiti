package contract

import (
	"context"
	"encoding/json"
)

// ContextPlan is the immutable, transaction-prepared recipe for producing one
// complete model context. Refs and all referenced versions are resolved by
// execution; byte/token bounds are enforced before staged bytes are committed.
type ContextPlan struct {
	ID                    ID            `json:"id"`
	TurnID                ID            `json:"turn_id"`
	ExpectedVersion       Version       `json:"expected_version"`
	Generation            Version       `json:"generation"`
	Refs                  []ArtifactRef `json:"refs"`
	ConfigurationRevision Version       `json:"configuration_revision"`
	ByteBound             int64         `json:"byte_bound"`
	TokenBound            int64         `json:"token_bound"`
}

// ContextPerformer builds, validates and stages the complete context outside a
// write Unit. It returns the frozen ArtifactLocator wire object; execution's
// context.commit remains the sole publication and authority fence.
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
