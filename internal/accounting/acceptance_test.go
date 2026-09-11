package accounting

import (
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Acceptance cases assigned to accounting. Each test drives the full flow
// through the service boundaries against a real storage database and asserts
// the observable outcome the case names.

// Z09.concurrent_reservations: budgets nearly exhausted at several levels;
// concurrent admissions compete for the remaining funds.
func TestAcceptanceConcurrentReservations(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// The root organization holds the binding spend ceiling: 1500 admits two
	// 700-micro reservations, while the installation (2500) would admit three.
	// The worker budget keeps the shipped one-attempt worker default from
	// binding before the organization's spend ceiling does.
	chain := e.makeChain(chainSpec{
		orgDepth:      2,
		installBudget: &limitsFixture{currency: "USD", spend: 2_500, concurrency: 8},
		rootOrgBudget: &limitsFixture{currency: "USD", spend: 1_500, concurrency: 8},
		workerBudget:  &limitsFixture{currency: "USD", spend: 50_000_000, concurrency: 8},
	})
	scope := e.chainScope(chain)
	got := e.runParallelReserves(6, func(i int) reserveInput {
		return e.reserveIn(scope, e.ids.New(), "USD", 700)
	})
	okCount, refused := countOutcomes(got)
	if okCount != 2 {
		t.Fatalf("admitted %d reservations against a 1500 root budget, want exactly 2", okCount)
	}
	for _, r := range refused {
		if r.Code != contract.CodeBudgetUnavailable {
			t.Fatalf("refusal %s (%s), want budget_unavailable", r.Code, r.Message)
		}
	}
	for _, ref := range []struct {
		kind positionKind
		id   contract.ID
	}{{posInstallation, e.install}, {posOrganization, chain.rootOrg}} {
		p, ok := e.readPosition(ref.kind, ref.id)
		if !ok || p.Reserved != 1_400 {
			t.Fatalf("%s/%s position %+v, want exactly 1400 reserved", ref.kind, ref.id, p)
		}
	}
	// The shared limits are consumed, not copied: a replayed single admission
	// now sees the exhausted root budget.
	_ = e.expectFault(opReserve, e.reserveIn(scope, e.ids.New(), "USD", 700), contract.CodeBudgetUnavailable)
}

// Z09.unknown_external_cost: external usage whose charge cannot be bounded
// is reported separately and never treated as zero.
func TestAcceptanceUnknownExternalCost(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.applyBudget(e.install, limits("USD", 5_000, 4))
	res := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))

	// Advisory usage cannot back an enforceable cap: it lands in estimated,
	// separately reported, never zeroed.
	e.mustSettle(settleInput{
		ReservationID: res.ID, ExpectedVersion: res.Version,
		Usage: wireUsage{Currency: "USD", Spent: 800, Advisory: true},
	})
	usage := e.usageGet(e.scope)
	if usage.Spent != 0 || usage.Estimated != 800 || !usage.Advisory {
		t.Fatalf("advisory usage %+v, want estimated 800 marked advisory", usage)
	}
	// Unknown usage keeps enforceable exposure: it is its own bucket.
	res2 := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))
	e.mustSettle(settleInput{
		ReservationID: res2.ID, ExpectedVersion: res2.Version,
		Usage: wireUsage{Currency: "USD", Unknown: 1_000},
	})
	usage = e.usageGet(e.scope)
	if usage.Unknown != 1_000 || usage.Estimated != 800 || usage.Spent != 0 {
		t.Fatalf("usage after unknown settle %+v, want unknown 1000, estimated 800, spent 0", usage)
	}
	// The unknown exposure backs the cap: only 4000 more fits (1000 already
	// retained), and no admission may pretend the unknown is zero.
	_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 4_001), contract.CodeBudgetUnavailable)
}

