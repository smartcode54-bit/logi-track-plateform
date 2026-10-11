package seed

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// The operations generator of the demo profile (T35, issue #55; Appendix D §D.1.2, §D.2): three months of tasks, trips,
// delivery stops, trip photos, check-in photos, standby records and incident reports ending on SEED_ANCHOR_DATE, run by
// the generated drivers of the master data (T26) on the trucks they held each day (their truck_assignments), so every
// trip carries its truck snapshot and stays in the tenant of its truck (invariants 1 and 10a; the R24 broker d13 runs
// TTP work before its move and NWR work after it, beside its suspended TTP membership). The smoke rows are never
// touched: the generator only appends, numbers its tasks after the highest sequence the plan already holds per type and
// Bangkok day, and numbers its trips in a range the fixture does not use (ZXJB|ZXZB yyMMdd 2NNNN).
//
// Every count is a function of the window position of a day, never of SEED_RANDOM_SEED: the PCG stream (Appendix D
// §D.1.4) draws routes, raw place spellings, times and sizes only, so the counts of Appendix D §D.1.2 hold under every
// seed. Prices are never typed: priceOperations (operations_billing.go) runs the billing engine over the plan's rate
// tables once every generator has run and writes what it returns (Appendix D §D.1.4).

// opsDays is the length of the demo window: the 92 days ending on the anchor (1 July to 30 September for the default
// anchor). The last one is "today", the board of Appendix D §D.2 #1.
const opsDays = 92

// opsRoute is one kind of trip: a source and destination hub (first spelling = the hub code, the others raw values the
// legacy screens stored), the explicit billing party when the task names one, and the job category. Whether a route is
// priced is decided by the billing engine over the plan's rate tables, never here; the no-rate routes link to parties
// that hold no rate card for their (hub, destination) pair, so their stored no_rate reason holds (invariant 2a).
type opsRoute struct {
	name   string
	typ    string
	src    []string
	dest   []string
	party  string // billing party symbol ("" = the hub links decide, ADR 0027)
	cat    string
	weight int
}

var (
	routeCJSF103    = opsRoute{name: "cjsf-103", typ: lineHaul, src: []string{"SPK-GW"}, dest: []string{"SPK890103", "SPK890103 - ลาดกระบัง26"}, party: "BP_CJSF", cat: "PRIMARY"}
	routeCJSF103Sup = opsRoute{name: "cjsf-103-sup", typ: lineHaul, src: []string{"SPK-GW"}, dest: []string{"SPK890103"}, party: "BP_CJSF", cat: "SUPPLEMENTARY"}
	routeCJSF146    = opsRoute{name: "cjsf-146", typ: lineHaul, src: []string{"SPK-GW", "ศูนย์เกตเวย์บางปู"}, dest: []string{"SPK890146", "SPK890146 - ประเวศ18"}, party: "BP_CJSF", cat: "PRIMARY"}
	routeCJSF174    = opsRoute{name: "cjsf-174", typ: lineHaul, src: []string{"SPK-GW"}, dest: []string{"SPK890174", "SPK890174 - ห้วยขวาง10"}, party: "BP_CJSF", cat: "PRIMARY"}
	routeSPXSOCE    = opsRoute{name: "spx-soce", typ: firstMile, src: []string{"ALANG-A", "วังทองหลาง"}, dest: []string{"SOCE", "SOCE (บัวโรย)"}, cat: "PRIMARY"}
	routeSPXWTL     = opsRoute{name: "spx-wtl12", typ: firstMile, src: []string{"ALANG-A"}, dest: []string{"WANGTHONGLANG12"}, cat: "SUPPLEMENTARY"}
	routeSPK146     = opsRoute{name: "spk-146", typ: lineHaul, src: []string{"SPK-GW", "Bangpu Gateway"}, dest: []string{"SPK890146"}, cat: "PRIMARY"}
	routeTTPSOCE    = opsRoute{name: "ttp-soce", typ: firstMile, src: []string{"TTP-HUB1"}, dest: []string{"SOCE"}, party: "BP_TTP", cat: "PRIMARY"}
	// No rate card (until T43 adds rounds for the demo hubs): the "need a rate card" rows of the Income page.
	routeSPXDemo  = opsRoute{name: "spx-demo", typ: firstMile, cat: "PRIMARY"} // source and SOC drawn from spxDemoHubs, spxDemoSOCs
	routeTTPHub2  = opsRoute{name: "ttp-hub2", typ: firstMile, src: []string{"TTP-HUB2"}, dest: []string{"SOCN", "SOCN (วังน้อย)"}, party: "BP_TTP", cat: "PRIMARY"}
	routeCJSFBPL  = opsRoute{name: "cjsf-bpl", typ: firstMile, src: []string{"CJSF-BPL"}, dest: []string{"SOCE"}, party: "BP_CJSF", cat: "PRIMARY"}
	routeCJSFLKB  = opsRoute{name: "cjsf-lkb", typ: lineHaul, src: []string{"CJSF-LKB"}, dest: []string{"SPK890103"}, party: "BP_CJSF", cat: "PRIMARY"}
	spxDemoHubs   = []string{"BLANG-B", "HUB-B", "LATPR-A", "BUENG-A", "MINBU-A", "SAPHN-A", "PRAKN-A", "BKHAE-A", "DONMG-A", "NONTH-A"}
	spxDemoSOCs   = []string{"SOCN", "SOCE", "SOCW"}
	ocrOrigins    = []string{"ลาดพร้าว 3 (OCR)", "บางกะปิ-ซ.8 (OCR)", "ห้วยขวาง 1O (OCR)", "มีนบุร1 (OCR)", "สะพานสูง-B (OCR)"}
	cancelReasons = []string{"ลูกค้ายกเลิกรอบ", "ไม่มีสินค้า", "รถเสียก่อนออกเดินทาง", "เลื่อนรอบตามลูกค้า"}
	incidentNotes = map[string]string{
		"incident_cause_tire":       "ยางแตกระหว่างทาง รอเปลี่ยนยาง",
		"incident_cause_traffic":    "รถติดหนักบนถนนบางนา-ตราด",
		"incident_cause_accident":   "เฉี่ยวชนเล็กน้อย ไม่มีผู้บาดเจ็บ",
		"incident_cause_engine":     "เครื่องยนต์ร้อนเกิน จอดพักเครื่อง",
		"incident_cause_weather":    "ฝนตกหนัก น้ำท่วมขัง",
		"incident_cause_checkpoint": "ด่านตรวจ รอตรวจเอกสาร",
	}
)

// routeGroups are the routes a truck runs by tenant kind and vehicle class, with their weights.
var routeGroups = map[string][]opsRoute{
	"own/4WJ": {routeW(routeCJSF103, 22), routeW(routeCJSF103Sup, 14), routeW(routeCJSF146, 8), routeW(routeSPXSOCE, 20), routeW(routeSPXWTL, 12),
		routeW(routeSPK146, 10), routeW(routeSPXDemo, 8)},
	"own/6WH":     {routeW(routeCJSF174, 45), routeW(routeTTPSOCE, 40), routeW(routeTTPHub2, 6), routeW(routeCJSFBPL, 7)},
	"own/10WH":    {routeW(routeCJSFLKB, 50), routeW(routeSPXDemo, 50)},
	"carrier/4WJ": {routeW(routeSPXSOCE, 42), routeW(routeSPXWTL, 15), routeW(routeSPK146, 30), routeW(routeSPXDemo, 13)},
	"carrier/6WH": {routeW(routeTTPSOCE, 80), routeW(routeTTPHub2, 20)},
}

