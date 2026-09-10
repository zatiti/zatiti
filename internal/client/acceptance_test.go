package client

// Named acceptance cases from AGENTS.md, proven client-locally: both
// transport families (local Unix socket, remote TLS) run against one
// scripted controller holding one durable command store, so every case
// observes the same durable state a real controller would retain.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// lookupRequest builds the command.get query for an original submission
// key. It is a query: no submission key of its own, so resolution
// terminates and no new durable identity is minted.
func lookupRequest(key string) contract.Request {
	return contract.Request{
		Schema: contract.SchemaRequest,
		Input:  json.RawMessage(`{"submission_key":"` + key + `"}`),
	}
}

// assertFault asserts err carries exactly the named fault.
func assertFault(t *testing.T, err error, code string) *contract.Fault {
	t.Helper()
	var fault *contract.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("err = %v, want fault %s", err, code)
	}
	if fault.Code != code {
		t.Fatalf("fault code = %q, want %q", fault.Code, code)
	}
	return fault
}

// assertEnvelopeEqual compares every field a machine client inspects.
func assertEnvelopeEqual(t *testing.T, got, want *contract.Result) {
	t.Helper()
	if got.Schema != want.Schema {
		t.Errorf("schema = %q, want %q", got.Schema, want.Schema)
	}
	if got.CommandID != want.CommandID {
		t.Errorf("command_id = %q, want %q", got.CommandID, want.CommandID)
	}
	if got.Status != want.Status {
		t.Errorf("status = %q, want %q", got.Status, want.Status)
	}
	if string(got.Data) != string(want.Data) {
		t.Errorf("data = %s, want %s", got.Data, want.Data)
	}
	if (got.NextCursor == nil) != (want.NextCursor == nil) ||
		(got.NextCursor != nil && *got.NextCursor != *want.NextCursor) {
		t.Errorf("next_cursor = %v, want %v", got.NextCursor, want.NextCursor)
	}
	if (got.Error == nil) != (want.Error == nil) {
		t.Fatalf("error = %v, want %v", got.Error, want.Error)
	}
	if got.Error != nil {
		if got.Error.Code != want.Error.Code || got.Error.Message != want.Error.Message ||
			got.Error.Retryable != want.Error.Retryable ||
			string(got.Error.Details) != string(want.Error.Details) {
			t.Errorf("fault = %+v, want %+v", got.Error, want.Error)
		}
	}
}

// submissionKeysOf decodes the request envelope of every captured request
// (optionally filtered by operation) and returns its submission key.
func submissionKeysOf(t *testing.T, fc *fakeController, op string) []string {
	t.Helper()
	var keys []string
	for _, r := range fc.allRequests() {
		if op != "" && r.Operation != op {
			continue
		}
		var req contract.Request
		if err := contract.DecodeStrict(r.Body, &req); err != nil {
			continue
		}
		keys = append(keys, req.SubmissionKey)
	}
	return keys
}

// allKeysEqual asserts every captured request for op carries exactly key:
// recovery never invents a new durable submission identity.
func allKeysEqual(t *testing.T, fc *fakeController, op, key string) {
	t.Helper()
	keys := submissionKeysOf(t, fc, op)
	if len(keys) == 0 {
		t.Fatalf("no captured requests for %s", op)
	}
	for _, got := range keys {
		if got != key {
			t.Fatalf("submission key %q in flight, want only %q", got, key)
		}
	}
}

// bodiesIdentical asserts every captured request for op carried the same
// bytes: replays are identical submissions, never rewritten.
func bodiesIdentical(t *testing.T, fc *fakeController, op string) {
	t.Helper()
	var first []byte
	n := 0
	for _, r := range fc.allRequests() {
		if r.Operation != op {
			continue
		}
		if first == nil {
			first = r.Body
		} else if !equalBytes(first, r.Body) {
			t.Fatalf("request bytes diverged for %s under one submission identity", op)
		}
		n++
	}
	if n == 0 {
		t.Fatalf("no captured requests for %s", op)
	}
}

// ptr returns a pointer to s.
func ptr(s string) *string { return &s }

// --- Z16.cli_to_mcp -----------------------------------------------------------

// TestZ16CliToMcpContinuesDurableWork is acceptance case Z16.cli_to_mcp:
// durable work is created through the CLI transport, the CLI disconnects,
// and the MCP transport inspects, decides and continues the SAME command
// against the SAME controller state. No duplicate controller, scheduler or
// task is created.
func TestZ16CliToMcpContinuesDurableWork(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	creds := &staticCreds{value: testCred}
	ctx := context.Background()

	accepted := acceptedResult(testCommandID, `{"task_id":"00000000-0000-4000-8000-000000000003"}`)
	fc.script("task.create", commitAndRespond("cli-mcp-task-1", accepted))

	// CLI transport creates and starts the durable work.
	cli := newLocalClient(t, fc, creds)
	created, err := cli.Call(ctx, "task.create", opRequest("cli-mcp-task-1", `{"title":"Ship the release"}`))
	if err != nil {
		t.Fatalf("cli call: %v", err)
	}
	if created.Status != contract.StatusAccepted {
		t.Fatalf("status = %q, want accepted", created.Status)
	}
	var started struct {
		TaskID contract.ID `json:"task_id"`
	}
	if err := json.Unmarshal(created.Data, &started); err != nil || started.TaskID == "" {
		t.Fatalf("accepted data %s: %v", created.Data, err)
	}

	// MCP transport continues the same work through a second client on the
	// same controller: command lookup by the original submission key first,
	// then a review decision on the same task.
	mcp := newLocalClient(t, fc, creds)
	lookup, err := mcp.Call(ctx, CommandGetOperation, lookupRequest("cli-mcp-task-1"))
	if err != nil {
		t.Fatalf("mcp lookup: %v", err)
	}
	assertEnvelopeEqual(t, &lookup, accepted)

	fc.script("review.decide", commitAndRespond("cli-mcp-approve-1",
		completedResult("00000000-0000-4000-8000-000000000004", `{"approved":true}`)))
	decided, err := mcp.Call(ctx, "review.decide", opRequest("cli-mcp-approve-1",
		fmt.Sprintf(`{"task_id":%q,"decision":"approve"}`, started.TaskID)))
	if err != nil {
		t.Fatalf("mcp review: %v", err)
	}
	if decided.CommandID == created.CommandID {
		t.Fatalf("decision reused the creation command id")
	}

	// One controller, exactly one durable mutation per submission key, and
	// the same principal on every request of both transports.
	if fc.eventCount() != 2 {
		t.Fatalf("events = %d, want 2 (task.create, review.decide)", fc.eventCount())
	}
	for _, r := range fc.allRequests() {
		if got := r.Header.Get(AuthHeader); got != testCred {
			t.Fatalf("authorization %q on %s, want the same principal on both transports", got, r.Operation)
		}
	}
	ops := map[string]bool{}
	for _, r := range fc.allRequests() {
		ops[r.Operation] = true
	}
	for _, want := range []string{"task.create", CommandGetOperation, "review.decide"} {
		if !ops[want] {
			t.Fatalf("operation %s never reached the controller", want)
		}
	}
}

