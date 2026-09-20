package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// This file implements storage's half of the P00-011 six-step offline
// restore protocol (docs/implementation/contracts.md "Restore protocol
// (revision 3)"): validating an imported SQLite image and its schema
// versions in staging (protocol step 2's database-level part), the
// crash-safe atomic file swap under exclusive ownership (step 4), reopening
// with a newer generation (the storage-owned part of step 5), and keeping
// the public mutation surface paused until an explicit resume (step 6).
//
// Full protocol ownership -- installation binding against the destination's
// real identity, key availability, capturing and composing the
// RecoveryOverlay across every owner, and the contract.SnapshotInventory /
// contract.RestoreCoordinator values entrypoint assembly actually wires to
// internal/installation (P00-011, primary owner installation) -- is P31's
// job, not this package's: storage has no BlobStore and cannot resolve a
// contract.ArtifactRef to bytes, so it cannot satisfy those interfaces by
// itself. PrepareRestore/CommitRestore below take an already-staged local
// file path instead of an ArtifactRef; installation resolves the source
// artifact through its own BlobStore and is expected to compose these
// methods -- reached through the Restorable interface -- into the
// contract-shaped capability. Restore merge/reconciliation logic itself
// stays out of this package: WriteRestoreOverlay only reopens a narrow,
// paused-only write surface so owner migrations/ports can merge the typed
// overlay through their own methods; storage never decodes it.

// Restorable is implemented by the *database Open returns, alongside
// contract.Database. It is the typed capability card step 1 requires:
// migrations/schema checks and offline restore mechanics through Go methods,
// never arbitrary domain SQL. A caller that needs it type-asserts the
// contract.Database Open returns:
//
//	db, err := storage.Open(ctx, cfg)
//	restorable := db.(storage.Restorable)
type Restorable interface {
	contract.Database

	// SchemaVersions is storage's typed backup-inventory contribution; see
	// inventory.go.
	SchemaVersions(ctx context.Context) ([]SchemaVersion, error)

	// PrepareRestore validates a locally staged candidate database image
	// against the caller's claims about it and stages it beside the live
	// file, without touching the live file. It performs no destructive
	// action; a validation failure leaves the live database untouched.
	PrepareRestore(ctx context.Context, image RestoreImage) (RestoreStaging, error)

	// CommitRestore atomically replaces the live database with a
	// previously prepared staging under exclusive ownership: it checkpoints
	// and closes every connection to the live database, swaps the file,
	// reopens with a newer generation, and leaves the reopened database
	// paused (see RestorePaused) until ResumeAfterRestore runs. The
	// receiver is closed as part of this call, superseded by the returned
	// value; every method on the receiver after a successful CommitRestore
	// observes a closed fault.
	CommitRestore(ctx context.Context, staging RestoreStaging) (Restorable, error)

	// WriteRestoreOverlay runs fn inside one short write transaction, like
	// Write, but is usable only while RestorePaused is true. It is the
	// mechanism protocol step 5 requires: owner migrations/ports merge the
	// typed monotonic recovery overlay into their own tables through
	// ordinary transactional writes; storage neither decodes the overlay
	// nor decides what changes.
	WriteRestoreOverlay(ctx context.Context, actor contract.Actor, scope contract.Scope, fn func(contract.Unit) error) error

	// RestorePaused reports whether the database is currently paused after
	// a restore, refusing Write until ResumeAfterRestore.
	RestorePaused(ctx context.Context) (bool, error)

	// ResumeAfterRestore clears the post-restore pause, admitting Write
	// again. The caller must have completed the owner overlay merge and its
	// own verification (protocol steps 5 and 6) first; storage cannot check
	// that this happened; it only enforces that the pause lifts explicitly,
	// never implicitly.
	ResumeAfterRestore(ctx context.Context) error
}

var _ Restorable = (*database)(nil)

