//go:build integration

// Integration tests of T39 (issue #59): GET /v1/billing/rows, rows/missing-billing-date and standby-diagnostics, as
// logitrack_app under RLS through the real API (harness_integration_test.go).
package billing_test

import (
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- fixtures of T39 ------------------------------------------------------------------------------------------

func (w *world) hub(code, th, en string) {
	w.exec(`INSERT INTO hubs (source_id, name_th, name_en, network) VALUES ($1, $2, nullif($3, ''), 'SPK')`, code, th, en)
}

func (w *world) driver(tenant, fullTh, mobile string) string {
	return w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, full_name_th, mobile)
		VALUES ($1, 'self', 'First', 'Last', nullif($2, ''), $3) RETURNING id::text`, tenant, fullTh, mobile)
}

// taskSpec is a task row (the pricing inputs of its trip).
type taskSpec struct {
	tenant, party, srcParty, src, dest, class, plate, cat, driver string
	plan                                                          time.Time
}

func (w *world) task(s taskSpec) string {
	if s.plan.IsZero() {
		s.plan = bkk(2026, 9, 10, 8, 0)
	}
	return w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		billing_party_id, source_linked_party_id, truck_type, license_plate_snapshot, job_category, driver_id)
		VALUES ($1, 'form', 'line_haul', 'completed', $2, $3, $4, nullif($5, '')::uuid, nullif($6, '')::uuid,
		        nullif($7, ''), nullif($8, ''), nullif($9, ''), nullif($10, '')::uuid) RETURNING id::text`,
		s.tenant, s.plan, s.src, s.dest, s.party, s.srcParty, s.class, s.plate, s.cat, s.driver)
}

