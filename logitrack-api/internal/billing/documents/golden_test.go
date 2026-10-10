package documents_test

import (
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/documents"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/golden"
)

// TestGolden runs testdata/golden/billing/billingDocument.json: the 16 cases
// of logitrack-web/lib/billingDocument.test.ts plus characterisation cases.
func TestGolden(t *testing.T) {
	f := golden.Load(t, "billing/billingDocument.json")
	if got := f.Ported(); got != 16 {
		t.Fatalf("%d ported cases, want 16", got)
	}
	golden.Run(t, f, map[string]golden.Func{
		"collectBillingRounds": func(_ testing.TB, a []any) golden.Result {
			return golden.Result{Value: roundsOut(documents.CollectRounds(rows(a[0])))}
		},
		"billingAxisDate": func(_ testing.TB, a []any) golden.Result {
			d, ok := documents.AxisDate(row(golden.Obj(a[0])))
			if !ok {
				return golden.Result{Value: nil}
			}
			return golden.Result{Value: d}
		},
		"billingDateBasisOf": func(_ testing.TB, a []any) golden.Result {
			return golden.Result{Value: string(documents.BasisOf(rows(a[0])))}
		},
		"formatFuelBand": func(_ testing.TB, a []any) golden.Result {
			return golden.Result{Value: documents.FormatFuelBand(optNum(a[0]), optNum(a[1]))}
		},
		"groupToLineItems": func(_ testing.TB, a []any) golden.Result {
			var rounds []documents.Round
			if a[1] != nil {
				rounds = documents.CollectRounds(rows(a[1]))
			}
			items := documents.GroupToLineItems(rows(a[0]), rounds)
			out := make([]any, len(items))
			for i, it := range items {
				dates := make([]any, len(it.Dates))
				for j, d := range it.Dates {
					dates[j] = d
				}
				out[i] = golden.Object("vehicleClass", it.VehicleClass, "route", it.Route, "count", it.Count,
					"unitPrice", it.UnitPrice, "total", it.Total, "dates", dates, "enumerateDays", it.EnumerateDays,
					"roundLabel", it.RoundLabel)
			}
			return golden.Result{Value: out}
		},
	})
}

func optNum(v any) *float64 {
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	return &f
}

func rows(v any) []documents.Row {
	var out []documents.Row
	for _, x := range golden.Arr(v) {
		out = append(out, row(golden.Obj(x)))
	}
	return out
}

func row(m map[string]any) documents.Row {
	return documents.Row{
		ID:                     golden.Str(m["id"]),
		TripRecordID:           golden.Str(m["tripRecordId"]),
		DeliveredAt:            golden.Date(m["deliveredTimestamp"]),
		BillingDate:            golden.Date(m["billingDate"]),
		BillingDateBasis:       documents.DateBasis(golden.Str(m["billingDateBasis"])),
		EstimateTHB:            golden.Num(m["billingEstimateThb"]),
		LookupHubCode:          golden.Str(m["billingLookupHubId"]),
		LookupDestinationCode:  golden.OptStr(m, "billingLookupDestination"),
		AddTHBPerTrip:          golden.OptNum(m, "billingAddThbPerTrip"),
		VehicleClass:           golden.OptStr(m, "vehicleClass"),
		HubDisplayName:         golden.Str(m["hubDisplayName"]),
		OriginHubCode:          golden.Str(m["originHubCode"]),
		DestinationDisplayName: golden.OptStr(m, "destinationDisplayName"),
		Type:                   documents.RowType(golden.Str(m["rowType"])),
		StopIndex:              golden.Int(m["stopIndex"]),
		JobCategory:            golden.Str(m["jobCategory"]),
		RoundEffectiveFromDate: golden.Str(m["billingRoundEffectiveFromDateStr"]),
		FuelBandLowerTHB:       golden.OptNum(m, "billingFuelBandLowerThb"),
		FuelBandUpperTHB:       golden.OptNum(m, "billingFuelBandUpperThb"),
		ReferenceFuelPriceTHB:  golden.OptNum(m, "billingReferenceFuelPriceThb"),
	}
}

func roundsOut(rs []documents.Round) []any {
	out := make([]any, len(rs))
	for i, r := range rs {
		o := golden.Object("label", r.Label, "effectiveFromDateStr", r.EffectiveFromDate,
			"fuelBandLowerThb", golden.Opt(r.FuelBandLowerTHB), "fuelBandUpperThb", golden.Opt(r.FuelBandUpperTHB),
			"addThbPerTrip", golden.Opt(r.AddTHBPerTrip))
		if !r.FirstBillingDate.IsZero() {
			o["firstBillingDate"] = r.FirstBillingDate
		}
		if !r.LastBillingDate.IsZero() {
			o["lastBillingDate"] = r.LastBillingDate
		}
		out[i] = o
	}
	return out
}
