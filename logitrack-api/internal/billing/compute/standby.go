package compute

import (
	"math"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// Standby rate sources (standby_records.billing_rate_source).
const (
	RateSourceStandbyRate = "standby_rate"
	RateSourceServiceFee  = "service_fee"
)

// StandbyInput is one completed standby record and the party links of its
// task.
type StandbyInput struct {
	CustomerPartyID              string // standby_records.customer_party_id
	TaskSourceLinkedPartyID      string // the task's source_linked_party_id
	TaskDestinationLinkedPartyID string // the task's destination_linked_party_id
	EndedAt                      time.Time
	StartedAt                    time.Time
	CreatedAt                    time.Time
}

// StandbyParty is the party a standby bills: the record's own party, else the
// task's source link, else its destination link, trimmed
// (fn:standbyBilling.ts:77-106). It never reads the task's explicit billing
// party (§6.18 #9, preserved for parity, R19).
func StandbyParty(in StandbyInput) (string, bool) {
	return ResolveTaskParty(TaskParties{
		BillingPartyID:           in.CustomerPartyID,
		SourceLinkedPartyID:      in.TaskSourceLinkedPartyID,
		DestinationLinkedPartyID: in.TaskDestinationLinkedPartyID,
	})
}

// StandbyAxisDate is the legacy standby date ended ?? started ?? created
// (standbyBillingDateMs, fn:standbyBilling.ts:126-132; the
// standby_records.billing_axis_date column). Pricing itself requires
// ended_at (see PriceStandby).
func StandbyAxisDate(in StandbyInput) (time.Time, bool) {
	for _, t := range [...]time.Time{in.EndedAt, in.StartedAt, in.CreatedAt} {
		if present(t) {
			return t, true
		}
	}
	return time.Time{}, false
}

// StandbyPrice is a priced standby event: a fixed price, duration ignored.
type StandbyPrice struct {
	PartyID           string
	EstimateTHB       float64
	RateSource        string // RateSourceStandbyRate or RateSourceServiceFee
	RateEntryID       string // "" for a service fee
	EffectiveFromDate string // yyyy-MM-dd of the rate; "" for a service fee
}

// StandbyResult is a standby price or the reason there is none.
type StandbyResult struct {
	PartyID string // resolved party; "" with no_customer
	Price   *StandbyPrice
	Reason  UnpricedReason // set iff Price == nil
}

// PriceStandby prices one completed standby record (§6.9; port of
// fn:standbyBilling.ts:134-209 and computeStandbyBilling,
// billingCompute.ts:574-588): the party's standby rate effective on the
// record's ended_at (newest on or before its Bangkok day, else the oldest),
// else the party's standby service fee (finite, >= 0). Unpriced reasons, in
// order: no_customer, no_ended_at (period membership is always ended_at, so a
// record without it is never invoiced and never priced), no_rate. Job
// category is a label only and plays no part.
func PriceStandby(in StandbyInput, rates []StandbyRate, standbyFeeTHB *float64) StandbyResult {
	party, ok := StandbyParty(in)
	if !ok {
		return StandbyResult{Reason: NoCustomer}
	}
	res := StandbyResult{PartyID: party}
	if !present(in.EndedAt) {
		res.Reason = NoEndedAt
		return res
	}
	if r, ok := SelectStandbyRate(party, in.EndedAt, rates); ok {
		res.Price = &StandbyPrice{
			PartyID:           party,
			EstimateTHB:       r.RateTHB,
			RateSource:        RateSourceStandbyRate,
			RateEntryID:       r.ID,
			EffectiveFromDate: clock.DateString(r.EffectiveFrom),
		}
		return res
	}
	if standbyFeeTHB != nil && !math.IsNaN(*standbyFeeTHB) && !math.IsInf(*standbyFeeTHB, 0) && *standbyFeeTHB >= 0 {
		res.Price = &StandbyPrice{PartyID: party, EstimateTHB: *standbyFeeTHB, RateSource: RateSourceServiceFee}
		return res
	}
	res.Reason = NoRate
	return res
}
