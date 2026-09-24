package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zatiti/zatiti/internal/contract"
)

// doOpenSession implements the open_session action kind: the frozen
// contract's single declared exception to one-physical-request-per-attempt
// (initialize + notifications/initialized). A successful session is kept
// in this adapter's bounded local table under a freshly minted opaque
// handle; the SDK's own Mcp-Session-Id never leaves this function.
func (a *Adapter) doOpenSession(ctx context.Context, dispatch contract.Dispatch, secret []byte, pinnedAddr string, in *wireOpenSession) (contract.Observation, error) {
	started := a.deps.Clock.Now()
	httpClient, rt := a.newHTTPClient()
	client := mcp.NewClient(&mcp.Implementation{Name: in.ClientName, Version: in.ClientVersion}, &mcp.ClientOptions{
		ElicitationHandler: neverGrantElicitation,
	})
	client.AddReceivingMiddleware(refusalMiddleware(rt))

	state := &callState{kind: kindOpenSession, ctx: ctx, blobs: a.deps.Blobs, secret: secret}
	rt.arm(state)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             a.profile.Endpoint,
		HTTPClient:           httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	session, connErr := client.Connect(ctx, transport, nil)
	rt.disarm()
	finished := a.deps.Clock.Now()

	physical := a.newPhysicalCall(dispatch, pinnedAddr, started)
	physical.FinishedAt = finished
	if _, locator, ok := firstStaged(state.captured); ok {
		physical.RequestContext = locator
	}
	handshake := buildHandshake(state.captured)

	if connErr != nil {
		outcome := classifyAttemptError(connErr, state)
		return a.observeOpenSessionFailure(physical, outcome, handshake, state, finished)
	}

	// The profile's protocol_version is what the server must negotiate
	// (docs/implementation/contracts.md, "MCP client connection adapter"):
	// Client.Connect itself only fails on a version its own SDK build
	// doesn't support at all, so a server that negotiates a version this
	// SDK supports but the operator's profile does not pin still needs an
	// explicit adapter-side check -- the physical handshake already
	// happened (bytes were sent and a session was actually negotiated), so
	// this is an authoritative post-hoc capability refusal, not a
	// pre-flight validation failure.
	if init := session.InitializeResult(); init == nil || init.ProtocolVersion != a.profile.ProtocolVersion {
		negotiated := "unknown"
		if init != nil {
			negotiated = init.ProtocolVersion
		}
		_ = session.Close()
		outcome := attemptOutcome{
			requestSent: "yes", confirmation: "authoritative_failure",
			errorCode:    "capability_unsupported",
			errorMessage: fmt.Sprintf("server negotiated protocol_version %q, profile pins %q", negotiated, a.profile.ProtocolVersion),
		}
		return a.observeOpenSessionFailure(physical, outcome, handshake, state, finished)
	}

	handle, err := a.sessions.add(&sessionEntry{credentialRef: dispatch.CredentialRef, session: session, roundTripper: rt, stateless: session.ID() == ""})
	if err != nil {
		_ = session.Close()
		return contract.Observation{}, err
	}

	physical.RequestSent = "yes"
	physical.Confirmation = "authoritative_success"
	if last := lastStatus(state.captured); last != 0 {
		physical.HTTPStatus = int64(last)
	}

	sessionState := "active"
	if session.ID() == "" {
		sessionState = "stateless"
	}

	ev := wireMCPEvidence{
		PhysicalCall:          physical,
		Kind:                  kindOpenSession,
		SessionHandle:         handle,
		SessionState:          sessionState,
		Handshake:             handshake,
		RefusedServerRequests: state.refused,
		Usage:                 a.attemptUsage(),
	}
	if init := session.InitializeResult(); init != nil {
		ev.ProtocolVersion = init.ProtocolVersion
		if init.ServerInfo != nil {
			ev.ServerName = init.ServerInfo.Name
			ev.ServerVersion = init.ServerInfo.Version
		}
		ev.ServerCapabilities = serverCapabilityNames(init.Capabilities)
	}
	if staged, _, ok := firstStaged(state.captured); ok {
		ev.StagedOutputs = []wireStagedOutput{staged}
	}
	doc, usage, evErr := a.buildSafeEvidence(&ev, state)
	if evErr != nil {
		return contract.Observation{}, evErr
	}
	t := finished
	disposition := contract.DispositionSucceeded
	if ev.PhysicalCall.ErrorCode == "response_redacted" {
		disposition = contract.DispositionFailed
	}
	return contract.Observation{Disposition: disposition, Evidence: doc, Usage: usage, ConfirmedAt: &t}, nil
}

