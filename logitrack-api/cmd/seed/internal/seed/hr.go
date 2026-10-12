package seed

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// The HR generator of the demo profile (T46, issue #66; Appendix D §D.1.2, §D.2 #9, #10 and #14, the holidays row;
// developer-spec.md §14): the public holidays of the anchor's year, the own fleet's payroll runs of the operations
// window with their line items, penalty applications and payout ledger rows, two penalties, and leave requests in
// every status. The smoke rows are never touched; the generator only appends.
//
// Payroll is never typed (Appendix D §D.1.4): every run is internal/hr/compute.Compute over the plan's own inputs, as
// the payroll.run job (internal/hr/run.go) reads them from the database: the config version in effect on the month's
// last Bangkok day, the holidays the tenant sees (public and its own; Sundays count too), the drivers' delivered trips
// of the month, their own and helper tasks, and in R2 the tenant's open penalties that existed when the round was
// generated. Rounds are processed in time order, and an approval commits its penalty installments as POST
// /v1/payroll/{id}/approve does (internal/hr ApplyPenalty), so invariant 6 (run totals, net, ledger, penalty
// balances) holds by construction. A round generated before the anchor day ended is approved (the newest) or paid (the
// older ones); the round still running on the anchor day is a draft preview, its first driver's sent for approval.
//
// Only the own fleet runs payroll here: it is the one tenant with a compensation config (smoke CC1, CC2), and runs are
// per tenant (main spec §6.18 #28). Its generated drivers that sign in (d8, d9, d10) are paid; the smoke drivers keep
// their fixture runs. Values come from the window position and the plan, never from SEED_RANDOM_SEED, so the counts of
// Appendix D §D.1.2 hold under every seed.

// demoPublicHolidays are the fixed-date Thai public holidays (Appendix D §D.1.4: lunar dates are UNVERIFIED and never
// generated); the anchor's year gets each one the plan does not hold yet, and the next year its New Year's Day.
var demoPublicHolidays = []struct{ md, en, th string }{
	{"01-01", "New Year's Day", "วันขึ้นปีใหม่"},
	{"04-06", "Chakri Memorial Day", "วันจักรี"},
	{"04-13", "Songkran Festival", "วันสงกรานต์"},
	{"04-14", "Songkran Festival", "วันสงกรานต์"},
	{"04-15", "Songkran Festival", "วันสงกรานต์"},
	{"05-01", "National Labour Day", "วันแรงงานแห่งชาติ"},
	{"05-04", "Coronation Day", "วันฉัตรมงคล"},
	{"06-03", "Queen Suthida's Birthday", "วันเฉลิมพระชนมพรรษาสมเด็จพระราชินี"},
	{"10-23", "King Chulalongkorn Memorial Day", "วันปิยมหาราช"},
	{"12-05", "Father's Day", "วันพ่อแห่งชาติ"},
	{"12-10", "Constitution Day", "วันรัฐธรรมนูญ"},
	{"12-31", "New Year's Eve", "วันสิ้นปี"},
}

// demoPenalty is one generated penalty of the own fleet's paid drivers: driver is the index into those drivers, at the
// Bangkok instant it was incurred (and recorded).
type demoPenalty struct {
	driver       int
	at           func(windowStart, anchor time.Time) time.Time
	totalTHB     float64
	installments int
	reason       string
}

// demoPenalties: one in three installments early in the window (two R2 runs deduct it, the draft of the last R2
// schedules the third: partially_deducted), one in full a few days before the anchor (pending, scheduled by the draft).
var demoPenalties = []demoPenalty{
	{driver: 0, at: func(w, _ time.Time) time.Time { return w.AddDate(0, 0, 9).Add(9 * time.Hour) }, totalTHB: 900, installments: 3,
		reason: "ส่งงานล่าช้ากว่า SLA สามรอบ (ตัวอย่าง)"},
	{driver: 1, at: func(_, a time.Time) time.Time { return a.AddDate(0, 0, -10).Add(10 * time.Hour) }, totalTHB: 500, installments: 1,
		reason: "เลขซีลไม่ตรงใบงาน (ตัวอย่าง)"},
}

