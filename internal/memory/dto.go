package memory

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs. JSON is snake_case and unknown fields are rejected at the
// decode boundary before these types are filled. Output schemas describe
// Payload.Data, not the result envelope. Peer payloads are decoded with
// plain Unmarshal into the fields this package consumes; bytes that must
// survive verbatim (change definitions, evidence sub-objects, job results)
// stay json.RawMessage and are strict-parsed where this owner acts on them.

// Classification constants shared across bindings, brains and claims.
const (
	classificationInternal   = "internal"
	classificationPublic     = "public"
	classificationRestricted = "restricted"
)

// Binding permission constants.
const (
	permRead    = "read"
	permWrite   = "write"
	permCurate  = "curate"
	permPromote = "promote"
	permRetract = "retract"
)

// Binding and brain state constants.
const (
	bindingActive    = "active"
	bindingArchiving = "archiving"
	bindingArchived  = "archived"

	brainProvisioning = "provisioning"
	brainActive       = "active"
	brainUnavailable  = "unavailable"
)

// Intent states follow the writer-intent lifecycle: prepared before the
// effects operation exists, admitted after _effects.admit, dispatched after
// the execution job exists, and terminal after a recorded disposition.
const (
	intentPrepared   = "prepared"
	intentAdmitted   = "admitted"
	intentDispatched = "dispatched"
	intentCompleted  = "completed"
	intentFailed     = "failed"
	intentUnknown    = "unknown"
)

// Job states mirror $defs/Job.
const (
	jobPending       = "pending"
	jobRunning       = "running"
	jobSucceeded     = "succeeded"
	jobFailed        = "failed"
	jobOutcomeUnkown = "outcome_unknown"
	jobCancelled     = "cancelled"
)

// Intent kinds.
const (
	intentRecall          = "recall"
	intentRemember        = "remember"
	intentPromote         = "promote"
	intentRetract         = "retract"
	intentWriterProvision = "writer_provision"
)

// Reconciliation kinds.
const (
	reconcilePromotionCorrection = "promotion_correction"
	reconcileRetraction          = "retraction_propagation"
	reconcileWriterAck           = "writer_ack"
)

// Requirement codes emitted by this owner.
const (
	reqWriterProvision    = "memory_writer_provision"
	reqWriteUnknown       = "memory_write_unknown"
	reqWriteNotSent       = "memory_write_not_sent"
	reqContextUnavailable = "memory_context_artifact_unavailable"
	reqReconciliation     = "memory_reconciliation_pending"
	reqFreshness          = "memory_freshness_unavailable"
)

// Serenity adapter command statuses and evidence fields this owner acts on.
const (
	serenitySchema         = "zatiti.serenity.action/v1"
	serenityEvidenceSchema = "zatiti.serenity.evidence/v1"

	serenityCommandNotAdmitted = "not_admitted"
	serenityCommandAccepted    = "accepted"
	serenityCommandCompleted   = "completed"
	serenityCommandFailed      = "failed"
	serenityCommandUnknown     = "unknown"
)

