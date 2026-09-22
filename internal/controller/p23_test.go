package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// ---- test helpers shared by this file ----

// jobWithInput commits one durable job exactly like job(), but with an
// explicit input document instead of the generic synthetic placeholder --
// needed for a job kind whose real owner-specific finish step (this file's
// configuration/skills tests) reads fields out of that exact sealed input.
func (f *fx) jobWithInput(owner, operation string, input json.RawMessage) contract.ID {
	id := contract.NewID()
	f.exec(`INSERT INTO execution_jobs (id, version, state, owner, operation, operation_id, input) VALUES (?, 1, 'pending', ?, ?, '', ?)`,
		string(id), owner, operation, string(input))
	return id
}

func (f *fx) seedConfigExportJob(jobID contract.ID) {
	f.exec(`INSERT INTO configuration_export_jobs (job_id, version) VALUES (?, 1)`, string(jobID))
}

func (f *fx) seedSkillEvaluation(evaluationID, jobID contract.ID, verifierID string) {
	f.exec(`INSERT INTO skills_evaluations (evaluation_id, job_id, version, verifier_id, state) VALUES (?, ?, 1, ?, 'pending')`,
		string(evaluationID), string(jobID), verifierID)
}

// ---- P23 item 3: skill evaluation and configuration export jobs reach
// their actual completion through their real, owner-specific finish path,
// not only the generic job ledger.

// configExportRunner mirrors internal/configuration's real RunJob
// (contract.LocalJobRunner) behavior exactly: it stages and publishes the
// sealed bundle bytes itself and mints the artifact id itself (the real
// owner has its own IDSource/BlobStore from Dependencies), returning a
// fully realized {"resource": ArtifactRef} result -- never a shortcut that
// hands the controller pre-baked success.
type configExportRunner struct {
	calls atomic.Int64
	blobs contract.BlobStore
}

func (r *configExportRunner) RunJob(ctx context.Context, job Job) (JobOutcome, error) {
	r.calls.Add(1)
	var in struct {
		Bundle json.RawMessage `json:"bundle"`
	}
	if err := json.Unmarshal(job.Input, &in); err != nil {
		return JobOutcome{}, err
	}
	stagingRef, digest, _, err := r.blobs.Stage(ctx, bytes.NewReader(in.Bundle), int64(len(in.Bundle)))
	if err != nil {
		return JobOutcome{}, err
	}
	if err := r.blobs.Publish(ctx, stagingRef, digest); err != nil {
		return JobOutcome{}, err
	}
	artifactID := contract.NewID()
	result, err := json.Marshal(map[string]any{"resource": map[string]any{"id": artifactID, "digest": digest}})
	if err != nil {
		return JobOutcome{}, err
	}
	return JobOutcome{State: jobStateSucceeded, Result: result, EvidenceIDs: []contract.ID{artifactID}}, nil
}

func TestConfigurationExportJobReachesRealOwnerFinish(t *testing.T) {
	f := newFx(t)
	blobs := newFakeBlobs()
	f.blobs = blobs
	runner := &configExportRunner{blobs: f.blobs}
	f.jobs = map[string]JobRunner{JobKey("configuration", "organization.export"): runner}
	input, _ := json.Marshal(map[string]any{"bundle": map[string]any{"format": "zatiti.organization/v1"}})
	job := f.jobWithInput("configuration", "organization.export", input)
	f.seedConfigExportJob(job)

	c, sess := f.started()
	for i := 0; i < 4; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if runner.calls.Load() != 1 {
		t.Fatalf("configuration's RunJob ran %d times, want exactly 1", runner.calls.Load())
	}
	if f.called("_execution.job.record") != 1 {
		t.Fatalf("_execution.job.record called %d times, want exactly 1", f.called("_execution.job.record"))
	}
	if f.called("_configuration.export.record") != 1 {
		t.Fatalf("_configuration.export.record called %d times, want exactly 1", f.called("_configuration.export.record"))
	}
	if got := f.jobState(job); got != "succeeded" {
		t.Fatalf("job ledger state is %s, want succeeded", got)
	}
	var artifactID, artifactDigest string
	if err := f.raw.Read(context.Background(), f.actor, f.scope(), func(u contract.Unit) error {
		return u.QueryRowContext(context.Background(), `SELECT artifact_id, artifact_digest FROM configuration_export_jobs WHERE job_id = ?`,
			string(job)).Scan(&artifactID, &artifactDigest)
	}); err != nil {
		t.Fatalf("read configuration_export_jobs: %v", err)
	}
	if artifactID == "" || artifactDigest == "" {
		t.Fatal("configuration's own local export-job row never recorded the published artifact")
	}
	if got := blobs.count(); got != 1 {
		t.Fatalf("blobs published %d times, want exactly 1", got)
	}
}

