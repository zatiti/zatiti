package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Exact local fakes of the durable worker-turn pipeline (P14/P15/P16) the
// controller drives. Each follows the frozen contract for its operation:
// version/generation fences, idempotent replay and the documented state
// transitions. Where the real internal/execution package leaves a seam
// unreachable from outside its own package (this file's turnsFixtureSchema
// doc comment, and turns.go's own header, explain exactly which and why),
// this fake represents the *documented, intended* behavior so the
// controller's own delivery/routing code is exercised faithfully -- never
// a shortcut that skips the controller's real call sequence.

const turnsFixtureSchema = `
CREATE TABLE execution_turns (
	seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, version INTEGER NOT NULL,
	worker_id TEXT NOT NULL, source_kind TEXT NOT NULL, source_id TEXT NOT NULL, source_version INTEGER NOT NULL,
	requester_id TEXT NOT NULL, state TEXT NOT NULL, generation INTEGER NOT NULL, steps_used INTEGER NOT NULL DEFAULT 0,
	attempt_id TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL, config_revision INTEGER NOT NULL DEFAULT 1,
	context_artifact_id TEXT NOT NULL DEFAULT '', context_artifact_digest TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX execution_turns_source ON execution_turns(worker_id, source_kind, source_id, source_version);
CREATE TABLE execution_context_plans (id TEXT PRIMARY KEY, turn_id TEXT NOT NULL, expected_version INTEGER NOT NULL,
	generation INTEGER NOT NULL, refs TEXT NOT NULL, config_revision INTEGER NOT NULL, byte_bound INTEGER NOT NULL,
	token_bound INTEGER NOT NULL);
CREATE TABLE execution_proposals (turn_id TEXT NOT NULL, step_index INTEGER NOT NULL, proposal_id TEXT NOT NULL,
	normalized_proposal TEXT NOT NULL, state TEXT NOT NULL, command_id TEXT NOT NULL DEFAULT '',
	effect_operation_id TEXT NOT NULL DEFAULT '', result_artifact TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
	PRIMARY KEY (turn_id, step_index, proposal_id));
CREATE TABLE execution_turn_attempts (id TEXT PRIMARY KEY, turn_id TEXT NOT NULL, version INTEGER NOT NULL, state TEXT NOT NULL);
CREATE TABLE execution_verification_requests (job_id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL, task_id TEXT NOT NULL,
	state TEXT NOT NULL, request TEXT NOT NULL);
`

// modelAdapterName is the adapter name the fake context.commit's simulated
// chain-through prepares a model_step effect against.
const modelAdapterName = "responses"

// ---- messaging ----

// message commits one admitted message awaiting a durable worker turn, as
// messaging would through _messaging.admit.
func (f *fx) message(recipient contract.ID, body string) contract.ID {
	id := contract.NewID()
	recipients, _ := json.Marshal([]contract.ID{recipient})
	scope, _ := json.Marshal(f.scope())
	f.exec(`INSERT INTO messaging_messages (id, version, sender_id, recipient_ids, scope, body, state, created_at)
		VALUES (?, 1, ?, ?, ?, ?, 'admitted', ?)`,
		string(id), string(contract.NewID()), string(recipients), string(scope), body, f.clock.Now().Format(time.RFC3339Nano))
	return id
}

func (f *fx) messagingReady(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in limitInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	rows, err := u.QueryContext(ctx, `SELECT id, version, sender_id, recipient_ids, scope, body, created_at
		FROM messaging_messages WHERE turn_id = '' ORDER BY seq LIMIT ?`, in.Limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []map[string]any{}
	for rows.Next() {
		var id, sender, recipients, scope, body, createdAt string
		var version int64
		if err := rows.Scan(&id, &version, &sender, &recipients, &scope, &body, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "version": version, "sender_id": sender, "recipient_ids": json.RawMessage(recipients),
			"scope": json.RawMessage(scope), "task_ids": []string{}, "body": body, "attachments": []any{},
			"state": "admitted", "created_at": createdAt,
		})
	}
	return map[string]any{"items": items}, rows.Err()
}