// wireScope mirrors $defs/Scope.
type wireScope struct {
	InstallationID contract.ID `json:"installation_id"`
	OrganizationID contract.ID `json:"organization_id,omitempty"`
	ProjectID      contract.ID `json:"project_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
}

// wireRef mirrors $defs/Ref.
type wireRef struct {
	ID      contract.ID `json:"id"`
	Version int64       `json:"version"`
}

// wireMoney mirrors $defs/Money: int64 micro-units of one explicit currency.
type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

// wireUsage mirrors $defs/Usage.
type wireUsage struct {
	Currency  string `json:"currency"`
	Spent     int64  `json:"spent"`
	Reserved  int64  `json:"reserved"`
	Estimated int64  `json:"estimated"`
	Unknown   int64  `json:"unknown"`
	Advisory  bool   `json:"advisory"`
}

// wireLimits mirrors $defs/Limits. All eight fields are required on the
// wire; the zero time is the unset root deadline.
type wireLimits struct {
	Currency        string    `json:"currency"`
	SpendMicroUnits int64     `json:"spend_micro_units"`
	Concurrency     int64     `json:"concurrency"`
	ModelSteps      int64     `json:"model_steps"`
	ChildCount      int64     `json:"child_count"`
	DelegationDepth int64     `json:"delegation_depth"`
	AttemptSeconds  int64     `json:"attempt_seconds"`
	RootDeadline    time.Time `json:"root_deadline"`
}

// wireArtifactRef mirrors $defs/ArtifactRef.
type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// wireAction mirrors $defs/Action. All twelve fields are required. The open
// precondition/parameter objects stay inert JSON: schema data, never
// executable authority.
type wireAction struct {
	Scope                 wireScope         `json:"scope"`
	Tool                  wireRef           `json:"tool"`
	Connection            wireRef           `json:"connection"`
	AccountIdentity       string            `json:"account_identity"`
	Destination           string            `json:"destination"`
	Content               []wireArtifactRef `json:"content"`
	NotBefore             time.Time         `json:"not_before"`
	ExpiresAt             time.Time         `json:"expires_at"`
	Preconditions         json.RawMessage   `json:"preconditions"`
	ConfigurationRevision int64             `json:"configuration_revision"`
	Parameters            json.RawMessage   `json:"parameters"`
	CostBound             wireMoney         `json:"cost_bound"`
}

// wireOperation mirrors $defs/Operation.
type wireOperation struct {
	ID                contract.ID     `json:"id"`
	Version           int64           `json:"version"`
	Action            json.RawMessage `json:"action"`
	ActionDigest      string          `json:"action_digest"`
	State             string          `json:"state"`
	AttemptIDs        []contract.ID   `json:"attempt_ids"`
	LinkedOperationID *contract.ID    `json:"linked_operation_id,omitempty"`
	Relationship      *string         `json:"relationship,omitempty"`
}

// wireObservation mirrors $defs/Observation.
type wireObservation struct {
	Disposition       string          `json:"disposition"`
	Evidence          json.RawMessage `json:"evidence"`
	Usage             wireUsage       `json:"usage"`
	ProviderReference string          `json:"provider_reference,omitempty"`
	ConfirmedAt       *time.Time      `json:"confirmed_at,omitempty"`
}

// wireRequirement mirrors $defs/Requirement.
type wireRequirement struct {
	Code        string       `json:"code"`
	Message     string       `json:"message"`
	ResourceID  *contract.ID `json:"resource_id,omitempty"`
	ChallengeID *contract.ID `json:"challenge_id,omitempty"`
}

// wireJob mirrors $defs/Job.
type wireJob struct {
	ID             contract.ID       `json:"id"`
	Version        int64             `json:"version"`
	Kind           string            `json:"kind"`
	State          string            `json:"state"`
	Requirements   []wireRequirement `json:"requirements"`
	ResultArtifact *wireArtifactRef  `json:"result_artifact,omitempty"`
	OperationID    *contract.ID      `json:"operation_id,omitempty"`
	Owner          string            `json:"owner"`
	Operation      string            `json:"operation"`
	Result         json.RawMessage   `json:"result,omitempty"`
}

// wireDiagnostic mirrors $defs/Diagnostic.
type wireDiagnostic struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// wireRequirement, wireDraft and wireValidation support the compiler
// validate/activate boundary.
type wireDraft struct {
	ID           contract.ID       `json:"id"`
	Version      int64             `json:"version"`
	BaseRevision int64             `json:"base_revision"`
	Changes      []json.RawMessage `json:"changes"`
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
}

// wireValidation mirrors $defs/Validation.
type wireValidation struct {
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
	Requirements []wireRequirement `json:"requirements"`
	Dependencies []wireRef         `json:"dependencies"`
}

// wireCandidate mirrors $defs/Candidate.
type wireCandidate struct {
	PlanID          contract.ID       `json:"plan_id"`
	BaseRevision    int64             `json:"base_revision"`
	CandidateDigest contract.Digest   `json:"candidate_digest"`
	Changes         []json.RawMessage `json:"changes"`
	Dependencies    []wireRef         `json:"dependencies"`
}

// wireMemoryBinding mirrors $defs/MemoryBinding.
type wireMemoryBinding struct {
	ID             contract.ID `json:"id"`
	Version        int64       `json:"version"`
	Scope          wireScope   `json:"scope"`
	BrainID        contract.ID `json:"brain_id"`
	Permissions    []string    `json:"permissions"`
	Classification string      `json:"classification"`
}

// wireClaim mirrors $defs/Claim.
type wireClaim struct {
	ID            contract.ID       `json:"id"`
	Version       int64             `json:"version"`
	BrainID       contract.ID       `json:"brain_id"`
	Text          string            `json:"text"`
	Sources       []wireArtifactRef `json:"sources"`
	Confidence    int64             `json:"confidence"`
	Freshness     time.Time         `json:"freshness"`
	Active        bool              `json:"active"`
	SourceBrainID *contract.ID      `json:"source_brain_id,omitempty"`
	SourceClaim   *wireRef          `json:"source_claim,omitempty"`
	CuratorID     *contract.ID      `json:"curator_id,omitempty"`
	Redaction     string            `json:"redaction,omitempty"`
}

// wireBinding mirrors the configuration $defs/Binding shape carried in
// scope snapshots: connections and tools reference writer destinations.
type wireBinding struct {
	ID           contract.ID `json:"id"`
	Version      int64       `json:"version"`
	Scope        wireScope   `json:"scope"`
	Kind         string      `json:"kind"`
	TargetID     contract.ID `json:"target_id"`
	Permissions  []string    `json:"permissions"`
	SourceScope  *wireScope  `json:"source_scope,omitempty"`
	Destinations []string    `json:"destinations,omitempty"`
}

// wireOrganization mirrors the snapshot ancestor fields this owner uses.
type wireOrganization struct {
	ID       contract.ID  `json:"id"`
	Version  int64        `json:"version"`
	ParentID *contract.ID `json:"parent_id,omitempty"`
}

// wireWorker mirrors the snapshot worker fields this owner uses.
type wireWorker struct {
	ID             contract.ID `json:"id"`
	Version        int64       `json:"version"`
	OrganizationID contract.ID `json:"organization_id"`
}

// wireProject mirrors the snapshot project fields this owner uses.
type wireProject struct {
	ID             contract.ID `json:"id"`
	Version        int64       `json:"version"`
	OrganizationID contract.ID `json:"organization_id"`
}

// Operation inputs.

type activateInput struct {
	Candidate wireCandidate `json:"candidate"`
}

type bootstrapInput struct {
	InstallationID contract.ID `json:"installation_id"`
	OrganizationID contract.ID `json:"organization_id"`
	ChiefID        contract.ID `json:"chief_id"`
}

type manifestInput struct {
	Scope wireScope `json:"scope"`
}

type recordInput struct {
	JobID       contract.ID     `json:"job_id"`
	OperationID contract.ID     `json:"operation_id"`
	Observation wireObservation `json:"observation"`
}

type selectInput struct {
	Scope            wireScope     `json:"scope"`
	BindingIDs       []contract.ID `json:"binding_ids"`
	Permission       string        `json:"permission"`
	MinimumFreshness time.Time     `json:"minimum_freshness"`
}

type validateInput struct {
	Candidate wireCandidate `json:"candidate"`
}

type bindingArchiveInput struct {
	Scope           wireScope    `json:"scope"`
	ID              contract.ID  `json:"id"`
	ExpectedVersion int64        `json:"expected_version"`
	DraftID         *contract.ID `json:"draft_id,omitempty"`
}

// bindingDefinition is the create/update definition embedded in the input
// schema: the full MemoryBinding minus server-allocated id/version.
type bindingDefinition struct {
	Scope          wireScope   `json:"scope"`
	BrainID        contract.ID `json:"brain_id"`
	Permissions    []string    `json:"permissions"`
	Classification string      `json:"classification"`
}

type bindingCreateInput struct {
	Scope      wireScope         `json:"scope"`
	Definition bindingDefinition `json:"definition"`
	DraftID    *contract.ID      `json:"draft_id,omitempty"`
}

type bindingUpdateInput struct {
	Scope           wireScope         `json:"scope"`
	ID              contract.ID       `json:"id"`
	ExpectedVersion int64             `json:"expected_version"`
	Definition      bindingDefinition `json:"definition"`
	DraftID         *contract.ID      `json:"draft_id,omitempty"`
}

type bindingGetInput struct {
	Scope wireScope   `json:"scope"`
	ID    contract.ID `json:"id"`
}

type bindingListInput struct {
	Scope  wireScope      `json:"scope"`
	Cursor string         `json:"cursor,omitempty"`
	Limit  *int64         `json:"limit,omitempty"`
	Filter *bindingFilter `json:"filter,omitempty"`
}

// bindingFilter carries the structured filter fields the schema declares.
// None of them map to the MemoryBinding wire shape, so any set field is
// refused with invalid_input before pagination.
type bindingFilter struct {
	State          string      `json:"state,omitempty"`
	Key            string      `json:"key,omitempty"`
	ParentID       contract.ID `json:"parent_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
	OrganizationID contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool       `json:"descendants,omitempty"`
	NeedsYou       *bool       `json:"needs_you,omitempty"`
}

