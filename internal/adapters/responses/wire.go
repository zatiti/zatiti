package responses

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs for the Responses adapter's profile, action, context and
// evidence schemas. These are Zatiti-side contracts, not representations of
// any upstream API. JSON is snake_case; unknown fields are rejected at the
// decode boundary. Schema validation (contract.ValidateSchema) runs before
// every strict decode, so these shapes only need to agree with the schema
// on field names and types, not re-enforce bounds ValidateSchema already
// checked.

// wireArtifactRef addresses one artifact by identity and content digest.
type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// wireVersionRef addresses one versioned definition.
type wireVersionRef struct {
	ID      contract.ID `json:"id"`
	Version int64       `json:"version"`
}

type wireRationalRate struct {
	NumeratorMicroUnits int64  `json:"numerator_micro_units"`
	DenominatorUnits    int64  `json:"denominator_units"`
	Unit                string `json:"unit"`
}

type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
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

// wireBoundEnforcement declares how far the profile's cost and disclosure
// bounds are enforceable, the cost ceiling, and where/what may be disclosed.
type wireBoundEnforcement struct {
	Cost                 string                 `json:"cost"`
	Disclosure           string                 `json:"disclosure"`
	MaximumCost          wireMoney              `json:"maximum_cost"`
	ProviderDestinations []string               `json:"provider_destinations"`
	Classifications      []string               `json:"classifications"`
	Evidence             wireCapabilityEvidence `json:"evidence"`
}

// wireResponsesProfile is the decoded zatiti.responses/v1 adapter profile.
type wireResponsesProfile struct {
	Schema             string                 `json:"schema"`
	Endpoint           string                 `json:"endpoint"`
	Model              string                 `json:"model"`
	ConnectionID       contract.ID            `json:"connection_id"`
	MaxInputTokens     int64                  `json:"max_input_tokens"`
	MaxOutputTokens    int64                  `json:"max_output_tokens"`
	MaxResponseBytes   int64                  `json:"max_response_bytes"`
	TimeoutSeconds     int64                  `json:"timeout_seconds"`
	Currency           string                 `json:"currency"`
	InputRate          wireRationalRate       `json:"input_rate"`
	OutputRate         wireRationalRate       `json:"output_rate"`
	Enforcement        wireBoundEnforcement   `json:"enforcement"`
	CapabilityEvidence wireCapabilityEvidence `json:"capability_evidence"`
}

type wireResponsesProfileV2 struct {
	Schema             string                 `json:"schema"`
	Endpoint           string                 `json:"endpoint"`
	Model              string                 `json:"model"`
	ConnectionID       contract.ID            `json:"connection_id"`
	MaxInputTokens     int64                  `json:"max_input_tokens"`
	MaxOutputTokens    int64                  `json:"max_output_tokens"`
	MaxResponseBytes   int64                  `json:"max_response_bytes"`
	TimeoutSeconds     int64                  `json:"timeout_seconds"`
	Currency           string                 `json:"currency"`
	InputRate          wireRationalRate       `json:"input_rate"`
	OutputRate         wireRationalRate       `json:"output_rate"`
	Enforcement        wireBoundEnforcement   `json:"enforcement"`
	CapabilityEvidence wireCapabilityEvidence `json:"capability_evidence"`
	Provider           string                 `json:"provider"`
	SessionMode        string                 `json:"session_mode"`
	Routing            json.RawMessage        `json:"routing"`
}

// ---------- action (Dispatch.Action) body ----------

