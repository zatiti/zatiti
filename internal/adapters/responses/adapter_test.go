package responses

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

const completedBody = `{"ref":"resp-001","state":"completed","finish":"completed","texts":["A cited brief.","A second part."],"tokens":{"in":120,"out":3},"usage_ref":"usage-77"}`

// defaultWorstCase is the charge the default action is admitted under: the
// synthetic protocol bounds input at one token per byte of the request it
// would encode with an EMPTY session handle (testProtocol.inputBound always
// measures with handle "", matching the qualified OpenAI protocol's own
// bound, which does not count the conversation handle as model-visible
// text) -- 2 micro-units each -- and the action allows 1000 output tokens
// at 7/2. h.transport.bodies[0] carries the real, non-empty session handle,
// so its synthetic_handle field is cleared and the body re-measured before
// pricing, exactly mirroring what admitStep actually admitted under.
func defaultWorstCase(h *harness) int64 {
	var sent testWireRequest
	if err := json.Unmarshal(h.transport.bodies[0], &sent); err != nil {
		panic("defaultWorstCase: decode sent body: " + err.Error())
	}
	sent.Handle = ""
	body, err := json.Marshal(sent)
	if err != nil {
		panic("defaultWorstCase: re-marshal sent body: " + err.Error())
	}
	return int64(len(body))*2 + 3500
}

// ---------- the unspecified wire boundary ----------

func TestProductionInvokeRefusesAtTheWireBoundary(t *testing.T) {
	t.Parallel()
	transport := &countingTransport{fn: respond(200, completedBody)}
	blobs := newFakeBlobStore()
	secrets := newFakeSecrets(testCredentialRef, []byte(testToken))
	a, err := New(contract.AdapterDependencies{HTTP: &http.Client{Transport: transport}, Secrets: secrets, Clock: newFakeClock(), Blobs: blobs},
		bindProfile(t, defaultProfile()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	action := defaultAction(storeContext(t, blobs, defaultContext(t)))

	obs, err := a.Invoke(context.Background(), testDispatch(t, action))
	f := mustFault(t, err, contract.CodeCapabilityUnsupported)
	if !strings.Contains(f.Message, testProtocolRevision) || !strings.Contains(f.Message, "not qualified") || !strings.Contains(f.Message, "no call was made") {
		t.Fatalf("message = %q", f.Message)
	}
	if obs.Disposition != "" {
		t.Fatalf("refusal carried disposition %q", obs.Disposition)
	}
	if transport.count() != 0 || secrets.getCount() != 0 || blobs.stageCount() != 0 {
		t.Fatalf("production refusal did work: calls=%d secret gets=%d stages=%d", transport.count(), secrets.getCount(), blobs.stageCount())
	}

	// The reachable local checks still run first in production.
	action.MaxOutputTokens = 4097
	_, err = a.Invoke(context.Background(), testDispatch(t, action))
	assertFault(t, err, contract.CodeBudgetUnavailable)
}

// ---------- the one physical call ----------

func TestInvokeSendsExactlyOneExactRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, completedBody))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if h.transport.count() != 1 {
		t.Fatalf("physical calls = %d, want exactly 1", h.transport.count())
	}
	req := h.transport.requests[0]
	if req.Method != http.MethodPost || req.URL.String() != testEndpoint {
		t.Fatalf("request = %s %s", req.Method, req.URL)
	}
	if got := req.Header.Get(testCredentialHeader); got != testToken {
		t.Fatalf("credential header = %q", got)
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("protocol header not applied: %v", req.Header)
	}
	if h.transport.getBody[0] {
		t.Fatal("request body is rewindable; net/http could replay it")
	}
	if _, ok := req.Context().Deadline(); !ok {
		t.Fatal("request carries no deadline")
	}

	var sent testWireRequest
	if err := json.Unmarshal(h.transport.bodies[0], &sent); err != nil {
		t.Fatalf("sent body: %v", err)
	}
	if sent.Model != testModel || sent.Limit != 1000 {
		t.Fatalf("sent model/limit = %q/%d, want the profile model and the action ceiling", sent.Model, sent.Limit)
	}
	if len(sent.Turns) != 2 || sent.Turns[0] != (testWireTurn{"system", "You are a careful researcher."}) || sent.Turns[1] != (testWireTurn{"user", "Summarize the source."}) {
		t.Fatalf("sent turns = %+v", sent.Turns)
	}
	if len(sent.Tools) != 1 || sent.Tools[0] != "fetch_source" {
		t.Fatalf("sent tools = %v", sent.Tools)
	}
	if strings.Contains(string(h.transport.bodies[0]), testToken) {
		t.Fatal("credential appeared in the request body")
	}

	ev := decodeEvidence(t, obs)
	pc := ev.PhysicalCall
	if pc.OperationID != h.dispatch.OperationID || pc.AttemptID != h.dispatch.AttemptID {
		t.Fatalf("physical call identity = %s/%s", pc.OperationID, pc.AttemptID)
	}
	if pc.AccountIdentity != "connection:"+string(testConnectionID) {
		t.Fatalf("account_identity = %q", pc.AccountIdentity)
	}
	if pc.RequestedDestination != testEndpoint || pc.ResolvedDestination != testEndpoint {
		t.Fatalf("destinations = %q / %q", pc.RequestedDestination, pc.ResolvedDestination)
	}
	if pc.ProfileDigest != h.adapter.profile.Digest || pc.CapabilityEvidence != testCapabilityArtifact() {
		t.Fatalf("profile binding = %s / %+v", pc.ProfileDigest, pc.CapabilityEvidence)
	}
	if pc.RequestSent != "yes" || pc.Confirmation != "authoritative_success" || pc.HTTPStatus != 200 || pc.ErrorCode != "" {
		t.Fatalf("physical call = %+v", pc)
	}
	if !pc.FinishedAt.After(pc.StartedAt) {
		t.Fatalf("finished %s is not after started %s", pc.FinishedAt, pc.StartedAt)
	}

	// The exact secret-free request record is staged before dispatch and
	// named by request_context (decodeEvidence checks the locator pairing).
	first := ev.StagedOutputs[0]
	if first.StagingRef != pc.RequestContext.StagingRef || first.Classification != "internal" || first.MediaType != "application/json" {
		t.Fatalf("staged request context = %+v", first)
	}
	record := requestRecordOf(t, h, ev)
	if record.Schema != requestRecordSchema || record.Method != http.MethodPost || record.Destination != testEndpoint {
		t.Fatalf("request record = %+v", record)
	}
	if len(record.Headers) != 1 || record.Headers[0].Name != "Content-Type" || record.Headers[0].Values[0] != "application/json" {
		t.Fatalf("request record headers = %+v; only the permitted, secret-free headers belong", record.Headers)
	}
	body, err := base64.StdEncoding.DecodeString(record.BodyBase64)
	if err != nil || string(body) != string(h.transport.bodies[0]) {
		t.Fatalf("request record body is not the body as sent (%v)", err)
	}
	if record.BodySize != int64(len(body)) || record.BodyDigest != contract.Hash(body) {
		t.Fatalf("request record body size/digest = %d / %s", record.BodySize, record.BodyDigest)
	}
	assertNoSecret(t, h, obs, nil)
}

