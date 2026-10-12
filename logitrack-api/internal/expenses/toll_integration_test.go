//go:build integration

package expenses_test

import (
	"bytes"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// The toll import: a card history (CSV or XLSX, or rows the client parsed) becomes pending other expenses (category
// toll) of one driver and truck, with the row's sequence, place, lane and type; non-toll and unreadable rows, rows a
// file repeats and transactions the truck already holds are skipped with their reason; dryRun writes nothing.
func TestTollImport(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	csv := []byte("ลำดับ,สถานที่,เครื่อง/เลน,วันที่เกิดรายการ,ประเภท,จำนวนเงิน\n" +
		"1,ด่านบางนา,03,15/03/2026 08:15:30,ผ่านทาง,45\n" +
		"2,สาขาสีลม,,15/03/2026 09:00,เติมเงิน,500\n" +
		",ด่านดินแดง,1,16/03/2026 10:00,ผ่านทาง,\"1,050.50\"\n" +
		"4,ด่านลาดพร้าว,2,ไม่ระบุ,ผ่านทาง,45\n" +
		"5,ด่านบางนา,03,15/03/2026 08:15:30,ผ่านทาง,45\n")
	form := map[string]string{"driverId": w.dO, "truckId": w.tO}
	r := w.multipart("/v1/expenses/toll-import", ta, "history.csv", csv, map[string]string{"driverId": w.dO, "truckId": w.tO,
		"dryRun": "true"})
	expect(t, r, http.StatusOK, "")
	if num(r.data()["created"]) != 2 || num(r.data()["skipped"]) != 3 || r.data()["dryRun"] != true ||
		w.count(`SELECT count(*) FROM vehicle_expenses`) != 0 {
		t.Fatalf("dry run %s", r.raw)
	}
	r = w.multipart("/v1/expenses/toll-import", ta, "history.csv", csv, form)
	expect(t, r, http.StatusOK, "")
	if num(r.data()["created"]) != 2 || num(r.data()["skipped"]) != 3 {
		t.Fatalf("import %s", r.raw)
	}
	reasons := map[int]string{}
	for _, s := range r.data()["skippedRows"].([]any) {
		m := s.(map[string]any)
		reasons[int(num(m["row"]))] = str(m["reason"])
	}
	if reasons[3] != "not_toll" || reasons[5] != "invalid_date" || reasons[6] != "duplicate_in_file" {
		t.Errorf("skipped rows %v", reasons)
	}
	var seq int
	var cat, status, desc, plate, place, lane, src, tenant string
	var at time.Time
	var amount float64
	if err := w.etl.QueryRow(t.Context(), `SELECT toll_import_sequence, category, status, description, truck_license_plate_snapshot,
		toll_location, toll_lane, toll_source_type, tenant_id::text, expense_at, amount_thb FROM vehicle_expenses
		WHERE toll_location = 'ด่านบางนา'`).Scan(&seq, &cat, &status, &desc, &plate, &place, &lane, &src, &tenant, &at, &amount); err != nil {
		t.Fatal(err)
	}
	if seq != 1 || cat != "toll" || status != "pending" || desc != "ผ่านทาง · ด่านบางนา · 03" || plate != "กข-1001" || lane != "03" ||
		src != "ผ่านทาง" || tenant != w.O || amount != 45 || !at.Equal(time.Date(2026, 3, 15, 1, 15, 30, 0, time.UTC)) {
		t.Fatalf("row %d %s %s %q %s %s %s %s %v %v", seq, cat, status, desc, plate, lane, src, tenant, at, amount)
	}
	if w.count(`SELECT toll_import_sequence FROM vehicle_expenses WHERE toll_location = 'ด่านดินแดง'`) != 3 {
		t.Error("a missing sequence is the row's position")
	}
	if w.events("expense.created") != 1 {
		t.Errorf("%d expense.created events, want one per import", w.events("expense.created"))
	}
	// A re-imported history adds nothing.
	r = w.multipart("/v1/expenses/toll-import", ta, "history.csv", csv, form)
	if num(r.data()["created"]) != 0 || num(r.data()["skipped"]) != 5 || w.count(`SELECT count(*) FROM vehicle_expenses`) != 2 {
		t.Fatalf("re-import %s", r.raw)
	}

	// XLSX with a title row and a date cell.
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for cell, v := range map[string]any{"A1": "Easy Pass statement", "A2": "No.", "B2": "Location", "C2": "Lane",
		"D2": "Transaction Date", "E2": "Type", "F2": "Amount", "A3": 1, "B3": "Bang Na", "C3": "3",
		"D3": time.Date(2026, 3, 20, 7, 30, 0, 0, time.UTC), "E3": "Expressway toll", "F3": 60} {
		if err := f.SetCellValue(sheet, cell, v); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	r = w.multipart("/v1/expenses/toll-import", ta, "history.xlsx", buf.Bytes(), form)
	if num(r.data()["created"]) != 1 {
		t.Fatalf("xlsx %s", r.raw)
	}
	if w.count(`SELECT count(*) FROM vehicle_expenses WHERE toll_location = 'Bang Na' AND expense_at = '2026-03-20 07:30:00+07'`) != 1 {
		t.Error("the date cell is a Bangkok wall time")
	}

	// The JSON form.
	r = w.post("/v1/expenses/toll-import", ta, map[string]any{"driverId": w.dO, "truckId": w.tO, "rows": []map[string]any{
		{"date": "2026-03-21T08:00:00+07:00", "amountThb": 30, "location": "ด่านใหม่", "sourceType": "ผ่านทาง"},
		{"date": "x", "amountThb": 30}}})
	if num(r.data()["created"]) != 1 || num(r.data()["skipped"]) != 1 {
		t.Fatalf("json %s", r.raw)
	}

	// Rules: accounting:edit_other; the driver and the truck in the caller's reach; a sheet without the columns.
	expect(t, w.multipart("/v1/expenses/toll-import", w.login("os.o"), "h.csv", csv, form), http.StatusForbidden, "permission_denied")
	r = w.multipart("/v1/expenses/toll-import", ta, "h.csv", csv, map[string]string{"driverId": w.dC, "truckId": w.tO})
	if r.fields()["driverId"] != "not_found" {
		t.Errorf("driver outside the reach: %s", r.raw)
	}
	r = w.multipart("/v1/expenses/toll-import", ta, "h.csv", csv, map[string]string{"driverId": w.dO, "truckId": w.tC})
	if r.fields()["truckId"] != "not_found" {
		t.Errorf("truck outside the reach: %s", r.raw)
	}
	r = w.multipart("/v1/expenses/toll-import", ta, "h.csv", []byte("a\nb\n"), form)
	if r.fields()["file"] != "missing_column" {
		t.Errorf("no columns: %s", r.raw)
	}
	r = w.multipart("/v1/expenses/toll-import", ta, "h.csv", nil, form)
	if r.fields()["file"] != "required" {
		t.Errorf("no file: %s", r.raw)
	}
	r = w.post("/v1/expenses/toll-import", ta, map[string]any{"rows": []any{}})
	if r.fields()["driverId"] != "required" || r.fields()["truckId"] != "required" {
		t.Errorf("required: %s", r.raw)
	}
}

// Two overlapping imports of one history (a double submit, a retry after a proxy timeout while the first one still
// runs) add it once: the imports of one truck hold an advisory lock, so the second one reads the first one's rows.
func TestTollImportConcurrent(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	const n = 300
	rows := make([]map[string]any, n)
	start := time.Date(2026, 4, 1, 6, 0, 0, 0, time.UTC)
	for i := range rows {
		rows[i] = map[string]any{"sequence": i + 1, "date": start.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			"amountThb": 45, "location": "ด่านบางนา", "lane": "03", "sourceType": "ผ่านทาง"}
	}
	body := map[string]any{"driverId": w.dO, "truckId": w.tO, "rows": rows}
	var wg sync.WaitGroup
	results := make([]resp, 3)
	for i := range results {
		wg.Go(func() { results[i] = w.post("/v1/expenses/toll-import", ta, body) })
	}
	wg.Wait()
	created := 0
	for _, r := range results {
		expect(t, r, http.StatusOK, "")
		created += int(num(r.data()["created"]))
	}
	if created != n || w.count(`SELECT count(*) FROM vehicle_expenses WHERE category = 'toll'`) != n {
		t.Fatalf("%d created, %d rows; want the history once (%d)", created,
			w.count(`SELECT count(*) FROM vehicle_expenses WHERE category = 'toll'`), n)
	}
}
