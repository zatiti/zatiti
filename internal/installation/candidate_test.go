package installation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Tests for installation's half of the completed restore handoff: the
// revocations a paused backup now captures, the published overlay reference
// the controller resolves before the swap, and the two exported wrappers
// entrypoint assembly resolves real artifacts through.
//
// Every artifact in this file is a real one: sealed by this package's own
// real backup/restore Perform through the real AES-256-GCM pipeline and
// published to the blob store. Nothing here opens a hand-made frame.

// setRevocations installs a _identity.revocations answer, standing in for
// identity's own read of its own tables (which this package, correctly, has
// no other way to reach).
func (e *testEnv) setRevocations(revocations ...peerRevocation) {
	e.t.Helper()
	e.ports.set(peerIdentityRevocations, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(identityRevocationsOutput{Revocations: revocations})
	})
}

// obligationsOfJob reads back the durable obligations captured for one job.
func (e *testEnv) obligationsOfJob(jobID contract.ID) []obligationRow {
	e.t.Helper()
	var out []obligationRow
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		out, err = listObligationsByJob(e.ctx, unit, jobID)
		return err
	}); err != nil {
		e.t.Fatalf("list obligations: %v", err)
	}
	return out
}

// TestBackupCapturesCurrentRevocationsIntoItsManifest is the capture half of
// the behavior internal/installation/restore_test.go's
// TestRestoreOverlayPreservesPostBackupUnknownEffectAcrossRewind doc comment
// named as unexercisable: a credential and a grant this installation holds
// revoked are now read through _identity.revocations and sealed into the
// manifest's retained obligations, with the owner and kind the owner's own
// merge operation folds back.
func TestBackupCapturesCurrentRevocationsIntoItsManifest(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	e.bindBackup(backupOnly{db: e.db})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	credentialID := e.ids.New()
	grantID := e.ids.New()
	principalID := e.ids.New()
	e.setRevocations(
		peerRevocation{Kind: "credential", ID: credentialID, Version: 2, PrincipalID: principalID},
		peerRevocation{Kind: "grant", ID: grantID, Version: 3, PrincipalID: principalID},
	)

	backup := e.backupNow("with-revocations")
	if len(e.ports.callsOf(peerIdentityRevocations)) == 0 {
		t.Fatal("backup never asked identity for this installation's current revocations")
	}

	sealed, ok := e.blobs.published[backup.Artifact.Digest]
	if !ok {
		t.Fatalf("bundle %s was not published", backup.Artifact.Digest)
	}
	_, key, frame := e.openArtifact(sealed)
	manifest, _, err := openBackupBundle(key, frame, e.install)
	if err != nil {
		t.Fatalf("open backup bundle: %v", err)
	}
	byKind := map[string]manifestObligation{}
	for _, o := range manifest.RetainedObligations {
		byKind[o.Kind] = o
	}
	credential, hasCredential := byKind[obligationRevokedCredential]
	grant, hasGrant := byKind[obligationRevokedGrant]
	if !hasCredential || !hasGrant {
		t.Fatalf("manifest retained obligations = %+v, want both revocation kinds", manifest.RetainedObligations)
	}
	if credential.ResourceID != credentialID || credential.Owner != "identity" || credential.State != "revoked" {
		t.Fatalf("captured credential revocation = %+v, want identity/%s/revoked", credential, credentialID)
	}
	if grant.ResourceID != grantID || grant.Owner != "identity" || grant.ResourceVersion != 3 {
		t.Fatalf("captured grant revocation = %+v, want identity/%s at version 3", grant, grantID)
	}
	if credential.RecordDigest == "" || grant.RecordDigest == "" {
		t.Fatal("captured revocations carry no record digest; they are not pinned inside the sealed document")
	}
}

// TestBackupOfAnInstallationWithNoObligationsCapturesNone is the required
// behavior "backup and restore of an installation with zero pending
// obligations still succeeds": the default fixture reports no pending
// effects, no memory obligations and no revocations, and the manifest
// carries an explicit empty list rather than failing or omitting the field.
func TestBackupOfAnInstallationWithNoObligationsCapturesNone(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	e.bindBackup(backupOnly{db: e.db})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	backup := e.backupNow("empty")
	sealed := e.blobs.published[backup.Artifact.Digest]
	_, key, frame := e.openArtifact(sealed)
	manifest, _, err := openBackupBundle(key, frame, e.install)
	if err != nil {
		t.Fatalf("open backup bundle: %v", err)
	}
	if manifest.RetainedObligations == nil || len(manifest.RetainedObligations) != 0 {
		t.Fatalf("retained obligations = %+v, want an explicit empty list", manifest.RetainedObligations)
	}
	if got := e.obligationsOfJob(backup.ID); len(got) != 0 {
		t.Fatalf("durable obligations = %+v, want none", got)
	}
}

