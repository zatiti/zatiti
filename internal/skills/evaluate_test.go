package skills

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// skill.evaluate seals an evaluation record — verifier identity, acceptance,
// execution posture, limits — and creates the execution job the controller
// drives. The outcome is never self-declared here: the state starts pending
// and stays pending until recorded downstream.

// evaluateInputFor builds a fully valid skill.evaluate input for one skill
// version under the environment's scope. The verifier profile is the
// artifact_contract alternative of the shared oneOf.
func (e *testEnv) evaluateInputFor(ref wireRef) evaluateInput {
	return evaluateInput{
		Scope: e.scope,
		Skill: ref,
		Acceptance: wireAcceptance{
			VerifierID:           "verifier-main",
			VerifierVersion:      "1.2.0",
			SealedInputs:         []wireArtifactRef{},
			ExpectedObservations: []wireExpectedObservation{},
			Mode:                 "independent",
			RequiredChildIDs:     []contract.ID{},
			Profile: json.RawMessage(`{
				"schema": "zatiti.verifier-profile/v1",
				"kind": "artifact_contract",
				"id": "verifier-main",
				"version": "1.2.0",
				"code_digest": "` + strings.Repeat("d", 64) + `",
				"supported_checks": ["presence", "digest"],
				"max_bytes": 65536,
				"timeout_seconds": 60,
				"capability_evidence": {
					"artifact": {"id": "` + string(e.ids.New()) + `", "digest": "` + strings.Repeat("e", 64) + `"},
					"adapter_version": "1.0.0",
					"source_revision": "rev-1",
					"protocol_revision": "proto-1",
					"profile_digest": "` + strings.Repeat("f", 64) + `",
					"qualified_at": "2026-09-01T00:00:00Z",
					"capabilities": ["presence"],
					"limitations": []
				}
			}`),
		},
		Profile: wireExecutionProfile{
			ID:                  e.ids.New(),
			Version:             1,
			Executor:            "hosted",
			Model:               "test-model",
			ConnectionID:        e.ids.New(),
			ProviderDestination: "local-test",
			Capabilities:        []string{},
			CostBound:           wireMoney{Currency: "USD", MicroUnits: 1000},
			Classification:      "internal",
			ContextCapture:      "complete",
		},
		Limits: wireLimits{
			Currency:        "USD",
			SpendMicroUnits: 1000,
			Concurrency:     1,
			ModelSteps:      10,
			ChildCount:      0,
			DelegationDepth: 1,
			AttemptSeconds:  60,
			RootDeadline:    "2026-09-10T13:00:00Z",
		},
	}
}

// importEvaluation imports the canonical archive and returns the skill
// reference together with its evaluation input.
func (e *testEnv) importEvaluation() (wireRef, evaluateInput) {
	e.t.Helper()
	payload, err := e.runImport(e.importFixture(validArchive()))
	if err != nil || payload.Error != nil {
		e.t.Fatalf("import failed: %v %v", err, payload.Error)
	}
	var out importOutput
	e.decode(payload.Data, &out)
	ref := wireRef{ID: out.Resource.ID, Version: out.Resource.Version}
	return ref, e.evaluateInputFor(ref)
}

