package qualification_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// QUALIFICATION.named_agent_clients: a compatibility claim for Claude
// Code, Codex or Cursor needs an actual session of the exact version. A
// real session is networked and billed to an operator account, so it runs
// only when ZATITI_QUALIFICATION_AGENT_CLIENTS=1 is set explicitly; without
// it the case records the client versions present on this host and is not
// run, which blocks every named-client compatibility claim.
const agentClientsOptIn = "ZATITI_QUALIFICATION_AGENT_CLIENTS"

// agentClient is one named client and how a headless session of it is
// driven against `zatiti mcp serve`.
type agentClient struct {
	name    string
	binary  string
	version []string
	// run returns the command that starts one headless session told to
	// discover and call the capabilities tool through the MCP server whose
	// stdio config file is at configPath.
	run func(ctx context.Context, binary, configPath string) *exec.Cmd
}

var agentClients = []agentClient{
	{
		name: "claude-code", binary: "claude", version: []string{"--version"},
		run: func(ctx context.Context, binary, configPath string) *exec.Cmd {
			return exec.CommandContext(ctx, binary, "-p",
				"--output-format", "json", "--strict-mcp-config", "--mcp-config", configPath,
				"--allowedTools", "mcp__zatiti__zatiti_capabilities",
				"Call the zatiti_capabilities tool exactly once and reply with only the number of items it returned.")
		},
	},
	{
		name: "codex", binary: "codex", version: []string{"--version"},
		// Codex's headless MCP configuration is a config-file key, not a
		// flag; no driver is pinned for it here, so its session is not run
		// even when the opt-in is set. Its version is still recorded.
	},
	{name: "cursor", binary: "cursor", version: []string{"--version"}},
}

func TestQualificationNamedAgentClients(t *testing.T) {
	c := beginCase(t, "QUALIFICATION.named_agent_clients", "QUALIFICATION",
		"Each named client/version has retained executed interoperability evidence.",
		"Unsupported or untested clients are not advertised as qualified and client compatibility is not an executor-containment claim.")
	present := map[string]string{}
	for _, cl := range agentClients {
		v, ok := toolVersion(cl.binary, cl.version...)
		c.version("client_"+cl.name, v)
		if ok {
			present[cl.name] = v
		}
	}
	if os.Getenv(agentClientsOptIn) != "1" {
		c.notRun("a named-client session is networked and billed; set %s=1 to run the sessions of the installed clients (%v); no client is advertised as compatible without an executed session", agentClientsOptIn, present)
	}
	ctrl := needController(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// One MCP server config every client can consume: the binary, its
	// configuration environment, no credential.
	config := map[string]any{"mcpServers": map[string]any{"zatiti": map[string]any{
		"command": ctrl.bin, "args": []string{"mcp", "serve"},
		"env": map[string]string{"ZATITI_STATE_DIR": ctrl.stateDir, "ZATITI_SOCKET": ctrl.socket},
	}}}
	raw, _ := json.Marshal(config)
	configPath := filepath.Join(ctrl.root, "mcp-config.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		c.fail("writing the MCP config: %v", err)
	}

	executed := 0
	for _, cl := range agentClients {
		if _, ok := present[cl.name]; !ok || cl.run == nil {
			c.observe("%s: no executed session (installed=%v, driver=%v); not advertised", cl.name, ok, cl.run != nil)
			continue
		}
		before := strings.Count(ctrl.serveLog.String(), "capabilities.list")
		cmd := cl.run(ctx, cl.binary, configPath)
		cmd.Dir = ctrl.root
		out, err := cmd.CombinedOutput()
		c.attach(cl.name+"_session_output", truncate(string(out), 4096))
		if err != nil {
			c.fail("%s session (%s) failed: %v\n%s", cl.name, present[cl.name], err, out)
		}
		after := strings.Count(ctrl.serveLog.String(), "capabilities.list")
		if after <= before {
			c.fail("%s session (%s) exited 0 but the controller served no capabilities.list call from it", cl.name, present[cl.name])
		}
		c.observe("%s %s: headless session discovered the MCP tools and the controller served %d capabilities.list call(s) from it", cl.name, present[cl.name], after-before)
		executed++
	}
	if executed == 0 {
		c.fail("no client session executed; nothing can be advertised")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("... (%d bytes)", len(s))
}
