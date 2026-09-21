package execution

import (
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// P18: driving independent verification to a bound task success transition.
// These tests exercise the real trusted verifier (NewVerifier, never a
// fake/hand-rolled stand-in for the "reaches succeeded" case) and the real
// _execution.verification.record boundary end to end -- never a shortcut
// that inserts SQL rows or calls an internal record function directly.
// _tasks.evidence.record and _artifacts.publish are peer calls this package
// cannot execute for real (they are owned by internal/tasks and
// internal/artifacts, outside this package's allowed imports), so the fake
// Ports peer in env_test.go serves them -- exactly the same trust boundary
// every other peer call in this suite (_tasks.transition, _accounting.settle,
// ...) already crosses through a fake, and it is what proves the real
// production code path in this package actually calls them with the right
// data, not that internal/tasks' own logic runs.

// TestReportedTaskSucceedsViaRealVerifierAndTaskEvidence is card P18's
// primary required behavior: a reported task reaches succeeded through the
// real NewVerifier and a real _tasks.evidence.record call, never a
// worker-supplied "passed" claim and never a shortcut that bypasses the
// production record path.
func TestReportedTaskSucceedsViaRealVerifierAndTaskEvidence(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 250})

	// Inspect the sealed request before verifying: the named output slot
	// P18 resolves at report time must already be bound to the real
	// published artifact reportedPipeline registered -- not an empty array
	// (P00.output_slot_binding_and_verification_request).
	var sealed wireVerificationRequest
	e.decode(job.Request, &sealed)
	if len(sealed.Outputs) != 1 || sealed.Outputs[0].Name != "result" ||
		sealed.Outputs[0].Artifact.Digest != fixtureDigest || sealed.Outputs[0].MediaType == "" {
		t.Fatalf("sealed request outputs %+v, want one resolved \"result\" slot", sealed.Outputs)
	}

	// The real verifier, run entirely outside any Unit, against a blob
	// store that actually holds the bound artifact's bytes.
	blobs := newFakeBlobStore(map[contract.Digest][]byte{fixtureDigest: fixtureContent})
	result := e.verifierResult([]byte(job.Request), blobs)
	if result.Status != "passed" {
		t.Fatalf("real verifier status %q, want passed", result.Status)
	}

	mustVerify(e, a, result)

	// The sealed request was published as a real, inspectable artifact --
	// not new bytes staged inside the Unit-bound record handler, but domain
	// metadata registered for bytes the verifier already blob-published.
	published := e.ports.Published()
	if len(published) != 1 || published[0].Digest != result.RequestArtifact.Digest || published[0].Size == 0 {
		t.Fatalf("published artifacts %+v, want exactly one publish of the sealed request", published)
	}

	// _tasks.evidence.record actually ran, with the real verdict, the
	// resolved output binding and the published request as its evidence --
	// never a worker-supplied disposition.
	evidence := e.ports.EvidenceRecords()
	if len(evidence) != 1 {
		t.Fatalf("evidence records %+v, want exactly one", evidence)
	}
	ev := evidence[0]
	if ev.TaskID != job.TaskID || ev.AttemptID != a.ID || ev.Verdict != "passed" {
		t.Fatalf("evidence record %+v, want a passed verdict bound to task %s attempt %s", ev, job.TaskID, a.ID)
	}
	if ev.VerificationArtifact.Digest != published[0].Digest {
		t.Fatalf("evidence verification_artifact %+v, want the published request artifact", ev.VerificationArtifact)
	}
	if len(ev.OutputBindings) != 1 || ev.OutputBindings[0].Name != "result" ||
		ev.OutputBindings[0].Artifact.Digest != fixtureDigest {
		t.Fatalf("evidence output_bindings %+v, want the resolved \"result\" slot", ev.OutputBindings)
	}

	// Only after that recorded evidence does the task actually succeed.
	transitions := e.ports.Transitions()
	if len(transitions) == 0 || transitions[len(transitions)-1].State != "succeeded" {
		t.Fatalf("task transitions %+v, want a final succeeded transition", transitions)
	}
	if got := e.readRun(e.readAttempt(a.ID).RunID); got.State != "succeeded" {
		t.Fatalf("run state %q, want succeeded", got.State)
	}
	if job := e.readVerificationJob(a.ID); job.State != "passed" {
		t.Fatalf("verification job state %q, want passed", job.State)
	}
}

