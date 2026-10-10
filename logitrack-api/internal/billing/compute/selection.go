package compute

import (
	"slices"
	"time"
)

// r16 is the R16 identity of an announcement row, the tie-break between rows
// with the same effective instant (§6.5): migrated rows first, by legacy
// document id (the order the legacy engine saw them in; UNVERIFIED: Firestore
// returned query results by document id), then rows created in Go by
// created_at and id (uuidv7, time-ordered). The first in this order wins, so
// of two live rows for one key and day the FIRST created prices; a correction
// voids the original instead (ADR 0009 §1). The selectors apply this order
// themselves, so the result never depends on the order rows were loaded in.
type r16 struct {
	legacyDocID string
	createdAt   time.Time
	id          string
}

// before reports whether o sorts before p in the R16 order. Strings compare
// by bytes, as Firestore orders document ids; never by a database collation.
func (o r16) before(p r16) bool {
	switch {
	case o.legacyDocID != "" && p.legacyDocID == "":
		return true
	case o.legacyDocID == "" && p.legacyDocID != "":
		return false
	case o.legacyDocID != p.legacyDocID:
		return o.legacyDocID < p.legacyDocID
	case !o.createdAt.Equal(p.createdAt):
		return o.createdAt.Before(p.createdAt)
	}
	return o.id < p.id
}

// RateEntry is one customer_rate_entries row (a route rate card, void only).
type RateEntry struct {
	ID              string
	LegacyDocID     string    // legacy_doc_id; "" for rows created in Go
	CreatedAt       time.Time // created_at
	PartyID         string    // billing_party_id
	ImportID        string    // import_id
	HubCode         string    // hub_code, ExtractHubID form
	DestinationCode string    // destination_code, NormalizeDestinationCode form
	VehicleClass    string    // vehicle_class, folded at lookup
	RateTHB         float64
	EffectiveFrom   time.Time   // effective_from_at
	JobCategory     JobCategory // "" is PRIMARY
	Voided          bool
}

// FuelAdjustment is one customer_fuel_rate_adjustments row (a fuel round,
// void only). Fuel is customer-level and has no oldest fallback.
type FuelAdjustment struct {
	ID                    string
	LegacyDocID           string
	CreatedAt             time.Time
	PartyID               string
	EffectiveFrom         time.Time
	RateMultiplier        float64
	AddTHBPerTrip         float64
	ReferenceFuelPriceTHB *float64 // reference_fuel_price_thb; the band derives from it
	Voided                bool
}

// StandbyRate is one standby_rate_entries row: a fixed price per standby
// event. Rows with voided_at set are never selected (R20).
type StandbyRate struct {
	ID            string
	LegacyDocID   string
	CreatedAt     time.Time
	PartyID       string
	RateTHB       float64
	EffectiveFrom time.Time
	Voided        bool // voided_at IS NOT NULL
}

// ordered picks, among candidates, the newest effective on or before bill
// (newest instant first, R16 order between equal instants) or — when every
// candidate starts after bill and oldest is set — the oldest one. Instants
// compare in whole milliseconds, as the legacy effectiveFromMs did.
func ordered[T any](cands []T, at func(T) time.Time, ord func(T) r16, bill time.Time, oldest bool) (T, bool) {
	var zero T
	if len(cands) == 0 {
		return zero, false
	}
	cmp := func(desc bool) func(a, b T) int {
		return func(a, b T) int {
			am, bm := at(a).UnixMilli(), at(b).UnixMilli()
			if am != bm {
				if (am > bm) == desc {
					return -1
				}
				return 1
			}
			if ord(a).before(ord(b)) {
				return -1
			}
			if ord(b).before(ord(a)) {
				return 1
			}
			return 0
		}
	}
	effective := make([]T, 0, len(cands))
	for _, c := range cands {
		if IsEffectiveOn(at(c), bill) {
			effective = append(effective, c)
		}
	}
	if len(effective) > 0 {
		slices.SortStableFunc(effective, cmp(true))
		return effective[0], true
	}
	if !oldest {
		return zero, false
	}
	all := slices.Clone(cands)
	slices.SortStableFunc(all, cmp(false))
	return all[0], true
}

// SelectRateEntry picks the rate card of a route (selectBillingRateEntry,
// billingCompute.ts:274-304): non-voided rows of the party, hub, destination,
// folded vehicle class and job category; among those effective on or before
// the bill's Bangkok day the newest, else — the trip predates every card —
// the OLDEST. Equal instants follow the R16 order. A blank class matches
// nothing (R15).
func SelectRateEntry(party, hub, destination, vehicleClass string, bill time.Time, entries []RateEntry, cat JobCategory) (RateEntry, bool) {
	class, ok := FoldVehicleClass(vehicleClass)
	if !ok {
		return RateEntry{}, false
	}
	if cat == "" {
		cat = Primary
	}
	var cands []RateEntry
	for _, e := range entries {
		if e.Voided || e.PartyID != party || e.HubCode != hub || e.DestinationCode != destination {
			continue
		}
		if ec, ok := FoldVehicleClass(e.VehicleClass); !ok || ec != class {
			continue
		}
		if RateJobCategory(string(e.JobCategory)) != cat {
			continue
		}
		cands = append(cands, e)
	}
	return ordered(cands,
		func(e RateEntry) time.Time { return e.EffectiveFrom },
		func(e RateEntry) r16 { return r16{e.LegacyDocID, e.CreatedAt, e.ID} },
		bill, true)
}

// SelectFuelAdjustment picks the party's fuel round for a bill date
// (selectFuelAdjustmentForBillingDate, billingCompute.ts:306-320): the newest
// non-voided adjustment effective on or before the bill's Bangkok day, R16
// order between equal instants, else none (multiplier 1, add 0). There is no
// oldest fallback.
func SelectFuelAdjustment(party string, bill time.Time, adjustments []FuelAdjustment) (FuelAdjustment, bool) {
	var cands []FuelAdjustment
	for _, a := range adjustments {
		if !a.Voided && a.PartyID == party {
			cands = append(cands, a)
		}
	}
	return ordered(cands,
		func(a FuelAdjustment) time.Time { return a.EffectiveFrom },
		func(a FuelAdjustment) r16 { return r16{a.LegacyDocID, a.CreatedAt, a.ID} },
		bill, false)
}

// SelectStandbyRate picks the party's standby rate (selectStandbyRateEntry,
// billingCompute.ts:553-568): the newest effective on or before the bill's
// Bangkok day, else the OLDEST; voided rows never (R20), R16 order between
// equal instants.
func SelectStandbyRate(party string, bill time.Time, rates []StandbyRate) (StandbyRate, bool) {
	var cands []StandbyRate
	for _, r := range rates {
		if !r.Voided && r.PartyID == party {
			cands = append(cands, r)
		}
	}
	return ordered(cands,
		func(r StandbyRate) time.Time { return r.EffectiveFrom },
		func(r StandbyRate) r16 { return r16{r.LegacyDocID, r.CreatedAt, r.ID} },
		bill, true)
}