// ---- worker turns ----

type fxTurn struct {
	id, version                                 string
	workerID, sourceKind, sourceID, requesterID string
	sourceVersion, generation, stepsUsed        int64
	state, scope                                string
	attemptID, configRevision                   string
	contextArtifactID, contextArtifactDigest    string
}

func loadFxTurnByID(ctx context.Context, u contract.Unit, id contract.ID) (*fxTurn, error) {
	t := &fxTurn{}
	err := u.QueryRowContext(ctx, `SELECT id, version, worker_id, source_kind, source_id, source_version, requester_id,
		state, generation, steps_used, attempt_id, scope, config_revision, context_artifact_id, context_artifact_digest
		FROM execution_turns WHERE id = ?`, string(id)).Scan(
		&t.id, &t.version, &t.workerID, &t.sourceKind, &t.sourceID, &t.sourceVersion, &t.requesterID,
		&t.state, &t.generation, &t.stepsUsed, &t.attemptID, &t.scope, &t.configRevision,
		&t.contextArtifactID, &t.contextArtifactDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodeNotFound, "turn not found")
	}
	return t, err
}

// loadFxActiveTurnForWorker mirrors admitTurn's safe-boundary-injection
// fallback (turn_ops.go, findActiveTurnForWorker): a source arriving while
// the worker already has a live (non-terminal) decision stream links into
// that stream instead of starting a second concurrent one.
func loadFxActiveTurnForWorker(ctx context.Context, u contract.Unit, worker contract.ID) (*fxTurn, error) {
	var id string
	err := u.QueryRowContext(ctx, `SELECT id FROM execution_turns WHERE worker_id = ?
		AND state NOT IN ('completed', 'failed', 'cancelled') ORDER BY seq LIMIT 1`, string(worker)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return loadFxTurnByID(ctx, u, contract.ID(id))
}

func loadFxTurnBySource(ctx context.Context, u contract.Unit, worker, kind, sourceID contract.ID, sourceVersion int64) (*fxTurn, error) {
	var id string
	err := u.QueryRowContext(ctx, `SELECT id FROM execution_turns WHERE worker_id = ? AND source_kind = ? AND source_id = ? AND source_version = ?`,
		string(worker), string(kind), string(sourceID), sourceVersion).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return loadFxTurnByID(ctx, u, contract.ID(id))
}

func (t *fxTurn) save(ctx context.Context, u contract.Unit) error {
	_, err := u.ExecContext(ctx, `UPDATE execution_turns SET version = ?, state = ?, generation = ?, steps_used = ?,
		attempt_id = ?, context_artifact_id = ?, context_artifact_digest = ? WHERE id = ?`,
		t.version, t.state, t.generation, t.stepsUsed, t.attemptID, t.contextArtifactID, t.contextArtifactDigest, t.id)
	return err
}

func (t *fxTurn) wire() map[string]any {
	limits := map[string]any{
		"currency": "USD", "spend_micro_units": 0, "concurrency": 1, "model_steps": 100, "child_count": 8,
		"delegation_depth": 3, "attempt_seconds": 1800, "root_deadline": "2027-01-01T00:00:00Z",
	}
	w := map[string]any{
		"id": t.id, "worker_id": t.workerID, "principal_id": t.workerID, "scope": json.RawMessage(t.scope),
		"source":       map[string]any{"kind": t.sourceKind, "source_id": t.sourceID, "source_version": t.sourceVersion},
		"requester_id": t.requesterID, "version": mustAtoi(t.version), "configuration_revision": mustAtoi(t.configRevision),
		"state": t.state, "generation": t.generation, "limits": limits, "root_id": t.id, "steps_used": t.stepsUsed,
		"created_at": "2026-03-01T12:00:00Z", "updated_at": "2026-03-01T12:00:00Z",
	}
	if t.attemptID != "" {
		w["attempt_id"] = t.attemptID
	}
	if t.contextArtifactID != "" {
		w["context_artifact"] = map[string]any{"id": t.contextArtifactID, "digest": t.contextArtifactDigest}
	}
	return w
}

func mustAtoi(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 1
		}
		n = n*10 + int64(c-'0')
	}
	if n == 0 {
		return 1
	}
	return n
}