type inspectInput struct {
	Scope   wireScope   `json:"scope"`
	BrainID contract.ID `json:"brain_id"`
	ClaimID contract.ID `json:"claim_id"`
}

type jobGetInput struct {
	Scope wireScope   `json:"scope"`
	ID    contract.ID `json:"id"`
}

type promoteInput struct {
	Scope                wireScope   `json:"scope"`
	SourceBrainID        contract.ID `json:"source_brain_id"`
	SourceClaim          wireRef     `json:"source_claim"`
	DestinationBindingID contract.ID `json:"destination_binding_id"`
	Redaction            string      `json:"redaction,omitempty"`
	Limits               wireLimits  `json:"limits"`
}

type recallInput struct {
	Scope            wireScope     `json:"scope"`
	Query            string        `json:"query"`
	BindingIDs       []contract.ID `json:"binding_ids"`
	MinimumFreshness time.Time     `json:"minimum_freshness"`
	Limits           wireLimits    `json:"limits"`
}

type rememberInput struct {
	Scope     wireScope         `json:"scope"`
	BindingID contract.ID       `json:"binding_id"`
	Text      string            `json:"text"`
	Sources   []wireArtifactRef `json:"sources"`
	Limits    wireLimits        `json:"limits"`
}

type retractInput struct {
	Scope   wireScope   `json:"scope"`
	BrainID contract.ID `json:"brain_id"`
	Claim   wireRef     `json:"claim"`
	Reason  string      `json:"reason"`
}

