package effects

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs. JSON is snake_case and unknown fields are rejected at the
// decode boundary before these types are filled. Output schemas describe
// Payload.Data, not the result envelope. Peer payloads are decoded with
// plain Unmarshal into the fields this package consumes, matching the
// scheduling module's trusted-peer convention; bytes that must survive
// verbatim (actions, evidence, parameters) stay json.RawMessage.

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
	ID                contract.ID   `json:"id"`
	Version           int64         `json:"version"`
	Action            wireAction    `json:"action"`
	ActionDigest      string        `json:"action_digest"`
	State             string        `json:"state"`
	AttemptIDs        []contract.ID `json:"attempt_ids"`
	LinkedOperationID *contract.ID  `json:"linked_operation_id,omitempty"`
	Relationship      *string       `json:"relationship,omitempty"`
}

// wireDispatch mirrors $defs/Dispatch.
type wireDispatch struct {
	OperationID   contract.ID     `json:"operation_id"`
	AttemptID     contract.ID     `json:"attempt_id"`
	Generation    int64           `json:"generation"`
	Adapter       string          `json:"adapter"`
	Action        json.RawMessage `json:"action"`
	CredentialRef string          `json:"credential_ref"`
	Deadline      time.Time       `json:"deadline"`
	ProviderKey   string          `json:"provider_key,omitempty"`
}

// wireObservation mirrors $defs/Observation.
type wireObservation struct {
	Disposition       string          `json:"disposition"`
	Evidence          json.RawMessage `json:"evidence"`
	Usage             wireUsage       `json:"usage"`
	ProviderReference string          `json:"provider_reference,omitempty"`
	ConfirmedAt       *time.Time      `json:"confirmed_at,omitempty"`
}

// wireDecisionRequirement mirrors $defs/DecisionRequirement.
type wireDecisionRequirement struct {
	ActionDigest       string        `json:"action_digest"`
	HumanRequired      bool          `json:"human_required"`
	EligiblePrincipals []contract.ID `json:"eligible_principals"`
	ExpiresAt          time.Time     `json:"expires_at"`
	SeparateProposer   bool          `json:"separate_proposer"`
}

// wireDecision mirrors $defs/Decision.
type wireDecision struct {
	ID            contract.ID `json:"id"`
	ReviewID      contract.ID `json:"review_id"`
	ReviewVersion int64       `json:"review_version"`
	ActionDigest  string      `json:"action_digest"`
	ReviewerID    contract.ID `json:"reviewer_id"`
	Decision      string      `json:"decision"`
	At            time.Time   `json:"at"`
	Reason        string      `json:"reason"`
}

// wirePolicyResult mirrors $defs/PolicyResult.
type wirePolicyResult struct {
	Decision     string                    `json:"decision"`
	Reasons      []string                  `json:"reasons"`
	Requirements []wireDecisionRequirement `json:"requirements"`
}

