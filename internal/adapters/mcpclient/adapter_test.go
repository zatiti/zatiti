package mcpclient

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func openSession(t *testing.T, a contract.Adapter, timeout time.Duration) (string, contract.Observation) {
	t.Helper()
	dispatch := testDispatch(t, wireOpenSession{
		Schema: "zatiti.mcp.action/v1", Kind: kindOpenSession,
		ClientName: "test-client", ClientVersion: "1.0.0",
	}, timeout)
	obs, err := a.Invoke(t.Context(), dispatch)
	if err != nil {
		t.Fatalf("open_session Invoke returned an error (should have been an Observation): %v", err)
	}
	ev := decodeEvidence(t, obs)
	return ev.SessionHandle, obs
}

func inputSchemaFor(t *testing.T, arguments map[string]any) (json.RawMessage, contract.Digest) {
	t.Helper()
	schema := map[string]any{"type": "object", "additionalProperties": true}
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	canon, err := contract.Canonicalize(raw)
	if err != nil {
		t.Fatalf("canonicalize schema: %v", err)
	}
	return raw, contract.Hash(canon)
}

func TestOpenSession_LegacyServer_ThreeRequests(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	handle, obs := openSession(t, a, 10*time.Second)
	if handle == "" {
		t.Fatalf("expected a non-empty session handle")
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("expected succeeded, got %s; evidence=%s", obs.Disposition, obs.Evidence)
	}

	counts := srv.counts()
	t.Logf("server-observed physical request counts: %+v (total=%d)", counts, srv.total())

	// Empirical confirmation of the open_session contract finding (folded
	// into the frozen contract 2026-09-24, "at most three JSON-RPC
	// messages... the discover probe is never omitted from evidence"):
	// go-sdk v1.7.0's Client.Connect always attempts SEP-2575
	// server/discover first. Against a legacy server (this one) that
	// fails it, Connect falls back to the legacy handshake, for THREE
	// physical requests total, all three recorded in evidence.handshake.
	if counts["server/discover"] != 1 {
		t.Errorf("expected exactly one server/discover attempt (SEP-2575 preflight), got %d", counts["server/discover"])
	}
	if counts["initialize"] != 1 {
		t.Errorf("expected exactly one initialize request, got %d", counts["initialize"])
	}
	if counts["notifications/initialized"] != 1 {
		t.Errorf("expected exactly one notifications/initialized request, got %d", counts["notifications/initialized"])
	}
	if srv.total() != 3 {
		t.Errorf("expected 3 total physical requests for open_session against a legacy server (1 discover + 2 legacy handshake), got %d", srv.total())
	}

	ev := decodeEvidence(t, obs)
	if len(ev.Handshake) != 3 {
		t.Fatalf("expected evidence.handshake to carry all 3 messages, got %d: %+v", len(ev.Handshake), ev.Handshake)
	}
	if ev.Handshake[0].Message != "server/discover" || ev.Handshake[1].Message != "initialize" || ev.Handshake[2].Message != "notifications/initialized" {
		t.Errorf("unexpected handshake messages: %+v", ev.Handshake)
	}
	if ev.ProtocolVersion != "2025-11-25" {
		t.Errorf("expected negotiated protocol_version 2025-11-25 (legacy fallback), got %q", ev.ProtocolVersion)
	}
	if ev.SessionState != "active" {
		t.Errorf("expected session_state active, got %q", ev.SessionState)
	}
}

func TestOpenSession_SEP2575AwareServer_OneRequest(t *testing.T) {
	srv := newSEP2575AwareControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	// This server only ever advertises 2026-07-28 support (see
	// buildControlledServer's doc comment): a profile pinning 2025-11-25
	// would now correctly be refused capability_unsupported by the
	// negotiated-version check, so this test pins what the server actually
	// negotiates to isolate the request-count/handshake-evidence finding.
	profile := buildProfileJSONWithVersion(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none", "2026-07-28")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	_, obs := openSession(t, a, 10*time.Second)
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("expected succeeded, got %s; evidence=%s", obs.Disposition, obs.Evidence)
	}

	counts := srv.counts()
	t.Logf("server-observed physical request counts against a SEP-2575-aware server: %+v (total=%d)", counts, srv.total())

	// Second, sharper edge of the same finding: against a server that DOES
	// support SEP-2575 (the same go-sdk build this adapter itself pins),
	// Connect never sends initialize/notifications/initialized at all --
	// evidence.handshake carries only the single successful discover
	// exchange, and the negotiated protocol version is 2026-07-28.
	if srv.total() != 1 || counts["server/discover"] != 1 {
		t.Errorf("expected exactly one server/discover request and nothing else, got counts=%+v total=%d", counts, srv.total())
	}
	ev := decodeEvidence(t, obs)
	if len(ev.Handshake) != 1 || ev.Handshake[0].Message != "server/discover" {
		t.Errorf("expected evidence.handshake to carry only the successful discover exchange, got %+v", ev.Handshake)
	}
	if ev.ProtocolVersion != "2026-07-28" {
		t.Errorf("expected negotiated protocol_version 2026-07-28, got %q", ev.ProtocolVersion)
	}
}

