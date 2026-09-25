package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

const adapterName = "responses"

// Adapter is the qualified hosted Responses model adapter.
type Adapter struct {
	profile  *responsesProfile
	deps     contract.AdapterDependencies
	client   *http.Client
	protocol wireProtocol // nil when the profile's protocol revision is not qualified in this build
	schema   json.RawMessage
}

// New constructs the Responses adapter from a zatiti.responses/v1 profile.
// The profile's capability_evidence must bind to its own bytes (see
// loadProfile); config is otherwise immutable for the adapter's lifetime.
func New(deps contract.AdapterDependencies, raw json.RawMessage) (contract.Adapter, error) {
	return newWithProtocols(deps, raw, qualifiedProtocols())
}

// newWithProtocols is New over an explicit protocol registry, so this
// package's tests can drive the transport, accounting and evidence
// machinery through a synthetic protocol as well as the qualified ones.
func newWithProtocols(deps contract.AdapterDependencies, raw json.RawMessage, protocols map[string]wireProtocol) (*Adapter, error) {
	if deps.HTTP == nil {
		return nil, invalidInput("responses adapter requires an HTTP client dependency")
	}
	if deps.Clock == nil {
		return nil, invalidInput("responses adapter requires a clock dependency")
	}
	profile, err := loadProfile(raw)
	if err != nil {
		return nil, err
	}
	protocol := protocols[profile.CapabilityEvidence.ProtocolRevision]
	if profile.Version == "zatiti.responses/v2" {
		want := map[string]string{"openai": openaiProtocolRevision, "openrouter": openRouterProtocolRevision, "experiential": experientialProtocolRevision}[profile.Provider]
		if want == "" || profile.CapabilityEvidence.ProtocolRevision != want {
			return nil, invalidInput("provider %q and capability_evidence.protocol_revision do not agree", profile.Provider)
		}
	}
	if protocol != nil {
		if err := protocol.validateProfile(profile.protocolProfile()); err != nil {
			return nil, capabilityUnsupported("profile cannot be honoured by wire protocol %q: %v", profile.CapabilityEvidence.ProtocolRevision, err)
		}
	}
	revisions := make([]string, 0, len(protocols))
	for revision := range protocols {
		revisions = append(revisions, revision)
	}
	contractDoc, err := buildContractDocument(revisions)
	if err != nil {
		return nil, err
	}
	// A private client value that reuses the shared Transport (and its
	// connection pool) is used instead of deps.HTTP directly:
	// AdapterDependencies.HTTP is shared across every adapter, so
	// CheckRedirect cannot be set on it without affecting siblings.
	// Redirects are never followed: a redirect would be a second physical
	// request disclosing the context to a destination the profile never
	// declared.
	client := &http.Client{
		Transport: deps.HTTP.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Adapter{
		profile:  profile,
		deps:     deps,
		client:   client,
		protocol: protocol,
		schema:   contractDoc,
	}, nil
}

// Name implements contract.Adapter.
func (a *Adapter) Name() string { return adapterName }

// Contract implements contract.Adapter, describing the local frozen seam:
// the profile, parameters, context and evidence schemas this adapter
// validates against, and the wire protocol revisions this build has
// qualified.
func (a *Adapter) Contract() json.RawMessage { return a.schema }

type contractDocument struct {
	Schema                     string          `json:"schema"`
	ProfileSchema              json.RawMessage `json:"profile_schema"`
	ParametersSchema           json.RawMessage `json:"parameters_schema"`
	EvidenceSchema             json.RawMessage `json:"evidence_schema"`
	ProfileSchemaV2            json.RawMessage `json:"profile_schema_v2"`
	ParametersSchemaV2         json.RawMessage `json:"parameters_schema_v2"`
	EvidenceSchemaV2           json.RawMessage `json:"evidence_schema_v2"`
	ContextSchema              json.RawMessage `json:"context_schema"`
	QualifiedProtocolRevisions []string        `json:"qualified_protocol_revisions"`
}

func buildContractDocument(revisions []string) (json.RawMessage, error) {
	ps, err := profileSchema()
	if err != nil {
		return nil, internalError("responses contract profile schema composition failed: %v", err)
	}
	as, err := parametersSchema()
	if err != nil {
		return nil, internalError("responses contract parameters schema composition failed: %v", err)
	}
	es, err := evidenceSchema()
	if err != nil {
		return nil, internalError("responses contract evidence schema composition failed: %v", err)
	}
	ps2, err := profileSchemaV2()
	if err != nil {
		return nil, internalError("responses contract v2 profile schema composition failed: %v", err)
	}
	as2, err := parametersSchemaV2()
	if err != nil {
		return nil, internalError("responses contract v2 parameters schema composition failed: %v", err)
	}
	es2, err := evidenceSchemaV2()
	if err != nil {
		return nil, internalError("responses contract v2 evidence schema composition failed: %v", err)
	}
	cs, err := contextSchema()
	if err != nil {
		return nil, internalError("responses contract context schema composition failed: %v", err)
	}
	slices.Sort(revisions)
	doc := contractDocument{
		Schema:                     "zatiti.responses.contract/v1",
		ProfileSchema:              ps,
		ParametersSchema:           as,
		EvidenceSchema:             es,
		ProfileSchemaV2:            ps2,
		ParametersSchemaV2:         as2,
		EvidenceSchemaV2:           es2,
		ContextSchema:              cs,
		QualifiedProtocolRevisions: revisions,
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, internalError("responses contract document encoding failed: %v", err)
	}
	return out, nil
}

// step is the validated, admitted prepare_session or model_step call every
// physical call of one Invoke or Reconcile works from. doc is nil for
// prepare_session: it has no context_artifact, and classification -- what
// every request/response record this step stages is classified under --
// falls back to "internal" ("repository and task content default to
// internal classification") rather than a nonexistent context's.
type step struct {
	act            *wireResponsesParameters
	doc            *contextDocument
	classification string
	request        protocolRequest
	bound          *int64
	bounds         admittedBounds
	secret         []byte
}

// admitStep performs every local check that precedes any physical call:
// action decoding, protocol selection and, for a model_step, context
// validation, the output ceiling, the input bound and the cost bound; then
// credential resolution for either kind. A non-nil error means nothing was
// sent.
func (a *Adapter) admitStep(ctx context.Context, dispatch contract.Dispatch, reconcile bool) (*step, error) {
	if dispatch.Adapter != adapterName {
		return nil, invalidInput("dispatch adapter %q does not match %q", dispatch.Adapter, adapterName)
	}
	act, err := decodeAction(dispatch.Action)
	if err != nil {
		return nil, err
	}
	s := &step{act: act}
	if a.profile.Version == "zatiti.responses/v2" {
		if act.Schema != "zatiti.responses.action/v2" {
			return nil, invalidInput("a v2 Responses profile requires a v2 action")
		}
		if act.SessionMode != a.profile.SessionMode {
			return nil, invalidInput("action session_mode does not match the configured provider profile")
		}
		if act.Kind == kindPrepareSession && act.SessionMode != "provider_conversation" {
			return nil, invalidInput("prepare_session is only valid for provider_conversation profiles")
		}
	} else if act.Schema != "zatiti.responses.action/v1" {
		return nil, invalidInput("a v1 Responses profile requires a v1 action")
	}

	if act.Kind == kindPrepareSession {
		// The wire boundary: everything above is defined by the frozen
		// Zatiti-side contract; the upstream request is defined only by a
		// qualified protocol revision.
		if a.protocol == nil {
			return nil, capabilityUnsupported(
				"wire protocol revision %q is not qualified in this build: the upstream request and response shapes are pinned only by real-endpoint qualification; no call was made",
				a.profile.CapabilityEvidence.ProtocolRevision)
		}
		if reconcile {
			// No documented way to list conversations or find one by
			// metadata: a lost prepare_session stays unknown forever,
			// never resolved by a later lookup (PROTOCOL.md).
			return nil, capabilityUnsupported("prepare_session has no documented authoritative lookup; a lost session creation stays unknown and reconciliation cannot resolve it")
		}
		s.classification = "internal"
		s.request = protocolRequest{
			Profile:     a.profile.protocolProfile(),
			OperationID: string(dispatch.OperationID),
			AttemptID:   string(dispatch.AttemptID),
			SessionID:   act.SessionID,
		}
		// Session creation is never billed by the pinned protocol (see
		// PROTOCOL.md's accounting section): there is no worst-case
		// charge to admit or refuse.
		s.bounds = admittedBounds{}
		if s.secret, err = a.resolveCredential(ctx, dispatch.CredentialRef); err != nil {
			return nil, err
		}
		return s, nil
	}

	// model_step: the same reachable-check ordering as before revision 3
	// -- local checks the profile alone can answer, then the persisted
	// context, before anything asks what the wire protocol can do.
	if act.MaxOutputTokens > a.profile.MaxOutputTokens {
		return nil, budgetUnavailable("action max_output_tokens %d exceeds the profile's max_output_tokens %d", act.MaxOutputTokens, a.profile.MaxOutputTokens)
	}
	doc, err := loadContext(ctx, a.deps.Blobs, a.profile, act)
	if err != nil {
		return nil, err
	}
	if a.protocol == nil {
		return nil, capabilityUnsupported(
			"wire protocol revision %q is not qualified in this build: the upstream request and response shapes are pinned only by real-endpoint qualification; no call was made",
			a.profile.CapabilityEvidence.ProtocolRevision)
	}
	limits := a.protocol.limits()
	if act.ContinuationReference != "" && !limits.SupportsContinuation {
		return nil, capabilityUnsupported("the qualified wire protocol cannot express continuation_reference")
	}
	if act.MaxOutputTokens < limits.MinOutputTokens {
		return nil, capabilityUnsupported("action max_output_tokens %d is below the %d-token minimum the qualified wire protocol can enforce", act.MaxOutputTokens, limits.MinOutputTokens)
	}
	if reconcile && !limits.SupportsReconcile {
		return nil, capabilityUnsupported("the qualified wire protocol has no documented authoritative lookup; the original outcome remains unknown and no call was made")
	}

	s.doc = doc
	s.classification = doc.Classification
	s.request = protocolRequest{
		Profile:               a.profile.protocolProfile(),
		OperationID:           string(dispatch.OperationID),
		AttemptID:             string(dispatch.AttemptID),
		MaxOutputTokens:       act.MaxOutputTokens,
		Context:               doc,
		ContinuationReference: act.ContinuationReference,
		SessionID:             act.SessionID,
	}
	s.bound = a.protocol.inputTokenBound(s.request)
	if reconcile {
		// A reconciliation resolves an attempt that was already admitted;
		// its bounds describe the outstanding charge, and cannot refuse.
		s.bounds = a.profile.outstandingBounds(act.MaxOutputTokens, s.bound)
	} else if s.bounds, err = a.profile.admit(act.MaxOutputTokens, limits, s.bound); err != nil {
		return nil, err
	}
	if s.secret, err = a.resolveCredential(ctx, dispatch.CredentialRef); err != nil {
		return nil, err
	}
	return s, nil
}

// callContext bounds the whole attempt by the earlier of the profile
// timeout and the dispatch deadline, both measured on the injected clock
// but applied as a duration so a test clock pinned in the past cannot
// pre-expire it.
func (a *Adapter) callContext(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc, error) {
	remaining := a.profile.Timeout
	if !deadline.IsZero() {
		if untilDeadline := deadline.Sub(a.deps.Clock.Now()); untilDeadline < remaining {
			remaining = untilDeadline
		}
	}
	if remaining <= 0 {
		return nil, nil, invalidInput("dispatch deadline has already passed; no call was made")
	}
	callCtx, cancel := context.WithTimeout(ctx, remaining)
	return callCtx, cancel, nil
}

// Invoke implements contract.Adapter. Revision 3 splits OpenAI conversation
// preparation from the model call into two separately admitted, journaled
// single-call effects (P00-009): a dispatch names exactly one of them, and
// Invoke performs exactly the one physical call that kind describes, never
// both. A non-nil error means nothing was sent; once a request is handed to
// the transport, the outcome is reported through Observation with a nil
// error.
func (a *Adapter) Invoke(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	s, err := a.admitStep(ctx, dispatch, false)
	if err != nil {
		return contract.Observation{}, err
	}
	callCtx, cancel, err := a.callContext(ctx, dispatch.Deadline)
	if err != nil {
		return contract.Observation{}, err
	}
	defer cancel()

	if s.act.Kind == kindPrepareSession {
		return a.invokePrepareSession(ctx, callCtx, dispatch, s)
	}
	call, err := a.encodeStep(s, s.act.SessionHandle)
	if err != nil {
		return contract.Observation{}, err
	}
	return a.invokeModelStep(ctx, callCtx, dispatch, s, call)
}

// invokePrepareSession performs the one physical call a prepare_session
// action describes: create the provider conversation and report its handle
// as session_handle. It never sends the model step itself. Session
// creation is never billed by the pinned protocol (PROTOCOL.md's
// accounting section), so usage is always no_charge once the request was
// sent.
func (a *Adapter) invokePrepareSession(ctx, callCtx context.Context, dispatch contract.Dispatch, s *step) (contract.Observation, error) {
	prep, err := a.protocol.prepare(s.request)
	if err != nil {
		return contract.Observation{}, capabilityUnsupported("the qualified wire protocol cannot prepare a session: %v", err)
	}
	if prep == nil {
		return contract.Observation{}, capabilityUnsupported("the qualified wire protocol requires no preparatory call; prepare_session does not apply to it")
	}
	po, err := a.physical(ctx, callCtx, *prep, s)
	if err != nil {
		return contract.Observation{}, err
	}
	physical := a.basePhysicalEvidence(dispatch, po)
	disposition, handle := interpretPrepareSession(a.protocol, s.secret, po, &physical)
	physical.ProviderReference = handle
	usage := a.profile.usageFor(usageInput{Bounds: s.bounds, Sent: disposition != contract.DispositionNotSent, NoCharge: true})
	var built builtEvidence
	if a.profile.Version == "zatiti.responses/v2" {
		built, err = buildPrepareSessionEvidenceV2(s.act.SessionID, handle, physical, usage, po.staged())
	} else {
		built, err = buildPrepareSessionEvidence(handle, physical, usage, po.staged())
	}
	if err != nil {
		return contract.Observation{}, err
	}
	return finishObservation(disposition, handle, built, physical.FinishedAt), nil
}

// invokeModelStep performs the one physical call already encoded as call:
// the model step, naming the session handle a prior prepare_session
// established (s.act.SessionHandle, admitted as a required, non-empty
// field of the action). A non-nil error means nothing was sent; once the
// request is handed to the transport, the outcome is reported through
// Observation with a nil error.
func (a *Adapter) invokeModelStep(ctx, callCtx context.Context, dispatch contract.Dispatch, s *step, call protocolCall) (contract.Observation, error) {
	po, err := a.physical(ctx, callCtx, call, s)
	if err != nil {
		return contract.Observation{}, err
	}
	staged := po.staged()

	physical := a.basePhysicalEvidence(dispatch, po)
	output := baseModelOutput(po)
	in := interpretation{secret: s.secret, bounds: s.bounds, inputBound: s.bound, classification: s.classification, stageErr: po.stageErr}
	if reason, message := po.failure(); reason != "" {
		if po.doErr != nil {
			physical.RequestSent, in.disposition, physical.Confirmation = classifyNetworkError(po.doErr)
			physical.ErrorCode = "transport_error"
			physical.ErrorMessage = sanitizeText(s.secret, message, maxMessageChars)
			output.Usage = a.profile.usageFor(usageInput{Bounds: s.bounds, Sent: in.disposition != contract.DispositionNotSent})
		} else {
			in.unknown(&physical, &output, a.profile, reason, message)
		}
	} else {
		result, decodeErr := a.protocol.decode(po.status, po.header, po.body)
		staged = in.interpret(ctx, a, po.status, result, decodeErr, &physical, &output, staged)
	}
	var built builtEvidence
	if a.profile.Version == "zatiti.responses/v2" {
		built, err = buildModelStepEvidenceV2(a.profile.SessionMode, s.act.SessionID, s.act.SessionHandle, physical, output, staged)
	} else {
		built, err = buildModelStepEvidence(s.act.SessionHandle, physical, output, staged)
	}
	if err != nil {
		return contract.Observation{}, err
	}
	reference := s.act.SessionHandle
	if a.profile.Version == "zatiti.responses/v2" && a.profile.SessionMode == "stateless" {
		reference = output.ResponseID
	}
	return finishObservation(in.disposition, reference, built, physical.FinishedAt), nil
}

// encodeStep translates the admitted step through the wire protocol,
// mapping a translation failure to the shared fault vocabulary.
func (a *Adapter) encodeStep(s *step, handle string) (protocolCall, error) {
	call, err := a.protocol.encode(s.request, handle)
	if err != nil {
		var f *contract.Fault
		if errors.As(err, &f) && f != nil {
			return protocolCall{}, f
		}
		return protocolCall{}, capabilityUnsupported("the qualified wire protocol cannot express this model step: %v", err)
	}
	if call.Method == "" || len(call.Body) == 0 {
		return protocolCall{}, internalError("wire protocol encoded an empty request")
	}
	return call, nil
}

// Reconcile implements contract.Adapter: one bounded, documented
// authoritative lookup for a model step whose outcome is unknown, keyed by
// Dispatch.ProviderKey -- the session handle the original model_step
// Invoke reported as Observation.ProviderReference. It never repeats the
// model step. prepare_session has no documented authoritative lookup
// (admitStep refuses it before any call); this method only ever resolves a
// model_step. The lookup can only confirm that the step completed; it
// cannot prove non-execution, so anything short of positive evidence
// leaves the outcome unknown, and a lookup that finds output but no usage
// never invents the missing usage -- the admitted worst case stays
// outstanding.
func (a *Adapter) Reconcile(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	s, err := a.admitStep(ctx, dispatch, true)
	if err != nil {
		return contract.Observation{}, err
	}
	if dispatch.ProviderKey == "" {
		return contract.Observation{}, prerequisiteMissing("reconciliation requires dispatch provider_key, the session handle the original attempt reported as its provider reference; the outcome remains unknown")
	}
	callCtx, cancel, err := a.callContext(ctx, dispatch.Deadline)
	if err != nil {
		return contract.Observation{}, err
	}
	defer cancel()

	call, err := a.protocol.reconcile(s.request.Profile, dispatch.ProviderKey)
	if err != nil {
		return contract.Observation{}, invalidInput("reconciliation lookup cannot be formed: %v", err)
	}
	po, err := a.physical(ctx, callCtx, call, s)
	if err != nil {
		return contract.Observation{}, err
	}
	staged := po.staged()
	physical := a.basePhysicalEvidence(dispatch, po)
	output := baseModelOutput(po)
	in := interpretation{secret: s.secret, bounds: s.bounds, inputBound: s.bound, classification: s.classification, stageErr: po.stageErr, reconcile: true}
	if reason, message := po.failure(); reason != "" {
		if po.doErr != nil {
			physical.RequestSent, _, _ = classifyNetworkError(po.doErr)
			physical.ErrorCode = "transport_error"
		} else {
			physical.ErrorCode = reason
		}
		// A failed lookup resolves nothing: the original outcome and its
		// reservation stay exactly as recorded.
		in.disposition = contract.DispositionUnknown
		physical.Confirmation = "unknown"
		physical.ErrorMessage = sanitizeText(s.secret, message, maxMessageChars)
		output.Usage = a.profile.usageFor(usageInput{Bounds: s.bounds, Sent: true})
	} else {
		result, decodeErr := a.protocol.decodeReconcile(po.status, po.header, po.body)
		staged = in.interpret(ctx, a, po.status, result, decodeErr, &physical, &output, staged)
	}
	built, err := buildModelStepEvidence(dispatch.ProviderKey, physical, output, staged)
	if err != nil {
		return contract.Observation{}, err
	}
	return finishObservation(in.disposition, dispatch.ProviderKey, built, physical.FinishedAt), nil
}

// physicalOutcome is what one physical call produced: the staged request
// record, the transport result, the bounded body and its staged copy.
type physicalOutcome struct {
	started, finished time.Time
	requestContext    wireStagedLocator
	stagedRequest     wireStagedOutput
	stagedResponse    *wireStagedOutput
	stageErr          error
	destination       string
	doErr             error
	status            int
	header            http.Header
	body              []byte
	readErr           error
	truncated         bool
}

// staged lists the outputs this call staged, request record first.
func (po *physicalOutcome) staged() []wireStagedOutput {
	out := []wireStagedOutput{po.stagedRequest}
	if po.stagedResponse != nil {
		out = append(out, *po.stagedResponse)
	}
	return out
}

// failure names why the call produced no complete, in-bounds body: a
// transport error, a body cut short after the status line, or a body
// beyond max_response_bytes. An empty reason means a full body arrived.
func (po *physicalOutcome) failure() (reason, message string) {
	switch {
	case po.doErr != nil:
		return "transport_error", po.doErr.Error()
	case po.readErr != nil:
		return "response_read_failed", po.readErr.Error()
	case po.truncated:
		return "max_response_bytes_exceeded", "response body exceeds the profile's max_response_bytes bound and was not interpreted"
	}
	return "", ""
}

// physical performs exactly one physical HTTP request: it checks the
// destination against the profile, stages the exact secret-free request
// record, sends once with no retry and no redirect, reads the response
// within max_response_bytes and stages it. A non-nil error means nothing
// was sent.
func (a *Adapter) physical(ctx, callCtx context.Context, call protocolCall, s *step) (*physicalOutcome, error) {
	if call.Method == "" || call.Destination == "" {
		return nil, internalError("wire protocol described an incomplete request")
	}
	if !a.profile.permitsDestination(call.Destination) {
		return nil, permissionDenied("wire protocol destination %q is not within enforcement.provider_destinations; declare the origin or that exact resource", redactURL(call.Destination))
	}
	stagedRequest, requestContext, err := stageRequestContext(ctx, a.deps.Blobs, s.secret, call, s.classification)
	if err != nil {
		return nil, err
	}
	po := &physicalOutcome{requestContext: requestContext, stagedRequest: stagedRequest, destination: call.Destination}

	var bodyReader io.Reader
	if len(call.Body) > 0 {
		bodyReader = bytes.NewReader(call.Body)
	}
	req, err := http.NewRequestWithContext(callCtx, call.Method, call.Destination, bodyReader)
	if err != nil {
		_ = a.deps.Blobs.RemoveStaged(ctx, stagedRequest.StagingRef)
		return nil, internalError("building the provider request failed")
	}
	// net/http may transparently replay a request whose body it can rewind.
	// One call is one physical request, so the body is made unrewindable.
	req.GetBody = nil
	for name, values := range call.Header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	a.protocol.authorize(req.Header, s.secret)

	po.started = a.now()
	resp, doErr := a.client.Do(req)
	if doErr != nil {
		po.finished = a.now()
		po.doErr = doErr
		return po, nil
	}
	defer func() { _ = resp.Body.Close() }()
	po.status = resp.StatusCode
	po.header = resp.Header

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, a.profile.MaxResponseBytes+1))
	po.finished = a.now()
	po.readErr = readErr
	if int64(len(body)) > a.profile.MaxResponseBytes {
		body = body[:a.profile.MaxResponseBytes]
		po.truncated = true
	}
	po.body = body

	// Best-effort: a staging failure must never erase an observation of a
	// call that may have been billed.
	if len(body) > 0 {
		mediaType := resp.Header.Get("Content-Type")
		if mediaType == "" || len(mediaType) > 256 {
			mediaType = "application/octet-stream"
		}
		stagedBody, err := stageBytes(ctx, a.deps.Blobs, scrubSecretBytes(s.secret, body), mediaType, s.classification, "provider_response")
		if err != nil {
			po.stageErr = err
		} else {
			po.stagedResponse = &stagedBody
		}
	}
	return po, nil
}