func TestInvokeRecordsCompletedOutputAndObservedUsage(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, completedBody))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	// Observation.ProviderReference is the session handle (the
	// reconciliation key, echoed from the action's own session_handle),
	// not the response id: revision 3 (P00-009) keys Reconcile by the
	// session a prepare_session minted, and this model_step's evidence
	// still carries its own response id separately.
	if obs.Disposition != contract.DispositionSucceeded || obs.ProviderReference != testSessionHandle {
		t.Fatalf("observation = %q / %q", obs.Disposition, obs.ProviderReference)
	}
	ev := decodeEvidence(t, obs)
	if obs.ConfirmedAt == nil || !obs.ConfirmedAt.Equal(ev.PhysicalCall.FinishedAt) {
		t.Fatalf("ConfirmedAt = %v", obs.ConfirmedAt)
	}
	if ev.SessionHandle != testSessionHandle {
		t.Fatalf("evidence session_handle = %q", ev.SessionHandle)
	}
	if ev.ResponseID != "resp-001" || ev.Output.ResponseID != "resp-001" || ev.PhysicalCall.ProviderReference != "resp-001" {
		t.Fatalf("response ids = %q / %q / %q", ev.ResponseID, ev.Output.ResponseID, ev.PhysicalCall.ProviderReference)
	}
	if ev.Output.Schema != "zatiti.model-output/v1" || ev.Output.FinishReason != "completed" {
		t.Fatalf("output = %+v", ev.Output)
	}
	if len(ev.OutputArtifacts) != 0 || len(ev.Output.ToolProposals) != 0 {
		t.Fatalf("adapter minted artifacts or proposals: %+v / %+v", ev.OutputArtifacts, ev.Output.ToolProposals)
	}

	// request, raw provider response, then one model_text per text output;
	// every staged locator matches exactly one StagedOutput.
	if len(ev.StagedOutputs) != 4 || ev.StagedOutputs[1].Purpose != "provider_response" {
		t.Fatalf("staged outputs = %+v", ev.StagedOutputs)
	}
	if string(h.blobs.stagedBytes(t, ev.StagedOutputs[1].StagingRef)) != completedBody {
		t.Fatal("staged provider_response is not the exact provider body")
	}
	wantTexts := []string{"A cited brief.", "A second part."}
	if len(ev.Output.TextOutputs) != len(wantTexts) {
		t.Fatalf("text_outputs = %+v", ev.Output.TextOutputs)
	}
	for i, loc := range ev.Output.TextOutputs {
		matches := 0
		for _, s := range ev.StagedOutputs {
			if s.StagingRef == loc.StagingRef && s.Digest == loc.Digest {
				matches++
				if s.Purpose != "model_text" || s.MediaType != textMediaType || s.Classification != "internal" {
					t.Fatalf("model text staged as %+v", s)
				}
			}
		}
		if loc.Kind != "staged" || matches != 1 {
			t.Fatalf("locator %+v matches %d staged outputs", loc, matches)
		}
		if got := string(h.blobs.stagedBytes(t, loc.StagingRef)); got != wantTexts[i] {
			t.Fatalf("text output %d = %q, want %q", i, got, wantTexts[i])
		}
	}

	// 120 input tokens * 2 + ceil(3 output tokens * 7/2) = 240 + 11.
	u := ev.Output.Usage
	if u.Billing != "observed" || u.Accounting != (wireUsage{Currency: "USD", Spent: 251}) {
		t.Fatalf("usage = %+v", u)
	}
	if u.InputTokens == nil || *u.InputTokens != 120 || u.OutputTokens == nil || *u.OutputTokens != 3 {
		t.Fatalf("token counts = %v / %v", u.InputTokens, u.OutputTokens)
	}
	if u.InputRate == nil || *u.InputRate != h.adapter.profile.InputRate || u.OutputRate == nil || *u.OutputRate != h.adapter.profile.OutputRate {
		t.Fatalf("usage rates = %+v / %+v, want the profile's", u.InputRate, u.OutputRate)
	}
	if u.ProviderUsageReference != "usage-77" {
		t.Fatalf("usage reference = %q", u.ProviderUsageReference)
	}
}

// ---------- usage is observed, or uncertain, never silently estimated ----------

func TestUsageAccounting(t *testing.T) {
	t.Parallel()
	advisory := withProfile(func(p *wireResponsesProfile) { p.Enforcement.Cost = enforcementAdvisory })
	cases := []struct {
		name        string
		opts        []harnessOption
		status      int
		body        string
		disposition string
		billing     string
		spent       int64
		unknownAll  bool // accounting.unknown must equal the admitted worst case
		advisory    bool
	}{
		{"success without a usage report keeps the whole reservation unknown", nil, 200,
			`{"ref":"r","state":"completed","finish":"completed","texts":["x"]}`, contract.DispositionSucceeded, "unknown", 0, true, false},
		{"advisory profile reports absent usage as advisory", []harnessOption{advisory}, 200,
			`{"ref":"r","state":"completed","finish":"completed"}`, contract.DispositionSucceeded, "advisory", 0, true, true},
		{"advisory profile flags observed usage advisory", []harnessOption{advisory}, 200,
			`{"ref":"r","state":"completed","finish":"completed","tokens":{"in":10,"out":2}}`, contract.DispositionSucceeded, "observed", 27, false, true},
		{"provider rejection without a usage report is not free", nil, 400,
			`{"error":{"code":"bad","message":"nope"}}`, contract.DispositionFailed, "unknown", 0, true, false},
		{"provider rejection the qualified contract says is unbilled", nil, 401,
			`{"no_charge":true,"error":{"code":"auth","message":"denied"}}`, contract.DispositionFailed, "no_charge", 0, false, false},
		{"provider failure that still reports usage", nil, 200,
			`{"ref":"r","state":"failed","tokens":{"in":50,"out":0},"error":{"code":"model_error","message":"boom"}}`, contract.DispositionFailed, "observed", 100, false, false},
		{"zero reported tokens are an observed zero, not an unknown", nil, 200,
			`{"ref":"r","state":"completed","finish":"completed","tokens":{"in":0,"out":0}}`, contract.DispositionSucceeded, "observed", 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, respond(tc.status, tc.body), tc.opts...)
			obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if obs.Disposition != tc.disposition {
				t.Fatalf("disposition = %q, want %q", obs.Disposition, tc.disposition)
			}
			u := decodeEvidence(t, obs).Output.Usage
			want := wireUsage{Currency: "USD", Spent: tc.spent, Advisory: tc.advisory}
			if tc.unknownAll {
				want.Unknown = defaultWorstCase(h)
			}
			if u.Billing != tc.billing || u.Accounting != want {
				t.Fatalf("usage = %s %+v, want %s %+v", u.Billing, u.Accounting, tc.billing, want)
			}
			if tc.billing != "observed" && (u.InputTokens != nil || u.OutputTokens != nil) {
				t.Fatalf("token counts invented: %v / %v", u.InputTokens, u.OutputTokens)
			}
		})
	}
}

