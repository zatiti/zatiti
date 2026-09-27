package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
	"github.com/zatiti/zatiti/internal/server"
)

const messageListInputSchema = `{"type":"object","additionalProperties":false,"properties":{"scope":{"type":"object"},"conversation_id":{"type":"string"},"limit":{"type":"integer"}},"required":["scope","conversation_id"]}`

// streamEnv is a bootstrapped application whose conversation.message.list
// admits only the principals in allowed, so tests can revoke access while a
// reply stream is open.
type streamEnv struct {
	*testEnv
	hub     *controller.ReplyHub
	srv     *server.Server
	client  *http.Client
	allowed atomic.Value // map[contract.ID]bool
}

func newStreamEnv(t *testing.T) *streamEnv {
	t.Helper()
	env := &streamEnv{testEnv: newBootstrappedEnv(t), hub: controller.NewReplyHub()}
	env.allowed.Store(map[contract.ID]bool{})
	env.cat.descriptors["conversation.message.list"] = contract.Descriptor{
		ID: "conversation.message.list", Version: 1, Owner: "messaging",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery,
		InputSchema: []byte(messageListInputSchema), ScopeRequired: []string{"installation_id"},
	}
	env.cat.handlers["conversation.message.list"] = func(_ context.Context, u contract.Unit, _ contract.Invocation) (contract.Payload, error) {
		if !env.allowed.Load().(map[contract.ID]bool)[u.Actor().PrincipalID] {
			return contract.Payload{}, &contract.Fault{Code: contract.CodePermissionDenied, Message: "not a participant"}
		}
		return contract.Payload{Status: contract.StatusCompleted, Data: []byte(`{"items":[]}`)}, nil
	}
	socketPath := shortSocketPath(t)
	srv, err := server.New(server.Config{SocketPath: socketPath, MaxBodyBytes: 1 << 20, Streams: env.hub}, env.app)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	env.srv = srv
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
	env.client = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}}
	return env
}

func (e *streamEnv) allow(ids ...contract.ID) {
	m := map[contract.ID]bool{}
	for _, id := range ids {
		m[id] = true
	}
	e.allowed.Store(m)
}

func (e *streamEnv) human(t *testing.T, token string) contract.Actor {
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	e.auth.registerCredential(token, actor)
	return actor
}

func (e *streamEnv) open(t *testing.T, ctx context.Context, token string, conversation contract.ID, submissionKey string) *http.Response {
	t.Helper()
	body := map[string]any{"schema": contract.SchemaRequest, "input": map[string]any{
		"scope": map[string]any{"installation_id": e.installation}, "conversation_id": conversation,
	}}
	if submissionKey != "" {
		body["submission_key"] = submissionKey
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/replies/stream", bytes.NewReader(mustMarshal(t, body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (e *streamEnv) route(recipient, conversation contract.ID) contract.ReplyRoute {
	return contract.ReplyRoute{
		Source: contract.NewID(), Scope: contract.Scope{InstallationID: e.installation}, Recipient: recipient,
		Conversation: conversation, Worker: contract.NewID(), Turn: contract.NewID(),
	}
}

// nextFrame reads SSE lines until one data frame arrives.
func nextFrame(t *testing.T, r *bufio.Reader) []contract.ReplyPreview {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading stream: %v", err)
		}
		if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data: "); ok {
			var out []contract.ReplyPreview
			if err := json.Unmarshal([]byte(data), &out); err != nil {
				t.Fatalf("frame %q: %v", data, err)
			}
			return out
		}
	}
}

func frameUntil(t *testing.T, r *bufio.Reader, ok func([]contract.ReplyPreview) bool) []contract.ReplyPreview {
	t.Helper()
	for {
		if f := nextFrame(t, r); ok(f) {
			return f
		}
	}
}

func decodeFaultEnvelope(t *testing.T, resp *http.Response) contract.Result {
	t.Helper()
	var result contract.Result
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding envelope: %v", err)
	}
	return result
}

