package accounting

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Budget definition operations: validate reports diagnostics without live
// changes, activate is the commitment point inside the caller's transaction,
// budget.get returns the intersected effective limits, and budget.propose
// stages an update through configuration under old authority. The wire
// schema already rejects unknown change kinds and off-schema definitions,
// so those two diagnostics are proven against the validator directly.

// candidate builds an activate or validate envelope for one change list.
func candidate(changes ...wireChange) candidateEnvelope {
	return candidateEnvelope{Candidate: wireCandidate{
		PlanID: contract.ID("00000000-0000-4000-8000-000000000099"), BaseRevision: 1,
		CandidateDigest: digest64, Dependencies: []wireRef{}, Changes: changes,
	}}
}

// rawLimits canonicalizes limits into a definition document.
func rawLimits(currency string, spend, concurrency int64) json.RawMessage {
	raw, err := canonicalJSON(limits(currency, spend, concurrency))
	if err != nil {
		panic(err)
	}
	return raw
}

func TestValidateChangeDiagnostics(t *testing.T) {
	t.Parallel()
	target := contract.ID("00000000-0000-4000-8000-0000000000aa")
	cases := []struct {
		name     string
		change   wireChange
		budgeted bool
		wantCode string
		wantPath string
	}{
		{"unconfigured currency with spend", budgetChange(changeActionCreate, target, 0, limits(unconfiguredCurrency, 1_000, 2)), false, "unconfigured_currency", "/changes/0/definition"},
		{"create with expected version", budgetChange(changeActionCreate, target, 3, limits("USD", 1_000, 2)), false, "create_expected_version", "/changes/0"},
		{"create of existing budget", budgetChange(changeActionCreate, target, 0, limits("USD", 1_000, 2)), true, "already_exists", "/changes/0"},
		{"update of missing budget", budgetChange(changeActionUpdate, target, 1, limits("USD", 1_000, 2)), false, "missing", "/changes/0"},
		{"stale version", budgetChange(changeActionUpdate, target, 99, limits("USD", 1_000, 2)), true, "stale_version", "/changes/0"},
		{"delete action is unsupported", budgetChange("delete", target, 1, limits("USD", 1_000, 2)), true, "unsupported_action", "/changes/0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if tc.budgeted {
				e.applyBudget(target, limits("USD", 500, 2))
			}
			payload := e.mustOK(opValidate, candidate(tc.change))
			var out validationResourceBody
			e.decode(payload.Data, &out)
			if len(out.Resource.Diagnostics) != 1 {
				t.Fatalf("diagnostics %+v, want exactly one", out.Resource.Diagnostics)
			}
			d := out.Resource.Diagnostics[0]
			if d.Code != tc.wantCode || d.Path != tc.wantPath {
				t.Fatalf("diagnostic %s at %s (%s), want %s at %s", d.Code, d.Path, d.Message, tc.wantCode, tc.wantPath)
			}
			if d.Severity != "error" {
				t.Fatalf("severity %q, want error", d.Severity)
			}
		})
	}

	// Unknown kinds and off-schema definitions never pass the wire schema,
	// so the validator is exercised directly for those two diagnostics.
	t.Run("unknown kind and invalid definition", func(t *testing.T) {
		e := newEnv(t)
		if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
			kind := e.svc.validateBudgetChange(e.ctx, unit, wireChange{
				Kind: "task", Action: "create", ID: target, ExpectedVersion: 0, Definition: rawLimits("USD", 1, 2),
			}, "/changes/0")
			if kind == nil || kind.Code != "unsupported_kind" {
				t.Fatalf("unknown kind diagnostic %+v, want unsupported_kind", kind)
			}
			def := e.svc.validateBudgetChange(e.ctx, unit, wireChange{
				Kind: changeKindBudget, Action: "create", ID: target, ExpectedVersion: 0,
				Definition: json.RawMessage(`{"currency":"USD","spend_micro_units":1000}`),
			}, "/changes/0")
			if def == nil || def.Code != "invalid_definition" || def.Path != "/changes/0/definition" {
				t.Fatalf("invalid definition diagnostic %+v, want invalid_definition at the definition path", def)
			}
			return nil
		}); err != nil {
			t.Fatalf("direct validation: %v", err)
		}
	})

	t.Run("an applicable change reports no diagnostics", func(t *testing.T) {
		e := newEnv(t)
		payload := e.mustOK(opValidate, candidate(budgetChange(changeActionCreate, e.install, 0, limits("USD", 1_000, 2))))
		var out validationResourceBody
		e.decode(payload.Data, &out)
		if len(out.Resource.Diagnostics) != 0 {
			t.Fatalf("applicable change reported %+v, want none", out.Resource.Diagnostics)
		}
	})
	t.Run("paths index each change", func(t *testing.T) {
		e := newEnv(t)
		payload := e.mustOK(opValidate, candidate(
			budgetChange(changeActionCreate, e.ids.New(), 0, limits(unconfiguredCurrency, 1_000, 2)),
			budgetChange(changeActionUpdate, e.ids.New(), 1, limits("USD", 1_000, 2)),
		))
		var out validationResourceBody
		e.decode(payload.Data, &out)
		if len(out.Resource.Diagnostics) != 2 {
			t.Fatalf("diagnostics %+v, want two", out.Resource.Diagnostics)
		}
		if out.Resource.Diagnostics[0].Path != "/changes/0/definition" || out.Resource.Diagnostics[1].Path != "/changes/1" {
			t.Fatalf("paths %q and %q, want indexed changes", out.Resource.Diagnostics[0].Path, out.Resource.Diagnostics[1].Path)
		}
	})
}

