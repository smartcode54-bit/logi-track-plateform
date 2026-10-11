//go:build integration

package expenses_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The class C write-back (main spec §12.1, §13.8; until P7b, for driver-created rows): with a writer configured, every
// staff write reaches the Firestore document in the legacy shape before COMMIT (driver-created rows by their
// legacy_doc_id, rows born in PostgreSQL by a minted id stored as legacy_doc_id, R25), only the fields the write set
// (the APK's own fields of a pending row stay), and a failed write-back rolls the request back (503 unavailable).
func TestWriteBack(t *testing.T) {
	fs := writeback.NewMemory()
	w := newWorld(t, withWriteBack(fs))
	// A fuel log a driver wrote in Firestore, mirrored to PostgreSQL.
	logged := w.expense(w.O, "fuel", w.dO, w.tO, day(1, 1), 1500, "pending", "odometer_km", "98500", "legacy_doc_id", "'fsExp1'",
		"legacy_driver_ref", "'uid_from_firestore'")
	os := w.login("os.o")
	r := w.post("/v1/expenses/"+logged+"/status", os, map[string]any{"status": "approved", "adminNote": "ตรวจแล้ว"})
	expect(t, r, http.StatusOK, "")
	doc, ok := fs.Doc("vehicle_expenses", "fsExp1")
	if !ok || doc["status"] != "APPROVED" || doc["adminNote"] != "ตรวจแล้ว" || doc["updatedAt"] == nil {
		t.Fatalf("the approval did not reach Firestore: %v", doc)
	}
	if _, ok := doc["amount"]; ok {
		t.Error("a status write wrote fields it did not change")
	}
	// The PM booking it created is a record born in PostgreSQL: a minted document id.
	booking, _ := r.data()["pmCheck"].(map[string]any)
	id := str(booking["maintenanceId"])
	if got := w.str(`SELECT legacy_doc_id FROM maintenance_records WHERE id = $1`, id); got == nil || *got != id {
		t.Fatalf("legacy_doc_id %v", got)
	}
	m, ok := fs.Doc("maintenance", id)
	if !ok || m["status"] != "PM Booking" || m["truckId"] != "trk_กข-1001" || m["type"] != "PM" || m["createdAt"] == nil ||
		m["currentMileage"] != int64(98500) {
		t.Fatalf("booking document %v", m)
	}
	// A staff entry: every staff field, the driver's Firebase uid, created_by's uid.
	r = w.post("/v1/expenses", w.login("mg.o"), map[string]any{"type": "other", "driverId": w.dO, "date": "2026-10-10",
		"amountThb": 45, "category": "toll"})
	expect(t, r, http.StatusCreated, "")
	eid := str(r.data()["id"])
	e, ok := fs.Doc("vehicle_expenses", eid)
	if !ok || e["driverId"] != "uid_Somchai" || e["truckId"] != "trk_กข-1001" || e["status"] != "APPROVED" || e["amount"] != 45.0 ||
		e["category"] != "toll" || e["createdByUid"] == nil || e["type"] != "other" {
		t.Fatalf("entry document %v", e)
	}
	// A PATCH writes what it changed.
	before := len(fs.Writes())
	expect(t, w.patch("/v1/expenses/"+eid, w.login("mg.o"), map[string]any{"amountThb": 50}), http.StatusOK, "")
	ws := fs.Writes()
	if len(ws) != before+1 || ws[len(ws)-1].Fields["amount"] != 50.0 || len(ws[len(ws)-1].Fields) != 2 {
		t.Fatalf("patch write %+v", ws[len(ws)-1])
	}
	// Maintenance writes go back with the legacy status literal.
	r = w.patch("/v1/maintenance/"+id, w.login("op.o"), map[string]any{"status": "scheduled", "appointmentTime": "09:00"})
	expect(t, r, http.StatusOK, "")
	if m, _ := fs.Doc("maintenance", id); m["status"] != "Scheduled" || m["appointmentTime"] != "09:00" {
		t.Fatalf("maintenance write %v", m)
	}

	// Firestore down: the request fails and PostgreSQL keeps nothing of it.
	fs.Fail(errors.New("firestore unavailable"))
	other := w.expense(w.O, "fuel", w.dO2, w.tO2, day(2, 1), 10, "pending", "legacy_doc_id", "'fsExp2'")
	events := w.count(`SELECT count(*) FROM outbox_events`)
	r = w.post("/v1/expenses/"+other+"/status", os, map[string]any{"status": "approved"})
	expect(t, r, http.StatusServiceUnavailable, "unavailable")
	if r.detail("reason") != "write_back" {
		t.Errorf("details %v", r.body)
	}
	if *w.str(`SELECT status FROM vehicle_expenses WHERE id = $1`, other) != "pending" ||
		w.count(`SELECT count(*) FROM outbox_events`) != events {
		t.Fatal("a failed write-back left a change behind")
	}
	expect(t, w.post("/v1/maintenance", w.login("op.o"), map[string]any{"truckId": w.tO2, "type": "CM", "serviceType": "x",
		"startDate": "2026-10-11"}), http.StatusServiceUnavailable, "unavailable")
	if w.count(`SELECT count(*) FROM maintenance_records WHERE truck_id = $1`, w.tO2) != 0 {
		t.Fatal("a failed write-back created a record")
	}
}
