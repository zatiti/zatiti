package scheduling

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

// Storage access. Every statement runs on the unit's transaction; scope
// dimensions are stored as explicit columns for filtered reads and the
// canonical scope JSON for wire output. Timestamps persist as fixed-width
// UTC strings so lexicographic comparison matches chronological order, which
// the keyset pagination and due-wake scan rely on.

// expectOneRow enforces optimistic fencing: an update that matched no row
// means another writer moved the version first.
func expectOneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("scheduling: confirm row: %w", err)
	}
	if n != 1 {
		return conflict("the resource changed concurrently; retry with the current version")
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

// stampArg encodes a nullable timestamp for a TEXT column.
func stampArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatStamp(*t)
}

// encodeStrings marshals a string list; nil normalizes to an empty list so
// stored JSON always re-reads as a populated slice.
func encodeStrings(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("scheduling: encode strings: %w", err)
	}
	return string(raw), nil
}

// decodeStrings parses a stored string list.
func decodeStrings(raw string) ([]string, error) {
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("scheduling: decode strings: %w", err)
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

// encodeIDs marshals an ID list with the same nil normalization.
func encodeIDs(ids []contract.ID) (string, error) {
	if ids == nil {
		ids = []contract.ID{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return "", fmt.Errorf("scheduling: encode ids: %w", err)
	}
	return string(raw), nil
}

// encodeJSON marshals one typed definition for a JSON column.
func encodeJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("scheduling: encode json: %w", err)
	}
	return string(raw), nil
}

// decodeJSON parses one stored typed definition.
func decodeJSON(raw string, v any) error {
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("scheduling: decode json: %w", err)
	}
	return nil
}

// scopeDims returns the filterable dimension columns of a scope.
func scopeDims(scope contract.Scope) (installation, organization, project, worker, task string) {
	return string(scope.InstallationID), string(scope.OrganizationID),
		string(scope.ProjectID), string(scope.WorkerID), string(scope.TaskID)
}

// keysetCond is the shared keyset continuation clause over (created_at, id).
func keysetCond(created time.Time, id contract.ID) (string, []any) {
	cond := `(created_at > ? OR (created_at = ? AND id > ?))`
	return cond, []any{formatStamp(created), formatStamp(created), string(id)}
}

// Schedules.

