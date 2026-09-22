package execution

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Attempt lifecycle: the worker-driven mutations (heartbeat, checkpoint,
// report) share the identity fence — bound worker, current lease, own
// generation, open lease window — and the read operations serve authorized
// bounded views. A report enters independent verification; neither a
// report nor a process exit succeeds a task directly.

// verificationDeadline bounds how long a verification job may wait for the
// trusted verifier's result.
const verificationDeadline = 5 * time.Minute

// handleAttemptGet is the attempt.get boundary.
func (s *Service) handleAttemptGet(ctx context.Context, unit contract.Unit, in getIDInput) (contract.Outcome[attemptBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	a, err := loadAttempt(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := narrowAttemptScope(in.Scope, a); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	return completedOutcome(attemptBody{Resource: attemptOut(a)})
}

// handleAttemptList is the attempt.list boundary: scope and structured
// exact-match filters apply before pagination.
func (s *Service) handleAttemptList(ctx context.Context, unit contract.Unit, in listInput) (contract.Outcome[attemptListBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[attemptListBody]{}, err
	}
	limit := int64(defaultListLimit)
	if in.Limit != nil {
		limit = *in.Limit
	}
	conds := []string{"installation_id = ?"}
	args := []any{in.Scope.InstallationID}
	org := in.Scope.OrganizationID
	filter := in.Filter
	if filter != nil {
		if filter.Key != "" || filter.ParentID != "" || filter.Descendants != nil || filter.NeedsYou != nil {
			return contract.Outcome[attemptListBody]{}, invalidInput("filter field is not supported for attempts")
		}
		if filter.Organization != "" {
			if org != "" && filter.Organization != org {
				return contract.Outcome[attemptListBody]{}, permissionDenied(
					"filter organization crosses the caller's scope")
			}
			org = filter.Organization
		}
		if filter.State != "" {
			conds = append(conds, "state = ?")
			args = append(args, filter.State)
		}
		if filter.WorkerID != "" {
			conds = append(conds, "worker_id = ?")
			args = append(args, filter.WorkerID)
		}
		if filter.TaskID != "" {
			conds = append(conds, "task_id = ?")
			args = append(args, filter.TaskID)
		}
	}
	if org != "" {
		conds = append(conds, "organization_id = ?")
		args = append(args, org)
	}
	if in.Cursor != "" {
		created, lastID, err := s.readCursor(opAttemptList, unit, filter, in.Cursor)
		if err != nil {
			return contract.Outcome[attemptListBody]{}, err
		}
		conds = append(conds, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, formatStamp(created), formatStamp(created), lastID)
	}
	rows, err := listAttempts(ctx, unit, conds, args, limit+1)
	if err != nil {
		return contract.Outcome[attemptListBody]{}, err
	}
	var next *string
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		cursor, cerr := s.mintCursor(opAttemptList, unit, filter, last.CreatedAt, last.ID, s.now())
		if cerr != nil {
			return contract.Outcome[attemptListBody]{}, cerr
		}
		next = &cursor
	}
	items := make([]wireAttempt, 0, len(rows))
	for _, a := range rows {
		items = append(items, attemptOut(a))
	}
	return contract.Outcome[attemptListBody]{Status: contract.StatusCompleted, Data: attemptListBody{Items: items}, NextCursor: next}, nil
}

// handleAttemptRecovery is the attempt.recovery boundary: inspect the
// attempt with its unresolved effect and cost obligations before
// replacement.
func (s *Service) handleAttemptRecovery(ctx context.Context, unit contract.Unit, in getIDInput) (contract.Outcome[recoveryBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[recoveryBody]{}, err
	}
	a, err := loadAttempt(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[recoveryBody]{}, err
	}
	if err := narrowAttemptScope(in.Scope, a); err != nil {
		return contract.Outcome[recoveryBody]{}, err
	}
	obligations, err := unresolvedObligations(ctx, unit, "attempt_id", a.ID)
	if err != nil {
		return contract.Outcome[recoveryBody]{}, err
	}
	return completedOutcome(recoveryBody{Resource: attemptOut(a), Obligations: obligations})
}

// handleAttemptHeartbeat is the attempt.heartbeat boundary: extend the
// current valid lease only for the bound identity and generation. A stale
// heartbeat cannot revive a fenced or expired attempt.
func (s *Service) handleAttemptHeartbeat(ctx context.Context, unit contract.Unit, in heartbeatInput) (contract.Outcome[attemptBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	a, err := loadAttemptForUpdate(ctx, unit, in.AttemptID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := narrowAttemptScope(in.Scope, a); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	now := s.now()
	if err := checkWorkerCall(unit, a, in.Scope, in.LeaseID, in.Generation, now); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	a.LeaseExpiresAt = now.Add(leaseDuration)
	a.LastHeartbeat = now
	a.UpdatedAt = now
	if err := updateAttempt(ctx, unit, a); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := touchLease(ctx, unit, a.LeaseID, a.LeaseExpiresAt, now); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	return completedOutcome(attemptBody{Resource: attemptOut(a)})
}

// handleAttemptCheckpoint is the attempt.checkpoint boundary: persist the
// reconstructable context and output disposition at a safe boundary with
// pinned versions.
func (s *Service) handleAttemptCheckpoint(ctx context.Context, unit contract.Unit, in checkpointInput) (contract.Outcome[attemptBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	a, err := loadAttemptForUpdate(ctx, unit, in.AttemptID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := narrowAttemptScope(in.Scope, a); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	now := s.now()
	if err := checkWorkerCall(unit, a, in.Scope, in.LeaseID, in.Generation, now); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	outputs := in.Outputs
	if outputs == nil {
		outputs = []wireArtifactRef{}
	}
	if err := insertCheckpoint(ctx, unit, s.newID(), a, in.Context, outputs, now); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	ref := in.Context
	a.ContextArtifact = &ref
	a.Outputs = outputs
	a.UpdatedAt = now
	if err := updateAttempt(ctx, unit, a); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	return completedOutcome(attemptBody{Resource: attemptOut(a)})
}

// handleAttemptReport is the attempt.report boundary: persist observations
// and enter independent verification. A report never succeeds the task
// directly; the trusted verifier path transitions it.
func (s *Service) handleAttemptReport(ctx context.Context, unit contract.Unit, in reportInput) (contract.Outcome[attemptBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	a, err := loadAttemptForUpdate(ctx, unit, in.AttemptID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := narrowAttemptScope(in.Scope, a); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	// The public cooperative path carries no named bindings (the frozen
	// attempt.report/_execution.report wire schema is a bare ArtifactRef
	// list): reportAttempt binds each reported output positionally against
	// the task's own named required-output slots.
	return s.reportAttempt(ctx, unit, a, in.LeaseID, in.Generation, in.Outputs, nil, in.Observations, in.Usage)
}

// reportAttempt is the report/verification transition shared by the public
// cooperative attempt.report and the controller-driven internal
// _execution.report: persist observations, seal the independent
// verification request and enter verifying. Neither path directly succeeds
// the task; only the trusted verifier path (_execution.verification.record)
// does. named carries the report_outputs proposal's own name->artifact
// bindings when the caller already has them (P16's interpretReportOutputs);
// nil for the public path, which has no names on the wire and binds each
// reported output positionally against the task's own RequiredOutputs.
func (s *Service) reportAttempt(ctx context.Context, unit contract.Unit, a *attemptRow, leaseID contract.ID, generation int64, outputs []wireArtifactRef, named []reportBindingProposal, observations json.RawMessage, usage wireUsage) (contract.Outcome[attemptBody], error) {
	now := s.now()
	if err := checkWorkerCall(unit, a, contract.Scope{WorkerID: a.WorkerID}, leaseID, generation, now); err != nil {
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

	// Resolve named output slots to published attempt artifacts before the
	// request is sealed: report_outputs already carries real names (P16's
	// interpretReportOutputs), and the public path binds each reported
	// output positionally against the task's own RequiredOutputs. A name
	// with nothing reported for it is a legitimate omission the trusted
	// verifier independently gates on; a reported binding that does not
	// resolve to a real published artifact refuses the whole report -- it
	// can never seal a fabricated claim into the request.
	bindings := named
	if bindings == nil {
		bindings = make([]reportBindingProposal, 0, len(outputs))
		for i, ref := range outputs {
			name := ""
			if i < len(task.RequiredOutputs) {
				name = task.RequiredOutputs[i]
			}
			bindings = append(bindings, reportBindingProposal{Name: name, Artifact: ref})
		}
	}
	resolvedOutputs, err := s.resolveOutputArtifacts(ctx, unit, r.Scope, bindings)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}

	now = s.now()
	a.Observations = append(a.Observations, wireObservation{
		Disposition: "accepted",
		Evidence:    observations,
		Usage:       usage,
		ConfirmedAt: formatStamp(now),
	})
	a.Outputs = outputs
	a.State = "reported"
	a.UpdatedAt = now
	if err := updateAttempt(ctx, unit, a); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}

	acceptanceJSON, err := canonicalJSON(task.Acceptance)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	jobID := s.newID()
	request := wireVerificationRequest{
		Schema:               "zatiti.verification-request/v1",
		JobID:                jobID,
		TaskID:               task.ID,
		AttemptID:            a.ID,
		Scope:                r.Scope,
		AcceptanceDigest:     sha256Hex(acceptanceJSON),
		Profile:              task.Acceptance.Profile,
		SealedInputs:         task.Acceptance.SealedInputs,
		Outputs:              resolvedOutputs,
		ExpectedObservations: task.Acceptance.ExpectedObservations,
		Deadline:             formatStamp(now.Add(verificationDeadline)),
	}
	requestJSON, err := canonicalJSON(request)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := insertVerificationJob(ctx, unit, jobID, a, requestJSON, sha256Hex(acceptanceJSON), now); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if usage.Unknown > 0 || usage.Advisory {
		// Uncertain cost stays in accounting: the reservation is not released
		// without conclusive usage evidence, and recovery surfaces the debt.
		if err := insertObligation(ctx, unit, s.newID(), "cost_unresolved",
			a.InstallationID, a.RunID, a.ID, a.ID,
			"report carried unknown or advisory cost; settle through accounting before close-out", now); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
	} else if a.ReservationID != "" {
		if err := s.settleBudget(ctx, unit, a.ReservationID, a.ReservationVersion,
			usage, false); err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
	}
	if _, err := s.transitionTask(ctx, unit, task.ID, task.Version, "verifying", []contract.ID{}, "", false); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	r.State = "verifying"
	r.UpdatedAt = now
	if err := updateRun(ctx, unit, r); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventAttemptReported, a.ID, a.Version); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventVerificationRequested, jobID, 1); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventRunVerifying, r.ID, r.Version); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	return completedOutcome(attemptBody{Resource: attemptOut(a)})
}

// handleAttemptCancel is the attempt.cancel boundary: stop the attempt,
// retire its lease and cancel the owning run. An effect dispatched without
// an observation is preserved as an unknown-effect obligation; nothing here
// asserts that an external process stopped.
func (s *Service) handleAttemptCancel(ctx context.Context, unit contract.Unit, in cancelInput) (contract.Outcome[dispositionBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	a, err := loadAttemptForUpdate(ctx, unit, in.ID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	if err := narrowAttemptScope(in.Scope, a); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	if a.State == "fenced" || a.State == "stopped" {
		return contract.Outcome[dispositionBody]{}, conflict("attempt is already %s", a.State)
	}

	now := s.now()
	open, err := openOperationOf(ctx, unit, a.ID)
	if err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	if open != nil {
		if err := insertObligation(ctx, unit, s.newID(), "unknown_effect",
			a.InstallationID, a.RunID, a.ID, open.ID,
			"attempt cancelled while a dispatched effect had no observation", now); err != nil {
			return contract.Outcome[dispositionBody]{}, err
		}
	}
	a.State = "stopped"
	a.RecoveryReason = in.Reason
	a.UpdatedAt = now
	if a.LeaseID != "" {
		if err := retireLease(ctx, unit, a.LeaseID, "released"); err != nil {
			return contract.Outcome[dispositionBody]{}, err
		}
	}
	if err := updateAttempt(ctx, unit, a); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventAttemptStopped, a.ID, a.Version); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	r, err := loadRun(ctx, unit, a.RunID)
	if err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	r.State = "cancelled"
	r.UpdatedAt = now
	if err := updateRun(ctx, unit, r); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventRunCancelled, r.ID, r.Version); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	task, err := s.callTaskSnapshot(ctx, unit, r.Scope, r.TaskID)
	if err == nil && task.State != "cancelled" {
		if _, err := s.transitionTask(ctx, unit, task.ID, task.Version, "cancelled", []contract.ID{}, "", false); err != nil {
			return contract.Outcome[dispositionBody]{}, err
		}
	}
	return completedOutcome(dispositionBody{Resource: wireDisposition{
		ID: a.ID, Version: a.Version, State: a.State,
	}})
}
