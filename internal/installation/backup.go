package installation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// installation.backup. Prepare (in the caller's transaction) requires a
// paused installation, creates the durable job and gathers the brain
// revisions the manifest pins. Perform (outside any transaction) resolves
// the installation's backup key, streams one consistent SQLite image from
// the DatabaseBackup capability through a hashing writer, frames it with
// the BackupManifest (schema zatiti.backup/v1) whose database_digest and
// database_size describe exactly those bytes, seals the frame under the
// backup key, stages and publishes it, and verifies the published bundle by
// decrypting it again before reporting success. Finish registers the
// artifact metadata through the artifacts owner and records the job.
// Without the capability Perform fails prerequisite_missing naming it and
// never fabricates a digest.

// backupPlan is Prepare's sealed decision for installation.backup.
type backupPlan struct {
	JobID          contract.ID `json:"job_id"`
	InstallationID contract.ID `json:"installation_id"`
	Generation     int64       `json:"generation"`
	BrainRevisions []wireRef   `json:"brain_revisions"`
	CreatedAt      string      `json:"created_at"`
	// KeyRef is the recorded backup key reference, empty before the first
	// backup custodied one.
	KeyRef string `json:"key_ref"`
}

// backupPerformed is Perform's outcome: the published sealed bundle and the
// database image it frames, all by digest and size.
type backupPerformed struct {
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	DatabaseDigest contract.Digest `json:"database_digest"`
	DatabaseSize   int64           `json:"database_size"`
	// KeyRef is the reference the bundle's sealing key resolves under.
	KeyRef string `json:"key_ref"`
}

// backupManifestDoc is the BackupManifest document (zatiti.backup/v1)
// sealed inside every backup bundle. Restore decodes it leniently: it is
// this package's own artifact format, not a wire operation boundary.
type backupManifestDoc struct {
	Schema                   string                  `json:"schema"`
	InstallationID           contract.ID             `json:"installation_id"`
	BackupID                 contract.ID             `json:"backup_id"`
	Generation               int64                   `json:"generation"`
	CreatedAt                string                  `json:"created_at"`
	DatabaseDigest           contract.Digest         `json:"database_digest"`
	DatabaseSize             int64                   `json:"database_size"`
	DatabaseArchiveEntry     string                  `json:"database_archive_entry"`
	DatabaseSchemaVersions   []manifestSchemaVersion `json:"database_schema_versions"`
	Artifacts                []manifestArtifactEntry `json:"artifacts"`
	Brains                   []manifestBrainEntry    `json:"brains"`
	RetainedObligations      []manifestObligation    `json:"retained_obligations"`
	KeyPrerequisites         []string                `json:"key_prerequisites"`
	SourceRevision           string                  `json:"source_revision"`
	ControllerVersion        string                  `json:"controller_version"`
	RequiredProtocolProfiles []string                `json:"required_protocol_profiles"`
	Paused                   bool                    `json:"paused"`
}

type manifestSchemaVersion struct {
	Owner           string          `json:"owner"`
	Version         int64           `json:"version"`
	MigrationDigest contract.Digest `json:"migration_digest"`
}

