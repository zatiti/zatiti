package execution

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Controller operations: context pinning, bounded tick admission, the
// observation-driven model loop and the trusted verification record — the
// only path that completes a task.

// reportedPipeline drives one task through enqueue, claim and report and
// returns the claim envelope, the stored attempt and the pending
// verification job.
func reportedPipeline(e *testEnv, mutate func(*wireTask), usage wireUsage) (claimBody, *attemptRow, *verificationJobRow) {
	e.t.Helper()
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, mutate)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)
	e.mustOK(opAttemptReport, reportInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation, ExpectedVersion: a.Version,
		Outputs:      []wireArtifactRef{{ID: e.ids.New(), Digest: fixtureDigest}},
		Observations: []byte(`{}`),
		Usage:        usage,
	})
	return claim, e.readAttempt(claim.Attempt.ID), e.readVerificationJob(claim.Attempt.ID)
}

// resultFor builds a schema-valid verification result bound to the stored
// job; callers then break exactly one binding for a fence test.
func resultFor(e *testEnv, job *verificationJobRow, status string, observed []wireObservedCheck) wireVerificationResult {
	e.t.Helper()
	return wireVerificationResult{
		Schema:             "zatiti.verification-result/v1",
		JobID:              job.ID,
		TaskID:             job.TaskID,
		AttemptID:          job.AttemptID,
		AcceptanceDigest:   job.AcceptanceDigest,
		VerifierID:         "verifier-core",
		VerifierVersion:    "1.0.0",
		VerifierCodeDigest: verifierCode,
		RequestArtifact:    wireArtifactRef{ID: e.ids.New(), Digest: sha256Hex(job.Request)},
		Status:             status,
		Observations:       observed,
		StartedAt:          e.clock.Now().Format(time.RFC3339),
		FinishedAt:         e.clock.Now().Format(time.RFC3339),
		StagedOutputs:      []json.RawMessage{},
		OutputArtifacts:    []json.RawMessage{},
		Independent:        true,
	}
}

// passedChecks observes the fixture acceptance's single digest check as
// passed.
func passedChecks() []wireObservedCheck {
	return []wireObservedCheck{{
		CheckID: "out-digest", Kind: "artifact_digest", Status: "passed",
		Evidence: []json.RawMessage{}, Explanation: "digest matched",
		ObservedDigest: fixtureDigest,
	}}
}

// mustVerify records a verdict and returns the attempt body.
func mustVerify(e *testEnv, a *attemptRow, result wireVerificationResult) attemptBody {
	e.t.Helper()
	payload := e.mustOK(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: result,
	})
	var body attemptBody
	e.decode(payload.Data, &body)
	return body
}

func TestContextRevisionFence(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)

	f := e.expectFault(opContext, contextInput{
		AttemptID: claim.Attempt.ID,
		Context: wireContext{
			AttemptID:             claim.Attempt.ID,
			Artifact:              wireArtifactRef{ID: e.ids.New(), Digest: fixtureDigest},
			ConfigurationRevision: 2,
			SourceArtifacts:       []wireArtifactRef{},
			Capture:               "complete",
		},
	}, contract.CodeStaleVersion)
	if !strings.Contains(f.Message, "configuration revision 2") {
		t.Fatalf("fault message %q does not name the pinned revision", f.Message)
	}
	if e.readAttempt(claim.Attempt.ID).ContextArtifact != nil {
		t.Fatalf("rejected context still pinned an artifact")
	}
}

func TestContextNamesWrongAttempt(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	f := e.expectFault(opContext, contextInput{
		AttemptID: claim.Attempt.ID,
		Context: wireContext{
			AttemptID:             e.ids.New(),
			Artifact:              wireArtifactRef{ID: e.ids.New(), Digest: fixtureDigest},
			ConfigurationRevision: 1,
			SourceArtifacts:       []wireArtifactRef{},
			Capture:               "complete",
		},
	}, contract.CodeInvalidInput)
	if !strings.Contains(f.Message, "context names attempt") {
		t.Fatalf("fault message %q does not name the attempt binding fence", f.Message)
	}
}

