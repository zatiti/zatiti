package accounting

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Effective limits: merging, defaults, intersection and the reservation
// order assembled from the configuration snapshot.

func TestMergeLimitsZeroIsUnsetOrRealZero(t *testing.T) {
	t.Parallel()
	t.Run("positive caps narrow", func(t *testing.T) {
		t.Parallel()
		var c levelCaps
		if err := c.mergeLimits(&wireLimits{Currency: "USD", SpendMicroUnits: 100, Concurrency: 4, ModelSteps: 50, AttemptSeconds: 600}); err != nil {
			t.Fatalf("mergeLimits: %v", err)
		}
		if c.currency != "USD" {
			t.Fatalf("currency %q, want usd", c.currency)
		}
		if c.spend == nil || *c.spend != 100 {
			t.Fatalf("spend cap %v, want 100", c.spend)
		}
		if c.concurrency == nil || *c.concurrency != 4 {
			t.Fatalf("concurrency cap %v, want 4", c.concurrency)
		}
	})
	t.Run("zero spend is a real zero", func(t *testing.T) {
		t.Parallel()
		var c levelCaps
		if err := c.mergeLimits(&wireLimits{Currency: unconfiguredCurrency, SpendMicroUnits: 0}); err != nil {
			t.Fatalf("mergeLimits: %v", err)
		}
		if c.spend == nil || *c.spend != 0 {
			t.Fatalf("spend cap %v, want present zero", c.spend)
		}
	})
	t.Run("zero concurrency is unset", func(t *testing.T) {
		t.Parallel()
		var c levelCaps
		if err := c.mergeLimits(&wireLimits{Currency: unconfiguredCurrency, Concurrency: 0}); err != nil {
			t.Fatalf("mergeLimits: %v", err)
		}
		if c.concurrency != nil {
			t.Fatalf("concurrency cap %v, want nil", c.concurrency)
		}
	})
	t.Run("currency conflict is a caller failure", func(t *testing.T) {
		t.Parallel()
		var c levelCaps
		if err := c.mergeLimits(&wireLimits{Currency: "USD"}); err != nil {
			t.Fatalf("mergeLimits: %v", err)
		}
		if err := c.mergeLimits(&wireLimits{Currency: "EUR"}); faultCode(err) != contract.CodeInvalidInput {
			t.Fatalf("conflicting currency fault %q, want invalid_input", faultCode(err))
		}
	})
	t.Run("XXX currency never conflicts", func(t *testing.T) {
		t.Parallel()
		var c levelCaps
		if err := c.mergeLimits(&wireLimits{Currency: "USD"}); err != nil {
			t.Fatalf("mergeLimits: %v", err)
		}
		if err := c.mergeLimits(&wireLimits{Currency: unconfiguredCurrency}); err != nil {
			t.Fatalf("mergeLimits with XXX: %v", err)
		}
	})
	t.Run("nil limits change nothing", func(t *testing.T) {
		t.Parallel()
		var c levelCaps
		if err := c.mergeLimits(nil); err != nil {
			t.Fatalf("mergeLimits(nil): %v", err)
		}
		if c.spend != nil || c.currency != "" {
			t.Fatalf("nil limits changed caps: %+v", c)
		}
	})
	t.Run("earlier deadline wins", func(t *testing.T) {
		t.Parallel()
		var c levelCaps
		late := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
		early := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
		if err := c.mergeLimits(&wireLimits{Currency: unconfiguredCurrency, RootDeadline: late}); err != nil {
			t.Fatalf("mergeLimits: %v", err)
		}
		if err := c.mergeLimits(&wireLimits{Currency: unconfiguredCurrency, RootDeadline: early}); err != nil {
			t.Fatalf("mergeLimits: %v", err)
		}
		if c.rootDeadline == nil || !c.rootDeadline.Equal(early) {
			t.Fatalf("deadline %v, want %v", c.rootDeadline, early)
		}
	})
}

func TestFillWorkerDefaults(t *testing.T) {
	t.Parallel()
	var c levelCaps
	fillWorkerDefaults(&c)
	if c.concurrency == nil || *c.concurrency != defaultWorkerConcurrency {
		t.Fatalf("worker concurrency %v, want %d", c.concurrency, defaultWorkerConcurrency)
	}
	if c.modelSteps == nil || *c.modelSteps != defaultWorkerModelSteps {
		t.Fatalf("worker model steps %v, want %d", c.modelSteps, defaultWorkerModelSteps)
	}
	if c.attemptSeconds == nil || *c.attemptSeconds != defaultWorkerAttemptSeconds {
		t.Fatalf("worker attempt seconds %v, want %d", c.attemptSeconds, defaultWorkerAttemptSeconds)
	}
	// Explicit caps are never tightened by the defaults.
	explicit := levelCaps{concurrency: ptr(7)}
	fillWorkerDefaults(&explicit)
	if *explicit.concurrency != 7 {
		t.Fatalf("explicit worker concurrency %d, want 7", *explicit.concurrency)
	}
}