// skillEvaluateRunner mirrors internal/skills' real RunJob behavior: it
// independently observes checks, stages and publishes the evidence
// document itself, and returns skill.evaluate's exact completion_schema
// shape (evaluation_id, passed, evidence[]).
type skillEvaluateRunner struct {
	calls atomic.Int64
	blobs contract.BlobStore
	pass  bool
}

func (r *skillEvaluateRunner) RunJob(ctx context.Context, job Job) (JobOutcome, error) {
	r.calls.Add(1)
	var in struct {
		EvaluationID contract.ID `json:"evaluation_id"`
	}
	if err := json.Unmarshal(job.Input, &in); err != nil {
		return JobOutcome{}, err
	}
	doc, _ := json.Marshal(map[string]any{"schema": "zatiti.skill-evaluation-result/v1", "passed": r.pass})
	stagingRef, digest, _, err := r.blobs.Stage(ctx, bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return JobOutcome{}, err
	}
	if err := r.blobs.Publish(ctx, stagingRef, digest); err != nil {
		return JobOutcome{}, err
	}
	evidenceID := contract.NewID()
	result, err := json.Marshal(map[string]any{
		"evaluation_id": in.EvaluationID, "passed": r.pass,
		"evidence": []map[string]any{{"id": evidenceID, "digest": digest}},
	})
	if err != nil {
		return JobOutcome{}, err
	}
	return JobOutcome{State: jobStateSucceeded, Result: result, EvidenceIDs: []contract.ID{evidenceID}}, nil
}

func TestSkillEvaluationJobReachesRealOwnerFinish(t *testing.T) {
	f := newFx(t)
	f.blobs = newFakeBlobs()
	runner := &skillEvaluateRunner{blobs: f.blobs, pass: true}
	f.jobs = map[string]JobRunner{JobKey("skills", "skill.evaluate"): runner}
	evaluationID := contract.NewID()
	input, _ := json.Marshal(map[string]any{
		"evaluation_id": evaluationID,
		"acceptance":    map[string]any{"verifier_id": "synthetic-verifier", "verifier_version": "1"},
	})
	job := f.jobWithInput("skills", "skill.evaluate", input)
	f.seedSkillEvaluation(evaluationID, job, "synthetic-verifier")

	c, sess := f.started()
	for i := 0; i < 4; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if runner.calls.Load() != 1 {
		t.Fatalf("skills' RunJob ran %d times, want exactly 1", runner.calls.Load())
	}
	if f.called("_skills.evaluation.record") != 1 {
		t.Fatalf("_skills.evaluation.record called %d times, want exactly 1", f.called("_skills.evaluation.record"))
	}
	var verifierID, state string
	var passed int
	if err := f.raw.Read(context.Background(), f.actor, f.scope(), func(u contract.Unit) error {
		return u.QueryRowContext(context.Background(), `SELECT verifier_id, passed, state FROM skills_evaluations WHERE evaluation_id = ?`,
			string(evaluationID)).Scan(&verifierID, &passed, &state)
	}); err != nil {
		t.Fatalf("read skills_evaluations: %v", err)
	}
	if verifierID != "synthetic-verifier" || passed != 1 || state != "recorded" {
		t.Fatalf("evaluation row verifier=%s passed=%d state=%s, want synthetic-verifier/1/recorded", verifierID, passed, state)
	}
}

// ---- P23 item 5 / required test: a crash after a job's claim commits but
// before the controller durably acknowledged it locally is recovered by a
// direct lookup of that exact claim (not merely the pending scan, which by
// definition no longer lists an already-claimed job), and either records
// it unknown (the safe default) or safely resumes it per the attached
// runner's declared mode -- never silently loses track of it.

// modeRunner is a countingRunner that also declares a fixed resumability
// mode, so both branches of the required behavior are exercised against
// the exact same underlying claim-crash scenario.
type modeRunner struct {
	countingRunner
	resumable bool
}

func (r *modeRunner) ResumableAfterAmbiguousClaim() bool { return r.resumable }

var _ ResumableJobRunner = (*modeRunner)(nil)

