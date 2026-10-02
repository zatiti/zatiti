package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/adapters/responses"
	"github.com/zatiti/zatiti/internal/contract"
)

// Hosted adapter and governed connection-validation integration tests.

// openaiProtocolRevision is internal/adapters/responses/openai.go's
// unexported constant naming the one qualified wire protocol revision this
// build ships (its own comment: "the published OpenAPI document this file
// was written against"). It must be reproduced literally here (this
// package cannot import an unexported identifier) for a profile to select
// the real openaiProtocol implementation rather than no protocol at all.
const openaiProtocolRevision = "openai-openapi/2.3.0@ddface9b"

// modelResponsesToolID/Version is the built-in "model-responses" tool
// contract internal/connections/migrations.go's migrationV1Tail seeds at a
// fixed identity (internal/connections/builtin_tools.go's
// toolNameModelResponses): id 0a000000-0000-4000-8000-0000000000c1, version
// 1, destinations_json ["api.openai.com"]. internal/execution/context_
// build.go pins defaultResolveVersion=1 for every connection/tool resolve
// it performs, so this identity is stable across installations and this
// test names it directly rather than discovering it through tool.list.
const (
	modelResponsesToolID      = contract.ID("0a000000-0000-4000-8000-0000000000c1")
	modelResponsesToolVersion = 1
	// modelResponsesDomainDestination is the fixed string the built-in
	// tool's own Destinations allowlist carries and connections.resolve
	// checks the connection/binding destination against verbatim
	// (internal/connections/handlers_internal.go handleResolve:
	// contains(tool.Destinations, in.Destination)). It has nothing to do
	// with the real wire URL a dispatched effect is actually sent to --
	// that is the adapter's own local profile file's endpoint, a
	// completely separate, locally-configured surface (see
	// responsesProviderProfile below) -- so a controlled provider that is
	// not literally api.openai.com is fully compatible with this domain-
	// level authorization check.
	modelResponsesDomainDestination = "api.openai.com"
)

// openaiProviderServer is a controlled TLS server that speaks just enough
// of the real, pinned OpenAI Responses wire protocol
// (internal/adapters/responses/openai.go) for the real qualified adapter to
// drive a genuine physical round trip against it: POST .../conversations
// mints a conversation id (prepare_session) and POST .../responses answers
// one completed, text-only response (model_step). It counts calls by path
// so a test can assert exactly how many physical calls a boundary made --
// the crash/injection style assertion this card's item 5 requires.
type openaiProviderServer struct {
	srv *httptest.Server

	mu    sync.Mutex
	calls []string
}

func newOpenAIProviderServer(t testing.TB) *openaiProviderServer {
	t.Helper()
	s := &openaiProviderServer{}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

// endpoint is the profile's https responses URL: openaiConversationsURL
// derives the conversations resource from it by replacing the final path
// segment, so it must end in exactly "/responses".
func (s *openaiProviderServer) endpoint() string { return s.srv.URL + "/v1/responses" }

// client is a real *http.Client trusting this server's TLS certificate --
// the same one internal/adapters/responses.New wraps into its own private
// client, reusing its Transport.
func (s *openaiProviderServer) client() *http.Client { return s.srv.Client() }

func (s *openaiProviderServer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *openaiProviderServer) callPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *openaiProviderServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, r.URL.Path)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var body string
	switch {
	case strings.HasSuffix(r.URL.Path, "/conversations"):
		body = fmt.Sprintf(`{"id":"conv_%s","object":"conversation"}`, strings.ReplaceAll(string(contract.NewID()), "-", ""))
	case strings.HasSuffix(r.URL.Path, "/responses") && requestDeclaresReplyTool(r):
		// A context that offers the sealed reply decision tool gets the
		// model's answer through it, exactly as a hosted worker replies.
		body = fmt.Sprintf(`{"id":"resp_%s","object":"response","status":"completed",`+
			`"output":[{"type":"function_call","id":"fc_1","call_id":"call_reply_1","name":"reply","status":"completed",`+
			`"arguments":"{\"text\":\"acknowledged\"}"}],`+
			`"usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16}}`,
			strings.ReplaceAll(string(contract.NewID()), "-", ""))
	case strings.HasSuffix(r.URL.Path, "/responses"):
		body = fmt.Sprintf(`{"id":"resp_%s","object":"response","status":"completed",`+
			`"output":[{"type":"message","role":"assistant","status":"completed",`+
			`"content":[{"type":"output_text","text":"acknowledged"}]}],`+
			`"usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16}}`,
			strings.ReplaceAll(string(contract.NewID()), "-", ""))
	default:
		w.WriteHeader(http.StatusNotFound)
		body = `{"error":{"type":"invalid_request_error","message":"integration fixture: no route"}}`
	}
	if _, err := w.Write([]byte(body)); err != nil {
		// The client side of this test connection observes and reports a
		// transport failure on its own; nothing here can recover a write
		// that already failed mid-response.
		return
	}
}

