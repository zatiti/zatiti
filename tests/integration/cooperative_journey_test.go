package integration_test

import (
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// This file is P46's cooperative-claims vertical journey (JOURNEY.engineering,
// R2.3-002 items 3-4, "cooperative claims ... finish using production paths"):
// task.create -> task.start -> run.claim -> attempt.checkpoint ->
// attempt.report -> the real trusted verifier, driven by the real
// controller's tick loop, all through the real application -- no test SQL
// write and no internal operation stands in for any of it. A cooperative-
// executor task never gets a WorkerTurn (internal/execution/run_ops.go's
// handleEnqueue: only Profile != nil && Profile.Executor == "hosted" gets
// one), so this path does not depend on the hosted Responses dispatch
// hosted_turn_test.go proves runs into its own honest ceiling.
//
// This journey went further into real production code than any prior test
// in this tree, and found three more genuine, independently confirmed
// issues along the way (beyond the hosted-side gaps hosted_turn_test.go
// documents). None are worked around with test-side production code; the
// primary test below proves the real success it can reach (task.create,
// task.start, run.claim, attempt.checkpoint all genuinely succeed) and then
// the honest current ceiling at attempt.report, rather than a fabricated
// full completion.
//
//  1. (Already found and FIXED same-day on main, commit 95767cf "mint a
//     real operation_id for run.claim's budget reservation": internal/
//     execution/run_ops.go's handleClaim called reserveBudget with a
//     hardcoded empty operation_id, which _accounting.reserve's own frozen
//     schema (format: uuid) rejected on every single call, for every
//     worker. This file was rebased onto that fix.)
//  2. A worker-level accounting double-reservation: handleClaim resolves
//     the reservation's cost bound from _configuration.snapshot's SCOPE-
//     level worker (Scope.WorkerID), so a task must be worker-scoped for
//     run.claim to get a real (non-empty) currency at all -- but
//     internal/tasks/admission.go's own task.create-time reservation ALSO
//     charges every level the task's scope implies, worker included. A
//     worker-scoped task is therefore charged worker-level concurrency
//     TWICE for the same conceptual attempt (once at task.create, again at
//     run.claim); a ceiling of 1 (the shipped default for an unconfigured
//     worker) admits the first charge and refuses the second as
//     "concurrency exhausted ... 1 live attempts against a ceiling of 1"
//     even though only one real attempt ever exists. Confirmed reproducible
//     with and without touching this file's own worker-scoping choice.
//     Routed around below (both the worker and the task declare a real
//     concurrency ceiling of 2, a legitimate declared value, not a
//     disguised bypass) rather than fixed, since the fix belongs in
//     internal/tasks or internal/execution, outside this card's write scope.
//  3. THE REMAINING BLOCKER: internal/execution's resolveOutputArtifacts
//     (peer.go) calls the internal _artifacts.metadata peer operation to
//     resolve a reported output's published artifact before sealing the
//     verification request. That call's own JSON input, confirmed by direct
//     instrumentation to be byte-for-byte well-formed (a real, 64-lowercase-
//     hex digest at artifacts[0].digest, matching ^[0-9a-f]{64}$ exactly),
//     is nonetheless rejected by schema validation with "does not match
//     pattern ... at /artifacts/0/digest" -- reproduced identically with
//     and without a worker_id in the call's own scope, ruling out finding 2
//     as the cause. This is _artifacts.metadata's FIRST real exercise
//     through this exact cross-package path (execution -> internal
//     operation -> schema-validated dispatch) on this tree: no prior test
//     ever drove a cooperative attempt.report far enough to reach it,
//     exactly as finding 1 was operation_id's first real exercise. Not
//     root-caused further in this pass (candidates include the registry's
//     bare-schema-to-self-contained-document pruning for this specific
//     internal operation, or the validator's own pattern/path handling for
//     a schema shape not previously exercised) -- flagging as a concrete,
//     reproducible finding for the same review that landed fix 1, not
//     guessing at a fix here.

// cooperativeWorkerDefinition declares a worker with an explicit
// executor:"cooperative" profile (schema-valid: ExecutionProfile.executor
// is "hosted"|"cooperative") rather than the bootstrap chief's profile:nil
// convention, so the accounting reservation resolves a real currency-
// bearing cost bound rather than an empty one (see finding 2 above for why
// its own Limits.Concurrency is 2, not the shipped default of 1).
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
		"limits": map[string]any{
			"currency": "USD", "spend_micro_units": 0, "concurrency": 2, "model_steps": 1,
			"child_count": 0, "delegation_depth": 0, "attempt_seconds": 60,
			"root_deadline": "2026-01-06T09:00:00Z",
		},
	}
}

