package qualification_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/adapters/responses"
	"github.com/zatiti/zatiti/internal/contract"
)

const (
	openRouterProbeGate     = "ZATITI_QUALIFY_OPENROUTER_PROBE"
	openRouterCredentialSvc = "ZATITI_QUALIFY_KEYCHAIN_SERVICE"
	openRouterCredentialAcc = "ZATITI_QUALIFY_KEYCHAIN_ACCOUNT"
	openRouterConnectionID  = "ZATITI_QUALIFY_CONNECTION_ID"
	openRouterModel         = "ZATITI_QUALIFY_MODEL"
	openRouterRouting       = "ZATITI_QUALIFY_OPENROUTER_ROUTING_JSON"
	openRouterCostCeiling   = "ZATITI_QUALIFY_MAX_COST_MICRO_USD"
	openRouterEndpoint      = "https://openrouter.ai/api/v1/responses"
	openRouterMaxInput      = int64(256)
	openRouterMaxOutput     = int64(16)
)

// openRouterKeychain resolves a credential by a non-secret Keychain locator.
// The key is returned directly to the adapter and is never included in output.
type openRouterKeychain struct{ service, account, ref string }

func (s openRouterKeychain) Put(context.Context, string, []byte) (string, error) {
	return "", errors.New("qualification never writes credentials")
}

func (s openRouterKeychain) Lookup(_ context.Context, ref string) (string, error) {
	if ref != s.ref {
		return "", &contract.Fault{Code: contract.CodeNotFound, Message: "unknown credential reference"}
	}
	return ref, nil
}

func (s openRouterKeychain) Get(ctx context.Context, ref string) ([]byte, error) {
	if ref != s.ref {
		return nil, &contract.Fault{Code: contract.CodeNotFound, Message: "unknown credential reference"}
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", s.service, "-a", s.account, "-w").Output()
	if err != nil {
		return nil, &contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "the selected Keychain credential is not readable"}
	}
	return bytes.TrimRight(out, "\r\n"), nil
}

func (openRouterKeychain) Delete(context.Context, string) error {
	return errors.New("qualification never deletes credentials")
}

type openRouterPriceCeiling struct {
	Currency string `json:"currency"`
	Input    string `json:"input_per_million"`
	Output   string `json:"output_per_million"`
}

type openRouterRouteSelection struct {
	Only              []string               `json:"only"`
	AllowFallbacks    bool                   `json:"allow_fallbacks"`
	RequireParameters bool                   `json:"require_parameters"`
	Privacy           []string               `json:"privacy,omitempty"`
	PriceCeiling      openRouterPriceCeiling `json:"price_ceiling"`
}

