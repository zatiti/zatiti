package server_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/server"
)

// --- bootstrap cannot reopen after initialization ---

// TestBootstrapCannotReopenAfterInitialization proves the local bootstrap
// route stays one-time through the actual HTTP transport: the first
// installation.init call over the private socket succeeds, and a second
// call -- the exact request replayed, exercising the same route a restart
// or a confused client would reach -- is refused with the named conflict
// fault installation owns for this case (internal/installation/bootstrap.go:
// "this installation is already initialized; bootstrap runs exactly once"),
// never a second installation and never a silent 200. This is revision 3's
// "bootstrap cannot reopen after initialization" admission boundary
// (internal/server/AGENTS.md, P41 card item 2), proven at the transport
// server package owns rather than assumed from installation's own tests.
func TestBootstrapCannotReopenAfterInitialization(t *testing.T) {
	env := newTestEnvWithIO(t, &statefulInstallationIO{})
	_, client := newLocalServer(t, env)

	resp, result := postOperation(t, client, "http://unix", "installation.init", "", installInitBody(t))
	if resp.StatusCode != http.StatusOK || result.Status != contract.StatusCompleted {
		t.Fatalf("first bootstrap status = %d result = %+v, want a completed 200", resp.StatusCode, result)
	}

	resp, result = postOperation(t, client, "http://unix", "installation.init", "", installInitBody(t))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second bootstrap status = %d, want 409; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodeConflict {
		t.Fatalf("second bootstrap result = %+v, want a named conflict fault", result)
	}
	if !strings.Contains(result.Error.Message, "already initialized") {
		t.Fatalf("fault message = %q, want it to name the reopened-bootstrap refusal", result.Error.Message)
	}
	if got := env.db.countEvents("installation.initialized"); got != 1 {
		t.Fatalf("installation.initialized committed %d times, want exactly 1", got)
	}
}

// --- quiesce/restore admission: queries scoped, mutations refuse ---

