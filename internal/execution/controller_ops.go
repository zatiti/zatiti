package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Controller operations: the trusted loop that pins request context, admits
// bounded owned work, expires leases, delivers effect observations and
// records the independent verifier's verdict. Every method here is internal;
// the controller identity gates them, and the verification path rechecks the
// exact accepted verifier before independently transitioning the task.

// handleContext is the _execution.context boundary: pin the persisted
// request context (its artifact reference, revision and capture bounds)
// before any model effect may be prepared for the attempt.
func (s *Service) handleContext(ctx context.Context, unit contract.Unit, in contextInput) (contract.Outcome[attemptBody], error) {
	a, err := loadAttempt(ctx, unit, in.AttemptID)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if a.InstallationID != installationOf(unit) {
		return contract.Outcome[attemptBody]{}, permissionDenied(
			"attempt %s belongs to another installation", a.ID)
	}
	if a.State != "claimed" && a.State != "running" && a.State != "waiting" {
		return contract.Outcome[attemptBody]{}, conflict("attempt is %s and cannot accept context", a.State)
	}
	if in.Context.AttemptID != "" && in.Context.AttemptID != a.ID {
		return contract.Outcome[attemptBody]{}, invalidInput(
			"context names attempt %s, not %s", in.Context.AttemptID, a.ID)
	}
	r, err := loadRun(ctx, unit, a.RunID)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if in.Context.ConfigurationRevision != r.ConfigurationRevision {
		return contract.Outcome[attemptBody]{}, staleVersion(
			"context is pinned to configuration revision %d; the run is pinned to revision %d",
			in.Context.ConfigurationRevision, r.ConfigurationRevision)
	}
	now := s.now()
	if err := insertLineage(ctx, unit, s.newID(), a, "initial",
		string(in.Context.Artifact.ID), in.Context.Artifact.Digest, "", now); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	ref := in.Context.Artifact
	a.ContextArtifact = &ref
	a.UpdatedAt = now
	if err := updateAttempt(ctx, unit, a); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	return completedOutcome(attemptBody{Resource: attemptOut(a)})
}

// handleFence is the _execution.fence boundary: fence every live attempt of
// this installation claimed under a controller generation below the given
// one and record recovery obligations. A superseded controller can never
// continue an attempt it claimed; the fence never asserts that its external
// process stopped.
func (s *Service) handleFence(ctx context.Context, unit contract.Unit, in fenceInput) (contract.Outcome[fenceBody], error) {
	rows, err := listAttempts(ctx, unit,
		[]string{"installation_id = ?", "generation < ?", "state IN " + liveStateSQL()},
		[]any{installationOf(unit), in.Generation}, 4096)
	if err != nil {
		return contract.Outcome[fenceBody]{}, err
	}
	now := s.now()
	ids := []contract.ID{}
	for _, a := range rows {
		if err := insertObligation(ctx, unit, s.newID(), "lease_conflict",
			a.InstallationID, a.RunID, a.ID, a.ID,
			"fenced by generation advance: "+in.Reason, now); err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
		a.State = "fenced"
		a.RecoveryReason = in.Reason
		a.UpdatedAt = now
		if a.LeaseID != "" {
			if err := retireLease(ctx, unit, a.LeaseID, "expired"); err != nil {
				return contract.Outcome[fenceBody]{}, err
			}
		}
		if err := updateAttempt(ctx, unit, a); err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
		if err := emitTransition(ctx, unit, eventAttemptFenced, a.ID, a.Version); err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
		if err := s.waitRun(ctx, unit, a.RunID, now); err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
		ids = append(ids, a.ID)
	}

	// A restart also fences every claimed WorkerTurn bound to a stale
	// generation: an old controller can never continue a decision stream it
	// claimed. Steps used and limits are untouched — a root's cumulative
	// spend is never reset by a fence, only the lease/claim binding is.
	// Pending (never claimed) and waiting (already parked) turns hold no
	// live claim to fence; work.claim's own generation check and wake
	// revalidation cover them.
	turns, err := listTurns(ctx, unit,
		[]string{"installation_id = ?", "generation < ?",
			"state IN ('claimed','context_pending','model_pending','proposal_pending')"},
		[]any{installationOf(unit), in.Generation}, 4096)
	if err != nil {
		return contract.Outcome[fenceBody]{}, err
	}
	for _, turn := range turns {
		turn.State = "waiting"
		turn.WaitingReason = waitingRecovery
		turn.LeaseID = ""
		turn.LeaseExpiresAt = time.Time{}
		turn.NextWake = now
		turn.UpdatedAt = now
		if err := updateTurn(ctx, unit, turn); err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
		if err := emitTransition(ctx, unit, eventTurnFenced, turn.ID, turn.Version); err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
	}
	return completedOutcome(fenceBody{AttemptIDs: ids})
}

