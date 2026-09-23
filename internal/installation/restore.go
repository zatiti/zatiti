package installation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// installation.restore requires exclusive quiesced maintenance. Prepare
// validates the uploaded backup artifact's metadata, creates the durable
// job and gathers the obligations the recovery overlay must preserve.
// Perform (outside any transaction) decrypts and verifies the backup
// bundle's binding, integrity and framed image, then exports the encrypted
// recovery overlay of this installation's CURRENT (pre-restore) state:
// source_database_digest comes from the DatabaseBackup capability, the
// obligations from Prepare's snapshot, sealed under the backup key, staged,
// published and verified. Finish registers the overlay artifact and leaves
// the job running and the installation paused.
//
// The actual file-level rewind (replacing the live SQLite database) is not
// this package's job: the capability is not the rewind mechanism, and a
// domain module cannot swap the file out from under its own open database
// mid-process. That step belongs to the controller, acting on its own
// platform/storage access outside any of this package's transactions, which
// is why _installation.restore.record exists as this package's own internal
// operation: the controller reports the disposition of work it performed
// itself, and this package keeps its local job bookkeeping and
// paused/unresolved state honest around that report.

// restorePlan is Prepare's sealed decision for installation.restore.
type restorePlan struct {
	JobID          contract.ID          `json:"job_id"`
	InstallationID contract.ID          `json:"installation_id"`
	Generation     int64                `json:"generation"`
	BackupArtifact wireArtifactRef      `json:"backup_artifact"`
	ArtifactSize   int64                `json:"artifact_size"`
	Obligations    []manifestObligation `json:"obligations"`
}

// restorePerformed is Perform's outcome: the published sealed recovery
// overlay and what it captured.
type restorePerformed struct {
	OverlayDigest        contract.Digest `json:"overlay_digest"`
	OverlaySize          int64           `json:"overlay_size"`
	SourceDatabaseDigest contract.Digest `json:"source_database_digest"`
	BackupDatabaseDigest contract.Digest `json:"backup_database_digest"`
}

// recoveryOverlayDoc is the RecoveryOverlay document
// (zatiti.recovery-overlay/v1) sealed inside the overlay bundle.
type recoveryOverlayDoc struct {
	Schema               string                  `json:"schema"`
	InstallationID       contract.ID             `json:"installation_id"`
	CapturedAt           string                  `json:"captured_at"`
	SourceGeneration     int64                   `json:"source_generation"`
	SourceDatabaseDigest contract.Digest         `json:"source_database_digest"`
	Obligations          []manifestObligation    `json:"obligations"`
	ArtifactEntries      []manifestArtifactEntry `json:"artifact_entries"`
	KeyPrerequisites     []string                `json:"key_prerequisites"`
}

const recoveryOverlaySchema = "zatiti.recovery-overlay/v1"

