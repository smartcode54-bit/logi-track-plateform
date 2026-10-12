package seed

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The billing and finance volume of the load profile (T43, issue #63; main spec §14.1-§14.2, Appendix D §D.1.2 "Load
// billing and finance"), on the load master data of T26 and over the 365 days ending SEED_ANCHOR_DATE: rate cards in
// two rounds (three for every third hub) on every load hub a party links, with SUPPLEMENTARY cards, R16 tie pairs and
// voided hand entries; for each load customer LC01-LC08 a fuel round a month (fuel band or multiplier) with voided
// duplicates, its service fees and three standby rounds; the fuel logs, toll imports and other expenses of the load
// drivers on the trucks they held; and the maintenance history they imply (maintenance_history.go) with periodic
// repairs and the repair that keeps every 13th load truck in the shop. Party-wide inputs go to the load customers only
// and every card prices a route of a load hub, so no demo or smoke price moves (load only appends). The statements and
// their lines and counters wait for T35V, the load-scale operations generator that gives load trips to invoice
// (developer-spec §18.3): until then load carries the demo ones. Every count is a function of the window, never of
// SEED_RANDOM_SEED, except the PM bookings, which follow the master mileages, intervals and vehicle classes T26 draws,
// and the receipts, since which rows are rejected follows the assignment spans T26 draws.

// The load volume (Appendix D §D.1.2).
const (
	loadFinDays      = 365
	loadFuelLogs     = 40000
	loadTollRows     = 13300
	loadOtherRows    = 6700
	loadPendingRows  = 4700
	loadRejectedRows = 3500
	loadReceiptFuel  = 200 // a receipt on every 200th fuel log not rejected
	loadReceiptOther = 100 // and on every 100th other expense
	loadFuelRounds   = 12  // monthly rounds per load customer, beside the voided duplicates
	loadRepairEvery  = 100 // days between the periodic repairs of a load truck
)

// loadFin is one run of the load generator; mark are the table lengths before the load master data.
type loadFin struct {
	*finGen
	mark   map[string]int
	start  time.Time    // Bangkok midnight of load window day 0
	rounds [3]time.Time // the rate and standby rounds: the window's first month, four and eight months later
}

// addLoadFinance appends the load profile's billing and finance volume (after addLoadMaster; mark are the table
// lengths before it).
func addLoadFinance(p *Plan, o Options, mark map[string]int) error {
	g := newFinGen(p, o)
	g.r = rng(o, "load-finance")
	start := g.anchor.AddDate(0, 0, -(loadFinDays - 1))
	r1 := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, clock.Bangkok)
	lf := &loadFin{finGen: g, mark: mark, start: start, rounds: [3]time.Time{r1, r1.AddDate(0, 4, 0), r1.AddDate(0, 8, 0)}}
	if err := lf.rateCards(); err != nil {
		return err
	}
	if err := lf.customerInputs(); err != nil {
		return err
	}
	odo, approved, shop, err := lf.expenses()
	if err != nil {
		return err
	}
	return lf.maintenance(odo, approved, shop)
}

// partyCodes are the codes of the plan's billing parties (the customer's, else the carrier's).
func partyCodes(p *Plan) map[string]string {
	out := map[string]string{}
	for _, pt := range planParties(p) {
		out[pt.id] = pt.code
	}
	return out
}

// loadCardBase is the round-1 price of a load card by vehicle class, plus a SOC offset and 10 THB per hub position.
var (
	loadCardBase  = map[string]int{"4WJ": 900, "6WH": 1400, "10WH": 2100}
	loadSOCOffset = map[string]int{"SOCE": 0, "SOCN": 200, "SOCW": 350}
	loadCardClass = []string{"4WJ", "6WH", "10WH"}
)