// RestoreImage names a locally staged candidate database file and the
// caller's own pre-verified expectations for it: the exact claims a trusted
// backup manifest makes. Installation resolves the manifest's source
// contract.ArtifactRef to local bytes through its own BlobStore -- storage
// has no blob access and never fetches artifact bytes itself.
// PrepareRestore's entire job is refusing, before anything destructive
// happens, when the bytes actually on disk do not match those claims.
type RestoreImage struct {
	// Path is a local filesystem path to the already-staged candidate
	// database file. PrepareRestore reads it but never mutates or removes
	// it; the file it stages internally is its own copy.
	Path string

	// ExpectedInstallationID is the destination's own bound identity,
	// resolved by the caller (e.g. from the platform installation lock).
	// InstallationID is what the source manifest claims. Storage does not
	// own installation identity itself; it only enforces that these two
	// caller-supplied values match, refusing before replacement otherwise
	// -- protocol step 2's installation-binding check.
	ExpectedInstallationID contract.ID
	InstallationID         contract.ID

	// DatabaseDigest is the manifest's claimed SHA-256 digest of the staged
	// image bytes.
	DatabaseDigest contract.Digest

	// SchemaVersions is the manifest's claimed set of applied owner
	// migrations. Every entry must match, byte-identical, what is actually
	// recorded inside the staged image's own storage_migrations table --
	// protocol step 2's schema-compatibility check.
	SchemaVersions []SchemaVersion
}

// RestoreStaging is the crash-safe handle PrepareRestore returns: a
// validated candidate image staged beside the live database file, plus a
// durable journal marker recording enough state for CommitRestore -- or an
// interrupted Open -- to reach a consistent outcome after a crash at any
// boundary. It carries no destructive authority by itself: only
// CommitRestore, called against the *database PrepareRestore was called on,
// consumes it.
type RestoreStaging struct {
	livePath    string
	stagedPath  string
	journalPath string
	oldPath     string

	digest         contract.Digest
	size           int64
	schemaVersions []SchemaVersion
}

// Digest returns the validated staged image's SHA-256 digest, matching the
// manifest claim PrepareRestore checked it against.
func (s RestoreStaging) Digest() contract.Digest { return s.digest }

// Size returns the validated staged image's byte size.
func (s RestoreStaging) Size() int64 { return s.size }

// SchemaVersions returns the validated staged image's own applied
// migrations, as read from its storage_migrations table.
func (s RestoreStaging) SchemaVersions() []SchemaVersion {
	out := make([]SchemaVersion, len(s.schemaVersions))
	copy(out, s.schemaVersions)
	return out
}

// restoreJournal is the durable on-disk marker CommitRestore writes before
// any irreversible step and removes only once the swap is fully durable.
// Its presence at Open is the signal a previous CommitRestore was
// interrupted.
type restoreJournal struct {
	Schema     string `json:"schema"` // zatiti.storage.restore-journal/v1
	LivePath   string `json:"live_path"`
	StagedPath string `json:"staged_path"`
	OldPath    string `json:"old_path"`
	Phase      string `json:"phase"` // staged | old_moved | swapped
	StartedAt  string `json:"started_at"`
}

const (
	restoreJournalSchema = "zatiti.storage.restore-journal/v1"
	restoreJournalSuffix = ".restore-journal"
	preRestoreSuffix     = ".restore-previous"

	// restorePhaseStaged: PrepareRestore validated and wrote the journal;
	// CommitRestore has not yet moved any file. A journal found in this
	// phase at Open means no destructive action was authorized to
	// completion (or was ever taken): the live database is untouched and
	// recovery abandons the prepared restore.
	restorePhaseStaged = "staged"
	// restorePhaseOldMoved: the live file has been renamed aside; the
	// staged file may or may not have been renamed into its place yet.
	// CommitRestore's authorization already took effect, so recovery
	// completes forward rather than reverting.
	restorePhaseOldMoved = "old_moved"
	// restorePhaseSwapped: the staged file now occupies the live path.
	// Nothing but removing the journal remains.
	restorePhaseSwapped = "swapped"
)