// demoLeave is one generated leave request: the driver (by its demo key), its type and status, its first day and length
// relative to the anchor, and how many evidence files it carries.
type demoLeave struct {
	driver      string
	typ, status string
	startDay    int // days after the anchor (negative: before)
	days        int
	reason      string
	rejection   string
	approver    string // a user symbol, or a user e-mail of the demo personas
	attachments int
}

var demoLeaves = []demoLeave{
	{driver: "D8", typ: "sick", status: "approved", startDay: -70, days: 2, reason: "ไข้หวัดใหญ่ (ตัวอย่าง)", approver: "U_WRT_MGR"},
	{driver: "D9", typ: "business", status: "pending", startDay: 5, days: 2, reason: "ติดต่อราชการที่อำเภอ (ตัวอย่าง)"},
	{driver: "D10", typ: "sick", status: "cancelled", startDay: -40, days: 1, reason: "ปวดฟัน นัดทำฟัน (ตัวอย่าง)", approver: "U_WRT_MGR"},
	{driver: "D12", typ: "sick", status: "rejected", startDay: -20, days: 3, reason: "ปวดหลัง (ตัวอย่าง)",
		rejection: "ไม่มีใบรับรองแพทย์ กรุณายื่นใหม่พร้อมใบรับรอง", approver: "U_NWR_ADMIN"},
	{driver: "D14", typ: "sick", status: "pending", startDay: -1, days: 2, reason: "ท้องเสีย มีใบรับรองแพทย์แนบ (ตัวอย่าง)", attachments: 2},
}

// hrGen holds one run of the generator.
type hrGen struct {
	p      *Plan
	o      Options
	own    string
	anchor time.Time // Bangkok midnight of the anchor day
	asOf   time.Time // the end of the anchor day
	window time.Time // Bangkok midnight of the operations window's first day
	byKey  map[string]Row
}

func (g *hrGen) id(table, key string) string { return ID(g.o.Namespace, table, key).String() }

func (g *hrGen) add(table string, r Row) { g.p.Tables[table] = append(g.p.Tables[table], r) }

func (g *hrGen) user(ref string) string {
	if id, ok := g.p.Symbols.ID(ref); ok {
		return id.String()
	}
	return ID(g.o.Namespace, "users", ref).String()
}

// addDemoHR appends the demo HR rows (after the operations generator, whose trips the payroll reads).
func addDemoHR(p *Plan, o Options) error {
	g := &hrGen{p: p, o: o, own: p.Symbols.MustID(ownFleetSymbol).String(), byKey: map[string]Row{}}
	g.anchor = time.Date(o.Anchor.Year(), o.Anchor.Month(), o.Anchor.Day(), 0, 0, 0, 0, clock.Bangkok)
	g.asOf = g.anchor.AddDate(0, 0, 1)
	g.window = g.anchor.AddDate(0, 0, -(opsDays - 1))
	for _, r := range p.Tables["drivers"] {
		g.byKey[demoDriverKey(r)] = r
	}
	g.holidays()
	if err := g.payroll(); err != nil {
		return err
	}
	return g.leave()
}

// demoDriverKey is the demo key (D8 ... D15) of a generated driver, from its national ID; "" for any other driver.
func demoDriverKey(r Row) string {
	for _, d := range demoDrivers {
		if d.nid == r.Str("id_card") {
			return d.key
		}
	}
	return ""
}

// --- holidays --------------------------------------------------------------------------------------------------

