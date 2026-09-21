package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Storage rows and access helpers. All timestamps persist as UTC RFC3339Nano
// strings and all JSON documents persist in canonical form, matching the
// shared storage conventions used across every domain owner.

// isNoRows reports whether err is the standard empty-scan result.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// formatStamp renders a UTC RFC3339Nano timestamp.
func formatStamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseStamp parses a stored UTC RFC3339Nano timestamp.
func parseStamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// nullStamp renders an optional timestamp: empty string when zero.
func nullStamp(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatStamp(t), Valid: true}
}

// parseNullStamp parses an optional stored timestamp column.
func parseNullStamp(s sql.NullString) (time.Time, error) {
	if !s.Valid || s.String == "" {
		return time.Time{}, nil
	}
	return parseStamp(s.String)
}

// jsonColumn marshals a value to its canonical JSON storage form.
func jsonColumn(v any) (string, error) {
	raw, err := canonicalJSON(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Brain kinds. Each is a distinct logical access boundary: one worker brain
// per worker, one shared brain per organization, and one installation-wide
// brain for deliberately shared cross-organizational knowledge (R15-003).
const (
	brainKindInstallation = "installation"
	brainKindOrganization = "organization"
	brainKindWorker       = "worker"
)

// brainRow is one memory_brains record.
type brainRow struct {
	ID                   contract.ID
	Version              int64
	InstallationID       contract.ID
	OrganizationID       contract.ID
	WorkerID             contract.ID
	Kind                 string
	Classification       string
	State                string
	Endpoint             string
	RootRef              string
	WriterOwner          string
	AllowedDestinations  []string
	ConnectionRefID      contract.ID
	ConnectionRefVersion int64
	ToolRefID            contract.ID
	ToolRefVersion       int64
	FreshnessAt          time.Time
	Revision             string
	Digest               contract.Digest
	IndexRevision        string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// provisioned reports whether a Serenity writer/connection is bound to this
// brain. No operation in this package's own catalog currently populates a
// connection reference (see the final report); until one does, every brain
// this package creates stays unprovisioned and governed dispatch refuses
// with prerequisite_missing rather than attempting a call with no
// destination.
func (b *brainRow) provisioned() bool {
	return b.State == brainActive && b.ConnectionRefID != "" && b.ToolRefID != ""
}

const brainColumns = `id, version, installation_id, organization_id, worker_id, kind, classification, state,
	endpoint, root_ref, writer_owner, allowed_destinations_json, connection_ref_id, connection_ref_version,
	tool_ref_id, tool_ref_version, freshness_at, revision, digest, index_revision, created_at, updated_at`

func scanBrain(scan func(dest ...any) error) (*brainRow, error) {
	var b brainRow
	var allowed string
	var freshness sql.NullString
	var created, updated string
	err := scan(&b.ID, &b.Version, &b.InstallationID, &b.OrganizationID, &b.WorkerID, &b.Kind, &b.Classification,
		&b.State, &b.Endpoint, &b.RootRef, &b.WriterOwner, &allowed, &b.ConnectionRefID, &b.ConnectionRefVersion,
		&b.ToolRefID, &b.ToolRefVersion, &freshness, &b.Revision, &b.Digest, &b.IndexRevision, &created, &updated)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(allowed), &b.AllowedDestinations); err != nil {
		return nil, fmt.Errorf("memory: brain %s allowed destinations: %w", b.ID, err)
	}
	var perr error
	b.FreshnessAt, perr = parseNullStamp(freshness)
	if perr != nil {
		return nil, fmt.Errorf("memory: brain %s freshness: %w", b.ID, perr)
	}
	b.CreatedAt, perr = parseStamp(created)
	if perr != nil {
		return nil, fmt.Errorf("memory: brain %s timestamp: %w", b.ID, perr)
	}
	b.UpdatedAt, perr = parseStamp(updated)
	if perr != nil {
		return nil, fmt.Errorf("memory: brain %s timestamp: %w", b.ID, perr)
	}
	return &b, nil
}

func insertBrain(ctx context.Context, unit contract.Unit, b *brainRow) error {
	allowed, err := jsonColumn(b.AllowedDestinations)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO memory_brains (`+brainColumns+`) VALUES
		(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(b.ID), b.Version, string(b.InstallationID), string(b.OrganizationID), string(b.WorkerID),
		b.Kind, b.Classification, b.State, b.Endpoint, b.RootRef, b.WriterOwner, allowed,
		string(b.ConnectionRefID), b.ConnectionRefVersion, string(b.ToolRefID), b.ToolRefVersion,
		nullStamp(b.FreshnessAt), b.Revision, string(b.Digest), b.IndexRevision,
		formatStamp(b.CreatedAt), formatStamp(b.UpdatedAt))
	return err
}

func loadBrain(ctx context.Context, unit contract.Unit, id contract.ID) (*brainRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+brainColumns+` FROM memory_brains WHERE id = ?`, string(id))
	return scanBrain(row.Scan)
}

// loadBrainByScope finds the one brain that occupies a (installation,
// organization, worker, kind) scope slot, matching the unique index.
func loadBrainByScope(ctx context.Context, unit contract.Unit, install, org, worker contract.ID, kind string) (*brainRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+brainColumns+` FROM memory_brains
		WHERE installation_id = ? AND organization_id = ? AND worker_id = ? AND kind = ?`,
		string(install), string(org), string(worker), kind)
	return scanBrain(row.Scan)
}