func routeW(r opsRoute, weight int) opsRoute { r.weight = weight; return r }

// boardFM is today's first-mile board (Appendix D §D.2 #1).
var boardFM = map[string]int{"pending": 6, "assigned": 8, "checked_in": 2, "in_transit": 3, "completed": 4, "cancelled": 2}

const (
	firstMile = "first_mile"
	lineHaul  = "line_haul"
)

// opsSpan is one truck a driver held: from its assignment's creation until it was revoked (zero: still held).
type opsSpan struct {
	truck    Row
	from, to time.Time
}

// opsDriver is a generated driver with a login and the trucks it held.
type opsDriver struct {
	key, id, user string // national ID (the natural key), drivers.id, users.id
	spans         []opsSpan
	queue         int // last run_order
	row           Row
}

// truckOn is the truck d held for the whole working day of day (05:00 to midnight), nil when none.
func (d *opsDriver) truckOn(day time.Time) Row {
	for _, s := range d.spans {
		if !s.from.After(day.Add(5*time.Hour)) && (s.to.IsZero() || !s.to.Before(day.AddDate(0, 0, 1))) {
			return s.truck
		}
	}
	return nil
}

// opsTrip is a generated trip the pricing pass reads back.
type opsTrip struct {
	trip, task Row
	stops      []Row // trip_delivery_stops
	legacy     bool  // stands for an ETL-migrated trip (snapshot computed_by etl)
}

// opsStandby is a generated standby record and its task, for the pricing pass.
type opsStandby struct {
	row, task Row
}

// opsGen holds the state of one run of the generator.
type opsGen struct {
	p       *Plan
	o       Options
	r       *rand.Rand
	s       Symbols
	start   time.Time // Bangkok midnight of window day 0
	hubs    map[string]Row
	users   map[string]string // e-mail -> users.id
	tenants map[string]string // tenant id -> code
	drivers []*opsDriver
	taskSeq map[string]int // task type + Bangkok day -> last sequence
	tripSeq map[string]int // Bangkok day -> last trip sequence
	trips   []*opsTrip
	standby []*opsStandby
	seal    int
	nTrip   int // trips generated so far: the position that decides a trip's variants
	nLegacy int
	nIncid  int
	labels  map[string]string
}

// addDemoOps appends the operations rows of the demo window (Appendix D §D.1.2): past days of delivered work with the
// variants of Appendix D §D.2 at fixed positions, then today's board. It runs after the master data and before the load
// generators, so the load profile carries the same rows; the prices come later (priceOperations).
func addDemoOps(p *Plan, o Options) error {
	g := &opsGen{p: p, o: o, r: rng(o, "demo-operations"), s: p.Symbols, hubs: map[string]Row{}, users: map[string]string{},
		tenants: map[string]string{}, taskSeq: map[string]int{}, tripSeq: map[string]int{}, labels: map[string]string{}}
	anchor := time.Date(o.Anchor.Year(), o.Anchor.Month(), o.Anchor.Day(), 0, 0, 0, 0, clock.Bangkok)
	g.start = anchor.AddDate(0, 0, -(opsDays - 1))
	for _, r := range p.Tables["hubs"] {
		g.hubs[r.Str("source_id")] = r
	}
	for _, r := range p.Tables["users"] {
		g.users[r.Str("email")] = r.Str("id")
	}
	for _, r := range p.Tables["tenants"] {
		g.tenants[r.Str("id")] = r.Str("code")
	}
	g.tenants[p.Symbols.MustID(ownFleetSymbol).String()] = "WRT"
	if err := g.collectDrivers(); err != nil {
		return err
	}
	g.collectSequences()
	switchDays := g.switchDays()
	for i := range opsDays - 1 {
		if err := g.pastDay(i, switchDays); err != nil {
			return err
		}
	}
	if err := g.today(); err != nil {
		return err
	}
	if p.labels == nil {
		p.labels = map[string]string{}
	}
	for k, v := range g.labels {
		p.labels[k] = v
	}
	p.ops = &opsResult{trips: g.trips, standby: g.standby}
	return nil
}

// collectDrivers lists the generated drivers that sign in (the smoke drivers keep their fixture work; a driver without
// the app never loads a trip) with the trucks they held, in plan order.
func (g *opsGen) collectDrivers() error {
	trucks := map[string]Row{}
	for _, r := range g.p.Tables["trucks"] {
		trucks[r.Str("id")] = r
	}
	users := map[string]bool{}
	for _, r := range g.p.Tables["users"] {
		users[r.Str("id")] = true
	}
	for _, r := range g.p.Tables["drivers"] {
		id := uuid.MustParse(r.Str("id"))
		if g.s.SymbolOf(id) != "" || r.Str("user_id") == "" || r.Str("status") == "inactive" {
			continue
		}
		if !users[r.Str("user_id")] {
			return fmt.Errorf("seed: driver %s names an unknown user", r.Str("id"))
		}
		d := &opsDriver{key: r.Str("id_card"), id: r.Str("id"), user: r.Str("user_id"), row: r}
		for _, a := range g.p.Tables["truck_assignments"] {
			if a.Str("driver_id") != d.id || (a.Str("status") != "active" && a.Str("status") != "revoked") {
				continue
			}
			from, err := time.Parse(time.RFC3339, a.Str("created_at"))
			if err != nil {
				return fmt.Errorf("seed: assignment %s: %w", a.Str("id"), err)
			}
			sp := opsSpan{truck: trucks[a.Str("truck_id")], from: from}
			if a.Str("status") == "revoked" {
				if sp.to, err = time.Parse(time.RFC3339, a.Str("revoked_at")); err != nil {
					return fmt.Errorf("seed: assignment %s: %w", a.Str("id"), err)
				}
			}
			d.spans = append(d.spans, sp)
		}
		if len(d.spans) > 0 {
			g.drivers = append(g.drivers, d)
		}
	}
	if len(g.drivers) == 0 {
		return fmt.Errorf("seed: the operations generator found no generated driver with a login and a truck")
	}
	return nil
}

// planTaskNoRx reads every task number of the plan, padded or not, so generated numbers never repeat one.
var planTaskNoRx = regexp.MustCompile(`^(FM|LH)-([0-9]{8})-([0-9]+)$`)

// collectSequences records the highest task sequence the plan holds per type and Bangkok day (padded or not).
func (g *opsGen) collectSequences() {
	for _, r := range g.p.Tables["tasks"] {
		if m := planTaskNoRx.FindStringSubmatch(r.Str("task_no")); m != nil {
			n, _ := strconv.Atoi(m[3])
			k := m[1] + m[2]
			g.taskSeq[k] = max(g.taskSeq[k], n)
		}
	}
}