func TestContextOnReportedAttemptConflicts(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.pinContext(claim.Attempt.ID, run.ConfigurationRevision)
	a := e.readAttempt(claim.Attempt.ID)
	e.mustOK(opAttemptReport, reportInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation, ExpectedVersion: a.Version,
		Outputs:      []wireArtifactRef{{ID: e.ids.New(), Digest: fixtureDigest}},
		Observations: []byte(`{}`),
		Usage:        wireUsage{Currency: "USD", Spent: 1},
	})
	a = e.readAttempt(claim.Attempt.ID)
	f := e.expectFault(opContext, contextInput{
		AttemptID: claim.Attempt.ID,
		Context: wireContext{
			AttemptID:             claim.Attempt.ID,
			Artifact:              wireArtifactRef{ID: e.ids.New(), Digest: fixtureDigest},
			ConfigurationRevision: run.ConfigurationRevision,
			SourceArtifacts:       []wireArtifactRef{},
			Capture:               "complete",
		},
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "cannot accept context") {
		t.Fatalf("fault message %q does not name the state fence", f.Message)
	}
	if a.ContextArtifact == nil {
		t.Fatalf("reported attempt lost its pinned context")
	}
}

func TestContextForeignInstallationDenied(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	// The installation fence reads the attempt row, not the request scope:
	// a row moved to a foreign installation is unreachable from this one.
	foreign := e.ids.New()
	e.inWrite(func(unit contract.Unit) error {
		_, err := unit.ExecContext(e.ctx,
			`UPDATE execution_attempts SET installation_id = ? WHERE id = ?`,
			string(foreign), string(claim.Attempt.ID))
		return err
	})
	f := e.expectFault(opContext, contextInput{
		AttemptID: claim.Attempt.ID,
		Context: wireContext{
			AttemptID:             claim.Attempt.ID,
			Artifact:              wireArtifactRef{ID: e.ids.New(), Digest: fixtureDigest},
			ConfigurationRevision: run.ConfigurationRevision,
			SourceArtifacts:       []wireArtifactRef{},
			Capture:               "complete",
		},
	}, contract.CodePermissionDenied)
	if !strings.Contains(f.Message, "another installation") {
		t.Fatalf("fault message %q does not name the installation fence", f.Message)
	}
}

func TestTickBounds(t *testing.T) {
	e := newEnv(t)
	// Garbage timestamps and out-of-range limits are rejected at bind; the
	// bound schema's date-time format never reaches the handler's parser.
	_ = e.expectFault(opTick, tickInput{Now: "not-a-timestamp", Limit: 1}, contract.CodeInvalidInput)
	_ = e.expectFault(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 0},
		contract.CodeInvalidInput)
	_ = e.expectFault(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 101},
		contract.CodeInvalidInput)
}

func TestTickExpiresLeases(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.clock.advance(2 * time.Minute)

	payload := e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})
	var body fenceBody
	e.decode(payload.Data, &body)
	if len(body.AttemptIDs) != 1 || body.AttemptIDs[0] != claim.Attempt.ID {
		t.Fatalf("tick fenced %v, want the expired attempt", body.AttemptIDs)
	}
	a := e.readAttempt(claim.Attempt.ID)
	if a.State != "fenced" {
		t.Fatalf("attempt state %q, want fenced", a.State)
	}
	if lease := e.readLease(a.LeaseID); lease.State != "expired" {
		t.Fatalf("lease state %q, want expired", lease.State)
	}
	obligations := e.readObligations("attempt_id", claim.Attempt.ID)
	if len(obligations) != 1 || obligations[0].Code != "lease_conflict" {
		t.Fatalf("obligations %+v, want one lease_conflict", obligations)
	}
	if !strings.Contains(obligations[0].Message, "recovery required") {
		t.Fatalf("obligation message %q does not demand recovery", obligations[0].Message)
	}
	if got := e.readRun(run.ID); got.State != "waiting" {
		t.Fatalf("run state %q, want waiting", got.State)
	}
}

