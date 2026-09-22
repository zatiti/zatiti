package controller

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

const (
	// restoreOwner/restoreOperation identify installation.restore's durable
	// job (execution_jobs owner/operation), which jobs() below never hands
	// to an ordinary JobRunner and settleJob never finishes: it is driven
	// exclusively by restore.go's own handoff (restoreWork/runRestore/
	// settleRestore), because database replacement cannot run as a runner
	// invoked while this same application/database handle stays open.
	restoreOwner     = "installation"
	restoreOperation = "installation.restore"

	// Owner-specific finish calls this package knows how to drive after the
	// generic _execution.job.record commit, by exact owner/operation
	// (P23 item 3: "register supported jobs by exact owner/operation").
	configurationOwner = "configuration"
	skillsOwner        = "skills"
	skillEvaluateOp    = "skill.evaluate"
)

// ResumableJobRunner is implemented by a JobRunner whose RunJob is safe to
// invoke again, unmodified, from the exact same claimed input after a crash
// leaves that claim's outcome ambiguous -- because it performs no
// unconfirmable external side effect a second call could duplicate (a pure
// read-and-compute local check, for example). A JobRunner that does not
// implement this is always recovered as outcome_unknown and never
// re-invoked: the safe default for anything that might already have taken
// an unconfirmable physical effect.
type ResumableJobRunner interface {
	JobRunner
	// ResumableAfterAmbiguousClaim reports whether this exact runner may be
	// invoked again for a job whose claim is durably confirmed but whose
	// outcome never became durable (P23 required test: "either records it
	// unknown or safely resumes according to its mode").
	ResumableAfterAmbiguousClaim() bool
}

// runnerResumable reports whether runner opts into resumption after an
// ambiguous claim. A nil runner (no attached JobRunner for this kind) is
// never resumable.
func runnerResumable(runner JobRunner) bool {
	r, ok := runner.(ResumableJobRunner)
	return ok && r.ResumableAfterAmbiguousClaim()
}

// Job is one claimed durable job handed to its runner: the original owner,
// operation and inert input the owner committed.
type Job struct {
	ID        contract.ID
	Kind      string
	Owner     string
	Operation string
	Input     json.RawMessage
}

// JobOutcome is what a runner established. State is a terminal job state;
// Result conforms to the originating operation's completion schema.
type JobOutcome struct {
	State        string          `json:"state"`
	Result       json.RawMessage `json:"result,omitempty"`
	EvidenceIDs  []contract.ID   `json:"evidence_ids,omitempty"`
	Requirements []Requirement   `json:"requirements,omitempty"`
}

// JobRunner executes one kind of durable local job outside any transaction.
// It returns an outcome for everything it established, including definite
// failure; an error means the runner cannot say what happened and the job is
// kept as outcome_unknown. A runner is never invoked twice for one claim.
type JobRunner interface {
	RunJob(ctx context.Context, job Job) (JobOutcome, error)
}

// JobKey is the Collaborators.Jobs key of one job kind.
func JobKey(owner, operation string) string { return owner + "/" + operation }

// jobs drives pending durable jobs and returns the network jobs that wait on
// an effects operation, indexed by that operation, for callback routing.
//
// A job is claimed only when a runner for its exact kind is attached. A job
// the controller cannot run is left pending and reported, never claimed:
// claiming it would take it away from a synchronous local IO call that may
// still be finishing it, and would strand it in running for nothing.
func (c *Controller) jobs(ctx, workCtx context.Context, sess *session) map[contract.ID]wireJob {
	waiting := map[contract.ID]wireJob{}
	var pending jobsOutput
	if err := c.call(ctx, sess, "_execution.job.pending", limitInput{Limit: c.batch()}, &pending); err != nil {
		c.note(err)
		return waiting
	}
	journaled := map[contract.ID]struct{}{}
	for _, e := range sess.journal.snapshot() {
		if e.Kind == kindJob && e.open() {
			journaled[e.JobID] = struct{}{}
		}
	}
	c.mu.Lock()
	runners := c.deps.Jobs
	c.mu.Unlock()
	for _, job := range pending.Items {
		if job.OperationID != "" {
			waiting[job.OperationID] = job
			continue
		}
		if job.Owner == restoreOwner && job.Operation == restoreOperation {
			// Never an ordinary JobRunner claim: database replacement is an
			// exclusive handoff (restore.go, restoreWork), not a runner
			// invoked while this same application/database handle stays
			// open and serving other work.
			continue
		}
		if job.State != jobStatePending {
			// outcome_unknown is retained for its owner; it is never rerun.
			continue
		}
		if _, ok := journaled[job.ID]; ok {
			continue
		}
		runner, ok := runners[JobKey(job.Owner, job.Operation)]
		if !ok {
			c.oblige(obligationJob, job.ID, prerequisiteMissing(
				"no job runner is attached for %s; job %s stays pending and unclaimed",
				JobKey(job.Owner, job.Operation), job.ID))
			continue
		}
		if !c.admitting() || !c.acquire() {
			return waiting
		}
		if !c.claimJob(ctx, workCtx, sess, job, runner) {
			c.free()
		}
	}
	return waiting
}

