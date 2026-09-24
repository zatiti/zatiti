package mcpclient

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs for the MCP client adapter's profile, action and evidence
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

// wireMoney is a bounded currency amount in micro-units.
type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

// ---------- transport (MCPClientProfile.transport oneOf) ----------

// wireStreamableHTTPTransport is the only transport this adapter's Phase-0
// scope qualifies; stdio is frozen shape, capability_unsupported.
type wireStreamableHTTPTransport struct {
	Kind                 string `json:"kind"` // streamable_http
	Endpoint             string `json:"endpoint"`
	AllowPrivateEndpoint bool   `json:"allow_private_endpoint"`
	MaxRedirects         int64  `json:"max_redirects"` // const 0
}

// wireTransportKind decodes only the discriminant, to route to the correct
// transport shape before a strict typed decode.
type wireTransportKind struct {
	Kind string `json:"kind"`
}

// wireMCPProfile is the decoded zatiti.mcp/v1 adapter profile. Transport is
// raw at this layer; loadProfile resolves it by its kind discriminant.
type wireMCPProfile struct {
	Schema             string                 `json:"schema"`
	Transport          json.RawMessage        `json:"transport"`
	ProtocolVersion    string                 `json:"protocol_version"`
	CredentialKind     string                 `json:"credential_kind"`
	AllowedTools       []string               `json:"allowed_tools"`
	ToolCallCost       wireMoney              `json:"tool_call_cost"`
	MaxRequestBytes    int64                  `json:"max_request_bytes"`
	MaxResponseBytes   int64                  `json:"max_response_bytes"`
	TimeoutSeconds     int64                  `json:"timeout_seconds"`
	Classifications    []string               `json:"classifications"`
	CapabilityEvidence wireCapabilityEvidence `json:"capability_evidence"`
}

// ---------- action (Dispatch.Action) bodies, kind-discriminated ----------

// wireActionKind decodes only the discriminant, to route to the correct
// action shape before a strict typed decode.
type wireActionKind struct {
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
}

type wireOpenSession struct {
	Schema        string `json:"schema"`
	Kind          string `json:"kind"` // open_session
	ClientName    string `json:"client_name"`
	ClientVersion string `json:"client_version"`
}

type wireListTools struct {
	Schema        string `json:"schema"`
	Kind          string `json:"kind"` // list_tools
	SessionHandle string `json:"session_handle"`
	Cursor        string `json:"cursor,omitempty"`
}

type wireCallTool struct {
	Schema            string          `json:"schema"`
	Kind              string          `json:"kind"` // call_tool
	SessionHandle     string          `json:"session_handle"`
	Tool              string          `json:"tool"`
	Arguments         json.RawMessage `json:"arguments"`
	InputSchema       json.RawMessage `json:"input_schema"`
	InputSchemaDigest contract.Digest `json:"input_schema_digest"`
	Classification    string          `json:"classification"`
}

type wireCloseSession struct {
	Schema        string `json:"schema"`
	Kind          string `json:"kind"` // close_session
	SessionHandle string `json:"session_handle"`
}

// ---------- evidence (Observation.Evidence) body ----------

type wirePhysicalCallEvidence struct {
	OperationID          contract.ID       `json:"operation_id"`
	AttemptID            contract.ID       `json:"attempt_id"`
	AccountIdentity      string            `json:"account_identity"`
	RequestedDestination string            `json:"requested_destination"`
	ResolvedDestination  string            `json:"resolved_destination"`
	ProfileDigest        contract.Digest   `json:"profile_digest"`
	CapabilityEvidence   wireArtifactRef   `json:"capability_evidence"`
	StartedAt            time.Time         `json:"started_at"`
	FinishedAt           time.Time         `json:"finished_at"`
	RequestContext       wireStagedLocator `json:"request_context"`
	RequestSent          string            `json:"request_sent"`
	Confirmation         string            `json:"confirmation"`
	HTTPStatus           int64             `json:"http_status,omitempty"`
	ProviderReference    string            `json:"provider_reference,omitempty"`
	ErrorCode            string            `json:"error_code,omitempty"`
	ErrorMessage         string            `json:"error_message,omitempty"`
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

// wireStagedLocator is the "staged" variant of ArtifactLocator, the only
// variant this adapter returns: it cannot mint artifact IDs, and the
// controller substitutes the artifact variant after publication.
type wireStagedLocator struct {
	Kind       string          `json:"kind"`
	StagingRef string          `json:"staging_ref"`
	Digest     contract.Digest `json:"digest"`
}

// wireHandshakeExchange records one of the exactly-two physical requests the
// frozen contract declares for open_session.
type wireHandshakeExchange struct {
	Message     string `json:"message"` // initialize | notifications/initialized
	HTTPStatus  int64  `json:"http_status"`
	RequestSent string `json:"request_sent"` // no | yes | unknown
}

// wireToolAnnotations are server-declared hints, copied verbatim for
// operator review. Untrusted: never change effect classification, policy,
// review or allowlist decisions.
type wireToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"read_only_hint,omitempty"`
	DestructiveHint *bool  `json:"destructive_hint,omitempty"`
	IdempotentHint  *bool  `json:"idempotent_hint,omitempty"`
	OpenWorldHint   *bool  `json:"open_world_hint,omitempty"`
}

// wireDiscoveredTool is one tool observed from a real list_tools call.
type wireDiscoveredTool struct {
	Name              string              `json:"name"`
	Title             string              `json:"title,omitempty"`
	Description       string              `json:"description,omitempty"`
	InputSchema       json.RawMessage     `json:"input_schema"`
	OutputSchema      json.RawMessage     `json:"output_schema,omitempty"`
	InputSchemaDigest contract.Digest     `json:"input_schema_digest"`
	Annotations       wireToolAnnotations `json:"annotations,omitempty"`
}

// wireContentSummary counts each Content variant in a tool result, never
// its bytes.
type wireContentSummary struct {
	Text             int64 `json:"text"`
	Image            int64 `json:"image"`
	Audio            int64 `json:"audio"`
	ResourceLink     int64 `json:"resource_link"`
	EmbeddedResource int64 `json:"embedded_resource"`
}

type wireMCPEvidence struct {
	Schema                  string                   `json:"schema"`
	PhysicalCall            wirePhysicalCallEvidence `json:"physical_call"`
	Kind                    string                   `json:"kind"`
	SessionHandle           string                   `json:"session_handle,omitempty"`
	SessionState            string                   `json:"session_state"`
	Handshake               []wireHandshakeExchange  `json:"handshake,omitempty"`
	ServerName              string                   `json:"server_name,omitempty"`
	ServerVersion           string                   `json:"server_version,omitempty"`
	ProtocolVersion         string                   `json:"protocol_version,omitempty"`
	ServerCapabilities      []string                 `json:"server_capabilities,omitempty"`
	Tools                   []wireDiscoveredTool     `json:"tools,omitempty"`
	NextCursor              string                   `json:"next_cursor,omitempty"`
	Tool                    string                   `json:"tool,omitempty"`
	ArgumentsDigest         contract.Digest          `json:"arguments_digest,omitempty"`
	IsError                 bool                     `json:"is_error,omitempty"`
	ContentSummary          *wireContentSummary      `json:"content_summary,omitempty"`
	StructuredContentDigest contract.Digest          `json:"structured_content_digest,omitempty"`
	RefusedServerRequests   []string                 `json:"refused_server_requests"`
	Usage                   wireProviderUsage        `json:"usage"`
	StagedOutputs           []wireStagedOutput       `json:"staged_outputs"`
	OutputArtifacts         []wireArtifactRef        `json:"output_artifacts"`
}
