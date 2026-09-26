package qualification_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/adapters/responses"
	"github.com/zatiti/zatiti/internal/contract"
)

// QUALIFICATION.adapter_bounds/responses, live: the hosted Responses
// adapter, constructed through its public constructor, against the real
// pinned endpoint with the founder's credential resolved by reference from
// the macOS keychain. It runs only when ZATITI_QUALIFY_RESPONSES_LIVE=1,
// because every step it takes is billed; otherwise the case is recorded as
// not run. Prompts are the smallest that prove each point and the run
// stops itself past a few cents.
//
// What it establishes, each as recorded evidence:
//  1. one billable model step per Invoke, counted at the transport, with
//     no hidden retry and an unrewindable body;
//  2. the conversation-first path end to end: the handle is minted, named
//     in the staged request record before the step is sent, and usable
//     afterwards for the documented reconciliation lookup;
//  3. exact observed token counts and the amount computed from the pinned
//     rates, with the documented total_tokens invariant checked;
//  4. the staged request records carry the headers as sent without the
//     credential, and nothing in evidence carries it;
//  5. whether the byte-based input bound held, so
//     input_token_bound:utf8_bytes is recorded only if it did;
//  6. adapter version, source revision, protocol revision and profile
//     digest pinned in the record.

const (
	liveGate            = "ZATITI_QUALIFY_RESPONSES_LIVE"
	liveEndpoint        = "https://api.openai.com/v1/responses"
	liveModel           = "gpt-5.6-luna"
	liveProtocol        = "openai-openapi/2.3.0@ddface9b"
	liveKeychainService = "zatiti-responses"
	liveKeychainAccount = "zatiti"
	liveCredentialRef   = "keychain:zatiti-responses/zatiti"
	liveInputBoundCap   = "input_token_bound:utf8_bytes"
	// liveSpendStop ends the run once the computed spend passes it: five
	// cents, far under the founder's ten-dollar monthly ceiling.
	liveSpendStop int64 = 50_000
)

// Pinned rates in micro-USD per token: input $0.20 per 1M, output $1.20
// per 1M (scratchpad/model-profile-pinned.md).
var (
	liveInputRate  = rational{1, 5}
	liveOutputRate = rational{6, 5}
)

type rational struct{ num, den int64 }

// ceilCost prices tokens at rate, rounding up as the adapter does.
func ceilCost(tokens int64, r rational) int64 {
	return (tokens*r.num + r.den - 1) / r.den
}

// keychainSecrets resolves the founder's credential by reference through
// the OS keychain helper, exactly as a trusted adapter must: the bytes go
// from the helper into the adapter's request header and nowhere else.
type keychainSecrets struct{}

func (keychainSecrets) Put(context.Context, string, []byte) (string, error) {
	return "", errors.New("qualification never writes credentials")
}

func (keychainSecrets) Lookup(_ context.Context, key string) (string, error) {
	if key != liveCredentialRef {
		return "", &contract.Fault{Code: contract.CodeNotFound, Message: "unknown credential reference"}
	}
	return liveCredentialRef, nil
}

func (keychainSecrets) Get(ctx context.Context, ref string) ([]byte, error) {
	if ref != liveCredentialRef {
		return nil, &contract.Fault{Code: contract.CodeNotFound, Message: "unknown credential reference"}
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password",
		"-s", liveKeychainService, "-a", liveKeychainAccount, "-w").Output()
	if err != nil {
		return nil, &contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "keychain item is not readable"}
	}
	return bytes.TrimRight(out, "\r\n"), nil
}

func (keychainSecrets) Delete(context.Context, string) error {
	return errors.New("qualification never deletes credentials")
}

// countingLiveTransport counts every physical request the adapter hands to
// the real transport and retains secret-free facts about each.
type countingLiveTransport struct {
	mu       sync.Mutex
	calls    atomic.Int32
	requests []string // "METHOD url rewindable=<bool>"
}

