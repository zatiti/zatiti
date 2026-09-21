package execution

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// execution-dispatch-model-step (same-day P22 gap fix) required behavioral
// tests:
//   - TestContextCommitDispatchesPrepareSessionNotModelStepWhenNoHandle
//   - TestPrepareSessionObservationPersistsHandleThenDispatchesModelStep
//   - TestContextCommitSkipsPrepareSessionOnceSessionHandleIsKnown
//   - TestPrepareSessionObservationNeverMisinterpretedAsModelOutput
//   - TestPrepareSessionObservationFailedParksTurnWaitingWithoutRetry
//
// These prove the exact gap P22 disclosed: nothing previously chained a
// committed context through to a dispatched Responses effect. handleContextCommit
// now does so, choosing prepare_session or model_step by whether the turn
// already carries a confirmed session_handle, and a prepare_session's own
// confirmed observation is interpreted through a distinct path
// (interpretPrepareSessionObservation) that persists the handle and chains
// straight through to the real model_step dispatch -- never through
// interpretTurnObservation's ModelOutput-specific validation.

// dispatchFixture wires one hosted, task-bound, model-tool-bound turn
// through admission and automatic claim, ready to drive context.prepare/
// .commit and assert on the real dispatched Responses effect.
type dispatchFixture struct {
	e         *testEnv
	worker    contract.ID
	toolID    contract.ID
	connID    contract.ID
	turnID    contract.ID
	attemptID contract.ID
}

func newDispatchFixture(t *testing.T) *dispatchFixture {
	t.Helper()
	e := newEnv(t)
	worker := e.ids.New()
	profile := fixtureHostedProfile(worker)
	e.installWorkerSnapshot(worker, profile)
	toolID, connID := e.installModelToolBinding(worker, profile)

	run := e.enqueueTask(worker, nil)
	turn := e.findTurnForSource("task", run.TaskID, 1, worker)
	if turn == nil {
		t.Fatalf("enqueue of a hosted task admitted no WorkerTurn")
	}
	claim := e.mustOK(opWorkClaim, workClaimInput{WorkID: turn.ID, ExpectedVersion: turn.Version, Generation: e.generation()})
	var claimBody2 workClaimBody
	e.decode(claim.Data, &claimBody2)
	if claimBody2.Item.Turn.AttemptID == "" {
		t.Fatalf("work.claim did not auto-claim a hosted attempt")
	}
	return &dispatchFixture{
		e: e, worker: worker, toolID: toolID, connID: connID,
		turnID: turn.ID, attemptID: claimBody2.Item.Turn.AttemptID,
	}
}

// commitFreshContext drives the turn through one context.prepare/.commit
// cycle and returns the committed context artifact plus the commit payload.
func (f *dispatchFixture) commitFreshContext(t *testing.T) (wireArtifactRef, turnBody) {
	t.Helper()
	e := f.e
	turn := e.readTurn(f.turnID)
	prep := e.mustOK(opContextPrepare, contextPrepareInput{TurnID: turn.ID, ExpectedVersion: turn.Version, Generation: turn.Generation})
	var plan contextPlanBody
	e.decode(prep.Data, &plan)
	ref := wireArtifactRef{ID: e.ids.New(), Digest: fixtureDigest}
	commit := e.mustOK(opContextCommit, contextCommitInput{
		PlanID: plan.Resource.ID, ExpectedVersion: plan.Resource.ExpectedVersion,
		Generation: plan.Resource.Generation, StagedContext: wireArtifactLocator{Kind: "artifact", Artifact: &ref},
	})
	var body turnBody
	e.decode(commit.Data, &body)
	return ref, body
}