// rateCards appends the load cards: every load hub a party links gets PRIMARY cards to two SOCs in 4WJ, 6WH and 10WH
// (a J&T SPK hub: from SPK-GW to it in 4WJ and 6WH), one import per party and round (round 2 three percent dearer, round
// 3 for every third hub another three percent), a SUPPLEMENTARY 6WH card on every fourth hub, the round-2 4WJ card of
// every twentieth imported twice (the R16 tie: the first created prices), and a voided hand entry on every tenth.
func (lf *loadFin) rateCards() error {
	codes := partyCodes(lf.p)
	spk := lf.s.MustID("BP_SPK").String()
	imports := map[string]time.Time{} // party + round -> the import's instant
	importAt := func(party string, k int) time.Time {
		key := fmt.Sprintf("%s:%d", party, k)
		if t, ok := imports[key]; ok {
			return t
		}
		eff := lf.rounds[k]
		t := time.Date(eff.Year(), eff.Month(), eff.Day()-6, 9, 30, 0, 0, clock.Bangkok).Add(time.Duration(len(imports)%40) * time.Minute)
		imports[key] = t
		return t
	}
	step := func(thb, k int) int {
		for range k {
			thb = int(math.Round(float64(thb)*1.03/10)) * 10
		}
		return thb
	}
	for i, h := range lf.p.Tables["hubs"][lf.mark["hubs"]:] {
		party, code := h.Str("linked_party_id"), h.Str("source_id")
		if party == "" {
			continue // a hub a driver made: no party links it
		}
		pc := codes[party]
		if pc == "" {
			return fmt.Errorf("seed: load hub %s links an unknown party", code)
		}
		type route struct{ hub, dest, class string }
		var routes []route
		if party == spk && strings.HasPrefix(code, "SPK89") {
			routes = []route{{"SPK-GW", code, "4WJ"}, {"SPK-GW", code, "6WH"}}
		} else {
			socs := []string{"SOCE", "SOCN"}
			if i%2 == 1 {
				socs = []string{"SOCN", "SOCW"}
			}
			for _, soc := range socs {
				for _, class := range loadCardClass {
					routes = append(routes, route{code, soc, class})
				}
			}
		}
		price := func(r route) int {
			base := loadCardBase[r.class] + loadSOCOffset[r.dest] + 10*(i%25)
			if r.hub == "SPK-GW" {
				base = loadCardBase[r.class] + 150 + 10*(i%25)
			}
			return base
		}
		n := 2
		if i%3 == 0 {
			n = 3
		}
		for k := range n {
			created := importAt(party, k)
			id := importID("rc", created)
			for _, r := range routes {
				lf.card(rateCard{party: party, code: pc, hub: r.hub, dest: r.dest, class: r.class, cat: "PRIMARY",
					thb: step(price(r), k), eff: lf.rounds[k], created: created, importID: id})
			}
			if i%4 == 1 && routes[0].hub != "SPK-GW" {
				sup := route{routes[0].hub, routes[0].dest, "6WH"}
				lf.card(rateCard{party: party, code: pc, hub: sup.hub, dest: sup.dest, class: sup.class, cat: "SUPPLEMENTARY",
					thb: int(math.Round(float64(step(price(sup), k))*0.8/10)) * 10, eff: lf.rounds[k], created: created, importID: id})
			}
		}
		r := routes[0]
		if i%20 == 7 {
			tie := importAt(party, 1).Add(5 * time.Minute)
			lf.card(rateCard{party: party, code: pc, hub: r.hub, dest: r.dest, class: r.class, cat: "PRIMARY",
				thb: step(price(r), 1) + 20, eff: lf.rounds[1], created: tie, importID: importID("rc", tie)})
		}
		if i%10 == 3 {
			typo := lf.rounds[1].AddDate(0, 0, 3).Add(14*time.Hour + time.Duration(i)*time.Second)
			lf.card(rateCard{party: party, code: pc, hub: r.hub, dest: r.dest, class: r.class, cat: "PRIMARY",
				thb: step(price(r), 1) * 10, eff: lf.rounds[1], created: typo, importID: importID("manual", typo),
				void: "พิมพ์ราคาผิด เกินหนึ่งหลัก"})
		}
	}
	return nil
}

// customers are the billing parties of the load customers LC01-LC08, in code order.
func (lf *loadFin) customers() []stmtParty {
	var out []stmtParty
	for _, pt := range planParties(lf.p) {
		if strings.HasPrefix(pt.code, "LC") {
			out = append(out, pt)
		}
	}
	return out
}