// switchDays are the Bangkok days a CJSF fuel round starts on (non-voided announcements): the generator runs a 00:21
// ICT trip on each one inside the window, which prices under the new round (invariant 11's rule, Appendix D §D.2 #8).
func (g *opsGen) switchDays() map[string]bool {
	out := map[string]bool{}
	cjsf := g.s.MustID("BP_CJSF").String()
	for _, r := range g.p.Tables["customer_fuel_rate_adjustments"] {
		if r.Str("billing_party_id") != cjsf || r["voided"] == true {
			continue
		}
		if t, err := time.Parse(time.RFC3339, r.Str("effective_from_at")); err == nil {
			out[clock.DateString(t)] = true
		}
	}
	return out
}

// day is window day i (Bangkok midnight).
func (g *opsGen) day(i int) time.Time { return g.start.AddDate(0, 0, i) }

// available are the drivers holding a truck on day, grouped by the truck's tenant code, in plan order.
func (g *opsGen) available(day time.Time) map[string][]*opsDriver {
	out := map[string][]*opsDriver{}
	for _, d := range g.drivers {
		if tr := d.truckOn(day); tr != nil {
			code := g.tenants[tr.Str("tenant_id")]
			out[code] = append(out[code], d)
		}
	}
	return out
}

// --- past days ------------------------------------------------------------------------------------------------

// The positions of the variants of Appendix D §D.2 among the past days.
var (
	multiDays  = map[int]string{25: "flat", 40: "legacy", 70: "failed"} // the first trip of the first own 4WJ driver
	orphanDays = map[int]bool{20: true, 50: true, 80: true}             // the NWR trip runs on the orphan plate
	noCustomer = map[int]bool{33: true, 66: true}                       // an own driver's manual task from a hub without a party
)

// pastDay generates window day i: the own fleet's drivers run two or three trips each, every carrier one trip (Appendix
// D §D.2 #18), one task is cancelled, and the standby and incident positions add theirs.
func (g *opsGen) pastDay(i int, switchDays map[string]bool) error {
	day := g.day(i)
	av := g.available(day)
	for k, d := range av["WRT"] {
		tr := d.truckOn(day)
		at := day.Add(6*time.Hour + time.Duration(5*g.r.IntN(10))*time.Minute)
		if k == 0 && tr.Str("vehicle_class") == "4WJ" && switchDays[clock.DateString(day)] && i > 0 {
			next, _, err := g.trip(tripReq{d: d, truck: tr, route: routeCJSF103, plan: day.Add(21 * time.Minute), outcome: "delivered"})
			if err != nil {
				return err
			}
			at = maxTime(at, next)
		}
		n := 2
		if (i+k)%3 == 0 {
			n = 3
		}
		for j := range n {
			req := tripReq{d: d, truck: tr, plan: at, outcome: "delivered"}
			req.route = g.pick("own", tr)
			if j == 0 && k == 0 && tr.Str("vehicle_class") == "4WJ" && multiDays[i] != "" {
				req.multi = multiDays[i]
			}
			if j == n-1 && i%4 == 0 && k == len(av["WRT"])-1 {
				req.outcome = "cancelled" // loaded, then cancelled
			}
			if j == 0 && k == 1 && noCustomer[i] {
				req.route, req.manual = opsRoute{name: "no-customer", typ: firstMile, src: []string{"RAMIN-DRV"},
					dest: []string{"จุดส่งลูกค้าหน้างาน"}, cat: "PRIMARY"}, true
			}
			next, _, err := g.trip(req)
			if err != nil {
				return err
			}
			at = next.Add(time.Duration(20+5*g.r.IntN(8)) * time.Minute)
		}
		if err := g.maybeStandby(i, k, d, tr, at); err != nil {
			return err
		}
	}
	for _, code := range []string{"NWR", "TTP"} {
		list := av[code]
		if len(list) == 0 {
			continue
		}
		d := list[i%len(list)]
		tr := d.truckOn(day)
		req := tripReq{d: d, truck: tr, route: g.pick("carrier", tr), outcome: "delivered",
			plan: day.Add(7*time.Hour + time.Duration(5*g.r.IntN(12))*time.Minute)}
		if code == "NWR" && orphanDays[i] {
			req.truck, req.orphan = nil, tr
		}
		next, _, err := g.trip(req)
		if err != nil {
			return err
		}
		if code == "TTP" {
			if err := g.maybeStandby(i, 10, d, tr, next.Add(30*time.Minute)); err != nil {
				return err
			}
		}
	}
	if i >= 3 && i%4 != 0 {
		return g.cancelledTask(day, i)
	}
	return nil
}

// expand draws the source hub and SOC of a demo-hub route; other routes come back unchanged.
func (g *opsGen) expand(r opsRoute) opsRoute {
	if len(r.src) == 0 {
		r.src = []string{spxDemoHubs[g.r.IntN(len(spxDemoHubs))]}
		r.dest = []string{spxDemoSOCs[g.r.IntN(len(spxDemoSOCs))]}
	}
	return r
}

// pick draws a route of the truck's group.
func (g *opsGen) pick(kind string, truck Row) opsRoute {
	group := routeGroups[kind+"/"+truck.Str("vehicle_class")]
	if len(group) == 0 {
		group = []opsRoute{routeW(routeSPXDemo, 1)}
	}
	total := 0
	for _, r := range group {
		total += r.weight
	}
	n := g.r.IntN(total)
	for _, r := range group {
		if n < r.weight {
			return r
		}
		n -= r.weight
	}
	return group[0]
}

// tripReq is one trip to generate.
type tripReq struct {
	d       *opsDriver
	truck   Row // nil: the orphan plate
	orphan  Row // the truck the driver holds when it runs on the orphan plate (its tenant)
	route   opsRoute
	plan    time.Time
	outcome string // delivered | in_transit | incident | cancelled
	multi   string // flat | legacy | failed
	manual  bool
	today   bool
}

