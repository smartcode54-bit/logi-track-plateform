//go:build integration

package expenses_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses/expensesdb"
)

func day(d int, hh int) time.Time { return time.Date(2026, 10, d, hh, 0, 0, 0, time.UTC) }

// AC "Expense list never scans other types" (features/accounting/api/expenses.ts:90-101 read every expense and
// filtered the type in the browser): type and status are SQL filters, a caller reads only the types its capabilities
// name, the plan of the list query filters the type in its index condition, and the keyset pages have no cap.
func TestListServerFilter(t *testing.T) {
	w := newWorld(t)
	for i := range 3 {
		st := "pending"
		if i == 2 {
			st = "approved"
		}
		w.expense(w.O, "fuel", w.dO, w.tO, day(1+i, 2), 1000+float64(i), st, "odometer_km", "90000")
	}
	w.expense(w.O, "other", w.dO, w.tO, day(4, 2), 45, "pending", "category", "'toll'")
	w.expense(w.O, "other", w.dO2, "", day(5, 2), 45, "pending", "category", "'toll'")
	w.expense(w.O, "other", w.dO, w.tO, day(6, 2), 60, "approved", "category", "'parking'")
	w.expense(w.O, "other", w.dO, w.tO, day(7, 2), 70, "rejected", "category", "'other'")
	w.expense(w.A, "fuel", w.dA, w.tA, day(8, 2), 900, "pending")
	w.expense(w.C, "fuel", w.dC, w.tC, day(9, 2), 800, "pending")

	ta := w.login("ta.o")
	r := w.get("/v1/expenses?type=fuel", ta)
	expect(t, r, http.StatusOK, "")
	if n := len(r.list()); n != 4 { // O's three and carrier A's (contractor reach); never C's
		t.Fatalf("fuel rows %d: %s", n, r.raw)
	}
	for _, e := range r.list() {
		if e["type"] != "fuel" {
			t.Fatalf("a fuel list holds %v", e["type"])
		}
	}
	counts := r.meta()["statusCounts"].(map[string]any)
	if num(counts["pending"]) != 3 || num(counts["approved"]) != 1 || num(counts["rejected"]) != 0 {
		t.Errorf("statusCounts %v", counts)
	}
	first := r.list()[0]
	if str(first["expenseDate"]) != "2026-10-08" || str(first["licensePlate"]) != "กค-2001" || str(first["driverName"]) != "Anan ดี" {
		t.Errorf("newest first, plate snapshot and driver name: %v", first)
	}
	if r = w.get("/v1/expenses?type=other&status=pending", ta); len(r.list()) != 2 {
		t.Fatalf("other pending %d", len(r.list()))
	}
	if r = w.get("/v1/expenses?status=PENDING", ta); len(r.list()) != 5 { // the legacy literal
		t.Fatalf("pending (legacy literal) %d", len(r.list()))
	}
	if r = w.get("/v1/expenses", ta); len(r.list()) != 8 {
		t.Fatalf("every type %d", len(r.list()))
	}
	if r = w.get("/v1/expenses?type=other&category=toll&driverId="+w.dO, ta); len(r.list()) != 1 {
		t.Fatalf("category + driver %d", len(r.list()))
	}
	if r = w.get("/v1/expenses?truckId="+w.tO+"&from=2026-10-02&to=2026-10-04", ta); len(r.list()) != 3 {
		t.Fatalf("truck + days %d", len(r.list()))
	}
	r = w.get("/v1/expenses?type=diesel&status=done&from=2026-13-01&limit=501&cursor=x", ta)
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["type"] != "invalid" || f["status"] != "invalid" || f["from"] != "invalid" || f["limit"] != "out_of_range" ||
		f["cursor"] != "invalid" {
		t.Errorf("violations %v", f)
	}
	expect(t, w.get("/v1/expenses?sort=amount", ta), http.StatusBadRequest, "bad_request")

	// A user whose tenant granted accounting:view_fuel only reads fuel rows, and another type is 403.
	us := w.login("us.o")
	if r = w.get("/v1/expenses", us); len(r.list()) != 4 {
		t.Fatalf("view_fuel only: %d rows", len(r.list()))
	}
	r = w.get("/v1/expenses?type=other", us)
	expect(t, r, http.StatusForbidden, "permission_denied")
	if m, _ := r.detail("missingCapability").([]any); len(m) == 0 || m[0] != "accounting:view_other" {
		t.Errorf("missingCapability %v", r.detail("missingCapability"))
	}
	expect(t, w.get("/v1/expenses", w.login("op.o")), http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/expenses", w.login("ds")), http.StatusForbidden, "permission_denied") // never a dispatcher
	if r = w.get("/v1/expenses", w.login("ta.c")); len(r.list()) != 1 || r.list()[0]["tenantId"] != w.C {
		t.Fatalf("carrier C reads its own row only: %s", r.raw)
	}
	if r = w.get("/v1/expenses", w.login("ta.a")); len(r.list()) != 1 || r.list()[0]["tenantId"] != w.A {
		t.Fatalf("carrier A reads its own row only: %s", r.raw)
	}

	// The plan of the list as served: on a table where other expenses (tolls) outnumber fuel logs, the fuel page reads
	// vehicle_expenses_type_date with the type in its index condition, never the other rows.
	w.exec(`INSERT INTO vehicle_expenses (tenant_id, tenant_source, driver_id, expense_type, category, expense_at, amount_thb)
		SELECT $1, 'driver', $2, 'other', 'toll', now() - make_interval(mins => g), 45 FROM generate_series(1, 5000) g`, w.O, w.dO)
	w.exec(`ANALYZE vehicle_expenses`)
	var plan []byte
	ctx := context.Background()
	if err := w.etl.QueryRow(ctx, `EXPLAIN (FORMAT JSON) `+expensesdb.ListExpenses, false, []string{w.O}, []string{"fuel"},
		nil, nil, nil, nil, nil, nil, nil, "00000000-0000-0000-0000-000000000000", 51).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan), `"Index Name": "vehicle_expenses_type_date"`) ||
		!strings.Contains(string(plan), "expense_type = ANY") || strings.Contains(string(plan), `"Node Type": "Seq Scan"`) {
		t.Errorf("the fuel list does not read through vehicle_expenses_type_date: %s", plan)
	}
}

