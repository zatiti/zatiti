package mcpclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zatiti/zatiti/internal/contract"
)

// requestRecordSchema names the request record document below.
const requestRecordSchema = "zatiti.adapter.request-record/v1"

// requestRecordHeader is one permitted request header, in sorted order.
type requestRecordHeader struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// requestRecord is the exact secret-free record of one physical request:
// method, destination, every header except Authorization, and the body as
// sent. The body is base64 so the record is exact whatever its encoding.
type requestRecord struct {
	Schema      string                `json:"schema"`
	Method      string                `json:"method"`
	Destination string                `json:"destination"`
	Headers     []requestRecordHeader `json:"headers"`
	BodyDigest  contract.Digest       `json:"body_digest"`
	BodySize    int64                 `json:"body_size"`
	BodyBase64  string                `json:"body_base64"`
}

// errResponseOversize is the sentinel a boundedBody Read returns once the
// profile's max_response_bytes bound is exceeded. Distinguishing this from
// an ordinary read failure is what lets the adapter report
// outcome_unknown/response_oversize instead of a generic transport failure.
var errResponseOversize = errors.New("mcpclient: response body exceeds the profile's max_response_bytes bound")

// errRequestOversize is the sentinel callRoundTripper.RoundTrip returns,
// before forwarding to the base transport, when the exact serialized
// request body exceeds the profile's max_request_bytes bound. No bytes are
// sent in this case.
type errRequestOversize struct{ size, bound int64 }

func (e *errRequestOversize) Error() string {
	return fmt.Sprintf("mcpclient: request body is %d bytes, exceeding the profile's %d byte bound", e.size, e.bound)
}

// errRedirectRefused is the sentinel callRoundTripper.RoundTrip returns in
// place of a 3xx response: max_redirects is frozen at 0, so a redirect is
// never followed, and the go-sdk's own client surfaces no path to a
// response's Location header, so this adapter captures it at the
// RoundTripper layer instead.
type errRedirectRefused struct {
	status   int
	location string
}

func (e *errRedirectRefused) Error() string {
	return fmt.Sprintf("mcpclient: server responded %d redirect to %q; max_redirects is 0, the redirect was not followed", e.status, e.location)
}

// capturedRequest is one physical request callRoundTripper observed, in the
// order it was sent: its JSON-RPC method (peeked from the body; empty for
// a DELETE with no body), the staged secret-free record, and the response
// status this adapter's own code recorded for it (filled in by the caller
// after the round trip completes, since the RoundTripper itself does not
// know the eventual disposition).
type capturedRequest struct {
	rpcMethod  string
	httpMethod string
	staged     wireStagedOutput
	locator    wireStagedLocator
	status     int
}

// callState is the per-Invoke-call context a callRoundTripper needs: the
// caller's ctx (for BlobStore.Stage), the credential to inject and the
// captured record of every physical request this one call made. One
// sessionEntry's ClientSession, and therefore its http.Client and
// callRoundTripper, lives for the session's whole lifetime and is reused
// across many separate Invoke calls (open_session, then later list_tools/
// call_tool/close_session calls); arm/disarm bracket each such call so a
// RoundTrip always attributes its staging and captures to the right one.
type callState struct {
	ctx      context.Context
	blobs    contract.BlobStore
	secret   []byte
	captured []capturedRequest
	refused  []string

	// oversize is set directly by boundedBody.Read the instant a response
	// body crosses max_response_bytes. The go-sdk's own SSE stream reader
	// (streamable.go's processStream) does not propagate a body Read error
	// through its public API: a break out of its event-scanning loop on any
	// non-errMalformedEvent read error is reported to the caller only as
	// the generic "request terminated without response", losing
	// errResponseOversize's identity before errors.Is/errors.As could ever
	// see it (confirmed by direct inspection of the returned error text).
	// This flag is this adapter's own witness of the fact, recorded at the
	// one layer that actually observes it, independent of whatever error
	// string the SDK ultimately synthesizes.
	oversize atomic.Bool
}

