package artifacts

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs for the artifacts operations. JSON is snake_case; optional
// scalars are pointers; unknown fields are rejected at the decode boundary.
// The shapes mirror the operation schemas embedded in the implementation
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

// wireArtifact is one immutable artifact metadata document.
type wireArtifact struct {
	ID             contract.ID      `json:"id"`
	Version        contract.Version `json:"version"`
	Scope          wireScope        `json:"scope"`
	Digest         contract.Digest  `json:"digest"`
	Size           int64            `json:"size"`
	MediaType      string           `json:"media_type"`
	Classification string           `json:"classification"`
	Encrypted      bool             `json:"encrypted"`
	State          string           `json:"state"`
	CreatedAt      string           `json:"created_at"`
}

// wireUpload is one resumable bounded upload.
type wireUpload struct {
	ID             contract.ID      `json:"id"`
	Version        contract.Version `json:"version"`
	Scope          wireScope        `json:"scope"`
	ExpectedSize   int64            `json:"expected_size"`
	ExpectedDigest contract.Digest  `json:"expected_digest"`
	ReceivedSize   int64            `json:"received_size"`
	ExpiresAt      string           `json:"expires_at"`
	State          string           `json:"state"`
}

// wireDisposition records the disposition of an upload mutation command.
type wireDisposition struct {
	ID           contract.ID       `json:"id"`
	Version      contract.Version  `json:"version"`
	State        string            `json:"state"`
	Job          *wireJob          `json:"job,omitempty"`
	Requirements []wireRequirement `json:"requirements,omitempty"`
}

// ---------- operation input bodies ----------

type metadataInput struct {
	Scope     wireScope         `json:"scope"`
	Artifacts []wireArtifactRef `json:"artifacts"`
}

type publishInput struct {
	Scope          wireScope       `json:"scope"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Encrypted      bool            `json:"encrypted"`
}

type artifactIDInput struct {
	Scope wireScope   `json:"scope"`
	ID    contract.ID `json:"id"`
}

type artifactListFilter struct {
	State          *string      `json:"state,omitempty"`
	Key            *string      `json:"key,omitempty"`
	ParentID       *contract.ID `json:"parent_id,omitempty"`
	WorkerID       *contract.ID `json:"worker_id,omitempty"`
	TaskID         *contract.ID `json:"task_id,omitempty"`
	OrganizationID *contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool        `json:"descendants,omitempty"`
	NeedsYou       *bool        `json:"needs_you,omitempty"`
}

type artifactListInput struct {
	Scope  wireScope           `json:"scope"`
	Cursor *string             `json:"cursor,omitempty"`
	Limit  *int64              `json:"limit,omitempty"`
	Filter *artifactListFilter `json:"filter,omitempty"`
}

type readInput struct {
	Scope  wireScope   `json:"scope"`
	ID     contract.ID `json:"id"`
	Offset int64       `json:"offset"`
	Length int64       `json:"length"`
}

type uploadBeginInput struct {
	Scope          wireScope       `json:"scope"`
	Size           int64           `json:"size"`
	Digest         contract.Digest `json:"digest"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
}

type uploadRefInput struct {
	Scope           wireScope   `json:"scope"`
	UploadID        contract.ID `json:"upload_id"`
	ExpectedVersion int64       `json:"expected_version"`
}

type chunkInput struct {
	Scope       wireScope       `json:"scope"`
	UploadID    contract.ID     `json:"upload_id"`
	Offset      int64           `json:"offset"`
	BytesB64    string          `json:"bytes_base64"`
	ChunkDigest contract.Digest `json:"chunk_digest"`
}

type exportInput struct {
	Scope wireScope   `json:"scope"`
	ID    contract.ID `json:"id"`
}

// ---------- operation output bodies ----------

type artifactOutput struct {
	Resource wireArtifact `json:"resource"`
}

type metadataOutput struct {
	Artifacts []wireArtifact `json:"artifacts"`
}

type artifactListOutput struct {
	Items []wireArtifact `json:"items"`
}

type uploadOutput struct {
	Resource wireUpload `json:"resource"`
}

type readOutput struct {
	BytesB64  string          `json:"bytes_base64"`
	Digest    contract.Digest `json:"digest"`
	Offset    int64           `json:"offset"`
	TotalSize int64           `json:"total_size"`
}

type jobOutput struct {
	Resource wireJob `json:"resource"`
}

// uploadCancelOutput is the artifact.upload.cancel oneOf body: the
// disposition of the cancel command in the synchronous path, or a job when
// the controller recovers the cancellation.
type uploadCancelOutput struct {
	Resource *wireDisposition `json:"resource,omitempty"`
	Job      *wireJob         `json:"job,omitempty"`
}

// uploadChunkOutput is the artifact.upload.chunk oneOf body.
type uploadChunkOutput struct {
	Resource *wireUpload `json:"resource,omitempty"`
	Job      *wireJob    `json:"job,omitempty"`
}

// uploadFinishOutput is the artifact.upload.finish oneOf body.
type uploadFinishOutput struct {
	Resource *wireArtifact `json:"resource,omitempty"`
	Job      *wireJob      `json:"job,omitempty"`
}
