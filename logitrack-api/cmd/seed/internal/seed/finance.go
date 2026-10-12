package seed

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strconv"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// The billing and finance generator of the demo profile (T43, issue #63; Appendix D §D.1.2 "Demo billing and finance",
// §D.2 #3-#8, #15, #16): on top of the smoke rate tables, two rate rounds for the routes the demo operations run that
// no smoke card prices (the SPX demo hubs to SOCE and SOCN, the CJSF distribution centres), a tie pair (R16) and two
// voided cards; a second fuel round for SPX (multiplier) and SPK (fuel band); a waiting-time fee; a mid-month CJSF
// standby rate; three months of fuel, toll and other expenses on the trucks the generated drivers held; and three
// maintenance records (completed, scheduled, in progress). The statements of the closed months come from
// statements.go once every price is in. Smoke rows are never touched (the generator only appends; the routes, fuel
// rounds and standby rate it adds price no smoke trip or standby record, so every golden vector of Appendix D §D.4.3
// and §D.4.4 keeps its value), and every count is a function of the window, never of SEED_RANDOM_SEED: the stream draws
// times, volumes, odometers and amounts only.
//
// Rate cards, fuel rounds, fees and standby rates are the inputs the office types; the prices of trips and standby
// records stay the engine's (priceOperations, Appendix D §D.1.4).

// finTables are the tables the T43 generator writes; load carries their demo rows until the load-scale generators land
// (Appendix D §D.1.2). finExactTables are those whose demo count does not depend on SEED_RANDOM_SEED: all but the
// statement lines, one per priced row of a party and month, whose routes the stream draws.
var (
	finTables = []string{"customer_rate_entries", "customer_fuel_rate_adjustments", "customer_service_fees",
		"standby_rate_entries", "billing_statements", "billing_statement_lines", "billing_counters", "vehicle_expenses",
		"maintenance_records"}
	finExactTables = []string{"customer_rate_entries", "customer_fuel_rate_adjustments", "customer_service_fees",
		"standby_rate_entries", "billing_statements", "billing_counters", "vehicle_expenses", "maintenance_records"}
)

// The demo expense volume (Appendix D §D.2 #16: 180 with the four of smoke: fuel 120, toll 40, other 20; 40 % pending,
// 50 % approved, 10 % rejected). Toll imports are always approved.
const (
	demoFuelLogs      = 118
	demoTollRows      = 39
	demoOtherRows     = 19
	demoPendingRows   = 71
	demoRejectedRows  = 17
	pmAlertThresholdK = 2000 // maintenance.PMAlertThresholdKm: a fuel log this close to the next service books a PM
)

// finGen holds one run of the generator.
type finGen struct {
	p      *Plan
	o      Options
	s      Symbols
	r      *rand.Rand
	start  time.Time // Bangkok midnight of window day 0 (the operations window)
	anchor time.Time // Bangkok midnight of SEED_ANCHOR_DATE
	round1 time.Time // first rate round: the first day of the window's first month
	round2 time.Time // second rate round: two months later
	own    string
	mgr    string // users.id of the manager who imports rate cards and announces rounds
	admin  string // users.id of the admin who voids
	ops    string // users.id of the operations staff (toll import, maintenance)
}

func newFinGen(p *Plan, o Options) *finGen {
	anchor := time.Date(o.Anchor.Year(), o.Anchor.Month(), o.Anchor.Day(), 0, 0, 0, 0, clock.Bangkok)
	start := anchor.AddDate(0, 0, -(opsDays - 1))
	round1 := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, clock.Bangkok)
	return &finGen{p: p, o: o, s: p.Symbols, r: rng(o, "demo-finance"), start: start, anchor: anchor, round1: round1,
		round2: round1.AddDate(0, 2, 0), own: p.Symbols.MustID(ownFleetSymbol).String(),
		mgr: p.Symbols.MustID("U_WRT_MGR").String(), admin: p.Symbols.MustID("U_WRT_ADMIN").String(),
		ops: p.Symbols.MustID("U_WRT_OPS").String()}
}

