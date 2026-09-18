package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Exact local fakes of the owners the controller calls. Each follows the
// frozen contract for its operation: version and generation fences, one-use
// claims, idempotent replays, and the documented refusals.

const fxDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// ---- effects ----

// action builds a schema-valid immutable action.
func (f *fx) action(parameters map[string]any, connection contract.ID) json.RawMessage {
	if parameters == nil {
		parameters = map[string]any{}
	}
	if connection == "" {
		connection = contract.NewID()
	}
	raw, _ := json.Marshal(map[string]any{
		"scope":                  f.scope(),
		"tool":                   map[string]any{"id": contract.NewID(), "version": 1},
		"connection":             map[string]any{"id": connection, "version": 3},
		"account_identity":       "synthetic-account",
		"destination":            "https://provider.invalid/v1",
		"content":                []any{},
		"not_before":             f.clock.Now().Format(time.RFC3339Nano),
		"expires_at":             f.clock.Now().Add(time.Hour).Format(time.RFC3339Nano),
		"preconditions":          map[string]any{},
		"configuration_revision": 1,
		"parameters":             parameters,
		"cost_bound":             map[string]any{"currency": "USD", "micro_units": 5000},
	})
	return raw
}

// prepare commits one prepared operation, as a domain owner would through
// _effects.prepare.
func (f *fx) prepare(adapter string, parameters map[string]any) contract.ID {
	id := contract.NewID()
	f.exec(`INSERT INTO effects_operations (id, version, state, action, adapter) VALUES (?, 1, 'prepared', ?, ?)`,
		string(id), string(f.action(parameters, "")), adapter)
	return id
}

func (f *fx) opState(id contract.ID) string {
	return f.queryString(`SELECT state FROM effects_operations WHERE id = ?`, string(id))
}

func (f *fx) attempts(id contract.ID) int64 {
	return f.queryInt(`SELECT COUNT(*) FROM effects_attempts WHERE operation_id = ?`, string(id))
}

func (f *fx) observations(id contract.ID) []string {
	f.t.Helper()
	ctx := context.Background()
	var out []string
	err := f.raw.Read(ctx, f.actor, f.scope(), func(u contract.Unit) error {
		rows, err := u.QueryContext(ctx, `SELECT kind || ':' || disposition FROM effects_observations WHERE operation_id = ? ORDER BY seq`, string(id))
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		f.t.Fatalf("observations: %v", err)
	}
	return out
}

type fxOperation struct {
	id, state, action, adapter, admitMode string
	version                               int64
}

func loadFxOperation(ctx context.Context, u contract.Unit, id contract.ID) (*fxOperation, error) {
	o := &fxOperation{id: string(id)}
	err := u.QueryRowContext(ctx, `SELECT version, state, action, adapter, admit_mode FROM effects_operations WHERE id = ?`, string(id)).
		Scan(&o.version, &o.state, &o.action, &o.adapter, &o.admitMode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodeNotFound, "operation not found")
	}
	return o, err
}

func (o *fxOperation) transition(ctx context.Context, u contract.Unit, state string) error {
	o.state = state
	o.version++
	_, err := u.ExecContext(ctx, `UPDATE effects_operations SET state = ?, version = ? WHERE id = ?`, state, o.version, o.id)
	return err
}

func (o *fxOperation) wire(ctx context.Context, u contract.Unit) (map[string]any, error) {
	rows, err := u.QueryContext(ctx, `SELECT id FROM effects_attempts WHERE operation_id = ? ORDER BY seq`, o.id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return map[string]any{
		"id": o.id, "version": o.version, "action": json.RawMessage(o.action),
		"action_digest": fxDigest, "state": o.state, "attempt_ids": ids,
	}, rows.Err()
}

func (f *fx) effectsPending(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in limitInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	rows, err := u.QueryContext(ctx, `SELECT id FROM effects_operations
		WHERE state IN ('prepared','awaiting_review','awaiting_confirmation','outcome_unknown') ORDER BY seq LIMIT ?`, in.Limit)
	if err != nil {
		return nil, err
	}
	var ids []contract.ID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, contract.ID(id))
	}
	_ = rows.Close()
	ops := []map[string]any{}
	for _, id := range ids {
		o, err := loadFxOperation(ctx, u, id)
		if err != nil {
			return nil, err
		}
		w, err := o.wire(ctx, u)
		if err != nil {
			return nil, err
		}
		ops = append(ops, w)
	}
	return map[string]any{"operations": ops}, nil
}

