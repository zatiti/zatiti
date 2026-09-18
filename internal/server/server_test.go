package server_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/server"
)

// newLocalServer starts a Server on a temporary Unix socket and returns it
// alongside a plain HTTP client dialing that socket and the socket path.
// Callers must arrange the server's shutdown (t.Cleanup does it here).
func newLocalServer(t *testing.T, app *testEnv) (*server.Server, *http.Client) {
	t.Helper()
	socketPath := shortSocketPath(t)
	srv, err := server.New(server.Config{
		SocketPath:   socketPath,
		MaxBodyBytes: 1 << 20,
	}, app.app)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("server did not stop within 5s of cancellation")
		}
	})

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}
	return srv, client
}

// postOperation issues one POST /v1/operations/{operation} call and decodes
// the envelope, regardless of whether it reports success or failure.
func postOperation(t *testing.T, client *http.Client, base, operation, credential string, body []byte) (*http.Response, contract.Result) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/operations/"+operation, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if credential != "" {
		req.Header.Set("Authorization", credential)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("performing request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var result contract.Result
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response envelope: %v", err)
	}
	return resp, result
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func installInitBody(t *testing.T) []byte {
	return mustMarshal(t, map[string]any{
		"schema": contract.SchemaRequest,
		"input":  map[string]any{"credential_store": "os", "owner_name": "test-owner"},
	})
}

// --- bootstrap ---

func TestLocalBootstrapSucceeds(t *testing.T) {
	env := newTestEnv(t)
	_, client := newLocalServer(t, env)

	resp, result := postOperation(t, client, "http://unix", "installation.init", "", installInitBody(t))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; result = %+v", resp.StatusCode, result)
	}
	if result.Status != contract.StatusCompleted {
		t.Fatalf("payload status = %q, want completed", result.Status)
	}
	if result.Schema != contract.SchemaResult {
		t.Fatalf("schema = %q, want %q", result.Schema, contract.SchemaResult)
	}
	if result.CommandID == "" {
		t.Fatal("result carries no command_id")
	}
	if !env.db.hasEvent("installation.initialized") {
		t.Fatal("bootstrap did not commit an installation.initialized event")
	}
}

func TestRemoteCannotBootstrap(t *testing.T) {
	env := newTestEnv(t)
	ca := newTestCA(t)
	serverCert, _ := ca.leaf(t, "zatiti-server-test", true)
	clientCert, clientLeaf := ca.leaf(t, "zatiti-client-test", false)
	env.auth.registerCertificate(fingerprintOf(clientLeaf), contract.Actor{
		PrincipalID: contract.NewID(), Kind: contract.KindHuman,
	})

	_, client := newRemoteServer(t, env, ca, serverCert, clientCert)

	resp, result := postOperation(t, client, "https://zatiti-remote-test", "installation.init", "", installInitBody(t))

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; result = %+v", resp.StatusCode, result)
	}
	if result.Status != contract.StatusFailed || result.Error == nil {
		t.Fatalf("result = %+v, want a failed envelope with a fault", result)
	}
	if result.Error.Code != contract.CodePermissionDenied {
		t.Fatalf("fault code = %q, want %q", result.Error.Code, contract.CodePermissionDenied)
	}
	if env.db.hasEvent("installation.initialized") {
		t.Fatal("remote bootstrap attempt must not initialize the installation")
	}
}

// --- ordinary local authenticated dispatch ---

func TestLocalAuthenticatedQuerySucceeds(t *testing.T) {
	env := newBootstrappedEnv(t)
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	env.auth.registerCredential("Bearer good-token", actor)
	_, client := newLocalServer(t, env)

	resp, result := postOperation(t, client, "http://unix", "capabilities.list", "Bearer good-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{}}))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; result = %+v", resp.StatusCode, result)
	}
	if result.Status != contract.StatusCompleted {
		t.Fatalf("payload status = %q, want completed", result.Status)
	}
	if !strings.Contains(string(result.Data), string(actor.PrincipalID)) {
		t.Fatalf("data = %s, want it to reflect the authenticated principal %s", result.Data, actor.PrincipalID)
	}
}

