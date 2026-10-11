package compute

import (
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// Period is one pay round's window (roundRange, fn:driverCompensation.ts:55-66): Bangkok midnight bounds.
type Period struct {
	// Key is the pay period yyyy-MM.
	Key   string
	Round Round
	// Start and End bound the round: R1 [day 1, day 16), R2 [day 16, next month's day 1).
	Start, End time.Time
	// MonthStart and MonthEnd bound the month: trip-volume counts, the config's effective instant and the SSO age.
	MonthStart, MonthEnd time.Time
}

var periodRx = regexp.MustCompile(`^(\d{4})-(0[1-9]|1[0-2])$`)

// ParsePeriod is the round of a pay period key yyyy-MM (month 01-12) and a round R1 or R2.
func ParsePeriod(key string, round Round) (Period, bool) {
	m := periodRx.FindStringSubmatch(key)
	if m == nil || (round != R1 && round != R2) {
		return Period{}, false
	}
	y, _ := strconv.Atoi(m[1])
	mon, _ := strconv.Atoi(m[2])
	midnight := func(y, m, d int) time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, clock.Bangkok) }
	p := Period{Key: key, Round: round, MonthStart: midnight(y, mon, 1)}
	if mon == 12 {
		p.MonthEnd = midnight(y+1, 1, 1)
	} else {
		p.MonthEnd = midnight(y, mon+1, 1)
	}
	r2 := midnight(y, mon, 16)
	if round == R1 {
		p.Start, p.End = p.MonthStart, r2
	} else {
		p.Start, p.End = r2, p.MonthEnd
	}
	return p, true
}

// Config is the compensation config version in effect for the month (driver_compensation_configs).
type Config struct {
	WeekdayRateTHB         float64
	HolidayRateTHB         float64
	PayStandby             bool
	HelperDayRateTHB       float64
	FuelIncentiveTiers     []IncentiveTier
	FuelMinRefuelsPerMonth int
	TripVolumeTiers        []IncentiveTier
	SSO                    SSORule
}

// Task is a task as the helper-day window reads it: the main driver's check-in anchors the window, else the plan
// date at Bangkok noon (ADR 0012).
type Task struct {
	CheckInAt *time.Time
	PlanAt    time.Time
}

// TaskWindowKey is the orchestrator's taskWindowKey: HelperWindowKey of the check-in, else of noon (Bangkok) of the
// plan date; "" for a task with neither.
func TaskWindowKey(t Task) string {
	switch {
	case t.CheckInAt != nil:
		return HelperWindowKey(*t.CheckInAt)
	case t.PlanAt.IsZero():
		return ""
	}
	b := t.PlanAt.In(clock.Bangkok)
	return HelperWindowKey(time.Date(b.Year(), b.Month(), b.Day(), 12, 0, 0, 0, clock.Bangkok))
}

// Penalty is an open penalty (pending or partially_deducted) of the driver.
type Penalty struct {
	ID                string
	TypeCode          string
	TypeName          string
	TotalTHB          float64
	RemainingTHB      float64
	InstallmentsTotal int
	InstallmentsPaid  int
}

// DriverInput is what the service loads for one driver.
type DriverInput struct {
	// MonthTrips are the driver's delivered trips of the month (by delivered_at): the round's trips pay base pay, all
	// of them count for the trip-volume tier and mark driving windows.
	MonthTrips []Trip
	// OwnTasks are the driver's own tasks (tasks.driver_id) planned within a day of the round; HelperTasks the tasks
	// the driver rode on as the helper (tasks.helper_driver_id), same window.
	OwnTasks, HelperTasks []Task
	SSO                   SSODriver
	// Penalties are the open penalties in deduction order (incurred_at, id); read in R2 only.
	Penalties []Penalty
}

// Line item types and the ADR 0013 categories the engine writes (payroll_line_items).
const (
	TypeEarning   = "earning"
	TypeDeduction = "deduction"

	CategoryTripCommission      = "TRIP_COMMISSION"
	CategoryHelperPay           = "HELPER_PAY"
	CategoryFuelIncentive       = "FUEL_INCENTIVE"
	CategoryTripVolumeIncentive = "TRIP_VOLUME_INCENTIVE"
	CategorySocialSecurity      = "SOCIAL_SECURITY"
	CategoryPenalty             = "PENALTY"
)