// pinnedClaim enqueues, claims and pins the request context for one run.
func pinnedClaim(e *testEnv) (wireRun, claimBody, *attemptRow) {
	e.t.Helper()
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.pinContext(claim.Attempt.ID, run.ConfigurationRevision)
	return run, claim, e.readAttempt(claim.Attempt.ID)
}

func TestTickAdmitsWithContextAndPreparesEffect(t *testing.T) {
	e := newEnv(t)
	_, claim, _ := pinnedClaim(e)

	payload := e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})
	var body fenceBody
	e.decode(payload.Data, &body)
	if len(body.AttemptIDs) != 1 || body.AttemptIDs[0] != claim.Attempt.ID {
		t.Fatalf("tick admitted %v, want the pinned attempt", body.AttemptIDs)
	}
	a := e.readAttempt(claim.Attempt.ID)
	if a.State != "running" {
		t.Fatalf("attempt state %q, want running", a.State)
	}
	op := e.readOpenOperation(claim.Attempt.ID)
	if op == nil || op.Kind != "model_step" || op.State != "prepared" {
		t.Fatalf("open operation %+v, want a prepared model step", op)
	}
	prepared := e.ports.PreparedOps()
	if len(prepared) != 1 {
		t.Fatalf("prepared effects %v, want exactly one", prepared)
	}
}

func TestTickConcurrencyBound(t *testing.T) {
	e := newEnv(t)
	// The fixture task binds concurrency 2 per worker. The count includes
	// each candidate itself (claimed is a live state), so three pinned
	// claims of one worker exceed the bound and the tick admits none.
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	ids := []contract.ID{}
	for i := 0; i < 3; i++ {
		run := e.enqueueTask(worker, nil)
		claim := e.claimRun(run.ID, worker)
		e.pinContext(claim.Attempt.ID, run.ConfigurationRevision)
		ids = append(ids, claim.Attempt.ID)
	}
	payload := e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})
	var body fenceBody
	e.decode(payload.Data, &body)
	if len(body.AttemptIDs) != 0 {
		t.Fatalf("tick admitted %v, want none above the bound", body.AttemptIDs)
	}
	for _, id := range ids {
		if got := e.readAttempt(id); got.State != "claimed" {
			t.Fatalf("attempt %s state %q, want claimed beyond the bound", id, got.State)
		}
	}
	if prepared := e.ports.PreparedOps(); len(prepared) != 0 {
		t.Fatalf("prepared effects %v, want none above the bound", prepared)
	}

	// Cancelling one claimed attempt leaves exactly two live: the tick
	// admits both remaining candidates within the bound.
	a := e.readAttempt(ids[0])
	e.mustOK(opAttemptCancel, cancelInput{
		Scope: e.scope, ID: ids[0], ExpectedVersion: a.Version, Reason: "over the bound",
	})
	payload = e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})
	e.decode(payload.Data, &body)
	if len(body.AttemptIDs) != 2 {
		t.Fatalf("tick admitted %d attempts, want 2 within the bound", len(body.AttemptIDs))
	}
	running := 0
	for _, id := range ids[1:] {
		if e.readAttempt(id).State == "running" {
			running++
		}
	}
	if running != 2 {
		t.Fatalf("%d attempts running, want 2", running)
	}
	if prepared := e.ports.PreparedOps(); len(prepared) != 2 {
		t.Fatalf("prepared effects %v, want one per admitted attempt", prepared)
	}
}

