package tasks

import (
	"context"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// Internal operations. Allowed internal callers (declared in the
// descriptors, enforced by the application): _tasks.create scheduling,
// messaging, execution; _tasks.ready execution, controller; _tasks.snapshot
// execution, effects, scheduling, memory, reviews, policy;
// _tasks.transition execution, scheduling, installation.

// handleTasksCreate creates or deduplicates an admitted task from a wake,
// responsibility or conversation occurrence. Identical content under the
// same source identity returns the existing task; differing content is a
// submission conflict.
func handleTasksCreate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Task          *wireTask `json:"task"`
		SourceID      string    `json:"source_id"`
		OccurrenceKey string    `json:"occurrence_key"`
	}](s, "_tasks.create", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.Task.ID == "" {
		in.Task.ID = s.ids.New()
	}
	if strings.TrimSpace(in.SourceID) == "" || strings.TrimSpace(in.OccurrenceKey) == "" {
		return contract.Payload{}, invalidInput("_tasks.create requires a non-empty source identity")
	}
	normalizeDefinition(in.Task)
	existing, err := getTaskBySource(ctx, unit, in.SourceID, in.OccurrenceKey)
	if err != nil {
		return contract.Payload{}, err
	}
	if existing != nil {
		digest := contract.Hash(mustJSON(in.Task))
		if existing.ContentDigest != string(digest) {
			return contract.Payload{}, submissionConflict(
				"occurrence %s/%s already admitted task %s with different content",
				in.SourceID, in.OccurrenceKey, existing.ID)
		}
		wire, err := existing.toWire()
		if err != nil {
			return contract.Payload{}, err
		}
		return s.completed(map[string]any{"resource": wire})
	}
	row, err := s.admitTask(ctx, unit, &admissionRequest{
		Definition:    in.Task,
		SourceID:      in.SourceID,
		OccurrenceKey: in.OccurrenceKey,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTasksReady is the bounded recovery scan of ready tasks without a
// current run. Execution deduplicates enqueue by task/version, so a task
// surfaced twice here cannot duplicate work.
func handleTasksReady(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Limit int64 `json:"limit"`
	}](s, "_tasks.ready", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	limit := in.Limit
	if limit < 1 || limit > 100 {
		return contract.Payload{}, invalidInput("ready scan limit must be between 1 and 100")
	}
	rows, err := listReady(ctx, unit, unit.Scope().InstallationID, int(limit))
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]*wireTask, 0, len(rows))
	for _, row := range rows {
		wire, err := row.toWire()
		if err != nil {
			return contract.Payload{}, err
		}
		items = append(items, wire)
	}
	return s.completed(map[string]any{"items": items})
}

