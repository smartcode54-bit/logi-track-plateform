package jsmath_test

import (
	"math"
	"math/big"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// The characterisation vectors of testdata/golden/billing pin Round, Round2
// and ToFixed against V8 (run by internal/billing/compute); these tests pin
// the edges by hand and fuzz Round against an exact reference.

func TestRound(t *testing.T) {
	negZero := math.Copysign(0, -1)
	for _, tc := range []struct{ in, want float64 }{
		{2.5, 3}, {-2.5, -2}, {1.5, 2}, {-1.5, -1}, {0.5, 1},
		{-0.5, negZero}, {-0.4, negZero}, {0.4, 0}, {negZero, negZero},
		{0.49999999999999994, 0}, {4503599627370495.5, 4503599627370496}, {-4503599627370495.5, -4503599627370495},
		{1e300, 1e300}, {math.Inf(-1), math.Inf(-1)},
	} {
		got := jsmath.Round(tc.in)
		if math.Float64bits(got) != math.Float64bits(tc.want) {
			t.Errorf("Round(%v) = %v (signbit %v), want %v (signbit %v)", tc.in, got, math.Signbit(got), tc.want, math.Signbit(tc.want))
		}
	}
	if !math.IsNaN(jsmath.Round(math.NaN())) {
		t.Errorf("Round(NaN) must be NaN")
	}
	if got := jsmath.Round2(1.005); got != 1 { // 100.49999999999999 satang
		t.Errorf("Round2(1.005) = %v, want 1", got)
	}
	if got := jsmath.Round2(-2.675); got != -2.67 {
		t.Errorf("Round2(-2.675) = %v, want -2.67", got)
	}
}

// reference is ECMAScript Math.round computed exactly: floor(x + 1/2) on the
// real value, -0 for [-0.5, -0).
func reference(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return x
	}
	if x < 0 && x >= -0.5 {
		return math.Copysign(0, -1)
	}
	sum := new(big.Float).SetPrec(2000).SetFloat64(x)
	sum.Add(sum, big.NewFloat(0.5))
	i, acc := sum.Int(nil)
	if acc == big.Above { // Int truncates toward zero; floor for negatives
		i.Sub(i, big.NewInt(1))
	}
	f, _ := new(big.Float).SetInt(i).Float64()
	return f
}

func FuzzRound(f *testing.F) {
	for _, x := range []float64{0, 0.5, -0.5, 2.5, -2.5, 0.49999999999999994, 4503599627370495.5, 116000.00000000001, -1e-300} {
		f.Add(x)
	}
	f.Fuzz(func(t *testing.T, x float64) {
		got, want := jsmath.Round(x), reference(x)
		if math.Float64bits(got) != math.Float64bits(want) && !(math.IsNaN(got) && math.IsNaN(want)) {
			t.Fatalf("Round(%v) = %v, want %v", x, got, want)
		}
	})
}

func TestToFixed(t *testing.T) {
	for _, tc := range []struct {
		in     float64
		digits int
		want   string
	}{
		{0.125, 2, "0.13"}, // exact tie: JavaScript picks the larger n
		{0.375, 2, "0.38"},
		{1.005, 2, "1.00"}, // 1.00499999999999989...
		{2.675, 2, "2.67"},
		{8.345, 2, "8.35"}, // 8.34500000000000063...
		{-2.5, 0, "-3"},
		{2.5, 0, "3"},
		{-0.001, 2, "-0.00"},
		{math.Copysign(0, -1), 2, "0.00"},
		{999.995, 2, "1000.00"}, // 999.995000000000004547...: rounds up and carries into a new digit
		{99.995, 2, "100.00"},
		{123456789012345680000, 0, "123456789012345683968"}, // the exact value, not the shortest form
		{37.01, 2, "37.01"},
		{38, 2, "38.00"},
		{1e21, 2, "1e+21"},
		{-1.5e21, 2, "-1.5e+21"},
		{math.NaN(), 2, "NaN"},
		{math.Inf(1), 2, "Infinity"},
		{123.456, 0, "123"},
	} {
		if got := jsmath.ToFixed(tc.in, tc.digits); got != tc.want {
			t.Errorf("ToFixed(%v, %d) = %q, want %q", tc.in, tc.digits, got, tc.want)
		}
	}
}

func TestNumberToString(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0, "0"}, {math.Copysign(0, -1), "0"}, {1200, "1200"}, {0.30000000000000004, "0.30000000000000004"},
		{1e21, "1e+21"}, {1.5e-7, "1.5e-7"}, {0.000001, "0.000001"}, {-2.5e22, "-2.5e+22"}, {123456789012345680000, "123456789012345680000"},
	} {
		if got := jsmath.NumberToString(tc.in); got != tc.want {
			t.Errorf("NumberToString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
