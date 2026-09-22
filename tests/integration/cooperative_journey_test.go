package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// This file is P46's cooperative-claims vertical journey (JOURNEY.engineering,
// R2.3-002 items 3-4, "cooperative claims ... finish using production paths").
// A cooperative-executor task never gets a WorkerTurn (internal/execution/
// run_ops.go's handleEnqueue: only Profile != nil && Profile.Executor ==
// "hosted" gets one), so this path does not depend on the hosted Responses
// dispatch hosted_turn_test.go proves runs into its own honest ceiling --
// it looked, going in, like the one vertical journey this card could drive
// all the way to a succeeded task with no caveat.
//
// It is not. Building and running this journey against the real
// application (not a fake accounting peer, which is what internal/
// execution's own unit tests use, and which is exactly why this is the
// first place in the repo this ever ran for real) found a second,
// independent, unconditional production bug, beyond the hosted-side ones
// hosted_turn_test.go documents:
//
// internal/execution/run_ops.go's handleClaim always calls
// s.reserveBudget(ctx, unit, r.Scope, rootTaskID, "", amount, task.Limits)
// -- the FIFTH argument, operationID, is a literal "" on every single
// call, with no exception. reserveBudget (peer.go) then builds
// {"operation_id": operationID, ...} unconditionally (a map, not an
// omitempty struct field), so _accounting.reserve's own frozen Money/
// operation_id schema (format: uuid) rejects the literal empty string
// every time. Confirmed by direct source read and by reproducing it twice
// against this real fixture, independent of currency/profile setup: run.
// claim cannot succeed against ANY worker, hosted or cooperative, on this
// tree today. (An earlier, narrower version of this file also hit --and
// this comment's own history briefly attributed the failure to-- a
// worker-profile-currency issue: giving the worker a real, non-nil
// ExecutionProfile with Scope.WorkerID naming it does fix THAT symptom,
// but only uncovers this second, unconditional one underneath it. Both are
// real; this one alone is already fully blocking.)
//
// This is a first-release-blocking defect (R2.3-003 lists "the cooperative
// external-worker protocol" as required for the first release) discovered
// by this card, not pre-briefed, and squarely outside tests/integration's
// write scope to fix (internal/execution is a sibling root). The tests
// below prove the honest current ceiling instead of a fabricated success:
// task.create and task.start are real, working production code (draft ->
// ready, one run minted); run.claim is correctly admitted as a request but
// is then refused by this exact, cited defect, and neither a run nor a
// task is left in a corrupted or falsely-advanced state by the refusal.

// cooperativeWorkerDefinition declares a worker with an explicit
// executor:"cooperative" profile (schema-valid: ExecutionProfile.executor
// is "hosted"|"cooperative") rather than the bootstrap chief's profile:nil
// convention, and is used together with Scope.WorkerID at every call site
// below -- see this file's top comment for why both are needed to reach
// the real remaining defect rather than stopping at an easier-to-fix
// symptom one layer up.
func cooperativeWorkerDefinition(orgID contract.ID, key string) map[string]any {
	return map[string]any{
		"organization_id": orgID, "key": key, "name": "Cooperative Worker " + key,
		"purpose": "integration cooperative worker", "instructions": "claim, checkpoint and report assigned tasks",
		"skill_versions": []any{}, "bindings": []string{},
		"profile": map[string]any{
			"id": contract.NewID(), "version": 1,
			"executor": "cooperative", "model": "", "connection_id": contract.NewID(),
			"provider_destination": "", "capabilities": []string{},
			"cost_bound":     map[string]any{"currency": "USD", "micro_units": 0},
			"classification": "internal", "context_capture": "advisory",
		},
		"limits": nil,
	}
}

// completableTaskDefinition is f.taskDefinition (helpers_test.go) with one
// change: expected_digest names the real digest of an artifact this test
// controls, not fixture_test.go's synthetic placeholder (no real artifact's
// SHA-256 could ever equal that fixed hex string). Never actually reaches
// verification given the run.claim defect this file documents, but keeps
// the task's own acceptance contract honest rather than pointing it at a
// digest nothing could satisfy even if claim worked.
func completableTaskDefinition(f *fixture, scope contract.Scope, owner, worker contract.ID, currency, outputDigest string) map[string]any {
	f.t.Helper()
	def := f.taskDefinition(scope, owner, worker, currency)
	acceptance := def["acceptance"].(map[string]any)
	observations := acceptance["expected_observations"].([]any)
	observations[0].(map[string]any)["expected_digest"] = outputDigest
	return def
}

