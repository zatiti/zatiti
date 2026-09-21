package controller

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire shapes of the durable worker-turn pipeline operations (P14/P15/P16)
// the controller drives. They mirror the frozen operation schemas exactly;
// as with wire.go, the controller depends on no owner's Go types.

// WorkItem kinds.
const (
	workKindClaim    = "claim"
	workKindContext  = "context"
	workKindProposal = "proposal"
	workKindResume   = "resume"
)

// Normalized-proposal kinds (execution's own private record shape, read only
// far enough to route -- never trusted as authority; the owner already
// decided these deterministically).
const (
	proposalKindLocalOperation = "local_operation"
	proposalKindExternalTool   = "external_tool"
)

type wireTurnSource struct {
	Kind              string      `json:"kind"`
	SourceID          contract.ID `json:"source_id"`
	SourceVersion     int64       `json:"source_version"`
	RecipientWorkerID contract.ID `json:"recipient_worker_id,omitempty"`
}

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

type wireWorkerTurn struct {
	ID                    contract.ID    `json:"id"`
	WorkerID              contract.ID    `json:"worker_id"`
	PrincipalID           contract.ID    `json:"principal_id"`
	Scope                 contract.Scope `json:"scope"`
	Source                wireTurnSource `json:"source"`
	RequesterID           contract.ID    `json:"requester_id"`
	Version               int64          `json:"version"`
	ConfigurationRevision int64          `json:"configuration_revision"`
	State                 string         `json:"state"`
	Generation            int64          `json:"generation"`
	Limits                wireLimits     `json:"limits"`
	RootID                contract.ID    `json:"root_id"`
	StepsUsed             int64          `json:"steps_used"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
	ConversationID        contract.ID    `json:"conversation_id,omitempty"`
	TaskID                contract.ID    `json:"task_id,omitempty"`
	RunID                 contract.ID    `json:"run_id,omitempty"`
	AttemptID             contract.ID    `json:"attempt_id,omitempty"`
	WaitingReason         string         `json:"waiting_reason,omitempty"`
	WaitingResourceID     contract.ID    `json:"waiting_resource_id,omitempty"`
	NextWake              time.Time      `json:"next_wake,omitempty"`
	LeaseID               contract.ID    `json:"lease_id,omitempty"`
	LeaseExpiresAt        time.Time      `json:"lease_expires_at,omitempty"`
	ContextArtifact       *wireArtifact  `json:"context_artifact,omitempty"`
	LastObservationID     contract.ID    `json:"last_observation_id,omitempty"`
}

type wireWorkItem struct {
	ID    contract.ID    `json:"id"`
	Kind  string         `json:"kind"`
	Scope contract.Scope `json:"scope"`
	Turn  wireWorkerTurn `json:"turn"`
	RunID contract.ID    `json:"run_id,omitempty"`
}

type wireContextPlan struct {
	ID                    contract.ID    `json:"id"`
	TurnID                contract.ID    `json:"turn_id"`
	ExpectedVersion       int64          `json:"expected_version"`
	Generation            int64          `json:"generation"`
	Refs                  []wireArtifact `json:"refs"`
	ConfigurationRevision int64          `json:"configuration_revision"`
	ByteBound             int64          `json:"byte_bound"`
	TokenBound            int64          `json:"token_bound"`
}

// wireCallbackRoute mirrors $defs/CallbackRoute: kind plus whichever of
// turn_id/step_index/job_id its kind uses.
type wireCallbackRoute struct {
	Kind      string      `json:"kind"`
	TurnID    contract.ID `json:"turn_id,omitempty"`
	StepIndex *int64      `json:"step_index,omitempty"`
	JobID     contract.ID `json:"job_id,omitempty"`
}

// wireArtifactLocator mirrors $defs/Adapter_ArtifactLocator, a strict oneOf:
// only the fields of the selected branch are ever populated together.
type wireArtifactLocator struct {
	Kind       string          `json:"kind"`
	Artifact   *wireArtifact   `json:"artifact,omitempty"`
	StagingRef string          `json:"staging_ref,omitempty"`
	Digest     contract.Digest `json:"digest,omitempty"`
}

type wireProposalRecord struct {
	TurnID              contract.ID     `json:"turn_id"`
	StepIndex           int64           `json:"step_index"`
	ProposalID          string          `json:"proposal_id"`
	SourceContextDigest string          `json:"source_context_digest"`
	NormalizedProposal  json.RawMessage `json:"normalized_proposal"`
	State               string          `json:"state"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	CommandID           contract.ID     `json:"command_id,omitempty"`
	EffectOperationID   contract.ID     `json:"effect_operation_id,omitempty"`
	ResultArtifact      *wireArtifact   `json:"result_artifact,omitempty"`
}