// requestDeclaresReplyTool reports whether a model_step request offers the
// sealed reply function tool.
func requestDeclaresReplyTool(r *http.Request) bool {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	var req struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if json.Unmarshal(raw, &req) != nil {
		return false
	}
	for _, t := range req.Tools {
		if t.Name == contract.LocalDecisionToolReply {
			return true
		}
	}
	return false
}

// profileDigestWithoutCapabilityEvidence reproduces internal/adapters/
// responses/profile.go's profileDigestWithoutCapabilityEvidence (unexported,
// so this package computes its own copy from the same public contract
// primitives): the canonical-JSON SHA-256 digest of raw with its top-level
// capability_evidence field removed.
func profileDigestWithoutCapabilityEvidence(t testing.TB, raw json.RawMessage) contract.Digest {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := contract.DecodeStrict(raw, &doc); err != nil {
		t.Fatalf("decode profile for digest: %v", err)
	}
	delete(doc, "capability_evidence")
	stripped, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal stripped profile: %v", err)
	}
	canon, err := contract.Canonicalize(stripped)
	if err != nil {
		t.Fatalf("canonicalize stripped profile: %v", err)
	}
	return contract.Hash(canon)
}

// responsesProviderProfile builds a valid, self-binding zatiti.responses/v1
// adapter profile (the trusted local configuration file cmd/zatiti/
// adapters.go loads from <state-dir>/adapters/responses.json in production;
// this test hands the same bytes straight to responses.New) pointed at
// endpoint and naming the real qualified OpenAI wire protocol revision, so
// Invoke really selects and speaks internal/adapters/responses/openai.go's
// protocol rather than a synthetic one.
// originOf returns endpoint's scheme://host[:port] with no path, so a
// profile can declare one origin-wide provider_destinations entry rather
// than separately naming every distinct path the adapter's own protocol
// dispatches to (.../responses and .../conversations).
func originOf(t testing.TB, endpoint string) string {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint %q: %v", endpoint, err)
	}
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	return u.String()
}

