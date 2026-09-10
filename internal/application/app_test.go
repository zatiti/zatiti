package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// ---- Z01.revoked_principal ----

// A revoked principal is denied at the very next dispatch (revalidation, not
// just authentication), the denial is durable across a controller restart,
// and the deterministic refusal is retained for replay.
func TestZ01RevokedPrincipal(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	ctx := context.Background()
	_, ioBase, _ := e.io.calls() // bootstrap baseline

	res, err := e.invoke(t, e.actor(), "business.apply", "z01-before", map[string]any{
		"name": "z01-item", "expected_version": 0,
	})
	if err != nil {
		t.Fatalf("apply before revocation: %v", err)
	}
	if res.Status != contract.StatusCompleted {
		t.Fatalf("apply status %q, want completed", res.Status)
	}

	e.identity.setRevoked(ctx, e.db, e.install, testPrincipal, true)

	_, err = e.invoke(t, e.actor(), "business.apply", "z01-after", map[string]any{
		"name": "z01-item", "expected_version": 1,
	})
	f := requireFault(t, err, contract.CodePermissionDenied)
	if !strings.Contains(f.Message, "revoked") {
		t.Fatalf("revocation fault message %q does not name revocation", f.Message)
	}

	// Authority revalidation precedes replay: a revoked principal cannot
	// even recover its own prior success from the retained disposition.
	_, err = e.invoke(t, e.actor(), "business.apply", "z01-before", map[string]any{
		"name": "z01-item", "expected_version": 0,
	})
	_ = requireFault(t, err, contract.CodePermissionDenied)

	// Revocation survives a controller restart.
	e2 := reopenEnv(t, e)
	if _, err := e2.invoke(t, e2.actor(), "business.apply", "z01-restart", map[string]any{
		"name": "z01-item", "expected_version": 1,
	}); err == nil {
		t.Fatal("revocation was not durable across restart")
	} else {
		_ = requireFault(t, err, contract.CodePermissionDenied)
	}

	// Nothing ran under the revoked principal and no state changed.
	if _, perform, _ := e.io.calls(); perform != ioBase {
		t.Fatalf("local IO ran %d times over baseline %d under a revoked principal", perform, ioBase)
	}
	if got := len(e2.readColumn(t, "business_journal", "note", "seq")); got != 0 {
		t.Fatalf("unexpected journal rows: %d", got)
	}
}

// ---- Z04.concurrent_apply ----

// Ten concurrent mutations of the same resource serialize on the ordered
// writer: exactly one activates, the rest are refused whole, and no partial
// state or lost event remains.
func TestZ04ConcurrentApply(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	const workers = 10
	type outcome struct {
		res contract.Result
		err error
	}
	results := make(chan outcome, workers)
	var start sync.WaitGroup
	start.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			start.Done()
			start.Wait() // release all goroutines together
			res, err := e.app.Invoke(ctx, e.actor(), "business.apply", contract.Request{
				Schema:        contract.SchemaRequest,
				SubmissionKey: fmt.Sprintf("z04-concurrent-%d", i),
				Input: mustMarshal(map[string]any{
					"name": "z04-item", "expected_version": 0,
				}),
			})
			results <- outcome{res, err}
		}(i)
	}

	var succeeded, stale int
	for i := 0; i < workers; i++ {
		out := <-results
		if out.err == nil {
			succeeded++
			if out.res.Status != contract.StatusCompleted {
				t.Fatalf("winner status %q, want completed", out.res.Status)
			}
			continue
		}
		f := faultOf(out.err)
		switch f.Code {
		case contract.CodeStaleVersion, contract.CodePermissionDenied, contract.CodeConflict:
			stale++
		default:
			t.Fatalf("worker refused with unexpected fault %s: %s", f.Code, f.Message)
		}
	}
	if succeeded != 1 {
		t.Fatalf("exactly one concurrent apply must activate, got %d", succeeded)
	}
	if stale != workers-1 {
		t.Fatalf("want %d whole refusals, got %d", workers-1, stale)
	}

	var version int
	if err := e.db.Read(ctx, e.serviceActor(), contract.Scope{InstallationID: e.install},
		func(u contract.Unit) error {
			return u.QueryRowContext(ctx, "SELECT version FROM business_items WHERE name = 'z04-item'").Scan(&version)
		}); err != nil {
		t.Fatalf("read winner: %v", err)
	}
	if version != 1 {
		t.Fatalf("item version %d, want exactly 1", version)
	}
	if got := e.eventCount(t); got != 2 { // bootstrap + the one winner
		t.Fatalf("event count %d, want bootstrap plus one apply event", got)
	}
}

// ---- Z04.submission_replay ----

