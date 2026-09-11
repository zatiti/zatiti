package accounting

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Settlement: the exact state machine reserved→settled, reserved→unknown,
// unknown→settled and unknown→released, terminal replay idempotency, usage
// sanity checks and exact ledger bookkeeping.

// settledEnv wires the common prefix of settle tests: a paid installation
// budget and one active reservation of the given amount.
func settledEnv(t *testing.T, amount int64) (*testEnv, wireReservation) {
	e := newEnv(t)
	e.applyBudget(e.install, limits("USD", 10_000_000, 8))
	res := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", amount))
	return e, res
}

func TestSettleInputValidation(t *testing.T) {
	t.Parallel()
	e, res := settledEnv(t, 1_000)
	t.Run("unknown reservation is not_found", func(t *testing.T) {
		_ = e.expectFault(opSettle, settleInput{
			ReservationID: e.ids.New(), ExpectedVersion: 1, Usage: wireUsage{Currency: "USD"},
		}, contract.CodeNotFound)
	})
	t.Run("stale version is stale_version", func(t *testing.T) {
		_ = e.expectFault(opSettle, settleInput{
			ReservationID: res.ID, ExpectedVersion: 99, Usage: wireUsage{Currency: "USD", Spent: 100},
		}, contract.CodeStaleVersion)
	})
	t.Run("reported reserved amount is invalid_input", func(t *testing.T) {
		_ = e.expectFault(opSettle, settleInput{
			ReservationID: res.ID, ExpectedVersion: res.Version,
			Usage: wireUsage{Currency: "USD", Reserved: 500},
		}, contract.CodeInvalidInput)
	})
	t.Run("nonexecution with nonzero usage is invalid_input", func(t *testing.T) {
		_ = e.expectFault(opSettle, settleInput{
			ReservationID: res.ID, ExpectedVersion: res.Version,
			Usage:        wireUsage{Currency: "USD", Spent: 500},
			Nonexecution: true,
		}, contract.CodeInvalidInput)
	})
	t.Run("nonzero usage in another currency is invalid_input", func(t *testing.T) {
		_ = e.expectFault(opSettle, settleInput{
			ReservationID: res.ID, ExpectedVersion: res.Version,
			Usage: wireUsage{Currency: "EUR", Spent: 500},
		}, contract.CodeInvalidInput)
	})
	t.Run("zero usage in another currency is accepted", func(t *testing.T) {
		// All-zero usage releases the whole bound; currency is not checked.
		res2 := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 500))
		out := e.mustSettle(settleInput{
			ReservationID: res2.ID, ExpectedVersion: res2.Version,
			Usage: wireUsage{Currency: unconfiguredCurrency},
		})
		if out.State != "settled" {
			t.Fatalf("state %q, want settled", out.State)
		}
	})
}

func TestSettleReservedToSettled(t *testing.T) {
	t.Parallel()
	e, res := settledEnv(t, 1_000)
	out := e.mustSettle(settleInput{
		ReservationID: res.ID, ExpectedVersion: res.Version,
		Usage: wireUsage{Currency: "USD", Spent: 700},
	})
	if out.State != "settled" || out.Version != res.Version+1 {
		t.Fatalf("settle result %+v, want settled at version %d", out, res.Version+1)
	}
	t.Run("positions move exactly", func(t *testing.T) {
		p, ok := e.readPosition(posInstallation, e.install)
		if !ok {
			t.Fatalf("installation position missing")
		}
		if p.Spent != 700 || p.Reserved != 0 || p.Unknown != 0 || p.Estimated != 0 || p.Concurrency != 0 {
			t.Fatalf("position %+v, want spent 700 reserved 0 slots 0", p)
		}
	})
	t.Run("ledger records spent and release", func(t *testing.T) {
		entries := e.readEntries(res.ID)
		// The ledger history is exact: the initial bind, the settled cost and
		// the released unused bound.
		want := map[string]int64{entryReserved: 1_000, entrySpent: 700, entryReleased: 300}
		if len(entries) != 3 {
			t.Fatalf("entries %+v, want bind, spent and released", entries)
		}
		for _, en := range entries {
			if want[en.Kind] != en.Amount {
				t.Fatalf("entry %s amount %d, want %d", en.Kind, en.Amount, want[en.Kind])
			}
			if en.Advisory {
				t.Fatalf("entry %s must not be advisory", en.Kind)
			}
		}
	})
	t.Run("usage view shows the settled cost", func(t *testing.T) {
		usage := e.usageGet(e.scope)
		if usage.Spent != 700 || usage.Reserved != 0 {
			t.Fatalf("usage %+v, want spent 700 reserved 0", usage)
		}
	})
}

