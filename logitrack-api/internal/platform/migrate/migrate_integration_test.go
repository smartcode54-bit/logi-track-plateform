//go:build integration

package migrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// The tests share one container and change cluster-wide roles, so none of them runs in parallel.
func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

func TestEmbeddedChainRoundTrip(t *testing.T) {
	migratetest.RoundTrip(t, migrations.FS)
}

func TestBaselineShapeRoundTrip(t *testing.T) {
	migratetest.RoundTrip(t, os.DirFS("testdata/baseline-shape"))
}

func TestIrreversibleMigrationSetsTheRoundTripFloor(t *testing.T) {
	migratetest.RoundTrip(t, os.DirFS("testdata/irreversible"))
}

// A Down that drops more than its Up created leaves the schema different after the round trip.
func TestRoundTripReportsDrift(t *testing.T) {
	rec := &fatalRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		migratetest.RoundTrip(rec, os.DirFS("testdata/drift"))
	}()
	<-done
	if !strings.Contains(rec.msg, "schema differs after up -> down-to 1 -> up") || !strings.Contains(rec.msg, "fx_a_v") {
		t.Fatalf("round trip did not report the dropped index:\n%s", rec.msg)
	}
}

// fatalRecorder captures the first Fatal of a helper and stops its goroutine, like testing.T.
type fatalRecorder struct {
	testing.TB
	msg string
}

func (r *fatalRecorder) Helper() {}

func (r *fatalRecorder) Fatal(args ...any) { r.msg = fmt.Sprint(args...); runtime.Goexit() }

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// Production stays at 9 until the P1 runbook (R59, R88): up-to 9 must stop at 0009_infra and
// leave 0010_d5_unique_constraints pending; a later up applies it outside a transaction.
func TestUpToNineLeavesD5ConstraintsPending(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, os.DirFS("testdata/baseline-shape"))

	res, err := r.UpTo(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 9 || res[8].Name != "0009_infra.sql" {
		t.Fatalf("up-to 9 applied %+v", res)
	}
	if v, _ := r.Version(ctx); v != 9 {
		t.Fatalf("version = %d, want 9", v)
	}
	st, err := r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if s.Applied != (s.Version <= 9) {
			t.Fatalf("status %+v", s)
		}
	}
	if last := st[len(st)-1]; last.Name != "0010_d5_unique_constraints.sql" || last.Applied {
		t.Fatalf("last status = %+v, want 0010_d5_unique_constraints.sql pending", last)
	}
	if res, err := r.UpTo(ctx, 9); err != nil || len(res) != 0 {
		t.Fatalf("a second up-to 9 changed %v (%v)", res, err)
	}

	res, err = r.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Name != "0010_d5_unique_constraints.sql" {
		t.Fatalf("up applied %+v", res)
	}
	var valid bool
	pool := d.Pool(t, db.RoleMigrator)
	if err := pool.QueryRow(ctx, `SELECT i.indisvalid FROM pg_index i
		WHERE i.indexrelid = 'shape_chats_one_open_per_driver'::regclass`).Scan(&valid); err != nil || !valid {
		t.Fatalf("the CONCURRENTLY index is not valid (%v)", err)
	}
}

// Both runs see the same pending migrations (the version table exists before they start), so
// without the session lock both would apply 0001 and one would fail.
func TestConcurrentRunsApplyEachVersionOnce(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	fsys := os.DirFS("testdata/baseline-shape")
	runners := []*migrate.Runner{migratetest.Runner(t, d, fsys), migratetest.Runner(t, d, fsys)}
	for _, r := range runners {
		if _, err := r.Version(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	applied := make([]int, len(runners))
	errs := make([]error, len(runners))
	for i, r := range runners {
		wg.Go(func() {
			res, err := r.Up(ctx)
			applied[i], errs[i] = len(res), err
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if applied[0]+applied[1] != 10 {
		t.Fatalf("applied %v migrations, want 10 in total", applied)
	}
	var rows int
	if err := d.Pool(t, db.RoleMigrator).QueryRow(ctx,
		`SELECT count(*) FROM goose_db_version WHERE version_id > 0`).Scan(&rows); err != nil || rows != 10 {
		t.Fatalf("goose_db_version has %d rows (%v), want 10", rows, err)
	}
}

// While another session holds goose's advisory lock, Up waits; it applies once the lock is free.
func TestUpWaitsForTheMigrationLock(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, os.DirFS("testdata/baseline-shape"))
	if _, err := r.Version(ctx); err != nil {
		t.Fatal(err)
	}
	holder, err := pgx.Connect(ctx, d.URL(db.RoleMigrator))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close(ctx) }()
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", lock.DefaultLockID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.Up(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Up finished while the lock was held (err %v)", err)
	case <-time.After(2 * time.Second):
	}
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", lock.DefaultLockID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Up did not finish after the lock was released")
	}
	if v, _ := r.Version(ctx); v != 10 {
		t.Fatalf("version = %d, want 10", v)
	}
}

