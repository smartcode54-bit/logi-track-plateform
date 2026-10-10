//go:build integration

// Acceptance tests of issue T15 on postgres:18-alpine (and the compose MinIO for media-copy): the fixture dump of
// testdata/firestore-fixtures loads twice with no change the second time, its dry-run report and the database
// after the load both equal the committed quarantine snapshot, billable trips are never dropped, reconcile exits 0
// and 2 on an injected mismatch, export-back round-trips a class A customer through an in-process Firestore, and
// the production order of the CI job etl-fixtures holds: load at `up-to 9` + `apply 11`, then `up` applies
// 0010_d5_unique_constraints after the owner's loser correction (R31, R59, R88).
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

var update = flag.Bool("update", false, "rewrite testdata/firestore-fixtures/quarantine.snapshot from the dry run")

func TestMain(m *testing.M) {
	flag.Parse()
	code := pgtest.Main(m)
	storagetest.Terminate()
	os.Exit(code)
}

const (
	ownFleet     = "01900000-0000-7000-8000-000000000001"
	fixtureDir   = "testdata/firestore-fixtures"
	snapshotFile = "testdata/firestore-fixtures/quarantine.snapshot"
)

type harness struct {
	t      *testing.T
	d      *pgtest.Database
	runner *migrate.Runner
	pool   *pgxpool.Pool // logitrack_etl
	extra  []string
	deps   deps
	ctx    context.Context
}

// newHarness migrates a fresh database the way production does before the P1 load: up-to 9, then apply 11.
func newHarness(t *testing.T) *harness {
	t.Helper()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	ctx := context.Background()
	if _, err := r.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply(ctx, 11); err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, d: d, runner: r, pool: d.Pool(t, db.RoleETL), ctx: ctx,
		deps: deps{now: func() time.Time { return time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) }}}
}

func (h *harness) env() []string {
	return append([]string{"APP_ENV=local", "LOG_LEVEL=warn", "LOG_FORMAT=json", "ETL_DATABASE_URL=" + h.d.URL(db.RoleETL),
		"OWN_FLEET_TENANT_ID=" + ownFleet, "S3_BUCKET=logitrack"}, h.extra...)
}

func (h *harness) run(args ...string) (int, string, string) {
	h.t.Helper()
	var out, errb bytes.Buffer
	code := run(h.ctx, args, h.env(), &out, &errb, h.deps)
	return code, out.String(), errb.String()
}

func (h *harness) mustRun(args ...string) string {
	h.t.Helper()
	code, out, errb := h.run(args...)
	if code != 0 {
		h.t.Fatalf("etl %v: exit %d\nstdout:\n%s\nstderr:\n%s", args, code, out, errb)
	}
	return out
}

func (h *harness) engine() *etl.Engine {
	h.t.Helper()
	e, err := etl.New(etl.Config{Tx: systemTx(h.pool), OwnFleetTenantID: uuid.MustParse(ownFleet), Bucket: "logitrack", Log: zerolog.Nop()})
	if err != nil {
		h.t.Fatal(err)
	}
	return e
}

func (h *harness) scalar(sql string, args ...any) any {
	h.t.Helper()
	var v any
	if err := h.pool.QueryRow(h.ctx, sql, args...).Scan(&v); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

// rowVersions fingerprints every row of schemas public and etl by (xmin, ctid): an UPDATE that writes the same
// values still changes both, so equal fingerprints mean no row was inserted, updated or deleted.
func (h *harness) rowVersions() map[string]string {
	h.t.Helper()
	rows, err := h.pool.Query(h.ctx, `SELECT n.nspname || '.' || c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname IN ('public', 'etl') AND c.relkind = 'r' AND c.relname <> 'goose_db_version' ORDER BY 1`)
	if err != nil {
		h.t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			h.t.Fatal(err)
		}
		tables = append(tables, s)
	}
	out := map[string]string{}
	for _, tbl := range tables {
		var n int64
		var fp string
		if err := h.pool.QueryRow(h.ctx, `SELECT count(*), coalesce(string_agg(xmin::text || ctid::text, ',' ORDER BY ctid), '') FROM `+tbl).Scan(&n, &fp); err != nil {
			h.t.Fatalf("%s: %v", tbl, err)
		}
		out[tbl] = fmt.Sprintf("%d:%s", n, fp)
	}
	return out
}

