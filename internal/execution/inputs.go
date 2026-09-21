package execution

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Typed input and output bodies, mirroring the frozen operation schemas in
// internal/execution/AGENTS.md exactly. Inputs decode strictly (unknown
// fields fail through the additionalProperties:false schemas); outputs
// marshal to canonical JSON and revalidate against the merged output
// schemas in bind.

// Internal operation inputs.

type contextInput struct {
	AttemptID contract.ID `json:"attempt_id"`
	Context   wireContext `json:"context"`
}

type enqueueInput struct {
	Task wireTask `json:"task"`
}

type fenceInput struct {
	Generation int64  `json:"generation"`
	Reason     string `json:"reason"`
}

type jobClaimInput struct {
	JobID           contract.ID      `json:"job_id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	Generation      int64            `json:"generation"`
}

type jobCreateInput struct {
	Scope       contract.Scope  `json:"scope"`
	Owner       string          `json:"owner"`
	Operation   string          `json:"operation"`
	Input       json.RawMessage `json:"input"`
	SourceID    contract.ID     `json:"source_id"`
	OperationID contract.ID     `json:"operation_id,omitempty"`
}

type jobPendingInput struct {
	Limit int64 `json:"limit"`
}

type jobRecordInput struct {
	JobID           contract.ID      `json:"job_id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	Generation      int64            `json:"generation"`
	State           string           `json:"state"`
	Result          json.RawMessage  `json:"result"`
	EvidenceIDs     []contract.ID    `json:"evidence_ids"`
}

type observationInput struct {
	AttemptID   contract.ID     `json:"attempt_id"`
	OperationID contract.ID     `json:"operation_id"`
	Observation wireObservation `json:"observation"`
}

type tickInput struct {
	Now   string `json:"now"`
	Limit int64  `json:"limit"`
}

type verificationRecordInput struct {
	AttemptID       contract.ID            `json:"attempt_id"`
	ExpectedVersion contract.Version       `json:"expected_version"`
	Result          wireVerificationResult `json:"result"`
}

// Public operation inputs.

type getIDInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

type listFilter struct {
	State        string      `json:"state,omitempty"`
	Key          string      `json:"key,omitempty"`
	ParentID     contract.ID `json:"parent_id,omitempty"`
	WorkerID     contract.ID `json:"worker_id,omitempty"`
	TaskID       contract.ID `json:"task_id,omitempty"`
	Organization contract.ID `json:"organization_id,omitempty"`
	Descendants  *bool       `json:"descendants,omitempty"`
	NeedsYou     *bool       `json:"needs_you,omitempty"`
}

type listInput struct {
	Scope  contract.Scope `json:"scope"`
	Cursor string         `json:"cursor,omitempty"`
	Limit  *int64         `json:"limit,omitempty"`
	Filter *listFilter    `json:"filter,omitempty"`
}

type cancelInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	Reason          string           `json:"reason"`
}

type checkpointInput struct {
	Scope           contract.Scope    `json:"scope"`
	AttemptID       contract.ID       `json:"attempt_id"`
	LeaseID         contract.ID       `json:"lease_id"`
	Generation      int64             `json:"generation"`
	ExpectedVersion contract.Version  `json:"expected_version"`
	Context         wireArtifactRef   `json:"context"`
	Outputs         []wireArtifactRef `json:"outputs"`
}

type heartbeatInput struct {
	Scope           contract.Scope   `json:"scope"`
	AttemptID       contract.ID      `json:"attempt_id"`
	LeaseID         contract.ID      `json:"lease_id"`
	Generation      int64            `json:"generation"`
	ExpectedVersion contract.Version `json:"expected_version"`
}

type reportInput struct {
	Scope           contract.Scope    `json:"scope"`
	AttemptID       contract.ID       `json:"attempt_id"`
	LeaseID         contract.ID       `json:"lease_id"`
	Generation      int64             `json:"generation"`
	ExpectedVersion contract.Version  `json:"expected_version"`
	Outputs         []wireArtifactRef `json:"outputs"`
	Observations    json.RawMessage   `json:"observations"`
	Usage           wireUsage         `json:"usage"`
}

