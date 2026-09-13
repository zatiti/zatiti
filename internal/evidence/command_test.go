package evidence

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Command replay, conflict, rollback and principal-isolation behavior — the
// local proving focus named in AGENTS.md: duplicate command concurrency,
// hash conflict, lost ack, replay, rollback+durable refusal, principal
// isolation and state-event atomicity.

func TestCommandBeginFreshReservationHasNoExisting(t *testing.T) {
	env := newEnv(t)
	principal := env.ids.New()
	digest := digestOf(t, map[string]string{"a": "1"})
	out := env.beginCommand(principal, "widget.create", 1, "key-1", digest)
	if out.CommandID == "" {
		t.Fatal("begin returned no command id")
	}
	if out.Existing != nil {
		t.Fatalf("fresh begin returned existing %+v, want none", out.Existing)
	}
}

// Z04.submission_replay: an identical retry (same principal, operation
// version, submission key and canonical input) returns the original durable
// disposition without another revision or event; changed input is refused.
func TestCommandReplayReturnsOriginalDispositionExactly(t *testing.T) {
	env := newEnv(t)
	principal := env.ids.New()
	digest := digestOf(t, map[string]string{"a": "1"})

	begin := env.beginCommand(principal, "widget.create", 1, "key-1", digest)
	data := json.RawMessage(`{"widget_id":"w-1"}`)
	finished := env.finishCommandOp(begin.CommandID, completeResult(begin.CommandID, data))
	if finished.Resource.Status != contract.StatusCompleted {
		t.Fatalf("finish status %q, want completed", finished.Resource.Status)
	}

	before, err := env.db.Events(env.ctx, 0, 500)
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	replay := env.beginCommand(principal, "widget.create", 1, "key-1", digest)
	if replay.CommandID != begin.CommandID {
		t.Fatalf("replay command id %s, want %s", replay.CommandID, begin.CommandID)
	}
	if replay.Existing == nil {
		t.Fatal("replay carried no existing disposition")
	}
	if replay.Existing.Status != contract.StatusCompleted {
		t.Fatalf("replay status %q, want completed", replay.Existing.Status)
	}
	if string(replay.Existing.Data) != string(data) {
		t.Fatalf("replay data %s, want %s", replay.Existing.Data, data)
	}
	if replay.Existing.Result.CommandID != begin.CommandID {
		t.Fatalf("replay result command id %s, want %s", replay.Existing.Result.CommandID, begin.CommandID)
	}

	after, err := env.db.Events(env.ctx, 0, 500)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("replay emitted %d new events, want 0", len(after)-len(before))
	}

	// Changed input under the same key conflicts instead of replaying.
	otherDigest := digestOf(t, map[string]string{"a": "2"})
	_ = env.expectFault(opCommandBegin, commandBeginInput{
		PrincipalID: principal, Operation: "widget.create", OperationVersion: 1,
		SubmissionKey: "key-1", RequestDigest: otherDigest,
	}, contract.CodeSubmissionConflict)
}

func TestCommandFinishRejectsCommandIDMismatch(t *testing.T) {
	env := newEnv(t)
	principal := env.ids.New()
	begin := env.beginCommand(principal, "widget.create", 1, "key-1", digestOf(t, 1))
	other := env.ids.New()
	_ = env.expectFault(opCommandFinish, commandFinishInput{
		CommandID: begin.CommandID,
		Result:    completeResult(other, nil),
	}, contract.CodeInvalidInput)
}

func TestCommandFinishRejectsSchemaMismatch(t *testing.T) {
	env := newEnv(t)
	principal := env.ids.New()
	begin := env.beginCommand(principal, "widget.create", 1, "key-1", digestOf(t, 1))
	bad := completeResult(begin.CommandID, nil)
	bad.Schema = "not-a-schema/v1"
	_ = env.expectFault(opCommandFinish, commandFinishInput{CommandID: begin.CommandID, Result: bad}, contract.CodeInvalidInput)
}

