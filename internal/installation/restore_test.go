package installation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// fixtureImage is the database image the synthetic fixture frames: any
// bytes serve restore's own decrypt/verify path, which checks the framed
// image against the manifest's digest and size, not that it is SQLite.
var fixtureImage = []byte("fixture database image bytes")

// sealFixtureBackup builds and publishes a correctly framed and sealed
// backup bundle for env e's installation, as installation.backup produces
// one, with mutate applied to the manifest before sealing.
func sealFixtureBackup(t *testing.T, e *testEnv, mutate func(*backupManifestDoc)) (wireArtifactRef, int64) {
	t.Helper()
	key, keyRef, _, err := resolveBackupKey(e.ctx, e.secrets, e.install, e.backupKeyRef())
	if err != nil {
		t.Fatalf("resolveBackupKey: %v", err)
	}
	manifest := fixtureManifest(e.install)
	if mutate != nil {
		mutate(&manifest)
	}
	plaintext, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	sealed, err := sealBundle(key, encodeFrame(plaintext, fixtureImage))
	if err != nil {
		t.Fatalf("sealBundle: %v", err)
	}
	artifact := encodeArtifact(keyRef, sealed)
	digest := e.blobs.publishBytes(artifact)
	return wireArtifactRef{ID: e.ids.New(), Digest: digest}, int64(len(artifact))
}

func fixtureManifest(installation contract.ID) backupManifestDoc {
	return backupManifestDoc{
		Schema: backupManifestSchema, InstallationID: installation, BackupID: "00000000-0000-4000-8000-000000009999",
		Generation: 1, CreatedAt: "2026-09-12T00:00:00Z",
		DatabaseDigest: digestOf(fixtureImage), DatabaseSize: int64(len(fixtureImage)), DatabaseArchiveEntry: databaseArchiveEntry,
		DatabaseSchemaVersions: []manifestSchemaVersion{}, Artifacts: []manifestArtifactEntry{}, Brains: []manifestBrainEntry{},
		RetainedObligations: []manifestObligation{}, KeyPrerequisites: []string{"fixture"},
		SourceRevision: "fixture", ControllerVersion: "fixture", RequiredProtocolProfiles: []string{}, Paused: true,
	}
}

func setArtifactMetadata(e *testEnv, ref wireArtifactRef, size int64, state string) {
	e.ports.set(peerArtifactsMetadata, func(inv contract.Invocation) (contract.Payload, error) {
		var in artifactsMetadataInput
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		return okPayload(artifactsMetadataOutput{Artifacts: []peerArtifact{{
			ID: ref.ID, Version: 1, Scope: in.Scope, Digest: ref.Digest, Size: size,
			MediaType: "application/octet-stream", Classification: "restricted", Encrypted: true,
			State: state, CreatedAt: "2026-09-12T00:00:00Z",
		}}})
	})
}

func TestRestoreRequiresExclusiveMaintenance(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	ref, size := sealFixtureBackup(t, e, nil)
	setArtifactMetadata(e, ref, size, "available")

	_, err := e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: ref, ExpectedVersion: 1}, true)
	if err == nil {
		t.Fatalf("expected restore to be refused outside maintenance mode")
	}
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("error = %v, want prerequisite_missing", err)
	}
}

func TestRestoreRejectsUnknownArtifact(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	ref := wireArtifactRef{ID: e.ids.New(), Digest: contract.Digest("0000000000000000000000000000000000000000000000000000000000000000"[:64])}
	_, err := e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: ref, ExpectedVersion: 2}, true)
	if err == nil {
		t.Fatalf("expected an unknown backup artifact to be refused")
	}
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodeNotFound {
		t.Fatalf("error = %v, want not_found", err)
	}
}

