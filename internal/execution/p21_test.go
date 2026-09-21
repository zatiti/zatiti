package execution

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// P21: cooperative recovery and run export jobs. These tests exercise the
// real public operation boundaries (never internal shortcuts) for the
// external-worker lifecycle, prove the cooperative claim context is a real,
// independently-checkable published document rather than a fabricated
// reference, prove replacement is blocked by real unresolved-effect
// recovery obligations and real cumulative budget accounting, prove a
// report is fenced to its actual authenticated worker identity, and prove
// run.export assembles and durably publishes a genuinely readable
// canonical history while preserving uncertainty across a simulated
// restart between its perform and record phases.

// driveDocumentJob runs a document-publish job's perform phase (outside any
// Unit, exactly as a future controller would) and its record phase (inside
// one), returning the published Artifact. It is the test-side stand-in for
// the controller this package does not yet own (Wave 3), exactly as
// context_build_test.go calls stageContext directly to prove that phase's
// own behavior.
func (e *testEnv) driveDocumentJob(j *jobRow) wireArtifact {
	e.t.Helper()
	var in documentJobInput
	if err := json.Unmarshal(j.Input, &in); err != nil {
		e.t.Fatalf("decode document job input: %v", err)
	}
	outcome, err := e.svc.RunJob(e.ctx, contract.JobWork{
		ID: j.ID, Version: j.Version, Owner: j.Owner, Operation: j.Operation,
		Scope: j.Scope, Input: j.Input,
	})
	if err != nil {
		e.t.Fatalf("RunJob (perform phase): %v", err)
	}
	if outcome.State != "succeeded" {
		e.t.Fatalf("RunJob outcome state %q, want succeeded", outcome.State)
	}
	var staged struct {
		Digest         contract.Digest `json:"digest"`
		Size           int64           `json:"size"`
		MediaType      string          `json:"media_type"`
		Classification string          `json:"classification"`
	}
	if err := json.Unmarshal(outcome.Result, &staged); err != nil {
		e.t.Fatalf("decode RunJob result: %v", err)
	}
	var published wireArtifact
	e.inWrite(func(unit contract.Unit) error {
		row, err := loadJobForUpdate(e.ctx, unit, j.ID, j.Version)
		if err != nil {
			return err
		}
		published, err = e.svc.recordDocumentJob(e.ctx, unit, row,
			wireArtifactRef{ID: uuidFromDigest(staged.Digest), Digest: staged.Digest},
			staged.Size, in.MediaType, in.Classification, e.clock.Now())
		return err
	})
	return published
}