// trip appends a task and its trip (with stops, photos, check-in photos and an incident at the incident positions)
// and returns when the driver is free again.
func (g *opsGen) trip(q tripReq) (time.Time, *opsTrip, error) {
	g.nTrip++
	n := g.nTrip
	tenantTruck := q.truck
	if tenantTruck == nil {
		tenantTruck = q.orphan
	}
	tenant := tenantTruck.Str("tenant_id")
	route := g.expand(q.route)
	var stops []string
	switch q.multi {
	case "flat", "failed": // J&T with the SPK extra-stop fee: flat fee for stops 2+ (Appendix D §D.2 #3)
		route = opsRoute{name: "spk-multi", typ: lineHaul, src: []string{"SPK-GW"}, dest: []string{"SPK890146"}, party: "BP_SPK", cat: "PRIMARY"}
		stops = []string{"SPK890146", "SPK890103", "SPK890174"}
	case "legacy": // CJSF has no extra_stop fee: every stop priced hub -> stop
		route = opsRoute{name: "cjsf-multi", typ: lineHaul, src: []string{"SPK-GW"}, dest: []string{"SPK890103"}, party: "BP_CJSF", cat: "PRIMARY"}
		stops = []string{"SPK890103", "SPK890146"}
	}
	plain := q.outcome == "delivered" && !q.today && q.multi == "" && q.truck != nil && !q.manual
	legacy := plain && n%150 == 75 && g.nLegacy < 5 // five mobile tasks the ETL loaded (Appendix D §D.2 #2)
	manual := q.manual || legacy || (plain && route.typ == firstMile && n%12 == 5)
	task := g.task(taskReq{d: q.d, truck: q.truck, tenant: tenant, route: route, plan: q.plan, manual: manual,
		legacy: legacy, stops: stops, status: map[string]string{"delivered": "completed", "in_transit": "in_transit",
			"incident": "in_transit", "cancelled": "cancelled"}[q.outcome], today: q.today})

	checkIn := q.plan.Add(time.Duration(5+5*g.r.IntN(5)) * time.Minute)
	std := checkIn.Add(time.Duration(20+5*g.r.IntN(7)) * time.Minute)
	dur := time.Duration(60+5*g.r.IntN(13)) * time.Minute // first mile: 1 to 2 hours
	if route.typ == lineHaul {
		dur = time.Duration(120+5*g.r.IntN(25)) * time.Minute
	}
	if q.multi != "" {
		dur += 90 * time.Minute
	}
	delivered := std.Add(dur)
	task["check_in_at"] = ts(checkIn)

	date := clock.DateString(q.plan)
	g.tripSeq[date]++
	prefix := "ZXZB"
	if route.typ == lineHaul {
		prefix = "ZXJB"
	}
	tripNo := prefix + q.plan.Format("060102") + fmt.Sprintf("2%04d", g.tripSeq[date])
	tripID := g.id("trip_records", tripNo)
	g.labels[tripID] = tripNo
	g.seal++
	src, dst := g.hubs[route.src[0]], g.hubs[route.dest[0]]
	trip := Row{"id": tripID, "tenant_id": tenant, "tenant_source": "task", "trip_no": tripNo, "status": q.outcome,
		"job_type": route.typ, "task_id": task.Str("id"), "driver_id": q.d.id, "origin_raw": task.Str("source_hub_raw"),
		"destination_raw": task.Str("destination_raw"), "seal_code": fmt.Sprintf("SPXS2%09d", g.seal), "std": ts(std),
		"parcel_count": intNum(20 + g.r.IntN(380)), "created_at": ts(std)}
	if c := task.Str("job_category"); c != "" {
		trip["job_category"] = c
	}
	if src != nil {
		trip["origin_hub_id"] = src.Str("id")
		if lat, ok := floatOf(src["latitude"]); ok {
			lng, _ := floatOf(src["longitude"])
			trip["loading_lat"], trip["loading_lng"] = num(round4(lat)), num(round4(lng))
		}
	}
	if n%78 == 40 && src != nil { // ten trips carry an OCR origin no hub resolves (Appendix D §D.2 #26)
		trip["origin_raw"] = ocrOrigins[(n/78)%len(ocrOrigins)]
		delete(trip, "origin_hub_id")
	}
	if dst != nil {
		trip["destination_hub_id"] = dst.Str("id")
		if hubs.NormalizeStationType(dst.Str("station_type")) == hubs.StationSOC {
			trip["destination_soc_key"] = hubs.NormalizeSocKey(dst.Str("source_id"))
		}
	}
	if q.truck != nil {
		trip["truck_id"], trip["truck_license_plate_snapshot"] = q.truck.Str("id"), q.truck.Str("license_plate")
		trip["truck_type_raw"], trip["vehicle_class"] = q.truck.Str("vehicle_class"), q.truck.Str("vehicle_class")
	} else {
		trip["truck_license_plate_snapshot"] = orphanPlate
	}
	if len(stops) > 0 {
		trip["is_multi_delivery"] = true
	}
	if legacy {
		trip["legacy_doc_id"], trip["legacy_task_ref"], trip["task_ref_match"] = tripNo, task.Str("legacy_doc_id"), "doc_id"
		trip["legacy_driver_ref"], trip["driver_ref_match"] = legacyUID(q.d.key), "auth_uid"
	}
	switch q.outcome {
	case "delivered":
		trip["delivered_at"], trip["delivered_via"] = ts(delivered), "mobile"
		if n%50 == 17 {
			trip["delivered_via"] = "admin_web"
		}
		if dst != nil {
			if lat, ok := floatOf(dst["latitude"]); ok {
				lng, _ := floatOf(dst["longitude"])
				trip["delivered_lat"], trip["delivered_lng"] = num(round4(lat+0.0004)), num(round4(lng-0.0003))
			}
		}
		if n%2 == 0 {
			trip["evidence_token"] = "seedEvidence" + tripNo
			trip["line_delivered_notified_at"] = ts(delivered.Add(time.Minute))
		}
	case "cancelled":
		task["cancelled_at"], task["cancel_reason"] = ts(std.Add(25*time.Minute)), cancelReasons[n%len(cancelReasons)]
	}
	g.add("trip_records", trip)
	ot := &opsTrip{trip: trip, task: task, legacy: legacy}
	g.trips = append(g.trips, ot)

	g.stops(ot, stops, q.multi, std, delivered, q.outcome)
	if err := g.photos(ot, q, std, delivered); err != nil {
		return time.Time{}, nil, err
	}
	if err := g.checkInPhotos(task, q.d, route.src[0], q.plan, checkIn); err != nil {
		return time.Time{}, nil, err
	}
	if q.outcome == "incident" || (q.outcome == "delivered" && !q.today && g.incidentAt(n)) {
		if err := g.incident(ot, q, std, delivered); err != nil {
			return time.Time{}, nil, err
		}
	}
	if q.outcome == "in_transit" || q.outcome == "incident" {
		return std, ot, nil
	}
	if q.outcome == "cancelled" {
		return std.Add(40 * time.Minute), ot, nil
	}
	return delivered, ot, nil
}

// incidentAt are the past trips whose driver reported an incident in transit (the trip still delivered): six
// driver-reported, two the office reported and one reported after delivery (with today's two, eleven beside the
// smoke IR1, Appendix D §D.2 #5).
func (g *opsGen) incidentAt(n int) bool {
	return n%71 == 30 && g.nIncid < 9
}

// taskReq is one task to generate.
type taskReq struct {
	d       *opsDriver // nil: not assigned
	truck   Row
	tenant  string // tenants.id: the truck's tenant, or the tenant of a task nobody holds
	route   opsRoute
	plan    time.Time
	manual  bool
	legacy  bool
	stops   []string
	status  string
	today   bool
	creator string // users.id of the staff who created it ("" = the tenant's operations staff)
	class   string // the vehicle class of a task without a truck
}

