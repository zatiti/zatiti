package installation

import (
	"context"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Requirement codes this package names in doctor/status beyond the
// pre-existing effects_unavailable/accounting_unavailable/
// prerequisite_missing set. Each is a distinct, inspectable category (P25
// item 4) rather than one generic "something is pending" signal:
//   - workerTurnStalledCode: a durable worker turn's Operation is still
//     outstanding (CallbackRoute.Kind "worker_turn") -- the turn cannot
//     progress until this effect resolves.
//   - jobUnclaimedCode: an Operation routes back to a job
//     (CallbackRoute.Kind "job") that is waiting on it -- the job cannot be
//     claimed complete until this effect resolves.
//   - effectPendingCode: every other outstanding Operation (memory/skill/
//     connection routing, or no callback route at all) -- unchanged from
//     the prior single-bucket behavior.
//   - memoryUnavailableCode: _memory.manifest could not be reached.
//   - liabilityUnknownCode: accounting carries an unconfirmed
//     (Usage.Unknown) amount -- a charge that may or may not have
//     happened and cannot yet be released or billed.
const (
	workerTurnStalledCode = "worker_turn_stalled"
	jobUnclaimedCode      = "job_unclaimed"
	effectPendingCode     = "effect_pending"
	memoryUnavailableCode = "memory_unavailable"
	liabilityUnknownCode  = "liability_unknown"
)

// requirementsFromPending turns _effects.pending's operations into visible
// Status/Job requirements naming what remains unresolved, without leaking
// their action payloads. An operation whose callback_route identifies it as
// a worker turn or a job gets its own named category so an operator (or a
// worker reading its own doctor output) can tell "a turn is stalled" apart
// from "a job is unclaimed" apart from every other outstanding effect,
// instead of one undifferentiated bucket.
func requirementsFromPending(ops []peerOperation) []wireRequirement {
	out := make([]wireRequirement, 0, len(ops))
	for _, op := range ops {
		id := op.ID
		code := effectPendingCode
		noun := "operation"
		if op.CallbackRoute != nil {
			switch op.CallbackRoute.Kind {
			case "worker_turn":
				code = workerTurnStalledCode
				noun = "worker turn"
			case "job":
				code = jobUnclaimedCode
				noun = "job"
			}
		}
		out = append(out, wireRequirement{
			Code:       code,
			Message:    noun + " " + string(op.ID) + " is " + op.State,
			ResourceID: &id,
		})
	}
	return out
}

// gatherRequirements assembles the best-effort diagnostic requirements for
// status/doctor and for the pending-effects fencing check in
// maintenance.enter: a peer being unavailable degrades to a named
// requirement rather than failing the whole read, since these are
// diagnostics, not authorization decisions.
func (s *Service) gatherRequirements(ctx context.Context, unit contract.Unit, scope wireScope) []wireRequirement {
	var reqs []wireRequirement
	if ops, err := s.effectsPending(ctx, unit, 50); err != nil {
		reqs = append(reqs, wireRequirement{Code: "effects_unavailable", Message: err.Error()})
	} else {
		reqs = append(reqs, requirementsFromPending(ops)...)
	}
	if acc, err := s.accountingInspect(ctx, unit, scope); err != nil {
		reqs = append(reqs, wireRequirement{Code: "accounting_unavailable", Message: err.Error()})
	} else {
		if acc.Limits.Currency == "" || acc.Limits.SpendMicroUnits == 0 {
			reqs = append(reqs, wireRequirement{
				Code:    "prerequisite_missing",
				Message: "currency and spend limits are not configured; paid execution remains unavailable",
			})
			// Until a budget is configured, the only admissible task is a
			// zero-spend one, and it names the installed verifier to
			// reference rather than one the operator must invent. The
			// sequence is printed in full: an operator copies it rather
			// than discovering it refusal by refusal.
			reqs = append(reqs, wireRequirement{
				Code: firstTaskRequirementCode,
				Message: "a first task is possible now as a zero-spend draft; run, in order:\n" +
					FirstTaskSequence,
			})
		}
		// An unconfirmed amount is an outstanding liability: it may or may
		// not have been charged by the provider, so it cannot be released
		// as unused or billed as spent until authoritative evidence
		// arrives (contract: "Preserve outcome_unknown and its reservation
		// until authoritative evidence").
		if acc.Usage.Unknown > 0 {
			reqs = append(reqs, wireRequirement{
				Code: liabilityUnknownCode,
				Message: fmt.Sprintf("%d %s of usage is unconfirmed and held as an outstanding liability pending authoritative evidence",
					acc.Usage.Unknown, acc.Usage.Currency),
			})
		}
	}
	if manifest, err := s.memoryManifest(ctx, unit, scope); err != nil {
		reqs = append(reqs, wireRequirement{Code: memoryUnavailableCode, Message: err.Error()})
	} else {
		reqs = append(reqs, manifest.Obligations...)
	}
	if s.backup == nil {
		reqs = append(reqs, wireRequirement{
			Code:    "prerequisite_missing",
			Message: "no database backup capability is bound; installation.backup and installation.restore are unavailable",
		})
	}
	if reqs == nil {
		reqs = []wireRequirement{}
	}
	return reqs
}

// runtimeReady reports whether this installation's own lifecycle state
// currently permits ordinary admission: initialized, not paused, not in
// maintenance. It deliberately stops there -- whether a paid model step or
// a durable task can actually run depends on process-assembly facts
// (adapter profiles, attached job runners, the trusted verifier) that only
// cmd/zatiti's own startup readiness classification observes (see
// cmd/zatiti/readiness.go's storage_only/chat_ready/task_ready levels,
// explicitly scoped apart from this package's doctor/status); installation
// never fabricates that broader signal from data it cannot see.
func runtimeReady(initialized, paused, maintenance bool) *bool {
	ready := initialized && !paused && !maintenance
	return &ready
}

// handleStatus serves both installation.status and installation.doctor: the
// brief embeds identical descriptions and schemas for the two, so both CLI
// names route to the one acknowledged-state, secret-free diagnostic view.
func handleStatus(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[scopeInput](s, inv.Operation, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	st, err := loadState(ctx, unit)
	if err != nil {
		return contract.Payload{}, err
	}
	if st == nil {
		return contract.Payload{}, notFound("installation is not initialized")
	}
	reqs := s.gatherRequirements(ctx, unit, in.Scope)
	return completed(resourceOut[wireStatus]{Resource: wireStatus{
		InstallationID: st.ID,
		Generation:     unit.Generation(),
		Paused:         st.Paused,
		Maintenance:    st.Maintenance,
		Initialized:    true,
		Requirements:   reqs,
		Version:        contract.Version(st.Version),
		RuntimeReady:   runtimeReady(true, st.Paused, st.Maintenance),
	}})
}

// handleJobGet inspects a backup or restore job this package created,
// answering entirely from its own local shadow of the durable job.
func handleJobGet(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[jobGetInput](s, opJobGet, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	j, err := loadJob(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if j == nil || j.InstallationID != in.Scope.InstallationID {
		return contract.Payload{}, notFound("job %s is unknown in this installation", in.ID)
	}
	out := j.wire()
	if j.Kind == "restore" {
		// Surface what this package preserved toward the recovery overlay it
		// could not publish (see restore.go), so an inspector can see the
		// obligations survived even though the encrypted artifact did not.
		obligations, err := listObligationsByJob(ctx, unit, j.ID)
		if err != nil {
			return contract.Payload{}, err
		}
		for _, ob := range obligations {
			resourceID := ob.ResourceID
			out.Requirements = append(out.Requirements, wireRequirement{
				Code:       "recovery_obligation_preserved",
				Message:    ob.Owner + "." + ob.Kind + " preserved as " + ob.State,
				ResourceID: &resourceID,
			})
		}
	}
	return completed(resourceOut[wireJob]{Resource: out})
}

// loadRequiredState loads the singleton state row and enforces the caller's
// expected_version against it, failing prerequisite_missing if the
// installation is somehow not yet initialized (unreachable in practice: the
// application layer only dispatches this operation once an installation
// exists) and stale_version on a version mismatch.
func loadRequiredState(ctx context.Context, unit contract.Unit, expected contract.Version) (*stateRow, error) {
	st, err := loadState(ctx, unit)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, prerequisiteMissing("installation is not initialized")
	}
	if contract.Version(st.Version) != expected {
		return nil, staleVersion("installation is at version %d, not the expected %d", st.Version, expected)
	}
	return st, nil
}

// handleMaintenanceEnter immediately blocks new admissions, fences current
// execution attempts at the installation's generation and records unresolved
// effects as inspectable requirements. It never waits on a model call, a
// configuration compile or a spend reservation, and it never claims an
// uncooperative external process actually stopped.
func handleMaintenanceEnter(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[versionedScopeInput](s, opMaintenanceEnter, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	st, err := loadRequiredState(ctx, unit, in.ExpectedVersion)
	if err != nil {
		return contract.Payload{}, err
	}
	if _, err := s.executionFence(ctx, unit, unit.Generation(), "installation.maintenance.enter"); err != nil {
		return contract.Payload{}, err
	}
	previous := st.Version
	st.Paused = true
	st.Maintenance = true
	st.Version++
	st.UpdatedAt = s.deps.Clock.Now()
	if err := updateState(ctx, unit, *st, previous); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventMaintenanceEnter, st.ID, contract.Version(st.Version)); err != nil {
		return contract.Payload{}, err
	}
	reqs := s.gatherRequirements(ctx, unit, in.Scope)
	return completed(resourceOut[wireStatus]{Resource: wireStatus{
		InstallationID: st.ID, Generation: unit.Generation(), Paused: st.Paused,
		Maintenance: st.Maintenance, Initialized: true, Requirements: reqs, Version: contract.Version(st.Version),
		RuntimeReady: runtimeReady(true, st.Paused, st.Maintenance),
	}})
}

// handlePause immediately blocks new admissions without fencing running
// work; unlike maintenance, it does not require exclusive ownership for
// backup/restore.
func handlePause(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[versionedScopeInput](s, opPause, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	st, err := loadRequiredState(ctx, unit, in.ExpectedVersion)
	if err != nil {
		return contract.Payload{}, err
	}
	previous := st.Version
	st.Paused = true
	st.Version++
	st.UpdatedAt = s.deps.Clock.Now()
	if err := updateState(ctx, unit, *st, previous); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventPaused, st.ID, contract.Version(st.Version)); err != nil {
		return contract.Payload{}, err
	}
	reqs := s.gatherRequirements(ctx, unit, in.Scope)
	return completed(resourceOut[wireStatus]{Resource: wireStatus{
		InstallationID: st.ID, Generation: unit.Generation(), Paused: st.Paused,
		Maintenance: st.Maintenance, Initialized: true, Requirements: reqs, Version: contract.Version(st.Version),
		RuntimeReady: runtimeReady(true, st.Paused, st.Maintenance),
	}})
}

// handleResume rechecks current authority (already revalidated by the
// application dispatcher for this transaction's actor) and recovery
// prerequisites: it refuses to resume while an unresolved restore job is
// still pending or running, and it never claims in-flight bytes from before
// the pause were retracted.
func handleResume(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[versionedScopeInput](s, opResume, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	st, err := loadRequiredState(ctx, unit, in.ExpectedVersion)
	if err != nil {
		return contract.Payload{}, err
	}
	unresolved, err := unresolvedRestoreJobs(ctx, unit, in.Scope.InstallationID)
	if err != nil {
		return contract.Payload{}, err
	}
	if len(unresolved) > 0 {
		return contract.Payload{}, prerequisiteMissing(
			"%d restore job(s) remain unresolved; resolve or record them before resuming", len(unresolved))
	}
	previous := st.Version
	st.Paused = false
	st.Maintenance = false
	st.Version++
	st.UpdatedAt = s.deps.Clock.Now()
	if err := updateState(ctx, unit, *st, previous); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventResumed, st.ID, contract.Version(st.Version)); err != nil {
		return contract.Payload{}, err
	}
	reqs := s.gatherRequirements(ctx, unit, in.Scope)
	return completed(resourceOut[wireStatus]{Resource: wireStatus{
		InstallationID: st.ID, Generation: unit.Generation(), Paused: st.Paused,
		Maintenance: st.Maintenance, Initialized: true, Requirements: reqs, Version: contract.Version(st.Version),
		RuntimeReady: runtimeReady(true, st.Paused, st.Maintenance),
	}})
}

// handleRestoreRecord is the internal seam the controller reports through
// after it independently performs the file-level IO and verification this
// package cannot reach itself (see restore.go). It only updates this
// package's own local job bookkeeping; it never claims the installation
// changed generation or that provider effects were undone.
func handleRestoreRecord(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[restoreRecordInput](s, opRestoreRecordName, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	j, err := loadJob(ctx, unit, in.JobID)
	if err != nil {
		return contract.Payload{}, err
	}
	if j == nil || j.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, notFound("job %s is unknown in this installation", in.JobID)
	}
	if j.Kind != "restore" {
		return contract.Payload{}, invalidInput("job %s is not a restore job", in.JobID)
	}
	previous := j.Version
	j.State = in.State
	j.Requirements = in.Requirements
	j.Version++
	j.UpdatedAt = s.deps.Clock.Now()
	if err := updateJob(ctx, unit, *j, previous); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventRestoreRecorded, j.ID, contract.Version(j.Version)); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[wireJob]{Resource: j.wire()})
}

// unresolvedRestoreJobs lists restore jobs for this installation that have
// not yet reached a terminal state.
func unresolvedRestoreJobs(ctx context.Context, r contract.Reader, installationID contract.ID) ([]contract.ID, error) {
	rows, err := r.QueryContext(ctx, `
		SELECT id FROM installation_jobs
		WHERE installation_id = ? AND kind = 'restore' AND state IN ('pending','running')`,
		string(installationID))
	if err != nil {
		return nil, internalError("list unresolved restore jobs: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []contract.ID
	for rows.Next() {
		var id contract.ID
		if err := rows.Scan(&id); err != nil {
			return nil, internalError("scan unresolved restore job: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, internalError("list unresolved restore jobs: %v", err)
	}
	return out, nil
}
