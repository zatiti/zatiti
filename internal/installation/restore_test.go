package installation

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
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
	key, err := ensureBackupKey(e.ctx, e.secrets, e.install)
	if err != nil {
		t.Fatalf("ensureBackupKey: %v", err)
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
	digest := e.blobs.publishBytes(sealed)
	return wireArtifactRef{ID: e.ids.New(), Digest: digest}, int64(len(sealed))
}

func fixtureManifest(installation contract.ID) backupManifestDoc {
	return backupManifestDoc{
		Schema: backupManifestSchema, InstallationID: installation, BackupID: "00000000-0000-4000-8000-000000009999",
		Generation: 1, CreatedAt: "2026-09-12T00:00:00Z",
		DatabaseDigest: digestOf(fixtureImage), DatabaseSize: int64(len(fixtureImage)), DatabaseArchiveEntry: databaseArchiveEntry,
		DatabaseSchemaVersions: []manifestSchemaVersion{}, Artifacts: []manifestArtifactEntry{}, Brains: []manifestBrainEntry{},
		RetainedObligations: []manifestObligation{}, KeyPrerequisites: []string{backupKeyRef(installation)},
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
		return unit.QueryRowContext(e.ctx,
			`SELECT resource_id FROM installation_recovery_obligations WHERE kind = 'claimed_effect'`).Scan(&obligationResource)
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
	key, err := ensureBackupKey(e.ctx, e.secrets, e.install)
	if err != nil {
		t.Fatalf("ensureBackupKey: %v", err)
	}
	manifest := fixtureManifest(foreignInstall)
	plaintext, _ := json.Marshal(manifest)
	sealed, err := sealBundle(key, encodeFrame(plaintext, fixtureImage))
	if err != nil {
		t.Fatalf("sealBundle: %v", err)
	}
	digest := e.blobs.publishBytes(sealed)
	ref := wireArtifactRef{ID: e.ids.New(), Digest: digest}
	setArtifactMetadata(e, ref, int64(len(sealed)), "available")

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
	overlay, err := openRecoveryOverlay(e.backupKey(), sealed, e.install)
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
