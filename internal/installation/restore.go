package installation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/zatiti/zatiti/internal/contract"
)

// installation.restore requires exclusive quiesced maintenance, validates
// the uploaded backup artifact's binding, integrity and version, and then
// needs to export an encrypted recovery overlay of this installation's
// CURRENT (pre-restore) obligations before any rewind proceeds -- but that
// overlay's source_database_digest field names a digest of the current
// database, which needs exactly the same missing seam installation.backup
// does (see backup.go). Everything reachable without that seam -- exclusive
// maintenance and version checks, the backup artifact's own decrypt and
// binding verification, and gathering the obligations the overlay would
// carry -- runs for real; the overlay's own capture then reports the same
// named prerequisite_missing.
//
// The actual file-level restore (replacing the live SQLite database) is not
// this package's job at all: contract.Database has no restore method, and a
// domain module cannot swap the file out from under its own open database
// mid-process regardless. That step belongs to the controller, acting on
// its own platform/storage access outside any of this package's
// transactions, which is why _installation.restore.record exists as this
// package's own internal operation: the controller reports the disposition
// of work it performed itself, and this package only keeps its local job
// bookkeeping and paused/unresolved state honest around that report.

// restorePlan is Prepare's sealed decision for installation.restore.
type restorePlan struct {
	JobID          contract.ID     `json:"job_id"`
	InstallationID contract.ID     `json:"installation_id"`
	Generation     int64           `json:"generation"`
	BackupArtifact wireArtifactRef `json:"backup_artifact"`
	ArtifactSize   int64           `json:"artifact_size"`
}

// backupManifestDoc is the subset of BackupManifest (schema zatiti.backup/v1)
// this package inspects once a backup artifact decrypts. It is decoded with
// the standard library's lenient json.Unmarshal, not contract.DecodeStrict:
// this is this package's own artifact byte format, not a wire operation
// boundary, so unrecognized fields (future manifest additions) are simply
// ignored rather than rejected.
type backupManifestDoc struct {
	Schema         string      `json:"schema"`
	InstallationID contract.ID `json:"installation_id"`
	DatabaseDigest string      `json:"database_digest"`
	DatabaseSize   int64       `json:"database_size"`
	Paused         bool        `json:"paused"`
}

const backupManifestSchema = "zatiti.backup/v1"

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

	// Gathered and durably preserved here, inside Prepare's transaction,
	// because Perform runs with no Unit at all: even though the encrypted
	// recovery overlay artifact this operation would eventually publish
	// cannot be produced (the same missing database-digest seam as
	// backup.go), the obligations it would have carried are captured against
	// this transaction's consistent snapshot and persisted locally, so they
	// remain durable and inspectable across the restore attempt rather than
	// evaporating with the failed overlay.
	pending, err := s.effectsPending(ctx, unit, 50)
	if err != nil {
		return contract.IOPlan{}, err
	}
	manifest, err := s.memoryManifest(ctx, unit, in.Scope)
	if err != nil {
		return contract.IOPlan{}, err
	}
	now := s.deps.Clock.Now()
	for _, op := range pending {
		if err := insertObligation(ctx, unit, obligationRow{
			ID: s.deps.IDs.New(), InstallationID: in.Scope.InstallationID, RestoreJobID: job.ID,
			Owner: "effects", Kind: "claimed_effect", ResourceID: op.ID,
			ResourceVersion: contract.Version(op.Version), RecordArtifact: wireArtifactRef{},
			RecordDigest: "", State: op.State, RecordedAt: now,
		}); err != nil {
			return contract.IOPlan{}, err
		}
	}
	for _, req := range manifest.Obligations {
		resourceID := s.deps.IDs.New()
		if req.ResourceID != nil {
			resourceID = *req.ResourceID
		}
		if err := insertObligation(ctx, unit, obligationRow{
			ID: s.deps.IDs.New(), InstallationID: in.Scope.InstallationID, RestoreJobID: job.ID,
			Owner: "memory", Kind: "memory_write", ResourceID: resourceID, ResourceVersion: 1,
			RecordArtifact: wireArtifactRef{}, RecordDigest: "", State: req.Code, RecordedAt: now,
		}); err != nil {
			return contract.IOPlan{}, err
		}
	}

	prepared, err := json.Marshal(restorePlan{
		JobID: job.ID, InstallationID: in.Scope.InstallationID, Generation: unit.Generation(),
		BackupArtifact: in.BackupArtifact, ArtifactSize: art.Size,
	})
	if err != nil {
		return contract.IOPlan{}, fmt.Errorf("installation: encode restore plan: %w", err)
	}
	return contract.IOPlan{
		ID: s.deps.IDs.New(), Owner: owner, Invocation: inv, Actor: unit.Actor(),
		Scope: unit.Scope(), Generation: unit.Generation(), Prepared: prepared,
	}, nil
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

	key, err := s.deps.Secrets.Get(ctx, backupKeyRef(p.InstallationID))
	if err != nil || len(key) != 32 {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing(
			"no backup encryption key is custodied for this installation; it cannot decrypt this artifact"))}, nil
	}

	rc, err := s.deps.Blobs.Open(ctx, p.BackupArtifact.Digest, 0, p.ArtifactSize)
	if err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("backup artifact bytes are unavailable: %v", err))}, nil
	}
	defer func() { _ = rc.Close() }()
	sealed, err := io.ReadAll(io.LimitReader(rc, p.ArtifactSize+1))
	if err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("backup artifact bytes could not be read: %v", err))}, nil
	}

	plaintext, err := openBundle(key, sealed)
	if err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("backup artifact failed integrity or authenticity verification"))}, nil
	}

	var manifest backupManifestDoc
	if err := json.Unmarshal(plaintext, &manifest); err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("decrypted backup bundle is not a valid manifest"))}, nil
	}
	if manifest.Schema != backupManifestSchema {
		return contract.IOResult{Fault: faultOf(artifactFault("backup manifest schema %q is not %s", manifest.Schema, backupManifestSchema))}, nil
	}
	if manifest.InstallationID != p.InstallationID {
		return contract.IOResult{Fault: faultOf(invalidInput("backup manifest belongs to a different installation"))}, nil
	}
	if !manifest.Paused {
		return contract.IOResult{Fault: faultOf(artifactFault("backup manifest does not carry the required paused state"))}, nil
	}
	if manifest.DatabaseDigest == "" {
		return contract.IOResult{Fault: faultOf(artifactFault("backup manifest carries no database digest"))}, nil
	}

	// The backup artifact is genuinely valid and bound to this installation.
	// What remains -- exporting the encrypted recovery overlay of this
	// installation's CURRENT (pre-restore) obligations -- needs a digest of
	// the current database to fill RecoveryOverlay.source_database_digest,
	// which this package has no seam to produce (see the file doc comment).
	return contract.IOResult{Fault: faultOf(prerequisiteMissing(
		"backup artifact verified, but no database backup capability is injected into this module " +
			"to capture the pre-restore recovery overlay's source database digest"))}, nil
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
		// performRestore never returns a faultless result today: the missing
		// database-bytes seam always fires last. This branch exists so the
		// moment that seam is wired, the paused-job-awaiting-the-controller
		// path needs no rework here.
		j.State = "running"
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