func TestActivateLifecycle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// The lifecycle governs the installation budget itself, so budget.get
	// observes every transition through the installation scope.
	target := e.install

	t.Run("create assigns version one and emits the event", func(t *testing.T) {
		payload := e.mustOK(opActivate, candidate(budgetChange(changeActionCreate, target, 0, limits("USD", 1_000_000, 4))))
		var out versionsOutput
		e.decode(payload.Data, &out)
		if len(out.Versions) != 1 || out.Versions[0].ID != target || out.Versions[0].Version != 1 {
			t.Fatalf("activate versions %+v, want the target at version 1", out.Versions)
		}
		events := e.readEvents()
		if len(events) != 1 || events[0].Kind != eventBudgetActivated || events[0].ResourceID != string(target) || events[0].ResourceVersion != 1 {
			t.Fatalf("events %+v, want one activation for the target", events)
		}
	})
	t.Run("create of an existing budget conflicts", func(t *testing.T) {
		_ = e.expectFault(opActivate, candidate(budgetChange(changeActionCreate, target, 0, limits("USD", 1_000, 2))), contract.CodeConflict)
	})
	t.Run("update moves the limits under the version fence", func(t *testing.T) {
		payload := e.mustOK(opActivate, candidate(budgetChange(changeActionUpdate, target, 1, limits("USD", 2_000_000, 6))))
		var out versionsOutput
		e.decode(payload.Data, &out)
		if out.Versions[0].Version != 2 {
			t.Fatalf("update version %d, want 2", out.Versions[0].Version)
		}
		payload = e.mustOK(opBudgetGet, scopeInput{Scope: e.scope})
		var limits limitsOutput
		e.decode(payload.Data, &limits)
		if limits.Limits.SpendMicroUnits != 2_000_000 || limits.Limits.Concurrency != 6 || limits.Limits.Currency != "USD" {
			t.Fatalf("budget.get after update %+v, want usd/2000000/6", limits.Limits)
		}
	})
	t.Run("archive retires the budget", func(t *testing.T) {
		payload := e.mustOK(opActivate, candidate(budgetChange(changeActionArchive, target, 2, limits("USD", 2_000_000, 6))))
		var out versionsOutput
		e.decode(payload.Data, &out)
		if out.Versions[0].Version != 3 {
			t.Fatalf("archive version %d, want 3", out.Versions[0].Version)
		}
		// With no active budget the installation falls back to its shipped
		// defaults and paid admission is refused again.
		payload = e.mustOK(opBudgetGet, scopeInput{Scope: e.scope})
		var limits limitsOutput
		e.decode(payload.Data, &limits)
		if limits.Limits.SpendMicroUnits != 0 || limits.Limits.Currency != unconfiguredCurrency {
			t.Fatalf("budget.get after archive %+v, want the XXX default", limits.Limits)
		}
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 100), contract.CodeBudgetUnavailable)
	})
	t.Run("update of an archived budget is not_found", func(t *testing.T) {
		_ = e.expectFault(opActivate, candidate(budgetChange(changeActionUpdate, target, 3, limits("USD", 1, 2))), contract.CodeNotFound)
	})
	t.Run("re-activation revives the identity with a fresh version", func(t *testing.T) {
		payload := e.mustOK(opActivate, candidate(budgetChange(changeActionCreate, target, 0, limits("USD", 3_000_000, 2))))
		var out versionsOutput
		e.decode(payload.Data, &out)
		if out.Versions[0].Version != 4 {
			t.Fatalf("re-activation version %d, want 4 (archived version plus one)", out.Versions[0].Version)
		}
		res := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 100))
		if res.State != "reserved" {
			t.Fatalf("state %q, want reserved under the revived budget", res.State)
		}
	})
	t.Run("every transition emitted its event", func(t *testing.T) {
		// Four activations plus the reservation event of the revived budget.
		events := e.readEvents()
		if len(events) != 5 {
			t.Fatalf("events %d, want 5", len(events))
		}
		wantActions := []string{changeActionCreate, changeActionUpdate, changeActionArchive, changeActionCreate}
		for i, action := range wantActions {
			if events[i].Kind != eventBudgetActivated {
				t.Fatalf("event %d kind %q, want %s", i, events[i].Kind, eventBudgetActivated)
			}
			var data map[string]string
			if err := json.Unmarshal([]byte(events[i].Data), &data); err != nil || data["action"] != action {
				t.Fatalf("event %d data %s, want action %q", i, events[i].Data, action)
			}
		}
	})
}

