// internal/state/lease.go
//
// Generation-bound work leases. Invariants:
//   - a lease is valid only while generation == current AND
//     expires_at > now; both are checked on every touch;
//   - controller restart advances the generation, voiding every lease
//     instantly with no cleanup pass (stale rows are overwritten in
//     place by the next acquirer);
//   - no timers: expiry is evaluated at read time; takeover of an
//     expired lease is a plain UPDATE inside the acquire transaction;
//   - heartbeats are themselves CAS: a worker that lost its lease
//     (expired and taken) discovers it at the next renew, never later.

package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LeaseTTL is the heartbeat deadline. A live worker renews well inside
// it; a dead worker's lease becomes takeable after it.
const LeaseTTL = 30 * time.Second

// ErrLeaseHeld: another live worker the resource.
var ErrLeaseHeld = errors.New("state: resource is leased by a live worker")

// ErrLeaseLost: the caller's lease is gone (expired, taken, or voided
// by generation advance). The worker must abort its unit of work.
var ErrLeaseLost = errors.New("state: lease lost")

// AcquireLease claims resource for workerID under generation. Returns
// nil on success; ErrLeaseHeld if a live lease exists.
func AcquireLease(ctx context.Context, db *sql.DB, resource, workerID string, generation uint64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var holder string
	var gen uint64
	var expires int64
	err = tx.QueryRowContext(ctx, `
		SELECT worker_id, generation, expires_at FROM leases WHERE resource = ?`,
		resource).Scan(&holder, &gen, &expires)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Free: insert.
	case err != nil:
		return fmt.Errorf("state: lease lookup: %w", err)
	default:
		live := gen == generation && expires > nowUnix()
		if live {
			return fmt.Errorf("%w: %s holds %s", ErrLeaseHeld, holder, resource)
		}
		// Expired or generation-voided: takeable below.
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO leases (resource, worker_id, generation, expires_at, acquired_at)
		VALUES (?, ?, ?, ?, strftime('%s','now'))
		ON CONFLICT(resource) DO UPDATE SET
			worker_id = excluded.worker_id,
			generation = excluded.generation,
			expires_at = excluded.expires_at,
			acquired_at = excluded.acquired_at`,
		resource, workerID, generation, nowUnix()+int64(LeaseTTL.Seconds())); err != nil {
		return fmt.Errorf("state: lease acquire: %w", err)
	}
	return tx.Commit()
}

// RenewLease extends the caller's lease. CAS on (resource, worker_id,
// generation, live): any mismatch is ErrLeaseLost. This is the
// heartbeat; call it well inside LeaseTTL.
func RenewLease(ctx context.Context, db *sql.DB, resource, workerID string, generation uint64) error {
	res, err := db.ExecContext(ctx, `
		UPDATE leases SET expires_at = strftime('%s','now') + ?
		WHERE resource = ? AND worker_id = ? AND generation = ?
		  AND expires_at > strftime('%s','now')`,
		int64(LeaseTTL.Seconds()), resource, workerID, generation)
	if err != nil {
		return fmt.Errorf("state: lease renew: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: %s", ErrLeaseLost, resource)
	}
	return nil
}

// ReleaseLease drops the caller's lease (work finished or aborting).
// Releasing a lease you no longer hold is fine — idempotent by design;
// the worker wanted it gone either way.
func ReleaseLease(ctx context.Context, db *sql.DB, resource, workerID string, generation uint64) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM leases
		WHERE resource = ? AND worker_id = ? AND generation = ?`,
		resource, workerID, generation)
	if err != nil {
		return fmt.Errorf("state: lease release: %w", err)
	}
	return nil
}

// LeaseHolder reports the current live holder, or "" if free. A stale-
// generation or expired row reports as free (it is, effectively).
func LeaseHolder(ctx context.Context, db *sql.DB, resource string, generation uint64) (string, error) {
	var holder string
	var gen uint64
	var expires int64
	err := db.QueryRowContext(ctx, `
		SELECT worker_id, generation, expires_at FROM leases WHERE resource = ?`,
		resource).Scan(&holder, &gen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("state: lease lookup: %w", err)
	}
	if gen != generation || expires <= nowUnix() {
		return "", nil
	}
	return holder, nil
}

func nowUnix() int64 { return time.Now().Unix() }