func (s *Service) prepareRestore(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[restoreInput](s, opRestore, inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if unit.ReadOnly() {
		return contract.IOPlan{}, invalidInput("operation %s is a mutation and requires a write transaction", opRestore)
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.IOPlan{}, err
	}
	st, err := loadRequiredState(ctx, unit, in.ExpectedVersion)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if !st.Maintenance {
		return contract.IOPlan{}, prerequisiteMissing(
			"installation.restore requires exclusive maintenance mode; call installation.maintenance.enter first")
	}

	artifacts, err := s.artifactsMetadata(ctx, unit, in.Scope, []wireArtifactRef{in.BackupArtifact})
	if err != nil {
		return contract.IOPlan{}, err
	}
	if len(artifacts) != 1 {
		return contract.IOPlan{}, notFound("backup artifact %s is unknown in this installation", in.BackupArtifact.ID)
	}
	art := artifacts[0]
	if art.Digest != in.BackupArtifact.Digest {
		return contract.IOPlan{}, invalidInput("backup artifact digest does not match the pinned reference")
	}
	if art.State != "available" {
		return contract.IOPlan{}, artifactFault("backup artifact %s is not available", art.ID)
	}

	job, err := s.executionJobCreate(ctx, unit, executionJobCreateInput{
		Scope: in.Scope, Owner: owner, Operation: opRestore, Input: inv.Input, SourceID: s.deps.IDs.New(),
	})
	if err != nil {
		return contract.IOPlan{}, err
	}
	if err := insertJob(ctx, unit, jobRow{
		ID: job.ID, Version: int64(job.Version), InstallationID: in.Scope.InstallationID,
		Kind: "restore", State: "pending", Requirements: []wireRequirement{},
		CreatedAt: s.deps.Clock.Now(), UpdatedAt: s.deps.Clock.Now(),
	}); err != nil {
		return contract.IOPlan{}, err
	}
	if err := emitTransition(ctx, unit, eventRestoreRequested, job.ID, contract.Version(job.Version)); err != nil {
		return contract.IOPlan{}, err
	}

	// The obligations the recovery overlay preserves are gathered here,
	// against this transaction's consistent snapshot, and persisted locally
	// so they stay durable and inspectable whatever happens to the overlay
	// artifact; Perform seals the same records into the overlay.
	pending, err := s.effectsPending(ctx, unit, 50)
	if err != nil {
		return contract.IOPlan{}, err
	}
	manifest, err := s.memoryManifest(ctx, unit, in.Scope)
	if err != nil {
		return contract.IOPlan{}, err
	}
	obligations, err := s.snapshotObligations(ctx, unit, in.Scope, job.ID, pending, manifest.Obligations)
	if err != nil {
		return contract.IOPlan{}, err
	}

	prepared, err := json.Marshal(restorePlan{
		JobID: job.ID, InstallationID: in.Scope.InstallationID, Generation: unit.Generation(),
		BackupArtifact: in.BackupArtifact, ArtifactSize: art.Size, Obligations: obligations,
	})
	if err != nil {
		return contract.IOPlan{}, fmt.Errorf("installation: encode restore plan: %w", err)
	}
	return contract.IOPlan{
		ID: s.deps.IDs.New(), Owner: owner, Invocation: inv, Actor: unit.Actor(),
		Scope: unit.Scope(), Generation: unit.Generation(), Prepared: prepared,
	}, nil
}

// obligationRecord names an obligation's own sealed record: the record
// artifact is the obligation itself inside the encrypted overlay and the
// record digest hashes its canonical fields, so the record is included,
// pinned and encrypted with the document rather than referenced elsewhere.
func obligationRecord(row obligationRow) (wireArtifactRef, contract.Digest) {
	record := mustMarshal(struct {
		ID              contract.ID      `json:"id"`
		Owner           string           `json:"owner"`
		Kind            string           `json:"kind"`
		ResourceID      contract.ID      `json:"resource_id"`
		ResourceVersion contract.Version `json:"resource_version"`
		State           string           `json:"state"`
		RecordedAt      string           `json:"recorded_at"`
	}{row.ID, row.Owner, row.Kind, row.ResourceID, row.ResourceVersion, row.State, formatStamp(row.RecordedAt)})
	digest := digestOf(record)
	return wireArtifactRef{ID: row.ID, Digest: digest}, digest
}

func (o obligationRow) manifest() manifestObligation {
	return manifestObligation{
		ID: o.ID, Owner: o.Owner, Kind: o.Kind, ResourceID: o.ResourceID, ResourceVersion: o.ResourceVersion,
		RecordArtifact: o.RecordArtifact, RecordDigest: o.RecordDigest, State: o.State, RecordedAt: formatStamp(o.RecordedAt),
	}
}

// Obligation kinds captured into a BackupManifest or RecoveryOverlay. The
// owner named alongside each kind is the owner whose own restore.merge
// operation folds it back after a rewind.
const (
	obligationClaimedEffect     = "claimed_effect"
	obligationUnknownEffect     = "unknown_effect"
	obligationMemoryWrite       = "memory_write"
	obligationRevokedCredential = "revoked_credential"
	obligationRevokedGrant      = "revoked_grant"
)

// snapshotObligations captures this installation's currently pending
// effects, unresolved memory obligations and current credential/grant
// revocations as durable RecoveryObligation rows tied to jobID (a backup or
// restore job), so they stay inspectable and mergeable independent of any
// sealed bundle's own encrypted bytes.
//
// Each pending Operation is classified by its actual state rather than one
// undifferentiated bucket: outcome_unknown becomes kind unknown_effect
// (contract.RecoveryObligation names it separately from an ordinary
// in-flight claim precisely so a later monotonic merge can tell an unknown
// outcome apart from a claim that is merely still running), everything
// else becomes claimed_effect.
//
// Revocations are captured through _identity.revocations, identity's own
// read of its own tables. Capturing them is what makes "retain revocation
// through restore" (credential.revoke's frozen behavior) enforceable at
// all: a credential or grant revoked AFTER a backup was taken is not in
// that backup's bytes, so without an obligation carrying it, rewinding to
// that backup would silently bring it back to life. This is the exact gap
// restore_test.go's TestRestoreOverlayPreservesPostBackupUnknownEffectAcrossRewind
// doc comment named as unexercisable before _identity.revocations existed.
//
// Both installation.backup (the paused/quiesced snapshot item 2 requires)
// and installation.restore (the monotonic recovery overlay item 3 requires)
// call this at Prepare time.
func (s *Service) snapshotObligations(ctx context.Context, unit contract.Unit, scope wireScope, jobID contract.ID, pending []peerOperation, memObligations []wireRequirement) ([]manifestObligation, error) {
	installationID := scope.InstallationID
	now := s.deps.Clock.Now()
	var obligations []manifestObligation
	for _, op := range pending {
		kind := obligationClaimedEffect
		if op.State == "outcome_unknown" {
			kind = obligationUnknownEffect
		}
		row := obligationRow{
			ID: s.deps.IDs.New(), InstallationID: installationID, RestoreJobID: jobID,
			Owner: "effects", Kind: kind, ResourceID: op.ID,
			ResourceVersion: contract.Version(op.Version), State: op.State, RecordedAt: now,
		}
		row.RecordArtifact, row.RecordDigest = obligationRecord(row)
		if err := insertObligation(ctx, unit, row); err != nil {
			return nil, err
		}
		obligations = append(obligations, row.manifest())
	}
	for _, req := range memObligations {
		resourceID := s.deps.IDs.New()
		if req.ResourceID != nil {
			resourceID = *req.ResourceID
		}
		row := obligationRow{
			ID: s.deps.IDs.New(), InstallationID: installationID, RestoreJobID: jobID,
			Owner: "memory", Kind: obligationMemoryWrite, ResourceID: resourceID, ResourceVersion: 1,
			State: req.Code, RecordedAt: now,
		}
		row.RecordArtifact, row.RecordDigest = obligationRecord(row)
		if err := insertObligation(ctx, unit, row); err != nil {
			return nil, err
		}
		obligations = append(obligations, row.manifest())
	}
	revocations, err := s.identityRevocations(ctx, unit, scope)
	if err != nil {
		return nil, err
	}
	for _, rev := range revocations {
		kind := obligationRevokedCredential
		if rev.Kind == "grant" {
			kind = obligationRevokedGrant
		}
		row := obligationRow{
			ID: s.deps.IDs.New(), InstallationID: installationID, RestoreJobID: jobID,
			Owner: "identity", Kind: kind, ResourceID: rev.ID, ResourceVersion: rev.Version,
			State: "revoked", RecordedAt: now,
		}
		row.RecordArtifact, row.RecordDigest = obligationRecord(row)
		if err := insertObligation(ctx, unit, row); err != nil {
			return nil, err
		}
		obligations = append(obligations, row.manifest())
	}
	if obligations == nil {
		obligations = []manifestObligation{}
	}
	return obligations, nil
}

func (s *Service) performRestore(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	var p restorePlan
	if err := json.Unmarshal(plan.Prepared, &p); err != nil {
		return contract.IOResult{}, fmt.Errorf("installation: decode restore plan: %w", err)
	}
	if s.deps.Blobs == nil {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing("no blob store is configured; the backup artifact cannot be read"))}, nil
	}
	if s.deps.Secrets == nil {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing("no secret store is configured; the backup key cannot be resolved"))}, nil
	}

	artifact, err := readPublished(ctx, s.deps.Blobs, p.BackupArtifact.Digest, p.ArtifactSize)
	if err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("backup artifact bytes are unavailable: %v", err))}, nil
	}
	// The sealing key resolves by the reference the bundle's own clear
	// header carries: no name lookup, nothing that a restart or a database
	// rewind could have lost.
	keyRef, sealed, err := decodeArtifact(artifact)
	if err != nil {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing("%v", err))}, nil
	}
	key, err := s.deps.Secrets.Get(ctx, keyRef)
	if err != nil || len(key) != 32 {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing(
			"the backup's key reference does not resolve in this installation's secret store; the bundle cannot be decrypted here"))}, nil
	}
	manifest, _, err := openBackupBundle(key, sealed, p.InstallationID)
	if err != nil {
		if errors.Is(err, errBundleForeign) {
			return contract.IOResult{Fault: faultOf(invalidInput("backup manifest belongs to a different installation"))}, nil
		}
		return contract.IOResult{Fault: faultOf(artifactFault("backup artifact failed integrity or authenticity verification: %v", err))}, nil
	}

	// The backup is genuinely valid and bound to this installation. Before
	// any rewind, export the encrypted recovery overlay of the current
	// state; its source digest is the current database's consistent image.
	if s.backup == nil {
		return contract.IOResult{Fault: faultOf(backupCapabilityMissing())}, nil
	}
	sourceDigest, _, err := digestImage(ctx, s.backup)
	if err != nil {
		return contract.IOResult{Fault: faultOf(fmt.Errorf("consistent backup of the current database failed: %w", err))}, nil
	}
	overlay := recoveryOverlayDoc{
		Schema: recoveryOverlaySchema, InstallationID: p.InstallationID,
		CapturedAt: formatStamp(s.deps.Clock.Now()), SourceGeneration: p.Generation,
		SourceDatabaseDigest: sourceDigest, Obligations: p.Obligations,
		ArtifactEntries: []manifestArtifactEntry{}, KeyPrerequisites: []string{keyRef},
	}
	if overlay.Obligations == nil {
		overlay.Obligations = []manifestObligation{}
	}
	overlayJSON, err := json.Marshal(overlay)
	if err != nil {
		return contract.IOResult{}, fmt.Errorf("installation: encode recovery overlay: %w", err)
	}
	sealedOverlay, err := sealBundle(key, encodeFrame(overlayJSON, nil))
	if err != nil {
		return contract.IOResult{Fault: faultOf(err)}, nil
	}
	overlayDigest, overlaySize, err := stagePublished(ctx, s.deps.Blobs, encodeArtifact(keyRef, sealedOverlay))
	if err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("recovery overlay could not be published: %v", err))}, nil
	}
	published, err := readPublished(ctx, s.deps.Blobs, overlayDigest, overlaySize)
	if err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("published recovery overlay could not be read back: %v", err))}, nil
	}
	if _, err := s.openPublishedOverlay(ctx, published, p.InstallationID); err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("published recovery overlay failed verification: %v", err))}, nil
	}

	raw, err := json.Marshal(restorePerformed{
		OverlayDigest: overlayDigest, OverlaySize: overlaySize,
		SourceDatabaseDigest: sourceDigest, BackupDatabaseDigest: manifest.DatabaseDigest,
	})
	if err != nil {
		return contract.IOResult{}, fmt.Errorf("installation: encode restore result: %w", err)
	}
	return contract.IOResult{Data: raw}, nil
}

