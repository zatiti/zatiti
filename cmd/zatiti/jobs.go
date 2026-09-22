package main

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
)

// jobKind names one durable local job's owner and exact operation -- the
// Collaborators.Jobs key (controller.JobKey(owner, operation)) a completed
// job actually commits under. This is NOT always the public catalog
// operation ID: execution's document-publish jobs (run_export,
// claim_context) are internal job-kind names distinct from the public
// run.export/run.claim operations that enqueue them
// (internal/execution/job_runner.go:14-40, :81-82, :183-192's
// terminalJobEvent), so this table is curated from each owner's actual
// RunJob/job-creation source rather than derived generically from
// contract.Descriptor.CompletionSchema: a generic walk would misname
// execution's two job kinds (their descriptor IDs are "run.export"/
// "run.claim", not "run_export"/"claim_context") and falsely report them
// missing.
type jobKind struct {
	Owner     string
	Operation string
}

func (k jobKind) key() string { return controller.JobKey(k.Owner, k.Operation) }

// landedJobKinds are every owner/operation this tree's domain packages
// actually implement contract.LocalJobRunner for, confirmed by reading
// each RunJob directly:
//   - internal/execution/job_runner.go:130 handles "run_export" and
//     "claim_context" under owner "execution" (job_runner.go:81-82 sets
//     Owner: ownerName ("execution"), Operation: kind).
//   - internal/skills/jobs.go:228 handles "skill.evaluate" only, under
//     owner "skills" (jobs.go:230-235 refuses any other owner/operation).
//   - internal/configuration/jobs.go:166 handles the three export
//     families under owner "configuration": "organization.export",
//     "team.export", "project.export" (dto.go:302-304's kindOrganization/
//     kindTeam/kindProject, jobs.go:131's family+".export").
var landedJobKinds = []jobKind{
	{Owner: "execution", Operation: "run_export"},
	{Owner: "execution", Operation: "claim_context"},
	{Owner: "skills", Operation: "skill.evaluate"},
	{Owner: "configuration", Operation: "organization.export"},
	{Owner: "configuration", Operation: "team.export"},
	{Owner: "configuration", Operation: "project.export"},
}

// catalogJobKinds is landedJobKinds plus every other owner/operation the
// public operation catalog declares as job-backed with NO landed runner on
// this tree, confirmed the same way (reading the admitting handler, not
// merely a non-nil CompletionSchema):
//   - internal/artifacts/localio.go:749-750 commits a durable job (Owner:
//     ownerName ("artifacts"), Operation: opArtifactExport
//     ("artifact.export")) through the shared _execution.job.create seam
//     (internal/artifacts/peer.go:61) for the public artifact.export
//     operation. internal/artifacts has no RunJob (contract.LocalJobRunner)
//     implementation on this tree (confirmed by grep: zero `func.*RunJob`
//     matches in internal/artifacts). This is a real, verified prerequisite
//     gap -- not invented -- and is exactly the scenario P24's required
//     test 2 ("a catalog-supported job kind lacking an attached runner
//     fails an assembly test") proves is detected, never silently dropped.
//
// internal/policy implements no contract.LocalJobRunner on this tree
// either (confirmed by grep), despite the P24 card's briefing assuming
// otherwise; it names no job-backed public operation, so it contributes no
// entry here.
var catalogJobKinds = []jobKind{
	{Owner: "execution", Operation: "run_export"},
	{Owner: "execution", Operation: "claim_context"},
	{Owner: "skills", Operation: "skill.evaluate"},
	{Owner: "configuration", Operation: "organization.export"},
	{Owner: "configuration", Operation: "team.export"},
	{Owner: "configuration", Operation: "project.export"},
	{Owner: "artifacts", Operation: "artifact.export"},
}

// missingJobRunners reports every catalogJobKinds entry absent from
// attached. Never silently omitted: production startup reporting
// (assemblyReadiness) and TestMissingJobRunnersDetectsTheKnownCatalogGap
// both walk this instead of discovering the gap only when a pending job
// never runs (P24 item 4 / required test 2).
func missingJobRunners(attached map[string]controller.JobRunner) []jobKind {
	var missing []jobKind
	for _, k := range catalogJobKinds {
		if _, ok := attached[k.key()]; !ok {
			missing = append(missing, k)
		}
	}
	return missing
}