// TestOpenSession_NegotiatedVersionNotPinned_CapabilityUnsupported proves
// the frozen contract's explicit rule (contracts.md, "MCP client connection
// adapter"): "any other negotiated version fails open_session as
// capability_unsupported." A profile pinning 2025-11-25 against a server
// that only negotiates 2026-07-28 must refuse, not silently accept a
// session at a version the operator never authorized.
func TestOpenSession_NegotiatedVersionNotPinned_CapabilityUnsupported(t *testing.T) {
	srv := newSEP2575AwareControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none") // pins 2025-11-25
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	_, obs := openSession(t, a, 10*time.Second)
	if obs.Disposition != contract.DispositionFailed {
		t.Fatalf("expected failed, got %s; evidence=%s", obs.Disposition, obs.Evidence)
	}
	ev := decodeEvidence(t, obs)
	if ev.PhysicalCall.ErrorCode != "capability_unsupported" {
		t.Errorf("expected error_code capability_unsupported, got %q", ev.PhysicalCall.ErrorCode)
	}
	if ev.PhysicalCall.RequestSent != "yes" {
		t.Errorf("expected request_sent yes (the discover probe genuinely went out), got %q", ev.PhysicalCall.RequestSent)
	}
}

func TestListTools_ExactlyOneRequest(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	handle, _ := openSession(t, a, 10*time.Second)
	before := srv.total()

	dispatch := testDispatch(t, wireListTools{Schema: "zatiti.mcp.action/v1", Kind: kindListTools, SessionHandle: handle}, 10*time.Second)
	obs, err := a.Invoke(t.Context(), dispatch)
	if err != nil {
		t.Fatalf("list_tools Invoke error: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("expected succeeded, got %s; evidence=%s", obs.Disposition, obs.Evidence)
	}
	if got := srv.total() - before; got != 1 {
		t.Errorf("expected exactly one physical request for list_tools, got %d", got)
	}
	if srv.counts()["tools/list"] != 1 {
		t.Errorf("expected exactly one tools/list request, got %d", srv.counts()["tools/list"])
	}

	ev := decodeEvidence(t, obs)
	names := map[string]bool{}
	for _, tool := range ev.Tools {
		names[tool.Name] = true
		if tool.InputSchemaDigest == "" {
			t.Errorf("discovered tool %q carries no input_schema_digest", tool.Name)
		}
	}
	for _, want := range []string{"echo", "stall", "big", "server-requests"} {
		if !names[want] {
			t.Errorf("expected discovered tool %q, got %+v", want, names)
		}
	}
}

func TestCallTool_ExactlyOneRequest_Success(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "bearer")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	handle, _ := openSession(t, a, 10*time.Second)
	before := srv.total()

	schema, digest := inputSchemaFor(t, nil)
	args, _ := json.Marshal(map[string]any{"text": "hello"})
	dispatch := testDispatch(t, wireCallTool{
		Schema: "zatiti.mcp.action/v1", Kind: kindCallTool, SessionHandle: handle,
		Tool: "echo", Arguments: args, InputSchema: schema, InputSchemaDigest: digest, Classification: "public",
	}, 10*time.Second)
	obs, err := a.Invoke(t.Context(), dispatch)
	if err != nil {
		t.Fatalf("call_tool Invoke error: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("expected succeeded, got %s; evidence=%s", obs.Disposition, obs.Evidence)
	}
	if got := srv.total() - before; got != 1 {
		t.Errorf("expected exactly one physical request for call_tool, got %d", got)
	}
	if srv.counts()["tools/call"] != 1 {
		t.Errorf("expected exactly one tools/call request, got %d", srv.counts()["tools/call"])
	}

	ev := decodeEvidence(t, obs)
	if ev.IsError {
		t.Errorf("expected is_error false")
	}
	if ev.ContentSummary == nil || ev.ContentSummary.Text != 1 {
		t.Errorf("expected content_summary.text=1, got %+v", ev.ContentSummary)
	}

	// Credential-byte absence: the raw token never appears in anything
	// staged, only in the real Authorization header the server actually
	// received.
	for _, doc := range blobs.stagedDocs() {
		if bytes.Contains(doc, []byte(testToken)) {
			t.Fatalf("staged document contains the raw credential: %s", doc)
		}
	}
	if bytes.Contains(obs.Evidence, []byte(testToken)) {
		t.Fatalf("evidence document contains the raw credential")
	}
}

func TestCallTool_ArgumentsMismatchPinnedSchema_ZeroRequests(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	handle, _ := openSession(t, a, 10*time.Second)
	before := srv.total()

	// A pinned schema that REQUIRES a field the actual arguments omit: a
	// looser-but-not-pinned-schema mismatch must refuse invalid_input with
	// zero additional physical requests.
	schemaMap := map[string]any{
		"type": "object", "additionalProperties": true,
		"required": []string{"must_be_present"},
	}
	schema, _ := json.Marshal(schemaMap)
	canon, _ := contract.Canonicalize(schema)
	digest := contract.Hash(canon)
	args, _ := json.Marshal(map[string]any{"text": "hello"})

	dispatch := testDispatch(t, wireCallTool{
		Schema: "zatiti.mcp.action/v1", Kind: kindCallTool, SessionHandle: handle,
		Tool: "echo", Arguments: args, InputSchema: schema, InputSchemaDigest: digest, Classification: "public",
	}, 10*time.Second)
	_, err := a.Invoke(t.Context(), dispatch)
	if err == nil {
		t.Fatalf("expected an error (never attempted), got a nil error")
	}
	f := mustFault(t, err)
	if f.Code != contract.CodeInvalidInput {
		t.Errorf("expected invalid_input, got %s: %s", f.Code, f.Message)
	}
	if got := srv.total() - before; got != 0 {
		t.Errorf("expected zero additional physical requests, got %d", got)
	}
}

func TestCallTool_Stall_LostResponse_Unknown(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"stall"}, []string{"public"}, "none")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	handle, _ := openSession(t, a, 10*time.Second)

	schema, digest := inputSchemaFor(t, nil)
	args, _ := json.Marshal(map[string]any{})
	dispatch := testDispatch(t, wireCallTool{
		Schema: "zatiti.mcp.action/v1", Kind: kindCallTool, SessionHandle: handle,
		Tool: "stall", Arguments: args, InputSchema: schema, InputSchemaDigest: digest, Classification: "public",
	}, 500*time.Millisecond)

	started := time.Now()
	obs, err := a.Invoke(t.Context(), dispatch)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("stall call_tool should report an Observation (a request was sent), got error: %v", err)
	}
	if obs.Disposition != contract.DispositionUnknown {
		t.Fatalf("expected unknown disposition for a lost response, got %s; evidence=%s", obs.Disposition, obs.Evidence)
	}
	if elapsed > 5*time.Second {
		t.Errorf("expected the call to give up near its deadline, took %s", elapsed)
	}
	ev := decodeEvidence(t, obs)
	if ev.PhysicalCall.RequestSent != "yes" {
		t.Errorf("expected request_sent=yes (the tool call was sent before it stalled), got %q", ev.PhysicalCall.RequestSent)
	}

	// No second Invoke: this test calls Invoke exactly once above. Confirm
	// the server itself saw exactly one tools/call for this attempt.
	if got := srv.counts()["tools/call"]; got != 1 {
		t.Errorf("expected exactly one tools/call request reached the server, got %d", got)
	}
}

