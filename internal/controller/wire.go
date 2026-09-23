package controller

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire shapes of the internal operations the controller calls. They mirror
// the frozen operation schemas; the controller depends on no owner's Go
// types. Inputs carry exactly the declared fields because owners reject
// unknown ones. Outputs decode only what the controller acts on.

// Operation states the controller branches on.
const (
	opStatePrepared             = "prepared"
	opStateAwaitingReview       = "awaiting_review"
	opStateReady                = "ready"
	opStateAwaitingConfirmation = "awaiting_confirmation"
	opStateOutcomeUnknown       = "outcome_unknown"
)

// Job states.
const (
	jobStatePending        = "pending"
	jobStateSucceeded      = "succeeded"
	jobStateFailed         = "failed"
	jobStateOutcomeUnknown = "outcome_unknown"
	jobStateCancelled      = "cancelled"
)

type wireRef struct {
	ID      contract.ID `json:"id"`
	Version int64       `json:"version"`
}

type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

// wireAction is the subset of $defs/Action the controller reads from a
// claimed dispatch: where published outputs belong, which connection a probe
// validated, what the charge bound was and the owner's inert parameters.
type wireAction struct {
	Scope      contract.Scope  `json:"scope"`
	Connection wireRef         `json:"connection"`
	Parameters json.RawMessage `json:"parameters"`
	CostBound  wireMoney       `json:"cost_bound"`
}

type wireOperation struct {
	ID            contract.ID     `json:"id"`
	Version       int64           `json:"version"`
	Action        json.RawMessage `json:"action"`
	State         string          `json:"state"`
	AttemptIDs    []contract.ID   `json:"attempt_ids"`
	CallbackRoute json.RawMessage `json:"callback_route,omitempty"`
}

type wireUsage struct {
	Currency  string `json:"currency"`
	Spent     int64  `json:"spent"`
	Reserved  int64  `json:"reserved"`
	Estimated int64  `json:"estimated"`
	Unknown   int64  `json:"unknown"`
	Advisory  bool   `json:"advisory"`
}

type wireJob struct {
	ID          contract.ID `json:"id"`
	Version     int64       `json:"version"`
	Kind        string      `json:"kind"`
	State       string      `json:"state"`
	Owner       string      `json:"owner"`
	Operation   string      `json:"operation"`
	OperationID contract.ID `json:"operation_id"`
}

type wireArtifact struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// Requirement mirrors $defs/Requirement: a named, inspectable reason a job
// is not complete.
type Requirement struct {
	Code        string      `json:"code"`
	Message     string      `json:"message"`
	ResourceID  contract.ID `json:"resource_id,omitempty"`
	ChallengeID contract.ID `json:"challenge_id,omitempty"`
}

// Inputs.

type limitInput struct {
	Limit int64 `json:"limit"`
}

type nowLimitInput struct {
	Now   time.Time `json:"now"`
	Limit int64     `json:"limit"`
}

type wakeAdmitInput struct {
	Wake json.RawMessage `json:"wake"`
}

type fenceInput struct {
	Generation int64  `json:"generation"`
	Reason     string `json:"reason"`
}

type effectsAdmitInput struct {
	OperationID     contract.ID `json:"operation_id"`
	ExpectedVersion int64       `json:"expected_version"`
}

type effectsClaimInput struct {
	OperationID contract.ID `json:"operation_id"`
	AttemptID   contract.ID `json:"attempt_id"`
	Generation  int64       `json:"generation"`
}

type effectsRecordInput struct {
	OperationID contract.ID          `json:"operation_id"`
	AttemptID   contract.ID          `json:"attempt_id"`
	Generation  int64                `json:"generation"`
	Observation contract.Observation `json:"observation"`
}

type executionObservationInput struct {
	AttemptID   contract.ID          `json:"attempt_id"`
	OperationID contract.ID          `json:"operation_id"`
	Observation contract.Observation `json:"observation"`
}

type memoryRecordInput struct {
	JobID       contract.ID          `json:"job_id"`
	OperationID contract.ID          `json:"operation_id"`
	Observation contract.Observation `json:"observation"`
}

type validationRecordInput struct {
	ConnectionID    contract.ID          `json:"connection_id"`
	ExpectedVersion int64                `json:"expected_version"`
	Observation     contract.Observation `json:"observation"`
}

type artifactsPublishInput struct {
	Scope          contract.Scope  `json:"scope"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Encrypted      bool            `json:"encrypted"`
}

type jobClaimInput struct {
	JobID           contract.ID `json:"job_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Generation      int64       `json:"generation"`
}

type jobRecordInput struct {
	JobID           contract.ID     `json:"job_id"`
	ExpectedVersion int64           `json:"expected_version"`
	Generation      int64           `json:"generation"`
	State           string          `json:"state"`
	Result          json.RawMessage `json:"result"`
	EvidenceIDs     []contract.ID   `json:"evidence_ids"`
}

type restoreRecordInput struct {
	JobID        contract.ID   `json:"job_id"`
	State        string        `json:"state"`
	Requirements []Requirement `json:"requirements"`
}

// restoreOverlayInput is _installation.restore.overlay's input: the restore
// job whose published recovery overlay the controller must merge after the
// swap.
type restoreOverlayInput struct {
	JobID contract.ID `json:"job_id"`
}

// Outputs.

type wakesOutput struct {
	Wakes []json.RawMessage `json:"wakes"`
}

type wakeAdmitOutput struct {
	Skipped bool `json:"skipped"`
}

type operationsOutput struct {
	Operations []wireOperation `json:"operations"`
}

type operationOutput struct {
	Resource wireOperation `json:"resource"`
}

type dispatchOutput struct {
	Resource contract.Dispatch `json:"resource"`
}

type attemptIDsOutput struct {
	AttemptIDs []contract.ID `json:"attempt_ids"`
}

type jobsOutput struct {
	Items []wireJob `json:"items"`
}

type itemsOutput struct {
	Items []json.RawMessage `json:"items"`
}

type jobClaimOutput struct {
	Job   wireJob         `json:"job"`
	Input json.RawMessage `json:"input"`
}

type jobOutput struct {
	Resource wireJob `json:"resource"`
}

type artifactOutput struct {
	Resource wireArtifact `json:"resource"`
}

// restoreOverlayOutput is _installation.restore.overlay's output: the
// published, sealed overlay artifact and the exact size needed to read it
// back in full. The controller never opens those bytes itself; it hands the
// reference to the entrypoint-supplied RestoreLifecycle.
type restoreOverlayOutput struct {
	Artifact wireArtifact `json:"artifact"`
	Size     int64        `json:"size"`
}
