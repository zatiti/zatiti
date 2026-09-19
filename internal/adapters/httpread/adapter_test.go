package httpread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestNew_RequiresDependencies(t *testing.T) {
	profile := defaultProfileJSON(t, "https://example.test", "application/json")
	if _, err := New(contract.AdapterDependencies{Clock: newFakeClock()}, profile); err == nil {
		t.Error("expected an error when HTTP is nil")
	}
	if _, err := New(contract.AdapterDependencies{HTTP: &http.Client{}}, profile); err == nil {
		t.Error("expected an error when Clock is nil")
	}
	if _, err := New(contract.AdapterDependencies{HTTP: &http.Client{}, Clock: newFakeClock()}, []byte(`{}`)); err == nil {
		t.Error("expected an error for an invalid profile")
	}
}

func TestName(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}), newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	if a.Name() != "httpread" {
		t.Errorf("Name() = %q", a.Name())
	}
}

func TestContract_ReturnsThreeSchemas(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}), newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	var doc struct {
		Schema           string          `json:"schema"`
		ProfileSchema    json.RawMessage `json:"profile_schema"`
		ParametersSchema json.RawMessage `json:"parameters_schema"`
		EvidenceSchema   json.RawMessage `json:"evidence_schema"`
	}
	if err := json.Unmarshal(a.Contract(), &doc); err != nil {
		t.Fatalf("unmarshal Contract(): %v", err)
	}
	if doc.Schema != "zatiti.httpread.contract/v1" {
		t.Errorf("schema = %q", doc.Schema)
	}
	if len(doc.ProfileSchema) == 0 || len(doc.ParametersSchema) == 0 || len(doc.EvidenceSchema) == 0 {
		t.Error("expected all three sub-schemas to be populated")
	}
}

func TestInvoke_RejectsWrongAdapter(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}), newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/", "application/json"))
	d.Adapter = "not-httpread"
	if _, err := a.Invoke(context.Background(), d); err == nil {
		t.Fatal("expected an error for a mismatched adapter name")
	}
}

