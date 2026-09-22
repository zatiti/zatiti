package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
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
	return f.prepareRouted(adapter, parameters, "")
}

// prepareRouted commits one prepared operation carrying an explicit
// callback_route, exactly as _effects.prepare persists one alongside the
// action when a caller supplies it (P00-006).
func (f *fx) prepareRouted(adapter string, parameters map[string]any, callbackRoute string) contract.ID {
	id := contract.NewID()
	f.exec(`INSERT INTO effects_operations (id, version, state, action, adapter, callback_route) VALUES (?, 1, 'prepared', ?, ?, ?)`,
		string(id), string(f.action(parameters, "")), adapter, callbackRoute)
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
	id, state, action, adapter, admitMode, callbackRoute string
	version                                              int64
}

func loadFxOperation(ctx context.Context, u contract.Unit, id contract.ID) (*fxOperation, error) {
	o := &fxOperation{id: string(id)}
	err := u.QueryRowContext(ctx, `SELECT version, state, action, adapter, admit_mode, callback_route FROM effects_operations WHERE id = ?`, string(id)).
		Scan(&o.version, &o.state, &o.action, &o.adapter, &o.admitMode, &o.callbackRoute)
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
	w := map[string]any{
		"id": o.id, "version": o.version, "action": json.RawMessage(o.action),
		"action_digest": fxDigest, "state": o.state, "attempt_ids": ids,
	}
	if o.callbackRoute != "" {
		w["callback_route"] = json.RawMessage(o.callbackRoute)
	}
	return w, rows.Err()
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
	id, state, kind string
	generation      int64
	consumed        int
}

func loadFxAttempt(ctx context.Context, u contract.Unit, operation, attempt contract.ID) (*fxAttempt, error) {
	a := &fxAttempt{id: string(attempt)}
	err := u.QueryRowContext(ctx, `SELECT generation, state, consumed, kind FROM effects_attempts WHERE id = ? AND operation_id = ?`,
		string(attempt), string(operation)).Scan(&a.generation, &a.state, &a.consumed, &a.kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodeNotFound, "attempt not found")
	}
	return a, err
}

// effectsClaim consumes the one-use claim of either an ordinary dispatch
// attempt (requires the operation to be "ready", as a real admit already
// left it) or a reconciliation attempt (_effects.reconciliation.prepare's
// own attempt, admitted while the operation stays outcome_unknown/
// awaiting_confirmation) -- the same call, mirroring the real owner's own
// documented behavior: "The one-use claim this attempt gets is consumed
// through the ordinary _effects.claim, exactly like a dispatch attempt's;
// only the disposition rules at claim and record differ... reconciliation
// never advances the operation to executing."
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
	if a.state != "prepared" || a.consumed != 0 {
		return nil, fxFault(contract.CodeConflict, "dispatch claim is consumed or absent")
	}
	reconciling := a.kind == "reconciliation"
	if !reconciling && o.state != "ready" {
		return nil, fxFault(contract.CodeConflict, "dispatch claim is consumed or absent")
	}
	if reconciling {
		switch o.state {
		case "outcome_unknown", "awaiting_confirmation":
		default:
			return nil, fxFault(contract.CodeConflict, "operation is no longer reconcilable")
		}
	}
	if _, err := u.ExecContext(ctx, `UPDATE effects_attempts SET state = 'claimed', consumed = 1 WHERE id = ? AND consumed = 0`, a.id); err != nil {
		return nil, err
	}
	if !reconciling {
		if err := o.transition(ctx, u, "executing"); err != nil {
			return nil, err
		}
	}
	resource := map[string]any{
		"operation_id": o.id, "attempt_id": a.id, "generation": a.generation, "adapter": o.adapter,
		"action": json.RawMessage(o.action), "credential_ref": "secret-ref-synthetic",
		"deadline": f.clock.Now().Add(time.Hour).Format(time.RFC3339Nano),
	}
	if o.callbackRoute != "" {
		resource["callback_route"] = json.RawMessage(o.callbackRoute)
	}
	return map[string]any{"resource": resource}, nil
}

// effectsReconciliationPrepare admits a bounded reconciliation read: a new
// attempt (kind reconciliation) linked to an already-uncertain operation,
// which stays otherwise untouched -- no version bump, no state change --
// exactly matching the real owner's documented behavior.
func (f *fx) effectsReconciliationPrepare(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
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
	switch o.state {
	case "outcome_unknown", "awaiting_confirmation":
	default:
		return nil, fxFault(contract.CodeConflict, "operation %s is not uncertain; nothing to reconcile")
	}
	_, err = u.ExecContext(ctx, `INSERT INTO effects_attempts (id, operation_id, generation, state, consumed, kind) VALUES (?, ?, ?, 'prepared', 0, 'reconciliation')`,
		string(contract.NewID()), o.id, u.Generation())
	if err != nil {
		return nil, err
	}
	w, err := o.wire(ctx, u)
	return map[string]any{"resource": w}, err
}

