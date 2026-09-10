package tasks

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The state engine. Legal transitions:
//
//	draft     -> ready
//	ready     -> running | waiting
//	running   -> verifying | failed | waiting
//	verifying -> succeeded | failed | waiting
//	waiting   -> prior_state (resume)
//
// Any non-terminal state may additionally move to cancelled, but only after
// a recorded cancellation intent. succeeded runs the acceptance fence first.

// legalTransitions is the admissible target set per current state. The
// cancelled target is gated by the recorded intent inside applyTransition.
var legalTransitions = map[string]map[string]bool{
	stateDraft:     {stateReady: true, stateCancelled: true},
	stateReady:     {stateRunning: true, stateWaiting: true, stateCancelled: true},
	stateRunning:   {stateVerifying: true, stateFailed: true, stateWaiting: true, stateCancelled: true},
	stateVerifying: {stateSucceeded: true, stateFailed: true, stateWaiting: true, stateCancelled: true},
	stateWaiting:   {stateCancelled: true}, // resume resolves only to the stored prior state
	stateSucceeded: {},
	stateFailed:    {},
	stateCancelled: {},
}

// transitionRequest carries the validated transition input.
type transitionRequest struct {
	TaskID          contract.ID
	ExpectedVersion int64
	Target          string
	EvidenceIDs     []contract.ID
	WaitingReason   string
	Manual          bool
}

// applyTransition validates and applies one state transition inside the
// caller's unit, returning the updated row.
func (s *Service) applyTransition(ctx context.Context, unit contract.Unit, req *transitionRequest) (*taskRow, error) {
	row, err := getTask(ctx, unit, req.TaskID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, notFound("task %s does not exist", req.TaskID)
	}
	if row.InstallationID != unit.Scope().InstallationID && unit.Scope().InstallationID != "" {
		return nil, permissionDenied("task %s is outside the authenticated installation", row.ID)
	}
	if row.Version != req.ExpectedVersion {
		return nil, staleVersion("task %s is at version %d, not the expected %d",
			row.ID, row.Version, req.ExpectedVersion)
	}
	if isTerminal(row.State) {
		return nil, conflictFault("task %s is terminal in state %s", row.ID, row.State)
	}

	// Waiting resumes to its stored prior state; the recorded-intent gate
	// inside the cancelled case covers terminal cancellation from waiting.
	if row.State == stateWaiting {
		if req.Target != row.PriorState && req.Target != stateCancelled {
			return nil, conflictFault("task %s is waiting and may only resume to %s, not %s",
				row.ID, row.PriorState, req.Target)
		}
	} else if !legalTransitions[row.State][req.Target] {
		return nil, conflictFault("transition %s to %s is not legal for task %s",
			row.State, req.Target, row.ID)
	}

	fromState := row.State
	now := s.clock.Now().UTC()
	switch req.Target {
	case stateReady:
		if row.CancellationRequested {
			return nil, conflictFault("task %s has a recorded cancellation intent and cannot start", row.ID)
		}
		if err := s.checkPrerequisites(ctx, unit, row); err != nil {
			return nil, err
		}
		row.State = stateReady
	case stateRunning:
		if row.CancellationRequested {
			return nil, conflictFault("task %s has a recorded cancellation intent and cannot start", row.ID)
		}
		row.State = stateRunning
	case stateVerifying:
		if row.CancellationRequested {
			return nil, conflictFault("task %s has a recorded cancellation intent and cannot enter verification", row.ID)
		}
		if _, err := s.recordEvidence(ctx, unit, row, req.EvidenceIDs, now); err != nil {
			return nil, err
		}
		row.State = stateVerifying
	case stateSucceeded:
		// The acceptance fence: recompute the seal, evaluate every
		// expected observation against held evidence, then check required
		// children. Evidence combines the set recorded at verifying entry
		// with anything submitted alongside this call.
		evidence, err := s.recordEvidence(ctx, unit, row, req.EvidenceIDs, now)
		if err != nil {
			return nil, err
		}
		evaluation, err := s.evaluateSuccess(ctx, unit, row, evidence, req.Manual)
		if err != nil {
			return nil, err
		}
		if err := s.checkRequiredChildren(ctx, unit, row); err != nil {
			return nil, err
		}
		row.State = stateSucceeded
		row.EstablishedBy = evaluation.established
	case stateFailed:
		row.State = stateFailed
	case stateWaiting:
		if req.WaitingReason == "" {
			return nil, invalidInput("entering waiting requires an explicit waiting_reason")
		}
		row.PriorState = row.State
		row.WaitingReason = req.WaitingReason
		row.State = stateWaiting
	case stateCancelled:
		if !row.CancellationRequested {
			return nil, conflictFault("task %s has no recorded cancellation intent; record intent before terminal cancellation", row.ID)
		}
		row.State = stateCancelled
	default:
		return nil, invalidInput("target state %q is not recognized", req.Target)
	}

	row.Version++
	row.UpdatedAt = now
	if row.State != stateWaiting {
		// Leaving waiting (or never entering it) clears the pause marker.
		row.WaitingReason = ""
		row.PriorState = ""
	}
	if err := updateTaskState(ctx, unit, row); err != nil {
		return nil, err
	}
	// A task entering ready is enqueued for execution inside this same
	// transaction; execution creates one run for the task/version pair and
	// deduplicates, so recovery rescans cannot duplicate work.
	if row.State == stateReady {
		wire, err := row.toWire()
		if err != nil {
			return nil, err
		}
		var enq peerEnqueueOut
		if err := s.callPeer(ctx, unit, "_execution.enqueue", peerEnqueueIn{Task: *wire}, &enq); err != nil {
			return nil, err
		}
	}
	if err := s.recordTransitionLog(ctx, unit, row, fromState, req); err != nil {
		return nil, err
	}
	if err := s.emitTaskEvent(ctx, unit, row, "tasks.task.transition", map[string]any{
		"task_id": row.ID, "from": fromState, "to": row.State,
		"waiting_reason": row.WaitingReason, "established_by": row.EstablishedBy,
	}); err != nil {
		return nil, err
	}
	return row, nil
}

