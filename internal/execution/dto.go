package execution

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire shapes mirrored from the frozen definitions in
// internal/execution/AGENTS.md. Handlers marshal these structs to canonical
// JSON and the merged output schemas validate them; peer responses decode
// into the same shapes. Scope is the shared contract.Scope directly — same
// snake_case tags.

type wireRef struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
}

type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

type wireUsage struct {
	Currency  string `json:"currency"`
	Spent     int64  `json:"spent"`
	Reserved  int64  `json:"reserved"`
	Estimated int64  `json:"estimated"`
	Unknown   int64  `json:"unknown"`
	Advisory  bool   `json:"advisory"`
}

type wireRequirement struct {
	Code       string      `json:"code"`
	Message    string      `json:"message"`
	ResourceID contract.ID `json:"resource_id,omitempty"`
}

type wireLimits struct {
	Currency        string `json:"currency"`
	SpendMicroUnits int64  `json:"spend_micro_units"`
	Concurrency     int64  `json:"concurrency"`
	ModelSteps      int64  `json:"model_steps"`
	ChildCount      int64  `json:"child_count"`
	DelegationDepth int64  `json:"delegation_depth"`
	AttemptSeconds  int64  `json:"attempt_seconds"`
	RootDeadline    string `json:"root_deadline"`
}

// wireExpectedObservation mirrors Adapter_ExpectedVerificationObservation.
// The exit-code bound arrives with the exit-code convention of the source
// document (a signed 32-bit range) and is carried opaquely.
type wireExpectedObservation struct {
	CheckID          string          `json:"check_id"`
	Kind             string          `json:"kind"`
	Expected         string          `json:"expected"`
	ArtifactName     string          `json:"artifact_name,omitempty"`
	ExpectedDigest   contract.Digest `json:"expected_digest,omitempty"`
	Schema           json.RawMessage `json:"schema,omitempty"`
	CommandID        string          `json:"command_id,omitempty"`
	ExpectedExitCode *int64          `json:"expected_exit_code,omitempty"`
}

type wireAcceptance struct {
	VerifierID           string                    `json:"verifier_id"`
	VerifierVersion      string                    `json:"verifier_version"`
	SealedInputs         []wireArtifactRef         `json:"sealed_inputs"`
	ExpectedObservations []wireExpectedObservation `json:"expected_observations"`
	Mode                 string                    `json:"mode"`
	RequiredChildIDs     []contract.ID             `json:"required_child_ids"`
	Profile              json.RawMessage           `json:"profile"`
}

type wireTask struct {
	ID                    contract.ID       `json:"id"`
	Version               contract.Version  `json:"version"`
	Scope                 contract.Scope    `json:"scope"`
	OwnerID               contract.ID       `json:"owner_id"`
	WorkerID              contract.ID       `json:"worker_id"`
	Outcome               string            `json:"outcome"`
	Inputs                []wireArtifactRef `json:"inputs"`
	RequiredOutputs       []string          `json:"required_outputs"`
	Acceptance            wireAcceptance    `json:"acceptance"`
	Limits                wireLimits        `json:"limits"`
	Dependencies          []contract.ID     `json:"dependencies"`
	State                 string            `json:"state"`
	ParentID              contract.ID       `json:"parent_id,omitempty"`
	RootID                contract.ID       `json:"root_id,omitempty"`
	WaitingReason         string            `json:"waiting_reason,omitempty"`
	CancellationRequested bool              `json:"cancellation_requested,omitempty"`
	ManualAcceptance      bool              `json:"manual_acceptance,omitempty"`
}

type wireExecutionProfile struct {
	ID                  contract.ID      `json:"id"`
	Version             contract.Version `json:"version"`
	Executor            string           `json:"executor"`
	Model               string           `json:"model"`
	ConnectionID        contract.ID      `json:"connection_id"`
	ProviderDestination string           `json:"provider_destination"`
	Capabilities        []string         `json:"capabilities"`
	CostBound           wireMoney        `json:"cost_bound"`
	Classification      string           `json:"classification"`
	ContextCapture      string           `json:"context_capture"`
	ConnectionVersion   contract.Version `json:"connection_version,omitempty"`
	AdapterProfile      json.RawMessage  `json:"adapter_profile,omitempty"`
}

