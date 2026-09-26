package responses

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Bound enforcement modes of the frozen BoundEnforcement schema.
const (
	enforcementEnforced    = "enforced"
	enforcementAdvisory    = "advisory"
	enforcementUnsupported = "unsupported"
)

// responsesProfile is the decoded, validated zatiti.responses/v1 adapter
// profile. Every provider-specific value the adapter uses -- endpoint,
// model, prices, bounds -- is read from here; none has a default.
type responsesProfile struct {
	Version string
	// QualificationOnly marks the evidence-free profile draft accepted only
	// for the single fixed qualification probe. It must never authorize a
	// model step, session creation, or reconciliation.
	QualificationOnly  bool
	Provider           string
	SessionMode        string
	Routing            json.RawMessage
	Endpoint           string
	Model              string
	ConnectionID       contract.ID
	MaxInputTokens     int64
	MaxOutputTokens    int64
	MaxResponseBytes   int64
	Timeout            time.Duration
	Currency           string
	InputRate          wireRationalRate
	OutputRate         wireRationalRate
	CostMode           string
	MaximumCost        int64
	Classifications    map[string]bool
	Destinations       []string
	CapabilityEvidence wireCapabilityEvidence
	Digest             contract.Digest
}

// permitsDestination reports whether the profile's enforcement.
// provider_destinations covers destination. Every physical call a wire
// protocol describes is checked against it before it is staged or sent.
func (p *responsesProfile) permitsDestination(destination string) bool {
	u, err := url.Parse(destination)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return false
	}
	for _, d := range p.Destinations {
		if destinationPermits(d, u) {
			return true
		}
	}
	return false
}

// protocolProfile is the profile as a wire protocol sees it.
func (p *responsesProfile) protocolProfile() protocolProfile {
	return protocolProfile{
		Endpoint:       p.Endpoint,
		Model:          p.Model,
		MaxInputTokens: p.MaxInputTokens,
		Capabilities:   p.CapabilityEvidence.Capabilities,
		Provider:       p.Provider,
		SessionMode:    p.SessionMode,
		Routing:        p.Routing,
	}
}

