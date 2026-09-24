package connections

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

const toolIDMCPProbe = contract.ID("0a000000-0000-4000-8000-0000000000c6")

func schemaDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// seedMCPConnection plants an active mcp provider connection with destinations
// and a succeeded open_session validation that recorded a session_handle.
func seedMCPConnection(t *testing.T, env *testEnv) wireConnection {
	t.Helper()
	conn := env.seedConnection(func(w *wireConnection) {
		w.Provider = "mcp"
		w.Destinations = []string{"https://mcp.example.test/"}
		w.AllowedScopes = []string{"mcp:tools"}
		w.AccountIdentity = "credential:mcp-test-ref"
	})
	evidence, err := json.Marshal(map[string]any{
		"account_identity": conn.AccountIdentity,
		"allowed_scopes":   conn.AllowedScopes,
		"session_handle":   "sess-test-handle-1",
		"kind":             "open_session",
	})
	if err != nil {
		t.Fatalf("marshal validation evidence: %v", err)
	}
	env.mustOK("_connections.validation.record", validationRecordIn{
		ConnectionID: conn.ID, ExpectedVersion: conn.Version,
		Observation: wireObservation{
			Disposition: obsSucceeded, Evidence: evidence,
			Usage: &wireUsage{Currency: "USD", Advisory: true},
		},
	})
	conn.Version = 2
	conn.ValidationState = connStateValid
	return conn
}

func recordDiscovery(t *testing.T, env *testEnv, conn wireConnection, tools []map[string]any, nextCursor string) {
	t.Helper()
	evidence, err := json.Marshal(map[string]any{
		"kind":           "list_tools",
		"session_handle": "sess-test-handle-1",
		"next_cursor":    nextCursor,
		"physical_call":  map[string]any{"operation_id": string(env.ids.New())},
		"tools":          tools,
	})
	if err != nil {
		t.Fatalf("marshal discovery evidence: %v", err)
	}
	env.mustOK("_connections.discovery.record", validationRecordIn{
		ConnectionID: conn.ID, ExpectedVersion: conn.Version,
		Observation: wireObservation{
			Disposition: obsSucceeded, Evidence: evidence,
			Usage: &wireUsage{Currency: "USD", Advisory: true},
		},
	})
}

func TestDiscoverRefusesNonMCPProvider(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	_ = env.expectFault("connection.discover", connValidateIn{
		Scope: env.scope, ID: conn.ID, ExpectedVersion: conn.Version,
	}, contract.CodeCapabilityUnsupported)
}

func TestDiscoverRequiresPriorSessionHandle(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(func(w *wireConnection) {
		w.Provider = "mcp"
		w.Destinations = []string{"https://mcp.example.test/"}
	})
	f := env.expectFault("connection.discover", connValidateIn{
		Scope: env.scope, ID: conn.ID, ExpectedVersion: conn.Version,
	}, contract.CodePrerequisiteMissing)
	if f.Message == "" {
		t.Fatalf("prerequisite refusal carried no message")
	}
}

func TestDiscoverAdmitsListToolsJob(t *testing.T) {
	env := newEnv(t)
	conn := seedMCPConnection(t, env)
	payload := env.mustOK("connection.discover", connValidateIn{
		Scope: env.scope, ID: conn.ID, ExpectedVersion: conn.Version,
	})
	var out jobOut
	env.decode(payload.Data, &out)
	if out.Resource.Operation != "connection.discover" {
		t.Fatalf("admitted job operation %q", out.Resource.Operation)
	}
	calls := env.ports.callsOf("_execution.job.create")
	if len(calls) != 1 {
		t.Fatalf("discover made %d job.create calls, want 1", len(calls))
	}
	var stored struct {
		Input json.RawMessage `json:"input"`
	}
	env.decode(calls[0].Input, &stored)
	var probe struct {
		Tool   wireRef         `json:"tool"`
		Action json.RawMessage `json:"action"`
	}
	env.decode(stored.Input, &probe)
	if probe.Tool.ID != toolIDMCPProbe {
		t.Fatalf("discover targeted tool %s, want mcp-probe", probe.Tool.ID)
	}
	var action struct {
		Schema        string `json:"schema"`
		Kind          string `json:"kind"`
		SessionHandle string `json:"session_handle"`
	}
	env.decode(probe.Action, &action)
	if action.Schema != "zatiti.mcp.action/v1" || action.Kind != "list_tools" {
		t.Fatalf("discover action %+v", action)
	}
	if action.SessionHandle != "sess-test-handle-1" {
		t.Fatalf("discover session_handle %q", action.SessionHandle)
	}
}