func TestInvoke_RejectsOriginNotAllowed(t *testing.T) {
	transport := &countingTransport{fn: func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://not-allowed.test/", "application/json"))
	_, err := a.Invoke(context.Background(), d)
	if err == nil {
		t.Fatal("expected an error for a disallowed origin")
	}
	if f := mustFault(t, err); f.Code != contract.CodePermissionDenied {
		t.Errorf("fault code = %q, want permission_denied", f.Code)
	}
	if transport.count() != 0 {
		t.Errorf("physical calls = %d, want 0", transport.count())
	}
}

func TestInvoke_RejectsMediaTypeNotAllowed(t *testing.T) {
	transport := &countingTransport{fn: func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/", "text/html"))
	_, err := a.Invoke(context.Background(), d)
	if err == nil {
		t.Fatal("expected an error for a disallowed expected_media_type")
	}
	if f := mustFault(t, err); f.Code != contract.CodeCapabilityUnsupported {
		t.Errorf("fault code = %q, want capability_unsupported", f.Code)
	}
	if transport.count() != 0 {
		t.Errorf("physical calls = %d, want 0", transport.count())
	}
}

func TestInvoke_RejectsDisallowedDestination(t *testing.T) {
	transport := &countingTransport{fn: func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}}
	// example.test resolves (per the fixed lookup) only to a private address.
	a := newTestAdapter(t, transport, newFakeBlobStore(), fixedLookup("10.0.0.5"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/", "application/json"))
	_, err := a.Invoke(context.Background(), d)
	if err == nil {
		t.Fatal("expected an error when every resolved address is disallowed")
	}
	if f := mustFault(t, err); f.Code != contract.CodePermissionDenied {
		t.Errorf("fault code = %q, want permission_denied", f.Code)
	}
	if transport.count() != 0 {
		t.Errorf("physical calls = %d, want 0 (never dial past a blocked resolution)", transport.count())
	}
}

func TestInvoke_DNSFailureNotFoundIsNotRetryable(t *testing.T) {
	transport := &countingTransport{fn: func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), failingLookup("example.test", true), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/", "application/json"))
	_, err := a.Invoke(context.Background(), d)
	f := mustFault(t, err)
	if f.Code != contract.CodeOutcomeUnknown {
		t.Errorf("fault code = %q, want outcome_unknown", f.Code)
	}
	if f.Retryable {
		t.Error("expected a name-not-found DNS failure to be reported as not retryable")
	}
	if transport.count() != 0 {
		t.Errorf("physical calls = %d, want 0", transport.count())
	}
}

func TestInvoke_DNSFailureTransientIsRetryable(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}), newFakeBlobStore(), failingLookup("example.test", false), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/", "application/json"))
	_, err := a.Invoke(context.Background(), d)
	f := mustFault(t, err)
	if f.Code != contract.CodeOutcomeUnknown {
		t.Errorf("fault code = %q, want outcome_unknown", f.Code)
	}
	if !f.Retryable {
		t.Error("expected a transient DNS failure to be reported as retryable")
	}
}

func TestInvoke_Succeeds(t *testing.T) {
	body := `{"hello":"world"}`
	transport := &countingTransport{fn: func(req *http.Request) (*http.Response, error) {
		return jsonResponse(200, "application/json; charset=utf-8", body), nil
	}}
	blobs := newFakeBlobStore()
	a := newTestAdapter(t, transport, blobs, fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json"))

	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("Disposition = %q", obs.Disposition)
	}
	if obs.ConfirmedAt == nil {
		t.Fatal("expected ConfirmedAt to be set")
	}
	validateEvidence(t, obs.Evidence)

	var ev wireHTTPReadEvidence
	if err := json.Unmarshal(obs.Evidence, &ev); err != nil {
		t.Fatalf("unmarshal evidence: %v", err)
	}
	if ev.Status != 200 {
		t.Errorf("status = %d", ev.Status)
	}
	if ev.MediaType != "application/json" {
		t.Errorf("media_type = %q, want the charset param stripped", ev.MediaType)
	}
	if string(ev.ContentDigest) != sha256Hex(body) {
		t.Errorf("content_digest = %q", ev.ContentDigest)
	}
	if ev.ContentSize == nil || *ev.ContentSize != int64(len(body)) {
		t.Errorf("content_size = %v", ev.ContentSize)
	}
	if len(ev.StagedOutputs) != 2 {
		t.Fatalf("staged_outputs = %v, want the request record and the body", ev.StagedOutputs)
	}
	if ev.StagedOutputs[0].Purpose != "context" {
		t.Errorf("first staged output = %+v, want the request record", ev.StagedOutputs[0])
	}
	if ev.StagedOutputs[1].Purpose != "public_source" || ev.StagedOutputs[1].Classification != "public" {
		t.Errorf("second staged output = %+v", ev.StagedOutputs[1])
	}
	assertRequestContext(t, blobs, ev, "https://example.test/data", "93.184.216.34:443")
	if ev.PhysicalCall.Confirmation != "authoritative_success" {
		t.Errorf("confirmation = %q", ev.PhysicalCall.Confirmation)
	}
	if ev.PhysicalCall.RequestedDestination != "https://example.test/data" {
		t.Errorf("requested_destination = %q", ev.PhysicalCall.RequestedDestination)
	}
	if ev.PhysicalCall.ResolvedDestination != "93.184.216.34:443" {
		t.Errorf("resolved_destination = %q", ev.PhysicalCall.ResolvedDestination)
	}
	if ev.PhysicalCall.AccountIdentity != "public" {
		t.Errorf("account_identity = %q", ev.PhysicalCall.AccountIdentity)
	}
	if len(ev.ValidatedDialAddresses) != 1 || ev.ValidatedDialAddresses[0] != "93.184.216.34" {
		t.Errorf("validated_dial_addresses = %v", ev.ValidatedDialAddresses)
	}
	if blobs.stagedCount() != 2 {
		t.Errorf("staged count = %d, want exactly 2: request record and body (no hidden retry)", blobs.stagedCount())
	}
	if transport.count() != 1 {
		t.Errorf("physical calls = %d, want exactly 1 (Z06 no hidden retries)", transport.count())
	}
}

func TestInvoke_MediaTypeMismatchStillRecordsContent(t *testing.T) {
	body := `<html>not json</html>`
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, "text/html", body), nil
	}), newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json"))

	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionFailed {
		t.Fatalf("Disposition = %q, want failed", obs.Disposition)
	}
	validateEvidence(t, obs.Evidence)
	var ev wireHTTPReadEvidence
	_ = json.Unmarshal(obs.Evidence, &ev)
	if ev.PhysicalCall.ErrorCode != "unexpected_media_type" {
		t.Errorf("error_code = %q", ev.PhysicalCall.ErrorCode)
	}
	if string(ev.ContentDigest) != sha256Hex(body) {
		t.Error("expected content to still be digested despite the media type mismatch")
	}
}

