package seed

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The operations generator (T35, issue #55) on the plans seed --profile demo loads: the counts do not depend on
// SEED_RANDOM_SEED, every lifecycle state of Appendix D §D.2 is present, and the invariants the issue names (1-3, 9,
// 11) hold on the plan before anything is written, under the default anchor, the integration tests' seed and an
// anchor set to a later day; the integration tests then load the same plans and run --verify (all twelve).

// opsPlans are the demo plans the tests check: two seeds, a later anchor, and both together (the integration tests'
// SEED_RANDOM_SEED, which deploy/vm/env.vm.example also sets, with an anchor of the staging VM's days: it once crashed
// today() on an unexpanded demo-hub route).
func opsPlans(t *testing.T) map[string]*Plan {
	t.Helper()
	later := time.Date(2026, 10, 11, 0, 0, 0, 0, clock.Bangkok)
	return map[string]*Plan{"default": plan(t, ProfileDemo), "integration-seed": planSeed(t, ProfileDemo, integrationSeed),
		"anchor-2026-10-11": planAt(t, 0, later), "integration-seed-anchor-2026-10-11": planAt(t, integrationSeed, later)}
}

// planAt builds the demo plan of a SEED_RANDOM_SEED (0: the default) and SEED_ANCHOR_DATE.
func planAt(t *testing.T, seed uint64, anchor time.Time) *Plan {
	t.Helper()
	p, err := Build(context.Background(), testFixture(), Options{Profile: ProfileDemo, RandomSeed: seed, Bucket: "logitrack",
		PublicBucket: "logitrack-public", Backend: "s3", Anchor: anchor})
	if err != nil {
		t.Fatalf("seed %d, anchor %s: %v", seed, clock.DateString(anchor), err)
	}
	return p
}

// The generator builds under any SEED_RANDOM_SEED and SEED_ANCHOR_DATE (Appendix D §D.1.4 suggests the anchor be today
// for live-looking boards): a sweep over seeds and the staging VM's October days, each plan with today's drivers and
// one trip at a time per driver. Before the fix, 11 of the 30 October anchors crashed under the VM's seed.
func TestOperationsBuildUnderSeedsAndAnchors(t *testing.T) {
	seeds := []uint64{0, integrationSeed, 1, 2, 3, 5, 8, 13}
	anchors := []time.Time{time.Date(2026, 10, 9, 0, 0, 0, 0, clock.Bangkok), time.Date(2026, 10, 12, 0, 0, 0, 0, clock.Bangkok),
		time.Date(2026, 10, 15, 0, 0, 0, 0, clock.Bangkok), time.Date(2026, 10, 27, 0, 0, 0, 0, clock.Bangkok)}
	for _, seed := range seeds {
		for _, anchor := range anchors {
			t.Run(fmt.Sprintf("%d-%s", seed, clock.DateString(anchor)), func(t *testing.T) {
				t.Parallel()
				p := planAt(t, seed, anchor)
				checkTodayDrivers(t, p, clock.DateString(anchor))
				checkDriverTimelines(t, p)
			})
		}
	}
}

// generated are the rows of table the demo appends to smoke.
func generated(t *testing.T, p *Plan, table string) []Row {
	t.Helper()
	n := len(plan(t, ProfileSmoke).Tables[table])
	return p.Tables[table][n:]
}

func countBy(rows []Row, key func(Row) string) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		out[key(r)]++
	}
	return out
}

func parseTS(t *testing.T, v string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, v)
	if err != nil {
		t.Fatalf("instant %q: %v", v, err)
	}
	return at
}

// The counts of the operations tables are a function of the window, never of SEED_RANDOM_SEED (the PCG stream draws
// routes, spellings, times and sizes only), so Appendix D §D.1.2 can state them exactly.
func TestOperationsCountsIgnoreTheSeed(t *testing.T) {
	a, b := plan(t, ProfileDemo), planSeed(t, ProfileDemo, integrationSeed)
	for _, table := range append(slices.Clone(opsTables), "trip_billing_stop_breakdown") {
		if len(a.Tables[table]) != len(b.Tables[table]) {
			t.Errorf("%s: %d rows with the default seed, %d with %d", table, len(a.Tables[table]), len(b.Tables[table]), integrationSeed)
		}
	}
	if reflectEqualRows(a.Tables["tasks"], b.Tables["tasks"]) {
		t.Error("two seeds draw the same tasks: the generator ignores SEED_RANDOM_SEED")
	}
}

func reflectEqualRows(a, b []Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if fmt.Sprint(a[i]) != fmt.Sprint(b[i]) {
			return false
		}
	}
	return true
}