// The embedded chain in the production order (R59, R88): up-to 9 applies the baseline and leaves
// 0010_d5_unique_constraints pending; apply 11 ships the storage schema of T11 ahead of it (P0);
// up-to 9 and apply 11 stay no-ops on the next deploys; the P1 runbook's up then builds the D5
// indexes outside a transaction, after 0011.
func TestEmbeddedChainUpToNineApplyElevenLeavesD5Pending(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	res, err := r.UpTo(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 9 || res[8].Name != "0009_infra.sql" {
		t.Fatalf("up-to 9 applied %+v", res)
	}
	pool := d.Pool(t, db.RoleMigrator)
	d5 := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			WHERE i.indisunique AND i.indisvalid AND c.relname IN
			('vehicle_expenses_fuel_taxinv','chats_one_open_per_driver','customer_service_fees_one_per_type')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := d5(); n != 0 {
		t.Fatalf("%d D5 unique indexes exist at version 9", n)
	}
	// Nothing past 9 is applied by up-to 9.
	st, err := r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st[9:] {
		if s.Applied {
			t.Fatalf("status %+v applied at version 9", s)
		}
	}
	if st[9].Name != "0010_d5_unique_constraints.sql" || st[10].Name != "0011_file_objects_storage_backend.sql" {
		t.Fatalf("pending = %+v, %+v", st[9], st[10])
	}

	// P0: apply 11 ahead of the held 0010.
	if res, err = r.Apply(ctx, 11); err != nil || len(res) != 1 || res[0].Name != "0011_file_objects_storage_backend.sql" {
		t.Fatalf("apply 11 applied %+v (%v)", res, err)
	}
	var col int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'file_objects' AND column_name = 'storage_backend'`).Scan(&col); err != nil || col != 1 {
		t.Fatalf("file_objects.storage_backend after apply 11: %d (%v)", col, err)
	}
	if st, err = r.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if st[9].Applied || !st[10].Applied {
		t.Fatalf("after apply 11: 0010 %+v, 0011 %+v", st[9], st[10])
	}
	if v, _ := r.Version(ctx); v != 11 {
		t.Fatalf("version = %d, want 11", v)
	}
	// The next deploy repeats both steps: no-ops, no out-of-order error.
	if res, err := r.UpTo(ctx, 9); err != nil || len(res) != 0 {
		t.Fatalf("up-to 9 after apply 11: %+v (%v)", res, err)
	}
	if res, err := r.Apply(ctx, 11); err != nil || len(res) != 0 {
		t.Fatalf("apply 11 again: %+v (%v)", res, err)
	}
	if n := d5(); n != 0 {
		t.Fatalf("%d D5 unique indexes before the P1 runbook", n)
	}

	// P1 runbook: up applies the held 0010 (and anything above 11).
	if res, err = r.Up(ctx); err != nil || len(res) != len(st)-10 || res[0].Name != "0010_d5_unique_constraints.sql" {
		t.Fatalf("up applied %+v (%v)", res, err)
	}
	if n := d5(); n != 3 {
		t.Fatalf("%d valid D5 unique indexes after up, want 3", n)
	}
	if st, err = r.Status(ctx); err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if !s.Applied {
			t.Fatalf("%s still pending after up", s.Name)
		}
	}
}

// Only a Held version may stay behind: apply refuses to skip any other pending version, and up
// refuses a database where one is missing below the applied maximum (goose's own default).
func TestHeldVersionIsTheOnlyAllowedGap(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	if _, err := r.UpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	if res, err := r.Apply(ctx, 11); err == nil || !strings.Contains(err.Error(), "0009_infra.sql") {
		t.Fatalf("apply 11 at version 8: %+v (%v)", res, err)
	}
	if v, _ := r.Version(ctx); v != 8 {
		t.Fatalf("version = %d after a refused apply, want 8", v)
	}
	if _, err := r.Apply(ctx, 99); err == nil || !strings.Contains(err.Error(), "no version 99") {
		t.Fatalf("apply of an unknown version: %v", err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	// A version recorded as missing below the maximum (not Held) stops every run.
	if _, err := d.Pool(t, db.RoleMigrator).Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = 5`); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func() ([]migrate.Result, error){
		"up":       func() ([]migrate.Result, error) { return r.Up(ctx) },
		"up-to 9":  func() ([]migrate.Result, error) { return r.UpTo(ctx, 9) },
		"apply 11": func() ([]migrate.Result, error) { return r.Apply(ctx, 11) },
	} {
		res, err := run()
		if name == "apply 11" {
			// 11 is applied: a no-op that never looks at the gap.
			if err != nil || len(res) != 0 {
				t.Errorf("%s: %+v (%v)", name, res, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "0005_billing.sql") || !strings.Contains(err.Error(), "out of order") {
			t.Errorf("%s with 0005 missing: %+v (%v)", name, res, err)
		}
	}
}

// Every object belongs to logitrack_migrator (R66), except the SECURITY DEFINER functions that 0009
// hands to logitrack_rls_definer (Appendix C §C.3.3).
func TestChainObjectsBelongToTheMigrator(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleMigrator)
	var foreign int
	// citext is a trusted extension: its member functions belong to the bootstrap superuser.
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname IN ('public', 'etl') AND pg_get_userbyid(p.proowner) <> 'logitrack_migrator'
		  AND NOT (p.prosecdef AND pg_get_userbyid(p.proowner) = 'logitrack_rls_definer')
		  AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')`).
		Scan(&foreign); err != nil || foreign != 0 {
		t.Fatalf("%d functions owned by neither logitrack_migrator nor (SECURITY DEFINER) logitrack_rls_definer (%v)", foreign, err)
	}
	var definer []string
	if err := pool.QueryRow(ctx, `SELECT coalesce(array_agg(p.oid::regprocedure::text ORDER BY p.oid::regprocedure::text), '{}')
		FROM pg_proc p WHERE pg_get_userbyid(p.proowner) = 'logitrack_rls_definer'`).Scan(&definer); err != nil {
		t.Fatal(err)
	}
	if want := []string{"app_recent_work_in_scope(uuid,uuid)", "app_task_in_scope(uuid)", "app_task_stops_in_scope(uuid)",
		"app_trip_in_scope(uuid)", "driver_directory()", "next_invoice_seq(uuid,uuid,integer,integer,text)",
		"next_task_seq(text,date)", "trg_driver_link_membership()"}; strings.Join(definer, " ") != strings.Join(want, " ") {
		t.Fatalf("functions of logitrack_rls_definer = %v, want %v", definer, want)
	}
	var notMine int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname IN ('public', 'etl') AND c.relname <> 'goose_db_version'
		  AND pg_get_userbyid(c.relowner) <> 'logitrack_migrator'`).Scan(&notMine); err != nil || notMine != 0 {
		t.Fatalf("%d relations not owned by logitrack_migrator (%v)", notMine, err)
	}
	var day string
	var quarantine string
	if err := pool.QueryRow(ctx, `SELECT bkk_date('2026-10-08T17:00:00Z')::text, app_quarantine_tenant_id()::text`).
		Scan(&day, &quarantine); err != nil {
		t.Fatal(err)
	}
	if day != "2026-10-09" || quarantine != "00000000-0000-7000-8000-00000000000f" {
		t.Fatalf("bkk_date = %s, quarantine id = %s", day, quarantine)
	}
	var etl, citext bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'etl'),
		EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citext')`).Scan(&etl, &citext); err != nil || !etl || !citext {
		t.Fatalf("etl schema %t, citext %t (%v)", etl, citext, err)
	}
}

