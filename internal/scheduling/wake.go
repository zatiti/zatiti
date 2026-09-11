package scheduling

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wake plumbing: occurrence identities, wake arming, and the internal
// wake.due/wake.admit operations the controller drives. Admission is the
// transactional gate every generated task passes: recheck the restrictive
// source state, deduplicate the occurrence, fence the wake exactly once,
// land the bounded task through tasks, and persist the next wake decision
// in the same transaction.

// Occurrence keys carry the pinned definition version and a fixed-width UTC
// stamp so the same instant always produces the same key. Event-driven
// responsibility wakes carry the raw durable event ID as their occurrence
// key instead; the controller mints those.

// scheduleOccurrenceKey is the occurrence identity of one cron instant: the
// schedule ID plus the pinned definition version plus the UTC instant.
func scheduleOccurrenceKey(id contract.ID, version contract.Version, instant time.Time) string {
	return fmt.Sprintf("sched|%s|%d|%s", id, version, formatStamp(instant))
}

// responsibilityOccurrenceKey is the occurrence identity of one timed
// responsibility reconsideration.
func responsibilityOccurrenceKey(id contract.ID, version contract.Version, instant time.Time) string {
	return fmt.Sprintf("resp|%s|%d|%s", id, version, formatStamp(instant))
}

// checkedMul multiplies two non-negative int64 counts, reporting false when
// the product would overflow. Budget fences treat overflow as exceeded: an
// unrepresentable product is certainly above the limit.
func checkedMul(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	prod := a * b
	if prod/b != a {
		return 0, false
	}
	return prod, true
}

// maxDeadlineSeconds bounds the duration arithmetic that derives a fresh
// root deadline from attempt_seconds; larger attempt bounds saturate.
const maxDeadlineSeconds = int64(math.MaxInt64) / int64(time.Second)

// freshDeadline rewrites a limits envelope's root deadline as one attempt
// window from admission. The definition's pinned root_deadline is a staging
// value that goes stale the moment it is written; tasks refuse a past
// deadline, so every generated task is bounded from its own admission
// instant instead.
func freshDeadline(l wireLimits, now time.Time) wireLimits {
	seconds := l.AttemptSeconds
	if seconds > maxDeadlineSeconds {
		seconds = maxDeadlineSeconds
	}
	l.RootDeadline = formatStamp(now.Add(time.Duration(seconds) * time.Second))
	return l
}

// insertSourceWake arms one pending wake for a source: a minted wake
// identity, the source's scope dimensions and the occurrence key that will
// deduplicate its admission.
func (s *Service) insertSourceWake(ctx context.Context, unit contract.Unit, scope contract.Scope, sourceKind string, sourceID contract.ID, occurrenceKey string, dueAt time.Time, conditionVersion contract.Version, now time.Time) error {
	installation, organization, project, worker, task := scopeDims(scope)
	return insertWake(ctx, unit, wakeRow{
		ID:               s.deps.IDs.New(),
		InstallationID:   installation,
		OrganizationID:   organization,
		ProjectID:        project,
		WorkerID:         worker,
		TaskID:           task,
		Scope:            scope,
		SourceKind:       sourceKind,
		SourceID:         sourceID,
		OccurrenceKey:    occurrenceKey,
		DueAt:            dueAt,
		ConditionVersion: conditionVersion,
		CreatedAt:        now,
	})
}

// wakeDue lists the wakes due at or before the controller's pinned instant.
// Reading admits nothing: admission happens only through wake.admit.
func (s *Service) wakeDue(ctx context.Context, unit contract.Unit, in wakeDueInput) (contract.Payload, error) {
	rows, err := listPendingWakes(ctx, unit, in.Now, in.Limit)
	if err != nil {
		return contract.Payload{}, err
	}
	wakes := make([]wireWake, 0, len(rows))
	for _, r := range rows {
		wakes = append(wakes, wireWake{
			ID:               r.ID,
			Scope:            r.Scope,
			SourceID:         r.SourceID,
			OccurrenceKey:    r.OccurrenceKey,
			DueAt:            r.DueAt,
			ConditionVersion: r.ConditionVersion,
		})
	}
	return completed(wakeDueBody{Wakes: wakes})
}