// AC "every lifecycle state present" (Appendix D §D.2 #1-#5, #18, #26): task, trip and stop statuses, today's board,
// the multi-drop modes, the four standby cases, the incident kinds, the legacy and orphan-plate rows, the unresolved
// origins and the switch-day trips.
func TestOperationsLifecycleStates(t *testing.T) {
	for name, p := range opsPlans(t) {
		t.Run(name, func(t *testing.T) {
			tasks, trips := p.Tables["tasks"], p.Tables["trip_records"]
			for _, st := range operations.TaskStatuses {
				if countBy(tasks, func(r Row) string { return r.Str("status") })[st] == 0 {
					t.Errorf("no task is %s", st)
				}
			}
			for _, st := range operations.TripStatuses {
				if countBy(trips, func(r Row) string { return r.Str("status") })[st] == 0 {
					t.Errorf("no trip is %s", st)
				}
			}
			for table, want := range map[string][]string{"task_delivery_stops": {"pending", "delivered", "failed"},
				"trip_delivery_stops": {"delivered", "failed"}} {
				got := countBy(generated(t, p, table), func(r Row) string { return r.Str("status") })
				for _, st := range want {
					if got[st] == 0 {
						t.Errorf("no generated %s row is %s", table, st)
					}
				}
			}
			anchor := p.Tables["tasks"][len(p.Tables["tasks"])-1].Str("plan_at")[:10]
			board := countBy(p.Tables["tasks"], func(r Row) string {
				if r.Str("task_type") != "first_mile" || r.Str("plan_at")[:10] != anchor {
					return ""
				}
				return r.Str("status")
			})
			for st, want := range boardFM {
				if board[st] != want {
					t.Errorf("today's first-mile board has %d %s tasks, Appendix D §D.2 #1 says %d", board[st], st, want)
				}
			}
			checkTodayDrivers(t, p, anchor)

			gtrips, gtasks := generated(t, p, "trip_records"), generated(t, p, "tasks")
			taskOf := byID(p.Tables["tasks"])
			snapOf := map[string]Row{}
			for _, s := range p.Tables["trip_billing_snapshots"] {
				snapOf[s.Str("trip_id")] = s
			}
			var orphan, ocr, flat, legacyMulti, failed, switchDay int
			hubIDs := map[string]bool{}
			for _, h := range p.Tables["hubs"] {
				hubIDs[h.Str("id")] = true
			}
			for _, tr := range gtrips {
				if tr.Str("truck_license_plate_snapshot") == orphanPlate && tr["truck_id"] == nil {
					orphan++
					if s := snapOf[tr.Str("id")]; tr.Str("status") == "delivered" && s.Str("unpriced_reason") != "no_vehicle_class" {
						t.Errorf("orphan-plate trip %s is not unpriced no_vehicle_class", tr.Str("trip_no"))
					}
				}
				if strings.HasSuffix(tr.Str("origin_raw"), "(OCR)") && tr["origin_hub_id"] == nil {
					ocr++
				}
				if tr["is_multi_delivery"] == true && tr.Str("status") == "delivered" {
					s := snapOf[tr.Str("id")]
					switch {
					case s.Str("estimate_thb") == "" || s["is_multi_delivery"] != true:
						t.Errorf("multi-drop trip %s is not priced as one", tr.Str("trip_no"))
					case taskOf[tr.Str("task_id")].Str("billing_party_id") == p.Symbols.MustID("BP_SPK").String(): // SF01 extra_stop fee
						flat++
						want := "600.00" // two extra stops at SF01 300
						if hasFailedStop(p, tr.Str("id")) {
							failed, want = failed+1, "300.00" // the failed stop is not charged
						}
						if s.Str("stop_charge_thb") != want {
							t.Errorf("flat multi-drop %s charges %s for its extra stops, want %s", tr.Str("trip_no"), s.Str("stop_charge_thb"), want)
						}
					default:
						legacyMulti++
					}
				}
				if k := taskOf[tr.Str("task_id")]; strings.HasSuffix(k.Str("plan_at"), "T00:21:00+07:00") {
					switchDay++
					s := snapOf[tr.Str("id")]
					if day := k.Str("plan_at")[:10]; s.Str("round_effective_from_date") != day {
						t.Errorf("switch-day trip %s (plan %s) priced under the round of %s", tr.Str("trip_no"), k.Str("plan_at"),
							s.Str("round_effective_from_date"))
					}
				}
			}
			if orphan != 3 || ocr != 10 || flat != 2 || failed != 1 || legacyMulti != 1 || switchDay == 0 {
				t.Errorf("orphan-plate %d (want 3), OCR origins %d (want 10), flat multi-drop %d (want 2, one with a failed stop: %d), "+
					"legacy multi-drop %d (want 1), switch-day trips %d (want at least one)", orphan, ocr, flat, failed, legacyMulti, switchDay)
			}
			unpadded := regexp.MustCompile(`^(FM|LH)-[0-9]{8}-[0-9]{1,2}$`)
			legacy := 0
			for _, k := range gtasks {
				if unpadded.MatchString(k.Str("task_no")) {
					legacy++
					if k.Str("legacy_doc_id") == "" || k["job_category"] != nil || k.Str("driver_ref_match") != "auth_uid" {
						t.Errorf("legacy task %s lacks its doc id, auth uid or NULL category", k.Str("task_no"))
					}
				}
			}
			if legacy != 5 {
				t.Errorf("%d generated legacy unpadded task numbers, Appendix D §D.2 #2 says 5", legacy)
			}
			sb := countBy(generated(t, p, "standby_records"), func(r Row) string {
				if v := r.Str("billing_unpriced_reason"); v != "" {
					return v
				}
				return r.Str("billing_rate_source")
			})
			// With the four smoke records: 15 rate, 4 fee, 3 no_customer, 2 no_rate, 1 no_ended_at (Appendix D §D.2 #4).
			if want := map[string]int{"standby_rate": 13, "service_fee": 3, "no_customer": 3, "no_rate": 1, "no_ended_at": 1}; fmt.Sprint(sb) != fmt.Sprint(want) {
				t.Errorf("generated standby cases %v, want %v", sb, want)
			}
			inc := generated(t, p, "incident_reports")
			kinds := countBy(inc, func(r Row) string { return r.Str("reported_by_kind") })
			after, onIncident := 0, 0
			tripByID := byID(p.Tables["trip_records"])
			for _, r := range inc {
				tr := tripByID[r.Str("trip_id")]
				if tr.Str("status") == "incident" {
					onIncident++
				}
				if tr.Str("delivered_at") != "" && parseTS(t, r.Str("created_at")).After(parseTS(t, tr.Str("delivered_at"))) {
					after++
				}
			}
			if len(inc) != 11 || kinds["admin"] != 2 || after != 1 || onIncident != 2 {
				t.Errorf("incidents: %d (want 11 beside IR1), %d by the office (want 2), %d after delivery (want 1), %d on a trip "+
					"stopped by it (want 2)", len(inc), kinds["admin"], after, onIncident)
			}
		})
	}
}

