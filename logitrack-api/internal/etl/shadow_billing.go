package etl

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The billing write-back of T38 (main spec §6.13 coexistence, §13.8; Appendix B §B.5.3 firestore.shadow): while the
// installed APKs and the legacy pages read trips and standby records in Firestore (class C, until P7b), the price
// billing.compute writes in PostgreSQL is projected onto the record's Firestore document as the legacy billing*
// fields (fn:tripBillingOnDelivered.ts:453-482, 581-600; fn:standbyBilling.ts:183-207), on trip.priced, trip.repriced
// and standby.priced, once the billing domain is PostgreSQL-owned (P3). These two projections own fields of documents
// another collection's mirror applies (the operations domain stays Firestore-written): they write only their mask, the
// mirror leaves those fields as PostgreSQL holds them (docCtx.billingOwned: no echo loop; jobCategory from the pricer's
// first write of the record on, before which it is the 3.x app's), they never create a
// document (a record PostgreSQL holds without a Firestore document has no legacy reader), and the reconciliation reads
// the record's collection and reports no Firestore-only documents for them. Each encoder is the inverse of its
// loader (map_ops.go tripSnapshot, loadTrip, loadStandby), so a fixture document loaded at P1 projects back unchanged.

// The projections of the billing fields and the Firestore collection each writes.
const (
	ProjectionTripBilling    = "trip_billing"
	ProjectionStandbyBilling = "standby_billing"
)

// fieldProjections are projections that own fields of another collection's documents: name -> Firestore collection.
var fieldProjections = map[string]string{ProjectionTripBilling: "trip_records", ProjectionStandbyBilling: "standby_records"}

// firestoreCollection is the Firestore collection a projection writes and the reconciliation reads.
func firestoreCollection(projection string) string {
	if c, ok := fieldProjections[projection]; ok {
		return c
	}
	return projection
}

// sameNumberOr: a stored number with the value of want, or an absent field when want is the loader's default.
func sameNumberOr(want, absent float64) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		if !has || cur == nil {
			return want == absent
		}
		c, ok := toFloat(cur)
		return ok && c == want
	}
}

func numberOrNil(v *float64) any {
	if v == nil {
		return nil
	}
	return jsNumber(*v)
}

func ymdOrNil(d *time.Time) any {
	if d == nil {
		return nil
	}
	return ymd(*d)
}

// partyDocSQL is the legacy document id a billing party was stamped with (billingCustomerId): its customer's, else its
// carrier's (subcontractors) document id, else the id itself (a party born in PostgreSQL before its projection).
const partyDocSQL = `coalesce(cu.legacy_doc_id, cu.id::text, tn.legacy_doc_id, tn.id::text)`