// loadProfile validates raw against the zatiti.responses/v1 schema, strict
// decodes it, checks that capability_evidence.profile_digest binds this
// exact profile (the digest of the canonical profile with the
// capability_evidence field itself omitted), and then applies the
// cross-field rules the frozen JSON Schema cannot express: rate units,
// currency agreement, a credential-free endpoint inside the declared
// disclosure destinations, and enforceable cost/disclosure bounds.
func loadProfile(raw json.RawMessage) (*responsesProfile, error) {
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, invalidInput("responses profile must be a JSON object")
	}
	var qualificationProbe struct {
		CapabilityEvidence json.RawMessage `json:"capability_evidence"`
	}
	if err := json.Unmarshal(raw, &qualificationProbe); err == nil && len(qualificationProbe.CapabilityEvidence) == 0 {
		return loadQualificationDraft(raw, head.Schema)
	}
	schema, err := profileSchema()
	var v2 wireResponsesProfileV2
	if head.Schema == "zatiti.responses/v2" {
		schema, err = profileSchemaV2()
	}
	if err != nil {
		return nil, internalError("responses profile schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("responses profile does not match the %s schema: %v", head.Schema, err)
	}
	var w wireResponsesProfile
	var provider, sessionMode string
	var routing json.RawMessage
	if head.Schema == "zatiti.responses/v2" {
		if err := contract.DecodeStrict(raw, &v2); err != nil {
			return nil, invalidInput("responses v2 profile decode failed: %v", err)
		}
		w = wireResponsesProfile{Schema: v2.Schema, Endpoint: v2.Endpoint, Model: v2.Model, ConnectionID: v2.ConnectionID, MaxInputTokens: v2.MaxInputTokens, MaxOutputTokens: v2.MaxOutputTokens, MaxResponseBytes: v2.MaxResponseBytes, TimeoutSeconds: v2.TimeoutSeconds, Currency: v2.Currency, InputRate: v2.InputRate, OutputRate: v2.OutputRate, Enforcement: v2.Enforcement, CapabilityEvidence: v2.CapabilityEvidence}
		provider, sessionMode, routing = v2.Provider, v2.SessionMode, v2.Routing
	} else if err := contract.DecodeStrict(raw, &w); err != nil {
		return nil, invalidInput("responses profile decode failed: %v", err)
	}
	if head.Schema != "zatiti.responses/v1" && head.Schema != "zatiti.responses/v2" {
		return nil, invalidInput("unsupported Responses profile schema %q", head.Schema)
	}

	digest, err := profileDigestWithoutCapabilityEvidence(raw)
	if err != nil {
		return nil, invalidInput("responses profile canonicalization failed: %v", err)
	}
	if digest != w.CapabilityEvidence.ProfileDigest {
		return nil, invalidInput(
			"responses profile capability_evidence.profile_digest %q does not bind this exact profile (computed %q); self-referential capability evidence grants no authority",
			w.CapabilityEvidence.ProfileDigest, digest)
	}

	if w.InputRate.Unit != "input_token" {
		return nil, invalidInput("responses profile input_rate.unit must be input_token, got %q", w.InputRate.Unit)
	}
	if w.OutputRate.Unit != "output_token" {
		return nil, invalidInput("responses profile output_rate.unit must be output_token, got %q", w.OutputRate.Unit)
	}
	if w.Enforcement.MaximumCost.Currency != w.Currency {
		return nil, invalidInput("responses profile enforcement.maximum_cost currency %q does not agree with the profile currency %q",
			w.Enforcement.MaximumCost.Currency, w.Currency)
	}

	// A model step is always a paid disclosure. A profile that declares
	// either bound unenforceable cannot back this adapter at all: "a
	// profile without enforceable charge bounds cannot claim a hard cap",
	// and there is no unbounded mode to fall back to.
	if w.Enforcement.Cost == enforcementUnsupported {
		return nil, capabilityUnsupported("responses profile declares cost bounds unsupported; a paid model step requires enforced or explicitly advisory cost bounds")
	}
	if w.Enforcement.Disclosure == enforcementUnsupported {
		return nil, capabilityUnsupported("responses profile declares disclosure bounds unsupported; a model step requires enforced or explicitly advisory disclosure bounds")
	}
	if head.Schema == "zatiti.responses/v2" {
		if err := validateV2Pricing(provider, sessionMode, routing, w); err != nil {
			return nil, err
		}
	}

	endpoint, err := parseEndpoint(w.Endpoint)
	if err != nil {
		return nil, err
	}
	permitted := false
	for _, d := range w.Enforcement.ProviderDestinations {
		if destinationPermits(d, endpoint) {
			permitted = true
			break
		}
	}
	if !permitted {
		return nil, permissionDenied("responses profile endpoint is not within enforcement.provider_destinations; a disclosure destination must be declared explicitly")
	}

	classifications := make(map[string]bool, len(w.Enforcement.Classifications))
	for _, c := range w.Enforcement.Classifications {
		classifications[c] = true
	}

	return &responsesProfile{
		Version: head.Schema, Provider: provider, SessionMode: sessionMode, Routing: routing,
		Endpoint:           w.Endpoint,
		Model:              w.Model,
		ConnectionID:       w.ConnectionID,
		MaxInputTokens:     w.MaxInputTokens,
		MaxOutputTokens:    w.MaxOutputTokens,
		MaxResponseBytes:   w.MaxResponseBytes,
		Timeout:            time.Duration(w.TimeoutSeconds) * time.Second,
		Currency:           w.Currency,
		InputRate:          w.InputRate,
		OutputRate:         w.OutputRate,
		CostMode:           w.Enforcement.Cost,
		MaximumCost:        w.Enforcement.MaximumCost.MicroUnits,
		Classifications:    classifications,
		Destinations:       w.Enforcement.ProviderDestinations,
		CapabilityEvidence: w.CapabilityEvidence,
		Digest:             digest,
	}, nil
}

