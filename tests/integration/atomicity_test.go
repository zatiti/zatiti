package integration_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// observableState is everything the public surface exposes about the owners
// a task admission touches. Atomicity cases compare it before and after a
// refused mutation; no sibling table is read directly.
type observableState struct {
	tasks, runs int
	// events counts domain events only. The evidence owner's own
	// evidence.command.* records are the retained command dispositions and
	// are asserted separately.
	events int
	usage  string
}

func (f *fixture) observe() observableState {
	f.t.Helper()
	usage := f.must(f.owner, "usage.get", "", map[string]any{"scope": f.scope()})
	return observableState{
		tasks:  f.count("task.list", map[string]any{"scope": f.scope()}),
		runs:   f.count("run.list", map[string]any{"scope": f.scope()}),
		events: f.domainEventCount(),
		usage:  canonical(f.t, usage.Data),
	}
}

// domainEventCount counts events that are not the evidence owner's command
// records.
func (f *fixture) domainEventCount() int {
	n := 0
	for _, k := range f.eventKinds() {
		if !strings.HasPrefix(k, "evidence.command.") {
			n++
		}
	}
	return n
}

// TestPeerRefusalRollsBackEveryOwner (Z14 atomic state/event failure, Z09
// missing price/currency): task.create composes tasks, configuration and
// accounting in one transaction. When the accounting peer refuses, no
// owner's rows and no events survive. budget_unavailable is a refusal that
// may stop holding, so the application retains no command for it and an
// identical retry re-evaluates instead of replaying.
func TestPeerRefusalRollsBackEveryOwner(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	org, chief := f.rootOrganization()
	scope := contract.Scope{InstallationID: f.installationID, OrganizationID: org}
	before := f.observe()

	// No budget is configured, so accounting refuses a task priced in USD.
	input := map[string]any{"scope": scope, "definition": taskDefinition(scope, f.owner.PrincipalID, chief, "USD")}
	_, err := f.invoke(f.owner, "task.create", "atomic-peer-1", input)
	if err == nil {
		t.Fatal("task.create in an unconfigured currency succeeded")
	}
	refusal := errAs(err)
	if refusal == nil {
		t.Fatalf("task.create refusal is not a contract fault: %v", err)
	}
	if after := f.observe(); after != before {
		t.Fatalf("refused task.create left state behind:\n before %+v\n after  %+v", before, after)
	}

	if refusal.Code != contract.CodeBudgetUnavailable {
		t.Fatalf("task.create refusal %q, want budget_unavailable from the accounting peer", refusal.Code)
	}
	if _, err := f.command("task.create", "atomic-peer-1"); faultCode(err) != contract.CodeNotFound {
		t.Fatalf("command lookup for a rolled-back budget refusal: %v, want not_found (the command row rolled back with the owners)", err)
	}

	// The identical retry re-evaluates and is refused the same way, still
	// without leaving anything behind.
	_, err = f.invoke(f.owner, "task.create", "atomic-peer-1", input)
	if faultCode(err) != contract.CodeBudgetUnavailable {
		t.Fatalf("identical retry: %v, want budget_unavailable", err)
	}
	if after := f.observe(); after != before {
		t.Fatalf("retried refusal changed state:\n before %+v\n after  %+v", before, after)
	}
}

// replayedCode reads the fault code of a refusal however the dispatcher
// delivered it: as an error, or as a failed envelope.
func replayedCode(res contract.Result, err error) string {
	if code := faultCode(err); code != "" {
		return code
	}
	if res.Error != nil {
		return res.Error.Code
	}
	return ""
}

// TestLateEventFailureRollsBackStateAndEvents (Z14 atomic state/event
// failure): a task whose definition scope is narrower than the request scope
// passes every owner's checks and writes, then storage refuses the event
// because its scope does not match the unit. The state written before the
// event in the same transaction must not survive.
func TestLateEventFailureRollsBackStateAndEvents(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	org, chief := f.rootOrganization()
	narrow := contract.Scope{InstallationID: f.installationID, OrganizationID: org}
	before := f.observe()

	lateInput := map[string]any{
		"scope": f.scope(), "definition": taskDefinition(narrow, f.owner.PrincipalID, chief, unconfiguredCurrency),
	}
	_, refusal := f.invoke(f.owner, "task.create", "atomic-event-1", lateInput)
	if faultCode(refusal) != contract.CodeInvalidInput {
		t.Fatalf("task.create with a mismatched event scope: %v, want invalid_input", refusal)
	}
	if after := f.observe(); after != before {
		t.Fatalf("failed event emission left state behind:\n before %+v\n after  %+v", before, after)
	}
	// The deterministic refusal is retained in its own short transaction:
	// exactly one evidence record, the same fault, and an identical retry
	// replays it without executing.
	cmd, err := f.command("task.create", "atomic-event-1")
	if err != nil {
		t.Fatalf("refused command is not retained: %v", err)
	}
	if cmd.Status != contract.StatusFailed || cmd.ErrorCode != contract.CodeInvalidInput ||
		cmd.Result.Error == nil || cmd.Result.Error.Message != errAs(refusal).Message {
		t.Fatalf("retained command %+v does not reproduce the refusal %v", cmd, refusal)
	}
	res, err := f.invoke(f.owner, "task.create", "atomic-event-1", lateInput)
	if got := replayedCode(res, err); got != contract.CodeInvalidInput {
		t.Fatalf("identical retry of the refused command returned %q, want the retained invalid_input", got)
	}
	if after := f.observe(); after != before {
		t.Fatalf("replayed refusal changed state:\n before %+v\n after  %+v", before, after)
	}
	failed := 0
	for _, k := range f.eventKinds() {
		if k == "evidence.command.failed" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("refusal and its replay left %d evidence.command.failed events, want one", failed)
	}

	// The same definition under its own scope commits state, run admission
	// and events together.
	created := f.must(f.owner, "task.create", "atomic-event-2", map[string]any{
		"scope": narrow, "definition": taskDefinition(narrow, f.owner.PrincipalID, chief, unconfiguredCurrency),
	})
	after := f.observe()
	if after.tasks != before.tasks+1 || after.events <= before.events {
		t.Fatalf("committed task.create: tasks %d->%d events %d->%d, want one task and new events",
			before.tasks, after.tasks, before.events, after.events)
	}
	committed, err := f.command("task.create", "atomic-event-2")
	if err != nil || committed.ID != created.CommandID || committed.Status != contract.StatusCompleted {
		t.Fatalf("committed command %+v (err %v), want completed %s", committed, err, created.CommandID)
	}
}

