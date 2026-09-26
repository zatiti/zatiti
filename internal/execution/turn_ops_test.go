package execution

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The durable worker turn pipeline: admission idempotency and
// safe-boundary injection, automatic hosted claims versus cooperative
// run.claim exclusivity, bounded/fair pending+claim, restart fencing that
// preserves cumulative limits, bounded chief conversation turns before any
// task exists, typed waiting reasons with wake revalidation, and the
// proposal/context/verification pipeline's own idempotency and conflict
// rules.

// TestTurnAdmitMessageToIdleChiefIsIdempotent is card P14's first required
// behavior: a message to an idle chief creates one durable turn; a
// duplicate creates none.
func TestTurnAdmitMessageToIdleChiefIsIdempotent(t *testing.T) {
	e := newEnv(t)
	chief := e.ids.New()
	e.installWorkerSnapshot(chief, fixtureHostedProfile(chief))
	messageID := e.ids.New()
	requester := e.ids.New()
	source := wireTurnSource{Kind: "message", SourceID: messageID, SourceVersion: 1}

	first := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source: source, WorkerID: chief, Scope: e.scope, RequesterID: requester,
	})
	var firstBody turnBody
	e.decode(first.Data, &firstBody)
	if firstBody.Resource.State != "pending" {
		t.Fatalf("new turn state %q, want pending", firstBody.Resource.State)
	}
	if firstBody.Resource.WorkerID != chief || firstBody.Resource.PrincipalID != chief {
		t.Fatalf("turn does not persist the accountable worker: %+v", firstBody.Resource)
	}
	if firstBody.Resource.RequesterID != requester {
		t.Fatalf("turn does not persist the accountable source actor: %+v", firstBody.Resource)
	}
	if firstBody.Resource.Source.Kind != "message" || firstBody.Resource.Source.SourceID != messageID {
		t.Fatalf("turn does not persist the trigger identity: %+v", firstBody.Resource.Source)
	}
	if firstBody.Resource.ConfigurationRevision == 0 {
		t.Fatalf("turn does not persist a configuration revision")
	}
	if firstBody.Resource.Limits.Concurrency == 0 && firstBody.Resource.Limits.ModelSteps == 0 {
		t.Fatalf("turn does not persist limits: %+v", firstBody.Resource.Limits)
	}

	// The duplicate: identical source identity, same worker.
	second := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source: source, WorkerID: chief, Scope: e.scope, RequesterID: requester,
	})
	var secondBody turnBody
	e.decode(second.Data, &secondBody)
	if secondBody.Resource.ID != firstBody.Resource.ID {
		t.Fatalf("duplicate admission minted turn %s, want the original %s", secondBody.Resource.ID, firstBody.Resource.ID)
	}
	if secondBody.Resource.Version != firstBody.Resource.Version {
		t.Fatalf("duplicate admission changed the turn version %d -> %d", firstBody.Resource.Version, secondBody.Resource.Version)
	}

	// Exactly one row: no second execution_turns row was created.
	if n := e.countLiveTurnsForWorker(chief); n != 1 {
		t.Fatalf("worker has %d live turns, want exactly 1", n)
	}

	// Both admissions linked the message to the turn through
	// _messaging.processed, idempotently — the seam P10 built expecting
	// execution to call through it.
	processed := e.ports.Processed()
	if len(processed) != 2 {
		t.Fatalf("_messaging.processed called %d times, want 2 (original + idempotent replay)", len(processed))
	}
	for _, p := range processed {
		if p.MessageID != messageID || p.RecipientID != chief || p.TurnID != firstBody.Resource.ID {
			t.Fatalf("_messaging.processed call %+v does not name the admitted turn", p)
		}
	}
}