// wireReview mirrors $defs/Review.
type wireReview struct {
	ID           contract.ID             `json:"id"`
	Version      int64                   `json:"version"`
	Scope        wireScope               `json:"scope"`
	ActionDigest string                  `json:"action_digest"`
	Preview      wireAction              `json:"preview"`
	Requirement  wireDecisionRequirement `json:"requirement"`
	ProposerID   contract.ID             `json:"proposer_id"`
	State        string                  `json:"state"`
	DecisionID   *contract.ID            `json:"decision_id,omitempty"`
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

// wireReservation mirrors $defs/Reservation.
type wireReservation struct {
	ID          contract.ID  `json:"id"`
	Version     int64        `json:"version"`
	Scope       wireScope    `json:"scope"`
	RootTaskID  *contract.ID `json:"root_task_id,omitempty"`
	OperationID contract.ID  `json:"operation_id"`
	Amount      wireMoney    `json:"amount"`
	State       string       `json:"state"`
}

// wirePrincipal decodes the trusted _identity.authority payload fields this
// package acts on.
type wirePrincipal struct {
	ID      contract.ID `json:"id"`
	Revoked bool        `json:"revoked"`
}

// wireAuthority decodes _identity.authority output.
type wireAuthority struct {
	Principal    wirePrincipal `json:"principal"`
	Restrictions []string      `json:"restrictions"`
}

// wireConnection decodes the _connections.resolve connection fields this
// package dispatches with.
type wireConnection struct {
	ID              contract.ID `json:"id"`
	Version         int64       `json:"version"`
	CredentialRef   string      `json:"credential_ref"`
	ValidationState string      `json:"validation_state"`
	ValidUntil      *time.Time  `json:"valid_until,omitempty"`
}

// wireTool decodes the _connections.resolve tool fields this package uses
// to classify and bound dispatch.
type wireTool struct {
	ID             contract.ID `json:"id"`
	Version        int64       `json:"version"`
	Name           string      `json:"name"`
	Effect         string      `json:"effect"`
	CostBound      wireMoney   `json:"cost_bound"`
	TimeoutSeconds int64       `json:"timeout_seconds"`
	Idempotency    string      `json:"idempotency"`
	Adapter        string      `json:"adapter"`
}

// resolveBody decodes _connections.resolve output.
type resolveBody struct {
	Connection wireConnection `json:"connection"`
	Tool       wireTool       `json:"tool"`
}

// authorityBody decodes _identity.authority output.
type authorityBody struct {
	Resource wireAuthority `json:"resource"`
}

// policyResultBody decodes _policy.check output.
type policyResultBody struct {
	Resource wirePolicyResult `json:"resource"`
}

// reviewResourceBody decodes _reviews.ensure output.
type reviewResourceBody struct {
	Resource wireReview `json:"resource"`
}

// reviewsCheckBody decodes _reviews.check output.
type reviewsCheckBody struct {
	Eligible bool          `json:"eligible"`
	Decision *wireDecision `json:"decision,omitempty"`
}

// reservationResourceBody decodes _accounting.reserve and _accounting.settle
// output.
type reservationResourceBody struct {
	Resource wireReservation `json:"resource"`
}

// inspectBody decodes _accounting.inspect output.
type inspectBody struct {
	Limits wireLimits `json:"limits"`
	Usage  wireUsage  `json:"usage"`
}

// jobResourceBody decodes _execution.job.create output and carries the
// operation.reconcile response.
type jobResourceBody struct {
	Resource wireJob `json:"resource"`
}

// artifactsMetadataBody decodes _artifacts.metadata output.
type artifactsMetadataBody struct {
	Artifacts []wireArtifactState `json:"artifacts"`
}

// wireArtifactState decodes the availability fields of $defs/Artifact.
type wireArtifactState struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
	State  string          `json:"state"`
}

// snapshotBody decodes _configuration.snapshot output fields this package
// checks.
type snapshotBody struct {
	Resource struct {
		Scope    wireScope `json:"scope"`
		Revision int64     `json:"revision"`
	} `json:"resource"`
}

// taskBody decodes _tasks.snapshot output fields this package charges
// through.
type taskBody struct {
	Resource struct {
		ID      contract.ID  `json:"id"`
		Version int64        `json:"version"`
		Scope   wireScope    `json:"scope"`
		RootID  *contract.ID `json:"root_id,omitempty"`
		State   string       `json:"state"`
	} `json:"resource"`
}

// Operation inputs.

type prepareInput struct {
	Scope    wireScope   `json:"scope"`
	Action   wireAction  `json:"action"`
	SourceID contract.ID `json:"source_id"`
}

type admitInput struct {
	OperationID     contract.ID `json:"operation_id"`
	ExpectedVersion int64       `json:"expected_version"`
}

type claimInput struct {
	OperationID contract.ID `json:"operation_id"`
	AttemptID   contract.ID `json:"attempt_id"`
	Generation  int64       `json:"generation"`
}

type recordInput struct {
	OperationID contract.ID     `json:"operation_id"`
	AttemptID   contract.ID     `json:"attempt_id"`
	Generation  int64           `json:"generation"`
	Observation wireObservation `json:"observation"`
}

type pendingInput struct {
	Limit int64 `json:"limit"`
}

type proposeInput struct {
	Scope  wireScope  `json:"scope"`
	Action wireAction `json:"action"`
}

type getOperationInput struct {
	Scope wireScope   `json:"scope"`
	ID    contract.ID `json:"id"`
}