func TestReplyStreamDeliversOnlyTheRequestersPreviews(t *testing.T) {
	e := newStreamEnv(t)
	alice, bob := e.human(t, "Bearer alice"), e.human(t, "Bearer bob")
	e.allow(alice.PrincipalID, bob.PrincipalID)
	conversation := contract.NewID()
	e.hub.Publish(e.route(bob.PrincipalID, conversation), "bob-stream", "For Bob only.", "streaming")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp := e.open(t, ctx, "Bearer alice", conversation, "")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d content-type %q, want an event stream", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	r := bufio.NewReader(resp.Body)
	if first := nextFrame(t, r); len(first) != 0 {
		t.Fatalf("initial snapshot = %+v, want none of Bob's previews", first)
	}
	e.hub.Publish(e.route(alice.PrincipalID, conversation), "alice-stream", "Hello Alice.", "streaming")
	got := frameUntil(t, r, func(f []contract.ReplyPreview) bool { return len(f) > 0 })
	if len(got) != 1 || got[0].ID != "alice-stream" || got[0].Text != "Hello Alice." {
		t.Fatalf("frame = %+v, want only Alice's preview", got)
	}
}

// A reconnect reads the current snapshot at once; nothing is replayed as a
// command and no earlier frame is needed.
func TestReplyStreamReconnectStartsFromTheCurrentSnapshot(t *testing.T) {
	e := newStreamEnv(t)
	alice := e.human(t, "Bearer alice")
	e.allow(alice.PrincipalID)
	conversation := contract.NewID()
	route := e.route(alice.PrincipalID, conversation)
	e.hub.Publish(route, "s1", "First. Second", "streaming")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	resp := e.open(t, ctx, "Bearer alice", conversation, "")
	_ = nextFrame(t, bufio.NewReader(resp.Body))
	cancel()

	e.hub.Publish(route, "s1", "First. Second. Third", "streaming")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	got := nextFrame(t, bufio.NewReader(e.open(t, ctx2, "Bearer alice", conversation, "").Body))
	if len(got) != 1 || got[0].Text != "First. Second. Third" || len(got[0].Phrases) != 2 {
		t.Fatalf("reconnect frame = %+v, want the current preview with its stable phrases", got)
	}
}

func TestReplyStreamClosesWhenAccessIsRevoked(t *testing.T) {
	e := newStreamEnv(t)
	alice := e.human(t, "Bearer alice")
	e.allow(alice.PrincipalID)
	conversation := contract.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := bufio.NewReader(e.open(t, ctx, "Bearer alice", conversation, "").Body)
	_ = nextFrame(t, r)

	e.allow()
	e.hub.Publish(e.route(alice.PrincipalID, conversation), "s1", "Too late.", "streaming")
	rest, err := io.ReadAll(r)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reading after revocation: %v", err)
	}
	if strings.Contains(string(rest), "Too late.") {
		t.Fatal("a preview was disclosed after the reader lost conversation access")
	}
}

func TestReplyStreamRefusesUnauthorizedAndMalformedRequests(t *testing.T) {
	e := newStreamEnv(t)
	e.human(t, "Bearer alice")
	service := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	e.auth.registerCredential("Bearer service", service)
	e.allow(service.PrincipalID)
	conversation := contract.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for name, c := range map[string]struct {
		token, key, code string
	}{
		"not a participant": {"Bearer alice", "", contract.CodePermissionDenied},
		"service principal": {"Bearer service", "", contract.CodePermissionDenied},
		"submission key":    {"Bearer alice", "k1", contract.CodeInvalidInput},
	} {
		resp := e.open(t, ctx, c.token, conversation, c.key)
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("%s: opened an event stream", name)
		}
		if result := decodeFaultEnvelope(t, resp); result.Error == nil || result.Error.Code != c.code {
			t.Fatalf("%s: result = %+v, want %s", name, result, c.code)
		}
	}
}

// An open reply stream never goes idle, so graceful shutdown must end it
// rather than wait for it: Close returns promptly and the stream closes.
func TestOpenReplyStreamDoesNotHoldShutdown(t *testing.T) {
	env := newStreamEnv(t)
	human := env.human(t, "tok-shutdown")
	conversation := contract.NewID()
	env.allow(human.PrincipalID)
	resp := env.open(t, context.Background(), "tok-shutdown", conversation, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", resp.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := env.srv.Close(ctx); err != nil {
		t.Fatalf("Close with an open stream: %v after %s", err, time.Since(start))
	}
	ended := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, resp.Body)
		ended <- err
	}()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("reply stream stayed open after Close")
	}
}
