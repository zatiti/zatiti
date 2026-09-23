package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Read runs fn inside a consistent read snapshot on one connection. The
// snapshot is pinned when the unit is created; every query inside the
// callback sees the same state even if a writer commits meanwhile. Exec and
// Emit are rejected on the unit. The callback runs exactly once and the
// snapshot is released after it returns.
func (d *database) Read(ctx context.Context, actor contract.Actor, scope contract.Scope, fn func(contract.Unit) error) error {
	if err := d.entryCheck(); err != nil {
		return err
	}
	if err := validateCaller(actor, scope); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: read canceled before start: %w", err)
	}

	conn, err := d.db.Conn(ctx)
	if err != nil {
		return storageFault("acquire connection", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN DEFERRED"); err != nil {
		return txFault("begin read snapshot", err)
	}
	gen, err := loadGeneration(ctx, conn)
	if err != nil {
		_ = d.finishTxn(conn, false)
		return storageFault("read generation in snapshot", err)
	}
	u := &unit{conn: conn, actor: actor, scope: scope, gen: gen, readOnly: true}
	cbErr := invokeCallback(fn, u)
	// Read transactions are never committed: rollback releases the snapshot.
	_ = d.finishTxn(conn, false)
	return cbErr
}

// Write runs fn inside one short write transaction. Writers serialize on a
// single in-process lock, so callbacks never overlap. The transaction opens
// with BEGIN IMMEDIATE, the callback runs exactly once, and state plus every
// emitted event commit together. A callback error, fault or panic rolls the
// whole transaction back; Write never retries the callback. Busy lock
// exhaustion returns a retryable controller_unavailable fault.
//
// Write is the public mutation surface. It refuses with prerequisite_missing
// after a restore (see restore.go, CommitRestore) until an explicit
// ResumeAfterRestore completes; WriteRestoreOverlay is the narrower surface
// usable during that paused window.
//
// A recursive Write from inside a callback blocks on the writer lock and
// deadlocks; the dispatcher contract forbids recursion before storage is
// reached.
func (d *database) Write(ctx context.Context, actor contract.Actor, scope contract.Scope, fn func(contract.Unit) error) error {
	if err := d.entryCheck(); err != nil {
		return err
	}
	if d.restoring.Load() {
		return prerequisiteMissingFault("database is paused after a restore; an explicit resume must complete before the public mutation surface reopens")
	}
	return d.writeLocked(ctx, actor, scope, fn)
}

// writeLocked is the shared write-transaction body for Write and
// WriteRestoreOverlay; the two differ only in whether the post-restore
// pause admits the call.
func (d *database) writeLocked(ctx context.Context, actor contract.Actor, scope contract.Scope, fn func(contract.Unit) error) error {
	if err := validateCaller(actor, scope); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: write canceled before start: %w", err)
	}

	d.wmu.Lock()
	defer d.wmu.Unlock()

	conn, err := d.db.Conn(ctx)
	if err != nil {
		return storageFault("acquire connection", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return txFault("begin write transaction", err)
	}
	gen, err := loadGeneration(ctx, conn)
	if err != nil {
		_ = d.finishTxn(conn, false)
		return storageFault("read generation in transaction", err)
	}
	u := &unit{conn: conn, actor: actor, scope: scope, gen: gen}
	cbErr := invokeCallback(fn, u)
	if cbErr != nil {
		// The callback failed, returned a fault or panicked: nothing it
		// wrote, including emitted events, becomes visible.
		_ = d.finishTxn(conn, false)
		return cbErr
	}
	if err := d.finishTxn(conn, true); err != nil {
		_ = d.finishTxn(conn, false)
		return txFault("commit write transaction", err)
	}
	return nil
}

// entryCheck rejects operations on a closed database.
func (d *database) entryCheck() error {
	if d.closed.Load() {
		return closedFault()
	}
	return nil
}

// finishTxn commits or rolls back the transaction on conn. It runs on a
// context detached from cancellation so cleanup survives a canceled caller.
// A rollback on a transaction that already ended reports an error that is
// safe to ignore at call sites.
func (d *database) finishTxn(conn *sql.Conn, commit bool) error {
	stmt := "ROLLBACK"
	if commit {
		stmt = "COMMIT"
	}
	_, err := conn.ExecContext(context.WithoutCancel(context.Background()), stmt)
	return err
}

// invokeCallback runs fn exactly once, converting a panic into an
// internal_error fault so the transaction rolls back instead of crashing the
// writer. The panic value stays in the wrapped error for server-side logs
// and never reaches the fault message.
func invokeCallback(fn func(contract.Unit) error, u *unit) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &FaultError{
				Fault: &contract.Fault{
					Code:      contract.CodeInternalError,
					Message:   "internal error: transaction callback panicked",
					Retryable: false,
				},
				Err: fmt.Errorf("recovered panic in transaction callback: %v", r),
			}
		}
	}()
	return fn(u)
}

