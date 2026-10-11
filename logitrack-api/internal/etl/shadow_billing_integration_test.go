//go:build integration

// The billing write-back of T38 (issue #58) on postgres:18-alpine against the in-process Firestore stand-in: once
// billing is PostgreSQL-owned (P3), the price billing.compute writes is projected onto the Firestore trip and standby
// record (trip_billing, standby_billing), field-masked and never creating a document; the fixture's legacy prices
// project back unchanged (the encoders invert the loaders); the mirror, which keeps applying the trips and standby
// records themselves (class C), leaves their billing fields as PostgreSQL holds them; the reconciliation compares them.
package etl

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

func (h *syncHarness) projectBilling(p *Projector, coll string, ids ...uuid.UUID) ProjectStats {
	h.t.Helper()
	t := ShadowTarget{Collection: coll, IDs: ids, All: len(ids) == 0}
	st, err := p.Project(h.ctx, t)
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

func (h *syncHarness) reconcileBilling(coll string, o map[string]bool) Check {
	h.t.Helper()
	docs, err := h.client.Documents(h.ctx, firestoreCollection(coll), false)
	if err != nil {
		h.t.Fatal(err)
	}
	rec := &Reconciliation{Match: true}
	if err := h.e.cfg.Tx(h.ctx, func(tx pgx.Tx) error { return reconcileProjection(h.ctx, tx, coll, docs, o, rec) }); err != nil {
		h.t.Fatal(err)
	}
	if len(rec.Checks) != 1 {
		h.t.Fatalf("checks %+v", rec.Checks)
	}
	return rec.Checks[0]
}

func TestBillingWriteBack(t *testing.T) {
	h := newSyncHarness(t)
	p3 := owned("auth", "masterdata", "billing")
	proj := h.projector(p3)

	// Before P3 the billing fields are Firestore's: nothing is projected.
	if st := h.projectBilling(h.projector(owned("auth", "masterdata")), ProjectionTripBilling); st.NotProjected != 1 || st.Written != 0 {
		t.Fatalf("projected while Firestore-owned: %+v", st)
	}
	// The legacy prices loaded at P1 are what Firestore says: every priced fixture document projects back unchanged.
	writes := len(h.fs.Writes())
	st := h.projectBilling(proj, ProjectionTripBilling)
	if st.Written != 0 || st.Unchanged == 0 || len(h.fs.Writes()) != writes {
		t.Fatalf("the loaded trip prices are rewritten: %+v (%d writes)", st, len(h.fs.Writes())-writes)
	}
	if st := h.projectBilling(proj, ProjectionStandbyBilling); st.Written != 0 || st.Unchanged == 0 {
		t.Fatalf("the loaded standby prices are rewritten: %+v", st)
	}
	if c := h.reconcileBilling(ProjectionTripBilling, p3); !c.OK {
		t.Fatalf("reconciliation of the loaded trip prices %+v", c)
	}
	if c := h.reconcileBilling(ProjectionStandbyBilling, p3); !c.OK {
		t.Fatalf("reconciliation of the loaded standby prices %+v", c)
	}

	// billing.compute reprices TRIP001 (a forced recompute on a new round, frozen as a manual override, the axis moved).
	trip := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP001'`)
	moved := time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC)
	h.exec(`UPDATE trip_billing_snapshots SET estimate_thb = 1300, base_rate_thb = 1300, manual_override = true,
		round_effective_from_date = '2026-09-01', fuel_band_lower_thb = 37.01, fuel_band_upper_thb = 38.00,
		reference_fuel_price_thb = 37.50, computed_by = 'admin_force' WHERE trip_id = $1`, trip)
	h.exec(`UPDATE trip_records SET billing_date = $2, job_category = 'PRIMARY' WHERE id = $1`, trip, moved)
	before := h.doc("trip_records/TRIP001")
	if st := h.projectBilling(proj, ProjectionTripBilling, trip); st.Written != 1 {
		t.Fatalf("repriced trip projection %+v", st)
	}
	w := h.fs.Writes()
	mask := slices.Sorted(slices.Values(w[len(w)-1].Mask))
	if strings.Join(mask, ",") != "billingBaseRateThb,billingDate,billingEstimateThb,billingFuelBandLowerThb,billingFuelBandUpperThb,"+
		"billingManualOverride,billingReferenceFuelPriceThb,billingRoundEffectiveFromDateStr,jobCategory" {
		t.Fatalf("write mask %v", mask)
	}
	f := h.doc("trip_records/TRIP001")
	if f["billingEstimateThb"] != int64(1300) || f["billingManualOverride"] != true || f["billingRoundEffectiveFromDateStr"] != "2026-09-01" ||
		f["billingFuelBandLowerThb"] != 37.01 || f["billingDate"] != (dump.Timestamp{Time: moved}) || f["jobCategory"] != "PRIMARY" {
		t.Fatalf("projected trip %v", f)
	}
	// Fields outside the mask (the operations fields the mirror applies) are untouched.
	for _, k := range []string{"status", "taskId", "driverId", "truckLicensePlate", "billingCustomerId", "billingFuelAdjustmentId"} {
		if !legacyEqual(before[k], f[k]) {
			t.Errorf("%s changed: %v -> %v", k, before[k], f[k])
		}
	}
	if st := h.projectBilling(proj, ProjectionTripBilling, trip); st.Unchanged != 1 {
		t.Fatalf("written twice: %+v", st)
	}

	// The mirror keeps applying the trip (class C) but no longer its billing fields: a legacy writer's stale price in
	// Firestore changes nothing in PostgreSQL once billing is owned.
	h.fs.Merge("trip_records/TRIP001", map[string]any{"billingEstimateThb": 1, "billingCustomerId": "cust_cjsf", "truckLicensePlate": "70-9999"})
	if rep := h.sweep(h.plan(p3)); applied(rep, "trip_records") != 1 {
		t.Fatalf("the trip was not mirrored: %+v", rep.Collections)
	}
	if v := h.scalar(`SELECT estimate_thb::float8 FROM trip_billing_snapshots WHERE trip_id = $1`, trip); v != 1300.0 {
		t.Fatalf("the mirror overwrote the price: %v", v)
	}
	if v := h.scalar(`SELECT truck_license_plate_snapshot FROM trip_records WHERE id = $1`, trip); v != "70-9999" {
		t.Fatalf("the operations fields were not mirrored: %v", v)
	}
	if v := h.scalar(`SELECT billing_date FROM trip_records WHERE id = $1`, trip); !v.(time.Time).Equal(moved) {
		t.Fatalf("the mirror moved the billing axis: %v", v)
	}
	// The projection writes the price back over the stale one; the reconciliation then matches again.
	if c := h.reconcileBilling(ProjectionTripBilling, p3); c.OK {
		t.Fatalf("a stale price in Firestore is not reported: %+v", c)
	}
	if st := h.projectBilling(proj, ProjectionTripBilling, trip); st.Written != 1 {
		t.Fatalf("repair projection %+v", st)
	}
	if f := h.doc("trip_records/TRIP001"); f["billingEstimateThb"] != int64(1300) || f["billingCustomerId"] != "cust_spx" {
		t.Fatalf("repaired trip %v", f)
	}
	if c := h.reconcileBilling(ProjectionTripBilling, p3); !c.OK {
		t.Fatalf("reconciliation after the repair %+v", c)
	}

	// Standby: a price on sb2 (unpriced in the fixture) reaches its document; a record without a document is not
	// created, a record born in PostgreSQL is skipped.
	sb2 := h.id(`SELECT id FROM standby_records WHERE legacy_doc_id = 'sb2'`)
	party := h.id(`SELECT bp.id FROM billing_parties bp JOIN customers c ON c.id = bp.customer_id WHERE c.legacy_doc_id = 'cust_spx'`)
	at := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	h.exec(`UPDATE standby_records SET billing_estimate_thb = 450, billing_party_id = $2, billing_rate_source = 'service_fee',
		billing_computed_at = $3 WHERE id = $1`, sb2, party, at)
	if st := h.projectBilling(proj, ProjectionStandbyBilling, sb2); st.Written != 1 {
		t.Fatalf("standby projection %+v", st)
	}
	if f := h.doc("standby_records/sb2"); f["billingEstimateThb"] != int64(450) || f["billingRateEntryId"] != "service_fee" ||
		f["billingCustomerId"] != "cust_spx" || f["billingComputedAt"] != (dump.Timestamp{Time: at}) {
		t.Fatalf("projected standby %v", f)
	}
	h.fs.Delete("standby_records/sb2")
	writes = len(h.fs.Writes())
	if st := h.projectBilling(proj, ProjectionStandbyBilling, sb2); st.Written != 0 || len(h.fs.Writes()) != writes {
		t.Fatalf("a missing document was created: %+v", st)
	}
	if _, ok := h.fs.Doc("standby_records/sb2"); ok {
		t.Fatal("standby_records/sb2 created")
	}
	own := h.scalar(`SELECT tenant_id::text FROM standby_records WHERE id = $1`, sb2).(string)
	born := h.id(`INSERT INTO standby_records (tenant_id, tenant_source, ended_at, status, billing_estimate_thb, billing_party_id,
		billing_rate_source, billing_computed_at) VALUES ($1, 'driver', $2, 'completed', 300, $3, 'service_fee', $2) RETURNING id`,
		own, at, party)
	if st := h.projectBilling(proj, ProjectionStandbyBilling, born); st.Skipped != 1 || st.Written != 0 {
		t.Fatalf("a record born in PostgreSQL: %+v", st)
	}
	// The standby mirror leaves the billing columns as well.
	sb3 := h.id(`SELECT id FROM standby_records WHERE legacy_doc_id = 'sb3'`)
	h.fs.Merge("standby_records/sb3", map[string]any{"billingEstimateThb": 1, "note": "edited"})
	h.sweep(h.plan(p3))
	if v := h.scalar(`SELECT billing_estimate_thb::float8 FROM standby_records WHERE id = $1`, sb3); v != 300.0 {
		t.Fatalf("the standby mirror overwrote the price: %v", v)
	}
	if v := h.scalar(`SELECT note FROM standby_records WHERE id = $1`, sb3); v != "edited" {
		t.Fatalf("the standby record was not mirrored: %v", v)
	}
	// Before P3 the mirror still loads the legacy price (the P1 load and P2 sweeps).
	h.fs.Merge("standby_records/sb3", map[string]any{"billingEstimateThb": 310, "note": "again"})
	h.sweep(h.plan(owned("auth", "masterdata")))
	if v := h.scalar(`SELECT billing_estimate_thb::float8 FROM standby_records WHERE id = $1`, sb3); v != 310.0 {
		t.Fatalf("the P2 mirror did not load the legacy price: %v", v)
	}
}
