package effects

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Guards test the fences that protect the state machine: scope binding,
// protocol versioning, mutation-only writes and the task-scope prerequisite.

func TestHandleRejectsUnknownOperation(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault("operation.nope", struct{}{}, contract.CodeNotFound)
}

func TestHandleRejectsWrongProtocolVersion(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFaultOnVersion(opGet, 2,
		getOperationInput{Scope: unitScope(env.scope), ID: env.ids.New()},
		contract.CodeInvalidInput)
}

func TestHandleRejectsMutationOnReadOnlyUnit(t *testing.T) {
	env := newEnv(t)
	payload, err := env.callReadOnly(opPrepare, prepareInput{
		Scope: unitScope(env.scope), Action: env.action(), SourceID: env.ids.New(),
	})
	if err == nil {
		if payload.Error == nil {
			t.Fatal("prepare on a read-only unit completed; want a fault")
		}
		if payload.Error.Code != contract.CodeInvalidInput {
			t.Fatalf("read-only fault %s, want %s", payload.Error.Code, contract.CodeInvalidInput)
		}
		return
	}
	f := faultFrom(err)
	if f == nil || f.Code != contract.CodeInvalidInput {
		t.Fatalf("read-only error %v, want %s", err, contract.CodeInvalidInput)
	}
}

func TestHandleRejectsEmptyInstallationScope(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault(opGet, getOperationInput{Scope: wireScope{}, ID: env.ids.New()},
		contract.CodeInvalidInput)
}

func TestHandleRejectsForeignScopeInput(t *testing.T) {
	env := newEnv(t)
	foreign := env.scope
	foreign.InstallationID = env.ids.New()
	// The transaction runs in env's scope while the input names another
	// installation: the scope fence denies the call.
	_ = env.expectFault(opGet, getOperationInput{Scope: unitScope(foreign), ID: env.ids.New()},
		contract.CodePermissionDenied)
}

func TestAdmitSharesTaskRootWithReservation(t *testing.T) {
	env := newEnv(t)
	root := env.ids.New()
	env.ports.taskRootID = &root
	action := env.action()
	action.Scope.TaskID = env.ids.New()
	o := env.prepareOp(env.scope, action, env.ids.New())
	env.admitOp(o.ID, o.Version)
	reserves := env.reserveCalls()
	if len(reserves) != 1 || reserves[0].RootTaskID == nil || *reserves[0].RootTaskID != root {
		t.Fatalf("reservations %+v, want one reserve carrying task root %s", reserves, root)
	}

	// A top-level task reports no root; the reservation omits the dimension.
	env.ports.taskRootID = nil
	rootless := env.action()
	rootless.Scope.TaskID = env.ids.New()
	o2 := env.prepareOp(env.scope, rootless, env.ids.New())
	env.admitOp(o2.ID, o2.Version)
	for _, r := range env.reserveCalls()[1:] {
		if r.RootTaskID != nil {
			t.Fatalf("rootless-task reservation %+v carries a task root", r)
		}
	}
}

func TestPrepareRejectsActionScopeMismatch(t *testing.T) {
	env := newEnv(t)
	action := env.action()
	action.Scope.InstallationID = env.ids.New()
	_ = env.expectFault(opPrepare, prepareInput{
		Scope: unitScope(env.scope), Action: action, SourceID: env.ids.New(),
	}, contract.CodeInvalidInput)
}

func TestClaimExpiredWindowFails(t *testing.T) {
	env := newEnv(t)
	o, attempt := env.admitted()
	env.clock.Advance(claimTTL + time.Minute)
	_ = env.expectFault(opClaim, claimInput{
		OperationID: o.ID, AttemptID: attempt, Generation: env.generation(),
	}, contract.CodeConflict)
}