// buildPrepareSessionEvidence assembles one schema-valid
// zatiti.responses.evidence/v1 document for a confirmed prepare_session
// call -- structurally distinct from a ModelOutput (no finish_reason,
// text_outputs, tool_proposals or usage; a required session_handle instead).
func buildPrepareSessionEvidence(t *testing.T, sessionHandle string) json.RawMessage {
	t.Helper()
	physicalCall := mustMarshal(t, map[string]any{
		"operation_id":          "00000000-0000-4000-8000-000000000101",
		"attempt_id":            "00000000-0000-4000-8000-000000000102",
		"account_identity":      "acct-test",
		"requested_destination": "https://provider.invalid/v1",
		"resolved_destination":  "https://provider.invalid/v1",
		"profile_digest":        digestA,
		"capability_evidence":   map[string]any{"id": "00000000-0000-4000-8000-000000000103", "digest": digestA},
		"started_at":            "2026-09-10T12:00:00.000000000Z",
		"finished_at":           "2026-09-10T12:00:01.000000000Z",
		"request_context": map[string]any{
			"kind":     "artifact",
			"artifact": map[string]any{"id": "00000000-0000-4000-8000-000000000104", "digest": digestA},
		},
		"request_sent": "yes",
		"confirmation": "authoritative_success",
	})
	evidence := wireResponsesEvidence{
		Schema:        "zatiti.responses.evidence/v1",
		PhysicalCall:  physicalCall,
		SessionHandle: sessionHandle,
		StagedOutputs: json.RawMessage(`[]`),
	}
	return mustMarshal(t, evidence)
}

// TestContextCommitDispatchesPrepareSessionNotModelStepWhenNoHandle proves
// the first required behavior: a turn with no session_handle, on reaching
// model_pending, dispatches a prepare_session effect -- asserted against the
// real _effects.prepare call shape, not a fake shortcut -- and never a
// model_step.
func TestContextCommitDispatchesPrepareSessionNotModelStepWhenNoHandle(t *testing.T) {
	f := newDispatchFixture(t)
	e := f.e

	_, committed := f.commitFreshContext(t)
	if committed.Resource.State != "model_pending" {
		t.Fatalf("committed turn state %q, want model_pending", committed.Resource.State)
	}

	calls := e.ports.EffectsPrepared()
	if len(calls) != 1 {
		t.Fatalf("_effects.prepare called %d times on first commit, want exactly 1", len(calls))
	}
	call := calls[0]
	if got := call.ActionKind(); got != "prepare_session" {
		t.Fatalf("dispatched action kind %q, want prepare_session", got)
	}
	params, _ := call.Action["parameters"].(map[string]any)
	if _, hasContext := params["context_artifact"]; hasContext {
		t.Fatalf("prepare_session action carries context_artifact %+v, want none (no model-visible content)", params["context_artifact"])
	}
	if _, hasHandle := params["session_handle"]; hasHandle {
		t.Fatalf("prepare_session action carries session_handle %+v, want none", params["session_handle"])
	}
	tool, _ := call.Action["tool"].(map[string]any)
	if tool == nil || tool["id"] != string(f.toolID) {
		t.Fatalf("dispatched action tool %+v, want the resolved model-dispatch tool %s", tool, f.toolID)
	}
	route := call.CallbackRoute
	if route == nil || route["kind"] != "worker_turn" || route["turn_id"] != string(f.turnID) {
		t.Fatalf("callback_route %+v does not name worker_turn/%s", route, f.turnID)
	}

	// The turn's own persisted session_handle is still empty -- nothing was
	// fabricated or guessed ahead of a real confirmed observation.
	turn := e.readTurn(f.turnID)
	if turn.SessionHandle != "" {
		t.Fatalf("turn session_handle %q before any confirmation, want empty", turn.SessionHandle)
	}
}

