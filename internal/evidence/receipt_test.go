package evidence

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// P34: resumable event-tail cursors bound to principal/scope/filter/
// retention, committed turn/proposal/task/run/effect/verification linkage
// projected without alteration, and retained command/result/job-completion
// links a client can recover after a lost acknowledgement.

// Required test: "A client drains events, restarts, then resumes without
// missing or endlessly rereading events." event.list must mint a resumable
// cursor even when the page it returns is already drained, so a client that
// stops polling (or crashes) after draining the backlog can pick back up
// exactly where it left off instead of either losing its place (no cursor
// at all) or rereading the entire backlog again (an empty/omitted cursor
// forcing a fresh, unfiltered replay from the start).
func TestEventListResumesAfterDrainWithoutMissingOrRereading(t *testing.T) {
	env := newEnv(t)
	first := env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	second := env.emitEvent(env.scope, "widget.thing.created", env.ids.New())

	// The client drains the full current backlog.
	items, cursor := env.listEvents(env.scope, nil, nil, nil)
	if len(items) != 2 || items[0].ResourceID != first.ResourceID || items[1].ResourceID != second.ResourceID {
		t.Fatalf("drained page = %+v, want exactly [%s %s]", items, first.ResourceID, second.ResourceID)
	}
	if cursor == nil {
		t.Fatal("a drained page returned no resumable cursor")
	}

	// The client "restarts" (new process, same durable cursor) and resumes
	// before anything new has happened: it must not reread what it already
	// drained.
	items, cursorAfterRestart := env.listEvents(env.scope, cursor, nil, nil)
	if len(items) != 0 {
		t.Fatalf("resume before new events reread %d already-seen events: %+v", len(items), items)
	}
	if cursorAfterRestart == nil {
		t.Fatal("resuming an already-drained tail returned no cursor to continue from")
	}

	// A new event lands after the restart. Resuming from the cursor handed
	// back on the empty resume must surface it — not miss it.
	third := env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	items, cursorAfterThird := env.listEvents(env.scope, cursorAfterRestart, nil, nil)
	if len(items) != 1 || items[0].ResourceID != third.ResourceID {
		t.Fatalf("resumed items = %+v, want exactly the new event %s", items, third.ResourceID)
	}
	if cursorAfterThird == nil {
		t.Fatal("resuming onto a freshly drained tail again returned no cursor")
	}

	// Draining again from that latest cursor must not repeat the third
	// event either.
	items, _ = env.listEvents(env.scope, cursorAfterThird, nil, nil)
	if len(items) != 0 {
		t.Fatalf("second resume reread %d events, want 0: %+v", len(items), items)
	}
}

// Required test (first half): "Foreign/revoked scope cannot use an earlier
// cursor." A cursor is bound to the exact calling principal at mint time; a
// different principal — even one otherwise authorized to read the same
// installation — cannot present it.
func TestEventListCursorForeignPrincipalIsRejected(t *testing.T) {
	env := newEnv(t)
	limit := int64(1)
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	_, next := env.listEvents(env.scope, nil, &limit, nil)
	if next == nil {
		t.Fatal("expected a next cursor")
	}

	stranger := contract.Actor{PrincipalID: env.ids.New(), Kind: contract.KindService}
	_ = env.expectFaultAs(stranger, env.scope, opEventList, eventListInput{
		Scope: env.scope, Cursor: next, Limit: &limit,
	}, contract.CodeInvalidInput)
}

// Required test (first half, continued): a scope that has been narrowed
// since the cursor was minted — the shape a revoked worker/task/project
// assignment takes once the caller's authorized scope no longer includes
// what it used to — cannot reuse the earlier, wider-scoped cursor either.
func TestEventListCursorNarrowedAfterRevocationIsRejected(t *testing.T) {
	env := newEnv(t)
	limit := int64(1)
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	_, next := env.listEvents(env.scope, nil, &limit, nil)
	if next == nil {
		t.Fatal("expected a next cursor")
	}

	// The caller's worker assignment is revoked between calls: its next
	// request is scoped narrower (to just that worker) than the cursor was
	// minted under.
	narrowed := env.scope
	narrowed.WorkerID = env.ids.New()
	_ = env.expectFault(opEventList, eventListInput{
		Scope: narrowed, Cursor: next, Limit: &limit,
	}, contract.CodeInvalidInput)
}