func (a *Adapter) observeOpenSessionFailure(physical wirePhysicalCallEvidence, outcome attemptOutcome, handshake []wireHandshakeExchange, state *callState, finished time.Time) (contract.Observation, error) {
	physical.RequestSent = outcome.requestSent
	physical.Confirmation = outcome.confirmation
	physical.HTTPStatus = outcome.httpStatus
	physical.ErrorCode = outcome.errorCode
	physical.ErrorMessage = truncateText(state.scrub(outcome.errorMessage), 2048)

	ev := wireMCPEvidence{
		PhysicalCall:          physical,
		Kind:                  kindOpenSession,
		SessionState:          "unknown",
		Handshake:             handshake,
		RefusedServerRequests: state.refused,
		Usage:                 a.attemptUsage(),
	}
	if staged, _, ok := firstStaged(state.captured); ok {
		ev.StagedOutputs = []wireStagedOutput{staged}
	}
	doc, usage, evErr := a.buildSafeEvidence(&ev, state)
	if evErr != nil {
		return contract.Observation{}, evErr
	}

	disposition := contract.DispositionFailed
	switch {
	case outcome.oversize:
		disposition = contract.DispositionUnknown
	case outcome.requestSent == "no":
		disposition = contract.DispositionNotSent
	case outcome.confirmation == "unknown":
		disposition = contract.DispositionUnknown
	}
	var confirmedAt *time.Time
	if disposition != contract.DispositionUnknown {
		t := finished
		confirmedAt = &t
	}
	return contract.Observation{Disposition: disposition, Evidence: doc, Usage: usage, ConfirmedAt: confirmedAt}, nil
}

// doListTools implements the list_tools action kind: exactly one
// tools/list request, one page, next_cursor never followed automatically.
func (a *Adapter) doListTools(ctx context.Context, dispatch contract.Dispatch, secret []byte, pinnedAddr string, in *wireListTools) (contract.Observation, error) {
	entry, ok := a.sessions.get(in.SessionHandle)
	if !ok {
		return contract.Observation{}, prerequisiteMissing("mcpclient: session handle %q is unknown to this adapter", in.SessionHandle)
	}

	started := a.deps.Clock.Now()
	state := &callState{kind: kindListTools, ctx: ctx, blobs: a.deps.Blobs, secret: secret}
	entry.roundTripper.arm(state)
	result, err := entry.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: in.Cursor})
	entry.roundTripper.disarm()
	finished := a.deps.Clock.Now()

	physical := a.newPhysicalCall(dispatch, pinnedAddr, started)
	physical.FinishedAt = finished
	if _, locator, ok := firstStaged(state.captured); ok {
		physical.RequestContext = locator
	}

	sessionState := "active"
	if entry.stateless {
		sessionState = "stateless"
	}

	if err != nil {
		outcome := classifyAttemptError(err, state)
		if outcome.sessionGone {
			a.sessions.remove(in.SessionHandle)
			sessionState = "expired"
		}
		return a.observeSimpleFailure(physical, outcome, kindListTools, in.SessionHandle, sessionState, state, finished)
	}

	physical.RequestSent = "yes"
	physical.Confirmation = "authoritative_success"
	if last := lastStatus(state.captured); last != 0 {
		physical.HTTPStatus = int64(last)
	}

	tools := make([]wireDiscoveredTool, 0, len(result.Tools))
	for _, t := range result.Tools {
		dt, terr := discoveredToolFrom(t)
		if terr != nil {
			return contract.Observation{}, terr
		}
		tools = append(tools, dt)
	}

	ev := wireMCPEvidence{
		PhysicalCall:          physical,
		Kind:                  kindListTools,
		SessionHandle:         in.SessionHandle,
		SessionState:          sessionState,
		Tools:                 tools,
		NextCursor:            result.NextCursor,
		RefusedServerRequests: state.refused,
		Usage:                 a.attemptUsage(),
	}
	if staged, _, ok := firstStaged(state.captured); ok {
		ev.StagedOutputs = []wireStagedOutput{staged}
	}
	doc, usage, evErr := a.buildSafeEvidence(&ev, state)
	if evErr != nil {
		return contract.Observation{}, evErr
	}
	t := finished
	disposition := contract.DispositionSucceeded
	if ev.PhysicalCall.ErrorCode == "response_redacted" {
		disposition = contract.DispositionFailed
	}
	return contract.Observation{Disposition: disposition, Evidence: doc, Usage: usage, ConfirmedAt: &t}, nil
}