// TestCooperativeWorkerClaimsChecksInReportsAndVerifiesThroughPublicOperations
// is P21's first required behavioral test: an external, non-hosted
// cooperative worker drives its entire lifecycle -- claim, checkpoint,
// report -- through exactly the supported public operations, and the task
// reaches succeeded only through the trusted, independent verifier
// (NewVerifier/opVerificationRec), never a worker-supplied disposition.
func TestCooperativeWorkerClaimsChecksInReportsAndVerifiesThroughPublicOperations(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureCooperativeProfile(worker))
	run := e.enqueueTask(worker, nil)

	// Claim: public run.claim, no controller/tick in the loop at all --
	// cooperative executors are reachable only through this path.
	claim := e.mustOK(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker, ExpectedVersion: 1,
		Capabilities: []string{"model.steps"},
	})
	var claimed claimBody
	e.decode(claim.Data, &claimed)
	if claimed.Attempt.Executor != "cooperative" {
		t.Fatalf("attempt executor %q, want cooperative", claimed.Attempt.Executor)
	}
	if claimed.Context.ID == "" || claimed.Context.Digest == "" {
		t.Fatalf("claim context %+v, want a real reference, not an empty one", claimed.Context)
	}

	// Checkpoint: public attempt.checkpoint, the worker's own published
	// context taking over from the synthesized advisory one.
	a := e.readAttempt(claimed.Attempt.ID)
	ownContext := wireArtifactRef{ID: e.ids.New(), Digest: digestA}
	e.mustOK(opAttemptCheckpoint, checkpointInput{
		Scope: e.scope, AttemptID: a.ID, LeaseID: a.LeaseID, Generation: a.Generation,
		ExpectedVersion: a.Version, Context: ownContext, Outputs: []wireArtifactRef{},
	})
	a = e.readAttempt(claimed.Attempt.ID)
	if a.ContextArtifact == nil || a.ContextArtifact.Digest != digestA {
		t.Fatalf("attempt context after checkpoint %+v, want the worker's own %s", a.ContextArtifact, digestA)
	}

	// Report: public attempt.report.
	e.mustOK(opAttemptReport, reportInput{
		Scope: e.scope, AttemptID: a.ID, LeaseID: a.LeaseID, Generation: a.Generation,
		ExpectedVersion: a.Version, Outputs: []wireArtifactRef{e.fixtureOutputRef()},
		Observations: []byte(`{}`), Usage: wireUsage{Currency: "USD", Spent: 42},
	})
	a = e.readAttempt(claimed.Attempt.ID)
	if a.State != "reported" {
		t.Fatalf("attempt state %q after report, want reported", a.State)
	}

	// Independent verification: the real trusted verifier, never a
	// worker-supplied "passed" claim.
	job := e.readVerificationJob(a.ID)
	blobs := newFakeBlobStore(map[contract.Digest][]byte{fixtureDigest: fixtureContent})
	result := e.verifierResult([]byte(job.Request), blobs)
	if result.Status != "passed" {
		t.Fatalf("real verifier status %q, want passed", result.Status)
	}
	mustVerify(e, a, result)

	if got := e.readRun(run.ID); got.State != "succeeded" {
		t.Fatalf("run state %q, want succeeded", got.State)
	}
	evidence := e.ports.EvidenceRecords()
	if len(evidence) != 1 || evidence[0].Verdict != "passed" {
		t.Fatalf("evidence records %+v, want exactly one passed verdict", evidence)
	}
}

// TestCooperativeClaimContextPublishesRealAdvisoryDocument is P21 item 1's
// core proof: a fresh cooperative claim's synthesized context is not the
// old digest-only synthetic envelope (a fabricated reference nothing ever
// staged) but a real durable document-publish commitment whose eventual
// bytes -- independently reconstructible and separately driven through the
// staging/record mechanism here -- genuinely decode to the advisory
// disclaimer and the worker's required capabilities the claim promised.
func TestCooperativeClaimContextPublishesRealAdvisoryDocument(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureCooperativeProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)

	// The digest is not fabricated: independently recomputing the exact
	// same deterministic content from the same underlying data (the
	// attempt, the run, the profile's required capabilities and current
	// bindings) yields the identical digest the claim returned.
	a := e.readAttempt(claim.Attempt.ID)
	r := e.readRun(run.ID)
	expected, err := buildClaimContext(a, r, []string{"model.steps"}, []wireBinding{})
	if err != nil {
		t.Fatalf("buildClaimContext: %v", err)
	}
	if got := sha256Hex(expected); got != claim.Context.Digest {
		t.Fatalf("claim context digest %s, want the independently recomputed %s", claim.Context.Digest, got)
	}

	// The durable job this claim committed to exists, keyed by the exact
	// same content reference, and is genuinely pending -- not a bare digest
	// pointing at nothing.
	var job *jobRow
	e.inWrite(func(unit contract.Unit) error {
		var jerr error
		job, jerr = findJobBySourceID(e.ctx, unit, claim.Context.ID)
		return jerr
	})
	if job == nil || job.Kind != "claim_context" || job.State != "pending" {
		t.Fatalf("claim context job %+v, want a pending claim_context job", job)
	}

	// Drive the job to completion (perform outside any Unit, then record)
	// and read the actually-published bytes back.
	published := e.driveDocumentJob(job)
	if published.Digest != claim.Context.Digest {
		t.Fatalf("published artifact digest %s, want the claim's own context digest %s", published.Digest, claim.Context.Digest)
	}
	rc, err := e.svc.deps.Blobs.Open(e.ctx, published.Digest, 0, -1)
	if err != nil {
		t.Fatalf("open published context bytes: %v", err)
	}
	defer func() { _ = rc.Close() }()
	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read published context bytes: %v", err)
	}
	var doc wireClaimContext
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode published claim context: %v", err)
	}
	if doc.Schema != claimContextSchema {
		t.Fatalf("published document schema %q, want %q", doc.Schema, claimContextSchema)
	}
	if !strings.Contains(doc.Advisory, "advisory") {
		t.Fatalf("published document advisory text %q, want an explicit advisory disclaimer", doc.Advisory)
	}
	if len(doc.RequiredCapabilities) != 1 || doc.RequiredCapabilities[0] != "model.steps" {
		t.Fatalf("published document required_capabilities %v, want [model.steps]", doc.RequiredCapabilities)
	}
	if doc.Capture != "advisory" {
		t.Fatalf("published document capture %q, want advisory", doc.Capture)
	}

	// The job's own terminal result now names the real artifact -- the
	// terminal job callback a caller inspects through job.get.
	got := e.mustOK(opJobGet, getIDInput{Scope: e.scope, ID: job.ID})
	var body jobBody
	e.decode(got.Data, &body)
	if body.Resource.State != "succeeded" || body.Resource.ResultArtifact == nil ||
		body.Resource.ResultArtifact.Digest != claim.Context.Digest {
		t.Fatalf("job.get resource %+v, want succeeded with the claim context as result_artifact", body.Resource)
	}
}

