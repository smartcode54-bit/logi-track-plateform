package compute_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// bkk is a Bangkok wall time.
func bkk(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, clock.Bangkok)
}

func TestParsePeriod(t *testing.T) {
	for _, tc := range []struct {
		key        string
		round      compute.Round
		start, end time.Time
		monthEnd   time.Time
	}{
		{"2026-06", compute.R1, bkk(2026, 6, 1, 0, 0), bkk(2026, 6, 16, 0, 0), bkk(2026, 7, 1, 0, 0)},
		{"2026-06", compute.R2, bkk(2026, 6, 16, 0, 0), bkk(2026, 7, 1, 0, 0), bkk(2026, 7, 1, 0, 0)},
		{"2026-12", compute.R2, bkk(2026, 12, 16, 0, 0), bkk(2027, 1, 1, 0, 0), bkk(2027, 1, 1, 0, 0)},
		{"2026-02", compute.R2, bkk(2026, 2, 16, 0, 0), bkk(2026, 3, 1, 0, 0), bkk(2026, 3, 1, 0, 0)},
	} {
		p, ok := compute.ParsePeriod(tc.key, tc.round)
		if !ok || !p.Start.Equal(tc.start) || !p.End.Equal(tc.end) || !p.MonthEnd.Equal(tc.monthEnd) || p.Key != tc.key {
			t.Errorf("ParsePeriod(%s, %s) = %+v, %v", tc.key, tc.round, p, ok)
		}
		// R1 ends on day 16 (Appendix D invariant 11 "payroll window").
		if tc.round == compute.R1 && p.End.In(clock.Bangkok).Day() != 16 {
			t.Errorf("R1 end %v", p.End)
		}
	}
	for _, bad := range []struct {
		key   string
		round compute.Round
	}{{"2026-13", compute.R1}, {"2026-00", compute.R1}, {"2026-6", compute.R1}, {"26-06", compute.R2}, {"2026-06", "R3"}, {"2026-06 ", compute.R1}} {
		if _, ok := compute.ParsePeriod(bad.key, bad.round); ok {
			t.Errorf("ParsePeriod(%q, %q) accepted", bad.key, bad.round)
		}
	}
}

func TestTaskWindowKey(t *testing.T) {
	in := bkk(2026, 6, 16, 11, 0)
	if k := compute.TaskWindowKey(compute.Task{CheckInAt: &in, PlanAt: bkk(2026, 6, 20, 9, 0)}); k != "2026-06-15" {
		t.Errorf("check-in anchor: %s", k)
	}
	// No check-in: the plan date at Bangkok noon, whatever the plan time (ADR 0012 decision 3).
	if k := compute.TaskWindowKey(compute.Task{PlanAt: bkk(2026, 6, 16, 2, 0)}); k != "2026-06-16" {
		t.Errorf("plan noon anchor: %s", k)
	}
	if k := compute.TaskWindowKey(compute.Task{}); k != "" {
		t.Errorf("no anchor: %q", k)
	}
}

func TestPenaltyInstallment(t *testing.T) {
	for _, tc := range []struct {
		total, remaining float64
		n                int
		want             float64
	}{
		{3000, 3000, 1, 3000}, // in full
		{3000, 2750, 1, 2750}, // a carried balance, in full
		{1000, 1000, 3, 333},  // Math.round(1000 / 3)
		{1000, 1, 3, 1},       // the rounding remainder after three installments
		{1000, 0, 3, 0},       // nothing left
		{1001, 1001, 2, 501},  // 500.5 rounds up
		{3000, 500, 2, 500},   // capped at the remaining balance
		{3000, -10, 1, 0},     // never below zero
		{3000, 3000, 0, 3000}, // a zero count is "in full"
		{10, 10, -3, 10},      // a negative count is "in full"
		{3000, 3000, 7000, 0}, // 0.43 rounds to zero: the balance waits for later rounds
		{-9, 9, 2, 0},         // Math.round(-4.5) is -4, clamped to 0
		{2e6, 2e6, 3, 666667}, // 666666.67
	} {
		if got := compute.PenaltyInstallment(tc.total, tc.remaining, tc.n); got != tc.want {
			t.Errorf("PenaltyInstallment(%v, %v, %d) = %v, want %v", tc.total, tc.remaining, tc.n, got, tc.want)
		}
	}
}