// TestRestoreOverlayExposesThePublishedOverlayToTheController proves the
// reference the controller must resolve before the swap is served exactly,
// with digest and size, against the same artifact installation.restore
// actually published -- not merely the id a requirement message could carry.
func TestRestoreOverlayExposesThePublishedOverlayToTheController(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	e.bindBackup(backupOnly{db: e.db})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})
	backup := e.backupNow("for-restore")
	size := int64(len(e.blobs.published[backup.Artifact.Digest]))
	setArtifactMetadata(e, backup.Artifact, size, "available")
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 2})

	payload, err := e.driveLocalIO(opRestore, restoreInput{
		Scope: e.scope, BackupArtifact: backup.Artifact, ExpectedVersion: 3,
	}, true)
	if err != nil || payload.Status != contract.StatusAccepted {
		t.Fatalf("restore: %v (%s %v)", err, payload.Status, payload.Error)
	}
	var job resourceOut[wireJob]
	e.decode(payload.Data, &job)

	overlayPayload, err := e.callQuery(opRestoreOverlayName, restoreOverlayInput{JobID: job.Resource.ID})
	if err != nil {
		t.Fatalf("%s: %v", opRestoreOverlayName, err)
	}
	var overlay restoreOverlayOutput
	e.decode(overlayPayload.Data, &overlay)
	if overlay.Artifact.ID == "" || overlay.Artifact.Digest == "" || overlay.Size < 1 {
		t.Fatalf("%s returned %+v, want a resolvable artifact reference", opRestoreOverlayName, overlay)
	}
	if _, ok := e.blobs.published[overlay.Artifact.Digest]; !ok {
		t.Fatalf("the reported overlay digest %s names no published bytes", overlay.Artifact.Digest)
	}
	if got := int64(len(e.blobs.published[overlay.Artifact.Digest])); got != overlay.Size {
		t.Fatalf("reported overlay size %d does not match the %d published bytes", overlay.Size, got)
	}
	// The same reference is still surfaced as an inspectable requirement,
	// for job.get's human-readable disposition.
	var reported contract.ID
	for _, r := range job.Resource.Requirements {
		if r.Code == "recovery_overlay_published" && r.ResourceID != nil {
			reported = *r.ResourceID
		}
	}
	if reported != overlay.Artifact.ID {
		t.Fatalf("the requirement names overlay %s but the query returns %s", reported, overlay.Artifact.ID)
	}

	// A job that registered no overlay is not_found, never a guess.
	_ = e.expectQueryFault(opRestoreOverlayName, restoreOverlayInput{JobID: backup.ID}, contract.CodeInvalidInput)
	_ = e.expectQueryFault(opRestoreOverlayName, restoreOverlayInput{JobID: e.ids.New()}, contract.CodeNotFound)
}

// TestResolveBackupCandidateDecryptsARealPublishedBundle proves the exported
// wrapper resolves a REAL sealed backup -- the bytes installation.backup
// itself published, under the key its own reference resolves -- into a
// staged, decrypted image file matching the manifest's digest, and that the
// same call refuses a foreign binding and a corrupted object.
func TestResolveBackupCandidateDecryptsARealPublishedBundle(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	e.bindBackup(backupOnly{db: e.db})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})
	backup := e.backupNow("exported-wrapper")

	dir := filepath.Join(t.TempDir(), "staging")
	candidate, err := ResolveBackupCandidate(e.ctx, e.secrets, e.blobs, e.install,
		contract.ArtifactRef{ID: backup.Artifact.ID, Digest: backup.Artifact.Digest}, 0, dir)
	if err != nil {
		t.Fatalf("ResolveBackupCandidate: %v", err)
	}
	image, err := os.ReadFile(candidate.Path)
	if err != nil {
		t.Fatalf("read staged candidate: %v", err)
	}
	if digestOf(image) != candidate.DatabaseDigest {
		t.Fatal("the staged image does not hash to the digest the manifest claims for it")
	}
	if int64(len(image)) != candidate.DatabaseSize {
		t.Fatalf("staged image is %d bytes, manifest claims %d", len(image), candidate.DatabaseSize)
	}
	if candidate.InstallationID != e.install {
		t.Fatalf("candidate is bound to %s, want %s", candidate.InstallationID, e.install)
	}
	if candidate.BackupID == "" || candidate.Generation < 1 {
		t.Fatalf("candidate carries no backup identity or generation: %+v", candidate)
	}
	if info, err := os.Stat(candidate.Path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("staged candidate mode = %v (%v); a decrypted image must be private", info.Mode().Perm(), err)
	}

	// Bound to another installation: refused, and nothing is staged.
	if _, err := ResolveBackupCandidate(e.ctx, e.secrets, e.blobs, e.ids.New(),
		contract.ArtifactRef{ID: backup.Artifact.ID, Digest: backup.Artifact.Digest}, 0, dir); err == nil {
		t.Fatal("a bundle bound to another installation was accepted")
	}

	// Corrupted bytes: the digest check refuses before any decryption.
	corrupted := append([]byte(nil), e.blobs.published[backup.Artifact.Digest]...)
	corrupted[len(corrupted)-1] ^= 0xFF
	e.blobs.mu.Lock()
	e.blobs.published[backup.Artifact.Digest] = corrupted
	e.blobs.mu.Unlock()
	_, err = ResolveBackupCandidate(e.ctx, e.secrets, e.blobs, e.install,
		contract.ArtifactRef{ID: backup.Artifact.ID, Digest: backup.Artifact.Digest}, 0, dir)
	if err == nil {
		t.Fatal("a corrupted backup artifact was accepted")
	}
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodeArtifactFault {
		t.Fatalf("corrupted artifact = %v, want artifact_fault", err)
	}
}

