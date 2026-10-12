package seed

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// T46 (issue #66): the demo HR rows. Appendix D invariant 6 (run totals and net, a ledger row per approved or paid run,
// penalty balances and installment counts against the approved applications) and the ledger and window parts of
// invariant 11 hold in the plan itself, under every anchor; every status of §D.2 #9, #10 and #14 is present.

func num2(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := numberOf(v)
	if !ok {
		t.Fatalf("not a number: %#v", v)
	}
	return f
}

func intVal(t *testing.T, v any) int {
	t.Helper()
	n, ok := intOf(v)
	if !ok {
		t.Fatalf("not an integer: %#v", v)
	}
	return n
}

// checkInvariant6 is §D.3 #6 over the plan's rows.
func checkInvariant6(t *testing.T, p *Plan) {
	t.Helper()
	type sums struct{ e, d float64 }
	lines := map[string]*sums{}
	for _, l := range p.Tables["payroll_line_items"] {
		s := lines[l.Str("payroll_run_id")]
		if s == nil {
			s = &sums{}
			lines[l.Str("payroll_run_id")] = s
		}
		if l.Str("item_type") == compute.TypeEarning {
			s.e += num2(t, l["amount_thb"])
		} else {
			s.d += num2(t, l["amount_thb"])
		}
	}
	tx := map[string]Row{}
	for _, r := range p.Tables["transactions"] {
		tx[r.Str("id")] = r
	}
	committed := map[string]bool{}
	for _, r := range p.Tables["payroll_runs"] {
		s := lines[r.Str("id")]
		if s == nil {
			s = &sums{}
		}
		e, d, net := num2(t, r["total_earnings_thb"]), num2(t, r["total_deductions_thb"]), num2(t, r["net_pay_thb"])
		if math.Abs(e-s.e) > 1e-9 || math.Abs(d-s.d) > 1e-9 || net != math.Max(0, e-d) {
			t.Errorf("run %s %s %s: totals %v/%v/%v, lines %v/%v", r.Str("driver_id"), r.Str("pay_period"), r.Str("pay_round"), e, d, net, s.e, s.d)
		}
		st := r.Str("status")
		if st != "approved" && st != "paid" {
			if r.Str("ledger_transaction_id") != "" {
				t.Errorf("run %s is %s with a ledger row", r.Str("id"), st)
			}
			continue
		}
		committed[r.Str("id")] = true
		led := tx[r.Str("ledger_transaction_id")]
		if led == nil {
			t.Errorf("run %s is %s without its ledger row", r.Str("id"), st)
			continue
		}
		created, err := time.Parse(time.RFC3339, led.Str("created_at"))
		if err != nil {
			t.Fatal(err)
		}
		if num2(t, led["amount_thb"]) != net || led.Str("payroll_run_id") != r.Str("id") || led.Str("tx_date") != clock.DateString(created) {
			t.Errorf("ledger row %s of run %s: amount %v (net %v), date %s on %s", led.Str("id"), r.Str("id"), led["amount_thb"], net,
				led.Str("tx_date"), led.Str("created_at"))
		}
	}
	applied, count := map[string]float64{}, map[string]int{}
	for _, a := range p.Tables["payroll_penalty_applications"] {
		if committed[a.Str("payroll_run_id")] {
			applied[a.Str("penalty_id")] += num2(t, a["applied_thb"])
			count[a.Str("penalty_id")]++
		}
	}
	for _, pen := range p.Tables["driver_penalties"] {
		total, remaining := num2(t, pen["total_thb"]), num2(t, pen["remaining_thb"])
		paid, inst := intVal(t, pen["installments_paid"]), intVal(t, pen["installments_total"])
		st := pen.Str("status")
		if pen.Str("legacy_doc_id") == "" && st != "cancelled" &&
			(remaining != total-applied[pen.Str("id")] || paid != min(count[pen.Str("id")], inst)) {
			t.Errorf("penalty %s: remaining %v of %v (applied %v), %d paid of %d (applications %d)", pen.Str("id"), remaining, total,
				applied[pen.Str("id")], paid, inst, count[pen.Str("id")])
		}
		if (st == "cleared") != (remaining == 0) && st != "cancelled" {
			t.Errorf("penalty %s is %s with %v remaining", pen.Str("id"), st, remaining)
		}
	}
}