type wireWorker struct {
	ID             contract.ID           `json:"id"`
	Version        contract.Version      `json:"version"`
	OrganizationID contract.ID           `json:"organization_id"`
	Key            string                `json:"key"`
	Name           string                `json:"name"`
	Purpose        string                `json:"purpose"`
	Instructions   string                `json:"instructions"`
	SkillVersions  []wireRef             `json:"skill_versions"`
	Bindings       []contract.ID         `json:"bindings"`
	Profile        *wireExecutionProfile `json:"profile"`
	Limits         *wireLimits           `json:"limits"`
}

type wireAttempt struct {
	ID              contract.ID      `json:"id"`
	Version         contract.Version `json:"version"`
	RunID           contract.ID      `json:"run_id"`
	WorkerID        contract.ID      `json:"worker_id"`
	Executor        string           `json:"executor"`
	Generation      int64            `json:"generation"`
	LeaseID         contract.ID      `json:"lease_id"`
	LeaseExpiresAt  string           `json:"lease_expires_at"`
	LastHeartbeat   string           `json:"last_heartbeat"`
	ReservationID   contract.ID      `json:"reservation_id"`
	State           string           `json:"state"`
	Capabilities    []string         `json:"capabilities"`
	ContextArtifact *wireArtifactRef `json:"context_artifact,omitempty"`
	RecoveryReason  string           `json:"recovery_reason,omitempty"`
}

type wireRun struct {
	ID                    contract.ID      `json:"id"`
	Version               contract.Version `json:"version"`
	TaskID                contract.ID      `json:"task_id"`
	ConfigurationRevision contract.Version `json:"configuration_revision"`
	InputVersions         []wireRef        `json:"input_versions"`
	State                 string           `json:"state"`
	AttemptIDs            []contract.ID    `json:"attempt_ids"`
}

type wireJob struct {
	ID             contract.ID       `json:"id"`
	Version        contract.Version  `json:"version"`
	Kind           string            `json:"kind"`
	State          string            `json:"state"`
	Requirements   []wireRequirement `json:"requirements"`
	ResultArtifact *wireArtifactRef  `json:"result_artifact,omitempty"`
	OperationID    contract.ID       `json:"operation_id,omitempty"`
	Owner          string            `json:"owner"`
	Operation      string            `json:"operation"`
	Result         json.RawMessage   `json:"result,omitempty"`
}

type wireDisposition struct {
	ID           contract.ID       `json:"id"`
	Version      contract.Version  `json:"version"`
	State        string            `json:"state"`
	Requirements []wireRequirement `json:"requirements,omitempty"`
}

type wireContext struct {
	AttemptID             contract.ID       `json:"attempt_id"`
	Artifact              wireArtifactRef   `json:"artifact"`
	ConfigurationRevision contract.Version  `json:"configuration_revision"`
	SourceArtifacts       []wireArtifactRef `json:"source_artifacts"`
	Capture               string            `json:"capture"`
}

type wireObservation struct {
	Disposition       string          `json:"disposition"`
	Evidence          json.RawMessage `json:"evidence"`
	Usage             wireUsage       `json:"usage"`
	ProviderReference string          `json:"provider_reference,omitempty"`
	ConfirmedAt       string          `json:"confirmed_at,omitempty"`
}

// wireBinding mirrors Binding: the scope's current tool/skill/connection/
// worker/repository/reporting/memory capability grants, resolved from
// configuration's scope snapshot. P15's context builder narrows a worker's
// authorized tools/memory to exactly the entries its own Bindings select.
type wireBinding struct {
	ID           contract.ID      `json:"id"`
	Version      contract.Version `json:"version"`
	Scope        contract.Scope   `json:"scope"`
	Kind         string           `json:"kind"`
	TargetID     contract.ID      `json:"target_id"`
	Permissions  []string         `json:"permissions"`
	SourceScope  *contract.Scope  `json:"source_scope,omitempty"`
	Destinations []string         `json:"destinations,omitempty"`
}