// waitRun returns a non-terminal run to waiting so a replacement attempt
// may be claimed after recovery.
func (s *Service) waitRun(ctx context.Context, unit contract.Unit, runID contract.ID, now time.Time) error {
	r, err := loadRun(ctx, unit, runID)
	if err != nil {
		return err
	}
	if terminalRunStates[r.State] || r.State == "waiting" || r.State == "verifying" {
		return nil
	}
	r.State = "waiting"
	r.UpdatedAt = now
	return updateRun(ctx, unit, r)
}

// handleObservation is the _execution.observation boundary. For a hosted
// attempt bound to a durable WorkerTurn (revision 3's turn/proposal
// pipeline, auto-claimed only through work.claim -- never a plain
// cooperative run.claim), P16's interpretation stage owns this call: it
// strictly validates the delivered ModelOutput and interprets it into
// bounded governed work instead of blindly preparing another model step.
// For every other attempt (the pre-turn hosted loop -- controller_ops.go's
// own prepareModelEffect/handleTick, audit finding G05, still driven only
// through plain run.claim -- see docs/roadmap.md 2026-09-21 P15 landing
// note), this operation's original, unconditional continue-the-loop
// behavior is unchanged below: retiring that separate path is a bigger,
// coordinated change this card does not own.
func (s *Service) handleObservation(ctx context.Context, unit contract.Unit, in observationInput) (contract.Outcome[attemptBody], error) {
	a, err := loadAttempt(ctx, unit, in.AttemptID)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if a.InstallationID != installationOf(unit) {
		return contract.Outcome[attemptBody]{}, permissionDenied(
			"attempt %s belongs to another installation", a.ID)
	}
	if a.State != "claimed" && a.State != "running" && a.State != "waiting" {
		return contract.Outcome[attemptBody]{}, conflict("attempt is %s and cannot continue its loop", a.State)
	}
	if turn, terr := findTurnByAttemptID(ctx, unit, a.ID); terr != nil {
		return contract.Outcome[attemptBody]{}, terr
	} else if turn != nil {
		// A third branch, checked before interpretTurnObservation's own
		// ModelOutput-specific validation (execution-dispatch-model-step, the
		// same-day P22 gap fix): this turn's own recorded dispatch bookkeeping
		// (execution_turn_dispatches, keyed by exact operation_ref) says
		// whether the delivered observation answers a prepare_session effect
		// -- a structurally different ResponsesEvidence document, never a
		// ModelOutput -- rather than inferring it from turn state, which a
		// redelivered/superseded observation could get wrong.
		dispatch, derr := loadTurnDispatchByRef(ctx, unit, turn.ID, string(in.OperationID))
		if derr != nil {
			return contract.Outcome[attemptBody]{}, derr
		}
		if dispatch != nil && dispatch.Kind == "prepare_session" {
			return s.interpretPrepareSessionObservation(ctx, unit, turn, a, dispatch, in)
		}
		return s.interpretTurnObservation(ctx, unit, turn, a, in)
	}
	// The delivery names the effects operation the controller dispatched;
	// the owned record is resolved by attempt and that reference.
	op, err := loadOperationRecordByRef(ctx, unit, a.ID, string(in.OperationID))
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if op.State == "recorded" || op.State == "failed" {
		return contract.Outcome[attemptBody]{}, conflict(
			"operation %s is %s and has no pending observation", op.ID, op.State)
	}

	now := s.now()
	switch in.Observation.Disposition {
	case "succeeded", "accepted":
		if err := updateOperationRecord(ctx, unit, op.ID, "recorded"); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
	case "failed", "not_sent":
		if err := updateOperationRecord(ctx, unit, op.ID, "failed"); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
	default: // "unknown"
		if err := insertObligation(ctx, unit, s.newID(), "unknown_effect",
			a.InstallationID, a.RunID, a.ID, op.ID,
			"effect outcome reported unknown; inspect the provider reference before replacement", now); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		if err := updateOperationRecord(ctx, unit, op.ID, "recorded"); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
	}

	if op.Kind == "model_step" {
		a.ModelStepsUsed++
	}
	obs := in.Observation
	obs.ConfirmedAt = formatStamp(now)
	a.Observations = append(a.Observations, obs)
	a.UpdatedAt = now
	if err := updateAttempt(ctx, unit, a); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}

	// The recorded observation continues the bounded model loop: a succeeded
	// model step prepares the next one until the task's step bound is
	// reached; a failed, not-sent or unknown effect stops the attempt for
	// recovery instead of guessing a replacement dispatch.
	if op.Kind != "model_step" {
		return completedOutcome(attemptBody{Resource: attemptOut(a)})
	}
	switch in.Observation.Disposition {
	case "succeeded", "accepted":
		r, err := loadRun(ctx, unit, a.RunID)
		if err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		task, err := s.callTaskSnapshot(ctx, unit, r.Scope, r.TaskID)
		if err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		if task.Limits.ModelSteps > 0 && a.ModelStepsUsed >= task.Limits.ModelSteps {
			if err := s.fenceAttempt(ctx, unit, a, "", "",
				fmt.Sprintf("model step bound %d reached", task.Limits.ModelSteps), now); err != nil {
				return contract.Outcome[attemptBody]{}, err
			}
			return completedOutcome(attemptBody{Resource: attemptOut(a)})
		}
		if task.CancellationRequested {
			return completedOutcome(attemptBody{Resource: attemptOut(a)})
		}
		snapshot, err := s.callScopeSnapshot(ctx, unit, r.Scope)
		if err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		if snapshot.Worker != nil && snapshot.Worker.Profile != nil {
			if err := s.prepareModelEffect(ctx, unit, r, a, snapshot.Worker.Profile, task, now); err != nil {
				return contract.Outcome[attemptBody]{}, err
			}
		}
	default:
		// The unknown case already recorded its unknown-effect obligation
		// above; failed and not-sent are conclusive. None leaves anything
		// further to reconcile, so the fence carries no extra obligation.
		reason := "model effect " + in.Observation.Disposition
		if err := s.fenceAttempt(ctx, unit, a, "", "", reason, now); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
	}
	return completedOutcome(attemptBody{Resource: attemptOut(a)})
}