func TestEvaluateHappyPath(t *testing.T) {
	e := newEnv(t)
	ref, in := e.importEvaluation()
	payload := e.mustOK("skill.evaluate", in)
	var out jobOutput
	e.decode(payload.Data, &out)
	job := out.Resource

	if job.Kind != "skill.evaluation" || job.State != "pending" {
		t.Fatalf("job = %s/%s, want skill.evaluation/pending", job.Kind, job.State)
	}
	if job.OperationID == "" || job.Owner != "skills" || job.Operation != "skill.evaluate" {
		t.Fatalf("job linkage missing: %+v", job)
	}

	// The execution job input is inert: identity and bounds only.
	calls := e.ports.callsOf("_execution.job.create")
	if len(calls) != 1 {
		t.Fatalf("job.create calls = %d, want 1", len(calls))
	}
	var created struct {
		Owner     string `json:"owner"`
		Operation string `json:"operation"`
		SourceID  string `json:"source_id"`
		Input     struct {
			EvaluationID string `json:"evaluation_id"`
			Skill        struct {
				ID contract.ID `json:"id"`
			} `json:"skill"`
		} `json:"input"`
	}
	if err := json.Unmarshal(calls[0].Input, &created); err != nil {
		t.Fatalf("decode job input: %v", err)
	}
	if created.Owner != "skills" || created.Operation != "skill.evaluate" {
		t.Fatalf("job owner/operation = %s/%s", created.Owner, created.Operation)
	}
	if created.SourceID != string(job.ID) || created.Input.EvaluationID != string(job.ID) {
		t.Fatalf("job source/evaluation linkage = %s/%s, want the evaluation id %s",
			created.SourceID, created.Input.EvaluationID, job.ID)
	}
	if created.Input.Skill.ID != ref.ID {
		t.Fatalf("job skill = %s, want %s", created.Input.Skill.ID, ref.ID)
	}

	// The record starts with no observations and no evidence: the outcome
	// belongs to the controller, never to this operation.
	var row *evaluationRow
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		row, err = fetchEvaluation(e.ctx, unit, e.install, job.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if row == nil || row.State != "pending" || row.ObservationsJSON != "" || row.EvidenceJSON != "" {
		t.Fatalf("evaluation row = %+v, want pending with no recorded outcome", row)
	}

	// status serves the stored disposition.
	status := e.mustOK("skill.evaluation.status", skillGetInput{Scope: e.scope, ID: job.ID})
	var statusOut jobOutput
	e.decode(status.Data, &statusOut)
	if statusOut.Resource.ID != job.ID || statusOut.Resource.State != "pending" ||
		statusOut.Resource.OperationID != job.OperationID {
		t.Fatalf("status resource = %+v", statusOut.Resource)
	}

	// The evaluation.created event rides the same transaction.
	events, err := e.db.Events(e.ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var evalEvents []contract.Event
	for _, ev := range events {
		if ev.Kind == "skills.evaluation.created" {
			evalEvents = append(evalEvents, ev)
		}
	}
	if len(evalEvents) != 1 || evalEvents[0].ResourceID != job.ID {
		t.Fatalf("evaluation events = %+v, want one for %s", evalEvents, job.ID)
	}
}

func TestEvaluateAcceptanceFences(t *testing.T) {
	t.Run("missing verifier identity", func(t *testing.T) {
		e := newEnv(t)
		_, in := e.importEvaluation()
		in.Acceptance.VerifierID = ""
		e.expectFault("skill.evaluate", in, contract.CodeInvalidInput)
	})
	t.Run("unknown mode", func(t *testing.T) {
		e := newEnv(t)
		_, in := e.importEvaluation()
		in.Acceptance.Mode = "self"
		e.expectFault("skill.evaluate", in, contract.CodeInvalidInput)
	})
	t.Run("unknown sealed input", func(t *testing.T) {
		e := newEnv(t)
		_, in := e.importEvaluation()
		in.Acceptance.SealedInputs = []wireArtifactRef{{
			ID: e.ids.New(), Digest: contract.Digest(strings.Repeat("a", 64)),
		}}
		e.expectFault("skill.evaluate", in, contract.CodeNotFound)
	})
	t.Run("sealed input digest mismatch", func(t *testing.T) {
		e := newEnv(t)
		_, in := e.importEvaluation()
		id := e.ids.New()
		e.ports.addArtifact(fakeArtifact{
			ID: id, Version: 1, Digest: contract.Digest(strings.Repeat("b", 64)),
			Size: 10, State: "available",
		})
		in.Acceptance.SealedInputs = []wireArtifactRef{{
			ID: id, Digest: contract.Digest(strings.Repeat("c", 64)),
		}}
		e.expectFault("skill.evaluate", in, contract.CodeConflict)
	})
	t.Run("sealed input in fault state", func(t *testing.T) {
		e := newEnv(t)
		_, in := e.importEvaluation()
		id := e.ids.New()
		e.ports.addArtifact(fakeArtifact{
			ID: id, Version: 1, Digest: contract.Digest(strings.Repeat("b", 64)),
			Size: 10, State: "fault",
		})
		in.Acceptance.SealedInputs = []wireArtifactRef{{
			ID: id, Digest: contract.Digest(strings.Repeat("b", 64)),
		}}
		e.expectFault("skill.evaluate", in, contract.CodeConflict)
	})
}

func TestEvaluateLimits(t *testing.T) {
	cases := []struct {
		name string
		mut  func(in *evaluateInput)
	}{
		{"negative spend", func(in *evaluateInput) { in.Limits.SpendMicroUnits = -1 }},
		{"negative concurrency", func(in *evaluateInput) { in.Limits.Concurrency = -1 }},
		{"past root deadline", func(in *evaluateInput) { in.Limits.RootDeadline = "2026-09-10T11:00:00Z" }},
		{"unparsable root deadline", func(in *evaluateInput) { in.Limits.RootDeadline = "not-a-time" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			_, in := e.importEvaluation()
			tc.mut(&in)
			e.expectFault("skill.evaluate", in, contract.CodeInvalidInput)
		})
	}
	t.Run("future root deadline accepts", func(t *testing.T) {
		e := newEnv(t)
		_, in := e.importEvaluation()
		in.Limits.RootDeadline = e.clock.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		e.mustOK("skill.evaluate", in)
	})
}

func TestEvaluateSkillFences(t *testing.T) {
	t.Run("unknown skill", func(t *testing.T) {
		e := newEnv(t)
		in := e.evaluateInputFor(wireRef{ID: e.ids.New(), Version: 1})
		e.expectFault("skill.evaluate", in, contract.CodeNotFound)
	})
	t.Run("archived skill", func(t *testing.T) {
		e := newEnv(t)
		ref, _ := e.importEvaluation()
		// Activate the draft, then archive it: an archived version is
		// retired and no longer evaluatable.
		v1 := mustVersionRow(e, ref.ID, int64(ref.Version))
		e.mustOK("_skills.activate", candidateInputOf(changeOf("create", e.defOfRow(v1), 0)))
		v1 = mustVersionRow(e, ref.ID, int64(ref.Version))
		e.mustOK("_skills.activate", candidateInputOf(changeOf("archive", e.defOfRow(v1), 1)))
		in2 := e.evaluateInputFor(wireRef{ID: ref.ID, Version: ref.Version})
		e.expectFault("skill.evaluate", in2, contract.CodeConflict)
	})
}

// mustVersionRow loads one exact skill row for fixture construction.
func mustVersionRow(e *testEnv, id contract.ID, version int64) *skillRow {
	e.t.Helper()
	var row *skillRow
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		row, err = fetchSkillVersion(e.ctx, unit, e.install, id, version)
		return err
	}); err != nil {
		e.t.Fatalf("mustVersionRow: %v", err)
	}
	if row == nil {
		e.t.Fatalf("mustVersionRow: %s v%d missing", id, version)
	}
	return row
}

func TestEvaluationStatusUnknown(t *testing.T) {
	e := newEnv(t)
	e.expectFault("skill.evaluation.status", skillGetInput{Scope: e.scope, ID: e.ids.New()}, contract.CodeNotFound)
}
