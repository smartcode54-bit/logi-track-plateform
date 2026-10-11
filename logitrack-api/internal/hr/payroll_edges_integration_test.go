//go:build integration

package hr_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// runFor is GET /v1/payroll/{id} of the driver's only run of a period and round.
func (w *world) runFor(t *testing.T, bearer, driver, period, round string) map[string]any {
	t.Helper()
	l := w.get("/v1/payroll?period="+period+"&round="+round+"&driverId="+driver, bearer)
	expect(t, l, http.StatusOK, "")
	if len(l.list()) != 1 {
		t.Fatalf("runs of %s in %s %s: %s", driver, period, round, l.raw)
	}
	d := w.get("/v1/payroll/"+str(l.list()[0]["id"]), bearer)
	expect(t, d, http.StatusOK, "")
	return d.data()
}

// penaltyState reads a penalty's balance, installment count and status.
func (w *world) penaltyState(t *testing.T, id string) (remaining float64, paid int, status string) {
	t.Helper()
	if err := w.etl.QueryRow(context.Background(), `SELECT remaining_thb::float8, installments_paid, status FROM driver_penalties
		WHERE id = $1`, id).Scan(&remaining, &paid, &status); err != nil {
		t.Fatal(err)
	}
	return remaining, paid, status
}

// r2Weekdays are the first n weekdays of a month's R2 (from the 16th), at 10:00 Bangkok.
func r2Weekdays(y int, m time.Month, n int) []time.Time {
	var out []time.Time
	for d := 16; len(out) < n; d++ {
		t := bkk(y, m, d, 10, 0)
		if t.Weekday() != time.Saturday && t.Weekday() != time.Sunday {
			out = append(out, t)
		}
	}
	return out
}

// TestPayrollConfigMonthBoundary: POST /v1/payroll/config stores a Bangkok day as Bangkok midnight, so a version dated
// the 1st of a month sits exactly on the previous month's exclusive end. A run uses the version in effect on its
// month's last Bangkok day (effective_from_at < the month end): that version prices its own month, never the one
// before; GET ?at= stays inclusive (T44 review).
func TestPayrollConfigMonthBoundary(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	r := w.post("/v1/payroll/config", ta, map[string]any{"effectiveFrom": "2026-01-01", "weekdayRateThb": 300})
	expect(t, r, http.StatusCreated, "")
	jan := str(r.data()["id"])
	r = w.post("/v1/payroll/config", ta, map[string]any{"effectiveFrom": "2026-07-01", "weekdayRateThb": 500})
	expect(t, r, http.StatusCreated, "")
	jul := str(r.data()["id"])
	if str(r.data()["effectiveFrom"]) != "2026-06-30T17:00:00Z" {
		t.Fatalf("the July version is not at June's exclusive end: %v", r.data())
	}
	w.trip(w.O, w.dO2, "J1", bkk(2026, 6, 2, 10, 0), false)  // Tue 2 June: R1
	w.trip(w.O, w.dO2, "J2", bkk(2026, 6, 30, 23, 0), false) // Tue 30 June 23:00, the month's last hour: R2
	w.trip(w.O, w.dO2, "K1", bkk(2026, 7, 1, 0, 30), false)  // Wed 1 July 00:30: July R1
	for _, c := range []struct {
		period, round, config string
		earnings              float64
	}{{"2026-06", "R1", jan, 300}, {"2026-06", "R2", jan, 300}, {"2026-07", "R1", jul, 500}} {
		job := w.generate(t, ta, c.period, c.round)
		res, _ := job["result"].(map[string]any)
		if str(job["status"]) != "succeeded" || str(res["configId"]) != c.config {
			t.Fatalf("%s %s: priced with %v, want %s", c.period, c.round, job, c.config)
		}
		if d := w.runFor(t, ta, w.dO2, c.period, c.round); num(d["totalEarningsThb"]) != c.earnings {
			t.Fatalf("%s %s: earnings %v, want %v", c.period, c.round, d["totalEarningsThb"], c.earnings)
		}
	}
	// ?at= answers the version in effect at that instant: the July version from its own instant on.
	for at, want := range map[string]string{"2026-06-30T23:59:59+07:00": jan, "2026-07-01": jul} {
		r = w.get("/v1/payroll/config?at="+url.QueryEscape(at), ta)
		expect(t, r, http.StatusOK, "")
		if str(r.data()["id"]) != want {
			t.Fatalf("?at=%s: %s", at, r.raw)
		}
	}
}

