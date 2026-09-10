package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestOpenRejectsEmptyPath(t *testing.T) {
	t.Parallel()
	if _, err := Open(context.Background(), Config{}); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestOpenRejectsNegativeBusyTimeout(t *testing.T) {
	t.Parallel()
	if _, err := Open(context.Background(), Config{Path: "unused.db", BusyTimeout: -time.Second}); err == nil {
		t.Fatal("expected error for negative busy timeout")
	}
}

func TestOpenRejectsQuestionMarkPath(t *testing.T) {
	t.Parallel()
	// The driver DSN cannot express a path with a query separator.
	if _, err := Open(context.Background(), Config{Path: t.TempDir() + "/weird?name.db"}); err == nil {
		t.Fatal("expected error for path containing '?'")
	}
}

func TestOpenValidatesConnectionPragmas(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, 0) // zero picks the default busy timeout
	scope := testScope()

	err := db.Read(context.Background(), testActor(), scope, func(u contract.Unit) error {
		for _, check := range []struct {
			pragma string
			want   string
		}{
			{"PRAGMA journal_mode", "wal"},
			{"PRAGMA foreign_keys", "1"},
			{"PRAGMA synchronous", "2"},
			{"PRAGMA busy_timeout", "5000"},
		} {
			var got any
			if err := u.QueryRowContext(context.Background(), check.pragma).Scan(&got); err != nil {
				return err
			}
			if got := fmt.Sprint(got); got != check.want {
				t.Errorf("%s = %v, want %s", check.pragma, got, check.want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read pragmas: %v", err)
	}
}

func TestOpenRejectsPathInNonDatabaseFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "not-a-db")
	if err := os.WriteFile(path, []byte("definitely not a database"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := Open(context.Background(), Config{Path: path}); err == nil {
		t.Fatal("expected error opening a non-database file")
	}
}

func TestGenerationRestartsAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gen.db")

	// First boot: generation zero until StartGeneration advances it.
	first := openTestDBAtPath(t, path, 0)
	gen, err := first.Generation(ctx)
	if err != nil {
		t.Fatalf("read generation: %v", err)
	}
	if gen != 0 {
		t.Fatalf("initial generation = %d, want 0", gen)
	}
	started, err := first.StartGeneration(ctx)
	if err != nil {
		t.Fatalf("start generation: %v", err)
	}
	if started != 1 {
		t.Fatalf("first StartGeneration = %d, want 1", started)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Second boot: the persisted generation survives the restart and the
	// next start advances it by exactly one.
	second := openTestDBAtPath(t, path, 0)
	gen, err = second.Generation(ctx)
	if err != nil {
		t.Fatalf("read generation after restart: %v", err)
	}
	if gen != 1 {
		t.Fatalf("generation after restart = %d, want 1", gen)
	}
	started, err = second.StartGeneration(ctx)
	if err != nil {
		t.Fatalf("start generation after restart: %v", err)
	}
	if started != 2 {
		t.Fatalf("second StartGeneration = %d, want 2", started)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestStartGenerationSerializes(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, 0)
	ctx := context.Background()

	const n = 8
	results := make(chan int64, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			gen, err := db.StartGeneration(ctx)
			results <- gen
			errs <- err
		}()
	}
	seen := make(map[int64]bool)
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("StartGeneration: %v", err)
		}
		gen := <-results
		if gen < 1 || gen > n {
			t.Fatalf("generation %d out of range", gen)
		}
		if seen[gen] {
			t.Fatalf("generation %d handed out twice", gen)
		}
		seen[gen] = true
	}
	gen, err := db.Generation(ctx)
	if err != nil {
		t.Fatalf("read generation: %v", err)
	}
	if gen != n {
		t.Fatalf("final generation = %d, want %d", gen, n)
	}
}

func TestOperationsAfterCloseAreRejected(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, 0)
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ctx := context.Background()

	if _, err := db.Generation(ctx); !isClosedFault(err) {
		t.Fatalf("Generation after close = %v, want closed fault", err)
	}
	if _, err := db.StartGeneration(ctx); !isClosedFault(err) {
		t.Fatalf("StartGeneration after close = %v, want closed fault", err)
	}
	if _, err := db.Events(ctx, 0, 10); !isClosedFault(err) {
		t.Fatalf("Events after close = %v, want closed fault", err)
	}
	readErr := db.Read(ctx, testActor(), testScope(), func(contract.Unit) error { return nil })
	if !isClosedFault(readErr) {
		t.Fatalf("Read after close = %v, want closed fault", readErr)
	}
	writeErr := db.Write(ctx, testActor(), testScope(), func(contract.Unit) error { return nil })
	if !isClosedFault(writeErr) {
		t.Fatalf("Write after close = %v, want closed fault", writeErr)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func isClosedFault(err error) bool {
	var fault *contract.Fault
	if !errors.As(err, &fault) {
		return false
	}
	return fault.Code == contract.CodeControllerUnavailable && !fault.Retryable
}
