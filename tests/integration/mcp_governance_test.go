package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

const governedMCPEndpoint = "https://mcp.example.test/"

// This profile and observations are synthetic: the test proves real owner
// transactions and authorization, not transport or live-provider qualification.
func governedMCPProfile(t *testing.T) json.RawMessage {
	t.Helper()
	p := map[string]any{"schema": "zatiti.mcp/v1", "transport": map[string]any{"kind": "streamable_http", "endpoint": governedMCPEndpoint, "allow_private_endpoint": false, "max_redirects": 0}, "protocol_version": "2025-11-25", "credential_kind": "none", "allowed_tools": []string{"alpha"}, "tool_call_cost": map[string]any{"currency": "USD", "micro_units": 7}, "max_control_replies": 2, "control_reply_cost": map[string]any{"currency": "USD", "micro_units": 3}, "max_request_bytes": 65536, "max_response_bytes": 65536, "timeout_seconds": 1800, "classifications": []string{"restricted"}}
	canonical, err := contract.Canonicalize(mustJSON(p))
	if err != nil {
		t.Fatal(err)
	}
	p["capability_evidence"] = map[string]any{"artifact": map[string]any{"id": contract.NewID(), "digest": syntheticDigest}, "adapter_version": "synthetic", "source_revision": "synthetic", "protocol_revision": "2025-11-25", "profile_digest": contract.Hash(canonical), "qualified_at": "2026-01-01T00:00:00Z", "capabilities": []string{}, "limitations": []string{"synthetic evidence; no transport qualification"}}
	return mustJSON(p)
}

func (f *fixture) mcpInternal(op string, input any) (contract.Payload, error) {
	p, err := f.app.Internal(context.Background(), f.controllerIdentity(), f.scope(), contract.Invocation{Operation: op, Version: 1, Input: mustJSON(input)})
	if err == nil && p.Error != nil {
		err = p.Error
	}
	return p, err
}
func (f *fixture) mustMCPInternal(op string, input any) contract.Payload {
	f.t.Helper()
	p, err := f.mcpInternal(op, input)
	if err != nil {
		f.t.Fatalf("%s: %v", op, err)
	}
	if p.Error != nil {
		f.t.Fatalf("%s: %v", op, p.Error)
	}
	return p
}

type governedMCPOperation struct {
	ID           contract.ID    `json:"id"`
	Version      int64          `json:"version"`
	State        string         `json:"state"`
	Action       map[string]any `json:"action"`
	ActionDigest string         `json:"action_digest"`
	AttemptIDs   []contract.ID  `json:"attempt_ids"`
}

