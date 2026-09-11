package accounting

import (
	"math"
	"testing"
)

// Money arithmetic: int64 micro-units with checked overflow, and exact
// rational rates that round upward.

func TestAddChecked(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		a, b    int64
		want    int64
		wantErr bool
	}{
		{"positive sum", 100, 200, 300, false},
		{"zero addend", 100, 0, 100, false},
		{"negative addend", 100, -400, -300, false},
		{"int64 max overflow", math.MaxInt64, 1, 0, true},
		{"int64 min underflow", math.MinInt64, -1, 0, true},
		{"max minus max", math.MaxInt64, -math.MaxInt64, 0, false},
		{"extreme sum stays representable", math.MaxInt64, math.MinInt64, -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := addChecked(tc.a, tc.b)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("addChecked(%d, %d): want overflow error, got %d", tc.a, tc.b, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("addChecked(%d, %d): unexpected error: %v", tc.a, tc.b, err)
			}
			if got != tc.want {
				t.Fatalf("addChecked(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestSubChecked(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		a, b    int64
		want    int64
		wantErr bool
	}{
		{"positive difference", 300, 200, 100, false},
		{"zero subtrahend", 100, 0, 100, false},
		{"negative result", 100, 400, -300, false},
		{"int64 min underflow", math.MinInt64, 1, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := subChecked(tc.a, tc.b)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("subChecked(%d, %d): want underflow error, got %d", tc.a, tc.b, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("subChecked(%d, %d): unexpected error: %v", tc.a, tc.b, err)
			}
			if got != tc.want {
				t.Fatalf("subChecked(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestRateValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rate Rate
		want bool
	}{
		{"well-formed", Rate{Numerator: 3, Denominator: 2}, true},
		{"free rate", Rate{Numerator: 0, Denominator: 1}, true},
		{"zero denominator", Rate{Numerator: 1, Denominator: 0}, false},
		{"negative denominator", Rate{Numerator: 1, Denominator: -2}, false},
		{"negative numerator", Rate{Numerator: -1, Denominator: 2}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.rate.Valid(); got != tc.want {
				t.Fatalf("Rate{%d/%d}.Valid() = %v, want %v", tc.rate.Numerator, tc.rate.Denominator, got, tc.want)
			}
		})
	}
}

func TestRateApplyCeil(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		rate     Rate
		units    int64
		want     int64
		wantErr  bool
		errFault string
	}{
		{"exact division", Rate{Numerator: 3, Denominator: 2}, 4, 6, false, ""},
		{"rounds up", Rate{Numerator: 3, Denominator: 2}, 3, 5, false, ""},
		{"zero numerator", Rate{Numerator: 0, Denominator: 7}, 100, 0, false, ""},
		{"zero units", Rate{Numerator: 5, Denominator: 3}, 0, 0, false, ""},
		{"negative units refused", Rate{Numerator: 1, Denominator: 1}, -1, 0, true, "invalid_input"},
		{"malformed rate refused", Rate{Numerator: 1, Denominator: 0}, 10, 0, true, "invalid_input"},
		{"overflow refused", Rate{Numerator: math.MaxInt64, Denominator: 1}, math.MaxInt64, 0, true, "invalid_input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.rate.ApplyCeil(tc.units)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ApplyCeil(%d over %d/%d): want error, got %d", tc.units, tc.rate.Numerator, tc.rate.Denominator, got)
				}
				f := faultCode(err)
				if f != tc.errFault {
					t.Fatalf("ApplyCeil error code %q, want %q", f, tc.errFault)
				}
				return
			}
			if err != nil {
				t.Fatalf("ApplyCeil: unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ApplyCeil(%d over %d/%d) = %d, want %d", tc.units, tc.rate.Numerator, tc.rate.Denominator, got, tc.want)
			}
		})
	}
}