// task appends a task (and its planned stops). Staff tasks are created the evening before their plan day by the
// tenant's operations staff (tenant_source form); a driver's manual task is created at check-in (tenant_source driver,
// client_op_id, R63); the five legacy ones stand for mobile tasks the ETL loaded: an unpadded number, a Firestore doc
// id, the driver's auth uid and no job category (ADR 0010).
func (g *opsGen) task(q taskReq) Row {
	route := g.expand(q.route)
	prefix := "FM"
	if route.typ == lineHaul {
		prefix = "LH"
	}
	dayKey := prefix + q.plan.Format("02012006")
	g.taskSeq[dayKey]++
	seq := g.taskSeq[dayKey]
	no := fmt.Sprintf("%s-%s-%03d", prefix, q.plan.Format("02012006"), seq)
	natural := no
	tenant := q.tenant
	srcRaw, destRaw := route.src[0], route.dest[0]
	if len(route.src) > 1 && g.r.IntN(5) == 0 {
		srcRaw = route.src[1+g.r.IntN(len(route.src)-1)]
	}
	if len(route.dest) > 1 && g.r.IntN(3) == 0 {
		destRaw = route.dest[1+g.r.IntN(len(route.dest)-1)]
	}
	r := Row{"tenant_id": tenant, "tenant_source": "form", "task_type": route.typ, "status": q.status,
		"plan_at": ts(q.plan), "plan_time": q.plan.Format("15:04"), "source_hub_raw": srcRaw, "destination_raw": destRaw}
	if q.legacy {
		g.nLegacy++
		doc := legacyDocID("TaskDoc", fmt.Sprintf("G%03d", g.nLegacy))
		no = fmt.Sprintf("%s-%s-%d", prefix, q.plan.Format("02012006"), seq)
		natural = "legacy:" + doc
		r["legacy_doc_id"] = doc
	} else {
		r["job_category"] = route.cat
	}
	r["task_no"] = no
	id := g.id("tasks", natural)
	r["id"] = id
	g.labels[id] = no
	if h := g.hubs[route.src[0]]; h != nil {
		r["source_hub_id"] = h.Str("id")
		if p := h.Str("linked_party_id"); p != "" {
			r["source_linked_party_id"] = p
		}
	}
	if h := g.hubs[route.dest[0]]; h != nil {
		r["destination_hub_id"] = h.Str("id")
		if p := h.Str("linked_party_id"); p != "" {
			r["destination_linked_party_id"] = p
		}
		if hubs.NormalizeStationType(h.Str("station_type")) == hubs.StationSOC {
			r["destination_soc_key"] = hubs.NormalizeSocKey(h.Str("source_id"))
		}
	}
	if route.party != "" {
		r["billing_party_id"] = g.s.MustID(route.party).String()
	}
	switch {
	case q.truck != nil:
		r["truck_id"], r["truck_type"], r["license_plate_snapshot"] = q.truck.Str("id"), q.truck.Str("vehicle_class"), q.truck.Str("license_plate")
	case q.d != nil: // the orphan plate: a rented truck the fleet does not know, no class (R15)
		r["license_plate_snapshot"] = orphanPlate
	case q.class != "":
		r["truck_type"] = q.class
	}
	created := time.Date(q.plan.Year(), q.plan.Month(), q.plan.Day(), 16, 0, 0, 0, clock.Bangkok).AddDate(0, 0, -1).
		Add(time.Duration(g.r.IntN(150)) * time.Minute)
	if q.today && q.status == "pending" {
		created = q.plan.Add(-time.Duration(60+g.r.IntN(120)) * time.Minute)
	}
	creator := q.creator
	if creator == "" {
		creator = g.staffOf(tenant)
	}
	if q.d != nil {
		r["driver_id"] = q.d.id
		q.d.queue++
		r["run_order"] = intNum(q.d.queue)
		if q.legacy {
			r["legacy_driver_ref"], r["driver_ref_match"] = legacyUID(q.d.key), "auth_uid"
		}
	}
	if q.manual {
		created = q.plan.Add(10 * time.Minute)
		r["tenant_source"] = "driver"
		if !q.legacy {
			r["created_by"] = q.d.user
			r["client_op_id"] = ClientOpID(g.o.Namespace, "tasks:"+natural).String()
		}
	} else {
		r["created_by"] = creator
	}
	r["created_at"] = ts(created)
	if len(q.stops) > 0 {
		r["is_multi_delivery"] = true
		for i, dst := range q.stops {
			st := Row{"id": g.id("task_delivery_stops", fmt.Sprintf("%s:%d", natural, i+1)), "task_id": id,
				"stop_index": intNum(i + 1), "destination_raw": dst, "sequence": intNum(i), "status": "pending"}
			if h := g.hubs[dst]; h != nil {
				st["destination_hub_id"] = h.Str("id")
				if p := h.Str("linked_party_id"); p != "" {
					st["destination_linked_party_id"] = p
				}
			}
			g.add("task_delivery_stops", st)
		}
	}
	g.add("tasks", r)
	return r
}

// staffOf is the operations user who creates a tenant's tasks.
func (g *opsGen) staffOf(tenant string) string {
	switch g.tenants[tenant] {
	case "NWR":
		return g.users["nwr.ops@logitrack.test"]
	case "TTP":
		return g.users["ttp.ops@logitrack.test"]
	}
	return g.s.MustID("U_WRT_OPS").String()
}

// legacyUID is a synthetic 28-character Firebase auth uid of a driver (SEEDUID…, Appendix D §D.1.3).
func legacyUID(nid string) string {
	return "SEEDUID" + strings.Repeat("0", 28-7-len(nid)) + nid
}

// stops appends the delivery progress of a multi-drop trip in completion order (not plan order) and moves the task's
// planned stops along: the failed-stop trip leaves its middle stop failed and still delivers two (the engine needs
// two delivered stops to price a gated multi-drop trip).
func (g *opsGen) stops(ot *opsTrip, stops []string, mode string, std, delivered time.Time, outcome string) {
	if len(stops) == 0 {
		return
	}
	order := []int{1, 0, 2} // completion order: the second planned stop first
	if len(stops) == 2 {
		order = []int{0, 1}
	}
	taskStops := g.p.Tables["task_delivery_stops"][len(g.p.Tables["task_delivery_stops"])-len(stops):]
	step := delivered.Sub(std) / time.Duration(len(stops))
	seq := 0
	for k, idx := range order {
		at := std.Add(step * time.Duration(k+1))
		if k == len(order)-1 {
			at = delivered
		}
		status := "delivered"
		if mode == "failed" && idx == 2 {
			status = "failed"
		}
		if outcome != "delivered" {
			status = "pending"
		}
		st := Row{"id": g.id("trip_delivery_stops", fmt.Sprintf("%s:%d", ot.trip.Str("trip_no"), idx+1)),
			"trip_id": ot.trip.Str("id"), "stop_index": intNum(idx + 1), "destination_raw": stops[idx], "status": status}
		if h := g.hubs[stops[idx]]; h != nil {
			st["destination_hub_id"] = h.Str("id")
		}
		if status == "delivered" {
			seq++
			st["delivered_at"], st["completed_seq"] = ts(at), intNum(seq)
			taskStops[idx]["delivered_at"] = ts(at)
		}
		taskStops[idx]["status"] = status
		ot.stops = append(ot.stops, st)
		g.add("trip_delivery_stops", st)
	}
	if mode == "failed" { // the failed stop was given up at the end of the run
		ot.trip["delivered_at"] = ts(delivered.Add(-step))
	}
}