// checkPrerequisites enforces that every declared dependency is succeeded
// before a task may become ready.
func (s *Service) checkPrerequisites(ctx context.Context, unit contract.Unit, row *taskRow) error {
	deps, err := row.decodeDependencies()
	if err != nil {
		return err
	}
	for _, dep := range deps {
		depRow, err := getTask(ctx, unit, dep)
		if err != nil {
			return err
		}
		if depRow == nil {
			return prerequisiteMissing("dependency task %s no longer exists", dep)
		}
		if depRow.State != stateSucceeded {
			return prerequisiteMissing("dependency task %s is %s, not succeeded", dep, depRow.State)
		}
	}
	return nil
}

// recordEvidence validates submitted evidence artifact ids against the
// artifacts owner and records them for the current attempt. Rows already
// recorded for this attempt are left in place, so resubmitting the
// verifying-entry evidence alongside the succeeded call is idempotent.
// Returns the full evidence set recorded for this attempt.
func (s *Service) recordEvidence(ctx context.Context, unit contract.Unit, row *taskRow, evidenceIDs []contract.ID, now time.Time) ([]evidenceRow, error) {
	scope, err := row.decodeScope()
	if err != nil {
		return nil, err
	}
	existing, err := evidenceForAttempt(ctx, unit, row.ID, row.Attempt)
	if err != nil {
		return nil, err
	}
	recorded := make(map[contract.ID]bool, len(existing))
	for _, e := range existing {
		recorded[e.ArtifactID] = true
	}
	if len(evidenceIDs) > 0 {
		refs := make([]wireArtifactRef, 0, len(evidenceIDs))
		for _, id := range evidenceIDs {
			if !recorded[id] {
				refs = append(refs, wireArtifactRef{ID: id})
			}
		}
		if len(refs) > 0 {
			metas, err := s.peerArtifactMetadata(ctx, unit, scope.toContract(), refs)
			if err != nil {
				return nil, err
			}
			byID := make(map[contract.ID]peerArtifact, len(metas))
			for _, m := range metas {
				byID[m.ID] = m
			}
			for _, ref := range refs {
				m, ok := byID[ref.ID]
				if !ok {
					return nil, artifactFault("evidence artifact %s is not resolvable", ref.ID)
				}
				if m.Scope.InstallationID != row.InstallationID {
					return nil, permissionDenied("evidence artifact %s is outside the installation scope", ref.ID)
				}
				if m.State != "available" {
					return nil, artifactFault("evidence artifact %s is not available", ref.ID)
				}
				if err := insertEvidence(ctx, unit, &evidenceRow{
					TaskID:       row.ID,
					ArtifactID:   ref.ID,
					Installation: row.InstallationID,
					Digest:       m.Digest,
					MediaType:    m.MediaType,
					Attempt:      row.Attempt,
					RecordedAt:   now,
				}); err != nil {
					return nil, err
				}
			}
		}
	}
	return evidenceForAttempt(ctx, unit, row.ID, row.Attempt)
}