// handleTick is the _execution.tick boundary: expire stale leases, then
// admit bounded owned work. Admission requires a pinned request context;
// hosted attempts get their model effect prepared here, after the context
// artifact exists. No network happens inside the transaction.
func (s *Service) handleTick(ctx context.Context, unit contract.Unit, in tickInput) (contract.Outcome[fenceBody], error) {
	now, err := parseStamp(in.Now)
	if err != nil {
		return contract.Outcome[fenceBody]{}, invalidInput("tick time %q is not a valid RFC3339 timestamp", in.Now)
	}
	if in.Limit < 1 || in.Limit > 100 {
		return contract.Outcome[fenceBody]{}, invalidInput("tick limit must be between 1 and 100")
	}

	ids := []contract.ID{}

	// Phase 1: expire leases. A stale lease fences the attempt but never
	// proves the external process stopped.
	expired, err := listLeaseExpiredAttempts(ctx, unit, installationOf(unit), now, in.Limit)
	if err != nil {
		return contract.Outcome[fenceBody]{}, err
	}
	for _, a := range expired {
		if err := s.fenceAttempt(ctx, unit, a, "lease_conflict",
			"lease expired; recovery required before a replacement attempt",
			"lease expired", now); err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
		ids = append(ids, a.ID)
	}

	// Phase 2: admit claimed attempts that carry a pinned context, bounded
	// by the remaining tick budget and the task's concurrency limit.
	remaining := in.Limit - int64(len(expired))
	if remaining <= 0 {
		return completedOutcome(fenceBody{AttemptIDs: ids})
	}
	rows, err := listAttempts(ctx, unit,
		[]string{"installation_id = ?", "state = 'claimed'"},
		[]any{installationOf(unit)}, remaining)
	if err != nil {
		return contract.Outcome[fenceBody]{}, err
	}
	for _, a := range rows {
		if a.ContextArtifact == nil {
			continue
		}
		r, err := loadRun(ctx, unit, a.RunID)
		if err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
		if r.State != "running" {
			continue
		}
		task, err := s.callTaskSnapshot(ctx, unit, r.Scope, r.TaskID)
		if err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
		if task.CancellationRequested {
			continue
		}
		if task.Limits.Concurrency > 0 {
			// The count includes this attempt (claimed is a live state), so
			// admission stays within the bound exactly when the count does
			// not exceed it.
			live, err := countLiveAttempts(ctx, unit, "worker_id", string(a.WorkerID))
			if err != nil {
				return contract.Outcome[fenceBody]{}, err
			}
			if live > task.Limits.Concurrency {
				continue
			}
		}
		a.State = "running"
		a.UpdatedAt = now
		if err := updateAttempt(ctx, unit, a); err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
		snapshot, err := s.callScopeSnapshot(ctx, unit, r.Scope)
		if err != nil {
			return contract.Outcome[fenceBody]{}, err
		}
		if snapshot.Worker != nil && snapshot.Worker.Profile != nil {
			if err := s.prepareModelEffect(ctx, unit, r, a, snapshot.Worker.Profile, task, now); err != nil {
				return contract.Outcome[fenceBody]{}, err
			}
		}
		ids = append(ids, a.ID)
	}
	return completedOutcome(fenceBody{AttemptIDs: ids})
}

