package execution

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Shared controller-loop fixtures plus the behavior the operation test
// files do not carry: lease expiry that never revives a fenced attempt,
// and the end-to-end pass where the real verifier's recorded document
// completes a task. Verifier, attempt, worker-gate and job fences live in
// their operation test files.

// claimContextTick claims the run, pins its request context and ticks once,
// returning the claim envelope of an attempt running with its first model
// effect prepared.
func (e *testEnv) claimContextTick(runID, workerID contract.ID) claimBody {
	e.t.Helper()
	claim := e.claimRun(runID, workerID)
	e.pinContext(claim.Attempt.ID, 1)
	e.mustOK(opTick, tickInput{Now: formatStamp(e.clock.Now()), Limit: 100})
	claim.Attempt = *e.readAttemptRaw(claim.Attempt.ID)
	return claim
}

// readAttemptRaw loads one attempt row without the helper indirection.
func (e *testEnv) readAttemptRaw(id contract.ID) *wireAttempt {
	a := e.readAttempt(id)
	return &wireAttempt{
		ID: a.ID, Version: a.Version, RunID: a.RunID, WorkerID: a.WorkerID,
		Executor: a.Executor, Generation: a.Generation, LeaseID: a.LeaseID,
		ReservationID: a.ReservationID, State: a.State,
		ContextArtifact: a.ContextArtifact, RecoveryReason: a.RecoveryReason,
	}
}

// reportAccepted reports the attempt for verification with conclusive usage.
func (e *testEnv) reportAccepted(claim claimBody, usage wireUsage) {
	e.t.Helper()
	a := e.readAttempt(claim.Attempt.ID)
	e.mustOK(opAttemptReport, reportInput{
		Scope:           e.scope,
		AttemptID:       a.ID,
		LeaseID:         a.LeaseID,
		Generation:      a.Generation,
		ExpectedVersion: a.Version,
		Outputs:         []wireArtifactRef{{ID: e.ids.New(), Digest: fixtureDigest}},
		Observations:    json.RawMessage("{}"),
		Usage:           usage,
	})
}

// verifierResult runs the real verifier over a stored verification request
// and returns the typed result the controller records.
func (e *testEnv) verifierResult(request []byte, blobs *fakeBlobStore) wireVerificationResult {
	e.t.Helper()
	v, err := NewVerifier(contract.VerifierDependencies{Clock: e.clock, Blobs: blobs})
	if err != nil {
		e.t.Fatalf("NewVerifier: %v", err)
	}
	res, err := v.Verify(context.Background(), contract.Verification{Request: request})
	if err != nil {
		e.t.Fatalf("verifier.Verify: %v", err)
	}
	var result wireVerificationResult
	e.decode(res.Document, &result)
	return result
}

func TestLeaseExpiryNoRevival(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimContextTick(run.ID, worker)

	// Outlive the lease: a tick fences the attempt and the run waits for
	// recovery instead of reviving the dead worker's lease.
	e.clock.advance(2 * leaseDuration)
	payload := e.mustOK(opTick, tickInput{Now: formatStamp(e.clock.Now()), Limit: 100})
	var fenced fenceBody
	e.decode(payload.Data, &fenced)
	found := false
	for _, id := range fenced.AttemptIDs {
		if id == claim.Attempt.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("tick did not fence the expired attempt %s", claim.Attempt.ID)
	}
	if got := e.readAttempt(claim.Attempt.ID); got.State != "fenced" {
		t.Fatalf("expired attempt state %q, want fenced", got.State)
	}
	if got := e.readRun(run.ID); got.State != "waiting" {
		t.Fatalf("run state %q, want waiting after the lease expired", got.State)
	}

	// A replacement attempt is a fresh attempt under the same controller
	// generation — the run's attempt list is the per-run counter — and the
	// old one stays fenced.
	replacement := e.claimRun(run.ID, worker)
	if replacement.Attempt.ID == claim.Attempt.ID || replacement.Attempt.Generation != e.generation() {
		t.Fatalf("replacement claim %+v, want a new attempt bound to generation %d", replacement.Attempt, e.generation())
	}
	if got := e.readRun(run.ID); len(got.AttemptIDs) != 2 || got.AttemptIDs[1] != replacement.Attempt.ID {
		t.Fatalf("run attempts %v, want the replacement appended second", got.AttemptIDs)
	}
	if got := e.readAttempt(claim.Attempt.ID); got.State != "fenced" {
		t.Fatalf("expired attempt state %q, the replacement must not revive it", got.State)
	}
}