// turn admits (or replays) one durable worker turn directly, as
// _execution.turn.admit or the _execution.enqueue task-turn hook would.
// Tests use it to set up a task-triggered turn with a pre-claimed attempt,
// exactly as autoClaimHostedRun would have left it.
func (f *fx) turn(worker contract.ID, sourceKind string, sourceID contract.ID, state string) *fxTurn {
	scope, _ := json.Marshal(f.scope())
	t := &fxTurn{
		id: string(contract.NewID()), version: "1", workerID: string(worker), sourceKind: sourceKind,
		sourceID: string(sourceID), sourceVersion: 1, requesterID: string(contract.NewID()),
		state: state, generation: f.generation(), stepsUsed: 0, scope: string(scope), configRevision: "1",
	}
	f.exec(`INSERT INTO execution_turns (id, version, worker_id, source_kind, source_id, source_version, requester_id,
		state, generation, steps_used, attempt_id, scope, config_revision) VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, 0, '', ?, 1)`,
		t.id, t.workerID, t.sourceKind, t.sourceID, t.sourceVersion, t.requesterID, t.state, t.generation, t.scope)
	return t
}

// turnAttempt gives a turn its own attempt, exactly as autoClaimHostedRun
// would when work.claim admits a task-triggered turn's queued run.
func (f *fx) turnAttempt(turnID contract.ID) contract.ID {
	id := contract.NewID()
	f.exec(`INSERT INTO execution_turn_attempts (id, turn_id, version, state) VALUES (?, ?, 1, 'claimed')`, string(id), string(turnID))
	f.exec(`UPDATE execution_turns SET attempt_id = ? WHERE id = ?`, string(id), string(turnID))
	return id
}

func (f *fx) turnState(id contract.ID) string {
	return f.queryString(`SELECT state FROM execution_turns WHERE id = ?`, string(id))
}

func (f *fx) turnAttemptState(id contract.ID) string {
	return f.queryString(`SELECT state FROM execution_turn_attempts WHERE id = ?`, string(id))
}

func (f *fx) messageTurn(id contract.ID) string {
	return f.queryString(`SELECT turn_id FROM messaging_messages WHERE id = ?`, string(id))
}

func (f *fx) verificationCount(attemptID contract.ID) int64 {
	return f.queryInt(`SELECT COUNT(*) FROM execution_verification_requests WHERE attempt_id = ?`, string(attemptID))
}

func (f *fx) executionTurnAdmit(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in turnAdmitInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	existing, err := loadFxTurnBySource(ctx, u, in.WorkerID, contract.ID(in.Source.Kind), in.Source.SourceID, in.Source.SourceVersion)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		existing, err = loadFxActiveTurnForWorker(ctx, u, in.WorkerID)
		if err != nil {
			return nil, err
		}
	}
	if existing != nil {
		if in.Source.Kind == "message" {
			if _, err := u.ExecContext(ctx, `UPDATE messaging_messages SET turn_id = ? WHERE id = ?`,
				existing.id, string(in.Source.SourceID)); err != nil {
				return nil, err
			}
		}
		return map[string]any{"resource": existing.wire()}, nil
	}
	t := f.turn(in.WorkerID, in.Source.Kind, in.Source.SourceID, "pending")
	t.requesterID = string(in.RequesterID)
	scope, _ := json.Marshal(in.Scope)
	t.scope = string(scope)
	if err := t.save(ctx, u); err != nil {
		return nil, err
	}
	if _, err := u.ExecContext(ctx, `UPDATE execution_turns SET requester_id = ?, scope = ? WHERE id = ?`, t.requesterID, t.scope, t.id); err != nil {
		return nil, err
	}
	if in.Source.Kind == "message" {
		if _, err := u.ExecContext(ctx, `UPDATE messaging_messages SET turn_id = ? WHERE id = ?`, t.id, string(in.Source.SourceID)); err != nil {
			return nil, err
		}
	}
	return map[string]any{"resource": t.wire()}, nil
}