// TestDiscoveryInstallsNoAuthority is the card property: recording a catalog
// never grows the trusted contracts table and never makes a discovered name
// dispatchable without an explicit binding + resolve composition.
func TestDiscoveryInstallsNoAuthority(t *testing.T) {
	env := newEnv(t)
	conn := seedMCPConnection(t, env)
	schemaA := `{"type":"object","properties":{"q":{"type":"string"}},"additionalProperties":false}`
	recordDiscovery(t, env, conn, []map[string]any{{
		"name": "alpha", "input_schema": json.RawMessage(schemaA),
		"input_schema_digest": schemaDigest(schemaA),
	}}, "")

	contracts := env.mustOK("tool.list", connListIn{Scope: env.scope})
	var catalog toolListOut
	env.decode(contracts.Data, &catalog)
	if len(catalog.Items) != 6 {
		t.Fatalf("discovery grew the trusted contracts catalog to %d", len(catalog.Items))
	}
	for _, item := range catalog.Items {
		if item.Name == "alpha" {
			t.Fatalf("discovered tool leaked into the trusted contracts catalog")
		}
	}

	// tool.get by discovered deterministic id refuses: discovery installs no
	// contracts row.
	toolID := mcpDiscoveredToolID(conn.ID, "alpha")
	_ = env.expectFault("tool.get", connGetIn{Scope: env.scope, ID: toolID}, contract.CodeNotFound)
}

func TestConnectionToolsPagesRecordedCatalog(t *testing.T) {
	env := newEnv(t)
	conn := seedMCPConnection(t, env)
	schemaA := `{"type":"object","additionalProperties":false}`
	schemaB := `{"type":"object","properties":{"x":{"type":"integer"}},"additionalProperties":false}`
	recordDiscovery(t, env, conn, []map[string]any{
		{"name": "alpha", "input_schema": json.RawMessage(schemaA), "input_schema_digest": schemaDigest(schemaA)},
		{"name": "beta", "title": "Beta", "input_schema": json.RawMessage(schemaB), "input_schema_digest": schemaDigest(schemaB)},
	}, "")

	payload := env.mustOK("connection.tools", struct {
		Scope        wireScope   `json:"scope"`
		ConnectionID contract.ID `json:"connection_id"`
	}{Scope: env.scope, ConnectionID: conn.ID})
	var out struct {
		Items []wireMCPDiscoveredTool `json:"items"`
	}
	env.decode(payload.Data, &out)
	if len(out.Items) != 2 {
		t.Fatalf("connection.tools returned %d items, want 2", len(out.Items))
	}
	if out.Items[0].Name != "alpha" || out.Items[1].Name != "beta" {
		t.Fatalf("tools order %+v", out.Items)
	}
	if out.Items[0].InputSchemaDigest != schemaDigest(schemaA) {
		t.Fatalf("pinned digest mismatch: %s", out.Items[0].InputSchemaDigest)
	}
	if out.Items[1].Title != "Beta" {
		t.Fatalf("optional title lost: %+v", out.Items[1])
	}
}

// TestResolveComposesDiscoveredMCPTool pins the resolve composition path:
// the returned Tool carries the recorded schema, external_mutation effect,
// mcp adapter and connection destinations.
func TestResolveComposesDiscoveredMCPTool(t *testing.T) {
	env := newEnv(t)
	conn := seedMCPConnection(t, env)
	schemaA := `{"type":"object","properties":{"q":{"type":"string"}},"additionalProperties":false}`
	digest := schemaDigest(schemaA)
	recordDiscovery(t, env, conn, []map[string]any{{
		"name": "alpha", "input_schema": json.RawMessage(schemaA),
		"input_schema_digest": digest,
	}}, "")

	toolID := mcpDiscoveredToolID(conn.ID, "alpha")
	payload := env.mustOK("_connections.resolve", resolveIn{
		Scope: env.scope, Connection: wireRef{ID: conn.ID, Version: conn.Version},
		Tool: wireRef{ID: toolID, Version: 1}, Destination: "https://mcp.example.test/",
	})
	var out struct {
		Connection wireConnection `json:"connection"`
		Tool       wireTool       `json:"tool"`
	}
	env.decode(payload.Data, &out)
	if out.Tool.ID != toolID || out.Tool.Name != "alpha" {
		t.Fatalf("composed tool %+v", out.Tool)
	}
	if out.Tool.Effect != "external_mutation" || out.Tool.Adapter != "mcp" {
		t.Fatalf("composed effect/adapter %+v", out.Tool)
	}
	if string(out.Tool.InputSchema) != schemaA {
		t.Fatalf("composed schema %s, want pinned %s", out.Tool.InputSchema, schemaA)
	}
	if len(out.Tool.Destinations) != 1 || out.Tool.Destinations[0] != "https://mcp.example.test/" {
		t.Fatalf("composed destinations %v", out.Tool.Destinations)
	}

	// Binding the deterministic id succeeds (no contracts row required).
	in := bindIn{Scope: env.scope, Binding: bindingDef(env.ids.New(), env.scope, bindingKindTool,
		toolID, []string{"connections.call"}, []string{"https://mcp.example.test/"})}
	_ = env.mustOK("tool.bind", in)
}

