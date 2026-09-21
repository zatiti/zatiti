package skills

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// The skill.evaluate durable job seam: RunJob (the frozen local job
// executor) performs real verification against published fixture artifacts,
// and _skills.evaluation.record durably finalizes the sealed evaluation row
// activation reads. These tests exercise the whole seam directly against
// Service.Handle/Service.RunJob the way the controller would drive it --
// never a fake standing in for the real behavior.

// digestCheck builds one artifact_digest expected observation pinned to a
// real published fixture's digest.
func digestCheck(checkID string, digest contract.Digest, expected string) wireExpectedObservation {
	return wireExpectedObservation{
		CheckID: checkID, Kind: "artifact_digest", Expected: expected,
		ExpectedDigest: string(digest),
	}
}

// admitEvaluation runs skill.evaluate and returns the sealed job resource
// plus the exact input that admitted it.
func (e *testEnv) admitEvaluation(in evaluateInput) wireJob {
	e.t.Helper()
	payload := e.mustOK("skill.evaluate", in)
	var out jobOutput
	e.decode(payload.Data, &out)
	return out.Resource
}

// runEvaluationJob builds the exact JobWork the controller would hand
// RunJob -- decoded from the real _execution.job.create call skill.evaluate
// made, not reconstructed by hand -- and drives it.
func (e *testEnv) runEvaluationJob(t *testing.T, job wireJob) contract.JobOutcome {
	t.Helper()
	calls := e.ports.callsOf("_execution.job.create")
	var work contract.JobWork
	for _, c := range calls {
		var created struct {
			Owner     string          `json:"owner"`
			Operation string          `json:"operation"`
			Input     json.RawMessage `json:"input"`
			SourceID  contract.ID     `json:"source_id"`
		}
		if err := json.Unmarshal(c.Input, &created); err != nil {
			t.Fatalf("decode _execution.job.create call: %v", err)
		}
		if created.SourceID == job.ID {
			work = contract.JobWork{
				ID: job.OperationID, Owner: created.Owner, Operation: created.Operation,
				Scope: e.scope.toContract(), Input: created.Input,
			}
		}
	}
	if work.Owner == "" {
		t.Fatalf("no _execution.job.create call admitted evaluation %s", job.ID)
	}
	outcome, err := e.svc.RunJob(e.ctx, work)
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	return outcome
}

// recordEvaluation calls _skills.evaluation.record the way execution/
// controller would, using RunJob's own outcome for verifier identity,
// evidence and verdict.
func (e *testEnv) recordEvaluation(job wireJob, expectedVersion int64, verifierID, verifierVersion string, outcome contract.JobOutcome, passed bool) (contract.Payload, error) {
	evidence := outcome.EvidenceIDs
	if evidence == nil {
		evidence = []contract.ID{}
	}
	return e.call("_skills.evaluation.record", evaluationRecordInput{
		EvaluationID: job.ID, JobID: job.OperationID, ExpectedVersion: expectedVersion,
		VerifierID: verifierID, VerifierVersion: verifierVersion,
		EvidenceIDs: evidence, Passed: passed,
	})
}