func (f *fx) effectsAdmit(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in effectsAdmitInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	o, err := loadFxOperation(ctx, u, in.OperationID)
	if err != nil {
		return nil, err
	}
	if o.version != in.ExpectedVersion {
		return nil, fxFault(contract.CodeStaleVersion, "operation version moved")
	}
	if o.state != "prepared" && o.state != "awaiting_review" {
		return nil, fxFault(contract.CodeConflict, "operation is not admissible")
	}
	switch o.admitMode {
	case "review":
		return nil, fxFault(contract.CodeReviewRequired, "review is not satisfied")
	case "deny":
		if err := o.transition(ctx, u, "denied"); err != nil {
			return nil, err
		}
		w, err := o.wire(ctx, u)
		return map[string]any{"resource": w}, err
	}
	_, err = u.ExecContext(ctx, `INSERT INTO effects_attempts (id, operation_id, generation, state, consumed) VALUES (?, ?, ?, 'prepared', 0)`,
		string(contract.NewID()), o.id, u.Generation())
	if err != nil {
		return nil, err
	}
	if err := o.transition(ctx, u, "ready"); err != nil {
		return nil, err
	}
	w, err := o.wire(ctx, u)
	return map[string]any{"resource": w}, err
}

type fxAttempt struct {
	id, state  string
	generation int64
	consumed   int
}

func loadFxAttempt(ctx context.Context, u contract.Unit, operation, attempt contract.ID) (*fxAttempt, error) {
	a := &fxAttempt{id: string(attempt)}
	err := u.QueryRowContext(ctx, `SELECT generation, state, consumed FROM effects_attempts WHERE id = ? AND operation_id = ?`,
		string(attempt), string(operation)).Scan(&a.generation, &a.state, &a.consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodeNotFound, "attempt not found")
	}
	return a, err
}

func (f *fx) effectsClaim(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in effectsClaimInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	o, err := loadFxOperation(ctx, u, in.OperationID)
	if err != nil {
		return nil, err
	}
	a, err := loadFxAttempt(ctx, u, in.OperationID, in.AttemptID)
	if err != nil {
		return nil, err
	}
	if in.Generation != a.generation || a.generation != u.Generation() {
		return nil, fxFault(contract.CodeConflict, "dispatch generation is fenced")
	}
	if o.state != "ready" || a.state != "prepared" || a.consumed != 0 {
		return nil, fxFault(contract.CodeConflict, "dispatch claim is consumed or absent")
	}
	if _, err := u.ExecContext(ctx, `UPDATE effects_attempts SET state = 'claimed', consumed = 1 WHERE id = ? AND consumed = 0`, a.id); err != nil {
		return nil, err
	}
	if err := o.transition(ctx, u, "executing"); err != nil {
		return nil, err
	}
	return map[string]any{"resource": map[string]any{
		"operation_id": o.id, "attempt_id": a.id, "generation": a.generation, "adapter": o.adapter,
		"action": json.RawMessage(o.action), "credential_ref": "secret-ref-synthetic",
		"deadline": f.clock.Now().Add(time.Hour).Format(time.RFC3339Nano),
	}}, nil
}

