package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

const (
	testOperation = "organization.create"
	testInput     = `{"key":"demo","name":"Demo organization"}`
	testCred      = "Bearer test-credential-material"
)

// dropAndCommit builds a responder that records the durable mutation and its
// exact request bytes, then drops the connection without a response — the
// lost-acknowledgement seam every replay test drives.
func dropAndCommit(key, commandID, data string) responder {
	return func(fc *fakeController, op string, body []byte) scriptedResponse {
		return scriptedResponse{
			beforeRsp: func() {
				fc.commit(op, key, body, *completedResult(commandID, data))
			},
			drop: true,
		}
	}
}

func dropResponse() responder {
	return func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{drop: true}
	}
}

func TestNewConfigValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  Config
	}{
		{"both endpoints", Config{SocketPath: "/tmp/x.sock", RemoteURL: "https://example.invalid"}},
		{"no endpoint", Config{}},
		{"tls on local socket", Config{SocketPath: "/tmp/x.sock", TLSConfig: &tls.Config{}}},
		{"remote not https", Config{RemoteURL: "http://controller.example.invalid"}},
		{"remote without host", Config{RemoteURL: "https://"}},
		{"negative timeout", Config{SocketPath: "/tmp/x.sock", Timeout: -time.Second}},
		{"insecure skip verify", Config{RemoteURL: "https://controller.example.invalid", TLSConfig: &tls.Config{InsecureSkipVerify: true}}},
		{"invalid url", Config{RemoteURL: "://bad"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(tc.cfg, nil)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("err = %v, want ErrInvalidRequest", err)
			}
		})
	}
	t.Run("valid local", func(t *testing.T) {
		t.Parallel()
		if _, err := New(Config{SocketPath: "/tmp/x.sock"}, nil); err != nil {
			t.Fatalf("local config rejected: %v", err)
		}
	})
	t.Run("valid remote", func(t *testing.T) {
		t.Parallel()
		if _, err := New(Config{RemoteURL: "https://controller.example.invalid"}, nil); err != nil {
			t.Fatalf("remote config rejected: %v", err)
		}
	})
}

func TestCallCompletedRoundTrip(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	fc.script(testOperation, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `{"draft_id":"00000000-0000-4000-8000-000000000002"}`)}
	})
	creds := &staticCreds{value: testCred}
	c := newLocalClient(t, fc, creds)

	res, err := c.Call(context.Background(), testOperation, opRequest("demo-key-1", testInput))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.Schema != contract.SchemaResult {
		t.Fatalf("schema = %q", res.Schema)
	}
	if res.CommandID != contract.ID(testCommandID) || res.Status != contract.StatusCompleted {
		t.Fatalf("envelope = %+v", res.Payload)
	}
	if creds.calls.Load() != 1 {
		t.Fatalf("credential fetched %d times, want 1 per call", creds.calls.Load())
	}
	reqs := fc.allRequests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if got := reqs[0].Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
	}
	if got := reqs[0].Header.Get(AuthHeader); got != testCred {
		t.Fatalf("credential header = %q", got)
	}
	var sent contract.Request
	if err := contract.DecodeStrict(reqs[0].Body, &sent); err != nil {
		t.Fatalf("sent body is not a strict envelope: %v", err)
	}
	if sent.Schema != contract.SchemaRequest || sent.SubmissionKey != "demo-key-1" || string(sent.Input) != testInput {
		t.Fatalf("sent envelope = %+v", sent)
	}
}

