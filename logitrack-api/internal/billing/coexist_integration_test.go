//go:build integration

// Coexistence and exposure rules of T38 (issue #58, review of PR #161): before P3 a deployment that runs the FS->PG
// mirror leaves the records the mirror writes to Firestore's pricer (one writer of PostgreSQL billing state); the
// job-category and standby-customer endpoints write their class C fields back to Firestore before PostgreSQL commits;
// the compute answers carry no unpriced reason for a caller that cannot read prices; the shim's trip lookup stays on
// the indexes.
package billing_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// safetyNet runs one job.billing.safety-net command through j and returns the finished job's result.
func (w *world) safetyNet(j *billing.ComputeJob) map[string]any {
	w.t.Helper()
	var cmd jobs.Job
	if err := db.WithSystem(context.Background(), w.pool, nil, func(tx pgx.Tx) error {
		var err error
		cmd, err = jobs.Enqueue(context.Background(), tx, jobs.NewInput{Type: jobs.TypeBillingSafetyNet})
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	body, _ := json.Marshal(jobs.Command{JobID: cmd.ID, Type: cmd.Type})
	if err := j.Handle(context.Background(), &mq.Delivery{Queue: billing.QueueCompute, MessageID: "1",
		RoutingKey: billing.RouteSafetyNet, Body: body}); err != nil {
		w.t.Fatal(err)
	}
	var raw []byte
	if err := w.etl.QueryRow(context.Background(), `SELECT result FROM jobs WHERE id = $1 AND status = 'succeeded'`, cmd.ID).
		Scan(&raw); err != nil {
		w.t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		w.t.Fatal(err)
	}
	return out
}

// --- the pricing gate: Firestore prices what the mirror writes until P3 -----------------------------------------------

func TestPricingLeavesMirroredRecordsToFirestore(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	recent := now.Add(-10 * time.Minute)
	mirrored, mirroredTask := w.deliveredTrip(w.O, w.partyY, "FS-1", "6WH", "PRIMARY", recent, recent)
	w.exec(`UPDATE trip_records SET legacy_doc_id = 'fsTrip1' WHERE id = $1`, mirrored)
	born, _ := w.deliveredTrip(w.O, w.partyY, "PG-1", "6WH", "PRIMARY", recent, recent)
	w.standbyRate(w.O, w.partyY, "2026-01-01", 450)
	sbMirrored := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status,
		legacy_doc_id) VALUES ($1, 'driver', $2, $3, $3, 'completed', 'fsSb1') RETURNING id::text`, w.O, w.partyY, recent)
	sbBorn := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status)
		VALUES ($1, 'driver', $2, $3, $3, 'completed') RETURNING id::text`, w.O, w.partyY, recent)

	// P1-P2: the mirror runs and billing is Firestore-owned. Every event of a mirrored record is acked without a write.
	gated := w.computeJobWith(true)
	for _, m := range []struct{ key, aggregate, id string }{
		{billing.RouteTripDelivered, "trip", mirrored}, {billing.RouteTripDeliveredAtChange, "trip", mirrored},
		{billing.RouteTaskPlanDateChanged, "task", mirroredTask}, {billing.RouteTripDelivered, "trip", born},
		{billing.RouteStandbyCompleted, "standby", sbMirrored}, {billing.RouteStandbyCustomerAssigned, "standby", sbMirrored},
		{billing.RouteStandbyCompleted, "standby", sbBorn},
	} {
		if err := w.deliver(gated, m.key, w.appendEvent(m.key, m.aggregate, m.id, w.O), m.id); err != nil {
			t.Fatalf("%s %s: %v", m.key, m.id, err)
		}
	}
	if s := w.snap(mirrored); s.exists {
		t.Fatalf("a mirrored trip was priced before P3: %+v", s)
	}
	if n := scan[int](w, `SELECT count(*)::int FROM trip_records WHERE id = $1 AND (billing_party_id IS NOT NULL
		OR billing_date IS NOT NULL)`, mirrored); n != 0 {
		t.Fatal("a mirrored trip's billing axis was stamped before P3")
	}
	if w.events(billing.RouteTripPriced, mirrored) != 0 || w.events(billing.RouteTripRepriced, mirrored) != 0 {
		t.Fatal("a pricing event of a mirrored trip before P3")
	}
	if s := w.snap(born); est(s) != 1260 { // October: 1300 less the -40 fuel round
		t.Fatalf("a trip born in PostgreSQL is priced whatever the phase: %+v", s)
	}
	if n := scan[int](w, `SELECT count(*)::int FROM standby_records WHERE id = $1 AND billing_estimate_thb IS NULL
		AND billing_computed_at IS NULL AND billing_last_event_id IS NULL`, sbMirrored); n != 1 {
		t.Fatal("a mirrored standby record was priced before P3")
	}
	if got := scan[float64](w, `SELECT billing_estimate_thb::float8 FROM standby_records WHERE id = $1`, sbBorn); got != 450 {
		t.Fatalf("a standby record born in PostgreSQL: %v", got)
	}

	// The safety net reads only the records born in PostgreSQL.
	born2, _ := w.deliveredTrip(w.O, w.partyY, "PG-2", "6WH", "PRIMARY", recent, recent)
	res := w.safetyNet(gated)
	trips, _ := res["trips"].(map[string]any)
	standby, _ := res["standby"].(map[string]any)
	if num(trips["scanned"]) != 1 || num(trips["priced"]) != 1 || num(standby["scanned"]) != 0 {
		t.Fatalf("gated safety net: %v", res)
	}
	if est(w.snap(born2)) != 1260 || w.snap(mirrored).exists {
		t.Fatal("the gated safety net priced the wrong trips")
	}

	// A backfill leaves the mirrored trip alone too (a skip, nothing written).
	tok := w.login("ta.o")
	r := w.post("/v1/jobs/billing.backfill-trips", tok, map[string]any{"fromDateStr": "2026-10-01", "toDateStr": "2026-10-31"})
	expect(t, r, http.StatusAccepted, "")
	if res := w.runJobWith(str(r.data()["jobId"]), true); num(res["written"]) != 0 || num(res["skipped"]) != 1 {
		t.Fatalf("gated backfill: %v", res)
	}
	if w.snap(mirrored).exists {
		t.Fatal("the gated backfill priced a mirrored trip")
	}

	// P3 (billing owned), or a deployment without the mirror (the staging VM): Go prices every record.
	open := w.computeJobWith(false)
	if err := w.deliver(open, billing.RouteTripDelivered, w.appendEvent(billing.RouteTripDelivered, "trip", mirrored, w.O), mirrored); err != nil {
		t.Fatal(err)
	}
	if s := w.snap(mirrored); est(s) != 1260 || s.billingParty == nil || *s.billingParty != w.partyY {
		t.Fatalf("P3 price of the mirrored trip: %+v", s)
	}
	if err := w.deliver(open, billing.RouteStandbyCompleted, w.appendEvent(billing.RouteStandbyCompleted, "standby", sbMirrored, w.O),
		sbMirrored); err != nil {
		t.Fatal(err)
	}
	if got := scan[float64](w, `SELECT billing_estimate_thb::float8 FROM standby_records WHERE id = $1`, sbMirrored); got != 450 {
		t.Fatalf("P3 price of the mirrored standby record: %v", got)
	}
}