// basePhysicalEvidence starts the physical-call evidence for the call po,
// before its outcome is classified. Shared by prepare_session, model_step
// and Reconcile.
func (a *Adapter) basePhysicalEvidence(dispatch contract.Dispatch, po *physicalOutcome) wirePhysicalCallEvidence {
	physical := wirePhysicalCallEvidence{
		OperationID:          dispatch.OperationID,
		AttemptID:            dispatch.AttemptID,
		AccountIdentity:      "connection:" + string(a.profile.ConnectionID),
		RequestedDestination: po.destination,
		ResolvedDestination:  po.destination,
		ProfileDigest:        a.profile.Digest,
		CapabilityEvidence:   a.profile.CapabilityEvidence.Artifact,
		StartedAt:            po.started,
		FinishedAt:           po.finished,
		RequestContext:       po.requestContext,
		RequestSent:          "yes",
	}
	if po.doErr == nil {
		physical.HTTPStatus = int64(po.status)
	}
	return physical
}

// baseModelOutput starts the model output for the call po, before its
// outcome is classified. Only a model_step (Invoke or Reconcile) has one:
// prepare_session mints no model output.
func baseModelOutput(po *physicalOutcome) wireModelOutput {
	return wireModelOutput{
		Schema:         "zatiti.model-output/v1",
		RequestContext: po.requestContext,
		FinishReason:   finishUnknown,
	}
}