// loadQualificationDraft accepts only the evidence-free v2 draft emitted by
// configuration's qualification candidate. This in-memory adapter profile
// has no pre-authorized capabilities: New pairs it with an action-level gate
// that permits exactly the fixed, one-request qualification probe.
func loadQualificationDraft(raw json.RawMessage, schemaName string) (*responsesProfile, error) {
	if schemaName != "zatiti.responses/v2" {
		return nil, invalidInput("qualification requires an evidence-free zatiti.responses/v2 profile draft")
	}
	var draft struct {
		Schema           string           `json:"schema"`
		Endpoint         string           `json:"endpoint"`
		Model            string           `json:"model"`
		ConnectionID     contract.ID      `json:"connection_id"`
		MaxInputTokens   int64            `json:"max_input_tokens"`
		MaxOutputTokens  int64            `json:"max_output_tokens"`
		MaxResponseBytes int64            `json:"max_response_bytes"`
		TimeoutSeconds   int64            `json:"timeout_seconds"`
		Currency         string           `json:"currency"`
		InputRate        wireRationalRate `json:"input_rate"`
		OutputRate       wireRationalRate `json:"output_rate"`
		Enforcement      struct {
			Cost                 string    `json:"cost"`
			Disclosure           string    `json:"disclosure"`
			MaximumCost          wireMoney `json:"maximum_cost"`
			ProviderDestinations []string  `json:"provider_destinations"`
		} `json:"enforcement"`
		Provider    string          `json:"provider"`
		SessionMode string          `json:"session_mode"`
		Routing     json.RawMessage `json:"routing"`
	}
	if err := contract.DecodeStrict(raw, &draft); err != nil {
		return nil, invalidInput("qualification profile draft decode failed: %v", err)
	}
	if draft.Schema != schemaName || draft.Model == "" || draft.ConnectionID == "" || draft.Endpoint == "" ||
		draft.MaxInputTokens < 1 || draft.MaxOutputTokens < 1 || draft.MaxResponseBytes < 1 ||
		draft.TimeoutSeconds < 1 || draft.Currency == "" || draft.InputRate.Unit != "input_token" ||
		draft.OutputRate.Unit != "output_token" || draft.Enforcement.MaximumCost.Currency != draft.Currency ||
		draft.Enforcement.Cost == enforcementUnsupported || draft.Enforcement.Disclosure == enforcementUnsupported {
		return nil, invalidInput("qualification profile draft is incomplete or has unsupported bounds")
	}
	if draft.Enforcement.Cost != enforcementEnforced && draft.Enforcement.Cost != enforcementAdvisory {
		return nil, invalidInput("qualification profile draft has an unsupported cost enforcement mode")
	}
	if draft.Enforcement.Disclosure != enforcementEnforced && draft.Enforcement.Disclosure != enforcementAdvisory {
		return nil, invalidInput("qualification profile draft has an unsupported disclosure enforcement mode")
	}
	switch draft.Provider {
	case "openai":
		if draft.Endpoint != "https://api.openai.com/v1/responses" || draft.SessionMode != "provider_conversation" || len(draft.Routing) > 0 && string(draft.Routing) != "{}" {
			return nil, invalidInput("OpenAI qualification draft endpoint, session mode or routing is invalid")
		}
	case "openrouter":
		if draft.Endpoint != "https://openrouter.ai/api/v1/responses" || draft.SessionMode != "stateless" {
			return nil, invalidInput("OpenRouter qualification draft endpoint or session mode is invalid")
		}
	case "experiential":
		if draft.Endpoint != "https://api.experientiallabs.ai/v1/responses" || draft.SessionMode != "stateless" {
			return nil, invalidInput("Experiential qualification draft endpoint or session mode is invalid")
		}
	default:
		return nil, invalidInput("qualification profile draft names an unsupported provider")
	}
	endpoint, err := parseEndpoint(draft.Endpoint)
	if err != nil {
		return nil, err
	}
	permitted := false
	for _, destination := range draft.Enforcement.ProviderDestinations {
		if destinationPermits(destination, endpoint) {
			permitted = true
			break
		}
	}
	if !permitted {
		return nil, permissionDenied("qualification endpoint is not inside enforcement.provider_destinations")
	}
	if draft.InputRate.Unit != "input_token" || draft.OutputRate.Unit != "output_token" || draft.InputRate.DenominatorUnits < 1 || draft.OutputRate.DenominatorUnits < 1 {
		return nil, invalidInput("qualification profile rates must be valid input/output token rates")
	}
	protocolRevision := map[string]string{
		"openai": openaiProtocolRevision, "openrouter": openRouterProtocolRevision, "experiential": experientialProtocolRevision,
	}[draft.Provider]
	p := &responsesProfile{
		Version: draft.Schema, QualificationOnly: true, Provider: draft.Provider, SessionMode: draft.SessionMode,
		Routing: draft.Routing, Endpoint: draft.Endpoint, Model: draft.Model, ConnectionID: draft.ConnectionID,
		MaxInputTokens: draft.MaxInputTokens, MaxOutputTokens: draft.MaxOutputTokens,
		MaxResponseBytes: draft.MaxResponseBytes, Timeout: time.Duration(draft.TimeoutSeconds) * time.Second,
		Currency: draft.Currency, InputRate: draft.InputRate, OutputRate: draft.OutputRate,
		CostMode: draft.Enforcement.Cost, MaximumCost: draft.Enforcement.MaximumCost.MicroUnits,
		Classifications: map[string]bool{"internal": true}, Destinations: draft.Enforcement.ProviderDestinations,
		CapabilityEvidence: wireCapabilityEvidence{ProtocolRevision: protocolRevision}, Digest: contract.Hash(raw),
	}
	return p, nil
}

