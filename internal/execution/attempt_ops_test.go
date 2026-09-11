package execution

import (
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Attempt lifecycle: heartbeat, checkpoint and report share the identity
// fence — the current lease, the owner generation, a live state and an open
// lease window. A report enters verification and never succeeds a task
// directly; cancellation preserves uncertain effects as obligations.

func TestAttemptHeartbeatExtendsLease(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	before := e.readAttempt(claim.Attempt.ID)
	e.clock.advance(5 * time.Second)

	payload := e.mustOK(opAttemptHeartbeat, heartbeatInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: claim.Attempt.LeaseID,
		Generation: claim.Attempt.Generation, ExpectedVersion: before.Version,
	})
	var body attemptBody
	e.decode(payload.Data, &body)
	if body.Resource.LastHeartbeat == claim.Attempt.LastHeartbeat {
		t.Fatalf("heartbeat did not advance the stamp")
	}
	a := e.readAttempt(claim.Attempt.ID)
	if !a.LeaseExpiresAt.After(before.LeaseExpiresAt) {
		t.Fatalf("lease expiry %v did not extend past %v", a.LeaseExpiresAt, before.LeaseExpiresAt)
	}
	if lease := e.readLease(a.LeaseID); lease.State != "active" {
		t.Fatalf("lease state %q, want active", lease.State)
	}
}

func TestAttemptHeartbeatWrongLease(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)
	f := e.expectFault(opAttemptHeartbeat, heartbeatInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: e.ids.New(),
		Generation: a.Generation, ExpectedVersion: a.Version,
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "is not the attempt's current lease") {
		t.Fatalf("fault message %q does not name the lease fence", f.Message)
	}
}

func TestAttemptHeartbeatWrongGeneration(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)
	f := e.expectFault(opAttemptHeartbeat, heartbeatInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation + 1, ExpectedVersion: a.Version,
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "does not match the attempt's current generation") {
		t.Fatalf("fault message %q does not name the generation fence", f.Message)
	}
}

func TestAttemptHeartbeatOnStoppedAttempt(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)
	e.mustOK(opAttemptCancel, cancelInput{
		Scope: e.scope, ID: claim.Attempt.ID, ExpectedVersion: a.Version, Reason: "worker shutdown",
	})
	stopped := e.readAttempt(claim.Attempt.ID)
	f := e.expectFault(opAttemptHeartbeat, heartbeatInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: stopped.LeaseID,
		Generation: stopped.Generation, ExpectedVersion: stopped.Version,
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "cannot be revived") {
		t.Fatalf("fault message %q does not name the revival fence", f.Message)
	}
}

func TestAttemptHeartbeatExpiredLease(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)
	e.clock.advance(2 * time.Minute)
	f := e.expectFault(opAttemptHeartbeat, heartbeatInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation, ExpectedVersion: a.Version,
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "lease expired at") {
		t.Fatalf("fault message %q does not name the expiry fence", f.Message)
	}
	// The tick scan fences the same expired attempt independently.
	payload := e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})
	var body fenceBody
	e.decode(payload.Data, &body)
	if len(body.AttemptIDs) != 1 || body.AttemptIDs[0] != claim.Attempt.ID {
		t.Fatalf("tick fenced %v, want the expired attempt", body.AttemptIDs)
	}
	if got := e.readAttempt(claim.Attempt.ID); got.State != "fenced" {
		t.Fatalf("attempt state %q, want fenced", got.State)
	}
}

func TestAttemptHeartbeatStaleVersion(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	_ = e.expectFault(opAttemptHeartbeat, heartbeatInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: claim.Attempt.LeaseID,
		Generation: claim.Attempt.Generation, ExpectedVersion: 9,
	}, contract.CodeStaleVersion)
}

func TestAttemptHeartbeatForeignWorkerScope(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)
	scope := e.scope
	scope.WorkerID = e.ids.New()
	_ = e.expectFault(opAttemptHeartbeat, heartbeatInput{
		Scope: scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation, ExpectedVersion: a.Version,
	}, contract.CodePermissionDenied)
}