func (f *fixture) mcpOperation(id contract.ID) governedMCPOperation {
	f.t.Helper()
	p := f.must(f.owner, "operation.get", "", map[string]any{"scope": f.scope(), "id": id})
	var b struct {
		Resource governedMCPOperation `json:"resource"`
	}
	decode(f.t, p.Data, &b)
	return b.Resource
}
func (f *fixture) mcpProbe(conn contract.ID, version int64, kind string) governedMCPOperation {
	f.t.Helper()
	p := f.must(f.owner, "connection."+kind, "mcp-"+kind+string(contract.NewID()), map[string]any{"scope": f.scope(), "id": conn, "expected_version": version})
	var body struct {
		Resource struct {
			ID          contract.ID `json:"id"`
			OperationID contract.ID `json:"operation_id"`
		} `json:"resource"`
	}
	decode(f.t, p.Data, &body)
	if body.Resource.OperationID == "" {
		f.t.Fatalf("probe job lacks operation_id: %s", p.Data)
	}
	op := f.mcpOperation(body.Resource.OperationID)
	if op.ID == "" || op.Action == nil {
		f.t.Fatal("linked job has no durable action")
	}
	return op
}
func (f *fixture) mcpAdmit(op governedMCPOperation) governedMCPOperation {
	f.t.Helper()
	if op.State == "awaiting_review" {
		f.approve(f.findReview(op.ActionDigest), "mcp-effect-review-"+string(op.ID))
	}
	p := f.mustMCPInternal("_effects.admit", map[string]any{"operation_id": op.ID, "expected_version": op.Version})
	var b struct {
		Resource governedMCPOperation `json:"resource"`
	}
	decode(f.t, p.Data, &b)
	if b.Resource.State != "ready" || len(b.Resource.AttemptIDs) != 1 {
		f.t.Fatalf("probe not ready: %s", p.Data)
	}
	return b.Resource
}
func (f *fixture) mcpComplete(conn contract.ID, version int64, kind string, op governedMCPOperation, extra map[string]any) {
	f.t.Helper()
	op = f.mcpAdmit(op)
	attempt := op.AttemptIDs[0]
	f.mustMCPInternal("_effects.claim", map[string]any{"operation_id": op.ID, "attempt_id": attempt, "generation": f.generation})
	params := op.Action["parameters"].(map[string]any)
	evidence := map[string]any{"kind": map[string]string{"validate": "open_session", "discover": "list_tools"}[kind], "session_handle": "synthetic-session", "physical_call": map[string]any{"operation_id": op.ID, "attempt_id": attempt, "account_identity": "none", "profile_digest": params["profile_digest"], "requested_destination": governedMCPEndpoint}}
	for k, v := range extra {
		evidence[k] = v
	}
	observation := map[string]any{"disposition": "succeeded", "evidence": evidence, "usage": map[string]any{"currency": "USD", "spent": 7, "reserved": 0, "estimated": 0, "unknown": 0, "advisory": false}}
	callbackOp := "_connections." + map[string]string{"validate": "validation", "discover": "discovery"}[kind] + ".record"
	_, err := f.mcpInternal(callbackOp, map[string]any{"connection_id": conn, "expected_version": version, "operation_id": op.ID, "attempt_id": attempt, "observation": observation})
	if faultCode(err) != contract.CodePrerequisiteMissing {
		f.t.Fatalf("unrecorded callback: want prerequisite_missing, got %v", err)
	}
	f.mustMCPInternal("_effects.record", map[string]any{"operation_id": op.ID, "attempt_id": attempt, "generation": f.generation, "observation": observation})
	callback := map[string]any{"connection_id": conn, "expected_version": version, "operation_id": op.ID, "attempt_id": attempt, "observation": observation}
	// Callback payload is not authoritative; the owner must use effects' record.
	callback["observation"] = map[string]any{"disposition": "failed", "evidence": map[string]any{}, "usage": observation["usage"]}
	f.mustMCPInternal("_connections."+map[string]string{"validate": "validation", "discover": "discovery"}[kind]+".record", callback)
}

func TestMCPGovernedInitialValidationAndDiscovery(t *testing.T) {
	f, err := assemble(t, fixtureOptions{mcpProfile: governedMCPProfile(t)})
	if err != nil {
		t.Fatal(err)
	}
	f.bootstrap()
	f.activate("mcp-connection", "connection.create", map[string]any{"definition": map[string]any{"scope": f.scope(), "provider": "mcp", "account_identity": "none", "credential_ref": "", "destinations": []string{governedMCPEndpoint}, "allowed_scopes": []string{}}})
	conn := f.findConnectionByAccount("none")
	f.configureMCPBudget(t)
	f.grantMCPController(t, "0a000000-0000-4000-8000-0000000000c6")
	initial := f.mcpProbe(conn, 1, "validate")
	// An identical public action has a different operation identity and no
	// connections-owned durable intent. It must never borrow freshness bypass.
	forged := f.must(f.owner, "operation.propose", "forged-mcp-probe", map[string]any{"scope": f.scope(), "action": initial.Action})
	var b struct {
		Resource governedMCPOperation `json:"resource"`
	}
	decode(t, forged.Data, &b)
	if b.Resource.State == "awaiting_review" {
		f.approve(f.findReview(b.Resource.ActionDigest), "forged-probe-review")
	}
	if _, err := f.mcpInternal("_effects.admit", map[string]any{"operation_id": b.Resource.ID, "expected_version": b.Resource.Version}); faultCode(err) != contract.CodePrerequisiteMissing {
		t.Fatalf("unowned initial probe: want prerequisite_missing, got %v", err)
	}
	f.mcpComplete(conn, 1, "validate", initial, nil)
	validated := f.must(f.owner, "connection.get", "", map[string]any{"scope": f.scope(), "id": conn})
	var c struct {
		Resource struct {
			Version         int64  `json:"version"`
			ValidationState string `json:"validation_state"`
		} `json:"resource"`
	}
	decode(t, validated.Data, &c)
	if c.Resource.ValidationState != "valid" {
		t.Fatalf("recorded validation not authoritative: %s", validated.Data)
	}
	discovery := f.mcpProbe(conn, c.Resource.Version, "discover")
	schema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"],"additionalProperties":false}`)
	canonical, err := contract.Canonicalize(schema)
	if err != nil {
		t.Fatal(err)
	}
	f.mcpComplete(conn, c.Resource.Version, "discover", discovery, map[string]any{"tools": []any{map[string]any{"name": "alpha", "input_schema": schema, "input_schema_digest": contract.Hash(canonical)}}, "next_cursor": ""})
	tools := f.must(f.owner, "connection.tools", "", map[string]any{"scope": f.scope(), "connection_id": conn})
	var listed struct {
		Items []json.RawMessage `json:"items"`
	}
	decode(t, tools.Data, &listed)
	if len(listed.Items) != 1 {
		t.Fatalf("discovery did not record catalog: %s", tools.Data)
	}
	exerciseMCPStaleCatalog(t, f, conn, c.Resource.Version, discovery, listed.Items[0])
}