func TestLocalUnknownCredentialIsDenied(t *testing.T) {
	env := newBootstrappedEnv(t)
	_, client := newLocalServer(t, env)

	resp, result := postOperation(t, client, "http://unix", "capabilities.list", "Bearer unknown-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{}}))

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodePermissionDenied {
		t.Fatalf("result = %+v, want permission_denied", result)
	}
}

func TestUnknownOperationIsNotFound(t *testing.T) {
	env := newBootstrappedEnv(t)
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	env.auth.registerCredential("Bearer good-token", actor)
	_, client := newLocalServer(t, env)

	resp, result := postOperation(t, client, "http://unix", "widget.teleport", "Bearer good-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{}}))

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodeNotFound {
		t.Fatalf("result = %+v, want not_found", result)
	}
}

// --- key recovery: recovering a durable outcome by submission key ---

// TestCommandGetRecoversByKey proves the transport serves the recovery
// lookup a disconnected client uses (internal/client's resolveByLookup) the
// same way as any other authenticated query, with the frozen command.get
// input (scope, submission_key, operation, operation_version): a key bound
// to a previously recorded disposition returns that disposition, an unbound
// key is a plain not_found rather than a different shape of failure, and the
// key alone is refused invalid_input.
func TestCommandGetRecoversByKey(t *testing.T) {
	env := newBootstrappedEnv(t)
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	env.auth.registerCredential("Bearer good-token", actor)
	recorded := contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: contract.NewID(),
		Payload:   contract.Payload{Status: contract.StatusCompleted, Data: []byte(`{"resource":{"widget":"already-created"}}`)},
	}
	env.cat.commands.record("widget-create-key-1", recorded)
	_, client := newLocalServer(t, env)
	lookup := func(key string) map[string]any {
		return map[string]any{
			"scope":             map[string]any{"installation_id": env.installation},
			"submission_key":    key,
			"operation":         "widget.create",
			"operation_version": 1,
		}
	}

	resp, result := postOperation(t, client, "http://unix", "command.get", "Bearer good-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": lookup("widget-create-key-1")}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; result = %+v", resp.StatusCode, result)
	}
	if !strings.Contains(string(result.Data), "already-created") {
		t.Fatalf("data = %s, want it to carry the recorded disposition", result.Data)
	}

	resp, result = postOperation(t, client, "http://unix", "command.get", "Bearer good-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": lookup("never-submitted")}))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodeNotFound {
		t.Fatalf("result = %+v, want not_found", result)
	}

	resp, result = postOperation(t, client, "http://unix", "command.get", "Bearer good-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{"submission_key": "widget-create-key-1"}}))
	if resp.StatusCode != http.StatusBadRequest || result.Error == nil || result.Error.Code != contract.CodeInvalidInput {
		t.Fatalf("status = %d result = %+v, want 400 invalid_input for a key-only lookup", resp.StatusCode, result)
	}
}

// --- protocol-level rejection: malformed/oversized body, content type ---

func TestMalformedBodyIsInvalidInput(t *testing.T) {
	env := newBootstrappedEnv(t)
	_, client := newLocalServer(t, env)

	req, err := http.NewRequest(http.MethodPost, "http://unix/v1/operations/capabilities.list", bytes.NewReader([]byte(`{not json`)))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("performing request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var result contract.Result
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response envelope: %v", err)
	}

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodeInvalidInput {
		t.Fatalf("result = %+v, want invalid_input", result)
	}
	if result.CommandID == "" {
		t.Fatal("even a protocol-level failure must carry a command_id")
	}
}

// TestProfileSpoofIsRejected proves a client cannot smuggle an actor
// identity through the request envelope itself: the envelope decodes with
// zatiti wire strictness, so an unrecognized top-level field (here, a
// self-asserted "actor") is a decode failure, not data the server might
// otherwise be tempted to trust over the Authorization credential.
func TestProfileSpoofIsRejected(t *testing.T) {
	env := newBootstrappedEnv(t)
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	env.auth.registerCredential("Bearer good-token", actor)
	_, client := newLocalServer(t, env)

	spoofed := mustMarshal(t, map[string]any{
		"schema": contract.SchemaRequest,
		"input":  map[string]any{},
		"actor":  map[string]any{"principal_id": "attacker-controlled", "kind": "human"},
	})
	resp, result := postOperation(t, client, "http://unix", "capabilities.list", "Bearer good-token", spoofed)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodeInvalidInput {
		t.Fatalf("result = %+v, want invalid_input", result)
	}
}