// wireResponsesParameters is the decoded zatiti.responses.action/v1 action:
// a kind-discriminated oneOf of ResponsesPrepareSessionParameters (only
// Schema/Kind) and ResponsesModelStepParameters (every other field). Schema
// validation against the composed oneOf (parametersSchema) runs before this
// struct is ever decoded into (decodeAction), so decoding a superset struct
// is safe: whichever fields the wire document omits simply decode to their
// zero value. MarshalJSON re-splits it back into the exact shape its Kind
// names, for the one caller (tests) that constructs an action as a Go value.
type wireResponsesParameters struct {
	Schema                 string           `json:"schema"`
	Kind                   string           `json:"kind"`
	SessionHandle          string           `json:"session_handle"`
	ContextArtifact        wireArtifactRef  `json:"context_artifact"`
	MaxOutputTokens        int64            `json:"max_output_tokens"`
	ToolContractVersions   []wireVersionRef `json:"tool_contract_versions"`
	ContinuationReference  string           `json:"continuation_reference,omitempty"`
	SessionMode            string           `json:"session_mode,omitempty"`
	SessionID              string           `json:"session_id,omitempty"`
	Model                  string           `json:"model,omitempty"`
	ProbeText              string           `json:"probe_text,omitempty"`
	ProfileDigest          contract.Digest  `json:"profile_digest,omitempty"`
	QualificationCostBound wireMoney        `json:"qualification_cost_bound,omitempty"`
	Provider               string           `json:"provider,omitempty"`
}

// MarshalJSON encodes w as exactly ResponsesPrepareSessionParameters (only
// schema/kind) or exactly ResponsesModelStepParameters (every other field),
// matching w.Kind. Production code never marshals this type -- Dispatch.
// Action is produced upstream of this package -- so this exists for tests
// and any future caller that builds one as a Go value.
func (w wireResponsesParameters) MarshalJSON() ([]byte, error) {
	if w.Schema == "zatiti.responses.action/v2" {
		if w.Kind == kindPrepareSession {
			return json.Marshal(struct {
				Schema      string `json:"schema"`
				Kind        string `json:"kind"`
				SessionMode string `json:"session_mode"`
				SessionID   string `json:"session_id,omitempty"`
			}{w.Schema, w.Kind, w.SessionMode, w.SessionID})
		}
		if w.Kind == kindQualificationProbe {
			return json.Marshal(struct {
				Schema          string          `json:"schema"`
				Kind            string          `json:"kind"`
				SessionMode     string          `json:"session_mode"`
				Model           string          `json:"model"`
				ProbeText       string          `json:"probe_text"`
				MaxOutputTokens int64           `json:"max_output_tokens"`
				ProfileDigest   contract.Digest `json:"profile_digest"`
				CostBound       wireMoney       `json:"qualification_cost_bound"`
				Provider        string          `json:"provider"`
			}{w.Schema, w.Kind, w.SessionMode, w.Model, w.ProbeText, w.MaxOutputTokens, w.ProfileDigest, w.QualificationCostBound, w.Provider})
		}
		return json.Marshal(struct {
			Schema                string           `json:"schema"`
			Kind                  string           `json:"kind"`
			SessionMode           string           `json:"session_mode"`
			SessionID             string           `json:"session_id,omitempty"`
			ContextArtifact       wireArtifactRef  `json:"context_artifact"`
			MaxOutputTokens       int64            `json:"max_output_tokens"`
			ToolContractVersions  []wireVersionRef `json:"tool_contract_versions"`
			SessionHandle         string           `json:"session_handle,omitempty"`
			ContinuationReference string           `json:"continuation_reference,omitempty"`
		}{w.Schema, w.Kind, w.SessionMode, w.SessionID, w.ContextArtifact, w.MaxOutputTokens, w.ToolContractVersions, w.SessionHandle, w.ContinuationReference})
	}
	if w.Kind == kindPrepareSession {
		return json.Marshal(struct {
			Schema string `json:"schema"`
			Kind   string `json:"kind"`
		}{w.Schema, w.Kind})
	}
	return json.Marshal(struct {
		Schema                string           `json:"schema"`
		Kind                  string           `json:"kind"`
		SessionHandle         string           `json:"session_handle"`
		ContextArtifact       wireArtifactRef  `json:"context_artifact"`
		MaxOutputTokens       int64            `json:"max_output_tokens"`
		ToolContractVersions  []wireVersionRef `json:"tool_contract_versions"`
		ContinuationReference string           `json:"continuation_reference,omitempty"`
	}{w.Schema, w.Kind, w.SessionHandle, w.ContextArtifact, w.MaxOutputTokens, w.ToolContractVersions, w.ContinuationReference})
}

