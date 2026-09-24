package mcpclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zatiti/zatiti/internal/contract"
)

const adapterName = "mcp"

// refusedMethods are the server-to-client request methods this adapter
// always refuses with a JSON-RPC method-not-found error, regardless of the
// go-sdk's own default per-feature behavior (which is inconsistent: a nil
// CreateMessageHandler answers -31001, a nil ElicitationHandler answers
// CodeInvalidParams, and roots/list succeeds by default with an empty
// list). "Unsupported features are refusals, never inferred success."
var refusedMethods = map[string]bool{
	"sampling/createMessage": true,
	"elicitation/create":     true,
	"roots/list":             true,
	"ping":                   true,
}

// Adapter is the qualified generic MCP client connection adapter.
type Adapter struct {
	profile    *mcpProfile
	deps       contract.AdapterDependencies
	transport  *http.Transport
	schema     json.RawMessage
	sessions   *sessionTable
	resolveIPs ipLookupFunc
}

// New constructs the mcpclient adapter from a zatiti.mcp/v1 profile. The
// profile's capability_evidence must bind to its own bytes (see
// loadProfile); config is otherwise immutable for the adapter's lifetime.
func New(deps contract.AdapterDependencies, raw json.RawMessage) (contract.Adapter, error) {
	if deps.HTTP == nil {
		return nil, invalidInput("mcpclient adapter requires an HTTP client dependency")
	}
	if deps.Clock == nil {
		return nil, invalidInput("mcpclient adapter requires a clock dependency")
	}
	if deps.Blobs == nil {
		return nil, invalidInput("mcpclient adapter requires a blob store dependency")
	}
	profile, err := loadProfile(raw)
	if err != nil {
		return nil, err
	}
	contractDoc, err := buildContractDocument()
	if err != nil {
		return nil, err
	}
	// A dedicated Transport is built here rather than reusing
	// deps.HTTP.Transport, exactly as httpread does: this adapter's
	// DialContext (pinnedDialContext) IS its security boundary against DNS
	// rebinding, and it is wrapped per call by a callRoundTripper that
	// injects Authorization and bounds request/response size, neither of
	// which may leak onto a Transport shared with a sibling adapter.
	transport := &http.Transport{DialContext: pinnedDialContext, DisableKeepAlives: true}
	// Installation trust roots may be injected without inheriting a proxy,
	// insecure verification, client credentials, or an alternate dialer.
	if supplied, ok := deps.HTTP.Transport.(*http.Transport); ok && supplied.TLSClientConfig != nil && supplied.TLSClientConfig.RootCAs != nil {
		transport.TLSClientConfig = &tls.Config{RootCAs: supplied.TLSClientConfig.RootCAs.Clone(), MinVersion: tls.VersionTLS12}
	}
	return &Adapter{
		profile:    profile,
		deps:       deps,
		transport:  transport,
		schema:     contractDoc,
		sessions:   newSessionTable(),
		resolveIPs: net.DefaultResolver.LookupIPAddr,
	}, nil
}

// Name implements contract.Adapter.
func (a *Adapter) Name() string { return adapterName }

// Contract implements contract.Adapter.
func (a *Adapter) Contract() json.RawMessage { return a.schema }

// Invoke implements contract.Adapter.
func (a *Adapter) Invoke(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	return a.call(ctx, dispatch)
}

// Reconcile implements contract.Adapter. The frozen contract declares every
// kind capability_unsupported for reconciliation: MCP exposes no
// authoritative call-lookup capability (no resources/prompts/tasks calls,
// ever), so there is nothing honest to check independently of repeating
// the original call.
func (a *Adapter) Reconcile(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	return contract.Observation{}, capabilityUnsupported("mcpclient: reconcile is not supported for any action kind; MCP has no authoritative call-lookup capability")
}

type contractDocument struct {
	Schema           string          `json:"schema"`
	ProfileSchema    json.RawMessage `json:"profile_schema"`
	ParametersSchema json.RawMessage `json:"parameters_schema"`
	EvidenceSchema   json.RawMessage `json:"evidence_schema"`
}