func (f *fx) effectsRecord(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in effectsRecordInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	o, err := loadFxOperation(ctx, u, in.OperationID)
	if err != nil {
		return nil, err
	}
	a, err := loadFxAttempt(ctx, u, in.OperationID, in.AttemptID)
	if err != nil {
		return nil, err
	}
	if in.Generation != a.generation {
		return nil, fxFault(contract.CodeConflict, "record generation does not match the attempt")
	}
	var prior string
	err = u.QueryRowContext(ctx, `SELECT disposition FROM effects_observations WHERE attempt_id = ? ORDER BY seq LIMIT 1`, a.id).Scan(&prior)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	insert := func(kind string) error {
		_, err := u.ExecContext(ctx, `INSERT INTO effects_observations (operation_id, attempt_id, kind, disposition, evidence, usage) VALUES (?, ?, ?, ?, ?, ?)`,
			o.id, a.id, kind, in.Observation.Disposition, string(in.Observation.Evidence), string(in.Observation.Usage))
		return err
	}
	if prior != "" {
		if prior != in.Observation.Disposition {
			if err := insert("dispute"); err != nil {
				return nil, err
			}
		}
		w, err := o.wire(ctx, u)
		return map[string]any{"resource": w}, err
	}
	if o.state != "ready" && o.state != "executing" {
		return nil, fxFault(contract.CodeConflict, "operation cannot record fresh evidence")
	}
	claimed := a.state == "claimed"
	if err := insert("physical"); err != nil {
		return nil, err
	}
	if _, err := u.ExecContext(ctx, `UPDATE effects_attempts SET state = 'recorded' WHERE id = ?`, a.id); err != nil {
		return nil, err
	}
	next := map[string]string{
		contract.DispositionSucceeded: "succeeded",
		contract.DispositionAccepted:  "awaiting_confirmation",
		contract.DispositionFailed:    "failed",
		contract.DispositionUnknown:   "outcome_unknown",
	}[in.Observation.Disposition]
	if in.Observation.Disposition == contract.DispositionNotSent {
		next = "failed"
		if claimed {
			// Once claimed, nothing proves non-execution.
			next = "outcome_unknown"
		}
	}
	if err := o.transition(ctx, u, next); err != nil {
		return nil, err
	}
	w, err := o.wire(ctx, u)
	return map[string]any{"resource": w}, err
}

// ---- execution ----

func (f *fx) executionFence(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in fenceInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	_, err := u.ExecContext(ctx, `INSERT INTO execution_fences (generation, reason) VALUES (?, ?)`, in.Generation, in.Reason)
	return map[string]any{"attempt_ids": []string{}}, err
}

func (f *fx) executionTick(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in struct {
		Now   string `json:"now"`
		Limit int64  `json:"limit"`
	}
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	_, err := u.ExecContext(ctx, `INSERT INTO execution_ticks (now) VALUES (?)`, in.Now)
	return map[string]any{"attempt_ids": []string{}}, err
}

func (f *fx) attemptResource(id contract.ID) map[string]any {
	stamp := f.clock.Now().Format(time.RFC3339Nano)
	return map[string]any{
		"id": id, "version": 2, "run_id": contract.NewID(), "worker_id": contract.NewID(), "executor": "hosted",
		"generation": 1, "lease_id": contract.NewID(), "lease_expires_at": stamp, "last_heartbeat": stamp,
		"reservation_id": contract.NewID(), "state": "running", "capabilities": []string{},
	}
}