// checkPayrollWindows is the payroll part of §D.3 #11: Bangkok-midnight bounds, R1 ending on the 16th.
func checkPayrollWindows(t *testing.T, p *Plan) {
	t.Helper()
	for _, r := range p.Tables["payroll_runs"] {
		want, ok := compute.ParsePeriod(r.Str("pay_period"), compute.Round(r.Str("pay_round")))
		if !ok {
			t.Fatalf("run %s: period %s %s", r.Str("id"), r.Str("pay_period"), r.Str("pay_round"))
		}
		start, err1 := time.Parse(time.RFC3339, r.Str("period_start"))
		end, err2 := time.Parse(time.RFC3339, r.Str("period_end"))
		if err1 != nil || err2 != nil || !start.Equal(want.Start) || !end.Equal(want.End) {
			t.Errorf("run %s: window %s - %s, want %s - %s", r.Str("id"), r.Str("period_start"), r.Str("period_end"), ts(want.Start), ts(want.End))
		}
	}
}

func generatedRows(smoke, p *Plan, table string) []Row {
	return p.Tables[table][len(smoke.Tables[table]):]
}

func TestDemoHRRows(t *testing.T) {
	smoke, demo := plan(t, ProfileSmoke), plan(t, ProfileDemo)
	checkInvariant6(t, demo)
	checkPayrollWindows(t, demo)

	statuses := map[string]int{}
	drivers := map[string]bool{}
	for _, r := range generatedRows(smoke, demo, "payroll_runs") {
		statuses[r.Str("status")]++
		drivers[r.Str("driver_id")] = true
		if r.Str("tenant_id") != demo.Symbols.MustID(ownFleetSymbol).String() {
			t.Errorf("run %s is not the own fleet's", r.Str("id"))
		}
	}
	if want := map[string]int{"paid": 12, "approved": 3, "pending_approval": 1, "draft": 2}; fmt.Sprint(statuses) != fmt.Sprint(want) {
		t.Errorf("generated run statuses %v, want %v (Appendix D §D.2 #9)", statuses, want)
	}
	if len(drivers) != 3 {
		t.Errorf("runs of %d drivers, want d8, d9, d10", len(drivers))
	}
	// The engine's lines: social security only for the drivers past probation, in R2; a penalty line names its
	// penalty, and its run applies exactly that amount to it.
	probation := map[string]bool{}
	for _, d := range demo.Tables["drivers"] {
		probation[d.Str("id")] = d["probation_passed"] == true
	}
	runs := map[string]Row{}
	for _, r := range demo.Tables["payroll_runs"] {
		runs[r.Str("id")] = r
	}
	apps := map[string]float64{}
	for _, a := range demo.Tables["payroll_penalty_applications"] {
		apps[a.Str("payroll_run_id")+"|"+a.Str("penalty_id")] = num2(t, a["applied_thb"])
	}
	sso := 0
	for _, l := range generatedRows(smoke, demo, "payroll_line_items") {
		run := runs[l.Str("payroll_run_id")]
		switch l.Str("category") {
		case compute.CategorySocialSecurity:
			sso++
			if run.Str("pay_round") != "R2" || !probation[run.Str("driver_id")] {
				t.Errorf("social security on %s %s of a driver past probation %v", run.Str("pay_period"), run.Str("pay_round"), probation[run.Str("driver_id")])
			}
		case compute.CategoryPenalty:
			if got := apps[l.Str("payroll_run_id")+"|"+l.Str("reference_id")]; got != num2(t, l["amount_thb"]) {
				t.Errorf("penalty line %s of %v, application %v", l.Str("id"), l["amount_thb"], got)
			}
		}
	}
	if sso != 6 {
		t.Errorf("%d social security lines, want 6 (d8 and d9 in three R2 rounds)", sso)
	}
	pens := map[string]int{}
	for _, r := range generatedRows(smoke, demo, "driver_penalties") {
		pens[r.Str("status")]++
	}
	if fmt.Sprint(pens) != fmt.Sprint(map[string]int{"partially_deducted": 1, "pending": 1}) {
		t.Errorf("generated penalties %v", pens)
	}

	// Holidays: the twelve fixed-date public holidays of 2026 smoke does not hold (HO1 to HO3 are the other three) and
	// 2027's New Year's Day, one per day and type and scope.
	seen := map[string]bool{}
	for _, r := range demo.Tables["holidays"] {
		k := r.Str("tenant_id") + "|" + r.Str("holiday_date") + "|" + r.Str("holiday_type")
		if seen[k] {
			t.Errorf("holiday %s twice", k)
		}
		seen[k] = true
	}
	gen := generatedRows(smoke, demo, "holidays")
	if len(gen) != 13 {
		t.Fatalf("%d generated holidays, want 13", len(gen))
	}
	for _, r := range gen {
		if r.Str("holiday_type") != "public" || r.Str("status") != "published" || r["tenant_id"] != nil || r.Str("name") == "" {
			t.Errorf("generated holiday %v", r)
		}
	}

	// Leave: every status, the decided ones with their decider, the rejection with its reason, two evidence files
	// owned by their request; nothing requested after the anchor day.
	anchorEnd := time.Date(DefaultAnchor.Year(), DefaultAnchor.Month(), DefaultAnchor.Day()+1, 0, 0, 0, 0, clock.Bangkok)
	leave := map[string]int{}
	files := map[string]Row{}
	for _, f := range demo.Tables["file_objects"] {
		files[f.Str("id")] = f
	}
	ids := map[string]bool{}
	for _, r := range generatedRows(smoke, demo, "leave_requests") {
		leave[r.Str("status")]++
		ids[r.Str("id")] = true
		created, _ := time.Parse(time.RFC3339, r.Str("created_at"))
		if !created.Before(anchorEnd) {
			t.Errorf("leave %s requested %s, after the anchor day", r.Str("id"), r.Str("created_at"))
		}
		decided := r.Str("status") == "approved" || r.Str("status") == "rejected" || r.Str("status") == "cancelled"
		if decided != (r.Str("approver_user_id") != "" && r.Str("decided_at") != "") {
			t.Errorf("leave %s %s: approver %q at %q", r.Str("id"), r.Str("status"), r.Str("approver_user_id"), r.Str("decided_at"))
		}
		if (r.Str("status") == "rejected") != (r.Str("rejection_reason") != "") {
			t.Errorf("leave %s %s: rejection reason %q", r.Str("id"), r.Str("status"), r.Str("rejection_reason"))
		}
	}
	if fmt.Sprint(leave) != fmt.Sprint(map[string]int{"approved": 1, "cancelled": 1, "pending": 2, "rejected": 1}) {
		t.Errorf("generated leave statuses %v", leave)
	}
	atts := demo.Tables["leave_request_attachments"]
	if len(atts) != 2 {
		t.Fatalf("%d leave attachments, want 2", len(atts))
	}
	for i, a := range atts {
		f := files[a.Str("file_id")]
		if f == nil || !ids[a.Str("leave_request_id")] || f.Str("purpose") != "leave_evidence" || f.Str("owner_id") != a.Str("leave_request_id") ||
			intVal(t, a["position"]) != i || !strings.HasPrefix(f.Str("object_key"), "leave/"+a.Str("leave_request_id")+"/") {
			t.Errorf("leave attachment %v (file %v)", a, f)
		}
	}
}