// wakeAdmit admits one due wake exactly once. Unknown, already-admitted and
// occurrence-duplicated deliveries report skipped so controller retries stay
// idempotent; a not-yet-due wake is a caller error.
func (s *Service) wakeAdmit(ctx context.Context, unit contract.Unit, in wakeAdmitInput) (contract.Payload, error) {
	now := s.deps.Clock.Now()
	row, err := loadWake(ctx, unit, in.Wake.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return completed(wakeAdmitBody{Skipped: true})
	}
	if err != nil {
		return contract.Payload{}, err
	}
	if row.AdmittedAt != nil {
		return completed(wakeAdmitBody{Skipped: true})
	}
	if row.DueAt.After(now) {
		return contract.Payload{}, invalidInput("wake %s is not due until %s", row.ID, formatStamp(row.DueAt))
	}
	switch row.SourceKind {
	case sourceSchedule:
		return s.admitScheduleWake(ctx, unit, row, now)
	case sourceResponsibility:
		return s.admitResponsibilityWake(ctx, unit, row, now)
	default:
		return contract.Payload{}, invalidInput("wake %s carries unknown source kind %q", row.ID, row.SourceKind)
	}
}

// recordOccurrence persists one occurrence decision: admitted with its task
// reference, or skipped with a reason.
func (s *Service) recordOccurrence(ctx context.Context, unit contract.Unit, wake wakeRow, occurrenceKey string, state string, reason string, taskRef contract.ID, now time.Time) error {
	installation, organization, project, worker, task := scopeDims(wake.Scope)
	return insertOccurrence(ctx, unit, occurrenceRow{
		ID:             s.deps.IDs.New(),
		InstallationID: installation,
		OrganizationID: organization,
		ProjectID:      project,
		WorkerID:       worker,
		TaskID:         task,
		Scope:          wake.Scope,
		SourceID:       wake.SourceID,
		OccurrenceKey:  occurrenceKey,
		TaskRef:        string(taskRef),
		State:          state,
		Reason:         reason,
		DecidedAt:      now,
		CreatedAt:      now,
	})
}

// skipWake consumes a wake without admitting work: the source row is gone,
// the occurrence was already decided, or another admission won the fence.
// The fence alone marks it handled; no occurrence row is invented for a
// decision this transaction did not make.
func (s *Service) skipWake(ctx context.Context, unit contract.Unit, wake wakeRow, now time.Time) (contract.Payload, error) {
	if _, err := fenceWakeAdmission(ctx, unit, wake.ID, now); err != nil {
		return contract.Payload{}, err
	}
	return completed(wakeAdmitBody{Skipped: true})
}