// Required test (second half): "expired cursor returns the named recovery
// path." Re-proves the existing cursor_expired/snapshot_required contract
// specifically as the P34 event-tail recovery path a resuming client must
// follow: fetch _evidence.snapshot and start a fresh replay, never treat the
// gap as complete history.
func TestEventListExpiredCursorNamesSnapshotRecoveryPath(t *testing.T) {
	env := newEnv(t)
	limit := int64(1)
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	_, next := env.listEvents(env.scope, nil, &limit, nil)
	if next == nil {
		t.Fatal("expected a next cursor")
	}

	env.clock.Advance(eventCursorTTL + 1)
	f := env.expectFault(opEventList, eventListInput{Scope: env.scope, Cursor: next, Limit: &limit}, contract.CodeCursorExpired)
	var details struct {
		SnapshotRequired bool `json:"snapshot_required"`
	}
	if err := json.Unmarshal(f.Details, &details); err != nil {
		t.Fatalf("decode fault details: %v", err)
	}
	if !details.SnapshotRequired {
		t.Fatal("cursor_expired fault did not name the snapshot recovery path")
	}

	// The named recovery path actually works: a fresh snapshot plus a
	// cursor-less replay observes every retained event, no gap presented as
	// complete history.
	snap := env.mustOK(opSnapshot, snapshotInput{Scope: env.scope})
	var snapOut snapshotOutput
	env.decode(snap.Data, &snapOut)
	if snapOut.LastSequence < 2 {
		t.Fatalf("snapshot checkpoint %d, want at least 2 retained events", snapOut.LastSequence)
	}
	items, _ := env.listEvents(env.scope, nil, nil, nil)
	if len(items) != 2 {
		t.Fatalf("fresh replay after recovery returned %d events, want 2", len(items))
	}
}

// Required test: turn/proposal/task/run/effect/verification linkage,
// including waiting and unknown dispositions, is projected exactly as
// another owner committed it — never derived, edited or inferred from
// anything a worker merely claims. Evidence has no code path that even sees
// a worker's self-report: it only ever reads what another owner's own
// transaction already durably committed through Unit.Emit (here simulated
// the same way storage's other real owners commit theirs), and exposes it
// unchanged except for denylisted-key redaction.
func TestEventListProjectsTurnProposalTaskRunEffectVerificationLinkage(t *testing.T) {
	env := newEnv(t)
	turnID := env.ids.New()
	proposalID := env.ids.New()
	taskID := env.ids.New()
	runID := env.ids.New()
	effectID := env.ids.New()
	verificationID := env.ids.New()

	fixtures := []struct {
		kind string
		id   contract.ID
		data json.RawMessage
	}{
		{"execution.turn.admitted", turnID,
			json.RawMessage(`{"disposition":"waiting","task_id":"` + string(taskID) + `"}`)},
		{"execution.proposal.recorded", proposalID,
			json.RawMessage(`{"turn_id":"` + string(turnID) + `","step_index":1,"kind":"tool_call"}`)},
		{"tasks.run.created", runID,
			json.RawMessage(`{"task_id":"` + string(taskID) + `","state":"running"}`)},
		{"effects.dispatch.recorded", effectID,
			json.RawMessage(`{"disposition":"unknown","task_id":"` + string(taskID) + `","credential_ref":"cred-ref-1","token":"do-not-leak"}`)},
		{"execution.verification.recorded", verificationID,
			json.RawMessage(`{"disposition":"unknown","task_id":"` + string(taskID) + `","attempt_id":"` + string(effectID) + `"}`)},
	}
	for _, f := range fixtures {
		env.emitEventWithData(env.scope, f.kind, f.id, f.data)
	}

	items, _ := env.listEvents(env.scope, nil, nil, nil)
	if len(items) != len(fixtures) {
		t.Fatalf("listed %d events, want %d", len(items), len(fixtures))
	}
	byResource := map[contract.ID]contract.Event{}
	for _, ev := range items {
		byResource[ev.ResourceID] = ev
	}
	for _, f := range fixtures {
		ev, ok := byResource[f.id]
		if !ok {
			t.Fatalf("%s: %s not projected by event.list", f.kind, f.id)
		}
		if ev.Kind != f.kind {
			t.Fatalf("%s: projected kind %q, want %q", f.id, ev.Kind, f.kind)
		}
		var want map[string]any
		if err := json.Unmarshal(f.data, &want); err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(ev.Data, &got); err != nil {
			t.Fatalf("decode projected data: %v", err)
		}
		for k, v := range want {
			if k == "token" {
				if got[k] != redactedPlaceholder {
					t.Fatalf("%s: secret field %q leaked as %v, want %v", f.kind, k, got[k], redactedPlaceholder)
				}
				continue
			}
			if got[k] != v {
				t.Fatalf("%s: field %q = %v, want %v exactly as committed — evidence must never alter linkage data",
					f.kind, k, got[k], v)
			}
		}

		single, err := env.getEvent(env.scope, ev.ID)
		if err != nil {
			t.Fatalf("event.get %s: %v", f.kind, err)
		}
		if single.Kind != f.kind || single.ResourceID != f.id {
			t.Fatalf("event.get %s = %+v, want kind/resource to match the committed fixture", f.kind, single)
		}
	}
}

