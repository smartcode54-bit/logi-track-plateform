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

// PricingTables are one billing party's rate tables as a pricing transaction reads them (main spec §6.13, R17): live
// rows from PostgreSQL FOR SHARE (a void or an edit of a row waits until the price that read it commits), never Redis.
// The slices are in the R16 order (legacy_doc_id by bytes with NULLs last, then created_at, id), which the selectors of
// internal/billing/compute apply again themselves. Tenants maps each rate entry, fuel round and standby rate id to its
// billing carrier, the tenant a priced snapshot carries (R61).
type PricingTables struct {
	Rates           []compute.RateEntry
	Fuel            []compute.FuelAdjustment
	StandbyRates    []compute.StandbyRate
	ExtraStopFeeTHB *float64 // customer_service_fees fee_type 'extra_stop'; nil = none (legacy multi-drop mode)
	StandbyFeeTHB   *float64 // customer_service_fees fee_type 'standby'; nil = none (no standby fallback)
	Tenants         map[string]uuid.UUID
	ExtraStopTenant *uuid.UUID
	StandbyTenant   *uuid.UUID
}

// LoadPricingTables reads the rate tables of party in tx, the caller's pricing transaction (T38 runs it under
// db.WithSystem, main spec §6.1). The rows are locked FOR SHARE until tx ends.
func LoadPricingTables(ctx context.Context, tx pgx.Tx, party uuid.UUID) (PricingTables, error) {
	q := billingdb.New(tx)
	t := PricingTables{Tenants: map[string]uuid.UUID{}}
	rates, err := q.PricingRateEntries(ctx, party)
	if err != nil {
		return t, err
	}
	for _, r := range rates {
		t.Rates = append(t.Rates, compute.RateEntry{ID: r.ID.String(), LegacyDocID: deref(r.LegacyDocID), CreatedAt: r.CreatedAt,
			PartyID: r.BillingPartyID.String(), ImportID: r.ImportID, HubCode: r.HubCode, DestinationCode: r.DestinationCode,
			VehicleClass: r.VehicleClass, RateTHB: r.RateThb, EffectiveFrom: r.EffectiveFromAt,
			JobCategory: compute.RateJobCategory(r.JobCategory)})
		t.Tenants[r.ID.String()] = r.TenantID
	}
	fuel, err := q.PricingFuelAdjustments(ctx, party)
	if err != nil {
		return t, err
	}
	for _, a := range fuel {
		t.Fuel = append(t.Fuel, compute.FuelAdjustment{ID: a.ID.String(), LegacyDocID: deref(a.LegacyDocID),
			CreatedAt: a.CreatedAt, PartyID: a.BillingPartyID.String(), EffectiveFrom: a.EffectiveFromAt,
			RateMultiplier: a.RateMultiplier, AddTHBPerTrip: a.AddThbPerTrip, ReferenceFuelPriceTHB: a.ReferenceFuelPriceThb})
		t.Tenants[a.ID.String()] = a.TenantID
	}
	standby, err := q.PricingStandbyRates(ctx, party)
	if err != nil {
		return t, err
	}
	for _, r := range standby {
		t.StandbyRates = append(t.StandbyRates, compute.StandbyRate{ID: r.ID.String(), LegacyDocID: deref(r.LegacyDocID),
			CreatedAt: r.CreatedAt, PartyID: r.BillingPartyID.String(), RateTHB: r.RateThb, EffectiveFrom: r.EffectiveFromAt})
		t.Tenants[r.ID.String()] = r.TenantID
	}
	for _, fee := range []struct {
		feeType string
		amount  **float64
		tenant  **uuid.UUID
	}{{"extra_stop", &t.ExtraStopFeeTHB, &t.ExtraStopTenant}, {"standby", &t.StandbyFeeTHB, &t.StandbyTenant}} {
		f, err := q.PricingServiceFee(ctx, billingdb.PricingServiceFeeParams{PartyID: party, FeeType: fee.feeType})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return t, err
		}
		amount, tenant := f.AmountThb, f.TenantID
		*fee.amount, *fee.tenant = &amount, &tenant
	}
	return t, nil
}

// PeriodLock is the period-lock read of a pricing transaction (port of fn:core/billingPeriodLock.ts, ADR 0008 §5; main
// spec §6.11, R17): in tx, every statement of party in the Bangkok months of instants is read FOR SHARE, whatever its
// status, so a status change (FOR UPDATE) waits for this transaction and a period cannot become sent while a reprice
// commits; a period is locked by a sent or paid statement, the latest generated_at supplying the invoice number. The
// first instant that falls in a locked period answers (the caller passes the old billing date first, then the new
// one, as the forced reprice checks both). A zero instant never locks (compute.PeriodLocks.LockFor), and neither does
// a uuid.Nil party.
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
	rows, err := billingdb.New(tx).PeriodStatements(ctx, billingdb.PeriodStatementsParams{PartyID: party, Periods: periods})
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
