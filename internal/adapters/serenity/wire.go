package serenity

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs for the Serenity adapter's profile, action and evidence schemas.
// JSON is snake_case; unknown fields are rejected at the decode boundary.
// Schema validation (contract.ValidateSchema) runs before every strict
// decode below, so these shapes only need to agree with the schema on
// field names and types, not re-enforce bounds ValidateSchema already
// checked.

// wireArtifactRef addresses one artifact by identity and content digest.
type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// wireVersionRef addresses one versioned resource.
type wireVersionRef struct {
	ID      contract.ID `json:"id"`
	Version int64       `json:"version"`
}

type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

// wireCapabilityEvidence records a qualification result: which adapter
// build, against which source and protocol revision, was qualified for a
// given profile digest and what it can/cannot do.
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

// wireBoundEnforcement is the profile's cost/disclosure enforcement claim
// for nested upstream provider calls.
type wireBoundEnforcement struct {
	Cost                 string                 `json:"cost"`
	Disclosure           string                 `json:"disclosure"`
	MaximumCost          wireMoney              `json:"maximum_cost"`
	ProviderDestinations []string               `json:"provider_destinations"`
	Classifications      []string               `json:"classifications"`
	Evidence             wireCapabilityEvidence `json:"evidence"`
}

// wireBrainMapping binds one Zatiti brain to its locally provisioned
// Serenity endpoint, brain root and single writer owner.
type wireBrainMapping struct {
	BrainID        contract.ID `json:"brain_id"`
	Endpoint       string      `json:"endpoint"`
	RootRef        string      `json:"root_ref"`
	WriterOwner    string      `json:"writer_owner"`
	Classification string      `json:"classification"`
}

type wireLookupSemantics struct {
	Mode                     string            `json:"mode"`
	RetentionSeconds         int64             `json:"retention_seconds"`
	CommandIdentitySupported bool              `json:"command_identity_supported"`
	Evidence                 []wireArtifactRef `json:"evidence"`
}

type wireFreshnessCapability struct {
	SourceRevisionSupported     bool              `json:"source_revision_supported"`
	IndexRevisionSupported      bool              `json:"index_revision_supported"`
	MinimumFreshnessEnforceable bool              `json:"minimum_freshness_enforceable"`
	ReadFacade                  string            `json:"read_facade"`
	Evidence                    []wireArtifactRef `json:"evidence"`
}

type wireBackupProtocol struct {
	Mode                    string            `json:"mode"`
	ProtocolProfile         string            `json:"protocol_profile"`
	ImmutableRevisionExport bool              `json:"immutable_revision_export"`
	RestoreSupported        bool              `json:"restore_supported"`
	Evidence                []wireArtifactRef `json:"evidence"`
}

// wireSerenityProfile is the decoded zatiti.serenity/v1 adapter profile.
type wireSerenityProfile struct {
	Schema                 string                  `json:"schema"`
	Version                string                  `json:"version"`
	Commit                 string                  `json:"commit"`
	BrainMappings          []wireBrainMapping      `json:"brain_mappings"`
	SupportedOperations    []string                `json:"supported_operations"`
	Enforcement            wireBoundEnforcement    `json:"enforcement"`
	CommandStatusLookup    wireLookupSemantics     `json:"command_status_lookup"`
	Freshness              wireFreshnessCapability `json:"freshness"`
	TimeoutSeconds         int64                   `json:"timeout_seconds"`
	MaxBytes               int64                   `json:"max_bytes"`
	BackupRevisionProtocol wireBackupProtocol      `json:"backup_revision_protocol"`
	CapabilityEvidence     wireCapabilityEvidence  `json:"capability_evidence"`
}

// ---------- action (Dispatch.Action) bodies, kind-discriminated ----------

type wireRecall struct {
	Schema                      string      `json:"schema"`
	BrainID                     contract.ID `json:"brain_id"`
	AdapterCommandID            contract.ID `json:"adapter_command_id"`
	Kind                        string      `json:"kind"`
	Query                       string      `json:"query"`
	MinimumFreshness            time.Time   `json:"minimum_freshness"`
	MaxClaims                   int64       `json:"max_claims"`
	MaximumCost                 wireMoney   `json:"maximum_cost"`
	AllowedProviderDestinations []string    `json:"allowed_provider_destinations"`
	Classification              string      `json:"classification"`
}

type wireRemember struct {
	Schema                      string            `json:"schema"`
	BrainID                     contract.ID       `json:"brain_id"`
	AdapterCommandID            contract.ID       `json:"adapter_command_id"`
	Kind                        string            `json:"kind"`
	Text                        string            `json:"text"`
	Sources                     []wireArtifactRef `json:"sources"`
	WriterOwner                 string            `json:"writer_owner"`
	MaximumCost                 wireMoney         `json:"maximum_cost"`
	AllowedProviderDestinations []string          `json:"allowed_provider_destinations"`
}

