package tasks

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// task.start behavior: the versioned draft-to-ready public path built on
// applyTransition, its idempotent restart of an already-ready task, and the
// start-time recheck of every admission fence -- prerequisites, sealed
// acceptance, budgets, pinned artifacts and worker binding -- required so a
// reassigned, updated, cancelled or dependency-blocked queued task cannot
// silently execute stale work.

// startTaskOK calls task.start and decodes the {task, run} result.
func (e *testEnv) startTaskOK(id contract.ID, version int64) (*wireTask, *wireRun) {
	e.t.Helper()
	payload := e.mustOK("task.start", map[string]any{
		"scope": e.scope, "id": id, "expected_version": version,
	})
	var out struct {
		Task wireTask `json:"task"`
		Run  wireRun  `json:"run"`
	}
	e.decode(payload.Data, &out)
	return &out.Task, &out.Run
}

// TestTaskStartCreatesExactlyOneReadyRun: public create then start creates
// exactly one ready run for the task/version pair.
func TestTaskStartCreatesExactlyOneReadyRun(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()

	task, run := env.startTaskOK(id, 1)
	if task.State != stateReady {
		t.Fatalf("task state %s, want ready", task.State)
	}
	if task.Version != 2 {
		t.Fatalf("task version %d, want 2", task.Version)
	}
	if run.TaskID != id || run.Version != task.Version || run.State == "" {
		t.Fatalf("run drifted: %+v", run)
	}
	if got := len(env.ports.callsOf("_execution.enqueue")); got != 1 {
		t.Fatalf("enqueue calls %d, want exactly 1", got)
	}
	if got := len(env.ports.enqueueRuns); got != 1 {
		t.Fatalf("distinct enqueued runs %d, want exactly 1", got)
	}
}

// TestTaskStartDuplicateStaleVersionIsDeterministic: a duplicate start call
// carrying the version from before the first call succeeded is refused
// deterministically (stale_version), never a second run.
func TestTaskStartDuplicateStaleVersionIsDeterministic(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	env.startTaskOK(id, 1)

	// A duplicate call against the now-superseded version is refused, every
	// time, the same way.
	for i := 0; i < 3; i++ {
		_ = env.expectFault("task.start", map[string]any{
			"scope": env.scope, "id": id, "expected_version": int64(1),
		}, contract.CodeStaleVersion)
	}
	if got := len(env.ports.enqueueRuns); got != 1 {
		t.Fatalf("distinct enqueued runs %d after duplicate stale starts, want exactly 1", got)
	}
}

// TestTaskStartIdempotentOnAlreadyReady: calling task.start again with the
// task's current (already-ready) version recovers the same ready task and
// the same deduplicated run, not a conflict and not a second run -- the
// idempotent-restart path a caller recovering from a lost acknowledgement
// depends on.
func TestTaskStartIdempotentOnAlreadyReady(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	first, firstRun := env.startTaskOK(id, 1)
	if first.State != stateReady {
		t.Fatalf("task state %s, want ready", first.State)
	}

	second, secondRun := env.startTaskOK(id, int64(first.Version))
	if second.State != stateReady || second.Version != first.Version {
		t.Fatalf("idempotent restart drifted: %+v", second)
	}
	if secondRun.ID != firstRun.ID || secondRun.Version != firstRun.Version {
		t.Fatalf("idempotent restart minted a distinct run: first %+v second %+v", firstRun, secondRun)
	}
	if got := len(env.ports.callsOf("_execution.enqueue")); got != 2 {
		t.Fatalf("enqueue calls %d, want 2 (initial ready plus the idempotent restart)", got)
	}
	if got := len(env.ports.enqueueRuns); got != 1 {
		t.Fatalf("distinct enqueued runs %d, want exactly 1 (deduplicated by task/version)", got)
	}
}

// TestTaskStartConcurrentIsDeterministic: two concurrent task.start calls
// against the same draft task and expected_version race through the
// database's single ordered writer; exactly one wins the transition to
// ready and exactly one run is ever created, never two, never neither
// resolved.
func TestTaskStartConcurrentIsDeterministic(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()

	const attempts = 4
	var wg sync.WaitGroup
	oks := make([]bool, attempts)
	faults := make([]*contract.Fault, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload, err := env.call("task.start", map[string]any{
				"scope": env.scope, "id": id, "expected_version": int64(1),
			})
			if err == nil && payload.Status == contract.StatusCompleted {
				oks[i] = true
				return
			}
			var f *contract.Fault
			if !asFault(err, &f) {
				f = payload.Error
			}
			faults[i] = f
		}(i)
	}
	wg.Wait()

	winners := 0
	for i, ok := range oks {
		if ok {
			winners++
			continue
		}
		f := faults[i]
		if f == nil {
			t.Fatalf("attempt %d neither succeeded nor carried a fault", i)
		}
		// Every loser fails deterministically: a version race
		// (stale_version) or, if a writer transaction had to wait past the
		// busy timeout, an explicitly retryable controller_unavailable --
		// never a silent success and never an unclassified failure.
		if f.Code != contract.CodeStaleVersion && f.Code != contract.CodeControllerUnavailable {
			t.Fatalf("attempt %d fault %s (%s), want stale_version or controller_unavailable", i, f.Code, f.Message)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent start winners %d, want exactly 1", winners)
	}
	if got := len(env.ports.enqueueRuns); got != 1 {
		t.Fatalf("distinct enqueued runs %d after concurrent start, want exactly 1", got)
	}
	row := env.readRow(id)
	if row.State != stateReady {
		t.Fatalf("task state %s after concurrent start, want ready", row.State)
	}
}

