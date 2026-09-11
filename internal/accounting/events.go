package accounting

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Event emission. Storage stamps event identity, sequence, timestamp and
// scope; handlers supply the kind and the resource coordinates. Kinds follow
// owner.entity.transition with at least three dot segments.

const (
	eventBudgetActivated     = "accounting.budget.activated"
	eventReservationReserved = "accounting.reservation.reserved"
	eventReservationSettled  = "accounting.reservation.settled"
	eventReservationUnknown  = "accounting.reservation.unknown"
	eventReservationReleased = "accounting.reservation.released"
)

// emitEvent appends one state-correlated event to the transaction outbox.
// A failure fails the handler and rolls back the mutation.
func emitEvent(ctx context.Context, unit contract.Unit, kind string, resourceID contract.ID, version contract.Version, data map[string]any) error {
	var raw json.RawMessage
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("accounting: encode %s event: %w", kind, err)
		}
		raw = encoded
	}
	err := unit.Emit(ctx, contract.Event{
		Kind:            kind,
		ResourceID:      resourceID,
		ResourceVersion: version,
		Data:            raw,
	})
	if err != nil {
		return fmt.Errorf("accounting: emit %s: %w", kind, err)
	}
	return nil
}