// TestTurnAdmitSafeBoundaryInjection: a message arriving during an active
// turn is linked to that turn as a safe-boundary injection rather than
// dropped or admitted as a second concurrent turn.
func TestTurnAdmitSafeBoundaryInjection(t *testing.T) {
	e := newEnv(t)
	chief := e.ids.New()
	e.installWorkerSnapshot(chief, fixtureHostedProfile(chief))
	requester := e.ids.New()

	first := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source:   wireTurnSource{Kind: "message", SourceID: e.ids.New(), SourceVersion: 1},
		WorkerID: chief, Scope: e.scope, RequesterID: requester,
	})
	var firstBody turnBody
	e.decode(first.Data, &firstBody)

	// A second, distinct message for the same still-active worker turn.
	injected := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source:   wireTurnSource{Kind: "message", SourceID: e.ids.New(), SourceVersion: 1},
		WorkerID: chief, Scope: e.scope, RequesterID: requester,
	})
	var injectedBody turnBody
	e.decode(injected.Data, &injectedBody)

	if injectedBody.Resource.ID != firstBody.Resource.ID {
		t.Fatalf("injection minted a second turn %s, want linkage into the active turn %s",
			injectedBody.Resource.ID, firstBody.Resource.ID)
	}
	if n := e.countLiveTurnsForWorker(chief); n != 1 {
		t.Fatalf("worker has %d live turns after injection, want exactly 1", n)
	}
}

// TestTurnAdmitPausedWorkerRefusesNewButNotReplay: pause blocks a brand new
// admission without inference or spending, but never blocks an exact
// replay of an already-committed admission.
func TestTurnAdmitPausedWorkerRefusesNewButNotReplay(t *testing.T) {
	e := newEnv(t)
	chief := e.ids.New()
	e.installWorkerSnapshot(chief, fixtureHostedProfile(chief))
	source := wireTurnSource{Kind: "message", SourceID: e.ids.New(), SourceVersion: 1}
	in := turnAdmitInput{Source: source, WorkerID: chief, Scope: e.scope, RequesterID: e.ids.New()}

	// Pause before any turn exists at all: a brand new admission is
	// refused without inference or spending.
	e.mustOK(opWorkerPause, workerGateInput{Scope: e.scope, ID: chief, ExpectedVersion: 1})
	f := e.expectFault(opTurnAdmit, in, contract.CodeConflict)
	if !strings.Contains(f.Message, "paused") {
		t.Fatalf("fault %q does not name the pause gate", f.Message)
	}

	// Resume and admit for real.
	e.mustOK(opWorkerResume, workerGateInput{Scope: e.scope, ID: chief, ExpectedVersion: 2})
	first := e.mustOK(opTurnAdmit, in)
	var firstBody turnBody
	e.decode(first.Data, &firstBody)

	// Pause again: the exact same source identity still replays cleanly —
	// a replay is never refused merely because the worker was paused
	// afterward.
	e.mustOK(opWorkerPause, workerGateInput{Scope: e.scope, ID: chief, ExpectedVersion: 3})
	replay := e.mustOK(opTurnAdmit, in)
	var replayBody turnBody
	e.decode(replay.Data, &replayBody)
	if replayBody.Resource.ID != firstBody.Resource.ID {
		t.Fatalf("replay under pause minted a new turn")
	}
}

// TestTurnAdmitBoundedChiefConversationBeforeTask: card P14/4 — the chief
// can hold a bounded conversation turn to clarify or propose configuration
// before any task exists; admission never requires a task.
func TestTurnAdmitBoundedChiefConversationBeforeTask(t *testing.T) {
	e := newEnv(t)
	chief := e.ids.New()
	limit := int64(1)
	limits := fixtureLimits(limit)
	e.ports.setSnapshot(e.install, &peerScopeSnapshot{
		Scope: e.scope, Revision: 1,
		Worker: &wireWorker{
			ID: chief, Version: 1, OrganizationID: e.org, Key: "chief", Name: "Chief",
			SkillVersions: []wireRef{}, Bindings: []contract.ID{},
			Profile: fixtureHostedProfile(chief), Limits: &limits,
		},
	})

	payload := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source:   wireTurnSource{Kind: "message", SourceID: e.ids.New(), SourceVersion: 1},
		WorkerID: chief, Scope: e.scope, RequesterID: e.ids.New(),
	})
	var body turnBody
	e.decode(payload.Data, &body)
	if body.Resource.TaskID != "" || body.Resource.RunID != "" {
		t.Fatalf("chat turn carries a task/run binding %+v, want none before any task exists", body.Resource)
	}
	if body.Resource.Limits.ModelSteps != limit {
		t.Fatalf("chat turn model-step limit %d, want the pinned worker bound %d", body.Resource.Limits.ModelSteps, limit)
	}

	// The bound is real: proposal.record refuses to advance the turn past
	// its own model-step limit — a narrow, in-package proof that "bounded"
	// is enforced, not merely documented. Drive the turn to claimed so a
	// proposal can be prepared/recorded against it.
	e.mustOK(opWorkClaim, workClaimInput{WorkID: body.Resource.ID, ExpectedVersion: body.Resource.Version, Generation: e.generation()})

	p := e.seedProposal(body.Resource.ID, 0, "call-1")
	turn := e.readTurn(body.Resource.ID)
	rec := e.mustOK(opProposalRecord, proposalRecordInput{ProposalID: p.ProposalID, ExpectedVersion: turn.Version})
	var recBody turnBody
	e.decode(rec.Data, &recBody)
	if recBody.Resource.StepsUsed < limit {
		t.Fatalf("steps used %d did not advance to the bound %d", recBody.Resource.StepsUsed, limit)
	}
	if recBody.Resource.State != "waiting" || recBody.Resource.WaitingReason != waitingBudget {
		t.Fatalf("turn at its step bound is %s/%s, want waiting/budget", recBody.Resource.State, recBody.Resource.WaitingReason)
	}
}