func (g *hrGen) holidays() {
	held := map[string]bool{}
	for _, r := range g.p.Tables["holidays"] {
		held[r.Str("tenant_id")+"|"+r.Str("holiday_date")+"|"+r.Str("holiday_type")] = true
	}
	created := ts(time.Date(g.anchor.Year(), 1, 5, 9, 0, 0, 0, clock.Bangkok))
	by := g.user("U_WRT_ADMIN")
	add := func(day, en, th string) {
		if held["|"+day+"|public"] {
			return
		}
		held["|"+day+"|public"] = true
		g.add("holidays", Row{"id": g.id("holidays", "public:"+day+":public"), "holiday_date": day, "holiday_type": "public",
			"status": "published", "name": th, "name_en": en, "name_th": th, "is_recurring": true, "created_by": by, "created_at": created})
	}
	for _, h := range demoPublicHolidays {
		add(fmt.Sprintf("%04d-%s", g.anchor.Year(), h.md), h.en, h.th)
	}
	add(fmt.Sprintf("%04d-01-01", g.anchor.Year()+1), demoPublicHolidays[0].en, demoPublicHolidays[0].th)
}

// --- payroll ---------------------------------------------------------------------------------------------------

// payDriver is a driver the generator pays, and its compute input of the month being priced.
type payDriver struct {
	id, nid string
	sso     compute.SSODriver
}

// payPenalty is a generated penalty's state while the rounds are processed.
type payPenalty struct {
	row       Row
	id        string
	driver    string
	created   time.Time
	total     float64
	remaining float64
	instTotal int
	instPaid  int
	typeCode  string
	typeName  string
	updated   time.Time
}

// configVersion is a compensation config row of the plan as the engine reads it.
type configVersion struct {
	at    time.Time
	id    string
	cfg   compute.Config
	types map[string]string // code -> English name
}

func parseDay(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.ParseInLocation(time.DateOnly, s, clock.Bangkok)
	if err != nil {
		return nil
	}
	return &t
}

// planNumber reads a numeric value of a plan row (numberOf), or fails naming what it found.
func planNumber(v any) (float64, error) {
	if f, ok := numberOf(v); ok {
		return f, nil
	}
	return 0, fmt.Errorf("not a number: %T", v)
}

func tiers(v any, minKey string) ([]compute.IncentiveTier, error) {
	list, _ := v.([]any)
	out := make([]compute.IncentiveTier, 0, len(list))
	for _, e := range list {
		m, _ := e.(map[string]any)
		min, err := planNumber(m[minKey])
		if err != nil {
			return nil, err
		}
		amt, err := planNumber(m["amountThb"])
		if err != nil {
			return nil, err
		}
		out = append(out, compute.IncentiveTier{Min: min, AmountTHB: amt})
	}
	return out, nil
}

// configs are the own fleet's compensation config versions of the plan, oldest first.
func (g *hrGen) configs() ([]configVersion, error) {
	var out []configVersion
	for _, r := range g.p.Tables["driver_compensation_configs"] {
		if r.Str("tenant_id") != g.own {
			continue
		}
		at, err := time.Parse(time.RFC3339, r.Str("effective_from_at"))
		if err != nil {
			return nil, fmt.Errorf("config %s: %w", r.Str("id"), err)
		}
		v := configVersion{at: at, id: r.Str("id"), types: map[string]string{}}
		nums := map[string]float64{}
		for _, col := range []string{"weekday_rate_thb", "holiday_rate_thb", "helper_day_rate_thb", "fuel_min_refuels_per_month",
			"sso_rate_percent", "sso_base_existing_thb", "sso_base_new_thb", "sso_existing_hired_before_year", "sso_max_age_inclusive"} {
			if nums[col], err = planNumber(r[col]); err != nil {
				return nil, fmt.Errorf("config %s %s: %w", r.Str("id"), col, err)
			}
		}
		fuel, err := tiers(r["fuel_incentive_tiers"], "minKmPerLitre")
		if err != nil {
			return nil, fmt.Errorf("config %s fuel tiers: %w", r.Str("id"), err)
		}
		trips, err := tiers(r["trip_volume_tiers"], "minTrips")
		if err != nil {
			return nil, fmt.Errorf("config %s trip tiers: %w", r.Str("id"), err)
		}
		standby, _ := r["pay_standby"].(bool)
		v.cfg = compute.Config{WeekdayRateTHB: nums["weekday_rate_thb"], HolidayRateTHB: nums["holiday_rate_thb"], PayStandby: standby,
			HelperDayRateTHB: nums["helper_day_rate_thb"], FuelIncentiveTiers: fuel, FuelMinRefuelsPerMonth: int(nums["fuel_min_refuels_per_month"]),
			TripVolumeTiers: trips, SSO: compute.SSORule{RatePercent: nums["sso_rate_percent"], BaseExistingTHB: nums["sso_base_existing_thb"],
				BaseNewTHB: nums["sso_base_new_thb"], ExistingHiredBeforeYear: int(nums["sso_existing_hired_before_year"]),
				MaxAgeInclusive: int(nums["sso_max_age_inclusive"])}}
		for _, t := range g.p.Tables["penalty_types"] {
			if t.Str("config_id") == v.id {
				v.types[t.Str("code")] = t.Str("name_en")
			}
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b configVersion) int { return a.at.Compare(b.at) })
	if len(out) == 0 {
		return nil, fmt.Errorf("the own fleet has no compensation config in the plan")
	}
	return out, nil
}

