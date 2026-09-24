package mcpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestReviewSessionIDMustNotBeStaged(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	blobs := newFakeBlobStore()
	a := newTestAdapter(t, blobs, nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none"))
	handle, _ := openSession(t, a, 10*time.Second)
	_, err := a.Invoke(t.Context(), testDispatch(t, wireListTools{Schema: "zatiti.mcp.action/v1", Kind: kindListTools, SessionHandle: handle}, 10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range blobs.stagedDocs() {
		if bytes.Contains(bytes.ToLower(doc), []byte("mcp-session-id")) {
			t.Errorf("raw MCP session header staged: %s", doc)
		}
	}
}

func TestReviewCredentialEchoMustNotReachEvidence(t *testing.T) {
	srv := alwaysRedirectServer("https://example.invalid/" + testToken)
	defer srv.Close()
	blobs := newFakeBlobStore()
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), buildProfileJSON(t, srv.URL, true, []string{"echo"}, []string{"public"}, "bearer"))
	_, obs := openSession(t, a, 10*time.Second)
	if bytes.Contains(obs.Evidence, []byte(testToken)) {
		t.Errorf("credential leaked into observation evidence")
	}
}

func TestAttemptUsageAndRestrictedRequest(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	blobs := newFakeBlobStore()
	a := newTestAdapter(t, blobs, nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"restricted"}, "none")).(*Adapter)
	a.profile.ToolCallCost = wireMoney{Currency: "EUR", MicroUnits: 123}
	handle, opened := openSession(t, a, 10*time.Second)
	if u := decodeEvidence(t, opened).Usage; u.Billing != "bounded_estimate" || u.Accounting.Currency != "EUR" || u.Accounting.Estimated != 123 {
		t.Errorf("open usage=%+v", u)
	}
	schema, digest := inputSchemaFor(t, nil)
	obs, err := a.Invoke(t.Context(), testDispatch(t, wireCallTool{Schema: "zatiti.mcp.action/v1", Kind: kindCallTool, SessionHandle: handle, Tool: "echo", Arguments: json.RawMessage(`{"text":"restricted input"}`), InputSchema: schema, InputSchemaDigest: digest, Classification: "restricted"}, 10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("observation=%+v", obs)
	}
	ev := decodeEvidence(t, obs)
	if ev.Usage.Billing != "bounded_estimate" || ev.Usage.Accounting.Estimated != 123 {
		t.Errorf("call usage=%+v", ev.Usage)
	}
	for _, output := range ev.StagedOutputs {
		if output.Purpose == "context" && output.Classification != "restricted" {
			t.Errorf("restricted arguments staged as %s", output.Classification)
		}
	}
}

func echoAction(t *testing.T, handle string) wireCallTool {
	schema, digest := inputSchemaFor(t, nil)
	return wireCallTool{Schema: "zatiti.mcp.action/v1", Kind: kindCallTool, SessionHandle: handle, Tool: "echo", Arguments: json.RawMessage(`{"text":"hello"}`), InputSchema: schema, InputSchemaDigest: digest, Classification: "public"}
}

