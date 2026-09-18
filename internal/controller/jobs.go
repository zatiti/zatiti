package controller

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

const (
	restoreOwner     = "installation"
	restoreOperation = "installation.restore"
)

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

// settleJobClaim resolves a job claim whose commit was never acknowledged by
// a process that has since died. A job still pending was never claimed. A
// job that left the pending scan was claimed by the dead generation: its
// post-claim version is unknowable through the allowed calls, so it is
// retained as a stranded claim rather than recorded by guesswork.
func (c *Controller) settleJobClaim(sess *session, e entry, pending map[contract.ID]wireJob) {
	if pending == nil {
		return
	}
	if job, ok := pending[e.JobID]; ok && job.State == jobStatePending {
		e.Phase = phaseDone
		c.journal(sess, e)
		return
	}
	f := prerequisiteMissing(
		"claim of job %s was not acknowledged before the controller stopped; its claimed version cannot be read, so the job cannot be recorded as unknown",
		e.JobID)
	e.Phase = phaseStranded
	e.Fault = f
	if c.journal(sess, e) {
		c.oblige(obligationJob, e.JobID, f)
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
	if e.JobOwner == restoreOwner && e.JobOp == restoreOperation {
		// The installation owner keeps its own maintenance bookkeeping and
		// learns the disposition of controller-performed IO only from here.
		requirements := e.Outcome.Requirements
		if requirements == nil {
			requirements = []Requirement{}
		}
		state := e.Outcome.State
		if state == jobStateCancelled {
			state = jobStateFailed
			requirements = append(requirements, Requirement{Code: contract.CodeConflict, Message: "the restore job was cancelled"})
		}
		err := c.write(func() error {
			return c.call(ctx, sess, "_installation.restore.record", restoreRecordInput{
				JobID: e.JobID, State: state, Requirements: requirements,
			}, nil)
		})
		if err != nil {
			c.note(err)
			if !transient(err) {
				c.refuseJob(sess, &e, faultOf(err))
			}
			return
		}
	}
	e.Phase = phaseDone
	e.Fault = nil
	c.journal(sess, e)
}

func (c *Controller) refuseJob(sess *session, e *entry, f *contract.Fault) {
	e.Phase = phaseRefused
	e.Fault = f
	if c.journal(sess, *e) {
		c.oblige(obligationJob, e.JobID, f)
	}
}
