package execution

import (
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Run lifecycle behavior: enqueue pinning and dedup, the one-owner claim
// fence with its identity, pause, capability and budget gates, cancellation,
// and the bounded list views with their cursor fences.

func TestNewRequiresClockAndIDs(t *testing.T) {
	if _, err := New(contract.Dependencies{}); err == nil || faultCode(err) != contract.CodeInternalError {
		t.Fatalf("New without deps: %v, want internal_error", err)
	}
	if _, err := New(contract.Dependencies{Clock: &fakeClock{}}); err == nil || faultCode(err) != contract.CodeInternalError {
		t.Fatalf("New without IDs: %v, want internal_error", err)
	}
}

func TestEnqueuePinsRunAndDedups(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	task := e.fixtureTask(worker, 4)
	e.ports.setTask(&task)

	payload := e.mustOK(opEnqueue, enqueueInput{Task: task})
	var first runBody
	e.decode(payload.Data, &first)
	if first.Resource.State != "ready" || first.Resource.TaskID != task.ID {
		t.Fatalf("enqueue pinned %+v", first.Resource)
	}
	if first.Resource.ConfigurationRevision != 1 {
		t.Fatalf("configuration revision %d, want the snapshot revision 1", first.Resource.ConfigurationRevision)
	}

	payload = e.mustOK(opEnqueue, enqueueInput{Task: task})
	var replay runBody
	e.decode(payload.Data, &replay)
	if replay.Resource.ID != first.Resource.ID {
		t.Fatalf("repeated enqueue minted run %s, want the pinned run %s", replay.Resource.ID, first.Resource.ID)
	}
	var enqueued bool
	for _, ev := range e.readEvents() {
		if ev.Kind == eventRunEnqueued && ev.ResourceID == string(first.Resource.ID) {
			enqueued = true
		}
	}
	if !enqueued {
		t.Fatalf("run.enqueued event missing for run %s", first.Resource.ID)
	}
}

func TestEnqueueRejectsNonReadyTask(t *testing.T) {
	e := newEnv(t)
	task := e.fixtureTask(e.ids.New(), 4)
	task.State = "draft"
	f := e.expectFault(opEnqueue, enqueueInput{Task: task}, contract.CodeInvalidInput)
	if !strings.Contains(f.Message, "draft") {
		t.Fatalf("fault message %q does not name the task state", f.Message)
	}
}

func TestEnqueueRequiresInstallationScope(t *testing.T) {
	e := newEnv(t)
	task := e.fixtureTask(e.ids.New(), 4)
	task.Scope = contract.Scope{}
	e.ports.setTask(&task)
	_ = e.expectFault(opEnqueue, enqueueInput{Task: task}, contract.CodeInvalidInput)
}

func TestEnqueueCrossInstallationDenied(t *testing.T) {
	e := newEnv(t)
	task := e.fixtureTask(e.ids.New(), 4)
	// A fresh installation identity never matches the caller's scope.
	task.Scope.InstallationID = e.ids.New()
	f := e.expectFault(opEnqueue, enqueueInput{Task: task}, contract.CodePermissionDenied)
	if !strings.Contains(f.Message, "installation") {
		t.Fatalf("fault message %q does not name the installation fence", f.Message)
	}
}

func TestEnqueueRejectsUnavailableInput(t *testing.T) {
	e := newEnv(t)
	task := e.fixtureTask(e.ids.New(), 4)
	task.Inputs = []wireArtifactRef{{ID: e.ids.New(), Digest: fixtureDigest}}
	e.ports.setTask(&task)
	f := e.expectFault(opEnqueue, enqueueInput{Task: task}, contract.CodePrerequisiteMissing)
	if !strings.Contains(f.Message, "pinned input") {
		t.Fatalf("fault message %q does not name the pinned input", f.Message)
	}
}

func TestEnqueuePeerlessRuntime(t *testing.T) {
	e := newEnv(t)
	peerless, err := New(contract.Dependencies{Clock: e.clock, IDs: e.ids})
	if err != nil {
		t.Fatalf("peerless New: %v", err)
	}
	task := e.fixtureTask(e.ids.New(), 4)
	raw := mustMarshal(t, enqueueInput{Task: task})
	err = e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		_, err := peerless.Handle(e.ctx, unit, contract.Invocation{Operation: opEnqueue, Version: 1, Input: raw})
		return err
	})
	if faultCode(err) != contract.CodePrerequisiteMissing {
		t.Fatalf("peerless enqueue: %v, want prerequisite_missing", err)
	}
}