// prepareModelEffect prepares the hosted loop's model effect through the
// effects owner and records the execution-owned operation row the
// observation path later closes.
func (s *Service) prepareModelEffect(ctx context.Context, unit contract.Unit, r *runRow, a *attemptRow, profile *wireExecutionProfile, task wireTask, now time.Time) error {
	action := map[string]any{
		"scope":                  r.Scope,
		"tool":                   map[string]any{"id": profile.ID, "version": profile.Version},
		"connection":             map[string]any{"id": profile.ConnectionID, "version": 1},
		"account_identity":       "",
		"destination":            profile.ProviderDestination,
		"content":                []any{},
		"not_before":             formatStamp(now),
		"expires_at":             formatStamp(a.LeaseExpiresAt),
		"preconditions":          map[string]any{},
		"configuration_revision": r.ConfigurationRevision,
		"parameters":             map[string]any{"attempt_id": a.ID, "run_id": r.ID, "model": profile.Model},
		"cost_bound":             profile.CostBound,
	}
	data, err := s.callPeer(ctx, unit, peerEffectsPrepare, map[string]any{
		"scope":     r.Scope,
		"action":    action,
		"source_id": a.ID,
	})
	if err != nil {
		return err
	}
	op, err := decodeResource[struct {
		ID contract.ID `json:"id"`
	}]("effects operation", data)
	if err != nil {
		return err
	}
	step, err := canonicalJSON(map[string]any{
		"attempt_id": a.ID, "run_id": r.ID, "effects_operation": op.ID,
	})
	if err != nil {
		return err
	}
	return insertOperationRecord(ctx, unit, s.newID(), a, "model_step", "prepared",
		string(op.ID), json.RawMessage(step), now)
}

// fenceAttempt fences one attempt: record the recovery obligation when the
// fence leaves something external to reconcile, mark the attempt with its
// recovery reason, retire its lease and return the run to waiting. An empty
// obligation kind records no obligation — the stop is conclusive and the
// recovery story lives in the attempt's recovery reason.
func (s *Service) fenceAttempt(ctx context.Context, unit contract.Unit, a *attemptRow, obligationKind, obligationMessage, reason string, now time.Time) error {
	if obligationKind != "" {
		if err := insertObligation(ctx, unit, s.newID(), obligationKind,
			a.InstallationID, a.RunID, a.ID, a.ID, obligationMessage, now); err != nil {
			return err
		}
	}
	a.State = "fenced"
	a.RecoveryReason = reason
	a.UpdatedAt = now
	if a.LeaseID != "" {
		if err := retireLease(ctx, unit, a.LeaseID, "expired"); err != nil {
			return err
		}
	}
	if err := updateAttempt(ctx, unit, a); err != nil {
		return err
	}
	if err := emitTransition(ctx, unit, eventAttemptFenced, a.ID, a.Version); err != nil {
		return err
	}
	r, err := loadRun(ctx, unit, a.RunID)
	if err != nil {
		return err
	}
	if terminalRunStates[r.State] || r.State == "waiting" || r.State == "verifying" {
		return nil
	}
	r.State = "waiting"
	r.UpdatedAt = now
	if err := updateRun(ctx, unit, r); err != nil {
		return err
	}
	// The attempt is dead, so the task's running state is stale: execution
	// set it at claim and nothing else can clear it. Returning the task to
	// waiting with the fence reason keeps "running" meaning a live attempt,
	// so recovery can admit a fresh generation. A task already moved to
	// verifying or a terminal state by another path is left alone.
	task, err := s.callTaskSnapshot(ctx, unit, r.Scope, r.TaskID)
	if err != nil {
		return err
	}
	if task.State != "running" {
		return nil
	}
	_, err = s.transitionTask(ctx, unit, task.ID, task.Version, "waiting",
		[]contract.ID{a.ID}, reason, false)
	return err
}