// Keyset pages: no cap, no row twice, newest first, statusCounts on the first page only.
func TestListPages(t *testing.T) {
	w := newWorld(t)
	at := day(1, 0)
	for i := range 120 {
		w.expense(w.O, "fuel", w.dO, w.tO, at.Add(time.Duration(i%7)*time.Hour), 10, "pending")
	}
	ta := w.login("ta.o")
	seen := map[string]bool{}
	var last time.Time
	cursor, pages := "", 0
	for {
		path := "/v1/expenses?type=fuel&limit=50"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := w.get(path, ta)
		expect(t, r, http.StatusOK, "")
		if (pages == 0) != (r.meta() != nil) {
			t.Fatalf("page %d meta %v", pages, r.meta())
		}
		for _, e := range r.list() {
			id := str(e["id"])
			if seen[id] {
				t.Fatalf("row %s twice", id)
			}
			seen[id] = true
			d, _ := time.Parse(time.RFC3339, str(e["date"]))
			if !last.IsZero() && d.After(last) {
				t.Fatalf("not newest first")
			}
			last = d
		}
		pages++
		if cursor = r.next(); cursor == "" {
			break
		}
	}
	if len(seen) != 120 || pages != 3 {
		t.Fatalf("%d rows in %d pages", len(seen), pages)
	}
}

