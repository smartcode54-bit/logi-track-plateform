//go:build integration

package fleet_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"github.com/xuri/excelize/v2"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// --- create, plate rule, uniqueness, vehicle class, tenancy ----------------------------------------------

func TestTruckCreate(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")

	// AC: the plate rule (422 with field errors, nothing written).
	for _, plate := range []string{"AB-1234", "70-1234", "กก-12", "กกก-1234"} {
		r := w.post("/v1/trucks", taO, newTruck(plate, nil))
		expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
		if f := r.fields(); f["licensePlate"] != "invalid" || len(f) != 1 {
			t.Fatalf("plate %q: %v", plate, f)
		}
	}
	if n := w.count(`SELECT count(*) FROM trucks`); n != 0 {
		t.Fatalf("a refused create wrote %d trucks", n)
	}
	r := w.post("/v1/trucks", taO, map[string]any{})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); len(f) != 8 || f["licensePlate"] != "required" || f["year"] != "required" {
		t.Fatalf("empty body: %v", f)
	}

	r = w.post("/v1/trucks", taO, newTruck(" กก-1234 ", map[string]any{"taxExpenseThb": 1500.5, "gpsVehicleId": "CT-1",
		"vin": "mrhfb26a0kp123456", "taxExpiryDate": "2027-01-31", "pmIntervalKm": 10000, "statusNote": "new fleet"}))
	expect(t, r, http.StatusCreated, "")
	d := r.data()
	id := str(d["id"])
	if d["licensePlate"] != "กก-1234" || d["vehicleClass"] != "4WJ" || d["ownershipType"] != "own" || d["tenantId"] != w.O ||
		d["vin"] != "MRHFB26A0KP123456" || d["taxExpiryDate"] != "2027-01-31" || d["taxExpenseThb"] != 1500.5 ||
		d["taxResponsible"] != fleet.DefaultTaxResponsible || d["createdBy"] == nil {
		t.Fatalf("created: %s", r.raw)
	}
	hist := d["statusHistory"].([]any)
	if len(hist) != 1 || hist[0].(map[string]any)["status"] != "active" || hist[0].(map[string]any)["previousStatus"] != nil ||
		hist[0].(map[string]any)["reason"] != "new fleet" || hist[0].(map[string]any)["changedByLabel"] != "ta.o@example.test" {
		t.Fatalf("first status entry: %v", hist)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'truck.created' AND aggregate_id = $1
		AND tenant_id = $2 AND realtime_topics = ARRAY['tenant:' || $2 || ':fleet'] AND payload->>'entity' = 'truck'`, id, w.O); n != 1 {
		t.Fatalf("outbox truck.created: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM trucks WHERE id = $1 AND tenant_source = 'self'`, id); n != 1 {
		t.Fatal("tenant_source")
	}

	// AC: UNIQUE (tenant_id, license_plate) -> 409 naming the field; the GPS vehicle id is unique too.
	r = w.post("/v1/trucks", w.login("mg.o"), newTruck("กก-1234", nil))
	expect(t, r, http.StatusConflict, "already_exists")
	if r.detail("field") != "licensePlate" {
		t.Fatalf("conflict: %s", r.raw)
	}
	r = w.post("/v1/trucks", taO, newTruck("กก-1235", map[string]any{"gpsVehicleId": "CT-1"}))
	expect(t, r, http.StatusConflict, "already_exists")
	if r.detail("field") != "gpsVehicleId" {
		t.Fatalf("gps conflict: %s", r.raw)
	}
	// Per tenant (D4): another carrier may hold the same plate.
	expect(t, w.post("/v1/trucks", w.login("ta.c"), newTruck("กก-1234", nil)), http.StatusCreated, "")

	// AC: an unknown type has no class, never 4WJ.
	r = w.post("/v1/trucks", taO, newTruck("กข-5555", map[string]any{"type": "Trailer"}))
	expect(t, r, http.StatusCreated, "")
	if v, ok := r.data()["vehicleClass"]; !ok || v != nil || r.data()["type"] != "Trailer" {
		t.Fatalf("unknown type: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM trucks WHERE license_plate = 'กข-5555' AND vehicle_class IS NULL`); n != 1 {
		t.Fatal("vehicle_class of an unknown type is not NULL")
	}

	// A subcontractor truck: an own-fleet admin names a carrier of its contractor reach; never another carrier.
	r = w.post("/v1/trucks", taO, newTruck("1กข-7777", map[string]any{"tenantId": w.A, "ownershipType": "own"}))
	expect(t, r, http.StatusCreated, "")
	if r.data()["tenantId"] != w.A || r.data()["ownershipType"] != "subcontractor" {
		t.Fatalf("subcontractor truck: %s", r.raw)
	}
	r = w.post("/v1/trucks", taO, newTruck("1กข-8888", map[string]any{"tenantId": w.C}))
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["tenantId"] != "not_in_reach" {
		t.Fatalf("carrier outside the reach: %v", r.fields())
	}
	// The carrier's own admin registers into its own tenant only.
	r = w.post("/v1/trucks", w.login("ta.a"), newTruck("1กข-9999", map[string]any{"tenantId": w.O}))
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	r = w.post("/v1/trucks", w.login("ta.a"), newTruck("1กข-9999", nil))
	expect(t, r, http.StatusCreated, "")
	if r.data()["tenantId"] != w.A || r.data()["ownershipType"] != "subcontractor" {
		t.Fatalf("carrier's truck: %s", r.raw)
	}

	// Capabilities: an operator lists and reads but does not create; a platform admin needs a tenant to act in.
	expect(t, w.post("/v1/trucks", w.login("op.o"), newTruck("กค-1111", nil)), http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/trucks/"+id, w.login("op.o")), http.StatusOK, "")
	expect(t, w.post("/v1/trucks", w.login("pa"), newTruck("กค-1111", nil)), http.StatusForbidden, "")
}

// The tenant layer: a carrier sees its own trucks only; own-fleet staff also reach the carriers working for them.
func TestTruckTenantIsolation(t *testing.T) {
	w := newWorld(t)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	tO := w.truck(w.O, "กก-1001", base)
	tA := w.truck(w.A, "กก-1002", base.Add(time.Minute))
	tC := w.truck(w.C, "กก-1003", base.Add(2*time.Minute))
	ids := func(r resp) []string {
		var out []string
		for _, it := range r.list() {
			out = append(out, str(it["id"]))
		}
		slices.Sort(out)
		return out
	}
	sorted := func(v ...string) []string { slices.Sort(v); return v }

	taO, taA, taC := w.login("ta.o"), w.login("ta.a"), w.login("ta.c")
	if got := ids(w.get("/v1/trucks", taO)); !slices.Equal(got, sorted(tO, tA)) {
		t.Fatalf("own fleet lists %v", got)
	}
	if got := ids(w.get("/v1/trucks", taA)); !slices.Equal(got, []string{tA}) {
		t.Fatalf("carrier A lists %v", got)
	}
	if got := ids(w.get("/v1/trucks", taC)); !slices.Equal(got, []string{tC}) {
		t.Fatalf("carrier C lists %v", got)
	}
	expect(t, w.get("/v1/trucks/"+tO, taA), http.StatusNotFound, "not_found")
	expect(t, w.patch("/v1/trucks/"+tO, taA, map[string]any{"notes": "x"}), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/trucks/"+tA, taO), http.StatusOK, "")
	expect(t, w.patch("/v1/trucks/"+tA, taO, map[string]any{"notes": "from the contractor"}), http.StatusOK, "")
	expect(t, w.get("/v1/trucks/"+tC, taO), http.StatusNotFound, "not_found")
	if got := ids(w.get("/v1/trucks?tenantId="+w.A, taO)); !slices.Equal(got, []string{tA}) {
		t.Fatalf("tenantId filter: %v", got)
	}
	if got := ids(w.get("/v1/trucks?tenantId="+w.C, taO)); len(got) != 0 {
		t.Fatalf("a tenant outside the reach lists %v", got)
	}

	// A platform-only principal has no tenant capabilities in the wildcard view.
	pa := w.login("pa")
	all := w.send(http.MethodGet, "/v1/trucks", "", "", nil)
	expect(t, all, http.StatusUnauthorized, "")
	req := func(method, path, act string, body any) resp {
		return w.do(method, path, pa, body, authz.HeaderActOnTenant, act)
	}
	expect(t, req(http.MethodGet, "/v1/trucks", "*", nil), http.StatusBadRequest, "header_not_allowed")
	expect(t, req(http.MethodPost, "/v1/trucks", "*", newTruck("กค-1999", nil)), http.StatusBadRequest, "header_not_allowed")
	if n := w.count(`SELECT count(*) FROM trucks WHERE license_plate = 'กค-1999'`); n != 0 {
		t.Fatalf("a refused header request created a truck: %d rows", n)
	}
	// UUID headers are refused by the same D1 boundary.
	expect(t, req(http.MethodGet, "/v1/trucks", w.C, nil), http.StatusBadRequest, "header_not_allowed")
	expect(t, req(http.MethodPatch, "/v1/trucks/"+tC, w.C, map[string]any{"notes": "platform"}), http.StatusBadRequest, "header_not_allowed")
	if n := w.count(`SELECT count(*) FROM trucks WHERE id = $1 AND notes = 'platform'`, tC); n != 0 {
		t.Fatalf("a refused header request changed the carrier truck notes: %d rows", n)
	}
}

// --- PATCH, status history (append-only) ----------------------------------------------------------------

func TestTruckPatchAndStatusHistory(t *testing.T) {
	w := newWorld(t)
	taO, mgO := w.login("ta.o"), w.login("mg.o")
	r := w.post("/v1/trucks", taO, newTruck("กก-2001", map[string]any{"type": "6 Wheels"}))
	expect(t, r, http.StatusCreated, "")
	id := str(r.data()["id"])
	if r.data()["vehicleClass"] != "6WH" {
		t.Fatalf("class: %s", r.raw)
	}
	events := func() int {
		return w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND routing_key = 'truck.updated'`, id)
	}

	r = w.patch("/v1/trucks/"+id, mgO, map[string]any{"status": "maintenance", "statusNote": "brake check",
		"currentMileage": 120000, "type": "Van"})
	expect(t, r, http.StatusOK, "")
	d := r.data()
	if d["status"] != "maintenance" || d["vehicleClass"] != "VAN" || d["currentMileage"] != float64(120000) {
		t.Fatalf("patched: %s", r.raw)
	}
	hist := d["statusHistory"].([]any)
	newest := hist[0].(map[string]any)
	if len(hist) != 2 || newest["status"] != "maintenance" || newest["previousStatus"] != "active" ||
		newest["reason"] != "brake check" || newest["changedByLabel"] != "mg.o@example.test" {
		t.Fatalf("history: %v", hist)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND routing_key = 'truck.updated'
		AND payload->'changed' ?& ARRAY['status', 'currentMileage', 'type']`, id); n != 1 {
		t.Fatalf("truck.updated with the changed fields: %d", n)
	}
	// The same values again: no write, no event, no history.
	before := events()
	expect(t, w.patch("/v1/trucks/"+id, mgO, map[string]any{"status": "maintenance", "type": "Van", "licensePlate": "กก-2001"}),
		http.StatusOK, "")
	if events() != before || w.count(`SELECT count(*) FROM status_history WHERE entity_id = $1`, id) != 2 {
		t.Fatal("a no-op PATCH wrote")
	}
	// A field change without a status change appends no history.
	expect(t, w.patch("/v1/trucks/"+id, mgO, map[string]any{"notes": "x"}), http.StatusOK, "")
	if n := w.count(`SELECT count(*) FROM status_history WHERE entity_id = $1`, id); n != 2 {
		t.Fatalf("history rows: %d", n)
	}
	// Not editable here: the tenant (frozen); the plate keeps its rule and uniqueness.
	r = w.patch("/v1/trucks/"+id, mgO, map[string]any{"tenantId": w.A})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["tenantId"] != "not_editable" {
		t.Fatalf("tenantId: %v", r.fields())
	}
	expect(t, w.patch("/v1/trucks/"+id, mgO, map[string]any{"tenantId": w.O, "notes": "y"}), http.StatusOK, "")
	expect(t, w.post("/v1/trucks", taO, newTruck("กก-2002", nil)), http.StatusCreated, "")
	r = w.patch("/v1/trucks/"+id, mgO, map[string]any{"licensePlate": "กก-2002"})
	expect(t, r, http.StatusConflict, "already_exists")
	expect(t, w.patch("/v1/trucks/"+id, w.login("op.o"), map[string]any{"notes": "z"}), http.StatusForbidden, "permission_denied")

	// legacyStatus is the raw legacy value of the current status (an ETL-loaded 'Available' truck): a write that keeps
	// the status keeps it, a status change clears it.
	legacy := w.truck(w.O, "กก-2003", now, "legacy_status", "'Available'")
	r = w.patch("/v1/trucks/"+legacy, mgO, map[string]any{"notes": "kept"})
	expect(t, r, http.StatusOK, "")
	if r.data()["legacyStatus"] != "Available" || r.data()["status"] != "active" {
		t.Fatalf("legacy status after a notes edit: %s", r.raw)
	}
	r = w.patch("/v1/trucks/"+legacy, mgO, map[string]any{"status": "sold"})
	expect(t, r, http.StatusOK, "")
	if v, ok := r.data()["legacyStatus"]; !ok || v != nil || r.data()["status"] != "sold" {
		t.Fatalf("legacy status after a status change: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM trucks WHERE id = $1 AND legacy_status IS NULL`, legacy); n != 1 {
		t.Fatal("legacy_status was not cleared")
	}

	// AC: UPDATE (and DELETE) on status history is rejected: revoked from the app role (42501) and refused by the
	// append-only trigger even for a role that could (23000).
	for _, what := range []string{"update-status-history", "delete-status-history"} {
		r := w.post("/v1/fleettest/mutate/"+what, taO, nil)
		expect(t, r, http.StatusOK, "")
		if r.data()["sqlstate"] != "42501" {
			t.Fatalf("%s as the app role: %s", what, r.raw)
		}
	}
	super, err := pgx.Connect(context.Background(), w.db.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = super.Close(context.Background()) }()
	if _, err := super.Exec(context.Background(), `UPDATE status_history SET reason = 'x' WHERE entity_id = $1`, id); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Fatalf("UPDATE status_history as the owner: %v", err)
	}
	if _, err := w.etl.Exec(context.Background(), `UPDATE status_history SET reason = 'x' WHERE entity_id = $1`, id); err == nil {
		t.Fatal("UPDATE status_history as the ETL role succeeded")
	}
	if n := w.count(`SELECT count(*) FROM status_history WHERE entity_id = $1 AND reason = 'brake check'`, id); n != 1 {
		t.Fatal("the history row changed")
	}
}

// Cost fields need more than fleet:view_trucks (Appendix C §C.2.3 row 1: non-financial fields), and so do the tax and
// insurance receipts that show the same amounts: in the truck payload and through GET /v1/files.
func TestTruckCostFields(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")
	r := w.post("/v1/trucks", taO, newTruck("กก-3001", map[string]any{"taxExpenseThb": 1000,
		"insurancePremiumThb": 9000, "paymentMethod": "transfer"}))
	expect(t, r, http.StatusCreated, "")
	id := str(r.data()["id"])
	for name, sees := range map[string]bool{"ta.o": true, "op.o": true, "us.o": false} {
		for _, d := range []map[string]any{w.get("/v1/trucks/"+id, w.login(name)).data(), w.get("/v1/trucks", w.login(name)).list()[0]} {
			got := d["taxExpenseThb"] != nil && d["insurancePremiumThb"] != nil && d["paymentMethod"] != nil
			if got != sees {
				t.Fatalf("%s sees cost fields %v: %v", name, got, d)
			}
		}
	}

	taxReceipt := w.upload(taO, "truck_receipt", id, "application/pdf", pdf)
	insReceipt := w.upload(taO, "truck_receipt", id, "application/pdf", pdf)
	photo := w.upload(taO, "truck_photo", id, "image/png", png)
	expect(t, w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "tax", "amount": 1000,
		"expiryDate": "2027-10-01", "receiptKey": taxReceipt}), http.StatusCreated, "")
	expect(t, w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "insurance", "amount": 9000,
		"expiryDate": "2027-10-01", "receiptKey": insReceipt}), http.StatusCreated, "")
	expect(t, w.patch("/v1/trucks/"+id, taO, map[string]any{"files": map[string]any{"imageFrontRight": []string{photo}}}),
		http.StatusOK, "")
	for name, sees := range map[string]bool{"op.o": true, "us.o": false} {
		files := w.get("/v1/trucks/"+id, w.login(name)).data()["files"].(map[string]any)
		_, tax := files["taxReceipt"]
		_, ins := files["insuranceReceipt"]
		if tax != sees || ins != sees || files["imageFrontRight"] == nil {
			t.Fatalf("%s sees receipts %v %v: %v", name, tax, ins, files)
		}
		for _, key := range []string{taxReceipt, insReceipt} {
			want := http.StatusOK // followed to the signed URL
			if !sees {
				want = http.StatusNotFound
			}
			if got := w.get("/v1/files?key="+url.QueryEscape(key), w.login(name)); got.status != want {
				t.Fatalf("%s GET /v1/files of a receipt: %d, want %d", name, got.status, want)
			}
		}
		if got := w.get("/v1/files?key="+url.QueryEscape(photo), w.login(name)); got.status != http.StatusOK {
			t.Fatalf("%s GET /v1/files of a photo: %d", name, got.status)
		}
	}
	// The receipt kinds take receipts only (the GET /v1/files rule follows the purpose).
	doc := w.upload(taO, "truck_document", id, "application/pdf", pdf)
	r = w.patch("/v1/trucks/"+id, taO, map[string]any{"files": map[string]any{"taxReceipt": []string{doc}}})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["files.taxReceipt"] != "purpose_mismatch" {
		t.Fatalf("a document as a receipt: %v", r.fields())
	}
}