// sumUsage aggregates the observed usage of an attempt for settlement.
func sumUsage(observations []wireObservation) wireUsage {
	out := wireUsage{}
	for _, obs := range observations {
		if obs.Usage.Currency != "" {
			out.Currency = obs.Usage.Currency
		}
		out.Spent += obs.Usage.Spent
		out.Reserved += obs.Usage.Reserved
		out.Estimated += obs.Usage.Estimated
		out.Unknown += obs.Usage.Unknown
		out.Advisory = out.Advisory || obs.Usage.Advisory
	}
	return out
}

// effectiveVerdict recomputes the overall verification verdict from the
// stored request's expected checks against the observed checks. A pass
// requires every expected check observed as required; unavailability,
// tampering and mismatches fail conservatively.
func effectiveVerdict(expected []wireExpectedObservation, observed []wireObservedCheck) (string, string) {
	byID := make(map[string]wireObservedCheck, len(observed))
	for _, obs := range observed {
		byID[obs.CheckID] = obs
	}
	tampered := ""
	unavailable := ""
	mismatch := ""
	for _, want := range expected {
		obs, ok := byID[want.CheckID]
		if !ok || obs.Status == "unavailable" {
			if unavailable == "" {
				unavailable = want.CheckID
			}
			continue
		}
		required := "passed"
		if want.Expected == "fail" {
			required = "failed"
		}
		if obs.Status == "tampered" {
			if tampered == "" {
				tampered = want.CheckID
			}
			continue
		}
		if obs.Status != required {
			if mismatch == "" {
				mismatch = want.CheckID
			}
		}
	}
	switch {
	case tampered != "":
		return "tampered", "check " + tampered + " observed a digest mismatch"
	case unavailable != "":
		return "prerequisite_missing", "check " + unavailable + " could not be observed"
	case mismatch != "":
		return "failed", "check " + mismatch + " did not observe the required outcome"
	}
	return "passed", ""
}