// PrepareRestore validates image against the caller's claims about it and
// stages a private copy beside the live database file, without touching the
// live file. Validation, all of it before the journal is written: the
// installation binding, the staged bytes' SHA-256 digest, an integrity
// check, and an exact match against the claimed schema versions. Any
// failure leaves the live database completely untouched.
func (d *database) PrepareRestore(ctx context.Context, image RestoreImage) (RestoreStaging, error) {
	if d.closed.Load() {
		return RestoreStaging{}, closedFault()
	}
	if image.Path == "" {
		return RestoreStaging{}, invalidInputFault("restore image path is required")
	}
	if image.ExpectedInstallationID == "" || image.InstallationID == "" {
		return RestoreStaging{}, invalidInputFault("restore requires both the destination's expected installation id and the source manifest's installation id")
	}
	if image.ExpectedInstallationID != image.InstallationID {
		return RestoreStaging{}, conflictFault("restore source installation %q does not match the destination installation %q", image.InstallationID, image.ExpectedInstallationID)
	}
	if image.DatabaseDigest == "" {
		return RestoreStaging{}, invalidInputFault("restore requires the manifest's claimed database digest")
	}
	if err := ctx.Err(); err != nil {
		return RestoreStaging{}, fmt.Errorf("storage: prepare restore canceled before start: %w", err)
	}

	stagedPath, digest, size, err := stageImageCopy(d.path, image.Path)
	if err != nil {
		return RestoreStaging{}, err
	}
	if digest != image.DatabaseDigest {
		_ = os.Remove(stagedPath)
		return RestoreStaging{}, conflictFault("staged restore image digest %s does not match the manifest's claimed digest %s", digest, image.DatabaseDigest)
	}

	actual, err := inspectStagedSchema(ctx, stagedPath)
	if err != nil {
		_ = os.Remove(stagedPath)
		return RestoreStaging{}, err
	}
	if !equalSchemaVersions(actual, image.SchemaVersions) {
		_ = os.Remove(stagedPath)
		return RestoreStaging{}, conflictFault("staged restore image schema versions do not match the manifest's claim")
	}

	journalPath := d.path + restoreJournalSuffix
	j := restoreJournal{
		Schema:     restoreJournalSchema,
		LivePath:   d.path,
		StagedPath: stagedPath,
		OldPath:    d.path + preRestoreSuffix,
		Phase:      restorePhaseStaged,
		StartedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeJournal(journalPath, j); err != nil {
		_ = os.Remove(stagedPath)
		return RestoreStaging{}, err
	}

	return RestoreStaging{
		livePath:       d.path,
		stagedPath:     stagedPath,
		journalPath:    journalPath,
		oldPath:        j.OldPath,
		digest:         digest,
		size:           size,
		schemaVersions: actual,
	}, nil
}

// stageImageCopy copies sourcePath into a fresh file in the same directory
// as livePath -- guaranteeing the later rename into place is same-filesystem
// and therefore atomic -- hashing the bytes in the same streaming pass so a
// large image is never held in memory. It fsyncs the staged file before
// returning: PrepareRestore's caller-visible success means the bytes are
// durable, not merely written.
func stageImageCopy(livePath, sourcePath string) (path string, digest contract.Digest, size int64, err error) {
	src, err := os.Open(sourcePath)
	if err != nil {
		return "", "", 0, fmt.Errorf("storage: open restore source %s: %w", sourcePath, err)
	}
	defer func() { _ = src.Close() }()

	dir := filepath.Dir(livePath)
	dst, err := os.CreateTemp(dir, filepath.Base(livePath)+".restore-staged-*")
	if err != nil {
		return "", "", 0, fmt.Errorf("storage: create restore staging file: %w", err)
	}
	stagedPath := dst.Name()
	cleanup := func() {
		_ = dst.Close()
		_ = os.Remove(stagedPath)
	}

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), src)
	if err != nil {
		cleanup()
		return "", "", 0, fmt.Errorf("storage: stage restore image: %w", err)
	}
	if err := dst.Sync(); err != nil {
		cleanup()
		return "", "", 0, fmt.Errorf("storage: sync restore staging file: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(stagedPath)
		return "", "", 0, fmt.Errorf("storage: close restore staging file: %w", err)
	}
	return stagedPath, contract.Digest(hex.EncodeToString(h.Sum(nil))), n, nil
}

// inspectStagedSchema opens the staged image read-only -- never writing to
// or creating WAL/SHM siblings for a file PrepareRestore does not yet own --
// runs an integrity check, and reads its applied migrations. Any failure
// (not a database, corrupt, or missing the storage migration ledger) is
// reported as a conflict: the staged bytes are not usable as a restore
// source, refused before replacement rather than after.
func inspectStagedSchema(ctx context.Context, path string) ([]SchemaVersion, error) {
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return nil, storageFault("open staged restore image", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.PingContext(ctx); err != nil {
		return nil, conflictFault("staged restore image is not a readable SQLite database")
	}
	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		return nil, conflictFault("staged restore image failed integrity check")
	}
	versions, err := querySchemaVersions(ctx, db)
	if err != nil {
		return nil, conflictFault("staged restore image is missing the storage migration ledger")
	}
	return versions, nil
}