func TestFillRootDefaults(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	var c levelCaps
	fillRootDefaults(&c, now)
	if c.childCount == nil || *c.childCount != defaultRootChildCount {
		t.Fatalf("root child count %v, want %d", c.childCount, defaultRootChildCount)
	}
	if c.delegationDepth == nil || *c.delegationDepth != defaultRootDelegationDepth {
		t.Fatalf("root delegation depth %v, want %d", c.delegationDepth, defaultRootDelegationDepth)
	}
	if c.rootDeadline == nil || !c.rootDeadline.Equal(now.Add(defaultRootDeadline)) {
		t.Fatalf("root deadline %v, want %v", c.rootDeadline, now.Add(defaultRootDeadline))
	}
	// A zero declared spend stays a real zero; it is not defaulted.
	declared := levelCaps{spend: ptr(0)}
	fillRootDefaults(&declared, now)
	if declared.spend == nil || *declared.spend != 0 {
		t.Fatalf("declared zero spend %v, want present zero", declared.spend)
	}
}

func TestEffectiveLimits(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	t.Run("empty chain yields the shipped defaults", func(t *testing.T) {
		t.Parallel()
		out, err := effectiveLimits(nil, time.Time{})
		if err != nil {
			t.Fatalf("effectiveLimits: %v", err)
		}
		if out.Currency != unconfiguredCurrency || out.SpendMicroUnits != 0 {
			t.Fatalf("currency/spend %q/%d, want XXX/0", out.Currency, out.SpendMicroUnits)
		}
		if out.Concurrency != defaultInstallationConcurrency {
			t.Fatalf("concurrency %d, want %d", out.Concurrency, defaultInstallationConcurrency)
		}
		if out.ModelSteps != defaultWorkerModelSteps || out.AttemptSeconds != defaultWorkerAttemptSeconds {
			t.Fatalf("worker steps/seconds %d/%d, want defaults", out.ModelSteps, out.AttemptSeconds)
		}
		if out.ChildCount != defaultRootChildCount || out.DelegationDepth != defaultRootDelegationDepth {
			t.Fatalf("root children/depth %d/%d, want defaults", out.ChildCount, out.DelegationDepth)
		}
		if !out.RootDeadline.IsZero() {
			t.Fatalf("pure limit view deadline %v, want zero", out.RootDeadline)
		}
	})
	t.Run("reservation deadline falls back to now plus default", func(t *testing.T) {
		t.Parallel()
		out, err := effectiveLimits(nil, now.Add(defaultRootDeadline))
		if err != nil {
			t.Fatalf("effectiveLimits: %v", err)
		}
		if !out.RootDeadline.Equal(now.Add(defaultRootDeadline)) {
			t.Fatalf("deadline %v, want %v", out.RootDeadline, now.Add(defaultRootDeadline))
		}
	})
	t.Run("caps intersect to the minimum", func(t *testing.T) {
		t.Parallel()
		levels := []level{
			{kind: posInstallation, ref: "i", caps: levelCaps{currency: "USD", spend: ptr(1000), concurrency: ptr(8)}},
			{kind: posOrganization, ref: "o", caps: levelCaps{spend: ptr(500), concurrency: ptr(6)}},
			{kind: posWorker, ref: "w", caps: levelCaps{concurrency: ptr(2)}},
		}
		out, err := effectiveLimits(levels, time.Time{})
		if err != nil {
			t.Fatalf("effectiveLimits: %v", err)
		}
		if out.Currency != "USD" || out.SpendMicroUnits != 500 || out.Concurrency != 2 {
			t.Fatalf("intersection = %+v, want usd/500/2", out)
		}
	})
	t.Run("currency conflict across levels fails", func(t *testing.T) {
		t.Parallel()
		levels := []level{
			{kind: posInstallation, ref: "i", caps: levelCaps{currency: "USD"}},
			{kind: posOrganization, ref: "o", caps: levelCaps{currency: "EUR"}},
		}
		_, err := effectiveLimits(levels, time.Time{})
		if faultCode(err) != contract.CodeInvalidInput {
			t.Fatalf("intersection fault %q, want invalid_input", faultCode(err))
		}
	})
}

func ptr(v int64) *int64 { return &v }

