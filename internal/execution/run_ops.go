package execution

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Run lifecycle: enqueue pins a ready task/version into a run, claim admits
// exactly one live attempt with its lease and budget envelope, cancel
// commits cancellation intent and fences governed work without asserting
// that any external process stopped, and the read/export operations serve
// authorized bounded views.

// handleEnqueue is the _execution.enqueue boundary: create or deduplicate
// the run for a ready task/version inside the caller's admission
// transaction. It pins the current configuration revision and input
// versions and never claims or dispatches.
func (s *Service) handleEnqueue(ctx context.Context, unit contract.Unit, in enqueueInput) (contract.Outcome[runBody], error) {
	if in.Task.Scope.InstallationID == "" {
		return contract.Outcome[runBody]{}, invalidInput("task scope must name the installation")
	}
	if err := checkInstallation(unit, in.Task.Scope.InstallationID); err != nil {
		return contract.Outcome[runBody]{}, err
	}
	if in.Task.ID == "" {
		return contract.Outcome[runBody]{}, invalidInput("enqueue requires a task identity")
	}
	if in.Task.State != "ready" {
		return contract.Outcome[runBody]{}, invalidInput("enqueue pins a ready task; the task is %s", in.Task.State)
	}
	workerID := in.Task.WorkerID
	if workerID == "" {
		workerID = in.Task.Scope.WorkerID
	}
	if workerID == "" {
		return contract.Outcome[runBody]{}, invalidInput("enqueue requires a bound worker")
	}

	// Dedup: one run per task/version identity; a repeated enqueue returns
	// the existing pinned run.
	prior, err := findRunByTask(ctx, unit, in.Task.ID, in.Task.Version)
	if err != nil {
		return contract.Outcome[runBody]{}, err
	}
	if prior != nil {
		return completedOutcome(runBody{Resource: runOut(prior)})
	}

	snapshot, err := s.callScopeSnapshot(ctx, unit, in.Task.Scope)
	if err != nil {
		return contract.Outcome[runBody]{}, err
	}
	inputs, err := s.pinInputVersions(ctx, unit, in.Task.Scope, in.Task.Inputs)
	if err != nil {
		return contract.Outcome[runBody]{}, err
	}

	now := s.now()
	r := &runRow{
		ID:                    s.newID(),
		Version:               1,
		TaskID:                in.Task.ID,
		TaskVersion:           in.Task.Version,
		InstallationID:        in.Task.Scope.InstallationID,
		OrganizationID:        in.Task.Scope.OrganizationID,
		ProjectID:             in.Task.Scope.ProjectID,
		WorkerID:              workerID,
		TaskScopeID:           in.Task.Scope.TaskID,
		Scope:                 in.Task.Scope,
		ConfigurationRevision: snapshot.Revision,
		InputVersions:         inputs,
		State:                 "ready",
		AttemptIDs:            []contract.ID{},
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := insertRun(ctx, unit, r); err != nil {
		return contract.Outcome[runBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventRunEnqueued, r.ID, r.Version); err != nil {
		return contract.Outcome[runBody]{}, err
	}

	// A hosted-executor task additionally admits a task-triggered
	// WorkerTurn in the same transaction, so the durable worker loop
	// (_execution.work.pending/.claim) can drive it without a human ever
	// calling run.claim. A cooperative-executor task never gets a turn: it
	// stays reachable only through the public run.claim path, structurally
	// ruling out an automatic hosted claim of a cooperative run.
	if snapshot.Worker != nil && snapshot.Worker.Profile != nil && snapshot.Worker.Profile.Executor == "hosted" {
		// The task's own pinned Limits is the accountable envelope for a
		// task-triggered turn — the same resource-limit source run.claim's
		// budget reservation already uses (task.Limits, not the worker's
		// general limits), so the turn's cumulative bound matches the one
		// governing its run.
		taskLimits := in.Task.Limits
		rootID := in.Task.RootID
		if rootID == "" {
			rootID = in.Task.ID
		}
		if _, _, err := s.admitTurn(ctx, unit, turnAdmitParams{
			Source: wireTurnSource{
				Kind: "task", SourceID: in.Task.ID, SourceVersion: in.Task.Version,
				RecipientWorkerID: workerID,
			},
			WorkerID:       workerID,
			Scope:          in.Task.Scope,
			RequesterID:    in.Task.OwnerID,
			ConfigRevision: snapshot.Revision,
			Limits:         &taskLimits,
			RootID:         rootID,
			TaskID:         in.Task.ID,
			RunID:          r.ID,
		}, now); err != nil {
			return contract.Outcome[runBody]{}, err
		}
	}
	return completedOutcome(runBody{Resource: runOut(r)})
}

// pinInputVersions resolves the task's input artifact references to pinned
// versions through the artifacts metadata boundary. A missing, unavailable
// or foreign input refuses the enqueue through the artifacts fault.
func (s *Service) pinInputVersions(ctx context.Context, unit contract.Unit, scope contract.Scope, inputs []wireArtifactRef) ([]wireRef, error) {
	out := make([]wireRef, 0, len(inputs))
	if len(inputs) == 0 {
		return out, nil
	}
	data, err := s.callPeer(ctx, unit, peerArtifactsMeta, map[string]any{
		"scope":     scope,
		"artifacts": inputs,
	})
	if err != nil {
		return nil, err
	}
	var body struct {
		Artifacts []wireArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, prerequisitesDecode(err)
	}
	byDigest := make(map[contract.Digest]wireArtifact, len(body.Artifacts))
	for _, a := range body.Artifacts {
		byDigest[a.Digest] = a
	}
	for _, in := range inputs {
		artifact, ok := byDigest[in.Digest]
		if !ok || artifact.ID != in.ID || artifact.State != "available" {
			return nil, prerequisiteMissing("pinned input %s is unavailable", in.ID)
		}
		out = append(out, wireRef{ID: artifact.ID, Version: artifact.Version})
	}
	return out, nil
}

// prerequisitesDecode wraps a malformed peer metadata response.
func prerequisitesDecode(err error) error {
	return prerequisiteMissing("pinned input metadata could not be read: %v", err)
}

// handleRunClaim is the run.claim boundary: atomically claim exactly one
// current attempt, reserve the budget envelope and bind the scoped caller,
// worker, lease and generation. A lost acknowledgement replays the original
// claim for the same worker; another worker's live ownership conflicts.
// Required executor guarantees the claim does not declare are refused.
func (s *Service) handleRunClaim(ctx context.Context, unit contract.Unit, in runClaimInput) (contract.Outcome[claimBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	if in.WorkerID == "" {
		return contract.Outcome[claimBody]{}, invalidInput("run.claim requires a worker identity")
	}
	if in.Scope.WorkerID != "" && in.Scope.WorkerID != in.WorkerID {
		return contract.Outcome[claimBody]{}, permissionDenied("scope worker %s does not match the claiming worker", in.Scope.WorkerID)
	}

	r, err := loadRunForUpdate(ctx, unit, in.RunID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	if err := narrowRunScope(in.Scope, r); err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	return s.admitAttempt(ctx, unit, r, in.WorkerID, in.Capabilities, s.now())
}

// admitAttempt performs the fenced attempt claim shared by the public
// run.claim path (an explicit cooperative or hosted caller) and the
// automatic hosted work.claim path (the controller claiming a queued hosted
// run without a human calling run.claim): worker-pause gate, one-owner
// fence and lost-ack replay, task/cancellation check, required-capability
// check, budget reservation, attempt/lease creation and run/task
// transition. declaredCapabilities is the caller's declared capability set
// — for an automatic hosted claim this is the worker's own profile
// capabilities, self-satisfying by construction.
func (s *Service) admitAttempt(ctx context.Context, unit contract.Unit, r *runRow, workerID contract.ID, declaredCapabilities []string, now time.Time) (contract.Outcome[claimBody], error) {
	// A paused worker blocks new admissions without inference or spending.
	gate, err := loadGate(ctx, unit, workerID)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	if gate.Paused {
		return contract.Outcome[claimBody]{}, conflict("worker admission is paused; resume the worker before claiming")
	}

	// One-owner fence first: a live attempt answers the same worker with the
	// original claim and any other worker with a conflict, before the
	// caller-scope binding below can mask the ownership fence.
	live, err := liveAttemptExists(ctx, unit, r.ID)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	if live {
		rows, err := listAttempts(ctx, unit,
			[]string{"run_id = ?", "state IN " + liveStateSQL()}, []any{r.ID}, 1)
		if err != nil {
			return contract.Outcome[claimBody]{}, err
		}
		if len(rows) == 1 && rows[0].WorkerID == workerID {
			return s.claimReplay(ctx, unit, r, rows[0])
		}
		return contract.Outcome[claimBody]{}, conflict("run already has a live attempt owner")
	}
	if r.WorkerID != workerID {
		return contract.Outcome[claimBody]{}, permissionDenied("run is bound to worker %s", r.WorkerID)
	}
	if !liveRunStates[r.State] {
		return contract.Outcome[claimBody]{}, conflict("run is %s and cannot be claimed", r.State)
	}

	task, err := s.callTaskSnapshot(ctx, unit, r.Scope, r.TaskID)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	if task.State != "ready" && task.State != "waiting" {
		return contract.Outcome[claimBody]{}, conflict("task is %s and cannot start an attempt", task.State)
	}
	if task.CancellationRequested {
		return contract.Outcome[claimBody]{}, conflict("task cancellation has been requested")
	}

	// Resuming a waiting run (a replacement attempt after the prior one was
	// fenced) is blocked until every unresolved-provider-effect obligation
	// the run carries is resolved (Z10.conflicting_replacement: "an expired
	// attempt may still control a repository resource or have an
	// unresolved provider effect"): a stale lease alone never proves the
	// prior external process stopped, so replacement cannot proceed on that
	// inference. This gates specifically on unknown_effect -- a genuinely
	// dispatched, unconfirmed effect a prior attempt left open -- and not
	// on the much more common lease_conflict obligation routine lease-expiry
	// or generation fencing always records (mere staleness of the claim
	// itself, not evidence of an in-flight external effect); a bare
	// lease_conflict alone never blocks a fresh replacement claim, matching
	// the existing lease-expiry-then-replace recovery flow. Resolution
	// comes only from the channel that actually resolves this kind (a later
	// conclusive _execution.observation -- see handleObservation), never a
	// claim-time shortcut; run.recovery/attempt.recovery expose the
	// obligation itself for inspection either way.
	if r.State == "waiting" {
		obligations, err := unresolvedObligations(ctx, unit, "run_id", r.ID)
		if err != nil {
			return contract.Outcome[claimBody]{}, err
		}
		unresolvedEffects := 0
		for _, o := range obligations {
			if o.Code == "unknown_effect" {
				unresolvedEffects++
			}
		}
		if unresolvedEffects > 0 {
			return contract.Outcome[claimBody]{}, prerequisiteMissing(
				"run has %d unresolved provider effect obligation(s); resolve them before a replacement attempt can be admitted -- see run.recovery",
				unresolvedEffects)
		}
	}

	// Root deadline: a fixed absolute bound no replacement attempt can push
	// out merely by being claimed again (Z12.root_limits_shared).
	if task.Limits.RootDeadline != "" {
		if deadline, derr := parseStamp(task.Limits.RootDeadline); derr == nil && !deadline.IsZero() && !now.Before(deadline) {
			return contract.Outcome[claimBody]{}, conflict(
				"task root deadline %s has passed; no further attempt may be claimed", task.Limits.RootDeadline)
		}
	}

	// Cumulative model steps: gated on the run's own running total, not the
	// fresh attempt's (which always starts at zero), so a worker cannot
	// accumulate more total model steps than the task allows just by being
	// replaced repeatedly.
	if task.Limits.ModelSteps > 0 && r.ModelStepsUsed >= task.Limits.ModelSteps {
		return contract.Outcome[claimBody]{}, conflict(
			"task model step bound %d already reached across %d prior attempt(s); no further attempt may be claimed",
			task.Limits.ModelSteps, len(r.AttemptIDs))
	}

	// Required executor guarantees: the declared capabilities must cover the
	// pinned execution profile's capabilities.
	snapshot, err := s.callScopeSnapshot(ctx, unit, r.Scope)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	executor := "cooperative"
	if snapshot.Worker != nil && snapshot.Worker.Profile != nil {
		executor = snapshot.Worker.Profile.Executor
		declared := make(map[string]bool, len(declaredCapabilities))
		for _, c := range declaredCapabilities {
			declared[c] = true
		}
		for _, required := range snapshot.Worker.Profile.Capabilities {
			if !declared[required] {
				return contract.Outcome[claimBody]{}, capabilityUnsupported(
					"claim does not declare the required executor capability %q", required)
			}
		}
	}

	// Reserve the budget envelope for the attempt; an accounting fault rolls
	// the claim back and the run stays ready. _accounting.reserve's own
	// frozen schema requires operation_id as a non-empty uuid, and no real
	// effects Operation exists yet at claim time (the worker has not chosen
	// what to do), so this mints a fresh identity for the reservation itself
	// -- the same pattern internal/tasks/admission.go's own budget
	// reservation already uses for the identical "no operation yet" case.
	amount := wireMoney{}
	if snapshot.Worker != nil && snapshot.Worker.Profile != nil {
		amount = snapshot.Worker.Profile.CostBound
	}
	rootTaskID := task.RootID
	if rootTaskID == "" {
		rootTaskID = task.ID
	}
	reservation, err := s.reserveBudget(ctx, unit, r.Scope, rootTaskID, s.newID(), amount, task.Limits)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}

	// The attempt binds the persisted controller generation it is claimed
	// under: a restart advances it and fences everything below. The run's
	// attempt list is the per-run counter; a replacement claim appends a
	// fresh attempt there and can never revive a fenced one.
	gen := unit.Generation()
	if gen < 1 {
		return contract.Outcome[claimBody]{}, prerequisiteMissing(
			"controller generation has not been started; a claim cannot bind generation %d", gen)
	}

	a := &attemptRow{
		ReservationID:      reservation.ID,
		ReservationVersion: reservation.Version,
		ID:                 s.newID(),
		Version:            1,
		RunID:              r.ID,
		TaskID:             r.TaskID,
		WorkerID:           workerID,
		Executor:           executor,
		InstallationID:     r.InstallationID,
		OrganizationID:     r.OrganizationID,
		ProjectID:          r.ProjectID,
		TaskScopeID:        r.TaskScopeID,
		WorkerScopeID:      workerID,
		Scope:              r.Scope,
		Generation:         gen,
		LeaseID:            s.newID(),
		LeaseExpiresAt:     leaseExpiryAt(now, task.Limits.RootDeadline),
		LastHeartbeat:      now,
		State:              "claimed",
		Capabilities:       nonEmptyStrings(declaredCapabilities),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := insertAttempt(ctx, unit, a); err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	if err := insertLease(ctx, unit, &leaseRow{
		ID: a.LeaseID, AttemptID: a.ID, RunID: r.ID, WorkerID: workerID,
		InstallationID: r.InstallationID, Generation: a.Generation, State: "active",
		ExpiresAt: a.LeaseExpiresAt, LastHeartbeat: now, CreatedAt: now,
	}); err != nil {
		return contract.Outcome[claimBody]{}, err
	}

	r.State = "running"
	r.AttemptIDs = append(r.AttemptIDs, a.ID)
	r.UpdatedAt = now
	if err := updateRun(ctx, unit, r); err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	if _, err := s.transitionTask(ctx, unit, task.ID, task.Version, "running", []contract.ID{a.ID}, "", false); err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventRunClaimed, r.ID, r.Version); err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventAttemptClaimed, a.ID, a.Version); err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	envelope, err := s.claimContextRef(ctx, unit, r, a, snapshot, now)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	return completedOutcome(claimBody{Attempt: attemptOut(a), Task: task, Context: envelope})
}

// claimReplay answers a repeated claim by the current worker with the
// original disposition: the same attempt, lease and generation, and the
// context lineage available at the time of the answer. No duplicate owner
// is created merely because an acknowledgement was lost. Recomputing the
// same deterministic claim-context bytes (when no real checkpoint exists
// yet) yields the identical digest/reference claimContextRef returned the
// first time, so a lost-ack replay is idempotent by content, not merely by
// a cached lookup.
func (s *Service) claimReplay(ctx context.Context, unit contract.Unit, r *runRow, a *attemptRow) (contract.Outcome[claimBody], error) {
	task, err := s.callTaskSnapshot(ctx, unit, r.Scope, r.TaskID)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	snapshot, err := s.callScopeSnapshot(ctx, unit, r.Scope)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	envelope, err := s.claimContextRef(ctx, unit, r, a, snapshot, s.now())
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	return completedOutcome(claimBody{Attempt: attemptOut(a), Task: task, Context: envelope})
}

// claimContextRef resolves the claim's context envelope: the attempt's own
// pinned context when one exists, else the newest checkpoint context of the
// run's prior attempts, else a durably published advisory claim-context
// document naming the worker's currently authorized bindings, the
// executor's required capabilities and an explicit advisory disclaimer
// (buildClaimContext) -- never a bare digest referencing bytes nothing ever
// staged. Publishing brand-new bytes cannot happen synchronously inside
// this Unit-bound call (AGENTS.md's transaction rules forbid filesystem/blob
// IO inside a Unit; see job_runner.go), so this seals the exact content by
// digest now and commits the durable document-publish job that completes
// it (job_runner.go's stageDocumentArtifact/recordDocumentJob) -- the
// reference returned here names precisely that content, not a fabrication.
// The controller replaces the envelope with the persisted request context
// before a hosted dispatch.
func (s *Service) claimContextRef(ctx context.Context, unit contract.Unit, r *runRow, a *attemptRow, snapshot peerScopeSnapshot, now time.Time) (wireArtifactRef, error) {
	if a.ContextArtifact != nil {
		return *a.ContextArtifact, nil
	}
	ref, err := latestRunCheckpoint(ctx, unit, r.ID)
	if err != nil {
		return wireArtifactRef{}, err
	}
	if ref != nil {
		return *ref, nil
	}

	var required []string
	if snapshot.Worker != nil && snapshot.Worker.Profile != nil {
		required = snapshot.Worker.Profile.Capabilities
	}
	raw, err := buildClaimContext(a, r, required, snapshot.Bindings)
	if err != nil {
		return wireArtifactRef{}, err
	}
	digest := sha256Hex(raw)
	envelope := wireArtifactRef{ID: uuidFromDigest(digest), Digest: digest}
	if _, err := s.enqueueDocumentPublish(ctx, unit, r.Scope, "claim_context", envelope.ID, raw,
		"application/json", "internal", now); err != nil {
		return wireArtifactRef{}, err
	}
	return envelope, nil
}

// handleRunGet is the run.get boundary.
func (s *Service) handleRunGet(ctx context.Context, unit contract.Unit, in getIDInput) (contract.Outcome[runBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[runBody]{}, err
	}
	r, err := loadRun(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[runBody]{}, err
	}
	if err := narrowRunScope(in.Scope, r); err != nil {
		return contract.Outcome[runBody]{}, err
	}
	return completedOutcome(runBody{Resource: runOut(r)})
}

// handleRunList is the run.list boundary: scope and structured exact-match
// filters apply before pagination; filter values are bound parameters, the
// column names are a fixed allowlist.
func (s *Service) handleRunList(ctx context.Context, unit contract.Unit, in listInput) (contract.Outcome[runListBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[runListBody]{}, err
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
			return contract.Outcome[runListBody]{}, invalidInput("filter field is not supported for runs")
		}
		if filter.Organization != "" {
			if org != "" && filter.Organization != org {
				return contract.Outcome[runListBody]{}, permissionDenied(
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
		created, lastID, err := s.readCursor(opRunList, unit, filter, in.Cursor)
		if err != nil {
			return contract.Outcome[runListBody]{}, err
		}
		conds = append(conds, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, formatStamp(created), formatStamp(created), lastID)
	}
	rows, err := listRuns(ctx, unit, conds, args, limit+1)
	if err != nil {
		return contract.Outcome[runListBody]{}, err
	}
	var next *string
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		cursor, cerr := s.mintCursor(opRunList, unit, filter, last.CreatedAt, last.ID, s.now())
		if cerr != nil {
			return contract.Outcome[runListBody]{}, cerr
		}
		next = &cursor
	}
	items := make([]wireRun, 0, len(rows))
	for _, r := range rows {
		items = append(items, runOut(r))
	}
	return contract.Outcome[runListBody]{Status: contract.StatusCompleted, Data: runListBody{Items: items}, NextCursor: next}, nil
}

// handleRunRecovery is the run.recovery boundary: inspect generation,
// leases, conflicting resources and effect obligations before replacement.
func (s *Service) handleRunRecovery(ctx context.Context, unit contract.Unit, in getIDInput) (contract.Outcome[recoveryBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[recoveryBody]{}, err
	}
	r, err := loadRun(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[recoveryBody]{}, err
	}
	if err := narrowRunScope(in.Scope, r); err != nil {
		return contract.Outcome[recoveryBody]{}, err
	}
	obligations, err := unresolvedObligations(ctx, unit, "run_id", r.ID)
	if err != nil {
		return contract.Outcome[recoveryBody]{}, err
	}
	return completedOutcome(recoveryBody{Resource: runOut(r), Obligations: obligations})
}

// handleRunCancel is the run.cancel boundary: commit cancellation intent,
// fence governed work as applicable and preserve uncertain effects. A
// dispatched operation left without an observation is never read as
// nonexecution.
func (s *Service) handleRunCancel(ctx context.Context, unit contract.Unit, in cancelInput) (contract.Outcome[dispositionBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	r, err := loadRunForUpdate(ctx, unit, in.ID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	if err := narrowRunScope(in.Scope, r); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	if terminalRunStates[r.State] {
		return contract.Outcome[dispositionBody]{}, conflict("run is already %s", r.State)
	}

	now := s.now()
	live, err := listAttempts(ctx, unit,
		[]string{"run_id = ?", "state IN " + liveStateSQL()}, []any{r.ID}, 4096)
	if err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	for _, a := range live {
		open, err := openOperationOf(ctx, unit, a.ID)
		if err != nil {
			return contract.Outcome[dispositionBody]{}, err
		}
		if open != nil {
			if err := insertObligation(ctx, unit, s.newID(), "unknown_effect",
				a.InstallationID, a.RunID, a.ID, open.ID,
				"run cancelled while a dispatched effect had no observation", now); err != nil {
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
		if _, err := s.transitionTask(ctx, unit, task.ID, task.Version, "cancelled", []contract.ID{r.ID}, "", false); err != nil {
			return contract.Outcome[dispositionBody]{}, err
		}
	}
	return completedOutcome(dispositionBody{Resource: wireDisposition{
		ID: r.ID, Version: r.Version, State: "cancelled",
	}})
}

// handleRunExport is the run.export boundary: assemble the run's full
// canonical history (accepted task, every attempt with its effects,
// outputs, observations and independently recorded verifier evidence, and
// the run's checkpoint lineage -- buildRunExportHistory, never a partial
// view of only the current attempt) and commit it as a durable bounded
// document-publish job. The eventual result is the run history artifact,
// published through the same job mechanism claim-context uses
// (job_runner.go); a repeated export while the run's history is unchanged
// replays the original job rather than growing a second one.
func (s *Service) handleRunExport(ctx context.Context, unit contract.Unit, in getIDInput) (contract.Outcome[jobBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	r, err := loadRun(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	if err := narrowRunScope(in.Scope, r); err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	// One export job per run: a repeated call replays the original,
	// already-committed durable job unconditionally, exactly like the
	// prior digest-only implementation and like claimReplay's own
	// lost-ack handling -- an export is a durable, immutable commitment to
	// a history snapshot, not silently recomputed (and its exported_at
	// stamp not silently drifted) on every repeated inspection.
	prior, err := findJobBySourceID(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	if prior != nil {
		return completedOutcome(jobBody{Resource: jobOut(prior)})
	}
	now := s.now()
	raw, err := s.buildRunExportHistory(ctx, unit, r, now)
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	j, err := s.enqueueDocumentPublish(ctx, unit, in.Scope, "run_export", in.ID, raw,
		"application/json", "internal", now)
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	return completedOutcome(jobBody{Resource: jobOut(j)})
}

// nonEmptyStrings guarantees a JSON array (never null) for optional lists.
func nonEmptyStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// time helpers shared by the tick and fence paths.
