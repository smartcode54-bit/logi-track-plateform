package billing

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// PricingTables are the rate tables of one billing party and one billing carrier as a pricing transaction reads them
// (main spec §6.13, R17): live rows from PostgreSQL FOR SHARE (a void or an edit of a row waits until the price that read
// it commits), never Redis. The slices are in the R16 order (legacy_doc_id by bytes with NULLs last, then created_at,
// id), which the selectors of internal/billing/compute apply again themselves. Carrier is the billing tenant every
// price computed from these tables carries (R61): trip_billing_snapshots.tenant_id and standby_records.billing_tenant_id.
// A row of another tenant never enters, whoever wrote it (an ETL row, a later self-billing decision), so the selection
// can never mix carriers.
type PricingTables struct {
	Carrier         uuid.UUID
	Rates           []compute.RateEntry
	Fuel            []compute.FuelAdjustment
	StandbyRates    []compute.StandbyRate
	ExtraStopFeeTHB *float64 // customer_service_fees fee_type 'extra_stop'; nil = none (legacy multi-drop mode)
	StandbyFeeTHB   *float64 // customer_service_fees fee_type 'standby'; nil = none (no standby fallback)
}

// BillingCarrier is the billing carrier a pricing transaction passes to LoadPricingTables: the rate-card owner, the own
// fleet today (R61). Whether a carrier tenant may bill a party itself, and how a trip's carrier is chosen then, is the
// open owner decision of main spec §19 Q13; until it is taken every price is the own fleet's. Read in tx (T38 runs
// pricing in db.WithSystem); pgx.ErrNoRows before the own-fleet row exists.
func BillingCarrier(ctx context.Context, tx pgx.Tx) (uuid.UUID, error) {
	return billingdb.New(tx).OwnFleetTenant(ctx)
}

// LoadPricingTables reads the rate tables of party priced by carrier in tx, the caller's pricing transaction (T38 runs it
// under db.WithSystem with the carrier of BillingCarrier, main spec §6.1). The rows are locked FOR SHARE until tx ends.
func LoadPricingTables(ctx context.Context, tx pgx.Tx, party, carrier uuid.UUID) (PricingTables, error) {
	q := billingdb.New(tx)
	t := PricingTables{Carrier: carrier}
	rates, err := q.PricingRateEntries(ctx, billingdb.PricingRateEntriesParams{PartyID: party, CarrierID: carrier})
	if err != nil {
		return t, err
	}
	for _, r := range rates {
		t.Rates = append(t.Rates, compute.RateEntry{ID: r.ID.String(), LegacyDocID: deref(r.LegacyDocID), CreatedAt: r.CreatedAt,
			PartyID: r.BillingPartyID.String(), ImportID: r.ImportID, HubCode: r.HubCode, DestinationCode: r.DestinationCode,
			VehicleClass: r.VehicleClass, RateTHB: r.RateThb, EffectiveFrom: r.EffectiveFromAt,
			JobCategory: compute.RateJobCategory(r.JobCategory)})
	}
	fuel, err := q.PricingFuelAdjustments(ctx, billingdb.PricingFuelAdjustmentsParams{PartyID: party, CarrierID: carrier})
	if err != nil {
		return t, err
	}
	for _, a := range fuel {
		t.Fuel = append(t.Fuel, compute.FuelAdjustment{ID: a.ID.String(), LegacyDocID: deref(a.LegacyDocID),
			CreatedAt: a.CreatedAt, PartyID: a.BillingPartyID.String(), EffectiveFrom: a.EffectiveFromAt,
			RateMultiplier: a.RateMultiplier, AddTHBPerTrip: a.AddThbPerTrip, ReferenceFuelPriceTHB: a.ReferenceFuelPriceThb})
	}
	standby, err := q.PricingStandbyRates(ctx, billingdb.PricingStandbyRatesParams{PartyID: party, CarrierID: carrier})
	if err != nil {
		return t, err
	}
	for _, r := range standby {
		t.StandbyRates = append(t.StandbyRates, compute.StandbyRate{ID: r.ID.String(), LegacyDocID: deref(r.LegacyDocID),
			CreatedAt: r.CreatedAt, PartyID: r.BillingPartyID.String(), RateTHB: r.RateThb, EffectiveFrom: r.EffectiveFromAt})
	}
	for _, fee := range []struct {
		feeType string
		amount  **float64
	}{{"extra_stop", &t.ExtraStopFeeTHB}, {"standby", &t.StandbyFeeTHB}} {
		f, err := q.PricingServiceFee(ctx, billingdb.PricingServiceFeeParams{PartyID: party, CarrierID: carrier, FeeType: fee.feeType})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return t, err
		}
		amount := f.AmountThb
		*fee.amount = &amount
	}
	return t, nil
}