func (g *finGen) add(table string, r Row) { g.p.Tables[table] = append(g.p.Tables[table], r) }

func (g *finGen) id(table, key string) string { return ID(g.o.Namespace, table, key).String() }

// addDemoFinance appends the rate tables, the expenses and the maintenance records of the demo (it runs after the
// operations generator, so the expenses follow the trucks the drivers held; the pricing pass and the statements come
// later in Build).
func addDemoFinance(p *Plan, o Options) error {
	g := newFinGen(p, o)
	g.rateCards()
	g.fuelRounds()
	g.serviceFees()
	g.standbyRates()
	odo, err := g.expenses()
	if err != nil {
		return err
	}
	return g.maintenance(odo)
}

// --- rate tables --------------------------------------------------------------------------------------------

// spxCardBase are the round-1 prices of the SPX demo-hub cards by vehicle class and SOC (round 2 adds spxRound2Step);
// a hub adds 10 THB per position in spxDemoHubs. SOCW gets no card: its trips stay the "need a rate card" rows.
var (
	spxCardSOCs  = []string{"SOCE", "SOCN"}
	spxCardBase  = map[string]map[string]int{"4WJ": {"SOCE": 950, "SOCN": 1200}, "6WH": {"SOCE": 1450, "SOCN": 1700}, "10WH": {"SOCE": 2200, "SOCN": 2600}}
	spxRound2    = map[string]int{"4WJ": 40, "6WH": 60, "10WH": 80}
	spxCardClass = []string{"4WJ", "6WH", "10WH"}
)

// importID is the import id the rate-card import writes: rc_{epoch ms} (manual_{ms} for a hand-entered card).
func importID(prefix string, at time.Time) string {
	return prefix + "_" + strconv.FormatInt(at.UnixMilli(), 10)
}

// rateCard is one customer_rate_entries row.
type rateCard struct {
	party, code      string // party id, its code (the natural key)
	hub, dest, class string
	cat              string
	thb              int
	eff, created     time.Time
	importID         string
	void             string // voided_reason ("" = live)
}

func (g *finGen) card(c rateCard) {
	hub, dest := compute.ExtractHubID(c.hub), compute.NormalizeDestinationCode(c.dest)
	natural := fmt.Sprintf("%s:%s:%s>%s:%s:%s", c.code, c.importID, hub, dest, c.class, c.cat)
	r := Row{"id": g.id("customer_rate_entries", natural), "tenant_id": g.own, "tenant_source": "form",
		"billing_party_id": c.party, "import_id": c.importID, "hub_code": hub, "raw_hub_name": c.hub, "destination_code": dest,
		"vehicle_class": c.class, "rate_thb": fmt.Sprintf("%d.00", c.thb), "job_category": c.cat,
		"effective_from_date": clock.DateString(c.eff), "effective_from_at": ts(c.eff), "imported_at": ts(c.created),
		"created_by": g.mgr, "created_at": ts(c.created), "voided": false}
	if c.void != "" {
		r["voided"], r["voided_at"], r["voided_by"], r["voided_reason"] = true, ts(c.created.Add(20*time.Minute)), g.admin, c.void
	}
	g.add("customer_rate_entries", r)
}