// TestRestoringInstallationRefusesMutationsButQueriesRemainScoped proves the
// server delivers, unmodified, whatever admission decision the owning
// domain reaches for a restoring/paused installation (internal/server/
// AGENTS.md restore protocol step 1, "close new admissions"; P41 card item
// 2's "queries remain intentionally scoped, prohibited mutations refuse").
// Admission-state enforcement itself belongs to the owning domain modules
// (internal/installation, internal/policy), not this transport package, so
// this test stands in for that decision with two independent forbidden
// mutations, each refusing with ITS OWN named fault -- proving the server
// never collapses distinct admission refusals into one generic code -- while
// an ordinary scoped query keeps working through the same transport in the
// same run.
func TestRestoringInstallationRefusesMutationsButQueriesRemainScoped(t *testing.T) {
	env := newBootstrappedEnv(t)
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	env.auth.registerCredential("Bearer good-token", actor)

	env.cat.descriptors["task.list"] = contract.Descriptor{
		ID: "task.list", Version: 1, Owner: "tasks",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery,
		InputSchema: []byte(pingInputSchema),
	}
	env.cat.handlers["task.list"] = func(context.Context, contract.Unit, contract.Invocation) (contract.Payload, error) {
		return contract.Payload{Status: contract.StatusCompleted, Data: []byte(`{"items":[]}`)}, nil
	}

	evidence := newFakeEvidence()
	evidence.registerOn(env.cat)

	env.cat.descriptors["task.start"] = contract.Descriptor{
		ID: "task.start", Version: 1, Owner: "tasks",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation,
		InputSchema: []byte(pingInputSchema), SubmissionKey: true,
	}
	env.cat.handlers["task.start"] = func(context.Context, contract.Unit, contract.Invocation) (contract.Payload, error) {
		return contract.Payload{}, &contract.Fault{
			Code:    contract.CodePrerequisiteMissing,
			Message: "installation is restoring; task admission is refused until resume",
		}
	}

	env.cat.descriptors["connection.rotate"] = contract.Descriptor{
		ID: "connection.rotate", Version: 1, Owner: "connections",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation,
		InputSchema: []byte(pingInputSchema), SubmissionKey: true,
	}
	env.cat.handlers["connection.rotate"] = func(context.Context, contract.Unit, contract.Invocation) (contract.Payload, error) {
		return contract.Payload{}, &contract.Fault{
			Code:    contract.CodeControllerUnavailable,
			Message: "installation is quiescing for maintenance; new admissions are refused",
		}
	}

	_, client := newLocalServer(t, env)

	// Queries remain intentionally scoped: still served normally while the
	// installation is (simulated) restoring.
	resp, result := postOperation(t, client, "http://unix", "task.list", "Bearer good-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{}}))
	if resp.StatusCode != http.StatusOK || result.Status != contract.StatusCompleted {
		t.Fatalf("task.list status = %d result = %+v, want a completed 200 query", resp.StatusCode, result)
	}

	// A prohibited mutation refuses with its own named fault.
	resp, result = postOperation(t, client, "http://unix", "task.start", "Bearer good-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "submission_key": "task-start-1", "input": map[string]any{}}))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("task.start status = %d, want 422; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("task.start result = %+v, want a named prerequisite_missing fault", result)
	}

	// A second, independent prohibited mutation refuses with a DIFFERENT
	// named fault: admission refusal is not one generic code standing in
	// for every forbidden operation.
	resp, result = postOperation(t, client, "http://unix", "connection.rotate", "Bearer good-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "submission_key": "connection-rotate-1", "input": map[string]any{}}))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("connection.rotate status = %d, want 503; result = %+v", resp.StatusCode, result)
	}
	if result.Error == nil || result.Error.Code != contract.CodeControllerUnavailable {
		t.Fatalf("connection.rotate result = %+v, want a named controller_unavailable fault", result)
	}
}

// --- remote mTLS principal mapping cannot be spoofed ---

// TestRemoteCannotSpoofPrincipalKindThroughBearerCredential proves the
// remote listener's identity comes exclusively from the verified
// certificate: handler.go's authenticate returns the certificate-derived
// actor unconditionally and uses a co-presented bearer credential only as a
// principal-match sanity check, never as a source of identity. This test
// registers a bearer credential for the SAME principal ID the certificate
// maps to, but asserting KindHuman where the certificate itself maps that
// principal to KindWorker -- modeling an attempt to ride a "local human
// actor" identity in on the remote transport. The dispatched actor's kind
// must be the certificate's, never the bearer's.
func TestRemoteCannotSpoofPrincipalKindThroughBearerCredential(t *testing.T) {
	env := newBootstrappedEnv(t)
	ca := newTestCA(t)
	serverCert, _ := ca.leaf(t, "zatiti-server-test", true)
	clientCert, clientLeaf := ca.leaf(t, "zatiti-client-test", false)

	principal := contract.NewID()
	env.auth.registerCertificate(fingerprintOf(clientLeaf), contract.Actor{PrincipalID: principal, Kind: contract.KindWorker})
	env.auth.registerCredential("Bearer spoof-token", contract.Actor{PrincipalID: principal, Kind: contract.KindHuman})

	_, client := newRemoteServer(t, env, ca, serverCert, clientCert)

	resp, result := postOperation(t, client, "https://zatiti-remote-test", "capabilities.list", "Bearer spoof-token",
		mustMarshal(t, map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{}}))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; result = %+v", resp.StatusCode, result)
	}
	if !strings.Contains(string(result.Data), `"seen_kind":"worker"`) {
		t.Fatalf("data = %s, want the certificate-mapped kind %q", result.Data, contract.KindWorker)
	}
	if strings.Contains(string(result.Data), `"seen_kind":"human"`) {
		t.Fatalf("data = %s, the remote transport let a bearer credential spoof a local-human actor kind", result.Data)
	}
}

// --- durable task submission survives disconnect, never repeats ---