// handleVerificationRecord is the _execution.verification.record boundary:
// the only trusted path to task completion. The result is rechecked against
// the stored request — task, attempt, acceptance digest, verifier identity
// and request artifact — before the task transitions independently.
func (s *Service) handleVerificationRecord(ctx context.Context, unit contract.Unit, in verificationRecordInput) (contract.Outcome[attemptBody], error) {
	a, err := loadAttemptForUpdate(ctx, unit, in.AttemptID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if a.InstallationID != installationOf(unit) {
		return contract.Outcome[attemptBody]{}, permissionDenied(
			"attempt %s belongs to another installation", a.ID)
	}
	if a.State != "reported" {
		return contract.Outcome[attemptBody]{}, conflict(
			"attempt is %s; verification requires a reported attempt", a.State)
	}
	v, err := loadVerificationJob(ctx, unit, a.ID)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if v.State != "pending" {
		return contract.Outcome[attemptBody]{}, conflict(
			"verification job is %s; a verdict is already recorded", v.State)
	}
	var request wireVerificationRequest
	if err := decodeJSON(string(v.Request), &request); err != nil {
		return contract.Outcome[attemptBody]{}, verificationFailed(
			"stored verification request does not decode: %v", err)
	}
	result := in.Result

	// Integrity rechecks: exact request binding.
	switch {
	case result.Schema != "zatiti.verification-result/v1":
		return contract.Outcome[attemptBody]{}, verificationFailed(
			"result schema %q is not the accepted verification result", result.Schema)
	case result.JobID != v.ID || result.AttemptID != v.AttemptID || result.TaskID != v.TaskID:
		return contract.Outcome[attemptBody]{}, verificationFailed(
			"result does not address the recorded verification job")
	case result.AcceptanceDigest != v.AcceptanceDigest ||
		result.AcceptanceDigest != request.AcceptanceDigest:
		return contract.Outcome[attemptBody]{}, verificationFailed(
			"acceptance digest does not match the pinned acceptance contract")
	case !result.Independent:
		return contract.Outcome[attemptBody]{}, verificationFailed(
			"verification result does not assert independence")
	}
	// Verifier identity and code digest must match the accepted profile.
	var profile wireProfileCore
	if err := decodeJSON(string(request.Profile), &profile); err != nil {
		return contract.Outcome[attemptBody]{}, verificationFailed(
			"pinned verifier profile does not decode: %v", err)
	}
	if result.VerifierID != profile.ID || result.VerifierVersion != profile.Version ||
		result.VerifierCodeDigest != profile.CodeDigest {
		return contract.Outcome[attemptBody]{}, verificationFailed(
			"verifier identity does not match the accepted profile")
	}
	// The result must bind the exact request artifact by digest.
	if result.RequestArtifact.Digest != sha256Hex(v.Request) {
		return contract.Outcome[attemptBody]{}, verificationFailed(
			"request artifact digest does not match the pinned verification request")
	}

	// Recompute the verdict from the sealed expectations.
	recomputed, explanation := effectiveVerdict(request.ExpectedObservations, result.Observations)
	if result.Status == "passed" && recomputed != "passed" {
		return contract.Outcome[attemptBody]{}, verificationFailed(
			"verifier status passed contradicts observed checks: %s", explanation)
	}
	effective := result.Status
	switch {
	case effective == "interrupted":
		// The verifier's own signal that it did not finish is preserved
		// distinctly: recomputing a verdict from whatever partial
		// observations an interrupted run happened to collect cannot
		// manufacture a more complete outcome than "it did not finish" --
		// collapsing it into prerequisite_missing would lose exactly the
		// distinction the interruption itself carries.
	case recomputed != "passed" && recomputed != "failed":
		// Tampering or missing evidence overrides a mere failure report.
		effective = recomputed
	}
	result.Status = effective

	now := s.now()
	resultJSON, err := canonicalJSON(result)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	v.State = effective
	if err := updateVerificationJob(ctx, unit, v, resultJSON, now); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventVerificationRecorded, v.ID, 1); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}

	r, err := loadRun(ctx, unit, a.RunID)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	task, err := s.callTaskSnapshot(ctx, unit, r.Scope, r.TaskID)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}

	// Cost settlement: a conclusive usage report settled the reservation at
	// report time. Uncertain cost kept the reservation open; the verdict is
	// the authoritative evidence accounting settles against.
	usage := sumUsage(a.Observations)
	heldCost := a.ReservationID != "" && (usage.Unknown > 0 || usage.Advisory)
	if heldCost {
		if err := s.settleBudget(ctx, unit, a.ReservationID, a.ReservationVersion, usage, false); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
	}

	// A designated required child that has not itself independently
	// succeeded blocks the parent's acceptance. The V1 verifier runner only
	// observes artifact/repository checks -- it cannot see a sibling task's
	// state -- so this task-config gate runs here, after the verifier's own
	// genuine finding is already durably recorded above: the verification
	// job keeps the honest "passed" the artifact checks actually earned,
	// while the task itself is still refused a success transition.
	if effective == "passed" {
		for _, childID := range task.Acceptance.RequiredChildIDs {
			child, err := s.callTaskSnapshot(ctx, unit, r.Scope, childID)
			if err != nil {
				return contract.Outcome[attemptBody]{}, err
			}
			if child.State != "succeeded" {
				effective = "failed"
				explanation = fmt.Sprintf("required child %s has not independently succeeded (state %q)", childID, child.State)
				break
			}
		}
	}

	switch effective {
	case "passed":
		if usage.Unknown == 0 && !usage.Advisory {
			// The cost question the obligations raised is conclusively
			// settled; close exactly those. Unknown-effect obligations stay
			// open for recovery regardless of the verdict.
			if err := resolveObligations(ctx, unit, a.ID, "cost_unresolved", now); err != nil {
				return contract.Outcome[attemptBody]{}, err
			}
		}
		// Publish the sealed request as a real artifact and hand the
		// trusted verdict to tasks as evidence before the success
		// transition: only this recorded lineage, or eligible explicit
		// manual acceptance, may ever establish succeeded.
		updatedTask, err := s.recordVerificationEvidence(ctx, unit, r.Scope, task.ID, task.Version, a.ID, v.Request, request, "passed")
		if err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		if task.Acceptance.Mode == "manual" {
			if _, err := s.transitionTask(ctx, unit, updatedTask.ID, updatedTask.Version, "waiting",
				[]contract.ID{v.ID}, "manual_acceptance", true); err != nil {
				return contract.Outcome[attemptBody]{}, err
			}
			r.State = "waiting"
		} else {
			if _, err := s.transitionTask(ctx, unit, updatedTask.ID, updatedTask.Version, "succeeded",
				[]contract.ID{v.ID}, "", false); err != nil {
				return contract.Outcome[attemptBody]{}, err
			}
			r.State = "succeeded"
			if err := emitTransition(ctx, unit, eventRunSucceeded, r.ID, r.Version); err != nil {
				return contract.Outcome[attemptBody]{}, err
			}
		}
	case "failed":
		a.State = "failed"
		a.RecoveryReason = "verification " + effective + ": " + explanation
		a.UpdatedAt = now
		if err := updateAttempt(ctx, unit, a); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		// A definitive check failure is real evidence too: record it before
		// the task's failed transition, the same as a pass.
		updatedTask, err := s.recordVerificationEvidence(ctx, unit, r.Scope, task.ID, task.Version, a.ID, v.Request, request, "failed")
		if err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		if _, err := s.transitionTask(ctx, unit, updatedTask.ID, updatedTask.Version, "failed",
			[]contract.ID{v.ID}, "", false); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		r.State = "failed"
		if err := emitTransition(ctx, unit, eventRunFailed, r.ID, r.Version); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
	case "tampered":
		// A tampered result is not trustworthy evidence -- never handed to
		// tasks as if it were a genuine verdict. Unlike an unavailable
		// verifier or an interrupted run (below), tampering is a
		// definitive, adversarial outcome: the task is fenced directly to
		// failed, not left open for a retry to reach a decisive verdict.
		a.State = "failed"
		a.RecoveryReason = "verification " + effective + ": " + explanation
		a.UpdatedAt = now
		if err := updateAttempt(ctx, unit, a); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		if _, err := s.transitionTask(ctx, unit, task.ID, task.Version, "failed",
			[]contract.ID{v.ID}, "", false); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		r.State = "failed"
		if err := emitTransition(ctx, unit, eventRunFailed, r.ID, r.Version); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
	default:
		// prerequisite_missing (the verifier itself, or an expected check,
		// was unavailable) and interrupted are inconclusive, not a
		// definitive failure: the task and run are left exactly as they
		// were (verifying) so a future attempt can still reach a decisive
		// verdict. Success is prevented either way -- neither state is
		// "succeeded" -- but execution never spends a permanent task
		// failure on "the verifier could not finish or was unavailable."
		a.State = "failed"
		a.RecoveryReason = "verification " + effective + ": " + explanation
		a.UpdatedAt = now
		if err := updateAttempt(ctx, unit, a); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		return completedOutcome(attemptBody{Resource: attemptOut(a)})
	}
	r.UpdatedAt = now
	if err := updateRun(ctx, unit, r); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	return completedOutcome(attemptBody{Resource: attemptOut(a)})
}

