package configuration

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Revision 3's export job ledger seam (P00-008; contract-proposals.md
// "Job registry linkage"). organization.export/team.export/project.export
// and the internal _configuration.export.prepare/_configuration.export.record
// pair replace the prior handler-local ID minting -- a synchronously
// "succeeded" Job/Artifact fabricated inside the query transaction, with no
// owner-backed lookup behind either ID -- with a two-phase durable flow:
//
//  1. buildExportJob seals the canonical export bundle and registers a
//     durable job through _execution.job.create (an owner-backed lookup: the
//     job id comes back from execution's own ledger, never s.ids.New()
//     inside this transaction). No blob IO happens here.
//  2. Service.RunJob performs the bounded IO outside any transaction: it
//     stages and publishes the canonical bundle bytes sealed at step 1 and
//     mints the artifact id.
//  3. _configuration.export.record durably finalizes this owner's local
//     record of the job once the caller (the controller, per its caller
//     allowlist) has the resulting artifact reference.
//
// configuration_export_jobs is this owner's own durable bookkeeping of that
// lineage -- job id, family, resource id and (once recorded) artifact
// reference -- so an export's result and its underlying bundle definitions
// stay honestly inspectable and durable across restart from this package's
// own tables, independent of execution's and artifacts' owned tables.

// exportJobInput is the bounded JSON _execution.job.create's input field
// carries for one export job: the resource identity for traceability plus
// the canonical bundle bytes RunJob stages and publishes. Embedding the
// bundle here (rather than re-deriving it from configuration's own tables
// later) is deliberate -- RunJob runs outside any transaction and has no
// Unit to query with.
type exportJobInput struct {
	Family     string          `json:"family"`
	ResourceID contract.ID     `json:"resource_id"`
	Bundle     json.RawMessage `json:"bundle"`
}

// executionJobCreateIn is the wire input of _execution.job.create.
type executionJobCreateIn struct {
	Scope       wireScope       `json:"scope"`
	Owner       string          `json:"owner"`
	Operation   string          `json:"operation"`
	Input       json.RawMessage `json:"input"`
	SourceID    contract.ID     `json:"source_id"`
	OperationID contract.ID     `json:"operation_id,omitempty"`
}

// exportPrepareIn is the wire input of _configuration.export.prepare.
type exportPrepareIn struct {
	Scope      wireScope   `json:"scope"`
	Family     string      `json:"family"`
	ResourceID contract.ID `json:"resource_id"`
}

// exportRecordIn is the wire input of _configuration.export.record.
type exportRecordIn struct {
	JobID           contract.ID     `json:"job_id"`
	ExpectedVersion int64           `json:"expected_version"`
	Generation      int64           `json:"generation"`
	Artifact        wireArtifactRef `json:"artifact"`
}