// TestVerificationMissingOutputBytesPreventsSuccess is one of card P18's
// three required independently-preventing-success cases: a task requiring
// an independently verified artifact whose bytes were never actually
// published never reaches succeeded, through the real verifier reading a
// real (empty) blob store -- never a hand-built "failed" result.
func TestVerificationMissingOutputBytesPreventsSuccess(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})

	// The blob store the real verifier reads from has nothing at
	// fixtureDigest: the bound output's bytes are missing.
	blobs := newFakeBlobStore(nil)
	result := e.verifierResult([]byte(job.Request), blobs)
	if result.Status != "prerequisite_missing" {
		t.Fatalf("real verifier status %q, want prerequisite_missing for unreadable bytes", result.Status)
	}

	before := len(e.ports.Transitions())
	mustVerify(e, a, result)

	if got := e.readVerificationJob(a.ID); got.State != "prerequisite_missing" {
		t.Fatalf("verification job state %q, want prerequisite_missing", got.State)
	}
	// Inconclusive verification is not evidence of anything and must never
	// be handed to tasks as if it were: a missing/unavailable verifier
	// observation is not a failed one.
	if evidence := e.ports.EvidenceRecords(); len(evidence) != 0 {
		t.Fatalf("evidence records %+v, want none for an inconclusive prerequisite_missing verdict", evidence)
	}
	// The task is never told it succeeded, and it is not slammed into a
	// permanent failure either -- an unavailable verifier stays retryable:
	// no NEW transition landed as a result of this record call (the task
	// stays wherever the reported/verifying pipeline already left it).
	if after := e.ports.Transitions(); len(after) != before {
		t.Fatalf("task transitions grew from %d to %d recording an inconclusive verdict, want unchanged: %+v",
			before, len(after), after)
	}
}

// TestVerificationWrongOutputDigestPreventsSuccess is card P18's second
// independently-preventing-success case: the real verifier, reading bytes
// that do not hash to the pinned expected digest, fails the check and the
// task never reaches succeeded -- the failure is real evidence too, bound
// to the task before its failed transition.
func TestVerificationWrongOutputDigestPreventsSuccess(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})

	// Bytes are present at the pinned digest key, but they are not the
	// pinned content: reading them observes a different digest than the
	// one the acceptance contract requires.
	blobs := newFakeBlobStore(map[contract.Digest][]byte{fixtureDigest: []byte("tampered, not the accepted bytes")})
	result := e.verifierResult([]byte(job.Request), blobs)
	if result.Status != "failed" {
		t.Fatalf("real verifier status %q, want failed for a mismatched digest", result.Status)
	}

	mustVerify(e, a, result)

	if got := e.readVerificationJob(a.ID); got.State != "failed" {
		t.Fatalf("verification job state %q, want failed", got.State)
	}
	// A definitive check failure is real evidence: recorded, then the task
	// fails on it -- never succeeds.
	evidence := e.ports.EvidenceRecords()
	if len(evidence) != 1 || evidence[0].Verdict != "failed" {
		t.Fatalf("evidence records %+v, want exactly one failed verdict", evidence)
	}
	transitions := e.ports.Transitions()
	if len(transitions) == 0 || transitions[len(transitions)-1].State != "failed" {
		t.Fatalf("task transitions %+v, want a final failed transition", transitions)
	}
	if got := e.readRun(e.readAttempt(a.ID).RunID); got.State != "failed" {
		t.Fatalf("run state %q, want failed", got.State)
	}
}