// --- the class C write-back of the two billing endpoints ---------------------------------------------------------------

func TestJobCategoryAndStandbyCustomerWriteBack(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	tok := w.login("ta.o")
	trip, task := w.deliveredTrip(w.O, w.partyY, "WB-1", "6WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))
	w.exec(`UPDATE tasks SET legacy_doc_id = 'fsTaskWB' WHERE id = $1`, task)
	expect(t, w.post("/v1/trips/"+trip+"/billing/compute", tok, nil), http.StatusOK, "")

	// A failed write-back keeps nothing: 503 unavailable (write_back), the task, the trip and the price unchanged.
	w.wb.Fail(errors.New("firestore unreachable"))
	r := w.post("/v1/trips/"+trip+"/job-category", tok, map[string]any{"jobCategory": "supplementary"})
	expect(t, r, http.StatusServiceUnavailable, "unavailable")
	if r.detail("reason") != "write_back" {
		t.Fatalf("%s", r.raw)
	}
	if c := scan[string](w, `SELECT job_category FROM tasks WHERE id = $1`, task); c != "PRIMARY" {
		t.Fatalf("task category after a failed write-back: %s", c)
	}
	if s := w.snap(trip); est(s) != 1160 || s.tripCat == nil || *s.tripCat != "PRIMARY" || w.events(billing.RouteTripRepriced, trip) != 0 {
		t.Fatalf("trip after a failed write-back: %+v", s)
	}
	w.wb.Fail(nil)

	// Firestore first: the task document takes the category the legacy callable wrote, then PostgreSQL commits.
	r = w.post("/v1/trips/"+trip+"/job-category", tok, map[string]any{"jobCategory": "supplementary"})
	expect(t, r, http.StatusOK, "")
	if s := w.snap(trip); est(s) != 950 || !s.manual || *s.tripCat != "SUPPLEMENTARY" {
		t.Fatalf("job category: %+v", s)
	}
	doc, ok := w.wb.Doc("tasks", "fsTaskWB")
	if !ok || doc["jobCategory"] != "SUPPLEMENTARY" || doc["updatedAt"] != now.UTC() || len(doc) != 2 {
		t.Fatalf("task write-back %v", doc)
	}
	// Nothing to change: nothing written back. A task born in PostgreSQL has no document.
	writes := len(w.wb.Writes())
	expect(t, w.post("/v1/trips/"+trip+"/job-category", tok, map[string]any{"jobCategory": "SUPPLEMENTARY"}), http.StatusOK, "")
	pgTrip, _ := w.deliveredTrip(w.O, w.partyY, "WB-2", "6WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 11, 0))
	expect(t, w.post("/v1/trips/"+pgTrip+"/job-category", tok, map[string]any{"jobCategory": "supplementary"}), http.StatusOK, "")
	if len(w.wb.Writes()) != writes {
		t.Fatalf("unexpected write-backs %v", w.wb.Writes()[writes:])
	}

	// The standby customer: the record's document takes customerId (the customer's id as Firestore names it).
	day := bkk(2026, 9, 15, 10, 0)
	w.standbyRate(w.O, w.partyY, "2026-09-01", 450)
	sb := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, ended_at, started_at, status, legacy_doc_id)
		VALUES ($1, 'driver', $2, $2, 'completed', 'fsSbWB') RETURNING id::text`, w.O, day)
	w.wb.Fail(errors.New("firestore unreachable"))
	expect(t, w.patch("/v1/standby/"+sb+"/customer", tok, map[string]any{"customerId": w.customerY}),
		http.StatusServiceUnavailable, "unavailable")
	if n := scan[int](w, `SELECT count(*)::int FROM standby_records WHERE id = $1 AND customer_party_id IS NULL
		AND billing_estimate_thb IS NULL`, sb); n != 1 || w.events(billing.RouteStandbyCustomerAssigned, sb) != 0 {
		t.Fatal("a failed write-back kept the standby customer")
	}
	w.wb.Fail(nil)
	r = w.patch("/v1/standby/"+sb+"/customer", tok, map[string]any{"customerId": w.customerY})
	expect(t, r, http.StatusOK, "")
	if num(r.data()["billingEstimateThb"]) != 450 {
		t.Fatalf("%s", r.raw)
	}
	customerDoc := scan[string](w, `SELECT coalesce(legacy_doc_id, id::text) FROM customers WHERE id = $1`, w.customerY)
	doc, ok = w.wb.Doc("standby_records", "fsSbWB")
	if !ok || doc["customerId"] != customerDoc || doc["customerResolved"] != true || doc["customerResolvedFrom"] != "manual" ||
		doc["updatedAt"] != now.UTC() {
		t.Fatalf("standby write-back %v", doc)
	}
}

// --- exposure: no unpriced reason for a caller that cannot read prices -------------------------------------------------

func TestComputeAnswersHideReasonsWithoutIncome(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	// Carrier A ran a trip the own fleet has no 18WH card for, and a standby record of a party without a standby rate.
	trip, _ := w.deliveredTrip(w.A, w.partyY, "A-NORATE", "18WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))
	sb := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status)
		VALUES ($1, 'driver', $2, $3, $3, 'completed') RETURNING id::text`, w.A, w.partyX, bkk(2026, 9, 15, 10, 0))
	for _, path := range []string{"/v1/trips/" + trip + "/billing/compute", "/v1/standby/" + sb + "/billing/compute"} {
		r := w.post(path, w.login("ta.a"), nil)
		expect(t, r, http.StatusOK, "")
		keys := make([]string, 0, len(r.data()))
		for k := range r.data() {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if r.data()["ok"] != false || !slices.Equal(keys, []string{"ok"}) {
			t.Fatalf("%s: the carrier read more than ok: %s", path, r.raw)
		}
		// The own fleet's billing staff read why.
		r = w.post(path, w.login("ta.o"), nil)
		expect(t, r, http.StatusOK, "")
		if r.data()["reason"] != "no_rate" || str(r.data()["error"]) == "" {
			t.Fatalf("%s: own fleet: %s", path, r.raw)
		}
	}
}

// --- the shim's trip lookup is three index probes ----------------------------------------------------------------------

func TestResolveTripRefUsesIndexes(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	trip, _ := w.deliveredTrip(w.O, w.partyY, "IDX-1", "6WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))
	w.exec(`UPDATE trip_records SET legacy_doc_id = 'fsIdx1' WHERE id = $1`, trip)
	id := uuidPtr(trip)
	for name, in := range map[string]billingdb.ResolveTripRefParams{
		"uuid": {ID: id, Ref: trip}, "legacy doc id": {Ref: "fsIdx1"}, "trip number": {Ref: "IDX-1"},
	} {
		var plan pgtest.Plan
		if err := db.WithSystem(context.Background(), w.pool, nil, func(tx pgx.Tx) error {
			if _, err := tx.Exec(context.Background(), `SET LOCAL enable_seqscan = off`); err != nil {
				return err
			}
			ex := &pgtest.Explainer{Tx: tx}
			_, _ = billingdb.New(ex).ResolveTripRef(context.Background(), in)
			plan = ex.Last()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if seq := plan.SeqScans("trip_records", "tasks"); len(seq) != 0 {
			t.Fatalf("%s: sequential scans %v\n%s", name, seq, plan)
		}
		idx := plan.Indexes()
		for _, want := range []string{"trip_records_legacy", "trip_records_trip_no"} {
			if !slices.Contains(idx, want) {
				t.Fatalf("%s: %s not used: %v\n%s", name, want, idx, plan)
			}
		}
	}
}
