package accounting

import (
	"context"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Reservation. One reserve call books an enforceable amount and one
// concurrency slot atomically across installation, ancestor organizations
// (root down), project, worker and the root dimension, inside the caller's
// transaction. Storage serializes writers, so the per-level check-then-update
// sequence inside one write callback is atomic: no two admissions can
// oversubscribe a shared position. Task effects share the root-task position
// with every sibling; administrative effects use their operation id as the
// accountable admission root.

// reserveRequest is the canonical replay fingerprint of one reservation
// request. A repeated operation id with an identical fingerprint returns the
// existing reservation in any state; a different fingerprint is a conflict.
type reserveRequest struct {
	Scope      wireScope    `json:"scope"`
	RootTaskID *contract.ID `json:"root_task_id,omitempty"`
	Amount     wireMoney    `json:"amount"`
	Limits     wireLimits   `json:"limits"`
}

// handleReserve is the _accounting.reserve boundary.
func (s *Service) handleReserve(ctx context.Context, unit contract.Unit, in reserveInput) (contract.Outcome[reservationResourceBody], error) {
	if in.Scope.InstallationID == "" {
		return contract.Outcome[reservationResourceBody]{}, invalidInput("scope must name the installation")
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if in.Amount.MicroUnits < 0 {
		return contract.Outcome[reservationResourceBody]{}, invalidInput("amount must not be negative")
	}

	// Replay first: a repeated operation id never double-charges.
	if prior, err := loadReservationByOperation(ctx, unit, in.OperationID); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	} else if prior != nil {
		fingerprint, err := canonicalJSON(reserveRequest{
			Scope:      in.Scope,
			RootTaskID: in.RootTaskID,
			Amount:     in.Amount,
			Limits:     in.Limits,
		})
		if err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
		if string(fingerprint) != prior.RequestJSON {
			return contract.Outcome[reservationResourceBody]{}, conflict("operation %s already exists with a different request", in.OperationID)
		}
		return completedOutcome(reservationResourceBody{Resource: reservationWire(prior)})
	}

	now := s.now()
	if in.Amount.MicroUnits > 0 {
		if in.Amount.Currency == unconfiguredCurrency {
			return contract.Outcome[reservationResourceBody]{}, budgetUnavailable("reserving a positive amount requires an explicitly configured currency")
		}
		if err := s.requirePaidInstallation(ctx, unit, in.Scope.InstallationID); err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
	}

	levels, err := s.levelsForScope(ctx, unit, in.Scope)
	if err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	// The root dimension: the shared root task for task effects, the
	// operation id itself for administrative effects. Caller-declared limits
	// bind this level; undeclared root caps fall back to the shipped
	// defaults while a zero declared spend stays a real zero.
	rootRef := in.OperationID
	if in.RootTaskID != nil {
		rootRef = *in.RootTaskID
	}
	root, err := rootLevel(rootRef, &in.Limits)
	if err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	fillRootDefaults(&root.caps, now)
	levels = append(levels, root)

	effective, err := effectiveLimits(levels, now.Add(defaultRootDeadline))
	if err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if effective.Currency != unconfiguredCurrency && in.Amount.MicroUnits > 0 && in.Amount.Currency != effective.Currency {
		return contract.Outcome[reservationResourceBody]{}, invalidInput("currency mismatch: budgets for %s are configured in %s, the reservation declares %s",
			in.Scope.InstallationID, effective.Currency, in.Amount.Currency)
	}

	// Per-level capacity: enforceable exposure is spent plus reserved plus
	// unknown; estimated usage is advisory and never backs a hard cap.
	for i := range levels {
		if err := s.chargeLevel(ctx, unit, &levels[i], &in, now); err != nil {
			return contract.Outcome[reservationResourceBody]{}, err
		}
	}

	limitsJSON, err := canonicalJSON(effective)
	if err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	requestJSON, err := canonicalJSON(reserveRequest{
		Scope:      in.Scope,
		RootTaskID: in.RootTaskID,
		Amount:     in.Amount,
		Limits:     in.Limits,
	})
	if err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	scopeJSON, err := canonicalJSON(in.Scope)
	if err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	refs := make([]levelRef, 0, len(levels))
	for i := range levels {
		refs = append(refs, levelRef{Kind: levels[i].kind, Ref: levels[i].ref})
	}
	positionsJSON, err := canonicalJSON(refs)
	if err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	row := &reservationRow{
		ID:            s.deps.IDs.New(),
		Version:       1,
		InstallID:     in.Scope.InstallationID,
		ScopeJSON:     string(scopeJSON),
		OperationID:   in.OperationID,
		Currency:      in.Amount.Currency,
		Amount:        in.Amount.MicroUnits,
		Slots:         1,
		LimitsJSON:    string(limitsJSON),
		RequestJSON:   string(requestJSON),
		PositionsJSON: string(positionsJSON),
		State:         stateReserved,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	for _, l := range levels {
		switch l.kind {
		case posOrganization:
			row.OrganizationID = l.ref
		case posProject:
			row.ProjectID = l.ref
		case posWorker:
			row.WorkerID = l.ref
		}
	}
	// The reservation row records a root task only for task effects; an
	// administrative effect's operation-keyed root position is accountable
	// admission accounting, never a fictional task reference.
	if in.RootTaskID != nil {
		row.RootTaskID = *in.RootTaskID
	}
	if err := insertReservation(ctx, unit, row); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := insertEntry(ctx, unit, &entry{
		ID:            s.deps.IDs.New(),
		ReservationID: row.ID,
		InstallID:     row.InstallID,
		Kind:          entryReserved,
		Currency:      row.Currency,
		Amount:        row.Amount,
		Note:          "reserve",
		CreatedAt:     now,
	}); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	if err := emitEvent(ctx, unit, eventReservationReserved, row.ID, contract.Version(row.Version), map[string]any{
		"operation_id": string(row.OperationID),
		"amount":       row.Amount,
		"currency":     row.Currency,
	}); err != nil {
		return contract.Outcome[reservationResourceBody]{}, err
	}
	return completedOutcome(reservationResourceBody{Resource: reservationWire(row)})
}

// requirePaidInstallation refuses paid work until the installation carries an
// active budget with an explicit currency and a finite spend ceiling. The
// shipped default spend of zero refuses by the same rule.
func (s *Service) requirePaidInstallation(ctx context.Context, unit contract.Unit, install contract.ID) error {
	b, err := s.loadBudget(ctx, unit, install, install)
	if err != nil {
		return err
	}
	if b == nil {
		return budgetUnavailable("installation %s has no budget; paid execution stays unavailable until limits are configured", install)
	}
	limits, err := budgetLimits(b)
	if err != nil {
		return err
	}
	if limits.Currency == unconfiguredCurrency || limits.SpendMicroUnits <= 0 {
		return budgetUnavailable("installation %s budget does not configure a currency and a finite spend ceiling", install)
	}
	return nil
}

// chargeLevel checks one level's capacity and applies the reservation's
// deltas to its shared position. The caller's own declared root spend is a
// caller-consistency bound (invalid input when the amount exceeds the
// declaration outright); configured caps exhaust as budget unavailable.
func (s *Service) chargeLevel(ctx context.Context, unit contract.Unit, l *level, in *reserveInput, now time.Time) error {
	p, err := ensurePosition(ctx, unit, l.kind, l.ref, in.Scope.InstallationID, in.Amount.Currency, now)
	if err != nil {
		return err
	}
	if p.Currency != unconfiguredCurrency && p.Currency != in.Amount.Currency {
		return invalidInput("currency mismatch: the %s position is pinned to %s, the reservation declares %s",
			l.kind, p.Currency, in.Amount.Currency)
	}
	if in.Amount.MicroUnits > 0 && l.caps.spend != nil {
		exposure, err := addChecked(p.Spent, p.Reserved)
		if err != nil {
			return err
		}
		exposure, err = addChecked(exposure, p.Unknown)
		if err != nil {
			return err
		}
		exposure, err = addChecked(exposure, in.Amount.MicroUnits)
		if err != nil {
			return err
		}
		switch {
		case in.Amount.MicroUnits > *l.caps.spend && l.kind == posRootTask:
			return invalidInput("amount %d exceeds the caller-declared root spend ceiling %d", in.Amount.MicroUnits, *l.caps.spend)
		case in.Amount.MicroUnits > *l.caps.spend, exposure > *l.caps.spend:
			return budgetUnavailable("budget exhausted at %s %s: exposure %d would exceed the spend ceiling %d",
				l.kind, l.ref, exposure, *l.caps.spend)
		}
	}
	if l.caps.concurrency != nil && p.Concurrency+1 > *l.caps.concurrency {
		return budgetUnavailable("concurrency exhausted at %s %s: %d live attempts against a ceiling of %d",
			l.kind, l.ref, p.Concurrency, *l.caps.concurrency)
	}
	return applyPositionDeltas(ctx, unit, p, 0, in.Amount.MicroUnits, 0, 0, 1, now)
}

// reservationWire maps a stored reservation row to its wire form.
func reservationWire(r *reservationRow) wireReservation {
	scope, err := decodeScopeJSON(r.ScopeJSON)
	if err != nil {
		// Stored rows were written through the same encoder; a decode failure
		// is a storage-corruption fault, not an output-shape choice.
		scope = wireScope{InstallationID: r.InstallID}
	}
	out := wireReservation{
		ID:          r.ID,
		Version:     r.Version,
		Scope:       scope,
		OperationID: r.OperationID,
		Amount:      wireMoney{Currency: r.Currency, MicroUnits: r.Amount},
		State:       r.State,
	}
	if r.RootTaskID != "" {
		id := r.RootTaskID
		out.RootTaskID = &id
	}
	return out
}

// decodeScopeJSON decodes a stored canonical scope document.
func decodeScopeJSON(raw string) (wireScope, error) {
	var s wireScope
	if err := contract.DecodeStrict([]byte(raw), &s); err != nil {
		return wireScope{}, fmt.Errorf("accounting: stored scope does not decode: %w", err)
	}
	return s, nil
}
