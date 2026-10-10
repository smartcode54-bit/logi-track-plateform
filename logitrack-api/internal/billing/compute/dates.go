package compute

import (
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// BillingDateBasis is the axis a billing party closes its periods on
// (billing_parties.billing_date_basis, ADR 0027). The empty value is
// delivered, the default for every party that never opted in.
type BillingDateBasis string

const (
	BasisDelivered BillingDateBasis = "delivered"
	BasisPlan      BillingDateBasis = "plan"
)

// present is the legacy "ms > 0" test of timestampLikeToMillis callers: the
// zero time (a NULL column) and instants at or before the epoch are absent.
func present(t time.Time) bool {
	return !t.IsZero() && t.UnixMilli() > 0
}

// BillingDates are the instants a trip may bill on (§6.3).
type BillingDates struct {
	Basis       BillingDateBasis // the billing party's basis
	PlanAt      time.Time        // tasks.plan_at; counts only for a plan-basis party
	DeliveredAt time.Time        // trip_records.delivered_at
	CreatedAt   time.Time        // trip_records.created_at
}

// planInstant is the plan date override of a plan-basis party
// (resolvePlanBillingDateMs, fn:tripBillingOnDelivered.ts:71-75).
func (d BillingDates) planInstant() (time.Time, bool) {
	if d.Basis == BasisPlan && present(d.PlanAt) {
		return d.PlanAt, true
	}
	return time.Time{}, false
}

// For is the instant rate selection runs against (BillingDateFor;
// getTripBillingDateMs, billingCompute.ts:252-262): the plan instant of a
// plan-basis party, else the delivery, else the creation instant. A delivered
// basis never uses the plan date. None of them is (zero, false), unpriced
// no_billing_date — the legacy Date.now() fallback is gone (R19, §6.18 #16).
func (d BillingDates) For() (time.Time, bool) {
	if t, ok := d.planInstant(); ok {
		return t, true
	}
	if present(d.DeliveredAt) {
		return d.DeliveredAt, true
	}
	if present(d.CreatedAt) {
		return d.CreatedAt, true
	}
	return time.Time{}, false
}

// Stored is the trip_records.billing_date axis the billing documents group by
// (fn:tripBillingOnDelivered.ts:382-385): the plan instant of a plan-basis
// party, else the delivery instant, else NULL (zero, false). Unlike For it
// never falls back to the creation instant.
func (d BillingDates) Stored() (time.Time, bool) {
	if t, ok := d.planInstant(); ok {
		return t, true
	}
	if present(d.DeliveredAt) {
		return d.DeliveredAt, true
	}
	return time.Time{}, false
}

// IsEffectiveOn reports whether an announcement effective at effectiveFrom
// applies to a trip billed at bill: its Bangkok calendar day is on or before
// the trip's (isEffectiveOnOrBeforeBillingDate, billingCompute.ts:191-193).
// Comparing days, not instants, absorbs rows stored at UTC midnight before
// 2026-08-09: a 00:21 ICT delivery on a switch day is in round for both
// storage conventions.
func IsEffectiveOn(effectiveFrom, bill time.Time) bool {
	return clock.Day(effectiveFrom) <= clock.Day(bill)
}
