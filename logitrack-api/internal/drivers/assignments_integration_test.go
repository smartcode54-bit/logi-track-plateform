//go:build integration

package drivers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
)

// injection is a failure-injection trigger: it raises inside the request transaction on event (INSERT or UPDATE)
// of table when the condition holds.
type injection struct{ name, event, table, when string }

// failOn installs inj (as the table owner, logitrack_migrator); the returned function removes it.
func (w *world) failOn(inj injection) func() {
	w.t.Helper()
	w.ownerExec(`CREATE OR REPLACE FUNCTION test_injected_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected failure (T23 test)'; END $$`)
	w.ownerExec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE %s ON %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION test_injected_failure()`,
		inj.name, inj.event, inj.table, inj.when))
	return func() { w.ownerExec(fmt.Sprintf(`DROP TRIGGER %s ON %s`, inj.name, inj.table)) }
}

// state is what an assignment write may change: the driver's row, its rows of truck_assignments, its status entries
// and the assignment outbox events.
type state struct {
	status, current        string
	active, total, entries int
	created, revoked       int
}

func (w *world) state(driver string) state {
	w.t.Helper()
	return state{
		status:  w.id(`SELECT status FROM drivers WHERE id = $1`, driver),
		current: w.opt(`SELECT current_assignment_id::text FROM drivers WHERE id = $1`, driver),
		active:  w.count(`SELECT count(*) FROM truck_assignments WHERE driver_id = $1 AND status = 'active'`, driver),
		total:   w.count(`SELECT count(*) FROM truck_assignments WHERE driver_id = $1`, driver),
		entries: w.count(`SELECT count(*) FROM status_history WHERE entity_type = 'driver' AND entity_id = $1`, driver),
		created: w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'assignment.created' AND payload->>'driverId' = $1`, driver),
		revoked: w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'assignment.revoked' AND payload->>'driverId' = $1`, driver),
	}
}

// AC: assign and revoke are each one PostgreSQL transaction (truck-assignment/actions.client.ts:70-135 and 151-200):
// a failure injected at the driver update or at the outbox append leaves no partial state — no assignment row, no
// half-updated driver, no status entry, no event — and the same request then succeeds in full.
func TestTruckAssignmentIsAtomic(t *testing.T) {
	w := newWorld(t)
	drv := w.driver(w.O, "Atomic")
	tr := w.truck(w.O, "กก-2001")
	ta := w.login("ta.o")
	body := map[string]any{"truckId": tr, "driverId": drv}
	clean := w.state(drv)
	if clean.status != "active" || clean.current != "" || clean.total != 0 {
		t.Fatalf("fixture %+v", clean)
	}

	driverUpdate := injection{"t_fail_driver", "UPDATE", "drivers",
		"NEW.id = '" + drv + "' AND NEW.current_assignment_id IS DISTINCT FROM OLD.current_assignment_id"}
	for _, inject := range []injection{
		driverUpdate,
		{"t_fail_outbox", "INSERT", "outbox_events", "NEW.routing_key = 'assignment.created'"},
		{"t_fail_status", "INSERT", "status_history", "NEW.entity_id = '" + drv + "'"},
	} {
		drop := w.failOn(inject)
		expect(t, w.post("/v1/truck-assignments", ta, body), http.StatusInternalServerError, "internal")
		drop()
		if got := w.state(drv); got != clean {
			t.Fatalf("%s: partial state %+v, want %+v", inject.name, got, clean)
		}
	}

	r := w.post("/v1/truck-assignments", ta, body)
	expect(t, r, http.StatusCreated, "")
	asg := str(r.data()["id"])
	if r.data()["status"] != "active" || r.data()["truckPlate"] != "กก-2001" || r.data()["driverName"] != "Atomic ดี" ||
		r.data()["tenantId"] != w.O || r.data()["adminLabel"] != "ta.o@example.test" {
		t.Errorf("assignment %s", r.raw)
	}
	assigned := w.state(drv)
	if assigned.status != "on_duty" || assigned.current != asg || assigned.active != 1 || assigned.entries != 1 || assigned.created != 1 {
		t.Fatalf("after the assignment %+v", assigned)
	}
	if v := w.id(`SELECT tenant_source FROM truck_assignments WHERE id = $1`, asg); v != "truck" {
		t.Errorf("tenant_source %s", v)
	}

	for _, inject := range []injection{
		driverUpdate, // the release runs in assignment_release_driver (0013): its failure rolls the revoke back too
		{"t_fail_status", "INSERT", "status_history", "NEW.entity_id = '" + drv + "'"},
		{"t_fail_outbox", "INSERT", "outbox_events", "NEW.routing_key = 'assignment.revoked'"},
	} {
		drop := w.failOn(inject)
		expect(t, w.post("/v1/truck-assignments/"+asg+"/revoke", ta, nil), http.StatusInternalServerError, "internal")
		drop()
		if got := w.state(drv); got != assigned {
			t.Fatalf("%s: partial revoke %+v, want %+v", inject.name, got, assigned)
		}
	}
	r = w.post("/v1/truck-assignments/"+asg+"/revoke", ta, nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["status"] != "revoked" || r.data()["revokedAt"] == nil {
		t.Errorf("revoked %s", r.raw)
	}
	if got := w.state(drv); got.status != "active" || got.current != "" || got.active != 0 || got.entries != 2 || got.revoked != 1 {
		t.Errorf("after the revoke %+v", got)
	}
	r = w.post("/v1/truck-assignments/"+asg+"/revoke", ta, nil)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "not_active" {
		t.Errorf("%s", r.raw)
	}
}

// Two assignments of one driver at the same moment: the driver row serialises them, exactly one wins.
func TestTruckAssignmentConcurrent(t *testing.T) {
	w := newWorld(t)
	drv := w.driver(w.O, "Race")
	trucks := []string{w.truck(w.O, "กก-3001"), w.truck(w.O, "กก-3002"), w.truck(w.O, "กก-3003"), w.truck(w.O, "กก-3004")}
	ta := w.login("ta.o")
	var mu sync.Mutex
	codes := map[int]int{}
	var wg sync.WaitGroup
	for _, tr := range trucks {
		wg.Go(func() {
			r := w.post("/v1/truck-assignments", ta, map[string]any{"truckId": tr, "driverId": drv})
			mu.Lock()
			codes[r.status]++
			mu.Unlock()
		})
	}
	wg.Wait()
	if codes[http.StatusCreated] != 1 || codes[http.StatusConflict] != len(trucks)-1 {
		t.Fatalf("answers %v", codes)
	}
	if n := w.count(`SELECT count(*) FROM truck_assignments WHERE driver_id = $1 AND status = 'active'`, drv); n != 1 {
		t.Errorf("%d active assignments", n)
	}
}

// The rules of an assignment: an inactive driver, a driver that already holds one (several drivers may share a truck),
// a truck outside the caller's reach, and a driver and truck of two unrelated tenants are refused; a driver of a
// carrier working for the truck's tenant is not. Reads: the caller's reach, filters, keyset pages; the capabilities
// fleet:view_assignments and fleet:manage_assignments.
func TestTruckAssignmentRules(t *testing.T) {
	w := newWorld(t)
	ta, op, us := w.login("ta.o"), w.login("op.o"), w.login("us.o")
	tO, tA, tB, tC := w.truck(w.O, "กข-4001"), w.truck(w.A, "กข-4002"), w.truck(w.B, "กข-4003"), w.truck(w.C, "กข-4004")
	dO, dO2, dA := w.driver(w.O, "Own"), w.driver(w.O, "Own2"), w.driver(w.A, "CarrierA")
	inactive := w.driver(w.O, "Gone", "status", "'inactive'")

	r := w.post("/v1/truck-assignments", ta, map[string]any{"truckId": tO, "driverId": inactive})
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "driver_inactive" {
		t.Errorf("%s", r.raw)
	}
	r = w.post("/v1/truck-assignments", ta, map[string]any{"truckId": tC, "driverId": dO})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["truckId"] != "not_found" {
		t.Errorf("%s", r.raw)
	}
	r = w.post("/v1/truck-assignments", ta, map[string]any{"truckId": tB, "driverId": dA})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["driverId"] != "not_in_truck_tenant" {
		t.Errorf("%s", r.raw)
	}
	r = w.post("/v1/truck-assignments", ta, map[string]any{"truckId": "x"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["truckId"] != "invalid" || f["driverId"] != "required" {
		t.Errorf("%s", r.raw)
	}
	// The operator manages assignments (TA MG OS OP); the user role reads none.
	r = w.post("/v1/truck-assignments", op, map[string]any{"truckId": tO, "driverId": dA})
	expect(t, r, http.StatusCreated, "")
	if r.data()["tenantId"] != w.O {
		t.Errorf("the row takes the truck's tenant: %s", r.raw)
	}
	expect(t, w.post("/v1/truck-assignments", ta, map[string]any{"truckId": tO, "driverId": dO}), http.StatusCreated, "")
	r = w.post("/v1/truck-assignments", ta, map[string]any{"truckId": tA, "driverId": dO})
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "driver_assigned" || r.detail("assignmentId") == nil {
		t.Errorf("%s", r.raw)
	}
	expect(t, w.post("/v1/truck-assignments", ta, map[string]any{"truckId": tA, "driverId": dO2}), http.StatusCreated, "")
	r = w.get("/v1/truck-assignments", us)
	expect(t, r, http.StatusForbidden, "permission_denied")
	if missing(r) != "fleet:view_assignments" {
		t.Errorf("%s", r.raw)
	}

	r = w.get("/v1/truck-assignments", ta)
	expect(t, r, http.StatusOK, "")
	if len(r.list()) != 3 {
		t.Fatalf("own fleet reads %d", len(r.list()))
	}
	r = w.get("/v1/truck-assignments?truckId="+tO, ta)
	if len(r.list()) != 2 {
		t.Errorf("by truck %d", len(r.list()))
	}
	r = w.get("/v1/truck-assignments?driverId="+dO2+"&status=active", ta)
	if len(r.list()) != 1 || r.list()[0]["truckId"] != tA {
		t.Errorf("by driver %s", r.raw)
	}
	// Pages of one.
	seen := map[string]bool{}
	cursor := ""
	for {
		path := "/v1/truck-assignments?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r = w.get(path, ta)
		expect(t, r, http.StatusOK, "")
		for _, it := range r.list() {
			seen[str(it["id"])] = true
		}
		if cursor = r.next(); cursor == "" {
			break
		}
	}
	if len(seen) != 3 {
		t.Errorf("paged %d", len(seen))
	}
	// Carrier A reads only the rows of its trucks (the truck of the own fleet holds A's driver: not A's row).
	r = w.get("/v1/truck-assignments", w.login("ta.a"))
	if len(r.list()) != 1 || r.list()[0]["truckId"] != tA {
		t.Errorf("carrier A reads %s", r.raw)
	}
	for _, q := range []string{"status=x", "truckId=x", "cursor=x"} {
		expect(t, w.get("/v1/truck-assignments?"+q, ta), http.StatusUnprocessableEntity, "invalid_argument")
	}
	expect(t, w.post("/v1/truck-assignments/019a0000-0000-7000-8000-0000000000aa/revoke", ta, nil), http.StatusNotFound, "not_found")
}

// TW8: q is the history's search on the server. It matches a case-insensitive substring of the four columns the
// page shows (plate, model, driver name as the Thai full name or first + last, admin label), combines with status,
// follows the keyset across pages (newest first), ignores surrounding blanks, and refuses more than 100 characters.
func TestTruckAssignmentSearch(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	t1, t2 := w.truck(w.O, "กข-5101"), w.truck(w.O, "ขค-5202")
	w.exec(`UPDATE trucks SET model = 'FRR 210' WHERE id = $1`, t2)
	d1, d2 := w.driver(w.O, "Somchai"), w.driver(w.O, "Mana")
	d3 := w.driver(w.O, "Preecha", "full_name_th", "'ปรีชา ใจดี'")
	rows := map[string]string{}
	for _, p := range [][2]string{{t1, d1}, {t2, d2}, {t1, d3}} {
		r := w.post("/v1/truck-assignments", ta, map[string]any{"truckId": p[0], "driverId": p[1]})
		expect(t, r, http.StatusCreated, "")
		rows[p[1]] = str(r.data()["id"])
	}
	w.exec(`UPDATE truck_assignments SET admin_label = 'Night Desk' WHERE id = $1`, rows[d2])

	// drivers walks every page of the query and returns the drivers of the rows in order.
	drivers := func(query string) []string {
		t.Helper()
		var out []string
		cursor := ""
		for {
			path := "/v1/truck-assignments?" + query
			if cursor != "" {
				path += "&cursor=" + url.QueryEscape(cursor)
			}
			r := w.get(path, ta)
			expect(t, r, http.StatusOK, "")
			for _, it := range r.list() {
				out = append(out, str(it["driverId"]))
			}
			if cursor = r.next(); cursor == "" {
				return out
			}
		}
	}
	q := func(v string) string { return "q=" + url.QueryEscape(v) }
	for _, c := range []struct {
		query string
		want  []string
	}{
		{q("5101"), []string{d3, d1}},              // plate, newest first
		{q("frr"), []string{d2}},                   // model, any case
		{q("SOMCHAI dee"), []string{d1}},           // first + last name
		{q("ใจดี"), []string{d3}},                  // Thai full name
		{q("night"), []string{d2}},                 // admin label
		{q("  5202 "), []string{d2}},               // surrounding blanks trimmed
		{q("   "), []string{d3, d2, d1}},           // blank: no filter
		{q("nobody"), nil},                         // no match: an empty page
		{q("5101") + "&limit=1", []string{d3, d1}}, // keyset pages of one keep the filter
	} {
		if got := drivers(c.query); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.query, got, c.want)
		}
	}
	expect(t, w.post("/v1/truck-assignments/"+rows[d1]+"/revoke", ta, nil), http.StatusOK, "")
	if got := drivers(q("5101") + "&status=active"); !slices.Equal(got, []string{d3}) {
		t.Errorf("q with status: %v", got)
	}
	if got := drivers(q("5101") + "&status=revoked"); !slices.Equal(got, []string{d1}) {
		t.Errorf("q with status revoked: %v", got)
	}
	r := w.get("/v1/truck-assignments?"+q(strings.Repeat("ก", 101)), ta)
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["q"] != "too_long" {
		t.Errorf("%s", r.raw)
	}
	expect(t, w.get("/v1/truck-assignments?"+q(strings.Repeat("ก", 100)), ta), http.StatusOK, "")
}

// AC: activeTruck (the truck of the running job), currentAssignment (the home truck) and tasks.truck_id (the truck of
// one job) stay three separate concepts: an assignment and its revoke write only the home truck, the driver payload
// shows the two driver-side concepts apart, and the drivers routes write neither.
func TestThreeTruckConcepts(t *testing.T) {
	w := newWorld(t)
	running, job, home := w.truck(w.O, "กค-5001"), w.truck(w.O, "กค-5002"), w.truck(w.O, "กค-5003")
	drv := w.driver(w.O, "Three")
	task := w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		driver_id, truck_id) VALUES ($1, 'form', 'first_mile', 'checked_in', now(), 'SRC', 'DST', $2, $3) RETURNING id::text`,
		w.O, drv, job)
	w.exec(`UPDATE drivers SET active_truck_id = $1, active_truck_plate = 'กค-5001', active_task_id = $2,
		active_started_at = now() WHERE id = $3`, running, task, drv)
	ta := w.login("ta.o")

	r := w.post("/v1/truck-assignments", ta, map[string]any{"truckId": home, "driverId": drv})
	expect(t, r, http.StatusCreated, "")
	asg := str(r.data()["id"])
	concepts := func() (active, plate, taskTruck, current string) {
		return w.opt(`SELECT active_truck_id::text FROM drivers WHERE id = $1`, drv),
			w.opt(`SELECT active_truck_plate FROM drivers WHERE id = $1`, drv),
			w.opt(`SELECT truck_id::text FROM tasks WHERE id = $1`, task),
			w.opt(`SELECT current_assignment_id::text FROM drivers WHERE id = $1`, drv)
	}
	if a, p, tt, c := concepts(); a != running || p != "กค-5001" || tt != job || c != asg {
		t.Fatalf("after the assignment: active %s %s, task %s, current %s", a, p, tt, c)
	}
	r = w.get("/v1/drivers/"+drv, ta)
	expect(t, r, http.StatusOK, "")
	at, _ := r.data()["activeTruck"].(map[string]any)
	ca, _ := r.data()["currentAssignment"].(map[string]any)
	if at == nil || at["truckId"] != running || at["truckPlate"] != "กค-5001" || at["taskId"] != task ||
		ca == nil || ca["id"] != asg || ca["truckId"] != home || ca["truckPlate"] != "กค-5003" {
		t.Fatalf("driver payload %s", r.raw)
	}
	r = w.get("/v1/trucks/"+home, ta)
	expect(t, r, http.StatusOK, "")
	if cur := r.data()["currentAssignments"].([]any); len(cur) != 1 || cur[0].(map[string]any)["driverId"] != drv {
		t.Errorf("truck's current assignments %v", cur)
	}
	for _, body := range []map[string]any{
		{"activeTruck": map[string]any{"truckId": home}}, {"currentAssignment": nil}, {"currentAssignmentId": asg},
	} {
		expect(t, w.patch("/v1/drivers/"+drv, ta, body), http.StatusUnprocessableEntity, "invalid_argument")
	}
	expect(t, w.patch("/v1/drivers/"+drv, ta, map[string]any{"status": "on_duty", "mobile": "0890000000"}), http.StatusOK, "")
	expect(t, w.post("/v1/truck-assignments/"+asg+"/revoke", ta, nil), http.StatusOK, "")
	if a, p, tt, c := concepts(); a != running || p != "กค-5001" || tt != job || c != "" {
		t.Errorf("after the revoke: active %s %s, task %s, current %s", a, p, tt, c)
	}
	r = w.get("/v1/drivers/"+drv, ta)
	if r.data()["currentAssignment"] != nil || r.data()["activeTruck"] == nil {
		t.Errorf("after the revoke %s", r.raw)
	}
}

