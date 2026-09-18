package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// registerReplayedRefusal adds a keyed mutation whose command identity is
// already bound to a retained refusal: the fake evidence owner answers
// _evidence.command.begin with the existing failed command, so
// application.Invoke replays that disposition instead of running the
// handler. It returns the retained command identity.
func registerReplayedRefusal(t *testing.T, env *testEnv, operation string, fault contract.Fault) contract.ID {
	t.Helper()
	commandID := contract.NewID()
	env.cat.descriptors[operation] = contract.Descriptor{
		ID: operation, Version: 1, Owner: "widgets",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation,
		InputSchema:   []byte(pingInputSchema),
		SubmissionKey: true,
	}
	env.cat.handlers[operation] = func(context.Context, contract.Unit, contract.Invocation) (contract.Payload, error) {
		t.Errorf("%s handler ran; a replayed refusal must not re-execute", operation)
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: "handler must not run"}
	}
	env.cat.descriptors["_evidence.command.begin"] = contract.Descriptor{
		ID: "_evidence.command.begin", Version: 1, Owner: "evidence",
		Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation,
		Callers: []string{"application"},
	}
	env.cat.handlers["_evidence.command.begin"] = func(_ context.Context, _ contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		var in struct {
			PrincipalID      contract.ID     `json:"principal_id"`
			Operation        string          `json:"operation"`
			OperationVersion int64           `json:"operation_version"`
			SubmissionKey    string          `json:"submission_key"`
			RequestDigest    contract.Digest `json:"request_digest"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInvalidInput, Message: err.Error()}
		}
		retained := contract.Result{
			Schema:    contract.SchemaResult,
			CommandID: commandID,
			Payload:   contract.Payload{Status: contract.StatusFailed, Data: json.RawMessage(`null`), Error: &fault},
		}
		data, err := json.Marshal(map[string]any{
			"command_id": commandID,
			"existing": map[string]any{
				"id": commandID, "principal_id": in.PrincipalID, "operation": in.Operation,
				"operation_version": in.OperationVersion, "submission_key": in.SubmissionKey,
				"request_digest": in.RequestDigest, "status": contract.StatusFailed,
				"data": map[string]any{}, "error_code": fault.Code, "result": retained,
			},
		})
		if err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
		}
		return contract.Payload{Status: contract.StatusCompleted, Data: data}, nil
	}
	return commandID
}

// TestReplayedRefusalKeepsItsFaultStatus proves the HTTP status is derived
// from the result envelope itself. A replayed refusal is a failed envelope;
// whether or not a Go error accompanies it out of application.Invoke, the
// wire answer is the fault's mapped status with the retained command
// identity and fault, never HTTP 200 around a failed envelope.
func TestReplayedRefusalKeepsItsFaultStatus(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{contract.CodeConflict, http.StatusConflict},
		{contract.CodePermissionDenied, http.StatusForbidden},
		{contract.CodePrerequisiteMissing, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			env := newBootstrappedEnv(t)
			actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
			env.auth.registerCredential("Bearer good-token", actor)
			fault := contract.Fault{Code: tc.code, Message: "the original refusal", Details: json.RawMessage(`{"reason":"kept"}`)}
			commandID := registerReplayedRefusal(t, env, "widget.create", fault)
			_, client := newLocalServer(t, env)

			resp, result := postOperation(t, client, "http://unix", "widget.create", "Bearer good-token",
				mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "submission_key": "widget-key-1", "input": map[string]any{}}))
			if resp.StatusCode != tc.want {
				t.Fatalf("HTTP status = %d, want %d for a replayed %s refusal", resp.StatusCode, tc.want, tc.code)
			}
			if resp.StatusCode != contract.HTTPStatus(result.Error) {
				t.Fatalf("HTTP status %d disagrees with the envelope's own fault %v", resp.StatusCode, result.Error)
			}
			if result.Status != contract.StatusFailed || result.Error == nil {
				t.Fatalf("status = %q error = %v, want the failed envelope", result.Status, result.Error)
			}
			if result.Error.Code != tc.code || result.Error.Message != fault.Message || string(result.Error.Details) != string(fault.Details) {
				t.Fatalf("fault = %+v, want the retained refusal %+v", result.Error, fault)
			}
			if result.CommandID != commandID {
				t.Fatalf("command_id = %s, want the retained command %s", result.CommandID, commandID)
			}
		})
	}
}