func TestCallAcceptedRoundTrip(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	fc.script("task.create", func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: acceptedResult(testCommandID, `{"task_id":"00000000-0000-4000-8000-000000000009"}`)}
	})
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	res, err := c.Call(context.Background(), "task.create", opRequest("", `{"title":"Ship"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.Status != contract.StatusAccepted || res.CommandID != contract.ID(testCommandID) {
		t.Fatalf("envelope = %+v", res.Payload)
	}
}

func TestDotSeparatedOperationInPath(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	fc.script("_evidence.command.finish", func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `null`)}
	})
	c := newLocalClient(t, fc, nil)
	if _, err := c.Call(context.Background(), "_evidence.command.finish", opRequest("", `null`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	reqs := fc.allRequests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	if want := operationsPathPrefix + "_evidence.command.finish"; !strings.HasSuffix(reqs[0].Operation, "_evidence.command.finish") || reqs[0].Operation != "_evidence.command.finish" {
		t.Fatalf("operation = %q, want %q", reqs[0].Operation, want)
	}
}

func TestCredentialPerRequestAndZeroed(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	fc.script(testOperation, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `null`)}
	}, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `null`)}
	})
	creds := &staticCreds{value: testCred}
	c := newLocalClient(t, fc, creds)

	for i := 0; i < 2; i++ {
		if _, err := c.Call(context.Background(), testOperation, opRequest("", `null`)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := creds.calls.Load(); got != 2 {
		t.Fatalf("credential fetched %d times for 2 calls", got)
	}
	for i, b := range creds.last {
		if b != 0 {
			t.Fatalf("credential buffer byte %d not zeroed after use: %d", i, b)
		}
	}
}

func TestCredentialFetchFailureSendsNothing(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	c := newLocalClient(t, fc, failingCreds{err: errors.New("keychain locked")})

	_, err := c.Call(context.Background(), testOperation, opRequest("k", `null`))
	if err == nil || !strings.Contains(err.Error(), "reading credential") {
		t.Fatalf("err = %v, want credential read failure", err)
	}
	if fc.requestCount("") != 0 {
		t.Fatalf("requests sent despite credential failure: %d", fc.requestCount(""))
	}
}

func TestNilCredentialSourceSendsNoHeader(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	fc.script(testOperation, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `null`)}
	})
	c := newLocalClient(t, fc, nil)
	if _, err := c.Call(context.Background(), testOperation, opRequest("", `null`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	reqs := fc.allRequests()
	if _, ok := reqs[0].Header[http.CanonicalHeaderKey(AuthHeader)]; ok {
		t.Fatalf("credential header present without a credential source")
	}
}

func TestFaultSurfacesAsError(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	fault := &contract.Fault{Code: contract.CodePermissionDenied, Message: "not permitted", Retryable: false}
	fc.script(testOperation, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: failedResult(testCommandID, fault)}
	})
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	res, err := c.Call(context.Background(), testOperation, opRequest("demo-key-1", testInput))
	var got *contract.Fault
	if !errors.As(err, &got) {
		t.Fatalf("err = %v, want *contract.Fault", err)
	}
	if got.Code != contract.CodePermissionDenied || got.Message != "not permitted" || got.Retryable {
		t.Fatalf("fault = %+v", got)
	}
	if res.Status != contract.StatusFailed || res.CommandID != contract.ID(testCommandID) {
		t.Fatalf("envelope = %+v", res.Payload)
	}
	if contract.CLIExit(got) != 3 {
		t.Fatalf("CLIExit = %d, want 3", contract.CLIExit(got))
	}
	if fc.requestCount(testOperation) != 1 {
		t.Fatalf("authoritative fault retried: %d requests", fc.requestCount(testOperation))
	}
}

func TestCursorExpiredSurfacesSnapshotRequired(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		details string
		want    bool
	}{
		{"snapshot required", `{"snapshot_required":true}`, true},
		{"snapshot not required", `{"snapshot_required":false}`, false},
		{"no details", ``, false},
		{"extra detail fields tolerated", `{"snapshot_required":true,"window":"7d"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeController(t)
			fault := &contract.Fault{Code: contract.CodeCursorExpired, Message: "cursor outside replay window"}
			if tc.details != "" {
				fault.Details = json.RawMessage(tc.details)
			}
			fc.script("events.list", func(*fakeController, string, []byte) scriptedResponse {
				return scriptedResponse{envelope: failedResult(testCommandID, fault)}
			})
			c := newLocalClient(t, fc, &staticCreds{value: testCred})

			_, err := c.Call(context.Background(), "events.list", opRequest("", `{"cursor":"opaque-1"}`))
			var ce *CursorExpiredError
			if !errors.As(err, &ce) {
				t.Fatalf("err = %v (%T), want CursorExpiredError", err, err)
			}
			if ce.SnapshotRequired != tc.want {
				t.Fatalf("snapshot_required = %v, want %v", ce.SnapshotRequired, tc.want)
			}
			if ce.Fault == nil || ce.Fault.Code != contract.CodeCursorExpired {
				t.Fatalf("wrapped fault = %+v", ce.Fault)
			}
			if fc.requestCount("events.list") != 1 {
				t.Fatalf("cursor expiry triggered extra requests: %d", fc.requestCount("events.list"))
			}
		})
	}
}