// --- Z16.mcp_to_cli -----------------------------------------------------------

// TestZ16McpToCliContinuesDurableWork is acceptance case Z16.mcp_to_cli:
// work started through MCP continues through the CLI with equivalent
// authorized state and durable identities, and no transport-specific
// prerequisite is required to continue the workflow.
func TestZ16McpToCliContinuesDurableWork(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	creds := &staticCreds{value: testCred}
	ctx := context.Background()

	accepted := acceptedResult(testCommandID, `{"task_id":"00000000-0000-4000-8000-000000000003"}`)
	fc.script("task.create", commitAndRespond("mcp-cli-task-1", accepted))
	mcp := newLocalClient(t, fc, creds)
	created, err := mcp.Call(ctx, "task.create", opRequest("mcp-cli-task-1", `{"title":"Sweep logs"}`))
	if err != nil {
		t.Fatalf("mcp call: %v", err)
	}
	var started struct {
		TaskID contract.ID `json:"task_id"`
	}
	if err := json.Unmarshal(created.Data, &started); err != nil || started.TaskID == "" {
		t.Fatalf("accepted data %s: %v", created.Data, err)
	}

	// The CLI transport resumes with the workflow's own operations only:
	// command lookup by the original key, then the continuation. No
	// re-authentication handshake, no transport-specific bootstrap.
	cli := newLocalClient(t, fc, creds)
	lookup, err := cli.Call(ctx, CommandGetOperation, lookupRequest("mcp-cli-task-1"))
	if err != nil {
		t.Fatalf("cli lookup: %v", err)
	}
	assertEnvelopeEqual(t, &lookup, accepted)

	fc.script("task.update", commitAndRespond("mcp-cli-update-1",
		completedResult("00000000-0000-4000-8000-000000000005", `{"status":"in_progress"}`)))
	if _, err := cli.Call(ctx, "task.update", opRequest("mcp-cli-update-1",
		fmt.Sprintf(`{"task_id":%q,"status":"in_progress"}`, started.TaskID))); err != nil {
		t.Fatalf("cli continuation: %v", err)
	}

	var ops []string
	for _, r := range fc.allRequests() {
		ops = append(ops, r.Operation)
	}
	wantOps := []string{"task.create", CommandGetOperation, "task.update"}
	if len(ops) != len(wantOps) {
		t.Fatalf("operations = %v, want exactly %v", ops, wantOps)
	}
	for i, want := range wantOps {
		if ops[i] != want {
			t.Fatalf("operation %d = %q, want %q", i, ops[i], want)
		}
	}
	if fc.eventCount() != 2 {
		t.Fatalf("events = %d, want 2", fc.eventCount())
	}
}

// --- Z16.disconnect_command_lookup ---------------------------------------------

// TestZ16DisconnectCommandLookup is acceptance case Z16.disconnect_command_lookup:
// a mutation commits immediately before its transport connection drops; the
// recovered disposition comes from the original submission key only, and a
// lost query never mints one.
func TestZ16DisconnectCommandLookup(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	creds := &staticCreds{value: testCred}
	ctx := context.Background()
	const key = "org-create-9"
	const data = `{"draft_id":"00000000-0000-4000-8000-000000000002"}`

	fc.script(testOperation, dropAndCommit(key, testCommandID, data))
	c := newLocalClient(t, fc, creds)

	// The controller committed, then the connection dropped before the
	// acknowledgement: the client replays the IDENTICAL bytes under the
	// SAME key and recovers the original disposition.
	res, err := c.Call(ctx, testOperation, opRequest(key, testInput))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.Status != contract.StatusCompleted || res.CommandID != contract.ID(testCommandID) {
		t.Fatalf("recovered envelope = %+v", res.Payload)
	}
	reqs := fc.allRequests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want original + identical replay", len(reqs))
	}
	if !equalBytes(reqs[0].Body, reqs[1].Body) {
		t.Fatalf("replay bytes differ from the original submission")
	}
	if fc.eventCount() != 1 {
		t.Fatalf("events = %d, want 1: replay must not repeat the mutation", fc.eventCount())
	}

	// An explicit reconnect-and-lookup with the original key returns the
	// same retained disposition without another mutation.
	looked, err := newLocalClient(t, fc, creds).Call(ctx, CommandGetOperation, lookupRequest(key))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	assertEnvelopeEqual(t, &looked, completedResult(testCommandID, data))
	if fc.eventCount() != 1 {
		t.Fatalf("events = %d after lookup, want 1", fc.eventCount())
	}

	// Retry ONLY with that key: the same key with changed input is refused
	// by name, never silently accepted.
	_, err = c.Call(ctx, testOperation, opRequest(key, `{"key":"other","name":"Different organization"}`))
	_ = assertFault(t, err, contract.CodeSubmissionConflict)
	if fc.eventCount() != 1 {
		t.Fatalf("events = %d after conflict, want 1", fc.eventCount())
	}

	// JSON-RPC request IDs are not durable submission keys: a lost query
	// (no submission key) is reported unknown, mints no key, and stores no
	// command.
	fc.script("events.list", dropResponse())
	_, err = c.Call(ctx, "events.list", opRequest("", `{"cursor":"c1"}`))
	var uae *UnknownAckError
	if !errors.As(err, &uae) {
		t.Fatalf("err = %v, want UnknownAckError", err)
	}
	if uae.SubmissionKey != "" {
		t.Fatalf("query minted submission key %q", uae.SubmissionKey)
	}
	if !strings.Contains(uae.Error(), "may be reissued") {
		t.Fatalf("query error text = %q, want a reissue instruction", uae.Error())
	}
	if got := fc.storeSize(); got != 1 {
		t.Fatalf("store holds %d commands, want only the original mutation", got)
	}
}

