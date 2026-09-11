package scheduling

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs. Types decoded from THIS package's own operation inputs are
// decoded strictly (unknown fields rejected) after schema validation; the
// structs below therefore cover exactly the fields the brief schemas allow.
// Types decoded from peer responses are read tolerantly with encoding/json
// because the owning peer already validated its output against the shared
// schema before sending; only the fields this package consumes are declared.

// Change kind and action constants for compiler candidates. Actions are
// exactly create, update and archive; other actions are refused.
const (
	kindSchedule       = "schedule"
	kindResponsibility = "responsibility"

	changeCreate  = "create"
	changeUpdate  = "update"
	changeArchive = "archive"
)

// Misfire policies.
const (
	misfireCoalesce = "coalesce"
	misfireSkip     = "skip"
)

// Wake source kinds and occurrence states.
const (
	sourceSchedule       = "schedule"
	sourceResponsibility = "responsibility"

	occurrenceAdmitted = "admitted"
	occurrenceSkipped  = "skipped"
)

// Restrictive states reported by pause/resume dispositions.
const (
	statePaused = "paused"
	stateActive = "active"
)

// Shared reference shapes.

// wireRef is the shared Ref definition: an exact resource identity and
// version.
type wireRef struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
}

// wireArtifactRef is the shared ArtifactRef definition.
type wireArtifactRef struct {
	ID     contract.ID `json:"id"`
	Digest string      `json:"digest"`
}

// wireDiagnostic is the shared Diagnostic definition.
type wireDiagnostic struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// wireRequirement is the shared Requirement definition.
type wireRequirement struct {
	Code       string      `json:"code"`
	Message    string      `json:"message"`
	ResourceID contract.ID `json:"resource_id,omitempty"`
}

// Draft and compiler candidate shapes (shared definitions).

// wireDraft is the shared Draft definition. Changes are inert JSON objects
// on this package's boundary; only configuration interprets them.
type wireDraft struct {
	ID           contract.ID       `json:"id"`
	Version      contract.Version  `json:"version"`
	BaseRevision int64             `json:"base_revision"`
	Changes      []json.RawMessage `json:"changes"`
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
}

// wireChange is one sealed candidate change.
type wireChange struct {
	Kind            string          `json:"kind"`
	Action          string          `json:"action"`
	ID              contract.ID     `json:"id"`
	ExpectedVersion int64           `json:"expected_version"`
	Definition      json.RawMessage `json:"definition"`
}

// candidateInput is the shared Candidate definition.
type candidateInput struct {
	PlanID          contract.ID  `json:"plan_id"`
	BaseRevision    int64        `json:"base_revision"`
	CandidateDigest string       `json:"candidate_digest"`
	Changes         []wireChange `json:"changes"`
	Dependencies    []wireRef    `json:"dependencies"`
}

// candidateEnvelope wraps the candidate the way the schema requires.
type candidateEnvelope struct {
	Candidate candidateInput `json:"candidate"`
}

// wireValidation is the shared Validation definition: one owner's candidate
// slice validation result.
type wireValidation struct {
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
	Requirements []wireRequirement `json:"requirements"`
	Dependencies []wireRef         `json:"dependencies"`
}

// Owned resource shapes.

// wireLimits is the shared Limits definition: the finite bounds one bounded
// run must stay inside. Every field is required by the schema.
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

// wireExpectedObservation is the shared Adapter_ExpectedVerificationObservation
// definition: one check a verifier runs against the task's outputs.
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

// wireAcceptance is the shared Acceptance definition. The verification profile
// is an adapter-owned wire shape this package carries verbatim and never
// interprets.
type wireAcceptance struct {
	VerifierID           string                    `json:"verifier_id"`
	VerifierVersion      string                    `json:"verifier_version"`
	SealedInputs         []wireArtifactRef         `json:"sealed_inputs"`
	ExpectedObservations []wireExpectedObservation `json:"expected_observations"`
	Mode                 string                    `json:"mode"`
	RequiredChildIDs     []contract.ID             `json:"required_child_ids"`
	Profile              json.RawMessage           `json:"profile"`
}

// wireTask is the shared Task definition: the exact task template a schedule
// admits or the bounded cycle task a responsibility creates.
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

// wireSchedule is the shared Schedule definition.
type wireSchedule struct {
	ID             contract.ID      `json:"id"`
	Version        contract.Version `json:"version"`
	Scope          contract.Scope   `json:"scope"`
	TaskTemplate   wireTask         `json:"task_template"`
	Timezone       string           `json:"timezone"`
	Expression     string           `json:"expression"`
	Misfire        string           `json:"misfire"`
	CatchUpSeconds int64            `json:"catch_up_seconds"`
	Paused         bool             `json:"paused"`
	NextWake       *time.Time       `json:"next_wake,omitempty"`
}

