package tasks

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Admission fence behavior: finite envelope, worker and artifact
// validation, budget precheck and reservation, acceptance sealing, and the
// atomic rollback of everything the fence admits.

func TestAdmissionBudgetFaults(t *testing.T) {
	t.Run("inspect peer fault propagates", func(t *testing.T) {
		env := newEnv(t)
		env.setPeerFault("_accounting.inspect", &contract.Fault{
			Code: contract.CodeBudgetUnavailable, Message: "envelope locked",
		})
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": env.taskDef()},
			contract.CodeBudgetUnavailable)
	})

	t.Run("spend over the intersected limit", func(t *testing.T) {
		env := newEnv(t)
		env.ports.inspectLimits.SpendMicroUnits = 100
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": env.taskDef()},
			contract.CodeBudgetUnavailable)
	})

	t.Run("accounting currency mismatch", func(t *testing.T) {
		env := newEnv(t)
		env.ports.inspectUsage.Currency = "EUR"
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": env.taskDef()},
			contract.CodeBudgetUnavailable)
	})

	t.Run("reserve peer fault aborts admission", func(t *testing.T) {
		env := newEnv(t)
		env.setPeerFault("_accounting.reserve", &contract.Fault{
			Code: contract.CodeBudgetUnavailable, Message: "reservation refused",
		})
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": env.taskDef()},
			contract.CodeBudgetUnavailable)
		items, _ := env.listTasksPage(map[string]any{"scope": env.scope})
		if len(items) != 0 {
			t.Fatalf("failed admission left %d tasks behind", len(items))
		}
	})
}

func TestAdmissionWorkerValidation(t *testing.T) {
	t.Run("unknown worker", func(t *testing.T) {
		env := newEnv(t)
		env.ports.missingWorker = true
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": env.taskDef()},
			contract.CodeNotFound)
	})

	t.Run("inactive worker", func(t *testing.T) {
		env := newEnv(t)
		env.ports.setWorkerState(env.worker, "archived")
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": env.taskDef()},
			contract.CodePermissionDenied)
	})
}

func TestAdmissionArtifactValidation(t *testing.T) {
	t.Run("digest mismatch", func(t *testing.T) {
		env := newEnv(t)
		def := env.taskDef()
		ref := env.artifactFixture("pinned-input")
		def.Inputs = []wireArtifactRef{{ID: ref.ID, Digest: contract.Digest(altDigest)}}
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def},
			contract.CodeInvalidInput)
	})

	t.Run("unavailable artifact", func(t *testing.T) {
		env := newEnv(t)
		def := env.taskDef()
		def.Inputs = []wireArtifactRef{env.artifactFixtureState("faulted-input", "fault")}
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def},
			contract.CodeArtifactFault)
	})

	t.Run("foreign installation artifact", func(t *testing.T) {
		env := newEnv(t)
		other := wireScope{InstallationID: env.ids.New()}
		id := env.ids.New()
		env.ports.registerArtifact(id, other, altDigest, "application/octet-stream", "available")
		def := env.taskDef()
		def.Inputs = []wireArtifactRef{{ID: id, Digest: contract.Digest(altDigest)}}
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def},
			contract.CodePermissionDenied)
	})
}

func TestAdmissionLimitsValidation(t *testing.T) {
	t.Run("past deadline", func(t *testing.T) {
		env := newEnv(t)
		def := env.taskDef()
		def.Limits.RootDeadline = "2026-09-09T00:00:00Z"
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def},
			contract.CodeInvalidInput)
	})

	t.Run("zero concurrency", func(t *testing.T) {
		env := newEnv(t)
		def := env.taskDef()
		def.Limits.Concurrency = 0
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def},
			contract.CodeInvalidInput)
	})
}

func TestAdmissionRequiredOutputBinding(t *testing.T) {
	env := newEnv(t)
	def := env.taskDef()
	def.RequiredOutputs = []string{"report.bin", "extra.bin"}
	_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def},
		contract.CodeInvalidInput)
}

