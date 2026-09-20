package tasks

import (
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// _tasks.evidence.record behavior: the sole trusted path that binds a
// sealed named-output check to an actual published artifact, replacing the
// old preknown-output-digest dependence. A named presence check can now be
// admitted without ever pinning a digest, because the real content is
// frequently unknowable until the run produces it -- but establishing
// success still requires the trusted binding this port alone writes; a raw
// evidence_ids submission (what a worker's self-report becomes once it
// reaches _tasks.transition) can never satisfy it.

// namedPresenceDef builds a task definition whose sole acceptance check is
// an artifact_presence observation naming outputName without pinning an
// expected_digest: the content is unknown at admission.
func (e *testEnv) namedPresenceDef(outputName string) taskDefInput {
	def := e.taskDef()
	def.RequiredOutputs = []string{outputName}
	def.Acceptance = acceptanceFixture(wireExpectedObservation{
		CheckID: "output-present", Kind: obsArtifactPresence,
		Expected: observationExpectedPass, ArtifactName: outputName,
	})
	return def
}

// TestEvidenceRecordEstablishesUnknownAtAdmissionOutput: a report whose
// bytes were unknown when the task was admitted -- no expected_digest was
// ever pinned -- satisfies its sealed presence check once the trusted
// _tasks.evidence.record port binds the slot to the artifact the run
// actually produced.
func TestEvidenceRecordEstablishesUnknownAtAdmissionOutput(t *testing.T) {
	env := newEnv(t)
	id := env.createTask(env.namedPresenceDef("report.bin"))

	v := env.runToReady(id)
	v = env.runToRunning(id, v)
	v = env.runToVerifying(id, v, nil)

	// The report is published only now, after the task was admitted and
	// started -- its bytes could not have been pinned at admission.
	report := env.artifactFixture("generated-report-bytes")
	verifier := env.verifierArtifactRefFixture("independent-verifier")
	env.recordEvidence(id, v, verifier, []wireOutputBinding{{Name: "report.bin", Artifact: report}}, verdictPassed)

	wire := env.transition(id, v, stateSucceeded, nil)
	if wire.State != stateSucceeded {
		t.Fatalf("state %s, want succeeded", wire.State)
	}
	row := env.readRow(id)
	if row.EstablishedBy != establishedVerification {
		t.Fatalf("established_by %q, want verification", row.EstablishedBy)
	}
}

// TestEvidenceRecordWorkerAssertionCannotSatisfy: the identical sealed
// presence check, satisfied only through _tasks.evidence.record above,
// refuses a bare worker assertion -- the same generated artifact reference
// submitted directly as raw evidence_ids on _tasks.transition, skipping the
// trusted binding port entirely.
func TestEvidenceRecordWorkerAssertionCannotSatisfy(t *testing.T) {
	env := newEnv(t)
	id := env.createTask(env.namedPresenceDef("report.bin"))

	v := env.runToReady(id)
	v = env.runToRunning(id, v)
	v = env.runToVerifying(id, v, nil)

	report := env.artifactFixture("generated-report-bytes")
	f := env.expectFault("_tasks.transition", map[string]any{
		"task_id": id, "expected_version": v, "state": stateSucceeded,
		"evidence_ids": []contract.ID{report.ID},
	}, contract.CodeVerificationFailed)
	if !strings.Contains(f.Message, "no trusted output binding") {
		t.Fatalf("refusal does not name the missing trusted binding: %s", f.Message)
	}
	row := env.readRow(id)
	if row.State == stateSucceeded {
		t.Fatalf("worker assertion established success")
	}
}

// TestEvidenceRecordRejectsStaleVersion: evidence recorded against a
// version the task has moved past is refused, never silently accepted
// against the current version.
func TestEvidenceRecordRejectsStaleVersion(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	v := env.runToReady(id)
	v = env.runToRunning(id, v)

	row := env.readRow(id)
	verifier := env.verifierArtifactRefFixture("verifier-result")
	_ = env.expectFault("_tasks.evidence.record", map[string]any{
		"task_id": id, "attempt_id": env.ids.New(), "expected_version": v - 1,
		"acceptance_digest": row.AcceptanceDigest, "verification_artifact": verifier,
		"output_bindings": []wireOutputBinding{}, "verdict": verdictPassed,
	}, contract.CodeStaleVersion)
}

// TestEvidenceRecordRejectsAcceptanceDigestMismatch: a caller-asserted
// acceptance digest that does not match the task's currently sealed
// contract is refused, defending against evidence produced against the
// wrong pinned contract.
func TestEvidenceRecordRejectsAcceptanceDigestMismatch(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	v := env.runToReady(id)
	v = env.runToRunning(id, v)

	verifier := env.verifierArtifactRefFixture("verifier-result")
	_ = env.expectFault("_tasks.evidence.record", map[string]any{
		"task_id": id, "attempt_id": env.ids.New(), "expected_version": v,
		"acceptance_digest": altDigest, "verification_artifact": verifier,
		"output_bindings": []wireOutputBinding{}, "verdict": verdictPassed,
	}, contract.CodeVerificationFailed)
}

// TestEvidenceRecordRejectsBeforeLiveAttempt: evidence cannot be recorded
// against a draft or ready task that has never started.
func TestEvidenceRecordRejectsBeforeLiveAttempt(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	row := env.readRow(id)
	verifier := env.verifierArtifactRefFixture("verifier-result")

	_ = env.expectFault("_tasks.evidence.record", map[string]any{
		"task_id": id, "attempt_id": env.ids.New(), "expected_version": int64(1),
		"acceptance_digest": row.AcceptanceDigest, "verification_artifact": verifier,
		"output_bindings": []wireOutputBinding{}, "verdict": verdictPassed,
	}, contract.CodeConflict)
}

// TestEvidenceRecordRejectsTerminalTask: evidence cannot be recorded
// against a task that already reached a terminal state.
func TestEvidenceRecordRejectsTerminalTask(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	v := env.runToReady(id)
	v = env.runToRunning(id, v)
	v = env.runToVerifying(id, v, nil)
	env.transition(id, v, stateFailed, nil)

	row := env.readRow(id)
	verifier := env.verifierArtifactRefFixture("verifier-result")
	_ = env.expectFault("_tasks.evidence.record", map[string]any{
		"task_id": id, "attempt_id": env.ids.New(), "expected_version": int64(row.Version),
		"acceptance_digest": row.AcceptanceDigest, "verification_artifact": verifier,
		"output_bindings": []wireOutputBinding{}, "verdict": verdictPassed,
	}, contract.CodeConflict)
}

// TestEvidenceRecordRejectsUndeclaredOutputName: a binding naming an output
// slot the accepted contract never declared cannot smuggle unrelated
// evidence into the fence.
func TestEvidenceRecordRejectsUndeclaredOutputName(t *testing.T) {
	env := newEnv(t)
	id := env.createTask(env.namedPresenceDef("report.bin"))
	v := env.runToReady(id)
	v = env.runToRunning(id, v)
	v = env.runToVerifying(id, v, nil)

	row := env.readRow(id)
	verifier := env.verifierArtifactRefFixture("verifier-result")
	other := env.artifactFixture("unrelated-output")
	_ = env.expectFault("_tasks.evidence.record", map[string]any{
		"task_id": id, "attempt_id": env.ids.New(), "expected_version": int64(v),
		"acceptance_digest": row.AcceptanceDigest, "verification_artifact": verifier,
		"output_bindings": []wireOutputBinding{{Name: "not-declared.bin", Artifact: other}},
		"verdict":          verdictPassed,
	}, contract.CodeInvalidInput)
}

// TestEvidenceRecordRejectsDuplicateOutputName: the same output name bound
// twice in one call is refused rather than silently taking the last value.
func TestEvidenceRecordRejectsDuplicateOutputName(t *testing.T) {
	env := newEnv(t)
	id := env.createTask(env.namedPresenceDef("report.bin"))
	v := env.runToReady(id)
	v = env.runToRunning(id, v)
	v = env.runToVerifying(id, v, nil)

	row := env.readRow(id)
	verifier := env.verifierArtifactRefFixture("verifier-result")
	a := env.artifactFixture("report-a")
	b := env.artifactFixture("report-b")
	_ = env.expectFault("_tasks.evidence.record", map[string]any{
		"task_id": id, "attempt_id": env.ids.New(), "expected_version": int64(v),
		"acceptance_digest": row.AcceptanceDigest, "verification_artifact": verifier,
		"output_bindings": []wireOutputBinding{
			{Name: "report.bin", Artifact: a}, {Name: "report.bin", Artifact: b},
		},
		"verdict": verdictPassed,
	}, contract.CodeInvalidInput)
}

// TestEvidenceRecordDigestMismatchFailsEstablishment: a presence check that
// does pin an expected_digest (known content) is not established by a
// binding whose artifact carries a different digest, even though the
// binding itself is trusted.
func TestEvidenceRecordDigestMismatchFailsEstablishment(t *testing.T) {
	env := newEnv(t)
	// The default fixture task pins an expected_digest on "report.bin".
	id := env.createDefaultTask()
	v := env.runToReady(id)
	v = env.runToRunning(id, v)
	v = env.runToVerifying(id, v, nil)

	verifier := env.verifierArtifactRefFixture("verifier-result")
	wrong := env.artifactFixture("not-the-pinned-content")
	env.recordEvidence(id, v, verifier, []wireOutputBinding{{Name: "report.bin", Artifact: wrong}}, verdictPassed)

	f := env.expectFault("_tasks.transition", map[string]any{
		"task_id": id, "expected_version": v, "state": stateSucceeded,
		"evidence_ids": []contract.ID{},
	}, contract.CodeVerificationFailed)
	if !strings.Contains(f.Message, "does not match the pinned digest") {
		t.Fatalf("refusal does not name the digest mismatch: %s", f.Message)
	}
}

// TestEvidenceRecordRejectsUnresolvableBinding: a binding naming an
// artifact that does not resolve in the artifacts owner is refused, never
// silently recorded.
func TestEvidenceRecordRejectsUnresolvableBinding(t *testing.T) {
	env := newEnv(t)
	id := env.createTask(env.namedPresenceDef("report.bin"))
	v := env.runToReady(id)
	v = env.runToRunning(id, v)
	v = env.runToVerifying(id, v, nil)

	row := env.readRow(id)
	verifier := env.verifierArtifactRefFixture("verifier-result")
	unresolvable := wireArtifactRef{ID: env.ids.New(), Digest: contract.Digest(altDigest)}
	_ = env.expectFault("_tasks.evidence.record", map[string]any{
		"task_id": id, "attempt_id": env.ids.New(), "expected_version": int64(v),
		"acceptance_digest": row.AcceptanceDigest, "verification_artifact": verifier,
		"output_bindings": []wireOutputBinding{{Name: "report.bin", Artifact: unresolvable}},
		"verdict":          verdictPassed,
	}, contract.CodeArtifactFault)
}
