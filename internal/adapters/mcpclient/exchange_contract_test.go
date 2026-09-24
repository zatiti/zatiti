package mcpclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestLostHandshakeIsRetainedWithoutInventedStatusOrRetry(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	srv.onRequest = func(w http.ResponseWriter, r *http.Request, _ []byte, method string) bool {
		if method != "server/discover" {
			return false
		}
		<-r.Context().Done()
		return true
	}
	a := newTestAdapter(t, newFakeBlobStore(), nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none"))
	_, obs := openSession(t, a, 100*time.Millisecond)
	ev := decodeEvidence(t, obs)
	if obs.Disposition != contract.DispositionUnknown || ev.PhysicalCall.RequestSent != "unknown" {
		t.Fatalf("lost handshake outcome=%s evidence=%s", obs.Disposition, obs.Evidence)
	}
	if srv.total() != 1 || len(ev.Handshake) != 1 || len(ev.Exchanges) != 1 {
		t.Fatalf("lost probe retried/dropped: requests=%v evidence=%s", srv.counts(), obs.Evidence)
	}
	if ev.Handshake[0].HTTPStatus != 0 || ev.Handshake[0].RequestSent != "unknown" || ev.Exchanges[0].HTTPStatus != 0 {
		t.Error("lost handshake fabricated response status")
	}
	if ev.Exchanges[0].RequestContext.StagingRef == "" || len(ev.StagedOutputs) != 1 {
		t.Error("lost handshake request context not retained")
	}
	var doc map[string]json.RawMessage
	_ = json.Unmarshal(obs.Evidence, &doc)
	var exchanges []map[string]json.RawMessage
	_ = json.Unmarshal(doc["exchanges"], &exchanges)
	if _, ok := exchanges[0]["http_status"]; ok {
		t.Error("unknown HTTP status must be omitted")
	}
}

func TestStagingFailureHasTypedUnavailableContextAndNoEmptyLocator(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	blobs := newFakeBlobStore()
	blobs.stageErr = errors.New("synthetic unavailable stage store")
	a := newTestAdapter(t, blobs, nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none"))
	_, obs := openSession(t, a, time.Second)
	ev := decodeEvidence(t, obs)
	if obs.Disposition != contract.DispositionNotSent || ev.PhysicalCall.RequestSent != "no" || ev.PhysicalCall.ContextUnavailable != "staging_failed" {
		t.Fatalf("pre-send failure=%s", obs.Evidence)
	}
	if ev.PhysicalCall.RequestContext != nil || len(ev.Exchanges) != 0 || len(ev.StagedOutputs) != 0 || srv.total() != 0 {
		t.Errorf("pre-send failure invented staging or sent request: %s", obs.Evidence)
	}
	var doc struct {
		Physical map[string]json.RawMessage `json:"physical_call"`
	}
	_ = json.Unmarshal(obs.Evidence, &doc)
	if _, ok := doc.Physical["request_context"]; ok {
		t.Error("empty request locator emitted")
	}
}

func TestControlRepliesStayInsideAdmittedBudgetAndAreAllPublished(t *testing.T) {
	for _, limit := range []int64{0, 1, 4} {
		t.Run(string(rune('0'+limit)), func(t *testing.T) {
			srv := newControlledServer()
			defer srv.close()
			profile := withControlReplyBudget(t, buildProfileJSON(t, srv.endpoint(), true, []string{"server-requests"}, []string{"public"}, "none"), 4, 3)
			a := newTestAdapter(t, newFakeBlobStore(), nil, profile)
			handle, _ := openSession(t, a, time.Second)
			schema, digest := inputSchemaFor(t, nil)
			action := wireCallTool{Schema: "zatiti.mcp.action/v1", Kind: kindCallTool, SessionHandle: handle, Tool: "server-requests", Arguments: json.RawMessage(`{}`), InputSchema: schema, InputSchemaDigest: digest, Classification: "public", ControlReplyLimit: limit}
			before := srv.total()
			obs, err := a.Invoke(t.Context(), testDispatch(t, action, 2*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			ev := decodeEvidence(t, obs)
			if actual := srv.total() - before; actual != int(limit)+1 {
				t.Fatalf("physical request count=%d budget=%d counts=%v", actual, limit, srv.counts())
			}
			if len(ev.Exchanges) != int(limit)+1 {
				t.Fatalf("unrecorded physical request: %s", obs.Evidence)
			}
			outputs := map[string]wireStagedOutput{}
			for _, output := range ev.StagedOutputs {
				outputs[output.StagingRef] = output
			}
			for i, exchange := range ev.Exchanges {
				output, ok := outputs[exchange.RequestContext.StagingRef]
				if !ok || output.Digest != exchange.RequestContext.Digest || exchange.Ordinal != int64(i+1) {
					t.Errorf("exchange has no matching staged output: %+v", exchange)
				}
				if i == 0 && (exchange.Kind != "main" || exchange.RPCMethod != "tools/call") {
					t.Errorf("wrong primary exchange: %+v", exchange)
				}
				if i > 0 && (exchange.Kind != "control_reply" || exchange.Method != "POST") {
					t.Errorf("wrong control exchange: %+v", exchange)
				}
			}
			if ev.Usage.Accounting.Estimated != limit*3 {
				t.Errorf("usage=%+v, want actual reply cost %d", ev.Usage, limit*3)
			}
			if limit < 4 {
				if obs.Disposition != contract.DispositionUnknown || ev.PhysicalCall.ErrorCode != "control_reply_limit_exceeded" {
					t.Errorf("overflow did not preserve unknown outcome: %s", obs.Evidence)
				}
				_, err = a.Invoke(t.Context(), testDispatch(t, action, time.Second))
				if err == nil || mustFault(t, err).Code != contract.CodePrerequisiteMissing {
					t.Errorf("aborted session remained available: %v", err)
				}
			} else if obs.Disposition != contract.DispositionSucceeded || len(ev.StagedOutputs) != 6 {
				t.Errorf("complete call evidence=%s", obs.Evidence)
			}
		})
	}
}

func TestTransportRejectsNonnegativeControlRepliesAndPrimaryRepeats(t *testing.T) {
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":"request","result":{}}`,
		`{"jsonrpc":"2.0","id":"request","error":{"code":-32603,"message":"other error"}}`,
		`{"jsonrpc":"2.0","id":"request","error":{"code":-32601,"message":"no","data":{"private":"data"}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			called := 0
			blobs := newFakeBlobStore()
			rt := &callRoundTripper{base: guardTestTransport(func(*http.Request) (*http.Response, error) {
				called++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody}, nil
			}), maxRequestBytes: 4096, maxResponseBytes: 4096}
			rt.arm(&callState{ctx: t.Context(), kind: kindCallTool, blobs: blobs, controlReplyLimit: 1})
			req, err := http.NewRequestWithContext(t.Context(), "POST", "https://example.invalid/mcp", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := rt.RoundTrip(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			rt.disarm()
			if err == nil || called != 0 || len(blobs.stagedDocs()) != 0 {
				t.Error("non-refusal reply was staged or sent")
			}
		})
	}
	called := 0
	rt := &callRoundTripper{base: guardTestTransport(func(*http.Request) (*http.Response, error) {
		called++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody}, nil
	}), maxRequestBytes: 4096, maxResponseBytes: 4096}
	rt.arm(&callState{ctx: t.Context(), kind: kindCallTool, blobs: newFakeBlobStore()})
	for i := 0; i < 2; i++ {
		req, err := http.NewRequestWithContext(t.Context(), "POST", "https://example.invalid/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}`))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := rt.RoundTrip(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if (err != nil) != (i == 1) {
			t.Errorf("request%d error=%v", i, err)
		}
	}
	rt.disarm()
	if called != 1 {
		t.Errorf("primary operation repeated %d times", called)
	}
}

func TestFullSessionTableRetainsCompletedHandshakeEvidence(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	a := newTestAdapter(t, newFakeBlobStore(), nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none")).(*Adapter)
	for i := 0; i < maxSessions; i++ {
		a.sessions.entries[strconv.Itoa(i)] = nil
	}
	_, obs := openSession(t, a, time.Second)
	ev := decodeEvidence(t, obs)
	if obs.Disposition != contract.DispositionFailed || ev.PhysicalCall.ErrorCode != "session_limit_exceeded" || ev.PhysicalCall.RequestSent != "yes" {
		t.Fatalf("completed handshake lost: %s", obs.Evidence)
	}
	if len(ev.Exchanges) != 3 || len(ev.StagedOutputs) != 3 || ev.PhysicalCall.RequestContext == nil {
		t.Errorf("missing handshake contexts: %s", obs.Evidence)
	}
	if srv.counts()["DELETE"] != 0 {
		t.Error("table exhaustion issued unadmitted cleanup DELETE")
	}
}

func TestPinnedProfileAndControlBudgetRefuseBeforeNetwork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		digest contract.Digest
		limit  int64
	}{{"profile drift", contract.Hash([]byte("different profile")), 0}, {"unadmitted control budget", "", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newControlledServer()
			defer srv.close()
			a := newTestAdapter(t, newFakeBlobStore(), nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none"))
			action := wireOpenSession{Schema: "zatiti.mcp.action/v1", Kind: kindOpenSession, ClientName: "test", ClientVersion: "1", ProfileDigest: tc.digest, ControlReplyLimit: tc.limit}
			_, err := a.Invoke(t.Context(), testDispatch(t, action, time.Second))
			if err == nil || mustFault(t, err).Code != contract.CodePermissionDenied {
				t.Errorf("unadmitted action accepted: %v", err)
			}
			if srv.total() != 0 {
				t.Error("unadmitted action reached provider")
			}
		})
	}
}