func TestInvoke_NonSuccessStatus(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(404, "application/json", `{"error":"not found"}`), nil
	}), newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/missing", "application/json"))

	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionFailed {
		t.Fatalf("Disposition = %q, want failed", obs.Disposition)
	}
	validateEvidence(t, obs.Evidence)
	var ev wireHTTPReadEvidence
	_ = json.Unmarshal(obs.Evidence, &ev)
	if ev.Status != 404 {
		t.Errorf("status = %d", ev.Status)
	}
	if ev.PhysicalCall.ErrorCode != "http_404" {
		t.Errorf("error_code = %q", ev.PhysicalCall.ErrorCode)
	}
}

func TestInvoke_NotModified(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotModified, Body: http.NoBody, Header: http.Header{}}, nil
	}), newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json", wireReadHeader{Name: "If-None-Match", Value: `"abc"`}))

	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("Disposition = %q, want succeeded for a 304", obs.Disposition)
	}
	validateEvidence(t, obs.Evidence)
}

func TestInvoke_RedirectIsNeverFollowed(t *testing.T) {
	transport := &countingTransport{fn: func(req *http.Request) (*http.Response, error) {
		resp := jsonResponse(302, "text/plain", "")
		resp.Header.Set("Location", "/moved")
		return resp, nil
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/old", "application/json"))

	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionFailed {
		t.Fatalf("Disposition = %q, want failed", obs.Disposition)
	}
	validateEvidence(t, obs.Evidence)
	var ev wireHTTPReadEvidence
	_ = json.Unmarshal(obs.Evidence, &ev)
	if ev.PhysicalCall.ErrorCode != "redirect_not_permitted" {
		t.Errorf("error_code = %q", ev.PhysicalCall.ErrorCode)
	}
	if ev.RedirectLocation != "https://example.test/moved" {
		t.Errorf("redirect_location = %q", ev.RedirectLocation)
	}
	if transport.count() != 1 {
		t.Errorf("physical calls = %d, want exactly 1 (max_redirects=0 must never auto-follow)", transport.count())
	}
}

func TestInvoke_MaxBytesExceeded(t *testing.T) {
	big := make([]byte, 200)
	for i := range big {
		big[i] = 'a'
	}
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, "text/plain", string(big)), nil
	}), newFakeBlobStore(), fixedLookup("93.184.216.34"), buildProfileJSON(t, []string{"https://example.test"}, []string{"text/plain"}, 100))
	d := testDispatch(t, readAction("https://example.test/big", "text/plain"))

	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionFailed {
		t.Fatalf("Disposition = %q, want failed", obs.Disposition)
	}
	validateEvidence(t, obs.Evidence)
	var ev wireHTTPReadEvidence
	_ = json.Unmarshal(obs.Evidence, &ev)
	if ev.PhysicalCall.ErrorCode != "max_bytes_exceeded" {
		t.Errorf("error_code = %q", ev.PhysicalCall.ErrorCode)
	}
	if ev.ContentDigest != "" {
		t.Error("expected no content_digest to be recorded for a bound-violating body")
	}
	if len(ev.StagedOutputs) != 1 || ev.StagedOutputs[0].Purpose != "context" {
		t.Errorf("staged_outputs = %v, want only the request record for a bound-violating body", ev.StagedOutputs)
	}
}