// TestTaskStartRejectsUnstartableStates: a task outside draft or ready
// (running, or terminal) refuses task.start with a deterministic conflict.
func TestTaskStartRejectsUnstartableStates(t *testing.T) {
	t.Run("already running", func(t *testing.T) {
		env := newEnv(t)
		id := env.createDefaultTask()
		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		_ = env.expectFault("task.start", map[string]any{
			"scope": env.scope, "id": id, "expected_version": v,
		}, contract.CodeConflict)
	})

	t.Run("terminal failed", func(t *testing.T) {
		env := newEnv(t)
		id := env.createDefaultTask()
		v := env.runToReady(id)
		v = env.runToRunning(id, v)
		v = env.runToVerifying(id, v, nil)
		env.transition(id, v, stateFailed, nil)
		_ = env.expectFault("task.start", map[string]any{
			"scope": env.scope, "id": id, "expected_version": v + 1,
		}, contract.CodeConflict)
	})
}

// TestTaskStartUnmetDependencyBlocks: task.start rejects a task whose
// declared dependency has not succeeded, the same prerequisite fence
// _tasks.transition applies, then admits it once the dependency does.
func TestTaskStartUnmetDependencyBlocks(t *testing.T) {
	env := newEnv(t)
	dep := env.createDefaultTask()
	def := env.taskDef()
	def.Dependencies = []contract.ID{dep}
	child := env.createTask(def)

	_ = env.expectFault("task.start", map[string]any{
		"scope": env.scope, "id": child, "expected_version": int64(1),
	}, contract.CodePrerequisiteMissing)

	// The default fixture task's own presence observation names "report.bin"
	// pinned to the digest of "default-output"; a fresh artifact of that
	// same name carries the identical digest and satisfies the binding.
	dv := env.runToReady(dep)
	dv = env.runToRunning(dep, dv)
	dv = env.runToVerifying(dep, dv, nil)
	verifier := env.verifierArtifactRefFixture("dep-verifier")
	bound := env.artifactFixture("default-output")
	env.recordEvidence(dep, dv, verifier, []wireOutputBinding{{Name: "report.bin", Artifact: bound}}, verdictPassed)
	env.transition(dep, dv, stateSucceeded, nil)

	task, _ := env.startTaskOK(child, int64(1))
	if task.State != stateReady {
		t.Fatalf("child state %s after dependency succeeded, want ready", task.State)
	}
}

// TestTaskStartRejectsCancelledQueuedWork: cancellation intent recorded
// against an already-ready (queued) task blocks a subsequent task.start
// from ever starting the stale queued work.
func TestTaskStartRejectsCancelledQueuedWork(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	task, _ := env.startTaskOK(id, 1)
	cancelled := env.mustCancel(t, id, int64(task.Version))
	_ = env.expectFault("task.start", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(cancelled.Version),
	}, contract.CodeConflict)
}

// TestTaskStartStaleInputUpdateSupersedes: a pending-input change to an
// already-queued task bumps its version and re-pins the enqueued run;
// task.start against the pre-update version is refused deterministically,
// so stale queued work (built on the superseded inputs) never starts.
func TestTaskStartStaleInputUpdateSupersedes(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	task, firstRun := env.startTaskOK(id, 1)
	if task.State != stateReady {
		t.Fatalf("task state %s, want ready", task.State)
	}

	// task.update is legal while ready; it re-pins the queued run under the
	// bumped version immediately, without waiting for a later task.start.
	input := env.artifactFixture("late-input")
	updated := env.mustOK("task.update", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(task.Version),
		"inputs": []wireArtifactRef{input},
	})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	env.decode(updated.Data, &out)
	if out.Resource.Version != task.Version+1 {
		t.Fatalf("update version %d, want %d", out.Resource.Version, task.Version+1)
	}
	if got := len(env.ports.enqueueRuns); got != 2 {
		t.Fatalf("distinct enqueued runs after update %d, want 2 (pre- and post-update version)", got)
	}
	freshRun, ok := env.ports.enqueueRuns[fmt.Sprintf("%s/%d", id, out.Resource.Version)]
	if !ok {
		t.Fatalf("update did not re-pin the run at the bumped version")
	}
	if freshRun.ID == firstRun.ID {
		t.Fatalf("update reused the stale pre-update run")
	}

	// Starting against the stale, pre-update version is refused.
	_ = env.expectFault("task.start", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(task.Version),
	}, contract.CodeStaleVersion)

	// Starting against the current version is the idempotent restart,
	// resolving to the already-current, re-pinned run.
	final, finalRun := env.startTaskOK(id, int64(out.Resource.Version))
	if final.Version != out.Resource.Version {
		t.Fatalf("idempotent restart version %d, want %d", final.Version, out.Resource.Version)
	}
	if finalRun.ID != freshRun.ID {
		t.Fatalf("idempotent restart resolved to a different run than the re-pin: %s vs %s", finalRun.ID, freshRun.ID)
	}
}

