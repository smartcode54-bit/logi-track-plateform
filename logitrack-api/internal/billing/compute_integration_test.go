//go:build integration

// Integration tests of T38 (issue #58): compute-on-delivery (the billing.compute consumer, its inbox and last_event_id
// rules, the safety net), the trip and standby billing endpoints, the backfill and impact-report jobs and the period
// lock, as logitrack_app through the real API and the worker consumers (harness_integration_test.go; the endpoints
// check the caller's reach under RLS, pricing runs in db.WithSystem).
package billing_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
)

// --- fixtures of T38 ------------------------------------------------------------------------------------------

// pricingFixture: hub SPK-GW; party Y (delivered basis) prices SPK-GW -> SPK890103 6WH at 1200 PRIMARY and 950
// SUPPLEMENTARY from 2026-09-01, 1300 PRIMARY from 2026-10-01; a fuel round of -40 THB from 2026-09-01 (reference price
// 37.50); party X (plan basis) prices the same route at 1000 PRIMARY and 800 SUPPLEMENTARY from 2026-09-01.
func (w *world) pricingFixture() {
	w.hub("SPK-GW", "เจแอนด์ที บางปู", "J&T EXPRESS บางปู")
	w.importRows("ta.o", w.partyY, "2026-09-01", rateRow("SPK-GW", "SPK890103", "6 Wheels", 1200, ""),
		rateRow("SPK-GW", "SPK890103", "6 Wheels", 950, "เสริม"))
	w.importRows("ta.o", w.partyY, "2026-10-01", rateRow("SPK-GW", "SPK890103", "6 Wheels", 1300, ""))
	w.importRows("ta.o", w.partyX, "2026-09-01", rateRow("SPK-GW", "SPK890103", "6 Wheels", 1000, ""),
		rateRow("SPK-GW", "SPK890103", "6 Wheels", 800, "เสริม"))
	w.exec(`INSERT INTO customer_fuel_rate_adjustments (tenant_id, billing_party_id, effective_from_date, effective_from_at,
		rate_multiplier, add_thb_per_trip, reference_fuel_price_thb) VALUES ($1, $2, '2026-09-01', bkk_midnight('2026-09-01'),
		1, -40, 37.50)`, w.O, w.partyY)
}

// deliveredTrip inserts a task of tenant for party (its explicit billing party; "" = none) and its delivered trip, not
// stamped and not priced, and returns the trip and task ids.
func (w *world) deliveredTrip(tenant, party, no, class, cat string, plan, delivered time.Time) (string, string) {
	task := w.task(taskSpec{tenant: tenant, party: party, src: "SPK-GW", dest: "SPK890103", class: class, cat: cat, plan: plan})
	return w.tripOf(tenant, "", no, task, "", delivered, nil, "", false), task
}