// CommitRestore atomically replaces the live database with staging under
// exclusive ownership. It checkpoints the live database's WAL (merging every
// committed page into the main file, so no -wal/-shm sidecar can ever bind
// to a different database file after the swap -- the mixed-WAL failure this
// method exists to prevent), closes every connection to it, then performs
// the durable rename sequence, reopens with a fresh generation, and leaves
// the reopened database paused. The receiver is closed as part of this
// call, superseded by the returned Restorable.
func (d *database) CommitRestore(ctx context.Context, staging RestoreStaging) (Restorable, error) {
	if d.closed.Load() {
		return nil, closedFault()
	}
	if staging.livePath != d.path {
		return nil, invalidInputFault("restore staging was prepared for %q, not the open database %q", staging.livePath, d.path)
	}

	d.wmu.Lock()
	defer d.wmu.Unlock()

	j, ok, err := readJournal(staging.journalPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, invalidInputFault("restore staging journal %q is missing; PrepareRestore must run again", staging.journalPath)
	}

	if err := d.checkpointAndClose(); err != nil {
		return nil, err
	}
	// The connection pool is gone regardless of what happens next: this
	// instance cannot serve another call either way.
	d.closed.Store(true)

	if err := completeStagedSwap(j, staging.journalPath); err != nil {
		return nil, err
	}
	if err := os.Remove(staging.journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, storageFault("remove completed restore journal", err)
	}

	next, err := openAt(ctx, d.path, d.busy)
	if err != nil {
		return nil, err
	}
	if _, err := next.StartGeneration(ctx); err != nil {
		_ = next.Close()
		return nil, err
	}
	if err := next.setRestorePaused(ctx, true); err != nil {
		_ = next.Close()
		return nil, err
	}
	return next, nil
}

// checkpointAndClose merges every committed WAL page into the live
// database's main file and closes the connection pool, then removes the now
// -- unneeded -wal/-shm sidecars so the impending rename can never leave
// them bound to a different database file underneath the live path.
func (d *database) checkpointAndClose() error {
	if _, err := d.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return storageFault("checkpoint database before restore", err)
	}
	if err := d.db.Close(); err != nil {
		return storageFault("close database before restore", err)
	}
	_ = os.Remove(d.path + "-wal")
	_ = os.Remove(d.path + "-shm")
	return nil
}