// Required test: "Receipt for an accepted effect remains pending until
// authoritative confirmation and never implies verified task success."
// _evidence.command.finish's accepted-then-replaced-once mechanics, applied
// to an effect-shaped receipt: the receipt stays "accepted" (pending) after
// the effect is merely dispatched, nothing about an unrelated task's own
// evidence is created or implied by that pending receipt, and only an
// explicit second finish (the authoritative confirmation) ever moves it to
// a terminal disposition.
func TestEffectReceiptStaysPendingUntilAuthoritativeConfirmation(t *testing.T) {
	env := newEnv(t)
	principal := env.actor.PrincipalID
	taskID := env.ids.New()
	digest := digestOf(t, map[string]string{"attempt": "1"})

	begin := env.beginCommand(principal, "_effects.record", 1, "attempt-1", digest)
	accepted := acceptedResult(begin.CommandID,
		json.RawMessage(`{"disposition":"accepted","task_id":"`+string(taskID)+`"}`))
	env.finishCommandOp(begin.CommandID, accepted)

	receipt := env.getCommandFor("attempt-1", "_effects.record")
	if receipt.Resource.Status != contract.StatusAccepted {
		t.Fatalf("effect receipt status %q, want accepted (pending)", receipt.Resource.Status)
	}
	var body struct {
		Disposition string `json:"disposition"`
	}
	env.decode(receipt.Resource.Data, &body)
	if body.Disposition != "accepted" {
		t.Fatalf("effect receipt disposition %q, want accepted", body.Disposition)
	}

	// The pending effect receipt implies nothing about the task it names:
	// evidence records only what it was explicitly told, so a separate,
	// unrelated command identity for that task's own outcome does not exist
	// merely because this effect was accepted.
	_ = env.expectFault(opCommandGet, commandGetInput{
		Scope: env.scope, SubmissionKey: "task-outcome", Operation: "task.start", OperationVersion: 1,
	}, contract.CodeNotFound)

	// Authoritative confirmation lands: the pending receipt is replaced
	// exactly once, and only now does the receipt state a terminal outcome.
	final := completeResult(begin.CommandID,
		json.RawMessage(`{"disposition":"succeeded","task_id":"`+string(taskID)+`"}`))
	env.finishCommandOp(begin.CommandID, final)

	receipt = env.getCommandFor("attempt-1", "_effects.record")
	if receipt.Resource.Status != contract.StatusCompleted {
		t.Fatalf("effect receipt status %q after confirmation, want completed", receipt.Resource.Status)
	}

	// The confirmed effect receipt still names no independent task-success
	// evidence: only the task's own owner can produce that, never evidence
	// on the effect's behalf.
	_ = env.expectFault(opCommandGet, commandGetInput{
		Scope: env.scope, SubmissionKey: "task-outcome", Operation: "task.start", OperationVersion: 1,
	}, contract.CodeNotFound)
}

// Required item: "Provide retained command/result and job completion links
// for clients to resolve lost acknowledgements and later outcomes." A
// client that never observed the accepted response naming an async job
// recovers it later by submission-key identity, and — once the job
// completes and the command is finished a second time — recovers the
// durable final outcome the same way, never by re-deriving or re-sending
// the mutation.
func TestCommandGetResolvesLostAcknowledgementAndLaterJobOutcome(t *testing.T) {
	env := newEnv(t)
	principal := env.actor.PrincipalID
	digest := digestOf(t, map[string]string{"op": "1"})
	begin := env.beginCommand(principal, "installation.backup", 1, "backup-1", digest)

	// The accepted response (naming the async job) was sent, but its
	// acknowledgement was lost before the client observed it.
	env.finishCommandOp(begin.CommandID,
		acceptedResult(begin.CommandID, json.RawMessage(`{"job":{"id":"job-1","state":"running"}}`)))

	// Recovery: the client never retried the mutation, only looked it up by
	// its original submission identity.
	recovered := env.getCommandFor("backup-1", "installation.backup")
	if recovered.Resource.Status != contract.StatusAccepted {
		t.Fatalf("recovered status %q, want accepted", recovered.Resource.Status)
	}
	var job struct {
		Job struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"job"`
	}
	env.decode(recovered.Resource.Data, &job)
	if job.Job.ID != "job-1" {
		t.Fatalf("recovered job reference %+v, want job-1", job.Job)
	}

	// The job later completes; the pending receipt is replaced exactly once
	// with the durable final outcome.
	env.finishCommandOp(begin.CommandID,
		completeResult(begin.CommandID, json.RawMessage(`{"artifact_id":"a-1","database_digest":"deadbeef"}`)))

	// A second lost acknowledgement recovers the same way — the final
	// outcome, not the earlier job reference and not a re-derived guess.
	final := env.getCommandFor("backup-1", "installation.backup")
	if final.Resource.Status != contract.StatusCompleted {
		t.Fatalf("final recovered status %q, want completed", final.Resource.Status)
	}
	if string(final.Resource.Data) != `{"artifact_id":"a-1","database_digest":"deadbeef"}` {
		t.Fatalf("final recovered data %s, want the retained completion output", final.Resource.Data)
	}
	if final.Resource.Result.CommandID != begin.CommandID {
		t.Fatalf("final recovered result command id %s, want %s", final.Resource.Result.CommandID, begin.CommandID)
	}
}
