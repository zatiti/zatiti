package responses

import (
	"errors"
	"math"
	"math/bits"
)

// errAmountOverflow reports that a charge does not fit int64 micro-units.
var errAmountOverflow = errors.New("amount overflows int64 micro-units")

// chargeFor returns tokens priced at rate, in micro-units, rounded upward:
// ceil(tokens * numerator / denominator) in checked 128-bit integer
// arithmetic. Rounding up means a reservation is never short and an
// observed charge is never understated by a fractional micro-unit.
func chargeFor(tokens int64, rate wireRationalRate) (int64, error) {
	if tokens < 0 || rate.NumeratorMicroUnits < 0 || rate.DenominatorUnits < 1 {
		return 0, errAmountOverflow
	}
	hi, lo := bits.Mul64(uint64(tokens), uint64(rate.NumeratorMicroUnits))
	den := uint64(rate.DenominatorUnits)
	if hi >= den {
		return 0, errAmountOverflow // the quotient would not fit 64 bits
	}
	quo, rem := bits.Div64(hi, lo, den)
	if rem > 0 {
		quo++
	}
	if quo > math.MaxInt64 {
		return 0, errAmountOverflow
	}
	return int64(quo), nil
}

// addAmounts returns a+b for non-negative micro-unit amounts, checked.
func addAmounts(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, errAmountOverflow
	}
	return a + b, nil
}

// costOf prices an input/output token pair under the profile's rates.
func (p *responsesProfile) costOf(inputTokens, outputTokens int64) (int64, error) {
	in, err := chargeFor(inputTokens, p.InputRate)
	if err != nil {
		return 0, err
	}
	out, err := chargeFor(outputTokens, p.OutputRate)
	if err != nil {
		return 0, err
	}
	return addAmounts(in, out)
}

// admittedBounds is what one call was admitted under: the worst-case
// charge the call can incur and whether that worst case is a hard cap or
// only advisory.
type admittedBounds struct {
	WorstCase int64
	Advisory  bool
}

// admit enforces the profile's token and cost bounds for one encoded call
// before anything is sent. maxOutputTokens is the action's requested output
// ceiling (already checked against the profile's); limits and
// inputTokenBound are what the qualified wire protocol can actually
// guarantee about the request it encoded.
//
// Under cost mode "enforced" every bound must be provable: the protocol
// must make the provider enforce the output ceiling and must bound billed
// input tokens, otherwise the hard cap is refused. Under "advisory" an
// unprovable bound is tolerated and the result is flagged advisory, but a
// bound that is known to be exceeded is still refused: advisory excuses
// uncertainty, not a known violation.
func (p *responsesProfile) admit(maxOutputTokens int64, limits protocolLimits, inputTokenBound *int64) (admittedBounds, error) {
	enforced := p.CostMode == enforcementEnforced
	advisory := !enforced

	if !limits.BoundsOutputTokens && enforced {
		return admittedBounds{}, capabilityUnsupported("the selected wire protocol cannot make the provider enforce an output token ceiling; hard cost cap refused")
	}
	inputTokens := p.MaxInputTokens
	switch {
	case inputTokenBound == nil && enforced:
		return admittedBounds{}, capabilityUnsupported("the selected wire protocol cannot bound billed input tokens for this request; hard cost cap refused")
	case inputTokenBound != nil && *inputTokenBound > p.MaxInputTokens:
		return admittedBounds{}, budgetUnavailable("request input may reach %d tokens, above the profile's max_input_tokens %d", *inputTokenBound, p.MaxInputTokens)
	case inputTokenBound != nil:
		if *inputTokenBound < 0 {
			return admittedBounds{}, internalError("wire protocol reported a negative input token bound")
		}
		inputTokens = *inputTokenBound
	}

	worst, err := p.costOf(inputTokens, maxOutputTokens)
	if err != nil {
		return admittedBounds{}, budgetUnavailable("worst-case charge for this model step cannot be represented: %v", err)
	}
	if worst > p.MaximumCost {
		return admittedBounds{}, budgetUnavailable("worst-case charge %d %s micro-units exceeds enforcement.maximum_cost %d", worst, p.Currency, p.MaximumCost)
	}
	return admittedBounds{WorstCase: worst, Advisory: advisory}, nil
}