// TestWorkClaimAutoClaimsHostedRunWithoutRunClaim is card P14's second
// required behavior: a queued hosted run gets an attempt without a human
// calling run.claim.
func TestWorkClaimAutoClaimsHostedRunWithoutRunClaim(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)

	turn := e.findTurnForSource("task", run.TaskID, 1, worker)
	if turn == nil {
		t.Fatalf("enqueue of a hosted-executor task admitted no WorkerTurn")
	}
	if turn.State != "pending" {
		t.Fatalf("task turn state %q, want pending", turn.State)
	}

	pending := e.mustOK(opWorkPending, workPendingInput{Limit: 10})
	var pendingBody workPendingBody
	e.decode(pending.Data, &pendingBody)
	var found bool
	for _, item := range pendingBody.Items {
		if item.Turn.ID == turn.ID {
			found = true
			if item.Kind != "claim" {
				t.Fatalf("work item kind %q, want claim", item.Kind)
			}
			if item.RunID != run.ID {
				t.Fatalf("work item run_id %s, want %s", item.RunID, run.ID)
			}
		}
	}
	if !found {
		t.Fatalf("work.pending did not surface the queued hosted run's turn")
	}

	claim := e.mustOK(opWorkClaim, workClaimInput{WorkID: turn.ID, ExpectedVersion: turn.Version, Generation: e.generation()})
	var claimBody2 workClaimBody
	e.decode(claim.Data, &claimBody2)
	if claimBody2.Item.Turn.AttemptID == "" {
		t.Fatalf("work.claim did not bind an attempt to the hosted turn")
	}

	updatedRun := e.readRun(run.ID)
	if updatedRun.State != "running" {
		t.Fatalf("run state %q after automatic claim, want running", updatedRun.State)
	}
	attempt := e.readAttempt(claimBody2.Item.Turn.AttemptID)
	if attempt.Executor != "hosted" {
		t.Fatalf("automatically claimed attempt executor %q, want hosted", attempt.Executor)
	}
	if attempt.WorkerID != worker {
		t.Fatalf("automatically claimed attempt worker %s, want %s", attempt.WorkerID, worker)
	}
}

// TestEnqueueNeverAdmitsTurnForCooperativeExecutor is card P14's exclusion
// half of the second required behavior: a cooperative run is never
// auto-claimed as hosted — it gets no WorkerTurn at all and stays reachable
// only through the public run.claim path, exactly as before this card.
func TestEnqueueNeverAdmitsTurnForCooperativeExecutor(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureCooperativeProfile(worker))
	run := e.enqueueTask(worker, nil)

	if turn := e.findTurnForSource("task", run.TaskID, 1, worker); turn != nil {
		t.Fatalf("a cooperative-executor task admitted a WorkerTurn %s; it must never be auto-claimable", turn.ID)
	}
	if got := e.readRun(run.ID); got.State != "ready" {
		t.Fatalf("cooperative run state %q before claim, want ready", got.State)
	}

	// The external worker's own explicit run.claim still works unchanged.
	claim := e.claimRun(run.ID, worker)
	if claim.Attempt.Executor != "cooperative" {
		t.Fatalf("claimed attempt executor %q, want cooperative", claim.Attempt.Executor)
	}
	if got := e.readRun(run.ID); got.State != "running" {
		t.Fatalf("cooperative run state %q after explicit claim, want running", got.State)
	}
}

