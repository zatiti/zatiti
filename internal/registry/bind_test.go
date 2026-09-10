package registry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// bindQueryDescriptor is a self-contained query descriptor for Bind tests.
func bindQueryDescriptor() contract.Descriptor {
	return contract.Descriptor{
		ID:         "test.query",
		Version:    3,
		Owner:      "tester",
		Visibility: contract.VisibilityPublic,
		Mode:       contract.ModeQuery,
		Effect:     contract.EffectLocal,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,` +
			`"properties":{"name":{"type":"string"}},"required":["name"]}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,` +
			`"properties":{"greeting":{"type":"string"},"echo":{"type":"string"}},"required":["greeting","echo"]}`),
	}
}

// bindMutationDescriptor is a self-contained mutation descriptor.
func bindMutationDescriptor() contract.Descriptor {
	return contract.Descriptor{
		ID:         "test.mutate",
		Version:    1,
		Owner:      "tester",
		Visibility: contract.VisibilityPublic,
		Mode:       contract.ModeMutation,
		Effect:     contract.EffectExternalMutation,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,` +
			`"properties":{"name":{"type":"string"}},"required":["name"]}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,` +
			`"properties":{"resource":{"$ref":"#/$defs/Scope"}},"required":["resource"]}`),
		SubmissionKey: true,
	}
}

// faultCodeOf asserts err carries a *contract.Fault and returns its code.
func faultCodeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected a fault, got nil error")
	}
	var f *contract.Fault
	if !errors.As(err, &f) {
		t.Fatalf("error %q does not carry a *contract.Fault", err)
	}
	return f.Code
}

func TestBindQueryHappyPath(t *testing.T) {
	handler, err := Bind(bindQueryDescriptor(), func(_ context.Context, _ contract.Unit, in bindIn) (contract.Outcome[bindOut], error) {
		return contract.Outcome[bindOut]{Data: bindOut{Greeting: "hello " + in.Name, Echo: in.Name}}, nil
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	payload, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "test.query",
		Version:   3,
		Input:     json.RawMessage(`{"name":"ada"}`),
	})
	if err != nil {
		t.Fatalf("invocation failed: %v", err)
	}
	if payload.Status != contract.StatusCompleted {
		t.Fatalf("status %q, want completed", payload.Status)
	}
	// Canonical output: sorted keys, no insignificant whitespace.
	want := `{"echo":"ada","greeting":"hello ada"}`
	if string(payload.Data) != want {
		t.Fatalf("payload data %s, want %s", payload.Data, want)
	}
	if payload.Error != nil {
		t.Fatalf("completed payload carries an error: %v", payload.Error)
	}
}

func TestBindRejectsInvalidInput(t *testing.T) {
	handler, err := Bind(bindQueryDescriptor(), func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[bindOut], error) {
		t.Fatal("handler must not run for rejected input")
		return contract.Outcome[bindOut]{}, nil
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	cases := map[string]string{
		"type violation": `{"name":42}`,
		"missing field":  `{}`,
		"unknown field":  `{"name":"a","extra":1}`,
		"duplicate keys": `{"name":"a","name":"b"}`,
		"trailing data":  `{"name":"a"} trailing`,
		"not an object":  `"name"`,
		"invalid json":   `{"name":`,
	}
	for name, input := range cases {
		_, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{
			Operation: "test.query",
			Version:   3,
			Input:     json.RawMessage(input),
		})
		if got := faultCodeOf(t, err); got != contract.CodeInvalidInput {
			t.Fatalf("%s: fault %q, want invalid_input", name, got)
		}
	}
}

func TestBindRejectsWrongOperationAndVersion(t *testing.T) {
	handler, err := Bind(bindQueryDescriptor(), func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[bindOut], error) {
		return contract.Outcome[bindOut]{}, nil
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	if _, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{Operation: "test.other", Version: 3, Input: json.RawMessage(`{"name":"a"}`)}); faultCodeOf(t, err) != contract.CodeInternalError {
		t.Fatal("invocation for another operation must be an internal fault")
	}
	if _, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{Operation: "test.query", Version: 2, Input: json.RawMessage(`{"name":"a"}`)}); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatal("wrong version must be a not_found fault")
	}
}

func TestBindAcceptedOutcomesAreMutationOnly(t *testing.T) {
	mutation, err := Bind(bindMutationDescriptor(), func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[map[string]any], error) {
		return contract.Outcome[map[string]any]{
			Status: contract.StatusAccepted,
			Data: map[string]any{"resource": map[string]any{
				"installation_id": "00000000-0000-4000-8000-000000000001",
			}},
		}, nil
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	payload, err := mutation(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "test.mutate",
		Version:   1,
		Input:     json.RawMessage(`{"name":"a"}`),
	})
	if err != nil {
		t.Fatalf("mutation invocation failed: %v", err)
	}
	if payload.Status != contract.StatusAccepted {
		t.Fatalf("status %q, want accepted", payload.Status)
	}

	query, err := Bind(bindQueryDescriptor(), func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[bindOut], error) {
		return contract.Outcome[bindOut]{Status: contract.StatusAccepted, Data: bindOut{}}, nil
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	_, err = query(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "test.query",
		Version:   3,
		Input:     json.RawMessage(`{"name":"a"}`),
	})
	if got := faultCodeOf(t, err); got != contract.CodeInternalError {
		t.Fatalf("query accepted outcome: fault %q, want internal_error", got)
	}
}

