package tasks

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Acceptance-fence behavior for the proving cases assigned to this module:
// success never follows a worker claim, the sealed contract is tamper-evident,
// manual labels never read as verification, required children gate the
// parent, and delegation narrows every dimension it inherits.

func TestFenceSuccessRequiresEvidence(t *testing.T) {
	t.Run("exit zero without output evidence", func(t *testing.T) {
		env := newEnv(t)
		content := env.artifactFixture("unrelated-bytes")
		id := env.createDefaultTask()

		// The worker reports completion but never produces the pinned
		// output bytes: verifying entry carries only an unrelated artifact.
		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		v = env.runToVerifying(id, v, []contract.ID{content.ID})
		f := env.expectFault("_tasks.transition", map[string]any{
			"task_id": id, "expected_version": v, "state": stateSucceeded,
			"evidence_ids": []contract.ID{},
		}, contract.CodeVerificationFailed)
		if !strings.Contains(f.Message, "output-present") {
			t.Fatalf("refusal does not name the unestablished check: %s", f.Message)
		}
	})

	t.Run("no verifier result artifact for a schema check", func(t *testing.T) {
		env := newEnv(t)
		def := env.taskDef()
		def.RequiredOutputs = []string{}
		def.Acceptance = acceptanceFixture(wireExpectedObservation{
			CheckID: "schema-valid", Kind: obsJSONSchema,
			Expected: observationExpectedPass,
			Schema:   json.RawMessage(`{"type":"object"}`),
		})
		id := env.createTask(def)

		// Plain content bytes are not a verifier result: the media type
		// fence refuses the claim without a verification artifact.
		content := env.artifactFixture("plain-output")
		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		v = env.runToVerifying(id, v, []contract.ID{content.ID})
		f := env.expectFault("_tasks.transition", map[string]any{
			"task_id": id, "expected_version": v, "state": stateSucceeded,
			"evidence_ids": []contract.ID{},
		}, contract.CodeVerificationFailed)
		if !strings.Contains(f.Message, "no verifier result artifact") {
			t.Fatalf("refusal does not name the missing verifier result: %s", f.Message)
		}
	})

	t.Run("a manual label is not verification", func(t *testing.T) {
		env := newEnv(t)
		content := env.artifactFixture("default-output") // digest pinned by taskDef
		verifier := env.verifierArtifactFixture("verifier-result")
		id := env.createDefaultTask()

		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		v = env.runToVerifying(id, v, []contract.ID{content.ID, verifier})
		_ = env.expectFault("_tasks.transition", map[string]any{
			"task_id": id, "expected_version": v, "state": stateSucceeded,
			"evidence_ids": []contract.ID{}, "manual": true,
		}, contract.CodeVerificationFailed)
	})
}

func TestFenceTamperedAcceptance(t *testing.T) {
	env := newEnv(t)
	content := env.artifactFixture("default-output")
	verifier := env.verifierArtifactFixture("verifier-result")
	id := env.createDefaultTask()

	v := env.runToReady(id)
	v = env.runToRunning(id, v)
	v = env.runToVerifying(id, v, []contract.ID{content.ID, verifier})

	// Rewrite the stored contract out of band: the seal digest stays, the
	// bytes no longer hash to it.
	row := env.readRow(id)
	tampered := strings.Replace(row.AcceptanceJSON, `"v1.2.3"`, `"v9.9.9"`, 1)
	if tampered == row.AcceptanceJSON {
		t.Fatalf("tamper replacement found nothing to change in %s", row.AcceptanceJSON)
	}
	env.execSQL(`UPDATE tasks_tasks SET acceptance_json = ? WHERE id = ?`, tampered, string(id))

	f := env.expectFault("_tasks.transition", map[string]any{
		"task_id": id, "expected_version": v, "state": stateSucceeded,
		"evidence_ids": []contract.ID{},
	}, contract.CodeVerificationFailed)
	if !strings.Contains(f.Message, "sealed digest") {
		t.Fatalf("refusal does not name the seal mismatch: %s", f.Message)
	}

	// Dependent qualifications are invalidated before the refusal.
	invalidations := env.ports.invalidationsOf()
	if len(invalidations) != 1 {
		t.Fatalf("invalidation calls %d, want 1", len(invalidations))
	}
	if len(invalidations[0].ChangedDependencies) != 1 ||
		invalidations[0].ChangedDependencies[0].ID != id {
		t.Fatalf("invalidation does not name the tampered task: %+v", invalidations[0])
	}
}