// Z14.paused_restore without the capability: a verified encrypted backup
// for THIS installation passes binding and integrity verification under
// exclusive maintenance, the obligations toward the recovery overlay are
// preserved durably, and the operation fails at the named missing
// capability rather than claiming anything was restored.
func TestRestoreVerifiesArtifactThenFailsWithoutBackupCapability(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	ref, size := sealFixtureBackup(t, e, nil)
	setArtifactMetadata(e, ref, size, "available")

	pendingOpID := e.ids.New()
	e.ports.set(peerEffectsPending, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(effectsPendingOutput{Operations: []peerOperation{
			{ID: pendingOpID, Version: 1, Action: json.RawMessage(`{}`), ActionDigest: "abc", State: "outcome_unknown", AttemptIDs: []contract.ID{}},
		}})
	})

	payload, err := e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: ref, ExpectedVersion: 2}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(restore): %v", err)
	}
	if payload.Status != contract.StatusFailed {
		t.Fatalf("restore status = %q, want failed", payload.Status)
	}
	if payload.Error == nil || payload.Error.Code != contract.CodePrerequisiteMissing || !strings.Contains(payload.Error.Message, "WithDatabaseBackup") {
		t.Fatalf("restore error = %v, want prerequisite_missing naming the capability", payload.Error)
	}

	// Preserve both backup and later durable obligations across rewind: the
	// unresolved effects and memory manifest this installation currently
	// carries were gathered before the operation failed and persisted into
	// this package's own durable obligations table.
	if len(e.ports.callsOf(peerEffectsPending)) == 0 {
		t.Fatalf("restore must gather current pending effects toward its recovery overlay")
	}
	if len(e.ports.callsOf(peerMemoryManifest)) == 0 {
		t.Fatalf("restore must gather the current memory manifest toward its recovery overlay")
	}
	var obligationCount int64
	var obligationResource contract.ID
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		if err := unit.QueryRowContext(e.ctx,
			`SELECT COUNT(*) FROM installation_recovery_obligations WHERE installation_id = ?`,
			string(e.install)).Scan(&obligationCount); err != nil {
			return err
		}
		// The pending operation this test injects carries state
		// outcome_unknown, so it is classified kind unknown_effect, not the
		// generic claimed_effect an ordinary in-flight claim would get (see
		// snapshotObligations in restore.go).
		return unit.QueryRowContext(e.ctx,
			`SELECT resource_id FROM installation_recovery_obligations WHERE kind = 'unknown_effect'`).Scan(&obligationResource)
	}); err != nil {
		t.Fatalf("read recovery obligations: %v", err)
	}
	if obligationCount == 0 {
		t.Fatalf("expected the pending effect to be preserved as a durable recovery obligation")
	}
	if obligationResource != pendingOpID {
		t.Fatalf("preserved obligation resource_id = %s, want %s", obligationResource, pendingOpID)
	}

	// The installation must still show maintenance/paused: a failed restore
	// never silently resumes admissions.
	payloadStatus := mustQueryOK(t, e, opStatus, scopeInput{Scope: e.scope})
	var out resourceOut[wireStatus]
	e.decode(payloadStatus.Data, &out)
	if !out.Resource.Paused || !out.Resource.Maintenance {
		t.Fatalf("installation must remain paused/maintenance after a failed restore: %+v", out.Resource)
	}

	// job.get surfaces the preserved obligation even though the restore job
	// itself failed.
	var restoreJobID contract.ID
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		return unit.QueryRowContext(e.ctx, `SELECT id FROM installation_jobs WHERE kind = 'restore'`).Scan(&restoreJobID)
	}); err != nil {
		t.Fatalf("find restore job id: %v", err)
	}
	jobPayload := mustQueryOK(t, e, opJobGet, jobGetInput{Scope: e.scope, ID: restoreJobID})
	var jobOut resourceOut[wireJob]
	e.decode(jobPayload.Data, &jobOut)
	foundPreserved := false
	for _, r := range jobOut.Resource.Requirements {
		if r.Code == "recovery_obligation_preserved" {
			foundPreserved = true
		}
	}
	if !foundPreserved {
		t.Fatalf("expected job.get to surface a preserved recovery obligation, got %+v", jobOut.Resource.Requirements)
	}
}