// ---------- persisted request context (zatiti.context/v1) ----------

type wireModelToolProposal struct {
	ID               string          `json:"id"`
	Tool             wireVersionRef  `json:"tool"`
	OperationID      string          `json:"operation_id"`
	OperationVersion int64           `json:"operation_version"`
	Input            json.RawMessage `json:"input"`
	SourceContext    wireArtifactRef `json:"source_context"`
	Explanation      string          `json:"explanation,omitempty"`
}

type wireContextText struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type wireContextArtifactPart struct {
	Kind           string          `json:"kind"`
	Artifact       wireArtifactRef `json:"artifact"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Label          string          `json:"label,omitempty"`
}

type wireContextToolCall struct {
	Kind     string                `json:"kind"`
	Proposal wireModelToolProposal `json:"proposal"`
}

type wireContextToolResult struct {
	Kind        string          `json:"kind"`
	ProposalID  string          `json:"proposal_id"`
	OperationID contract.ID     `json:"operation_id"`
	Status      string          `json:"status"`
	Artifact    wireArtifactRef `json:"artifact"`
	ErrorCode   string          `json:"error_code,omitempty"`
}

type wireContextMemoryExcerpt struct {
	Kind            string            `json:"kind"`
	BrainID         contract.ID       `json:"brain_id"`
	Claim           wireVersionRef    `json:"claim"`
	Text            string            `json:"text"`
	Sources         []wireArtifactRef `json:"sources"`
	Confidence      int64             `json:"confidence"`
	Freshness       time.Time         `json:"freshness"`
	Scope           contract.Scope    `json:"scope"`
	SelectedContext wireArtifactRef   `json:"selected_context"`
}

// wireContextMessage is one model-visible message. Parts stay raw here
// because ContextPart is a kind-discriminated union; decodeContextPart
// resolves each one in its exact semantic order.
type wireContextMessage struct {
	ID              contract.ID       `json:"id"`
	Role            string            `json:"role"`
	Origin          string            `json:"origin"`
	Parts           []json.RawMessage `json:"parts"`
	SourceArtifacts []wireArtifactRef `json:"source_artifacts"`
	SenderID        contract.ID       `json:"sender_id,omitempty"`
	MessageID       contract.ID       `json:"message_id,omitempty"`
	CreatedAt       *time.Time        `json:"created_at,omitempty"`
}

type wireContextToolDefinition struct {
	Tool         wireVersionRef  `json:"tool"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	Effect       string          `json:"effect"`
	Destinations []string        `json:"destinations"`
	BindingID    contract.ID     `json:"binding_id"`
	SchemaDigest contract.Digest `json:"schema_digest"`
}

type wireContextCompaction struct {
	SourceContexts   []wireArtifactRef `json:"source_contexts"`
	CompactorProfile wireVersionRef    `json:"compactor_profile"`
	SummaryArtifact  wireArtifactRef   `json:"summary_artifact"`
}

// wireContextArtifact is the decoded zatiti.context/v1 document an action's
// context_artifact must contain.
type wireContextArtifact struct {
	Schema                string                      `json:"schema"`
	AttemptID             contract.ID                 `json:"attempt_id"`
	Scope                 contract.Scope              `json:"scope"`
	ConfigurationRevision int64                       `json:"configuration_revision"`
	Worker                wireVersionRef              `json:"worker"`
	ExecutionProfile      wireVersionRef              `json:"execution_profile"`
	SkillVersions         []wireVersionRef            `json:"skill_versions"`
	Messages              []wireContextMessage        `json:"messages"`
	Tools                 []wireContextToolDefinition `json:"tools"`
	SourceArtifacts       []wireArtifactRef           `json:"source_artifacts"`
	Capture               string                      `json:"capture"`
	CreatedAt             time.Time                   `json:"created_at"`
	Compaction            *wireContextCompaction      `json:"compaction,omitempty"`
}