func TestTickSkipsMissingContextAndCancellation(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)

	// A claimed attempt without a pinned context stays unadmitted.
	payload := e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})
	var body fenceBody
	e.decode(payload.Data, &body)
	if len(body.AttemptIDs) != 0 {
		t.Fatalf("tick admitted %v without a pinned context", body.AttemptIDs)
	}

	// Cancellation suppresses admission even with the context pinned.
	e.pinContext(claim.Attempt.ID, run.ConfigurationRevision)
	task := e.ports.tasks[run.TaskID]
	task.CancellationRequested = true
	payload = e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})
	e.decode(payload.Data, &body)
	if len(body.AttemptIDs) != 0 {
		t.Fatalf("tick admitted %v under requested cancellation", body.AttemptIDs)
	}
	if e.readAttempt(claim.Attempt.ID).State != "claimed" {
		t.Fatalf("attempt state %q, want still claimed", e.readAttempt(claim.Attempt.ID).State)
	}
}

func TestFenceSweepsAllLiveAttempts(t *testing.T) {
	e := newEnv(t)
	first, claim1, _ := pinnedClaim(e)
	_, claim2, _ := pinnedClaim(e)

	payload := e.mustOK(opFence, fenceInput{Generation: 2, Reason: "rotation"})
	var body fenceBody
	e.decode(payload.Data, &body)
	if len(body.AttemptIDs) != 2 {
		t.Fatalf("fence swept %d attempts, want 2", len(body.AttemptIDs))
	}
	for _, claim := range []claimBody{claim1, claim2} {
		a := e.readAttempt(claim.Attempt.ID)
		if a.State != "fenced" {
			t.Fatalf("attempt %s state %q, want fenced", a.ID, a.State)
		}
		if lease := e.readLease(a.LeaseID); lease.State != "expired" {
			t.Fatalf("lease state %q, want expired", lease.State)
		}
		obligations := e.readObligations("attempt_id", claim.Attempt.ID)
		if len(obligations) != 1 || obligations[0].Code != "lease_conflict" {
			t.Fatalf("obligations %+v, want one lease_conflict", obligations)
		}
	}
	if got := e.readRun(first.ID); got.State != "waiting" {
		t.Fatalf("run state %q, want waiting", got.State)
	}
}

// observe dispatches one observation for the attempt's current open
// operation and returns the operation record id used.
func observe(e *testEnv, attemptID contract.ID, disposition string) contract.ID {
	e.t.Helper()
	op := e.readOpenOperation(attemptID)
	if op == nil {
		e.t.Fatalf("attempt %s has no open operation to observe", attemptID)
	}
	e.mustOK(opObservation, observationInput{
		AttemptID: attemptID, OperationID: op.ID,
		Observation: wireObservation{
			Disposition: disposition,
			Evidence:    json.RawMessage(`{}`),
			Usage:       wireUsage{Currency: "USD"},
		},
	})
	return op.ID
}

func TestObservationBoundedLoop(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.pinContext(claim.Attempt.ID, run.ConfigurationRevision)
	e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})

	// Each succeeded step closes its record, increments the counter and
	// prepares the next; the fourth hits the task's bound and fences.
	for step := 1; step <= 3; step++ {
		observe(e, claim.Attempt.ID, "succeeded")
		a := e.readAttempt(claim.Attempt.ID)
		if a.ModelStepsUsed != int64(step) {
			t.Fatalf("model steps used %d after step %d", a.ModelStepsUsed, step)
		}
		if op := e.readOpenOperation(claim.Attempt.ID); op == nil || op.State != "prepared" {
			t.Fatalf("step %d left no freshly prepared operation", step)
		}
		if e.readAttempt(claim.Attempt.ID).State != "running" {
			t.Fatalf("step %d disturbed the running attempt", step)
		}
	}
	observe(e, claim.Attempt.ID, "succeeded")
	a := e.readAttempt(claim.Attempt.ID)
	if a.State != "fenced" || a.RecoveryReason != "model step bound 4 reached" {
		t.Fatalf("attempt state %q reason %q, want the bound fence", a.State, a.RecoveryReason)
	}
	if a.ModelStepsUsed != 4 {
		t.Fatalf("model steps used %d, want 4", a.ModelStepsUsed)
	}
	if op := e.readOpenOperation(claim.Attempt.ID); op != nil {
		t.Fatalf("bound fence left operation %+v open", op)
	}
	if got := e.readRun(run.ID); got.State != "waiting" {
		t.Fatalf("run state %q, want waiting", got.State)
	}
	if prepared := e.ports.PreparedOps(); len(prepared) != 4 {
		t.Fatalf("prepared effects %d, want one per step", len(prepared))
	}
}