// callRoundTripper is the sole integration point for every physical
// request any Invoke/Reconcile call makes through the go-sdk client: it
// stages the exact secret-free request record before any byte reaches the
// wire, sets the Authorization header only after staging succeeds, bounds
// both request and response size, and captures a 3xx response's Location
// header the go-sdk's own client API exposes no other way to reach.
type callRoundTripper struct {
	base             http.RoundTripper
	maxRequestBytes  int64
	maxResponseBytes int64

	mu      sync.Mutex
	current *callState
}

// arm attaches state as the call every subsequent RoundTrip attributes to,
// until disarm clears it. Only one call may be armed on a given
// callRoundTripper at a time: the go-sdk issues its requests for one
// logical operation (Connect, ListTools, CallTool, Close) sequentially, so
// this adapter never arms concurrently from two goroutines on the same
// session.
func (rt *callRoundTripper) arm(state *callState) {
	rt.mu.Lock()
	rt.current = state
	rt.mu.Unlock()
}

func (rt *callRoundTripper) disarm() {
	rt.mu.Lock()
	rt.current = nil
	rt.mu.Unlock()
}

// recordRefusal appends method to the armed call's refused_server_requests.
// A method arriving with nothing armed (the server sent an unsolicited
// request outside any adapter-initiated call) is still refused by the
// middleware itself; there is simply no attempt to attribute it to.
func (rt *callRoundTripper) recordRefusal(method string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.current == nil {
		return
	}
	rt.current.refused = append(rt.current.refused, method)
}

func (rt *callRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	state := rt.current
	rt.mu.Unlock()
	if state == nil {
		return nil, internalError("mcpclient: a physical request was attempted outside any armed call")
	}

	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, internalError("mcpclient: reading the outgoing request body failed: %v", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))
	}
	if int64(len(bodyBytes)) > rt.maxRequestBytes {
		return nil, &errRequestOversize{size: int64(len(bodyBytes)), bound: rt.maxRequestBytes}
	}

	rpcMethod := peekRPCMethod(bodyBytes)

	permitted := http.Header{}
	for name, values := range req.Header {
		if strings.EqualFold(name, "Authorization") {
			continue
		}
		permitted[name] = values
	}

	staged, locator, err := stageRequestRecord(state.ctx, state.blobs, state.secret, req.Method, req.URL.String(), permitted, bodyBytes)
	if err != nil {
		return nil, err
	}

	rt.mu.Lock()
	state.captured = append(state.captured, capturedRequest{rpcMethod: rpcMethod, httpMethod: req.Method, staged: staged, locator: locator})
	rt.mu.Unlock()

	if len(state.secret) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(state.secret))
	}

	resp, doErr := rt.base.RoundTrip(req)
	if doErr != nil {
		return nil, doErr
	}

	rt.mu.Lock()
	if n := len(state.captured); n > 0 {
		state.captured[n-1].status = resp.StatusCode
	}
	rt.mu.Unlock()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location := resp.Header.Get("Location")
		_ = resp.Body.Close()
		return nil, &errRedirectRefused{status: resp.StatusCode, location: location}
	}

	resp.Body = &boundedBody{r: resp.Body, limit: rt.maxResponseBytes, closer: resp.Body, state: state}
	return resp, nil
}

// peekRPCMethod reads the JSON-RPC "method" field from a request body
// without a full decode; a DELETE close_session request has no body and
// yields "".
func peekRPCMethod(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var peek struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &peek); err != nil {
		return ""
	}
	return peek.Method
}

// boundedBody wraps a response body so a Read that would cross limit bytes
// returns errResponseOversize instead of the data, distinguishing "the
// body is definitively too large" from any other read failure at the call
// site via errors.As/errors.Is.
type boundedBody struct {
	r      io.Reader
	limit  int64
	read   int64
	closer io.Closer
	// state, if non-nil, is flagged the instant this body trips
	// errResponseOversize -- see callState.oversize's doc comment for why
	// this adapter cannot rely on the SDK's returned error to carry that
	// fact back out.
	state *callState
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.read > b.limit {
		b.markOversize()
		return 0, errResponseOversize
	}
	// Request one byte past the limit so "exactly at the bound" is
	// distinguishable from "exceeds it", matching httpread's convention.
	remaining := b.limit + 1 - b.read
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := b.r.Read(p)
	b.read += int64(n)
	if b.read > b.limit {
		b.markOversize()
		return n, errResponseOversize
	}
	return n, err
}

