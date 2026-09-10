package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	_ "modernc.org/sqlite"
)

func TestWriteCommitsStateAndEventsTogether(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	scope := testScope()
	if err := bumpState(ctx, db, testActor(), scope, "w1"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readStateValue(t, db); got != 1 {
		t.Fatalf("state value = %d, want 1", got)
	}
	if got := countEvents(t, db); got != 1 {
		t.Fatalf("event count = %d, want 1", got)
	}
}

func TestCallbackErrorRollsBackStateAndEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	scope := testScope()
	actor := testActor()

	// State written, then the callback fails: neither state nor event commits.
	writeErr := db.Write(ctx, actor, scope, func(u contract.Unit) error {
		if _, err := u.ExecContext(ctx,
			"UPDATE testx_state SET value = value + 1, version = version + 1 WHERE id = 1"); err != nil {
			return err
		}
		if err := u.Emit(ctx, contract.Event{
			Kind:            "testx.counter.bumped",
			ResourceID:      scope.InstallationID,
			ResourceVersion: 1,
		}); err != nil {
			return err
		}
		return errors.New("injected failure after state and event writes")
	})
	if writeErr == nil {
		t.Fatal("expected callback error to surface")
	}
	if got := readStateValue(t, db); got != 0 {
		t.Fatalf("state value after rollback = %d, want 0", got)
	}
	if got := countEvents(t, db); got != 0 {
		t.Fatalf("event count after rollback = %d, want 0", got)
	}

	// Event emitted, then the state write fails: the event must not survive
	// without its state either.
	writeErr = db.Write(ctx, actor, scope, func(u contract.Unit) error {
		if err := u.Emit(ctx, contract.Event{
			Kind:            "testx.counter.bumped",
			ResourceID:      scope.InstallationID,
			ResourceVersion: 1,
		}); err != nil {
			return err
		}
		_, err := u.ExecContext(ctx, "INSERT INTO testx_state (id, value, version) VALUES (1, 5, 5)")
		return err
	})
	if writeErr == nil {
		t.Fatal("expected state write failure to surface")
	}
	if got := countEvents(t, db); got != 0 {
		t.Fatalf("event count after failed state write = %d, want 0", got)
	}
}

func TestCallbackPanicRollsBackAndIsFaulted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var panicErr error
	func() {
		// Write must recover the callback panic itself; this guard keeps a
		// regression from crashing the test binary.
		defer func() { _ = recover() }()
		panicErr = db.Write(ctx, testActor(), testScope(), func(u contract.Unit) error {
			if _, err := u.ExecContext(ctx,
				"UPDATE testx_state SET value = value + 1, version = version + 1 WHERE id = 1"); err != nil {
				return err
			}
			panic("boom inside callback")
		})
	}()
	if panicErr == nil {
		t.Fatal("expected the panic to surface as an error")
	}
	var fault *contract.Fault
	if !errors.As(panicErr, &fault) || fault.Code != contract.CodeInternalError {
		t.Fatalf("expected internal_error fault, got %v", panicErr)
	}
	if strings.Contains(fault.Message, "boom") {
		t.Fatalf("fault message leaked panic detail: %s", fault.Message)
	}
	if got := readStateValue(t, db); got != 0 {
		t.Fatalf("state value after panic rollback = %d, want 0", got)
	}
	// The writer must still work after the recovered panic.
	if err := bumpState(ctx, db, testActor(), testScope(), "after-panic"); err != nil {
		t.Fatalf("write after panic: %v", err)
	}
}

func TestCallbackRunsExactlyOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	scope := testScope()

	// A successful write registers its callback id once.
	ids := []contract.ID{contract.NewID(), contract.NewID()}
	for _, id := range ids {
		if err := db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
			_, err := u.ExecContext(ctx, "INSERT INTO testx_calls (id, at) VALUES (?, ?)", id, "t")
			return err
		}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// A failing write also runs once; its insert must roll back, not
	// partially survive.
	failed := contract.NewID()
	err := db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
		if _, err := u.ExecContext(ctx, "INSERT INTO testx_calls (id, at) VALUES (?, ?)", failed, "t"); err != nil {
			return err
		}
		return errors.New("injected")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if err := db.Read(ctx, testActor(), scope, func(u contract.Unit) error {
		var n int
		if err := u.QueryRowContext(ctx, "SELECT COUNT(*) FROM testx_calls").Scan(&n); err != nil {
			return err
		}
		if n != len(ids) {
			t.Fatalf("callback ran %d times total, want %d", n, len(ids))
		}
		return nil
	}); err != nil {
		t.Fatalf("read call count: %v", err)
	}
}