func responsesProviderProfile(t testing.TB, endpoint string) json.RawMessage {
	t.Helper()
	newEvidence := func() map[string]any {
		return map[string]any{
			"artifact":          map[string]any{"id": contract.NewID(), "digest": syntheticDigest},
			"adapter_version":   "integration-fixture-1",
			"source_revision":   "integration-fixture-rev",
			"protocol_revision": openaiProtocolRevision,
			"profile_digest":    syntheticDigest, // enforcement.evidence keeps this placeholder forever
			"qualified_at":      "2026-01-01T00:00:00Z",
			"capabilities":      []string{},
			"limitations":       []string{},
		}
	}
	// Two INDEPENDENT evidence objects, never the same map: profile.go's
	// loadProfile binds the digest only against top-level capability_
	// evidence.profile_digest, but the whole profile (enforcement.evidence
	// included) is what the digest is computed OVER. Aliasing the two
	// (as internal/adapters/responses's own testhelpers_test.go's defaultProfile
	// happens to do, relying on bindProfile only ever mutating the top-level
	// copy afterward) makes the digest self-referential and non-reproducible
	// here, where both copies would change together.
	capabilityEvidence := newEvidence()
	enforcementEvidence := newEvidence()
	doc := map[string]any{
		"schema": "zatiti.responses/v1", "endpoint": endpoint, "model": "integration-test-model",
		"connection_id": contract.NewID(), "max_input_tokens": 100000, "max_output_tokens": 4096,
		"max_response_bytes": 1 << 20, "timeout_seconds": 30, "currency": "USD",
		"input_rate":  map[string]any{"numerator_micro_units": 2, "denominator_units": 1, "unit": "input_token"},
		"output_rate": map[string]any{"numerator_micro_units": 7, "denominator_units": 2, "unit": "output_token"},
		"enforcement": map[string]any{
			// "advisory", not "enforced": an enforced hard cost cap needs a
			// claimed input-token bound (capInputTokenBoundBytes, internal/
			// adapters/responses/openai.go -- unexported, only earned by
			// real upstream qualification), which this controlled-provider
			// fixture makes no claim to. Advisory is a real, schema-valid
			// enforcement mode (profile.go admits "enforced or explicitly
			// advisory"), not a relaxation invented for this test.
			"cost": "advisory", "disclosure": "advisory",
			"maximum_cost": map[string]any{"currency": "USD", "micro_units": 1000000},
			// An origin-wide grant (no path): profile.go's destinationPermits
			// treats a declared destination with an empty/"/" path as
			// covering every path on that origin, which both the prepare_
			// session (.../conversations) and model_step (.../responses)
			// physical calls need -- a single declared exact path would
			// permit only one of the two.
			"provider_destinations": []string{originOf(t, endpoint)},
			"classifications":       []string{"internal"},
			"evidence":              enforcementEvidence,
		},
		"capability_evidence": capabilityEvidence,
	}
	raw := mustJSON(doc)
	digest := profileDigestWithoutCapabilityEvidence(t, raw)
	capabilityEvidence["profile_digest"] = digest
	doc["capability_evidence"] = capabilityEvidence
	return mustJSON(doc)
}

// buildResponsesAdapter constructs the real internal/adapters/responses
// adapter against provider, exactly through its public constructor
// (responses.New), never a package-internal test seam.
func buildResponsesAdapter(t testing.TB, f *fixture, provider *openaiProviderServer) contract.Adapter {
	t.Helper()
	deps := contract.AdapterDependencies{HTTP: provider.client(), Secrets: f.secrets, Clock: f.clock, Blobs: f.plat.Blobs()}
	adapter, err := responses.New(deps, responsesProviderProfile(t, provider.endpoint()))
	if err != nil {
		t.Fatalf("responses.New against the controlled provider: %v", err)
	}
	return adapter
}

