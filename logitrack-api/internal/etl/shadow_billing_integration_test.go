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

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
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

// TestBillingCoexistenceKeepsGoWrites (review of PR #161): at P3, with the mirror still applying trips, standby records
// and tasks whole (class C), what Go wrote stays written. The resolved หลัก/เสริม of a priced trip or standby record is
// the pricer's (a later document version without it, such as the legacy LINE callable's lineDeliveredNotifiedAt write,
// changes nothing), while an unpriced record still takes the 3.x app's; and the class C writes of the job-category and
// standby-customer endpoints, merged into Firestore first by the write-back (billing.TaskJobCategoryLegacyWrite,
// billing.StandbyCustomerLegacyWrite through writeback.Firestore), survive the standby_billing projection's own echo and
// a later legacy edit of the task.
func TestBillingCoexistenceKeepsGoWrites(t *testing.T) {
	h := newSyncHarness(t)
	p3 := owned("auth", "masterdata", "billing")
	proj := h.projector(p3)
	wb := writeback.Firestore{FS: h.client}
	at := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)

	// The pricer resolved TRIP001 (its document carries no jobCategory) as เสริม; the LINE callable then writes the trip.
	trip := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP001'`)
	h.exec(`UPDATE trip_records SET job_category = 'SUPPLEMENTARY' WHERE id = $1`, trip)
	h.exec(`UPDATE trip_billing_snapshots SET job_category_at_pricing = 'SUPPLEMENTARY' WHERE trip_id = $1`, trip)
	h.fs.Merge("trip_records/TRIP001", map[string]any{"lineDeliveredNotifiedAt": dump.Timestamp{Time: at}})
	if rep := h.sweep(h.plan(p3)); applied(rep, "trip_records") != 1 {
		t.Fatalf("the trip was not mirrored: %+v", rep.Collections)
	}
	if v := h.scalar(`SELECT job_category FROM trip_records WHERE id = $1`, trip); v != "SUPPLEMENTARY" {
		t.Fatalf("the mirror reverted the priced trip's category: %v", v)
	}
	if st := h.projectBilling(proj, ProjectionTripBilling, trip); st.Written != 1 {
		t.Fatalf("trip projection %+v", st)
	}
	if f := h.doc("trip_records/TRIP001"); f["jobCategory"] != "SUPPLEMENTARY" {
		t.Fatalf("projected category %v", f["jobCategory"])
	}
	// An unpriced delivered trip still takes the document's category (the 3.x app seeds it at loading).
	var unpriced string
	if err := h.pool.QueryRow(h.ctx, `SELECT tr.legacy_doc_id FROM trip_records tr
		LEFT JOIN trip_billing_snapshots s ON s.trip_id = tr.id
		WHERE s.trip_id IS NULL AND tr.legacy_doc_id IS NOT NULL ORDER BY tr.legacy_doc_id LIMIT 1`).Scan(&unpriced); err != nil {
		t.Fatal(err)
	}
	h.fs.Merge("trip_records/"+unpriced, map[string]any{"jobCategory": "SUPPLEMENTARY"})
	h.sweep(h.plan(p3))
	if v := h.scalar(`SELECT job_category FROM trip_records WHERE legacy_doc_id = $1`, unpriced); v != "SUPPLEMENTARY" {
		t.Fatalf("an unpriced trip did not take the document's category: %v", v)
	}
	// Before P3 the document's category is applied to a priced trip as well (Firestore prices it).
	h.fs.Merge("trip_records/TRIP001", map[string]any{"jobCategory": "PRIMARY"})
	h.sweep(h.plan(owned("auth", "masterdata")))
	if v := h.scalar(`SELECT job_category FROM trip_records WHERE id = $1`, trip); v != "PRIMARY" {
		t.Fatalf("the P2 mirror did not apply the category: %v", v)
	}

	// A priced standby record (sb3) keeps the category the pricer denormalised.
	sb3 := h.id(`SELECT id FROM standby_records WHERE legacy_doc_id = 'sb3'`)
	h.exec(`UPDATE standby_records SET job_category = 'SUPPLEMENTARY' WHERE id = $1`, sb3)
	h.fs.Merge("standby_records/sb3", map[string]any{"lineNotifiedAt": dump.Timestamp{Time: at}})
	h.sweep(h.plan(p3))
	if v := h.scalar(`SELECT job_category FROM standby_records WHERE id = $1`, sb3); v != "SUPPLEMENTARY" {
		t.Fatalf("the mirror reverted the priced standby record's category: %v", v)
	}

	// PATCH /v1/standby/{id}/customer on sb2: Firestore first (the write-back), then PostgreSQL (customer and price).
	sb2 := h.id(`SELECT id FROM standby_records WHERE legacy_doc_id = 'sb2'`)
	cjsf := h.id(`SELECT bp.id FROM billing_parties bp JOIN customers c ON c.id = bp.customer_id WHERE c.legacy_doc_id = 'cust_cjsf'`)
	if err := wb.Merge(h.ctx, "standby_records", "sb2", billing.StandbyCustomerLegacyWrite("cust_cjsf", at)); err != nil {
		t.Fatal(err)
	}
	h.exec(`UPDATE standby_records SET customer_party_id = $2, customer_resolved = true, customer_resolved_from = 'manual',
		billing_estimate_thb = 500, billing_party_id = $2, billing_rate_source = 'service_fee', billing_computed_at = $3
		WHERE id = $1`, sb2, cjsf, at)
	if st := h.projectBilling(proj, ProjectionStandbyBilling, sb2); st.Written != 1 {
		t.Fatalf("standby projection %+v", st)
	}
	h.sweep(h.plan(p3)) // the projection's write moved the document: the mirror applies it whole
	var party string
	var resolved bool
	var from string
	if err := h.pool.QueryRow(h.ctx, `SELECT customer_party_id::text, customer_resolved, customer_resolved_from
		FROM standby_records WHERE id = $1`, sb2).Scan(&party, &resolved, &from); err != nil {
		t.Fatal(err)
	}
	if party != cjsf.String() || !resolved || from != "manual" {
		t.Fatalf("the standby customer was reverted: %s %v %s", party, resolved, from)
	}
	if v := h.scalar(`SELECT billing_estimate_thb::float8 FROM standby_records WHERE id = $1`, sb2); v != 500.0 {
		t.Fatalf("standby price %v", v)
	}

	// POST /v1/trips/{id}/job-category on a task_1 trip: the task's category goes to Firestore first; a later legacy
	// edit of the task (mirrored whole) keeps it.
	task := h.id(`SELECT id FROM tasks WHERE legacy_doc_id = 'task_1'`)
	if err := wb.Merge(h.ctx, "tasks", "task_1", billing.TaskJobCategoryLegacyWrite(compute.Supplementary, at)); err != nil {
		t.Fatal(err)
	}
	h.exec(`UPDATE tasks SET job_category = 'SUPPLEMENTARY' WHERE id = $1`, task)
	h.fs.Merge("tasks/task_1", map[string]any{"licensePlate": "70-5555"})
	if rep := h.sweep(h.plan(p3)); applied(rep, "tasks") != 1 {
		t.Fatalf("the task was not mirrored: %+v", rep.Collections)
	}
	if v := h.scalar(`SELECT job_category FROM tasks WHERE id = $1`, task); v != "SUPPLEMENTARY" {
		t.Fatalf("the task category was reverted: %v", v)
	}
}