type runClaimInput struct {
	Scope           contract.Scope   `json:"scope"`
	RunID           contract.ID      `json:"run_id"`
	WorkerID        contract.ID      `json:"worker_id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	Capabilities    []string         `json:"capabilities"`
}

type workerGateInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
}

// Revision 3 internal inputs: the durable worker turn pipeline.

type turnAdmitInput struct {
	Source      wireTurnSource `json:"source"`
	WorkerID    contract.ID    `json:"worker_id"`
	Scope       contract.Scope `json:"scope"`
	RequesterID contract.ID    `json:"requester_id"`
}

type workPendingInput struct {
	Limit int64 `json:"limit"`
}

type workClaimInput struct {
	WorkID          contract.ID      `json:"work_id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	Generation      int64            `json:"generation"`
}

type contextPrepareInput struct {
	TurnID          contract.ID      `json:"turn_id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	Generation      int64            `json:"generation"`
}

type contextCommitInput struct {
	PlanID          contract.ID         `json:"plan_id"`
	ExpectedVersion contract.Version    `json:"expected_version"`
	Generation      int64               `json:"generation"`
	StagedContext   wireArtifactLocator `json:"staged_context"`
}

type proposalPrepareInput struct {
	TurnID          contract.ID      `json:"turn_id"`
	StepIndex       int64            `json:"step_index"`
	ProposalID      string           `json:"proposal_id"`
	ExpectedVersion contract.Version `json:"expected_version"`
}

type proposalRecordInput struct {
	ProposalID        string           `json:"proposal_id"`
	ExpectedVersion   contract.Version `json:"expected_version"`
	CommandID         contract.ID      `json:"command_id,omitempty"`
	EffectOperationID contract.ID      `json:"effect_operation_id,omitempty"`
	ResultArtifact    *wireArtifactRef `json:"result_artifact,omitempty"`
}

type verificationPendingInput struct {
	Limit int64 `json:"limit"`
}

type verificationClaimInput struct {
	RequestID       contract.ID      `json:"request_id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	Generation      int64            `json:"generation"`
}

// Revision 3 output bodies.

type turnBody struct {
	Resource wireWorkerTurn `json:"resource"`
}

type workPendingBody struct {
	Items []wireWorkItem `json:"items"`
}

type workClaimBody struct {
	Item       wireWorkItem     `json:"item"`
	ClaimToken string           `json:"claim_token"`
	Version    contract.Version `json:"version"`
}

type contextPlanBody struct {
	Resource wireContextPlan `json:"resource"`
}

type proposalBody struct {
	Resource wireProposalRecord `json:"resource"`
}

type verificationPendingBody struct {
	Items []wireVerificationRequest `json:"items"`
}

type verificationClaimBody struct {
	Request    wireVerificationRequest `json:"request"`
	ClaimToken string                  `json:"claim_token"`
}

// Output bodies. Every output revalidates against its merged output schema,
// so the shapes below carry exactly the declared properties.

type runBody struct {
	Resource wireRun `json:"resource"`
}

type attemptBody struct {
	Resource wireAttempt `json:"resource"`
}

type jobBody struct {
	Resource wireJob `json:"resource"`
}

type dispositionBody struct {
	Resource wireDisposition `json:"resource"`
}

type fenceBody struct {
	AttemptIDs []contract.ID `json:"attempt_ids"`
}

type claimBody struct {
	Attempt wireAttempt     `json:"attempt"`
	Task    wireTask        `json:"task"`
	Context wireArtifactRef `json:"context"`
}

type recoveryBody struct {
	Resource    any               `json:"resource"`
	Obligations []wireRequirement `json:"obligations"`
}

type jobClaimBody struct {
	Job   wireJob         `json:"job"`
	Input json.RawMessage `json:"input"`
}

type attemptListBody struct {
	Items []wireAttempt `json:"items"`
}

type runListBody struct {
	Items []wireRun `json:"items"`
}

type jobListBody struct {
	Items []wireJob `json:"items"`
}

// Verification wire shapes, frozen from the local adapter payload section.

// wireObservedCheck mirrors Adapter_ObservedVerificationCheck. Evidence
// locators are the bounded oneOf artifacts|staged documents, carried opaque.
type wireObservedCheck struct {
	CheckID          string            `json:"check_id"`
	Kind             string            `json:"kind"`
	Status           string            `json:"status"`
	Evidence         []json.RawMessage `json:"evidence"`
	Explanation      string            `json:"explanation"`
	ObservedDigest   contract.Digest   `json:"observed_digest,omitempty"`
	ObservedExitCode *int64            `json:"observed_exit_code,omitempty"`
}

