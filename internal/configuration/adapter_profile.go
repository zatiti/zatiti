package configuration

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

type responsesProfileConfig struct {
	Schema             string          `json:"schema"`
	Endpoint           string          `json:"endpoint"`
	Model              string          `json:"model"`
	ConnectionID       contract.ID     `json:"connection_id"`
	MaxInputTokens     int64           `json:"max_input_tokens"`
	MaxOutputTokens    int64           `json:"max_output_tokens"`
	MaxResponseBytes   int64           `json:"max_response_bytes"`
	TimeoutSeconds     int64           `json:"timeout_seconds"`
	Currency           string          `json:"currency"`
	InputRate          profileRate     `json:"input_rate"`
	OutputRate         profileRate     `json:"output_rate"`
	Enforcement        json.RawMessage `json:"enforcement"`
	CapabilityEvidence json.RawMessage `json:"capability_evidence"`
	Provider           string          `json:"provider,omitempty"`
	SessionMode        string          `json:"session_mode,omitempty"`
	Routing            json.RawMessage `json:"routing,omitempty"`
}
type profileRate struct {
	Numerator   int64  `json:"numerator_micro_units"`
	Denominator int64  `json:"denominator_units"`
	Unit        string `json:"unit"`
}

func validateEditableProfile(p wireExecutionProfile) error {
	if p.ConnectionVersion < 1 || len(p.AdapterProfile) == 0 || !json.Valid(p.AdapterProfile) {
		return fmt.Errorf("adapter_profile and positive connection_version are required")
	}
	var c responsesProfileConfig
	if err := contract.DecodeStrict(p.AdapterProfile, &c); err != nil {
		return fmt.Errorf("adapter_profile is not a strict Responses profile: %w", err)
	}
	if c.Schema != "zatiti.responses/v1" && c.Schema != "zatiti.responses/v2" {
		return fmt.Errorf("adapter_profile schema must be zatiti.responses/v1 or zatiti.responses/v2")
	}
	if c.Model == "" || len(c.Model) > 256 || c.ConnectionID == "" || c.Endpoint == "" {
		return fmt.Errorf("adapter_profile requires model, connection_id and endpoint")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("adapter_profile endpoint must be an HTTPS URL")
	}
	if c.Model != p.Model || c.ConnectionID != p.ConnectionID || c.Endpoint != p.ProviderDestination {
		return fmt.Errorf("adapter_profile model, connection_id and endpoint must match the legacy profile fields")
	}
	wantEndpoint := "https://api.openai.com/v1/responses"
	if c.Schema == "zatiti.responses/v2" {
		switch c.Provider {
		case "openrouter":
			wantEndpoint = "https://openrouter.ai/api/v1/responses"
		case "experiential":
			wantEndpoint = "https://api.experientiallabs.ai/v1/responses"
		}
	}
	if c.Endpoint != wantEndpoint {
		return fmt.Errorf("adapter_profile endpoint is not the fixed endpoint for its provider preset")
	}
	if c.MaxInputTokens < 1 || c.MaxOutputTokens < 1 || c.MaxResponseBytes < 1 || c.TimeoutSeconds < 1 {
		return fmt.Errorf("adapter_profile bounds must be positive")
	}
	if c.Currency == "" || c.Currency != p.CostBound.Currency {
		return fmt.Errorf("adapter_profile currency must match cost_bound currency")
	}
	if c.InputRate.Numerator < 0 || c.InputRate.Denominator < 1 || c.InputRate.Unit != "input_token" || c.OutputRate.Numerator < 0 || c.OutputRate.Denominator < 1 || c.OutputRate.Unit != "output_token" {
		return fmt.Errorf("adapter_profile token rates are invalid")
	}
	if c.Schema == "zatiti.responses/v1" && (c.Provider != "" || c.SessionMode != "" || len(c.Routing) != 0) {
		return fmt.Errorf("v1 adapter_profile cannot include revision-2 provider fields")
	}
	if c.Schema == "zatiti.responses/v2" {
		if c.Provider != "openai" && c.Provider != "openrouter" && c.Provider != "experiential" {
			return fmt.Errorf("adapter_profile provider is invalid")
		}
		want := "stateless"
		if c.Provider == "openai" {
			want = "provider_conversation"
		}
		if c.SessionMode != want {
			return fmt.Errorf("adapter_profile session_mode does not match provider")
		}
		if len(c.Routing) == 0 || string(c.Routing) == "null" {
			return fmt.Errorf("adapter_profile routing is required")
		}
	}
	if c.Enforcement == nil || c.CapabilityEvidence == nil {
		return fmt.Errorf("adapter_profile enforcement and capability_evidence are required")
	}
	estimate, err := worstCaseModelCost(c)
	if err != nil {
		return err
	}
	if p.CostBound.MicroUnits != estimate {
		return fmt.Errorf("cost_bound micro_units %d disagrees with rounded worst-case estimate %d", p.CostBound.MicroUnits, estimate)
	}
	return nil
}

// worstCaseModelCost uses exact integers and rounds the combined charge upward.
func worstCaseModelCost(p responsesProfileConfig) (int64, error) {
	if p.InputRate.Denominator < 1 || p.OutputRate.Denominator < 1 {
		return 0, fmt.Errorf("invalid rate denominator")
	}
	a := new(big.Int).Mul(big.NewInt(p.MaxInputTokens), big.NewInt(p.InputRate.Numerator))
	a.Mul(a, big.NewInt(p.OutputRate.Denominator))
	b := new(big.Int).Mul(big.NewInt(p.MaxOutputTokens), big.NewInt(p.OutputRate.Numerator))
	b.Mul(b, big.NewInt(p.InputRate.Denominator))
	n := new(big.Int).Add(a, b)
	d := new(big.Int).Mul(big.NewInt(p.InputRate.Denominator), big.NewInt(p.OutputRate.Denominator))
	q, rem := new(big.Int).QuoRem(n, d, new(big.Int))
	if rem.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, fmt.Errorf("worst-case model cost overflows int64")
	}
	return q.Int64(), nil
}

func adapterProfilePresent(raw json.RawMessage) bool {
	return len(raw) > 0 && strings.TrimSpace(string(raw)) != "null"
}