// listBrains returns every brain in one installation, ordered for stable
// manifest output.
func listBrains(ctx context.Context, unit contract.Unit, install contract.ID) ([]*brainRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+brainColumns+` FROM memory_brains
		WHERE installation_id = ? ORDER BY created_at, id`, string(install))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*brainRow
	for rows.Next() {
		b, err := scanBrain(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// recordBrainRevision updates a brain's observed revision/digest/freshness
// after a Serenity attestation, without changing its provisioning fields.
func recordBrainRevision(ctx context.Context, unit contract.Unit, b *brainRow, revision string, digest contract.Digest, indexRevision string, freshness time.Time, now time.Time) error {
	_, err := unit.ExecContext(ctx, `UPDATE memory_brains SET version = version + 1,
		revision = ?, digest = ?, index_revision = ?, freshness_at = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		revision, string(digest), indexRevision, nullStamp(freshness), formatStamp(now), string(b.ID), b.Version)
	if err != nil {
		return err
	}
	b.Version++
	b.Revision, b.Digest, b.IndexRevision, b.FreshnessAt, b.UpdatedAt = revision, digest, indexRevision, freshness, now
	return nil
}

// bindingRow is one memory_bindings record.
type bindingRow struct {
	ID             contract.ID
	Version        int64
	InstallationID contract.ID
	OrganizationID contract.ID
	ProjectID      contract.ID
	WorkerID       contract.ID
	TaskID         contract.ID
	BrainID        contract.ID
	Permissions    []string
	Classification string
	State          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (b *bindingRow) scope() wireScope {
	return wireScope{
		InstallationID: b.InstallationID, OrganizationID: b.OrganizationID,
		ProjectID: b.ProjectID, WorkerID: b.WorkerID, TaskID: b.TaskID,
	}
}

func (b *bindingRow) hasPermission(perm string) bool {
	for _, p := range b.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

const bindingColumns = `id, version, installation_id, organization_id, project_id, worker_id, task_id,
	brain_id, permissions_json, classification, state, created_at, updated_at`

func scanBinding(scan func(dest ...any) error) (*bindingRow, error) {
	var b bindingRow
	var permissions string
	var created, updated string
	err := scan(&b.ID, &b.Version, &b.InstallationID, &b.OrganizationID, &b.ProjectID, &b.WorkerID, &b.TaskID,
		&b.BrainID, &permissions, &b.Classification, &b.State, &created, &updated)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(permissions), &b.Permissions); err != nil {
		return nil, fmt.Errorf("memory: binding %s permissions: %w", b.ID, err)
	}
	var perr error
	b.CreatedAt, perr = parseStamp(created)
	if perr != nil {
		return nil, fmt.Errorf("memory: binding %s timestamp: %w", b.ID, perr)
	}
	b.UpdatedAt, perr = parseStamp(updated)
	if perr != nil {
		return nil, fmt.Errorf("memory: binding %s timestamp: %w", b.ID, perr)
	}
	return &b, nil
}

func insertBinding(ctx context.Context, unit contract.Unit, b *bindingRow) error {
	permissions, err := jsonColumn(b.Permissions)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO memory_bindings (`+bindingColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(b.ID), b.Version, string(b.InstallationID), string(b.OrganizationID), string(b.ProjectID),
		string(b.WorkerID), string(b.TaskID), string(b.BrainID), permissions, b.Classification, b.State,
		formatStamp(b.CreatedAt), formatStamp(b.UpdatedAt))
	return err
}

func loadBinding(ctx context.Context, unit contract.Unit, id contract.ID) (*bindingRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+bindingColumns+` FROM memory_bindings WHERE id = ?`, string(id))
	return scanBinding(row.Scan)
}

// updateBinding persists new scope/brain/permissions/classification for one
// binding under its version fence and bumps the version.
func updateBinding(ctx context.Context, unit contract.Unit, b *bindingRow, scope wireScope, brainID contract.ID, permissions []string, classification string, now time.Time) error {
	permJSON, err := jsonColumn(permissions)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `UPDATE memory_bindings SET version = version + 1,
		organization_id = ?, project_id = ?, worker_id = ?, task_id = ?, brain_id = ?,
		permissions_json = ?, classification = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		string(scope.OrganizationID), string(scope.ProjectID), string(scope.WorkerID), string(scope.TaskID),
		string(brainID), permJSON, classification, formatStamp(now), string(b.ID), b.Version)
	if err != nil {
		return err
	}
	if err := requireOneRow(res); err != nil {
		return err
	}
	b.Version++
	b.OrganizationID, b.ProjectID, b.WorkerID, b.TaskID = scope.OrganizationID, scope.ProjectID, scope.WorkerID, scope.TaskID
	b.BrainID, b.Permissions, b.Classification, b.UpdatedAt = brainID, permissions, classification, now
	return nil
}

// setBindingState transitions one binding's lifecycle state under its
// version fence and bumps the version.
func setBindingState(ctx context.Context, unit contract.Unit, b *bindingRow, state string, now time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE memory_bindings SET version = version + 1, state = ?, updated_at = ?
		WHERE id = ? AND version = ?`, state, formatStamp(now), string(b.ID), b.Version)
	if err != nil {
		return err
	}
	if err := requireOneRow(res); err != nil {
		return err
	}
	b.Version++
	b.State, b.UpdatedAt = state, now
	return nil
}

