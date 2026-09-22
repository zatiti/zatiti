package qualification_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

const (
	goSDKPin        = "github.com/modelcontextprotocol/go-sdk v1.7.0"
	protocolPin     = "2025-11-25"
	catalogCLIName  = "capabilities"
	mcpToolPrefix   = "zatiti_"
	principalCreate = "principal.create"
)

// TestQualificationBaselineMCP is QUALIFICATION.baseline_mcp: the pinned
// protocol client with resources, prompts, sampling, elicitation and task
// extensions unconfigured discovers and operates the product over stdio —
// discovery against the registry, a setup challenge, long work followed by
// durable command lookup, and recovery of a mutation whose session died —
// with the frozen result/error mapping on every answer.
func TestQualificationBaselineMCP(t *testing.T) {
	c := beginCase(t, "QUALIFICATION.baseline_mcp", "QUALIFICATION",
		"Baseline stdio tools and durable polling provide complete authorized functionality without optional features.",
		"Input/output schemas, result/error mapping and negotiated protocol behavior match the pinned supported revision.")
	ctrl := needController(t, c)
	c.version("mcp_go_sdk", goSDKPin)
	c.version("mcp_protocol_pin", protocolPin)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// 1. Negotiation and discovery against the registry the CLI exposes.
	m, err := ctrl.mcp(ctx, "qualification-baseline")
	if err != nil {
		c.fail("%v", err)
	}
	defer m.close()
	init := m.session.InitializeResult()
	if init == nil || init.ProtocolVersion != protocolPin {
		c.fail("negotiated protocol %+v, want %s", init, protocolPin)
	}
	c.observe("initialize negotiated protocol %s with server %s %s", init.ProtocolVersion, init.ServerInfo.Name, init.ServerInfo.Version)
	if caps := init.Capabilities; caps != nil {
		c.attach("server_capabilities", caps)
	}
	tools, err := m.session.ListTools(ctx, nil)
	if err != nil {
		c.fail("tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
		if tool.InputSchema == nil {
			c.fail("tool %s advertises no input schema", tool.Name)
		}
	}
	res, err := ctrl.cli(ctx, catalogCLIName, "--json", "--input", `{}`)
	if err != nil {
		c.fail("%v", err)
	}
	catalog, err := res.envelope()
	if err != nil {
		c.fail("%v", err)
	}
	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(catalog.Data, &listed); err != nil {
		c.fail("capabilities data: %v", err)
	}
	missing := []string{}
	for _, item := range listed.Items {
		want := mcpToolName(item.ID)
		if !names[want] {
			missing = append(missing, item.ID+" -> "+want)
		}
	}
	if len(missing) > 0 {
		c.fail("%d catalog operations have no MCP tool: %v", len(missing), missing)
	}
	c.observe("tools/list advertised %d tools; every one of the %d catalog operations the CLI lists has its zatiti_ tool with an input schema", len(tools.Tools), len(listed.Items))
	c.attach("tool_count", len(tools.Tools))
	c.attach("catalog_count", len(listed.Items))

	// 2. A setup challenge without secret bytes: a second init is refused
	// by the one-time initializer and the refusal names no credential.
	setup, env, err := m.call(ctx, mcpToolName("installation.init"), map[string]any{
		"credential_store": "headless", "owner_name": "again", "headless_key_ref": "installation/owner",
	}, "")
	if err != nil {
		c.fail("installation.init over MCP: %v", err)
	}
	if !setup.IsError || env.Status != contract.StatusFailed || env.Error == nil {
		c.fail("re-initialization answered %+v, want a failed envelope with isError", env)
	}
	if strings.Contains(textOf(setup), "Bearer") || strings.Contains(string(env.Data), "Bearer") {
		c.fail("the setup refusal carried credential bytes")
	}
	c.observe("re-init over MCP refused with %s (isError=true), no credential bytes in the tool result", env.Error.Code)

	// 3. Long work through durable identities: a keyed mutation, then the
	// command looked up by its submission key from a different session.
	key := "qual-baseline-" + string(contract.NewID())[:8]
	input := map[string]any{
		"scope": map[string]any{"installation_id": ctrl.installationID},
		"definition": map[string]any{
			"kind": "client_agent", "name": "baseline-agent-" + key,
			"scope": map[string]any{"installation_id": ctrl.installationID}, "revoked": false,
		},
	}
	created, createdEnv, err := m.call(ctx, mcpToolName(principalCreate), input, key)
	if err != nil {
		c.fail("principal.create over MCP: %v", err)
	}
	if created.IsError || createdEnv.Status != contract.StatusCompleted || createdEnv.CommandID == "" {
		c.fail("principal.create answered %+v", createdEnv)
	}
	var text contract.Result
	if err := json.Unmarshal([]byte(textOf(created)), &text); err != nil || text.CommandID != createdEnv.CommandID {
		c.fail("text content is not the same envelope as structuredContent: %q", textOf(created))
	}
	c.observe("principal.create with submission key completed as command %s; text content equals structuredContent", createdEnv.CommandID)

	// The session dies; a fresh one recovers the disposition by lookup,
	// then replays with the original key and gets the same command.
	m.close()
	m2, err := ctrl.mcp(ctx, "qualification-baseline-2")
	if err != nil {
		c.fail("%v", err)
	}
	defer m2.close()
	lookup, lookupEnv, err := m2.call(ctx, mcpToolName("command.get"), map[string]any{
		"scope":          map[string]any{"installation_id": ctrl.installationID},
		"submission_key": key, "operation": principalCreate, "operation_version": 1,
	}, "")
	if err != nil {
		c.fail("command.get: %v", err)
	}
	if lookup.IsError || lookupEnv.Status != contract.StatusCompleted {
		c.fail("command.get answered %+v", lookupEnv)
	}
	var cmd struct {
		Resource struct {
			ID     contract.ID `json:"id"`
			Status string      `json:"status"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(lookupEnv.Data, &cmd); err != nil || cmd.Resource.ID != createdEnv.CommandID {
		c.fail("command.get returned %s, want command %s", lookupEnv.Data, createdEnv.CommandID)
	}
	_, replayEnv, err := m2.call(ctx, mcpToolName(principalCreate), input, key)
	if err != nil {
		c.fail("replay: %v", err)
	}
	if replayEnv.CommandID != createdEnv.CommandID {
		c.fail("replay from a new session minted command %s, want the original %s", replayEnv.CommandID, createdEnv.CommandID)
	}
	c.observe("after the session ended, command.get by submission key found command %s and the keyed replay returned it unchanged", cmd.Resource.ID)

	// 4. Domain faults are tool results with isError; a keyed mutation
	// whose input changed under the same key is refused as a conflict.
	changed := map[string]any{}
	for k, v := range input {
		changed[k] = v
	}
	changed["definition"] = map[string]any{
		"kind": "client_agent", "name": "renamed", "scope": map[string]any{"installation_id": ctrl.installationID}, "revoked": false,
	}
	conflict, conflictEnv, err := m2.call(ctx, mcpToolName(principalCreate), changed, key)
	if err != nil {
		c.fail("changed-input replay: %v", err)
	}
	if !conflict.IsError || conflictEnv.Error == nil || conflictEnv.Error.Code != contract.CodeSubmissionConflict {
		c.fail("changed input under the reused key answered %+v, want submission_conflict with isError", conflictEnv)
	}
	c.observe("changed input under the same key: isError=true, error.code=%s", conflictEnv.Error.Code)

	// The flow just proven above (steps 3-4: keyed create, session death,
	// lookup-by-key and keyed replay from a fresh session, changed input
	// under the same key refused) is also the exact behavior Z04.
	// submission_replay and Z16.disconnect_command_lookup name; it is
	// recorded again here under each case's own identity rather than only
	// under QUALIFICATION.baseline_mcp, so a release audit finds it. This
	// harness does not additionally prove restart-survival or the >=30-day
	// retention window Z04.submission_replay's own text also names; that
	// narrower scope is recorded honestly rather than claimed.
	z04 := beginCase(t, "Z04.submission_replay", "Z04",
		"Identical retry returns the original durable command disposition without another revision or event.",
		"Changed input is refused; command identity survives restart and remains retained through unresolved obligations and at least 30 days.")
	z04.observe("identical replay from a fresh session (after the original session ended) returned the original command %s unchanged; changed input under the same key was refused %s", createdEnv.CommandID, conflictEnv.Error.Code)
	z04.observe("restart-survival and the >=30-day retention window are not exercised by this harness; only same-run replay/lookup and changed-input refusal are proven here")
	z16 := beginCase(t, "Z16.disconnect_command_lookup", "Z16",
		"The original disposition is recovered without another business mutation or event.",
		"JSON-RPC request IDs are not treated as durable submission keys.")
	z16.observe("after the MCP session that submitted command %s ended (a real transport disconnect), a fresh session recovered the identical disposition through command.get keyed by submission_key, and a same-key replay returned the same command rather than minting a second one", createdEnv.CommandID)

	// 5. A malformed protocol message is a protocol error, not a tool
	// result: an unknown method gets a JSON-RPC error object.
	answer, err := ctrl.rawFrame(ctx, `{"jsonrpc":"2.0","id":7,"method":"zatiti/not-a-method"}`)
	if err != nil {
		c.fail("raw frame: %v", err)
	}
	var rpc struct {
		ID    any `json:"id"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Result any `json:"result"`
	}
	if err := json.Unmarshal([]byte(answer), &rpc); err != nil || rpc.Error == nil || rpc.Result != nil {
		c.fail("unknown method answered %q, want a JSON-RPC error object", answer)
	}
	c.observe("unknown JSON-RPC method answered with protocol error code %d (%q), no tool result", rpc.Error.Code, rpc.Error.Message)
	c.attach("raw_protocol_error", answer)
	if strings.Contains(m2.log.String(), "Bearer") {
		c.fail("mcp diagnostics leaked a credential")
	}
}

// TestZ13CompatibilityNotContainment is the qualification share of
// Z13.compatibility_not_containment: a client's self-declared name changes
// nothing the controller grants. Two sessions naming themselves after
// external agent clients see the identical tool surface and the identical
// refusal of an unkeyed mutation; the capability catalog declares no
// containment, sandbox or interruption guarantee for any executor.
func TestZ13CompatibilityNotContainment(t *testing.T) {
	c := beginCase(t, "Z13.compatibility_not_containment", "Z13",
		"Client interoperability grants no implicit launch, sandbox, interruption, cost-enforcement or exact-context-replay guarantees.",
		"Unsupported executor guarantees fail explicitly instead of being inferred from a client, harness, container or subprocess name.")
	ctrl := needController(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	surfaces := map[string][]string{}
	refusals := map[string]string{}
	for _, name := range []string{"claude-code", "codex", "qualification-generic"} {
		m, err := ctrl.mcp(ctx, name)
		if err != nil {
			c.fail("%v", err)
		}
		tools, err := m.session.ListTools(ctx, nil)
		if err != nil {
			c.fail("tools/list as %s: %v", name, err)
		}
		list := make([]string, 0, len(tools.Tools))
		for _, tool := range tools.Tools {
			list = append(list, tool.Name)
		}
		surfaces[name] = list
		// An unkeyed mutation is refused for every client name alike: the
		// name selected nothing.
		_, env, err := m.call(ctx, mcpToolName(principalCreate), map[string]any{
			"scope": map[string]any{"installation_id": ctrl.installationID},
			"definition": map[string]any{
				"kind": "client_agent", "name": "posing-" + name,
				"scope": map[string]any{"installation_id": ctrl.installationID}, "revoked": false,
			},
		}, "")
		if err != nil {
			c.fail("unkeyed mutation as %s: %v", name, err)
		}
		if env.Error == nil {
			c.fail("unkeyed mutation as %s was admitted: %+v", name, env)
		}
		refusals[name] = env.Error.Code
		m.close()
	}
	base := strings.Join(surfaces["qualification-generic"], ",")
	for name, list := range surfaces {
		if strings.Join(list, ",") != base {
			c.fail("client %q saw a different tool surface than the generic client", name)
		}
		if refusals[name] != refusals["qualification-generic"] {
			c.fail("client %q got refusal %s, generic got %s", name, refusals[name], refusals["qualification-generic"])
		}
	}
	c.observe("clients named claude-code, codex and qualification-generic saw the same %d tools and the same refusal (%s) of an unkeyed mutation", len(surfaces["qualification-generic"]), refusals["qualification-generic"])

	// The public catalog declares executor behavior only as declared
	// capabilities and advisory limits; nothing in it claims containment.
	res, err := ctrl.cli(ctx, catalogCLIName, "--json", "--input", `{}`)
	if err != nil {
		c.fail("%v", err)
	}
	env, err := res.envelope()
	if err != nil {
		c.fail("%v", err)
	}
	lower := strings.ToLower(string(env.Data))
	for _, claim := range []string{"sandboxed", "contained executor", "guaranteed interruption", "exactly-once"} {
		if strings.Contains(lower, claim) {
			c.fail("the public catalog claims %q", claim)
		}
	}
	c.observe("the public capability catalog (%d bytes) makes no sandbox, contained-executor, guaranteed-interruption or exactly-once claim", len(env.Data))
}

// mcpToolName maps a dot-separated operation id to its MCP tool name,
// including the two frozen exceptions.
func mcpToolName(operation string) string {
	switch operation {
	case "installation.init":
		return mcpToolPrefix + "installation_init"
	case "capabilities.list":
		return mcpToolPrefix + "capabilities"
	}
	return mcpToolPrefix + strings.ReplaceAll(operation, ".", "_")
}
