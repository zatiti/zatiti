package accounting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Storage rows and access helpers. All timestamps persist as UTC RFC3339Nano
// strings and all JSON documents persist in canonical form, matching the
// shared storage conventions.

// isNoRows reports whether err is the standard empty-scan result.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// formatStamp renders a UTC RFC3339Nano timestamp.
func formatStamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseStamp parses a stored UTC RFC3339Nano timestamp.
func parseStamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// position is one aggregate counter row shared by every reservation against
// a dimension. Root and ancestor caps are shared across children through
// these rows, never copied per child.
type position struct {
	Kind        positionKind
	RefID       contract.ID
	InstallID   contract.ID
	Currency    string
	Spent       int64
	Reserved    int64
	Unknown     int64
	Estimated   int64
	Concurrency int64
	Version     int64
	UpdatedAt   time.Time
}

// zeroAmounts reports whether no monetary amount is recorded on the
// position; concurrency slots are live state, not amounts.
func (p *position) zeroAmounts() bool {
	return p.Spent == 0 && p.Reserved == 0 && p.Unknown == 0 && p.Estimated == 0
}

// loadPosition returns the position row for one dimension, or nil.
func loadPosition(ctx context.Context, unit contract.Unit, kind positionKind, ref contract.ID) (*position, error) {
	row := unit.QueryRowContext(ctx, `SELECT installation_id, currency, spent, reserved, unknown, estimated, concurrency, version, updated_at
		FROM accounting_positions WHERE kind = ? AND ref_id = ?`, string(kind), string(ref))
	var p position
	var updated string
	err := row.Scan(&p.InstallID, &p.Currency, &p.Spent, &p.Reserved, &p.Unknown, &p.Estimated, &p.Concurrency, &p.Version, &updated)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Kind = kind
	p.RefID = ref
	p.UpdatedAt, err = parseStamp(updated)
	if err != nil {
		return nil, fmt.Errorf("accounting: position %s/%s timestamp: %w", kind, ref, err)
	}
	return &p, nil
}

// ensurePosition creates the position row for one dimension when absent and
// returns its current state. The creation pins the currency; a later
// reservation books against the pinned currency, and an XXX-pinned position
// with no recorded amounts re-pins to the first configured currency.
func ensurePosition(ctx context.Context, unit contract.Unit, kind positionKind, ref contract.ID, install contract.ID, currency string, now time.Time) (*position, error) {
	_, err := unit.ExecContext(ctx, `INSERT INTO accounting_positions
		(kind, ref_id, installation_id, currency, spent, reserved, unknown, estimated, concurrency, version, updated_at)
		VALUES (?, ?, ?, ?, 0, 0, 0, 0, 0, 1, ?)
		ON CONFLICT (kind, ref_id) DO NOTHING`, string(kind), string(ref), string(install), currency, formatStamp(now))
	if err != nil {
		return nil, err
	}
	p, err := loadPosition(ctx, unit, kind, ref)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("accounting: position %s/%s missing after insert", kind, ref)
	}
	if p.Currency == unconfiguredCurrency && currency != unconfiguredCurrency && p.zeroAmounts() {
		if _, err := unit.ExecContext(ctx, `UPDATE accounting_positions SET currency = ?, version = version + 1, updated_at = ?
			WHERE kind = ? AND ref_id = ?`, currency, formatStamp(now), string(kind), string(ref)); err != nil {
			return nil, err
		}
		p.Currency = currency
	}
	return p, nil
}

// applyPositionDeltas applies checked amount and slot deltas to one
// position. Callers validate caps before calling; the update itself refuses
// to drive any counter negative.
func applyPositionDeltas(ctx context.Context, unit contract.Unit, p *position, dSpent, dReserved, dUnknown, dEstimated, dConcurrency int64, now time.Time) error {
	if p.Spent+dSpent < 0 || p.Reserved+dReserved < 0 || p.Unknown+dUnknown < 0 || p.Estimated+dEstimated < 0 || p.Concurrency+dConcurrency < 0 {
		return invalidInput("position %s/%s update would drive a counter negative", p.Kind, p.RefID)
	}
	_, err := unit.ExecContext(ctx, `UPDATE accounting_positions
		SET spent = spent + ?, reserved = reserved + ?, unknown = unknown + ?, estimated = estimated + ?,
			concurrency = concurrency + ?, version = version + 1, updated_at = ?
		WHERE kind = ? AND ref_id = ?`,
		dSpent, dReserved, dUnknown, dEstimated, dConcurrency, formatStamp(now), string(p.Kind), string(p.RefID))
	return err
}

// levelRef is one charged dimension of a reservation, persisted with the row
// so settlement reverses every position the reservation touched.
type levelRef struct {
	Kind positionKind `json:"kind"`
	Ref  contract.ID  `json:"ref"`
}

