package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	_ "modernc.org/sqlite"
)

// z14TestEvent returns a valid event in the testx namespace.
func z14TestEvent(scope contract.Scope, n int) contract.Event {
	return contract.Event{
		Kind:            "testx.counter.bumped",
		ResourceID:      scope.InstallationID,
		ResourceVersion: 1,
		Data:            []byte(fmt.Sprintf(`{"n":%d}`, n)),
	}
}

// TestEventSequencesIncreaseGlobally proves the persisted sequence is
// strictly increasing across transactions, including two events written in
// one transaction, and that caller-supplied identity fields are ignored.
func TestEventSequencesIncreaseGlobally(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	scope := testScope()

	// Two events inside one transaction get adjacent sequences.
	if err := db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
		for i := 1; i <= 2; i++ {
			if err := u.Emit(ctx, z14TestEvent(scope, i)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("double emit: %v", err)
	}
	// Caller-supplied identity fields are ignored: a bogus sequence and
	// timestamp do not survive.
	if err := db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
		e := z14TestEvent(scope, 3)
		e.Sequence = 9999
		e.ID = "not-assigned-by-storage"
		return u.Emit(ctx, e)
	}); err != nil {
		t.Fatalf("emit with spoofed identity: %v", err)
	}

	events, err := db.Events(ctx, 0, maxEventPage)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}
	for i, e := range events {
		if want := int64(i + 1); e.Sequence != want {
			t.Fatalf("event %d sequence = %d, want %d", i, e.Sequence, want)
		}
		if !validUUIDShape(string(e.ID)) {
			t.Fatalf("event %d id %q is not a storage-assigned UUID", i, e.ID)
		}
		if e.At.IsZero() {
			t.Fatalf("event %d has no storage-assigned timestamp", i)
		}
	}
}

// TestEventsPagination proves the after-cursor pages through the feed and
// that limit bounds are enforced.
func TestEventsPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	scope := testScope()
	for i := 1; i <= 5; i++ {
		if err := db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
			return u.Emit(ctx, z14TestEvent(scope, i))
		}); err != nil {
			t.Fatalf("emit %d: %v", i, err)
		}
	}

	page, err := db.Events(ctx, 0, 2)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(page) != 2 || page[0].Sequence != 1 || page[1].Sequence != 2 {
		t.Fatalf("first page = %v, want sequences [1 2]", seqs(page))
	}
	page, err = db.Events(ctx, 2, 2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(page) != 2 || page[0].Sequence != 3 || page[1].Sequence != 4 {
		t.Fatalf("second page = %v, want sequences [3 4]", seqs(page))
	}
	// Limit zero or negative selects the default page; five events fit.
	all, err := db.Events(ctx, 0, 0)
	if err != nil {
		t.Fatalf("default page: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("default page returned %d events, want 5", len(all))
	}
	if _, err := db.Events(ctx, 0, maxEventPage+1); err == nil {
		t.Fatal("expected rejection for an oversized page")
	} else {
		requireFault(t, err, contract.CodeInvalidInput, false)
	}
}

func seqs(events []contract.Event) []int64 {
	out := make([]int64, 0, len(events))
	for _, e := range events {
		out = append(out, e.Sequence)
	}
	return out
}

// TestEventsRoundTripFields proves the trusted feed restores every persisted
// field, including scope columns and the raw JSON payload.
func TestEventsRoundTripFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	scope := testScope()
	scope.OrganizationID = contract.NewID()
	scope.ProjectID = contract.NewID()
	scope.WorkerID = contract.NewID()
	scope.TaskID = contract.NewID()
	before := time.Now().UTC().Add(-time.Minute)

	if err := db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
		return u.Emit(ctx, z14TestEvent(scope, 7))
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}

	events, err := db.Events(ctx, 0, 1)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	e := events[0]
	if e.Scope != scope {
		t.Fatalf("event scope = %+v, want %+v", e.Scope, scope)
	}
	if e.Kind != "testx.counter.bumped" {
		t.Fatalf("event kind = %q", e.Kind)
	}
	if e.ResourceID != scope.InstallationID {
		t.Fatalf("event resource id = %q", e.ResourceID)
	}
	if e.ResourceVersion != 1 {
		t.Fatalf("event resource version = %d, want 1", e.ResourceVersion)
	}
	if string(e.Data) != `{"n":7}` {
		t.Fatalf("event data = %s, want {\"n\":7}", e.Data)
	}
	if e.At.Before(before) || e.At.After(time.Now().UTC().Add(time.Minute)) {
		t.Fatalf("event timestamp %v outside the test window", e.At)
	}
}