func TestFenceRequiredChildren(t *testing.T) {
	t.Run("failed child blocks parent acceptance", func(t *testing.T) {
		env := newEnv(t)
		child := env.createDefaultTask()
		content := env.artifactFixture("parent-output")
		def := env.taskDef()
		def.Acceptance = acceptanceFixture(presenceObservation("output-present", "report.bin", content))
		def.Acceptance.RequiredChildIDs = []contract.ID{child}
		parent := env.createTask(def)

		// Drive the child to a terminal failure.
		cv := env.runToReady(child)
		cv = env.runToRunning(child, cv)
		cv = env.runToVerifying(child, cv, nil)
		env.transition(child, cv, stateFailed, nil)

		verifier := env.verifierArtifactFixture("parent-verifier")
		pv := env.runToReady(parent)
		pv = env.runToRunning(parent, pv)
		pv = env.runToVerifying(parent, pv, []contract.ID{content.ID, verifier})
		f := env.expectFault("_tasks.transition", map[string]any{
			"task_id": parent, "expected_version": pv, "state": stateSucceeded,
			"evidence_ids": []contract.ID{},
		}, contract.CodeVerificationFailed)
		if !strings.Contains(f.Message, "not succeeded") {
			t.Fatalf("refusal does not name the failed child: %s", f.Message)
		}
	})

	t.Run("manually accepted child cannot satisfy an independent parent", func(t *testing.T) {
		env := newEnv(t)
		manual := env.createManualTask(t)
		// Accept the manual child once the attempt is live.
		mv := env.runToReady(manual)
		mv = env.runToRunning(manual, mv)
		env.acceptManual(t, manual, mv, "accept")
		if row := env.readRow(manual); row.EstablishedBy != establishedManual {
			t.Fatalf("child established by %q, want manual", row.EstablishedBy)
		}

		content := env.artifactFixture("parent-output")
		def := env.taskDef()
		def.Acceptance = acceptanceFixture(presenceObservation("output-present", "report.bin", content))
		def.Acceptance.RequiredChildIDs = []contract.ID{manual}
		parent := env.createTask(def)

		verifier := env.verifierArtifactFixture("parent-verifier")
		pv := env.runToReady(parent)
		pv = env.runToRunning(parent, pv)
		pv = env.runToVerifying(parent, pv, []contract.ID{content.ID, verifier})
		f := env.expectFault("_tasks.transition", map[string]any{
			"task_id": parent, "expected_version": pv, "state": stateSucceeded,
			"evidence_ids": []contract.ID{},
		}, contract.CodeVerificationFailed)
		if !strings.Contains(f.Message, "not independent verification") {
			t.Fatalf("refusal does not name the establishment gap: %s", f.Message)
		}
	})

	t.Run("verified child completes the parent", func(t *testing.T) {
		env := newEnv(t)
		child := env.createDefaultTask()
		content := env.artifactFixture("default-output")
		verifier := env.verifierArtifactFixture("child-verifier")
		cv := env.runToReady(child)
		cv = env.runToRunning(child, cv)
		cv = env.runToVerifying(child, cv, []contract.ID{content.ID, verifier})
		env.transition(child, cv, stateSucceeded, nil)

		pcontent := env.artifactFixture("parent-output")
		def := env.taskDef()
		def.Acceptance = acceptanceFixture(presenceObservation("output-present", "report.bin", pcontent))
		def.Acceptance.RequiredChildIDs = []contract.ID{child}
		parent := env.createTask(def)

		pverifier := env.verifierArtifactFixture("parent-verifier")
		pv := env.runToReady(parent)
		pv = env.runToRunning(parent, pv)
		pv = env.runToVerifying(parent, pv, []contract.ID{pcontent.ID, pverifier})
		wire := env.transition(parent, pv, stateSucceeded, nil)
		if wire.State != stateSucceeded {
			t.Fatalf("parent state %s, want succeeded", wire.State)
		}
	})
}

