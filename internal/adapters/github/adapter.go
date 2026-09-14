package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

const adapterName = "github"

// userAgent identifies this adapter build to GitHub. It never carries
// secret material.
const userAgent = "zatiti-github-adapter/1"

// Adapter is the qualified GitHub repository artifact/publication adapter.
type Adapter struct {
	profile *githubProfile
	deps    contract.AdapterDependencies
	client  *http.Client
	schema  json.RawMessage
}

// New constructs the GitHub adapter from a zatiti.github/v1 profile. The
// profile's capability_evidence must bind to its own bytes (see
// loadProfile); config is otherwise immutable for the adapter's lifetime.
func New(deps contract.AdapterDependencies, raw json.RawMessage) (contract.Adapter, error) {
	if deps.HTTP == nil {
		return nil, invalidInput("github adapter requires an HTTP client dependency")
	}
	if deps.Clock == nil {
		return nil, invalidInput("github adapter requires a clock dependency")
	}
	profile, err := loadProfile(raw)
	if err != nil {
		return nil, err
	}
	contractDoc, err := buildContractDocument()
	if err != nil {
		return nil, err
	}
	// A private client value that reuses the shared Transport (and its
	// connection pool) is used instead of deps.HTTP directly:
	// AdapterDependencies.HTTP is shared across every adapter, so
	// CheckRedirect cannot be set on it without affecting siblings.
	// Redirects are never followed: "HTTP redirects cannot widen
	// host/account scope."
	client := &http.Client{
		Transport: deps.HTTP.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Adapter{profile: profile, deps: deps, client: client, schema: contractDoc}, nil
}

// Name implements contract.Adapter.
func (a *Adapter) Name() string { return adapterName }

// Contract implements contract.Adapter, describing the local frozen seam:
// the profile, parameters and evidence schemas this adapter validates
// against.
func (a *Adapter) Contract() json.RawMessage { return a.schema }

// Invoke implements contract.Adapter: exactly one physical GitHub REST call
// for dispatch's action.
func (a *Adapter) Invoke(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	return a.call(ctx, dispatch, false)
}

// Reconcile implements contract.Adapter: one bounded read checking the
// current provider state of dispatch's original effect. It never repeats
// the original mutating call.
func (a *Adapter) Reconcile(ctx context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	return a.call(ctx, dispatch, true)
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
		return nil, internalError("github contract profile schema composition failed: %v", err)
	}
	as, err := parametersSchema()
	if err != nil {
		return nil, internalError("github contract parameters schema composition failed: %v", err)
	}
	es, err := evidenceSchema()
	if err != nil {
		return nil, internalError("github contract evidence schema composition failed: %v", err)
	}
	doc := contractDocument{Schema: "zatiti.github.contract/v1", ProfileSchema: ps, ParametersSchema: as, EvidenceSchema: es}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, internalError("github contract document encoding failed: %v", err)
	}
	return out, nil
}

