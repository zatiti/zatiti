package artifacts

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// defaultUploadExpiry bounds a resumable upload's lifetime (Wire conventions
// and limits: upload expiry 24 hours).
const defaultUploadExpiry = 24 * time.Hour

// defaultListLimit is the page size a caller receives when it omits limit.
const defaultListLimit = 50

// handleMetadata is the _artifacts.metadata boundary: validate scope,
// digests, availability and classification of pinned artifacts before
// disclosure or acceptance. A reference that does not resolve to an
// existing artifact whose digest and scope match is silently omitted from
// the result; callers check the returned count against what they expect.
func handleMetadata(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[metadataInput](opMetadata, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	out := make([]wireArtifact, 0, len(in.Artifacts))
	seen := map[contract.ID]bool{}
	for _, ref := range in.Artifacts {
		if seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		a, err := loadArtifact(ctx, unit, ref.ID)
		if err != nil {
			if isFault(err, contract.CodeNotFound) {
				continue
			}
			return contract.Payload{}, err
		}
		if narrowScope(in.Scope.toContract(), a.OrganizationID, a.ProjectID, a.WorkerID, a.TaskID) != nil {
			continue
		}
		if ref.Digest != "" && ref.Digest != a.Digest {
			continue
		}
		out = append(out, a.wire())
	}
	return s.completed(metadataOutput{Artifacts: out})
}

// isFault reports whether err carries a *contract.Fault with the given code.
func isFault(err error, code string) bool {
	var f *contract.Fault
	return asFault(err, &f) && f != nil && f.Code == code
}

// handlePublish is the _artifacts.publish boundary: publish metadata only
// after a trusted local IO phase has staged/hashed/published bytes. This
// handler never touches the blob store — no filesystem streaming is
// permitted inside a Unit — so it trusts the caller's own LocalIO Perform
// phase already published the exact digest under the shared BlobStore.
func handlePublish(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[publishInput](opPublish, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	if !in.Encrypted {
		return contract.Payload{}, invalidInput(
			"published artifact metadata must declare encrypted=true; every blob store object is encrypted at rest")
	}
	now := s.deps.Clock.Now()
	a := &artifactRow{
		ID:             s.deps.IDs.New(),
		Version:        1,
		InstallationID: in.Scope.InstallationID,
		OrganizationID: in.Scope.OrganizationID,
		ProjectID:      in.Scope.ProjectID,
		WorkerID:       in.Scope.WorkerID,
		TaskID:         in.Scope.TaskID,
		Scope:          in.Scope.toContract(),
		Digest:         in.Digest,
		Size:           in.Size,
		MediaType:      in.MediaType,
		Classification: in.Classification,
		Encrypted:      true,
		State:          "available",
		CreatedAt:      now,
	}
	if err := insertArtifact(ctx, unit, a); err != nil {
		return contract.Payload{}, err
	}
	if err := insertPin(ctx, unit, s.deps.IDs.New(), a.ID, "published", now); err != nil {
		return contract.Payload{}, err
	}
	data, err := marshalData(a.wire())
	if err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventArtifactPublished, a.ID, a.Version, data); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(artifactOutput{Resource: a.wire()})
}

// handleArtifactGet is the artifact.get boundary: resolve exact identity
// under current authorization; a scope mismatch or missing artifact never
// discloses cross-scope data. A faulted artifact is still returned — its
// state field carries the honest observation rather than hiding it as
// not_found.
func handleArtifactGet(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[artifactIDInput](opArtifactGet, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	a, err := loadArtifact(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := narrowScope(in.Scope.toContract(), a.OrganizationID, a.ProjectID, a.WorkerID, a.TaskID); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(artifactOutput{Resource: a.wire()})
}

// listFilterFingerprint is the subset of the list filter that participates
// in the cursor's filter fingerprint; only fields artifacts supports.
type listFilterFingerprint struct {
	State string `json:"state"`
}

// handleArtifactList is the artifact.list boundary: read an authorized
// consistent snapshot; apply scope and filter before pagination. Filters are
// structured exact-match fields; artifacts supports only state, and any
// other populated filter field refuses invalid_input rather than silently
// ignoring an unsupported narrowing the caller asked for.
func handleArtifactList(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[artifactListInput](opArtifactList, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	var stateFilter string
	if in.Filter != nil {
		if in.Filter.Key != nil || in.Filter.ParentID != nil || in.Filter.WorkerID != nil ||
			in.Filter.TaskID != nil || in.Filter.OrganizationID != nil ||
			in.Filter.Descendants != nil || in.Filter.NeedsYou != nil {
			return contract.Payload{}, invalidInput("artifact.list supports only the state filter field")
		}
		if in.Filter.State != nil {
			if *in.Filter.State != "available" && *in.Filter.State != "fault" {
				return contract.Payload{}, invalidInput("artifact.list state filter must be available or fault")
			}
			stateFilter = *in.Filter.State
		}
	}
	limit := int64(defaultListLimit)
	if in.Limit != nil {
		limit = *in.Limit
	}

	conds := []string{"installation_id = ?"}
	args := []any{in.Scope.InstallationID}
	if in.Scope.OrganizationID != "" {
		conds = append(conds, "organization_id = ?")
		args = append(args, in.Scope.OrganizationID)
	}
	if in.Scope.ProjectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, in.Scope.ProjectID)
	}
	if in.Scope.WorkerID != "" {
		conds = append(conds, "worker_id = ?")
		args = append(args, in.Scope.WorkerID)
	}
	if in.Scope.TaskID != "" {
		conds = append(conds, "task_id = ?")
		args = append(args, in.Scope.TaskID)
	}
	if stateFilter != "" {
		conds = append(conds, "state = ?")
		args = append(args, stateFilter)
	}

	fingerprint := listFilterFingerprint{State: stateFilter}
	if in.Cursor != nil && *in.Cursor != "" {
		createdAt, lastID, cerr := s.readCursor(opArtifactList, unit, fingerprint, *in.Cursor)
		if cerr != nil {
			return contract.Payload{}, cerr
		}
		conds = append(conds, "(created_at < ? OR (created_at = ? AND id < ?))")
		stamp := formatStamp(createdAt)
		args = append(args, stamp, stamp, string(lastID))
	}

	rows, err := listArtifacts(ctx, unit, conds, args, limit+1)
	if err != nil {
		return contract.Payload{}, err
	}
	var nextCursor *string
	if int64(len(rows)) > limit {
		last := rows[limit-1]
		rows = rows[:limit]
		cursor, cerr := s.mintCursor(opArtifactList, unit, fingerprint, last.CreatedAt, last.ID, s.deps.Clock.Now())
		if cerr != nil {
			return contract.Payload{}, cerr
		}
		nextCursor = &cursor
	}
	items := make([]wireArtifact, 0, len(rows))
	for _, a := range rows {
		items = append(items, a.wire())
	}
	raw, err := marshalData(artifactListOutput{Items: items})
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw, NextCursor: nextCursor}, nil
}

// handleUploadBegin is the artifact.upload.begin boundary: allocate a
// bounded staged upload tied to caller/scope/digest/size; no published
// artifact exists yet.
func handleUploadBegin(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[uploadBeginInput](opUploadBegin, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	now := s.deps.Clock.Now()
	u := &uploadRow{
		ID:             s.deps.IDs.New(),
		Version:        1,
		InstallationID: in.Scope.InstallationID,
		OrganizationID: in.Scope.OrganizationID,
		ProjectID:      in.Scope.ProjectID,
		WorkerID:       in.Scope.WorkerID,
		TaskID:         in.Scope.TaskID,
		Scope:          in.Scope.toContract(),
		ExpectedSize:   in.Size,
		ExpectedDigest: in.Digest,
		ReceivedSize:   0,
		MediaType:      in.MediaType,
		Classification: in.Classification,
		State:          "open",
		ExpiresAt:      now.Add(defaultUploadExpiry),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := insertUpload(ctx, unit, u); err != nil {
		return contract.Payload{}, err
	}
	data, err := marshalData(u.wire())
	if err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventUploadBegun, u.ID, u.Version, data); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(uploadOutput{Resource: u.wire()})
}

// marshalJSON is a small alias kept for symmetry with other handlers in this
// package that need a bare json.RawMessage without the Payload wrapper.
func marshalJSON(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, internalError("encoding failed")
	}
	return json.RawMessage(raw), nil
}