// The generator builds under any SEED_ANCHOR_DATE (the staging VM's October days, a month's first day, January, which
// prices the previous year's rounds with the earliest version, a later year's September): invariant 6 and the windows
// hold each time, every fixed-date public holiday of the window is in the calendar the engine priced against, the
// newest past round is approved and the running one a draft preview.
func TestDemoHRUnderAnchors(t *testing.T) {
	for _, anchor := range []time.Time{
		time.Date(2026, 10, 11, 0, 0, 0, 0, clock.Bangkok), time.Date(2026, 10, 1, 0, 0, 0, 0, clock.Bangkok),
		time.Date(2026, 9, 16, 0, 0, 0, 0, clock.Bangkok), time.Date(2027, 1, 20, 0, 0, 0, 0, clock.Bangkok),
		time.Date(2027, 1, 3, 0, 0, 0, 0, clock.Bangkok), time.Date(2027, 9, 29, 0, 0, 0, 0, clock.Bangkok),
	} {
		t.Run(clock.DateString(anchor), func(t *testing.T) {
			t.Parallel()
			p := planAt(t, 0, anchor)
			checkInvariant6(t, p)
			checkPayrollWindows(t, p)
			checkWindowHolidays(t, p, anchor)
			asOf := anchor.AddDate(0, 0, 1)
			var running, approved int
			for _, r := range p.Tables["payroll_runs"] {
				created, _ := time.Parse(time.RFC3339, r.Str("created_at"))
				if created.After(asOf) {
					t.Errorf("run %s %s generated %s, after the anchor day", r.Str("pay_period"), r.Str("pay_round"), r.Str("created_at"))
				}
				for _, col := range []string{"approved_at", "payment_date"} {
					if at, err := time.Parse(time.RFC3339, r.Str(col)); err == nil && at.After(asOf) {
						t.Errorf("run %s %s: %s %s after the anchor day", r.Str("pay_period"), r.Str("pay_round"), col, r.Str(col))
					}
				}
				end, _ := time.Parse(time.RFC3339, r.Str("period_end"))
				if end.After(asOf) && r.Str("status") != "draft" && r.Str("status") != "pending_approval" {
					t.Errorf("the running round %s %s is %s", r.Str("pay_period"), r.Str("pay_round"), r.Str("status"))
				}
				if end.After(asOf) {
					running++
				}
				if r.Str("status") == "approved" {
					approved++
				}
			}
			if running == 0 || approved == 0 {
				t.Errorf("anchor %s: %d runs of the running round, %d approved", clock.DateString(anchor), running, approved)
			}
		})
	}
}

