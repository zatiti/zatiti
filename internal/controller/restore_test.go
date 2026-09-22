package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// mergeInsertsRestoredObligation stands in for a real owner's monotonic
// overlay merge: it re-inserts the exact "revoked/unknown" row the swapped-
// in backup file never had, exactly as internal/installation's own
// RecoveryOverlay -- captured from THIS installation's state just before the
// swap -- would let a real owner merge do.
func mergeInsertsRestoredObligation(id, kind string) func(context.Context, contract.Unit) error {
	return func(ctx context.Context, u contract.Unit) error {
		_, err := u.ExecContext(ctx,
			`INSERT INTO marker_restored_obligations (id, kind) VALUES (?, ?) ON CONFLICT(id) DO NOTHING`, id, kind)
		return err
	}
}

// awaitHandoff blocks until c signals ErrRestoreHandoff (the goroutine that
// performed the swap has finished and the channel is closed) or fails the
// test after a bounded number of ticks.
func awaitHandoff(t *testing.T, f *fx, c *Controller, sess *session) {
	t.Helper()
	for i := 0; i < 20; i++ {
		select {
		case <-c.restoreHandoff:
			return
		default:
		}
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	t.Fatal("restore never signalled ErrRestoreHandoff")
}

// TestRestoreSwapsActualDatabaseToSelectedBackupWhileNewerObligationsSurvive
// proves the assignment's first required behavior: the live database's
// actual content changes to the selected backup, while a revocation/unknown-
// liability obligation recorded after the backup was taken -- present only
// in the pre-swap live database, absent from the backup file itself --
// survives through the owner overlay merge.
func TestRestoreSwapsActualDatabaseToSelectedBackupWhileNewerObligationsSurvive(t *testing.T) {
	f := newFx(t)
	f.exec(`INSERT INTO marker_value (id, value) VALUES (1, 'live-content')`)
	f.exec(`INSERT INTO marker_restored_obligations (id, kind) VALUES ('ob-1', 'revocation')`)

	img := f.buildBackupImage("backup-content")
	job := f.restoreJob(t)
	f.restoreBackups[job] = img
	f.restoreMerge[job] = mergeInsertsRestoredObligation("ob-1", "revocation")
	f.restoreLifecycle = fakeRestoreLifecycle{f: f}

	c, sess := f.started()
	awaitHandoff(t, f, c, sess)

	// The lifetime that performed the swap never admits again and its own
	// Application is now stale; entrypoint reassembles over the reopened
	// database and calls Run again.
	f.restart()
	f.started()

	if got := f.queryString(`SELECT value FROM marker_value WHERE id = 1`); got != "backup-content" {
		t.Fatalf("live database value after restore = %q, want the selected backup's %q", got, "backup-content")
	}
	if got := f.queryString(`SELECT kind FROM marker_restored_obligations WHERE id = 'ob-1'`); got != "revocation" {
		t.Fatalf("obligation recorded after the backup was taken did not survive: %q", got)
	}
	if got := f.queryString(`SELECT state FROM installation_restores ORDER BY seq DESC LIMIT 1`); got != "succeeded" {
		t.Fatalf("final restore disposition = %q, want succeeded", got)
	}
	if got := f.queryString(`SELECT job_id FROM installation_restores ORDER BY seq DESC LIMIT 1`); got != string(job) {
		t.Fatalf("restore disposition named job %q, want %q", got, job)
	}
}

// TestRestoreCrashDuringOverlayMergeResumesPausedWithoutDispatch proves the
// assignment's second required behavior: a crash between the atomic swap
// and the owner overlay merge/resume -- simulated by a genuine process
// restart at exactly that boundary, the merge closure failing once to
// leave the swap durable but the merge not yet committed -- recovers by
// finishing the SAME restore (never re-swapping, never starting a second
// one) and never dispatches an unrelated pending effect's adapter call
// while doing it.
func TestRestoreCrashDuringOverlayMergeResumesPausedWithoutDispatch(t *testing.T) {
	f := newFx(t)
	adapter := f.adapter("synthetic")
	f.prepare("synthetic", nil) // an ordinary effect that must never dispatch mid-restore

	img := f.buildBackupImage("backup-content")
	job := f.restoreJob(t)
	f.restoreBackups[job] = img
	attempts := 0
	f.restoreMerge[job] = func(ctx context.Context, u contract.Unit) error {
		attempts++
		if attempts == 1 {
			return errors.New("simulated crash before the merge transaction could commit")
		}
		return mergeInsertsRestoredObligation("ob-1", "revocation")(ctx, u)
	}
	f.restoreLifecycle = fakeRestoreLifecycle{f: f}

	c, sess := f.started()
	if err := f.pass(c, sess); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("merge was attempted %d times before the simulated crash, want exactly 1", attempts)
	}
	select {
	case <-c.restoreHandoff:
		t.Fatal("handoff signalled despite the merge never succeeding; the swap must not be reported done")
	default:
	}
	// c.database(), not f.raw: the swap already replaced this lifetime's own
	// handle, and f.raw (the fixture's pre-swap handle) is now closed --
	// exactly the staleness this card's own protocol requires ending the
	// lifetime over, checked for real by TestNoOldGenerationCanCommitAfter
	// RestoreReopen below.
	restorable, ok := c.database().(storage.Restorable)
	if !ok {
		t.Fatalf("c.database() does not implement storage.Restorable")
	}
	paused, err := restorable.RestorePaused(context.Background())
	if err != nil {
		t.Fatalf("RestorePaused after simulated crash: %v", err)
	}
	if !paused {
		t.Fatal("expected the database to still report paused: the swap landed, only the merge failed")
	}
	if adapter.calls() != 0 {
		t.Fatalf("an adapter was invoked before recovery even began: %d calls", adapter.calls())
	}

	// Recovery: a genuine process restart. An ordinary entrypoint restart
	// reopens storage at the same path (already-durable per storage's own
	// crash-safe journal) and advances the generation again, exactly as it
	// would after any other crash. This lifetime never itself performs the
	// swap (that already happened, durably, before the crash), so its own
	// Application was never stale, and start()'s own recovery pass
	// (recoverRestoreBeforeFence, then settle) finishes the whole restore
	// -- merge retry, resume, final disposition -- inline, before start()
	// even returns; it never needs ErrRestoreHandoff or another restart.
	f.restart()
	f.started()

	if attempts != 2 {
		t.Fatalf("merge ran %d times across recovery, want exactly 2 (the failed attempt plus one success)", attempts)
	}
	restorable2, ok := f.raw.(storage.Restorable)
	if !ok {
		t.Fatalf("f.raw (post-restart) does not implement storage.Restorable")
	}
	stillPaused, err := restorable2.RestorePaused(context.Background())
	if err != nil {
		t.Fatalf("RestorePaused after recovery: %v", err)
	}
	if stillPaused {
		t.Fatal("recovery never lifted the storage write gate")
	}
	if adapter.calls() != 0 {
		t.Fatalf("recovery dispatched a provider call for an unrelated pending effect: %d calls", adapter.calls())
	}

	if got := f.queryString(`SELECT value FROM marker_value WHERE id = 1`); got != "backup-content" {
		t.Fatalf("live database value after recovered restore = %q, want %q", got, "backup-content")
	}
	if got := f.queryString(`SELECT kind FROM marker_restored_obligations WHERE id = 'ob-1'`); got != "revocation" {
		t.Fatal("obligation merged on recovery did not survive")
	}
	if got := f.queryString(`SELECT state FROM installation_restores ORDER BY seq DESC LIMIT 1`); got != "succeeded" {
		t.Fatalf("final restore disposition = %q, want succeeded", got)
	}
}

