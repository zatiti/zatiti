package mcpclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// This regression verifies SDK reply concurrency and evidence attribution.
// The observed reply POSTs remain a frozen one-request-contract blocker; this
// test does not qualify request-count compliance.
func TestIndependentParallelRefusalReplies(t *testing.T) {
	const n = 16
	srv := newControlledServer()
	defer srv.close()
	var replies atomic.Int32
	all := make(chan struct{})
	srv.onRequest = func(w http.ResponseWriter, r *http.Request, body []byte, method string) bool {
		if method == "POST(unparsed)" {
			if replies.Add(1) == n {
				close(all)
			}
			select {
			case <-all:
			case <-r.Context().Done():
				return true
			}
			w.Header().Set("Mcp-Session-Id", "refusal-response-session")
			w.WriteHeader(http.StatusAccepted)
			return true
		}
		if method != "tools/call" {
			return false
		}
		var rpc struct{ ID json.RawMessage }
		_ = json.Unmarshal(body, &rpc)
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < n; i++ {
			_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"server-%d\",\"method\":\"ping\",\"params\":{}}\n\n", i)
		}
		w.(http.Flusher).Flush()
		select {
		case <-all:
		case <-r.Context().Done():
			return true
		}
		_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}}\n\n", rpc.ID)
		return true
	}
	a := newTestAdapter(t, newFakeBlobStore(), nil, buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none"))
	handle, _ := openSession(t, a, time.Second)
	before := srv.total()
	obs, err := a.Invoke(t.Context(), testDispatch(t, echoAction(t, handle), 3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("physical HTTP requests for one tool call with %d refusal replies: %d; server counts=%v", n, srv.total()-before, srv.counts())
	if srv.counts()["DELETE"] != 0 {
		t.Error("SDK cleanup sent an unadmitted DELETE during call_tool")
	}
	if replies.Load() != n {
		t.Fatalf("only %d replies: %s", replies.Load(), obs.Evidence)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Errorf("not succeeded: %s", obs.Evidence)
	}
	if strings.Count(string(obs.Evidence), `"ping"`) != n {
		t.Errorf("refusals missing: %s", obs.Evidence)
	}
}