// tripOf inserts a delivered trip of task (party "" = not stamped) and returns its id.
func (w *world) tripOf(tenant, party, no, task, driver string, delivered time.Time, billingDate *time.Time, cat string, multi bool) string {
	return w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, job_category, billing_party_id,
		billing_date, delivered_at, created_at, task_id, driver_id, is_multi_delivery)
		VALUES ($1, 'driver', $2, 'delivered', 'line_haul', nullif($3, ''), nullif($4, '')::uuid, $5, $6, $6,
		        nullif($7, '')::uuid, nullif($8, '')::uuid, $9) RETURNING id::text`,
		tenant, no, cat, party, billingDate, delivered, task, driver, multi)
}

// priced stamps the display provenance of a snapshot (lookup codes, round, band, fuel).
func (w *world) priced(trip, hub, dest, round string, add float64, manual bool) {
	w.exec(`UPDATE trip_billing_snapshots SET lookup_hub_code = $2, lookup_destination_code = $3,
		round_effective_from_date = nullif($4, '')::date, add_thb_per_trip = $5, manual_override = $6,
		fuel_band_lower_thb = CASE WHEN $4 <> '' THEN 37.01 END, fuel_band_upper_thb = CASE WHEN $4 <> '' THEN 38.00 END
		WHERE trip_id = $1`, trip, hub, dest, round, add, manual)
}

// rows calls GET /v1/billing/rows as user with the query.
func (w *world) rows(user string, q url.Values) resp {
	w.t.Helper()
	return w.get("/v1/billing/rows?"+q.Encode(), w.login(user))
}

func rowList(r resp) []map[string]any {
	items, _ := r.data()["rows"].([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any))
	}
	return out
}

func unpricedList(r resp) []map[string]any {
	items, _ := r.data()["unpriced"].([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any))
	}
	return out
}

func ids(rows []map[string]any, keep func(map[string]any) bool) []string {
	var out []string
	for _, r := range rows {
		if keep(r) {
			out = append(out, str(r["id"]))
		}
	}
	sort.Strings(out)
	return out
}

func inInvoice(r map[string]any) bool { return r["inInvoiceSet"] == true }
func inPreview(r map[string]any) bool { return r["inPreview"] == true }

func totalOf(r resp, set, key string) float64 {
	t, _ := r.data()[set].(map[string]any)
	return num(t[key])
}

// invoiceFixture is the invoice-set fixture of TestStatementInvoiceSet (T37) for October, party X (plan basis).
func (w *world) invoiceFixture() {
	tok := w.login("ta.o")
	prim, supp := ptr("PRIMARY"), ptr("SUPPLEMENTARY")
	p1 := w.trip(w.O, w.partyX, "P1", bkk(2026, 11, 2, 9, 0), ptr(bkk(2026, 10, 3, 8, 0)), prim)
	w.snapshot(p1, w.O, ptr(1000.10), false)
	p2 := w.trip(w.O, w.partyX, "P2", bkk(2026, 10, 1, 9, 0), ptr(bkk(2026, 9, 30, 23, 59)), prim)
	w.snapshot(p2, w.O, ptr(999.0), false)
	p3 := w.trip(w.O, w.partyX, "P3", bkk(2026, 10, 2, 9, 0), nil, prim)
	w.snapshot(p3, w.O, ptr(999.0), false)
	p4 := w.trip(w.O, w.partyX, "P4", bkk(2026, 10, 4, 9, 0), ptr(bkk(2026, 10, 4, 0, 0)), supp)
	w.snapshot(p4, w.O, ptr(950.0), false)
	p5 := w.trip(w.O, w.partyX, "P5", bkk(2026, 10, 5, 9, 0), ptr(bkk(2026, 10, 5, 0, 0)), nil)
	w.snapshot(p5, w.O, ptr(800.0), false)
	m := w.trip(w.O, w.partyX, "M1", bkk(2026, 10, 6, 9, 0), ptr(bkk(2026, 10, 31, 23, 59)), prim)
	w.snapshot(m, w.O, ptr(1300.0), true)
	w.stop(m, 1, "SOCE", 1000)
	w.stop(m, 2, "SOCN", 300)
	w.stop(m, 3, "SOCW", 0)
	z := w.trip(w.O, w.partyX, "Z0", bkk(2026, 10, 7, 9, 0), ptr(bkk(2026, 10, 7, 0, 0)), prim)
	w.snapshot(z, w.O, ptr(0.0), false)
	u := w.trip(w.O, w.partyX, "U0", bkk(2026, 10, 7, 9, 0), ptr(bkk(2026, 10, 7, 0, 0)), prim)
	w.snapshot(u, w.O, nil, false)
	b := w.trip(w.A, w.partyX, "B0", bkk(2026, 10, 8, 9, 0), ptr(bkk(2026, 10, 8, 0, 0)), prim)
	w.snapshot(b, w.A, ptr(700.0), false)
	ct := w.trip(w.A, w.partyX, "CT", bkk(2026, 10, 9, 9, 0), ptr(bkk(2026, 10, 9, 0, 0)), prim)
	w.snapshot(ct, w.O, ptr(1500.0), false)
	rO := w.post("/v1/billing/standby-rates", tok, map[string]any{"customerId": w.partyX, "rateThb": 500,
		"effectiveFromDateStr": "2026-01-01"})
	expect(w.t, rO, http.StatusCreated, "")
	rA := w.standbyRate(w.A, w.partyX, "2026-01-01", 400)
	w.standby(w.O, w.partyX, str(rO.data()["id"]), w.O, bkk(2026, 10, 10, 0, 0), 500)
	w.standby(w.A, w.partyX, rA, w.A, bkk(2026, 10, 10, 0, 0), 400)
	w.standby(w.O, w.partyX, str(rO.data()["id"]), w.O, bkk(2026, 9, 30, 23, 0), 500)
	w.standby(w.O, w.partyX, "", "", bkk(2026, 10, 11, 0, 0), 250) // a service-fee price the ETL loaded: the own fleet's
}

// AC: preview filters never change the invoice set; the rows API's invoice set is exactly the lines a statement inserts
// for the same toggles (TestStatementInvoiceSet's fixture, every toggle combination below).
func TestRowsInvoiceSetEqualsStatementLines(t *testing.T) {
	w := newWorld(t)
	w.invoiceFixture()
	for _, tc := range []struct {
		types, categories []string
	}{
		{nil, nil},
		{[]string{"trip"}, nil},
		{[]string{"multidrop", "standby"}, nil},
		{nil, []string{"PRIMARY", "unverified"}},
		{[]string{"trip", "standby"}, []string{"SUPPLEMENTARY"}},
	} {
		q := url.Values{"customerId": {w.customerX}, "year": {"2026"}, "month": {"10"}}
		body := map[string]any{"customerId": w.partyX, "year": 2026, "month": 10}
		if tc.types != nil {
			q["types"] = []string{strings.Join(tc.types, ",")}
			body["types"] = tc.types
		}
		if tc.categories != nil {
			q["categories[]"] = tc.categories
			body["categories"] = tc.categories
		}
		r := w.rows("ta.o", q)
		expect(t, r, http.StatusOK, "")
		got := ids(rowList(r), inInvoice)
		st := w.statement("ta.o", body)
		if len(got) == 0 {
			expect(t, st, http.StatusConflict, "failed_precondition")
			continue
		}
		expect(t, st, http.StatusCreated, "")
		lines := w.get("/v1/billing/statements/"+str(st.data()["id"])+"/lines", w.login("ta.o"))
		var want []string
		for _, l := range lines.list() {
			want = append(want, str(l["rowId"]))
		}
		sort.Strings(want)
		if !slices.Equal(got, want) {
			t.Fatalf("%v %v: rows invoice set %v, statement lines %v", tc.types, tc.categories, got, want)
		}
		if inv := totalOf(r, "invoice", "grandTotal"); inv != num(st.data()["totalAmount"]) ||
			totalOf(r, "invoice", "withholdingTax") != num(st.data()["withholdingTax"]) ||
			totalOf(r, "invoice", "net") != num(st.data()["netAmount"]) || totalOf(r, "invoice", "withholdingTaxRate") != 0.03 {
			t.Fatalf("%v %v: totals %v, statement %s", tc.types, tc.categories, r.data()["invoice"], st.raw)
		}
		if del := w.del("/v1/billing/statements/"+str(st.data()["id"]), w.login("ta.o")); del.status != http.StatusNoContent {
			t.Fatalf("delete draft %d %s", del.status, del.raw)
		}
	}

	// The full set of the month: every priced row of X, flagged; what the statement leaves out is visible, not in the set.
	r := w.rows("ta.o", url.Values{"customerId": {w.partyX}, "year": {"2026"}, "month": {"10"}})
	expect(t, r, http.StatusOK, "")
	all := rowList(r)
	byNo := map[string]map[string]any{}
	for _, row := range all {
		byNo[str(row["tripNo"])+"/"+str(row["rowType"])] = row
	}
	for key, want := range map[string]bool{"P1/trip": true, "P4/trip": true, "P5/trip": true, "CT/trip": true,
		"M1-s1/multidrop_stop": true, "M1-s2/multidrop_stop": true, "B0/trip": false} {
		row, ok := byNo[key]
		if !ok || row["inInvoiceSet"] != want {
			t.Fatalf("%s: %v (want inInvoiceSet %v); rows %v", key, row, want, byNo)
		}
	}
	if slices.ContainsFunc(all, func(m map[string]any) bool { return str(m["tripNo"]) == "P2" || str(m["tripNo"]) == "P3" }) {
		t.Fatal("trips outside the plan period (or without billing_date) are rows of October")
	}
	if !slices.Contains(byNo["B0/trip"]["flags"].([]any), any("other_carrier")) {
		t.Fatalf("a carrier's own price is flagged: %v", byNo["B0/trip"])
	}
	if str(r.data()["basis"]) != "plan" || str(byNo["P1/trip"]["billingDateBasis"]) != "plan" ||
		str(byNo["P1/trip"]["axisDate"]) != "2026-10-03T01:00:00Z" {
		t.Fatalf("plan axis: %s / %v", r.data()["basis"], byNo["P1/trip"])
	}
	if r.data()["documentable"] != true || num(r.data()["outsideReach"]) != 0 {
		t.Fatalf("documentable %v outsideReach %v", r.data()["documentable"], r.data()["outsideReach"])
	}
	// Counts of the toggles' buckets, over the priced rows whatever the toggles.
	tc, _ := r.data()["typeCounts"].(map[string]any)
	cc, _ := r.data()["categoryCounts"].(map[string]any)
	if num(tc["trip"]) != 5 || num(tc["multidrop_stop"]) != 2 || num(tc["standby"]) != 3 ||
		num(cc["PRIMARY"]) != 5 || num(cc["SUPPLEMENTARY"]) != 1 || num(cc["unverified"]) != 1 {
		t.Fatalf("counts %v %v", tc, cc)
	}
}

// AC: preview filters never change the invoice set: plate, vehicle class and review month narrow inPreview and the
// preview totals only.
func TestRowsPreviewFiltersNeverChangeTheInvoiceSet(t *testing.T) {
	w := newWorld(t)
	w.hub("SPK-GW", "เจแอนด์ที บางปู", "J&T EXPRESS บางปู")
	w.hub("SPK890103-ลาดกระบัง26", "ลาดกระบัง26", "")
	drv := w.driver(w.A, "สมชาย ใจดี", "0812345678")
	mk := func(no, plate, class string, day int, amount float64, plan time.Time) string {
		task := w.task(taskSpec{tenant: w.O, party: w.partyY, src: "SPK-GW", dest: "SPK890103", class: class, plate: plate,
			cat: "PRIMARY", driver: drv, plan: plan})
		id := w.tripOf(w.O, w.partyY, no, task, drv, bkk(2026, 9, day, 10, 0), ptr(bkk(2026, 9, day, 10, 0)), "PRIMARY", false)
		w.snapshot(id, w.O, ptr(amount), false)
		w.priced(id, "SPK-GW", "SPK890103", "2026-09-01", 0, false)
		return id
	}
	mk("T1", "70-1111", "6WH", 3, 1000, bkk(2026, 9, 3, 8, 0))
	mk("T2", "70-2222", "4WJ", 4, 800, bkk(2026, 8, 31, 8, 0)) // planned in August, delivered in September
	mk("T3", "", "", 5, 600, bkk(2026, 9, 5, 8, 0))
	base := url.Values{"customerId": {w.customerY}, "year": {"2026"}, "month": {"9"}}
	full := w.rows("ta.o", base)
	expect(t, full, http.StatusOK, "")
	wantSet := ids(rowList(full), inInvoice)
	if len(wantSet) != 3 || totalOf(full, "invoice", "grandTotal") != 2400 || totalOf(full, "preview", "grandTotal") != 2400 {
		t.Fatalf("full %s", full.raw)
	}
	row := rowList(full)[0]
	if str(row["hubDisplayName"]) != "J&T EXPRESS บางปู" || str(row["originHubCode"]) != "SPK-GW" ||
		str(row["destinationDisplayName"]) != "ลาดกระบัง26" || str(row["driverName"]) != "สมชาย ใจดี" ||
		str(row["driverPhone"]) != "0812345678" || str(row["subcontractorName"]) != "A" || str(row["customerId"]) != w.customerY ||
		str(row["truckLicensePlate"]) != "70-1111" || str(row["vehicleClass"]) != "6WH" || str(row["jobCategory"]) != "PRIMARY" ||
		str(row["roundEffectiveFromDate"]) != "2026-09-01" || num(row["fuelBandLowerThb"]) != 37.01 {
		t.Fatalf("display fields resolved on the server: %v", row)
	}
	opts, _ := full.data()["filterOptions"].(map[string]any)
	plates, _ := opts["plates"].([]any)
	classes, _ := opts["vehicleClasses"].([]any)
	months, _ := opts["reviewMonths"].([]any)
	if len(plates) != 3 || str(plates[2].(map[string]any)["value"]) != "__none__" || len(classes) != 3 || len(months) != 2 {
		t.Fatalf("options %v", opts)
	}
	for _, f := range []struct {
		key, value string
		want       []string
		total      float64
	}{
		{"plate", "plate:70-1111", []string{"T1"}, 1000},
		{"plate", "__none__", []string{"T3"}, 600},
		{"vehicleClass", "4WJ", []string{"T2"}, 800},
		{"vehicleClass", "__none__", []string{"T3"}, 600},
		{"reviewMonth", "2026-08", []string{"T2"}, 800}, // a delivered-basis period is reviewed on the plan month
	} {
		q := url.Values{}
		for k, v := range base {
			q[k] = v
		}
		q.Set(f.key, f.value)
		r := w.rows("ta.o", q)
		expect(t, r, http.StatusOK, "")
		if got := ids(rowList(r), inInvoice); !slices.Equal(got, wantSet) {
			t.Fatalf("%s=%s changed the invoice set: %v", f.key, f.value, got)
		}
		if totalOf(r, "invoice", "grandTotal") != 2400 || totalOf(r, "preview", "grandTotal") != f.total {
			t.Fatalf("%s=%s totals %v / %v", f.key, f.value, r.data()["invoice"], r.data()["preview"])
		}
		var previewNos []string
		for _, row := range rowList(r) {
			if inPreview(row) {
				previewNos = append(previewNos, str(row["tripNo"]))
			}
		}
		if !slices.Equal(previewNos, f.want) {
			t.Fatalf("%s=%s preview %v, want %v", f.key, f.value, previewNos, f.want)
		}
	}
	// The toggles do change the invoice set (and the preview within it).
	r := w.rows("ta.o", url.Values{"customerId": {w.customerY}, "year": {"2026"}, "month": {"9"}, "types": {"standby"}})
	if len(ids(rowList(r), inInvoice)) != 0 || len(rowList(r)) != 3 {
		t.Fatalf("toggled off: %s", r.raw)
	}
	// Bad parameters.
	for q, field := range map[string]string{"types=bogus": "types", "categories=": "categories", "reviewMonth=2026-13": "reviewMonth"} {
		r := w.get("/v1/billing/rows?customerId="+w.customerY+"&year=2026&month=9&"+q, w.login("ta.o"))
		if r.status != http.StatusUnprocessableEntity || r.fields()[field] == "" {
			t.Fatalf("%s: %d %s", q, r.status, r.raw)
		}
	}
	expect(t, w.get("/v1/billing/rows?customerId="+w.customerY+"&year=2026&month=9&foo=1", w.login("ta.o")),
		http.StatusBadRequest, "bad_request")
}

func TestRowsAccess(t *testing.T) {
	w := newWorld(t)
	q := url.Values{"customerId": {w.partyY}, "year": {"2026"}, "month": {"9"}}
	// Only the own fleet bills, so only it previews: a carrier's admin holds the capability but is refused.
	r := w.rows("ta.a", q)
	expect(t, r, http.StatusForbidden, "permission_denied")
	if r.detail("reason") != "own_fleet_only" {
		t.Fatalf("%s", r.raw)
	}
	expect(t, w.get("/v1/billing/standby-diagnostics?"+q.Encode(), w.login("ta.c")), http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/billing/rows/missing-billing-date?"+q.Encode(), w.login("ta.a")), http.StatusForbidden, "permission_denied")
	// A plain user of the own fleet does not hold accounting:billing_document.
	expect(t, w.rows("us.o", q), http.StatusForbidden, "permission_denied")
	// An id that names no party is an empty month; all reads every party.
	r = w.rows("ta.o", url.Values{"customerId": {"0192d6a4-0000-7000-8000-000000000001"}, "year": {"2026"}, "month": {"9"}})
	expect(t, r, http.StatusOK, "")
	if len(rowList(r)) != 0 || r.data()["billingPartyId"] != nil || r.data()["documentable"] != false {
		t.Fatalf("unknown party %s", r.raw)
	}
	r = w.rows("ta.o", url.Values{"customerId": {"all"}, "year": {"2026"}, "month": {"9"}})
	expect(t, r, http.StatusOK, "")
	if r.data()["basis"] != nil || r.data()["documentable"] != false {
		t.Fatalf("all %s", r.raw)
	}
	for _, q := range []string{"customerId=" + w.partyY + "&year=2026", "customerId=x&year=2026&month=9", "year=2026&month=9"} {
		if r := w.get("/v1/billing/rows?"+q, w.login("ta.o")); r.status != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s", q, r.status, r.raw)
		}
	}
}

// Rows of a trip the caller cannot see (run by an independent carrier, priced by the own fleet) are counted, not
// shown: outsideReach, and no document can be made (POST /v1/billing/statements would refuse rows_outside_reach).
func TestRowsOutsideReach(t *testing.T) {
	w := newWorld(t)
	t1 := w.trip(w.O, w.partyY, "IN", bkk(2026, 9, 5, 10, 0), nil, ptr("PRIMARY"))
	w.snapshot(t1, w.O, ptr(1000.0), false)
	hidden := w.trip(w.C, w.partyY, "OUT", bkk(2026, 9, 6, 10, 0), nil, ptr("PRIMARY"))
	w.snapshot(hidden, w.O, ptr(900.0), false)
	r := w.rows("ta.o", url.Values{"customerId": {w.partyY}, "year": {"2026"}, "month": {"9"}})
	expect(t, r, http.StatusOK, "")
	if len(rowList(r)) != 1 || num(r.data()["outsideReach"]) != 1 || r.data()["documentable"] != false {
		t.Fatalf("outside reach %s", r.raw)
	}
	st := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 9})
	expect(t, st, http.StatusConflict, "failed_precondition")
	if st.detail("reason") != "rows_outside_reach" {
		t.Fatalf("%s", st.raw)
	}
}

// AC: unpriced rows carry reason codes: the stored one, else a dry run of the pricing rules over the live rate tables of
// the billing carrier (not_computed when the rules would price it).
func TestRowsUnpricedReasons(t *testing.T) {
	w := newWorld(t)
	w.hub("SPK-GW", "เจแอนด์ที บางปู", "")
	w.importRows("ta.o", w.partyY, "2026-01-01", rateRow("SPK-GW", "SPK890103", "6 Wheels", 1200, ""))
	feeR := w.post("/v1/billing/service-fees", w.login("ta.o"), map[string]any{"customerId": w.partyY, "feeType": "extra_stop",
		"amountThb": 150, "unit": "per_stop"})
	expect(t, feeR, http.StatusCreated, "")
	day := bkk(2026, 9, 12, 10, 0)
	trip := func(no string, s taskSpec, stamp bool) string {
		task := ""
		if s.tenant != "" {
			task = w.task(s)
		}
		party := ""
		if stamp {
			party = w.partyY
		}
		return w.tripOf(w.O, party, no, task, "", day, nil, "", false)
	}
	trip("NOTASK", taskSpec{}, true)
	trip("NOCLASS", taskSpec{tenant: w.O, party: w.partyY, src: "SPK-GW", dest: "SPK890103"}, false)
	trip("PRICES", taskSpec{tenant: w.O, party: w.partyY, src: "เจแอนด์ที บางปู", dest: "SPK890103-ลาดกระบัง26", class: "6WH"}, false)
	trip("NORATE", taskSpec{tenant: w.O, srcParty: w.partyY, src: "SPK-GW", dest: "SPK999", class: "6WH"}, false)
	stored := trip("STORED", taskSpec{tenant: w.O, party: w.partyY, src: "SPK-GW", dest: "SPK999", class: "6WH"}, true)
	w.snapshot(stored, w.O, nil, false) // unpriced_reason no_rate, stored by pricing
	trip("NOPARTY", taskSpec{tenant: w.O, src: "SPK-GW", dest: "SPK890103", class: "6WH"}, false)
	zero := trip("ZERO", taskSpec{tenant: w.O, party: w.partyY, src: "SPK-GW", dest: "SPK890103", class: "6WH"}, true)
	w.snapshot(zero, w.O, ptr(0.0), false) // a 0 price is no price
	mt := w.task(taskSpec{tenant: w.O, party: w.partyY, src: "SPK-GW", dest: "SPK890103", class: "6WH"})
	multi := w.tripOf(w.O, "", "MULTI", mt, "", day, nil, "", true)
	w.exec(`INSERT INTO trip_delivery_stops (trip_id, stop_index, destination_raw, status, completed_seq) VALUES
		($1, 1, 'SPK890103', 'delivered', 1), ($1, 2, 'SPK777', 'pending', NULL)`, multi)
	multi2 := w.tripOf(w.O, "", "MULTI2", mt, "", day, nil, "", true)
	w.exec(`INSERT INTO trip_delivery_stops (trip_id, stop_index, destination_raw, status, completed_seq) VALUES
		($1, 1, 'SPK777', 'delivered', 1), ($1, 2, 'SPK890103', 'delivered', 2)`, multi2)
	w.trip(w.O, w.partyX, "OTHER", day, ptr(day), ptr("PRIMARY")) // another party's unpriced trip: not in Y's month

	// Standby: no party, party with no rate, party with a rate (dry run prices it), stored reason.
	w.exec(`INSERT INTO standby_records (tenant_id, tenant_source, ended_at, started_at, status) VALUES ($1, 'driver', $2, $2, 'completed')`, w.O, day)
	w.exec(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status)
		VALUES ($1, 'driver', $2, $3, $3, 'completed')`, w.O, w.partyX, day)
	w.standbyRate(w.O, w.partyY, "2026-01-01", 450)
	w.exec(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status)
		VALUES ($1, 'driver', $2, $3, $3, 'completed')`, w.O, w.partyY, day)
	w.exec(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status,
		billing_unpriced_reason) VALUES ($1, 'driver', $2, $3, $3, 'completed', 'no_rate')`, w.O, w.partyY, day)

	r := w.rows("ta.o", url.Values{"customerId": {w.customerY}, "year": {"2026"}, "month": {"9"}})
	expect(t, r, http.StatusOK, "")
	if len(rowList(r)) != 0 {
		t.Fatalf("priced rows %v", rowList(r))
	}
	got := map[string]map[string]any{}
	standby := map[string]int{}
	for _, u := range unpricedList(r) {
		if str(u["rowType"]) == "standby" {
			standby[str(u["reason"])+"/"+str(u["reasonSource"])]++
			continue
		}
		got[str(u["tripNo"])] = u
	}
	for no, want := range map[string][2]string{
		"NOTASK": {"task_missing", "dry_run"}, "NOCLASS": {"no_vehicle_class", "dry_run"}, "PRICES": {"not_computed", "dry_run"},
		"NORATE": {"no_rate", "dry_run"}, "STORED": {"no_rate", "stored"}, "NOPARTY": {"no_customer", "dry_run"},
		"ZERO": {"not_computed", "dry_run"}, "MULTI": {"multi_insufficient_stops", "dry_run"}, "MULTI2": {"not_computed", "dry_run"},
	} {
		u, ok := got[no]
		if !ok || str(u["reason"]) != want[0] || str(u["reasonSource"]) != want[1] {
			t.Errorf("%s: %v, want %v", no, u, want)
		}
	}
	if _, ok := got["OTHER"]; ok || len(got) != 9 {
		t.Errorf("unpriced trips %v", got)
	}
	if p := got["PRICES"]; num(p["dryRunEstimateThb"]) != 1200 || str(p["lookupHubCode"]) != "SPK-GW" ||
		str(p["lookupDestinationCode"]) != "SPK890103" || str(p["lookupVehicleClass"]) != "6WH" ||
		str(p["billingPartyId"]) != w.partyY || str(p["customerId"]) != w.customerY || str(p["hubDisplayName"]) != "เจแอนด์ที บางปู" {
		t.Errorf("dry run of PRICES %v", p)
	}
	// Flat extra-stop mode: base 1200 + one fee 150 (no fuel on the fee).
	if num(got["MULTI2"]["dryRunEstimateThb"]) != 1350 {
		t.Errorf("multi-drop dry run %v", got["MULTI2"])
	}
	if !sameCounts(standby, map[string]int{"no_customer/dry_run": 1, "not_computed/dry_run": 1, "no_rate/stored": 1}) {
		t.Errorf("standby reasons %v", standby)
	}

	// standby-diagnostics: the same reasons from the same query, plus a record without ended_at (in no month at all).
	w.exec(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, status, created_at)
		VALUES ($1, 'driver', $2, 'completed', $3)`, w.O, w.partyY, day)
	d := w.get("/v1/billing/standby-diagnostics?customerId="+w.customerY+"&year=2026&month=9", w.login("ta.o"))
	expect(t, d, http.StatusOK, "")
	un, _ := d.data()["unpriced"].([]any)
	missing, _ := d.data()["missingEndedAt"].([]any)
	diag := map[string]int{}
	for _, x := range un {
		m := x.(map[string]any)
		diag[str(m["reason"])+"/"+str(m["reasonSource"])]++
	}
	if !sameCounts(diag, standby) || len(missing) != 1 || str(missing[0].(map[string]any)["reason"]) != "no_ended_at" ||
		d.data()["missingEndedAtTruncated"] != false {
		t.Fatalf("diagnostics %s", d.raw)
	}
	// X's record without a rate shows under X (no_rate) and the record no party resolves for under every party.
	dx := w.get("/v1/billing/standby-diagnostics?customerId="+w.partyX+"&year=2026&month=9", w.login("ta.o"))
	reasons := map[string]int{}
	for _, x := range dx.data()["unpriced"].([]any) {
		reasons[str(x.(map[string]any)["reason"])]++
	}
	if !sameCounts(reasons, map[string]int{"no_rate": 1, "no_customer": 1}) {
		t.Fatalf("X diagnostics %s", dx.raw)
	}
}

func sameCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// GET /v1/billing/rows/missing-billing-date: trips of the party delivered in the month without billing_date (in no plan
// period), priced or not; a trip with one, or of another month or party, is not listed.
func TestMissingBillingDate(t *testing.T) {
	w := newWorld(t)
	a := w.trip(w.O, w.partyX, "NODATE-P", bkk(2026, 9, 3, 9, 0), nil, ptr("PRIMARY"))
	w.snapshot(a, w.O, ptr(999.0), false)
	w.trip(w.O, w.partyX, "NODATE-U", bkk(2026, 9, 4, 9, 0), nil, ptr("PRIMARY"))
	w.trip(w.O, w.partyX, "DATED", bkk(2026, 9, 5, 9, 0), ptr(bkk(2026, 9, 5, 9, 0)), ptr("PRIMARY"))
	w.trip(w.O, w.partyX, "OCT", bkk(2026, 10, 1, 9, 0), nil, ptr("PRIMARY"))
	w.trip(w.O, w.partyY, "OTHERPARTY", bkk(2026, 9, 6, 9, 0), nil, ptr("PRIMARY"))
	r := w.get("/v1/billing/rows/missing-billing-date?customerId="+w.customerX+"&year=2026&month=9", w.login("ta.o"))
	expect(t, r, http.StatusOK, "")
	items, _ := r.body["data"].([]any)
	if len(items) != 2 {
		t.Fatalf("%s", r.raw)
	}
	first, second := items[0].(map[string]any), items[1].(map[string]any)
	if str(first["tripNo"]) != "NODATE-P" || first["priced"] != true || str(second["tripNo"]) != "NODATE-U" || second["priced"] != false ||
		str(first["deliveredAt"]) != "2026-09-03T02:00:00Z" {
		t.Fatalf("%s", r.raw)
	}
	// customerId=all is not a party here.
	if r := w.get("/v1/billing/rows/missing-billing-date?customerId=all&year=2026&month=9", w.login("ta.o")); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("all: %d", r.status)
	}
}

// The billing projection of GET /v1/trips (issue #50 attaches it for accounting:view_income): the stored snapshot with
// its stops for the billing carrier's staff; a carrier that ran a trip the own fleet priced reads the party but no price
// (R61).
func TestTripBillingProjection(t *testing.T) {
	w := newWorld(t)
	single := w.trip(w.A, w.partyY, "RUN-BY-A", bkk(2026, 9, 3, 10, 0), ptr(bkk(2026, 9, 3, 10, 0)), ptr("PRIMARY"))
	w.snapshot(single, w.O, ptr(1160.0), false)
	w.priced(single, "SPK-GW", "SPK890103", "2026-09-01", -40, false)
	m := w.trip(w.O, w.partyY, "MULTI", bkk(2026, 9, 4, 10, 0), nil, ptr("PRIMARY"))
	w.snapshot(m, w.O, ptr(1350.0), true)
	w.stop(m, 1, "SPK890103", 1200)
	w.stop(m, 2, "SPK777", 150)
	unpriced := w.trip(w.O, w.partyY, "UNPRICED", bkk(2026, 9, 5, 10, 0), nil, ptr("PRIMARY"))
	w.snapshot(unpriced, w.O, nil, false)
	path := "/v1/billingtest/trip-billing?ids=" + strings.Join([]string{single, m, unpriced}, ",")
	r := w.get(path, w.login("ta.o"))
	expect(t, r, http.StatusOK, "")
	trips, _ := r.data()["trips"].(map[string]any)
	s := trips[single].(map[string]any)
	if s["priced"] != true || num(s["estimateThb"]) != 1160 || str(s["lookupHubCode"]) != "SPK-GW" || num(s["addThbPerTrip"]) != -40 ||
		str(s["roundEffectiveFromDate"]) != "2026-09-01" || str(s["customerId"]) != w.customerY || r.data()["visible"] != true {
		t.Fatalf("single %v", s)
	}
	mm := trips[m].(map[string]any)
	if stops, _ := mm["stops"].([]any); mm["isMultiDelivery"] != true || len(stops) != 2 || num(stops[1].(map[string]any)["finalRateThb"]) != 150 {
		t.Fatalf("multi %v", mm)
	}
	u := trips[unpriced].(map[string]any)
	if u["priced"] != false || u["estimateThb"] != nil || str(u["unpricedReason"]) != "no_rate" {
		t.Fatalf("unpriced %v", u)
	}
	// Carrier A ran the first trip: it reads the trip's party, never the own fleet's price.
	ra := w.get("/v1/billingtest/trip-billing?ids="+single, w.login("ta.a"))
	expect(t, ra, http.StatusOK, "")
	a := ra.data()["trips"].(map[string]any)[single].(map[string]any)
	if a["estimateThb"] != nil || a["priced"] != false || str(a["billingPartyId"]) != w.partyY {
		t.Fatalf("carrier A reads %v", a)
	}
	if rp := w.get("/v1/billingtest/trip-billing?ids="+single, w.login("us.o")); rp.data()["visible"] != false {
		t.Fatalf("a user without accounting:view_income: %s", rp.raw)
	}
}