func TestReportedUsageThatCannotBePricedStaysUnknown(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, `{"ref":"r","state":"completed","finish":"completed","tokens":{"in":9223372036854775807,"out":1}}`))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	u := decodeEvidence(t, obs).Output.Usage
	// The tokens are recorded exactly as reported; the amount is not
	// clamped to something representable.
	if u.Billing != "unknown" || u.Accounting.Spent != 0 || u.Accounting.Unknown != defaultWorstCase(h) {
		t.Fatalf("usage = %+v", u)
	}
	if u.InputTokens == nil || *u.InputTokens != 9223372036854775807 || u.OutputTokens == nil || *u.OutputTokens != 1 {
		t.Fatalf("token counts = %v / %v", u.InputTokens, u.OutputTokens)
	}
}

// ---------- lost responses are unknown, never failed ----------

type timeoutError struct{}

func (timeoutError) Error() string   { return "synthetic: i/o timeout awaiting response" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestTransportFailureClassification(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		err          error
		disposition  string
		requestSent  string
		confirmation string
		billing      string
	}{
		{"dns failure never left", &net.DNSError{Err: "no such host", Name: "models.example.test", IsNotFound: true},
			contract.DispositionNotSent, "no", "authoritative_nonexecution", "no_charge"},
		{"dial failure never left", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
			contract.DispositionNotSent, "no", "authoritative_nonexecution", "no_charge"},
		{"timeout after the request was written", &net.OpError{Op: "read", Net: "tcp", Err: timeoutError{}},
			contract.DispositionUnknown, "unknown", "unknown", "unknown"},
		{"connection reset", &net.OpError{Op: "write", Net: "tcp", Err: errors.New("connection reset by peer")},
			contract.DispositionUnknown, "unknown", "unknown", "unknown"},
		{"unclassified transport error", errors.New("unexpected EOF"),
			contract.DispositionUnknown, "unknown", "unknown", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(*http.Request) (*http.Response, error) { return nil, tc.err })
			obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
			if err != nil {
				t.Fatalf("Invoke returned an error for an attempted call: %v", err)
			}
			if h.transport.count() != 1 {
				t.Fatalf("physical calls = %d, want exactly 1 (no hidden retry)", h.transport.count())
			}
			if obs.Disposition != tc.disposition || obs.ConfirmedAt != nil {
				t.Fatalf("disposition = %q confirmed %v, want %q unconfirmed", obs.Disposition, obs.ConfirmedAt, tc.disposition)
			}
			ev := decodeEvidence(t, obs)
			pc := ev.PhysicalCall
			if pc.RequestSent != tc.requestSent || pc.Confirmation != tc.confirmation || pc.HTTPStatus != 0 || pc.ErrorCode != "transport_error" {
				t.Fatalf("physical call = %+v", pc)
			}
			if ev.Output.FinishReason != "unknown" {
				t.Fatalf("finish_reason = %q", ev.Output.FinishReason)
			}
			u := ev.Output.Usage
			want := wireUsage{Currency: "USD"}
			if tc.billing == "unknown" {
				want.Unknown = defaultWorstCase(h) // the reservation is kept
			}
			if u.Billing != tc.billing || u.Accounting != want {
				t.Fatalf("usage = %s %+v, want %s %+v", u.Billing, u.Accounting, tc.billing, want)
			}
		})
	}
}

func TestReceivedButUnclassifiableResponsesStayUnknown(t *testing.T) {
	t.Parallel()
	small := withProfile(func(p *wireResponsesProfile) { p.MaxResponseBytes = 16 })
	cases := []struct {
		name   string
		opts   []harnessOption
		status int
		body   string
		code   string
	}{
		{"undecodable success body", nil, 200, `<html>gateway</html>`, "response_undecodable"},
		{"unknown provider state", nil, 200, `{"ref":"r","state":"pending-ish"}`, "response_undecodable"},
		{"unknown finish reason", nil, 200, `{"ref":"r","state":"completed","finish":"vibes"}`, "response_undecodable"},
		{"negative usage", nil, 200, `{"ref":"r","state":"completed","finish":"completed","tokens":{"in":-1,"out":1}}`, "response_undecodable"},
		{"oversized reference", nil, 200, `{"ref":"` + strings.Repeat("r", 1025) + `","state":"completed","finish":"completed"}`, "response_undecodable"},
		{"more text outputs than evidence can carry", nil, 200, `{"ref":"r","state":"completed","finish":"completed","texts":[` + strings.Repeat(`"t",`, 254) + `"t"]}`, "response_undecodable"},
		{"body beyond max_response_bytes", []harnessOption{small}, 200, completedBody, "max_response_bytes_exceeded"},
		{"server error cannot rule out execution", nil, 503, `{"error":{"code":"overloaded","message":"try later"}}`, "http_503"},
		{"gateway timeout cannot rule out execution", nil, 504, `upstream timed out`, "http_504"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, respond(tc.status, tc.body), tc.opts...)
			obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if h.transport.count() != 1 {
				t.Fatalf("physical calls = %d, want exactly 1", h.transport.count())
			}
			if obs.Disposition != contract.DispositionUnknown || obs.ConfirmedAt != nil {
				t.Fatalf("disposition = %q confirmed %v", obs.Disposition, obs.ConfirmedAt)
			}
			ev := decodeEvidence(t, obs)
			pc := ev.PhysicalCall
			if pc.RequestSent != "yes" || pc.Confirmation != "unknown" || pc.ErrorCode != tc.code || pc.HTTPStatus != int64(tc.status) {
				t.Fatalf("physical call = %+v", pc)
			}
			u := ev.Output.Usage
			if u.Billing != "unknown" || u.Accounting.Unknown != defaultWorstCase(h) || u.Accounting.Spent != 0 {
				t.Fatalf("usage = %+v", u)
			}
			if len(ev.Output.TextOutputs) != 0 {
				t.Fatalf("text outputs recorded for an unclassifiable response: %+v", ev.Output.TextOutputs)
			}
		})
	}
}

// brokenBody delivers part of a body and then fails, as a connection that
// times out after a success status does.
type brokenBody struct {
	data *strings.Reader
}

func (b *brokenBody) Read(p []byte) (int, error) {
	if b.data.Len() == 0 {
		return 0, timeoutError{}
	}
	return b.data.Read(p)
}

func (b *brokenBody) Close() error { return nil }