func TestInstallmentIndex(t *testing.T) {
	for _, tc := range [][3]int{{0, 1, 1}, {0, 3, 1}, {1, 3, 2}, {2, 3, 3}, {3, 3, 3}, {1, 1, 1}, {0, 0, 1}} {
		if got := compute.InstallmentIndex(tc[0], tc[1]); got != tc[2] {
			t.Errorf("InstallmentIndex(%d, %d) = %d, want %d", tc[0], tc[1], got, tc[2])
		}
	}
}

var testConfig = compute.Config{
	WeekdayRateTHB: 300, HolidayRateTHB: 350, HelperDayRateTHB: 400, FuelMinRefuelsPerMonth: 5,
	FuelIncentiveTiers: []compute.IncentiveTier{{Min: 10, AmountTHB: 1000}},
	TripVolumeTiers:    []compute.IncentiveTier{{Min: 3, AmountTHB: 1000}, {Min: 5, AmountTHB: 1500}},
	SSO:                compute.SSORule{RatePercent: 5, BaseExistingTHB: 15000, BaseNewTHB: 12000, ExistingHiredBeforeYear: 2026, MaxAgeInclusive: 55},
}

type lineSummary struct {
	Type, Category, Name string
	Amount               float64
}

func summary(s compute.Payslip) []lineSummary {
	out := make([]lineSummary, len(s.Lines))
	for i, l := range s.Lines {
		out[i] = lineSummary{l.Type, l.Category, l.Name, l.AmountTHB}
	}
	return out
}

func at(t time.Time) *time.Time { return &t }

// TestComputeR1 is the first round of June 2026: trip pay split by day type, helper windows minus driving windows,
// no incentive and no deduction.
func TestComputeR1(t *testing.T) {
	p, _ := compute.ParsePeriod("2026-06", compute.R1)
	isHoliday := compute.HolidayChecker(map[string]bool{"2026-06-03": true})
	in := compute.DriverInput{
		MonthTrips: []compute.Trip{
			{DeliveredAt: bkk(2026, 6, 2, 9, 0)},                   // Tue: weekday
			{DeliveredAt: bkk(2026, 6, 3, 9, 0)},                   // listed holiday
			{DeliveredAt: bkk(2026, 6, 7, 9, 0)},                   // Sunday: holiday
			{DeliveredAt: bkk(2026, 6, 8, 15, 0), MultiStop: true}, // excluded, still a driving window (8th)
			{DeliveredAt: bkk(2026, 6, 10, 9, 0)},                  // weekday
			{DeliveredAt: bkk(2026, 6, 16, 1, 0)},                  // R2 by delivered_at, but its window (15th) is a driving window
		},
		OwnTasks: []compute.Task{{PlanAt: bkk(2026, 6, 12, 8, 0)}}, // own task without a trip: the 12th drives
		HelperTasks: []compute.Task{
			{CheckInAt: at(bkk(2026, 6, 4, 13, 0)), PlanAt: bkk(2026, 6, 4, 8, 0)},   // window 4th: paid
			{CheckInAt: at(bkk(2026, 6, 5, 2, 0)), PlanAt: bkk(2026, 6, 4, 8, 0)},    // window 4th again: one day
			{CheckInAt: at(bkk(2026, 6, 8, 14, 0)), PlanAt: bkk(2026, 6, 8, 8, 0)},   // window 8th: the multi-stop trip drove
			{PlanAt: bkk(2026, 6, 12, 8, 0)},                                         // window 12th: own task
			{CheckInAt: at(bkk(2026, 6, 16, 11, 0)), PlanAt: bkk(2026, 6, 15, 8, 0)}, // window 15th: the trip of the 16th 01:00
			{CheckInAt: at(bkk(2026, 6, 14, 12, 0)), PlanAt: bkk(2026, 6, 14, 8, 0)}, // window 14th: paid
			{CheckInAt: at(bkk(2026, 6, 16, 12, 0)), PlanAt: bkk(2026, 6, 16, 8, 0)}, // window 16th: R2, not this round
			{CheckInAt: at(bkk(2026, 6, 1, 11, 0)), PlanAt: bkk(2026, 6, 1, 8, 0)},   // window 31 May: last month
		},
		SSO:       compute.SSODriver{ProbationPassed: true},
		Penalties: []compute.Penalty{{ID: "p1", TotalTHB: 500, RemainingTHB: 500, InstallmentsTotal: 1}}, // R1 never deducts
	}
	s := compute.Compute(p, testConfig, isHoliday, in)
	want := []lineSummary{
		{compute.TypeEarning, compute.CategoryTripCommission, "Trip pay (weekday)", 600},
		{compute.TypeEarning, compute.CategoryTripCommission, "Trip pay (holiday)", 700},
		{compute.TypeEarning, compute.CategoryHelperPay, "Helper/training pay", 800},
	}
	if got := summary(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("lines %+v\nwant %+v", got, want)
	}
	if s.Base.WeekdayTrips != 2 || s.Base.HolidayTrips != 2 || s.Base.ExcludedTrips != 1 || s.Base.BasePayTHB != 1300 {
		t.Errorf("base %+v", s.Base)
	}
	if !reflect.DeepEqual(s.HelperDays, []string{"2026-06-04", "2026-06-14"}) {
		t.Errorf("helper days %v", s.HelperDays)
	}
	if s.TotalEarningsTHB != 2100 || s.TotalDeductionsTHB != 0 || s.NetPayTHB != 2100 || len(s.Applications) != 0 {
		t.Errorf("totals %v %v %v %v", s.TotalEarningsTHB, s.TotalDeductionsTHB, s.NetPayTHB, s.Applications)
	}
	l := s.Lines[0]
	if *l.Quantity != 2 || *l.UnitRateTHB != 300 || l.Meta["dayType"] != "weekday" || l.Meta["excludedTrips"] != 1 {
		t.Errorf("weekday line %+v", l)
	}
	h := s.Lines[2]
	if *h.Quantity != 2 || *h.UnitRateTHB != 400 || !reflect.DeepEqual(h.Meta["windows"], []string{"2026-06-04", "2026-06-14"}) {
		t.Errorf("helper line %+v", h)
	}
}

