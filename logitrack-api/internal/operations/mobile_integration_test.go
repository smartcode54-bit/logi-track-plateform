//go:build integration

package operations_test

import (
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The addDeliveryStop shim target POST /v1/mobile/tasks/{id}/stops (T32, issue #52; Appendix B §B.2.21, §B.2.22) as
// the driver acting as itself, behind the capability guard of the route (the credential check, auth.RequireShim, is
// covered with real cf_shim keys and Firebase ID tokens in internal/iam). PostgreSQL-owned path.
func TestDriverAddsStops(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	d := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String()}))
	id := d["id"].(string)
	path := "/v1/mobile/tasks/" + id + "/stops"
	// A hub linked to customer Y: a stop at it takes Y as its party (the driver never names one).
	hubY := r.id(`INSERT INTO hubs (source_id, name_th, network, linked_party_id) VALUES ('BKK-HUB', 'บางกะปิ', 'SPX', $1)
		RETURNING id`, r.partyY)

	// The first extra stop upgrades the single-stop task: stop 1 is the task's destination, no party in the rows.
	s := r.must(200, "driverO1", "POST", path, map[string]any{"destination": "bkk-9", "isCustom": true, "sourceId": " src-1 "})
	stops, _ := s["stops"].([]any)
	if s["taskId"] != id || s["upgradedFromSingle"] != true || s["pending"] != false || len(stops) != 2 ||
		stops[0].(map[string]any)["destination"] != "SOCE" || stops[1].(map[string]any)["destination"] != "BKK-9" ||
		stops[1].(map[string]any)["isCustom"] != true || stops[1].(map[string]any)["sourceId"] != "src-1" {
		t.Fatalf("first stop: %v", s)
	}
	for _, st := range stops {
		if _, ok := st.(map[string]any)["destinationLinkedPartyId"]; ok {
			t.Fatalf("a driver reads a stop's billing party: %v", st)
		}
	}
	if evs := r.events(id); len(evs) == 0 || evs[len(evs)-1].route != "task.updated" {
		t.Fatalf("events %v", routes(evs))
	}
	// The next one: the task was multi-drop already.
	s = r.must(200, "driverO1", "POST", path, map[string]any{"destination": "bkk-hub"})
	if s["upgradedFromSingle"] != false || len(s["stops"].([]any)) != 3 {
		t.Fatalf("second stop: %v", s)
	}
	if r.count(`SELECT count(*) FROM task_delivery_stops WHERE task_id = $1 AND destination_raw = 'BKK-HUB'
		AND destination_hub_id = $2 AND destination_linked_party_id = $3`, id, hubY, r.partyY) != 1 {
		t.Fatal("the stop did not take its hub's party")
	}
	r.refused(409, "duplicate_destination", "driverO1", "POST", path, map[string]any{"destination": " BKK-9"})
	r.refused(422, "destination required", "driverO1", "POST", path, map[string]any{})
	// A driver never names the billing party of a stop.
	r.refused(422, "destinationLinkedPartyId not_supported", "driverO1", "POST", path,
		map[string]any{"destination": "bkk-7", "destinationLinkedPartyId": r.partyX.String()})

	// Another driver's task is 403 not_task_owner (R28: no phone or plate fallback); the helper may add stops.
	r.refused(403, operations.CodeNotTaskOwner, "driverO2", "POST", path, map[string]any{"destination": "bkk-8"})
	r.exec(`UPDATE tasks SET helper_driver_id = $2 WHERE id = $1`, id, r.dO2)
	r.must(200, "driverO2", "POST", path, map[string]any{"destination": "bkk-8"})

	// {id} may be the Firestore task doc id; an unknown id or doc id is 404, a quarantined task too.
	r.exec(`UPDATE tasks SET legacy_doc_id = 'fsTaskDoc1' WHERE id = $1`, id)
	if s := r.must(200, "driverO1", "POST", "/v1/mobile/tasks/fsTaskDoc1/stops", map[string]any{"destination": "bkk-5"}); s["taskId"] != id {
		t.Fatalf("by doc id: %v", s)
	}
	r.refused(404, "not_found", "driverO1", "POST", "/v1/mobile/tasks/0190a0b0-0000-7000-8000-000000000001/stops",
		map[string]any{"destination": "x"})
	r.refused(404, "not_found", "driverO1", "POST", "/v1/mobile/tasks/noSuchDoc/stops", map[string]any{"destination": "x"})
	// A quarantined task (no driver: a tenant orphan) is not found, not another driver's.
	r.exec(`INSERT INTO tasks (legacy_doc_id, tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw,
		destination_raw) VALUES ('fsOrphan', app_quarantine_tenant_id(), 'quarantine', 'first_mile', 'pending', now(), 'SRC', 'DST')`)
	r.refused(404, "not_found", "driverO1", "POST", "/v1/mobile/tasks/fsOrphan/stops", map[string]any{"destination": "bkk-6"})

	// Staff use POST /v1/tasks/{id}/stops: the driver route refuses a principal without mobile:submit_trip.
	r.refused(403, "permission_denied", "staffO", "POST", path, map[string]any{"destination": "bkk-4"})
	r.refused(400, "bad_request", "driverO1", "POST", path+"?x=1", map[string]any{"destination": "bkk-4"})
}

