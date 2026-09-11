package connections

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs. Names mirror the embedded $defs catalog; JSON is snake_case and
// unknown fields are rejected at the decode boundary before these types are
// filled. Output schemas describe Payload.Data, not the result envelope.

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

// wireMoney mirrors $defs/Money.
type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

// wireConnection mirrors $defs/Connection. ValidatedAt and ValidUntil are
// optional scalars and therefore pointers on the wire.
type wireConnection struct {
	ID              contract.ID `json:"id"`
	Version         int64       `json:"version"`
	Scope           wireScope   `json:"scope"`
	Provider        string      `json:"provider"`
	AccountIdentity string      `json:"account_identity"`
	CredentialRef   string      `json:"credential_ref"`
	Destinations    []string    `json:"destinations"`
	AllowedScopes   []string    `json:"allowed_scopes"`
	ValidationState string      `json:"validation_state"`
	ValidatedAt     *time.Time  `json:"validated_at,omitempty"`
	ValidUntil      *time.Time  `json:"valid_until,omitempty"`
}

// wireTool mirrors $defs/Tool. Schemas stay inert JSON.
type wireTool struct {
	ID                  contract.ID     `json:"id"`
	Version             int64           `json:"version"`
	Name                string          `json:"name"`
	InputSchema         json.RawMessage `json:"input_schema"`
	OutputSchema        json.RawMessage `json:"output_schema"`
	Effect              string          `json:"effect"`
	Destinations        []string        `json:"destinations"`
	CredentialKind      string          `json:"credential_kind"`
	CostBound           wireMoney       `json:"cost_bound"`
	TimeoutSeconds      int64           `json:"timeout_seconds"`
	Idempotency         string          `json:"idempotency"`
	KeyRetentionSeconds int64           `json:"key_retention_seconds"`
	Confirmation        string          `json:"confirmation"`
	Reconciliation      string          `json:"reconciliation"`
	Adapter             string          `json:"adapter"`
}

// wireChallenge mirrors $defs/Challenge.
type wireChallenge struct {
	ID           contract.ID       `json:"id"`
	Version      int64             `json:"version"`
	ConnectionID contract.ID       `json:"connection_id"`
	State        string            `json:"state"`
	ExpiresAt    time.Time         `json:"expires_at"`
	ConsentURL   string            `json:"consent_url,omitempty"`
	HelperRef    string            `json:"helper_ref,omitempty"`
	Requirements []wireRequirement `json:"requirements,omitempty"`
}

// wireObservation mirrors $defs/Observation. Evidence stays inert JSON;
// provider bodies and secrets never enter it.
type wireObservation struct {
	Disposition       string          `json:"disposition"`
	Evidence          json.RawMessage `json:"evidence"`
	Usage             *wireUsage      `json:"usage"`
	ProviderReference string          `json:"provider_reference,omitempty"`
	ConfirmedAt       *time.Time      `json:"confirmed_at,omitempty"`
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

// wireValidation mirrors $defs/Validation: the internal candidate validation
// output collected by the compiler.
type wireValidation struct {
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
	Requirements []wireRequirement `json:"requirements"`
	Dependencies []wireRef         `json:"dependencies"`
}

// wireJob mirrors $defs/Job as returned by asynchronous operations. Result
// stays inert JSON.
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

// wireDisposition mirrors $defs/Disposition.
type wireDisposition struct {
	ID      contract.ID `json:"id"`
	Version int64       `json:"version"`
	State   string      `json:"state"`
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
// configuration schema-validated every entry when staged.
type wireDraft struct {
	ID           contract.ID       `json:"id"`
	Version      int64             `json:"version"`
	BaseRevision int64             `json:"base_revision"`
	Changes      []json.RawMessage `json:"changes"`
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
}

// Change kinds and actions. Connections owns the connection kind; binding
// changes route to configuration through the compiler.
const (
	kindConnection = "connection"
	kindBinding    = "binding"

	actionCreate  = "create"
	actionUpdate  = "update"
	actionArchive = "archive"
	actionDelete  = "delete"
)

// Connection validation states carried on the wire.
const (
	connStateUnverified = "unverified"
	connStateValid      = "valid"
	connStateInvalid    = "invalid"
	connStateExpired    = "expired"
	connStateRevoked    = "revoked"
)

// Private connection lifecycle state. The wire Connection def has no lifecycle
// field; archived connections stop resolving but stay queryable, so the state
// lives only in the owner-local table.
const (
	connLifecycleActive   = "active"
	connLifecycleArchived = "archived"
)

// Challenge states carried on the wire.
const (
	challengePending                = "pending"
	challengeExternalActionRequired = "external_action_required"
	challengeCompleted              = "completed"
	challengeCancelled              = "cancelled"
	challengeExpired                = "expired"
	challengeFailed                 = "failed"
)

// Setup challenge methods.
const (
	methodBrowser        = "browser"
	methodStoreReference = "store_reference"
)

// Observation dispositions.
const (
	obsSucceeded = "succeeded"
	obsFailed    = "failed"
	obsAccepted  = "accepted"
	obsUnknown   = "unknown"
	obsNotSent   = "not_sent"
)