// --- Z16.cursor_expiry ----------------------------------------------------------

// TestZ16CursorExpiry is acceptance case Z16.cursor_expiry: a stale event
// cursor surfaces the named cursor-expiry result with snapshot_required, the
// caller recovers through a fresh authorized snapshot and a new replay
// position, and both transports classify the boundary identically. No
// unseen gap is ever presented as complete history.
func TestZ16CursorExpiry(t *testing.T) {
	t.Parallel()
	for _, remote := range []bool{false, true} {
		name := "local"
		if remote {
			name = "remote"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeController(t)
			creds := &staticCreds{value: testCred}
			c := newLocalClient(t, fc, creds)
			if remote {
				c = newRemoteClient(t, fc, creds)
			}
			ctx := context.Background()

			fc.script("events.list", faultEnvelope(testCommandID, &contract.Fault{
				Code:    contract.CodeCursorExpired,
				Message: "event cursor fell outside the retained replay window",
				Details: json.RawMessage(`{"snapshot_required":true}`),
			}))
			res, err := c.Call(ctx, "events.list", opRequest("", `{"cursor":"stale-cursor"}`))
			var expired *CursorExpiredError
			if !errors.As(err, &expired) {
				t.Fatalf("err = %v, want CursorExpiredError", err)
			}
			if !expired.SnapshotRequired {
				t.Fatalf("snapshot_required detail lost")
			}
			var fault *contract.Fault
			if !errors.As(err, &fault) || fault.Code != contract.CodeCursorExpired {
				t.Fatalf("fault chain lost cursor_expired: %v", err)
			}
			// Nothing about the expired read is presented as history: no
			// data, no cursor, no durable mutation, no hidden retry.
			if string(res.Data) != "null" && len(res.Data) != 0 {
				t.Fatalf("expired read returned data %s", res.Data)
			}
			if res.NextCursor != nil {
				t.Fatalf("expired read returned cursor %q", *res.NextCursor)
			}
			if fc.eventCount() != 0 {
				t.Fatalf("events = %d after an expired query", fc.eventCount())
			}
			if got := fc.requestCount("events.list"); got != 1 {
				t.Fatalf("events.list requests = %d, want 1 (a named refusal is not retried)", got)
			}

			// Recovery: fetch a fresh authorized snapshot, then resume from
			// the NEW cursor. The stale cursor is never silently resumed.
			fc.script("events.snapshot", func(*fakeController, string, []byte) scriptedResponse {
				return scriptedResponse{envelope: completedResult(testCommandID,
					`{"events":[{"id":"00000000-0000-4000-8000-00000000000a"}],"cursor":"fresh-cursor"}`)}
			})
			snap, err := c.Call(ctx, "events.snapshot", opRequest("", `{"scope":{}}`))
			if err != nil {
				t.Fatalf("snapshot: %v", err)
			}
			var snapshot struct {
				Cursor string `json:"cursor"`
			}
			if err := json.Unmarshal(snap.Data, &snapshot); err != nil || snapshot.Cursor == "" {
				t.Fatalf("snapshot data %s: %v", snap.Data, err)
			}

			fc.script("events.list", func(*fakeController, string, []byte) scriptedResponse {
				page := completedResult(testCommandID, `{"events":[{"id":"00000000-0000-4000-8000-00000000000b"}]}`)
				page.NextCursor = ptr("cursor-2")
				return scriptedResponse{envelope: page}
			})
			page, err := c.Call(ctx, "events.list", opRequest("", `{"cursor":"fresh-cursor"}`))
			if err != nil {
				t.Fatalf("resumed read: %v", err)
			}
			if page.NextCursor == nil || *page.NextCursor != "cursor-2" {
				t.Fatalf("resumed page cursor = %v", page.NextCursor)
			}
			if got := fc.requestCount("events.list"); got != 2 {
				t.Fatalf("events.list requests = %d, want the stale and the resumed read", got)
			}
		})
	}
}

// --- Z16.interrupted_upload ------------------------------------------------------