// Z14.outbox_revocation_restore-flavored: a tampered/corrupt backup artifact
// is rejected at the integrity check, before this package ever considers it
// a candidate for rewinding anything.
func TestRestoreRejectsTamperedArtifact(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	ref, size := sealFixtureBackup(t, e, nil)
	setArtifactMetadata(e, ref, size, "available")

	// Corrupt the published bytes after sealing.
	e.blobs.mu.Lock()
	raw := e.blobs.published[ref.Digest]
	corrupted := append([]byte(nil), raw...)
	corrupted[len(corrupted)-1] ^= 0xFF
	e.blobs.published[ref.Digest] = corrupted
	e.blobs.mu.Unlock()

	payload, err := e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: ref, ExpectedVersion: 2}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(restore): %v", err)
	}
	if payload.Status != contract.StatusFailed || payload.Error == nil || payload.Error.Code != contract.CodeArtifactFault {
		t.Fatalf("tampered restore: status=%q error=%v, want failed/artifact_fault", payload.Status, payload.Error)
	}
}

func TestRestoreRejectsBackupFromAnotherInstallation(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	foreignInstall := e.ids.New()
	key, keyRef, _, err := resolveBackupKey(e.ctx, e.secrets, e.install, "")
	if err != nil {
		t.Fatalf("resolveBackupKey: %v", err)
	}
	manifest := fixtureManifest(foreignInstall)
	plaintext, _ := json.Marshal(manifest)
	sealed, err := sealBundle(key, encodeFrame(plaintext, fixtureImage))
	if err != nil {
		t.Fatalf("sealBundle: %v", err)
	}
	artifact := encodeArtifact(keyRef, sealed)
	digest := e.blobs.publishBytes(artifact)
	ref := wireArtifactRef{ID: e.ids.New(), Digest: digest}
	setArtifactMetadata(e, ref, int64(len(artifact)), "available")

	payload, err := e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: ref, ExpectedVersion: 2}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(restore): %v", err)
	}
	if payload.Status != contract.StatusFailed || payload.Error == nil || payload.Error.Code != contract.CodeInvalidInput {
		t.Fatalf("cross-installation restore: status=%q error=%v, want failed/invalid_input", payload.Status, payload.Error)
	}
}

func TestRestoreRecordUpdatesLocalJobBookkeeping(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	jobID := e.ids.New()
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		return insertJob(e.ctx, unit, jobRow{
			ID: jobID, Version: 1, InstallationID: e.install, Kind: "restore", State: "running",
			Requirements: []wireRequirement{}, CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now(),
		})
	}); err != nil {
		t.Fatalf("seed restore job: %v", err)
	}

	payload := e.mustOK(opRestoreRecordName, restoreRecordInput{
		JobID: jobID, State: "succeeded", Requirements: []wireRequirement{},
	})
	var out resourceOut[wireJob]
	e.decode(payload.Data, &out)
	if out.Resource.State != "succeeded" {
		t.Fatalf("recorded job state = %q, want succeeded", out.Resource.State)
	}

	_ = e.expectFault(opRestoreRecordName, restoreRecordInput{
		JobID: e.ids.New(), State: "succeeded", Requirements: []wireRequirement{},
	}, contract.CodeNotFound)
}

func TestRestoreExpectedVersionIsChecked(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})
	ref, size := sealFixtureBackup(t, e, nil)
	setArtifactMetadata(e, ref, size, "available")

	_, err := e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: ref, ExpectedVersion: 99}, true)
	if err == nil {
		t.Fatalf("expected a stale expected_version to be refused")
	}
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodeStaleVersion {
		t.Fatalf("error = %v, want stale_version", err)
	}
}