// requireOneRow fails a version-fenced update that touched zero rows: either
// the row vanished or another writer already moved its version.
func requireOneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return staleVersion("resource version no longer matches the expected version")
	}
	return nil
}

// Writer-intent lifecycle rows: one row per durable Serenity command, keyed
// by its pre-dispatch adapter_command_id so a lost acknowledgement can be
// reconciled instead of blindly repeated.
type intentRow struct {
	ID               contract.ID
	Kind             string
	InstallationID   contract.ID
	BrainID          contract.ID
	AdapterCommandID contract.ID
	OperationID      contract.ID
	JobID            contract.ID
	PayloadDigest    contract.Digest
	State            string
	Detail           json.RawMessage
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const intentColumns = `id, kind, installation_id, brain_id, adapter_command_id, operation_id, job_id,
	payload_digest, state, detail_json, created_at, updated_at`

func scanIntent(scan func(dest ...any) error) (*intentRow, error) {
	var i intentRow
	var operationID, jobID sql.NullString
	var detail sql.NullString
	var created, updated string
	err := scan(&i.ID, &i.Kind, &i.InstallationID, &i.BrainID, &i.AdapterCommandID, &operationID, &jobID,
		&i.PayloadDigest, &i.State, &detail, &created, &updated)
	if err != nil {
		return nil, err
	}
	if operationID.Valid {
		i.OperationID = contract.ID(operationID.String)
	}
	if jobID.Valid {
		i.JobID = contract.ID(jobID.String)
	}
	if detail.Valid {
		i.Detail = json.RawMessage(detail.String)
	}
	var perr error
	i.CreatedAt, perr = parseStamp(created)
	if perr != nil {
		return nil, fmt.Errorf("memory: intent %s timestamp: %w", i.ID, perr)
	}
	i.UpdatedAt, perr = parseStamp(updated)
	if perr != nil {
		return nil, fmt.Errorf("memory: intent %s timestamp: %w", i.ID, perr)
	}
	return &i, nil
}

func insertIntent(ctx context.Context, unit contract.Unit, i *intentRow) error {
	var operationID, jobID, detail sql.NullString
	if i.OperationID != "" {
		operationID = sql.NullString{String: string(i.OperationID), Valid: true}
	}
	if i.JobID != "" {
		jobID = sql.NullString{String: string(i.JobID), Valid: true}
	}
	if len(i.Detail) > 0 {
		detail = sql.NullString{String: string(i.Detail), Valid: true}
	}
	_, err := unit.ExecContext(ctx, `INSERT INTO memory_intents (`+intentColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(i.ID), i.Kind, string(i.InstallationID), string(i.BrainID), string(i.AdapterCommandID),
		operationID, jobID, string(i.PayloadDigest), i.State, detail, formatStamp(i.CreatedAt), formatStamp(i.UpdatedAt))
	return err
}

func loadIntentByOperation(ctx context.Context, unit contract.Unit, operationID contract.ID) (*intentRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+intentColumns+` FROM memory_intents WHERE operation_id = ?`, string(operationID))
	return scanIntent(row.Scan)
}

// loadIntentByJob resolves a writer intent by this owner's own durable job
// id (memory_jobs.id), the stable command identity `_memory.record`'s job_id
// names alongside the ephemeral effects operation_id. Unlike operation_id --
// which a bounded reconciliation read admits fresh, under its own distinct
// Operation, separate from the original write (R15-009) -- the job this
// owner opened at dispatch never changes across any number of redeliveries
// or reconciliation attempts, so it is the correct fallback correlation key
// when a callback names an operation_id this package no longer recognizes.
func loadIntentByJob(ctx context.Context, unit contract.Unit, jobID contract.ID) (*intentRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+intentColumns+` FROM memory_intents WHERE job_id = ?`, string(jobID))
	return scanIntent(row.Scan)
}

