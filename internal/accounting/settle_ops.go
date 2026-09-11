package accounting

import (
	"context"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Settlement. A settle transitions one reservation along the exact state
// machine reserved→settled, reserved→released (authoritative nonexecution),
// reserved→unknown, unknown→settled and unknown→released, applying the
// matching deltas to every position the reservation charged and appending one
// ledger entry per movement. Terminal states replay idempotently on an
// identical usage report; any other change conflicts. Unknown outcomes keep
// their concurrency slot held until supported evidence resolves them;
// advisory and unknown amounts are never silently zeroed.

// handleSettle is the _accounting.settle boundary.
func (s *Service) handleSettle(ctx context.Context, unit contract.Unit, in settleInput) (contract.Outcome[reservationResourceBody], error) {
	r, err := loadReservation(ctx, unit, in.ReservationID)
	if err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if r == nil {
		return contract.Outcome[reservationResourceBody]{}, notFound("reservation %s does not exist", in.ReservationID)
	}
	usageJSON, err := canonicalJSON(in.Usage)
	if err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	// Terminal idempotency first: a replayed terminal command with the exact
	// usage report returns the current reservation; any other terminal
	// request conflicts instead of rewriting history.
	if r.State == stateSettled || r.State == stateReleased {
		if r.SettleUsageJSON == string(usageJSON) {
			return completedOutcome(reservationResourceBody{Resource: reservationWire(r)})
		}
		return contract.Outcome[reservationResourceBody]{}, conflict("reservation %s is already %s with a different usage report", r.ID, r.State)
	}
	if in.ExpectedVersion != r.Version {
		return contract.Outcome[reservationResourceBody]{}, staleVersion("reservation %s is at version %d, settle expects %d", r.ID, r.Version, in.ExpectedVersion)
	}
	if in.Usage.Reserved != 0 {
		return contract.Outcome[reservationResourceBody]{}, invalidInput("settle usage reports observed effects; reserved is managed by the reservation itself")
	}
	nonzero := in.Usage.Spent != 0 || in.Usage.Estimated != 0 || in.Usage.Unknown != 0
	if in.Nonexecution && nonzero {
		return contract.Outcome[reservationResourceBody]{}, invalidInput("authoritative nonexecution requires an all-zero usage report")
	}
	if nonzero && in.Usage.Currency != r.Currency {
		return contract.Outcome[reservationResourceBody]{}, invalidInput("currency mismatch: reservation %s is booked in %s, usage reports %s",
			r.ID, r.Currency, in.Usage.Currency)
	}
	now := s.now()
	switch r.State {
	case stateReserved:
		if in.Nonexecution {
			return s.releaseFromReserved(ctx, unit, r, string(usageJSON), now)
		}
		if in.Usage.Unknown > 0 {
			return s.settleUnknown(ctx, unit, r, in.Usage, string(usageJSON), now)
		}
		return s.settleToSettled(ctx, unit, r, in.Usage, string(usageJSON), now)
	case stateUnknown:
		prior, err := decodeUsageJSON(r.SettleUsageJSON)
		if err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
		if in.Nonexecution {
			return s.resolveReleased(ctx, unit, r, prior.Unknown, string(usageJSON), now)
		}
		return s.resolveSettled(ctx, unit, r, prior, in.Usage, string(usageJSON), now)
	default:
		return contract.Outcome[reservationResourceBody]{}, fmt.Errorf("accounting: reservation %s is in unhandled state %s", r.ID, r.State)
	}
}

// settleDeltas moves one set of position deltas across every dimension the
// reservation charged. A missing position row is storage corruption and
// fails closed; settlement never invents balances.
func (s *Service) settleDeltas(ctx context.Context, unit contract.Unit, r *reservationRow,
	dSpent, dReserved, dUnknown, dEstimated, dSlots int64, now time.Time) error {
	var refs []levelRef
	if err := contract.DecodeStrict([]byte(r.PositionsJSON), &refs); err != nil {
		return fmt.Errorf("accounting: reservation %s charged positions do not decode: %w", r.ID, err)
	}
	if len(refs) == 0 {
		return fmt.Errorf("accounting: reservation %s records no charged positions", r.ID)
	}
	for _, ref := range refs {
		p, err := loadPosition(ctx, unit, ref.Kind, ref.Ref)
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("accounting: reservation %s references missing position %s/%s", r.ID, ref.Kind, ref.Ref)
		}
		if err := applyPositionDeltas(ctx, unit, p, dSpent, dReserved, dUnknown, dEstimated, dSlots, now); err != nil {
			return err
		}
	}
	return nil
}