// startedTask is task.start's decoded output.
type startedTask struct {
	Task struct {
		ID      contract.ID `json:"id"`
		Version int64       `json:"version"`
		State   string      `json:"state"`
	} `json:"task"`
	Run struct {
		ID      contract.ID `json:"id"`
		Version int64       `json:"version"`
		State   string      `json:"state"`
	} `json:"run"`
}

// TestRunClaimRefusesEveryAttemptWithAHardcodedEmptyOperationID (JOURNEY.
// engineering; R8.1-005 "runs and attempts: ... claim, heartbeat,
// checkpoint, report"; the defect this file's top comment documents): a
// real task.create/task.start produces a real ready run (one run, no
// attempts yet) through the real application; the real public run.claim,
// called by a real authenticated cooperative worker's own principal, is
// refused invalid_input naming operation_id -- exactly and only the cited
// defect, never a setup mistake in this test (the worker carries a real,
// schema-valid cooperative ExecutionProfile and the task's scope names it,
// eliminating every other candidate cause this file's history found) --
// and the refusal leaves the run exactly as it was: still "ready", still
// zero attempts, claimable again the instant the real defect is fixed.
func TestRunClaimRefusesEveryAttemptWithAHardcodedEmptyOperationID(t *testing.T) {
	t.Parallel()
	cf := newControllerFixture(t, controllerFixtureOptions{})
	f := cf.fixture
	org, _ := f.rootOrganization()
	f.activate("coop-worker", "worker.create", map[string]any{"definition": cooperativeWorkerDefinition(org, "coop-worker")})
	worker := f.findWorkerByKey("coop-worker")
	scope := contract.Scope{InstallationID: f.installationID, OrganizationID: org, WorkerID: worker}

	note := f.uploadArtifact("coop-note", []byte("cooperative worker's intended output, never actually reported"), "text/plain")
	def := completableTaskDefinition(f, scope, f.owner.PrincipalID, worker, unconfiguredCurrency, note.Digest)

	created := f.must(f.owner, "task.create", "coop-task", map[string]any{"scope": scope, "definition": def})
	var task struct {
		Resource struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
			State   string      `json:"state"`
		} `json:"resource"`
	}
	decode(t, created.Data, &task)
	if task.Resource.State != "draft" {
		t.Fatalf("task.create state %q, want draft", task.Resource.State)
	}

	startRes := f.must(f.owner, "task.start", "coop-start", map[string]any{
		"scope": scope, "id": task.Resource.ID, "expected_version": task.Resource.Version,
	})
	var started startedTask
	decode(t, startRes.Data, &started)
	if started.Task.State != "ready" || started.Run.State != "ready" {
		t.Fatalf("task.start left task %q run %q, want both ready", started.Task.State, started.Run.State)
	}
	if n := f.count("run.list", map[string]any{"scope": scope}); n != 1 {
		t.Fatalf("task.start created %d runs, want exactly 1 (P00.task_start_readies_and_enqueues)", n)
	}
	if n := f.count("attempt.list", map[string]any{"scope": scope}); n != 0 {
		t.Fatalf("task.start already created %d attempts, want 0 before any claim", n)
	}

	_, err := f.invoke(f.owner, "run.claim", "coop-claim", map[string]any{
		"scope": scope, "run_id": started.Run.ID, "worker_id": worker,
		"expected_version": started.Run.Version, "capabilities": []string{},
	})
	if faultCode(err) != contract.CodeInvalidInput {
		t.Fatalf("run.claim: %v, want invalid_input (the accounting reservation's hardcoded empty operation_id) -- "+
			"if this now succeeds, the defect this file documents has been fixed and this whole file should be rewritten "+
			"as a real succeeding journey", err)
	}
	if err == nil || !containsAll(err.Error(), "operation_id") {
		t.Fatalf("run.claim refusal %v does not name operation_id; this may be a DIFFERENT defect than the one this test documents -- investigate before assuming it is the same cause", err)
	}

	// The refusal left nothing behind: the run is exactly as task.start
	// left it, and no attempt was ever created.
	runOut := f.must(f.owner, "run.get", "", map[string]any{"scope": scope, "id": started.Run.ID})
	var runState struct {
		Resource struct {
			Version int64  `json:"version"`
			State   string `json:"state"`
		} `json:"resource"`
	}
	decode(t, runOut.Data, &runState)
	if runState.Resource.State != "ready" || runState.Resource.Version != started.Run.Version {
		t.Fatalf("run after the refused claim: state %q version %d, want unchanged ready/%d", runState.Resource.State, runState.Resource.Version, started.Run.Version)
	}
	if n := f.count("attempt.list", map[string]any{"scope": scope}); n != 0 {
		t.Fatalf("the refused claim left %d attempts behind, want 0", n)
	}
}