func buildContractDocument() (json.RawMessage, error) {
	ps, err := profileSchema()
	if err != nil {
		return nil, internalError("mcp contract profile schema composition failed: %v", err)
	}
	as, err := parametersSchema()
	if err != nil {
		return nil, internalError("mcp contract parameters schema composition failed: %v", err)
	}
	es, err := evidenceSchema()
	if err != nil {
		return nil, internalError("mcp contract evidence schema composition failed: %v", err)
	}
	doc := contractDocument{Schema: "zatiti.mcp.contract/v1", ProfileSchema: ps, ParametersSchema: as, EvidenceSchema: es}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, internalError("mcp contract document encoding failed: %v", err)
	}
	return out, nil
}

// call implements Invoke. A non-nil error means no physical request was
// sent: invalid input, a tool/classification outside the profile's
// allowlists, a session handle unknown to this adapter's local table, an
// endpoint resolving only to disallowed addresses, or a credential that
// could not be resolved. Once at least one physical request is sent, the
// outcome is always reported through Observation with a nil error.
func (a *Adapter) call(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	if dispatch.Adapter != adapterName {
		return contract.Observation{}, invalidInput("dispatch adapter %q does not match %q", dispatch.Adapter, adapterName)
	}

	act, err := decodeAction(dispatch.Action)
	if err != nil {
		return contract.Observation{}, err
	}

	var handle string
	switch act.Kind {
	case kindListTools:
		handle = act.ListTools.SessionHandle
	case kindCallTool:
		handle = act.CallTool.SessionHandle
	case kindCloseSession:
		handle = act.CloseSession.SessionHandle
	}
	if handle != "" {
		entry, ok := a.sessions.get(handle)
		if !ok {
			return contract.Observation{}, prerequisiteMissing("mcpclient: unknown session handle")
		}
		if !entry.mu.TryLock() {
			return contract.Observation{}, prerequisiteMissing("mcpclient: session already has an active operation")
		}
		defer entry.mu.Unlock()
		// Recheck after locking: close_session may have removed the handle.
		if current, ok := a.sessions.get(handle); !ok || current != entry {
			return contract.Observation{}, prerequisiteMissing("mcpclient: session is closed")
		}
		if entry.credentialRef != dispatch.CredentialRef {
			return contract.Observation{}, permissionDenied("mcpclient: session belongs to another credential reference")
		}
	}

	secret, err := a.resolveCredential(ctx, dispatch.CredentialRef)
	if err != nil {
		return contract.Observation{}, err
	}

	pinnedCtx, pinnedAddr, err := a.pinDial(ctx)
	if err != nil {
		return contract.Observation{}, err
	}

	callCtx, cancel := callContext(pinnedCtx, a.deps.Clock, a.profile.Timeout, dispatch.Deadline)
	defer cancel()

	switch act.Kind {
	case kindOpenSession:
		return a.doOpenSession(callCtx, dispatch, secret, pinnedAddr, act.OpenSession)
	case kindListTools:
		return a.doListTools(callCtx, dispatch, secret, pinnedAddr, act.ListTools)
	case kindCallTool:
		return a.doCallTool(callCtx, dispatch, secret, pinnedAddr, act.CallTool)
	case kindCloseSession:
		return a.doCloseSession(callCtx, dispatch, secret, pinnedAddr, act.CloseSession)
	default:
		return contract.Observation{}, capabilityUnsupported("mcp action kind %q is not a qualified v1 capability", act.Kind)
	}
}

// resolveCredential resolves dispatch.CredentialRef to secret bytes outside
// any transaction. credential_kind none means every dispatch carries no
// credential and Authorization is never set; bearer requires a non-empty
// reference and secret store.
func (a *Adapter) resolveCredential(ctx context.Context, ref string) ([]byte, error) {
	if a.profile.CredentialKind == "none" {
		return nil, nil
	}
	if ref == "" {
		return nil, invalidInput("dispatch credential_ref is empty")
	}
	if a.deps.Secrets == nil {
		return nil, prerequisiteMissing("mcpclient adapter requires a secret store dependency to resolve credential %q", ref)
	}
	secret, err := a.deps.Secrets.Get(ctx, ref)
	if err != nil {
		return nil, secretFault(err)
	}
	if len(secret) == 0 {
		return nil, prerequisiteMissing("mcpclient credential %q resolved to empty secret material", ref)
	}
	return secret, nil
}