// TestLostClaimAckReplaysSameLeaseAndContext is half of P21's second
// required behavioral test: a repeated claim by the same worker (a lost
// network acknowledgement) recovers the exact same attempt, lease and
// context reference -- never a second owner or a merely-similar context.
func TestLostClaimAckReplaysSameLeaseAndContext(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureCooperativeProfile(worker))
	run := e.enqueueTask(worker, nil)
	first := e.claimRun(run.ID, worker)

	payload := e.mustOK(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: 2, Capabilities: []string{"model.steps"},
	})
	var replay claimBody
	e.decode(payload.Data, &replay)
	if replay.Attempt.ID != first.Attempt.ID || replay.Attempt.LeaseID != first.Attempt.LeaseID {
		t.Fatalf("replay attempt/lease %s/%s, want the original %s/%s",
			replay.Attempt.ID, replay.Attempt.LeaseID, first.Attempt.ID, first.Attempt.LeaseID)
	}
	if replay.Context != first.Context {
		t.Fatalf("replay context %+v, want the identical original %+v", replay.Context, first.Context)
	}
}

// TestStaleHeartbeatCannotReviveSupersededClaim is the other half of P21's
// second required behavioral test: once an attempt is superseded (fenced by
// lease expiry, replaced by a fresh attempt), its old lease/generation can
// never mutate current execution again -- a stale heartbeat is refused, not
// silently accepted as if it were still live.
func TestStaleHeartbeatCannotReviveSupersededClaim(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureCooperativeProfile(worker))
	run := e.enqueueTask(worker, nil)
	original := e.claimRun(run.ID, worker)

	// Outlive the lease and fence it via the ordinary tick scan.
	e.clock.advance(2 * leaseDuration)
	e.mustOK(opTick, tickInput{Now: formatStamp(e.clock.Now()), Limit: 100})
	if got := e.readAttempt(original.Attempt.ID); got.State != "fenced" {
		t.Fatalf("original attempt state %q, want fenced", got.State)
	}

	// A fresh replacement supersedes it (a bare lease_conflict obligation
	// from routine lease expiry does not itself block replacement).
	replacement := e.claimRun(run.ID, worker)
	if replacement.Attempt.ID == original.Attempt.ID {
		t.Fatalf("replacement reused the original attempt %s", original.Attempt.ID)
	}

	// The stale heartbeat, still carrying the original (now-fenced)
	// attempt's own lease/generation, is refused -- it can never mutate
	// current execution again.
	f := e.expectFault(opAttemptHeartbeat, heartbeatInput{
		Scope: e.scope, AttemptID: original.Attempt.ID, LeaseID: original.Attempt.LeaseID,
		Generation: original.Attempt.Generation, ExpectedVersion: e.readAttempt(original.Attempt.ID).Version,
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "fenced") {
		t.Fatalf("stale heartbeat fault %q, want it to name the fenced state", f.Message)
	}
	// The live replacement is entirely unaffected.
	if got := e.readAttempt(replacement.Attempt.ID); got.State != "claimed" {
		t.Fatalf("replacement attempt state %q, want claimed and untouched", got.State)
	}
}

