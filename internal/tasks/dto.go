package tasks

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs mirror the frozen local schema definitions one to one. Fields
// use json tags exactly as the schemas name them; strict decoding rejects
// anything else. Optional UUID and string fields stay empty when absent.

// wireScope mirrors #/$defs/Scope.
type wireScope struct {
	InstallationID contract.ID `json:"installation_id"`
	OrganizationID contract.ID `json:"organization_id,omitempty"`
	ProjectID      contract.ID `json:"project_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
}

// scopeFromContract converts the unit scope to its wire shape.
func scopeFromContract(sc contract.Scope) wireScope {
	return wireScope{
		InstallationID: sc.InstallationID,
		OrganizationID: sc.OrganizationID,
		ProjectID:      sc.ProjectID,
		WorkerID:       sc.WorkerID,
		TaskID:         sc.TaskID,
	}
}

// toContract converts a wire scope to the contract shape.
func (w wireScope) toContract() contract.Scope {
	return contract.Scope{
		InstallationID: w.InstallationID,
		OrganizationID: w.OrganizationID,
		ProjectID:      w.ProjectID,
		WorkerID:       w.WorkerID,
		TaskID:         w.TaskID,
	}
}

// wireArtifactRef mirrors #/$defs/ArtifactRef.
type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// wireRef mirrors #/$defs/Ref.
type wireRef struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
}

// wireMoney mirrors #/$defs/Money.
type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

// wireExpectedObservation mirrors #/$defs/Adapter_ExpectedVerificationObservation.
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

// wireVerificationProfile is the decoded oneOf verification profile. Exactly
// one kind is present; the schema guarantees the const discriminator matches.
type wireVerificationProfile struct {
	Kind            string          `json:"kind"`
	ID              string          `json:"id"`
	Version         string          `json:"version"`
	CodeDigest      contract.Digest `json:"code_digest"`
	SupportedChecks []string        `json:"supported_checks,omitempty"`
	RunnerProfile   string          `json:"runner_profile,omitempty"`
	CommandID       string          `json:"command_id,omitempty"`
	CommandDigest   contract.Digest `json:"command_digest,omitempty"`
}

// profileDecoded extracts the discriminator and identity fields from either
// profile variant without re-encoding the whole document. Decoding is
// lenient: profile documents carry verifier-runner fields beyond the
// identity fields tasks consumes, and the peer owns the strictness of its
// own document.
func profileDecoded(raw json.RawMessage) (*wireVerificationProfile, error) {
	var p wireVerificationProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// wireAcceptance mirrors #/$defs/Acceptance.
type wireAcceptance struct {
	VerifierID           string                    `json:"verifier_id"`
	VerifierVersion      string                    `json:"verifier_version"`
	SealedInputs         []wireArtifactRef         `json:"sealed_inputs"`
	ExpectedObservations []wireExpectedObservation `json:"expected_observations"`
	Mode                 string                    `json:"mode"`
	RequiredChildIDs     []contract.ID             `json:"required_child_ids"`
	Profile              json.RawMessage           `json:"profile"`
}

const (
	acceptanceModeIndependent = "independent"
	acceptanceModeManual      = "manual"

	// obsArtifactPresence and friends name the five observation kinds.
	obsArtifactPresence       = "artifact_presence"
	obsArtifactDigest         = "artifact_digest"
	obsJSONSchema             = "json_schema"
	obsRepositoryPatchApplies = "repository_patch_applies"
	obsRepositoryCommand      = "repository_command"

	// profileArtifactContract and profileRepositoryPatch name the two
	// verifier profile kinds.
	profileArtifactContract = "artifact_contract"
	profileRepositoryPatch  = "repository_patch"

	observationExpectedPass = "pass"
	observationExpectedFail = "fail"

	// verificationMediaType is the media type of verifier-produced result
	// artifacts. Only the qualified verifier runner inside execution mints
	// them; tasks treats their presence as the structural marker that the
	// pinned verifier ran for this task.
	verificationMediaType = "application/zatiti.verification.v1+json"
)

// wireLimits mirrors #/$defs/Limits. Every field is required by the schema.
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

// wireTask mirrors #/$defs/Task, the pinned task contract.
type wireTask struct {
	ID                    contract.ID       `json:"id"`
	Version               contract.Version  `json:"version"`
	Scope                 wireScope         `json:"scope"`
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

// Task states.
const (
	stateDraft     = "draft"
	stateReady     = "ready"
	stateRunning   = "running"
	stateWaiting   = "waiting"
	stateVerifying = "verifying"
	stateSucceeded = "succeeded"
	stateFailed    = "failed"
	stateCancelled = "cancelled"

	establishedVerification = "verification"
	establishedManual       = "manual"
)