type manifestArtifactEntry struct {
	Artifact       wireArtifactRef `json:"artifact"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Encrypted      bool            `json:"encrypted"`
	ArchiveEntry   string          `json:"archive_entry"`
	Pins           []contract.ID   `json:"pins"`
}

type manifestBrainEntry struct {
	Revision             json.RawMessage `json:"revision"`
	ExportArtifact       wireArtifactRef `json:"export_artifact"`
	WriterOwner          string          `json:"writer_owner"`
	AdapterProfileDigest contract.Digest `json:"adapter_profile_digest"`
	KeyPrerequisites     []string        `json:"key_prerequisites"`
}

// manifestObligation is one RecoveryObligation as carried inside a sealed
// manifest or overlay. record_artifact names the obligation's own record
// inside the sealed document (its id) and record_digest hashes that record,
// so the record is included, pinned and encrypted with the document.
type manifestObligation struct {
	ID              contract.ID      `json:"id"`
	Owner           string           `json:"owner"`
	Kind            string           `json:"kind"`
	ResourceID      contract.ID      `json:"resource_id"`
	ResourceVersion contract.Version `json:"resource_version"`
	RecordArtifact  wireArtifactRef  `json:"record_artifact"`
	RecordDigest    contract.Digest  `json:"record_digest"`
	State           string           `json:"state"`
	RecordedAt      string           `json:"recorded_at"`
}

const backupManifestSchema = "zatiti.backup/v1"

// backupCapabilityMissing is the named fault every path that needs the
// consistent-backup capability reports when assembly did not bind it.
func backupCapabilityMissing() error {
	return prerequisiteMissing("no database backup capability is bound: entrypoint assembly must supply " +
		"installation.WithDatabaseBackup(contract.DatabaseBackup); installation.backup and installation.restore are unavailable")
}

func (s *Service) prepareBackup(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[scopeInput](s, opBackup, inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if unit.ReadOnly() {
		return contract.IOPlan{}, invalidInput("operation %s is a mutation and requires a write transaction", opBackup)
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.IOPlan{}, err
	}
	st, err := loadState(ctx, unit)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if st == nil {
		return contract.IOPlan{}, prerequisiteMissing("installation is not initialized")
	}
	// BackupManifest's paused field is a literal const true: a backup can
	// only be taken of a quiesced installation.
	if !st.Paused {
		return contract.IOPlan{}, prerequisiteMissing(
			"installation.backup requires the installation to be paused first")
	}

	job, err := s.executionJobCreate(ctx, unit, executionJobCreateInput{
		Scope: in.Scope, Owner: owner, Operation: opBackup, Input: inv.Input, SourceID: s.deps.IDs.New(),
	})
	if err != nil {
		return contract.IOPlan{}, err
	}
	now := s.deps.Clock.Now()
	if err := insertJob(ctx, unit, jobRow{
		ID: job.ID, Version: int64(job.Version), InstallationID: in.Scope.InstallationID,
		Kind: "backup", State: "pending", Requirements: []wireRequirement{},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return contract.IOPlan{}, err
	}

	manifest, err := s.memoryManifest(ctx, unit, in.Scope)
	if err != nil {
		return contract.IOPlan{}, err
	}
	keyRef, err := loadBackupKeyRef(ctx, unit, in.Scope.InstallationID)
	if err != nil {
		return contract.IOPlan{}, err
	}

	prepared, err := json.Marshal(backupPlan{
		JobID: job.ID, InstallationID: in.Scope.InstallationID, Generation: unit.Generation(),
		BrainRevisions: manifest.BrainRevisions, CreatedAt: formatStamp(now), KeyRef: keyRef,
	})
	if err != nil {
		return contract.IOPlan{}, fmt.Errorf("installation: encode backup plan: %w", err)
	}
	return contract.IOPlan{
		ID: s.deps.IDs.New(), Owner: owner, Invocation: inv, Actor: unit.Actor(),
		Scope: unit.Scope(), Generation: unit.Generation(), Prepared: prepared,
	}, nil
}

func (s *Service) performBackup(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	var p backupPlan
	if err := json.Unmarshal(plan.Prepared, &p); err != nil {
		return contract.IOResult{}, fmt.Errorf("installation: decode backup plan: %w", err)
	}
	if s.deps.Secrets == nil {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing(
			"no secret store is configured; the backup encryption key cannot be custodied"))}, nil
	}
	if s.deps.Blobs == nil {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing(
			"no blob store is configured; the backup bundle cannot be published"))}, nil
	}
	if s.backup == nil {
		return contract.IOResult{Fault: faultOf(backupCapabilityMissing())}, nil
	}
	if p.Generation < 1 {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing(
			"backup requires a started controller generation; the manifest pins generation %d", p.Generation))}, nil
	}
	key, keyRef, _, err := resolveBackupKey(ctx, s.deps.Secrets, p.InstallationID, p.KeyRef)
	if err != nil {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing("backup encryption key unavailable: %v", err))}, nil
	}

	// One consistent image, hashed and counted as it streams. A failure
	// here leaves no manifest: the digest describes only bytes that arrived
	// in full.
	image, imageDigest, imageSize, err := captureImage(ctx, s.backup)
	if err != nil {
		return contract.IOResult{Fault: faultOf(fmt.Errorf("consistent database backup failed: %w", err))}, nil
	}

	sourceRevision, controllerVersion := buildRevision()
	brains := make([]manifestBrainEntry, 0)
	manifest := backupManifestDoc{
		Schema: backupManifestSchema, InstallationID: p.InstallationID, BackupID: p.JobID,
		Generation: p.Generation, CreatedAt: p.CreatedAt,
		DatabaseDigest: imageDigest, DatabaseSize: imageSize, DatabaseArchiveEntry: databaseArchiveEntry,
		// Migration metadata is storage-private; this package cannot read it
		// and lists no schema versions rather than guessing them.
		DatabaseSchemaVersions:   []manifestSchemaVersion{},
		Artifacts:                []manifestArtifactEntry{},
		Brains:                   brains,
		RetainedObligations:      []manifestObligation{},
		KeyPrerequisites:         []string{keyRef},
		SourceRevision:           sourceRevision,
		ControllerVersion:        controllerVersion,
		RequiredProtocolProfiles: []string{},
		Paused:                   true,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return contract.IOResult{}, fmt.Errorf("installation: encode backup manifest: %w", err)
	}
	sealed, err := sealBundle(key, encodeFrame(manifestJSON, image))
	if err != nil {
		return contract.IOResult{Fault: faultOf(err)}, nil
	}
	digest, size, err := stagePublished(ctx, s.deps.Blobs, encodeArtifact(keyRef, sealed))
	if err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("backup bundle could not be published: %v", err))}, nil
	}

	// Verify the whole published bundle before it is ever reported, the way
	// a restore will: read it back, resolve the key by the reference in its
	// own header, decrypt it, and check the framed image against the
	// manifest.
	published, err := readPublished(ctx, s.deps.Blobs, digest, size)
	if err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("published backup bundle could not be read back: %v", err))}, nil
	}
	verified, verifiedImage, err := s.openPublishedBackup(ctx, published, p.InstallationID)
	if err != nil {
		return contract.IOResult{Fault: faultOf(artifactFault("published backup bundle failed verification: %v", err))}, nil
	}
	if verified.BackupID != p.JobID || digestOf(verifiedImage) != imageDigest || int64(len(verifiedImage)) != imageSize {
		return contract.IOResult{Fault: faultOf(artifactFault("published backup bundle does not frame the captured image"))}, nil
	}

	raw, err := json.Marshal(backupPerformed{Digest: digest, Size: size, DatabaseDigest: imageDigest, DatabaseSize: imageSize, KeyRef: keyRef})
	if err != nil {
		return contract.IOResult{}, fmt.Errorf("installation: encode backup result: %w", err)
	}
	return contract.IOResult{Data: raw}, nil
}

// openPublishedBackup resolves the sealing key from a published artifact's
// own clear header and opens the bundle. It is the exact path a restore
// takes, so a backup verifies its restorability, not just its contents.
func (s *Service) openPublishedBackup(ctx context.Context, artifact []byte, installationID contract.ID) (backupManifestDoc, []byte, error) {
	keyRef, sealed, err := decodeArtifact(artifact)
	if err != nil {
		return backupManifestDoc{}, nil, err
	}
	key, err := s.deps.Secrets.Get(ctx, keyRef)
	if err != nil || len(key) != 32 {
		return backupManifestDoc{}, nil, fmt.Errorf("the bundle's key reference does not resolve in this installation's secret store")
	}
	return openBackupBundle(key, sealed, installationID)
}

// openBackupBundle decrypts a sealed bundle, decodes its frame and checks
// the manifest's binding to this installation and its internal
// consistency: schema, paused, and the framed image matching
// database_digest and database_size.
func openBackupBundle(key, sealed []byte, installationID contract.ID) (backupManifestDoc, []byte, error) {
	frame, err := openBundle(key, sealed)
	if err != nil {
		return backupManifestDoc{}, nil, err
	}
	manifestJSON, image, err := decodeFrame(frame)
	if err != nil {
		return backupManifestDoc{}, nil, err
	}
	var manifest backupManifestDoc
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return backupManifestDoc{}, nil, fmt.Errorf("bundle manifest is not valid JSON: %w", err)
	}
	switch {
	case manifest.Schema != backupManifestSchema:
		return backupManifestDoc{}, nil, fmt.Errorf("bundle manifest schema %q is not %s", manifest.Schema, backupManifestSchema)
	case manifest.InstallationID != installationID:
		return backupManifestDoc{}, nil, errBundleForeign
	case !manifest.Paused:
		return backupManifestDoc{}, nil, fmt.Errorf("bundle manifest does not carry the required paused state")
	case manifest.DatabaseArchiveEntry != databaseArchiveEntry:
		return backupManifestDoc{}, nil, fmt.Errorf("bundle manifest names archive entry %q, not %s", manifest.DatabaseArchiveEntry, databaseArchiveEntry)
	case manifest.DatabaseDigest == "" || manifest.DatabaseSize < 1:
		return backupManifestDoc{}, nil, fmt.Errorf("bundle manifest carries no database digest or size")
	case int64(len(image)) != manifest.DatabaseSize || digestOf(image) != manifest.DatabaseDigest:
		return backupManifestDoc{}, nil, fmt.Errorf("framed database image does not match the manifest's digest and size")
	}
	return manifest, image, nil
}

func (s *Service) finishBackup(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	var p backupPlan
	if err := json.Unmarshal(plan.Prepared, &p); err != nil {
		return contract.Payload{}, fmt.Errorf("installation: decode backup plan: %w", err)
	}
	j, err := loadJob(ctx, unit, p.JobID)
	if err != nil {
		return contract.Payload{}, err
	}
	if j == nil {
		return contract.Payload{}, internalError("backup job %s vanished before Finish", p.JobID)
	}
	previous := j.Version
	now := s.deps.Clock.Now()
	if result.Fault != nil {
		j.State = "failed"
		j.Requirements = []wireRequirement{{Code: result.Fault.Code, Message: result.Fault.Message}}
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
		if err := emitTransition(ctx, unit, eventBackupFailed, j.ID, contract.Version(j.Version)); err != nil {
			return contract.Payload{}, err
		}
		return contract.Payload{Status: contract.StatusFailed, Error: result.Fault}, nil
	}

	var perf backupPerformed
	if err := json.Unmarshal(result.Data, &perf); err != nil {
		return contract.Payload{}, fmt.Errorf("installation: decode backup perform result: %w", err)
	}
	// The bundle bytes are already published; only now does its metadata
	// become an artifact of this installation, in the same transaction that
	// records the job.
	artifact, err := s.artifactsPublish(ctx, unit, wireScope{InstallationID: p.InstallationID}, perf.Digest, perf.Size, backupMediaType)
	if err != nil {
		return contract.Payload{}, err
	}
	if perf.KeyRef == "" {
		return contract.Payload{}, internalError("backup perform result carries no key reference")
	}
	if perf.KeyRef != p.KeyRef {
		if err := recordBackupKeyRef(ctx, unit, p.InstallationID, perf.KeyRef, now); err != nil {
			return contract.Payload{}, err
		}
	}
	backup := wireBackup{
		ID: j.ID, Version: 1, Artifact: artifact, InstallationID: p.InstallationID,
		CreatedAt: formatStamp(now), BrainRevisions: p.BrainRevisions,
		KeyPrerequisites: []string{perf.KeyRef}, Verified: true,
	}
	j.State = "succeeded"
	j.Result = mustMarshal(resourceOut[wireBackup]{Resource: backup})
	j.Version++
	j.UpdatedAt = now
	if err := updateJob(ctx, unit, *j, previous); err != nil {
		return contract.Payload{}, err
	}
	if _, err := s.executionJobRecord(ctx, unit, executionJobRecordInput{
		JobID: j.ID, ExpectedVersion: previous, Generation: p.Generation, State: j.State, Result: j.Result,
	}); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventBackupCompleted, j.ID, contract.Version(j.Version)); err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusAccepted, Data: mustMarshal(resourceOut[wireJob]{Resource: j.wire()})}, nil
}

func mustMarshal(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("installation: marshal invariant violated: %v", err))
	}
	return raw
}