func (c *countingLiveTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	c.mu.Lock()
	c.requests = append(c.requests, fmt.Sprintf("%s %s rewindable=%v", r.Method, r.URL.String(), r.GetBody != nil))
	c.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

// liveProfile is the founder-pinned profile: advisory cost mode with the
// byte bound claimed, so the run itself observes whether the bound held
// without ever asserting a hard cap it has not yet earned.
func liveProfile(t *testing.T, connectionID contract.ID) (json.RawMessage, map[string]any) {
	t.Helper()
	profile := map[string]any{
		"schema":             "zatiti.responses/v1",
		"endpoint":           liveEndpoint,
		"model":              liveModel,
		"connection_id":      string(connectionID),
		"max_input_tokens":   272000,
		"max_output_tokens":  128000,
		"max_response_bytes": 1 << 20,
		"timeout_seconds":    120,
		"currency":           "USD",
		"input_rate":         map[string]any{"numerator_micro_units": liveInputRate.num, "denominator_units": liveInputRate.den, "unit": "input_token"},
		"output_rate":        map[string]any{"numerator_micro_units": liveOutputRate.num, "denominator_units": liveOutputRate.den, "unit": "output_token"},
		"enforcement": map[string]any{
			"cost":                  "advisory",
			"disclosure":            "enforced",
			"maximum_cost":          map[string]any{"currency": "USD", "micro_units": 10_000_000},
			"provider_destinations": []string{"https://api.openai.com"},
			"classifications":       []string{"public", "internal"},
			"evidence":              capabilityEvidence("responses-qualification", sourceRevision(), liveProtocol, []string{}, []string{}),
		},
		"capability_evidence": capabilityEvidence("internal/adapters/responses@"+sourceRevision(), sourceRevision(), liveProtocol,
			[]string{liveInputBoundCap}, []string{"qualification run: cost mode advisory until the byte bound is confirmed"}),
	}
	return bindProfile(t, profile), profile
}

// liveContext is the smallest complete-capture context for one prompt,
// optionally declaring one function tool.
func liveContext(t *testing.T, blobs *memoryBlobs, system, user string, tool map[string]any) (map[string]any, []map[string]any) {
	t.Helper()
	tools := []map[string]any{}
	pinned := []map[string]any{}
	if tool != nil {
		tools = append(tools, tool)
		pinned = append(pinned, tool["tool"].(map[string]any))
	}
	message := func(role, origin, text string) map[string]any {
		return map[string]any{
			"id": string(contract.NewID()), "role": role, "origin": origin,
			"parts":            []map[string]any{{"kind": "text", "text": text}},
			"source_artifacts": []any{},
		}
	}
	doc := map[string]any{
		"schema":                 "zatiti.context/v1",
		"attempt_id":             string(contract.NewID()),
		"scope":                  map[string]any{"installation_id": string(contract.NewID())},
		"configuration_revision": 1,
		"worker":                 map[string]any{"id": string(contract.NewID()), "version": 1},
		"execution_profile":      map[string]any{"id": string(contract.NewID()), "version": 1},
		"skill_versions":         []any{},
		"messages":               []map[string]any{message("system", "effective_instruction", system), message("user", "user_message", user)},
		"tools":                  tools,
		"source_artifacts":       []any{},
		"capture":                "complete",
		"created_at":             time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal context: %v", err)
	}
	return map[string]any{"id": string(contract.NewID()), "digest": string(blobs.put(raw))}, pinned
}

func liveAction(contextRef map[string]any, maxOutput int64, pinned []map[string]any) map[string]any {
	if pinned == nil {
		pinned = []map[string]any{}
	}
	return map[string]any{
		"schema":                 "zatiti.responses.action/v1",
		"kind":                   "model_step",
		"context_artifact":       contextRef,
		"max_output_tokens":      maxOutput,
		"tool_contract_versions": pinned,
	}
}

// liveEvidence is the subset of zatiti.responses.evidence/v1 the case reads.
type liveEvidence struct {
	PhysicalCall struct {
		RequestedDestination string `json:"requested_destination"`
		RequestSent          string `json:"request_sent"`
		Confirmation         string `json:"confirmation"`
		HTTPStatus           int64  `json:"http_status"`
		ProviderReference    string `json:"provider_reference"`
		ErrorCode            string `json:"error_code"`
		ErrorMessage         string `json:"error_message"`
		RequestContext       struct {
			Kind       string          `json:"kind"`
			StagingRef string          `json:"staging_ref"`
			Digest     contract.Digest `json:"digest"`
		} `json:"request_context"`
	} `json:"physical_call"`
	ResponseID string `json:"response_id"`
	Output     struct {
		FinishReason string `json:"finish_reason"`
		TextOutputs  []struct {
			Digest contract.Digest `json:"digest"`
		} `json:"text_outputs"`
		Usage struct {
			Accounting struct {
				Currency string `json:"currency"`
				Spent    int64  `json:"spent"`
				Unknown  int64  `json:"unknown"`
				Advisory bool   `json:"advisory"`
			} `json:"accounting"`
			Billing      string `json:"billing"`
			InputTokens  *int64 `json:"input_tokens"`
			OutputTokens *int64 `json:"output_tokens"`
		} `json:"usage"`
		ContinuationReference string `json:"continuation_reference"`
	} `json:"output"`
	StagedOutputs []struct {
		StagingRef string          `json:"staging_ref"`
		Digest     contract.Digest `json:"digest"`
		Purpose    string          `json:"purpose"`
	} `json:"staged_outputs"`
}

// requestRecord is the adapter's staged request record shape.
type requestRecord struct {
	Method      string `json:"method"`
	Destination string `json:"destination"`
	Headers     []struct {
		Name   string   `json:"name"`
		Values []string `json:"values"`
	} `json:"headers"`
	BodyBase64 string `json:"body_base64"`
}

func (b *memoryBlobs) bytesOf(d contract.Digest) []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.objects[d]
}

