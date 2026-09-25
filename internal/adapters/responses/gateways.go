package responses

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
)

// These revision identifiers bind profiles to the exact gateway payload
// shape implemented here. Provider choice is never derived from a model ID.
const (
	openRouterProtocolRevision   = "openrouter-responses/stateless-v1"
	experientialProtocolRevision = "experiential-responses/stateless-v1"
)

type gatewayProtocol struct{ provider string }

type openRouterRoute struct {
	Only              []string `json:"only"`
	AllowFallbacks    bool     `json:"allow_fallbacks"`
	RequireParameters bool     `json:"require_parameters"`
	Privacy           []string `json:"privacy,omitempty"`
	PriceCeiling      *struct {
		Currency string `json:"currency"`
		Input    string `json:"input_per_million"`
		Output   string `json:"output_per_million"`
	} `json:"price_ceiling,omitempty"`
}
type experientialRoute struct {
	Gateway struct {
		Retry struct {
			MaxPerRoute int `json:"max_attempts_per_route"`
			MaxTotal    int `json:"max_total_attempts"`
		} `json:"retry"`
		Backoff struct {
			Type string `json:"type"`
		} `json:"backoff"`
		Routing struct {
			AllowFallbacks bool `json:"allow_fallbacks"`
		} `json:"routing"`
	} `json:"gateway"`
	RouteID string   `json:"route_id,omitempty"`
	Privacy []string `json:"privacy,omitempty"`
}

func validateV2Pricing(provider, _ string, routing json.RawMessage, p wireResponsesProfile) error {
	if provider == "experiential" && p.Enforcement.Cost == enforcementEnforced {
		return capabilityUnsupported("Experiential exposes gateway/platform cost but this profile has no documented upstream price ceiling; enforced model-step cost is unavailable, use advisory mode")
	}
	if provider != "openrouter" || p.Enforcement.Cost != enforcementEnforced {
		return nil
	}
	var r openRouterRoute
	if err := json.Unmarshal(routing, &r); err != nil || r.PriceCeiling == nil {
		return capabilityUnsupported("an enforced OpenRouter profile requires its documented route price ceiling")
	}
	if r.PriceCeiling.Currency != "USD" || p.Currency != "USD" {
		return invalidInput("OpenRouter price ceilings and enforced profile rates must use USD")
	}
	in, ok1 := new(big.Rat).SetString(r.PriceCeiling.Input)
	out, ok2 := new(big.Rat).SetString(r.PriceCeiling.Output)
	if !ok1 || !ok2 {
		return invalidInput("OpenRouter price ceiling is not an exact decimal")
	}
	rateIn := new(big.Rat).SetFrac(big.NewInt(p.InputRate.NumeratorMicroUnits), big.NewInt(p.InputRate.DenominatorUnits))
	rateOut := new(big.Rat).SetFrac(big.NewInt(p.OutputRate.NumeratorMicroUnits), big.NewInt(p.OutputRate.DenominatorUnits))
	if rateIn.Cmp(in) < 0 || rateOut.Cmp(out) < 0 {
		return capabilityUnsupported("profile rates are below OpenRouter's maximum per-token route price; the configured cost bound would understate charges")
	}
	return nil
}

func (g gatewayProtocol) limits() protocolLimits {
	return protocolLimits{BoundsOutputTokens: true, MinOutputTokens: 1, SupportsReconcile: false}
}
func (g gatewayProtocol) validateProfile(p protocolProfile) error {
	wantEndpoint, wantMode := "", "stateless"
	switch g.provider {
	case "openrouter":
		wantEndpoint = "https://openrouter.ai/api/v1/responses"
	case "experiential":
		wantEndpoint = "https://api.experientiallabs.ai/v1/responses"
	default:
		return fmt.Errorf("unknown gateway provider")
	}
	if p.Provider != g.provider || p.SessionMode != wantMode || p.Endpoint != wantEndpoint {
		return fmt.Errorf("provider, protocol, session mode and fixed endpoint do not agree")
	}
	if p.MaxInputTokens <= 0 {
		return fmt.Errorf("max_input_tokens must be positive")
	}
	return nil
}
func (g gatewayProtocol) inputTokenBound(in protocolRequest) *int64 {
	return (openaiProtocol{}).inputTokenBound(in)
}
func (g gatewayProtocol) prepare(protocolRequest) (*protocolCall, error) { return nil, nil }
func (g gatewayProtocol) decodePrepare(int, http.Header, []byte) (string, error) {
	return "", errors.New("stateless gateway has no session preparation")
}
func (g gatewayProtocol) authorize(h http.Header, secret []byte) {
	h.Set("Authorization", "Bearer "+string(secret))
}
func (g gatewayProtocol) reconcile(protocolProfile, string) (protocolCall, error) {
	return protocolCall{}, errors.New("stateless gateway has no authoritative reconciliation")
}
func (g gatewayProtocol) decodeReconcile(int, http.Header, []byte) (protocolResult, error) {
	return protocolResult{}, errors.New("stateless gateway has no authoritative reconciliation")
}

