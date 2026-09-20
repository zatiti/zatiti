package execution

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
// canonical scope JSON for wire output. Timestamps persist as UTC RFC3339Nano
// strings so lexicographic comparison matches chronological order, which the
// keyset pagination and lease-expiry scans rely on. Updates are version
// fenced: a statement that matches no row means another writer moved the
// resource first and the caller sees stale_version.

// runRow is the storage representation of one run.
type runRow struct {
	ID                    contract.ID
	Version               contract.Version
	TaskID                contract.ID
	TaskVersion           contract.Version
	InstallationID        contract.ID
	OrganizationID        contract.ID
	ProjectID             contract.ID
	WorkerID              contract.ID
	TaskScopeID           contract.ID
	Scope                 contract.Scope
	ConfigurationRevision contract.Version
	InputVersions         []wireRef
	State                 string
	AttemptIDs            []contract.ID
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// attemptRow is the storage representation of one attempt.
type attemptRow struct {
	ID                 contract.ID
	Version            contract.Version
	RunID              contract.ID
	TaskID             contract.ID
	WorkerID           contract.ID
	Executor           string
	InstallationID     contract.ID
	OrganizationID     contract.ID
	ProjectID          contract.ID
	TaskScopeID        contract.ID
	WorkerScopeID      contract.ID
	Scope              contract.Scope
	Generation         int64
	LeaseID            contract.ID
	LeaseExpiresAt     time.Time
	LastHeartbeat      time.Time
	ReservationID      contract.ID
	ReservationVersion contract.Version
	State              string
	Capabilities       []string
	ContextArtifact    *wireArtifactRef
	RecoveryReason     string
	ModelStepsUsed     int64
	Outputs            []wireArtifactRef
	Observations       []wireObservation
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// liveAttemptStates are the states in which an attempt owns its run.
var liveAttemptStates = []string{"claimed", "running", "waiting"}

// liveStateSQL renders the live-state IN clause for the one-owner fence.
func liveStateSQL() string {
	return "('" + liveAttemptStates[0] + "','" + liveAttemptStates[1] + "','" + liveAttemptStates[2] + "')"
}

// jobRow is the storage representation of one durable job.
type jobRow struct {
	ID                contract.ID
	Version           contract.Version
	Kind              string
	State             string
	InstallationID    contract.ID
	OrganizationID    contract.ID
	ProjectID         contract.ID
	Scope             contract.Scope
	Owner             string
	Operation         string
	Input             json.RawMessage
	InputHash         string
	SourceID          contract.ID
	ClaimedGeneration int64
	Requirements      []wireRequirement
	Result            json.RawMessage
	ResultArtifact    *wireArtifactRef
	OperationID       contract.ID
	CompletionSchema  string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// gateRow is the storage representation of one worker pause gate.
type gateRow struct {
	WorkerID       contract.ID
	InstallationID contract.ID
	Paused         bool
	Version        contract.Version
	PausedBy       string
	UpdatedAt      time.Time
}

// leaseRow is the storage representation of one lease.
type leaseRow struct {
	ID             contract.ID
	AttemptID      contract.ID
	RunID          contract.ID
	WorkerID       contract.ID
	InstallationID contract.ID
	Generation     int64
	State          string
	ExpiresAt      time.Time
	LastHeartbeat  time.Time
	CreatedAt      time.Time
}

// verificationJobRow is the storage representation of one verification job.
type verificationJobRow struct {
	ID               contract.ID
	Version          contract.Version
	AttemptID        contract.ID
	RunID            contract.ID
	TaskID           contract.ID
	InstallationID   contract.ID
	State            string
	Request          json.RawMessage
	Result           json.RawMessage
	AcceptanceDigest contract.Digest
}

// isNoRows reports whether the error is an empty single-row scan.
func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// formatStamp renders a UTC timestamp at fixed nanosecond width so stored
// stamps order lexicographically under the keyset cursors; the zero time
// renders as the empty string so optional stamp columns stay distinct from
// the epoch.
func formatStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

// parseStamp parses a stored UTC timestamp (fixed-width on write, RFC3339
// family accepted on read).
func parseStamp(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("execution: parse stamp: %w", err)
	}
	return t, nil
}

// encodeJSON marshals a value for a JSON column.
func encodeJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("execution: encode json: %w", err)
	}
	return string(raw), nil
}

// decodeJSON parses a stored JSON column into out; an empty column leaves
// out untouched.
func decodeJSON(raw string, out any) error {
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("execution: decode json: %w", err)
	}
	return nil
}

// encodeScope renders the canonical scope snapshot stored on every row.
func encodeScope(scope contract.Scope) (string, error) { return encodeJSON(scope) }

// expectOneRow enforces optimistic fencing: a version-fenced update that
// matches no row lost the race to a concurrent writer.
func expectOneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("execution: confirm row: %w", err)
	}
	if n != 1 {
		return staleVersion("the resource changed concurrently; retry with the current version")
	}
	return nil
}

// boolInt encodes a bool for an INTEGER column.
func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// joinConds joins WHERE fragments with AND.
func joinConds(conds []string) string {
	out := conds[0]
	for _, c := range conds[1:] {
		out += " AND " + c
	}
	return out
}

const runColumns = `id, version, task_id, task_version, installation_id,
organization_id, project_id, worker_id, task_scope_id, scope_json,
configuration_revision, input_versions_json, state, attempt_ids_json,
created_at, updated_at`

func scanRun(scan func(dest ...any) error) (*runRow, error) {
	var r runRow
	var scopeJSON, inputsJSON, attemptsJSON string
	var created, updated string
	err := scan(&r.ID, &r.Version, &r.TaskID, &r.TaskVersion,
		&r.InstallationID, &r.OrganizationID, &r.ProjectID, &r.WorkerID, &r.TaskScopeID,
		&scopeJSON, &r.ConfigurationRevision, &inputsJSON, &r.State, &attemptsJSON,
		&created, &updated)
	if err != nil {
		return nil, err
	}
	if err := decodeJSON(scopeJSON, &r.Scope); err != nil {
		return nil, err
	}
	if err := decodeJSON(inputsJSON, &r.InputVersions); err != nil {
		return nil, err
	}
	if err := decodeJSON(attemptsJSON, &r.AttemptIDs); err != nil {
		return nil, err
	}
	if r.InputVersions == nil {
		r.InputVersions = []wireRef{}
	}
	if r.AttemptIDs == nil {
		r.AttemptIDs = []contract.ID{}
	}
	if r.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = parseStamp(updated); err != nil {
		return nil, err
	}
	return &r, nil
}

