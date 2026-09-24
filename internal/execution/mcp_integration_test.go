package execution

import (
	"encoding/json"
	"github.com/zatiti/zatiti/internal/contract"
	"testing"
)

func TestMCPContextRequiresSelectedConnectionAndEndpoint(t *testing.T) {
	for _, scenario := range []string{"bound", "unselected", "wrong_destination", "freshness_fault"} {
		t.Run(scenario, func(t *testing.T) {
			e := newEnv(t)
			worker := e.ids.New()
			profile := fixtureHostedProfile(worker)
			e.installWorkerSnapshot(worker, profile)
			toolID, _ := e.installProductToolBinding(worker, profile, "mcp-test", "external_read")
			owner := e.ids.New()
			snap := e.ports.snapshots[e.install]
			bindingID := e.ids.New()
			destination := profile.ProviderDestination
			snap.Bindings = append(snap.Bindings, wireBinding{ID: bindingID, Version: 1, Scope: e.scope, Kind: "connection", TargetID: owner, Destinations: []string{destination}})
			if scenario != "unselected" {
				snap.Worker.Bindings = append(snap.Worker.Bindings, bindingID)
			}
			if scenario == "wrong_destination" {
				snap.Bindings[len(snap.Bindings)-1].Destinations = []string{"https://other.example/mcp"}
			}
			e.ports.connections[owner] = wireConnection{ID: owner, Version: 7, Provider: "mcp", AccountIdentity: "mcp-account", Destinations: []string{destination}}
			tool := e.ports.tools[toolID]
			tool.Adapter = "mcp"
			tool.Version = 9
			e.ports.tools[toolID] = tool
			e.ports.mcpOwners[toolID] = owner
			if scenario == "freshness_fault" {
				e.ports.setFault(peerConnectionsToolResolve, &contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "validation expired"})
			}
			plan := e.prepareContext(e.admitClaimedTurn(worker))
			found := false
			for _, c := range plan.Recipe.Components {
				if c.ToolID == toolID {
					found = true
					if c.ConnectionID != owner || c.ConnectionVersion != 7 || c.ToolVersion != 9 || c.Classification != "restricted" {
						t.Fatalf("wrong MCP authority: %+v", c)
					}
				}
			}
			if found != (scenario == "bound") {
				t.Fatalf("MCP exposed=%v for %s", found, scenario)
			}
		})
	}
}

func TestMCPProposalRejectsChangedAuthorityAndUnrestrictedInput(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"classification":{"type":"string"},"arguments":{"type":"object"}},"required":["classification","arguments"]}`)
	for _, scenario := range []string{"valid", "public", "schema", "account", "currency", "version", "negative_cost"} {
		t.Run(scenario, func(t *testing.T) {
			c := contextComponent{Adapter: "mcp", ConnectionID: "connection", ConnectionVersion: 7, ToolID: "tool", ToolVersion: 9, AccountIdentity: "account", Destinations: []string{"https://mcp.example/"}, InputSchema: schema}
			conn := wireConnection{ID: c.ConnectionID, Version: c.ConnectionVersion, Provider: "mcp", AccountIdentity: c.AccountIdentity}
			tool := wireTool{ID: c.ToolID, Version: c.ToolVersion, Adapter: "mcp", Destinations: c.Destinations, InputSchema: schema, CostBound: wireMoney{Currency: "USD", MicroUnits: 42}}
			input := json.RawMessage(`{"classification":"restricted","arguments":{"value":"synthetic"}}`)
			switch scenario {
			case "public":
				input = json.RawMessage(`{"classification":"public","arguments":{}}`)
			case "schema":
				tool.InputSchema = json.RawMessage(`{}`)
			case "account":
				conn.AccountIdentity = "different"
			case "currency":
				tool.CostBound.Currency = "EUR"
			case "version":
				tool.Version++
			case "negative_cost":
				tool.CostBound.MicroUnits = -1
			}
			err := validateMCPProposal(&c, conn, tool, input, "USD")
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("validation error=%v for %s", err, scenario)
			}
		})
	}
}

func TestMCPInterpretationPreparesAuthoritativeCostAndTurnCallback(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	turn := e.admitClaimedTurn(worker)
	turn.AttemptID = e.ids.New() // A task attempt must not replace the callback owner.
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"classification":{"const":"restricted"},"arguments":{"type":"object"},"profile_digest":{"const":"pinned"}},"required":["classification","arguments","profile_digest"]}`)
	conn := wireConnection{ID: e.ids.New(), Version: 7, Provider: "mcp", AccountIdentity: "synthetic-account"}
	tool := wireTool{ID: e.ids.New(), Version: 9, Adapter: "mcp", InputSchema: schema, Destinations: []string{"https://mcp.example/"}, CostBound: wireMoney{Currency: turn.Limits.Currency, MicroUnits: 42}}
	e.ports.setConnection(conn)
	e.ports.setTool(tool)
	comp := contextComponent{Adapter: "mcp", ConnectionID: conn.ID, ConnectionVersion: conn.Version, AccountIdentity: conn.AccountIdentity, ToolID: tool.ID, ToolVersion: tool.Version, InputSchema: schema, Destinations: tool.Destinations}
	input := json.RawMessage(`{"classification":"restricted","arguments":{"value":"synthetic"},"profile_digest":"pinned"}`)
	now := e.clock.Now()
	base := &proposalRow{ID: e.ids.New(), InstallationID: e.install, TurnID: turn.ID, StepIndex: 0, ProposalID: "mcp-call", SourceContextDigest: fixtureDigest, CreatedAt: now, UpdatedAt: now}
	e.inWrite(func(unit contract.Unit) error {
		_, err := e.svc.interpretExternalTool(e.ctx, unit, turn, base, &comp, wireModelToolProposal{Input: input}, now)
		return err
	})
	calls := e.ports.EffectsPrepared()
	if len(calls) != 1 {
		t.Fatalf("prepared %d effects", len(calls))
	}
	call := calls[0]
	cost, ok := call.Action["cost_bound"].(map[string]any)
	if !ok || cost["micro_units"] != json.Number("42") || cost["currency"] != turn.Limits.Currency {
		t.Fatalf("wrong cost: %#v", call.Action["cost_bound"])
	}
	if call.SourceID != turn.ID || call.CallbackRoute["turn_id"] != string(turn.ID) {
		t.Fatalf("callback owner mismatch: %+v", call)
	}
	parameters := call.Action["parameters"].(map[string]any)
	if parameters["profile_digest"] != "pinned" || parameters["classification"] != "restricted" {
		t.Fatalf("lost pinned envelope: %#v", parameters)
	}
}