// rateCards appends two rounds for every SPX demo hub to SOCE and SOCN in each class (round 2 two months after round
// 1), the CJSF distribution-centre routes (round 2 of CJSF-BPL -> SOCE imported twice the same morning: the R16 tie, the
// first created prices), and two hand-entered cards voided twenty minutes later.
func (g *finGen) rateCards() {
	spx, cjsf := g.s.MustID("BP_SPX").String(), g.s.MustID("BP_CJSF").String()
	imp1 := time.Date(g.round1.Year(), g.round1.Month(), g.round1.Day()-6, 9, 30, 0, 0, clock.Bangkok)
	imp2 := time.Date(g.round2.Year(), g.round2.Month(), g.round2.Day()-7, 10, 0, 0, 0, clock.Bangkok)
	for _, round := range []struct {
		eff, created time.Time
		step         bool
	}{{g.round1, imp1, false}, {g.round2, imp2, true}} {
		id := importID("rc", round.created)
		for h, hub := range spxDemoHubs {
			for _, soc := range spxCardSOCs {
				for _, class := range spxCardClass {
					thb := spxCardBase[class][soc] + 10*h
					if round.step {
						thb += spxRound2[class]
					}
					g.card(rateCard{party: spx, code: "SPX", hub: hub, dest: soc, class: class, cat: "PRIMARY", thb: thb,
						eff: round.eff, created: round.created, importID: id})
				}
			}
		}
		bpl, lkb := 1750, 2400
		if round.step {
			bpl, lkb = 1800, 2480
		}
		g.card(rateCard{party: cjsf, code: "CJSF", hub: "CJSF-BPL", dest: "SOCE", class: "6WH", cat: "PRIMARY", thb: bpl,
			eff: round.eff, created: round.created.Add(10 * time.Minute), importID: importID("rc", round.created.Add(10*time.Minute))})
		g.card(rateCard{party: cjsf, code: "CJSF", hub: "CJSF-LKB", dest: "SPK890103", class: "10WH", cat: "PRIMARY", thb: lkb,
			eff: round.eff, created: round.created.Add(10 * time.Minute), importID: importID("rc", round.created.Add(10*time.Minute))})
	}
	// The R16 tie: a second import of the CJSF-BPL round-2 card five minutes later, same effective instant. The first
	// created (1800) prices; this one never does while that one lives.
	tie := imp2.Add(15 * time.Minute)
	g.card(rateCard{party: cjsf, code: "CJSF", hub: "CJSF-BPL", dest: "SOCE", class: "6WH", cat: "PRIMARY", thb: 1820,
		eff: g.round2, created: tie, importID: importID("rc", tie)})
	// Voided hand entries (void only: the trigger demands voided_by, the CHECK voided_at).
	typo := imp2.AddDate(0, 0, 2).Add(4 * time.Hour)
	g.card(rateCard{party: spx, code: "SPX", hub: spxDemoHubs[0], dest: "SOCE", class: "4WJ", cat: "PRIMARY", thb: 9900,
		eff: g.round2, created: typo, importID: importID("manual", typo), void: "พิมพ์ราคาผิด เกินหนึ่งหลัก"})
	dup := typo.Add(time.Hour)
	g.card(rateCard{party: cjsf, code: "CJSF", hub: "CJSF-LKB", dest: "SPK890103", class: "10WH", cat: "PRIMARY", thb: 2840,
		eff: g.round2, created: dup, importID: importID("manual", dup), void: "ซ้ำกับรอบนำเข้า ยกเลิก"})
}

// fuelRounds appends the second round of SPX (multiplier and per-trip add, no band) and of SPK (fuel band: the add is
// the band surcharge of its reference price, as the rate-card page computes it), both from round 2.
func (g *finGen) fuelRounds() {
	created := time.Date(g.round2.Year(), g.round2.Month(), g.round2.Day()-4, 10, 0, 0, 0, clock.Bangkok)
	month := thaiMonth(g.round2)
	g.add("customer_fuel_rate_adjustments", Row{"id": g.id("customer_fuel_rate_adjustments", "SPX:"+clock.DateString(g.round2)),
		"tenant_id": g.own, "tenant_source": "form", "billing_party_id": g.s.MustID("BP_SPX").String(),
		"effective_from_date": clock.DateString(g.round2), "effective_from_at": ts(g.round2), "rate_multiplier": "1.050000",
		"add_thb_per_trip": "30.00", "announcement_note": "รอบ " + month + " ปรับสูตรราคา x1.05 +30", "created_by": g.mgr,
		"created_at": ts(created), "voided": false})
	const ref = 36.80
	add := compute.FuelSurchargeTHB(ref, 41, 10)
	g.add("customer_fuel_rate_adjustments", Row{"id": g.id("customer_fuel_rate_adjustments", "SPK:"+clock.DateString(g.round2)),
		"tenant_id": g.own, "tenant_source": "form", "billing_party_id": g.s.MustID("BP_SPK").String(),
		"effective_from_date": clock.DateString(g.round2), "effective_from_at": ts(g.round2), "rate_multiplier": "1.000000",
		"add_thb_per_trip": money(add), "reference_fuel_price_thb": money(ref),
		"announcement_note": fmt.Sprintf("รอบ %s ดีเซลอ้างอิง %.2f", month, ref), "fuel_band_enabled": true,
		"fuel_band_baseline_floor": intNum(41), "fuel_band_thb_per_baht": "10.00", "created_by": g.mgr,
		"created_at": ts(created.Add(5 * time.Minute)), "voided": false})
}

