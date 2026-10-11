//go:build integration

package drivers_test

import (
	"fmt"
	"net/http"
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
		driverUpdate,
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