func TestCommandFinishRejectsStatusErrorDisagreement(t *testing.T) {
	env := newEnv(t)
	principal := env.ids.New()

	begin1 := env.beginCommand(principal, "widget.create", 1, "key-completed-with-error", digestOf(t, 1))
	withError := completeResult(begin1.CommandID, nil)
	withError.Error = &contract.Fault{Code: contract.CodeInvalidInput, Message: "should not be set"}
	_ = env.expectFault(opCommandFinish, commandFinishInput{CommandID: begin1.CommandID, Result: withError}, contract.CodeInvalidInput)

	begin2 := env.beginCommand(principal, "widget.create", 1, "key-failed-without-error", digestOf(t, 2))
	noError := failedResult(begin2.CommandID, "", "")
	noError.Error = nil
	_ = env.expectFault(opCommandFinish, commandFinishInput{CommandID: begin2.CommandID, Result: noError}, contract.CodeInvalidInput)
}

func TestCommandFinishRejectsUnknownCommand(t *testing.T) {
	env := newEnv(t)
	unknown := env.ids.New()
	_ = env.expectFault(opCommandFinish, commandFinishInput{
		CommandID: unknown, Result: completeResult(unknown, nil),
	}, contract.CodeNotFound)
}

func TestCommandFinishTwiceIsRejected(t *testing.T) {
	env := newEnv(t)
	principal := env.ids.New()
	begin := env.beginCommand(principal, "widget.create", 1, "key-1", digestOf(t, 1))
	_ = env.finishCommandOp(begin.CommandID, completeResult(begin.CommandID, nil))
	_ = env.expectFault(opCommandFinish, commandFinishInput{
		CommandID: begin.CommandID, Result: completeResult(begin.CommandID, nil),
	}, contract.CodeInternalError)
}

// Rollback + durable refusal: the whole transaction (including the
// reservation) rolls back on handler failure. A later attempt reserves
// again from a clean slate — evidence itself makes no attempt to remember
// a rolled-back reservation, which is what lets a fresh finish() persist an
// entirely independent outcome (application's own recordRefusal path relies
// on exactly this).
func TestCommandBeginRollbackDiscardsReservation(t *testing.T) {
	env := newEnv(t)
	principal := env.ids.New()
	digest := digestOf(t, map[string]string{"a": "1"})
	raw, _ := json.Marshal(commandBeginInput{
		PrincipalID: principal, Operation: "widget.create", OperationVersion: 1,
		SubmissionKey: "key-1", RequestDigest: digest,
	})

	var firstID contract.ID
	simulatedFailure := &contract.Fault{Code: contract.CodeConflict, Message: "simulated handler failure"}
	err := env.db.Write(env.ctx, env.actor, env.scope, func(unit contract.Unit) error {
		payload, hErr := env.svc.Handle(env.ctx, unit, contract.Invocation{Operation: opCommandBegin, Version: 1, Input: raw})
		if hErr != nil {
			t.Fatalf("begin failed inside transaction: %v", hErr)
		}
		var out commandBeginOutput
		env.decode(payload.Data, &out)
		firstID = out.CommandID
		// The handler that would have run after begin() fails; the whole
		// transaction, including the reservation just made, rolls back.
		return simulatedFailure
	})
	if err == nil {
		t.Fatal("expected the simulated failure to roll back the transaction")
	}

	// A fresh begin with the identical identity and digest reserves again:
	// nothing survived the rollback to replay against.
	second := env.beginCommand(principal, "widget.create", 1, "key-1", digest)
	if second.Existing != nil {
		t.Fatalf("post-rollback begin found existing %+v, want a fresh reservation", second.Existing)
	}
	if second.CommandID == firstID {
		t.Fatalf("post-rollback begin reused the rolled-back command id %s", firstID)
	}

	// Finishing now durably records the refusal under the fresh identity —
	// mirroring application's recordRefusal after a handler fault.
	refusal := failedResult(second.CommandID, contract.CodeConflict, "durable refusal")
	finished := env.finishCommandOp(second.CommandID, refusal)
	if finished.Resource.Status != contract.StatusFailed {
		t.Fatalf("refusal status %q, want failed", finished.Resource.Status)
	}

	replay := env.beginCommand(principal, "widget.create", 1, "key-1", digest)
	if replay.Existing == nil || replay.Existing.Status != contract.StatusFailed {
		t.Fatalf("replay after refusal = %+v, want a failed existing disposition", replay.Existing)
	}
}

