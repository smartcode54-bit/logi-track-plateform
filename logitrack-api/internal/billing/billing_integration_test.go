//go:build integration

package billing_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/bangchak"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// bkk is a Bangkok wall time as an instant.
func bkk(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.FixedZone("ICT", 7*3600)).UTC()
}

// AC: UPDATE/DELETE on announcement rows are rejected except the void columns + updated_at (main spec §6.12).
func TestAnnouncementRowsAreVoidOnly(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	importID := w.importRows("ta.o", w.partyX, "2026-10-01",
		rateRow("SPK-GW", "SPK890103-ลาดกระบัง26", "6 Wheels", 1200, ""),
		rateRow("HUBA - Hub A", "soce", "4wj", 900.5, "เสริม"))
	if importID != fmt.Sprintf("rc_%d", now.UnixMilli()) {
		t.Fatalf("import id %s", importID)
	}
	r := w.get("/v1/billing/rate-entries?customerId="+w.customerX, tok) // the customer id names its party too
	expect(t, r, http.StatusOK, "")
	rows := r.list()
	if len(rows) != 2 {
		t.Fatalf("rows %s", r.raw)
	}
	byHub := map[string]map[string]any{}
	for _, e := range rows {
		byHub[str(e["hubId"])] = e
		if str(e["tenantId"]) != w.O || str(e["billingPartyId"]) != w.partyX || str(e["customerId"]) != w.customerX ||
			str(e["effectiveFrom"]) != "2026-09-30T17:00:00Z" || str(e["effectiveFromDateStr"]) != "2026-10-01" ||
			str(e["importId"]) != importID || e["voided"] != false {
			t.Fatalf("row %v", e)
		}
	}
	// codes normalised on write as the web wrote them (extractHubId, normalizeDestinationCode with the SPK collapse,
	// the vehicle-class mapper, jobCategoryFromCell).
	if e := byHub["SPK-GW"]; str(e["destinationCode"]) != "SPK890103" || str(e["vehicleClass"]) != "6WH" ||
		num(e["rateThb"]) != 1200 || str(e["jobCategory"]) != "PRIMARY" {
		t.Fatalf("SPK-GW row %v", e)
	}
	if e := byHub["HUBA"]; str(e["destinationCode"]) != "SOCE" || str(e["vehicleClass"]) != "4WJ" ||
		num(e["rateThb"]) != 900.5 || str(e["jobCategory"]) != "SUPPLEMENTARY" || str(e["rawHubName"]) != "HUBA - Hub A" {
		t.Fatalf("HUBA row %v", e)
	}

	// The database, as logitrack_app under the caller's RLS: only the void columns and updated_at may change.
	for what, want := range map[string]string{
		"update-rate": "23000", "void-rate-and-pay": "23000", "void-rate-no-by": "23000", "delete-rate": "42501",
		"void-rate": "",
	} {
		if got, n := w.mutate("ta.o", what); got != want || (want == "" && n != 2) {
			t.Errorf("%s: sqlstate %q (%d rows), want %q", what, got, n, want)
		}
	}

	// The API's void: reason required, voided_by the caller, once.
	id := str(byHub["SPK-GW"]["id"])
	expect(t, w.post("/v1/billing/rate-entries/"+id+"/void", tok, map[string]any{"reason": " "}), http.StatusUnprocessableEntity, "invalid_argument")
	v := w.post("/v1/billing/rate-entries/"+id+"/void", tok, map[string]any{"reason": "wrong price"})
	expect(t, v, http.StatusOK, "")
	if v.data()["voided"] != true || str(v.data()["voidedBy"]) != w.userTAO || str(v.data()["voidedReason"]) != "wrong price" ||
		str(v.data()["voidedAt"]) != now.Format(time.RFC3339) || num(v.data()["rateThb"]) != 1200 {
		t.Fatalf("voided %s", v.raw)
	}
	again := w.post("/v1/billing/rate-entries/"+id+"/void", tok, map[string]any{"reason": "twice"})
	expect(t, again, http.StatusConflict, billing.CodeAnnouncementImmutable)
	if again.detail("reason") != "already_voided" {
		t.Fatalf("second void %s", again.raw)
	}
	if got, _ := w.mutate("ta.o", "unvoid-rate"); got != "23000" {
		t.Fatalf("un-void: sqlstate %q", got)
	}
	expect(t, w.post("/v1/billing/rate-entries/"+uuid.NewString()+"/void", tok, map[string]any{"reason": "x"}), http.StatusNotFound, "not_found")
	if l := w.get("/v1/billing/rate-entries?customerId="+w.partyX, tok).list(); len(l) != 1 {
		t.Fatalf("voided rows are hidden by default: %v", l)
	}
	if l := w.get("/v1/billing/rate-entries?customerId="+w.partyX+"&includeVoided=true", tok).list(); len(l) != 2 {
		t.Fatalf("includeVoided: %v", l)
	}

	// Fuel rounds: the same rule.
	f := w.post("/v1/billing/fuel-adjustments", tok, map[string]any{"customerId": w.partyX, "effectiveFromDateStr": "2026-10-01",
		"mode": "percent", "pct": 5})
	expect(t, f, http.StatusCreated, "")
	for what, want := range map[string]string{"update-fuel": "23000", "delete-fuel": "42501", "delete-standby": "42501"} {
		if got, _ := w.mutate("ta.o", what); got != want {
			t.Errorf("%s: sqlstate %q, want %q", what, got, want)
		}
	}
	fid := str(f.data()["id"])
	expect(t, w.post("/v1/billing/fuel-adjustments/"+fid+"/void", tok, map[string]any{"reason": "typo"}), http.StatusOK, "")
	expect(t, w.post("/v1/billing/fuel-adjustments/"+fid+"/void", tok, map[string]any{"reason": "typo"}), http.StatusConflict,
		billing.CodeAnnouncementImmutable)
}