func (c *Controller) pendingJobIndex(ctx context.Context, sess *session) map[contract.ID]wireJob {
	var pending jobsOutput
	if err := c.call(ctx, sess, "_execution.job.pending", limitInput{Limit: MaxBatch}, &pending); err != nil {
		c.note(err)
		return nil
	}
	index := make(map[contract.ID]wireJob, len(pending.Items))
	for _, job := range pending.Items {
		index[job.ID] = job
	}
	return index
}

// claimJob claims generation-bound ownership of one job and starts its
// runner.
func (c *Controller) claimJob(ctx, workCtx context.Context, sess *session, job wireJob, runner JobRunner) bool {
	e := entry{
		ID:         string(contract.NewID()),
		Kind:       kindJob,
		Phase:      phaseAdmitted,
		Generation: sess.generation,
		JobID:      job.ID,
		JobVersion: job.Version,
		JobOwner:   job.Owner,
		JobOp:      job.Operation,
	}
	if !c.journal(sess, e) {
		return false
	}
	claim := jobClaimInput{JobID: job.ID, ExpectedVersion: job.Version, Generation: sess.generation}
	var claimed jobClaimOutput
	err := c.write(func() error { return c.call(ctx, sess, "_execution.job.claim", claim, &claimed) })
	if err != nil && !isFault(err) {
		// The acknowledgement may be lost. The owner replays a claim of the
		// same generation, so asking again is safe and tells us the truth.
		err = c.write(func() error { return c.call(ctx, sess, "_execution.job.claim", claim, &claimed) })
	}
	if err != nil {
		c.note(err)
		if isFault(err) {
			e.Phase = phaseDone
			c.journal(sess, e)
		}
		return false
	}
	e.Phase = phaseClaimed
	e.JobVersion = claimed.Job.Version
	e.JobInput = claimed.Input
	if !c.journal(sess, e) {
		return false
	}
	c.resolve(obligationJob, job.ID)
	c.hold(e.ID)
	c.workers.Add(1)
	go c.runJob(workCtx, sess, e, runner, Job{
		ID: job.ID, Kind: claimed.Job.Kind, Owner: job.Owner, Operation: job.Operation, Input: claimed.Input,
	})
	return true
}

func (c *Controller) runJob(workCtx context.Context, sess *session, e entry, runner JobRunner, job Job) {
	defer c.workers.Done()
	defer c.free()
	defer c.release(e.ID)

	outcome := runOnce(workCtx, runner, job)
	e.Outcome = &outcome
	e.Phase = phaseObserved
	if !c.journal(sess, e) {
		return
	}
	c.settleJob(workCtx, sess, e)
}

// runOnce invokes the runner and normalizes what it returned. Anything short
// of an explicit terminal outcome is unknown.
func runOnce(ctx context.Context, runner JobRunner, job Job) (outcome JobOutcome) {
	defer func() {
		if recover() != nil {
			outcome = unknownJob("the job runner panicked")
		}
	}()
	got, err := runner.RunJob(ctx, job)
	if err != nil {
		return unknownJob("the job runner returned an error instead of an outcome")
	}
	switch got.State {
	case jobStateSucceeded, jobStateFailed, jobStateOutcomeUnknown, jobStateCancelled:
	default:
		return unknownJob("the job runner returned a non-terminal state")
	}
	if !jsonObject(got.Result) {
		got.Result = json.RawMessage(`{}`)
	}
	return got
}

