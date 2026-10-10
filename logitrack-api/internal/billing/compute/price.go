package compute

import (
	"errors"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// ErrMultiInsufficientStops: a multi-drop trip with fewer than two delivered
// stops is not priced and nothing is written (legacy throws,
// fn:tripBillingOnDelivered.ts:407-414). It is an error, not an unpriced
// reason (R62).
var ErrMultiInsufficientStops = errors.New("multi_insufficient_stops")

// TaskInput is what pricing reads from a trip's task.
type TaskInput struct {
	TaskParties
	SourceHub   string // tasks.source_hub (code or display name)
	Destination string // tasks.destination (code or display name)
	TruckType   string // tasks.truck_type; "" is NULL
}

// StopCharge is one line of a multi-drop breakdown
// (trip_billing_stop_breakdown).
type StopCharge struct {
	StopIndex       int     // 1 = base leg, 2.. = extra stops
	DestinationCode string  // normalised destination
	BaseRateTHB     float64 // raw card price, or the flat fee
	FinalRateTHB    float64 // fuel-adjusted price, or the flat fee
}

// TripPrice is a priced snapshot (trip_billing_snapshots, §6.10) with its full
// ADR 0009 provenance, for single and multi-drop trips alike.
type TripPrice struct {
	PartyID string
	// EstimateTHB is the billed amount: the final rate of a single trip, or
	// BaseRateTHB + StopChargeTHB of a multi-drop trip (an unrounded float sum).
	EstimateTHB float64
	// BaseRateTHB is the RAW card price of a single trip but the FUEL-ADJUSTED
	// base leg of a multi-drop trip (§6.18 #3, preserved).
	BaseRateTHB           float64
	StopChargeTHB         float64 // multi-drop extra stops; 0 on a single trip
	IsMultiDelivery       bool
	RateEntryID           string // the card that priced the (base) leg
	RateImportID          string
	LookupHubCode         string
	LookupDestinationCode string // single: the destination priced; multi: the base leg's ("" when none)
	FuelAdjustmentID      string // "" when no fuel round applied
	RateMultiplier        float64
	AddTHBPerTrip         float64
	FuelEffectiveFromDate string       // yyyy-MM-dd of the applied fuel round; "" when none
	RoundProvenance                    // round + band (ADR 0009 §4)
	Stops                 []StopCharge // multi-drop only
	JobCategory           JobCategory  // the category that priced (set by PriceTrip)
	ManualOverride        bool         // SUPPLEMENTARY prices are frozen (set by PriceTrip)
}

// Outcome is a price or the reason there is none.
type Outcome struct {
	Price  *TripPrice
	Reason UnpricedReason // set iff Price == nil
}

func unpriced(r UnpricedReason) Outcome { return Outcome{Reason: r} }

// fuelFor is the fuel round applied to a category: none for SUPPLEMENTARY,
// whose cards are fixed prices fuel never moves (billingCompute.ts:424-427,
// 619-622).
func fuelFor(cat JobCategory, party string, bill time.Time, fuel []FuelAdjustment) *FuelAdjustment {
	if cat == Supplementary {
		return nil
	}
	if adj, ok := SelectFuelAdjustment(party, bill, fuel); ok {
		return &adj
	}
	return nil
}

// applyFuel fills the fuel provenance of p from the applied round.
func applyFuel(p *TripPrice, adj *FuelAdjustment) {
	p.RateMultiplier, p.AddTHBPerTrip = 1, 0
	if adj == nil {
		return
	}
	p.FuelAdjustmentID = adj.ID
	p.RateMultiplier, p.AddTHBPerTrip = adj.RateMultiplier, adj.AddTHBPerTrip
	p.FuelEffectiveFromDate = clock.DateString(adj.EffectiveFrom)
}

// PriceLeg prices a single trip for one job category
// (computeTripBillingFromParts, billingCompute.ts:590-643): party, hub,
// destination, class, bill date, rate card, fuel unless SUPPLEMENTARY, final
// rate. The task's hub and destination must already be resolved name -> code.
// Unpriced reasons come in the order no_customer, no_vehicle_class,
// no_billing_date, no_rate.
func PriceLeg(dates BillingDates, task TaskInput, rates []RateEntry, fuel []FuelAdjustment, cat JobCategory) Outcome {
	if cat == "" {
		cat = Primary
	}
	party, ok := ResolveTaskParty(task.TaskParties)
	if !ok {
		return unpriced(NoCustomer)
	}
	hub := ExtractHubID(task.SourceHub)
	dest := NormalizeDestinationCode(task.Destination)
	class, ok := TripVehicleClass(task.TruckType)
	if !ok {
		return unpriced(NoVehicleClass)
	}
	bill, ok := dates.For()
	if !ok {
		return unpriced(NoBillingDate)
	}
	rate, ok := SelectRateEntry(party, hub, dest, class, bill, rates, cat)
	if !ok {
		return unpriced(NoRate)
	}
	adj := fuelFor(cat, party, bill, fuel)
	p := &TripPrice{
		PartyID:               party,
		BaseRateTHB:           rate.RateTHB,
		RateEntryID:           rate.ID,
		RateImportID:          rate.ImportID,
		LookupHubCode:         hub,
		LookupDestinationCode: dest,
		RoundProvenance:       ResolveRoundProvenance(rate.EffectiveFrom, adj),
	}
	applyFuel(p, adj)
	p.EstimateTHB = FinalRateTHB(rate.RateTHB, p.RateMultiplier, p.AddTHBPerTrip)
	return Outcome{Price: p}
}

// PriceMultiDelivery prices a multi-drop trip for one job category
// (computeMultiDeliveryBilling, billingCompute.ts:405-532). stops are the raw
// destinations of the delivered stops in completion order (not name -> code
// resolved; only the task's destination is). extraStopFeeTHB is the party's
// extra_stop service fee; nil (or negative, or NaN) selects the legacy
// per-route mode.
//
// FLAT (fee present): the base leg prices the PLANNED destination — the
// delivered stop equal to it, else the planned destination itself, else the
// first stop with a card — and every other delivered stop is charged the fee,
// without fuel. LEGACY: every stop is priced hub -> stop with fuel; stop 1 is
// the base, unmatched stops are free. Extra stops are numbered from 2 (the
// "stop 3+" comments are wrong, §6.18 #4). No base, or a base of 0, is
// no_rate.
func PriceMultiDelivery(dates BillingDates, task TaskInput, stops []string, rates []RateEntry, fuel []FuelAdjustment, extraStopFeeTHB *float64, cat JobCategory) (Outcome, error) {
	if len(stops) < 2 {
		return Outcome{}, ErrMultiInsufficientStops
	}
	if cat == "" {
		cat = Primary
	}
	party, ok := ResolveTaskParty(task.TaskParties)
	if !ok {
		return unpriced(NoCustomer), nil
	}
	hub := ExtractHubID(task.SourceHub)
	class, ok := TripVehicleClass(task.TruckType)
	if !ok {
		return unpriced(NoVehicleClass), nil
	}
	bill, ok := dates.For()
	if !ok {
		return unpriced(NoBillingDate), nil
	}
	adj := fuelFor(cat, party, bill, fuel)
	p := &TripPrice{PartyID: party, IsMultiDelivery: true, LookupHubCode: hub}
	applyFuel(p, adj)
	sel := func(dest string) (RateEntry, bool) {
		return SelectRateEntry(party, hub, dest, class, bill, rates, cat)
	}

	var base *RateEntry
	if extraStopFeeTHB != nil && *extraStopFeeTHB >= 0 {
		fee := *extraStopFeeTHB
		norm := make([]string, len(stops))
		for i, s := range stops {
			norm[i] = NormalizeDestinationCode(s)
		}
		planned := NormalizeDestinationCode(task.Destination)
		baseIdx := -1
		if planned != "" {
			for i, d := range norm {
				if d == planned {
					baseIdx = i
					break
				}
			}
		}
		baseDest := planned
		if baseIdx >= 0 {
			baseDest = norm[baseIdx]
		}
		var match RateEntry
		found := false
		if baseDest != "" {
			match, found = sel(baseDest)
		}
		if !found {
			for i, d := range norm {
				if m, ok := sel(d); ok {
					match, found, baseIdx, baseDest = m, true, i, d
					break
				}
			}
		}
		if !found {
			return unpriced(NoRate), nil
		}
		base = &match
		p.BaseRateTHB = FinalRateTHB(match.RateTHB, p.RateMultiplier, p.AddTHBPerTrip)
		p.Stops = append(p.Stops, StopCharge{StopIndex: 1, DestinationCode: baseDest, BaseRateTHB: match.RateTHB, FinalRateTHB: p.BaseRateTHB})
		exclude := 0
		if baseIdx >= 0 {
			exclude = baseIdx
		}
		seq := 2
		for i := range stops {
			if i == exclude {
				continue
			}
			p.StopChargeTHB += fee
			p.Stops = append(p.Stops, StopCharge{StopIndex: seq, DestinationCode: norm[i], BaseRateTHB: fee, FinalRateTHB: fee})
			seq++
		}
	} else {
		for i, s := range stops {
			dest := NormalizeDestinationCode(s)
			m, ok := sel(dest)
			if !ok {
				continue
			}
			final := FinalRateTHB(m.RateTHB, p.RateMultiplier, p.AddTHBPerTrip)
			if i == 0 {
				p.BaseRateTHB = final
				base = &m
			} else {
				p.StopChargeTHB += final
			}
			p.Stops = append(p.Stops, StopCharge{StopIndex: i + 1, DestinationCode: dest, BaseRateTHB: m.RateTHB, FinalRateTHB: final})
		}
	}
	if p.BaseRateTHB == 0 || len(p.Stops) == 0 || base == nil {
		return unpriced(NoRate), nil
	}
	p.EstimateTHB = p.BaseRateTHB + p.StopChargeTHB
	p.RateEntryID, p.RateImportID = base.ID, base.ImportID
	p.LookupDestinationCode = p.Stops[0].DestinationCode
	p.RoundProvenance = ResolveRoundProvenance(base.EffectiveFrom, adj)
	return Outcome{Price: p}, nil
}

// Tables are the pricing inputs of the trip's billing party, read by the
// caller inside its pricing transaction (never from a cache, R17, R53).
type Tables struct {
	Basis           BillingDateBasis
	Rates           []RateEntry
	Fuel            []FuelAdjustment
	ExtraStopFeeTHB *float64 // customer_service_fees fee_type='extra_stop'; nil = none
	Hubs            HubMaps
}

// TripInput is one delivered trip and its task.
type TripInput struct {
	Task TaskInput
	// JobCategory is the task's own category: PRIMARY or SUPPLEMENTARY is
	// explicit and authoritative; anything else is a legacy task.
	JobCategory string
	PlanAt      time.Time // tasks.plan_at
	DeliveredAt time.Time // trip_records.delivered_at
	CreatedAt   time.Time // trip_records.created_at
	// IsMultiDelivery and StopProgressCount gate the multi-drop path (flag
	// set and at least two progress entries); DeliveredStops are the raw
	// destinations of the stops with a destination and status delivered, in
	// completion order.
	IsMultiDelivery   bool
	StopProgressCount int
	DeliveredStops    []string
}

// TripResult is the outcome of pricing a trip: a price, or an unpriced
// reason, plus the axis columns trip_records carries either way.
type TripResult struct {
	PartyID     string    // resolved billing party; "" with no_customer
	BillingDate time.Time // stored billing_date axis; zero = NULL
	Price       *TripPrice
	Reason      UnpricedReason // set iff Price == nil
}

// PriceTrip is the pure part of PriceTrip (§6.10 states 5 and 7): the server
// pricing rule of fn:tripBillingOnDelivered.ts:321-603 with R15 and R19.
//
//   - the task's hub and destination are resolved name -> code (never
//     code -> name, §6.4);
//   - a task with an explicit category prices that category only (no
//     fallback); a legacy task tries PRIMARY, then SUPPLEMENTARY (§6.7);
//   - a single trip that finds no card retries once with the Thai display
//     name of its destination (cards keyed by name, §6.8);
//   - a SUPPLEMENTARY price is a manual override (frozen).
//
// It returns ErrMultiInsufficientStops for a gated multi-drop trip with fewer
// than two delivered stops. Freezing, period locks and persistence are the
// caller's.
func PriceTrip(in TripInput, t Tables) (TripResult, error) {
	dates := BillingDates{Basis: t.Basis, PlanAt: in.PlanAt, DeliveredAt: in.DeliveredAt, CreatedAt: in.CreatedAt}
	res := TripResult{}
	res.BillingDate, _ = dates.Stored()
	party, ok := ResolveTaskParty(in.Task.TaskParties)
	if !ok {
		res.Reason = NoCustomer
		return res, nil
	}
	res.PartyID = party

	rawDest := in.Task.Destination
	task := in.Task
	task.SourceHub = t.Hubs.ResolveNameToCode(in.Task.SourceHub)
	task.Destination = t.Hubs.ResolveNameToCode(rawDest)

	multi := in.IsMultiDelivery && in.StopProgressCount >= 2
	if multi && len(in.DeliveredStops) < 2 {
		return res, ErrMultiInsufficientStops
	}

	priceFor := func(cat JobCategory) Outcome {
		if multi {
			o, _ := PriceMultiDelivery(dates, task, in.DeliveredStops, t.Rates, t.Fuel, t.ExtraStopFeeTHB, cat)
			return o
		}
		o := PriceLeg(dates, task, t.Rates, t.Fuel, cat)
		if o.Reason != NoRate || rawDest == "" {
			return o
		}
		alt, found := t.Hubs.CodeToName(trim(task.Destination))
		if !found {
			alt, found = t.Hubs.CodeToName(trim(rawDest))
		}
		if !found || alt == task.Destination {
			return o
		}
		retry := task
		retry.Destination = alt
		return PriceLeg(dates, retry, t.Rates, t.Fuel, cat)
	}

	cats := []JobCategory{Primary, Supplementary}
	if explicit, ok := ExplicitJobCategory(in.JobCategory); ok {
		cats = []JobCategory{explicit}
	}
	for _, cat := range cats {
		o := priceFor(cat)
		if o.Price != nil {
			o.Price.JobCategory = cat
			o.Price.ManualOverride = cat == Supplementary
			res.Price = o.Price
			return res, nil
		}
		if o.Reason != NoRate {
			// no_customer, no_vehicle_class and no_billing_date do not depend
			// on the category: the next category cannot price either.
			res.Reason = o.Reason
			return res, nil
		}
	}
	res.Reason = NoRate
	return res, nil
}