// Operation outputs.

type versionsOutput struct {
	Versions []wireRef `json:"versions"`
}

type bindingsOutput struct {
	Bindings []wireMemoryBinding `json:"bindings"`
}

type manifestOutput struct {
	BrainRevisions []wireRef         `json:"brain_revisions"`
	Obligations    []wireRequirement `json:"obligations"`
}

type validateOutput struct {
	Resource wireValidation `json:"resource"`
}

type selectOutput struct {
	Bindings []wireMemoryBinding `json:"bindings"`
}

type jobResourceOutput struct {
	Resource wireJob `json:"resource"`
}

type stagedResourceOutput struct {
	Draft    wireDraft         `json:"draft"`
	Resource wireMemoryBinding `json:"resource"`
}

type bindingResourceOutput struct {
	Resource wireMemoryBinding `json:"resource"`
}

type bindingListOutput struct {
	Items []wireMemoryBinding `json:"items"`
}

type claimResourceOutput struct {
	Resource wireClaim `json:"resource"`
}

// Peer payload bodies.

type policyCheckInput struct {
	Scope           wireScope       `json:"scope"`
	Capability      string          `json:"capability"`
	Action          json.RawMessage `json:"action,omitempty"`
	CandidateDigest string          `json:"candidate_digest,omitempty"`
}

type policyResultBody struct {
	Resource wirePolicyResult `json:"resource"`
}

// wirePolicyResult mirrors $defs/PolicyResult.
type wirePolicyResult struct {
	Decision     string            `json:"decision"`
	Reasons      []string          `json:"reasons"`
	Requirements []json.RawMessage `json:"requirements"`
}

type configurationSnapshotInput struct {
	Scope wireScope `json:"scope"`
}

type snapshotBody struct {
	Resource struct {
		Scope     wireScope          `json:"scope"`
		Revision  int64              `json:"revision"`
		Ancestors []wireOrganization `json:"ancestors"`
		Bindings  []wireBinding      `json:"bindings"`
		Worker    *wireWorker        `json:"worker,omitempty"`
		Project   *wireProject       `json:"project,omitempty"`
	} `json:"resource"`
}