// PeriodLock is the period-lock read of a pricing transaction (port of fn:core/billingPeriodLock.ts, ADR 0008 §5; main
// spec §6.11, R17): in tx, it first takes the shared advisory lock of every (party, Bangkok month) of instants, in
// ascending order, then reads every statement of party in those months FOR SHARE, whatever its status. A status change
// takes the period's exclusive advisory lock before it locks its statement FOR UPDATE (SetStatementStatus), so a period
// cannot become sent while a reprice commits, not even through a statement created after this read (the row locks cover
// only the statements that existed). A period is locked by a sent or paid statement, the latest generated_at supplying
// the invoice number. The first instant that falls in a locked period answers (the caller passes the old billing date
// first, then the new one, as the forced reprice checks both). A zero instant never locks
// (compute.PeriodLocks.LockFor), and neither does a uuid.Nil party.
//
// The read sees the statements tx may read: pricing runs in db.WithSystem (every billing carrier). A request path that
// checks a lock in db.WithPrincipal sees only the statements of its tenant reach.
func PeriodLock(ctx context.Context, tx pgx.Tx, party uuid.UUID, instants ...time.Time) (compute.LockedPeriod, bool, error) {
	if party == uuid.Nil {
		return compute.LockedPeriod{}, false, nil
	}
	var periods []int32
	for _, at := range instants {
		if at.IsZero() || at.UnixMilli() == 0 {
			continue
		}
		y, m := compute.PeriodOf(at)
		if k := int32(y*100 + m); !slices.Contains(periods, k) {
			periods = append(periods, k)
		}
	}
	if len(periods) == 0 {
		return compute.LockedPeriod{}, false, nil
	}
	q := billingdb.New(tx)
	slices.Sort(periods) // one order for every transaction: the old and new periods of two reprices cannot deadlock
	for _, k := range periods {
		if err := q.PeriodAdvisoryShared(ctx, billingdb.PeriodAdvisorySharedParams{PartyID: party, Period: k}); err != nil {
			return compute.LockedPeriod{}, false, err
		}
	}
	rows, err := q.PeriodStatements(ctx, billingdb.PeriodStatementsParams{PartyID: party, Periods: periods})
	if err != nil {
		return compute.LockedPeriod{}, false, err
	}
	statements := make([]compute.LockedPeriod, len(rows))
	for i, r := range rows {
		statements[i] = compute.LockedPeriod{PartyID: r.BillingPartyID.String(), Year: int(r.PeriodYear),
			Month: int(r.PeriodMonth), InvoiceNumber: r.InvoiceNumber, Status: r.Status, GeneratedAt: r.GeneratedAt}
	}
	locks := compute.NewPeriodLocks(statements)
	for _, at := range instants {
		if l, ok := locks.LockFor(party.String(), at); ok {
			return l, true, nil
		}
	}
	return compute.LockedPeriod{}, false, nil
}

// ErrPeriodLocked is the refusal of a money write into a sent or paid period: 409 billing_period_locked with
// details.blockedInvoiceNumber (main spec §6.10 state 6, Appendix B §B.1.5).
func ErrPeriodLocked(l compute.LockedPeriod) *httpx.Error {
	return httpx.NewError(http.StatusConflict, CodeBillingPeriodLocked,
		"Period already invoiced ("+l.InvoiceNumber+") — cancel or credit-note it first").
		WithDetails(map[string]any{"blockedInvoiceNumber": l.InvoiceNumber, "periodYear": l.Year, "periodMonth": l.Month})
}
