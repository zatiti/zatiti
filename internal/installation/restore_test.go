package installation

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// sealFixtureBackup builds and publishes a synthetic, correctly-sealed
// backup artifact for env e's installation, as a real installation.backup
// would if the database-bytes seam existed (see backup.go). It is a test
// fixture, not a claim that installation.backup itself produced these
// bytes: constructing it directly is the only way to exercise restore's own
// decrypt/verify path under the same contract gap that blocks backup.
func sealFixtureBackup(t *testing.T, e *testEnv, mutate func(*backupManifestDoc)) (wireArtifactRef, int64) {
	t.Helper()
	key, err := ensureBackupKey(e.ctx, e.secrets, e.install)
	if err != nil {
		t.Fatalf("ensureBackupKey: %v", err)
	}
	manifest := backupManifestDoc{
		Schema: backupManifestSchema, InstallationID: e.install,
		DatabaseDigest: "d34d", DatabaseSize: 4096, Paused: true,
	}
	if mutate != nil {
		mutate(&manifest)
	}
	plaintext, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	sealed, err := sealBundle(key, plaintext)
	if err != nil {
		t.Fatalf("sealBundle: %v", err)
	}
	digest := e.blobs.publishBytes(sealed)
	return wireArtifactRef{ID: e.ids.New(), Digest: digest}, int64(len(sealed))
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

// Z14.paused_restore: a verified encrypted backup for THIS installation,
// bound to this installation and carrying the required paused:true, passes
// binding and integrity verification under exclusive maintenance. Capturing
// the pre-restore recovery overlay needs a digest of the CURRENT database --
// the same missing seam as backup.go -- so the operation reports that named
// gap honestly rather than claiming the restore actually resumed anything.
func TestRestoreVerifiesArtifactThenFailsHonestlyOnTheOverlayDatabaseSeam(t *testing.T) {
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
	if payload.Error == nil || payload.Error.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("restore error = %v, want prerequisite_missing", payload.Error)
	}

	// Preserve both backup and later durable obligations across rewind: the
	// unresolved effects and memory manifest this installation currently
	// carries were gathered before the operation failed and persisted into
	// this package's own durable obligations table -- the encrypted overlay
	// artifact it would publish is exactly what the gap blocks, not the
	// preservation of the obligations themselves.
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
	manifest := backupManifestDoc{Schema: backupManifestSchema, InstallationID: foreignInstall,
		DatabaseDigest: "d34d", DatabaseSize: 4096, Paused: true}
	plaintext, _ := json.Marshal(manifest)
	sealed, err := sealBundle(key, plaintext)
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
