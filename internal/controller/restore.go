package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// This file is the controller's half of the P00-011 six-step offline restore
// handoff (docs/implementation/contracts.md "Restore protocol (revision 3)").
// internal/installation's own restore.go captures the paused-snapshot
// RecoveryOverlay of this installation's CURRENT (pre-restore) state and
// leaves its execution_jobs row "running" with an external_action_required
// requirement naming the verified backup image: a domain module cannot swap
// the live SQLite file out from under its own open database mid-process, so
// that step -- and everything after it -- belongs here.
//
// This is deliberately NOT an ordinary JobRunner invocation: a JobRunner
// (Collaborators.Jobs) is invoked by claimJob/runJob while this SAME
// application/database handle stays open and serving other work, which is
// exactly what database replacement cannot tolerate. Instead the sequence
// below (1) quiesces admission and drains in-flight effects, (2) hands the
// already-verified backup off to storage.Restorable -- "entrypoint storage
// lifecycle code", reached directly because entrypoint assembly attaches
// this controller's db/adapters/collaborators the same way it would attach
// them to a freshly reopened database -- for the atomic file swap and
// generation advance, (3) merges the owner-opaque RecoveryOverlay through an
// entrypoint-supplied capability neither this package nor installation's own
// transactions can reach mid-swap, and (4) lifts the storage write gate.
//
// The one call this lifetime's own Application can never make again once
// CommitRestore succeeds is the final _installation.restore.record: that
// call needs the full schema-checked, policy-gated Invocation boundary
// (Application.Internal), and Application was constructed over the NOW
// CLOSED pre-restore database handle, which this package has no frozen seam
// to repoint. So a lifetime that itself performs the swap ends deliberately
// (ErrRestoreHandoff) the moment the swap, merge and resume are durable;
// entrypoint reassembles Application/Controller over the freshly reopened
// database (an ordinary storage.Open at the same path already observes the
// swap CommitRestore made durable) and calls Run again. That resumed
// lifetime's own start() recovers the still-open journal entry and records
// the disposition through its own, valid Application under its own, current
// generation -- never the one that performed the swap. A lifetime that only
// RECOVERS a swap some earlier, now-dead process already performed (its own
// Application was built fresh, after the swap, and was never stale) finishes
// the same entry inline and keeps serving.

const (
	// jobStateRunning mirrors the shared Job.state value execution's ledger
	// uses for a job an owner's own Finish already transitioned out of
	// "pending" without a controller claim -- the one Job state
	// _execution.job.pending must list for the controller to ever discover
	// installation.restore's external_action_required handoff, since
	// Prepare/Perform/Finish run synchronously inside one
	// Application.Invoke and never sit "pending" for any meaningful
	// duration.
	jobStateRunning = "running"
)

// ErrRestoreHandoff is what Run returns when this lifetime ends because it
// itself performed a restore's atomic database swap: the swap, owner
// overlay merge and storage resume are all durable, but this lifetime's own
// Application is now permanently unusable (built over the pre-restore
// database handle, which CommitRestore closed). It is not a displacement --
// ownership was never lost and no newer generation raced this one -- so a
// caller must not treat it as a fault. The caller reassembles
// Application/Controller exactly as at first startup (an ordinary
// storage.Open at the same path already observes the swap) and calls Run
// again; the resumed lifetime finishes recording the durable disposition
// under its own, current generation.
var ErrRestoreHandoff = errors.New(
	"controller: restore handoff; reopen storage at the same path, reassemble and run again")