func TestAttemptCheckpointPinsContext(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)

	payload := e.mustOK(opAttemptCheckpoint, checkpointInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation, ExpectedVersion: a.Version,
		Context: wireArtifactRef{ID: e.ids.New(), Digest: fixtureDigest},
		Outputs: []wireArtifactRef{{ID: e.ids.New(), Digest: digestB}},
	})
	var body attemptBody
	e.decode(payload.Data, &body)
	if body.Resource.ContextArtifact == nil || body.Resource.ContextArtifact.Digest != fixtureDigest {
		t.Fatalf("checkpoint context %+v", body.Resource.ContextArtifact)
	}
	// A checkpoint under a foreign lease is refused. Re-read first: the
	// first checkpoint advanced the attempt's version.
	a = e.readAttempt(claim.Attempt.ID)
	_ = e.expectFault(opAttemptCheckpoint, checkpointInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: e.ids.New(),
		Generation: a.Generation, ExpectedVersion: a.Version,
		Context: wireArtifactRef{ID: e.ids.New(), Digest: digestB},
		Outputs: []wireArtifactRef{{ID: e.ids.New(), Digest: digestB}},
	}, contract.CodeConflict)

	// The checkpoint context is what a replayed claim hands back.
	payload = e.mustOK(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: run.Version + 1, Capabilities: []string{"model.steps"},
	})
	var replay claimBody
	e.decode(payload.Data, &replay)
	if replay.Attempt.ID != claim.Attempt.ID || replay.Context.Digest != fixtureDigest {
		t.Fatalf("replayed claim context %+v, want the checkpoint digest", replay.Context)
	}
}

func TestAttemptReportEntersVerification(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)

	payload := e.mustOK(opAttemptReport, reportInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation, ExpectedVersion: a.Version,
		Outputs:      []wireArtifactRef{{ID: e.ids.New(), Digest: fixtureDigest}},
		Observations: []byte(`{"steps":3}`),
		Usage:        wireUsage{Currency: "USD", Spent: 250},
	})
	var body attemptBody
	e.decode(payload.Data, &body)
	if body.Resource.State != "reported" {
		t.Fatalf("reported state %q, want reported", body.Resource.State)
	}

	// Conclusive usage settled the reservation at report time.
	settles := e.ports.SettleCalls()
	if len(settles) != 1 || settles[0].ReservationID != a.ReservationID || settles[0].Usage.Spent != 250 {
		t.Fatalf("settlements %+v, want one settle of the reported usage", settles)
	}

	job := e.readVerificationJob(claim.Attempt.ID)
	if job == nil || job.State != "pending" {
		t.Fatalf("verification job %+v, want a pending job", job)
	}
	if job.AcceptanceDigest == "" || len(job.Request) == 0 {
		t.Fatalf("verification job lacks the pinned request and acceptance digest")
	}
	transitions := e.ports.Transitions()
	if len(transitions) == 0 || transitions[len(transitions)-1].State != "verifying" {
		t.Fatalf("task transitions %v, want a final verifying transition", transitions)
	}
	if got := e.readRun(run.ID); got.State != "verifying" {
		t.Fatalf("run state %q, want verifying", got.State)
	}
	kinds := map[string]bool{}
	for _, ev := range e.readEvents() {
		kinds[ev.Kind] = true
	}
	if !kinds[eventAttemptReported] || !kinds[eventVerificationRequested] || !kinds[eventRunVerifying] {
		t.Fatalf("report events missing: %v", kinds)
	}
}