func TestObservationFailedFences(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.pinContext(claim.Attempt.ID, run.ConfigurationRevision)
	e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})

	opID := observe(e, claim.Attempt.ID, "failed")
	if op := e.readOperation(opID); op.State != "failed" {
		t.Fatalf("operation state %q, want failed", op.State)
	}
	a := e.readAttempt(claim.Attempt.ID)
	if a.State != "fenced" || a.RecoveryReason != "model effect failed" {
		t.Fatalf("attempt state %q reason %q, want the effect fence", a.State, a.RecoveryReason)
	}
	if obligations := e.readObligations("attempt_id", claim.Attempt.ID); len(obligations) != 0 {
		t.Fatalf("obligations %+v, want none for a conclusive failure", obligations)
	}
}

func TestObservationUnknownPreservesObligation(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.pinContext(claim.Attempt.ID, run.ConfigurationRevision)
	e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})

	opID := observe(e, claim.Attempt.ID, "unknown")
	if op := e.readOperation(opID); op.State != "recorded" {
		t.Fatalf("operation state %q, want recorded for an unknown effect", op.State)
	}
	a := e.readAttempt(claim.Attempt.ID)
	if a.State != "fenced" || a.RecoveryReason != "model effect unknown" {
		t.Fatalf("attempt state %q reason %q, want the effect fence", a.State, a.RecoveryReason)
	}
	obligations := e.readObligations("attempt_id", claim.Attempt.ID)
	if len(obligations) != 1 || obligations[0].Code != "unknown_effect" {
		t.Fatalf("obligations %+v, want one unknown_effect", obligations)
	}
}

