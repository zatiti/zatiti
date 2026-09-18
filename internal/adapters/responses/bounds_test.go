package responses

import (
	"math"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestChargeForRoundsUpAndChecksOverflow(t *testing.T) {
	t.Parallel()
	rate := func(num, den int64) wireRationalRate {
		return wireRationalRate{NumeratorMicroUnits: num, DenominatorUnits: den, Unit: "output_token"}
	}
	cases := []struct {
		name    string
		tokens  int64
		rate    wireRationalRate
		want    int64
		wantErr bool
	}{
		{"exact", 4, rate(7, 2), 14, false},
		{"rounds up", 3, rate(7, 2), 11, false}, // 10.5 -> 11
		{"tiny fraction rounds up", 1, rate(1, 1000000), 1, false},
		{"zero tokens", 0, rate(7, 2), 0, false},
		{"explicit zero price", 1000, rate(0, 1), 0, false},
		{"128-bit intermediate", math.MaxInt64, rate(4, 4), math.MaxInt64, false},
		{"overflow", math.MaxInt64, rate(2, 1), 0, true},
		{"rounding overflows", math.MaxInt64, rate(math.MaxInt64, math.MaxInt64-1), 0, true},
		{"negative tokens", -1, rate(1, 1), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := chargeFor(tc.tokens, tc.rate)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("chargeFor = %d, want %d", got, tc.want)
			}
		})
	}
	if _, err := addAmounts(math.MaxInt64, 1); err == nil {
		t.Fatal("addAmounts did not detect overflow")
	}
}

func TestAdmit(t *testing.T) {
	t.Parallel()
	bound := func(n int64) *int64 { return &n }
	full := protocolLimits{BoundsOutputTokens: true}
	profile := func(mode string, maxCost int64) *responsesProfile {
		return &responsesProfile{
			MaxInputTokens: 1000, Currency: "USD", CostMode: mode, MaximumCost: maxCost,
			InputRate:  wireRationalRate{NumeratorMicroUnits: 2, DenominatorUnits: 1, Unit: "input_token"},
			OutputRate: wireRationalRate{NumeratorMicroUnits: 7, DenominatorUnits: 2, Unit: "output_token"},
		}
	}
	cases := []struct {
		name      string
		profile   *responsesProfile
		maxOutput int64
		limits    protocolLimits
		input     *int64
		wantCode  string
		wantWorst int64
		wantAdv   bool
	}{
		// 100 input * 2 + ceil(3 * 3.5) = 200 + 11
		{"enforced within bounds uses the tighter input bound", profile(enforcementEnforced, 211), 3, full, bound(100), "", 211, false},
		{"enforced one micro-unit over the ceiling", profile(enforcementEnforced, 210), 3, full, bound(100), contract.CodeBudgetUnavailable, 0, false},
		{"enforced without an output ceiling", profile(enforcementEnforced, 1<<40), 3, protocolLimits{}, bound(100), contract.CodeCapabilityUnsupported, 0, false},
		{"enforced without an input bound", profile(enforcementEnforced, 1<<40), 3, full, nil, contract.CodeCapabilityUnsupported, 0, false},
		{"input above max_input_tokens", profile(enforcementEnforced, 1<<40), 3, full, bound(1001), contract.CodeBudgetUnavailable, 0, false},
		{"input exactly max_input_tokens", profile(enforcementEnforced, 1<<40), 2, full, bound(1000), "", 2007, false},
		// Advisory tolerates unprovable bounds and prices the profile maximum.
		{"advisory without any provable bound", profile(enforcementAdvisory, 1<<40), 2, protocolLimits{}, nil, "", 2007, true},
		{"advisory still refuses a known input violation", profile(enforcementAdvisory, 1<<40), 2, protocolLimits{}, bound(1001), contract.CodeBudgetUnavailable, 0, false},
		{"advisory still refuses a known ceiling violation", profile(enforcementAdvisory, 2006), 2, protocolLimits{}, nil, contract.CodeBudgetUnavailable, 0, false},
		{"negative protocol bound", profile(enforcementEnforced, 1<<40), 2, full, bound(-1), contract.CodeInternalError, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.profile.admit(tc.maxOutput, tc.limits, tc.input)
			if tc.wantCode != "" {
				assertFault(t, err, tc.wantCode)
				return
			}
			if err != nil {
				t.Fatalf("admit: %v", err)
			}
			if got.WorstCase != tc.wantWorst || got.Advisory != tc.wantAdv {
				t.Fatalf("admit = %+v, want worst %d advisory %v", got, tc.wantWorst, tc.wantAdv)
			}
		})
	}

	overflow := profile(enforcementEnforced, math.MaxInt64)
	overflow.OutputRate.NumeratorMicroUnits = math.MaxInt64
	overflow.OutputRate.DenominatorUnits = 1
	_, err := overflow.admit(2, full, bound(1))
	assertFault(t, err, contract.CodeBudgetUnavailable)
}