// TestDiscoveryMarksAbsentToolsStale is the complete-catalog stale property:
// a second succeed page without a previously recorded name marks it stale so
// resolve stops composing it.
func TestDiscoveryMarksAbsentToolsStale(t *testing.T) {
	env := newEnv(t)
	conn := seedMCPConnection(t, env)
	schemaA := `{"type":"object","additionalProperties":false}`
	schemaB := `{"type":"object","additionalProperties":false}`
	recordDiscovery(t, env, conn, []map[string]any{
		{"name": "alpha", "input_schema": json.RawMessage(schemaA), "input_schema_digest": schemaDigest(schemaA)},
		{"name": "beta", "input_schema": json.RawMessage(schemaB), "input_schema_digest": schemaDigest(schemaB)},
	}, "")

	// Incomplete page (next_cursor set) must not mark absences stale.
	recordDiscovery(t, env, conn, []map[string]any{
		{"name": "alpha", "input_schema": json.RawMessage(schemaA), "input_schema_digest": schemaDigest(schemaA)},
	}, "page-2")
	toolBeta := mcpDiscoveredToolID(conn.ID, "beta")
	_ = env.mustOK("_connections.resolve", resolveIn{
		Scope: env.scope, Connection: wireRef{ID: conn.ID, Version: conn.Version},
		Tool: wireRef{ID: toolBeta, Version: 1}, Destination: "https://mcp.example.test/",
	})

	// Complete page naming only alpha marks beta stale.
	recordDiscovery(t, env, conn, []map[string]any{
		{"name": "alpha", "input_schema": json.RawMessage(schemaA), "input_schema_digest": schemaDigest(schemaA)},
	}, "")
	_ = env.expectFault("_connections.resolve", resolveIn{
		Scope: env.scope, Connection: wireRef{ID: conn.ID, Version: conn.Version},
		Tool: wireRef{ID: toolBeta, Version: 1}, Destination: "https://mcp.example.test/",
	}, contract.CodeNotFound)

	// connection.tools still surfaces the stale row for operators.
	payload := env.mustOK("connection.tools", struct {
		Scope        wireScope   `json:"scope"`
		ConnectionID contract.ID `json:"connection_id"`
	}{Scope: env.scope, ConnectionID: conn.ID})
	var out struct {
		Items []wireMCPDiscoveredTool `json:"items"`
	}
	env.decode(payload.Data, &out)
	if len(out.Items) != 2 {
		t.Fatalf("tools listing lost stale rows: %d", len(out.Items))
	}
}

func TestMCPValidateUsesOpenSession(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(func(w *wireConnection) {
		w.Provider = "mcp"
		w.Destinations = []string{"https://mcp.example.test/"}
	})
	payload := env.mustOK("connection.validate", connValidateIn{
		Scope: env.scope, ID: conn.ID, ExpectedVersion: conn.Version,
	})
	var out jobOut
	env.decode(payload.Data, &out)
	calls := env.ports.callsOf("_execution.job.create")
	if len(calls) != 1 {
		t.Fatalf("validate made %d job.create calls", len(calls))
	}
	var stored struct {
		Input json.RawMessage `json:"input"`
	}
	env.decode(calls[0].Input, &stored)
	var probe struct {
		Action json.RawMessage `json:"action"`
	}
	env.decode(stored.Input, &probe)
	var action struct {
		Kind string `json:"kind"`
	}
	env.decode(probe.Action, &action)
	if action.Kind != "open_session" {
		t.Fatalf("mcp validate action kind %q, want open_session", action.Kind)
	}
}
