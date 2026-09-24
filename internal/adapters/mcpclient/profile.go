package mcpclient

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// transportStreamableHTTP is the only transport kind this Phase-0 revision
// qualifies. transportStdio is a frozen but unqualified shape: a profile
// naming it refuses capability_unsupported at load time.
const (
	transportStreamableHTTP = "streamable_http"
	transportStdio          = "stdio"
)

// mcpProfile is the decoded, validated zatiti.mcp/v1 adapter profile, with
// the tool/classification allowlists indexed for O(1) lookup at Invoke time.
type mcpProfile struct {
	Endpoint             string
	AllowPrivateEndpoint bool
	ProtocolVersion      string
	CredentialKind       string // bearer | none
	AllowedTools         map[string]bool
	ToolCallCost         wireMoney
	MaxRequestBytes      int64
	MaxResponseBytes     int64
	Timeout              time.Duration
	Classifications      map[string]bool
	CapabilityEvidence   wireCapabilityEvidence
	Digest               contract.Digest
}

// loadProfile validates raw against the zatiti.mcp/v1 schema, strict
// decodes it, and checks that capability_evidence.profile_digest binds this
// exact profile: the digest of the canonical profile with the
// capability_evidence field itself omitted. A profile whose evidence does
// not bind to its own bytes is self-referential and grants no authority.
func loadProfile(raw json.RawMessage) (*mcpProfile, error) {
	schema, err := profileSchema()
	if err != nil {
		return nil, internalError("mcp profile schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("mcp profile does not match the zatiti.mcp/v1 schema: %v", err)
	}
	var w wireMCPProfile
	if err := contract.DecodeStrict(raw, &w); err != nil {
		return nil, invalidInput("mcp profile decode failed: %v", err)
	}

	digest, err := profileDigestWithoutCapabilityEvidence(raw)
	if err != nil {
		return nil, invalidInput("mcp profile canonicalization failed: %v", err)
	}
	if digest != w.CapabilityEvidence.ProfileDigest {
		return nil, invalidInput(
			"mcp profile capability_evidence.profile_digest %q does not bind this exact profile (computed %q); self-referential capability evidence grants no authority",
			w.CapabilityEvidence.ProfileDigest, digest)
	}

	// Plain (non-strict) unmarshal: this is a peek at the kind discriminator
	// only, on a JSON object that legitimately carries other fields the
	// wireTransportKind type doesn't declare. DecodeStrict's
	// DisallowUnknownFields would reject those as unknown fields even
	// though they belong to the full transport shape decoded below.
	var kind wireTransportKind
	if err := json.Unmarshal(w.Transport, &kind); err != nil {
		return nil, invalidInput("mcp profile transport decode failed: %v", err)
	}
	if kind.Kind == transportStdio {
		return nil, capabilityUnsupported("mcp profile transport %q is a frozen shape not yet qualified; only streamable_http is supported", transportStdio)
	}
	if kind.Kind != transportStreamableHTTP {
		return nil, invalidInput("mcp profile transport kind %q is not recognized", kind.Kind)
	}
	var st wireStreamableHTTPTransport
	if err := contract.DecodeStrict(w.Transport, &st); err != nil {
		return nil, invalidInput("mcp profile streamable_http transport decode failed: %v", err)
	}

	tools := make(map[string]bool, len(w.AllowedTools))
	for _, t := range w.AllowedTools {
		tools[t] = true
	}
	classifications := make(map[string]bool, len(w.Classifications))
	for _, c := range w.Classifications {
		classifications[c] = true
	}

	return &mcpProfile{
		Endpoint:             st.Endpoint,
		AllowPrivateEndpoint: st.AllowPrivateEndpoint,
		ProtocolVersion:      w.ProtocolVersion,
		CredentialKind:       w.CredentialKind,
		AllowedTools:         tools,
		ToolCallCost:         w.ToolCallCost,
		MaxRequestBytes:      w.MaxRequestBytes,
		MaxResponseBytes:     w.MaxResponseBytes,
		Timeout:              time.Duration(w.TimeoutSeconds) * time.Second,
		Classifications:      classifications,
		CapabilityEvidence:   w.CapabilityEvidence,
		Digest:               digest,
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

func (p *mcpProfile) allowsTool(name string) bool {
	return p.AllowedTools[name]
}

func (p *mcpProfile) allowsClassification(c string) bool {
	return p.Classifications[c]
}
