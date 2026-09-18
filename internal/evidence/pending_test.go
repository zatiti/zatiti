package evidence

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Pending dispositions. The shared contract has a synchronous local IO
// mutation "persist command identity plus accepted internal pending
// disposition at Prepare, then replace pending disposition once at Finish":
// the first finish commits the accepted disposition with the Prepare
// transaction and a second finish, in the Finish transaction, replaces it.
// Exactly one replacement exists, only over an accepted disposition.

// acceptedResult builds a schema-valid accepted Result for commandID.
func acceptedResult(commandID contract.ID, data json.RawMessage) contract.Result {
	return contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: commandID,
		Payload:   contract.Payload{Status: contract.StatusAccepted, Data: data},
	}
}

func (e *testEnv) getCommand(key string) commandResourceBody {
	e.t.Helper()
	payload := e.mustOK(opCommandGet, commandGetInput{
		Scope: e.scope, SubmissionKey: key, Operation: "artifact.upload.chunk", OperationVersion: 1,
	})
	var out commandResourceBody
	e.decode(payload.Data, &out)
	return out
}

func TestPendingDispositionIsReplacedOnceAtFinish(t *testing.T) {
	env := newEnv(t)
	principal := env.actor.PrincipalID
	digest := digestOf(t, map[string]string{"chunk": "1"})
	begin := env.beginCommand(principal, "artifact.upload.chunk", 1, "key-1", digest)

	pending := env.finishCommandOp(begin.CommandID, acceptedResult(begin.CommandID, json.RawMessage(`{"plan":"p1"}`)))
	if pending.Resource.Status != contract.StatusAccepted {
		t.Fatalf("pending status %q, want accepted", pending.Resource.Status)
	}
	// A crash between Perform and Finish leaves exactly this record: both a
	// same-key replay and a lost-acknowledgement lookup report it accepted.
	joined := env.beginCommand(principal, "artifact.upload.chunk", 1, "key-1", digest)
	if joined.Existing == nil || joined.Existing.Status != contract.StatusAccepted {
		t.Fatalf("same-key begin over a pending command returned %+v, want the accepted disposition", joined.Existing)
	}
	if got := env.getCommand("key-1"); got.Resource.Status != contract.StatusAccepted {
		t.Fatalf("command.get over a pending command: status %q, want accepted", got.Resource.Status)
	}

	final := completeResult(begin.CommandID, json.RawMessage(`{"received_size":42}`))
	replaced := env.finishCommandOp(begin.CommandID, final)
	if replaced.Resource.Status != contract.StatusCompleted || replaced.Resource.ID != begin.CommandID {
		t.Fatalf("replaced command %+v, want completed %s", replaced.Resource, begin.CommandID)
	}

	replay := env.beginCommand(principal, "artifact.upload.chunk", 1, "key-1", digest)
	if replay.Existing == nil {
		t.Fatal("replay of a finished local IO command returned no disposition")
	}
	want, _ := json.Marshal(final)
	got, _ := json.Marshal(replay.Existing.Result)
	if string(got) != string(want) {
		t.Fatalf("replayed result %s, want the identical retained result %s", got, want)
	}
	if got := env.getCommand("key-1"); got.Resource.Status != contract.StatusCompleted {
		t.Fatalf("command.get after replacement: status %q, want completed", got.Resource.Status)
	}

	// Once: the replacement is itself final.
	_ = env.expectFault(opCommandFinish, commandFinishInput{
		CommandID: begin.CommandID, Result: completeResult(begin.CommandID, json.RawMessage(`{"received_size":43}`)),
	}, contract.CodeInternalError)
	if got := env.getCommand("key-1"); string(got.Resource.Data) != `{"received_size":42}` {
		t.Fatalf("a refused third finish changed the retained data to %s", got.Resource.Data)
	}
}

func TestPendingDispositionReplacedByFailure(t *testing.T) {
	env := newEnv(t)
	begin := env.beginCommand(env.actor.PrincipalID, "artifact.upload.chunk", 1, "key-1", digestOf(t, 1))
	env.finishCommandOp(begin.CommandID, acceptedResult(begin.CommandID, nil))
	refusal := failedResult(begin.CommandID, contract.CodeArtifactFault, "staged bytes do not match the digest")
	env.finishCommandOp(begin.CommandID, refusal)
	got := env.getCommand("key-1")
	if got.Resource.Status != contract.StatusFailed || got.Resource.ErrorCode != contract.CodeArtifactFault {
		t.Fatalf("replaced command status %q code %q, want failed artifact_fault", got.Resource.Status, got.Resource.ErrorCode)
	}
}

// An accepted job reference may replace the pending disposition (asynchronous
// local IO returns {job}), but it cannot then be replaced again: "a replay
// cannot ... lose an accepted job reference".
func TestAcceptedReplacementIsFinal(t *testing.T) {
	env := newEnv(t)
	begin := env.beginCommand(env.actor.PrincipalID, "artifact.upload.chunk", 1, "key-1", digestOf(t, 1))
	env.finishCommandOp(begin.CommandID, acceptedResult(begin.CommandID, nil))
	env.finishCommandOp(begin.CommandID, acceptedResult(begin.CommandID, json.RawMessage(`{"job":{"id":"j1"}}`)))
	_ = env.expectFault(opCommandFinish, commandFinishInput{
		CommandID: begin.CommandID, Result: completeResult(begin.CommandID, nil),
	}, contract.CodeInternalError)
	if got := env.getCommand("key-1"); string(got.Resource.Data) != `{"job":{"id":"j1"}}` {
		t.Fatalf("retained data %s, want the accepted job reference", got.Resource.Data)
	}
}

func TestTerminalDispositionIsNeverReplaced(t *testing.T) {
	for name, first := range map[string]func(contract.ID) contract.Result{
		"completed": func(id contract.ID) contract.Result { return completeResult(id, nil) },
		"failed": func(id contract.ID) contract.Result {
			return failedResult(id, contract.CodeConflict, "duplicate")
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			begin := env.beginCommand(env.actor.PrincipalID, "artifact.upload.chunk", 1, "key-1", digestOf(t, 1))
			env.finishCommandOp(begin.CommandID, first(begin.CommandID))
			_ = env.expectFault(opCommandFinish, commandFinishInput{
				CommandID: begin.CommandID, Result: acceptedResult(begin.CommandID, nil),
			}, contract.CodeInternalError)
		})
	}
}

// State-event atomicity: each disposition change emits one transition event,
// versioned in order so consumers see accepted before the replacement.
func TestPendingReplacementEmitsVersionedTransitions(t *testing.T) {
	env := newEnv(t)
	begin := env.beginCommand(env.actor.PrincipalID, "artifact.upload.chunk", 1, "key-1", digestOf(t, 1))
	env.finishCommandOp(begin.CommandID, acceptedResult(begin.CommandID, nil))
	env.finishCommandOp(begin.CommandID, completeResult(begin.CommandID, nil))

	events, err := env.db.Events(env.ctx, 0, 500)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var got []string
	for _, ev := range events {
		if ev.ResourceID == begin.CommandID {
			got = append(got, ev.Kind+"@"+string(rune('0'+ev.ResourceVersion)))
		}
	}
	want := []string{eventCommandAccepted + "@1", eventCommandCompleted + "@2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("transition events %v, want %v", got, want)
	}
}