// containsAll reports whether s contains every one of subs.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// TestRunClaimRefusalIsIdenticalOverCLIAndMCP (Z02 CLI/MCP parity; Z16
// "start through CLI, continue through MCP"; JOURNEY.
// cross_interface_fault_matrix): the same journey -- task.create and
// task.start started over the real CLI, run.claim continued over a real
// MCP client, a genuine transport switch mid-journey -- reaches the
// identical invalid_input refusal this file's sibling test proves in-
// process, with an equivalent envelope and the run left in the identical
// unclaimed state, under either transport. This is the parity claim R12-004
// requires even for a refusal: "equivalent results, state, errors... on
// isolated matching fixtures."
func TestRunClaimRefusalIsIdenticalOverCLIAndMCP(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	f.provisionTransportCredential(transportToken)
	op := servedOperator(t, f)
	cliTr := cliTransport{op: op, descs: f.catalog.Public()}
	mcpTr := newMCPTransport(t, op, f.catalog.Public())

	org, _ := f.rootOrganization()
	f.activate("coop-worker-cli", "worker.create", map[string]any{"definition": cooperativeWorkerDefinition(org, "coop-worker-cli")})
	worker := f.findWorkerByKey("coop-worker-cli")
	scope := contract.Scope{InstallationID: f.installationID, OrganizationID: org, WorkerID: worker}
	note := f.uploadArtifact("coop-cli-note", []byte("cross-interface cooperative output, never actually reported"), "text/plain")
	def := completableTaskDefinition(f, scope, f.owner.PrincipalID, worker, unconfiguredCurrency, note.Digest)

	// Start the journey through the real CLI (a generated Cobra command,
	// internal/cli.Execute, over the socket client -- the same driver
	// TestTransportParity uses).
	createOut := cliTr.run(t, call{name: "task.create", op: "task.create", key: "cli-mcp-task", input: map[string]any{"scope": scope, "definition": def}})
	if createOut.code != "" {
		t.Fatalf("task.create over the CLI: %s", createOut.code)
	}
	var task struct {
		Resource struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"resource"`
	}
	decode(t, createOut.envelope.Data, &task)

	startOut := cliTr.run(t, call{name: "task.start", op: "task.start", key: "cli-mcp-start", input: map[string]any{"scope": scope, "id": task.Resource.ID, "expected_version": task.Resource.Version}})
	if startOut.code != "" {
		t.Fatalf("task.start over the CLI: %s", startOut.code)
	}
	var started startedTask
	decode(t, startOut.envelope.Data, &started)

	// Continue through MCP: the claim that is refused, a genuine transport
	// switch mid-journey.
	claimOut := mcpTr.run(t, call{name: "run.claim", op: "run.claim", key: "cli-mcp-claim", input: map[string]any{
		"scope": scope, "run_id": started.Run.ID, "worker_id": worker, "expected_version": started.Run.Version, "capabilities": []string{},
	}})
	if claimOut.code != contract.CodeInvalidInput {
		t.Fatalf("run.claim over MCP: code %q signal %s, want invalid_input (see cooperative_journey_test.go's top comment); "+
			"if this now succeeds, this whole file should be rewritten as a real succeeding journey", claimOut.code, claimOut.signal)
	}

	// The identical claim over the real client-side socket (internal/
	// client, not the raw MCP frame) sees the same refusal -- transport
	// parity of the fault itself, not just of a success path.
	clientOp := op
	_, err := clientOp.Call(context.Background(), "run.claim", contract.Request{Schema: contract.SchemaRequest, SubmissionKey: "cli-mcp-claim-2", Input: mustJSON(map[string]any{
		"scope": scope, "run_id": started.Run.ID, "worker_id": worker, "expected_version": started.Run.Version, "capabilities": []string{},
	})})
	if faultCode(err) != contract.CodeInvalidInput {
		t.Fatalf("run.claim over the client socket: %v, want invalid_input matching the MCP refusal above", err)
	}

	// Neither refused attempt left anything behind.
	runOut := f.must(f.owner, "run.get", "", map[string]any{"scope": scope, "id": started.Run.ID})
	var runState struct {
		Resource struct {
			State string `json:"state"`
		} `json:"resource"`
	}
	decode(t, runOut.Data, &runState)
	if runState.Resource.State != "ready" {
		t.Fatalf("run state %q after two refused claims (CLI-started, MCP- and client-continued), want unchanged ready", runState.Resource.State)
	}
	if n := f.count("attempt.list", map[string]any{"scope": scope}); n != 0 {
		t.Fatalf("the refused claims left %d attempts behind, want 0", n)
	}
}