// reservationRow is one stored reservation with its replay fingerprint and
// sealed effective limits.
type reservationRow struct {
	ID              contract.ID
	Version         int64
	InstallID       contract.ID
	OrganizationID  contract.ID
	ProjectID       contract.ID
	WorkerID        contract.ID
	RootTaskID      contract.ID
	ScopeJSON       string
	OperationID     contract.ID
	Currency        string
	Amount          int64
	Slots           int64
	LimitsJSON      string
	RequestJSON     string
	PositionsJSON   string
	State           string
	SettleUsageJSON string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// reservation states.
const (
	stateReserved = "reserved"
	stateSettled  = "settled"
	stateUnknown  = "unknown"
	stateReleased = "released"
)

const reservationColumns = `id, version, installation_id, organization_id, project_id, worker_id, root_task_id,
	scope_json, operation_id, currency, amount, concurrency_slots, limits_json, request_json, positions_json, state,
	settle_usage_json, created_at, updated_at`

func scanReservation(scan func(dest ...any) error) (*reservationRow, error) {
	var r reservationRow
	var scopeJSON, limitsJSON, requestJSON, positionsJSON, settleUsage string
	var created, updated string
	var rootTask string
	err := scan(&r.ID, &r.Version, &r.InstallID, &r.OrganizationID, &r.ProjectID, &r.WorkerID, &rootTask,
		&scopeJSON, &r.OperationID, &r.Currency, &r.Amount, &r.Slots, &limitsJSON, &requestJSON, &positionsJSON,
		&r.State, &settleUsage, &created, &updated)
	if err != nil {
		return nil, err
	}
	r.RootTaskID = contract.ID(rootTask)
	r.ScopeJSON = scopeJSON
	r.LimitsJSON = limitsJSON
	r.RequestJSON = requestJSON
	r.PositionsJSON = positionsJSON
	r.SettleUsageJSON = settleUsage
	var perr error
	r.CreatedAt, perr = parseStamp(created)
	if perr != nil {
		return nil, fmt.Errorf("accounting: reservation %s timestamp: %w", r.ID, perr)
	}
	r.UpdatedAt, perr = parseStamp(updated)
	if perr != nil {
		return nil, fmt.Errorf("accounting: reservation %s timestamp: %w", r.ID, perr)
	}
	return &r, nil
}

// loadReservation returns one reservation by id, or nil.
func loadReservation(ctx context.Context, unit contract.Unit, id contract.ID) (*reservationRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+reservationColumns+` FROM accounting_reservations WHERE id = ?`, string(id))
	r, err := scanReservation(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return r, err
}

// loadReservationByOperation returns the reservation replaying one operation
// id, or nil.
func loadReservationByOperation(ctx context.Context, unit contract.Unit, operationID contract.ID) (*reservationRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+reservationColumns+` FROM accounting_reservations WHERE operation_id = ?`, string(operationID))
	r, err := scanReservation(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return r, err
}

// insertReservation persists a new reservation row.
func insertReservation(ctx context.Context, unit contract.Unit, r *reservationRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO accounting_reservations (`+reservationColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), r.Version, string(r.InstallID), string(r.OrganizationID), string(r.ProjectID), string(r.WorkerID),
		string(r.RootTaskID), r.ScopeJSON, string(r.OperationID), r.Currency, r.Amount, r.Slots, r.LimitsJSON,
		r.RequestJSON, r.PositionsJSON, r.State, r.SettleUsageJSON, formatStamp(r.CreatedAt), formatStamp(r.UpdatedAt))
	return err
}

// updateReservationState transitions one reservation with its new version
// and settle usage, fenced on the expected current version.
func updateReservationState(ctx context.Context, unit contract.Unit, r *reservationRow, newState string, settleUsageJSON string, now time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE accounting_reservations
		SET version = version + 1, state = ?, settle_usage_json = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		newState, settleUsageJSON, formatStamp(now), string(r.ID), r.Version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return staleVersion("reservation %s changed concurrently", r.ID)
	}
	r.Version++
	r.State = newState
	r.SettleUsageJSON = settleUsageJSON
	r.UpdatedAt = now
	return nil
}

// entry is one append-only exact ledger record.
type entry struct {
	ID            contract.ID
	ReservationID contract.ID
	InstallID     contract.ID
	Kind          string
	Currency      string
	Amount        int64
	Advisory      bool
	Note          string
	CreatedAt     time.Time
}

// ledger entry kinds.
const (
	entryReserved  = "reserved"
	entrySpent     = "spent"
	entryEstimated = "estimated"
	entryUnknown   = "unknown"
	entryReleased  = "released"
)

// insertEntry appends one ledger record in the same transaction.
func insertEntry(ctx context.Context, unit contract.Unit, e *entry) error {
	advisory := 0
	if e.Advisory {
		advisory = 1
	}
	_, err := unit.ExecContext(ctx, `INSERT INTO accounting_entries
		(id, reservation_id, installation_id, kind, currency, amount, advisory, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(e.ID), string(e.ReservationID), string(e.InstallID), e.Kind, e.Currency, e.Amount, advisory, e.Note, formatStamp(e.CreatedAt))
	return err
}