func TestTimeoutAfterSuccessStatusStaysUnknown(t *testing.T) {
	t.Parallel()
	partial := `{"ref":"resp-001","state":"completed","finish":"comp`
	h := newHarness(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: &brokenBody{data: strings.NewReader(partial)}}, nil
	})
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if h.transport.count() != 1 || obs.Disposition != contract.DispositionUnknown || obs.ConfirmedAt != nil {
		t.Fatalf("calls %d, disposition %q, confirmed %v", h.transport.count(), obs.Disposition, obs.ConfirmedAt)
	}
	ev := decodeEvidence(t, obs)
	pc := ev.PhysicalCall
	if pc.HTTPStatus != 200 || pc.RequestSent != "yes" || pc.Confirmation != "unknown" || pc.ErrorCode != "response_read_failed" {
		t.Fatalf("physical call = %+v", pc)
	}
	if u := ev.Output.Usage; u.Billing != "unknown" || u.Accounting.Unknown != defaultWorstCase(h) {
		t.Fatalf("reservation not kept: %+v", u)
	}
	// What did arrive is kept as evidence, under a generic media type.
	got := ev.StagedOutputs[1]
	if got.Purpose != "provider_response" || got.MediaType != "application/octet-stream" || string(h.blobs.stagedBytes(t, got.StagingRef)) != partial {
		t.Fatalf("partial body staged as %+v", got)
	}
}

func TestOversizedResponseStagesOnlyTheBoundedPrefix(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, completedBody), withProfile(func(p *wireResponsesProfile) { p.MaxResponseBytes = 16 }))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	ev := decodeEvidence(t, obs)
	if len(ev.StagedOutputs) != 2 || ev.StagedOutputs[1].Size != 16 {
		t.Fatalf("staged outputs = %+v", ev.StagedOutputs)
	}
	if got := string(h.blobs.stagedBytes(t, ev.StagedOutputs[1].StagingRef)); got != completedBody[:16] {
		t.Fatalf("staged prefix = %q", got)
	}
}

// ---------- authoritative provider outcomes ----------

func TestProviderOutcomes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		status       int
		body         string
		disposition  string
		confirmation string
		errorCode    string
		finish       string
		confirmed    bool
	}{
		{"client error is an authoritative failure", 422, `{"error":{"code":"invalid","message":"bad turn"}}`,
			contract.DispositionFailed, "authoritative_failure", "http_422", "failed", true},
		{"client error with an undecodable body", 403, `forbidden`,
			contract.DispositionFailed, "authoritative_failure", "http_403", "failed", true},
		{"provider reports the step failed", 200, `{"ref":"r9","state":"failed","error":{"code":"model_error","message":"boom"}}`,
			contract.DispositionFailed, "authoritative_failure", "model_error", "failed", true},
		{"provider acceptance is not completion", 202, `{"ref":"r9","state":"accepted"}`,
			contract.DispositionAccepted, "provider_accepted", "", "unknown", false},
		{"length limit is a completed step", 200, `{"ref":"r9","state":"completed","finish":"length_limit","texts":["cut"],"tokens":{"in":1,"out":1}}`,
			contract.DispositionSucceeded, "authoritative_success", "", "length_limit", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, respond(tc.status, tc.body))
			obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if h.transport.count() != 1 {
				t.Fatalf("physical calls = %d", h.transport.count())
			}
			if obs.Disposition != tc.disposition || (obs.ConfirmedAt != nil) != tc.confirmed {
				t.Fatalf("disposition = %q confirmed=%v", obs.Disposition, obs.ConfirmedAt != nil)
			}
			ev := decodeEvidence(t, obs)
			if ev.PhysicalCall.Confirmation != tc.confirmation || ev.PhysicalCall.ErrorCode != tc.errorCode || ev.Output.FinishReason != tc.finish {
				t.Fatalf("confirmation/error/finish = %q/%q/%q", ev.PhysicalCall.Confirmation, ev.PhysicalCall.ErrorCode, ev.Output.FinishReason)
			}
		})
	}
}

func TestRefusalAndContinuationAreCarried(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, `{"ref":"r2","state":"completed","finish":"refused","refusal":"cannot help","resume":"next-7","tokens":{"in":1,"out":1}}`),
		withAction(func(a *wireResponsesParameters) { a.ContinuationReference = "prev-6" }))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var sent testWireRequest
	if err := json.Unmarshal(h.transport.bodies[0], &sent); err != nil || sent.Resume != "prev-6" {
		t.Fatalf("continuation not sent: %+v (%v)", sent, err)
	}
	out := decodeEvidence(t, obs).Output
	if out.FinishReason != "refused" || out.Refusal != "cannot help" || out.ContinuationReference != "next-7" {
		t.Fatalf("output = %+v", out)
	}
}

// A model tool proposal is an observation. The adapter never executes one,
// and -- because the frozen contract gives it no tool-to-operation mapping
// -- never fabricates a typed one either.
func TestToolCallsAreNeverExecutedNorFabricated(t *testing.T) {
	t.Parallel()
	body := `{"ref":"r3","state":"completed","finish":"tool_calls","calls":[{"id":"c1","name":"fetch_source","args":{"url":"https://sources.example.test/a"}}],"tokens":{"in":5,"out":5}}`
	h := newHarness(t, respond(200, body))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if h.transport.count() != 1 {
		t.Fatalf("physical calls = %d; a tool call must not trigger another request", h.transport.count())
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("disposition = %q", obs.Disposition)
	}
	ev := decodeEvidence(t, obs)
	if ev.Output.FinishReason != "tool_calls" || len(ev.Output.ToolProposals) != 0 {
		t.Fatalf("output = %+v", ev.Output)
	}
	if ev.PhysicalCall.ErrorCode != "tool_proposal_mapping_unspecified" || !strings.Contains(ev.PhysicalCall.ErrorMessage, "1 tool call(s)") {
		t.Fatalf("gap not flagged: %+v", ev.PhysicalCall)
	}
	// Nothing is lost: the raw calls remain in the staged provider body.
	if got := string(h.blobs.stagedBytes(t, ev.StagedOutputs[1].StagingRef)); got != body {
		t.Fatalf("staged provider_response = %q", got)
	}
	if ev.Output.Usage.Billing != "observed" || ev.Output.Usage.Accounting.Spent != 28 {
		t.Fatalf("usage = %+v", ev.Output.Usage)
	}
}

// ---------- hard caps are enforced before sending, or refused ----------