// wireVerificationResult mirrors Adapter_VerificationResult, the result the
// trusted verifier submits through the controller admission identity.
type wireVerificationResult struct {
	Schema             string              `json:"schema"`
	JobID              contract.ID         `json:"job_id"`
	TaskID             contract.ID         `json:"task_id"`
	AttemptID          contract.ID         `json:"attempt_id"`
	AcceptanceDigest   contract.Digest     `json:"acceptance_digest"`
	VerifierID         string              `json:"verifier_id"`
	VerifierVersion    string              `json:"verifier_version"`
	VerifierCodeDigest contract.Digest     `json:"verifier_code_digest"`
	RequestArtifact    wireArtifactRef     `json:"request_artifact"`
	Status             string              `json:"status"`
	Observations       []wireObservedCheck `json:"observations"`
	StartedAt          string              `json:"started_at"`
	FinishedAt         string              `json:"finished_at"`
	StagedOutputs      []json.RawMessage   `json:"staged_outputs"`
	OutputArtifacts    []json.RawMessage   `json:"output_artifacts"`
	Independent        bool                `json:"independent"`
}

// wireVerifierOutputRequirement mirrors VerifierOutputRequirement.
type wireVerifierOutputRequirement struct {
	Name       string          `json:"name"`
	Artifact   wireArtifactRef `json:"artifact"`
	MediaType  string          `json:"media_type"`
	JSONSchema json.RawMessage `json:"json_schema,omitempty"`
}

// wireVerificationRequest mirrors VerificationRequest, the document the
// report path pins into the verification job and the verifier consumes.
type wireVerificationRequest struct {
	Schema               string                          `json:"schema"`
	JobID                contract.ID                     `json:"job_id"`
	TaskID               contract.ID                     `json:"task_id"`
	AttemptID            contract.ID                     `json:"attempt_id"`
	Scope                contract.Scope                  `json:"scope"`
	AcceptanceDigest     contract.Digest                 `json:"acceptance_digest"`
	Profile              json.RawMessage                 `json:"profile"`
	SealedInputs         []wireArtifactRef               `json:"sealed_inputs"`
	Outputs              []wireVerifierOutputRequirement `json:"outputs"`
	ExpectedObservations []wireExpectedObservation       `json:"expected_observations"`
	Deadline             string                          `json:"deadline"`
	Repository           *string                         `json:"repository,omitempty"`
	BaseSHA              *string                         `json:"base_sha,omitempty"`
	Patch                *wireArtifactRef                `json:"patch,omitempty"`
}

// wireProfileCore reads the shared identity fields of either verifier
// profile flavor (artifact_contract or repository_patch).
type wireProfileCore struct {
	Schema     string          `json:"schema"`
	Kind       string          `json:"kind"`
	ID         string          `json:"id"`
	Version    string          `json:"version"`
	CodeDigest contract.Digest `json:"code_digest"`
	MaxBytes   int64           `json:"max_bytes"`
	Timeout    int64           `json:"timeout_seconds"`
}

// wireRepositoryProfile mirrors RepositoryVerifierProfile (schema kind
// "repository_patch"): the repository_patch_applies/repository_command
// flavor's fields beyond the shared identity core wireProfileCore already
// reads. command_id/runner_profile/environment_profile are opaque
// identifiers that resolve only against the prequalified local runner
// registry (repository_runner.go); nothing here is executable argv/env by
// itself, matching "no request supplies executable paths or argv"
// (internal/execution/AGENTS.md).
type wireRepositoryProfile struct {
	Schema             string          `json:"schema"`
	Kind               string          `json:"kind"`
	ID                 string          `json:"id"`
	Version            string          `json:"version"`
	CodeDigest         contract.Digest `json:"code_digest"`
	RunnerProfile      string          `json:"runner_profile"`
	CommandID          string          `json:"command_id"`
	CommandDigest      contract.Digest `json:"command_digest"`
	EnvironmentProfile string          `json:"environment_profile"`
	Network            string          `json:"network"`
	MaxOutputBytes     int64           `json:"max_output_bytes"`
	TimeoutSeconds     int64           `json:"timeout_seconds"`
}