// recordTransitionLog persists the durable history entry for a transition.
func (s *Service) recordTransitionLog(ctx context.Context, unit contract.Unit, row *taskRow, fromState string, req *transitionRequest) error {
	raw, err := marshalData(nonNilIDs(req.EvidenceIDs))
	if err != nil {
		return err
	}
	return insertTransition(ctx, unit, &transitionRow{
		ID:            s.ids.New(),
		TaskID:        row.ID,
		Installation:  row.InstallationID,
		FromState:     fromState,
		ToState:       row.State,
		EvidenceJSON:  string(raw),
		WaitingReason: req.WaitingReason,
		Manual:        req.Manual,
		RecordedAt:    row.UpdatedAt,
	})
}

// requestCancellation records cancel intent: new work blocks, terminal
// cancellation becomes possible once execution stops or is fenced.
func (s *Service) requestCancellation(ctx context.Context, unit contract.Unit, row *taskRow, reason string) (*taskRow, error) {
	if isTerminal(row.State) {
		return nil, conflictFault("task %s is terminal in state %s and cannot be cancelled", row.ID, row.State)
	}
	if row.CancellationRequested {
		return nil, conflictFault("task %s already has a recorded cancellation intent", row.ID)
	}
	now := s.clock.Now().UTC()
	row.CancellationRequested = true
	row.Version++
	row.UpdatedAt = now
	if err := updateTaskState(ctx, unit, row); err != nil {
		return nil, err
	}
	if err := s.emitTaskEvent(ctx, unit, row, "tasks.task.cancel_requested", map[string]any{
		"task_id": row.ID, "reason": reason, "state": row.State,
	}); err != nil {
		return nil, err
	}
	return row, nil
}

// retryTask starts a new run attempt under the unchanged accepted contract.
// Only failed or cancelled tasks retry; the acceptance, sealed digest,
// inputs and required outputs never move.
func (s *Service) retryTask(ctx context.Context, unit contract.Unit, row *taskRow, reason string) (*taskRow, error) {
	if row.State != stateFailed && row.State != stateCancelled {
		return nil, conflictFault("task %s in state %s cannot retry; only failed or cancelled tasks retry", row.ID, row.State)
	}
	if err := s.checkPrerequisites(ctx, unit, row); err != nil {
		return nil, err
	}
	fromState := row.State
	now := s.clock.Now().UTC()
	row.Attempt++
	row.State = stateReady
	row.WaitingReason = ""
	row.PriorState = ""
	row.CancellationRequested = false
	row.EstablishedBy = ""
	row.Version++
	row.UpdatedAt = now
	if err := updateTaskState(ctx, unit, row); err != nil {
		return nil, err
	}
	// Re-admission: execution deduplicates task/version and returns the
	// fresh run for the bumped version.
	wire, err := row.toWire()
	if err != nil {
		return nil, err
	}
	var enq peerEnqueueOut
	if err := s.callPeer(ctx, unit, "_execution.enqueue", peerEnqueueIn{Task: *wire}, &enq); err != nil {
		return nil, err
	}
	if err := s.recordTransitionLog(ctx, unit, row, fromState, &transitionRequest{
		TaskID: row.ID, Target: stateReady, WaitingReason: reason,
	}); err != nil {
		return nil, err
	}
	if err := s.emitTaskEvent(ctx, unit, row, "tasks.task.retried", map[string]any{
		"task_id": row.ID, "attempt": row.Attempt, "reason": reason,
	}); err != nil {
		return nil, err
	}
	return row, nil
}