func TestActivateRollsBackOnFailure(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// The first change applies, the second is inapplicable; the whole
	// candidate must roll back with the caller's transaction.
	_ = e.expectFault(opActivate, candidate(
		budgetChange(changeActionCreate, e.install, 0, limits("USD", 1_000_000, 4)),
		budgetChange("delete", e.ids.New(), 1, limits("USD", 1, 2)),
	), contract.CodeInvalidInput)
	payload := e.mustOK(opBudgetGet, scopeInput{Scope: e.scope})
	var limits limitsOutput
	e.decode(payload.Data, &limits)
	if limits.Limits.SpendMicroUnits != 0 || limits.Limits.Currency != unconfiguredCurrency {
		t.Fatalf("failed activation left limits %+v, want the untouched default", limits.Limits)
	}
}

func TestBudgetGetDefaultsAndIntersection(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	t.Run("the unconfigured installation shows its shipped defaults", func(t *testing.T) {
		payload := e.mustOK(opBudgetGet, scopeInput{Scope: e.scope})
		var out limitsOutput
		e.decode(payload.Data, &out)
		if out.Limits.Currency != unconfiguredCurrency || out.Limits.SpendMicroUnits != 0 {
			t.Fatalf("default limits %+v, want XXX/0", out.Limits)
		}
		if out.Limits.Concurrency != defaultInstallationConcurrency || out.Limits.ModelSteps != defaultWorkerModelSteps {
			t.Fatalf("default caps %+v, want shipped defaults", out.Limits)
		}
	})
	t.Run("a budget replaces the default", func(t *testing.T) {
		e.applyBudget(e.install, limits("USD", 7_000_000, 2))
		payload := e.mustOK(opBudgetGet, scopeInput{Scope: e.scope})
		var out limitsOutput
		e.decode(payload.Data, &out)
		if out.Limits.Currency != "USD" || out.Limits.SpendMicroUnits != 7_000_000 || out.Limits.Concurrency != 2 {
			t.Fatalf("budget limits %+v, want usd/7000000/2", out.Limits)
		}
	})
	t.Run("chain budgets intersect to the minimum", func(t *testing.T) {
		// A fresh environment: the installation budget of the parent subtests
		// would collide with this chain's own installation budget.
		sub := newEnv(t)
		chain := sub.makeChain(chainSpec{
			orgDepth:      2,
			installBudget: &limitsFixture{currency: "USD", spend: 10_000_000, concurrency: 8},
			rootOrgBudget: &limitsFixture{currency: "USD", spend: 5_000_000, concurrency: 6},
			// The worker budget keeps the shipped one-attempt worker default
			// from narrowing the intersection below the org ceiling.
			workerBudget: &limitsFixture{currency: "USD", spend: 50_000_000, concurrency: 16},
		})
		payload := sub.mustOK(opBudgetGet, scopeInput{Scope: sub.chainScope(chain)})
		var out limitsOutput
		sub.decode(payload.Data, &out)
		if out.Limits.SpendMicroUnits != 5_000_000 || out.Limits.Concurrency != 6 || out.Limits.Currency != "USD" {
			t.Fatalf("intersected limits %+v, want usd/5000000/6", out.Limits)
		}
	})
	t.Run("another installation is denied", func(t *testing.T) {
		_ = e.expectFault(opBudgetGet, scopeInput{Scope: wireScope{InstallationID: e.ids.New()}}, contract.CodePermissionDenied)
	})
}