// TestReplacementBlockedByUnresolvedProviderEffectObligation is P21 item 2's
// core proof (Z10.conflicting_replacement): an attempt fenced while it had
// a genuinely dispatched, unconfirmed provider effect leaves the run unable
// to admit a replacement while that specific obligation stands -- a stale
// lease alone is never read as proof the prior external process stopped --
// and the blocking obligation is itself inspectable through run.recovery
// (P21 item 4), not just an opaque refusal.
func TestReplacementBlockedByUnresolvedProviderEffectObligation(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimContextTick(run.ID, worker)

	prepared := e.ports.EffectsPrepared()
	if len(prepared) != 1 {
		t.Fatalf("effects prepared %+v, want exactly one dispatched model_step", prepared)
	}
	opID := prepared[0].ID

	// The dispatched effect's outcome comes back genuinely unknown (e.g. a
	// timeout after the request was sent) -- never assumed to be nonexecution.
	e.mustOK(opObservation, observationInput{
		AttemptID: claim.Attempt.ID, OperationID: opID,
		Observation: wireObservation{Disposition: "unknown", Evidence: []byte(`{}`), Usage: wireUsage{Currency: "USD"}},
	})
	if got := e.readAttempt(claim.Attempt.ID); got.State != "fenced" {
		t.Fatalf("attempt state %q after an unknown effect observation, want fenced", got.State)
	}
	obligations := e.readObligations("run_id", run.ID)
	found := false
	for _, o := range obligations {
		if o.Code == "unknown_effect" {
			found = true
		}
	}
	if !found {
		t.Fatalf("run obligations %+v, want an unknown_effect obligation recorded", obligations)
	}

	// Replacement is refused while that obligation stands.
	f := e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker, ExpectedVersion: e.readRun(run.ID).Version,
		Capabilities: []string{"model.steps"},
	}, contract.CodePrerequisiteMissing)
	if !strings.Contains(f.Message, "effect") {
		t.Fatalf("blocked replacement fault %q, want it to name the unresolved effect obligation", f.Message)
	}

	// P21 item 4: the blocking obligation itself is inspectable through
	// run.recovery, so a caller can actually see what's blocked and why --
	// not just receive an opaque refusal from run.claim.
	recovered := e.mustOK(opRunRecovery, getIDInput{Scope: e.scope, ID: run.ID})
	var recovery recoveryBody
	e.decode(recovered.Data, &recovery)
	named := false
	for _, o := range recovery.Obligations {
		if o.Code == "unknown_effect" && o.ResourceID != "" {
			named = true
		}
	}
	if !named {
		t.Fatalf("run.recovery obligations %+v, want the unknown_effect obligation with its owning operation named", recovery.Obligations)
	}

	// This obligation kind currently has no resolution path reachable from
	// inside this package once the attempt that recorded it is fenced (see
	// handleObservation's own note): replacement stays refused, and the run
	// stays waiting for genuine out-of-band recovery -- never an inferred
	// or claim-time shortcut around the uncertainty.
	f = e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker, ExpectedVersion: e.readRun(run.ID).Version,
		Capabilities: []string{"model.steps"},
	}, contract.CodePrerequisiteMissing)
	if !strings.Contains(f.Message, "effect") {
		t.Fatalf("still-blocked replacement fault %q, want it to keep naming the unresolved effect obligation", f.Message)
	}
}