// loadIntentByExecutionJob resolves a writer intent by the linked
// `_execution.job.create` id (memory_jobs.execution_job_id), the second
// durable identity `_memory.record`'s job_id may legitimately name --
// handleRecord's own cross-check already accepts either this owner's job id
// or its linked execution job id, so the stable-identity fallback must
// recognize both.
func loadIntentByExecutionJob(ctx context.Context, unit contract.Unit, executionJobID contract.ID) (*intentRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT mi.id, mi.kind, mi.installation_id, mi.brain_id, mi.adapter_command_id,
		mi.operation_id, mi.job_id, mi.payload_digest, mi.state, mi.detail_json, mi.created_at, mi.updated_at
		FROM memory_intents mi JOIN memory_jobs mj ON mj.id = mi.job_id
		WHERE mj.execution_job_id = ?`, string(executionJobID))
	return scanIntent(row.Scan)
}

// listPendingIntents returns every writer intent not yet in a terminal
// state, for the backup manifest's key-recovery prerequisites (R15-009).
func listPendingIntents(ctx context.Context, unit contract.Unit, install contract.ID) ([]*intentRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+intentColumns+` FROM memory_intents
		WHERE installation_id = ? AND state NOT IN (?, ?) ORDER BY created_at, id`,
		string(install), intentCompleted, intentFailed)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*intentRow
	for rows.Next() {
		i, err := scanIntent(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func setIntentState(ctx context.Context, unit contract.Unit, i *intentRow, state string, now time.Time) error {
	_, err := unit.ExecContext(ctx, `UPDATE memory_intents SET state = ?, updated_at = ? WHERE id = ?`,
		state, formatStamp(now), string(i.ID))
	if err != nil {
		return err
	}
	i.State, i.UpdatedAt = state, now
	return nil
}

// jobRow is one memory_jobs record: the domain-facing async disposition of
// a recall, remember, promote or retract call.
type jobRow struct {
	ID                   contract.ID
	Version              int64
	InstallationID       contract.ID
	OrganizationID       contract.ID
	ProjectID            contract.ID
	WorkerID             contract.ID
	TaskID               contract.ID
	Kind                 string
	State                string
	Operation            string
	OperationIDs         []contract.ID
	BindingIDs           []contract.ID
	Requirements         []wireRequirement
	Result               json.RawMessage
	ResultArtifactID     contract.ID
	ResultArtifactDigest contract.Digest
	ExecutionJobID       contract.ID
	ExecutionJobVersion  int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

const jobColumns = `id, version, installation_id, organization_id, project_id, worker_id, task_id,
	kind, state, operation, operation_ids_json, binding_ids_json, requirements_json, result_json,
	result_artifact_id, result_artifact_digest, execution_job_id, execution_job_version, created_at, updated_at`

func scanJob(scan func(dest ...any) error) (*jobRow, error) {
	var j jobRow
	var operationIDs, bindingIDs, requirements string
	var result, artifactID, artifactDigest sql.NullString
	var created, updated string
	err := scan(&j.ID, &j.Version, &j.InstallationID, &j.OrganizationID, &j.ProjectID, &j.WorkerID, &j.TaskID,
		&j.Kind, &j.State, &j.Operation, &operationIDs, &bindingIDs, &requirements, &result,
		&artifactID, &artifactDigest, &j.ExecutionJobID, &j.ExecutionJobVersion, &created, &updated)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(operationIDs), &j.OperationIDs); err != nil {
		return nil, fmt.Errorf("memory: job %s operation ids: %w", j.ID, err)
	}
	if err := json.Unmarshal([]byte(bindingIDs), &j.BindingIDs); err != nil {
		return nil, fmt.Errorf("memory: job %s binding ids: %w", j.ID, err)
	}
	if err := json.Unmarshal([]byte(requirements), &j.Requirements); err != nil {
		return nil, fmt.Errorf("memory: job %s requirements: %w", j.ID, err)
	}
	if result.Valid {
		j.Result = json.RawMessage(result.String)
	}
	if artifactID.Valid {
		j.ResultArtifactID = contract.ID(artifactID.String)
	}
	if artifactDigest.Valid {
		j.ResultArtifactDigest = contract.Digest(artifactDigest.String)
	}
	var perr error
	j.CreatedAt, perr = parseStamp(created)
	if perr != nil {
		return nil, fmt.Errorf("memory: job %s timestamp: %w", j.ID, perr)
	}
	j.UpdatedAt, perr = parseStamp(updated)
	if perr != nil {
		return nil, fmt.Errorf("memory: job %s timestamp: %w", j.ID, perr)
	}
	return &j, nil
}

func insertJob(ctx context.Context, unit contract.Unit, j *jobRow) error {
	operationIDs, err := jsonColumn(j.OperationIDs)
	if err != nil {
		return err
	}
	bindingIDs, err := jsonColumn(j.BindingIDs)
	if err != nil {
		return err
	}
	requirements, err := jsonColumn(j.Requirements)
	if err != nil {
		return err
	}
	var result, artifactID, artifactDigest sql.NullString
	if len(j.Result) > 0 {
		result = sql.NullString{String: string(j.Result), Valid: true}
	}
	if j.ResultArtifactID != "" {
		artifactID = sql.NullString{String: string(j.ResultArtifactID), Valid: true}
	}
	if j.ResultArtifactDigest != "" {
		artifactDigest = sql.NullString{String: string(j.ResultArtifactDigest), Valid: true}
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO memory_jobs (`+jobColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(j.ID), j.Version, string(j.InstallationID), string(j.OrganizationID), string(j.ProjectID),
		string(j.WorkerID), string(j.TaskID), j.Kind, j.State, j.Operation, operationIDs, bindingIDs,
		requirements, result, artifactID, artifactDigest, string(j.ExecutionJobID), j.ExecutionJobVersion,
		formatStamp(j.CreatedAt), formatStamp(j.UpdatedAt))
	return err
}