// An identical retry returns the original disposition before any
// stale-version validation runs and without re-executing; a changed input on
// the same submission identity conflicts; the identity and disposition
// survive a restart.
func TestZ04SubmissionReplay(t *testing.T) {
	e := newTestEnv(t)
	input := map[string]any{"name": "z04-replayed", "expected_version": 0}

	res, err := e.invoke(t, e.actor(), "business.apply", "z04-replay", input)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if got := e.business.executions.Load(); got != 1 {
		t.Fatalf("executions %d, want 1", got)
	}

	// Identical retry: the retained disposition replays even though the
	// handler's version check would now refuse it (item is at version 1).
	replay, err := e.invoke(t, e.actor(), "business.apply", "z04-replay", input)
	if err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	if replay.CommandID != res.CommandID || replay.Status != res.Status {
		t.Fatalf("replay envelope differs: %+v vs %+v", replay.Payload, res.Payload)
	}
	if string(replay.Data) != string(res.Data) {
		t.Fatalf("replay data %s, want original %s", replay.Data, res.Data)
	}
	if got := e.business.executions.Load(); got != 1 {
		t.Fatalf("identical retry re-executed the handler (%d executions)", got)
	}

	// A changed input on the same submission identity conflicts at the
	// atomic begin boundary; the original disposition stands.
	_, err = e.invoke(t, e.actor(), "business.apply", "z04-replay", map[string]any{
		"name": "z04-replayed", "expected_version": 1,
	})
	_ = requireFault(t, err, contract.CodeSubmissionConflict)
	if got := e.business.executions.Load(); got != 1 {
		t.Fatalf("conflicting retry executed the handler (%d executions)", got)
	}
	if res2 := e.commandGet(t, "business.apply", "z04-replay"); res2 == nil ||
		res2.Status != contract.StatusCompleted || res2.ID != string(res.CommandID) {
		t.Fatalf("original command was disturbed by the conflict: %+v", res2)
	}

	// The submission identity and its disposition survive a restart.
	e2 := reopenEnv(t, e)
	again, err := e2.invoke(t, e2.actor(), "business.apply", "z04-replay", input)
	if err != nil {
		t.Fatalf("replay after restart: %v", err)
	}
	if again.CommandID != res.CommandID {
		t.Fatalf("post-restart replay command %s, want %s", again.CommandID, res.CommandID)
	}
	if got := e2.business.executions.Load(); got != 0 {
		t.Fatalf("post-restart replay re-executed the handler (%d executions)", got)
	}
}

