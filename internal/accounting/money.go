package accounting

import (
	"math/big"
)

// Money arithmetic. Amounts are int64 micro-units; every operation checks
// overflow and refuses instead of wrapping. Rates are exact rationals with
// integer numerator/denominator; reservations round upward (ceil) and
// floating point never touches money.

// addChecked returns a+b or a fault when the sum overflows int64.
func addChecked(a, b int64) (int64, error) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, invalidInput("amount overflow: %d + %d exceeds the int64 range", a, b)
	}
	return sum, nil
}

// subChecked returns a-b or a fault when the difference underflows int64.
// Callers use it on quantities they already know are non-negative.
func subChecked(a, b int64) (int64, error) {
	diff := a - b
	if (b < 0 && diff < a) || (b > 0 && diff > a) {
		return 0, invalidInput("amount underflow: %d - %d exceeds the int64 range", a, b)
	}
	return diff, nil
}

// Rate is one exact rational multiplier: numerator/denominator. The
// denominator must be positive; the numerator may be zero (a free rate).
type Rate struct {
	Numerator   int64
	Denominator int64
}

// Valid reports whether the rate is well-formed: a positive denominator and
// a non-negative numerator.
func (r Rate) Valid() bool {
	return r.Denominator > 0 && r.Numerator >= 0
}

// ApplyCeil returns ceil(units * Numerator / Denominator) with checked
// overflow across the whole computation. It is the conversion seam where a
// price (numerator micro-units per denominator units) produces the enforceable
// bound a reservation charges; the reservation always rounds up so that a
// settled actual cost never exceeds the reserved bound through rounding.
func (r Rate) ApplyCeil(units int64) (int64, error) {
	if !r.Valid() {
		return 0, invalidInput("rate %d/%d is malformed: the denominator must be positive and the numerator non-negative",
			r.Numerator, r.Denominator)
	}
	if units < 0 {
		return 0, invalidInput("rate applied to a negative quantity %d", units)
	}
	num := new(big.Int).Mul(big.NewInt(r.Numerator), big.NewInt(units))
	den := big.NewInt(r.Denominator)
	// Ceil division on non-negative values: (num + den - 1) / den.
	num.Add(num, den)
	num.Sub(num, big.NewInt(1))
	num.Div(num, den)
	if !num.IsInt64() {
		return 0, invalidInput("rate %d/%d over %d overflows the int64 range", r.Numerator, r.Denominator, units)
	}
	return num.Int64(), nil
}