// photos appends the trip photos of a trip and their objects by outcome: loading (runsheet, pre_close on every third
// trip, closing, seal; a trip cancelled after loading keeps its runsheet only), then delivery (arrived, opening on every
// fourth); a multi-drop trip takes stop_{n}_arrived per delivered stop instead, linked to the stop.
func (g *opsGen) photos(ot *opsTrip, q tripReq, std, delivered time.Time) error {
	n := g.nTrip
	type shot struct {
		typ  string
		at   time.Time
		stop Row
	}
	shots := []shot{{"runsheet", std.Add(-28 * time.Minute), nil}}
	if q.outcome != "cancelled" {
		if n%3 == 0 {
			shots = append(shots, shot{"pre_close", std.Add(-18 * time.Minute), nil})
		}
		shots = append(shots, shot{"closing", std.Add(-11 * time.Minute), nil}, shot{"seal", std.Add(-4 * time.Minute), nil})
	}
	if q.outcome == "delivered" {
		if len(ot.stops) == 0 {
			shots = append(shots, shot{"arrived", delivered.Add(-14 * time.Minute), nil})
			if n%4 == 0 {
				shots = append(shots, shot{"opening", delivered.Add(-6 * time.Minute), nil})
			}
		}
		for _, st := range ot.stops {
			if st.Str("status") != "delivered" {
				continue
			}
			at, _ := time.Parse(time.RFC3339, st.Str("delivered_at"))
			idx, _ := strconv.Atoi(fmt.Sprint(st["stop_index"]))
			shots = append(shots, shot{fmt.Sprintf("stop_%d_arrived", idx), at.Add(-9 * time.Minute), st})
		}
	}
	tripID := uuid.MustParse(ot.trip.Str("id"))
	for pos, sh := range shots {
		fid, err := g.object("trip_photo", tripID, sh.typ, sh.at, ot.trip.Str("tenant_id"), q.d.user)
		if err != nil {
			return err
		}
		ph := Row{"id": g.id("trip_photos", ot.trip.Str("trip_no")+":"+sh.typ), "trip_id": tripID.String(),
			"photo_type": sh.typ, "file_id": fid, "position": intNum(pos), "created_at": ts(sh.at)}
		if sh.stop != nil {
			ph["stop_id"] = sh.stop.Str("id")
		}
		if lat, ok := floatOf(ot.trip["loading_lat"]); ok && pos < 4 && n%5 == 0 {
			lng, _ := floatOf(ot.trip["loading_lng"])
			ph["geocode_lat"], ph["geocode_lng"] = num(lat), num(lng)
		}
		g.add("trip_photos", ph)
	}
	return nil
}

// checkInPhotos attach the check-in photo (and, every other task, the app screenshot of ADR 0019) to the tasks of the
// last two weeks: the driver app requires them since the check-in flow moved to the API; older tasks keep none, as
// the ETL finds them.
func (g *opsGen) checkInPhotos(task Row, d *opsDriver, srcHub string, plan, at time.Time) error {
	if plan.Before(g.day(opsDays - 14)) {
		return nil
	}
	id := uuid.MustParse(task.Str("id"))
	fid, err := g.object("checkin_photo", id, "", at.Add(-time.Minute), task.Str("tenant_id"), d.user)
	if err != nil {
		return err
	}
	task["check_in_photo_file_id"] = fid
	if h := g.hubs[srcHub]; h != nil {
		if lat, ok := floatOf(h["latitude"]); ok {
			lng, _ := floatOf(h["longitude"])
			task["check_in_lat"], task["check_in_lng"] = num(round4(lat+0.0002)), num(round4(lng+0.0002))
		}
	}
	if g.nTrip%2 == 0 {
		fid, err := g.object("checkin_app_screenshot", id, "", at, task.Str("tenant_id"), d.user)
		if err != nil {
			return err
		}
		task["check_in_app_screenshot_file_id"] = fid
	}
	return nil
}

// object appends a committed placeholder object of a purpose (key from the internal/storage template of main spec
// §9.2: owner uuid, variant, millisecond stamp of the upload) and returns its id.
func (g *opsGen) object(purpose string, owner uuid.UUID, variant string, at time.Time, tenant, by string) (string, error) {
	p, ok := storage.Purposes[purpose]
	if !ok {
		return "", fmt.Errorf("seed: unknown purpose %s", purpose)
	}
	key, err := p.Key(storage.KeyInput{EntityID: owner, Variant: variant, ContentType: "image/jpeg", Now: at})
	if err != nil {
		return "", fmt.Errorf("seed: %s key: %w", purpose, err)
	}
	id := g.id("file_objects", "logitrack:"+key)
	r := Row{"id": id, "bucket": "logitrack", "object_key": key, "content_type": "image/jpeg",
		"size_bytes": "$seed:generated", "sha256": "$seed:generated", "purpose": purpose, "owner_kind": p.OwnerKind,
		"owner_id": owner.String(), "tenant_id": tenant, "status": "committed", "created_at": ts(at)}
	if by != "" {
		r["uploaded_by"] = by
	}
	g.add("file_objects", r)
	return id, nil
}

// incident appends an incident report on a trip: by the driver in transit (client_op_id, R63), by the office (two),
// or after delivery (one: the LINE delay note). Today's in-transit incidents leave the trip in status incident.
func (g *opsGen) incident(ot *opsTrip, q tripReq, std, delivered time.Time) error {
	g.nIncid++
	at := std.Add(delivered.Sub(std) / 2).Truncate(time.Minute)
	kind, by := "driver", q.d.user
	switch {
	case q.today:
		at = std.Add(35 * time.Minute)
	case g.nIncid == 5: // reported after delivery: the LINE delay note
		at = delivered.Add(50 * time.Minute)
	case g.nIncid == 2 || g.nIncid == 7: // the office reported it
		kind, by = "admin", g.staffOf(ot.trip.Str("tenant_id"))
	}
	causes := []string{"incident_cause_tire", "incident_cause_traffic", "incident_cause_accident", "incident_cause_engine",
		"incident_cause_weather", "incident_cause_checkpoint"}
	cause := causes[g.nIncid%len(causes)]
	natural := ot.trip.Str("trip_no") + ":" + at.Format("2006-01-02T15:04")
	id := g.id("incident_reports", natural)
	g.labels[id] = ot.trip.Str("trip_no")
	r := Row{"id": id, "tenant_id": ot.trip.Str("tenant_id"), "tenant_source": "trip", "driver_id": q.d.id,
		"trip_id": ot.trip.Str("id"), "delay_cause": cause, "description": incidentNotes[cause], "reported_by_kind": kind,
		"reported_by_user_id": by, "created_at": ts(at)}
	if lat, ok := floatOf(ot.trip["loading_lat"]); ok {
		lng, _ := floatOf(ot.trip["loading_lng"])
		r["lat"], r["lng"] = num(round4(lat-0.021)), num(round4(lng+0.017))
	}
	if q.truck != nil {
		r["truck_id"], r["truck_license_plate_snapshot"] = q.truck.Str("id"), q.truck.Str("license_plate")
	} else {
		r["truck_license_plate_snapshot"] = orphanPlate
	}
	if kind == "driver" {
		r["client_op_id"] = ClientOpID(g.o.Namespace, "incident_reports:"+natural).String()
	}
	owner := uuid.MustParse(id)
	fid, err := g.object("incident_photo", owner, "situation1", at.Add(-time.Minute), r.Str("tenant_id"), by)
	if err != nil {
		return err
	}
	r["situation1_photo_file_id"] = fid
	if g.nIncid%2 == 1 {
		if r["situation2_photo_file_id"], err = g.object("incident_photo", owner, "situation2", at.Add(-30*time.Second), r.Str("tenant_id"), by); err != nil {
			return err
		}
	}
	if g.nIncid%3 == 0 {
		if r["map_photo_file_id"], err = g.object("incident_photo", owner, "map", at.Add(-90*time.Second), r.Str("tenant_id"), by); err != nil {
			return err
		}
	}
	g.add("incident_reports", r)
	return nil
}