func TestRequireMigrator(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if err := migrate.RequireMigrator(ctx, d.Pool(t, db.RoleMigrator)); err != nil {
		t.Fatalf("migrator rejected: %v", err)
	}
	for _, role := range []string{db.RoleApp, db.RoleETL, db.RoleReadonly} {
		err := migrate.RequireMigrator(ctx, d.Pool(t, role))
		if err == nil || !strings.Contains(err.Error(), "not as "+role) {
			t.Errorf("%s: err = %v", role, err)
		}
	}
	super, err := pgx.Connect(ctx, d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = super.Close(ctx) }()
	if err := migrate.RequireMigrator(ctx, super); err == nil || !strings.Contains(err.Error(), "superuser=true") {
		t.Errorf("superuser: err = %v", err)
	}
}

// 0001 refuses a wrong login or a missing role, and because goose runs the file in one transaction
// none of its objects survives the refusal (the assertion comes first so its error is the first one;
// TestPreambleStartsWithTheRoleAssertion checks the order). goose runs here without the
// RequireMigrator preflight of cmd/migrate.
func TestPreambleRefusesWrongLoginAndMissingRoles(t *testing.T) {
	ctx := context.Background()

	t.Run("superuser login", func(t *testing.T) {
		d := pgtest.NewDatabase(t)
		err := gooseUp(t, d.SuperURL())
		if err == nil || !strings.Contains(err.Error(), "migrations run as logitrack_migrator (MIGRATE_DATABASE_URL), not postgres") {
			t.Fatalf("err = %v", err)
		}
		assertNothingCreated(t, d)
		// goose commits its version table before 0001 runs; it belongs to the login that ran goose,
		// so after such a run it must be dropped before cmd/migrate takes over (README, Migrations).
		var owner string
		if err := d.Pool(t, db.RoleMigrator).QueryRow(context.Background(),
			`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.goose_db_version'::regclass`).Scan(&owner); err != nil || owner != "postgres" {
			t.Fatalf("goose_db_version owner = %q (%v)", owner, err)
		}
	})

	for _, tc := range []struct{ name, breakSQL, restoreSQL, want string }{
		{"etl role missing",
			"ALTER ROLE logitrack_etl RENAME TO logitrack_etl_gone", "ALTER ROLE logitrack_etl_gone RENAME TO logitrack_etl",
			"role logitrack_etl is missing or has wrong attributes"},
		{"rls_definer role missing",
			"ALTER ROLE logitrack_rls_definer RENAME TO logitrack_rls_definer_gone",
			"ALTER ROLE logitrack_rls_definer_gone RENAME TO logitrack_rls_definer",
			"role logitrack_rls_definer is missing or has wrong attributes"},
		{"readonly cannot log in",
			"ALTER ROLE logitrack_readonly NOLOGIN", "ALTER ROLE logitrack_readonly LOGIN",
			"role logitrack_readonly is missing or has wrong attributes"},
		{"app bypasses RLS",
			"ALTER ROLE logitrack_app BYPASSRLS", "ALTER ROLE logitrack_app NOBYPASSRLS",
			"role logitrack_app is missing or has wrong attributes"},
		{"migrator cannot SET ROLE logitrack_rls_definer",
			"REVOKE logitrack_rls_definer FROM logitrack_migrator",
			"GRANT logitrack_rls_definer TO logitrack_migrator WITH INHERIT FALSE",
			"logitrack_migrator must be granted logitrack_rls_definer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := pgtest.NewDatabase(t)
			c := d.Cluster()
			if err := c.SuperExec(ctx, tc.breakSQL); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.SuperExec(context.Background(), tc.restoreSQL); err != nil {
					t.Errorf("restore: %v", err)
				}
			})
			err := gooseUp(t, d.URL(db.RoleMigrator))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant %q", err, tc.want)
			}
			assertNothingCreated(t, d)
		})
	}
}

func gooseUp(t *testing.T, url string) error {
	t.Helper()
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Up(context.Background())
	return err
}

// assertNothingCreated checks that the refused 0001 rolled back completely.
func assertNothingCreated(t *testing.T, d *pgtest.Database) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var objects int
	if err := conn.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM pg_namespace WHERE nspname = 'etl') +
		(SELECT count(*) FROM pg_extension WHERE extname = 'citext') +
		(SELECT count(*) FROM pg_proc WHERE proname IN ('bkk_date', 'app_user_id'))`).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if objects != 0 {
		t.Fatalf("%d objects of 0001 exist after the refusal", objects)
	}
}
