package tasks

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// _tasks.dependencies.wake behavior: a bounded scan of the dependents of a
// just-completed task, returning their honest current state without ever
// synthesizing success for a dependent whose required child failed.

// wake calls _tasks.dependencies.wake and decodes its result.
func (e *testEnv) wake(completed contract.ID, limit int64, cursor string) ([]*wireTask, string) {
	e.t.Helper()
	payload := e.mustOK("_tasks.dependencies.wake", map[string]any{
		"completed_task_id": completed, "limit": limit, "cursor": cursor,
	})
	var out struct {
		Dependents []*wireTask `json:"dependents"`
		NextCursor string      `json:"next_cursor"`
	}
	e.decode(payload.Data, &out)
	return out.Dependents, out.NextCursor
}

// TestDependenciesWakeListsDependents: every task with a recorded
// dependency edge onto the completed task is returned, both a plain
// prerequisite dependent and a delegating parent whose required child it is.
func TestDependenciesWakeListsDependents(t *testing.T) {
	env := newEnv(t)
	completed := env.createDefaultTask()

	depDef := env.taskDef()
	depDef.Dependencies = []contract.ID{completed}
	prereqDependent := env.createTask(depDef)

	parentDef := env.taskDef()
	parentDef.Acceptance.RequiredChildIDs = []contract.ID{completed}
	parent := env.createTask(parentDef)
	// Record the parent->completed dependency edge the way delegation does
	// when a required child is attached (task.delegate's own admission path
	// creates a fresh child, so the edge is inserted directly here against
	// the already-existing "completed" task).
	env.execSQL(`INSERT INTO tasks_dependencies (task_id, depends_on_id, installation_id, created_at)
		VALUES (?, ?, ?, ?)`, string(parent), string(completed), string(env.install), "2026-09-10T12:00:00Z")

	items, next := env.wake(completed, 10, "")
	if next != "" {
		t.Fatalf("unexpected next_cursor %q for a single page", next)
	}
	seen := map[contract.ID]bool{}
	for _, it := range items {
		seen[it.ID] = true
	}
	if !seen[prereqDependent] {
		t.Fatalf("wake did not surface the plain prerequisite dependent: %+v", items)
	}
	if !seen[parent] {
		t.Fatalf("wake did not surface the delegating parent dependent: %+v", items)
	}
}

// TestDependenciesWakeNeverReportsSuccessForFailedRequiredChild: a parent
// whose required child just failed is returned exactly as it is durably
// stored -- never as succeeded, and wake performs no transition of its own.
func TestDependenciesWakeNeverReportsSuccessForFailedRequiredChild(t *testing.T) {
	env := newEnv(t)
	child := env.createDefaultTask()
	parentDef := env.taskDef()
	parentDef.Acceptance.RequiredChildIDs = []contract.ID{child}
	parent := env.createTask(parentDef)
	env.execSQL(`INSERT INTO tasks_dependencies (task_id, depends_on_id, installation_id, created_at)
		VALUES (?, ?, ?, ?)`, string(parent), string(child), string(env.install), "2026-09-10T12:00:00Z")

	// Drive the child to a terminal failure.
	cv := env.runToReady(child)
	cv = env.runToRunning(child, cv)
	cv = env.runToVerifying(child, cv, nil)
	env.transition(child, cv, stateFailed, nil)

	// Drive the parent partway, short of success.
	pv := env.runToReady(parent)
	pv = env.runToRunning(parent, pv)
	env.transition(parent, pv, stateVerifying, nil)

	items, _ := env.wake(child, 10, "")
	found := false
	for _, it := range items {
		if it.ID != parent {
			continue
		}
		found = true
		if it.State == stateSucceeded {
			t.Fatalf("wake reported the parent as succeeded despite its required child failing")
		}
		if it.State != stateVerifying {
			t.Fatalf("parent state %s, want the actual stored verifying state", it.State)
		}
	}
	if !found {
		t.Fatalf("wake did not surface the parent dependent")
	}

	// Wake itself never transitioned anything.
	row := env.readRow(parent)
	if row.State != stateVerifying {
		t.Fatalf("wake mutated the parent's stored state to %s", row.State)
	}
}

// TestDependenciesWakePagination pages through more dependents than fit in
// one limited page without loss or duplication.
func TestDependenciesWakePagination(t *testing.T) {
	env := newEnv(t)
	completed := env.createDefaultTask()

	var ids []contract.ID
	for i := 0; i < 3; i++ {
		def := env.taskDef()
		def.Dependencies = []contract.ID{completed}
		ids = append(ids, env.createTask(def))
	}

	page1, next1 := env.wake(completed, 2, "")
	if len(page1) != 2 || next1 == "" {
		t.Fatalf("first page items %d next %q, want 2 items and a cursor", len(page1), next1)
	}
	page2, next2 := env.wake(completed, 2, next1)
	if len(page2) != 1 || next2 != "" {
		t.Fatalf("second page items %d next %q, want 1 item and no cursor", len(page2), next2)
	}
	seen := map[contract.ID]bool{}
	for _, it := range append(page1, page2...) {
		if seen[it.ID] {
			t.Fatalf("dependent %s surfaced twice across pages", it.ID)
		}
		seen[it.ID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Fatalf("dependent %s missing from the paginated scan", id)
		}
	}
}

// TestDependenciesWakeRejectsUnknownCompletedTask: waking against a task
// identity that does not exist is refused, not an empty page.
func TestDependenciesWakeRejectsUnknownCompletedTask(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault("_tasks.dependencies.wake", map[string]any{
		"completed_task_id": env.ids.New(), "limit": int64(10),
	}, contract.CodeNotFound)
}

// TestDependenciesWakeRejectsBadLimit enforces the frozen 1..100 bound.
func TestDependenciesWakeRejectsBadLimit(t *testing.T) {
	env := newEnv(t)
	completed := env.createDefaultTask()
	_ = env.expectFault("_tasks.dependencies.wake", map[string]any{
		"completed_task_id": completed, "limit": int64(0),
	}, contract.CodeInvalidInput)
	_ = env.expectFault("_tasks.dependencies.wake", map[string]any{
		"completed_task_id": completed, "limit": int64(101),
	}, contract.CodeInvalidInput)
}

// TestDependenciesWakeNoDependentsIsAnEmptyPage: a task nothing depends on
// returns an empty, cursor-less page rather than a fault.
func TestDependenciesWakeNoDependentsIsAnEmptyPage(t *testing.T) {
	env := newEnv(t)
	completed := env.createDefaultTask()
	items, next := env.wake(completed, 10, "")
	if len(items) != 0 || next != "" {
		t.Fatalf("no-dependents wake = %+v, %q, want empty", items, next)
	}
}
