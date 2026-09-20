package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
	_ "modernc.org/sqlite"
)

// seedRestoreSource opens a fresh database, migrates and writes one row of
// state, backs it up, and returns the backup bytes plus the schema versions
// and generation captured at that point. It is the shared fixture every
// restore test builds its "known good" source image from.
func seedRestoreSource(t *testing.T, ctx context.Context) (image []byte, schema []SchemaVersion, gen int64) {
	t.Helper()
	src := openTestDB(t, 0)
	if err := src.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate source: %v", err)
	}
	if _, err := src.StartGeneration(ctx); err != nil {
		t.Fatalf("start source generation: %v", err)
	}
	if err := bumpState(ctx, src, testActor(), testScope(), "source"); err != nil {
		t.Fatalf("seed source write: %v", err)
	}
	var buf bytes.Buffer
	if err := src.Backup(ctx, &buf); err != nil {
		t.Fatalf("backup source: %v", err)
	}
	schema, err := src.SchemaVersions(ctx)
	if err != nil {
		t.Fatalf("read source schema versions: %v", err)
	}
	gen, err = src.Generation(ctx)
	if err != nil {
		t.Fatalf("read source generation: %v", err)
	}
	return buf.Bytes(), schema, gen
}

// writeImageFile writes data to a fresh file in dir and returns its path.
func writeImageFile(t *testing.T, dir string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, "source-image.db")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write restore source image: %v", err)
	}
	return path
}

// TestRestorableInterfaceSatisfiedByOpen proves the value Open returns can
// be asserted to Restorable: the seam P31 (internal/installation) composes
// its contract.SnapshotInventory/RestoreCoordinator implementation from.
func TestRestorableInterfaceSatisfiedByOpen(t *testing.T) {
	t.Parallel()
	opened, err := Open(context.Background(), Config{Path: filepath.Join(t.TempDir(), "restorable.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = opened.Close() }()
	if _, ok := opened.(Restorable); !ok {
		t.Fatalf("Open result %T does not implement Restorable", opened)
	}
}

// TestPrepareRestoreRejectsInstallationMismatch proves a restore claiming a
// different destination installation refuses before any staging validation
// completes, and before the live database is touched.
func TestPrepareRestoreRejectsInstallationMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	imageBytes, schema, _ := seedRestoreSource(t, ctx)
	imagePath := writeImageFile(t, t.TempDir(), imageBytes)

	dest := openTestDB(t, 0)
	_, err := dest.PrepareRestore(ctx, RestoreImage{
		Path:                   imagePath,
		ExpectedInstallationID: contract.NewID(),
		InstallationID:         contract.NewID(),
		DatabaseDigest:         contract.Hash(imageBytes),
		SchemaVersions:         schema,
	})
	requireFault(t, err, contract.CodeConflict, false)
}

// TestPrepareRestoreRejectsDigestMismatch proves a wrong claimed digest
// refuses before replacement -- the required "wrong ... digest refuses
// before replacement" behavior.
func TestPrepareRestoreRejectsDigestMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	imageBytes, schema, _ := seedRestoreSource(t, ctx)
	imagePath := writeImageFile(t, t.TempDir(), imageBytes)
	installationID := contract.NewID()

	dest := openTestDB(t, 0)
	_, err := dest.PrepareRestore(ctx, RestoreImage{
		Path:                   imagePath,
		ExpectedInstallationID: installationID,
		InstallationID:         installationID,
		DatabaseDigest:         contract.Digest(strings.Repeat("0", 64)),
		SchemaVersions:         schema,
	})
	requireFault(t, err, contract.CodeConflict, false)

	// The live database is untouched: it opens and serves normally.
	if err := dest.Write(ctx, testActor(), testScope(), func(contract.Unit) error { return nil }); err != nil {
		t.Fatalf("live database write after refused restore: %v", err)
	}
}

// TestPrepareRestoreRejectsSchemaMismatch proves a manifest that misstates
// the staged image's applied migrations refuses before replacement -- the
// required "wrong ... schema ... refuses before replacement" behavior.
func TestPrepareRestoreRejectsSchemaMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	imageBytes, _, _ := seedRestoreSource(t, ctx)
	imagePath := writeImageFile(t, t.TempDir(), imageBytes)
	installationID := contract.NewID()

	dest := openTestDB(t, 0)
	wrongSchema := []SchemaVersion{{Owner: "testx", Version: 999, MigrationDigest: contract.Hash([]byte("not the real migration"))}}
	_, err := dest.PrepareRestore(ctx, RestoreImage{
		Path:                   imagePath,
		ExpectedInstallationID: installationID,
		InstallationID:         installationID,
		DatabaseDigest:         contract.Hash(imageBytes),
		SchemaVersions:         wrongSchema,
	})
	requireFault(t, err, contract.CodeConflict, false)
}