// wireHostedWorker creates and activates the real production chain a
// hosted worker needs: a connection.create with a custodied credential
// reference, a binding.create against the seeded model-responses tool
// contract, and a worker.create carrying an inline hosted ExecutionProfile
// naming that connection. Every step activates through the real compiler
// (stage -> plan -> apply -> owner review), exactly as R6-003 requires;
// nothing here mutates configuration directly. It returns the activated
// worker's id.
func (cf *controllerFixture) wireHostedWorker(orgID contract.ID, key string) (workerID, connectionID contract.ID) {
	f := cf.fixture
	f.t.Helper()
	credRef, err := f.secrets.Put(context.Background(), "integration/hosted-worker/"+key, []byte("fake-provider-api-key"))
	if err != nil {
		f.t.Fatalf("custody hosted worker credential: %v", err)
	}
	account := "integration-hosted-account-" + key
	f.activate(key+"-connection", "connection.create", map[string]any{
		"definition": map[string]any{
			"scope": f.scope(), "provider": "openai", "account_identity": account,
			"credential_ref": credRef, "destinations": []string{modelResponsesDomainDestination}, "allowed_scopes": []string{},
		},
	})
	connID := f.findConnectionByAccount(account)

	// Validation admits a governed prepare_session probe. The controller
	// claims it and records the result before hosted dispatch can resolve
	// this connection.

	f.activate(key+"-binding", "binding.create", map[string]any{
		"definition": map[string]any{
			"scope": f.scope(), "kind": "tool", "target_id": modelResponsesToolID,
			"permissions": []string{"invoke", string(modelResponsesToolID)}, "destinations": []string{},
		},
	})
	bindingID := f.findBindingByTarget(modelResponsesToolID)

	f.activate(key+"-worker", "worker.create", map[string]any{
		"definition": map[string]any{
			"organization_id": orgID, "key": key, "name": "Hosted Worker " + key,
			"purpose": "integration hosted worker", "instructions": "respond to messages",
			"skill_versions": []any{}, "bindings": []contract.ID{bindingID},
			"profile": map[string]any{
				"id": contract.NewID(), "version": 1,
				"executor": "hosted", "model": "integration-test-model", "connection_id": connID,
				"connection_version":   2,
				"provider_destination": modelResponsesDomainDestination, "capabilities": []string{},
				"cost_bound":     map[string]any{"currency": "USD", "micro_units": 1000000},
				"classification": "internal", "context_capture": "complete",
			},
			"limits": map[string]any{
				"currency": "USD", "spend_micro_units": 1000000, "concurrency": 1, "model_steps": 10,
				"child_count": 0, "delegation_depth": 0, "attempt_seconds": 60,
				"root_deadline": "2026-01-06T09:00:00Z",
			},
		},
	})
	f.must(f.owner, "connection.validate", key+"-validate", map[string]any{
		"scope": f.scope(), "id": connID, "expected_version": 1,
	})
	return f.findWorkerByKey(key), connID
}

func (f *fixture) findConnectionByAccount(account string) contract.ID {
	f.t.Helper()
	res := f.must(f.owner, "connection.list", "", map[string]any{"scope": f.scope()})
	var out struct {
		Items []struct {
			ID              contract.ID `json:"id"`
			AccountIdentity string      `json:"account_identity"`
		} `json:"items"`
	}
	decode(f.t, res.Data, &out)
	for _, c := range out.Items {
		if c.AccountIdentity == account {
			return c.ID
		}
	}
	f.t.Fatalf("no activated connection with account_identity %q: %s", account, res.Data)
	return ""
}

func (f *fixture) findBindingByTarget(targetID contract.ID) contract.ID {
	f.t.Helper()
	res := f.must(f.owner, "binding.list", "", map[string]any{"scope": f.scope()})
	var out struct {
		Items []struct {
			ID       contract.ID `json:"id"`
			TargetID contract.ID `json:"target_id"`
		} `json:"items"`
	}
	decode(f.t, res.Data, &out)
	for _, b := range out.Items {
		if b.TargetID == targetID {
			return b.ID
		}
	}
	f.t.Fatalf("no activated binding targeting %s: %s", targetID, res.Data)
	return ""
}

func (f *fixture) findWorkerByKey(key string) contract.ID {
	f.t.Helper()
	res := f.must(f.owner, "worker.list", "", map[string]any{"scope": f.scope()})
	var out struct {
		Items []struct {
			ID  contract.ID `json:"id"`
			Key string      `json:"key"`
		} `json:"items"`
	}
	decode(f.t, res.Data, &out)
	for _, w := range out.Items {
		if w.Key == key {
			return w.ID
		}
	}
	f.t.Fatalf("no activated worker with key %q: %s", key, res.Data)
	return ""
}