// forMonth is the version that prices a month (internal/hr ConfigForMonth: the latest effective_from_at before the
// month's exclusive end, else the earliest).
func forMonth(vs []configVersion, monthEnd time.Time) configVersion {
	out := vs[0]
	for _, v := range vs {
		if v.at.Before(monthEnd) {
			out = v
		}
	}
	return out
}

// round is one pay round of the window and when it was generated.
type round struct {
	period compute.Period
	gen    time.Time
	past   bool // generated after it ended, before the anchor day ended
}

// rounds are the pay rounds of the window's three months that started by the anchor day, oldest first.
func (g *hrGen) rounds() []round {
	var out []round
	for off := 2; off >= 0; off-- {
		m := time.Date(g.anchor.Year(), g.anchor.Month()-time.Month(off), 1, 0, 0, 0, 0, clock.Bangkok)
		for _, r := range []compute.Round{compute.R1, compute.R2} {
			p, _ := compute.ParsePeriod(fmt.Sprintf("%04d-%02d", m.Year(), int(m.Month())), r)
			if !p.Start.Before(g.asOf) {
				continue
			}
			gen := p.End.Add(9 * time.Hour)
			past := !gen.After(g.asOf)
			if !past {
				gen = g.asOf.Add(-6 * time.Hour) // a preview of the running round, the anchor day at 18:00
			}
			out = append(out, round{period: p, gen: gen, past: past})
		}
	}
	return out
}

