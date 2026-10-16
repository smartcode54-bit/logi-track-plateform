//go:build integration

package expenses_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// AC "Legacy maintenance statuses normalised to lower_snake": the legacy literals are accepted on input and every
// record, payload and filter holds the canonical lower_snake status. The truck follows the record in one transaction:
// in the shop while in_progress, back to active with its service date and mileages on completion.
func TestMaintenanceStatuses(t *testing.T) {
	w := newWorld(t)
	op := w.login("op.o") // operator: fleet:manage_maintenance
	r := w.post("/v1/maintenance", op, map[string]any{"truckId": w.tO2, "type": "pm", "serviceType": "เช็คระยะ 10,000",
		"startDate": "2026-10-11", "status": "PM Booking", "currentMileage": 49000, "nextServiceMileage": 50000})
	expect(t, r, http.StatusCreated, "")
	id := str(r.data()["id"])
	if r.data()["status"] != "pm_booking" || r.data()["type"] != "PM" || r.data()["licensePlate"] != "กข-1002" ||
		r.data()["truckStatus"] != "active" {
		t.Fatalf("created %s", r.raw)
	}
	if w.count(`SELECT count(*) FROM trucks WHERE id = $1 AND active_maintenance_id = $2 AND status = 'active'`, w.tO2, id) != 1 {
		t.Fatal("a booking becomes the truck's active maintenance, the truck stays active")
	}
	if w.events("maintenance.created") != 1 || w.events("maintenance.reminder_requested") != 0 {
		t.Error("events after the booking")
	}
	r = w.patch("/v1/maintenance/"+id, op, map[string]any{"status": "Scheduled", "appointmentTime": "09:30", "provider": "อู่ A"})
	if r.data()["status"] != "scheduled" || w.events("maintenance.reminder_requested") != 1 || w.events("maintenance.updated") != 1 {
		t.Fatalf("scheduled %s (reminders %d)", r.raw, w.events("maintenance.reminder_requested"))
	}
	if r = w.get("/v1/maintenance?status=Scheduled", op); len(r.list()) != 1 {
		t.Fatalf("filter by the legacy literal: %s", r.raw)
	}
	r = w.patch("/v1/maintenance/"+id, op, map[string]any{"status": "In-Progress"})
	if r.data()["status"] != "in_progress" || r.data()["truckStatus"] != "maintenance" {
		t.Fatalf("in progress %s", r.raw)
	}
	if *w.str(`SELECT status FROM maintenance_records WHERE id = $1`, id) != "in_progress" {
		t.Fatal("not stored lower_snake")
	}
	if w.count(`SELECT count(*) FROM status_history WHERE entity_type = 'truck' AND entity_id = $1 AND status = 'maintenance'
		AND previous_status = 'active'`, w.tO2) != 1 {
		t.Error("the truck's status change is not in its history")
	}
	r = w.patch("/v1/maintenance/"+id, op, map[string]any{"status": "pm_booking"})
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "invalid_transition" || r.detail("from") != "in_progress" || r.detail("to") != "pm_booking" {
		t.Errorf("details %v", r.body)
	}
	r = w.patch("/v1/maintenance/"+id, op, map[string]any{"status": "completed", "endDate": "2026-10-12",
		"currentMileage": 49500, "nextServiceMileage": 60000, "totalCostThb": 2500.5})
	expect(t, r, http.StatusOK, "")
	if r.data()["status"] != "completed" || r.data()["truckStatus"] != "active" || num(r.data()["displayCostThb"]) != 2500.5 {
		t.Fatalf("completed %s", r.raw)
	}
	if w.count(`SELECT count(*) FROM trucks WHERE id = $1 AND status = 'active' AND active_maintenance_id IS NULL
		AND last_alert_mileage IS NULL AND last_service_date = '2026-10-12' AND current_mileage = 49500
		AND next_service_mileage = 60000`, w.tO2) != 1 {
		t.Fatal("completion did not reach the truck")
	}
	expect(t, w.patch("/v1/maintenance/"+id, op, map[string]any{"status": "in_progress"}), http.StatusConflict, "failed_precondition")
	// A completed record's correction re-applies the completion fields (the legacy form did on every save).
	expect(t, w.patch("/v1/maintenance/"+id, op, map[string]any{"endDate": "2026-10-13"}), http.StatusOK, "")
	if w.count(`SELECT count(*) FROM trucks WHERE id = $1 AND last_service_date = '2026-10-13'`, w.tO2) != 1 {
		t.Error("the corrected end date did not reach the truck")
	}
	// Status filters take only the vocabulary.
	expect(t, w.get("/v1/maintenance?status=Done", op), http.StatusUnprocessableEntity, "invalid_argument")
}

