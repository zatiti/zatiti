package execution

import (
	"context"
	"encoding/json"

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

	// A paused worker blocks new admissions without inference or spending.
	gate, err := loadGate(ctx, unit, in.WorkerID)
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
		if len(rows) == 1 && rows[0].WorkerID == in.WorkerID {
			return s.claimReplay(ctx, unit, r, rows[0])
		}
		return contract.Outcome[claimBody]{}, conflict("run already has a live attempt owner")
	}
	if r.WorkerID != in.WorkerID {
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

	// Required executor guarantees: the declared capabilities must cover the
	// pinned execution profile's capabilities.
	snapshot, err := s.callScopeSnapshot(ctx, unit, in.Scope)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	executor := "cooperative"
	if snapshot.Worker != nil && snapshot.Worker.Profile != nil {
		executor = snapshot.Worker.Profile.Executor
		declared := make(map[string]bool, len(in.Capabilities))
		for _, c := range in.Capabilities {
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
	// the claim back and the run stays ready.
	amount := wireMoney{}
	if snapshot.Worker != nil && snapshot.Worker.Profile != nil {
		amount = snapshot.Worker.Profile.CostBound
	}
	rootTaskID := task.RootID
	if rootTaskID == "" {
		rootTaskID = task.ID
	}
	reservation, err := s.reserveBudget(ctx, unit, r.Scope, rootTaskID, "", amount, task.Limits)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}

	// Generations count the run's prior attempts: a replacement claim after
	// a fenced attempt is a new generation and can never revive the old one.
	gen, err := maxAttemptGeneration(ctx, unit, r.ID)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}

	now := s.now()
	a := &attemptRow{
		ReservationID:      reservation.ID,
		ReservationVersion: reservation.Version,
		ID:                 s.newID(),
		Version:            1,
		RunID:              r.ID,
		TaskID:             r.TaskID,
		WorkerID:           in.WorkerID,
		Executor:           executor,
		InstallationID:     r.InstallationID,
		OrganizationID:     r.OrganizationID,
		ProjectID:          r.ProjectID,
		TaskScopeID:        r.TaskScopeID,
		WorkerScopeID:      in.WorkerID,
		Scope:              r.Scope,
		Generation:         gen + 1,
		LeaseID:            s.newID(),
		LeaseExpiresAt:     leaseExpiryAt(now, task.Limits.RootDeadline),
		LastHeartbeat:      now,
		State:              "claimed",
		Capabilities:       nonEmptyStrings(in.Capabilities),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := insertAttempt(ctx, unit, a); err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	if err := insertLease(ctx, unit, &leaseRow{
		ID: a.LeaseID, AttemptID: a.ID, RunID: r.ID, WorkerID: in.WorkerID,
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
	envelope, err := s.claimContextRef(ctx, unit, r, a)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	return completedOutcome(claimBody{Attempt: attemptOut(a), Task: task, Context: envelope})
}

// claimReplay answers a repeated claim by the current worker with the
// original disposition: the same attempt, lease and generation, and the
// context lineage available at the time of the answer. No duplicate owner
// is created merely because an acknowledgement was lost.
func (s *Service) claimReplay(ctx context.Context, unit contract.Unit, r *runRow, a *attemptRow) (contract.Outcome[claimBody], error) {
	task, err := s.callTaskSnapshot(ctx, unit, r.Scope, r.TaskID)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	envelope, err := s.claimContextRef(ctx, unit, r, a)
	if err != nil {
		return contract.Outcome[claimBody]{}, err
	}
	return completedOutcome(claimBody{Attempt: attemptOut(a), Task: task, Context: envelope})
}

// claimContextRef resolves the claim's context envelope: the attempt's own
// pinned context when one exists, else the newest checkpoint context of the
// run's prior attempts, else the deterministic digest envelope of the
// pinned inputs. The controller replaces the envelope with the persisted
// request context before dispatch.
func (s *Service) claimContextRef(ctx context.Context, unit contract.Unit, r *runRow, a *attemptRow) (wireArtifactRef, error) {
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
	bundle, err := canonicalJSON(struct {
		TaskID        contract.ID      `json:"task_id"`
		TaskVersion   contract.Version `json:"task_version"`
		Revision      contract.Version `json:"configuration_revision"`
		InputVersions []wireRef        `json:"input_versions"`
	}{r.TaskID, r.TaskVersion, r.ConfigurationRevision, r.InputVersions})
	if err != nil {
		return wireArtifactRef{}, err
	}
	digest := sha256Hex(bundle)
	return wireArtifactRef{ID: uuidFromDigest(digest), Digest: digest}, nil
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

// handleRunExport is the run.export boundary: commit the authorized bounded
// export as a durable job. The eventual result is the run history artifact,
// published through the job pipeline.
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
	inputJSON, err := canonicalJSON(map[string]any{
		"scope":  in.Scope,
		"run_id": in.ID,
	})
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	prior, err := findJobBySourceID(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	if prior != nil {
		if prior.InputHash != string(sha256Hex(inputJSON)) {
			return contract.Outcome[jobBody]{}, conflict("run already has an export job with different bounds")
		}
		return completedOutcome(jobBody{Resource: jobOut(prior)})
	}
	now := s.now()
	j := &jobRow{
		ID:               s.newID(),
		Version:          1,
		Kind:             "run_export",
		State:            "pending",
		InstallationID:   r.InstallationID,
		OrganizationID:   r.OrganizationID,
		ProjectID:        r.ProjectID,
		Scope:            in.Scope,
		Owner:            ownerName,
		Operation:        opRunExport,
		Input:            inputJSON,
		InputHash:        string(sha256Hex(inputJSON)),
		SourceID:         in.ID,
		Requirements:     []wireRequirement{},
		CompletionSchema: schemaExportResult,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := insertJob(ctx, unit, j); err != nil {
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
