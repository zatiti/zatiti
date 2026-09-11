package accounting

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs. JSON is snake_case and unknown fields are rejected at the
// decode boundary before these types are filled. Output schemas describe
// Payload.Data, not the result envelope. Mirrors of configuration-owned
// objects (snapshot) carry every field configuration emits so strict
// decoding of the trusted peer payload cannot silently drop data.

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

// dimensions reports whether the scope names any dimension beyond the
// installation.
func (s wireScope) dimensions() bool {
	return s.OrganizationID != "" || s.ProjectID != "" || s.WorkerID != "" || s.TaskID != ""
}

// wireMoney mirrors $defs/Money: int64 micro-units of one explicit currency.
type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

// wireLimits mirrors $defs/Limits. All eight fields are required on the wire;
// the zero time is the unset root deadline and currency XXX is unconfigured.
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

// wireUsage mirrors $defs/Usage. Advisory marks usage whose amounts are
// estimates that cannot back an enforceable hard cap; unknown carries
// observed physical effects whose cost is not yet established.
type wireUsage struct {
	Currency  string `json:"currency"`
	Spent     int64  `json:"spent"`
	Reserved  int64  `json:"reserved"`
	Estimated int64  `json:"estimated"`
	Unknown   int64  `json:"unknown"`
	Advisory  bool   `json:"advisory"`
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

// wireValidation mirrors $defs/Validation.
type wireValidation struct {
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
	Requirements []wireRequirement `json:"requirements"`
	Dependencies []wireRef         `json:"dependencies"`
}

// wireChange mirrors the $defs/Change oneOf shape shared with configuration:
// kind discriminator, explicit action, object identity, create-only
// expected_version zero and a complete typed definition.
type wireChange struct {
	Kind            string          `json:"kind"`
	Action          string          `json:"action"`
	ID              contract.ID     `json:"id"`
	ExpectedVersion int64           `json:"expected_version"`
	Definition      json.RawMessage `json:"definition"`
}

// Change kinds and actions accounting owns or acts on.
const (
	changeKindBudget = "budget"
)

const (
	changeActionCreate  = "create"
	changeActionUpdate  = "update"
	changeActionArchive = "archive"
)

// scopeInput is the shared {scope} input body of the query operations.
type scopeInput struct {
	Scope wireScope `json:"scope"`
}

// limitsOutput is the budget.get output body.
type limitsOutput struct {
	Limits wireLimits `json:"limits"`
}

// budgetProposeInput is the budget.propose input body.
type budgetProposeInput struct {
	Scope           wireScope       `json:"scope"`
	ExpectedVersion int64           `json:"expected_version"`
	Limits          json.RawMessage `json:"limits"`
	DraftID         *contract.ID    `json:"draft_id,omitempty"`
}

// stageInput is the _configuration.stage input body.
type stageInput struct {
	Scope   wireScope       `json:"scope"`
	Change  json.RawMessage `json:"change"`
	DraftID *contract.ID    `json:"draft_id,omitempty"`
}

// reserveInput is the _accounting.reserve input body.
type reserveInput struct {
	Scope       wireScope    `json:"scope"`
	RootTaskID  *contract.ID `json:"root_task_id,omitempty"`
	OperationID contract.ID  `json:"operation_id"`
	Amount      wireMoney    `json:"amount"`
	Limits      wireLimits   `json:"limits"`
}

// reservationResourceBody is the reserve and settle output body.
type reservationResourceBody struct {
	Resource wireReservation `json:"resource"`
}

// validationResourceBody is the validate output body.
type validationResourceBody struct {
	Resource wireValidation `json:"resource"`
}

// usageResourceBody is the usage.get output body.
type usageResourceBody struct {
	Resource wireUsage `json:"resource"`
}

// settleInput is the _accounting.settle input body.
type settleInput struct {
	ReservationID   contract.ID `json:"reservation_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Usage           wireUsage   `json:"usage"`
	Nonexecution    bool        `json:"authoritative_nonexecution"`
}

// inspectOutput is the _accounting.inspect output body.
type inspectOutput struct {
	Limits wireLimits `json:"limits"`
	Usage  wireUsage  `json:"usage"`
}

// wireCandidate is the internal wire form of $defs/Candidate shared by
// _accounting.validate and _accounting.activate.
type wireCandidate struct {
	PlanID          contract.ID  `json:"plan_id"`
	BaseRevision    int64        `json:"base_revision"`
	CandidateDigest string       `json:"candidate_digest"`
	Changes         []wireChange `json:"changes"`
	Dependencies    []wireRef    `json:"dependencies"`
}

type candidateEnvelope struct {
	Candidate wireCandidate `json:"candidate"`
}

// versionsOutput is the _accounting.activate output body.
type versionsOutput struct {
	Versions []wireRef `json:"versions"`
}

// wireDraft mirrors $defs/Draft as staged by configuration. Changes stay
// inert JSON; each entry was schema-validated when staged.
type wireDraft struct {
	ID           contract.ID       `json:"id"`
	Version      int64             `json:"version"`
	BaseRevision int64             `json:"base_revision"`
	Changes      []json.RawMessage `json:"changes"`
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
}

type draftResourceBody struct {
	Resource wireDraft `json:"resource"`
}

// Snapshot mirrors. These decode the trusted _configuration.snapshot payload
// strictly, so every field configuration emits must be declared here even
// when accounting ignores it.

type snapshotScopeBody struct {
	Resource snapshotScope `json:"resource"`
}

type snapshotScope struct {
	Scope     wireScope        `json:"scope"`
	Revision  int64            `json:"revision"`
	Ancestors []snapshotOrg    `json:"ancestors"`
	Bindings  json.RawMessage  `json:"bindings"`
	Worker    *snapshotWorker  `json:"worker,omitempty"`
	Project   *snapshotProject `json:"project,omitempty"`
}

type snapshotOrg struct {
	ID         contract.ID     `json:"id"`
	Version    int64           `json:"version"`
	Key        string          `json:"key"`
	Name       string          `json:"name"`
	ChiefID    contract.ID     `json:"chief_id"`
	ParentID   *contract.ID    `json:"parent_id,omitempty"`
	Limits     *wireLimits     `json:"limits,omitempty"`
	Extensions json.RawMessage `json:"extensions,omitempty"`
}

type snapshotProject struct {
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

type snapshotWorker struct {
	ID             contract.ID     `json:"id"`
	Version        int64           `json:"version"`
	OrganizationID contract.ID     `json:"organization_id"`
	Key            string          `json:"key"`
	Name           string          `json:"name"`
	Purpose        string          `json:"purpose"`
	Instructions   string          `json:"instructions"`
	SkillVersions  []wireRef       `json:"skill_versions"`
	Bindings       []contract.ID   `json:"bindings"`
	Profile        json.RawMessage `json:"profile"`
	Limits         *wireLimits     `json:"limits"`
	Extensions     json.RawMessage `json:"extensions,omitempty"`
}