// TestComputeR2 is the second round: the trip-volume tier over the month, SSO at the month end and penalties in the
// given order, the last one short.
func TestComputeR2(t *testing.T) {
	p, _ := compute.ParsePeriod("2026-06", compute.R2)
	isHoliday := compute.HolidayChecker(nil)
	hire, birth := bkk(2025, 5, 1, 0, 0), bkk(1990, 1, 1, 0, 0)
	in := compute.DriverInput{
		MonthTrips: []compute.Trip{
			{DeliveredAt: bkk(2026, 6, 2, 9, 0)}, {DeliveredAt: bkk(2026, 6, 3, 9, 0)}, // R1 trips count for the tier
			{DeliveredAt: bkk(2026, 6, 17, 9, 0)}, {DeliveredAt: bkk(2026, 6, 18, 9, 0)},
			{DeliveredAt: bkk(2026, 6, 21, 9, 0)},                  // Sunday
			{DeliveredAt: bkk(2026, 6, 22, 9, 0), MultiStop: true}, // never counted
		},
		SSO: compute.SSODriver{HireDate: &hire, BirthDate: &birth, ProbationPassed: true},
		Penalties: []compute.Penalty{
			{ID: "a", TypeCode: "LATE_SLA", TypeName: "Late / SLA breach", TotalTHB: 1000, RemainingTHB: 1000, InstallmentsTotal: 1},
			{ID: "b", TypeCode: "DAMAGE", TotalTHB: 3000, RemainingTHB: 2000, InstallmentsTotal: 3, InstallmentsPaid: 1},
			{ID: "c", TotalTHB: 900, RemainingTHB: 900, InstallmentsTotal: 1},
			{ID: "d", TypeCode: "X", TotalTHB: 50, RemainingTHB: 0, InstallmentsTotal: 1}, // nothing left: no line
		},
	}
	s := compute.Compute(p, testConfig, isHoliday, in)
	// Earnings: 2 weekday (600) + 1 holiday (350) + tier 5 trips (1500) = 2450. SSO 750, then a 1000, b 1000 (3000/3),
	// c gets the last 2450 - 750 - 2000 = -300 -> nothing left.
	want := []lineSummary{
		{compute.TypeEarning, compute.CategoryTripCommission, "Trip pay (weekday)", 600},
		{compute.TypeEarning, compute.CategoryTripCommission, "Trip pay (holiday)", 350},
		{compute.TypeEarning, compute.CategoryTripVolumeIncentive, "Trip-volume incentive", 1500},
		{compute.TypeDeduction, compute.CategorySocialSecurity, "Social security", 750},
		{compute.TypeDeduction, compute.CategoryPenalty, "Late / SLA breach", 1000},
		{compute.TypeDeduction, compute.CategoryPenalty, "DAMAGE", 700},
	}
	if got := summary(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("lines %+v\nwant %+v", got, want)
	}
	if s.PayableMonthTrips != 5 || s.TotalEarningsTHB != 2450 || s.TotalDeductionsTHB != 2450 || s.NetPayTHB != 0 {
		t.Errorf("totals %d %v %v %v", s.PayableMonthTrips, s.TotalEarningsTHB, s.TotalDeductionsTHB, s.NetPayTHB)
	}
	if !reflect.DeepEqual(s.Applications, []compute.Application{{PenaltyID: "a", AppliedTHB: 1000}, {PenaltyID: "b", AppliedTHB: 700}}) {
		t.Errorf("applications %+v", s.Applications)
	}
	sso := s.Lines[3]
	if sso.Meta["basePercent"] != 5.0 || sso.Meta["baseThb"] != 15000.0 {
		t.Errorf("sso meta %v", sso.Meta)
	}
	b := s.Lines[5]
	if b.ReferenceID != "b" || b.Meta["installmentIndex"] != 2 || b.Meta["installmentsTotal"] != 3 || b.Meta["remainingThb"] != 1300.0 ||
		b.Meta["totalThb"] != 3000.0 {
		t.Errorf("penalty meta %+v", b)
	}
	if v := s.Lines[2].Meta; v["tripCount"] != 5 || v["tierMinTrips"] != 5.0 {
		t.Errorf("volume meta %v", v)
	}
}