func unknownJob(message string) JobOutcome {
	return JobOutcome{
		State:        jobStateOutcomeUnknown,
		Result:       json.RawMessage(`{}`),
		Requirements: []Requirement{{Code: contract.CodeOutcomeUnknown, Message: message}},
	}
}

// settleJobClaim resolves a job claim whose commit was never durably
// acknowledged locally before the controller that attempted it died: the
// entry is at phaseAdmitted, carrying the job's pre-claim expected version
// and the generation that was attempting the claim, but never reached the
// local phaseClaimed write that would prove the claim itself committed.
//
// Re-scanning the bounded pending list alone cannot resolve this: a job
// that DID get claimed by definition no longer shows up there, which is
// exactly the ambiguous case that needs resolving, not the one the old scan
// could already answer. Instead this calls _execution.job.claim again with
// the EXACT (job_id, expected_version, generation) triple the original
// attempt used -- the same "durable claim lookup" claimJob's own retry
// already relies on for a lost in-process acknowledgement ("the owner
// replays a claim of the same generation, so asking again is safe and
// tells us the truth"), now reused across a restart because the journal,
// not memory, is what carries that triple forward. Two outcomes:
//
//   - The call still succeeds: the exact claim is now durably confirmed,
//     whether it is a genuinely fresh claim (this call itself is the first
//     one that ever committed, since the job's row was still pending at
//     the recorded expected_version) or an idempotent replay of a claim an
//     earlier, now-dead process actually made. The two cannot be told
//     apart from the response alone, so the entry advances to phaseClaimed
//     with the confirmed post-claim version and input durably journaled;
//     resumeClaimedJobs (the ordinary tick flow, never settle itself)
//     decides from there whether to record it unknown or safely resume
//     it, per the attached runner's declared mode.
//   - The call fails: the exact claim cannot be resolved this way (for
//     example the job moved on for an unrelated reason). The bounded
//     pending scan is consulted as a fallback of last resort -- if the job
//     is still visibly pending, the original claim attempt provably never
//     reached the owner and the entry simply finishes, letting the
//     ordinary jobs() scan reclaim it fresh. Otherwise the claim is
//     retained as a stranded, reported obligation rather than guessed at:
//     never silently dropped.
func (c *Controller) settleJobClaim(ctx context.Context, sess *session, e entry, pending map[contract.ID]wireJob) {
	claim := jobClaimInput{JobID: e.JobID, ExpectedVersion: e.JobVersion, Generation: e.Generation}
	var claimed jobClaimOutput
	err := c.write(func() error { return c.call(ctx, sess, "_execution.job.claim", claim, &claimed) })
	if err == nil {
		e.Phase = phaseClaimed
		e.JobVersion = claimed.Job.Version
		e.JobInput = claimed.Input
		if !c.journal(sess, e) {
			return
		}
		c.resolve(obligationJob, e.JobID)
		return
	}
	c.note(err)
	if job, ok := pending[e.JobID]; ok && job.State == jobStatePending {
		e.Phase = phaseDone
		c.journal(sess, e)
		return
	}
	f := prerequisiteMissing(
		"claim of job %s could not be resolved by direct lookup (%s) and it no longer appears pending in this bounded scan; its claimed version cannot be established",
		e.JobID, faultOf(err).Code)
	e.Phase = phaseStranded
	e.Fault = f
	if c.journal(sess, e) {
		c.oblige(obligationJob, e.JobID, f)
	}
}

// jobResumable reports whether the runner currently attached for a
// journaled job entry's exact owner/operation declares itself resumable.
func (c *Controller) jobResumable(e entry) bool {
	c.mu.Lock()
	runner := c.deps.Jobs[JobKey(e.JobOwner, e.JobOp)]
	c.mu.Unlock()
	return runnerResumable(runner)
}

