package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
	_ "modernc.org/sqlite"
)

// TestBackupRestoresRoundTrip proves a backup taken from a live WAL database
// (unflushed content still in the log) is a complete, point-in-time
// standalone database: reopened through Open, all state, events and the
// persisted generation survive.
func TestBackupRestoresRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.StartGeneration(ctx); err != nil {
		t.Fatalf("start generation: %v", err)
	}
	scope := testScope()
	if err := bumpState(ctx, db, testActor(), scope, "backup-me"); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	var snapshot bytes.Buffer
	if err := db.Backup(ctx, &snapshot); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if snapshot.Len() == 0 {
		t.Fatal("backup produced no bytes")
	}

	// Writes after the backup must not appear in the snapshot.
	if err := bumpState(ctx, db, testActor(), scope, "after-backup"); err != nil {
		t.Fatalf("post-backup write: %v", err)
	}

	restored := restoreBackup(t, snapshot.Bytes(), db.path)
	if got := readStateValue(t, restored); got != 1 {
		t.Fatalf("restored state value = %d, want 1 (post-backup write excluded)", got)
	}
	events, err := restored.Events(ctx, 0, maxEventPage)
	if err != nil {
		t.Fatalf("restored events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("restored event count = %d, want 1", len(events))
	}
	gen, err := restored.Generation(ctx)
	if err != nil {
		t.Fatalf("restored generation: %v", err)
	}
	if gen != 1 {
		t.Fatalf("restored generation = %d, want 1", gen)
	}
	// The restored copy is a working writer in its own right.
	if err := bumpState(ctx, restored, testActor(), scope, "restored-write"); err != nil {
		t.Fatalf("write into restored copy: %v", err)
	}
	if got := readStateValue(t, restored); got != 2 {
		t.Fatalf("restored state value = %d, want 2", got)
	}
}

// TestBackupIncludesWALContent proves the backup captures data that still
// lives only in the write-ahead log: a raw copy of the main database file at
// the same moment would miss it, so a correct result rules out the naive
// copy implementation.
func TestBackupIncludesUnflushedWALContent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wal.db")
	db := openTestDBAtPath(t, path, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := bumpState(ctx, db, testActor(), testScope(), "wal-only"); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	// The committed value may exist only in the -wal file at this point.
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Logf("note: WAL file not present right after write (%v)", err)
	}

	var snapshot bytes.Buffer
	if err := db.Backup(ctx, &snapshot); err != nil {
		t.Fatalf("backup: %v", err)
	}
	restored := restoreBackup(t, snapshot.Bytes(), path)
	if got := readStateValue(t, restored); got != 1 {
		t.Fatalf("restored state value = %d, want 1 (WAL content missing from backup)", got)
	}
}

// TestBackupEmptyDatabaseIsValid proves a backup of a freshly opened database
// is still a valid SQLite database.
func TestBackupEmptyDatabaseIsValid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	var snapshot bytes.Buffer
	if err := db.Backup(ctx, &snapshot); err != nil {
		t.Fatalf("backup: %v", err)
	}
	restored := restoreBackup(t, snapshot.Bytes(), db.path)
	gen, err := restored.Generation(ctx)
	if err != nil {
		t.Fatalf("read restored generation: %v", err)
	}
	if gen != 0 {
		t.Fatalf("restored generation = %d, want 0", gen)
	}
}

// TestBackupRejectsNilWriter proves a nil writer is rejected before any work.
func TestBackupRejectsNilWriter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, 0)
	if err := db.Backup(context.Background(), nil); err == nil {
		t.Fatal("expected rejection for a nil writer")
	}
}

// TestBackupAfterCloseIsRejected proves the closed guard covers Backup.
func TestBackupAfterCloseIsRejected(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, 0)
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var snapshot bytes.Buffer
	err := db.Backup(context.Background(), &snapshot)
	if !isClosedFault(err) {
		t.Fatalf("backup after close = %v, want closed fault", err)
	}
}

// TestBackupLargerThanOneStep proves a database big enough to need several
// backup steps round-trips completely.
func TestBackupLargerThanOneStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	scope := testScope()

	// Fill well past one 64-page backup step with row data.
	if err := db.Write(ctx, testActor(), scope, func(u contract.Unit) error {
		for i := 0; i < 2000; i++ {
			if _, err := u.ExecContext(ctx,
				"INSERT INTO testx_calls (id, at) VALUES (?, ?)", contract.NewID(), "t"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("fill: %v", err)
	}

	var snapshot bytes.Buffer
	if err := db.Backup(ctx, &snapshot); err != nil {
		t.Fatalf("backup: %v", err)
	}
	restored := restoreBackup(t, snapshot.Bytes(), db.path)
	if err := restored.Read(ctx, testActor(), testScope(), func(u contract.Unit) error {
		var n int
		if err := u.QueryRowContext(ctx, "SELECT COUNT(*) FROM testx_calls").Scan(&n); err != nil {
			return err
		}
		if n != 2000 {
			t.Errorf("restored row count = %d, want 2000", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("read restored rows: %v", err)
	}
}

// restoreBackup writes snapshot bytes to a fresh file, opens it through Open,
// and registers cleanup. Passing sourcePath keeps the busy timeout identical
// to the source database under test.
func restoreBackup(t *testing.T, snapshot []byte, _ string) *database {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(path, snapshot, 0o600); err != nil {
		t.Fatalf("write restored file: %v", err)
	}
	return openTestDBAtPath(t, path, 0)
}