func (g gatewayProtocol) encode(in protocolRequest, _ string) (protocolCall, error) {
	base, err := (openaiProtocol{}).encode(in, "")
	if err != nil {
		return protocolCall{}, err
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(base.Body, &body); err != nil {
		return protocolCall{}, err
	}
	delete(body, "conversation")
	delete(body, "prompt_cache_options")
	delete(body, "service_tier")
	body["store"] = json.RawMessage("false")
	if in.SessionID != "" {
		b, _ := json.Marshal(in.SessionID)
		body["session_id"] = b
	}
	switch g.provider {
	case "openrouter":
		var r openRouterRoute
		if err := json.Unmarshal(in.Profile.Routing, &r); err != nil {
			return protocolCall{}, fmt.Errorf("OpenRouter routing decode failed")
		}
		if len(r.Only) == 0 || r.AllowFallbacks || !r.RequireParameters {
			return protocolCall{}, fmt.Errorf("OpenRouter routing must pin providers, disable fallbacks and require parameters")
		}
		p := map[string]any{"only": r.Only, "allow_fallbacks": false, "require_parameters": true}
		for _, v := range r.Privacy {
			switch v {
			case "no_training", "data_policy":
				p["data_collection"] = "deny"
			case "zero_retention":
				p["zdr"] = true
			}
		}
		if r.PriceCeiling != nil {
			p["max_price"] = map[string]json.Number{"prompt": json.Number(r.PriceCeiling.Input), "completion": json.Number(r.PriceCeiling.Output)}
		}
		b, _ := json.Marshal(p)
		body["provider"] = b
		base.Header.Set("X-OpenRouter-Metadata", "enabled")
	case "experiential":
		var r experientialRoute
		if err := json.Unmarshal(in.Profile.Routing, &r); err != nil {
			return protocolCall{}, fmt.Errorf("Experiential routing decode failed")
		}
		if r.Gateway.Retry.MaxPerRoute != 1 || r.Gateway.Retry.MaxTotal != 1 || r.Gateway.Backoff.Type != "none" || r.Gateway.Routing.AllowFallbacks {
			return protocolCall{}, fmt.Errorf("Experiential routing must constrain execution to one attempt with no fallback")
		}
		routing := map[string]any{"allow_fallbacks": false}
		if r.RouteID != "" {
			routing["route_id"] = r.RouteID
		}
		controls := map[string]any{"retry": map[string]any{"max_attempts_per_route": 1, "max_total_attempts": 1, "backoff": map[string]string{"type": "none"}}, "routing": routing}
		b, _ := json.Marshal(controls)
		body["gateway"] = b
		privacy := map[string]any{}
		for _, v := range r.Privacy {
			switch v {
			case "no_training", "data_policy":
				privacy["data_collection"] = "deny"
			case "zero_retention":
				privacy["zdr"] = true
			}
		}
		if len(privacy) > 0 {
			b, _ = json.Marshal(privacy)
			body["provider"] = b
		}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return protocolCall{}, err
	}
	base.Body = b
	return base, nil
}

func (g gatewayProtocol) decode(status int, h http.Header, body []byte) (protocolResult, error) {
	if g.provider == "experiential" {
		if h.Get("x-gateway-replay-repair") != "" {
			return protocolResult{}, fmt.Errorf("Experiential disclosed an internal replay repair despite the single-attempt route profile")
		}
		for _, v := range h.Values("x-experiential-ignored-parameters") {
			for _, field := range strings.Split(v, ",") {
				f := strings.Trim(strings.TrimSpace(field), "\"[] ")
				switch f {
				case "model", "input", "tools", "tool_choice", "max_output_tokens", "store", "gateway", "session_id", "provider", "background", "stream":
					return protocolResult{}, fmt.Errorf("Experiential disclosed ignored safety parameter %q", f)
				}
			}
		}
	}
	r, err := (openaiProtocol{}).decode(status, h, body)
	if err != nil {
		return r, err
	}
	var meta struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
		Usage    struct {
			Cost json.Number `json:"cost"`
		} `json:"usage"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	_ = d.Decode(&meta)
	r.RequestedModel = ""
	if status >= 200 && status < 300 { // The requested identity comes from the secret-free request record; core fills it from the pinned profile.
		r.ServedModel = meta.Model
	}
	if v := h.Get("x-request-id"); v != "" {
		r.ProviderRequestID = v
	}
	if g.provider == "openrouter" {
		r.ServingProvider = "openrouter"
		if meta.Usage.Cost != "" {
			if validDecimalCost(meta.Usage.Cost.String()) {
				r.SourceCostDecimal = meta.Usage.Cost.String()
				r.SourceCostCurrency = "USD"
				r.SourceCostKind = "provider_billed"
			} else {
				r.UsageUnpriceable = "OpenRouter returned an invalid or non-decimal cost"
			}
		}
	} else {
		r.ServingProvider = "experiential"
		// The gateway's route/provider label is retained in the staged raw
		// response; the frozen serving_provider enum identifies the gateway.
		if meta.Usage.Cost != "" {
			if validDecimalCost(meta.Usage.Cost.String()) {
				r.SourceCostDecimal = meta.Usage.Cost.String()
				r.SourceCostCurrency = "USD"
				r.SourceCostKind = "gateway_platform"
			}
			r.UsageUnpriceable = "gateway cost is platform cost and does not price any BYOK upstream inference"
		}
	}
	return r, nil
}

func validDecimalCost(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" {
		return false
	}
	if len(parts[0]) > 1 && parts[0][0] == '0' {
		return false
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return false
		}
	}
	if len(parts) == 2 {
		if len(parts[1]) == 0 || len(parts[1]) > 18 {
			return false
		}
		for _, c := range parts[1] {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}
