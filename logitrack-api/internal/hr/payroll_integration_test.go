//go:build integration

package hr_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// config posts the June 2026 compensation config of O (and a later version that June must not use).
func (w *world) config(t *testing.T) {
	t.Helper()
	ta := w.login("ta.o")
	r := w.post("/v1/payroll/config", ta, map[string]any{
		"effectiveFrom":      "2026-01-01",
		"fuelIncentiveTiers": []any{map[string]any{"minKmPerLitre": 10, "amountThb": 1000}},
		"tripVolumeTiers":    []any{map[string]any{"minTrips": 3, "amountThb": 1000}, map[string]any{"minTrips": 5, "amountThb": 1500}},
		"sso":                map[string]any{},
		"penaltyTypes": []any{map[string]any{"code": "LATE_SLA", "nameEn": "Late / SLA breach", "nameTh": "ล่าช้า / เกิน SLA",
			"defaultAmountThb": 3000}},
	})
	expect(t, r, http.StatusCreated, "")
	r = w.post("/v1/payroll/config", ta, map[string]any{"effectiveFrom": "2026-07-15", "weekdayRateThb": 500})
	expect(t, r, http.StatusCreated, "")
}

// scenario writes June 2026 for O's drivers (see TestPayrollRun for the expected pay).
func (w *world) scenario() (penalties map[string]string) {
	// Holidays: a public one on Wed 3 June; carrier C's own on Thu 4 June (not O's).
	w.exec(`INSERT INTO holidays (tenant_id, holiday_date, holiday_type, status, name) VALUES (NULL, '2026-06-03', 'public', 'published', 'Queen')`)
	w.exec(`INSERT INTO holidays (tenant_id, holiday_date, holiday_type, status, name) VALUES ($1, '2026-06-04', 'company', 'published', 'C day')`, w.C)
	// dO's delivered trips (one run by carrier A: a driver's trips count whatever tenant ran them).
	w.trip(w.O, w.dO, "T1", bkk(2026, 6, 2, 9, 0), false)   // Tue: weekday
	w.trip(w.A, w.dO, "T2", bkk(2026, 6, 3, 10, 0), false)  // public holiday
	w.trip(w.O, w.dO, "T3", bkk(2026, 6, 4, 10, 0), false)  // C's holiday only: weekday for O
	w.trip(w.O, w.dO, "T4", bkk(2026, 6, 7, 10, 0), false)  // Sunday
	w.trip(w.O, w.dO, "T5", bkk(2026, 6, 8, 15, 0), true)   // multi-stop: no pay, still a driving window (8th)
	w.trip(w.O, w.dO, "T6", bkk(2026, 6, 17, 10, 0), false) // R2 weekday
	w.trip(w.O, w.dO, "T7", bkk(2026, 6, 21, 10, 0), false) // R2 Sunday (window 20th)
	w.trip(w.O, w.dO, "T8", bkk(2026, 7, 1, 8, 0), false)   // July
	w.exec(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, driver_id) VALUES ($1, 'trip', 'T9', 'in_transit', 'first_mile', $2)`, w.O, w.dO)
	w.trip(w.O, w.dO2, "U1", bkk(2026, 6, 18, 10, 0), false) // dO2's only trip (R2)
	w.trip(w.A, w.dA, "V1", bkk(2026, 6, 2, 10, 0), false)   // carrier A's driver: never in O's runs (per tenant, §6.18 #28)

	// dO's own task on the 12th (cancelled, no trip): the window drives (keyed by drivers.id, the fix of
	// fn:driverCompensation.ts:199-204: the legacy query matched the auth uid and missed it).
	w.task(w.O, w.dO, "", bkk(2026, 6, 12, 8, 0), nil, "cancelled")
	// dO helps dO2.
	for _, h := range []struct {
		plan    time.Time
		checkIn *time.Time
	}{
		{bkk(2026, 6, 5, 8, 0), at(bkk(2026, 6, 5, 13, 0))},   // window 5th: paid (R1)
		{bkk(2026, 6, 5, 9, 0), at(bkk(2026, 6, 6, 1, 0))},    // window 5th again: one day
		{bkk(2026, 6, 8, 8, 0), at(bkk(2026, 6, 8, 16, 0))},   // window 8th: the multi-stop trip drove
		{bkk(2026, 6, 12, 8, 0), at(bkk(2026, 6, 12, 13, 0))}, // window 12th: own task
		{bkk(2026, 6, 14, 8, 0), at(bkk(2026, 6, 15, 2, 0))},  // window 14th: paid (R1)
		{bkk(2026, 6, 15, 8, 0), at(bkk(2026, 6, 16, 11, 0))}, // window 15th: paid (R1, not R2)
		{bkk(2026, 6, 20, 8, 0), at(bkk(2026, 6, 20, 13, 0))}, // window 20th: T7 drove
		{bkk(2026, 6, 25, 8, 0), nil},                         // no check-in: plan noon, window 25th: paid (R2)
		{bkk(2026, 5, 30, 8, 0), at(bkk(2026, 5, 31, 13, 0))}, // May
	} {
		w.task(w.O, w.dO2, w.dO, h.plan, h.checkIn, "completed")
	}
	// Penalties: deducted in (incurred_at, id) order; a cancelled one never.
	penalties = map[string]string{
		"P1": w.id(`INSERT INTO driver_penalties (tenant_id, driver_id, type_code, type_name_snapshot, total_thb, remaining_thb,
			installments_total, status, incurred_at) VALUES ($1, $2, 'LATE_SLA', 'Late / SLA breach', 3000, 3000, 3, 'pending', $3)
			RETURNING id::text`, w.O, w.dO, bkk(2026, 6, 1, 9, 0)),
		"P2": w.id(`INSERT INTO driver_penalties (tenant_id, driver_id, type_code, total_thb, remaining_thb, installments_total, status,
			incurred_at) VALUES ($1, $2, 'DAMAGE', 500, 500, 1, 'pending', $3) RETURNING id::text`, w.O, w.dO, bkk(2026, 6, 10, 9, 0)),
		"P3": w.id(`INSERT INTO driver_penalties (tenant_id, driver_id, type_code, total_thb, remaining_thb, installments_total, status,
			incurred_at) VALUES ($1, $2, 'X', 900, 900, 1, 'cancelled', $3) RETURNING id::text`, w.O, w.dO, bkk(2026, 5, 1, 9, 0)),
		"P5": w.id(`INSERT INTO driver_penalties (tenant_id, driver_id, type_code, total_thb, remaining_thb, installments_total, status,
			incurred_at) VALUES ($1, $2, 'LATE_SLA', 1000, 1000, 1, 'pending', $3) RETURNING id::text`, w.O, w.dO2, bkk(2026, 6, 2, 9, 0)),
	}
	return penalties
}

type line struct {
	Type, Category, Name string
	Amount               float64
}

func lines(d map[string]any) []line {
	var out []line
	items, _ := d["lineItems"].([]any)
	for i, it := range items {
		m := it.(map[string]any)
		if int(num(m["lineNo"])) != i {
			panic("line numbers are not 0..n-1")
		}
		out = append(out, line{str(m["type"]), str(m["category"]), str(m["name"]), num(m["amountThb"])})
	}
	return out
}

// runOf finds the driver's run in a list.
func runOf(t *testing.T, list []map[string]any, driver string) map[string]any {
	t.Helper()
	for _, r := range list {
		if str(r["driverId"]) == driver {
			return r
		}
	}
	t.Fatalf("no run of %s in %v", driver, list)
	return nil
}

// TestPayrollRun is the June 2026 run of O through the API and the worker consumer: R1 and R2 pay, the own-task fix,
// draft-only regeneration, concurrent runs refused, approval with the penalty balances and the Bangkok-dated ledger
// row in one transaction, the status transitions, and the frozen line items.
func TestPayrollRun(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	ctx := context.Background()

	// Without a config the run is refused before a job exists.
	r := w.post("/v1/jobs/payroll.run", ta, map[string]any{"period": "2026-06", "round": "R1"})
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "no_compensation_config" {
		t.Fatalf("%s", r.raw)
	}
	r = w.post("/v1/jobs/payroll.run", ta, map[string]any{"period": "2026-13", "round": "R3"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["period"] != "invalid" || f["round"] != "invalid" {
		t.Fatalf("%v", f)
	}
	w.config(t)
	pens := w.scenario()

	// lock:job:payroll.run:{tenant}:{period}:{round}: a second run of O's June R1 while one is queued is 409 with the
	// queued job; another round, or another tenant, is not blocked.
	r = w.post("/v1/jobs/payroll.run", ta, map[string]any{"period": "2026-06", "round": "R1"})
	expect(t, r, http.StatusAccepted, "")
	jobID := str(r.data()["jobId"])
	r = w.post("/v1/jobs/payroll.run", w.login("ta2.o"), map[string]any{"period": "2026-06", "round": "r1"})
	expect(t, r, http.StatusConflict, "already_exists")
	if r.detail("jobId") != jobID {
		t.Fatalf("held lock: %s", r.raw)
	}
	w.runJob(t, jobID)
	j := w.get("/v1/jobs/"+jobID, ta)
	expect(t, j, http.StatusOK, "")
	res, _ := j.data()["result"].(map[string]any)
	if str(j.data()["status"]) != "succeeded" || num(res["written"]) != 2 || num(res["skippedApproved"]) != 0 || num(res["drivers"]) != 2 {
		t.Fatalf("job %v", j.data())
	}
	if w.rdb.Exists(ctx, w.ks.JobLock("payroll.run", w.O+":2026-06:R1")).Val() != 0 {
		t.Fatal("the job lock outlived the job")
	}

	// R1: dO 2 weekday (T1, T3: C's holiday is not O's) + 2 holiday (T2 public, T4 Sunday), T5 multi-stop excluded;
	// helper windows 5th, 14th, 15th (8th: drove; 12th: own task). No incentive, no deduction in R1.
	l := w.get("/v1/payroll?period=2026-06&round=R1", ta)
	expect(t, l, http.StatusOK, "")
	if len(l.list()) != 2 {
		t.Fatalf("%s", l.raw)
	}
	if c, _ := l.meta()["statusCounts"].(map[string]any); num(c["draft"]) != 2 || num(c["approved"]) != 0 {
		t.Fatalf("counts %v", l.meta())
	}
	o1 := runOf(t, l.list(), w.dO)
	d := w.get("/v1/payroll/"+str(o1["id"]), ta)
	expect(t, d, http.StatusOK, "")
	want := []line{{"earning", "TRIP_COMMISSION", "Trip pay (weekday)", 600}, {"earning", "TRIP_COMMISSION", "Trip pay (holiday)", 700},
		{"earning", "HELPER_PAY", "Helper/training pay", 1200}}
	if got := lines(d.data()); !reflect.DeepEqual(got, want) {
		t.Fatalf("R1 lines %+v", got)
	}
	if num(d.data()["totalEarningsThb"]) != 2500 || num(d.data()["netPayThb"]) != 2500 || str(d.data()["status"]) != "draft" ||
		str(d.data()["driverName"]) != "Somchai ดี" || str(d.data()["periodStart"]) != "2026-05-31T17:00:00Z" ||
		str(d.data()["periodEnd"]) != "2026-06-15T17:00:00Z" {
		t.Fatalf("R1 run %v", d.data())
	}
	helper := d.data()["lineItems"].([]any)[2].(map[string]any)
	if num(helper["quantity"]) != 3 || num(helper["unitRateThb"]) != 400 ||
		!reflect.DeepEqual(helper["meta"].(map[string]any)["windows"], []any{"2026-06-05", "2026-06-14", "2026-06-15"}) {
		t.Fatalf("helper line %v", helper)
	}
	o2 := runOf(t, l.list(), w.dO2)
	if num(o2["totalEarningsThb"]) != 0 || num(o2["netPayThb"]) != 0 {
		t.Fatalf("dO2 R1 %v", o2)
	}

	// Regeneration rewrites draft runs only (created_at and id kept, lines replaced); dO2's run left draft.
	w.trip(w.O, w.dO, "T10", bkk(2026, 6, 9, 10, 0), false)
	r = w.post("/v1/payroll/"+str(o2["id"])+"/status", ta, map[string]any{"status": "PENDING_APPROVAL"})
	expect(t, r, http.StatusOK, "")
	job := w.generate(t, ta, "2026-06", "R1")
	if res, _ := job["result"].(map[string]any); num(res["written"]) != 1 || num(res["skippedApproved"]) != 1 {
		t.Fatalf("regenerate %v", job)
	}
	d2 := w.get("/v1/payroll/"+str(o1["id"]), ta)
	if got := lines(d2.data()); got[0].Amount != 900 || num(d2.data()["totalEarningsThb"]) != 2800 ||
		str(d2.data()["createdAt"]) != str(d.data()["createdAt"]) {
		t.Fatalf("regenerated %v", d2.data())
	}
	if w.count(`SELECT count(*) FROM payroll_runs WHERE pay_period = '2026-06' AND pay_round = 'R1'`) != 2 {
		t.Fatal("regeneration duplicated a run")
	}
	if s := w.get("/v1/payroll/"+str(o2["id"]), ta).data(); str(s["status"]) != "pending_approval" {
		t.Fatalf("pending run touched: %v", s)
	}

	// R2: dO T6 weekday + T7 Sunday, helper window 25th (20th drove), the trip-volume tier over the month's 7 payable
	// trips (T1-T4, T6, T7, T10), SSO 15000 x 5% (hired 2025), then P1 (3000 / 3 = 1000) and P2 (500) in incurred
	// order; P3 is cancelled.
	w.generate(t, ta, "2026-06", "R2")
	l = w.get("/v1/payroll?period=2026-06&round=R2", ta)
	r2 := runOf(t, l.list(), w.dO)
	d = w.get("/v1/payroll/"+str(r2["id"]), ta)
	want = []line{{"earning", "TRIP_COMMISSION", "Trip pay (weekday)", 300}, {"earning", "TRIP_COMMISSION", "Trip pay (holiday)", 350},
		{"earning", "HELPER_PAY", "Helper/training pay", 400}, {"earning", "TRIP_VOLUME_INCENTIVE", "Trip-volume incentive", 1500},
		{"deduction", "SOCIAL_SECURITY", "Social security", 750}, {"deduction", "PENALTY", "Late / SLA breach", 1000},
		{"deduction", "PENALTY", "DAMAGE", 500}}
	if got := lines(d.data()); !reflect.DeepEqual(got, want) {
		t.Fatalf("R2 lines %+v", got)
	}
	if num(d.data()["totalEarningsThb"]) != 2550 || num(d.data()["totalDeductionsThb"]) != 2250 || num(d.data()["netPayThb"]) != 300 {
		t.Fatalf("R2 totals %v", d.data())
	}
	apps := d.data()["penaltyApplications"].([]any)
	if len(apps) != 2 {
		t.Fatalf("applications %v", apps)
	}
	p1 := d.data()["lineItems"].([]any)[5].(map[string]any)
	if m := p1["meta"].(map[string]any); num(m["installmentIndex"]) != 1 || num(m["installmentsTotal"]) != 3 ||
		num(m["remainingThb"]) != 2000 || num(m["totalThb"]) != 3000 || str(p1["referenceId"]) != pens["P1"] {
		t.Fatalf("penalty line %v", p1)
	}
	// dO2: one weekday trip (300), in probation (no SSO), P5 takes what is left.
	q2 := runOf(t, l.list(), w.dO2)
	if num(q2["totalEarningsThb"]) != 300 || num(q2["totalDeductionsThb"]) != 300 || num(q2["netPayThb"]) != 0 {
		t.Fatalf("dO2 R2 %v", q2)
	}

	// Approve dO's R2 in one transaction.
	before := time.Now()
	r = w.post("/v1/payroll/"+str(r2["id"])+"/approve", ta, nil)
	expect(t, r, http.StatusOK, "")
	ledger := str(r.data()["ledgerTransactionId"])
	if str(r.data()["status"]) != "approved" || ledger == "" || str(r.data()["approvedAt"]) == "" || str(r.data()["approvedBy"]) == "" {
		t.Fatalf("approved %v", r.data())
	}
	var amount float64
	var typ, source, runID, period, round, txDate, created string
	if err := w.etl.QueryRow(ctx, `SELECT amount_thb::float8, tx_type, tx_date_source, payroll_run_id::text, pay_period, pay_round,
		tx_date::text, bkk_date(created_at)::text FROM transactions WHERE id = $1`, ledger).
		Scan(&amount, &typ, &source, &runID, &period, &round, &txDate, &created); err != nil {
		t.Fatal(err)
	}
	if amount != 300 || typ != "driver_payout" || source != "bangkok" || runID != str(r2["id"]) || period != "2026-06" || round != "R2" ||
		txDate != created || (txDate != before.In(bkkZone).Format(time.DateOnly) && txDate != time.Now().In(bkkZone).Format(time.DateOnly)) {
		t.Fatalf("ledger %v %s %s %s %s %s %s %s", amount, typ, source, runID, period, round, txDate, created)
	}
	for _, c := range []struct {
		id        string
		remaining float64
		paid      int
		status    string
	}{{pens["P1"], 2000, 1, "partially_deducted"}, {pens["P2"], 0, 1, "cleared"}, {pens["P3"], 900, 0, "cancelled"}} {
		var rem float64
		var paid int
		var st string
		if err := w.etl.QueryRow(ctx, `SELECT remaining_thb::float8, installments_paid, status FROM driver_penalties WHERE id = $1`, c.id).
			Scan(&rem, &paid, &st); err != nil {
			t.Fatal(err)
		}
		if rem != c.remaining || paid != c.paid || st != c.status {
			t.Errorf("penalty %s: %v %d %s", c.id, rem, paid, st)
		}
	}
	if w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'payroll.approved' AND aggregate_id = $1
		AND realtime_topics @> ARRAY['tenant:' || $2 || ':hr']`, str(r2["id"]), w.O) != 1 {
		t.Fatal("payroll.approved on tenant:{O}:hr")
	}
	// A second approval, or a cancellation of an approved run, is refused; its line items are frozen.
	r = w.post("/v1/payroll/"+str(r2["id"])+"/approve", ta, nil)
	expect(t, r, http.StatusConflict, "failed_precondition")
	r = w.post("/v1/payroll/"+str(r2["id"])+"/status", ta, map[string]any{"status": "cancelled"})
	expect(t, r, http.StatusConflict, "failed_precondition")
	if _, err := w.etl.Exec(ctx, `UPDATE payroll_line_items SET amount_thb = 1 WHERE payroll_run_id = $1`, str(r2["id"])); err == nil {
		t.Fatal("the lines of an approved run were writable")
	}
	r = w.post("/v1/payroll/"+str(r2["id"])+"/status", ta, map[string]any{"status": "approved"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	r = w.post("/v1/payroll/"+str(r2["id"])+"/status", ta, map[string]any{"status": "paid", "paymentMethod": "transfer"})
	expect(t, r, http.StatusOK, "")
	if str(r.data()["status"]) != "paid" || str(r.data()["paymentDate"]) == "" || str(r.data()["paymentMethod"]) != "transfer" {
		t.Fatalf("paid %v", r.data())
	}
	if w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'payroll.status_changed' AND aggregate_id = $1`, str(r2["id"])) != 1 {
		t.Fatal("payroll.status_changed")
	}

	// A regeneration after the approval leaves the paid run alone and rewrites dO2's draft.
	job = w.generate(t, ta, "2026-06", "R2")
	if res, _ := job["result"].(map[string]any); num(res["written"]) != 1 || num(res["skippedApproved"]) != 1 {
		t.Fatalf("regenerate R2 %v", job)
	}
	// dO2's draft scheduled P5; once P5 is cancelled its approval is stale until the run is regenerated.
	r = w.post("/v1/penalties/"+pens["P5"]+"/cancel", ta, nil)
	expect(t, r, http.StatusOK, "")
	r = w.post("/v1/payroll/"+str(q2["id"])+"/approve", ta, nil)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "stale_run" {
		t.Fatalf("%s", r.raw)
	}
	w.generate(t, ta, "2026-06", "R2")
	r = w.post("/v1/payroll/"+str(q2["id"])+"/approve", ta, nil)
	expect(t, r, http.StatusOK, "")
	if num(r.data()["netPayThb"]) != 300 || len(r.data()["penaltyApplications"].([]any)) != 0 {
		t.Fatalf("dO2 approved %v", r.data())
	}

	// Transitions: a pending run re-opens to draft, a draft is cancelled and re-opened.
	for _, step := range []struct{ to, code string }{{"draft", ""}, {"cancelled", ""}, {"paid", "failed_precondition"}, {"draft", ""}, {"bogus", "invalid_argument"}} {
		r = w.post("/v1/payroll/"+str(o2["id"])+"/status", ta, map[string]any{"status": step.to})
		switch step.code {
		case "":
			expect(t, r, http.StatusOK, "")
		case "invalid_argument":
			expect(t, r, http.StatusUnprocessableEntity, step.code)
		default:
			expect(t, r, http.StatusConflict, step.code)
		}
	}

	// Appendix D invariants 6 and 11 hold on what the engine wrote.
	if n := w.count(invariant6); n != 0 {
		t.Fatalf("invariant 6: %d rows", n)
	}
	if n := w.count(invariant11); n != 0 {
		t.Fatalf("invariant 11: %d rows", n)
	}
}

var bkkZone = time.FixedZone("ICT", 7*3600)

// invariant6 counts the rows Appendix D invariant 6 reports (cmd/seed/internal/seed/invariants.go inv6).
const invariant6 = `SELECT count(*) FROM (
SELECT r.id FROM payroll_runs r
LEFT JOIN (SELECT payroll_run_id,
                  COALESCE(sum(amount_thb) FILTER (WHERE item_type = 'earning'), 0)   AS e,
                  COALESCE(sum(amount_thb) FILTER (WHERE item_type = 'deduction'), 0) AS d
           FROM payroll_line_items GROUP BY 1) l ON l.payroll_run_id = r.id
WHERE r.total_earnings_thb <> COALESCE(l.e, 0) OR r.total_deductions_thb <> COALESCE(l.d, 0)
   OR r.net_pay_thb <> GREATEST(0, r.total_earnings_thb - r.total_deductions_thb)
   OR (r.status IN ('approved','paid') AND r.ledger_transaction_id IS NULL)
UNION ALL
SELECT p.id FROM driver_penalties p
LEFT JOIN (SELECT a.penalty_id, sum(a.applied_thb) AS applied, count(*) AS n
           FROM payroll_penalty_applications a JOIN payroll_runs r ON r.id = a.payroll_run_id
           WHERE r.status IN ('approved','paid') GROUP BY 1) x ON x.penalty_id = p.id
WHERE (p.legacy_doc_id IS NULL AND p.status <> 'cancelled'
       AND (p.remaining_thb <> p.total_thb - COALESCE(x.applied, 0) OR p.installments_paid <> LEAST(COALESCE(x.n, 0), p.installments_total)))
   OR (p.status = 'cleared' AND p.remaining_thb <> 0)
   OR (p.status IN ('pending','partially_deducted') AND p.remaining_thb = 0)) v`

// invariant11 counts the payroll window and ledger date rows of Appendix D invariant 11.
const invariant11 = `SELECT count(*) FROM (
SELECT id FROM payroll_runs
  WHERE period_start <> bkk_midnight(bkk_date(period_start)) OR period_end <> bkk_midnight(bkk_date(period_end))
     OR (pay_round = 'R1' AND EXTRACT(DAY FROM bkk_date(period_end)) <> 16)
UNION ALL SELECT id FROM transactions WHERE tx_date_source = 'bangkok' AND tx_date <> bkk_date(created_at)) v`

// TestPayrollTenancy: RLS and the capabilities decide who reads and changes runs.
func TestPayrollTenancy(t *testing.T) {
	w := newWorld(t)
	w.config(t)
	w.scenario()
	ta := w.login("ta.o")
	w.generate(t, ta, "2026-06", "R1")
	l := w.get("/v1/payroll", ta)
	expect(t, l, http.StatusOK, "")
	id := str(runOf(t, l.list(), w.dO)["id"])

	// A user of O without hr:view_payroll; the admins of carrier A (working for O: its reach is A only) and of C.
	expect(t, w.get("/v1/payroll", w.login("us.o")), http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/jobs/payroll.run", w.login("us.o"), map[string]any{"period": "2026-06", "round": "R1"}), http.StatusForbidden,
		"permission_denied")
	for _, who := range []string{"ta.a", "ta.c"} {
		tok := w.login(who)
		if r := w.get("/v1/payroll", tok); r.status != http.StatusOK || len(r.list()) != 0 {
			t.Fatalf("%s lists %s", who, r.raw)
		}
		expect(t, w.get("/v1/payroll/"+id, tok), http.StatusNotFound, "not_found")
		expect(t, w.post("/v1/payroll/"+id+"/approve", tok, nil), http.StatusNotFound, "not_found")
		expect(t, w.post("/v1/payroll/"+id+"/status", tok, map[string]any{"status": "cancelled"}), http.StatusNotFound, "not_found")
		// No config in their tenant: a run of theirs is refused, and O's configs are out of reach (runs are per tenant with
		// the tenant's own config, so A's drivers wait for A's own config: §6.18 #28, owner question §19.2 Q21).
		r := w.post("/v1/jobs/payroll.run", tok, map[string]any{"period": "2026-06", "round": "R1"})
		expect(t, r, http.StatusConflict, "failed_precondition")
		if r := w.get("/v1/payroll/config", tok); r.status != http.StatusOK || len(r.body["data"].([]any)) != 0 {
			t.Fatalf("%s configs %s", who, r.raw)
		}
	}
	// The job is readable by its tenant's staff holding hr:manage_payroll, not by another tenant's.
	r := w.post("/v1/jobs/payroll.run", ta, map[string]any{"period": "2026-06", "round": "R2"})
	expect(t, r, http.StatusAccepted, "")
	jobID := str(r.data()["jobId"])
	expect(t, w.get("/v1/jobs/"+jobID, w.login("ta2.o")), http.StatusOK, "")
	expect(t, w.get("/v1/jobs/"+jobID, w.login("ta.c")), http.StatusNotFound, "")
	// Unknown ids and query parameters.
	expect(t, w.get("/v1/payroll/not-a-uuid", ta), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/payroll?bogus=1", ta), http.StatusBadRequest, "bad_request")
	r = w.get("/v1/payroll?period=2026-6&round=R9&status=x&driverId=y&cursor=z", ta)
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["period"] != "invalid" || f["round"] != "invalid" || f["status"] != "invalid" || f["driverId"] != "invalid" ||
		f["cursor"] != "invalid" {
		t.Fatalf("%v", f)
	}
}

// TestPayrollPaging: keyset pages over (period_end, id), newest first.
func TestPayrollPaging(t *testing.T) {
	w := newWorld(t)
	w.config(t)
	ta := w.login("ta.o")
	for _, p := range []string{"2026-04", "2026-05", "2026-06"} {
		for _, rnd := range []string{"R1", "R2"} {
			w.generate(t, ta, p, rnd)
		}
	}
	var seen []string
	cur := ""
	for i := 0; ; i++ {
		path := "/v1/payroll?limit=5"
		if cur != "" {
			path += "&cursor=" + cur
		}
		r := w.get(path, ta)
		expect(t, r, http.StatusOK, "")
		if cur != "" && r.meta() != nil {
			t.Fatal("statusCounts on a later page")
		}
		for _, it := range r.list() {
			seen = append(seen, str(it["periodEnd"])+" "+str(it["id"]))
		}
		if cur = r.next(); cur == "" {
			break
		}
		if i > 5 {
			t.Fatal("no end")
		}
	}
	if len(seen) != 12 {
		t.Fatalf("%d runs", len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if seen[i-1] <= seen[i] {
			t.Fatalf("not newest first: %v", seen)
		}
	}
	if r := w.get("/v1/payroll?period=2026-05&status=draft&driverId="+w.dO, ta); len(r.list()) != 2 {
		t.Fatalf("%s", r.raw)
	}
}

// TestPayrollRunGuards: two generations of one period and round that slip past the Redis lock (expired, or a replayed
// command) serialise on the advisory lock and leave one run per driver; a run whose config vanished before the worker
// picked it up ends failed without writing anything, and its lock is released.
func TestPayrollRunGuards(t *testing.T) {
	w := newWorld(t)
	w.config(t)
	w.scenario()
	ta := w.login("ta.o")
	ctx := context.Background()
	lock := w.ks.JobLock("payroll.run", w.O+":2026-06:R2")
	var ids []string
	for range 2 {
		r := w.post("/v1/jobs/payroll.run", ta, map[string]any{"period": "2026-06", "round": "R2"})
		expect(t, r, http.StatusAccepted, "")
		ids = append(ids, str(r.data()["jobId"]))
		if err := w.rdb.Del(ctx, lock).Err(); err != nil { // as if the lock had expired
			t.Fatal(err)
		}
	}
	bodies := make([][]byte, len(ids))
	for i, id := range ids {
		if err := w.etl.QueryRow(ctx, `SELECT payload::text FROM outbox_events WHERE routing_key = 'job.payroll.run' AND aggregate_id = $1`,
			id).Scan(&bodies[i]); err != nil {
			t.Fatal(err)
		}
	}
	errs := make(chan error, len(ids))
	for _, body := range bodies {
		go func() {
			reg := w.newJob(t).Registration()
			errs <- reg.Handler(ctx, &mq.Delivery{Queue: reg.Queue, RoutingKey: hr.RoutePayrollJob, Body: body})
		}()
	}
	for range ids {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		if j := w.get("/v1/jobs/"+id, ta); str(j.data()["status"]) != "succeeded" {
			t.Fatalf("job %s: %v", id, j.data())
		}
	}
	if n := w.count(`SELECT count(*) FROM payroll_runs WHERE pay_period = '2026-06' AND pay_round = 'R2'`); n != 2 {
		t.Fatalf("%d runs", n)
	}
	if n := w.count(`SELECT count(*) FROM payroll_line_items l JOIN payroll_runs r ON r.id = l.payroll_run_id
		WHERE r.pay_period = '2026-06' AND r.pay_round = 'R2' AND r.driver_id = $1`, w.dO); n != 7 {
		t.Fatalf("%d lines: a concurrent generation doubled them", n)
	}
	if n := w.count(invariant6); n != 0 {
		t.Fatalf("invariant 6: %d", n)
	}

	// The config is removed after the submission: the job fails naming it, nothing is written, the lock is free.
	r := w.post("/v1/jobs/payroll.run", ta, map[string]any{"period": "2026-05", "round": "R1"})
	expect(t, r, http.StatusAccepted, "")
	id := str(r.data()["jobId"])
	w.exec(`DELETE FROM penalty_types`)
	w.exec(`DELETE FROM driver_compensation_configs`)
	w.runJob(t, id)
	j := w.get("/v1/jobs/"+id, ta)
	if str(j.data()["status"]) != "failed" || str(j.data()["error"]) != "No compensation config exists — create one in Compensation Config first" {
		t.Fatalf("%v", j.data())
	}
	if n := w.count(`SELECT count(*) FROM payroll_runs WHERE pay_period = '2026-05'`); n != 0 {
		t.Fatalf("%d runs written by a failed job", n)
	}
	if w.rdb.Exists(ctx, w.ks.JobLock("payroll.run", w.O+":2026-05:R1")).Val() != 0 {
		t.Fatal("the failed job kept its lock")
	}
	// A redelivered command of a finished job is a no-op.
	w.runJob(t, id)
}