func TestHardCapRefusalsMakeNoCall(t *testing.T) {
	t.Parallel()
	noBound := func(p *testProtocol) { p.inputBound = nil }
	cases := []struct {
		name string
		opts []harnessOption
		code string
		want string
	}{
		{"protocol cannot enforce an output ceiling", []harnessOption{withProtocol(func(p *testProtocol) { p.lim.BoundsOutputTokens = false })},
			contract.CodeCapabilityUnsupported, "output token ceiling"},
		{"protocol cannot bound input tokens", []harnessOption{withProtocol(noBound)},
			contract.CodeCapabilityUnsupported, "input tokens"},
		{"request exceeds max_input_tokens", []harnessOption{withProfile(func(p *wireResponsesProfile) { p.MaxInputTokens = 10 })},
			contract.CodeBudgetUnavailable, "max_input_tokens"},
		{"worst case exceeds maximum_cost", []harnessOption{withProfile(func(p *wireResponsesProfile) { p.Enforcement.MaximumCost.MicroUnits = 3000 })},
			contract.CodeBudgetUnavailable, "maximum_cost"},
		{"continuation the protocol cannot express", []harnessOption{
			withProtocol(func(p *testProtocol) { p.lim.SupportsContinuation = false }),
			withAction(func(a *wireResponsesParameters) { a.ContinuationReference = "prev-1" })},
			contract.CodeCapabilityUnsupported, "continuation_reference"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, respond(200, completedBody), tc.opts...)
			_, err := h.adapter.Invoke(context.Background(), h.dispatch)
			f := mustFault(t, err, tc.code)
			if !strings.Contains(f.Message, tc.want) {
				t.Fatalf("message %q does not mention %q", f.Message, tc.want)
			}
			if h.transport.count() != 0 || h.secrets.getCount() != 0 || h.blobs.stageCount() != 0 {
				t.Fatalf("refusal did work: calls=%d secret gets=%d stages=%d", h.transport.count(), h.secrets.getCount(), h.blobs.stageCount())
			}
		})
	}
}

func TestUnexpressibleStepIsRefusedBeforeAnyStagingOrCall(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, completedBody), withProtocol(func(p *testProtocol) { p.encodeErr = errors.New("artifact parts are not qualified") }))
	_, err := h.adapter.Invoke(context.Background(), h.dispatch)
	f := mustFault(t, err, contract.CodeCapabilityUnsupported)
	if !strings.Contains(f.Message, "artifact parts are not qualified") || h.transport.count() != 0 || h.blobs.stageCount() != 0 {
		t.Fatalf("message %q, calls %d, stages %d", f.Message, h.transport.count(), h.blobs.stageCount())
	}
}

func TestOutputCeilingBelowTheProtocolMinimumIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, completedBody), withProtocol(func(p *testProtocol) { p.lim.MinOutputTokens = 16 }),
		withAction(func(a *wireResponsesParameters) { a.MaxOutputTokens = 15 }))
	_, err := h.adapter.Invoke(context.Background(), h.dispatch)
	f := mustFault(t, err, contract.CodeCapabilityUnsupported)
	if !strings.Contains(f.Message, "16-token minimum") || h.transport.count() != 0 || h.secrets.getCount() != 0 {
		t.Fatalf("message %q, calls %d, secret gets %d", f.Message, h.transport.count(), h.secrets.getCount())
	}
}

func TestAdvisoryCostModeSendsWithoutProvableBoundsAndSaysSo(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, `{"ref":"r","state":"completed","finish":"completed"}`),
		withProfile(func(p *wireResponsesProfile) { p.Enforcement.Cost = enforcementAdvisory }),
		withProtocol(func(p *testProtocol) { p.inputBound = nil; p.lim.BoundsOutputTokens = false }))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	u := decodeEvidence(t, obs).Output.Usage
	// With no provable input bound the profile maximum is priced instead:
	// 100000 * 2 + 1000 * 7/2.
	if u.Billing != "advisory" || u.Accounting != (wireUsage{Currency: "USD", Unknown: 203500, Advisory: true}) {
		t.Fatalf("usage = %+v", u)
	}
}

// ---------- credentials ----------

func TestCredentialFailuresMakeNoCall(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*harness)
		code string
	}{
		{"empty credential_ref", func(h *harness) { h.dispatch.CredentialRef = "" }, contract.CodeInvalidInput},
		{"unknown credential_ref", func(h *harness) { h.dispatch.CredentialRef = "cred-missing" }, contract.CodeNotFound},
		{"empty secret material", func(h *harness) { h.secrets.values[testCredentialRef] = nil }, contract.CodePrerequisiteMissing},
		{"no secret store", func(h *harness) { h.adapter.deps.Secrets = nil }, contract.CodePrerequisiteMissing},
		{"opaque secret store failure", func(h *harness) { h.secrets.getErr = errors.New("keychain locked: " + testToken) }, contract.CodeInternalError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, respond(200, completedBody))
			tc.edit(h)
			obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
			assertFault(t, err, tc.code)
			if h.transport.count() != 0 {
				t.Fatalf("physical calls = %d", h.transport.count())
			}
			assertNoSecret(t, h, obs, err)
		})
	}
}

func TestSecretIsScrubbedFromEveryDiagnostic(t *testing.T) {
	t.Parallel()

	t.Run("provider body echoing the credential", func(t *testing.T) {
		t.Parallel()
		body := fmt.Sprintf(`{"ref":"id-%s","state":"completed","finish":"refused","texts":["echo %s"],"refusal":"saw %s","resume":"next-%s","usage_ref":"u-%s","tokens":{"in":1,"out":1}}`,
			testToken, testToken, testToken, testToken, testToken)
		h := newHarness(t, respond(200, body))
		obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		assertNoSecret(t, h, obs, nil)
		ev := decodeEvidence(t, obs)
		// obs.ProviderReference is the session handle (never secret-derived
		// here); the response id the credential leaked into is scrubbed on
		// the physical call's own provider_reference instead.
		if ev.Output.Refusal != "saw [redacted]" || ev.PhysicalCall.ProviderReference != "id-[redacted]" || obs.ProviderReference != testSessionHandle {
			t.Fatalf("scrubbed fields = %q / %q / %q", ev.Output.Refusal, ev.PhysicalCall.ProviderReference, obs.ProviderReference)
		}
	})

	t.Run("provider error message with control characters", func(t *testing.T) {
		t.Parallel()
		body := fmt.Sprintf(`{"error":{"code":"auth","message":"bad key %s\u0000\u001b[31m here\nnext"}}`, testToken)
		h := newHarness(t, respond(401, body))
		obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		assertNoSecret(t, h, obs, nil)
		if got := decodeEvidence(t, obs).PhysicalCall.ErrorMessage; got != "bad key [redacted][31m here\nnext" {
			t.Fatalf("error_message = %q", got)
		}
	})

	t.Run("transport error quoting the credential", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("proxy rejected header %s: %s", testCredentialHeader, testToken)
		})
		obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		assertNoSecret(t, h, obs, nil)
		if !strings.Contains(decodeEvidence(t, obs).PhysicalCall.ErrorMessage, redactedPlaceholder) {
			t.Fatal("transport error message was dropped instead of scrubbed")
		}
	})

	t.Run("long provider message is bounded", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, respond(400, `{"error":{"code":"x","message":"`+strings.Repeat("é", 5000)+`"}}`))
		obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if got := decodeEvidence(t, obs).PhysicalCall.ErrorMessage; got != strings.Repeat("é", 2048) {
			t.Fatalf("error_message has %d bytes", len(got))
		}
	})
}

// ---------- staging ----------

