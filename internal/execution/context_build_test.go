package execution

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// P15 required behavioral tests:
//   - TestContextCommitProducesSchemaValidOrderedContextAndModelStepAction
//   - TestContextPrepareRefusesBeforeAnyNetworkIO
//   - TestContextAdvanceKeepsToolResultOnceAndPriorContextBytesImmutable

// admitClaimedTurn admits a message-sourced turn for worker and drives it
// to claimed, returning the loaded row.
func (e *testEnv) admitClaimedTurn(worker contract.ID) *turnRow {
	e.t.Helper()
	payload := e.mustOK(opTurnAdmit, turnAdmitInput{
		Source:      wireTurnSource{Kind: "message", SourceID: e.ids.New(), SourceVersion: 1},
		WorkerID:    worker,
		Scope:       e.scope,
		RequesterID: e.ids.New(),
	})
	var body turnBody
	e.decode(payload.Data, &body)
	e.mustOK(opWorkClaim, workClaimInput{
		WorkID: body.Resource.ID, ExpectedVersion: body.Resource.Version, Generation: e.generation(),
	})
	return e.readTurn(body.Resource.ID)
}

// prepareContext calls _execution.context.prepare and returns the loaded
// plan row.
func (e *testEnv) prepareContext(t *turnRow) *contextPlanRow {
	e.t.Helper()
	payload := e.mustOK(opContextPrepare, contextPrepareInput{
		TurnID: t.ID, ExpectedVersion: t.Version, Generation: t.Generation,
	})
	var body contextPlanBody
	e.decode(payload.Data, &body)
	return e.readContextPlan(body.Resource.ID)
}

// seedRecordedProposal directly inserts a recorded proposal carrying a
// result artifact and effect operation id -- simulating a model tool call
// P16's interpretation stage already recorded evidence for.
func (e *testEnv) seedRecordedProposal(turnID contract.ID, stepIndex int64, proposalID string, result wireArtifactRef, operationID contract.ID) *proposalRow {
	e.t.Helper()
	row := &proposalRow{
		ID:                  e.ids.New(),
		InstallationID:      e.install,
		TurnID:              turnID,
		StepIndex:           stepIndex,
		ProposalID:          proposalID,
		SourceContextDigest: fixtureDigest,
		NormalizedProposal:  json.RawMessage(`{"kind":"report_outputs","bindings":[]}`),
		State:               "recorded",
		CreatedAt:           e.clock.Now(),
		UpdatedAt:           e.clock.Now(),
		EffectOperationID:   operationID,
		ResultArtifact:      &result,
	}
	e.inWrite(func(unit contract.Unit) error {
		return insertProposal(e.ctx, unit, row)
	})
	return row
}

// contextPartKind reads just the "kind" discriminator of one ContextPart.
func contextPartKind(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("decode context part kind: %v", err)
	}
	return probe.Kind
}