// checkWindowHolidays: each of the fifteen fixed-date public holidays that falls in the operations window (the window's
// first day to the anchor day, the payroll rounds' months included) is a published public row of the plan, so the
// generated runs price those trips at the holiday rate and /app/holidays shows them; none was published after the
// anchor day.
func checkWindowHolidays(t *testing.T, p *Plan, anchor time.Time) {
	t.Helper()
	window := anchor.AddDate(0, 0, -(opsDays - 1))
	asOf := anchor.AddDate(0, 0, 1)
	public := map[string]bool{}
	for _, r := range p.Tables["holidays"] {
		if r["tenant_id"] != nil || r.Str("holiday_type") != "public" {
			continue // smoke's company draft HO4 keeps its fixture day
		}
		public[r.Str("holiday_date")] = true
		if created, err := time.Parse(time.RFC3339, r.Str("created_at")); err != nil || created.After(asOf) {
			t.Errorf("holiday %s created %q, after the anchor day", r.Str("holiday_date"), r.Str("created_at"))
		}
	}
	if len(demoPublicHolidays) != 15 {
		t.Errorf("%d fixed-date public holidays, want the 15 of the Thai calendar", len(demoPublicHolidays))
	}
	for y := window.Year(); y <= anchor.Year(); y++ {
		for _, h := range demoPublicHolidays {
			day, err := time.ParseInLocation(time.DateOnly, fmt.Sprintf("%04d-%s", y, h.md), clock.Bangkok)
			if err != nil {
				t.Fatal(err)
			}
			if !day.Before(window) && day.Before(asOf) && !public[clock.DateString(day)] {
				t.Errorf("anchor %s: the public holiday %s (%s) of the window is missing", clock.DateString(anchor), clock.DateString(day), h.en)
			}
		}
	}
	if next := fmt.Sprintf("%04d-01-01", anchor.Year()+1); !public[next] {
		t.Errorf("anchor %s: the next New Year's Day %s is missing", clock.DateString(anchor), next)
	}
}

// The line meta the engine writes is JSON the jsonb column takes (the loader marshals it).
func TestDemoPayrollLinesMarshal(t *testing.T) {
	smoke, demo := plan(t, ProfileSmoke), plan(t, ProfileDemo)
	for _, l := range generatedRows(smoke, demo, "payroll_line_items") {
		if _, err := json.Marshal(l["meta"]); err != nil {
			t.Fatalf("line %s meta: %v", l.Str("id"), err)
		}
		if q, ok := l["quantity"].(string); ok {
			if _, err := strconv.ParseFloat(q, 64); err != nil {
				t.Errorf("line %s quantity %q", l.Str("id"), q)
			}
		}
	}
	if !slices.ContainsFunc(generatedRows(smoke, demo, "payroll_line_items"), func(l Row) bool {
		return l.Str("category") == compute.CategoryTripVolumeIncentive
	}) {
		t.Error("no generated run reaches a trip-volume tier")
	}
}