func TestAdmitRollsBackWhenReserveFaults(t *testing.T) {
	// A fault from a mid-sequence peer call must abort the whole admission
	// transaction: no attempt, no claim, no state transition.
	env := newEnv(t)
	o := env.staged()
	env.ports.failOp(opAccountingReserve, &contract.Fault{
		Code: contract.CodeConflict, Message: "concurrency cap reached",
	})
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
		contract.CodeConflict)
	after := env.mustFindOperation(o.ID)
	if after.State != opStatePrepared || after.Version != o.Version {
		t.Fatalf("post-fault operation state %q version %d, want prepared at version %d",
			after.State, after.Version, o.Version)
	}
	if attempts := env.attemptsOf(o.ID); len(attempts) != 0 {
		t.Fatalf("faulted admission left %d attempts, want none", len(attempts))
	}
}

func TestAdmitTwiceIsFenced(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	env.admitOp(o.ID, o.Version)
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
		contract.CodeStaleVersion)
}

func TestRecordOnUnadmittedOperationIsConflict(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	_ = env.expectFault(opRecord, recordInput{
		OperationID: o.ID, AttemptID: env.ids.New(), Generation: env.generation(),
		Observation: env.observation(dispSucceeded),
	}, contract.CodeNotFound)
}

func TestReconcileIsVersionFenced(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	stale := o.Version
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	// Reconcile reads and does not bump the version: the current version
	// reconciles, a version captured before the transition does not.
	env.reconcileOp(env.scope, o.ID, o.Version)
	_ = env.expectFault(opReconcile, reconcileInput{
		Scope: unitScope(env.scope), ID: o.ID, ExpectedVersion: stale,
	}, contract.CodeStaleVersion)
}

func TestPendingRejectsLimitBounds(t *testing.T) {
	env := newEnv(t)
	for _, limit := range []int64{0, -1, 101} {
		_ = env.expectFault(opPending, pendingInput{Limit: limit}, contract.CodeInvalidInput)
	}
}

func TestListRejectsLimitBounds(t *testing.T) {
	env := newEnv(t)
	zero := int64(0)
	over := int64(maxListLimit + 1)
	for _, limit := range []*int64{&zero, &over} {
		_ = env.expectFault(opList, listOperationsInput{
			Scope: unitScope(env.scope), Limit: limit,
		}, contract.CodeInvalidInput)
	}
}

func TestListRejectsUnsupportedFilters(t *testing.T) {
	env := newEnv(t)
	yes := true
	_ = env.expectFault(opList, listOperationsInput{
		Scope: unitScope(env.scope), Filter: &operationFilter{Descendants: &yes},
	}, contract.CodeInvalidInput)
	_ = env.expectFault(opList, listOperationsInput{
		Scope: unitScope(env.scope), Filter: &operationFilter{NeedsYou: &yes},
	}, contract.CodeInvalidInput)
}

func TestListRejectsUnknownStateFilter(t *testing.T) {
	env := newEnv(t)
	bogus := "half_done"
	_ = env.expectFault(opList, listOperationsInput{
		Scope: unitScope(env.scope), Filter: &operationFilter{State: &bogus},
	}, contract.CodeInvalidInput)
}

// operation.get carries its own org dimension: an explicitly-set request org
// must agree with the operation's stamped org, or the operation reads as
// not-found (existence in a scope you cannot reach is not distinguishable).
func TestOperationGetOrgDimension(t *testing.T) {
	env := newEnv(t)
	action := env.action()
	action.Scope.OrganizationID = env.org
	o := env.prepareOp(env.scope, action, env.ids.New())

	// The stamped org resolves.
	same := unitScope(env.scope)
	same.OrganizationID = env.org
	payload := env.mustOK(opGet, getOperationInput{Scope: same, ID: o.ID})
	var body operationResourceBody
	env.decode(payload.Data, &body)
	if body.Resource.ID != o.ID {
		t.Fatalf("org-scoped get returned operation %s, want %s", body.Resource.ID, o.ID)
	}

	// A foreign org does not.
	foreign := unitScope(env.scope)
	foreign.OrganizationID = env.ids.New()
	_ = env.expectFault(opGet, getOperationInput{Scope: foreign, ID: o.ID},
		contract.CodeNotFound)
}