// TestImportedSkillEvaluationJobFinishesAndActivationConsumesRealEvidence
// proves the required behavior: an imported skill's evaluation job finishes
// (RunJob independently verifies a real published fixture, never a
// self-declared pass) and activation consumes that real evidence -- the
// version only activates because a genuine passing evaluation exists for
// its exact immutable identity.
func TestImportedSkillEvaluationJobFinishesAndActivationConsumesRealEvidence(t *testing.T) {
	e := newEnv(t)
	ref, baseIn := e.importEvaluation()

	fixture := []byte(`{"greeting":"hello"}`)
	digest := contract.Hash(fixture)
	e.blobs.seed(digest, fixture)

	in := baseIn
	in.Acceptance.ExpectedObservations = []wireExpectedObservation{digestCheck("fixture-digest", digest, "pass")}
	job := e.admitEvaluation(in)
	if job.State != "pending" {
		t.Fatalf("job state = %q, want pending", job.State)
	}

	outcome := e.runEvaluationJob(t, job)
	if outcome.State != "succeeded" {
		t.Fatalf("RunJob outcome state = %q, want succeeded (real fixture verification should finish)", outcome.State)
	}
	var result struct {
		EvaluationID contract.ID       `json:"evaluation_id"`
		Passed       bool              `json:"passed"`
		Evidence     []wireArtifactRef `json:"evidence"`
	}
	if err := json.Unmarshal(outcome.Result, &result); err != nil {
		t.Fatalf("decode RunJob result: %v", err)
	}
	if !result.Passed || result.EvaluationID != job.ID || len(result.Evidence) == 0 {
		t.Fatalf("RunJob result = %+v, want a genuine pass with evidence", result)
	}
	if len(outcome.EvidenceIDs) == 0 {
		t.Fatalf("RunJob produced no evidence ids")
	}

	recorded, err := e.recordEvaluation(job, 1, in.Acceptance.VerifierID, in.Acceptance.VerifierVersion, outcome, true)
	if err != nil || recorded.Error != nil {
		t.Fatalf("_skills.evaluation.record: %v %v", err, recorded.Error)
	}
	var recordedOut jobOutput
	e.decode(recorded.Data, &recordedOut)
	if recordedOut.Resource.State != "succeeded" {
		t.Fatalf("recorded evaluation state = %q, want succeeded", recordedOut.Resource.State)
	}

	// Activation must now succeed: it consumes this exact, real evidence.
	row := mustVersionRow(e, ref.ID, int64(ref.Version))
	e.mustOK("_skills.activate", candidateInputOf(changeOf("create", e.defOfRow(row), 0)))
	if state, ok := e.rowState(ref.ID, int64(ref.Version)); !ok || state != "active" {
		t.Fatalf("skill state = %q (found %v), want active", state, ok)
	}

	status := e.mustOK("skill.evaluation.status", skillGetInput{Scope: e.scope, ID: job.ID})
	var statusOut jobOutput
	e.decode(status.Data, &statusOut)
	if statusOut.Resource.State != "succeeded" || statusOut.Resource.Result == nil {
		t.Fatalf("evaluation status = %+v, want succeeded with recorded evidence", statusOut.Resource)
	}
}

// TestFailingFixtureBlocksActivation proves the required behavior: a
// failing fixture (a digest mismatch RunJob genuinely observes) blocks
// activation of that exact skill version.
func TestFailingFixtureBlocksActivation(t *testing.T) {
	e := newEnv(t)
	ref, baseIn := e.importEvaluation()

	fixture := []byte("actual fixture bytes")
	digest := contract.Hash(fixture)
	e.blobs.seed(digest, fixture)
	wrongDigest := contract.Digest(strings.Repeat("f", 64))

	in := baseIn
	in.Acceptance.ExpectedObservations = []wireExpectedObservation{digestCheck("fixture-digest", digest, "pass")}
	// Pin the expectation to a digest that will never match the seeded
	// bytes: RunJob must observe a genuine mismatch, not merely a missing
	// artifact.
	in.Acceptance.ExpectedObservations[0].ExpectedDigest = string(wrongDigest)
	e.blobs.seed(wrongDigest, []byte("different bytes entirely"))

	job := e.admitEvaluation(in)
	outcome := e.runEvaluationJob(t, job)
	if outcome.State != "succeeded" {
		t.Fatalf("RunJob outcome state = %q, want succeeded (the job itself ran to a definite verdict)", outcome.State)
	}
	var result struct {
		Passed bool `json:"passed"`
	}
	if err := json.Unmarshal(outcome.Result, &result); err != nil {
		t.Fatalf("decode RunJob result: %v", err)
	}
	if result.Passed {
		t.Fatalf("RunJob reported passed=true for a genuinely mismatched fixture")
	}

	recorded, err := e.recordEvaluation(job, 1, in.Acceptance.VerifierID, in.Acceptance.VerifierVersion, outcome, false)
	if err != nil || recorded.Error != nil {
		t.Fatalf("_skills.evaluation.record: %v %v", err, recorded.Error)
	}
	var recordedOut jobOutput
	e.decode(recorded.Data, &recordedOut)
	if recordedOut.Resource.State != "failed" {
		t.Fatalf("recorded evaluation state = %q, want failed", recordedOut.Resource.State)
	}

	// Activation of this exact version must now be refused.
	row := mustVersionRow(e, ref.ID, int64(ref.Version))
	e.expectFault("_skills.activate", candidateInputOf(changeOf("create", e.defOfRow(row), 0)), contract.CodeConflict)
	if state, ok := e.rowState(ref.ID, int64(ref.Version)); !ok || state != "draft" {
		t.Fatalf("skill state = %q (found %v), want still draft after refused activation", state, ok)
	}
}