// TestFenceRestartFencesStaleTurnClaimsPreservingLimits is card P14's third
// required behavior: a controller restart fences stale claims, respects
// pause/cancel and does not reset a root's cumulative limits.
func TestFenceRestartFencesStaleTurnClaimsPreservingLimits(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	var task wireTask
	run := e.enqueueTask(worker, func(tk *wireTask) { task = *tk })
	turn := e.findTurnForSource("task", run.TaskID, 1, worker)

	e.mustOK(opWorkClaim, workClaimInput{WorkID: turn.ID, ExpectedVersion: turn.Version, Generation: e.generation()})
	claimed := e.readTurn(turn.ID)
	if claimed.State != "claimed" {
		t.Fatalf("turn state %q after claim, want claimed", claimed.State)
	}
	if claimed.Generation != e.generation() {
		t.Fatalf("claimed turn generation %d, want the current generation %d", claimed.Generation, e.generation())
	}

	// While still actively claimed, a second claim attempt naming a
	// mismatched generation is refused — the live binding is fenced, not
	// blindly overwritten by whatever generation a caller asserts.
	_ = e.expectFault(opWorkClaim, workClaimInput{
		WorkID: turn.ID, ExpectedVersion: claimed.Version, Generation: claimed.Generation + 1,
	}, contract.CodeConflict)

	// Advance the turn's cumulative spend so the fence's effect on it is
	// observable: record one proposal.
	p := e.seedProposal(turn.ID, 0, "call-1")
	rec := e.mustOK(opProposalRecord, proposalRecordInput{ProposalID: p.ProposalID, ExpectedVersion: claimed.Version})
	var recBody turnBody
	e.decode(rec.Data, &recBody)
	stepsBeforeFence := recBody.Resource.StepsUsed
	limitsBeforeFence := recBody.Resource.Limits
	if stepsBeforeFence == 0 {
		t.Fatalf("recording a proposal did not advance the turn's cumulative step count")
	}

	// Simulate a controller restart: the persisted generation advances and
	// the new controller fences every stale claim below it.
	newGeneration := e.advanceGeneration()
	e.mustOK(opFence, fenceInput{Generation: newGeneration, Reason: "controller restart"})

	fenced := e.readTurn(turn.ID)
	if fenced.State != "waiting" || fenced.WaitingReason != waitingRecovery {
		t.Fatalf("turn after fence is %s/%s, want waiting/recovery", fenced.State, fenced.WaitingReason)
	}
	if fenced.LeaseID != "" {
		t.Fatalf("fenced turn retains a lease %s, want it cleared", fenced.LeaseID)
	}
	if fenced.StepsUsed != stepsBeforeFence {
		t.Fatalf("fence reset cumulative steps %d -> %d, want unchanged", stepsBeforeFence, fenced.StepsUsed)
	}
	if fenced.Limits != limitsBeforeFence {
		t.Fatalf("fence altered the turn's limits envelope: %+v -> %+v", limitsBeforeFence, fenced.Limits)
	}

	// Respects cancel: the task was cancelled while the turn sat fenced —
	// wake revalidation must not resume it.
	task.CancellationRequested = true
	e.ports.setTask(&task)
	f := e.expectFault(opWorkClaim, workClaimInput{
		WorkID: fenced.ID, ExpectedVersion: fenced.Version, Generation: newGeneration,
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "blocked") {
		t.Fatalf("fault %q does not describe the still-blocked revalidation", f.Message)
	}
	if got := e.readTurn(fenced.ID); got.State != "waiting" {
		t.Fatalf("cancelled task's turn resumed despite cancellation: %s", got.State)
	}

	// Respects pause too, independently: clear the cancellation but pause
	// the worker, and resume is still refused.
	task.CancellationRequested = false
	e.ports.setTask(&task)
	e.mustOK(opWorkerPause, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 1})
	current := e.readTurn(fenced.ID)
	_ = e.expectFault(opWorkClaim, workClaimInput{
		WorkID: current.ID, ExpectedVersion: current.Version, Generation: newGeneration,
	}, contract.CodeConflict)
	if got := e.readTurn(current.ID); got.State != "waiting" {
		t.Fatalf("paused worker's turn resumed despite pause: %s", got.State)
	}

	// Once neither condition blocks it, the fenced claim resumes cleanly
	// under the new generation, and cumulative steps/limits are exactly
	// what they were before the fence — a restart never resets a root's
	// cumulative spend.
	e.mustOK(opWorkerResume, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 2})
	final := e.readTurn(current.ID)
	resumed := e.mustOK(opWorkClaim, workClaimInput{WorkID: final.ID, ExpectedVersion: final.Version, Generation: newGeneration})
	var resumedBody workClaimBody
	e.decode(resumed.Data, &resumedBody)
	if resumedBody.Item.Turn.State != "claimed" {
		t.Fatalf("resumed turn state %q, want claimed", resumedBody.Item.Turn.State)
	}
	if resumedBody.Item.Turn.StepsUsed != stepsBeforeFence {
		t.Fatalf("resume altered cumulative steps: %d -> %d", stepsBeforeFence, resumedBody.Item.Turn.StepsUsed)
	}
	if resumedBody.Item.Turn.Limits != limitsBeforeFence {
		t.Fatalf("resume altered the turn's limits envelope")
	}
}

