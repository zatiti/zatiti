package installation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// installation.backup. BackupManifest (schema zatiti.backup/v1, embedded in
// this package's AGENTS.md $defs) requires database_digest, database_size
// and a literal paused:true. This package has no way to obtain a consistent
// point-in-time digest of its own SQLite database: contract.Dependencies
// (internal/contract/module.go:57-63) carries no contract.Database handle,
// and none of the outgoing calls this package is allowlisted for produces
// one either (contract.Database.Backup exists only on the interface storage
// hands to the entrypoint/controller, never to a domain module). Perform
// does every other real, real piece of the pipeline -- resolving or minting
// the installation's backup encryption key, gathering brain revisions and
// pending obligations -- and then returns a named prerequisite_missing
// fault at the exact point the database digest would be required, rather
// than fabricating one. See doc.go for the full account and the final
// implementation report for the exact contract question this raises.

// backupPlan is Prepare's sealed decision for installation.backup.
type backupPlan struct {
	JobID          contract.ID `json:"job_id"`
	InstallationID contract.ID `json:"installation_id"`
	Generation     int64       `json:"generation"`
	BrainRevisions []wireRef   `json:"brain_revisions"`
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
	if err := insertJob(ctx, unit, jobRow{
		ID: job.ID, Version: int64(job.Version), InstallationID: in.Scope.InstallationID,
		Kind: "backup", State: "pending", Requirements: []wireRequirement{},
		CreatedAt: s.deps.Clock.Now(), UpdatedAt: s.deps.Clock.Now(),
	}); err != nil {
		return contract.IOPlan{}, err
	}

	manifest, err := s.memoryManifest(ctx, unit, in.Scope)
	if err != nil {
		return contract.IOPlan{}, err
	}

	prepared, err := json.Marshal(backupPlan{
		JobID: job.ID, InstallationID: in.Scope.InstallationID, Generation: unit.Generation(),
		BrainRevisions: manifest.BrainRevisions,
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
	if _, err := ensureBackupKey(ctx, s.deps.Secrets, p.InstallationID); err != nil {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing("backup encryption key unavailable: %v", err))}, nil
	}
	// Every other manifest ingredient this package can reach (brain
	// revisions, pending obligations) was already gathered through Ports in
	// Prepare's transaction where peer calls belong; the one remaining
	// ingredient -- a consistent digest of this installation's own SQLite
	// database -- has no seam at all (see the file doc comment). Report it
	// honestly instead of fabricating a digest BackupManifest would then
	// treat as verified.
	return contract.IOResult{Fault: faultOf(prerequisiteMissing(
		"no database backup capability is injected into this module; " +
			"contract.Dependencies exposes no seam for a consistent SQLite backup digest"))}, nil
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

	// performBackup never returns a faultless result today: the missing
	// database-bytes seam (see the file doc comment) always fires first.
	// This branch stays real and complete -- building the actual Backup
	// resource from the plan's gathered brain revisions and the published
	// bundle Perform would return, and storing it as the job's result exactly
	// as installation.job.get's eventual result schema declares -- so the
	// moment that seam is wired, success recording needs no rework here. The
	// operation's own OutputSchema is always {resource: Job}; the Backup
	// resource itself only ever surfaces through job.get.
	var perf struct {
		Artifact wireArtifactRef `json:"artifact"`
	}
	if err := json.Unmarshal(result.Data, &perf); err != nil {
		return contract.Payload{}, fmt.Errorf("installation: decode backup perform result: %w", err)
	}
	backup := wireBackup{
		ID: j.ID, Version: 1, Artifact: perf.Artifact, InstallationID: p.InstallationID,
		CreatedAt: formatStamp(now), BrainRevisions: p.BrainRevisions,
		KeyPrerequisites: []string{backupKeyRef(p.InstallationID)}, Verified: true,
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
	return contract.Payload{Status: contract.StatusAccepted, Data: mustMarshal(resourceOut[wireJob]{Resource: j.wire()})}, nil
}

func mustMarshal(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("installation: marshal invariant violated: %v", err))
	}
	return raw
}
