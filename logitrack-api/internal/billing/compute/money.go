package compute

import (
	"math"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// FinalRateTHB is the priced amount of one leg: Round2(base * mult + add)
// (computeFinalRateThb, billingCompute.ts:323-325). The product is rounded on
// its own, as V8 does, before the add.
func FinalRateTHB(base, mult, add float64) float64 {
	return jsmath.Round2(float64(base*mult) + add)
}

// FuelBandFloor is the lower integer of the upper-inclusive ฿1.00 band
// (n, n+1] holding a retail diesel price, computed in integer satang so a
// price of exactly x.00 stays in the band below (fuelBandFloor,
// billingCompute.ts:205-209): 42.00 -> 41, 42.01 -> 42, 41.1 -> 41. NaN and
// ±Inf give NaN.
func FuelBandFloor(price float64) float64 {
	if math.IsNaN(price) || math.IsInf(price, 0) {
		return math.NaN()
	}
	satang := jsmath.Round(float64(price * 100))
	return math.Ceil(satang/100) - 1
}

// FuelBandRange is the inclusive bounds of the band holding price, e.g.
// 42.00 -> {41.01, 42}; ok is false when there is no price to derive a band
// from (fuelBandRange, billingCompute.ts:212-219).
func FuelBandRange(price float64) (lower, upper float64, ok bool) {
	floor := FuelBandFloor(price)
	if math.IsNaN(floor) || math.IsInf(floor, 0) {
		return 0, 0, false
	}
	return jsmath.Round2(floor + 0.01), floor + 1, true
}

// FuelSurchargeTHB is the signed per-trip fuel step: one thbPerBaht per band
// above (or below) the baseline band, never clamped
// (computeFuelSurchargeThb, billingCompute.ts:226-236). baselineFloor 41 means
// the band 41.01-42.00 carries +0. Any non-finite input gives NaN.
func FuelSurchargeTHB(price, baselineFloor, thbPerBaht float64) float64 {
	floor := FuelBandFloor(price)
	if !finite(floor) || !finite(baselineFloor) || !finite(thbPerBaht) {
		return math.NaN()
	}
	return jsmath.Round2(float64((floor - baselineFloor) * thbPerBaht))
}

// WithholdingTHB is the withholding tax of a statement total at a fractional
// rate: Round2(total * rate) (web:lib/billingDocument.ts:365,675). The net
// amount is total - WithholdingTHB(total, rate), unrounded.
func WithholdingTHB(total, rate float64) float64 {
	return jsmath.Round2(float64(total * rate))
}

func finite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}

// RoundProvenance is the price round a record was priced under and the fuel
// band it prints on an invoice (ADR 0009 §4), denormalised onto the snapshot.
type RoundProvenance struct {
	RoundEffectiveFromDate string   // round_effective_from_date, yyyy-MM-dd Bangkok
	FuelBandLowerTHB       *float64 // nil: no reference price
	FuelBandUpperTHB       *float64
	ReferenceFuelPriceTHB  *float64
}

// ResolveRoundProvenance derives the round from the applied fuel adjustment's
// effective date, or from the rate entry's when no adjustment applied — never
// the later of the two (resolveBillingRoundProvenance,
// billingCompute.ts:68-86; §6.18 #25). The band comes from the adjustment's
// reference price whenever it has one.
func ResolveRoundProvenance(rateEffectiveFrom time.Time, adj *FuelAdjustment) RoundProvenance {
	round := rateEffectiveFrom
	if adj != nil {
		round = adj.EffectiveFrom
	}
	p := RoundProvenance{RoundEffectiveFromDate: clock.DateString(round)}
	if adj != nil && adj.ReferenceFuelPriceTHB != nil {
		price := *adj.ReferenceFuelPriceTHB
		p.ReferenceFuelPriceTHB = &price
		if lower, upper, ok := FuelBandRange(price); ok {
			p.FuelBandLowerTHB, p.FuelBandUpperTHB = &lower, &upper
		}
	}
	return p
}