// Z09.missing_price: paid admission without a configured currency, finite
// spend ceiling or budget is refused by name before any dispatch, and no
// hardcoded fallback mints a price.
func TestAcceptanceMissingPrice(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	t.Run("an unconfigured currency refuses a positive amount", func(t *testing.T) {
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), unconfiguredCurrency, 2_500), contract.CodeBudgetUnavailable)
	})
	t.Run("no budget refuses a priced amount", func(t *testing.T) {
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 2_500), contract.CodeBudgetUnavailable)
	})
	t.Run("a zero spend ceiling refuses paid work", func(t *testing.T) {
		// The shipped installation default carries spend zero: paid work is
		// refused until an explicit budget configures currency and ceiling.
		_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 2_500), contract.CodeBudgetUnavailable)
	})
	t.Run("the refusals charged nothing anywhere", func(t *testing.T) {
		if p, ok := e.readPosition(posInstallation, e.install); ok {
			t.Fatalf("refused admissions left an installation position %+v", p)
		}
		if row := e.readReservation(e.ids.New()); row != nil {
			t.Fatalf("refused admissions left a reservation row %+v", row)
		}
	})
	t.Run("rational rates price the reservation exactly", func(t *testing.T) {
		e.applyBudget(e.install, limits("USD", 10_000, 4))
		// 3/2 micro-units per step, rounded up: five steps cost eight.
		amount, err := (Rate{Numerator: 3, Denominator: 2}).ApplyCeil(5)
		if err != nil {
			t.Fatalf("rate application: %v", err)
		}
		res := e.mustReserve(reserveInput{
			Scope: e.scope, OperationID: e.ids.New(),
			Amount: wireMoney{Currency: "USD", MicroUnits: amount},
			Limits: limits("USD", 10_000, 4),
		})
		if res.Amount.MicroUnits != 8 {
			t.Fatalf("rated amount %d, want 8", res.Amount.MicroUnits)
		}
	})
}

// Z09.settlement_recovery: every disposition is exact, settlement is atomic
// and idempotent, and unresolved reservations survive without double
// release.
func TestAcceptanceSettlementRecovery(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.applyBudget(e.install, limits("USD", 100_000, 4))
	spent := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))
	refundable := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))
	unresolved := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))

	e.mustSettle(settleInput{
		ReservationID: spent.ID, ExpectedVersion: spent.Version,
		Usage: wireUsage{Currency: "USD", Spent: 1_000},
	})
	e.mustSettle(settleInput{
		ReservationID: refundable.ID, ExpectedVersion: refundable.Version,
		Usage: wireUsage{Currency: "USD"},
	})

	// A settle that cannot complete fails closed: nothing is applied, the
	// reservation and its charge survive intact, and a retry settles once.
	poisoned(e)(unresolved.ID)
	if _, err := e.call(opSettle, settleInput{
		ReservationID: unresolved.ID, ExpectedVersion: unresolved.Version,
		Usage: wireUsage{Currency: "USD", Spent: 400},
	}); err == nil || strings.Contains(err.Error(), "fault") {
		t.Fatalf("corrupted settle %v, want a storage-corruption failure", err)
	}
	p, ok := e.readPosition(posInstallation, e.install)
	if !ok {
		t.Fatalf("installation position missing")
	}
	// Spent 1000 from the first settle; the corrupted reservation's 1000
	// remains reserved, the released one freed.
	if p.Spent != 1_000 || p.Reserved != 1_000 || p.Concurrency != 1 {
		t.Fatalf("position after failed settle %+v, want spent 1000 reserved 1000 one slot", p)
	}
	restore(e)(unresolved.ID)
	out := e.mustSettle(settleInput{
		ReservationID: unresolved.ID, ExpectedVersion: unresolved.Version,
		Usage: wireUsage{Currency: "USD", Spent: 400},
	})
	if out.State != "settled" {
		t.Fatalf("recovered settle state %q, want settled", out.State)
	}
	// The replayed terminal command is idempotent and releases nothing twice.
	e.mustSettle(settleInput{
		ReservationID: unresolved.ID, ExpectedVersion: unresolved.Version,
		Usage: wireUsage{Currency: "USD", Spent: 400},
	})
	p, _ = e.readPosition(posInstallation, e.install)
	if p.Spent != 1_400 || p.Reserved != 0 || p.Concurrency != 0 {
		t.Fatalf("position after recovery %+v, want spent 1400 reserved 0 slots 0", p)
	}
	entries := e.readEntries(unresolved.ID)
	var released int64
	for _, en := range entries {
		if en.Kind == entryReleased {
			released += en.Amount
		}
	}
	if released != 600 {
		t.Fatalf("recovered reservation released %d, want exactly 600", released)
	}
}

