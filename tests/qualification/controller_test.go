package qualification_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zatiti/zatiti/internal/contract"
)

// The controlled controller: the real binary built from this tree, served
// on a short private socket, initialized once and shared by every case
// that needs a live installation. Nothing here reaches into the binary's
// state directory except to read the owner profile it wrote, the way a
// human operator's CLI does.

const (
	startupBudget = 3 * time.Minute
	shutdownGrace = 30 * time.Second
	// callBudget bounds one CLI or raw MCP subprocess. The CLI compiles the
	// operation catalog at startup, which is slow on a loaded machine.
	callBudget = 5 * time.Minute
)

// controller is one served installation.
type controller struct {
	bin, root, stateDir, socket string
	installationID              contract.ID
	credential                  []byte
	serve                       *exec.Cmd
	serveExit                   chan error
	serveLog                    *lockedBuffer
	cancel                      context.CancelFunc
}

var (
	shared    *controller
	sharedErr error
)

// startShared builds and starts the shared controller; failures are
// reported by every case that needs it rather than aborting the package.
func startShared() {
	c, err := startController(context.Background())
	if err != nil {
		sharedErr = err
		return
	}
	shared = c
}

func stopShared() {
	if shared != nil {
		shared.stop()
	}
}

// needController returns the shared controller or fails the case with the
// start error: a controller that could not be built or served is a failed
// qualification, not a skipped one.
func needController(t *testing.T, c *caseRun) *controller {
	t.Helper()
	if sharedErr != nil {
		c.fail("the controlled controller did not start: %v", sharedErr)
	}
	c.version("zatiti_binary_sha256", fileDigest(shared.bin))
	return shared
}

// shortTempDir returns a directory whose socket path stays under the
// 104-byte Unix socket limit; nested test temp layouts exceed it.
func shortTempDir() (string, error) {
	return os.MkdirTemp("", "ztq")
}

// buildBinary compiles cmd/zatiti from the module this test tree belongs to.
func buildBinary(dir string) (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "zatiti")
	build := exec.Command("go", "build", "-p", "2", "-o", bin, "./cmd/zatiti")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/zatiti: %v\n%s", err, out)
	}
	return bin, nil
}

func writeMasterKey(dir string) (string, error) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(0x51 + i)
	}
	path := filepath.Join(dir, "master.key")
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return "", err
	}
	return "file:" + path, nil
}