func TestObservationPendingFences(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.pinContext(claim.Attempt.ID, run.ConfigurationRevision)
	e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})
	opID := observe(e, claim.Attempt.ID, "succeeded")

	// The closed operation has no pending observation left.
	f := e.expectFault(opObservation, observationInput{
		AttemptID: claim.Attempt.ID, OperationID: opID,
		Observation: wireObservation{
			Disposition: "succeeded",
			Evidence:    json.RawMessage(`{}`),
			Usage:       wireUsage{Currency: "USD"},
		},
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "no pending observation") {
		t.Fatalf("fault message %q does not name the closed record", f.Message)
	}

	// An unknown operation id is not found.
	_ = e.expectFault(opObservation, observationInput{
		AttemptID: claim.Attempt.ID, OperationID: e.ids.New(),
		Observation: wireObservation{
			Disposition: "succeeded",
			Evidence:    json.RawMessage(`{}`),
			Usage:       wireUsage{Currency: "USD"},
		},
	}, contract.CodeNotFound)

	// An operation of a different attempt does not serve this loop.
	_, otherClaim, _ := pinnedClaim(e)
	e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})
	other := e.readOpenOperation(otherClaim.Attempt.ID)
	f = e.expectFault(opObservation, observationInput{
		AttemptID: claim.Attempt.ID, OperationID: other.ID,
		Observation: wireObservation{
			Disposition: "succeeded",
			Evidence:    json.RawMessage(`{}`),
			Usage:       wireUsage{Currency: "USD"},
		},
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "does not belong to attempt") {
		t.Fatalf("fault message %q does not name the operation binding fence", f.Message)
	}

	// A fenced attempt cannot continue its loop at all: the state fence
	// answers before the operation record is even read.
	observe(e, claim.Attempt.ID, "succeeded")
	observe(e, claim.Attempt.ID, "succeeded")
	observe(e, claim.Attempt.ID, "succeeded") // fourth success: the bound fence
	if a := e.readAttempt(claim.Attempt.ID); a.State != "fenced" {
		t.Fatalf("precondition: attempt state %q, want fenced by the bound", a.State)
	}
	f = e.expectFault(opObservation, observationInput{
		AttemptID: claim.Attempt.ID, OperationID: opID,
		Observation: wireObservation{
			Disposition: "succeeded",
			Evidence:    json.RawMessage(`{}`),
			Usage:       wireUsage{Currency: "USD"},
		},
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "cannot continue its loop") {
		t.Fatalf("fault message %q does not name the loop state fence", f.Message)
	}
}

func TestVerificationPassSucceedsTask(t *testing.T) {
	e := newEnv(t)
	claim, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 250})
	_ = claim

	body := mustVerify(e, a, resultFor(e, job, "passed", passedChecks()))
	if body.Resource.State != "reported" {
		t.Fatalf("verified attempt state %q, want reported retained", body.Resource.State)
	}
	job = e.readVerificationJob(a.ID)
	if job.State != "passed" || len(job.Result) == 0 {
		t.Fatalf("verification job state %q result %d bytes", job.State, len(job.Result))
	}
	transitions := e.ports.Transitions()
	if len(transitions) == 0 || transitions[len(transitions)-1].State != "succeeded" {
		t.Fatalf("task transitions %v, want a final succeeded transition", transitions)
	}
	if got := e.readRun(e.readAttempt(a.ID).RunID); got.State != "succeeded" {
		t.Fatalf("run state %q, want succeeded", got.State)
	}
	kinds := map[string]bool{}
	for _, ev := range e.readEvents() {
		kinds[ev.Kind] = true
	}
	if !kinds[eventVerificationRecorded] || !kinds[eventRunSucceeded] {
		t.Fatalf("verification events missing: %v", kinds)
	}
	if settles := e.ports.SettleCalls(); len(settles) != 1 {
		t.Fatalf("settlements %+v, want the report-time settle only", settles)
	}
}

func TestVerificationPassManualModeWaits(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, func(task *wireTask) { task.Acceptance.Mode = "manual" },
		wireUsage{Currency: "USD", Spent: 1})
	mustVerify(e, a, resultFor(e, job, "passed", passedChecks()))
	transitions := e.ports.Transitions()
	last := transitions[len(transitions)-1]
	if last.State != "waiting" || !last.Manual || last.WaitingRe != "manual_acceptance" {
		t.Fatalf("manual verdict transition %+v, want waiting/manual_acceptance", last)
	}
	r := e.readRun(e.readAttempt(a.ID).RunID)
	if r.State != "waiting" {
		t.Fatalf("run state %q, want waiting", r.State)
	}
}

func TestVerificationFailFailsTask(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})
	failed := passedChecks()
	failed[0].Status = "failed"
	failed[0].Explanation = "digest mismatched"
	mustVerify(e, a, resultFor(e, job, "failed", failed))

	a = e.readAttempt(a.ID)
	if a.State != "failed" || !strings.Contains(a.RecoveryReason, "verification failed") {
		t.Fatalf("attempt state %q reason %q, want failed with verification reason", a.State, a.RecoveryReason)
	}
	job = e.readVerificationJob(a.ID)
	if job.State != "failed" {
		t.Fatalf("verification job state %q, want failed", job.State)
	}
	transitions := e.ports.Transitions()
	if transitions[len(transitions)-1].State != "failed" {
		t.Fatalf("task transition %+v, want failed", transitions[len(transitions)-1])
	}
	if got := e.readRun(e.readAttempt(a.ID).RunID); got.State != "failed" {
		t.Fatalf("run state %q, want failed", got.State)
	}
}

