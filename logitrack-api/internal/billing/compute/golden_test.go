package compute_test

import (
	"errors"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/golden"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// TestGolden runs the vectors exported from the TypeScript engine
// (testdata/golden/billing, main spec §6.16). The ported-case counts pin the
// acceptance criterion "every case ported": they equal the `it` blocks of the
// Vitest files at commit 4f552099.
func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file   string
		ported int
	}{
		{"billing/billingCompute.json", 49},
		{"billing/billingRates.json", 5},
		{"billing/billingPeriodLock.json", 12},
		{"billing/characterisation.json", 0},
	} {
		t.Run(tc.file, func(t *testing.T) {
			f := golden.Load(t, tc.file)
			if got := f.Ported(); got != tc.ported {
				t.Fatalf("%d ported cases, want %d", got, tc.ported)
			}
			golden.Run(t, f, adapters)
		})
	}
}

var adapters = map[string]golden.Func{
	"normalizeVehicleClass": func(_ testing.TB, a []any) golden.Result {
		class, ok := compute.FoldVehicleClass(golden.Str(a[0]))
		if !ok {
			return golden.Result{Value: nil, Reason: string(compute.NoVehicleClass)}
		}
		return golden.Result{Value: class}
	},
	"extractHubId": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.ExtractHubID(golden.Str(a[0]))}
	},
	"normalizeDestinationCode": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.NormalizeDestinationCode(golden.Str(a[0]))}
	},
	"computeFinalRateThb": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.FinalRateTHB(golden.Num(a[0]), golden.Num(a[1]), golden.Num(a[2]))}
	},
	"fuelBandFloor": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.FuelBandFloor(golden.Num(a[0]))}
	},
	"fuelBandRange": func(_ testing.TB, a []any) golden.Result {
		lower, upper, ok := compute.FuelBandRange(golden.Num(a[0]))
		if !ok {
			return golden.Result{Value: nil}
		}
		return golden.Result{Value: golden.Object("lowerThb", lower, "upperThb", upper)}
	},
	"computeFuelSurchargeThb": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.FuelSurchargeTHB(golden.Num(a[0]), golden.Num(a[1]), golden.Num(a[2]))}
	},
	"bangkokDateStrFromMillis": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: clock.DateString(epoch(a[0]))}
	},
	"isEffectiveOnOrBeforeBillingDate": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.IsEffectiveOn(epoch(a[0]), epoch(a[1]))}
	},
	"selectBillingRateEntry": func(_ testing.TB, a []any) golden.Result {
		e, ok := compute.SelectRateEntry(golden.Str(a[0]), golden.Str(a[1]), golden.Str(a[2]), golden.Str(a[3]),
			epoch(a[4]), rateEntries(a[5]), compute.JobCategory(golden.Str(a[6])))
		return idOrNil(e.ID, ok)
	},
	"selectFuelAdjustmentForBillingDate": func(_ testing.TB, a []any) golden.Result {
		f, ok := compute.SelectFuelAdjustment(golden.Str(a[0]), epoch(a[1]), fuelAdjustments(a[2]))
		return idOrNil(f.ID, ok)
	},
	"selectStandbyRateEntry": func(_ testing.TB, a []any) golden.Result {
		r, ok := compute.SelectStandbyRate(golden.Str(a[0]), epoch(a[1]), standbyRates(a[2]))
		return idOrNil(r.ID, ok)
	},
	"computeStandbyBilling": func(_ testing.TB, a []any) golden.Result {
		party := golden.Str(a[1])
		if party == "" {
			return golden.Result{Value: nil, Reason: string(compute.NoCustomer)}
		}
		r, ok := compute.SelectStandbyRate(party, epoch(a[0]), standbyRates(a[2]))
		if !ok {
			return golden.Result{Value: nil, Reason: string(compute.NoRate)}
		}
		return golden.Result{Value: golden.Object("customerId", party, "rateThb", r.RateTHB, "rateEntryId", r.ID,
			"effectiveFromDateStr", clock.DateString(r.EffectiveFrom))}
	},
	"resolveBillingRoundProvenance": func(_ testing.TB, a []any) golden.Result {
		var adj *compute.FuelAdjustment
		if a[1] != nil {
			f := fuelAdjustment(golden.Obj(a[1]))
			adj = &f
		}
		return golden.Result{Value: provenance(compute.ResolveRoundProvenance(epoch(a[0]), adj))}
	},
	"computeTripBillingFromParts": func(_ testing.TB, a []any) golden.Result {
		if a[1] == nil {
			return golden.Result{Value: nil}
		}
		o := compute.PriceLeg(tripDates(golden.Obj(a[0])), taskInput(golden.Obj(a[1])), rateEntries(a[2]), fuelAdjustments(a[3]),
			compute.JobCategory(golden.Str(a[4])))
		return singleResult(o)
	},
	"computeMultiDeliveryBilling": func(_ testing.TB, a []any) golden.Result {
		task := taskInput(golden.Obj(a[1]))
		task.TruckType = golden.Str(a[3])
		var stops []string
		for _, s := range golden.Arr(a[2]) {
			stops = append(stops, golden.Str(golden.Obj(s)["destination"]))
		}
		var fee *float64
		if a[6] != nil {
			f := golden.Num(a[6])
			fee = &f
		}
		o, err := compute.PriceMultiDelivery(tripDates(golden.Obj(a[0])), task, stops, rateEntries(a[4]), fuelAdjustments(a[5]), fee,
			compute.JobCategory(golden.Str(a[7])))
		if errors.Is(err, compute.ErrMultiInsufficientStops) {
			return golden.Result{Value: nil, Reason: err.Error()}
		}
		if o.Price == nil {
			return golden.Result{Value: nil, Reason: string(o.Reason)}
		}
		p := o.Price
		stopsOut := make([]any, len(p.Stops))
		for i, s := range p.Stops {
			stopsOut[i] = golden.Object("stopIndex", s.StopIndex, "destination", s.DestinationCode, "baseRateThb", s.BaseRateTHB, "finalRateThb", s.FinalRateTHB)
		}
		return golden.Result{Value: golden.Object(
			"customerId", p.PartyID,
			"baseRateThb", p.BaseRateTHB,
			"stopChargeThb", p.StopChargeTHB,
			"totalBillingThb", p.EstimateTHB,
			"stopBreakdown", stopsOut,
			"rateMultiplier", p.RateMultiplier,
			"fuelAdjustmentId", nonEmpty(p.FuelAdjustmentID),
			"addThbPerTrip", p.AddTHBPerTrip,
			"rateImportId", p.RateImportID,
			"effectiveFromDateStr", nonEmpty(p.FuelEffectiveFromDate),
			"roundEffectiveFromDateStr", p.RoundEffectiveFromDate,
			"fuelBandLowerThb", golden.Opt(p.FuelBandLowerTHB),
			"fuelBandUpperThb", golden.Opt(p.FuelBandUpperTHB),
			"referenceFuelPriceThb", golden.Opt(p.ReferenceFuelPriceTHB),
		)}
	},
	"getTripBillingDateMs": func(_ testing.TB, a []any) golden.Result {
		bill, ok := tripDates(golden.Obj(a[0])).For()
		if !ok {
			return golden.Result{Value: nil, Reason: string(compute.NoBillingDate)}
		}
		return golden.Result{Value: float64(bill.UnixMilli())}
	},
	"resolveTaskCustomerId": func(_ testing.TB, a []any) golden.Result {
		party, _ := compute.ResolveTaskParty(taskInput(golden.Obj(a[0])).TaskParties)
		return golden.Result{Value: party}
	},
	"snapshotCarriesFuel": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.CarriesFuel(snapshot(golden.Obj(a[0])))}
	},
	"isFrozenBillingSnapshot": func(_ testing.TB, a []any) golden.Result {
		m := golden.Obj(a[0])
		return golden.Result{Value: compute.IsFrozen(snapshot(m), compute.JobCategory(golden.Str(m["jobCategory"])))}
	},
	"computeTripBilling": func(_ testing.TB, a []any) golden.Result {
		trip, task := golden.Obj(a[0]), golden.Obj(a[1])
		res, err := compute.PriceTrip(compute.TripInput{
			Task:        taskInput(task),
			JobCategory: golden.Str(task["jobCategory"]),
			DeliveredAt: golden.Date(trip["deliveredTimestamp"]),
			CreatedAt:   golden.Date(trip["createdAt"]),
		}, compute.Tables{Basis: compute.BasisDelivered, Rates: rateEntries(a[2]), Fuel: fuelAdjustments(a[3])})
		if err != nil {
			return golden.Result{Value: nil, Reason: err.Error()}
		}
		return singleResult(compute.Outcome{Price: res.Price, Reason: res.Reason})
	},
	"billingPeriodKey": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.PeriodKey(golden.Str(a[0]), golden.Int(a[1]), golden.Int(a[2]))}
	},
	"bangkokYearMonth": func(_ testing.TB, a []any) golden.Result {
		y, m := compute.PeriodOf(epoch(a[0]))
		return golden.Result{Value: golden.Object("year", y, "month", m)}
	},
	"lockFor": func(_ testing.TB, a []any) golden.Result {
		var periods []compute.LockedPeriod
		for _, l := range golden.Arr(a[0]) {
			m := golden.Obj(l)
			periods = append(periods, compute.LockedPeriod{
				PartyID: golden.Str(m["customerId"]), Year: golden.Int(m["year"]), Month: golden.Int(m["month"]),
				InvoiceNumber: golden.Str(m["invoiceNumber"]), Status: golden.Str(m["status"]),
			})
		}
		lock, ok := compute.NewPeriodLocks(periods).LockFor(golden.Str(a[1]), epoch(a[2]))
		if !ok {
			return golden.Result{Value: nil}
		}
		return golden.Result{Value: golden.Object("customerId", lock.PartyID, "year", lock.Year, "month", lock.Month,
			"invoiceNumber", lock.InvoiceNumber, "status", lock.Status)}
	},
	"Math.round": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: jsmath.Round(golden.Num(a[0]))}
	},
	"round2": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: jsmath.Round2(golden.Num(a[0]))}
	},
	"toFixed": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: jsmath.ToFixed(golden.Num(a[0]), golden.Int(a[1]))}
	},
	"withholdingThb": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.WithholdingTHB(golden.Num(a[0]), golden.Num(a[1]))}
	},
}