// TestConnectionValidateProbeMakesTheConnectionValid (gap 0 above, now
// closed): connection.validate admits one governed prepare_session probe
// through the ordinary effects path. The controller admits and claims it,
// the real Responses adapter makes exactly one physical call to the
// controlled provider, and the recorded observation moves the connection
// from unverified to valid with a bounded freshness window.
func TestConnectionValidateProbeMakesTheConnectionValid(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	provider := newOpenAIProviderServer(t)
	adapter := buildResponsesAdapter(t, f, provider)
	cf := attachController(t, f, controllerFixtureOptions{adapters: map[string]contract.Adapter{"responses": adapter}})

	_, connID := cf.wireHostedWorker(cf.mustRootOrg(t), "hosted-validate")

	var state, validUntil string
	waitFor(t, 20*time.Second, "the validation probe to make the connection valid", func() bool {
		status := f.must(f.owner, "connection.get", "", map[string]any{"scope": f.scope(), "id": connID})
		var out struct {
			Resource struct {
				ValidationState string `json:"validation_state"`
				ValidUntil      string `json:"valid_until"`
			} `json:"resource"`
		}
		decode(t, status.Data, &out)
		state, validUntil = out.Resource.ValidationState, out.Resource.ValidUntil
		return state == "valid"
	})
	if validUntil == "" {
		t.Fatalf("valid connection carries no freshness bound")
	}
	paths := provider.callPaths()
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/conversations") {
		t.Fatalf("validation made physical calls %v, want exactly one prepare_session call to /conversations", paths)
	}
}

// mustRootOrg is f.rootOrganization's org half, named for callers here that
// do not need the chief id.
func (cf *controllerFixture) mustRootOrg(t testing.TB) contract.ID {
	t.Helper()
	org, _ := cf.rootOrganization()
	return org
}