// crashAfterClaimAck simulates the exact ambiguity window this test
// targets: _execution.job.claim's transaction genuinely commits (the fake
// handler's own DB write lands), but the acknowledgement back to the
// controller is lost AND the controller is marked abandoned in the same
// moment -- exactly as a real process death right after the claim
// committed, but before the local dispatch journal's phaseClaimed write,
// would leave things. The claimJob code path's own in-process retry then
// also finds the controller abandoned and gives up, leaving the journal at
// phaseAdmitted: the pre-claim version and the generation that attempted
// the claim, nothing more.
func crashAfterClaimAck() injection { return injection{crash: true} }

func TestJobClaimCrashRecovery_DurableLookup(t *testing.T) {
	t.Run("non-resumable: recorded unknown by direct lookup, runner never invoked", func(t *testing.T) {
		f := newFx(t)
		runner := &modeRunner{countingRunner: countingRunner{outcome: JobOutcome{State: jobStateSucceeded, Result: json.RawMessage(`{}`)}}, resumable: false}
		f.jobs = map[string]JobRunner{JobKey("artifacts", "artifact.export"): runner}
		job := f.job("artifacts", "artifact.export", "")

		c, sess := f.started()
		f.arm("_execution.job.claim", crashAfterClaimAck())
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
		// The claim committed server-side (fake handler's own write landed)
		// even though the controller never durably learned that locally.
		if got := f.jobState(job); got != "running" {
			t.Fatalf("job is %s after the simulated crash, want running (the claim really committed)", got)
		}
		if runner.calls.Load() != 0 {
			t.Fatalf("the runner ran before the claim was ever durably confirmed locally")
		}

		f.restart()
		c, sess = f.started()
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if got := f.called("_execution.job.claim"); got < 2 {
			t.Fatalf("direct claim lookup never retried the exact claim after restart (claim calls: %d)", got)
		}
		if runner.calls.Load() != 0 {
			t.Fatalf("a non-resumable runner was invoked for a job whose claim was merely confirmed by lookup: %d calls", runner.calls.Load())
		}
		if got := f.jobState(job); got != "outcome_unknown" {
			t.Fatalf("job is %s, want outcome_unknown", got)
		}
		s := c.Status()
		for _, o := range s.Obligations {
			if o.Kind == obligationJob && o.ResourceID == job {
				t.Fatalf("the resolved job still carries an open obligation: %+v", o)
			}
		}
	})

	t.Run("resumable: direct lookup confirms the claim and the runner completes it for real", func(t *testing.T) {
		f := newFx(t)
		runner := &modeRunner{countingRunner: countingRunner{outcome: JobOutcome{State: jobStateSucceeded, Result: json.RawMessage(`{"ok":true}`)}}, resumable: true}
		f.jobs = map[string]JobRunner{JobKey("artifacts", "artifact.export"): runner}
		job := f.job("artifacts", "artifact.export", "")

		c, sess := f.started()
		f.arm("_execution.job.claim", crashAfterClaimAck())
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
		if got := f.jobState(job); got != "running" {
			t.Fatalf("job is %s after the simulated crash, want running", got)
		}
		if runner.calls.Load() != 0 {
			t.Fatal("the runner ran before the claim was ever durably confirmed locally")
		}

		f.restart()
		c, sess = f.started()
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if runner.calls.Load() != 1 {
			t.Fatalf("resumable runner ran %d times after direct claim lookup, want exactly 1", runner.calls.Load())
		}
		if got := f.jobState(job); got != "succeeded" {
			t.Fatalf("job is %s, want succeeded (the resumable runner actually completed it)", got)
		}
		if got := f.queryString(`SELECT result FROM execution_jobs WHERE id = ?`, string(job)); got != `{"ok":true}` {
			t.Fatalf("result %s, want the runner's real completion", got)
		}
	})
}

// ---- P23 item 4 / required test: reconciliation calls Adapter.Reconcile
// exactly once per admitted read and never becomes a second physical
// effect, even across many ticks once the original operation's uncertainty
// is authoritatively resolved -- a reconcile call counter proves this,
// bounded only by ordinary pending scans thereafter.