// TestContextCommitProducesSchemaValidOrderedContextAndModelStepAction
// proves the core P15 behavior: prepare/stage/commit build a real
// zatiti.context/v1 document and a real zatiti.responses.action/v1
// model_step action, both schema-valid against the frozen adapter
// definitions, and a controlled provider consuming the staged bytes
// observes the intended transcript order -- worker instructions, then the
// authorized inbox, then a prior tool result -- with real (never
// profile-as-tool, never hardcoded) resolved Tool/Connection refs.
func TestContextCommitProducesSchemaValidOrderedContextAndModelStepAction(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	profile := fixtureHostedProfile(worker)
	e.installWorkerSnapshot(worker, profile)
	toolID, connID := e.installModelToolBinding(worker, profile)

	e.ports.setMessages(worker, []wireMessage{{
		ID: e.ids.New(), Version: 1, SenderID: e.ids.New(), RecipientIDs: []contract.ID{worker},
		Scope: e.scope, TaskIDs: []contract.ID{}, Body: "please help with this",
		Attachments: []wireArtifactRef{}, State: "admitted", CreatedAt: "2026-09-10T12:00:00.000000000Z",
	}})

	turn := e.admitClaimedTurn(worker)
	toolResult := wireArtifactRef{ID: e.ids.New(), Digest: digestA}
	e.seedRecordedProposal(turn.ID, 0, "call-1", toolResult, e.ids.New())
	e.ports.artifacts[digestA] = wireArtifact{
		ID: toolResult.ID, Version: 1, Scope: e.scope, Digest: digestA,
		Size: int64(len(fixtureContent)), MediaType: "application/octet-stream",
		Classification: "internal", State: "available", CreatedAt: formatStamp(e.clock.Now()),
	}

	plan := e.prepareContext(turn)
	locator, raw, err := e.svc.stageContext(e.ctx, turn, plan)
	if err != nil {
		t.Fatalf("stageContext: %v", err)
	}
	if locator.Kind != "artifact" || locator.Artifact == nil {
		t.Fatalf("stageContext returned locator %+v, want a published artifact", locator)
	}

	// The produced context passes the real, frozen zatiti.context/v1 schema.
	schema, err := contextArtifactSchema()
	if err != nil {
		t.Fatalf("contextArtifactSchema: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		t.Fatalf("assembled context fails the real adapter schema: %v", err)
	}

	commitPayload := e.mustOK(opContextCommit, contextCommitInput{
		PlanID: plan.ID, ExpectedVersion: plan.ExpectedVersion, Generation: plan.Generation, StagedContext: locator,
	})
	var committed turnBody
	e.decode(commitPayload.Data, &committed)
	if committed.Resource.State != "model_pending" {
		t.Fatalf("committed turn state %q, want model_pending", committed.Resource.State)
	}

	// A controlled provider decodes the staged bytes and observes the
	// intended ordered messages: instructions, then inbox, then the prior
	// tool result -- each exactly once.
	var doc wireContextArtifact
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode staged context: %v", err)
	}
	if len(doc.Messages) != 3 {
		t.Fatalf("staged context carries %d messages, want 3 (instruction, inbox, tool_result): %s", len(doc.Messages), raw)
	}
	if doc.Messages[0].Role != "developer" || doc.Messages[0].Origin != "effective_instruction" {
		t.Fatalf("message[0] = %+v, want the developer/effective_instruction worker instructions first", doc.Messages[0])
	}
	if contextPartKind(t, doc.Messages[0].Parts[0]) != "text" {
		t.Fatalf("message[0] part kind %q, want text", contextPartKind(t, doc.Messages[0].Parts[0]))
	}
	if doc.Messages[1].Role != "user" || doc.Messages[1].Origin != "user_message" {
		t.Fatalf("message[1] = %+v, want the untrusted user_message inbox item second", doc.Messages[1])
	}
	if doc.Messages[2].Role != "tool" || doc.Messages[2].Origin != "tool_result" {
		t.Fatalf("message[2] = %+v, want the prior tool_result last", doc.Messages[2])
	}
	toolResultCount := 0
	for _, m := range doc.Messages {
		for _, part := range m.Parts {
			if contextPartKind(t, part) == "tool_result" {
				var p wireContextToolResultPart
				if err := json.Unmarshal(part, &p); err != nil {
					t.Fatalf("decode tool_result part: %v", err)
				}
				if p.Artifact.ID == toolResult.ID {
					toolResultCount++
				}
			}
		}
	}
	if toolResultCount != 1 {
		t.Fatalf("tool result %s appears %d times in the context, want exactly once", toolResult.ID, toolResultCount)
	}

	// The context also carries the resolved model-dispatch tool (never the
	// worker's own execution profile identity) plus the four sealed local
	// decision tools.
	foundModelTool := false
	for _, tool := range doc.Tools {
		if tool.Tool.ID == toolID {
			foundModelTool = true
			if tool.Tool.ID == profile.ID {
				t.Fatalf("model tool ref reuses the execution profile identity (profile-as-tool)")
			}
		}
	}
	if !foundModelTool {
		t.Fatalf("context tools %+v do not carry the resolved model-dispatch tool %s", doc.Tools, toolID)
	}
	localNames := map[string]bool{}
	for _, tool := range doc.Tools {
		localNames[tool.Name] = true
	}
	for _, want := range []string{
		contract.LocalDecisionToolReply, contract.LocalDecisionToolClarify,
		contract.LocalDecisionToolReportOutputs, contract.LocalDecisionToolCycleDecision,
	} {
		if !localNames[want] {
			t.Fatalf("context tools do not carry the sealed local decision tool %q", want)
		}
	}

	// Construct the exact Responses model_step action fields once a session
	// handle is known, and confirm it too passes the real adapter schema.
	action, parameters, toolRef, connectionRef, account, err := buildResponsesModelStepAction(
		plan, *locator.Artifact, "session-handle-1", "")
	if err != nil {
		t.Fatalf("buildResponsesModelStepAction: %v", err)
	}
	if action.Schema != "zatiti.responses.action/v1" || action.Kind != "model_step" {
		t.Fatalf("action %+v does not carry the frozen schema/kind discriminator", action)
	}
	if action.ContextArtifact.ID != locator.Artifact.ID || action.ContextArtifact.Digest != locator.Artifact.Digest {
		t.Fatalf("action context_artifact %+v does not name the committed context %+v", action.ContextArtifact, *locator.Artifact)
	}
	if toolRef.ID != toolID {
		t.Fatalf("resolved tool ref %s, want the connections-resolved tool %s (not profile-as-tool)", toolRef.ID, toolID)
	}
	if connectionRef.ID != connID {
		t.Fatalf("resolved connection ref %s, want the worker's bound connection %s", connectionRef.ID, connID)
	}
	if account == "" {
		t.Fatalf("resolved account identity is empty")
	}
	actionSchema, err := responsesModelStepSchema()
	if err != nil {
		t.Fatalf("responsesModelStepSchema: %v", err)
	}
	actionRaw, err := json.Marshal(parameters)
	if err != nil {
		t.Fatalf("marshal action parameters: %v", err)
	}
	if err := contract.ValidateSchema(actionSchema, actionRaw); err != nil {
		t.Fatalf("assembled model_step action fails the real adapter schema: %v", err)
	}
}