// The request record must be secret-free. A context that itself quotes the
// credential would put it in the body, so nothing is staged and nothing is
// sent.
func TestRequestRecordContainingTheCredentialIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, completedBody), withContext(func(c *wireContextArtifact) {
		c.Messages[1].Parts = []json.RawMessage{textPart(t, "my key is "+testToken)}
	}))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	f := mustFault(t, err, contract.CodeInternalError)
	if !strings.Contains(f.Message, "nothing was staged or sent") {
		t.Fatalf("message = %q", f.Message)
	}
	if h.transport.count() != 0 || h.blobs.stageCount() != 0 {
		t.Fatalf("calls = %d, stages = %d", h.transport.count(), h.blobs.stageCount())
	}
	assertNoSecret(t, h, obs, err)
}

// 254 text outputs plus the request context and the provider response fill
// staged_outputs to its 256-item bound exactly.
func TestMaximumTextOutputsStillFitTheEvidenceSchema(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, `{"ref":"r","state":"completed","finish":"completed","texts":[`+strings.Repeat(`"t",`, 253)+`"t"]}`))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	ev := decodeEvidence(t, obs)
	if obs.Disposition != contract.DispositionSucceeded || len(ev.StagedOutputs) != 256 || len(ev.Output.TextOutputs) != 254 {
		t.Fatalf("disposition %q, staged %d, texts %d", obs.Disposition, len(ev.StagedOutputs), len(ev.Output.TextOutputs))
	}
}

func TestRequestStagingFailureMakesNoCall(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, completedBody))
	h.blobs.failStageAt = 1
	_, err := h.adapter.Invoke(context.Background(), h.dispatch)
	f := mustFault(t, err, contract.CodeArtifactFault)
	if strings.Contains(f.Message, "/private/path") {
		t.Fatalf("blob store internals leaked: %q", f.Message)
	}
	if h.transport.count() != 0 {
		t.Fatalf("physical calls = %d; the request context must be persisted before dispatch", h.transport.count())
	}
}

func TestStagingFailureAfterTheCallNeverErasesTheObservation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		failAt    int
		errorCode string
		texts     int
	}{
		{"raw provider response", 2, "provider_response_staging_failed", 2},
		{"one model text", 3, "model_text_staging_failed", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, respond(200, completedBody))
			h.blobs.failStageAt = tc.failAt
			obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
			if err != nil {
				t.Fatalf("Invoke discarded a billed observation: %v", err)
			}
			ev := decodeEvidence(t, obs)
			if obs.Disposition != contract.DispositionSucceeded || ev.Output.Usage.Accounting.Spent != 251 {
				t.Fatalf("disposition/usage = %q / %+v", obs.Disposition, ev.Output.Usage)
			}
			if ev.PhysicalCall.ErrorCode != tc.errorCode || len(ev.Output.TextOutputs) != tc.texts {
				t.Fatalf("error_code = %q, text outputs = %d", ev.PhysicalCall.ErrorCode, len(ev.Output.TextOutputs))
			}
		})
	}
}

// ---------- dispatch checks and reconcile ----------

func TestDispatchChecks(t *testing.T) {
	t.Parallel()

	t.Run("wrong adapter", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, respond(200, completedBody))
		h.dispatch.Adapter = "github"
		_, err := h.adapter.Invoke(context.Background(), h.dispatch)
		assertFault(t, err, contract.CodeInvalidInput)
		_, err = h.adapter.Reconcile(context.Background(), h.dispatch)
		assertFault(t, err, contract.CodeInvalidInput)
	})

	t.Run("deadline already passed", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, respond(200, completedBody))
		h.dispatch.Deadline = newFakeClock().now.Add(-1)
		_, err := h.adapter.Invoke(context.Background(), h.dispatch)
		f := mustFault(t, err, contract.CodeInvalidInput)
		if !strings.Contains(f.Message, "deadline") || h.transport.count() != 0 || h.blobs.stageCount() != 0 {
			t.Fatalf("message %q calls %d stages %d", f.Message, h.transport.count(), h.blobs.stageCount())
		}
	})

	t.Run("no dispatch deadline still bounds the call by the profile timeout", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, respond(200, completedBody))
		h.dispatch.Deadline = time.Time{}
		if _, err := h.adapter.Invoke(context.Background(), h.dispatch); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if _, ok := h.transport.requests[0].Context().Deadline(); !ok {
			t.Fatal("request carries no deadline")
		}
	})
}

// ---------- reconcile: the documented lookup, keyed by the provider key ----------

func TestReconcileRequiresTheProviderKeyAndNeverRepeatsTheStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, completedBody))
	obs, err := h.adapter.Reconcile(context.Background(), h.dispatch)
	f := mustFault(t, err, contract.CodePrerequisiteMissing)
	if !strings.Contains(f.Message, "provider_key") || obs.Disposition != "" || h.transport.count() != 0 {
		t.Fatalf("message %q, obs %+v, calls %d", f.Message, obs, h.transport.count())
	}

	noLookup := newHarness(t, respond(200, completedBody), withProtocol(func(p *testProtocol) { p.lim.SupportsReconcile = false }))
	noLookup.dispatch.ProviderKey = "handle-1"
	_, err = noLookup.adapter.Reconcile(context.Background(), noLookup.dispatch)
	f = mustFault(t, err, contract.CodeCapabilityUnsupported)
	if !strings.Contains(f.Message, "remains unknown") || noLookup.transport.count() != 0 {
		t.Fatalf("message %q, calls %d", f.Message, noLookup.transport.count())
	}
}

func TestReconcileOutcomes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		fn          func(*http.Request) (*http.Response, error)
		disposition string
		confirm     string
		code        string
		texts       int
	}{
		{"completed output found", respond(200, `{"ref":"resp-9","state":"completed","finish":"completed","texts":["recovered"]}`),
			contract.DispositionSucceeded, "authoritative_success", "", 1},
		{"nothing found yet", respond(200, `{}`), contract.DispositionUnknown, "unknown", "reconcile_unresolved", 0},
		{"lookup not found never proves nonexecution", respond(404, `{"error":"no such handle"}`), contract.DispositionUnknown, "unknown", "http_404", 0},
		{"lookup server error", respond(503, ``), contract.DispositionUnknown, "unknown", "http_503", 0},
		{"lookup transport failure", func(*http.Request) (*http.Response, error) {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		}, contract.DispositionUnknown, "unknown", "transport_error", 0},
		{"lookup undecodable", respond(200, `<html>`), contract.DispositionUnknown, "unknown", "response_undecodable", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tc.fn, withProfile(func(p *wireResponsesProfile) {
				p.Enforcement.ProviderDestinations = []string{"https://models.example.test"}
			}))
			h.dispatch.ProviderKey = "handle-7"
			obs, err := h.adapter.Reconcile(context.Background(), h.dispatch)
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if h.transport.count() != 1 {
				t.Fatalf("physical calls = %d, want exactly 1", h.transport.count())
			}
			req := h.transport.requests[0]
			if req.Method != http.MethodGet || req.URL.String() != testEndpoint+"/lookup/handle-7" || req.Header.Get(testCredentialHeader) != testToken {
				t.Fatalf("lookup request = %s %s %v", req.Method, req.URL, req.Header)
			}
			if obs.Disposition != tc.disposition || obs.ProviderReference != "handle-7" {
				t.Fatalf("disposition %q reference %q", obs.Disposition, obs.ProviderReference)
			}
			ev := decodeEvidence(t, obs)
			if ev.PhysicalCall.Confirmation != tc.confirm || ev.PhysicalCall.ErrorCode != tc.code || len(ev.Output.TextOutputs) != tc.texts {
				t.Fatalf("physical call %+v, texts %d", ev.PhysicalCall, len(ev.Output.TextOutputs))
			}
			// A lookup never sees usage: the amount stays outstanding either way.
			u := ev.Output.Usage
			if u.Billing != "unknown" || u.Accounting.Unknown == 0 || u.Accounting.Spent != 0 {
				t.Fatalf("usage = %+v", u)
			}
			record := requestRecordOf(t, h, ev)
			if record.Method != http.MethodGet || record.BodySize != 0 {
				t.Fatalf("reconcile request record = %+v, want its own bodiless lookup", record)
			}
		})
	}
}