// admitScheduleWake admits one cron occurrence: the pinned instant first,
// then at most one coalesced catch-up when the misfire policy allows it, a
// skipped occurrence row for every other missed instant, and the next wake —
// all in the caller's transaction.
func (s *Service) admitScheduleWake(ctx context.Context, unit contract.Unit, wake wakeRow, now time.Time) (contract.Payload, error) {
	sched, found, err := loadSchedule(ctx, unit, wake.SourceID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return s.skipWake(ctx, unit, wake, now)
	}
	if sched.Paused {
		// Left pending on purpose: resume re-admits it through the misfire
		// path, so the durable next-wake identity survives the pause.
		return completed(wakeAdmitBody{Skipped: true})
	}
	if sched.Archived {
		if err := s.recordOccurrence(ctx, unit, wake, wake.OccurrenceKey, occurrenceSkipped, "archived", "", now); err != nil {
			return contract.Payload{}, err
		}
		return s.skipWake(ctx, unit, wake, now)
	}
	exists, err := occurrenceExists(ctx, unit, wake.SourceID, wake.OccurrenceKey)
	if err != nil {
		return contract.Payload{}, err
	}
	if exists {
		return s.skipWake(ctx, unit, wake, now)
	}
	admitted, err := fenceWakeAdmission(ctx, unit, wake.ID, now)
	if err != nil {
		return contract.Payload{}, err
	}
	if !admitted {
		return completed(wakeAdmitBody{Skipped: true})
	}

	expr, err := parseCron(sched.Expression)
	if err != nil {
		return contract.Payload{}, invalidInput("schedule %s expression is no longer a valid cron expression: %v", sched.ID, err)
	}
	loc, err := loadLocation(sched.Timezone)
	if err != nil {
		return contract.Payload{}, invalidInput("schedule %s timezone is no longer resolvable", sched.ID)
	}

	var template wireTask
	if err := decodeJSON(string(sched.TaskTemplate), &template); err != nil {
		return contract.Payload{}, err
	}

	// Misfire enumeration over the missed instants after the pinned one.
	// The pinned instant is always admitted; the coalesce policy admits
	// additionally the newest instant inside the catch-up window, and every
	// other additional instant is recorded skipped — as is every additional
	// instant under the skip policy.
	additional := expr.instantsBetween(wake.DueAt, now, loc)
	missed := make([]time.Time, 0, len(additional))
	for _, t := range additional {
		if t.After(wake.DueAt) {
			missed = append(missed, t)
		}
	}
	var coalesced time.Time
	var hasCoalesced bool
	if sched.Misfire == misfireCoalesce {
		window := time.Duration(sched.CatchUpSeconds) * time.Second
		for i := len(missed) - 1; i >= 0; i-- {
			if now.Sub(missed[i]) <= window {
				coalesced = missed[i]
				hasCoalesced = true
				missed = append(missed[:i], missed[i+1:]...)
				break
			}
		}
	}
	for _, t := range missed {
		if err := s.recordOccurrence(ctx, unit, wake, scheduleOccurrenceKey(wake.SourceID, wake.ConditionVersion, t),
			occurrenceSkipped, "misfire", "", now); err != nil {
			return contract.Payload{}, err
		}
	}

	task, err := s.admitOccurrenceTask(ctx, unit, template, wake, wake.OccurrenceKey, now)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.recordOccurrence(ctx, unit, wake, wake.OccurrenceKey, occurrenceAdmitted, "", task.ID, now); err != nil {
		return contract.Payload{}, err
	}
	if hasCoalesced {
		extra, err := s.admitOccurrenceTask(ctx, unit, template, wake,
			scheduleOccurrenceKey(wake.SourceID, wake.ConditionVersion, coalesced), now)
		if err != nil {
			return contract.Payload{}, err
		}
		if err := s.recordOccurrence(ctx, unit, wake, scheduleOccurrenceKey(wake.SourceID, wake.ConditionVersion, coalesced),
			occurrenceAdmitted, "", extra.ID, now); err != nil {
			return contract.Payload{}, err
		}
	}

	// Arm the next wake only when no other pending wake remains for the
	// schedule: a concurrent arming must not produce two live wakes. When
	// the expression admits no occurrence within the horizon the schedule
	// exists without a pending wake until a resume re-arms it.
	pending, err := pendingWakeExists(ctx, unit, sched.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !pending {
		sched.NextWake = nil
		if next, ok := expr.nextAfter(now, loc); ok {
			sched.NextWake = &next
			if err := s.insertSourceWake(ctx, unit, sched.Scope, sourceSchedule, sched.ID,
				scheduleOccurrenceKey(sched.ID, sched.Version, next), next, sched.Version, now); err != nil {
				return contract.Payload{}, err
			}
		}
		sched.UpdatedAt = now
		if err := updateScheduleNextWake(ctx, unit, sched); err != nil {
			return contract.Payload{}, err
		}
	}
	return completed(wakeAdmitBody{Task: &task, Skipped: false})
}

// admitResponsibilityWake admits one responsibility wake: the aggregate
// spend fence, the bounded cycle task through tasks, and the occurrence row.
// The next wake is the cycle outcome's durable decision, recorded by
// cycle.record — admission deliberately does not arm one.
func (s *Service) admitResponsibilityWake(ctx context.Context, unit contract.Unit, wake wakeRow, now time.Time) (contract.Payload, error) {
	resp, found, err := loadResponsibility(ctx, unit, wake.SourceID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return s.skipWake(ctx, unit, wake, now)
	}
	if resp.Paused {
		return completed(wakeAdmitBody{Skipped: true})
	}
	if resp.Archived {
		if err := s.recordOccurrence(ctx, unit, wake, wake.OccurrenceKey, occurrenceSkipped, "archived", "", now); err != nil {
			return contract.Payload{}, err
		}
		return s.skipWake(ctx, unit, wake, now)
	}
	exists, err := occurrenceExists(ctx, unit, wake.SourceID, wake.OccurrenceKey)
	if err != nil {
		return contract.Payload{}, err
	}
	if exists {
		return s.skipWake(ctx, unit, wake, now)
	}
	admitted, err := fenceWakeAdmission(ctx, unit, wake.ID, now)
	if err != nil {
		return contract.Payload{}, err
	}
	if !admitted {
		return completed(wakeAdmitBody{Skipped: true})
	}

	// Aggregate fence: every recorded cycle spends at most the per-cycle
	// limit, so admitting one more cycle exceeds the aggregate when the
	// checked product does. A zero per-cycle spend always admits.
	count, err := countCycles(ctx, unit, resp.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	total, ok := checkedMul(count+1, resp.CycleLimits.SpendMicroUnits)
	if !ok || total > resp.AggregateLimits.SpendMicroUnits {
		return contract.Payload{}, budgetUnavailable(
			"responsibility %s aggregate spend limit %d %s would be exceeded by cycle %d at %d micro-units per cycle",
			resp.ID, resp.AggregateLimits.SpendMicroUnits, resp.CycleLimits.Currency, count+1, resp.CycleLimits.SpendMicroUnits)
	}

	acceptance := wireAcceptance{}
	if err := decodeJSON(string(resp.Acceptance), &acceptance); err != nil {
		return contract.Payload{}, err
	}
	cycle := wireTask{
		ID:              s.deps.IDs.New(),
		Version:         1,
		Scope:           resp.Scope,
		OwnerID:         resp.WorkerID,
		WorkerID:        resp.WorkerID,
		Outcome:         resp.Outcome,
		Inputs:          []wireArtifactRef{},
		RequiredOutputs: []string{},
		Acceptance:      acceptance,
		Limits:          freshDeadline(resp.CycleLimits, now),
		Dependencies:    []contract.ID{},
		State:           "draft",
	}
	task, err := s.landTask(ctx, unit, cycle, wake.SourceID, wake.OccurrenceKey)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.recordOccurrence(ctx, unit, wake, wake.OccurrenceKey, occurrenceAdmitted, "", task.ID, now); err != nil {
		return contract.Payload{}, err
	}
	return completed(wakeAdmitBody{Task: &task, Skipped: false})
}

// admitOccurrenceTask builds one task from a schedule's template, rewrites
// its identity and deadline for this occurrence, and lands it through tasks.
func (s *Service) admitOccurrenceTask(ctx context.Context, unit contract.Unit, template wireTask, wake wakeRow, occurrenceKey string, now time.Time) (wireTask, error) {
	born := template
	born.ID = s.deps.IDs.New()
	born.Version = 1
	born.State = "draft"
	born.Limits = freshDeadline(born.Limits, now)
	return s.landTask(ctx, unit, born, wake.SourceID, occurrenceKey)
}

// landTask lands one generated task through tasks' admission fence and
// readies it for execution: create as draft, fence the draft→ready
// transition, enqueue the run. tasks owns its own validation, budget
// reservation and worker checks; their faults propagate unchanged so the
// surrounding transaction rolls back with the peer's own code.
func (s *Service) landTask(ctx context.Context, unit contract.Unit, task wireTask, sourceID contract.ID, occurrenceKey string) (wireTask, error) {
	created, err := s.tasksCreate(ctx, unit, task, sourceID, occurrenceKey)
	if err != nil {
		return wireTask{}, err
	}
	ready, err := s.tasksTransition(ctx, unit, created.ID, created.Version, "ready")
	if err != nil {
		return wireTask{}, err
	}
	if err := s.executionEnqueue(ctx, unit, ready); err != nil {
		return wireTask{}, err
	}
	return ready, nil
}

// tasksCreate calls _tasks.create: task admission with source+occurrence
// deduplication inside tasks' own transaction share.
func (s *Service) tasksCreate(ctx context.Context, unit contract.Unit, task wireTask, sourceID contract.ID, occurrenceKey string) (wireTask, error) {
	data, err := s.callPeer(ctx, unit, "_tasks.create", tasksCreateCallInput{
		Task:          task,
		SourceID:      sourceID,
		OccurrenceKey: occurrenceKey,
	})
	if err != nil {
		return wireTask{}, err
	}
	var body peerTaskBody
	if err := json.Unmarshal(data, &body); err != nil {
		return wireTask{}, fmt.Errorf("scheduling: decode tasks create: %w", err)
	}
	return body.Resource, nil
}

// tasksTransition calls _tasks.transition: the fenced draft→ready move that
// checkPrerequisites guards on the tasks side.
func (s *Service) tasksTransition(ctx context.Context, unit contract.Unit, taskID contract.ID, expected contract.Version, state string) (wireTask, error) {
	data, err := s.callPeer(ctx, unit, "_tasks.transition", tasksTransitionCallInput{
		TaskID:          taskID,
		ExpectedVersion: expected,
		State:           state,
		EvidenceIDs:     []contract.ID{},
	})
	if err != nil {
		return wireTask{}, err
	}
	var body peerTaskBody
	if err := json.Unmarshal(data, &body); err != nil {
		return wireTask{}, fmt.Errorf("scheduling: decode tasks transition: %w", err)
	}
	return body.Resource, nil
}

// executionEnqueue calls _execution.enqueue: execution creates one run for
// the ready task/version and owns claiming and dispatch.
func (s *Service) executionEnqueue(ctx context.Context, unit contract.Unit, task wireTask) error {
	_, err := s.callPeer(ctx, unit, "_execution.enqueue", executionEnqueueCallInput{Task: task})
	return err
}