func TestClaimBindsControllerGeneration(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	gen := e.advanceGeneration()
	if gen != 2 {
		t.Fatalf("precondition: generation %d, want 2", gen)
	}
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	if claim.Attempt.Generation != 2 {
		t.Fatalf("first attempt generation %d, want the persisted controller generation 2", claim.Attempt.Generation)
	}
	if lease := e.readLease(claim.Attempt.LeaseID); lease.Generation != 2 {
		t.Fatalf("lease generation %d, want the persisted controller generation 2", lease.Generation)
	}
	// The worker's calls bind that generation, not a per-run ordinal.
	a := e.readAttempt(claim.Attempt.ID)
	_ = e.expectFault(opAttemptHeartbeat, heartbeatInput{
		Scope: e.scope, AttemptID: a.ID, LeaseID: a.LeaseID,
		Generation: 1, ExpectedVersion: a.Version,
	}, contract.CodeConflict)
	e.mustOK(opAttemptHeartbeat, heartbeatInput{
		Scope: e.scope, AttemptID: a.ID, LeaseID: a.LeaseID,
		Generation: 2, ExpectedVersion: a.Version,
	})
}

func TestFenceMarksOnlyEarlierGenerations(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))

	// A run whose first attempt expired and was replaced: the replacement is
	// the run's second attempt but still bound to generation 1, so the
	// per-run ordinal plays no part in what a fence sweeps.
	peerRun := e.enqueueTask(worker, nil)
	peer := e.claimRun(peerRun.ID, worker)
	e.clock.advance(2 * leaseDuration)
	e.mustOK(opTick, tickInput{Now: formatStamp(e.clock.Now()), Limit: 100})
	peerReplacement := e.claimRun(peerRun.ID, worker)
	if peerReplacement.Attempt.ID == peer.Attempt.ID || peerReplacement.Attempt.Generation != 1 {
		t.Fatalf("peer replacement %+v, want a second attempt still bound to generation 1", peerReplacement.Attempt)
	}
	// A fresh run's first attempt, also generation 1.
	oldRun := e.enqueueTask(worker, nil)
	old := e.claimRun(oldRun.ID, worker)

	// Restart: the new controller fences generation 2 and admits under it.
	gen := e.advanceGeneration()
	newRun := e.enqueueTask(worker, nil)
	current := e.claimRun(newRun.ID, worker)
	if current.Attempt.Generation != gen {
		t.Fatalf("attempt claimed after the restart carries generation %d, want %d", current.Attempt.Generation, gen)
	}

	payload := e.mustOK(opFence, fenceInput{Generation: gen, Reason: "restart"})
	var body fenceBody
	e.decode(payload.Data, &body)
	fenced := map[contract.ID]bool{}
	for _, id := range body.AttemptIDs {
		fenced[id] = true
	}
	if len(fenced) != 2 || !fenced[old.Attempt.ID] || !fenced[peerReplacement.Attempt.ID] {
		t.Fatalf("fence swept %v, want exactly the generation-1 attempts %s and %s",
			body.AttemptIDs, old.Attempt.ID, peerReplacement.Attempt.ID)
	}
	if got := e.readAttempt(current.Attempt.ID); got.State != "claimed" {
		t.Fatalf("current-generation attempt state %q, the fence must not touch it", got.State)
	}
	if got := e.readAttempt(old.Attempt.ID); got.State != "fenced" || got.RecoveryReason != "restart" {
		t.Fatalf("old attempt state %q reason %q, want fenced by the restart", got.State, got.RecoveryReason)
	}
	// A fence for the generation already running is a no-op: nothing below
	// it is live any more.
	payload = e.mustOK(opFence, fenceInput{Generation: gen, Reason: "restart"})
	e.decode(payload.Data, &body)
	if len(body.AttemptIDs) != 0 {
		t.Fatalf("second fence swept %v, want nothing", body.AttemptIDs)
	}
}

func TestVerificationPassesTask(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimContextTick(run.ID, worker)
	e.reportAccepted(claim, wireUsage{Currency: "USD", Spent: 120})
	a := e.readAttempt(claim.Attempt.ID)
	job := e.readVerificationJob(a.ID)

	// The document the real verifier records is accepted unchanged by the
	// record handler: the two sides of the trust boundary agree.
	blobs := newFakeBlobStore(map[contract.Digest][]byte{fixtureDigest: fixtureContent})
	result := e.verifierResult([]byte(job.Request), blobs)
	result.Status = "passed"
	e.mustOK(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: result,
	})

	if got := e.readRun(run.ID); got.State != "succeeded" {
		t.Fatalf("run state %q, want succeeded", got.State)
	}
	if got := e.readAttempt(a.ID); got.State != "reported" {
		t.Fatalf("attempt state %q, want the reported attempt kept for audit", got.State)
	}
	transitions := e.ports.Transitions()
	if len(transitions) == 0 || transitions[len(transitions)-1].State != "succeeded" {
		t.Fatalf("task transitions %v, want a succeeded transition", transitions)
	}
	var succeeded bool
	for _, ev := range e.readEvents() {
		if ev.Kind == eventRunSucceeded && ev.ResourceID == string(run.ID) {
			succeeded = true
		}
	}
	if !succeeded {
		t.Fatalf("run.succeeded event missing for run %s", run.ID)
	}
	if got := len(e.ports.SettleCalls()); got != 1 {
		t.Fatalf("settle calls %d, want exactly the report-time settle", got)
	}
}
