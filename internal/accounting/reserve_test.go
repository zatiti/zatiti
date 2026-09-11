package accounting

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Reservations: input validation, paid-execution gating, replay, per-level
// capacity and the atomicity of concurrent admissions against shared
// positions.

func TestReserveInputValidation(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	t.Run("installation mismatch fails", func(t *testing.T) {
		in := e.reserveIn(wireScope{InstallationID: e.ids.New()}, e.ids.New(), unconfiguredCurrency, 0)
		_ = e.expectFault(opReserve, in, contract.CodePermissionDenied)
	})
	t.Run("negative amount fails", func(t *testing.T) {
		in := e.reserveIn(e.scope, e.ids.New(), unconfiguredCurrency, -5)
		_ = e.expectFault(opReserve, in, contract.CodeInvalidInput)
	})
}

func TestReserveUnpaidAdmission(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	t.Run("zero amount with XXX currency succeeds", func(t *testing.T) {
		op := e.ids.New()
		res := e.mustReserve(e.reserveIn(e.scope, op, unconfiguredCurrency, 0))
		if res.State != "reserved" || res.Version != 1 {
			t.Fatalf("reservation state %q version %d, want reserved/1", res.State, res.Version)
		}
		if res.OperationID != op || res.Amount.MicroUnits != 0 {
			t.Fatalf("reservation %+v mismatches the request", res)
		}
	})
	t.Run("position is created with the reservation", func(t *testing.T) {
		p, ok := e.readPosition(posInstallation, e.install)
		if !ok {
			t.Fatalf("installation position missing")
		}
		if p.Reserved != 0 || p.Concurrency != 1 || p.Currency != unconfiguredCurrency {
			t.Fatalf("installation position %+v, want zero reserved, one slot, XXX currency", p)
		}
	})
	t.Run("reserved ledger entry is written", func(t *testing.T) {
		op := e.ids.New()
		res := e.mustReserve(e.reserveIn(e.scope, op, unconfiguredCurrency, 0))
		entries := e.readEntries(res.ID)
		if len(entries) != 1 || entries[0].Kind != entryReserved {
			t.Fatalf("entries %+v, want one reserved entry", entries)
		}
	})
}

func TestReservePaidGating(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	budgetVersion := int64(0)
	t.Run("positive amount with XXX currency is budget_unavailable", func(t *testing.T) {
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), unconfiguredCurrency, 1000), contract.CodeBudgetUnavailable)
	})
	t.Run("positive amount without a budget is budget_unavailable", func(t *testing.T) {
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 1000), contract.CodeBudgetUnavailable)
	})
	t.Run("budget without finite spend refuses paid work", func(t *testing.T) {
		budgetVersion = e.applyBudget(e.install, limits("USD", 0, 4))
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 1000), contract.CodeBudgetUnavailable)
	})
	t.Run("configured budget admits paid work", func(t *testing.T) {
		// A budget already exists with a zero spend ceiling; the finite
		// configuration stages as an update under the version fence.
		version := e.applyBudgetUpdate(e.install, budgetVersion, limits("USD", 1_000_000, 4))
		if version != budgetVersion+1 {
			t.Fatalf("update version %d, want %d", version, budgetVersion+1)
		}
		res := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1000))
		if res.Amount.Currency != "USD" || res.Amount.MicroUnits != 1000 {
			t.Fatalf("reservation amount %+v, want usd/1000", res.Amount)
		}
		p, ok := e.readPosition(posInstallation, e.install)
		if !ok || p.Reserved != 1000 {
			t.Fatalf("installation position %+v, want 1000 reserved", p)
		}
	})
	t.Run("currency mismatch against configured budgets fails", func(t *testing.T) {
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "EUR", 1000), contract.CodeInvalidInput)
	})
}

