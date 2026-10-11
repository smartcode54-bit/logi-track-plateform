//go:build integration

// Acceptance tests of issue T41 (#61) on postgres:18-alpine: the finance cut-over rehearsed on a synthetic dump of
// three closed months (no production data). P1 load at the production schema -> the owner's D5 sign-off and `goose up`
// -> changes at source -> `load --since=watermark` -> `reconcile --finance` exits 0 with every allow-list case of main
// spec §6.17 listed with its reason, divergences #1 and #9 at zero differences, statements as stored, numbering at
// lastSeq + 1, and no row changed; a fee the owner replaced, a tampered snapshot and the unsigned D5 duplicates each
// make it exit 2 with the reason.
package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

func ts(t time.Time) dump.Timestamp { return dump.Timestamp{Time: t} }

// ict is a Bangkok wall time.
func ict(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, time.FixedZone("ICT", 7*3600)).UTC()
}

// financeDump writes the rehearsal dump: stage 1 is the P1 full dump, stage 2 the dump after the changes at source
// (a card voided, a statement sent and a second one numbered, a counter moved, a trip priced, a fuel day captured, a
// month's fetch error replaced).
func financeDump(t *testing.T, stage int) string {
	t.Helper()
	t0 := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC) // every P1 document
	t1 := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC) // the changes at source
	d := func(path string, f map[string]any) dump.Doc { return doc(path, t0, f) }
	changed := func(path string, f map[string]any) dump.Doc { return doc(path, t1, f) }
	jul := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) // 07:00 ICT: legacy UTC-midnight rows
	aug := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rate := func(id, party, hub, dest, class, cat string, thb float64, eff time.Time, imp string) dump.Doc {
		f := map[string]any{"customerId": party, "importId": imp, "hubId": hub, "destinationCode": dest, "vehicleClass": class,
			"rateThb": thb, "effectiveFrom": ts(eff)}
		if cat != "" {
			f["jobCategory"] = cat
		}
		return d("customer_rate_entries/"+id, f)
	}
	sepVoid := rate("rate_spx_sep_void", "cust_spx", "HUBA", "SOCE", "6WH", "", 1250, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), "imp_sepv")
	sepVoid.Fields["voided"], sepVoid.Fields["voidedAt"], sepVoid.Fields["voidedBy"] = true, ts(ict(2026, 9, 20, 9)), "uid_admin"
	spk := rate("rate_spx_spk", "cust_spx", "SPK-GW", "SPK", "PICKUP", "", 600, jul, "imp_spk")
	if stage == 2 {
		spk = doc(spk.Path, t1, spk.Fields)
		spk.Fields["voided"], spk.Fields["voidedAt"], spk.Fields["voidedBy"], spk.Fields["voidedReason"] = true, ts(ict(2026, 9, 29, 9)), "uid_admin", "re-announced"
	}
	rates := []dump.Doc{
		rate("rate_spx_jul", "cust_spx", "HUBA", "SOCE", "6WH", "", 1000, jul, "imp_jul"),
		rate("rate_spx_aug", "cust_spx", "huba", "SOCE", "6 WHEELS", "PRIMARY", 1100, aug, "imp_aug"),
		rate("rate_spx_supp", "cust_spx", "HUBA", "SOCE", "6WH", "SUPPLEMENTARY", 800, jul, "imp_supp"),
		rate("rate_spx_4wj", "cust_spx", "HUBA", "SOCE", "4WJ", "", 900, jul, "imp_4wj"),
		rate("rate_spx_hubb", "cust_spx", "HUBA", "HUBB", "6WH", "", 400, jul, "imp_jul"),
		sepVoid, spk,
	}
	cj1 := rate("rate_cjsf_jul", "cust_cjsf", "HUBA", "SOCE", "6WH", "", 2000, ict(2026, 7, 1, 0), "imp_cj1")
	cj1.Fields["effectiveFromDateStr"] = "2026-07-01"
	cj2 := rate("rate_cjsf_aug", "cust_cjsf", "HUBA", "SOCE", "6WH", "", 2200, ict(2026, 8, 1, 0), "imp_cj2")
	cj2.Fields["effectiveFromDateStr"] = "2026-08-01"
	rates = append(rates, cj1, cj2)

	priced := func(party string, delivered time.Time, est, base float64, imp string, fuel bool, extra map[string]any) map[string]any {
		f := map[string]any{"status": "delivered", "jobType": "first_mile", "driverId": "uid_own", "createdAt": ts(delivered.Add(-4 * time.Hour)),
			"deliveredTimestamp": ts(delivered), "billingCustomerId": party, "billingDate": ts(delivered), "billingEstimateThb": est,
			"billingBaseRateThb": base, "billingRateImportId": imp, "billingLookupHubId": "HUBA", "billingLookupDestination": "SOCE",
			"billingRateMultiplier": 1.0, "billingAddThbPerTrip": 0.0, "jobCategory": "PRIMARY"}
		if fuel {
			f["billingFuelAdjustmentId"], f["billingRateMultiplier"], f["billingEffectiveFromDateStr"] = "fa_spx_jul", 1.05, "2026-07-01"
			f["billingRoundEffectiveFromDateStr"], f["billingFuelBandLowerThb"], f["billingFuelBandUpperThb"] = "2026-07-01", 32.01, 33.0
			f["billingReferenceFuelPriceThb"] = 32.94
		}
		for k, v := range extra {
			f[k] = v
		}
		return f
	}
	task := func(id string, plan time.Time, party, src, dest, truck string, extra map[string]any) dump.Doc {
		f := map[string]any{"taskId": strings.ToUpper(id), "taskType": "FIRST_MILE", "status": "Completed", "date": ts(plan), "driverId": "drv_own",
			"sourceHub": src, "destination": dest, "billingCustomerId": party}
		if truck != "" {
			f["truckType"] = truck
		}
		for k, v := range extra {
			f[k] = v
		}
		return d("tasks/"+id, f)
	}
	trip := func(id, taskID string, f map[string]any) dump.Doc {
		f["taskId"] = taskID
		return d("trip_records/"+id, f)
	}
	tasks := []dump.Doc{
		task("ta1", ict(2026, 8, 10, 8), "cust_spx", "HUBA - Hub A", "SOCE (บัวโรย)", "6 Wheels", nil),
		task("ta2", ict(2026, 8, 11, 8), "cust_spx", "HUBA", "SOCE", "6 Wheels", nil),
		task("ta3", ict(2026, 8, 12, 8), "cust_spx", "HUBA", "SOCE", "", nil),
		task("ta4", ict(2026, 8, 13, 8), "cust_spx", "HUBA", "SOCE", "6 Wheels", map[string]any{"jobCategory": "SUPPLEMENTARY"}),
		task("ta5", ict(2026, 9, 10, 8), "cust_spx", "HUBA", "SOCE", "6 Wheels", nil),
		task("ta6", ict(2026, 8, 2, 9), "cust_cjsf", "HUBA", "SOCE", "6 Wheels", nil),
		task("ta7", ict(2026, 8, 15, 8), "cust_spx", "HUBA", "SOCE", "6 Wheels", map[string]any{"jobCategory": "PRIMARY"}),
		task("ta8", ict(2026, 9, 5, 8), "cust_spx", "HUBA", "SOCE", "6 Wheels", map[string]any{"isMultiDelivery": true}),
		task("ta9", ict(2026, 9, 6, 8), "cust_spx", "SPK-GW", "SPK-X1", "PICKUP", nil),
		task("ta9b", ict(2026, 9, 7, 8), "cust_spx", "SPK-GW", "SPK-Y2", "PICKUP", nil),
		task("ta10", ict(2026, 9, 8, 8), "cust_spx", "HUBA", "NOWHERE", "6 Wheels", nil),
		task("ta11", ict(2026, 8, 20, 8), "cust_spx", "HUBA", "SOCE", "6 Wheels", map[string]any{"jobCategory": "SUPPLEMENTARY"}),
		task("ta12", ict(2026, 9, 3, 9), "cust_cjsf", "HUBA", "SOCE", "6 Wheels", nil),
		task("ta13", ict(2026, 6, 20, 8), "cust_spx", "HUBA", "SOCE", "6 Wheels", nil),
		task("tsb9", ict(2026, 9, 8, 8), "cust_cjsf", "HUBA", "SOCE", "6 Wheels", map[string]any{"sourceHubLinkedCustomerId": "cust_spx"}),
	}
	spkTrip := func(delivered time.Time) map[string]any {
		return priced("cust_spx", delivered, 630, 600, "imp_spk", true, map[string]any{"billingLookupHubId": "SPK-GW", "billingLookupDestination": "SPK"})
	}
	t6 := priced("cust_cjsf", ict(2026, 8, 2, 15), 2000, 2000, "imp_cj1", false, map[string]any{"billingDate": ts(ict(2026, 7, 30, 9)),
		"billingRoundEffectiveFromDateStr": "2026-07-01"})
	t12 := priced("cust_cjsf", ict(2026, 9, 4, 10), 2200, 2200, "imp_cj2", false, map[string]any{"billingDate": ts(ict(2026, 9, 3, 9)),
		"billingRoundEffectiveFromDateStr": "2026-08-01"})
	trips := []dump.Doc{
		trip("TRIP-A1", "ta1", priced("cust_spx", ict(2026, 8, 10, 12), 1155, 1100, "imp_aug", true, nil)),
		trip("TRIP-A2", "ta2", priced("cust_spx", ict(2026, 8, 11, 12), 1500, 1500, "manual", false, map[string]any{"billingManualOverride": true})),
		trip("TRIP-A3", "ta3", priced("cust_spx", ict(2026, 8, 12, 12), 945, 900, "imp_4wj", true, nil)),
		trip("TRIP-A4", "ta4", priced("cust_spx", ict(2026, 8, 13, 12), 840, 800, "imp_supp", true, map[string]any{"jobCategory": "SUPPLEMENTARY"})),
		trip("TRIP-S5", "ta5", priced("cust_spx", ict(2026, 9, 10, 12), 1312.5, 1250, "imp_sepv", true, nil)),
		trip("TRIP-C6", "ta6", t6),
		trip("TRIP-A7", "ta7", priced("cust_spx", ict(2026, 8, 15, 12), 1155, 1100, "imp_aug", true, map[string]any{
			"billingRoundEffectiveFromDateStr": "2026-06-15", "billingFuelBandLowerThb": 30.01, "billingFuelBandUpperThb": 31.0,
			"billingReferenceFuelPriceThb": 30.5})),
		trip("TRIP-M8", "ta8", priced("cust_spx", ict(2026, 9, 5, 15), 1205, 1155, "imp_aug", true, map[string]any{
			"isMultiDelivery": true, "billingIsMultiDelivery": true, "billingStopChargeThb": 50.0,
			"billingMultiDeliveryBreakdown": []any{map[string]any{"stopIndex": int64(1), "destination": "SOCE", "baseRateThb": 1100.0, "finalRateThb": 1155.0},
				map[string]any{"stopIndex": int64(2), "destination": "HUBB", "baseRateThb": 50.0, "finalRateThb": 50.0}},
			"deliveryStopsProgress": []any{map[string]any{"index": int64(2), "destination": "HUBB", "status": "delivered"},
				map[string]any{"index": int64(1), "destination": "SOCE", "status": "delivered"}}})),
		trip("TRIP-K9", "ta9", spkTrip(ict(2026, 9, 6, 12))),
		trip("TRIP-K9B", "ta9b", spkTrip(ict(2026, 9, 7, 12))),
		trip("TRIP-U10", "ta10", map[string]any{"status": "delivered", "jobType": "first_mile", "driverId": "uid_own",
			"createdAt": ts(ict(2026, 9, 8, 8)), "deliveredTimestamp": ts(ict(2026, 9, 8, 12)), "billingCustomerId": "cust_spx",
			"billingDate": ts(ict(2026, 9, 8, 12))}),
		trip("TRIP-F11", "ta11", priced("cust_spx", ict(2026, 8, 20, 12), 800, 800, "imp_supp", false, map[string]any{
			"jobCategory": "SUPPLEMENTARY", "billingManualOverride": true})),
		trip("TRIP-C12", "ta12", t12),
		trip("TRIP-O13", "ta13", priced("cust_spx", ict(2026, 6, 20, 12), 999, 999, "imp_old", false, nil)),
	}
	standbyRate := d("standby_rate_entries/sr_spx", map[string]any{"customerId": "cust_spx", "rateThb": 300.0, "effectiveFrom": ts(ict(2026, 7, 1, 0))})
	standby := []dump.Doc{
		d("standby_records/sb1", map[string]any{"driverId": "uid_own", "customerId": "cust_spx", "billingCustomerId": "cust_spx",
			"startedAt": ts(ict(2026, 8, 20, 8)), "endedAt": ts(ict(2026, 8, 20, 10)), "status": "completed", "createdAt": ts(ict(2026, 8, 20, 10)),
			"billingEstimateThb": 300.0, "billingRateEntryId": "sr_spx", "billingEffectiveFromDateStr": "2026-07-01"}),
		d("standby_records/sb9", map[string]any{"driverId": "uid_own", "taskId": "tsb9", "billingCustomerId": "cust_spx",
			"startedAt": ts(ict(2026, 9, 8, 10)), "endedAt": ts(ict(2026, 9, 8, 12)), "status": "completed", "createdAt": ts(ict(2026, 9, 8, 12)),
			"billingEstimateThb": 300.0, "billingRateEntryId": "sr_spx", "billingEffectiveFromDateStr": "2026-07-01"}),
		d("standby_records/sb3", map[string]any{"driverId": "uid_own", "customerId": "cust_cjsf", "startedAt": ts(ict(2026, 9, 9, 8)),
			"endedAt": ts(ict(2026, 9, 9, 10)), "status": "completed", "createdAt": ts(ict(2026, 9, 9, 10))}),
	}
	statement := func(id, party, inv string, month int64, status string, total, wht, net float64) map[string]any {
		return map[string]any{"customerId": party, "invoiceNumber": inv, "period": map[string]any{"month": month, "year": int64(2026)},
			"totalAmount": total, "withholdingTax": wht, "netAmount": net, "tripCount": int64(1), "tripOnlyCount": int64(1),
			"tripSubtotal": total, "status": status, "generatedAt": ts(ict(2026, time.Month(month+1), 2, 10))}
	}
	spxStatement := statement("st_spx_202608", "cust_spx", "SPX-202608-001", 8, "draft", 1155, 11.55, 1143.45)
	statements := []dump.Doc{d("billing_statements/st_cjsf_202607",
		statement("st_cjsf_202607", "cust_cjsf", "CJSF-202607-001", 7, "sent", 2000, 20, 1980.0000000001))}
	spxCounter := map[string]any{"lastSeq": int64(1), "customerCode": "SPX"}
	fuelDaily := []dump.Doc{d("fuel_daily_snapshots/2026-09-01", map[string]any{"capturedAt": ts(ict(2026, 9, 1, 5)), "source": "bangchak_api",
		"status": "ok", "items": []any{map[string]any{"nameTh": "ดีเซล", "price": 32.94}}})}
	monthlySep := map[string]any{"capturedAt": ts(ict(2026, 9, 1, 5)), "source": "bangchak_api", "status": "error", "errorMessage": "timeout", "items": []any{}}
	if stage == 1 {
		statements = append(statements, d("billing_statements/st_spx_202608", spxStatement))
	} else {
		spxStatement["status"], spxStatement["sentAt"] = "sent", ts(ict(2026, 9, 29, 10))
		statements = append(statements, changed("billing_statements/st_spx_202608", spxStatement),
			changed("billing_statements/st_spx_202608b", statement("st_spx_202608b", "cust_spx", "SPX-202608-002", 8, "draft", 840, 8.4, 831.6)))
		spxCounter["lastSeq"] = int64(2)
		tasks = append(tasks, changed("tasks/ts2", map[string]any{"taskId": "TS2", "taskType": "FIRST_MILE", "status": "Completed",
			"date": ts(ict(2026, 9, 20, 8)), "driverId": "drv_own", "sourceHub": "HUBA", "destination": "SOCE", "billingCustomerId": "cust_spx",
			"truckType": "6 Wheels"}))
		f := priced("cust_spx", ict(2026, 9, 20, 12), 1155, 1100, "imp_aug", true, nil)
		f["taskId"] = "ts2"
		trips = append(trips, changed("trip_records/TRIP-S2", f))
		fuelDaily = append(fuelDaily, changed("fuel_daily_snapshots/2026-09-02", map[string]any{"capturedAt": ts(ict(2026, 9, 2, 5)),
			"source": "bangchak_api", "status": "ok", "items": []any{map[string]any{"nameTh": "ดีเซล", "price": 33.04}}}))
		monthlySep = map[string]any{"capturedAt": ts(ict(2026, 9, 29, 5)), "source": "bangchak_api", "status": "ok",
			"items": []any{map[string]any{"nameTh": "ดีเซล", "price": 33.04}}}
	}
	counterDoc := d("billing_counters/cust_spx_202608", spxCounter)
	monthlyDoc := d("fuel_monthly_snapshots/2026-09", monthlySep)
	if stage == 2 {
		counterDoc, monthlyDoc = changed(counterDoc.Path, spxCounter), changed(monthlyDoc.Path, monthlySep)
	}
	exported := t0.Add(24 * time.Hour)
	if stage == 2 {
		exported = t1.Add(24 * time.Hour)
	}
	return writeDump(t, exported,
		collection{"customers", []dump.Doc{
			d("customers/cust_spx", map[string]any{"code": "SPX", "name": "Shopee Express"}),
			d("customers/cust_cjsf", map[string]any{"code": "CJSF", "name": "CJ Super Fresh", "billingDateBasis": "plan"})}},
		collection{"hubs", []dump.Doc{
			d("hubs/hub_a", map[string]any{"source_id": "HUBA", "source_name_th": "ฮับ เอ", "source_name_en": "Hub A"}),
			d("hubs/hub_b", map[string]any{"source_id": "HUBB", "source_name_th": "บางนา"}),
			d("hubs/hub_soce", map[string]any{"source_id": "SOCE", "source_name_th": "บัวโรย", "station_type": "RETURN_CENTER"}),
			d("hubs/hub_spk", map[string]any{"source_id": "SPK-GW", "source_name_th": "J&T บางปู", "station_type": "LH_HUB"})}},
		collection{"drivers", []dump.Doc{d("drivers/drv_own", map[string]any{"firstName": "Somchai", "lastName": "Own", "mobile": "0810000001",
			"authId": "uid_own", "status": "Active", "fullNameTh": "สมชาย ขับดี"})}},
		collection{"customer_rate_entries", rates},
		collection{"customer_fuel_rate_adjustments", []dump.Doc{d("customer_fuel_rate_adjustments/fa_spx_jul", map[string]any{
			"customerId": "cust_spx", "effectiveFrom": ts(jul), "rateMultiplier": 1.05, "addThbPerTrip": 0.0, "referenceFuelPriceThbPerLitre": 32.94,
			"announcementNote": "5%", "fuelBandEnabled": false})}},
		collection{"customer_service_fees", []dump.Doc{
			d("customer_service_fees/fee_es_a", map[string]any{"customerId": "cust_spx", "feeType": "extra_stop", "amountThb": 50.0, "unit": "per_stop"}),
			d("customer_service_fees/fee_es_b", map[string]any{"customerId": "cust_spx", "feeType": "extra_stop", "amountThb": 60.0, "unit": "per_stop"}),
			d("customer_service_fees/fee_sb_a", map[string]any{"customerId": "cust_spx", "feeType": "standby", "amountThb": 250.0, "unit": "per_trip"})}},
		collection{"standby_rate_entries", []dump.Doc{standbyRate}},
		collection{"fuel_daily_snapshots", fuelDaily},
		collection{"fuel_monthly_snapshots", []dump.Doc{d("fuel_monthly_snapshots/2026-08", map[string]any{"capturedAt": ts(ict(2026, 8, 31, 5)),
			"source": "bangchak_api", "status": "ok", "items": []any{map[string]any{"nameTh": "ดีเซล", "price": 32.94}}}), monthlyDoc}},
		collection{"tasks", tasks},
		collection{"trip_records", trips},
		collection{"standby_records", standby},
		collection{"billing_statements", statements},
		collection{"billing_counters", []dump.Doc{counterDoc, d("billing_counters/cust_cjsf_202607", map[string]any{"lastSeq": int64(1)})}},
	)
}