// --- files ------------------------------------------------------------------------------------------------

func TestTruckFiles(t *testing.T) {
	w := newWorld(t)
	taO, taA := w.login("ta.o"), w.login("ta.a")
	pending := uuid.NewString()
	front := w.upload(taO, "truck_photo", pending, "image/png", png)
	reg := w.upload(taO, "truck_document", pending, "application/pdf", pdf)
	r := w.post("/v1/trucks", taO, newTruck("1กข-4001", map[string]any{"tenantId": w.A,
		"files": map[string]any{"imageFrontRight": []string{front}, "documentRegister": []string{reg}}}))
	expect(t, r, http.StatusCreated, "")
	id := str(r.data()["id"])
	files := r.data()["files"].(map[string]any)
	fr := files["imageFrontRight"].([]any)[0].(map[string]any)
	if fr["key"] != front || files["documentRegister"].([]any)[0].(map[string]any)["key"] != reg {
		t.Fatalf("files: %s", r.raw)
	}
	if code, body := fetch(t, str(fr["url"])); code != http.StatusOK || !bytes.Equal(body, png) {
		t.Fatalf("presigned GET: %d", code)
	}
	if n := w.count(`SELECT count(*) FROM file_objects f JOIN truck_files tf ON tf.file_id = f.id
		WHERE tf.truck_id = $1 AND f.status = 'committed' AND f.owner_kind = 'truck' AND f.owner_id = $1`, id); n != 2 {
		t.Fatalf("committed truck files: %d", n)
	}

	// The carrier's admin edits the truck and sends the files back: the keys the truck links stay linked although
	// ta.a neither uploaded them nor is in the uploader's tenant.
	events := w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1`, id)
	r = w.patch("/v1/trucks/"+id, taA, map[string]any{"files": map[string]any{"imageFrontRight": []string{front},
		"documentRegister": []string{reg}}})
	expect(t, r, http.StatusOK, "")
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1`, id); n != events {
		t.Fatalf("sending the files back changed something (%d -> %d)", events, n)
	}
	// A key of someone else that is not the truck's is not found; a wrong purpose is refused.
	other := w.upload(w.login("ta.c"), "truck_photo", pending, "image/png", png)
	r = w.patch("/v1/trucks/"+id, taA, map[string]any{"files": map[string]any{"imageBackLeft": []string{other}}})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["files.imageBackLeft"] != "not_found" {
		t.Fatalf("foreign key: %v", r.fields())
	}
	doc := w.upload(taA, "truck_document", id, "application/pdf", pdf)
	r = w.patch("/v1/trucks/"+id, taA, map[string]any{"files": map[string]any{"imageBackLeft": []string{doc}}})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["files.imageBackLeft"] != "purpose_mismatch" {
		t.Fatalf("purpose: %v", r.fields())
	}
	// Moving a file to another kind and clearing a kind; the objects stay.
	r = w.patch("/v1/trucks/"+id, taA, map[string]any{"files": map[string]any{"imageFrontRight": []string{},
		"other": []string{front, doc}}})
	expect(t, r, http.StatusOK, "")
	files = r.data()["files"].(map[string]any)
	if _, ok := files["imageFrontRight"]; ok || len(files["other"].([]any)) != 2 || len(files["documentRegister"].([]any)) != 1 {
		t.Fatalf("after the move: %v", files)
	}
	if n := w.count(`SELECT count(*) FROM truck_files WHERE truck_id = $1`, id); n != 3 {
		t.Fatalf("truck_files rows: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND routing_key = 'truck.updated'
		AND payload->'changed' ? 'files'`, id); n != 1 {
		t.Fatalf("files change event: %d", n)
	}
	r = w.post("/v1/trucks", taO, newTruck("กก-4002", map[string]any{"files": map[string]any{"imageFrontRight": []string{"a", "b"}, "x": []string{"c"}}}))
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["files.imageFrontRight"] != "too_many" || f["files.x"] != "invalid" {
		t.Fatalf("files validation: %v", f)
	}
}

// A truck read or write never holds one pool connection while it waits for a second (the storage signer takes its
// own): every call finishes on a one-connection pool, and concurrent ones on two (the T20 review regression).
func TestFleetNeverNestsPoolConnections(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")
	front := w.upload(taO, "truck_photo", uuid.NewString(), "image/png", png)
	receipt := w.upload(taO, "truck_receipt", uuid.NewString(), "application/pdf", pdf)
	r := w.post("/v1/trucks", taO, newTruck("กก-5001", map[string]any{"files": map[string]any{"imageFrontRight": []string{front}}}))
	expect(t, r, http.StatusCreated, "")
	id := uuid.MustParse(str(r.data()["id"]))

	service := func(maxConns int32) *fleet.Service {
		pool, err := db.NewPool(context.Background(), w.db.URL(db.RoleApp), "pgtest-small", db.Options{MaxConns: maxConns})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		st, err := storage.New(pool, w.stCfg, zerolog.Nop(), w.local)
		if err != nil {
			t.Fatal(err)
		}
		fs, err := fleet.New(fleet.Deps{Pool: pool, Files: st, Log: zerolog.Nop(), Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		return fs
	}
	o := uuid.MustParse(w.O)
	p := &authz.Principal{UserID: uuid.MustParse(w.id(`SELECT id::text FROM users WHERE email = 'ta.o@example.test'`)),
		TenantID: &o, TenantRole: authz.TenantAdmin, TenantKind: authz.TenantKindOwnFleet, Steward: true,
		Caps: authz.NewCapSet(authz.FleetViewTrucks, authz.FleetEditTruck, authz.FleetCreateTruck, authz.FleetViewRenewals,
			authz.FleetManageRenewals)}
	keep := fleet.TruckInput{Files: fleet.Field[map[string][]string]{Set: true, V: map[string][]string{"imageFrontRight": {front}}}}

	fs := service(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d, err := fs.GetTruck(ctx, p, id)
	if err != nil || len(d.Files["imageFrontRight"]) != 1 || d.Files["imageFrontRight"][0].URL == nil {
		t.Fatalf("GetTruck on one connection: %v %+v", err, d)
	}
	if _, err := fs.PatchTruck(ctx, p, "", id, keep); err != nil {
		t.Fatalf("PatchTruck on one connection: %v", err)
	}
	if _, _, err := fs.RecordRenewal(ctx, p, "", id, fleet.RenewalInput{Type: "tax", Amount: fleet.Field[float64]{Set: true, V: 10},
		ExpiryDate: fleet.Field[string]{Set: true, V: "2027-10-11"}, ReceiptKey: fleet.Field[string]{Set: true, V: receipt}}); err != nil {
		t.Fatalf("RecordRenewal on one connection: %v", err)
	}
	if _, _, _, err := fs.ListTrucks(ctx, p, fleet.ListTrucksInput{}); err != nil {
		t.Fatalf("ListTrucks on one connection: %v", err)
	}
	if _, _, err := fs.ListRenewals(ctx, p, fleet.ListRenewalsInput{}); err != nil {
		t.Fatalf("ListRenewals on one connection: %v", err)
	}

	fs = service(2)
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			_, err := fs.GetTruck(ctx, p, id)
			errs <- err
		})
		wg.Go(func() {
			in := keep
			in.Notes = fleet.Field[string]{Set: true, V: fmt.Sprint("note ", i)}
			_, err := fs.PatchTruck(ctx, p, "", id, in)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent truck calls on two connections: %v", err)
		}
	}
}

// --- renewals ---------------------------------------------------------------------------------------------

func TestRenewalOverview(t *testing.T) {
	w := newWorld(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	date := func(days int) string { return "'" + now.AddDate(0, 0, days).Format(time.DateOnly) + "'" } // today = 2026-10-11
	overdue := w.truck(w.O, "กก-6001", base, "tax_expiry_date", date(-5), "insurance_expiry_date", date(200))
	soon := w.truck(w.O, "กก-6002", base, "tax_expiry_date", date(100), "insurance_expiry_date", date(10))
	fine := w.truck(w.O, "กก-6003", base, "tax_expiry_date", date(100), "insurance_expiry_date", date(120))
	missing := w.truck(w.A, "กก-6004", base, "tax_expiry_date", date(300))
	working := w.truck(w.O, "กก-6005", base, "tax_expiry_date", date(-1), "insurance_expiry_date", date(90),
		"tax_renewal_status", "'in_progress'", "tax_expense_thb", "1200")
	_ = w.truck(w.C, "กก-6006", base) // outside the reach

	op := w.login("op.o") // operator: fleet:view_renewals
	r := w.get("/v1/trucks/renewals?due=30", op)
	expect(t, r, http.StatusOK, "")
	var got []string
	for _, it := range r.list() {
		got = append(got, str(it["truckId"]))
	}
	// Most urgent first: a missing date, then by the earlier expiry; the fine truck (both > 30 days) is not due.
	if want := []string{missing, overdue, working, soon}; !slices.Equal(got, want) {
		t.Fatalf("due in 30 days: %v, want %v", got, want)
	}
	byID := map[string]map[string]any{}
	for _, it := range r.list() {
		byID[str(it["truckId"])] = it
	}
	state := func(id, side string) (string, any) {
		s := byID[id][side].(map[string]any)
		return str(s["state"]), s["daysRemaining"]
	}
	for _, c := range []struct {
		id, side, state string
		days            any
	}{
		{overdue, "tax", "overdue", float64(-5)}, {overdue, "insurance", "ok", float64(200)},
		{soon, "insurance", "expiring_soon", float64(10)}, {missing, "insurance", "overdue", nil},
		{working, "tax", "in_progress", float64(-1)},
	} {
		if s, d := state(c.id, c.side); s != c.state || d != c.days {
			t.Errorf("%s %s: %s %v, want %s %v", c.id, c.side, s, d, c.state, c.days)
		}
	}
	if byID[missing]["nextDueDate"] != nil || byID[soon]["nextDueDate"] != now.AddDate(0, 0, 10).Format(time.DateOnly) ||
		byID[working]["tax"].(map[string]any)["amountThb"] != float64(1200) {
		t.Fatalf("items: %s", r.raw)
	}
	if n := len(w.get("/v1/trucks/renewals", op).list()); n != 5 {
		t.Fatalf("without due: %d trucks", n)
	}
	// Keyset over (due date, id).
	p1 := w.get("/v1/trucks/renewals?limit=2", op)
	p2 := w.get("/v1/trucks/renewals?limit=2&cursor="+url.QueryEscape(p1.next()), op)
	p3 := w.get("/v1/trucks/renewals?limit=2&cursor="+url.QueryEscape(p2.next()), op)
	if len(p1.list()) != 2 || len(p2.list()) != 2 || len(p3.list()) != 1 || p3.next() != "" ||
		str(p3.list()[0]["truckId"]) != fine {
		t.Fatalf("pages: %s | %s | %s", p1.raw, p2.raw, p3.raw)
	}
	expect(t, w.get("/v1/trucks/renewals?due=400", op), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/trucks/renewals?due=x", op), http.StatusUnprocessableEntity, "invalid_argument")
	for _, query := range []string{"q=%FF", "q=a%00b"} {
		r := w.get("/v1/trucks/renewals?"+query, op)
		expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
		if r.fields()["q"] != "invalid" {
			t.Errorf("%s: %s", query, r.raw)
		}
	}
	expect(t, w.get("/v1/trucks/renewals", w.login("us.o")), http.StatusForbidden, "permission_denied")
	if got := w.get("/v1/trucks/renewals?ownership=subcontractor", op).list(); len(got) != 1 || str(got[0]["truckId"]) != missing {
		t.Fatalf("ownership filter: %v", got)
	}
}

func TestRecordRenewal(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")
	r := w.post("/v1/trucks", taO, newTruck("1กข-7001", map[string]any{"tenantId": w.A, "taxExpiryDate": "2026-10-01"}))
	expect(t, r, http.StatusCreated, "")
	id := str(r.data()["id"])

	// The gate is fleet:manage_renewals (R5): an operator who may view renewals may not record one.
	expect(t, w.post("/v1/trucks/"+id+"/renewals", w.login("op.o"), map[string]any{"type": "tax", "amount": 100,
		"expiryDate": "2027-10-01"}), http.StatusForbidden, "permission_denied")
	r = w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "tax"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["amount"] != "required" || f["expiryDate"] != "required" {
		t.Fatalf("completed needs amount and expiry: %v", f)
	}

	receipt := w.upload(taO, "truck_receipt", id, "application/pdf", pdf)
	r = w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "tax", "amount": 1850.25, "paymentMethod": "transfer",
		"expiryDate": "2027-10-01", "receiptKey": receipt, "responsible": "Fleet admin"})
	expect(t, r, http.StatusCreated, "")
	tr := r.data()["transaction"].(map[string]any)
	tk := r.data()["truck"].(map[string]any)
	if tr["type"] != "tax" || tr["subType"] != "Tax Renewal" || tr["amountThb"] != 1850.25 || tr["date"] != "2026-10-11" ||
		tr["receiptKey"] != receipt || tr["notes"] != "Expiry: 2027-10-01" || tr["paymentMethod"] != "transfer" {
		t.Fatalf("transaction: %v", tr)
	}
	if tk["taxExpiryDate"] != "2027-10-01" || tk["taxRenewalStatus"] != "completed" || tk["taxExpenseThb"] != 1850.25 ||
		tk["taxResponsible"] != "Fleet admin" || tk["paymentMethod"] != "transfer" {
		t.Fatalf("truck after the renewal: %v", tk)
	}
	if hist := tk["statusHistory"].([]any); hist[0].(map[string]any)["status"] != "tax_renewed" ||
		hist[0].(map[string]any)["reason"] != "Expiry: 2027-10-01" {
		t.Fatalf("history: %v", hist)
	}
	if rs := tk["files"].(map[string]any)["taxReceipt"].([]any); len(rs) != 1 || rs[0].(map[string]any)["key"] != receipt {
		t.Fatalf("receipt file: %v", tk["files"])
	}
	// The ledger row: the truck's tenant (A, tenant_source truck), the receipt, the actor.
	if n := w.count(`SELECT count(*) FROM transactions t JOIN file_objects f ON f.id = t.receipt_file_id
		WHERE t.truck_id = $1 AND t.tenant_id = $2 AND t.tenant_source = 'truck' AND t.tx_type = 'tax'
		  AND t.amount_thb = 1850.25 AND t.tx_date = '2026-10-11' AND t.tx_date_source = 'bangkok'
		  AND t.performed_by_label = 'ta.o@example.test' AND f.object_key = $3`, id, w.A, receipt); n != 1 {
		t.Fatalf("transactions row: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND routing_key = 'truck.renewed'
		AND tenant_id = $2`, id, w.A); n != 1 {
		t.Fatalf("truck.renewed: %d", n)
	}

	// Insurance work in progress: the truck only, no ledger row; the overview shows it in progress.
	doc := w.upload(taO, "insurance_document", id, "application/pdf", pdf)
	r = w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "insurance", "status": "in_progress",
		"provider": "Viriyah", "coverage": "2+", "documentKeys": []string{doc}})
	expect(t, r, http.StatusOK, "")
	if r.data()["transaction"] != nil || r.data()["truck"].(map[string]any)["insuranceRenewalStatus"] != "in_progress" ||
		r.data()["truck"].(map[string]any)["insuranceCompany"] != "Viriyah" {
		t.Fatalf("draft: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM transactions WHERE truck_id = $1`, id); n != 1 {
		t.Fatalf("a draft wrote a ledger row: %d", n)
	}
	items := w.get("/v1/trucks/renewals", taO).list()
	if len(items) != 1 || items[0]["insurance"].(map[string]any)["state"] != "in_progress" {
		t.Fatalf("overview: %v", items)
	}
	// Completing it with the documents sent again keeps one link per document.
	r = w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "insurance", "amount": 9000, "expiryDate": "2027-10-11",
		"startDate": "2026-10-12", "policyNumber": "P-77", "coverage": "2+", "documentKeys": []string{doc}, "date": "2026-10-10"})
	expect(t, r, http.StatusCreated, "")
	tr = r.data()["transaction"].(map[string]any)
	if tr["subType"] != "Insurance Renewal (2+)" || tr["notes"] != "Policy: P-77 | Expiry: 2027-10-11" || tr["date"] != "2026-10-10" {
		t.Fatalf("insurance transaction: %v", tr)
	}
	if docs := r.data()["truck"].(map[string]any)["files"].(map[string]any)["insuranceDocument"].([]any); len(docs) != 1 {
		t.Fatalf("insurance documents: %v", docs)
	}

	// T25: the insurance form sends its whole document list (the stored keys it keeps go back as themselves, a removed
	// one is unlinked), its person in charge (maintenance_responsible, as the legacy form wrote it) and the truck's
	// insurance notes; none of them is the ledger note.
	doc2 := w.uploadNamed(taO, "insurance_document", id, "application/pdf", "policy-2", pdf)
	r = w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "insurance", "status": "in_progress",
		"responsible": "Somchai", "insuranceNotes": "Claim #42 pending with insurer", "documents": []string{doc2}})
	expect(t, r, http.StatusOK, "")
	tk = r.data()["truck"].(map[string]any)
	if docs := tk["files"].(map[string]any)["insuranceDocument"].([]any); len(docs) != 1 || docs[0].(map[string]any)["key"] != doc2 {
		t.Fatalf("documents after the form removed one and added one: %v", docs)
	}
	if tk["maintenanceResponsible"] != "Somchai" || tk["insuranceNotes"] != "Claim #42 pending with insurer" ||
		tk["taxResponsible"] != "Fleet admin" {
		t.Fatalf("insurance person in charge and notes: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM file_objects WHERE object_key = $1 AND deleted_at IS NULL`, doc); n != 1 {
		t.Fatalf("an unlinked document's object: %d", n) // the link goes, the object stays (main spec §9.3)
	}
	// The same list again changes nothing (no event).
	events := w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1`, id)
	expect(t, w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "insurance", "status": "in_progress",
		"responsible": "Somchai", "documents": []string{doc2}}), http.StatusOK, "")
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1`, id); n != events {
		t.Fatalf("an unchanged renewal save wrote %d events", n-events)
	}
	r = w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "tax", "insuranceNotes": "x", "documents": []string{doc2},
		"status": "in_progress"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["insuranceNotes"] != "not_available" || f["documents"] != "not_available" {
		t.Fatalf("insurance fields on a tax renewal: %v", f)
	}

	// The ledger is append-only for the app role.
	for _, what := range []string{"update-transactions", "delete-transactions"} {
		r := w.post("/v1/fleettest/mutate/"+what, taO, nil)
		if r.data()["sqlstate"] != "42501" {
			t.Fatalf("%s: %s", what, r.raw)
		}
	}
	// A carrier admin records renewals of its own trucks; the own fleet's trucks are not found. Without notes the
	// ledger row carries the legacy text, never the truck's insurance notes.
	r = w.post("/v1/trucks/"+id+"/renewals", w.login("ta.a"), map[string]any{"type": "tax", "amount": 1,
		"expiryDate": "2028-10-01"})
	expect(t, r, http.StatusCreated, "")
	if tr := r.data()["transaction"].(map[string]any); tr["notes"] != "Expiry: 2028-10-01" {
		t.Fatalf("tax ledger note: %v", tr)
	}
	r = w.post("/v1/trucks", taO, newTruck("กก-7002", nil))
	expect(t, w.post("/v1/trucks/"+str(r.data()["id"])+"/renewals", w.login("ta.a"), map[string]any{"type": "tax",
		"amount": 1, "expiryDate": "2028-10-01"}), http.StatusNotFound, "not_found")
}