// pauseWorker blocks new admission for one worker, exactly as worker.pause
// does in the real system: an exact replay or an injection into an already-
// active stream is unaffected, but a pending turn is not surfaced for claim
// while its worker is paused (turn_ops.go, handleWorkPending/handleWorkClaim).
func (f *fx) pauseWorker(id contract.ID) {
	f.mu.Lock()
	f.paused[id] = true
	f.mu.Unlock()
}

func (f *fx) isPaused(id contract.ID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paused[id]
}

func (f *fx) executionWorkPending(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in limitInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	rows, err := u.QueryContext(ctx, `SELECT id, state, worker_id, next_wake FROM execution_turns ORDER BY seq LIMIT ?`, in.Limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	type row struct{ id, state, worker, nextWake string }
	var found []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.state, &r.worker, &r.nextWake); err != nil {
			return nil, err
		}
		found = append(found, r)
	}
	now := f.clock.Now()
	items := []map[string]any{}
	for _, r := range found {
		var kind string
		switch r.state {
		case "pending":
			if f.isPaused(contract.ID(r.worker)) {
				continue
			}
			kind = "claim"
		case "waiting":
			if r.nextWake == "" {
				continue
			}
			due, err := time.Parse(time.RFC3339Nano, r.nextWake)
			if err != nil || due.After(now) {
				continue
			}
			kind = "resume"
		case "claimed":
			kind = "context"
		case "model_pending":
			kind = "proposal"
		default:
			continue
		}
		t, err := loadFxTurnByID(ctx, u, contract.ID(r.id))
		if err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": r.id, "kind": kind, "scope": json.RawMessage(t.scope), "turn": t.wire()})
	}
	return map[string]any{"items": items}, nil
}