// TestNoOldGenerationCanCommitAfterRestoreReopen proves the assignment's
// third required behavior: once a restore's atomic swap has advanced the
// generation, a worker still holding the pre-restore session -- the exact
// value the goroutine that performed the swap itself used -- can never
// commit again, because that session's own Application was built over the
// now-closed pre-restore database handle.
func TestNoOldGenerationCanCommitAfterRestoreReopen(t *testing.T) {
	f := newFx(t)
	img := f.buildBackupImage("backup-content")
	job := f.restoreJob(t)
	f.restoreBackups[job] = img
	f.restoreLifecycle = fakeRestoreLifecycle{f: f}

	c, sess := f.started()
	oldGeneration := sess.generation
	awaitHandoff(t, f, c, sess)

	// This is the exact session the swap ran under: its own Application
	// (f.app / f.db, captured by New at construction) is now permanently
	// stale, closed by CommitRestore from inside this same lifetime.
	err := c.write(func() error {
		return c.call(context.Background(), sess, "_execution.tick", nowLimitInput{Now: f.clock.Now(), Limit: 1}, nil)
	})
	if err == nil {
		t.Fatal("an ordinary write under the pre-restore session committed after the swap; the old generation's Application must be unusable")
	}

	f.restart()
	_, newSess := f.started()
	if newSess.generation <= oldGeneration {
		t.Fatalf("generation did not advance across the restore: old=%d new=%d", oldGeneration, newSess.generation)
	}
}