// TestVerificationTamperedProfilePreventsSuccess is card P18's third
// independently-preventing-success case: even a genuine, otherwise-passing
// real-verifier result is refused once its pinned verifier identity is
// tampered with before being recorded -- a worker (or anything sitting
// between the verifier and the record call) cannot swap in an unapproved
// verifier/contract and still reach succeeded.
func TestVerificationTamperedProfilePreventsSuccess(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})

	blobs := newFakeBlobStore(map[contract.Digest][]byte{fixtureDigest: fixtureContent})
	result := e.verifierResult([]byte(job.Request), blobs)
	if result.Status != "passed" {
		t.Fatalf("real verifier status %q, want passed before tampering", result.Status)
	}
	// Tamper with the accepted verifier's own code digest after the real
	// verifier produced a genuine result but before it is recorded.
	result.VerifierCodeDigest = digestB

	before := len(e.ports.Transitions())
	f := e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: result,
	}, contract.CodeVerificationFailed)
	if f.Message == "" {
		t.Fatalf("expected a verification_failed fault naming the identity mismatch")
	}

	if got := e.readVerificationJob(a.ID); got.State != "pending" {
		t.Fatalf("verification job state %q, want still pending -- a tampered result is refused, not recorded", got.State)
	}
	if evidence := e.ports.EvidenceRecords(); len(evidence) != 0 {
		t.Fatalf("evidence records %+v, want none for a refused tampered result", evidence)
	}
	if after := e.ports.Transitions(); len(after) != before {
		t.Fatalf("task transitions grew from %d to %d for a refused tampered result, want unchanged: %+v",
			before, len(after), after)
	}
}

// TestVerificationFailedRequiredChildPreventsSuccess is card P18's fourth
// independently-preventing-success case: a parent acceptance contract
// naming a required child that has not itself independently succeeded
// blocks the parent's success even though the real verifier's own
// artifact checks genuinely pass -- the verification job keeps the honest
// verdict the checks earned, but the task cannot succeed on it.
func TestVerificationFailedRequiredChildPreventsSuccess(t *testing.T) {
	e := newEnv(t)
	childID := e.ids.New()
	_, a, job := reportedPipeline(e, func(task *wireTask) {
		task.Acceptance.RequiredChildIDs = []contract.ID{childID}
	}, wireUsage{Currency: "USD", Spent: 1})

	// The required child exists but has not succeeded.
	e.ports.setTask(&wireTask{
		ID: childID, Version: 1, Scope: e.scope, OwnerID: e.ids.New(), WorkerID: e.ids.New(),
		Outcome: "artifact", Inputs: []wireArtifactRef{}, RequiredOutputs: []string{},
		Acceptance: fixtureAcceptance("independent"), Limits: fixtureLimits(4),
		Dependencies: []contract.ID{}, State: "running",
	})

	blobs := newFakeBlobStore(map[contract.Digest][]byte{fixtureDigest: fixtureContent})
	result := e.verifierResult([]byte(job.Request), blobs)
	if result.Status != "passed" {
		t.Fatalf("real verifier status %q, want passed: the parent's own artifact checks are genuinely satisfied", result.Status)
	}

	mustVerify(e, a, result)

	// The verifier's own genuine finding is preserved, distinct from the
	// task-level dependency gate.
	if got := e.readVerificationJob(a.ID); got.State != "passed" {
		t.Fatalf("verification job state %q, want passed -- the checks really did pass", got.State)
	}
	evidence := e.ports.EvidenceRecords()
	if len(evidence) != 1 || evidence[0].Verdict != "failed" {
		t.Fatalf("evidence records %+v, want exactly one failed verdict (blocked on the required child)", evidence)
	}
	transitions := e.ports.Transitions()
	if len(transitions) == 0 || transitions[len(transitions)-1].State != "failed" {
		t.Fatalf("task transitions %+v, want a final failed transition blocked on the required child", transitions)
	}
	reasoned := e.readAttempt(a.ID)
	if !strings.Contains(reasoned.RecoveryReason, string(childID)) {
		t.Fatalf("recovery reason %q does not name the blocking required child %s", reasoned.RecoveryReason, childID)
	}
}

