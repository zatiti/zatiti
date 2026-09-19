package installation

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

func TestBackupRequiresInstallationPaused(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.bindBackup(backupOnly{db: e.db})
	_, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err == nil {
		t.Fatalf("expected backup to be refused while the installation is running")
	}
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("error = %v, want prerequisite_missing", err)
	}
}

// Without the capability the operation does every other real piece of the
// pipeline -- pause required, a durable job created and shadowed -- then
// fails at the exact missing prerequisite by name, custodies no key it
// could never use, and never fabricates a digest.
func TestBackupWithoutCapabilityFailsPrerequisiteMissing(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	payload, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(backup): %v", err)
	}
	if payload.Status != contract.StatusFailed || payload.Error == nil || payload.Error.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("backup = %s %v, want failed/prerequisite_missing", payload.Status, payload.Error)
	}
	if !strings.Contains(payload.Error.Message, "WithDatabaseBackup") {
		t.Fatalf("fault does not name the capability: %s", payload.Error.Message)
	}
	if ref := e.backupKeyRef(); ref != "" || len(e.secrets.m) != 1 {
		t.Fatalf("a backup that cannot run must not custody a key: ref %q, %d secrets (the owner credential only)", ref, len(e.secrets.m))
	}
	if len(e.blobs.published) != 0 || len(e.blobs.staged) != 0 {
		t.Fatalf("nothing may be staged or published without the capability: %d staged, %d published", len(e.blobs.staged), len(e.blobs.published))
	}
	if len(e.ports.callsOf(peerExecutionJobCreate)) != 1 || len(e.ports.callsOf(peerExecutionJobRecord)) != 1 {
		t.Fatalf("backup must create exactly one durable job and record its disposition")
	}
	if state := e.jobState("backup"); state != "failed" {
		t.Fatalf("backup job state = %q, want failed", state)
	}
}

// TestBackupProducesVerifiedBundleWhoseImageRestoresToAWorkingDatabase: with
// the capability bound, backup streams one consistent image through the
// hashing writer, seals it with the manifest, publishes and verifies the
// bundle, and the job result names the artifact. The proof is the bytes:
// the framed image, whose digest and size the manifest records, opens as a
// working SQLite database holding this installation's state.
func TestBackupProducesVerifiedBundleWhoseImageRestoresToAWorkingDatabase(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	e.bindBackup(backupOnly{db: e.db})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	payload, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(backup): %v", err)
	}
	if payload.Status != contract.StatusAccepted {
		t.Fatalf("backup status = %q (%v), want accepted", payload.Status, payload.Error)
	}
	var out resourceOut[wireJob]
	e.decode(payload.Data, &out)
	if out.Resource.State != "succeeded" || out.Resource.Kind != "backup" {
		t.Fatalf("backup job = %+v, want succeeded backup", out.Resource)
	}
	var result resourceOut[wireBackup]
	e.decode(out.Resource.Result, &result)
	backup := result.Resource
	if !backup.Verified || backup.InstallationID != e.install || backup.Artifact.ID == "" || backup.Artifact.Digest == "" {
		t.Fatalf("backup resource = %+v, want a verified artifact of this installation", backup)
	}
	// The key prerequisite is the store reference the sealing key resolves
	// under: recorded for reuse and carried by the artifact itself.
	if len(backup.KeyPrerequisites) != 1 || backup.KeyPrerequisites[0] != e.backupKeyRef() || !strings.HasPrefix(backup.KeyPrerequisites[0], fakeRefPrefix) {
		t.Fatalf("key prerequisites = %v, recorded reference %q", backup.KeyPrerequisites, e.backupKeyRef())
	}
	publishes := e.ports.callsOf(peerArtifactsPublish)
	if len(publishes) != 1 || !strings.Contains(string(publishes[0].Input), string(backup.Artifact.Digest)) {
		t.Fatalf("backup must publish exactly the bundle's metadata: %d calls", len(publishes))
	}

	// The published bundle decrypts under the custodied key and frames the
	// manifest and the image the manifest describes.
	sealed, ok := e.blobs.published[backup.Artifact.Digest]
	if !ok {
		t.Fatalf("bundle %s was not published to the blob store", backup.Artifact.Digest)
	}
	ref, key, frame := e.openArtifact(sealed)
	if ref != backup.KeyPrerequisites[0] {
		t.Fatalf("artifact header names key reference %q, the backup names %q", ref, backup.KeyPrerequisites[0])
	}
	manifest, image, err := openBackupBundle(key, frame, e.install)
	if err != nil {
		t.Fatalf("published bundle does not open: %v", err)
	}
	if manifest.BackupID != out.Resource.ID || manifest.Generation < 1 || !manifest.Paused || manifest.DatabaseArchiveEntry != databaseArchiveEntry {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.DatabaseDigest != digestOf(image) || manifest.DatabaseSize != int64(len(image)) {
		t.Fatalf("manifest digest/size %s/%d do not describe the framed image %s/%d", manifest.DatabaseDigest, manifest.DatabaseSize, digestOf(image), len(image))
	}

	// The image is a working database: open it as storage does and read
	// this installation's state back out of it.
	path := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	restored, err := storage.Open(e.ctx, storage.Config{Path: path})
	if err != nil {
		t.Fatalf("the backup image does not open as a database: %v", err)
	}
	defer func() { _ = restored.Close() }()
	var st *stateRow
	if err := restored.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		st, err = loadState(e.ctx, unit)
		return err
	}); err != nil {
		t.Fatalf("read state from the restored database: %v", err)
	}
	if st == nil || st.ID != e.install || !st.Paused {
		t.Fatalf("restored database state = %+v, want installation %s paused", st, e.install)
	}
	if state := e.jobState("backup"); state != "succeeded" {
		t.Fatalf("backup job state = %q, want succeeded", state)
	}
}