func loadJob(ctx context.Context, unit contract.Unit, id contract.ID) (*jobRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM memory_jobs WHERE id = ?`, string(id))
	return scanJob(row.Scan)
}

// updateJob persists a new state, result body, result artifact and
// requirements list for one job, bumping its version.
func updateJob(ctx context.Context, unit contract.Unit, j *jobRow, state string, result json.RawMessage, artifact contract.ID, digest contract.Digest, requirements []wireRequirement, now time.Time) error {
	requirementsJSON, err := jsonColumn(requirements)
	if err != nil {
		return err
	}
	var resultCol, artifactCol, digestCol sql.NullString
	if len(result) > 0 {
		resultCol = sql.NullString{String: string(result), Valid: true}
	}
	if artifact != "" {
		artifactCol = sql.NullString{String: string(artifact), Valid: true}
	}
	if digest != "" {
		digestCol = sql.NullString{String: string(digest), Valid: true}
	}
	_, err = unit.ExecContext(ctx, `UPDATE memory_jobs SET version = version + 1, state = ?, result_json = ?,
		result_artifact_id = ?, result_artifact_digest = ?, requirements_json = ?, updated_at = ?
		WHERE id = ?`,
		state, resultCol, artifactCol, digestCol, requirementsJSON, formatStamp(now), string(j.ID))
	if err != nil {
		return err
	}
	j.Version++
	j.State, j.Result, j.ResultArtifactID, j.ResultArtifactDigest = state, result, artifact, digest
	j.Requirements, j.UpdatedAt = requirements, now
	return nil
}

// linkExecutionJob records the durable execution_jobs identity this owner's
// job is tracked under, once `_execution.job.create` returns it.
func linkExecutionJob(ctx context.Context, unit contract.Unit, j *jobRow, executionJobID contract.ID, executionJobVersion int64, now time.Time) error {
	_, err := unit.ExecContext(ctx, `UPDATE memory_jobs SET execution_job_id = ?, execution_job_version = ?, updated_at = ?
		WHERE id = ?`, string(executionJobID), executionJobVersion, formatStamp(now), string(j.ID))
	if err != nil {
		return err
	}
	j.ExecutionJobID, j.ExecutionJobVersion, j.UpdatedAt = executionJobID, executionJobVersion, now
	return nil
}

// claimRow is one observed version of one Serenity claim, cached locally.
// Rows are append only: a correction or retraction is a new version, never
// an edit of history.
type claimRow struct {
	BrainID            contract.ID
	ID                 contract.ID
	Version            int64
	Text               string
	Sources            []wireArtifactRef
	Confidence         int64
	Freshness          time.Time
	Active             bool
	SourceBrainID      contract.ID
	SourceClaimID      contract.ID
	SourceClaimVersion int64
	CuratorID          contract.ID
	Redaction          string
	RecordedAt         time.Time
}

func insertClaim(ctx context.Context, unit contract.Unit, c *claimRow) error {
	sources, err := jsonColumn(c.Sources)
	if err != nil {
		return err
	}
	var sourceBrain, sourceClaim, curator, redaction sql.NullString
	var sourceClaimVersion sql.NullInt64
	if c.SourceBrainID != "" {
		sourceBrain = sql.NullString{String: string(c.SourceBrainID), Valid: true}
	}
	if c.SourceClaimID != "" {
		sourceClaim = sql.NullString{String: string(c.SourceClaimID), Valid: true}
		sourceClaimVersion = sql.NullInt64{Int64: c.SourceClaimVersion, Valid: true}
	}
	if c.CuratorID != "" {
		curator = sql.NullString{String: string(c.CuratorID), Valid: true}
	}
	if c.Redaction != "" {
		redaction = sql.NullString{String: c.Redaction, Valid: true}
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO memory_claims
		(brain_id, id, version, text, sources_json, confidence, freshness, active,
		 source_brain_id, source_claim_id, source_claim_version, curator_id, redaction, recorded_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(c.BrainID), string(c.ID), c.Version, c.Text, sources, c.Confidence, formatStamp(c.Freshness),
		boolInt(c.Active), sourceBrain, sourceClaim, sourceClaimVersion, curator, redaction, formatStamp(c.RecordedAt))
	return err
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func scanClaim(scan func(dest ...any) error) (*claimRow, error) {
	var c claimRow
	var sources string
	var active int64
	var sourceBrain, sourceClaim, curator, redaction sql.NullString
	var sourceClaimVersion sql.NullInt64
	var freshness, recorded string
	err := scan(&c.BrainID, &c.ID, &c.Version, &c.Text, &sources, &c.Confidence, &freshness, &active,
		&sourceBrain, &sourceClaim, &sourceClaimVersion, &curator, &redaction, &recorded)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(sources), &c.Sources); err != nil {
		return nil, fmt.Errorf("memory: claim %s/%s sources: %w", c.BrainID, c.ID, err)
	}
	c.Active = active != 0
	if sourceBrain.Valid {
		c.SourceBrainID = contract.ID(sourceBrain.String)
	}
	if sourceClaim.Valid {
		c.SourceClaimID = contract.ID(sourceClaim.String)
	}
	if sourceClaimVersion.Valid {
		c.SourceClaimVersion = sourceClaimVersion.Int64
	}
	if curator.Valid {
		c.CuratorID = contract.ID(curator.String)
	}
	if redaction.Valid {
		c.Redaction = redaction.String
	}
	var perr error
	c.Freshness, perr = parseStamp(freshness)
	if perr != nil {
		return nil, fmt.Errorf("memory: claim %s/%s freshness: %w", c.BrainID, c.ID, perr)
	}
	c.RecordedAt, perr = parseStamp(recorded)
	if perr != nil {
		return nil, fmt.Errorf("memory: claim %s/%s recorded_at: %w", c.BrainID, c.ID, perr)
	}
	return &c, nil
}

const claimColumns = `brain_id, id, version, text, sources_json, confidence, freshness, active,
	source_brain_id, source_claim_id, source_claim_version, curator_id, redaction, recorded_at`

// loadLatestClaim returns the highest-version cached row for one claim, or
// sql.ErrNoRows if it has never been observed locally.
func loadLatestClaim(ctx context.Context, unit contract.Unit, brainID, claimID contract.ID) (*claimRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+claimColumns+` FROM memory_claims
		WHERE brain_id = ? AND id = ? ORDER BY version DESC LIMIT 1`, string(brainID), string(claimID))
	return scanClaim(row.Scan)
}

// listClaimsByBrains returns one keyset page of the latest cached version of
// every claim across the named brains -- memory.list's local read facade
// (R15-007/P00-017): never a Serenity call, so unauthorized brains are
// excluded entirely by the caller never naming them here, and retracted
// claims remain listed with active=false rather than disappearing (R15-006).
// Ordering and the (recorded_at, id) keyset mirror handleBindingList's
// pagination convention.
func listClaimsByBrains(ctx context.Context, unit contract.Unit, brainIDs []contract.ID, afterRecorded time.Time, afterID contract.ID, limit int64) ([]*claimRow, error) {
	if len(brainIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(brainIDs))
	args := make([]any, 0, len(brainIDs)+4)
	for i, id := range brainIDs {
		placeholders[i] = "?"
		args = append(args, string(id))
	}
	query := `SELECT ` + claimColumns + ` FROM memory_claims c
		WHERE c.brain_id IN (` + strings.Join(placeholders, ",") + `)
		AND c.version = (SELECT MAX(c2.version) FROM memory_claims c2 WHERE c2.brain_id = c.brain_id AND c2.id = c.id)
		AND (c.recorded_at > ? OR (c.recorded_at = ? AND c.id > ?))
		ORDER BY c.recorded_at, c.id LIMIT ?`
	args = append(args, formatStamp(afterRecorded), formatStamp(afterRecorded), string(afterID), limit)
	rows, err := unit.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*claimRow
	for rows.Next() {
		c, err := scanClaim(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// promotionRow is one memory_promotions record: a destination claim's
// durable lineage back to its source brain, claim, version and evidence.
type promotionRow struct {
	ID                      contract.ID
	IntentID                contract.ID
	JobID                   contract.ID
	SourceBrainID           contract.ID
	SourceClaimID           contract.ID
	SourceClaimVersion      int64
	DestinationBindingID    contract.ID
	DestinationBrainID      contract.ID
	DestinationClaimID      contract.ID
	DestinationClaimVersion int64
	CuratorID               contract.ID
	Redaction               string
	State                   string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

func insertPromotion(ctx context.Context, unit contract.Unit, p *promotionRow) error {
	var destClaim sql.NullString
	var destClaimVersion sql.NullInt64
	if p.DestinationClaimID != "" {
		destClaim = sql.NullString{String: string(p.DestinationClaimID), Valid: true}
		destClaimVersion = sql.NullInt64{Int64: p.DestinationClaimVersion, Valid: true}
	}
	var redaction sql.NullString
	if p.Redaction != "" {
		redaction = sql.NullString{String: p.Redaction, Valid: true}
	}
	_, err := unit.ExecContext(ctx, `INSERT INTO memory_promotions
		(id, intent_id, job_id, source_brain_id, source_claim_id, source_claim_version,
		 destination_binding_id, destination_brain_id, destination_claim_id, destination_claim_version,
		 curator_id, redaction, state, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(p.ID), string(p.IntentID), string(p.JobID), string(p.SourceBrainID), string(p.SourceClaimID),
		p.SourceClaimVersion, string(p.DestinationBindingID), string(p.DestinationBrainID), destClaim,
		destClaimVersion, string(p.CuratorID), redaction, p.State, formatStamp(p.CreatedAt), formatStamp(p.UpdatedAt))
	return err
}