// RestoreLifecycle is the entrypoint-supplied capability that hands the
// controller's exclusive restore handoff the two things only
// internal/installation's own private backup-bundle, key-resolution and
// overlay code can produce: a decrypted local candidate database image, and
// the merge of a captured RecoveryOverlay into every owner's own tables.
// Entrypoint assembly alone may implement this, backed by direct Go access
// to internal/installation, exactly as it alone supplies
// installation.WithDatabaseBackup; the controller never decrypts a backup
// bundle or resolves a secret reference itself, exactly as it never decodes
// a StagedOutput's bytes. A restore job observed without one attached is
// recorded failed with prerequisite_missing, never guessed at.
type RestoreLifecycle interface {
	// StageCandidate resolves restoreJobID's already-verified backup
	// artifact (installation.restore's Prepare/Perform already checked its
	// integrity and installation binding) into a locally staged, decrypted
	// candidate database file under dir, ready for
	// storage.Restorable.PrepareRestore. The caller owns dir and removes
	// the staged file once PrepareRestore has made its own copy.
	StageCandidate(ctx context.Context, restoreJobID contract.ID, dir string) (RestoreCandidate, error)

	// MergeOverlay folds restoreJobID's already-captured RecoveryOverlay
	// into every owner's own tables monotonically -- never resurrecting a
	// revoked credential, resending a consumed dispatch or erasing a
	// liability absent from the older snapshot -- against u, which
	// storage.Restorable.WriteRestoreOverlay opened on the freshly
	// reopened, still-paused database. Called exactly once per restore
	// attempt inside that one transaction; a recovered retry after a crash
	// between WriteRestoreOverlay and ResumeAfterRestore calls it again
	// with the same restoreJobID, so it must be safe to repeat.
	MergeOverlay(ctx context.Context, u contract.Unit, restoreJobID contract.ID) error
}

// RestoreCandidate is what StageCandidate resolved: a locally staged,
// decrypted database file plus the exact claims
// storage.Restorable.PrepareRestore validates it against before anything
// destructive runs.
type RestoreCandidate struct {
	// Path is a local filesystem path to the staged candidate database
	// file. The controller reads it but never mutates it, and removes it
	// once PrepareRestore has made its own copy.
	Path string
	// InstallationID is what the backup manifest claims; PrepareRestore
	// refuses a mismatch against this installation's own bound identity
	// before anything destructive runs.
	InstallationID contract.ID
	// DatabaseDigest is the manifest's claimed SHA-256 digest of the staged
	// image bytes.
	DatabaseDigest contract.Digest
	// SchemaVersions is the manifest's claimed set of applied owner
	// migrations, exactly as recorded inside the staged image's own
	// migration ledger.
	SchemaVersions []storage.SchemaVersion
}

const restoreStagingDirName = "restore-staging"

// database returns the controller's current database handle. Guarded: the
// restore protocol replaces this field from its own worker goroutine the
// instant CommitRestore's atomic swap commits, while the main loop
// concurrently reads it every tick (superseded, Events at start).
func (c *Controller) database() contract.Database {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.db
}

func (c *Controller) setDatabase(db contract.Database) {
	c.mu.Lock()
	c.db = db
	c.mu.Unlock()
}

func (c *Controller) restoreLifecycle() RestoreLifecycle {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deps.RestoreLifecycle
}

// beginHandoff signals Run's main loop to end this lifetime once, the
// moment this goroutine's own CommitRestore call made this lifetime's
// Application permanently stale.
func (c *Controller) beginHandoff() {
	select {
	case <-c.restoreHandoff:
	default:
		close(c.restoreHandoff)
	}
}

// restoreWork is the ordinary tick-flow step (never inside settle, which
// must never invoke local IO of its own): it resumes any open, currently
// unheld kindRestore journal entry through its remaining phases, or -- only
// once nothing is already in flight -- looks for a newly Finish-transitioned
// installation.restore job to begin. Exactly one restore is ever driven at a
// time; discovering a second while one is already open is never attempted.
func (c *Controller) restoreWork(ctx, workCtx context.Context, sess *session) {
	if c.abandoned.Load() {
		return
	}
	for _, e := range sess.journal.snapshot() {
		if e.Kind != kindRestore || !e.open() || c.held(e.ID) {
			continue
		}
		if e.Phase == phaseRestoreResumed {
			// A pure owner call away from done; settle() alone finishes it,
			// exactly as it alone finishes an ordinary claimed job's record.
			continue
		}
		c.restoring.Store(true)
		c.hold(e.ID)
		c.workers.Add(1)
		go func(e entry) {
			defer c.workers.Done()
			defer c.release(e.ID)
			c.runRestore(workCtx, sess, e)
		}(e)
		return
	}
	if c.restoring.Load() || !c.admitting() {
		return
	}
	var pending jobsOutput
	if err := c.call(ctx, sess, "_execution.job.pending", limitInput{Limit: c.batch()}, &pending); err != nil {
		c.note(err)
		return
	}
	for _, job := range pending.Items {
		if job.Owner != restoreOwner || job.Operation != restoreOperation {
			continue
		}
		if job.State != jobStateRunning {
			// Prepare committed but Finish has not run yet (or this is a
			// stray outcome_unknown entry from an unrelated recovery path);
			// nothing to hand off yet.
			return
		}
		e := entry{
			ID: string(contract.NewID()), Kind: kindRestore, Phase: phaseAdmitted, Generation: sess.generation,
			JobID: job.ID, JobVersion: job.Version, JobOwner: job.Owner, JobOp: job.Operation,
		}
		if !c.journal(sess, e) {
			return
		}
		c.restoring.Store(true)
		c.hold(e.ID)
		c.workers.Add(1)
		go func(e entry) {
			defer c.workers.Done()
			defer c.release(e.ID)
			c.runRestore(workCtx, sess, e)
		}(e)
		return
	}
}

