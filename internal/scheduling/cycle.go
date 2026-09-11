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

// cycleRecord persists one completed cycle: the bounded outcome, the durable
// next-wake decision and the next wake, with the minimum reconsideration
// interval and the per-responsibility spend limits enforced.
func (s *Service) cycleRecord(ctx context.Context, unit contract.Unit, in cycleRecordInput) (contract.Payload, error) {
	now := s.deps.Clock.Now()
	row, found, err := loadResponsibility(ctx, unit, in.ResponsibilityID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("responsibility %s is unknown in this installation", in.ResponsibilityID)
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
	}); err != nil {
		return contract.Payload{}, err
	}

	row.Version++
	row.NextWake = &in.NextWake
	row.LastCycleAt = &now
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
	if err := emitTransition(ctx, unit, eventCycleRecorded, row.ID, row.Version); err != nil {
		return contract.Payload{}, err
	}

	wire, err := row.wire()
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(responsibilityBody{Resource: wire})
}
