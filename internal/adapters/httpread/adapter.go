package httpread

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

const adapterName = "httpread"

// defaultUserAgent identifies this adapter build to a fetched origin when
// the action does not itself supply a User-Agent header (User-Agent is one
// of the permitted action headers, so a caller may still override this).
const defaultUserAgent = "zatiti-httpread-adapter/1"

// Adapter is the qualified bounded public HTTP read adapter.
type Adapter struct {
	profile    *httpreadProfile
	deps       contract.AdapterDependencies
	client     *http.Client
	schema     json.RawMessage
	resolveIPs ipLookupFunc
}

// New constructs the httpread adapter from a zatiti.httpread/v1 profile.
// The profile's capability_evidence must bind to its own bytes (see
// loadProfile); config is otherwise immutable for the adapter's lifetime.
func New(deps contract.AdapterDependencies, raw json.RawMessage) (contract.Adapter, error) {
	if deps.HTTP == nil {
		return nil, invalidInput("httpread adapter requires an HTTP client dependency")
	}
	if deps.Clock == nil {
		return nil, invalidInput("httpread adapter requires a clock dependency")
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
	// deps.HTTP.Transport: this adapter's DialContext (pinnedDialContext)
	// IS its security boundary, resolving, validating and pinning every
	// destination address it dials. Reusing a shared Transport risks
	// inheriting a proxy or dial override configured for a sibling adapter
	// that would route connections around that validation entirely.
	transport := &http.Transport{DialContext: pinnedDialContext}
	client := &http.Client{
		Transport: transport,
		// max_redirects is frozen at 0: a redirect is never followed
		// automatically, it is reported as a failed read whose evidence
		// carries redirect_location for an explicit follow-up read.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Adapter{
		profile:    profile,
		deps:       deps,
		client:     client,
		schema:     contractDoc,
		resolveIPs: net.DefaultResolver.LookupIPAddr,
	}, nil
}

// Name implements contract.Adapter.
func (a *Adapter) Name() string { return adapterName }

// Contract implements contract.Adapter, describing the local frozen seam:
// the profile, parameters and evidence schemas this adapter validates
// against.
func (a *Adapter) Contract() json.RawMessage { return a.schema }

// Invoke implements contract.Adapter: exactly one bounded GET for
// dispatch's action.
func (a *Adapter) Invoke(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	return a.call(ctx, dispatch)
}

// Reconcile implements contract.Adapter. Unlike a mutation adapter, a GET
// has no separate remote side effect to check against: the effect of a
// qualified httpread action is the bounded read itself, so honestly
// reconciling it means repeating exactly that read, not a distinct, cheaper
// check.
func (a *Adapter) Reconcile(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	return a.call(ctx, dispatch)
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
		return nil, internalError("httpread contract profile schema composition failed: %v", err)
	}
	as, err := parametersSchema()
	if err != nil {
		return nil, internalError("httpread contract parameters schema composition failed: %v", err)
	}
	es, err := evidenceSchema()
	if err != nil {
		return nil, internalError("httpread contract evidence schema composition failed: %v", err)
	}
	doc := contractDocument{Schema: "zatiti.httpread.contract/v1", ProfileSchema: ps, ParametersSchema: as, EvidenceSchema: es}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, internalError("httpread contract document encoding failed: %v", err)
	}
	return out, nil
}