// loadRun reads one run by id.
func loadRun(ctx context.Context, unit contract.Unit, id contract.ID) (*runRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+runColumns+` FROM execution_runs WHERE id = ?`, id)
	r, err := scanRun(row.Scan)
	if isNoRows(err) {
		return nil, notFound("run %s does not exist", id)
	}
	return r, err
}

// loadRunForUpdate reads one run and enforces the version fence.
func loadRunForUpdate(ctx context.Context, unit contract.Unit, id contract.ID, expected contract.Version) (*runRow, error) {
	r, err := loadRun(ctx, unit, id)
	if err != nil {
		return nil, err
	}
	if r.Version != expected {
		return nil, staleVersion("run %s version %d does not match expected version %d", id, r.Version, expected)
	}
	return r, nil
}

// insertRun persists a new run at version 1.
func insertRun(ctx context.Context, unit contract.Unit, r *runRow) error {
	scopeJSON, err := encodeScope(r.Scope)
	if err != nil {
		return err
	}
	inputsJSON, err := encodeJSON(r.InputVersions)
	if err != nil {
		return err
	}
	attemptsJSON, err := encodeJSON(r.AttemptIDs)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO execution_runs
		(id, version, task_id, task_version, installation_id, organization_id,
		 project_id, worker_id, task_scope_id, scope_json, configuration_revision,
		 input_versions_json, state, attempt_ids_json, created_at, updated_at)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, r.TaskVersion, r.InstallationID, r.OrganizationID,
		r.ProjectID, r.WorkerID, r.TaskScopeID, scopeJSON, r.ConfigurationRevision,
		inputsJSON, r.State, attemptsJSON, formatStamp(r.CreatedAt), formatStamp(r.UpdatedAt))
	return err
}

// updateRun applies a version-fenced mutation to one run; the caller mutates
// the row struct first, and the stored version bumps on success.
func updateRun(ctx context.Context, unit contract.Unit, r *runRow) error {
	scopeJSON, err := encodeScope(r.Scope)
	if err != nil {
		return err
	}
	inputsJSON, err := encodeJSON(r.InputVersions)
	if err != nil {
		return err
	}
	attemptsJSON, err := encodeJSON(r.AttemptIDs)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `UPDATE execution_runs SET
		version = ?, worker_id = ?, task_scope_id = ?, scope_json = ?,
		configuration_revision = ?, input_versions_json = ?, state = ?,
		attempt_ids_json = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		r.Version+1, r.WorkerID, r.TaskScopeID, scopeJSON, r.ConfigurationRevision,
		inputsJSON, r.State, attemptsJSON, formatStamp(r.UpdatedAt), r.ID, r.Version)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	r.Version++
	return nil
}

const attemptColumns = `id, version, run_id, task_id, worker_id, executor,
installation_id, organization_id, project_id, task_scope_id, worker_scope_id,
scope_json, generation, lease_id, lease_expires_at, last_heartbeat,
reservation_id, reservation_version, state, capabilities_json,
context_artifact_json, recovery_reason, model_steps_used, outputs_json,
observations_json, created_at, updated_at`

func scanAttempt(scan func(dest ...any) error) (*attemptRow, error) {
	var a attemptRow
	var scopeJSON, capsJSON, contextJSON, outputsJSON, obsJSON string
	var leaseExpires, lastHeartbeat, created, updated string
	err := scan(&a.ID, &a.Version, &a.RunID, &a.TaskID, &a.WorkerID, &a.Executor,
		&a.InstallationID, &a.OrganizationID, &a.ProjectID, &a.TaskScopeID, &a.WorkerScopeID,
		&scopeJSON, &a.Generation, &a.LeaseID, &leaseExpires, &lastHeartbeat,
		&a.ReservationID, &a.ReservationVersion, &a.State, &capsJSON, &contextJSON,
		&a.RecoveryReason, &a.ModelStepsUsed, &outputsJSON, &obsJSON, &created, &updated)
	if err != nil {
		return nil, err
	}
	if err := decodeJSON(scopeJSON, &a.Scope); err != nil {
		return nil, err
	}
	if err := decodeJSON(capsJSON, &a.Capabilities); err != nil {
		return nil, err
	}
	if err := decodeJSON(contextJSON, &a.ContextArtifact); err != nil {
		return nil, err
	}
	if err := decodeJSON(outputsJSON, &a.Outputs); err != nil {
		return nil, err
	}
	if err := decodeJSON(obsJSON, &a.Observations); err != nil {
		return nil, err
	}
	if a.Capabilities == nil {
		a.Capabilities = []string{}
	}
	if a.Outputs == nil {
		a.Outputs = []wireArtifactRef{}
	}
	if a.Observations == nil {
		a.Observations = []wireObservation{}
	}
	if a.LeaseExpiresAt, err = parseStamp(leaseExpires); err != nil {
		return nil, err
	}
	if a.LastHeartbeat, err = parseStamp(lastHeartbeat); err != nil {
		return nil, err
	}
	if a.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	if a.UpdatedAt, err = parseStamp(updated); err != nil {
		return nil, err
	}
	return &a, nil
}

// loadAttempt reads one attempt by id.
func loadAttempt(ctx context.Context, unit contract.Unit, id contract.ID) (*attemptRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+attemptColumns+` FROM execution_attempts WHERE id = ?`, id)
	a, err := scanAttempt(row.Scan)
	if isNoRows(err) {
		return nil, notFound("attempt %s does not exist", id)
	}
	return a, err
}

// loadAttemptForUpdate reads one attempt and enforces the version fence.
func loadAttemptForUpdate(ctx context.Context, unit contract.Unit, id contract.ID, expected contract.Version) (*attemptRow, error) {
	a, err := loadAttempt(ctx, unit, id)
	if err != nil {
		return nil, err
	}
	if a.Version != expected {
		return nil, staleVersion("attempt %s version %d does not match expected version %d", id, a.Version, expected)
	}
	return a, nil
}

// insertAttempt persists a new attempt at version 1. The partial unique
// index on live attempts is the storage-level one-owner fence; callers
// pre-check liveAttemptExists inside the same transaction, so a violation
// here is a defensive backstop.
func insertAttempt(ctx context.Context, unit contract.Unit, a *attemptRow) error {
	scopeJSON, err := encodeScope(a.Scope)
	if err != nil {
		return err
	}
	capsJSON, err := encodeJSON(a.Capabilities)
	if err != nil {
		return err
	}
	contextJSON, err := encodeJSON(a.ContextArtifact)
	if err != nil {
		return err
	}
	outputsJSON, err := encodeJSON(a.Outputs)
	if err != nil {
		return err
	}
	obsJSON, err := encodeJSON(a.Observations)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO execution_attempts
		(id, version, run_id, task_id, worker_id, executor, installation_id,
		 organization_id, project_id, task_scope_id, worker_scope_id, scope_json,
		 generation, lease_id, lease_expires_at, last_heartbeat, reservation_id,
		 reservation_version, state, capabilities_json, context_artifact_json,
		 recovery_reason, model_steps_used, outputs_json, observations_json,
		 created_at, updated_at)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.RunID, a.TaskID, a.WorkerID, a.Executor, a.InstallationID,
		a.OrganizationID, a.ProjectID, a.TaskScopeID, a.WorkerScopeID, scopeJSON,
		a.Generation, a.LeaseID, formatStamp(a.LeaseExpiresAt), formatStamp(a.LastHeartbeat),
		a.ReservationID, a.ReservationVersion, a.State, capsJSON, contextJSON,
		a.RecoveryReason, a.ModelStepsUsed, outputsJSON, obsJSON,
		formatStamp(a.CreatedAt), formatStamp(a.UpdatedAt))
	return err
}

// updateAttempt applies a version-fenced mutation to one attempt.
func updateAttempt(ctx context.Context, unit contract.Unit, a *attemptRow) error {
	capsJSON, err := encodeJSON(a.Capabilities)
	if err != nil {
		return err
	}
	contextJSON, err := encodeJSON(a.ContextArtifact)
	if err != nil {
		return err
	}
	outputsJSON, err := encodeJSON(a.Outputs)
	if err != nil {
		return err
	}
	obsJSON, err := encodeJSON(a.Observations)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `UPDATE execution_attempts SET
		version = ?, lease_id = ?, lease_expires_at = ?, last_heartbeat = ?,
		reservation_id = ?, reservation_version = ?, state = ?, capabilities_json = ?,
		context_artifact_json = ?, recovery_reason = ?, model_steps_used = ?,
		outputs_json = ?, observations_json = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		a.Version+1, a.LeaseID, formatStamp(a.LeaseExpiresAt), formatStamp(a.LastHeartbeat),
		a.ReservationID, a.ReservationVersion, a.State, capsJSON, contextJSON,
		a.RecoveryReason, a.ModelStepsUsed, outputsJSON, obsJSON, formatStamp(a.UpdatedAt),
		a.ID, a.Version)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	a.Version++
	return nil
}

