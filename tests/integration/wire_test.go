package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// wireTransport is the desktop-side driver: the Flutter application at
// apps/desktop is a direct wire client of internal/server, so this driver
// speaks exactly what it consumes and nothing internal/client adds: one
// POST /v1/operations/{id} per call over the private Unix socket, the
// request envelope as JSON, the credential as the Authorization header, and
// the result envelope read back from the HTTP status the frozen mapping
// assigns. Every call opens a fresh connection, as a reconnecting client
// does.
type wireTransport struct {
	sock  string
	token string
}

func (wireTransport) name() string { return "wire" }

func (w wireTransport) post(t *testing.T, op string, req contract.Request) (int, contract.Result, error) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("wire: encode request: %v", err)
	}
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", w.sock)
			},
		},
	}
	httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://zatiti/v1/operations/"+op, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("wire: build request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if w.token != "" {
		httpReq.Header.Set("Authorization", w.token)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return 0, contract.Result{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, contract.Result{}, err
	}
	var res contract.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("wire: response %d is not one result envelope: %v: %s", resp.StatusCode, err, raw)
	}
	return resp.StatusCode, res, nil
}

func (w wireTransport) run(t *testing.T, c call) outcome {
	status, res, err := w.post(t, c.op, contract.Request{Schema: contract.SchemaRequest, SubmissionKey: c.key, Input: mustJSON(c.input)})
	if err != nil {
		return outcome{signal: "transport error: " + err.Error()}
	}
	out := outcome{envelope: res, signal: "http " + itoa(status)}
	if res.Error != nil {
		out.code = res.Error.Code
	}
	want := http.StatusOK
	switch {
	case res.Status == contract.StatusAccepted:
		want = http.StatusAccepted
	case res.Status == contract.StatusFailed && res.Error != nil:
		want = contract.HTTPStatus(res.Error)
	}
	if status != want {
		t.Errorf("%s: wire HTTP status %d, the frozen mapping of status %q code %q is %d", c.name, status, res.Status, out.code, want)
	}
	return out
}

// TestWireClientReplaysOriginalKeyAfterDisconnect (Z16 disconnect after
// mutation, command lookup; desktop contract): a wire client that loses the
// response to a keyed mutation re-sends the identical request on a new
// connection and receives the original command; command.get with the frozen
// input returns that same command with its complete result.
func TestWireClientReplaysOriginalKeyAfterDisconnect(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	f.provisionTransportCredential(transportToken)
	w := wireTransport{sock: f.serve(), token: transportToken}
	req := contract.Request{Schema: contract.SchemaRequest, SubmissionKey: "wire-1", Input: mustJSON(principalInput(f, "wire-agent"))}

	status, first, err := w.post(t, "principal.create", req)
	if err != nil || status != http.StatusOK || first.Status != contract.StatusCompleted {
		t.Fatalf("first submission: http %d status %q err %v", status, first.Status, err)
	}
	// The connection is gone (DisableKeepAlives); the client knows only its
	// key and re-sends verbatim.
	status, again, err := w.post(t, "principal.create", req)
	if err != nil || status != http.StatusOK || again.CommandID != first.CommandID || canonical(t, again.Data) != canonical(t, first.Data) {
		t.Fatalf("re-sent submission: http %d command %s err %v, want the original %s with identical data", status, again.CommandID, err, first.CommandID)
	}
	f.expectPrincipals("wire-agent")

	status, lookup, err := w.post(t, "command.get", contract.Request{Schema: contract.SchemaRequest, Input: mustJSON(map[string]any{
		"scope": f.scope(), "submission_key": "wire-1", "operation": "principal.create", "operation_version": 1,
	})})
	if err != nil || status != http.StatusOK {
		t.Fatalf("command.get: http %d err %v", status, err)
	}
	var out struct {
		Resource retainedCommand `json:"resource"`
	}
	decode(t, lookup.Data, &out)
	if out.Resource.ID != first.CommandID || out.Resource.Result.CommandID != first.CommandID ||
		canonical(t, out.Resource.Result.Data) != canonical(t, first.Data) {
		t.Fatalf("command.get returned %+v, want the original command %s and result", out.Resource, first.CommandID)
	}

	// Without a credential the wire is refused with the mapped status and a
	// failed envelope, never data.
	status, denied, err := (wireTransport{sock: w.sock}).post(t, "installation.status", contract.Request{Schema: contract.SchemaRequest, Input: mustJSON(map[string]any{"scope": f.scope()})})
	if err != nil || denied.Status != contract.StatusFailed || denied.Error == nil || status != contract.HTTPStatus(denied.Error) {
		t.Fatalf("unauthenticated wire call: http %d %+v err %v", status, denied, err)
	}
}