type scheduleRow struct {
	ID             contract.ID
	Version        contract.Version
	InstallationID string
	OrganizationID string
	ProjectID      string
	WorkerID       string
	TaskID         string
	Scope          contract.Scope
	TaskTemplate   json.RawMessage
	Timezone       string
	Expression     string
	Misfire        string
	CatchUpSeconds int64
	Paused         bool
	NextWake       *time.Time
	Archived       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const scheduleCols = `id, version, installation_id, organization_id, project_id, worker_id, task_id, scope_json, task_template_json, timezone, expression, misfire, catch_up_seconds, paused, next_wake, archived, created_at, updated_at`

func scanSchedule(scan func(...any) error) (scheduleRow, error) {
	r := scheduleRow{}
	var scopeJSON, templateJSON, created, updated string
	var workerID, taskID string
	var paused, archived int64
	var nextWake sql.NullString
	err := scan(&r.ID, &r.Version, &r.InstallationID, &r.OrganizationID, &r.ProjectID,
		&workerID, &taskID, &scopeJSON, &templateJSON, &r.Timezone, &r.Expression,
		&r.Misfire, &r.CatchUpSeconds, &paused, &nextWake, &archived, &created, &updated)
	if err != nil {
		return scheduleRow{}, err
	}
	if r.Scope, err = decodeScope(scopeJSON); err != nil {
		return scheduleRow{}, err
	}
	r.TaskTemplate = json.RawMessage(templateJSON)
	r.WorkerID, r.TaskID = workerID, taskID
	r.Paused, r.Archived = paused == 1, archived == 1
	if r.NextWake, err = scanStamp(nextWake); err != nil {
		return scheduleRow{}, err
	}
	if r.CreatedAt, err = parseStamp(created); err != nil {
		return scheduleRow{}, err
	}
	if r.UpdatedAt, err = parseStamp(updated); err != nil {
		return scheduleRow{}, err
	}
	return r, nil
}

// scanStamp parses a nullable stored timestamp.
func scanStamp(null sql.NullString) (*time.Time, error) {
	if !null.Valid {
		return nil, nil
	}
	t, err := parseStamp(null.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// insertSchedule persists one new schedule row.
func insertSchedule(ctx context.Context, unit contract.Unit, r scheduleRow) error {
	scopeJSON, err := encodeScope(r.Scope)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO scheduling_schedules
		(id, version, installation_id, organization_id, project_id, worker_id, task_id,
		 scope_json, task_template_json, timezone, expression, misfire, catch_up_seconds,
		 paused, next_wake, archived, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Version, r.InstallationID, r.OrganizationID, r.ProjectID, r.WorkerID, r.TaskID,
		scopeJSON, string(r.TaskTemplate), r.Timezone, r.Expression, r.Misfire, r.CatchUpSeconds,
		boolInt(r.Paused), stampArg(r.NextWake), boolInt(r.Archived),
		formatStamp(r.CreatedAt), formatStamp(r.UpdatedAt))
	if err != nil {
		return fmt.Errorf("scheduling: insert schedule: %w", err)
	}
	return nil
}

// loadSchedule reads one schedule by installation and id. A missing row is
// not an error; the found flag distinguishes it for not_found faults.
func loadSchedule(ctx context.Context, unit contract.Unit, id contract.ID) (scheduleRow, bool, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+scheduleCols+` FROM scheduling_schedules WHERE installation_id = ? AND id = ?`,
		unit.Scope().InstallationID, id)
	r, err := scanSchedule(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return scheduleRow{}, false, nil
	}
	if err != nil {
		return scheduleRow{}, false, fmt.Errorf("scheduling: load schedule: %w", err)
	}
	return r, true, nil
}

// listSchedules reads schedules matching the caller's conditions, ordered by
// (created_at, id). conds are joined with AND and must already include any
// keyset continuation clause.
func listSchedules(ctx context.Context, unit contract.Unit, conds []string, args []any, limit int64) ([]scheduleRow, error) {
	query := `SELECT ` + scheduleCols + ` FROM scheduling_schedules`
	if len(conds) > 0 {
		query += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	query += ` ORDER BY created_at, id LIMIT ?`
	args = append(append([]any{}, args...), limit)
	rows, err := unit.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("scheduling: list schedules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []scheduleRow
	for rows.Next() {
		r, err := scanSchedule(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduling: list schedules: %w", err)
	}
	return out, nil
}

// updateScheduleDefinition rewrites a schedule's definition columns behind
// the version fence; r.Version is the post-bump version.
func updateScheduleDefinition(ctx context.Context, unit contract.Unit, r scheduleRow) error {
	scopeJSON, err := encodeScope(r.Scope)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `UPDATE scheduling_schedules SET
		organization_id = ?, project_id = ?, worker_id = ?, task_id = ?, scope_json = ?,
		task_template_json = ?, timezone = ?, expression = ?, misfire = ?,
		catch_up_seconds = ?, paused = ?, next_wake = ?, version = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		r.OrganizationID, r.ProjectID, r.WorkerID, r.TaskID, scopeJSON,
		string(r.TaskTemplate), r.Timezone, r.Expression, r.Misfire, r.CatchUpSeconds,
		boolInt(r.Paused), stampArg(r.NextWake), r.Version, formatStamp(r.UpdatedAt),
		r.ID, r.InstallationID, r.Version-1)
	if err != nil {
		return fmt.Errorf("scheduling: update schedule: %w", err)
	}
	return expectOneRow(res)
}

// updateScheduleState flips the paused flag behind the version fence; the
// pending wake is left in place so a later resume admits it through the
// misfire path. r.Version is the post-bump version.
func updateScheduleState(ctx context.Context, unit contract.Unit, r scheduleRow) error {
	res, err := unit.ExecContext(ctx, `UPDATE scheduling_schedules SET
		paused = ?, version = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		boolInt(r.Paused), r.Version, formatStamp(r.UpdatedAt),
		r.ID, r.InstallationID, r.Version-1)
	if err != nil {
		return fmt.Errorf("scheduling: update schedule state: %w", err)
	}
	return expectOneRow(res)
}

// updateScheduleNextWake persists the next-wake decision behind the current
// version fence without bumping the version: admission is operational state,
// not a definition change. r.Version is the row's current version.
func updateScheduleNextWake(ctx context.Context, unit contract.Unit, r scheduleRow) error {
	res, err := unit.ExecContext(ctx, `UPDATE scheduling_schedules SET
		next_wake = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		stampArg(r.NextWake), formatStamp(r.UpdatedAt),
		r.ID, r.InstallationID, r.Version)
	if err != nil {
		return fmt.Errorf("scheduling: update schedule next wake: %w", err)
	}
	return expectOneRow(res)
}

// archiveScheduleRow marks a schedule archived, drops its pending wake and
// bumps the version behind the fence. r.Version is the post-bump version.
func archiveScheduleRow(ctx context.Context, unit contract.Unit, r scheduleRow) error {
	res, err := unit.ExecContext(ctx, `UPDATE scheduling_schedules SET
		archived = 1, next_wake = NULL, version = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		r.Version, formatStamp(r.UpdatedAt), r.ID, r.InstallationID, r.Version-1)
	if err != nil {
		return fmt.Errorf("scheduling: archive schedule: %w", err)
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	return deletePendingWakes(ctx, unit, r.ID)
}

// Responsibilities.

type responsibilityRow struct {
	ID                   contract.ID
	Version              contract.Version
	InstallationID       string
	OrganizationID       string
	ProjectID            string
	WorkerID             contract.ID
	TaskID               string
	Scope                contract.Scope
	Outcome              string
	Signals              []string
	Triggers             []string
	ReasoningPolicy      string
	MinIntervalSeconds   int64
	CycleLimits          wireLimits
	AggregateLimits      wireLimits
	PauseConditions      []string
	EscalationConditions []string
	Acceptance           json.RawMessage
	Paused               bool
	NextWake             *time.Time
	LastCycleAt          *time.Time
	Archived             bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

const responsibilityCols = `id, version, installation_id, organization_id, project_id, worker_id, task_id, scope_json, outcome, signals_json, triggers_json, reasoning_policy, min_interval_seconds, cycle_limits_json, aggregate_limits_json, pause_conditions_json, escalation_conditions_json, acceptance_json, paused, next_wake, last_cycle_at, archived, created_at, updated_at`

func scanResponsibility(scan func(...any) error) (responsibilityRow, error) {
	r := responsibilityRow{}
	var scopeJSON, signalsJSON, triggersJSON, cycleJSON, aggregateJSON, pauseJSON, escalationJSON, acceptanceJSON string
	var created, updated, taskID string
	var paused, archived int64
	var nextWake, lastCycle sql.NullString
	err := scan(&r.ID, &r.Version, &r.InstallationID, &r.OrganizationID, &r.ProjectID,
		&r.WorkerID, &taskID, &scopeJSON, &r.Outcome, &signalsJSON, &triggersJSON,
		&r.ReasoningPolicy, &r.MinIntervalSeconds, &cycleJSON, &aggregateJSON,
		&pauseJSON, &escalationJSON, &acceptanceJSON, &paused, &nextWake, &lastCycle,
		&archived, &created, &updated)
	if err != nil {
		return responsibilityRow{}, err
	}
	if r.Scope, err = decodeScope(scopeJSON); err != nil {
		return responsibilityRow{}, err
	}
	r.TaskID = taskID
	if r.Signals, err = decodeStrings(signalsJSON); err != nil {
		return responsibilityRow{}, err
	}
	if r.Triggers, err = decodeStrings(triggersJSON); err != nil {
		return responsibilityRow{}, err
	}
	if err = decodeJSON(cycleJSON, &r.CycleLimits); err != nil {
		return responsibilityRow{}, err
	}
	if err = decodeJSON(aggregateJSON, &r.AggregateLimits); err != nil {
		return responsibilityRow{}, err
	}
	if r.PauseConditions, err = decodeStrings(pauseJSON); err != nil {
		return responsibilityRow{}, err
	}
	if r.EscalationConditions, err = decodeStrings(escalationJSON); err != nil {
		return responsibilityRow{}, err
	}
	r.Acceptance = json.RawMessage(acceptanceJSON)
	r.Paused, r.Archived = paused == 1, archived == 1
	if r.NextWake, err = scanStamp(nextWake); err != nil {
		return responsibilityRow{}, err
	}
	if r.LastCycleAt, err = scanStamp(lastCycle); err != nil {
		return responsibilityRow{}, err
	}
	if r.CreatedAt, err = parseStamp(created); err != nil {
		return responsibilityRow{}, err
	}
	if r.UpdatedAt, err = parseStamp(updated); err != nil {
		return responsibilityRow{}, err
	}
	return r, nil
}

// insertResponsibility persists one new responsibility row.
func insertResponsibility(ctx context.Context, unit contract.Unit, r responsibilityRow) error {
	scopeJSON, err := encodeScope(r.Scope)
	if err != nil {
		return err
	}
	signalsJSON, err := encodeStrings(r.Signals)
	if err != nil {
		return err
	}
	triggersJSON, err := encodeStrings(r.Triggers)
	if err != nil {
		return err
	}
	cycleJSON, err := encodeJSON(r.CycleLimits)
	if err != nil {
		return err
	}
	aggregateJSON, err := encodeJSON(r.AggregateLimits)
	if err != nil {
		return err
	}
	pauseJSON, err := encodeStrings(r.PauseConditions)
	if err != nil {
		return err
	}
	escalationJSON, err := encodeStrings(r.EscalationConditions)
	if err != nil {
		return err
	}
	acceptanceJSON := string(r.Acceptance)
	_, err = unit.ExecContext(ctx, `INSERT INTO scheduling_responsibilities
		(id, version, installation_id, organization_id, project_id, worker_id, task_id,
		 scope_json, outcome, signals_json, triggers_json, reasoning_policy,
		 min_interval_seconds, cycle_limits_json, aggregate_limits_json,
		 pause_conditions_json, escalation_conditions_json, acceptance_json,
		 paused, next_wake, last_cycle_at, archived, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Version, r.InstallationID, r.OrganizationID, r.ProjectID, r.WorkerID, r.TaskID,
		scopeJSON, r.Outcome, signalsJSON, triggersJSON, r.ReasoningPolicy,
		r.MinIntervalSeconds, cycleJSON, aggregateJSON, pauseJSON, escalationJSON,
		acceptanceJSON, boolInt(r.Paused), stampArg(r.NextWake), stampArg(r.LastCycleAt),
		boolInt(r.Archived), formatStamp(r.CreatedAt), formatStamp(r.UpdatedAt))
	if err != nil {
		return fmt.Errorf("scheduling: insert responsibility: %w", err)
	}
	return nil
}

// loadResponsibility reads one responsibility by installation and id. A
// missing row is not an error; the found flag distinguishes it.
func loadResponsibility(ctx context.Context, unit contract.Unit, id contract.ID) (responsibilityRow, bool, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+responsibilityCols+` FROM scheduling_responsibilities WHERE installation_id = ? AND id = ?`,
		unit.Scope().InstallationID, id)
	r, err := scanResponsibility(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return responsibilityRow{}, false, nil
	}
	if err != nil {
		return responsibilityRow{}, false, fmt.Errorf("scheduling: load responsibility: %w", err)
	}
	return r, true, nil
}

// listResponsibilities reads responsibilities matching the caller's
// conditions, ordered by (created_at, id).
func listResponsibilities(ctx context.Context, unit contract.Unit, conds []string, args []any, limit int64) ([]responsibilityRow, error) {
	query := `SELECT ` + responsibilityCols + ` FROM scheduling_responsibilities`
	if len(conds) > 0 {
		query += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	query += ` ORDER BY created_at, id LIMIT ?`
	args = append(append([]any{}, args...), limit)
	rows, err := unit.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("scheduling: list responsibilities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []responsibilityRow
	for rows.Next() {
		r, err := scanResponsibility(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduling: list responsibilities: %w", err)
	}
	return out, nil
}

// updateResponsibilityDefinition rewrites a responsibility's definition
// columns behind the version fence; r.Version is the post-bump version.
func updateResponsibilityDefinition(ctx context.Context, unit contract.Unit, r responsibilityRow) error {
	signalsJSON, err := encodeStrings(r.Signals)
	if err != nil {
		return err
	}
	triggersJSON, err := encodeStrings(r.Triggers)
	if err != nil {
		return err
	}
	cycleJSON, err := encodeJSON(r.CycleLimits)
	if err != nil {
		return err
	}
	aggregateJSON, err := encodeJSON(r.AggregateLimits)
	if err != nil {
		return err
	}
	pauseJSON, err := encodeStrings(r.PauseConditions)
	if err != nil {
		return err
	}
	escalationJSON, err := encodeStrings(r.EscalationConditions)
	if err != nil {
		return err
	}
	scopeJSON, err := encodeScope(r.Scope)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `UPDATE scheduling_responsibilities SET
		organization_id = ?, project_id = ?, worker_id = ?, task_id = ?, scope_json = ?,
		outcome = ?, signals_json = ?, triggers_json = ?, reasoning_policy = ?,
		min_interval_seconds = ?, cycle_limits_json = ?, aggregate_limits_json = ?,
		pause_conditions_json = ?, escalation_conditions_json = ?, acceptance_json = ?,
		paused = ?, next_wake = ?, version = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		r.OrganizationID, r.ProjectID, string(r.WorkerID), r.TaskID, scopeJSON,
		r.Outcome, signalsJSON, triggersJSON, r.ReasoningPolicy, r.MinIntervalSeconds,
		cycleJSON, aggregateJSON, pauseJSON, escalationJSON, string(r.Acceptance),
		boolInt(r.Paused), stampArg(r.NextWake), r.Version, formatStamp(r.UpdatedAt),
		r.ID, r.InstallationID, r.Version-1)
	if err != nil {
		return fmt.Errorf("scheduling: update responsibility: %w", err)
	}
	return expectOneRow(res)
}

// updateResponsibilityState flips the paused flag behind the version fence.
// r.Version is the post-bump version.
func updateResponsibilityState(ctx context.Context, unit contract.Unit, r responsibilityRow) error {
	res, err := unit.ExecContext(ctx, `UPDATE scheduling_responsibilities SET
		paused = ?, version = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		boolInt(r.Paused), r.Version, formatStamp(r.UpdatedAt),
		r.ID, r.InstallationID, r.Version-1)
	if err != nil {
		return fmt.Errorf("scheduling: update responsibility state: %w", err)
	}
	return expectOneRow(res)
}

// archiveResponsibilityRow marks a responsibility archived, drops its
// pending wake and bumps the version behind the fence.
func archiveResponsibilityRow(ctx context.Context, unit contract.Unit, r responsibilityRow) error {
	res, err := unit.ExecContext(ctx, `UPDATE scheduling_responsibilities SET
		archived = 1, next_wake = NULL, version = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		r.Version, formatStamp(r.UpdatedAt), r.ID, r.InstallationID, r.Version-1)
	if err != nil {
		return fmt.Errorf("scheduling: archive responsibility: %w", err)
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	return deletePendingWakes(ctx, unit, r.ID)
}

// recordResponsibilityCycle persists a recorded cycle: next wake, last cycle
// stamp and the post-bump version, behind the fence.
func recordResponsibilityCycle(ctx context.Context, unit contract.Unit, r responsibilityRow) error {
	res, err := unit.ExecContext(ctx, `UPDATE scheduling_responsibilities SET
		next_wake = ?, last_cycle_at = ?, version = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		stampArg(r.NextWake), stampArg(r.LastCycleAt), r.Version, formatStamp(r.UpdatedAt),
		r.ID, r.InstallationID, r.Version-1)
	if err != nil {
		return fmt.Errorf("scheduling: record cycle: %w", err)
	}
	return expectOneRow(res)
}

// Wakes.

type wakeRow struct {
	ID               contract.ID
	InstallationID   string
	OrganizationID   string
	ProjectID        string
	WorkerID         string
	TaskID           string
	Scope            contract.Scope
	SourceKind       string
	SourceID         contract.ID
	OccurrenceKey    string
	DueAt            time.Time
	ConditionVersion contract.Version
	AdmittedAt       *time.Time
	CreatedAt        time.Time
}

const wakeCols = `id, installation_id, organization_id, project_id, worker_id, task_id, scope_json, source_kind, source_id, occurrence_key, due_at, condition_version, admitted_at, created_at`

func scanWake(scan func(...any) error) (wakeRow, error) {
	r := wakeRow{}
	var scopeJSON, due, created string
	var conditionVersion int64
	var admitted sql.NullString
	err := scan(&r.ID, &r.InstallationID, &r.OrganizationID, &r.ProjectID, &r.WorkerID,
		&r.TaskID, &scopeJSON, &r.SourceKind, &r.SourceID, &r.OccurrenceKey, &due,
		&conditionVersion, &admitted, &created)
	if err != nil {
		return wakeRow{}, err
	}
	r.ConditionVersion = contract.Version(conditionVersion)
	if r.Scope, err = decodeScope(scopeJSON); err != nil {
		return wakeRow{}, err
	}
	if r.DueAt, err = parseStamp(due); err != nil {
		return wakeRow{}, err
	}
	if r.AdmittedAt, err = scanStamp(admitted); err != nil {
		return wakeRow{}, err
	}
	if r.CreatedAt, err = parseStamp(created); err != nil {
		return wakeRow{}, err
	}
	return r, nil
}

// insertWake persists one wake row. The (source_id, occurrence_key) unique
// index makes a duplicate insert of the same occurrence fail loudly; callers
// never insert a key that is already present.
func insertWake(ctx context.Context, unit contract.Unit, w wakeRow) error {
	scopeJSON, err := encodeScope(w.Scope)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO scheduling_wakes
		(id, installation_id, organization_id, project_id, worker_id, task_id, scope_json,
		 source_kind, source_id, occurrence_key, due_at, condition_version, admitted_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.InstallationID, w.OrganizationID, w.ProjectID, w.WorkerID, w.TaskID,
		scopeJSON, w.SourceKind, w.SourceID, w.OccurrenceKey, formatStamp(w.DueAt),
		w.ConditionVersion, stampArg(w.AdmittedAt), formatStamp(w.CreatedAt))
	if err != nil {
		return fmt.Errorf("scheduling: insert wake: %w", err)
	}
	return nil
}

// loadWake reads one wake by installation and id.
func loadWake(ctx context.Context, unit contract.Unit, id contract.ID) (wakeRow, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+wakeCols+` FROM scheduling_wakes WHERE installation_id = ? AND id = ?`,
		unit.Scope().InstallationID, id)
	return scanWake(row.Scan)
}

// listPendingWakes reads wakes due at or before now that have not been
// admitted, oldest due first.
func listPendingWakes(ctx context.Context, unit contract.Unit, now time.Time, limit int64) ([]wakeRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+wakeCols+` FROM scheduling_wakes
		WHERE installation_id = ? AND admitted_at IS NULL AND due_at <= ?
		ORDER BY due_at, created_at, id LIMIT ?`,
		unit.Scope().InstallationID, formatStamp(now), limit)
	if err != nil {
		return nil, fmt.Errorf("scheduling: list pending wakes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []wakeRow
	for rows.Next() {
		r, err := scanWake(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduling: list pending wakes: %w", err)
	}
	return out, nil
}

// fenceWakeAdmission marks a wake admitted exactly once. It returns false
// when the wake was already admitted, which makes duplicate deliveries
// idempotent.
func fenceWakeAdmission(ctx context.Context, unit contract.Unit, id contract.ID, now time.Time) (bool, error) {
	res, err := unit.ExecContext(ctx,
		`UPDATE scheduling_wakes SET admitted_at = ? WHERE id = ? AND admitted_at IS NULL`,
		formatStamp(now), id)
	if err != nil {
		return false, fmt.Errorf("scheduling: fence wake: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("scheduling: fence wake: %w", err)
	}
	return n == 1, nil
}

// pendingWakeExists reports whether a not-yet-admitted wake remains for a
// source, which resume uses to decide whether a new wake must be armed.
func pendingWakeExists(ctx context.Context, unit contract.Unit, sourceID contract.ID) (bool, error) {
	var one int64
	err := unit.QueryRowContext(ctx,
		`SELECT 1 FROM scheduling_wakes WHERE source_id = ? AND admitted_at IS NULL LIMIT 1`,
		sourceID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("scheduling: check pending wake: %w", err)
	}
	return true, nil
}

// deletePendingWakes removes not-yet-admitted wakes for one source, so a
// definition change or archive re-arms or cancels the pending wake without
// touching admitted history.
func deletePendingWakes(ctx context.Context, unit contract.Unit, sourceID contract.ID) error {
	_, err := unit.ExecContext(ctx,
		`DELETE FROM scheduling_wakes WHERE source_id = ? AND admitted_at IS NULL`, sourceID)
	if err != nil {
		return fmt.Errorf("scheduling: delete pending wakes: %w", err)
	}
	return nil
}

// Occurrences.

type occurrenceRow struct {
	ID             contract.ID
	InstallationID string
	OrganizationID string
	ProjectID      string
	WorkerID       string
	TaskID         string
	Scope          contract.Scope
	SourceID       contract.ID
	OccurrenceKey  string
	TaskRef        string
	State          string
	Reason         string
	DecidedAt      time.Time
	CreatedAt      time.Time
}

// occurrenceExists reports whether the (source_id, occurrence_key) pair was
// already recorded, the dedupe fence for admission.
func occurrenceExists(ctx context.Context, unit contract.Unit, sourceID contract.ID, occurrenceKey string) (bool, error) {
	var one int64
	err := unit.QueryRowContext(ctx,
		`SELECT 1 FROM scheduling_occurrences WHERE source_id = ? AND occurrence_key = ? LIMIT 1`,
		sourceID, occurrenceKey).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("scheduling: check occurrence: %w", err)
	}
	return true, nil
}

// insertOccurrence records one admitted or skipped occurrence.
func insertOccurrence(ctx context.Context, unit contract.Unit, o occurrenceRow) error {
	scopeJSON, err := encodeScope(o.Scope)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `INSERT INTO scheduling_occurrences
		(id, installation_id, organization_id, project_id, worker_id, task_id, scope_json,
		 source_id, occurrence_key, task_ref, state, reason, decided_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.InstallationID, o.OrganizationID, o.ProjectID, o.WorkerID, o.TaskID,
		scopeJSON, o.SourceID, o.OccurrenceKey, o.TaskRef, o.State, o.Reason,
		formatStamp(o.DecidedAt), formatStamp(o.CreatedAt))
	if err != nil {
		return fmt.Errorf("scheduling: insert occurrence: %w", err)
	}
	return nil
}

// Cycles.

type cycleRow struct {
	ID                    contract.ID
	ResponsibilityID      contract.ID
	ResponsibilityVersion contract.Version
	InstallationID        string
	OrganizationID        string
	ProjectID             string
	WorkerID              string
	TaskID                string
	Scope                 contract.Scope
	NextWake              time.Time
	Outputs               []wireArtifactRef
	TaskIDs               []contract.ID
	RecordedAt            time.Time
}

// insertCycle records one completed reasoning cycle.
func insertCycle(ctx context.Context, unit contract.Unit, c cycleRow) error {
	scopeJSON, err := encodeScope(c.Scope)
	if err != nil {
		return err
	}
	outputsJSON, err := encodeJSON(c.Outputs)
	if err != nil {
		return err
	}
	taskIDsJSON, err := encodeIDs(c.TaskIDs)
	if err != nil {
		return err
	}
	installation, organization, project, worker, task := scopeDims(c.Scope)
	_, err = unit.ExecContext(ctx, `INSERT INTO scheduling_cycles
		(id, responsibility_id, responsibility_version, installation_id, organization_id,
		 project_id, worker_id, task_id, scope_json, next_wake, outputs_json, task_ids_json,
		 recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.ResponsibilityID, c.ResponsibilityVersion, installation, organization,
		project, worker, task, scopeJSON, formatStamp(c.NextWake), outputsJSON,
		taskIDsJSON, formatStamp(c.RecordedAt))
	if err != nil {
		return fmt.Errorf("scheduling: insert cycle: %w", err)
	}
	return nil
}

// countCycles returns how many cycles a responsibility has recorded, the
// aggregate budget check's input.
func countCycles(ctx context.Context, unit contract.Unit, responsibilityID contract.ID) (int64, error) {
	var n int64
	if err := unit.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM scheduling_cycles WHERE responsibility_id = ?`,
		responsibilityID).Scan(&n); err != nil {
		return 0, fmt.Errorf("scheduling: count cycles: %w", err)
	}
	return n, nil
}