// call implements both Invoke and Reconcile. It performs at most one
// physical HTTP GET: no hidden retry, no unaccounted preflight read. A
// non-nil error means no physical request was sent -- invalid input, a
// destination outside the profile's allowed origins/media types, a
// destination resolving only to disallowed addresses, a request record that
// could not be staged before sending, or a transport failure before any
// response line arrived (see classifyPreResponseFailure
// and this package's doc.go for why the latter cannot be reported as an
// Observation). Once a response line is received, the outcome is always
// reported through Observation with a nil error.
func (a *Adapter) call(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	if dispatch.Adapter != adapterName {
		return contract.Observation{}, invalidInput("dispatch adapter %q does not match %q", dispatch.Adapter, adapterName)
	}

	act, err := decodeAction(dispatch.Action)
	if err != nil {
		return contract.Observation{}, err
	}
	if !a.profile.allowsOrigin(act.origin) {
		return contract.Observation{}, permissionDenied("origin %q is not in the profile's allowed_origins", act.origin)
	}
	if !a.profile.allowsMediaType(act.ExpectedMediaType) {
		return contract.Observation{}, capabilityUnsupported("expected_media_type %q is not in the profile's allowed_media_types", act.ExpectedMediaType)
	}

	host := act.parsedURL.Hostname()
	port := act.parsedURL.Port()
	if port == "" {
		port = defaultPortForScheme(act.parsedURL.Scheme)
	}
	validated, lookupErr := resolveValidatedIPs(ctx, a.resolveIPs, host)
	if lookupErr != nil {
		return contract.Observation{}, classifyPreResponseFailure(lookupErr)
	}
	if len(validated) == 0 {
		return contract.Observation{}, permissionDenied("host %q has no permitted dial address (loopback/private/link-local/metadata refused)", host)
	}
	pinnedAddr := net.JoinHostPort(validated[0].String(), port)
	dialAddresses := make([]string, len(validated))
	for i, ip := range validated {
		dialAddresses[i] = ip.String()
	}

	callCtx, cancel := callContext(ctx, a.deps.Clock, a.profile.Timeout, dispatch.Deadline)
	defer cancel()
	reqCtx := withPinnedDial(callCtx, pinnedAddr)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, act.URL, nil)
	if err != nil {
		return contract.Observation{}, internalError("building httpread request failed: %v", err)
	}
	hasUserAgent := false
	for _, h := range act.Headers {
		req.Header.Set(h.Name, h.Value)
		if h.Name == "User-Agent" {
			hasUserAgent = true
		}
	}
	if !hasUserAgent {
		req.Header.Set("User-Agent", defaultUserAgent)
	}

	// The exact request record is persisted before any request byte is
	// written; if it cannot be, nothing is sent.
	requestContext, requestStaged, err := stageRequestRecord(ctx, a.deps.Blobs, req, requestRecord{
		Schema: requestRecordSchema, Method: http.MethodGet, URL: act.URL, PinnedAddress: pinnedAddr,
	})
	if err != nil {
		return contract.Observation{}, err
	}

	started := a.deps.Clock.Now()
	resp, doErr := a.client.Do(req)
	if doErr != nil {
		return contract.Observation{}, classifyPreResponseFailure(doErr)
	}
	defer func() { _ = resp.Body.Close() }()

	physical := wirePhysicalCallEvidence{
		OperationID:          dispatch.OperationID,
		AttemptID:            dispatch.AttemptID,
		AccountIdentity:      "public",
		RequestedDestination: act.URL,
		ResolvedDestination:  pinnedAddr,
		ProfileDigest:        a.profile.Digest,
		CapabilityEvidence:   a.profile.CapabilityEvidence.Artifact,
		StartedAt:            started,
		RequestContext:       requestContext,
		RequestSent:          "yes",
		HTTPStatus:           int64(resp.StatusCode),
	}

	base := wireHTTPReadEvidence{
		Schema:                 "zatiti.httpread.evidence/v1",
		RequestedURL:           act.URL,
		ResolvedURL:            act.URL, // no redirect is ever followed in v1, so the fetched URL never differs from the requested one
		ValidatedDialAddresses: dialAddresses,
		Status:                 int64(resp.StatusCode),
		MediaType:              parseMediaType(resp.Header.Get("Content-Type")),
		Usage:                  noChargeUsage(),
		StagedOutputs:          []wireStagedOutput{requestStaged}, // the request record; the controller publishes it
		OutputArtifacts:        []wireArtifactRef{},               // the adapter cannot mint artifact IDs
		ETag:                   resp.Header.Get("ETag"),
		LastModified:           resp.Header.Get("Last-Modified"),
	}

	isRedirect := resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != ""
	if isRedirect {
		return a.recordRedirect(ctx, act, physical, base, resp)
	}
	return a.recordBoundedRead(ctx, act, physical, base, resp)
}

// recordRedirect handles a 3xx response carrying a Location header. Since
// max_redirects is frozen at 0, the redirect is never followed and its body
// is never consumed -- "Enforce response size/time bounds before consuming
// unlimited bytes" applies most simply here as consuming none at all.
func (a *Adapter) recordRedirect(_ context.Context, act *action, physical wirePhysicalCallEvidence, base wireHTTPReadEvidence, resp *http.Response) (contract.Observation, error) {
	finished := a.deps.Clock.Now()
	physical.FinishedAt = finished
	physical.Confirmation = "authoritative_failure"
	physical.ErrorCode = "redirect_not_permitted"
	physical.ErrorMessage = "max_redirects is 0; issue a new explicit read for the redirect target"

	location := resp.Header.Get("Location")
	if resolved, rerr := act.parsedURL.Parse(location); rerr == nil {
		location = resolved.String()
	}
	base.Freshness = finished
	base.RedirectLocation = location

	built, evErr := a.buildEvidence(physical, base)
	if evErr != nil {
		return contract.Observation{}, evErr
	}
	return contract.Observation{
		Disposition: contract.DispositionFailed,
		Evidence:    built.doc,
		Usage:       built.usage,
		ConfirmedAt: &finished,
	}, nil
}