// Line is one payroll line item (ADR 0013): the settled amount and its breakdown.
type Line struct {
	Type        string
	Category    string
	Name        string
	AmountTHB   float64
	Quantity    *float64
	UnitRateTHB *float64
	// ReferenceID names the penalty of a PENALTY line.
	ReferenceID string
	Meta        map[string]any
}

// Application is a penalty deduction to commit when the run is approved.
type Application struct {
	PenaltyID  string
	AppliedTHB float64
}

// Payslip is one driver's run of a round.
type Payslip struct {
	Lines              []Line
	Applications       []Application
	TotalEarningsTHB   float64
	TotalDeductionsTHB float64
	NetPayTHB          float64
	// Base is the round's trip pay, HelperDays the paid helper windows, PayableMonthTrips the trip-volume count (R2).
	Base              BasePayResult
	HelperDays        []string
	PayableMonthTrips int
}

func ptr(v float64) *float64 { return &v }

// Compute is the per-driver body of generateDriverPayoutRun (fn:driverCompensation.ts:130-292):
//
//   - trip pay: the round's delivered trips through BasePay, written as a weekday and a holiday line (both always,
//     their sum the legacy "Trip pay", ADR 0013);
//   - helper pay: helper windows of the round (startKey <= key < endKey) minus the windows the driver drove (own
//     delivered trips of the month by delivered_at, own tasks by check-in or plan noon), HELPER_PAY when positive;
//   - R2 only: the fuel incentive (a stub: no km/L source yet, OQ2, so always 0 and never a line), the trip-volume
//     tier over the month's payable trips, SSO at the month end and the open penalties' installments, deducted by
//     ApplyDeductions; a deduction line and an application per penalty that gave something;
//   - totals in line order and net = max(0, earnings - deductions).
func Compute(p Period, cfg Config, isHoliday HolidayFunc, in DriverInput) Payslip {
	var s Payslip
	var round []Trip
	for _, t := range in.MonthTrips {
		if !t.DeliveredAt.Before(p.Start) && t.DeliveredAt.Before(p.End) {
			round = append(round, t)
		}
	}
	s.Base = BasePay(round, BasePayConfig{WeekdayRateTHB: cfg.WeekdayRateTHB, HolidayRateTHB: cfg.HolidayRateTHB,
		PayStandby: cfg.PayStandby}, isHoliday)
	weekday := RoundTHB(float64(float64(s.Base.WeekdayTrips) * cfg.WeekdayRateTHB))
	s.Lines = append(s.Lines,
		Line{Type: TypeEarning, Category: CategoryTripCommission, Name: "Trip pay (weekday)", AmountTHB: weekday,
			Quantity: ptr(float64(s.Base.WeekdayTrips)), UnitRateTHB: ptr(cfg.WeekdayRateTHB),
			Meta: map[string]any{"dayType": "weekday", "excludedTrips": s.Base.ExcludedTrips}},
		Line{Type: TypeEarning, Category: CategoryTripCommission, Name: "Trip pay (holiday)", AmountTHB: s.Base.BasePayTHB - weekday,
			Quantity: ptr(float64(s.Base.HolidayTrips)), UnitRateTHB: ptr(cfg.HolidayRateTHB),
			Meta: map[string]any{"dayType": "holiday"}})

	driving := map[string]bool{}
	for _, t := range in.MonthTrips {
		driving[HelperWindowKey(t.DeliveredAt)] = true
	}
	for _, t := range in.OwnTasks {
		if k := TaskWindowKey(t); k != "" {
			driving[k] = true
		}
	}
	startKey, endKey := DateKey(p.Start), DateKey(p.End)
	var helperKeys []string
	for _, t := range in.HelperTasks {
		if k := TaskWindowKey(t); k != "" && k >= startKey && k < endKey {
			helperKeys = append(helperKeys, k)
		}
	}
	s.HelperDays = EligibleHelperDayKeys(helperKeys, driving)
	if pay := HelperPay(len(s.HelperDays), cfg.HelperDayRateTHB); pay > 0 {
		s.Lines = append(s.Lines, Line{Type: TypeEarning, Category: CategoryHelperPay, Name: "Helper/training pay", AmountTHB: pay,
			Quantity: ptr(float64(len(s.HelperDays))), UnitRateTHB: ptr(cfg.HelperDayRateTHB),
			Meta: map[string]any{"windows": slices.Clone(s.HelperDays)}})
	}

	if p.Round == R2 {
		for _, t := range in.MonthTrips {
			if !t.MultiStop && (cfg.PayStandby || !t.Standby) {
				s.PayableMonthTrips++
			}
		}
		fuel, _ := FuelIncentive(nil, 0, cfg.FuelIncentiveTiers, cfg.FuelMinRefuelsPerMonth) // OQ2 stub (§6.18 #22)
		volume := TripVolumeIncentive(s.PayableMonthTrips, cfg.TripVolumeTiers)
		sso := SSO(in.SSO, cfg.SSO, p.MonthEnd)
		if fuel > 0 {
			s.Lines = append(s.Lines, Line{Type: TypeEarning, Category: CategoryFuelIncentive, Name: "Fuel incentive", AmountTHB: fuel})
		}
		if volume > 0 {
			s.Lines = append(s.Lines, Line{Type: TypeEarning, Category: CategoryTripVolumeIncentive, Name: "Trip-volume incentive",
				AmountTHB: volume, Meta: map[string]any{"tripCount": s.PayableMonthTrips, "tierMinTrips": reachedTier(s.PayableMonthTrips, cfg.TripVolumeTiers)}})
		}
		earnings := sum(s.Lines, TypeEarning)
		scheduled := make([]ScheduledPenalty, len(in.Penalties))
		for i, pen := range in.Penalties {
			scheduled[i] = ScheduledPenalty{ID: pen.ID, RemainingTHB: pen.RemainingTHB,
				InstallmentTHB: PenaltyInstallment(pen.TotalTHB, pen.RemainingTHB, pen.InstallmentsTotal)}
		}
		ded := ApplyDeductions(earnings, sso, scheduled)
		if ded.SSOAppliedTHB > 0 {
			s.Lines = append(s.Lines, Line{Type: TypeDeduction, Category: CategorySocialSecurity, Name: "Social security",
				AmountTHB: ded.SSOAppliedTHB, Meta: map[string]any{"basePercent": cfg.SSO.RatePercent, "baseThb": SSOBaseTHB(in.SSO, cfg.SSO)}})
		}
		for i, pp := range ded.PerPenalty {
			if pp.AppliedTHB <= 0 {
				continue
			}
			pen := in.Penalties[i]
			name := pen.TypeName
			if name == "" {
				name = pen.TypeCode
			}
			if name == "" {
				name = "Penalty"
			}
			s.Lines = append(s.Lines, Line{Type: TypeDeduction, Category: CategoryPenalty, Name: name, AmountTHB: pp.AppliedTHB,
				ReferenceID: pen.ID, Meta: map[string]any{"installmentIndex": InstallmentIndex(pen.InstallmentsPaid, pen.InstallmentsTotal),
					"installmentsTotal": pen.InstallmentsTotal, "remainingThb": pp.RemainingAfterTHB, "totalThb": pen.TotalTHB}})
			s.Applications = append(s.Applications, Application{PenaltyID: pen.ID, AppliedTHB: pp.AppliedTHB})
		}
	}
	s.TotalEarningsTHB = sum(s.Lines, TypeEarning)
	s.TotalDeductionsTHB = sum(s.Lines, TypeDeduction)
	s.NetPayTHB = max(0, s.TotalEarningsTHB-s.TotalDeductionsTHB)
	return s
}

// InstallmentIndex is the 1-based installment a deduction is, as approval records it: installments_paid + 1, at
// most installments_total (a balance carried past the last installment keeps the last index).
func InstallmentIndex(paid, total int) int {
	return max(1, min(paid+1, total))
}

// reachedTier is the Min of the tier TripVolumeIncentive pays (nil below every tier).
func reachedTier(count int, tiers []IncentiveTier) any {
	var reached any
	for _, t := range sortedTiers(tiers) {
		if float64(count) >= t.Min {
			reached = t.Min
		}
	}
	return reached
}

// sum adds the amounts of one line type in line order (the legacy reduce).
func sum(lines []Line, typ string) float64 {
	var s float64
	for _, l := range lines {
		if l.Type == typ {
			s += l.AmountTHB
		}
	}
	return s
}
