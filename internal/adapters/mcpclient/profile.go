package mcpclient

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
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
		return nil, invalidInput("mcp profile does not match the zatiti.mcp/v1 schema")
	}
	var w wireMCPProfile
	if err := contract.DecodeStrict(raw, &w); err != nil {
		return nil, invalidInput("mcp profile decode failed")
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

	if err := validateEndpoint(st.Endpoint); err != nil {
		return nil, err
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

// validateEndpoint excludes known credential-carrying URL forms. An opaque
// path or an unrecognized query name cannot establish whether its value is a
// credential; credential bytes are still checked before staging any request.
func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "" {
		return invalidInput("mcp endpoint must be an absolute HTTPS URL with a host")
	}
	if u.User != nil {
		return capabilityUnsupported("mcp endpoint userinfo credentials are unsupported; use an opaque credential reference")
	}
	if u.Fragment != "" || strings.Contains(endpoint, "#") {
		return invalidInput("mcp endpoint fragments are unsupported")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return invalidInput("mcp endpoint port is invalid")
		}
	}
	// Inspect decoded and dot-normalized segments without rewriting the actual
	// endpoint. The frozen /mcp/<credential> route is forbidden under any prefix.
	decodedPath := decodeEndpointComponent(u.Path)
	if credentialPathRoute(decodedPath) || credentialPathRoute(path.Clean(decodedPath)) {
		return capabilityUnsupported("mcp credential-in-path endpoints are unsupported; use an opaque credential reference")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return invalidInput("mcp endpoint query is malformed")
	}
	for key := range query {
		key = strings.ToLower(decodeEndpointComponent(key))
		key = strings.NewReplacer("_", "", "-", "").Replace(key)
		switch key {
		case "key", "apikey", "token", "accesstoken", "authtoken", "bearer", "auth", "authorization", "credential", "credentials", "secret", "clientsecret", "password", "signature", "sig", "oauthtoken", "xamzsignature", "xamzcredential", "xamzsecuritytoken", "xgoogsignature", "xgoogcredential":
			return capabilityUnsupported("mcp endpoint query credentials are unsupported; use an opaque credential reference")
		}
	}
	return nil
}

func decodeEndpointComponent(value string) string {
	for {
		decoded, err := url.PathUnescape(value)
		if err != nil || decoded == value {
			return value
		}
		value = decoded
	}
}

func credentialPathRoute(endpointPath string) bool {
	segments := strings.Split(endpointPath, "/")
	for i, segment := range segments {
		if !strings.EqualFold(segment, "mcp") {
			continue
		}
		for _, next := range segments[i+1:] {
			if next == "" || next == "." {
				continue
			}
			if next != ".." {
				return true
			}
			break
		}
	}
	return false
}