// liveAttemptExists reports whether any live attempt currently owns the run.
func liveAttemptExists(ctx context.Context, unit contract.Unit, runID contract.ID) (bool, error) {
	row := unit.QueryRowContext(ctx, `SELECT COUNT(1) FROM execution_attempts
		WHERE run_id = ? AND state IN `+liveStateSQL(), runID)
	var n int64
	if err := row.Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// countLiveAttempts counts live attempts within one scope bucket; column is
// a fixed allowlist, never caller input.
func countLiveAttempts(ctx context.Context, unit contract.Unit, column, id string) (int64, error) {
	if column != "worker_id" && column != "installation_id" {
		return 0, fmt.Errorf("execution: invalid count column %q", column)
	}
	row := unit.QueryRowContext(ctx, `SELECT COUNT(1) FROM execution_attempts
		WHERE `+column+` = ? AND state IN `+liveStateSQL(), id)
	var n int64
	err := row.Scan(&n)
	return n, err
}

// listAttempts reads attempts matching the conditions, newest first.
func listAttempts(ctx context.Context, unit contract.Unit, conds []string, args []any, limit int64) ([]*attemptRow, error) {
	query := `SELECT ` + attemptColumns + ` FROM execution_attempts`
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
	var out []*attemptRow
	for rows.Next() {
		a, err := scanAttempt(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// listLeaseExpiredAttempts reads the installation's live attempts whose
// lease has expired.
func listLeaseExpiredAttempts(ctx context.Context, unit contract.Unit, installation contract.ID, now time.Time, limit int64) ([]*attemptRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+attemptColumns+` FROM execution_attempts
		WHERE installation_id = ? AND state IN `+liveStateSQL()+` AND lease_expires_at <= ?
		ORDER BY lease_expires_at LIMIT ?`, installation, formatStamp(now), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*attemptRow
	for rows.Next() {
		a, err := scanAttempt(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const jobColumns = `id, version, kind, state, installation_id, organization_id,
project_id, scope_json, owner, operation, input_json, input_hash, source_id,
claimed_generation, requirements_json, result_json, result_artifact_json,
operation_id, completion_schema, created_at, updated_at`

func scanJob(scan func(dest ...any) error) (*jobRow, error) {
	var j jobRow
	var scopeJSON, inputJSON, requirementsJSON, resultJSON, artifactJSON string
	var created, updated string
	err := scan(&j.ID, &j.Version, &j.Kind, &j.State, &j.InstallationID,
		&j.OrganizationID, &j.ProjectID, &scopeJSON, &j.Owner, &j.Operation,
		&inputJSON, &j.InputHash, &j.SourceID, &j.ClaimedGeneration,
		&requirementsJSON, &resultJSON, &artifactJSON, &j.OperationID,
		&j.CompletionSchema, &created, &updated)
	if err != nil {
		return nil, err
	}
	if err := decodeJSON(scopeJSON, &j.Scope); err != nil {
		return nil, err
	}
	if inputJSON != "" {
		j.Input = json.RawMessage(inputJSON)
	}
	if err := decodeJSON(requirementsJSON, &j.Requirements); err != nil {
		return nil, err
	}
	if j.Requirements == nil {
		j.Requirements = []wireRequirement{}
	}
	if len(resultJSON) > 0 {
		j.Result = json.RawMessage(resultJSON)
	}
	if err := decodeJSON(artifactJSON, &j.ResultArtifact); err != nil {
		return nil, err
	}
	if j.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	if j.UpdatedAt, err = parseStamp(updated); err != nil {
		return nil, err
	}
	return &j, nil
}

// loadJob reads one durable job by id.
func loadJob(ctx context.Context, unit contract.Unit, id contract.ID) (*jobRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM execution_jobs WHERE id = ?`, id)
	j, err := scanJob(row.Scan)
	if isNoRows(err) {
		return nil, notFound("job %s does not exist", id)
	}
	return j, err
}

// loadJobForUpdate reads one durable job and enforces the version fence.
func loadJobForUpdate(ctx context.Context, unit contract.Unit, id contract.ID, expected contract.Version) (*jobRow, error) {
	j, err := loadJob(ctx, unit, id)
	if err != nil {
		return nil, err
	}
	if j.Version != expected {
		return nil, staleVersion("job %s version %d does not match expected version %d", id, j.Version, expected)
	}
	return j, nil
}

// listJobs reads jobs matching the conditions, newest first.
func listJobs(ctx context.Context, unit contract.Unit, conds []string, args []any, limit int64) ([]*jobRow, error) {
	query := `SELECT ` + jobColumns + ` FROM execution_jobs`
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
	var out []*jobRow
	for rows.Next() {
		j, err := scanJob(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// insertJob persists a new durable job at version 1.
func insertJob(ctx context.Context, unit contract.Unit, j *jobRow) error {
	scopeJSON, err := encodeScope(j.Scope)
	if err != nil {
		return err
	}
	requirementsJSON, err := encodeJSON(j.Requirements)
	if err != nil {
		return err
	}
	artifactJSON, err := encodeJSON(j.ResultArtifact)
	if err != nil {
		return err
	}
	input := string(j.Input)
	if input == "" {
		input = "null"
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO execution_jobs
		(id, version, kind, state, installation_id, organization_id, project_id,
		 scope_json, owner, operation, input_json, input_hash, source_id,
		 claimed_generation, requirements_json, result_json, result_artifact_json,
		 operation_id, completion_schema, created_at, updated_at)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Kind, j.State, j.InstallationID, j.OrganizationID, j.ProjectID,
		scopeJSON, j.Owner, j.Operation, input, j.InputHash, j.SourceID,
		j.ClaimedGeneration, requirementsJSON, string(j.Result), artifactJSON,
		j.OperationID, j.CompletionSchema, formatStamp(j.CreatedAt), formatStamp(j.UpdatedAt))
	return err
}

// updateJob applies a version-fenced mutation to one durable job.
func updateJob(ctx context.Context, unit contract.Unit, j *jobRow) error {
	requirementsJSON, err := encodeJSON(j.Requirements)
	if err != nil {
		return err
	}
	artifactJSON, err := encodeJSON(j.ResultArtifact)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `UPDATE execution_jobs SET
		version = ?, state = ?, claimed_generation = ?, requirements_json = ?,
		result_json = ?, result_artifact_json = ?, operation_id = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		j.Version+1, j.State, j.ClaimedGeneration, requirementsJSON,
		string(j.Result), artifactJSON, j.OperationID, formatStamp(j.UpdatedAt),
		j.ID, j.Version)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	j.Version++
	return nil
}

// loadLease reads one lease by id.
func loadLease(ctx context.Context, unit contract.Unit, id contract.ID) (*leaseRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT id, attempt_id, run_id, worker_id,
		installation_id, generation, state, expires_at, last_heartbeat, created_at
		FROM execution_leases WHERE id = ?`, id)
	var l leaseRow
	var expires, heartbeat, created string
	err := row.Scan(&l.ID, &l.AttemptID, &l.RunID, &l.WorkerID, &l.InstallationID,
		&l.Generation, &l.State, &expires, &heartbeat, &created)
	if isNoRows(err) {
		return nil, notFound("lease %s does not exist", id)
	}
	if err != nil {
		return nil, err
	}
	if l.ExpiresAt, err = parseStamp(expires); err != nil {
		return nil, err
	}
	if l.LastHeartbeat, err = parseStamp(heartbeat); err != nil {
		return nil, err
	}
	if l.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	return &l, nil
}

// insertLease persists a new active lease.
func insertLease(ctx context.Context, unit contract.Unit, l *leaseRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO execution_leases
		(id, attempt_id, run_id, worker_id, installation_id, generation, state,
		 expires_at, last_heartbeat, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)`,
		l.ID, l.AttemptID, l.RunID, l.WorkerID, l.InstallationID, l.Generation,
		formatStamp(l.ExpiresAt), formatStamp(l.LastHeartbeat), formatStamp(l.CreatedAt))
	return err
}

// retireLease moves one lease to its terminal state.
func retireLease(ctx context.Context, unit contract.Unit, id contract.ID, state string) error {
	res, err := unit.ExecContext(ctx, `UPDATE execution_leases
		SET state = ? WHERE id = ?`, state, id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

// loadGate reads the pause gate of one worker; a missing gate reads as an
// unpaused worker at version 1.
func loadGate(ctx context.Context, unit contract.Unit, workerID contract.ID) (*gateRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT worker_id, installation_id, paused,
		version, paused_by, updated_at FROM execution_worker_gates WHERE worker_id = ?`, workerID)
	var g gateRow
	var paused int64
	var updated string
	err := row.Scan(&g.WorkerID, &g.InstallationID, &paused, &g.Version, &g.PausedBy, &updated)
	if isNoRows(err) {
		return &gateRow{WorkerID: workerID, Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	g.Paused = paused == 1
	if g.UpdatedAt, err = parseStamp(updated); err != nil {
		return nil, err
	}
	return &g, nil
}

// setGate writes the pause gate with optimistic fencing. The upsert guard on
// the current version makes a concurrent pause or resume lose cleanly.
func setGate(ctx context.Context, unit contract.Unit, g *gateRow) error {
	res, err := unit.ExecContext(ctx, `INSERT INTO execution_worker_gates
		(worker_id, installation_id, paused, version, paused_by, updated_at)
		VALUES (?, ?, ?, 2, ?, ?)
		ON CONFLICT(worker_id) DO UPDATE SET
		  paused = excluded.paused, version = version + 1,
		  paused_by = excluded.paused_by, updated_at = excluded.updated_at
		WHERE execution_worker_gates.version = ?`,
		g.WorkerID, g.InstallationID, boolInt(g.Paused), g.PausedBy,
		formatStamp(g.UpdatedAt), g.Version)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	g.Version++
	return nil
}

// insertCheckpoint persists one checkpoint record.
func insertCheckpoint(ctx context.Context, unit contract.Unit, id contract.ID, a *attemptRow, contextRef wireArtifactRef, outputs []wireArtifactRef, at time.Time) error {
	contextJSON, err := encodeJSON(contextRef)
	if err != nil {
		return err
	}
	outputsJSON, err := encodeJSON(outputs)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO execution_checkpoints
		(id, attempt_id, run_id, lease_id, installation_id, generation,
		 context_json, outputs_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, a.ID, a.RunID, a.LeaseID, a.InstallationID, a.Generation,
		contextJSON, outputsJSON, formatStamp(at))
	return err
}

// latestRunCheckpoint reads the newest checkpoint context across one run's
// attempts; no checkpoint reads as nil. Replacement claims continue from
// the run's checkpoint lineage.
func latestRunCheckpoint(ctx context.Context, unit contract.Unit, runID contract.ID) (*wireArtifactRef, error) {
	row := unit.QueryRowContext(ctx, `SELECT context_json FROM execution_checkpoints
		WHERE run_id = ? ORDER BY created_at DESC LIMIT 1`, runID)
	var raw string
	err := row.Scan(&raw)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ref wireArtifactRef
	if err := decodeJSON(raw, &ref); err != nil {
		return nil, err
	}
	return &ref, nil
}

// insertLineage persists one context lineage record.
func insertLineage(ctx context.Context, unit contract.Unit, id contract.ID, a *attemptRow, kind, artifactID string, digest contract.Digest, operationID contract.ID, at time.Time) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO execution_context_lineage
		(id, attempt_id, run_id, installation_id, kind, artifact_id,
		 artifact_digest, operation_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, a.ID, a.RunID, a.InstallationID, kind, artifactID, digest, operationID, formatStamp(at))
	return err
}

// insertObligation records one unresolved recovery obligation.
func insertObligation(ctx context.Context, unit contract.Unit, id contract.ID, kind string, installationID, runID, attemptID, resourceID contract.ID, message string, at time.Time) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO execution_obligations
		(id, kind, installation_id, run_id, attempt_id, resource_id, message,
		 resolved_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', ?)`,
		id, kind, installationID, runID, attemptID, resourceID, message, formatStamp(at))
	return err
}

// unresolvedObligations reads open obligations for one resource; column is a
// fixed allowlist, never caller input.
func unresolvedObligations(ctx context.Context, unit contract.Unit, column string, id contract.ID) ([]wireRequirement, error) {
	if column != "run_id" && column != "attempt_id" {
		return nil, fmt.Errorf("execution: invalid obligation column %q", column)
	}
	rows, err := unit.QueryContext(ctx, `SELECT kind, message, resource_id
		FROM execution_obligations WHERE `+column+` = ? AND resolved_at = ''
		ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []wireRequirement
	for rows.Next() {
		var kind, message string
		var resourceID contract.ID
		if err := rows.Scan(&kind, &message, &resourceID); err != nil {
			return nil, err
		}
		out = append(out, wireRequirement{Code: kind, Message: message, ResourceID: resourceID})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []wireRequirement{}
	}
	return out, nil
}

// resolveObligations closes the open obligations of one attempt that carry
// exactly the given kind; obligations of any other kind stay open for
// recovery inspection.
func resolveObligations(ctx context.Context, unit contract.Unit, attemptID contract.ID, kind string, at time.Time) error {
	_, err := unit.ExecContext(ctx, `UPDATE execution_obligations
		SET resolved_at = ? WHERE attempt_id = ? AND kind = ? AND resolved_at = ''`,
		formatStamp(at), attemptID, kind)
	return err
}

// insertOperationRecord records one owned operation row.
func insertOperationRecord(ctx context.Context, unit contract.Unit, id contract.ID, a *attemptRow, kind, state, operationRef string, step any, at time.Time) error {
	stepJSON, err := encodeJSON(step)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO execution_operations
		(id, attempt_id, run_id, installation_id, kind, state, operation_ref,
		 step_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, a.ID, a.RunID, a.InstallationID, kind, state, operationRef, stepJSON, formatStamp(at))
	return err
}

// updateOperationRecord moves one owned operation to its terminal state.
func updateOperationRecord(ctx context.Context, unit contract.Unit, id contract.ID, state string) error {
	res, err := unit.ExecContext(ctx, `UPDATE execution_operations
		SET state = ? WHERE id = ?`, state, id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

// insertVerificationJob persists the verification request for a reported
// attempt.
func insertVerificationJob(ctx context.Context, unit contract.Unit, id contract.ID, a *attemptRow, request json.RawMessage, acceptanceDigest contract.Digest, at time.Time) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO execution_verification_jobs
		(id, attempt_id, run_id, task_id, installation_id, state, request_json,
		 result_json, acceptance_digest, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?, '', ?, ?, ?)`,
		id, a.ID, a.RunID, a.TaskID, a.InstallationID, string(request),
		acceptanceDigest, formatStamp(at), formatStamp(at))
	return err
}

const verificationJobColumns = `id, version, attempt_id, run_id, task_id,
installation_id, state, request_json, result_json, acceptance_digest`

func scanVerificationJob(scan func(dest ...any) error) (*verificationJobRow, error) {
	var v verificationJobRow
	var request, result string
	err := scan(&v.ID, &v.Version, &v.AttemptID, &v.RunID, &v.TaskID, &v.InstallationID,
		&v.State, &request, &result, &v.AcceptanceDigest)
	if err != nil {
		return nil, err
	}
	v.Request = json.RawMessage(request)
	if result != "" {
		v.Result = json.RawMessage(result)
	}
	return &v, nil
}

// loadVerificationJob reads the newest verification job of one attempt.
func loadVerificationJob(ctx context.Context, unit contract.Unit, attemptID contract.ID) (*verificationJobRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+verificationJobColumns+`
		FROM execution_verification_jobs WHERE attempt_id = ?
		ORDER BY created_at DESC LIMIT 1`, attemptID)
	v, err := scanVerificationJob(row.Scan)
	if isNoRows(err) {
		return nil, notFound("no verification job exists for attempt %s", attemptID)
	}
	return v, err
}

// loadVerificationJobByID reads one verification job by its own id (the
// VerificationRequest.job_id the sealed request and _execution.verification
// pipeline name).
func loadVerificationJobByID(ctx context.Context, unit contract.Unit, id contract.ID) (*verificationJobRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+verificationJobColumns+`
		FROM execution_verification_jobs WHERE id = ?`, id)
	v, err := scanVerificationJob(row.Scan)
	if isNoRows(err) {
		return nil, notFound("verification request %s does not exist", id)
	}
	return v, err
}

// listPendingVerificationJobs reads the installation's pending verification
// jobs oldest first, bounded: the fair scan _execution.verification.pending
// serves. No worker can claim verifier authority through this read-only
// scan; only the trusted verifier path claims and records a verdict.
func listPendingVerificationJobs(ctx context.Context, unit contract.Unit, installation contract.ID, limit int64) ([]*verificationJobRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+verificationJobColumns+`
		FROM execution_verification_jobs
		WHERE installation_id = ? AND state = 'pending'
		ORDER BY created_at ASC LIMIT ?`, installation, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*verificationJobRow
	for rows.Next() {
		v, err := scanVerificationJob(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// updateVerificationJob records the verifier result and terminal state,
// version-fenced.
func updateVerificationJob(ctx context.Context, unit contract.Unit, v *verificationJobRow, result json.RawMessage, at time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE execution_verification_jobs
		SET version = version + 1, state = ?, result_json = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		v.State, string(result), formatStamp(at), v.ID, v.Version)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	v.Version++
	return nil
}

// operationRow is the storage representation of one owned operation record.
type operationRow struct {
	ID             contract.ID
	AttemptID      contract.ID
	RunID          contract.ID
	InstallationID contract.ID
	Kind           string
	State          string
	OperationRef   string
	CreatedAt      time.Time
}

// loadOperationRecord reads one owned operation record by id.
func loadOperationRecord(ctx context.Context, unit contract.Unit, id contract.ID) (*operationRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT id, attempt_id, run_id, installation_id,
		kind, state, operation_ref, created_at FROM execution_operations WHERE id = ?`, id)
	var o operationRow
	var created string
	err := row.Scan(&o.ID, &o.AttemptID, &o.RunID, &o.InstallationID,
		&o.Kind, &o.State, &o.OperationRef, &created)
	if isNoRows(err) {
		return nil, notFound("operation record %s does not exist", id)
	}
	if err != nil {
		return nil, err
	}
	if o.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	return &o, nil
}

// loadOperationRecordByRef reads the attempt's owned record of one effects
// operation. The controller delivers by the identity it holds — the attempt
// from the action parameters and the effects operation it claimed — so the
// record is keyed by both; this owner's record id is never handed out.
func loadOperationRecordByRef(ctx context.Context, unit contract.Unit, attemptID contract.ID, ref string) (*operationRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT id, attempt_id, run_id, installation_id,
		kind, state, operation_ref, created_at FROM execution_operations
		WHERE attempt_id = ? AND operation_ref = ?
		ORDER BY created_at DESC, id DESC LIMIT 1`, attemptID, ref)
	var o operationRow
	var created string
	err := row.Scan(&o.ID, &o.AttemptID, &o.RunID, &o.InstallationID,
		&o.Kind, &o.State, &o.OperationRef, &created)
	if isNoRows(err) {
		return nil, notFound("attempt %s has no operation record for effects operation %s", attemptID, ref)
	}
	if err != nil {
		return nil, err
	}
	if o.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	return &o, nil
}

// openOperationOf returns the attempt's newest operation that has not been
// closed by an observation: prepared (intent persisted, effect not yet
// dispatched) or admitted (dispatched, no observation yet). No match reads
// as nil.
func openOperationOf(ctx context.Context, unit contract.Unit, attemptID contract.ID) (*operationRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT id, attempt_id, run_id, installation_id,
		kind, state, operation_ref, created_at FROM execution_operations
		WHERE attempt_id = ? AND state IN ('prepared','admitted')
		ORDER BY created_at DESC, id DESC LIMIT 1`, attemptID)
	var o operationRow
	var created string
	err := row.Scan(&o.ID, &o.AttemptID, &o.RunID, &o.InstallationID,
		&o.Kind, &o.State, &o.OperationRef, &created)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if o.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	return &o, nil
}

// findRunByTask locates the run pinned to one task/version; no match reads
// as nil. Enqueue deduplicates on this identity.
func findRunByTask(ctx context.Context, unit contract.Unit, taskID contract.ID, taskVersion contract.Version) (*runRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+runColumns+` FROM execution_runs
		WHERE task_id = ? AND task_version = ? ORDER BY created_at DESC LIMIT 1`, taskID, taskVersion)
	r, err := scanRun(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return r, err
}

// listRuns reads runs matching the conditions, newest first.
func listRuns(ctx context.Context, unit contract.Unit, conds []string, args []any, limit int64) ([]*runRow, error) {
	query := `SELECT ` + runColumns + ` FROM execution_runs`
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
	var out []*runRow
	for rows.Next() {
		r, err := scanRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// touchLease extends one lease to a new expiry and stamps the heartbeat.
func touchLease(ctx context.Context, unit contract.Unit, id contract.ID, expiresAt, heartbeatAt time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE execution_leases
		SET expires_at = ?, last_heartbeat = ? WHERE id = ?`,
		formatStamp(expiresAt), formatStamp(heartbeatAt), id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

// turnRow is the storage representation of one durable worker turn.
type turnRow struct {
	ID                    contract.ID
	Version               contract.Version
	WorkerID              contract.ID
	PrincipalID           contract.ID
	InstallationID        contract.ID
	OrganizationID        contract.ID
	ProjectID             contract.ID
	TaskScopeID           contract.ID
	Scope                 contract.Scope
	Source                wireTurnSource
	RequesterID           contract.ID
	ConfigurationRevision contract.Version
	State                 string
	Generation            int64
	Limits                wireLimits
	RootID                contract.ID
	StepsUsed             int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	ConversationID        contract.ID
	TaskID                contract.ID
	RunID                 contract.ID
	AttemptID             contract.ID
	WaitingReason         string
	WaitingResourceID     contract.ID
	NextWake              time.Time
	LeaseID               contract.ID
	LeaseExpiresAt        time.Time
	ContextArtifact       *wireArtifactRef
	LastObservationID     contract.ID
}

const turnColumns = `id, version, worker_id, principal_id, installation_id,
organization_id, project_id, task_scope_id, scope_json, source_kind, source_id,
source_version, recipient_worker_id, requester_id, configuration_revision,
state, generation, limits_json, root_id, steps_used, created_at, updated_at,
conversation_id, task_id, run_id, attempt_id, waiting_reason,
waiting_resource_id, next_wake, lease_id, lease_expires_at,
context_artifact_json, last_observation_id`

func scanTurn(scan func(dest ...any) error) (*turnRow, error) {
	var t turnRow
	var scopeJSON, limitsJSON, contextJSON string
	var sourceKind, sourceID string
	var sourceVersion contract.Version
	var recipientWorkerID contract.ID
	var nextWake, leaseExpires, created, updated string
	err := scan(&t.ID, &t.Version, &t.WorkerID, &t.PrincipalID, &t.InstallationID,
		&t.OrganizationID, &t.ProjectID, &t.TaskScopeID, &scopeJSON, &sourceKind, &sourceID,
		&sourceVersion, &recipientWorkerID, &t.RequesterID, &t.ConfigurationRevision,
		&t.State, &t.Generation, &limitsJSON, &t.RootID, &t.StepsUsed, &created, &updated,
		&t.ConversationID, &t.TaskID, &t.RunID, &t.AttemptID, &t.WaitingReason,
		&t.WaitingResourceID, &nextWake, &t.LeaseID, &leaseExpires,
		&contextJSON, &t.LastObservationID)
	if err != nil {
		return nil, err
	}
	t.Source = wireTurnSource{
		Kind: sourceKind, SourceID: contract.ID(sourceID), SourceVersion: sourceVersion,
		RecipientWorkerID: recipientWorkerID,
	}
	if err := decodeJSON(scopeJSON, &t.Scope); err != nil {
		return nil, err
	}
	if err := decodeJSON(limitsJSON, &t.Limits); err != nil {
		return nil, err
	}
	if err := decodeJSON(contextJSON, &t.ContextArtifact); err != nil {
		return nil, err
	}
	if t.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	if t.UpdatedAt, err = parseStamp(updated); err != nil {
		return nil, err
	}
	if t.NextWake, err = parseStamp(nextWake); err != nil {
		return nil, err
	}
	if t.LeaseExpiresAt, err = parseStamp(leaseExpires); err != nil {
		return nil, err
	}
	return &t, nil
}

// loadTurn reads one worker turn by id.
func loadTurn(ctx context.Context, unit contract.Unit, id contract.ID) (*turnRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+turnColumns+` FROM execution_turns WHERE id = ?`, id)
	t, err := scanTurn(row.Scan)
	if isNoRows(err) {
		return nil, notFound("turn %s does not exist", id)
	}
	return t, err
}

// loadTurnForUpdate reads one worker turn and enforces the version fence.
func loadTurnForUpdate(ctx context.Context, unit contract.Unit, id contract.ID, expected contract.Version) (*turnRow, error) {
	t, err := loadTurn(ctx, unit, id)
	if err != nil {
		return nil, err
	}
	if t.Version != expected {
		return nil, staleVersion("turn %s version %d does not match expected version %d", id, t.Version, expected)
	}
	return t, nil
}

// findTurnBySource locates the turn admitted for one exact source identity;
// no match reads as nil. This is _execution.turn.admit's idempotency key.
func findTurnBySource(ctx context.Context, unit contract.Unit, installation contract.ID, sourceKind string, sourceID contract.ID, sourceVersion contract.Version, workerID contract.ID) (*turnRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+turnColumns+` FROM execution_turns
		WHERE installation_id = ? AND source_kind = ? AND source_id = ? AND source_version = ? AND worker_id = ?`,
		installation, sourceKind, sourceID, sourceVersion, workerID)
	t, err := scanTurn(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return t, err
}

// findActiveTurnForWorker locates the worker's one live (non-terminal)
// decision stream; no match reads as nil. A message or responsibility
// trigger for a worker with an active turn is a safe-boundary injection into
// that turn, never a second concurrent stream.
func findActiveTurnForWorker(ctx context.Context, unit contract.Unit, installation, workerID contract.ID) (*turnRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+turnColumns+` FROM execution_turns
		WHERE installation_id = ? AND worker_id = ?
		AND state NOT IN ('completed','failed','cancelled')
		ORDER BY created_at DESC LIMIT 1`, installation, workerID)
	t, err := scanTurn(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return t, err
}

// insertTurn persists a new worker turn at version 1.
func insertTurn(ctx context.Context, unit contract.Unit, t *turnRow) error {
	scopeJSON, err := encodeScope(t.Scope)
	if err != nil {
		return err
	}
	limitsJSON, err := encodeJSON(t.Limits)
	if err != nil {
		return err
	}
	contextJSON, err := encodeJSON(t.ContextArtifact)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO execution_turns
		(id, version, worker_id, principal_id, installation_id, organization_id,
		 project_id, task_scope_id, scope_json, source_kind, source_id,
		 source_version, recipient_worker_id, requester_id, configuration_revision,
		 state, generation, limits_json, root_id, steps_used, created_at, updated_at,
		 conversation_id, task_id, run_id, attempt_id, waiting_reason,
		 waiting_resource_id, next_wake, lease_id, lease_expires_at,
		 context_artifact_json, last_observation_id)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.WorkerID, t.PrincipalID, t.InstallationID, t.OrganizationID,
		t.ProjectID, t.TaskScopeID, scopeJSON, t.Source.Kind, t.Source.SourceID,
		t.Source.SourceVersion, t.Source.RecipientWorkerID, t.RequesterID, t.ConfigurationRevision,
		t.State, t.Generation, limitsJSON, t.RootID, t.StepsUsed, formatStamp(t.CreatedAt), formatStamp(t.UpdatedAt),
		t.ConversationID, t.TaskID, t.RunID, t.AttemptID, t.WaitingReason,
		t.WaitingResourceID, formatStamp(t.NextWake), t.LeaseID, formatStamp(t.LeaseExpiresAt),
		contextJSON, t.LastObservationID)
	return err
}

// updateTurn applies a version-fenced mutation to one worker turn.
func updateTurn(ctx context.Context, unit contract.Unit, t *turnRow) error {
	contextJSON, err := encodeJSON(t.ContextArtifact)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `UPDATE execution_turns SET
		version = ?, state = ?, generation = ?, root_id = ?, steps_used = ?, updated_at = ?,
		task_id = ?, run_id = ?, attempt_id = ?, waiting_reason = ?, waiting_resource_id = ?,
		next_wake = ?, lease_id = ?, lease_expires_at = ?, context_artifact_json = ?,
		last_observation_id = ?
		WHERE id = ? AND version = ?`,
		t.Version+1, t.State, t.Generation, t.RootID, t.StepsUsed, formatStamp(t.UpdatedAt),
		t.TaskID, t.RunID, t.AttemptID, t.WaitingReason, t.WaitingResourceID,
		formatStamp(t.NextWake), t.LeaseID, formatStamp(t.LeaseExpiresAt), contextJSON,
		t.LastObservationID, t.ID, t.Version)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	t.Version++
	return nil
}

// listTurns reads turns matching the conditions, oldest first: the durable
// worker loop's fair, bounded scans order by admission time.
func listTurns(ctx context.Context, unit contract.Unit, conds []string, args []any, limit int64) ([]*turnRow, error) {
	query := `SELECT ` + turnColumns + ` FROM execution_turns`
	if len(conds) > 0 {
		query += ` WHERE ` + joinConds(conds)
	}
	query += ` ORDER BY created_at ASC, id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := unit.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*turnRow
	for rows.Next() {
		t, err := scanTurn(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// proposalRow is the storage representation of one durable ProposalRecord.
type proposalRow struct {
	ID                  contract.ID
	InstallationID      contract.ID
	TurnID              contract.ID
	StepIndex           int64
	ProposalID          string
	SourceContextDigest contract.Digest
	NormalizedProposal  json.RawMessage
	State               string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	CommandID           contract.ID
	EffectOperationID   contract.ID
	ResultArtifact      *wireArtifactRef
}

const proposalColumns = `id, installation_id, turn_id, step_index, proposal_id,
source_context_digest, normalized_proposal_json, state, created_at, updated_at,
command_id, effect_operation_id, result_artifact_json`

func scanProposal(scan func(dest ...any) error) (*proposalRow, error) {
	var p proposalRow
	var normalizedJSON, artifactJSON string
	var created, updated string
	err := scan(&p.ID, &p.InstallationID, &p.TurnID, &p.StepIndex, &p.ProposalID,
		&p.SourceContextDigest, &normalizedJSON, &p.State, &created, &updated,
		&p.CommandID, &p.EffectOperationID, &artifactJSON)
	if err != nil {
		return nil, err
	}
	if normalizedJSON != "" {
		p.NormalizedProposal = json.RawMessage(normalizedJSON)
	}
	if err := decodeJSON(artifactJSON, &p.ResultArtifact); err != nil {
		return nil, err
	}
	if p.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	if p.UpdatedAt, err = parseStamp(updated); err != nil {
		return nil, err
	}
	return &p, nil
}

// findProposalByKey locates the proposal at its unique (turn_id, step_index,
// proposal_id) key; no match reads as nil.
func findProposalByKey(ctx context.Context, unit contract.Unit, turnID contract.ID, stepIndex int64, proposalID string) (*proposalRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+proposalColumns+` FROM execution_proposals
		WHERE turn_id = ? AND step_index = ? AND proposal_id = ?`, turnID, stepIndex, proposalID)
	p, err := scanProposal(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return p, err
}

// findProposalByID locates the newest proposal carrying the given
// proposal_id within the caller's installation; no match reads as nil.
// _execution.proposal.record's input names only the proposal identity, so
// this resolves the record it was staged against.
func findProposalByID(ctx context.Context, unit contract.Unit, installation contract.ID, proposalID string) (*proposalRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+proposalColumns+` FROM execution_proposals
		WHERE installation_id = ? AND proposal_id = ? ORDER BY created_at DESC LIMIT 1`,
		installation, proposalID)
	p, err := scanProposal(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return p, err
}

// insertProposal persists a new proposal record.
func insertProposal(ctx context.Context, unit contract.Unit, p *proposalRow) error {
	artifactJSON, err := encodeJSON(p.ResultArtifact)
	if err != nil {
		return err
	}
	normalized := string(p.NormalizedProposal)
	if normalized == "" {
		normalized = "{}"
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO execution_proposals
		(id, installation_id, turn_id, step_index, proposal_id, source_context_digest,
		 normalized_proposal_json, state, created_at, updated_at, command_id,
		 effect_operation_id, result_artifact_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.InstallationID, p.TurnID, p.StepIndex, p.ProposalID, p.SourceContextDigest,
		normalized, p.State, formatStamp(p.CreatedAt), formatStamp(p.UpdatedAt),
		p.CommandID, p.EffectOperationID, artifactJSON)
	return err
}

// updateProposal records the recorded disposition of an existing proposal.
func updateProposal(ctx context.Context, unit contract.Unit, p *proposalRow) error {
	artifactJSON, err := encodeJSON(p.ResultArtifact)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `UPDATE execution_proposals SET
		state = ?, updated_at = ?, command_id = ?, effect_operation_id = ?, result_artifact_json = ?
		WHERE id = ?`,
		p.State, formatStamp(p.UpdatedAt), p.CommandID, p.EffectOperationID, artifactJSON, p.ID)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

// contextPlanRow is the storage representation of one versioned immutable
// ContextPlan.
type contextPlanRow struct {
	ID                    contract.ID
	InstallationID        contract.ID
	TurnID                contract.ID
	ExpectedVersion       contract.Version
	Generation            int64
	Refs                  []wireArtifactRef
	ConfigurationRevision contract.Version
	ByteBound             int64
	TokenBound            int64
	Committed             bool
	CreatedAt             time.Time
}

// insertContextPlan persists a new immutable context plan.
func insertContextPlan(ctx context.Context, unit contract.Unit, p *contextPlanRow) error {
	refsJSON, err := encodeJSON(p.Refs)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO execution_context_plans
		(id, installation_id, turn_id, expected_version, generation, refs_json,
		 configuration_revision, byte_bound, token_bound, committed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		p.ID, p.InstallationID, p.TurnID, p.ExpectedVersion, p.Generation, refsJSON,
		p.ConfigurationRevision, p.ByteBound, p.TokenBound, formatStamp(p.CreatedAt))
	return err
}

// loadContextPlan reads one context plan by id.
func loadContextPlan(ctx context.Context, unit contract.Unit, id contract.ID) (*contextPlanRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT id, installation_id, turn_id, expected_version,
		generation, refs_json, configuration_revision, byte_bound, token_bound, committed, created_at
		FROM execution_context_plans WHERE id = ?`, id)
	var p contextPlanRow
	var refsJSON string
	var committed int64
	var created string
	err := row.Scan(&p.ID, &p.InstallationID, &p.TurnID, &p.ExpectedVersion, &p.Generation,
		&refsJSON, &p.ConfigurationRevision, &p.ByteBound, &p.TokenBound, &committed, &created)
	if isNoRows(err) {
		return nil, notFound("context plan %s does not exist", id)
	}
	if err != nil {
		return nil, err
	}
	if err := decodeJSON(refsJSON, &p.Refs); err != nil {
		return nil, err
	}
	p.Committed = committed == 1
	if p.CreatedAt, err = parseStamp(created); err != nil {
		return nil, err
	}
	return &p, nil
}

// commitContextPlan marks a plan committed exactly once; a repeated commit
// of the same plan id matches no row, so the caller's own idempotent-replay
// check (by turn state) must run first.
func commitContextPlan(ctx context.Context, unit contract.Unit, id contract.ID) error {
	res, err := unit.ExecContext(ctx, `UPDATE execution_context_plans
		SET committed = 1 WHERE id = ? AND committed = 0`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

// verificationClaimRow tracks the generation-bound claim of one sealed
// VerificationRequest, kept separate from execution_verification_jobs so the
// original table's shape stays untouched.
type verificationClaimRow struct {
	RequestID         contract.ID
	ClaimedGeneration int64
	ClaimToken        string
	ClaimedAt         time.Time
}

// loadVerificationClaim reads the current claim of one verification job; no
// claim reads as nil.
func loadVerificationClaim(ctx context.Context, unit contract.Unit, requestID contract.ID) (*verificationClaimRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT request_id, claimed_generation, claim_token, claimed_at
		FROM execution_verification_claims WHERE request_id = ?`, requestID)
	var c verificationClaimRow
	var claimedAt string
	err := row.Scan(&c.RequestID, &c.ClaimedGeneration, &c.ClaimToken, &claimedAt)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.ClaimedAt, err = parseStamp(claimedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// setVerificationClaim upserts the claim of one verification job.
func setVerificationClaim(ctx context.Context, unit contract.Unit, c *verificationClaimRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO execution_verification_claims
		(request_id, claimed_generation, claim_token, claimed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET
		  claimed_generation = excluded.claimed_generation,
		  claim_token = excluded.claim_token, claimed_at = excluded.claimed_at`,
		c.RequestID, c.ClaimedGeneration, c.ClaimToken, formatStamp(c.ClaimedAt))
	return err
}

// findJobBySourceID locates the newest job committed for one source identity;
// no match reads as nil. Job identity is the source identity plus the
// canonical input hash; callers compare the hash for divergence.
func findJobBySourceID(ctx context.Context, unit contract.Unit, sourceID contract.ID) (*jobRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM execution_jobs
		WHERE source_id = ? ORDER BY created_at DESC LIMIT 1`, sourceID)
	j, err := scanJob(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return j, err
}
