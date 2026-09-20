package client

// Revision 3 regression coverage: worker/task lifecycle command recovery
// (task.start, review.decide), the unsupported-server-version boundary, the
// four mutually distinguishable disposition states, cursor recovery that
// never replays a write, and the public Lookup/Poll reconnect helpers this
// card adds. Named acceptance cases Z16/Z21/JOURNEY are proven in
// acceptance_test.go; this file proves the specific behaviors P38 requires.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// --- worker/task lifecycle regression fixtures -----------------------------

const (
	testTaskID   = "00000000-0000-4000-8000-0000000000t1"
	testReviewID = "00000000-0000-4000-8000-0000000000d1"
)

// taskStartInput and reviewDecideInput render the frozen task.start and
// review.decide input shapes so the lost-acknowledgement regressions below
// exercise the exact operations that ready a task's run and record a human
// review decision, not a stand-in operation name.
func taskStartInput(taskID string) string {
	return `{"scope":{"installation_id":"` + testInstallation + `"},"id":"` + taskID + `","expected_version":1}`
}

func reviewDecideInput(reviewID string) string {
	return `{"scope":{"installation_id":"` + testInstallation + `"},"id":"` + reviewID +
		`","expected_version":1,"action_digest":"` + strings.Repeat("a", 64) + `","decision":"approve","reason":"looks correct"}`
}

// TestLostTaskStartAcknowledgementResolvesOriginalCommand is the card's
// named worker-lifecycle regression: task.start readies a task and enqueues
// its run in one transaction. A lost acknowledgement must recover that SAME
// command through the client's identical-replay and command-lookup
// recovery, never ready and enqueue a second run.
func TestLostTaskStartAcknowledgementResolvesOriginalCommand(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "task-start-001"
	const committed = "00000000-0000-4000-8000-0000000000c1"
	fc.script("task.start", dropAndCommit(key, committed,
		`{"task":{"id":"`+testTaskID+`","status":"ready"},"run":{"id":"00000000-0000-4000-8000-0000000000r1"}}`))
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	res, err := c.Call(context.Background(), "task.start", opRequest(key, taskStartInput(testTaskID)))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.CommandID != contract.ID(committed) || res.Status != contract.StatusCompleted {
		t.Fatalf("recovered envelope = %+v, want the retained task.start command", res.Payload)
	}
	if got := fc.eventCount(); got != 1 {
		t.Fatalf("business mutations = %d, want exactly 1: task.start must ready and enqueue the run exactly once", got)
	}

	// An explicit reconnect-and-lookup by the original key recovers the
	// identical disposition without a second run.
	looked, err := newLocalClient(t, fc, &staticCreds{value: testCred}).Call(context.Background(), CommandGetOperation, lookupRequest("task.start", key))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	retained := retainedEnvelope(t, looked)
	if retained.CommandID != contract.ID(committed) {
		t.Fatalf("lookup recovered %+v, want the original task.start command", retained.Payload)
	}
	if fc.eventCount() != 1 {
		t.Fatalf("events = %d after lookup, want 1: lookup must never enqueue a second run", fc.eventCount())
	}
}

// TestLostReviewDecisionAcknowledgementResolvesOriginalCommand is the
// card's named worker-lifecycle regression for the review boundary: a
// human's approve/reject decision on a proposed action is immutable, so a
// lost acknowledgement recovering through command.get lookup (never a
// replayed submission) must return the SAME decision, never mint a second
// one.
func TestLostReviewDecisionAcknowledgementResolvesOriginalCommand(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "review-decide-001"
	const committed = "00000000-0000-4000-8000-0000000000c2"
	fc.script("review.decide", dropAndCommit(key, committed,
		`{"resource":{"id":"`+testReviewID+`","decision":"approve"}}`))
	c := newLocalClient(t, fc, &staticCreds{value: testCred})
	c.unknownReplays = 0 // force resolution through command.get, not identical replay

	res, err := c.Call(context.Background(), "review.decide", opRequest(key, reviewDecideInput(testReviewID)))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.CommandID != contract.ID(committed) || res.Status != contract.StatusCompleted {
		t.Fatalf("recovered envelope = %+v, want the retained review.decide command", res.Payload)
	}
	if got := fc.eventCount(); got != 1 {
		t.Fatalf("business mutations = %d, want exactly 1: one decision, never two", got)
	}
	if got := fc.requestCount(CommandGetOperation); got != 1 {
		t.Fatalf("command lookups = %d, want 1", got)
	}
}

// --- unsupported server version -------------------------------------------

