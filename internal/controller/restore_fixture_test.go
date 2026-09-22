package controller

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Restore fixture support: a locally built, self-contained candidate
// database image (built the same way installation.restore's own decrypted
// candidate would be, minus the encryption this package must never
// duplicate) plus a fake RestoreLifecycle that hands it to the controller
// exactly as entrypoint assembly's real, installation-backed implementation
// would, and merges a caller-supplied set of "owner" rows into the
// post-swap paused database, standing in for real owner merge methods this
// package cannot import.

// restoreBackupImage is one locally staged, self-contained candidate
// database file plus the exact claims storage.Restorable.PrepareRestore
// validates it against.
type restoreBackupImage struct {
	path           string
	installationID contract.ID
	digest         contract.Digest
	schemaVersions []storage.SchemaVersion
}

// buildBackupImage opens a fresh, throwaway sqlite database with this
// fixture's exact migrations, seeds marker_value with want, checkpoints its
// WAL into the main file (a staged candidate must be a single self-
// contained file, exactly what a real decrypted backup image is) and
// returns everything PrepareRestore needs to accept it.
func (f *fx) buildBackupImage(want string) restoreBackupImage {
	f.t.Helper()
	ctx := context.Background()
	dir := f.t.TempDir()
	path := filepath.Join(dir, "backup.db")
	db, err := storage.Open(ctx, storage.Config{Path: path})
	if err != nil {
		f.t.Fatalf("storage.Open backup image: %v", err)
	}
	if err := db.Migrate(ctx, fixtureMigrations()); err != nil {
		f.t.Fatalf("migrate backup image: %v", err)
	}
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	scope := f.scope()
	if err := db.Write(ctx, actor, scope, func(u contract.Unit) error {
		if _, err := u.ExecContext(ctx, `INSERT INTO marker_value (id, value) VALUES (1, ?)`, want); err != nil {
			return err
		}
		// The backup image is a real, earlier snapshot of THIS installation:
		// it carries the same seeded principal and installation-initialized
		// event start() itself requires, exactly as a genuine backup taken
		// before the swap would.
		if _, err := u.ExecContext(ctx, `INSERT INTO identity_principals (id, kind, revoked) VALUES (?, 'service', 0)`,
			string(f.actor.PrincipalID)); err != nil {
			return err
		}
		return u.Emit(ctx, contract.Event{Kind: "installation.installation.initialized", ResourceID: f.install, ResourceVersion: 1})
	}); err != nil {
		f.t.Fatalf("seed backup image: %v", err)
	}
	restorable, ok := db.(storage.Restorable)
	if !ok {
		f.t.Fatalf("storage.Open backup image does not implement storage.Restorable")
	}
	versions, err := restorable.SchemaVersions(ctx)
	if err != nil {
		f.t.Fatalf("SchemaVersions backup image: %v", err)
	}
	if err := db.Close(); err != nil {
		f.t.Fatalf("close backup image before checkpoint: %v", err)
	}
	// Checkpoint the WAL into the main file directly: a staged candidate
	// PrepareRestore reads is a single copied file, with no sidecar
	// contract.Database itself relies on -wal/-shm being open alongside it.
	checkpointSQLiteFile(f.t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatalf("read backup image: %v", err)
	}
	sum := sha256.Sum256(data)
	return restoreBackupImage{
		path: path, installationID: f.install,
		digest: contract.Digest(hex.EncodeToString(sum[:])), schemaVersions: versions,
	}
}

// checkpointSQLiteFile opens path directly (the "sqlite" driver is already
// registered process-wide by internal/storage's own import) and merges every
// committed WAL page into the main file, then removes the now-unneeded
// sidecars -- the same discipline storage's own checkpointAndClose applies
// before a restore's live-side swap, applied here to the fixture's synthetic
// candidate so it is a portable single file.
func checkpointSQLiteFile(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite file for checkpoint: %v", err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint sqlite file: %v", err)
	}
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}

// restoreJob commits one execution_jobs row exactly as installation.restore's
// own synchronous Prepare/Perform/Finish leaves it: created, then
// transitioned straight to "running" (with claimed_generation already set,
// as handleJobRecord's unclaimed-record path does) under this fixture's
// current generation, naming a synthetic verified backup reference in its
// original input. The controller's own restore handoff never reads that
// input back (StageCandidate resolves everything from the job id alone),
// so its exact shape only needs to satisfy _execution.job.claim's schema.
func (f *fx) restoreJob(t *testing.T) contract.ID {
	t.Helper()
	id := contract.NewID()
	input, err := json.Marshal(map[string]any{
		"scope":            f.scope(),
		"backup_artifact":  map[string]any{"id": string(contract.NewID()), "digest": stagedDigest},
		"expected_version": 1,
	})
	if err != nil {
		t.Fatalf("encode restore job input: %v", err)
	}
	f.exec(`INSERT INTO execution_jobs (id, version, state, owner, operation, operation_id, input, claimed_generation)
		VALUES (?, 1, 'running', ?, ?, '', ?, ?)`,
		string(id), restoreOwner, restoreOperation, string(input), f.generation())
	return id
}

// fakeRestoreLifecycle stands in for entrypoint assembly's real,
// installation-backed RestoreLifecycle: StageCandidate copies a fixture-
// registered restoreBackupImage into the controller's own staging
// directory (proving the controller cleans up what it was handed, never
// what StageCandidate itself retains), and MergeOverlay runs a fixture-
// registered per-job merge closure standing in for real owner merge
// methods this package cannot import.
type fakeRestoreLifecycle struct {
	f *fx
}

func (l fakeRestoreLifecycle) StageCandidate(_ context.Context, jobID contract.ID, dir string) (RestoreCandidate, error) {
	l.f.mu.Lock()
	img, ok := l.f.restoreBackups[jobID]
	l.f.mu.Unlock()
	if !ok {
		return RestoreCandidate{}, fxFault(contract.CodeNotFound, "no backup image registered for this restore job")
	}
	data, err := os.ReadFile(img.path)
	if err != nil {
		return RestoreCandidate{}, err
	}
	dst := filepath.Join(dir, "candidate-"+string(jobID)+".db")
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return RestoreCandidate{}, err
	}
	return RestoreCandidate{
		Path: dst, InstallationID: img.installationID, DatabaseDigest: img.digest, SchemaVersions: img.schemaVersions,
	}, nil
}

func (l fakeRestoreLifecycle) MergeOverlay(ctx context.Context, u contract.Unit, jobID contract.ID) error {
	l.f.mu.Lock()
	merge := l.f.restoreMerge[jobID]
	l.f.mu.Unlock()
	if merge == nil {
		return nil
	}
	return merge(ctx, u)
}
