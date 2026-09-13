package installation

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs mirror the brief's embedded schema definitions. JSON integer is
// int64, UUID is contract.ID, timestamps are UTC RFC3339Nano strings,
// optional scalars are pointers, arrays are slices, and unknown fields are
// rejected on input.

// wireScope is the Scope resource shape.
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

// wireRequirement is the Requirement resource shape.
type wireRequirement struct {
	Code        string       `json:"code"`
	Message     string       `json:"message"`
	ResourceID  *contract.ID `json:"resource_id,omitempty"`
	ChallengeID *contract.ID `json:"challenge_id,omitempty"`
}

// wireRef is the Ref resource shape (id plus version).
type wireRef struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
}

// wireArtifactRef is the ArtifactRef resource shape.
type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// wireStatus is the Status resource shape returned by init, doctor and
// status, and by maintenance.enter/pause/resume.
type wireStatus struct {
	InstallationID contract.ID       `json:"installation_id"`
	Generation     int64             `json:"generation"`
	Paused         bool              `json:"paused"`
	Maintenance    bool              `json:"maintenance"`
	Initialized    bool              `json:"initialized"`
	Requirements   []wireRequirement `json:"requirements"`
	Version        contract.Version  `json:"version"`
}

// wireJob is the Job resource shape.
type wireJob struct {
	ID             contract.ID       `json:"id"`
	Version        contract.Version  `json:"version"`
	Kind           string            `json:"kind"`
	State          string            `json:"state"`
	Requirements   []wireRequirement `json:"requirements"`
	ResultArtifact *wireArtifactRef  `json:"result_artifact,omitempty"`
	OperationID    *contract.ID      `json:"operation_id,omitempty"`
	Owner          string            `json:"owner"`
	Operation      string            `json:"operation"`
	Result         json.RawMessage   `json:"result,omitempty"`
}

// wireBackup is the Backup resource shape: the eventual installation.backup
// job result.
type wireBackup struct {
	ID               contract.ID      `json:"id"`
	Version          contract.Version `json:"version"`
	Artifact         wireArtifactRef  `json:"artifact"`
	InstallationID   contract.ID      `json:"installation_id"`
	CreatedAt        string           `json:"created_at"`
	BrainRevisions   []wireRef        `json:"brain_revisions"`
	KeyPrerequisites []string         `json:"key_prerequisites"`
	Verified         bool             `json:"verified"`
}

// resourceOut wraps a single resource under the literal "resource" key every
// operation output uses.
type resourceOut[T any] struct {
	Resource T `json:"resource"`
}

// Input DTOs. Strict decoding plus schema validation precede every handler.

type initInput struct {
	CredentialStore string  `json:"credential_store"`
	OwnerName       string  `json:"owner_name"`
	HeadlessKeyRef  *string `json:"headless_key_ref,omitempty"`
}

type scopeInput struct {
	Scope wireScope `json:"scope"`
}

type versionedScopeInput struct {
	Scope           wireScope        `json:"scope"`
	ExpectedVersion contract.Version `json:"expected_version"`
}

type jobGetInput struct {
	Scope wireScope   `json:"scope"`
	ID    contract.ID `json:"id"`
}

type restoreInput struct {
	Scope           wireScope        `json:"scope"`
	BackupArtifact  wireArtifactRef  `json:"backup_artifact"`
	ExpectedVersion contract.Version `json:"expected_version"`
}

// restoreRecordInput is the internal _installation.restore.record input: the
// controller reports a maintenance job's disposition after it independently
// performed the file-level IO and verification this package cannot reach.
type restoreRecordInput struct {
	JobID        contract.ID       `json:"job_id"`
	State        string            `json:"state"`
	Requirements []wireRequirement `json:"requirements"`
}