// TestWorkClaimResumeRevalidatesCurrentState: card P14/5 — a waiting turn's
// wake revalidates current state rather than blindly resuming, and keeps
// the same typed reason while still blocked.
func TestWorkClaimResumeRevalidatesCurrentState(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	payload := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source:   wireTurnSource{Kind: "message", SourceID: e.ids.New(), SourceVersion: 1},
		WorkerID: worker, Scope: e.scope, RequesterID: e.ids.New(),
	})
	var body turnBody
	e.decode(payload.Data, &body)
	e.mustOK(opWorkClaim, workClaimInput{WorkID: body.Resource.ID, ExpectedVersion: body.Resource.Version, Generation: e.generation()})

	// Park it waiting on a typed reason with a wake already due.
	var due contract.ID
	e.inWrite(func(unit contract.Unit) error {
		turn, err := loadTurn(e.ctx, unit, body.Resource.ID)
		if err != nil {
			return err
		}
		turn.State = "waiting"
		turn.WaitingReason = waitingClarification
		turn.NextWake = e.clock.Now().Add(-time.Second)
		due = turn.ID
		return updateTurn(e.ctx, unit, turn)
	})
	parked := e.readTurn(due)

	// Pause the worker: revalidation must find it still blocked and keep
	// the turn waiting rather than resuming it.
	e.mustOK(opWorkerPause, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 1})
	f := e.expectFault(opWorkClaim, workClaimInput{
		WorkID: parked.ID, ExpectedVersion: parked.Version, Generation: e.generation(),
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "blocked") {
		t.Fatalf("fault %q does not describe the still-blocked revalidation", f.Message)
	}
	stillWaiting := e.readTurn(parked.ID)
	if stillWaiting.State != "waiting" || stillWaiting.WaitingReason != waitingClarification {
		t.Fatalf("revalidation changed state to %s/%s, want waiting/clarification preserved",
			stillWaiting.State, stillWaiting.WaitingReason)
	}

	// Resume the worker and the next due wake actually revalidates clean
	// and resumes the turn.
	e.mustOK(opWorkerResume, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 2})
	e.inWrite(func(unit contract.Unit) error {
		turn, err := loadTurn(e.ctx, unit, parked.ID)
		if err != nil {
			return err
		}
		turn.NextWake = e.clock.Now().Add(-time.Second)
		return updateTurn(e.ctx, unit, turn)
	})
	resumable := e.readTurn(parked.ID)
	resumed := e.mustOK(opWorkClaim, workClaimInput{
		WorkID: resumable.ID, ExpectedVersion: resumable.Version, Generation: e.generation(),
	})
	var resumedBody workClaimBody
	e.decode(resumed.Data, &resumedBody)
	if resumedBody.Item.Kind != "resume" {
		t.Fatalf("claim kind %q, want resume", resumedBody.Item.Kind)
	}
	if resumedBody.Item.Turn.State != "claimed" {
		t.Fatalf("resumed turn state %q, want claimed", resumedBody.Item.Turn.State)
	}
	if resumedBody.Item.Turn.WaitingReason != "" {
		t.Fatalf("resumed turn still carries waiting reason %q", resumedBody.Item.Turn.WaitingReason)
	}
}