func TestRunClaimOneOwner(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)

	payload := e.mustOK(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker, ExpectedVersion: 1,
		Capabilities: []string{"model.steps"},
	})
	var claim claimBody
	e.decode(payload.Data, &claim)
	if claim.Attempt.State != "claimed" || claim.Attempt.Generation != 1 {
		t.Fatalf("claim attempt state %q generation %d", claim.Attempt.State, claim.Attempt.Generation)
	}
	if claim.Attempt.WorkerID != worker {
		t.Fatalf("claim bound worker %s, want %s", claim.Attempt.WorkerID, worker)
	}
	a := e.readAttempt(claim.Attempt.ID)
	if a.State != "claimed" || a.ReservationID == "" {
		t.Fatalf("stored attempt state %q reservation %q", a.State, a.ReservationID)
	}
	if got := e.readRun(run.ID); got.State != "running" {
		t.Fatalf("run state %q, want running", got.State)
	}
	transitions := e.ports.Transitions()
	if len(transitions) != 1 || transitions[0].State != "running" {
		t.Fatalf("task transitions %v, want one running transition", transitions)
	}
	lease := e.readLease(a.LeaseID)
	if lease == nil || lease.State != "active" {
		t.Fatalf("lease %+v, want an active lease", lease)
	}
}

func TestRunClaimReplaySameWorker(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	first := e.claimRun(run.ID, worker)

	payload := e.mustOK(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: 2, Capabilities: []string{"model.steps"},
	})
	var replay claimBody
	e.decode(payload.Data, &replay)
	if replay.Attempt.ID != first.Attempt.ID || replay.Attempt.LeaseID != first.Attempt.LeaseID {
		t.Fatalf("replayed claim minted attempt %s, want the original %s",
			replay.Attempt.ID, first.Attempt.ID)
	}
}

func TestRunClaimSecondWorkerConflicts(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	other := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	e.claimRun(run.ID, worker)

	// The one-owner fence answers a second worker with a conflict naming the
	// live owner; ownership never transfers through a racing claim.
	f := e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: other,
		ExpectedVersion: 2, Capabilities: []string{},
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "live attempt owner") {
		t.Fatalf("fault message %q does not name the one-owner fence", f.Message)
	}
	current := e.readRun(run.ID)
	if len(current.AttemptIDs) != 1 {
		t.Fatalf("run carries %d attempts, want exactly one", len(current.AttemptIDs))
	}
}

func TestRunClaimWrongWorkerIdentity(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	other := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)

	// The run is bound to the task's worker; another worker's claim is a
	// caller-scope violation even with no live attempt.
	_ = e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: other,
		ExpectedVersion: 1, Capabilities: []string{},
	}, contract.CodePermissionDenied)
}

func TestRunClaimPausedWorker(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	e.mustOK(opWorkerPause, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 1})

	f := e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: 1, Capabilities: []string{"model.steps"},
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "paused") {
		t.Fatalf("fault message %q does not name the pause gate", f.Message)
	}

	// Resume by a service principal lifts the gate.
	e.mustOK(opWorkerResume, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 2})
	e.claimRun(run.ID, worker)
}

func TestRunClaimCapabilityFence(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	f := e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: 1, Capabilities: []string{},
	}, contract.CodeCapabilityUnsupported)
	if !strings.Contains(f.Message, "model.steps") {
		t.Fatalf("fault message %q does not name the required capability", f.Message)
	}
}

func TestRunClaimStaleVersion(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	_ = e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: 7, Capabilities: []string{},
	}, contract.CodeStaleVersion)
}

func TestRunClaimCancelledRunConflict(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	e.mustOK(opRunCancel, cancelInput{Scope: e.scope, ID: run.ID, ExpectedVersion: 1, Reason: "unneeded"})
	_ = e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: 2, Capabilities: []string{"model.steps"},
	}, contract.CodeConflict)
}

func TestRunClaimTaskCancellationRequested(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	e.ports.setTask(&wireTask{ID: run.TaskID, Version: 1, State: "ready", CancellationRequested: true})
	_ = e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: 1, Capabilities: []string{"model.steps"},
	}, contract.CodeConflict)
}

func TestRunClaimPeerFaultPassesThrough(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	e.ports.setFault(peerAccountReserve, &contract.Fault{
		Code: contract.CodeBudgetUnavailable, Message: "budget exhausted",
	})
	// The accounting fault reaches the caller unchanged; the claim rolls back
	// and the run stays ready.
	_ = e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: 1, Capabilities: []string{"model.steps"},
	}, contract.CodeBudgetUnavailable)
	if got := e.readRun(run.ID); got.State != "ready" {
		t.Fatalf("run state %q after a rolled-back claim, want ready", got.State)
	}
}

