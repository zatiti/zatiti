package scheduling

import (
	"context"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The internal cycle record operation: execution persists one completed
// bounded reasoning cycle — its accountable outputs, the task ids it
// produced and its durable next-wake decision — behind the responsibility's
// version fence. Recording is what counts a cycle against the per-cycle and
// aggregate spend limits, and what arms the next wake.
//
// Outcome classification (design decision — the frozen input schema carries
// no explicit decision field; the sealed CycleDecisionProposal choice
// (continue|wait|escalate|done) is execution's own local reasoning tool and
// collapses to these wire fields before this boundary, so scheduling
// classifies the reported cycle structurally from what it actually
// receives):
//   - useful-work: len(task_ids) > 0 — the cycle produced bounded work.
//     Recorded and reported as eventCycleRecorded.
//   - no-work: task_ids and outputs both empty — a pure idle reasoning
//     pass. Still consumes the per-cycle spend and respects the minimum
//     reconsideration interval and aggregate fence exactly like a working
//     cycle (see cycleRecordAggregateFence below), which is what stops an
//     idle loop from spending or reconsidering unboundedly. Reported as
//     eventCycleIdle.
//   - wait: the fault returned when a caller tries to record a cycle before
//     the minimum reconsideration interval has elapsed — the system's own
//     "not yet" answer to a premature reconsideration attempt.
//   - escalate: the fault returned when recording this cycle would exceed
//     the responsibility's aggregate spend limit (budgetUnavailable,
//     retryable) — the operator must resume the responsibility with a
//     larger budget or pause/replan it, the human escalation this code
//     path exists to force rather than silently overspending.

// cycleOutcomeUsefulWork is the event/classification label for a cycle that
// produced at least one bounded task.
const cycleOutcomeUsefulWork = "useful_work"

// cycleOutcomeNoWork is the label for a cycle that produced neither tasks
// nor outputs: a pure idle reasoning pass that still counts against spend.
const cycleOutcomeNoWork = "no_work"

// classifyCycleOutcome returns the structural outcome label for one recorded
// cycle. See the package-level design-decision comment above.
func classifyCycleOutcome(outputs []wireArtifactRef, taskIDs []contract.ID) string {
	if len(taskIDs) > 0 {
		return cycleOutcomeUsefulWork
	}
	return cycleOutcomeNoWork
}

// cycleRecord persists one completed cycle: the bounded outcome, the durable
// next-wake decision and the next wake, with the minimum reconsideration
// interval and the per-responsibility spend limits enforced. cycle_id is the
// unique replay/conflict fence: an identical repeat for the same cycle_id
// replays the current responsibility state without re-mutating anything,
// and a differing repeat is submission_conflict — checked BEFORE the
// version fence, matching the wire convention that a replay is identified
// by content before stale-version validation runs.
func (s *Service) cycleRecord(ctx context.Context, unit contract.Unit, in cycleRecordInput) (contract.Payload, error) {
	now := s.deps.Clock.Now()
	row, found, err := loadResponsibility(ctx, unit, in.ResponsibilityID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("responsibility %s is unknown in this installation", in.ResponsibilityID)
	}

	existing, foundCycle, err := findCycleByCycleID(ctx, unit, row.ID, in.CycleID)
	if err != nil {
		return contract.Payload{}, err
	}
	if foundCycle {
		if cycleRecordMatches(existing, in) {
			wire, err := row.wire()
			if err != nil {
				return contract.Payload{}, err
			}
			return completed(responsibilityBody{Resource: wire})
		}
		return contract.Payload{}, submissionConflict(
			"cycle %s for responsibility %s is already recorded with different content", in.CycleID, row.ID)
	}

	if row.Archived {
		return contract.Payload{}, conflict("responsibility %s is archived and cannot record cycles", row.ID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"responsibility %s is at version %d, not the expected %d", row.ID, row.Version, in.ExpectedVersion)
	}

	// Minimum reconsideration interval: consecutive cycle starts are at
	// least one interval apart. This cycle started when its wake was due,
	// which is the responsibility's current next wake.
	if row.NextWake != nil && in.NextWake.Sub(*row.NextWake) < time.Duration(row.MinIntervalSeconds)*time.Second {
		return contract.Payload{}, invalidInput(
			"next wake for responsibility %s must be at least %d seconds after the current cycle start",
			row.ID, row.MinIntervalSeconds)
	}

	// Aggregate fence: the per-cycle limit bounds every recorded cycle, so
	// recording one more exceeds the aggregate when the checked product
	// does. A zero per-cycle spend always records.
	count, err := countCycles(ctx, unit, row.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	total, ok := checkedMul(count+1, row.CycleLimits.SpendMicroUnits)
	if !ok || total > row.AggregateLimits.SpendMicroUnits {
		return contract.Payload{}, budgetUnavailable(
			"responsibility %s aggregate spend limit %d %s would be exceeded by cycle %d at %d micro-units per cycle",
			row.ID, row.AggregateLimits.SpendMicroUnits, row.CycleLimits.Currency, count+1, row.CycleLimits.SpendMicroUnits)
	}

	if in.Outputs == nil {
		in.Outputs = []wireArtifactRef{}
	}
	if err := insertCycle(ctx, unit, cycleRow{
		ID:                    s.deps.IDs.New(),
		ResponsibilityID:      row.ID,
		ResponsibilityVersion: row.Version,
		Scope:                 row.Scope,
		NextWake:              in.NextWake,
		Outputs:               in.Outputs,
		TaskIDs:               in.TaskIDs,
		RecordedAt:            now,
		CycleID:               in.CycleID,
		TurnID:                in.TurnID,
	}); err != nil {
		return contract.Payload{}, err
	}

	row.Version++
	row.NextWake = &in.NextWake
	row.LastCycleAt = &now
	row.LastCycleID = in.CycleID
	row.UpdatedAt = now
	if err := recordResponsibilityCycle(ctx, unit, row); err != nil {
		return contract.Payload{}, err
	}

	// One pending wake at a time: drop any leftover before arming the next
	// cycle's wake under the post-bump version.
	if err := deletePendingWakes(ctx, unit, row.ID); err != nil {
		return contract.Payload{}, err
	}
	if err := s.insertSourceWake(ctx, unit, row.Scope, sourceResponsibility, row.ID,
		responsibilityOccurrenceKey(row.ID, row.Version, in.NextWake), in.NextWake, row.Version, now); err != nil {
		return contract.Payload{}, err
	}
	eventKind := eventCycleRecorded
	if classifyCycleOutcome(in.Outputs, in.TaskIDs) == cycleOutcomeNoWork {
		eventKind = eventCycleIdle
	}
	if err := emitTransition(ctx, unit, eventKind, row.ID, row.Version); err != nil {
		return contract.Payload{}, err
	}

	wire, err := row.wire()
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(responsibilityBody{Resource: wire})
}

// cycleRecordMatches reports whether an already-recorded cycle's content
// matches a repeated cycle.record call for the same cycle_id: the replay
// condition the wire convention requires to be checked before stale-version
// validation. turn_id, next_wake, outputs and task_ids must all agree;
// anything else is submission_conflict.
func cycleRecordMatches(existing cycleRow, in cycleRecordInput) bool {
	if existing.TurnID != in.TurnID {
		return false
	}
	if !existing.NextWake.Equal(in.NextWake) {
		return false
	}
	if !artifactRefsEqual(existing.Outputs, in.Outputs) {
		return false
	}
	return idsEqual(existing.TaskIDs, in.TaskIDs)
}

// artifactRefsEqual compares two ArtifactRef slices by exact content and
// order: outputs are an ordered accountable list, not a set.
func artifactRefsEqual(a, b []wireArtifactRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Digest != b[i].Digest {
			return false
		}
	}
	return true
}

// idsEqual compares two ID slices by exact content and order.
func idsEqual(a, b []contract.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