func TestConcurrentSessionOperationCannotReplaceEvidence(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	entered, release := make(chan struct{}), make(chan struct{})
	srv.onRequest = func(w http.ResponseWriter, r *http.Request, body []byte, method string) bool {
		if method == "tools/call" {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		return false
	}
	a := newTestAdapter(t, newFakeBlobStore(), nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none"))
	handle, _ := openSession(t, a, 10*time.Second)
	first := testDispatch(t, echoAction(t, handle), 10*time.Second)
	type result struct {
		obs contract.Observation
		err error
	}
	done := make(chan result, 1)
	go func() { obs, err := a.Invoke(t.Context(), first); done <- result{obs, err} }()
	<-entered
	_, err := a.Invoke(t.Context(), testDispatch(t, wireListTools{Schema: "zatiti.mcp.action/v1", Kind: kindListTools, SessionHandle: handle}, time.Second))
	close(release)
	if err == nil || mustFault(t, err).Code != contract.CodePrerequisiteMissing {
		t.Errorf("overlapping session operation was not refused: %v", err)
	}
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	ev := decodeEvidence(t, r.obs)
	if ev.PhysicalCall.AttemptID != first.AttemptID || ev.PhysicalCall.RequestContext.StagingRef == "" || r.obs.Disposition != contract.DispositionSucceeded {
		t.Errorf("original call lost evidence: %+v", r.obs)
	}
	if srv.counts()["tools/list"] != 0 {
		t.Error("overlapping operation reached server")
	}
}

type failResultStore struct{ contract.BlobStore }

func (b failResultStore) Stage(ctx context.Context, r io.Reader, n int64) (string, contract.Digest, int64, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	var d map[string]json.RawMessage
	_ = json.Unmarshal(body, &d)
	if _, ok := d["content"]; ok {
		return "", "", 0, fmt.Errorf("synthetic staging failure")
	}
	return b.BlobStore.Stage(ctx, bytes.NewReader(body), n)
}
func TestToolResultStagingFailurePreservesKnownOutcome(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	a := newTestAdapter(t, failResultStore{newFakeBlobStore()}, nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none"))
	handle, _ := openSession(t, a, 10*time.Second)
	obs, err := a.Invoke(t.Context(), testDispatch(t, echoAction(t, handle), 10*time.Second))
	if err != nil {
		t.Fatalf("post-send staging failure lost observation: %v", err)
	}
	ev := decodeEvidence(t, obs)
	if obs.Disposition != contract.DispositionSucceeded || ev.PhysicalCall.ErrorCode != "artifact_fault" || ev.PhysicalCall.RequestContext.StagingRef == "" {
		t.Errorf("outcome=%+v evidence=%s", obs, obs.Evidence)
	}
}

func TestProviderToolErrorAndSecretEcho(t *testing.T) {
	for _, tc := range []struct {
		name    string
		isError bool
		text    string
	}{{"tool error", true, "error"}, {"secret result", false, testToken}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newControlledServer()
			defer srv.close()
			srv.onRequest = func(w http.ResponseWriter, r *http.Request, body []byte, method string) bool {
				if method != "tools/call" {
					return false
				}
				var rpc struct{ ID json.RawMessage }
				_ = json.Unmarshal(body, &rpc)
				w.Header().Set("Content-Type", "application/json")
				response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"isError": tc.isError, "content": []any{map[string]any{"type": "text", "text": tc.text}}}})
				_, _ = w.Write(response)
				return true
			}
			blobs := newFakeBlobStore()
			a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "bearer"))
			handle, _ := openSession(t, a, 10*time.Second)
			obs, err := a.Invoke(t.Context(), testDispatch(t, echoAction(t, handle), 10*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			ev := decodeEvidence(t, obs)
			if tc.isError && (obs.Disposition != contract.DispositionFailed || ev.PhysicalCall.Confirmation != "authoritative_failure") {
				t.Errorf("tool failure reported as success: %s", obs.Evidence)
			}
			for _, doc := range blobs.stagedDocs() {
				if bytes.Contains(doc, []byte(testToken)) {
					t.Error("secret tool result staged")
				}
			}
			if !tc.isError && ev.PhysicalCall.ErrorCode != "result_redacted" {
				t.Errorf("missing withheld-result evidence: %s", obs.Evidence)
			}
		})
	}
}

func TestCloseSessionStatusAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stall bool
	}{{"refused", false}, {"timeout", true}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newControlledServer()
			defer srv.close()
			srv.onRequest = func(w http.ResponseWriter, r *http.Request, body []byte, method string) bool {
				if method != "DELETE" {
					return false
				}
				if tc.stall {
					<-r.Context().Done()
				} else {
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
				return true
			}
			a := newTestAdapter(t, newFakeBlobStore(), nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none"))
			handle, _ := openSession(t, a, time.Second)
			obs, err := a.Invoke(t.Context(), testDispatch(t, wireCloseSession{Schema: "zatiti.mcp.action/v1", Kind: kindCloseSession, SessionHandle: handle}, 100*time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			ev := decodeEvidence(t, obs)
			if tc.stall && obs.Disposition != contract.DispositionUnknown {
				t.Errorf("timeout disposition=%s", obs.Disposition)
			}
			if !tc.stall && (obs.Disposition != contract.DispositionFailed || ev.PhysicalCall.HTTPStatus != 405) {
				t.Errorf("refusal=%s", obs.Evidence)
			}
			if srv.counts()["DELETE"] != 1 {
				t.Errorf("DELETE count=%d", srv.counts()["DELETE"])
			}
		})
	}
}

func TestSessionCredentialReferenceCannotChange(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	secrets := newFakeSecrets(testCredentialRef, []byte(testToken))
	_, _ = secrets.Put(t.Context(), "other", []byte("other-token"))
	a := newTestAdapter(t, newFakeBlobStore(), secrets, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "bearer"))
	handle, _ := openSession(t, a, 10*time.Second)
	d := testDispatch(t, echoAction(t, handle), 10*time.Second)
	d.CredentialRef = "other"
	_, err := a.Invoke(t.Context(), d)
	if err == nil || mustFault(t, err).Code != contract.CodePermissionDenied {
		t.Fatalf("credential switch accepted: %v", err)
	}
	if srv.counts()["tools/call"] != 0 {
		t.Error("credential switch reached server")
	}
}