// TestZ16InterruptedUpload is acceptance case Z16.interrupted_upload: a
// bounded chunk upload is partially accepted before the client disconnects;
// retries reuse the stable upload identity without corrupting content, the
// finish boundary verifies digest and bounds, a partial upload is cancelled
// rather than published, and a controller filesystem path is refused by
// name with nothing committed.
func TestZ16InterruptedUpload(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	creds := &staticCreds{value: testCred}
	c := newLocalClient(t, fc, creds)
	ctx := context.Background()

	// Stable upload identity from an accepted begin.
	fc.script("artifact.upload.begin", commitAndRespond("upload-begin-1",
		acceptedResult("00000000-0000-4000-8000-00000000000a", `{"upload_id":"00000000-0000-4000-8000-00000000000b"}`)))
	begun, err := c.Call(ctx, "artifact.upload.begin", opRequest("upload-begin-1", `{"name":"bundle.bin","size":2}`))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	var upload struct {
		UploadID contract.ID `json:"upload_id"`
	}
	if err := json.Unmarshal(begun.Data, &upload); err != nil || upload.UploadID == "" {
		t.Fatalf("begin data %s: %v", begun.Data, err)
	}
	chunkInput := func(index int) string {
		return fmt.Sprintf(`{"upload_id":%q,"index":%d,"data":"chunk-%d"}`, upload.UploadID, index, index)
	}

	// Chunk 1 is acknowledged; chunk 2 is accepted but its acknowledgement
	// is lost to the disconnect. The identical-key replay recovers it.
	fc.script("artifact.upload.chunk",
		commitAndRespond("upload-chunk-1", completedResult("00000000-0000-4000-8000-00000000000c", `{"offset":1}`)),
		dropAndCommit("upload-chunk-2", "00000000-0000-4000-8000-00000000000d", `{"offset":2}`),
	)
	if _, err := c.Call(ctx, "artifact.upload.chunk", opRequest("upload-chunk-1", chunkInput(1))); err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	retried, err := c.Call(ctx, "artifact.upload.chunk", opRequest("upload-chunk-2", chunkInput(2)))
	if err != nil {
		t.Fatalf("chunk 2 after disconnect: %v", err)
	}

	// An explicit retry of the accepted chunk deduplicates to the same
	// disposition, and the stored bytes are identical — content cannot
	// drift and no duplicate artifact can be published.
	again, err := c.Call(ctx, "artifact.upload.chunk", opRequest("upload-chunk-2", chunkInput(2)))
	if err != nil {
		t.Fatalf("chunk 2 retry: %v", err)
	}
	if again.CommandID != retried.CommandID {
		t.Fatalf("chunk retry minted command %q, want the original %q", again.CommandID, retried.CommandID)
	}
	// Captured: chunk 1, the lost chunk 2, its identical replay, and the
	// explicit retry — every chunk-2 submission carries the same bytes.
	var chunkBodies [][]byte
	for _, r := range fc.allRequests() {
		if r.Operation == "artifact.upload.chunk" {
			chunkBodies = append(chunkBodies, r.Body)
		}
	}
	if len(chunkBodies) != 4 ||
		!equalBytes(chunkBodies[1], chunkBodies[2]) || !equalBytes(chunkBodies[2], chunkBodies[3]) {
		t.Fatalf("chunk request bytes diverged: %d captured", len(chunkBodies))
	}

	// Finish verifies the full digest and bounds of the accepted chunks.
	digest := strings.Repeat("a", 64)
	fc.script("artifact.upload.finish", commitAndRespond("upload-finish-1",
		completedResult("00000000-0000-4000-8000-00000000000e",
			fmt.Sprintf(`{"artifact_id":"00000000-0000-4000-8000-00000000000f","sha256":%q,"size":2}`, digest))))
	fin, err := c.Call(ctx, "artifact.upload.finish", opRequest("upload-finish-1", fmt.Sprintf(`{"upload_id":%q}`, upload.UploadID)))
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	var published struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(fin.Data, &published); err != nil || len(published.SHA256) != 64 {
		t.Fatalf("finish data %s: %v", fin.Data, err)
	}

	// A separate partial upload is cancelled: its staged bytes stay
	// reclaimable and it is never published as a completed artifact.
	const partialID = "00000000-0000-4000-8000-000000000010"
	fc.script("artifact.upload.begin", commitAndRespond("upload-begin-2",
		acceptedResult("00000000-0000-4000-8000-00000000000f", `{"upload_id":"`+partialID+`"}`)))
	fc.script("artifact.upload.chunk", commitAndRespond("upload-chunk-3",
		completedResult("00000000-0000-4000-8000-000000000011", `{"offset":1}`)))
	fc.script("artifact.upload.cancel", commitAndRespond("upload-cancel-1",
		completedResult("00000000-0000-4000-8000-000000000012", `{"cancelled":true,"staged_bytes_reclaimable":true}`)))
	if _, err := c.Call(ctx, "artifact.upload.begin", opRequest("upload-begin-2", `{"name":"partial.bin","size":1}`)); err != nil {
		t.Fatalf("begin 2: %v", err)
	}
	if _, err := c.Call(ctx, "artifact.upload.chunk", opRequest("upload-chunk-3",
		fmt.Sprintf(`{"upload_id":%q,"index":1,"data":"chunk-1"}`, partialID))); err != nil {
		t.Fatalf("partial chunk: %v", err)
	}
	cancelled, err := c.Call(ctx, "artifact.upload.cancel", opRequest("upload-cancel-1",
		fmt.Sprintf(`{"upload_id":%q}`, partialID)))
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var cancelState struct {
		Cancelled bool `json:"cancelled"`
	}
	if err := json.Unmarshal(cancelled.Data, &cancelState); err != nil || !cancelState.Cancelled {
		t.Fatalf("cancel data %s: %v", cancelled.Data, err)
	}
	if cancelled.Status != contract.StatusCompleted {
		t.Fatalf("cancel status = %q", cancelled.Status)
	}
	if got := fc.requestCount("artifact.upload.finish"); got != 1 {
		t.Fatalf("finish requests = %d, want 1: partial bytes were published as an artifact", got)
	}

	// Neither interface accepts an unrestricted controller filesystem path:
	// the controller refuses it by name and the client surfaces the fault
	// verbatim, committing nothing.
	fc.script("artifact.upload.chunk", faultEnvelope("00000000-0000-4000-8000-000000000013", &contract.Fault{
		Code:    contract.CodeInvalidInput,
		Message: "input must not reference controller filesystem paths",
	}))
	_, err = c.Call(ctx, "artifact.upload.chunk", opRequest("upload-path-1",
		fmt.Sprintf(`{"upload_id":%q,"path":"/var/lib/zatiti/state.db"}`, partialID)))
	_ = assertFault(t, err, contract.CodeInvalidInput)
	if fc.eventCount() != 7 {
		t.Fatalf("events = %d, want 7", fc.eventCount())
	}
}

