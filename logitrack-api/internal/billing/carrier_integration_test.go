//go:build integration

package billing_test

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// Regression (T37 review): every billing write comes from the own fleet until the owner decides carrier self-billing
// (main spec §19 Q13, R61). A carrier's tenant_admin holds accounting:edit_rate_card for its own tenant and billing
// parties are a global master, so without the check carrier C (no contractor link) could import a 1 THB rate, a -90 %
// fuel round or take the own fleet's extra_stop slot of customer Y, and number Y's month first.
func TestBillingWritesAreOwnFleetOnly(t *testing.T) {
	w := newWorld(t)
	tokO := w.login("ta.o")
	w.importRows("ta.o", w.partyY, "2026-10-01", rateRow("HUBA", "SOCE", "4WJ", 1200, ""))
	rate := str(w.get("/v1/billing/rate-entries?customerId="+w.partyY, tokO).list()[0]["id"])
	fuel := w.post("/v1/billing/fuel-adjustments", tokO, map[string]any{"customerId": w.partyY, "effectiveFromDateStr": "2026-10-01",
		"mode": "percent", "pct": 5})
	expect(t, fuel, http.StatusCreated, "")
	fee := w.post("/v1/billing/service-fees", tokO, map[string]any{"customerId": w.partyY, "feeType": "waiting_time",
		"amountThb": 100, "unit": "per_trip"})
	expect(t, fee, http.StatusCreated, "")
	sb := w.post("/v1/billing/standby-rates", tokO, map[string]any{"customerId": w.partyY, "rateThb": 500, "effectiveFromDateStr": "2026-10-01"})
	expect(t, sb, http.StatusCreated, "")
	trip := w.trip(w.O, w.partyY, "O-1", bkk(2026, 10, 5, 9, 0), nil, ptr("PRIMARY"))
	w.snapshot(trip, w.O, ptr(1200.0), false)
	stmt := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, stmt, http.StatusCreated, "")

	type call struct {
		method, path string
		body         any
	}
	calls := []call{
		{http.MethodPost, "/v1/billing/rate-entries/import", map[string]any{"customerId": w.partyY, "effectiveFromDateStr": "2026-10-05",
			"rows": []map[string]any{rateRow("HUBA", "SOCE", "4WJ", 1, "")}}},
		{http.MethodPost, "/v1/billing/rate-entries", map[string]any{"customerId": w.partyY, "effectiveFromDateStr": "2026-10-05",
			"hubId": "HUBA", "destinationCode": "SOCE", "vehicleClass": "4WJ", "rateThb": 1, "jobCategory": ""}},
		{http.MethodPost, "/v1/billing/rate-entries/" + rate + "/void", map[string]any{"reason": "x"}},
		{http.MethodPost, "/v1/billing/fuel-adjustments", map[string]any{"customerId": w.partyY, "effectiveFromDateStr": "2026-10-05",
			"mode": "percent", "pct": -90}},
		{http.MethodPost, "/v1/billing/fuel-adjustments/" + str(fuel.data()["id"]) + "/void", map[string]any{"reason": "x"}},
		{http.MethodPost, "/v1/billing/service-fees", map[string]any{"customerId": w.partyY, "feeType": "extra_stop", "amountThb": 1,
			"unit": "per_stop"}},
		{http.MethodPatch, "/v1/billing/service-fees/" + str(fee.data()["id"]), map[string]any{"amountThb": 1}},
		{http.MethodDelete, "/v1/billing/service-fees/" + str(fee.data()["id"]), nil},
		{http.MethodPost, "/v1/billing/standby-rates", map[string]any{"customerId": w.partyY, "rateThb": 1, "effectiveFromDateStr": "2026-10-05"}},
		{http.MethodPatch, "/v1/billing/standby-rates/" + str(sb.data()["id"]), map[string]any{"rateThb": 1}},
		{http.MethodPost, "/v1/billing/standby-rates/" + str(sb.data()["id"]) + "/void", map[string]any{"reason": "x"}},
		{http.MethodPost, "/v1/billing/statements", map[string]any{"customerId": w.partyY, "year": 2026, "month": 11}},
		{http.MethodPost, "/v1/billing/statements/" + str(stmt.data()["id"]) + "/status", map[string]any{"status": "sent"}},
		{http.MethodDelete, "/v1/billing/statements/" + str(stmt.data()["id"]), nil},
	}
	refused := func(who string, r resp, c call) {
		t.Helper()
		if r.status != http.StatusForbidden || r.code() != "permission_denied" || r.detail("reason") != billing.ReasonOwnFleetOnly {
			t.Errorf("%s %s %s: %d %s", who, c.method, c.path, r.status, r.raw)
		}
	}
	// Carrier A works for the own fleet (contractor reach), C does not; neither bills.
	for _, user := range []string{"ta.a", "ta.c"} {
		for _, c := range calls {
			refused(user, w.do(c.method, c.path, w.login(user), c.body), c)
		}
	}
	// Platform authority cannot supply membership or tenant-write authority through a header.
	pa := w.login("pa")
	for _, target := range []string{w.C, w.O, "*"} {
		expect(t, w.post("/v1/billing/standby-rates", pa, calls[8].body, "X-Act-On-Tenant", target), http.StatusBadRequest, "header_not_allowed")
	}
	expect(t, w.post("/v1/billing/standby-rates", pa, calls[8].body), http.StatusForbidden, "permission_denied")
	for _, tbl := range []string{"customer_rate_entries", "customer_fuel_rate_adjustments", "customer_service_fees",
		"standby_rate_entries", "billing_statements", "billing_counters"} {
		if n := scan[int](w, `SELECT count(*) FROM `+tbl+` WHERE tenant_id <> $1`, w.O); n != 0 {
			t.Errorf("%s: %d rows of a carrier", tbl, n)
		}
	}
	if got := scan[string](w, `SELECT status FROM billing_statements WHERE id = $1`, str(stmt.data()["id"])); got != "draft" {
		t.Fatalf("O's statement moved to %s", got)
	}
	// Nobody took the own fleet's fee slot of Y (customer_service_fees_one_per_type is per party and type).
	expect(t, w.post("/v1/billing/service-fees", tokO, calls[5].body), http.StatusCreated, "")
}

