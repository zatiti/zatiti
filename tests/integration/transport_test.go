package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zatiti/zatiti/internal/cli"
	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/mcp"
	"github.com/zatiti/zatiti/internal/server"
)

// transportToken is the synthetic printable credential the fixture custodies
// as the local trusted helper and provisions to the owner, so the owner can
// authenticate over the socket. The bootstrap credential itself is 32 raw
// random bytes, which are not a valid HTTP header value.
const transportToken = "zat_synthetic_owner_transport_credential_0001"

// staticCredential hands out a private copy of the header value per call;
// internal/client zeroes the buffer it receives.
type staticCredential []byte

func (s staticCredential) Credential(context.Context) ([]byte, error) {
	return append([]byte(nil), s...), nil
}

// provisionTransportCredential custodies token in platform custody and
// provisions it to the owner through the real credential.provision.
func (f *fixture) provisionTransportCredential(token string) {
	f.t.Helper()
	ref, err := f.secrets.Put(context.Background(), "integration/owner-transport", []byte(token))
	if err != nil {
		f.t.Fatalf("custody transport credential: %v", err)
	}
	f.must(f.owner, "credential.provision", "fixture-transport-credential", map[string]any{
		"scope": f.scope(), "principal_id": f.owner.PrincipalID, "store_ref": ref,
	})
}

// serve starts the real internal/server on a private Unix socket in a short
// temp directory (Unix socket paths are length-limited) and returns the
// socket path.
func (f *fixture) serve() string {
	f.t.Helper()
	dir, err := os.MkdirTemp("", "zt")
	if err != nil {
		f.t.Fatalf("socket dir: %v", err)
	}
	sock := filepath.Join(dir, "c.sock")
	srv, err := server.New(server.Config{SocketPath: sock, MaxBodyBytes: 4 << 20}, f.app)
	if err != nil {
		_ = os.RemoveAll(dir)
		f.t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	f.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			f.t.Errorf("server did not stop")
		}
		_ = os.RemoveAll(dir)
	})
	return sock
}

func newClient(t testing.TB, sock string, creds contract.CredentialSource) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{SocketPath: sock, Timeout: 30 * time.Second}, creds)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c
}

// call is one scripted operation.
type call struct {
	name  string
	op    string
	key   string
	input any
}

// outcome is what one transport delivered for one call: the result envelope
// and the transport's own failure signal.
type outcome struct {
	envelope contract.Result
	code     string // fault code, "" when the call succeeded
	signal   string // transport-specific: CLI exit code, MCP isError, client error
}

// transport runs one scripted call against a served fixture.
type transport interface {
	name() string
	run(t *testing.T, c call) outcome
}

// applicationTransport is the in-process baseline: application.Invoke.
type applicationTransport struct{ f *fixture }

func (a applicationTransport) name() string { return "application" }

func (a applicationTransport) run(t *testing.T, c call) outcome {
	res, err := a.f.invoke(a.f.owner, c.op, c.key, c.input)
	return outcome{envelope: res, code: faultCode(err), signal: faultCode(err)}
}

// clientTransport is internal/client over internal/server's Unix socket.
type clientTransport struct{ op contract.Operator }

func (clientTransport) name() string { return "client" }

func (ct clientTransport) run(t *testing.T, c call) outcome {
	res, err := ct.op.Call(context.Background(), c.op, contract.Request{
		Schema: contract.SchemaRequest, SubmissionKey: c.key, Input: mustJSON(c.input),
	})
	out := outcome{envelope: res, code: faultCode(err)}
	if err != nil && out.code == "" {
		out.signal = "transport error: " + err.Error()
	} else {
		out.signal = out.code
	}
	return out
}

// cliTransport is internal/cli Execute in-process over the socket client.
type cliTransport struct {
	op    contract.Operator
	descs []contract.Descriptor
}

func (cliTransport) name() string { return "cli" }

func (ct cliTransport) run(t *testing.T, c call) outcome {
	var path []string
	for _, d := range ct.descs {
		if d.ID == c.op {
			path = d.CLI
		}
	}
	if path == nil {
		t.Fatalf("operation %s has no CLI mapping", c.op)
	}
	args := append(append([]string{}, path...), "--json", "--input", string(mustJSON(c.input)))
	if c.key != "" {
		args = append(args, "--submission-key", c.key)
	}
	var stdout, stderr bytes.Buffer
	exit := cli.Execute(context.Background(), args, ct.op, ct.descs, cli.IO{In: strings.NewReader(""), Out: &stdout, Err: &stderr})
	out := outcome{signal: "exit " + itoa(exit)}
	if stdout.Len() == 0 {
		out.signal += " without an envelope: " + strings.TrimSpace(stderr.String())
		return out
	}
	if err := json.Unmarshal(stdout.Bytes(), &out.envelope); err != nil {
		t.Fatalf("cli stdout is not one envelope: %v: %s", err, stdout.String())
	}
	if out.envelope.Error != nil {
		out.code = out.envelope.Error.Code
	}
	if want := contract.CLIExit(out.envelope.Error); exit != want {
		t.Errorf("%s: cli exit %d, the frozen mapping of %q is %d", c.name, exit, out.code, want)
	}
	return out
}