// An upsert that finds a run key of the plan held by an application run skips that driver's whole HR chain, and never a
// row that points at a skipped run or penalty; the other drivers' chains and the transactions of no run stay (T46
// review: a held key made the seed's payout row and penalty applications point at a run it never wrote).
func TestHeldHRChainsSkipWholeDriverChains(t *testing.T) {
	demo := plan(t, ProfileDemo)
	// d8's paid R2 run that deducts a penalty installment: the chain with every kind of child.
	var target Row
	applied := map[string]bool{}
	for _, a := range demo.Tables["payroll_penalty_applications"] {
		applied[a.Str("payroll_run_id")] = true
	}
	for _, r := range demo.Tables["payroll_runs"] {
		if r.Str("status") == "paid" && r.Str("pay_round") == "R2" && applied[r.Str("id")] && r.Str("legacy_doc_id") == "" {
			target = r
		}
	}
	if target == nil {
		t.Fatal("no paid R2 run with penalty applications in the demo plan")
	}
	driver := target.Str("driver_id")
	held := []HeldRun{{DriverID: driver, Period: target.Str("pay_period"), Round: target.Str("pay_round"), ExistingID: "app-run", PlanID: target.Str("id")}}
	c := heldHRChains(demo.Tables, held)
	if len(c.drivers) != 1 || len(c.runs) < 2 || len(c.penalties) == 0 {
		t.Fatalf("chain of %s: %d drivers, %d runs, %d penalties", driver, len(c.drivers), len(c.runs), len(c.penalties))
	}
	kept := map[string][]Row{}
	for _, table := range LoadOrder {
		kept[table] = c.keep(table, demo.Tables[table])
	}
	runs := map[string]bool{}
	for _, r := range kept["payroll_runs"] {
		if r.Str("driver_id") == driver {
			t.Errorf("run %s of the held driver kept", r.Str("id"))
		}
		runs[r.Str("id")] = true
	}
	pens := map[string]bool{}
	for _, r := range kept["driver_penalties"] {
		if r.Str("driver_id") == driver {
			t.Errorf("penalty %s of the held driver kept", r.Str("id"))
		}
		pens[r.Str("id")] = true
	}
	for _, table := range []string{"payroll_line_items", "payroll_penalty_applications", "transactions", "driver_advances"} {
		for _, r := range kept[table] {
			if id := r.Str("payroll_run_id"); id != "" && !runs[id] {
				t.Errorf("%s row %s points at the skipped run %s", table, describe(r), id)
			}
			if id := r.Str("penalty_id"); id != "" && !pens[id] {
				t.Errorf("%s row %s points at the skipped penalty %s", table, describe(r), id)
			}
		}
	}
	noRun := func(rows []Row) (n int) {
		for _, r := range rows {
			if r.Str("payroll_run_id") == "" {
				n++
			}
		}
		return n
	}
	if a, b := noRun(kept["transactions"]), noRun(demo.Tables["transactions"]); a != b {
		t.Errorf("%d of %d transactions of no run kept", a, b)
	}
	if got, want := len(kept["payroll_runs"]), len(demo.Tables["payroll_runs"])-len(c.runs); got != want || got == 0 {
		t.Errorf("%d runs kept, want %d (the other drivers' chains)", got, want)
	}
	if got := c.keep("holidays", demo.Tables["holidays"]); len(got) != len(demo.Tables["holidays"]) {
		t.Errorf("a table outside the HR chain lost rows")
	}
	if none := heldHRChains(demo.Tables, nil); len(none.keep("payroll_runs", demo.Tables["payroll_runs"])) != len(demo.Tables["payroll_runs"]) {
		t.Error("no held key skipped runs")
	}

	// keepWritable: the children of a run are written only under a run this load inserted; a row of no run passes.
	inserted := map[string]map[string]bool{"payroll_runs": {target.Str("id"): true}}
	for _, table := range []string{"payroll_line_items", "payroll_penalty_applications", "transactions", "driver_advances"} {
		for _, r := range keepWritable(table, demo.Tables[table], inserted) {
			if id := r.Str("payroll_run_id"); id != "" && id != target.Str("id") {
				t.Errorf("%s row %s under a run this load did not insert", table, describe(r))
			}
		}
	}
	if got := noRun(keepWritable("transactions", demo.Tables["transactions"], inserted)); got != noRun(demo.Tables["transactions"]) {
		t.Errorf("keepWritable dropped transactions of no run: %d kept", got)
	}
}