// TestContextPrepareRefusesBeforeAnyNetworkIO proves the three named
// refusal conditions -- missing instruction bytes, stale pinned
// configuration and an out-of-scope memory binding -- refuse
// _execution.context.prepare before any bytes are staged or published: the
// turn never leaves its pre-prepare state, and stageContext is never
// reached.
func TestContextPrepareRefusesBeforeAnyNetworkIO(t *testing.T) {
	t.Run("missing instruction bytes", func(t *testing.T) {
		e := newEnv(t)
		worker := e.ids.New()
		profile := fixtureHostedProfile(worker)
		e.installWorkerSnapshot(worker, profile)
		e.ports.snapshots[e.install].Worker.Instructions = ""
		turn := e.admitClaimedTurn(worker)

		f := e.expectFault(opContextPrepare, contextPrepareInput{
			TurnID: turn.ID, ExpectedVersion: turn.Version, Generation: turn.Generation,
		}, contract.CodePrerequisiteMissing)
		if !strings.Contains(f.Message, "instruction") {
			t.Fatalf("fault %q does not name the missing instruction bytes", f.Message)
		}
		if after := e.readTurn(turn.ID); after.State != "claimed" {
			t.Fatalf("turn state %q after refused prepare, want claimed (never advanced)", after.State)
		}
	})

	t.Run("stale config", func(t *testing.T) {
		e := newEnv(t)
		worker := e.ids.New()
		profile := fixtureHostedProfile(worker)
		e.installWorkerSnapshot(worker, profile)
		turn := e.admitClaimedTurn(worker)

		// Configuration moves on after the turn pinned revision 1.
		e.ports.snapshots[e.install].Revision = 2

		f := e.expectFault(opContextPrepare, contextPrepareInput{
			TurnID: turn.ID, ExpectedVersion: turn.Version, Generation: turn.Generation,
		}, contract.CodeStaleVersion)
		if !strings.Contains(f.Message, "revision") {
			t.Fatalf("fault %q does not name the stale configuration revision", f.Message)
		}
		if after := e.readTurn(turn.ID); after.State != "claimed" {
			t.Fatalf("turn state %q after refused prepare, want claimed (never advanced)", after.State)
		}
	})

	t.Run("out-of-scope memory", func(t *testing.T) {
		e := newEnv(t)
		worker := e.ids.New()
		profile := fixtureHostedProfile(worker)
		e.installWorkerSnapshot(worker, profile)
		snap := e.ports.snapshots[e.install]
		memoryBindingID := e.ids.New()
		snap.Worker.Bindings = append(snap.Worker.Bindings, memoryBindingID)
		snap.Bindings = append(snap.Bindings, wireBinding{
			ID: memoryBindingID, Version: 1, Scope: e.scope, Kind: "memory",
			TargetID: e.ids.New(), Permissions: []string{"read"},
		})
		e.ports.setFault(peerMemorySelect, &contract.Fault{
			Code: contract.CodePermissionDenied, Message: "brain is outside the worker's authorized scope",
		})
		turn := e.admitClaimedTurn(worker)

		f := e.expectFault(opContextPrepare, contextPrepareInput{
			TurnID: turn.ID, ExpectedVersion: turn.Version, Generation: turn.Generation,
		}, contract.CodePermissionDenied)
		if !strings.Contains(f.Message, "scope") {
			t.Fatalf("fault %q does not describe the out-of-scope memory refusal", f.Message)
		}
		if after := e.readTurn(turn.ID); after.State != "claimed" {
			t.Fatalf("turn state %q after refused prepare, want claimed (never advanced)", after.State)
		}
	})
}