func principalInput(f *fixture, name string) map[string]any {
	return map[string]any{"scope": f.scope(), "definition": map[string]any{
		"kind": "client_agent", "name": name, "scope": f.scope(), "revoked": false,
	}}
}

// TestSubmissionReplayReturnsRetainedResult (R8.4-005, Z04 repeated
// submission key): an identical retry returns the original command and
// result and executes nothing; changed input under the same key conflicts;
// the command is recoverable by lookup.
func TestSubmissionReplayReturnsRetainedResult(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	first := f.must(f.owner, "principal.create", "replay-1", principalInput(f, "replay-agent"))
	principals, events := f.count("principal.list", map[string]any{"scope": f.scope()}), f.eventCount()

	second := f.must(f.owner, "principal.create", "replay-1", principalInput(f, "replay-agent"))
	if second.CommandID != first.CommandID || second.Status != first.Status {
		t.Fatalf("replay returned command %s status %s, original %s %s", second.CommandID, second.Status, first.CommandID, first.Status)
	}
	if canonical(t, second.Data) != canonical(t, first.Data) {
		t.Fatalf("replayed data differs from the original:\n original %s\n replay   %s", first.Data, second.Data)
	}
	if n := f.count("principal.list", map[string]any{"scope": f.scope()}); n != principals {
		t.Fatalf("replay changed the principal count from %d to %d", principals, n)
	}
	if n := f.eventCount(); n != events {
		t.Fatalf("replay emitted events: %d -> %d", events, n)
	}

	_, err := f.invoke(f.owner, "principal.create", "replay-1", principalInput(f, "replay-other"))
	if faultCode(err) != contract.CodeSubmissionConflict {
		t.Fatalf("changed input under a used key: %v, want submission_conflict", err)
	}
	if n := f.count("principal.list", map[string]any{"scope": f.scope()}); n != principals {
		t.Fatalf("conflicting submission changed the principal count from %d to %d", principals, n)
	}

	cmd, err := f.command("principal.create", "replay-1")
	if err != nil {
		t.Fatalf("command lookup: %v", err)
	}
	if cmd.ID != first.CommandID || cmd.Status != contract.StatusCompleted || cmd.Result.CommandID != first.CommandID {
		t.Fatalf("command lookup %+v does not return the original command %s", cmd, first.CommandID)
	}
	if canonical(t, cmd.Result.Data) != canonical(t, first.Data) {
		t.Fatalf("command lookup retains %s, original result was %s", cmd.Result.Data, first.Data)
	}
}

// TestReplayPrecedesStaleVersionValidation (wire conventions): replaying an
// identical input returns the original disposition before stale-version
// validation, even though the resource version has since moved on.
func TestReplayPrecedesStaleVersionValidation(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	pause := map[string]any{"scope": f.scope(), "expected_version": 1}
	first := f.must(f.owner, "installation.pause", "stale-1", pause)

	// The installation is now at version 2: the same input is stale for any
	// new command, but the retained command replays.
	_, err := f.invoke(f.owner, "installation.pause", "stale-2", pause)
	if faultCode(err) != contract.CodeStaleVersion {
		t.Fatalf("new command against the old version: %v, want stale_version", err)
	}
	replay := f.must(f.owner, "installation.pause", "stale-1", pause)
	if replay.CommandID != first.CommandID || canonical(t, replay.Data) != canonical(t, first.Data) {
		t.Fatalf("replay %s %s, original %s %s", replay.CommandID, replay.Data, first.CommandID, first.Data)
	}
}

// TestConcurrentDuplicateSubmissionsSerialize (wire conventions: concurrent
// duplicates serialize): many identical concurrent submissions create one
// principal and all resolve to the one command.
func TestConcurrentDuplicateSubmissionsSerialize(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	const callers = 8
	results := make([]contract.Result, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = f.invoke(f.owner, "principal.create", "concurrent-1", principalInput(f, "concurrent-agent"))
		}(i)
	}
	wg.Wait()
	commands := map[contract.ID]bool{}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		commands[results[i].CommandID] = true
	}
	if len(commands) != 1 {
		t.Fatalf("concurrent duplicates resolved to %d commands, want one: %v", len(commands), commands)
	}
	if n := f.count("principal.list", map[string]any{"scope": f.scope()}); n != 2 {
		t.Fatalf("concurrent duplicates left %d principals, want the owner and one agent", n)
	}
}