func (g *hrGen) payroll() error {
	cfgs, err := g.configs()
	if err != nil {
		return fmt.Errorf("seed: demo payroll: %w", err)
	}
	var drivers []payDriver
	for _, r := range g.p.Tables["drivers"] {
		id := uuid.MustParse(r.Str("id"))
		if r.Str("tenant_id") != g.own || g.p.Symbols.SymbolOf(id) != "" || r.Str("user_id") == "" || r.Str("status") == "inactive" {
			continue
		}
		probation, _ := r["probation_passed"].(bool)
		drivers = append(drivers, payDriver{id: r.Str("id"), nid: r.Str("id_card"),
			sso: compute.SSODriver{HireDate: parseDay(r.Str("hire_date")), BirthDate: parseDay(r.Str("birth_date")), ProbationPassed: probation}})
	}
	slices.SortFunc(drivers, func(a, b payDriver) int { return compareStrings(a.nid, b.nid) })
	if len(drivers) == 0 {
		return nil
	}
	paid := map[string]bool{}
	for _, d := range drivers {
		paid[d.id] = true
	}
	// The holidays the own fleet sees (public and its own, every type and status: ListHolidayDates).
	keys := map[string]bool{}
	for _, r := range g.p.Tables["holidays"] {
		if t := r.Str("tenant_id"); t == "" || t == g.own {
			keys[r.Str("holiday_date")] = true
		}
	}
	isHoliday := compute.HolidayChecker(keys)
	trips := map[string][]compute.Trip{}
	for _, t := range g.p.Tables["trip_records"] {
		if t.Str("status") != "delivered" || !paid[t.Str("driver_id")] {
			continue
		}
		at, err := time.Parse(time.RFC3339, t.Str("delivered_at"))
		if err != nil {
			return fmt.Errorf("seed: demo payroll: trip %s: %w", t.Str("trip_no"), err)
		}
		trips[t.Str("driver_id")] = append(trips[t.Str("driver_id")], compute.Trip{DeliveredAt: at, MultiStop: t["is_multi_delivery"] == true})
	}
	type planTask struct {
		driver, helper string
		task           compute.Task
	}
	var tasks []planTask
	for _, k := range g.p.Tables["tasks"] {
		if !paid[k.Str("driver_id")] && !paid[k.Str("helper_driver_id")] {
			continue
		}
		plan, err := time.Parse(time.RFC3339, k.Str("plan_at"))
		if err != nil {
			return fmt.Errorf("seed: demo payroll: task %s: %w", k.Str("task_no"), err)
		}
		pt := planTask{driver: k.Str("driver_id"), helper: k.Str("helper_driver_id"), task: compute.Task{PlanAt: plan}}
		if s := k.Str("check_in_at"); s != "" {
			at, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return fmt.Errorf("seed: demo payroll: task %s: %w", k.Str("task_no"), err)
			}
			pt.task.CheckInAt = &at
		}
		tasks = append(tasks, pt)
	}
	pens, err := g.penalties(drivers, cfgs)
	if err != nil {
		return err
	}
	rounds := g.rounds()
	lastPast := -1
	for i, r := range rounds {
		if r.past {
			lastPast = i
		}
	}
	approver := g.user("U_WRT_ADMIN")
	for i, rd := range rounds {
		p := rd.period
		ver := forMonth(cfgs, p.MonthEnd)
		wFrom, wTo := p.Start.AddDate(0, 0, -1), p.End.AddDate(0, 0, 1)
		for di, d := range drivers {
			in := compute.DriverInput{SSO: d.sso}
			for _, t := range trips[d.id] {
				if !t.DeliveredAt.Before(p.MonthStart) && t.DeliveredAt.Before(p.MonthEnd) {
					in.MonthTrips = append(in.MonthTrips, t)
				}
			}
			for _, t := range tasks {
				if t.task.PlanAt.Before(wFrom) || !t.task.PlanAt.Before(wTo) {
					continue
				}
				if t.driver == d.id {
					in.OwnTasks = append(in.OwnTasks, t.task)
				}
				if t.helper == d.id {
					in.HelperTasks = append(in.HelperTasks, t.task)
				}
			}
			var open []*payPenalty
			if p.Round == compute.R2 {
				for _, pen := range pens {
					if pen.driver == d.id && pen.remaining > 0 && !pen.created.After(rd.gen) {
						open = append(open, pen)
						in.Penalties = append(in.Penalties, compute.Penalty{ID: pen.id, TypeCode: pen.typeCode, TypeName: pen.typeName,
							TotalTHB: pen.total, RemainingTHB: pen.remaining, InstallmentsTotal: pen.instTotal, InstallmentsPaid: pen.instPaid})
					}
				}
			}
			slip := compute.Compute(p, ver.cfg, isHoliday, in)
			status := "draft"
			approvedAt := rd.gen.Add(25 * time.Hour)
			paidAt := approvedAt.Add(48 * time.Hour)
			switch {
			case rd.past && i < lastPast && !paidAt.After(g.asOf):
				status = "paid"
			case rd.past && !approvedAt.After(g.asOf):
				status = "approved"
			case rd.past || di == 0:
				status = "pending_approval"
			}
			g.writeRun(d, rd, slip, status, approvedAt, paidAt, approver)
			if status == "approved" || status == "paid" {
				byID := map[string]*payPenalty{}
				for _, pen := range open {
					byID[pen.id] = pen
				}
				for _, a := range slip.Applications {
					pen := byID[a.PenaltyID]
					pen.remaining = max(0, pen.remaining-a.AppliedTHB)
					pen.instPaid = min(pen.instPaid+1, pen.instTotal)
					pen.updated = approvedAt
				}
			}
		}
	}
	for _, pen := range pens {
		status := "pending"
		switch {
		case pen.remaining <= 0:
			status = "cleared"
		case pen.instPaid > 0 || pen.remaining < pen.total:
			status = "partially_deducted"
		}
		pen.row["remaining_thb"], pen.row["installments_paid"], pen.row["status"] = money(pen.remaining), intNum(pen.instPaid), status
		pen.row["updated_at"] = ts(pen.updated)
	}
	return nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// penalties appends the generated penalties (their balances are set once every round is processed) and returns their