// TestUnsupportedServerVersionIsPermanentActionableError is the card's
// named regression: when the controller cannot serve an operation the
// client called -- an older controller lacking a revision-3 operation, or a
// contract version it does not support -- the fault must surface
// immediately as an actionable *contract.Fault the caller can branch on by
// code, never as an unknown outcome, and never be retried as though it were
// transient unavailability.
func TestUnsupportedServerVersionIsPermanentActionableError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		fault    *contract.Fault
		wantExit int
	}{
		{
			"capability unsupported",
			&contract.Fault{Code: contract.CodeCapabilityUnsupported,
				Message: "controller does not support this operation's current contract version", Retryable: false},
			5,
		},
		{
			"operation not registered on this controller",
			&contract.Fault{Code: contract.CodeNotFound,
				Message: "operation is not registered on this controller", Retryable: false},
			1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeController(t)
			// memory.list is a revision-3 operation; an older or
			// incompatible controller is exactly what would refuse it this
			// way.
			fc.script("memory.list", faultEnvelope(testCommandID, tc.fault))
			c := newLocalClient(t, fc, &staticCreds{value: testCred})

			res, err := c.Call(context.Background(), "memory.list", opRequest("", `{"scope":{"installation_id":"`+testInstallation+`"}}`))
			fault := assertFault(t, err, tc.fault.Code)
			if fault.Retryable {
				t.Fatalf("fault = %+v, want a permanent (non-retryable) disposition", fault)
			}
			var uae *UnknownAckError
			if errors.As(err, &uae) {
				t.Fatalf("version mismatch surfaced as an unknown outcome, not a permanent error: %v", err)
			}
			if res.Status != contract.StatusFailed || res.CommandID != contract.ID(testCommandID) {
				t.Fatalf("envelope = %+v, want the authoritative failed disposition", res.Payload)
			}
			if got := contract.CLIExit(fault); got != tc.wantExit {
				t.Fatalf("CLIExit(%s) = %d, want %d (a stable, addressable disposition)", fault.Code, got, tc.wantExit)
			}
			if got := fc.requestCount("memory.list"); got != 1 {
				t.Fatalf("requests = %d, want exactly 1: a permanent error is never retried", got)
			}
		})
	}
}

// --- disposition separation -------------------------------------------------

// TestDispositionStatesAreMutuallyDistinguishable proves the client keeps
// the four dispositions a caller must tell apart cleanly separate: bytes
// never reaching the controller, bytes sent with no authoritative
// acknowledgement, an accepted durable job, and a completed result. No two
// of them share a detection path.
func TestDispositionStatesAreMutuallyDistinguishable(t *testing.T) {
	t.Parallel()

	// 1. No bytes sent: dial itself never succeeds.
	notSent, err := New(Config{SocketPath: filepath.Join(t.TempDir(), "absent.sock")}, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	notSent.connectAttempts = 2
	notSent.backoff = func(int) time.Duration { return 0 }
	_, errNotSent := notSent.Call(context.Background(), testOperation, opRequest("k", `null`))
	if !errors.Is(errNotSent, ErrControllerUnavailable) {
		t.Fatalf("err = %v, want ErrControllerUnavailable", errNotSent)
	}
	if errors.Is(errNotSent, ErrUnknownOutcome) {
		t.Fatalf("no-bytes-sent must never be classified as an unknown acknowledgement")
	}

	// 2. Bytes sent, acknowledgement lost: unknown, never confused with
	// not-sent or a durable disposition.
	fcUnknown := newFakeController(t)
	fcUnknown.script(testOperation, dropResponse(), dropResponse())
	unknown := newLocalClient(t, fcUnknown, &staticCreds{value: testCred})
	unknown.unknownReplays = 0
	_, errUnknown := unknown.Call(context.Background(), testOperation, opRequest("k2", `null`))
	if !errors.Is(errUnknown, ErrUnknownOutcome) {
		t.Fatalf("err = %v, want ErrUnknownOutcome", errUnknown)
	}
	if errors.Is(errUnknown, ErrControllerUnavailable) {
		t.Fatalf("an unknown acknowledgement must never be classified as no-bytes-sent")
	}

	// 3. Accepted: a durable job reference, no error.
	fcAccepted := newFakeController(t)
	fcAccepted.script("task.create", func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: acceptedResult(testCommandID, `{"task_id":"00000000-0000-4000-8000-000000000009"}`)}
	})
	accepted, err := newLocalClient(t, fcAccepted, &staticCreds{value: testCred}).
		Call(context.Background(), "task.create", opRequest("k3", `null`))
	if err != nil || accepted.Status != contract.StatusAccepted {
		t.Fatalf("accepted call = %+v, err %v", accepted.Payload, err)
	}

	// 4. Completed: a finished result, no error, a different status than
	// accepted.
	fcCompleted := newFakeController(t)
	fcCompleted.script(testOperation, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `{"ok":true}`)}
	})
	completed, err := newLocalClient(t, fcCompleted, &staticCreds{value: testCred}).
		Call(context.Background(), testOperation, opRequest("k4", `null`))
	if err != nil || completed.Status != contract.StatusCompleted {
		t.Fatalf("completed call = %+v, err %v", completed.Payload, err)
	}
	if completed.Status == accepted.Status {
		t.Fatalf("accepted and completed dispositions collapsed to the same status")
	}
}