// buildExportJob assembles the zatiti.organization/v1 bundle for one
// resource, seals it as canonical JSON, and registers a durable job through
// the execution job ledger -- never inventing a job id locally. It performs
// no blob IO: bytes stage and publish later, outside this transaction, when
// the job is claimed and run (RunJob). Shared by the public
// organization.export/team.export/project.export handlers and the internal
// _configuration.export.prepare.
func (s *Service) buildExportJob(ctx context.Context, unit contract.Unit, family string, scope wireScope, resourceID contract.ID) (wireJob, error) {
	bundle := &exportBundle{Format: exportFormat}
	install := scope.InstallationID
	switch family {
	case kindOrganization:
		row, err := fetchOrgByID(ctx, unit, install, resourceID)
		if err != nil {
			return wireJob{}, faultOf(err)
		}
		if row == nil {
			return wireJob{}, notFound("organization %s not found", resourceID)
		}
		bundle.Organization = ptrOf(orgDef(row))
		if err := s.exportOrganizationScope(ctx, unit, install, resourceID, bundle); err != nil {
			return wireJob{}, err
		}
	case kindTeam:
		row, err := fetchTeamByID(ctx, unit, install, resourceID)
		if err != nil {
			return wireJob{}, faultOf(err)
		}
		if row == nil {
			return wireJob{}, notFound("team %s not found", resourceID)
		}
		bundle.Teams = []wireTeam{teamDef(row)}
	case kindProject:
		row, err := fetchProjectByID(ctx, unit, install, resourceID)
		if err != nil {
			return wireJob{}, faultOf(err)
		}
		if row == nil {
			return wireJob{}, notFound("project %s not found", resourceID)
		}
		bundle.Projects = []wireProject{projectDef(row)}
	default:
		return wireJob{}, invalidInput("unsupported export family %s", family)
	}

	raw, err := json.Marshal(bundle)
	if err != nil {
		return wireJob{}, internalError("export bundle encoding failed")
	}
	canon, err := contract.Canonicalize(raw)
	if err != nil {
		return wireJob{}, invalidInput("export bundle is not canonicalizable: %v", err)
	}
	jobInput, err := json.Marshal(exportJobInput{Family: family, ResourceID: resourceID, Bundle: json.RawMessage(canon)})
	if err != nil {
		return wireJob{}, internalError("export job input encoding failed")
	}

	created, err := s.callOwner(ctx, unit, "_execution.job.create", executionJobCreateIn{
		Scope: scope, Owner: ownerName, Operation: family + ".export",
		Input: jobInput, SourceID: s.ids.New(),
	})
	if err != nil {
		return wireJob{}, err
	}
	var body struct {
		Resource wireJob `json:"resource"`
	}
	if err := contract.DecodeStrict(created, &body); err != nil {
		return wireJob{}, faultWrap(internalError(
			"_execution.job.create returned a result that is not the frozen {\"resource\": Job} shape: %v", err), err)
	}
	job := body.Resource

	now := s.clock.Now()
	if err := insertExportJob(ctx, unit, &exportJobRow{
		JobID: job.ID, Version: 1, InstallationID: install,
		Family: family, ResourceID: resourceID, State: "pending",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return wireJob{}, faultOf(err)
	}
	return job, nil
}

// RunJob implements contract.LocalJobRunner for configuration-owned durable
// jobs (currently: organization/team/project export). It performs exactly
// the bounded IO buildExportJob deferred past its owning transaction --
// staging and publishing the canonical bundle bytes sealed at admission
// time -- and nothing else: no domain writes happen here. The caller
// commits the resulting job/artifact linkage through
// _configuration.export.record once this returns, matching the shared
// LocalJobRunner contract ("owners retain their domain rows and receive
// idempotent typed completion callbacks through JobOutcome").
func (s *Service) RunJob(ctx context.Context, work contract.JobWork) (contract.JobOutcome, error) {
	if work.Owner != ownerName {
		return contract.JobOutcome{}, invalidInput("configuration cannot run a job owned by %q", work.Owner)
	}
	if s.blobs == nil {
		return contract.JobOutcome{}, internalError("export job run requires a blob store")
	}
	var in exportJobInput
	if err := contract.DecodeStrict(work.Input, &in); err != nil {
		return contract.JobOutcome{}, invalidInput("export job input is not decodable: %v", err)
	}
	canon, err := contract.Canonicalize(in.Bundle)
	if err != nil {
		return contract.JobOutcome{}, invalidInput("export bundle is not canonicalizable: %v", err)
	}
	stagingRef, digest, size, err := s.blobs.Stage(ctx, bytes.NewReader(canon), int64(len(canon)))
	if err != nil {
		return contract.JobOutcome{}, err
	}
	if err := s.blobs.Publish(ctx, stagingRef, digest); err != nil {
		return contract.JobOutcome{}, err
	}
	artifactID := s.ids.New()
	artifact := wireArtifact{
		ID: artifactID, Version: 1, Scope: scopeFromContract(work.Scope),
		Digest: digest, Size: size, MediaType: "application/json",
		Classification: "internal", Encrypted: true, State: "available",
		CreatedAt: s.clock.Now(),
	}
	result, err := json.Marshal(map[string]any{"resource": artifact})
	if err != nil {
		return contract.JobOutcome{}, internalError("export job result encoding failed")
	}
	return contract.JobOutcome{
		State:       "succeeded",
		Result:      result,
		EvidenceIDs: []contract.ID{artifactID},
	}, nil
}

// handleExportPrepare implements _configuration.export.prepare: the same
// durable-job registration buildExportJob gives the public export
// operations, exposed as an internal operation for the controller/
// application to drive an export directly (for example, a scheduled backup
// path) without going through a public mutation's submission-key envelope.
func handleExportPrepare(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[exportPrepareIn](s, "_configuration.export.prepare", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	if s.blobs == nil {
		return contract.Payload{}, internalError("export requires a blob store")
	}
	job, err := s.buildExportJob(ctx, unit, in.Family, in.Scope, in.ResourceID)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.accepted(map[string]any{"resource": job})
}

// handleExportRecord implements _configuration.export.record: durably
// finalizes this owner's local export-job record with the artifact
// reference the caller (the controller) obtained after RunJob staged and
// published the canonical bytes outside any transaction. generation fences
// the call to the controller epoch that prepared the job -- a call carrying
// a different generation names a stale controller, never proof the job
// itself failed. expected_version is optimistic concurrency over this
// owner's own local row, not over execution's job ledger, which this
// package has no port to read.
func handleExportRecord(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[exportRecordIn](s, "_configuration.export.record", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.Generation != unit.Generation() {
		return contract.Payload{}, staleVersion(
			"export job %s was prepared under controller generation %d, current generation is %d",
			in.JobID, in.Generation, unit.Generation())
	}
	install := unit.Scope().InstallationID
	row, err := fetchExportJobByID(ctx, unit, install, in.JobID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if row == nil {
		return contract.Payload{}, notFound("export job %s not found", in.JobID)
	}
	if row.State == "succeeded" {
		if row.ArtifactID == in.Artifact.ID && row.ArtifactDigest == string(in.Artifact.Digest) {
			return s.completed(map[string]any{"resource": exportJobDef(row)})
		}
		return contract.Payload{}, staleVersion("export job %s already recorded a different artifact", in.JobID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("export job %s is at version %d", in.JobID, row.Version)
	}
	row.Version++
	row.State = "succeeded"
	row.ArtifactID = in.Artifact.ID
	row.ArtifactDigest = string(in.Artifact.Digest)
	row.UpdatedAt = s.clock.Now()
	if err := writeExportJob(ctx, unit, row); err != nil {
		return contract.Payload{}, faultOf(err)
	}
	return s.completed(map[string]any{"resource": exportJobDef(row)})
}

// exportJobDef converts a durable local export-job record to its wire Job.
func exportJobDef(row *exportJobRow) wireJob {
	job := wireJob{
		ID: row.JobID, Version: row.Version, Kind: "local", State: row.State,
		Requirements: []wireRequirement{}, Owner: ownerName, Operation: row.Family + ".export",
	}
	if row.ArtifactID != "" {
		job.ResultArtifact = &wireArtifactRef{ID: row.ArtifactID, Digest: contract.Digest(row.ArtifactDigest)}
	}
	return job
}
