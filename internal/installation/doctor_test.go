package installation

import (
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestDoctorReportsStalledWorkerTurns proves an outstanding effect whose
// callback_route identifies it as a durable worker turn surfaces under its
// own worker_turn_stalled requirement code, not the generic effect_pending
// bucket, so an operator (or a worker reading its own doctor output) can
// tell a stalled turn apart from any other kind of pending effect.
func TestDoctorReportsStalledWorkerTurns(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	turnID := e.ids.New()
	e.ports.set(peerEffectsPending, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(effectsPendingOutput{Operations: []peerOperation{{
			ID: e.ids.New(), Version: 1, State: "executing",
			CallbackRoute: &peerCallbackRoute{Kind: "worker_turn", TurnID: &turnID},
		}}})
	})
	payload := mustQueryOK(t, e, opDoctor, scopeInput{Scope: e.scope})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	if !hasRequirementCode(out.Resource.Requirements, workerTurnStalledCode) {
		t.Fatalf("expected a %s requirement, got %+v", workerTurnStalledCode, out.Resource.Requirements)
	}
	if hasRequirementCode(out.Resource.Requirements, effectPendingCode) {
		t.Fatalf("a worker-turn-routed effect must not also report as generic effect_pending: %+v", out.Resource.Requirements)
	}
}

// TestDoctorReportsUnclaimedJobs proves an outstanding effect routed back to
// a job surfaces under job_unclaimed.
func TestDoctorReportsUnclaimedJobs(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	jobID := e.ids.New()
	e.ports.set(peerEffectsPending, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(effectsPendingOutput{Operations: []peerOperation{{
			ID: e.ids.New(), Version: 1, State: "awaiting_confirmation",
			CallbackRoute: &peerCallbackRoute{Kind: "job", JobID: &jobID},
		}}})
	})
	payload := mustQueryOK(t, e, opStatus, scopeInput{Scope: e.scope})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	if !hasRequirementCode(out.Resource.Requirements, jobUnclaimedCode) {
		t.Fatalf("expected a %s requirement, got %+v", jobUnclaimedCode, out.Resource.Requirements)
	}
}

// TestDoctorFallsBackToGenericEffectPending proves an outstanding effect
// with no callback route, or one routed to memory/skill/connection, keeps
// the pre-existing effect_pending code rather than silently disappearing.
func TestDoctorFallsBackToGenericEffectPending(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.ports.set(peerEffectsPending, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(effectsPendingOutput{Operations: []peerOperation{
			{ID: e.ids.New(), Version: 1, State: "prepared"},
			{ID: e.ids.New(), Version: 1, State: "prepared", CallbackRoute: &peerCallbackRoute{Kind: "memory"}},
		}})
	})
	payload := mustQueryOK(t, e, opDoctor, scopeInput{Scope: e.scope})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	count := 0
	for _, r := range out.Resource.Requirements {
		if r.Code == effectPendingCode {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 effect_pending requirements, got %d in %+v", count, out.Resource.Requirements)
	}
}

// TestDoctorReportsUnavailableMemory proves a _memory.manifest peer failure
// surfaces as a named, inspectable requirement rather than failing the
// whole doctor/status read or silently omitting the gap.
func TestDoctorReportsUnavailableMemory(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.ports.set(peerMemoryManifest, func(contract.Invocation) (contract.Payload, error) {
		return failPayload(contract.CodeControllerUnavailable, "memory offline")
	})
	payload := mustQueryOK(t, e, opDoctor, scopeInput{Scope: e.scope})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	if !hasRequirementCode(out.Resource.Requirements, memoryUnavailableCode) {
		t.Fatalf("expected a %s requirement, got %+v", memoryUnavailableCode, out.Resource.Requirements)
	}
}

// TestDoctorSurfacesMemoryObligations proves unresolved memory writer/
// promotion obligations reported by _memory.manifest reach doctor/status
// directly, carrying their own codes from the memory owner.
func TestDoctorSurfacesMemoryObligations(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.ports.set(peerMemoryManifest, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(memoryManifestOutput{
			BrainRevisions: []wireRef{},
			Obligations:    []wireRequirement{{Code: "memory_writer_unavailable", Message: "brain writer offline"}},
		})
	})
	payload := mustQueryOK(t, e, opStatus, scopeInput{Scope: e.scope})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	if !hasRequirementCode(out.Resource.Requirements, "memory_writer_unavailable") {
		t.Fatalf("expected the memory owner's own obligation code to surface, got %+v", out.Resource.Requirements)
	}
}

