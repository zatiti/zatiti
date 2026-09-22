package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// This file is P46 item 1's Responses-adapter half ("the actual controller,
// Responses adapter pointed at a controlled provider") and item 2's honest
// account of the hosted half of "message-to-chief-to-task-to-tool-to-
// output-to-verification-to-reply": it proves the real controller really
// dispatches the real internal/adapters/responses adapter's two-effect
// prepare_session/model_step split against a controlled provider server --
// a genuine physical TLS round trip, not a handler-only shortcut -- and
// then documents, with a passing test asserting the CURRENT correct
// behavior rather than a fabricated success, exactly where production code
// honestly stops today.
//
// Four independently confirmed, pre-existing production gaps (none
// invented nor worked around here; all cited to source) make "the model
// decides autonomously and replies" unreachable through real code on this
// tree right now. Gap 0 was found while building this card's own
// controller fixture (not pre-briefed) and turned out to be the most
// fundamental of the four -- it blocks the chain even before gaps 1-3 are
// ever reached:
//
//  0. internal/connections implements no contract.LocalJobRunner (zero
//     `func...RunJob` anywhere in internal/connections/*.go), and
//     connection.validate's own job carries no operation_id at creation,
//     so the controller's generic job-claim phase can never claim or drive
//     it: "no job runner is attached for connections/connection.validate."
//     No connection can ever leave validation_state "unverified" through
//     real production code, on this tree, today -- which _connections.
//     resolve requires before any hosted (or external-tool) dispatch can
//     resolve a tool/connection pair. See
//     TestConnectionValidateJobHasNoRunnerAndStaysPendingForever below.
//  1. internal/adapters/responses/interpret.go's interpret() (the 2xx
//     decoded-response branch) never converts a decoded tool_calls entry
//     into a typed wireModelToolProposal -- it only flags
//     "tool_proposal_mapping_unspecified" and returns (interpret.go:159-
//     167); evidence.go:197-198 defaults ModelOutput.tool_proposals to an
//     empty slice whenever nothing set it, which is always. The adapter's
//     own comment names the reason: ModelToolProposal requires
//     operation_id/operation_version and "neither the action nor
//     ContextToolDefinition carries that mapping, so this package has no
//     honest source for them" -- a real contract gap, not a bug this
//     package could quietly patch.
//  2. internal/execution/interpret.go's interpretTurnObservation refuses
//     invalid_input the instant it sees output.ToolProposals is empty
//     (interpret.go:169-178: "a hosted turn cannot progress on text alone"),
//     for EVERY disposition including a plain text-only reply. Given (1)
//     always empties tool_proposals, every real hosted model_step
//     observation is refused at this exact point today, regardless of task-
//     vs-message-triggered and regardless of what the model said.
//  3. Separately and even earlier in the pipeline: a message/responsibility-
//     triggered turn is never given an AttemptID (only a task-triggered
//     turn gets one, via _execution.enqueue's automatic turn admission --
//     internal/execution/turn_ops.go's autoClaimHostedRun), and
//     dispatchModelEffect's own model_step dispatch is gated on
//     t.AttemptID != "" (turn_ops.go:571). So a bare chat message to a
//     hosted worker never even reaches a physical call: the controller
//     correctly declines to dispatch into a dead end rather than
//     fabricating an attempt. This is already logged (docs/roadmap.md,
//     2026-09-21, P16/P22 landing notes: "a bare chat/responsibility turn
//     ... has no schema-valid path to receive a ModelOutput today").
//
// TestConnectionValidateJobHasNoRunnerAndStaysPendingForever proves gap 0
// directly. TestHostedTurnsNeverDispatchGivenAnUnverifiableConnection
// proves its consequence: neither a chat message nor a task ever causes a
// single physical call, because gap 0 blocks connection resolution before
// gaps 1-3 are ever reached. TestResponsesAdapterDispatches
// PrepareSessionAndModelStepAgainstControlledProvider proves, independent
// of gap 0 (it calls Adapter.Invoke directly, bypassing connections
// entirely), that the adapter itself genuinely completes two real physical
// calls against a controlled provider -- the proof that gaps 1/2 are real
// adapter/execution-layer gaps, not an artifact of this fixture's own
// wiring being broken. None of these tests work around any gap with
// test-side production code; each asserts the CURRENT correct behavior,
// exactly as this card's restore-protocol section requires for that
// separate, already-known gap.

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

	// connection.validate is accepted (a real admitted job), but see
	// TestConnectionValidateJobHasNoRunnerAndStaysPendingForever below: it
	// can never actually complete on this tree today (internal/connections
	// implements no contract.LocalJobRunner, so nothing ever claims and
	// drives its dispatch), so this connection stays validation_state
	// "unverified" forever. Worker/binding creation below does not itself
	// require a valid connection -- only _connections.resolve, at actual
	// dispatch time, does -- so this fixture still returns a fully
	// activated worker/binding/connection triple; what it can never do is
	// make that connection resolvable.
	f.must(f.owner, "connection.validate", key+"-validate", map[string]any{
		"scope": f.scope(), "id": connID, "expected_version": 1,
	})

	f.activate(key+"-binding", "binding.create", map[string]any{
		"definition": map[string]any{
			"scope": f.scope(), "kind": "tool", "target_id": modelResponsesToolID,
			"permissions": []string{"invoke"}, "destinations": []string{},
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

// TestConnectionValidateJobHasNoRunnerAndStaysPendingForever (P46 item 1/2,
// gap 0 above -- discovered while building this card's controller fixture,
// not pre-briefed): a real connection.validate call is admitted (a real
// job is created), but internal/connections implements no
// contract.LocalJobRunner anywhere on this tree (confirmed by direct
// source read: zero `func...RunJob` in internal/connections/*.go) and its
// job carries no operation_id of its own at creation, so the controller's
// generic job-claim phase (internal/controller/jobs.go's jobs()) can never
// claim it -- it reports a "no job runner is attached for connections/
// connection.validate" obligation and the job stays pending forever. Not
// even a single physical call reaches the controlled provider for the
// validation probe itself. cmd/zatiti's own landedJobKinds/catalogJobKinds
// tables (cmd/zatiti/jobs.go) never list "connections" at all -- unlike
// "artifacts/artifact.export", which they do flag as a known, deliberate
// gap -- so this looks like an unnoticed omission, not a documented
// limitation, and it silently makes EVERY connection.validate call and
// EVERY connection dependent on it permanently unusable in production.
func TestConnectionValidateJobHasNoRunnerAndStaysPendingForever(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	provider := newOpenAIProviderServer(t)
	adapter := buildResponsesAdapter(t, f, provider)
	cf := attachController(t, f, controllerFixtureOptions{adapters: map[string]contract.Adapter{"responses": adapter}})

	_, connID := cf.wireHostedWorker(cf.mustRootOrg(t), "hosted-obligation")

	waitFor(t, 10*time.Second, "the controller to report the missing connections job runner as an obligation", func() bool {
		for _, o := range cf.ctl.Status().Obligations {
			if o.Kind == "job" && o.Fault.Code == contract.CodePrerequisiteMissing &&
				strings.Contains(o.Fault.Message, "connections/connection.validate") {
				return true
			}
		}
		return false
	})
	time.Sleep(300 * time.Millisecond)
	if n := provider.callCount(); n != 0 {
		t.Fatalf("controlled provider received %d physical calls for a validation job that was never claimed, want 0: paths %v", n, provider.callPaths())
	}
	status := f.must(f.owner, "connection.get", "", map[string]any{"scope": f.scope(), "id": connID})
	var out struct {
		Resource struct {
			ValidationState string `json:"validation_state"`
		} `json:"resource"`
	}
	decode(t, status.Data, &out)
	if out.Resource.ValidationState != "unverified" {
		t.Fatalf("connection validation_state %q, want unverified (never fabricated valid)", out.Resource.ValidationState)
	}
}

// TestHostedTurnsNeverDispatchGivenAnUnverifiableConnection (P46 item 2):
// because a connection can never become valid (the test above), the two
// independent gaps this card was briefed to expect -- a message-triggered
// turn never receiving an AttemptID (turn_ops.go's dispatch gate) and the
// Responses adapter never populating ModelOutput.tool_proposals (this
// file's own top doc comment, gaps 1/2) -- are moot in practice for a
// hosted worker on this tree: the chain breaks even earlier, at connection
// resolution, before either of them is ever reached. This test proves that
// earlier, more fundamental ceiling directly: neither a chat message nor a
// task assigned to a hosted worker ever causes a single physical call to
// the controlled provider, because _connections.resolve refuses
// prerequisite_missing for the permanently-unverified connection and the
// worker's tool binding is simply skipped rather than offered
// (context_build.go: "a stale product-tool binding must never block
// chat/task progress"). The task is left stalled, never fabricated into
// either terminal state; the message never receives a reply.
func TestHostedTurnsNeverDispatchGivenAnUnverifiableConnection(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	provider := newOpenAIProviderServer(t)
	adapter := buildResponsesAdapter(t, f, provider)
	cf := attachController(t, f, controllerFixtureOptions{adapters: map[string]contract.Adapter{"responses": adapter}})

	org := cf.mustRootOrg(t)
	scope := contract.Scope{InstallationID: f.installationID, OrganizationID: org}
	worker, _ := cf.wireHostedWorker(org, "hosted-stall")

	// A chat message to the hosted worker.
	conv := f.must(f.owner, "conversation.create", "hosted-stall-conv", map[string]any{
		"scope": f.scope(), "kind": "direct", "participant_ids": []contract.ID{f.owner.PrincipalID, worker}, "title": "hosted chat",
	})
	var conversation struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	decode(t, conv.Data, &conversation)
	f.must(f.owner, "conversation.message.send", "hosted-stall-msg", map[string]any{
		"scope": f.scope(), "conversation_id": conversation.Resource.ID, "message_id": contract.NewID(),
		"body": "hello, hosted worker", "attachments": []any{}, "task_ids": []contract.ID{},
	})

	// A task assigned to the same hosted worker.
	def := f.taskDefinition(scope, f.owner.PrincipalID, worker, unconfiguredCurrency)
	created := f.must(f.owner, "task.create", "hosted-stall-task", map[string]any{"scope": scope, "definition": def})
	var task struct {
		Resource struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"resource"`
	}
	decode(t, created.Data, &task)
	f.must(f.owner, "task.start", "hosted-stall-start", map[string]any{
		"scope": scope, "id": task.Resource.ID, "expected_version": task.Resource.Version,
	})

	// Many real ticks: enough for discoverMessages/turnWork/dispatch to run
	// dozens of times over. Neither path ever reaches the provider.
	time.Sleep(500 * time.Millisecond)

	if n := provider.callCount(); n != 0 {
		t.Fatalf("controlled provider received %d physical calls though the worker's only connection is permanently unverified, want 0: paths %v", n, provider.callPaths())
	}
	messages := f.must(f.owner, "conversation.message.list", "", map[string]any{"scope": f.scope(), "conversation_id": conversation.Resource.ID})
	var ml struct {
		Items []any `json:"items"`
	}
	decode(t, messages.Data, &ml)
	if len(ml.Items) != 1 {
		t.Fatalf("conversation carries %d messages, want exactly the owner's own -- no reply was ever produced: %s", len(ml.Items), messages.Data)
	}
	taskStatus := f.must(f.owner, "task.get", "", map[string]any{"scope": scope, "id": task.Resource.ID})
	var taskOut struct {
		Resource struct {
			State string `json:"state"`
		} `json:"resource"`
	}
	decode(t, taskStatus.Data, &taskOut)
	if taskOut.Resource.State == "succeeded" || taskOut.Resource.State == "failed" {
		t.Fatalf("task reached terminal state %q though its worker's connection can never resolve; independent verification must never be bypassed", taskOut.Resource.State)
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
	// This is the exact, independently confirmed gap this file's top doc
	// comment cites: a real completed response with real output carries no
	// tool proposals, because the adapter has no honest operation_id/
	// operation_version to mint one with.
	if len(evidence.Output.ToolProposals) != 0 {
		t.Fatalf("model_step evidence carries %d tool_proposals; the known adapter gap (interpret.go's tool_proposal_mapping_unspecified) appears to have closed -- update this test and hosted_turn_test.go's doc comment", len(evidence.Output.ToolProposals))
	}
}