// customerInputs appends what the office types for each load customer: a fuel round on the first of every month of
// the window (even customers on the fuel band, its reference price rising 0.35 THB a month; odd ones a multiplier and a
// per-trip add), the duplicate announcement of every fourth month voided two hours later, an extra-stop, a standby and
// a waiting-time fee, and a standby rate from each of the three rounds.
func (lf *loadFin) customerInputs() error {
	parties := lf.customers()
	if len(parties) != loadCustomers {
		return fmt.Errorf("seed: %d load customers, want %d", len(parties), loadCustomers)
	}
	for pi, pt := range parties {
		for k := range loadFuelRounds {
			eff := lf.rounds[0].AddDate(0, k, 0)
			created := time.Date(eff.Year(), eff.Month(), eff.Day()-3, 10, 0, 0, 0, clock.Bangkok).Add(time.Duration(5*pi) * time.Minute)
			r := Row{"id": lf.id("customer_fuel_rate_adjustments", pt.code+":"+clock.DateString(eff)), "tenant_id": lf.own,
				"tenant_source": "form", "billing_party_id": pt.id, "effective_from_date": clock.DateString(eff),
				"effective_from_at": ts(eff), "created_by": lf.mgr, "created_at": ts(created), "voided": false}
			if pi%2 == 0 {
				ref := math.Round((33.00+0.35*float64(k)+0.10*float64(pi))*100) / 100
				r["rate_multiplier"], r["add_thb_per_trip"] = "1.000000", money(compute.FuelSurchargeTHB(ref, 41, 10))
				r["reference_fuel_price_thb"], r["fuel_band_enabled"] = money(ref), true
				r["fuel_band_baseline_floor"], r["fuel_band_thb_per_baht"] = intNum(41), "10.00"
				r["announcement_note"] = fmt.Sprintf("รอบ %s ดีเซลอ้างอิง %.2f", thaiMonth(eff), ref)
			} else {
				r["rate_multiplier"] = fmt.Sprintf("%.6f", 1+0.01*float64(k%5))
				r["add_thb_per_trip"] = fmt.Sprintf("%d.00", 10*(k%3))
				r["announcement_note"] = fmt.Sprintf("รอบ %s ปรับสูตรราคา", thaiMonth(eff))
			}
			lf.add("customer_fuel_rate_adjustments", r)
			if k%4 == 2 {
				dup := r.clone()
				dup["id"] = lf.id("customer_fuel_rate_adjustments", pt.code+":"+clock.DateString(eff)+":duplicate")
				dup["created_at"] = ts(created.Add(30 * time.Minute))
				dup["voided"], dup["voided_at"], dup["voided_by"] = true, ts(created.Add(150*time.Minute)), lf.admin
				dup["voided_reason"] = "ประกาศซ้ำรอบ ยกเลิก"
				lf.add("customer_fuel_rate_adjustments", dup)
			}
		}
		feeAt := ts(time.Date(lf.rounds[0].Year(), lf.rounds[0].Month(), lf.rounds[0].Day()-6, 9, 50, 0, 0, clock.Bangkok))
		for _, f := range []struct {
			typ, unit, note string
			thb             int
		}{{"extra_stop", "per_stop", "ค่าโยกจุดที่ 2 ขึ้นไป", 250 + 25*(pi%4)}, {"standby", "per_trip", "ค่ารอเมื่อไม่มีอัตรา standby", 450 + 50*(pi%3)},
			{"waiting_time", "per_trip", "ค่ารอลงสินค้าเกิน 2 ชั่วโมง", 100 + 25*(pi%3)}} {
			lf.add("customer_service_fees", Row{"id": lf.id("customer_service_fees", pt.code+":"+f.typ), "tenant_id": lf.own,
				"tenant_source": "form", "billing_party_id": pt.id, "fee_type": f.typ, "amount_thb": fmt.Sprintf("%d.00", f.thb),
				"unit": f.unit, "note": f.note, "created_at": feeAt})
		}
		for k, eff := range lf.rounds {
			lf.add("standby_rate_entries", Row{"id": lf.id("standby_rate_entries", pt.code+":"+clock.DateString(eff)),
				"tenant_id": lf.own, "tenant_source": "form", "billing_party_id": pt.id, "rate_thb": fmt.Sprintf("%d.00", 600+50*k+25*(pi%4)),
				"effective_from_date": clock.DateString(eff), "effective_from_at": ts(eff), "note": "อัตรา standby รอบ " + thaiMonth(eff),
				"created_at": ts(time.Date(eff.Year(), eff.Month(), eff.Day()-5, 10, 5, 0, 0, clock.Bangkok))})
		}
	}
	return nil
}