type configurationStageInput struct {
	Scope   wireScope       `json:"scope"`
	Change  json.RawMessage `json:"change"`
	DraftID *contract.ID    `json:"draft_id,omitempty"`
}

type draftResourceBody struct {
	Resource wireDraft `json:"resource"`
}

type artifactsMetadataInput struct {
	Scope     wireScope         `json:"scope"`
	Artifacts []wireArtifactRef `json:"artifacts"`
}

type artifactsMetadataBody struct {
	Artifacts []struct {
		ID             contract.ID     `json:"id"`
		Version        int64           `json:"version"`
		Digest         contract.Digest `json:"digest"`
		State          string          `json:"state"`
		Classification string          `json:"classification"`
	} `json:"artifacts"`
}

type effectsPrepareInput struct {
	Scope    wireScope   `json:"scope"`
	Action   wireAction  `json:"action"`
	SourceID contract.ID `json:"source_id"`
}

type operationResourceBody struct {
	Resource wireOperation `json:"resource"`
}

type executionJobCreateInput struct {
	Scope     wireScope       `json:"scope"`
	Owner     string          `json:"owner"`
	Operation string          `json:"operation"`
	Input     json.RawMessage `json:"input"`
	SourceID  contract.ID     `json:"source_id"`
}

type executionJobRecordInput struct {
	JobID           contract.ID     `json:"job_id"`
	ExpectedVersion int64           `json:"expected_version"`
	Generation      int64           `json:"generation"`
	State           string          `json:"state"`
	Result          json.RawMessage `json:"result"`
	EvidenceIDs     []contract.ID   `json:"evidence_ids"`
}

type jobResourceBody struct {
	Resource wireJob `json:"resource"`
}

// wireArtifact mirrors $defs/Artifact.
type wireArtifact struct {
	ID             contract.ID     `json:"id"`
	Version        int64           `json:"version"`
	Scope          wireScope       `json:"scope"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Encrypted      bool            `json:"encrypted"`
	State          string          `json:"state"`
	CreatedAt      time.Time       `json:"created_at"`
}

type artifactsPublishInput struct {
	Scope          wireScope       `json:"scope"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Encrypted      bool            `json:"encrypted"`
}

type artifactResourceBody struct {
	Resource wireArtifact `json:"resource"`
}

// Serenity action parameters. Each is one strictly validated document under
// the adapter $defs, embedded as the logical action's inert parameters.

type serenityRecall struct {
	Schema              string      `json:"schema"`
	BrainID             contract.ID `json:"brain_id"`
	AdapterCommandID    contract.ID `json:"adapter_command_id"`
	Kind                string      `json:"kind"`
	Query               string      `json:"query"`
	MinimumFreshness    time.Time   `json:"minimum_freshness"`
	MaxClaims           int64       `json:"max_claims"`
	MaximumCost         wireMoney   `json:"maximum_cost"`
	AllowedDestinations []string    `json:"allowed_provider_destinations"`
	Classification      string      `json:"classification"`
}

type serenityRemember struct {
	Schema              string            `json:"schema"`
	BrainID             contract.ID       `json:"brain_id"`
	AdapterCommandID    contract.ID       `json:"adapter_command_id"`
	Kind                string            `json:"kind"`
	Text                string            `json:"text"`
	Sources             []wireArtifactRef `json:"sources"`
	WriterOwner         string            `json:"writer_owner"`
	MaximumCost         wireMoney         `json:"maximum_cost"`
	AllowedDestinations []string          `json:"allowed_provider_destinations"`
}

type serenityPromote struct {
	Schema                   string            `json:"schema"`
	BrainID                  contract.ID       `json:"brain_id"`
	AdapterCommandID         contract.ID       `json:"adapter_command_id"`
	Kind                     string            `json:"kind"`
	SourceBrainID            contract.ID       `json:"source_brain_id"`
	SourceClaim              wireRef           `json:"source_claim"`
	SourceDisclosureEvidence []wireArtifactRef `json:"source_disclosure_evidence"`
	Text                     string            `json:"text"`
	Sources                  []wireArtifactRef `json:"sources"`
	CuratorID                contract.ID       `json:"curator_id"`
	WriterOwner              string            `json:"writer_owner"`
	MaximumCost              wireMoney         `json:"maximum_cost"`
	AllowedDestinations      []string          `json:"allowed_provider_destinations"`
	Redaction                string            `json:"redaction,omitempty"`
}