func TestCallTool_OversizeResponse_Unknown(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	// A tiny max_response_bytes so the "big" tool's honest, legitimately
	// large response definitively exceeds the bound.
	profile := buildTinyResponseProfile(t, srv.endpoint())
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	handle, _ := openSession(t, a, 10*time.Second)

	schema, digest := inputSchemaFor(t, nil)
	args, _ := json.Marshal(map[string]any{"size": 1 << 20})
	dispatch := testDispatch(t, wireCallTool{
		Schema: "zatiti.mcp.action/v1", Kind: kindCallTool, SessionHandle: handle,
		Tool: "big", Arguments: args, InputSchema: schema, InputSchemaDigest: digest, Classification: "public",
	}, 10*time.Second)
	obs, err := a.Invoke(t.Context(), dispatch)
	if err != nil {
		t.Fatalf("expected an Observation (a request was sent and a response arrived), got error: %v", err)
	}
	if obs.Disposition != contract.DispositionUnknown {
		t.Fatalf("expected unknown disposition for an oversize response, got %s; evidence=%s", obs.Disposition, obs.Evidence)
	}
	ev := decodeEvidence(t, obs)
	if ev.PhysicalCall.ErrorCode != "response_oversize" {
		t.Errorf("expected error_code response_oversize, got %q", ev.PhysicalCall.ErrorCode)
	}
}