// TestCumulativeModelStepsBlockReplacementAcrossAttempts is P21 item 2's
// budget-replacement proof: a task's model-step bound is enforced against
// the run's real cumulative total across every attempt it has admitted, so
// a worker cannot accumulate more total model steps than the limit allows
// just by being replaced repeatedly.
func TestCumulativeModelStepsBlockReplacementAcrossAttempts(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, func(task *wireTask) { task.Limits.ModelSteps = 1 })
	claim := e.claimContextTick(run.ID, worker)

	prepared := e.ports.EffectsPrepared()
	if len(prepared) != 1 {
		t.Fatalf("effects prepared %+v, want exactly one dispatched model_step", prepared)
	}
	// A conclusive success consumes the run's one and only allowed step and
	// fences the attempt on the now-reached bound.
	e.mustOK(opObservation, observationInput{
		AttemptID: claim.Attempt.ID, OperationID: prepared[0].ID,
		Observation: wireObservation{
			Disposition: "succeeded", Evidence: []byte(`{}`),
			Usage: wireUsage{Currency: "USD", Spent: 1},
		},
	})
	if got := e.readAttempt(claim.Attempt.ID); got.State != "fenced" {
		t.Fatalf("attempt state %q after reaching the model step bound, want fenced", got.State)
	}
	if got := e.readRun(run.ID); got.ModelStepsUsed != 1 {
		t.Fatalf("run cumulative model_steps_used %d, want 1", got.ModelStepsUsed)
	}

	// A fresh attempt's own counter would start at zero, but the run's
	// cumulative total already reached the bound -- replacement is refused.
	f := e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker, ExpectedVersion: e.readRun(run.ID).Version,
		Capabilities: []string{"model.steps"},
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "model step bound") {
		t.Fatalf("blocked replacement fault %q, want it to name the cumulative model step bound", f.Message)
	}
}

// TestRootDeadlinePassedRefusesClaim is P21 item 2's deadline proof: a
// task's root deadline is a fixed absolute bound that a claim cannot admit
// past, regardless of how many attempts preceded it.
func TestRootDeadlinePassedRefusesClaim(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureCooperativeProfile(worker))
	run := e.enqueueTask(worker, func(task *wireTask) {
		task.Limits.RootDeadline = formatStamp(e.clock.Now().Add(-time.Minute))
	})
	f := e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker, ExpectedVersion: 1,
		Capabilities: []string{"model.steps"},
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "root deadline") {
		t.Fatalf("refused claim fault %q, want it to name the root deadline", f.Message)
	}
}

// TestReportRefusedFromDifferentWorkerActor is P21 item 5's core proof: a
// report whose authenticated actor is a different worker than the one the
// claim is actually bound to is refused, regardless of what lease_id,
// generation or attempt_id the request otherwise supplies -- a report
// claiming to be from a different worker than the one holding the claim is
// never silently accepted.
func TestReportRefusedFromDifferentWorkerActor(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	impostor := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureCooperativeProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)

	in := reportInput{
		Scope: e.scope, AttemptID: a.ID, LeaseID: a.LeaseID, Generation: a.Generation,
		ExpectedVersion: a.Version, Outputs: []wireArtifactRef{e.fixtureOutputRef()},
		Observations: []byte(`{}`), Usage: wireUsage{Currency: "USD", Spent: 1},
	}
	impostorActor := contract.Actor{PrincipalID: impostor, Kind: contract.KindWorker}
	payload, err := e.callAsActor(impostorActor, e.scope, opAttemptReport, in)
	var f *contract.Fault
	if err != nil {
		errors.As(err, &f)
	}
	if f == nil {
		f = payload.Error
	}
	if f == nil || f.Code != contract.CodePermissionDenied {
		t.Fatalf("impostor report fault %v, want permission_denied", f)
	}
	if got := e.readAttempt(a.ID); got.State != "claimed" {
		t.Fatalf("attempt state %q after a refused impostor report, want unchanged claimed", got.State)
	}

	// The legitimate worker's own actor identity still succeeds.
	legitimateActor := contract.Actor{PrincipalID: worker, Kind: contract.KindWorker}
	_, err = e.callAsActor(legitimateActor, e.scope, opAttemptReport, in)
	if err != nil {
		t.Fatalf("legitimate worker report: %v", err)
	}
	if got := e.readAttempt(a.ID); got.State != "reported" {
		t.Fatalf("attempt state %q after the legitimate worker's report, want reported", got.State)
	}
}