// runRestore drives one restore journal entry forward from its current
// durable phase. It is safe to call again, unmodified, for an entry left at
// any open phase: every step first checks reality (RestorePaused) rather
// than trusting what phase was last written, so a crash at any boundary
// resumes correctly rather than repeating a destructive step or losing
// track of one that already committed. It never dispatches an adapter or an
// ordinary job runner.
func (c *Controller) runRestore(ctx context.Context, sess *session, e entry) {
	if e.Phase == phaseAdmitted {
		if !c.restoreClaim(ctx, sess, &e) {
			return
		}
		if !c.drainOrdinary(ctx) {
			// Retry a later tick; the entry stays exactly as journaled.
			return
		}
		e.Phase = phaseRestoreQuiescing
		if !c.journal(sess, e) {
			return
		}
	}

	restorable, ok := c.database().(storage.Restorable)
	if !ok {
		c.failRestore(ctx, sess, &e, prerequisiteMissing(
			"this database does not implement the offline restore protocol; restore cannot proceed"))
		return
	}

	performedSwap := false
	if e.Phase == phaseRestoreQuiescing {
		paused, err := restorable.RestorePaused(ctx)
		if err != nil {
			c.note(err)
			return
		}
		if !paused {
			next, ok := c.performSwap(ctx, sess, &e, restorable)
			if !ok {
				return
			}
			restorable = next
			performedSwap = true
		}
		e.Phase = phaseRestoreSwapped
		if !c.journal(sess, e) {
			return
		}
	}

	if performedSwap {
		// The controller's own field now names the freshly reopened
		// database: start()/superseded() must observe it, even though this
		// lifetime's Application still cannot.
		c.setDatabase(restorable)
	} else if e.Phase == phaseRestoreSwapped || e.Phase == phaseRestoreResumed {
		// Resuming past a swap this call did not itself perform: c.database()
		// already names the live handle (either this lifetime's own,
		// constructed fresh after an earlier one performed the swap, or the
		// same handle that already advanced above in this exact call).
		if r, ok := c.database().(storage.Restorable); ok {
			restorable = r
		}
	}

	if e.Phase == phaseRestoreSwapped {
		if !c.mergeAndResume(ctx, sess, restorable, e.JobID) {
			return
		}
		e.Phase = phaseRestoreResumed
		if !c.journal(sess, e) {
			return
		}
	}

	if e.Phase == phaseRestoreResumed && performedSwap {
		c.beginHandoff()
	}
	// Otherwise: settle() finishes it (final _installation.restore.record),
	// whether that runs later this same tick, at next start()'s recovery
	// pass, or on a resumed lifetime that never itself touched the swap.
}

