package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// testActor returns a fresh human actor for tests.
func testActor() contract.Actor {
	return contract.Actor{
		PrincipalID:  contract.NewID(),
		Kind:         contract.KindHuman,
		CredentialID: contract.NewID(),
	}
}

// testScope returns a fresh installation scope for tests.
func testScope() contract.Scope {
	return contract.Scope{InstallationID: contract.NewID()}
}

// openTestDB opens a database in the test's temporary directory and closes
// it on cleanup.
func openTestDB(t *testing.T, busy time.Duration) *database {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	return openTestDBAtPath(t, path, busy)
}

// openTestDBAtPath opens a database at an explicit path. The caller owns the
// path lifetime.
func openTestDBAtPath(t *testing.T, path string, busy time.Duration) *database {
	t.Helper()
	opened, err := Open(context.Background(), Config{Path: path, BusyTimeout: busy})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db, ok := opened.(*database)
	if !ok {
		t.Fatalf("Open returned %T, want *database", opened)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// counterMigrations returns the test fixture schema for owner testx: a
// counter row, a call log with unique ids and a parent/child pair wired with
// a foreign key.
func counterMigrations() []contract.Migration {
	v1 := `
	CREATE TABLE testx_state (
		id      INTEGER PRIMARY KEY CHECK (id = 1),
		value   INTEGER NOT NULL,
		version INTEGER NOT NULL
	);
	INSERT INTO testx_state (id, value, version) VALUES (1, 0, 1);
	CREATE TABLE testx_calls (
		id TEXT PRIMARY KEY,
		at TEXT NOT NULL
	);
	CREATE TABLE testx_parent (
		id TEXT PRIMARY KEY
	);
	CREATE TABLE testx_child (
		id        TEXT PRIMARY KEY,
		parent_id TEXT NOT NULL REFERENCES testx_parent(id)
	);
	CREATE INDEX testx_state_version_idx ON testx_state (version);
	`
	return []contract.Migration{{
		Owner:   "testx",
		Version: 1,
		SQL:     v1,
		SHA256:  contract.Hash([]byte(v1)),
	}}
}

// bumpState increments the fixture counter inside one write transaction and
// emits one correlated event. It is the representative state+event write the
// atomicity tests fault around.
func bumpState(ctx context.Context, db contract.Database, actor contract.Actor, scope contract.Scope, writerID string) error {
	return db.Write(ctx, actor, scope, func(u contract.Unit) error {
		if _, err := u.ExecContext(ctx,
			"UPDATE testx_state SET value = value + 1, version = version + 1 WHERE id = 1"); err != nil {
			return err
		}
		data := fmt.Sprintf(`{"writer":%q}`, writerID)
		return u.Emit(ctx, contract.Event{
			Kind:            "testx.counter.bumped",
			ResourceID:      scope.InstallationID,
			ResourceVersion: 1,
			Data:            []byte(data),
		})
	})
}

// readStateValue returns the fixture counter value through a read snapshot.
func readStateValue(t *testing.T, db contract.Database) int64 {
	t.Helper()
	var value int64
	err := db.Read(context.Background(), testActor(), testScope(), func(u contract.Unit) error {
		return u.QueryRowContext(context.Background(), "SELECT value FROM testx_state WHERE id = 1").Scan(&value)
	})
	if err != nil {
		t.Fatalf("read state value: %v", err)
	}
	return value
}

// countEvents returns the number of events in the outbox.
func countEvents(t *testing.T, db contract.Database) int {
	t.Helper()
	events, err := db.Events(context.Background(), 0, maxEventPage)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return len(events)
}

// requireFault asserts that err carries a fault with the given code and
// retryable flag.
func requireFault(t *testing.T, err error, code string, retryable bool) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected fault %s, got nil error", code)
	}
	var fault *contract.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("expected a fault, got %v", err)
	}
	if fault.Code != code {
		t.Fatalf("expected fault code %s, got %s (%s)", code, fault.Code, fault.Message)
	}
	if fault.Retryable != retryable {
		t.Fatalf("expected retryable=%v, got %v", retryable, fault.Retryable)
	}
}

// concurrentMax tracks the maximum simultaneous value of a counter.
type concurrentMax struct {
	mu  sync.Mutex
	now atomic.Int64
	max int64
}

func (c *concurrentMax) enter() {
	n := c.now.Add(1)
	c.mu.Lock()
	if n > c.max {
		c.max = n
	}
	c.mu.Unlock()
}

func (c *concurrentMax) exit() { c.now.Add(-1) }
func (c *concurrentMax) value() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max
}