func (f *fx) executionWorkClaim(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in workClaimInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	t, err := loadFxTurnByID(ctx, u, in.WorkID)
	if err != nil {
		return nil, err
	}
	switch t.state {
	case "pending":
		if f.isPaused(contract.ID(t.workerID)) {
			return nil, fxFault(contract.CodeConflict, "worker admission is paused")
		}
		if mustAtoi(t.version) != in.ExpectedVersion {
			return nil, fxFault(contract.CodeStaleVersion, "turn version moved")
		}
		t.version = itoa(mustAtoi(t.version) + 1)
		t.state = "claimed"
		t.generation = in.Generation
		if err := t.save(ctx, u); err != nil {
			return nil, err
		}
		return map[string]any{"item": map[string]any{"id": t.id, "kind": "context", "scope": json.RawMessage(t.scope), "turn": t.wire()},
			"claim_token": "lease-synthetic", "version": mustAtoi(t.version)}, nil
	case "waiting":
		if f.isPaused(contract.ID(t.workerID)) {
			return nil, fxFault(contract.CodeConflict, "worker admission is paused")
		}
		if mustAtoi(t.version) != in.ExpectedVersion {
			return nil, fxFault(contract.CodeStaleVersion, "turn version moved")
		}
		// revalidateWaitingTurn's real counterpart rechecks the specific
		// waiting reason; this fixture's tests only exercise the recovery
		// reason a fence sets, which is always ready to resume.
		t.version = itoa(mustAtoi(t.version) + 1)
		t.state = "claimed"
		t.generation = in.Generation
		if err := t.save(ctx, u); err != nil {
			return nil, err
		}
		if _, err := u.ExecContext(ctx, `UPDATE execution_turns SET next_wake = '' WHERE id = ?`, t.id); err != nil {
			return nil, err
		}
		return map[string]any{"item": map[string]any{"id": t.id, "kind": "context", "scope": json.RawMessage(t.scope), "turn": t.wire()},
			"claim_token": "lease-synthetic", "version": mustAtoi(t.version)}, nil
	case "claimed", "model_pending":
		if t.generation == in.Generation {
			kind := "context"
			if t.state == "model_pending" {
				kind = "proposal"
			}
			return map[string]any{"item": map[string]any{"id": t.id, "kind": kind, "scope": json.RawMessage(t.scope), "turn": t.wire()},
				"claim_token": "lease-synthetic", "version": mustAtoi(t.version)}, nil
		}
		return nil, fxFault(contract.CodeConflict, "turn already claimed by another generation")
	default:
		return nil, fxFault(contract.CodeConflict, "turn has no pending work")
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// ---- context plans ----

func (f *fx) executionContextPrepare(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in contextPrepareInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	t, err := loadFxTurnByID(ctx, u, in.TurnID)
	if err != nil {
		return nil, err
	}
	if t.state != "claimed" && t.state != "context_pending" {
		return nil, fxFault(contract.CodeConflict, "turn cannot prepare context")
	}
	planID := contract.NewID()
	if _, err := u.ExecContext(ctx, `INSERT INTO execution_context_plans (id, turn_id, expected_version, generation, refs, config_revision, byte_bound, token_bound)
		VALUES (?, ?, ?, ?, '[]', ?, 1048576, 100000)`,
		string(planID), t.id, mustAtoi(t.version), t.generation, mustAtoi(t.configRevision)); err != nil {
		return nil, err
	}
	if t.state != "context_pending" {
		t.state = "context_pending"
		if err := t.save(ctx, u); err != nil {
			return nil, err
		}
	}
	var scope contract.Scope
	if err := json.Unmarshal([]byte(t.scope), &scope); err != nil {
		return nil, err
	}
	var attempt any
	if t.attemptID != "" {
		attempt = t.attemptID
	}
	return map[string]any{"resource": map[string]any{
		"id": planID, "turn_id": t.id, "expected_version": mustAtoi(t.version), "generation": t.generation,
		"scope": scope, "attempt_id": attempt, "recipe": map[string]any{},
		"refs": []any{}, "configuration_revision": mustAtoi(t.configRevision), "byte_bound": 1048576, "token_bound": 100000,
	}}, nil
}

type fxContextPlan struct {
	id, turnID                       string
	expectedVersion, generation      int64
	byteBound, tokenBound, configRev int64
}

func loadFxContextPlan(ctx context.Context, u contract.Unit, id contract.ID) (*fxContextPlan, error) {
	p := &fxContextPlan{id: string(id)}
	err := u.QueryRowContext(ctx, `SELECT turn_id, expected_version, generation, config_revision, byte_bound, token_bound
		FROM execution_context_plans WHERE id = ?`, string(id)).
		Scan(&p.turnID, &p.expectedVersion, &p.generation, &p.configRev, &p.byteBound, &p.tokenBound)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodeNotFound, "context plan not found")
	}
	return p, err
}

// executionContextCommit simulates the documented-but-unimplemented chain-
// through turns.go's header explains in full: the real handleContextCommit
// (internal/execution/turn_ops.go) commits the context and stops, leaving
// nothing to dispatch its model_step effect since _effects.prepare's
// caller allowlist does not include "controller". This fake represents
// what execution's own doc comments (context_build.go,
// buildResponsesModelStepAction) say that handler is intended to also do --
// prepare the model_step effect internally, exactly as interpretExternalTool
// and prepareModelEffect already do in the real, landed code for their own
// cases -- so the controller's own generic effect dispatch and worker_turn
// callback routing are exercised through their real call sequence, not
// bypassed.
func (f *fx) executionContextCommit(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in contextCommitInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	p, err := loadFxContextPlan(ctx, u, in.PlanID)
	if err != nil {
		return nil, err
	}
	if p.expectedVersion != in.ExpectedVersion || p.generation != in.Generation {
		return nil, fxFault(contract.CodeStaleVersion, "context plan does not match the given version/generation")
	}
	if in.StagedContext.Kind != "artifact" || in.StagedContext.Artifact == nil {
		return nil, fxFault(contract.CodeArtifactFault, "staged context is an unpublished obligation")
	}
	t, err := loadFxTurnByID(ctx, u, contract.ID(p.turnID))
	if err != nil {
		return nil, err
	}
	if t.state != "context_pending" {
		return nil, fxFault(contract.CodeConflict, "turn cannot commit context")
	}
	t.contextArtifactID = string(in.StagedContext.Artifact.ID)
	t.contextArtifactDigest = string(in.StagedContext.Artifact.Digest)
	t.state = "model_pending"
	if err := t.save(ctx, u); err != nil {
		return nil, err
	}
	route, _ := json.Marshal(wireCallbackRoute{Kind: "worker_turn", TurnID: t.id2(), StepIndex: int64Ptr(t.stepsUsed)})
	action := f.action(map[string]any{"schema": "zatiti.responses.action/v1", "kind": "model_step"}, "")
	if _, err := u.ExecContext(ctx, `INSERT INTO effects_operations (id, version, state, action, adapter, callback_route)
		VALUES (?, 1, 'prepared', ?, ?, ?)`, string(contract.NewID()), string(action), modelAdapterName, string(route)); err != nil {
		return nil, err
	}
	return map[string]any{"resource": t.wire()}, nil
}