// TestChangedSkillVersionCannotReuseOldQualification proves the required
// behavior: a changed skill version (a fresh immutable version produced by
// re-importing changed content) never inherits or is blocked by a prior
// version's evaluation -- qualification is scoped to the exact content-
// hashed identity, never reused across versions.
func TestChangedSkillVersionCannotReuseOldQualification(t *testing.T) {
	e := newEnv(t)
	ref1, baseIn := e.importEvaluation()

	// Version 1 fails its evaluation.
	wrongDigest := contract.Digest(strings.Repeat("a", 64))
	e.blobs.seed(wrongDigest, []byte("v1 bytes"))
	in1 := baseIn
	in1.Acceptance.ExpectedObservations = []wireExpectedObservation{digestCheck("c", wrongDigest, "pass")}
	in1.Acceptance.ExpectedObservations[0].ExpectedDigest = string(contract.Digest(strings.Repeat("b", 64)))
	job1 := e.admitEvaluation(in1)
	outcome1 := e.runEvaluationJob(t, job1)
	if _, err := e.recordEvaluation(job1, 1, in1.Acceptance.VerifierID, in1.Acceptance.VerifierVersion, outcome1, false); err != nil {
		t.Fatalf("record v1 evaluation: %v", err)
	}
	row1 := mustVersionRow(e, ref1.ID, int64(ref1.Version))
	e.expectFault("_skills.activate", candidateInputOf(changeOf("create", e.defOfRow(row1), 0)), contract.CodeConflict)

	// Re-import changed content under the same skill name: a fresh
	// immutable version, continuing the same identity.
	changedArchive := buildZip([]zipEntry{
		{Name: "SKILL.md", Data: []byte(strings.Replace(validSkillMD, "Greeter", "Greeter Mk II", 1))},
		{Name: "scripts/run.sh", Data: []byte("#!/bin/sh\necho hi v2\n")},
	})
	payload, err := e.runImport(e.importFixture(changedArchive))
	if err != nil || payload.Error != nil {
		t.Fatalf("import v2: %v %v", err, payload.Error)
	}
	var imported importOutput
	e.decode(payload.Data, &imported)
	ref2 := wireRef{ID: imported.Resource.ID, Version: imported.Resource.Version}
	if ref2.ID != ref1.ID || ref2.Version != ref1.Version+1 {
		t.Fatalf("v2 ref = %+v, want same identity %s at version %d", ref2, ref1.ID, ref1.Version+1)
	}

	// Version 2 has no evaluation of its own: v1's failure must not bleed
	// forward, and it activates cleanly (evaluation is never required, only
	// a genuinely failing one blocks).
	row2 := mustVersionRow(e, ref2.ID, int64(ref2.Version))
	if len(e.defOfRow(row2).EvaluationRefs) != 0 {
		t.Fatalf("v2 evaluation_refs = %+v, want none: qualification must never carry across versions", e.defOfRow(row2).EvaluationRefs)
	}
	e.mustOK("_skills.activate", candidateInputOf(changeOf("create", e.defOfRow(row2), 0)))
	if state, ok := e.rowState(ref2.ID, int64(ref2.Version)); !ok || state != "active" {
		t.Fatalf("v2 state = %q (found %v), want active: a changed version must not reuse v1's failed qualification", state, ok)
	}

	// Now evaluate v2 for itself and fail it: this version's own failure
	// must block only this version, never reach back to v1's (already
	// refused) activation attempt or any other identity.
	in2 := e.evaluateInputFor(ref2)
	in2.Acceptance.ExpectedObservations = []wireExpectedObservation{digestCheck("c2", wrongDigest, "pass")}
	in2.Acceptance.ExpectedObservations[0].ExpectedDigest = string(contract.Digest(strings.Repeat("e", 64)))
	job2 := e.admitEvaluation(in2)
	outcome2 := e.runEvaluationJob(t, job2)
	if _, err := e.recordEvaluation(job2, 1, in2.Acceptance.VerifierID, in2.Acceptance.VerifierVersion, outcome2, false); err != nil {
		t.Fatalf("record v2 evaluation: %v", err)
	}
	// v2 is already active; a later failing evaluation does not retroactively
	// deactivate it -- activation is a one-time gate at the transition, not
	// a standing invariant this card is asked to enforce continuously.
	if state, _ := e.rowState(ref2.ID, int64(ref2.Version)); state != "active" {
		t.Fatalf("v2 state changed to %q after a later evaluation; activation gate must run once at the transition", state)
	}
}