// loadShopStart is the day a load truck T26 left in the shop went in (the repair that keeps it there): 2 to 6 days
// before the anchor, by its position.
func (lf *loadFin) loadShopStart(i int) time.Time { return lf.anchor.AddDate(0, 0, -(2 + i%5)) }

// expenses appends the load drivers' expenses over the past days of the load window, on the truck each held that day:
// fuel logs, toll rows (each tenant's office imports its own) and other expenses, none on a truck from the day it went
// into the shop. It returns the odometer models, the approved fuel logs and the shop start of each truck in the shop.
func (lf *loadFin) expenses() (map[string]odometer, []fuelApproval, map[string]time.Time, error) {
	og := &opsGen{p: lf.p, o: lf.o, s: lf.s}
	if err := og.collectDrivers(); err != nil {
		return nil, nil, nil, err
	}
	office, err := approvers(lf.p, lf.o)
	if err != nil {
		return nil, nil, nil, err
	}
	load := map[string]bool{}
	for _, d := range lf.p.Tables["drivers"][lf.mark["drivers"]:] {
		load[d.Str("id")] = true
	}
	shop := map[string]time.Time{}
	for i, t := range lf.p.Tables["trucks"][lf.mark["trucks"]:] {
		if t.Str("status") == "maintenance" {
			shop[t.Str("id")] = lf.loadShopStart(i)
		}
	}
	var slots []expSlot
	for i := range loadFinDays - 1 {
		day := lf.start.AddDate(0, 0, i)
		for _, d := range og.drivers {
			if !load[d.id] {
				continue
			}
			tr := d.truckOn(day)
			if tr == nil {
				continue
			}
			if in, ok := shop[tr.Str("id")]; ok && !day.Before(in) {
				continue
			}
			slots = append(slots, expSlot{day: i, d: d, truck: tr})
		}
	}
	if len(slots) == 0 {
		return nil, nil, nil, fmt.Errorf("seed: no load driver holds a truck in the load window")
	}
	odo, approved, err := lf.writeExpenses(og, expensePlan{tag: "L", start: lf.start, anchor: lf.anchor, days: loadFinDays,
		fuel: loadFuelLogs, toll: loadTollRows, other: loadOtherRows, pending: loadPendingRows, rejected: loadRejectedRows,
		fuelSlots: slots, tollSlots: slots, otherSlots: slots, tollOffset: len(slots) / 3, otherOffset: len(slots) / 5,
		receiptFuel: loadReceiptFuel, receiptOther: loadReceiptOther, office: office})
	return odo, approved, shop, err
}

// loadRepairs are the periodic repairs of a load truck (service type, labour, parts).
var loadRepairs = []struct {
	service      string
	labor, parts int
}{
	{"เปลี่ยนยาง 2 เส้น", 400, 6800}, {"ซ่อมระบบเบรก", 1200, 2600}, {"เปลี่ยนแบตเตอรี่", 200, 3900},
	{"ซ่อมระบบไฟส่องสว่าง", 600, 850}, {"ซ่อมระบบแอร์", 1500, 2200}, {"เปลี่ยนใบปัดน้ำฝนและไส้กรองอากาศ", 300, 750},
}