// wireResponsibility is the shared Responsibility definition: one ongoing
// reasoning cycle owned by a worker.
type wireResponsibility struct {
	ID                   contract.ID      `json:"id"`
	Version              contract.Version `json:"version"`
	Scope                contract.Scope   `json:"scope"`
	WorkerID             contract.ID      `json:"worker_id"`
	Outcome              string           `json:"outcome"`
	Signals              []string         `json:"signals"`
	Triggers             []string         `json:"triggers"`
	ReasoningPolicy      string           `json:"reasoning_policy"`
	MinIntervalSeconds   int64            `json:"min_interval_seconds"`
	CycleLimits          wireLimits       `json:"cycle_limits"`
	AggregateLimits      wireLimits       `json:"aggregate_limits"`
	PauseConditions      []string         `json:"pause_conditions"`
	EscalationConditions []string         `json:"escalation_conditions"`
	Acceptance           wireAcceptance   `json:"acceptance"`
	Paused               bool             `json:"paused"`
	NextWake             *time.Time       `json:"next_wake,omitempty"`
}

// wireWake is the shared Wake definition: one durable wake condition.
type wireWake struct {
	ID               contract.ID      `json:"id"`
	Scope            contract.Scope   `json:"scope"`
	SourceID         contract.ID      `json:"source_id"`
	OccurrenceKey    string           `json:"occurrence_key"`
	DueAt            time.Time        `json:"due_at"`
	ConditionVersion contract.Version `json:"condition_version"`
}

// wireDisposition is the shared Disposition definition: the immediate
// restrictive state a pause/resume reports. Job, operation and requirement
// detail stay absent for scheduling's immediate commits.
type wireDisposition struct {
	ID           contract.ID       `json:"id"`
	Version      contract.Version  `json:"version"`
	State        string            `json:"state"`
	Requirements []wireRequirement `json:"requirements,omitempty"`
}

// Input DTOs for owned operations.

type scheduleDefinitionInput struct {
	Scope          contract.Scope `json:"scope"`
	TaskTemplate   wireTask       `json:"task_template"`
	Timezone       string         `json:"timezone"`
	Expression     string         `json:"expression"`
	Misfire        string         `json:"misfire"`
	CatchUpSeconds int64          `json:"catch_up_seconds"`
	Paused         bool           `json:"paused"`
}

type scheduleCreateInput struct {
	Scope      contract.Scope          `json:"scope"`
	Definition scheduleDefinitionInput `json:"definition"`
	DraftID    contract.ID             `json:"draft_id,omitempty"`
}

type scheduleUpdateInput struct {
	Scope           contract.Scope          `json:"scope"`
	ID              contract.ID             `json:"id"`
	ExpectedVersion contract.Version        `json:"expected_version"`
	Definition      scheduleDefinitionInput `json:"definition"`
	DraftID         contract.ID             `json:"draft_id,omitempty"`
}

type scheduleArchiveInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	DraftID         contract.ID      `json:"draft_id,omitempty"`
}

type scheduleGetInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

type responsibilityDefinitionInput struct {
	Scope                contract.Scope `json:"scope"`
	WorkerID             contract.ID    `json:"worker_id"`
	Outcome              string         `json:"outcome"`
	Signals              []string       `json:"signals"`
	Triggers             []string       `json:"triggers"`
	ReasoningPolicy      string         `json:"reasoning_policy"`
	MinIntervalSeconds   int64          `json:"min_interval_seconds"`
	CycleLimits          wireLimits     `json:"cycle_limits"`
	AggregateLimits      wireLimits     `json:"aggregate_limits"`
	PauseConditions      []string       `json:"pause_conditions"`
	EscalationConditions []string       `json:"escalation_conditions"`
	Acceptance           wireAcceptance `json:"acceptance"`
	Paused               bool           `json:"paused"`
}

type responsibilityCreateInput struct {
	Scope      contract.Scope                `json:"scope"`
	Definition responsibilityDefinitionInput `json:"definition"`
	DraftID    contract.ID                   `json:"draft_id,omitempty"`
}

type responsibilityUpdateInput struct {
	Scope           contract.Scope                `json:"scope"`
	ID              contract.ID                   `json:"id"`
	ExpectedVersion contract.Version              `json:"expected_version"`
	Definition      responsibilityDefinitionInput `json:"definition"`
	DraftID         contract.ID                   `json:"draft_id,omitempty"`
}

type responsibilityArchiveInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	DraftID         contract.ID      `json:"draft_id,omitempty"`
}

type responsibilityGetInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

type pauseResumeInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
}