// TestPrepareRestoreRejectsNonDatabaseImage proves a corrupt or non-SQLite
// staged file is refused, not silently accepted as an empty database.
func TestPrepareRestoreRejectsNonDatabaseImage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	imagePath := writeImageFile(t, t.TempDir(), []byte("definitely not a sqlite database"))
	installationID := contract.NewID()

	dest := openTestDB(t, 0)
	_, err := dest.PrepareRestore(ctx, RestoreImage{
		Path:                   imagePath,
		ExpectedInstallationID: installationID,
		InstallationID:         installationID,
		DatabaseDigest:         contract.Hash([]byte("definitely not a sqlite database")),
		SchemaVersions:         nil,
	})
	requireFault(t, err, contract.CodeConflict, false)
}

// prepareValidRestore runs the full PrepareRestore validation path against
// dest with a known-good source image and returns the resulting staging.
func prepareValidRestore(t *testing.T, ctx context.Context, dest *database, imageBytes []byte, schema []SchemaVersion, installationID contract.ID) RestoreStaging {
	t.Helper()
	imagePath := writeImageFile(t, t.TempDir(), imageBytes)
	staging, err := dest.PrepareRestore(ctx, RestoreImage{
		Path:                   imagePath,
		ExpectedInstallationID: installationID,
		InstallationID:         installationID,
		DatabaseDigest:         contract.Hash(imageBytes),
		SchemaVersions:         schema,
	})
	if err != nil {
		t.Fatalf("prepare restore: %v", err)
	}
	return staging
}

// TestRestoreRoundTripReplacesLiveDatabaseAndBumpsGeneration proves
// CommitRestore actually replaces the live database's content with the
// source image's and hands back a database whose generation is strictly
// newer than the pre-restore live generation -- the required "reopened
// database has a newer generation" behavior.
func TestRestoreRoundTripReplacesLiveDatabaseAndBumpsGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	installationID := contract.NewID()
	imageBytes, schema, _ := seedRestoreSource(t, ctx)

	path := filepath.Join(t.TempDir(), "dest.db")
	dest := openTestDBAtPath(t, path, 0)
	if err := dest.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate destination: %v", err)
	}
	if _, err := dest.StartGeneration(ctx); err != nil {
		t.Fatalf("start destination generation: %v", err)
	}
	// Distinct destination-only state that must NOT survive the restore.
	if err := bumpState(ctx, dest, testActor(), testScope(), "destination-only"); err != nil {
		t.Fatalf("seed destination write: %v", err)
	}
	if err := bumpState(ctx, dest, testActor(), testScope(), "destination-only-2"); err != nil {
		t.Fatalf("seed destination write 2: %v", err)
	}
	preGen, err := dest.Generation(ctx)
	if err != nil {
		t.Fatalf("read destination generation: %v", err)
	}

	staging := prepareValidRestore(t, ctx, dest, imageBytes, schema, installationID)
	if staging.Digest() != contract.Hash(imageBytes) {
		t.Fatalf("staging digest = %s, want %s", staging.Digest(), contract.Hash(imageBytes))
	}

	restored, err := dest.CommitRestore(ctx, staging)
	if err != nil {
		t.Fatalf("commit restore: %v", err)
	}
	defer func() { _ = restored.Close() }()

	// The receiver is superseded: every call on it now observes closed.
	if _, err := dest.Generation(ctx); !isClosedFault(err) {
		t.Fatalf("generation on superseded receiver = %v, want closed fault", err)
	}

	postGen, err := restored.Generation(ctx)
	if err != nil {
		t.Fatalf("read restored generation: %v", err)
	}
	if postGen <= preGen {
		t.Fatalf("restored generation = %d, want strictly greater than pre-restore generation %d", postGen, preGen)
	}

	// The restored content is the SOURCE image's content (value 1), not the
	// destination's pre-restore content (value 2). Read runs even while
	// paused, so this exercises the restored data directly.
	var value int64
	err = restored.Read(ctx, testActor(), testScope(), func(u contract.Unit) error {
		return u.QueryRowContext(ctx, "SELECT value FROM testx_state WHERE id = 1").Scan(&value)
	})
	if err != nil {
		t.Fatalf("read restored state: %v", err)
	}
	if value != 1 {
		t.Fatalf("restored state value = %d, want 1 (the source image's value, not the destination's)", value)
	}
}