// Z14.paused_restore with the capability: the backup verifies, the
// encrypted recovery overlay of the CURRENT installation is published and
// verified (its source digest is the current database's consistent image,
// its obligations are the ones Prepare preserved), the job stays running
// and the installation stays paused in maintenance awaiting the
// controller's rewind.
func TestRestorePublishesRecoveryOverlayAndAwaitsTheController(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	capability := &recordingBackup{inner: backupOnly{db: e.db}}
	e.bindBackup(capability)
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	ref, size := sealFixtureBackup(t, e, nil)
	setArtifactMetadata(e, ref, size, "available")
	pendingOpID := e.ids.New()
	e.ports.set(peerEffectsPending, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(effectsPendingOutput{Operations: []peerOperation{
			{ID: pendingOpID, Version: 1, Action: json.RawMessage(`{}`), ActionDigest: "abc", State: "outcome_unknown", AttemptIDs: []contract.ID{}},
		}})
	})

	payload, err := e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: ref, ExpectedVersion: 2}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(restore): %v", err)
	}
	if payload.Status != contract.StatusAccepted {
		t.Fatalf("restore status = %q (%v), want accepted", payload.Status, payload.Error)
	}
	var out resourceOut[wireJob]
	e.decode(payload.Data, &out)
	if out.Resource.State != "running" {
		t.Fatalf("restore job state = %q, want running (paused, awaiting the controller)", out.Resource.State)
	}
	var overlayID contract.ID
	for _, r := range out.Resource.Requirements {
		if r.Code == "recovery_overlay_published" && r.ResourceID != nil {
			overlayID = *r.ResourceID
		}
	}
	if overlayID == "" {
		t.Fatalf("restore did not report the published recovery overlay: %+v", out.Resource.Requirements)
	}

	// Exactly one artifact was published: the overlay. It decrypts under the
	// backup key, is bound to this installation, names the current
	// database's consistent digest and carries the preserved obligation.
	publishes := e.ports.callsOf(peerArtifactsPublish)
	if len(publishes) != 1 {
		t.Fatalf("restore published %d artifacts, want the overlay only", len(publishes))
	}
	var published artifactsPublishInput
	if err := json.Unmarshal(publishes[0].Input, &published); err != nil {
		t.Fatalf("decode publish input: %v", err)
	}
	sealed, ok := e.blobs.published[published.Digest]
	if !ok || published.MediaType != overlayMediaType {
		t.Fatalf("overlay %s (%s) was not published to the blob store", published.Digest, published.MediaType)
	}
	_, overlayKey, overlayFrame := e.openArtifact(sealed)
	overlay, err := openRecoveryOverlay(overlayKey, overlayFrame, e.install)
	if err != nil {
		t.Fatalf("published overlay does not open: %v", err)
	}
	if current := capability.last(); current == "" || overlay.SourceDatabaseDigest != current {
		t.Fatalf("overlay source digest %s is not the image the capability streamed (%s)", overlay.SourceDatabaseDigest, current)
	}
	if len(overlay.Obligations) != 1 || overlay.Obligations[0].ResourceID != pendingOpID || overlay.Obligations[0].RecordDigest == "" {
		t.Fatalf("overlay obligations = %+v, want the pending effect with its record digest", overlay.Obligations)
	}

	status := mustQueryOK(t, e, opStatus, scopeInput{Scope: e.scope})
	var st resourceOut[wireStatus]
	e.decode(status.Data, &st)
	if !st.Resource.Paused || !st.Resource.Maintenance {
		t.Fatalf("installation must stay paused/maintenance while the restore awaits the controller: %+v", st.Resource)
	}
}