// Regression (T37 review): the pricing read is per billing carrier. Rows of another tenant that bypassed the API (an
// ETL row, a later self-billing decision) never enter the own fleet's selection, so a newer 1 THB card, a -90 % round
// or a 1 THB fee of carrier C can neither price an own-fleet trip nor move its snapshot to C.
func TestPricingTablesReadOneCarrier(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tokO := w.login("ta.o")
	w.importRows("ta.o", w.partyY, "2026-10-01", rateRow("HUBA", "SOCE", "4WJ", 1200, ""))
	expect(t, w.post("/v1/billing/fuel-adjustments", tokO, map[string]any{"customerId": w.partyY, "effectiveFromDateStr": "2026-10-01",
		"mode": "percent", "pct": 5}), http.StatusCreated, "")
	expect(t, w.post("/v1/billing/service-fees", tokO, map[string]any{"customerId": w.partyY, "feeType": "extra_stop",
		"amountThb": 300, "unit": "per_stop"}), http.StatusCreated, "")
	// Carrier C's rows of the same party, newer, loaded around the API.
	w.exec(`INSERT INTO customer_rate_entries (tenant_id, billing_party_id, import_id, hub_code, destination_code, vehicle_class,
		rate_thb, effective_from_date, effective_from_at) VALUES ($1, $2, 'rc_9', 'HUBA', 'SOCE', '4WJ', 1, '2026-10-05',
		bkk_midnight('2026-10-05'))`, w.C, w.partyY)
	w.exec(`INSERT INTO customer_fuel_rate_adjustments (tenant_id, billing_party_id, effective_from_date, effective_from_at,
		rate_multiplier) VALUES ($1, $2, '2026-10-05', bkk_midnight('2026-10-05'), 0.1)`, w.C, w.partyY)
	w.standbyRate(w.C, w.partyY, "2026-10-05", 1)
	w.exec(`INSERT INTO customer_service_fees (tenant_id, billing_party_id, fee_type, amount_thb, unit)
		VALUES ($1, $2, 'standby', 1, 'per_trip')`, w.C, w.partyY)

	load := func(carrier string) billing.PricingTables {
		t.Helper()
		var tables billing.PricingTables
		if err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
			var err error
			tables, err = billing.LoadPricingTables(ctx, tx, uuid.MustParse(w.partyY), uuid.MustParse(carrier))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return tables
	}
	o := load(w.O)
	if len(o.Rates) != 1 || o.Rates[0].RateTHB != 1200 || len(o.Fuel) != 1 || o.Fuel[0].RateMultiplier != 1.05 ||
		len(o.StandbyRates) != 0 || o.ExtraStopFeeTHB == nil || *o.ExtraStopFeeTHB != 300 || o.StandbyFeeTHB != nil ||
		o.Carrier != uuid.MustParse(w.O) {
		t.Fatalf("own fleet's tables %+v", o)
	}
	bill := bkk(2026, 10, 10, 9, 0)
	if e, ok := compute.SelectRateEntry(w.partyY, "HUBA", "SOCE", "4WJ", bill, o.Rates, compute.Primary); !ok || e.RateTHB != 1200 {
		t.Fatalf("selected %+v", e)
	}
	if a, ok := compute.SelectFuelAdjustment(w.partyY, bill, o.Fuel); !ok || a.RateMultiplier != 1.05 {
		t.Fatalf("fuel %+v", a)
	}
	c := load(w.C) // the filter is the carrier, not the own fleet
	if len(c.Rates) != 1 || c.Rates[0].RateTHB != 1 || len(c.StandbyRates) != 1 || c.ExtraStopFeeTHB != nil ||
		c.StandbyFeeTHB == nil || *c.StandbyFeeTHB != 1 {
		t.Fatalf("carrier C's tables %+v", c)
	}
}

