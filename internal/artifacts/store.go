package artifacts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Storage access. Every statement runs on the unit's transaction; scope
// dimensions are stored as explicit columns for filtered reads and the
// canonical scope JSON for wire output. Timestamps persist as UTC
// RFC3339Nano strings so lexicographic comparison matches chronological
// order. Updates are version fenced: a statement that matches no row means
// another writer moved the resource first and the caller sees stale_version.

// artifactRow is the storage representation of one immutable artifact.
type artifactRow struct {
	ID             contract.ID
	Version        contract.Version
	InstallationID contract.ID
	OrganizationID contract.ID
	ProjectID      contract.ID
	WorkerID       contract.ID
	TaskID         contract.ID
	Scope          contract.Scope
	Digest         contract.Digest
	Size           int64
	MediaType      string
	Classification string
	Encrypted      bool
	State          string
	CreatedAt      time.Time
}

// uploadRow is the storage representation of one resumable upload.
type uploadRow struct {
	ID             contract.ID
	Version        contract.Version
	InstallationID contract.ID
	OrganizationID contract.ID
	ProjectID      contract.ID
	WorkerID       contract.ID
	TaskID         contract.ID
	Scope          contract.Scope
	ExpectedSize   int64
	ExpectedDigest contract.Digest
	ReceivedSize   int64
	MediaType      string
	Classification string
	State          string
	ArtifactID     contract.ID
	ExpiresAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// chunkRow is the storage representation of one received upload chunk.
type chunkRow struct {
	UploadID  contract.ID
	Offset    int64
	Length    int64
	Digest    contract.Digest
	CreatedAt time.Time
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// formatStamp renders a UTC timestamp at fixed nanosecond width so stored
// stamps order lexicographically; the zero time renders as the empty string
// so optional stamp columns stay distinct from the epoch.
func formatStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

func parseStamp(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("artifacts: parse stamp: %w", err)
	}
	return t, nil
}

func encodeJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("artifacts: encode json: %w", err)
	}
	return string(raw), nil
}

func decodeJSON(raw string, out any) error {
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("artifacts: decode json: %w", err)
	}
	return nil
}

func encodeScope(scope contract.Scope) (string, error) { return encodeJSON(scope) }

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// expectOneRow enforces optimistic fencing: a version-fenced update that
// matches no row lost the race to a concurrent writer.
func expectOneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("artifacts: confirm row: %w", err)
	}
	if n != 1 {
		return staleVersion("the resource changed concurrently; retry with the current version")
	}
	return nil
}

const artifactColumns = `id, version, installation_id, organization_id, project_id,
worker_id, task_id, scope_json, digest, size, media_type, classification,
encrypted, state, created_at`

func scanArtifact(scan func(dest ...any) error) (*artifactRow, error) {
	var a artifactRow
	var scopeJSON string
	var encrypted int64
	var created string
	err := scan(&a.ID, &a.Version, &a.InstallationID, &a.OrganizationID, &a.ProjectID,
		&a.WorkerID, &a.TaskID, &scopeJSON, &a.Digest, &a.Size, &a.MediaType,
		&a.Classification, &encrypted, &a.State, &created)
	if err != nil {
		return nil, err
	}
	if err := decodeJSON(scopeJSON, &a.Scope); err != nil {
		return nil, err
	}
	a.Encrypted = encrypted == 1
	if a.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	return &a, nil
}

// loadArtifact reads one artifact by id.
func loadArtifact(ctx context.Context, unit contract.Unit, id contract.ID) (*artifactRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+artifactColumns+` FROM artifacts_metadata WHERE id = ?`, id)
	a, err := scanArtifact(row.Scan)
	if isNoRows(err) {
		return nil, notFound("artifact %s does not exist", id)
	}
	return a, err
}

// insertArtifact persists a new artifact at version 1.
func insertArtifact(ctx context.Context, unit contract.Unit, a *artifactRow) error {
	scopeJSON, err := encodeScope(a.Scope)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO artifacts_metadata
		(id, version, installation_id, organization_id, project_id, worker_id,
		 task_id, scope_json, digest, size, media_type, classification,
		 encrypted, state, created_at)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.InstallationID, a.OrganizationID, a.ProjectID, a.WorkerID, a.TaskID,
		scopeJSON, a.Digest, a.Size, a.MediaType, a.Classification,
		boolInt(a.Encrypted), a.State, formatStamp(a.CreatedAt))
	return err
}

// markArtifactFault transitions an available artifact to the fault state,
// bumping its version, and records the durable observation that caused it.
// A row already in fault state is left unchanged: the first observation
// stands.
func (s *Service) markArtifactFault(ctx context.Context, unit contract.Unit, a *artifactRow, code, message string) error {
	if a.State == "fault" {
		return nil
	}
	res, err := unit.ExecContext(ctx, `UPDATE artifacts_metadata SET version = ?, state = 'fault'
		WHERE id = ? AND version = ?`, a.Version+1, a.ID, a.Version)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	a.Version++
	a.State = "fault"
	return insertFault(ctx, unit, s.deps.IDs.New(), a.ID, code, message, s.deps.Clock.Now())
}