func TestOversizedBodyIsInvalidInput(t *testing.T) {
	env := newBootstrappedEnv(t)
	socketPath := shortSocketPath(t)
	srv, err := server.New(server.Config{SocketPath: socketPath, MaxBodyBytes: 64}, env.app)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("server did not stop")
		}
	})
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}, Timeout: 5 * time.Second}

	oversized := mustMarshal(t, map[string]any{
		"schema": contract.SchemaRequest,
		"input":  map[string]any{"padding": strings.Repeat("x", 200)},
	})
	resp, result := postOperation(t, client, "http://unix", "capabilities.list", "", oversized)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodeInvalidInput {
		t.Fatalf("result = %+v, want invalid_input", result)
	}
}

func TestWrongContentTypeIsInvalidInput(t *testing.T) {
	env := newBootstrappedEnv(t)
	_, client := newLocalServer(t, env)

	req, err := http.NewRequest(http.MethodPost, "http://unix/v1/operations/capabilities.list",
		bytes.NewReader(mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{}})))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("performing request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var result contract.Result
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response envelope: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodeInvalidInput {
		t.Fatalf("result = %+v, want invalid_input", result)
	}
}

// --- Unix socket permissions ---

func TestUnixSocketIsOwnerOnly(t *testing.T) {
	env := newTestEnv(t)
	socketPath := shortSocketPath(t)
	srv, err := server.New(server.Config{SocketPath: socketPath, MaxBodyBytes: 1 << 20}, env.app)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })

	fi, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket permissions = %o, want 0600", got)
	}
}

// --- remote mutual TLS ---

// newRemoteServer starts a Server with a remote TLS listener bound to an
// ephemeral loopback port and returns it alongside an HTTPS client
// presenting clientCert and trusting the test CA.
func newRemoteServer(t *testing.T, env *testEnv, ca *testCA, serverCert, clientCert tls.Certificate) (*server.Server, *http.Client) {
	t.Helper()
	socketPath := shortSocketPath(t)
	addr := freeLoopbackAddr(t)
	srv, err := server.New(server.Config{
		SocketPath:    socketPath,
		RemoteAddress: addr,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientCAs:    ca.pool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
		},
		MaxBodyBytes: 1 << 20,
	}, env.app)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("server did not stop")
		}
	})

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
			TLSClientConfig: &tls.Config{
				RootCAs:      ca.pool,
				Certificates: []tls.Certificate{clientCert},
				ServerName:   "localhost",
			},
		},
		Timeout: 5 * time.Second,
	}
	return srv, client
}

// freeLoopbackAddr reserves an ephemeral loopback port by binding and
// immediately releasing it, so the returned address is very likely free for
// the server under test to bind next.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving loopback port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("releasing loopback port: %v", err)
	}
	return addr
}

func TestRemoteCertificatePrincipalSucceeds(t *testing.T) {
	env := newBootstrappedEnv(t)
	ca := newTestCA(t)
	serverCert, _ := ca.leaf(t, "zatiti-server-test", true)
	clientCert, clientLeaf := ca.leaf(t, "zatiti-client-test", false)
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	env.auth.registerCertificate(fingerprintOf(clientLeaf), actor)

	_, client := newRemoteServer(t, env, ca, serverCert, clientCert)

	resp, result := postOperation(t, client, "https://zatiti-remote-test", "capabilities.list", "",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{}}))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; result = %+v", resp.StatusCode, result)
	}
	if !strings.Contains(string(result.Data), string(actor.PrincipalID)) {
		t.Fatalf("data = %s, want it to reflect the certificate-mapped principal %s", result.Data, actor.PrincipalID)
	}
}

func TestRemoteUnrecognizedCertificateIsDenied(t *testing.T) {
	env := newBootstrappedEnv(t)
	ca := newTestCA(t)
	serverCert, _ := ca.leaf(t, "zatiti-server-test", true)
	clientCert, _ := ca.leaf(t, "zatiti-unregistered-client", false)
	// Deliberately never registered: models a revoked or unknown certificate.

	_, client := newRemoteServer(t, env, ca, serverCert, clientCert)

	resp, result := postOperation(t, client, "https://zatiti-remote-test", "capabilities.list", "",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{}}))

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodePermissionDenied {
		t.Fatalf("result = %+v, want permission_denied", result)
	}
}