// Regression (T37 review): a period read by a pricing transaction cannot become sent while the price commits, even
// through a statement created after the read (the FOR SHARE rows cover only the statements that existed).
func TestPeriodLockCoversStatementsCreatedLater(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tok := w.login("ta.o")
	party := uuid.MustParse(w.partyY)
	trip := w.trip(w.O, w.partyY, "D-1", bkk(2026, 12, 3, 9, 0), nil, ptr("PRIMARY"))
	w.snapshot(trip, w.O, ptr(100.0), false)

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.bypass_tenant', 'on', true)`); err != nil {
		t.Fatal(err)
	}
	// The forced reprice of a trip moving from November to December: both periods, December not invoiced yet.
	if _, ok, err := billing.PeriodLock(ctx, tx, party, bkk(2026, 11, 20, 0, 0), bkk(2026, 12, 3, 9, 0)); err != nil || ok {
		t.Fatalf("before: %v %v", ok, err)
	}
	// A draft does not lock a period: it is created while the pricing transaction is open.
	s := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 12})
	expect(t, s, http.StatusCreated, "")
	done := make(chan resp, 1)
	go func() {
		done <- w.post("/v1/billing/statements/"+str(s.data()["id"])+"/status", tok, map[string]any{"status": "sent"})
	}()
	select {
	case r := <-done:
		t.Fatalf("the new statement turned sent while the price was open: %d %s", r.status, r.raw)
	case <-time.After(400 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expect(t, <-done, http.StatusOK, "")
	// Other periods and parties are not held: a November statement of another party turns sent at once.
	other := w.trip(w.O, w.partyX, "X-1", bkk(2026, 11, 3, 9, 0), ptr(bkk(2026, 11, 3, 0, 0)), ptr("PRIMARY"))
	w.snapshot(other, w.O, ptr(100.0), false)
	tx2, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()
	if _, err := tx2.Exec(ctx, `SELECT set_config('app.bypass_tenant', 'on', true)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := billing.PeriodLock(ctx, tx2, party, bkk(2026, 11, 20, 0, 0)); err != nil {
		t.Fatal(err)
	}
	x := w.statement("ta.o", map[string]any{"customerId": w.partyX, "year": 2026, "month": 11})
	expect(t, x, http.StatusCreated, "")
	expect(t, w.post("/v1/billing/statements/"+str(x.data()["id"])+"/status", tok, map[string]any{"status": "sent"}), http.StatusOK, "")
}

// Regression (T37 review): the stored withholding rate and the withholding amount come from one read. A company rate
// committed while the statement waits on the counter lock (R66) used to be stored next to an amount computed at the
// old rate.
func TestStatementWithholdingReadOnce(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	for i, amount := range []float64{1160.0, 1000.0} {
		id := w.trip(w.O, w.partyY, fmt.Sprintf("H-%d", i), bkk(2026, 10, 2+i, 9, 0), nil, ptr("PRIMARY"))
		w.snapshot(id, w.O, &amount, false)
	}
	expect(t, w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10}), http.StatusCreated, "")

	hold, err := pgx.Connect(ctx, w.db.URL(db.RoleETL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Close(context.Background()) }()
	htx, err := hold.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = htx.Rollback(context.Background()) }()
	if _, err := htx.Exec(ctx, `SELECT last_seq FROM billing_counters WHERE billing_party_id = $1 AND period_year = 2026
		AND period_month = 10 FOR UPDATE`, w.partyY); err != nil {
		t.Fatal(err)
	}
	done := make(chan resp, 1)
	go func() { done <- w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10}) }()
	// Wait until the statement transaction waits on the counter row (it read the issuer at 3 % before).
	deadline := time.Now().Add(10 * time.Second)
	for scan[int](w, `SELECT count(*) FROM pg_locks WHERE NOT granted AND locktype IN ('transactionid', 'tuple')`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the statement never waited on the counter lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	w.exec(`UPDATE companies SET withholding_tax_rate = 1 WHERE id = $1`, w.companyO)
	if err := htx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	expect(t, r, http.StatusCreated, "")
	total, rate := num(r.data()["totalAmount"]), num(r.data()["withholdingTaxRate"])
	wht := compute.WithholdingTHB(total, rate)
	if total != 2160 || rate != 0.01 || num(r.data()["withholdingTax"]) != wht ||
		math.Abs(num(r.data()["netAmount"])-(total-wht)) > 1e-9 {
		t.Fatalf("statement %s: withholding %v, want %v at the stored rate", r.raw, r.data()["withholdingTax"], wht)
	}
}

// Regression (T37 review): a priced standby record is invoiced by the carrier stored with its price (0015), not by the
// current state of the fee or rate that priced it. A service fee deleted or re-typed later, or an ETL row whose rate
// entry is not linked yet, used to drop the record out of every invoice.
func TestStandbyInvoicedByItsRecordedCarrier(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	fee := w.post("/v1/billing/service-fees", tok, map[string]any{"customerId": w.partyY, "feeType": "standby", "amountThb": 450, "unit": "per_trip"})
	expect(t, fee, http.StatusCreated, "")
	w.standby(w.O, w.partyY, "", w.O, bkk(2026, 10, 10, 9, 0), 450)
	standby := map[string]any{"customerId": w.partyY, "year": 2026, "month": 10, "types": []string{"standby"}}
	total := func(want float64) {
		t.Helper()
		r := w.statement("ta.o", standby)
		expect(t, r, http.StatusCreated, "")
		if num(r.data()["totalAmount"]) != want {
			t.Fatalf("total %s, want %v", r.raw, want)
		}
	}
	total(450)
	expect(t, w.del("/v1/billing/service-fees/"+str(fee.data()["id"]), tok), http.StatusNoContent, "")
	total(450)
	fee = w.post("/v1/billing/service-fees", tok, map[string]any{"customerId": w.partyY, "feeType": "standby", "amountThb": 450, "unit": "per_trip"})
	expect(t, fee, http.StatusCreated, "")
	expect(t, w.patch("/v1/billing/service-fees/"+str(fee.data()["id"]), tok, map[string]any{"feeType": "waiting_time"}), http.StatusOK, "")
	total(450)
	// The ETL shape: priced from a standby rate whose row is not linked yet, no carrier recorded: the own fleet's.
	w.exec(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status,
		billing_estimate_thb, billing_party_id, billing_rate_source) VALUES ($1, 'driver', $2, $3, $3, 'completed', 300, $2, 'standby_rate')`,
		w.O, w.partyY, bkk(2026, 10, 11, 9, 0))
	total(750)
	// A record carrier A priced (A is in the own fleet's reach) is A's, never the own fleet's invoice: stored, or (no
	// carrier recorded) through the immutable rate entry it names, as operations.BillingVisible reads it.
	w.standby(w.A, w.partyY, "", w.A, bkk(2026, 10, 12, 9, 0), 999)
	w.standby(w.A, w.partyY, w.standbyRate(w.A, w.partyY, "2026-01-01", 888), "", bkk(2026, 10, 13, 9, 0), 888)
	total(750)
}

// Regression (T37 review): the invoice set is read under the caller's RLS, so a trip or standby record the own fleet
// priced but that runs in a tenant outside its reach (a carrier without a contractor link, the quarantine tenant) used
// to be left off the invoice silently. The statement is refused instead, and the counter does not move.
func TestStatementRefusesRowsOutsideReach(t *testing.T) {
	w := newWorld(t)
	prim := ptr("PRIMARY")
	quarantine := scan[string](w, `SELECT id::text FROM tenants WHERE kind = 'quarantine'`)
	outside := func(month int, want int) {
		t.Helper()
		r := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": month})
		expect(t, r, http.StatusConflict, billing.CodeFailedPrecondition)
		if r.detail("reason") != "rows_outside_reach" || num(r.detail("hiddenRows")) != float64(want) {
			t.Fatalf("month %d: %s", month, r.raw)
		}
		if n := scan[int](w, `SELECT count(*) FROM billing_counters WHERE billing_party_id = $1 AND period_month = $2`, w.partyY, month); n != 0 {
			t.Fatalf("month %d: the counter moved", month)
		}
	}
	// October: a trip of contractor A (in reach) and one of C (no contractor link), both priced by O.
	a := w.trip(w.A, w.partyY, "A-1", bkk(2026, 10, 5, 9, 0), nil, prim)
	w.snapshot(a, w.O, ptr(1000.0), false)
	c := w.trip(w.C, w.partyY, "C-1", bkk(2026, 10, 6, 9, 0), nil, prim)
	w.snapshot(c, w.O, ptr(2000.0), false)
	outside(10, 1)
	// November: a quarantined trip with an own-fleet snapshot.
	o := w.trip(w.O, w.partyY, "O-1", bkk(2026, 11, 5, 9, 0), nil, prim)
	w.snapshot(o, w.O, ptr(500.0), false)
	q := w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, job_category, billing_party_id,
		delivered_at, created_at) VALUES ($1, 'quarantine', 'Q-1', 'delivered', 'line_haul', 'PRIMARY', $2, $3, $3) RETURNING id::text`,
		quarantine, w.partyY, bkk(2026, 11, 6, 9, 0))
	w.snapshot(q, w.O, ptr(700.0), false)
	outside(11, 1)
	// December: a standby record of C that O priced; the trip toggle alone leaves it out of the set, so that passes.
	d := w.trip(w.O, w.partyY, "O-2", bkk(2026, 12, 5, 9, 0), nil, prim)
	w.snapshot(d, w.O, ptr(100.0), false)
	w.standby(w.C, w.partyY, "", w.O, bkk(2026, 12, 6, 9, 0), 300)
	outside(12, 1)
	r := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 12, "types": []string{"trip"}})
	expect(t, r, http.StatusCreated, "")
	if num(r.data()["totalAmount"]) != 100 {
		t.Fatalf("December trips %s", r.raw)
	}
}

// 0015 backfills the carrier of every priced standby record: the carrier of the rate entry it names, else the own fleet
// (never through customer_service_fees, as operations.BillingVisible); unpriced rows stay NULL.
func TestStandbyBillingTenantBackfill(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	if _, err := r.UpTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.Connect(ctx, d.URL(db.RoleETL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	id := func(sql string, args ...any) string {
		t.Helper()
		var v string
		if err := c.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return v
	}
	if _, err := c.Exec(ctx, `SELECT set_config('app.bypass_tenant', 'on', false)`); err != nil {
		t.Fatal(err)
	}
	own := id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'O', 'O') RETURNING id::text`)
	carrier := id(`INSERT INTO tenants (kind, name_th, name_en, code, legal_type) VALUES ('carrier', 'A', 'A', 'A', 'company') RETURNING id::text`)
	cust := id(`INSERT INTO customers (code, name) VALUES ('Y', 'Y') RETURNING id::text`)
	party := id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, cust)
	rate := id(`INSERT INTO standby_rate_entries (tenant_id, billing_party_id, rate_thb, effective_from_date, effective_from_at)
		VALUES ($1, $2, 400, '2026-01-01', '2025-12-31T17:00:00Z') RETURNING id::text`, carrier, party)
	if _, err := c.Exec(ctx, `INSERT INTO customer_service_fees (tenant_id, billing_party_id, fee_type, amount_thb, unit)
		VALUES ($1, $2, 'standby', 450, 'per_trip')`, carrier, party); err != nil {
		t.Fatal(err)
	}
	standby := func(estimate *float64, source, entry string) string {
		return id(`INSERT INTO standby_records (tenant_id, tenant_source, ended_at, status, billing_estimate_thb, billing_party_id,
			billing_rate_source, billing_rate_entry_id) VALUES ($1, 'driver', now(), 'completed', $2, $3, nullif($4, ''), nullif($5, '')::uuid)
			RETURNING id::text`, own, estimate, party, source, entry)
	}
	fromRate := standby(ptr(400.0), "standby_rate", rate)
	fromFee := standby(ptr(450.0), "service_fee", "")
	unlinked := standby(ptr(300.0), "standby_rate", "")
	unpriced := standby(nil, "", "")
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	for row, want := range map[string]string{fromRate: carrier, fromFee: own, unlinked: own, unpriced: ""} {
		var got *string
		if err := c.QueryRow(ctx, `SELECT billing_tenant_id::text FROM standby_records WHERE id = $1`, row).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if (want == "" && got != nil) || (want != "" && (got == nil || *got != want)) {
			t.Errorf("standby %s: billing tenant %v, want %q", row, got, want)
		}
	}
}