// TestRestorePausesWriteUntilResume proves the public mutation surface
// stays closed after CommitRestore until an explicit ResumeAfterRestore,
// that WriteRestoreOverlay is usable during that window for the owner
// overlay merge, and that Write works again -- and WriteRestoreOverlay no
// longer does -- once resumed.
func TestRestorePausesWriteUntilResume(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	installationID := contract.NewID()
	imageBytes, schema, _ := seedRestoreSource(t, ctx)

	dest := openTestDB(t, 0)
	if err := dest.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate destination: %v", err)
	}
	staging := prepareValidRestore(t, ctx, dest, imageBytes, schema, installationID)
	restored, err := dest.CommitRestore(ctx, staging)
	if err != nil {
		t.Fatalf("commit restore: %v", err)
	}
	defer func() { _ = restored.Close() }()

	paused, err := restored.RestorePaused(ctx)
	if err != nil {
		t.Fatalf("read restore-paused: %v", err)
	}
	if !paused {
		t.Fatal("expected the reopened database to be paused after CommitRestore")
	}

	// The public mutation surface refuses.
	writeErr := restored.Write(ctx, testActor(), testScope(), func(contract.Unit) error { return nil })
	requireFault(t, writeErr, contract.CodePrerequisiteMissing, false)

	// The owner overlay merge surface is usable while paused.
	if err := restored.WriteRestoreOverlay(ctx, testActor(), testScope(), func(u contract.Unit) error {
		_, err := u.ExecContext(ctx, "UPDATE testx_state SET value = value + 100 WHERE id = 1")
		return err
	}); err != nil {
		t.Fatalf("write restore overlay while paused: %v", err)
	}
	var value int64
	if err := restored.Read(ctx, testActor(), testScope(), func(u contract.Unit) error {
		return u.QueryRowContext(ctx, "SELECT value FROM testx_state WHERE id = 1").Scan(&value)
	}); err != nil {
		t.Fatalf("read after overlay write: %v", err)
	}
	if value != 101 {
		t.Fatalf("state value after overlay write = %d, want 101", value)
	}

	if err := restored.ResumeAfterRestore(ctx); err != nil {
		t.Fatalf("resume after restore: %v", err)
	}
	paused, err = restored.RestorePaused(ctx)
	if err != nil {
		t.Fatalf("read restore-paused after resume: %v", err)
	}
	if paused {
		t.Fatal("expected RestorePaused to be false after ResumeAfterRestore")
	}

	// The public mutation surface works again.
	if err := restored.Write(ctx, testActor(), testScope(), func(contract.Unit) error { return nil }); err != nil {
		t.Fatalf("write after resume: %v", err)
	}
	// The overlay surface no longer does: there is no active restore.
	overlayErr := restored.WriteRestoreOverlay(ctx, testActor(), testScope(), func(contract.Unit) error { return nil })
	requireFault(t, overlayErr, contract.CodeInvalidInput, false)
}

// TestRestorePauseSurvivesReopen proves the pause is durable -- not merely
// an in-process flag lost on restart -- by closing and reopening the
// restored database and checking Write still refuses.
func TestRestorePauseSurvivesReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	installationID := contract.NewID()
	imageBytes, schema, _ := seedRestoreSource(t, ctx)

	path := filepath.Join(t.TempDir(), "dest.db")
	dest := openTestDBAtPath(t, path, 0)
	if err := dest.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate destination: %v", err)
	}
	staging := prepareValidRestore(t, ctx, dest, imageBytes, schema, installationID)
	restored, err := dest.CommitRestore(ctx, staging)
	if err != nil {
		t.Fatalf("commit restore: %v", err)
	}
	if err := restored.Close(); err != nil {
		t.Fatalf("close restored: %v", err)
	}

	reopened := openTestDBAtPath(t, path, 0)
	paused, err := reopened.RestorePaused(ctx)
	if err != nil {
		t.Fatalf("read restore-paused after reopen: %v", err)
	}
	if !paused {
		t.Fatal("expected the pause to survive close and reopen")
	}
	writeErr := reopened.Write(ctx, testActor(), testScope(), func(contract.Unit) error { return nil })
	requireFault(t, writeErr, contract.CodePrerequisiteMissing, false)
}