const promotionColumns = `id, intent_id, job_id, source_brain_id, source_claim_id, source_claim_version,
	destination_binding_id, destination_brain_id, destination_claim_id, destination_claim_version,
	curator_id, redaction, state, created_at, updated_at`

func scanPromotion(scan func(dest ...any) error) (*promotionRow, error) {
	var p promotionRow
	var destClaim sql.NullString
	var destClaimVersion sql.NullInt64
	var redaction sql.NullString
	var created, updated string
	err := scan(&p.ID, &p.IntentID, &p.JobID, &p.SourceBrainID, &p.SourceClaimID, &p.SourceClaimVersion,
		&p.DestinationBindingID, &p.DestinationBrainID, &destClaim, &destClaimVersion,
		&p.CuratorID, &redaction, &p.State, &created, &updated)
	if err != nil {
		return nil, err
	}
	if destClaim.Valid {
		p.DestinationClaimID = contract.ID(destClaim.String)
	}
	if destClaimVersion.Valid {
		p.DestinationClaimVersion = destClaimVersion.Int64
	}
	if redaction.Valid {
		p.Redaction = redaction.String
	}
	var perr error
	p.CreatedAt, perr = parseStamp(created)
	if perr != nil {
		return nil, fmt.Errorf("memory: promotion %s timestamp: %w", p.ID, perr)
	}
	p.UpdatedAt, perr = parseStamp(updated)
	if perr != nil {
		return nil, fmt.Errorf("memory: promotion %s timestamp: %w", p.ID, perr)
	}
	return &p, nil
}