// assignWorker moves the pinned worker under a version check. Concurrent
// conflicting assignments serialize on the version and lose cleanly.
func (s *Service) assignWorker(ctx context.Context, unit contract.Unit, row *taskRow, workerID contract.ID) (*taskRow, error) {
	if isTerminal(row.State) {
		return nil, conflictFault("task %s is terminal in state %s and cannot be reassigned", row.ID, row.State)
	}
	if row.CancellationRequested {
		return nil, conflictFault("task %s has a recorded cancellation intent and cannot be reassigned", row.ID)
	}
	if err := s.validateWorker(ctx, unit, mustDecodeScope(row), workerID); err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC()
	row.WorkerID = workerID
	row.Version++
	row.UpdatedAt = now
	if err := updateTaskState(ctx, unit, row); err != nil {
		return nil, err
	}
	if err := s.emitTaskEvent(ctx, unit, row, "tasks.task.assigned", map[string]any{
		"task_id": row.ID, "worker_id": workerID,
	}); err != nil {
		return nil, err
	}
	return row, nil
}

// updatePendingContract rewrites outcome and inputs while the task is
// still pending. The accepted verifier and envelope never move here.
func (s *Service) updatePendingContract(ctx context.Context, unit contract.Unit, row *taskRow, inputs []wireArtifactRef, outcome string, hasOutcome bool) (*taskRow, error) {
	if row.State != stateDraft && row.State != stateReady {
		return nil, conflictFault("task %s in state %s is no longer pending; only draft or ready tasks accept input updates", row.ID, row.State)
	}
	if row.CancellationRequested {
		return nil, conflictFault("task %s has a recorded cancellation intent and cannot accept updates", row.ID)
	}
	if len(inputs) > 0 {
		scope, err := row.decodeScope()
		if err != nil {
			return nil, err
		}
		if _, err := s.validateArtifacts(ctx, unit, scope.toContract(), inputs); err != nil {
			return nil, err
		}
	}
	now := s.clock.Now().UTC()
	row.InputsJSON = string(mustJSON(nonNilRefs(inputs)))
	if hasOutcome {
		row.Outcome = outcome
	}
	row.Version++
	row.UpdatedAt = now
	if err := updateTaskContract(ctx, unit, row); err != nil {
		return nil, err
	}
	if err := s.emitTaskEvent(ctx, unit, row, "tasks.task.updated", map[string]any{
		"task_id": row.ID, "version": row.Version,
	}); err != nil {
		return nil, err
	}
	return row, nil
}