// pinDial resolves and validates the profile's fixed endpoint host, and
// attaches the single validated dial address to ctx so pinnedDialContext
// connects to exactly that address with no further hostname resolution
// (preventing DNS rebinding between validation and connection). A failure
// here means no physical request is ever attempted, matching httpread's
// convention: dial validation is a policy question about the configured
// profile, not a live transport condition worth an Observation.
func (a *Adapter) pinDial(ctx context.Context) (context.Context, string, error) {
	u, err := url.Parse(a.profile.Endpoint)
	if err != nil {
		return ctx, "", internalError("mcpclient: profile endpoint %q does not parse as a URL: %v", a.profile.Endpoint, err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = defaultPortForScheme(u.Scheme)
	}
	validated, lookupErr := resolveValidatedIPs(ctx, a.resolveIPs, host, a.profile.AllowPrivateEndpoint)
	if lookupErr != nil {
		return ctx, "", internalError("mcpclient: resolving endpoint host %q failed: %v", host, lookupErr)
	}
	if len(validated) == 0 {
		return ctx, "", permissionDenied("mcpclient: endpoint host %q has no permitted dial address (loopback/private/link-local/metadata refused; set allow_private_endpoint to permit)", host)
	}
	pinnedAddr := net.JoinHostPort(validated[0].String(), port)
	return withPinnedDial(ctx, pinnedAddr), pinnedAddr, nil
}

// callContext bounds ctx by the earlier of the profile's configured
// timeout (measured from the injected clock) and the dispatch's deadline.
func callContext(ctx context.Context, clock contract.Clock, timeout time.Duration, deadline time.Time) (context.Context, context.CancelFunc) {
	d := clock.Now().Add(timeout)
	if !deadline.IsZero() && deadline.Before(d) {
		d = deadline
	}
	return context.WithDeadline(ctx, d)
}

// newHTTPClient builds the per-session http.Client and its callRoundTripper.
// One of these is built once per open_session and then reused, armed and
// disarmed, for every later call against that same session.
func (a *Adapter) newHTTPClient() (*http.Client, *callRoundTripper) {
	rt := &callRoundTripper{
		base:             a.transport,
		maxRequestBytes:  a.profile.MaxRequestBytes,
		maxResponseBytes: a.profile.MaxResponseBytes,
	}
	return &http.Client{Transport: rt}, rt
}

// neverGrantElicitation is wired into every Client's ElicitationHandler for
// the sole purpose of making the client advertise the elicitation
// capability during open_session. Without any handler set, the go-sdk
// never advertises the capability at all, and the SERVER's own
// (*ServerSession).Elicit refuses locally with "client does not support
// elicitation" before ever sending a wire request -- which would also
// block a malicious/misbehaving server, but leaves elicitation/create out
// of refused_server_requests, unlike the other three refused methods
// (which the SDK does not locally capability-gate the same way, and so do
// reach refusalMiddleware over the wire). Advertising the capability makes
// the refusal uniform and evidenced for all four methods; this handler
// itself must never actually run, because refusalMiddleware refuses
// elicitation/create before the go-sdk's own dispatch ever reaches it -- if
// it ever does run, that is itself a bug in the refusal path, so it
// returns a hard failure rather than silently granting anything.
func neverGrantElicitation(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	return nil, fmt.Errorf("mcpclient: elicitation/create reached the client handler instead of being refused by middleware")
}

// refusalMiddleware returns the mcp.Middleware this adapter installs on
// every Client it constructs: it intercepts exactly the four server-to-
// client request methods the frozen contract requires refusing, answering
// JSON-RPC method-not-found directly rather than falling through to the
// go-sdk's own inconsistent default per-feature behavior, and records each
// refusal against whichever call is currently armed on rt.
func refusalMiddleware(rt *callRoundTripper) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if refusedMethods[method] {
				rt.recordRefusal(method)
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: fmt.Sprintf("method %q is refused by this connection", method)}
			}
			return next(ctx, method, req)
		}
	}
}

// newPhysicalCall builds the physical_call evidence skeleton common to
// every action kind. Fields specific to the outcome (finished_at,
// request_sent, confirmation, http_status, error_code/message) are filled
// in by the caller once the attempt concludes.
func (a *Adapter) newPhysicalCall(dispatch contract.Dispatch, pinnedAddr string, started time.Time) wirePhysicalCallEvidence {
	accountIdentity := "none"
	if dispatch.CredentialRef != "" {
		accountIdentity = "credential:" + dispatch.CredentialRef
	}
	return wirePhysicalCallEvidence{
		OperationID:          dispatch.OperationID,
		AttemptID:            dispatch.AttemptID,
		AccountIdentity:      accountIdentity,
		RequestedDestination: a.profile.Endpoint,
		ResolvedDestination:  pinnedAddr,
		ProfileDigest:        a.profile.Digest,
		CapabilityEvidence:   a.profile.CapabilityEvidence.Artifact,
		StartedAt:            started,
	}
}

