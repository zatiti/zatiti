package client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// lostAck scripts one exchange whose request bytes arrive and whose
// acknowledgement is lost without any commit.
func lostAck(*fakeController, string, []byte) scriptedResponse {
	return scriptedResponse{drop: true}
}

// lookupInputs returns the decoded command.get inputs the fake received.
func lookupInputs(t *testing.T, fc *fakeController) []map[string]json.RawMessage {
	t.Helper()
	var inputs []map[string]json.RawMessage
	for _, r := range fc.allRequests() {
		if r.Operation != CommandGetOperation {
			continue
		}
		var req contract.Request
		if err := contract.DecodeStrict(r.Body, &req); err != nil {
			t.Fatalf("lookup body invalid: %v", err)
		}
		if req.SubmissionKey != "" {
			t.Fatalf("lookup must not carry a submission key of its own")
		}
		var input map[string]json.RawMessage
		if err := json.Unmarshal(req.Input, &input); err != nil {
			t.Fatalf("lookup input invalid: %v", err)
		}
		inputs = append(inputs, input)
	}
	return inputs
}

// TestCommandLookupSendsFrozenInput: the recovery lookup carries exactly the
// frozen command.get input (scope, submission_key, operation,
// operation_version) and the recovered disposition is the retained original
// envelope from data.resource.result, not the lookup's own envelope.
func TestCommandLookupSendsFrozenInput(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "lookup-frozen-001"
	const committed = "00000000-0000-4000-8000-000000000021"
	fc.script(testOperation, dropAndCommit(key, committed, `{"draft_id":"00000000-0000-4000-8000-000000000022"}`))
	c := newLocalClient(t, fc, &staticCreds{value: testCred})
	c.unknownReplays = 0

	res, err := c.Call(context.Background(), testOperation, opRequest(key, testInput))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.CommandID != contract.ID(committed) || res.Status != contract.StatusCompleted {
		t.Fatalf("recovered envelope = %+v, want the retained original %s", res, committed)
	}
	if string(res.Data) != `{"draft_id":"00000000-0000-4000-8000-000000000022"}` {
		t.Fatalf("recovered data = %s, want the original command's data", res.Data)
	}
	inputs := lookupInputs(t, fc)
	if len(inputs) != 1 {
		t.Fatalf("lookups = %d, want 1", len(inputs))
	}
	want := map[string]string{
		"scope":             `{"installation_id":"` + testInstallation + `"}`,
		"submission_key":    `"` + key + `"`,
		"operation":         `"` + testOperation + `"`,
		"operation_version": `1`,
	}
	if len(inputs[0]) != len(want) {
		t.Fatalf("lookup input fields = %v, want exactly %v", inputs[0], want)
	}
	for field, value := range want {
		if got := string(inputs[0][field]); got != value {
			t.Errorf("lookup input %s = %s, want %s", field, got, value)
		}
	}
}

// TestCommandLookupRecoversRetainedRefusal: a refused command's retained
// fault is recovered as that command's disposition, with its own command
// identity.
func TestCommandLookupRecoversRetainedRefusal(t *testing.T) {
	t.Parallel()
	fc := newFakeController(t)
	const key = "lookup-refusal-001"
	const committed = "00000000-0000-4000-8000-000000000023"
	refusal := failedResult(committed, &contract.Fault{Code: contract.CodeConflict, Message: "organization key already exists"})
	fc.script(testOperation, func(fc *fakeController, op string, body []byte) scriptedResponse {
		return scriptedResponse{drop: true, beforeRsp: func() { fc.commit(op, key, body, *refusal) }}
	})
	c := newLocalClient(t, fc, &staticCreds{value: testCred})
	c.unknownReplays = 0

	res, err := c.Call(context.Background(), testOperation, opRequest(key, testInput))
	fault := assertFault(t, err, contract.CodeConflict)
	if fault.Message != "organization key already exists" {
		t.Fatalf("fault = %+v, want the retained refusal", fault)
	}
	if res.CommandID != contract.ID(committed) || res.Status != contract.StatusFailed {
		t.Fatalf("recovered envelope = %+v, want the retained refusal %s", res, committed)
	}
}