// mcpTransport is internal/mcp Serve over a pipe with a real go-sdk client,
// fronting the same socket client.
type mcpTransport struct {
	session *gosdk.ClientSession
	descs   []contract.Descriptor
}

func (mcpTransport) name() string { return "mcp" }

func newMCPTransport(t *testing.T, op contract.Operator, descs []contract.Descriptor) mcpTransport {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	var diagnostics lockedBuffer
	served := make(chan error, 1)
	go func() { served <- mcp.Serve(ctx, serverConn, serverConn, &diagnostics, op, descs) }()
	c := gosdk.NewClient(&gosdk.Implementation{Name: "integration-client", Version: "0.0.1"}, nil)
	session, err := c.Connect(ctx, &gosdk.IOTransport{Reader: clientConn, Writer: clientConn}, nil)
	if err != nil {
		cancel()
		select {
		case serveErr := <-served:
			t.Fatalf("mcp connect: %v (serve: %v; diagnostics: %s)", err, serveErr, diagnostics.String())
		case <-time.After(5 * time.Second):
			t.Fatalf("mcp connect: %v", err)
		}
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		_ = serverConn.Close()
		_ = clientConn.Close()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Errorf("mcp server did not stop")
		}
	})
	return mcpTransport{session: session, descs: descs}
}

func (mt mcpTransport) run(t *testing.T, c call) outcome {
	tool := ""
	for _, d := range mt.descs {
		if d.ID == c.op {
			tool = d.MCP
		}
	}
	if tool == "" {
		t.Fatalf("operation %s has no MCP mapping", c.op)
	}
	var input map[string]any
	if err := json.Unmarshal(mustJSON(c.input), &input); err != nil {
		t.Fatalf("mcp input: %v", err)
	}
	args := map[string]any{"input": input}
	if c.key != "" {
		args["submission_key"] = c.key
	}
	res, err := mt.session.CallTool(context.Background(), &gosdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return outcome{signal: "protocol error: " + err.Error()}
	}
	out := outcome{signal: "isError=" + boolText(res.IsError)}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil || res.StructuredContent == nil {
		out.signal += " without structured content"
		return out
	}
	if err := json.Unmarshal(structured, &out.envelope); err != nil {
		t.Fatalf("mcp structuredContent is not an envelope: %v: %s", err, structured)
	}
	if len(res.Content) == 1 {
		if text, ok := res.Content[0].(*gosdk.TextContent); ok {
			if canonical(t, []byte(text.Text)) != canonical(t, structured) {
				t.Errorf("%s: mcp text content is not equivalent to structuredContent", c.name)
			}
		}
	}
	if out.envelope.Error != nil {
		out.code = out.envelope.Error.Code
	}
	if res.IsError != (out.envelope.Status == contract.StatusFailed) {
		t.Errorf("%s: mcp isError=%t disagrees with envelope status %q", c.name, res.IsError, out.envelope.Status)
	}
	return out
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func itoa(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

var (
	uuidPattern      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	timestampPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$`)
)

// normalizer is the explicit mapping R12-004 allows: generated identities
// map to stable placeholders in first-seen order and timestamps to one
// placeholder. Nothing else is touched: versions, statuses, error codes,
// messages, permissions and cursors compare verbatim.
type normalizer struct {
	ids map[string]string
}

func newNormalizer() *normalizer { return &normalizer{ids: map[string]string{}} }

func (n *normalizer) value(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(x))
		for _, k := range keys {
			out[k] = n.value(x[k])
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = n.value(x[i])
		}
		return out
	case string:
		switch {
		case uuidPattern.MatchString(x):
			if _, ok := n.ids[x]; !ok {
				n.ids[x] = "id#" + itoa(len(n.ids)+1)
			}
			return n.ids[x]
		case timestampPattern.MatchString(x):
			return "timestamp"
		}
		return x
	default:
		return v
	}
}

// envelope renders one normalized envelope as canonical JSON.
func (n *normalizer) envelope(t *testing.T, res contract.Result) string {
	t.Helper()
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	out, err := json.Marshal(n.value(v))
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return string(out)
}

// canonical renders arbitrary JSON with sorted keys and exact numbers.
func canonical(t testing.TB, raw []byte) string {
	t.Helper()
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("canonical: %v: %s", err, raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	return string(out)
}

// errAs is errors.As for a contract fault.
func errAs(err error) *contract.Fault {
	var f *contract.Fault
	if errors.As(err, &f) {
		return f
	}
	return nil
}