// --- cursor recovery never replays a write ----------------------------------

// TestExpiredEventCursorResynchronizesSnapshotWithoutReplayingWrites is the
// card's named cursor-recovery regression: an expired cursor is recovered
// ONLY through a fresh authorized snapshot read, never by silently filling
// the gap and never through any mutating call.
func TestExpiredEventCursorResynchronizesSnapshotWithoutReplayingWrites(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	c := newLocalClient(t, fc, &staticCreds{value: testCred})
	ctx := context.Background()

	fc.script("events.list", faultEnvelope(testCommandID, &contract.Fault{
		Code: contract.CodeCursorExpired, Message: "event cursor fell outside the retained replay window",
		Details: json.RawMessage(`{"snapshot_required":true}`),
	}))
	_, err := c.Call(ctx, "events.list", opRequest("", `{"cursor":"stale"}`))
	var expired *CursorExpiredError
	if !errors.As(err, &expired) || !expired.SnapshotRequired {
		t.Fatalf("err = %v, want CursorExpiredError with snapshot_required", err)
	}

	fc.script("events.snapshot", func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `{"events":[],"cursor":"fresh-1"}`)}
	})
	snap, err := c.Call(ctx, "events.snapshot", opRequest("", `{"scope":{}}`))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var snapshot struct {
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(snap.Data, &snapshot); err != nil || snapshot.Cursor != "fresh-1" {
		t.Fatalf("snapshot data = %s", snap.Data)
	}

	fc.script("events.list", func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `{"events":[]}`)}
	})
	if _, err := c.Call(ctx, "events.list", opRequest("", `{"cursor":"fresh-1"}`)); err != nil {
		t.Fatalf("resumed read: %v", err)
	}

	// No mutation was ever sent: every request issued while recovering was a
	// query (no submission key).
	for _, r := range fc.allRequests() {
		var req contract.Request
		if err := contract.DecodeStrict(r.Body, &req); err == nil && req.SubmissionKey != "" {
			t.Fatalf("cursor recovery issued a mutation: %s carried submission key %q", r.Operation, req.SubmissionKey)
		}
	}
	if fc.eventCount() != 0 {
		t.Fatalf("events = %d, want 0: cursor recovery must never write", fc.eventCount())
	}
}

// --- Lookup: the public reconnect helper ------------------------------------

// TestLookupHelperRecoversWithoutTheOriginalRequestBytes proves Lookup: a
// caller that lost its own state (a restarted CLI process, a reconnecting
// desktop or MCP session) can still recover a committed command's
// disposition from just the operation, scope and original submission key --
// nothing besides the read-only command.get query is ever sent.
func TestLookupHelperRecoversWithoutTheOriginalRequestBytes(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "reconnect-lookup-001"
	const committed = "00000000-0000-4000-8000-0000000000e1"
	// Simulates state this process already lost: a command committed by an
	// earlier process, whose original request bytes are gone.
	fc.commit(testOperation, key, []byte(`bytes this process no longer has`),
		*completedResult(committed, `{"draft_id":"00000000-0000-4000-8000-000000000002"}`))
	before := fc.eventCount()
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	res, found, err := c.Lookup(context.Background(), testOperation,
		json.RawMessage(`{"installation_id":"`+testInstallation+`"}`), key)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !found {
		t.Fatalf("found = false, want true: the command already committed")
	}
	if res.CommandID != contract.ID(committed) || res.Status != contract.StatusCompleted {
		t.Fatalf("recovered envelope = %+v", res.Payload)
	}
	if fc.eventCount() != before {
		t.Fatalf("events %d -> %d: Lookup must never mutate", before, fc.eventCount())
	}
	if got := fc.requestCount(testOperation); got != 0 {
		t.Fatalf("requests to %s = %d, want 0: Lookup never resends the original submission", testOperation, got)
	}
	if got := fc.requestCount(CommandGetOperation); got != 1 {
		t.Fatalf("command.get requests = %d, want 1", got)
	}
}