// epoch reads a millisecond instant; unlike golden.Millis it keeps 0 as the
// epoch, which the calendar vectors use as a real instant.
func epoch(v any) time.Time {
	if v == nil {
		return time.Time{}
	}
	return time.UnixMilli(int64(golden.Num(v))).UTC()
}

func idOrNil(id string, ok bool) golden.Result {
	if !ok {
		return golden.Result{Value: nil}
	}
	return golden.Result{Value: id}
}

func nonEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func order(m map[string]any) (legacy string, created time.Time) {
	return golden.Str(m["legacyDocId"]), golden.Millis(m["createdAtMs"])
}

func rateEntries(v any) []compute.RateEntry {
	var out []compute.RateEntry
	for _, x := range golden.Arr(v) {
		m := golden.Obj(x)
		legacy, created := order(m)
		out = append(out, compute.RateEntry{
			ID: golden.Str(m["id"]), LegacyDocID: legacy, CreatedAt: created,
			PartyID: golden.Str(m["customerId"]), ImportID: golden.Str(m["importId"]),
			HubCode: golden.Str(m["hubId"]), DestinationCode: golden.Str(m["destinationCode"]),
			VehicleClass: golden.Str(m["vehicleClass"]), RateTHB: golden.Num(m["rateThb"]),
			EffectiveFrom: epoch(m["effectiveFromMs"]), JobCategory: compute.JobCategory(golden.Str(m["jobCategory"])),
			Voided: golden.Bool(m["voided"]),
		})
	}
	return out
}