// TestEmitValidation proves the outbox rejects malformed events before any
// database work.
func TestEmitValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	scope := testScope()

	cases := []struct {
		name  string
		event contract.Event
	}{
		{"short kind", contract.Event{Kind: "testx.bumped", ResourceID: scope.InstallationID, ResourceVersion: 1}},
		{"empty kind segment", contract.Event{Kind: "testx..bumped", ResourceID: scope.InstallationID, ResourceVersion: 1}},
		{"empty resource", contract.Event{Kind: "testx.counter.bumped", ResourceVersion: 1}},
		{"non-uuid resource", contract.Event{Kind: "testx.counter.bumped", ResourceID: "abc", ResourceVersion: 1}},
		{"zero version", contract.Event{Kind: "testx.counter.bumped", ResourceID: scope.InstallationID}},
		{"invalid json data", contract.Event{
			Kind: "testx.counter.bumped", ResourceID: scope.InstallationID, ResourceVersion: 1,
			Data: []byte("{not json"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
				return u.Emit(ctx, tc.event)
			})
			requireFault(t, err, contract.CodeInvalidInput, false)
		})
	}

	// An event carrying an explicit foreign scope is rejected; a zero scope
	// is stamped with the unit's scope instead.
	err := db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
		other := testScope()
		foreign := z14TestEvent(other, 1)
		foreign.Scope = other
		return u.Emit(ctx, foreign)
	})
	requireFault(t, err, contract.CodeInvalidInput, false)
	if got := countEvents(t, db); got != 0 {
		t.Fatalf("event count = %d, want 0", got)
	}
}

// TestZ14FaultInjectionBetweenStateAndOutbox is acceptance case Z14: a fault
// injected after the state write but before (or during) the outbox write must
// roll back both halves. The injection is a BEFORE INSERT trigger on the
// outbox created through a raw side connection, mirroring a disk or
// constraint failure at exactly that boundary.
func TestZ14FaultInjectionBetweenStateAndOutbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	scope := testScope()

	side, err := sql.Open("sqlite", db.path)
	if err != nil {
		t.Fatalf("open side connection: %v", err)
	}
	defer func() { _ = side.Close() }()
	_, err = side.ExecContext(ctx, `CREATE TRIGGER z14_outbox_guard
		BEFORE INSERT ON storage_events
		BEGIN SELECT RAISE(ABORT, 'injected outbox failure'); END`)
	if err != nil {
		t.Fatalf("create outbox trigger: %v", err)
	}

	// The state write succeeds, then Emit hits the trigger: the whole
	// transaction must roll back so neither half survives.
	err = db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
		if _, err := u.ExecContext(ctx,
			"UPDATE testx_state SET value = value + 1, version = version + 1 WHERE id = 1"); err != nil {
			return err
		}
		return u.Emit(ctx, z14TestEvent(scope, 1))
	})
	if err == nil {
		t.Fatal("expected the injected outbox failure to surface")
	}
	var fault *contract.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("expected a contract fault, got %v", err)
	}
	if fault.Code != contract.CodeInternalError && fault.Code != contract.CodeControllerUnavailable {
		t.Fatalf("fault code = %s, want internal_error or controller_unavailable", fault.Code)
	}
	if got := readStateValue(t, db); got != 0 {
		t.Fatalf("state value after injected outbox failure = %d, want 0", got)
	}
	if got := countEvents(t, db); got != 0 {
		t.Fatalf("event count after injected outbox failure = %d, want 0", got)
	}

	// Remove the guard: the identical write now commits both halves.
	if _, err := side.ExecContext(ctx, "DROP TRIGGER z14_outbox_guard"); err != nil {
		t.Fatalf("drop outbox trigger: %v", err)
	}
	if err := db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
		if _, err := u.ExecContext(ctx,
			"UPDATE testx_state SET value = value + 1, version = version + 1 WHERE id = 1"); err != nil {
			return err
		}
		return u.Emit(ctx, z14TestEvent(scope, 2))
	}); err != nil {
		t.Fatalf("write after removing guard: %v", err)
	}
	if got := readStateValue(t, db); got != 1 {
		t.Fatalf("state value = %d, want 1", got)
	}
	if got := countEvents(t, db); got != 1 {
		t.Fatalf("event count = %d, want 1", got)
	}
}

// TestZ14FaultInjectionOnStateWrite is the mirror direction: the event is
// emitted first and the state write then fails through an injected trigger.
// The emitted event must not survive alone.
func TestZ14FaultInjectionOnStateWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	scope := testScope()

	side, err := sql.Open("sqlite", db.path)
	if err != nil {
		t.Fatalf("open side connection: %v", err)
	}
	defer func() { _ = side.Close() }()
	_, err = side.ExecContext(ctx, `CREATE TRIGGER z14_state_guard
		BEFORE UPDATE ON testx_state
		BEGIN SELECT RAISE(ABORT, 'injected state failure'); END`)
	if err != nil {
		t.Fatalf("create state trigger: %v", err)
	}

	err = db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
		if err := u.Emit(ctx, z14TestEvent(scope, 1)); err != nil {
			return err
		}
		_, err := u.ExecContext(ctx,
			"UPDATE testx_state SET value = value + 1, version = version + 1 WHERE id = 1")
		return err
	})
	if err == nil {
		t.Fatal("expected the injected state failure to surface")
	}
	if got := readStateValue(t, db); got != 0 {
		t.Fatalf("state value after injected state failure = %d, want 0", got)
	}
	if got := countEvents(t, db); got != 0 {
		t.Fatalf("event count after injected state failure = %d, want 0", got)
	}
}
