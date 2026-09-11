package execution

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// Durable jobs: inert work intents committed by peers, claimed atomically
// under a generation, and recorded back by the claiming provider. Job
// identity is the source identity plus the canonical input hash; the job
// pipeline never carries executable authority.

// handleJobCreate is the _execution.job.create boundary: validate the owner
// and operation, deduplicate the source identity against the canonical
// input hash and commit the job in the caller's transaction.
func (s *Service) handleJobCreate(ctx context.Context, unit contract.Unit, in jobCreateInput) (contract.Outcome[jobBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	if in.Owner == "" {
		return contract.Outcome[jobBody]{}, invalidInput("job.create requires an owner identity")
	}
	if in.Operation == "" {
		return contract.Outcome[jobBody]{}, invalidInput("job.create requires the originating operation")
	}
	inputJSON, err := canonicalJSON(in.Input)
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	inputHash := sha256Hex(inputJSON)

	// Dedup: one job per source identity; the same identity with the same
	// input replays, with different input conflicts.
	prior, err := findJobBySourceID(ctx, unit, in.SourceID)
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	if prior != nil {
		if prior.InputHash == string(inputHash) {
			return completedOutcome(jobBody{Resource: jobOut(prior)})
		}
		return contract.Outcome[jobBody]{}, conflict(
			"source identity %s is already committed with different input", in.SourceID)
	}

	now := s.now()
	j := &jobRow{
		ID:                s.newID(),
		Version:           1,
		Kind:              "operation",
		State:             "pending",
		InstallationID:    in.Scope.InstallationID,
		OrganizationID:    in.Scope.OrganizationID,
		ProjectID:         in.Scope.ProjectID,
		Scope:             in.Scope,
		Owner:             in.Owner,
		Operation:         in.Operation,
		Input:             inputJSON,
		InputHash:         string(inputHash),
		SourceID:          in.SourceID,
		ClaimedGeneration: 0,
		Requirements:      []wireRequirement{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := insertJob(ctx, unit, j); err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	return completedOutcome(jobBody{Resource: jobOut(j)})
}

// handleJobClaim is the _execution.job.claim boundary: atomically claim one
// current job owner under a generation. A repeat claim by the same
// generation replays the original claim — an abandoned claimed intent is
// recovered by its owner, never blindly redispatched to another.
func (s *Service) handleJobClaim(ctx context.Context, unit contract.Unit, in jobClaimInput) (contract.Outcome[jobClaimBody], error) {
	j, err := loadJob(ctx, unit, in.JobID)
	if err != nil {
		return contract.Outcome[jobClaimBody]{}, err
	}
	if j.InstallationID != installationOf(unit) {
		return contract.Outcome[jobClaimBody]{}, permissionDenied(
			"job %s belongs to another installation", j.ID)
	}

	if j.State == "running" {
		if j.ClaimedGeneration == in.Generation {
			// Lost acknowledgement: the same owner re-claims its own intent.
			return completedOutcome(jobClaimBody{Job: jobOut(j), Input: j.Input})
		}
		return contract.Outcome[jobClaimBody]{}, conflict(
			"job is already claimed by generation %d", j.ClaimedGeneration)
	}
	if terminalJobStates[j.State] {
		return contract.Outcome[jobClaimBody]{}, conflict("job is already %s", j.State)
	}
	if j.Version != in.ExpectedVersion {
		return contract.Outcome[jobClaimBody]{}, staleVersion(
			"job %s version %d does not match expected version %d", j.ID, j.Version, in.ExpectedVersion)
	}

	now := s.now()
	j.State = "running"
	j.ClaimedGeneration = in.Generation
	j.UpdatedAt = now
	if err := updateJob(ctx, unit, j); err != nil {
		return contract.Outcome[jobClaimBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventJobClaimed, j.ID, j.Version); err != nil {
		return contract.Outcome[jobClaimBody]{}, err
	}
	return completedOutcome(jobClaimBody{Job: jobOut(j), Input: j.Input})
}

// handleJobPending is the _execution.job.pending boundary: read pending or
// recoverable local jobs with their original owner and input. This is a
// bounded scan, never a second scheduler.
func (s *Service) handleJobPending(ctx context.Context, unit contract.Unit, in jobPendingInput) (contract.Outcome[jobListBody], error) {
	if in.Limit < 1 || in.Limit > 100 {
		return contract.Outcome[jobListBody]{}, invalidInput("job.pending limit must be between 1 and 100")
	}
	rows, err := listJobs(ctx, unit,
		[]string{"installation_id = ?", "state IN ('pending','outcome_unknown')"},
		[]any{installationOf(unit)}, in.Limit)
	if err != nil {
		return contract.Outcome[jobListBody]{}, err
	}
	items := make([]wireJob, 0, len(rows))
	for _, j := range rows {
		items = append(items, jobOut(j))
	}
	return completedOutcome(jobListBody{Items: items})
}

// handleJobRecord is the _execution.job.record boundary: record the real
// result of the originating operation. Only the current claim holder may
// record, and unknown-effect evidence is preserved as obligations.
func (s *Service) handleJobRecord(ctx context.Context, unit contract.Unit, in jobRecordInput) (contract.Outcome[jobBody], error) {
	j, err := loadJobForUpdate(ctx, unit, in.JobID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	if j.ClaimedGeneration != in.Generation {
		return contract.Outcome[jobBody]{}, conflict(
			"job claim generation %d does not match the current owner %d",
			in.Generation, j.ClaimedGeneration)
	}
	if terminalJobStates[j.State] {
		return contract.Outcome[jobBody]{}, conflict("job is already %s", j.State)
	}

	now := s.now()
	j.State = in.State
	j.Result = in.Result
	j.UpdatedAt = now
	if err := updateJob(ctx, unit, j); err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	// Preserve the recorded evidence identities in the event so the audit
	// trail links the result to its backing records.
	evidence := in.EvidenceIDs
	if evidence == nil {
		evidence = []contract.ID{}
	}
	data, err := canonicalJSON(struct {
		State       string        `json:"state"`
		EvidenceIDs []contract.ID `json:"evidence_ids"`
	}{in.State, evidence})
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	if err := unit.Emit(ctx, contract.Event{
		Kind:            eventJobRecorded,
		ResourceID:      j.ID,
		ResourceVersion: j.Version,
		Data:            data,
	}); err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	return completedOutcome(jobBody{Resource: jobOut(j)})
}

// handleJobGet is the job.get boundary.
func (s *Service) handleJobGet(ctx context.Context, unit contract.Unit, in getIDInput) (contract.Outcome[jobBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	j, err := loadJob(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	if err := narrowScope(in.Scope, j.OrganizationID, j.ProjectID, "", ""); err != nil {
		return contract.Outcome[jobBody]{}, err
	}
	return completedOutcome(jobBody{Resource: jobOut(j)})
}