func TestWriteSerializesConcurrentWriters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	scope := testScope()

	const writers = 16
	maxConcurrency := &concurrentMax{}
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
				maxConcurrency.enter()
				defer maxConcurrency.exit()
				if _, err := u.ExecContext(ctx,
					"UPDATE testx_state SET value = value + 1, version = version + 1 WHERE id = 1"); err != nil {
					return err
				}
				return u.Emit(ctx, contract.Event{
					Kind:            "testx.counter.bumped",
					ResourceID:      scope.InstallationID,
					ResourceVersion: 1,
					Data:            []byte(fmt.Sprintf(`{"writer":%d}`, i)),
				})
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	if got := maxConcurrency.value(); got != 1 {
		t.Fatalf("max concurrent write callbacks = %d, want 1", got)
	}
	if got := readStateValue(t, db); got != writers {
		t.Fatalf("state value = %d, want %d (no lost updates)", got, writers)
	}
	if got := countEvents(t, db); got != writers {
		t.Fatalf("event count = %d, want %d", got, writers)
	}
}

func TestBusyExhaustionReturnsRetryableFault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "busy.db")
	db := openTestDBAtPath(t, path, 50*time.Millisecond)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// An external connection holds the single writer slot.
	side, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open side connection: %v", err)
	}
	defer func() { _ = side.Close() }()
	sideConn, err := side.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire side connection: %v", err)
	}
	defer func() { _ = sideConn.Close() }()
	if _, err := sideConn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("side begin: %v", err)
	}
	if _, err := sideConn.ExecContext(ctx,
		"UPDATE testx_state SET value = value + 100 WHERE id = 1"); err != nil {
		t.Fatalf("side update: %v", err)
	}

	// The writer waits the bounded busy window, then reports a retryable
	// controller_unavailable fault. The callback never runs.
	called := false
	err = db.Write(ctx, testActor(), testScope(), func(contract.Unit) error {
		called = true
		return nil
	})
	requireFault(t, err, contract.CodeControllerUnavailable, true)
	if called {
		t.Fatal("callback must not run when the write lock is unavailable")
	}
	_, _ = sideConn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")

	// Once the lock is released the write succeeds.
	if err := bumpState(ctx, db, testActor(), testScope(), "after-busy"); err != nil {
		t.Fatalf("write after busy: %v", err)
	}
	if got := readStateValue(t, db); got != 1 {
		t.Fatalf("state value = %d, want 1", got)
	}
}

func TestReadSnapshotStaysConsistentAcrossConcurrentWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := bumpState(ctx, db, testActor(), testScope(), "seed"); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	scope := testScope()

	inSnapshot := make(chan struct{})
	writeDone := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		readDone <- db.Read(ctx, testActor(), scope, func(u contract.Unit) error {
			var before int64
			if err := u.QueryRowContext(ctx, "SELECT value FROM testx_state WHERE id = 1").Scan(&before); err != nil {
				return err
			}
			inSnapshot <- struct{}{}
			<-writeDone // a writer commits while this snapshot is open
			var after int64
			if err := u.QueryRowContext(ctx, "SELECT value FROM testx_state WHERE id = 1").Scan(&after); err != nil {
				return err
			}
			if before != after {
				t.Errorf("snapshot changed mid-read: %d then %d", before, after)
			}
			return nil
		})
	}()

	<-inSnapshot
	if err := bumpState(ctx, db, testActor(), testScope(), "mid-snapshot"); err != nil {
		t.Fatalf("concurrent write: %v", err)
	}
	close(writeDone)
	if err := <-readDone; err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if got := readStateValue(t, db); got != 2 {
		t.Fatalf("state value after both = %d, want 2", got)
	}
	if got := countEvents(t, db); got != 2 {
		t.Fatalf("event count = %d, want 2", got)
	}
}

