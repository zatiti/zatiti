package tasks

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Lifecycle behavior: admission through the state engine, versioned
// assignment and pending updates, waiting and resume, cancellation intent
// and disposition, retry, listing with filters and cursors, and the
// internal create dedup path.

func TestCreateAndGetTask(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()

	wire := env.snapshot(id)
	if wire.ID != id || wire.State != stateDraft {
		t.Fatalf("snapshot %s state %s, want %s draft", wire.ID, wire.State, stateDraft)
	}
	if wire.Version != 1 || wire.OwnerID != env.owner || wire.WorkerID != env.worker {
		t.Fatalf("snapshot identity drifted: %+v", wire)
	}
	if wire.RootID != id {
		t.Fatalf("direct task root %q, want self %s", wire.RootID, id)
	}
	if wire.Limits.Currency != "USD" || wire.Limits.RootDeadline != testDeadline {
		t.Fatalf("limits not stored verbatim: %+v", wire.Limits)
	}

	// task.get returns the same resource through the public path.
	payload := env.mustOK("task.get", map[string]any{"scope": env.scope, "id": id})
	var got struct {
		Resource wireTask `json:"resource"`
	}
	env.decode(payload.Data, &got)
	if got.Resource.ID != id || got.Resource.State != stateDraft {
		t.Fatalf("task.get resource drifted: %+v", got.Resource)
	}

	// The created event reached the storage outbox.
	events, err := env.db.Events(env.ctx, 0, 100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) == 0 || events[0].Kind != "tasks.task.created" || events[0].ResourceID != id {
		t.Fatalf("created event missing: %+v", events)
	}
}

func TestCreateRefusesForeignOwner(t *testing.T) {
	env := newEnv(t)
	def := env.taskDef()
	def.OwnerID = env.ids.New() // not the authenticated principal
	_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def},
		contract.CodePermissionDenied)
}

func TestCreateRefusesParentAttachment(t *testing.T) {
	env := newEnv(t)
	parent := env.createDefaultTask()
	def := env.taskDef()
	def.ParentID = parent
	_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def},
		contract.CodeInvalidInput)
}

func TestLifecycleThroughSuccess(t *testing.T) {
	env := newEnv(t)
	content := env.artifactFixture("report-output")
	verifier := env.verifierArtifactFixture("verifier-result")
	def := env.taskDef()
	def.RequiredOutputs = []string{"output.bin"}
	def.Acceptance = acceptanceFixture(presenceObservation("output-present", "output.bin", content))
	id := env.createTask(def)

	// draft -> ready enqueues exactly one run for task/version 1.
	v := env.runToReady(id)
	if v != 2 {
		t.Fatalf("ready version %d, want 2", v)
	}
	runs := env.ports.callsOf("_execution.enqueue")
	if len(runs) != 1 {
		t.Fatalf("enqueue calls %d after ready, want 1", len(runs))
	}

	// ready -> running -> verifying with evidence -> succeeded.
	v = env.runToRunning(id, v)
	v = env.runToVerifying(id, v, []contract.ID{content.ID, verifier})
	wire := env.transition(id, v, stateSucceeded, nil)
	if wire.State != stateSucceeded || wire.Version != contract.Version(v+1) {
		t.Fatalf("success drifted: state %s version %d", wire.State, wire.Version)
	}
	row := env.readRow(id)
	if row.EstablishedBy != establishedVerification {
		t.Fatalf("established_by %q, want verification", row.EstablishedBy)
	}

	// Terminal: nothing further moves.
	_ = env.expectFault("_tasks.transition", map[string]any{
		"task_id": id, "expected_version": wire.Version, "state": stateFailed, "evidence_ids": []contract.ID{},
	}, contract.CodeConflict)
}

func TestVersionedAssignment(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()

	payload := env.mustOK("task.assign", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(1), "worker_id": env.worker,
	})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	env.decode(payload.Data, &out)
	if out.Resource.WorkerID != env.worker || out.Resource.Version != 2 {
		t.Fatalf("assign result drifted: worker %s version %d", out.Resource.WorkerID, out.Resource.Version)
	}

	// A second assignment against the stale version loses cleanly.
	_ = env.expectFault("task.assign", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(1), "worker_id": env.worker,
	}, contract.CodeStaleVersion)
}

