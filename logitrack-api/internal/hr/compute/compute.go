package compute

import (
	"math"
	"slices"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// Round is a semi-monthly pay round: R1 = Bangkok days 1-15, R2 = day 16 to the end of the month.
type Round string

// The two pay rounds (payroll_runs.pay_round).
const (
	R1 Round = "R1"
	R2 Round = "R2"
)

// RoundTHB rounds to whole baht, half toward +Inf (roundTHB, compensationCompute.ts:13): the only rounding site of
// payroll money (§6.15).
func RoundTHB(n float64) float64 { return jsmath.Round(n) }

// DateKey is the Bangkok yyyy-MM-dd of an instant (bangkokDateKey).
func DateKey(t time.Time) string { return clock.DateString(t) }

// AssignRound is the round of a delivered instant: day 1-15 (Bangkok) is R1, 16 to the end R2 (assignRound).
func AssignRound(t time.Time) Round {
	if t.In(clock.Bangkok).Day() <= 15 {
		return R1
	}
	return R2
}

// HolidayFunc reports whether an instant falls on a paid holiday.
type HolidayFunc func(time.Time) bool

// HolidayChecker is makeHolidayChecker: Sunday (Bangkok) or a date in keys (Bangkok yyyy-MM-dd, the holiday
// calendar).
func HolidayChecker(keys map[string]bool) HolidayFunc {
	return func(t time.Time) bool {
		if t.In(clock.Bangkok).Weekday() == time.Sunday {
			return true
		}
		return keys[DateKey(t)]
	}
}

// Trip is a delivered trip as base pay counts it.
type Trip struct {
	// DeliveredAt assigns the trip to its round and its weekday / holiday class.
	DeliveredAt time.Time
	Standby     bool
	MultiStop   bool
}

// BasePayConfig are the per-trip rates of a compensation config.
type BasePayConfig struct {
	WeekdayRateTHB float64
	HolidayRateTHB float64
	// PayStandby false excludes standby trips from pay (FR3).
	PayStandby bool
}

// BasePayResult is computeBasePay's answer.
type BasePayResult struct {
	WeekdayTrips  int
	HolidayTrips  int
	ExcludedTrips int
	BasePayTHB    float64
}

// BasePay is the per-trip pay (computeBasePay, FR5-FR7): multi-stop trips are excluded (OQ9), standby trips unless
// PayStandby; a holiday trip earns the holiday rate. The caller passes the trips of the round.
func BasePay(trips []Trip, cfg BasePayConfig, isHoliday HolidayFunc) BasePayResult {
	var r BasePayResult
	for _, t := range trips {
		if t.MultiStop || (t.Standby && !cfg.PayStandby) {
			r.ExcludedTrips++
			continue
		}
		if isHoliday(t.DeliveredAt) {
			r.HolidayTrips++
		} else {
			r.WeekdayTrips++
		}
	}
	r.BasePayTHB = RoundTHB(float64(float64(r.WeekdayTrips)*cfg.WeekdayRateTHB) + float64(float64(r.HolidayTrips)*cfg.HolidayRateTHB))
	return r
}

// IncentiveTier is one band: Min is the inclusive threshold (km/L for fuel, a trip count for volume).
type IncentiveTier struct {
	Min       float64
	AmountTHB float64
}

// sortedTiers sorts by Min ascending and keeps equal Mins in input order (Array.prototype.sort is stable).
func sortedTiers(tiers []IncentiveTier) []IncentiveTier {
	s := slices.Clone(tiers)
	slices.SortStableFunc(s, func(a, b IncentiveTier) int {
		switch {
		case a.Min < b.Min:
			return -1
		case a.Min > b.Min:
			return 1
		}
		return 0
	})
	return s
}

// FuelIncentive is computeFuelIncentive (FR9, FR9.1): the whole incentive needs strictly more refuels than
// minRefuels; then the highest tier whose Min the km/L reaches pays. A nil km/L is not eligible.
func FuelIncentive(kmPerLitre *float64, refuelCount int, tiers []IncentiveTier, minRefuels int) (amountTHB float64, eligible bool) {
	if kmPerLitre == nil || refuelCount <= minRefuels {
		return 0, false
	}
	for _, t := range sortedTiers(tiers) {
		if *kmPerLitre >= t.Min {
			amountTHB = t.AmountTHB
		}
	}
	return amountTHB, true
}

// TripVolumeIncentive is computeTripVolumeIncentive (FR12): the highest reached tier only, never additive.
func TripVolumeIncentive(tripCount int, tiers []IncentiveTier) float64 {
	var amount float64
	for _, t := range sortedTiers(tiers) {
		if float64(tripCount) >= t.Min {
			amount = t.AmountTHB
		}
	}
	return amount
}

// HelperPay is computeHelperPay: eligible helper days times the daily rate, neither below zero.
func HelperPay(days int, ratePerDay float64) float64 {
	return RoundTHB(float64(float64(max(0, days)) * math.Max(0, ratePerDay)))
}

// HelperWindowKey is helperWindowKey (ADR 0012): the work window 12:00 of day D to 11:59:59 of day D+1 (Bangkok) is
// keyed to D, the Bangkok date of t - 12h, so a shift across midnight is one day.
func HelperWindowKey(t time.Time) string {
	b := t.In(clock.Bangkok)
	if b.Hour() < 12 {
		b = b.AddDate(0, 0, -1)
	}
	return b.Format(time.DateOnly)
}

// EligibleHelperDayKeys is eligibleHelperDayKeys: the distinct helper window keys, in first-seen order, minus the
// windows in which the driver drove (a driving window pays as trips, never also as a helper day); empty keys are
// ignored.
func EligibleHelperDayKeys(helperKeys []string, driving map[string]bool) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, k := range helperKeys {
		if k == "" || driving[k] || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// AgeYearsAt is ageYearsAt: whole years between two instants on the Bangkok calendar.
func AgeYearsAt(birth, asOf time.Time) int {
	b, n := birth.In(clock.Bangkok), asOf.In(clock.Bangkok)
	age := n.Year() - b.Year()
	if n.Month() < b.Month() || (n.Month() == b.Month() && n.Day() < b.Day()) {
		age--
	}
	return age
}

// SSORule is the social-security rule of a compensation config.
type SSORule struct {
	RatePercent             float64
	BaseExistingTHB         float64
	BaseNewTHB              float64
	ExistingHiredBeforeYear int
	MaxAgeInclusive         int
}

// SSODriver are the driver fields the rule reads.
type SSODriver struct {
	HireDate        *time.Time
	BirthDate       *time.Time
	ProbationPassed bool
}

// SSOBaseTHB is the notional base of the driver: hired before ExistingHiredBeforeYear (Bangkok year) uses the
// existing base, else the new one; no hire date counts as existing.
func SSOBaseTHB(d SSODriver, r SSORule) float64 {
	if d.HireDate != nil && d.HireDate.In(clock.Bangkok).Year() >= r.ExistingHiredBeforeYear {
		return r.BaseNewTHB
	}
	return r.BaseExistingTHB
}

// SSO is computeSso (FR13): 0 in probation or above MaxAgeInclusive at asOf, else the base times the rate, whole baht.
func SSO(d SSODriver, r SSORule, asOf time.Time) float64 {
	if !d.ProbationPassed {
		return 0
	}
	if d.BirthDate != nil && AgeYearsAt(*d.BirthDate, asOf) > r.MaxAgeInclusive {
		return 0
	}
	return RoundTHB(float64(SSOBaseTHB(d, r)*r.RatePercent) / 100)
}

// ScheduledPenalty is a penalty's deduction for one round.
type ScheduledPenalty struct {
	ID string
	// RemainingTHB is the outstanding balance before this round.
	RemainingTHB float64
	// InstallmentTHB is what the round should deduct (PenaltyInstallment).
	InstallmentTHB float64
}

// PenaltyDeduction is what one penalty gave up this round.
type PenaltyDeduction struct {
	ID                string
	AppliedTHB        float64
	RemainingAfterTHB float64
}

// Deductions is applyDeductions' answer.
type Deductions struct {
	SSOAppliedTHB       float64
	PenaltiesAppliedTHB float64
	PerPenalty          []PenaltyDeduction
	// NetTHB is earnings - SSO - penalties, never below zero (FR15.1).
	NetTHB float64
}

// ApplyDeductions is applyDeductions (FR15.1): SSO first (mandatory, capped at the earnings), then the penalties in
// order, each min(installment, remaining, available); a shortfall stays as the penalty's remaining balance.
func ApplyDeductions(earningsTHB, ssoTHB float64, penalties []ScheduledPenalty) Deductions {
	available := math.Max(0, earningsTHB)
	var d Deductions
	d.SSOAppliedTHB = math.Min(available, math.Max(0, ssoTHB))
	available -= d.SSOAppliedTHB
	d.PerPenalty = make([]PenaltyDeduction, 0, len(penalties))
	for _, p := range penalties {
		want := math.Min(math.Max(0, p.InstallmentTHB), math.Max(0, p.RemainingTHB))
		applied := math.Min(want, available)
		available -= applied
		d.PenaltiesAppliedTHB += applied
		d.PerPenalty = append(d.PerPenalty, PenaltyDeduction{ID: p.ID, AppliedTHB: applied, RemainingAfterTHB: p.RemainingTHB - applied})
	}
	d.NetTHB = RoundTHB(available)
	return d
}

// PenaltyInstallment is the scheduled deduction of one round (penaltyInstallmentThb, driverPenaltySchema.ts;
// fn:driverCompensation.ts:265-269): total / installments rounded when split, else the whole remaining balance,
// never more than the remaining balance nor below zero.
func PenaltyInstallment(totalTHB, remainingTHB float64, installmentsTotal int) float64 {
	per := remainingTHB
	if installmentsTotal > 1 {
		per = jsmath.Round(totalTHB / float64(installmentsTotal))
	}
	return math.Min(math.Max(0, per), math.Max(0, remainingTHB))
}