func TestSettleAdvisoryLandsInEstimated(t *testing.T) {
	t.Parallel()
	e, res := settledEnv(t, 1_000)
	e.mustSettle(settleInput{
		ReservationID: res.ID, ExpectedVersion: res.Version,
		Usage: wireUsage{Currency: "USD", Spent: 700, Estimated: 200, Advisory: true},
	})
	p, ok := e.readPosition(posInstallation, e.install)
	if !ok {
		t.Fatalf("installation position missing")
	}
	// Advisory usage is never enforceable spent: the whole observed amount
	// lands in the estimated bucket and no bound is released as proven.
	if p.Spent != 0 || p.Estimated != 900 || p.Reserved != 0 || p.Concurrency != 0 {
		t.Fatalf("advisory settle position %+v, want spent 0 estimated 900 slots 0", p)
	}
	entries := e.readEntries(res.ID)
	var spent, estimated int64
	for _, en := range entries {
		switch en.Kind {
		case entrySpent:
			spent += en.Amount
		case entryEstimated:
			estimated += en.Amount
			if !en.Advisory {
				t.Fatalf("estimated entry must be advisory")
			}
		case entryReleased:
			t.Fatalf("advisory settle must not release a proven bound")
		}
	}
	if spent != 0 || estimated != 900 {
		t.Fatalf("entries spent %d estimated %d, want 0/900", spent, estimated)
	}
}

func TestSettleReservedToUnknown(t *testing.T) {
	t.Parallel()
	e, res := settledEnv(t, 1_000)
	out := e.mustSettle(settleInput{
		ReservationID: res.ID, ExpectedVersion: res.Version,
		Usage: wireUsage{Currency: "USD", Unknown: 400},
	})
	if out.State != "unknown" {
		t.Fatalf("state %q, want unknown", out.State)
	}
	p, ok := e.readPosition(posInstallation, e.install)
	if !ok {
		t.Fatalf("installation position missing")
	}
	// The observed unknown moves into the unknown bucket, the rest of the
	// bound leaves reserved, and the concurrency slot stays held.
	if p.Unknown != 400 || p.Reserved != 0 || p.Concurrency != 1 {
		t.Fatalf("position %+v, want unknown 400 reserved 0 slots 1", p)
	}
	entries := e.readEntries(res.ID)
	var unknown, released int64
	for _, en := range entries {
		switch en.Kind {
		case entryUnknown:
			unknown += en.Amount
		case entryReleased:
			released += en.Amount
		}
	}
	if unknown != 400 || released != 600 {
		t.Fatalf("entries unknown %d released %d, want 400/600", unknown, released)
	}
	// The retained unknown keeps enforceable exposure: a reservation that
	// alone would fit the cap no longer does once the unknown is counted.
	_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 10_000_000), contract.CodeBudgetUnavailable)
}

func TestSettleUnknownRecovery(t *testing.T) {
	t.Parallel()
	e, res := settledEnv(t, 1_000)
	e.mustSettle(settleInput{
		ReservationID: res.ID, ExpectedVersion: res.Version,
		Usage: wireUsage{Currency: "USD", Unknown: 800},
	})
	out := e.mustSettle(settleInput{
		ReservationID: res.ID, ExpectedVersion: 2,
		Usage: wireUsage{Currency: "USD", Spent: 300},
	})
	if out.State != "settled" || out.Version != 3 {
		t.Fatalf("recovery result %+v, want settled at version 3", out)
	}
	p, ok := e.readPosition(posInstallation, e.install)
	if !ok {
		t.Fatalf("installation position missing")
	}
	if p.Spent != 300 || p.Unknown != 0 || p.Reserved != 0 || p.Concurrency != 0 {
		t.Fatalf("recovery position %+v, want spent 300 unknown 0 slots 0", p)
	}
	entries := e.readEntries(res.ID)
	// Exact ledger history: the initial bind, the unknown bind with its
	// remainder, then the resolution into spent cost and the newly freed
	// exposure.
	want := []struct {
		kind   string
		amount int64
	}{
		{entryReserved, 1_000},
		{entryUnknown, 800},
		{entryReleased, 200},
		{entrySpent, 300},
		{entryReleased, 500},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries %+v, want %d movements", entries, len(want))
	}
	for i, w := range want {
		if entries[i].Kind != w.kind || entries[i].Amount != w.amount {
			t.Fatalf("entry %d = %s/%d, want %s/%d", i, entries[i].Kind, entries[i].Amount, w.kind, w.amount)
		}
	}
}

func TestSettleUnknownReleased(t *testing.T) {
	t.Parallel()
	e, res := settledEnv(t, 1_000)
	e.mustSettle(settleInput{
		ReservationID: res.ID, ExpectedVersion: res.Version,
		Usage: wireUsage{Currency: "USD", Unknown: 800},
	})
	out := e.mustSettle(settleInput{
		ReservationID: res.ID, ExpectedVersion: 2,
		Usage:        wireUsage{Currency: "USD"},
		Nonexecution: true,
	})
	if out.State != "released" {
		t.Fatalf("state %q, want released", out.State)
	}
	p, ok := e.readPosition(posInstallation, e.install)
	if !ok {
		t.Fatalf("installation position missing")
	}
	if p.Unknown != 0 || p.Reserved != 0 || p.Spent != 0 || p.Concurrency != 0 {
		t.Fatalf("release position %+v, want all zeros", p)
	}
	entries := e.readEntries(res.ID)
	// Exact ledger history: the initial bind, the retained unknown with the
	// released remainder, then the authoritative nonexecution release of the
	// retained amount.
	want := []struct {
		kind   string
		amount int64
	}{
		{entryReserved, 1_000},
		{entryUnknown, 800},
		{entryReleased, 200},
		{entryReleased, 800},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries %+v, want %d movements", entries, len(want))
	}
	for i, w := range want {
		if entries[i].Kind != w.kind || entries[i].Amount != w.amount {
			t.Fatalf("entry %d = %s/%d, want %s/%d", i, entries[i].Kind, entries[i].Amount, w.kind, w.amount)
		}
	}
}