// recordBoundedRead reads resp.Body up to the profile's max_bytes bound (one
// byte past it, to distinguish "exactly at the bound" from "exceeds it")
// and reports the outcome.
func (a *Adapter) recordBoundedRead(ctx context.Context, act *action, physical wirePhysicalCallEvidence, base wireHTTPReadEvidence, resp *http.Response) (contract.Observation, error) {
	limited := io.LimitReader(resp.Body, a.profile.MaxBytes+1)
	data, readErr := io.ReadAll(limited)
	finished := a.deps.Clock.Now()
	physical.FinishedAt = finished
	base.Freshness = finished

	if readErr != nil {
		// The status line (and headers) already arrived -- physical.
		// HTTPStatus is set -- but the body did not fully arrive: this is
		// the "timeout-after-success unknown" case, distinct from a
		// pre-response failure, which never reaches this far.
		physical.Confirmation = "unknown"
		physical.ErrorMessage = truncateText(readErr.Error(), 2048)
		built, evErr := a.buildEvidence(physical, base)
		if evErr != nil {
			return contract.Observation{}, evErr
		}
		return contract.Observation{Disposition: contract.DispositionUnknown, Evidence: built.doc, Usage: built.usage}, nil
	}

	if int64(len(data)) > a.profile.MaxBytes {
		physical.Confirmation = "authoritative_failure"
		physical.ErrorCode = "max_bytes_exceeded"
		physical.ErrorMessage = fmt.Sprintf("response body exceeds the profile's %d byte bound", a.profile.MaxBytes)
		built, evErr := a.buildEvidence(physical, base)
		if evErr != nil {
			return contract.Observation{}, evErr
		}
		return contract.Observation{Disposition: contract.DispositionFailed, Evidence: built.doc, Usage: built.usage, ConfirmedAt: &finished}, nil
	}

	isSuccessStatus := (resp.StatusCode >= 200 && resp.StatusCode < 300) || resp.StatusCode == http.StatusNotModified

	disposition := contract.DispositionFailed
	physical.Confirmation = "authoritative_failure"
	switch {
	case !isSuccessStatus:
		physical.ErrorCode = fmt.Sprintf("http_%d", resp.StatusCode)
	case act.ExpectedMediaType != "" && base.MediaType != "" && base.MediaType != act.ExpectedMediaType:
		physical.ErrorCode = "unexpected_media_type"
		physical.ErrorMessage = fmt.Sprintf("expected media type %q, received %q", act.ExpectedMediaType, base.MediaType)
	default:
		disposition = contract.DispositionSucceeded
		physical.Confirmation = "authoritative_success"
	}

	digest := contract.Hash(data)
	size := int64(len(data))
	base.ContentDigest = digest
	base.ContentSize = &size
	staged, stageErr := stageContent(ctx, a.deps.Blobs, data, base.MediaType)
	if stageErr != nil {
		return contract.Observation{}, stageErr
	}
	if staged != nil {
		base.StagedOutputs = append(base.StagedOutputs, *staged)
	}

	built, evErr := a.buildEvidence(physical, base)
	if evErr != nil {
		return contract.Observation{}, evErr
	}
	t := finished
	return contract.Observation{Disposition: disposition, Evidence: built.doc, Usage: built.usage, ConfirmedAt: &t}, nil
}

type builtEvidence struct {
	doc   json.RawMessage
	usage json.RawMessage
}

// buildEvidence assembles and marshals the zatiti.httpread.evidence/v1
// document, plus a standalone marshal of its accounting Usage for
// Observation.Usage. The controller validates Observation.Usage against
// $defs/Usage (exactly currency, spent, reserved, estimated, unknown,
// advisory) and replaces anything else with synthesized unknown billing,
// so the nested ProviderUsage stays in the evidence only.
func (a *Adapter) buildEvidence(physical wirePhysicalCallEvidence, base wireHTTPReadEvidence) (builtEvidence, error) {
	usageDoc, err := json.Marshal(base.Usage.Accounting)
	if err != nil {
		return builtEvidence{}, internalError("encoding httpread usage failed: %v", err)
	}
	base.PhysicalCall = physical
	doc, err := json.Marshal(base)
	if err != nil {
		return builtEvidence{}, internalError("encoding httpread evidence failed: %v", err)
	}
	return builtEvidence{doc: doc, usage: usageDoc}, nil
}

// parseMediaType returns contentType's base media type (its parameters,
// such as charset, stripped), matching what expected_media_type compares
// against. An unparseable or absent Content-Type yields the original
// (possibly empty) string rather than an error: a malformed header is
// evidence in its own right, not a reason to abort a completed read.
func parseMediaType(contentType string) string {
	if contentType == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return contentType
	}
	return mt
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