// profileDigestWithoutCapabilityEvidence computes the canonical-JSON SHA-256
// digest of raw with its top-level capability_evidence field removed.
func profileDigestWithoutCapabilityEvidence(raw json.RawMessage) (contract.Digest, error) {
	var doc map[string]json.RawMessage
	if err := contract.DecodeStrict(raw, &doc); err != nil {
		return "", err
	}
	delete(doc, "capability_evidence")
	stripped, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal stripped profile: %w", err)
	}
	canon, err := contract.Canonicalize(stripped)
	if err != nil {
		return "", fmt.Errorf("canonicalize stripped profile: %w", err)
	}
	return contract.Hash(canon), nil
}

// parseEndpoint parses the profile endpoint and rejects anything that could
// carry a secret or an ambiguous destination in the URL itself: the
// endpoint is recorded verbatim in evidence, and "opaque references, URLs,
// headers and diagnostics must never contain secret bytes".
func parseEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, invalidInput("responses profile endpoint is not a valid URL")
	}
	if u.Scheme != "https" || u.Hostname() == "" {
		return nil, invalidInput("responses profile endpoint must be an https URL with a host")
	}
	if u.User != nil {
		return nil, invalidInput("responses profile endpoint must not carry userinfo; credentials resolve only through the secret store")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, invalidInput("responses profile endpoint must not carry a query or fragment")
	}
	return u, nil
}

// destinationPermits reports whether the declared disclosure destination
// covers endpoint: the same https origin, and either no path of its own (an
// origin-wide grant) or exactly the endpoint's path.
func destinationPermits(destination string, endpoint *url.URL) bool {
	d, err := url.Parse(destination)
	if err != nil || d.Scheme != "https" || d.User != nil || d.RawQuery != "" || d.Fragment != "" {
		return false
	}
	if !strings.EqualFold(d.Hostname(), endpoint.Hostname()) || effectivePort(d) != effectivePort(endpoint) {
		return false
	}
	if d.Path == "" || d.Path == "/" {
		return true
	}
	return d.Path == endpoint.Path
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	return "443"
}