// One transaction for the record and the truck; RBAC and reach; reminders; files; pages.
func TestMaintenanceRecords(t *testing.T) {
	w := newWorld(t)
	op := w.login("op.o")
	r := w.post("/v1/maintenance", op, map[string]any{"truckId": w.tO, "type": "CM", "serviceType": "ยางแตก",
		"startDate": "2026-10-11"})
	expect(t, r, http.StatusCreated, "")
	first := str(r.data()["id"])
	if r.data()["status"] != "in_progress" || r.data()["truckStatus"] != "maintenance" {
		t.Fatalf("the form's default status puts the truck in the shop: %s", r.raw)
	}
	// A second open record of the truck leaves the first one active.
	r = w.post("/v1/maintenance", op, map[string]any{"truckId": w.tO, "type": "PM", "serviceType": "เช็คระยะ",
		"startDate": "2026-10-20", "status": "scheduled", "pickupAppointmentAt": "2026-10-20T17:00"})
	second := str(r.data()["id"])
	if r.data()["pickupAppointmentAt"] != "2026-10-20T10:00:00Z" {
		t.Errorf("pickup %v", r.data()["pickupAppointmentAt"])
	}
	if w.count(`SELECT count(*) FROM trucks WHERE id = $1 AND active_maintenance_id = $2`, w.tO, first) != 1 {
		t.Fatal("the active maintenance moved")
	}
	// Cancelling the record in the shop returns the truck and hands it to nobody else's record.
	r = w.patch("/v1/maintenance/"+first, op, map[string]any{"status": "cancelled"})
	if r.data()["truckStatus"] != "active" {
		t.Fatalf("cancelled %s", r.raw)
	}
	if w.count(`SELECT count(*) FROM trucks WHERE id = $1 AND active_maintenance_id IS NULL`, w.tO) != 1 {
		t.Error("the cancelled record is still the active one")
	}

	// Reminders: the truck's driver (dO has tO), never a closed record or a truck without a driver.
	r = w.post("/v1/maintenance/"+second+"/remind", op, map[string]any{"title": "นัดเช็คระยะ", "body": "พรุ่งนี้ 09:00"})
	expect(t, r, http.StatusAccepted, "")
	if r.data()["eventId"] == nil || w.events("maintenance.reminder_requested") != 1 {
		t.Fatalf("remind %s", r.raw)
	}
	var payload map[string]any
	var raw []byte
	if err := w.etl.QueryRow(t.Context(), `SELECT payload FROM outbox_events WHERE routing_key = 'maintenance.reminder_requested'`).
		Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"] != second || payload["title"] != "นัดเช็คระยะ" || payload["body"] != "พรุ่งนี้ 09:00" || payload["truckId"] != w.tO {
		t.Errorf("payload %v", payload)
	}
	r = w.post("/v1/maintenance/"+first+"/remind", op, nil)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "not_open" {
		t.Errorf("closed: %v", r.body)
	}
	noDriver := str(w.post("/v1/maintenance", op, map[string]any{"truckId": w.tO2, "type": "PM", "serviceType": "x",
		"startDate": "2026-10-11", "status": "scheduled"}).data()["id"])
	r = w.post("/v1/maintenance/"+noDriver+"/remind", op, nil)
	if r.status != http.StatusConflict || r.detail("reason") != "no_driver" {
		t.Errorf("no driver: %s", r.raw)
	}
	// The maintenance.created of a record created scheduled carries the truck's driver topic.
	var topics []string
	if err := w.etl.QueryRow(t.Context(), `SELECT realtime_topics FROM outbox_events WHERE routing_key = 'maintenance.created'
		AND aggregate_id = $1`, second).Scan(&topics); err != nil {
		t.Fatal(err)
	}
	if len(topics) != 2 || topics[0] != "tenant:"+w.O+":fleet" || topics[1] != "driver:"+w.dO {
		t.Errorf("topics %v", topics)
	}

	// Files: maintenance_file uploads by kind, the keys back in the payload, a key the record links kept.
	img := w.upload(op, "maintenance_file", second, "image/jpeg", "image", jpeg)
	inv := w.upload(op, "maintenance_file", second, "image/jpeg", "invoice", jpeg)
	r = w.patch("/v1/maintenance/"+second, op, map[string]any{"files": map[string]any{"images": []string{img}, "invoices": []string{inv}}})
	files, _ := r.data()["files"].(map[string]any)
	if imgs, _ := files["images"].([]any); len(imgs) != 1 || imgs[0] != img {
		t.Fatalf("files %s", r.raw)
	}
	r = w.patch("/v1/maintenance/"+second, w.login("mg.o"), map[string]any{"files": map[string]any{"images": []string{img}}})
	expect(t, r, http.StatusOK, "") // a key the record links stays linked, whoever sends it back
	r = w.patch("/v1/maintenance/"+second, op, map[string]any{"files": map[string]any{"photos": []string{img}}})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")

	// RBAC and reach: a user has no fleet:manage_maintenance; another carrier cannot see the record; the driver's
	// fields and the truck are not editable.
	expect(t, w.get("/v1/maintenance", w.login("us.o")), http.StatusForbidden, "permission_denied")
	expect(t, w.patch("/v1/maintenance/"+second, w.login("ta.c"), map[string]any{"notes": "x"}), http.StatusNotFound, "not_found")
	if r = w.get("/v1/maintenance", w.login("ta.c")); len(r.list()) != 0 {
		t.Fatalf("carrier C sees %d records", len(r.list()))
	}
	r = w.patch("/v1/maintenance/"+second, op, map[string]any{"truckId": w.tO2, "checkInAt": "2026-10-11T00:00:00Z"})
	if r.fields()["truckId"] != "not_editable" || r.fields()["checkInAt"] != "not_editable" {
		t.Errorf("violations %v", r.fields())
	}
	r = w.post("/v1/maintenance", op, map[string]any{"truckId": w.tC, "type": "PM", "serviceType": "x", "startDate": "2026-10-11"})
	if r.fields()["truckId"] != "not_found" {
		t.Errorf("a truck outside the reach: %s", r.raw)
	}
	// Own-fleet staff manage a carrier's truck (contractor reach); the carrier's own staff too.
	expect(t, w.post("/v1/maintenance", op, map[string]any{"truckId": w.tA, "type": "PM", "serviceType": "x",
		"startDate": "2026-10-11", "status": "scheduled"}), http.StatusCreated, "")
	if r = w.get("/v1/maintenance", w.login("ta.a")); len(r.list()) != 1 {
		t.Fatalf("carrier A sees %d records", len(r.list()))
	}

	// Pages and counts.
	for i := range 60 {
		w.exec(`INSERT INTO maintenance_records (tenant_id, truck_id, maintenance_type, service_type, start_date, status,
			created_at) VALUES ($1, $2, 'CM', 'x', '2026-01-01', 'completed', now() - make_interval(hours => $3::int))`, w.O, w.tO2, i)
	}
	r = w.get("/v1/maintenance?truckId="+w.tO2+"&limit=50", op)
	counts := r.meta()["statusCounts"].(map[string]any)
	if len(r.list()) != 50 || r.next() == "" || num(counts["completed"]) != 60 || num(counts["scheduled"]) != 1 {
		t.Fatalf("page 1: %d rows, counts %v", len(r.list()), counts)
	}
	r = w.get("/v1/maintenance?truckId="+w.tO2+"&limit=50&cursor="+r.next(), op)
	if len(r.list()) != 11 || r.next() != "" || r.meta() != nil {
		t.Fatalf("page 2: %d rows", len(r.list()))
	}
	if r = w.get("/v1/maintenance?type=cm&truckId="+w.tO2, op); len(r.list()) != 50 {
		t.Fatalf("type filter %d", len(r.list()))
	}
}