// TestPrepareSessionObservationPersistsHandleThenDispatchesModelStep proves
// the second required behavior: once the prepare_session effect's
// observation confirms a session_handle, the turn's session_handle is
// persisted AND a model_step effect is now dispatched -- not before.
func TestPrepareSessionObservationPersistsHandleThenDispatchesModelStep(t *testing.T) {
	f := newDispatchFixture(t)
	e := f.e

	contextRef, _ := f.commitFreshContext(t)
	prepared := e.ports.EffectsPrepared()
	if len(prepared) != 1 {
		t.Fatalf("_effects.prepare called %d times before confirmation, want 1", len(prepared))
	}
	sessionOpID := e.ports.PreparedOps()[0]

	deliver := e.mustOK(opObservation, observationInput{
		AttemptID: f.attemptID, OperationID: sessionOpID,
		Observation: wireObservation{
			Disposition: "succeeded",
			Evidence:    buildPrepareSessionEvidence(t, "sess-handle-abc"),
			Usage:       wireUsage{Currency: "USD"},
		},
	})
	if deliver.Status != contract.StatusCompleted {
		t.Fatalf("prepare_session confirmation status %q, want completed (fault %v)", deliver.Status, deliver.Error)
	}

	turn := e.readTurn(f.turnID)
	if turn.SessionHandle != "sess-handle-abc" {
		t.Fatalf("turn session_handle %q after confirmation, want sess-handle-abc", turn.SessionHandle)
	}
	if turn.State != "model_pending" {
		t.Fatalf("turn state %q after session confirmation, want model_pending (still awaiting model_step)", turn.State)
	}
	// interpretTurnObservation's ModelOutput-specific step accounting never
	// engaged for this prepare_session confirmation: no proposal was
	// interpreted, so the turn's step counter is untouched.
	if turn.StepsUsed != 0 {
		t.Fatalf("turn steps_used %d after a prepare_session confirmation, want 0 (never routed through model-output interpretation)", turn.StepsUsed)
	}

	calls := e.ports.EffectsPrepared()
	if len(calls) != 2 {
		t.Fatalf("_effects.prepare called %d times after confirmation, want exactly 2 (prepare_session, then model_step)", len(calls))
	}
	second := calls[1]
	if got := second.ActionKind(); got != "model_step" {
		t.Fatalf("second dispatched action kind %q, want model_step", got)
	}
	params, _ := second.Action["parameters"].(map[string]any)
	if got, _ := params["session_handle"].(string); got != "sess-handle-abc" {
		t.Fatalf("model_step action session_handle %q, want sess-handle-abc", got)
	}
	ctxArtifact, _ := params["context_artifact"].(map[string]any)
	if ctxArtifact == nil || ctxArtifact["id"] != string(contextRef.ID) {
		t.Fatalf("model_step action context_artifact %+v does not name the committed context %s", ctxArtifact, contextRef.ID)
	}
}

// TestContextCommitSkipsPrepareSessionOnceSessionHandleIsKnown proves the
// third required behavior: a turn that already has a session_handle skips
// straight to model_step on its next context commit -- no redundant
// prepare_session round trip.
func TestContextCommitSkipsPrepareSessionOnceSessionHandleIsKnown(t *testing.T) {
	f := newDispatchFixture(t)
	e := f.e

	f.commitFreshContext(t)
	sessionOpID := e.ports.PreparedOps()[0]
	e.mustOK(opObservation, observationInput{
		AttemptID: f.attemptID, OperationID: sessionOpID,
		Observation: wireObservation{
			Disposition: "succeeded",
			Evidence:    buildPrepareSessionEvidence(t, "sess-handle-xyz"),
			Usage:       wireUsage{Currency: "USD"},
		},
	})
	if got := len(e.ports.EffectsPrepared()); got != 2 {
		t.Fatalf("_effects.prepare called %d times after the first confirmation, want 2", got)
	}

	// A stray duplicate model_step observation for the automatically
	// dispatched effect above would advance the turn out of model_pending in
	// a way this test does not need; instead, directly prove the *next*
	// context.commit cycle (as if the turn had returned to claimed through
	// the ordinary turn-pipeline loop after a prior model_step's own
	// observation) dispatches model_step immediately, never a second
	// prepare_session, because the turn's session_handle is already known.
	e.inWrite(func(unit contract.Unit) error {
		turn, err := loadTurn(e.ctx, unit, f.turnID)
		if err != nil {
			return err
		}
		turn.State = "claimed"
		turn.UpdatedAt = e.clock.Now()
		return updateTurn(e.ctx, unit, turn)
	})

	f.commitFreshContext(t)
	calls := e.ports.EffectsPrepared()
	if len(calls) != 3 {
		t.Fatalf("_effects.prepare called %d times after the second commit, want 3 (prepare_session, model_step, model_step)", len(calls))
	}
	for i, kind := range []string{"prepare_session", "model_step", "model_step"} {
		if got := calls[i].ActionKind(); got != kind {
			t.Fatalf("dispatch %d action kind %q, want %q: %+v", i, got, kind, calls)
		}
	}
}