func (t *fxTurn) id2() contract.ID { return contract.ID(t.id) }
func int64Ptr(n int64) *int64      { return &n }

// ---- proposals ----

func (f *fx) executionProposalPrepare(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in proposalPrepareInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	var normalized, state, commandID, effectOpID, resultArtifact string
	err := u.QueryRowContext(ctx, `SELECT normalized_proposal, state, command_id, effect_operation_id, result_artifact
		FROM execution_proposals WHERE turn_id = ? AND step_index = ? AND proposal_id = ?`,
		string(in.TurnID), in.StepIndex, in.ProposalID).Scan(&normalized, &state, &commandID, &effectOpID, &resultArtifact)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodePrerequisiteMissing, "no normalized model evidence is persisted for this proposal")
	}
	if err != nil {
		return nil, err
	}
	w := map[string]any{
		"turn_id": in.TurnID, "step_index": in.StepIndex, "proposal_id": in.ProposalID,
		"source_context_digest": fxDigest, "normalized_proposal": json.RawMessage(normalized), "state": state,
		"created_at": "2026-03-01T12:00:00Z", "updated_at": "2026-03-01T12:00:00Z",
	}
	if commandID != "" {
		w["command_id"] = commandID
	}
	if effectOpID != "" {
		w["effect_operation_id"] = effectOpID
	}
	if resultArtifact != "" {
		w["result_artifact"] = json.RawMessage(resultArtifact)
	}
	return map[string]any{"resource": w}, nil
}

func (f *fx) executionProposalRecord(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in proposalRecordInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	var turnID string
	var stepIndex int64
	var state string
	err := u.QueryRowContext(ctx, `SELECT turn_id, step_index, state FROM execution_proposals WHERE proposal_id = ?`, in.ProposalID).
		Scan(&turnID, &stepIndex, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodeNotFound, "proposal does not exist")
	}
	if err != nil {
		return nil, err
	}
	t, err := loadFxTurnByID(ctx, u, contract.ID(turnID))
	if err != nil {
		return nil, err
	}
	if state == "recorded" {
		return map[string]any{"resource": t.wire()}, nil
	}
	var artifact string
	if in.ResultArtifact != nil {
		raw, _ := json.Marshal(in.ResultArtifact)
		artifact = string(raw)
	}
	if _, err := u.ExecContext(ctx, `UPDATE execution_proposals SET state = 'recorded', command_id = ?, effect_operation_id = ?,
		result_artifact = ? WHERE turn_id = ? AND step_index = ? AND proposal_id = ?`,
		string(in.CommandID), string(in.EffectOperationID), artifact, turnID, stepIndex, in.ProposalID); err != nil {
		return nil, err
	}
	t.stepsUsed++
	t.state = "claimed"
	if err := t.save(ctx, u); err != nil {
		return nil, err
	}
	return map[string]any{"resource": t.wire()}, nil
}

// ---- verification ----

