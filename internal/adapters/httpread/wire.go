package httpread

import (
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs for the httpread adapter's profile, action and evidence
// schemas. JSON is snake_case; unknown fields are rejected at the decode
// boundary. Schema validation (contract.ValidateSchema) runs before every
// strict decode below, so these shapes only need to agree with the schema
// on field names and types, not re-enforce bounds ValidateSchema already
// checked.

// wireArtifactRef addresses one artifact by identity and content digest.
type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// wireCapabilityEvidence records a qualification result: which adapter
// build, against which protocol revision, was qualified for a given profile
// digest and what it can/cannot do.
type wireCapabilityEvidence struct {
	Artifact         wireArtifactRef `json:"artifact"`
	AdapterVersion   string          `json:"adapter_version"`
	SourceRevision   string          `json:"source_revision"`
	ProtocolRevision string          `json:"protocol_revision"`
	ProfileDigest    contract.Digest `json:"profile_digest"`
	QualifiedAt      time.Time       `json:"qualified_at"`
	Capabilities     []string        `json:"capabilities"`
	Limitations      []string        `json:"limitations"`
}

// wireHTTPReadProfile is the decoded zatiti.httpread/v1 adapter profile.
type wireHTTPReadProfile struct {
	Schema             string                 `json:"schema"`
	AllowedOrigins     []string               `json:"allowed_origins"`
	MaxBytes           int64                  `json:"max_bytes"`
	TimeoutSeconds     int64                  `json:"timeout_seconds"`
	MaxRedirects       int64                  `json:"max_redirects"`
	AllowedMediaTypes  []string               `json:"allowed_media_types"`
	CapabilityEvidence wireCapabilityEvidence `json:"capability_evidence"`
}

// wireReadHeader is one caller-supplied request header from the permitted,
// secret-free set (Accept, Accept-Language, If-None-Match,
// If-Modified-Since, User-Agent).
type wireReadHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// wireHTTPReadParameters is the decoded zatiti.httpread.action/v1 action.
type wireHTTPReadParameters struct {
	Schema            string           `json:"schema"`
	Kind              string           `json:"kind"`
	URL               string           `json:"url"`
	Method            string           `json:"method"`
	Headers           []wireReadHeader `json:"headers"`
	ExpectedMediaType string           `json:"expected_media_type"`
}

// ---------- evidence (Observation.Evidence) body ----------

// wireArtifactLocator is the ArtifactLocator oneOf: kind "staged" names
// bytes this adapter staged (staging_ref + digest) and kind "artifact" a
// published ArtifactRef. This adapter only ever emits the staged variant,
// for the request record it stages before sending.
type wireArtifactLocator struct {
	Kind       string           `json:"kind"`
	Artifact   *wireArtifactRef `json:"artifact,omitempty"`
	StagingRef string           `json:"staging_ref,omitempty"`
	Digest     contract.Digest  `json:"digest,omitempty"`
}

type wirePhysicalCallEvidence struct {
	OperationID          contract.ID         `json:"operation_id"`
	AttemptID            contract.ID         `json:"attempt_id"`
	AccountIdentity      string              `json:"account_identity"`
	RequestedDestination string              `json:"requested_destination"`
	ResolvedDestination  string              `json:"resolved_destination"`
	ProfileDigest        contract.Digest     `json:"profile_digest"`
	CapabilityEvidence   wireArtifactRef     `json:"capability_evidence"`
	StartedAt            time.Time           `json:"started_at"`
	FinishedAt           time.Time           `json:"finished_at"`
	RequestContext       wireArtifactLocator `json:"request_context"`
	RequestSent          string              `json:"request_sent"`
	Confirmation         string              `json:"confirmation"`
	HTTPStatus           int64               `json:"http_status,omitempty"`
	ProviderReference    string              `json:"provider_reference,omitempty"`
	ErrorCode            string              `json:"error_code,omitempty"`
	ErrorMessage         string              `json:"error_message,omitempty"`
}

type wireRationalRate struct {
	NumeratorMicroUnits int64  `json:"numerator_micro_units"`
	DenominatorUnits    int64  `json:"denominator_units"`
	Unit                string `json:"unit"`
}

type wireUsage struct {
	Currency  string `json:"currency"`
	Spent     int64  `json:"spent"`
	Reserved  int64  `json:"reserved"`
	Estimated int64  `json:"estimated"`
	Unknown   int64  `json:"unknown"`
	Advisory  bool   `json:"advisory"`
}

type wireProviderUsage struct {
	Accounting             wireUsage         `json:"accounting"`
	Billing                string            `json:"billing"`
	InputTokens            *int64            `json:"input_tokens,omitempty"`
	OutputTokens           *int64            `json:"output_tokens,omitempty"`
	InputRate              *wireRationalRate `json:"input_rate,omitempty"`
	OutputRate             *wireRationalRate `json:"output_rate,omitempty"`
	ProviderUsageReference string            `json:"provider_usage_reference,omitempty"`
}

type wireStagedOutput struct {
	StagingRef     string          `json:"staging_ref"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Purpose        string          `json:"purpose"`
}

type wireHTTPReadEvidence struct {
	Schema                 string                   `json:"schema"`
	PhysicalCall           wirePhysicalCallEvidence `json:"physical_call"`
	RequestedURL           string                   `json:"requested_url"`
	ResolvedURL            string                   `json:"resolved_url"`
	ValidatedDialAddresses []string                 `json:"validated_dial_addresses"`
	Status                 int64                    `json:"status"`
	MediaType              string                   `json:"media_type"`
	Freshness              time.Time                `json:"freshness"`
	Usage                  wireProviderUsage        `json:"usage"`
	StagedOutputs          []wireStagedOutput       `json:"staged_outputs"`
	OutputArtifacts        []wireArtifactRef        `json:"output_artifacts"`
	ContentDigest          contract.Digest          `json:"content_digest,omitempty"`
	ContentSize            *int64                   `json:"content_size,omitempty"`
	ETag                   string                   `json:"etag,omitempty"`
	LastModified           string                   `json:"last_modified,omitempty"`
	RedirectLocation       string                   `json:"redirect_location,omitempty"`
}