// maintenance replays the load trucks' history: the PM bookings of their approved fuel logs, a repair every
// loadRepairEvery days of the active assignment of each (booked the afternoon before, in the shop from 08:30 to 16:30
// the next day, completed), and the repair in progress of each truck T26 left in the shop.
func (lf *loadFin) maintenance(odo map[string]odometer, approved []fuelApproval, shop map[string]time.Time) error {
	office, err := approvers(lf.p, lf.o)
	if err != nil {
		return err
	}
	h := newFleetHistory(lf.p, lf.o)
	register := func(tr Row) *fleetTruck {
		m, ok := odo[tr.Str("id")]
		if !ok {
			m = odometerOf(tr)
		}
		start := lf.start
		return h.truck(tr, func(day time.Time) int { return m.at(windowDay(start, day), 0) }, office[tr.Str("tenant_id")])
	}
	for _, a := range approved {
		h.fuel(register(a.truck), a.at, a.reading)
	}
	trucks := lf.p.Tables["trucks"][lf.mark["trucks"]:]
	from := map[string]time.Time{}
	for _, a := range lf.p.Tables["truck_assignments"][lf.mark["truck_assignments"]:] {
		if a.Str("status") == "active" {
			at, err := time.Parse(time.RFC3339, a.Str("created_at"))
			if err != nil {
				return fmt.Errorf("seed: assignment %s: %w", a.Str("id"), err)
			}
			from[a.Str("truck_id")] = at
		}
	}
	n := 0
	for i, tr := range trucks {
		held, ok := from[tr.Str("id")]
		if !ok {
			continue
		}
		b := held.In(clock.Bangkok)
		first := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, clock.Bangkok).AddDate(0, 0, 40+i%30)
		last := lf.anchor.AddDate(0, 0, -3)
		if in, ok := shop[tr.Str("id")]; ok {
			last = in.AddDate(0, 0, -3)
		}
		for day := first; day.Before(last); day = day.AddDate(0, 0, loadRepairEvery) {
			fix := loadRepairs[n%len(loadRepairs)]
			n++
			booked, in, out := day.AddDate(0, 0, -1).Add(14*time.Hour), day.Add(8*time.Hour+30*time.Minute), day.Add(16*time.Hour+30*time.Minute)
			r := lf.repair(tr, fix.service, day, "completed", office[tr.Str("tenant_id")])
			r["end_date"], r["check_in_at"], r["check_out_at"] = clock.DateString(day), ts(in), ts(out)
			r["cost_labor_thb"], r["cost_parts_thb"] = fmt.Sprintf("%d.00", fix.labor), fmt.Sprintf("%d.00", fix.parts)
			r["total_cost_thb"], r["invoice_amount_thb"] = fmt.Sprintf("%d.00", fix.labor+fix.parts), fmt.Sprintf("%d.00", fix.labor+fix.parts)
			r["provider"], r["payment_method"] = garages[n%len(garages)], []string{"transfer", "cash", "billing"}[n%3]
			r["created_at"], r["updated_at"] = ts(booked), ts(out)
			h.record(register(tr), r, fleetStep{booked, "scheduled"}, fleetStep{in, "in_progress"}, fleetStep{out, "completed"})
		}
		if in, ok := shop[tr.Str("id")]; ok {
			booked, checkIn := in.AddDate(0, 0, -1).Add(16*time.Hour), in.Add(8*time.Hour+40*time.Minute)
			r := lf.repair(tr, "ซ่อมเครื่องยนต์", in, "in_progress", office[tr.Str("tenant_id")])
			r["check_in_at"], r["provider"], r["payment_method"] = ts(checkIn), garages[i%len(garages)], "billing"
			r["notes"], r["created_at"], r["updated_at"] = "เครื่องยนต์มีเสียงดัง รออะไหล่", ts(booked), ts(checkIn)
			h.record(register(tr), r, fleetStep{booked, "scheduled"}, fleetStep{checkIn, "in_progress"})
		}
	}
	changed, err := h.run()
	if err != nil {
		return err
	}
	lf.p.noteTruckChanges(changed)
	return nil
}

// repair is a maintenance record of a load truck the office books (CM), by its approver.
func (lf *loadFin) repair(tr Row, service string, start time.Time, status, by string) Row {
	natural := tr.Str("license_plate") + ":" + clock.DateString(start) + ":CM"
	r := Row{"id": lf.id("maintenance_records", natural), "tenant_id": tr.Str("tenant_id"), "tenant_source": "truck",
		"truck_id": tr.Str("id"), "truck_license_plate_snapshot": tr.Str("license_plate"), "maintenance_type": "CM",
		"service_type": service, "start_date": clock.DateString(start), "status": status, "created_by": by, "updated_by": by}
	if brand := tr.Str("brand"); brand != "" {
		r["truck_brand_snapshot"] = brand
	}
	return r
}

// loadMark are the table lengths before the load master data.
func loadMark(p *Plan) map[string]int {
	out := map[string]int{}
	for _, t := range []string{"hubs", "trucks", "drivers", "truck_assignments"} {
		out[t] = len(p.Tables[t])
	}
	return out
}