func TestPendingContractUpdate(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()

	input := env.artifactFixture("late-input")
	payload := env.mustOK("task.update", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(1),
		"inputs": []wireArtifactRef{input}, "outcome": "rewritten outcome",
	})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	env.decode(payload.Data, &out)
	if out.Resource.Outcome != "rewritten outcome" || out.Resource.Version != 2 {
		t.Fatalf("update result drifted: %+v", out.Resource)
	}
	if len(out.Resource.Inputs) != 1 || out.Resource.Inputs[0].ID != input.ID {
		t.Fatalf("update inputs not stored: %+v", out.Resource.Inputs)
	}

	// Once running, the pending contract freezes.
	v := int64(env.transition(id, 2, stateReady, nil).Version)
	v = env.runToRunning(id, v)
	_ = env.expectFault("task.update", map[string]any{
		"scope": env.scope, "id": id, "expected_version": v, "inputs": []wireArtifactRef{},
	}, contract.CodeConflict)
}

func TestWaitingAndResume(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	v := env.runToReady(id)

	// ready -> waiting with an explicit reason.
	wire := env.transitionWait(id, v, "awaiting operator input")
	if wire.State != stateWaiting || wire.WaitingReason == "" {
		t.Fatalf("waiting result drifted: %+v", wire)
	}
	if wire.Version != contract.Version(v+1) {
		t.Fatalf("waiting version %d, want %d", wire.Version, v+1)
	}
	row := env.readRow(id)
	if row.PriorState != stateReady {
		t.Fatalf("prior state %q, want ready", row.PriorState)
	}

	// Waiting resolves only to its prior state.
	_ = env.expectFault("_tasks.transition", map[string]any{
		"task_id": id, "expected_version": wire.Version, "state": stateRunning,
		"evidence_ids": []contract.ID{},
	}, contract.CodeConflict)

	// Entering waiting without a reason is refused (on a fresh ready task).
	other := env.createDefaultTask()
	ov := env.runToReady(other)
	_ = env.expectFault("_tasks.transition", map[string]any{
		"task_id": other, "expected_version": ov, "state": stateWaiting,
		"evidence_ids": []contract.ID{},
	}, contract.CodeInvalidInput)

	// Resume to ready: same task/version run, no duplicate enqueue.
	resumed := env.transition(id, int64(wire.Version), stateReady, nil)
	if resumed.State != stateReady || resumed.WaitingReason != "" {
		t.Fatalf("resume drifted: %+v", resumed)
	}
	run, ok := env.ports.enqueueRuns[fmt.Sprintf("%s/%d", id, resumed.Version)]
	if !ok {
		t.Fatalf("resumed task not enqueued at version %d", resumed.Version)
	}
	if run.Version != int64(resumed.Version) {
		t.Fatalf("run version %d, want %d", run.Version, resumed.Version)
	}
	// Enqueue dedup: the same task/version pair appears once.
	enqueueCalls := env.ports.callsOf("_execution.enqueue")
	seen := map[string]bool{}
	for _, c := range enqueueCalls {
		var in peerEnqueueIn
		if err := json.Unmarshal(c.Input, &in); err != nil {
			t.Fatalf("enqueue input: %v", err)
		}
		key := fmt.Sprintf("%s/%d", in.Task.ID, in.Task.Version)
		if seen[key] {
			t.Fatalf("duplicate enqueue for %s", key)
		}
		seen[key] = true
	}
}

