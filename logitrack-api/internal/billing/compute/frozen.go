package compute

import "math"

// Snapshot is the stored price the frozen rule reads
// (trip_billing_snapshots; legacy billing* fields of trip_records). Nil
// pointers are absent values: a multiplier reads as 1, an add as 0, so a
// snapshot that never carried fuel fields carries no fuel.
type Snapshot struct {
	FuelAdjustmentID *string  // fuel_adjustment_id
	RateMultiplier   *float64 // rate_multiplier
	AddTHBPerTrip    *float64 // add_thb_per_trip
	ManualOverride   bool     // manual_override
}

// CarriesFuel reports whether a stored snapshot was priced WITH a fuel round:
// a non-blank adjustment id, a finite multiplier other than 1, or a finite
// non-zero per-trip add (snapshotCarriesFuel, billingCompute.ts:339-344). On a
// SUPPLEMENTARY row this is the signature of a corrupted price.
//
// This is the only definition in the module (TestFrozenRuleHasOneDefinition).
func CarriesFuel(s Snapshot) bool {
	if s.FuelAdjustmentID != nil && trim(*s.FuelAdjustmentID) != "" {
		return true
	}
	if s.RateMultiplier != nil && !math.IsNaN(*s.RateMultiplier) && !math.IsInf(*s.RateMultiplier, 0) && *s.RateMultiplier != 1 {
		return true
	}
	return s.AddTHBPerTrip != nil && !math.IsNaN(*s.AddTHBPerTrip) && !math.IsInf(*s.AddTHBPerTrip, 0) && *s.AddTHBPerTrip != 0
}

// IsFrozen reports whether a priced snapshot must survive a forced recompute
// (isFrozenBillingSnapshot, billingCompute.ts:356-361; ADR 0005, ADR 0008
// amendment 2026-10-01): an explicit manual override always; a SUPPLEMENTARY
// trip only while its price is clean of fuel — a เสริม label carrying fuel was
// written by a path that ignored หลัก/เสริม and must stay repairable.
// tripJobCategory is trip_records.job_category ("" when absent). A frozen
// trip's forced recompute only restamps billing_date (§6.10 state 3).
//
// This is the only definition in the module (TestFrozenRuleHasOneDefinition).
func IsFrozen(s Snapshot, tripJobCategory JobCategory) bool {
	if s.ManualOverride {
		return true
	}
	return tripJobCategory == Supplementary && !CarriesFuel(s)
}