// handleTasksSnapshot returns the current pinned contract and dependency
// state for one task. Callers still obey their own authority.
func handleTasksSnapshot(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope wireScope   `json:"scope"`
		ID    contract.ID `json:"id"`
	}](s, "_tasks.snapshot", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := getTask(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("task %s does not exist", in.ID)
	}
	if unit.Scope().InstallationID != "" && row.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, permissionDenied("task %s is outside the authenticated installation", row.ID)
	}
	if in.Scope.InstallationID != "" && in.Scope.InstallationID != row.InstallationID {
		return contract.Payload{}, permissionDenied("task %s is outside the requested scope", row.ID)
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTasksTransition validates and applies a legal state transition
// with the pinned independent acceptance fence.
func handleTasksTransition(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		TaskID          contract.ID   `json:"task_id"`
		ExpectedVersion int64         `json:"expected_version"`
		State           string        `json:"state"`
		EvidenceIDs     []contract.ID `json:"evidence_ids"`
		WaitingReason   string        `json:"waiting_reason"`
		Manual          bool          `json:"manual"`
	}](s, "_tasks.transition", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.ExpectedVersion < 1 {
		return contract.Payload{}, invalidInput("expected_version must be at least 1")
	}
	row, _, err := s.applyTransition(ctx, unit, &transitionRequest{
		TaskID:          in.TaskID,
		ExpectedVersion: in.ExpectedVersion,
		Target:          in.State,
		EvidenceIDs:     in.EvidenceIDs,
		WaitingReason:   in.WaitingReason,
		Manual:          in.Manual,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTasksEvidenceRecord binds trusted verifier evidence to the pinned
// acceptance digest and named output bindings for one run attempt, before
// any task state depends on it. Only this recorded lineage, or eligible
// explicit manual acceptance, can ever establish succeeded: evaluateSuccess
// consults exactly the bindings this handler writes, never a raw,
// untrusted evidence_ids submission.
func handleTasksEvidenceRecord(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		TaskID               contract.ID         `json:"task_id"`
		AttemptID            contract.ID         `json:"attempt_id"`
		ExpectedVersion      int64               `json:"expected_version"`
		AcceptanceDigest     string              `json:"acceptance_digest"`
		VerificationArtifact wireArtifactRef     `json:"verification_artifact"`
		OutputBindings       []wireOutputBinding `json:"output_bindings"`
		Verdict              string              `json:"verdict"`
	}](s, "_tasks.evidence.record", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := getTask(ctx, unit, in.TaskID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("task %s does not exist", in.TaskID)
	}
	if unit.Scope().InstallationID != "" && row.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, permissionDenied("task %s is outside the authenticated installation", row.ID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("task %s is at version %d, not the expected %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	if isTerminal(row.State) {
		return contract.Payload{}, conflictFault("task %s is terminal in state %s and cannot record new evidence", row.ID, row.State)
	}
	if row.State == stateDraft || row.State == stateReady {
		return contract.Payload{}, conflictFault("task %s has not started; evidence cannot be recorded before a live attempt", row.ID)
	}
	if row.AcceptanceDigest != in.AcceptanceDigest {
		return contract.Payload{}, verificationFailed(
			"recorded evidence targets acceptance digest %s, task %s is currently sealed to %s",
			in.AcceptanceDigest, row.ID, row.AcceptanceDigest)
	}
	if err := s.verifySeal(ctx, unit, row); err != nil {
		return contract.Payload{}, err
	}

	acceptance, err := row.decodeAcceptance()
	if err != nil {
		return contract.Payload{}, err
	}
	declared := map[string]bool{}
	for _, o := range acceptance.ExpectedObservations {
		if o.ArtifactName != "" {
			declared[o.ArtifactName] = true
		}
	}
	seen := map[string]bool{}
	refs := []wireArtifactRef{in.VerificationArtifact}
	for _, b := range in.OutputBindings {
		if !declared[b.Name] {
			return contract.Payload{}, invalidInput("output binding %q is not a declared output slot on task %s", b.Name, row.ID)
		}
		if seen[b.Name] {
			return contract.Payload{}, invalidInput("output binding %q is duplicated", b.Name)
		}
		seen[b.Name] = true
		refs = append(refs, b.Artifact)
	}

	scope, err := row.decodeScope()
	if err != nil {
		return contract.Payload{}, err
	}
	if _, err := s.validateArtifacts(ctx, unit, scope.toContract(), refs); err != nil {
		return contract.Payload{}, err
	}

	now := s.clock.Now().UTC()
	if _, err := s.recordEvidence(ctx, unit, row, []contract.ID{in.VerificationArtifact.ID}, now); err != nil {
		return contract.Payload{}, err
	}
	for _, b := range in.OutputBindings {
		if err := upsertOutputBinding(ctx, unit, &outputBindingRow{
			TaskID: row.ID, Attempt: row.Attempt, Name: b.Name,
			ArtifactID: b.Artifact.ID, Digest: string(b.Artifact.Digest),
			Installation: row.InstallationID, RecordedAt: now,
		}); err != nil {
			return contract.Payload{}, internalError("recording output binding %q failed: %v", b.Name, err)
		}
	}
	if err := s.emitTaskEvent(ctx, unit, row, "tasks.task.evidence_recorded", map[string]any{
		"task_id": row.ID, "attempt_id": in.AttemptID, "verdict": in.Verdict,
		"bound_outputs": len(in.OutputBindings),
	}); err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTasksDependenciesWake bounded-scans the dependents of a
// just-completed task and returns their current, honestly observed state.
// It never transitions a dependent itself and never reports success for one
// whose required child failed: it hands back exactly what is durably
// stored, so the caller (execution or the controller) can decide whether
// each dependent is now eligible to start.
func handleTasksDependenciesWake(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		CompletedTaskID contract.ID `json:"completed_task_id"`
		Limit           int64       `json:"limit"`
		Cursor          string      `json:"cursor"`
	}](s, "_tasks.dependencies.wake", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.Limit < 1 || in.Limit > 100 {
		return contract.Payload{}, invalidInput("wake scan limit must be between 1 and 100")
	}
	completed, err := getTask(ctx, unit, in.CompletedTaskID)
	if err != nil {
		return contract.Payload{}, err
	}
	if completed == nil {
		return contract.Payload{}, notFound("completed task %s does not exist", in.CompletedTaskID)
	}
	if unit.Scope().InstallationID != "" && completed.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, permissionDenied("task %s is outside the authenticated installation", completed.ID)
	}
	fingerprint := string(in.CompletedTaskID)
	offset, err := decodeCursor(unit, "_tasks.dependencies.wake", fingerprint, in.Cursor)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, err := listDependents(ctx, unit, completed.ID, completed.InstallationID, int(in.Limit), offset)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]*wireTask, 0, len(rows))
	for _, row := range rows {
		wire, err := row.toWire()
		if err != nil {
			return contract.Payload{}, err
		}
		items = append(items, wire)
	}
	out := map[string]any{"dependents": items}
	if len(items) == int(in.Limit) {
		next, err := encodeCursor(unit, "_tasks.dependencies.wake", fingerprint, offset+int64(in.Limit))
		if err != nil {
			return contract.Payload{}, err
		}
		out["next_cursor"] = next
	}
	return s.completed(out)
}