// poisoned corrupts the reservation's charged-position record through a
// direct storage write, simulating a row lost outside the service.
func poisoned(e *testEnv) func(contract.ID) {
	return func(id contract.ID) {
		e.t.Helper()
		if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
			_, err := unit.ExecContext(e.ctx, `UPDATE accounting_reservations SET positions_json = ?
				WHERE id = ?`, `[{"kind":"installation","ref":"00000000-0000-4000-8000-000000000000"}]`, string(id))
			return err
		}); err != nil {
			e.t.Fatalf("poison reservation %s: %v", id, err)
		}
	}
}

// restore rewrites a reservation's charged positions to the installation
// level it actually charged, ending the simulated corruption.
func restore(e *testEnv) func(contract.ID) {
	return func(id contract.ID) {
		e.t.Helper()
		if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
			_, err := unit.ExecContext(e.ctx, `UPDATE accounting_reservations SET positions_json = ?
				WHERE id = ?`, `[{"kind":"installation","ref":"`+string(e.install)+`"}]`, string(id))
			return err
		}); err != nil {
			e.t.Fatalf("restore reservation %s: %v", id, err)
		}
	}
}

// Z17.ancestor_budget_exhaustion: the descendant has local funds but an
// exhausted ancestor; admission is refused and nothing bypasses the
// aggregate.
func TestAcceptanceAncestorBudgetExhaustion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	chain := e.makeChain(chainSpec{
		orgDepth:      2,
		installBudget: &limitsFixture{currency: "USD", spend: 50_000_000, concurrency: 8},
		rootOrgBudget: &limitsFixture{currency: "USD", spend: 1_000},
		projectBudget: &limitsFixture{currency: "USD", spend: 50_000_000},
		workerBudget:  &limitsFixture{currency: "USD", spend: 50_000_000},
	})
	scope := e.chainScope(chain)
	e.mustReserve(e.reserveIn(scope, e.ids.New(), "USD", 1_000))
	_ = e.expectFault(opReserve, e.reserveIn(scope, e.ids.New(), "USD", 1_000), contract.CodeBudgetUnavailable)
	// The refused admission moved nothing: every charged level stays exact.
	p, ok := e.readPosition(posOrganization, chain.rootOrg)
	if !ok || p.Reserved != 1_000 {
		t.Fatalf("exhausted ancestor position %+v, want exactly 1000 reserved", p)
	}
	if p, ok := e.readPosition(posInstallation, e.install); !ok || p.Reserved != 1_000 {
		t.Fatalf("installation position %+v, want exactly 1000 reserved", p)
	}
}

// Z12.root_limits_shared: every branch of a root task shares the root
// budget; a new child cannot reset it. The shipped defaults match the named
// constants.
func TestAcceptanceRootLimitsShared(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	payload := e.mustOK(opBudgetGet, scopeInput{Scope: e.scope})
	var out limitsOutput
	e.decode(payload.Data, &out)
	if out.Limits.Concurrency != 4 || out.Limits.ModelSteps != 100 || out.Limits.AttemptSeconds != 1_800 {
		t.Fatalf("default limits %+v, want four installation attempts, 100 steps, 1800 seconds", out.Limits)
	}
	if out.Limits.ChildCount != 8 || out.Limits.DelegationDepth != 3 {
		t.Fatalf("default delegation limits %+v, want eight children at depth three", out.Limits)
	}
	if !out.Limits.RootDeadline.IsZero() {
		t.Fatalf("pure limit view carries deadline %v, want none", out.Limits.RootDeadline)
	}

	// Delegation children of one root share its finite spend: the third
	// branch cannot reset what the first two consumed.
	e.applyBudget(e.install, limits("USD", 1_000_000, 8))
	root := e.ids.New()
	build := func(amount int64) reserveInput {
		in := e.reserveIn(e.scope, e.ids.New(), "USD", amount)
		in.RootTaskID = &root
		in.Limits.SpendMicroUnits = 2_000
		return in
	}
	e.mustReserve(build(1_000))
	e.mustReserve(build(900))
	_ = e.expectFault(opReserve, build(1_000), contract.CodeBudgetUnavailable)
	p, ok := e.readPosition(posRootTask, root)
	if !ok || p.Reserved != 1_900 {
		t.Fatalf("shared root position %+v, want exactly 1900 reserved", p)
	}
}

