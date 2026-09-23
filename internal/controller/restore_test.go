package controller

import (
	"context"
	"encoding/json"
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

// TestRestoreThreadsTheJobInputAndJournaledOverlayIntoTheLifecycle proves
// the two references P50 added to the pre-swap window actually reach the
// capability that needs them, and reach it in the form the real
// installation-backed implementation resolves: StageCandidate receives the
// restore job's own original input, which is the only carrier of the
// verified backup artifact reference anywhere in the protocol, and
// MergeOverlay receives the published recovery-overlay reference
// _installation.restore.overlay reported before the swap.
func TestRestoreThreadsTheJobInputAndJournaledOverlayIntoTheLifecycle(t *testing.T) {
	f := newFx(t)
	f.exec(`INSERT INTO marker_value (id, value) VALUES (1, 'live-content')`)
	img := f.buildBackupImage("backup-content")
	job := f.restoreJob(t)
	f.restoreBackups[job] = img
	f.restoreLifecycle = fakeRestoreLifecycle{f: f}
	registered := f.restoreOverlays[job]

	c, sess := f.started()
	awaitHandoff(t, f, c, sess)

	if f.stageCalls != 1 {
		t.Fatalf("StageCandidate ran %d times for one restore, want exactly 1", f.stageCalls)
	}
	var staged struct {
		BackupArtifact struct {
			ID     contract.ID     `json:"id"`
			Digest contract.Digest `json:"digest"`
		} `json:"backup_artifact"`
	}
	if err := json.Unmarshal(f.stagedInputs[0], &staged); err != nil {
		t.Fatalf("StageCandidate received input that is not the restore job's own: %v", err)
	}
	if staged.BackupArtifact.ID == "" || staged.BackupArtifact.Digest != stagedDigest {
		t.Fatalf("StageCandidate received %+v, want the job input's verified backup artifact", staged.BackupArtifact)
	}
	if len(f.mergedOverlays) != 1 {
		t.Fatalf("MergeOverlay ran %d times, want exactly 1", len(f.mergedOverlays))
	}
	if f.mergedOverlays[0] != registered {
		t.Fatalf("MergeOverlay received %+v, want the reference the owner registered (%+v)",
			f.mergedOverlays[0], registered)
	}
}

// TestRestoreResumeAfterACrashNeverRestagesTheBackupBundle is the required
// behavior "killing the process between a successful StageCandidate/
// CommitRestore and a completed MergeOverlay, then restarting, resumes from
// the journaled phase and finishes without re-decrypting the backup
// bundle." Staging is where the encrypted bundle is read, decrypted and
// written to disk in the clear, so a second StageCandidate call after
// recovery is exactly the waste (and the extra plaintext copy) the journal
// exists to prevent. The recovered merge must also receive the same
// journaled overlay reference, because the operation that produced it
// cannot be called again after the swap.
func TestRestoreResumeAfterACrashNeverRestagesTheBackupBundle(t *testing.T) {
	f := newFx(t)
	img := f.buildBackupImage("backup-content")
	job := f.restoreJob(t)
	f.restoreBackups[job] = img
	registered := f.restoreOverlays[job]
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
	if f.stageCalls != 1 {
		t.Fatalf("StageCandidate ran %d times before the crash, want 1", f.stageCalls)
	}

	// A genuine restart: the swap is already durable, so recovery resumes
	// from the journaled phase rather than re-staging anything.
	f.restart()
	f.started()

	if f.stageCalls != 1 {
		t.Fatalf("recovery re-decrypted the backup bundle: StageCandidate ran %d times in total, want 1", f.stageCalls)
	}
	if attempts != 2 {
		t.Fatalf("merge ran %d times across recovery, want exactly 2 (the failed attempt plus one success)", attempts)
	}
	if len(f.mergedOverlays) != 2 {
		t.Fatalf("MergeOverlay ran %d times, want 2", len(f.mergedOverlays))
	}
	for i, got := range f.mergedOverlays {
		if got != registered {
			t.Fatalf("merge attempt %d received overlay %+v, want the journaled reference %+v", i+1, got, registered)
		}
	}
	if got := f.queryString(`SELECT kind FROM marker_restored_obligations WHERE id = 'ob-1'`); got != "revocation" {
		t.Fatal("the obligation merged on recovery did not survive")
	}
}

// TestRestoreRefusesBeforeTheSwapWhenNoOverlayIsRegistered proves the
// pre-swap resolution is a real gate, not bookkeeping: a restore job whose
// owner registered no recovery overlay is recorded failed and the live
// database is never touched, because the refusal happens before
// performSwap runs at all. Resuming storage without merging an overlay
// would silently drop every obligation it retained, so refusing is the
// safe outcome, not merely the honest one.
func TestRestoreRefusesBeforeTheSwapWhenNoOverlayIsRegistered(t *testing.T) {
	f := newFx(t)
	f.exec(`INSERT INTO marker_value (id, value) VALUES (1, 'live-content')`)
	img := f.buildBackupImage("backup-content")
	job := f.restoreJob(t)
	f.restoreBackups[job] = img
	f.mu.Lock()
	delete(f.restoreOverlays, job)
	f.mu.Unlock()
	f.restoreLifecycle = fakeRestoreLifecycle{f: f}

	c, sess := f.started()
	for i := 0; i < 5; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}

	if got := f.queryString(`SELECT state FROM installation_restores ORDER BY seq DESC LIMIT 1`); got != jobStateFailed {
		t.Fatalf("restore disposition = %q, want failed", got)
	}
	if f.stageCalls != 0 {
		t.Fatalf("StageCandidate ran %d times after an unresolvable overlay; no bundle may be decrypted", f.stageCalls)
	}
	if got := f.queryString(`SELECT value FROM marker_value WHERE id = 1`); got != "live-content" {
		t.Fatalf("live database value = %q; a refused restore must never touch the database file", got)
	}
	select {
	case <-c.restoreHandoff:
		t.Fatal("handoff signalled for a restore that never swapped anything")
	default:
	}
}

