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
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
	// Unchanged since the freeze but holding stale content, plus a field the encoder does not own: the rollback
	// must replace the owned fields from PostgreSQL and keep the other one (update mask).
	stale := byID["cust_cjsf"]
	stale.Fields = map[string]any{"code": "STALE", "name": "stale before rollback", "legacyExtra": "kept"}
	fake.Put(stale)

	code, out, errb := h.run("export-back", "--collection", "customers", "--frozen-at", freeze.Format(time.RFC3339))
	if code != 2 || !strings.Contains(out, "REFUSED\tcustomers/cust_spx") {
		t.Fatalf("a document changed after the freeze must be refused: exit %d\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "export-back: 1 written, 1 refused") {
		t.Fatalf("summary line: %s", out)
	}
	if w := fake.Writes(); len(w) != 1 || w[0].Path != "customers/cust_cjsf" {
		t.Fatalf("writes: %#v", w)
	}
	got, _ := fake.Doc("customers/cust_cjsf")
	if !got.UpdateTime.After(freeze) {
		t.Fatalf("customers/cust_cjsf was not written: updateTime %v", got.UpdateTime)
	}
	want := maps.Clone(byID["cust_cjsf"].Fields)
	want["legacyExtra"] = "kept"
	if !reflect.DeepEqual(got.Fields, want) {
		t.Fatalf("round trip of customers/cust_cjsf:\n got %#v\nwant %#v", got.Fields, want)
	}
	if zdup, ok := fake.Doc("customers/cust_zdup"); !ok || !reflect.DeepEqual(zdup.Fields, byID["cust_zdup"].Fields) {
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
	// The re-home settles the tenant only: the hub finding of task_4 stays open.
	if v := h.scalar(`SELECT resolution IS NULL FROM etl.quarantine WHERE doc_path = 'tasks/task_4' AND field = 'sourceHub'
		AND reason_code = 'hub_unresolved'`); v != true {
		t.Error("a re-home must leave the hub_unresolved finding open")
	}
	// No audit, no change: one tenant_rehomed event, committed with the move (R22, Appendix C §C.4.13).
	rehomed := func() int64 {
		return h.scalar(`SELECT count(*) FROM security_events WHERE event_type = 'tenant_rehomed'`).(int64)
	}
	if v := h.scalar(`SELECT count(*) FROM security_events WHERE event_type = 'tenant_rehomed' AND tenant_id = $1
		AND actor_user_id IS NULL AND details->>'table' = 'tasks' AND details->>'by' = 'owner'
		AND details->>'fromTenantId' = $2 AND details->>'docPath' = 'tasks/task_4' AND jsonb_array_length(details->'moved') = 1`,
		ownFleet, tenancy.QuarantineTenantID); v != int64(1) || rehomed() != 1 {
		t.Errorf("tenant_rehomed events: %v of %d", v, rehomed())
	}
	// TRIP004 cannot be re-homed into the quarantine tenant itself, and the refused move writes no event.
	if code, _, _ := h.run("quarantine", "resolve", "--collection", "trip_records", "--doc", "trip_records/TRIP004", "--action", "rehome",
		"--tenant", tenancy.QuarantineTenantID); code == 0 {
		t.Error("re-homing into the quarantine tenant must fail")
	}
	if rehomed() != 1 {
		t.Errorf("a refused re-home wrote an event: %d", rehomed())
	}
	// Re-homed to the own fleet, TRIP004 keeps its driver and trip-number findings open.
	out = h.mustRun("quarantine", "resolve", "--collection", "trip_records", "--doc", "trip_records/TRIP004", "--action", "rehome",
		"--tenant", ownFleet)
	if !strings.Contains(out, "1 finding(s) resolved, 1 row(s) and 0 file(s) re-homed") {
		t.Errorf("rehome TRIP004: %s", out)
	}
	if v := h.scalar(`SELECT string_agg(coalesce(field, '-') || ' ' || reason_code || ' ' || coalesce(resolution, 'open'), ', '
		ORDER BY field NULLS FIRST) FROM etl.quarantine WHERE doc_path = 'trip_records/TRIP004'`); v !=
		"- tenant_unresolved rehomed, driverId driver_unresolved open, spxTripId trip_no_mismatch open" {
		t.Errorf("TRIP004 findings: %v", v)
	}
	if rehomed() != 2 {
		t.Errorf("tenant_rehomed events after TRIP004: %d", rehomed())
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

// collection is one collection of a hand-made dump.
type collection struct {
	name string
	docs []dump.Doc
}

// writeDump writes a dump of the collections and returns its directory.
func writeDump(t *testing.T, exportedAt time.Time, colls ...collection) string {
	t.Helper()
	dir := t.TempDir()
	w, err := dump.NewWriter(dir, exportedAt, "p", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range colls {
		if err := w.WriteCollection(c.name, false, c.docs); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func doc(path string, ut time.Time, fields map[string]any) dump.Doc {
	return dump.Doc{ID: path[strings.LastIndexByte(path, '/')+1:], Path: path, CreateTime: ut.Add(-time.Hour), UpdateTime: ut, Fields: fields}
}

// --since=watermark looks back 10 minutes (main spec §13.7): an update that committed while an earlier dump was
// being read, older than that dump's newest document, is applied by the next delta load (review T15).
func TestWatermarkOverlap(t *testing.T) {
	h := newHarness(t)
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	d1 := writeDump(t, t0.Add(time.Hour), collection{"customers", []dump.Doc{
		doc("customers/ca", t0, map[string]any{"code": "CA", "name": "A old"}),
		doc("customers/cb", t0.Add(5*time.Minute), map[string]any{"code": "CB", "name": "B"}),
	}})
	h.mustRun("load", "--dump", d1)
	d2 := writeDump(t, t0.Add(2*time.Hour), collection{"customers", []dump.Doc{
		doc("customers/ca", t0.Add(3*time.Minute), map[string]any{"code": "CA", "name": "A updated"}), // missed by d1
		doc("customers/cb", t0.Add(5*time.Minute), map[string]any{"code": "CB", "name": "B"}),
		doc("customers/cc", t0.Add(-11*time.Minute), map[string]any{"code": "CC", "name": "C"}), // before the look-back
	}})
	out := h.mustRun("load", "--dump", d2, "--since", "watermark")
	if !strings.Contains(out, "1 document(s) applied") {
		t.Fatalf("delta load: %s", out)
	}
	if !regexp.MustCompile(`customers\s+3\s+1\s+1\s+1\s`).MatchString(out) {
		t.Errorf("want 3 docs: 1 unchanged inside the look-back, 1 before the mark, 1 loaded:\n%s", out)
	}
	if v := h.scalar(`SELECT name FROM customers WHERE legacy_doc_id = 'ca'`); v != "A updated" {
		t.Errorf("the update inside the look-back was lost: %v", v)
	}
}

// Pending documents (a collection a later task maps) never move the watermark, so the first --since=watermark load
// that maps the collection still applies them (review T15; the mapping side is covered in internal/etl).
func TestPendingDocumentsLeaveNoWatermark(t *testing.T) {
	h := newHarness(t)
	h.mustRun("load", "--fixtures")
	if v := h.scalar(`SELECT status FROM etl.source_docs WHERE doc_path = 'settings/tenancy'`); v != "pending" {
		t.Fatalf("settings/tenancy: %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.watermarks WHERE collection IN ('settings', 'checkin', 'legacy_tmp')`); v != int64(2) {
		t.Errorf("only the dropped and unknown collections may have a watermark, not the pending one: %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.watermarks WHERE collection = 'settings'`); v != int64(0) {
		t.Errorf("a pending collection moved the watermark: %v", v)
	}
}

// A re-applied row keeps its tenant (main spec §13.5): a platform admin's re-home is never undone by a delta load,
// --force or retry, broker drift is reported and not moved, a re-linked child keeps its tenant and drops the link,
// and a quarantined parent whose chain now resolves (a fix at source) takes its children and files along
// (review T15).
func TestReapplyKeepsTheTenant(t *testing.T) {
	h := newHarness(t)
	h.mustRun("load", "--fixtures")
	t1 := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	storageURL := func(key string) string {
		return "https://firebasestorage.googleapis.com/v0/b/logitrack-legacy.appspot.com/o/" + url.PathEscape(key) + "?alt=media&token=t"
	}
	task := func(id string, ut time.Time, extra map[string]any) dump.Doc {
		f := map[string]any{"taskId": "FM-" + id, "taskType": "FIRST_MILE", "status": "Pending",
			"date": dump.Timestamp{Time: time.Date(2026, 9, 9, 2, 0, 0, 0, time.UTC)}, "sourceHub": "SOCE", "destination": "SOCN",
			"checkInPhotoUrl": storageURL("tasks/" + id + "/checkin.jpg")}
		maps.Copy(f, extra)
		return doc("tasks/"+id, ut, f)
	}
	trip := func(id, taskID string, ut time.Time) dump.Doc {
		return doc("trip_records/"+id, ut, map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": taskID,
			"driverId": "uid_own", "createdAt": dump.Timestamp{Time: t1},
			"photos": []any{map[string]any{"type": "seal", "url": storageURL("trip_records/" + id + "/seal.jpg")}}})
	}
	d1 := writeDump(t, t1.Add(time.Hour),
		collection{"tasks", []dump.Doc{task("task_sec", t1, nil), task("task_fix", t1, nil), task("task_lone", t1, nil)}},
		collection{"trip_records", []dump.Doc{trip("TRIPSEC", "task_sec", t1), trip("TRIPFIX", "task_fix", t1)}})
	h.mustRun("load", "--dump", d1)
	tenantOf := func(table, legacyID string) string {
		return h.scalar(`SELECT tenant_id::text || ' ' || tenant_source FROM `+table+` WHERE legacy_doc_id = $1`, legacyID).(string)
	}
	fileTenant := func(key string) string {
		return h.scalar(`SELECT tenant_id::text FROM file_objects WHERE object_key = $1`, key).(string)
	}
	quarantine := tenancy.QuarantineTenantID + " quarantine"
	for _, x := range [][2]string{{"tasks", "task_sec"}, {"tasks", "task_fix"}, {"tasks", "task_lone"}, {"trip_records", "TRIPSEC"}, {"trip_records", "TRIPFIX"}} {
		if v := tenantOf(x[0], x[1]); v != quarantine {
			t.Fatalf("%s %s: %s", x[0], x[1], v)
		}
	}

	// The platform admin re-homes task_sec (its trip and both files follow) and the childless task_lone.
	out := h.mustRun("quarantine", "resolve", "--collection", "tasks", "--doc", "tasks/task_sec", "--action", "rehome", "--tenant", ownFleet)
	if !strings.Contains(out, "2 row(s) and 2 file(s) re-homed") {
		t.Fatalf("rehome task_sec: %s", out)
	}
	h.mustRun("quarantine", "resolve", "--collection", "tasks", "--doc", "tasks/task_lone", "--action", "rehome", "--tenant", ownFleet)
	if v := tenantOf("trip_records", "TRIPSEC"); v != ownFleet+" task" {
		t.Errorf("TRIPSEC follows its task: %s", v)
	}
	for _, k := range []string{"tasks/task_sec/checkin.jpg", "trip_records/TRIPSEC/seal.jpg"} {
		if v := fileTenant(k); v != ownFleet {
			t.Errorf("file %s stayed on %s", k, v)
		}
	}
	if v := h.scalar(`SELECT jsonb_array_length(details->'moved')::bigint FROM security_events WHERE details->>'docPath' = 'tasks/task_sec'`); v != int64(4) {
		t.Errorf("tenant_rehomed moved list: %v", v)
	}

	// Firestore then changes task_sec and task_lone (status), fixes task_fix at source (a driver), moves task_1 to
	// another carrier's driver (broker drift) and re-links TRIPSEC to a task of that carrier.
	t2 := t1.Add(24 * time.Hour)
	d2 := writeDump(t, t2.Add(time.Hour),
		collection{"tasks", []dump.Doc{
			task("task_sec", t2, map[string]any{"status": "Assigned"}),
			task("task_lone", t2, map[string]any{"status": "Assigned"}),
			task("task_fix", t2, map[string]any{"driverId": "drv_own"}),
			doc("tasks/task_1", t2, map[string]any{"taskId": "FM-01092026-001", "taskType": "FIRST_MILE", "status": "Completed",
				"date": dump.Timestamp{Time: time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)}, "driverId": "drv_alpha", "sourceHub": "SOCE",
				"destination": "SOCE"}),
		}},
		collection{"trip_records", []dump.Doc{trip("TRIPSEC", "task_2", t2)}})
	for _, args := range [][]string{
		{"load", "--dump", d2, "--collections", "tasks"},
		{"load", "--dump", d2, "--collections", "tasks", "--force"},
		{"load", "--dump", d2},
		{"load", "--dump", d2, "--since", "watermark", "--force"},
		{"quarantine", "resolve", "--collection", "tasks", "--doc", "tasks/task_sec", "--action", "retry"},
		{"quarantine", "resolve", "--collection", "tasks", "--doc", "tasks/task_lone", "--action", "retry"},
	} {
		h.mustRun(args...)
		for _, x := range [][3]string{
			{"tasks", "task_sec", ownFleet + " form"}, {"tasks", "task_lone", ownFleet + " form"},
			{"tasks", "task_fix", ownFleet + " driver"}, {"trip_records", "TRIPFIX", ownFleet + " task"},
			{"tasks", "task_1", ownFleet + " driver"}, {"trip_records", "TRIP001", ownFleet + " task"},
		} {
			if v := tenantOf(x[0], x[1]); v != x[2] {
				t.Errorf("after %v: %s %s is %s, want %s", args, x[0], x[1], v, x[2])
			}
		}
	}
	if v := h.scalar(`SELECT status FROM tasks WHERE legacy_doc_id = 'task_sec'`); v != "assigned" {
		t.Errorf("task_sec content is re-applied: %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE reason_code = 'tenant_unresolved' AND resolved_at IS NULL
		AND doc_path IN ('tasks/task_sec', 'tasks/task_lone', 'tasks/task_fix', 'trip_records/TRIPSEC', 'trip_records/TRIPFIX')`); v != int64(0) {
		t.Errorf("open tenant_unresolved findings reopened: %v", v)
	}
	// Fix at source: task_fix and its trip left the quarantine tenant together, with the trip's photo.
	if v := h.scalar(`SELECT string_agg(doc_path || ' ' || resolution, ', ' ORDER BY doc_path) FROM etl.quarantine
		WHERE reason_code = 'tenant_unresolved' AND doc_path IN ('tasks/task_fix', 'trip_records/TRIPFIX')`); v !=
		"tasks/task_fix fixed_at_source, trip_records/TRIPFIX fixed_at_source" {
		t.Errorf("fix at source findings: %v", v)
	}
	if v := h.scalar(`SELECT status FROM etl.source_docs WHERE doc_path = 'trip_records/TRIPFIX'`); v != "loaded" {
		t.Errorf("TRIPFIX source doc: %v", v)
	}
	for _, k := range []string{"tasks/task_fix/checkin.jpg", "trip_records/TRIPFIX/seal.jpg"} {
		if v := fileTenant(k); v != ownFleet {
			t.Errorf("file %s stayed on %s", k, v)
		}
	}
	// Broker drift: reported on the chain's field, never moved.
	if v := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE doc_path = 'tasks/task_1' AND field = 'driverId'
		AND reason_code = 'tenant_mismatch' AND resolved_at IS NULL`); v != int64(1) {
		t.Errorf("broker drift finding: %v", v)
	}
	// Re-link to another tenant's task: TRIPSEC keeps its tenant, the link is dropped and reported.
	if v := h.scalar(`SELECT coalesce(task_id::text, 'null') || ' ' || legacy_task_ref || ' ' || task_ref_match || ' ' || tenant_id::text
		FROM trip_records WHERE legacy_doc_id = 'TRIPSEC'`); v != "null task_2 none "+ownFleet {
		t.Errorf("re-linked TRIPSEC: %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE doc_path = 'trip_records/TRIPSEC' AND field = 'taskId'
		AND reason_code = 'tenant_mismatch' AND resolved_at IS NULL`); v != int64(1) {
		t.Errorf("re-link finding: %v", v)
	}
}

// The reason-code gate of reconcile (main spec §13.9, Appendix A §A.3.11): the baseline is the last matching run,
// so a new code or a higher count keeps failing on a plain rerun until the findings are resolved or the owner
// accepts them (review T15).
func TestReconcileFindingsGate(t *testing.T) {
	h := newHarness(t)
	h.mustRun("load", "--fixtures")
	reconcile := func(args ...string) (int, string) {
		code, out, errb := h.run(append([]string{"reconcile", "--fixtures"}, args...)...)
		if code != 0 && code != 2 {
			t.Fatalf("reconcile: exit %d\n%s\n%s", code, out, errb)
		}
		return code, out
	}
	if code, out := reconcile(); code != 0 {
		t.Fatalf("first run: %s", out)
	}
	if _, err := h.pool.Exec(h.ctx, `INSERT INTO etl.quarantine (collection, doc_path, field, reason_code, detail)
		VALUES ('customers', 'customers/cust_spx', 'taxId', 'bad_number', 'injected')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if code, out := reconcile(); code != 2 || !strings.Contains(out, "findings\tbad_number") || !strings.Contains(out, "did not have") {
			t.Fatalf("a new code must keep failing on a rerun: exit %d\n%s", code, out)
		}
	}
	h.mustRun("quarantine", "resolve", "--collection", "customers", "--doc", "customers/cust_spx", "--action", "skip")
	if code, out := reconcile(); code != 0 {
		t.Fatalf("resolved: %s", out)
	}
	if _, err := h.pool.Exec(h.ctx, `INSERT INTO etl.quarantine (collection, doc_path, field, reason_code)
		VALUES ('tasks', 'tasks/task_3', 'destination', 'hub_unresolved')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if code, out := reconcile(); code != 2 || !strings.Contains(out, "(+1)") {
			t.Fatalf("a higher count of a known code must fail: exit %d\n%s", code, out)
		}
	}
	dir := t.TempDir()
	if code, out := reconcile("--accept-findings", "owner", "--out", dir); code != 0 || !strings.Contains(out, "reconcile: match") {
		t.Fatalf("accepted: exit %d\n%s", code, out)
	}
	if v := h.scalar(`SELECT report->>'acceptedBy' FROM etl.reconciliation_runs ORDER BY started_at DESC, id DESC LIMIT 1`); v != "owner" {
		t.Errorf("acceptedBy: %v", v)
	}
	if code, out := reconcile(); code != 0 {
		t.Fatalf("the accepted run is the new baseline: %s", out)
	}
}