func TestBindStatusHandling(t *testing.T) {
	var status string
	handler, err := Bind(bindQueryDescriptor(), func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[bindOut], error) {
		return contract.Outcome[bindOut]{Status: status, Data: bindOut{Greeting: "hi", Echo: "hi"}}, nil
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	invoke := func() (contract.Payload, error) {
		return handler(context.Background(), &fakeUnit{}, contract.Invocation{
			Operation: "test.query",
			Version:   3,
			Input:     json.RawMessage(`{"name":"a"}`),
		})
	}
	status = ""
	payload, err := invoke()
	if err != nil || payload.Status != contract.StatusCompleted {
		t.Fatalf("empty status must default to completed, got %q err %v", payload.Status, err)
	}
	status = "shipped"
	_, err = invoke()
	if got := faultCodeOf(t, err); got != contract.CodeInternalError {
		t.Fatal("unknown status must be an internal fault")
	}
	if !strings.Contains(err.Error(), "shipped") {
		t.Fatalf("fault message %q does not name the invalid status", err.Error())
	}
}

func TestBindValidatesOutputAgainstSchema(t *testing.T) {
	handler, err := Bind(bindQueryDescriptor(), func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[map[string]any], error) {
		return contract.Outcome[map[string]any]{
			Data: map[string]any{"greeting": "hi", "echo": "hi", "bogus": true},
		}, nil
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	_, err = handler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "test.query",
		Version:   3,
		Input:     json.RawMessage(`{"name":"a"}`),
	})
	if got := faultCodeOf(t, err); got != contract.CodeInternalError {
		t.Fatalf("schema-violating output: fault %q, want internal_error", got)
	}
}

func TestBindResolvesSharedDefinitions(t *testing.T) {
	// The output schema references #/$defs/Scope; Bind merges the shared
	// definitions, so a conforming resource passes and a malformed one does
	// not.
	handler, err := Bind(bindMutationDescriptor(), func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[map[string]any], error) {
		return contract.Outcome[map[string]any]{
			Data: map[string]any{"resource": json.RawMessage(`{"installation_id":"00000000-0000-4000-8000-000000000001"}`)},
		}, nil
	})
	if err != nil {
		t.Fatalf("bind with a $ref output schema failed: %v", err)
	}
	payload, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "test.mutate",
		Version:   1,
		Input:     json.RawMessage(`{"name":"a"}`),
	})
	if err != nil {
		t.Fatalf("$ref output rejected: %v", err)
	}
	if !strings.Contains(string(payload.Data), `"resource"`) {
		t.Fatalf("payload data %s does not carry the resource", payload.Data)
	}

	unresolved, err := Bind(contract.Descriptor{
		ID: "test.broken", Version: 1, Owner: "tester",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"$ref":"#/$defs/Missing"}},"required":["x"]}`),
	}, func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[bindOut], error) {
		return contract.Outcome[bindOut]{}, nil
	})
	if err == nil || unresolved != nil {
		t.Fatal("bind must reject schemas whose references do not resolve")
	}
}

func TestBindPassesFaultsThroughUnchanged(t *testing.T) {
	want := &contract.Fault{Code: contract.CodePermissionDenied, Message: "not yours", Retryable: false}
	handler, err := Bind(bindQueryDescriptor(), func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[bindOut], error) {
		return contract.Outcome[bindOut]{}, want
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	_, err = handler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "test.query",
		Version:   3,
		Input:     json.RawMessage(`{"name":"a"}`),
	})
	var got *contract.Fault
	if !errors.As(err, &got) {
		t.Fatalf("fault error surfaced as %T (%v)", err, err)
	}
	if got != want {
		t.Fatalf("fault identity changed: %v", got)
	}
}

func TestBindWrapsPlainErrorsAsInternal(t *testing.T) {
	handler, err := Bind(bindQueryDescriptor(), func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[bindOut], error) {
		return contract.Outcome[bindOut]{}, errors.New("boom")
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	_, err = handler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "test.query",
		Version:   3,
		Input:     json.RawMessage(`{"name":"a"}`),
	})
	if got := faultCodeOf(t, err); got != contract.CodeInternalError {
		t.Fatalf("plain error: fault %q, want internal_error", got)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("wrapped error %q lost the cause", err.Error())
	}
}

func TestBindRejectsMisdescriptors(t *testing.T) {
	bad := []contract.Descriptor{
		// Query with a submission key.
		{
			ID: "test.bad", Version: 1, Owner: "tester",
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
			SubmissionKey: true,
		},
		// Version below one.
		{
			ID: "test.bad", Version: 0, Owner: "tester",
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		},
		// Unknown visibility.
		{
			ID: "test.bad", Version: 1, Owner: "tester",
			Visibility: "secret", Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		},
		// Empty input schema.
		{
			ID: "test.bad", Version: 1, Owner: "tester",
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}
	for i, d := range bad {
		handler, err := Bind(d, func(_ context.Context, _ contract.Unit, _ bindIn) (contract.Outcome[bindOut], error) {
			return contract.Outcome[bindOut]{}, nil
		})
		if err == nil || handler != nil {
			t.Fatalf("case %d: bind accepted a misdescribed operation", i)
		}
	}
	var missing func(context.Context, contract.Unit, bindIn) (contract.Outcome[bindOut], error)
	if _, err := Bind(bindQueryDescriptor(), missing); err == nil {
		t.Fatal("bind accepted a nil handler function")
	}
}

// bindIn and bindOut are the DTOs used by the bind tests.
type bindIn struct {
	Name string `json:"name"`
}

type bindOut struct {
	Greeting string `json:"greeting"`
	Echo     string `json:"echo"`
}