// startController builds the binary, serves it headless on a fresh state
// directory, initializes the installation over the socket and reads back
// the owner profile the controller wrote.
func startController(ctx context.Context) (*controller, error) {
	root, err := shortTempDir()
	if err != nil {
		return nil, err
	}
	bin, err := buildBinary(root)
	if err != nil {
		return nil, err
	}
	masterKey, err := writeMasterKey(root)
	if err != nil {
		return nil, err
	}
	c := &controller{
		bin: bin, root: root,
		stateDir: filepath.Join(root, "state"), socket: filepath.Join(root, "s.sock"),
		serveLog: &lockedBuffer{}, serveExit: make(chan error, 1),
	}
	serveCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.serve = exec.CommandContext(serveCtx, bin, "serve", "--credential-backend", "headless", "--master-key", masterKey, "--tick-interval", "100ms")
	c.serve.Env = c.env()
	c.serve.Stderr = c.serveLog
	c.serve.Stdout = c.serveLog
	if err := c.serve.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("starting serve: %w", err)
	}
	go func() { c.serveExit <- c.serve.Wait() }()
	if err := c.waitForSocket(); err != nil {
		c.stop()
		return nil, err
	}
	res, err := c.cli(ctx, "init", "--json", "--input", `{"credential_store":"headless","owner_name":"Qualification Owner","headless_key_ref":"installation/owner"}`)
	if err != nil {
		c.stop()
		return nil, err
	}
	env, err := res.envelope()
	if err != nil {
		c.stop()
		return nil, fmt.Errorf("init: %w", err)
	}
	var out struct {
		Resource struct {
			InstallationID contract.ID `json:"installation_id"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(env.Data, &out); err != nil || out.Resource.InstallationID == "" {
		c.stop()
		return nil, fmt.Errorf("init data %s: %v", env.Data, err)
	}
	c.installationID = out.Resource.InstallationID
	cred, err := c.readOwnerProfile()
	if err != nil {
		c.stop()
		return nil, err
	}
	c.credential = cred
	return c, nil
}

// env is the configuration environment every subprocess receives: never
// a credential, never the parent's full environment.
func (c *controller) env() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"ZATITI_STATE_DIR=" + c.stateDir, "ZATITI_SOCKET=" + c.socket,
	}
}

func (c *controller) waitForSocket() error {
	deadline := time.Now().Add(startupBudget)
	for time.Now().Before(deadline) {
		select {
		case err := <-c.serveExit:
			c.serveExit <- err
			return fmt.Errorf("serve exited before listening: %v\n%s", err, c.serveLog.String())
		default:
		}
		if _, err := os.Stat(c.socket); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("serve did not listen within %s\n%s", startupBudget, c.serveLog.String())
}

// readOwnerProfile reads the one profile the controller handed over at
// bootstrap: the complete Authorization header value.
func (c *controller) readOwnerProfile() ([]byte, error) {
	dir := filepath.Join(c.stateDir, "profiles")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("owner profile was not handed over: %w", err)
	}
	if len(entries) != 1 {
		return nil, fmt.Errorf("profiles directory holds %d entries, want the owner profile alone", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		return nil, err
	}
	return bytes.TrimSpace(data), nil
}

func (c *controller) stop() {
	if c.serve == nil || c.serve.Process == nil {
		return
	}
	_ = c.serve.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-c.serveExit:
		c.serveExit <- err
	case <-time.After(shutdownGrace):
		_ = c.serve.Process.Kill()
		<-c.serveExit
	}
	c.cancel()
}

// cliResult is one CLI subprocess outcome.
type cliResult struct {
	args     []string
	code     int
	out, err string
}

// envelope decodes the single JSON result envelope --json mode emits.
func (r cliResult) envelope() (contract.Result, error) {
	var res contract.Result
	if err := json.Unmarshal([]byte(r.out), &res); err != nil {
		return res, fmt.Errorf("zatiti %s: stdout is not one JSON envelope (exit %d): %q (stderr %q)", strings.Join(r.args, " "), r.code, r.out, r.err)
	}
	if res.Status != contract.StatusCompleted {
		return res, fmt.Errorf("zatiti %s: status %s (exit %d): %+v", strings.Join(r.args, " "), res.Status, r.code, res.Error)
	}
	return res, nil
}

// cli runs one command of the binary as the selected profile.
func (c *controller) cli(ctx context.Context, args ...string) (cliResult, error) {
	ctx, cancel := context.WithTimeout(ctx, callBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Env = c.env()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := cliResult{args: args, out: out.String(), err: errb.String()}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		r.code = exit.ExitCode()
		if ctx.Err() != nil {
			return r, fmt.Errorf("zatiti %v exceeded the %s call budget (%v)", args, callBudget, ctx.Err())
		}
	} else if err != nil {
		return r, fmt.Errorf("running zatiti %v: %w", args, err)
	}
	return r, nil
}

// ---------- MCP ----------

// mcpSession is one `zatiti mcp serve` subprocess driven by the pinned
// official client with every optional feature left unconfigured: no
// sampling, elicitation, roots, resources or prompts handlers.
type mcpSession struct {
	cmd     *exec.Cmd
	session *gosdk.ClientSession
	log     *lockedBuffer
}

func (c *controller) mcp(ctx context.Context, clientName string, args ...string) (*mcpSession, error) {
	cmd := exec.CommandContext(ctx, c.bin, append([]string{"mcp", "serve"}, args...)...)
	cmd.Env = c.env()
	log := &lockedBuffer{}
	cmd.Stderr = log
	transport := &gosdk.CommandTransport{Command: cmd}
	cl := gosdk.NewClient(&gosdk.Implementation{Name: clientName, Version: "0"}, nil)
	session, err := cl.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect: %v\n%s", err, log.String())
	}
	return &mcpSession{cmd: cmd, session: session, log: log}, nil
}

func (m *mcpSession) close() { _ = m.session.Close() }

// call invokes one tool and decodes the structured envelope.
func (m *mcpSession) call(ctx context.Context, tool string, input any, key string) (*gosdk.CallToolResult, contract.Result, error) {
	args := map[string]any{"input": input}
	if key != "" {
		args["submission_key"] = key
	}
	res, err := m.session.CallTool(ctx, &gosdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return nil, contract.Result{}, err
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return res, contract.Result{}, err
	}
	var env contract.Result
	if err := json.Unmarshal(raw, &env); err != nil {
		return res, contract.Result{}, fmt.Errorf("structuredContent is not a result envelope: %s", raw)
	}
	return res, env, nil
}

// textOf concatenates the text content of a tool result.
func textOf(res *gosdk.CallToolResult) string {
	var b strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*gosdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// rawFrame writes one raw JSON-RPC line to a fresh `mcp serve` process and
// returns the first line it answers, for protocol-level error cases the
// SDK client cannot produce.
func (c *controller) rawFrame(ctx context.Context, frame string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, callBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, "mcp", "serve")
	cmd.Env = c.env()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if _, err := io.WriteString(stdin, frame+"\n"); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("no answer to the raw frame: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// TestZ01DuplicateControllerRefusesExclusiveOwnership is
// Z01.duplicate_controller: a second real `zatiti serve` process started
// against the shared controller's own state directory (a different socket,
// so the collision under test is the state directory's exclusive install
// lock, never an unrelated socket bind conflict) must refuse to serve
// anything, and the original controller must keep working throughout.
func TestZ01DuplicateControllerRefusesExclusiveOwnership(t *testing.T) {
	c := beginCase(t, "Z01.duplicate_controller", "Z01",
		"Second controller refuses exclusive ownership and serves no requests.",
		"Only the original controller admits work; no extra scheduler or writer appears.")
	ctrl := needController(t, c)

	before, err := ctrl.cli(context.Background(), "installation", "status", "--json",
		"--input", `{"scope":{"installation_id":"`+string(ctrl.installationID)+`"}}`)
	if err != nil {
		c.fail("original controller query before the duplicate attempt: %v", err)
	}
	if _, err := before.envelope(); err != nil {
		c.fail("original controller was not healthy before the duplicate attempt: %v", err)
	}

	secondSocket := filepath.Join(ctrl.root, "second.sock")
	masterKey := "file:" + filepath.Join(ctrl.root, "master.key")
	// A real exclusive-lock refusal is near-instant (opening an already-
	// locked SQLite state file fails immediately, not after minutes); this
	// budget is generous headroom on a loaded machine, not an expectation
	// that a duplicate controller legitimately takes this long to refuse.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dup := exec.CommandContext(ctx, ctrl.bin, "serve", "--credential-backend", "headless", "--master-key", masterKey, "--tick-interval", "100ms")
	dup.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"ZATITI_STATE_DIR=" + ctrl.stateDir, "ZATITI_SOCKET=" + secondSocket,
	}
	var dupLog lockedBuffer
	dup.Stdout, dup.Stderr = &dupLog, &dupLog
	runErr := dup.Run()
	if runErr == nil {
		c.fail("a second `zatiti serve` against the same state directory exited 0; it must refuse exclusive ownership\nlog:\n%s", dupLog.String())
	}
	c.observe("a second `zatiti serve` against the shared controller's own state directory exited non-zero (%v), refusing exclusive ownership", runErr)
	c.attach("duplicate_controller_log", dupLog.String())
	if _, err := os.Stat(secondSocket); err == nil {
		c.fail("the duplicate controller created a second socket at %s; it must serve no requests", secondSocket)
	}
	c.observe("the duplicate controller never created a socket at %s: no extra scheduler or writer appeared", secondSocket)

	after, err := ctrl.cli(context.Background(), "installation", "status", "--json",
		"--input", `{"scope":{"installation_id":"`+string(ctrl.installationID)+`"}}`)
	if err != nil {
		c.fail("original controller query after the duplicate attempt: %v", err)
	}
	if _, err := after.envelope(); err != nil {
		c.fail("the original controller stopped admitting work after the duplicate attempt: %v", err)
	}
	c.observe("the original controller kept serving completed queries throughout and after the duplicate attempt")
}

// lockedBuffer is a concurrency-safe log sink.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
