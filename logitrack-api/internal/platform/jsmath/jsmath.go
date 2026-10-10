// Package jsmath reproduces the JavaScript number semantics the legacy billing
// code depends on, bit for bit (main spec §6.2, R20). It is the only rounding
// money code may call: math.Round rounds ties away from zero (math.Round(-2.5)
// is -3) while ECMAScript Math.round rounds them toward +Inf (-2), and fuel
// discounts make negative ties real.
//
// Every product in this package and in the packages that price money is
// wrapped in an explicit float64(...) conversion. Go may fuse x*y+z into one
// fused multiply-add on arm64, ppc64le, s390x and riscv64 (and possibly amd64
// with GOAMD64=v3); the conversion forces the intermediate rounding that V8
// performs, so the result matches JavaScript.
package jsmath

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Round is ECMAScript Math.round: the integral number closest to x, ties
// toward +Inf. It keeps the sign of zero as JavaScript does: Round(-0.4) and
// Round(-0.5) are -0, Round(0.4) is +0, and 0.49999999999999994 rounds to +0
// (floor(x+0.5) would give 1).
func Round(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return x
	}
	if x < 0 && x >= -0.5 {
		return math.Copysign(0, -1)
	}
	if x > 0 && x < 0.5 {
		return 0
	}
	f := math.Floor(x)
	// x-f is exact for every finite float64, so the comparison is exact too.
	if float64(x-f) >= 0.5 {
		f++
	}
	return f
}

// Round2 is the legacy two-decimal rounding Math.round(x * 100) / 100.
func Round2(x float64) float64 {
	return Round(float64(x*100)) / 100
}

// ToFixed is ECMAScript Number.prototype.toFixed(digits) for 0 <= digits <= 100:
// the exact decimal value of x rounded half up on its magnitude (the larger n
// of two equally close candidates), so ToFixed(0.125, 2) is "0.13" where
// strconv's round-half-even gives "0.12". -0 prints without a sign, any other
// negative value keeps it ("-0.00" for -0.001). |x| >= 1e21 falls back to the
// shortest round-trip form, as JavaScript's ToString does ("1e+21").
func ToFixed(x float64, digits int) string {
	if digits < 0 || digits > 100 {
		panic("jsmath: ToFixed digits out of range")
	}
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	}
	sign := ""
	if x < 0 {
		sign = "-"
	}
	x = math.Abs(x) // also turns -0, which is not < 0, into +0
	if x >= 1e21 {
		return sign + NumberToString(x)
	}
	// 1100 fractional digits are more than the 1074 a float64 can carry, so
	// this is the exact decimal expansion of x, with no rounding yet.
	exact := new(big.Float).SetFloat64(x).Text('f', 1100)
	intPart, frac, _ := strings.Cut(exact, ".")
	digitsStr := []byte(intPart + frac[:digits])
	if frac[digits] >= '5' {
		// Round half up: at exactly half JavaScript picks the larger n, and a
		// digit >= 5 followed by anything is at least half.
		i := len(digitsStr) - 1
		for ; i >= 0; i-- {
			if digitsStr[i] == '9' {
				digitsStr[i] = '0'
				continue
			}
			digitsStr[i]++
			break
		}
		if i < 0 {
			digitsStr = append([]byte{'1'}, digitsStr...)
		}
	}
	n := len(digitsStr) - digits
	if digits == 0 {
		return sign + string(digitsStr)
	}
	return sign + string(digitsStr[:n]) + "." + string(digitsStr[n:])
}

// NumberToString is ECMAScript Number::toString(10) for the values money code
// prints: the shortest decimal that round-trips, in exponent form from 1e21
// up and below 1e-6, plain otherwise.
func NumberToString(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	case x == 0:
		return "0"
	}
	abs := math.Abs(x)
	if abs >= 1e21 || abs < 1e-6 {
		s := strconv.FormatFloat(x, 'e', -1, 64)
		mant, exp, _ := strings.Cut(s, "e")
		exp = strings.TrimLeft(exp, "+")
		neg := strings.HasPrefix(exp, "-")
		exp = strings.TrimLeft(strings.TrimPrefix(exp, "-"), "0")
		if neg {
			return mant + "e-" + exp
		}
		return mant + "e+" + exp
	}
	return strconv.FormatFloat(x, 'f', -1, 64)
}