func TestVerificationMissingCheckIsPrerequisiteMissing(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})
	// A failure report with no observation for the expected check is
	// overridden: missing evidence cannot pass as a mere failure.
	mustVerify(e, a, resultFor(e, job, "failed", []wireObservedCheck{}))
	job = e.readVerificationJob(a.ID)
	if job.State != "prerequisite_missing" {
		t.Fatalf("verification job state %q, want prerequisite_missing", job.State)
	}
	a = e.readAttempt(a.ID)
	if !strings.Contains(a.RecoveryReason, "could not be observed") {
		t.Fatalf("recovery reason %q does not name the missing evidence", a.RecoveryReason)
	}
}

func TestVerificationTamperedOverridesReportedFailure(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})
	tampered := passedChecks()
	tampered[0].Status = "tampered"
	mustVerify(e, a, resultFor(e, job, "failed", tampered))
	job = e.readVerificationJob(a.ID)
	if job.State != "tampered" {
		t.Fatalf("verification job state %q, want tampered", job.State)
	}
}

func TestVerificationPassedContradictedByChecks(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})
	contradicted := passedChecks()
	contradicted[0].Status = "failed"
	f := e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version,
		Result: resultFor(e, job, "passed", contradicted),
	}, contract.CodeVerificationFailed)
	if !strings.Contains(f.Message, "check out-digest") {
		t.Fatalf("fault message %q does not name the contradicting check", f.Message)
	}
	// The verdict is refused, not rewritten.
	if job := e.readVerificationJob(a.ID); job.State != "pending" {
		t.Fatalf("verification job state %q, want still pending", job.State)
	}
}

func TestVerificationIntegrityFences(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})

	breakSchema := resultFor(e, job, "passed", passedChecks())
	breakSchema.Schema = "zatiti.verification-result/v2"
	// The result schema constant is enforced at bind, before the handler.
	_ = e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: breakSchema,
	}, contract.CodeInvalidInput)

	breakJob := resultFor(e, job, "passed", passedChecks())
	breakJob.JobID = e.ids.New()
	f := e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: breakJob,
	}, contract.CodeVerificationFailed)
	if !strings.Contains(f.Message, "does not address the recorded verification job") {
		t.Fatalf("job binding fault %q", f.Message)
	}

	breakDigest := resultFor(e, job, "passed", passedChecks())
	breakDigest.AcceptanceDigest = digestB
	f = e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: breakDigest,
	}, contract.CodeVerificationFailed)
	if !strings.Contains(f.Message, "acceptance digest does not match") {
		t.Fatalf("acceptance fault %q", f.Message)
	}

	breakIndependent := resultFor(e, job, "passed", passedChecks())
	breakIndependent.Independent = false
	// The wire schema pins independence as a constant: a non-independent
	// result is refused at bind, before the handler's own fence.
	f = e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: breakIndependent,
	}, contract.CodeInvalidInput)
	if !strings.Contains(f.Message, "/result/independent") {
		t.Fatalf("independence bind fault %q", f.Message)
	}

	breakIdentity := resultFor(e, job, "passed", passedChecks())
	breakIdentity.VerifierID = "other-verifier"
	f = e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: breakIdentity,
	}, contract.CodeVerificationFailed)
	if !strings.Contains(f.Message, "verifier identity does not match") {
		t.Fatalf("identity fault %q", f.Message)
	}

	breakCode := resultFor(e, job, "passed", passedChecks())
	breakCode.VerifierCodeDigest = digestB
	f = e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: breakCode,
	}, contract.CodeVerificationFailed)
	if !strings.Contains(f.Message, "verifier identity does not match") {
		t.Fatalf("code digest fault %q", f.Message)
	}

	breakRequest := resultFor(e, job, "passed", passedChecks())
	breakRequest.RequestArtifact.Digest = digestB
	f = e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: breakRequest,
	}, contract.CodeVerificationFailed)
	if !strings.Contains(f.Message, "request artifact digest does not match") {
		t.Fatalf("request binding fault %q", f.Message)
	}

	// Every breach rolled back: the job is still pending and a bound result
	// still records.
	mustVerify(e, a, resultFor(e, job, "passed", passedChecks()))
	if job := e.readVerificationJob(a.ID); job.State != "passed" {
		t.Fatalf("verification job state %q after the accepted record", job.State)
	}
}