// TestCommitRestoreExactlyOneWriter proves concurrent Write calls against
// the reopened database still serialize -- the required "exactly one
// writer" behavior survives a restore.
func TestCommitRestoreExactlyOneWriter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	installationID := contract.NewID()
	imageBytes, schema, _ := seedRestoreSource(t, ctx)

	dest := openTestDB(t, 0)
	if err := dest.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate destination: %v", err)
	}
	staging := prepareValidRestore(t, ctx, dest, imageBytes, schema, installationID)
	restored, err := dest.CommitRestore(ctx, staging)
	if err != nil {
		t.Fatalf("commit restore: %v", err)
	}
	defer func() { _ = restored.Close() }()
	if err := restored.ResumeAfterRestore(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}

	var tracker concurrentMax
	const n = 8
	errs := make(chan error, n)
	scope := testScope()
	for i := 0; i < n; i++ {
		go func() {
			errs <- restored.Write(ctx, testActor(), scope, func(u contract.Unit) error {
				tracker.enter()
				defer tracker.exit()
				_, err := u.ExecContext(ctx, "UPDATE testx_state SET value = value + 1 WHERE id = 1")
				return err
			})
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}
	if got := tracker.value(); got != 1 {
		t.Fatalf("max concurrent writer callbacks = %d, want 1 (exactly one writer)", got)
	}
}

// TestRestoreCrashBeforeCommitAbandonsAndKeepsOldDatabase simulates a crash
// between PrepareRestore (which durably wrote a "staged"-phase journal) and
// any CommitRestore call: reopening the live path must find the live
// database exactly as it was, with the abandoned restore cleaned up. This is
// the "staged-image boundary" arm of the required crash-recovery behavior.
func TestRestoreCrashBeforeCommitAbandonsAndKeepsOldDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	installationID := contract.NewID()
	imageBytes, schema, _ := seedRestoreSource(t, ctx)

	path := filepath.Join(t.TempDir(), "dest.db")
	dest := openTestDBAtPath(t, path, 0)
	if err := dest.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate destination: %v", err)
	}
	if err := bumpState(ctx, dest, testActor(), testScope(), "pre-crash"); err != nil {
		t.Fatalf("seed destination write: %v", err)
	}
	staging := prepareValidRestore(t, ctx, dest, imageBytes, schema, installationID)

	// Simulate the crash: no CommitRestore call, just an unclean process
	// exit. The journal and staged file are still on disk.
	if _, err := os.Stat(staging.journalPath); err != nil {
		t.Fatalf("expected a journal file on disk before recovery: %v", err)
	}
	if _, err := os.Stat(staging.stagedPath); err != nil {
		t.Fatalf("expected a staged file on disk before recovery: %v", err)
	}
	if err := dest.Close(); err != nil {
		t.Fatalf("close before simulated restart: %v", err)
	}

	// A fresh process reopens the same path.
	reopened := openTestDBAtPath(t, path, 0)
	if _, err := os.Stat(staging.journalPath); !os.IsNotExist(err) {
		t.Fatalf("expected the abandoned journal to be removed on reopen, stat err = %v", err)
	}
	if _, err := os.Stat(staging.stagedPath); !os.IsNotExist(err) {
		t.Fatalf("expected the abandoned staged file to be removed on reopen, stat err = %v", err)
	}
	var value int64
	if err := reopened.Read(ctx, testActor(), testScope(), func(u contract.Unit) error {
		return u.QueryRowContext(ctx, "SELECT value FROM testx_state WHERE id = 1").Scan(&value)
	}); err != nil {
		t.Fatalf("read reopened old database: %v", err)
	}
	if value != 1 {
		t.Fatalf("reopened old database state value = %d, want 1 (the pre-crash destination content)", value)
	}
	paused, err := reopened.RestorePaused(ctx)
	if err != nil {
		t.Fatalf("read restore-paused: %v", err)
	}
	if paused {
		t.Fatal("an abandoned restore must not leave the old database paused")
	}
}