func TestReconciliationCallsAdapterExactlyOnce(t *testing.T) {
	f := newFx(t)
	adapter := f.adapter("synthetic")
	adapter.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
		return contract.Observation{Disposition: contract.DispositionUnknown, Evidence: json.RawMessage(`{}`), Usage: json.RawMessage(fxUsage)}, nil
	}
	op := f.prepare("synthetic", nil)

	c, sess := f.started()
	if err := f.pass(c, sess); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := f.opState(op); got != "outcome_unknown" {
		t.Fatalf("operation is %s after the original dispatch, want outcome_unknown", got)
	}
	if adapter.reconciles() != 0 {
		t.Fatalf("Adapter.Reconcile was called before any reconciliation was ever admitted")
	}

	adapter.reconcileReply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
		return contract.Observation{Disposition: contract.DispositionSucceeded, Evidence: json.RawMessage(`{}`), Usage: json.RawMessage(fxUsage)}, nil
	}
	for i := 0; i < 6; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if got := adapter.reconciles(); got != 1 {
		t.Fatalf("Adapter.Reconcile called %d times across %d ticks, want exactly 1", got, 6)
	}
	if got := f.called("_effects.reconciliation.record"); got != 1 {
		t.Fatalf("_effects.reconciliation.record called %d times, want exactly 1 original write", got)
	}
	if got := f.called("_effects.admit"); got != 1 {
		t.Fatalf("_effects.admit called %d times; reconciliation must never replay the original write", got)
	}
	if got := f.opState(op); got != "succeeded" {
		t.Fatalf("operation is %s, want succeeded once the authoritative reconciliation merged in", got)
	}
	// Bounded lookup reads keep happening every tick (the pending scan
	// dispatch and driveReconciliation both use); that is expected and is
	// exactly what "bounded reads, one original write" means -- distinct
	// from a second physical effect, which reconciles() == 1 rules out.
	if got := f.called("_effects.pending"); got < 6 {
		t.Fatalf("expected the bounded pending scan to keep running every tick (%d calls)", got)
	}
}

// TestReconciliationNeverDoublesWhileOutstanding proves the journal-open
// guard: while one reconciliation read is still outstanding (claimed, not
// yet recorded), a later tick's discovery never admits a second one for the
// same operation.
func TestReconciliationNeverDoublesWhileOutstanding(t *testing.T) {
	f := newFx(t)
	adapter := f.adapter("synthetic")
	adapter.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
		return contract.Observation{Disposition: contract.DispositionUnknown, Evidence: json.RawMessage(`{}`), Usage: json.RawMessage(fxUsage)}, nil
	}
	op := f.prepare("synthetic", nil)
	c, sess := f.started()
	if err := f.pass(c, sess); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := f.opState(op); got != "outcome_unknown" {
		t.Fatalf("operation is %s, want outcome_unknown", got)
	}
	// Reconciliation's own record call is refused (retryable), so the read
	// stays outstanding (claimed, never recorded) across several ticks.
	f.arm("_effects.reconciliation.record", injection{
		fail:   &contract.Fault{Code: contract.CodeControllerUnavailable, Message: "synthetic outage", Retryable: true},
		sticky: true,
	})
	for i := 0; i < 5; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if got := adapter.reconciles(); got != 1 {
		t.Fatalf("Adapter.Reconcile called %d times while one read stayed outstanding, want exactly 1", got)
	}
}

// ---- P23 required test: the trusted verifier's own identity can never be
// replaced by a worker-supplied runner. Verification is driven exclusively
// through Collaborators.Verifier; a runner attached under any job key --
// including one that could plausibly be confused with a verification job
// -- is never consulted for it.

type identityCheckVerifier struct {
	calls atomic.Int64
}

func (v *identityCheckVerifier) Verify(_ context.Context, in contract.Verification) (contract.VerificationResult, error) {
	v.calls.Add(1)
	var probe verificationRequestProbe
	_ = json.Unmarshal(in.Request, &probe)
	doc, _ := json.Marshal(map[string]any{
		"schema": "zatiti.verification-result/v1", "job_id": probe.JobID, "task_id": probe.TaskID,
		"attempt_id": probe.AttemptID, "acceptance_digest": fxDigest,
		"verifier_id": "the-real-trusted-verifier", "verifier_version": "1", "verifier_code_digest": fxDigest,
		"request_artifact": map[string]any{"id": contract.NewID(), "digest": fxDigest},
		"status":           "passed", "observations": []any{},
		"started_at": "2026-03-01T12:00:00Z", "finished_at": "2026-03-01T12:00:01Z",
		"staged_outputs": []any{}, "output_artifacts": []any{}, "independent": true,
	})
	return contract.VerificationResult{Document: doc}, nil
}

// neverCalledRunner fails the test outright if the controller ever invokes
// it: standing in for a worker-authored job runner that must never be
// consulted for independent verification.
type neverCalledRunner struct{ t *testing.T }