// TestPayrollPenaltyPastLastInstallment: a balance left after a penalty's last installment (a shortfall of the round's
// earnings, or the remainder of a rounded split) keeps deducting in later R2 rounds while installments_paid stays at
// installments_total (the schema CHECK); Appendix D invariant 6 counts those applications capped at installments_total
// (T44 review).
func TestPayrollPenaltyPastLastInstallment(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	expect(t, w.post("/v1/payroll/config", ta, map[string]any{"effectiveFrom": "2026-01-01"}), http.StatusCreated, "")
	// dO2 (no SSO): one weekday trip a round (300) against 500 in one installment.
	short := w.id(`INSERT INTO driver_penalties (tenant_id, driver_id, type_code, total_thb, remaining_thb, installments_total, status,
		incurred_at) VALUES ($1, $2, 'DAMAGE', 500, 500, 1, 'pending', $3) RETURNING id::text`, w.O, w.dO2, bkk(2026, 6, 1, 9, 0))
	// dE (no SSO): two weekday trips a round (600) against 1000 in three installments of 333, which leave 1.
	dE := w.driver(w.O, "Ekachai")
	split := w.id(`INSERT INTO driver_penalties (tenant_id, driver_id, type_code, total_thb, remaining_thb, installments_total, status,
		incurred_at) VALUES ($1, $2, 'LATE_SLA', 1000, 1000, 3, 'pending', $3) RETURNING id::text`, w.O, dE, bkk(2026, 6, 1, 9, 0))
	type step struct {
		applied, remaining float64
		paid               int
		status             string
	}
	plan := []struct {
		driver, penalty string
		trips           int
		steps           []step
	}{
		{w.dO2, short, 1, []step{{300, 200, 1, "partially_deducted"}, {200, 0, 1, "cleared"}}},
		{dE, split, 2, []step{{333, 667, 1, "partially_deducted"}, {333, 334, 2, "partially_deducted"}, {333, 1, 3, "partially_deducted"},
			{1, 0, 3, "cleared"}}},
	}
	for i, month := range []time.Month{time.June, time.July, time.August, time.September} {
		period := time.Date(2026, month, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
		for _, p := range plan {
			for k, at := range r2Weekdays(2026, month, p.trips) {
				w.trip(w.O, p.driver, p.driver+"-"+period+"-"+strconv.Itoa(k), at, false)
			}
		}
		w.generate(t, ta, period, "R2")
		for _, p := range plan {
			if i >= len(p.steps) {
				continue
			}
			want := p.steps[i]
			d := w.runFor(t, ta, p.driver, period, "R2")
			apps, _ := d["penaltyApplications"].([]any)
			if len(apps) != 1 || num(apps[0].(map[string]any)["appliedThb"]) != want.applied {
				t.Fatalf("%s %s: applications %v, want %v", p.driver, period, apps, want.applied)
			}
			expect(t, w.post("/v1/payroll/"+str(d["id"])+"/approve", ta, nil), http.StatusOK, "")
			if rem, paid, st := w.penaltyState(t, p.penalty); rem != want.remaining || paid != want.paid || st != want.status {
				t.Fatalf("%s after %s: %v %d %s, want %+v", p.penalty, period, rem, paid, st, want)
			}
		}
	}
	for _, p := range plan {
		if n := w.count(`SELECT count(*) FROM payroll_penalty_applications a JOIN payroll_runs r ON r.id = a.payroll_run_id
			WHERE a.penalty_id = $1 AND r.status = 'approved'`, p.penalty); n != len(p.steps) {
			t.Fatalf("%s: %d approved applications, want %d", p.penalty, n, len(p.steps))
		}
	}
	if n := w.count(invariant6); n != 0 {
		t.Fatalf("invariant 6: %d rows", n)
	}
}

// TestPayrollMovedDriverPenalties: a moved driver's penalties keep the tenant they were stamped with
// (tenancy.MoveDriver: rows that reference the driver keep their frozen tenant), and so does a quarantined one; the
// consumer reads with the bypass, so ListOpenPenalties names the job's tenant and a run never schedules another
// tenant's penalty. Without it, O's run of dC would show C's penalty and stay stale for good (C is outside O's reach:
// the approval cannot lock it, nor O cancel it), and O's approval of dA would pay down A's penalty (T44 review).
func TestPayrollMovedDriverPenalties(t *testing.T) {
	w := newWorld(t)
	w.config(t)
	ta := w.login("ta.o")
	ctx := context.Background()
	open := map[string]float64{
		w.id(`INSERT INTO driver_penalties (tenant_id, driver_id, type_code, type_name_snapshot, total_thb, remaining_thb, installments_total,
			status, incurred_at) VALUES ($1, $2, 'C_ONLY', 'C secret penalty', 3000, 3000, 1, 'pending', $3) RETURNING id::text`,
			w.C, w.dC, bkk(2026, 6, 1, 9, 0)): 3000,
		w.id(`INSERT INTO driver_penalties (tenant_id, driver_id, type_code, type_name_snapshot, total_thb, remaining_thb, installments_total,
			status, incurred_at) VALUES ($1, $2, 'A_ONLY', 'A penalty', 200, 200, 1, 'pending', $3) RETURNING id::text`,
			w.A, w.dA, bkk(2026, 6, 1, 9, 0)): 200,
		// dO2 never moved; an ETL row whose tenant chain ran out sits in the quarantine tenant.
		w.id(`INSERT INTO driver_penalties (tenant_id, tenant_source, driver_id, type_code, total_thb, remaining_thb, installments_total,
			status, incurred_at) VALUES (app_quarantine_tenant_id(), 'quarantine', $1, 'Q', 400, 400, 1, 'pending', $2) RETURNING id::text`,
			w.dO2, bkk(2026, 6, 1, 9, 0)): 400,
	}
	tx, err := w.etl.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ driver, from string }{{w.dC, w.C}, {w.dA, w.A}} {
		if _, err := tenancy.MoveDriver(ctx, tx, uuid.MustParse(m.driver), uuid.MustParse(m.from), uuid.MustParse(w.O)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for i, drv := range []string{w.dC, w.dA, w.dO2} {
		w.trip(w.O, drv, "M"+strconv.Itoa(i), bkk(2026, 6, 18, 10+i, 0), false) // Thu 18 June: R2 weekday
	}
	job := w.generate(t, ta, "2026-06", "R2")
	if res, _ := job["result"].(map[string]any); num(res["drivers"]) != 4 {
		t.Fatalf("O's drivers after the moves: %v", job)
	}
	for _, drv := range []string{w.dC, w.dA, w.dO2} {
		d := w.runFor(t, ta, drv, "2026-06", "R2")
		if apps, _ := d["penaltyApplications"].([]any); len(apps) != 0 {
			t.Fatalf("%s: another tenant's penalty scheduled: %v", drv, apps)
		}
		for _, l := range lines(d) {
			if l.Category == "PENALTY" {
				t.Fatalf("%s: penalty line %+v", drv, l)
			}
		}
		if str(d["tenantId"]) != w.O || num(d["netPayThb"]) != 300 {
			t.Fatalf("%s: %v", drv, d)
		}
		expect(t, w.post("/v1/payroll/"+str(d["id"])+"/approve", ta, nil), http.StatusOK, "")
	}
	for id, total := range open {
		if rem, paid, st := w.penaltyState(t, id); rem != total || paid != 0 || st != "pending" {
			t.Fatalf("penalty %s: %v %d %s", id, rem, paid, st)
		}
	}
	if n := w.count(invariant6); n != 0 {
		t.Fatalf("invariant 6: %d rows", n)
	}
}