func TestCancellationIntentAndDisposition(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	v := env.runToReady(id)
	v = env.runToRunning(id, v)

	// Record intent.
	wire := env.mustCancel(t, id, v)
	if !wire.CancellationRequested {
		t.Fatalf("cancellation intent not recorded")
	}

	// Intent blocks work entry and reassignment.
	_ = env.expectFault("_tasks.transition", map[string]any{
		"task_id": id, "expected_version": wire.Version, "state": stateVerifying,
		"evidence_ids": []contract.ID{},
	}, contract.CodeConflict)
	_ = env.expectFault("task.assign", map[string]any{
		"scope": env.scope, "id": id, "expected_version": wire.Version, "worker_id": env.worker,
	}, contract.CodeConflict)

	// Terminal cancellation requires the recorded intent (present here).
	cancelled := env.transition(id, int64(wire.Version), stateCancelled, nil)
	if cancelled.State != stateCancelled {
		t.Fatalf("cancelled transition drifted: %+v", cancelled)
	}

	// Retry from cancelled opens a new attempt under the same contract.
	payload := env.mustOK("task.retry", map[string]any{
		"scope": env.scope, "id": id, "expected_version": cancelled.Version, "reason": "false alarm",
	})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	env.decode(payload.Data, &out)
	if out.Resource.State != stateReady || out.Resource.Version != cancelled.Version+1 {
		t.Fatalf("retry drifted: %+v", out.Resource)
	}
	row := env.readRow(id)
	if row.Attempt != 1 {
		t.Fatalf("attempt %d after retry, want 1", row.Attempt)
	}
	if row.CancellationRequested {
		t.Fatalf("retry must clear the cancellation intent")
	}
	run, ok := env.ports.enqueueRuns[fmt.Sprintf("%s/%d", id, out.Resource.Version)]
	if !ok || run.Version != int64(out.Resource.Version) {
		t.Fatalf("retry did not enqueue the fresh task version")
	}
}

func TestTerminalCancelRefusedWithoutIntent(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	v := env.runToReady(id)
	_ = env.expectFault("_tasks.transition", map[string]any{
		"task_id": id, "expected_version": v, "state": stateCancelled, "evidence_ids": []contract.ID{},
	}, contract.CodeConflict)
}

func TestCancelOfTerminalTaskRefused(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	v := env.runToReady(id)
	v = env.runToRunning(id, v)
	v = env.runToVerifying(id, v, nil)
	env.transition(id, v, stateFailed, nil)
	_ = env.expectFault("task.cancel", map[string]any{
		"scope": env.scope, "id": id, "expected_version": v + 1, "reason": "late",
	}, contract.CodeConflict)
}

func TestRetryOnlyFromFailedOrCancelled(t *testing.T) {
	env := newEnv(t)
	id := env.createDefaultTask()
	_ = env.expectFault("task.retry", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(1), "reason": "too early",
	}, contract.CodeConflict)
}

func TestLegalTransitionMatrix(t *testing.T) {
	env := newEnv(t)

	t.Run("draft to running is refused", func(t *testing.T) {
		id := env.createDefaultTask()
		_ = env.expectFault("_tasks.transition", map[string]any{
			"task_id": id, "expected_version": int64(1), "state": stateRunning,
			"evidence_ids": []contract.ID{},
		}, contract.CodeConflict)
	})

	t.Run("stale expected version is refused", func(t *testing.T) {
		id := env.createDefaultTask()
		_ = env.expectFault("_tasks.transition", map[string]any{
			"task_id": id, "expected_version": int64(7), "state": stateReady,
			"evidence_ids": []contract.ID{},
		}, contract.CodeStaleVersion)
	})

	t.Run("unknown target is invalid_input", func(t *testing.T) {
		id := env.createDefaultTask()
		_ = env.expectFault("_tasks.transition", map[string]any{
			"task_id": id, "expected_version": int64(1), "state": "quantum",
			"evidence_ids": []contract.ID{},
		}, contract.CodeInvalidInput)
	})
}