// TestOpenRouterProfileQualificationProbe performs one explicitly authorized,
// fixed-prompt probe against the selected OpenRouter model and a single pinned
// upstream route. It records a not_run result unless every input is supplied;
// the API key itself is read only from the named macOS Keychain item.
func TestOpenRouterProfileQualificationProbe(t *testing.T) {
	c := beginCase(t, "QUALIFICATION.responses/openrouter-profile-probe", "QUALIFICATION",
		"One fixed, bounded probe uses the explicitly selected OpenRouter model and exactly one pinned upstream route.",
		"The provider receives an enforced route price ceiling and the adapter performs exactly one physical request.",
		"The credential is resolved from Keychain and is absent from retained evidence, request records and diagnostics.",
		"Record source, model, route, profile digest, observed usage and physical request count without claiming full first-chat qualification.")
	if os.Getenv(openRouterProbeGate) != "1" {
		c.notRun("%s is not 1; the probe is a billable live request and requires explicit authorization", openRouterProbeGate)
	}
	service := strings.TrimSpace(os.Getenv(openRouterCredentialSvc))
	account := strings.TrimSpace(os.Getenv(openRouterCredentialAcc))
	connectionID := strings.TrimSpace(os.Getenv(openRouterConnectionID))
	model := strings.TrimSpace(os.Getenv(openRouterModel))
	routingRaw := strings.TrimSpace(os.Getenv(openRouterRouting))
	maxCost, costErr := strconv.ParseInt(strings.TrimSpace(os.Getenv(openRouterCostCeiling)), 10, 64)
	if service == "" || account == "" || connectionID == "" || model == "" || routingRaw == "" || costErr != nil || maxCost <= 0 {
		c.notRun("live probe requires Keychain service/account, connection UUID, model, pinned routing JSON and a positive %s", openRouterCostCeiling)
	}

	route, inputRate, outputRate, err := parseProbeRoute(routingRaw)
	if err != nil {
		c.fail("%s: %v", openRouterRouting, err)
	}
	maximum, err := probeWorstCaseCost(inputRate, outputRate)
	if err != nil {
		c.fail("price ceiling cannot be represented safely: %v", err)
	}
	if maximum > maxCost {
		c.fail("configured maximum-cost ceiling %d micro-USD is below the probe's integer worst-case reservation %d micro-USD", maxCost, maximum)
	}

	secrets := openRouterKeychain{service: service, account: account, ref: "keychain:" + service + "/" + account}
	secret, err := secrets.Get(context.Background(), secrets.ref)
	if err != nil || len(secret) == 0 {
		c.notRun("selected Keychain item is not readable; no provider request was made")
	}
	defer clear(secret)

	probe := openRouterProbe{model: model, connectionID: connectionID, route: route, inputRate: inputRate, outputRate: outputRate, maxCost: maxCost}
	profileJSON, actionJSON, profileDigest, err := probe.documents()
	if err != nil {
		c.fail("%v", err)
	}
	c.version("adapter_source_revision", sourceRevision())
	c.version("protocol_revision", "openrouter-responses/stateless-v1")
	c.version("model", model)
	c.version("upstream_route", route.Only[0])
	c.version("credential_store", "macOS Keychain; account reference redacted")
	c.version("profile_digest", profileDigest)
	c.attach("profile", json.RawMessage(profileJSON))
	c.attach("max_cost_micro_usd", maxCost)
	c.attach("probe_input_token_bound", openRouterMaxInput)
	c.attach("probe_output_token_bound", openRouterMaxOutput)

	transport := &countingLiveTransport{}
	blobs := newMemoryBlobs()
	adapter, err := responses.New(contract.AdapterDependencies{
		HTTP: &http.Client{Transport: transport}, Secrets: secrets,
		Clock: &stepClock{now: time.Now().UTC()}, Blobs: blobs,
	}, profileJSON)
	if err != nil {
		c.fail("responses.New rejected the pinned candidate profile: %v", err)
	}
	dispatch := contract.Dispatch{
		OperationID: contract.NewID(), AttemptID: contract.NewID(), Generation: 1,
		Adapter: "responses", Action: actionJSON, CredentialRef: secrets.ref,
		Deadline: time.Now().Add(35 * time.Second),
	}
	c.attach("operation_id", dispatch.OperationID)
	c.attach("attempt_id", dispatch.AttemptID)
	obs, err := adapter.Invoke(context.Background(), dispatch)
	c.attach("physical_requests", transport.calls.Load())
	if err != nil {
		c.fail("responses.Invoke refused the selected qualification probe: %v", err)
	}
	// Scan for leaked credential bytes, then retain every record of the
	// billable request before any assertion can end the case, so a failed or
	// unknown outcome still carries the IDs an operator needs to resolve it.
	for label, raw := range map[string][]byte{
		"evidence": obs.Evidence, "usage": obs.Usage, "provider reference": []byte(obs.ProviderReference),
	} {
		if bytes.Contains(raw, secret) {
			c.fail("credential bytes leaked into %s", label)
		}
	}
	for _, body := range blobs.all() {
		if bytes.Contains(body, secret) {
			c.fail("credential bytes leaked into a staged blob")
		}
	}
	c.attach("response_evidence", json.RawMessage(obs.Evidence))
	c.attach("usage", json.RawMessage(obs.Usage))
	var evidence liveEvidence
	if err := json.Unmarshal(obs.Evidence, &evidence); err != nil {
		c.fail("provider evidence did not decode: %v", err)
	}
	if n := len(evidence.StagedOutputs); n > 0 {
		c.attach("provider_response", json.RawMessage(blobs.bytesOf(evidence.StagedOutputs[n-1].Digest)))
	}
	if transport.calls.Load() != 1 {
		c.fail("physical requests = %d, want exactly one; do not rerun until non-execution of the extra requests is established", transport.calls.Load())
	}
	if obs.Disposition != contract.DispositionSucceeded {
		rerun := ""
		if obs.Disposition == contract.DispositionUnknown {
			rerun = "; the outcome is unknown, so a rerun is forbidden until authoritative non-execution is established"
		}
		c.fail("probe disposition=%s http=%d code=%q: %s%s", obs.Disposition, evidence.PhysicalCall.HTTPStatus, evidence.PhysicalCall.ErrorCode, evidence.PhysicalCall.ErrorMessage, rerun)
	}
	if err := probe.checkStagedRequest(blobs, evidence); err != nil {
		c.fail("%v", err)
	}
	spent, err := probe.checkCharge(evidence, obs.Evidence)
	c.attach("observed_charge_micro_usd", spent)
	if err != nil {
		c.fail("%v", err)
	}
	if len(evidence.StagedOutputs) == 0 {
		c.fail("successful probe did not retain the provider response artifact")
	}
	c.observe("one fixed probe succeeded via OpenRouter model %s on pinned route %s; max cost ceiling=%d micro-USD; profile=%s", model, route.Only[0], maxCost, profileDigest)
}