// TestRestartDuringEvaluationNeverRerunsUncertaintyOrInventsAPass proves the
// required behavior across three restart scenarios: (1) a job that never
// finishes leaves the evaluation genuinely pending, never a fabricated
// terminal state; (2) an uncertain external action (a fixture RunJob cannot
// observe at all) yields outcome_unknown, never a guessed pass or fail, and
// is never silently turned into one later; (3) once a terminal disposition
// is durably recorded, a restart that redrives the identical completion is
// idempotent, and a restart that tries to redrive a *different* verdict is
// refused outright -- never overwriting a decided outcome.
func TestRestartDuringEvaluationNeverRerunsUncertaintyOrInventsAPass(t *testing.T) {
	t.Run("job never finishes leaves the evaluation pending, not a fabricated pass", func(t *testing.T) {
		e := newEnv(t)
		_, in := e.importEvaluation()
		job := e.admitEvaluation(in)
		// Simulate a restart before RunJob ever ran: nothing records this
		// evaluation. Status must stay pending with no invented evidence.
		status := e.mustOK("skill.evaluation.status", skillGetInput{Scope: e.scope, ID: job.ID})
		var out jobOutput
		e.decode(status.Data, &out)
		if out.Resource.State != "pending" || out.Resource.Result != nil {
			t.Fatalf("evaluation status = %+v, want pending with no result before any recording", out.Resource)
		}
	})

	t.Run("an unobservable external action yields outcome_unknown, never a guessed verdict", func(t *testing.T) {
		e := newEnv(t)
		_, baseIn := e.importEvaluation()
		in := baseIn
		// A digest that was never published: the fixture is genuinely
		// unavailable, distinct from a definite mismatch.
		missing := contract.Digest(strings.Repeat("9", 64))
		in.Acceptance.ExpectedObservations = []wireExpectedObservation{digestCheck("missing", missing, "pass")}
		job := e.admitEvaluation(in)
		outcome := e.runEvaluationJob(t, job)
		if outcome.State != "outcome_unknown" {
			t.Fatalf("RunJob outcome state = %q, want outcome_unknown for an unobservable check", outcome.State)
		}
		if string(outcome.Result) != "{}" {
			t.Fatalf("RunJob result = %s, want the empty object: nothing about pass/fail may be asserted", outcome.Result)
		}
		if len(outcome.EvidenceIDs) != 0 {
			t.Fatalf("RunJob minted evidence %v for an unresolved outcome", outcome.EvidenceIDs)
		}
		// Nothing calls _skills.evaluation.record on an outcome_unknown
		// job (it carries no passed verdict to record): the row stays
		// pending, honestly reflecting that no qualification was granted.
		status := e.mustOK("skill.evaluation.status", skillGetInput{Scope: e.scope, ID: job.ID})
		var out jobOutput
		e.decode(status.Data, &out)
		if out.Resource.State != "pending" {
			t.Fatalf("evaluation status = %q after an unresolved run, want still pending (never an invented pass)", out.Resource.State)
		}
	})

	t.Run("a repeated restart redriving the identical completion is idempotent", func(t *testing.T) {
		e := newEnv(t)
		_, baseIn := e.importEvaluation()
		fixture := []byte("idempotent fixture")
		digest := contract.Hash(fixture)
		e.blobs.seed(digest, fixture)
		in := baseIn
		in.Acceptance.ExpectedObservations = []wireExpectedObservation{digestCheck("c", digest, "pass")}
		job := e.admitEvaluation(in)
		outcome := e.runEvaluationJob(t, job)

		first, err := e.recordEvaluation(job, 1, in.Acceptance.VerifierID, in.Acceptance.VerifierVersion, outcome, true)
		if err != nil || first.Error != nil {
			t.Fatalf("first record: %v %v", err, first.Error)
		}
		var firstOut jobOutput
		e.decode(first.Data, &firstOut)

		// A restart redelivers the exact same completion under the exact
		// same (now stale) expected_version it originally observed.
		second, err := e.recordEvaluation(job, 1, in.Acceptance.VerifierID, in.Acceptance.VerifierVersion, outcome, true)
		if err != nil || second.Error != nil {
			t.Fatalf("replayed record must be idempotent, got: %v %v", err, second.Error)
		}
		var secondOut jobOutput
		e.decode(second.Data, &secondOut)
		if secondOut.Resource.State != "succeeded" || secondOut.Resource.State != firstOut.Resource.State {
			t.Fatalf("replayed record state = %q, want the unchanged succeeded disposition", secondOut.Resource.State)
		}

		// A restart that tries to redrive a *different* verdict for the
		// same evaluation must never be allowed to overwrite the decided
		// outcome -- this is exactly "never invent a different pass".
		payload, callErr := e.recordEvaluation(job, 1, in.Acceptance.VerifierID, in.Acceptance.VerifierVersion, outcome, false)
		var f *contract.Fault
		if callErr != nil {
			errors.As(callErr, &f)
		}
		if f == nil {
			f = payload.Error
		}
		if f == nil {
			t.Fatalf("recording a contradicting verdict against a terminal evaluation must be refused, got completed payload")
		}
		if f.Code != contract.CodeStaleVersion {
			t.Fatalf("contradicting record fault = %s, want %s", f.Code, contract.CodeStaleVersion)
		}
	})

	t.Run("an evaluator changed since admission invalidates rather than records", func(t *testing.T) {
		e := newEnv(t)
		_, baseIn := e.importEvaluation()
		fixture := []byte("evaluator changed fixture")
		digest := contract.Hash(fixture)
		e.blobs.seed(digest, fixture)
		in := baseIn
		in.Acceptance.ExpectedObservations = []wireExpectedObservation{digestCheck("c", digest, "pass")}
		job := e.admitEvaluation(in)
		outcome := e.runEvaluationJob(t, job)

		// A different verifier identity than the one this evaluation
		// admitted -- simulating an evaluator upgrade racing the job.
		payload, err := e.recordEvaluation(job, 1, "a-different-verifier", "9.9.9", outcome, true)
		if err != nil {
			t.Fatalf("record with changed evaluator: %v", err)
		}
		if payload.Error != nil {
			t.Fatalf("changed-evaluator record returned a fault instead of invalidating: %v", payload.Error)
		}
		var out jobOutput
		e.decode(payload.Data, &out)
		if out.Resource.State != "failed" {
			t.Fatalf("invalidated evaluation state = %q, want failed (never succeeded from a mismatched evaluator)", out.Resource.State)
		}
	})
}