func TestReadUnitRejectsExecAndEmit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	err := db.Read(ctx, testActor(), testScope(), func(u contract.Unit) error {
		if !u.ReadOnly() {
			t.Error("read unit reports ReadOnly() = false")
		}
		if _, err := u.ExecContext(ctx, "UPDATE testx_state SET value = 99 WHERE id = 1"); err != nil {
			var fault *contract.Fault
			if !errors.As(err, &fault) || fault.Code != contract.CodePermissionDenied {
				t.Errorf("exec on read unit: got %v, want permission_denied", err)
			}
		} else {
			t.Error("exec on read unit succeeded")
		}
		if err := u.Emit(ctx, contract.Event{
			Kind:            "testx.counter.bumped",
			ResourceID:      contract.NewID(),
			ResourceVersion: 1,
		}); err != nil {
			var fault *contract.Fault
			if !errors.As(err, &fault) || fault.Code != contract.CodePermissionDenied {
				t.Errorf("emit on read unit: got %v, want permission_denied", err)
			}
		} else {
			t.Error("emit on read unit succeeded")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := countEvents(t, db); got != 0 {
		t.Fatalf("event count = %d, want 0", got)
	}
}

func TestUnitCarriesActorScopeGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.StartGeneration(ctx); err != nil {
		t.Fatalf("start generation: %v", err)
	}
	actor, scope := testActor(), testScope()

	err := db.Write(ctx, actor, scope, func(u contract.Unit) error {
		if u.Actor() != actor {
			t.Errorf("unit actor = %+v, want %+v", u.Actor(), actor)
		}
		if u.Scope() != scope {
			t.Errorf("unit scope = %+v, want %+v", u.Scope(), scope)
		}
		if u.Generation() != 1 {
			t.Errorf("unit generation = %d, want 1", u.Generation())
		}
		if u.ReadOnly() {
			t.Error("write unit reports ReadOnly() = true")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestWriteWithCanceledContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	called := false
	err := db.Write(canceled, testActor(), testScope(), func(contract.Unit) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
	if called {
		t.Fatal("callback must not run with a canceled context")
	}
	if got := readStateValue(t, db); got != 0 {
		t.Fatalf("state value = %d, want 0", got)
	}
}

func TestWriteRejectsInvalidCaller(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)

	cases := []struct {
		name  string
		actor contract.Actor
		scope contract.Scope
	}{
		{"empty principal", contract.Actor{Kind: contract.KindHuman}, testScope()},
		{"bad kind", contract.Actor{PrincipalID: contract.NewID(), Kind: "ghost"}, testScope()},
		{"empty scope", testActor(), contract.Scope{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := db.Write(ctx, tc.actor, tc.scope, func(u contract.Unit) error { return nil })
			requireFault(t, err, contract.CodeInvalidInput, false)
			readErr := db.Read(ctx, tc.actor, tc.scope, func(u contract.Unit) error { return nil })
			requireFault(t, readErr, contract.CodeInvalidInput, false)
		})
	}
}

func TestForeignKeyEnforcement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Inserting a child for a missing parent must fail on the foreign key
	// and leave nothing behind.
	err := db.Write(ctx, testActor(), testScope(), func(u contract.Unit) error {
		_, err := u.ExecContext(ctx,
			"INSERT INTO testx_child (id, parent_id) VALUES (?, ?)", contract.NewID(), contract.NewID())
		return err
	})
	if err == nil {
		t.Fatal("expected foreign key violation")
	}
	if err := db.Read(ctx, testActor(), testScope(), func(u contract.Unit) error {
		var n int
		if err := u.QueryRowContext(ctx, "SELECT COUNT(*) FROM testx_child").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("child rows after failed insert = %d, want 0", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("read children: %v", err)
	}
}