// TestTaskStartChangedAssignmentSupersedesQueuedRun: reassigning the worker
// of an already-queued (ready) task re-pins the run under the new worker
// immediately; the run tied to the pre-reassignment version is never what
// a subsequent start resolves to.
func TestTaskStartChangedAssignmentSupersedesQueuedRun(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	task, firstRun := env.startTaskOK(id, 1)

	newWorker := env.ids.New()
	assigned := env.mustOK("task.assign", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(task.Version), "worker_id": newWorker,
	})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	env.decode(assigned.Data, &out)
	if out.Resource.WorkerID != newWorker || out.Resource.State != stateReady {
		t.Fatalf("reassignment drifted: %+v", out.Resource)
	}
	freshRun, ok := env.ports.enqueueRuns[fmt.Sprintf("%s/%d", id, out.Resource.Version)]
	if !ok || freshRun.ID == firstRun.ID {
		t.Fatalf("reassignment did not re-pin a fresh run distinct from the pre-reassignment one")
	}

	// The stale, pre-reassignment version can never start.
	_ = env.expectFault("task.start", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(task.Version),
	}, contract.CodeStaleVersion)

	// The current, reassigned version resolves to the re-pinned run.
	_, currentRun := env.startTaskOK(id, int64(out.Resource.Version))
	if currentRun.ID != freshRun.ID {
		t.Fatalf("start after reassignment resolved to %s, want the re-pinned run %s", currentRun.ID, freshRun.ID)
	}
}

// TestTaskStartRechecksWorkerBinding: task.start's idempotent restart of an
// already-ready task refuses to re-enqueue once the assigned worker is no
// longer active.
func TestTaskStartRechecksWorkerBinding(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	task, _ := env.startTaskOK(id, 1)

	env.ports.setWorkerState(env.worker, "archived")
	_ = env.expectFault("task.start", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(task.Version),
	}, contract.CodePermissionDenied)
}

// TestTaskStartRechecksBudget: task.start's idempotent restart refuses to
// re-enqueue once the intersected accounting currency no longer matches the
// task's pinned currency.
func TestTaskStartRechecksBudget(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	task, _ := env.startTaskOK(id, 1)

	env.ports.inspectUsage.Currency = "EUR"
	_ = env.expectFault("task.start", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(task.Version),
	}, contract.CodeBudgetUnavailable)
}

// TestTaskStartRechecksSealedArtifacts: task.start's idempotent restart
// refuses to re-enqueue once a pinned input artifact stops resolving.
func TestTaskStartRechecksSealedArtifacts(t *testing.T) {
	env := newEnv(t)
	def := env.taskDef()
	input := env.artifactFixture("start-recheck-input")
	def.Inputs = []wireArtifactRef{input}
	id := env.createTask(def)
	task, _ := env.startTaskOK(id, 1)

	// The input artifact faults out from under the queued task.
	env.ports.artifacts[input.ID] = peerArtifact{
		ID: input.ID, Version: 1, Scope: env.scope, Digest: string(input.Digest),
		MediaType: "application/octet-stream", State: "fault",
	}
	_ = env.expectFault("task.start", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(task.Version),
	}, contract.CodeArtifactFault)
}

// TestTaskStartOutputSchemaCarriesFullRun confirms task.start's run output
// is not a truncated projection: every required Run field round-trips.
func TestTaskStartOutputSchemaCarriesFullRun(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	payload := env.mustOK("task.start", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(1),
	})
	var raw map[string]json.RawMessage
	env.decode(payload.Data, &raw)
	runRaw, ok := raw["run"]
	if !ok {
		t.Fatalf("task.start output carries no run member")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(runRaw, &probe); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	for _, field := range []string{"id", "version", "task_id", "configuration_revision", "input_versions", "state", "attempt_ids"} {
		if _, ok := probe[field]; !ok {
			t.Fatalf("run output missing required field %q: %s", field, runRaw)
		}
	}
}
