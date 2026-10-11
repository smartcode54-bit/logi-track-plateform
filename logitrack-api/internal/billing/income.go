package billing

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
)

// StopCharge is one billed stop of a multi-drop trip (trip_billing_stop_breakdown).
type StopCharge struct {
	StopIndex       int32   `json:"stopIndex"`
	DestinationCode string  `json:"destinationCode"`
	BaseRateTHB     float64 `json:"baseRateThb"`
	FinalRateTHB    float64 `json:"finalRateThb"`
}

// TripBilling is the billing projection of a delivered trip that GET /v1/trips attaches for accounting:view_income
// (Appendix B §B.2.12: "billing* fields only with accounting:view_income, never for dispatchers"; the Income page reads
// GET /v1/trips?axis=delivered, issue #50 / T30): the stamped party and axis, and the stored snapshot with its ADR 0009
// provenance (estimateThb null with unpricedReason when unpriced; every price field null when the trip has no snapshot
// the caller may read: a tenant that ran a trip another carrier priced reads no price, R61).
type TripBilling struct {
	BillingPartyID         *uuid.UUID   `json:"billingPartyId"`
	CustomerID             *uuid.UUID   `json:"customerId"`
	BillingDate            *time.Time   `json:"billingDate"`
	Priced                 bool         `json:"priced"`
	EstimateTHB            *float64     `json:"estimateThb"`
	UnpricedReason         *string      `json:"unpricedReason"`
	BaseRateTHB            *float64     `json:"baseRateThb"`
	StopChargeTHB          *float64     `json:"stopChargeThb"`
	RateEntryID            *uuid.UUID   `json:"rateEntryId"`
	RateImportID           *string      `json:"rateImportId"`
	LookupHubCode          *string      `json:"lookupHubCode"`
	LookupDestinationCode  *string      `json:"lookupDestinationCode"`
	FuelAdjustmentID       *uuid.UUID   `json:"fuelAdjustmentId"`
	RateMultiplier         *float64     `json:"rateMultiplier"`
	AddTHBPerTrip          *float64     `json:"addThbPerTrip"`
	FuelEffectiveFromDate  *string      `json:"fuelEffectiveFromDate"`
	RoundEffectiveFromDate *string      `json:"roundEffectiveFromDate"`
	FuelBandLowerTHB       *float64     `json:"fuelBandLowerThb"`
	FuelBandUpperTHB       *float64     `json:"fuelBandUpperThb"`
	ReferenceFuelPriceTHB  *float64     `json:"referenceFuelPriceThb"`
	IsMultiDelivery        bool         `json:"isMultiDelivery"`
	ManualOverride         bool         `json:"manualOverride"`
	JobCategoryAtPricing   *string      `json:"jobCategoryAtPricing"`
	ComputedBy             *string      `json:"computedBy"`
	ComputedAt             *time.Time   `json:"computedAt"`
	Stops                  []StopCharge `json:"stops"`
}

// IncomeBillingVisible reports whether GET /v1/trips may attach TripBilling for p: accounting:view_income, never for a
// dispatcher (Appendix B §B.2.12, Appendix C).
func IncomeBillingVisible(p *authz.Principal) bool {
	return p != nil && !p.Dispatcher && p.Can(authz.AccountingViewIncome)
}

// TripBillingOf reads the billing projection of trips in tx, the caller's request transaction (db.WithPrincipal: RLS
// decides which snapshots it reads), keyed by trip id; a trip the caller cannot read is absent. GET /v1/trips (T30)
// calls it for the page it lists when IncomeBillingVisible.
func TripBillingOf(ctx context.Context, tx pgx.Tx, tripIDs []uuid.UUID) (map[uuid.UUID]TripBilling, error) {
	out := make(map[uuid.UUID]TripBilling, len(tripIDs))
	if len(tripIDs) == 0 {
		return out, nil
	}
	q := billingdb.New(tx)
	rows, err := q.TripBillingByIDs(ctx, tripIDs)
	if err != nil {
		return nil, err
	}
	var multi []uuid.UUID
	for _, r := range rows {
		b := TripBilling{BillingPartyID: r.BillingPartyID, CustomerID: r.BilledID, BillingDate: timeUTC(r.BillingDate),
			Stops: []StopCharge{}}
		if r.HasSnapshot {
			b.Priced = r.EstimateThb != nil && *r.EstimateThb != 0
			b.EstimateTHB, b.UnpricedReason, b.BaseRateTHB, b.StopChargeTHB = r.EstimateThb, r.UnpricedReason, r.BaseRateThb, r.StopChargeThb
			b.RateEntryID, b.RateImportID, b.LookupHubCode, b.LookupDestinationCode = r.RateEntryID, r.RateImportID,
				r.LookupHubCode, r.LookupDestinationCode
			b.FuelAdjustmentID, b.RateMultiplier, b.AddTHBPerTrip = r.FuelAdjustmentID, r.RateMultiplier, r.AddThbPerTrip
			b.FuelEffectiveFromDate, b.RoundEffectiveFromDate = dateStr(r.FuelEffectiveFromDate), dateStr(r.RoundEffectiveFromDate)
			b.FuelBandLowerTHB, b.FuelBandUpperTHB, b.ReferenceFuelPriceTHB = r.FuelBandLowerThb, r.FuelBandUpperThb, r.ReferenceFuelPriceThb
			b.IsMultiDelivery = r.IsMultiDelivery != nil && *r.IsMultiDelivery
			b.ManualOverride = r.ManualOverride != nil && *r.ManualOverride
			b.JobCategoryAtPricing, b.ComputedBy, b.ComputedAt = r.JobCategoryAtPricing, r.ComputedBy, timeUTC(r.ComputedAt)
			if b.IsMultiDelivery {
				multi = append(multi, r.ID)
			}
		}
		out[r.ID] = b
	}
	if len(multi) > 0 {
		stops, err := q.BillingRowStops(ctx, multi)
		if err != nil {
			return nil, err
		}
		for _, st := range stops {
			b := out[st.TripID]
			b.Stops = append(b.Stops, StopCharge{StopIndex: st.StopIndex, DestinationCode: st.DestinationCode,
				BaseRateTHB: st.BaseRateThb, FinalRateTHB: st.FinalRateThb})
			out[st.TripID] = b
		}
	}
	return out, nil
}