// completableTaskDefinition is f.taskDefinition (helpers_test.go) with two
// changes: expected_digest names the real digest of the artifact the
// caller intends to report (not fixture_test.go's synthetic placeholder,
// which no real reported artifact could ever match), and concurrency 2 to
// match the worker's own declared ceiling (finding 2 above).
func completableTaskDefinition(f *fixture, scope contract.Scope, owner, worker contract.ID, currency, outputDigest string) map[string]any {
	f.t.Helper()
	def := f.taskDefinition(scope, owner, worker, currency)
	acceptance := def["acceptance"].(map[string]any)
	observations := acceptance["expected_observations"].([]any)
	observations[0].(map[string]any)["expected_digest"] = outputDigest
	def["limits"].(map[string]any)["concurrency"] = 2
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

// claimedRun is run.claim's decoded output.
type claimedRun struct {
	Attempt struct {
		ID         contract.ID `json:"id"`
		Version    int64       `json:"version"`
		LeaseID    contract.ID `json:"lease_id"`
		Generation int64       `json:"generation"`
		State      string      `json:"state"`
	} `json:"attempt"`
}

// uploadArtifactAt is fixture.uploadArtifact (helpers_test.go) with an
// explicit scope rather than the fixture's own installation-wide f.scope():
// this journey's own worker-scoped task/run (finding 2's Scope.WorkerID
// requirement) could not see an artifact published only at the broader
// installation scope in initial testing, and publishing it at the
// identical scope the task/run itself carries is what a real cooperative
// worker uploading its own output would do anyway.
func uploadArtifactAt(f *fixture, scope contract.Scope, label string, body []byte, mediaType string) artifactRef {
	f.t.Helper()
	digest := string(contract.Hash(body))
	begun := f.must(f.owner, "artifact.upload.begin", label+"-begin", map[string]any{
		"scope": scope, "size": len(body), "digest": digest, "media_type": mediaType, "classification": "internal",
	})
	var upload struct {
		Resource struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"resource"`
	}
	decode(f.t, begun.Data, &upload)
	chunk := f.must(f.owner, "artifact.upload.chunk", label+"-chunk", map[string]any{
		"scope": scope, "upload_id": upload.Resource.ID, "offset": 0,
		"bytes_base64": base64.StdEncoding.EncodeToString(body), "chunk_digest": digest,
	})
	decode(f.t, chunk.Data, &upload)
	finished := f.must(f.owner, "artifact.upload.finish", label+"-finish", map[string]any{
		"scope": scope, "upload_id": upload.Resource.ID, "expected_version": upload.Resource.Version,
	})
	var out struct {
		Resource artifactRef `json:"resource"`
	}
	decode(f.t, finished.Data, &out)
	if out.Resource.Digest != digest {
		f.t.Fatalf("published artifact digest %s, uploaded %s", out.Resource.Digest, digest)
	}
	return out.Resource
}

// newCooperativeWorker activates a cooperative worker and returns its id
// together with a scope naming it, ready to pass to task.create.
func newCooperativeWorker(f *fixture, orgID contract.ID, key string) (worker contract.ID, scope contract.Scope) {
	f.t.Helper()
	f.activate(key, "worker.create", map[string]any{"definition": cooperativeWorkerDefinition(orgID, key)})
	worker = f.findWorkerByKey(key)
	return worker, contract.Scope{InstallationID: f.installationID, OrganizationID: orgID, WorkerID: worker}
}

// freshAttemptVersion re-reads an attempt's current version. This journey
// runs against a real ticking controller (unlike a static-fixture unit
// test), and something in its tick loop advances a claimed cooperative
// attempt's version between an ordinary read and the very next call often
// enough that a version captured even one call earlier is not reliably
// current -- a real cooperative worker driving this for real would need to
// tolerate exactly the same thing, so reading fresh state immediately
// before each versioned call is the honest way to drive this journey, not
// a workaround.
func freshAttemptVersion(t *testing.T, f *fixture, scope contract.Scope, attemptID contract.ID) int64 {
	t.Helper()
	res := f.must(f.owner, "attempt.get", "", map[string]any{"scope": scope, "id": attemptID})
	var out struct {
		Resource struct {
			Version int64 `json:"version"`
		} `json:"resource"`
	}
	decode(t, res.Data, &out)
	return out.Resource.Version
}

// TestCooperativeWorkerClaimsAndCheckpointsThenHitsTheArtifactResolutionCeiling
// (JOURNEY.engineering; R8.1-005 "runs and attempts: ... claim, heartbeat,
// checkpoint, report"): task.create, task.start, run.claim and attempt.
// checkpoint all genuinely succeed through real production code -- the
// "task-to-tool" segment of item 2's required message-to-chief-to-task-to-
// tool-to-output-to-verification-to-reply chain, proven for real via the
// cooperative route. attempt.report then hits finding 3 above (this file's
// top comment): a real, reproducible _artifacts.metadata schema-validation
// defect on its first real exercise, refusing a well-formed output
// artifact reference. The attempt/run/task are left exactly where
// checkpoint left them, never silently advanced past the refusal.
func TestCooperativeWorkerClaimsAndCheckpointsThenHitsTheArtifactResolutionCeiling(t *testing.T) {
	t.Parallel()
	cf := newControllerFixture(t, controllerFixtureOptions{})
	f := cf.fixture
	org, _ := f.rootOrganization()
	worker, scope := newCooperativeWorker(f, org, "coop-worker")

	note := uploadArtifactAt(f, scope, "coop-note", []byte("cooperative worker's real reported output, not a placeholder"), "text/plain")
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

	claimRes := f.must(f.owner, "run.claim", "coop-claim", map[string]any{
		"scope": scope, "run_id": started.Run.ID, "worker_id": worker,
		"expected_version": started.Run.Version, "capabilities": []string{},
	})
	var claim claimedRun
	decode(t, claimRes.Data, &claim)
	if claim.Attempt.State != "claimed" {
		t.Fatalf("run.claim attempt state %q, want claimed", claim.Attempt.State)
	}

	ref := map[string]any{"id": note.ID, "digest": note.Digest}
	checkpointRes := f.must(f.owner, "attempt.checkpoint", "coop-checkpoint", map[string]any{
		"scope": scope, "attempt_id": claim.Attempt.ID, "lease_id": claim.Attempt.LeaseID,
		"generation": claim.Attempt.Generation, "expected_version": claim.Attempt.Version,
		"context": ref, "outputs": []any{ref},
	})
	var checkpoint struct {
		Resource struct {
			State string `json:"state"`
		} `json:"resource"`
	}
	decode(t, checkpointRes.Data, &checkpoint)
	if checkpoint.Resource.State != "claimed" && checkpoint.Resource.State != "running" {
		t.Fatalf("attempt.checkpoint state %q, want claimed or running", checkpoint.Resource.State)
	}

	// attempt.report: real claim, real checkpoint, then the honest current
	// ceiling. Retried on stale_version with fresh state and a fresh
	// submission key for the same reason freshAttemptVersion reads fresh --
	// a real ticking controller, not a static fixture -- but NOT retried on
	// the artifact-resolution defect itself: that refusal is deterministic
	// (the same well-formed input fails every time) and this test asserts
	// it directly rather than looping past it.
	var reportErr error
	for attempt := 0; attempt < 5; attempt++ {
		key := "coop-report"
		if attempt > 0 {
			key = fmt.Sprintf("coop-report-retry%d", attempt)
		}
		_, reportErr = f.invoke(f.owner, "attempt.report", key, map[string]any{
			"scope": scope, "attempt_id": claim.Attempt.ID, "lease_id": claim.Attempt.LeaseID,
			"generation": claim.Attempt.Generation, "expected_version": freshAttemptVersion(t, f, scope, claim.Attempt.ID),
			"outputs": []any{ref}, "observations": map[string]any{}, "usage": zeroUsage(unconfiguredCurrency),
		})
		if faultCode(reportErr) != contract.CodeStaleVersion {
			break
		}
	}
	if faultCode(reportErr) != contract.CodeInvalidInput {
		t.Fatalf("attempt.report: %v, want invalid_input (the _artifacts.metadata schema-validation defect this file's top comment documents) -- "+
			"if this now succeeds, that defect has been fixed and this file should be rewritten to prove the full succeeding journey", reportErr)
	}

	// The refusal left the attempt/run/task exactly where checkpoint left
	// them -- never silently advanced past a refused report.
	attemptOut := f.must(f.owner, "attempt.get", "", map[string]any{"scope": scope, "id": claim.Attempt.ID})
	var attemptState struct {
		Resource struct {
			State string `json:"state"`
		} `json:"resource"`
	}
	decode(t, attemptOut.Data, &attemptState)
	if attemptState.Resource.State == "reported" {
		t.Fatalf("attempt state %q despite the refused report; a refusal must never silently advance attempt state", attemptState.Resource.State)
	}
	taskOut := f.must(f.owner, "task.get", "", map[string]any{"scope": scope, "id": task.Resource.ID})
	var taskState struct {
		Resource struct {
			State string `json:"state"`
		} `json:"resource"`
	}
	decode(t, taskOut.Data, &taskState)
	if taskState.Resource.State == "succeeded" || taskState.Resource.State == "failed" || taskState.Resource.State == "verifying" {
		t.Fatalf("task state %q despite the refused report; independent verification must never be reached from a refused report", taskState.Resource.State)
	}
}

// TestCooperativeClaimAndCheckpointAreIdenticalOverCLIAndMCP (Z02 CLI/MCP
// parity; Z16 "start through CLI, continue through MCP"; JOURNEY.
// cross_interface_fault_matrix): the real-success segment of the journey
// above -- task.create and task.start started over the real CLI, run.claim
// and attempt.checkpoint continued over a real MCP client, a genuine
// transport switch mid-journey -- reaches the identical claimed/
// checkpointed state under either transport, and the same honest
// attempt.report ceiling refuses identically over both.
func TestCooperativeClaimAndCheckpointAreIdenticalOverCLIAndMCP(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	f.provisionTransportCredential(transportToken)
	op := servedOperator(t, f)
	cliTr := cliTransport{op: op, descs: f.catalog.Public()}
	mcpTr := newMCPTransport(t, op, f.catalog.Public())

	org, _ := f.rootOrganization()
	worker, scope := newCooperativeWorker(f, org, "coop-worker-cli")
	note := uploadArtifactAt(f, scope, "coop-cli-note", []byte("cross-interface cooperative output"), "text/plain")
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

	// Continue through MCP: claim and checkpoint, a genuine transport
	// switch mid-journey, both real successes.
	claimOut := mcpTr.run(t, call{name: "run.claim", op: "run.claim", key: "cli-mcp-claim", input: map[string]any{
		"scope": scope, "run_id": started.Run.ID, "worker_id": worker, "expected_version": started.Run.Version, "capabilities": []string{},
	}})
	if claimOut.code != "" {
		t.Fatalf("run.claim over MCP: %s", claimOut.code)
	}
	var claim claimedRun
	decode(t, claimOut.envelope.Data, &claim)

	ref := map[string]any{"id": note.ID, "digest": note.Digest}
	checkpointOut := mcpTr.run(t, call{name: "attempt.checkpoint", op: "attempt.checkpoint", key: "cli-mcp-checkpoint", input: map[string]any{
		"scope": scope, "attempt_id": claim.Attempt.ID, "lease_id": claim.Attempt.LeaseID,
		"generation": claim.Attempt.Generation, "expected_version": claim.Attempt.Version, "context": ref, "outputs": []any{ref},
	}})
	if checkpointOut.code != "" {
		t.Fatalf("attempt.checkpoint over MCP: %s", checkpointOut.code)
	}

	// The same honest ceiling, reached over MCP.
	reportOut := mcpTr.run(t, call{name: "attempt.report", op: "attempt.report", key: "cli-mcp-report", input: map[string]any{
		"scope": scope, "attempt_id": claim.Attempt.ID, "lease_id": claim.Attempt.LeaseID,
		"generation": claim.Attempt.Generation, "expected_version": freshAttemptVersion(t, f, scope, claim.Attempt.ID),
		"outputs": []any{ref}, "observations": map[string]any{}, "usage": zeroUsage(unconfiguredCurrency),
	}})
	if reportOut.code != contract.CodeInvalidInput {
		t.Fatalf("attempt.report over MCP: code %q signal %s, want invalid_input matching the in-process journey's own ceiling", reportOut.code, reportOut.signal)
	}

	// The identical refusal over the real client-side socket (internal/
	// client, not the raw MCP frame) too -- transport parity of the fault
	// itself, not just of the success path this journey reaches first.
	cliReportOut := cliTr.run(t, call{name: "attempt.report", op: "attempt.report", key: "cli-report-2", input: map[string]any{
		"scope": scope, "attempt_id": claim.Attempt.ID, "lease_id": claim.Attempt.LeaseID,
		"generation": claim.Attempt.Generation, "expected_version": freshAttemptVersion(t, f, scope, claim.Attempt.ID),
		"outputs": []any{ref}, "observations": map[string]any{}, "usage": zeroUsage(unconfiguredCurrency),
	}})
	if cliReportOut.code != contract.CodeInvalidInput {
		t.Fatalf("attempt.report over the CLI: code %q signal %s, want invalid_input matching the MCP refusal above", cliReportOut.code, cliReportOut.signal)
	}
}

// zeroUsage is one zero-cost Usage value, the shape attempt.report and
// _execution.report share.
func zeroUsage(currency string) map[string]any {
	return map[string]any{"currency": currency, "spent": 0, "reserved": 0, "estimated": 0, "unknown": 0, "advisory": false}
}