// doCallTool implements the call_tool action kind: exactly one tools/call
// request, with every pre-send authority check (allowed_tools, recorded
// discovery digest, classification, strict argument validation against the
// pinned input schema) refusing before any byte is sent.
func (a *Adapter) doCallTool(ctx context.Context, dispatch contract.Dispatch, secret []byte, pinnedAddr string, in *wireCallTool) (contract.Observation, error) {
	entry, ok := a.sessions.get(in.SessionHandle)
	if !ok {
		return contract.Observation{}, prerequisiteMissing("mcpclient: session handle %q is unknown to this adapter", in.SessionHandle)
	}
	if !a.profile.allowsTool(in.Tool) {
		return contract.Observation{}, permissionDenied("mcpclient: tool %q is not in the profile's allowed_tools", in.Tool)
	}
	if !a.profile.allowsClassification(in.Classification) {
		return contract.Observation{}, permissionDenied("mcpclient: classification %q is not in the profile's classifications", in.Classification)
	}
	canonSchema, cerr := contract.Canonicalize(in.InputSchema)
	if cerr != nil {
		return contract.Observation{}, invalidInput("mcpclient: call_tool input_schema does not canonicalize: %v", cerr)
	}
	if computed := contract.Hash(canonSchema); computed != in.InputSchemaDigest {
		return contract.Observation{}, invalidInput(
			"mcpclient: call_tool input_schema_digest %q does not match the pinned schema (computed %q)", in.InputSchemaDigest, computed)
	}
	if verr := contract.ValidateSchema(in.InputSchema, in.Arguments); verr != nil {
		return contract.Observation{}, invalidInput("mcpclient: call_tool arguments do not match the pinned input schema: %v", verr)
	}
	if int64(len(in.Arguments)) > a.profile.MaxRequestBytes {
		return contract.Observation{}, invalidInput("mcpclient: call_tool arguments are %d bytes, exceeding the profile's %d byte max_request_bytes bound", len(in.Arguments), a.profile.MaxRequestBytes)
	}

	started := a.deps.Clock.Now()
	state := &callState{kind: kindCallTool, ctx: ctx, blobs: a.deps.Blobs, secret: secret, classification: in.Classification}
	entry.roundTripper.arm(state)
	result, err := entry.session.CallTool(ctx, &mcp.CallToolParams{Name: in.Tool, Arguments: json.RawMessage(in.Arguments)})
	entry.roundTripper.disarm()
	finished := a.deps.Clock.Now()

	physical := a.newPhysicalCall(dispatch, pinnedAddr, started)
	physical.FinishedAt = finished
	if _, locator, ok := firstStaged(state.captured); ok {
		physical.RequestContext = locator
	}

	sessionState := "active"
	if entry.stateless {
		sessionState = "stateless"
	}

	if err != nil {
		outcome := classifyAttemptError(err, state)
		if outcome.sessionGone {
			a.sessions.remove(in.SessionHandle)
			sessionState = "expired"
		}
		return a.observeCallToolFailure(physical, outcome, in, sessionState, state, finished)
	}

	physical.RequestSent = "yes"
	physical.Confirmation = "authoritative_success"
	if result.IsError {
		physical.Confirmation = "authoritative_failure"
	}
	if last := lastStatus(state.captured); last != 0 {
		physical.HTTPStatus = int64(last)
	}

	summary := contentSummaryOf(result.Content)
	argsDigest := contract.Hash(canonicalOrRaw(in.Arguments))

	ev := wireMCPEvidence{
		PhysicalCall:          physical,
		Kind:                  kindCallTool,
		SessionHandle:         in.SessionHandle,
		SessionState:          sessionState,
		Tool:                  in.Tool,
		ArgumentsDigest:       argsDigest,
		IsError:               result.IsError,
		ContentSummary:        &summary,
		RefusedServerRequests: state.refused,
		Usage:                 a.attemptUsage(),
	}

	resultDoc, merr := json.Marshal(struct {
		Content           []mcp.Content `json:"content"`
		StructuredContent any           `json:"structured_content,omitempty"`
		IsError           bool          `json:"is_error"`
	}{Content: result.Content, StructuredContent: result.StructuredContent, IsError: result.IsError})
	if merr != nil {
		return contract.Observation{}, internalError("mcpclient: encoding the tool result for staging failed: %v", merr)
	}
	if result.StructuredContent != nil {
		structDoc, serr := json.Marshal(result.StructuredContent)
		if serr == nil {
			if canon, cerr := contract.Canonicalize(structDoc); cerr == nil {
				ev.StructuredContentDigest = contract.Hash(canon)
			}
		}
	}

	var staged *wireStagedOutput
	var stageErr error
	if state.containsSensitive(resultDoc) {
		ev.PhysicalCall.ErrorCode = "result_redacted"
		ev.PhysicalCall.ErrorMessage = "tool completed but its result contains confidential transport material and was not staged"
	} else {
		staged, stageErr = stageToolResult(ctx, a.deps.Blobs, resultDoc, in.Classification)
	}
	if stageErr != nil {
		ev.PhysicalCall.ErrorCode = "artifact_fault"
		ev.PhysicalCall.ErrorMessage = "tool completed but its result could not be staged"
	}
	outputs := []wireStagedOutput{}
	if s, _, ok := firstStaged(state.captured); ok {
		outputs = append(outputs, s)
	}
	if staged != nil {
		outputs = append(outputs, *staged)
	}
	ev.StagedOutputs = outputs

	doc, usage, evErr := a.buildSafeEvidence(&ev, state)
	if evErr != nil {
		return contract.Observation{}, evErr
	}
	t := finished
	disposition := contract.DispositionSucceeded
	if result.IsError {
		disposition = contract.DispositionFailed
	}
	return contract.Observation{Disposition: disposition, Evidence: doc, Usage: usage, ConfirmedAt: &t}, nil
}