// now reads the injected clock in UTC, the only zone the frozen UTC
// timestamp format admits.
func (a *Adapter) now() time.Time { return a.deps.Clock.Now().UTC() }

// finishObservation assembles the Observation for one attempted physical
// call from its already-marshaled evidence and usage. Observation.Usage
// carries the accounting Usage the controller validates against
// $defs/Usage; the full ProviderUsage lives in the evidence.
func finishObservation(disposition, reference string, built builtEvidence, finishedAt time.Time) contract.Observation {
	var confirmedAt *time.Time
	if disposition == contract.DispositionSucceeded || disposition == contract.DispositionFailed {
		t := finishedAt
		confirmedAt = &t
	}
	return contract.Observation{
		Disposition:       disposition,
		ProviderReference: reference,
		Evidence:          built.doc,
		Usage:             built.usage,
		ConfirmedAt:       confirmedAt,
	}
}

// resolveCredential resolves dispatch.CredentialRef to secret bytes outside
// any transaction, as the shared contract requires of a trusted adapter.
func (a *Adapter) resolveCredential(ctx context.Context, ref string) ([]byte, error) {
	if ref == "" {
		return nil, invalidInput("dispatch credential_ref is empty")
	}
	if a.deps.Secrets == nil {
		return nil, prerequisiteMissing("responses adapter requires a secret store dependency to resolve credential %q", ref)
	}
	secret, err := a.deps.Secrets.Get(ctx, ref)
	if err != nil {
		return nil, secretFault(err)
	}
	if len(secret) == 0 {
		return nil, prerequisiteMissing("responses credential %q resolved to empty secret material", ref)
	}
	return secret, nil
}

// redactURL keeps a destination's scheme, host and path for a diagnostic
// and drops any query, which a lookup may carry.
func redactURL(destination string) string {
	if i := strings.IndexByte(destination, '?'); i >= 0 {
		return destination[:i] + "?..."
	}
	return destination
}