// wireMessage mirrors Message: one admitted inbox item. Content is always
// untrusted -- the context builder never treats a message body as a grant.
type wireMessage struct {
	ID             contract.ID       `json:"id"`
	Version        contract.Version  `json:"version"`
	SenderID       contract.ID       `json:"sender_id"`
	RecipientIDs   []contract.ID     `json:"recipient_ids"`
	Scope          contract.Scope    `json:"scope"`
	TaskIDs        []contract.ID     `json:"task_ids"`
	Body           string            `json:"body"`
	Attachments    []wireArtifactRef `json:"attachments"`
	State          string            `json:"state"`
	CreatedAt      string            `json:"created_at"`
	ConversationID contract.ID       `json:"conversation_id,omitempty"`
}

// wireMemoryBinding mirrors MemoryBinding: one authorized memory grant
// _memory.select returned after filtering the caller's requested binding
// IDs to what the current permission/freshness bound actually admits.
type wireMemoryBinding struct {
	ID             contract.ID      `json:"id"`
	Version        contract.Version `json:"version"`
	Scope          contract.Scope   `json:"scope"`
	BrainID        contract.ID      `json:"brain_id"`
	Permissions    []string         `json:"permissions"`
	Classification string           `json:"classification"`
}

// wireTool mirrors Tool: the adapter action shape a resolved binding names.
// P15 carries only fields its context assembly and action construction
// need; extra frozen fields decode and are ignored by Go's default
// (non-strict) json.Unmarshal used for peer responses.
type wireTool struct {
	CostBound    wireMoney        `json:"cost_bound"`
	ID           contract.ID      `json:"id"`
	Version      contract.Version `json:"version"`
	Name         string           `json:"name"`
	InputSchema  json.RawMessage  `json:"input_schema"`
	OutputSchema json.RawMessage  `json:"output_schema"`
	Effect       string           `json:"effect"`
	Destinations []string         `json:"destinations"`
	Adapter      string           `json:"adapter"`
}

// wireConnection mirrors Connection: the validated account/credential
// binding a resolved model-dispatch or product tool call is authorized
// under.
type wireConnection struct {
	ID              contract.ID      `json:"id"`
	Version         contract.Version `json:"version"`
	Provider        string           `json:"provider"`
	AccountIdentity string           `json:"account_identity"`
	Destinations    []string         `json:"destinations"`
}

// wireArtifact mirrors the Artifact definition for peer metadata reads.
type wireArtifact struct {
	ID             contract.ID      `json:"id"`
	Version        contract.Version `json:"version"`
	Scope          contract.Scope   `json:"scope"`
	Digest         contract.Digest  `json:"digest"`
	Size           int64            `json:"size"`
	MediaType      string           `json:"media_type"`
	Classification string           `json:"classification"`
	Encrypted      bool             `json:"encrypted"`
	State          string           `json:"state"`
	CreatedAt      string           `json:"created_at"`
}

// attemptOut renders an attempt row as its wire shape.
func attemptOut(a *attemptRow) wireAttempt {
	out := wireAttempt{
		Version:        a.Version,
		RunID:          a.RunID,
		WorkerID:       a.WorkerID,
		Executor:       a.Executor,
		Generation:     a.Generation,
		LeaseID:        a.LeaseID,
		LeaseExpiresAt: formatStamp(a.LeaseExpiresAt),
		LastHeartbeat:  formatStamp(a.LastHeartbeat),
		ReservationID:  a.ReservationID,
		State:          a.State,
		RecoveryReason: a.RecoveryReason,
	}
	out.ID = a.ID
	if len(a.Capabilities) > 0 {
		out.Capabilities = a.Capabilities
	} else {
		out.Capabilities = []string{}
	}
	if a.ContextArtifact != nil {
		ref := *a.ContextArtifact
		out.ContextArtifact = &ref
	}
	return out
}

// runOut renders a run row as its wire shape.
func runOut(r *runRow) wireRun {
	out := wireRun{
		Version:               r.Version,
		TaskID:                r.TaskID,
		ConfigurationRevision: r.ConfigurationRevision,
		State:                 r.State,
	}
	out.ID = r.ID
	out.InputVersions = r.InputVersions
	if out.InputVersions == nil {
		out.InputVersions = []wireRef{}
	}
	out.AttemptIDs = r.AttemptIDs
	if out.AttemptIDs == nil {
		out.AttemptIDs = []contract.ID{}
	}
	return out
}

// Revision 3 wire shapes: the durable WorkerTurn/ProposalRecord pipeline.