// listFilter is the shared structured list filter. Pointer fields
// distinguish an absent filter from an empty one; fields the listed resource
// does not carry are refused as invalid_input by the list handlers.
type listFilter struct {
	State          *string      `json:"state,omitempty"`
	Key            *string      `json:"key,omitempty"`
	ParentID       *contract.ID `json:"parent_id,omitempty"`
	WorkerID       *contract.ID `json:"worker_id,omitempty"`
	TaskID         *contract.ID `json:"task_id,omitempty"`
	OrganizationID *contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool        `json:"descendants,omitempty"`
	NeedsYou       *bool        `json:"needs_you,omitempty"`
}

type listInput struct {
	Scope  contract.Scope `json:"scope"`
	Cursor *string        `json:"cursor,omitempty"`
	Limit  *int64         `json:"limit,omitempty"`
	Filter *listFilter    `json:"filter,omitempty"`
}

type wakeDueInput struct {
	Now   time.Time `json:"now"`
	Limit int64     `json:"limit"`
}

type wakeAdmitInput struct {
	Wake wireWake `json:"wake"`
}

type cycleRecordInput struct {
	ResponsibilityID contract.ID       `json:"responsibility_id"`
	ExpectedVersion  contract.Version  `json:"expected_version"`
	NextWake         time.Time         `json:"next_wake"`
	Outputs          []wireArtifactRef `json:"outputs"`
	TaskIDs          []contract.ID     `json:"task_ids"`
}

// Output bodies for owned operations. Resource and items keys are literal.

type scheduleBody struct {
	Resource wireSchedule `json:"resource"`
}

type responsibilityBody struct {
	Resource wireResponsibility `json:"resource"`
}

type stagedScheduleBody struct {
	Draft    wireDraft    `json:"draft"`
	Resource wireSchedule `json:"resource"`
}

type stagedResponsibilityBody struct {
	Draft    wireDraft          `json:"draft"`
	Resource wireResponsibility `json:"resource"`
}

type dispositionBody struct {
	Resource wireDisposition `json:"resource"`
}

type versionsBody struct {
	Versions []wireRef `json:"versions"`
}

type validationBody struct {
	Resource wireValidation `json:"resource"`
}

type wakeDueBody struct {
	Wakes []wireWake `json:"wakes"`
}

type wakeAdmitBody struct {
	Task    *wireTask `json:"task,omitempty"`
	Skipped bool      `json:"skipped"`
}

// Outgoing call input shapes (exact brief schemas).

type stageCallInput struct {
	Scope   contract.Scope `json:"scope"`
	Change  wireChange     `json:"change"`
	DraftID contract.ID    `json:"draft_id,omitempty"`
}

type snapshotCallInput struct {
	Scope contract.Scope `json:"scope"`
}

type tasksCreateCallInput struct {
	Task          wireTask    `json:"task"`
	SourceID      contract.ID `json:"source_id"`
	OccurrenceKey string      `json:"occurrence_key"`
}

type tasksTransitionCallInput struct {
	TaskID          contract.ID      `json:"task_id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	State           string           `json:"state"`
	EvidenceIDs     []contract.ID    `json:"evidence_ids"`
}

// Accounting interacts with generated tasks only through tasks' own
// admission reservation, so scheduling makes no direct accounting calls in
// v1: the per-scope envelopes are accounting's ledger, and the
// per-responsibility aggregate fence is scheduling-local arithmetic.

type executionEnqueueCallInput struct {
	Task wireTask `json:"task"`
}

// Peer response shapes. Decoded tolerantly; peers validate their own output
// against the shared schemas before sending.

type draftResource struct {
	Resource wireDraft `json:"resource"`
}

// peerTaskBody is the {resource: Task} envelope tasks returns from its
// internal create and transition operations.
type peerTaskBody struct {
	Resource wireTask `json:"resource"`
}

// peerWorker is the slice of the shared Worker definition scheduling
// consumes when resolving a worker dependency.
type peerWorker struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
}

// peerBinding is the slice of the shared Binding definition scheduling
// consumes when a worker resolves through a scope binding rather than the
// snapshot's worker field.
type peerBinding struct {
	ID       contract.ID      `json:"id"`
	Version  contract.Version `json:"version"`
	Scope    contract.Scope   `json:"scope"`
	Kind     string           `json:"kind"`
	TargetID contract.ID      `json:"target_id"`
}

type snapshotResource struct {
	Scope    contract.Scope `json:"scope"`
	Revision int64          `json:"revision"`
	Worker   *peerWorker    `json:"worker,omitempty"`
	Bindings []peerBinding  `json:"bindings"`
}

type snapshotBody struct {
	Resource snapshotResource `json:"resource"`
}