// unit is the transaction-scoped authority handed to callbacks. It wraps the
// one connection the transaction runs on; it is valid only inside its
// callback and must not be retained or shared with goroutines.
type unit struct {
	conn     *sql.Conn
	actor    contract.Actor
	scope    contract.Scope
	gen      int64
	readOnly bool
}

// QueryContext runs a query inside the caller's transaction or snapshot.
func (u *unit) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return u.conn.QueryContext(ctx, query, args...)
}

// QueryRowContext runs a single-row query inside the caller's transaction or
// snapshot.
func (u *unit) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return u.conn.QueryRowContext(ctx, query, args...)
}

// ExecContext runs a statement inside the write transaction. On a read
// snapshot unit it is rejected with permission_denied.
func (u *unit) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if u.readOnly {
		return nil, readOnlyFault("exec")
	}
	return u.conn.ExecContext(ctx, query, args...)
}

// Actor returns the authenticated caller bound to this transaction.
func (u *unit) Actor() contract.Actor { return u.actor }

// Scope returns the explicit scope bound to this transaction.
func (u *unit) Scope() contract.Scope { return u.scope }

// Generation returns the persisted controller generation as observed by this
// transaction: the value inside the write transaction or the read snapshot.
func (u *unit) Generation() int64 { return u.gen }

// ReadOnly reports whether the unit is a read snapshot. Exec and Emit are
// rejected on read snapshot units.
func (u *unit) ReadOnly() bool { return u.readOnly }

// Emit appends event to the storage event outbox inside the same
// transaction as the state change it correlates to. Storage assigns the
// event identity, sequence and timestamp. An event with no explicit scope is
// stamped with the unit's own scope, as before. An event may instead carry
// an explicit scope that narrows the unit's own -- more specific along any
// dimension the unit itself left unset -- and that narrower scope is what
// gets persisted, not the unit's coarser one: a bare-installation-scoped
// unit can emit (and have stored) a precisely worker- or task-scoped event.
// The unit's installation must always match, and any dimension the unit's
// own scope HAS set must equal the event's same dimension exactly; the unit
// may be coarser than the event but never contradict it. An event whose
// scope names a different installation, or contradicts a dimension the unit
// itself has set, is rejected.
func (u *unit) Emit(ctx context.Context, event contract.Event) error {
	if u.readOnly {
		return readOnlyFault("emit")
	}
	scope := u.scope
	if event.Scope != (contract.Scope{}) {
		if !scopeNarrows(u.scope, event.Scope) {
			return invalidInputFault("event scope does not match the unit scope")
		}
		scope = event.Scope
	}
	event.Scope = scope
	return appendEvent(ctx, u.conn, scope, event)
}

// scopeNarrows reports whether eventScope is a legitimate narrower-or-equal
// projection of unitScope: the installation must always match, and every
// other dimension unitScope has set must equal eventScope's same dimension.
// unitScope may leave a dimension unset that eventScope sets (the unit is
// coarser than the event), but it may never disagree with a dimension it
// does set. Mirrors evidence.scopeVisible (internal/evidence/store.go) and
// execution.narrowScope (internal/execution/scope.go), which apply this
// same relation in their own directions.
func scopeNarrows(unitScope, eventScope contract.Scope) bool {
	if unitScope.InstallationID != eventScope.InstallationID {
		return false
	}
	if unitScope.OrganizationID != "" && unitScope.OrganizationID != eventScope.OrganizationID {
		return false
	}
	if unitScope.ProjectID != "" && unitScope.ProjectID != eventScope.ProjectID {
		return false
	}
	if unitScope.WorkerID != "" && unitScope.WorkerID != eventScope.WorkerID {
		return false
	}
	if unitScope.TaskID != "" && unitScope.TaskID != eventScope.TaskID {
		return false
	}
	return true
}

// validateCaller checks the application-supplied authority before any
// database work. Public clients never supply actors; assembly does.
func validateCaller(actor contract.Actor, scope contract.Scope) error {
	if actor.PrincipalID == "" {
		return invalidInputFault("actor principal is required")
	}
	switch actor.Kind {
	case contract.KindHuman, contract.KindClientAgent, contract.KindWorker, contract.KindService:
	default:
		return invalidInputFault("actor kind %q is not a recognized principal kind", actor.Kind)
	}
	if scope.InstallationID == "" {
		return invalidInputFault("installation scope is required")
	}
	return nil
}