// encodeTripBilling encodes the billing fields of the selected trips that carry a snapshot (trip_billing_snapshots):
// the price and its full provenance, the multi-drop breakdown, the stamped party and billing date, and the resolved
// หลัก/เสริม the price was taken under (jobCategory). A trip without a Firestore document (born in PostgreSQL) or in the
// quarantine tenant is skipped.
func encodeTripBilling(ctx context.Context, tx pgx.Tx, sel selection, _ bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT tr.id, tr.legacy_doc_id, tr.tenant_source, tr.billing_date, tr.job_category,
		  CASE WHEN bp.id IS NULL THEN NULL ELSE `+partyDocSQL+` END,
		  s.estimate_thb::float8, s.unpriced_reason, s.base_rate_thb::float8, s.stop_charge_thb::float8, s.rate_import_id,
		  s.lookup_hub_code, s.lookup_destination_code, coalesce(fa.legacy_doc_id, fa.id::text), s.rate_multiplier::float8,
		  s.add_thb_per_trip::float8, s.fuel_effective_from_date, s.round_effective_from_date, s.fuel_band_lower_thb::float8,
		  s.fuel_band_upper_thb::float8, s.reference_fuel_price_thb::float8, s.is_multi_delivery, s.manual_override
		FROM trip_records tr
		JOIN trip_billing_snapshots s ON s.trip_id = tr.id
		LEFT JOIN billing_parties bp ON bp.id = tr.billing_party_id
		LEFT JOIN customers cu ON cu.id = bp.customer_id
		LEFT JOIN tenants tn ON tn.id = bp.tenant_id
		LEFT JOIN customer_fuel_rate_adjustments fa ON fa.id = s.fuel_adjustment_id
		WHERE $1 OR tr.id = ANY($2) ORDER BY tr.id`, sel.all, sel.ids)
	if err != nil {
		return nil, fmt.Errorf("etl: encode trip billing: %w", err)
	}
	type trip struct {
		id                                                   uuid.UUID
		legacy, category, party, reason, importID, hub, dest *string
		fuel                                                 *string
		source                                               string
		billingDate, fuelFrom, roundFrom                     *time.Time
		estimate, base, stopCharge, lower, upper, reference  *float64
		multiplier, add                                      float64
		multi, manual                                        bool
	}
	var ts []trip
	var ids []uuid.UUID
	for rows.Next() {
		var t trip
		if err := rows.Scan(&t.id, &t.legacy, &t.source, &t.billingDate, &t.category, &t.party, &t.estimate, &t.reason, &t.base,
			&t.stopCharge, &t.importID, &t.hub, &t.dest, &t.fuel, &t.multiplier, &t.add, &t.fuelFrom, &t.roundFrom, &t.lower,
			&t.upper, &t.reference, &t.multi, &t.manual); err != nil {
			rows.Close()
			return nil, err
		}
		ts = append(ts, t)
		ids = append(ids, t.id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	stops, err := tripBreakdowns(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]ExportDoc, 0, len(ts))
	for _, t := range ts {
		path := "trip_records/" + deref(t.legacy, t.id.String())
		switch {
		case quarantinedRow(t.source):
			out = append(out, newDoc(path).skip("in the quarantine tenant").doc())
			continue
		case t.legacy == nil || *t.legacy == "":
			out = append(out, newDoc(path).skip("born in PostgreSQL: no Firestore document to carry its price").doc())
			continue
		}
		var breakdown any
		if s := stops[t.id]; len(s) > 0 {
			breakdown = s
		}
		b := newDoc(path).
			set("billingEstimateThb", numberOrNil(t.estimate)).set("billingUnpricedReason", t.reason).
			set("billingBaseRateThb", numberOrNil(t.base)).set("billingStopChargeThb", numberOrNil(t.stopCharge)).
			set("billingRateImportId", t.importID).set("billingLookupHubId", t.hub).set("billingLookupDestination", t.dest).
			set("billingFuelAdjustmentId", t.fuel).
			set("billingRateMultiplier", jsNumber(t.multiplier)).eq("billingRateMultiplier", sameNumberOr(t.multiplier, 1)).
			set("billingAddThbPerTrip", jsNumber(t.add)).eq("billingAddThbPerTrip", sameNumberOr(t.add, 0)).
			set("billingEffectiveFromDateStr", ymdOrNil(t.fuelFrom)).set("billingRoundEffectiveFromDateStr", ymdOrNil(t.roundFrom)).
			set("billingFuelBandLowerThb", numberOrNil(t.lower)).set("billingFuelBandUpperThb", numberOrNil(t.upper)).
			set("billingReferenceFuelPriceThb", numberOrNil(t.reference)).
			set("billingIsMultiDelivery", t.multi).eq("billingIsMultiDelivery", sameBool(t.multi, false)).
			set("billingManualOverride", t.manual).eq("billingManualOverride", sameBool(t.manual, false)).
			set("billingMultiDeliveryBreakdown", breakdown).
			set("billingCustomerId", t.party).set("billingDate", t.billingDate).set("jobCategory", t.category)
		b.d.NoCreate = true
		out = append(out, b.doc())
	}
	return out, nil
}

// tripBreakdowns are the billingMultiDeliveryBreakdown[] of trips: {stopIndex, destination, baseRateThb, finalRateThb}.
func tripBreakdowns(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (map[uuid.UUID][]any, error) {
	out := map[uuid.UUID][]any{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT trip_id, stop_index, destination_code, base_rate_thb::float8, final_rate_thb::float8
		FROM trip_billing_stop_breakdown WHERE trip_id = ANY($1) ORDER BY trip_id, stop_index`, ids)
	if err != nil {
		return nil, fmt.Errorf("etl: encode trip billing breakdown: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var idx int32
		var dest string
		var base, final float64
		if err := rows.Scan(&id, &idx, &dest, &base, &final); err != nil {
			return nil, err
		}
		out[id] = append(out[id], map[string]any{"stopIndex": int64(idx), "destination": dest, "baseRateThb": jsNumber(base),
			"finalRateThb": jsNumber(final)})
	}
	return out, rows.Err()
}

