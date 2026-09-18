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
	CapabilityEvidence wireCapabilityEvidence
	Digest             contract.Digest
}

// loadProfile validates raw against the zatiti.responses/v1 schema, strict
// decodes it, checks that capability_evidence.profile_digest binds this
// exact profile (the digest of the canonical profile with the
// capability_evidence field itself omitted), and then applies the
// cross-field rules the frozen JSON Schema cannot express: rate units,
// currency agreement, a credential-free endpoint inside the declared
// disclosure destinations, and enforceable cost/disclosure bounds.
func loadProfile(raw json.RawMessage) (*responsesProfile, error) {
	schema, err := profileSchema()
	if err != nil {
		return nil, internalError("responses profile schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("responses profile does not match the zatiti.responses/v1 schema: %v", err)
	}
	var w wireResponsesProfile
	if err := contract.DecodeStrict(raw, &w); err != nil {
		return nil, invalidInput("responses profile decode failed: %v", err)
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
		CapabilityEvidence: w.CapabilityEvidence,
		Digest:             digest,
	}, nil
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