func TestAttemptReportUnknownCostHoldsReservation(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)

	e.mustOK(opAttemptReport, reportInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation, ExpectedVersion: a.Version,
		Outputs:      []wireArtifactRef{{ID: e.ids.New(), Digest: fixtureDigest}},
		Observations: []byte(`{}`),
		Usage:        wireUsage{Currency: "USD", Unknown: 40},
	})
	if settles := e.ports.SettleCalls(); len(settles) != 0 {
		t.Fatalf("settlements %+v, want none while cost is unresolved", settles)
	}
	obligations := e.readObligations("attempt_id", claim.Attempt.ID)
	if len(obligations) != 1 || obligations[0].Code != "cost_unresolved" {
		t.Fatalf("obligations %+v, want one cost_unresolved", obligations)
	}
}

func TestAttemptReportTwiceRejected(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)
	e.mustOK(opAttemptReport, reportInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation, ExpectedVersion: a.Version,
		Outputs:      []wireArtifactRef{{ID: e.ids.New(), Digest: fixtureDigest}},
		Observations: []byte(`{}`),
		Usage:        wireUsage{Currency: "USD", Spent: 1},
	})
	// A reported attempt is no longer live: the identity fence refuses it.
	a = e.readAttempt(claim.Attempt.ID)
	f := e.expectFault(opAttemptReport, reportInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation, ExpectedVersion: a.Version,
		Outputs:      []wireArtifactRef{},
		Observations: []byte(`{}`),
		Usage:        wireUsage{Currency: "USD"},
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "cannot be revived") {
		t.Fatalf("fault message %q does not name the revival fence", f.Message)
	}
}

func TestAttemptCancelStopsAndPreservesOpenEffect(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.pinContext(claim.Attempt.ID, run.ConfigurationRevision)
	e.mustOK(opTick, tickInput{Now: e.clock.Now().Format(time.RFC3339), Limit: 10})
	a := e.readAttempt(claim.Attempt.ID)

	payload := e.mustOK(opAttemptCancel, cancelInput{
		Scope: e.scope, ID: claim.Attempt.ID, ExpectedVersion: a.Version, Reason: "operator stop",
	})
	var body dispositionBody
	e.decode(payload.Data, &body)
	if body.Resource.State != "stopped" {
		t.Fatalf("cancel disposition %+v", body.Resource)
	}
	stopped := e.readAttempt(claim.Attempt.ID)
	if stopped.State != "stopped" || stopped.RecoveryReason != "operator stop" {
		t.Fatalf("attempt state %q reason %q", stopped.State, stopped.RecoveryReason)
	}
	if lease := e.readLease(stopped.LeaseID); lease.State != "released" {
		t.Fatalf("lease state %q, want released", lease.State)
	}
	obligations := e.readObligations("attempt_id", claim.Attempt.ID)
	if len(obligations) != 1 || obligations[0].Code != "unknown_effect" {
		t.Fatalf("obligations %+v, want one unknown_effect for the dispatched effect", obligations)
	}
	if got := e.readRun(run.ID); got.State != "cancelled" {
		t.Fatalf("run state %q, want cancelled", got.State)
	}
}

func TestAttemptCancelWithoutOpenEffect(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)
	e.mustOK(opAttemptCancel, cancelInput{
		Scope: e.scope, ID: claim.Attempt.ID, ExpectedVersion: a.Version, Reason: "unused",
	})
	if obligations := e.readObligations("attempt_id", claim.Attempt.ID); len(obligations) != 0 {
		t.Fatalf("obligations %+v, want none without a dispatched effect", obligations)
	}
}