// state, in deduction order (incurred_at, id).
func (g *hrGen) penalties(drivers []payDriver, cfgs []configVersion) ([]*payPenalty, error) {
	var out []*payPenalty
	by := g.user("U_WRT_MGR")
	for _, sp := range demoPenalties {
		if sp.driver >= len(drivers) {
			continue
		}
		d := drivers[sp.driver]
		at := sp.at(g.window, g.anchor)
		ver := forMonth(cfgs, at.Add(time.Nanosecond)) // the version in effect at the instant (penaltyTypeName)
		name, ok := ver.types["LATE_SLA"]
		if !ok {
			return nil, fmt.Errorf("seed: demo penalties: the config in effect on %s has no LATE_SLA type", date(at))
		}
		id := g.id("driver_penalties", d.nid+":"+at.Format("2006-01-02T15:04"))
		r := Row{"id": id, "tenant_id": g.own, "tenant_source": "driver", "driver_id": d.id, "type_code": "LATE_SLA",
			"type_name_snapshot": name, "total_thb": money(sp.totalTHB), "remaining_thb": money(sp.totalTHB),
			"installments_total": intNum(sp.installments), "installments_paid": intNum(0), "reason": sp.reason, "status": "pending",
			"incurred_at": ts(at), "created_by": by, "created_at": ts(at), "updated_at": ts(at)}
		g.add("driver_penalties", r)
		out = append(out, &payPenalty{row: r, id: id, driver: d.id, created: at, total: sp.totalTHB, remaining: sp.totalTHB,
			instTotal: sp.installments, typeCode: "LATE_SLA", typeName: name, updated: at})
	}
	slices.SortFunc(out, func(a, b *payPenalty) int {
		if c := a.created.Compare(b.created); c != 0 {
			return c
		}
		return compareStrings(a.id, b.id)
	})
	return out, nil
}

// writeRun appends one run, its lines, its penalty applications and, once approved, its payout ledger row.
func (g *hrGen) writeRun(d payDriver, rd round, s compute.Payslip, status string, approvedAt, paidAt time.Time, approver string) {
	p := rd.period
	key := d.nid + ":" + p.Key + ":" + string(p.Round)
	runID := g.id("payroll_runs", key)
	run := Row{"id": runID, "tenant_id": g.own, "tenant_source": "driver", "driver_id": d.id, "pay_period": p.Key,
		"pay_round": string(p.Round), "period_start": ts(p.Start), "period_end": ts(p.End), "status": status,
		"total_earnings_thb": money(s.TotalEarningsTHB), "total_deductions_thb": money(s.TotalDeductionsTHB), "net_pay_thb": money(s.NetPayTHB),
		"currency": "THB", "created_at": ts(rd.gen)}
	if status == "approved" || status == "paid" {
		txID := g.id("transactions", "payout:"+key)
		run["approved_by"], run["approved_at"], run["ledger_transaction_id"] = approver, ts(approvedAt), txID
		g.add("transactions", Row{"id": txID, "tenant_id": g.own, "tenant_source": "driver", "tx_type": "driver_payout",
			"sub_type": "Driver Compensation", "amount_thb": money(s.NetPayTHB), "tx_date": date(approvedAt), "tx_date_source": "bangkok",
			"driver_id": d.id, "payroll_run_id": runID, "pay_period": p.Key, "pay_round": string(p.Round), "performed_by_user_id": approver,
			"created_at": ts(approvedAt)})
	}
	if status == "paid" {
		run["payment_date"], run["payment_method"] = ts(paidAt), "transfer"
	}
	g.add("payroll_runs", run)
	for i, l := range s.Lines {
		line := Row{"id": g.id("payroll_line_items", key+":"+strconv.Itoa(i)), "payroll_run_id": runID, "line_no": intNum(i),
			"item_type": l.Type, "category": l.Category, "name": l.Name, "amount_thb": money(l.AmountTHB)}
		if l.Quantity != nil {
			line["quantity"] = strconv.FormatFloat(*l.Quantity, 'f', 3, 64)
		}
		if l.UnitRateTHB != nil {
			line["unit_rate_thb"] = money(*l.UnitRateTHB)
		}
		if l.ReferenceID != "" {
			line["reference_id"] = l.ReferenceID
		}
		if l.Meta != nil {
			line["meta"] = l.Meta
		}
		g.add("payroll_line_items", line)
	}
	for _, a := range s.Applications {
		g.add("payroll_penalty_applications", Row{"payroll_run_id": runID, "penalty_id": a.PenaltyID, "applied_thb": money(a.AppliedTHB)})
	}
}