// manualAcceptance records an explicitly labeled manual decision from an
// eligible principal and moves the task to succeeded or failed. The label
// stays manual forever; it never reads as automated verification.
func (s *Service) manualAcceptance(ctx context.Context, unit contract.Unit, row *taskRow, decision string, evidenceIDs []contract.ID, reason string) (*taskRow, error) {
	acceptance, err := row.decodeAcceptance()
	if err != nil {
		return nil, err
	}
	if acceptance.Mode != acceptanceModeManual {
		return nil, invalidInput("task %s requires independent verification; manual acceptance applies only to manual contracts", row.ID)
	}
	if !row.ManualAcceptance {
		return nil, invalidInput("task %s does not permit manual acceptance", row.ID)
	}
	if decision != "accept" && decision != "reject" {
		return nil, invalidInput("decision %q is not accept or reject", decision)
	}
	if isTerminal(row.State) {
		return nil, conflictFault("task %s is terminal in state %s and cannot be decided again", row.ID, row.State)
	}
	if row.State == stateDraft {
		return nil, conflictFault("task %s has not started; manual acceptance waits for a live attempt", row.ID)
	}

	// Eligibility: policy must allow the manual acceptance capability and
	// reviews must confirm the principal is an eligible reviewer for this
	// exact action digest. A false or missing eligibility is never
	// permission.
	actionDigest := manualActionDigest(row, decision)
	var policy peerPolicyCheckOut
	if err := s.callPeer(ctx, unit, "_policy.check", peerPolicyCheckIn{
		Scope:      scopeFromContract(unit.Scope()),
		Capability: "tasks.manual_acceptance",
	}, &policy); err != nil {
		return nil, err
	}
	if policy.Resource.Decision != "allow" {
		return nil, permissionDenied("manual acceptance is not allowed by policy: %s", policy.Resource.Decision)
	}
	var reviews peerReviewsCheckOut
	if err := s.callPeer(ctx, unit, "_reviews.check", peerReviewsCheckIn{
		Scope:        scopeFromContract(unit.Scope()),
		ActionDigest: string(actionDigest),
	}, &reviews); err != nil {
		return nil, err
	}
	if !reviews.Eligible {
		return nil, permissionDenied("principal is not an eligible reviewer for this manual acceptance")
	}

	// Evidence submitted with a manual decision is validated and recorded
	// like any other, but it never establishes observations.
	if _, err := s.recordEvidence(ctx, unit, row, evidenceIDs, s.clock.Now().UTC()); err != nil {
		return nil, err
	}

	now := s.clock.Now().UTC()
	if err := insertDecision(ctx, unit, &decisionRow{
		ID:           s.ids.New(),
		TaskID:       row.ID,
		Installation: row.InstallationID,
		Decision:     decision,
		ReviewerID:   unit.Actor().PrincipalID,
		ActionDigest: string(actionDigest),
		Reason:       reason,
		DecidedAt:    now,
	}); err != nil {
		return nil, err
	}
	fromState := row.State
	if decision == "accept" {
		row.State = stateSucceeded
		row.EstablishedBy = establishedManual
	} else {
		row.State = stateFailed
	}
	row.Version++
	row.UpdatedAt = now
	if err := updateTaskState(ctx, unit, row); err != nil {
		return nil, err
	}
	if err := s.recordTransitionLog(ctx, unit, row, fromState, &transitionRequest{
		TaskID: row.ID, Target: row.State, EvidenceIDs: evidenceIDs, Manual: true,
	}); err != nil {
		return nil, err
	}
	if err := s.emitTaskEvent(ctx, unit, row, "tasks.task.manual_decision", map[string]any{
		"task_id": row.ID, "decision": decision, "manual": true, "reviewer_id": unit.Actor().PrincipalID,
	}); err != nil {
		return nil, err
	}
	return row, nil
}

// manualActionDigest binds the manual decision to the exact task, version,
// decision and sealed acceptance.
func manualActionDigest(row *taskRow, decision string) contract.Digest {
	return contract.Hash([]byte(strings.Join([]string{
		"tasks.manual_acceptance/v1",
		string(row.ID), strconv.FormatInt(row.Version, 10), decision, row.AcceptanceDigest,
	}, "|")))
}

func isTerminal(state string) bool {
	return state == stateSucceeded || state == stateFailed || state == stateCancelled
}

// mustDecodeScope is decodeScope for rows already known to hold valid scope
// JSON; it degrades a decode failure to an empty scope rather than
// aborting an assignment.
func mustDecodeScope(row *taskRow) wireScope {
	sc, err := row.decodeScope()
	if err != nil {
		return wireScope{}
	}
	return sc
}
