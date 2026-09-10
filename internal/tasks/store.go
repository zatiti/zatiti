package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Store layer for the tasks_ tables. Rows scan through one decoder per
// table; JSON columns marshal and unmarshal at the edges. All queries run
// inside the caller's unit (one transaction).

const timeLayout = time.RFC3339Nano

// taskRow is one durable task with its pinned contract.
type taskRow struct {
	ID                    contract.ID
	Version               int64
	InstallationID        contract.ID
	OrganizationID        contract.ID
	ScopeJSON             string
	OwnerID               contract.ID
	WorkerID              contract.ID
	ParentID              contract.ID
	RootID                contract.ID
	Outcome               string
	InputsJSON            string
	RequiredOutputsJSON   string
	AcceptanceJSON        string
	AcceptanceDigest      string
	LimitsJSON            string
	DependenciesJSON      string
	State                 string
	PriorState            string
	WaitingReason         string
	CancellationRequested bool
	ManualAcceptance      bool
	EstablishedBy         string
	Attempt               int64
	SourceID              string
	OccurrenceKey         string
	ContentDigest         string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type evidenceRow struct {
	TaskID       contract.ID
	ArtifactID   contract.ID
	Installation contract.ID
	Digest       string
	MediaType    string
	Attempt      int64
	RecordedAt   time.Time
}

type decisionRow struct {
	ID           contract.ID
	TaskID       contract.ID
	Installation contract.ID
	Decision     string
	ReviewerID   contract.ID
	ActionDigest string
	Reason       string
	DecidedAt    time.Time
}

type transitionRow struct {
	ID            contract.ID
	TaskID        contract.ID
	Installation  contract.ID
	FromState     string
	ToState       string
	EvidenceJSON  string
	WaitingReason string
	Manual        bool
	RecordedAt    time.Time
}

// rowScanner abstracts *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

const taskColumns = `id, version, installation_id, organization_id, scope_json, owner_id, worker_id, parent_id, root_id,
	outcome, inputs_json, required_outputs_json, acceptance_json, acceptance_digest, limits_json,
	dependencies_json, state, prior_state, waiting_reason, cancellation_requested, manual_acceptance,
	established_by, attempt, source_id, occurrence_key, content_digest, created_at, updated_at`

func scanTask(row rowScanner) (*taskRow, error) {
	var r taskRow
	var scope, inputs, outputs, acceptance, limits, deps sql.NullString
	var cancellation, manual int64
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.OrganizationID, &scope, &r.OwnerID, &r.WorkerID,
		&r.ParentID, &r.RootID, &r.Outcome, &inputs, &outputs, &acceptance, &r.AcceptanceDigest,
		&limits, &deps, &r.State, &r.PriorState, &r.WaitingReason, &cancellation, &manual,
		&r.EstablishedBy, &r.Attempt, &r.SourceID, &r.OccurrenceKey, &r.ContentDigest,
		&created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.ScopeJSON = scope.String
	r.InputsJSON = inputs.String
	r.RequiredOutputsJSON = outputs.String
	r.AcceptanceJSON = acceptance.String
	r.LimitsJSON = limits.String
	r.DependenciesJSON = deps.String
	r.CancellationRequested = cancellation != 0
	r.ManualAcceptance = manual != 0
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

// getTask fetches one task by id; nil when absent.
func getTask(ctx context.Context, unit contract.Unit, id contract.ID) (*taskRow, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM tasks_tasks WHERE id = ?`, string(id))
	return scanTask(row)
}

// getTaskBySource resolves a wake/responsibility/conversation occurrence.
func getTaskBySource(ctx context.Context, unit contract.Unit, sourceID, occurrenceKey string) (*taskRow, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM tasks_tasks WHERE source_id = ? AND occurrence_key = ?`,
		sourceID, occurrenceKey)
	return scanTask(row)
}

// insertTask persists a new task row at version 1.
func insertTask(ctx context.Context, unit contract.Unit, r *taskRow) error {
	cancellation, manual := flagInts(r.CancellationRequested, r.ManualAcceptance)
	_, err := unit.ExecContext(ctx, `INSERT INTO tasks_tasks
		(id, version, installation_id, organization_id, scope_json, owner_id, worker_id, parent_id, root_id,
		 outcome, inputs_json, required_outputs_json, acceptance_json, acceptance_digest,
		 limits_json, dependencies_json, state, prior_state, waiting_reason,
		 cancellation_requested, manual_acceptance, established_by, attempt,
		 source_id, occurrence_key, content_digest, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), r.Version, string(r.InstallationID), string(r.OrganizationID), r.ScopeJSON, string(r.OwnerID),
		string(r.WorkerID), string(r.ParentID), string(r.RootID), r.Outcome,
		r.InputsJSON, r.RequiredOutputsJSON, r.AcceptanceJSON, r.AcceptanceDigest,
		r.LimitsJSON, r.DependenciesJSON, r.State, r.PriorState, r.WaitingReason,
		cancellation, manual, r.EstablishedBy, r.Attempt,
		r.SourceID, r.OccurrenceKey, r.ContentDigest,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// updateTaskState writes the mutable task fields after a transition or
// assignment. Acceptance, inputs, scope and identity never change here.
func updateTaskState(ctx context.Context, unit contract.Unit, r *taskRow) error {
	cancellation, manual := flagInts(r.CancellationRequested, r.ManualAcceptance)
	_, err := unit.ExecContext(ctx, `UPDATE tasks_tasks SET
		version = ?, worker_id = ?, state = ?, prior_state = ?, waiting_reason = ?,
		cancellation_requested = ?, manual_acceptance = ?, established_by = ?,
		attempt = ?, updated_at = ?
		WHERE id = ?`,
		r.Version, string(r.WorkerID), r.State, r.PriorState, r.WaitingReason,
		cancellation, manual, r.EstablishedBy, r.Attempt,
		r.UpdatedAt.Format(timeLayout), string(r.ID))
	return err
}

// updateTaskContract rewrites the pending contract fields for task.update.
// Allowed only while the task is pending (draft or ready); the accepted
// verifier never changes.
func updateTaskContract(ctx context.Context, unit contract.Unit, r *taskRow) error {
	_, err := unit.ExecContext(ctx, `UPDATE tasks_tasks SET
		version = ?, outcome = ?, inputs_json = ?, updated_at = ?
		WHERE id = ?`,
		r.Version, r.Outcome, r.InputsJSON, r.UpdatedAt.Format(timeLayout), string(r.ID))
	return err
}

func flagInts(a, b bool) (int64, int64) {
	var x, y int64
	if a {
		x = 1
	}
	if b {
		y = 1
	}
	return x, y
}

// dependency helpers ---------------------------------------------------------

func insertDependency(ctx context.Context, unit contract.Unit, taskID, dependsOn, installation contract.ID, at time.Time) error {
	_, err := unit.ExecContext(ctx,
		`INSERT INTO tasks_dependencies (task_id, depends_on_id, installation_id, created_at)
		 VALUES (?, ?, ?, ?)`,
		string(taskID), string(dependsOn), string(installation), at.Format(timeLayout))
	return err
}

// dependencyIDs returns the declared dependency ids of a task.
func dependencyIDs(ctx context.Context, unit contract.Unit, taskID contract.ID) ([]contract.ID, error) {
	rows, err := unit.QueryContext(ctx,
		`SELECT depends_on_id FROM tasks_dependencies WHERE task_id = ? ORDER BY depends_on_id`,
		string(taskID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []contract.ID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, contract.ID(id))
	}
	return out, rows.Err()
}

// dependencyReaches reports whether following dependency edges from start
// reaches target, walking bounded depth-first. It detects the cycles an
// insert would create before the edge exists.
func dependencyReaches(ctx context.Context, unit contract.Unit, start, target contract.ID) (bool, error) {
	seen := map[contract.ID]bool{}
	stack := []contract.ID{start}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[current] {
			continue
		}
		seen[current] = true
		if current == target {
			return true, nil
		}
		deps, err := dependencyIDs(ctx, unit, current)
		if err != nil {
			return false, err
		}
		stack = append(stack, deps...)
	}
	return false, nil
}

// countChildren counts direct children of a parent.
func countChildren(ctx context.Context, unit contract.Unit, parentID contract.ID) (int64, error) {
	var n int64
	err := unit.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks_tasks WHERE parent_id = ?`, string(parentID)).Scan(&n)
	return n, err
}

// listTasks applies a structured filter. Fields are exact matches combined
// with AND; filter values never interpolate into SQL. A DescendantOf anchor
// restricts membership to the anchor's subtree through a recursive CTE;
// UNION deduplicates nodes, so an accidental cycle cannot loop forever.
func listTasks(ctx context.Context, unit contract.Unit, installation contract.ID, f listFilter) ([]*taskRow, error) {
	var where []string
	var args []any
	prefix := ""
	if f.DescendantOf != "" {
		prefix = `WITH RECURSIVE task_descendants(id) AS (
			SELECT depends_on_id FROM tasks_dependencies WHERE task_id = ?
			UNION
			SELECT d.depends_on_id FROM tasks_dependencies d
			JOIN task_descendants t ON d.task_id = t.id
		) `
		// The CTE placeholder precedes the main WHERE placeholders in the
		// composed SQL text; bind its anchor argument first.
		where = append(where, "id IN (SELECT id FROM task_descendants)")
		args = append(args, string(f.DescendantOf))
	}
	where = append(where, "installation_id = ?")
	args = append(args, string(installation))
	if f.State != "" {
		where = append(where, "state = ?")
		args = append(args, f.State)
	}
	if f.ParentID != "" {
		where = append(where, "parent_id = ?")
		args = append(args, string(f.ParentID))
	}
	if f.WorkerID != "" {
		where = append(where, "worker_id = ?")
		args = append(args, string(f.WorkerID))
	}
	if f.TaskID != "" {
		where = append(where, "id = ?")
		args = append(args, string(f.TaskID))
	}
	if f.OrganizationID != "" {
		where = append(where, "organization_id = ?")
		args = append(args, string(f.OrganizationID))
	}
	if f.RootID != "" {
		where = append(where, "root_id = ?")
		args = append(args, string(f.RootID))
	}
	query := prefix + `SELECT ` + taskColumns + ` FROM tasks_tasks WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY created_at, id LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)
	rows, err := unit.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*taskRow
	for rows.Next() {
		r, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// listReady returns ready tasks oldest-first for the recovery scan.
func listReady(ctx context.Context, unit contract.Unit, installation contract.ID, limit int) ([]*taskRow, error) {
	rows, err := unit.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM tasks_tasks
		 WHERE installation_id = ? AND state = ? AND cancellation_requested = 0
		 ORDER BY updated_at, id LIMIT ?`,
		string(installation), stateReady, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*taskRow
	for rows.Next() {
		r, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// evidence helpers -----------------------------------------------------------

func insertEvidence(ctx context.Context, unit contract.Unit, r *evidenceRow) error {
	_, err := unit.ExecContext(ctx,
		`INSERT INTO tasks_evidence (task_id, artifact_id, installation_id, digest, media_type, attempt, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(r.TaskID), string(r.ArtifactID), string(r.Installation), r.Digest, r.MediaType,
		r.Attempt, r.RecordedAt.Format(timeLayout))
	return err
}

// evidenceForAttempt returns the recorded evidence of one attempt.
func evidenceForAttempt(ctx context.Context, unit contract.Unit, taskID contract.ID, attempt int64) ([]evidenceRow, error) {
	rows, err := unit.QueryContext(ctx,
		`SELECT task_id, artifact_id, installation_id, digest, media_type, attempt, recorded_at
		 FROM tasks_evidence WHERE task_id = ? AND attempt = ? ORDER BY artifact_id`,
		string(taskID), attempt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []evidenceRow
	for rows.Next() {
		var r evidenceRow
		var recorded string
		if err := rows.Scan(&r.TaskID, &r.ArtifactID, &r.Installation, &r.Digest, &r.MediaType,
			&r.Attempt, &recorded); err != nil {
			return nil, err
		}
		if r.RecordedAt, err = time.Parse(timeLayout, recorded); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func insertDecision(ctx context.Context, unit contract.Unit, r *decisionRow) error {
	_, err := unit.ExecContext(ctx,
		`INSERT INTO tasks_manual_decisions (id, task_id, installation_id, decision, reviewer_id, action_digest, reason, decided_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), string(r.TaskID), string(r.Installation), r.Decision, string(r.ReviewerID),
		r.ActionDigest, r.Reason, r.DecidedAt.Format(timeLayout))
	return err
}

func insertTransition(ctx context.Context, unit contract.Unit, r *transitionRow) error {
	manual := 0
	if r.Manual {
		manual = 1
	}
	_, err := unit.ExecContext(ctx,
		`INSERT INTO tasks_transitions (id, task_id, installation_id, from_state, to_state, evidence_ids_json, waiting_reason, manual, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), string(r.TaskID), string(r.Installation), r.FromState, r.ToState,
		r.EvidenceJSON, r.WaitingReason, manual, r.RecordedAt.Format(timeLayout))
	return err
}

// row conversion -------------------------------------------------------------

// decodeScope parses the stored scope JSON.
func (r *taskRow) decodeScope() (wireScope, error) {
	var sc wireScope
	if err := json.Unmarshal([]byte(r.ScopeJSON), &sc); err != nil {
		return sc, internalError("stored scope decoding failed: %v", err)
	}
	return sc, nil
}

// decodeAcceptance parses the stored acceptance contract.
func (r *taskRow) decodeAcceptance() (*wireAcceptance, error) {
	var a wireAcceptance
	if err := json.Unmarshal([]byte(r.AcceptanceJSON), &a); err != nil {
		return nil, internalError("stored acceptance decoding failed: %v", err)
	}
	return &a, nil
}

// decodeLimits parses the stored limits envelope.
func (r *taskRow) decodeLimits() (wireLimits, error) {
	var l wireLimits
	if err := json.Unmarshal([]byte(r.LimitsJSON), &l); err != nil {
		return l, internalError("stored limits decoding failed: %v", err)
	}
	return l, nil
}

// decodeDependencies parses the declared dependency ids.
func (r *taskRow) decodeDependencies() ([]contract.ID, error) {
	var deps []contract.ID
	if err := json.Unmarshal([]byte(r.DependenciesJSON), &deps); err != nil {
		return nil, internalError("stored dependencies decoding failed: %v", err)
	}
	return deps, nil
}

// decodeInputs parses the pinned input artifact references.
func (r *taskRow) decodeInputs() ([]wireArtifactRef, error) {
	var refs []wireArtifactRef
	if err := json.Unmarshal([]byte(r.InputsJSON), &refs); err != nil {
		return nil, internalError("stored inputs decoding failed: %v", err)
	}
	return refs, nil
}

// decodeRequiredOutputs parses the declared output names.
func (r *taskRow) decodeRequiredOutputs() ([]string, error) {
	var names []string
	if err := json.Unmarshal([]byte(r.RequiredOutputsJSON), &names); err != nil {
		return nil, internalError("stored required outputs decoding failed: %v", err)
	}
	return names, nil
}

// toWire converts the stored row into its wire representation.
func (r *taskRow) toWire() (*wireTask, error) {
	sc, err := r.decodeScope()
	if err != nil {
		return nil, err
	}
	acceptance, err := r.decodeAcceptance()
	if err != nil {
		return nil, err
	}
	limits, err := r.decodeLimits()
	if err != nil {
		return nil, err
	}
	inputs, err := r.decodeInputs()
	if err != nil {
		return nil, err
	}
	outputs, err := r.decodeRequiredOutputs()
	if err != nil {
		return nil, err
	}
	deps, err := r.decodeDependencies()
	if err != nil {
		return nil, err
	}
	if deps == nil {
		deps = []contract.ID{}
	}
	if inputs == nil {
		inputs = []wireArtifactRef{}
	}
	if outputs == nil {
		outputs = []string{}
	}
	if acceptance.SealedInputs == nil {
		acceptance.SealedInputs = []wireArtifactRef{}
	}
	if acceptance.ExpectedObservations == nil {
		acceptance.ExpectedObservations = []wireExpectedObservation{}
	}
	if acceptance.RequiredChildIDs == nil {
		acceptance.RequiredChildIDs = []contract.ID{}
	}
	return &wireTask{
		ID:                    r.ID,
		Version:               contract.Version(r.Version),
		Scope:                 sc,
		OwnerID:               r.OwnerID,
		WorkerID:              r.WorkerID,
		Outcome:               r.Outcome,
		Inputs:                inputs,
		RequiredOutputs:       outputs,
		Acceptance:            *acceptance,
		Limits:                limits,
		Dependencies:          deps,
		State:                 r.State,
		ParentID:              r.ParentID,
		RootID:                r.RootID,
		WaitingReason:         r.WaitingReason,
		CancellationRequested: r.CancellationRequested,
		ManualAcceptance:      r.ManualAcceptance,
	}, nil
}

// limitOf normalizes the list limit: default 50, maximum 200.
func limitOf(limit int64) int {
	switch {
	case limit <= 0:
		return 50
	case limit > 200:
		return 200
	default:
		return int(limit)
	}
}