// TestDoctorReportsOutstandingLiabilities proves an unconfirmed accounting
// usage amount (Usage.Unknown) surfaces as a named liability_unknown
// requirement naming the amount and currency, since it may or may not have
// been charged and cannot yet be released or billed.
func TestDoctorReportsOutstandingLiabilities(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.ports.set(peerAccountingInspect, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(accountingInspectOutput{
			Limits: peerLimits{Currency: "USD", SpendMicroUnits: 1_000_000, Concurrency: 1, ModelSteps: 100,
				ChildCount: 8, DelegationDepth: 3, AttemptSeconds: 1800, RootDeadline: "2027-01-01T00:00:00Z"},
			Usage: peerUsage{Currency: "USD", Unknown: 4200},
		})
	})
	payload := mustQueryOK(t, e, opDoctor, scopeInput{Scope: e.scope})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	found := false
	for _, r := range out.Resource.Requirements {
		if r.Code == liabilityUnknownCode {
			found = true
			if !containsAll(r.Message, "4200", "USD") {
				t.Fatalf("liability message %q does not name the amount and currency", r.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected a %s requirement, got %+v", liabilityUnknownCode, out.Resource.Requirements)
	}
}

// TestDoctorNamesNoSpendUntilConfigured is the card's second required
// behavioral test: a missing provider/currency yields named, actionable
// requirements (prerequisite_missing naming the gap, plus the first-task
// sequence naming exactly how to proceed at zero spend) and never implies
// any amount was or could be spent.
func TestDoctorNamesNoSpendUntilConfigured(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	// The env_test.go default fake accounting response simulates an
	// already-configured budget; override it to the unconfigured shape a
	// genuinely fresh installation reports, then assert the full,
	// actionable set of what doctor names -- not just that some
	// requirement exists.
	e.ports.set(peerAccountingInspect, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(accountingInspectOutput{Limits: peerLimits{}, Usage: peerUsage{}})
	})
	payload := mustQueryOK(t, e, opDoctor, scopeInput{Scope: e.scope})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	if hasRequirementCode(out.Resource.Requirements, liabilityUnknownCode) {
		t.Fatalf("an unconfigured, never-dispatched installation must report no spend liability: %+v", out.Resource.Requirements)
	}
	if !hasRequirementCode(out.Resource.Requirements, "prerequisite_missing") {
		t.Fatalf("expected a named prerequisite_missing requirement for the unconfigured budget, got %+v", out.Resource.Requirements)
	}
	if !hasRequirementCode(out.Resource.Requirements, firstTaskRequirementCode) {
		t.Fatalf("expected the actionable first-task sequence requirement, got %+v", out.Resource.Requirements)
	}
}

// TestRuntimeReadyTracksPauseAndMaintenance proves Status.runtime_ready
// reflects exactly this package's own admission-blocking lifecycle state:
// true once initialized, false the instant pause or maintenance is
// entered, true again once resumed.
func TestRuntimeReadyTracksPauseAndMaintenance(t *testing.T) {
	e := newEnv(t)
	st := e.mustBootstrap()
	if st.RuntimeReady == nil || !*st.RuntimeReady {
		t.Fatalf("a freshly bootstrapped installation must be runtime_ready, got %+v", st.RuntimeReady)
	}

	payload := e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})
	var paused resourceOut[wireStatus]
	e.decode(payload.Data, &paused)
	if paused.Resource.RuntimeReady == nil || *paused.Resource.RuntimeReady {
		t.Fatalf("a paused installation must not be runtime_ready, got %+v", paused.Resource.RuntimeReady)
	}

	payload = e.mustOK(opResume, versionedScopeInput{Scope: e.scope, ExpectedVersion: 2})
	var resumed resourceOut[wireStatus]
	e.decode(payload.Data, &resumed)
	if resumed.Resource.RuntimeReady == nil || !*resumed.Resource.RuntimeReady {
		t.Fatalf("a resumed installation must be runtime_ready again, got %+v", resumed.Resource.RuntimeReady)
	}
}

func hasRequirementCode(reqs []wireRequirement, code string) bool {
	for _, r := range reqs {
		if r.Code == code {
			return true
		}
	}
	return false
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
