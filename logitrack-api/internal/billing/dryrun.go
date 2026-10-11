package billing

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
)

// Reasons a row carries no price. The stored ones are compute.UnpricedReason (R62: no_customer, no_rate,
// no_vehicle_class, no_billing_date for a trip; no_customer, no_rate, no_ended_at for a standby record); the others are
// diagnostic only, never stored (main spec §6.9, §6.13): not_computed (the rules price it: the compute has not run),
// task_missing (a trip without its task cannot be priced, §6.10 state 4) and multi_insufficient_stops (a multi-drop
// trip with fewer than two delivered stops, an error rather than a reason).
const (
	ReasonNotComputed            = "not_computed"
	ReasonTaskMissing            = "task_missing"
	ReasonMultiInsufficientStops = "multi_insufficient_stops"
)

// Where an unpriced row's reason comes from: the snapshot the pricing stored (stored) or a dry run of the pricing rules
// over the live rate tables of the billing carrier (dry_run, nothing written).
const (
	ReasonStored = "stored"
	ReasonDryRun = "dry_run"
)

// dryRun prices nothing: it answers why an unpriced row has no price by running the pure pricing rules
// (compute.PriceTrip, compute.PriceStandby) over the live rate tables of one billing carrier, read without a lock in the
// request transaction (a diagnosis writes nothing, so it takes no FOR SHARE: pricing itself reads LoadPricingTables).
type dryRun struct {
	carrier uuid.UUID
	hubs    compute.HubMaps
	parties map[uuid.UUID]billingdb.PartyRefsRow
	tables  map[uuid.UUID]*dryTables
}

type dryTables struct {
	rates        []compute.RateEntry
	fuel         []compute.FuelAdjustment
	standby      []compute.StandbyRate
	extraStopFee *float64
	standbyFee   *float64
}

// loadDryRun reads the party rows and the rate tables of parties for carrier.
func loadDryRun(ctx context.Context, q *billingdb.Queries, carrier uuid.UUID, hubs compute.HubMaps, parties []uuid.UUID) (*dryRun, error) {
	d := &dryRun{carrier: carrier, hubs: hubs, parties: map[uuid.UUID]billingdb.PartyRefsRow{}, tables: map[uuid.UUID]*dryTables{}}
	if len(parties) == 0 {
		return d, nil
	}
	refs, err := q.PartyRefs(ctx, parties)
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		d.parties[r.ID] = r
		d.tables[r.ID] = &dryTables{}
	}
	t := func(id uuid.UUID) *dryTables {
		if x := d.tables[id]; x != nil {
			return x
		}
		x := &dryTables{}
		d.tables[id] = x
		return x
	}
	rates, err := q.DryRunRateEntries(ctx, billingdb.DryRunRateEntriesParams{PartyIds: parties, CarrierID: carrier})
	if err != nil {
		return nil, err
	}
	for _, r := range rates {
		x := t(r.BillingPartyID)
		x.rates = append(x.rates, compute.RateEntry{ID: r.ID.String(), LegacyDocID: deref(r.LegacyDocID), CreatedAt: r.CreatedAt,
			PartyID: r.BillingPartyID.String(), ImportID: r.ImportID, HubCode: r.HubCode, DestinationCode: r.DestinationCode,
			VehicleClass: r.VehicleClass, RateTHB: r.RateThb, EffectiveFrom: r.EffectiveFromAt,
			JobCategory: compute.RateJobCategory(r.JobCategory)})
	}
	fuel, err := q.DryRunFuelAdjustments(ctx, billingdb.DryRunFuelAdjustmentsParams{PartyIds: parties, CarrierID: carrier})
	if err != nil {
		return nil, err
	}
	for _, a := range fuel {
		x := t(a.BillingPartyID)
		x.fuel = append(x.fuel, compute.FuelAdjustment{ID: a.ID.String(), LegacyDocID: deref(a.LegacyDocID),
			CreatedAt: a.CreatedAt, PartyID: a.BillingPartyID.String(), EffectiveFrom: a.EffectiveFromAt,
			RateMultiplier: a.RateMultiplier, AddTHBPerTrip: a.AddThbPerTrip, ReferenceFuelPriceTHB: a.ReferenceFuelPriceThb})
	}
	standby, err := q.DryRunStandbyRates(ctx, billingdb.DryRunStandbyRatesParams{PartyIds: parties, CarrierID: carrier})
	if err != nil {
		return nil, err
	}
	for _, r := range standby {
		x := t(r.BillingPartyID)
		x.standby = append(x.standby, compute.StandbyRate{ID: r.ID.String(), LegacyDocID: deref(r.LegacyDocID),
			CreatedAt: r.CreatedAt, PartyID: r.BillingPartyID.String(), RateTHB: r.RateThb, EffectiveFrom: r.EffectiveFromAt})
	}
	fees, err := q.DryRunServiceFees(ctx, billingdb.DryRunServiceFeesParams{PartyIds: parties, CarrierID: carrier})
	if err != nil {
		return nil, err
	}
	for _, f := range fees {
		x, amount := t(f.BillingPartyID), f.AmountThb
		switch {
		case f.FeeType == "extra_stop" && x.extraStopFee == nil:
			x.extraStopFee = &amount
		case f.FeeType == "standby" && x.standbyFee == nil:
			x.standbyFee = &amount
		}
	}
	return d, nil
}

// tripVerdict is a dry run's answer for one trip: the reason, and the price the rules would give when they price it.
type tripVerdict struct {
	reason   string
	estimate *float64
	hubCode  string // the lookup keys the rules used: ExtractHubID / NormalizeDestinationCode of the resolved names
	destCode string
	class    string
}

// trip answers for one unpriced trip. party is the trip's resolved party (uuid.Nil when none resolves).
func (d *dryRun) trip(party uuid.UUID, in compute.TripInput) tripVerdict {
	v := tripVerdict{
		hubCode:  compute.ExtractHubID(d.hubs.ResolveNameToCode(in.Task.SourceHub)),
		destCode: compute.NormalizeDestinationCode(d.hubs.ResolveNameToCode(in.Task.Destination)),
	}
	v.class, _ = compute.TripVehicleClass(in.Task.TruckType)
	tables := compute.Tables{Hubs: d.hubs}
	if ref, ok := d.parties[party]; ok {
		tables.Basis = compute.BillingDateBasis(ref.BillingDateBasis)
		if x := d.tables[party]; x != nil {
			tables.Rates, tables.Fuel, tables.ExtraStopFeeTHB = x.rates, x.fuel, x.extraStopFee
		}
	}
	res, err := compute.PriceTrip(in, tables)
	switch {
	case errors.Is(err, compute.ErrMultiInsufficientStops):
		v.reason = ReasonMultiInsufficientStops
	case err != nil:
		v.reason = string(compute.NoRate)
	case res.Price != nil:
		v.reason = ReasonNotComputed
		est := res.Price.EstimateTHB
		v.estimate = &est
	default:
		v.reason = string(res.Reason)
	}
	return v
}

// standby answers for one unpriced standby record.
func (d *dryRun) standby(in compute.StandbyInput) (string, *float64) {
	party, ok := compute.StandbyParty(in)
	var rates []compute.StandbyRate
	var fee *float64
	if ok {
		if id, err := uuid.Parse(party); err == nil {
			if x := d.tables[id]; x != nil {
				rates, fee = x.standby, x.standbyFee
			}
		}
	}
	res := compute.PriceStandby(in, rates, fee)
	if res.Price != nil {
		est := res.Price.EstimateTHB
		return ReasonNotComputed, &est
	}
	return string(res.Reason), nil
}