func TestOpenSession_Redirect_Refused(t *testing.T) {
	target := alwaysRedirectServer("https://evil.example/elsewhere")
	defer target.Close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, target.URL, true, []string{"echo"}, []string{"public"}, "none")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	dispatch := testDispatch(t, wireOpenSession{
		Schema: "zatiti.mcp.action/v1", Kind: kindOpenSession, ClientName: "test-client", ClientVersion: "1.0.0",
	}, 10*time.Second)
	obs, err := a.Invoke(t.Context(), dispatch)
	if err != nil {
		t.Fatalf("expected an Observation (a request was sent and redirected), got error: %v", err)
	}
	if obs.Disposition != contract.DispositionFailed {
		t.Fatalf("expected failed disposition for a redirect, got %s; evidence=%s", obs.Disposition, obs.Evidence)
	}
	ev := decodeEvidence(t, obs)
	if ev.PhysicalCall.ErrorCode != "redirect_not_permitted" {
		t.Errorf("expected error_code redirect_not_permitted, got %q", ev.PhysicalCall.ErrorCode)
	}
	if !strings.Contains(ev.PhysicalCall.ErrorMessage, "evil.example") {
		t.Errorf("expected the redirect location recorded in error_message, got %q", ev.PhysicalCall.ErrorMessage)
	}
}

func TestCallTool_ServerRequestsRefused_ResultStillHonest(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"server-requests"}, []string{"public"}, "none")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	handle, _ := openSession(t, a, 10*time.Second)

	schema, digest := inputSchemaFor(t, nil)
	args, _ := json.Marshal(map[string]any{})
	dispatch := testDispatch(t, wireCallTool{
		Schema: "zatiti.mcp.action/v1", Kind: kindCallTool, SessionHandle: handle,
		Tool: "server-requests", Arguments: args, InputSchema: schema, InputSchemaDigest: digest, Classification: "public",
	}, 10*time.Second)
	obs, err := a.Invoke(t.Context(), dispatch)
	if err != nil {
		t.Fatalf("call_tool Invoke error: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("expected the tool result to still be recorded honestly despite refused server requests, got %s; evidence=%s", obs.Disposition, obs.Evidence)
	}
	ev := decodeEvidence(t, obs)
	if ev.IsError {
		t.Errorf("expected is_error false")
	}
	want := map[string]bool{"sampling/createMessage": true, "elicitation/create": true, "roots/list": true, "ping": true}
	got := map[string]bool{}
	for _, m := range ev.RefusedServerRequests {
		got[m] = true
	}
	for m := range want {
		if !got[m] {
			t.Errorf("expected %q to be named in refused_server_requests, got %+v", m, ev.RefusedServerRequests)
		}
	}
}

func TestCallTool_ToolNotInAllowedTools_Refused(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	// echo is allowed by the profile, but not "big" -- discovering "big"
	// via list_tools must not itself grant authority to call it.
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	handle, _ := openSession(t, a, 10*time.Second)
	before := srv.total()

	schema, digest := inputSchemaFor(t, nil)
	args, _ := json.Marshal(map[string]any{"size": 10})
	dispatch := testDispatch(t, wireCallTool{
		Schema: "zatiti.mcp.action/v1", Kind: kindCallTool, SessionHandle: handle,
		Tool: "big", Arguments: args, InputSchema: schema, InputSchemaDigest: digest, Classification: "public",
	}, 10*time.Second)
	_, err := a.Invoke(t.Context(), dispatch)
	if err == nil {
		t.Fatalf("expected permission_denied, got success")
	}
	f := mustFault(t, err)
	if f.Code != contract.CodePermissionDenied {
		t.Errorf("expected permission_denied, got %s: %s", f.Code, f.Message)
	}
	if got := srv.total() - before; got != 0 {
		t.Errorf("expected zero additional physical requests, got %d", got)
	}
}