// TestPrepareSessionObservationNeverMisinterpretedAsModelOutput proves the
// fourth required behavior directly: a prepare_session observation's own
// ResponsesEvidence document -- which does not satisfy ModelOutput's schema
// (no finish_reason/text_outputs/tool_proposals/usage) -- is never validated
// against modelOutputSchema. If handleObservation's routing regressed to
// always falling through to interpretTurnObservation, this call would fail
// invalid_input instead of completing.
func TestPrepareSessionObservationNeverMisinterpretedAsModelOutput(t *testing.T) {
	f := newDispatchFixture(t)
	e := f.e

	f.commitFreshContext(t)
	sessionOpID := e.ports.PreparedOps()[0]
	evidence := buildPrepareSessionEvidence(t, "sess-handle-routing")

	// Sanity: this evidence genuinely does not satisfy ModelOutput's schema,
	// so a successful _execution.observation call below is real proof of
	// correct routing, not a coincidence of a lenient schema.
	modelSchema, err := modelOutputSchema()
	if err != nil {
		t.Fatalf("modelOutputSchema: %v", err)
	}
	if err := contract.ValidateSchema(modelSchema, evidence); err == nil {
		t.Fatalf("prepare_session evidence unexpectedly satisfies ModelOutput's schema; the test no longer proves routing")
	}

	payload := e.mustOK(opObservation, observationInput{
		AttemptID: f.attemptID, OperationID: sessionOpID,
		Observation: wireObservation{Disposition: "succeeded", Evidence: evidence, Usage: wireUsage{Currency: "USD"}},
	})
	if payload.Status != contract.StatusCompleted {
		t.Fatalf("prepare_session observation status %q, want completed", payload.Status)
	}
	turn := e.readTurn(f.turnID)
	if turn.SessionHandle != "sess-handle-routing" {
		t.Fatalf("turn session_handle %q, want sess-handle-routing", turn.SessionHandle)
	}
}

// TestPrepareSessionObservationFailedParksTurnWaitingWithoutRetry proves a
// failed prepare_session dispatch never fabricates a session and never
// automatically retries: the turn parks waiting on the typed effect reason,
// and the failed dispatch's own bookkeeping row prevents any later
// observation for the same operation_ref from being reinterpreted.
func TestPrepareSessionObservationFailedParksTurnWaitingWithoutRetry(t *testing.T) {
	f := newDispatchFixture(t)
	e := f.e

	f.commitFreshContext(t)
	sessionOpID := e.ports.PreparedOps()[0]
	e.mustOK(opObservation, observationInput{
		AttemptID: f.attemptID, OperationID: sessionOpID,
		Observation: wireObservation{Disposition: "failed", Evidence: json.RawMessage(`{}`), Usage: wireUsage{Currency: "USD"}},
	})

	turn := e.readTurn(f.turnID)
	if turn.State != "waiting" || turn.WaitingReason != waitingEffect {
		t.Fatalf("turn state/reason after a failed prepare_session = %s/%s, want waiting/effect", turn.State, turn.WaitingReason)
	}
	if turn.SessionHandle != "" {
		t.Fatalf("turn session_handle %q after a failed prepare_session, want empty", turn.SessionHandle)
	}
	if got := len(e.ports.EffectsPrepared()); got != 1 {
		t.Fatalf("_effects.prepare called %d times after a failed prepare_session, want 1 (no automatic retry)", got)
	}

	// A stray redelivery of the same failed observation is refused, not
	// reinterpreted a second time.
	_ = e.expectFault(opObservation, observationInput{
		AttemptID: f.attemptID, OperationID: sessionOpID,
		Observation: wireObservation{Disposition: "failed", Evidence: json.RawMessage(`{}`), Usage: wireUsage{Currency: "USD"}},
	}, contract.CodeConflict)
}