// Review of PR #127: a row carries its truck's tenant, so the driver's side of an assignment may lie outside what the
// writer reads, and the rules still hold for the driver whatever tenant its rows are in.
//  1. The own fleet's truck holds a carrier's driver (O's row, invisible to the carrier): the carrier's staff get 409
//     driver_assigned for a second truck, as the legacy page refused a driver with a currentAssignment.
//  2. A revoke moves the home truck to the driver's active row in a tenant the revoker cannot read, instead of freeing
//     the driver.
//  3. The carrier revoking the row of its truck that binds an own-fleet driver releases that driver, which it cannot
//     read, in the same transaction (status entry with the carrier's admin as actor, event on both fleets), so the
//     driver is free for a broker-carrier move.
func TestTruckAssignmentAcrossTenants(t *testing.T) {
	w := newWorld(t)
	taO, taA, mgA := w.login("ta.o"), w.login("ta.a"), w.login("mg.a")
	tO, tA, tA2 := w.truck(w.O, "กง-6001"), w.truck(w.A, "กง-6002"), w.truck(w.A, "กง-6003")
	dA, dO := w.driver(w.A, "Carried"), w.driver(w.O, "Home")

	r := w.post("/v1/truck-assignments", taO, map[string]any{"truckId": tO, "driverId": dA})
	expect(t, r, http.StatusCreated, "")
	rowO := str(r.data()["id"])
	if r.data()["tenantId"] != w.O || len(w.get("/v1/truck-assignments?driverId="+dA, taA).list()) != 0 {
		t.Fatalf("O's row is not hidden from A: %s", r.raw)
	}
	for _, who := range []string{taA, mgA} {
		r = w.post("/v1/truck-assignments", who, map[string]any{"truckId": tA, "driverId": dA})
		expect(t, r, http.StatusConflict, "failed_precondition")
		if r.detail("reason") != "driver_assigned" || r.detail("assignmentId") != rowO {
			t.Errorf("a second truck for a driver O's truck holds: %s", r.raw)
		}
	}
	held := w.state(dA)
	if held.status != "on_duty" || held.current != rowO || held.active != 1 || held.entries != 1 {
		t.Fatalf("after the carrier's attempts %+v", held)
	}

	// Loaded data: a second active row, A's, is the home truck; A revokes it.
	rowA := w.id(`INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status, created_at)
		VALUES ($1, 'truck', $2, $3, 'active', now() - interval '1 day') RETURNING id::text`, w.A, tA2, dA)
	w.exec(`UPDATE drivers SET current_assignment_id = $1 WHERE id = $2`, rowA, dA)
	expect(t, w.post("/v1/truck-assignments/"+rowA+"/revoke", taA, nil), http.StatusOK, "")
	if got := w.state(dA); got.status != "on_duty" || got.current != rowO || got.active != 1 || got.entries != held.entries {
		t.Fatalf("A revoked its row while O's is active: %+v", got)
	}
	expect(t, w.post("/v1/truck-assignments/"+rowO+"/revoke", taO, nil), http.StatusOK, "")
	if got := w.state(dA); got.status != "active" || got.current != "" || got.active != 0 || got.entries != held.entries+1 {
		t.Fatalf("after O's revoke %+v", got)
	}
	expect(t, w.post("/v1/truck-assignments", taA, map[string]any{"truckId": tA, "driverId": dA}), http.StatusCreated, "")

	r = w.post("/v1/truck-assignments", taO, map[string]any{"truckId": tA2, "driverId": dO})
	expect(t, r, http.StatusCreated, "")
	rowAO := str(r.data()["id"])
	if r.data()["tenantId"] != w.A {
		t.Fatalf("the row takes the truck's tenant: %s", r.raw)
	}
	expect(t, w.get("/v1/drivers/"+dO, taA), http.StatusNotFound, "not_found")
	r = w.post("/v1/truck-assignments/"+rowAO+"/revoke", taA, nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["status"] != "revoked" || r.data()["driverName"] != nil {
		t.Errorf("revoked %s", r.raw)
	}
	if got := w.state(dO); got.status != "active" || got.current != "" || got.active != 0 || got.entries != 2 || got.revoked != 1 {
		t.Fatalf("the own-fleet driver after the carrier's revoke %+v", got)
	}
	if n := w.count(`SELECT count(*) FROM status_history h JOIN users u ON u.id = h.changed_by_user_id
		WHERE h.entity_type = 'driver' AND h.entity_id = $1 AND h.status = 'active' AND h.previous_status = 'on_duty'
		  AND u.email = 'ta.a@example.test' AND h.changed_by_label = 'ta.a@example.test'
		  AND h.reason = 'truck assignment revoked'`, dO); n != 1 {
		t.Errorf("status entries of the release: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'assignment.revoked' AND aggregate_id = $1
		AND realtime_topics @> ARRAY['tenant:' || $2 || ':fleet', 'tenant:' || $3 || ':fleet']`, rowAO, w.A, w.O); n != 1 {
		t.Errorf("assignment.revoked on both fleets: %d", n)
	}
	r = w.get("/v1/drivers/"+dO, taO)
	if r.data()["currentAssignment"] != nil || r.data()["status"] != "active" {
		t.Errorf("driver after the release %s", r.raw)
	}
	expect(t, w.patch("/v1/drivers/"+dO, taO, map[string]any{"tenantId": w.B}), http.StatusOK, "")
}

// A current_assignment_id naming a row that is no longer active (loaded data) is no home truck: the driver payload
// does not show it, revoking the driver's real active row (which it does not name) frees the driver, and the
// broker-carrier move clears it (with on_duty back to active) instead of refusing driver_holds_truck.
func TestStaleCurrentAssignment(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")
	tr := w.truck(w.O, "กจ-7001")
	stale := func(first string) (driver, row string) {
		driver = w.driver(w.O, first, "status", "'on_duty'")
		row = w.id(`INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status, revoked_at)
			VALUES ($1, 'truck', $2, $3, 'revoked', now()) RETURNING id::text`, w.O, tr, driver)
		w.exec(`UPDATE drivers SET current_assignment_id = $1 WHERE id = $2`, row, driver)
		return driver, row
	}

	d1, _ := stale("Stale")
	r := w.get("/v1/drivers/"+d1, taO)
	expect(t, r, http.StatusOK, "")
	if r.data()["currentAssignment"] != nil {
		t.Errorf("a revoked row shows as the home truck: %s", r.raw)
	}
	r = w.get("/v1/drivers?q=Stale", taO)
	if len(r.list()) != 1 || r.list()[0]["currentAssignment"] != nil {
		t.Errorf("list %s", r.raw)
	}
	legacy := w.id(`INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status)
		VALUES ($1, 'truck', $2, $3, 'active') RETURNING id::text`, w.O, tr, d1)
	expect(t, w.post("/v1/truck-assignments/"+legacy+"/revoke", taO, nil), http.StatusOK, "")
	if got := w.state(d1); got.status != "active" || got.current != "" || got.active != 0 || got.entries != 1 {
		t.Fatalf("after revoking the row the pointer does not name %+v", got)
	}

	d2, row2 := stale("Mover")
	r = w.patch("/v1/drivers/"+d2, taO, map[string]any{"tenantId": w.B})
	expect(t, r, http.StatusOK, "")
	if r.data()["tenantId"] != w.B || r.data()["status"] != "active" || r.data()["currentAssignment"] != nil {
		t.Errorf("moved %s", r.raw)
	}
	if got := w.state(d2); got.status != "active" || got.current != "" || got.entries != 1 {
		t.Errorf("after the move %+v", got)
	}
	if n := w.count(`SELECT count(*) FROM status_history WHERE entity_type = 'driver' AND entity_id = $1
		AND status = 'active' AND previous_status = 'on_duty' AND changed_by_label = 'ta.o@example.test'`, d2); n != 1 {
		t.Errorf("status entry of the move: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM security_events WHERE event_type = 'driver_tenant_moved'
		AND details->>'clearedAssignmentId' = $1`, row2); n != 1 {
		t.Errorf("driver_tenant_moved names the cleared assignment: %d", n)
	}
}