// TestFailedLookupSurfacesUnknownOutcome: a lookup that itself fails (any
// fault other than not_found) says nothing about the original command. The
// client reports the outcome as still unknown, never the lookup's fault as
// the command's own result.
func TestFailedLookupSurfacesUnknownOutcome(t *testing.T) {
	t.Parallel()
	for _, code := range []string{
		contract.CodeInvalidInput,
		contract.CodePermissionDenied,
		contract.CodeControllerUnavailable,
		contract.CodeInternalError,
	} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			fc := newFakeController(t)
			const key = "lookup-failed-001"
			fc.script(testOperation, lostAck)
			fc.script(CommandGetOperation, faultEnvelope("cmd-lookup", &contract.Fault{Code: code, Message: "lookup refused"}))
			c := newLocalClient(t, fc, &staticCreds{value: testCred})
			c.unknownReplays = 0

			res, err := c.Call(context.Background(), testOperation, opRequest(key, testInput))
			if !errors.Is(err, ErrUnknownOutcome) {
				t.Fatalf("err = %v, want ErrUnknownOutcome", err)
			}
			var unknown *UnknownAckError
			if !errors.As(err, &unknown) || unknown.SubmissionKey != key || unknown.Operation != testOperation {
				t.Fatalf("err = %#v, want UnknownAckError for %s/%s", err, testOperation, key)
			}
			var fault *contract.Fault
			if errors.As(err, &fault) {
				t.Fatalf("the lookup's fault %q must not be reachable as the command's fault", fault.Code)
			}
			if res.Schema != "" || res.CommandID != "" || res.Status != "" {
				t.Fatalf("result = %+v, want the zero envelope for an unknown outcome", res)
			}
			if got := fc.requestCount(testOperation); got != 1 {
				t.Fatalf("submissions = %d, want 1: a failed lookup licenses no replay", got)
			}
		})
	}
}

// TestLookupWithoutScopeStaysUnknown: command.get requires a scope, and the
// client only knows the one the original input named. Without it no frozen
// lookup can be formed, so nothing is sent and the outcome stays unknown.
func TestLookupWithoutScopeStaysUnknown(t *testing.T) {
	t.Parallel()
	for name, input := range map[string]string{
		"no scope":         `{"key":"demo","name":"Demo organization"}`,
		"scope not object": `{"scope":"everything"}`,
		"input not object": `["demo"]`,
		"null input":       `null`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeController(t)
			fc.script(testOperation, lostAck)
			c := newLocalClient(t, fc, &staticCreds{value: testCred})
			c.unknownReplays = 0

			_, err := c.Call(context.Background(), testOperation, opRequest("lookup-noscope-001", input))
			if !errors.Is(err, ErrUnknownOutcome) {
				t.Fatalf("err = %v, want ErrUnknownOutcome", err)
			}
			if got := fc.requestCount(CommandGetOperation); got != 0 {
				t.Fatalf("lookups = %d, want 0: no frozen lookup can be formed without a scope", got)
			}
		})
	}
}

// TestLookupResourceMismatchStaysUnknown: a completed lookup whose resource
// is not the command that was asked for (or is not a well-formed retained
// envelope) proves nothing about the original command.
func TestLookupResourceMismatchStaysUnknown(t *testing.T) {
	t.Parallel()
	const key = "lookup-mismatch-001"
	const committed = "00000000-0000-4000-8000-000000000024"
	original := storedCommand{operation: testOperation, envelope: *completedResult(committed, `{"ok":true}`)}
	mutate := func(edit func(*fakeCommand)) string {
		env := commandResource(key, original)
		var body struct {
			Resource fakeCommand `json:"resource"`
		}
		if err := json.Unmarshal(env.Data, &body); err != nil {
			t.Fatalf("resource: %v", err)
		}
		edit(&body.Resource)
		return mustJSON(body)
	}
	cases := map[string]string{
		"other key":          mutate(func(c *fakeCommand) { c.SubmissionKey = "someone-elses-key" }),
		"other operation":    mutate(func(c *fakeCommand) { c.Operation = "team.create" }),
		"command id differs": mutate(func(c *fakeCommand) { c.ID = "00000000-0000-4000-8000-0000000000ff" }),
		"no result schema":   mutate(func(c *fakeCommand) { c.Result.Schema = "" }),
		"failed no fault":    mutate(func(c *fakeCommand) { c.Result.Status = contract.StatusFailed }),
		"no resource":        `{}`,
		"unknown field":      `{"resource":null,"extra":1}`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeController(t)
			fc.script(testOperation, lostAck)
			fc.script(CommandGetOperation, func(*fakeController, string, []byte) scriptedResponse {
				return scriptedResponse{envelope: completedResult("cmd-lookup", data)}
			})
			c := newLocalClient(t, fc, &staticCreds{value: testCred})
			c.unknownReplays = 0

			res, err := c.Call(context.Background(), testOperation, opRequest(key, testInput))
			if !errors.Is(err, ErrUnknownOutcome) {
				t.Fatalf("err = %v (result %+v), want ErrUnknownOutcome", err, res)
			}
			if res.CommandID != "" {
				t.Fatalf("result = %+v, want the zero envelope", res)
			}
			if strings.Contains(err.Error(), testCred) {
				t.Fatalf("diagnostic leaks the credential: %v", err)
			}
		})
	}
}