func TestCloseSession_ExactlyOneDelete(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	handle, _ := openSession(t, a, 10*time.Second)
	before := srv.total()

	dispatch := testDispatch(t, wireCloseSession{Schema: "zatiti.mcp.action/v1", Kind: kindCloseSession, SessionHandle: handle}, 10*time.Second)
	obs, err := a.Invoke(t.Context(), dispatch)
	if err != nil {
		t.Fatalf("close_session Invoke error: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("expected succeeded, got %s; evidence=%s", obs.Disposition, obs.Evidence)
	}
	if got := srv.total() - before; got != 1 {
		t.Errorf("expected exactly one physical request (DELETE) for close_session, got %d", got)
	}
	if srv.counts()["DELETE"] != 1 {
		t.Errorf("expected exactly one DELETE, got %d", srv.counts()["DELETE"])
	}

	// The local handle is dropped either way: a second close against the
	// same handle is now unknown.
	dispatch2 := testDispatch(t, wireCloseSession{Schema: "zatiti.mcp.action/v1", Kind: kindCloseSession, SessionHandle: handle}, 10*time.Second)
	_, err2 := a.Invoke(t.Context(), dispatch2)
	if err2 == nil {
		t.Fatalf("expected prerequisite_missing for a handle already closed")
	}
	mustFault(t, err2)
}

func TestCallTool_UnknownSessionHandle_PrerequisiteMissing(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	schema, digest := inputSchemaFor(t, nil)
	args, _ := json.Marshal(map[string]any{"text": "hi"})
	dispatch := testDispatch(t, wireCallTool{
		Schema: "zatiti.mcp.action/v1", Kind: kindCallTool, SessionHandle: "never-opened",
		Tool: "echo", Arguments: args, InputSchema: schema, InputSchemaDigest: digest, Classification: "public",
	}, 10*time.Second)
	_, err := a.Invoke(t.Context(), dispatch)
	if err == nil {
		t.Fatalf("expected prerequisite_missing")
	}
	f := mustFault(t, err)
	if f.Code != contract.CodePrerequisiteMissing {
		t.Errorf("expected prerequisite_missing, got %s: %s", f.Code, f.Message)
	}
	if srv.total() != 0 {
		t.Errorf("expected zero physical requests, got %d", srv.total())
	}
}

func TestReconcile_CapabilityUnsupported(t *testing.T) {
	srv := newControlledServer()
	defer srv.close()

	blobs := newFakeBlobStore()
	profile := buildProfileJSON(t, srv.endpoint(), true, []string{"echo"}, []string{"public"}, "none")
	a := newTestAdapter(t, blobs, newFakeSecrets(testCredentialRef, []byte(testToken)), profile)

	dispatch := testDispatch(t, wireOpenSession{Schema: "zatiti.mcp.action/v1", Kind: kindOpenSession, ClientName: "c", ClientVersion: "1"}, 10*time.Second)
	_, err := a.Reconcile(t.Context(), dispatch)
	if err == nil {
		t.Fatalf("expected capability_unsupported")
	}
	f := mustFault(t, err)
	if f.Code != contract.CodeCapabilityUnsupported {
		t.Errorf("expected capability_unsupported, got %s: %s", f.Code, f.Message)
	}
	if srv.total() != 0 {
		t.Errorf("Reconcile must never send a physical request, got %d", srv.total())
	}
}

// buildTinyResponseProfile is buildProfileJSON with max_response_bytes cut
// to a few hundred bytes, so the "big" tool's honest, legitimately large
// response definitively exceeds the bound.
func buildTinyResponseProfile(t *testing.T, endpoint string) json.RawMessage {
	t.Helper()
	raw := buildProfileJSON(t, endpoint, true, []string{"big"}, []string{"public"}, "none")
	var w wireMCPProfile
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	w.MaxResponseBytes = 512
	stripped, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal stripped profile: %v", err)
	}
	digest, err := profileDigestWithoutCapabilityEvidence(stripped)
	if err != nil {
		t.Fatalf("compute digest: %v", err)
	}
	w.CapabilityEvidence.ProfileDigest = digest
	final, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal final profile: %v", err)
	}
	return final
}