// cancelledTask appends a task the office cancelled before anyone was assigned: no driver, no trip.
func (g *opsGen) cancelledTask(day time.Time, i int) error {
	own := g.s.MustID(ownFleetSymbol).String()
	route := []opsRoute{routeSPXSOCE, routeCJSF103, routeSPXDemo, routeCJSF174}[i%4]
	if route.name == routeSPXDemo.name {
		route.src, route.dest = []string{spxDemoHubs[i%len(spxDemoHubs)]}, []string{spxDemoSOCs[i%len(spxDemoSOCs)]}
	}
	class := "4WJ"
	if route.name == routeCJSF174.name {
		class = "6WH"
	}
	plan := day.Add(13*time.Hour + time.Duration(5*g.r.IntN(24))*time.Minute)
	t := g.task(taskReq{tenant: own, route: route, plan: plan, status: "cancelled", class: class})
	t["cancelled_at"], t["cancel_reason"] = ts(plan.Add(-3*time.Hour)), cancelReasons[i%len(cancelReasons)]
	return nil
}

// --- standby ------------------------------------------------------------------------------------------------

// standbyPlan are the standby events of the window (Appendix D §D.2 #4, 21 beside the four of smoke): who waited for
// whom, keyed by the window day and the driver's position (0-2 own fleet, 10 the TTP driver of the day).
type standbySpec struct {
	customer string // party symbol; "" = unresolved
	at       string // the hub the driver waited at
	legacy   bool   // ETL-migrated, no ended_at
}

var standbyPlan = map[[2]int]standbySpec{
	{5, 0}: {"BP_CJSF", "CJSF-BPL", false}, {9, 1}: {"BP_CJSF", "CJSF-LKB", false}, {14, 0}: {"BP_CJSF", "CJSF-SMP", false},
	{18, 1}: {"BP_CJSF", "CJSF-BPL", false}, {23, 2}: {"BP_CJSF", "CJSF-LKB", false}, {27, 0}: {"BP_CJSF", "CJSF-BPL", false},
	{31, 1}: {"BP_CJSF", "CJSF-SMP", false}, {38, 2}: {"BP_CJSF", "CJSF-BPL", false}, {44, 0}: {"BP_CJSF", "CJSF-LKB", false},
	{52, 1}: {"BP_CJSF", "CJSF-BPL", false}, {61, 2}: {"BP_CJSF", "CJSF-SMP", false}, {72, 0}: {"BP_CJSF", "CJSF-BPL", false},
	{84, 1}:  {"BP_CJSF", "CJSF-LKB", false},
	{12, 10}: {"BP_TTP", "TTP-HUB1", false}, {49, 10}: {"BP_TTP", "TTP-HUB2", false}, {77, 10}: {"BP_TTP", "TTP-HUB1", false},
	{16, 2}: {"", "RAMIN-DRV", false}, {41, 0}: {"", "RAMIN-DRV", false}, {69, 1}: {"", "RAMIN-DRV", false},
	{57, 0}: {"BP_SPX", "BLANG-B", false},
	{8, 1}:  {"BP_CJSF", "CJSF-BPL", true},
}

// maybeStandby appends the standby record of (day i, driver position k) when the plan has one: the driver waits at a
// hub after its last trip, the customer resolved from the hub's link (R19: never from the task's billing party).
func (g *opsGen) maybeStandby(i, k int, d *opsDriver, truck Row, from time.Time) error {
	sp, ok := standbyPlan[[2]int{i, k}]
	if !ok {
		return nil
	}
	started := from.Truncate(5 * time.Minute)
	ended := started.Add(time.Duration(120+15*g.r.IntN(25)) * time.Minute)
	natural := d.key + ":" + started.Format("2006-01-02T15:04")
	id := g.id("standby_records", natural)
	r := Row{"id": id, "tenant_id": truck.Str("tenant_id"), "tenant_source": "driver", "driver_id": d.id,
		"start_location_raw": sp.at, "end_location_raw": sp.at, "started_at": ts(started), "status": "completed",
		"truck_id": truck.Str("id"), "truck_license_plate_snapshot": truck.Str("license_plate"), "created_at": ts(started)}
	if h := g.hubs[sp.at]; h != nil {
		if lat, ok := floatOf(h["latitude"]); ok {
			lng, _ := floatOf(h["longitude"])
			r["lat"], r["lng"] = num(round4(lat)), num(round4(lng))
		}
	}
	if sp.customer != "" {
		r["customer_party_id"], r["customer_resolved"], r["customer_resolved_from"] = g.s.MustID(sp.customer).String(), true, "origin_hub"
	}
	if sp.legacy { // the legacy app never closed it: no ended_at, so no billing axis (R19)
		r["legacy_doc_id"] = legacyDocID("StbDoc", fmt.Sprintf("G%02d", i))
		r["legacy_driver_ref"], r["driver_ref_match"] = legacyUID(d.key), "auth_uid"
	} else {
		r["ended_at"], r["duration_minutes"] = ts(ended), intNum(int(ended.Sub(started).Minutes()))
		r["client_op_id"] = ClientOpID(g.o.Namespace, "standby_records:"+natural).String()
		r["note"] = "รองานที่ " + sp.at
	}
	g.add("standby_records", r)
	g.standby = append(g.standby, &opsStandby{row: r})
	return nil
}

// --- today ----------------------------------------------------------------------------------------------------