func TestInjectedTrustRootsKeepTLSVerification(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprint(trusted), func(t *testing.T) {
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			if trusted {
				client = srv.httpSrv.Client()
			}
			a, err := New(contract.AdapterDependencies{HTTP: client, Clock: newFakeClock(), Blobs: newFakeBlobStore()}, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none"))
			if err != nil {
				t.Fatal(err)
			}
			_, obs := openSession(t, a, 10*time.Second)
			if (obs.Disposition == contract.DispositionSucceeded) != trusted {
				t.Errorf("trusted=%v disposition=%s", trusted, obs.Disposition)
			}
			if a.(*Adapter).transport.TLSClientConfig != nil && a.(*Adapter).transport.TLSClientConfig.InsecureSkipVerify {
				t.Error("dependency bypass copied")
			}
		})
	}
}

func TestExpiredSessionIsDroppedWithoutReconnect(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	a := newTestAdapter(t, newFakeBlobStore(), nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none")).(*Adapter)
	handle, _ := openSession(t, a, 10*time.Second)
	entry, _ := a.sessions.get(handle)
	srv.dropSession(entry.session.ID())
	action := testDispatch(t, echoAction(t, handle), time.Second)
	obs, err := a.Invoke(t.Context(), action)
	if err != nil {
		t.Fatal(err)
	}
	ev := decodeEvidence(t, obs)
	if ev.PhysicalCall.HTTPStatus != 404 {
		t.Errorf("missing server 404: %s", obs.Evidence)
	}
	_, err = a.Invoke(t.Context(), action)
	if err == nil || mustFault(t, err).Code != contract.CodePrerequisiteMissing {
		t.Errorf("expired handle retained: %v", err)
	}
}

func TestSecretCatalogIsFailedInsteadOfEmptySuccess(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	srv.onRequest = func(w http.ResponseWriter, r *http.Request, body []byte, method string) bool {
		if method != "tools/list" {
			return false
		}
		var rpc struct{ ID json.RawMessage }
		_ = json.Unmarshal(body, &rpc)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":%q,"inputSchema":{"type":"object"}}]}}`, rpc.ID, testToken)
		return true
	}
	a := newTestAdapter(t, newFakeBlobStore(), newFakeSecrets(testCredentialRef, []byte(testToken)), buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "bearer"))
	handle, _ := openSession(t, a, 10*time.Second)
	obs, err := a.Invoke(t.Context(), testDispatch(t, wireListTools{Schema: "zatiti.mcp.action/v1", Kind: kindListTools, SessionHandle: handle}, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(obs.Evidence, []byte(testToken)) {
		t.Error("catalog secret escaped")
	}
	if obs.Disposition != contract.DispositionFailed {
		t.Errorf("secret catalog treated as successful empty discovery: %s", obs.Evidence)
	}
}

func TestStatelessCloseStillAttemptsOneDelete(t *testing.T) {
	srv := newSEP2575AwareControlledServer()
	defer srv.close()
	a := newTestAdapter(t, newFakeBlobStore(), nil, buildProfileJSONWithVersion(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none", "2026-07-28"))
	handle, _ := openSession(t, a, time.Second)
	obs, err := a.Invoke(t.Context(), testDispatch(t, wireCloseSession{Schema: "zatiti.mcp.action/v1", Kind: kindCloseSession, SessionHandle: handle}, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if srv.counts()["DELETE"] != 1 || decodeEvidence(t, obs).PhysicalCall.RequestContext.StagingRef == "" {
		t.Errorf("stateless close missing physical attempt: %s", obs.Evidence)
	}
}

func TestNumericCredentialEchoIsNotStaged(t *testing.T) {
	const token = "123456789"
	srv := newControlledServer()
	defer srv.close()
	srv.onRequest = func(w http.ResponseWriter, r *http.Request, body []byte, method string) bool {
		if method != "tools/call" {
			return false
		}
		var rpc struct{ ID json.RawMessage }
		_ = json.Unmarshal(body, &rpc)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[],"structuredContent":{"token":%s}}}`, rpc.ID, token)
		return true
	}
	blobs := newFakeBlobStore()
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(token)), buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "bearer"))
	handle, _ := openSession(t, a, time.Second)
	obs, err := a.Invoke(t.Context(), testDispatch(t, echoAction(t, handle), time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if decodeEvidence(t, obs).PhysicalCall.ErrorCode != "result_redacted" {
		t.Errorf("numeric credential not withheld: %s", obs.Evidence)
	}
	for _, doc := range blobs.stagedDocs() {
		if bytes.Contains(doc, []byte(token)) {
			t.Error("numeric credential staged")
		}
	}
}