type serenityRetract struct {
	Schema           string      `json:"schema"`
	BrainID          contract.ID `json:"brain_id"`
	AdapterCommandID contract.ID `json:"adapter_command_id"`
	Kind             string      `json:"kind"`
	Claim            wireRef     `json:"claim"`
	Reason           string      `json:"reason"`
	WriterOwner      string      `json:"writer_owner"`
	Removal          string      `json:"removal"`
}

// serenityEvidence decodes a recorded evidence document after schema
// validation. Required fields are enforced by the adapter schema; pointers
// mark optional evidence fields.
type serenityEvidence struct {
	Schema              string                   `json:"schema"`
	PhysicalCall        serenityPhysicalCall     `json:"physical_call"`
	Kind                string                   `json:"kind"`
	BrainID             contract.ID              `json:"brain_id"`
	AdapterCommandID    contract.ID              `json:"adapter_command_id"`
	CommandStatus       string                   `json:"command_status"`
	Claims              []serenityMemoryClaim    `json:"claims"`
	BrainRevisions      []serenityBrainRevision  `json:"brain_revisions"`
	Usage               serenityProviderUsage    `json:"usage"`
	StagedOutputs       []serenityStagedOutput   `json:"staged_outputs"`
	OutputArtifacts     []wireArtifactRef        `json:"output_artifacts"`
	WriterOwner         *string                  `json:"writer_owner,omitempty"`
	ActiveRecallRemoved *bool                    `json:"active_recall_removed,omitempty"`
	HistoricalErasure   *bool                    `json:"historical_erasure,omitempty"`
	SelectedContext     *serenityArtifactLocator `json:"selected_context,omitempty"`
	LookupAuthoritative *bool                    `json:"lookup_authoritative,omitempty"`
}

// serenityPhysicalCall decodes the one-physical-request evidence fields this
// owner acts on; the full document stays inert JSON in intent detail.
type serenityPhysicalCall struct {
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
}

// serenityBrainRevision decodes one brain revision attestation.
type serenityBrainRevision struct {
	BrainID       contract.ID     `json:"brain_id"`
	Revision      string          `json:"revision"`
	Digest        contract.Digest `json:"digest"`
	ObservedAt    time.Time       `json:"observed_at"`
	IndexRevision *string         `json:"index_revision,omitempty"`
}

// serenityMemoryClaim mirrors the adapter MemoryClaim shape.
type serenityMemoryClaim struct {
	ID            contract.ID       `json:"id"`
	Version       int64             `json:"version"`
	BrainID       contract.ID       `json:"brain_id"`
	Text          string            `json:"text"`
	Sources       []wireArtifactRef `json:"sources"`
	Confidence    int64             `json:"confidence"`
	Freshness     time.Time         `json:"freshness"`
	Active        bool              `json:"active"`
	SourceBrainID *contract.ID      `json:"source_brain_id,omitempty"`
	SourceClaim   *wireRef          `json:"source_claim,omitempty"`
	CuratorID     *contract.ID      `json:"curator_id,omitempty"`
	Redaction     string            `json:"redaction,omitempty"`
}

// serenityProviderUsage decodes the accounting usage; billing class governs
// whether spend is observed, estimated or unknown.
type serenityProviderUsage struct {
	Accounting wireUsage `json:"accounting"`
	Billing    string    `json:"billing"`
}

// serenityStagedOutput decodes the IO handoff entries; publication replaces
// them with real artifact references.
type serenityStagedOutput struct {
	StagingRef     string          `json:"staging_ref"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Purpose        string          `json:"purpose"`
}

// serenityArtifactLocator is the selected_context locator: either a
// published artifact or a staged handoff reference.
type serenityArtifactLocator struct {
	Kind       string           `json:"kind"`
	Artifact   *wireArtifactRef `json:"artifact,omitempty"`
	StagingRef string           `json:"staging_ref,omitempty"`
	Digest     contract.Digest  `json:"digest,omitempty"`
}