func hasFailedStop(p *Plan, tripID string) bool {
	for _, s := range p.Tables["trip_delivery_stops"] {
		if s.Str("trip_id") == tripID && s.Str("status") == "failed" {
			return true
		}
	}
	return false
}

// checkTodayDrivers: a driver with a task under way today points at it (drivers.active_task_id), holds one at most,
// and that task is checked in or in transit; it holds the task's truck as its active truck from the check-in on
// (activeTruck is one concept: truck, plate, task and start set together, as GET /v1/drivers/{id} reads them), and a
// driver without a task under way holds none of the four.
func checkTodayDrivers(t *testing.T, p *Plan, anchor string) {
	t.Helper()
	tasks, trucks := byID(p.Tables["tasks"]), byID(p.Tables["trucks"])
	active, underWay := 0, 0
	for _, k := range generated(t, p, "tasks") {
		if (k.Str("status") == "checked_in" || k.Str("status") == "in_transit") && k.Str("plan_at")[:10] == anchor {
			underWay++
		}
	}
	for _, d := range generated(t, p, "drivers") {
		id := d.Str("active_task_id")
		if id == "" {
			if d["active_truck_id"] != nil || d["active_truck_plate"] != nil || d["active_started_at"] != nil {
				t.Errorf("driver %s holds an active truck without a task under way", d.Str("id_card"))
			}
			continue
		}
		active++
		k := tasks[id]
		if k == nil || k.Str("driver_id") != d.Str("id") || (k.Str("status") != "checked_in" && k.Str("status") != "in_transit") ||
			k.Str("plan_at")[:10] != anchor {
			t.Errorf("driver %s points at %s, which is not its task under way today", d.Str("id_card"), id)
			continue
		}
		if tr := trucks[k.Str("truck_id")]; tr == nil || d.Str("active_truck_id") != tr.Str("id") ||
			d.Str("active_truck_plate") != tr.Str("license_plate") || d.Str("active_started_at") != k.Str("check_in_at") {
			t.Errorf("driver %s: active truck %q (%q) since %q, want its task's truck %s and check-in %s", d.Str("id_card"),
				d.Str("active_truck_id"), d.Str("active_truck_plate"), d.Str("active_started_at"), k.Str("truck_id"), k.Str("check_in_at"))
		}
	}
	if active != underWay || active == 0 {
		t.Errorf("%d generated drivers point at a task under way, the board has %d generated ones", active, underWay)
	}
}