// TestOpenRecoveryOverlayReadsARealSealedOverlay proves the second exported
// wrapper against the real sealed overlay installation.restore published:
// the obligations come back in the exact RecoveryObligation shape each
// owner's merge operation consumes, bound to this installation, and a
// foreign binding is refused.
func TestOpenRecoveryOverlayReadsARealSealedOverlay(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.startGeneration()
	e.bindBackup(backupOnly{db: e.db})
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})
	backup := e.backupNow("overlay-source")
	size := int64(len(e.blobs.published[backup.Artifact.Digest]))
	setArtifactMetadata(e, backup.Artifact, size, "available")
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 2})

	credentialID := e.ids.New()
	e.setRevocations(peerRevocation{Kind: "credential", ID: credentialID, Version: 2, PrincipalID: e.ids.New()})

	payload, err := e.driveLocalIO(opRestore, restoreInput{
		Scope: e.scope, BackupArtifact: backup.Artifact, ExpectedVersion: 3,
	}, true)
	if err != nil || payload.Status != contract.StatusAccepted {
		t.Fatalf("restore: %v (%s %v)", err, payload.Status, payload.Error)
	}
	var job resourceOut[wireJob]
	e.decode(payload.Data, &job)
	overlayPayload, err := e.callQuery(opRestoreOverlayName, restoreOverlayInput{JobID: job.Resource.ID})
	if err != nil {
		t.Fatalf("%s: %v", opRestoreOverlayName, err)
	}
	var ref restoreOverlayOutput
	e.decode(overlayPayload.Data, &ref)

	overlay, err := OpenRecoveryOverlay(e.ctx, e.secrets, e.blobs, e.install,
		contract.ArtifactRef{ID: ref.Artifact.ID, Digest: ref.Artifact.Digest}, ref.Size)
	if err != nil {
		t.Fatalf("OpenRecoveryOverlay: %v", err)
	}
	if overlay.InstallationID != e.install || overlay.CapturedAt == "" || overlay.SourceDatabaseDigest == "" {
		t.Fatalf("overlay = %+v, want this installation, a capture stamp and a source digest", overlay)
	}
	found := false
	for _, ob := range overlay.Obligations {
		if ob.Kind == obligationRevokedCredential && ob.ResourceID == credentialID {
			found = true
			if ob.Owner != "identity" || ob.RecordDigest == "" || ob.RecordedAt == "" {
				t.Fatalf("captured revocation obligation = %+v, want a pinned identity-owned record", ob)
			}
		}
	}
	if !found {
		t.Fatalf("the sealed overlay does not carry the revoked credential: %+v", overlay.Obligations)
	}

	// The same overlay, read against a different installation, is refused.
	if _, err := OpenRecoveryOverlay(e.ctx, e.secrets, e.blobs, e.ids.New(),
		contract.ArtifactRef{ID: ref.Artifact.ID, Digest: ref.Artifact.Digest}, ref.Size); err == nil {
		t.Fatal("an overlay bound to another installation was accepted")
	}

	// Size 0 (the controller's own case: a reference with no recorded size)
	// resolves the identical document.
	unsized, err := OpenRecoveryOverlay(e.ctx, e.secrets, e.blobs, e.install,
		contract.ArtifactRef{ID: ref.Artifact.ID, Digest: ref.Artifact.Digest}, 0)
	if err != nil {
		t.Fatalf("OpenRecoveryOverlay without a recorded size: %v", err)
	}
	if len(unsized.Obligations) != len(overlay.Obligations) || unsized.CapturedAt != overlay.CapturedAt {
		t.Fatal("resolving without a recorded size produced a different overlay")
	}
}

// TestMergeOperationNamesOnlyOwnersThatDeclareOne pins the routing table
// this package hands entrypoint assembly: exactly the three owners whose
// obligation kinds this package produces, and nothing else.
func TestMergeOperationNamesOnlyOwnersThatDeclareOne(t *testing.T) {
	for owner, want := range map[string]string{
		"effects":  "_effects.restore.merge",
		"identity": "_identity.restore.merge",
		"memory":   "_memory.restore.merge",
	} {
		got, ok := MergeOperation(owner)
		if !ok || got != want {
			t.Errorf("MergeOperation(%q) = %q/%t, want %q/true", owner, got, ok, want)
		}
	}
	for _, owner := range []string{"installation", "execution", "tasks", "", "Effects"} {
		if got, ok := MergeOperation(owner); ok {
			t.Errorf("MergeOperation(%q) = %q, want no merge operation", owner, got)
		}
	}
}