func (a *Adapter) observeCallToolFailure(physical wirePhysicalCallEvidence, outcome attemptOutcome, in *wireCallTool, sessionState string, state *callState, finished time.Time) (contract.Observation, error) {
	physical.RequestSent = outcome.requestSent
	physical.Confirmation = outcome.confirmation
	physical.HTTPStatus = outcome.httpStatus
	physical.ErrorCode = outcome.errorCode
	physical.ErrorMessage = truncateText(state.scrub(outcome.errorMessage), 2048)

	ev := wireMCPEvidence{
		PhysicalCall:          physical,
		Kind:                  kindCallTool,
		SessionHandle:         in.SessionHandle,
		SessionState:          sessionState,
		Tool:                  in.Tool,
		ArgumentsDigest:       contract.Hash(canonicalOrRaw(in.Arguments)),
		RefusedServerRequests: state.refused,
		Usage:                 a.attemptUsage(),
	}
	if staged, _, ok := firstStaged(state.captured); ok {
		ev.StagedOutputs = []wireStagedOutput{staged}
	}
	doc, usage, evErr := a.buildSafeEvidence(&ev, state)
	if evErr != nil {
		return contract.Observation{}, evErr
	}

	disposition := contract.DispositionFailed
	switch {
	case outcome.oversize:
		disposition = contract.DispositionUnknown
	case outcome.requestSent == "no":
		disposition = contract.DispositionNotSent
	case outcome.confirmation == "unknown":
		disposition = contract.DispositionUnknown
	}
	var confirmedAt *time.Time
	if disposition != contract.DispositionUnknown {
		t := finished
		confirmedAt = &t
	}
	return contract.Observation{Disposition: disposition, Evidence: doc, Usage: usage, ConfirmedAt: confirmedAt}, nil
}