// --- Z21.reconnect_no_duplicate ---------------------------------------------------

// TestZ21ReconnectNoDuplicate is acceptance case Z21.reconnect_no_duplicate:
// the desktop's mutation loses its acknowledgement, the draft stays
// explicitly unsent while offline, and reconnection recovers the
// disposition through the ORIGINAL submission identity without any
// duplicate task or effect.
func TestZ21ReconnectNoDuplicate(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	creds := &staticCreds{value: testCred}
	ctx := context.Background()
	const key = "desktop-draft-77"
	const op = "conversation.send"
	const input = `{"conversation_id":"00000000-0000-4000-8000-000000000020","text":"hello"}`

	// The send receives no acknowledgement and, so far, committed nothing.
	fc.script(op, dropResponse(), dropResponse())
	desktop := newLocalClient(t, fc, creds)
	desktop.unknownReplays = 0
	_, err := desktop.Call(ctx, op, opRequest(key, input))
	var uae *UnknownAckError
	if !errors.As(err, &uae) || !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("err = %v, want unknown outcome", err)
	}
	if uae.SubmissionKey != key {
		t.Fatalf("unknown outcome key = %q, want the original %q", uae.SubmissionKey, key)
	}

	// Offline: the controller stops. The caller has seen ErrUnknownOutcome,
	// never a fabricated acknowledgement — unsent input and cached
	// acknowledged state remain distinguishable.
	fc.StopUnix()
	if fc.eventCount() != 0 {
		t.Fatalf("events = %d while offline, want no duplicate effect", fc.eventCount())
	}

	// Reconnect: a fresh socket serves the same controller state. The
	// explicit lookup with the original key finds nothing — not_found
	// proves the command never committed, so the caller retries the
	// identical draft under the SAME key.
	fc.ListenUnix()
	reconnected := newLocalClient(t, fc, creds)
	_, lookupErr := reconnected.Call(ctx, CommandGetOperation, lookupRequest(key))
	_ = assertFault(t, lookupErr, contract.CodeNotFound)

	fc.script(op, commitAndRespond(key,
		completedResult("00000000-0000-4000-8000-000000000021", `{"message_id":"00000000-0000-4000-8000-000000000022"}`)))
	res, err := reconnected.Call(ctx, op, opRequest(key, input))
	if err != nil {
		t.Fatalf("retry after reconnect: %v", err)
	}
	if res.Status != contract.StatusCompleted {
		t.Fatalf("retry status = %q", res.Status)
	}
	if fc.eventCount() != 1 {
		t.Fatalf("events = %d after reconnect retry, want exactly 1", fc.eventCount())
	}
	if got := fc.storeSize(); got != 1 {
		t.Fatalf("store holds %d commands, want 1", got)
	}
	allKeysEqual(t, fc, op, key)
	bodiesIdentical(t, fc, op)
}

// --- JOURNEY.cross_interface_fault_matrix -------------------------------------------

// clientCtor builds a client on one transport family of the fake.
type clientCtor func(t *testing.T, fc *fakeController, creds contract.CredentialSource) *Client

// TestJourneyCrossInterfaceFaultMatrix is acceptance case
// JOURNEY.cross_interface_fault_matrix, proven client-locally at the
// mutation-acknowledgement, claim/conflict, review, upload, effect-return
// and restart boundaries: each fault boundary is interrupted on one
// transport and continued through the opposite one, in both directions.
// Submission identity, pinned contracts, events and authorization must
// remain equivalent; unknown outcomes stay unknown until reconciliation;
// no duplicate protected effect occurs. Each subtest name records the
// boundary and both transports as evidence.
func TestJourneyCrossInterfaceFaultMatrix(t *testing.T) {
	t.Parallel()
	type boundary struct {
		name   string
		events int
		run    func(t *testing.T, start *Client, cont clientCtor, fc *fakeController, creds contract.CredentialSource)
	}
	cases := []boundary{
		{"mutation_acknowledgement_lost", 1, matrixAcknowledgementLost},
		{"claim_conflict_boundary", 0, matrixSubmissionConflict},
		{"review_boundary", 0, matrixReviewRequired},
		{"upload_chunk_interrupted", 2, matrixUploadInterrupted},
		{"effect_unknown_then_reconciled", 1, matrixEffectUnknown},
		{"controller_restart", 1, matrixControllerRestart},
	}
	directions := []struct {
		name  string
		start clientCtor
		cont  clientCtor
	}{
		{"unix_to_tls", newLocalClient, newRemoteClient},
		{"tls_to_unix", newRemoteClient, newLocalClient},
	}
	for _, b := range cases {
		for _, d := range directions {
			t.Run(b.name+"/"+d.name, func(t *testing.T) {
				t.Parallel()
				fc := newFakeController(t)
				creds := &staticCreds{value: testCred}
				start := d.start(t, fc, creds)
				b.run(t, start, d.cont, fc, creds)
				if got := fc.eventCount(); got != b.events {
					t.Fatalf("events = %d, want %d", got, b.events)
				}
				// Authorization is equivalent on both transports: the same
				// principal carried every request in both directions.
				for _, r := range fc.allRequests() {
					if got := r.Header.Get(AuthHeader); got != testCred {
						t.Fatalf("authorization %q on %s, want the same principal on both transports", got, r.Operation)
					}
				}
			})
		}
	}
}

const matrixKey = "journey-key-1"