func TestVerificationRecordRequiresReportedAttempt(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	result := wireVerificationResult{
		Schema:             "zatiti.verification-result/v1",
		JobID:              e.ids.New(),
		TaskID:             run.TaskID,
		AttemptID:          claim.Attempt.ID,
		AcceptanceDigest:   fixtureDigest,
		VerifierID:         "verifier-core",
		VerifierVersion:    "1.0.0",
		VerifierCodeDigest: verifierCode,
		RequestArtifact:    wireArtifactRef{ID: e.ids.New(), Digest: fixtureDigest},
		Status:             "passed",
		Observations:       []wireObservedCheck{},
		StartedAt:          e.clock.Now().Format(time.RFC3339),
		FinishedAt:         e.clock.Now().Format(time.RFC3339),
		StagedOutputs:      []json.RawMessage{},
		OutputArtifacts:    []json.RawMessage{},
		Independent:        true,
	}
	f := e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: claim.Attempt.ID, ExpectedVersion: claim.Attempt.Version, Result: result,
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "verification requires a reported attempt") {
		t.Fatalf("fault message %q does not name the state fence", f.Message)
	}
}

func TestVerificationRecordStaleVersion(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})
	_ = e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version + 5,
		Result: resultFor(e, job, "passed", passedChecks()),
	}, contract.CodeStaleVersion)
}

func TestVerificationRecordTwiceRejected(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})
	mustVerify(e, a, resultFor(e, job, "passed", passedChecks()))
	f := e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version,
		Result: resultFor(e, job, "passed", passedChecks()),
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "already recorded") {
		t.Fatalf("fault message %q does not name the recorded verdict", f.Message)
	}
}

func TestVerificationSettlesHeldCostOnPass(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Unknown: 40})
	if settles := e.ports.SettleCalls(); len(settles) != 0 {
		t.Fatalf("settlements %+v before the verdict, want the reservation held", settles)
	}
	if len(e.readObligations("attempt_id", a.ID)) != 1 {
		t.Fatalf("obligations %+v, want the cost obligation", e.readObligations("attempt_id", a.ID))
	}
	mustVerify(e, a, resultFor(e, job, "passed", passedChecks()))
	settles := e.ports.SettleCalls()
	if len(settles) != 1 || settles[0].ReservationID != a.ReservationID {
		t.Fatalf("settlements %+v, want one settle of reservation %s", settles, a.ReservationID)
	}
	// The verdict settles the held reservation, but usage with an unknown
	// component keeps the cost_unresolved obligation open until accounting
	// trues up the unknown share.
	if obligations := e.readObligations("attempt_id", a.ID); len(obligations) != 1 ||
		obligations[0].Code != "cost_unresolved" {
		t.Fatalf("obligations %+v, want the cost obligation still open on pass", obligations)
	}
}

func TestVerificationSettlesHeldCostOnFailKeepsObligation(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Unknown: 40})
	failed := passedChecks()
	failed[0].Status = "failed"
	mustVerify(e, a, resultFor(e, job, "failed", failed))
	if settles := e.ports.SettleCalls(); len(settles) != 1 {
		t.Fatalf("settlements %+v, want the held cost settled at the verdict", settles)
	}
	if obligations := e.readObligations("attempt_id", a.ID); len(obligations) != 1 {
		t.Fatalf("obligations %+v, want the cost obligation still open on failure", obligations)
	}
}