// resumeClaimedJobs re-invokes a resumable job kind's runner for any
// claimed-but-not-yet-recorded job of that kind this controller definitely
// holds a durable claim for -- whether settleJobClaim just confirmed it by
// direct lookup, or an earlier goroutine of this same process claimed it
// and never reached its own phaseObserved write. It runs in the ordinary
// tick flow, never inside settle: settle must never invoke a runner (a
// held entry is invisible to it, so a resumed job never races settle's own
// unconditional "claimed means unknown" fallback for a non-resumable one).
func (c *Controller) resumeClaimedJobs(ctx, workCtx context.Context, sess *session) {
	if !c.admitting() {
		return
	}
	for _, e := range sess.journal.snapshot() {
		if e.Kind != kindJob || e.Phase != phaseClaimed || c.held(e.ID) {
			continue
		}
		c.mu.Lock()
		runner := c.deps.Jobs[JobKey(e.JobOwner, e.JobOp)]
		c.mu.Unlock()
		if !runnerResumable(runner) {
			// Left exactly as it is: settle's existing phaseClaimed fallback
			// records it outcome_unknown on its own next pass, precisely as
			// it already does for every claimed effect.
			continue
		}
		if !c.admitting() || !c.acquire() {
			return
		}
		c.hold(e.ID)
		c.workers.Add(1)
		go c.runJob(workCtx, sess, e, runner, Job{
			ID: e.JobID, Owner: e.JobOwner, Operation: e.JobOp, Input: e.JobInput,
		})
	}
}

// settleJob advances one job entry from its durable phase. A claim without
// an outcome means the runner may have run: the job is recorded as
// outcome_unknown under the generation that claimed it and is never rerun.
func (c *Controller) settleJob(ctx context.Context, sess *session, e entry) {
	if e.Phase == phaseClaimed {
		outcome := unknownJob("the controller stopped after claiming this job and before its outcome was durable")
		e.Outcome = &outcome
		e.Phase = phaseObserved
		if !c.journal(sess, e) {
			return
		}
	}
	if e.Phase == phaseObserved {
		if e.Outcome == nil {
			c.refuseJob(sess, &e, internalFault("journal entry for job %s reached record without an outcome", e.JobID))
			return
		}
		evidence := e.Outcome.EvidenceIDs
		if evidence == nil {
			evidence = []contract.ID{}
		}
		var recorded jobOutput
		err := c.write(func() error {
			return c.call(ctx, sess, "_execution.job.record", jobRecordInput{
				JobID:           e.JobID,
				ExpectedVersion: e.JobVersion,
				Generation:      e.Generation,
				State:           e.Outcome.State,
				Result:          e.Outcome.Result,
				EvidenceIDs:     evidence,
			}, &recorded)
		})
		if err != nil {
			c.note(err)
			if !transient(err) {
				c.refuseJob(sess, &e, faultOf(err))
			}
			return
		}
		e.Phase = phaseRecorded
		if !c.journal(sess, e) {
			return
		}
	}
	if e.Phase != phaseRecorded {
		return
	}
	if e.JobOwner == configurationOwner && e.Outcome.State == jobStateSucceeded {
		if !c.finishConfigurationExport(ctx, sess, &e) {
			return
		}
	}
	if e.JobOwner == skillsOwner && e.JobOp == skillEvaluateOp && e.Outcome.State == jobStateSucceeded {
		if !c.finishSkillEvaluation(ctx, sess, &e) {
			return
		}
	}
	e.Phase = phaseDone
	e.Fault = nil
	c.journal(sess, e)
}

// configurationExportRecordInput is _configuration.export.record's wire
// input: configuration's own RunJob (export jobs, any family) stages and
// publishes the export bundle's bytes itself and mints the artifact
// reference directly in its JobOutcome.Result -- unlike a document-publish
// job, whose staged reference execution's own _execution.job.record
// handler promotes internally -- so this owner-specific finish call, not
// the generic ledger record alone, is what durably links that artifact to
// configuration's own local export-job row.
type configurationExportRecordInput struct {
	JobID           contract.ID  `json:"job_id"`
	ExpectedVersion int64        `json:"expected_version"`
	Generation      int64        `json:"generation"`
	Artifact        wireArtifact `json:"artifact"`
}