// TestProposalRecordDuplicateReplaysDifferingResultConflicts proves the
// proposal pipeline's own submission identity: an identical repeat replays,
// a differing repeat is refused submission_conflict without altering the
// original record.
func TestProposalRecordDuplicateReplaysDifferingResultConflicts(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	payload := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source:   wireTurnSource{Kind: "message", SourceID: e.ids.New(), SourceVersion: 1},
		WorkerID: worker, Scope: e.scope, RequesterID: e.ids.New(),
	})
	var body turnBody
	e.decode(payload.Data, &body)
	e.mustOK(opWorkClaim, workClaimInput{WorkID: body.Resource.ID, ExpectedVersion: body.Resource.Version, Generation: e.generation()})

	p := e.seedProposal(body.Resource.ID, 1, "call-1")
	turn := e.readTurn(body.Resource.ID)
	commandID := e.ids.New()
	first := e.mustOK(opProposalRecord, proposalRecordInput{
		ProposalID: p.ProposalID, ExpectedVersion: turn.Version, CommandID: commandID,
	})
	var firstBody turnBody
	e.decode(first.Data, &firstBody)

	// Identical repeat: replays the same recorded disposition.
	replay := e.mustOK(opProposalRecord, proposalRecordInput{
		ProposalID: p.ProposalID, ExpectedVersion: turn.Version, CommandID: commandID,
	})
	var replayBody turnBody
	e.decode(replay.Data, &replayBody)
	if replayBody.Resource.StepsUsed != firstBody.Resource.StepsUsed {
		t.Fatalf("identical repeat double-counted steps: %d -> %d", firstBody.Resource.StepsUsed, replayBody.Resource.StepsUsed)
	}

	// Differing repeat: refused, original unchanged.
	_ = e.expectFault(opProposalRecord, proposalRecordInput{
		ProposalID: p.ProposalID, ExpectedVersion: turn.Version, CommandID: e.ids.New(),
	}, contract.CodeSubmissionConflict)
	unchanged := e.readTurn(body.Resource.ID)
	if unchanged.StepsUsed != firstBody.Resource.StepsUsed {
		t.Fatalf("refused differing repeat still mutated steps: %d -> %d", firstBody.Resource.StepsUsed, unchanged.StepsUsed)
	}
}

// TestContextPrepareCommitStaleGenerationDiscardsPlan proves P00-002's
// context-plan staleness rule: a plan whose generation no longer matches
// the current controller generation is discarded and refused, never
// committed and never spent/sent.
func TestContextPrepareCommitStaleGenerationDiscardsPlan(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	payload := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source:   wireTurnSource{Kind: "continuation", SourceID: e.ids.New(), SourceVersion: 1},
		WorkerID: worker, Scope: e.scope, RequesterID: e.ids.New(),
	})
	var body turnBody
	e.decode(payload.Data, &body)
	e.mustOK(opWorkClaim, workClaimInput{WorkID: body.Resource.ID, ExpectedVersion: body.Resource.Version, Generation: e.generation()})
	turn := e.readTurn(body.Resource.ID)

	prep := e.mustOK(opContextPrepare, contextPrepareInput{
		TurnID: turn.ID, ExpectedVersion: turn.Version, Generation: turn.Generation,
	})
	var plan contextPlanBody
	e.decode(prep.Data, &plan)

	// The controller restarts before commit: generation advances.
	newGeneration := e.advanceGeneration()

	locator := wireArtifactLocator{Kind: "artifact", Artifact: &wireArtifactRef{ID: e.ids.New(), Digest: fixtureDigest}}
	f := e.expectFault(opContextCommit, contextCommitInput{
		PlanID: plan.Resource.ID, ExpectedVersion: plan.Resource.ExpectedVersion,
		Generation: plan.Resource.Generation, StagedContext: locator,
	}, contract.CodeStaleVersion)
	if !strings.Contains(f.Message, "changed") && !strings.Contains(f.Message, "does not match") {
		t.Fatalf("fault %q does not describe a discarded stale plan", f.Message)
	}

	// The real recovery path: the new controller fences the stale claim,
	// then resumes it under the new generation, and only then does a
	// rebuilt prepare/commit succeed — never spending or sending against
	// the discarded plan.
	e.mustOK(opFence, fenceInput{Generation: newGeneration, Reason: "controller restart"})
	fenced := e.readTurn(turn.ID)
	if fenced.State != "waiting" {
		t.Fatalf("turn state %q after fence, want waiting", fenced.State)
	}
	resumed := e.mustOK(opWorkClaim, workClaimInput{WorkID: fenced.ID, ExpectedVersion: fenced.Version, Generation: newGeneration})
	var resumedBody workClaimBody
	e.decode(resumed.Data, &resumedBody)
	if resumedBody.Item.Turn.State != "claimed" {
		t.Fatalf("resumed turn state %q, want claimed", resumedBody.Item.Turn.State)
	}

	fresh := e.mustOK(opContextPrepare, contextPrepareInput{
		TurnID: turn.ID, ExpectedVersion: resumedBody.Item.Turn.Version, Generation: newGeneration,
	})
	var freshPlan contextPlanBody
	e.decode(fresh.Data, &freshPlan)
	if freshPlan.Resource.Generation != newGeneration {
		t.Fatalf("rebuilt plan generation %d, want the current generation %d", freshPlan.Resource.Generation, newGeneration)
	}
	commit := e.mustOK(opContextCommit, contextCommitInput{
		PlanID: freshPlan.Resource.ID, ExpectedVersion: freshPlan.Resource.ExpectedVersion,
		Generation: freshPlan.Resource.Generation, StagedContext: locator,
	})
	var committed turnBody
	e.decode(commit.Data, &committed)
	if committed.Resource.State != "model_pending" {
		t.Fatalf("committed turn state %q, want model_pending", committed.Resource.State)
	}
}