// TestResponsesAdapterDispatchesPrepareSessionAndModelStepAgainstControlledProvider
// (P46 item 1, the controller-independent half: "the actual ... Responses
// adapter pointed at a controlled provider"): proves the real internal/
// adapters/responses adapter, constructed through its exact public
// constructor with no test seam, genuinely completes both physical calls
// of the revision-3 prepare_session/model_step split against a controlled
// provider server -- a real TLS round trip, real evidence, a real returned
// session handle -- independent of the connections-domain job-runner gap
// above (Adapter.Invoke is called directly here, exactly as the real
// controller's own perform.go calls it, with no connections/execution
// plumbing in between). This is the proof that the adapter half of this
// card's "actual controller, Responses adapter pointed at a controlled
// provider" requirement is real and working; the sibling tests above prove
// what currently keeps the rest of the pipeline from ever reaching it.
func TestResponsesAdapterDispatchesPrepareSessionAndModelStepAgainstControlledProvider(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	provider := newOpenAIProviderServer(t)
	adapter := buildResponsesAdapter(t, f, provider)
	credRef, err := f.secrets.Put(context.Background(), "integration/direct-adapter-credential", []byte("fake-provider-api-key"))
	if err != nil {
		t.Fatalf("custody credential: %v", err)
	}

	prepareAction := mustJSON(map[string]any{"schema": "zatiti.responses.action/v1", "kind": "prepare_session"})
	prepareObs, err := adapter.Invoke(context.Background(), contract.Dispatch{
		OperationID: contract.NewID(), AttemptID: contract.NewID(), Generation: 1,
		Adapter: "responses", Action: prepareAction, CredentialRef: credRef,
		Deadline: f.clock.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("prepare_session Invoke: %v", err)
	}
	if prepareObs.Disposition != "succeeded" {
		t.Fatalf("prepare_session disposition %q, want succeeded: evidence %s", prepareObs.Disposition, prepareObs.Evidence)
	}
	if prepareObs.ProviderReference == "" {
		t.Fatalf("prepare_session succeeded with no session handle: %s", prepareObs.Evidence)
	}
	if provider.callCount() != 1 || !strings.HasSuffix(provider.callPaths()[0], "/conversations") {
		t.Fatalf("physical calls after prepare_session %v, want exactly one .../conversations call", provider.callPaths())
	}

	contextBody := mustJSON(map[string]any{
		"schema": "zatiti.context/v1", "attempt_id": contract.NewID(),
		"scope": map[string]any{"installation_id": f.installationID}, "configuration_revision": 1,
		"worker":            map[string]any{"id": contract.NewID(), "version": 1},
		"execution_profile": map[string]any{"id": contract.NewID(), "version": 1},
		"skill_versions":    []any{}, "messages": []any{}, "tools": []any{}, "source_artifacts": []any{},
		"capture": "complete", "created_at": "2026-01-01T00:00:00Z",
	})
	contextArtifact := f.uploadArtifact("direct-adapter-context", contextBody, "application/json")
	modelStepAction := mustJSON(map[string]any{
		"schema": "zatiti.responses.action/v1", "kind": "model_step",
		"session_handle": prepareObs.ProviderReference, "context_artifact": map[string]any{"id": contextArtifact.ID, "digest": contextArtifact.Digest},
		"max_output_tokens": 128, "tool_contract_versions": []any{},
	})
	stepObs, err := adapter.Invoke(context.Background(), contract.Dispatch{
		OperationID: contract.NewID(), AttemptID: contract.NewID(), Generation: 1,
		Adapter: "responses", Action: modelStepAction, CredentialRef: credRef,
		Deadline: f.clock.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("model_step Invoke: %v", err)
	}
	if stepObs.Disposition != "succeeded" {
		t.Fatalf("model_step disposition %q, want succeeded: evidence %s", stepObs.Disposition, stepObs.Evidence)
	}
	if provider.callCount() != 2 || !strings.HasSuffix(provider.callPaths()[1], "/responses") {
		t.Fatalf("physical calls after model_step %v, want [.../conversations, .../responses]", provider.callPaths())
	}
	var evidence struct {
		SessionHandle string `json:"session_handle"`
		Output        struct {
			FinishReason  string `json:"finish_reason"`
			ToolProposals []any  `json:"tool_proposals"`
		} `json:"output"`
	}
	decode(t, stepObs.Evidence, &evidence)
	if evidence.SessionHandle != prepareObs.ProviderReference {
		t.Fatalf("model_step evidence session_handle %q, want the prepare_session handle %q", evidence.SessionHandle, prepareObs.ProviderReference)
	}
	// Bare model text is never a governed decision: with no tool call there
	// is nothing to map, so the evidence carries no proposal.
	if len(evidence.Output.ToolProposals) != 0 {
		t.Fatalf("text-only model_step evidence carries %d tool_proposals, want 0", len(evidence.Output.ToolProposals))
	}
}

// TestResponsesAdapterMapsSealedReplyCallToTypedProposal closes gap 1: the
// real adapter, over a real TLS round trip to a controlled provider, turns
// the model's call of the sealed reply decision tool -- declared in the
// context exactly as execution's context builder declares it -- into one
// typed ModelToolProposal bound to the context that was sent.
func TestResponsesAdapterMapsSealedReplyCallToTypedProposal(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	provider := newOpenAIProviderServer(t)
	adapter := buildResponsesAdapter(t, f, provider)
	credRef, err := f.secrets.Put(context.Background(), "integration/direct-adapter-reply-credential", []byte("fake-provider-api-key"))
	if err != nil {
		t.Fatalf("custody credential: %v", err)
	}
	replyID := contract.LocalDecisionToolID(contract.LocalDecisionToolReply)
	replyTool := map[string]any{"id": replyID, "version": 1}
	contextBody := mustJSON(map[string]any{
		"schema": "zatiti.context/v1", "attempt_id": contract.NewID(),
		"scope": map[string]any{"installation_id": f.installationID}, "configuration_revision": 1,
		"worker":            map[string]any{"id": contract.NewID(), "version": 1},
		"execution_profile": map[string]any{"id": contract.NewID(), "version": 1},
		"skill_versions":    []any{}, "messages": []any{}, "source_artifacts": []any{},
		"tools": []any{map[string]any{
			"operation_id":      contract.LocalDecisionOperationID(contract.LocalDecisionToolReply),
			"operation_version": contract.LocalDecisionOperationVersion,
			"tool":              replyTool, "name": contract.LocalDecisionToolReply,
			"description": "sealed local decision tool: reply", "input_schema": json.RawMessage(contract.ReplyProposalSchema),
			"output_schema": map[string]any{}, "effect": "local", "destinations": []string{},
			"binding_id": replyID, "schema_digest": contract.Hash(contract.ReplyProposalSchema),
		}},
		"capture": "complete", "created_at": "2026-01-01T00:00:00Z",
	})
	contextArtifact := f.uploadArtifact("direct-adapter-reply-context", contextBody, "application/json")
	stepObs, err := adapter.Invoke(context.Background(), contract.Dispatch{
		OperationID: contract.NewID(), AttemptID: contract.NewID(), Generation: 1,
		Adapter: "responses", CredentialRef: credRef, Deadline: f.clock.Now().Add(time.Minute),
		Action: mustJSON(map[string]any{
			"schema": "zatiti.responses.action/v1", "kind": "model_step", "session_handle": "conv_integration",
			"context_artifact":  map[string]any{"id": contextArtifact.ID, "digest": contextArtifact.Digest},
			"max_output_tokens": 128, "tool_contract_versions": []any{replyTool},
		}),
	})
	if err != nil {
		t.Fatalf("model_step Invoke: %v", err)
	}
	if stepObs.Disposition != "succeeded" || provider.callCount() != 1 {
		t.Fatalf("disposition %q after %d calls: %s", stepObs.Disposition, provider.callCount(), stepObs.Evidence)
	}
	var evidence struct {
		PhysicalCall struct {
			ErrorCode string `json:"error_code"`
		} `json:"physical_call"`
		Output struct {
			ToolProposals []struct {
				ID               string          `json:"id"`
				Tool             map[string]any  `json:"tool"`
				OperationID      string          `json:"operation_id"`
				OperationVersion int64           `json:"operation_version"`
				Input            json.RawMessage `json:"input"`
				SourceContext    struct {
					ID     contract.ID     `json:"id"`
					Digest contract.Digest `json:"digest"`
				} `json:"source_context"`
			} `json:"tool_proposals"`
		} `json:"output"`
	}
	decode(t, stepObs.Evidence, &evidence)
	if evidence.PhysicalCall.ErrorCode != "" || len(evidence.Output.ToolProposals) != 1 {
		t.Fatalf("evidence = %s", stepObs.Evidence)
	}
	p := evidence.Output.ToolProposals[0]
	if p.ID != "call_reply_1" || p.Tool["id"] != string(replyID) || p.OperationID != contract.LocalDecisionOperationID(contract.LocalDecisionToolReply) ||
		p.OperationVersion != 1 || p.SourceContext.ID != contextArtifact.ID || string(p.SourceContext.Digest) != contextArtifact.Digest ||
		string(p.Input) != `{"text":"acknowledged"}` {
		t.Fatalf("proposal = %+v (input %s)", p, p.Input)
	}
}

// TestWorkerReplyDeliveryUsesRegisteredWorkerPrincipal checks durable history under the worker actor.
func TestWorkerReplyDeliveryUsesRegisteredWorkerPrincipal(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	_, chief := f.rootOrganization()
	conv := f.must(f.owner, "conversation.create", "reply-shape-conv", map[string]any{
		"scope": f.scope(), "kind": "direct", "participant_ids": []contract.ID{f.owner.PrincipalID, chief}, "title": "reply",
	})
	var conversation struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	decode(t, conv.Data, &conversation)
	turnID, proposalID := contract.NewID(), "call_reply_1"
	_, err := f.app.ExecuteWorker(context.Background(), contract.WorkerRequest{
		TurnID: turnID, ProposalID: proposalID, WorkerID: chief,
		Scope:     contract.Scope{InstallationID: f.installationID, WorkerID: chief},
		Operation: "conversation.message.send", Version: 1,
		Input: mustJSON(map[string]any{
			"scope": f.scope(), "conversation_id": conversation.Resource.ID, "message_id": contract.NewID(),
			"body": "acknowledged", "attachments": []any{}, "task_ids": []contract.ID{},
		}),
		SubmissionKey: "worker-turn/" + string(turnID) + "/" + proposalID,
	})
	if err != nil {
		t.Fatalf("worker reply delivery: %v", err)
	}
	history := f.must(f.owner, "conversation.message.list", "", map[string]any{"scope": f.scope(), "conversation_id": conversation.Resource.ID})
	var list struct {
		Items []any `json:"items"`
	}
	decode(t, history.Data, &list)
	if len(list.Items) != 1 {
		t.Fatalf("owner history = %s, want one delivered worker reply", history.Data)
	}
}

func TestHostedMessageReplyPersistsAsTheWorker(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	provider := newOpenAIProviderServer(t)
	adapter := buildResponsesAdapter(t, f, provider)
	var performer contract.ContextPerformer
	for _, module := range f.modules {
		if p, ok := module.(contract.ContextPerformer); ok {
			performer = p
			break
		}
	}
	if performer == nil {
		t.Fatal("assembled execution owner has no context performer")
	}
	cf := attachController(t, f, controllerFixtureOptions{
		adapters: map[string]contract.Adapter{"responses": adapter}, context: performer,
	})
	worker, connection := cf.wireHostedWorker(cf.mustRootOrg(t), "hosted-durable-reply")
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("controller status: %+v; provider calls: %v", cf.ctl.Status(), provider.callPaths())
			operations := f.must(f.owner, "operation.list", "", map[string]any{"scope": f.scope(), "limit": 100})
			t.Logf("operations: %s", operations.Data)
			var listed struct {
				Items []struct {
					Action json.RawMessage `json:"action"`
				} `json:"items"`
			}
			decode(t, operations.Data, &listed)
			for _, operation := range listed.Items {
				policy := f.must(f.owner, "policy.explain", "", map[string]any{"scope": f.scope(), "action": operation.Action})
				t.Logf("probe policy: %s", policy.Data)
			}
		}
	})
	waitFor(t, 20*time.Second, "governed connection validation", func() bool {
		status := f.must(f.owner, "connection.get", "", map[string]any{"scope": f.scope(), "id": connection})
		return strings.Contains(string(status.Data), `"validation_state":"valid"`)
	})
	conversation := f.directConversation("hosted-durable-conversation", f.owner.PrincipalID, worker)
	f.must(f.owner, "conversation.message.send", "hosted-durable-request", map[string]any{
		"scope": f.scope(), "conversation_id": conversation, "message_id": contract.NewID(),
		"body": "hello, hosted worker", "attachments": []any{}, "task_ids": []contract.ID{},
	})
	waitFor(t, 30*time.Second, "the durable worker reply in owner history", func() bool {
		bodies := f.messageBodies(conversation)
		return len(bodies) == 2 && (bodies[0] == "acknowledged" || bodies[1] == "acknowledged")
	})
	history := f.must(f.owner, "conversation.message.list", "", map[string]any{"scope": f.scope(), "conversation_id": conversation})
	var messages struct {
		Items []struct {
			Body     string      `json:"body"`
			SenderID contract.ID `json:"sender_id"`
		} `json:"items"`
	}
	decode(t, history.Data, &messages)
	replies := 0
	for _, message := range messages.Items {
		if message.Body == "acknowledged" && message.SenderID == worker {
			replies++
		}
	}
	if len(messages.Items) != 2 || replies != 1 {
		t.Fatalf("durable history = %s", history.Data)
	}
	// One validation probe and exactly one prepare/model pair. No tool
	// effect or second model step may be invented to deliver the reply.
	paths := provider.callPaths()
	if len(paths) != 3 || !strings.HasSuffix(paths[0], "/conversations") || !strings.HasSuffix(paths[1], "/conversations") || !strings.HasSuffix(paths[2], "/responses") {
		t.Fatalf("hosted reply physical calls = %v", paths)
	}
}

// TestResponsesAdapterDispatchesPrepareSessionAndModelStepAgainstControlledProvider
// (P46 item 1, the controller-independent half: "the actual ... Responses
// adapter pointed at a controlled provider"): proves the real internal/
// adapters/responses adapter, constructed through its exact public
// constructor with no test seam, genuinely completes both physical calls
// of the revision-3 prepare_session/model_step split against a controlled
// provider server -- a real TLS round trip, real evidence, a real returned
// session handle. Adapter.Invoke is called directly here, as the
// controller's perform path calls it, without connection resolution or
// execution admission.
