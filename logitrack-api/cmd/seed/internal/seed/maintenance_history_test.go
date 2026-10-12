package seed

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// buildPlan builds a plan with o (the test fixture, the test buckets).
func buildPlan(t *testing.T, o Options) *Plan {
	t.Helper()
	o.Bucket, o.PublicBucket, o.Backend = "logitrack", "logitrack-public", "s3"
	p, err := Build(context.Background(), testFixture(), o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// histTruck is a truck's maintenance state in the test's replay.
type histTruck struct {
	status                   string
	current, next, lastAlert int
	active                   string
	lastService              string
	recs                     map[string]string // record id -> status (records opened so far)
}

// histEvent is one step of the test's replay.
type histEvent struct {
	at      time.Time
	kind    int // 0 a fuel approval, 1 a record step (after the approvals of the same instant)
	reading int
	rec     Row
	status  string
}

// checkFleetHistory replays, independently of maintenance_history.go, the rules of maintenance.CheckPMInTx and
// applyToTruck over the rows the finance generators added to before (the same plan built without them): for every
// truck with a generated approved fuel log or maintenance record, from its row in before, the approvals at the
// expense's updated_at and each generated record opened at its created_at (scheduled), in progress at its check-in and
// completed or cancelled at its updated_at; a PM booking the check makes must be a generated pm-booked record of that
// truck created at that approval with the check's note (its mileages as the service later set them), and every such
// record must be made by a reading. Each truck row must end as the replay leaves it. It returns the bookings made.
func checkFleetHistory(t *testing.T, p, before *Plan) int {
	t.Helper()
	old := map[string]map[string]bool{}
	for _, table := range []string{"vehicle_expenses", "maintenance_records"} {
		old[table] = map[string]bool{}
		for _, r := range before.Tables[table] {
			old[table][r.Str("id")] = true
		}
	}
	initial := byID(before.Tables["trucks"])
	events := map[string][]histEvent{}
	for _, r := range p.Tables["vehicle_expenses"] {
		if old["vehicle_expenses"][r.Str("id")] || r.Str("expense_type") != "fuel" || r.Str("status") != "approved" {
			continue
		}
		odo, _ := intOf(r["odometer_km"])
		if odo <= 0 || r.Str("truck_id") == "" {
			continue
		}
		events[r.Str("truck_id")] = append(events[r.Str("truck_id")], histEvent{at: parseTS(t, r.Str("updated_at")), reading: odo})
	}
	booked := map[string]Row{} // truck | created_at -> a PM the check booked
	for _, r := range p.Tables["maintenance_records"] {
		if old["maintenance_records"][r.Str("id")] {
			continue
		}
		truck := r.Str("truck_id")
		if r.Str("maintenance_type") == "PM" && r.Str("service_type") == maintenance.PMServiceType {
			booked[truck+"|"+parseTS(t, r.Str("created_at")).UTC().String()] = r
			if r.Str("status") == "completed" || r.Str("status") == "cancelled" {
				events[truck] = append(events[truck], histEvent{at: parseTS(t, r.Str("updated_at")), kind: 1, rec: r, status: r.Str("status")})
			}
			continue
		}
		events[truck] = append(events[truck], histEvent{at: parseTS(t, r.Str("created_at")), kind: 1, rec: r, status: "scheduled"})
		if v := r.Str("check_in_at"); v != "" && r.Str("status") != "scheduled" {
			events[truck] = append(events[truck], histEvent{at: parseTS(t, v), kind: 1, rec: r, status: "in_progress"})
		}
		if r.Str("status") == "completed" || r.Str("status") == "cancelled" {
			events[truck] = append(events[truck], histEvent{at: parseTS(t, r.Str("updated_at")), kind: 1, rec: r, status: r.Str("status")})
		}
	}
	made := 0
	used := map[string]bool{}
	trucks := byID(p.Tables["trucks"])
	for id, evs := range events {
		row := initial[id]
		if row == nil {
			t.Errorf("truck %s: generated rows name a truck the master data does not have", id)
			continue
		}
		plate := row.Str("license_plate")
		h := &histTruck{status: row.Str("status"), active: row.Str("active_maintenance_id"), lastService: row.Str("last_service_date"),
			recs: map[string]string{}}
		h.current, _ = intOf(row["current_mileage"])
		h.next, _ = intOf(row["next_service_mileage"])
		h.lastAlert, _ = intOf(row["last_alert_mileage"])
		for _, m := range before.Tables["maintenance_records"] {
			if m.Str("truck_id") == id {
				h.recs[m.Str("id")] = m.Str("status")
			}
		}
		slices.SortStableFunc(evs, func(a, b histEvent) int {
			if c := a.at.Compare(b.at); c != 0 {
				return c
			}
			return a.kind - b.kind
		})
		open := func(st string) bool { return st != "" && st != "completed" && st != "cancelled" }
		activeIsOpen := func(self string) bool { return h.active != "" && h.active != self && open(h.recs[h.active]) }
		for _, e := range evs {
			if e.kind == 0 {
				if h.next <= 0 {
					continue
				}
				if h.active != "" {
					if open(h.recs[h.active]) {
						continue
					}
					h.active, h.lastAlert = "", 0
				}
				if e.reading == h.lastAlert {
					continue
				}
				if !maintenance.IsDue(int64(e.reading), int64(h.next)) {
					h.current = e.reading
					continue
				}
				busy := false
				for _, st := range h.recs {
					busy = busy || st == "pm_booking" || st == "scheduled"
				}
				if busy {
					continue
				}
				key := id + "|" + e.at.UTC().String()
				r := booked[key]
				if r == nil {
					t.Errorf("%s: the approved reading %d km of %s is due for the service at %d km, but no PM booking was made",
						plate, e.reading, e.at.Format(time.RFC3339), h.next)
					continue
				}
				if r.Str("notes") != maintenance.PMNotes(int64(e.reading), int64(h.next)) || r.Str("start_date") != clock.DateString(e.at) ||
					r.Str("created_by") == "" || r.Str("tenant_id") != row.Str("tenant_id") {
					t.Errorf("%s: the PM booking of %s is not the check's (%s, %s)", plate, e.at.Format(time.RFC3339), r.Str("notes"), r.Str("start_date"))
				}
				used[key], made = true, made+1
				h.recs[r.Str("id")] = "pm_booking"
				h.active, h.lastAlert, h.current = r.Str("id"), e.reading, e.reading
				continue
			}
			rid := e.rec.Str("id")
			if _, opened := h.recs[rid]; !opened && !open(e.status) {
				t.Errorf("%s: record %s closes, but nothing opened it", plate, rid)
				continue
			}
			h.recs[rid] = e.status
			if open(e.status) {
				if !activeIsOpen(rid) {
					h.active = rid
				}
				if e.status == "in_progress" {
					h.status = "maintenance"
				}
				continue
			}
			if h.status == "maintenance" {
				h.status = "active"
			}
			if !activeIsOpen(rid) {
				h.active = ""
			}
			h.lastAlert = 0
			if e.status == "completed" {
				h.lastService = e.rec.Str("end_date")
				if m, ok := intOf(e.rec["current_mileage"]); ok {
					h.current = m
				}
				if n, ok := intOf(e.rec["next_service_mileage"]); ok && e.rec.Str("maintenance_type") == "PM" {
					if n-h.current <= maintenance.PMAlertThresholdKm {
						t.Errorf("%s: the service of %s sets the next service %d km within the alert threshold of %d km", plate, rid, n, h.current)
					}
					h.next = n
				}
			}
		}
		got := trucks[id]
		want := map[string]string{"status": h.status, "current_mileage": fmt.Sprint(h.current), "next_service_mileage": "",
			"last_alert_mileage": "", "active_maintenance_id": h.active, "last_service_date": h.lastService}
		if h.next > 0 {
			want["next_service_mileage"] = fmt.Sprint(h.next)
		}
		if h.lastAlert != 0 {
			want["last_alert_mileage"] = fmt.Sprint(h.lastAlert)
		}
		for col, w := range want {
			g := ""
			if v := got[col]; v != nil {
				g = fmt.Sprint(v)
			}
			if g != w && (col != "current_mileage" || g != "" || w != "0") {
				t.Errorf("%s: %s is %q, the replay of its approved fuel logs and records gives %q", plate, col, g, w)
			}
		}
	}
	for key, r := range booked {
		if !used[key] {
			t.Errorf("PM booking %s (%s) was made by no approved reading", r.Str("id"), r.Str("truck_license_plate_snapshot"))
		}
	}
	return made
}