func TestReserveReplay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.applyBudget(e.install, limits("USD", 1_000_000, 4))
	op := e.ids.New()
	first := e.mustReserve(e.reserveIn(e.scope, op, "USD", 500))

	t.Run("identical replay returns the same reservation", func(t *testing.T) {
		second := e.mustReserve(e.reserveIn(e.scope, op, "USD", 500))
		if second.ID != first.ID || second.Version != first.Version {
			t.Fatalf("replay returned %+v, want the original %+v", second, first)
		}
		p, _ := e.readPosition(posInstallation, e.install)
		if p.Reserved != 500 {
			t.Fatalf("replay double-charged: position reserved %d, want 500", p.Reserved)
		}
	})

	t.Run("different fingerprint conflicts", func(t *testing.T) {
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, op, "USD", 900), contract.CodeConflict)
	})

	t.Run("replay works after settlement", func(t *testing.T) {
		res := e.mustSettle(settleInput{
			ReservationID:   first.ID,
			ExpectedVersion: first.Version,
			Usage:           wireUsage{Currency: "USD", Spent: 500},
		})
		if res.State != "settled" {
			t.Fatalf("state %q, want settled", res.State)
		}
		again := e.mustReserve(e.reserveIn(e.scope, op, "USD", 500))
		if again.ID != first.ID || again.State != "settled" {
			t.Fatalf("post-settlement replay = %+v, want the settled reservation", again)
		}
	})

	t.Run("post-settlement different request conflicts", func(t *testing.T) {
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, op, "USD", 700), contract.CodeConflict)
	})
}

func TestReserveCapacityChecks(t *testing.T) {
	t.Run("caller-declared root spend exceeded is invalid_input", func(t *testing.T) {
		e := newEnv(t)
		e.applyBudget(e.install, limits("USD", 1_000_000, 4))
		in := e.reserveIn(e.scope, e.ids.New(), "USD", 10_000)
		in.Limits.SpendMicroUnits = 5_000
		_ = e.expectFault(opReserve, in, contract.CodeInvalidInput)
		// The refused reservation charged nothing anywhere.
		p, ok := e.readPosition(posInstallation, e.install)
		if ok {
			t.Fatalf("refused reservation left a position: %+v", p)
		}
	})
	t.Run("exhausted installation budget is budget_unavailable", func(t *testing.T) {
		e := newEnv(t)
		// The first 1000 binds inside the 1500 ceiling; a second 1000 would
		// push exposure to 2000, past it.
		e.applyBudget(e.install, limits("USD", 1_500, 4))
		e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 1_000), contract.CodeBudgetUnavailable)
		p, _ := e.readPosition(posInstallation, e.install)
		if p.Reserved != 1_000 {
			t.Fatalf("failed admission left reserved %d, want 1000", p.Reserved)
		}
	})
	t.Run("unknown exposure counts against the cap", func(t *testing.T) {
		e := newEnv(t)
		e.applyBudget(e.install, limits("USD", 4_500, 4))
		res := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))
		e.mustSettle(settleInput{
			ReservationID:   res.ID,
			ExpectedVersion: res.Version,
			Usage:           wireUsage{Currency: "USD", Unknown: 1_000},
		})
		p, _ := e.readPosition(posInstallation, e.install)
		if p.Unknown != 1_000 || p.Reserved != 0 {
			t.Fatalf("position after unknown settle %+v, want unknown 1000 reserved 0", p)
		}
		// A fresh admission of 4000 would fit (4000 < 4500) but the retained
		// unknown pushes enforceable exposure to 5000, past the ceiling.
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 4_000), contract.CodeBudgetUnavailable)
	})
	t.Run("estimated exposure never backs the cap", func(t *testing.T) {
		e := newEnv(t)
		e.applyBudget(e.install, limits("USD", 2_000, 4))
		res := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))
		e.mustSettle(settleInput{
			ReservationID:   res.ID,
			ExpectedVersion: res.Version,
			Usage:           wireUsage{Currency: "USD", Spent: 800, Estimated: 5_000, Advisory: true},
		})
		p, _ := e.readPosition(posInstallation, e.install)
		if p.Estimated != 5_800 || p.Spent != 0 {
			t.Fatalf("advisory settle produced %+v, want estimated 5800 spent 0", p)
		}
		next := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))
		if next.ID == "" {
			t.Fatalf("advisory estimate blocked admission")
		}
	})
	t.Run("concurrency exhaustion is budget_unavailable", func(t *testing.T) {
		e := newEnv(t)
		e.applyBudget(e.install, limits("USD", 100_000_000, 2))
		e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 100))
		e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 100))
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 100), contract.CodeBudgetUnavailable)
	})
	t.Run("worker concurrency default is one", func(t *testing.T) {
		e := newEnv(t)
		chain := e.makeChain(chainSpec{
			orgDepth:      2,
			installBudget: &limitsFixture{currency: "USD", spend: 100_000_000},
		})
		scope := e.chainScope(chain)
		e.mustReserve(e.reserveIn(scope, e.ids.New(), "USD", 100))
		_ = e.expectFault(opReserve, e.reserveIn(scope, e.ids.New(), "USD", 100), contract.CodeBudgetUnavailable)
	})
}