// serviceFees appends an SPX waiting-time fee: a fee type the engine never reads (like SF03), one per party and type
// (customer_service_fees_one_per_type).
func (g *finGen) serviceFees() {
	g.add("customer_service_fees", Row{"id": g.id("customer_service_fees", "SPX:waiting_time"), "tenant_id": g.own,
		"tenant_source": "form", "billing_party_id": g.s.MustID("BP_SPX").String(), "fee_type": "waiting_time",
		"amount_thb": "150.00", "unit": "per_trip", "note": "ค่ารอลงสินค้าเกิน 2 ชั่วโมง",
		"created_at": ts(time.Date(g.round1.Year(), g.round1.Month(), g.round1.Day()-6, 9, 50, 0, 0, clock.Bangkok))})
}

// standbyRates appends a mid-month CJSF standby round (900 from the 16th of round 2's month): the CJSF waits after it
// price at 900, those before at the smoke rates.
func (g *finGen) standbyRates() {
	eff := g.round2.AddDate(0, 0, 15)
	g.add("standby_rate_entries", Row{"id": g.id("standby_rate_entries", "CJSF:"+clock.DateString(eff)), "tenant_id": g.own,
		"tenant_source": "form", "billing_party_id": g.s.MustID("BP_CJSF").String(), "rate_thb": "900.00",
		"effective_from_date": clock.DateString(eff), "effective_from_at": ts(eff), "note": "ปรับอัตรา standby กลางเดือน",
		"created_at": ts(eff.AddDate(0, 0, -6).Add(10*time.Hour + 5*time.Minute))})
}

// thaiMonth is the Thai abbreviation of an instant's Bangkok month (the announcement notes of the smoke rounds).
func thaiMonth(t time.Time) string {
	return []string{"ม.ค.", "ก.พ.", "มี.ค.", "เม.ย.", "พ.ค.", "มิ.ย.", "ก.ค.", "ส.ค.", "ก.ย.", "ต.ค.", "พ.ย.", "ธ.ค."}[t.In(clock.Bangkok).Month()-1]
}

// --- expenses -----------------------------------------------------------------------------------------------

// expSlot is a past window day on which a generated driver held a truck.
type expSlot struct {
	day   int
	d     *opsDriver
	truck Row
}

// expense is one generated vehicle_expenses row before its status is decided.
type expense struct {
	kind string // fuel | toll | other
	slot expSlot
	at   time.Time
	n    int // position within its kind
}

// odometer is a truck's reading on window day i: its mileage at the master data's creation plus its daily run, and a
// drawn part of one day's run (monotone: a truck refuels at most once a day).
type odometer struct {
	base, perDay int
}

func (g *finGen) odometerOf(truck Row) odometer {
	base := 60000
	if v, ok := floatOf(truck["current_mileage"]); ok {
		base = int(v)
	}
	perDay := map[string]int{"4WJ": 130, "6WH": 150, "10WH": 170}[truck.Str("vehicle_class")]
	if perDay == 0 {
		perDay = 120
	}
	return odometer{base: base, perDay: perDay}
}

