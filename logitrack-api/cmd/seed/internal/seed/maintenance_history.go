package seed

import (
	"container/heap"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The maintenance history the seeded expenses and records imply (T43, Appendix D §D.2 #15). Approving a fuel log with
// an odometer runs maintenance.CheckPMInTx (internal/expenses, on a staff Create and on an approval): the truck takes the
// reading as its current mileage, or, once the reading is within maintenance.PMAlertThresholdKm of the next service
// and no PM booking or scheduled visit is open, a pm_booking record becomes the truck's active maintenance. A record
// opened or closed moves the truck's active maintenance, shop status, last service date, mileage and next service
// (maintenance.applyToTruck). fleetHistory replays both, per truck in time order, over the rows a generator writes:
// the PM bookings it adds are the ones the check would book (serviced and completed two days later, the service's
// mileage and next service on the record, when that is before the anchor), and the truck rows end as the services leave them, so the
// seeded fleet is a state the API could have produced. A generator registers the trucks, the approved fuel logs and its
// own records, then runs it once.

// fleetRecord is a maintenance record of the replay: its row (the final state) and its status as the replay goes.
type fleetRecord struct {
	row    Row
	status string // "" until it is opened
}

// fleetTruck is a truck's maintenance state as the replay goes.
type fleetTruck struct {
	row         Row
	status      string
	current     int
	next        int // 0: no next service mileage
	lastAlert   int // 0: none (checkPM reads Number(lastAlertMileage) || 0)
	active      *fleetRecord
	lastService string
	interval    int
	records     []*fleetRecord
	updated     time.Time // the last change (trucks.updated_at)
	mileageOn   func(day time.Time) int
	approver    string // the staff user who audits the expenses of the truck's tenant
	initial     Row    // the columns the replay may change, as the row held them
}

// fleetState is what a step can change on a truck.
type fleetState struct {
	status                   string
	current, next, lastAlert int
	active                   *fleetRecord
	lastService              string
}

func (t *fleetTruck) state() fleetState {
	return fleetState{status: t.status, current: t.current, next: t.next, lastAlert: t.lastAlert, active: t.active,
		lastService: t.lastService}
}

// fleetEvent is one step: a fuel log approved with its odometer (rec nil), or a record taking a status, with the
// completion fields it carries from then on.
type fleetEvent struct {
	at      time.Time
	seq     int
	truck   *fleetTruck
	reading int
	rec     *fleetRecord
	status  string
	set     Row
}

type fleetEvents []*fleetEvent

func (e fleetEvents) Len() int { return len(e) }
func (e fleetEvents) Less(i, j int) bool {
	if !e[i].at.Equal(e[j].at) {
		return e[i].at.Before(e[j].at)
	}
	return e[i].seq < e[j].seq
}
func (e fleetEvents) Swap(i, j int) { e[i], e[j] = e[j], e[i] }
func (e *fleetEvents) Push(x any)   { *e = append(*e, x.(*fleetEvent)) }
func (e *fleetEvents) Pop() any {
	old := *e
	x := old[len(old)-1]
	*e = old[:len(old)-1]
	return x
}

// fleetHistory is one replay.
type fleetHistory struct {
	p      *Plan
	o      Options
	cutoff time.Time // the anchor's midnight: a booking is serviced and completed only when that ends before it
	trucks map[string]*fleetTruck
	order  []*fleetTruck
	events fleetEvents
	seq    int
	booked int
}

// fleetColumns are the truck columns the services change (UpdateTruckMaintenance), in a fixed order.
var fleetColumns = []string{"status", "current_mileage", "next_service_mileage", "last_alert_mileage", "active_maintenance_id",
	"last_service_date"}

func newFleetHistory(p *Plan, o Options) *fleetHistory {
	anchor := time.Date(o.Anchor.Year(), o.Anchor.Month(), o.Anchor.Day(), 0, 0, 0, 0, clock.Bangkok)
	return &fleetHistory{p: p, o: o, cutoff: anchor, trucks: map[string]*fleetTruck{}}
}

// truck registers a truck once (the plan's maintenance records of it so far count as they stand) and returns its state.
func (h *fleetHistory) truck(row Row, mileageOn func(time.Time) int, approver string) *fleetTruck {
	if t := h.trucks[row.Str("id")]; t != nil {
		return t
	}
	t := &fleetTruck{row: row, status: row.Str("status"), lastService: row.Str("last_service_date"), mileageOn: mileageOn,
		approver: approver, initial: Row{}}
	for _, c := range fleetColumns {
		t.initial[c] = row[c]
	}
	t.current, _ = intOf(row["current_mileage"])
	t.next, _ = intOf(row["next_service_mileage"])
	t.lastAlert, _ = intOf(row["last_alert_mileage"])
	t.interval, _ = intOf(row["pm_interval_km"])
	if t.interval <= 0 {
		t.interval = 10000
	}
	for _, m := range h.p.Tables["maintenance_records"] {
		if m.Str("truck_id") != row.Str("id") {
			continue
		}
		rec := &fleetRecord{row: m, status: m.Str("status")}
		t.records = append(t.records, rec)
		if m.Str("id") == row.Str("active_maintenance_id") {
			t.active = rec
		}
	}
	h.trucks[row.Str("id")] = t
	h.order = append(h.order, t)
	return t
}

func (h *fleetHistory) push(e *fleetEvent) {
	h.seq++
	e.seq = h.seq
	heap.Push(&h.events, e)
}

// fuel is a fuel log of t approved at at with its odometer.
func (h *fleetHistory) fuel(t *fleetTruck, at time.Time, reading int) {
	h.push(&fleetEvent{at: at, truck: t, reading: reading})
}

// fleetStep is a record taking a status at an instant.
type fleetStep struct {
	at     time.Time
	status string
}

// record appends a record the generator writes (row: its final state) and the steps that led there, in order: created
// with an open status, then any later statuses (in_progress at the check-in, completed or cancelled).
func (h *fleetHistory) record(t *fleetTruck, row Row, steps ...fleetStep) {
	rec := &fleetRecord{row: row}
	h.p.Tables["maintenance_records"] = append(h.p.Tables["maintenance_records"], row)
	for _, s := range steps {
		h.push(&fleetEvent{at: s.at, truck: t, rec: rec, status: s.status})
	}
}

// run replays every event in time order and writes the trucks' final state on their rows. changed lists the trucks
// whose row differs from what it held before, with the changed columns.
func (h *fleetHistory) run() (changed map[string][]string, err error) {
	for h.events.Len() > 0 {
		e := heap.Pop(&h.events).(*fleetEvent)
		t := e.truck
		before := t.state()
		switch {
		case e.rec == nil:
			if err := h.checkPM(t, e.at, e.reading); err != nil {
				return nil, err
			}
		case e.status == maintenance.StatusCompleted || e.status == maintenance.StatusCancelled:
			for k, v := range e.set {
				e.rec.row[k] = v
			}
			h.close(t, e.rec, e.status)
		default:
			h.open(t, e.rec, e.status)
		}
		if t.state() != before {
			t.updated = e.at
		}
	}
	changed = map[string][]string{}
	for _, t := range h.order {
		final := t.final()
		var cols []string
		for _, c := range fleetColumns {
			if fmt.Sprint(final[c]) != fmt.Sprint(t.initial[c]) {
				cols = append(cols, c)
				if final[c] == nil {
					if _, had := t.row[c]; had {
						t.row[c] = nil
					}
				} else {
					t.row[c] = final[c]
				}
			}
		}
		if len(cols) > 0 {
			t.row["updated_at"] = ts(t.updated.In(clock.Bangkok))
			changed[t.row.Str("id")] = cols
		}
	}
	return changed, nil
}

// final is the truck's state as row values.
func (t *fleetTruck) final() Row {
	r := Row{"status": t.status, "current_mileage": intNum(t.current), "next_service_mileage": nil, "last_alert_mileage": nil,
		"active_maintenance_id": nil, "last_service_date": nil}
	if t.initial["current_mileage"] == nil && t.current == 0 {
		r["current_mileage"] = nil
	}
	if t.next > 0 {
		r["next_service_mileage"] = intNum(t.next)
	}
	if t.lastAlert != 0 {
		r["last_alert_mileage"] = intNum(t.lastAlert)
	}
	if t.active != nil {
		r["active_maintenance_id"] = t.active.row.Str("id")
	}
	if t.lastService != "" {
		r["last_service_date"] = t.lastService
	}
	return r
}

// open is applyToTruck for a record taking an open status (pm_booking, scheduled, in_progress).
func (h *fleetHistory) open(t *fleetTruck, rec *fleetRecord, status string) {
	if rec.status == "" {
		t.records = append(t.records, rec)
	}
	rec.status = status
	if !t.activeIsOpen(rec) {
		t.active = rec
	}
	if status == maintenance.StatusInProgress {
		t.status = "maintenance"
	}
}

// close is applyToTruck for a record completed or cancelled: the truck leaves the shop, its active maintenance and PM
// alert mark are cleared, and a completion records the service date, the mileage and, for a PM, the next service.
func (h *fleetHistory) close(t *fleetTruck, rec *fleetRecord, status string) {
	rec.status = status
	if t.status == "maintenance" {
		t.status = "active"
	}
	if !t.activeIsOpen(rec) {
		t.active = nil
	}
	t.lastAlert = 0
	if status == maintenance.StatusCompleted {
		if end := rec.row.Str("end_date"); end != "" {
			t.lastService = end
		}
		if m, ok := intOf(rec.row["current_mileage"]); ok {
			t.current = m
		}
		if next, ok := intOf(rec.row["next_service_mileage"]); ok && rec.row.Str("maintenance_type") == "PM" {
			t.next = next
		}
	}
}

// activeIsOpen reports whether the truck's active maintenance is another record that is still open.
func (t *fleetTruck) activeIsOpen(self *fleetRecord) bool {
	return t.active != nil && t.active != self && openStatus(t.active.status)
}

func openStatus(s string) bool {
	return s != maintenance.StatusCompleted && s != maintenance.StatusCancelled
}

// checkPM is maintenance.checkPM for a fuel log approved at at: in order, no next service (nothing), an open active
// maintenance (nothing; a terminal one is cleared with the alert mark), the reading equal to the last alert (nothing),
// not due (the reading becomes the current mileage), due with a PM booking or scheduled visit open (nothing), else a
// pm_booking record dated the Bangkok day of the approval, by the approver, which becomes the truck's active
// maintenance with the reading as its last alert and current mileage. A booking whose service (two days later, 08:30 to
// 16:30) ends before the anchor is completed then: the record takes the service's mileage, end date, costs and the next
// service mileage the office sets (the next round past the one due, more than the alert threshold away), and the truck
// takes them through applyToTruck.
func (h *fleetHistory) checkPM(t *fleetTruck, at time.Time, reading int) error {
	if t.next <= 0 {
		return nil
	}
	if t.active != nil {
		if openStatus(t.active.status) {
			return nil
		}
		t.active, t.lastAlert = nil, 0
	}
	if reading == t.lastAlert {
		return nil
	}
	if !maintenance.IsDue(int64(reading), int64(t.next)) {
		t.current = reading
		return nil
	}
	if slices.ContainsFunc(t.records, func(r *fleetRecord) bool {
		return r.status == maintenance.StatusPMBooking || r.status == maintenance.StatusScheduled
	}) {
		return nil
	}
	b := at.In(clock.Bangkok)
	day := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, clock.Bangkok)
	plate, due := t.row.Str("license_plate"), t.next
	row := Row{"id": ID(h.o.Namespace, "maintenance_records", plate+":"+clock.DateString(day)+":PM").String(),
		"tenant_id": t.row.Str("tenant_id"), "tenant_source": "truck", "truck_id": t.row.Str("id"),
		"truck_license_plate_snapshot": plate, "maintenance_type": "PM", "service_type": maintenance.PMServiceType,
		"start_date": clock.DateString(day), "status": maintenance.StatusPMBooking, "current_mileage": intNum(reading),
		"next_service_mileage": intNum(due), "notes": maintenance.PMNotes(int64(reading), int64(due)), "created_by": t.approver,
		"created_at": ts(b)}
	if brand := t.row.Str("brand"); brand != "" {
		row["truck_brand_snapshot"] = brand
	}
	rec := &fleetRecord{row: row, status: maintenance.StatusPMBooking}
	h.p.Tables["maintenance_records"] = append(h.p.Tables["maintenance_records"], row)
	t.records = append(t.records, rec)
	t.active, t.lastAlert, t.current = rec, reading, reading
	h.booked++
	service := day.AddDate(0, 0, 2)
	out := service.Add(16*time.Hour + 30*time.Minute)
	if !out.Before(h.cutoff) {
		return nil // still booked today: the truck's active maintenance
	}
	mileage := t.mileageOn(service)
	if mileage < reading {
		return fmt.Errorf("seed: %s serviced at %d km after a reading of %d km", plate, mileage, reading)
	}
	next := due + t.interval
	for next-mileage <= maintenance.PMAlertThresholdKm {
		next += t.interval
	}
	labor, parts := pmCost(t.row.Str("vehicle_class"))
	h.push(&fleetEvent{at: out, truck: t, rec: rec, status: maintenance.StatusCompleted, set: Row{
		"status": maintenance.StatusCompleted, "end_date": clock.DateString(service),
		"check_in_at": ts(service.Add(8*time.Hour + 30*time.Minute)), "check_out_at": ts(out),
		"cost_labor_thb": money(labor), "cost_parts_thb": money(parts), "total_cost_thb": money(labor + parts),
		"invoice_amount_thb": money(labor + parts), "provider": garages[h.booked%len(garages)], "payment_method": "transfer",
		"driver_submitted": true, "current_mileage": intNum(mileage), "next_service_mileage": intNum(next),
		"updated_by": t.approver, "updated_at": ts(out)}})
	return nil
}

// pmCost is the labour and parts of a periodic service by vehicle class.
func pmCost(class string) (labor, parts float64) {
	switch class {
	case "6WH":
		return 2200, 5650
	case "10WH", "12WH", "18WH":
		return 3200, 9200
	}
	return 1500, 3200
}

// garages are synthetic service providers (§D.1.9: no real business is implied).
var garages = []string{"อู่ทดสอบลาดกระบัง (ตัวอย่าง)", "อู่ทดสอบบางนา (ตัวอย่าง)", "อู่ทดสอบรังสิต (ตัวอย่าง)",
	"ศูนย์บริการทดสอบบางพลี (ตัวอย่าง)"}

// approvers maps every tenant of the plan to the staff user who audits its expenses and books its maintenance: the
// own fleet's operation staff, NWR's tenant admin of the fixture, and every other tenant's {code}.admin login.
func approvers(p *Plan, o Options) (map[string]string, error) {
	users := map[string]bool{}
	for _, u := range p.Tables["users"] {
		users[u.Str("id")] = true
	}
	out := map[string]string{}
	for _, t := range p.Tables["tenants"] {
		var id string
		switch code := t.Str("code"); code {
		case "WRT":
			id = p.Symbols.MustID("U_WRT_OPS").String()
		case "NWR":
			id = p.Symbols.MustID("U_NWR_ADMIN").String()
		default:
			id = ID(o.Namespace, "users", strings.ToLower(code)+".admin@logitrack.test").String()
		}
		if users[id] {
			out[t.Str("id")] = id
		}
	}
	own := p.Symbols.MustID(ownFleetSymbol).String()
	if out[own] == "" {
		return nil, fmt.Errorf("seed: the own fleet has no expense approver")
	}
	return out, nil
}
