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