// Duplicate command concurrency / lost ack: N concurrent identical
// submissions serialize to exactly one durable disposition; nothing else
// duplicates or replaces it merely because an acknowledgement was lost. Each
// attempt runs begin and (when fresh) finish inside one write transaction,
// mirroring application's own invokeMutation composition — begin, the
// handler and finish always share one transaction in real dispatch.
func TestConcurrentIdenticalCommandsConverge(t *testing.T) {
	env := newEnv(t)
	principal := env.ids.New()
	digest := digestOf(t, map[string]string{"a": "1"})
	const attempts = 8

	run := func() (contract.ID, bool, error) {
		beginRaw, err := json.Marshal(commandBeginInput{
			PrincipalID: principal, Operation: "widget.create", OperationVersion: 1,
			SubmissionKey: "key-race", RequestDigest: digest,
		})
		if err != nil {
			return "", false, err
		}
		var id contract.ID
		var replay bool
		err = env.db.Write(env.ctx, env.actor, env.scope, func(unit contract.Unit) error {
			payload, hErr := env.svc.Handle(env.ctx, unit, contract.Invocation{Operation: opCommandBegin, Version: 1, Input: beginRaw})
			if hErr != nil {
				return hErr
			}
			if payload.Error != nil {
				return payload.Error
			}
			var begin commandBeginOutput
			if dErr := json.Unmarshal(payload.Data, &begin); dErr != nil {
				return dErr
			}
			id = begin.CommandID
			if begin.Existing != nil {
				replay = true
				return nil
			}
			finishRaw, mErr := json.Marshal(commandFinishInput{
				CommandID: begin.CommandID,
				Result:    completeResult(begin.CommandID, json.RawMessage(`{"n":1}`)),
			})
			if mErr != nil {
				return mErr
			}
			finPayload, hErr := env.svc.Handle(env.ctx, unit, contract.Invocation{Operation: opCommandFinish, Version: 1, Input: finishRaw})
			if hErr != nil {
				return hErr
			}
			if finPayload.Error != nil {
				return finPayload.Error
			}
			return nil
		})
		return id, replay, err
	}

	var wg sync.WaitGroup
	ids := make([]contract.ID, attempts)
	replays := make([]bool, attempts)
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, replay, err := run()
			ids[i] = id
			replays[i] = replay
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("attempt %d failed: %v", i, err)
		}
	}
	first := ids[0]
	freshCount := 0
	for i, id := range ids {
		if id != first {
			t.Fatalf("attempt %d command id %s, want %s (every attempt must converge on one identity)", i, id, first)
		}
		if !replays[i] {
			freshCount++
		}
	}
	if freshCount != 1 {
		t.Fatalf("%d attempts ran fresh, want exactly 1 (the rest must observe the durable disposition)", freshCount)
	}
}

// Principal isolation: command.get never returns another principal's
// command — it is indistinguishable from an absent one.
func TestCommandGetIsolatesByPrincipal(t *testing.T) {
	env := newEnv(t)
	owner := env.ids.New()
	stranger := env.ids.New()
	begin := env.beginCommand(owner, "widget.create", 1, "shared-key", digestOf(t, 1))
	env.finishCommandOp(begin.CommandID, completeResult(begin.CommandID, json.RawMessage(`{"ok":true}`)))

	ownerActor := contract.Actor{PrincipalID: owner, Kind: contract.KindService}
	payload := env.mustOKAs(ownerActor, env.scope, opCommandGet, commandGetInput{
		Scope: env.scope, SubmissionKey: "shared-key", Operation: "widget.create", OperationVersion: 1,
	})
	var out commandResourceBody
	env.decode(payload.Data, &out)
	if out.Resource.ID != begin.CommandID {
		t.Fatalf("owner's command.get returned %s, want %s", out.Resource.ID, begin.CommandID)
	}

	strangerActor := contract.Actor{PrincipalID: stranger, Kind: contract.KindService}
	_ = env.expectFaultAs(strangerActor, env.scope, opCommandGet, commandGetInput{
		Scope: env.scope, SubmissionKey: "shared-key", Operation: "widget.create", OperationVersion: 1,
	}, contract.CodeNotFound)
}

