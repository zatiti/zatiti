package mcp

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
)

func testDescriptor(t *testing.T, submissionKey bool) contract.Descriptor {
	t.Helper()
	return contract.Descriptor{
		ID:            "widget.create",
		Version:       1,
		Owner:         "test",
		Visibility:    contract.VisibilityPublic,
		Mode:          contract.ModeMutation,
		Effect:        contract.EffectLocal,
		InputSchema:   json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"name":{"type":"string"}},"required":["scope","name"]}`),
		OutputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Worker"}},"required":["resource"]}`),
		CLI:           []string{"widget", "create"},
		MCP:           "zatiti_widget_create",
		SubmissionKey: submissionKey,
	}
}

func TestWrapInputSchemaResolvesSharedRefs(t *testing.T) {
	defs, err := loadSharedDefs()
	if err != nil {
		t.Fatalf("loadSharedDefs() error = %v", err)
	}
	d := testDescriptor(t, true)
	schema, err := wrapInputSchema(d, defs)
	if err != nil {
		t.Fatalf("wrapInputSchema() error = %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatalf("wrapped input schema is not valid JSON: %v", err)
	}
	if doc["type"] != "object" {
		t.Fatalf("wrapped input schema type = %v, want object", doc["type"])
	}
	props, _ := doc["properties"].(map[string]any)
	if _, ok := props["input"]; !ok {
		t.Fatal(`wrapped input schema has no "input" property`)
	}
	if _, ok := props["submission_key"]; !ok {
		t.Fatal(`wrapped input schema has no "submission_key" property for a submission-key descriptor`)
	}
	required, _ := doc["required"].([]any)
	if !containsString(required, "input") || !containsString(required, "submission_key") {
		t.Fatalf("wrapped input schema required = %v, want input and submission_key", required)
	}

	// A valid instance whose "scope" relies on the shared $defs/Scope
	// reference must validate against the wrapped, self-contained schema.
	instance := json.RawMessage(`{"input":{"scope":{"installation_id":"00000000-0000-4000-8000-000000000000"},"name":"a"},"submission_key":"k1"}`)
	if err := contract.ValidateSchema(schema, instance); err != nil {
		t.Fatalf("ValidateSchema() error = %v, want a valid instance to pass", err)
	}

	// additionalProperties:false rejects a credential-profile-shaped field:
	// the tool schema never exposes a way to pick a credential profile.
	withProfile := json.RawMessage(`{"input":{"scope":{"installation_id":"00000000-0000-4000-8000-000000000000"},"name":"a"},"submission_key":"k1","credential_profile":"admin"}`)
	if err := contract.ValidateSchema(schema, withProfile); err == nil {
		t.Fatal("ValidateSchema() accepted an unknown credential_profile field, want rejection")
	}
}

func TestWrapInputSchemaWithoutSubmissionKey(t *testing.T) {
	defs, err := loadSharedDefs()
	if err != nil {
		t.Fatalf("loadSharedDefs() error = %v", err)
	}
	d := testDescriptor(t, false)
	d.Mode = contract.ModeQuery
	schema, err := wrapInputSchema(d, defs)
	if err != nil {
		t.Fatalf("wrapInputSchema() error = %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatalf("wrapped input schema is not valid JSON: %v", err)
	}
	props, _ := doc["properties"].(map[string]any)
	if _, ok := props["submission_key"]; ok {
		t.Fatal(`wrapped input schema declares "submission_key" for a descriptor that does not require one`)
	}
	required, _ := doc["required"].([]any)
	if containsString(required, "submission_key") {
		t.Fatalf("wrapped input schema required = %v, must not require submission_key", required)
	}
}

func TestWrapInputSchemaRejectsMissingInputSchema(t *testing.T) {
	defs, err := loadSharedDefs()
	if err != nil {
		t.Fatalf("loadSharedDefs() error = %v", err)
	}
	d := testDescriptor(t, false)
	d.InputSchema = nil
	if _, err := wrapInputSchema(d, defs); err == nil {
		t.Fatal("wrapInputSchema() error = nil, want an error for a descriptor with no input schema")
	}
}

func TestWrapOutputSchemaAllowsCompletedAndFailedShapes(t *testing.T) {
	defs, err := loadSharedDefs()
	if err != nil {
		t.Fatalf("loadSharedDefs() error = %v", err)
	}
	d := testDescriptor(t, true)
	schema, err := wrapOutputSchema(d, defs)
	if err != nil {
		t.Fatalf("wrapOutputSchema() error = %v", err)
	}

	completed := json.RawMessage(`{"schema":"zatiti.result/v1","command_id":"00000000-0000-4000-8000-000000000001","status":"completed","data":{"resource":{"id":"00000000-0000-4000-8000-000000000002","version":1,"organization_id":"00000000-0000-4000-8000-000000000003","key":"k","name":"n","purpose":"p","instructions":"i","skill_versions":[],"bindings":[],"profile":null,"limits":null}},"error":null,"next_cursor":null}`)
	if err := contract.ValidateSchema(schema, completed); err != nil {
		t.Fatalf("ValidateSchema() on a completed result error = %v", err)
	}

	failed := json.RawMessage(`{"schema":"zatiti.result/v1","command_id":"","status":"failed","data":null,"error":{"code":"controller_unavailable","message":"boom","retryable":true},"next_cursor":null}`)
	if err := contract.ValidateSchema(schema, failed); err != nil {
		t.Fatalf("ValidateSchema() on a failed result error = %v", err)
	}
}

func TestWrapOutputSchemaRejectsMissingOutputSchema(t *testing.T) {
	defs, err := loadSharedDefs()
	if err != nil {
		t.Fatalf("loadSharedDefs() error = %v", err)
	}
	d := testDescriptor(t, false)
	d.OutputSchema = nil
	if _, err := wrapOutputSchema(d, defs); err == nil {
		t.Fatal("wrapOutputSchema() error = nil, want an error for a descriptor with no output schema")
	}
}

func TestValidateDescriptor(t *testing.T) {
	base := testDescriptor(t, true)
	if err := validateDescriptor(base); err != nil {
		t.Fatalf("validateDescriptor() error = %v, want nil for a well-formed descriptor", err)
	}

	noID := base
	noID.ID = ""
	if err := validateDescriptor(noID); err == nil {
		t.Fatal("validateDescriptor() error = nil, want an error for a descriptor with no ID")
	}

	internalDescriptor := base
	internalDescriptor.Visibility = contract.VisibilityInternal
	if err := validateDescriptor(internalDescriptor); err == nil {
		t.Fatal("validateDescriptor() error = nil, want an error for an internal descriptor")
	}

	noMCP := base
	noMCP.MCP = ""
	if err := validateDescriptor(noMCP); err == nil {
		t.Fatal("validateDescriptor() error = nil, want an error for a descriptor with no MCP mapping")
	}
}

func TestDecodeToolArgs(t *testing.T) {
	t.Run("valid with submission key", func(t *testing.T) {
		args, err := decodeToolArgs(json.RawMessage(`{"input":{"name":"a"},"submission_key":"k1"}`))
		if err != nil {
			t.Fatalf("decodeToolArgs() error = %v", err)
		}
		if string(args.Input) != `{"name":"a"}` {
			t.Fatalf("args.Input = %s, want {\"name\":\"a\"}", args.Input)
		}
		if args.SubmissionKey != "k1" {
			t.Fatalf("args.SubmissionKey = %q, want k1", args.SubmissionKey)
		}
	})

	t.Run("missing input is rejected", func(t *testing.T) {
		if _, err := decodeToolArgs(json.RawMessage(`{"submission_key":"k1"}`)); err == nil {
			t.Fatal(`decodeToolArgs() error = nil, want an error for missing "input"`)
		}
	})

	t.Run("empty arguments is rejected as missing input", func(t *testing.T) {
		if _, err := decodeToolArgs(nil); err == nil {
			t.Fatal("decodeToolArgs() error = nil, want an error for absent arguments")
		}
	})

	t.Run("unknown field is rejected", func(t *testing.T) {
		if _, err := decodeToolArgs(json.RawMessage(`{"input":{},"credential_profile":"admin"}`)); err == nil {
			t.Fatal("decodeToolArgs() error = nil, want an error for an unknown field")
		}
	})

	t.Run("duplicate key is rejected", func(t *testing.T) {
		if _, err := decodeToolArgs(json.RawMessage(`{"input":{},"input":{}}`)); err == nil {
			t.Fatal("decodeToolArgs() error = nil, want an error for a duplicate key")
		}
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		if _, err := decodeToolArgs(json.RawMessage(`{`)); err == nil {
			t.Fatal("decodeToolArgs() error = nil, want an error for malformed JSON")
		}
	})
}

func TestTransportFault(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		code      string
		retryable bool
	}{
		{"controller unavailable", client.ErrControllerUnavailable, contract.CodeControllerUnavailable, true},
		{"unknown outcome", client.ErrUnknownOutcome, contract.CodeOutcomeUnknown, false},
		{"tls certificate", client.ErrTLSCertificate, contract.CodeInternalError, false},
		{"invalid request", client.ErrInvalidRequest, contract.CodeInvalidInput, false},
		{"unclassified", errors.New("boom"), contract.CodeInternalError, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fault := transportFault(tc.err)
			if fault.Code != tc.code {
				t.Fatalf("transportFault(%v).Code = %q, want %q", tc.err, fault.Code, tc.code)
			}
			if fault.Retryable != tc.retryable {
				t.Fatalf("transportFault(%v).Retryable = %v, want %v", tc.err, fault.Retryable, tc.retryable)
			}
		})
	}
}

func TestTransportFailureResultHasNoCommandID(t *testing.T) {
	result := transportFailureResult(client.ErrControllerUnavailable)
	if result.Status != contract.StatusFailed {
		t.Fatalf("result.Status = %q, want failed", result.Status)
	}
	if result.CommandID != "" {
		t.Fatalf("result.CommandID = %q, want empty: no command was ever created", result.CommandID)
	}
	if result.Error == nil || result.Error.Code != contract.CodeControllerUnavailable {
		t.Fatalf("result.Error = %+v, want controller_unavailable", result.Error)
	}
}

func containsString(items []any, want string) bool {
	for _, item := range items {
		if s, ok := item.(string); ok && s == want {
			return true
		}
	}
	return false
}
