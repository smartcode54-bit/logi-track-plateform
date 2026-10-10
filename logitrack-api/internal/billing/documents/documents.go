// Package documents holds the pure layout rules of the billing documents
// (invoice, receipt, Excel detail): which date a row is billed on, the price
// rounds of a period and the invoice line items (main spec §6.14, ADR 0009
// §6-7, ADR 0027). Port of the pure part of web:lib/billingDocument.ts; the
// renderers (PDF, Excel) arrive with the server-side documents (T39).
//
// Money sums stay unrounded float sums in row order, as the browser computed
// them (§6.2); every product would be wrapped in float64(...) (there are
// none), enforced with the billing engine by TestNoFusedMultiplyAdd.
package documents

import (
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// DateBasis is the axis a billing party closes its periods on (ADR 0027).
type DateBasis string

const (
	BasisDelivered DateBasis = "delivered"
	BasisPlan      DateBasis = "plan"
)

// RowType is the kind of billed row.
type RowType string

const (
	RowTrip          RowType = "trip"
	RowMultidropStop RowType = "multidrop_stop" // one extra stop of a multi-drop trip
	RowStandby       RowType = "standby"        // จอดรอ
)

// Row is one billed row of a statement (BillingTripRow,
// web:lib/billingDocument.ts:43-110), as GET /v1/billing/rows returns it.
// Fields a legacy `??` reads (absent and empty differ) are pointers.
type Row struct {
	ID           string
	TripRecordID string
	DeliveredAt  time.Time // zero = absent
	// BillingDate is trip_records.billing_date, frozen when the row was
	// priced; absent on standby and on trips priced before ADR 0027. Read it
	// through AxisDate, never raw.
	BillingDate time.Time
	// BillingDateBasis is stamped only when a single party was requested.
	BillingDateBasis       DateBasis
	EstimateTHB            float64
	LookupHubCode          string
	LookupDestinationCode  *string
	AddTHBPerTrip          *float64
	VehicleClass           *string
	HubDisplayName         string
	OriginHubCode          string // the hub CODE the invoice prints as origin
	DestinationDisplayName *string
	Type                   RowType // "" is a trip
	StopIndex              int
	JobCategory            string
	// Round + fuel band denormalised onto the row when it was priced
	// (ADR 0009 §4); "" / nil on legacy rows.
	RoundEffectiveFromDate string
	FuelBandLowerTHB       *float64
	FuelBandUpperTHB       *float64
	ReferenceFuelPriceTHB  *float64
}

// AxisDate is the date a row belongs to its statement by (billingAxisDate,
// web:lib/billingDocument.ts:124-126): the frozen billing date when the row
// has one, else the delivery instant (rows priced before ADR 0027, standby).
// Every date a document prints is read on this axis, or a plan-basis
// customer's invoice prints the neighbouring month's delivery dates.
func AxisDate(r Row) (time.Time, bool) {
	if !r.BillingDate.IsZero() {
		return r.BillingDate, true
	}
	if !r.DeliveredAt.IsZero() {
		return r.DeliveredAt, true
	}
	return time.Time{}, false
}

// BasisOf is the axis a set of rows was built on (billingDateBasisOf,
// web:lib/billingDocument.ts:136-138): plan when any row says so, else
// delivered — nothing stamped keeps every customer who never opted in on the
// delivered default.
func BasisOf(rows []Row) DateBasis {
	for _, r := range rows {
		if r.BillingDateBasis == BasisPlan {
			return BasisPlan
		}
	}
	return BasisDelivered
}

// Round is one price round present in a period: the invoice legend
// (BillingRound, web:lib/billingDocument.ts:141-152).
type Round struct {
	Label             string // R1, R2, ... by date order; derived, never stored
	EffectiveFromDate string // yyyy-MM-dd
	FuelBandLowerTHB  *float64
	FuelBandUpperTHB  *float64
	AddTHBPerTrip     *float64
	// The round's span inside the period on the billing axis; zero = none.
	FirstBillingDate time.Time
	LastBillingDate  time.Time
}

// CollectRounds lists the distinct rounds of a period's rows, oldest first,
// labelled R1.. (collectBillingRounds, web:lib/billingDocument.ts:160-187).
// Rows without a round are ignored, so legacy trips cannot invent one; the
// band and add of a round come from its first row; the span widens over every
// row's axis date.
func CollectRounds(rows []Row) []Round {
	var rounds []Round
	index := map[string]int{}
	for _, r := range rows {
		key := r.RoundEffectiveFromDate
		if key == "" {
			continue
		}
		d, hasDate := AxisDate(r)
		if i, ok := index[key]; ok {
			if hasDate {
				ex := &rounds[i]
				if ex.FirstBillingDate.IsZero() || d.Before(ex.FirstBillingDate) {
					ex.FirstBillingDate = d
				}
				if ex.LastBillingDate.IsZero() || d.After(ex.LastBillingDate) {
					ex.LastBillingDate = d
				}
			}
			continue
		}
		index[key] = len(rounds)
		rounds = append(rounds, Round{
			EffectiveFromDate: key,
			FuelBandLowerTHB:  r.FuelBandLowerTHB,
			FuelBandUpperTHB:  r.FuelBandUpperTHB,
			AddTHBPerTrip:     r.AddTHBPerTrip,
			FirstBillingDate:  d,
			LastBillingDate:   d,
		})
	}
	slices.SortStableFunc(rounds, func(a, b Round) int {
		switch {
		case a.EffectiveFromDate < b.EffectiveFromDate:
			return -1
		case a.EffectiveFromDate > b.EffectiveFromDate:
			return 1
		}
		return 0
	})
	for i := range rounds {
		rounds[i].Label = "R" + strconv.Itoa(i+1)
	}
	return rounds
}

// FormatFuelBand prints a band the way the contract is written,
// "37.01–38.00", or "-" when the row carries no band (formatFuelBand,
// web:lib/billingDocument.ts:190-193). Numbers use JavaScript toFixed(2).
func FormatFuelBand(lower, upper *float64) string {
	if lower == nil || upper == nil {
		return "-"
	}
	return jsmath.ToFixed(*lower, 2) + "–" + jsmath.ToFixed(*upper, 2)
}

// MultidropRoute is the single invoice line every multi-drop extra stop is
// grouped under (ค่าโยก, §6.18 #5).
const MultidropRoute = "ค่าโยก"

// LineItem is one line of the invoice body.
type LineItem struct {
	VehicleClass  string
	Route         string
	Count         int
	UnitPrice     float64
	Total         float64 // unrounded float sum of the rows, in row order
	Dates         []time.Time
	EnumerateDays bool   // standby lists each day; others print a range
	RoundLabel    string // "" when the rows carry no round
}

type lineKey struct {
	vehicleClass, route string
	unitPrice           uint64 // float64 bits, -0 and NaN canonical
	round               string
}

// canonicalBits keys a price the way the legacy string key did: -0 and +0
// are one price, every NaN is one price.
func canonicalBits(x float64) uint64 {
	if x == 0 {
		return 0
	}
	if math.IsNaN(x) {
		return math.Float64bits(math.NaN())
	}
	return math.Float64bits(x)
}

// GroupToLineItems groups rows into invoice lines by vehicle class, route,
// unit price and price round (groupToLineItems,
// web:lib/billingDocument.ts:289-338). The round is part of the key, so a line
// never spans two rounds even at an identical price, and count × unitPrice =
// total holds on every line. Origin prints the hub CODE, destination the
// display NAME; standby lines are marked "(Stand by)" and list their days;
// every multi-drop stop goes under ค่าโยก. Dates are axis dates. Lines keep
// the order their first row appeared in.
func GroupToLineItems(rows []Row, rounds []Round) []LineItem {
	labels := make(map[string]string, len(rounds))
	for _, r := range rounds {
		labels[r.EffectiveFromDate] = r.Label
	}
	var items []LineItem
	index := map[lineKey]int{}
	for _, r := range rows {
		isStandby := r.Type == RowStandby
		isStop := r.Type == RowMultidropStop
		vc := "-"
		if r.VehicleClass != nil {
			vc = *r.VehicleClass
		}
		origin := firstNonEmpty(r.OriginHubCode, r.LookupHubCode, r.HubDisplayName, "-")
		dest := "-"
		switch {
		case r.DestinationDisplayName != nil:
			dest = *r.DestinationDisplayName
		case r.LookupDestinationCode != nil:
			dest = *r.LookupDestinationCode
		}
		route := origin + " → " + dest
		switch {
		case isStandby:
			route += " (Stand by)"
		case isStop:
			route = MultidropRoute
		}
		k := lineKey{vc, route, canonicalBits(r.EstimateTHB), r.RoundEffectiveFromDate}
		d, hasDate := AxisDate(r)
		if i, ok := index[k]; ok {
			it := &items[i]
			it.Count++
			it.Total += r.EstimateTHB
			if hasDate {
				it.Dates = append(it.Dates, d)
			}
			continue
		}
		it := LineItem{
			VehicleClass:  vc,
			Route:         route,
			Count:         1,
			UnitPrice:     r.EstimateTHB,
			Total:         r.EstimateTHB,
			Dates:         []time.Time{},
			EnumerateDays: isStandby,
			RoundLabel:    labels[r.RoundEffectiveFromDate],
		}
		if hasDate {
			it.Dates = append(it.Dates, d)
		}
		index[k] = len(items)
		items = append(items, it)
	}
	return items
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