// commandGet reads one retained command through the public evidence query.
func (e *testEnv) commandGet(t *testing.T, operation, key string) *fakeCommand {
	t.Helper()
	res, err := e.invoke(t, e.serviceActor(), "command.get", "", map[string]any{
		"operation": operation, "submission_key": key,
	})
	if err != nil {
		return nil
	}
	var out struct {
		Resource fakeCommand `json:"resource"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("decode command.get: %v", err)
	}
	return &out.Resource
}

// ---- Z10.lost_claim_ack ----

// Repeating a claim with the same submission identity recovers the original
// disposition without a duplicate attempt; a fresh identity for the same
// worker is refused whole and retained.
func TestZ10LostClaimAck(t *testing.T) {
	e := newTestEnv(t)

	res, err := e.invoke(t, e.actor(), "business.claim", "z10-claim", map[string]any{"worker": "w1"})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	// The ack was lost in transit: the client repeats with the same
	// submission identity and must recover the original disposition.
	again, err := e.invoke(t, e.actor(), "business.claim", "z10-claim", map[string]any{"worker": "w1"})
	if err != nil {
		t.Fatalf("claim re-acknowledgement: %v", err)
	}
	if again.CommandID != res.CommandID || string(again.Data) != string(res.Data) {
		t.Fatalf("claim re-ack differs: %+v vs %+v", again.Payload, res.Payload)
	}

	// A new submission identity for the same worker hits the owner's
	// conflict and the deterministic refusal is retained.
	_, err = e.invoke(t, e.actor(), "business.claim", "z10-dup", map[string]any{"worker": "w1"})
	_ = requireFault(t, err, contract.CodeConflict)
	dup := e.commandGet(t, "business.claim", "z10-dup")
	if dup == nil || dup.Status != contract.StatusFailed || dup.ErrorCode != contract.CodeConflict {
		t.Fatalf("duplicate claim refusal not retained: %+v", dup)
	}

	// Recovery after restart replays the original acknowledgement.
	e2 := reopenEnv(t, e)
	recovered, err := e2.invoke(t, e2.actor(), "business.claim", "z10-claim", map[string]any{"worker": "w1"})
	if err != nil {
		t.Fatalf("claim recovery after restart: %v", err)
	}
	if recovered.CommandID != res.CommandID {
		t.Fatalf("recovered claim %s, want %s", recovered.CommandID, res.CommandID)
	}
	if got := e2.readColumn(t, "business_attempts", "id", "id"); len(got) != 1 {
		t.Fatalf("attempt rows %d, want exactly 1 (no duplicate)", len(got))
	}
}

// ---- Z16.disconnect_command_lookup ----

// After a disconnect, the client recovers the retained disposition by
// submission key without re-running anything; request identities are not
// submission keys.
func TestZ16DisconnectCommandLookup(t *testing.T) {
	e := newTestEnv(t)

	res, err := e.invoke(t, e.actor(), "business.apply", "z16-key", map[string]any{
		"name": "z16-item", "expected_version": 0,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	e2 := reopenEnv(t, e)

	// Recover the disposition through the evidence owner by submission key.
	lookup, err := e2.invoke(t, e2.actor(), "command.get", "", map[string]any{
		"operation": "business.apply", "submission_key": "z16-key",
	})
	if err != nil {
		t.Fatalf("command lookup after disconnect: %v", err)
	}
	var retained struct {
		Resource fakeCommand `json:"resource"`
	}
	if err := json.Unmarshal(lookup.Data, &retained); err != nil {
		t.Fatalf("decode command.get: %v", err)
	}
	if retained.Resource.ID != string(res.CommandID) || retained.Resource.Status != contract.StatusCompleted {
		t.Fatalf("retained command %+v does not match the original disposition", retained.Resource)
	}
	if retained.Resource.Result == nil ||
		string(retained.Resource.Result.Data) != string(res.Data) {
		t.Fatalf("retained result envelope %v does not replay the original %s",
			retained.Resource.Result, res.Data)
	}

	// Retrying with the original submission identity neither re-executes
	// nor duplicates.
	replay, err := e2.invoke(t, e2.actor(), "business.apply", "z16-key", map[string]any{
		"name": "z16-item", "expected_version": 0,
	})
	if err != nil {
		t.Fatalf("replay after disconnect: %v", err)
	}
	if replay.CommandID != res.CommandID {
		t.Fatalf("replay command %s, want %s", replay.CommandID, res.CommandID)
	}
	if got := e2.business.executions.Load(); got != 0 {
		t.Fatalf("post-disconnect replay re-executed (%d executions)", got)
	}

	// A request identity is not a submission identity: looking the command
	// id up as a submission key finds nothing.
	_, err = e2.invoke(t, e2.actor(), "command.get", "", map[string]any{
		"operation": "business.apply", "submission_key": string(res.CommandID),
	})
	_ = requireFault(t, err, contract.CodeNotFound)
}

// ---- Z21.reconnect_no_duplicate ----

// A client that reconnects and resubmits with the original submission
// identity produces no duplicate tasks or effects.
func TestZ21ReconnectNoDuplicate(t *testing.T) {
	e := newTestEnv(t)

	res, err := e.invoke(t, e.actor(), "business.apply", "z21-key", map[string]any{
		"name": "z21-item", "expected_version": 0,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	before := e.eventCount(t)

	e2 := reopenEnv(t, e)
	replay, err := e2.invoke(t, e2.actor(), "business.apply", "z21-key", map[string]any{
		"name": "z21-item", "expected_version": 0,
	})
	if err != nil {
		t.Fatalf("resubmit after reconnect: %v", err)
	}
	if replay.CommandID != res.CommandID || replay.Status != contract.StatusCompleted {
		t.Fatalf("resubmission envelope %+v, want original %+v", replay.Payload, res.Payload)
	}
	if got := e2.business.executions.Load(); got != 0 {
		t.Fatalf("resubmission created a duplicate execution (%d)", got)
	}
	if got := e2.eventCount(t); got != before {
		t.Fatalf("resubmission changed the event log: %d events, want %d", got, before)
	}
	if got := len(e2.readColumn(t, "business_items", "name", "name")); got != 1 {
		t.Fatalf("resubmission duplicated the item: %d rows", got)
	}
}

// ---- Z14.atomic_state_event ----

// Fault injection proves neither owner state without its event nor an event
// without state ever commits: the whole transaction, evidence identity
// included, rolls back, and only deterministic refusals are re-recorded.
func TestZ14AtomicStateEvent(t *testing.T) {
	cases := []struct {
		name        string
		stage       string
		wantCode    string
		wantRefusal bool // deterministic refusals are retained; crash faults are not
	}{
		{"error before emit", "before_emit", contract.CodeConflict, true},
		{"failed payload", "conflict", contract.CodeConflict, true},
		{"handler panic", "panic", contract.CodeInternalError, false},
		{"error after emit", "after_emit", contract.CodeConflict, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			baseline := e.eventCount(t)

			_, err := e.invoke(t, e.actor(), "business.failing", "z14-"+tc.stage, map[string]any{
				"stage": tc.stage,
			})
			_ = requireFault(t, err, tc.wantCode)

			// Neither state nor event survived.
			if got := len(e.readColumn(t, "business_journal", "note", "seq")); got != 0 {
				t.Fatalf("state survived the fault: %d journal rows", got)
			}
			if got := e.eventCount(t); got != baseline {
				t.Fatalf("event survived the fault: %d events, want %d", got, baseline)
			}

			// Retained disposition follows refusal determinism.
			cmd := e.commandGet(t, "business.failing", "z14-"+tc.stage)
			if tc.wantRefusal {
				if cmd == nil || cmd.Status != contract.StatusFailed || cmd.ErrorCode != tc.wantCode {
					t.Fatalf("deterministic refusal not retained: %+v", cmd)
				}
				replay, rErr := e.invoke(t, e.actor(), "business.failing", "z14-"+tc.stage, map[string]any{
					"stage": tc.stage,
				})
				if rErr != nil {
					t.Fatalf("refusal replay: %v", rErr)
				}
				if replay.Error == nil || replay.Error.Code != tc.wantCode {
					t.Fatalf("replayed refusal lost its fault: %+v", replay.Payload)
				}
			} else {
				if cmd != nil {
					t.Fatalf("non-deterministic crash fault was retained as a refusal: %+v", cmd)
				}
			}
		})
	}
}

// ---- cross-owner rollback ----

// A nested internal call that fails rolls the whole transaction back across
// every owner boundary: the caller's writes and the callee's writes vanish
// together.
func TestCrossOwnerRollback(t *testing.T) {
	e := newTestEnv(t)
	baseline := e.eventCount(t)

	_, err := e.invoke(t, e.actor(), "business.crossfail", "cross-1", map[string]any{"note": "boom"})
	_ = requireFault(t, err, contract.CodeConflict)

	if got := len(e.readColumn(t, "business_journal", "note", "seq")); got != 0 {
		t.Fatalf("caller state survived rollback: %d rows", got)
	}
	if got := len(e.readColumn(t, "sibling_log", "note", "seq")); got != 0 {
		t.Fatalf("callee state survived the caller's rollback: %d rows", got)
	}
	if got := e.eventCount(t); got != baseline {
		t.Fatalf("event survived rollback: %d events, want %d", got, baseline)
	}
}

// ---- internal entry point ----

// The Internal entry admits only service principals, only internal
// operations allowlisting the controller, only inside this installation's
// scope — and it revalidates authority like every other entry.
func TestInternalEntryPoint(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	scope := contract.Scope{InstallationID: e.install}

	// Happy path: the controller claims internal work and it commits.
	payload, err := e.app.Internal(ctx, e.serviceActor(), scope, contract.Invocation{
		Operation: "sibling.job", Input: mustMarshal(map[string]any{}),
	})
	if err != nil {
		t.Fatalf("internal job: %v", err)
	}
	if payload.Status != contract.StatusCompleted {
		t.Fatalf("internal job status %q", payload.Status)
	}
	if got := len(e.readColumn(t, "sibling_log", "note", "seq")); got != 1 {
		t.Fatalf("internal job wrote %d rows, want 1", got)
	}

	// Only a service principal may enter.
	_, err = e.app.Internal(ctx, e.actor(), scope, contract.Invocation{Operation: "sibling.job"})
	_ = requireFault(t, err, contract.CodePermissionDenied)

	// The explicit scope must name this installation.
	_, err = e.app.Internal(ctx, e.serviceActor(), contract.Scope{InstallationID: "99900000-0000-4000-8000-000000000099"},
		contract.Invocation{Operation: "sibling.job"})
	_ = requireFault(t, err, contract.CodePermissionDenied)

	// Empty scope is invalid, never an implicit global.
	_, err = e.app.Internal(ctx, e.serviceActor(), contract.Scope{}, contract.Invocation{Operation: "sibling.job"})
	_ = requireFault(t, err, contract.CodeInvalidInput)

	// Public operations are not internal dispatch targets.
	_, err = e.app.Internal(ctx, e.serviceActor(), scope, contract.Invocation{Operation: "business.query"})
	_ = requireFault(t, err, contract.CodePermissionDenied)

	// Internal operations the controller is not allowlisted for stay closed.
	_, err = e.app.Internal(ctx, e.serviceActor(), scope, contract.Invocation{Operation: "sibling.step"})
	_ = requireFault(t, err, contract.CodePermissionDenied)

	// A pinned version the descriptor does not carry is refused.
	_, err = e.app.Internal(ctx, e.serviceActor(), scope, contract.Invocation{
		Operation: "sibling.job", Version: 99,
	})
	_ = requireFault(t, err, contract.CodeCapabilityUnsupported)

	// Revocation denies the next internal dispatch too.
	e.identity.setRevoked(ctx, e.db, e.install, servicePrincipal, true)
	_, err = e.app.Internal(ctx, e.serviceActor(), scope, contract.Invocation{Operation: "sibling.job"})
	_ = requireFault(t, err, contract.CodePermissionDenied)
}

// ---- port allowlists and dispatch guards ----

// Internal calls carry the caller's fixed identity, honor the descriptor
// allowlist (empty never authorizes), join the caller's transaction exactly,
// reject reentrant public dispatch before storage, fail closed on operation
// loops and depth overflow, and cannot mutate under a read snapshot.
func TestPortGuards(t *testing.T) {
	t.Run("empty allowlist never authorizes", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.badcall", "guard-badcall", map[string]any{})
		f := requireFault(t, err, contract.CodePermissionDenied)
		if !strings.Contains(f.Message, "does not allow caller business") {
			t.Fatalf("allowlist fault message %q", f.Message)
		}
	})

	t.Run("nil unit is an invalid call", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.nilcall", "guard-nil", map[string]any{})
		_ = requireFault(t, err, contract.CodeInvalidInput)
	})

	t.Run("retained unit cannot join a new transaction", func(t *testing.T) {
		e := newTestEnv(t)
		if _, err := e.invoke(t, e.actor(), "business.peek", "guard-retain", map[string]any{}); err != nil {
			t.Fatalf("peek: %v", err)
		}
		_, err := e.invoke(t, e.actor(), "business.stalecall", "guard-stale", map[string]any{})
		f := requireFault(t, err, contract.CodeInternalError)
		if !strings.Contains(f.Message, "join the current transaction unit") {
			t.Fatalf("same-unit fault message %q", f.Message)
		}
	})

	t.Run("nested call sees the caller actor scope and generation", func(t *testing.T) {
		e := newTestEnv(t)
		res, err := e.invoke(t, e.actor(), "business.peek", "guard-echo", map[string]any{})
		if err != nil {
			t.Fatalf("peek: %v", err)
		}
		if e.sibling.seenActor.PrincipalID != testPrincipal || e.sibling.seenActor.Kind != contract.KindHuman {
			t.Fatalf("nested call actor %+v, want the caller", e.sibling.seenActor)
		}
		if e.sibling.seenScope.InstallationID != e.install {
			t.Fatalf("nested call scope %+v, want the caller scope", e.sibling.seenScope)
		}
		if e.sibling.seenGen == 0 {
			t.Fatal("nested call saw generation 0")
		}
		if res.Status != contract.StatusCompleted {
			t.Fatalf("echo status %q", res.Status)
		}
	})

	t.Run("query cannot invoke a mutation", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.peekmut", "", map[string]any{})
		_ = requireFault(t, err, contract.CodePermissionDenied)
	})

	t.Run("reentrant public dispatch is forbidden before storage", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.reentrant", "guard-reent", map[string]any{"target": "invoke"})
		f := requireFault(t, err, contract.CodeInternalError)
		if !strings.Contains(f.Message, "reentrant public dispatch is forbidden") {
			t.Fatalf("reentrancy fault message %q", f.Message)
		}
		if got := len(e.readColumn(t, "evidence_commands", "id", "id")); got != 0 {
			t.Fatalf("reentrant dispatch reached storage: %d command rows", got)
		}
	})

	t.Run("reentrant internal dispatch is forbidden", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.reentrant", "guard-reent-i", map[string]any{"target": "internal"})
		f := requireFault(t, err, contract.CodeInternalError)
		if !strings.Contains(f.Message, "reentrant dispatch is forbidden") {
			t.Fatalf("reentrancy fault message %q", f.Message)
		}
	})

	t.Run("recursive operation loop fails closed", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.loop", "guard-loop", map[string]any{})
		f := requireFault(t, err, contract.CodeInternalError)
		if !strings.Contains(f.Message, "recursive operation loop detected") {
			t.Fatalf("cycle fault message %q", f.Message)
		}
	})

	t.Run("version pinning rejects unsupported versions", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.versioned", "guard-ver", map[string]any{})
		_ = requireFault(t, err, contract.CodeCapabilityUnsupported)
	})

	t.Run("dispatch depth overflows closed", func(t *testing.T) {
		e := newTestEnv(t)
		chainPorts := e.router.For("chain")
		for i := 0; i < 40; i++ {
			id := fmt.Sprintf("chain.%02d", i)
			next := fmt.Sprintf("chain.%02d", i+1)
			e.cat.ops[id] = fakeOp{
				desc: contract.Descriptor{ID: id, Version: 1, Owner: "chain",
					Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation,
					Callers: []string{"business", "chain"}},
				h: func(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
					if _, err := chainPorts.Call(ctx, u, contract.Invocation{Operation: next, Input: []byte(`{}`)}); err != nil {
						return contract.Payload{}, err
					}
					return payloadJSON(map[string]any{"stepped": id})
				},
			}
		}
		e.cat.ops["business.deep"] = fakeOp{
			desc: contract.Descriptor{ID: "business.deep", Version: 1, Owner: "business",
				Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, SubmissionKey: true,
				InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)},
			h: func(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
				if _, err := chainPorts.Call(ctx, u, contract.Invocation{Operation: "chain.00", Input: []byte(`{}`)}); err != nil {
					return contract.Payload{}, err
				}
				return payloadJSON(map[string]any{"deep": true})
			},
		}
		_, err := e.invoke(t, e.actor(), "business.deep", "guard-depth", map[string]any{})
		f := requireFault(t, err, contract.CodeInternalError)
		if !strings.Contains(f.Message, "internal dispatch depth exceeded") {
			t.Fatalf("depth fault message %q", f.Message)
		}
	})
}

// ---- local IO routing ----

// The registered local IO operations route through Prepare/Perform/Finish:
// the accepted disposition is durable before local work runs, Perform never
// runs inside a transaction, concurrent same-key calls join instead of
// duplicating Perform, and faults finish as inspectable failed results.
func TestLocalIOMutationLifecycle(t *testing.T) {
	t.Run("happy path and terminal replay", func(t *testing.T) {
		e := newTestEnv(t)
		// The bootstrap ran through the same IO service; every phase assertion
		// below reads the delta over that baseline.
		basePrep, basePerform, baseFinish := e.io.calls()
		res, err := e.invoke(t, e.actor(), "artifact.upload.finish", "io-1", map[string]any{})
		if err != nil {
			t.Fatalf("io mutation: %v", err)
		}
		if res.Status != contract.StatusCompleted {
			t.Fatalf("io status %q, want completed", res.Status)
		}
		prep, perform, finish := e.io.calls()
		if prep != basePrep+1 || perform != basePerform+1 || finish != baseFinish+1 {
			t.Fatalf("phase calls prepare=%d perform=%d finish=%d, want baseline %d/%d/%d plus 1",
				prep, perform, finish, basePrep, basePerform, baseFinish)
		}
		if e.io.sawOpenTx.Load() {
			t.Fatal("Perform ran inside an open transaction")
		}

		replay, err := e.invoke(t, e.actor(), "artifact.upload.finish", "io-1", map[string]any{})
		if err != nil {
			t.Fatalf("io replay: %v", err)
		}
		if replay.CommandID != res.CommandID || replay.Status != contract.StatusCompleted {
			t.Fatalf("io replay envelope %+v, want original %+v", replay.Payload, res.Payload)
		}
		if _, p, _ := e.io.calls(); p != basePerform+1 {
			t.Fatalf("io replay ran Perform again (%d calls over baseline %d)", p, basePerform)
		}
	})

	t.Run("concurrent same-key calls join the accepted command", func(t *testing.T) {
		e := newTestEnv(t)
		_, basePerform, _ := e.io.calls() // bootstrap baseline
		e.io.gate = make(chan struct{})
		var releaseGate sync.Once
		release := func() { releaseGate.Do(func() { close(e.io.gate) }) }
		defer release()

		done := make(chan contract.Result, 1)
		errCh := make(chan error, 1)
		go func() {
			res, err := e.app.Invoke(context.Background(), e.actor(), "artifact.upload.finish", contract.Request{
				Schema: contract.SchemaRequest, SubmissionKey: "io-join", Input: []byte(`{}`),
			})
			done <- res
			errCh <- err
		}()

		<-e.io.started // first call is inside Perform, holding the gate
		joined, err := e.app.Invoke(context.Background(), e.actor(), "artifact.upload.finish", contract.Request{
			Schema: contract.SchemaRequest, SubmissionKey: "io-join", Input: []byte(`{}`),
		})
		if err != nil {
			t.Fatalf("join call: %v", err)
		}
		if joined.Status != contract.StatusAccepted {
			t.Fatalf("join status %q, want the accepted disposition", joined.Status)
		}
		release()

		res := <-done
		if err := <-errCh; err != nil {
			t.Fatalf("first call: %v", err)
		}
		if res.Status != contract.StatusCompleted {
			t.Fatalf("first call status %q, want completed", res.Status)
		}
		if joined.CommandID != res.CommandID {
			t.Fatalf("join command %s, want the in-progress command %s", joined.CommandID, res.CommandID)
		}
		if _, p, _ := e.io.calls(); p != basePerform+1 {
			t.Fatalf("Perform ran %d times over baseline %d, want exactly 1", p, basePerform)
		}
	})

	t.Run("perform fault finishes as a failed result", func(t *testing.T) {
		e := newTestEnv(t)
		e.io.failPerform = true
		res, err := e.invoke(t, e.actor(), "artifact.upload.finish", "io-fault", map[string]any{})
		if err != nil {
			t.Fatalf("io fault must finish as a failed result, got error: %v", err)
		}
		if res.Status != contract.StatusFailed || res.Error.Code != contract.CodeArtifactFault {
			t.Fatalf("io fault envelope %+v, want failed artifact_fault", res.Payload)
		}
		e.io.failPerform = false
		replay, err := e.invoke(t, e.actor(), "artifact.upload.finish", "io-fault", map[string]any{})
		if err != nil {
			t.Fatalf("faulted replay: %v", err)
		}
		if replay.Status != contract.StatusFailed || replay.CommandID != res.CommandID {
			t.Fatalf("faulted replay envelope %+v, want retained %+v", replay.Payload, res.Payload)
		}
	})

	t.Run("perform panic becomes an internal fault", func(t *testing.T) {
		e := newTestEnv(t)
		e.io.panicMask.Store(true)
		res, err := e.invoke(t, e.actor(), "artifact.upload.finish", "io-panic", map[string]any{})
		if err != nil {
			t.Fatalf("io panic must become a failed result, got error: %v", err)
		}
		if res.Status != contract.StatusFailed || res.Error.Code != contract.CodeInternalError {
			t.Fatalf("io panic envelope %+v, want failed internal_error", res.Payload)
		}
	})

	t.Run("unrouted io operation fails with a precise internal error", func(t *testing.T) {
		e := newTestEnv(t)
		e.cat.ops["artifact.upload.cancel"] = fakeOp{
			desc: contract.Descriptor{ID: "artifact.upload.cancel", Version: 1, Owner: "artifacts",
				Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, SubmissionKey: true,
				InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)},
		}
		_, err := e.invoke(t, e.actor(), "artifact.upload.cancel", "io-unrouted", map[string]any{})
		f := requireFault(t, err, contract.CodeInternalError)
		if !strings.Contains(f.Message, "has no owning LocalIO service") {
			t.Fatalf("unrouted io fault message %q", f.Message)
		}
	})

	t.Run("io query performs outside transactions and rechecks authority", func(t *testing.T) {
		e := newTestEnv(t)
		res, err := e.invoke(t, e.actor(), "artifact.read", "", map[string]any{})
		if err != nil {
			t.Fatalf("io query: %v", err)
		}
		if res.Status != contract.StatusCompleted {
			t.Fatalf("io query status %q", res.Status)
		}
		if e.io.sawOpenTx.Load() {
			t.Fatal("io query Perform ran inside a transaction")
		}
		if res.CommandID == "" {
			t.Fatal("io query returned no command identity")
		}

		e.identity.setRevoked(context.Background(), e.db, e.install, testPrincipal, true)
		_, err = e.invoke(t, e.actor(), "artifact.read", "", map[string]any{})
		_ = requireFault(t, err, contract.CodePermissionDenied)
	})
}

// ---- bootstrap ----

func TestBootstrap(t *testing.T) {
	t.Run("uninitialized controller admits only bootstrap", func(t *testing.T) {
		e := openEnv(t, t.TempDir(), 0)
		_, err := e.invoke(t, e.actor(), "business.apply", "boot-early", map[string]any{
			"name": "x", "expected_version": 0,
		})
		f := requireFault(t, err, contract.CodePermissionDenied)
		if !strings.Contains(f.Message, "installation is not initialized") {
			t.Fatalf("uninitialized fault message %q", f.Message)
		}
		// Authentication is refused too: no installation identity exists.
		_, err = e.app.Authenticate(context.Background(), []byte(testCredential))
		_ = requireFault(t, err, contract.CodePermissionDenied)
	})

	t.Run("a mutation without a submission key must be the bootstrap", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.nosub", "", map[string]any{})
		f := requireFault(t, err, contract.CodeInternalError)
		if !strings.Contains(f.Message, "not the registered bootstrap") {
			t.Fatalf("bootstrap routing fault message %q", f.Message)
		}
	})

	t.Run("failed bootstrap leaves the controller uninitialized", func(t *testing.T) {
		e := openEnv(t, t.TempDir(), 0)
		e.io.failPerform = true
		_, err := e.app.Invoke(context.Background(), e.serviceActor(), "installation.init", contract.Request{
			Schema: contract.SchemaRequest,
			Input:  mustMarshal(map[string]any{"credential_store": "os"}),
		})
		_ = requireFault(t, err, contract.CodeArtifactFault)
		if installationIDForTest(e.app) != "" {
			t.Fatal("failed bootstrap remembered an installation identity")
		}
		if got := e.eventCount(t); got != 0 {
			t.Fatalf("failed bootstrap emitted %d events", got)
		}

		// The exclusive lock retries the bootstrap and succeeds.
		e.io.failPerform = false
		bootstrapInit(t, e)
		if got := e.eventCount(t); got != 1 {
			t.Fatalf("recovered bootstrap emitted %d events, want 1", got)
		}
	})
}

// ---- envelope and admission validation ----

func TestAdmissionValidation(t *testing.T) {
	t.Run("internal operations are never disclosed through public routing", func(t *testing.T) {
		e := newTestEnv(t)
		for _, op := range []string{"_identity.authority", "_policy.check", "_evidence.command.begin", "sibling.echo", "business.nope"} {
			if _, err := e.invoke(t, e.actor(), op, "", map[string]any{}); err == nil {
				t.Fatalf("public dispatch of %q succeeded", op)
			} else {
				_ = requireFault(t, err, contract.CodeNotFound)
			}
		}
	})

	t.Run("request envelope schema is enforced", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.app.Invoke(context.Background(), e.actor(), "business.query", contract.Request{
			Schema: "zatiti.request/v0", Input: []byte(`{}`),
		})
		_ = requireFault(t, err, contract.CodeInvalidInput)
	})

	t.Run("input schema is enforced", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.apply", "adm-schema", map[string]any{"name": "x"})
		_ = requireFault(t, err, contract.CodeInvalidInput)
	})

	t.Run("submission key wire rules", func(t *testing.T) {
		e := newTestEnv(t)
		input := map[string]any{"name": "x", "expected_version": 0}
		if _, err := e.invoke(t, e.actor(), "business.apply", "", input); err == nil {
			t.Fatal("empty submission key accepted")
		} else {
			_ = requireFault(t, err, contract.CodeInvalidInput)
		}
		if _, err := e.invoke(t, e.actor(), "business.apply", strings.Repeat("k", 129), input); err == nil {
			t.Fatal("oversized submission key accepted")
		} else {
			_ = requireFault(t, err, contract.CodeInvalidInput)
		}
		if _, err := e.invoke(t, e.actor(), "business.apply", "bad\x01key", input); err == nil {
			t.Fatal("non-printable submission key accepted")
		} else {
			_ = requireFault(t, err, contract.CodeInvalidInput)
		}
	})

	t.Run("request size limit", func(t *testing.T) {
		e := newTestEnv(t)
		big := `{"pad":"` + strings.Repeat("a", 1<<20+64) + `"}`
		_, err := e.app.Invoke(context.Background(), e.actor(), "business.query", contract.Request{
			Schema: contract.SchemaRequest, Input: []byte(big),
		})
		f := requireFault(t, err, contract.CodeInvalidInput)
		if !strings.Contains(f.Message, "exceeds") {
			t.Fatalf("size fault message %q", f.Message)
		}
	})

	t.Run("queries do not create durable commands", func(t *testing.T) {
		e := newTestEnv(t)
		res, err := e.invoke(t, e.actor(), "business.query", "", map[string]any{})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if res.Schema != contract.SchemaResult {
			t.Fatalf("query envelope schema %q", res.Schema)
		}
		if got := len(e.readColumn(t, "evidence_commands", "id", "id")); got != 0 {
			t.Fatalf("query created %d durable commands", got)
		}
	})
}

// ---- authentication entry ----

func TestAuthenticationEntry(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	actor, err := e.app.Authenticate(ctx, []byte(testCredential))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if actor.PrincipalID != testPrincipal || actor.Kind != contract.KindHuman {
		t.Fatalf("authenticated actor %+v", actor)
	}

	if _, err := e.app.Authenticate(ctx, []byte("nope")); err == nil {
		t.Fatal("unknown credential authenticated")
	} else {
		_ = requireFault(t, err, contract.CodePermissionDenied)
	}

	fp := contract.Digest("aa-bb-cc")
	e.auth.certs[fp] = e.actor()
	certActor, err := e.app.AuthenticateCertificate(ctx, fp)
	if err != nil {
		t.Fatalf("certificate authentication: %v", err)
	}
	if certActor.PrincipalID != testPrincipal {
		t.Fatalf("certificate actor %+v", certActor)
	}
	if _, err := e.app.AuthenticateCertificate(ctx, ""); err == nil {
		t.Fatal("empty fingerprint accepted")
	} else {
		_ = requireFault(t, err, contract.CodeInvalidInput)
	}

	e.app.Close()
	_, err = e.app.Invoke(ctx, e.actor(), "business.query", contract.Request{Schema: contract.SchemaRequest, Input: []byte(`{}`)})
	f := requireFault(t, err, contract.CodeControllerUnavailable)
	if !f.Retryable {
		t.Fatal("shutdown fault must be retryable")
	}
	if _, err := e.app.Authenticate(ctx, []byte(testCredential)); err == nil {
		t.Fatal("authenticate admitted after close")
	}
}

// ---- scope derivation ----

func TestScopeDerivation(t *testing.T) {
	t.Run("explicit scope resolves", func(t *testing.T) {
		e := newTestEnv(t)
		res, err := e.invoke(t, e.actor(), "business.scoped", "", map[string]any{
			"scope": map[string]any{"installation_id": string(e.install)},
		})
		if err != nil {
			t.Fatalf("scoped query: %v", err)
		}
		if res.Status != contract.StatusCompleted {
			t.Fatalf("scoped status %q", res.Status)
		}
	})

	t.Run("foreign scope is refused", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.scoped", "", map[string]any{
			"scope": map[string]any{"installation_id": "88800000-0000-4000-8000-000000000088"},
		})
		f := requireFault(t, err, contract.CodePermissionDenied)
		if !strings.Contains(f.Message, "outside this controller") {
			t.Fatalf("foreign scope fault message %q", f.Message)
		}
	})

	t.Run("scope required but missing is invalid", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.invoke(t, e.actor(), "business.scoped", "", map[string]any{
			"scope": map[string]any{},
		})
		_ = requireFault(t, err, contract.CodeInvalidInput)
	})
}

// ---- policy gate decisions ----

func TestPolicyGate(t *testing.T) {
	ctx := context.Background()
	input := map[string]any{"name": "policy-item", "expected_version": 0}

	t.Run("deny refuses and is retained", func(t *testing.T) {
		e := newTestEnv(t)
		e.policy.setRule(ctx, e.db, e.install, "business.apply", "deny", []string{"not allowed here"})
		_, err := e.invoke(t, e.actor(), "business.apply", "policy-deny", input)
		f := requireFault(t, err, contract.CodePermissionDenied)
		if !strings.Contains(f.Message, "not allowed here") {
			t.Fatalf("policy denial lost its reason: %q", f.Message)
		}
		cmd := e.commandGet(t, "business.apply", "policy-deny")
		if cmd == nil || cmd.ErrorCode != contract.CodePermissionDenied {
			t.Fatalf("policy refusal not retained: %+v", cmd)
		}
	})

	t.Run("review demands its requirement", func(t *testing.T) {
		e := newTestEnv(t)
		e.policy.setRule(ctx, e.db, e.install, "business.apply", "review", []string{"needs a human"})
		_, err := e.invoke(t, e.actor(), "business.apply", "policy-review", input)
		f := requireFault(t, err, contract.CodeReviewRequired)
		if !strings.Contains(f.Message, "needs a human") {
			t.Fatalf("review fault lost its reason: %q", f.Message)
		}
		if len(f.Details) == 0 {
			t.Fatal("review fault carries no requirement details")
		}
		var details struct {
			Reasons []string `json:"reasons"`
		}
		if err := json.Unmarshal(f.Details, &details); err != nil {
			t.Fatalf("review details are not decodable: %v", err)
		}
		if len(details.Reasons) == 0 || details.Reasons[0] != "needs a human" {
			t.Fatalf("review details reasons %v", details.Reasons)
		}
	})

	t.Run("prerequisite missing maps to its code", func(t *testing.T) {
		e := newTestEnv(t)
		e.policy.setRule(ctx, e.db, e.install, "business.apply", "prerequisite_missing", []string{"install first"})
		_, err := e.invoke(t, e.actor(), "business.apply", "policy-prereq", input)
		_ = requireFault(t, err, contract.CodePrerequisiteMissing)
	})

	t.Run("unknown decision fails closed", func(t *testing.T) {
		e := newTestEnv(t)
		e.policy.setRule(ctx, e.db, e.install, "business.apply", "maybe", []string{})
		_, err := e.invoke(t, e.actor(), "business.apply", "policy-unknown", input)
		_ = requireFault(t, err, contract.CodeInternalError)
		if got := len(e.readColumn(t, "evidence_commands", "id", "id")); got != 0 {
			t.Fatalf("failed-closed decision recorded %d commands", got)
		}
	})
}

// ---- constructor and ports seams ----

func TestConstructorValidation(t *testing.T) {
	e := newTestEnv(t) // for valid fakes
	if _, err := New(nil, e.cat, e.auth, e.clock, e.ids); err == nil {
		t.Fatal("nil database accepted")
	}
	if _, err := New(e.db, nil, e.auth, e.clock, e.ids); err == nil {
		t.Fatal("nil registry accepted")
	}
	if _, err := New(e.db, e.cat, nil, e.clock, e.ids); err == nil {
		t.Fatal("nil authenticator accepted")
	}
	if _, err := New(e.db, e.cat, e.auth, nil, e.ids); err == nil {
		t.Fatal("nil clock accepted")
	}
	if _, err := New(e.db, e.cat, e.auth, e.clock, nil); err == nil {
		t.Fatal("nil id source accepted")
	}
}

func TestUnboundPortsReject(t *testing.T) {
	fresh := NewPorts()
	if _, err := fresh.For("business").Call(context.Background(), nil, contract.Invocation{}); err == nil {
		t.Fatal("unbound ports accepted a call")
	} else if f := faultOf(err); f.Code != contract.CodeInternalError {
		t.Fatalf("unbound ports fault %s", f.Code)
	}
	if _, err := fresh.For("").Call(context.Background(), nil, contract.Invocation{}); err == nil {
		t.Fatal("empty-owner ports accepted a call")
	}
}