// effectsReconciliationRecord merges the qualified reconciliation
// observation into the original operation's uncertainty: an authoritative
// disposition (succeeded/failed) settles the operation, matching a
// correction; unknown/not_sent is eventual-consistency evidence that
// leaves the operation's own state untouched. A duplicate callback for the
// same reconciliation attempt settles only once.
func (f *fx) effectsReconciliationRecord(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
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
	if a.kind != "reconciliation" {
		return nil, fxFault(contract.CodeConflict, "attempt %s is not a reconciliation read; use _effects.record")
	}
	if in.Generation != a.generation {
		return nil, fxFault(contract.CodeConflict, "record generation does not match the attempt")
	}
	var prior string
	err = u.QueryRowContext(ctx, `SELECT disposition FROM effects_observations WHERE attempt_id = ? ORDER BY seq LIMIT 1`, a.id).Scan(&prior)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if prior != "" {
		// Settles only once: the read already recorded its answer.
		w, err := o.wire(ctx, u)
		return map[string]any{"resource": w}, err
	}
	if _, err := u.ExecContext(ctx, `INSERT INTO effects_observations (operation_id, attempt_id, kind, disposition, evidence, usage) VALUES (?, ?, 'reconciliation', ?, ?, ?)`,
		o.id, a.id, in.Observation.Disposition, string(in.Observation.Evidence), string(in.Observation.Usage)); err != nil {
		return nil, err
	}
	next := map[string]string{
		contract.DispositionSucceeded: "succeeded",
		contract.DispositionFailed:    "failed",
	}[in.Observation.Disposition]
	if next != "" {
		if err := o.transition(ctx, u, next); err != nil {
			return nil, err
		}
	}
	w, err := o.wire(ctx, u)
	return map[string]any{"resource": w}, err
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
	if a.kind == "reconciliation" {
		return nil, fxFault(contract.CodeConflict, "attempt %s is a reconciliation read; use _effects.reconciliation.record")
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

// executionFence mirrors the real _execution.fence's own turn-fencing
// behavior (controller_ops.go, handleFence): a restart also fences every
// WorkerTurn claimed under a generation below the given one back to
// waiting/recovery with an immediate next_wake, so it becomes reachable
// again through the ordinary resume -> claimed -> context/proposal pipeline
// instead of being silently abandoned mid-flight.
func (f *fx) executionFence(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in fenceInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	if _, err := u.ExecContext(ctx, `INSERT INTO execution_fences (generation, reason) VALUES (?, ?)`, in.Generation, in.Reason); err != nil {
		return nil, err
	}
	now := f.clock.Now().Format(time.RFC3339Nano)
	if _, err := u.ExecContext(ctx, `UPDATE execution_turns SET state = 'waiting', next_wake = ?
		WHERE generation < ? AND state IN ('claimed', 'context_pending', 'model_pending', 'proposal_pending')`,
		now, in.Generation); err != nil {
		return nil, err
	}
	return map[string]any{"attempt_ids": []string{}}, nil
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
	var turnID string
	err := u.QueryRowContext(ctx, `SELECT id FROM execution_turns WHERE attempt_id = ?`, string(in.AttemptID)).Scan(&turnID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if turnID != "" {
		return f.interpretTurnObservation(ctx, u, contract.ID(turnID), in)
	}
	var n int
	if err := u.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_observations WHERE operation_id = ?`, string(in.OperationID)).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, fxFault(contract.CodeConflict, "operation has no pending observation")
	}
	_, err = u.ExecContext(ctx, `INSERT INTO execution_observations (operation_id, attempt_id, disposition, evidence) VALUES (?, ?, ?, ?)`,
		string(in.OperationID), string(in.AttemptID), in.Observation.Disposition, string(in.Observation.Evidence))
	return map[string]any{"resource": f.attemptResource(in.AttemptID)}, err
}

// fxToolProposal is the fake's own convenience shape for a controlled
// model response's tool_proposals[] entries. The real ModelToolProposal
// carries tool.id/operation_id and defers "what kind of decision is this"
// to matching against sealed local-decision-tool ids or the committed
// context plan's own resolved tool list (interpret.go); this fake, testing
// only the controller's own delivery/routing code and never execution's
// interpretation logic, names the kind directly.
type fxToolProposal struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Digest string `json:"result_digest,omitempty"`
}

// interpretTurnObservation is the fake's turn-aware branch of
// _execution.observation: given a controlled ModelOutput-shaped evidence
// (fxToolProposal entries under "tool_proposals"), it records each proposal
// and advances the turn/attempt exactly as P16's real interpretation stage
// would for the two dispositions this package's required tests exercise --
// reply (terminal, no outside-unit action) and report_outputs (reports the
// turn's attempt and admits a durable verification request).
func (f *fx) interpretTurnObservation(ctx context.Context, u contract.Unit, turnID contract.ID, in executionObservationInput) (any, error) {
	if in.Observation.Disposition != contract.DispositionSucceeded && in.Observation.Disposition != contract.DispositionAccepted {
		// Mirrors the real interpretTurnObservation's own first check
		// (internal/execution/interpret.go): an unconfirmed delivery
		// carries no model output to interpret, refused rather than
		// silently accepted or guessed at.
		return nil, fxFault(contract.CodeConflict, "observation disposition carries no model output to interpret")
	}
	t, err := loadFxTurnByID(ctx, u, turnID)
	if err != nil {
		return nil, err
	}
	var body struct {
		ToolProposals []fxToolProposal `json:"tool_proposals"`
	}
	if err := json.Unmarshal(in.Observation.Evidence, &body); err != nil {
		return nil, fxFault(contract.CodeInvalidInput, "malformed model output evidence")
	}
	stepIndex := t.stepsUsed
	for _, tp := range body.ToolProposals {
		normalized, _ := json.Marshal(map[string]any{"kind": tp.Kind, "text": tp.Text})
		state := "recorded"
		if tp.Kind == "local_operation" || tp.Kind == "external_tool" {
			state = "prepared"
		}
		if _, err := u.ExecContext(ctx, `INSERT INTO execution_proposals (turn_id, step_index, proposal_id, normalized_proposal, state, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, string(turnID), stepIndex, tp.ID, string(normalized), state, f.clock.Now().Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
		switch tp.Kind {
		case "reply":
			t.state = "completed"
		case "report_outputs":
			t.state = "reporting"
			if in.AttemptID != "" {
				if _, err := u.ExecContext(ctx, `UPDATE execution_turn_attempts SET state = 'reported' WHERE id = ?`, string(in.AttemptID)); err != nil {
					return nil, err
				}
				jobID := contract.NewID()
				req := verificationRequestDoc(jobID, in.AttemptID, tp.Digest)
				if _, err := u.ExecContext(ctx, `INSERT INTO execution_verification_requests (job_id, attempt_id, task_id, state, request)
					VALUES (?, ?, ?, 'pending', ?)`, string(jobID), string(in.AttemptID), string(contract.NewID()), string(req)); err != nil {
					return nil, err
				}
			}
		}
		t.stepsUsed++
	}
	if err := t.save(ctx, u); err != nil {
		return nil, err
	}
	return map[string]any{"resource": f.attemptResource(in.AttemptID)}, nil
}

// verificationRequestDoc builds a schema-valid Adapter_VerificationRequest
// document naming one expected check against the given digest.
func verificationRequestDoc(jobID, attemptID contract.ID, digest string) json.RawMessage {
	if digest == "" {
		digest = fxDigest
	}
	raw, _ := json.Marshal(map[string]any{
		"schema": "zatiti.verification-request/v1", "job_id": jobID, "task_id": contract.NewID(),
		"attempt_id": attemptID, "scope": map[string]any{"installation_id": contract.NewID()},
		"acceptance_digest": fxDigest,
		"profile": map[string]any{
			"schema": "zatiti.verifier-profile/v1", "kind": "artifact_contract", "id": "synthetic-verifier",
			"version": "1", "code_digest": fxDigest, "supported_checks": []string{"digest"},
			"max_bytes": 1048576, "timeout_seconds": 30,
			"capability_evidence": map[string]any{
				"artifact":        map[string]any{"id": contract.NewID(), "digest": fxDigest},
				"adapter_version": "1", "source_revision": "1", "protocol_revision": "1", "profile_digest": fxDigest,
				"qualified_at": "2026-03-01T12:00:00Z", "capabilities": []string{}, "limitations": []string{},
			},
		},
		"sealed_inputs": []any{}, "outputs": []any{},
		"expected_observations": []any{
			map[string]any{"check_id": "output-digest", "kind": "artifact_digest", "expected": "pass", "expected_digest": digest},
		},
		"deadline": "2027-01-01T00:00:00Z",
	})
	return raw
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

// configurationExportRecord durably links the artifact reference the
// controller obtained (after configuration's own RunJob already staged and
// published its bytes) to configuration's own local export-job row,
// mirroring the real owner's exact generation-fenced, expected_version-
// gated behavior (internal/configuration/jobs.go, handleExportRecord).
func (f *fx) configurationExportRecord(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in configurationExportRecordInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	if in.Generation != u.Generation() {
		return nil, fxFault(contract.CodeStaleVersion, "export job was prepared under a different controller generation")
	}
	var version int64
	var artifactID, artifactDigest string
	err := u.QueryRowContext(ctx, `SELECT version, artifact_id, artifact_digest FROM configuration_export_jobs WHERE job_id = ?`, string(in.JobID)).
		Scan(&version, &artifactID, &artifactDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodeNotFound, "export job not found")
	}
	if err != nil {
		return nil, err
	}
	if artifactID != "" {
		if artifactID == string(in.Artifact.ID) && artifactDigest == string(in.Artifact.Digest) {
			return map[string]any{"resource": f.jobResource(in.JobID, configurationOwner, "organization.export", "succeeded")}, nil
		}
		return nil, fxFault(contract.CodeStaleVersion, "export job already recorded a different artifact")
	}
	if version != in.ExpectedVersion {
		return nil, fxFault(contract.CodeStaleVersion, "export job is at a different version")
	}
	if _, err := u.ExecContext(ctx, `UPDATE configuration_export_jobs SET version = version + 1, artifact_id = ?, artifact_digest = ? WHERE job_id = ?`,
		string(in.Artifact.ID), string(in.Artifact.Digest), string(in.JobID)); err != nil {
		return nil, err
	}
	return map[string]any{"resource": f.jobResource(in.JobID, configurationOwner, "organization.export", "succeeded")}, nil
}

// skillsEvaluationRecord records published verifier evidence against the
// exact immutable skill version the evaluation names, mirroring the real
// owner's own behavior (internal/skills/jobs.go): the caller's asserted
// verifier identity must match what was sealed at admission, and a
// mismatch invalidates the evaluation rather than recording it.
func (f *fx) skillsEvaluationRecord(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in skillsEvaluationRecordInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	var jobID, sealedVerifierID string
	var version int64
	var state string
	err := u.QueryRowContext(ctx, `SELECT job_id, version, verifier_id, state FROM skills_evaluations WHERE evaluation_id = ?`, string(in.EvaluationID)).
		Scan(&jobID, &version, &sealedVerifierID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodeNotFound, "evaluation not found")
	}
	if err != nil {
		return nil, err
	}
	if jobID != string(in.JobID) {
		return nil, fxFault(contract.CodeConflict, "evaluation is linked to a different job")
	}
	if state != "pending" {
		return nil, fxFault(contract.CodeConflict, "evaluation already recorded a terminal disposition")
	}
	if version != in.ExpectedVersion {
		return nil, fxFault(contract.CodeStaleVersion, "evaluation is at a different version")
	}
	if sealedVerifierID != "" && sealedVerifierID != in.VerifierID {
		// A verifier identity that no longer matches what was admitted
		// invalidates the evaluation instead of recording the mismatch.
		if _, err := u.ExecContext(ctx, `UPDATE skills_evaluations SET version = version + 1, state = 'invalidated' WHERE evaluation_id = ?`,
			string(in.EvaluationID)); err != nil {
			return nil, err
		}
		return nil, fxFault(contract.CodeConflict, "verifier identity does not match what was admitted")
	}
	passed := 0
	if in.Passed {
		passed = 1
	}
	if _, err := u.ExecContext(ctx, `UPDATE skills_evaluations SET version = version + 1, verifier_id = ?, verifier_version = ?, passed = ?, state = 'recorded' WHERE evaluation_id = ?`,
		in.VerifierID, in.VerifierVersion, passed, string(in.EvaluationID)); err != nil {
		return nil, err
	}
	return map[string]any{"resource": f.jobResource(in.JobID, skillsOwner, skillEvaluateOp, "succeeded")}, nil
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

	mu             chan struct{}
	invoked        []contract.Dispatch
	reconciled     int
	reconciledCall []contract.Dispatch
	// reply, when set, produces the observation.
	reply func(ctx context.Context, d contract.Dispatch) (contract.Observation, error)
	// reconcileReply, when set, produces Reconcile's observation. When nil,
	// Reconcile fails loudly: a test that expects reconciliation must set
	// this explicitly, so an adapter that is never supposed to be
	// reconciled still catches an accidental call.
	reconcileReply func(ctx context.Context, d contract.Dispatch) (contract.Observation, error)
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

func (a *fakeAdapter) Reconcile(ctx context.Context, d contract.Dispatch) (contract.Observation, error) {
	a.mu <- struct{}{}
	a.reconciled++
	a.reconciledCall = append(a.reconciledCall, d)
	reply := a.reconcileReply
	<-a.mu
	if reply != nil {
		return reply(ctx, d)
	}
	return contract.Observation{}, errors.New("reconcile is not armed on this fake adapter for this test")
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

// Stage computes the real digest of the given bytes and returns a unique
// staging reference; Publish (below) is what records it published.
func (b *fakeBlobs) Stage(_ context.Context, r io.Reader, size int64) (string, contract.Digest, int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	digest := contract.Hash(data)
	ref := "staged-" + string(contract.NewID())
	return ref, digest, int64(len(data)), nil
}

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