// normalizedProposalProbe reads only the inert "kind"/operation discriminator
// fields the controller needs to route a prepared proposal -- never trusted
// as authorization, only as a hint of which already-authorized path to
// drive next.
type normalizedProposalProbe struct {
	Kind             string          `json:"kind"`
	Operation        string          `json:"operation,omitempty"`
	OperationVersion int64           `json:"operation_version,omitempty"`
	Input            json.RawMessage `json:"input,omitempty"`
}

type wireMessage struct {
	ID             contract.ID    `json:"id"`
	Version        int64          `json:"version"`
	SenderID       contract.ID    `json:"sender_id"`
	RecipientIDs   []contract.ID  `json:"recipient_ids"`
	Scope          contract.Scope `json:"scope"`
	TaskIDs        []contract.ID  `json:"task_ids"`
	Body           string         `json:"body"`
	Attachments    []wireArtifact `json:"attachments"`
	State          string         `json:"state"`
	CreatedAt      time.Time      `json:"created_at"`
	ConversationID contract.ID    `json:"conversation_id,omitempty"`
}

// ---- Inputs ----

type turnAdmitInput struct {
	Source      wireTurnSource `json:"source"`
	WorkerID    contract.ID    `json:"worker_id"`
	Scope       contract.Scope `json:"scope"`
	RequesterID contract.ID    `json:"requester_id"`
}

type workClaimInput struct {
	WorkID          contract.ID `json:"work_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Generation      int64       `json:"generation"`
}

type contextPrepareInput struct {
	TurnID          contract.ID `json:"turn_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Generation      int64       `json:"generation"`
}

type contextCommitInput struct {
	PlanID          contract.ID         `json:"plan_id"`
	ExpectedVersion int64               `json:"expected_version"`
	Generation      int64               `json:"generation"`
	StagedContext   wireArtifactLocator `json:"staged_context"`
}

type proposalPrepareInput struct {
	TurnID          contract.ID `json:"turn_id"`
	StepIndex       int64       `json:"step_index"`
	ProposalID      string      `json:"proposal_id"`
	ExpectedVersion int64       `json:"expected_version"`
}

type proposalRecordInput struct {
	ProposalID        string        `json:"proposal_id"`
	ExpectedVersion   int64         `json:"expected_version"`
	CommandID         contract.ID   `json:"command_id,omitempty"`
	EffectOperationID contract.ID   `json:"effect_operation_id,omitempty"`
	ResultArtifact    *wireArtifact `json:"result_artifact,omitempty"`
}

type executionReportInput struct {
	Scope           contract.Scope  `json:"scope"`
	AttemptID       contract.ID     `json:"attempt_id"`
	LeaseID         contract.ID     `json:"lease_id"`
	Generation      int64           `json:"generation"`
	ExpectedVersion int64           `json:"expected_version"`
	Outputs         []wireArtifact  `json:"outputs"`
	Observations    json.RawMessage `json:"observations"`
	Usage           wireUsage       `json:"usage"`
}

type verificationClaimInput struct {
	RequestID       contract.ID `json:"request_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Generation      int64       `json:"generation"`
}

type verificationRecordInput struct {
	AttemptID       contract.ID     `json:"attempt_id"`
	ExpectedVersion int64           `json:"expected_version"`
	Result          json.RawMessage `json:"result"`
}

// ---- Outputs ----

type messagesOutput struct {
	Items []wireMessage `json:"items"`
}

type turnOutput struct {
	Resource wireWorkerTurn `json:"resource"`
}

type workItemsOutput struct {
	Items []wireWorkItem `json:"items"`
}

type workClaimOutput struct {
	Item       wireWorkItem `json:"item"`
	ClaimToken string       `json:"claim_token"`
	Version    int64        `json:"version"`
}

type contextPlanOutput struct {
	Resource wireContextPlan `json:"resource"`
}

type proposalOutput struct {
	Resource wireProposalRecord `json:"resource"`
}

type verificationItemsOutput struct {
	Items []json.RawMessage `json:"items"`
}

type verificationClaimOutput struct {
	Request    json.RawMessage `json:"request"`
	ClaimToken string          `json:"claim_token"`
}

// verificationRequestProbe reads only the fields the controller needs from
// an opaque Adapter_VerificationRequest document to drive the trusted
// verifier and record its result.
type verificationRequestProbe struct {
	JobID     contract.ID    `json:"job_id"`
	TaskID    contract.ID    `json:"task_id"`
	AttemptID contract.ID    `json:"attempt_id"`
	Scope     contract.Scope `json:"scope"`
}