// TestLookupHelperNotFoundLeavesNothingCommitted proves the not-found half:
// an identity that never committed is reported found=false, with no
// resubmission attempted on the caller's behalf.
func TestLookupHelperNotFoundLeavesNothingCommitted(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	_, found, err := c.Lookup(context.Background(), testOperation,
		json.RawMessage(`{"installation_id":"`+testInstallation+`"}`), "never-sent-001")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if found {
		t.Fatalf("found = true, want false: nothing was ever committed")
	}
	if got := fc.requestCount(testOperation); got != 0 {
		t.Fatalf("requests = %d, want 0: not_found licenses recovery, not an automatic resubmission", got)
	}
}

// TestLookupHelperRejectsInvalidCoordinates proves Lookup validates its own
// arguments client-side (bad operation ID, bad submission key, malformed or
// non-object scope) before anything is sent.
func TestLookupHelperRejectsInvalidCoordinates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		operation string
		scope     string
		key       string
	}{
		{"bad operation", "Bad.Op", `{"installation_id":"x"}`, "k"},
		{"empty key", testOperation, `{"installation_id":"x"}`, ""},
		{"scope not object", testOperation, `"not-an-object"`, "k"},
		{"scope malformed json", testOperation, `{not json}`, "k"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeController(t)
			c := newLocalClient(t, fc, &staticCreds{value: testCred})
			_, _, err := c.Lookup(context.Background(), tc.operation, json.RawMessage(tc.scope), tc.key)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("err = %v, want ErrInvalidRequest", err)
			}
			if got := fc.requestCount(""); got != 0 {
				t.Fatalf("requests = %d, want 0: invalid coordinates must send nothing", got)
			}
		})
	}
}

// --- Poll: the bounded poll helper ------------------------------------------

// TestPollRefusesToResendAMutation proves Poll's central guarantee: given a
// keyed request, it sends nothing at all rather than repeat a mutation.
func TestPollRefusesToResendAMutation(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	_, err := c.Poll(context.Background(), testOperation, opRequest("poll-key-1", testInput), PollOptions{})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if got := fc.requestCount(""); got != 0 {
		t.Fatalf("requests = %d, want 0: Poll must never send a keyed request", got)
	}
}

// TestPollStopsAtTerminalDisposition proves Poll repeats an accepted query
// only until a terminal (completed/failed) result arrives, then stops.
func TestPollStopsAtTerminalDisposition(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	running := func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: acceptedResult(testCommandID, `{"state":"running"}`)}
	}
	fc.script("job.get", running, running, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `{"state":"succeeded"}`)}
	})
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	res, err := c.Poll(context.Background(), "job.get",
		opRequest("", `{"job_id":"00000000-0000-4000-8000-000000000009"}`), PollOptions{MaxAttempts: 5})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if res.Status != contract.StatusCompleted {
		t.Fatalf("status = %q, want completed", res.Status)
	}
	if got := fc.requestCount("job.get"); got != 3 {
		t.Fatalf("requests = %d, want exactly 3 (stop at the terminal result)", got)
	}
}

// TestPollExhaustionIsBounded proves Poll never polls unboundedly: an
// operation that stays accepted forever exhausts MaxAttempts and returns
// ErrPollExhausted rather than looping forever.
func TestPollExhaustionIsBounded(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	running := func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: acceptedResult(testCommandID, `{"state":"running"}`)}
	}
	fc.script("job.get", running, running, running)
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	_, err := c.Poll(context.Background(), "job.get",
		opRequest("", `{"job_id":"00000000-0000-4000-8000-00000000000a"}`), PollOptions{MaxAttempts: 3})
	if !errors.Is(err, ErrPollExhausted) {
		t.Fatalf("err = %v, want ErrPollExhausted", err)
	}
	if got := fc.requestCount("job.get"); got != 3 {
		t.Fatalf("requests = %d, want exactly MaxAttempts (3)", got)
	}
}

// TestPollSurfacesFailedResultImmediately proves a failed query terminates
// Poll on the first attempt, its fault reachable exactly as Call would
// surface it.
func TestPollSurfacesFailedResultImmediately(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	fc.script("job.get", faultEnvelope(testCommandID, &contract.Fault{
		Code: contract.CodeNotFound, Message: "job not found",
	}))
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	_, err := c.Poll(context.Background(), "job.get",
		opRequest("", `{"job_id":"00000000-0000-4000-8000-00000000000b"}`), PollOptions{MaxAttempts: 5})
	_ = assertFault(t, err, contract.CodeNotFound)
	if got := fc.requestCount("job.get"); got != 1 {
		t.Fatalf("requests = %d, want 1: a failed result is terminal on the first attempt", got)
	}
}