// call implements both Invoke (reconcile=false) and Reconcile
// (reconcile=true). It performs exactly one physical HTTP request: no
// hidden retry, no unaccounted preflight read. A non-nil error means the
// call was never attempted (invalid input, an action outside the profile's
// allowlists, or an automation bound the profile does not permit); once
// the request is actually sent, the outcome is reported through
// Observation with a nil error.
func (a *Adapter) call(ctx context.Context, dispatch contract.Dispatch, reconcile bool) (contract.Observation, error) {
	if dispatch.Adapter != adapterName {
		return contract.Observation{}, invalidInput("dispatch adapter %q does not match %q", dispatch.Adapter, adapterName)
	}

	act, err := decodeAction(dispatch.Action)
	if err != nil {
		return contract.Observation{}, err
	}
	if !a.profile.allowsRepository(act.Repository) {
		return contract.Observation{}, permissionDenied("repository %s is not in the profile's allowed_repositories", act.Repository.slug())
	}
	if !a.profile.allowsAction(act.Kind) {
		return contract.Observation{}, capabilityUnsupported("action kind %q is not in the profile's allowed_actions", act.Kind)
	}
	if automation := actionAutomation(act); automation != nil {
		if boundsErr := a.profile.automationWithinBounds(*automation); boundsErr != nil {
			return contract.Observation{}, permissionDenied("%v", boundsErr)
		}
	}

	secret, err := a.resolveCredential(ctx, dispatch.CredentialRef)
	if err != nil {
		return contract.Observation{}, err
	}

	var method, path string
	var body []byte
	if reconcile {
		method, path, err = buildReconcileRequest(act)
	} else {
		method, path, body, err = buildRequest(ctx, a.deps.Blobs, act)
	}
	if err != nil {
		return contract.Observation{}, err
	}

	destination := strings.TrimRight(a.profile.APIBase, "/") + path

	callCtx, cancel := callContext(ctx, a.deps.Clock, a.profile.Timeout, dispatch.Deadline)
	defer cancel()

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(callCtx, method, destination, bodyReader)
	if err != nil {
		return contract.Observation{}, internalError("building github request failed: %v", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+string(secret))
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	started := a.deps.Clock.Now()
	resp, doErr := a.client.Do(req)
	finished := a.deps.Clock.Now()

	physical := wirePhysicalCallEvidence{
		OperationID:          dispatch.OperationID,
		AttemptID:            dispatch.AttemptID,
		AccountIdentity:      "credential:" + dispatch.CredentialRef,
		RequestedDestination: destination,
		ResolvedDestination:  destination,
		ProfileDigest:        a.profile.Digest,
		CapabilityEvidence:   a.profile.CapabilityEvidence.Artifact,
		StartedAt:            started,
		FinishedAt:           finished,
		RequestContext:       requestContextRef(a.profile),
	}

	if doErr != nil {
		requestSent, disposition, confirmation := classifyNetworkError(doErr)
		physical.RequestSent = requestSent
		physical.Confirmation = confirmation
		physical.ErrorMessage = truncateText(scrubSecret(secret, doErr.Error()), 2048)
		built, evErr := a.buildEvidence(act, physical, nil, evidenceFields{})
		if evErr != nil {
			return contract.Observation{}, evErr
		}
		return contract.Observation{Disposition: disposition, Evidence: built.doc, Usage: built.usage}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	physical.RequestSent = "yes"
	physical.HTTPStatus = int64(resp.StatusCode)

	respBody, _ := readBounded(resp.Body, a.profile.MaxResponseBytes) // disposition is driven by status, never truncated

	is2xx := resp.StatusCode >= 200 && resp.StatusCode < 300
	disposition := contract.DispositionFailed
	if is2xx {
		disposition = contract.DispositionSucceeded
		physical.Confirmation = "authoritative_success"
	} else {
		physical.Confirmation = "authoritative_failure"
		physical.ErrorCode = fmt.Sprintf("http_%d", resp.StatusCode)
		var ghErr ghErrorResponse
		if json.Unmarshal(respBody, &ghErr) == nil && ghErr.Message != "" {
			physical.ErrorMessage = truncateText(scrubSecret(secret, ghErr.Message), 2048)
		}
	}

	staged, _ := stageProviderResponse(ctx, a.deps.Blobs, respBody, resp.Header.Get("Content-Type")) // best-effort; never discards a confirmed observation

	var fields evidenceFields
	switch {
	case is2xx && reconcile:
		fields = interpretReconcileResponse(act, respBody)
		disposition = fields.disposition
		physical.Confirmation = fields.confirmation
	case is2xx:
		fields = interpretInvokeResponse(act, respBody)
	case reconcile:
		fields = interpretReconcileFailure()
		disposition = fields.disposition
		physical.Confirmation = fields.confirmation
	}

	built, evErr := a.buildEvidence(act, physical, staged, fields)
	if evErr != nil {
		return contract.Observation{}, evErr
	}

	var confirmedAt *time.Time
	if disposition == contract.DispositionSucceeded || disposition == contract.DispositionFailed {
		t := finished
		confirmedAt = &t
	}

	return contract.Observation{
		Disposition:       disposition,
		ProviderReference: fields.providerReference(),
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
		return nil, prerequisiteMissing("github adapter requires a secret store dependency to resolve credential %q", ref)
	}
	secret, err := a.deps.Secrets.Get(ctx, ref)
	if err != nil {
		return nil, secretFault(err)
	}
	if len(secret) == 0 {
		return nil, prerequisiteMissing("github credential %q resolved to empty secret material", ref)
	}
	return secret, nil
}

type builtEvidence struct {
	doc   json.RawMessage
	usage json.RawMessage
}

// buildEvidence assembles and marshals the zatiti.github.evidence/v1
// document, plus a standalone marshal of its usage for
// Observation.Usage.
func (a *Adapter) buildEvidence(act *action, physical wirePhysicalCallEvidence, staged *wireStagedOutput, fields evidenceFields) (builtEvidence, error) {
	usage := noChargeUsage()
	usageDoc, err := json.Marshal(usage)
	if err != nil {
		return builtEvidence{}, internalError("encoding github usage failed: %v", err)
	}

	stagedOutputs := []wireStagedOutput{}
	if staged != nil {
		stagedOutputs = append(stagedOutputs, *staged)
	}

	ev := wireGitHubEvidence{
		Schema:            "zatiti.github.evidence/v1",
		PhysicalCall:      physical,
		Kind:              act.Kind,
		Repository:        act.Repository,
		Usage:             usage,
		StagedOutputs:     stagedOutputs,
		OutputArtifacts:   []wireArtifactRef{},
		Branch:            fields.branch,
		ObservedHeadSHA:   fields.observedHeadSHA,
		ObservedBaseSHA:   fields.observedBaseSHA,
		CommitSHA:         fields.commitSHA,
		PullRequestNumber: fields.pullRequestNumber,
		PullRequestURL:    fields.pullRequestURL,
	}
	doc, err := json.Marshal(ev)
	if err != nil {
		return builtEvidence{}, internalError("encoding github evidence failed: %v", err)
	}
	return builtEvidence{doc: doc, usage: usageDoc}, nil
}
