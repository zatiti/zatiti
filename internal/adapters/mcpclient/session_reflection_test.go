package mcpclient

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestIndependentSessionIDReflectedInRPCReply(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()
	reply := make(chan struct{})
	rawSessionChannel := make(chan string, 1)
	srv.onRequest = func(w http.ResponseWriter, r *http.Request, body []byte, method string) bool {
		if method == "POST(unparsed)" {
			w.WriteHeader(http.StatusAccepted)
			close(reply)
			return true
		}
		if method != "tools/call" {
			return false
		}
		rawSession := r.Header.Get("Mcp-Session-Id")
		rawSessionChannel <- rawSession
		var rpc struct{ ID json.RawMessage }
		_ = json.Unmarshal(body, &rpc)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%q,\"method\":\"ping\",\"params\":{}}\n\n", rawSession)
		w.(http.Flusher).Flush()
		select {
		case <-reply:
		case <-r.Context().Done():
			return true
		}
		_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}}\n\n", rpc.ID)
		return true
	}
	blobs := newFakeBlobStore()
	a := newTestAdapter(t, blobs, nil, withControlReplyBudget(t, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none"), 1, 0))
	handle, _ := openSession(t, a, time.Second)
	action := echoAction(t, handle)
	action.ControlReplyLimit = 1
	_, err := a.Invoke(t.Context(), testDispatch(t, action, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	rawSession := <-rawSessionChannel
	if rawSession == "" {
		t.Fatal("no stateful session observed")
	}
	for _, doc := range blobs.stagedDocs() {
		var record struct {
			BodyBase64 string `json:"body_base64"`
		}
		_ = json.Unmarshal(doc, &record)
		body, _ := base64.StdEncoding.DecodeString(record.BodyBase64)
		if bytes.Contains(body, []byte(rawSession)) {
			t.Errorf("raw session ID was staged inside reflected server RPC response body")
		}
	}
}
