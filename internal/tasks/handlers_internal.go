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
	row, err := s.applyTransition(ctx, unit, &transitionRequest{
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