// appendEvent appends an outbox event as a producer would and returns its id (the AMQP message_id the relay gives it).
func (w *world) appendEvent(key, aggregate, id string, tenant string) int64 {
	w.t.Helper()
	var out int64
	tid := uuid.MustParse(tenant)
	if err := db.WithSystem(context.Background(), w.pool, nil, func(tx pgx.Tx) error {
		a, err := outbox.Append(context.Background(), tx, outbox.Event{RoutingKey: key, AggregateType: aggregate, AggregateID: id,
			TenantID: &tid, Payload: map[string]any{"id": id, "source": "mirror"}})
		out = a.ID
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	return out
}

func (w *world) computeJob() *billing.ComputeJob {
	w.t.Helper()
	j, err := billing.NewComputeJob(billing.JobDeps{Pool: w.pool, Now: func() time.Time { return now }, Log: zerolog.Nop()},
		prometheus.NewRegistry())
	if err != nil {
		w.t.Fatal(err)
	}
	return j
}

// deliver hands billing.compute the message key with outbox id ev about record id.
func (w *world) deliver(j *billing.ComputeJob, key string, ev int64, id string) error {
	body, _ := json.Marshal(map[string]any{"id": id})
	return j.Handle(context.Background(), &mq.Delivery{Queue: billing.QueueCompute, MessageID: strconv.FormatInt(ev, 10),
		RoutingKey: key, Body: body})
}

// snapshot reads a trip's stored price: estimate (nil = none), reason, manual override, last_event_id, computed_by.
type snap struct {
	estimate              *float64
	reason                *string
	manual                bool
	lastEvent             *int64
	computedBy            string
	tenant                string
	multiplier, add       float64
	fuel                  *string
	category              *string
	importID              *string
	roundDate             *time.Time
	exists                bool
	billingDate           *time.Time
	billingParty, tripCat *string
}

func (w *world) snap(trip string) snap {
	w.t.Helper()
	var s snap
	err := w.etl.QueryRow(context.Background(), `SELECT s.estimate_thb::float8, s.unpriced_reason, s.manual_override, s.last_event_id,
		s.computed_by, s.tenant_id::text, s.rate_multiplier::float8, s.add_thb_per_trip::float8, s.fuel_adjustment_id::text,
		s.job_category_at_pricing, s.rate_import_id, s.round_effective_from_date::timestamptz,
		tr.billing_date, tr.billing_party_id::text, tr.job_category
		FROM trip_records tr JOIN trip_billing_snapshots s ON s.trip_id = tr.id WHERE tr.id = $1`, trip).
		Scan(&s.estimate, &s.reason, &s.manual, &s.lastEvent, &s.computedBy, &s.tenant, &s.multiplier, &s.add, &s.fuel,
			&s.category, &s.importID, &s.roundDate, &s.billingDate, &s.billingParty, &s.tripCat)
	if errors.Is(err, pgx.ErrNoRows) {
		return snap{}
	}
	if err != nil {
		w.t.Fatal(err)
	}
	s.exists = true
	return s
}

func (w *world) events(key, id string) int {
	return scan[int](w, `SELECT count(*)::int FROM outbox_events WHERE routing_key = $1 AND aggregate_id = $2`, key, id)
}

// lockPeriod inserts a statement of party for a Bangkok month with status (the legacy loader's row).
func (w *world) lockPeriod(party, invoice string, year, month int, status string) {
	w.exec(`INSERT INTO billing_statements (tenant_id, billing_party_id, invoice_number, period_year, period_month,
		total_amount, withholding_tax_rate, withholding_tax, net_amount, status, generated_at)
		VALUES ($1, $2, $3, $4, $5, 100, 0.01, 1, 99, $6, now())`, w.O, party, invoice, year, month, status)
}

func est(s snap) float64 {
	if s.estimate == nil {
		return -1
	}
	return *s.estimate
}

// --- AC: redelivery writes once; stale events are dropped -------------------------------------------------------

func TestComputeOnDeliveryIsIdempotentAndOrdered(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	j := w.computeJob()
	trip, _ := w.deliveredTrip(w.O, w.partyY, "TR-1", "6WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))

	// An older date-change event, produced before the delivery was priced.
	older := w.appendEvent(billing.RouteTripDeliveredAtChange, "trip", trip, w.O)
	delivered := w.appendEvent(billing.RouteTripDelivered, "trip", trip, w.O)
	if err := w.deliver(j, billing.RouteTripDelivered, delivered, trip); err != nil {
		t.Fatal(err)
	}
	s := w.snap(trip)
	if est(s) != 1160 || s.lastEvent == nil || *s.lastEvent <= delivered || s.computedBy != billing.ComputedByMobile ||
		s.tenant != w.O || s.billingParty == nil || *s.billingParty != w.partyY || s.tripCat == nil || *s.tripCat != "PRIMARY" ||
		s.fuel == nil || s.add != -40 {
		t.Fatalf("first pricing: %+v", s)
	}
	priced := *s.lastEvent
	if n := w.events(billing.RouteTripPriced, trip); n != 1 {
		t.Fatalf("trip.priced events %d", n)
	}

	// A redelivery of the same message is acked by the inbox: nothing is written twice.
	w.exec(`UPDATE trip_billing_snapshots SET computed_at = '2020-01-01' WHERE trip_id = $1`, trip)
	if err := w.deliver(j, billing.RouteTripDelivered, delivered, trip); err != nil {
		t.Fatal(err)
	}
	if n := w.events(billing.RouteTripPriced, trip); n != 1 {
		t.Fatalf("redelivery wrote again: %d trip.priced", n)
	}
	if got := scan[time.Time](w, `SELECT computed_at FROM trip_billing_snapshots WHERE trip_id = $1`, trip); got.Year() != 2020 {
		t.Fatalf("redelivery rewrote the snapshot at %s", got)
	}
	if n := scan[int](w, `SELECT count(*)::int FROM consumer_inbox WHERE consumer = 'billing.compute' AND message_id = $1`,
		strconv.FormatInt(delivered, 10)); n != 1 {
		t.Fatalf("inbox rows %d", n)
	}

	// The older event arrives late: its id is below last_event_id, so even a forcing event changes nothing.
	w.exec(`UPDATE trip_records SET delivered_at = $2 WHERE id = $1`, trip, bkk(2026, 10, 2, 9, 0))
	if err := w.deliver(j, billing.RouteTripDeliveredAtChange, older, trip); err != nil {
		t.Fatal(err)
	}
	if s := w.snap(trip); est(s) != 1160 || *s.lastEvent != priced {
		t.Fatalf("stale event applied: %+v", s)
	}
	// A newer date change (appended after the price) reprices on the new round: October 1300, the September fuel round
	// is still the newest (-40).
	newer := w.appendEvent(billing.RouteTripDeliveredAtChange, "trip", trip, w.O)
	if err := w.deliver(j, billing.RouteTripDeliveredAtChange, newer, trip); err != nil {
		t.Fatal(err)
	}
	s = w.snap(trip)
	if est(s) != 1260 || *s.lastEvent <= newer || s.computedBy != billing.ComputedByAdminForce ||
		s.billingDate == nil || !s.billingDate.Equal(bkk(2026, 10, 2, 9, 0)) {
		t.Fatalf("newer event: %+v", s)
	}
	if n := w.events(billing.RouteTripRepriced, trip); n != 1 {
		t.Fatalf("trip.repriced events %d", n)
	}

	// A task's plan-date change reprices its delivered trips (plan basis), and is stale once applied.
	ptrip, ptask := w.deliveredTrip(w.O, w.partyX, "TR-PLAN", "6WH", "PRIMARY", bkk(2026, 9, 10, 8, 0), bkk(2026, 9, 11, 9, 0))
	ev := w.appendEvent(billing.RouteTripDelivered, "trip", ptrip, w.O)
	if err := w.deliver(j, billing.RouteTripDelivered, ev, ptrip); err != nil {
		t.Fatal(err)
	}
	if s := w.snap(ptrip); est(s) != 1000 || !s.billingDate.Equal(bkk(2026, 9, 10, 8, 0)) {
		t.Fatalf("plan-basis price: %+v", s)
	}
	w.exec(`UPDATE tasks SET plan_at = $2 WHERE id = $1`, ptask, bkk(2026, 9, 20, 8, 0))
	pev := w.appendEvent(billing.RouteTaskPlanDateChanged, "task", ptask, w.O)
	if err := w.deliver(j, billing.RouteTaskPlanDateChanged, pev, ptask); err != nil {
		t.Fatal(err)
	}
	if s := w.snap(ptrip); est(s) != 1000 || !s.billingDate.Equal(bkk(2026, 9, 20, 8, 0)) {
		t.Fatalf("plan date moved: %+v", s)
	}

	// A message about a record that does not exist is acked; an unknown routing key is permanent.
	if err := w.deliver(j, billing.RouteTripDelivered, w.appendEvent(billing.RouteTripDelivered, "trip", uuid.NewString(), w.O),
		uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := w.deliver(j, "trip.updated", 999999, trip); !mq.IsPermanent(err) {
		t.Fatalf("unknown key: %v", err)
	}
}

// --- AC: forced recompute in a sent/paid month is 409 billing_period_locked (old or new period) --------------------

func TestForcedRecomputeRespectsPeriodLocks(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	tok := w.login("ta.o")
	compute := func(trip string, force bool) resp {
		return w.post("/v1/trips/"+trip+"/billing/compute", tok, map[string]any{"forceRecompute": force})
	}

	// Old period: priced in September, September sent.
	old, _ := w.deliveredTrip(w.O, w.partyY, "LOCK-OLD", "6WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))
	r := compute(old, false)
	expect(t, r, http.StatusOK, "")
	if num(r.data()["billingEstimateThb"]) != 1160 {
		t.Fatalf("%s", r.raw)
	}
	w.lockPeriod(w.partyY, "TTP-202609-001", 2026, 9, "sent")
	before := w.snap(old)
	r = compute(old, true)
	expect(t, r, http.StatusConflict, billing.CodeBillingPeriodLocked)
	if r.detail("blockedInvoiceNumber") != "TTP-202609-001" {
		t.Fatalf("%s", r.raw)
	}
	if after := w.snap(old); est(after) != est(before) || *after.lastEvent != *before.lastEvent {
		t.Fatalf("a blocked recompute wrote: %+v", after)
	}
	// A plain compute of a priced trip is a skip, never blocked.
	r = compute(old, false)
	expect(t, r, http.StatusOK, "")
	if r.data()["skipped"] != true || num(r.data()["billingEstimateThb"]) != 1160 {
		t.Fatalf("%s", r.raw)
	}
	// The job-category change and the manual price check the same lock.
	expect(t, w.post("/v1/trips/"+old+"/job-category", tok, map[string]any{"jobCategory": "supplementary"}),
		http.StatusConflict, billing.CodeBillingPeriodLocked)
	expect(t, w.do(http.MethodPut, "/v1/trips/"+old+"/billing/manual", tok, map[string]any{"billingEstimateThb": 999,
		"jobCategory": "PRIMARY"}), http.StatusConflict, billing.CodeBillingPeriodLocked)

	// New period: a plan-basis trip priced in September (open) whose plan date moved into a paid October.
	tripN, taskN := w.deliveredTrip(w.O, w.partyX, "LOCK-NEW", "6WH", "PRIMARY", bkk(2026, 9, 25, 8, 0), bkk(2026, 9, 25, 18, 0))
	expect(t, compute(tripN, false), http.StatusOK, "")
	w.lockPeriod(w.partyX, "CJSF-202610-001", 2026, 10, "paid")
	w.exec(`UPDATE tasks SET plan_at = $2 WHERE id = $1`, taskN, bkk(2026, 10, 1, 8, 0))
	r = compute(tripN, true)
	expect(t, r, http.StatusConflict, billing.CodeBillingPeriodLocked)
	if r.detail("blockedInvoiceNumber") != "CJSF-202610-001" {
		t.Fatalf("%s", r.raw)
	}
	if s := w.snap(tripN); !s.billingDate.Equal(bkk(2026, 9, 25, 8, 0)) {
		t.Fatalf("blocked reprice moved the axis: %+v", s)
	}
	// The same refusal through the consumer is acked and writes nothing.
	j := w.computeJob()
	if err := w.deliver(j, billing.RouteTaskPlanDateChanged, w.appendEvent(billing.RouteTaskPlanDateChanged, "task", taskN, w.O), taskN); err != nil {
		t.Fatal(err)
	}
	if s := w.snap(tripN); !s.billingDate.Equal(bkk(2026, 9, 25, 8, 0)) {
		t.Fatalf("consumer moved a locked trip: %+v", s)
	}
	// A cancelled statement never locks; draft neither.
	w.exec(`UPDATE billing_statements SET status = 'cancelled' WHERE invoice_number = 'CJSF-202610-001'`)
	r = compute(tripN, true)
	expect(t, r, http.StatusOK, "")
	if s := w.snap(tripN); !s.billingDate.Equal(bkk(2026, 10, 1, 8, 0)) {
		t.Fatalf("open period: %+v (%s)", s, r.raw)
	}

	// forceRecompute needs accounting:recompute_force (a user lacks it) and the own fleet.
	expect(t, w.post("/v1/trips/"+old+"/billing/compute", w.login("us.o"), map[string]any{"forceRecompute": true}),
		http.StatusForbidden, "permission_denied")
}

// --- AC: frozen rows only restamped; SUPPLEMENTARY never fuel-adjusted -----------------------------------------------

func TestFrozenRowsAndSupplementary(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	tok := w.login("ta.o")
	compute := func(trip string, force bool) resp {
		w.t.Helper()
		r := w.post("/v1/trips/"+trip+"/billing/compute", tok, map[string]any{"forceRecompute": force})
		expect(w.t, r, http.StatusOK, "")
		return r
	}

	// SUPPLEMENTARY prices the เสริม card with no fuel round, frozen (manual_override).
	supp, _ := w.deliveredTrip(w.O, w.partyY, "SUPP", "6WH", "SUPPLEMENTARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))
	compute(supp, false)
	s := w.snap(supp)
	if est(s) != 950 || s.fuel != nil || s.multiplier != 1 || s.add != 0 || !s.manual || *s.category != "SUPPLEMENTARY" {
		t.Fatalf("supplementary: %+v", s)
	}
	// A legacy task without a category whose PRIMARY card is missing falls back to SUPPLEMENTARY, still without fuel.
	w.exec(`INSERT INTO hubs (source_id, name_th, network) VALUES ('SPK-B', 'บี', 'SPK')`)
	w.importRows("ta.o", w.partyY, "2026-09-01", rateRow("SPK-B", "SPK890103", "6 Wheels", 700, "เสริม"))
	ltask := w.task(taskSpec{tenant: w.O, party: w.partyY, src: "SPK-B", dest: "SPK890103", class: "6WH"})
	legacy := w.tripOf(w.O, "", "LEGACY", ltask, "", bkk(2026, 9, 16, 10, 0), nil, "", false)
	compute(legacy, false)
	if s := w.snap(legacy); est(s) != 700 || s.fuel != nil || !s.manual || *s.tripCat != "SUPPLEMENTARY" {
		t.Fatalf("legacy fallback: %+v", s)
	}

	// Frozen: a forced recompute keeps the price even after a new SUPPLEMENTARY card; a plan-basis frozen trip moves
	// only its billing date.
	w.importRows("ta.o", w.partyY, "2026-09-02", rateRow("SPK-GW", "SPK890103", "6 Wheels", 990, "เสริม"))
	r := compute(supp, true)
	if r.data()["skipped"] != true || num(r.data()["billingEstimateThb"]) != 950 || est(w.snap(supp)) != 950 {
		t.Fatalf("frozen repriced: %s", r.raw)
	}
	ftrip, ftask := w.deliveredTrip(w.O, w.partyX, "FROZEN-PLAN", "6WH", "SUPPLEMENTARY", bkk(2026, 9, 10, 8, 0), bkk(2026, 9, 12, 9, 0))
	compute(ftrip, false)
	if s := w.snap(ftrip); est(s) != 800 || !s.billingDate.Equal(bkk(2026, 9, 10, 8, 0)) {
		t.Fatalf("frozen plan trip: %+v", s)
	}
	w.exec(`UPDATE tasks SET plan_at = $2 WHERE id = $1`, ftask, bkk(2026, 9, 18, 8, 0))
	r = compute(ftrip, true)
	if r.data()["billingDateMoved"] != true || r.data()["skipped"] != true || num(r.data()["billingEstimateThb"]) != 800 {
		t.Fatalf("restamp: %s", r.raw)
	}
	if s := w.snap(ftrip); est(s) != 800 || !s.billingDate.Equal(bkk(2026, 9, 18, 8, 0)) {
		t.Fatalf("restamp: %+v", s)
	}
	r = compute(ftrip, true)
	if r.data()["billingDateMoved"] == true {
		t.Fatalf("a restamp to the same date moved: %s", r.raw)
	}

	// A เสริม row priced WITH fuel (the removed browser writer) is not frozen: a forced recompute repairs it.
	w.exec(`UPDATE trip_billing_snapshots SET manual_override = false, add_thb_per_trip = -40, estimate_thb = 910
		WHERE trip_id = $1`, supp)
	compute(supp, true)
	if s := w.snap(supp); est(s) != 990 || s.add != 0 || !s.manual {
		t.Fatalf("supp_priced_with_fuel repair: %+v", s)
	}

	// The job-category change is the only path that reprices a frozen row: PRIMARY with fuel, un-frozen.
	r = w.post("/v1/trips/"+supp+"/job-category", tok, map[string]any{"jobCategory": "primary"})
	expect(t, r, http.StatusOK, "")
	s = w.snap(supp)
	if est(s) != 1160 || s.manual || s.add != -40 || *s.tripCat != "PRIMARY" || s.computedBy != billing.ComputedBySetJobCategory ||
		s.roundDate == nil || s.importID == nil {
		t.Fatalf("job category PRIMARY: %+v", s)
	}
	if cat := scan[string](w, `SELECT t.job_category FROM tasks t JOIN trip_records tr ON tr.task_id = t.id WHERE tr.id = $1`, supp); cat != "PRIMARY" {
		t.Fatalf("task category %s", cat)
	}
	if w.events(billing.RouteTripRepriced, supp) == 0 {
		t.Fatal("no trip.repriced")
	}
	// The same target again is a no-op.
	r = w.post("/v1/trips/"+supp+"/job-category", tok, map[string]any{"jobCategory": "PRIMARY"})
	expect(t, r, http.StatusOK, "")
	if r.data()["skipped"] != true {
		t.Fatalf("%s", r.raw)
	}
	// A target without a card is 422 no_rate and writes nothing.
	nr, _ := w.deliveredTrip(w.O, w.partyY, "NO-SUPP", "4WJ", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))
	expect(t, w.post("/v1/trips/"+nr+"/job-category", tok, map[string]any{"jobCategory": "SUPPLEMENTARY"}),
		http.StatusUnprocessableEntity, "no_rate")
	if w.snap(nr).exists {
		t.Fatal("a refused job-category change wrote a snapshot")
	}
	expect(t, w.post("/v1/trips/"+nr+"/job-category", tok, map[string]any{"jobCategory": "x"}),
		http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.post("/v1/trips/"+nr+"/job-category", w.login("us.o"), map[string]any{"jobCategory": "PRIMARY"}),
		http.StatusForbidden, "permission_denied")
}

// --- manual price (accounting:override_price) -----------------------------------------------------------------------

func TestManualPrice(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	tok := w.login("ta.o")
	put := func(trip string, body map[string]any) resp {
		return w.do(http.MethodPut, "/v1/trips/"+trip+"/billing/manual", tok, body)
	}
	trip, _ := w.deliveredTrip(w.O, w.partyY, "MAN", "6WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))
	// The reference price itself: not an override, the reference's provenance.
	expect(t, put(trip, map[string]any{"billingEstimateThb": 1160, "jobCategory": "PRIMARY"}), http.StatusOK, "")
	if s := w.snap(trip); est(s) != 1160 || s.manual || s.add != -40 || s.computedBy != billing.ComputedByManualEdit {
		t.Fatalf("reference: %+v", s)
	}
	// Another price: a manual override with the reference's provenance (legacy EditBillingDialog).
	expect(t, put(trip, map[string]any{"billingEstimateThb": 1234.5, "jobCategory": "PRIMARY"}), http.StatusOK, "")
	if s := w.snap(trip); est(s) != 1234.5 || !s.manual || s.importID == nil || *s.importID == "manual" {
		t.Fatalf("override: %+v", s)
	}
	// A route without a card: manual import id, multiplier 1.
	norate, _ := w.deliveredTrip(w.O, w.partyY, "MAN-NR", "4WJ", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))
	expect(t, put(norate, map[string]any{"billingEstimateThb": 500, "jobCategory": "SUPPLEMENTARY"}), http.StatusOK, "")
	if s := w.snap(norate); est(s) != 500 || !s.manual || *s.importID != "manual" || s.multiplier != 1 || *s.tripCat != "SUPPLEMENTARY" {
		t.Fatalf("no reference: %+v", s)
	}
	for _, body := range []map[string]any{{"billingEstimateThb": 0, "jobCategory": "PRIMARY"},
		{"billingEstimateThb": 10.001, "jobCategory": "PRIMARY"}, {"billingEstimateThb": 10}, {"jobCategory": "PRIMARY"}} {
		expect(t, put(trip, body), http.StatusUnprocessableEntity, "invalid_argument")
	}
	// accounting:override_price and the own fleet.
	expect(t, w.do(http.MethodPut, "/v1/trips/"+trip+"/billing/manual", w.login("us.o"),
		map[string]any{"billingEstimateThb": 10, "jobCategory": "PRIMARY"}), http.StatusForbidden, "permission_denied")
	expect(t, w.do(http.MethodPut, "/v1/trips/"+uuid.NewString()+"/billing/manual", tok,
		map[string]any{"billingEstimateThb": 10, "jobCategory": "PRIMARY"}), http.StatusNotFound, "not_found")
}

// --- AC: every unpriced row carries an R62 reason; the safety net prices what no event did ------------------------

func TestUnpricedReasonsAndSafetyNet(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	recent := now.Add(-10 * time.Minute)
	noCustomer, _ := w.deliveredTrip(w.O, "", "NO-CUST", "6WH", "PRIMARY", recent, recent)
	noRate, _ := w.deliveredTrip(w.O, w.partyY, "NO-RATE", "18WH", "PRIMARY", recent, recent)
	noClass, _ := w.deliveredTrip(w.O, w.partyY, "NO-CLASS", "", "PRIMARY", recent, recent)
	ndTask := w.task(taskSpec{tenant: w.O, party: w.partyY, src: "SPK-GW", dest: "SPK890103", class: "6WH", cat: "PRIMARY"})
	noDate := w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, task_id, created_at)
		VALUES ($1, 'driver', 'NO-DATE', 'delivered', 'line_haul', $2, '1970-01-01T00:00:00Z') RETURNING id::text`, w.O, ndTask)
	priced, _ := w.deliveredTrip(w.O, w.partyY, "PRICED", "6WH", "PRIMARY", recent, recent)
	old, _ := w.deliveredTrip(w.O, w.partyY, "OLD", "6WH", "PRIMARY", bkk(2026, 9, 1, 8, 0), bkk(2026, 9, 1, 9, 0))

	sbNoCustomer := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, ended_at, started_at, status)
		VALUES ($1, 'driver', $2, $2, 'completed') RETURNING id::text`, w.O, recent)
	sbNoRate := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status)
		VALUES ($1, 'driver', $2, $3, $3, 'completed') RETURNING id::text`, w.O, w.partyX, recent)
	sbNoEnd := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, started_at, status)
		VALUES ($1, 'driver', $2, $3, 'completed') RETURNING id::text`, w.O, w.partyY, recent)
	w.standbyRate(w.O, w.partyY, "2026-01-01", 450)
	sbPriced := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status)
		VALUES ($1, 'driver', $2, $3, $3, 'completed') RETURNING id::text`, w.O, w.partyY, recent)

	// The scheduler's command: a jobs row and its job.billing.safety-net message.
	var cmd jobs.Job
	if err := db.WithSystem(context.Background(), w.pool, nil, func(tx pgx.Tx) error {
		var err error
		cmd, err = jobs.Enqueue(context.Background(), tx, jobs.NewInput{Type: jobs.TypeBillingSafetyNet})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(jobs.Command{JobID: cmd.ID, Type: cmd.Type})
	j := w.computeJob()
	if err := j.Handle(context.Background(), &mq.Delivery{Queue: billing.QueueCompute, MessageID: "1",
		RoutingKey: billing.RouteSafetyNet, Body: body}); err != nil {
		t.Fatal(err)
	}
	for trip, want := range map[string]string{noCustomer: "no_customer", noRate: "no_rate", noClass: "no_vehicle_class"} {
		if s := w.snap(trip); s.estimate != nil || s.reason == nil || *s.reason != want || s.computedBy != billing.ComputedByScheduler {
			t.Fatalf("%s: %+v", want, s)
		}
	}
	if s := w.snap(priced); est(s) != 1260 {
		t.Fatalf("priced: %+v", s)
	}
	if w.snap(old).exists || w.snap(noDate).exists {
		t.Fatal("the safety net reached outside its window")
	}
	// no_rate keeps the codes the lookup found no card for; the axis is stamped on an unpriced trip.
	if c := scan[string](w, `SELECT lookup_destination_code FROM trip_billing_snapshots WHERE trip_id = $1`, noRate); c != "SPK890103" {
		t.Fatalf("lookup %q", c)
	}
	if s := w.snap(noRate); s.billingParty == nil || *s.billingParty != w.partyY || s.billingDate == nil {
		t.Fatalf("unpriced stamp: %+v", s)
	}
	// no_billing_date: no plan, delivery or creation instant (R19), through the plain compute.
	r := w.post("/v1/trips/"+noDate+"/billing/compute", w.login("ta.o"), nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["ok"] != false || r.data()["reason"] != "no_billing_date" {
		t.Fatalf("%s", r.raw)
	}
	if s := w.snap(noDate); s.reason == nil || *s.reason != "no_billing_date" {
		t.Fatalf("no_billing_date: %+v", s)
	}
	for id, want := range map[string]string{sbNoCustomer: "no_customer", sbNoRate: "no_rate"} {
		if got := scan[*string](w, `SELECT billing_unpriced_reason FROM standby_records WHERE id = $1`, id); got == nil || *got != want {
			t.Fatalf("standby %s: %v", want, got)
		}
	}
	// A record without ended_at is outside every window: priced on request, it is no_ended_at.
	r = w.post("/v1/standby/"+sbNoEnd+"/billing/compute", w.login("ta.o"), nil)
	expect(t, r, http.StatusOK, "")
	if got := scan[*string](w, `SELECT billing_unpriced_reason FROM standby_records WHERE id = $1`, sbNoEnd); got == nil || *got != "no_ended_at" {
		t.Fatalf("no_ended_at: %v (%s)", got, r.raw)
	}
	if got := scan[float64](w, `SELECT billing_estimate_thb::float8 FROM standby_records WHERE id = $1`, sbPriced); got != 450 {
		t.Fatalf("standby price %v", got)
	}
	// Every unpriced row of the database carries an R62 reason.
	if n := scan[int](w, `SELECT count(*)::int FROM trip_billing_snapshots WHERE estimate_thb IS NULL AND (unpriced_reason IS NULL
		OR unpriced_reason NOT IN ('no_customer','no_rate','no_vehicle_class','no_billing_date'))`); n != 0 {
		t.Fatalf("%d unpriced trip snapshots without a reason", n)
	}
	if n := scan[int](w, `SELECT count(*)::int FROM standby_records WHERE billing_computed_at IS NOT NULL AND billing_estimate_thb IS NULL
		AND (billing_unpriced_reason IS NULL OR billing_unpriced_reason NOT IN ('no_customer','no_rate','no_ended_at'))`); n != 0 {
		t.Fatalf("%d unpriced standby records without a reason", n)
	}
	// The job ended with its counts.
	status := scan[string](w, `SELECT status FROM jobs WHERE id = $1`, cmd.ID)
	if status != jobs.StatusSucceeded {
		t.Fatalf("safety net job %s", status)
	}
}

// --- AC: a carrier tenant that ran a trip cannot read its billing --------------------------------------------------

func TestCarrierCannotReadBilling(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	// Carrier A (working for the own fleet) ran the trip and the standby record; the own fleet prices them (R61).
	trip, _ := w.deliveredTrip(w.A, w.partyY, "A-TRIP", "6WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))
	w.standbyRate(w.O, w.partyY, "2026-01-01", 450)
	sb := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status)
		VALUES ($1, 'driver', $2, $3, $3, 'completed') RETURNING id::text`, w.A, w.partyY, bkk(2026, 9, 15, 10, 0))
	j := w.computeJob()
	if err := w.deliver(j, billing.RouteTripDelivered, w.appendEvent(billing.RouteTripDelivered, "trip", trip, w.A), trip); err != nil {
		t.Fatal(err)
	}
	if err := w.deliver(j, billing.RouteStandbyCompleted, w.appendEvent(billing.RouteStandbyCompleted, "standby", sb, w.A), sb); err != nil {
		t.Fatal(err)
	}
	if s := w.snap(trip); est(s) != 1160 || s.tenant != w.O {
		t.Fatalf("carrier trip price: %+v", s)
	}
	if got := scan[string](w, `SELECT billing_tenant_id::text FROM standby_records WHERE id = $1`, sb); got != w.O {
		t.Fatalf("standby billing tenant %s", got)
	}

	taa := w.login("ta.a")
	// The plain compute answers the carrier without an amount; forcing is the own fleet's.
	r := w.post("/v1/trips/"+trip+"/billing/compute", taa, nil)
	expect(t, r, http.StatusOK, "")
	if _, ok := r.data()["billingEstimateThb"]; ok || r.data()["skipped"] != true {
		t.Fatalf("carrier read an amount: %s", r.raw)
	}
	expect(t, w.post("/v1/trips/"+trip+"/billing/compute", taa, map[string]any{"forceRecompute": true}),
		http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/trips/"+trip+"/job-category", taa, map[string]any{"jobCategory": "PRIMARY"}),
		http.StatusForbidden, "permission_denied")
	r = w.post("/v1/standby/"+sb+"/billing/compute", taa, nil)
	expect(t, r, http.StatusOK, "")
	if _, ok := r.data()["billingEstimateThb"]; ok {
		t.Fatalf("carrier read a standby amount: %s", r.raw)
	}
	// Its RLS reads no snapshot of the trip (the snapshot carries the own fleet, R61).
	tb := w.get("/v1/billingtest/trip-billing?ids="+trip, taa)
	expect(t, tb, http.StatusOK, "")
	if row, _ := tb.data()["trips"].(map[string]any)[trip].(map[string]any); row != nil && row["estimateThb"] != nil {
		t.Fatalf("carrier RLS read the snapshot: %s", tb.raw)
	}
	if n := scan[int](w, `SELECT count(*)::int FROM trip_billing_snapshots WHERE trip_id = $1`, trip); n != 1 {
		t.Fatal("no snapshot")
	}
	// The standby billing columns are cleared by the service projection for the carrier, kept for the own fleet.
	rec := w.standbyRecord(sb)
	pA := &authz.Principal{TenantID: uuidPtr(w.A), TenantRole: authz.TenantAdmin, TenantKind: authz.TenantKindCarrier}
	pO := &authz.Principal{TenantID: uuidPtr(w.O), TenantRole: authz.TenantAdmin, TenantKind: authz.TenantKindOwnFleet}
	if v := operations.Project(pA, rec, uuid.Nil); v.Billing || v.Standby.BillingEstimateThb != nil || v.Standby.BillingLastEventID != nil {
		t.Fatalf("carrier projection: %+v", v.Standby)
	}
	if v := operations.Project(pO, rec, uuid.Nil); !v.Billing || v.Standby.BillingEstimateThb == nil || *v.Standby.BillingEstimateThb != 450 {
		t.Fatalf("own fleet projection: %+v", v.Standby)
	}
	// Independent carrier C cannot even reach the trip.
	expect(t, w.post("/v1/trips/"+trip+"/billing/compute", w.login("ta.c"), nil), http.StatusNotFound, "not_found")
}