func (f *fx) executionObservation(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in executionObservationInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	var n int
	if err := u.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_observations WHERE operation_id = ?`, string(in.OperationID)).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, fxFault(contract.CodeConflict, "operation has no pending observation")
	}
	_, err := u.ExecContext(ctx, `INSERT INTO execution_observations (operation_id, attempt_id, disposition, evidence) VALUES (?, ?, ?, ?)`,
		string(in.OperationID), string(in.AttemptID), in.Observation.Disposition, string(in.Observation.Evidence))
	return map[string]any{"resource": f.attemptResource(in.AttemptID)}, err
}

// job commits one durable job as an owner would through _execution.job.create.
func (f *fx) job(owner, operation string, waitsOn contract.ID) contract.ID {
	id := contract.NewID()
	f.exec(`INSERT INTO execution_jobs (id, version, state, owner, operation, operation_id, input) VALUES (?, 1, 'pending', ?, ?, ?, ?)`,
		string(id), owner, operation, string(waitsOn), `{"subject":"synthetic"}`)
	return id
}

func (f *fx) jobState(id contract.ID) string {
	return f.queryString(`SELECT state FROM execution_jobs WHERE id = ?`, string(id))
}

type fxJob struct {
	id, state, owner, operation, operationID, input, result string
	version, claimed                                        int64
}

func loadFxJob(ctx context.Context, u contract.Unit, id contract.ID) (*fxJob, error) {
	j := &fxJob{id: string(id)}
	err := u.QueryRowContext(ctx, `SELECT version, state, owner, operation, operation_id, input, claimed_generation, result
		FROM execution_jobs WHERE id = ?`, string(id)).
		Scan(&j.version, &j.state, &j.owner, &j.operation, &j.operationID, &j.input, &j.claimed, &j.result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodeNotFound, "job not found")
	}
	return j, err
}

func (j *fxJob) wire() map[string]any {
	w := map[string]any{
		"id": j.id, "version": j.version, "kind": "operation", "state": j.state,
		"requirements": []any{}, "owner": j.owner, "operation": j.operation,
	}
	if j.operationID != "" {
		w["operation_id"] = j.operationID
	}
	if j.result != "" {
		w["result"] = json.RawMessage(j.result)
	}
	return w
}

func (j *fxJob) save(ctx context.Context, u contract.Unit) error {
	j.version++
	_, err := u.ExecContext(ctx, `UPDATE execution_jobs SET version = ?, state = ?, claimed_generation = ?, result = ? WHERE id = ?`,
		j.version, j.state, j.claimed, j.result, j.id)
	return err
}

func (f *fx) jobPending(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in limitInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	rows, err := u.QueryContext(ctx, `SELECT id FROM execution_jobs WHERE state IN ('pending','outcome_unknown') ORDER BY seq LIMIT ?`, in.Limit)
	if err != nil {
		return nil, err
	}
	var ids []contract.ID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, contract.ID(id))
	}
	_ = rows.Close()
	items := []map[string]any{}
	for _, id := range ids {
		j, err := loadFxJob(ctx, u, id)
		if err != nil {
			return nil, err
		}
		items = append(items, j.wire())
	}
	return map[string]any{"items": items}, nil
}

func (f *fx) jobClaim(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in jobClaimInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	j, err := loadFxJob(ctx, u, in.JobID)
	if err != nil {
		return nil, err
	}
	switch {
	case j.state == "running" && j.claimed == in.Generation:
		return map[string]any{"job": j.wire(), "input": json.RawMessage(j.input)}, nil
	case j.state == "running":
		return nil, fxFault(contract.CodeConflict, "job is claimed by another generation")
	case j.state != "pending":
		return nil, fxFault(contract.CodeConflict, "job is not claimable")
	case j.version != in.ExpectedVersion:
		return nil, fxFault(contract.CodeStaleVersion, "job version moved")
	}
	j.state, j.claimed = "running", in.Generation
	if err := j.save(ctx, u); err != nil {
		return nil, err
	}
	return map[string]any{"job": j.wire(), "input": json.RawMessage(j.input)}, nil
}

func (f *fx) jobRecord(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in jobRecordInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	j, err := loadFxJob(ctx, u, in.JobID)
	if err != nil {
		return nil, err
	}
	if j.version != in.ExpectedVersion {
		return nil, fxFault(contract.CodeStaleVersion, "job version moved")
	}
	if j.claimed != in.Generation {
		return nil, fxFault(contract.CodeConflict, "job is owned by another generation")
	}
	if j.state != "running" {
		return nil, fxFault(contract.CodeConflict, "job is already "+j.state)
	}
	j.state, j.result = in.State, string(in.Result)
	if err := j.save(ctx, u); err != nil {
		return nil, err
	}
	return map[string]any{"resource": j.wire()}, nil
}

// ---- scheduling ----

// wake commits one durable due wake.
func (f *fx) wake(occurrence string, due time.Time) contract.ID {
	id := contract.NewID()
	f.exec(`INSERT INTO scheduling_wakes (id, source_id, occurrence_key, due_at) VALUES (?, ?, ?, ?)`,
		string(id), string(contract.NewID()), occurrence, due.UTC().Format(time.RFC3339Nano))
	return id
}

func (f *fx) wakeDue(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in struct {
		Now   time.Time `json:"now"`
		Limit int64     `json:"limit"`
	}
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	rows, err := u.QueryContext(ctx, `SELECT id, source_id, occurrence_key, due_at FROM scheduling_wakes WHERE admitted = 0 ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	wakes := []map[string]any{}
	for rows.Next() {
		var id, source, key, due string
		if err := rows.Scan(&id, &source, &key, &due); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, due)
		if err != nil {
			return nil, err
		}
		if at.After(in.Now) || int64(len(wakes)) >= in.Limit {
			continue
		}
		wakes = append(wakes, map[string]any{
			"id": id, "scope": f.scope(), "source_id": source, "occurrence_key": key, "due_at": due, "condition_version": 1,
		})
	}
	return map[string]any{"wakes": wakes}, rows.Err()
}