func TestBuildLevelsValidatesNamedDimensions(t *testing.T) {
	e := newEnv(t)
	t.Run("named organization without snapshot ancestors fails", func(t *testing.T) {
		scope := wireScope{InstallationID: e.install, OrganizationID: e.ids.New()}
		e.ports.setSnapshot(e.install, &snapshotScope{Revision: 1, Ancestors: []snapshotOrg{}, Bindings: json.RawMessage("[]")})
		_, err := e.levelsFor(scope)
		if faultCode(err) != contract.CodeInvalidInput {
			t.Fatalf("fault %q (err %v), want invalid_input", faultCode(err), err)
		}
	})
	t.Run("named project absent from snapshot fails", func(t *testing.T) {
		scope := wireScope{InstallationID: e.install, ProjectID: e.ids.New()}
		e.ports.setSnapshot(e.install, &snapshotScope{Revision: 1, Ancestors: []snapshotOrg{}, Bindings: json.RawMessage("[]")})
		_, err := e.levelsFor(scope)
		if faultCode(err) != contract.CodeInvalidInput {
			t.Fatalf("fault %q (err %v), want invalid_input", faultCode(err), err)
		}
	})
	t.Run("named worker absent from snapshot fails", func(t *testing.T) {
		scope := wireScope{InstallationID: e.install, WorkerID: e.ids.New()}
		e.ports.setSnapshot(e.install, &snapshotScope{Revision: 1, Ancestors: []snapshotOrg{}, Bindings: json.RawMessage("[]")})
		_, err := e.levelsFor(scope)
		if faultCode(err) != contract.CodeInvalidInput {
			t.Fatalf("fault %q (err %v), want invalid_input", faultCode(err), err)
		}
	})
	t.Run("project outside its organization fails", func(t *testing.T) {
		other := e.ids.New()
		chain := e.makeChain(chainSpec{orgDepth: 1})
		scope := e.chainScope(chain)
		scope.OrganizationID = other
		_, err := e.levelsFor(scope)
		if faultCode(err) != contract.CodeInvalidInput {
			t.Fatalf("fault %q (err %v), want invalid_input", faultCode(err), err)
		}
	})
}

func TestBuildLevelsOrderAndBudgets(t *testing.T) {
	e := newEnv(t)
	chain := e.makeChain(chainSpec{
		orgDepth:      2,
		installBudget: &limitsFixture{currency: "USD", spend: 10_000_000},
		rootOrgBudget: &limitsFixture{currency: "USD", spend: 5_000_000},
	})
	levels, err := e.levelsFor(e.chainScope(chain))
	if err != nil {
		t.Fatalf("levelsFor: %v", err)
	}
	// installation, root org, base org, project, worker; reserve appends the
	// root-task level last.
	if len(levels) != 5 {
		t.Fatalf("levels %d, want 5", len(levels))
	}
	wantOrder := []struct {
		kind positionKind
		ref  contract.ID
	}{
		{posInstallation, e.install},
		{posOrganization, chain.rootOrg},
		{posOrganization, chain.baseOrg},
		{posProject, chain.project},
		{posWorker, chain.worker},
	}
	for i, w := range wantOrder {
		if levels[i].kind != w.kind || levels[i].ref != w.ref {
			t.Fatalf("level %d = %s/%s, want %s/%s", i, levels[i].kind, levels[i].ref, w.kind, w.ref)
		}
	}
	// The installation budget replaces the shipped default spend ceiling, so
	// a large budget does not inherit the default's zero.
	if levels[0].caps.spend == nil || *levels[0].caps.spend != 10_000_000 {
		t.Fatalf("installation spend %v, want 10000000", levels[0].caps.spend)
	}
	if levels[1].caps.spend == nil || *levels[1].caps.spend != 5_000_000 {
		t.Fatalf("root organization spend %v, want 5000000", levels[1].caps.spend)
	}
	// The worker level carries the shipped defaults for its unconfigured
	// caps.
	worker := levels[4]
	if worker.caps.concurrency == nil || *worker.caps.concurrency != defaultWorkerConcurrency {
		t.Fatalf("worker concurrency %v, want %d", worker.caps.concurrency, defaultWorkerConcurrency)
	}
}

func TestBudgetReplacesInstallationDefault(t *testing.T) {
	e := newEnv(t)
	// Without a budget the installation default refuses paid work: spend cap
	// zero. With one, the declared spend governs.
	levels, err := e.levelsFor(wireScope{InstallationID: e.install})
	if err != nil {
		t.Fatalf("levelsFor: %v", err)
	}
	if levels[0].caps.spend == nil || *levels[0].caps.spend != 0 {
		t.Fatalf("default installation spend %v, want present zero", levels[0].caps.spend)
	}
	if levels[0].caps.concurrency == nil || *levels[0].caps.concurrency != defaultInstallationConcurrency {
		t.Fatalf("default installation concurrency %v, want %d", levels[0].caps.concurrency, defaultInstallationConcurrency)
	}
	e.applyBudget(e.install, limits("USD", 7_000_000, 2))
	levels, err = e.levelsFor(wireScope{InstallationID: e.install})
	if err != nil {
		t.Fatalf("levelsFor: %v", err)
	}
	if levels[0].caps.spend == nil || *levels[0].caps.spend != 7_000_000 {
		t.Fatalf("budget installation spend %v, want 7000000", levels[0].caps.spend)
	}
	if levels[0].caps.concurrency == nil || *levels[0].caps.concurrency != 2 {
		t.Fatalf("budget installation concurrency %v, want 2", levels[0].caps.concurrency)
	}
}
