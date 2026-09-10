package configuration

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs. Names concatenate operation path segments per the shared
// contract; JSON is snake_case and unknown fields are rejected at the decode
// boundary before these types are filled. Output schemas describe
// Payload.Data, not the result envelope.

// wireScope mirrors $defs/Scope.
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

func scopeFromContract(s contract.Scope) wireScope {
	return wireScope{
		InstallationID: s.InstallationID,
		OrganizationID: s.OrganizationID,
		ProjectID:      s.ProjectID,
		WorkerID:       s.WorkerID,
		TaskID:         s.TaskID,
	}
}

// wireRef mirrors $defs/Ref.
type wireRef struct {
	ID      contract.ID `json:"id"`
	Version int64       `json:"version"`
}

// wireDiagnostic mirrors $defs/Diagnostic.
type wireDiagnostic struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// wireRequirement mirrors $defs/Requirement.
type wireRequirement struct {
	Code        string      `json:"code"`
	Message     string      `json:"message"`
	ResourceID  contract.ID `json:"resource_id,omitempty"`
	ChallengeID contract.ID `json:"challenge_id,omitempty"`
}

// wireDecisionRequirement mirrors $defs/DecisionRequirement.
type wireDecisionRequirement struct {
	ActionDigest       string        `json:"action_digest"`
	HumanRequired      bool          `json:"human_required"`
	EligiblePrincipals []contract.ID `json:"eligible_principals"`
	ExpiresAt          time.Time     `json:"expires_at"`
	SeparateProposer   bool          `json:"separate_proposer"`
}

// wireMoney mirrors $defs/Money.
type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

// wireLimits mirrors $defs/Limits. All eight fields are required on the wire.
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

// wireExecutionProfile mirrors $defs/ExecutionProfile. Worker definitions
// carry it as nullable; a nil pointer serializes as null and a bootstrap
// chief's null profile never becomes executable authority.
type wireExecutionProfile struct {
	ID                  contract.ID `json:"id"`
	Version             int64       `json:"version"`
	Executor            string      `json:"executor"`
	Model               string      `json:"model"`
	ConnectionID        contract.ID `json:"connection_id"`
	ProviderDestination string      `json:"provider_destination"`
	Capabilities        []string    `json:"capabilities"`
	CostBound           wireMoney   `json:"cost_bound"`
	Classification      string      `json:"classification"`
	ContextCapture      string      `json:"context_capture"`
}

// wireOrganization mirrors $defs/Organization.
type wireOrganization struct {
	ID         contract.ID     `json:"id"`
	Version    int64           `json:"version"`
	Key        string          `json:"key"`
	Name       string          `json:"name"`
	ChiefID    contract.ID     `json:"chief_id"`
	ParentID   *contract.ID    `json:"parent_id,omitempty"`
	Limits     *wireLimits     `json:"limits,omitempty"`
	Extensions json.RawMessage `json:"extensions,omitempty"`
}

// wireTeam mirrors $defs/Team.
type wireTeam struct {
	ID             contract.ID     `json:"id"`
	Version        int64           `json:"version"`
	OrganizationID contract.ID     `json:"organization_id"`
	Key            string          `json:"key"`
	Name           string          `json:"name"`
	WorkerIDs      []contract.ID   `json:"worker_ids"`
	Extensions     json.RawMessage `json:"extensions,omitempty"`
}

// wireProject mirrors $defs/Project.
type wireProject struct {
	ID             contract.ID     `json:"id"`
	Version        int64           `json:"version"`
	OrganizationID contract.ID     `json:"organization_id"`
	Key            string          `json:"key"`
	Name           string          `json:"name"`
	Repositories   []string        `json:"repositories"`
	Bindings       []contract.ID   `json:"bindings"`
	Classification string          `json:"classification"`
	Limits         *wireLimits     `json:"limits,omitempty"`
	Extensions     json.RawMessage `json:"extensions,omitempty"`
}

// wireWorker mirrors $defs/Worker.
type wireWorker struct {
	ID             contract.ID           `json:"id"`
	Version        int64                 `json:"version"`
	OrganizationID contract.ID           `json:"organization_id"`
	Key            string                `json:"key"`
	Name           string                `json:"name"`
	Purpose        string                `json:"purpose"`
	Instructions   string                `json:"instructions"`
	SkillVersions  []wireRef             `json:"skill_versions"`
	Bindings       []contract.ID         `json:"bindings"`
	Profile        *wireExecutionProfile `json:"profile"`
	Limits         *wireLimits           `json:"limits"`
	Extensions     json.RawMessage       `json:"extensions,omitempty"`
}

// wireBinding mirrors $defs/Binding.
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

// wireChange mirrors the $defs/Change oneOf: kind discriminator, explicit
// action, object identity, zero expected_version for creation and a complete
// typed definition.
type wireChange struct {
	Kind            string          `json:"kind"`
	Action          string          `json:"action"`
	ID              contract.ID     `json:"id"`
	ExpectedVersion int64           `json:"expected_version"`
	Definition      json.RawMessage `json:"definition"`
}

// wireDraft mirrors $defs/Draft. Changes stay inert JSON at this boundary;
// each entry was schema-validated when staged.
type wireDraft struct {
	ID           contract.ID       `json:"id"`
	Version      int64             `json:"version"`
	BaseRevision int64             `json:"base_revision"`
	Changes      []json.RawMessage `json:"changes"`
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
}