// openRouterProbe holds the non-secret selection one probe is built from.
type openRouterProbe struct {
	model, connectionID   string
	route                 openRouterRouteSelection
	inputRate, outputRate exactRate
	maxCost               int64
}

// documents builds the enforced-cost candidate profile and its fixed
// qualification_probe action, bound to the profile by digest.
func (p openRouterProbe) documents() (profile, action json.RawMessage, digest string, err error) {
	profile, err = json.Marshal(map[string]any{
		"schema": "zatiti.responses/v2", "endpoint": openRouterEndpoint, "model": p.model,
		"connection_id": p.connectionID, "max_input_tokens": openRouterMaxInput,
		"max_output_tokens": openRouterMaxOutput, "max_response_bytes": 65536,
		"timeout_seconds": 30, "currency": "USD",
		"input_rate":  map[string]any{"numerator_micro_units": p.inputRate.num, "denominator_units": p.inputRate.den, "unit": "input_token"},
		"output_rate": map[string]any{"numerator_micro_units": p.outputRate.num, "denominator_units": p.outputRate.den, "unit": "output_token"},
		"enforcement": map[string]any{
			"cost": "enforced", "disclosure": "enforced",
			"maximum_cost":          map[string]any{"currency": "USD", "micro_units": p.maxCost},
			"provider_destinations": []string{"https://openrouter.ai"},
		},
		"provider": "openrouter", "session_mode": "stateless", "routing": p.route,
	})
	if err != nil {
		return nil, nil, "", fmt.Errorf("marshal profile: %w", err)
	}
	canonical, err := contract.Canonicalize(profile)
	if err != nil {
		return nil, nil, "", fmt.Errorf("canonicalize profile: %w", err)
	}
	digest = string(contract.Hash(canonical))
	action, err = json.Marshal(map[string]any{
		"schema": "zatiti.responses.action/v2", "kind": "qualification_probe",
		"session_mode": "stateless", "model": p.model,
		"probe_text":        "Reply with exactly: ZATITI_MODEL_QUALIFIED",
		"max_output_tokens": openRouterMaxOutput, "profile_digest": digest,
		"qualification_cost_bound": map[string]any{"currency": "USD", "micro_units": p.maxCost},
		"provider":                 "openrouter",
	})
	if err != nil {
		return nil, nil, "", fmt.Errorf("marshal probe action: %w", err)
	}
	return profile, action, digest, nil
}