// listArtifacts reads artifacts matching the conditions, newest first.
func listArtifacts(ctx context.Context, unit contract.Unit, conds []string, args []any, limit int64) ([]*artifactRow, error) {
	query := `SELECT ` + artifactColumns + ` FROM artifacts_metadata`
	if len(conds) > 0 {
		query += ` WHERE ` + joinConds(conds)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := unit.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*artifactRow
	for rows.Next() {
		a, err := scanArtifact(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func joinConds(conds []string) string {
	out := conds[0]
	for _, c := range conds[1:] {
		out += " AND " + c
	}
	return out
}

const uploadColumns = `id, version, installation_id, organization_id, project_id,
worker_id, task_id, scope_json, expected_size, expected_digest, received_size,
media_type, classification, state, artifact_id, expires_at, created_at, updated_at`

func scanUpload(scan func(dest ...any) error) (*uploadRow, error) {
	var u uploadRow
	var scopeJSON string
	var expires, created, updated string
	err := scan(&u.ID, &u.Version, &u.InstallationID, &u.OrganizationID, &u.ProjectID,
		&u.WorkerID, &u.TaskID, &scopeJSON, &u.ExpectedSize, &u.ExpectedDigest, &u.ReceivedSize,
		&u.MediaType, &u.Classification, &u.State, &u.ArtifactID, &expires, &created, &updated)
	if err != nil {
		return nil, err
	}
	if err := decodeJSON(scopeJSON, &u.Scope); err != nil {
		return nil, err
	}
	if u.ExpiresAt, err = parseStamp(expires); err != nil {
		return nil, err
	}
	if u.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	if u.UpdatedAt, err = parseStamp(updated); err != nil {
		return nil, err
	}
	return &u, nil
}

// loadUpload reads one upload by id.
func loadUpload(ctx context.Context, unit contract.Unit, id contract.ID) (*uploadRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+uploadColumns+` FROM artifacts_uploads WHERE id = ?`, id)
	u, err := scanUpload(row.Scan)
	if isNoRows(err) {
		return nil, notFound("upload %s does not exist", id)
	}
	return u, err
}

// insertUpload persists a new upload at version 1.
func insertUpload(ctx context.Context, unit contract.Unit, u *uploadRow) error {
	scopeJSON, err := encodeScope(u.Scope)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO artifacts_uploads
		(id, version, installation_id, organization_id, project_id, worker_id,
		 task_id, scope_json, expected_size, expected_digest, received_size,
		 media_type, classification, state, artifact_id, expires_at, created_at, updated_at)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, '', ?, ?, ?)`,
		u.ID, u.InstallationID, u.OrganizationID, u.ProjectID, u.WorkerID, u.TaskID,
		scopeJSON, u.ExpectedSize, u.ExpectedDigest, u.MediaType, u.Classification,
		u.State, formatStamp(u.ExpiresAt), formatStamp(u.CreatedAt), formatStamp(u.UpdatedAt))
	return err
}

// updateUpload applies a version-fenced mutation to one upload; the caller
// mutates the row struct first, and the stored version bumps on success.
func updateUpload(ctx context.Context, unit contract.Unit, u *uploadRow) error {
	res, err := unit.ExecContext(ctx, `UPDATE artifacts_uploads SET
		version = ?, received_size = ?, state = ?, artifact_id = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		u.Version+1, u.ReceivedSize, u.State, u.ArtifactID, formatStamp(u.UpdatedAt),
		u.ID, u.Version)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	u.Version++
	return nil
}

// loadChunk reads one chunk of an upload at the given offset; nil, nil when
// no chunk starts at that exact offset.
func loadChunk(ctx context.Context, unit contract.Unit, uploadID contract.ID, offset int64) (*chunkRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT upload_id, offset_bytes, length, digest, created_at
		FROM artifacts_chunks WHERE upload_id = ? AND offset_bytes = ?`, uploadID, offset)
	var c chunkRow
	var created string
	err := row.Scan(&c.UploadID, &c.Offset, &c.Length, &c.Digest, &created)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	return &c, nil
}

// insertChunk persists one received chunk.
func insertChunk(ctx context.Context, unit contract.Unit, c *chunkRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO artifacts_chunks
		(upload_id, offset_bytes, length, digest, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		c.UploadID, c.Offset, c.Length, c.Digest, formatStamp(c.CreatedAt))
	return err
}

// listChunks reads every chunk of one upload in offset order.
func listChunks(ctx context.Context, unit contract.Unit, uploadID contract.ID) ([]*chunkRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT upload_id, offset_bytes, length, digest, created_at
		FROM artifacts_chunks WHERE upload_id = ? ORDER BY offset_bytes`, uploadID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*chunkRow
	for rows.Next() {
		var c chunkRow
		var created string
		if err := rows.Scan(&c.UploadID, &c.Offset, &c.Length, &c.Digest, &created); err != nil {
			return nil, err
		}
		if c.CreatedAt, err = parseStamp(created); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// insertPin records one retention pin against an artifact.
func insertPin(ctx context.Context, unit contract.Unit, id, artifactID contract.ID, reason string, at time.Time) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO artifacts_pins
		(id, artifact_id, reason, created_at) VALUES (?, ?, ?, ?)`,
		id, artifactID, reason, formatStamp(at))
	return err
}

// insertFault records one durable integrity fault against an artifact.
func insertFault(ctx context.Context, unit contract.Unit, id, artifactID contract.ID, code, message string, at time.Time) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO artifacts_faults
		(id, artifact_id, code, message, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, artifactID, code, message, formatStamp(at))
	return err
}