// finishConfigurationExport extracts the artifact reference configuration's
// own RunJob already published (JobOutcome.Result, shape {"resource":
// ArtifactRef}) and durably links it to configuration's own export-job row
// through _configuration.export.record. expected_version is 1: this local
// row is sealed once at _configuration.export.prepare and never touched
// again before this exact call, the same fixed-version convention this
// package already relies on for a sealed VerificationRequest.
func (c *Controller) finishConfigurationExport(ctx context.Context, sess *session, e *entry) bool {
	var body struct {
		Resource wireArtifact `json:"resource"`
	}
	if json.Unmarshal(e.Outcome.Result, &body) != nil || body.Resource.ID == "" {
		f := internalFault("configuration export job %s succeeded with a result that does not name a resource artifact", e.JobID)
		c.refuseJob(sess, e, f)
		return false
	}
	err := c.write(func() error {
		return c.call(ctx, sess, "_configuration.export.record", configurationExportRecordInput{
			JobID: e.JobID, ExpectedVersion: 1, Generation: sess.generation, Artifact: body.Resource,
		}, nil)
	})
	if err != nil {
		c.note(err)
		if !transient(err) {
			c.refuseJob(sess, e, faultOf(err))
		}
		return false
	}
	return true
}

// skillsEvaluationRecordInput is _skills.evaluation.record's wire input.
type skillsEvaluationRecordInput struct {
	EvaluationID    contract.ID   `json:"evaluation_id"`
	JobID           contract.ID   `json:"job_id"`
	ExpectedVersion int64         `json:"expected_version"`
	VerifierID      string        `json:"verifier_id"`
	VerifierVersion string        `json:"verifier_version"`
	EvidenceIDs     []contract.ID `json:"evidence_ids"`
	Passed          bool          `json:"passed"`
}

// skillEvaluationJobInput reads only the sealed acceptance identity fields
// skills' own evaluationJobInput carries as the job's original claimed
// input -- verifier_id/verifier_version are not part of RunJob's own
// result, only of what was sealed at admission (journaled as e.JobInput at
// claim time), exactly what _skills.evaluation.record's own doc requires:
// "a verifier identity that no longer matches what was admitted
// invalidates the evaluation instead of recording the mismatched
// submission."
type skillEvaluationJobInput struct {
	Acceptance struct {
		VerifierID      string `json:"verifier_id"`
		VerifierVersion string `json:"verifier_version"`
	} `json:"acceptance"`
}

// skillEvaluationResult reads skill.evaluate's own completion_schema
// (evaluation_id, passed, evidence[]), exactly what skills' RunJob returns.
type skillEvaluationResult struct {
	EvaluationID contract.ID    `json:"evaluation_id"`
	Passed       bool           `json:"passed"`
	Evidence     []wireArtifact `json:"evidence"`
}

// finishSkillEvaluation records the independently established evaluation
// outcome against the exact accepted verifier identity through
// _skills.evaluation.record, using the sealed acceptance the job was
// claimed with (never a value the runner could substitute) and the
// evidence artifacts the runner's own RunJob already staged and published.
func (c *Controller) finishSkillEvaluation(ctx context.Context, sess *session, e *entry) bool {
	var sealed skillEvaluationJobInput
	if json.Unmarshal(e.JobInput, &sealed) != nil || sealed.Acceptance.VerifierID == "" {
		f := internalFault("skill evaluation job %s has no sealed verifier identity to record against", e.JobID)
		c.refuseJob(sess, e, f)
		return false
	}
	var result skillEvaluationResult
	if json.Unmarshal(e.Outcome.Result, &result) != nil || result.EvaluationID == "" {
		f := internalFault("skill evaluation job %s succeeded with a result that does not name its evaluation", e.JobID)
		c.refuseJob(sess, e, f)
		return false
	}
	evidenceIDs := make([]contract.ID, 0, len(result.Evidence))
	for _, ref := range result.Evidence {
		evidenceIDs = append(evidenceIDs, ref.ID)
	}
	err := c.write(func() error {
		return c.call(ctx, sess, "_skills.evaluation.record", skillsEvaluationRecordInput{
			EvaluationID: result.EvaluationID, JobID: e.JobID, ExpectedVersion: 1,
			VerifierID: sealed.Acceptance.VerifierID, VerifierVersion: sealed.Acceptance.VerifierVersion,
			EvidenceIDs: evidenceIDs, Passed: result.Passed,
		}, nil)
	})
	if err != nil {
		c.note(err)
		if !transient(err) {
			c.refuseJob(sess, e, faultOf(err))
		}
		return false
	}
	return true
}

func (c *Controller) refuseJob(sess *session, e *entry, f *contract.Fault) {
	e.Phase = phaseRefused
	e.Fault = f
	if c.journal(sess, *e) {
		c.oblige(obligationJob, e.JobID, f)
	}
}