// A truck may hold more files of a kind than one request may newly link (renewals append without a cap): its own
// payload goes back in a PATCH, and only the new keys of a request count against MaxFilesPerKind.
func TestTruckFilesAfterRenewals(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")
	r := w.post("/v1/trucks", taO, newTruck("กก-7101", nil))
	expect(t, r, http.StatusCreated, "")
	id := str(r.data()["id"])
	docs := func(n int, prefix string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = w.uploadNamed(taO, "insurance_document", id, "application/pdf", fmt.Sprint(prefix, i), pdf)
		}
		return out
	}
	held := append(docs(11, "a"), docs(11, "b")...)
	for _, batch := range [][]string{held[:11], held[11:]} {
		expect(t, w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "insurance", "status": "in_progress",
			"documentKeys": batch}), http.StatusOK, "")
	}
	files := w.get("/v1/trucks/"+id, taO).data()["files"].(map[string]any)
	if n := len(files["insuranceDocument"].([]any)); n != 22 {
		t.Fatalf("insurance documents after two renewals: %d", n)
	}
	// The truck's own 22 documents go back with an unrelated change: the files are unchanged.
	events := w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1`, id)
	r = w.patch("/v1/trucks/"+id, taO, map[string]any{"notes": "echo", "files": map[string]any{"insuranceDocument": held}})
	expect(t, r, http.StatusOK, "")
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND routing_key = 'truck.updated'
		AND payload->'changed' = '["notes"]'::jsonb`, id); n != 1 || w.count(`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1`, id) != events+1 {
		t.Fatalf("sending the files back: %d notes-only events", n)
	}
	// 21 new keys in one request are too many; 20 are fine, next to the 22 the truck holds.
	fresh := docs(21, "c")
	r = w.patch("/v1/trucks/"+id, taO, map[string]any{"files": map[string]any{"insuranceDocument": append(slices.Clone(held), fresh...)}})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["files.insuranceDocument"] != "too_many" {
		t.Fatalf("21 new keys: %v", r.fields())
	}
	if n := w.count(`SELECT count(*) FROM truck_files WHERE truck_id = $1`, id); n != 22 {
		t.Fatalf("a refused PATCH linked files: %d", n)
	}
	r = w.patch("/v1/trucks/"+id, taO, map[string]any{"files": map[string]any{"insuranceDocument": append(slices.Clone(held), fresh[:20]...)}})
	expect(t, r, http.StatusOK, "")
	if n := len(r.data()["files"].(map[string]any)["insuranceDocument"].([]any)); n != 42 {
		t.Fatalf("documents after 20 new keys: %d", n)
	}

	// A null or blank startDate on a tax renewal is absent, like every other insurance-only field.
	for _, start := range []any{nil, ""} {
		expect(t, w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "tax", "amount": 100,
			"expiryDate": "2027-10-11", "startDate": start, "provider": nil, "policyNumber": ""}), http.StatusCreated, "")
	}
	r = w.post("/v1/trucks/"+id+"/renewals", taO, map[string]any{"type": "tax", "amount": 100, "expiryDate": "2027-10-11",
		"startDate": "2026-10-11"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["startDate"] != "not_available" {
		t.Fatalf("a startDate on a tax renewal: %v", r.fields())
	}
}

// The GPS vehicle id resolves the platform's Cartrack vehicles across tenants (trucks_gps is global): only a steward
// sets or changes it, so a carrier can neither learn which ids other tenants hold nor squat one; a carrier still sends
// its truck's stored id back or clears it.
func TestGPSVehicleIDIsStewardOnly(t *testing.T) {
	w := newWorld(t)
	taO, taC := w.login("ta.o"), w.login("ta.c")
	expect(t, w.post("/v1/trucks", taO, newTruck("กก-7201", map[string]any{"gpsVehicleId": "CT-OWN-7"})), http.StatusCreated, "")
	notEditable := func(r resp) {
		t.Helper()
		expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
		if r.fields()["gpsVehicleId"] != "not_editable" {
			t.Fatalf("gpsVehicleId: %s", r.raw)
		}
	}
	trucks := w.count(`SELECT count(*) FROM trucks`)
	notEditable(w.post("/v1/trucks", taC, newTruck("กก-7202", map[string]any{"gpsVehicleId": "CT-OWN-7"}))) // held by O: no 409
	notEditable(w.post("/v1/trucks", taC, newTruck("กก-7202", map[string]any{"gpsVehicleId": "CT-FREE"})))  // free: no squatting
	for _, dry := range []bool{true, false} {
		r := w.post("/v1/trucks/import", taC, map[string]any{"dryRun": dry, "rows": []map[string]any{
			{"licensePlate": "กก-7203", "province": "x", "type": "Van", "brand": "x", "model": "x", "color": "x", "year": 2020,
				"gpsVehicleId": "CT-OWN-7"},
			{"licensePlate": "กก-7204", "province": "x", "type": "Van", "brand": "x", "model": "x", "color": "x", "year": 2020,
				"gpsVehicleId": "CT-NONE"},
		}})
		expect(t, r, http.StatusOK, "")
		errs := r.data()["errors"].([]any)
		if len(errs) != 2 || r.data()["created"] != float64(0) {
			t.Fatalf("import dryRun=%v: %s", dry, r.raw)
		}
		for _, e := range errs {
			f := e.(map[string]any)["fields"].([]any)
			if len(f) != 1 || f[0].(map[string]any)["field"] != "gpsVehicleId" || f[0].(map[string]any)["reason"] != "not_editable" {
				t.Fatalf("import row: %v", e)
			}
		}
	}
	if n := w.count(`SELECT count(*) FROM trucks`); n != trucks {
		t.Fatalf("refused writes created trucks: %d -> %d", trucks, n)
	}
	// The carrier's own truck with a legacy id: sent back unchanged (with another change) or cleared, never changed.
	own := w.truck(w.C, "กก-7205", now, "gps_vehicle_id", "'CT-C-1'")
	expect(t, w.patch("/v1/trucks/"+own, taC, map[string]any{"gpsVehicleId": "CT-C-1", "notes": "x"}), http.StatusOK, "")
	notEditable(w.patch("/v1/trucks/"+own, taC, map[string]any{"gpsVehicleId": "CT-OWN-7"}))
	r := w.patch("/v1/trucks/"+own, taC, map[string]any{"gpsVehicleId": nil})
	expect(t, r, http.StatusOK, "")
	if v, ok := r.data()["gpsVehicleId"]; !ok || v != nil {
		t.Fatalf("cleared: %s", r.raw)
	}
	// A steward keeps the 409 of the global key; and sets the id on a carrier truck of its reach.
	r = w.post("/v1/trucks", taO, newTruck("กก-7206", map[string]any{"gpsVehicleId": "CT-OWN-7"}))
	expect(t, r, http.StatusConflict, "already_exists")
	r = w.post("/v1/trucks", taO, newTruck("1กข-7207", map[string]any{"tenantId": w.A, "gpsVehicleId": "CT-A-1"}))
	expect(t, r, http.StatusCreated, "")
	notEditable(w.patch("/v1/trucks/"+str(r.data()["id"]), w.login("ta.a"), map[string]any{"gpsVehicleId": "CT-A-2"}))
}

// --- import -------------------------------------------------------------------------------------------------

func TestImportJSON(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")
	expect(t, w.post("/v1/trucks", taO, newTruck("กก-8001", map[string]any{"brand": "Hino"})), http.StatusCreated, "")
	expect(t, w.post("/v1/trucks", taO, newTruck("กก-8002", nil)), http.StatusCreated, "")
	rows := []map[string]any{
		{"licensePlate": "กก-8101", "province": "กรุงเทพมหานคร", "type": "6 Wheels", "brand": "Isuzu", "model": "FRR",
			"color": "White", "year": 2566, "status": "Active", "ownership": "Company", "registrationDate": "15/01/2023"}, // 1: created
		{"licensePlate": "AB-12", "province": "x", "type": "x", "brand": "x", "model": "x", "color": "x", "year": 2020},   // 2: plate
		{"licensePlate": "กก-8102", "province": "x", "type": "x", "brand": "x", "model": "x", "year": 2020},               // 3: color
		{"licensePlate": "กก-8101", "province": "x", "type": "x", "brand": "x", "model": "x", "color": "x", "year": 2020}, // 4: repeated
		{"licensePlate": "กก-8001", "brand": "Isuzu", "status": "maintenance"},                                            // 5: updated
		{"licensePlate": "กก-8002", "brand": "Isuzu"},                                                                     // 6: unchanged
		{"licensePlate": "กก-8103", "province": "x", "type": "Trailer", "brand": "x", "model": "x", "color": "x",
			"year": 2020, "ownership": "Subcontractor"}, // 7: not the tenant's ownership
		{"licensePlate": "กก-8104", "province": "x", "type": "Trailer", "brand": "x", "model": "x", "color": "x",
			"year": 2020, "status": "in-transit"}, // 8: status
	}
	dry := w.post("/v1/trucks/import", taO, map[string]any{"rows": rows, "dryRun": true})
	expect(t, dry, http.StatusOK, "")
	trucks := w.count(`SELECT count(*) FROM trucks`)
	if trucks != 2 || dry.data()["dryRun"] != true || dry.data()["created"] != float64(1) {
		t.Fatalf("dry run wrote or miscounted (%d trucks): %s", trucks, dry.raw)
	}

	r := w.post("/v1/trucks/import", taO, map[string]any{"rows": rows})
	expect(t, r, http.StatusOK, "")
	d := r.data()
	if d["created"] != float64(1) || d["updated"] != float64(1) || d["unchanged"] != float64(1) {
		t.Fatalf("counts: %s", r.raw)
	}
	// AC: per-row reasons for the rejected rows.
	want := map[int]map[string]string{
		2: {"licensePlate": "invalid"}, 3: {"color": "required"}, 4: {"licensePlate": "duplicate_in_file"},
		7: {"ownership": "mismatch"}, 8: {"status": "invalid"},
	}
	errs := d["errors"].([]any)
	if len(errs) != len(want) {
		t.Fatalf("errors: %s", r.raw)
	}
	for _, e := range errs {
		m := e.(map[string]any)
		row := int(m["row"].(float64))
		got := map[string]string{}
		for _, f := range m["fields"].([]any) {
			got[str(f.(map[string]any)["field"])] = str(f.(map[string]any)["reason"])
		}
		for f, reason := range want[row] {
			if got[f] != reason || len(got) != len(want[row]) {
				t.Errorf("row %d: %v, want %v", row, got, want[row])
			}
		}
	}
	if n := w.count(`SELECT count(*) FROM trucks WHERE license_plate = 'กก-8101' AND year = 2023 AND vehicle_class = '6WH'
		AND status = 'active' AND registration_date = '2023-01-15' AND ownership_type = 'own'`); n != 1 {
		t.Fatal("the created row")
	}
	if n := w.count(`SELECT count(*) FROM trucks WHERE license_plate = 'กก-8001' AND brand = 'Isuzu' AND status = 'maintenance'`); n != 1 {
		t.Fatal("the updated row")
	}
	if n := w.count(`SELECT count(*) FROM status_history h JOIN trucks t ON t.id = h.entity_id
		WHERE t.license_plate = 'กก-8001' AND h.status = 'maintenance' AND h.previous_status = 'active' AND h.reason = 'imported'`); n != 1 {
		t.Fatal("the updated row's status entry")
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key IN ('truck.created', 'truck.updated')`); n != 4 {
		t.Fatalf("events: %d (2 creates + 1 import create + 1 import update)", n)
	}

	// A carrier of the reach as the target; rows over the limit; capabilities.
	r = w.post("/v1/trucks/import", taO, map[string]any{"tenantId": w.A, "rows": []map[string]any{{"licensePlate": "1กข-8201",
		"province": "x", "type": "Van", "brand": "x", "model": "x", "color": "x", "year": 2020, "ownership": "subcontractor"}}})
	expect(t, r, http.StatusOK, "")
	if r.data()["created"] != float64(1) || w.count(`SELECT count(*) FROM trucks WHERE tenant_id = $1 AND license_plate = '1กข-8201'`, w.A) != 1 {
		t.Fatalf("import into a carrier: %s", r.raw)
	}
	many := make([]map[string]any, fleet.MaxImportRows+1)
	expect(t, w.post("/v1/trucks/import", taO, map[string]any{"rows": many}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.post("/v1/trucks/import", taO, map[string]any{"rows": []map[string]any{}}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.post("/v1/trucks/import", w.login("op.o"), map[string]any{"rows": rows}), http.StatusForbidden, "permission_denied")
}

func TestImportXLSX(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	reg := time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC)
	// The legacy template (TruckImportDialog.tsx downloadTemplate), its sample row deleted.
	for i, row := range [][]any{
		{"License Plate", "Province", "Type", "Brand", "Model", "Color", "Year", "VIN", "Engine Number", "Fuel Type",
			"Ownership", "Status", "Registration Date", "Max Load", "Tax Expiry Date"},
		{"กก-9001", "กรุงเทพมหานคร", "10W", "Isuzu", "FXZ360", "White", 2023, "", "", "Diesel", "Company", "Active", reg, 25000,
			time.Date(2567, 3, 31, 0, 0, 0, 0, time.UTC)}, // a date cell typed in the Buddhist era (31/03/2567)
		{"กก-9002", "นนทบุรี", "Trailer", "Hino", "500", "Blue", "2565", "", "", "Diesel", "Company", "", nil, nil},
		{},
		{"9กก-9003", "นนทบุรี", "Van", "Toyota", "Commuter", "", 2020},
	} {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	style, _ := f.NewStyle(&excelize.Style{NumFmt: 14})
	_ = f.SetCellStyle("Sheet1", "M2", "M2", style)
	thai := "dd/mm/yyyy"
	thaiStyle, _ := f.NewStyle(&excelize.Style{CustomNumFmt: &thai})
	_ = f.SetCellStyle("Sheet1", "O2", "O2", thaiStyle)
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	r := w.multipart("/v1/trucks/import", taO, "trucks.xlsx", buf.Bytes(), nil)
	expect(t, r, http.StatusOK, "")
	d := r.data()
	errs := d["errors"].([]any)
	if d["created"] != float64(2) || len(errs) != 1 || errs[0].(map[string]any)["row"] != float64(5) {
		t.Fatalf("xlsx import: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM trucks WHERE license_plate = 'กก-9001' AND vehicle_class = '10WH'
		AND registration_date = '2023-01-15' AND max_load_weight_kg = 25000 AND fuel_type = 'Diesel'
		AND tax_expiry_date = '2024-03-31'`); n != 1 {
		t.Fatal("row 2 of the sheet")
	}
	// The Buddhist-era date cell landed on 2024-03-31: overdue in the renewal overview, not 543 years away.
	var tax map[string]any
	for _, it := range w.get("/v1/trucks/renewals?due=30", taO).list() {
		if it["licensePlate"] == "กก-9001" {
			tax = it["tax"].(map[string]any)
		}
	}
	if tax == nil || tax["state"] != "overdue" || tax["expiryDate"] != "2024-03-31" || tax["daysRemaining"] != float64(-924) {
		t.Fatalf("renewal of the imported truck: %v", tax)
	}
	if n := w.count(`SELECT count(*) FROM trucks WHERE license_plate = 'กก-9002' AND vehicle_class IS NULL AND year = 2022
		AND status = 'active'`); n != 1 {
		t.Fatal("row 3 of the sheet")
	}
	// CSV works too; a sheet without a plate column and a file that is no spreadsheet are 422 on the file.
	csv := "licensePlate,province,type,brand,model,color,year\n1กข-9101,ตาก,Pickup,Toyota,Revo,ขาว,2019\n"
	r = w.multipart("/v1/trucks/import", taO, "trucks.csv", []byte(csv), map[string]string{"dryRun": "true"})
	expect(t, r, http.StatusOK, "")
	if r.data()["created"] != float64(1) || r.data()["dryRun"] != true || w.count(`SELECT count(*) FROM trucks WHERE license_plate = '1กข-9101'`) != 0 {
		t.Fatalf("csv dry run: %s", r.raw)
	}
	r = w.multipart("/v1/trucks/import", taO, "x.csv", []byte("province,brand\nx,y\n"), nil)
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["file"] != "missing_column" {
		t.Fatalf("missing column: %v", r.fields())
	}
	r = w.multipart("/v1/trucks/import", taO, "x.bin", []byte{0, 1, 2, 3}, nil)
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	// A row reaching column XFD is refused at once (excelize pads it to 16384 cells).
	wide := excelize.NewFile()
	_ = wide.SetCellValue("Sheet1", "A1", "licensePlate")
	_ = wide.SetCellValue("Sheet1", "XFD2", "x")
	buf.Reset()
	if err := wide.Write(&buf); err != nil {
		t.Fatal(err)
	}
	_ = wide.Close()
	r = w.multipart("/v1/trucks/import", taO, "wide.xlsx", buf.Bytes(), nil)
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["file"] != "too_many_columns" {
		t.Fatalf("wide sheet: %s", r.raw)
	}
	r = w.multipart("/v1/trucks/import", taO, "", nil, map[string]string{"tenantId": w.A})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["file"] != "required" {
		t.Fatalf("no file: %v", r.fields())
	}
}

// --- list: keyset without a cap, filters, the dispatcher projection ----------------------------------------

// AC: the list returns more than 100 trucks through the cursor (a fixture of 150; the web capped it at 100).
func TestTruckListPaging(t *testing.T) {
	w := newWorld(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var all []string
	for i := range 150 {
		status := "active"
		if i%10 == 0 {
			status = "maintenance"
		}
		all = append(all, w.truck(w.O, fmt.Sprintf("กก-%d", 1000+i), base.Add(time.Duration(i)*time.Minute), "status", "'"+status+"'"))
	}
	slices.Reverse(all) // newest first
	taO := w.login("ta.o")
	collect := func(limit string) ([]string, int) {
		var ids []string
		pages := 0
		cur := ""
		for {
			path := "/v1/trucks?" + limit
			if cur != "" {
				path += "&cursor=" + url.QueryEscape(cur)
			}
			r := w.get(path, taO)
			expect(t, r, http.StatusOK, "")
			pages++
			for _, it := range r.list() {
				ids = append(ids, str(it["id"]))
			}
			if cur = r.next(); cur == "" {
				return ids, pages
			}
			if pages > 100 {
				t.Fatal("no end")
			}
		}
	}
	for _, c := range []struct {
		limit string
		pages int
	}{{"", 3}, {"limit=100", 2}, {"limit=500", 1}, {"limit=7", 22}} {
		ids, pages := collect(c.limit)
		if !slices.Equal(ids, all) || pages != c.pages {
			t.Fatalf("%q: %d ids in %d pages", c.limit, len(ids), pages)
		}
	}
	if r := w.get("/v1/trucks?status=maintenance", taO); len(r.list()) != 15 {
		t.Fatalf("status filter: %d", len(r.list()))
	}
	if r := w.get("/v1/trucks?q="+url.QueryEscape("กก-1149"), taO); len(r.list()) != 1 {
		t.Fatalf("q: %d", len(r.list()))
	}
	if r := w.get("/v1/trucks?type=4wj&limit=500", taO); len(r.list()) != 150 {
		t.Fatalf("type by class: %d", len(r.list()))
	}
	if r := w.get("/v1/trucks?type="+url.QueryEscape("4 wheels jumbo")+"&limit=500", taO); len(r.list()) != 150 {
		t.Fatalf("type by word: %d", len(r.list()))
	}
	expect(t, w.get("/v1/trucks?limit=501", taO), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/trucks?cursor=bad", taO), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/trucks?status=parked", taO), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/trucks?sort=plate", taO), http.StatusBadRequest, "bad_request")
	// Text PostgreSQL cannot hold (not UTF-8, NUL) is refused before SQL as 422, never a 500 (TW8).
	for _, query := range []string{"q=%FF", "q=a%00b", "type=%FF", "type=a%00b"} {
		r := w.get("/v1/trucks?"+query, taO)
		expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
		if f := r.fields(); f["q"] != "invalid" && f["type"] != "invalid" {
			t.Errorf("%s: %s", query, r.raw)
		}
	}
}

// A dispatcher reads trucks through the scope_trucks projection only (Appendix C §C.3.7, R85): its own organisation's
// and those of recent work for its grant, without insurance, tax, cost or document columns.
func TestDispatcherReadsTheProjection(t *testing.T) {
	w := newWorld(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	inScope := w.truck(w.A, "กก-1201", base)
	_ = w.truck(w.A, "กก-1202", base, "insurance_company", "'Secret Insurer'")
	own := w.truck(w.D, "กก-1203", base)
	w.exec(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		billing_party_id, truck_id) VALUES ($1, 'form', 'first_mile', 'pending', now(), 'SRC', 'DST', $2, $3)`, w.A, w.partyX, inScope)
	ds := w.login("ds")
	r := w.get("/v1/trucks", ds)
	expect(t, r, http.StatusOK, "")
	var ids []string
	for _, it := range r.list() {
		ids = append(ids, str(it["id"]))
		for k := range it {
			if !slices.Contains([]string{"id", "tenantId", "licensePlate", "province", "vehicleClass", "type", "status"}, k) {
				t.Fatalf("the projection carries %s: %v", k, it)
			}
		}
	}
	slices.Sort(ids)
	want := []string{inScope, own}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("dispatcher reads %v, want %v", ids, want)
	}
	// The detail is the full row: the dispatcher reads it for its own organisation's trucks only (it is an operator
	// of D), never for a truck its grant opens to the projection.
	expect(t, w.get("/v1/trucks/"+own, ds), http.StatusOK, "")
	expect(t, w.get("/v1/trucks/"+inScope, ds), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/trucks?ownership=own", ds), http.StatusUnprocessableEntity, "invalid_argument")
	p1 := w.get("/v1/trucks?limit=1", ds)
	p2 := w.get("/v1/trucks?limit=1&cursor="+url.QueryEscape(p1.next()), ds)
	if len(p1.list()) != 1 || len(p2.list()) != 1 || p2.next() != "" || p1.list()[0]["id"] == p2.list()[0]["id"] {
		t.Fatalf("projection pages: %s | %s", p1.raw, p2.raw)
	}
}