// slowReadCloser returns n bytes then a read error, simulating a body that
// starts arriving (a status line was already received) but never
// completes -- the "timeout-after-success unknown" acceptance focus.
type slowReadCloser struct {
	data []byte
	sent bool
}

func (s *slowReadCloser) Read(p []byte) (int, error) {
	if s.sent {
		return 0, errors.New("connection reset by peer")
	}
	n := copy(p, s.data)
	s.sent = true
	return n, nil
}
func (s *slowReadCloser) Close() error { return nil }

func TestInvoke_BodyReadFailureAfterStatusIsUnknown(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       &slowReadCloser{data: []byte(`{"partial":`)},
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}), newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json"))

	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionUnknown {
		t.Fatalf("Disposition = %q, want unknown", obs.Disposition)
	}
	if obs.ConfirmedAt != nil {
		t.Error("an unknown disposition must not carry a confirmed time")
	}
	validateEvidence(t, obs.Evidence)
	var ev wireHTTPReadEvidence
	_ = json.Unmarshal(obs.Evidence, &ev)
	if ev.Status != 200 {
		t.Errorf("status = %d, want the status line that was actually received", ev.Status)
	}
	if ev.PhysicalCall.Confirmation != "unknown" {
		t.Errorf("confirmation = %q", ev.PhysicalCall.Confirmation)
	}
}

func TestInvoke_NoHiddenRetryOnTransportFailure(t *testing.T) {
	transport := &countingTransport{fn: func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset by peer")
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json"))

	_, err := a.Invoke(context.Background(), d)
	f := mustFault(t, err)
	if f.Code != contract.CodeOutcomeUnknown {
		t.Errorf("fault code = %q, want outcome_unknown", f.Code)
	}
	if !f.Retryable {
		t.Error("expected a generic pre-response transport failure to be reported as retryable")
	}
	if transport.count() != 1 {
		t.Errorf("physical calls = %d, want exactly 1 (Z06 no hidden retries)", transport.count())
	}
}

func TestInvoke_HeadersAppliedAndDefaultUserAgent(t *testing.T) {
	var seen http.Header
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seen = req.Header.Clone()
		return jsonResponse(200, "application/json", "{}"), nil
	}), newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json", wireReadHeader{Name: "Accept-Language", Value: "en-US"}))

	if _, err := a.Invoke(context.Background(), d); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got := seen.Get("Accept-Language"); got != "en-US" {
		t.Errorf("Accept-Language = %q", got)
	}
	if got := seen.Get("User-Agent"); got != defaultUserAgent {
		t.Errorf("User-Agent = %q, want the default", got)
	}
}

func TestInvoke_CallerSuppliedUserAgentOverridesDefault(t *testing.T) {
	var seen http.Header
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seen = req.Header.Clone()
		return jsonResponse(200, "application/json", "{}"), nil
	}), newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json", wireReadHeader{Name: "User-Agent", Value: "research-bot/1"}))

	if _, err := a.Invoke(context.Background(), d); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got := seen.Get("User-Agent"); got != "research-bot/1" {
		t.Errorf("User-Agent = %q, want the caller-supplied value", got)
	}
}