// TestRestoreOverlayPreservesPostBackupUnknownEffectAcrossRewind: required
// behavioral test "a post-backup revocation and unknown write remain
// effective after restore" (the unknown-write half of that requirement; a
// credential/grant revocation cannot be exercised here because no owner
// port installation may call enumerates current revocations -- see the P31
// handoff). An effect that went outcome_unknown after the backup was taken
// is captured into the monotonic recovery overlay, classified distinctly
// (kind unknown_effect, not the generic claimed_effect an ordinary
// in-flight claim gets), and durably preserved -- both in the published
// overlay artifact and in this package's own obligations table,
// independent of that artifact -- before any rewind occurs, so the merge
// after rewind has enough to know this write must survive it.
func TestRestoreOverlayPreservesPostBackupUnknownEffectAcrossRewind(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	capability := &recordingBackup{inner: backupOnly{db: e.db}}
	e.bindBackup(capability)
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	ref, size := sealFixtureBackup(t, e, nil)
	setArtifactMetadata(e, ref, size, "available")
	unknownOpID := e.ids.New()
	e.ports.set(peerEffectsPending, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(effectsPendingOutput{Operations: []peerOperation{
			{ID: unknownOpID, Version: 1, Action: json.RawMessage(`{}`), ActionDigest: "abc", State: "outcome_unknown", AttemptIDs: []contract.ID{}},
		}})
	})

	payload, err := e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: ref, ExpectedVersion: 2}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(restore): %v", err)
	}
	if payload.Status != contract.StatusAccepted {
		t.Fatalf("restore status = %q (%v), want accepted", payload.Status, payload.Error)
	}
	var out resourceOut[wireJob]
	e.decode(payload.Data, &out)
	var overlayID contract.ID
	for _, r := range out.Resource.Requirements {
		if r.Code == "recovery_overlay_published" && r.ResourceID != nil {
			overlayID = *r.ResourceID
		}
	}
	if overlayID == "" {
		t.Fatalf("restore did not report the published recovery overlay: %+v", out.Resource.Requirements)
	}

	publishes := e.ports.callsOf(peerArtifactsPublish)
	if len(publishes) == 0 {
		t.Fatalf("restore published no artifacts")
	}
	var published artifactsPublishInput
	if err := json.Unmarshal(publishes[len(publishes)-1].Input, &published); err != nil {
		t.Fatalf("decode publish input: %v", err)
	}
	sealed, ok := e.blobs.published[published.Digest]
	if !ok || published.MediaType != overlayMediaType {
		t.Fatalf("overlay %s (%s) was not published", published.Digest, published.MediaType)
	}
	_, overlayKey, overlayFrame := e.openArtifact(sealed)
	overlay, err := openRecoveryOverlay(overlayKey, overlayFrame, e.install)
	if err != nil {
		t.Fatalf("published overlay does not open: %v", err)
	}
	if len(overlay.Obligations) != 1 || overlay.Obligations[0].ResourceID != unknownOpID || overlay.Obligations[0].Kind != "unknown_effect" {
		t.Fatalf("overlay obligations = %+v, want the post-backup unknown write preserved as unknown_effect", overlay.Obligations)
	}

	// Durable independent of the overlay artifact: a merge performed after
	// the controller's rewind can read this table even if the encrypted
	// overlay bytes were never needed again.
	var kind string
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		return unit.QueryRowContext(e.ctx,
			`SELECT kind FROM installation_recovery_obligations WHERE resource_id = ?`, string(unknownOpID)).Scan(&kind)
	}); err != nil {
		t.Fatalf("read recovery obligation: %v", err)
	}
	if kind != "unknown_effect" {
		t.Fatalf("durable obligation kind = %q, want unknown_effect", kind)
	}
}