// TestRunExportReachesSucceededWithReadableCanonicalHistory is P21's third
// required behavioral test (first half): run.export assembles the full
// canonical history -- accepted task, attempt, effects, outputs,
// observations and the independently recorded verifier evidence with its
// source digests -- and, once its durable job is driven to completion,
// produces genuinely readable bytes a caller can decode and check field by
// field, not an opaque or partial stand-in.
func TestRunExportReachesSucceededWithReadableCanonicalHistory(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 7})
	blobs := newFakeBlobStore(map[contract.Digest][]byte{fixtureDigest: fixtureContent})
	result := e.verifierResult([]byte(job.Request), blobs)
	if result.Status != "passed" {
		t.Fatalf("real verifier status %q, want passed", result.Status)
	}
	mustVerify(e, a, result)
	run := e.readRun(a.RunID)
	if run.State != "succeeded" {
		t.Fatalf("run state %q, want succeeded before export", run.State)
	}

	exportPayload := e.mustOK(opRunExport, getIDInput{Scope: e.scope, ID: run.ID})
	var exportJob jobBody
	e.decode(exportPayload.Data, &exportJob)
	if exportJob.Resource.Kind != "run_export" || exportJob.Resource.State != "pending" {
		t.Fatalf("export job %+v, want a pending run_export job", exportJob.Resource)
	}

	var raw jobRow
	e.inWrite(func(unit contract.Unit) error {
		row, err := loadJob(e.ctx, unit, exportJob.Resource.ID)
		if err == nil {
			raw = *row
		}
		return err
	})
	published := e.driveDocumentJob(&raw)
	if published.Digest == "" {
		t.Fatalf("published export artifact %+v, want a real digest", published)
	}

	rc, err := e.svc.deps.Blobs.Open(e.ctx, published.Digest, 0, -1)
	if err != nil {
		t.Fatalf("open published export bytes: %v", err)
	}
	defer func() { _ = rc.Close() }()
	content, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read published export bytes: %v", err)
	}
	var history wireRunExportHistory
	if err := json.Unmarshal(content, &history); err != nil {
		t.Fatalf("decode published run export history: %v", err)
	}
	if history.Schema != runExportSchema {
		t.Fatalf("history schema %q, want %q", history.Schema, runExportSchema)
	}
	if history.Task.ID != job.TaskID {
		t.Fatalf("history task id %s, want %s", history.Task.ID, job.TaskID)
	}
	if len(history.Attempts) != 1 || history.Attempts[0].Attempt.ID != a.ID {
		t.Fatalf("history attempts %+v, want exactly one matching attempt %s", history.Attempts, a.ID)
	}
	if history.Attempts[0].Verification == nil || history.Attempts[0].Verification.State != "passed" {
		t.Fatalf("history verification %+v, want a passed verifier record", history.Attempts[0].Verification)
	}
	if history.Attempts[0].Verification.AcceptanceDigest != job.AcceptanceDigest {
		t.Fatalf("history verification acceptance_digest %s, want %s",
			history.Attempts[0].Verification.AcceptanceDigest, job.AcceptanceDigest)
	}
	if history.Attempts[0].Verification.RequestDigest != sha256Hex(job.Request) {
		t.Fatalf("history verification request_digest %s, want the sealed request's own digest %s",
			history.Attempts[0].Verification.RequestDigest, sha256Hex(job.Request))
	}
	if len(history.Attempts[0].Outputs) != 1 {
		t.Fatalf("history attempt outputs %+v, want the one reported output", history.Attempts[0].Outputs)
	}
	if history.TotalCosts.Spent != 7 {
		t.Fatalf("history total_costs.spent %d, want 7", history.TotalCosts.Spent)
	}

	// job.get's own terminal result names this same real artifact.
	got := e.mustOK(opJobGet, getIDInput{Scope: e.scope, ID: exportJob.Resource.ID})
	var body jobBody
	e.decode(got.Data, &body)
	if body.Resource.State != "succeeded" || body.Resource.ResultArtifact == nil ||
		body.Resource.ResultArtifact.Digest != published.Digest {
		t.Fatalf("job.get resource %+v, want succeeded naming the published history artifact", body.Resource)
	}

	// P21 item 4: the terminal job callback -- a kind-specific event beyond
	// the generic job.recorded, so a caller watching run-level completion
	// observes this export's own completion, not just a job ID it has to
	// separately poll.
	var sawTerminalCallback bool
	for _, ev := range e.readEvents() {
		if ev.Kind == eventRunExportSucceeded && ev.ResourceID == string(exportJob.Resource.ID) {
			sawTerminalCallback = true
		}
	}
	if !sawTerminalCallback {
		t.Fatalf("no %s terminal callback event for job %s", eventRunExportSucceeded, exportJob.Resource.ID)
	}
}