func TestSettleTerminalReplay(t *testing.T) {
	t.Parallel()
	e, res := settledEnv(t, 1_000)
	usage := wireUsage{Currency: "USD", Spent: 700}
	e.mustSettle(settleInput{ReservationID: res.ID, ExpectedVersion: res.Version, Usage: usage})

	t.Run("identical report returns the settled reservation", func(t *testing.T) {
		out := e.mustSettle(settleInput{ReservationID: res.ID, ExpectedVersion: res.Version, Usage: usage})
		if out.State != "settled" || out.Version != res.Version+1 {
			t.Fatalf("replay %+v, want the settled version", out)
		}
		p, _ := e.readPosition(posInstallation, e.install)
		if p.Spent != 700 {
			t.Fatalf("replay double-counted spent %d, want 700", p.Spent)
		}
	})
	t.Run("different report conflicts", func(t *testing.T) {
		_ = e.expectFault(opSettle, settleInput{
			ReservationID: res.ID, ExpectedVersion: res.Version,
			Usage: wireUsage{Currency: "USD", Spent: 800},
		}, contract.CodeConflict)
	})
	t.Run("advisory flag change conflicts", func(t *testing.T) {
		_ = e.expectFault(opSettle, settleInput{
			ReservationID: res.ID, ExpectedVersion: res.Version,
			Usage: wireUsage{Currency: "USD", Spent: 700, Advisory: true},
		}, contract.CodeConflict)
	})
}

func TestSettleReversesEveryChargedPosition(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	chain := e.makeChain(chainSpec{
		orgDepth:      2,
		installBudget: &limitsFixture{currency: "USD", spend: 100_000_000},
	})
	scope := e.chainScope(chain)
	res := e.mustReserve(e.reserveIn(scope, e.ids.New(), "USD", 400))
	e.mustSettle(settleInput{
		ReservationID: res.ID, ExpectedVersion: res.Version,
		Usage: wireUsage{Currency: "USD", Spent: 250},
	})
	for _, org := range []contract.ID{chain.rootOrg, chain.baseOrg} {
		p, ok := e.readPosition(posOrganization, org)
		if !ok {
			t.Fatalf("organization %s position missing after settle", org)
		}
		if p.Spent != 250 || p.Reserved != 0 || p.Concurrency != 0 {
			t.Fatalf("organization %s position %+v, want spent 250 reserved 0 slots 0", org, p)
		}
	}
	usage := e.usageGet(scope)
	if usage.Spent != 250 {
		t.Fatalf("worker usage %+v, want spent 250", usage)
	}
}

func TestSettleSlotLifecycle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.applyBudget(e.install, limits("USD", 100_000_000, 2))
	first := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 100))
	e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 100))
	// Both slots are live: a third admission is refused.
	_ = e.expectFault(opReserve, e.reserveIn(e.scope, e.ids.New(), "USD", 100), contract.CodeBudgetUnavailable)
	// Settling the first frees exactly one slot.
	e.mustSettle(settleInput{
		ReservationID: first.ID, ExpectedVersion: first.Version,
		Usage: wireUsage{Currency: "USD", Spent: 100},
	})
	third := e.mustReserve(e.reserveIn(e.scope, e.ids.New(), "USD", 100))
	if third.ID == "" {
		t.Fatalf("freed slot not reusable")
	}
	// Replaying the terminal transition is idempotent and frees no second
	// slot: the position stays exactly where the first settle left it.
	out := e.mustSettle(settleInput{
		ReservationID: first.ID, ExpectedVersion: first.Version,
		Usage: wireUsage{Currency: "USD", Spent: 100},
	})
	if out.State != "settled" || out.Version != first.Version+1 {
		t.Fatalf("replay %+v, want settled at version %d", out, first.Version+1)
	}
	p, ok := e.readPosition(posInstallation, e.install)
	if !ok {
		t.Fatalf("installation position missing")
	}
	// The replay moves nothing: spent stays 100, the second and third
	// reservations hold their bounds, and the two live slots stay live.
	if p.Spent != 100 || p.Reserved != 200 || p.Concurrency != 2 {
		t.Fatalf("position after replay %+v, want spent 100 with two live slots and their bounds", p)
	}
}
