package contract

import (
	"context"
	"encoding/json"
)

// WorkerRequest is one worker-authored local proposal's request to invoke
// an ordinary public operation under the worker's own authenticated actor.
// It is a trusted-runtime-composition value, not a JSON wire document in
// its own right: WorkerOperator resolves TurnID/WorkerID against the
// persisted turn/worker mapping and re-enters ordinary application
// authorization for Operation/Version/Input exactly as a CLI/MCP caller
// would.
type WorkerRequest struct {
	TurnID        ID
	ProposalID    string
	WorkerID      ID
	Scope         Scope
	Operation     string
	Version       Version
	SubmissionKey string
	Input         json.RawMessage
}

// WorkerOperator lets a worker-authored local proposal invoke an ordinary
// public operation under the worker's own authenticated actor, scope-
// intersected with the task/source authorization envelope. It resolves the
// actor from the persisted turn/worker mapping: WorkerID is an asserted
// match against that turn, never a way to select any principal. Source
// user text never becomes authority, and the controller's administrative
// identity authorizes scheduling/bookkeeping only, never a model proposal.
//
// Implemented by application; injected only into trusted runtime
// composition (execution, controller). Callers must not construct a public
// handler call directly with an invented Actor and must not use controller
// privileges to bypass a worker denial.
type WorkerOperator interface {
	ExecuteWorker(ctx context.Context, request WorkerRequest) (Result, error)
}

// JobWork is the minimal shared durable-job input, kept in contract (not
// controller) to avoid a domain->controller import. Execution owns the
// durable ledger (execution_jobs); owners retain their domain rows and
// receive idempotent typed completion callbacks through JobOutcome.
type JobWork struct {
	ID         ID
	Version    Version
	Generation int64
	Owner      string
	Operation  string
	Scope      Scope
	Input      json.RawMessage
}

// JobOutcome is a LocalJobRunner's established result for one JobWork.
// State is the terminal (or outcome_unknown) job state; Result matches the
// originating operation's declared completion_schema.
type JobOutcome struct {
	State        string
	Result       json.RawMessage
	EvidenceIDs  []ID
	Requirements []Requirement
}

// LocalJobRunner performs bounded approved IO outside a Unit for one
// durable job. Local runners do no direct domain writes: prepare/claim
// occurs through owner ports, RunJob performs the bounded IO, the
// controller publishes outputs, then owner finish and execution record
// commit together. A runner that needs governed network IO must create
// effects through its owner pipeline instead of invoking a provider ad hoc.
type LocalJobRunner interface {
	RunJob(ctx context.Context, work JobWork) (JobOutcome, error)
}

// Requirement mirrors the shared $defs/Requirement schema (RequirementSchema
// below): a stable code and message plus optional resource/challenge
// references. It is the strict wire shape for JobOutcome.Requirements and
// every owning package's local mirror of the same $def.
type Requirement struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	ResourceID  *ID    `json:"resource_id,omitempty"`
	ChallengeID *ID    `json:"challenge_id,omitempty"`
}

// SnapshotInventory is a narrow entrypoint-owned capability, separate from
// ordinary DatabaseBackup, supplied only to internal/installation.
type SnapshotInventory interface {
	// Inventory returns the exact BackupManifest document (frozen in
	// docs/implementation/adapter-schemas.json), declared as json.RawMessage
	// here for the same reason Verification/VerificationResult are: this
	// package does not import a domain schema type.
	Inventory(ctx context.Context) (json.RawMessage, error)
}

// RestoreCoordinator implements the six-step restore protocol
// (contract-proposals.md section 7; docs/implementation/contracts.md
// "Restore protocol (revision 3)"), supplied by entrypoint assembly to
// internal/installation only, exactly as DatabaseBackup is: never to the
// controller or any other module.
type RestoreCoordinator interface {
	// Prepare validates the source bundle and stages a RecoveryOverlay (the
	// exact document frozen in docs/implementation/adapter-schemas.json); it
	// performs no destructive action.
	Prepare(ctx context.Context, source ArtifactRef) (json.RawMessage, error)
	// Commit atomically switches the database/blobs under exclusive
	// maintenance ownership after Prepare succeeds and the caller has
	// captured the current recovery overlay (restore protocol step 3).
	// overlay is the exact RecoveryOverlay Prepare returned, unmodified.
	Commit(ctx context.Context, overlay json.RawMessage) error
}