// TestContextCommitRefusesStagedLocator proves the staged-locator-is-an-
// obligation rule: an unpublished staged context can never be committed.
func TestContextCommitRefusesStagedLocator(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	payload := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source:   wireTurnSource{Kind: "continuation", SourceID: e.ids.New(), SourceVersion: 1},
		WorkerID: worker, Scope: e.scope, RequesterID: e.ids.New(),
	})
	var body turnBody
	e.decode(payload.Data, &body)
	e.mustOK(opWorkClaim, workClaimInput{WorkID: body.Resource.ID, ExpectedVersion: body.Resource.Version, Generation: e.generation()})
	turn := e.readTurn(body.Resource.ID)
	prep := e.mustOK(opContextPrepare, contextPrepareInput{TurnID: turn.ID, ExpectedVersion: turn.Version, Generation: turn.Generation})
	var plan contextPlanBody
	e.decode(prep.Data, &plan)

	staged := wireArtifactLocator{Kind: "staged", StagingRef: "stage-1", Digest: fixtureDigest}
	_ = e.expectFault(opContextCommit, contextCommitInput{
		PlanID: plan.Resource.ID, ExpectedVersion: plan.Resource.ExpectedVersion,
		Generation: plan.Resource.Generation, StagedContext: staged,
	}, contract.CodeArtifactFault)
}

// TestProposalPrepareWithoutEvidenceIsPrerequisiteMissing proves prepare
// never fabricates a placeholder proposal when no normalized model evidence
// is persisted.
func TestProposalPrepareWithoutEvidenceIsPrerequisiteMissing(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	payload := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source:   wireTurnSource{Kind: "message", SourceID: e.ids.New(), SourceVersion: 1},
		WorkerID: worker, Scope: e.scope, RequesterID: e.ids.New(),
	})
	var body turnBody
	e.decode(payload.Data, &body)

	_ = e.expectFault(opProposalPrepare, proposalPrepareInput{
		TurnID: body.Resource.ID, StepIndex: 0, ProposalID: "call-1", ExpectedVersion: body.Resource.Version,
	}, contract.CodePrerequisiteMissing)
}

