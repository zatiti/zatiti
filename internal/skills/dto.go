package skills

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs for the skills operations. JSON is snake_case; optional scalars
// are pointers; unknown fields are rejected at the decode boundary. The
// shapes mirror the operation schemas embedded in the implementation
// assignment; schema validation runs before typed decoding.

// wireScope carries the explicit call scope.
type wireScope struct {
	InstallationID contract.ID `json:"installation_id"`
	OrganizationID contract.ID `json:"organization_id,omitempty"`
	ProjectID      contract.ID `json:"project_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
}

func (s wireScope) toContract() contract.Scope {
	return contract.Scope{
		InstallationID: s.InstallationID,
		OrganizationID: s.OrganizationID,
		ProjectID:      s.ProjectID,
		WorkerID:       s.WorkerID,
		TaskID:         s.TaskID,
	}
}

// wireRef pins one exact resource version.
type wireRef struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
}

// wireDiagnostic is one structured validation note.
type wireDiagnostic struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// wireRequirement declares one unmet prerequisite or pinned expectation.
type wireRequirement struct {
	Code        string      `json:"code"`
	Message     string      `json:"message"`
	ResourceID  contract.ID `json:"resource_id,omitempty"`
	ChallengeID contract.ID `json:"challenge_id,omitempty"`
}

// wireArtifactRef addresses one artifact by identity and content digest.
type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// wireJob is the shared job resource body.
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

// wireSkill is one immutable skill version. Requirements come from the
// imported allowed-tools frontmatter; they declare pinned tool needs and
// never install permission.
type wireSkill struct {
	ID                  contract.ID      `json:"id"`
	Version             contract.Version `json:"version"`
	Name                string           `json:"name"`
	InstructionArtifact wireArtifactRef  `json:"instruction_artifact"`
	ContentDigest       contract.Digest  `json:"content_digest"`
	InputSchema         json.RawMessage  `json:"input_schema"`
	OutputSchema        json.RawMessage  `json:"output_schema"`
	Requirements        []string         `json:"requirements"`
	Dependencies        []wireRef        `json:"dependencies"`
	Source              string           `json:"source"`
	License             string           `json:"license"`
	EvaluationRefs      []contract.ID    `json:"evaluation_refs"`
	Diagnostics         []wireDiagnostic `json:"diagnostics"`
}

// wireDraft is the configuration draft returned by staging operations.
type wireDraft struct {
	ID           contract.ID       `json:"id"`
	Version      contract.Version  `json:"version"`
	BaseRevision int64             `json:"base_revision"`
	Changes      []json.RawMessage `json:"changes"`
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
}

// wireValidation is the _skills.validate result body.
type wireValidation struct {
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
	Requirements []wireRequirement `json:"requirements"`
	Dependencies []wireRef         `json:"dependencies"`
}

// wireExpectedObservation is one self-authored expected check (Adapter_
// ExpectedVerificationObservation). It is sealed data, never authority.
type wireExpectedObservation struct {
	CheckID          string          `json:"check_id"`
	Kind             string          `json:"kind"`
	Expected         string          `json:"expected"`
	ArtifactName     string          `json:"artifact_name,omitempty"`
	ExpectedDigest   string          `json:"expected_digest,omitempty"`
	Schema           json.RawMessage `json:"schema,omitempty"`
	CommandID        string          `json:"command_id,omitempty"`
	ExpectedExitCode *int64          `json:"expected_exit_code,omitempty"`
}

// wireAcceptance pins the verifier identity/version, sealed fixture inputs
// and expected observations for an evaluation.
type wireAcceptance struct {
	VerifierID           string                    `json:"verifier_id"`
	VerifierVersion      string                    `json:"verifier_version"`
	SealedInputs         []wireArtifactRef         `json:"sealed_inputs"`
	ExpectedObservations []wireExpectedObservation `json:"expected_observations"`
	Mode                 string                    `json:"mode"`
	RequiredChildIDs     []contract.ID             `json:"required_child_ids"`
	Profile              json.RawMessage           `json:"profile"`
}

// wireMoney is an int64 micro-unit amount.
type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

// wireExecutionProfile pins the execution posture of an evaluation.
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

// wireLimits bounds an evaluation.
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

// wireCandidate is the compiler candidate slice.
type wireCandidate struct {
	PlanID          contract.ID     `json:"plan_id"`
	BaseRevision    int64           `json:"base_revision"`
	CandidateDigest contract.Digest `json:"candidate_digest"`
	Changes         []wireChange    `json:"changes"`
	Dependencies    []wireRef       `json:"dependencies"`
}

// wireChange is one typed candidate change; Definition stays inert JSON so
// only the owned skill slice is decoded here.
type wireChange struct {
	Kind            string           `json:"kind"`
	Action          string           `json:"action"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	Definition      json.RawMessage  `json:"definition"`
}

// ---------- operation input bodies ----------

type skillGetInput struct {
	Scope wireScope   `json:"scope"`
	ID    contract.ID `json:"id"`
}

type skillListFilter struct {
	State          *string      `json:"state,omitempty"`
	Key            *string      `json:"key,omitempty"`
	ParentID       *contract.ID `json:"parent_id,omitempty"`
	WorkerID       *contract.ID `json:"worker_id,omitempty"`
	TaskID         *contract.ID `json:"task_id,omitempty"`
	OrganizationID *contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool        `json:"descendants,omitempty"`
	NeedsYou       *bool        `json:"needs_you,omitempty"`
}

type skillListInput struct {
	Scope  wireScope        `json:"scope"`
	Cursor *string          `json:"cursor,omitempty"`
	Limit  *int64           `json:"limit,omitempty"`
	Filter *skillListFilter `json:"filter,omitempty"`
}

type skillArchiveInput struct {
	Scope           wireScope        `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	DraftID         *contract.ID     `json:"draft_id,omitempty"`
}

type evaluateInput struct {
	Scope      wireScope            `json:"scope"`
	Skill      wireRef              `json:"skill"`
	Acceptance wireAcceptance       `json:"acceptance"`
	Profile    wireExecutionProfile `json:"profile"`
	Limits     wireLimits           `json:"limits"`
}

type importInput struct {
	Scope    wireScope       `json:"scope"`
	Artifact wireArtifactRef `json:"artifact"`
	Source   string          `json:"source"`
	License  string          `json:"license"`
	DraftID  *contract.ID    `json:"draft_id,omitempty"`
}

type candidateInput struct {
	Candidate wireCandidate `json:"candidate"`
}

// ---------- operation output bodies ----------

type skillOutput struct {
	Resource wireSkill `json:"resource"`
}

type archiveOutput struct {
	Draft    wireDraft `json:"draft"`
	Resource wireSkill `json:"resource"`
}

type jobOutput struct {
	Resource wireJob `json:"resource"`
}

type versionsOutput struct {
	Versions []wireRef `json:"versions"`
}

type validationOutput struct {
	Resource wireValidation `json:"resource"`
}

// importOutput is the skill.import oneOf body: the completed resource in the
// synchronous path, or a job when the controller recovers the import.
type importOutput struct {
	Resource *wireSkill `json:"resource,omitempty"`
	Job      *wireJob   `json:"job,omitempty"`
}