// recordVerificationEvidence publishes the sealed verification request --
// already blob-published outside any Unit by the verifier's own
// stageRequest -- as a real, inspectable artifact, then hands it and the
// resolved named output bindings to tasks as the trusted evidence lineage
// for this verdict, before any task state depends on it. Publishing here
// registers domain metadata for bytes that already exist; it performs no
// blob IO itself, so it is safe inside this Unit-bound handler.
func (s *Service) recordVerificationEvidence(ctx context.Context, unit contract.Unit, scope contract.Scope, taskID contract.ID, taskVersion contract.Version, attemptID contract.ID, requestBytes json.RawMessage, request wireVerificationRequest, verdict string) (wireTask, error) {
	digest := sha256Hex(requestBytes)
	published, err := s.publishArtifact(ctx, unit, scope, digest, int64(len(requestBytes)),
		"application/json", "internal", true)
	if err != nil {
		return wireTask{}, err
	}
	bindings := make([]reportBindingProposal, 0, len(request.Outputs))
	for _, o := range request.Outputs {
		bindings = append(bindings, reportBindingProposal{Name: o.Name, Artifact: o.Artifact})
	}
	return s.recordTaskEvidence(ctx, unit, taskID, attemptID, taskVersion, request.AcceptanceDigest,
		wireArtifactRef{ID: published.ID, Digest: published.Digest}, bindings, verdict)
}