type wireInspect struct {
	Schema           string         `json:"schema"`
	BrainID          contract.ID    `json:"brain_id"`
	AdapterCommandID contract.ID    `json:"adapter_command_id"`
	Kind             string         `json:"kind"`
	Claim            wireVersionRef `json:"claim"`
	MinimumFreshness time.Time      `json:"minimum_freshness"`
}

type wirePromote struct {
	Schema                      string            `json:"schema"`
	BrainID                     contract.ID       `json:"brain_id"`
	AdapterCommandID            contract.ID       `json:"adapter_command_id"`
	Kind                        string            `json:"kind"`
	SourceBrainID               contract.ID       `json:"source_brain_id"`
	SourceClaim                 wireVersionRef    `json:"source_claim"`
	SourceDisclosureEvidence    []wireArtifactRef `json:"source_disclosure_evidence"`
	Text                        string            `json:"text"`
	Sources                     []wireArtifactRef `json:"sources"`
	CuratorID                   contract.ID       `json:"curator_id"`
	WriterOwner                 string            `json:"writer_owner"`
	MaximumCost                 wireMoney         `json:"maximum_cost"`
	AllowedProviderDestinations []string          `json:"allowed_provider_destinations"`
	Redaction                   string            `json:"redaction,omitempty"`
}

type wireRetract struct {
	Schema           string         `json:"schema"`
	BrainID          contract.ID    `json:"brain_id"`
	AdapterCommandID contract.ID    `json:"adapter_command_id"`
	Kind             string         `json:"kind"`
	Claim            wireVersionRef `json:"claim"`
	Reason           string         `json:"reason"`
	WriterOwner      string         `json:"writer_owner"`
	Removal          string         `json:"removal"`
}

type wireBrainRevision struct {
	BrainID       contract.ID     `json:"brain_id"`
	Revision      string          `json:"revision"`
	Digest        contract.Digest `json:"digest"`
	ObservedAt    time.Time       `json:"observed_at"`
	IndexRevision string          `json:"index_revision,omitempty"`
}

type wireExportRevision struct {
	Schema           string            `json:"schema"`
	BrainID          contract.ID       `json:"brain_id"`
	AdapterCommandID contract.ID       `json:"adapter_command_id"`
	Kind             string            `json:"kind"`
	Revision         wireBrainRevision `json:"revision"`
}

// ---------- evidence ----------

// wirePhysicalCallEvidence is the shared PhysicalCallEvidence shape. This
// adapter only ever emits it with request_sent "no": it holds no HTTP client.
type wirePhysicalCallEvidence struct {
	OperationID          contract.ID     `json:"operation_id"`
	AttemptID            contract.ID     `json:"attempt_id"`
	AccountIdentity      string          `json:"account_identity"`
	RequestedDestination string          `json:"requested_destination"`
	ResolvedDestination  string          `json:"resolved_destination"`
	ProfileDigest        contract.Digest `json:"profile_digest"`
	CapabilityEvidence   wireArtifactRef `json:"capability_evidence"`
	StartedAt            time.Time       `json:"started_at"`
	FinishedAt           time.Time       `json:"finished_at"`
	RequestContext       wireArtifactRef `json:"request_context"`
	RequestSent          string          `json:"request_sent"`
	Confirmation         string          `json:"confirmation"`
	ErrorCode            string          `json:"error_code,omitempty"`
	ErrorMessage         string          `json:"error_message,omitempty"`
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
	Accounting wireUsage `json:"accounting"`
	Billing    string    `json:"billing"`
}

// wireSerenityEvidence is the zatiti.serenity.evidence/v1 document. Claims,
// brain revisions and staged outputs are always empty here: the pinned
// upstream returns nothing this adapter can truthfully place in them (see
// PROTOCOL.md), and the adapter never fabricates an entry.
type wireSerenityEvidence struct {
	Schema              string                   `json:"schema"`
	PhysicalCall        wirePhysicalCallEvidence `json:"physical_call"`
	Kind                string                   `json:"kind"`
	BrainID             contract.ID              `json:"brain_id"`
	AdapterCommandID    contract.ID              `json:"adapter_command_id"`
	CommandStatus       string                   `json:"command_status"`
	Claims              []json.RawMessage        `json:"claims"`
	BrainRevisions      []json.RawMessage        `json:"brain_revisions"`
	Usage               wireProviderUsage        `json:"usage"`
	StagedOutputs       []json.RawMessage        `json:"staged_outputs"`
	OutputArtifacts     []wireArtifactRef        `json:"output_artifacts"`
	WriterOwner         string                   `json:"writer_owner,omitempty"`
	LookupAuthoritative *bool                    `json:"lookup_authoritative,omitempty"`
}