func TestRateEntryRules(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	expect(t, w.get("/v1/billing/rate-entries", w.login("us.o")), http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/billing/rate-entries/import", w.login("us.o"), map[string]any{}), http.StatusForbidden, "permission_denied")

	r := w.post("/v1/billing/rate-entries/import", tok, map[string]any{"customerId": w.partyX, "effectiveFromDateStr": "2026-02-30",
		"rows": []map[string]any{
			rateRow("HUBA", "SOCE", "", 100, ""),                  // blank class: never the legacy 4WJ (R15)
			rateRow("HUBA", "SOCE", "Trailer", 100, "เสริมพิเศษ"), // unknown class, unknown category (never PRIMARY)
			rateRow("", "", "4WJ", 10.555, "primary"),
			rateRow("HUBA", "SOCN", "VAN", -1, "งานหลัก"),
		}})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	want := map[string]string{"effectiveFromDateStr": "invalid", "rows[0].vehicleClass": "required",
		"rows[1].vehicleClass": "invalid", "rows[1].jobCategory": "invalid", "rows[2].hubId": "required",
		"rows[2].destinationCode": "required", "rows[2].rateThb": "precision", "rows[3].rateThb": "out_of_range"}
	if got := r.fields(); !maps(got, want) {
		t.Fatalf("fields %v, want %v", got, want)
	}
	r = w.post("/v1/billing/rate-entries/import", tok, map[string]any{"customerId": uuid.NewString(), "effectiveFromDateStr": "2026-10-01",
		"rows": []map[string]any{rateRow("HUBA", "SOCE", "4WJ", 100, "")}})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["customerId"] != "not_found" {
		t.Fatalf("unknown party %s", r.raw)
	}

	// A manual row: import id manual_<ms>, the raw hub name defaults to the hub, the carrier party of A is billable too.
	m := w.post("/v1/billing/rate-entries", tok, map[string]any{"customerId": w.A, "effectiveFromDateStr": "2026-10-11",
		"hubId": "hubz", "destinationCode": "SPK123-x", "vehicleClass": "10W", "rateThb": 2500, "distanceKm": 12.5,
		"jobCategory": "supplement"})
	expect(t, m, http.StatusCreated, "")
	d := m.data()
	if str(d["importId"]) != fmt.Sprintf("manual_%d", now.UnixMilli()) || str(d["hubId"]) != "HUBZ" ||
		str(d["rawHubName"]) != "HUBZ" || str(d["destinationCode"]) != "SPK123" ||
		str(d["vehicleClass"]) != "10WH" || num(d["distanceKm"]) != 12.5 || str(d["jobCategory"]) != "SUPPLEMENTARY" ||
		str(d["createdBy"]) != w.userTAO || str(d["tenantId"]) != w.O {
		t.Fatalf("manual row %s", m.raw)
	}
	partyA := scan[string](w, `SELECT id::text FROM billing_parties WHERE tenant_id = $1`, w.A)
	if str(d["billingPartyId"]) != partyA || str(d["customerId"]) != w.A { // a carrier party bills the carrier
		t.Fatalf("carrier party %s / %s, want %s / %s", d["billingPartyId"], d["customerId"], partyA, w.A)
	}

	// Fuel rounds: the multiplier and add are computed once, here (main spec §6.6).
	for _, tc := range []struct {
		body       map[string]any
		mult, add  float64
		band       bool
		lower, upr float64
	}{
		{map[string]any{"mode": "percent", "pct": 5.5}, 1.055, 0, false, 0, 0},
		{map[string]any{"mode": "percent", "pct": -2, "referenceFuelPriceThbPerLitre": 42}, 0.98, 0, false, 41.01, 42},
		{map[string]any{"mode": "fixed_thb", "addThbPerTrip": -40}, 1, -40, false, 0, 0},
		{map[string]any{"mode": "band", "referenceFuelPriceThbPerLitre": 43.01}, 1, 20, true, 43.01, 44},
		{map[string]any{"mode": "band", "referenceFuelPriceThbPerLitre": 39, "fuelBandBaselineFuelFloor": 0, "fuelBandThbPerBaht": 0}, 1, -30, true, 38.01, 39},
		{map[string]any{"mode": "band", "referenceFuelPriceThbPerLitre": 42.01, "fuelBandBaselineFuelFloor": 40, "fuelBandThbPerBaht": 5}, 1, 10, true, 42.01, 43},
	} {
		body := map[string]any{"customerId": w.partyY, "effectiveFromDateStr": "2026-10-01", "announcementNote": "round"}
		for k, v := range tc.body {
			body[k] = v
		}
		r := w.post("/v1/billing/fuel-adjustments", tok, body)
		expect(t, r, http.StatusCreated, "")
		d := r.data()
		if num(d["rateMultiplier"]) != tc.mult || num(d["addThbPerTrip"]) != tc.add || d["fuelBandEnabled"] != tc.band {
			t.Errorf("%v: %s", tc.body, r.raw)
		}
		if tc.lower != 0 {
			b, _ := d["fuelBand"].(map[string]any)
			if num(b["lowerThb"]) != tc.lower || num(b["upperThb"]) != tc.upr {
				t.Errorf("%v: band %v", tc.body, b)
			}
		}
	}
	for body, field := range map[string][2]string{
		`{"mode":"percent"}`:               {"pct", "required"},
		`{"mode":"percent","pct":-100}`:    {"pct", "out_of_range"},
		`{"mode":"percent","pct":1.23456}`: {"pct", "precision"},
		`{"mode":"fixed_thb"}`:             {"addThbPerTrip", "required"},
		`{"mode":"band"}`:                  {"referenceFuelPriceThbPerLitre", "required"},
		`{"mode":"band","referenceFuelPriceThbPerLitre":42,"fuelBandBaselineFuelFloor":41.5}`: {"fuelBandBaselineFuelFloor", "invalid"},
		`{"mode":"curve"}`: {"mode", "invalid"},
		`{}`:               {"mode", "required"},
	} {
		var extra map[string]any
		if err := jsonUnmarshal(body, &extra); err != nil {
			t.Fatal(err)
		}
		extra["customerId"], extra["effectiveFromDateStr"] = w.partyY, "2026-10-01"
		r := w.post("/v1/billing/fuel-adjustments", tok, extra)
		expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
		if r.fields()[field[0]] != field[1] {
			t.Errorf("%s: %s", body, r.raw)
		}
	}
}

func maps(a, b map[string]string) bool {
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

// AC: equal effective_from: legacy rows by legacy_doc_id ASC, new rows by created_at, id (R16), whatever order they load in.
func TestPricingTablesFollowR16(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	ctx := context.Background()
	instant := bkk(2026, 9, 1, 0, 0)
	// Two ETL-loaded rows of one key and instant, loaded doc id b first.
	for _, l := range []struct {
		doc  string
		rate float64
		at   time.Time
	}{{"b", 1000, now.Add(-2 * time.Hour)}, {"a", 1100, now.Add(-time.Hour)}} {
		w.exec(`INSERT INTO customer_rate_entries (legacy_doc_id, tenant_id, billing_party_id, import_id, hub_code,
			destination_code, vehicle_class, rate_thb, effective_from_date, effective_from_at, created_at)
			VALUES ($1, $2, $3, 'rc_1', 'HUBA', 'SOCE', '4WJ', $4, '2026-09-01', $5, $6)`, l.doc, w.O, w.partyY, l.rate, instant, l.at)
	}
	// New rows of the same key and day through the API: the first created must win among them.
	for _, rate := range []float64{1200, 1300} {
		w.importRows("ta.o", w.partyY, "2026-09-01", rateRow("HUBA", "SOCE", "4WJ", rate, ""))
		w.importRows("ta.o", w.partyY, "2026-09-01", rateRow("HUBB", "SOCE", "4WJ", rate, ""))
	}
	// One import with two rows of a third key: input order (same created_at, uuidv7 ids) decides.
	w.importRows("ta.o", w.partyY, "2026-09-01", rateRow("HUBC", "SOCE", "4WJ", 700, ""), rateRow("HUBC", "SOCE", "4WJ", 800, ""))
	// Fuel rounds and standby rates of one day: the first created wins.
	for _, pct := range []float64{1, 2} {
		expect(t, w.post("/v1/billing/fuel-adjustments", tok, map[string]any{"customerId": w.partyY, "effectiveFromDateStr": "2026-09-01",
			"mode": "percent", "pct": pct}), http.StatusCreated, "")
		expect(t, w.post("/v1/billing/standby-rates", tok, map[string]any{"customerId": w.partyY, "effectiveFromDateStr": "2026-09-01",
			"rateThb": 500 + pct}), http.StatusCreated, "")
	}

	var tables billing.PricingTables
	if err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
		carrier, err := billing.BillingCarrier(ctx, tx)
		if err != nil {
			return err
		}
		tables, err = billing.LoadPricingTables(ctx, tx, uuid.MustParse(w.partyY), carrier)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if tables.Carrier != uuid.MustParse(w.O) {
		t.Fatalf("billing carrier %v, want the own fleet", tables.Carrier)
	}
	if len(tables.Rates) != 8 || tables.Rates[0].LegacyDocID != "a" || tables.Rates[1].LegacyDocID != "b" {
		t.Fatalf("pricing order (legacy rows by doc id first): %+v", tables.Rates[:2])
	}
	bill := bkk(2026, 9, 15, 12, 0)
	for i := 0; i < 20; i++ { // the selector applies R16 itself: any load order gives the same winner
		rates := slices.Clone(tables.Rates)
		rand.Shuffle(len(rates), func(a, b int) { rates[a], rates[b] = rates[b], rates[a] })
		for hub, want := range map[string]float64{"HUBA": 1100, "HUBB": 1200, "HUBC": 700} {
			e, ok := compute.SelectRateEntry(w.partyY, hub, "SOCE", "4WJ", bill, rates, compute.Primary)
			if !ok || e.RateTHB != want {
				t.Fatalf("%s: picked %+v, want %v", hub, e, want)
			}
		}
		fuel := slices.Clone(tables.Fuel)
		rand.Shuffle(len(fuel), func(a, b int) { fuel[a], fuel[b] = fuel[b], fuel[a] })
		if a, ok := compute.SelectFuelAdjustment(w.partyY, bill, fuel); !ok || a.RateMultiplier != 1.01 {
			t.Fatalf("fuel %+v", a)
		}
		sb := slices.Clone(tables.StandbyRates)
		rand.Shuffle(len(sb), func(a, b int) { sb[a], sb[b] = sb[b], sb[a] })
		if r, ok := compute.SelectStandbyRate(w.partyY, bill, sb); !ok || r.RateTHB != 501 {
			t.Fatalf("standby %+v", r)
		}
	}
	// A void (FOR UPDATE) waits while a pricing transaction holds its rows FOR SHARE.
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // a failed assertion must not leave the connection holding its locks
	if _, err := tx.Exec(ctx, `SELECT set_config('app.bypass_tenant', 'on', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := billing.LoadPricingTables(ctx, tx, uuid.MustParse(w.partyY), uuid.MustParse(w.O)); err != nil {
		t.Fatal(err)
	}
	winner := ""
	for _, e := range tables.Rates {
		if e.HubCode == "HUBB" && e.RateTHB == 1200 {
			winner = e.ID
		}
	}
	done := make(chan resp, 1)
	go func() {
		done <- w.post("/v1/billing/rate-entries/"+winner+"/void", tok, map[string]any{"reason": "late"})
	}()
	select {
	case r := <-done:
		t.Fatalf("the void did not wait for the pricing transaction: %d %s", r.status, r.raw)
	case <-time.After(400 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expect(t, <-done, http.StatusOK, "")
}

// statement creates a statement as user and returns the response.
func (w *world) statement(user string, body map[string]any) resp {
	w.t.Helper()
	return w.post("/v1/billing/statements", w.login(user), body)
}

// AC: a failed statement save leaves no invoice-number gap; a deleted draft leaves its number unused.
func TestStatementNumbersHaveNoGap(t *testing.T) {
	w := newWorld(t)
	t1 := w.trip(w.O, w.partyY, "T-1", bkk(2026, 10, 5, 10, 0), nil, ptr("PRIMARY"))
	w.snapshot(t1, w.O, ptr(1160.0), false)

	s1 := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, s1, http.StatusCreated, "")
	if str(s1.data()["invoiceNumber"]) != "TTP-202610-001" {
		t.Fatalf("first number %s", s1.raw)
	}
	// Fails after the counter moved and the statement row was written: nothing billable among the chosen types.
	f := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10, "types": []string{"standby"}})
	expect(t, f, http.StatusConflict, billing.CodeFailedPrecondition)
	if f.detail("reason") != "no_billable_rows" {
		t.Fatalf("failure %s", f.raw)
	}
	if n := scan[int](w, `SELECT last_seq FROM billing_counters WHERE billing_party_id = $1 AND period_year = 2026 AND period_month = 10`, w.partyY); n != 1 {
		t.Fatalf("counter after a failed save: %d, want 1 (no gap)", n)
	}
	s2 := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, s2, http.StatusCreated, "")
	if str(s2.data()["invoiceNumber"]) != "TTP-202610-002" {
		t.Fatalf("second number %s", s2.raw)
	}
	expect(t, w.del("/v1/billing/statements/"+str(s2.data()["id"]), w.login("ta.o")), http.StatusNoContent, "")
	s3 := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, s3, http.StatusCreated, "")
	if str(s3.data()["invoiceNumber"]) != "TTP-202610-003" {
		t.Fatalf("after a deleted draft %s", s3.raw)
	}
	// Other parties and months number on their own counters; the counters are reachable only through the allocator.
	other := w.trip(w.O, w.partyY, "T-2", bkk(2026, 11, 1, 0, 0), nil, ptr("PRIMARY"))
	w.snapshot(other, w.O, ptr(100.0), false)
	s4 := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 11})
	if str(s4.data()["invoiceNumber"]) != "TTP-202611-001" {
		t.Fatalf("November %s", s4.raw)
	}
	for _, what := range []string{"read-counters", "bump-counter"} {
		if got, _ := w.mutate("ta.o", what); got != "42501" {
			t.Errorf("%s: sqlstate %q, want 42501 (only next_invoice_seq reaches the counters)", what, got)
		}
	}
	if got, _ := w.mutate("ta.o", "update-line"); got != "42501" {
		t.Errorf("statement lines are immutable: sqlstate %q", got)
	}
}

// The invoice set: the party's axis inside the Bangkok month, this billing carrier's priced snapshots, multi-drop stops,
// standby by ended_at, and the type and category toggles (main spec §6.14).
func TestStatementInvoiceSet(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	prim, supp := ptr("PRIMARY"), ptr("SUPPLEMENTARY")
	// Plan-basis party X: the axis is billing_date.
	p1 := w.trip(w.O, w.partyX, "P1", bkk(2026, 11, 2, 9, 0), ptr(bkk(2026, 10, 3, 8, 0)), prim)
	w.snapshot(p1, w.O, ptr(1000.10), false)
	p2 := w.trip(w.O, w.partyX, "P2", bkk(2026, 10, 1, 9, 0), ptr(bkk(2026, 9, 30, 23, 59)), prim) // plan date in September
	w.snapshot(p2, w.O, ptr(999.0), false)
	p3 := w.trip(w.O, w.partyX, "P3", bkk(2026, 10, 2, 9, 0), nil, prim) // no billing_date: invisible to plan periods
	w.snapshot(p3, w.O, ptr(999.0), false)
	p4 := w.trip(w.O, w.partyX, "P4", bkk(2026, 10, 4, 9, 0), ptr(bkk(2026, 10, 4, 0, 0)), supp)
	w.snapshot(p4, w.O, ptr(950.0), false)
	p5 := w.trip(w.O, w.partyX, "P5", bkk(2026, 10, 5, 9, 0), ptr(bkk(2026, 10, 5, 0, 0)), nil) // unverified
	w.snapshot(p5, w.O, ptr(800.0), false)
	m := w.trip(w.O, w.partyX, "M1", bkk(2026, 10, 6, 9, 0), ptr(bkk(2026, 10, 31, 23, 59)), prim)
	w.snapshot(m, w.O, ptr(1300.0), true)
	w.stop(m, 1, "SOCE", 1000)
	w.stop(m, 2, "SOCN", 300)
	w.stop(m, 3, "SOCW", 0) // a stop without a charge is no line
	z := w.trip(w.O, w.partyX, "Z0", bkk(2026, 10, 7, 9, 0), ptr(bkk(2026, 10, 7, 0, 0)), prim)
	w.snapshot(z, w.O, ptr(0.0), false)
	u := w.trip(w.O, w.partyX, "U0", bkk(2026, 10, 7, 9, 0), ptr(bkk(2026, 10, 7, 0, 0)), prim)
	w.snapshot(u, w.O, nil, false)
	b := w.trip(w.A, w.partyX, "B0", bkk(2026, 10, 8, 9, 0), ptr(bkk(2026, 10, 8, 0, 0)), prim) // priced by carrier A
	w.snapshot(b, w.A, ptr(700.0), false)
	carrierTrip := w.trip(w.A, w.partyX, "CT", bkk(2026, 10, 9, 9, 0), ptr(bkk(2026, 10, 9, 0, 0)), prim) // run by A, billed by O
	w.snapshot(carrierTrip, w.O, ptr(1500.0), false)
	// Standby: priced from O's rate (in), from A's rate (out), ended in September (out).
	rO := w.post("/v1/billing/standby-rates", tok, map[string]any{"customerId": w.partyX, "rateThb": 500, "effectiveFromDateStr": "2026-01-01"})
	expect(t, rO, http.StatusCreated, "")
	expect(t, w.post("/v1/billing/standby-rates", w.login("ta.a"), map[string]any{"customerId": w.partyX, "rateThb": 400,
		"effectiveFromDateStr": "2026-01-01"}), http.StatusForbidden, "permission_denied") // only the own fleet bills (§19 Q13)
	rA := w.standbyRate(w.A, w.partyX, "2026-01-01", 400) // a carrier's row that bypassed the API (ETL)
	s1 := w.standby(w.O, w.partyX, str(rO.data()["id"]), w.O, bkk(2026, 10, 10, 0, 0), 500)
	w.standby(w.A, w.partyX, rA, w.A, bkk(2026, 10, 10, 0, 0), 400)
	w.standby(w.O, w.partyX, str(rO.data()["id"]), w.O, bkk(2026, 9, 30, 23, 0), 500)

	r := w.statement("ta.o", map[string]any{"customerId": w.partyX, "year": 2026, "month": 10, "note": "October",
		"categories": []string{"PRIMARY", "unverified"}})
	expect(t, r, http.StatusCreated, "")
	d := r.data()
	// P1 1000.10 + P5 800 + CT 1500 + M1 stops 1000 + 300 + standby 500 = 5100.10
	if str(d["invoiceNumber"]) != "CJSF-202610-001" || num(d["tripCount"]) != 6 || num(d["tripOnlyCount"]) != 3 ||
		num(d["tripSubtotal"]) != 3300.1 || num(d["multiDropCount"]) != 2 || num(d["multiDropSubtotal"]) != 1300 ||
		num(d["standbyCount"]) != 1 || num(d["standbySubtotal"]) != 500 || num(d["totalAmount"]) != 5100.1 ||
		num(d["withholdingTaxRate"]) != 0.03 || num(d["withholdingTax"]) != 153 || num(d["netAmount"]) != 4947.1 ||
		str(d["status"]) != "draft" || str(d["tenantId"]) != w.O || str(d["issuerCompanyId"]) != w.companyO ||
		str(d["customerName"]) != "CJ Super Fresh" || str(d["customerCode"]) != "CJSF" || str(d["note"]) != "October" ||
		str(d["generatedAt"]) != now.Format(time.RFC3339) || str(d["dueDate"]) != now.Add(30*24*time.Hour).Format(time.RFC3339) ||
		str(d["generatedBy"]) != w.userTAO || str(d["generatedByName"]) != "ta.o@example.test" ||
		str(d["customerId"]) != w.customerX || str(d["billingPartyId"]) != w.partyX {
		t.Fatalf("statement %s", r.raw)
	}
	// documents[] is in the create response and the detail even before the render (T39): an empty list, not a missing key.
	if docs, ok := d["documents"].([]any); !ok || len(docs) != 0 {
		t.Fatalf("documents before the render (T39): %s", r.raw)
	}
	detail := w.get("/v1/billing/statements/"+str(d["id"]), tok)
	expect(t, detail, http.StatusOK, "")
	if docs, ok := detail.data()["documents"].([]any); !ok || len(docs) != 0 {
		t.Fatalf("detail documents: %s", detail.raw)
	}
	if l := w.get("/v1/billing/statements?customerId="+w.customerX, tok).list(); len(l) != 1 ||
		str(l[0]["generatedByName"]) != "ta.o@example.test" || str(l[0]["customerId"]) != w.customerX {
		t.Fatalf("list row %v", l)
	} else if _, has := l[0]["documents"]; has {
		t.Fatalf("a list row carries documents: %v", l[0])
	}
	// The creator's name: the display name, else the email; null without a creator.
	w.exec(`UPDATE users SET display_name = '  ' WHERE id = $1`, w.userTAO)
	if r := w.get("/v1/billing/statements/"+str(d["id"]), tok); str(r.data()["generatedByName"]) != "ta.o@example.test" {
		t.Fatalf("blank display name: %s", r.raw)
	}
	w.exec(`UPDATE users SET display_name = 'Ta O' WHERE id = $1`, w.userTAO)
	if r := w.get("/v1/billing/statements/"+str(d["id"]), tok); str(r.data()["generatedByName"]) != "Ta O" {
		t.Fatalf("display name: %s", r.raw)
	}
	w.exec(`UPDATE billing_statements SET generated_by = NULL WHERE id = $1`, str(d["id"]))
	if r := w.get("/v1/billing/statements/"+str(d["id"]), tok); r.data()["generatedByName"] != nil || r.data()["generatedBy"] != nil {
		t.Fatalf("no creator: %s", r.raw)
	}
	lines := w.get("/v1/billing/statements/"+str(d["id"])+"/lines", tok)
	expect(t, lines, http.StatusOK, "")
	got := map[string]float64{}
	for _, l := range lines.list() {
		got[str(l["rowType"])+":"+str(l["rowId"])] = num(l["amountThb"])
	}
	want := map[string]float64{"trip:" + p1: 1000.1, "trip:" + p5: 800, "trip:" + carrierTrip: 1500,
		"multidrop_stop:" + m + "_s1": 1000, "multidrop_stop:" + m + "_s2": 300, "standby:" + s1: 500}
	if len(got) != len(want) {
		t.Fatalf("lines %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("lines %v, want %v", got, want)
		}
	}
	// Statement creation emitted statement.created for documents.render (T39) on the billing tenant's topic.
	if n := scan[int](w, `SELECT count(*) FROM outbox_events WHERE routing_key = 'statement.created' AND aggregate_id = $1
		AND tenant_id = $2 AND realtime_topics = ARRAY['tenant:' || $2 || ':billing'] AND payload->>'billingPartyId' = $3`,
		str(d["id"]), w.O, w.partyX); n != 1 {
		t.Fatalf("statement.created rows %d", n)
	}

	// Toggles: SUPPLEMENTARY only adds P4; standby only adds S1; an empty list is refused.
	r = w.statement("ta.o", map[string]any{"customerId": w.partyX, "year": 2026, "month": 10, "categories": []string{"SUPPLEMENTARY"},
		"types": []string{"trip", "multidrop"}})
	expect(t, r, http.StatusCreated, "")
	if num(r.data()["totalAmount"]) != 950 || str(r.data()["invoiceNumber"]) != "CJSF-202610-002" {
		t.Fatalf("supplementary %s", r.raw)
	}
	r = w.statement("ta.o", map[string]any{"customerId": w.partyX, "year": 2026, "month": 10, "types": []string{"standby"},
		"categories": []string{"SUPPLEMENTARY"}}) // standby is exempt from the category toggles
	expect(t, r, http.StatusCreated, "")
	if num(r.data()["totalAmount"]) != 500 || num(r.data()["standbyCount"]) != 1 {
		t.Fatalf("standby %s", r.raw)
	}
	r = w.statement("ta.o", map[string]any{"customerId": w.partyX, "year": 2026, "month": 13, "types": []string{}, "categories": []string{"OTHER"}})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["month"] != "out_of_range" || f["types"] != "required" || f["categories"] != "invalid" {
		t.Fatalf("toggles %s", r.raw)
	}

	// Delivered-basis party Y: Bangkok month bounds on delivered_at (00:00 ICT on the 1st is in, 23:59:59 the day before out).
	in := w.trip(w.O, w.partyY, "Y-IN", time.Date(2026, 9, 30, 17, 0, 0, 0, time.UTC), ptr(bkk(2026, 1, 1, 0, 0)), prim)
	w.snapshot(in, w.O, ptr(10.0), false)
	out := w.trip(w.O, w.partyY, "Y-OUT", time.Date(2026, 9, 30, 16, 59, 59, 0, time.UTC), nil, prim)
	w.snapshot(out, w.O, ptr(20.0), false)
	r = w.statement("ta.o", map[string]any{"customerId": w.customerY, "year": 2026, "month": 10})
	expect(t, r, http.StatusCreated, "")
	if num(r.data()["totalAmount"]) != 10 || r.data()["dueDate"] != nil {
		t.Fatalf("delivered basis %s", r.raw)
	}
	// An issuer outside the billing tenant, or none: 422.
	r = w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10, "issuerCompanyId": w.companyA})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["issuerCompanyId"] != "not_found" {
		t.Fatalf("issuer %s", r.raw)
	}
}

// AC: a statement keeps its WHT rate after the company rate changes (R18).
func TestStatementKeepsItsWithholdingRate(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	for i, amount := range []float64{1160.10, 333.33} {
		id := w.trip(w.O, w.partyY, fmt.Sprintf("W-%d", i), bkk(2026, 10, 2+i, 9, 0), nil, ptr("PRIMARY"))
		w.snapshot(id, w.O, &amount, false)
	}
	r := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, r, http.StatusCreated, "")
	id := str(r.data()["id"])
	// 1493.43 x 0.03 = 44.8029 -> 44.80 (WithholdingTHB); net 1448.63.
	check := func(r resp, rate, wht, net float64) {
		t.Helper()
		if num(r.data()["totalAmount"]) != 1493.43 || num(r.data()["withholdingTaxRate"]) != rate ||
			num(r.data()["withholdingTax"]) != wht || num(r.data()["netAmount"]) != net {
			t.Fatalf("statement %s", r.raw)
		}
	}
	check(r, 0.03, 44.8, 1448.63)
	w.exec(`UPDATE companies SET withholding_tax_rate = 1 WHERE id = $1`, w.companyO)
	check(w.get("/v1/billing/statements/"+id, tok), 0.03, 44.8, 1448.63)
	r = w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, r, http.StatusCreated, "")
	check(r, 0.01, 14.93, 1478.5)
	// A named issuer of the billing tenant.
	other := w.company(w.O, "subcontractor", 5)
	r = w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10, "issuerCompanyId": other})
	expect(t, r, http.StatusCreated, "")
	check(r, 0.05, 74.67, 1418.76)
}

// AC: rate rows, counters and statements of a trip run by a carrier carry the own-fleet billing tenant (R61).
func TestBillingTenantOfACarrierTrip(t *testing.T) {
	w := newWorld(t)
	tokO, tokA := w.login("ta.o"), w.login("ta.a")
	w.importRows("ta.o", w.partyY, "2026-10-01", rateRow("HUBA", "SOCE", "4WJ", 1200, ""))
	expect(t, w.post("/v1/billing/fuel-adjustments", tokO, map[string]any{"customerId": w.partyY, "effectiveFromDateStr": "2026-10-01",
		"mode": "fixed_thb", "addThbPerTrip": 10}), http.StatusCreated, "")
	expect(t, w.post("/v1/billing/service-fees", tokO, map[string]any{"customerId": w.partyY, "feeType": "extra_stop",
		"amountThb": 300, "unit": "per_stop"}), http.StatusCreated, "")
	expect(t, w.post("/v1/billing/standby-rates", tokO, map[string]any{"customerId": w.partyY, "rateThb": 500,
		"effectiveFromDateStr": "2026-10-01"}), http.StatusCreated, "")
	for _, tbl := range []string{"customer_rate_entries", "customer_fuel_rate_adjustments", "customer_service_fees", "standby_rate_entries"} {
		if n := scan[int](w, `SELECT count(*) FROM `+tbl+` WHERE tenant_id <> $1 OR tenant_source <> 'form'`, w.O); n != 0 {
			t.Errorf("%s: %d rows not stamped with the own fleet (form)", tbl, n)
		}
	}
	// Carrier A ran the trip; the own fleet priced it (its snapshot carries O) and bills it.
	trip := w.trip(w.A, w.partyY, "A-1", bkk(2026, 10, 5, 9, 0), nil, ptr("PRIMARY"))
	w.snapshot(trip, w.O, ptr(1210.0), false)
	r := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, r, http.StatusCreated, "")
	id := str(r.data()["id"])
	if str(r.data()["tenantId"]) != w.O || num(r.data()["totalAmount"]) != 1210 {
		t.Fatalf("statement %s", r.raw)
	}
	if got := scan[string](w, `SELECT tenant_id::text || '/' || tenant_source FROM billing_counters WHERE billing_party_id = $1`, w.partyY); got != w.O+"/form" {
		t.Fatalf("counter tenant %s", got)
	}
	if got := scan[string](w, `SELECT tenant_id::text || '/' || tenant_source FROM billing_statements WHERE id = $1`, id); got != w.O+"/form" {
		t.Fatalf("statement tenant %s", got)
	}
	// The carrier never reads the price its contractor charges: rate tables, statements.
	for _, path := range []string{"/v1/billing/rate-entries", "/v1/billing/rate-entries?customerId=" + w.partyY,
		"/v1/billing/fuel-adjustments?customerId=" + w.partyY, "/v1/billing/service-fees", "/v1/billing/standby-rates?customerId=" + w.partyY,
		"/v1/billing/statements"} {
		r := w.get(path, tokA)
		expect(t, r, http.StatusOK, "")
		if len(r.list()) != 0 {
			t.Errorf("%s as carrier A: %s", path, r.raw)
		}
	}
	expect(t, w.get("/v1/billing/statements/"+id, tokA), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/billing/statements/"+id+"/lines", tokA), http.StatusNotFound, "not_found")
	// The carrier bills nothing itself until the owner decides carrier self-billing (main spec §19 Q13): its statement
	// and its rate card are refused before they number a month or reach the pricing tables.
	own := w.trip(w.A, w.partyY, "A-2", bkk(2026, 10, 6, 9, 0), nil, ptr("PRIMARY"))
	w.snapshot(own, w.A, ptr(99.0), false)
	r = w.statement("ta.a", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, r, http.StatusForbidden, "permission_denied")
	if r.detail("reason") != billing.ReasonOwnFleetOnly {
		t.Fatalf("carrier statement %s", r.raw)
	}
	if n := scan[int](w, `SELECT count(*) FROM billing_statements WHERE tenant_id = $1`, w.A); n != 0 {
		t.Fatalf("%d statements of A", n)
	}
	if n := scan[int](w, `SELECT last_seq FROM billing_counters WHERE billing_party_id = $1`, w.partyY); n != 1 {
		t.Fatalf("O's counter moved: %d", n)
	}
	r = w.post("/v1/billing/rate-entries/import", tokA, map[string]any{"customerId": w.partyY, "effectiveFromDateStr": "2026-10-01",
		"rows": []map[string]any{rateRow("HUBZ", "SOCE", "4WJ", 1, "")}})
	expect(t, r, http.StatusForbidden, "permission_denied")
	if l := w.get("/v1/billing/rate-entries?customerId="+w.partyY, tokO).list(); len(l) != 1 || str(l[0]["tenantId"]) != w.O {
		t.Fatalf("O's rows %v", l)
	}
	if l := w.get("/v1/billing/rate-entries", w.login("ta.c")).list(); len(l) != 0 {
		t.Fatalf("C's reach %v", l)
	}
	expect(t, w.post("/v1/billing/rate-entries/"+str(w.get("/v1/billing/rate-entries?customerId="+w.partyY, tokO).list()[0]["id"])+"/void",
		w.login("ta.c"), map[string]any{"reason": "x"}), http.StatusForbidden, "permission_denied")
}

// Status machine, the period lock read FOR SHARE and the 409 of a money write into a sent or paid period.
func TestStatementStatusAndPeriodLock(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tok := w.login("ta.o")
	party := uuid.MustParse(w.partyY)
	for i := range 3 {
		id := w.trip(w.O, w.partyY, fmt.Sprintf("L-%d", i), bkk(2026, time.Month(10+i), 3, 9, 0), nil, ptr("PRIMARY"))
		w.snapshot(id, w.O, ptr(100.0), false)
	}
	lock := func(instants ...time.Time) (compute.LockedPeriod, bool) {
		t.Helper()
		var l compute.LockedPeriod
		var ok bool
		if err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
			var err error
			l, ok, err = billing.PeriodLock(ctx, tx, party, instants...)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return l, ok
	}
	oct := bkk(2026, 10, 15, 0, 0)
	s := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, s, http.StatusCreated, "")
	id := str(s.data()["id"])
	if _, ok := lock(oct); ok {
		t.Fatal("a draft locks its period")
	}
	expect(t, w.post("/v1/billing/statements/"+id+"/status", w.login("us.o"), map[string]any{"status": "sent"}), http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/billing/statements/"+id+"/status", tok, map[string]any{"status": "paid"}), http.StatusConflict, billing.CodeFailedPrecondition)
	expect(t, w.post("/v1/billing/statements/"+id+"/status", tok, map[string]any{"status": "draft"}), http.StatusUnprocessableEntity, "invalid_argument")
	r := w.post("/v1/billing/statements/"+id+"/status", tok, map[string]any{"status": "sent"})
	expect(t, r, http.StatusOK, "")
	if str(r.data()["status"]) != "sent" || str(r.data()["sentAt"]) != now.Format(time.RFC3339) {
		t.Fatalf("sent %s", r.raw)
	}
	l, ok := lock(time.Time{}, oct)
	if !ok || l.InvoiceNumber != "TTP-202610-001" || l.Year != 2026 || l.Month != 10 {
		t.Fatalf("lock %+v %v", l, ok)
	}
	if e := billing.ErrPeriodLocked(l); e.Code != billing.CodeBillingPeriodLocked || e.Details["blockedInvoiceNumber"] != "TTP-202610-001" {
		t.Fatalf("error %+v", e)
	}
	if _, ok := lock(bkk(2026, 11, 1, 0, 0), time.Time{}); ok {
		t.Fatal("November is not locked")
	}
	// The lock month is the Bangkok month: 2026-10-31T17:00Z is November.
	if _, ok := lock(time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC)); ok {
		t.Fatal("a November instant locked by October")
	}
	if l, ok := lock(time.Date(2026, 10, 31, 16, 59, 0, 0, time.UTC)); !ok || l.Month != 10 {
		t.Fatal("an October instant not locked")
	}
	expect(t, w.del("/v1/billing/statements/"+id, tok), http.StatusConflict, billing.CodeStatementNotDraft)
	r = w.post("/v1/billing/statements/"+id+"/status", tok, map[string]any{"status": "paid"})
	expect(t, r, http.StatusOK, "")
	if str(r.data()["paidAt"]) != now.Format(time.RFC3339) {
		t.Fatalf("paid %s", r.raw)
	}
	expect(t, w.post("/v1/billing/statements/"+id+"/status", tok, map[string]any{"status": "cancelled"}), http.StatusConflict, billing.CodeFailedPrecondition)
	if n := scan[int](w, `SELECT count(*) FROM outbox_events WHERE routing_key = 'statement.status_changed' AND aggregate_id = $1`, id); n != 2 {
		t.Fatalf("status events %d", n)
	}
	// Several locking statements: the latest generated_at supplies the number; a cancelled one never locks.
	s2 := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	id2 := str(s2.data()["id"])
	expect(t, w.post("/v1/billing/statements/"+id2+"/status", tok, map[string]any{"status": "sent"}), http.StatusOK, "")
	w.exec(`UPDATE billing_statements SET generated_at = generated_at + interval '1 minute' WHERE id = $1`, id2)
	if l, _ := lock(oct); l.InvoiceNumber != "TTP-202610-002" {
		t.Fatalf("latest lock %+v", l)
	}
	s3 := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 12})
	id3 := str(s3.data()["id"])
	expect(t, w.post("/v1/billing/statements/"+id3+"/status", tok, map[string]any{"status": "cancelled"}), http.StatusOK, "")
	if _, ok := lock(bkk(2026, 12, 2, 0, 0)); ok {
		t.Fatal("a cancelled statement locks")
	}

	// A period read FOR SHARE by a pricing transaction cannot turn sent until that transaction ends.
	nov := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 11})
	expect(t, nov, http.StatusCreated, "")
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // a failed assertion must not leave the connection holding its locks
	if _, err := tx.Exec(ctx, `SELECT set_config('app.bypass_tenant', 'on', true)`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := billing.PeriodLock(ctx, tx, party, bkk(2026, 11, 5, 0, 0)); err != nil || ok {
		t.Fatalf("November before: %v %v", ok, err)
	}
	done := make(chan resp, 1)
	go func() {
		done <- w.post("/v1/billing/statements/"+str(nov.data()["id"])+"/status", tok, map[string]any{"status": "sent"})
	}()
	select {
	case r := <-done:
		t.Fatalf("the status change did not wait for the pricing transaction: %d %s", r.status, r.raw)
	case <-time.After(400 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expect(t, <-done, http.StatusOK, "")
	if l, ok := lock(bkk(2026, 11, 5, 0, 0)); !ok || l.InvoiceNumber != "TTP-202611-001" {
		t.Fatalf("November after: %+v", l)
	}

	// The list: newest first, filters, keyset paging; a party filter naming nothing is an empty list.
	all := w.get("/v1/billing/statements?limit=2", tok)
	expect(t, all, http.StatusOK, "")
	if len(all.list()) != 2 || all.next() == "" {
		t.Fatalf("page 1 %s", all.raw)
	}
	rest := w.get("/v1/billing/statements?limit=2&cursor="+all.next(), tok)
	if len(rest.list()) != 2 || rest.next() != "" {
		t.Fatalf("page 2 %s", rest.raw)
	}
	if l := w.get("/v1/billing/statements?status=sent&year=2026&month=10&customerId="+w.partyY, tok).list(); len(l) != 1 || str(l[0]["id"]) != id2 {
		t.Fatalf("filtered %v", l)
	}
	if l := w.get("/v1/billing/statements?customerId="+uuid.NewString(), tok).list(); len(l) != 0 {
		t.Fatalf("unknown party %v", l)
	}
	expect(t, w.get("/v1/billing/statements?status=lost", tok), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/billing/statements?page=2", tok), http.StatusBadRequest, "bad_request")
}

func TestServiceFeesAndStandbyRates(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tok := w.login("ta.o")
	fee := w.post("/v1/billing/service-fees", tok, map[string]any{"customerId": w.partyX, "feeType": "extra_stop",
		"amountThb": 300, "unit": "per_stop", "note": " drop "})
	expect(t, fee, http.StatusCreated, "")
	if str(fee.data()["note"]) != "drop" || num(fee.data()["amountThb"]) != 300 {
		t.Fatalf("fee %s", fee.raw)
	}
	dup := w.post("/v1/billing/service-fees", tok, map[string]any{"customerId": w.partyX, "feeType": "extra_stop",
		"amountThb": 1, "unit": "per_trip"})
	expect(t, dup, http.StatusConflict, billing.CodeAlreadyExists)
	if dup.detail("field") != "feeType" {
		t.Fatalf("duplicate %s", dup.raw)
	}
	bad := w.post("/v1/billing/service-fees", tok, map[string]any{"customerId": w.partyX, "feeType": "custom", "amountThb": -1, "unit": "per_day"})
	expect(t, bad, http.StatusUnprocessableEntity, "invalid_argument")
	if f := bad.fields(); f["customTypeName"] != "required" || f["amountThb"] != "out_of_range" || f["unit"] != "invalid" {
		t.Fatalf("custom %s", bad.raw)
	}
	for range 2 { // custom fees are not unique
		expect(t, w.post("/v1/billing/service-fees", tok, map[string]any{"customerId": w.partyX, "feeType": "custom",
			"customTypeName": "ค่าขึ้นลง", "amountThb": 50, "unit": "per_trip"}), http.StatusCreated, "")
	}
	fid := str(fee.data()["id"])
	p := w.patch("/v1/billing/service-fees/"+fid, tok, map[string]any{"amountThb": 350.25, "note": nil})
	expect(t, p, http.StatusOK, "")
	if num(p.data()["amountThb"]) != 350.25 || p.data()["note"] != nil || str(p.data()["feeType"]) != "extra_stop" {
		t.Fatalf("patched %s", p.raw)
	}
	expect(t, w.patch("/v1/billing/service-fees/"+fid, tok, map[string]any{"customerId": w.partyY}), http.StatusUnprocessableEntity, "invalid_argument")
	sbFee := w.post("/v1/billing/service-fees", tok, map[string]any{"customerId": w.partyX, "feeType": "standby", "amountThb": 450, "unit": "per_trip"})
	expect(t, sbFee, http.StatusCreated, "")
	if l := w.get("/v1/billing/service-fees?customerId="+w.partyX, tok).list(); len(l) != 4 {
		t.Fatalf("fees %v", l)
	}

	sb := w.post("/v1/billing/standby-rates", tok, map[string]any{"customerId": w.partyX, "rateThb": 600, "effectiveFromDateStr": "2026-08-09", "note": "งานหมด"})
	expect(t, sb, http.StatusCreated, "")
	if str(sb.data()["effectiveFrom"]) != "2026-08-08T17:00:00Z" || sb.data()["voided"] != false {
		t.Fatalf("standby rate %s", sb.raw)
	}
	sid := str(sb.data()["id"])
	p = w.patch("/v1/billing/standby-rates/"+sid, tok, map[string]any{"rateThb": 650, "effectiveFromDateStr": "2026-08-10"})
	expect(t, p, http.StatusOK, "")
	if num(p.data()["rateThb"]) != 650 || str(p.data()["effectiveFrom"]) != "2026-08-09T17:00:00Z" || str(p.data()["note"]) != "งานหมด" {
		t.Fatalf("patched standby %s", p.raw)
	}
	var tables billing.PricingTables
	load := func() {
		t.Helper()
		if err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
			var err error
			tables, err = billing.LoadPricingTables(ctx, tx, uuid.MustParse(w.partyX), uuid.MustParse(w.O))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	load()
	if tables.ExtraStopFeeTHB == nil || *tables.ExtraStopFeeTHB != 350.25 || tables.StandbyFeeTHB == nil || *tables.StandbyFeeTHB != 450 ||
		len(tables.StandbyRates) != 1 || tables.StandbyRates[0].RateTHB != 650 || tables.Carrier != uuid.MustParse(w.O) {
		t.Fatalf("pricing tables %+v", tables)
	}
	v := w.post("/v1/billing/standby-rates/"+sid+"/void", tok, map[string]any{"reason": "replaced"})
	expect(t, v, http.StatusOK, "")
	if v.data()["voided"] != true || str(v.data()["voidedBy"]) != w.userTAO {
		t.Fatalf("void %s", v.raw)
	}
	again := w.post("/v1/billing/standby-rates/"+sid+"/void", tok, map[string]any{"reason": "again"})
	expect(t, again, http.StatusConflict, billing.CodeFailedPrecondition)
	expect(t, w.patch("/v1/billing/standby-rates/"+sid, tok, map[string]any{"rateThb": 1}), http.StatusConflict, billing.CodeFailedPrecondition)
	if l := w.get("/v1/billing/standby-rates?customerId="+w.partyX, tok).list(); len(l) != 0 {
		t.Fatalf("voided standby rate listed: %v", l)
	}
	if l := w.get("/v1/billing/standby-rates?includeVoided=true", tok).list(); len(l) != 1 {
		t.Fatalf("includeVoided %v", l)
	}
	load()
	if len(tables.StandbyRates) != 0 {
		t.Fatal("a voided standby rate still prices")
	}
	expect(t, w.del("/v1/billing/service-fees/"+fid, tok), http.StatusNoContent, "")
	expect(t, w.del("/v1/billing/service-fees/"+fid, tok), http.StatusNotFound, "not_found")
	load()
	if tables.ExtraStopFeeTHB != nil {
		t.Fatal("a deleted fee still prices")
	}
	// Every write emitted ratecard.changed for its table on the billing tenant's topic.
	rows, err := w.etl.Query(ctx, `SELECT payload->>'table', count(*) FROM outbox_events WHERE routing_key = 'ratecard.changed'
		AND payload->>'billingPartyId' = $1 GROUP BY 1`, w.partyX)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		counts[k] = n
	}
	rows.Close()
	if counts["serviceFees"] != 6 || counts["standby"] != 3 {
		t.Fatalf("ratecard.changed per table %v", counts)
	}
	// ratecard.changed names the customer the web keys ['rateCard', table, customerId] by, next to the party.
	if n := scan[int](w, `SELECT count(*) FROM outbox_events WHERE routing_key = 'ratecard.changed' AND payload->>'billingPartyId' = $1
		AND payload->>'customerId' IS DISTINCT FROM $2`, w.partyX, w.customerX); n != 0 {
		t.Fatalf("%d ratecard.changed rows without the customer id", n)
	}
	for _, path := range []string{"/v1/billing/service-fees?customerId=" + w.customerX, "/v1/billing/standby-rates?includeVoided=true"} {
		for _, row := range w.get(path, tok).list() {
			if str(row["customerId"]) != w.customerX || str(row["billingPartyId"]) != w.partyX {
				t.Fatalf("%s: %v", path, row)
			}
		}
	}
}

// The rate-card bundle (cache:ratecard:{partyId}) serves the lists of one party; a write drops it; paging matches
// the PostgreSQL path.
func TestRateCardListsAndBundleCache(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tok := w.login("ta.o")
	var rows []map[string]any
	for i := range 5 {
		rows = append(rows, rateRow(fmt.Sprintf("HUB%d", i), "SOCE", "4WJ", float64(100+i), ""))
	}
	w.importRows("ta.o", w.partyY, "2026-09-01", rows[:3]...)
	w.importRows("ta.o", w.partyY, "2026-10-01", rows[3:]...)
	pages := func(base string) []string {
		t.Helper()
		var ids []string
		cursor := ""
		for range 10 {
			r := w.get(base+"&limit=2&cursor="+cursor, tok)
			expect(t, r, http.StatusOK, "")
			for _, e := range r.list() {
				ids = append(ids, str(e["id"]))
			}
			if cursor = r.next(); cursor == "" {
				break
			}
		}
		return ids
	}
	viaBundle := pages("/v1/billing/rate-entries?customerId=" + w.partyY)
	viaSQL := pages("/v1/billing/rate-entries?includeVoided=false")
	if len(viaBundle) != 5 || !slices.Equal(viaBundle, viaSQL) {
		t.Fatalf("bundle pages %v, SQL pages %v", viaBundle, viaSQL)
	}
	key := w.ks.RateCard(w.partyY)
	if w.rdb.Exists(ctx, key).Val() != 1 {
		t.Fatal("the bundle was not cached")
	}
	first := w.get("/v1/billing/rate-entries?customerId="+w.partyY+"&limit=1", tok).list()[0]
	if num(first["rateThb"]) != 104 && num(first["rateThb"]) != 103 {
		t.Fatalf("newest effective date first: %v", first)
	}
	w.importRows("ta.o", w.partyY, "2026-11-01", rateRow("HUBN", "SOCE", "4WJ", 1, ""))
	if w.rdb.Exists(ctx, key).Val() != 0 {
		t.Fatal("ratecard.changed did not drop the bundle")
	}
	if l := w.get("/v1/billing/rate-entries?customerId="+w.partyY+"&limit=500", tok).list(); len(l) != 6 || str(l[0]["hubId"]) != "HUBN" {
		t.Fatalf("after the import %v", l)
	}
	expect(t, w.get("/v1/billing/rate-entries?cursor=nope", tok), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/billing/rate-entries?includeVoided=yes", tok), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/billing/rate-entries?limit=501", tok), http.StatusUnprocessableEntity, "invalid_argument")
	if l := w.get("/v1/billing/rate-entries?customerId="+uuid.NewString(), tok).list(); len(l) != 0 {
		t.Fatalf("unknown party %v", l)
	}
}

// --- Bangchak ---------------------------------------------------------------------------------------------

var prices = bangchak.Prices{Locale: "th", FetchedAt: now, Source: bangchak.Source, Items: []bangchak.Item{
	{NameTh: "ไฮดีเซล S", NameEn: "Hi Diesel S", Price: 32.94, Unit: bangchak.DefaultUnit},
	{NameTh: "แก๊สโซฮอล์ 95", NameEn: "Gasohol 95", Price: 35.05, Unit: bangchak.DefaultUnit},
}}

func (w *world) fuelJob(feed bangchak.Client, at time.Time) *billing.FuelJob {
	w.t.Helper()
	j, err := billing.NewFuelJob(billing.FuelJobDeps{Pool: w.pool, Bangchak: feed, Locks: jobs.NewRedisLocker(w.rdb, w.ks),
		Now: func() time.Time { return at }, Log: zerolog.Nop()})
	if err != nil {
		w.t.Fatal(err)
	}
	return j
}

// run delivers the lt.jobs command of a job to the consumer.
func (w *world) run(j *billing.FuelJob, jobID string) {
	w.t.Helper()
	body := scan[[]byte](w, `SELECT payload::text::bytea FROM outbox_events WHERE exchange = 'lt.jobs'
		AND routing_key = 'job.bangchak.snapshot' AND aggregate_id = $1`, jobID)
	reg := j.Registration()
	if reg.Queue != "bangchak.snapshot" {
		w.t.Fatal(reg.Queue)
	}
	if err := reg.Handler(context.Background(), &mq.Delivery{Queue: reg.Queue, RoutingKey: billing.RouteBangchakJob, Body: body}); err != nil {
		w.t.Fatal(err)
	}
}

// submit posts the job as ta.o and returns its id.
func (w *world) submit() string {
	w.t.Helper()
	r := w.post("/v1/jobs/bangchak.snapshot", w.login("ta.o"), nil)
	expect(w.t, r, http.StatusAccepted, "")
	return str(r.data()["jobId"])
}

func (w *world) monthly() (string, string) {
	w.t.Helper()
	return scan[string](w, `SELECT status FROM fuel_monthly_snapshots WHERE month_key = '2026-10'`),
		scan[string](w, `SELECT items::text FROM fuel_monthly_snapshots WHERE month_key = '2026-10'`)
}

// AC: a failed Bangchak fetch leaves the prior monthly row intact (fixes persistFuelMonthlySnapshot.ts:88-96); the daily
// row is insert-only.
func TestBangchakSnapshotJob(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	feed := &fakeFeed{prices: prices}
	morning := bkk(2026, 10, 11, 5, 0)

	id := w.submit()
	held := w.post("/v1/jobs/bangchak.snapshot", tok, nil)
	expect(t, held, http.StatusConflict, "already_exists")
	if held.detail("jobId") != id {
		t.Fatalf("held lock %s", held.raw)
	}
	w.run(w.fuelJob(feed, morning), id)
	j := w.get("/v1/jobs/"+id, tok)
	expect(t, j, http.StatusOK, "")
	res, _ := j.data()["result"].(map[string]any)
	if str(j.data()["status"]) != jobs.StatusSucceeded || str(res["monthKey"]) != "2026-10" || str(res["dayKey"]) != "2026-10-11" ||
		num(res["itemCount"]) != 2 || res["dailyInserted"] != true {
		t.Fatalf("job %s", j.raw)
	}
	status, before := w.monthly()
	if status != "ok" || !strings.Contains(before, "32.94") {
		t.Fatalf("monthly %s %s", status, before)
	}
	// The lock is released with the job: a second run may start.
	id2 := w.submit()
	feed.set(bangchak.Prices{Locale: "th", FetchedAt: now, Source: bangchak.Source, Items: []bangchak.Item{
		{NameTh: "ไฮดีเซล S", NameEn: "Hi Diesel S", Price: 31.94, Unit: bangchak.DefaultUnit}}}, nil)
	w.run(w.fuelJob(feed, morning.Add(time.Hour)), id2)
	if _, after := w.monthly(); !strings.Contains(after, "31.94") || strings.Contains(after, "32.94") {
		t.Fatalf("monthly not replaced by a successful run: %s", after)
	}
	if daily := scan[string](w, `SELECT items::text FROM fuel_daily_snapshots WHERE day_key = '2026-10-11'`); !strings.Contains(daily, "32.94") {
		t.Fatalf("the day's first prices were overwritten: %s", daily)
	}
	_, good := w.monthly()

	// Failures: the fetch, an empty response, the feed off. Each ends the job failed and writes no snapshot.
	for name, tc := range map[string]struct {
		feed  bangchak.Client
		cause string
	}{
		"fetch":    {&fakeFeed{err: errors.New("Bangchak API HTTP 503")}, "Bangchak API HTTP 503"},
		"empty":    {&fakeFeed{prices: bangchak.Prices{Source: bangchak.Source}}, billing.ErrTextBangchakNoItems},
		"off":      {nil, billing.ErrTextBangchakDisabled},
		"next day": {&fakeFeed{err: errors.New("timeout")}, "timeout"},
	} {
		at := morning.Add(2 * time.Hour)
		if name == "next day" {
			at = bkk(2026, 10, 12, 5, 0)
		}
		id := w.submit()
		w.run(w.fuelJob(tc.feed, at), id)
		j := w.get("/v1/jobs/"+id, tok)
		if str(j.data()["status"]) != jobs.StatusFailed || str(j.data()["error"]) != tc.cause {
			t.Fatalf("%s: job %s", name, j.raw)
		}
		if status, after := w.monthly(); status != "ok" || after != good {
			t.Fatalf("%s: the monthly row changed after a failed run: %s %s", name, status, after)
		}
	}
	if n := scan[int](w, `SELECT count(*) FROM fuel_daily_snapshots`); n != 1 {
		t.Fatalf("daily rows %d", n)
	}
	// A legacy error row of another month is never rewritten by a failure either, and is replaced by a success.
	w.exec(`INSERT INTO fuel_monthly_snapshots (month_key, captured_at, source, status, error_message) VALUES ('2026-09', now(), 'bangchak_api', 'error', 'x')`)

	// Reads (accounting:view_fuel).
	daily := w.get("/v1/fuel/daily?from=2026-10-01", tok)
	expect(t, daily, http.StatusOK, "")
	if l := daily.list(); len(l) != 1 || str(l[0]["dayKey"]) != "2026-10-11" || str(l[0]["monthKey"]) != "2026-10" ||
		len(l[0]["items"].([]any)) != 2 {
		t.Fatalf("daily %s", daily.raw)
	}
	if l := w.get("/v1/fuel/daily?from=2026-10-12", tok).list(); len(l) != 0 {
		t.Fatalf("from %v", l)
	}
	expect(t, w.get("/v1/fuel/daily/2026-10-11", tok), http.StatusOK, "")
	expect(t, w.get("/v1/fuel/daily/2026-10-12", tok), http.StatusNotFound, "not_found")
	monthly := w.get("/v1/fuel/monthly?limit=1", tok)
	expect(t, monthly, http.StatusOK, "")
	if l := monthly.list(); len(l) != 1 || str(l[0]["monthKey"]) != "2026-10" || monthly.next() == "" {
		t.Fatalf("monthly %s", monthly.raw)
	}
	more := w.get("/v1/fuel/monthly?limit=1&cursor="+monthly.next(), tok)
	if l := more.list(); len(l) != 1 || str(l[0]["status"]) != "error" || str(l[0]["errorMessage"]) != "x" {
		t.Fatalf("monthly page 2 %s", more.raw)
	}
	expect(t, w.get("/v1/fuel/daily", w.login("us.o")), http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/jobs/bangchak.snapshot", w.login("us.o"), nil), http.StatusForbidden, "permission_denied")
	// The snapshots are platform-wide and the daily row is insert-only: a carrier's tenant_admin, who holds
	// accounting:edit_rate_card for its own tenant, may not capture a day (steward rule, R60).
	for _, user := range []string{"ta.a", "ta.c"} {
		expect(t, w.post("/v1/jobs/bangchak.snapshot", w.login(user), nil), http.StatusForbidden, "permission_denied")
	}
	// Tenant staff holding accounting:edit_rate_card read the job (jobs.TypeCapabilities); another tenant's do not.
	expect(t, w.get("/v1/jobs/"+id, w.login("ta.a")), http.StatusNotFound, "not_found")
}

func TestRetailPrices(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	w.feed.set(prices, nil)
	r := w.get("/v1/fuel/retail?locale=en", tok)
	expect(t, r, http.StatusOK, "")
	if str(r.data()["locale"]) != "en" || str(r.data()["source"]) != bangchak.Source || len(r.data()["items"].([]any)) != 2 {
		t.Fatalf("retail %s", r.raw)
	}
	expect(t, w.get("/v1/fuel/retail?locale=en", tok), http.StatusOK, "")
	if n := w.feed.count(); n != 1 {
		t.Fatalf("cache:fuel:retail:en did not serve the second read: %d fetches", n)
	}
	if ttl := w.rdb.PTTL(context.Background(), w.ks.FuelRetail("en")).Val(); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("retail TTL %v", ttl)
	}
	w.feed.set(bangchak.Prices{}, errors.New("down"))
	fail := w.get("/v1/fuel/retail", tok) // locale defaults to th
	expect(t, fail, http.StatusServiceUnavailable, "unavailable")
	if fail.detail("reason") != "bangchak_unavailable" || w.rdb.Exists(context.Background(), w.ks.FuelRetail("th")).Val() != 0 {
		t.Fatalf("failure %s", fail.raw)
	}
	expect(t, w.get("/v1/fuel/retail?locale=jp", tok), http.StatusUnprocessableEntity, "invalid_argument")
	off, err := billing.New(billing.Deps{Pool: w.pool, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := off.RetailPrices(context.Background(), "th"); err == nil {
		t.Fatal("the feed off answered")
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