// exerciseMCPStaleCatalog admits a real call with the full pinned envelope,
// then records a separately admitted discovery before the original claim.
// Rediscovery must invalidate that old pin without consuming its claim.
func exerciseMCPStaleCatalog(t *testing.T, f *fixture, conn contract.ID, version int64, discovery governedMCPOperation, item json.RawMessage) {
	t.Helper()
	var tool struct {
		ID          contract.ID     `json:"id"`
		Version     int64           `json:"version"`
		InputSchema json.RawMessage `json:"input_schema"`
		Digest      string          `json:"input_schema_digest"`
	}
	decode(t, item, &tool)
	f.grantMCPController(t, string(tool.ID))
	for _, binding := range []struct {
		kind   string
		target contract.ID
	}{{"tool", tool.ID}, {"connection", conn}} {
		f.activate("mcp-bound-"+binding.kind, "binding.create", map[string]any{"definition": map[string]any{"scope": f.scope(), "kind": binding.kind, "target_id": binding.target, "permissions": []string{"invoke"}, "destinations": []string{governedMCPEndpoint}}})
	}

	var action map[string]any
	decode(t, mustJSON(discovery.Action), &action)
	previous := action["parameters"].(map[string]any)
	var revision int64
	if err := f.db.Read(context.Background(), f.owner, f.scope(), func(u contract.Unit) error {
		return u.QueryRowContext(context.Background(), "SELECT revision FROM configuration_head WHERE id = 1").Scan(&revision)
	}); err != nil {
		t.Fatal(err)
	}
	action["configuration_revision"] = revision

	action["tool"] = map[string]any{"id": tool.ID, "version": tool.Version}
	action["parameters"] = map[string]any{"schema": "zatiti.mcp.action/v1", "kind": "call_tool", "session_handle": "synthetic-session", "tool": "alpha", "arguments": map[string]any{"q": "synthetic"}, "input_schema": tool.InputSchema, "input_schema_digest": tool.Digest, "classification": "restricted", "profile_digest": previous["profile_digest"], "control_reply_limit": 2}
	// Profile cost7 plus two authorized control responses at3 each.
	action["cost_bound"] = map[string]any{"currency": "USD", "micro_units": 13}
	var underfunded map[string]any
	decode(t, mustJSON(action), &underfunded)
	underfunded["cost_bound"] = map[string]any{"currency": "USD", "micro_units": 7}
	denied := f.must(f.owner, "operation.propose", "mcp-underfunded-call", map[string]any{"scope": f.scope(), "action": underfunded})
	var deniedBody struct {
		Resource governedMCPOperation `json:"resource"`
	}
	decode(t, denied.Data, &deniedBody)
	if _, err := f.mcpInternal("_effects.admit", map[string]any{"operation_id": deniedBody.Resource.ID, "expected_version": deniedBody.Resource.Version}); faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("underfunded call: want permission_denied, got %v", err)
	}
	proposed := f.must(f.owner, "operation.propose", "mcp-pinned-call", map[string]any{"scope": f.scope(), "action": action})
	var body struct {
		Resource governedMCPOperation `json:"resource"`
	}
	decode(t, proposed.Data, &body)
	admitted := f.mcpAdmit(body.Resource)
	next := f.mcpProbe(conn, version, "discover")
	changed := json.RawMessage(`{"type":"object","properties":{"q":{"type":"integer"}},"required":["q"],"additionalProperties":false}`)
	canon, err := contract.Canonicalize(changed)
	if err != nil {
		t.Fatal(err)
	}
	f.mcpComplete(conn, version, "discover", next, map[string]any{"tools": []any{map[string]any{"name": "alpha", "input_schema": changed, "input_schema_digest": contract.Hash(canon)}}, "next_cursor": ""})
	_, err = f.mcpInternal("_effects.claim", map[string]any{"operation_id": admitted.ID, "attempt_id": admitted.AttemptIDs[0], "generation": f.generation})
	if faultCode(err) != contract.CodeStaleVersion {
		t.Fatalf("changed catalog claim: want stale_version, got %v", err)
	}
	current := f.mcpOperation(admitted.ID)
	if current.State != "ready" {
		t.Fatalf("refused claim changed operation state to %q", current.State)
	}
}

