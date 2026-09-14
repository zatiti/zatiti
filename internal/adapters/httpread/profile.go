package httpread

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// httpreadProfile is the decoded, validated zatiti.httpread/v1 adapter
// profile, with origin/media-type allowlists indexed for O(1) lookup at
// Invoke time.
type httpreadProfile struct {
	AllowedOrigins     map[string]bool
	MaxBytes           int64
	Timeout            time.Duration
	AllowedMediaTypes  map[string]bool
	CapabilityEvidence wireCapabilityEvidence
	Digest             contract.Digest
}

// loadProfile validates raw against the zatiti.httpread/v1 schema, strict
// decodes it, and checks that capability_evidence.profile_digest binds this
// exact profile: the digest of the canonical profile with the
// capability_evidence field itself omitted. A profile whose evidence does
// not bind to its own bytes is self-referential and grants no authority.
func loadProfile(raw json.RawMessage) (*httpreadProfile, error) {
	schema, err := profileSchema()
	if err != nil {
		return nil, internalError("httpread profile schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("httpread profile does not match the zatiti.httpread/v1 schema: %v", err)
	}
	var w wireHTTPReadProfile
	if err := contract.DecodeStrict(raw, &w); err != nil {
		return nil, invalidInput("httpread profile decode failed: %v", err)
	}

	digest, err := profileDigestWithoutCapabilityEvidence(raw)
	if err != nil {
		return nil, invalidInput("httpread profile canonicalization failed: %v", err)
	}
	if digest != w.CapabilityEvidence.ProfileDigest {
		return nil, invalidInput(
			"httpread profile capability_evidence.profile_digest %q does not bind this exact profile (computed %q); self-referential capability evidence grants no authority",
			w.CapabilityEvidence.ProfileDigest, digest)
	}

	origins := make(map[string]bool, len(w.AllowedOrigins))
	for _, o := range w.AllowedOrigins {
		origins[o] = true
	}
	mediaTypes := make(map[string]bool, len(w.AllowedMediaTypes))
	for _, m := range w.AllowedMediaTypes {
		mediaTypes[m] = true
	}

	return &httpreadProfile{
		AllowedOrigins:     origins,
		MaxBytes:           w.MaxBytes,
		Timeout:            time.Duration(w.TimeoutSeconds) * time.Second,
		AllowedMediaTypes:  mediaTypes,
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

func (p *httpreadProfile) allowsOrigin(origin string) bool {
	return p.AllowedOrigins[origin]
}

func (p *httpreadProfile) allowsMediaType(mediaType string) bool {
	return p.AllowedMediaTypes[mediaType]
}