func TestBudgetPropose(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.applyBudget(e.install, limits("USD", 1_000_000, 4))
	e.ports.stageBody = func(in stageInput) draftResourceBody {
		return draftResourceBody{Resource: wireDraft{
			ID: e.ids.New(), Version: 1, BaseRevision: 1,
			Changes: []json.RawMessage{in.Change}, Diagnostics: []wireDiagnostic{},
		}}
	}

	t.Run("unconfigured currency with spend is invalid_input", func(t *testing.T) {
		_ = e.expectFault(opBudgetPropose, budgetProposeInput{
			Scope: e.scope, ExpectedVersion: 1, Limits: rawLimits(unconfiguredCurrency, 1_000, 2),
		}, contract.CodeInvalidInput)
	})
	t.Run("a missing budget is not_found", func(t *testing.T) {
		scoped := e.scope
		scoped.WorkerID = e.ids.New()
		_ = e.expectFault(opBudgetPropose, budgetProposeInput{
			Scope: scoped, ExpectedVersion: 1, Limits: rawLimits("USD", 1_000, 2),
		}, contract.CodeNotFound)
	})
	t.Run("a stale version is refused", func(t *testing.T) {
		_ = e.expectFault(opBudgetPropose, e.proposeIn(99, 2_000_000, 4), contract.CodeStaleVersion)
	})
	t.Run("staging records the exact budget change and returns the draft", func(t *testing.T) {
		draftID := e.ids.New()
		payload := e.mustOK(opBudgetPropose, budgetProposeInput{
			Scope: e.scope, ExpectedVersion: 1, DraftID: &draftID, Limits: rawLimits("USD", 2_000_000, 6),
		})
		var out draftResourceBody
		e.decode(payload.Data, &out)
		if len(out.Resource.Changes) != 1 {
			t.Fatalf("draft changes %d, want the staged change", len(out.Resource.Changes))
		}
		stages := e.ports.stagesOf()
		if len(stages) != 1 {
			t.Fatalf("stage calls %d, want 1", len(stages))
		}
		st := stages[0]
		if st.Scope.InstallationID != e.install || st.DraftID == nil || *st.DraftID != draftID {
			t.Fatalf("stage input %+v, want the installation scope and draft id", st)
		}
		var change wireChange
		if err := contract.DecodeStrict(st.Change, &change); err != nil {
			t.Fatalf("staged change decode: %v", err)
		}
		if change.Kind != changeKindBudget || change.Action != changeActionUpdate || change.ID != e.install || change.ExpectedVersion != 1 {
			t.Fatalf("staged change %+v, want a budget update of the installation at version 1", change)
		}
		var staged wireLimits
		if err := contract.DecodeStrict(change.Definition, &staged); err != nil {
			t.Fatalf("staged definition decode: %v", err)
		}
		if staged.Currency != "USD" || staged.SpendMicroUnits != 2_000_000 || staged.Concurrency != 6 {
			t.Fatalf("staged limits %+v, want usd/2000000/6", staged)
		}
	})
	t.Run("the most specific named dimension is the target", func(t *testing.T) {
		chain := e.makeChain(chainSpec{
			orgDepth:     1,
			workerBudget: &limitsFixture{currency: "USD", spend: 500_000},
		})
		scope := e.chainScope(chain)
		payload := e.mustOK(opBudgetPropose, budgetProposeInput{
			Scope: scope, ExpectedVersion: 1, Limits: rawLimits("USD", 600_000, 2),
		})
		var out draftResourceBody
		e.decode(payload.Data, &out)
		if out.Resource.ID == "" {
			t.Fatalf("staged draft missing")
		}
		change := e.ports.stagesOf()[1]
		var parsed wireChange
		if err := contract.DecodeStrict(change.Change, &parsed); err != nil {
			t.Fatalf("staged change decode: %v", err)
		}
		if parsed.ID != chain.worker {
			t.Fatalf("staged change target %s, want the worker %s", parsed.ID, chain.worker)
		}
	})
	t.Run("another installation is denied", func(t *testing.T) {
		in := e.proposeIn(1, 2_000_000, 4)
		in.Scope = wireScope{InstallationID: e.ids.New()}
		_ = e.expectFault(opBudgetPropose, in, contract.CodePermissionDenied)
	})
}

// proposeIn builds a propose input against the installation budget.
func (e *testEnv) proposeIn(expectedVersion, spend, concurrency int64) budgetProposeInput {
	return budgetProposeInput{
		Scope: e.scope, ExpectedVersion: expectedVersion, Limits: rawLimits("USD", spend, concurrency),
	}
}