// wirePlan mirrors $defs/Plan.
type wirePlan struct {
	ID                    contract.ID               `json:"id"`
	Version               int64                     `json:"version"`
	DraftID               contract.ID               `json:"draft_id"`
	BaseRevision          int64                     `json:"base_revision"`
	CandidateDigest       string                    `json:"candidate_digest"`
	Changes               []json.RawMessage         `json:"changes"`
	Dependencies          []wireRef                 `json:"dependencies"`
	CompilerVersion       string                    `json:"compiler_version"`
	SchemaVersion         string                    `json:"schema_version"`
	AuthorityRequirements []wireRequirement         `json:"authority_requirements"`
	Decisions             []wireDecisionRequirement `json:"decisions"`
	Diagnostics           []wireDiagnostic          `json:"diagnostics"`
	Requirements          []wireRequirement         `json:"requirements"`
}

// wireRevision mirrors $defs/Revision.
type wireRevision struct {
	ID              contract.ID `json:"id"`
	Version         int64       `json:"version"`
	PlanID          contract.ID `json:"plan_id"`
	CandidateDigest string      `json:"candidate_digest"`
	ActivatedAt     time.Time   `json:"activated_at"`
}

// wireValidation mirrors $defs/Validation.
type wireValidation struct {
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
	Requirements []wireRequirement `json:"requirements"`
	Dependencies []wireRef         `json:"dependencies"`
}

// wireJob mirrors $defs/Job as returned by export/import and asynchronous
// operations. Result stays inert JSON.
type wireJob struct {
	ID             contract.ID       `json:"id"`
	Version        int64             `json:"version"`
	Kind           string            `json:"kind"`
	State          string            `json:"state"`
	Requirements   []wireRequirement `json:"requirements"`
	ResultArtifact *wireArtifactRef  `json:"result_artifact,omitempty"`
	OperationID    contract.ID       `json:"operation_id,omitempty"`
	Owner          string            `json:"owner"`
	Operation      string            `json:"operation"`
	Result         json.RawMessage   `json:"result,omitempty"`
}

// wireArtifactRef mirrors $defs/ArtifactRef.
type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// wireDisposition mirrors $defs/Disposition (draft.discard output).
type wireDisposition struct {
	ID      contract.ID `json:"id"`
	Version int64       `json:"version"`
	State   string      `json:"state"`
}

// wireScopeSnapshot mirrors $defs/ScopeSnapshot.
type wireScopeSnapshot struct {
	Scope     wireScope          `json:"scope"`
	Revision  int64              `json:"revision"`
	Ancestors []wireOrganization `json:"ancestors"`
	Bindings  []wireBinding      `json:"bindings"`
	Worker    *wireWorker        `json:"worker,omitempty"`
	Project   *wireProject       `json:"project,omitempty"`
}

// Resource states used by the configuration-owned tables. Admission (new
// work, new child configurations, new bindings) is refused for anything not
// active; archived keeps the definition exportable.
const (
	stateActive    = "active"
	stateArchiving = "archiving"
	stateArchived  = "archived"
)

// Compiler/schema identity sealed into every plan. schemaVersion follows the
// embedded catalog revision; compilerVersion the package revision.
const (
	compilerVersion = "configuration-compiler/v1"
	schemaVersion   = "zatiti.schemas/v1"
)

// change kinds and actions.
const (
	kindOrganization     = "organization"
	kindTeam             = "team"
	kindProject          = "project"
	kindWorker           = "worker"
	kindBinding          = "binding"
	kindExecutionProfile = "execution_profile"
	kindSkill            = "skill"
	kindConnection       = "connection"
	kindPolicy           = "policy"
	kindAutonomyRule     = "autonomy_rule"
	kindSchedule         = "schedule"
	kindResponsibility   = "responsibility"
	kindMemoryBinding    = "memory_binding"
	kindBudget           = "budget"

	actionCreate  = "create"
	actionUpdate  = "update"
	actionArchive = "archive"
	actionDelete  = "delete"
)

// ownedKinds are the concrete definition kinds configuration itself stores
// and activates. Every other kind routes to its owning domain through the
// internal validate/activate boundary.
var ownedKinds = map[string]bool{
	kindOrganization:     true,
	kindTeam:             true,
	kindProject:          true,
	kindWorker:           true,
	kindBinding:          true,
	kindExecutionProfile: true,
}

// ownerForKind maps a change kind to the owning domain that validates and
// activates it. Configuration-owned kinds never leave this package.
var ownerForKind = map[string]string{
	kindOrganization:     "configuration",
	kindTeam:             "configuration",
	kindProject:          "configuration",
	kindWorker:           "configuration",
	kindBinding:          "configuration",
	kindExecutionProfile: "configuration",
	kindSkill:            "skills",
	kindConnection:       "connections",
	kindPolicy:           "policy",
	kindAutonomyRule:     "policy",
	kindSchedule:         "scheduling",
	kindResponsibility:   "scheduling",
	kindMemoryBinding:    "memory",
	kindBudget:           "accounting",
}

// validateCallerOwners is the internal-caller owner order used by the
// compiler: identity lands worker/principal state first, then the domains
// whose effective slices the bundle touches, then accounting ceilings.
var compilerOwnerOrder = []string{"identity", "skills", "connections", "policy", "accounting", "scheduling", "memory"}