func TestFenceManualAcceptanceLabel(t *testing.T) {
	t.Run("independent contract refuses manual decisions", func(t *testing.T) {
		env := newEnv(t)
		id := env.createDefaultTask()
		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		_ = env.expectFault("task.accept", map[string]any{
			"scope": env.scope, "id": id, "expected_version": v,
			"decision": "accept", "reason": "looked fine to me", "evidence_ids": []contract.ID{},
		}, contract.CodeInvalidInput)
	})

	t.Run("policy denial blocks the decision", func(t *testing.T) {
		env := newEnv(t)
		id := env.createManualTask(t)
		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		env.ports.setPolicy("deny")
		_ = env.expectFault("task.accept", map[string]any{
			"scope": env.scope, "id": id, "expected_version": v,
			"decision": "accept", "reason": "operator approved", "evidence_ids": []contract.ID{},
		}, contract.CodePermissionDenied)
	})

	t.Run("ineligible reviewer blocks the decision", func(t *testing.T) {
		env := newEnv(t)
		id := env.createManualTask(t)
		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		env.ports.setReviewsEligible(false)
		_ = env.expectFault("task.accept", map[string]any{
			"scope": env.scope, "id": id, "expected_version": v,
			"decision": "accept", "reason": "self approval", "evidence_ids": []contract.ID{},
		}, contract.CodePermissionDenied)
	})

	t.Run("accept records the durable decision", func(t *testing.T) {
		env := newEnv(t)
		id := env.createManualTask(t)
		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		acc := env.acceptManual(t, id, v, "accept")
		if acc.State != stateSucceeded {
			t.Fatalf("accept state %s, want succeeded", acc.State)
		}
		row := env.readRow(id)
		if row.EstablishedBy != establishedManual {
			t.Fatalf("established_by %q, want manual", row.EstablishedBy)
		}
		var decision, reviewer, digest string
		env.queryOne(
			`SELECT decision, reviewer_id, action_digest FROM tasks_manual_decisions WHERE task_id = ?`,
			[]any{string(id)},
			func(r *sql.Row) error {
				return r.Scan(&decision, &reviewer, &digest)
			})
		if decision != "accept" || reviewer != string(env.owner) || digest == "" {
			t.Fatalf("decision row drifted: decision %q reviewer %q digest %q", decision, reviewer, digest)
		}
	})

	t.Run("reject fails the task", func(t *testing.T) {
		env := newEnv(t)
		id := env.createManualTask(t)
		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		acc := env.acceptManual(t, id, v, "reject")
		if acc.State != stateFailed {
			t.Fatalf("reject state %s, want failed", acc.State)
		}
	})

	t.Run("transition can never establish manual success", func(t *testing.T) {
		env := newEnv(t)
		id := env.createManualTask(t)
		content := env.artifactFixture("default-output")
		verifier := env.verifierArtifactFixture("verifier-result")
		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		v = env.runToVerifying(id, v, []contract.ID{content.ID, verifier})
		_ = env.expectFault("_tasks.transition", map[string]any{
			"task_id": id, "expected_version": v, "state": stateSucceeded,
			"evidence_ids": []contract.ID{},
		}, contract.CodeVerificationFailed)
	})

	t.Run("draft task waits for a live attempt", func(t *testing.T) {
		env := newEnv(t)
		id := env.createManualTask(t)
		_ = env.expectFault("task.accept", map[string]any{
			"scope": env.scope, "id": id, "expected_version": int64(1),
			"decision": "accept", "reason": "too early", "evidence_ids": []contract.ID{},
		}, contract.CodeConflict)
	})
}

func TestFenceCancellationIntentBlocksEverything(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()

	// Intent at draft.
	env.mustCancel(t, id, 1)

	_ = env.expectFault("_tasks.transition", map[string]any{
		"task_id": id, "expected_version": 2, "state": stateReady,
		"evidence_ids": []contract.ID{},
	}, contract.CodeConflict)
	_ = env.expectFault("task.update", map[string]any{
		"scope": env.scope, "id": id, "expected_version": 2,
		"inputs": []wireArtifactRef{}, "outcome": "rewritten while cancelled",
	}, contract.CodeConflict)
	_ = env.expectFault("task.delegate", map[string]any{
		"scope": env.scope, "id": id, "expected_version": 2, "child": env.taskDef(),
	}, contract.CodeConflict)

	// Terminal disposition still resolves through the recorded intent.
	wire := env.transition(id, 2, stateCancelled, nil)
	if wire.State != stateCancelled {
		t.Fatalf("disposition state %s, want cancelled", wire.State)
	}
}