// localJobShim adapts one owner's contract.LocalJobRunner (the frozen
// domain seam every job-owning package implements: RunJob(ctx,
// contract.JobWork) (contract.JobOutcome, error)) to controller.JobRunner
// (RunJob(ctx, controller.Job) (controller.JobOutcome, error)) --
// Collaborators.Jobs' required shape. These are genuinely different Go
// types with no existing bridge in internal/controller (verified: no
// contract.JobWork{ construction anywhere outside tests in the whole
// tree). cmd/zatiti is the only allowed-write package positioned to
// bridge them, since Collaborators is assembled here.
//
// Version, Generation and Scope are zero in the contract.JobWork this shim
// constructs. controller.Job (internal/controller/jobs.go:49-55) carries
// none of the three: the wire Job the controller reads back from
// _execution.job.pending/.claim (internal/execution/dto.go:154-165's
// wireJob) never exposes them, so the controller has nothing to hand down
// even if this shim wanted it. Verified safe for every landedJobKinds
// runner by reading each RunJob directly: none reads work.Version or
// work.Generation; only configuration's RunJob reads work.Scope
// (internal/configuration/jobs.go:190, to stamp its own intermediate
// result artifact's scope field), and that field is discarded downstream
// -- the controller's owner-specific finish call
// (_configuration.export.record) reads only the result's artifact id and
// digest (internal/configuration/jobs.go:65-70's exportRecordIn{Artifact
// wireArtifactRef}), never its scope. If a future job owner's RunJob comes
// to depend on Version/Generation/Scope, this shim's zero-value defaults
// would silently misbehave for it -- flagged in the P24 handoff as a
// prerequisite gap for internal/controller/internal/contract to close by
// carrying them on controller.Job, rather than invented here.
type localJobShim struct {
	runner contract.LocalJobRunner
}

// RunJob implements controller.JobRunner.
func (s localJobShim) RunJob(ctx context.Context, job controller.Job) (controller.JobOutcome, error) {
	outcome, err := s.runner.RunJob(ctx, contract.JobWork{
		ID: job.ID, Owner: job.Owner, Operation: job.Operation, Input: job.Input,
	})
	if err != nil {
		return controller.JobOutcome{}, err
	}
	reqs := make([]controller.Requirement, 0, len(outcome.Requirements))
	for _, r := range outcome.Requirements {
		reqs = append(reqs, controller.Requirement{
			Code: r.Code, Message: r.Message,
			ResourceID:  idOrEmpty(r.ResourceID),
			ChallengeID: idOrEmpty(r.ChallengeID),
		})
	}
	return controller.JobOutcome{
		State: outcome.State, Result: outcome.Result,
		EvidenceIDs: outcome.EvidenceIDs, Requirements: reqs,
	}, nil
}

// idOrEmpty converts contract.Requirement's optional pointer identity
// (nil when absent) to controller.Requirement's empty-string convention.
func idOrEmpty(id *contract.ID) contract.ID {
	if id == nil {
		return ""
	}
	return *id
}

// buildJobRunners attaches a localJobShim for every landedJobKinds entry
// whose owner is present in owners (keyed by module/owner name, exactly
// contract.Module.Name() -- see modules() in assembly.go). A
// landedJobKinds entry naming an owner modules() never constructed, or one
// that does not implement contract.LocalJobRunner, is silently skipped
// here (never claimed) and would be caught by
// TestBuildJobRunnersAttachesEveryLandedKind failing, not by a production
// panic -- consistent with "missing collaborator stays inspectable,
// never guessed at".
func buildJobRunners(owners map[string]contract.LocalJobRunner) map[string]controller.JobRunner {
	jobs := make(map[string]controller.JobRunner, len(landedJobKinds))
	for _, k := range landedJobKinds {
		runner, ok := owners[k.Owner]
		if !ok {
			continue
		}
		jobs[k.key()] = localJobShim{runner: runner}
	}
	return jobs
}