func uuidPtr(s string) *uuid.UUID {
	id := uuid.MustParse(s)
	return &id
}

// standbyRecord reads a standby record as the operations service does.
func (w *world) standbyRecord(id string) operations.Standby {
	w.t.Helper()
	var rec operationsdb.StandbyRecord
	if err := db.WithSystem(context.Background(), w.pool, nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT * FROM standby_records WHERE id = $1`, id)
		if err != nil {
			return err
		}
		rec, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[operationsdb.StandbyRecord])
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	return rec
}

// --- standby: compute, the customer repair, the period lock ----------------------------------------------------------

func TestStandbyComputeAndCustomer(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	tok := w.login("ta.o")
	day := bkk(2026, 9, 15, 10, 0)
	sb := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, ended_at, started_at, status)
		VALUES ($1, 'driver', $2, $2, 'completed') RETURNING id::text`, w.O, day)
	rate := w.standbyRate(w.O, w.partyY, "2026-09-01", 450)
	r := w.post("/v1/standby/"+sb+"/billing/compute", tok, nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["ok"] != false || r.data()["reason"] != "no_customer" {
		t.Fatalf("%s", r.raw)
	}
	// The admin's repair names the customer (its id, the customer the party bills), then the record prices.
	r = w.patch("/v1/standby/"+sb+"/customer", tok, map[string]any{"customerId": w.customerY})
	expect(t, r, http.StatusOK, "")
	if num(r.data()["billingEstimateThb"]) != 450 {
		t.Fatalf("%s", r.raw)
	}
	var source, entry, from, tenant, resolved string
	var last int64
	if err := w.etl.QueryRow(context.Background(), `SELECT billing_rate_source, billing_rate_entry_id::text,
		billing_effective_from_date::text, billing_tenant_id::text, customer_resolved_from, billing_last_event_id
		FROM standby_records WHERE id = $1`, sb).Scan(&source, &entry, &from, &tenant, &resolved, &last); err != nil {
		t.Fatal(err)
	}
	if source != "standby_rate" || entry != rate || from != "2026-09-01" || tenant != w.O || resolved != "manual" {
		t.Fatalf("standby snapshot %s %s %s %s %s", source, entry, from, tenant, resolved)
	}
	// Its standby.customer_assigned is stale for billing.compute (the endpoint priced already).
	assigned := scan[int64](w, `SELECT max(id) FROM outbox_events WHERE routing_key = 'standby.customer_assigned' AND aggregate_id = $1`, sb)
	if assigned >= last {
		t.Fatalf("customer_assigned %d not below last_event_id %d", assigned, last)
	}
	before := w.events(billing.RouteStandbyPriced, sb)
	if err := w.deliver(w.computeJob(), billing.RouteStandbyCustomerAssigned, assigned, sb); err != nil {
		t.Fatal(err)
	}
	if w.events(billing.RouteStandbyPriced, sb) != before {
		t.Fatal("a stale customer_assigned priced again")
	}
	// Repricing a priced record needs recompute_force; a sent period leaves it alone.
	expect(t, w.patch("/v1/standby/"+sb+"/customer", tok, map[string]any{"customerId": "not-a-uuid"}),
		http.StatusUnprocessableEntity, "invalid_argument")
	w.lockPeriod(w.partyY, "TTP-202609-001", 2026, 9, "sent")
	r = w.post("/v1/standby/"+sb+"/billing/compute", tok, map[string]any{"forceRecompute": true})
	expect(t, r, http.StatusOK, "")
	if r.data()["blocked"] != true || r.data()["invoiceNumber"] != "TTP-202609-001" || num(r.data()["billingEstimateThb"]) != 450 {
		t.Fatalf("%s", r.raw)
	}
	expect(t, w.patch("/v1/standby/"+sb+"/customer", tok, map[string]any{"customerId": w.partyX}),
		http.StatusConflict, billing.CodeBillingPeriodLocked)
	if got := scan[string](w, `SELECT customer_party_id::text FROM standby_records WHERE id = $1`, sb); got != w.partyY {
		t.Fatalf("a blocked repair changed the customer: %s", got)
	}
	expect(t, w.post("/v1/standby/"+sb+"/billing/compute", w.login("us.o"), map[string]any{"forceRecompute": true}),
		http.StatusForbidden, "permission_denied")
}

// --- jobs: backfills and the impact report --------------------------------------------------------------------------

// runJob hands the billing.backfill consumer the command of job id and returns the finished job's result.
func (w *world) runJob(id string) map[string]any {
	w.t.Helper()
	var key string
	var payload []byte
	if err := w.etl.QueryRow(context.Background(), `SELECT routing_key, payload FROM outbox_events WHERE aggregate_id = $1
		AND routing_key LIKE 'job.%'`, id).Scan(&key, &payload); err != nil {
		w.t.Fatal(err)
	}
	j, err := billing.NewBackfillJob(billing.JobDeps{Pool: w.pool, Locks: jobs.NewRedisLocker(w.rdb, w.ks),
		Now: func() time.Time { return now }, Log: zerolog.Nop()})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := j.Handle(context.Background(), &mq.Delivery{Queue: billing.QueueBackfill, MessageID: "1", RoutingKey: key,
		Body: payload}); err != nil {
		w.t.Fatal(err)
	}
	r := w.get("/v1/jobs/"+id, w.login("ta.o"))
	expect(w.t, r, http.StatusOK, "")
	if r.data()["status"] != jobs.StatusSucceeded {
		w.t.Fatalf("job: %s", r.raw)
	}
	res, _ := r.data()["result"].(map[string]any)
	return res
}

func TestBackfillJobs(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	tok := w.login("ta.o")
	a, _ := w.deliveredTrip(w.O, w.partyY, "BF-A", "6WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 15, 10, 0))
	b, _ := w.deliveredTrip(w.O, w.partyY, "BF-B", "18WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 16, 10, 0))
	c, _ := w.deliveredTrip(w.O, "", "BF-C", "6WH", "PRIMARY", bkk(2026, 9, 14, 8, 0), bkk(2026, 9, 17, 10, 0))
	outside, _ := w.deliveredTrip(w.O, w.partyY, "BF-OUT", "6WH", "PRIMARY", bkk(2026, 8, 14, 8, 0), bkk(2026, 8, 31, 18, 0))
	// The party's run reaches its unstamped trips (task party), never another party's or the window's outside.
	r := w.post("/v1/jobs/billing.backfill-trips", tok, map[string]any{"fromDateStr": "2026-09-01", "toDateStr": "2026-09-30",
		"customerId": w.customerY})
	expect(t, r, http.StatusAccepted, "")
	jobID := str(r.data()["jobId"])
	// A second run of the same scope is refused while the first holds lock:job; the standby backfill runs beside it.
	expect(t, w.post("/v1/jobs/billing.backfill-trips", tok, map[string]any{"fromDateStr": "2026-09-01",
		"toDateStr": "2026-09-30", "customerId": w.customerY}), http.StatusConflict, "already_exists")
	sbJob := w.post("/v1/jobs/billing.backfill-standby", tok, map[string]any{"fromDateStr": "2026-09-01",
		"toDateStr": "2026-09-30", "customerId": w.customerY})
	expect(t, sbJob, http.StatusAccepted, "")
	res := w.runJob(jobID)
	if num(res["scanned"]) != 2 || num(res["eligible"]) != 2 || num(res["written"]) != 1 || num(res["failed"]) != 1 ||
		res["capped"] != false {
		t.Fatalf("backfill: %v", res)
	}
	if est(w.snap(a)) != 1160 || w.snap(b).reason == nil || w.snap(c).exists || w.snap(outside).exists {
		t.Fatal("backfill touched the wrong trips")
	}
	// The lock was released with the job.
	expect(t, w.post("/v1/jobs/billing.backfill-trips", tok, map[string]any{"fromDateStr": "2026-09-01",
		"toDateStr": "2026-09-30", "customerId": w.customerY, "maxWrite": 1}), http.StatusAccepted, "")
	_ = w.runJob(str(sbJob.data()["jobId"]))

	// forceRecompute over every party, with a sent September for Y: blocked, counted with its invoice.
	w.lockPeriod(w.partyY, "TTP-202609-009", 2026, 9, "sent")
	r = w.post("/v1/jobs/billing.backfill-trips", tok, map[string]any{"fromDateStr": "2026-09-01", "toDateStr": "2026-09-30",
		"forceRecompute": true})
	expect(t, r, http.StatusAccepted, "")
	res = w.runJob(str(r.data()["jobId"]))
	invoices, _ := res["blockedInvoices"].([]any)
	if num(res["scanned"]) != 3 || num(res["blocked"]) != 1 || len(invoices) != 1 || invoices[0] != "TTP-202609-009" {
		t.Fatalf("forced backfill: %v", res)
	}

	// Parameters: dates required and ordered, bounds, recompute_force, own fleet.
	for _, body := range []map[string]any{{"toDateStr": "2026-09-30"}, {"fromDateStr": "2026-09-30", "toDateStr": "2026-09-01"},
		{"fromDateStr": "2026-09-01", "toDateStr": "2026-09-30", "maxScan": 2001},
		{"fromDateStr": "2026-09-01", "toDateStr": "2026-09-30", "maxWrite": 0}} {
		expect(t, w.post("/v1/jobs/billing.backfill-trips", tok, body), http.StatusUnprocessableEntity, "invalid_argument")
	}
	expect(t, w.post("/v1/jobs/billing.backfill-trips", w.login("us.o"), map[string]any{"fromDateStr": "2026-09-01",
		"toDateStr": "2026-09-30"}), http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/jobs/billing.backfill-trips", w.login("ta.a"), map[string]any{"fromDateStr": "2026-09-01",
		"toDateStr": "2026-09-30"}), http.StatusForbidden, "permission_denied")

	// The impact report is read-only: a band round stored with the old floor and a trip delivered at 03:00 on a switch day.
	w.exec(`INSERT INTO customer_fuel_rate_adjustments (tenant_id, billing_party_id, effective_from_date, effective_from_at,
		rate_multiplier, add_thb_per_trip, reference_fuel_price_thb, fuel_band_enabled, fuel_band_baseline_floor,
		fuel_band_thb_per_baht) VALUES ($1, $2, '2026-09-20', bkk_midnight('2026-09-20'), 1, 20, 42.00, true, 41, 10)`, w.O, w.partyY)
	night, _ := w.deliveredTrip(w.O, w.partyY, "NIGHT", "6WH", "PRIMARY", bkk(2026, 9, 19, 8, 0), bkk(2026, 9, 20, 3, 0))
	w.exec(`UPDATE trip_records SET billing_party_id = $2 WHERE id = $1`, night, w.partyY)
	snapshots := scan[int](w, `SELECT count(*)::int FROM trip_billing_snapshots`)
	r = w.post("/v1/jobs/billing.impact-report", tok, map[string]any{"fromDateStr": "2026-09-01", "toDateStr": "2026-09-30"})
	expect(t, r, http.StatusAccepted, "")
	rep := w.runJob(str(r.data()["jobId"]))
	bands, _ := rep["bandImpacts"].([]any)
	bounds, _ := rep["boundaryImpacts"].([]any)
	if rep["ok"] != true || len(bands) != 1 || len(bounds) != 1 {
		t.Fatalf("impact report: %v", rep)
	}
	band := bands[0].(map[string]any)
	// 42.00 sits in the band (41, 42]: floor 41, so the corrected surcharge over baseline 41 is 0, not the stored 20.
	if band["customerId"] != w.customerY || num(band["correctedAddThbPerTrip"]) != 0 || num(band["deltaThbPerTrip"]) != -20 {
		t.Fatalf("band: %v", band)
	}
	if bd := bounds[0].(map[string]any); bd["tripId"] != night || bd["shouldBeDateStr"] != "2026-09-20" {
		t.Fatalf("boundary: %v", bd)
	}
	if n := scan[int](w, `SELECT count(*)::int FROM trip_billing_snapshots`); n != snapshots {
		t.Fatal("the impact report wrote")
	}
	expect(t, w.post("/v1/jobs/billing.impact-report", tok, map[string]any{"fromDateStr": "2026-09-01", "toDateStr": "2026-09-30",
		"forceRecompute": true}), http.StatusUnprocessableEntity, "invalid_argument")
}

// --- the shim target (computeTripBillingSnapshot) ---------------------------------------------------------------------

func TestShimTargetComputesForTheDriver(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	drv := w.driver(w.O, "คนขับ", "0811111111")
	other := w.driver(w.O, "อีกคน", "0822222222")
	task := w.task(taskSpec{tenant: w.O, party: w.partyY, src: "SPK-GW", dest: "SPK890103", class: "6WH", cat: "PRIMARY", driver: drv})
	trip := w.tripOf(w.O, "", "SHIM-1", task, drv, bkk(2026, 9, 15, 10, 0), nil, "", false)
	w.exec(`UPDATE trip_records SET legacy_doc_id = 'fsTripDoc1' WHERE id = $1`, trip)
	p := &authz.Principal{TenantID: uuidPtr(w.O), TenantRole: authz.Driver, DriverID: uuidPtr(drv)}
	ctx := context.Background()
	out, err := w.svc.ComputeTripForShim(ctx, p, "", "fsTripDoc1", billing.ComputeInput{})
	if err != nil || !out.OK || out.Skipped {
		t.Fatalf("shim compute: %+v %v", out, err)
	}
	if s := w.snap(trip); est(s) != 1160 || s.computedBy != billing.ComputedByMobile {
		t.Fatalf("shim price: %+v", s)
	}
	b, _ := json.Marshal(out)
	if string(b) != `{"ok":true}` {
		t.Fatalf("the shim answer carries more than ok/skipped/error: %s", b)
	}
	// Again (by trip number): already priced, a skip.
	if out, err := w.svc.ComputeTripForShim(ctx, p, "", "SHIM-1", billing.ComputeInput{}); err != nil || !out.Skipped {
		t.Fatalf("second call: %+v %v", out, err)
	}
	// A trip not mirrored yet; another driver's trip; forceRecompute; a staff principal.
	if out, err := w.svc.ComputeTripForShim(ctx, p, "", "unknownDoc", billing.ComputeInput{}); err != nil || out.Error != billing.ReasonNotMirrored {
		t.Fatalf("not mirrored: %+v %v", out, err)
	}
	po := &authz.Principal{TenantID: uuidPtr(w.O), TenantRole: authz.Driver, DriverID: uuidPtr(other)}
	if _, err := w.svc.ComputeTripForShim(ctx, po, "", "fsTripDoc1", billing.ComputeInput{}); httpStatus(err) != http.StatusForbidden {
		t.Fatalf("another driver: %v", err)
	}
	if _, err := w.svc.ComputeTripForShim(ctx, p, "", "fsTripDoc1", billing.ComputeInput{ForceRecompute: true}); httpStatus(err) != http.StatusUnprocessableEntity {
		t.Fatalf("force: %v", err)
	}
	staff := &authz.Principal{TenantID: uuidPtr(w.O), TenantRole: authz.TenantAdmin}
	if _, err := w.svc.ComputeTripForShim(ctx, staff, "", "fsTripDoc1", billing.ComputeInput{}); httpStatus(err) != http.StatusForbidden {
		t.Fatalf("staff: %v", err)
	}
}

func httpStatus(err error) int {
	if he, ok := errors.AsType[*httpx.Error](err); ok {
		return he.Status
	}
	return 0
}

// --- multi-drop: the flat extra-stop fee, the breakdown, a manual amount, too few stops ----------------------------

func TestMultiDropPricing(t *testing.T) {
	w := newWorld(t)
	w.pricingFixture()
	tok := w.login("ta.o")
	w.exec(`INSERT INTO customer_service_fees (tenant_id, billing_party_id, fee_type, amount_thb, unit)
		VALUES ($1, $2, 'extra_stop', 300, 'per_stop')`, w.O, w.partyY)
	task := w.task(taskSpec{tenant: w.O, party: w.partyY, src: "SPK-GW", dest: "SPK890103", class: "6WH", cat: "PRIMARY"})
	multi := w.tripOf(w.O, "", "MULTI", task, "", bkk(2026, 9, 15, 10, 0), nil, "", true)
	w.exec(`INSERT INTO trip_delivery_stops (trip_id, stop_index, destination_raw, status, completed_seq) VALUES
		($1, 1, 'SPK777', 'delivered', 1), ($1, 2, 'SPK890103', 'delivered', 2)`, multi)
	r := w.post("/v1/trips/"+multi+"/billing/compute", tok, nil)
	expect(t, r, http.StatusOK, "")
	// FLAT: the planned destination is the base leg (1200 - 40 fuel), every other delivered stop the 300 fee.
	if num(r.data()["billingEstimateThb"]) != 1460 {
		t.Fatalf("%s", r.raw)
	}
	var base, stop float64
	var isMulti bool
	if err := w.etl.QueryRow(context.Background(), `SELECT base_rate_thb::float8, stop_charge_thb::float8, is_multi_delivery
		FROM trip_billing_snapshots WHERE trip_id = $1`, multi).Scan(&base, &stop, &isMulti); err != nil {
		t.Fatal(err)
	}
	if base != 1160 || stop != 300 || !isMulti {
		t.Fatalf("multi snapshot base %v stop %v multi %v", base, stop, isMulti)
	}
	if n := scan[int](w, `SELECT count(*)::int FROM trip_billing_stop_breakdown WHERE trip_id = $1`, multi); n != 2 {
		t.Fatalf("breakdown rows %d", n)
	}
	// A manual amount replaces the whole price: one amount, no stops to invoice.
	expect(t, w.do(http.MethodPut, "/v1/trips/"+multi+"/billing/manual", tok, map[string]any{"billingEstimateThb": 1500,
		"jobCategory": "PRIMARY"}), http.StatusOK, "")
	if err := w.etl.QueryRow(context.Background(), `SELECT is_multi_delivery FROM trip_billing_snapshots WHERE trip_id = $1`, multi).
		Scan(&isMulti); err != nil || isMulti {
		t.Fatalf("manual multi: %v %v", isMulti, err)
	}
	if n := scan[int](w, `SELECT count(*)::int FROM trip_billing_stop_breakdown WHERE trip_id = $1`, multi); n != 0 {
		t.Fatalf("a manual amount kept %d breakdown rows", n)
	}
	if s := w.snap(multi); est(s) != 1500 || !s.manual {
		t.Fatalf("manual multi: %+v", s)
	}

	// Too few delivered stops: not a price, not an unpriced reason (R62), nothing written; a manual amount still goes.
	short := w.tripOf(w.O, "", "SHORT", task, "", bkk(2026, 9, 15, 11, 0), nil, "", true)
	w.exec(`INSERT INTO trip_delivery_stops (trip_id, stop_index, destination_raw, status, completed_seq) VALUES
		($1, 1, 'SPK890103', 'delivered', 1), ($1, 2, 'SPK777', 'pending', NULL)`, short)
	r = w.post("/v1/trips/"+short+"/billing/compute", tok, nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["ok"] != false || r.data()["reason"] != billing.ReasonMultiInsufficientStops {
		t.Fatalf("%s", r.raw)
	}
	if w.snap(short).exists {
		t.Fatal("multi_insufficient_stops wrote a snapshot")
	}
	expect(t, w.post("/v1/trips/"+short+"/job-category", tok, map[string]any{"jobCategory": "SUPPLEMENTARY"}),
		http.StatusConflict, billing.CodeFailedPrecondition)
	expect(t, w.do(http.MethodPut, "/v1/trips/"+short+"/billing/manual", tok, map[string]any{"billingEstimateThb": 900,
		"jobCategory": "PRIMARY"}), http.StatusOK, "")
	if s := w.snap(short); est(s) != 900 || !s.manual || *s.importID != "manual" {
		t.Fatalf("manual on a short multi-drop: %+v", s)
	}
}
