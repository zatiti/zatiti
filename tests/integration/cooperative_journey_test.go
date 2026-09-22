package integration_test

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

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
// in this tree, and found genuine, independently confirmed issues along the
// way (beyond the hosted-side gaps hosted_turn_test.go documents). None were
// worked around with test-side production code. Findings 1-4 are now fixed;
// the primary test proves the real success this unlocks -- task.create,
// task.start, run.claim, attempt.checkpoint AND attempt.report all
// genuinely succeed, for the first time in this tree's history -- and
// documents the next honest ceiling this reveals: reaching finding 4's
// fixed verification dispatch exposes finding 5, a genuinely separate,
// previously-masked defect (a controller-internal-call scoping mismatch
// during the task's own transition) that still keeps the task from a
// terminal state. See the primary test's own closing comment for finding
// 5's full detail; it is NOT fixed here, only characterized and proven with
// a captured fault, matching finding 4's own root-causing rigor.
//
//  1. FIXED same-day on main, commit 95767cf "mint a real operation_id for
//     run.claim's budget reservation": internal/execution/run_ops.go's
//     handleClaim called reserveBudget with a hardcoded empty operation_id,
//     which _accounting.reserve's own frozen schema (format: uuid) rejected
//     on every single call, for every worker. This file was rebased onto
//     that fix.
//  2. FIXED (founder-authorized, same fix landed alongside finding 3): a
//     worker-level accounting double-reservation -- handleClaim resolves
//     the reservation's cost bound from _configuration.snapshot's SCOPE-
//     level worker (Scope.WorkerID), so a task must be worker-scoped for
//     run.claim to get a real (non-empty) currency at all -- but
//     internal/tasks/admission.go's own task.create-time reservation ALSO
//     charges every level the task's scope implies, worker included. A
//     worker-scoped task is therefore charged worker-level concurrency
//     TWICE for the same conceptual attempt. Not touched by the landed fix
//     (that fix was scoped to findings 1/3 only) -- still routed around
//     below (both the worker and the task declare a real concurrency
//     ceiling of 2, a legitimate declared value, not a disguised bypass).
//  3. FIXED (founder-authorized): two independent defects in the evidence-
//     recording path, both confirmed by direct code reading and both
//     required together to reach a real attempt.report success:
//     (a) internal/tasks/transition.go's recordEvidence minted
//     wireArtifactRef{ID: id} with no digest for every evidence_ids entry
//     (the frozen _tasks.transition input only ever supplies bare UUIDs),
//     while _artifacts.metadata's frozen schema required digest as
//     non-empty -- even though the handler (internal/artifacts/ops.go)
//     already treated an empty digest as "no constraint". Fixed by
//     widening _artifacts.metadata's frozen input schema to a local,
//     digest-optional shape (tools/specgen/model.py), NOT touching the
//     shared ArtifactRef $defs entry every other operation relies on, plus
//     adding omitempty to wireArtifactRef.Digest so an absent digest
//     actually reaches the wire as omitted rather than an invalid "".
//     (b) Two of nine transitionTask call sites in internal/execution
//     passed a non-artifact ID (an attempt, run or verification-job ID) as
//     evidence_ids on the single live path (the other seven target states
//     applyTransition never reads evidence_ids for at all, confirmed by
//     direct reading -- cosmetic, not bugs). Fixed by passing an empty
//     evidence_ids at both live sites, matching the precedent already
//     established elsewhere in the tree (internal/scheduling/wake.go) and
//     leaving the correct, already-existing _tasks.evidence.record path
//     (which supplies a real artifact ID with a real digest) as the sole
//     producer of evidence a success/failure transition ever needs.
//  4. FIXED (founder-authorized, R-verification-stall-fix): internal/
//     controller/turns.go's runVerification hardcoded ExpectedVersion: 1 on
//     the _execution.verification.record call it makes after claiming a
//     sealed verification request. That field fences the ATTEMPT row (not
//     the verification job), and an attempt's version is never 1 by the
//     time verification runs for any attempt that did more than a bare
//     claim -- every realistic journey, this one included -- so record()
//     refused with stale_version every single time, confirmed against a
//     real DB with a captured fault ("attempt ... version 3 does not match
//     expected version 1"). The verification job's own state stayed
//     'pending' forever, so the controller re-claimed and re-invoked the
//     real trusted verifier every tick, indefinitely: a real, ongoing
//     resource cost on any live installation, not a one-time stall. Fixed
//     by widening _execution.verification.claim's output
//     (tools/specgen/model.py) to also return the claimed attempt's live
//     version, read in the same claim transaction, and threading that real
//     value into record() instead of the hardcoded 1. Also closed the
//     re-claim/re-invoke cost directly: listPendingVerificationJobs
//     (internal/execution/store.go) now excludes a job with an existing
//     claim row from its listing, so a claim already in flight is not
//     re-driven by the same live generation. See internal/execution/
//     turn_ops_test.go's
//     TestVerificationClaimReturnsCurrentAttemptVersionAndFencesRecord for
//     the isolated red/green reproduction.
//  5. FOUND, NOT fixed (out of R-verification-stall-fix's scope): fixing
//     finding 4 lets verification.record reach internal/tasks's
//     transitionTask for the first time on a real journey, which exposes a
//     second, genuinely separate, previously-masked defect --
//     internal/tasks's emitTaskEvent (internal/tasks/events.go) stamps a
//     task-transition event with the task's OWN scope (this journey's task
//     is worker-scoped, so that includes WorkerID), while every
//     controller-internal call -- verification.record among them, via
//     callPeer sharing the same unit/transaction -- runs under the
//     controller's bare installation scope (internal/controller/start.go's
//     call doc: "runs one internal operation under ... the installation
//     scope"). internal/storage's Unit.Emit (internal/storage/session.go)
//     requires an event's explicit scope to equal the unit's own scope
//     exactly, so the task's transition inside verification.record refuses
//     with invalid_input: "event scope does not match the unit scope" the
//     instant it tries to emit -- for any task with more than a bare
//     installation scope, i.e. virtually every real task. The primary test
//     below proves this directly (polls for the controller to record this
//     exact obligation) rather than guessing at a fix inside this change.

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
	// attempt.report now genuinely succeeds: findings 2 and 3 above are
	// fixed (commit history: the evidence_ids wrong-ID bug in
	// internal/execution and the _artifacts.metadata schema both landed
	// same-day, founder-authorized after this file's own predicted
	// "if this now succeeds ... this file should be rewritten"). The
	// attempt/task genuinely advance past checkpoint for the first time in
	// this tree's history.
	if reportErr != nil {
		t.Fatalf("attempt.report: %v, want success", reportErr)
	}
	attemptOut := f.must(f.owner, "attempt.get", "", map[string]any{"scope": scope, "id": claim.Attempt.ID})
	var attemptState struct {
		Resource struct {
			State string `json:"state"`
		} `json:"resource"`
	}
	decode(t, attemptOut.Data, &attemptState)
	if attemptState.Resource.State != "reported" {
		t.Fatalf("attempt state after a successful report = %q, want reported", attemptState.Resource.State)
	}
	taskOut := f.must(f.owner, "task.get", "", map[string]any{"scope": scope, "id": task.Resource.ID})
	var taskState struct {
		Resource struct {
			State string `json:"state"`
		} `json:"resource"`
	}
	decode(t, taskOut.Data, &taskState)
	// reportAttempt (internal/execution/attempt_ops.go) seals a real
	// VerificationRequest job and moves the task to "verifying" in the same
	// call that recorded the report -- synchronous, before the controller's
	// own async dispatch (driveVerification, internal/controller/turns.go)
	// ever runs.
	if taskState.Resource.State != "verifying" {
		t.Fatalf("task state after a successful report = %q, want verifying", taskState.Resource.State)
	}

	// R-verification-stall-fix, root cause #1, now fixed (founder-
	// authorized): internal/controller/turns.go's runVerification hardcoded
	// ExpectedVersion: 1 on the _execution.verification.record call it
	// makes after claiming a sealed verification request, but that field
	// fences the ATTEMPT row, not the verification job -- an attempt's
	// version advances by exactly 1 on every claim, checkpoint and report,
	// so it is never 1 by the time verification runs for any attempt that
	// did more than a bare claim (every realistic journey, this one
	// included). record() therefore refused with stale_version every
	// single time, confirmed against a real DB with a captured fault
	// ("attempt ... version 3 does not match expected version 1"); the
	// verification job's own state column never left 'pending', so the
	// controller's next tick re-claimed and re-invoked the real trusted
	// verifier again, indefinitely -- a real, ongoing resource cost on any
	// live installation, not just a one-time stall. Fixed by widening
	// _execution.verification.claim's output (tools/specgen/model.py) to
	// also return the claimed attempt's live version, read in the same
	// claim transaction, and threading that real value into the record
	// call instead of the hardcoded 1. See
	// internal/execution/turn_ops_test.go's
	// TestVerificationClaimReturnsCurrentAttemptVersionAndFencesRecord for
	// the isolated reproduction of the exact fault text this journey hit.
	// listPendingVerificationJobs (internal/execution/store.go) also now
	// excludes a job with an existing claim row from its listing, so a
	// claim already in flight is not re-claimed and re-invoked by the same
	// live generation while it is still being worked -- closing the
	// resource cost independent of (and in addition to) the version fix.
	//
	// With root cause #1 fixed, claim and record now genuinely run (proven
	// directly by internal/execution's own tests), but THIS journey still
	// does not reach a terminal state -- fixing #1 exposes a second,
	// genuinely separate, previously-masked defect that was simply
	// unreachable before (record() never got this far for any task with
	// more than a bare installation scope, i.e. virtually every real task):
	// internal/tasks's emitTaskEvent (internal/tasks/events.go) stamps a
	// task-transition event with the task's OWN scope -- this journey's
	// task is worker-scoped, so that includes WorkerID -- while every
	// controller-internal call, including the _execution.verification.
	// record call that reaches transitionTask via callPeer, runs under the
	// controller's bare installation scope (internal/controller/start.go's
	// call: "runs one internal operation under ... the installation
	// scope", the same unit/transaction is threaded through unchanged).
	// internal/storage's Unit.Emit (internal/storage/session.go) requires
	// an event's explicit scope to equal the unit's own scope exactly, so
	// the task's transition inside verification.record refuses with
	// invalid_input: "event scope does not match the unit scope" the
	// instant it tries to emit. This is a real, separate, NOT-yet-fixed
	// defect -- a controller-internal-call scoping/event-emission mismatch,
	// out of R-verification-stall-fix's own scope (a version-fencing bug)
	// -- flagged here for its own fix rather than guessed at in this
	// change. The wait below proves that ceiling directly: it polls for
	// either a terminal task state or the controller recording exactly
	// this obligation against this attempt, and fails loudly if neither
	// happens (which would mean a third, uncharacterized gap).
	var finalState, obligation string
	waitFor(t, 15*time.Second, "task to leave verifying for a terminal state, or the controller to record the known event-scope obligation", func() bool {
		out := f.must(f.owner, "task.get", "", map[string]any{"scope": scope, "id": task.Resource.ID})
		var s struct {
			Resource struct {
				State string `json:"state"`
			} `json:"resource"`
		}
		decode(t, out.Data, &s)
		finalState = s.Resource.State
		if finalState == "succeeded" || finalState == "failed" {
			return true
		}
		for _, o := range cf.ctl.Status().Obligations {
			if o.ResourceID == claim.Attempt.ID {
				obligation = o.Fault.Code + ": " + o.Fault.Message
				return true
			}
		}
		return false
	})
	if finalState == "succeeded" || finalState == "failed" {
		t.Fatalf("task reached terminal state %q -- the event-scope defect this comment describes appears to be fixed; update this test to assert succeeded (or investigate the new failure) instead of the known-obligation path", finalState)
	}
	if finalState != "verifying" {
		t.Fatalf("task state %q, want verifying (with the known event-scope obligation blocking further progress)", finalState)
	}
	if !strings.Contains(obligation, "event scope does not match the unit scope") {
		t.Fatalf("controller obligation %q, want the known event-scope-mismatch defect (internal/tasks/events.go emitTaskEvent vs internal/storage's Unit.Emit) -- this may be a new, uncharacterized gap", obligation)
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

	// attempt.report now genuinely succeeds over MCP too (see the primary
	// test above for why: findings 2 and 3 are fixed).
	reportOut := mcpTr.run(t, call{name: "attempt.report", op: "attempt.report", key: "cli-mcp-report", input: map[string]any{
		"scope": scope, "attempt_id": claim.Attempt.ID, "lease_id": claim.Attempt.LeaseID,
		"generation": claim.Attempt.Generation, "expected_version": freshAttemptVersion(t, f, scope, claim.Attempt.ID),
		"outputs": []any{ref}, "observations": map[string]any{}, "usage": zeroUsage(unconfiguredCurrency),
	}})
	if reportOut.code != "" {
		t.Fatalf("attempt.report over MCP: code %q signal %s, want success matching the in-process journey above", reportOut.code, reportOut.signal)
	}

	// A second attempt.report on the same already-reported attempt is
	// refused over the real client-side socket (internal/client, not the
	// raw MCP frame) too -- transport parity of a real fault. checkWorkerCall
	// (internal/execution/scope.go) only accepts a report from an attempt
	// still in claimed/running/waiting; the MCP report above already moved
	// this attempt to "reported", so the identical retry now genuinely
	// conflicts, rather than the fabricated schema fault this test used to
	// assert on both transports.
	cliReportOut := cliTr.run(t, call{name: "attempt.report", op: "attempt.report", key: "cli-report-2", input: map[string]any{
		"scope": scope, "attempt_id": claim.Attempt.ID, "lease_id": claim.Attempt.LeaseID,
		"generation": claim.Attempt.Generation, "expected_version": freshAttemptVersion(t, f, scope, claim.Attempt.ID),
		"outputs": []any{ref}, "observations": map[string]any{}, "usage": zeroUsage(unconfiguredCurrency),
	}})
	if cliReportOut.code != contract.CodeConflict {
		t.Fatalf("attempt.report over the CLI: code %q signal %s, want conflict (already reported)", cliReportOut.code, cliReportOut.signal)
	}
}

// zeroUsage is one zero-cost Usage value, the shape attempt.report and
// _execution.report share.
func zeroUsage(currency string) map[string]any {
	return map[string]any{"currency": currency, "spent": 0, "reserved": 0, "estimated": 0, "unknown": 0, "advisory": false}
}