// wireTurnSource mirrors TurnSource.
type wireTurnSource struct {
	Kind              string           `json:"kind"`
	SourceID          contract.ID      `json:"source_id"`
	SourceVersion     contract.Version `json:"source_version"`
	RecipientWorkerID contract.ID      `json:"recipient_worker_id,omitempty"`
}

// wireWorkerTurn mirrors WorkerTurn.
type wireWorkerTurn struct {
	ID                    contract.ID      `json:"id"`
	WorkerID              contract.ID      `json:"worker_id"`
	PrincipalID           contract.ID      `json:"principal_id"`
	Scope                 contract.Scope   `json:"scope"`
	Source                wireTurnSource   `json:"source"`
	RequesterID           contract.ID      `json:"requester_id"`
	Version               contract.Version `json:"version"`
	ConfigurationRevision contract.Version `json:"configuration_revision"`
	State                 string           `json:"state"`
	Generation            int64            `json:"generation"`
	Limits                wireLimits       `json:"limits"`
	RootID                contract.ID      `json:"root_id"`
	StepsUsed             int64            `json:"steps_used"`
	CreatedAt             string           `json:"created_at"`
	UpdatedAt             string           `json:"updated_at"`
	ConversationID        contract.ID      `json:"conversation_id,omitempty"`
	TaskID                contract.ID      `json:"task_id,omitempty"`
	RunID                 contract.ID      `json:"run_id,omitempty"`
	AttemptID             contract.ID      `json:"attempt_id,omitempty"`
	WaitingReason         string           `json:"waiting_reason,omitempty"`
	WaitingResourceID     contract.ID      `json:"waiting_resource_id,omitempty"`
	NextWake              string           `json:"next_wake,omitempty"`
	LeaseID               contract.ID      `json:"lease_id,omitempty"`
	LeaseExpiresAt        string           `json:"lease_expires_at,omitempty"`
	ContextArtifact       *wireArtifactRef `json:"context_artifact,omitempty"`
	LastObservationID     contract.ID      `json:"last_observation_id,omitempty"`
}

// wireProposalRecord mirrors ProposalRecord.
type wireProposalRecord struct {
	TurnID              contract.ID      `json:"turn_id"`
	StepIndex           int64            `json:"step_index"`
	ProposalID          string           `json:"proposal_id"`
	SourceContextDigest contract.Digest  `json:"source_context_digest"`
	NormalizedProposal  json.RawMessage  `json:"normalized_proposal"`
	State               string           `json:"state"`
	CreatedAt           string           `json:"created_at"`
	UpdatedAt           string           `json:"updated_at"`
	CommandID           contract.ID      `json:"command_id,omitempty"`
	EffectOperationID   contract.ID      `json:"effect_operation_id,omitempty"`
	ResultArtifact      *wireArtifactRef `json:"result_artifact,omitempty"`
}

// wireContextPlan mirrors ContextPlan.
type wireContextPlan struct {
	ID                    contract.ID       `json:"id"`
	TurnID                contract.ID       `json:"turn_id"`
	ExpectedVersion       contract.Version  `json:"expected_version"`
	Generation            int64             `json:"generation"`
	Scope                 contract.Scope    `json:"scope"`
	AttemptID             contract.ID       `json:"attempt_id,omitempty"`
	Refs                  []wireArtifactRef `json:"refs"`
	ConfigurationRevision contract.Version  `json:"configuration_revision"`
	ByteBound             int64             `json:"byte_bound"`
	TokenBound            int64             `json:"token_bound"`
	Recipe                json.RawMessage   `json:"recipe"`
}

// wireWorkItem mirrors WorkItem.
type wireWorkItem struct {
	ID    contract.ID    `json:"id"`
	Kind  string         `json:"kind"`
	Scope contract.Scope `json:"scope"`
	Turn  wireWorkerTurn `json:"turn"`
	RunID contract.ID    `json:"run_id,omitempty"`
}

// wireArtifactLocator mirrors Adapter_ArtifactLocator: kind "artifact" names
// a published ArtifactRef, kind "staged" names an unpublished staging
// reference/digest. The schema's oneOf enforces the exact field set for
// each kind; strict decoding here only shapes the fields, never chooses
// between them.
type wireArtifactLocator struct {
	Kind       string           `json:"kind"`
	Artifact   *wireArtifactRef `json:"artifact,omitempty"`
	StagingRef string           `json:"staging_ref,omitempty"`
	Digest     contract.Digest  `json:"digest,omitempty"`
}