// TestRestoreHandoffClosesTheDatabaseItAdoptedFromCommitRestore proves the
// controller hands back the one database handle it ever owns.
//
// CommitRestore closes the pre-restore database and returns a freshly
// reopened one, which this controller then serves from. Nothing outside
// this package can close that handle afterwards: entrypoint assembly's own
// field still names the pre-swap database, so its reassembly closes that
// one (a no-op) and opens a third. Before this was fixed, every completed
// restore left one extra live SQLite connection pool on the live file for
// the remaining life of the process -- measured directly against a real
// spawned `zatiti serve`: two pools on zatiti.db after a restore where a
// process doing the identical work without a restore holds exactly one.
//
// The assertion is the one that matters: after the lifetime ends, the
// adopted handle is genuinely closed, not merely dereferenced.
func TestRestoreHandoffClosesTheDatabaseItAdoptedFromCommitRestore(t *testing.T) {
	f := newFx(t)
	f.exec(`INSERT INTO marker_value (id, value) VALUES (1, 'live-content')`)
	img := f.buildBackupImage("backup-content")
	job := f.restoreJob(t)
	f.restoreBackups[job] = img
	f.restoreLifecycle = fakeRestoreLifecycle{f: f}

	c, sess := f.started()
	awaitHandoff(t, f, c, sess)

	c.mu.Lock()
	adopted := c.adopted
	c.mu.Unlock()
	if adopted == nil {
		t.Fatal("the controller did not adopt the database CommitRestore returned; nothing would ever close it")
	}
	if adopted != c.database() {
		t.Fatal("the adopted handle is not the one this lifetime serves from")
	}
	// Still usable while the lifetime is live: the merge and resume steps
	// run against it after the swap.
	if err := adopted.Read(context.Background(), f.actor, f.scope(), func(contract.Unit) error { return nil }); err != nil {
		t.Fatalf("the adopted database is unusable before the lifetime ends: %v", err)
	}

	// Ending the lifetime releases it.
	c.releaseAdopted()
	if err := adopted.Read(context.Background(), f.actor, f.scope(), func(contract.Unit) error { return nil }); err == nil {
		t.Fatal("the database adopted from CommitRestore is still open after the lifetime ended; it leaks for the life of the process")
	}
	c.mu.Lock()
	again := c.adopted
	c.mu.Unlock()
	if again != nil {
		t.Fatal("releasing the adopted handle did not clear it; a second release would close it twice")
	}
	// Safe to repeat: Run reaches this on every exit path.
	c.releaseAdopted()
}

// TestControllerNeverClosesTheDatabaseItWasConstructedWith is the other half
// of the ownership rule: a lifetime that never performs a swap adopts
// nothing, so ending it must leave entrypoint assembly's own database open.
// Closing that one would pull the file out from under the process that
// still owns it.
func TestControllerNeverClosesTheDatabaseItWasConstructedWith(t *testing.T) {
	f := newFx(t)
	c, sess := f.started()
	if err := f.pass(c, sess); err != nil {
		t.Fatalf("tick: %v", err)
	}
	c.mu.Lock()
	adopted := c.adopted
	c.mu.Unlock()
	if adopted != nil {
		t.Fatal("a controller that performed no swap adopted a database it does not own")
	}
	c.releaseAdopted()
	if err := f.raw.Read(context.Background(), f.actor, f.scope(), func(contract.Unit) error { return nil }); err != nil {
		t.Fatalf("the controller closed the database entrypoint assembly owns: %v", err)
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
