//go:build integration

// Acceptance tests of issue T24 (#44, slice T24L) on postgres:18-alpine: the full load of every collection of
// Appendix A §A.3 from the fixture dump (links that point forward in the load order, children, files), the P1 runbook
// rehearsal (up-to 9 -> load -> reconcile -> the owner's sign-off -> up applies 0010 -> later duplicates become field
// quarantine; a surviving duplicate stops goose up), media-copy of every registered object from the in-process fake
// object source into the compose MinIO and rewrite-urls --apply, and the ETL bookkeeping of a re-home done through
// the API.
package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagetest"
)

// newP0Harness migrates like production before the P1 load: up-to 9, then every later version but the held 0010
// (0011+ ship in P0 with `migrate apply`).
func newP0Harness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	for _, f := range h.runner.Files() {
		if f.Version > 11 {
			if _, err := h.runner.Apply(h.ctx, f.Version); err != nil {
				t.Fatalf("apply %d: %v", f.Version, err)
			}
		}
	}
	return h
}

// Every collection of the fixture dump loads with its links, children and files (Appendix A §A.3.2-§A.3.10).
func TestFullLoadOfEveryCollection(t *testing.T) {
	h := newP0Harness(t)
	h.mustRun("load", "--fixtures")
	check := func(name, sql string, want any, args ...any) {
		t.Helper()
		if v := h.scalar(sql, args...); fmt.Sprint(v) != fmt.Sprint(want) {
			t.Errorf("%s: %v, want %v", name, v, want)
		}
	}
	// Parties and rate tables (§A.3.2, §A.3.9).
	check("owner company in the own fleet", `SELECT tenant_id::text || ' ' || tenant_source || ' ' || withholding_tax_rate::text
		FROM companies WHERE legacy_doc_id = 'co_owner'`, ownFleet+" self 3.00")
	check("company files", `SELECT count(*) FROM companies c JOIN file_objects f ON f.id IN (c.logo_file_id, c.stamp_file_id, c.signature_file_id)
		WHERE c.legacy_doc_id = 'co_owner' AND f.purpose LIKE 'company_%'`, 3)
	check("subcontractor company on its carrier", `SELECT t.legacy_doc_id FROM companies c JOIN tenants t ON t.id = c.tenant_id
		WHERE c.legacy_doc_id = 'co_alpha'`, "sub_alpha")
	check("Bangkok date of a UTC-midnight rate", `SELECT effective_from_date::text || ' ' || job_category || ' ' || vehicle_class
		FROM customer_rate_entries WHERE legacy_doc_id = 'rate_spx_a'`, "2026-08-01 PRIMARY 6WH")
	check("folded class", `SELECT vehicle_class FROM customer_rate_entries WHERE legacy_doc_id = 'rate_spx_b'`, "2W")
	check("voided legacy announcement", `SELECT voided::text || ' ' || (voided_by IS NULL)::text FROM customer_rate_entries
		WHERE legacy_doc_id = 'rate_spx_void'`, "true true")
	check("fuel band switched off", `SELECT fuel_band_enabled::text || ' ' || add_thb_per_trip::text FROM customer_fuel_rate_adjustments
		WHERE legacy_doc_id = 'fa_cjsf_band'`, "false -10.00")
	check("standby rate day", `SELECT effective_from_date::text FROM standby_rate_entries WHERE legacy_doc_id = 'sr_spx'`, "2026-08-01")
	check("service fees, duplicates kept", `SELECT count(*) FROM customer_service_fees`, 5)
	check("statement: WHT rate and rounded net", `SELECT invoice_number || ' ' || withholding_tax_rate::text || ' ' || net_amount::text || ' ' || status
		FROM billing_statements WHERE legacy_doc_id = 'st_cjsf_202609'`, "CJSF-202609-001 0.0100 495.00 sent")
	check("statement without invoiceNumber", `SELECT invoice_number || ' ' || total_amount::text FROM billing_statements
		WHERE legacy_doc_id = 'ST-SPX-2026-08'`, "ST-SPX-2026-08 1234.56")
	check("counters split on the last _", `SELECT string_agg(customer_code || ' ' || period_year || '-' || period_month || ' ' || last_seq, ', '
		ORDER BY customer_code) FROM billing_counters`, "CJSF 2026-9 1, SPX 2026-8 0")
	// Hubs, aliases, distances (§A.3.3).
	check("hub winner and loser", `SELECT h.legacy_doc_id || ' ' || h.network || ' ' || h.station_type FROM hubs h WHERE source_id = 'HUBA'`,
		"hub_a1 SPX HUB")
	check("loser maps to the winner's row", `SELECT sd.target_id = h.id::text FROM etl.source_docs sd JOIN hubs h ON h.source_id = 'HUBA'
		WHERE sd.doc_path = 'hubs/hub_a2'`, true)
	check("Thai name moved from EN", `SELECT name_th || ' ' || coalesce(name_en, 'null') FROM hubs WHERE source_id = 'HUBB'`, "บางนา null")
	check("SPK network and SOC", `SELECT string_agg(source_id || ' ' || network || ' ' || station_type, ', ' ORDER BY source_id)
		FROM hubs WHERE source_id IN ('SPK-GW', 'SOCE')`, "SOCE SPX SOC, SPK-GW SPK HUB")
	check("aliases name -> code only", `SELECT string_agg(a.alias::text || '>' || h.source_id::text, ', ' ORDER BY a.alias)
		FROM hub_name_aliases a JOIN hubs h ON h.id = a.hub_id WHERE h.source_id IN ('HUBA', 'SPK-GW')`, "Hub A>HUBA, J&T บางปู>SPK-GW, ฮับ เอ>HUBA")
	check("one table, both directions", `SELECT string_agg(direction || ' ' || soc_key || ' ' || distance_m, ', ' ORDER BY direction)
		FROM hub_soc_distances`, "hub_to_soc SOCE 12000, soc_to_hub SOCE 12500")
	check("distances_last_calculated", `SELECT value->>'pairs' FROM settings WHERE key = 'distances_last_calculated'`, "1")
	check("a task resolves its hub now", `SELECT h.source_id FROM tasks t JOIN hubs h ON h.id = t.source_hub_id WHERE t.legacy_doc_id = 'task_1'`, "HUBA")
	// Fleet (§A.3.4).
	check("truck files", `SELECT string_agg(kind, ',' ORDER BY position) FROM truck_files tf JOIN trucks t ON t.id = tf.truck_id
		WHERE t.legacy_doc_id = 'trk_own1'`, "image_front_right,document_tax,insurance_document,legacy_image")
	check("truck status history", `SELECT string_agg(s.status || '/' || coalesce(changed_by_label, '-') || '/' || coalesce(reason, '-'), ', ' ORDER BY changed_at)
		FROM status_history s JOIN trucks t ON t.id = s.entity_id WHERE s.entity_type = 'truck' AND t.legacy_doc_id = 'trk_own1'`,
		"Active/admin@example.test/registered, Tax Renewed/admin@example.test/-")
	check("driver status history", `SELECT string_agg(s.status || '/' || coalesce(changed_by_label, '-') || '/' || coalesce(previous_status, '-'), ', '
		ORDER BY changed_at) FROM status_history s JOIN drivers d ON d.id = s.entity_id WHERE s.entity_type = 'driver' AND d.legacy_doc_id = 'drv_own'`,
		"Active/system/-, On-Duty/Admin A/Active")
	check("customer driver ids", `SELECT string_agg(cu.code || ':' || dc.id_type_key || '=' || dc.value, ',') FROM driver_customer_codes dc
		JOIN customers cu ON cu.id = dc.customer_id JOIN drivers d ON d.id = dc.driver_id WHERE d.legacy_doc_id = 'drv_own'`, "SPX:appId=A-001")
	check("home truck", `SELECT a.legacy_doc_id FROM drivers d JOIN truck_assignments a ON a.id = d.current_assignment_id
		WHERE d.legacy_doc_id = 'drv_own'`, "ta_1")
	check("active task", `SELECT t.legacy_doc_id FROM drivers d JOIN tasks t ON t.id = d.active_task_id WHERE d.legacy_doc_id = 'drv_own'`, "task_1")
	check("active maintenance", `SELECT m.legacy_doc_id FROM trucks t JOIN maintenance_records m ON m.id = t.active_maintenance_id
		WHERE t.legacy_doc_id = 'trk_own1'`, "mnt1")
	check("assignments", `SELECT string_agg(legacy_doc_id || ' ' || status || ' ' || admin_label || ' ' || tenant_source, ', ' ORDER BY legacy_doc_id)
		FROM truck_assignments`, "ta_1 active Admin A truck, ta_2 revoked System truck")
	check("installations", `SELECT string_agg(install_id || ' ' || platform || ' ' || flavor, ', ' ORDER BY install_id) FROM mobile_installations`,
		"inst_alpha1 ios prod, inst_own1 android prod")
	check("latest position", `SELECT gps_vehicle_id || ' ' || speed_kmh::text || ' ' || tenant_source FROM vehicle_locations`, "GPS1 40.0 truck")
	check("unknown class never guessed", `SELECT coalesce(vehicle_class, 'null') FROM trucks WHERE legacy_doc_id = 'trk_trailer'`, "null")
	// Operations (§A.3.5, §A.3.6, §A.3.8).
	check("task stops", `SELECT string_agg(s.stop_index || ' ' || s.destination_raw || ' ' || s.status, ', ' ORDER BY s.stop_index)
		FROM task_delivery_stops s JOIN tasks t ON t.id = s.task_id WHERE t.legacy_doc_id = 'task_md'`, "1 SOCE delivered, 2 HUBB pending")
	check("task number counters", `SELECT string_agg(task_type || ' ' || plan_date || ' ' || last_seq, ', ' ORDER BY task_type, plan_date)
		FROM task_number_counters`, "first_mile 2026-09-01 3, line_haul 2026-09-01 1, line_haul 2026-09-05 12")
	check("trip stops in completion order", `SELECT string_agg(s.stop_index || '@' || s.completed_seq, ', ' ORDER BY s.completed_seq)
		FROM trip_delivery_stops s JOIN trip_records r ON r.id = s.trip_id WHERE r.legacy_doc_id = 'TRIP_MD'`, "2@1, 1@2")
	check("stop photo linked to its stop", `SELECT s.stop_index FROM trip_photos p JOIN trip_delivery_stops s ON s.id = p.stop_id
		JOIN trip_records r ON r.id = p.trip_id WHERE r.legacy_doc_id = 'TRIP_MD' AND p.photo_type = 'stop_2_arrived'`, 2)
	check("multi-drop breakdown", `SELECT string_agg(b.stop_index || ' ' || b.final_rate_thb::text, ', ' ORDER BY b.stop_index)
		FROM trip_billing_stop_breakdown b JOIN trip_records r ON r.id = b.trip_id WHERE r.legacy_doc_id = 'TRIP_MD'`, "1 1050.00, 2 450.26")
	check("snapshot links the rate row and the fuel adjustment", `SELECT e.legacy_doc_id || ' ' || f.legacy_doc_id FROM trip_billing_snapshots s
		JOIN trip_records r ON r.id = s.trip_id JOIN customer_rate_entries e ON e.id = s.rate_entry_id
		JOIN customer_fuel_rate_adjustments f ON f.id = s.fuel_adjustment_id WHERE r.legacy_doc_id = 'TRIP001'`, "rate_spx_a fa_spx_1")
	check("standby rate link", `SELECT e.legacy_doc_id || ' ' || s.billing_rate_source FROM standby_records s
		JOIN standby_rate_entries e ON e.id = s.billing_rate_entry_id WHERE s.legacy_doc_id = 'sb3'`, "sr_spx standby_rate")
	check("maintenance files", `SELECT string_agg(f.kind, ',' ORDER BY f.position) FROM maintenance_files f JOIN maintenance_records m
		ON m.id = f.maintenance_id WHERE m.legacy_doc_id = 'mnt1'`, "image,receipt,invoice")
	// Finance and HR (§A.3.10).
	check("penalty types", `SELECT string_agg(code, ',') FROM penalty_types`, "LATE_SLA")
	check("approved payroll with its lines", `SELECT p.status || ' ' || count(l.*) || ' ' || p.net_pay_thb::text FROM payroll_runs p
		JOIN payroll_line_items l ON l.payroll_run_id = p.id WHERE p.legacy_doc_id = 'uid_own_2026-09_R1' GROUP BY p.status, p.net_pay_thb`,
		"approved 2 2900.00")
	check("payroll doc id wins", `SELECT pay_period || ' ' || pay_round FROM payroll_runs WHERE legacy_doc_id = 'uid_own_2026-09_R2'`, "2026-09 R2")
	check("ledger link", `SELECT t.legacy_doc_id || ' ' || t.tx_date_source || ' ' || t.tenant_source FROM payroll_runs p
		JOIN transactions t ON t.id = p.ledger_transaction_id WHERE p.legacy_doc_id = 'uid_own_2026-09_R1'`, "tx_payout_1 legacy_utc form")
	check("payout links its run", `SELECT p.legacy_doc_id FROM transactions t JOIN payroll_runs p ON p.id = t.payroll_run_id
		WHERE t.legacy_doc_id = 'tx_payout_1'`, "uid_own_2026-09_R1")
	check("penalty application", `SELECT count(*) FROM payroll_penalty_applications`, 1)
	check("renewal on the truck's tenant", `SELECT tenant_source || ' ' || tx_date::text FROM transactions WHERE legacy_doc_id = 'tx_tax_1'`,
		"truck 2026-08-15")
	check("leave by doc id and auth uid", `SELECT string_agg(legacy_doc_id || ' ' || driver_ref_match || ' ' || start_date, ', ' ORDER BY legacy_doc_id)
		FROM leave_requests`, "lv_1 doc_id 2026-09-03, lv_2 auth_uid 2026-09-10")
	check("leave attachment", `SELECT count(*) FROM leave_request_attachments`, 1)
	check("holidays", `SELECT string_agg(legacy_doc_id || ' ' || holiday_date || ' ' || coalesce(tenant_id::text, 'null'), ', ' ORDER BY legacy_doc_id)
		FROM holidays`, "ho_1 2026-10-23 null, ho_2 2026-11-02 "+ownFleet)
	// Comms and platform (§A.3.10).
	check("chats", `SELECT string_agg(legacy_doc_id || ' ' || status, ', ' ORDER BY legacy_doc_id) FROM chats`,
		"chat_1 open, chat_2 in_progress, chat_3 closed")
	check("messages", `SELECT string_agg(m.legacy_doc_id || ' ' || m.sender_role || ' ' || (m.image_file_id IS NOT NULL)::text, ', '
		ORDER BY m.legacy_doc_id) FROM chat_messages m`, "m1 driver false, m2 admin true")
	check("broadcast", `SELECT coalesce(title, 'null') || ' ' || recipient_count FROM broadcasts`, "null 2")
	check("security event", `SELECT event_type || ' ' || actor_legacy_uid || ' ' || coalesce(tenant_id::text, 'null') FROM security_events
		WHERE legacy_doc_id = 'se_1'`, "trip_record_renamed uid_admin null")
	check("mobile_app split: floor and release halves", `SELECT value->>'minAllowedVersion' || ' ' || (value->>'latestVersion') || ' ' ||
		(value->>'flavor') FROM settings WHERE key = 'mobile_app'`, "3.1.0 3.2.0 prod")
	check("release row with a public APK", `SELECT r.version || ' ' || f.visibility || ' ' || f.purpose || ' ' || f.bucket
		FROM mobile_app_releases r JOIN file_objects f ON f.id = r.apk_file_id`, "3.2.0 public apk logitrack-public")
	check("generic setting", `SELECT value->>'primaryColor' FROM settings WHERE key = 'branding'`, "#123456")
	check("waitlist and partner interest", `SELECT (SELECT count(*) FROM waitlist) + (SELECT count(*) FROM partner_interest)`, 2)
	check("fuel snapshots", `SELECT (SELECT count(*) FROM fuel_daily_snapshots) || ' ' || (SELECT string_agg(status, ',' ORDER BY month_key)
		FROM fuel_monthly_snapshots)`, "1 ok,error")
	// Every legacy URL became a file_objects row: nothing is left for rewrite-urls but the APK URL kept verbatim in
	// settings('mobile_app') until `rewrite-urls --apply`.
	code, out, _ := h.run("rewrite-urls")
	if code != 2 || strings.Count(strings.TrimSpace(out), "\n") != 0 || !strings.Contains(out, "UNMAPPED\tsettings.value\t1 row(s)") {
		t.Errorf("rewrite-urls after the full load: exit %d\n%s", code, out)
	}

	t.Run("--force re-applies every collection to the same state (immutable rows kept, history appended once)", func(t *testing.T) {
		counts := func() string {
			return h.scalar(`SELECT (SELECT count(*) FROM status_history) || ' ' || (SELECT count(*) FROM security_events) || ' ' ||
				(SELECT count(*) FROM transactions) || ' ' || (SELECT count(*) FROM payroll_line_items) || ' ' || (SELECT count(*) FROM file_objects)
				|| ' ' || (SELECT count(*) FROM hub_name_aliases) || ' ' || (SELECT count(*) FROM trip_photos)`).(string)
		}
		before := counts()
		h.mustRun("load", "--fixtures", "--force")
		if after := counts(); after != before {
			t.Errorf("row counts after --force: %s, before %s", after, before)
		}
		state, err := h.engine().QuarantineReport(h.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if text := sectionsText(t, state); text != readSnapshot(t) {
			t.Errorf("state after --force differs from the snapshot:\n%s", diff(readSnapshot(t), text))
		}
		if v := h.scalar(`SELECT status FROM payroll_runs WHERE legacy_doc_id = 'uid_own_2026-09_R1'`); v != "approved" {
			t.Errorf("payroll status after --force: %v", v)
		}
	})

	t.Run("a re-applied trip keeps its restamp lock (the statements loaded before)", func(t *testing.T) {
		h.mustRun("load", "--fixtures", "--collections", "trip_records", "--force")
		if v := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE doc_path = 'trip_records/TRIP002' AND reason_code = 'billing_date_locked'
			AND resolved_at IS NULL`); v != int64(1) {
			t.Errorf("billing_date_locked on TRIP002: %v", v)
		}
	})

	t.Run("reconcile: every collection accounted for, money exact", func(t *testing.T) {
		dir := t.TempDir()
		out := h.mustRun("reconcile", "--fixtures", "--out", dir)
		if !strings.Contains(out, "reconcile: match") {
			t.Fatalf("reconcile: %s", out)
		}
		csvs, err := filepath.Glob(filepath.Join(dir, "*.csv"))
		if err != nil || len(csvs) != 1 {
			t.Fatalf("report files: %v %v", csvs, err)
		}
		b, err := os.ReadFile(csvs[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range []string{"counts,hubs,8,8,true", "counts,payroll,3,3,true", "counts,messages,3,3,true",
			"counts,mobile_installations,4,4,true", "counts,billing_counters,2,2,true", "counts,settings,3,3,true",
			"targets,mobile_installations,2,2,true", "targets,hubs,6,6,true", "targets,settings -> settings,2,2,true",
			"targets,settings -> tenants,1,1,true", "quarantine_rows,companies,1,1,true", "quarantine_rows,transactions,1,1,true"} {
			if !strings.Contains(string(b), line) {
				t.Errorf("reconciliation report lacks %q:\n%s", line, b)
			}
		}
	})
}

// The P1 runbook on the fixtures (main spec §13.9, R59, R88): up-to 9 (+ the P0 migrations) -> full load ->
// reconcile -> the owner's sign-off (D5 losers corrected, findings accepted) -> goose up applies 0010 -> a later
// delta's duplicate of a D5 key is quarantined on its field instead of failing the load on the new unique index.
func TestRunbookRehearsal(t *testing.T) {
	h := newP0Harness(t)
	if v, err := h.runner.Version(h.ctx); err != nil || v < 11 {
		t.Fatalf("schema version %d %v", v, err)
	}
	if v := h.scalar(`SELECT count(*) FROM pg_indexes WHERE indexname = 'chats_one_open_per_driver'`); v != int64(0) {
		t.Fatal("0010 must be held back before the load")
	}
	h.mustRun("load", "--fixtures")
	if out := h.mustRun("reconcile", "--fixtures"); !strings.Contains(out, "reconcile: match") {
		t.Fatalf("reconcile after the load: %s", out)
	}
	// The owner reviews the quarantine report, corrects the D5 losers and accepts the remaining findings.
	if out := h.mustRun("quarantine", "list", "--reason", "duplicate_service_fee"); strings.Count(out, "customer_service_fees/") != 4 {
		t.Errorf("duplicate fees listed: %s", out)
	}
	correctD5Losers(t, h)
	if out := h.mustRun("reconcile", "--fixtures", "--accept-findings", "owner"); !strings.Contains(out, "reconcile: match") {
		t.Fatalf("signed-off reconcile: %s", out)
	}
	if _, err := h.runner.Up(h.ctx); err != nil {
		t.Fatalf("goose up applies 0010 on the signed-off data: %v", err)
	}
	if v := h.scalar(`SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE i.indisvalid AND i.indisunique
		AND c.relname IN ('vehicle_expenses_fuel_taxinv', 'chats_one_open_per_driver', 'customer_service_fees_one_per_type')`); v != int64(3) {
		t.Fatalf("valid D5 indexes: %v", v)
	}

	// A delta after 0010 (the mirror applies the same transforms): new duplicates of each D5 key.
	t1 := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	d := writeDump(t, t1.Add(time.Hour),
		collection{"vehicle_expenses", []dump.Doc{doc("vehicle_expenses/exp5", t1, map[string]any{"driverId": "uid_own", "type": "fuel",
			"date": dump.Timestamp{Time: t1}, "amount": 900.0, "taxInvId": "INV-1"})}},
		collection{"chats", []dump.Doc{doc("chats/chat_4", t1, map[string]any{"driverId": "uid_own", "status": "open"})}},
		collection{"customer_service_fees", []dump.Doc{doc("customer_service_fees/fee_es_c", t1, map[string]any{"customerId": "cust_spx",
			"feeType": "extra_stop", "amountThb": 70.0, "unit": "per_stop"})}})
	out := h.mustRun("load", "--dump", d, "--since", "watermark")
	if !strings.Contains(out, "3 document(s) applied") {
		t.Fatalf("delta after 0010: %s", out)
	}
	if v := h.scalar(`SELECT coalesce(tax_inv_id, 'null') FROM vehicle_expenses WHERE legacy_doc_id = 'exp5'`); v != "null" {
		t.Errorf("exp5 keeps a duplicate receipt number: %v", v)
	}
	if v := h.scalar(`SELECT status FROM chats WHERE legacy_doc_id = 'chat_4'`); v != "closed" {
		t.Errorf("chat_4: %v", v)
	}
	for path, want := range map[string]string{"vehicle_expenses/exp5": "taxInvId duplicate_natural_key loaded",
		"chats/chat_4": "status duplicate_natural_key loaded", "customer_service_fees/fee_es_c": "feeType duplicate_service_fee rejected"} {
		if v := h.scalar(`SELECT q.field || ' ' || q.reason_code || ' ' || sd.status FROM etl.quarantine q
			JOIN etl.source_docs sd ON sd.doc_path = q.doc_path WHERE q.doc_path = $1 AND q.resolved_at IS NULL`, path); v != want {
			t.Errorf("%s: %v, want %s", path, v, want)
		}
	}
}

// A duplicate that survives the sign-off stops `goose up` (Appendix A §A.4 D5): the build fails on the duplicate and
// leaves an INVALID index, and every rerun stops on the 0010 INVALID-index check until the rows are fixed.
func TestSurvivingDuplicateStopsGooseUp(t *testing.T) {
	h := newP0Harness(t)
	h.mustRun("load", "--fixtures")
	// The owner corrects the receipt and the fees but forgets the second open chat.
	for _, sql := range []string{
		`UPDATE vehicle_expenses SET tax_inv_id = NULL WHERE legacy_doc_id = 'exp2'`,
		`UPDATE customer_service_fees SET fee_type = 'custom', custom_type_name = 'D5 loser' WHERE legacy_doc_id IN ('fee_es_b', 'fee_sb_b')`,
	} {
		if _, err := h.pool.Exec(h.ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	_, err := h.runner.Up(h.ctx)
	if err == nil || !strings.Contains(err.Error(), "chats_one_open_per_driver") {
		t.Fatalf("goose up with a surviving duplicate: %v", err)
	}
	_, err = h.runner.Up(h.ctx)
	if err == nil || !strings.Contains(err.Error(), "INVALID") {
		t.Fatalf("the rerun must stop on the INVALID index: %v", err)
	}
	if v := h.scalar(`SELECT count(*) FROM goose_db_version WHERE version_id = 10 AND is_applied`); v != int64(0) {
		t.Errorf("0010 recorded as applied: %v", v)
	}
}

// media-copy copies every object the full load registered from the fake object source (gcptest) into the compose
// MinIO under the identical key, the APK into S3_PUBLIC_BUCKET; one object GCS lacks stays missing_at_source;
// rewrite-urls --apply points the APK URL of settings('mobile_app') at S3_PUBLIC_BASE_URL, after which no Firebase
// Storage URL is left outside file_objects.legacy_url.
func TestFullLoadMediaCopyAndRewriteURLs(t *testing.T) {
	h := newP0Harness(t)
	mc := storagetest.Shared(t)
	s3cfg := mc.Config("")
	s3, err := storage.NewS3(s3cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s3.Bootstrap(h.ctx, zerolog.Nop()); err != nil {
		t.Fatal(err)
	}
	fake := gcptest.New(t, "logitrack-fixtures")
	key := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(key, fake.ServiceAccountJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	const publicBase = "https://media.example.test/public"
	h.extra = []string{"ETL_GCS_BUCKET=logitrack-legacy.appspot.com", "GOOGLE_APPLICATION_CREDENTIALS=" + key,
		"S3_ENDPOINT=" + s3cfg.Endpoint, "S3_REGION=" + s3cfg.Region, "S3_ACCESS_KEY_ID=" + s3cfg.AccessKeyID,
		"S3_SECRET_ACCESS_KEY=" + s3cfg.SecretAccessKey, "S3_BUCKET=" + s3cfg.Bucket, "S3_PUBLIC_BUCKET=" + s3cfg.PublicBucket}
	h.deps.google = fake.Client()
	h.mustRun("load", "--fixtures")
	if code, _, _ := h.run("rewrite-urls", "--apply"); code != 2 {
		t.Error("--apply without S3_PUBLIC_BASE_URL is a configuration error")
	}
	h.extra = append(h.extra, "S3_PUBLIC_BASE_URL="+publicBase)
	const apk = "app_releases/prod/3.2.0/logitrack.apk"
	// Before the copy the APK is not committed: --apply leaves its URL and says so.
	code, out, _ := h.run("rewrite-urls", "--apply")
	if code != 2 || !strings.Contains(out, "PENDING\tsettings.mobile_app.apkDownloadUrl: "+apk) || !strings.Contains(out, "0 rewritten") {
		t.Fatalf("rewrite-urls --apply before media-copy: exit %d\n%s", code, out)
	}
	// The source holds every registered object but one.
	const missing = "trucks/trk_own1/legacy.jpg"
	rows, err := h.pool.Query(h.ctx, `SELECT object_key FROM file_objects WHERE object_key <> $1`, missing)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		fake.PutObject("logitrack-legacy.appspot.com", k, "application/octet-stream", []byte("object:"+k))
	}
	out = h.mustRun("media-copy")
	if !strings.Contains(out, fmt.Sprintf("%d copied, 1 missing at source", len(keys))) {
		t.Fatalf("media-copy: %s", out)
	}
	if v := h.scalar(`SELECT string_agg(DISTINCT purpose, ',' ORDER BY purpose) FROM file_objects WHERE status = 'committed'`); v !=
		"apk,chat_image,checkin_photo,company_logo,company_signature,company_stamp,customer_logo,driver_id_card,driver_profile,"+
			"expense_receipt,incident_photo,insurance_document,leave_evidence,maintenance_file,penalty_evidence,standby_photo,"+
			"tenant_document,trip_photo,truck_document,truck_photo,truck_receipt" {
		t.Errorf("committed purposes: %v", v)
	}
	if v := h.scalar(`SELECT bucket || ' ' || visibility || ' ' || status FROM file_objects WHERE purpose = 'apk'`); v != s3cfg.PublicBucket+" public committed" {
		t.Errorf("APK row: %v", v)
	}
	if v := h.scalar(`SELECT status FROM file_objects WHERE object_key = $1`, missing); v != "missing_at_source" {
		t.Errorf("missing object: %v", v)
	}
	if out := h.mustRun("media-copy", "--verify"); !strings.Contains(out, fmt.Sprintf("%d ok, 0 mismatched", len(keys))) {
		t.Errorf("verify: %s", out)
	}
	out = h.mustRun("rewrite-urls", "--apply")
	if !strings.Contains(out, "1 rewritten") || !strings.Contains(out, "no Firebase Storage URL outside file_objects.legacy_url") {
		t.Errorf("rewrite-urls --apply: %s", out)
	}
	if v := h.scalar(`SELECT value->>'apkDownloadUrl' FROM settings WHERE key = 'mobile_app'`); v != publicBase+"/"+apk {
		t.Errorf("apkDownloadUrl: %v", v)
	}
	if out := h.mustRun("rewrite-urls", "--apply"); !strings.Contains(out, "0 rewritten") {
		t.Errorf("a second --apply: %s", out)
	}
}

// A re-home through the API (POST /v1/tenants/quarantine/rows/{table}/{id}/rehome) cannot reach schema etl: it moves the
// rows and writes tenant_rehomed; the next ETL command settles the bookkeeping (the document becomes loaded, its
// tenant_unresolved finding resolved as rehomed by the event's actor), so reconcile keeps matching.
func TestAPIRehomeSettlesTheBookkeeping(t *testing.T) {
	h := newP0Harness(t)
	h.mustRun("load", "--fixtures")
	ctx := context.Background()
	var taskID uuid.UUID
	if err := h.pool.QueryRow(ctx, `SELECT id FROM tasks WHERE legacy_doc_id = 'task_4'`).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	to := uuid.MustParse(ownFleet)
	err := db.WithSystem(ctx, h.pool, nil, func(tx pgx.Tx) error {
		moved, err := tenancy.Rehome(ctx, tx, "tasks", taskID, to)
		if err != nil {
			return err
		}
		list := make([]map[string]string, 0, len(moved))
		for _, m := range moved {
			list = append(list, map[string]string{"table": m.Table, "id": m.ID.String()})
		}
		email := "pa@example.test"
		return security.Append(ctx, tx, security.Event{EventType: "tenant_rehomed", Severity: security.SeverityInfo,
			Summary: "api re-home", TenantID: &to, ActorEmail: &email, OccurredAt: time.Now(),
			Details: map[string]any{"table": "tasks", "id": taskID.String(), "fromTenantId": tenancy.QuarantineTenantID,
				"toTenantId": ownFleet, "moved": list, "via": "api"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	h.mustRun("status")
	if v := h.scalar(`SELECT sd.status || ' ' || q.resolution || ' ' || q.resolved_by FROM etl.source_docs sd JOIN etl.quarantine q
		ON q.doc_path = sd.doc_path AND q.reason_code = 'tenant_unresolved' WHERE sd.doc_path = 'tasks/task_4'`); v != "loaded rehomed api:pa@example.test" {
		t.Errorf("settled bookkeeping: %v", v)
	}
	if out := h.mustRun("reconcile", "--fixtures"); !strings.Contains(out, "reconcile: match") {
		t.Errorf("reconcile after an API re-home: %s", out)
	}
}

// quarantine resolve --action=retry finishes the links that point forward in the load order as a load does: an
// assignment rejected while its truck sat in the quarantine tenant, retried after the truck's re-home, sets its driver's
// home truck, and a retried driver document gets the link back (review T24L).
func TestRetryRunsTheAfterHooks(t *testing.T) {
	h := newP0Harness(t)
	h.mustRun("load", "--fixtures")
	t1 := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	d := writeDump(t, t1.Add(time.Hour),
		collection{"drivers", []dump.Doc{doc("drivers/drv_x", t1, map[string]any{"firstName": "X", "lastName": "Ray", "mobile": "0810000099",
			"status": "Active", "currentAssignment": map[string]any{"assignmentId": "ta_x", "truckId": "trk_orphan"}})}},
		collection{"truckAssignment", []dump.Doc{doc("truckAssignment/ta_x", t1, map[string]any{"truckId": "trk_orphan",
			"driverId": "drv_x", "status": "active"})}})
	h.mustRun("load", "--dump", d, "--since", "watermark")
	home := func() any {
		t.Helper()
		return h.scalar(`SELECT coalesce(a.legacy_doc_id, 'null') FROM drivers d LEFT JOIN truck_assignments a ON a.id = d.current_assignment_id
			WHERE d.legacy_doc_id = 'drv_x'`)
	}
	if v := h.scalar(`SELECT sd.status || ' ' || q.reason_code FROM etl.source_docs sd JOIN etl.quarantine q ON q.doc_path = sd.doc_path
		AND q.resolved_at IS NULL WHERE sd.doc_path = 'truckAssignment/ta_x'`); v != "rejected tenant_unresolved" {
		t.Fatalf("ta_x on a quarantined truck: %v", v)
	}
	if v := home(); v != "null" {
		t.Fatalf("home truck before the retry: %v", v)
	}
	h.mustRun("quarantine", "resolve", "--collection", "trucks", "--doc", "trucks/trk_orphan", "--action", "rehome", "--tenant", ownFleet)
	if out := h.mustRun("quarantine", "resolve", "--collection", "truckAssignment", "--doc", "truckAssignment/ta_x", "--action", "retry"); !strings.Contains(out, "loaded") {
		t.Fatalf("retry: %s", out)
	}
	if v := home(); v != "ta_x" {
		t.Errorf("home truck after the retried assignment: %v, want ta_x", v)
	}
	// A retried driver document runs the assignments' hook for itself too.
	h.system(`UPDATE drivers SET current_assignment_id = NULL WHERE legacy_doc_id = 'drv_x'`)
	h.mustRun("quarantine", "resolve", "--collection", "drivers", "--doc", "drivers/drv_x", "--action", "retry")
	if v := home(); v != "ta_x" {
		t.Errorf("home truck after the retried driver: %v, want ta_x", v)
	}
	// Nothing else moved: the fixture driver keeps its home truck and its active task.
	if v := h.scalar(`SELECT a.legacy_doc_id || ' ' || k.legacy_doc_id FROM drivers d JOIN truck_assignments a ON a.id = d.current_assignment_id
		JOIN tasks k ON k.id = d.active_task_id WHERE d.legacy_doc_id = 'drv_own'`); v != "ta_1 task_1" {
		t.Errorf("drv_own: %v", v)
	}
}

// A sent or paid statement is the period lock pricing reads (R17), so the R19 guard covers it: one that cannot load
// (here an invoice number another statement holds) aborts the load with exit 2 and commits nothing, while a draft with
// the same defect is only rejected (duplicate_natural_key) (Appendix A §A.3.0, §A.3.9).
func TestSentStatementThatCannotLoadAborts(t *testing.T) {
	h := newP0Harness(t)
	h.mustRun("load", "--fixtures")
	t1 := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	statement := func(id, status string) string {
		return writeDump(t, t1.Add(time.Hour), collection{"billing_statements", []dump.Doc{doc("billing_statements/"+id, t1, map[string]any{
			"customerId": "cust_cjsf", "invoiceNumber": "CJSF-202609-001", "period": map[string]any{"month": 9.0, "year": 2026.0},
			"totalAmount": 100.0, "withholdingTax": 1.0, "netAmount": 99.0, "status": status})}})
	}
	code, _, errb := h.run("load", "--dump", statement("st_dup_sent", "sent"), "--since", "watermark")
	if code != 2 || !strings.Contains(errb, "run aborted at billing_statements/st_dup_sent (duplicate_natural_key)") {
		t.Fatalf("sent duplicate: exit %d\n%s", code, errb)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.source_docs WHERE doc_path = 'billing_statements/st_dup_sent'`); v != int64(0) {
		t.Errorf("an aborted load committed the document: %v", v)
	}
	h.mustRun("load", "--dump", statement("st_dup_draft", "draft"), "--since", "watermark")
	if v := h.scalar(`SELECT sd.status || ' ' || q.field || ' ' || q.reason_code FROM etl.source_docs sd JOIN etl.quarantine q
		ON q.doc_path = sd.doc_path AND q.resolved_at IS NULL WHERE sd.doc_path = 'billing_statements/st_dup_draft'`); v != "rejected invoiceNumber duplicate_natural_key" {
		t.Errorf("draft duplicate: %v", v)
	}
}

// Duplicate hubs fixed at source by deleting a document (reimport-hubs-dev.js): a document missing from a full dump was
// deleted at source and is no candidate any more, so the surviving duplicate takes the row over (same row id, so
// legacy references keep resolving), merged or unchanged, through a delta load and a forced one (Appendix A §A.3.3).
func TestHubDuplicateDeletedAtSource(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	hy1 := map[string]any{"source_id": "HUBY", "source_name_th": "ฮับ วาย เก่า", "source_name_en": "Hub Y", "latitude": 13.1, "longitude": 100.1}
	hy2 := map[string]any{"source_id": "HUBY", "source_name_th": "ฮับ วาย"}
	first := func(t *testing.T) (*harness, any) {
		h := newHarness(t)
		h.mustRun("load", "--dump", writeDump(t, t0.Add(time.Hour), collection{"hubs", []dump.Doc{doc("hubs/hy1", t0, hy1), doc("hubs/hy2", t0, hy2)}}))
		if v := h.scalar(`SELECT legacy_doc_id || ' ' || name_th FROM hubs WHERE source_id = 'HUBY'`); v != "hy1 ฮับ วาย เก่า" {
			t.Fatalf("first load: %v", v)
		}
		return h, h.scalar(`SELECT id::text FROM hubs WHERE source_id = 'HUBY'`)
	}
	after := func(t *testing.T, h *harness, id any, want string) {
		t.Helper()
		if v := h.scalar(`SELECT legacy_doc_id || ' ' || name_th || ' ' || (id::text = $1)::text FROM hubs WHERE source_id = 'HUBY'`, id); v != want+" true" {
			t.Errorf("hub row: %v, want %s on the same row", v, want)
		}
		if v := h.scalar(`SELECT status FROM etl.source_docs WHERE doc_path = 'hubs/hy2'`); v != "loaded" {
			t.Errorf("hubs/hy2: %v", v)
		}
	}
	t.Run("the survivor is merged at source", func(t *testing.T) {
		h, id := first(t)
		merged := map[string]any{"source_id": "HUBY", "source_name_th": "ฮับ วาย ใหม่", "source_name_en": "Hub Y", "latitude": 13.2, "longitude": 100.2}
		d := writeDump(t, t0.Add(48*time.Hour), collection{"hubs", []dump.Doc{doc("hubs/hy2", t0.Add(24*time.Hour), merged)}})
		h.mustRun("load", "--dump", d, "--since", "watermark")
		after(t, h, id, "hy2 ฮับ วาย ใหม่")
		h.mustRun("load", "--dump", d, "--force")
		after(t, h, id, "hy2 ฮับ วาย ใหม่")
	})
	t.Run("the more complete duplicate is deleted, the survivor unchanged", func(t *testing.T) {
		h, id := first(t)
		d := writeDump(t, t0.Add(48*time.Hour), collection{"hubs", []dump.Doc{doc("hubs/hy2", t0, hy2)}})
		if out := h.mustRun("load", "--dump", d, "--since", "watermark"); !strings.Contains(out, "1 document(s) applied") {
			t.Errorf("takeover load: %s", out)
		}
		after(t, h, id, "hy2 ฮับ วาย")
		if out := h.mustRun("load", "--dump", d, "--since", "watermark"); !strings.Contains(out, "0 document(s) applied") {
			t.Errorf("a second load re-applied the survivor: %s", out)
		}
		if out := h.mustRun("reconcile", "--dump", d); !strings.Contains(out, "reconcile: match") {
			t.Errorf("reconcile: %s", out)
		}
	})
}

// The winner of a duplicated source_id is the most complete document that can load (a Thai name): a richer
// English-only duplicate never keeps the hub out of the table; when no document of the source_id can load, each is
// rejected on its own and none maps to a row that does not exist (Appendix A §A.3.3).
func TestHubWinnerMustLoad(t *testing.T) {
	h := newHarness(t)
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	h.mustRun("load", "--dump", writeDump(t, t0.Add(time.Hour), collection{"hubs", []dump.Doc{
		doc("hubs/hx1", t0, map[string]any{"source_id": "HUBX", "source_name_en": "Hub X", "latitude": 13.5, "longitude": 100.5,
			"station_type": "HUB", "createdByDriver": true, "extra1": "a", "extra2": "b"}),
		doc("hubs/hx2", t0, map[string]any{"source_id": "HUBX", "source_name_th": "ฮับ เอ็กซ์"}),
		doc("hubs/hz1", t0, map[string]any{"source_id": "HUBZ", "source_name_en": "Hub Z", "latitude": 13.6}),
		doc("hubs/hz2", t0, map[string]any{"source_id": "HUBZ", "hubName": "Hub Z old"}),
	}}))
	if v := h.scalar(`SELECT legacy_doc_id || ' ' || name_th FROM hubs WHERE source_id = 'HUBX'`); v != "hx2 ฮับ เอ็กซ์" {
		t.Errorf("HUBX: %v", v)
	}
	if v := h.scalar(`SELECT sd.status || ' ' || q.reason_code || ' ' || (sd.target_id = h.id::text)::text FROM etl.source_docs sd
		JOIN etl.quarantine q ON q.doc_path = sd.doc_path JOIN hubs h ON h.source_id = 'HUBX' WHERE sd.doc_path = 'hubs/hx1'`); v != "rejected duplicate_hub_source_id true" {
		t.Errorf("hubs/hx1: %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM hubs WHERE source_id = 'HUBZ'`); v != int64(0) {
		t.Errorf("HUBZ rows: %v", v)
	}
	if v := h.scalar(`SELECT string_agg(sd.doc_path || ' ' || sd.status || ' ' || q.reason_code || ' ' || coalesce(sd.target_id, 'null'), ', '
		ORDER BY sd.doc_path) FROM etl.source_docs sd JOIN etl.quarantine q ON q.doc_path = sd.doc_path WHERE sd.doc_path LIKE 'hubs/hz%'`); v !=
		"hubs/hz1 rejected missing_required null, hubs/hz2 rejected missing_required null" {
		t.Errorf("HUBZ documents: %v", v)
	}
}

// A subcontractor company's carrier is never guessed (R11, Appendix C rule 3): the carrier whose tax id or id-card
// number is the taxId, else the one carrier with the same Thai name and no contradicting number; same-name carriers or
// a contradicting number leave the company in the quarantine tenant with tenant_unresolved (Appendix A §A.3.2).
func TestCompanyCarrierIsNeverGuessed(t *testing.T) {
	h := newHarness(t)
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sub := func(id, name, typ string, extra map[string]any) dump.Doc {
		f := map[string]any{"name": name, "type": typ, "status": "active"}
		maps.Copy(f, extra)
		return doc("subcontractors/"+id, t0, f)
	}
	co := func(id, name, taxID string) dump.Doc {
		return doc("companies/"+id, t0, map[string]any{"nameTh": name, "taxId": taxID, "branchType": "headquarters", "address": "x",
			"companyType": "subcontractor", "accountNumber": "000-" + id})
	}
	h.mustRun("load", "--dump", writeDump(t, t0.Add(time.Hour),
		collection{"subcontractors", []dump.Doc{
			sub("sub_a", "สมชาย ใจดี", "individual", map[string]any{"idCardNumber": "3100000000001"}),
			sub("sub_b", "สมชาย ใจดี", "individual", map[string]any{"idCardNumber": "3100000000002"}),
			sub("sub_c", "Charlie Co", "company", map[string]any{"taxId": "0105555000777"}),
			sub("sub_d1", "ดวงดี ขนส่ง", "individual", nil),
			sub("sub_d2", "ดวงดี ขนส่ง", "individual", nil),
			sub("sub_e", "Echo", "individual", nil),
		}},
		collection{"companies", []dump.Doc{
			co("co_b", "สมชาย ใจดี", "3100000000002"),     // sub_b by its id-card number, not sub_a by name
			co("co_n", "สมชาย ใจดี", "3100000000009"),     // both same-name carriers carry another number
			co("co_c", "Charlie Co", "0105555000888"),     // the only same-name carrier has another tax id
			co("co_c2", "Charlie Group", "0105555000777"), // sub_c by tax id
			co("co_d", "ดวงดี ขนส่ง", "0105555000555"),    // two same-name carriers without numbers
			co("co_e", "Echo", "0105555000444"),           // one same-name carrier without a number
		}}))
	if v := h.scalar(`SELECT string_agg(c.legacy_doc_id || ' ' || coalesce(t.legacy_doc_id, 'Q') || ' ' || c.tenant_source, ', ' ORDER BY c.legacy_doc_id)
		FROM companies c JOIN tenants t ON t.id = c.tenant_id`); v !=
		"co_b sub_b self, co_c Q quarantine, co_c2 sub_c self, co_d Q quarantine, co_e sub_e self, co_n Q quarantine" {
		t.Errorf("company tenants: %v", v)
	}
	if v := h.scalar(`SELECT string_agg(doc_path || ': ' || split_part(detail, ';', 1), ' | ' ORDER BY doc_path) FROM etl.quarantine
		WHERE collection = 'companies' AND reason_code = 'tenant_unresolved' AND field IS NULL AND resolved_at IS NULL`); v !=
		"companies/co_c: a carrier tenant has this Thai name but another tax id or id-card number than the company's taxId | "+
			"companies/co_d: several carrier tenants match this taxId or Thai name | "+
			"companies/co_n: a carrier tenant has this Thai name but another tax id or id-card number than the company's taxId" {
		t.Errorf("tenant_unresolved findings: %v", v)
	}
}