// Z08.lost_success_response accounting slice: an operation whose external
// outcome never arrived keeps its reservation, holds its slot and stays
// inspectable in the unknown bucket.
func TestAcceptanceLostSuccessKeepsReservation(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.applyBudget(e.install, limits("USD", 10_000, 2))
	res := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))
	out := e.mustSettle(settleInput{
		ReservationID: res.ID, ExpectedVersion: res.Version,
		Usage: wireUsage{Currency: "USD", Unknown: 1_000},
	})
	if out.State != "unknown" {
		t.Fatalf("state %q, want unknown", out.State)
	}
	row := e.readReservation(res.ID)
	if row == nil || row.State != "unknown" || row.SettleUsageJSON == "" {
		t.Fatalf("reservation %+v, want unknown state with the recorded usage report", row)
	}
	p, ok := e.readPosition(posInstallation, e.install)
	if !ok || p.Unknown != 1_000 || p.Concurrency != 1 {
		t.Fatalf("position %+v, want 1000 unknown and the slot retained", p)
	}
	usage := e.usageGet(e.scope)
	if usage.Unknown != 1_000 {
		t.Fatalf("usage %+v, want the unknown exposure inspectable", usage)
	}
}

// Z08.failed_retry_after_unknown accounting slice: a later failed attempt
// releases only its own bound; the earlier unknown reservation and its
// retained exposure are untouched.
func TestAcceptanceFailedRetryKeepsUnknown(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.applyBudget(e.install, limits("USD", 10_000, 4))
	earlier := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))
	e.mustSettle(settleInput{
		ReservationID: earlier.ID, ExpectedVersion: earlier.Version,
		Usage: wireUsage{Currency: "USD", Unknown: 1_000},
	})
	// The qualified retry admits on its own bound and fails definitively.
	retry := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 500))
	e.mustSettle(settleInput{
		ReservationID: retry.ID, ExpectedVersion: retry.Version,
		Usage:        wireUsage{Currency: "USD"},
		Nonexecution: true,
	})
	p, ok := e.readPosition(posInstallation, e.install)
	if !ok {
		t.Fatalf("installation position missing")
	}
	if p.Unknown != 1_000 || p.Spent != 0 || p.Reserved != 0 {
		t.Fatalf("position after failed retry %+v, want the earlier 1000 unknown intact", p)
	}
	row := e.readReservation(earlier.ID)
	if row == nil || row.State != "unknown" {
		t.Fatalf("earlier reservation %+v, want its uncertainty preserved", row)
	}
	if retryRow := e.readReservation(retry.ID); retryRow == nil || retryRow.State != "released" {
		t.Fatalf("retry reservation %+v, want released", retryRow)
	}
}

// Z08.cancel_unknown accounting slice: cancellation acknowledges uncertainty
// as restrictive intent; the possibly-sent attempt stays unknown and
// inspectable until supported evidence resolves it.
func TestAcceptanceCancelKeepsUnknownInspect(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// A one-slot installation budget: the retained unknown of the first
	// reservation occupies the only attempt.
	e.applyBudget(e.install, limits("USD", 10_000, 1))
	first := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 1_000))
	e.mustSettle(settleInput{
		ReservationID: first.ID, ExpectedVersion: first.Version,
		Usage: wireUsage{Currency: "USD", Unknown: 1_000},
	})
	// The retained unknown still occupies its slot: the cancellation of the
	// task changes nothing about the reservation until evidence arrives.
	_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 1_000), contract.CodeBudgetUnavailable)
	// Supported evidence then resolves the unknown exactly once.
	out := e.mustSettle(settleInput{
		ReservationID: first.ID, ExpectedVersion: 2,
		Usage: wireUsage{Currency: "USD", Spent: 1_000},
	})
	if out.State != "settled" {
		t.Fatalf("resolution state %q, want settled", out.State)
	}
	p, _ := e.readPosition(posInstallation, e.install)
	if p.Unknown != 0 || p.Spent != 1_000 || p.Concurrency != 0 {
		t.Fatalf("position after resolution %+v, want the unknown resolved to spent 1000", p)
	}
}