// failingBackup writes part of an image and then fails, like a database
// that becomes unavailable mid-copy.
type failingBackup struct{ err error }

func (f failingBackup) Backup(_ context.Context, w io.Writer) error {
	if _, err := w.Write([]byte("partial image bytes")); err != nil {
		return err
	}
	return f.err
}

// TestBackupFailureMidStreamLeavesNoCompletedManifest: a capability that
// fails after streaming some bytes yields a failed job naming the failure,
// nothing staged or published, and no artifact registered.
func TestBackupFailureMidStreamLeavesNoCompletedManifest(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	e.bindBackup(failingBackup{err: errors.New("database went away")})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	payload, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(backup): %v", err)
	}
	if payload.Status != contract.StatusFailed || payload.Error == nil || !strings.Contains(payload.Error.Message, "database went away") {
		t.Fatalf("backup = %s %v, want failed naming the stream failure", payload.Status, payload.Error)
	}
	if len(e.blobs.published) != 0 || len(e.blobs.staged) != 0 {
		t.Fatalf("a failed stream must publish nothing: %d staged, %d published", len(e.blobs.staged), len(e.blobs.published))
	}
	if len(e.ports.callsOf(peerArtifactsPublish)) != 0 {
		t.Fatal("a failed backup must not register an artifact")
	}
	if state := e.jobState("backup"); state != "failed" {
		t.Fatalf("backup job state = %q, want failed", state)
	}
}

func TestBackupWithoutSecretStoreFailsAtPerform(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	svc, err := New(contract.Dependencies{Clock: e.clock, IDs: e.ids, Ports: e.ports, Blobs: e.blobs}, WithDatabaseBackup(backupOnly{db: e.db}))
	if err != nil {
		t.Fatalf("New without secrets: %v", err)
	}
	e.svc = svc

	payload, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(backup): %v", err)
	}
	if payload.Status != contract.StatusFailed || payload.Error == nil || payload.Error.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("backup without a secret store: status=%q error=%v, want failed/prerequisite_missing", payload.Status, payload.Error)
	}
}

// TestDoctorReportsMissingBackupCapability: doctor names the unbound
// capability and stops naming it once assembly binds one.
func TestDoctorReportsMissingBackupCapability(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	names := func() bool {
		payload := mustQueryOK(t, e, opDoctor, scopeInput{Scope: e.scope})
		var out resourceOut[wireStatus]
		e.decode(payload.Data, &out)
		for _, r := range out.Resource.Requirements {
			if strings.Contains(r.Message, "database backup capability") {
				return true
			}
		}
		return false
	}
	if !names() {
		t.Fatal("doctor does not report the unbound backup capability")
	}
	e.bindBackup(backupOnly{db: e.db})
	if names() {
		t.Fatal("doctor still reports a bound backup capability as missing")
	}
}