// While operations is Firestore-owned the driver's stop is appended to the task document first (the progress the 3.x
// app wrote on the other stops is kept), with the document's own isMultiDelivery deciding upgradedFromSingle; an
// unacknowledged write answers 202 pending.
func TestDriverAddsStopsWriteBack(t *testing.T) {
	mem := writeback.NewMemory()
	ack := &acks{}
	r := newWriteWorld(t, writeOptions{writeBack: &operations.WriteBack{Writer: mem, Acks: ack}})
	id := r.id(`INSERT INTO tasks (legacy_doc_id, tenant_id, tenant_source, task_no, task_type, status, plan_at,
		source_hub_raw, destination_raw, billing_party_id, driver_id, run_order, job_category)
		VALUES ('fsTask2', $1, 'driver', 'FM-12102026-002', 'first_mile', 'assigned', '2026-10-11T17:00:00Z', 'ALANG-A',
		'SOCE', $2, $3, 1, 'PRIMARY') RETURNING id`, r.O, r.partyX, r.dO1).String()
	mem.Put("tasks", "fsTask2", map[string]any{"destination": "SOCE", "isMultiDelivery": false, "driverId": "drv_o1",
		"destinationLinkedCustomerId": "cust_cjsf"}, now)

	// Not acknowledged: 202 pending, the document written, PostgreSQL untouched, the writer's notify event queued.
	d := r.must(202, "driverO1", "POST", "/v1/mobile/tasks/fsTask2/stops", map[string]any{"destination": "bkk-2"})
	if d["pending"] != true || d["upgradedFromSingle"] != true || d["taskId"] != id || len(d["stops"].([]any)) != 0 {
		t.Fatalf("pending answer %v", d)
	}
	doc, _ := mem.Doc("tasks", "fsTask2")
	stops, _ := doc["deliveryStops"].([]any)
	if doc["isMultiDelivery"] != true || len(stops) != 2 || stops[0].(map[string]any)["destinationLinkedCustomerId"] != "cust_cjsf" ||
		stops[1].(map[string]any)["destination"] != "BKK-2" {
		t.Fatalf("document %v", doc)
	}
	if r.count(`SELECT count(*) FROM task_delivery_stops`) != 0 {
		t.Fatal("the write-back wrote PostgreSQL")
	}
	if evs := r.events(id); len(evs) != 1 || evs[0].route != "task.updated" || evs[0].headers["origin"] != "writeback" {
		t.Fatalf("events %v", evs)
	}
	// Acknowledged: 200; the document is multi-drop now.
	ack.set("tasks", "fsTask2")
	if d := r.must(200, "driverO1", "POST", "/v1/mobile/tasks/"+id+"/stops", map[string]any{"destination": "bkk-3"}); d["upgradedFromSingle"] != false ||
		d["pending"] != false {
		t.Fatalf("acknowledged answer %v", d)
	}
	r.refused(409, "duplicate_destination", "driverO1", "POST", "/v1/mobile/tasks/fsTask2/stops", map[string]any{"destination": "BKK-3"})
	// A hub linked to carrier P (a partner): P is outside the own-fleet driver's tenant reach (tenants.p_read), yet the
	// stop names it, as addDeliveryStop copied the hub's linked partner (AddDeliveryStopDialog: h.linkedCustomerId).
	carrierP := r.id(`INSERT INTO tenants (kind, name_th, code, legal_type, legacy_doc_id)
		VALUES ('carrier', 'พาร์ทเนอร์ P', 'CP', 'company', 'sub_p') RETURNING id`)
	partyP := r.id(`INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', $1) RETURNING id`, carrierP)
	r.exec(`INSERT INTO hubs (source_id, name_th, network, linked_party_id) VALUES ('PARTNER-HUB', 'พาร์ทเนอร์', 'SPX', $1)`, partyP)
	r.must(200, "driverO1", "POST", "/v1/mobile/tasks/fsTask2/stops", map[string]any{"destination": "partner-hub"})
	doc, _ = mem.Doc("tasks", "fsTask2")
	stops, _ = doc["deliveryStops"].([]any)
	if last := stops[len(stops)-1].(map[string]any); last["destination"] != "PARTNER-HUB" || last["destinationLinkedCustomerId"] != "sub_p" ||
		last["destinationCustomerLinkKind"] != "partner" || last["destinationLinkedCustomerCode"] != "CP" {
		t.Fatalf("partner stop %v", last)
	}
	r.refused(403, operations.CodeNotTaskOwner, "driverO2", "POST", "/v1/mobile/tasks/fsTask2/stops", map[string]any{"destination": "bkk-4"})
}