func TestReserveRootDimensions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.applyBudget(e.install, limits("USD", 100_000_000, 8))
	t.Run("task effects share the root-task position", func(t *testing.T) {
		root := e.ids.New()
		first := e.reserveIn(e.scope, e.ids.New(), "USD", 500)
		first.RootTaskID = &root
		first.Limits.SpendMicroUnits = 5_000
		res := e.mustReserve(first)
		second := e.reserveIn(e.scope, e.ids.New(), "USD", 300)
		second.RootTaskID = &root
		second.Limits.SpendMicroUnits = 5_000
		e.mustReserve(second)
		p, ok := e.readPosition(posRootTask, root)
		if !ok {
			t.Fatalf("root-task position missing")
		}
		if p.Reserved != 800 {
			t.Fatalf("shared root reserved %d, want 800", p.Reserved)
		}
		if res.RootTaskID == nil || *res.RootTaskID != root {
			t.Fatalf("reservation root task %v, want %s", res.RootTaskID, root)
		}
	})
	t.Run("administrative effects key the root by operation id", func(t *testing.T) {
		op := e.ids.New()
		res := e.mustReserve(e.reserveIn(e.scope, op, "USD", 200))
		if res.RootTaskID != nil {
			t.Fatalf("administrative reservation carries root task %s", *res.RootTaskID)
		}
		p, ok := e.readPosition(posRootTask, op)
		if !ok {
			t.Fatalf("operation-keyed root position missing")
		}
		if p.Reserved != 200 {
			t.Fatalf("operation root reserved %d, want 200", p.Reserved)
		}
	})
	t.Run("shared root spend cap spans siblings", func(t *testing.T) {
		root := e.ids.New()
		first := e.reserveIn(e.scope, e.ids.New(), "USD", 1_000)
		first.RootTaskID = &root
		first.Limits.SpendMicroUnits = 1_500
		e.mustReserve(first)
		second := e.reserveIn(e.scope, e.ids.New(), "USD", 600)
		second.RootTaskID = &root
		second.Limits.SpendMicroUnits = 1_500
		_ = e.expectFault(opReserve, second, contract.CodeBudgetUnavailable)
	})
}

func TestReserveChargesAncestors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	chain := e.makeChain(chainSpec{
		orgDepth:      2,
		installBudget: &limitsFixture{currency: "USD", spend: 100_000_000},
	})
	scope := e.chainScope(chain)
	res := e.mustReserve(e.reserveIn(scope, e.ids.New(), "USD", 400))

	t.Run("every charged level is recorded", func(t *testing.T) {
		var refs []levelRef
		row := e.readReservation(res.ID)
		if err := contract.DecodeStrict([]byte(row.PositionsJSON), &refs); err != nil {
			t.Fatalf("positions decode: %v", err)
		}
		want := []levelRef{
			{Kind: posInstallation, Ref: e.install},
			{Kind: posOrganization, Ref: chain.rootOrg},
			{Kind: posOrganization, Ref: chain.baseOrg},
			{Kind: posProject, Ref: chain.project},
			{Kind: posWorker, Ref: chain.worker},
			{Kind: posRootTask, Ref: res.OperationID},
		}
		if len(refs) != len(want) {
			t.Fatalf("charged positions %v, want %v", refs, want)
		}
		for i := range want {
			if refs[i] != want[i] {
				t.Fatalf("position %d = %+v, want %+v", i, refs[i], want[i])
			}
		}
	})

	t.Run("ancestor org positions carry the charge", func(t *testing.T) {
		for _, org := range []contract.ID{chain.rootOrg, chain.baseOrg} {
			p, ok := e.readPosition(posOrganization, org)
			if !ok || p.Reserved != 400 {
				t.Fatalf("organization %s position %+v, want 400 reserved", org, p)
			}
		}
	})

	t.Run("usage reports the most specific dimension", func(t *testing.T) {
		usage := e.usageGet(scope)
		if usage.Reserved != 400 || usage.Currency != "USD" {
			t.Fatalf("worker usage %+v, want 400 reserved in usd", usage)
		}
	})
}