// doCloseSession attempts one HTTP DELETE, including for stateless sessions.
// The real termination status is recorded and the local handle is dropped
// whether the server confirms termination or not.
func (a *Adapter) doCloseSession(ctx context.Context, dispatch contract.Dispatch, secret []byte, pinnedAddr string, in *wireCloseSession) (contract.Observation, error) {
	entry, ok := a.sessions.get(in.SessionHandle)
	if !ok {
		return contract.Observation{}, prerequisiteMissing("mcpclient: session handle %q is unknown to this adapter", in.SessionHandle)
	}
	defer a.sessions.remove(in.SessionHandle)

	started := a.deps.Clock.Now()
	state := &callState{kind: kindCloseSession, ctx: ctx, blobs: a.deps.Blobs, secret: secret}
	entry.roundTripper.arm(state)
	var closeErr error
	if entry.stateless {
		// A stateless SDK Close sends nothing. The frozen adapter action
		// still requires one explicit termination attempt and its real status.
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, a.profile.Endpoint, nil)
		if err != nil {
			closeErr = err
		} else {
			resp, err := entry.roundTripper.RoundTrip(req)
			closeErr = err
			if resp != nil {
				closeErr = resp.Body.Close()
			}
		}
	} else {
		closeErr = entry.session.Close()
	}
	entry.roundTripper.disarm()
	if entry.stateless {
		_ = entry.session.Close()
	}
	finished := a.deps.Clock.Now()

	physical := a.newPhysicalCall(dispatch, pinnedAddr, started)
	physical.FinishedAt = finished
	if _, locator, ok := firstStaged(state.captured); ok {
		physical.RequestContext = locator
	}

	sessionState := "closed"

	if closeErr != nil {
		outcome := classifyAttemptError(closeErr, state)
		return a.observeSimpleFailure(physical, outcome, kindCloseSession, in.SessionHandle, sessionState, state, finished)
	}

	if status := lastStatus(state.captured); status < 200 || status >= 300 {
		outcome := attemptOutcome{requestSent: "yes", confirmation: "authoritative_failure", httpStatus: int64(status), errorCode: "termination_refused", errorMessage: "server did not confirm session termination"}
		return a.observeSimpleFailure(physical, outcome, kindCloseSession, in.SessionHandle, sessionState, state, finished)
	}

	physical.RequestSent = "yes"
	physical.Confirmation = "authoritative_success"
	if last := lastStatus(state.captured); last != 0 {
		physical.HTTPStatus = int64(last)
	}
	ev := wireMCPEvidence{
		PhysicalCall: physical, Kind: kindCloseSession, SessionHandle: in.SessionHandle,
		SessionState: sessionState, RefusedServerRequests: state.refused, Usage: a.attemptUsage(),
	}
	if staged, _, ok := firstStaged(state.captured); ok {
		ev.StagedOutputs = []wireStagedOutput{staged}
	}
	doc, usage, evErr := a.buildSafeEvidence(&ev, state)
	if evErr != nil {
		return contract.Observation{}, evErr
	}
	t := finished
	disposition := contract.DispositionSucceeded
	if ev.PhysicalCall.ErrorCode == "response_redacted" {
		disposition = contract.DispositionFailed
	}
	return contract.Observation{Disposition: disposition, Evidence: doc, Usage: usage, ConfirmedAt: &t}, nil
}

// observeSimpleFailure builds the failure Observation shared by list_tools
// and close_session: kinds with no result-specific fields to preserve.
func (a *Adapter) observeSimpleFailure(physical wirePhysicalCallEvidence, outcome attemptOutcome, kind, sessionHandle, sessionState string, state *callState, finished time.Time) (contract.Observation, error) {
	physical.RequestSent = outcome.requestSent
	physical.Confirmation = outcome.confirmation
	physical.HTTPStatus = outcome.httpStatus
	physical.ErrorCode = outcome.errorCode
	physical.ErrorMessage = truncateText(state.scrub(outcome.errorMessage), 2048)

	ev := wireMCPEvidence{
		PhysicalCall: physical, Kind: kind, SessionHandle: sessionHandle,
		SessionState: sessionState, RefusedServerRequests: state.refused, Usage: a.attemptUsage(),
	}
	if staged, _, ok := firstStaged(state.captured); ok {
		ev.StagedOutputs = []wireStagedOutput{staged}
	}
	doc, usage, evErr := a.buildSafeEvidence(&ev, state)
	if evErr != nil {
		return contract.Observation{}, evErr
	}

	disposition := contract.DispositionFailed
	switch {
	case outcome.oversize:
		disposition = contract.DispositionUnknown
	case outcome.requestSent == "no":
		disposition = contract.DispositionNotSent
	case outcome.confirmation == "unknown":
		disposition = contract.DispositionUnknown
	}
	var confirmedAt *time.Time
	if disposition != contract.DispositionUnknown {
		t := finished
		confirmedAt = &t
	}
	return contract.Observation{Disposition: disposition, Evidence: doc, Usage: usage, ConfirmedAt: confirmedAt}, nil
}

// handshakeMessages are the physical requests buildHandshake records, in
// the frozen MCPHandshakeExchange schema's allowed order: the pinned
// go-sdk client always tries the SEP-2575 server/discover probe first
// (never omitted from evidence, whether the server answers it or not),
// then, only when the server does not, the legacy initialize request and
// notifications/initialized.
var handshakeMessages = map[string]bool{
	"server/discover":           true,
	"initialize":                true,
	"notifications/initialized": true,
}