// publicRows are the row fingerprints of schema public (the reconcile writes only its etl.reconciliation_runs row).
func publicRows(h *harness) map[string]string {
	out := map[string]string{}
	for k, v := range h.rowVersions() {
		if strings.HasPrefix(k, "public.") {
			out[k] = v
		}
	}
	return out
}

func readCSV(t *testing.T, dir, prefix string) [][]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, prefix+"*.csv"))
	if err != nil || len(files) != 1 {
		t.Fatalf("%s report files in %s: %v %v", prefix, dir, files, err)
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// The finance cut-over rehearsal (main spec §6.17, §13.9): P1 load, D5 sign-off, delta at source, `load --since`,
// `reconcile --finance`.
func TestFinanceCutoverRehearsal(t *testing.T) {
	h := newP0Harness(t)
	d1 := financeDump(t, 1)
	h.mustRun("load", "--dump", d1)

	// Before the owner's D5 sign-off the finance reconcile refuses: 0010 is held and the party keeps two extra_stop fees.
	code, out, _ := h.run("reconcile", "--dump", d1, "--finance")
	if code != 2 || !strings.Contains(out, "MISMATCH\tservice_fees_d5\tcustomer_service_fees_one_per_type") ||
		!strings.Contains(out, "MISMATCH\tservice_fees_d5\tcust_spx extra_stop") {
		t.Fatalf("finance reconcile before the D5 sign-off: exit %d\n%s", code, out)
	}
	// The P1 runbook: reconcile, the owner's sign-off (the legacy extra_stop winner fee_es_a kept), goose up applies 0010.
	if _, err := h.pool.Exec(h.ctx, `UPDATE customer_service_fees SET fee_type = 'custom', custom_type_name = 'D5 loser: extra_stop'
		WHERE legacy_doc_id = 'fee_es_b'`); err != nil {
		t.Fatal(err)
	}
	if out := h.mustRun("reconcile", "--dump", d1, "--accept-findings", "owner"); !strings.Contains(out, "reconcile: match") {
		t.Fatalf("signed-off reconcile: %s", out)
	}
	if _, err := h.runner.Up(h.ctx); err != nil {
		t.Fatalf("goose up: %v", err)
	}

	// The changes at source, then the delta load (the voidedBy user exists now, so the void applies).
	if _, err := h.pool.Exec(h.ctx, `INSERT INTO users (legacy_auth_uid, email) VALUES ('uid_admin', 'admin@example.test')`); err != nil {
		t.Fatal(err)
	}
	d2 := financeDump(t, 2)
	out = h.mustRun("load", "--dump", d2, "--since", "watermark")
	if !strings.Contains(out, "8 document(s) applied") {
		t.Fatalf("delta load: %s", out)
	}
	if v := h.scalar(`SELECT voided::text || ' ' || (voided_by IS NOT NULL)::text FROM customer_rate_entries WHERE legacy_doc_id = 'rate_spx_spk'`); v != "true true" {
		t.Fatalf("the void at source applied by the delta: %v", v)
	}

	// The finance reconcile: exit 0, nothing written.
	before := publicRows(h)
	dir := t.TempDir()
	out = h.mustRun("reconcile", "--dump", d2, "--finance", "--out", dir)
	if !strings.Contains(out, "reconcile: match") || !strings.Contains(out, "parity 2026-07 2026-08 2026-09: 17 recomputed") ||
		!strings.Contains(out, "0 mismatched") {
		t.Fatalf("finance reconcile: %s", out)
	}
	if after := publicRows(h); len(after) != len(before) {
		t.Fatal("tables changed")
	} else {
		for k, v := range before {
			if after[k] != v {
				t.Errorf("the finance reconcile changed %s", k)
			}
		}
	}

	checks := map[string][]string{}
	for _, r := range readCSV(t, dir, "reconcile-")[1:] {
		checks[r[0]+"|"+r[1]] = r
	}
	parity := map[string][]string{}
	for _, r := range readCSV(t, dir, "parity-")[1:] {
		parity[r[0]+" "+r[1]] = r // kind key
	}
	// Every allow-list case of §6.17 is listed with its reason.
	for key, want := range map[string]string{
		"trip TRIP-A1": "exact|", "trip TRIP-A2": "allowed|frozen", "trip TRIP-F11": "allowed|frozen",
		"trip TRIP-A3": "allowed|no_vehicle_class", "trip TRIP-A4": "allowed|supp_priced_with_fuel",
		"trip TRIP-S5": "allowed|since_voided", "trip TRIP-C6": "allowed|plan_date_drifted",
		"trip TRIP-A7": "allowed|set_trip_job_category_provenance", "trip TRIP-M8": "exact|",
		"trip TRIP-K9": "allowed|since_voided", "trip TRIP-K9B": "allowed|since_voided", "trip TRIP-U10": "exact|",
		"trip TRIP-C12": "exact|", "trip TRIP-S2": "exact|",
		"standby sb1": "exact|", "standby sb9": "exact|", "standby sb3": "exact|",
	} {
		r, ok := parity[key]
		if !ok {
			t.Errorf("parity report lacks %s", key)
			continue
		}
		if got := r[6] + "|" + r[7]; got != want {
			t.Errorf("%s: %s (%s), want %s", key, got, r[9], want)
		}
	}
	if _, ok := parity["trip TRIP-O13"]; ok {
		t.Error("a trip outside the window was recomputed")
	}
	if r := parity["trip TRIP-S5"]; !strings.Contains(r[9], "rate entry rate_spx_sep_void") {
		t.Errorf("since-voided lists the voided id: %v", r)
	}
	// What a difference means for invoiced money: a sent period is never repriced, an open one is by a forced recompute.
	for key, want := range map[string]string{"trip TRIP-C6": "period sent (CJSF-202607-001): never repriced",
		"trip TRIP-A3": "period sent (SPX-202608-001): never repriced", "trip TRIP-K9": "open period: a forced recompute after the flip reprices it"} {
		if r := parity[key]; !strings.Contains(r[9], want) {
			t.Errorf("%s: %q lacks %q", key, r[9], want)
		}
	}
	for _, k := range []string{"trip TRIP-K9", "trip TRIP-K9B"} {
		if parity[k][8] != "1" {
			t.Errorf("%s is decided by divergence #1: %v", k, parity[k])
		}
	}
	if parity["standby sb9"][8] != "9" {
		t.Errorf("sb9 is decided by divergence #9: %v", parity["standby sb9"])
	}
	for key, want := range map[string]string{
		"divergence_1|SPK dash collision (§6.18 #1)":                       "2,0,true",
		"divergence_9|standby ignores the task's billing party (§6.18 #9)": "1,0,true",
		"spk_collisions|SPK": "SPK-X1 SPK-Y2,SPK,true",
		"service_fees_d5|customer_service_fees_one_per_type": "valid,valid,true",
		"service_fees_d5_winner|cust_spx extra_stop":         "fee_es_a,fee_es_a,true",
		"statements|billing_statements":                      "3,3,true",
		"counters_next|cust_spx 2026-08":                     "2,SPX-202608-003,true",
		"counters_next|cust_cjsf 2026-07":                    "1,CJSF-202607-002,true",
		"rate_tables|customer_rate_entries":                  "9,9,true",
		"rate_tables|customer_fuel_rate_adjustments":         "1,1,true",
		"fuel_snapshots|fuel_daily_snapshots":                "2,2,true",
		"fuel_snapshots|fuel_monthly_snapshots":              "2,2,true",
		"standby_rate_review|sr_spx":                         "2026-06-30T17:00:00Z,2026-07-01,true",
	} {
		r, ok := checks[key]
		if !ok {
			t.Errorf("reconciliation report lacks %s", key)
			continue
		}
		if got := r[2] + "," + r[3] + "," + r[4]; got != want {
			t.Errorf("%s: %s, want %s (%s)", key, got, want, r[5])
		}
	}
	if r := checks["statement_totals|st_spx_202608 SPX-202608-001"]; r == nil || r[4] != "true" || !strings.HasPrefix(r[5], "sent;") {
		t.Errorf("statement totals are reported, never failed: %v", r)
	}

	t.Run("a fee the owner replaced fails with fee_changed", func(t *testing.T) {
		if _, err := h.pool.Exec(h.ctx, `UPDATE customer_service_fees SET amount_thb = 60 WHERE legacy_doc_id = 'fee_es_a'`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = h.pool.Exec(h.ctx, `UPDATE customer_service_fees SET amount_thb = 50 WHERE legacy_doc_id = 'fee_es_a'`)
		}()
		code, out, _ := h.run("reconcile", "--dump", d2, "--finance")
		if code != 2 || !strings.Contains(out, "MISMATCH\tparity_trip\tTRIP-M8 (cust_spx 2026-09)") || !strings.Contains(out, "fee_changed: priced with an extra_stop fee of 50.00") {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
	t.Run("a snapshot that differs from the dump and the recompute fails", func(t *testing.T) {
		if _, err := h.pool.Exec(h.ctx, `UPDATE trip_billing_snapshots SET estimate_thb = 1160 WHERE trip_id =
			(SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP-A1')`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = h.pool.Exec(h.ctx, `UPDATE trip_billing_snapshots SET estimate_thb = 1155 WHERE trip_id =
				(SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP-A1')`)
		}()
		code, out, _ := h.run("reconcile", "--dump", d2, "--finance")
		if code != 2 || !strings.Contains(out, "MISMATCH\tparity_load\tTRIP-A1") ||
			!strings.Contains(out, "MISMATCH\tparity_trip\tTRIP-A1 (cust_spx 2026-08)\tsource 1160 import imp_aug") || !strings.Contains(out, "amount_differs") {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
	t.Run("the window is closed months only", func(t *testing.T) {
		if code, _, errb := h.run("reconcile", "--dump", d2, "--finance", "--through", "2026-10"); code != 2 || !strings.Contains(errb, "not a closed month") {
			t.Errorf("--through=2026-10 on 2 Oct: exit %d %s", code, errb)
		}
		if code, _, _ := h.run("reconcile", "--dump", d2, "--months", "2"); code != 2 {
			t.Errorf("--months without --finance: exit %d", code)
		}
	})
	if out := h.mustRun("reconcile", "--dump", d2, "--finance", "--months", "1"); !strings.Contains(out, "parity 2026-09: ") {
		t.Errorf("one month: %s", out)
	}
}

// The committed fixture dump holds legacy prices the engine does not reproduce (a fuel round its snapshot lacks, a
// multi-drop trip priced as single): the finance reconcile exits 2 and names a reason for every mismatch.
func TestFinanceReconcileOnTheFixtures(t *testing.T) {
	h := newHarness(t)
	h.mustRun("load", "--fixtures")
	code, out, errb := h.run("reconcile", "--fixtures", "--finance")
	if code != 2 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errb)
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "MISMATCH\tparity_trip\t") || strings.HasPrefix(line, "MISMATCH\tparity_standby\t") {
			n++
			fields := strings.Split(line, "\t")
			if len(fields) < 6 || !strings.Contains(fields[5], ": ") {
				t.Errorf("a mismatch without its reason: %q", line)
			}
		}
	}
	if n == 0 || !strings.Contains(out, "MISMATCH\tparity_trip\tTRIP001 (cust_spx 2026-09)") {
		t.Errorf("parity mismatches on the fixtures:\n%s", out)
	}
}