type operationFilter struct {
	State          *string      `json:"state,omitempty"`
	Key            *string      `json:"key,omitempty"`
	ParentID       *contract.ID `json:"parent_id,omitempty"`
	WorkerID       *contract.ID `json:"worker_id,omitempty"`
	TaskID         *contract.ID `json:"task_id,omitempty"`
	OrganizationID *contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool        `json:"descendants,omitempty"`
	NeedsYou       *bool        `json:"needs_you,omitempty"`
}

type listOperationsInput struct {
	Scope  wireScope        `json:"scope"`
	Cursor *string          `json:"cursor,omitempty"`
	Limit  *int64           `json:"limit,omitempty"`
	Filter *operationFilter `json:"filter,omitempty"`
}

type reconcileInput struct {
	Scope           wireScope   `json:"scope"`
	ID              contract.ID `json:"id"`
	ExpectedVersion int64       `json:"expected_version"`
}

// linkedProposeInput is the shared body of operation.compensation.propose
// and operation.replacement.propose.
type linkedProposeInput struct {
	Scope           wireScope   `json:"scope"`
	ID              contract.ID `json:"id"`
	ExpectedVersion int64       `json:"expected_version"`
	Action          wireAction  `json:"action"`
}

// Outputs.

type operationResourceBody struct {
	Resource wireOperation `json:"resource"`
}

type pendingOutput struct {
	Operations []wireOperation `json:"operations"`
}

type operationListOutput struct {
	Items []wireOperation `json:"items"`
}

// Peer call inputs.

type identityAuthorityInput struct {
	PrincipalID contract.ID `json:"principal_id"`
	Scope       wireScope   `json:"scope"`
}

type configurationSnapshotInput struct {
	Scope wireScope `json:"scope"`
}

type policyCheckInput struct {
	Scope      wireScope   `json:"scope"`
	Capability string      `json:"capability"`
	Action     *wireAction `json:"action,omitempty"`
}

type reviewsEnsureInput struct {
	Scope       wireScope               `json:"scope"`
	Action      wireAction              `json:"action"`
	Requirement wireDecisionRequirement `json:"requirement"`
}

type reviewsCheckInput struct {
	Scope        wireScope `json:"scope"`
	ActionDigest string    `json:"action_digest"`
}

type accountingReserveInput struct {
	Scope       wireScope    `json:"scope"`
	RootTaskID  *contract.ID `json:"root_task_id,omitempty"`
	OperationID contract.ID  `json:"operation_id"`
	Amount      wireMoney    `json:"amount"`
	Limits      wireLimits   `json:"limits"`
}

type accountingSettleInput struct {
	ReservationID   contract.ID `json:"reservation_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Usage           wireUsage   `json:"usage"`
	Nonexecution    bool        `json:"authoritative_nonexecution"`
}

type accountingInspectInput struct {
	Scope wireScope `json:"scope"`
}

type connectionsResolveInput struct {
	Scope       wireScope `json:"scope"`
	Connection  wireRef   `json:"connection"`
	Tool        wireRef   `json:"tool"`
	Destination string    `json:"destination"`
}

type connectionsValidationRecordInput struct {
	ConnectionID    contract.ID     `json:"connection_id"`
	ExpectedVersion int64           `json:"expected_version"`
	Observation     wireObservation `json:"observation"`
}

type connectionResourceBody struct {
	Resource wireConnection `json:"resource"`
}

// dispatchResourceBody carries the _effects.claim response.
type dispatchResourceBody struct {
	Resource wireDispatch `json:"resource"`
}

type tasksSnapshotInput struct {
	Scope wireScope   `json:"scope"`
	ID    contract.ID `json:"id"`
}

type artifactsMetadataInput struct {
	Scope     wireScope         `json:"scope"`
	Artifacts []wireArtifactRef `json:"artifacts"`
}

type executionJobCreateInput struct {
	Scope     wireScope       `json:"scope"`
	Owner     string          `json:"owner"`
	Operation string          `json:"operation"`
	Input     json.RawMessage `json:"input"`
	SourceID  contract.ID     `json:"source_id"`
}