// TestVerificationPendingAndClaimAreGenerationBound proves the sealed
// verification-request scan/claim boundary: claim is exact version+
// generation bound and a lost acknowledgement resolves through the same
// token rather than assuming unclaimed.
func TestVerificationPendingAndClaimAreGenerationBound(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.pinContext(claim.Attempt.ID, 1)
	e.reportAccepted(claim, wireUsage{Currency: "USD"})
	job := e.readVerificationJob(claim.Attempt.ID)

	pending := e.mustOK(opVerificationPend, verificationPendingInput{Limit: 10})
	var pendingBody verificationPendingBody
	e.decode(pending.Data, &pendingBody)
	var seen bool
	for _, req := range pendingBody.Items {
		if req.JobID == job.ID {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("verification.pending did not surface job %s", job.ID)
	}

	gen := e.generation()
	first := e.mustOK(opVerificationClaim, verificationClaimInput{RequestID: job.ID, ExpectedVersion: 1, Generation: gen})
	var firstClaim verificationClaimBody
	e.decode(first.Data, &firstClaim)

	// Lost acknowledgement: the same generation re-claims and recovers the
	// same token.
	replay := e.mustOK(opVerificationClaim, verificationClaimInput{RequestID: job.ID, ExpectedVersion: 1, Generation: gen})
	var replayClaim verificationClaimBody
	e.decode(replay.Data, &replayClaim)
	if replayClaim.ClaimToken != firstClaim.ClaimToken {
		t.Fatalf("lost-ack replay minted a new claim token %s, want %s", replayClaim.ClaimToken, firstClaim.ClaimToken)
	}

	// A different generation cannot steal the claim.
	_ = e.expectFault(opVerificationClaim, verificationClaimInput{
		RequestID: job.ID, ExpectedVersion: 1, Generation: gen + 1,
	}, contract.CodeConflict)
}

// TestVerificationClaimReturnsCurrentAttemptVersionAndFencesRecord
// reproduces R-verification-stall-fix directly against real version
// fencing: an attempt's version advances by exactly 1 on every claim,
// checkpoint (pinContext) and report, so it is never 1 by the time
// verification runs for any attempt that did more than a bare claim --
// every realistic cooperative journey. internal/controller/turns.go's
// runVerification used to hardcode ExpectedVersion: 1 on the
// _execution.verification.record call that follows a claim, which
// therefore refused with stale_version every single time, forever (the
// verification job's own state column never left 'pending', so the
// controller's next tick re-claimed and re-invoked the trusted verifier
// again, indefinitely).
//
// This test first proves the fault text a hardcoded 1 actually produces
// (the exact regression, independent of any controller wiring), then
// proves _execution.verification.claim's widened output carries the real
// live version the caller needs to avoid it, and that using that real
// value lets record genuinely succeed.
func TestVerificationClaimReturnsCurrentAttemptVersionAndFencesRecord(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	claim := e.claimRun(run.ID, worker)
	e.pinContext(claim.Attempt.ID, 1)
	e.reportAccepted(claim, wireUsage{Currency: "USD"})
	job := e.readVerificationJob(claim.Attempt.ID)

	realVersion := e.readAttempt(claim.Attempt.ID).Version
	if realVersion == 1 {
		t.Fatalf("attempt version is 1 after claim+checkpoint+report, want >1 (checkpoint/report each advance it) -- this test would not exercise the bug")
	}

	gen := e.generation()
	claimed := e.mustOK(opVerificationClaim, verificationClaimInput{RequestID: job.ID, ExpectedVersion: 1, Generation: gen})
	var claimedBody verificationClaimBody
	e.decode(claimed.Data, &claimedBody)
	if claimedBody.AttemptVersion != realVersion {
		t.Fatalf("verification.claim attempt_version = %d, want the attempt's real current version %d", claimedBody.AttemptVersion, realVersion)
	}

	// RED: this is the exact bug -- a hardcoded ExpectedVersion: 1, as
	// internal/controller/turns.go's runVerification used to send, is
	// refused with stale_version naming the real current version.
	f := e.expectFault(opVerificationRec, verificationRecordInput{
		AttemptID: claim.Attempt.ID, ExpectedVersion: 1,
		Result: resultFor(e, job, "passed", passedChecks()),
	}, contract.CodeStaleVersion)
	if !strings.Contains(f.Message, "does not match expected version 1") {
		t.Fatalf("stale_version fault message %q does not contain %q", f.Message, "does not match expected version 1")
	}
	if !strings.Contains(f.Message, fmt.Sprintf("version %d", realVersion)) {
		t.Fatalf("stale_version fault message %q does not name the real version %d", f.Message, realVersion)
	}

	// GREEN: the real value verification.claim returned -- exactly what
	// runVerification now threads through instead of 1 -- lets record
	// genuinely succeed.
	e.mustOK(opVerificationRec, verificationRecordInput{
		AttemptID: claim.Attempt.ID, ExpectedVersion: claimedBody.AttemptVersion,
		Result: resultFor(e, job, "passed", passedChecks()),
	})
}