// restoreClaim resolves the job's original input (naming the verified
// backup artifact) through a safe replay claim: installation's own Finish
// already transitioned this job to "running" under the current generation,
// so this call never contests an actual claim, it only reads back what was
// already committed.
func (c *Controller) restoreClaim(ctx context.Context, sess *session, e *entry) bool {
	var claimed jobClaimOutput
	err := c.write(func() error {
		return c.call(ctx, sess, "_execution.job.claim", jobClaimInput{
			JobID: e.JobID, ExpectedVersion: e.JobVersion, Generation: e.Generation,
		}, &claimed)
	})
	if err != nil {
		c.note(err)
		if !transient(err) {
			c.failRestore(ctx, sess, e, faultOf(err))
		}
		return false
	}
	e.JobInput = claimed.Input
	e.JobVersion = claimed.Job.Version
	return c.journal(sess, *e)
}

// drainOrdinary waits for every OTHER in-flight unit -- an adapter call, a
// job runner or a turn-work step already outside a transaction -- to finish
// and record its own outcome, so nothing else touches this lifetime's
// Application while the swap closes its database handle out from under it.
// It never interrupts that work itself: new admission already stopped the
// instant this restore was discovered, so the count only falls. It gives up
// (reporting false) only if ctx ends, in which case the entry stays exactly
// as journaled for a later attempt.
func (c *Controller) drainOrdinary(ctx context.Context) bool {
	const poll = 20 * time.Millisecond
	for {
		c.mu.Lock()
		n := c.status.InFlight
		c.mu.Unlock()
		if n <= 1 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(poll):
		}
	}
}

// performSwap hands the already-verified backup off to storage's own
// offline restore mechanics: stage a decrypted candidate image (via the
// entrypoint-supplied RestoreLifecycle -- the controller never decrypts a
// backup bundle itself), validate it, then atomically replace the live
// database file and advance the generation. It reports the freshly reopened
// Restorable on success.
func (c *Controller) performSwap(ctx context.Context, sess *session, e *entry, restorable storage.Restorable) (storage.Restorable, bool) {
	lifecycle := c.restoreLifecycle()
	if lifecycle == nil {
		c.failRestore(ctx, sess, e, prerequisiteMissing(
			"no restore lifecycle capability is attached; this database swap cannot proceed"))
		return nil, false
	}
	dir := filepath.Join(c.cfg.StateDir, journalDirName, restoreStagingDirName)
	if err := os.MkdirAll(dir, dirPrivate); err != nil {
		c.failRestore(ctx, sess, e, unavailable("restore staging directory cannot be created"))
		return nil, false
	}
	candidate, err := lifecycle.StageCandidate(ctx, e.JobID, dir)
	if err != nil {
		c.failRestore(ctx, sess, e, faultOf(err))
		return nil, false
	}
	defer func() {
		if candidate.Path != "" {
			_ = os.Remove(candidate.Path)
		}
	}()

	staging, err := restorable.PrepareRestore(ctx, storage.RestoreImage{
		Path:                   candidate.Path,
		ExpectedInstallationID: sess.scope.InstallationID,
		InstallationID:         candidate.InstallationID,
		DatabaseDigest:         candidate.DatabaseDigest,
		SchemaVersions:         candidate.SchemaVersions,
	})
	if err != nil {
		c.failRestore(ctx, sess, e, faultOf(err))
		return nil, false
	}

	next, err := restorable.CommitRestore(ctx, staging)
	if err != nil {
		c.note(err)
		// Ambiguous by construction: storage's own on-disk journal already
		// resolves whether the swap landed on the next Open (recovery
		// re-checks RestorePaused rather than trusting this branch), so the
		// entry stays exactly as journaled for a later attempt instead of
		// being guessed at here.
		return nil, false
	}
	return next, true
}

// mergeAndResume folds the restore's RecoveryOverlay into every owner's own
// tables through the entrypoint-supplied merge capability, inside the one
// transaction storage permits while paused, then lifts the storage write
// gate. Both steps are safe to repeat: WriteRestoreOverlay's own callback
// (MergeOverlay) must itself be monotonic/idempotent, and ResumeAfterRestore
// is skipped once RestorePaused already reports false.
func (c *Controller) mergeAndResume(ctx context.Context, sess *session, restorable storage.Restorable, jobID contract.ID) bool {
	lifecycle := c.restoreLifecycle()
	if lifecycle == nil {
		c.note(prerequisiteMissing("no restore lifecycle capability is attached; the recovery overlay cannot be merged"))
		return false
	}
	err := restorable.WriteRestoreOverlay(ctx, sess.actor, sess.scope, func(u contract.Unit) error {
		return lifecycle.MergeOverlay(ctx, u, jobID)
	})
	if err != nil {
		c.note(faultOf(err))
		return false
	}
	paused, err := restorable.RestorePaused(ctx)
	if err != nil {
		c.note(err)
		return false
	}
	if paused {
		if err := restorable.ResumeAfterRestore(ctx); err != nil {
			c.note(faultOf(err))
			return false
		}
	}
	return true
}