// restoreOverlayInput is the _installation.restore.overlay input.
type restoreOverlayInput struct {
	JobID contract.ID `json:"job_id"`
}

// restoreOverlayOutput is the _installation.restore.overlay output: the
// published, sealed recovery-overlay artifact and its exact byte size, which
// a caller needs to read the blob back in full.
type restoreOverlayOutput struct {
	Artifact wireArtifactRef `json:"artifact"`
	Size     int64           `json:"size"`
}

// handleRestoreOverlay implements _installation.restore.overlay: the
// published recovery-overlay artifact this restore job's own Finish already
// registered. The controller reads it BEFORE the database swap, while its
// own Application is still valid, and journals it, so the reference survives
// a crash between the claim and the merge exactly as the job id already
// does. This operation performs no IO and decrypts nothing; a job with no
// registered overlay is not_found rather than a guess.
func handleRestoreOverlay(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[restoreOverlayInput](s, opRestoreOverlayName, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	j, err := loadJob(ctx, unit, in.JobID)
	if err != nil {
		return contract.Payload{}, err
	}
	if j == nil || j.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, notFound("job %s is unknown in this installation", in.JobID)
	}
	if j.Kind != "restore" {
		return contract.Payload{}, invalidInput("job %s is not a restore job", in.JobID)
	}
	if j.OverlayArtifact.ID == "" || j.OverlayArtifact.Digest == "" || j.OverlaySize < 1 {
		return contract.Payload{}, notFound(
			"restore job %s has no published recovery overlay registered against it", in.JobID)
	}
	return completed(restoreOverlayOutput{Artifact: j.OverlayArtifact, Size: j.OverlaySize})
}