func readSnapshot(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(snapshotFile)
	if err != nil {
		t.Fatalf("%v (run the test once with -update)", err)
	}
	return string(b)
}

func sectionsText(t *testing.T, s etl.ReportSections) string {
	t.Helper()
	var b bytes.Buffer
	if err := s.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestFixtureLoad is the core acceptance test of T15.
func TestFixtureLoad(t *testing.T) {
	h := newHarness(t)
	before := h.rowVersions()

	// Dry run: the report equals the snapshot and nothing is committed.
	report := filepath.Join(t.TempDir(), "report.tsv")
	h.mustRun("load", "--dump", fixtureDir, "--dry-run", "--report", report)
	got, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(snapshotFile, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if snap := readSnapshot(t); string(got) != snap {
		t.Fatalf("dry-run report differs from %s:\n%s", snapshotFile, diff(snap, string(got)))
	}
	if after := h.rowVersions(); !reflect.DeepEqual(before, after) {
		t.Fatal("a dry run changed rows")
	}

	// Real load: the database state is the same report.
	out := h.mustRun("load", "--fixtures")
	if !strings.Contains(out, "(committed)") {
		t.Fatalf("load output: %s", out)
	}
	state, err := h.engine().QuarantineReport(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if text := sectionsText(t, state); text != readSnapshot(t) {
		t.Fatalf("database after the load differs from the snapshot:\n%s", diff(readSnapshot(t), text))
	}

	t.Run("every emitted reason code is in Appendix A §A.3.0", func(t *testing.T) {
		rows, err := h.pool.Query(h.ctx, `SELECT DISTINCT reason_code FROM etl.quarantine`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var code string
			if err := rows.Scan(&code); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(etl.Reasons, etl.Reason(code)) {
				t.Errorf("reason %q is not in the catalog", code)
			}
		}
	})

	t.Run("every tenant-stamped row carries a tenant_source of the CHECK list", func(t *testing.T) {
		rows, err := h.pool.Query(h.ctx, `SELECT table_name::text FROM information_schema.columns
			WHERE table_schema = 'public' AND column_name = 'tenant_source' ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		var tables []string
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			tables = append(tables, s)
		}
		seen := map[string]bool{}
		for _, tbl := range tables {
			r, err := h.pool.Query(h.ctx, `SELECT DISTINCT tenant_source FROM `+tbl)
			if err != nil {
				t.Fatal(err)
			}
			for r.Next() {
				var s string
				_ = r.Scan(&s)
				seen[s] = true
				if !slices.Contains(tenancy.Sources, tenancy.Source(s)) {
					t.Errorf("%s: tenant_source %q", tbl, s)
				}
			}
		}
		for _, want := range []string{"self", "driver", "task", "trip", "truck", "form", "quarantine"} {
			if !seen[want] {
				t.Errorf("the fixtures exercise no %q row", want)
			}
		}
	})

	t.Run("a trip without createdAt and a photo of unknown type load, flagged, not dropped", func(t *testing.T) {
		if v := h.scalar(`SELECT created_at = std FROM trip_records WHERE legacy_doc_id = 'TRIP002'`); v != true {
			t.Error("TRIP002: created_at must be std")
		}
		if v := h.scalar(`SELECT created_at = '2026-09-04T05:00:00Z'::timestamptz FROM trip_records WHERE legacy_doc_id = 'TRIP003'`); v != true {
			t.Error("TRIP003: created_at must be the Firestore create time")
		}
		if v := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE doc_path IN ('trip_records/TRIP002', 'trip_records/TRIP003')
			AND reason_code = 'created_at_derived'`); v != int64(2) {
			t.Errorf("created_at_derived findings: %v", v)
		}
		if v := h.scalar(`SELECT count(*) FROM trip_photos p JOIN trip_records r ON r.id = p.trip_id
			WHERE r.legacy_doc_id = 'TRIP001' AND p.photo_type = 'selfie_with_truck' AND NOT p.photo_type_known`); v != int64(1) {
			t.Error("the unknown photo type must load with photo_type_known = false")
		}
		if v := h.scalar(`SELECT count(*) FROM trip_photos p JOIN trip_records r ON r.id = p.trip_id WHERE r.legacy_doc_id = 'TRIP001'`); v != int64(3) {
			t.Errorf("TRIP001 photos: %v", v)
		}
		if v := h.scalar(`SELECT estimate_thb::text FROM trip_billing_snapshots s JOIN trip_records r ON r.id = s.trip_id
			WHERE r.legacy_doc_id = 'TRIP001'`); v != "1234.50" {
			t.Errorf("TRIP001 snapshot: %v", v)
		}
		if v := h.scalar(`SELECT count(*) FROM file_objects WHERE status = 'missing_at_source' AND storage_backend = 's3'
			AND legacy_url NOT LIKE '%token=%'`); v.(int64) < 8 {
			t.Errorf("registered legacy objects: %v", v)
		}
	})

	t.Run("the second load changes 0 rows", func(t *testing.T) {
		before := h.rowVersions()
		out := h.mustRun("load", "--dump", fixtureDir)
		if !strings.Contains(out, "0 document(s) applied") {
			t.Errorf("second load output:\n%s", out)
		}
		if after := h.rowVersions(); !reflect.DeepEqual(before, after) {
			for k := range before {
				if before[k] != after[k] {
					t.Errorf("%s changed", k)
				}
			}
		}
	})

	t.Run("reconcile exits 0 on a match and 2 on an injected mismatch", func(t *testing.T) {
		out := h.mustRun("reconcile", "--fixtures", "--out", t.TempDir())
		if !strings.Contains(out, "reconcile: match") {
			t.Fatalf("reconcile: %s", out)
		}
		if _, err := h.pool.Exec(h.ctx, `UPDATE trip_billing_snapshots SET estimate_thb = estimate_thb + 1
			WHERE trip_id = (SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP001')`); err != nil {
			t.Fatal(err)
		}
		code, out, errb := h.run("reconcile", "--fixtures")
		if code != 2 || !strings.Contains(out, "trip_money") {
			t.Fatalf("injected mismatch: exit %d\n%s\n%s", code, out, errb)
		}
		if _, err := h.pool.Exec(h.ctx, `UPDATE trip_billing_snapshots SET estimate_thb = estimate_thb - 1
			WHERE trip_id = (SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP001')`); err != nil {
			t.Fatal(err)
		}
		if _, err := h.pool.Exec(h.ctx, `DELETE FROM incident_reports WHERE legacy_doc_id = 'inc2'`); err != nil {
			t.Fatal(err)
		}
		if code, out, _ := h.run("reconcile", "--fixtures"); code != 2 || !strings.Contains(out, "targets\tincident_reports") {
			t.Fatalf("a missing row must be a mismatch: exit %d\n%s", code, out)
		}
		runs := h.scalar(`SELECT count(*) FILTER (WHERE outcome = 'mismatch') FROM etl.reconciliation_runs`)
		if runs != int64(2) {
			t.Errorf("reconciliation_runs mismatches: %v", runs)
		}
	})

	t.Run("goose up applies 0010 after the owner's loser correction (R88)", func(t *testing.T) {
		// D5: the duplicate fuel receipt loaded unchanged with duplicate_natural_key; the owner keeps exp1.
		if v := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE doc_path = 'vehicle_expenses/exp2' AND reason_code = 'duplicate_natural_key'`); v != int64(1) {
			t.Fatalf("duplicate_natural_key on exp2: %v", v)
		}
		if _, err := h.pool.Exec(h.ctx, `UPDATE vehicle_expenses SET tax_inv_id = NULL WHERE legacy_doc_id = 'exp2'`); err != nil {
			t.Fatal(err)
		}
		if _, err := h.runner.Up(h.ctx); err != nil {
			t.Fatalf("goose up after the correction: %v", err)
		}
		if v := h.scalar(`SELECT count(*) FROM pg_indexes WHERE indexname IN ('vehicle_expenses_fuel_taxinv', 'chats_one_open_per_driver',
			'customer_service_fees_one_per_type')`); v != int64(3) {
			t.Errorf("D5 indexes: %v", v)
		}
	})
}

// diff shows the lines only one side has.
func diff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for _, l := range w {
		if !slices.Contains(g, l) {
			b.WriteString("- " + l + "\n")
		}
	}
	for _, l := range g {
		if !slices.Contains(w, l) {
			b.WriteString("+ " + l + "\n")
		}
	}
	return b.String()
}

// A billable document is never rejected (R19): the run aborts with exit 2 and commits nothing.
func TestBillableRejectionAbortsTheRun(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	w, err := dump.NewWriter(dir, at, "p", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCollection("trip_records", false, []dump.Doc{{ID: "T1", Path: "trip_records/T1", UpdateTime: at,
		Fields: map[string]any{"status": "loading", "jobType": "first_mile", "billingEstimateThb": 100.0}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	code, _, errb := h.run("load", "--dump", dir)
	if code != 2 || !strings.Contains(errb, "billing evidence") {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.source_docs`); v != int64(0) {
		t.Fatalf("an aborted load committed %v source docs", v)
	}
}

// OWN_FLEET_TENANT_ID is required (R56) and the login must be logitrack_etl (R66).
func TestConfigurationRefusals(t *testing.T) {
	h := newHarness(t)
	var out, errb bytes.Buffer
	env := []string{"APP_ENV=local", "ETL_DATABASE_URL=" + h.d.URL(db.RoleETL)}
	if code := run(h.ctx, []string{"status"}, env, &out, &errb, deps{}); code != 2 || !strings.Contains(errb.String(), "OWN_FLEET_TENANT_ID") {
		t.Fatalf("without OWN_FLEET_TENANT_ID: exit %d %s", code, errb.String())
	}
	errb.Reset()
	env = []string{"APP_ENV=local", "ETL_DATABASE_URL=" + h.d.URL(db.RoleApp), "OWN_FLEET_TENANT_ID=" + ownFleet}
	if code := run(h.ctx, []string{"status"}, env, &out, &errb, deps{}); code != 2 || !strings.Contains(errb.String(), "logitrack_etl") {
		t.Fatalf("as logitrack_app: exit %d %s", code, errb.String())
	}
	h.mustRun("load", "--fixtures", "--collections", "subcontractors")
	errb.Reset()
	env = []string{"APP_ENV=local", "ETL_DATABASE_URL=" + h.d.URL(db.RoleETL), "OWN_FLEET_TENANT_ID=" + uuid.NewString()}
	if code := run(h.ctx, []string{"load", "--fixtures"}, env, &out, &errb, deps{}); code != 2 || !strings.Contains(errb.String(), "own-fleet") {
		t.Fatalf("another own-fleet id: exit %d %s", code, errb.String())
	}
}

// export-back round-trips a class A fixture: the customers document written to Firestore equals the dumped one.
func TestExportBackRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.mustRun("load", "--fixtures")
	fake := gcptest.New(t, "logitrack-fixtures")
	key := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(key, fake.ServiceAccountJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	h.extra = []string{"ETL_FIRESTORE_PROJECT_ID=logitrack-fixtures", "GOOGLE_APPLICATION_CREDENTIALS=" + key}
	h.deps.google = fake.Client()
	fixture, err := dump.OpenDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	customers, err := fixture.Read("customers")
	if err != nil {
		t.Fatal(err)
	}
	freeze := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	byID := map[string]dump.Doc{}
	for _, c := range customers {
		c.UpdateTime = freeze.Add(-time.Hour) // unchanged in Firestore since the freeze
		fake.Put(c)
		byID[c.ID] = c
	}
	changed := byID["cust_spx"]
	changed.UpdateTime = freeze.Add(time.Hour) // edited in Firestore after the freeze
	fake.Put(changed)

	code, out, errb := h.run("export-back", "--collection", "customers", "--frozen-at", freeze.Format(time.RFC3339))
	if code != 2 || !strings.Contains(out, "REFUSED\tcustomers/cust_spx") {
		t.Fatalf("a document changed after the freeze must be refused: exit %d\n%s\n%s", code, out, errb)
	}
	got, _ := fake.Doc("customers/cust_cjsf")
	if !reflect.DeepEqual(got.Fields, byID["cust_cjsf"].Fields) {
		t.Fatalf("round trip of customers/cust_cjsf:\n got %#v\nwant %#v", got.Fields, byID["cust_cjsf"].Fields)
	}
	if _, ok := fake.Doc("customers/cust_zdup"); !ok {
		t.Fatal("the fixture's rejected duplicate stays in Firestore untouched")
	}
	h.mustRun("export-back", "--collection", "customers", "--frozen-at", freeze.Format(time.RFC3339), "--overwrite-after-freeze")
	spx, _ := fake.Doc("customers/cust_spx")
	if spx.Fields["code"] != "SPX" || spx.Fields["name"] != "Shopee Express" || spx.Fields["branchType"] != "สำนักงานใหญ่" ||
		spx.Fields["billingDateBasis"] != "delivered" || !strings.Contains(spx.Fields["logoUrl"].(string), "customers%2Fcust_spx%2Flogo.png") {
		t.Fatalf("cust_spx in the legacy shape: %#v", spx.Fields)
	}
	if strings.Contains(spx.Fields["logoUrl"].(string), "token=") {
		t.Error("export-back never writes a download token")
	}
}

// dump reads Firestore (collections and a collection group) into a dump that loads.
func TestDumpThenLoad(t *testing.T) {
	h := newHarness(t)
	fake := gcptest.New(t, "logitrack-fixtures")
	fixture, err := dump.OpenDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"subcontractors", "drivers", "trucks"} {
		docs, err := fixture.Read(n)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range docs {
			fake.Put(d)
		}
	}
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	fake.Put(dump.Doc{ID: "i1", Path: "drivers/drv_own/mobile_installations/i1", CreateTime: at, UpdateTime: at, Fields: map[string]any{"appVersion": "3.2.0"}})
	key := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(key, fake.ServiceAccountJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	h.extra = []string{"ETL_FIRESTORE_PROJECT_ID=logitrack-fixtures", "GOOGLE_APPLICATION_CREDENTIALS=" + key}
	h.deps.google = fake.Client()
	out := filepath.Join(t.TempDir(), "dump")
	h.mustRun("dump", "--collections", "all", "--groups", "mobile_installations", "--out", out)
	d, err := dump.OpenDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Names(), []string{"drivers", "subcontractors", "trucks", "mobile_installations"}) {
		t.Fatalf("dumped collections %v", d.Names())
	}
	h.mustRun("load", "--dump", out)
	if v := h.scalar(`SELECT count(*) FROM drivers`); v != int64(3) {
		t.Errorf("drivers loaded: %v", v)
	}
	if v := h.scalar(`SELECT status FROM etl.source_docs WHERE doc_path = 'drivers/drv_own/mobile_installations/i1'`); v != "pending" {
		t.Errorf("mobile_installations (T24) must be recorded pending: %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.watermarks`); v.(int64) < 3 {
		t.Errorf("watermarks: %v", v)
	}
}

// --since=watermark applies only documents after each collection's watermark; quarantine resolve re-homes a row
// with its children, retries and skips.
func TestWatermarksAndQuarantineResolve(t *testing.T) {
	h := newHarness(t)
	h.mustRun("load", "--fixtures")

	dir := t.TempDir()
	at := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC) // equal to the fixture updateTime: before the watermark
	w, err := dump.NewWriter(dir, at.Add(48*time.Hour), "p", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCollection("customers", false, []dump.Doc{
		{ID: "cust_spx", Path: "customers/cust_spx", UpdateTime: at, Fields: map[string]any{"code": "SPX", "name": "stale"}},
		{ID: "cust_new", Path: "customers/cust_new", UpdateTime: at.Add(time.Hour), Fields: map[string]any{"code": "NEW", "name": "New"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := h.mustRun("load", "--dump", dir, "--since", "watermark")
	if !strings.Contains(out, "1 document(s) applied") {
		t.Fatalf("delta load: %s", out)
	}
	if v := h.scalar(`SELECT name FROM customers WHERE legacy_doc_id = 'cust_spx'`); v != "Shopee Express" {
		t.Errorf("a document at the watermark must not be applied: %v", v)
	}

	// task_4 is driverless (D6): re-home it to the own fleet.
	h.mustRun("quarantine", "resolve", "--collection", "tasks", "--doc", "tasks/task_4", "--action", "rehome", "--tenant", ownFleet, "--by", "owner")
	if v := h.scalar(`SELECT tenant_id::text || ' ' || tenant_source FROM tasks WHERE legacy_doc_id = 'task_4'`); v != ownFleet+" form" {
		t.Errorf("re-homed task: %v", v)
	}
	if v := h.scalar(`SELECT resolution FROM etl.quarantine WHERE doc_path = 'tasks/task_4' AND reason_code = 'tenant_unresolved'`); v != "rehomed" {
		t.Errorf("finding resolution: %v", v)
	}
	if v := h.scalar(`SELECT status FROM etl.source_docs WHERE doc_path = 'tasks/task_4'`); v != "loaded" {
		t.Errorf("source doc status: %v", v)
	}
	// TRIP004 cannot be re-homed into the quarantine tenant itself.
	if code, _, _ := h.run("quarantine", "resolve", "--collection", "trip_records", "--doc", "trip_records/TRIP004", "--action", "rehome",
		"--tenant", tenancy.QuarantineTenantID); code == 0 {
		t.Error("re-homing into the quarantine tenant must fail")
	}
	// skip accepts the findings of a document; retry re-applies it from etl.source_docs.raw.
	h.mustRun("quarantine", "resolve", "--collection", "trip_records", "--doc", "trip_records/TRIP001", "--action", "skip")
	if v := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE doc_path = 'trip_records/TRIP001' AND resolved_at IS NULL`); v != int64(0) {
		t.Errorf("skip left %v open findings", v)
	}
	out = h.mustRun("quarantine", "resolve", "--collection", "trip_records", "--doc", "trip_records/TRIP003", "--action", "retry")
	if !strings.Contains(out, "outcome loaded") {
		t.Errorf("retry: %s", out)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE doc_path = 'trip_records/TRIP003' AND resolution = 'retried'`); v.(int64) == 0 {
		t.Error("retry must resolve the old findings as retried")
	}
	out = h.mustRun("quarantine", "list", "--collection", "trip_records", "--reason", "task_ambiguous")
	if !strings.Contains(out, "trip_records/TRIP003") {
		t.Errorf("list: %s", out)
	}
	out = h.mustRun("status")
	if !strings.Contains(out, "trip_records") || !strings.Contains(out, "2026-09-02T00:00:00Z") {
		t.Errorf("status: %s", out)
	}
}

// media-copy copies registered objects from GCS into MinIO under the identical key and commits them; an object GCS
// lacks stays missing_at_source with a finding; --verify re-checks the copies; rewrite-urls finds no stray URL.
func TestMediaCopy(t *testing.T) {
	h := newHarness(t)
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
	h.extra = []string{"ETL_GCS_BUCKET=logitrack-legacy.appspot.com", "GOOGLE_APPLICATION_CREDENTIALS=" + key,
		"S3_ENDPOINT=" + s3cfg.Endpoint, "S3_REGION=" + s3cfg.Region, "S3_ACCESS_KEY_ID=" + s3cfg.AccessKeyID,
		"S3_SECRET_ACCESS_KEY=" + s3cfg.SecretAccessKey, "S3_BUCKET=" + s3cfg.Bucket}
	h.deps.google = fake.Client()
	h.mustRun("load", "--fixtures", "--collections", "subcontractors,customers,trucks,drivers,tasks,trip_records")
	rows, err := h.pool.Query(h.ctx, `SELECT object_key FROM file_objects WHERE object_key <> 'trip_records/TRIP001/odd.jpg'`)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		fake.PutObject("logitrack-legacy.appspot.com", k, "image/jpeg", []byte("jpeg:"+k))
		n++
	}
	out := h.mustRun("media-copy")
	if !strings.Contains(out, fmt.Sprintf("%d copied, 1 missing at source", n)) {
		t.Fatalf("media-copy: %s", out)
	}
	if v := h.scalar(`SELECT status FROM file_objects WHERE object_key = 'trip_records/TRIP001/odd.jpg'`); v != "missing_at_source" {
		t.Errorf("missing object: %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE collection = 'file_objects' AND reason_code = 'file_missing_at_source'`); v != int64(1) {
		t.Errorf("file_missing_at_source findings: %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM file_objects WHERE status = 'committed' AND sha256 IS NOT NULL AND size_bytes > 0`); v != int64(n) {
		t.Errorf("committed rows: %v", v)
	}
	out = h.mustRun("media-copy", "--verify")
	if !strings.Contains(out, fmt.Sprintf("%d ok, 0 mismatched", n)) {
		t.Errorf("verify: %s", out)
	}
	if code, out, _ := h.run("rewrite-urls"); code != 0 {
		t.Errorf("rewrite-urls: exit %d %s", code, out)
	}
}

// dump --out=s3 writes S3_BUCKET etl/dumps/{ts}/ (R74) and load --dump=s3:… reads it back.
func TestDumpToS3(t *testing.T) {
	h := newHarness(t)
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
	fixture, err := dump.OpenDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	customers, err := fixture.Read("customers")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range customers {
		fake.Put(c)
	}
	key := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(key, fake.ServiceAccountJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	h.extra = []string{"ETL_FIRESTORE_PROJECT_ID=logitrack-fixtures", "GOOGLE_APPLICATION_CREDENTIALS=" + key,
		"S3_ENDPOINT=" + s3cfg.Endpoint, "S3_REGION=" + s3cfg.Region, "S3_ACCESS_KEY_ID=" + s3cfg.AccessKeyID,
		"S3_SECRET_ACCESS_KEY=" + s3cfg.SecretAccessKey, "S3_BUCKET=" + s3cfg.Bucket}
	h.deps.google = fake.Client()
	out := h.mustRun("dump", "--collections", "customers", "--out", "s3")
	const prefix = "etl/dumps/20261002T000000Z/"
	if !strings.Contains(out, "dump written: s3:"+prefix) {
		t.Fatalf("dump: %s", out)
	}
	out = h.mustRun("load", "--dump", "s3:"+prefix)
	if !strings.Contains(out, "3 document(s) applied") {
		t.Fatalf("load from s3: %s", out)
	}
	if code, _, _ := h.run("load", "--dump", "s3:documents/x/"); code != 2 {
		t.Error("an s3 dump outside etl/dumps/ must be refused")
	}
}