// TestDisconnectAfterTaskSubmissionNeitherCancelsNorRepeatsDurableWork is the
// P41 card's required "disconnect after task submission does not cancel
// durable work or repeat it" behavior, generalized beyond
// TestDisconnectAfterCommitStillCommits's bootstrap-only LocalIO path to an
// ordinary durable mutation (submission-key required, real commandBegin/
// handler/commandFinish lifecycle -- the same path task.start's real
// production handler runs through). The client submits, and the raw
// connection is torn down while the handler is deliberately still running,
// well before its durable write commits; the handler itself asserts its own
// context is never cancelled despite the disconnect (proving the dispatch
// context server.go detaches with context.WithoutCancel is never mistaken
// for the request's lifetime, so a server request context is never kept
// alive as -- or torn down as if it were -- a task's own lifetime). The test
// then reconnects and resubmits the identical request: the durable work
// must not run a second time, only replay its original recorded
// disposition.
func TestDisconnectAfterTaskSubmissionNeitherCancelsNorRepeatsDurableWork(t *testing.T) {
	env := newBootstrappedEnv(t)
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	env.auth.registerCredential("Bearer good-token", actor)

	evidence := newFakeEvidence()
	evidence.registerOn(env.cat)

	var mu sync.Mutex
	invocations := 0
	started := make(chan struct{})
	finished := make(chan struct{})

	env.cat.descriptors["task.start"] = contract.Descriptor{
		ID: "task.start", Version: 1, Owner: "tasks",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation,
		InputSchema: []byte(pingInputSchema), SubmissionKey: true,
	}
	env.cat.handlers["task.start"] = func(ctx context.Context, u contract.Unit, _ contract.Invocation) (contract.Payload, error) {
		mu.Lock()
		invocations++
		mu.Unlock()
		close(started)
		// Give the test a deterministic window to disconnect the client
		// out from under this in-flight request before the durable write
		// that admits the task ever commits.
		time.Sleep(200 * time.Millisecond)
		if err := ctx.Err(); err != nil {
			t.Errorf("task.start handler observed a cancelled context after the client disconnected: %v", err)
		}
		if err := u.Emit(ctx, contract.Event{
			ID: contract.NewID(), At: time.Now(), Scope: u.Scope(),
			Kind: "task.started", ResourceID: contract.NewID(), ResourceVersion: 1,
			Data: []byte(`{}`),
		}); err != nil {
			return contract.Payload{}, err
		}
		close(finished)
		return contract.Payload{Status: contract.StatusAccepted, Data: []byte(`{"job":{"id":"job-1","state":"pending"}}`)}, nil
	}

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

	body := mustMarshal(t, map[string]any{
		"schema": contract.SchemaRequest, "submission_key": "task-start-key-1", "input": map[string]any{},
	})
	req, err := http.NewRequest(http.MethodPost, "http://unix/v1/operations/task.start", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer good-token")

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dialing socket: %v", err)
	}
	if err := req.Write(conn); err != nil {
		t.Fatalf("writing request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("task.start handler never started")
	}
	// Disconnect while the handler is still inside its deliberate delay,
	// well before the durable write commits.
	if err := conn.Close(); err != nil {
		t.Fatalf("closing connection: %v", err)
	}

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("task.start never committed despite the client disconnecting")
	}

	if got := env.db.countEvents("task.started"); got != 1 {
		t.Fatalf("task.started committed %d times, want exactly 1 despite the disconnect", got)
	}
	mu.Lock()
	gotInvocations := invocations
	mu.Unlock()
	if gotInvocations != 1 {
		t.Fatalf("task.start handler ran %d times, want exactly 1", gotInvocations)
	}

	// Reconnect and resubmit the identical request: a client recovering
	// from a lost response must see the original durable disposition
	// replayed, never a second execution of the durable work.
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}
	resp, result := postOperation(t, client, "http://unix", "task.start", "Bearer good-token", body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("replayed task.start status = %d, want 202; result = %+v", resp.StatusCode, result)
	}
	if result.Status != contract.StatusAccepted {
		t.Fatalf("replayed task.start payload status = %q, want accepted", result.Status)
	}
	if !strings.Contains(string(result.Data), "job-1") {
		t.Fatalf("replayed task.start data = %s, want the original recorded job", result.Data)
	}

	mu.Lock()
	gotInvocations = invocations
	mu.Unlock()
	if gotInvocations != 1 {
		t.Fatalf("task.start handler ran %d times after a replayed resubmission, want it to stay at 1", gotInvocations)
	}
	if got := env.db.countEvents("task.started"); got != 1 {
		t.Fatalf("task.started committed %d times after a replayed resubmission, want it to stay at 1", got)
	}
}