// matrixAcknowledgementLost: the mutation commits on the starting transport
// immediately before its connection drops; the continuing transport
// recovers the identical disposition through the original submission key.
func matrixAcknowledgementLost(t *testing.T, start *Client, cont clientCtor, fc *fakeController, creds contract.CredentialSource) {
	t.Helper()
	const data = `{"draft_id":"00000000-0000-4000-8000-000000000002"}`
	fc.script(testOperation, dropAndCommit(matrixKey, testCommandID, data))
	res, err := start.Call(context.Background(), testOperation, opRequest(matrixKey, testInput))
	if err != nil {
		t.Fatalf("starting transport: %v", err)
	}
	looked, err := cont(t, fc, creds).Call(context.Background(), CommandGetOperation, lookupRequest(matrixKey))
	if err != nil {
		t.Fatalf("continuing transport lookup: %v", err)
	}
	assertEnvelopeEqual(t, &looked, completedResult(testCommandID, data))
	if looked.CommandID != res.CommandID || looked.Status != res.Status {
		t.Fatalf("dispositions disagree across transports: %+v vs %+v", res.Payload, looked.Payload)
	}
	allKeysEqual(t, fc, testOperation, matrixKey)
}

// matrixSubmissionConflict: changed input under a live submission key is
// refused by name on both transports, with no auto-retry and no effect.
func matrixSubmissionConflict(t *testing.T, start *Client, cont clientCtor, fc *fakeController, creds contract.CredentialSource) {
	t.Helper()
	fault := &contract.Fault{
		Code:      contract.CodeSubmissionConflict,
		Message:   "submission key reused with different input",
		Retryable: false,
	}
	fc.script(testOperation, faultEnvelope(testCommandID, fault), faultEnvelope(testCommandID, fault))
	_, err := start.Call(context.Background(), testOperation, opRequest(matrixKey, testInput))
	_ = assertFault(t, err, contract.CodeSubmissionConflict)
	_, err = cont(t, fc, creds).Call(context.Background(), testOperation, opRequest(matrixKey, testInput))
	_ = assertFault(t, err, contract.CodeSubmissionConflict)
	if got := fc.requestCount(testOperation); got != 2 {
		t.Fatalf("requests = %d, want exactly one per transport (named refusals are not retried)", got)
	}
}

// matrixReviewRequired: a review boundary refuses identically on both
// transports, and the envelope a machine client received agrees with the
// CLI exit code the same disposition maps to.
func matrixReviewRequired(t *testing.T, start *Client, cont clientCtor, fc *fakeController, creds contract.CredentialSource) {
	t.Helper()
	fault := &contract.Fault{Code: contract.CodeReviewRequired, Message: "human decision required"}
	fc.script(testOperation, faultEnvelope(testCommandID, fault), faultEnvelope(testCommandID, fault))
	_, err := start.Call(context.Background(), testOperation, opRequest(matrixKey, testInput))
	f1 := assertFault(t, err, contract.CodeReviewRequired)
	_, err = cont(t, fc, creds).Call(context.Background(), testOperation, opRequest(matrixKey, testInput))
	f2 := assertFault(t, err, contract.CodeReviewRequired)
	if contract.CLIExit(f1) != 3 || contract.CLIExit(f2) != 3 {
		t.Fatalf("CLIExit(review_required) = %d/%d, want 3 on both transports", contract.CLIExit(f1), contract.CLIExit(f2))
	}
}

// matrixUploadInterrupted: a chunk commits and loses its acknowledgement on
// the starting transport; the continuing transport finishes the same upload
// under the same stable identity.
func matrixUploadInterrupted(t *testing.T, start *Client, cont clientCtor, fc *fakeController, creds contract.CredentialSource) {
	t.Helper()
	ctx := context.Background()
	const chunkOp = "artifact.upload.chunk"
	const chunkKey = matrixKey + "-chunk"
	const finishKey = matrixKey + "-finish"
	const uploadID = "00000000-0000-4000-8000-000000000005"
	input := fmt.Sprintf(`{"upload_id":%q,"index":1,"data":"chunk-1"}`, uploadID)

	fc.script(chunkOp, dropAndCommit(chunkKey, "00000000-0000-4000-8000-000000000006", `{"offset":1}`))
	if _, err := start.Call(ctx, chunkOp, opRequest(chunkKey, input)); err != nil {
		t.Fatalf("starting transport chunk: %v", err)
	}
	digest := strings.Repeat("a", 64)
	fc.script("artifact.upload.finish", commitAndRespond(finishKey,
		completedResult("00000000-0000-4000-8000-000000000007",
			fmt.Sprintf(`{"artifact_id":"00000000-0000-4000-8000-000000000007","sha256":%q,"size":1}`, digest))))
	fin, err := cont(t, fc, creds).Call(ctx, "artifact.upload.finish",
		opRequest(finishKey, fmt.Sprintf(`{"upload_id":%q}`, uploadID)))
	if err != nil {
		t.Fatalf("continuing transport finish: %v", err)
	}
	if fin.Status != contract.StatusCompleted {
		t.Fatalf("finish status = %q", fin.Status)
	}
	allKeysEqual(t, fc, chunkOp, chunkKey)
}