// encodeStandbyBilling encodes the inline billing snapshot of the selected standby records that carry one (a price, an
// unpriced reason or a computed time): the price, the party, the rate entry (its legacy id, or "service_fee" for the
// standby service fee, as fn:standbyBilling.ts:197 wrote), the rate's effective day, the computed time and the
// denormalised หลัก/เสริม label.
func encodeStandbyBilling(ctx context.Context, tx pgx.Tx, sel selection, _ bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT sr.id, sr.legacy_doc_id, sr.tenant_source, sr.job_category,
		  CASE WHEN bp.id IS NULL THEN NULL ELSE `+partyDocSQL+` END,
		  sr.billing_estimate_thb::float8, sr.billing_unpriced_reason, sr.billing_rate_source,
		  coalesce(re.legacy_doc_id, re.id::text), sr.billing_effective_from_date, sr.billing_computed_at
		FROM standby_records sr
		LEFT JOIN billing_parties bp ON bp.id = sr.billing_party_id
		LEFT JOIN customers cu ON cu.id = bp.customer_id
		LEFT JOIN tenants tn ON tn.id = bp.tenant_id
		LEFT JOIN standby_rate_entries re ON re.id = sr.billing_rate_entry_id
		WHERE ($1 OR sr.id = ANY($2))
		  AND (sr.billing_estimate_thb IS NOT NULL OR sr.billing_unpriced_reason IS NOT NULL OR sr.billing_computed_at IS NOT NULL)
		ORDER BY sr.id`, sel.all, sel.ids)
	if err != nil {
		return nil, fmt.Errorf("etl: encode standby billing: %w", err)
	}
	defer rows.Close()
	var out []ExportDoc
	for rows.Next() {
		var id uuid.UUID
		var legacy, category, party, reason, source, entry *string
		var src string
		var estimate *float64
		var from, computed *time.Time
		if err := rows.Scan(&id, &legacy, &src, &category, &party, &estimate, &reason, &source, &entry, &from, &computed); err != nil {
			return nil, err
		}
		path := "standby_records/" + deref(legacy, id.String())
		switch {
		case quarantinedRow(src):
			out = append(out, newDoc(path).skip("in the quarantine tenant").doc())
			continue
		case legacy == nil || *legacy == "":
			out = append(out, newDoc(path).skip("born in PostgreSQL: no Firestore document to carry its price").doc())
			continue
		}
		rateEntry := entry
		if source != nil && *source == "service_fee" {
			fee := "service_fee"
			rateEntry = &fee
		}
		b := newDoc(path).
			set("billingEstimateThb", numberOrNil(estimate)).set("billingUnpricedReason", reason).
			set("billingCustomerId", party).set("billingRateEntryId", rateEntry).
			set("billingEffectiveFromDateStr", ymdOrNil(from)).set("billingComputedAt", computed).set("jobCategory", category)
		b.d.NoCreate = true
		out = append(out, b.doc())
	}
	return out, rows.Err()
}