// failRestore records a definite, non-retryable failure while this
// lifetime's Application is still known-valid (only ever reached before a
// swap this call performed itself, since after that this lifetime never
// admits or writes ordinarily again) and closes the entry.
func (c *Controller) failRestore(ctx context.Context, sess *session, e *entry, f *contract.Fault) {
	err := c.write(func() error {
		return c.call(ctx, sess, "_installation.restore.record", restoreRecordInput{
			JobID: e.JobID, State: jobStateFailed,
			Requirements: []Requirement{{Code: f.Code, Message: f.Message}},
		}, nil)
	})
	if err != nil {
		c.note(err)
		if transient(err) {
			return
		}
	}
	e.Phase = phaseRefused
	e.Fault = f
	if c.journal(sess, *e) {
		c.oblige(obligationJob, e.JobID, f)
	}
	c.restoring.Store(false)
}

// settleRestore finishes an entry already at phaseRestoreResumed: the
// database swap, overlay merge and storage resume are all durable, and only
// the final _installation.restore.record call -- an ordinary owner write,
// never an adapter or job runner -- remains. It runs inline in settle,
// exactly as settleJob's own record step does.
func (c *Controller) settleRestore(ctx context.Context, sess *session, e entry) {
	err := c.write(func() error {
		return c.call(ctx, sess, "_installation.restore.record", restoreRecordInput{
			JobID: e.JobID, State: jobStateSucceeded, Requirements: []Requirement{},
		}, nil)
	})
	if err != nil {
		c.note(err)
		if !transient(err) {
			f := faultOf(err)
			e.Phase = phaseRefused
			e.Fault = f
			if c.journal(sess, e) {
				c.oblige(obligationJob, e.JobID, f)
			}
		}
		return
	}
	e.Phase = phaseDone
	e.Fault = nil
	c.journal(sess, e)
}

// recoverRestoreBeforeFence runs at the very start of a lifetime, before the
// ordinary generation fence: an ordinary write -- including the fence itself
// -- is refused outright while storage reports paused for restore, so a
// restore left mid-protocol by a dead process must be driven back to
// resumed (or safely re-attempted from scratch) here, never guessed at or
// silently bypassed.
func (c *Controller) recoverRestoreBeforeFence(ctx context.Context, sess *session) error {
	restorable, ok := c.database().(storage.Restorable)
	if !ok {
		return nil
	}
	paused, err := restorable.RestorePaused(ctx)
	if err != nil {
		return err
	}
	var open *entry
	for _, e := range sess.journal.snapshot() {
		if e.Kind == kindRestore && e.open() {
			ee := e
			open = &ee
			break
		}
	}
	if !paused {
		// Storage admits ordinary writes normally: any open entry left at
		// phaseRestoreResumed is finished by the ordinary settle() pass
		// below through this lifetime's own valid Application; an entry at
		// an earlier phase restarts cleanly from scratch on the ordinary
		// tick flow. Nothing here blocks the fence.
		return nil
	}
	if open == nil {
		return unavailable(
			"the database reports paused for restore but this controller's own journal has no record of one in progress; refusing to fence or admit over unexplained paused state")
	}
	c.restoring.Store(true)
	c.hold(open.ID)
	c.workers.Add(1)
	func() {
		defer c.workers.Done()
		defer c.release(open.ID)
		c.runRestore(ctx, sess, *open)
	}()
	stillPaused, err := restorable.RestorePaused(ctx)
	if err != nil {
		return err
	}
	if stillPaused {
		return unavailable("restore recovery could not lift the storage write gate; retrying next startup")
	}
	return nil
}