// buildHandshake filters captured physical requests down to the at-most-
// three handshake messages the frozen MCPHandshakeExchange schema can
// represent, in the order sent. A captured request that never received a
// response (status 0, e.g. a stall) is omitted: the schema requires a real
// HTTP status on every entry.
func buildHandshake(captured []capturedRequest) []wireHandshakeExchange {
	out := make([]wireHandshakeExchange, 0, 3)
	for _, c := range captured {
		if c.status == 0 {
			continue
		}
		if !handshakeMessages[c.rpcMethod] {
			continue
		}
		out = append(out, wireHandshakeExchange{Message: c.rpcMethod, HTTPStatus: int64(c.status), RequestSent: "yes"})
		if len(out) == 3 {
			break
		}
	}
	return out
}

func lastStatus(captured []capturedRequest) int {
	if len(captured) == 0 {
		return 0
	}
	return captured[len(captured)-1].status
}

// serverCapabilityNames flattens the SDK's ServerCapabilities struct into
// the bounded string list the frozen evidence schema carries.
func serverCapabilityNames(c *mcp.ServerCapabilities) []string {
	if c == nil {
		return nil
	}
	var names []string
	if c.Completions != nil {
		names = append(names, "completions")
	}
	encoded, _ := json.Marshal(c)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &fields)
	if _, ok := fields["logging"]; ok {
		names = append(names, "logging")
	}
	if c.Prompts != nil {
		names = append(names, "prompts")
	}
	if c.Resources != nil {
		names = append(names, "resources")
	}
	if c.Tools != nil {
		names = append(names, "tools")
	}
	for k := range c.Experimental {
		if len(names) >= 32 {
			break
		}
		names = append(names, truncateText(k, 64))
	}
	for k := range c.Extensions {
		if len(names) >= 32 {
			break
		}
		names = append(names, truncateText(k, 64))
	}
	if len(names) > 32 {
		names = names[:32]
	}
	return names
}

// discoveredToolFrom converts one SDK Tool into the frozen
// MCPDiscoveredTool wire shape, computing the pinned input schema digest
// from the tool's real, observed input schema.
func discoveredToolFrom(t *mcp.Tool) (wireDiscoveredTool, error) {
	inputDoc, err := json.Marshal(t.InputSchema)
	if err != nil {
		return wireDiscoveredTool{}, internalError("mcpclient: encoding discovered tool %q input schema failed: %v", t.Name, err)
	}
	canon, cerr := contract.Canonicalize(inputDoc)
	if cerr != nil {
		return wireDiscoveredTool{}, internalError("mcpclient: canonicalizing discovered tool %q input schema failed: %v", t.Name, cerr)
	}
	dt := wireDiscoveredTool{
		Name:              t.Name,
		Title:             t.Title,
		Description:       t.Description,
		InputSchema:       inputDoc,
		InputSchemaDigest: contract.Hash(canon),
	}
	if t.OutputSchema != nil {
		outputDoc, oerr := json.Marshal(t.OutputSchema)
		if oerr == nil {
			dt.OutputSchema = outputDoc
		}
	}
	if t.Annotations != nil {
		dt.Annotations = wireToolAnnotations{
			Title:           t.Annotations.Title,
			ReadOnlyHint:    &t.Annotations.ReadOnlyHint,
			DestructiveHint: t.Annotations.DestructiveHint,
			IdempotentHint:  &t.Annotations.IdempotentHint,
			OpenWorldHint:   t.Annotations.OpenWorldHint,
		}
	}
	return dt, nil
}

// contentSummaryOf counts each Content variant in a tool result, never its
// bytes: resource_link/embedded-resource content is recorded as opaque
// references only, never fetched.
func contentSummaryOf(content []mcp.Content) wireContentSummary {
	var s wireContentSummary
	for _, c := range content {
		switch c.(type) {
		case *mcp.TextContent:
			s.Text++
		case *mcp.ImageContent:
			s.Image++
		case *mcp.AudioContent:
			s.Audio++
		case *mcp.ResourceLink:
			s.ResourceLink++
		case *mcp.EmbeddedResource:
			s.EmbeddedResource++
		}
	}
	return s
}

// canonicalOrRaw returns the canonical-JSON form of raw for digesting, or
// raw itself if canonicalization fails (defensive: arguments already
// passed strict schema validation by the time this is called).
func canonicalOrRaw(raw json.RawMessage) []byte {
	if canon, err := contract.Canonicalize(raw); err == nil {
		return canon
	}
	return raw
}