// openPublishedOverlay resolves the key from the overlay artifact's own
// header and opens it, the path a later merge takes.
func (s *Service) openPublishedOverlay(ctx context.Context, artifact []byte, installationID contract.ID) (recoveryOverlayDoc, error) {
	keyRef, sealed, err := decodeArtifact(artifact)
	if err != nil {
		return recoveryOverlayDoc{}, err
	}
	key, err := s.deps.Secrets.Get(ctx, keyRef)
	if err != nil || len(key) != 32 {
		return recoveryOverlayDoc{}, fmt.Errorf("the overlay's key reference does not resolve in this installation's secret store")
	}
	return openRecoveryOverlay(key, sealed, installationID)
}

// openRecoveryOverlay decrypts a sealed overlay bundle and checks its
// binding to this installation.
func openRecoveryOverlay(key, sealed []byte, installationID contract.ID) (recoveryOverlayDoc, error) {
	frame, err := openBundle(key, sealed)
	if err != nil {
		return recoveryOverlayDoc{}, err
	}
	doc, image, err := decodeFrame(frame)
	if err != nil {
		return recoveryOverlayDoc{}, err
	}
	if len(image) != 0 {
		return recoveryOverlayDoc{}, errors.New("recovery overlay frames an image")
	}
	var overlay recoveryOverlayDoc
	if err := json.Unmarshal(doc, &overlay); err != nil {
		return recoveryOverlayDoc{}, fmt.Errorf("recovery overlay is not valid JSON: %w", err)
	}
	switch {
	case overlay.Schema != recoveryOverlaySchema:
		return recoveryOverlayDoc{}, fmt.Errorf("recovery overlay schema %q is not %s", overlay.Schema, recoveryOverlaySchema)
	case overlay.InstallationID != installationID:
		return recoveryOverlayDoc{}, errBundleForeign
	case overlay.SourceDatabaseDigest == "":
		return recoveryOverlayDoc{}, errors.New("recovery overlay carries no source database digest")
	}
	return overlay, nil
}