// setRestorePaused persists the post-restore pause state in the same short
// transaction shape as StartGeneration, and mirrors it into the in-process
// flag Write/WriteRestoreOverlay check.
func (d *database) setRestorePaused(ctx context.Context, paused bool) error {
	if d.closed.Load() {
		return closedFault()
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()

	conn, err := d.db.Conn(ctx)
	if err != nil {
		return storageFault("acquire connection", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return txFault("begin restore-pause transaction", err)
	}
	val := 0
	if paused {
		val = 1
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO storage_restore (id, paused) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET paused = excluded.paused`, val); err != nil {
		_ = d.finishTxn(conn, false)
		return storageFault("record restore pause state", err)
	}
	if err := d.finishTxn(conn, true); err != nil {
		_ = d.finishTxn(conn, false)
		return txFault("commit restore-pause state", err)
	}
	d.restoring.Store(paused)
	return nil
}

// loadRestorePaused reads the persisted storage_restore row. A missing row
// means never restored on this file: not paused.
func loadRestorePaused(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (bool, error) {
	var paused int
	err := q.QueryRowContext(ctx, "SELECT paused FROM storage_restore WHERE id = 1").Scan(&paused)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return paused != 0, nil
}

// RestorePaused reports the in-process pause flag, kept in sync with the
// persisted storage_restore row by setRestorePaused and by bootstrap on
// every Open.
func (d *database) RestorePaused(ctx context.Context) (bool, error) {
	if d.closed.Load() {
		return false, closedFault()
	}
	return d.restoring.Load(), nil
}

// ResumeAfterRestore clears the post-restore pause. The caller must have
// completed the owner overlay merge and its own verification first; storage
// enforces only that the pause lifts explicitly.
func (d *database) ResumeAfterRestore(ctx context.Context) error {
	if d.closed.Load() {
		return closedFault()
	}
	if !d.restoring.Load() {
		return invalidInputFault("database is not currently paused for restore")
	}
	return d.setRestorePaused(ctx, false)
}

// WriteRestoreOverlay runs fn inside one short write transaction, exactly
// like Write, but only while RestorePaused is true.
func (d *database) WriteRestoreOverlay(ctx context.Context, actor contract.Actor, scope contract.Scope, fn func(contract.Unit) error) error {
	if err := d.entryCheck(); err != nil {
		return err
	}
	if !d.restoring.Load() {
		return invalidInputFault("database is not paused for restore; WriteRestoreOverlay is only usable between CommitRestore and ResumeAfterRestore")
	}
	return d.writeLocked(ctx, actor, scope, fn)
}

// fileExists reports whether path names an existing file, distinguishing a
// genuine stat failure from absence.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, storageFault("stat "+path, err)
}

// completeStagedSwap drives an authorized restore from j's current phase
// through to "swapped": renaming the live file aside (if not already done)
// and renaming the staged file into its place (if not already done),
// persisting the phase transition durably before each step. It is
// idempotent, so CommitRestore and a later recovery pass both call it
// safely after a crash at either rename.
func completeStagedSwap(j restoreJournal, journalPath string) error {
	if j.Phase == restorePhaseStaged {
		liveExists, err := fileExists(j.LivePath)
		if err != nil {
			return err
		}
		if liveExists {
			if err := os.Rename(j.LivePath, j.OldPath); err != nil {
				return storageFault("move live database aside for restore", err)
			}
		} else {
			oldExists, err := fileExists(j.OldPath)
			if err != nil {
				return err
			}
			if !oldExists {
				return storageFault("resume restore", fmt.Errorf("neither live database %q nor its pre-restore copy %q exist", j.LivePath, j.OldPath))
			}
			// Already moved aside by an earlier, interrupted attempt.
		}
		j.Phase = restorePhaseOldMoved
		if err := writeJournal(journalPath, j); err != nil {
			return err
		}
	}
	if j.Phase == restorePhaseOldMoved {
		stagedExists, err := fileExists(j.StagedPath)
		if err != nil {
			return err
		}
		if stagedExists {
			if err := os.Rename(j.StagedPath, j.LivePath); err != nil {
				return storageFault("swap staged database into place", err)
			}
		} else {
			liveExists, err := fileExists(j.LivePath)
			if err != nil {
				return err
			}
			if !liveExists {
				return storageFault("resume restore", fmt.Errorf("neither staged image %q nor live database %q exist", j.StagedPath, j.LivePath))
			}
			// Already swapped into place by an earlier, interrupted attempt.
		}
		j.Phase = restorePhaseSwapped
		if err := writeJournal(journalPath, j); err != nil {
			return err
		}
	}
	return nil
}

// abandonPreparedRestore removes a "staged"-phase journal and its staged
// image. No destructive action was ever taken at that phase, so the live
// database is untouched and needs no repair -- it is simply left in place.
func abandonPreparedRestore(j restoreJournal, journalPath string) error {
	if err := os.Remove(j.StagedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return storageFault("remove abandoned restore staging file", err)
	}
	if err := os.Remove(journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return storageFault("remove abandoned restore journal", err)
	}
	return nil
}

// recoverInterruptedRestore runs at the start of every openAt, before the
// path is opened any other way. A journal in phase "staged" means
// CommitRestore's destructive action was never authorized to completion (or
// never began): the restore is abandoned and the live database is left
// exactly as it was. A journal in phase "old_moved" or "swapped" means
// CommitRestore's authorization already took effect: the swap completes
// forward, never reverts, so an explicitly requested restore is never
// silently discarded by an unrelated process restart. Either way, this
// yields a recoverable database -- the old one or the new one -- and never
// a live path bound to a foreign -wal/-shm pair.
func recoverInterruptedRestore(livePath string) error {
	journalPath := livePath + restoreJournalSuffix
	j, ok, err := readJournal(journalPath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	switch j.Phase {
	case restorePhaseStaged:
		return abandonPreparedRestore(j, journalPath)
	case restorePhaseOldMoved, restorePhaseSwapped:
		if err := completeStagedSwap(j, journalPath); err != nil {
			return err
		}
		if err := os.Remove(journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return storageFault("remove recovered restore journal", err)
		}
		return nil
	default:
		return storageFault("recover restore", fmt.Errorf("unknown restore journal phase %q", j.Phase))
	}
}

// writeJournal marshals j to path and fsyncs it before returning, so a
// caller-visible success means the phase transition is durable, not merely
// buffered.
func writeJournal(path string, j restoreJournal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return storageFault("encode restore journal", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return storageFault("write restore journal", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return storageFault("reopen restore journal for sync", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return storageFault("sync restore journal", err)
	}
	return nil
}

// readJournal reads and decodes the journal at path. A missing file is not
// an error: ok is false and the caller proceeds normally.
func readJournal(path string) (restoreJournal, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return restoreJournal{}, false, nil
	}
	if err != nil {
		return restoreJournal{}, false, storageFault("read restore journal", err)
	}
	var j restoreJournal
	if err := json.Unmarshal(data, &j); err != nil {
		return restoreJournal{}, false, storageFault("decode restore journal", err)
	}
	return j, true, nil
}