// wakeAdmit deduplicates the occurrence, admits one cycle and persists the
// next wake in the same transaction.
func (f *fx) wakeAdmit(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in struct {
		Wake struct {
			ID            contract.ID     `json:"id"`
			Scope         json.RawMessage `json:"scope"`
			SourceID      contract.ID     `json:"source_id"`
			OccurrenceKey string          `json:"occurrence_key"`
			DueAt         time.Time       `json:"due_at"`
			Condition     int64           `json:"condition_version"`
		} `json:"wake"`
	}
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	var admitted int
	err := u.QueryRowContext(ctx, `SELECT admitted FROM scheduling_wakes WHERE id = ?`, string(in.Wake.ID)).Scan(&admitted)
	if errors.Is(err, sql.ErrNoRows) || admitted == 1 {
		return map[string]any{"skipped": true}, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := u.ExecContext(ctx, `UPDATE scheduling_wakes SET admitted = 1 WHERE id = ?`, string(in.Wake.ID)); err != nil {
		return nil, err
	}
	var seen int
	if err := u.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduling_cycles WHERE occurrence_key = ?`, in.Wake.OccurrenceKey).Scan(&seen); err != nil {
		return nil, err
	}
	if seen > 0 {
		return map[string]any{"skipped": true}, nil
	}
	if _, err := u.ExecContext(ctx, `INSERT INTO scheduling_cycles (occurrence_key, wake_id) VALUES (?, ?)`, in.Wake.OccurrenceKey, string(in.Wake.ID)); err != nil {
		return nil, err
	}
	next := in.Wake.DueAt.Add(time.Hour)
	_, err = u.ExecContext(ctx, `INSERT INTO scheduling_wakes (id, source_id, occurrence_key, due_at) VALUES (?, ?, ?, ?)`,
		string(contract.NewID()), string(in.Wake.SourceID), strings.TrimSpace(in.Wake.OccurrenceKey)+"+1h", next.UTC().Format(time.RFC3339Nano))
	return map[string]any{"skipped": false}, err
}

// ---- callbacks ----

func (f *fx) jobResource(id contract.ID, owner, operation, state string) map[string]any {
	return map[string]any{
		"id": id, "version": 2, "kind": "operation", "state": state, "requirements": []any{}, "owner": owner, "operation": operation,
	}
}

func (f *fx) memoryRecord(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in memoryRecordInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	_, err := u.ExecContext(ctx, `INSERT INTO memory_records (operation_id, job_id, disposition, evidence) VALUES (?, ?, ?, ?)`,
		string(in.OperationID), string(in.JobID), in.Observation.Disposition, string(in.Observation.Evidence))
	return map[string]any{"resource": f.jobResource(in.JobID, "memory", "memory.remember", "succeeded")}, err
}

func (f *fx) validationRecord(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in validationRecordInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	_, err := u.ExecContext(ctx, `INSERT INTO connections_validations (connection_id, expected_version, disposition) VALUES (?, ?, ?)`,
		string(in.ConnectionID), in.ExpectedVersion, in.Observation.Disposition)
	return map[string]any{"resource": map[string]any{
		"id": in.ConnectionID, "version": in.ExpectedVersion + 1, "scope": f.scope(), "provider": "synthetic",
		"account_identity": "synthetic-account", "credential_ref": "secret-ref-synthetic",
		"destinations": []string{}, "allowed_scopes": []string{}, "validation_state": "valid",
	}}, err
}

func (f *fx) artifactsPublish(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in artifactsPublishInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	id := contract.NewID()
	scope, _ := json.Marshal(in.Scope)
	_, err := u.ExecContext(ctx, `INSERT INTO artifacts_items (id, digest, size, media_type, classification, scope) VALUES (?, ?, ?, ?, ?, ?)`,
		string(id), string(in.Digest), in.Size, in.MediaType, in.Classification, string(scope))
	return map[string]any{"resource": map[string]any{
		"id": id, "version": 1, "scope": in.Scope, "digest": in.Digest, "size": in.Size, "media_type": in.MediaType,
		"classification": in.Classification, "encrypted": in.Encrypted, "state": "available",
		"created_at": f.clock.Now().Format(time.RFC3339Nano),
	}}, err
}

func (f *fx) restoreRecord(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in restoreRecordInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	reqs, _ := json.Marshal(in.Requirements)
	_, err := u.ExecContext(ctx, `INSERT INTO installation_restores (job_id, state, requirements) VALUES (?, ?, ?)`,
		string(in.JobID), in.State, string(reqs))
	return map[string]any{"resource": f.jobResource(in.JobID, restoreOwner, restoreOperation, in.State)}, err
}

func (f *fx) tasksReady(_ context.Context, _ contract.Unit, input json.RawMessage) (any, error) {
	var in limitInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	f.mu.Lock()
	n := f.ready
	f.mu.Unlock()
	items := []map[string]any{}
	for i := 0; i < n; i++ {
		items = append(items, map[string]any{"id": contract.NewID()})
	}
	return map[string]any{"items": items}, nil
}

// ---- providers ----

// fakeAdapter is a controlled provider that counts physical invocations.
type fakeAdapter struct {
	name string
	fx   *fx

	mu         chan struct{}
	invoked    []contract.Dispatch
	reconciled int
	// reply, when set, produces the observation.
	reply func(ctx context.Context, d contract.Dispatch) (contract.Observation, error)
}

func (f *fx) adapter(name string) *fakeAdapter {
	a := &fakeAdapter{name: name, fx: f, mu: make(chan struct{}, 1)}
	f.adapters[name] = a
	return a
}

func (a *fakeAdapter) Name() string              { return a.name }
func (a *fakeAdapter) Contract() json.RawMessage { return json.RawMessage(`{}`) }

func (a *fakeAdapter) Invoke(ctx context.Context, d contract.Dispatch) (contract.Observation, error) {
	a.mu <- struct{}{}
	a.invoked = append(a.invoked, d)
	reply := a.reply
	<-a.mu
	if reply != nil {
		return reply(ctx, d)
	}
	return succeeded(nil), nil
}

func (a *fakeAdapter) Reconcile(context.Context, contract.Dispatch) (contract.Observation, error) {
	a.mu <- struct{}{}
	a.reconciled++
	<-a.mu
	return contract.Observation{}, errors.New("reconcile is never called by the controller")
}

func (a *fakeAdapter) calls() int {
	a.mu <- struct{}{}
	defer func() { <-a.mu }()
	return len(a.invoked)
}

func (a *fakeAdapter) reconciles() int {
	a.mu <- struct{}{}
	defer func() { <-a.mu }()
	return a.reconciled
}

const fxUsage = `{"currency":"USD","spent":1200,"reserved":0,"estimated":0,"unknown":0,"advisory":false}`

func succeeded(evidence map[string]any) contract.Observation {
	if evidence == nil {
		evidence = map[string]any{"schema": "zatiti.synthetic.evidence/v1"}
	}
	raw, _ := json.Marshal(evidence)
	return contract.Observation{
		Disposition: contract.DispositionSucceeded, ProviderReference: "provider-ref-1",
		Evidence: raw, Usage: json.RawMessage(fxUsage),
	}
}

// fakeBlobs records publications.
type fakeBlobs struct {
	contract.BlobStore
	mu        chan struct{}
	published []string
	fail      error
}

func newFakeBlobs() *fakeBlobs { return &fakeBlobs{mu: make(chan struct{}, 1)} }

func (b *fakeBlobs) Publish(_ context.Context, ref string, _ contract.Digest) error {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()
	if b.fail != nil {
		return b.fail
	}
	b.published = append(b.published, ref)
	return nil
}

func (b *fakeBlobs) count() int {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()
	return len(b.published)
}