// TestRestoreCrashAfterOldMovedCompletesForwardOnReopen simulates a crash
// exactly between the two renames CommitRestore performs -- the live file
// already moved aside, the staged file not yet swapped in -- by driving the
// journal to that phase directly and then reopening. Recovery must complete
// the swap, never leave the live path missing or bound to the wrong file.
func TestRestoreCrashAfterOldMovedCompletesForwardOnReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	installationID := contract.NewID()
	imageBytes, schema, _ := seedRestoreSource(t, ctx)

	path := filepath.Join(t.TempDir(), "dest.db")
	dest := openTestDBAtPath(t, path, 0)
	if err := dest.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate destination: %v", err)
	}
	staging := prepareValidRestore(t, ctx, dest, imageBytes, schema, installationID)

	// Drive the journal to "old_moved" the same way CommitRestore's first
	// phase would, then stop -- simulating a crash immediately after that
	// rename, before the second one runs.
	j, ok, err := readJournal(staging.journalPath)
	if err != nil || !ok {
		t.Fatalf("read journal: ok=%v err=%v", ok, err)
	}
	if err := dest.checkpointAndClose(); err != nil {
		t.Fatalf("checkpoint and close: %v", err)
	}
	if err := os.Rename(j.LivePath, j.OldPath); err != nil {
		t.Fatalf("simulate live-aside rename: %v", err)
	}
	j.Phase = restorePhaseOldMoved
	if err := writeJournal(staging.journalPath, j); err != nil {
		t.Fatalf("write mid-restore journal: %v", err)
	}
	// At this exact point the live path does not exist yet.
	if _, err := os.Stat(j.LivePath); !os.IsNotExist(err) {
		t.Fatalf("expected the live path to be absent mid-swap, stat err = %v", err)
	}

	reopened := openTestDBAtPath(t, path, 0)
	if _, err := os.Stat(staging.journalPath); !os.IsNotExist(err) {
		t.Fatalf("expected the journal to be removed after recovery completes, stat err = %v", err)
	}
	var value int64
	if err := reopened.Read(ctx, testActor(), testScope(), func(u contract.Unit) error {
		return u.QueryRowContext(ctx, "SELECT value FROM testx_state WHERE id = 1").Scan(&value)
	}); err != nil {
		t.Fatalf("read completed-forward database: %v", err)
	}
	if value != 1 {
		t.Fatalf("completed-forward database state value = %d, want 1 (the source image's content)", value)
	}
	if _, err := os.Stat(j.OldPath); err != nil {
		t.Fatalf("expected the pre-restore database to remain recoverable at %q: %v", j.OldPath, err)
	}
}

// TestRestoreCrashAfterSwappedRemovesJournalOnReopen simulates a crash
// after both renames completed but before the journal was removed: recovery
// must simply clean up the journal and leave the already-swapped database
// alone.
func TestRestoreCrashAfterSwappedRemovesJournalOnReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	installationID := contract.NewID()
	imageBytes, schema, _ := seedRestoreSource(t, ctx)

	path := filepath.Join(t.TempDir(), "dest.db")
	dest := openTestDBAtPath(t, path, 0)
	if err := dest.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate destination: %v", err)
	}
	staging := prepareValidRestore(t, ctx, dest, imageBytes, schema, installationID)

	j, ok, err := readJournal(staging.journalPath)
	if err != nil || !ok {
		t.Fatalf("read journal: ok=%v err=%v", ok, err)
	}
	if err := dest.checkpointAndClose(); err != nil {
		t.Fatalf("checkpoint and close: %v", err)
	}
	if err := os.Rename(j.LivePath, j.OldPath); err != nil {
		t.Fatalf("simulate live-aside rename: %v", err)
	}
	if err := os.Rename(j.StagedPath, j.LivePath); err != nil {
		t.Fatalf("simulate staged-into-place rename: %v", err)
	}
	j.Phase = restorePhaseSwapped
	if err := writeJournal(staging.journalPath, j); err != nil {
		t.Fatalf("write mid-restore journal: %v", err)
	}

	reopened := openTestDBAtPath(t, path, 0)
	if _, err := os.Stat(staging.journalPath); !os.IsNotExist(err) {
		t.Fatalf("expected the journal to be removed after recovery, stat err = %v", err)
	}
	var value int64
	if err := reopened.Read(ctx, testActor(), testScope(), func(u contract.Unit) error {
		return u.QueryRowContext(ctx, "SELECT value FROM testx_state WHERE id = 1").Scan(&value)
	}); err != nil {
		t.Fatalf("read swapped database: %v", err)
	}
	if value != 1 {
		t.Fatalf("swapped database state value = %d, want 1", value)
	}
}

// TestPrepareRestoreAfterCloseIsRejected proves the closed guard covers
// PrepareRestore.
func TestPrepareRestoreAfterCloseIsRejected(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, 0)
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, err := db.PrepareRestore(context.Background(), RestoreImage{Path: "unused"})
	if !isClosedFault(err) {
		t.Fatalf("prepare restore after close = %v, want closed fault", err)
	}
}

// TestCommitRestoreRejectsForeignStaging proves CommitRestore refuses a
// staging handle prepared against a different database file.
func TestCommitRestoreRejectsForeignStaging(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	installationID := contract.NewID()
	imageBytes, schema, _ := seedRestoreSource(t, ctx)

	a := openTestDB(t, 0)
	if err := a.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate a: %v", err)
	}
	staging := prepareValidRestore(t, ctx, a, imageBytes, schema, installationID)

	b := openTestDB(t, 0)
	_, err := b.CommitRestore(ctx, staging)
	requireFault(t, err, contract.CodeInvalidInput, false)
}