func fuelAdjustment(m map[string]any) compute.FuelAdjustment {
	legacy, created := order(m)
	return compute.FuelAdjustment{
		ID: golden.Str(m["id"]), LegacyDocID: legacy, CreatedAt: created,
		PartyID: golden.Str(m["customerId"]), EffectiveFrom: epoch(m["effectiveFromMs"]),
		RateMultiplier: golden.Num(m["rateMultiplier"]), AddTHBPerTrip: golden.Num(m["addThbPerTrip"]),
		ReferenceFuelPriceTHB: golden.OptNum(m, "referenceFuelPriceThb"), Voided: golden.Bool(m["voided"]),
	}
}

func fuelAdjustments(v any) []compute.FuelAdjustment {
	var out []compute.FuelAdjustment
	for _, x := range golden.Arr(v) {
		out = append(out, fuelAdjustment(golden.Obj(x)))
	}
	return out
}

func standbyRates(v any) []compute.StandbyRate {
	var out []compute.StandbyRate
	for _, x := range golden.Arr(v) {
		m := golden.Obj(x)
		legacy, created := order(m)
		out = append(out, compute.StandbyRate{
			ID: golden.Str(m["id"]), LegacyDocID: legacy, CreatedAt: created,
			PartyID: golden.Str(m["customerId"]), RateTHB: golden.Num(m["rateThb"]),
			EffectiveFrom: epoch(m["effectiveFromMs"]), Voided: golden.Bool(m["voided"]),
		})
	}
	return out
}