// matrixEffectUnknown: the effect boundary yields no authoritative envelope
// and no commit. The starting transport reports the outcome unknown with
// the original key preserved; the continuing transport reconciles — lookup
// says not_found, licensing exactly one identical resubmission under that
// same key, never a new one.
func matrixEffectUnknown(t *testing.T, start *Client, cont clientCtor, fc *fakeController, creds contract.CredentialSource) {
	t.Helper()
	ctx := context.Background()
	fc.script(testOperation, dropResponse(), dropResponse())
	start.unknownReplays = 0
	_, err := start.Call(ctx, testOperation, opRequest(matrixKey, testInput))
	var uae *UnknownAckError
	if !errors.As(err, &uae) || !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("err = %v, want unknown outcome", err)
	}
	if uae.SubmissionKey != matrixKey {
		t.Fatalf("unknown outcome key = %q, want the original %q", uae.SubmissionKey, matrixKey)
	}
	if fc.eventCount() != 0 {
		t.Fatalf("events = %d, want none before reconciliation", fc.eventCount())
	}

	// The continuing transport reconciles: still nothing committed, so the
	// lookup refuses with not_found, and one identical resubmission under
	// the original key commits exactly once.
	contd := cont(t, fc, creds)
	_, lookupErr := contd.Call(ctx, CommandGetOperation, lookupRequest(matrixKey))
	_ = assertFault(t, lookupErr, contract.CodeNotFound)
	fc.script(testOperation, commitAndRespond(matrixKey,
		completedResult(testCommandID, `{"draft_id":"00000000-0000-4000-8000-000000000002"}`)))
	res, err := contd.Call(ctx, testOperation, opRequest(matrixKey, testInput))
	if err != nil {
		t.Fatalf("continuing transport resubmission: %v", err)
	}
	if res.CommandID != contract.ID(testCommandID) {
		t.Fatalf("resubmission envelope = %+v", res.Payload)
	}
	if fc.eventCount() != 1 {
		t.Fatalf("events = %d after reconciliation, want 1", fc.eventCount())
	}
	allKeysEqual(t, fc, testOperation, matrixKey)
	bodiesIdentical(t, fc, testOperation)
}

// matrixControllerRestart: the local endpoint restarts between the two
// transports; the continuing transport recovers the retained disposition by
// the original key and nothing re-executes.
func matrixControllerRestart(t *testing.T, start *Client, cont clientCtor, fc *fakeController, creds contract.CredentialSource) {
	t.Helper()
	const data = `{"draft_id":"00000000-0000-4000-8000-000000000002"}`
	fc.script(testOperation, dropAndCommit(matrixKey, testCommandID, data))
	res, err := start.Call(context.Background(), testOperation, opRequest(matrixKey, testInput))
	if err != nil {
		t.Fatalf("starting transport: %v", err)
	}
	// The controller's local endpoint restarts; durable state is retained.
	fc.StopUnix()
	fc.ListenUnix()
	looked, err := cont(t, fc, creds).Call(context.Background(), CommandGetOperation, lookupRequest(matrixKey))
	if err != nil {
		t.Fatalf("continuing transport after restart: %v", err)
	}
	assertEnvelopeEqual(t, &looked, completedResult(testCommandID, data))
	if looked.CommandID != res.CommandID {
		t.Fatalf("command identity changed across restart: %q vs %q", res.CommandID, looked.CommandID)
	}
}

// --- QUALIFICATION.named_agent_clients ------------------------------------------------