// TestBackupRestoredElsewhereReadsIdenticalJobHistory: required behavioral
// test "back up real artifact and domain state, restore elsewhere, read
// identical bytes and find the same accepted task/history." This package's
// own accepted/durable history is its job records. A backup's own image
// cannot contain proof of that same backup's success -- Perform streams the
// image before Finish ever commits the job succeeded -- so this backs up
// twice: the second backup's framed image, written to a path with no
// relation to the original database file and opened there as a fresh
// database, must read back the exact same accepted first backup job --
// same id, state, result -- that the original database recorded, not a
// copy that merely looks similar.
func TestBackupRestoredElsewhereReadsIdenticalJobHistory(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	e.bindBackup(backupOnly{db: e.db})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	first := e.backupNow("first")
	second := e.backupNow("second")

	var wantJob *jobRow
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		wantJob, err = loadJob(e.ctx, unit, first.ID)
		return err
	}); err != nil {
		t.Fatalf("read the first backup job from the original database: %v", err)
	}
	if wantJob == nil || wantJob.State != "succeeded" {
		t.Fatalf("original database's first backup job = %+v, want succeeded", wantJob)
	}

	sealed, ok := e.blobs.published[second.Artifact.Digest]
	if !ok {
		t.Fatalf("bundle %s was not published", second.Artifact.Digest)
	}
	_, key, frame := e.openArtifact(sealed)
	_, image, err := openBackupBundle(key, frame, e.install)
	if err != nil {
		t.Fatalf("open backup bundle: %v", err)
	}

	// "Elsewhere": a directory with no relation to the original database
	// file's own temp directory.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere", "restored.db")
	if err := os.MkdirAll(filepath.Dir(elsewhere), 0o700); err != nil {
		t.Fatalf("mkdir elsewhere: %v", err)
	}
	if err := os.WriteFile(elsewhere, image, 0o600); err != nil {
		t.Fatalf("write image elsewhere: %v", err)
	}
	restored, err := storage.Open(e.ctx, storage.Config{Path: elsewhere})
	if err != nil {
		t.Fatalf("the backup image does not open elsewhere: %v", err)
	}
	defer func() { _ = restored.Close() }()

	var gotJob *jobRow
	if err := restored.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		gotJob, err = loadJob(e.ctx, unit, first.ID)
		return err
	}); err != nil {
		t.Fatalf("read the first backup job from the database restored elsewhere: %v", err)
	}
	if gotJob == nil || gotJob.State != wantJob.State || gotJob.Kind != wantJob.Kind || string(gotJob.Result) != string(wantJob.Result) {
		t.Fatalf("job history restored elsewhere = %+v, want the identical original %+v", gotJob, wantJob)
	}
	var restoredResult resourceOut[wireBackup]
	if err := json.Unmarshal(gotJob.Result, &restoredResult); err != nil {
		t.Fatalf("decode restored job result: %v", err)
	}
	if restoredResult.Resource.Artifact.Digest != first.Artifact.Digest || restoredResult.Resource.ID != first.ID {
		t.Fatalf("job history restored elsewhere = %+v, want the same accepted first backup %+v", restoredResult.Resource, first)
	}
}

// TestBackupThenRestoreRoundTrip: a real backup's artifact is accepted by
// restore end to end within this package: the bundle backup sealed is the
// bundle restore verifies.
func TestBackupThenRestoreRoundTrip(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	e.bindBackup(backupOnly{db: e.db})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	payload, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err != nil || payload.Status != contract.StatusAccepted {
		t.Fatalf("backup: %v (%+v)", err, payload)
	}
	var job resourceOut[wireJob]
	e.decode(payload.Data, &job)
	var result resourceOut[wireBackup]
	e.decode(job.Resource.Result, &result)
	backup := result.Resource
	size := int64(len(e.blobs.published[backup.Artifact.Digest]))
	setArtifactMetadata(e, backup.Artifact, size, "available")

	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 2})
	payload, err = e.driveLocalIO(opRestore, restoreInput{Scope: e.scope, BackupArtifact: backup.Artifact, ExpectedVersion: 3}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(restore): %v", err)
	}
	if payload.Status != contract.StatusAccepted {
		t.Fatalf("restore of a real backup = %q (%v), want accepted", payload.Status, payload.Error)
	}
}