// turnOut renders a turn row as its wire shape.
func turnOut(t *turnRow) wireWorkerTurn {
	out := wireWorkerTurn{
		WorkerID:              t.WorkerID,
		PrincipalID:           t.PrincipalID,
		Scope:                 t.Scope,
		Source:                t.Source,
		RequesterID:           t.RequesterID,
		Version:               t.Version,
		ConfigurationRevision: t.ConfigurationRevision,
		State:                 t.State,
		Generation:            t.Generation,
		Limits:                t.Limits,
		RootID:                t.RootID,
		StepsUsed:             t.StepsUsed,
		CreatedAt:             formatStamp(t.CreatedAt),
		UpdatedAt:             formatStamp(t.UpdatedAt),
		ConversationID:        t.ConversationID,
		TaskID:                t.TaskID,
		RunID:                 t.RunID,
		AttemptID:             t.AttemptID,
		WaitingReason:         t.WaitingReason,
		WaitingResourceID:     t.WaitingResourceID,
		NextWake:              formatStamp(t.NextWake),
		LeaseID:               t.LeaseID,
		LeaseExpiresAt:        formatStamp(t.LeaseExpiresAt),
		LastObservationID:     t.LastObservationID,
	}
	out.ID = t.ID
	if t.ContextArtifact != nil {
		ref := *t.ContextArtifact
		out.ContextArtifact = &ref
	}
	return out
}

// proposalOut renders a proposal row as its wire shape.
func proposalOut(p *proposalRow) wireProposalRecord {
	out := wireProposalRecord{
		TurnID:              p.TurnID,
		StepIndex:           p.StepIndex,
		ProposalID:          p.ProposalID,
		SourceContextDigest: p.SourceContextDigest,
		NormalizedProposal:  p.NormalizedProposal,
		State:               p.State,
		CreatedAt:           formatStamp(p.CreatedAt),
		UpdatedAt:           formatStamp(p.UpdatedAt),
		CommandID:           p.CommandID,
		EffectOperationID:   p.EffectOperationID,
	}
	if len(out.NormalizedProposal) == 0 {
		out.NormalizedProposal = json.RawMessage(`{}`)
	}
	if p.ResultArtifact != nil {
		ref := *p.ResultArtifact
		out.ResultArtifact = &ref
	}
	return out
}

// contextPlanOut renders a context plan row as its wire shape.
func contextPlanOut(p *contextPlanRow) (wireContextPlan, error) {
	recipe, err := json.Marshal(p.Recipe)
	if err != nil {
		return wireContextPlan{}, err
	}
	out := wireContextPlan{
		TurnID:                p.TurnID,
		ExpectedVersion:       p.ExpectedVersion,
		Generation:            p.Generation,
		Scope:                 p.Recipe.Scope,
		AttemptID:             p.Recipe.AttemptID,
		Refs:                  p.Refs,
		ConfigurationRevision: p.ConfigurationRevision,
		ByteBound:             p.ByteBound,
		TokenBound:            p.TokenBound,
		Recipe:                recipe,
	}
	out.ID = p.ID
	if out.Refs == nil {
		out.Refs = []wireArtifactRef{}
	}
	return out, nil
}

// workItemOut renders one turn as a work item of the given kind.
func workItemOut(kind string, t *turnRow) wireWorkItem {
	return wireWorkItem{
		ID:    t.ID,
		Kind:  kind,
		Scope: t.Scope,
		Turn:  turnOut(t),
		RunID: t.RunID,
	}
}

// jobOut renders a job row as its wire shape.
func jobOut(j *jobRow) wireJob {
	out := wireJob{
		Version:   j.Version,
		Kind:      j.Kind,
		State:     j.State,
		Owner:     j.Owner,
		Operation: j.Operation,
	}
	out.ID = j.ID
	out.Requirements = j.Requirements
	if out.Requirements == nil {
		out.Requirements = []wireRequirement{}
	}
	if j.ResultArtifact != nil {
		ref := *j.ResultArtifact
		out.ResultArtifact = &ref
	}
	if j.OperationID != "" {
		out.OperationID = j.OperationID
	}
	if len(j.Result) > 0 {
		out.Result = json.RawMessage(j.Result)
	}
	return out
}