func TestFenceDelegationNarrowing(t *testing.T) {
	t.Run("project widening refused", func(t *testing.T) {
		env := newEnv(t)
		parent := env.createDefaultTask()
		child := env.taskDef()
		child.Scope = wireScope{InstallationID: env.install, ProjectID: "p-wide"}
		_ = env.expectFault("task.delegate", map[string]any{
			"scope": env.scope, "id": parent, "expected_version": int64(1), "child": child,
		}, contract.CodeInvalidInput)
	})

	t.Run("currency mismatch refused", func(t *testing.T) {
		env := newEnv(t)
		parent := env.createDefaultTask()
		child := env.taskDef()
		child.Limits.Currency = "EUR"
		_ = env.expectFault("task.delegate", map[string]any{
			"scope": env.scope, "id": parent, "expected_version": int64(1), "child": child,
		}, contract.CodeInvalidInput)
	})

	t.Run("spend expansion refused", func(t *testing.T) {
		env := newEnv(t)
		parent := env.createDefaultTask()
		child := env.taskDef()
		child.Limits.SpendMicroUnits = 999999
		_ = env.expectFault("task.delegate", map[string]any{
			"scope": env.scope, "id": parent, "expected_version": int64(1), "child": child,
		}, contract.CodeInvalidInput)
	})

	t.Run("late deadline is contained", func(t *testing.T) {
		env := newEnv(t)
		parent := env.createDefaultTask()
		child := env.taskDef()
		child.Limits.RootDeadline = "2027-01-01T00:00:00Z"
		id := env.mustDelegate(t, parent, child)
		if got := env.snapshot(id).Limits.RootDeadline; got != testDeadline {
			t.Fatalf("child deadline %s, want the parent deadline %s", got, testDeadline)
		}
	})

	t.Run("earlier deadline is kept", func(t *testing.T) {
		env := newEnv(t)
		parent := env.createDefaultTask()
		child := env.taskDef()
		child.Limits.RootDeadline = "2026-09-10T18:00:00Z"
		id := env.mustDelegate(t, parent, child)
		if got := env.snapshot(id).Limits.RootDeadline; got != "2026-09-10T18:00:00Z" {
			t.Fatalf("child deadline %s, want the requested earlier bound", got)
		}
	})

	t.Run("depth is consumed along the chain", func(t *testing.T) {
		env := newEnv(t)
		root := env.createDefaultTask()
		child := env.mustDelegate(t, root, env.taskDef())
		if got := env.snapshot(child).Limits.DelegationDepth; got != 1 {
			t.Fatalf("child depth %d, want 1 (parent 2 minus one edge)", got)
		}
		grandchild := env.mustDelegate(t, child, env.taskDef())
		if got := env.snapshot(grandchild).Limits.DelegationDepth; got != 0 {
			t.Fatalf("grandchild depth %d, want 0", got)
		}
		// A depth-0 grandchild cannot delegate at all.
		_ = env.expectFault("task.delegate", map[string]any{
			"scope": env.scope, "id": grandchild,
			"expected_version": env.snapshot(grandchild).Version, "child": env.taskDef(),
		}, contract.CodeInvalidInput)
	})

	t.Run("child count is finite", func(t *testing.T) {
		env := newEnv(t)
		parent := env.createDefaultTask()
		env.mustDelegate(t, parent, env.taskDef())
		env.mustDelegate(t, parent, env.taskDef())
		_ = env.expectFault("task.delegate", map[string]any{
			"scope": env.scope, "id": parent, "expected_version": env.snapshot(parent).Version,
			"child": env.taskDef(),
		}, contract.CodeInvalidInput)
	})

	t.Run("reservations share the root budget", func(t *testing.T) {
		env := newEnv(t)
		parent := env.createDefaultTask()
		child := env.mustDelegate(t, parent, env.taskDef())
		reserves := env.ports.reservesOf()
		if len(reserves) != 2 {
			t.Fatalf("reserve calls %d, want parent plus child", len(reserves))
		}
		if reserves[0].RootTaskID != parent || reserves[1].RootTaskID != parent {
			t.Fatalf("reservations do not share the root: %s then %s",
				reserves[0].RootTaskID, reserves[1].RootTaskID)
		}
		_ = child
	})
}

// createManualTask admits a manual-acceptance task whose one presence
// observation matches the default output artifact.
func (e *testEnv) createManualTask(t *testing.T) contract.ID {
	t.Helper()
	def := e.taskDef()
	def.Acceptance.Mode = acceptanceModeManual
	def.ManualAcceptance = true
	return e.createTask(def)
}

// acceptManual records a manual decision through task.accept.
func (e *testEnv) acceptManual(t *testing.T, id contract.ID, version int64, decision string) *wireTask {
	t.Helper()
	payload := e.mustOK("task.accept", map[string]any{
		"scope": e.scope, "id": id, "expected_version": version,
		"decision": decision, "reason": "operator decision", "evidence_ids": []contract.ID{},
	})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return &out.Resource
}