// TestComputeStubsAndZeros: the fuel incentive is the OQ2 stub (never a line, §6.18 #22); both trip lines are written
// at zero; a driver above the age cap pays no SSO; no earnings deduct nothing.
func TestComputeStubsAndZeros(t *testing.T) {
	p, _ := compute.ParsePeriod("2026-06", compute.R2)
	birth := bkk(1960, 1, 1, 0, 0)
	s := compute.Compute(p, testConfig, compute.HolidayChecker(nil), compute.DriverInput{
		SSO:       compute.SSODriver{BirthDate: &birth, ProbationPassed: true},
		Penalties: []compute.Penalty{{ID: "a", TotalTHB: 100, RemainingTHB: 100, InstallmentsTotal: 1}},
	})
	want := []lineSummary{
		{compute.TypeEarning, compute.CategoryTripCommission, "Trip pay (weekday)", 0},
		{compute.TypeEarning, compute.CategoryTripCommission, "Trip pay (holiday)", 0},
	}
	if got := summary(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("lines %+v", got)
	}
	if s.NetPayTHB != 0 || len(s.Applications) != 0 {
		t.Errorf("%+v", s)
	}
	// Younger, new hire (2026): the new base 12000 at 5% = 600, capped at the earnings (2 weekday trips = 600).
	birth2, hire := bkk(1990, 1, 1, 0, 0), bkk(2026, 1, 15, 0, 0)
	s = compute.Compute(p, testConfig, compute.HolidayChecker(nil), compute.DriverInput{
		MonthTrips: []compute.Trip{{DeliveredAt: bkk(2026, 6, 17, 9, 0)}, {DeliveredAt: bkk(2026, 6, 18, 9, 0)}},
		SSO:        compute.SSODriver{BirthDate: &birth2, HireDate: &hire, ProbationPassed: true},
	})
	if s.TotalDeductionsTHB != 600 || s.NetPayTHB != 0 || s.Lines[2].Meta["baseThb"] != 12000.0 {
		t.Errorf("%+v", summary(s))
	}
}