func TestDependenciesListing(t *testing.T) {
	env := newEnv(t)
	dep := env.createDefaultTask()
	def := env.taskDef()
	def.Dependencies = []contract.ID{dep}
	child := env.createTask(def)

	payload := env.mustOK("task.dependencies", map[string]any{"scope": env.scope, "id": child})
	var out struct {
		Dependencies []*wireTask `json:"dependencies"`
	}
	env.decode(payload.Data, &out)
	if len(out.Dependencies) != 1 || out.Dependencies[0].ID != dep {
		t.Fatalf("dependencies listing drifted: %+v", out.Dependencies)
	}

	// Readiness is fenced on dependency success: the dependency is draft.
	_ = env.expectFault("_tasks.transition", map[string]any{
		"task_id": child, "expected_version": int64(1), "state": stateReady,
		"evidence_ids": []contract.ID{},
	}, contract.CodePrerequisiteMissing)

	// Unknown dependency ids are refused at admission.
	def2 := env.taskDef()
	def2.Dependencies = []contract.ID{env.ids.New()}
	_ = env.expectFault("task.create", map[string]any{"scope": env.scope, "definition": def2},
		contract.CodeNotFound)
}

func TestListFiltersAndPagination(t *testing.T) {
	env := newEnv(t)
	a := env.createDefaultTask()
	b := env.createDefaultTask()
	other := env.createDefaultTask()

	// Assign one task to a distinct worker and filter on it.
	worker2 := env.ids.New()
	env.mustOK("task.assign", map[string]any{
		"scope": env.scope, "id": b, "expected_version": int64(1), "worker_id": worker2,
	})

	// Move one task to ready.
	env.runToReady(other)

	if items, _ := env.listTasksPage(map[string]any{"scope": env.scope}); len(items) != 3 {
		t.Fatalf("unfiltered list %d items, want 3", len(items))
	}
	if items, _ := env.listTasksPage(map[string]any{
		"scope": env.scope, "filter": map[string]any{"state": stateDraft},
	}); len(items) != 2 {
		t.Fatalf("draft filter %d items, want 2", len(items))
	}
	items, _ := env.listTasksPage(map[string]any{
		"scope": env.scope, "filter": map[string]any{"worker_id": worker2},
	})
	if len(items) != 1 || items[0].ID != b {
		t.Fatalf("worker filter drifted: %+v", items)
	}
	items, _ = env.listTasksPage(map[string]any{
		"scope": env.scope, "filter": map[string]any{"task_id": a},
	})
	if len(items) != 1 || items[0].ID != a {
		t.Fatalf("task_id filter drifted: %+v", items)
	}

	// Unsupported task filters refuse invalid_input.
	_ = env.expectFault("task.list", map[string]any{
		"scope": env.scope, "filter": map[string]any{"key": "wake"},
	}, contract.CodeInvalidInput)
	_ = env.expectFault("task.list", map[string]any{
		"scope": env.scope, "filter": map[string]any{"needs_you": true},
	}, contract.CodeInvalidInput)
	_ = env.expectFault("task.list", map[string]any{
		"scope": env.scope, "filter": map[string]any{"state": "bogus"},
	}, contract.CodeInvalidInput)

	// Descendants listing follows the delegation dependency edge.
	parent := env.mustDelegate(t, a, env.taskDef())
	descendants, _ := env.listTasksPage(map[string]any{
		"scope": env.scope, "filter": map[string]any{"descendants": true, "parent_id": a},
	})
	if len(descendants) != 1 || descendants[0].ID != parent {
		t.Fatalf("descendant listing drifted: %+v", descendants)
	}

	// Pagination: limit 2 pages through the four tasks (a, b, other, child).
	page1, next := env.listTasksPage(map[string]any{"scope": env.scope, "limit": int64(2)})
	if len(page1) != 2 || next == "" {
		t.Fatalf("first page items %d next %q", len(page1), next)
	}
	page2, next2 := env.listTasksPage(map[string]any{"scope": env.scope, "limit": int64(2), "cursor": next})
	if len(page2) != 2 || next2 == "" {
		t.Fatalf("second page items %d next %q", len(page2), next2)
	}
	if page1[0].ID == page2[0].ID {
		t.Fatalf("pages overlap")
	}
	page3, next3 := env.listTasksPage(map[string]any{"scope": env.scope, "limit": int64(2), "cursor": next2})
	if len(page3) != 0 || next3 != "" {
		t.Fatalf("third page items %d next %q", len(page3), next3)
	}

	// A cursor minted for one query fails against another.
	_ = env.expectFault("task.list", map[string]any{
		"scope": env.scope, "limit": int64(2), "cursor": next,
		"filter": map[string]any{"state": stateDraft},
	}, contract.CodeCursorExpired)
	f := env.expectFault("task.list", map[string]any{
		"scope": env.scope, "limit": int64(2), "cursor": `{"offset":9,"sig":"deadbeef"}`,
	}, contract.CodeCursorExpired)
	if !strings.Contains(string(f.Details), "snapshot_required") {
		t.Fatalf("cursor_expired details %s missing snapshot_required", f.Details)
	}
}