// AC "A second fuel row with the same (driver_id, tax_inv_id) -> 409 duplicate_tax_invoice": staff entries and edits
// meet it, other drivers and other rows do not, and the D5 unique index of 0010 holds below the API.
func TestDuplicateTaxInvoice(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	body := map[string]any{"type": "fuel", "driverId": w.dO, "date": "2026-10-10T08:00", "amountThb": 1500,
		"volumeLiters": 40.5, "pricePerLiter": 37.04, "taxInvId": "INV-001", "stationTaxId": "0105536000001"}
	r := w.post("/v1/expenses", ta, body)
	expect(t, r, http.StatusCreated, "")
	e := r.data()
	if e["status"] != "approved" || e["truckId"] != w.tO || e["licensePlate"] != "กข-1001" ||
		e["adminNote"] != "บันทึกโดย admin (ข้อมูลจาก petro app)" || e["createdBy"] == nil || e["pmCheck"] != nil {
		t.Fatalf("staff entry: %s", r.raw)
	}
	if *w.str(`SELECT tenant_source FROM vehicle_expenses WHERE id = $1`, e["id"]) != "driver" {
		t.Error("tenant_source")
	}
	r = w.post("/v1/expenses", ta, body)
	expect(t, r, http.StatusConflict, "duplicate_tax_invoice")
	if r.detail("field") != "taxInvId" {
		t.Errorf("details %v", r.body)
	}
	// Another driver may hold the same invoice number; an other row has no tax invoice.
	body["driverId"] = w.dO2
	expect(t, w.post("/v1/expenses", ta, body), http.StatusCreated, "")
	r = w.post("/v1/expenses", ta, map[string]any{"type": "other", "driverId": w.dO, "date": "2026-10-10", "amountThb": 50,
		"taxInvId": "INV-001", "category": "parking"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["taxInvId"] != "not_available" {
		t.Errorf("violations %v", r.fields())
	}
	// An edit that takes another fuel row's invoice of the same driver.
	other := w.expense(w.O, "fuel", w.dO, w.tO, day(9, 1), 900, "pending", "tax_inv_id", "'INV-002'")
	expect(t, w.patch("/v1/expenses/"+other, ta, map[string]any{"taxInvId": "INV-001"}), http.StatusConflict, "duplicate_tax_invoice")
	expect(t, w.patch("/v1/expenses/"+other, ta, map[string]any{"taxInvId": "INV-003"}), http.StatusOK, "")
	// Two concurrent entries of one invoice: one 201, one 409.
	var wg sync.WaitGroup
	statuses := make([]int, 4)
	for i := range statuses {
		wg.Go(func() {
			statuses[i] = w.post("/v1/expenses", ta, map[string]any{"type": "fuel", "driverId": w.dO, "date": "2026-10-10",
				"amountThb": 10, "taxInvId": "INV-RACE"}).status
		})
	}
	wg.Wait()
	slices.Sort(statuses)
	if statuses[0] != http.StatusCreated || statuses[1] != http.StatusConflict {
		t.Fatalf("concurrent entries %v", statuses)
	}
	// The index of 0010 (vehicle_expenses_fuel_taxinv) refuses a duplicate written below the API too.
	_, err := w.etl.Exec(context.Background(), `INSERT INTO vehicle_expenses (tenant_id, tenant_source, driver_id, expense_type,
		expense_at, amount_thb, tax_inv_id) VALUES ($1, 'driver', $2, 'fuel', now(), 1, 'INV-001')`, w.O, w.dO)
	if pe, ok := err.(*pgconn.PgError); !ok || pe.Code != "23505" || pe.ConstraintName != "vehicle_expenses_fuel_taxinv" {
		t.Fatalf("the D5 index: %v", err)
	}
}

// AC "Approving a fuel log over the threshold creates exactly one PM Booking": the PM check runs in the approval's
// transaction; later approvals, a bulk approval and concurrent approvals of the same truck find the booking.
func TestApprovalCreatesOnePMBooking(t *testing.T) {
	w := newWorld(t)
	odo := func(km string) []string { return []string{"odometer_km", km} }
	r1 := w.expense(w.O, "fuel", w.dO, w.tO, day(1, 1), 1500, "pending", odo("98500")...)
	r2 := w.expense(w.O, "fuel", w.dO, w.tO, day(2, 1), 1500, "pending", odo("98700")...)
	r3 := w.expense(w.O, "fuel", w.dO, w.tO, day(3, 1), 1500, "pending", odo("99000")...)
	toll := w.expense(w.O, "other", w.dO, w.tO, day(3, 2), 45, "pending", "category", "'toll'")

	os := w.login("os.o") // operation_staff: accounting:audit_expense
	r := w.post("/v1/expenses/"+r1+"/status", os, map[string]any{"status": "APPROVED", "adminNote": " ok "})
	expect(t, r, http.StatusOK, "")
	pm, _ := r.data()["pmCheck"].(map[string]any)
	if r.data()["status"] != "approved" || r.data()["adminNote"] != "ok" || pm == nil || pm["created"] != true || pm["outcome"] != "created" {
		t.Fatalf("approval: %s", r.raw)
	}
	booking := str(pm["maintenanceId"])
	var status, notes, start string
	var mileage, next int
	if err := w.etl.QueryRow(context.Background(), `SELECT status, notes, start_date::text, current_mileage, next_service_mileage
		FROM maintenance_records WHERE id = $1`, booking).Scan(&status, &notes, &start, &mileage, &next); err != nil {
		t.Fatal(err)
	}
	if status != "pm_booking" || start != "2026-10-11" || mileage != 98500 || next != 100000 ||
		notes != "ระบบอัตโนมัติ: จากรายการเติมน้ำมัน (98,500 / 100,000 กม. เหลือ 1,500 กม. ≤ 2,000 กม.)" {
		t.Fatalf("booking %s %q %s %d %d", status, notes, start, mileage, next)
	}
	if n := w.count(`SELECT count(*) FROM trucks WHERE id = $1 AND active_maintenance_id = $2 AND last_alert_mileage = 98500
		AND current_mileage = 98500`, w.tO, booking); n != 1 {
		t.Fatal("the truck does not hold the booking")
	}
	r = w.post("/v1/expenses/"+r2+"/status", os, map[string]any{"status": "approved"})
	if pm, _ := r.data()["pmCheck"].(map[string]any); pm["created"] != false || pm["outcome"] != "active_maintenance" {
		t.Fatalf("second approval: %s", r.raw)
	}
	r = w.post("/v1/expenses/bulk-status", os, map[string]any{"ids": []string{r3, toll, r3}, "status": "approved"})
	expect(t, r, http.StatusOK, "")
	rows := r.body["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("bulk rows %d", len(rows))
	}
	for _, row := range rows {
		m := row.(map[string]any)
		switch m["id"] {
		case r3:
			if pm, _ := m["pmCheck"].(map[string]any); pm["outcome"] != "active_maintenance" {
				t.Errorf("bulk fuel row %v", m)
			}
		case toll:
			if m["pmCheck"] != nil || m["status"] != "approved" {
				t.Errorf("bulk toll row %v", m)
			}
		}
	}
	if n := w.count(`SELECT count(*) FROM maintenance_records WHERE truck_id = $1`, w.tO); n != 1 {
		t.Fatalf("%d PM bookings, want exactly one", n)
	}
	if w.events("maintenance.created") != 1 || w.events("expense.approved") != 4 {
		t.Errorf("events: maintenance.created %d expense.approved %d", w.events("maintenance.created"), w.events("expense.approved"))
	}
	// Re-approving changes nothing and emits nothing.
	r = w.post("/v1/expenses/"+r1+"/status", os, map[string]any{"status": "approved"})
	if r.data()["pmCheck"] != nil || w.events("expense.approved") != 4 {
		t.Fatalf("re-approval: %s", r.raw)
	}
	// Concurrent approvals of one truck over the threshold: one booking.
	truck := w.truck(w.O, "กข-1003", "next_service_mileage", "50000")
	var ids []string
	for i := range 6 {
		ids = append(ids, w.expense(w.O, "fuel", w.dO2, truck, day(4, i), 100, "pending", odo(strconv.Itoa(49000+10*i))...))
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Go(func() { w.post("/v1/expenses/"+id+"/status", os, map[string]any{"status": "approved"}) })
	}
	wg.Wait()
	if n := w.count(`SELECT count(*) FROM maintenance_records WHERE truck_id = $1`, truck); n != 1 {
		t.Fatalf("%d bookings after concurrent approvals", n)
	}
	if n := w.count(`SELECT count(*) FROM vehicle_expenses WHERE truck_id = $1 AND status = 'approved'`, truck); n != 6 {
		t.Fatalf("%d approved", n)
	}

	// Within the threshold the truck's mileage moves; carrier A's truck through the contractor reach.
	rA := w.expense(w.A, "fuel", w.dA, w.tA, day(5, 1), 900, "pending", odo("10000")...)
	r = w.post("/v1/expenses/"+rA+"/status", w.login("ta.o"), map[string]any{"status": "approved"})
	if pm, _ := r.data()["pmCheck"].(map[string]any); pm["outcome"] != "within_threshold" {
		t.Fatalf("within: %s", r.raw)
	}
	if w.count(`SELECT current_mileage FROM trucks WHERE id = $1`, w.tA) != 10000 {
		t.Error("current mileage not moved")
	}
	// Rejecting: expense.status_changed, the note kept when blank.
	rj := w.expense(w.O, "fuel", w.dO, w.tO2, day(6, 1), 10, "pending", "admin_note", "'เดิม'", "odometer_km", "1")
	r = w.post("/v1/expenses/"+rj+"/status", os, map[string]any{"status": "rejected", "adminNote": "  "})
	if r.data()["status"] != "rejected" || r.data()["adminNote"] != "เดิม" || w.events("expense.status_changed") != 1 {
		t.Fatalf("reject: %s", r.raw)
	}
	expect(t, w.post("/v1/expenses/"+rj+"/status", os, map[string]any{"status": "done"}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.post("/v1/expenses/"+rj+"/status", w.login("mg.o"), map[string]any{"status": "pending"}), http.StatusOK, "")
	expect(t, w.post("/v1/expenses/"+rj+"/status", w.login("op.o"), map[string]any{"status": "approved"}), http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/expenses/"+rj+"/status", w.login("ta.c"), map[string]any{"status": "approved"}), http.StatusNotFound, "not_found")
	r = w.post("/v1/expenses/bulk-status", os, map[string]any{"ids": []string{rj, "0199f000-0000-7000-8000-000000000000"}, "status": "approved"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if w.count(`SELECT count(*) FROM vehicle_expenses WHERE id = $1 AND status = 'pending'`, rj) != 1 {
		t.Error("a refused bulk request wrote")
	}
}

// The PM check route (checkMaintenanceAlert, which ran unauthenticated): accounting:audit_expense, a truck of the
// caller's reach, the legacy outcomes.
func TestPMCheckRoute(t *testing.T) {
	w := newWorld(t)
	os := w.login("os.o")
	path := "/v1/trucks/" + w.tO + "/pm-check"
	r := w.post(path, os, map[string]any{"mileage": 97000})
	expect(t, r, http.StatusOK, "")
	if r.data()["outcome"] != "within_threshold" || r.data()["created"] != false {
		t.Fatalf("within: %s", r.raw)
	}
	r = w.post(path, os, map[string]any{"mileage": 101000})
	if r.data()["outcome"] != "created" {
		t.Fatalf("overdue: %s", r.raw)
	}
	id := str(r.data()["maintenanceId"])
	if n := w.str(`SELECT notes FROM maintenance_records WHERE id = $1`, id); *n != "ระบบอัตโนมัติ: เลยกำหนดเช็คระยะ (101,000 กม. / รอบ 100,000 กม.)" {
		t.Errorf("notes %q", *n)
	}
	if r = w.post(path, os, map[string]any{"mileage": 101500}); r.data()["outcome"] != "active_maintenance" {
		t.Fatalf("active: %s", r.raw)
	}
	// The booking is cancelled: the next check clears it and finds the alerted mileage.
	w.exec(`UPDATE maintenance_records SET status = 'cancelled' WHERE id = $1`, id)
	if r = w.post(path, os, map[string]any{"mileage": 101000}); r.data()["outcome"] != "created" {
		// the cancelled booking is cleared together with its alert mark, so the same mileage books again
		t.Fatalf("after cancel: %s", r.raw)
	}
	if r = w.post(path, os, map[string]any{"mileage": 101000}); r.data()["outcome"] != "active_maintenance" {
		t.Fatalf("active again: %s", r.raw)
	}
	if r = w.post("/v1/trucks/"+w.tO2+"/pm-check", os, map[string]any{"mileage": 1000}); r.data()["outcome"] != "no_next_service_mileage" {
		t.Fatalf("no next service: %s", r.raw)
	}
	expect(t, w.post("/v1/trucks/"+w.tC+"/pm-check", os, map[string]any{"mileage": 1}), http.StatusNotFound, "not_found")
	expect(t, w.post(path, w.login("op.o"), map[string]any{"mileage": 1}), http.StatusForbidden, "permission_denied")
	r = w.post(path, os, map[string]any{})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["mileage"] != "required" {
		t.Errorf("violations %v", r.fields())
	}
	expect(t, w.post(path, "", map[string]any{"mileage": 1}), http.StatusUnauthorized, "unauthenticated")
}

// Staff entries and edits: the type's edit capability, the truck and its plate set together, the photos committed to
// the row, the fields a PATCH may not change, and a PATCH that changes nothing writes nothing.
func TestCreateAndPatch(t *testing.T) {
	w := newWorld(t)
	mg := w.login("mg.o")
	key := w.upload(mg, "expense_receipt", "0199f000-0000-7000-8000-000000000001", "image/jpeg", "", jpeg)
	r := w.post("/v1/expenses", mg, map[string]any{"type": "other", "driverId": w.dO2, "truckId": w.tO2,
		"date": "2026-10-10", "amountThb": 120.5, "category": "Parking", "description": "ลานจอด", "receiptKey": key})
	expect(t, r, http.StatusCreated, "")
	e := r.data()
	id := str(e["id"])
	if e["category"] != "parking" || e["licensePlate"] != "กข-1002" || e["receiptKey"] != key || e["adminNote"] != "บันทึกโดย admin" ||
		e["date"] != "2026-10-09T17:00:00Z" || e["expenseDate"] != "2026-10-10" {
		t.Fatalf("entry %s", r.raw)
	}
	if w.count(`SELECT count(*) FROM file_objects WHERE object_key = $1 AND status = 'committed' AND owner_kind = 'expense'
		AND owner_id = $2`, key, id) != 1 {
		t.Error("the receipt is not committed to the row")
	}
	if w.events("expense.created") != 1 {
		t.Error("expense.created")
	}
	// The truck and its plate change together; null clears both; the type and the driver are not editable.
	r = w.patch("/v1/expenses/"+id, mg, map[string]any{"truckId": w.tO, "amountThb": 130})
	if r.data()["licensePlate"] != "กข-1001" || num(r.data()["amountThb"]) != 130 {
		t.Fatalf("truck change %s", r.raw)
	}
	w.exec(`UPDATE trucks SET license_plate = 'กข-9999' WHERE id = $1`, w.tO)
	r = w.get("/v1/expenses?driverId="+w.dO2, mg)
	if r.list()[0]["licensePlate"] != "กข-1001" {
		t.Error("the plate snapshot followed the truck")
	}
	r = w.patch("/v1/expenses/"+id, mg, map[string]any{"truckId": nil, "receiptKey": nil})
	if r.data()["truckId"] != nil || r.data()["licensePlate"] != nil || r.data()["receiptKey"] != nil {
		t.Fatalf("cleared %s", r.raw)
	}
	r = w.patch("/v1/expenses/"+id, mg, map[string]any{"type": "fuel", "driverId": w.dO, "status": "approved",
		"volumeLiters": 3, "truckId": "x"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	for field, reason := range map[string]string{"type": "not_editable", "driverId": "not_editable", "status": "not_editable",
		"volumeLiters": "not_available", "truckId": "invalid"} {
		if r.fields()[field] != reason {
			t.Errorf("%s: %q (all %v)", field, r.fields()[field], r.fields())
		}
	}
	before := w.events("expense.updated")
	expect(t, w.patch("/v1/expenses/"+id, mg, map[string]any{"amountThb": 130, "category": "parking"}), http.StatusOK, "")
	if w.events("expense.updated") != before {
		t.Error("a PATCH that changed nothing emitted")
	}
	// Capabilities per type: operation staff audit but do not edit; a carrier's own staff cannot reach O's row.
	r = w.post("/v1/expenses", w.login("os.o"), map[string]any{"type": "fuel", "driverId": w.dO, "date": "2026-10-10", "amountThb": 1})
	expect(t, r, http.StatusForbidden, "permission_denied")
	expect(t, w.patch("/v1/expenses/"+id, w.login("ta.c"), map[string]any{"note": "x"}), http.StatusNotFound, "not_found")
	r = w.post("/v1/expenses", mg, map[string]any{"type": "fuel", "driverId": w.dC, "date": "2026-10-10", "amountThb": 1})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["driverId"] != "not_found" {
		t.Errorf("a driver outside the reach: %v", r.fields())
	}
	r = w.post("/v1/expenses", mg, map[string]any{"type": "fuel", "date": "2026-10-10"})
	if r.fields()["driverId"] != "required" || r.fields()["amountThb"] != "required" {
		t.Errorf("required: %v", r.fields())
	}
	// A truck alone (no driver): stamped with the truck's tenant.
	r = w.post("/v1/expenses", mg, map[string]any{"type": "other", "truckId": w.tA, "date": "2026-10-10", "amountThb": 5})
	expect(t, r, http.StatusCreated, "")
	if r.data()["tenantId"] != w.A || *w.str(`SELECT tenant_source FROM vehicle_expenses WHERE id = $1`, r.data()["id"]) != "truck" {
		t.Errorf("truck-only entry %s", r.raw)
	}
	r = w.patch("/v1/expenses/"+str(r.data()["id"]), mg, map[string]any{"truckId": nil})
	if r.fields()["truckId"] != "required" {
		t.Errorf("a row needs a driver or a truck: %s", r.raw)
	}
	// An approved fuel entry with an odometer reading runs the PM check, as an approval does.
	r = w.post("/v1/expenses", mg, map[string]any{"type": "fuel", "driverId": w.dO, "date": "2026-10-10", "amountThb": 1,
		"odometerKm": 99000})
	if pm, _ := r.data()["pmCheck"].(map[string]any); pm == nil || pm["outcome"] != "created" {
		t.Fatalf("approved entry: %s", r.raw)
	}
}

// One request body decoded into the payload type, to keep the JSON names honest.
func TestPayloadNames(t *testing.T) {
	w := newWorld(t)
	w.expense(w.O, "fuel", w.dO, w.tO, day(1, 1), 1, "pending", "volume_liters", "40.125", "price_per_liter", "37.04",
		"odometer_km", "1000", "station_tax_id", "'S'", "tax_inv_id", "'T'", "refill_lat", "13.7", "refill_lng", "100.5",
		"distance_km", "12.5")
	r := w.get("/v1/expenses", w.login("ta.o"))
	var got []map[string]any
	if err := json.Unmarshal(mustJSON(t, r.body["data"]), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "tenantId", "type", "category", "status", "date", "expenseDate", "amountThb", "driverId", "driverName",
		"truckId", "licensePlate", "volumeLiters", "pricePerLiter", "odometerKm", "stationTaxId", "taxInvId", "refillLat",
		"refillLng", "note", "description", "adminNote", "distanceKm", "tollImportSequence", "tollLocation", "tollLane",
		"tollSourceType", "receiptKey", "odometerKey", "createdBy", "createdAt", "updatedAt"}
	if len(got[0]) != len(want) {
		t.Errorf("%d fields, want %d: %v", len(got[0]), len(want), got[0])
	}
	for _, k := range want {
		if _, ok := got[0][k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
	if num(got[0]["volumeLiters"]) != 40.125 || num(got[0]["distanceKm"]) != 12.5 {
		t.Errorf("values %v", got[0])
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