// AC "invariants 1-3, 9, 11 pass", evaluated on the plan as Appendix D §D.3 states them (the integration tests run the
// SQL of all twelve on the loaded database): every trip has a same-tenant task and a truck snapshot; every delivered
// trip has a snapshot that is priced or carries an unpriced reason that re-derives; trip and task categories agree;
// no business row references a pending object; Bangkok-day columns and the switch-day round. Beside them the rules a
// generated row could break: tenancy (#10a), period locks (#5), and one trip at a time per driver on a truck it held.
func TestOperationsInvariants(t *testing.T) {
	for name, p := range opsPlans(t) {
		t.Run(name, func(t *testing.T) {
			tasks, trucks, drivers := byID(p.Tables["tasks"]), byID(p.Tables["trucks"]), byID(p.Tables["drivers"])
			snap := map[string]Row{}
			for _, s := range p.Tables["trip_billing_snapshots"] {
				snap[s.Str("trip_id")] = s
			}
			rates := map[string]bool{}
			for _, r := range p.Tables["customer_rate_entries"] {
				if r["voided"] != true {
					rates[r.Str("billing_party_id")+"|"+r.Str("hub_code")+"|"+r.Str("destination_code")] = true
				}
			}
			for _, tr := range p.Tables["trip_records"] {
				k := tasks[tr.Str("task_id")]
				// #1
				if k == nil || k.Str("tenant_id") != tr.Str("tenant_id") || (tr["truck_id"] == nil && tr["truck_license_plate_snapshot"] == nil) ||
					(tr["truck_id"] != nil && (tr["truck_license_plate_snapshot"] == nil || tr["vehicle_class"] == nil)) {
					t.Errorf("#1 trip %s: task, tenant or truck snapshot", tr.Str("trip_no"))
					continue
				}
				// #3
				if k["job_category"] != nil && tr.Str("job_category") != k.Str("job_category") {
					t.Errorf("#3 trip %s: category %q, task %q", tr.Str("trip_no"), tr.Str("job_category"), k.Str("job_category"))
				}
				// #10a
				if tk := trucks[tr.Str("truck_id")]; tk != nil && tk.Str("tenant_id") != tr.Str("tenant_id") {
					t.Errorf("#10a trip %s runs a truck of another tenant", tr.Str("trip_no"))
				}
				if d := drivers[tr.Str("driver_id")]; d != nil && d.Str("tenant_id") != tr.Str("tenant_id") &&
					!hasDriverMembership(p, d.Str("user_id"), tr.Str("tenant_id")) {
					t.Errorf("#10a trip %s: driver of another tenant without a membership there", tr.Str("trip_no"))
				}
				// #2a
				if tr.Str("status") != "delivered" {
					continue
				}
				s := snap[tr.Str("id")]
				party := firstNonEmpty(k.Str("billing_party_id"), k.Str("source_linked_party_id"), k.Str("destination_linked_party_id"))
				switch {
				case s == nil:
					t.Errorf("#2 delivered trip %s has no snapshot", tr.Str("trip_no"))
				case s.Str("estimate_thb") != "":
				case s.Str("unpriced_reason") == "no_customer" && party == "":
				case s.Str("unpriced_reason") == "no_vehicle_class" && party != "" && k["truck_type"] == nil:
				case s.Str("unpriced_reason") == "no_rate" && party != "" && k["truck_type"] != nil &&
					!rates[party+"|"+s.Str("lookup_hub_code")+"|"+s.Str("lookup_destination_code")]:
				default:
					t.Errorf("#2 trip %s: reason %q does not re-derive", tr.Str("trip_no"), s.Str("unpriced_reason"))
				}
				// #11: tasks.plan_date is bkk_date(plan_at) by construction (generated column); the switch-day trips are
				// checked in TestOperationsLifecycleStates.
			}
			checkPending(t, p)
			checkLocks(t, p)
			checkDriverTimelines(t, p)
		})
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func hasDriverMembership(p *Plan, user, tenant string) bool {
	for _, m := range p.Tables["memberships"] {
		if m.Str("user_id") == user && m.Str("tenant_id") == tenant && m.Str("role") == "driver" {
			return true
		}
	}
	return false
}

// checkPending is invariant 9b: no business row references a pending upload.
func checkPending(t *testing.T, p *Plan) {
	t.Helper()
	pending := map[string]bool{}
	for _, f := range p.Tables["file_objects"] {
		if f.Str("status") == "pending" {
			pending[f.Str("id")] = true
		}
	}
	refs := map[string][]string{"trip_photos": {"file_id"}, "tasks": {"check_in_photo_file_id", "check_in_app_screenshot_file_id"},
		"incident_reports": {"map_photo_file_id", "situation1_photo_file_id", "situation2_photo_file_id"}}
	for table, cols := range refs {
		for _, r := range p.Tables[table] {
			for _, c := range cols {
				if pending[r.Str(c)] {
					t.Errorf("#9b %s %s references the pending object %s", table, r.Str("id"), r.Str(c))
				}
			}
		}
	}
}

// checkLocks is invariant 5 for the generated prices: none written after a sent or paid statement of its party and
// Bangkok month.
func checkLocks(t *testing.T, p *Plan) {
	t.Helper()
	sent := map[string]time.Time{}
	for _, b := range p.Tables["billing_statements"] {
		if b.Str("status") == "sent" || b.Str("status") == "paid" {
			sent[fmt.Sprintf("%s|%s|%s", b.Str("billing_party_id"), b["period_year"], b["period_month"])] = parseTS(t, b.Str("sent_at"))
		}
	}
	trips := byID(p.Tables["trip_records"])
	key := func(party string, axis time.Time) string {
		y, m := clock.YearMonth(axis)
		return fmt.Sprintf("%s|%d|%d", party, y, m)
	}
	for _, s := range generated(t, p, "trip_billing_snapshots") {
		tr := trips[s.Str("trip_id")]
		axis := tr.Str("billing_date")
		if axis == "" {
			axis = tr.Str("delivered_at")
		}
		if at, ok := sent[key(tr.Str("billing_party_id"), parseTS(t, axis))]; ok && parseTS(t, s.Str("updated_at")).After(at) {
			t.Errorf("#5 trip %s priced after its period was sent", tr.Str("trip_no"))
		}
	}
	for _, r := range generated(t, p, "standby_records") {
		axis := r.Str("ended_at")
		if axis == "" {
			axis = r.Str("started_at")
		}
		if at, ok := sent[key(r.Str("billing_party_id"), parseTS(t, axis))]; ok && parseTS(t, r.Str("billing_computed_at")).After(at) {
			t.Errorf("#5 standby %s priced after its period was sent", r.Str("id"))
		}
	}
}

// checkDriverTimelines: a driver runs one trip at a time (loading to delivery), on a truck it held then, and its
// standby waits do not overlap its trips.
func checkDriverTimelines(t *testing.T, p *Plan) {
	t.Helper()
	type span struct {
		what     string
		from, to time.Time
	}
	held := map[string][]opsSpan{}
	trucks := byID(p.Tables["trucks"])
	for _, a := range p.Tables["truck_assignments"] {
		if a.Str("status") == "cancelled" {
			continue
		}
		sp := opsSpan{truck: trucks[a.Str("truck_id")], from: parseTS(t, a.Str("created_at"))}
		if a.Str("revoked_at") != "" {
			sp.to = parseTS(t, a.Str("revoked_at"))
		}
		held[a.Str("driver_id")] = append(held[a.Str("driver_id")], sp)
	}
	byDriver := map[string][]span{}
	for _, tr := range generated(t, p, "trip_records") {
		from := parseTS(t, tr.Str("std"))
		to := from.Add(40 * time.Minute)
		if tr.Str("delivered_at") != "" {
			to = parseTS(t, tr.Str("delivered_at"))
		}
		if id := tr.Str("truck_id"); id != "" {
			ok := false
			for _, sp := range held[tr.Str("driver_id")] {
				if sp.truck.Str("id") == id && !sp.from.After(from) && (sp.to.IsZero() || sp.to.After(to)) {
					ok = true
				}
			}
			if !ok {
				t.Errorf("trip %s runs truck %s its driver did not hold", tr.Str("trip_no"), id)
			}
		}
		byDriver[tr.Str("driver_id")] = append(byDriver[tr.Str("driver_id")], span{tr.Str("trip_no"), from, to})
	}
	for _, r := range generated(t, p, "standby_records") {
		from := parseTS(t, r.Str("started_at"))
		to := from.Add(time.Hour)
		if r.Str("ended_at") != "" {
			to = parseTS(t, r.Str("ended_at"))
		}
		byDriver[r.Str("driver_id")] = append(byDriver[r.Str("driver_id")], span{"standby " + r.Str("id"), from, to})
	}
	for d, spans := range byDriver {
		sort.Slice(spans, func(i, j int) bool { return spans[i].from.Before(spans[j].from) })
		for i := 1; i < len(spans); i++ {
			if spans[i].from.Before(spans[i-1].to) {
				t.Errorf("driver %s: %s starts before %s ends", d, spans[i].what, spans[i-1].what)
			}
		}
	}
}

// Golden prices of the generated vectors (the engine's, never typed; Appendix D §D.1.4): the switch-day trips take
// the round that starts that day (FA02 16 Aug, legacy 07:00 instant: 1200 - 50; FA03 1 Sep: 1200 - 40), the flat
// multi-drop is RE08 1100 plus two stops at SF01 300, and every priced SUPPLEMENTARY trip is frozen without fuel.
func TestOperationsGoldenPrices(t *testing.T) {
	p := plan(t, ProfileDemo)
	tasks, trips := byID(p.Tables["tasks"]), byID(p.Tables["trip_records"])
	seen := map[string]bool{}
	for _, s := range generated(t, p, "trip_billing_snapshots") {
		tr := trips[s.Str("trip_id")]
		k := tasks[tr.Str("task_id")]
		switch k.Str("plan_at") {
		case "2026-08-16T00:21:00+07:00":
			seen["aug16"] = true
			if s.Str("estimate_thb") != "1150.00" || s.Str("rate_entry_id") != p.Symbols.MustID("RE01").String() ||
				s.Str("fuel_adjustment_id") != p.Symbols.MustID("FA02").String() {
				t.Errorf("16 Aug 00:21 trip: %s via %s / %s, want 1150.00 = RE01 1200 - FA02 50", s.Str("estimate_thb"),
					s.Str("rate_entry_id"), s.Str("fuel_adjustment_id"))
			}
		case "2026-09-01T00:21:00+07:00":
			seen["sep1"] = true
			if s.Str("estimate_thb") != "1160.00" || s.Str("fuel_adjustment_id") != p.Symbols.MustID("FA03").String() {
				t.Errorf("1 Sep 00:21 trip: %s, want 1160.00 = RE01 1200 - FA03 40", s.Str("estimate_thb"))
			}
		}
		if s.Str("stop_charge_thb") == "600.00" && s["is_multi_delivery"] == true {
			seen["flat"] = true
			if s.Str("estimate_thb") != "1700.00" || s.Str("base_rate_thb") != "1100.00" {
				t.Errorf("flat multi-drop %s: %s (base %s), want 1700.00 = RE08 1100 + 2 x SF01 300", tr.Str("trip_no"),
					s.Str("estimate_thb"), s.Str("base_rate_thb"))
			}
		}
		if s.Str("job_category_at_pricing") == string(compute.Supplementary) && s.Str("estimate_thb") != "" {
			seen["sup"] = true
			if s["manual_override"] != true || s["fuel_adjustment_id"] != nil || s.Str("rate_multiplier") != "1.000000" ||
				s.Str("add_thb_per_trip") != "0.00" {
				t.Errorf("SUPPLEMENTARY trip %s is not frozen without fuel (invariant 4)", tr.Str("trip_no"))
			}
		}
	}
	for _, want := range []string{"aug16", "sep1", "flat", "sup"} {
		if !seen[want] {
			t.Errorf("the demo has no %s vector", want)
		}
	}
}

// Generated rows follow the conventions of the fixture: uuid v5 ids of their natural keys (task and trip numbers,
// "<trip_no>:<photo_type>", "<bucket>:<object key>"), object keys from the internal/storage templates, client_op_id on
// every native driver-created row (R63) and none on staff or legacy rows, and task numbers that never repeat one the
// plan already holds. Rows that stand for ETL-migrated data keep the legacy shapes (Appendix D §D.1.3 #5, §D.1.6).
func TestOperationsConventions(t *testing.T) {
	for name, p := range opsPlans(t) {
		t.Run(name, func(t *testing.T) {
			checkConventions(t, p)
			checkLegacyRows(t, p)
			checkCreatedAtCheckIn(t, p)
		})
	}
}

func checkConventions(t *testing.T, p *Plan) {
	t.Helper()
	ns := DefaultNamespace
	numbers := map[string]int{}
	for _, k := range p.Tables["tasks"] {
		if _, _, seq, _, ok := operations.ParseTaskNo(k.Str("task_no")); ok {
			numbers[fmt.Sprintf("%s|%d", k.Str("task_no")[:11], seq)]++
		}
	}
	for n, c := range numbers {
		if c > 1 {
			t.Errorf("task number %s (type, day, sequence) is held by %d tasks", n, c)
		}
	}
	for _, k := range generated(t, p, "tasks") {
		natural := k.Str("task_no")
		if doc := k.Str("legacy_doc_id"); doc != "" {
			natural = "legacy:" + doc
		}
		if k.Str("id") != ID(ns, "tasks", natural).String() {
			t.Errorf("task %s: id is not uuid v5 of its natural key", natural)
		}
		driverMade := k.Str("tenant_source") == "driver"
		if driverMade && k.Str("legacy_doc_id") == "" && (k["client_op_id"] == nil || k.Str("created_by") != userOf(p, k.Str("driver_id"))) {
			t.Errorf("driver-created task %s lacks its client_op_id or creator", natural)
		}
		if !driverMade && k["client_op_id"] != nil {
			t.Errorf("staff task %s carries a client_op_id", natural)
		}
	}
	for _, tr := range generated(t, p, "trip_records") {
		if tr.Str("id") != ID(ns, "trip_records", tr.Str("trip_no")).String() || !regexp.MustCompile(`^ZX[JZ]B[0-9]{6}2[0-9]{4}$`).MatchString(tr.Str("trip_no")) {
			t.Errorf("trip %s: id or number outside the generated range", tr.Str("trip_no"))
		}
	}
	trips := byID(p.Tables["trip_records"])
	for _, ph := range generated(t, p, "trip_photos") {
		if ph.Str("id") != ID(ns, "trip_photos", trips[ph.Str("trip_id")].Str("trip_no")+":"+ph.Str("photo_type")).String() {
			t.Errorf("photo %s: id is not uuid v5 of <trip_no>:<photo_type>", ph.Str("id"))
		}
	}
	for _, f := range generated(t, p, "file_objects") {
		if f.Str("id") != ID(ns, "file_objects", "logitrack:"+f.Str("object_key")).String() || f.Str("status") != "committed" {
			t.Errorf("object %s: id or status", f.Str("object_key"))
		}
		switch tr := trips[f.Str("owner_id")]; {
		case f["legacy_url"] != nil: // an object the ETL copied: its legacy key under the trip's legacy doc id, no uploader
			if tr == nil || tr.Str("legacy_doc_id") == "" || f.Str("object_key") != "trip_records/"+tr.Str("legacy_doc_id")+"/"+
				strings.TrimSuffix(path.Base(f.Str("object_key")), ".jpg")+".jpg" || f["uploaded_by"] != nil ||
				f.Str("legacy_url") != legacyStorageURL+strings.ReplaceAll(f.Str("object_key"), "/", "%2F")+"?alt=media" {
				t.Errorf("legacy object %s: key, URL or uploader", f.Str("object_key"))
			}
		case !strings.Contains(f.Str("object_key"), f.Str("owner_id")) || f["uploaded_by"] == nil:
			t.Errorf("object %s: a native upload names its owner in its key and its uploader", f.Str("object_key"))
		}
	}
	own := p.Symbols.MustID(ownFleetSymbol).String()
	for _, r := range generated(t, p, "standby_records") {
		if (r["legacy_doc_id"] == nil) != (r["client_op_id"] != nil) {
			t.Errorf("standby %s: a native record carries a client_op_id, a legacy one none", r.Str("id"))
		}
		if priced := r["billing_estimate_thb"] != nil; priced != (r.Str("billing_tenant_id") == own) {
			t.Errorf("standby %s: a priced record bills on the own fleet (0015), an unpriced one names no carrier", r.Str("id"))
		}
	}
	for _, r := range generated(t, p, "incident_reports") {
		if (r.Str("reported_by_kind") == "driver") != (r["client_op_id"] != nil) {
			t.Errorf("incident %s: client_op_id on a driver report only", r.Str("id"))
		}
	}
}

// checkLegacyRows: the generated rows that stand for ETL-migrated data look as the ETL writes them. Their photos are
// the objects it copied (trip_records/{doc}/{type}.jpg, legacy_url, no uploader), a legacy task has no check-in photo,
// and the driver's auth uid they carry (driver_ref_match auth_uid, R12) is the one its driver and user hold (R55),
// never made of its national ID (drivers.id_card is behind drivers:view_pii, legacy_driver_ref is not).
func checkLegacyRows(t *testing.T, p *Plan) {
	t.Helper()
	drivers, users, objects := byID(p.Tables["drivers"]), byID(p.Tables["users"]), byID(p.Tables["file_objects"])
	var nids []string
	for _, d := range p.Tables["drivers"] {
		if n := d.Str("id_card"); n != "" {
			nids = append(nids, n)
		}
	}
	refs := 0
	for _, table := range []string{"tasks", "trip_records", "standby_records"} {
		for _, r := range generated(t, p, table) {
			if r.Str("driver_ref_match") != "auth_uid" {
				continue
			}
			refs++
			uid, d := r.Str("legacy_driver_ref"), drivers[r.Str("driver_id")]
			if d == nil || len(uid) != 28 || !strings.HasPrefix(uid, "SEEDUID") || d.Str("legacy_auth_uid") != uid ||
				users[d.Str("user_id")].Str("legacy_auth_uid") != uid {
				t.Errorf("%s %s: legacy_driver_ref %q is not the auth uid its driver and user hold", table, r.Str("id"), uid)
			}
			for _, n := range nids {
				if strings.Contains(uid, n) {
					t.Errorf("%s %s: legacy_driver_ref %q holds a national ID", table, r.Str("id"), uid)
				}
			}
		}
	}
	legacyTrips, photos := 0, 0
	byTrip := map[string][]Row{}
	for _, ph := range p.Tables["trip_photos"] {
		byTrip[ph.Str("trip_id")] = append(byTrip[ph.Str("trip_id")], ph)
	}
	for _, tr := range generated(t, p, "trip_records") {
		if tr.Str("legacy_doc_id") == "" {
			continue
		}
		legacyTrips++
		for _, ph := range byTrip[tr.Str("id")] {
			photos++
			f := objects[ph.Str("file_id")]
			if f == nil || !strings.HasPrefix(f.Str("object_key"), "trip_records/"+tr.Str("legacy_doc_id")+"/") || f["legacy_url"] == nil ||
				f["uploaded_by"] != nil {
				t.Errorf("legacy trip %s: photo %s is not an object the ETL copied", tr.Str("trip_no"), ph.Str("photo_type"))
			}
		}
	}
	for _, k := range generated(t, p, "tasks") {
		if k.Str("legacy_doc_id") != "" && (k["check_in_photo_file_id"] != nil || k["check_in_app_screenshot_file_id"] != nil) {
			t.Errorf("legacy task %s carries a check-in photo", k.Str("task_no"))
		}
	}
	if refs != 11 || legacyTrips != 5 || photos == 0 {
		t.Errorf("%d generated rows name their driver by auth uid (want 11: 5 tasks, 5 trips, 1 standby), %d legacy trips "+
			"(want 5) with %d photos", refs, legacyTrips, photos)
	}
}

// checkCreatedAtCheckIn: a driver creates its own task as part of the check-in (R63), so the task is created in the
// minutes before the check-in (never after it), and before the check-in photo it owns.
func checkCreatedAtCheckIn(t *testing.T, p *Plan) {
	t.Helper()
	objects := byID(p.Tables["file_objects"])
	n := 0
	for _, k := range generated(t, p, "tasks") {
		if k.Str("tenant_source") != "driver" || k.Str("check_in_at") == "" {
			continue
		}
		n++
		created := parseTS(t, k.Str("created_at"))
		if lead := parseTS(t, k.Str("check_in_at")).Sub(created); lead < 0 || lead > 5*time.Minute {
			t.Errorf("driver-created task %s: created %s, check-in %s: not created at its check-in", k.Str("task_no"),
				k.Str("created_at"), k.Str("check_in_at"))
		}
		if f := objects[k.Str("check_in_photo_file_id")]; f != nil && parseTS(t, f.Str("created_at")).Before(created) {
			t.Errorf("driver-created task %s: check-in photo taken %s, before the task", k.Str("task_no"), f.Str("created_at"))
		}
	}
	if n == 0 {
		t.Error("no driver-created task checked in")
	}
}

func userOf(p *Plan, driverID string) string {
	for _, d := range p.Tables["drivers"] {
		if d.Str("id") == driverID {
			return d.Str("user_id")
		}
	}
	return ""
}

// An upsert into a shared database prices the generated rows from the database's pricing inputs (repricedTables,
// Appendix D §D.1.7). With inputs equal to the plan's nothing changes. With a rate card the plan lacks (an admin priced
// one of the "need a rate card" routes on the staging VM), the generated trips of that route take it through the
// engine's oldest-entry fallback, every other generated row keeps its price, the smoke rows stay, and the plan itself
// is left as it was (Load writes the copies).
func TestRepricedTables(t *testing.T) {
	p := plan(t, ProfileDemo)
	in, err := planEngineInputs(p)
	if err != nil {
		t.Fatal(err)
	}
	same, err := repricedTables(p, in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(same, p.Tables) {
		t.Fatal("repricing from the plan's own inputs changed the plan's rows")
	}

	tasks, trips := byID(p.Tables["tasks"]), byID(p.Tables["trip_records"])
	var target Row
	for _, s := range generated(t, p, "trip_billing_snapshots") {
		k := tasks[trips[s.Str("trip_id")].Str("task_id")]
		if s.Str("unpriced_reason") == "no_rate" && k.Str("truck_type") != "" && k.Str("job_category") == "PRIMARY" {
			target = s
			break
		}
	}
	if target == nil {
		t.Fatal("the demo has no unpriced no_rate trip")
	}
	k := tasks[trips[target.Str("trip_id")].Str("task_id")]
	party, _ := compute.ResolveTaskParty(compute.TaskParties{BillingPartyID: k.Str("billing_party_id"),
		SourceLinkedPartyID: k.Str("source_linked_party_id"), DestinationLinkedPartyID: k.Str("destination_linked_party_id")})
	route := func(s Row) bool {
		kk := tasks[trips[s.Str("trip_id")].Str("task_id")]
		return s.Str("lookup_hub_code") == target.Str("lookup_hub_code") && s.Str("lookup_destination_code") ==
			target.Str("lookup_destination_code") && kk.Str("truck_type") == k.Str("truck_type") &&
			firstNonEmpty(kk.Str("billing_party_id"), kk.Str("source_linked_party_id"), kk.Str("destination_linked_party_id")) == party
	}
	card := compute.RateEntry{ID: "00000000-0000-7000-8000-0000000c0de1", CreatedAt: time.Date(2026, 10, 4, 9, 0, 0, 0, clock.Bangkok),
		PartyID: party, ImportID: "rc_1791090000000", HubCode: target.Str("lookup_hub_code"),
		DestinationCode: target.Str("lookup_destination_code"), VehicleClass: k.Str("truck_type"), RateTHB: 1234,
		EffectiveFrom: time.Date(2026, 10, 5, 0, 0, 0, 0, clock.Bangkok), JobCategory: compute.Primary}
	in.parties[party].rates = append(in.parties[party].rates, card)
	out, err := repricedTables(p, in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Tables, plan(t, ProfileDemo).Tables) {
		t.Fatal("repricedTables changed the plan")
	}
	for _, table := range []string{"trip_records", "trip_billing_snapshots", "trip_billing_stop_breakdown", "standby_records"} {
		if len(out[table]) != len(p.Tables[table]) {
			t.Errorf("%s: %d rows after repricing, %d before", table, len(out[table]), len(p.Tables[table]))
		}
	}
	before := map[string]Row{}
	for _, s := range p.Tables["trip_billing_snapshots"] {
		before[s.Str("trip_id")] = s
	}
	moved := 0
	for _, s := range out["trip_billing_snapshots"] {
		b := before[s.Str("trip_id")]
		switch {
		case b.Str("unpriced_reason") == "no_rate" && route(b):
			moved++
			if s.Str("rate_entry_id") != card.ID || s.Str("base_rate_thb") != "1234.00" || s["unpriced_reason"] != nil {
				t.Errorf("trip %s on the priced route: rate %s, base %s, reason %v", trips[s.Str("trip_id")].Str("trip_no"),
					s.Str("rate_entry_id"), s.Str("base_rate_thb"), s["unpriced_reason"])
			}
			if tr := tripOf(out, s.Str("trip_id")); tr.Str("billing_party_id") != party || tr.Str("billing_date") == "" {
				t.Errorf("trip %s: billing party %q, date %q after repricing", tr.Str("trip_no"), tr.Str("billing_party_id"), tr.Str("billing_date"))
			}
		case !reflect.DeepEqual(s, b):
			t.Errorf("trip %s off the route was repriced", trips[s.Str("trip_id")].Str("trip_no"))
		}
	}
	if moved == 0 {
		t.Error("no trip took the new rate card")
	}
}

func tripOf(tables map[string][]Row, id string) Row {
	for _, r := range tables["trip_records"] {
		if r.Str("id") == id {
			return r
		}
	}
	return nil
}

// Load prices an upsert's generated rows once every pricing input is in: each table the engine reads loads before the
// first table that carries a generated price.
func TestPricingInputsLoadFirst(t *testing.T) {
	first := len(LoadOrder)
	for i, table := range LoadOrder {
		if pricedTables[table] {
			first = min(first, i)
		}
	}
	for _, table := range pricingInputs {
		if i := slices.Index(LoadOrder, table); i < 0 || i >= first {
			t.Errorf("%s loads at %d, the first priced table at %d", table, i, first)
		}
	}
	for table := range pricedTables {
		if !slices.Contains(LoadOrder, table) {
			t.Errorf("priced table %s is not in the load order", table)
		}
	}
}