func TestInternalCreateDedup(t *testing.T) {
	env := newEnv(t)
	def := env.fullTaskDef()

	source := env.ids.New()
	key := "wake-occurrence-1"
	mkIn := func() map[string]any {
		return map[string]any{"task": def, "source_id": source, "occurrence_key": key}
	}

	payload := env.mustOK("_tasks.create", mkIn())
	var first struct {
		Resource wireTask `json:"resource"`
	}
	env.decode(payload.Data, &first)

	// Identical content under the same occurrence returns the same task.
	payload2 := env.mustOK("_tasks.create", mkIn())
	var second struct {
		Resource wireTask `json:"resource"`
	}
	env.decode(payload2.Data, &second)
	if second.Resource.ID != first.Resource.ID {
		t.Fatalf("replay created %s, want existing %s", second.Resource.ID, first.Resource.ID)
	}
	if got := len(env.ports.callsOf("_accounting.reserve")); got != 1 {
		t.Fatalf("reserve calls %d after replay, want 1 (dedup must not re-admit)", got)
	}

	// Different content under the same occurrence is a conflict.
	changed := def
	changed.Outcome = "a different outcome"
	_ = env.expectFault("_tasks.create", map[string]any{
		"task": changed, "source_id": source, "occurrence_key": key,
	}, contract.CodeSubmissionConflict)

	// Empty source identity is refused.
	_ = env.expectFault("_tasks.create", map[string]any{
		"task": def, "source_id": "  ", "occurrence_key": key,
	}, contract.CodeInvalidInput)
}

func TestReadyScan(t *testing.T) {
	env := newEnv(t)
	a := env.createDefaultTask()
	b := env.createDefaultTask()
	c := env.createDefaultTask()
	env.runToReady(a)
	env.runToReady(b)

	// A ready task with cancellation intent stays out of the scan.
	v := env.runToReady(c)
	env.mustCancel(t, c, v)

	payload := env.mustOK("_tasks.ready", map[string]any{"limit": int64(10)})
	var out struct {
		Items []*wireTask `json:"items"`
	}
	env.decode(payload.Data, &out)
	if len(out.Items) != 2 {
		t.Fatalf("ready scan %d items, want 2", len(out.Items))
	}
	for _, item := range out.Items {
		if item.ID == c {
			t.Fatalf("cancelled-intent task surfaced in ready scan")
		}
	}

	_ = env.expectFault("_tasks.ready", map[string]any{"limit": int64(0)}, contract.CodeInvalidInput)
	_ = env.expectFault("_tasks.ready", map[string]any{"limit": int64(101)}, contract.CodeInvalidInput)
}

// mustCancel records cancellation intent through the public op and returns
// the resulting resource.
func (e *testEnv) mustCancel(t *testing.T, id contract.ID, version int64) *wireTask {
	t.Helper()
	payload := e.mustOK("task.cancel", map[string]any{
		"scope": e.scope, "id": id, "expected_version": version, "reason": "user requested",
	})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return &out.Resource
}

// mustDelegate delegates one child from the parent and returns its id.
func (e *testEnv) mustDelegate(t *testing.T, parent contract.ID, child taskDefInput) contract.ID {
	t.Helper()
	payload := e.mustOK("task.delegate", map[string]any{
		"scope": e.scope, "id": parent, "expected_version": e.snapshot(parent).Version,
		"child": child,
	})
	var out struct {
		Resource wireTask `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource.ID
}