func (r neverCalledRunner) RunJob(context.Context, Job) (JobOutcome, error) {
	r.t.Fatal("a worker-supplied job runner was invoked to establish verification; only the attached Verifier may")
	return JobOutcome{}, nil
}

func TestVerifierIdentityCannotBeReplacedByWorkerRunner(t *testing.T) {
	f := newFx(t)
	verifier := &identityCheckVerifier{}
	f.verifier = verifier
	// A plausibly-named job runner is attached under keys an attacker (or a
	// bug) might hope the controller confuses with verification. It must
	// never be reached.
	f.jobs = map[string]JobRunner{
		JobKey("execution", "verification"):        neverCalledRunner{t: t},
		JobKey("execution", "skill.evaluate"):      neverCalledRunner{t: t},
		JobKey("execution", "verification.claim"):  neverCalledRunner{t: t},
		JobKey("execution", "verification.record"): neverCalledRunner{t: t},
	}
	attemptID := contract.NewID()
	jobID := contract.NewID()
	req := verificationRequestDoc(jobID, attemptID, fxDigest)
	f.exec(`INSERT INTO execution_verification_requests (job_id, attempt_id, task_id, state, request) VALUES (?, ?, ?, 'pending', ?)`,
		string(jobID), string(attemptID), string(contract.NewID()), string(req))

	c, sess := f.started()
	for i := 0; i < 3; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if verifier.calls.Load() != 1 {
		t.Fatalf("the attached Verifier was called %d times, want exactly 1", verifier.calls.Load())
	}
	if got := f.called("_execution.verification.record"); got != 1 {
		t.Fatalf("_execution.verification.record called %d times, want exactly 1", got)
	}
	if got := f.queryString(`SELECT state FROM execution_verification_requests WHERE job_id = ?`, string(jobID)); got != "recorded" {
		t.Fatalf("verification request is %s, want recorded", got)
	}
}

// TestVerificationRunsOutsideTheTickLoop proves item 1 for verification:
// claiming is a quick transaction, but the trusted verifier call itself
// runs on a worker goroutine bounded by the same concurrency slots
// everything else outside a Unit shares -- the tick loop that claimed it
// returns without waiting for Verify to return.
func TestVerificationRunsOutsideTheTickLoop(t *testing.T) {
	f := newFx(t)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	verifier := &blockingVerifier{release: release, started: started}
	f.verifier = verifier
	attemptID := contract.NewID()
	jobID := contract.NewID()
	req := verificationRequestDoc(jobID, attemptID, fxDigest)
	f.exec(`INSERT INTO execution_verification_requests (job_id, attempt_id, task_id, state, request) VALUES (?, ?, ?, 'pending', ?)`,
		string(jobID), string(attemptID), string(contract.NewID()), string(req))

	c, sess := f.started()
	tickDone := make(chan error, 1)
	go func() { tickDone <- c.tick(context.Background(), context.Background(), sess) }()
	select {
	case err := <-tickDone:
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("tick never returned; it appears to be blocking on the verifier call")
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the verifier was never actually invoked")
	}
	close(release)
	c.workers.Wait()
	if got := f.called("_execution.verification.record"); got != 1 {
		t.Fatalf("_execution.verification.record called %d times, want exactly 1", got)
	}
}

type blockingVerifier struct {
	release <-chan struct{}
	started chan struct{}
}

func (v *blockingVerifier) Verify(_ context.Context, in contract.Verification) (contract.VerificationResult, error) {
	v.started <- struct{}{}
	<-v.release
	var probe verificationRequestProbe
	_ = json.Unmarshal(in.Request, &probe)
	doc, _ := json.Marshal(map[string]any{
		"schema": "zatiti.verification-result/v1", "job_id": probe.JobID, "task_id": probe.TaskID,
		"attempt_id": probe.AttemptID, "acceptance_digest": fxDigest,
		"verifier_id": "blocking-verifier", "verifier_version": "1", "verifier_code_digest": fxDigest,
		"request_artifact": map[string]any{"id": contract.NewID(), "digest": fxDigest},
		"status":           "passed", "observations": []any{},
		"started_at": "2026-03-01T12:00:00Z", "finished_at": "2026-03-01T12:00:01Z",
		"staged_outputs": []any{}, "output_artifacts": []any{}, "independent": true,
	})
	return contract.VerificationResult{Document: doc}, nil
}