// loadPromotionByIntent returns the promotion lineage row opened by one
// promote intent, keyed by the intent_id unique constraint.
func loadPromotionByIntent(ctx context.Context, unit contract.Unit, intentID contract.ID) (*promotionRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+promotionColumns+` FROM memory_promotions WHERE intent_id = ?`, string(intentID))
	return scanPromotion(row.Scan)
}

// finalizePromotion records the destination claim Serenity confirmed and
// marks the promotion lineage complete.
func finalizePromotion(ctx context.Context, unit contract.Unit, p *promotionRow, claimID contract.ID, claimVersion int64, now time.Time) error {
	_, err := unit.ExecContext(ctx, `UPDATE memory_promotions SET destination_claim_id = ?, destination_claim_version = ?,
		state = ?, updated_at = ? WHERE id = ?`,
		string(claimID), claimVersion, "completed", formatStamp(now), string(p.ID))
	if err != nil {
		return err
	}
	p.DestinationClaimID, p.DestinationClaimVersion, p.State, p.UpdatedAt = claimID, claimVersion, "completed", now
	return nil
}

// listPromotionsBySource returns every promotion lineage row copied from one
// source claim id, regardless of the version copied, so a retraction can
// open reconciliation against every downstream copy.
func listPromotionsBySource(ctx context.Context, unit contract.Unit, sourceBrainID, sourceClaimID contract.ID) ([]*promotionRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+promotionColumns+` FROM memory_promotions
		WHERE source_brain_id = ? AND source_claim_id = ?`, string(sourceBrainID), string(sourceClaimID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*promotionRow
	for rows.Next() {
		p, err := scanPromotion(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// reconciliationRow is one open or resolved durable obligation: a lost
// writer acknowledgement or a downstream promotion correction.
type reconciliationRow struct {
	ID                 contract.ID
	Kind               string
	InstallationID     contract.ID
	PromotionID        contract.ID
	IntentID           contract.ID
	SourceBrainID      contract.ID
	SourceClaimID      contract.ID
	SourceClaimVersion int64
	DestinationBrainID contract.ID
	Reason             string
	State              string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

const reconciliationOpen = "open"

func insertReconciliation(ctx context.Context, unit contract.Unit, r *reconciliationRow) error {
	nullable := func(id contract.ID) sql.NullString {
		if id == "" {
			return sql.NullString{}
		}
		return sql.NullString{String: string(id), Valid: true}
	}
	var sourceClaimVersion sql.NullInt64
	if r.SourceClaimID != "" {
		sourceClaimVersion = sql.NullInt64{Int64: r.SourceClaimVersion, Valid: true}
	}
	_, err := unit.ExecContext(ctx, `INSERT INTO memory_reconciliation
		(id, kind, installation_id, promotion_id, intent_id, source_brain_id, source_claim_id,
		 source_claim_version, destination_brain_id, reason, state, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(r.ID), r.Kind, string(r.InstallationID), nullable(r.PromotionID), nullable(r.IntentID),
		nullable(r.SourceBrainID), nullable(r.SourceClaimID), sourceClaimVersion, nullable(r.DestinationBrainID),
		r.Reason, r.State, formatStamp(r.CreatedAt), formatStamp(r.UpdatedAt))
	return err
}

const reconciliationColumns = `id, kind, installation_id, promotion_id, intent_id, source_brain_id, source_claim_id,
	source_claim_version, destination_brain_id, reason, state, created_at, updated_at`

func scanReconciliation(scan func(dest ...any) error) (*reconciliationRow, error) {
	var r reconciliationRow
	var promotion, intent, sourceBrain, sourceClaim, destBrain sql.NullString
	var sourceClaimVersion sql.NullInt64
	var created, updated string
	err := scan(&r.ID, &r.Kind, &r.InstallationID, &promotion, &intent, &sourceBrain, &sourceClaim,
		&sourceClaimVersion, &destBrain, &r.Reason, &r.State, &created, &updated)
	if err != nil {
		return nil, err
	}
	if promotion.Valid {
		r.PromotionID = contract.ID(promotion.String)
	}
	if intent.Valid {
		r.IntentID = contract.ID(intent.String)
	}
	if sourceBrain.Valid {
		r.SourceBrainID = contract.ID(sourceBrain.String)
	}
	if sourceClaim.Valid {
		r.SourceClaimID = contract.ID(sourceClaim.String)
	}
	if sourceClaimVersion.Valid {
		r.SourceClaimVersion = sourceClaimVersion.Int64
	}
	if destBrain.Valid {
		r.DestinationBrainID = contract.ID(destBrain.String)
	}
	var perr error
	r.CreatedAt, perr = parseStamp(created)
	if perr != nil {
		return nil, fmt.Errorf("memory: reconciliation %s timestamp: %w", r.ID, perr)
	}
	r.UpdatedAt, perr = parseStamp(updated)
	if perr != nil {
		return nil, fmt.Errorf("memory: reconciliation %s timestamp: %w", r.ID, perr)
	}
	return &r, nil
}

// listOpenReconciliation returns every unresolved obligation for one
// installation, for the backup manifest and for duplicate-obligation checks.
func listOpenReconciliation(ctx context.Context, unit contract.Unit, install contract.ID) ([]*reconciliationRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+reconciliationColumns+` FROM memory_reconciliation
		WHERE installation_id = ? AND state = ? ORDER BY created_at, id`, string(install), reconciliationOpen)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*reconciliationRow
	for rows.Next() {
		r, err := scanReconciliation(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// hasOpenObligationsForBrain reports whether one brain still has an
// in-flight writer intent or an unresolved reconciliation obligation naming
// it as a destination, blocking its bindings from archiving without
// omission (no silent deletion of retained work or obligations).
func hasOpenObligationsForBrain(ctx context.Context, unit contract.Unit, brainID contract.ID) (bool, error) {
	var one int
	row := unit.QueryRowContext(ctx, `SELECT 1 FROM memory_intents WHERE brain_id = ? AND state NOT IN (?, ?) LIMIT 1`,
		string(brainID), intentCompleted, intentFailed)
	err := row.Scan(&one)
	if err == nil {
		return true, nil
	}
	if !isNoRows(err) {
		return false, err
	}
	row = unit.QueryRowContext(ctx, `SELECT 1 FROM memory_reconciliation WHERE destination_brain_id = ? AND state = ? LIMIT 1`,
		string(brainID), reconciliationOpen)
	err = row.Scan(&one)
	if err == nil {
		return true, nil
	}
	if !isNoRows(err) {
		return false, err
	}
	return false, nil
}

// openReconciliationExists reports whether an unresolved obligation of one
// kind already covers one promotion, so retraction propagation stays
// idempotent under replay.
func openReconciliationExists(ctx context.Context, unit contract.Unit, install contract.ID, kind string, promotionID contract.ID) (bool, error) {
	row := unit.QueryRowContext(ctx, `SELECT 1 FROM memory_reconciliation
		WHERE installation_id = ? AND kind = ? AND promotion_id = ? AND state = ? LIMIT 1`,
		string(install), kind, string(promotionID), reconciliationOpen)
	var one int
	err := row.Scan(&one)
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