// today generates the board of the anchor day (Appendix D §D.2 #1): first-mile tasks pending 6, assigned 8, checked in
// 2, in transit 3 (one trip in transit, two stopped by an incident), completed 4, cancelled 2, counting the smoke tasks
// that fall on that day; plus a line-haul multi-drop task assigned with its stops pending. The drivers with a task under way point at it
// (drivers.active_task_id, back-filled after the load, Appendix D §D.1.5).
func (g *opsGen) today() error {
	day := g.day(opsDays - 1)
	av := g.available(day)
	// The board counts every first-mile task of the day: the smoke call-out TK22 (pending) and check-in TK24 sit on
	// the default anchor, so the generator fills up to the board instead of adding it whole.
	have := map[string]int{}
	for _, r := range g.p.Tables["tasks"] {
		if r.Str("task_type") == firstMile && strings.HasPrefix(r.Str("plan_at"), clock.DateString(day)) {
			have[r.Str("status")]++
		}
	}
	want := func(status string) int { return max(0, boardFM[status]-have[status]) }
	pickFM := func(tr Row, kind string) opsRoute {
		for range 50 {
			if r := g.pick(kind, tr); r.typ == firstMile {
				return r
			}
		}
		return routeSPXDemo
	}
	all := append(append(append([]*opsDriver{}, av["WRT"]...), av["NWR"]...), av["TTP"]...)
	kindOf := func(d *opsDriver) string {
		if g.tenants[d.truckOn(day).Str("tenant_id")] == "WRT" {
			return "own"
		}
		return "carrier"
	}
	free := map[*opsDriver]time.Time{}
	count := map[string]int{}
	run := func(d *opsDriver, outcome string) error {
		tr := d.truckOn(day)
		at := free[d]
		if at.IsZero() {
			at = day.Add(6*time.Hour + time.Duration(5*g.r.IntN(6))*time.Minute)
		}
		next, ot, err := g.trip(tripReq{d: d, truck: tr, route: pickFM(tr, kindOf(d)), plan: at, outcome: outcome, today: true})
		if err != nil {
			return err
		}
		free[d] = next.Add(20 * time.Minute)
		count[map[string]string{"delivered": "completed", "in_transit": "in_transit", "incident": "in_transit"}[outcome]]++
		if outcome != "delivered" {
			d.row["active_task_id"] = ot.task.Str("id")
		}
		return nil
	}
	// Completed in the morning: one per tenant's first driver, then the own fleet's second.
	var morning []*opsDriver
	for _, code := range []string{"WRT", "NWR", "TTP"} {
		if len(av[code]) > 0 {
			morning = append(morning, av[code][0])
		}
	}
	if len(av["WRT"]) > 1 {
		morning = append(morning, av["WRT"][1])
	}
	for _, d := range morning {
		if count["completed"] < want("completed") {
			if err := run(d, "delivered"); err != nil {
				return err
			}
		}
	}
	// Under way: the first in transit, the next two stopped by an incident; then two checked in.
	busy := map[*opsDriver]bool{}
	for _, outcome := range []string{"in_transit", "incident", "incident"}[:min(3, want("in_transit"))] {
		for _, d := range all {
			if !busy[d] && free[d] != (time.Time{}) {
				busy[d] = true
				if err := run(d, outcome); err != nil {
					return err
				}
				break
			}
		}
	}
	for _, d := range all {
		if count["checked_in"] >= want("checked_in") {
			break
		}
		if busy[d] {
			continue
		}
		busy[d] = true
		tr := d.truckOn(day)
		at := maxTime(free[d], day.Add(9*time.Hour+50*time.Minute))
		route := pickFM(tr, kindOf(d))
		t := g.task(taskReq{d: d, truck: tr, tenant: tr.Str("tenant_id"), route: route, plan: at, status: "checked_in", today: true})
		checkIn := at.Add(15 * time.Minute)
		t["check_in_at"] = ts(checkIn)
		if err := g.checkInPhotos(t, d, route.src[0], at, checkIn); err != nil {
			return err
		}
		d.row["active_task_id"] = t.Str("id")
		free[d] = checkIn
		count["checked_in"]++
	}
	// Assigned for the afternoon, round-robin over the drivers, queued after their current work.
	for k := range want("assigned") {
		d := all[k%len(all)]
		tr := d.truckOn(day)
		at := maxTime(free[d], day.Add(11*time.Hour)).Add(time.Duration(90+30*(k/len(all))) * time.Minute).Truncate(5 * time.Minute)
		g.task(taskReq{d: d, truck: tr, tenant: tr.Str("tenant_id"), route: pickFM(tr, kindOf(d)), plan: at, status: "assigned", today: true})
	}
	if len(av["WRT"]) > 0 { // the multi-drop line haul of tomorrow morning's run, stops still pending
		d := av["WRT"][0]
		tr := d.truckOn(day)
		g.task(taskReq{d: d, truck: tr, tenant: tr.Str("tenant_id"), plan: day.Add(17 * time.Hour), status: "assigned", today: true,
			route: opsRoute{name: "spk-multi", typ: lineHaul, src: []string{"SPK-GW"}, dest: []string{"SPK890146"}, party: "BP_SPK", cat: "PRIMARY"},
			stops: []string{"SPK890146", "SPK890103"}})
	}
	// Pending: call-outs nobody holds yet; two from the TTP dispatcher, one from the own fleet's dispatcher.
	own, ttp := g.s.MustID(ownFleetSymbol).String(), g.s.MustID("TN_TTP").String()
	pending := []struct {
		tenant, by, class string
		route             opsRoute
	}{
		{ttp, g.s.MustID("U_TTP_DISP").String(), "6WH", routeTTPSOCE}, {ttp, g.s.MustID("U_TTP_DISP").String(), "6WH", routeTTPHub2},
		{own, g.users["wrt.dispatch@logitrack.test"], "4WJ", routeSPXSOCE}, {own, "", "4WJ", routeSPXWTL},
		{own, "", "6WH", routeCJSFBPL}, {own, "", "4WJ", routeSPXSOCE},
	}
	for k, pd := range pending[:min(len(pending), want("pending"))] {
		g.task(taskReq{tenant: pd.tenant, creator: pd.by, class: pd.class, route: pd.route, status: "pending", today: true,
			plan: day.Add(time.Duration(12*60+45*k) * time.Minute)})
	}
	// Cancelled: one before anyone held it, one after it was assigned.
	if want("cancelled") > 0 {
		c1 := g.task(taskReq{tenant: own, class: "4WJ", route: routeSPXSOCE, status: "cancelled", today: true, plan: day.Add(14 * time.Hour)})
		c1["cancelled_at"], c1["cancel_reason"] = ts(day.Add(8*time.Hour)), cancelReasons[0]
	}
	if len(all) > 0 && want("cancelled") > 1 {
		d := all[len(all)-1]
		tr := d.truckOn(day)
		c2 := g.task(taskReq{d: d, truck: tr, tenant: tr.Str("tenant_id"), route: pickFM(tr, kindOf(d)), status: "cancelled",
			today: true, plan: day.Add(15 * time.Hour)})
		c2["cancelled_at"], c2["cancel_reason"] = ts(day.Add(9*time.Hour+30*time.Minute)), cancelReasons[1]
	}
	return nil
}

func (g *opsGen) add(table string, r Row) { g.p.Tables[table] = append(g.p.Tables[table], r) }

func (g *opsGen) id(table, key string) string { return ID(g.o.Namespace, table, key).String() }

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// opsTables are the tables the operations generator writes (their demo counts are exact in Appendix D §D.1.2).
var opsTables = []string{"tasks", "task_delivery_stops", "trip_records", "trip_delivery_stops", "trip_photos",
	"trip_billing_snapshots", "standby_records", "incident_reports", "file_objects"}