// buildEvidence assembles and marshals the zatiti.mcp.evidence/v1 document,
// plus a standalone marshal of its accounting Usage for Observation.Usage.
func (a *Adapter) buildEvidence(ev wireMCPEvidence) (json.RawMessage, json.RawMessage, error) {
	ev.Schema = "zatiti.mcp.evidence/v1"
	if ev.RefusedServerRequests == nil {
		ev.RefusedServerRequests = []string{}
	}
	if ev.StagedOutputs == nil {
		ev.StagedOutputs = []wireStagedOutput{}
	}
	if ev.OutputArtifacts == nil {
		ev.OutputArtifacts = []wireArtifactRef{}
	}
	usageDoc, err := json.Marshal(ev.Usage.Accounting)
	if err != nil {
		return nil, nil, internalError("encoding mcp usage failed: %v", err)
	}
	doc, err := json.Marshal(ev)
	if err != nil {
		return nil, nil, internalError("encoding mcp evidence failed: %v", err)
	}
	return doc, usageDoc, nil
}

// attemptOutcome summarizes how a physical call concluded, derived from the
// state a callRoundTripper captured and the error (if any) an SDK method
// call returned.
type attemptOutcome struct {
	requestSent  string // no | yes | unknown
	confirmation string
	httpStatus   int64
	errorCode    string
	errorMessage string
	redirect     *errRedirectRefused
	oversize     bool
	sessionGone  bool
}

// classifyAttemptError inspects the error an SDK method call returned
// (after at least a pinned dial was established) and the physical requests
// callRoundTripper captured, and reports how to record the attempt. A nil
// err with at least one captured request is the success path and is not
// passed here.
func classifyAttemptError(err error, state *callState) attemptOutcome {
	captured := state.captured
	var redirect *errRedirectRefused
	if errors.As(err, &redirect) {
		return attemptOutcome{
			requestSent: "yes", confirmation: "authoritative_failure",
			httpStatus: int64(redirect.status), errorCode: "redirect_not_permitted",
			errorMessage: redirect.Error(), redirect: redirect,
		}
	}
	// state.oversize, not errors.Is(err, errResponseOversize): the go-sdk's
	// SSE stream reader does not propagate a response-body Read error
	// through its public API (confirmed by direct inspection -- a tripped
	// boundedBody surfaces only as the generic "request terminated without
	// response"), so this adapter's own witness at the RoundTripper layer
	// is the only reliable signal. See callState.oversize's doc comment.
	if state.oversize.Load() {
		msg := errResponseOversize.Error()
		if err != nil {
			msg = err.Error()
		}
		return attemptOutcome{
			requestSent: "yes", confirmation: "unknown",
			errorCode: "response_oversize", errorMessage: msg, oversize: true,
		}
	}
	if errors.Is(err, mcp.ErrSessionMissing) {
		return attemptOutcome{
			requestSent: "yes", confirmation: "authoritative_nonexecution",
			httpStatus: http.StatusNotFound, errorCode: "session_not_found",
			errorMessage: err.Error(), sessionGone: true,
		}
	}
	var oversizeReq *errRequestOversize
	if errors.As(err, &oversizeReq) {
		return attemptOutcome{requestSent: "no", confirmation: "authoritative_nonexecution", errorCode: "request_oversize", errorMessage: err.Error()}
	}
	if len(captured) == 0 {
		return attemptOutcome{requestSent: "no", confirmation: "authoritative_nonexecution", errorCode: "transport_error", errorMessage: truncateText(err.Error(), 2048)}
	}
	return attemptOutcome{requestSent: "yes", confirmation: "unknown", errorCode: "transport_error", errorMessage: truncateText(err.Error(), 2048)}
}

// firstStaged returns the staged request record for the first captured
// physical request, the locator every kind's physical_call.request_context
// carries.
func firstStaged(captured []capturedRequest) (wireStagedOutput, wireStagedLocator, bool) {
	if len(captured) == 0 {
		return wireStagedOutput{}, wireStagedLocator{}, false
	}
	return captured[0].staged, captured[0].locator, true
}