// errRawPeer is a non-fault peer transport failure; bind wraps it into
// internal_error instead of leaking it raw.
type errRawPeer struct{}

func (errRawPeer) Error() string { return "connection reset" }

func TestRunClaimRawPeerErrorBecomesInternalError(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	e.ports.setRawError(peerTasksSnapshot, errRawPeer{})
	_ = e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: 1, Capabilities: []string{"model.steps"},
	}, contract.CodeInternalError)
}

func TestRunCancelStopsLiveAttempt(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)

	e.mustOK(opRunCancel, cancelInput{Scope: e.scope, ID: run.ID, ExpectedVersion: 2, Reason: "owner abort"})
	a := e.readAttempt(claim.Attempt.ID)
	if a.State != "stopped" || a.RecoveryReason != "owner abort" {
		t.Fatalf("attempt state %q reason %q, want stopped/owner abort", a.State, a.RecoveryReason)
	}
	if got := e.readRun(run.ID); got.State != "cancelled" {
		t.Fatalf("run state %q, want cancelled", got.State)
	}
	if lease := e.readLease(a.LeaseID); lease.State != "released" {
		t.Fatalf("lease state %q, want released", lease.State)
	}
	transitions := e.ports.Transitions()
	if len(transitions) != 2 || transitions[1].State != "cancelled" {
		t.Fatalf("task transitions %v, want running then cancelled", transitions)
	}
}

func TestRunCancelTerminalConflict(t *testing.T) {
	e := newEnv(t)
	run := e.enqueueTask(e.ids.New(), nil)
	e.mustOK(opRunCancel, cancelInput{Scope: e.scope, ID: run.ID, ExpectedVersion: 1, Reason: "first"})
	_ = e.expectFault(opRunCancel, cancelInput{Scope: e.scope, ID: run.ID, ExpectedVersion: 2, Reason: "again"},
		contract.CodeConflict)
}

func TestRunExportDedups(t *testing.T) {
	e := newEnv(t)
	run := e.enqueueTask(e.ids.New(), nil)
	payload := e.mustOK(opRunExport, getIDInput{Scope: e.scope, ID: run.ID})
	var first jobBody
	e.decode(payload.Data, &first)
	if first.Resource.Kind != "run_export" || first.Resource.State != "pending" {
		t.Fatalf("export job kind %q state %q", first.Resource.Kind, first.Resource.State)
	}
	payload = e.mustOK(opRunExport, getIDInput{Scope: e.scope, ID: run.ID})
	var replay jobBody
	e.decode(payload.Data, &replay)
	if replay.Resource.ID != first.Resource.ID {
		t.Fatalf("repeat export minted job %s, want the original %s", replay.Resource.ID, first.Resource.ID)
	}
}

func TestRunGetNotFound(t *testing.T) {
	e := newEnv(t)
	_ = e.expectFault(opRunGet, getIDInput{Scope: e.scope, ID: e.ids.New()}, contract.CodeNotFound)
}

func TestRunGetCrossOrgDenied(t *testing.T) {
	e := newEnv(t)
	run := e.enqueueTask(e.ids.New(), nil)
	scope := e.scope
	scope.OrganizationID = e.ids.New()
	_ = e.expectFault(opRunGet, getIDInput{Scope: scope, ID: run.ID}, contract.CodePermissionDenied)
}

func TestRunRecoveryListsObligations(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.mustOK(opFence, fenceInput{Generation: 5, Reason: "rotation"})

	payload := e.mustOK(opRunRecovery, getIDInput{Scope: e.scope, ID: run.ID})
	var body struct {
		Resource    wireRun           `json:"resource"`
		Obligations []wireRequirement `json:"obligations"`
	}
	e.decode(payload.Data, &body)
	if body.Resource.ID != run.ID {
		t.Fatalf("recovery resource %s, want run %s", body.Resource.ID, run.ID)
	}
	if len(body.Obligations) != 1 || body.Obligations[0].Code != "lease_conflict" {
		t.Fatalf("obligations %+v, want one lease_conflict", body.Obligations)
	}
	if e.readAttempt(claim.Attempt.ID).State != "fenced" {
		t.Fatalf("fence did not stop the live attempt")
	}
}

