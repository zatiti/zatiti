package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/identity"
)

// TestEndToEndBinary is the wave milestone: the real binary, as a human runs
// it. `zatiti serve` on an empty state directory; `zatiti init` over the
// socket; the owner credential handed to the owner profile; two real CLI
// commands as the owner (principal create with a submission key and its
// exact replay, then principal list); and `zatiti mcp serve` driven by the
// official go-sdk client over stdio. Every subprocess is cleaned up. Any
// failure is reported at its exact step; nothing here skips.
func TestEndToEndBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end binary test skipped in -short mode")
	}
	root := shortTempDir(t)
	bin := buildBinary(t, root)
	stateDir := filepath.Join(root, "state")
	socket := filepath.Join(root, "s.sock")
	masterKey := writeMasterKey(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// A credential must never arrive through the environment: the CLI
	// subprocesses get only the configuration variables.
	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		envStateDir + "=" + stateDir, envSocket + "=" + socket,
	}

	serve := exec.CommandContext(ctx, bin, "serve", "--credential-backend", "headless", "--master-key", masterKey, "--tick-interval", "100ms")
	serve.Env = env
	serveLog := &lockedBuffer{}
	serve.Stdout = &failOnWrite{t: t, name: "serve stdout"}
	serve.Stderr = serveLog
	if err := serve.Start(); err != nil {
		t.Fatalf("starting serve: %v", err)
	}
	serveExit := make(chan error, 1)
	go func() { serveExit <- serve.Wait() }()
	t.Cleanup(func() {
		_ = serve.Process.Signal(syscall.SIGKILL)
		<-serveExit
	})
	waitFor(t, startupBudget, "the controller socket", func() bool {
		select {
		case err := <-serveExit:
			serveExit <- err
			t.Fatalf("serve exited before listening: %v\n%s", err, serveLog.String())
		default:
		}
		_, statErr := os.Stat(socket)
		return statErr == nil
	})

	cli := func(args ...string) cliResult {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("running %v: %v", args, err)
		}
		return cliResult{code: code, out: out.String(), err: errb.String()}
	}
	envelope := func(step string, r cliResult) contract.Result {
		t.Helper()
		if r.code != 0 {
			t.Fatalf("%s: exit %d\nstdout: %s\nstderr: %s\nserve log:\n%s", step, r.code, r.out, r.err, serveLog.String())
		}
		var res contract.Result
		if err := json.Unmarshal([]byte(r.out), &res); err != nil {
			t.Fatalf("%s: stdout is not one JSON envelope: %q: %v", step, r.out, err)
		}
		if res.Status != contract.StatusCompleted {
			t.Fatalf("%s: status %s: %+v", step, res.Status, res.Error)
		}
		return res
	}

	// 1. init over the socket; the CLI reports where the owner credential
	// landed (never the credential) and the profile is ready on return.
	initRes := envelope("zatiti init", cli("init", "--json", "--input", `{"credential_store":"headless","owner_name":"End To End Owner","headless_key_ref":"installation/owner"}`))
	var initOut struct {
		Resource struct {
			InstallationID contract.ID `json:"installation_id"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(initRes.Data, &initOut); err != nil || initOut.Resource.InstallationID == "" {
		t.Fatalf("init data %s: %v", initRes.Data, err)
	}
	profile := filepath.Join(stateDir, profilesDirName, defaultProfile)
	if _, err := os.Stat(profile); err != nil {
		t.Fatalf("owner profile was not handed over: %v\nserve log:\n%s", err, serveLog.String())
	}
	if r := cli("init", "--json", "--input", `{"credential_store":"headless","owner_name":"x","headless_key_ref":"installation/owner"}`); r.code != 4 || strings.Contains(r.err, "Bearer") {
		t.Fatalf("second init: exit %d, want 4 (conflict) and no credential on stderr: %s", r.code, r.err)
	}

	// 2. A keyed mutation as the owner, then its exact replay.
	scope := fmt.Sprintf(`{"installation_id":%q}`, initOut.Resource.InstallationID)
	createInput := `{"scope":` + scope + `,"definition":{"kind":"client_agent","name":"e2e-agent","scope":` + scope + `,"revoked":false}}`
	created := envelope("principal create", cli("principal", "create", "--json", "--submission-key", "e2e-principal-1", "--input", createInput))
	if created.CommandID == "" || !strings.Contains(string(created.Data), "e2e-agent") {
		t.Fatalf("principal create returned %+v", created)
	}
	replay := envelope("principal create replay", cli("principal", "create", "--json", "--submission-key", "e2e-principal-1", "--input", createInput))
	if replay.CommandID != created.CommandID {
		t.Fatalf("replay command %s, want %s", replay.CommandID, created.CommandID)
	}
	if r := cli("principal", "create", "--json", "--submission-key", "e2e-principal-1", "--input", strings.Replace(createInput, "e2e-agent", "other", 1)); r.code != 4 || !strings.Contains(r.out, contract.CodeSubmissionConflict) {
		t.Fatalf("changed input under the same key: exit %d %s", r.code, r.out)
	}

	// 3. A query as the owner.
	listed := envelope("principal list", cli("principal", "list", "--json", "--input", `{"scope":`+scope+`}`))
	if !strings.Contains(string(listed.Data), "e2e-agent") || !strings.Contains(string(listed.Data), identity.ControllerPrincipalName) {
		t.Fatalf("principal list lacks the created and controller principals: %s", listed.Data)
	}
	// An unknown profile authenticates nothing and never reaches the socket
	// with a guessed default.
	if r := cli("principal", "list", "--profile", "nobody", "--input", `{"scope":`+scope+`}`); r.code == 0 || !strings.Contains(r.err, "nobody") {
		t.Fatalf("unknown profile: exit %d %s", r.code, r.err)
	}

	// 4. MCP over stdio with the official client: a second controller is
	// never started, tools are the catalog, a call is the same operation.
	mcpCmd := exec.CommandContext(ctx, bin, "mcp", "serve")
	mcpCmd.Env = env
	mcpLog := &lockedBuffer{}
	mcpCmd.Stderr = mcpLog
	transport := &gosdk.CommandTransport{Command: mcpCmd}
	mcpClient := gosdk.NewClient(&gosdk.Implementation{Name: "e2e", Version: "0"}, nil)
	session, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v\n%s", err, mcpLog.String())
	}
	defer func() { _ = session.Close() }()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"zatiti_principal_list", "zatiti_principal_create", "zatiti_capabilities", "zatiti_installation_init"} {
		if !names[want] {
			t.Fatalf("tools/list lacks %s (%d tools)", want, len(tools.Tools))
		}
	}
	call, err := session.CallTool(ctx, &gosdk.CallToolParams{
		Name:      "zatiti_principal_list",
		Arguments: map[string]any{"input": map[string]any{"scope": map[string]any{"installation_id": initOut.Resource.InstallationID}}},
	})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if call.IsError {
		t.Fatalf("tools/call isError: %+v", call.Content)
	}
	structured, err := json.Marshal(call.StructuredContent)
	if err != nil || !strings.Contains(string(structured), "e2e-agent") {
		t.Fatalf("structuredContent %s (%v) lacks the created principal", structured, err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("closing the MCP session: %v", err)
	}
	if strings.Contains(mcpLog.String(), "Bearer") {
		t.Fatalf("mcp diagnostics leaked a credential: %s", mcpLog.String())
	}

	// 5. Signal shutdown: serve exits 0 and removes its socket.
	if err := serve.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signalling serve: %v", err)
	}
	select {
	case err := <-serveExit:
		serveExit <- err
		if err != nil {
			t.Fatalf("serve exit after SIGTERM: %v\n%s", err, serveLog.String())
		}
	case <-time.After(shutdownGrace + 10*time.Second):
		t.Fatalf("serve did not exit after SIGTERM\n%s", serveLog.String())
	}
	if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
	if log := serveLog.String(); !strings.Contains(log, "bootstrap completed") || !strings.Contains(log, "controller running") {
		t.Fatalf("serve log lacks the bootstrap and controller lines:\n%s", log)
	}
}

// cliResult is one CLI subprocess outcome.
type cliResult struct {
	code     int
	out, err string
}

// buildBinary compiles cmd/zatiti into dir.
func buildBinary(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "zatiti")
	build := exec.Command("go", "build", "-p", "2", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// failOnWrite fails the test if a byte is ever written: serve's stdout must
// stay empty.
type failOnWrite struct {
	t    *testing.T
	name string
}

func (f *failOnWrite) Write(p []byte) (int, error) {
	f.t.Errorf("%s received %q; it must stay empty", f.name, p)
	return len(p), nil
}