// ---------- evidence (Observation.Evidence) body ----------

type wirePhysicalCallEvidence struct {
	OperationID          contract.ID       `json:"operation_id"`
	AttemptID            contract.ID       `json:"attempt_id"`
	AccountIdentity      string            `json:"account_identity"`
	RequestedDestination string            `json:"requested_destination"`
	ResolvedDestination  string            `json:"resolved_destination"`
	ProfileDigest        contract.Digest   `json:"profile_digest"`
	CapabilityEvidence   *wireArtifactRef  `json:"capability_evidence,omitempty"`
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
	RequestedModel         string            `json:"requested_model,omitempty"`
	ServedModel            string            `json:"served_model,omitempty"`
	ServingProvider        string            `json:"serving_provider,omitempty"`
	ProviderRequestID      string            `json:"provider_request_id,omitempty"`
	SourceCostDecimal      string            `json:"source_cost_decimal,omitempty"`
	SourceCostCurrency     string            `json:"source_cost_currency,omitempty"`
	SourceCostKind         string            `json:"source_cost_kind,omitempty"`
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

type wireModelOutput struct {
	Schema                string                  `json:"schema"`
	ResponseID            string                  `json:"response_id"`
	RequestContext        wireStagedLocator       `json:"request_context"`
	FinishReason          string                  `json:"finish_reason"`
	TextOutputs           []wireStagedLocator     `json:"text_outputs"`
	ToolProposals         []wireModelToolProposal `json:"tool_proposals"`
	Usage                 wireProviderUsage       `json:"usage"`
	Refusal               string                  `json:"refusal,omitempty"`
	ContinuationReference string                  `json:"continuation_reference,omitempty"`
}

// wireResponsesEvidence is the decoded zatiti.responses.evidence/v1
// document. Revision 3: session_handle is required on every evidence
// document, prepare_session's own handle or the model_step's echoed input;
// response_id/output/output_artifacts are optional and present only for a
// model_step evidence document -- a prepare_session mints no model output.
type wireResponsesEvidence struct {
	Schema          string                   `json:"schema"`
	PhysicalCall    wirePhysicalCallEvidence `json:"physical_call"`
	SessionHandle   string                   `json:"session_handle"`
	ResponseID      string                   `json:"response_id,omitempty"`
	Output          *wireModelOutput         `json:"output,omitempty"`
	StagedOutputs   []wireStagedOutput       `json:"staged_outputs"`
	OutputArtifacts []wireArtifactRef        `json:"output_artifacts,omitempty"`
}

type wireResponsesEvidenceV2 struct {
	Schema          string                       `json:"schema"`
	Kind            string                       `json:"kind,omitempty"`
	Qualification   *wireQualificationMetadataV2 `json:"qualification,omitempty"`
	PhysicalCall    wirePhysicalCallEvidence     `json:"physical_call"`
	SessionHandle   string                       `json:"session_handle,omitempty"`
	ResponseID      string                       `json:"response_id,omitempty"`
	Output          *wireModelOutput             `json:"output,omitempty"`
	StagedOutputs   []wireStagedOutput           `json:"staged_outputs"`
	OutputArtifacts []wireArtifactRef            `json:"output_artifacts,omitempty"`
	SessionMode     string                       `json:"session_mode"`
	SessionID       string                       `json:"session_id,omitempty"`
}

type wireQualificationMetadataV2 struct {
	AdapterVersion   string   `json:"adapter_version"`
	SourceRevision   string   `json:"source_revision"`
	ProtocolRevision string   `json:"protocol_revision"`
	Capabilities     []string `json:"capabilities"`
	Limitations      []string `json:"limitations"`
}