func TestRunListPagination(t *testing.T) {
	e := newEnv(t)
	ids := []contract.ID{}
	for i := 0; i < 3; i++ {
		run := e.enqueueTask(e.ids.New(), nil)
		ids = append(ids, run.ID)
	}
	limit := int64(1)
	payload := e.mustOK(opRunList, listInput{Scope: e.scope, Limit: &limit})
	var page1 runListBody
	e.decode(payload.Data, &page1)
	if len(page1.Items) != 1 || payload.NextCursor == nil {
		t.Fatalf("first page items %d cursor %v, want one item and a cursor", len(page1.Items), payload.NextCursor)
	}
	if page1.Items[0].ID != ids[2] {
		t.Fatalf("first page run %s, want newest %s", page1.Items[0].ID, ids[2])
	}

	payload = e.mustOK(opRunList, listInput{Scope: e.scope, Limit: &limit, Cursor: *payload.NextCursor})
	var page2 runListBody
	e.decode(payload.Data, &page2)
	if len(page2.Items) != 1 || page2.Items[0].ID != ids[1] {
		t.Fatalf("second page %+v, want run %s", page2.Items, ids[1])
	}
}

func listFilterPtr() *listFilter { return &listFilter{State: "ready"} }

func TestRunListCursorFences(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 2; i++ {
		e.enqueueTask(e.ids.New(), nil)
	}
	limit := int64(1)
	payload := e.mustOK(opRunList, listInput{Scope: e.scope, Limit: &limit, Filter: listFilterPtr()})
	cursor := *payload.NextCursor

	// A tampered cursor is malformed, not replayable.
	tampered := "x" + cursor[1:]
	_ = e.expectFault(opRunList, listInput{Scope: e.scope, Filter: listFilterPtr(), Cursor: tampered},
		contract.CodeInvalidInput)

	// Another principal's cursor does not decode for this caller.
	_, err := e.callAsActor(e.other, e.scope, opRunList,
		listInput{Scope: e.scope, Filter: listFilterPtr(), Cursor: cursor})
	if faultCode(err) != contract.CodeInvalidInput {
		t.Fatalf("foreign principal cursor: %v, want invalid_input", err)
	}

	// The cursor minted under one filter does not serve another filter.
	_ = e.expectFault(opRunList, listInput{Scope: e.scope, Cursor: cursor},
		contract.CodeInvalidInput)

	// An expired cursor reports cursor_expired with snapshot_required.
	e.clock.advance(16 * time.Minute)
	f := e.expectFault(opRunList, listInput{Scope: e.scope, Filter: listFilterPtr(), Cursor: cursor},
		contract.CodeCursorExpired)
	if !strings.Contains(string(f.Details), "snapshot_required") {
		t.Fatalf("cursor expired details %s, want snapshot_required", f.Details)
	}
}

func TestRunListFilterCrossesScope(t *testing.T) {
	e := newEnv(t)
	e.enqueueTask(e.ids.New(), nil)
	scope := e.scope
	scope.OrganizationID = e.org
	filter := &listFilter{Organization: e.ids.New()}
	_ = e.expectFault(opRunList, listInput{Scope: scope, Filter: filter}, contract.CodePermissionDenied)
}

func TestRunListUnsupportedFilterField(t *testing.T) {
	e := newEnv(t)
	e.enqueueTask(e.ids.New(), nil)
	filter := &listFilter{Key: "anything"}
	_ = e.expectFault(opRunList, listInput{Scope: e.scope, Filter: filter}, contract.CodeInvalidInput)
}

func TestUnknownOperationNotFound(t *testing.T) {
	e := newEnv(t)
	f := e.expectFault("execution.nonexistent", map[string]any{}, contract.CodeNotFound)
	if !strings.Contains(f.Message, "unknown operation") {
		t.Fatalf("fault message %q does not name the unknown operation", f.Message)
	}
}

func TestWrongVersionRejected(t *testing.T) {
	e := newEnv(t)
	_, err := e.call(opRunGet, getIDInput{Scope: e.scope, ID: e.ids.New()})
	if err == nil {
		t.Fatal("expected an error before any version check")
	}
	raw := mustMarshal(t, getIDInput{Scope: e.scope, ID: e.ids.New()})
	err = e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		_, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: opRunGet, Version: 9, Input: raw})
		return err
	})
	if faultCode(err) != contract.CodeInvalidInput {
		t.Fatalf("version mismatch: %v, want invalid_input", err)
	}
}

func TestMutationInReadOnlyTransaction(t *testing.T) {
	e := newEnv(t)
	run := e.enqueueTask(e.ids.New(), nil)
	raw := mustMarshal(t, cancelInput{Scope: e.scope, ID: run.ID, ExpectedVersion: 1, Reason: "no"})
	err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		_, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: opRunCancel, Version: 1, Input: raw})
		return err
	})
	if faultCode(err) != contract.CodeInvalidInput {
		t.Fatalf("read-only mutation: %v, want invalid_input", err)
	}
}
