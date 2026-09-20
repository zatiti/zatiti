package application

// Behavioral tests for ExecuteWorker (contract.WorkerOperator, P00-003).
// These register fake descriptors under the exact real catalog operation
// ids ("grant.create", "review.decide", "task.delegate", ...) so the
// allowlist and scope checks under test exercise the same strings the real
// registry would present, while the surrounding authority/policy/evidence
// behavior comes from the same fake owners every other test in this package
// already proves against (Z01/Z04/Z10/Z16/Z21).

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// workerOpsHarness registers a small set of fake operations under real
// catalog operation ids and records every invocation that actually reached
// a handler, so a denial can be proven to have happened before dispatch,
// not merely asserted.
type workerOpsHarness struct {
	reached   map[string]int
	seenActor contract.Actor
	seenScope contract.Scope
}

// scopedFakeSchema is the input schema every fake worker operation shares:
// an explicit scope object plus an inert note field. It intentionally
// mirrors the shape every real catalog operation's input carries (a
// top-level "scope") without depending on any real operation's exact
// schema.
var scopedFakeSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["scope"],
	"properties":{"scope":{"type":"object","additionalProperties":false,"required":["installation_id"],
		"properties":{"installation_id":{"type":"string"},"organization_id":{"type":"string"},
			"project_id":{"type":"string"},"worker_id":{"type":"string"},"task_id":{"type":"string"}}},
		"note":{"type":"string"}}}`)

// registerWorkerOp adds one fake descriptor to e's catalog under a real
// catalog operation id and returns a harness that records reached calls.
func newWorkerOpsHarness(e *testEnv) *workerOpsHarness {
	h := &workerOpsHarness{reached: map[string]int{}}
	handlerFor := func(id string) contract.Handler {
		return func(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
			h.reached[id]++
			h.seenActor = u.Actor()
			h.seenScope = u.Scope()
			if _, err := u.ExecContext(ctx, "INSERT INTO sibling_log (note) VALUES (?)", id); err != nil {
				return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
			}
			return payloadJSON(map[string]any{"ran": id})
		}
	}
	register := func(id, visibility, mode string, submissionKey bool) {
		e.cat.ops[id] = fakeOp{
			desc: contract.Descriptor{
				ID: id, Version: 1, Owner: "worker-fixture", Visibility: visibility,
				Mode: mode, SubmissionKey: submissionKey, InputSchema: scopedFakeSchema,
				OutputSchema: json.RawMessage(`{"type":"object"}`),
			},
			h: handlerFor(id),
		}
	}
	// An allowlisted operation: the positive control every denial test is
	// contrasted against.
	register("task.delegate", contract.VisibilityPublic, contract.ModeMutation, true)
	// Named explicitly by the card and by P00.worker_operation_denies_unlisted_operation:
	// public, real-shaped, but never on the worker-visible allowlist.
	register("grant.create", contract.VisibilityPublic, contract.ModeMutation, true)
	register("review.decide", contract.VisibilityPublic, contract.ModeMutation, true)
	// An internal owner operation: never reachable regardless of allowlist
	// contents, because it is not public at all.
	register("_internal.bookkeeping", contract.VisibilityInternal, contract.ModeMutation, false)
	return h
}

// workerScopedInput builds one fake operation's input: an explicit scope
// plus a note, matching scopedFakeSchema.
func workerScopedInput(installation, worker contract.ID, note string) json.RawMessage {
	return mustMarshal(map[string]any{
		"scope": map[string]any{"installation_id": installation, "worker_id": worker},
		"note":  note,
	})
}

const (
	workerPrincipalA = "40000000-0000-4000-8000-000000000100"
	workerPrincipalB = "40000000-0000-4000-8000-000000000200"
	otherWorkerA     = "40000000-0000-4000-8000-000000000900"
)

// ---- required behavioral test 1: allowlist denial and human-required refusal ----

// TestWorkerOperatorDeniesOperationsOutsideTheAllowlist proves the P00-003
// contract: a malicious worker proposal naming grant.create, review.decide
// or an internal owner operation is refused before its handler ever runs,
// while an allowlisted operation for the same worker succeeds under that
// worker's own actor -- never the controller's administrative identity.
func TestWorkerOperatorDeniesOperationsOutsideTheAllowlist(t *testing.T) {
	e := newTestEnv(t)
	h := newWorkerOpsHarness(e)
	ctx := context.Background()

	e.identity.seed(ctx, e.db, e.install, workerPrincipalA, contract.KindWorker)
	scope := contract.Scope{InstallationID: e.install, WorkerID: workerPrincipalA}

	deny := func(t *testing.T, name, operation string) {
		t.Helper()
		req := contract.WorkerRequest{
			TurnID: "50000000-0000-4000-8000-000000000001", ProposalID: "malicious-" + name,
			WorkerID: workerPrincipalA, Scope: scope, Operation: operation,
			Input: workerScopedInput(e.install, workerPrincipalA, "malicious "+name),
		}
		_, err := e.app.ExecuteWorker(ctx, req)
		_ = requireFault(t, err, contract.CodePermissionDenied)
		if h.reached[operation] != 0 {
			t.Fatalf("%s handler was reached (%d times); the allowlist must refuse before dispatch",
				operation, h.reached[operation])
		}
	}
	t.Run("grant.create", func(t *testing.T) { deny(t, "grant", "grant.create") })
	t.Run("review.decide", func(t *testing.T) { deny(t, "review", "review.decide") })
	t.Run("internal_owner_operation", func(t *testing.T) { deny(t, "internal", "_internal.bookkeeping") })

	// An allowlisted operation (task.delegate) submitted the same way for
	// the same worker succeeds under that worker's own intersected scope.
	allowed := contract.WorkerRequest{
		TurnID: "50000000-0000-4000-8000-000000000001", ProposalID: "legitimate-delegate",
		WorkerID: workerPrincipalA, Scope: scope, Operation: "task.delegate",
		Input: workerScopedInput(e.install, workerPrincipalA, "legitimate"),
	}
	res, err := e.app.ExecuteWorker(ctx, allowed)
	if err != nil {
		t.Fatalf("allowlisted task.delegate: %v", err)
	}
	if res.Status != contract.StatusCompleted {
		t.Fatalf("allowlisted task.delegate status %q, want completed", res.Status)
	}
	if h.reached["task.delegate"] != 1 {
		t.Fatalf("task.delegate handler reached %d times, want 1", h.reached["task.delegate"])
	}
	if h.seenActor.PrincipalID != workerPrincipalA || h.seenActor.Kind != contract.KindWorker {
		t.Fatalf("task.delegate ran as actor %+v, want the worker's own actor (never a substituted identity)",
			h.seenActor)
	}
}

// TestWorkerOperatorRefusesAHumanRequiredDecision proves the second half of
// the same required behavior: even an allowlisted operation always refuses
// once policy demands a human decision, because a worker proposal can never
// itself satisfy that review (revision-3 ruling: services, workers and
// agents are never eligible reviewers).
func TestWorkerOperatorRefusesAHumanRequiredDecision(t *testing.T) {
	e := newTestEnv(t)
	h := newWorkerOpsHarness(e)
	ctx := context.Background()

	e.identity.seed(ctx, e.db, e.install, workerPrincipalA, contract.KindWorker)
	e.policy.setRule(ctx, e.db, e.install, "task.delegate", "review", nil)
	scope := contract.Scope{InstallationID: e.install, WorkerID: workerPrincipalA}

	req := contract.WorkerRequest{
		TurnID: "50000000-0000-4000-8000-000000000002", ProposalID: "needs-a-human",
		WorkerID: workerPrincipalA, Scope: scope, Operation: "task.delegate",
		Input: workerScopedInput(e.install, workerPrincipalA, "needs a human decision"),
	}
	if _, err := e.app.ExecuteWorker(ctx, req); requireFault(t, err, contract.CodeReviewRequired) == nil {
		t.Fatal("expected a review_required fault")
	}
	if h.reached["task.delegate"] != 0 {
		t.Fatalf("handler reached %d times for a human-required decision, want 0", h.reached["task.delegate"])
	}

	// A second, distinct proposal cannot route around the same standing
	// requirement either.
	req2 := req
	req2.ProposalID = "needs-a-human-again"
	req2.Input = workerScopedInput(e.install, workerPrincipalA, "second attempt")
	if _, err := e.app.ExecuteWorker(ctx, req2); requireFault(t, err, contract.CodeReviewRequired) == nil {
		t.Fatal("expected a review_required fault on retry")
	}
	if h.reached["task.delegate"] != 0 {
		t.Fatalf("handler reached %d times after retry, want still 0", h.reached["task.delegate"])
	}
}

// ---- required behavioral test 2: crash after commit then redeliver ----

// TestWorkerOperatorCrashRedeliverReplaysOneMutation proves that redelivering
// the identical (turn, proposal) after a crash recovers the original command
// result instead of executing the domain mutation a second time. reopenEnv
// closes and reopens the same database file: durable state (the evidence
// command, the identity principal, the domain row) survives; the in-memory
// handler-invocation counter does not, so a nonzero count after redelivery
// can only mean the handler actually ran again.
func TestWorkerOperatorCrashRedeliverReplaysOneMutation(t *testing.T) {
	e := newTestEnv(t)
	h := newWorkerOpsHarness(e)
	ctx := context.Background()

	e.identity.seed(ctx, e.db, e.install, workerPrincipalA, contract.KindWorker)
	scope := contract.Scope{InstallationID: e.install, WorkerID: workerPrincipalA}
	req := contract.WorkerRequest{
		TurnID: "50000000-0000-4000-8000-000000000003", ProposalID: "crash-proposal",
		WorkerID: workerPrincipalA, Scope: scope, Operation: "task.delegate",
		Input: workerScopedInput(e.install, workerPrincipalA, "before crash"),
	}

	first, err := e.app.ExecuteWorker(ctx, req)
	if err != nil {
		t.Fatalf("first ExecuteWorker: %v", err)
	}
	if h.reached["task.delegate"] != 1 {
		t.Fatalf("handler reached %d times before crash, want 1", h.reached["task.delegate"])
	}

	e2 := reopenEnv(t, e)
	h2 := newWorkerOpsHarness(e2)

	again, err := e2.app.ExecuteWorker(ctx, req)
	if err != nil {
		t.Fatalf("redelivered ExecuteWorker: %v", err)
	}
	if again.CommandID != first.CommandID || string(again.Data) != string(first.Data) {
		t.Fatalf("redelivered result %+v does not replay the original %+v", again.Payload, first.Payload)
	}
	if h2.reached["task.delegate"] != 0 {
		t.Fatalf("redelivery re-executed the handler (%d times); one domain mutation must never become two",
			h2.reached["task.delegate"])
	}
	if got := len(e2.readColumn(t, "sibling_log", "note", "seq")); got != 1 {
		t.Fatalf("sibling_log has %d rows after redelivery, want exactly 1 domain mutation", got)
	}

	// A different proposal for the same turn is a different command
	// entirely: it must run for real, not join the first proposal's replay.
	distinct := req
	distinct.ProposalID = "crash-proposal-2"
	distinct.Input = workerScopedInput(e.install, workerPrincipalA, "a second, distinct proposal")
	if _, err := e2.app.ExecuteWorker(ctx, distinct); err != nil {
		t.Fatalf("distinct proposal after redelivery: %v", err)
	}
	if h2.reached["task.delegate"] != 1 {
		t.Fatalf("distinct proposal handler reached %d times, want 1", h2.reached["task.delegate"])
	}
}

// ---- required behavioral test 3: pause/revoke observed by the real dispatcher ----

// TestWorkerOperatorObservesRevocationAndPolicyPauseBetweenProposals proves
// that a revocation or policy pause recorded after a proposal was authored,
// and before the next admission, is observed live by the real dispatcher
// a.Invoke already runs on every call -- not by any state WorkerOperator
// caches locally across proposals.
func TestWorkerOperatorObservesRevocationAndPolicyPauseBetweenProposals(t *testing.T) {
	e := newTestEnv(t)
	h := newWorkerOpsHarness(e)
	ctx := context.Background()

	e.identity.seed(ctx, e.db, e.install, workerPrincipalB, contract.KindWorker)
	scope := contract.Scope{InstallationID: e.install, WorkerID: workerPrincipalB}
	makeReq := func(proposal string) contract.WorkerRequest {
		return contract.WorkerRequest{
			TurnID: "50000000-0000-4000-8000-000000000004", ProposalID: proposal,
			WorkerID: workerPrincipalB, Scope: scope, Operation: "task.delegate",
			Input: workerScopedInput(e.install, workerPrincipalB, proposal),
		}
	}

	// Baseline: the worker is current, so its first proposal admits.
	if _, err := e.app.ExecuteWorker(ctx, makeReq("before-revoke")); err != nil {
		t.Fatalf("baseline proposal: %v", err)
	}
	if h.reached["task.delegate"] != 1 {
		t.Fatalf("baseline handler reached %d times, want 1", h.reached["task.delegate"])
	}

	// Revoke between proposal authoring and this next admission.
	e.identity.setRevoked(ctx, e.db, e.install, workerPrincipalB, true)
	if _, err := e.app.ExecuteWorker(ctx, makeReq("after-revoke")); requireFault(t, err, contract.CodePermissionDenied) == nil {
		t.Fatal("expected permission_denied after revocation")
	}
	if h.reached["task.delegate"] != 1 {
		t.Fatalf("handler reached %d times after revocation, want still 1 (never re-dispatched)",
			h.reached["task.delegate"])
	}

	// Restore the worker and pause the operation through policy instead: a
	// deny rule recorded between proposal and admission is likewise
	// observed by the real dispatcher's own policy gate.
	e.identity.setRevoked(ctx, e.db, e.install, workerPrincipalB, false)
	e.policy.setRule(ctx, e.db, e.install, "task.delegate", "deny", []string{"paused"})
	if _, err := e.app.ExecuteWorker(ctx, makeReq("after-pause")); requireFault(t, err, contract.CodePermissionDenied) == nil {
		t.Fatal("expected permission_denied after policy pause")
	}
	if h.reached["task.delegate"] != 1 {
		t.Fatalf("handler reached %d times after policy pause, want still 1", h.reached["task.delegate"])
	}

	// Lifting the pause restores admission for a fresh proposal.
	e.policy.setRule(ctx, e.db, e.install, "task.delegate", "", nil)
	if _, err := e.app.ExecuteWorker(ctx, makeReq("after-resume")); err != nil {
		t.Fatalf("proposal after resume: %v", err)
	}
	if h.reached["task.delegate"] != 2 {
		t.Fatalf("handler reached %d times after resume, want 2", h.reached["task.delegate"])
	}
}

// ---- additional coverage: scope intersection and actor resolution ----

// TestWorkerOperatorRefusesScopeOutsideTheEnvelope proves the scope
// intersection P00-003 requires: an allowlisted operation whose own
// declared scope names a different worker than the proposal's authorization
// envelope is refused, even though the operation itself is permitted.
func TestWorkerOperatorRefusesScopeOutsideTheEnvelope(t *testing.T) {
	e := newTestEnv(t)
	h := newWorkerOpsHarness(e)
	ctx := context.Background()

	e.identity.seed(ctx, e.db, e.install, workerPrincipalA, contract.KindWorker)
	req := contract.WorkerRequest{
		TurnID: "50000000-0000-4000-8000-000000000005", ProposalID: "scope-escape",
		WorkerID:  workerPrincipalA,
		Scope:     contract.Scope{InstallationID: e.install, WorkerID: workerPrincipalA},
		Operation: "task.delegate",
		Input:     workerScopedInput(e.install, otherWorkerA, "targets a different worker than the envelope authorizes"),
	}
	if _, err := e.app.ExecuteWorker(ctx, req); requireFault(t, err, contract.CodePermissionDenied) == nil {
		t.Fatal("expected permission_denied for an out-of-envelope scope")
	}
	if h.reached["task.delegate"] != 0 {
		t.Fatalf("handler reached %d times for an out-of-envelope scope, want 0", h.reached["task.delegate"])
	}
}

// TestWorkerOperatorRequiresAnAssertedWorkerScopeMatch proves WorkerID is an
// asserted match against the request's own scope, never a free choice: a
// request whose scope names a different worker than its own WorkerID is
// refused before the allowlist, identity or the ordinary dispatcher ever
// run.
func TestWorkerOperatorRequiresAnAssertedWorkerScopeMatch(t *testing.T) {
	e := newTestEnv(t)
	h := newWorkerOpsHarness(e)
	ctx := context.Background()

	e.identity.seed(ctx, e.db, e.install, workerPrincipalA, contract.KindWorker)
	req := contract.WorkerRequest{
		TurnID: "50000000-0000-4000-8000-000000000006", ProposalID: "mismatched-scope-worker",
		WorkerID:  workerPrincipalA,
		Scope:     contract.Scope{InstallationID: e.install, WorkerID: otherWorkerA},
		Operation: "task.delegate",
		Input:     workerScopedInput(e.install, workerPrincipalA, "scope names a different worker"),
	}
	if _, err := e.app.ExecuteWorker(ctx, req); requireFault(t, err, contract.CodePermissionDenied) == nil {
		t.Fatal("expected permission_denied for a worker/scope mismatch")
	}
	if h.reached["task.delegate"] != 0 {
		t.Fatalf("handler reached %d times for a worker/scope mismatch, want 0", h.reached["task.delegate"])
	}
}

// TestWorkerOperatorRejectsAnUnknownOrNonWorkerPrincipal proves actor
// resolution goes through identity, never through caller-supplied data: an
// unknown WorkerID and a real principal of the wrong kind are both refused.
func TestWorkerOperatorRejectsAnUnknownOrNonWorkerPrincipal(t *testing.T) {
	e := newTestEnv(t)
	h := newWorkerOpsHarness(e)
	ctx := context.Background()

	unknown := contract.ID("40000000-0000-4000-8000-000000000999")
	unknownReq := contract.WorkerRequest{
		TurnID: "50000000-0000-4000-8000-000000000007", ProposalID: "unknown-worker",
		WorkerID: unknown, Scope: contract.Scope{InstallationID: e.install, WorkerID: unknown},
		Operation: "task.delegate",
		Input:     workerScopedInput(e.install, unknown, "no such principal"),
	}
	if _, err := e.app.ExecuteWorker(ctx, unknownReq); requireFault(t, err, contract.CodePermissionDenied) == nil {
		t.Fatal("expected permission_denied for an unknown worker principal")
	}

	human := contract.ID(testPrincipal) // seeded as KindHuman by newTestEnv
	humanReq := contract.WorkerRequest{
		TurnID: "50000000-0000-4000-8000-000000000007", ProposalID: "human-as-worker",
		WorkerID: human, Scope: contract.Scope{InstallationID: e.install, WorkerID: human},
		Operation: "task.delegate",
		Input:     workerScopedInput(e.install, human, "a human principal is not a worker"),
	}
	if _, err := e.app.ExecuteWorker(ctx, humanReq); requireFault(t, err, contract.CodePermissionDenied) == nil {
		t.Fatal("expected permission_denied for a non-worker principal")
	}
	if h.reached["task.delegate"] != 0 {
		t.Fatalf("handler reached %d times for an invalid actor, want 0", h.reached["task.delegate"])
	}
}