func TestCommandGetUnknownIsNotFound(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault(opCommandGet, commandGetInput{
		Scope: env.scope, SubmissionKey: "nope", Operation: "widget.create", OperationVersion: 1,
	}, contract.CodeNotFound)
}

func TestCommandGetReturnsFailedDispositionVerbatim(t *testing.T) {
	env := newEnv(t)
	begin := env.beginCommand(env.actor.PrincipalID, "widget.create", 1, "key-1", digestOf(t, 1))
	details := json.RawMessage(`{"reason":"budget_unavailable"}`)
	refusal := failedResult(begin.CommandID, contract.CodeBudgetUnavailable, "over budget")
	refusal.Error.Details = details
	refusal.Error.Retryable = true
	env.finishCommandOp(begin.CommandID, refusal)

	payload := env.mustOK(opCommandGet, commandGetInput{
		Scope: env.scope, SubmissionKey: "key-1", Operation: "widget.create", OperationVersion: 1,
	})
	var out commandResourceBody
	env.decode(payload.Data, &out)
	if out.Resource.Status != contract.StatusFailed {
		t.Fatalf("status %q, want failed", out.Resource.Status)
	}
	if out.Resource.ErrorCode != contract.CodeBudgetUnavailable {
		t.Fatalf("error_code %q, want %q", out.Resource.ErrorCode, contract.CodeBudgetUnavailable)
	}
	if out.Resource.Result.Error == nil || !out.Resource.Result.Error.Retryable {
		t.Fatalf("retained result fault retryable = %v, want true", out.Resource.Result.Error)
	}
	if string(out.Resource.Result.Error.Details) != string(details) {
		t.Fatalf("retained fault details %s, want %s", out.Resource.Result.Error.Details, details)
	}
}

// State-event atomicity: finishing a command emits exactly one
// state-correlated transition event for that command, in the same
// transaction as the state change itself.
func TestCommandFinishEmitsOneTransitionEvent(t *testing.T) {
	env := newEnv(t)
	principal := env.ids.New()
	begin := env.beginCommand(principal, "widget.create", 1, "key-1", digestOf(t, 1))
	env.finishCommandOp(begin.CommandID, completeResult(begin.CommandID, nil))

	events, err := env.db.Events(env.ctx, 0, 500)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var matches int
	for _, ev := range events {
		if ev.ResourceID == begin.CommandID {
			matches++
			if ev.Kind != eventCommandCompleted {
				t.Fatalf("event kind %q, want %q", ev.Kind, eventCommandCompleted)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("%d transition events for command %s, want exactly 1", matches, begin.CommandID)
	}
}

func TestCommandFinishFailedEmitsFailedKind(t *testing.T) {
	env := newEnv(t)
	principal := env.ids.New()
	begin := env.beginCommand(principal, "widget.create", 1, "key-1", digestOf(t, 1))
	env.finishCommandOp(begin.CommandID, failedResult(begin.CommandID, contract.CodeConflict, "conflict"))

	events, err := env.db.Events(env.ctx, 0, 500)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.ResourceID == begin.CommandID {
			found = true
			if ev.Kind != eventCommandFailed {
				t.Fatalf("event kind %q, want %q", ev.Kind, eventCommandFailed)
			}
		}
	}
	if !found {
		t.Fatalf("no transition event recorded for failed command %s", begin.CommandID)
	}
}
