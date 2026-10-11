package compute_test

import (
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/golden"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/compute"
)

// TestGolden runs the vectors exported from the TypeScript engine (testdata/golden/hr, main spec §6.16). The ported
// count pins the acceptance criterion "lib/compensationCompute.test.ts ported and green": it equals the `it` blocks of
// the Vitest file at commit 4f552099.
func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file   string
		ported int
	}{
		{"hr/compensationCompute.json", 29},
		{"hr/characterisation.json", 0},
	} {
		t.Run(tc.file, func(t *testing.T) {
			f := golden.Load(t, tc.file)
			if got := f.Ported(); got != tc.ported {
				t.Fatalf("%d ported cases, want %d", got, tc.ported)
			}
			if len(f.Cases) == 0 {
				t.Fatal("no cases")
			}
			golden.Run(t, f, adapters)
		})
	}
}

var adapters = map[string]golden.Func{
	"roundTHB": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.RoundTHB(golden.Num(a[0]))}
	},
	"assignRound": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: string(compute.AssignRound(golden.Date(a[0])))}
	},
	"bangkokDateKey": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.DateKey(golden.Date(a[0]))}
	},
	"isHoliday": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.HolidayChecker(keySet(a[0]))(golden.Date(a[1]))}
	},
	"computeBasePay": func(_ testing.TB, a []any) golden.Result {
		var trips []compute.Trip
		for _, x := range golden.Arr(a[0]) {
			m := golden.Obj(x)
			trips = append(trips, compute.Trip{DeliveredAt: golden.Date(m["deliveredAt"]), Standby: golden.Bool(m["isStandby"]),
				MultiStop: golden.Bool(m["isMultiStop"])})
		}
		c := golden.Obj(a[1])
		cfg := compute.BasePayConfig{WeekdayRateTHB: golden.Num(c["weekdayRateThb"]), HolidayRateTHB: golden.Num(c["holidayRateThb"]),
			PayStandby: golden.Bool(c["payStandby"])}
		h := golden.Obj(a[2])
		isHoliday := compute.HolidayChecker(keySet(h["keys"]))
		if golden.Bool(h["never"]) {
			isHoliday = func(time.Time) bool { return false }
		}
		r := compute.BasePay(trips, cfg, isHoliday)
		return golden.Result{Value: golden.Object("weekdayTrips", r.WeekdayTrips, "holidayTrips", r.HolidayTrips,
			"excludedTrips", r.ExcludedTrips, "basePayThb", r.BasePayTHB)}
	},
	"computeFuelIncentive": func(_ testing.TB, a []any) golden.Result {
		var km *float64
		if a[0] != nil {
			v := golden.Num(a[0])
			km = &v
		}
		amount, eligible := compute.FuelIncentive(km, golden.Int(a[1]), tiers(a[2]), golden.Int(a[3]))
		return golden.Result{Value: golden.Object("amountThb", amount, "eligible", eligible)}
	},
	"computeTripVolumeIncentive": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.TripVolumeIncentive(golden.Int(a[0]), tiers(a[1]))}
	},
	"computeSso": func(_ testing.TB, a []any) golden.Result {
		d := golden.Obj(a[0])
		r := golden.Obj(a[1])
		rule := compute.SSORule{RatePercent: golden.Num(r["ratePercent"]), BaseExistingTHB: golden.Num(r["baseExistingThb"]),
			BaseNewTHB: golden.Num(r["baseNewThb"]), ExistingHiredBeforeYear: golden.Int(r["existingHiredBeforeYear"]),
			MaxAgeInclusive: golden.Int(r["maxAgeInclusive"])}
		return golden.Result{Value: compute.SSO(compute.SSODriver{HireDate: optDate(d["hireDate"]), BirthDate: optDate(d["birthDate"]),
			ProbationPassed: golden.Bool(d["probationPassed"])}, rule, golden.Date(a[2]))}
	},
	"computeHelperPay": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.HelperPay(golden.Int(a[0]), golden.Num(a[1]))}
	},
	"helperWindowKey": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.HelperWindowKey(golden.Date(a[0]))}
	},
	"eligibleHelperDayKeys": func(_ testing.TB, a []any) golden.Result {
		var keys []string
		for _, k := range golden.Arr(a[0]) {
			keys = append(keys, golden.Str(k))
		}
		out := compute.EligibleHelperDayKeys(keys, keySet(a[1]))
		v := make([]any, len(out))
		for i, k := range out {
			v[i] = k
		}
		return golden.Result{Value: v}
	},
	"ageYearsAt": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: compute.AgeYearsAt(golden.Date(a[0]), golden.Date(a[1]))}
	},
	"applyDeductions": func(_ testing.TB, a []any) golden.Result {
		var ps []compute.ScheduledPenalty
		for _, x := range golden.Arr(a[2]) {
			m := golden.Obj(x)
			ps = append(ps, compute.ScheduledPenalty{ID: golden.Str(m["id"]), RemainingTHB: golden.Num(m["remainingThb"]),
				InstallmentTHB: golden.Num(m["installmentThb"])})
		}
		d := compute.ApplyDeductions(golden.Num(a[0]), golden.Num(a[1]), ps)
		per := make([]any, len(d.PerPenalty))
		for i, p := range d.PerPenalty {
			per[i] = golden.Object("id", p.ID, "appliedThb", p.AppliedTHB, "remainingAfterThb", p.RemainingAfterTHB)
		}
		return golden.Result{Value: golden.Object("ssoAppliedThb", d.SSOAppliedTHB, "penaltiesAppliedThb", d.PenaltiesAppliedTHB,
			"perPenalty", per, "netThb", d.NetTHB)}
	},
}

func keySet(v any) map[string]bool {
	out := map[string]bool{}
	for _, k := range golden.Arr(v) {
		out[golden.Str(k)] = true
	}
	return out
}

func tiers(v any) []compute.IncentiveTier {
	var out []compute.IncentiveTier
	for _, x := range golden.Arr(v) {
		m := golden.Obj(x)
		out = append(out, compute.IncentiveTier{Min: golden.Num(m["min"]), AmountTHB: golden.Num(m["amountThb"])})
	}
	return out
}

func optDate(v any) *time.Time {
	t, ok := v.(time.Time)
	if !ok {
		return nil
	}
	return &t
}