// --- leave -----------------------------------------------------------------------------------------------------

func (g *hrGen) leave() error {
	for _, l := range demoLeaves {
		d := g.byKey[l.driver]
		if d == nil {
			continue
		}
		start := g.anchor.AddDate(0, 0, l.startDay)
		end := start.AddDate(0, 0, l.days-1)
		// A leave that starts later was requested the evening before the anchor day; one that has started, on its
		// first morning (a sick leave reported as it begins).
		created := g.anchor.AddDate(0, 0, -1).Add(20 * time.Hour)
		if l.startDay < 0 {
			created = start.Add(7 * time.Hour)
		}
		key := d.Str("id_card") + ":" + date(start)
		id := g.id("leave_requests", key)
		r := Row{"id": id, "tenant_id": d.Str("tenant_id"), "tenant_source": "driver", "driver_id": d.Str("id"), "leave_type": l.typ,
			"status": l.status, "start_date": date(start), "end_date": date(end), "reason": l.reason,
			"client_op_id": ClientOpID(g.o.Namespace, "leave:"+key).String(), "created_at": ts(created)}
		if l.approver != "" {
			r["approver_user_id"], r["decided_at"] = g.user(l.approver), ts(created.Add(5*time.Hour))
		}
		if l.rejection != "" {
			r["rejection_reason"] = l.rejection
		}
		g.add("leave_requests", r)
		for pos := range l.attachments {
			fid, err := g.evidence(uuid.MustParse(id), pos, created.Add(-time.Duration(l.attachments-pos)*time.Minute), d)
			if err != nil {
				return err
			}
			g.add("leave_request_attachments", Row{"leave_request_id": id, "file_id": fid, "position": intNum(pos)})
		}
	}
	return nil
}

// evidence appends the file_objects row of one leave evidence photo (drawn by objects like every generated photo).
func (g *hrGen) evidence(owner uuid.UUID, pos int, at time.Time, d Row) (string, error) {
	p := storage.Purposes["leave_evidence"]
	key, err := p.Key(storage.KeyInput{EntityID: owner, Variant: strconv.Itoa(pos), ContentType: "image/jpeg", Now: at})
	if err != nil {
		return "", fmt.Errorf("seed: leave_evidence key: %w", err)
	}
	id := g.id("file_objects", "logitrack:"+key)
	r := Row{"id": id, "bucket": "logitrack", "object_key": key, "content_type": "image/jpeg", "size_bytes": "$seed:generated",
		"sha256": "$seed:generated", "purpose": "leave_evidence", "owner_kind": p.OwnerKind, "owner_id": owner.String(),
		"tenant_id": d.Str("tenant_id"), "status": "committed", "created_at": ts(at)}
	if u := d.Str("user_id"); u != "" {
		r["uploaded_by"] = u
	}
	g.add("file_objects", r)
	return id, nil
}

// hrTables are the tables the HR generator appends to (their demo counts are exact in Appendix D §D.1.2).
var hrTables = []string{"driver_penalties", "payroll_runs", "payroll_line_items", "payroll_penalty_applications", "transactions",
	"leave_requests", "leave_request_attachments", "holidays"}