func TestReconcile_PerformsTheSameBoundedRead(t *testing.T) {
	transport := &countingTransport{fn: func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, "application/json", `{"ok":true}`), nil
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json"))

	obs, err := a.Reconcile(context.Background(), d)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("Disposition = %q", obs.Disposition)
	}
	validateEvidence(t, obs.Evidence)
	if transport.count() != 1 {
		t.Errorf("physical calls = %d, want exactly 1", transport.count())
	}
}

func TestInvoke_DefaultPortForPlainHTTP(t *testing.T) {
	transport := &countingTransport{fn: func(req *http.Request) (*http.Response, error) {
		return jsonResponse(200, "application/json", "{}"), nil
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "http://example.test", "application/json"))
	d := testDispatch(t, readAction("http://example.test/data", "application/json"))

	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var ev wireHTTPReadEvidence
	_ = json.Unmarshal(obs.Evidence, &ev)
	if ev.PhysicalCall.ResolvedDestination != "93.184.216.34:80" {
		t.Errorf("resolved_destination = %q, want the default http port 80", ev.PhysicalCall.ResolvedDestination)
	}
}

func TestInvoke_StageFailurePropagatesAsFault(t *testing.T) {
	blobs := newFakeBlobStore()
	blobs.stageErr = &contract.Fault{Code: contract.CodeArtifactFault, Message: "staging backend unavailable"}
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, "application/json", `{"a":1}`), nil
	}), blobs, fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json"))

	_, err := a.Invoke(context.Background(), d)
	if err == nil {
		t.Fatal("expected a staging failure to surface as an error")
	}
	if f := mustFault(t, err); f.Code != contract.CodeArtifactFault {
		t.Errorf("fault code = %q, want the underlying *contract.Fault to pass through unchanged", f.Code)
	}
}

func TestInvoke_StageFailureWrapsGenericError(t *testing.T) {
	blobs := newFakeBlobStore()
	blobs.stageErr = errors.New("disk full")
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, "application/json", `{"a":1}`), nil
	}), blobs, fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json"))

	_, err := a.Invoke(context.Background(), d)
	if err == nil {
		t.Fatal("expected a staging failure to surface as an error")
	}
	if f := mustFault(t, err); f.Code != contract.CodeInternalError {
		t.Errorf("fault code = %q, want internal_error for a non-Fault staging error", f.Code)
	}
}