// Quarantine is not a tenant destination; platform-only principals have no maintenance capability.
// TestMaintenanceQuarantineReview in internal/iam covers its header-free review DTO and snapshot plate.
func TestMaintenanceQuarantinedRecord(t *testing.T) {
	w := newWorld(t)
	id := w.id(`INSERT INTO maintenance_records (tenant_id, tenant_source, truck_id, truck_license_plate_snapshot,
		maintenance_type, service_type, start_date, status, legacy_doc_id)
		VALUES (app_quarantine_tenant_id(), 'quarantine', NULL, 'zz-9999', 'CM', 'เบรก', '2026-09-02', 'in_progress', 'mnt2')
		RETURNING id::text`)
	r := w.do(http.MethodGet, "/v1/maintenance", w.login("pa"), nil, "X-Act-On-Tenant", "*")
	// This branch refuses the wildcard at authorization; P1.1 supplies D1 header closure.
	expect(t, r, http.StatusForbidden, "permission_denied")
	r = w.do(http.MethodPatch, "/v1/maintenance/"+id, w.login("pa"), map[string]any{"serviceType": "changed"},
		"X-Act-On-Tenant", "*")
	expect(t, r, http.StatusForbidden, "permission_denied")
	if n := w.count(`SELECT count(*) FROM maintenance_records WHERE id = $1 AND service_type = 'เบรก'`, id); n != 1 {
		t.Fatalf("a refused header request changed the quarantined record: %d rows", n)
	}
	for _, m := range w.get("/v1/maintenance", w.login("op.o")).list() {
		if m["id"] == id {
			t.Fatal("a tenant lists a quarantined record")
		}
	}
}