func TestRequestValidationTable(t *testing.T) {
	t.Parallel()
	longKey := strings.Repeat("k", submissionKeyMax+1)
	cases := []struct {
		name      string
		operation string
		req       contract.Request
	}{
		{"empty operation", "", opRequest("", `null`)},
		{"uppercase operation", "Organization.Create", opRequest("", `null`)},
		{"slash in operation", "organization/create", opRequest("", `null`)},
		{"leading dot", ".organization.create", opRequest("", `null`)},
		{"double dot", "organization..create", opRequest("", `null`)},
		{"trailing dot", "organization.create.", opRequest("", `null`)},
		{"too long", strings.Repeat("o", operationIDMax+1), opRequest("", `null`)},
		{"wrong schema", testOperation, contract.Request{Schema: "other/v1", Input: json.RawMessage(`null`)}},
		{"key too long", testOperation, opRequest(longKey, `null`)},
		{"key with control byte", testOperation, opRequest("key\nnewline", `null`)},
		{"input duplicate key", testOperation, opRequest("", `{"a":1,"a":2}`)},
		{"input trailing data", testOperation, opRequest("", `{"a":1} trailing`)},
		{"input not json", testOperation, opRequest("", `not-json`)},
		{"input integer overflow", testOperation, opRequest("", `{"n":9223372036854775808}`)},
		{"input bad utf8 escape", testOperation, opRequest("", `{"s":"\ud800 lone"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeController(t)
			c := newLocalClient(t, fc, &staticCreds{value: testCred})
			_, err := c.Call(context.Background(), tc.operation, tc.req)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("err = %v, want ErrInvalidRequest", err)
			}
			if fc.requestCount("") != 0 {
				t.Fatalf("invalid request was sent to the controller")
			}
		})
	}
}

func TestResponseValidationTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script responder
	}{
		{"wrong schema", raw(`{"schema":"other/v1","command_id":"`+testCommandID+`","status":"completed","data":null,"error":null,"next_cursor":null}`, http.StatusOK)},
		{"missing command id", raw(`{"schema":"zatiti.result/v1","status":"completed","data":null,"error":null,"next_cursor":null}`, http.StatusOK)},
		{"unknown status", raw(`{"schema":"zatiti.result/v1","command_id":"`+testCommandID+`","status":"pending","data":null,"error":null,"next_cursor":null}`, http.StatusOK)},
		{"completed with fault", raw(`{"schema":"zatiti.result/v1","command_id":"`+testCommandID+`","status":"completed","data":null,"error":{"code":"internal_error","message":"x","retryable":false},"next_cursor":null}`, http.StatusOK)},
		{"failed without fault", raw(`{"schema":"zatiti.result/v1","command_id":"`+testCommandID+`","status":"failed","data":null,"error":null,"next_cursor":null}`, http.StatusConflict)},
		{"status disagrees with http", raw(`{"schema":"zatiti.result/v1","command_id":"`+testCommandID+`","status":"accepted","data":null,"error":null,"next_cursor":null}`, http.StatusOK)},
		{"http 202 with completed", raw(`{"schema":"zatiti.result/v1","command_id":"`+testCommandID+`","status":"completed","data":null,"error":null,"next_cursor":null}`, http.StatusAccepted)},
		{"duplicate key", raw(`{"schema":"zatiti.result/v1","command_id":"`+testCommandID+`","command_id":"x","status":"completed","data":null,"error":null,"next_cursor":null}`, http.StatusOK)},
		{"unknown field", raw(`{"schema":"zatiti.result/v1","command_id":"`+testCommandID+`","status":"completed","data":null,"error":null,"next_cursor":null,"extra":1}`, http.StatusOK)},
		{"trailing data", raw(`{"schema":"zatiti.result/v1","command_id":"`+testCommandID+`","status":"completed","data":null,"error":null,"next_cursor":null} {}`, http.StatusOK)},
		{"redirect refused", raw(``, http.StatusFound)},
		{"http 401 unmapped", raw(``, http.StatusUnauthorized)},
		{"oversized body", oversizedBody()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeController(t)
			fc.script("capabilities.list", tc.script)
			c := newLocalClient(t, fc, &staticCreds{value: testCred})
			_, err := c.Call(context.Background(), "capabilities.list", opRequest("", `null`))
			if err == nil {
				t.Fatalf("protocol violation accepted")
			}
			if fc.requestCount("capabilities.list") != 1 {
				t.Fatalf("protocol violation triggered extra requests: %d", fc.requestCount("capabilities.list"))
			}
		})
	}
}

// raw scripts one raw HTTP response, bypassing envelope construction.
func raw(body string, status int) responder {
	return func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{raw: body, rawStatus: status}
	}
}

func oversizedBody() responder {
	return func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{raw: strings.Repeat("x", maxResponseBytes+1), rawStatus: http.StatusOK}
	}
}

func TestConnectRetriesBeforeFirstByte(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	path := shortTempSocket(t)
	c, err := New(Config{SocketPath: path}, &staticCreds{value: testCred})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	c.connectAttempts = 3
	var backoffs int
	c.backoff = func(int) time.Duration {
		// The controller comes up exactly after the first dial failure, so
		// attempt 1 deterministically fails before any byte was sent and
		// attempt 2 succeeds.
		fc.ListenUnixAt(path)
		backoffs++
		return 0
	}
	fc.script(testOperation, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `null`)}
	})

	res, err := c.Call(context.Background(), testOperation, opRequest("k", `null`))
	if err != nil {
		t.Fatalf("call after reconnect: %v", err)
	}
	if res.Status != contract.StatusCompleted {
		t.Fatalf("status = %q", res.Status)
	}
	if backoffs != 1 {
		t.Fatalf("backoffs = %d, want exactly one failed connection attempt", backoffs)
	}
	if fc.requestCount(testOperation) != 1 {
		t.Fatalf("requests = %d, want 1 (the retried dial sent nothing)", fc.requestCount(testOperation))
	}
}

func TestConnectExhaustedReturnsUnavailable(t *testing.T) {
	t.Parallel()
	c, err := New(Config{SocketPath: filepath.Join(t.TempDir(), "absent.sock")}, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	c.connectAttempts = 4
	c.backoff = func(int) time.Duration { return 0 }

	_, err = c.Call(context.Background(), testOperation, opRequest("k", `null`))
	if !errors.Is(err, ErrControllerUnavailable) {
		t.Fatalf("err = %v, want ErrControllerUnavailable", err)
	}
	if errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("connect failure must never be an unknown acknowledgement")
	}
}

func TestTLSRemoteRoundTrip(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	fc.script(testOperation, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `{"ok":true}`)}
	})
	c := newRemoteClient(t, fc, &staticCreds{value: testCred})

	res, err := c.Call(context.Background(), testOperation, opRequest("k", testInput))
	if err != nil {
		t.Fatalf("remote tls call: %v", err)
	}
	if res.Status != contract.StatusCompleted || res.CommandID != contract.ID(testCommandID) {
		t.Fatalf("envelope = %+v", res.Payload)
	}
	reqs := fc.allRequests()
	if len(reqs) != 1 || reqs[0].Header.Get(AuthHeader) != testCred {
		t.Fatalf("remote request = %+v", reqs)
	}
}

func TestTLSVerificationFailureSurfacesImmediately(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	base, _ := fc.ListenTLS()
	// System roots do not trust the fake's CA: verification fails.
	c, err := New(Config{RemoteURL: base}, &staticCreds{value: testCred})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	c.connectAttempts = 3
	c.unknownReplays = 2
	c.backoff = func(int) time.Duration { return 0 }

	_, err = c.Call(context.Background(), testOperation, opRequest("k", testInput))
	if !errors.Is(err, ErrTLSCertificate) {
		t.Fatalf("err = %v, want ErrTLSCertificate", err)
	}
	if got := fc.handshakeCount(); got != 1 {
		t.Fatalf("TLS handshakes = %d, want 1 (verification failures never retry)", got)
	}
}

func TestTimeoutBoundsExchange(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	fc.script("capabilities.list", func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{hang: 2 * time.Second}
	})
	c, err := New(Config{SocketPath: fc.ListenUnix(), Timeout: 50 * time.Millisecond}, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	c.connectAttempts = 3
	c.unknownReplays = 2
	c.backoff = func(int) time.Duration { return 0 }

	_, err = c.Call(context.Background(), "capabilities.list", opRequest("", `null`))
	if !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("err = %v, want ErrUnknownOutcome", err)
	}
	if fc.requestCount("capabilities.list") != 1 {
		t.Fatalf("requests = %d, want 1 (timeout retries are the caller's job)", fc.requestCount("capabilities.list"))
	}
}

func TestCancellationBeforeSendSendsNothing(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	c := newLocalClient(t, fc, &staticCreds{value: testCred})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Call(ctx, testOperation, opRequest("k", `null`))
	if !errors.Is(err, ErrControllerUnavailable) {
		t.Fatalf("err = %v, want ErrControllerUnavailable", err)
	}
	if fc.requestCount("") != 0 {
		t.Fatalf("cancelled context sent requests")
	}
}

func TestUnknownAckReplaysIdenticalKeyedSubmission(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "demo-org-create-001"
	const committed = "00000000-0000-4000-8000-000000000001"
	fc.script(testOperation, dropAndCommit(key, committed, `{"draft_id":"00000000-0000-4000-8000-000000000002"}`))
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	res, err := c.Call(context.Background(), testOperation, opRequest(key, testInput))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.CommandID != contract.ID(committed) || res.Status != contract.StatusCompleted {
		t.Fatalf("recovered envelope = %+v", res.Payload)
	}
	if got := fc.requestCount(testOperation); got != 2 {
		t.Fatalf("requests = %d, want original plus one identical replay", got)
	}
	if got := fc.eventCount(); got != 1 {
		t.Fatalf("business mutations = %d, want exactly 1", got)
	}
	reqs := fc.allRequests()
	if !equalBytes(reqs[0].Body, reqs[1].Body) {
		t.Fatalf("replay bytes differ:\n%s\n%s", reqs[0].Body, reqs[1].Body)
	}
}

func TestUnknownAckResolvedByCommandLookup(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "demo-org-create-002"
	const committed = "00000000-0000-4000-8000-000000000003"
	fc.script(testOperation, dropAndCommit(key, committed, `{"draft_id":"00000000-0000-4000-8000-000000000004"}`))
	c := newLocalClient(t, fc, &staticCreds{value: testCred})
	c.unknownReplays = 0 // every submission exchange is lost; only the lookup resolves

	res, err := c.Call(context.Background(), testOperation, opRequest(key, testInput))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.CommandID != contract.ID(committed) {
		t.Fatalf("recovered envelope = %+v", res.Payload)
	}
	if got := fc.eventCount(); got != 1 {
		t.Fatalf("business mutations = %d, want exactly 1", got)
	}
	lookups := 0
	for _, r := range fc.allRequests() {
		if r.Operation != CommandGetOperation {
			continue
		}
		lookups++
		var req contract.Request
		if err := contract.DecodeStrict(r.Body, &req); err != nil {
			t.Fatalf("lookup body invalid: %v", err)
		}
		if req.SubmissionKey != "" {
			t.Fatalf("lookup must not carry a submission key of its own")
		}
		var input struct {
			SubmissionKey string `json:"submission_key"`
		}
		if err := json.Unmarshal(req.Input, &input); err != nil {
			t.Fatalf("lookup input invalid: %v", err)
		}
		if input.SubmissionKey != key {
			t.Fatalf("lookup input = %+v, want the original key %q", input, key)
		}
	}
	if lookups != 1 {
		t.Fatalf("command lookups = %d, want 1", lookups)
	}
}

func TestUnknownAckNotFoundLicensesFinalReplay(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "demo-org-create-003"
	const committed = "00000000-0000-4000-8000-000000000005"
	// First exchange: bytes received, connection dropped before processing.
	fc.script(testOperation, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{drop: true}
	})
	// Final replay after the not_found lookup: commit and answer.
	fc.script(testOperation, func(fc *fakeController, op string, body []byte) scriptedResponse {
		return scriptedResponse{
			envelope: completedResult(committed, `{"draft_id":"00000000-0000-4000-8000-000000000006"}`),
			beforeRsp: func() {
				fc.commit(op, key, body, *completedResult(committed, `{"draft_id":"00000000-0000-4000-8000-000000000006"}`))
			},
		}
	})
	c := newLocalClient(t, fc, &staticCreds{value: testCred})
	c.unknownReplays = 0

	res, err := c.Call(context.Background(), testOperation, opRequest(key, testInput))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.CommandID != contract.ID(committed) {
		t.Fatalf("envelope = %+v", res.Payload)
	}
	if got := fc.requestCount(CommandGetOperation); got != 1 {
		t.Fatalf("lookups = %d, want 1", got)
	}
	if got := fc.requestCount(testOperation); got != 2 {
		t.Fatalf("submissions = %d, want initial plus final replay", got)
	}
	if got := fc.eventCount(); got != 1 {
		t.Fatalf("business mutations = %d, want exactly 1", got)
	}
}

func TestUnknownAckUnresolvable(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "demo-org-create-004"
	// Every submission attempt drops; the command never committed, and the
	// final replay into a still-down controller fails again.
	fc.script(testOperation, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{drop: true}
	}, func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{drop: true}
	})
	c := newLocalClient(t, fc, &staticCreds{value: testCred})
	c.unknownReplays = 0

	res, err := c.Call(context.Background(), testOperation, opRequest(key, testInput))
	if res.CommandID != "" {
		t.Fatalf("unexpected result on unresolvable ack: %+v", res)
	}
	var uae *UnknownAckError
	if !errors.As(err, &uae) {
		t.Fatalf("err = %v, want UnknownAckError", err)
	}
	if !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("err = %v, want ErrUnknownOutcome class", err)
	}
	if uae.Operation != testOperation || uae.SubmissionKey != key {
		t.Fatalf("unknown ack = %+v", uae)
	}
	if got := fc.eventCount(); got != 0 {
		t.Fatalf("business mutations = %d, want 0", got)
	}
}

func TestNonKeyedLossIsNotRetried(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	fc.script("capabilities.list", func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{drop: true}
	})
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	_, err := c.Call(context.Background(), "capabilities.list", opRequest("", `null`))
	var uae *UnknownAckError
	if !errors.As(err, &uae) {
		t.Fatalf("err = %v, want UnknownAckError", err)
	}
	if uae.SubmissionKey != "" {
		t.Fatalf("query unknown-ack carries a submission key: %+v", uae)
	}
	if got := fc.requestCount("capabilities.list"); got != 1 {
		t.Fatalf("requests = %d, want 1 (queries are never replayed)", got)
	}
}

func TestCancellationAfterSendIsNotCommandCancellation(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "demo-org-create-005"
	const committed = "00000000-0000-4000-8000-000000000007"
	fc.script(testOperation, func(fc *fakeController, op string, body []byte) scriptedResponse {
		return scriptedResponse{
			beforeRsp: func() { fc.commit(op, key, body, *completedResult(committed, `{"task_id":"t-1"}`)) },
			hang:      2 * time.Second,
		}
	})
	c := newLocalClient(t, fc, &staticCreds{value: testCred})
	c.unknownReplays = 2

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.Call(ctx, testOperation, opRequest(key, testInput))
	if !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("err = %v, want ErrUnknownOutcome", err)
	}
	// The client must not keep using the dead context: no replay, no lookup.
	if got := fc.requestCount(testOperation); got != 1 {
		t.Fatalf("submissions = %d, want 1", got)
	}
	if got := fc.requestCount(CommandGetOperation); got != 0 {
		t.Fatalf("lookups on dead context = %d, want 0", got)
	}
	// The command committed server-side; the caller recovers it by key.
	res, err := c.Call(context.Background(), CommandGetOperation, contract.Request{
		Schema: contract.SchemaRequest,
		Input:  json.RawMessage(`{"submission_key":"` + key + `"}`),
	})
	if err != nil {
		t.Fatalf("lookup after cancellation: %v", err)
	}
	if res.CommandID != contract.ID(committed) || res.Status != contract.StatusCompleted {
		t.Fatalf("command was not durably accepted before cancellation: %+v", res.Payload)
	}
}

func TestCallConcurrent(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	creds := &staticCreds{value: testCred}
	c := newLocalClient(t, fc, creds)

	const workers, calls = 8, 4
	for w := 0; w < workers; w++ {
		op := fmt.Sprintf("load.op.%d", w)
		for i := 0; i < calls; i++ {
			fc.script(op, func(*fakeController, string, []byte) scriptedResponse {
				return scriptedResponse{envelope: completedResult(testCommandID, `null`)}
			})
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			op := fmt.Sprintf("load.op.%d", w)
			for i := 0; i < calls; i++ {
				if _, err := c.Call(context.Background(), op, opRequest(fmt.Sprintf("w%d-c%d", w, i), `null`)); err != nil {
					errs[w] = err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	for w, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", w, err)
		}
	}
	if got := creds.calls.Load(); got != workers*calls {
		t.Fatalf("credential fetches = %d, want %d", got, workers*calls)
	}
}

func TestSubmissionConflictSurfaces(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "demo-org-create-006"
	// The controller committed the original request; a changed input under
	// the same key must surface submission_conflict, never a silent retry.
	fc.script(testOperation, func(fc *fakeController, op string, body []byte) scriptedResponse {
		return scriptedResponse{envelope: completedResult(testCommandID, `{"draft_id":"d-1"}`)}
	})
	fc.commit(testOperation, key, []byte(`{"schema":"zatiti.request/v1","submission_key":"`+key+`","input":{"other":"bytes"}}`), *completedResult(testCommandID, `{"draft_id":"d-0"}`))
	c := newLocalClient(t, fc, &staticCreds{value: testCred})

	res, err := c.Call(context.Background(), testOperation, opRequest(key, `{"name":"Different"}`))
	var fault *contract.Fault
	if !errors.As(err, &fault) || fault.Code != contract.CodeSubmissionConflict {
		t.Fatalf("err = %v, want submission_conflict", err)
	}
	if res.Status != contract.StatusFailed {
		t.Fatalf("envelope = %+v", res.Payload)
	}
}