func TestRemoteMismatchedBearerPrincipalIsDenied(t *testing.T) {
	env := newBootstrappedEnv(t)
	ca := newTestCA(t)
	serverCert, _ := ca.leaf(t, "zatiti-server-test", true)
	clientCert, clientLeaf := ca.leaf(t, "zatiti-client-test", false)
	certActor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	env.auth.registerCertificate(fingerprintOf(clientLeaf), certActor)
	otherActor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	env.auth.registerCredential("Bearer other-token", otherActor)

	_, client := newRemoteServer(t, env, ca, serverCert, clientCert)

	resp, result := postOperation(t, client, "https://zatiti-remote-test", "capabilities.list", "Bearer other-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{}}))

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodePermissionDenied {
		t.Fatalf("result = %+v, want permission_denied", result)
	}
}

// --- token redaction ---

func TestCredentialIsNeverLogged(t *testing.T) {
	env := newBootstrappedEnv(t)
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	const secret = "Bearer super-secret-token-should-never-appear-in-logs"
	env.auth.registerCredential(secret, actor)
	_, client := newLocalServer(t, env)

	var logBuf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	resp, result := postOperation(t, client, "http://unix", "capabilities.list", secret,
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{}}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; result = %+v", resp.StatusCode, result)
	}

	if strings.Contains(logBuf.String(), secret) {
		t.Fatalf("log output contains the raw credential: %s", logBuf.String())
	}
	if strings.Contains(logBuf.String(), "super-secret-token") {
		t.Fatalf("log output contains a fragment of the raw credential: %s", logBuf.String())
	}
}

// --- disconnect after commit ---

// TestDisconnectAfterCommitStillCommits proves that a client disconnecting
// mid-request never causes the server to treat an already-dispatched
// mutation as rolled back. The request is written directly to the socket
// and the connection is torn down immediately afterward, before the
// deliberately delayed local IO phase finishes; the test then waits, via a
// channel rather than a sleep, for the mutation to durably commit despite
// nobody being left to read the response.
func TestDisconnectAfterCommitStillCommits(t *testing.T) {
	finished := make(chan struct{})
	env := newTestEnvWithIO(t, delayedInstallationIO{delay: 200 * time.Millisecond, finished: finished})
	socketPath := shortSocketPath(t)
	srv, err := server.New(server.Config{SocketPath: socketPath, MaxBodyBytes: 1 << 20}, env.app)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("server did not stop")
		}
	})

	body := installInitBody(t)
	req, err := http.NewRequest(http.MethodPost, "http://unix/v1/operations/installation.init", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dialing socket: %v", err)
	}
	if err := req.Write(conn); err != nil {
		t.Fatalf("writing request: %v", err)
	}
	// Disconnect immediately: the server has the full request, but its
	// delayed local IO phase has not run yet.
	if err := conn.Close(); err != nil {
		t.Fatalf("closing connection: %v", err)
	}

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("bootstrap's local IO Finish phase never ran")
	}

	if !env.db.hasEvent("installation.initialized") {
		t.Fatal("mutation did not commit despite the client disconnecting mid-request")
	}
}

// --- Close/Serve lifecycle sanity ---

func TestCloseIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	socketPath := shortSocketPath(t)
	srv, err := server.New(server.Config{SocketPath: socketPath, MaxBodyBytes: 1 << 20}, env.app)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ctx) }()

	if err := srv.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := srv.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket file still exists after Close: err = %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct {
		name string
		cfg  server.Config
	}{
		{"missing socket path", server.Config{MaxBodyBytes: 1024}},
		{"non-positive max body", server.Config{SocketPath: shortSocketPath(t), MaxBodyBytes: 0}},
		{"remote without tls config", server.Config{
			SocketPath: shortSocketPath(t), MaxBodyBytes: 1024, RemoteAddress: "127.0.0.1:0",
		}},
		{"remote without client auth required", server.Config{
			SocketPath: shortSocketPath(t), MaxBodyBytes: 1024, RemoteAddress: "127.0.0.1:0",
			TLSConfig: &tls.Config{ClientAuth: tls.NoClientCert},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := server.New(tc.cfg, env.app); err == nil {
				t.Fatal("New succeeded, want a validation error")
			}
		})
	}
}

func TestNewRequiresApplication(t *testing.T) {
	cfg := server.Config{SocketPath: shortSocketPath(t), MaxBodyBytes: 1024}
	if _, err := server.New(cfg, nil); err == nil {
		t.Fatal("New succeeded with a nil application, want an error")
	}
}