func (s *Service) finishRestore(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	var p restorePlan
	if err := json.Unmarshal(plan.Prepared, &p); err != nil {
		return contract.Payload{}, fmt.Errorf("installation: decode restore plan: %w", err)
	}
	j, err := loadJob(ctx, unit, p.JobID)
	if err != nil {
		return contract.Payload{}, err
	}
	if j == nil {
		return contract.Payload{}, internalError("restore job %s vanished before Finish", p.JobID)
	}
	previous := j.Version
	now := s.deps.Clock.Now()
	if result.Fault != nil {
		j.State = "failed"
		j.Requirements = []wireRequirement{{Code: result.Fault.Code, Message: result.Fault.Message}}
	} else {
		var perf restorePerformed
		if err := json.Unmarshal(result.Data, &perf); err != nil {
			return contract.Payload{}, fmt.Errorf("installation: decode restore perform result: %w", err)
		}
		overlayRef, err := s.artifactsPublish(ctx, unit, wireScope{InstallationID: p.InstallationID}, perf.OverlayDigest, perf.OverlaySize, overlayMediaType)
		if err != nil {
			return contract.Payload{}, err
		}
		overlayID := overlayRef.ID
		// The restore is accepted and stays paused: the overlay is published,
		// the verified backup names the image to rewind to, and the
		// controller performs the rewind and reports through
		// _installation.restore.record.
		//
		// The overlay reference is recorded twice, deliberately and for two
		// different readers: as typed columns on this job row, which
		// _installation.restore.overlay serves to the controller that must
		// actually fetch and merge those bytes after the swap, and as the
		// recovery_overlay_published requirement below, which installation.
		// job.get surfaces as an inspectable disposition. A requirement is a
		// human-readable prerequisite and carries neither digest nor size, so
		// it can never stand in for the machine-resolvable reference.
		j.OverlayArtifact = overlayRef
		j.OverlaySize = perf.OverlaySize
		j.State = "running"
		j.Requirements = append(j.Requirements,
			wireRequirement{
				Code:       "recovery_overlay_published",
				Message:    "encrypted recovery overlay of the current installation published; source database digest " + string(perf.SourceDatabaseDigest),
				ResourceID: &overlayID,
			},
			wireRequirement{
				Code:    "external_action_required",
				Message: "the controller must rewind the database to the verified backup image " + string(perf.BackupDatabaseDigest) + " and report through _installation.restore.record",
			})
	}
	j.Version++
	j.UpdatedAt = now
	if err := updateJob(ctx, unit, *j, previous); err != nil {
		return contract.Payload{}, err
	}
	if _, err := s.executionJobRecord(ctx, unit, executionJobRecordInput{
		JobID: j.ID, ExpectedVersion: previous, Generation: p.Generation, State: j.State,
	}); err != nil {
		return contract.Payload{}, err
	}
	if result.Fault != nil {
		return contract.Payload{Status: contract.StatusFailed, Error: result.Fault}, nil
	}
	return contract.Payload{Status: contract.StatusAccepted, Data: mustMarshal(resourceOut[wireJob]{Resource: j.wire()})}, nil
}