// TestQualificationNamedAgentClientsEnvelopeParity proves the client-local
// deliverable of acceptance case QUALIFICATION.named_agent_clients. The
// named-client qualification itself — retained interoperability evidence
// from real Claude Code, Codex and Cursor sessions performing setup, long
// work, denial and disconnected-mutation recovery — is release evidence
// owned by integration qualification and is deliberately NOT simulated
// here. What the client must guarantee those agents is envelope fidelity:
// every field a named agent client or its operator inspects (schema,
// command identity, status, data, fault, cursor) survives every disposition
// unmodified, and the CLI exit-code mapping agrees with the envelope a
// machine client received.
func TestQualificationNamedAgentClientsEnvelopeParity(t *testing.T) {
	t.Parallel()
	next := "cursor-2"
	cases := []struct {
		name     string
		op       string
		env      *contract.Result
		wantExit int
	}{
		{"completed", "task.create", &contract.Result{
			Schema:    contract.SchemaResult,
			CommandID: contract.ID(testCommandID),
			Payload:   contract.Payload{Status: contract.StatusCompleted, Data: json.RawMessage(`{"task_id":"00000000-0000-4000-8000-000000000003"}`)},
		}, 0},
		{"accepted job reference", "task.create", &contract.Result{
			Schema:    contract.SchemaResult,
			CommandID: contract.ID(testCommandID),
			Payload:   contract.Payload{Status: contract.StatusAccepted, Data: json.RawMessage(`{"job_id":"00000000-0000-4000-8000-000000000004"}`)},
		}, 0},
		{"paged page cursor", "events.list", &contract.Result{
			Schema:    contract.SchemaResult,
			CommandID: contract.ID(testCommandID),
			Payload:   contract.Payload{Status: contract.StatusCompleted, Data: json.RawMessage(`{"events":[]}`), NextCursor: &next},
		}, 0},
		{"denied", "task.create", failedResult(testCommandID, &contract.Fault{
			Code: contract.CodePermissionDenied, Message: "not permitted", Retryable: false,
		}), 3},
		{"conflicting state", "organization.update", failedResult(testCommandID, &contract.Fault{
			Code: contract.CodeSubmissionConflict, Message: "key reused with different input",
		}), 4},
		{"prerequisite missing", "organization.apply", failedResult(testCommandID, &contract.Fault{
			Code: contract.CodePrerequisiteMissing, Message: "base revision required",
		}), 5},
		{"unavailable", "task.create", failedResult(testCommandID, &contract.Fault{
			Code: contract.CodeControllerUnavailable, Message: "installation busy", Retryable: true,
		}), 6},
		{"other failure", "task.create", failedResult(testCommandID, &contract.Fault{
			Code: contract.CodeInternalError, Message: "internal error",
		}), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeController(t)
			fc.script(tc.op, func(*fakeController, string, []byte) scriptedResponse {
				return scriptedResponse{envelope: tc.env}
			})
			c := newLocalClient(t, fc, &staticCreds{value: testCred})
			res, err := c.Call(context.Background(), tc.op, opRequest("", `null`))
			assertEnvelopeEqual(t, &res, tc.env)
			if tc.env.Status == contract.StatusFailed {
				fault := assertFault(t, err, tc.env.Error.Code)
				if got := contract.CLIExit(fault); got != tc.wantExit {
					t.Fatalf("CLIExit(%s) = %d, want %d", fault.Code, got, tc.wantExit)
				}
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

// --- Local proving focus ----------------------------------------------------------------

// TestProveDisconnectBeforeCommitSingleEffect: a disconnect BEFORE the
// commit (nothing durable happened yet) recovers through the client's
// identical replay and produces exactly one business effect.
func TestProveDisconnectBeforeCommitSingleEffect(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	creds := &staticCreds{value: testCred}
	const key = "before-commit-key"
	const data = `{"draft_id":"00000000-0000-4000-8000-000000000002"}`

	// Two lost exchanges, then the commit lands on the client's third
	// identical attempt: one effect, no duplicate.
	fc.script(testOperation, dropResponse(), dropResponse(), commitAndRespond(key, completedResult(testCommandID, data)))
	c := newLocalClient(t, fc, creds)
	res, err := c.Call(context.Background(), testOperation, opRequest(key, testInput))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.CommandID != contract.ID(testCommandID) {
		t.Fatalf("envelope = %+v", res.Payload)
	}
	if fc.eventCount() != 1 {
		t.Fatalf("events = %d, want exactly 1", fc.eventCount())
	}

	// A later identical call deduplicates to the original disposition.
	again, err := c.Call(context.Background(), testOperation, opRequest(key, testInput))
	if err != nil {
		t.Fatalf("dedupe call: %v", err)
	}
	if again.CommandID != res.CommandID {
		t.Fatalf("dedupe minted new command %q", again.CommandID)
	}
	if fc.eventCount() != 1 {
		t.Fatalf("events = %d after dedupe, want 1", fc.eventCount())
	}
	bodiesIdentical(t, fc, testOperation)
	allKeysEqual(t, fc, testOperation, key)
}

// TestProveNoSecretInDiagnostics: no error class carries credential
// material, the wire carries the credential only in the Authorization
// header (never a request body), and the handed-out buffer is zeroed after
// use.
func TestProveNoSecretInDiagnostics(t *testing.T) {
	t.Parallel()
	creds := &staticCreds{value: testCred}
	ctx := context.Background()

	// 1. Controller unavailable: nothing is sent at all.
	unavailable, err := New(Config{SocketPath: shortTempSocket(t)}, creds)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	unavailable.connectAttempts = 2
	unavailable.backoff = func(int) time.Duration { return 0 }
	_, errUnavailable := unavailable.Call(ctx, testOperation, opRequest("diag-key", testInput))
	if !errors.Is(errUnavailable, ErrControllerUnavailable) {
		t.Fatalf("err = %v, want ErrControllerUnavailable", errUnavailable)
	}

	// 2. TLS verification failure: named, immediate, no retry.
	fcTLS := newFakeController(t)
	base, _ := fcTLS.ListenTLS()
	badTrust, err := New(Config{RemoteURL: base, TLSConfig: &tls.Config{RootCAs: x509.NewCertPool()}}, creds)
	if err != nil {
		t.Fatalf("new remote: %v", err)
	}
	_, errTLS := badTrust.Call(ctx, testOperation, opRequest("diag-key", testInput))
	if !errors.Is(errTLS, ErrTLSCertificate) {
		t.Fatalf("err = %v, want ErrTLSCertificate", errTLS)
	}

	// 3. Unknown outcome after lost acknowledgements.
	fcDrop := newFakeController(t)
	fcDrop.script(testOperation, dropResponse(), dropResponse())
	unknown := newLocalClient(t, fcDrop, creds)
	unknown.unknownReplays = 0
	_, errUnknown := unknown.Call(ctx, testOperation, opRequest("diag-key", testInput))
	var uae *UnknownAckError
	if !errors.As(errUnknown, &uae) {
		t.Fatalf("err = %v, want UnknownAckError", errUnknown)
	}

	// 4. Named fault envelope.
	fcFault := newFakeController(t)
	fcFault.script(testOperation, faultEnvelope(testCommandID, &contract.Fault{
		Code: contract.CodePermissionDenied, Message: "not permitted",
	}))
	_, errFault := newLocalClient(t, fcFault, creds).Call(ctx, testOperation, opRequest("diag-key", testInput))
	_ = assertFault(t, errFault, contract.CodePermissionDenied)

	// 5. Malformed envelope on a query.
	fcFault.script("events.list", raw("{not json", http.StatusOK))
	_, errMalformed := newLocalClient(t, fcFault, creds).Call(ctx, "events.list", opRequest("", `{"cursor":"c"}`))
	var uaq *UnknownAckError
	if !errors.As(errMalformed, &uaq) {
		t.Fatalf("err = %v, want UnknownAckError", errMalformed)
	}

	// 6. Credential source failure.
	_, errCreds := newLocalClient(t, newFakeController(t), failingCreds{err: errors.New("keychain locked")}).
		Call(ctx, testOperation, opRequest("k", `null`))
	if errCreds == nil || !strings.Contains(errCreds.Error(), "reading credential") {
		t.Fatalf("err = %v, want credential read failure", errCreds)
	}

	// No diagnostic string anywhere carries credential material.
	for i, e := range []error{errUnavailable, errTLS, errUnknown, errFault, errMalformed, errCreds} {
		if strings.Contains(e.Error(), "super-secret") || strings.Contains(e.Error(), testCred) {
			t.Fatalf("error %d carries credential material: %q", i, e.Error())
		}
	}
	// The wire: credential bytes appear only in the Authorization header,
	// never in a request body.
	for _, fc := range []*fakeController{fcTLS, fcDrop, fcFault} {
		for _, r := range fc.allRequests() {
			if got := r.Header.Get(AuthHeader); got != testCred {
				t.Fatalf("authorization %q on %s, want the credential header only", got, r.Operation)
			}
			if strings.Contains(string(r.Body), testCred) {
				t.Fatalf("request body for %s carries credential material", r.Operation)
			}
		}
	}
	// The last handed-out buffer was scrubbed after use.
	for i, b := range creds.last {
		if b != 0 {
			t.Fatalf("credential buffer byte %d not zeroed: %d", i, b)
		}
	}
}