func (b *boundedBody) markOversize() {
	if b.state != nil {
		b.state.oversize.Store(true)
	}
}

func (b *boundedBody) Close() error {
	if b.closer == nil {
		return nil
	}
	return b.closer.Close()
}

// stageRequestRecord stages the request record before any byte is sent and
// returns its StagedOutput (purpose context) plus the staged
// ArtifactLocator that names it as physical_call.request_context. A record
// containing the credential is refused rather than staged -- a backstop
// that should never trigger, since Authorization is set only after this
// call returns.
func stageRequestRecord(ctx context.Context, blobs contract.BlobStore, secret []byte, method, destination string, permitted http.Header, body []byte) (wireStagedOutput, wireStagedLocator, error) {
	if blobs == nil {
		return wireStagedOutput{}, wireStagedLocator{}, prerequisiteMissing("mcpclient adapter requires a blob store dependency to stage the request context before sending")
	}
	record := requestRecord{
		Schema:      requestRecordSchema,
		Method:      method,
		Destination: destination,
		Headers:     make([]requestRecordHeader, 0, len(permitted)),
		BodyDigest:  contract.Hash(body),
		BodySize:    int64(len(body)),
		BodyBase64:  base64.StdEncoding.EncodeToString(body),
	}
	for name, values := range permitted {
		record.Headers = append(record.Headers, requestRecordHeader{Name: name, Values: values})
	}
	slices.SortFunc(record.Headers, func(a, b requestRecordHeader) int { return strings.Compare(a.Name, b.Name) })
	doc, err := json.Marshal(record)
	if err != nil {
		return wireStagedOutput{}, wireStagedLocator{}, internalError("encoding the mcpclient request record failed")
	}
	if len(secret) > 0 && (bytes.Contains(doc, secret) || bytes.Contains(body, secret)) {
		return wireStagedOutput{}, wireStagedLocator{}, internalError("the mcpclient request record would contain credential material; nothing was staged or sent")
	}
	stagingRef, digest, size, err := blobs.Stage(ctx, bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return wireStagedOutput{}, wireStagedLocator{}, blobFault(err)
	}
	staged := wireStagedOutput{
		StagingRef:     stagingRef,
		Digest:         digest,
		Size:           size,
		MediaType:      "application/json",
		Classification: "internal",
		Purpose:        "context",
	}
	return staged, wireStagedLocator{Kind: "staged", StagingRef: stagingRef, Digest: digest}, nil
}

// noChargeUsage is the ProviderUsage this adapter reports for every kind:
// the frozen contract prices tool_call_cost as a bounded estimate the
// operator configures, not a metered per-call cost this adapter observes.
func noChargeUsage() wireProviderUsage {
	return wireProviderUsage{
		Accounting: wireUsage{Currency: "USD"},
		Billing:    "no_charge",
	}
}

// stageToolResult stages the tool call's raw content/structured content as
// the single purpose tool_result StagedOutput the frozen contract declares.
// Staging is best-effort: a nil BlobStore or empty body yields no staged
// output, and a Stage failure is returned to the caller to decide whether
// it is worth recording -- it must never erase an already-confirmed
// provider observation.
func stageToolResult(ctx context.Context, blobs contract.BlobStore, body []byte, classification string) (*wireStagedOutput, error) {
	if len(body) == 0 || blobs == nil {
		return nil, nil
	}
	stagingRef, digest, size, err := blobs.Stage(ctx, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, blobFault(err)
	}
	if classification == "" {
		classification = "internal"
	}
	return &wireStagedOutput{
		StagingRef:     stagingRef,
		Digest:         digest,
		Size:           size,
		MediaType:      "application/json",
		Classification: classification,
		Purpose:        "tool_result",
	}, nil
}