// Grant exactly the capability and endpoint the trusted controller will
// recheck at admission, through the normal owner-reviewed grant lifecycle.
func (f *fixture) grantMCPController(t *testing.T, capability string) {
	t.Helper()
	input := map[string]any{"scope": f.scope(), "definition": map[string]any{"principal_id": f.controllerIdentity().PrincipalID, "scope": f.scope(), "capabilities": []string{capability}, "destinations": []string{governedMCPEndpoint}, "denied": false}}
	_, err := f.invoke(f.owner, "grant.create", "mcp-grant-stage-"+capability, input)
	if err == nil {
		return
	}
	digest := requiredDigest(t, err)
	f.approve(f.findReview(digest), "mcp-grant-review-"+capability)
	f.must(f.owner, "grant.create", "mcp-grant-apply-"+capability, input)
}

func (f *fixture) configureMCPBudget(t *testing.T) {
	t.Helper()
	// Read the actual numeric head; Revision.version is the resource version,
	// not the configuration sequence required by draft.create.
	var head int64
	err := f.db.Read(context.Background(), f.owner, f.scope(), func(u contract.Unit) error {
		return u.QueryRowContext(context.Background(), "SELECT revision FROM configuration_head WHERE id = 1").Scan(&head)
	})
	if err != nil {
		t.Fatal(err)
	}
	created := f.must(f.owner, "configuration.draft.create", "mcp-budget-draft", map[string]any{"scope": f.scope(), "base_revision": head})
	var body struct {
		Resource draftRef `json:"resource"`
	}
	decode(t, created.Data, &body)
	limits := map[string]any{"currency": "USD", "spend_micro_units": 10000, "concurrency": 4, "model_steps": 10, "child_count": 10, "delegation_depth": 5, "attempt_seconds": 1800, "root_deadline": "2027-01-01T00:00:00Z"}
	updated := f.must(f.owner, "configuration.draft.update", "mcp-budget-change", map[string]any{"scope": f.scope(), "id": body.Resource.ID, "expected_version": body.Resource.Version, "changes": []any{map[string]any{"kind": "budget", "action": "create", "id": f.installationID, "expected_version": 0, "definition": limits}}})
	decode(t, updated.Data, &body)
	plan := f.plan("mcp-budget-plan", body.Resource)
	_, err = f.apply("mcp-budget-apply-1", plan)
	digest := requiredDigest(t, err)
	f.approve(f.findReview(digest), "mcp-budget-review")
	if _, err = f.apply("mcp-budget-apply-2", plan); err != nil {
		t.Fatal(err)
	}
}
