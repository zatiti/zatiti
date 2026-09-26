package responses

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

const qualificationProbeText = "Reply with exactly: ZATITI_MODEL_QUALIFIED"

func qualificationDraftJSON() json.RawMessage {
	return json.RawMessage(`{
		"schema":"zatiti.responses/v2",
		"endpoint":"https://openrouter.ai/api/v1/responses",
		"model":"z-ai/glm-flash-latest",
		"connection_id":"0a000000-0000-4000-8000-0000000000c1",
		"max_input_tokens":2048,
		"max_output_tokens":64,
		"max_response_bytes":1048576,
		"timeout_seconds":60,
		"currency":"USD",
		"input_rate":{"numerator_micro_units":1,"denominator_units":1,"unit":"input_token"},
		"output_rate":{"numerator_micro_units":1,"denominator_units":1,"unit":"output_token"},
		"enforcement":{"cost":"advisory","disclosure":"enforced","maximum_cost":{"currency":"USD","micro_units":1000000},"provider_destinations":["https://openrouter.ai"]},
		"provider":"openrouter",
		"session_mode":"stateless",
		"routing":{"only":["DeepInfra"],"allow_fallbacks":false,"require_parameters":true}
	}`)
}

func qualificationProbeActionJSON(text string, costBound int64) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"schema": "zatiti.responses.action/v2", "kind": kindQualificationProbe,
		"session_mode": "stateless", "model": "z-ai/glm-flash-latest",
		"probe_text": text, "max_output_tokens": 16, "profile_digest": strings.Repeat("b", 64),
		"provider":                 "openrouter",
		"qualification_cost_bound": map[string]any{"currency": "USD", "micro_units": costBound},
	})
	return b
}

func TestQualificationDraftPerformsOnlyOneFixedProbe(t *testing.T) {
	t.Parallel()
	body := `{"id":"resp_probe","object":"response","status":"completed","model":"z-ai/glm-flash-latest","output":[{"type":"message","id":"msg_probe","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ZATITI_MODEL_QUALIFIED","annotations":[]}]}],"usage":{"input_tokens":20,"output_tokens":8,"total_tokens":28}}`
	transport := &countingTransport{fn: respond(http.StatusOK, body)}
	deps := testDeps()
	deps.HTTP = &http.Client{Transport: transport}
	adapter, err := New(deps, qualificationDraftJSON())
	if err != nil {
		t.Fatalf("New(evidence-free draft): %v", err)
	}
	clock := newFakeClock()
	probe := contract.Dispatch{
		OperationID:   "2f0a4ddb-9925-4ac2-b5f2-5dfb7e204e90",
		AttemptID:     "2f0a4ddb-9925-4ac2-b5f2-5dfb7e204e91",
		Generation:    1,
		Adapter:       adapterName,
		Action:        qualificationProbeActionJSON(qualificationProbeText, 3000),
		CredentialRef: testCredentialRef,
		Deadline:      clock.Now().Add(time.Minute),
	}
	obs, err := adapter.Invoke(context.Background(), probe)
	if err != nil {
		t.Fatalf("Invoke(qualification_probe): %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Fatalf("probe disposition = %q, want succeeded; evidence=%s", obs.Disposition, obs.Evidence)
	}
	if got := transport.count(); got != 1 {
		t.Fatalf("physical requests = %d, want exactly one", got)
	}
	if n := deps.Blobs.(*fakeBlobStore).stageCount(); n < 2 {
		t.Fatalf("staged outputs = %d, want request context and provider response at minimum", n)
	}
}

func TestQualificationDraftCannotRunOrdinaryModelStep(t *testing.T) {
	t.Parallel()
	transport := &countingTransport{fn: respond(http.StatusOK, `{}`)}
	deps := testDeps()
	deps.HTTP = &http.Client{Transport: transport}
	adapter, err := New(deps, qualificationDraftJSON())
	if err != nil {
		t.Fatalf("New(evidence-free draft): %v", err)
	}
	ordinary := json.RawMessage(`{"schema":"zatiti.responses.action/v2","kind":"model_step","session_mode":"stateless","context_artifact":{"id":"0a000000-0000-4000-8000-0000000000c2","digest":"` + strings.Repeat("a", 64) + `"},"max_output_tokens":16,"tool_contract_versions":[]}`)
	dispatch := contract.Dispatch{Adapter: adapterName, Action: ordinary, CredentialRef: testCredentialRef}
	_, err = adapter.Invoke(context.Background(), dispatch)
	f := mustFault(t, err, contract.CodeInvalidInput)
	if !strings.Contains(f.Message, "only its exact bounded qualification probe") {
		t.Fatalf("fault = %q, want qualification-only restriction", f.Message)
	}
	if got := transport.count(); got != 0 {
		t.Fatalf("ordinary model step issued %d physical requests", got)
	}
}

func TestQualificationProbeRejectsChangedPromptBeforeCall(t *testing.T) {
	t.Parallel()
	transport := &countingTransport{fn: respond(http.StatusOK, `{}`)}
	deps := testDeps()
	deps.HTTP = &http.Client{Transport: transport}
	adapter, err := New(deps, qualificationDraftJSON())
	if err != nil {
		t.Fatalf("New(evidence-free draft): %v", err)
	}
	dispatch := contract.Dispatch{Adapter: adapterName, Action: qualificationProbeActionJSON("include private context", 3000), CredentialRef: testCredentialRef}
	_, err = adapter.Invoke(context.Background(), dispatch)
	_ = mustFault(t, err, contract.CodeInvalidInput)
	if got := transport.count(); got != 0 {
		t.Fatalf("changed probe issued %d physical requests", got)
	}
}

func TestQualificationProbeRefusesCostBelowReservedWorstCase(t *testing.T) {
	t.Parallel()
	transport := &countingTransport{fn: respond(http.StatusOK, `{}`)}
	deps := testDeps()
	deps.HTTP = &http.Client{Transport: transport}
	adapter, err := New(deps, qualificationDraftJSON())
	if err != nil {
		t.Fatalf("New(evidence-free draft): %v", err)
	}
	dispatch := contract.Dispatch{Adapter: adapterName, Action: qualificationProbeActionJSON(qualificationProbeText, 1000), CredentialRef: testCredentialRef}
	_, err = adapter.Invoke(context.Background(), dispatch)
	_ = mustFault(t, err, contract.CodeBudgetUnavailable)
	if got := transport.count(); got != 0 {
		t.Fatalf("underfunded probe issued %d physical requests", got)
	}
}