// jobState reads the local shadow of the one job of kind.
func (e *testEnv) jobState(kind string) string {
	e.t.Helper()
	var state string
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		return unit.QueryRowContext(e.ctx, `SELECT state FROM installation_jobs WHERE kind = ?`, kind).Scan(&state)
	}); err != nil {
		e.t.Fatalf("read %s job: %v", kind, err)
	}
	return state
}

// TestBackupRefusesWithoutStartedGeneration: a manifest pins the controller
// generation; a generation that was never started is refused by name
// rather than written as zero.
func TestBackupRefusesWithoutStartedGeneration(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.bindBackup(backupOnly{db: e.db})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})
	payload, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(backup): %v", err)
	}
	if payload.Status != contract.StatusFailed || payload.Error == nil || !strings.Contains(payload.Error.Message, "generation") {
		t.Fatalf("backup = %s %v, want failed naming the generation", payload.Status, payload.Error)
	}
	if len(e.blobs.published) != 0 {
		t.Fatal("nothing may be published without a generation")
	}
}

// TestBackupKeySurvivesByReference: the secret store resolves keys only by
// the reference Put returned, never by name. A second backup after a
// service restart reuses the recorded key; a bundle sealed before key
// references were custodied is refused by name; and a bundle whose
// reference no longer resolves is refused rather than decrypted with a
// fresh, wrong key.
func TestBackupKeySurvivesByReference(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	e.bindBackup(backupOnly{db: e.db})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	first := e.backupNow("first")
	// A restart: a fresh Service over the same database and secret store.
	e.bindBackup(backupOnly{db: e.db})
	second := e.backupNow("second")
	if first.KeyPrerequisites[0] != second.KeyPrerequisites[0] {
		t.Fatalf("second backup minted a new key %q instead of reusing %q", second.KeyPrerequisites[0], first.KeyPrerequisites[0])
	}
	if got := len(e.secrets.m); got != 2 {
		t.Fatalf("%d secrets custodied, want the owner credential and one backup key", got)
	}
	for _, b := range []wireBackup{first, second} {
		ref, _, _ := e.openArtifact(e.blobs.published[b.Artifact.Digest])
		if ref != b.KeyPrerequisites[0] {
			t.Fatalf("bundle header reference %q differs from the backup's %q", ref, b.KeyPrerequisites[0])
		}
	}

	// A pre-reference bundle (no header) is refused by name.
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 2})
	_, key, frame := e.openArtifact(e.blobs.published[first.Artifact.Digest])
	_ = key
	legacy := wireArtifactRef{ID: e.ids.New(), Digest: e.blobs.publishBytes(frame)}
	setArtifactMetadata(e, legacy, int64(len(frame)), "available")
	payload, err := e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: legacy, ExpectedVersion: 3}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(restore legacy): %v", err)
	}
	if payload.Status != contract.StatusFailed || payload.Error == nil || payload.Error.Code != contract.CodePrerequisiteMissing ||
		!strings.Contains(payload.Error.Message, "key reference") {
		t.Fatalf("legacy bundle restore = %s %v, want prerequisite_missing naming the missing key reference", payload.Status, payload.Error)
	}

	// A bundle whose reference no longer resolves is refused, not re-keyed.
	if err := e.secrets.Delete(e.ctx, first.KeyPrerequisites[0]); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	setArtifactMetadata(e, first.Artifact, int64(len(e.blobs.published[first.Artifact.Digest])), "available")
	payload, err = e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: first.Artifact, ExpectedVersion: 3}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(restore lost key): %v", err)
	}
	if payload.Status != contract.StatusFailed || payload.Error == nil || payload.Error.Code != contract.CodePrerequisiteMissing ||
		!strings.Contains(payload.Error.Message, "does not resolve") {
		t.Fatalf("lost-key restore = %s %v, want prerequisite_missing naming the unresolvable reference", payload.Status, payload.Error)
	}
}

// backupNow runs one accepted backup and returns its Backup resource.
func (e *testEnv) backupNow(label string) wireBackup {
	e.t.Helper()
	payload, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err != nil || payload.Status != contract.StatusAccepted {
		e.t.Fatalf("%s backup: %v (%s %v)", label, err, payload.Status, payload.Error)
	}
	var job resourceOut[wireJob]
	e.decode(payload.Data, &job)
	var result resourceOut[wireBackup]
	e.decode(job.Resource.Result, &result)
	return result.Resource
}