// ---------- prepare_session and model_step: separately admitted, journaled
// single-call effects (revision 3, P00-009) ----------

// prepareThenStep answers the synthetic prepare resource with a handle and
// the step with body -- used only to prove a model_step Invoke never also
// calls the prepare resource, now that the two are separate actions.
func prepareThenStep(handleStatus int, handleBody, stepBody string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/prepare") {
			return respond(handleStatus, handleBody)(r)
		}
		return respond(200, stepBody)(r)
	}
}

// TestPrepareSessionAndModelStepAreSeparatelyDispatchedSingleCallEffects is
// the P00-009 split end to end: prepare_session and model_step are
// admitted as two separate dispatches, each Invoke performs exactly the one
// physical call its own kind describes, the model_step names the handle a
// prior prepare_session minted, and the two are never chained inside one
// Invoke (a crash between them cannot repeat either call, because there is
// no code path in which one Invoke could ever attempt both).
func TestPrepareSessionAndModelStepAreSeparatelyDispatchedSingleCallEffects(t *testing.T) {
	t.Parallel()
	h := newHarness(t, prepareThenStep(200, `{"handle":"conv-42"}`, completedBody),
		withProtocol(func(p *testProtocol) { p.withPrepare = true }),
		withProfile(func(p *wireResponsesProfile) {
			p.Enforcement.ProviderDestinations = []string{"https://models.example.test"}
		}))

	// 1. prepare_session: exactly one physical call, to the prepare
	// resource only, no context_artifact, no model output.
	prepObs, err := h.adapter.Invoke(context.Background(), testDispatch(t, defaultPrepareSessionAction()))
	if err != nil {
		t.Fatalf("prepare_session Invoke: %v", err)
	}
	if h.transport.count() != 1 || !strings.HasSuffix(h.transport.requests[0].URL.Path, "/prepare") {
		t.Fatalf("physical calls after prepare_session = %d, want exactly 1 to the prepare resource", h.transport.count())
	}
	if prepObs.Disposition != contract.DispositionSucceeded || prepObs.ProviderReference != "conv-42" {
		t.Fatalf("prepare_session disposition %q, provider reference %q", prepObs.Disposition, prepObs.ProviderReference)
	}
	prepEv := decodePrepareSessionEvidence(t, prepObs)
	if prepEv.SessionHandle != "conv-42" || prepEv.PhysicalCall.Confirmation != "authoritative_success" {
		t.Fatalf("prepare_session evidence = %+v", prepEv)
	}
	if len(prepEv.StagedOutputs) != 2 || prepEv.StagedOutputs[0].Purpose != "context" || prepEv.StagedOutputs[1].Purpose != "provider_response" {
		t.Fatalf("prepare_session staged outputs = %+v", prepEv.StagedOutputs)
	}
	if prepObs.Usage == nil {
		t.Fatal("prepare_session observation carries no usage document")
	}
	var prepUsage wireUsage
	if err := json.Unmarshal(prepObs.Usage, &prepUsage); err != nil {
		t.Fatalf("decode prepare_session usage: %v", err)
	}
	if prepUsage != (wireUsage{Currency: "USD"}) {
		t.Fatalf("prepare_session usage = %+v; session creation is never billed", prepUsage)
	}

	// 2. model_step, naming that handle: exactly one MORE physical call
	// (never a second prepare call -- the model_step action carries no
	// mechanism to request one), to the step resource only.
	action := h.action
	action.SessionHandle = prepObs.ProviderReference
	stepObs, err := h.adapter.Invoke(context.Background(), testDispatch(t, action))
	if err != nil {
		t.Fatalf("model_step Invoke: %v", err)
	}
	if h.transport.count() != 2 {
		t.Fatalf("physical calls after model_step = %d, want exactly 2 total (the prepare call is never repeated)", h.transport.count())
	}
	stepReq := h.transport.requests[1]
	if stepReq.URL.String() != testEndpoint {
		t.Fatalf("model_step request went to %q, want the step resource", stepReq.URL)
	}
	var sent testWireRequest
	if err := json.Unmarshal(h.transport.bodies[1], &sent); err != nil || sent.Handle != "conv-42" {
		t.Fatalf("model_step did not carry the handle: %+v (%v)", sent, err)
	}
	if stepObs.Disposition != contract.DispositionSucceeded || stepObs.ProviderReference != "conv-42" {
		t.Fatalf("model_step disposition %q, provider reference %q; the session handle is the reconciliation key", stepObs.Disposition, stepObs.ProviderReference)
	}
	stepEv := decodeEvidence(t, stepObs)
	if stepEv.SessionHandle != "conv-42" || stepEv.Output.Usage.Accounting.Spent != 251 {
		t.Fatalf("model_step evidence session_handle %q, usage %+v", stepEv.SessionHandle, stepEv.Output.Usage)
	}

	// 3. Reconciling the model_step by that same handle never invents
	// usage a lookup cannot see.
	reconcileDispatch := testDispatch(t, action)
	reconcileDispatch.ProviderKey = "conv-42"
	reconcileObs, err := h.adapter.Reconcile(context.Background(), reconcileDispatch)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if h.transport.count() != 3 {
		t.Fatalf("physical calls after Reconcile = %d, want exactly 3 total", h.transport.count())
	}
	reconcileEv := decodeEvidence(t, reconcileObs)
	if reconcileEv.Output.Usage.Billing != "unknown" || reconcileEv.Output.Usage.Accounting.Spent != 0 {
		t.Fatalf("reconcile usage = %+v; a lookup never sees usage and never invents it", reconcileEv.Output.Usage)
	}

	// 4. There is no mechanism by which "a crash between prepare_session
	// and model_step" could make this adapter repeat the prepare_session
	// call: reconciliation is refused for it outright (no documented
	// lookup), so the only way to obtain a session at all is a brand new,
	// deliberately dispatched prepare_session -- a caller decision, never
	// an adapter-internal retry.
	prepareDispatch := testDispatch(t, defaultPrepareSessionAction())
	prepareDispatch.ProviderKey = "conv-42"
	_, err = h.adapter.Reconcile(context.Background(), prepareDispatch)
	f := mustFault(t, err, contract.CodeCapabilityUnsupported)
	if !strings.Contains(f.Message, "no documented authoritative lookup") || h.transport.count() != 3 {
		t.Fatalf("message %q, calls %d; reconciling prepare_session must never call out", f.Message, h.transport.count())
	}
}