func TestResponsesLiveQualification(t *testing.T) {
	c := beginCase(t, "QUALIFICATION.adapter_bounds/responses-live", "QUALIFICATION",
		"One billable physical model step per Invoke, counted, no hidden retry.",
		"Conversation minted first, named in the staged request record before the step is sent, and usable for the documented reconciliation lookup.",
		"Exact observed token counts and the amount computed from the pinned rates.",
		"Staged request records carry the headers as sent without the credential; nothing in evidence carries it.",
		"input_token_bound:utf8_bytes recorded only if the byte bound held.",
		"Adapter version, source revision, protocol revision and profile digest pinned.")
	if os.Getenv(liveGate) != "1" {
		c.notRun("%s is not set to 1; the live case bills the founder's account and runs only on request", liveGate)
	}
	secrets := keychainSecrets{}
	if _, err := secrets.Get(context.Background(), liveCredentialRef); err != nil {
		c.notRun("the keychain item %s/%s is not readable: %v", liveKeychainService, liveKeychainAccount, err)
	}

	connectionID := contract.NewID()
	profileJSON, profile := liveProfile(t, connectionID)
	profileDigest := profile["capability_evidence"].(map[string]any)["profile_digest"].(string)
	c.version("adapter_version", "internal/adapters/responses@"+sourceRevision())
	c.version("protocol_revision", liveProtocol)
	c.version("profile_digest", profileDigest)
	c.version("model", liveModel)
	c.version("endpoint", liveEndpoint)
	c.attach("profile", json.RawMessage(profileJSON))

	transport := &countingLiveTransport{}
	blobs := newMemoryBlobs()
	adapter, err := responses.New(contract.AdapterDependencies{
		HTTP: &http.Client{Transport: transport}, Secrets: secrets, Clock: &stepClock{now: time.Now().UTC()}, Blobs: blobs,
	}, profileJSON)
	if err != nil {
		c.fail("responses.New refused the pinned profile: %v", err)
	}
	var contractDoc struct {
		Qualified []string `json:"qualified_protocol_revisions"`
	}
	if err := json.Unmarshal(adapter.Contract(), &contractDoc); err != nil || len(contractDoc.Qualified) != 1 || contractDoc.Qualified[0] != liveProtocol {
		c.fail("adapter contract does not pin protocol revision %s: %v %v", liveProtocol, contractDoc.Qualified, err)
	}
	c.observe("adapter constructed from the pinned profile (digest %s); contract pins protocol revision %s", profileDigest, liveProtocol)

	secret, _ := secrets.Get(context.Background(), liveCredentialRef)
	var spent int64
	disagreements := []string{}
	boundHeld := true

	// A leak check over every byte the adapter produced: evidence, usage,
	// provider reference, every staged blob.
	leakCheck := func(label string, obs contract.Observation) {
		haystacks := [][]byte{obs.Evidence, obs.Usage, []byte(obs.ProviderReference)}
		blobs.mu.Lock()
		for _, data := range blobs.objects {
			haystacks = append(haystacks, data)
		}
		blobs.mu.Unlock()
		for _, h := range haystacks {
			if bytes.Contains(h, secret) {
				c.fail("%s: the credential appears in adapter output", label)
			}
		}
	}
	decode := func(label string, obs contract.Observation) liveEvidence {
		var ev liveEvidence
		if err := json.Unmarshal(obs.Evidence, &ev); err != nil {
			c.fail("%s: evidence does not decode: %v", label, err)
		}
		leakCheck(label, obs)
		return ev
	}
	record := func(label string, ev liveEvidence) requestRecord {
		var rec requestRecord
		if err := json.Unmarshal(blobs.bytesOf(ev.PhysicalCall.RequestContext.Digest), &rec); err != nil {
			c.fail("%s: request record does not decode: %v", label, err)
		}
		for _, h := range rec.Headers {
			if strings.EqualFold(h.Name, "Authorization") {
				c.fail("%s: the request record carries the Authorization header", label)
			}
		}
		return rec
	}
	account := func(label string, ev liveEvidence) {
		if ev.Output.Usage.InputTokens == nil || ev.Output.Usage.OutputTokens == nil {
			disagreements = append(disagreements, label+": the response carried no usage")
			return
		}
		in, out := *ev.Output.Usage.InputTokens, *ev.Output.Usage.OutputTokens
		cost := ceilCost(in, liveInputRate) + ceilCost(out, liveOutputRate)
		spent += cost
		c.observe("%s: input_tokens=%d output_tokens=%d; pinned rates price it at %d micro-USD; adapter reported billing=%s spent=%d", label, in, out, cost, ev.Output.Usage.Billing, ev.Output.Usage.Accounting.Spent)
		if ev.Output.Usage.Billing == "observed" && ev.Output.Usage.Accounting.Spent != cost {
			disagreements = append(disagreements, fmt.Sprintf("%s: adapter spent %d differs from the test's %d", label, ev.Output.Usage.Accounting.Spent, cost))
		}
		if ev.PhysicalCall.ErrorCode == "input_token_bound_exceeded" {
			boundHeld = false
			c.observe("%s: %s", label, ev.PhysicalCall.ErrorMessage)
		}
		if spent > liveSpendStop {
			c.fail("computed spend %d micro-USD passed the %d stop; run ended", spent, liveSpendStop)
		}
	}
	deadline := func() time.Time { return time.Now().Add(2 * time.Minute) }

	// ---- 1 + 2 + 3 + 4: one text step ----
	textCtx, _ := liveContext(t, blobs, "Reply with exactly the word OK and nothing else.", "Ready?", nil)
	before := transport.calls.Load()
	obs, err := adapter.Invoke(context.Background(), dispatch(t, "responses", liveAction(textCtx, 128, nil), liveCredentialRef, deadline()))
	if err != nil {
		c.fail("text step: Invoke refused: %v", err)
	}
	calls := transport.calls.Load() - before
	ev := decode("text step", obs)
	c.attach("text_step_evidence", json.RawMessage(obs.Evidence))
	c.attach("text_step_transport", append([]string(nil), transport.requests...))
	c.observe("text step: %d physical requests for one Invoke (conversation + response), disposition=%s http=%d finish=%s response_id=%s provider_reference=%s",
		calls, obs.Disposition, ev.PhysicalCall.HTTPStatus, ev.Output.FinishReason, ev.ResponseID, obs.ProviderReference)
	if calls != 2 {
		c.fail("text step: %d physical requests, want exactly 2 (conversation, then response)", calls)
	}
	for _, r := range transport.requests {
		if strings.Contains(r, "rewindable=true") {
			c.fail("a request body was rewindable: %s", r)
		}
	}
	if obs.Disposition != contract.DispositionSucceeded {
		c.fail("text step: disposition %s, error %s: %s", obs.Disposition, ev.PhysicalCall.ErrorCode, ev.PhysicalCall.ErrorMessage)
	}
	handle := obs.ProviderReference
	if !strings.HasPrefix(handle, "conv_") {
		disagreements = append(disagreements, "conversation id does not carry the conv_ prefix: "+handle)
	}
	stepRecord := record("text step", ev)
	var stepBody map[string]json.RawMessage
	body, _ := base64.StdEncoding.DecodeString(stepRecord.BodyBase64)
	if err := json.Unmarshal(body, &stepBody); err != nil {
		c.fail("text step: recorded body is not JSON: %v", err)
	}
	if string(stepBody["conversation"]) != `"`+handle+`"` {
		c.fail("text step: the staged request record names conversation %s, not the handle %s", stepBody["conversation"], handle)
	}
	// The request record was staged before the step left: the transport
	// saw the response request after the record's staging ref existed.
	if ev.PhysicalCall.RequestContext.Kind != "staged" || len(ev.StagedOutputs) < 3 || ev.StagedOutputs[2].StagingRef != ev.PhysicalCall.RequestContext.StagingRef {
		c.fail("text step: request_context %+v is not the staged step record", ev.PhysicalCall.RequestContext)
	}
	c.observe("text step: staged request record %s names conversation %s and carries headers %v without Authorization", stepRecord.Destination, handle, headerNames(stepRecord))
	text := ""
	if len(ev.Output.TextOutputs) > 0 {
		text = string(blobs.bytesOf(ev.Output.TextOutputs[0].Digest))
	}
	c.observe("text step: model text %q", text)
	if ev.Output.ContinuationReference != handle {
		disagreements = append(disagreements, "response.conversation.id "+ev.Output.ContinuationReference+" differs from the minted conversation "+handle)
	}
	account("text step", ev)
	rawResponse := blobs.bytesOf(ev.StagedOutputs[3].Digest)
	c.attach("text_step_provider_response", json.RawMessage(rawResponse))
	var totals struct {
		Usage struct {
			InputTokens   int64 `json:"input_tokens"`
			OutputTokens  int64 `json:"output_tokens"`
			TotalTokens   int64 `json:"total_tokens"`
			OutputDetails struct {
				Reasoning int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		ServiceTier string `json:"service_tier"`
		Store       bool   `json:"store"`
	}
	if err := json.Unmarshal(rawResponse, &totals); err == nil {
		if totals.Usage.InputTokens+totals.Usage.OutputTokens != totals.Usage.TotalTokens {
			disagreements = append(disagreements, fmt.Sprintf("usage.total_tokens %d != input %d + output %d", totals.Usage.TotalTokens, totals.Usage.InputTokens, totals.Usage.OutputTokens))
		}
		c.observe("text step: provider reports reasoning_tokens=%d service_tier=%q store=%v", totals.Usage.OutputDetails.Reasoning, totals.ServiceTier, totals.Store)
	}

	// ---- 2: the handle resolves through the documented lookup ----
	before = transport.calls.Load()
	rd := dispatch(t, "responses", liveAction(textCtx, 128, nil), liveCredentialRef, deadline())
	rd.ProviderKey = handle
	robs, err := adapter.Reconcile(context.Background(), rd)
	if err != nil {
		c.fail("reconcile: refused: %v", err)
	}
	rev := decode("reconcile", robs)
	c.attach("reconcile_evidence", json.RawMessage(robs.Evidence))
	c.observe("reconcile: %d physical request(s), disposition=%s http=%d finish=%s error_code=%q texts=%d",
		transport.calls.Load()-before, robs.Disposition, rev.PhysicalCall.HTTPStatus, rev.Output.FinishReason, rev.PhysicalCall.ErrorCode, len(rev.Output.TextOutputs))
	if transport.calls.Load()-before != 1 {
		c.fail("reconcile: %d physical requests, want exactly 1", transport.calls.Load()-before)
	}
	if robs.Disposition != contract.DispositionSucceeded {
		c.fail("reconcile: the minted handle did not resolve the completed step: %s %s: %s", robs.Disposition, rev.PhysicalCall.ErrorCode, rev.PhysicalCall.ErrorMessage)
	}
	recovered := ""
	if len(rev.Output.TextOutputs) > 0 {
		recovered = string(blobs.bytesOf(rev.Output.TextOutputs[0].Digest))
	}
	if recovered != text {
		disagreements = append(disagreements, fmt.Sprintf("reconciled text %q differs from the step's %q", recovered, text))
	}
	record("reconcile", rev)
	c.attach("reconcile_provider_response", json.RawMessage(blobs.bytesOf(rev.StagedOutputs[1].Digest)))

	// ---- 2, negative: a handle the provider never minted resolves nothing ----
	before = transport.calls.Load()
	missing := dispatch(t, "responses", liveAction(textCtx, 128, nil), liveCredentialRef, deadline())
	missing.ProviderKey = "conv_zatiti_qualification_never_minted"
	mobs, err := adapter.Reconcile(context.Background(), missing)
	if err != nil {
		c.fail("reconcile missing: refused: %v", err)
	}
	mev := decode("reconcile missing", mobs)
	c.observe("reconcile of a never-minted handle: disposition=%s http=%d error_code=%q (%d request)", mobs.Disposition, mev.PhysicalCall.HTTPStatus, mev.PhysicalCall.ErrorCode, transport.calls.Load()-before)
	if mobs.Disposition != contract.DispositionUnknown {
		c.fail("reconcile missing: disposition %s, want unknown (not-found never proves nonexecution)", mobs.Disposition)
	}

	// ---- the documented function_call shape, live ----
	tool := map[string]any{
		"tool":          map[string]any{"id": string(contract.NewID()), "version": 1},
		"name":          "fetch_source",
		"description":   "Fetch one public source by URL.",
		"input_schema":  map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}}, "required": []string{"url"}},
		"output_schema": map[string]any{"type": "object"},
		"effect":        "external_read",
		"destinations":  []string{"https://example.com"},
		"binding_id":    string(contract.NewID()),
		"schema_digest": string(contract.Hash([]byte("fetch_source"))),
	}
	toolCtx, pinned := liveContext(t, blobs, "You must call the fetch_source tool with url https://example.com/ before answering. Do not answer in text.", "Fetch the source.", tool)
	before = transport.calls.Load()
	tobs, err := adapter.Invoke(context.Background(), dispatch(t, "responses", liveAction(toolCtx, 256, pinned), liveCredentialRef, deadline()))
	if err != nil {
		c.fail("tool step: Invoke refused: %v", err)
	}
	tev := decode("tool step", tobs)
	c.attach("tool_step_evidence", json.RawMessage(tobs.Evidence))
	c.observe("tool step: %d physical requests, disposition=%s finish=%s error_code=%q", transport.calls.Load()-before, tobs.Disposition, tev.Output.FinishReason, tev.PhysicalCall.ErrorCode)
	if tobs.Disposition != contract.DispositionSucceeded {
		c.fail("tool step: %s %s: %s", tobs.Disposition, tev.PhysicalCall.ErrorCode, tev.PhysicalCall.ErrorMessage)
	}
	record("tool step", tev)
	account("tool step", tev)
	toolResponse := blobs.bytesOf(tev.StagedOutputs[3].Digest)
	c.attach("tool_step_provider_response", json.RawMessage(toolResponse))
	if tev.Output.FinishReason == "tool_calls" && tev.PhysicalCall.ErrorCode == "tool_proposal_mapping_unspecified" {
		c.observe("tool step: the model returned a function_call item; decoded as tool_calls and retained raw, no typed proposal fabricated (known contract gap)")
	} else {
		disagreements = append(disagreements, fmt.Sprintf("tool step: expected a function_call, got finish=%s error_code=%s", tev.Output.FinishReason, tev.PhysicalCall.ErrorCode))
	}

	// ---- 5 + summary ----
	c.attach("computed_spend_micro_usd", spent)
	c.attach("byte_bound_held", boundHeld)
	c.attach("disagreements", disagreements)
	c.observe("total computed spend %d micro-USD (%.6f USD) over 2 model steps and 2 lookups", spent, float64(spent)/1e6)
	if boundHeld {
		c.observe("the byte-based input bound held on every step; %s may be recorded in the profile's capability_evidence", liveInputBoundCap)
	} else {
		c.observe("the byte-based input bound did NOT hold; leave the profile advisory and do not record %s", liveInputBoundCap)
	}
	if len(disagreements) > 0 {
		c.observe("documentation vs live disagreements: %s", strings.Join(disagreements, "; "))
	} else {
		c.observe("no disagreement between the pinned documented shapes and the live endpoint was observed")
	}
}

func headerNames(rec requestRecord) []string {
	names := make([]string, 0, len(rec.Headers))
	for _, h := range rec.Headers {
		names = append(names, h.Name)
	}
	return names
}
