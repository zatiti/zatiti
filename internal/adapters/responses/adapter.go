package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
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
// machinery through a synthetic protocol while production keeps the
// (empty) qualified registry.
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
		protocol: protocols[profile.CapabilityEvidence.ProtocolRevision],
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
		ContextSchema:              cs,
		QualifiedProtocolRevisions: revisions,
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, internalError("responses contract document encoding failed: %v", err)
	}
	return out, nil
}

// Reconcile implements contract.Adapter. It never performs a physical
// call: reconciliation is "only documented authoritative lookup under
// qualified retention, otherwise preserve unknown", and the frozen profile
// schema carries neither a lookup contract nor a retention window. Issuing
// the model step again would be a second paid mutation, not a read. The
// refusal leaves the original attempt's unknown outcome and reservation
// exactly as recorded.
func (a *Adapter) Reconcile(_ context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	if dispatch.Adapter != adapterName {
		return contract.Observation{}, invalidInput("dispatch adapter %q does not match %q", dispatch.Adapter, adapterName)
	}
	return contract.Observation{}, capabilityUnsupported("responses adapter has no qualified authoritative lookup or retention window; the original outcome remains unknown and no call was made")
}

// Invoke implements contract.Adapter: at most one physical HTTP request for
// dispatch's model step. A non-nil error means no request was attempted;
// once the request is handed to the transport, the outcome is reported
// through Observation with a nil error.
func (a *Adapter) Invoke(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	if dispatch.Adapter != adapterName {
		return contract.Observation{}, invalidInput("dispatch adapter %q does not match %q", dispatch.Adapter, adapterName)
	}
	act, err := decodeAction(dispatch.Action)
	if err != nil {
		return contract.Observation{}, err
	}
	if act.MaxOutputTokens > a.profile.MaxOutputTokens {
		return contract.Observation{}, budgetUnavailable("action max_output_tokens %d exceeds the profile's max_output_tokens %d", act.MaxOutputTokens, a.profile.MaxOutputTokens)
	}
	doc, err := loadContext(ctx, a.deps.Blobs, a.profile, act)
	if err != nil {
		return contract.Observation{}, err
	}

	// The exact unspecified boundary: everything above is defined by the
	// frozen Zatiti-side contract; the upstream request is not.
	if a.protocol == nil {
		return contract.Observation{}, capabilityUnsupported(
			"wire protocol revision %q is not qualified in this build: the upstream request and response shapes are pinned only by real-endpoint qualification, and none has been performed; no call was made",
			a.profile.CapabilityEvidence.ProtocolRevision)
	}
	limits := a.protocol.limits()
	if act.ContinuationReference != "" && !limits.SupportsContinuation {
		return contract.Observation{}, capabilityUnsupported("the qualified wire protocol cannot express continuation_reference")
	}
	call, err := a.protocol.encode(protocolRequest{
		Model:                 a.profile.Model,
		MaxOutputTokens:       act.MaxOutputTokens,
		Context:               doc,
		ContinuationReference: act.ContinuationReference,
	})
	if err != nil {
		var f *contract.Fault
		if errors.As(err, &f) && f != nil {
			return contract.Observation{}, f
		}
		return contract.Observation{}, capabilityUnsupported("the qualified wire protocol cannot express this model step: %v", err)
	}
	if call.Method == "" || len(call.Body) == 0 {
		return contract.Observation{}, internalError("wire protocol encoded an empty request")
	}

	bounds, err := a.profile.admit(act.MaxOutputTokens, limits, call.InputTokenBound)
	if err != nil {
		return contract.Observation{}, err
	}

	secret, err := a.resolveCredential(ctx, dispatch.CredentialRef)
	if err != nil {
		return contract.Observation{}, err
	}

	remaining := a.profile.Timeout
	if !dispatch.Deadline.IsZero() {
		if untilDeadline := dispatch.Deadline.Sub(a.deps.Clock.Now()); untilDeadline < remaining {
			remaining = untilDeadline
		}
	}
	if remaining <= 0 {
		return contract.Observation{}, invalidInput("dispatch deadline has already passed; no call was made")
	}

	// Persist the literal model-visible upstream request before dispatch.
	// The body is secret-free by the wireProtocol contract.
	stagedRequest, err := stageBytes(ctx, a.deps.Blobs, call.Body, "application/octet-stream", doc.Classification, "context")
	if err != nil {
		return contract.Observation{}, err
	}
	staged := []wireStagedOutput{stagedRequest}

	// The timeout is measured on the injected clock but applied as a
	// duration, so a test clock pinned in the past cannot pre-expire it.
	callCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, call.Method, a.profile.Endpoint, bytes.NewReader(call.Body))
	if err != nil {
		_ = a.deps.Blobs.RemoveStaged(ctx, stagedRequest.StagingRef)
		return contract.Observation{}, internalError("building responses request failed")
	}
	// net/http may transparently replay a request whose body it can rewind.
	// One Invoke is one physical request, so the body is made unrewindable.
	req.GetBody = nil
	for name, values := range call.Header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	a.protocol.authorize(req.Header, secret)

	started := a.now()
	resp, doErr := a.client.Do(req)

	physical := wirePhysicalCallEvidence{
		OperationID:          dispatch.OperationID,
		AttemptID:            dispatch.AttemptID,
		AccountIdentity:      "connection:" + string(a.profile.ConnectionID),
		RequestedDestination: a.profile.Endpoint,
		ResolvedDestination:  a.profile.Endpoint,
		ProfileDigest:        a.profile.Digest,
		CapabilityEvidence:   a.profile.CapabilityEvidence.Artifact,
		StartedAt:            started,
		RequestContext:       requestContextRef(act),
	}
	output := wireModelOutput{
		Schema:         "zatiti.model-output/v1",
		RequestContext: requestContextRef(act),
		FinishReason:   finishUnknown,
	}

	if doErr != nil {
		physical.FinishedAt = a.now()
		requestSent, disposition, confirmation := classifyNetworkError(doErr)
		physical.RequestSent = requestSent
		physical.Confirmation = confirmation
		physical.ErrorCode = "transport_error"
		physical.ErrorMessage = sanitizeText(secret, doErr.Error(), maxMessageChars)
		output.Usage = a.profile.usageFor(usageInput{Bounds: bounds, Sent: disposition != contract.DispositionNotSent})
		return observe(disposition, physical, output, staged)
	}
	defer func() { _ = resp.Body.Close() }()

	physical.RequestSent = "yes"
	physical.HTTPStatus = int64(resp.StatusCode)

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, a.profile.MaxResponseBytes+1))
	physical.FinishedAt = a.now()
	truncated := int64(len(body)) > a.profile.MaxResponseBytes
	if truncated {
		body = body[:a.profile.MaxResponseBytes]
	}

	// Best-effort: a staging failure must never erase an observation of a
	// call that may have been billed.
	var stageErr error
	if len(body) > 0 {
		mediaType := resp.Header.Get("Content-Type")
		if mediaType == "" || len(mediaType) > 256 {
			mediaType = "application/octet-stream"
		}
		stagedBody, err := stageBytes(ctx, a.deps.Blobs, scrubSecretBytes(secret, body), mediaType, doc.Classification, "provider_response")
		if err != nil {
			stageErr = err
		} else {
			staged = append(staged, stagedBody)
		}
	}

	in := interpretation{secret: secret, bounds: bounds, classification: doc.Classification, stageErr: stageErr}
	switch {
	case readErr != nil:
		// Headers arrived but the body did not: the model step may have
		// run and been billed. This is the timeout-after-success case.
		in.unknown(&physical, &output, a.profile, "response_read_failed", readErr.Error())
	case truncated:
		in.unknown(&physical, &output, a.profile, "max_response_bytes_exceeded",
			fmt.Sprintf("response body exceeds the profile's %d byte bound and was not interpreted", a.profile.MaxResponseBytes))
	default:
		result, decodeErr := a.protocol.decode(resp.StatusCode, resp.Header, body)
		staged = in.interpret(ctx, a, resp, result, decodeErr, &physical, &output, staged)
	}
	return observe(in.disposition, physical, output, staged)
}

// now reads the injected clock in UTC, the only zone the frozen UTC
// timestamp format admits.
func (a *Adapter) now() time.Time { return a.deps.Clock.Now().UTC() }

// observe assembles the Observation for one attempted physical call.
func observe(disposition string, physical wirePhysicalCallEvidence, output wireModelOutput, staged []wireStagedOutput) (contract.Observation, error) {
	built, err := buildEvidence(physical, output, staged)
	if err != nil {
		return contract.Observation{}, err
	}
	var confirmedAt *time.Time
	if disposition == contract.DispositionSucceeded || disposition == contract.DispositionFailed {
		t := physical.FinishedAt
		confirmedAt = &t
	}
	return contract.Observation{
		Disposition:       disposition,
		ProviderReference: output.ResponseID,
		Evidence:          built.doc,
		Usage:             built.usage,
		ConfirmedAt:       confirmedAt,
	}, nil
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