// checkStagedRequest verifies the retained request record: POST to the
// pinned endpoint, no Authorization header, and a body that preserves the
// selected model, exact route, output bound and hard price ceiling.
func (p openRouterProbe) checkStagedRequest(blobs *memoryBlobs, evidence liveEvidence) error {
	if evidence.PhysicalCall.RequestedDestination != openRouterEndpoint {
		return fmt.Errorf("request destination %q did not match the pinned OpenRouter endpoint", evidence.PhysicalCall.RequestedDestination)
	}
	var request requestRecord
	if err := json.Unmarshal(blobs.bytesOf(evidence.PhysicalCall.RequestContext.Digest), &request); err != nil {
		return fmt.Errorf("staged request record did not decode: %w", err)
	}
	if request.Method != http.MethodPost || request.Destination != openRouterEndpoint {
		return fmt.Errorf("staged request method/destination were %q %q, want POST to the pinned OpenRouter endpoint", request.Method, request.Destination)
	}
	for _, header := range request.Headers {
		if strings.EqualFold(header.Name, "Authorization") {
			return errors.New("staged request record contains Authorization")
		}
	}
	body, err := base64.StdEncoding.DecodeString(request.BodyBase64)
	if err != nil {
		return fmt.Errorf("staged request body was not valid base64: %w", err)
	}
	var wire struct {
		Model           string `json:"model"`
		MaxOutputTokens int64  `json:"max_output_tokens"`
		Provider        struct {
			Only              []string `json:"only"`
			AllowFallbacks    bool     `json:"allow_fallbacks"`
			RequireParameters bool     `json:"require_parameters"`
			DataCollection    string   `json:"data_collection"`
			ZDR               bool     `json:"zdr"`
			MaxPrice          struct {
				Prompt     json.Number `json:"prompt"`
				Completion json.Number `json:"completion"`
			} `json:"max_price"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return fmt.Errorf("staged provider request did not decode: %w", err)
	}
	if wire.Model != p.model || wire.MaxOutputTokens != openRouterMaxOutput || len(wire.Provider.Only) != 1 ||
		wire.Provider.Only[0] != p.route.Only[0] || wire.Provider.AllowFallbacks || !wire.Provider.RequireParameters ||
		wire.Provider.MaxPrice.Prompt.String() != p.route.PriceCeiling.Input || wire.Provider.MaxPrice.Completion.String() != p.route.PriceCeiling.Output {
		return errors.New("staged provider request did not preserve the selected model, exact route, output bound and hard price ceiling")
	}
	for _, v := range p.route.Privacy {
		if (v == "zero_retention" && !wire.Provider.ZDR) || (v != "zero_retention" && wire.Provider.DataCollection != "deny") {
			return fmt.Errorf("staged provider request did not carry the selected privacy control %q", v)
		}
	}
	return nil
}

// checkCharge fails a nominally successful probe whose adapter accounting
// reports a post-success finding, an unpriced charge, or a charge above the
// approved ceiling. It returns the charge it compared.
func (p openRouterProbe) checkCharge(evidence liveEvidence, raw json.RawMessage) (int64, error) {
	var usage struct {
		Output struct {
			Usage struct {
				Billing    string `json:"billing"`
				Accounting struct {
					Spent     int64 `json:"spent"`
					Estimated int64 `json:"estimated"`
				} `json:"accounting"`
			} `json:"usage"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &usage); err != nil {
		return 0, fmt.Errorf("provider usage did not decode: %w", err)
	}
	u := usage.Output.Usage
	charge := u.Accounting.Spent
	if u.Billing == "bounded_estimate" {
		charge = u.Accounting.Estimated
	}
	if evidence.PhysicalCall.ErrorCode != "" {
		return charge, fmt.Errorf("adapter reported %q after the provider call", evidence.PhysicalCall.ErrorCode)
	}
	if u.Billing != "observed" && u.Billing != "bounded_estimate" {
		return charge, fmt.Errorf("probe charge was %q, want observed or bounded_estimate", u.Billing)
	}
	if charge > p.maxCost {
		return charge, fmt.Errorf("probe charge %d micro-USD exceeds the approved ceiling %d", charge, p.maxCost)
	}
	return charge, nil
}

type exactRate struct{ num, den int64 }

// frozenDecimal is the ResponsesRoutingOpenRouter price pattern; big.Rat
// alone also accepts signs, leading zeros, underscores and hex forms that
// are not JSON numbers.
var frozenDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,18})?$`)

// parseProbeRoute strictly decodes the operator's routing selection and
// checks it against the frozen OpenRouter route shape before any request.
func parseProbeRoute(raw string) (route openRouterRouteSelection, in, out exactRate, err error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&route); err != nil {
		return route, in, out, fmt.Errorf("must be a valid OpenRouter routing object: %w", err)
	}
	if dec.More() {
		return route, in, out, errors.New("must hold exactly one routing object")
	}
	if len(route.Only) != 1 || strings.TrimSpace(route.Only[0]) == "" || route.AllowFallbacks || !route.RequireParameters || route.PriceCeiling.Currency != "USD" {
		return route, in, out, errors.New("the pinned route must name exactly one provider, disable fallbacks, require parameters and include a USD price ceiling")
	}
	for _, v := range route.Privacy {
		if v != "no_training" && v != "data_policy" && v != "zero_retention" {
			return route, in, out, fmt.Errorf("privacy value %q is not one of no_training, data_policy, zero_retention", v)
		}
	}
	if in, err = exactMicroRate(route.PriceCeiling.Input); err != nil {
		return route, in, out, fmt.Errorf("price_ceiling.input_per_million: %w", err)
	}
	if out, err = exactMicroRate(route.PriceCeiling.Output); err != nil {
		return route, in, out, fmt.Errorf("price_ceiling.output_per_million: %w", err)
	}
	return route, in, out, nil
}

// OpenRouter's USD-per-million token ceiling has the same numeric value as
// micro-USD per token, so it maps exactly without floating-point rounding.
func exactMicroRate(value string) (exactRate, error) {
	if len(value) > 64 || !frozenDecimal.MatchString(value) {
		return exactRate{}, fmt.Errorf("rate %q must be a plain decimal matching %s", value, frozenDecimal)
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() <= 0 || !r.Num().IsInt64() || !r.Denom().IsInt64() {
		return exactRate{}, fmt.Errorf("rate must be positive and fit exact signed 64-bit integers")
	}
	return exactRate{num: r.Num().Int64(), den: r.Denom().Int64()}, nil
}

// probeWorstCaseCost rounds the input and output charges up separately and
// then adds them, exactly as the adapter's reservation does.
func probeWorstCaseCost(input, output exactRate) (int64, error) {
	in, err := ceilCharge(input, openRouterMaxInput)
	if err != nil {
		return 0, err
	}
	out, err := ceilCharge(output, openRouterMaxOutput)
	if err != nil {
		return 0, err
	}
	if in > math.MaxInt64-out {
		return 0, fmt.Errorf("worst-case amount exceeds signed 64-bit micro-USD")
	}
	return in + out, nil
}

func ceilCharge(rate exactRate, tokens int64) (int64, error) {
	total := new(big.Int).Mul(big.NewInt(rate.num), big.NewInt(tokens))
	quotient, remainder := new(big.Int).QuoRem(total, big.NewInt(rate.den), new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("worst-case amount exceeds signed 64-bit micro-USD")
	}
	return quotient.Int64(), nil
}

// fixedResponseTransport answers every request with one canned body and
// never reaches the network.
type fixedResponseTransport struct {
	calls int
	body  string
}

func (f *fixedResponseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls++
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(f.body)), ContentLength: int64(len(f.body)), Request: r,
	}, nil
}

// TestOpenRouterProbeDocumentsAreAcceptedOffline runs the probe's exact
// profile and action through the real adapter against a canned response, so
// a mismatch between the probe and the adapter's enforced-cost, routing or
// worst-case reservation rules fails here instead of on a billable call.
func TestOpenRouterProbeDocumentsAreAcceptedOffline(t *testing.T) {
	for _, tc := range []struct {
		name, routing string
		worst         int64
	}{
		// 256 input tokens at 0.2 plus 16 output tokens at 1.25 is 51.2 + 20, rounded up.
		{"single-rounding-agrees", `{"only":["DeepInfra"],"allow_fallbacks":false,"require_parameters":true,` +
			`"price_ceiling":{"currency":"USD","input_per_million":"0.2","output_per_million":"1.25"}}`, 72},
		// 25.6 and 0.16 round up separately to 26 + 1; one combined rounding would give 26.
		{"per-charge-rounding", `{"only":["DeepInfra"],"allow_fallbacks":false,"require_parameters":true,` +
			`"privacy":["no_training","zero_retention"],` +
			`"price_ceiling":{"currency":"USD","input_per_million":"0.1","output_per_million":"0.01"}}`, 27},
	} {
		t.Run(tc.name, func(t *testing.T) { runOfflineProbe(t, tc.routing, tc.worst) })
	}
}

// runOfflineProbe drives the probe's documents through the real adapter at
// the probe's worst case (accepted, one request) and one micro-USD below it
// (refused for budget before any request).
func runOfflineProbe(t *testing.T, routing string, wantWorst int64) {
	t.Helper()
	route, inputRate, outputRate, err := parseProbeRoute(routing)
	if err != nil {
		t.Fatal(err)
	}
	worst, err := probeWorstCaseCost(inputRate, outputRate)
	if err != nil {
		t.Fatal(err)
	}
	if worst != wantWorst {
		t.Fatalf("worst-case reservation = %d micro-USD, want %d", worst, wantWorst)
	}
	probe := openRouterProbe{
		model: "z-ai/glm-flash-latest", connectionID: "0a000000-0000-4000-8000-0000000000c1",
		route: route, inputRate: inputRate, outputRate: outputRate, maxCost: worst,
	}
	profile, action, _, err := probe.documents()
	if err != nil {
		t.Fatal(err)
	}
	const ref = "cred-openrouter"
	secret := []byte("synthetic-openrouter-key")
	transport := &fixedResponseTransport{body: `{"id":"resp_probe","object":"response","status":"completed","model":"z-ai/glm-flash-latest",` +
		`"output":[{"type":"message","id":"msg_probe","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ZATITI_MODEL_QUALIFIED","annotations":[]}]}],` +
		`"usage":{"input_tokens":20,"output_tokens":8,"total_tokens":28}}`}
	blobs := newMemoryBlobs()
	adapter, err := responses.New(contract.AdapterDependencies{
		HTTP: &http.Client{Transport: transport}, Secrets: memorySecrets{refs: map[string][]byte{ref: secret}},
		Clock: &stepClock{now: time.Now().UTC()}, Blobs: blobs,
	}, profile)
	if err != nil {
		t.Fatalf("responses.New rejected the probe profile: %v", err)
	}
	obs, err := adapter.Invoke(context.Background(), contract.Dispatch{
		OperationID: contract.NewID(), AttemptID: contract.NewID(), Generation: 1,
		Adapter: "responses", Action: action, CredentialRef: ref, Deadline: time.Now().Add(35 * time.Second),
	})
	if err != nil {
		t.Fatalf("Invoke refused the probe action: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded || transport.calls != 1 {
		t.Fatalf("disposition=%s physical requests=%d, want succeeded after exactly one; evidence=%s", obs.Disposition, transport.calls, obs.Evidence)
	}
	var evidence liveEvidence
	if err := json.Unmarshal(obs.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if err := probe.checkStagedRequest(blobs, evidence); err != nil {
		t.Fatal(err)
	}
	if charge, err := probe.checkCharge(evidence, obs.Evidence); err != nil || charge <= 0 {
		t.Fatalf("charge check: charge=%d err=%v", charge, err)
	}
	for _, body := range append(blobs.all(), obs.Evidence, obs.Usage) {
		if bytes.Contains(body, secret) {
			t.Fatal("credential bytes leaked into retained output")
		}
	}

	// A ceiling one micro-USD below the worst case is refused for budget
	// before any request is made.
	probe.maxCost = worst - 1
	profile, action, _, err = probe.documents()
	if err != nil {
		t.Fatal(err)
	}
	transport.calls = 0
	adapter, err = responses.New(contract.AdapterDependencies{
		HTTP: &http.Client{Transport: transport}, Secrets: memorySecrets{refs: map[string][]byte{ref: secret}},
		Clock: &stepClock{now: time.Now().UTC()}, Blobs: newMemoryBlobs(),
	}, profile)
	if err == nil {
		_, err = adapter.Invoke(context.Background(), contract.Dispatch{
			OperationID: contract.NewID(), AttemptID: contract.NewID(), Generation: 1,
			Adapter: "responses", Action: action, CredentialRef: ref, Deadline: time.Now().Add(35 * time.Second),
		})
	}
	var fault *contract.Fault
	if !errors.As(err, &fault) || fault.Code != contract.CodeBudgetUnavailable || transport.calls != 0 {
		t.Fatalf("underfunded probe: err=%v physical requests=%d, want budget refusal before any request", err, transport.calls)
	}
}

// TestOpenRouterProbeRejectsMalformedRoutes proves the pre-send guard refuses
// route selections the frozen schema forbids, which would otherwise reach the
// wire without the pinned route or price ceiling.
func TestOpenRouterProbeRejectsMalformedRoutes(t *testing.T) {
	route := func(extra, input string) string {
		return `{"only":["DeepInfra"],"allow_fallbacks":false,"require_parameters":true,` + extra +
			`"price_ceiling":{"currency":"USD","input_per_million":"` + input + `","output_per_million":"1.25"}}`
	}
	for _, raw := range []string{
		route("", "+0.2"), route("", "00.2"), route("", "0x10"), route("", "0b1"), route("", "0o7"),
		route("", "1p-2"), route("", "1_000"), route("", "1e3"), route("", ".5"), route("", "0"),
		route(`"privacy":["bogus"],`, "0.2"), route(`"extra":true,`, "0.2"),
	} {
		if _, _, _, err := parseProbeRoute(raw); err == nil {
			t.Errorf("parseProbeRoute accepted %s", raw)
		}
	}
}

// TestOpenRouterProbeChargeGuard proves a nominal success still fails when the
// adapter flags a post-success finding, cannot price the call, or reports a
// charge above the approved ceiling.
func TestOpenRouterProbeChargeGuard(t *testing.T) {
	probe := openRouterProbe{maxCost: 72}
	for name, tc := range map[string]struct {
		code, evidence string
		ok             bool
	}{
		"bounded":      {"", `{"output":{"usage":{"billing":"bounded_estimate","accounting":{"estimated":40}}}}`, true},
		"observed":     {"", `{"output":{"usage":{"billing":"observed","accounting":{"spent":72}}}}`, true},
		"over-ceiling": {"", `{"output":{"usage":{"billing":"observed","accounting":{"spent":73}}}}`, false},
		"unknown":      {"", `{"output":{"usage":{"billing":"unknown","accounting":{"unknown":72}}}}`, false},
		"adapter-flag": {"input_token_bound_exceeded", `{"output":{"usage":{"billing":"observed","accounting":{"spent":1}}}}`, false},
	} {
		var ev liveEvidence
		ev.PhysicalCall.ErrorCode = tc.code
		if _, err := probe.checkCharge(ev, json.RawMessage(tc.evidence)); (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", name, err, tc.ok)
		}
	}
}