// TestRunExportRestartPreservesUncertainty is P21's third required
// behavioral test (second half): a simulated restart between the export
// job's perform phase (bytes actually staged outside any Unit) and its
// record phase (the Unit-bound step that would register real metadata and
// mark it succeeded) leaves job.get still reporting the honest pending
// state -- never a fabricated success invented from the fact that the
// bytes merely happen to already exist -- and the export can still be
// completed afterward without inventing or losing anything.
func TestRunExportRestartPreservesUncertainty(t *testing.T) {
	e := newEnv(t)
	_, a, job := reportedPipeline(e, nil, wireUsage{Currency: "USD", Spent: 3})
	blobs := newFakeBlobStore(map[contract.Digest][]byte{fixtureDigest: fixtureContent})
	result := e.verifierResult([]byte(job.Request), blobs)
	mustVerify(e, a, result)
	run := e.readRun(a.RunID)

	exportPayload := e.mustOK(opRunExport, getIDInput{Scope: e.scope, ID: run.ID})
	var exportJob jobBody
	e.decode(exportPayload.Data, &exportJob)

	var raw jobRow
	e.inWrite(func(unit contract.Unit) error {
		row, err := loadJob(e.ctx, unit, exportJob.Resource.ID)
		if err == nil {
			raw = *row
		}
		return err
	})

	// Perform phase only: bytes are genuinely staged outside any Unit, as a
	// controller crashing right after this point would leave them.
	outcome, err := e.svc.RunJob(e.ctx, contract.JobWork{
		ID: raw.ID, Version: raw.Version, Owner: raw.Owner, Operation: raw.Operation,
		Scope: raw.Scope, Input: raw.Input,
	})
	if err != nil || outcome.State != "succeeded" {
		t.Fatalf("RunJob perform phase: outcome=%+v err=%v", outcome, err)
	}

	// Before the record phase ever runs (the simulated restart), the job's
	// own inspectable state still honestly reports pending -- not succeeded,
	// not any invented disposition -- even though the bytes already exist.
	mid := e.mustOK(opJobGet, getIDInput{Scope: e.scope, ID: exportJob.Resource.ID})
	var midBody jobBody
	e.decode(mid.Data, &midBody)
	if midBody.Resource.State != "pending" {
		t.Fatalf("job state after perform but before record %q, want pending (uncertainty preserved)", midBody.Resource.State)
	}
	if midBody.Resource.ResultArtifact != nil {
		t.Fatalf("job result_artifact %+v before the record phase ever ran, want none", midBody.Resource.ResultArtifact)
	}

	// Recovery: the record phase can still complete the job afterward,
	// using the exact bytes the perform phase already staged -- nothing was
	// lost, and nothing was fabricated in between.
	published := e.driveDocumentJob(&raw)
	final := e.mustOK(opJobGet, getIDInput{Scope: e.scope, ID: exportJob.Resource.ID})
	var finalBody jobBody
	e.decode(final.Data, &finalBody)
	if finalBody.Resource.State != "succeeded" || finalBody.Resource.ResultArtifact == nil ||
		finalBody.Resource.ResultArtifact.Digest != published.Digest {
		t.Fatalf("job state after recovery %+v, want succeeded naming %s", finalBody.Resource, published.Digest)
	}
}