func (f *fx) executionVerificationPending(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in limitInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	rows, err := u.QueryContext(ctx, `SELECT request FROM execution_verification_requests WHERE state = 'pending' ORDER BY job_id LIMIT ?`, in.Limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []json.RawMessage{}
	for rows.Next() {
		var req string
		if err := rows.Scan(&req); err != nil {
			return nil, err
		}
		items = append(items, json.RawMessage(req))
	}
	return map[string]any{"items": items}, rows.Err()
}

func (f *fx) executionVerificationClaim(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in verificationClaimInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	var state, req, attemptID string
	err := u.QueryRowContext(ctx, `SELECT state, request, attempt_id FROM execution_verification_requests WHERE job_id = ?`, string(in.RequestID)).
		Scan(&state, &req, &attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fxFault(contract.CodeNotFound, "verification request not found")
	}
	if err != nil {
		return nil, err
	}
	if state != "pending" {
		return nil, fxFault(contract.CodeConflict, "verification request already claimed")
	}
	if _, err := u.ExecContext(ctx, `UPDATE execution_verification_requests SET state = 'claimed' WHERE job_id = ?`, string(in.RequestID)); err != nil {
		return nil, err
	}
	// Mirror the real handler's widened output: the claimed attempt's own
	// current live version, read in this same transaction.
	var attemptVersion int64
	if err := u.QueryRowContext(ctx, `SELECT version FROM execution_turn_attempts WHERE id = ?`, attemptID).Scan(&attemptVersion); err != nil {
		return nil, err
	}
	return map[string]any{"request": json.RawMessage(req), "claim_token": "verify-lease-synthetic", "attempt_version": attemptVersion}, nil
}

func (f *fx) executionVerificationRecord(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in verificationRecordInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	var probe struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(in.Result, &probe); err != nil {
		return nil, fxFault(contract.CodeInvalidInput, "malformed verification result")
	}
	next := "failed"
	if probe.Status == "passed" {
		next = "succeeded"
	}
	if _, err := u.ExecContext(ctx, `UPDATE execution_turn_attempts SET state = ? WHERE id = ?`, next, string(in.AttemptID)); err != nil {
		return nil, err
	}
	if _, err := u.ExecContext(ctx, `UPDATE execution_verification_requests SET state = 'recorded' WHERE attempt_id = ?`, string(in.AttemptID)); err != nil {
		return nil, err
	}
	return map[string]any{"resource": f.attemptResource(in.AttemptID)}, nil
}

// ---- verifier / worker operator ----

// fakeVerifier independently establishes a controlled, always-"passed"
// verification outcome by echoing the identifiers a real trusted verifier
// runner would read from the sealed request.
type fakeVerifier struct {
	status string // "passed" or "failed"; defaults to "passed"
}

func (v *fakeVerifier) Verify(_ context.Context, in contract.Verification) (contract.VerificationResult, error) {
	var probe verificationRequestProbe
	if err := json.Unmarshal(in.Request, &probe); err != nil {
		return contract.VerificationResult{}, err
	}
	status := v.status
	if status == "" {
		status = "passed"
	}
	now := "2026-03-01T12:05:00Z"
	doc, err := json.Marshal(map[string]any{
		"schema": "zatiti.verification-result/v1", "job_id": probe.JobID, "task_id": probe.TaskID,
		"attempt_id": probe.AttemptID, "acceptance_digest": fxDigest,
		"verifier_id": "synthetic-verifier", "verifier_version": "1", "verifier_code_digest": fxDigest,
		"request_artifact": map[string]any{"id": contract.NewID(), "digest": fxDigest},
		"status":           status, "observations": []any{}, "started_at": now, "finished_at": now,
		"staged_outputs": []any{}, "output_artifacts": []any{}, "independent": true,
	})
	return contract.VerificationResult{Document: doc}, err
}

// ---- execution report ----

func (f *fx) executionReport(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error) {
	var in executionReportInput
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	if _, err := u.ExecContext(ctx, `UPDATE execution_turn_attempts SET state = 'reported' WHERE id = ?`, string(in.AttemptID)); err != nil {
		return nil, err
	}
	return map[string]any{"resource": f.attemptResource(in.AttemptID)}, nil
}