// tripDates maps the legacy TripBillingTimestamps: an explicit billingDateMs
// override is the plan instant of a plan-basis party.
func tripDates(m map[string]any) compute.BillingDates {
	d := compute.BillingDates{
		Basis:       compute.BasisDelivered,
		DeliveredAt: golden.Millis(m["deliveredTimestamp"]),
		CreatedAt:   golden.Millis(m["createdAt"]),
	}
	if plan := golden.Millis(m["billingDateMs"]); !plan.IsZero() {
		d.Basis, d.PlanAt = compute.BasisPlan, plan
	}
	return d
}

func taskInput(m map[string]any) compute.TaskInput {
	return compute.TaskInput{
		TaskParties: compute.TaskParties{
			BillingPartyID:           golden.Str(m["billingCustomerId"]),
			SourceLinkedPartyID:      golden.Str(m["sourceHubLinkedCustomerId"]),
			DestinationLinkedPartyID: golden.Str(m["destinationLinkedCustomerId"]),
		},
		SourceHub:   golden.Str(m["sourceHub"]),
		Destination: golden.Str(m["destination"]),
		TruckType:   golden.Str(m["truckType"]),
	}
}

func snapshot(m map[string]any) compute.Snapshot {
	return compute.Snapshot{
		FuelAdjustmentID: golden.OptStr(m, "billingFuelAdjustmentId"),
		RateMultiplier:   golden.OptNum(m, "billingRateMultiplier"),
		AddTHBPerTrip:    golden.OptNum(m, "billingAddThbPerTrip"),
		ManualOverride:   golden.Bool(m["billingManualOverride"]),
	}
}

func provenance(p compute.RoundProvenance) map[string]any {
	return golden.Object(
		"roundEffectiveFromDateStr", p.RoundEffectiveFromDate,
		"fuelBandLowerThb", golden.Opt(p.FuelBandLowerTHB),
		"fuelBandUpperThb", golden.Opt(p.FuelBandUpperTHB),
		"referenceFuelPriceThb", golden.Opt(p.ReferenceFuelPriceTHB),
	)
}

// singleResult shapes a single-trip price like TripBillingComputed.
func singleResult(o compute.Outcome) golden.Result {
	if o.Price == nil {
		return golden.Result{Value: nil, Reason: string(o.Reason)}
	}
	p := o.Price
	v := golden.Object(
		"customerId", p.PartyID,
		"baseRateThb", p.BaseRateTHB,
		"finalRateThb", p.EstimateTHB,
		"rateImportId", p.RateImportID,
		"lookupHubId", p.LookupHubCode,
		"lookupDestination", p.LookupDestinationCode,
		"fuelAdjustmentId", nonEmpty(p.FuelAdjustmentID),
		"rateMultiplier", p.RateMultiplier,
		"addThbPerTrip", p.AddTHBPerTrip,
		"effectiveFromDateStr", nonEmpty(p.FuelEffectiveFromDate),
	)
	for k, x := range provenance(p.RoundProvenance) {
		v[k] = x
	}
	return golden.Result{Value: v}
}