// appendEntry writes one ledger entry for the reservation movement.
func (s *Service) appendEntry(ctx context.Context, unit contract.Unit, r *reservationRow, kind string, amount int64, advisory bool, note string, now time.Time) error {
	if amount <= 0 {
		return nil
	}
	return insertEntry(ctx, unit, &entry{
		ID:            s.deps.IDs.New(),
		ReservationID: r.ID,
		InstallID:     r.InstallID,
		Kind:          kind,
		Currency:      r.Currency,
		Amount:        amount,
		Advisory:      advisory,
		Note:          note,
		CreatedAt:     now,
	})
}

// settleToSettled completes a reservation from reserved. Advisory usage is
// never enforceable spent: its whole amount lands in the estimated bucket,
// which no hard cap claims. The proven unused portion of the reservation is
// released.
func (s *Service) settleToSettled(ctx context.Context, unit contract.Unit, r *reservationRow,
	usage wireUsage, usageJSON string, now time.Time) (contract.Outcome[reservationResourceBody], error) {
	dSpent, dEstimated := int64(0), int64(0)
	if usage.Advisory {
		total, err := addChecked(usage.Spent, usage.Estimated)
		if err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
		dEstimated = total
	} else {
		dSpent = usage.Spent
		dEstimated = usage.Estimated
	}
	if err := s.settleDeltas(ctx, unit, r, dSpent, -r.Amount, 0, dEstimated, -1, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if !usage.Advisory {
		if err := s.appendEntry(ctx, unit, r, entrySpent, usage.Spent, false, "settled", now); err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
	}
	estimated := usage.Estimated
	note := "settled"
	if usage.Advisory {
		total, err := addChecked(usage.Spent, usage.Estimated)
		if err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
		estimated = total
		note = "settled advisory"
	}
	if err := s.appendEntry(ctx, unit, r, entryEstimated, estimated, true, note, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	released := int64(0)
	if !usage.Advisory && usage.Spent < r.Amount {
		released = r.Amount - usage.Spent
	}
	if err := s.appendEntry(ctx, unit, r, entryReleased, released, false, "unspent bound released", now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := updateReservationState(ctx, unit, r, stateSettled, usageJSON, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := emitEvent(ctx, unit, eventReservationSettled, r.ID, contract.Version(r.Version), map[string]any{
		"operation_id": string(r.OperationID),
	}); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	return completedOutcome(reservationResourceBody{Resource: reservationWire(r)})
}

// releaseFromReserved concludes a never-executed reservation with
// authoritative nonexecution: the whole unused bound is released and the
// concurrency slot returns, leaving nothing charged anywhere.
func (s *Service) releaseFromReserved(ctx context.Context, unit contract.Unit, r *reservationRow,
	usageJSON string, now time.Time) (contract.Outcome[reservationResourceBody], error) {
	if err := s.settleDeltas(ctx, unit, r, 0, -r.Amount, 0, 0, -1, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := s.appendEntry(ctx, unit, r, entryReleased, r.Amount, false, "authoritative nonexecution", now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := updateReservationState(ctx, unit, r, stateReleased, usageJSON, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := emitEvent(ctx, unit, eventReservationReleased, r.ID, contract.Version(r.Version), map[string]any{
		"operation_id": string(r.OperationID),
		"resolution":   "nonexecution",
	}); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	return completedOutcome(reservationResourceBody{Resource: reservationWire(r)})
}

// settleUnknown retains unresolved physical effects: the observed unknown
// amount moves into the enforceable unknown bucket, the reservation bound
// leaves reserved, and the concurrency slot stays held until supported
// evidence resolves the outcome.
func (s *Service) settleUnknown(ctx context.Context, unit contract.Unit, r *reservationRow,
	usage wireUsage, usageJSON string, now time.Time) (contract.Outcome[reservationResourceBody], error) {
	if err := s.settleDeltas(ctx, unit, r, 0, -r.Amount, usage.Unknown, 0, 0, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := s.appendEntry(ctx, unit, r, entryUnknown, usage.Unknown, false, "unknown effects retained", now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if remainder := r.Amount - usage.Unknown; remainder > 0 {
		if err := s.appendEntry(ctx, unit, r, entryReleased, remainder, false, "bound released, unknown retained", now); err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
	}
	if err := updateReservationState(ctx, unit, r, stateUnknown, usageJSON, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := emitEvent(ctx, unit, eventReservationUnknown, r.ID, contract.Version(r.Version), map[string]any{
		"operation_id": string(r.OperationID),
	}); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	return completedOutcome(reservationResourceBody{Resource: reservationWire(r)})
}

// resolveSettled resolves a retained unknown into an exact outcome: the
// prior unknown leaves the unknown bucket, observed cost lands in its
// buckets, the freed exposure is released, and the held concurrency slot is
// returned.
func (s *Service) resolveSettled(ctx context.Context, unit contract.Unit, r *reservationRow,
	prior, usage wireUsage, usageJSON string, now time.Time) (contract.Outcome[reservationResourceBody], error) {
	dSpent, dEstimated := int64(0), int64(0)
	if usage.Advisory {
		total, err := addChecked(usage.Spent, usage.Estimated)
		if err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
		dEstimated = total
	} else {
		dSpent = usage.Spent
		dEstimated = usage.Estimated
	}
	if err := s.settleDeltas(ctx, unit, r, dSpent, 0, -prior.Unknown, dEstimated, -1, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	charged := int64(0)
	if !usage.Advisory {
		charged = usage.Spent
		if err := s.appendEntry(ctx, unit, r, entrySpent, usage.Spent, false, "unknown resolved", now); err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
	}
	estimated := usage.Estimated
	if usage.Advisory {
		total, err := addChecked(usage.Spent, usage.Estimated)
		if err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
		estimated = total
	}
	if err := s.appendEntry(ctx, unit, r, entryEstimated, estimated, true, "unknown resolved advisory", now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if remainder := prior.Unknown - charged; remainder > 0 {
		if err := s.appendEntry(ctx, unit, r, entryReleased, remainder, false, "unknown resolved, unused portion released", now); err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
	}
	if err := updateReservationState(ctx, unit, r, stateSettled, usageJSON, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := emitEvent(ctx, unit, eventReservationSettled, r.ID, contract.Version(r.Version), map[string]any{
		"operation_id": string(r.OperationID),
		"resolution":   "unknown",
	}); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	return completedOutcome(reservationResourceBody{Resource: reservationWire(r)})
}

// resolveReleased concludes a retained unknown with authoritative
// nonexecution: the retained unknown is released and the held concurrency
// slot returns.
func (s *Service) resolveReleased(ctx context.Context, unit contract.Unit, r *reservationRow,
	priorUnknown int64, usageJSON string, now time.Time) (contract.Outcome[reservationResourceBody], error) {
	if err := s.settleDeltas(ctx, unit, r, 0, 0, -priorUnknown, 0, -1, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := s.appendEntry(ctx, unit, r, entryReleased, priorUnknown, false, "authoritative nonexecution", now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := updateReservationState(ctx, unit, r, stateReleased, usageJSON, now); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := emitEvent(ctx, unit, eventReservationReleased, r.ID, contract.Version(r.Version), map[string]any{
		"operation_id": string(r.OperationID),
		"resolution":   "nonexecution",
	}); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	return completedOutcome(reservationResourceBody{Resource: reservationWire(r)})
}

// decodeUsageJSON decodes a stored canonical settle usage report.
func decodeUsageJSON(raw string) (wireUsage, error) {
	var u wireUsage
	if err := contract.DecodeStrict([]byte(raw), &u); err != nil {
		return wireUsage{}, fmt.Errorf("accounting: stored settle usage does not decode: %w", err)
	}
	return u, nil
}