func (o odometer) at(day int, part int) int { return o.base + o.perDay*day + part }

// expenses appends the fuel logs, toll imports and other expenses of the window's past days, on the truck each driver
// held that day (Appendix D §D.2 #16), and returns the odometer model of every truck it refuelled.
func (g *finGen) expenses() (map[string]odometer, error) {
	og := &opsGen{p: g.p, o: g.o, s: g.s}
	if err := og.collectDrivers(); err != nil {
		return nil, err
	}
	tenantCode := map[string]string{}
	for _, t := range g.p.Tables["tenants"] {
		tenantCode[t.Str("id")] = t.Str("code")
	}
	tenantCode[g.own] = "WRT"
	var all, own []expSlot
	for i := range opsDays - 1 {
		day := g.start.AddDate(0, 0, i)
		for _, d := range og.drivers {
			if tr := d.truckOn(day); tr != nil {
				s := expSlot{day: i, d: d, truck: tr}
				all = append(all, s)
				if tenantCode[tr.Str("tenant_id")] == "WRT" {
					own = append(own, s)
				}
			}
		}
	}
	if len(all) < demoFuelLogs || len(own) < demoTollRows {
		return nil, fmt.Errorf("seed: %d driver days (%d own fleet) cannot carry the demo expenses", len(all), len(own))
	}
	pick := func(slots []expSlot, n, offset int) []expSlot { // n slots spread evenly over the window
		out := make([]expSlot, n)
		for j := range n {
			out[j] = slots[(j*len(slots)+offset)/n%len(slots)]
		}
		return out
	}
	var list []*expense
	for j, s := range pick(all, demoFuelLogs, 0) {
		at := g.start.AddDate(0, 0, s.day).Add(5*time.Hour + 30*time.Minute + time.Duration(5*g.r.IntN(8))*time.Minute)
		list = append(list, &expense{kind: "fuel", slot: s, at: at, n: j})
	}
	for j, s := range pick(own, demoTollRows, len(own)/2) {
		at := g.start.AddDate(0, 0, s.day).Add(8*time.Hour + 10*time.Minute + time.Duration(g.r.IntN(300))*time.Minute)
		list = append(list, &expense{kind: "toll", slot: s, at: at, n: j})
	}
	for j, s := range pick(all, demoOtherRows, len(all)/(2*demoOtherRows)) {
		at := g.start.AddDate(0, 0, s.day).Add(12*time.Hour + time.Duration(g.r.IntN(360))*time.Minute)
		list = append(list, &expense{kind: "other", slot: s, at: at, n: j})
	}
	// Statuses: toll imports approved; of the fuel and other rows the newest are still pending audit, and of the older
	// ones every so often one was rejected (the counts of §D.2 #16 whatever the anchor). Newest by window day, then fuel
	// (mornings) before other (afternoons), then position: never by a drawn time, so the statuses, and with them the
	// receipts (a rejected row has none), do not depend on SEED_RANDOM_SEED.
	var audited []*expense
	for _, e := range list {
		if e.kind != "toll" {
			audited = append(audited, e)
		}
	}
	sort.SliceStable(audited, func(i, j int) bool {
		a, b := audited[i], audited[j]
		if a.slot.day != b.slot.day {
			return a.slot.day > b.slot.day
		}
		if a.kind != b.kind {
			return a.kind == "other"
		}
		return a.n > b.n
	})
	status := map[*expense]string{}
	older := len(audited) - demoPendingRows
	for k, e := range audited {
		switch {
		case k < demoPendingRows:
			status[e] = "pending"
		case (k-demoPendingRows)*demoRejectedRows/older != (k-demoPendingRows+1)*demoRejectedRows/older:
			status[e] = "rejected"
		default:
			status[e] = "approved"
		}
	}
	// Fuel logs: odometers per truck in window order, the distance since the truck's previous log, the station's
	// tax invoice (unique per driver, vehicle_expenses_fuel_taxinv).
	odo := map[string]odometer{}
	last := map[string]int{}
	sort.SliceStable(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
	rejectNotes := []string{"ไม่มีใบเสร็จ", "ยอดไม่ตรงกับใบเสร็จ", "เลขไมล์ไม่สอดคล้องกับรอบก่อน", "ซ้ำกับรายการเดิม"}
	tollBatch := map[string]int{}
	for _, e := range list {
		s := e.slot
		tr := s.truck
		natural := fmt.Sprintf("%s:%s:%s", s.d.key, e.kind, e.at.Format("2006-01-02T15:04"))
		id := g.id("vehicle_expenses", natural)
		g.p.labels[id] = tr.Str("license_plate")
		r := Row{"id": id, "tenant_id": tr.Str("tenant_id"), "tenant_source": "driver", "driver_id": s.d.id,
			"truck_id": tr.Str("id"), "truck_license_plate_snapshot": tr.Str("license_plate"), "expense_at": ts(e.at)}
		created := e.at.Add(time.Minute)
		switch e.kind {
		case "fuel":
			m, ok := odo[tr.Str("id")]
			if !ok {
				m = g.odometerOf(tr)
				odo[tr.Str("id")] = m
			}
			reading := m.at(s.day, g.r.IntN(m.perDay/4))
			price := 33.50 + 0.025*float64(s.day) + float64(g.r.IntN(31)-15)/100
			price = math.Round(price*100) / 100
			lo, hi := 30, 45
			if v, ok := tankLitres[tr.Str("vehicle_class")]; ok {
				lo, hi = v[0], v[1]
			}
			milli := (lo+g.r.IntN(hi-lo))*1000 + g.r.IntN(1000)
			liters := float64(milli) / 1000
			r["expense_type"], r["volume_liters"] = "fuel", fmt.Sprintf("%d.%03d", milli/1000, milli%1000)
			r["price_per_liter"], r["amount_thb"] = money(price), money(jsmath.Round2(liters*price))
			r["odometer_km"] = intNum(reading)
			if prev, ok := last[tr.Str("id")]; ok {
				r["distance_km"] = fmt.Sprintf("%d.00", reading-prev)
			}
			last[tr.Str("id")] = reading
			r["tax_inv_id"] = fmt.Sprintf("SEED-TAXINV-G%04d", e.n+1)
			r["station_tax_id"] = thaiID(fmt.Sprintf("09999000030%d", e.n%10))
		case "toll":
			// The office imports a week of toll-gate rows every Monday at 10:00 (the anchor's morning for the last
			// week); the import numbers its rows from 1.
			days := (8 - int(e.at.Weekday())) % 7 // the next Monday after the trip's day
			if days == 0 {
				days = 7
			}
			monday := e.at.AddDate(0, 0, days)
			imported := time.Date(monday.Year(), monday.Month(), monday.Day(), 10, 0, 0, 0, clock.Bangkok)
			if !imported.Before(g.anchor) {
				imported = g.anchor.Add(9 * time.Hour)
			}
			batch := clock.DateString(imported)
			tollBatch[batch]++
			amounts := []int{25, 30, 45, 50, 60, 75, 90, 110}
			r["expense_type"], r["category"], r["amount_thb"] = "other", "toll", fmt.Sprintf("%d.00", amounts[g.r.IntN(len(amounts))])
			r["toll_import_sequence"], r["toll_location"] = intNum(tollBatch[batch]), tollGates[e.n%len(tollGates)]
			r["toll_lane"], r["toll_source_type"] = fmt.Sprintf("%02d", 1+g.r.IntN(12)), "ผ่านทาง"
			r["created_by_user_id"], r["created_at"], r["status"] = g.ops, ts(imported), "approved"
			g.add("vehicle_expenses", r)
			continue
		case "other":
			o := otherExpenses[e.n%len(otherExpenses)]
			r["expense_type"], r["category"], r["description"] = "other", o.category, o.text
			r["amount_thb"] = fmt.Sprintf("%d.00", o.lo+10*g.r.IntN((o.hi-o.lo)/10+1))
		}
		r["status"] = status[e]
		r["created_by_user_id"], r["created_at"] = s.d.user, ts(created)
		r["client_op_id"] = ClientOpID(g.o.Namespace, "vehicle_expenses:"+natural).String()
		if status[e] == "rejected" {
			r["admin_note"] = rejectNotes[e.n%len(rejectNotes)]
		} else if (e.kind == "fuel" && e.n%3 == 0) || (e.kind == "other" && e.n%2 == 0) {
			fid, err := og.object("expense_receipt", ID(g.o.Namespace, "vehicle_expenses", natural), "", created,
				tr.Str("tenant_id"), s.d.user)
			if err != nil {
				return nil, err
			}
			r["receipt_file_id"] = fid
		}
		g.add("vehicle_expenses", r)
	}
	return odo, nil
}

// tankLitres are the litres a refuel takes by vehicle class (lower, upper).
var tankLitres = map[string][2]int{"4WJ": {38, 58}, "6WH": {65, 95}, "10WH": {110, 160}}

// tollGates are synthetic toll-gate names (§D.1.9: no real place is implied).
var tollGates = []string{"ด่านตัวอย่าง A", "ด่านตัวอย่าง B", "ด่านทดสอบบางนา", "ด่านทดสอบรามอินทรา", "ด่านทดสอบดินแดง",
	"ด่านทดสอบสุขสวัสดิ์"}

// otherExpenses are the other-expense kinds a driver submits from the app (category, description, amount range).
var otherExpenses = []struct {
	category, text string
	lo, hi         int
}{
	{"tire_repair", "ปะยางหลังซ้าย", 150, 350},
	{"parking", "ค่าจอดรถรอลงสินค้า", 20, 80},
	{"other", "ค่าล้างรถ", 120, 200},
	{"maintenance", "เปลี่ยนหลอดไฟหน้า", 180, 450},
}

// --- maintenance ----------------------------------------------------------------------------------------------

// maintenance appends three records on the own fleet's demo trucks (Appendix D §D.2 #15, beside the three of smoke): a
// PM booked from a fuel log within the alert threshold and completed (the truck takes the service's mileage, date and
// next service mileage), a scheduled tyre change (the truck's active maintenance) and a brake repair in progress (the
// truck in the shop), as the maintenance service leaves the truck after each step (maintenance.applyToTruck).
func (g *finGen) maintenance(odo map[string]odometer) error {
	trucks := map[string]Row{}
	for _, t := range g.p.Tables["trucks"] {
		trucks[t.Str("license_plate")] = t
	}
	truck := func(key string) (Row, error) {
		for _, s := range demoTrucks {
			if s.key == key {
				if t := trucks[s.plate]; t != nil {
					return t, nil
				}
			}
		}
		return nil, fmt.Errorf("seed: demo truck %s not in the plan", key)
	}
	wrt1, err := truck("WRT1")
	if err != nil {
		return err
	}
	wrt2, err := truck("WRT2")
	if err != nil {
		return err
	}
	wrt3, err := truck("WRT3")
	if err != nil {
		return err
	}
	rec := func(t Row, typ, service string, start time.Time, status string) Row {
		natural := t.Str("license_plate") + ":" + clock.DateString(start) + ":" + typ
		r := Row{"id": g.id("maintenance_records", natural), "tenant_id": t.Str("tenant_id"), "tenant_source": "truck",
			"truck_id": t.Str("id"), "truck_license_plate_snapshot": t.Str("license_plate"), "truck_brand_snapshot": t.Str("brand"),
			"maintenance_type": typ, "service_type": service, "start_date": clock.DateString(start), "status": status,
			"created_by": g.ops}
		g.p.labels[r.Str("id")] = t.Str("license_plate")
		return r
	}

	// 1. The PM of WRT1: the first day its fuel logs come within the alert threshold of the next service (else window
	// day 20) books it; serviced two days later; the truck takes the mileage, the date and the next service mileage.
	pm, _ := floatOf(wrt1["pm_interval_km"])
	interval := int(pm)
	if interval == 0 {
		interval = 10000
	}
	m, ok := odo[wrt1.Str("id")]
	if !ok {
		m = g.odometerOf(wrt1)
	}
	due := (m.base/interval + 1) * interval
	day := 20
	for i := range opsDays - 4 {
		if due-m.at(i, 0) <= pmAlertThresholdK {
			day = i
			break
		}
	}
	booked := g.start.AddDate(0, 0, day).Add(10 * time.Hour)
	serviced := g.start.AddDate(0, 0, day+2)
	mileage := m.at(day+2, 0)
	r1 := rec(wrt1, "PM", fmt.Sprintf("เช็คระยะ %s กม.", groupThousands(due)), serviced, "completed")
	r1["end_date"] = clock.DateString(serviced.AddDate(0, 0, 1))
	r1["cost_labor_thb"], r1["cost_parts_thb"], r1["total_cost_thb"], r1["invoice_amount_thb"] = "1500.00", "3200.00", "4700.00", "4700.00"
	r1["provider"], r1["payment_method"], r1["driver_submitted"] = "อู่ทดสอบลาดกระบัง (ตัวอย่าง)", "transfer", true
	r1["current_mileage"], r1["next_service_mileage"] = intNum(mileage), intNum(due+interval)
	r1["check_in_at"], r1["check_out_at"] = ts(serviced.Add(8*time.Hour+30*time.Minute)), ts(serviced.AddDate(0, 0, 1).Add(16*time.Hour))
	r1["notes"] = fmt.Sprintf("ระบบอัตโนมัติ: จากรายการเติมน้ำมัน (เหลือไม่เกิน %s กม.)", groupThousands(pmAlertThresholdK))
	r1["created_at"], r1["updated_by"] = ts(booked), g.ops
	g.add("maintenance_records", r1)
	wrt1["current_mileage"], wrt1["next_service_mileage"] = intNum(mileage), intNum(due+interval)
	wrt1["last_service_date"] = r1.Str("end_date")

	// 2. A tyre change booked for two days after the anchor: WRT2's active maintenance, the truck still running.
	r2 := rec(wrt2, "CM", "เปลี่ยนยางหน้า 2 เส้น", g.anchor.AddDate(0, 0, 2), "scheduled")
	r2["appointment_time"], r2["provider"] = "09:30", "ร้านยางทดสอบบางนา (ตัวอย่าง)"
	r2["notes"], r2["created_at"] = "ดอกยางหน้าเหลือน้อย นัดเปลี่ยนก่อนรอบส่งงานถัดไป", ts(g.anchor.AddDate(0, 0, -1).Add(14*time.Hour))
	g.add("maintenance_records", r2)
	wrt2["active_maintenance_id"] = r2.Str("id")

	// 3. A brake repair in the shop since the day before the anchor: WRT3 is in maintenance (its driver D11 runs no
	// trip of the demo).
	r3 := rec(wrt3, "CM", "ซ่อมระบบเบรกหลัง", g.anchor.AddDate(0, 0, -1), "in_progress")
	r3["provider"], r3["payment_method"] = "อู่ทดสอบสมุทรปราการ (ตัวอย่าง)", "billing"
	r3["check_in_at"], r3["notes"] = ts(g.anchor.AddDate(0, 0, -1).Add(8*time.Hour+40*time.Minute)), "เบรกหลังมีเสียงดัง รอเปลี่ยนจานเบรก"
	r3["created_at"], r3["updated_by"] = ts(g.anchor.AddDate(0, 0, -2).Add(16*time.Hour)), g.ops
	g.add("maintenance_records", r3)
	wrt3["active_maintenance_id"], wrt3["status"] = r3.Str("id"), "maintenance"
	return nil
}

// groupThousands writes n with thousands separators ("50,000"), as the maintenance notes do.
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