// TestReserveConcurrentAtomicity is the acceptance focus for concurrent
// reservations: writers serialize on storage, so the per-level check and the
// delta apply inside one write transaction are atomic. No number of parallel
// admissions may oversubscribe a shared position.
func TestReserveConcurrentAtomicity(t *testing.T) {
	t.Run("installation concurrency cap admits exactly its ceiling", func(t *testing.T) {
		e := newEnv(t)
		// No budget: the shipped installation default caps concurrency at
		// four and the zero spend cap is irrelevant for zero-amount work.
		const attempts = 12
		got := e.runParallelReserves(attempts, func(i int) reserveInput {
			return e.reserveIn(e.scope, e.ids.New(), unconfiguredCurrency, 0)
		})
		okCount, refused := countOutcomes(got)
		if okCount != 4 {
			t.Fatalf("admitted %d reservations, want exactly 4", okCount)
		}
		for _, r := range refused {
			if r.Code != contract.CodeBudgetUnavailable {
				t.Fatalf("refusal %s (%s), want budget_unavailable", r.Code, r.Message)
			}
		}
		p, _ := e.readPosition(posInstallation, e.install)
		if p.Concurrency != 4 {
			t.Fatalf("installation live slots %d, want exactly 4", p.Concurrency)
		}
	})

	t.Run("spend cap admits exactly the affordable amount", func(t *testing.T) {
		e := newEnv(t)
		// Concurrency four would admit more; the spend cap is the binding
		// constraint at 2500 total for 1000-per-admission work.
		e.applyBudget(e.install, limits("USD", 2_500, 4))
		const attempts = 8
		got := e.runParallelReserves(attempts, func(i int) reserveInput {
			return e.reserveIn(e.scope, e.ids.New(), "USD", 1_000)
		})
		okCount, refused := countOutcomes(got)
		if okCount != 2 {
			t.Fatalf("admitted %d paid reservations, want exactly 2", okCount)
		}
		for _, r := range refused {
			if r.Code != contract.CodeBudgetUnavailable {
				t.Fatalf("refusal %s (%s), want budget_unavailable", r.Code, r.Message)
			}
		}
		p, _ := e.readPosition(posInstallation, e.install)
		if p.Reserved != 2_000 {
			t.Fatalf("installation reserved %d, want exactly 2000", p.Reserved)
		}
	})

	t.Run("worker concurrency stays at one across parallel attempts", func(t *testing.T) {
		e := newEnv(t)
		chain := e.makeChain(chainSpec{
			orgDepth:      1,
			installBudget: &limitsFixture{currency: "USD", spend: 100_000_000, concurrency: 16},
		})
		scope := e.chainScope(chain)
		got := e.runParallelReserves(6, func(i int) reserveInput {
			return e.reserveIn(scope, e.ids.New(), "USD", 100)
		})
		okCount, refused := countOutcomes(got)
		if okCount != 1 {
			t.Fatalf("admitted %d reservations on one worker, want exactly 1", okCount)
		}
		for _, r := range refused {
			if r.Code != contract.CodeBudgetUnavailable {
				t.Fatalf("refusal %s (%s), want budget_unavailable", r.Code, r.Message)
			}
		}
	})
}

// reserveOutcome is one parallel reserve attempt's result.
type reserveOutcome struct {
	reservation wireReservation
	fault       *contract.Fault
}

// runParallelReserves fires attempts concurrently, each inside its own write
// transaction, and collects the outcomes without failing the test.
func (e *testEnv) runParallelReserves(n int, build func(i int) reserveInput) []reserveOutcome {
	e.t.Helper()
	inputs := make([]reserveInput, n)
	for i := range inputs {
		inputs[i] = build(i)
	}
	results := make([]reserveOutcome, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			raw, err := json.Marshal(inputs[idx])
			if err != nil {
				e.t.Errorf("marshal reserve input: %v", err)
				return
			}
			var out reserveOutcome
			callErr := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
				payload, callErr := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: opReserve, Version: 1, Input: raw})
				if callErr != nil {
					return callErr
				}
				// Decoding the completed payload inside the callback keeps the
				// outcome collection local to this attempt.
				return json.Unmarshal(payload.Data, &out.reservation)
			})
			if callErr != nil {
				var f *contract.Fault
				if errors.As(callErr, &f) {
					out.fault = f
				} else {
					e.t.Errorf("unexpected non-fault error: %v", callErr)
					return
				}
			}
			results[idx] = out
		}(i)
	}
	wg.Wait()
	return results
}

// countOutcomes splits parallel outcomes into successes and refusals.
func countOutcomes(got []reserveOutcome) (ok int, refused []*contract.Fault) {
	for _, r := range got {
		if r.fault == nil {
			ok++
			continue
		}
		refused = append(refused, r.fault)
	}
	return ok, refused
}