// TestVerificationCrashAtClaimAndRecordYieldsExactlyOneVerdict is card
// P18's third required behavior: a simulated crash/retry at verifier claim
// (a lost acknowledgement re-claims the same generation-bound token rather
// than minting a second one) and at record time (a caller that does not
// know whether its first record call actually committed retries it) each
// still yields exactly one verdict, correctly bound to the same task and
// attempt -- never a duplicate evidence record, publish or transition, and
// never a lost one.
func TestVerificationCrashAtClaimAndRecordYieldsExactlyOneVerdict(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 1})

	// Crash at claim: the first claim call's acknowledgement is lost, and
	// the caller retries under the exact same generation before ever
	// learning the outcome. Both calls must observe the one live claim.
	gen := e.generation()
	first := e.mustOK(opVerificationClaim, verificationClaimInput{
		RequestID: job.ID, ExpectedVersion: 1, Generation: gen,
	})
	var firstClaim verificationClaimBody
	e.decode(first.Data, &firstClaim)
	retry := e.mustOK(opVerificationClaim, verificationClaimInput{
		RequestID: job.ID, ExpectedVersion: 1, Generation: gen,
	})
	var retryClaim verificationClaimBody
	e.decode(retry.Data, &retryClaim)
	if retryClaim.ClaimToken != firstClaim.ClaimToken {
		t.Fatalf("crash-at-claim retry minted a new claim token %s, want the same %s",
			retryClaim.ClaimToken, firstClaim.ClaimToken)
	}

	// Invoke the real verifier outside any Unit, exactly once, against the
	// exact sealed request bytes the claim resolved to (byte-identical to
	// job.Request: nothing about the request changes between claim
	// replays) -- a crash after this point but before the caller learns
	// whether its record call landed is the next simulated fault.
	if retryClaim.Request.JobID != job.ID {
		t.Fatalf("claimed request job_id %s, want %s", retryClaim.Request.JobID, job.ID)
	}
	blobs := newFakeBlobStore(map[contract.Digest][]byte{fixtureDigest: fixtureContent})
	result := e.verifierResult([]byte(job.Request), blobs)
	if result.Status != "passed" {
		t.Fatalf("real verifier status %q, want passed", result.Status)
	}

	// First record call: this is the one that actually commits.
	mustVerify(e, a, result)

	// Crash at record: the caller never learned whether its first record
	// call committed and retries with the identical result. The retry must
	// be refused, not silently accepted as a second verdict.
	f := e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: a.ID, ExpectedVersion: a.Version, Result: result,
	}, contract.CodeConflict)
	if f.Message == "" {
		t.Fatalf("expected a conflict fault naming the already-recorded verdict")
	}

	// Exactly one verdict, one evidence record, one publish, one
	// succeeded transition -- correctly bound to this task and attempt.
	if got := e.readVerificationJob(a.ID); got.State != "passed" {
		t.Fatalf("verification job state %q, want passed exactly once", got.State)
	}
	evidence := e.ports.EvidenceRecords()
	if len(evidence) != 1 || evidence[0].TaskID != job.TaskID || evidence[0].AttemptID != a.ID {
		t.Fatalf("evidence records %+v, want exactly one bound to task %s attempt %s", evidence, job.TaskID, a.ID)
	}
	if len(e.ports.Published()) != 1 {
		t.Fatalf("published artifacts %+v, want exactly one publish", e.ports.Published())
	}
	succeeded := 0
	for _, tr := range e.ports.Transitions() {
		if tr.TaskID == job.TaskID && tr.State == "succeeded" {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("succeeded transitions for task %s: %d, want exactly 1", job.TaskID, succeeded)
	}
}
