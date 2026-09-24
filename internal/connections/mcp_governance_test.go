package connections

import (
	"encoding/json"
	"github.com/zatiti/zatiti/internal/contract"
	"testing"
)

func TestMCPInitialValidationRequiresExactOwnerIntent(t *testing.T) {
	env := newEnv(t)
	installMCPFixtureProfile(env)
	conn := env.seedConnection(func(w *wireConnection) {
		w.Provider = "mcp"
		w.Destinations = []string{"https://mcp.example.test/"}
		w.CredentialRef = "mcp-test-ref"
		w.AccountIdentity = "credential:mcp-test-ref"
	})
	env.mustOK("connection.validate", connValidateIn{Scope: env.scope, ID: conn.ID, ExpectedVersion: conn.Version})
	op := env.ports.lastPrepared
	var action governedMCPAction
	env.decode(env.ports.prepared[op], &action)
	request := map[string]any{"scope": env.scope, "connection": action.Connection, "tool": action.Tool, "destination": action.Destination, "operation_id": op, "action": action}
	payload := env.mustOK("_connections.resolve", request)
	var out struct {
		ValidationIntent bool           `json:"validation_intent"`
		Connection       wireConnection `json:"connection"`
	}
	env.decode(payload.Data, &out)
	if !out.ValidationIntent || out.Connection.ValidationState != connStateUnverified {
		t.Fatal("intent must permit only probe resolution without fabricating validation")
	}
	request["operation_id"] = env.ids.New()
	_ = env.expectFault("_connections.resolve", request, contract.CodePrerequisiteMissing)
	request["operation_id"] = op
	action.Parameters = json.RawMessage(`{"schema":"zatiti.mcp.action/v1","kind":"list_tools","session_handle":"forged"}`)
	request["action"] = action
	_ = env.expectFault("_connections.resolve", request, contract.CodePrerequisiteMissing)
	_ = env.expectFault("connection.validate", connValidateIn{Scope: env.scope, ID: conn.ID, ExpectedVersion: conn.Version}, contract.CodeConflict)
	creates := env.ports.callsOf("_execution.job.create")
	if len(creates) != 1 {
		t.Fatal("probe duplicated jobs")
	}
	var job struct {
		OperationID contract.ID `json:"operation_id"`
	}
	env.decode(creates[0].Input, &job)
	if job.OperationID != op {
		t.Fatal("job lacks governed operation linkage")
	}
}

func TestMCPCallbackCannotInventOrSubstituteOperation(t *testing.T) {
	env := newEnv(t)
	conn := seedMCPConnection(t, env)
	obs := wireObservation{Disposition: obsSucceeded, Evidence: json.RawMessage(`{"kind":"open_session","session_handle":"forged"}`), Usage: &wireUsage{Currency: "USD"}}
	_ = env.expectFault("_connections.validation.record", map[string]any{"connection_id": conn.ID, "expected_version": conn.Version, "operation_id": env.ids.New(), "attempt_id": env.ids.New(), "observation": obs}, contract.CodePermissionDenied)
}

func TestMCPRediscoveryRejectsOldVersionAndPinsFullEnvelope(t *testing.T) {
	env := newEnv(t)
	conn := seedMCPConnection(t, env)
	schema := `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"],"additionalProperties":false}`
	tools := []map[string]any{{"name": "alpha", "input_schema": json.RawMessage(schema), "input_schema_digest": schemaDigest(schema)}}
	recordDiscovery(t, env, conn, tools, "")
	input := resolveIn{Scope: env.scope, Connection: wireRef{ID: conn.ID, Version: conn.Version}, Tool: wireRef{ID: mcpDiscoveredToolID(conn.ID, "alpha"), Version: 1}, Destination: env.svc.mcpProfile.Endpoint}
	payload := env.mustOK("_connections.resolve", input)
	var out struct {
		Tool wireTool `json:"tool"`
	}
	env.decode(payload.Data, &out)
	var doc map[string]any
	env.decode(out.Tool.InputSchema, &doc)
	props := doc["properties"].(map[string]any)
	params := map[string]any{"arguments": map[string]any{"q": "hi"}}
	for k, v := range props {
		if k != "arguments" {
			params[k] = v.(map[string]any)["const"]
		}
	}
	raw, _ := json.Marshal(params)
	if err := contract.ValidateSchema(out.Tool.InputSchema, raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"tool", "session_handle", "profile_digest", "classification", "input_schema_digest"} {
		old := params[field]
		params[field] = "tampered"
		raw, _ = json.Marshal(params)
		if contract.ValidateSchema(out.Tool.InputSchema, raw) == nil {
			t.Errorf("tampered %s accepted", field)
		}
		params[field] = old
	}
	recordDiscovery(t, env, conn, tools, "")
	_ = env.expectFault("_connections.resolve", input, contract.CodeStaleVersion)
	input.Tool.Version = 2
	env.mustOK("_connections.resolve", input)
}