// TestInvoke_ProductionAdapterRefusesLoopback exercises the real,
// production DialContext (via New, not the test-only direct construction
// used elsewhere in this file) end to end: a profile whose allowed_origins
// names a loopback destination must never be dialed.
func TestInvoke_ProductionAdapterRefusesLoopback(t *testing.T) {
	profile := defaultProfileJSON(t, "http://127.0.0.1:1", "text/plain")
	a, err := New(contract.AdapterDependencies{HTTP: &http.Client{}, Clock: newFakeClock()}, profile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d := testDispatch(t, readAction("http://127.0.0.1:1/", "text/plain"))
	_, err = a.Invoke(context.Background(), d)
	if err == nil {
		t.Fatal("expected the production adapter to refuse a loopback destination")
	}
	if f := mustFault(t, err); f.Code != contract.CodePermissionDenied {
		t.Errorf("fault code = %q, want permission_denied", f.Code)
	}
}

// assertRequestContext checks physical_call.request_context is a staged
// locator matching exactly one StagedOutput of purpose context, and that the
// staged bytes are the request record actually sent: method, URL, headers as
// sent and the pinned dial address, with no credential carrier.
func assertRequestContext(t *testing.T, blobs *fakeBlobStore, ev wireHTTPReadEvidence, url, pinned string) {
	t.Helper()
	rc := ev.PhysicalCall.RequestContext
	if rc.Kind != "staged" || rc.Artifact != nil || rc.StagingRef == "" || rc.Digest == "" {
		t.Fatalf("request_context = %+v, want a staged locator", rc)
	}
	matches := 0
	for _, so := range ev.StagedOutputs {
		if so.StagingRef == rc.StagingRef {
			matches++
			if so.Digest != rc.Digest || so.Purpose != "context" || so.MediaType != "application/json" {
				t.Errorf("staged output for the request context = %+v", so)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("request_context matches %d staged outputs, want exactly 1", matches)
	}
	body, err := blobs.Open(context.Background(), rc.Digest, 0, 0)
	if err != nil {
		t.Fatalf("open staged request record: %v", err)
	}
	var record requestRecord
	if err := json.NewDecoder(body).Decode(&record); err != nil {
		t.Fatalf("decode staged request record: %v", err)
	}
	if record.Schema != requestRecordSchema || record.Method != "GET" || record.URL != url || record.PinnedAddress != pinned {
		t.Errorf("request record = %+v", record)
	}
	sawUserAgent := false
	for _, h := range record.Headers {
		if h.Name == "Authorization" || h.Name == "Cookie" {
			t.Errorf("request record carries a credential header %q", h.Name)
		}
		if h.Name == "User-Agent" {
			sawUserAgent = true
		}
	}
	if !sawUserAgent {
		t.Errorf("request record omits the User-Agent header actually sent: %+v", record.Headers)
	}
}

func TestInvoke_RequestRecordCarriesHeadersAsSent(t *testing.T) {
	var sent http.Header
	blobs := newFakeBlobStore()
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sent = req.Header.Clone()
		return jsonResponse(200, "application/json", `{}`), nil
	}), blobs, fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json",
		wireReadHeader{Name: "Accept-Language", Value: "en"}, wireReadHeader{Name: "If-None-Match", Value: `"v1"`}))
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var ev wireHTTPReadEvidence
	_ = json.Unmarshal(obs.Evidence, &ev)
	assertRequestContext(t, blobs, ev, "https://example.test/data", "93.184.216.34:443")
	body, _ := blobs.Open(context.Background(), ev.PhysicalCall.RequestContext.Digest, 0, 0)
	var record requestRecord
	_ = json.NewDecoder(body).Decode(&record)
	got := http.Header{}
	for _, h := range record.Headers {
		got.Add(h.Name, h.Value)
	}
	for name, values := range sent {
		if strings.Join(got.Values(name), ",") != strings.Join(values, ",") {
			t.Errorf("record header %s = %v, sent %v", name, got.Values(name), values)
		}
	}
	if len(got) != len(sent) {
		t.Errorf("record has %d headers, sent %d", len(got), len(sent))
	}
}

func TestInvoke_RequestRecordStagingFailureSendsNothing(t *testing.T) {
	transport := &countingTransport{fn: func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, "application/json", `{}`), nil
	}}
	blobs := newFakeBlobStore()
	blobs.stageErr = errors.New("disk full")
	a := newTestAdapter(t, transport, blobs, fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json"))
	obs, err := a.Invoke(context.Background(), d)
	f := mustFault(t, err)
	if f.Code != contract.CodeInternalError || strings.Contains(f.Message, "disk full") {
		t.Errorf("fault = %+v, want an internal_error that does not leak the store's message", f)
	}
	if obs.Evidence != nil || obs.Disposition != "" {
		t.Errorf("a refusal must carry no observation: %+v", obs)
	}
	if transport.count() != 0 {
		t.Errorf("physical calls = %d, want 0: nothing is sent when the request record cannot be staged", transport.count())
	}
}

func TestInvoke_NoBlobStoreSendsNothing(t *testing.T) {
	transport := &countingTransport{fn: func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, "application/json", `{}`), nil
	}}
	a := newTestAdapter(t, transport, nil, fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
	d := testDispatch(t, readAction("https://example.test/data", "application/json"))
	_, err := a.Invoke(context.Background(), d)
	f := mustFault(t, err)
	if f.Code != contract.CodePrerequisiteMissing {
		t.Errorf("fault code = %q, want prerequisite_missing", f.Code)
	}
	if transport.count() != 0 {
		t.Errorf("physical calls = %d, want 0", transport.count())
	}
}