// TestPrepareSessionOutcomesAreHonestAndNeverBilled proves prepare_session
// reports its OWN outcome -- not the "model step was never sent" framing
// the pre-split adapter used -- and that session creation is never charged
// on any disposition, including a failure or an unresolved one.
func TestPrepareSessionOutcomesAreHonestAndNeverBilled(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		fn          func(*http.Request) (*http.Response, error)
		disposition string
		confirm     string
		requestSent string
		code        string
	}{
		{"rejected credential is an authoritative failure", respond(401, `{"error":"bad key"}`),
			contract.DispositionFailed, "authoritative_failure", "yes", "http_401"},
		{"undecodable success body cannot rule out creation", respond(200, `{"nothing":true}`),
			contract.DispositionUnknown, "unknown", "yes", "response_undecodable"},
		{"server error cannot rule out creation", respond(503, ``),
			contract.DispositionUnknown, "unknown", "yes", "http_503"},
		{"unclassified transport error cannot rule out creation", func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection reset by peer")
		}, contract.DispositionUnknown, "unknown", "unknown", "transport_error"},
		{"dial failure never left", func(*http.Request) (*http.Response, error) {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		}, contract.DispositionNotSent, "authoritative_nonexecution", "no", "transport_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tc.fn,
				withProtocol(func(p *testProtocol) { p.withPrepare = true }),
				withProfile(func(p *wireResponsesProfile) {
					p.Enforcement.ProviderDestinations = []string{"https://models.example.test"}
				}))
			obs, err := h.adapter.Invoke(context.Background(), testDispatch(t, defaultPrepareSessionAction()))
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if h.transport.count() != 1 {
				t.Fatalf("physical calls = %d, want exactly 1 (no hidden retry)", h.transport.count())
			}
			if obs.Disposition != tc.disposition || obs.ProviderReference != "" {
				t.Fatalf("disposition %q reference %q, want %q with no handle", obs.Disposition, obs.ProviderReference, tc.disposition)
			}
			ev := decodePrepareSessionEvidenceLenient(t, obs)
			pc := ev.PhysicalCall
			if pc.RequestSent != tc.requestSent || pc.Confirmation != tc.confirm || pc.ErrorCode != tc.code {
				t.Fatalf("physical call = %+v, want request_sent %q confirmation %q code %q", pc, tc.requestSent, tc.confirm, tc.code)
			}
			var usage wireUsage
			if err := json.Unmarshal(obs.Usage, &usage); err != nil {
				t.Fatalf("decode usage: %v", err)
			}
			if usage != (wireUsage{Currency: "USD"}) {
				t.Fatalf("usage = %+v; session creation is never billed, on any disposition", usage)
			}
		})
	}
}

func TestPrepareSessionDestinationMustBeDeclared(t *testing.T) {
	t.Parallel()
	// The default profile declares only the endpoint itself, not its
	// sibling prepare resource.
	h := newHarness(t, prepareThenStep(200, `{"handle":"conv-1"}`, completedBody), withProtocol(func(p *testProtocol) { p.withPrepare = true }))
	_, err := h.adapter.Invoke(context.Background(), testDispatch(t, defaultPrepareSessionAction()))
	f := mustFault(t, err, contract.CodePermissionDenied)
	if !strings.Contains(f.Message, "provider_destinations") || h.transport.count() != 0 || h.blobs.stageCount() != 0 {
		t.Fatalf("message %q, calls %d, stages %d", f.Message, h.transport.count(), h.blobs.stageCount())
	}
}

// TestModelStepNeverCallsThePrepareResource proves a model_step Invoke
// makes exactly the one physical call its own action describes, even when
// the qualified protocol also implements a preparatory call for
// prepare_session: the two kinds are dispatched, and therefore invoked,
// independently.
func TestModelStepNeverCallsThePrepareResource(t *testing.T) {
	t.Parallel()
	h := newHarness(t, prepareThenStep(200, `{"handle":"conv-99"}`, completedBody),
		withProtocol(func(p *testProtocol) { p.withPrepare = true }),
		withProfile(func(p *wireResponsesProfile) {
			p.Enforcement.ProviderDestinations = []string{"https://models.example.test"}
		}))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if h.transport.count() != 1 || h.transport.requests[0].URL.String() != testEndpoint {
		t.Fatalf("physical calls = %d to %v, want exactly 1 to the step resource, never the prepare resource",
			h.transport.count(), h.transport.requests)
	}
	if obs.Disposition != contract.DispositionSucceeded || obs.ProviderReference != testSessionHandle {
		t.Fatalf("disposition %q, provider reference %q", obs.Disposition, obs.ProviderReference)
	}
}

// ---------- accounting findings flagged after a success ----------

func TestUnpriceableUsageIsRecordedButNotPriced(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, `{"ref":"r","state":"completed","finish":"completed","tokens":{"in":10,"out":2},"unpriceable":"served at a tier the profile does not price"}`))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	ev := decodeEvidence(t, obs)
	u := ev.Output.Usage
	if obs.Disposition != contract.DispositionSucceeded || u.Billing != "unknown" || u.Accounting.Spent != 0 || u.Accounting.Unknown != defaultWorstCase(h) {
		t.Fatalf("disposition %q usage %+v", obs.Disposition, u)
	}
	if u.InputTokens == nil || *u.InputTokens != 10 || ev.PhysicalCall.ErrorCode != "usage_unpriceable" {
		t.Fatalf("tokens %v, error_code %q", u.InputTokens, ev.PhysicalCall.ErrorCode)
	}
}

func TestReportedInputAboveTheAdmittedBoundIsFlagged(t *testing.T) {
	t.Parallel()
	h := newHarness(t, respond(200, `{"ref":"r","state":"completed","finish":"completed","tokens":{"in":999999,"out":1}}`))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	ev := decodeEvidence(t, obs)
	if obs.Disposition != contract.DispositionSucceeded || ev.PhysicalCall.ErrorCode != "input_token_bound_exceeded" || ev.Output.Usage.Billing != "observed" {
		t.Fatalf("disposition %q, physical %+v, usage %+v", obs.Disposition, ev.PhysicalCall, ev.Output.Usage)
	}
}