// TestContextAdvanceKeepsToolResultOnceAndPriorContextBytesImmutable proves
// P15's advance/immutability rule: a tool result folded into a context
// appears exactly once per build, and a context already staged and
// published stays byte-for-byte readable, unmutated, after a later context
// is built for the same turn.
func TestContextAdvanceKeepsToolResultOnceAndPriorContextBytesImmutable(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	profile := fixtureHostedProfile(worker)
	e.installWorkerSnapshot(worker, profile)
	turn := e.admitClaimedTurn(worker)

	toolResult := wireArtifactRef{ID: e.ids.New(), Digest: digestA}
	e.seedRecordedProposal(turn.ID, 0, "call-1", toolResult, e.ids.New())
	e.ports.artifacts[digestA] = wireArtifact{
		ID: toolResult.ID, Version: 1, Scope: e.scope, Digest: digestA,
		Size: int64(len(fixtureContent)), MediaType: "application/octet-stream",
		Classification: "internal", State: "available", CreatedAt: formatStamp(e.clock.Now()),
	}

	planA := e.prepareContext(turn)
	locatorA, rawA, err := e.svc.stageContext(e.ctx, turn, planA)
	if err != nil {
		t.Fatalf("stageContext (A): %v", err)
	}
	countA := countToolResultOccurrences(t, rawA, toolResult.ID)
	if countA != 1 {
		t.Fatalf("first context carries the tool result %d times, want exactly once", countA)
	}

	// A second build (the turn is still context_pending -- a rebuild before
	// commit, exactly as a retry after a discarded stale plan would do)
	// must still carry the same tool result exactly once, and must never
	// touch the bytes already staged for the first plan. The first prepare
	// moved the turn to context_pending and bumped its version; re-read it
	// before preparing again.
	turn = e.readTurn(turn.ID)
	planB := e.prepareContext(turn)
	if planB.ID == planA.ID {
		t.Fatalf("second prepare returned the same plan id; plans must be distinct/immutable")
	}
	locatorB, rawB, err := e.svc.stageContext(e.ctx, turn, planB)
	if err != nil {
		t.Fatalf("stageContext (B): %v", err)
	}
	countB := countToolResultOccurrences(t, rawB, toolResult.ID)
	if countB != 1 {
		t.Fatalf("second context carries the tool result %d times, want exactly once", countB)
	}

	// Re-read the first published artifact's bytes directly from the blob
	// store: they must still be exactly what was staged, unaffected by the
	// second build.
	rc, err := e.blobsOpen(locatorA.Artifact.Digest, int64(len(rawA)))
	if err != nil {
		t.Fatalf("re-open the first staged context: %v", err)
	}
	defer func() { _ = rc.Close() }()
	reread, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read the first staged context: %v", err)
	}
	if string(reread) != string(rawA) {
		t.Fatalf("the first context's published bytes changed after a later context was built")
	}
	if locatorA.Artifact.Digest == locatorB.Artifact.Digest && string(rawA) != string(rawB) {
		t.Fatalf("two different documents published under the same digest")
	}
}

// countToolResultOccurrences counts how many tool_result parts in raw name
// artifact id.
func countToolResultOccurrences(t *testing.T, raw []byte, id contract.ID) int {
	t.Helper()
	var doc wireContextArtifact
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	count := 0
	for _, m := range doc.Messages {
		for _, part := range m.Parts {
			if contextPartKind(t, part) != "tool_result" {
				continue
			}
			var p wireContextToolResultPart
			if err := json.Unmarshal(part, &p); err != nil {
				t.Fatalf("decode tool_result part: %v", err)
			}
			if p.Artifact.ID == id {
				count++
			}
		}
	}
	return count
}

// blobsOpen opens the test env's underlying blob store directly, outside
// any unit, exactly as stageContext itself does.
func (e *testEnv) blobsOpen(digest contract.Digest, maxLen int64) (io.ReadCloser, error) {
	return e.svc.deps.Blobs.Open(e.ctx, digest, 0, maxLen)
}