func TestAttemptCancelOnFencedConflicts(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.mustOK(opFence, fenceInput{Generation: claim.Attempt.Generation + 1, Reason: "rotation"})
	a := e.readAttempt(claim.Attempt.ID)
	f := e.expectFault(opAttemptCancel, cancelInput{
		Scope: e.scope, ID: claim.Attempt.ID, ExpectedVersion: a.Version, Reason: "again",
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "attempt is already fenced") {
		t.Fatalf("fault message %q does not name the fenced state", f.Message)
	}
}

func TestAttemptCancelTwiceRejected(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.mustOK(opAttemptCancel, cancelInput{
		Scope: e.scope, ID: claim.Attempt.ID, ExpectedVersion: claim.Attempt.Version, Reason: "first",
	})
	a := e.readAttempt(claim.Attempt.ID)
	f := e.expectFault(opAttemptCancel, cancelInput{
		Scope: e.scope, ID: claim.Attempt.ID, ExpectedVersion: a.Version, Reason: "again",
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "attempt is already stopped") {
		t.Fatalf("fault message %q does not name the stopped state", f.Message)
	}
}

func TestAttemptGetFences(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)

	_ = e.expectFault(opAttemptGet, getIDInput{Scope: e.scope, ID: e.ids.New()}, contract.CodeNotFound)
	scope := e.scope
	scope.OrganizationID = e.ids.New()
	_ = e.expectFault(opAttemptGet, getIDInput{Scope: scope, ID: claim.Attempt.ID},
		contract.CodePermissionDenied)

	payload := e.mustOK(opAttemptGet, getIDInput{Scope: e.scope, ID: claim.Attempt.ID})
	var body attemptBody
	e.decode(payload.Data, &body)
	if body.Resource.ID != claim.Attempt.ID || body.Resource.State != "claimed" {
		t.Fatalf("attempt get %+v", body.Resource)
	}
}

func TestAttemptRecoverySurfacesObligations(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	a := e.readAttempt(claim.Attempt.ID)
	e.mustOK(opAttemptReport, reportInput{
		Scope: e.scope, AttemptID: claim.Attempt.ID, LeaseID: a.LeaseID,
		Generation: a.Generation, ExpectedVersion: a.Version,
		Outputs:      []wireArtifactRef{{ID: e.ids.New(), Digest: fixtureDigest}},
		Observations: []byte(`{}`),
		Usage:        wireUsage{Currency: "USD", Unknown: 5},
	})

	payload := e.mustOK(opAttemptRecovery, getIDInput{Scope: e.scope, ID: claim.Attempt.ID})
	var body struct {
		Resource    wireAttempt       `json:"resource"`
		Obligations []wireRequirement `json:"obligations"`
	}
	e.decode(payload.Data, &body)
	if body.Resource.State != "reported" {
		t.Fatalf("recovery attempt state %q", body.Resource.State)
	}
	if len(body.Obligations) != 1 || body.Obligations[0].Code != "cost_unresolved" {
		t.Fatalf("obligations %+v, want the cost obligation", body.Obligations)
	}
}

func TestAttemptListFences(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)

	// An unsupported filter field is refused, not silently ignored.
	filter := &listFilter{Key: "x"}
	_ = e.expectFault(opAttemptList, listInput{Scope: e.scope, Filter: filter}, contract.CodeInvalidInput)

	// A filter organization outside the caller's scope is a caller violation.
	scope := e.scope
	scope.OrganizationID = e.org
	cross := &listFilter{Organization: e.ids.New()}
	_ = e.expectFault(opAttemptList, listInput{Scope: scope, Filter: cross}, contract.CodePermissionDenied)

	// A run.list cursor is bound to that operation; attempt.list refuses it.
	e.enqueueTask(e.ids.New(), nil)
	limit := int64(1)
	payload := e.mustOK(opRunList, listInput{Scope: e.scope, Limit: &limit})
	f := e.expectFault(opAttemptList, listInput{
		Scope: e.scope, Limit: &limit, Cursor: *payload.NextCursor,
	}, contract.CodeInvalidInput)
	if !strings.Contains(f.Message, "does not belong") {
		t.Fatalf("fault message %q does not name the cursor binding fence", f.Message)
	}

	// The state filter narrows by exact match.
	stateFilter := &listFilter{State: "claimed"}
	payload = e.mustOK(opAttemptList, listInput{Scope: e.scope, Filter: stateFilter})
	var body attemptListBody
	e.decode(payload.Data, &body)
	if len(body.Items) != 1 || body.Items[0].ID != claim.Attempt.ID {
		t.Fatalf("claimed filter items %+v, want the live attempt", body.Items)
	}
}
