package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// fakeAdapter is the minimal contract.Adapter test double: only Name is
// ever inspected by assemblyReadiness/hasRegisteredModelAdapter (they key
// off the map, not the value), so the other methods are unreachable stubs.
type fakeAdapter struct{ name string }

func (f fakeAdapter) Name() string              { return f.name }
func (f fakeAdapter) Contract() json.RawMessage { return json.RawMessage(`{}`) }
func (f fakeAdapter) Invoke(context.Context, contract.Dispatch) (contract.Observation, error) {
	return contract.Observation{}, nil
}
func (f fakeAdapter) Reconcile(context.Context, contract.Dispatch) (contract.Observation, error) {
	return contract.Observation{}, nil
}

func hasCategory(reqs []startupRequirement, category string) bool {
	for _, r := range reqs {
		for _, c := range r.Categories {
			if c == category {
				return true
			}
		}
	}
	return false
}

// TestAssemblyReadinessStorageOnlyWithNoModelAdapter proves P24 item 5's
// weakest state: no responses adapter registered classifies as
// storage_only (never chat/task ready) and reports the gap under
// provider/price/currency -- the three aspects a missing profile leaves
// simultaneously unknown (readiness.go's own reasoning: cmd/zatiti never
// independently validates a price/currency no adapter construction ever
// checked).
func TestAssemblyReadinessStorageOnlyWithNoModelAdapter(t *testing.T) {
	level, reqs := assemblyReadiness(map[string]contract.Adapter{}, []string{"responses"}, nil, true, true)
	if level != readinessStorageOnly {
		t.Fatalf("level = %s, want %s", level, readinessStorageOnly)
	}
	for _, want := range []string{"provider", "price", "currency"} {
		if !hasCategory(reqs, want) {
			t.Fatalf("requirements %+v lack category %q for a missing responses adapter", reqs, want)
		}
	}
}

// TestAssemblyReadinessChatReadyWithVerifierOrRunnerMissing proves a
// registered model adapter alone reaches chat_ready but never task_ready
// while the verifier or a catalog job runner is still missing -- a model
// turn can start, but task automation cannot be trusted to finish.
func TestAssemblyReadinessChatReadyWithVerifierOrRunnerMissing(t *testing.T) {
	adapters := map[string]contract.Adapter{"responses": fakeAdapter{name: "responses"}}

	level, reqs := assemblyReadiness(adapters, nil, []jobKind{{Owner: "artifacts", Operation: "artifact.export"}}, true, true)
	if level != readinessChatReady {
		t.Fatalf("with a missing job runner: level = %s, want %s", level, readinessChatReady)
	}
	if !hasCategory(reqs, "runner") {
		t.Fatalf("missing job runner did not produce a runner requirement: %+v", reqs)
	}

	level, reqs = assemblyReadiness(adapters, nil, nil, false, true)
	if level != readinessChatReady {
		t.Fatalf("with no verifier attached: level = %s, want %s", level, readinessChatReady)
	}
	if !hasCategory(reqs, "runner") {
		t.Fatalf("missing verifier did not produce a requirement: %+v", reqs)
	}
}

// TestAssemblyReadinessTaskReady proves the strongest state requires the
// model adapter registered, every catalog job kind attached and the
// verifier attached, together -- never any single one alone.
func TestAssemblyReadinessTaskReady(t *testing.T) {
	adapters := map[string]contract.Adapter{"responses": fakeAdapter{name: "responses"}}
	level, reqs := assemblyReadiness(adapters, nil, nil, true, true)
	if level != readinessTaskReady {
		t.Fatalf("level = %s, want %s (reqs: %+v)", level, readinessTaskReady, reqs)
	}
	if hasCategory(reqs, "runner") {
		t.Fatalf("task_ready still reported a runner requirement: %+v", reqs)
	}
}

// TestAssemblyReadinessMissingToolAdapterNeverDemotesTaskReady proves a
// missing NON-model "tools" adapter (github/httpread/serenity) is still
// reported as a requirement but does not by itself block task_ready: a
// task that never dispatches to an unconfigured tool adapter completes
// correctly without it, unlike an unattached verifier or job runner.
func TestAssemblyReadinessMissingToolAdapterNeverDemotesTaskReady(t *testing.T) {
	adapters := map[string]contract.Adapter{"responses": fakeAdapter{name: "responses"}}
	level, reqs := assemblyReadiness(adapters, []string{"github"}, nil, true, true)
	if level != readinessTaskReady {
		t.Fatalf("level = %s, want %s even with a missing tools adapter", level, readinessTaskReady)
	}
	if !hasCategory(reqs, "tools") {
		t.Fatalf("missing github adapter did not produce a tools requirement: %+v", reqs)
	}
	if hasCategory(reqs, "provider") {
		t.Fatalf("a missing NON-model adapter must not be reported as a provider gap: %+v", reqs)
	}
}

// TestAssemblyReadinessHelperGapIsReportedNeverSilent proves P24 item 4:
// an unprovisioned connection-setup helper key is exposed as an
// inspectable "helper" requirement, never a silent gap a caller only
// discovers when connection.setup.complete unexpectedly fails.
func TestAssemblyReadinessHelperGapIsReportedNeverSilent(t *testing.T) {
	_, reqs := assemblyReadiness(map[string]contract.Adapter{}, nil, nil, true, false)
	if !hasCategory(reqs, "helper") {
		t.Fatalf("an unprovisioned helper key produced no helper requirement: %+v", reqs)
	}
	var msg string
	for _, r := range reqs {
		if hasCategoryOne(r, "helper") {
			msg = r.Message
		}
	}
	if !strings.Contains(msg, "connection helper") {
		t.Fatalf("helper requirement message %q does not point the operator at the remediation command", msg)
	}
}

func hasCategoryOne(r startupRequirement, category string) bool {
	for _, c := range r.Categories {
		if c == category {
			return true
		}
	}
	return false
}