func TestAdmissionAcceptanceSealRefusals(t *testing.T) {
	cases := []struct {
		name string
		mut  func(def *taskDefInput)
	}{
		{"empty verifier identity", func(d *taskDefInput) { d.Acceptance.VerifierID = "" }},
		{"empty verifier version", func(d *taskDefInput) { d.Acceptance.VerifierVersion = "" }},
		{
			"unknown acceptance mode",
			func(d *taskDefInput) { d.Acceptance.Mode = "auto" },
		},
		{
			"artifact observations under a repository profile kind",
			func(d *taskDefInput) {
				d.Acceptance.Profile = repositoryProfileFixture()
			},
		},
		{
			"duplicate check ids",
			func(d *taskDefInput) {
				ref := wireArtifactRef{ID: contract.ID("00000000-0000-4000-8000-0000000000a1"),
					Digest: contract.Digest(altDigest)}
				obs := presenceObservation("same-check", "report.bin", ref)
				dup := obs
				dup.ArtifactName = "other.bin"
				d.Acceptance.ExpectedObservations = []wireExpectedObservation{obs, dup}
			},
		},
		{
			"presence observation without pinned digest",
			func(d *taskDefInput) {
				obs := presenceObservation("no-digest", "report.bin", wireArtifactRef{
					ID: contract.ID("00000000-0000-4000-8000-0000000000a2"),
				})
				obs.ExpectedDigest = ""
				d.Acceptance.ExpectedObservations = []wireExpectedObservation{obs}
				d.RequiredOutputs = nil
			},
		},
		{
			"json_schema observation without a schema",
			func(d *taskDefInput) {
				obs := wireExpectedObservation{
					CheckID: "schema-check", Kind: obsJSONSchema, Expected: observationExpectedPass,
				}
				d.Acceptance.ExpectedObservations = []wireExpectedObservation{obs}
				d.RequiredOutputs = nil
			},
		},
		{
			"profile does not declare the required check",
			func(d *taskDefInput) {
				// A json_schema demand against a profile that supports only
				// presence cannot fence success independently.
				obs := wireExpectedObservation{
					CheckID: "covered-check", Kind: obsJSONSchema,
					Expected: observationExpectedPass,
					Schema:   json.RawMessage(`{"type":"object"}`),
				}
				d.Acceptance.Profile = artifactProfileFixture(`"presence"`)
				d.Acceptance.ExpectedObservations = []wireExpectedObservation{obs}
				d.RequiredOutputs = nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			def := env.taskDef()
			tc.mut(&def)
			_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def},
				contract.CodeInvalidInput)
		})
	}
}

func TestAdmissionInitialStateValidation(t *testing.T) {
	t.Run("non-draft initial state", func(t *testing.T) {
		env := newEnv(t)
		def := env.fullTaskDef()
		def.State = stateRunning
		_ = env.expectFault("_tasks.create", map[string]any{
			"task": def, "source_id": env.ids.New(), "occurrence_key": "occ",
		}, contract.CodeInvalidInput)
	})

	t.Run("version other than one", func(t *testing.T) {
		env := newEnv(t)
		def := env.fullTaskDef()
		def.Version = 2
		_ = env.expectFault("_tasks.create", map[string]any{
			"task": def, "source_id": env.ids.New(), "occurrence_key": "occ",
		}, contract.CodeInvalidInput)
	})

	t.Run("foreign installation scope", func(t *testing.T) {
		env := newEnv(t)
		def := env.taskDef()
		def.Scope = wireScope{InstallationID: env.ids.New()}
		_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def},
			contract.CodePermissionDenied)
	})
}

func TestAdmissionRollsBackReservationOnCycle(t *testing.T) {
	env := newEnv(t)
	parent := env.createDefaultTask()

	// A child depending on its own parent closes a cycle the moment the
	// delegation edge parent->child is inserted. The reservation and the
	// child row must roll back together.
	childDef := env.taskDef()
	childDef.Dependencies = []contract.ID{parent}
	_ = env.expectFault("task.delegate", map[string]any{
		"scope": env.scope, "id": parent, "expected_version": int64(1), "child": childDef,
	}, contract.CodeInvalidInput)

	items, _ := env.listTasksPage(map[string]any{"scope": env.scope})
	if len(items) != 1 || items[0].ID != parent {
		t.Fatalf("rolled-back admission left %d tasks, want only the parent", len(items))
	}
	if v := env.snapshot(parent).Version; v != 1 {
		t.Fatalf("parent version moved to %d during a failed delegation", v)
	}
	// Both admissions reserved: the parent's own and the child's attempted
	// one. The child's reservation is rolled back with the child row.
	if got := len(env.ports.reservesOf()); got != 2 {
		t.Fatalf("reserve calls %d, want parent plus attempted child", got)
	}
}
