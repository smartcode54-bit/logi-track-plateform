//go:build integration

// Row-level security behaviour of the baseline as principals: logitrack_app sessions carrying the
// request GUCs that db.WithPrincipal will set (T07). These cover the Appendix C §C.3.5 / §C.3.6 rules
// a catalog check cannot see; the layout itself is checked by internal/platform/db/rls_catalog_test.go.
package migrations_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
)

// affected runs sql and returns the number of rows it changed.
func affected(t *testing.T, c *pgx.Conn, sql string, args ...any) int64 {
	t.Helper()
	tag, err := c.Exec(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return tag.RowsAffected()
}

// principal opens a logitrack_app session for user acting in tenant with role (no bypass); extra adds
// GUC name, value pairs such as app.driver_id.
func principal(t *testing.T, d *pgtest.Database, user, tenant, role string, extra ...string) *pgx.Conn {
	t.Helper()
	return connect(t, d, db.RoleApp, append([]string{"app.user_id", user, "app.tenant_id", tenant, "app.role", role}, extra...)...)
}

// The driver maintenance gate (C.3.5 "0006_finance_hr") opens only maintenance rows of the driver's
// active tenant. The foreign keys drivers.active_truck_id and truck_assignments.truck_id accept a
// truck of any tenant, and an assignment left in a former tenant stays visible to the driver, so
// none of the three may open another tenant's records or their maintenance_files.
func TestDriverMaintenanceGateIsTenantBound(t *testing.T) {
	d, _ := migrated(t)
	etl := connect(t, d, db.RoleETL)
	a := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'A') RETURNING id::text`)
	b := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th, legal_type) VALUES ('carrier', 'B', 'company') RETURNING id::text`)
	truck := func(tenant, plate string) string {
		return scalar[string](t, etl, `INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand,
			model, year, color, type_raw) VALUES ($1, 'self', $2, 'BKK', 'active', 'Isuzu', 'X', 2020, 'white', '6 Wheels')
			RETURNING id::text`, tenant, plate)
	}
	record := func(tenant, truckID, notes string) string {
		m := scalar[string](t, etl, `INSERT INTO maintenance_records (tenant_id, truck_id, maintenance_type, service_type,
			start_date, status, notes) VALUES ($1, $2, 'CM', 'engine', current_date, 'in_progress', $3) RETURNING id::text`,
			tenant, truckID, notes)
		f := scalar[string](t, etl, `INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, status, committed_at,
			owner_kind, owner_id, storage_backend) VALUES ('private', $1, $2, 'maintenance_receipt', 'committed', now(),
			'maintenance', $3, 's3') RETURNING id::text`, "maintenance/"+m+"/receipt.jpg", tenant, m)
		exec(t, etl, `INSERT INTO maintenance_files (maintenance_id, file_id, kind) VALUES ($1, $2, 'receipt')`, m, f)
		return m
	}
	ta, tb := truck(a, "70-0001"), truck(b, "70-0002")
	ma, mb := record(a, ta, "tenant A"), record(b, tb, "tenant B secret")
	da := scalar[string](t, etl, `INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile)
		VALUES ($1, 'self', 'สมชาย', 'ใจดี', '0800000000') RETURNING id::text`, a)

	drv := principal(t, d, "0199c000-0000-7000-8000-0000000000d1", a, "driver", "app.driver_id", da)
	staffA := principal(t, d, "0199c000-0000-7000-8000-0000000000a1", a, "tenant_admin")

	// gate requires that the driver reads, updates and sees the files of record m exactly n times.
	gate := func(step, m string, n int64) {
		t.Helper()
		read := scalar[int64](t, drv, `SELECT count(*) FROM maintenance_records WHERE id = $1`, m)
		files := scalar[int64](t, drv, `SELECT count(*) FROM maintenance_files WHERE maintenance_id = $1`, m)
		updated := affected(t, drv, `UPDATE maintenance_records SET notes = notes WHERE id = $1`, m)
		if read != n || files != n || updated != n {
			t.Errorf("%s: record %s read %d, files %d, updated %d; want %d each", step, m, read, files, updated, n)
		}
	}
	gate("no truck", ma, 0)
	gate("no truck", mb, 0)

	// The legitimate path: the own-tenant truck the driver checked in with.
	if n := affected(t, drv, `UPDATE drivers SET active_truck_id = $1 WHERE id = $2`, ta, da); n != 1 {
		t.Fatalf("driver sets its active truck: %d rows", n)
	}
	gate("own active truck", ma, 1)
	gate("own active truck", mb, 0)

	// 1. The driver points active_truck_id at B's truck (t_driver_self_columns allows the column, the FK any truck).
	if n := affected(t, drv, `UPDATE drivers SET active_truck_id = $1 WHERE id = $2`, tb, da); n != 1 {
		t.Fatalf("driver sets active_truck_id: %d rows", n)
	}
	gate("active truck of B", mb, 0)
	gate("active truck of B", ma, 0)
	exec(t, drv, `UPDATE drivers SET active_truck_id = NULL WHERE id = $1`, da)

	// 2. Staff of A assign their driver to B's truck (p_tenant_staff checks only the assignment's tenant).
	exec(t, staffA, `INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status)
		VALUES ($1, 'self', $2, $3, 'active')`, a, tb, da)
	gate("assignment in A to B's truck", mb, 0)
	exec(t, staffA, `UPDATE truck_assignments SET status = 'revoked', revoked_at = now() WHERE truck_id = $1`, tb)

	// 3. An assignment still active in B from before the driver moved to A (C.1.4).
	exec(t, etl, `INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status)
		VALUES ($1, 'driver', $2, $3, 'active')`, b, tb, da)
	if n := scalar[int64](t, drv, `SELECT count(*) FROM truck_assignments WHERE tenant_id = $1`, b); n != 1 {
		t.Fatalf("the driver sees %d of its assignments in B, want 1 (self-scope by driver_id)", n)
	}
	gate("stale assignment in B", mb, 0)

	// The home truck of an active own-tenant assignment opens its records.
	exec(t, staffA, `INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status)
		VALUES ($1, 'self', $2, $3, 'active')`, a, ta, da)
	gate("own assignment", ma, 1)
	gate("own assignment", mb, 0)
	if notes := scalar[string](t, etl, `SELECT notes FROM maintenance_records WHERE id = $1`, mb); notes != "tenant B secret" {
		t.Fatalf("B's record changed to %q", notes)
	}
}

// file_objects (C.3.5 p_commit, C.3.6 t_file_objects_commit_columns): outside WithSystem an update
// may only commit an upload. The tenant, identity, exposure and storage-backend (0011) columns stay as
// uploaded, and status moves only pending -> committed; WithSystem (re-home C.3.10, missing_at_source)
// is unaffected.
func TestFileObjectsUpdateOnlyCommits(t *testing.T) {
	d, _ := migrated(t)
	etl := connect(t, d, db.RoleETL)
	a := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'A') RETURNING id::text`)
	b := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th, legal_type) VALUES ('carrier', 'B', 'company') RETURNING id::text`)
	u := scalar[string](t, etl, `INSERT INTO users (email) VALUES ('driver@example.test') RETURNING id::text`)
	op := scalar[string](t, etl, `INSERT INTO users (email) VALUES ('ops@example.test') RETURNING id::text`)
	drv := principal(t, d, u, a, "driver", "app.driver_id", "0199c000-0000-7000-8000-0000000000d1")
	upload := func(key string) string {
		return scalar[string](t, drv, `INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, uploaded_by, expires_at,
			storage_backend) VALUES ('private', $1, $2, 'trip_photo', $3, now() + interval '1 hour', 'local') RETURNING id::text`, key, a, u)
	}
	const commit = `UPDATE file_objects SET status = 'committed', committed_at = now(), expires_at = NULL,
		owner_kind = 'trip', owner_id = uuidv7() WHERE id = $1`

	f := upload("uploads/a.jpg")
	for _, set := range []string{
		"tenant_id = '" + b + "', status = 'committed', committed_at = now(), expires_at = NULL, owner_kind = 'truck', owner_id = uuidv7()",
		"tenant_id = NULL",
		"bucket = 'public', object_key = 'app_releases/evil.apk', purpose = 'apk', visibility = 'public'",
		"object_key = 'uploads/other.jpg'",
		"uploaded_by = '" + op + "'",
		"status = 'missing_at_source'",
		"storage_backend = 's3'", // 0011: where an object lives is fixed at upload too (T11)
	} {
		wantSQLState(t, drv, "42501", "fixed at upload", "UPDATE file_objects SET "+set+" WHERE id = $1", f)
	}
	if n := affected(t, drv, commit, f); n != 1 {
		t.Fatalf("the uploader commits its upload: %d rows", n)
	}
	wantSQLState(t, drv, "42501", "fixed at upload",
		`UPDATE file_objects SET status = 'pending', committed_at = NULL, expires_at = now() + interval '1 hour' WHERE id = $1`, f)

	// Staff in reach commit an upload of another user, but cannot publish it; staff of B do not see it.
	g := upload("uploads/b.jpg")
	staffB := principal(t, d, "0199c000-0000-7000-8000-0000000000b1", b, "tenant_admin")
	if n := affected(t, staffB, commit, g); n != 0 {
		t.Fatalf("staff of B commit A's upload: %d rows", n)
	}
	staffA := principal(t, d, op, a, "operator")
	if n := affected(t, staffA, commit, g); n != 1 {
		t.Fatalf("staff of A commit the driver's upload: %d rows", n)
	}
	wantSQLState(t, staffA, "42501", "fixed at upload", `UPDATE file_objects SET purpose = 'apk', visibility = 'public' WHERE id = $1`, g)

	// p_commit's WITH CHECK holds on its own: with the column guard off (app.etl_load, cmd/etl only),
	// the uploader still cannot move its row out of its tenant.
	h := upload("uploads/c.jpg")
	unguarded := principal(t, d, u, a, "driver", "app.driver_id", "0199c000-0000-7000-8000-0000000000d1", "app.etl_load", "on")
	wantSQLState(t, unguarded, "42501", "row-level security", `UPDATE file_objects SET tenant_id = $2 WHERE id = $1`, h, b)
	wantSQLState(t, unguarded, "42501", "row-level security", `UPDATE file_objects SET tenant_id = NULL WHERE id = $1`, h)

	// WithSystem: quarantine re-home and the ETL / storage status changes still work.
	sys := connect(t, d, db.RoleApp, "app.bypass_tenant", "on")
	if n := affected(t, sys, `UPDATE file_objects SET tenant_id = $2 WHERE id = $1`, f, b); n != 1 {
		t.Fatalf("WithSystem re-home: %d rows", n)
	}
	if n := affected(t, sys, `UPDATE file_objects SET status = 'missing_at_source' WHERE id = $1`, g); n != 1 {
		t.Fatalf("WithSystem missing_at_source: %d rows", n)
	}
}

// The SECURITY DEFINER helpers of 0013 bypass RLS, so they check their caller themselves (T23 review):
// driver_active_assignment answers only staff that reach the driver (then the driver's active row of any tenant
// counts), assignment_release_driver acts only on a revoked row of the caller's reach, with the caller as the status
// entry's user, and releases a driver the caller cannot read.
func TestDriverAssignmentHelpersCheckTheirCaller(t *testing.T) {
	d, _ := migrated(t)
	etl := connect(t, d, db.RoleETL)
	o := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'O') RETURNING id::text`)
	a := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th, legal_type, contractor_tenant_id)
		VALUES ('carrier', 'A', 'company', $1) RETURNING id::text`, o)
	c := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th, legal_type) VALUES ('carrier', 'C', 'company') RETURNING id::text`)
	truck := func(tenant, plate string) string {
		return scalar[string](t, etl, `INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand,
			model, year, color, type_raw) VALUES ($1, 'self', $2, 'BKK', 'active', 'Isuzu', 'X', 2020, 'white', '6 Wheels')
			RETURNING id::text`, tenant, plate)
	}
	driver := func(tenant string) string {
		return scalar[string](t, etl, `INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, status)
			VALUES ($1, 'self', 'สมชาย', 'ใจดี', '0800000000', 'on_duty') RETURNING id::text`, tenant)
	}
	user := func(email string) string {
		return scalar[string](t, etl, `INSERT INTO users (email, display_name) VALUES ($1::text, $1::text) RETURNING id::text`, email)
	}
	tO, dA := truck(o, "71-0001"), driver(a)
	rowO := scalar[string](t, etl, `INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status)
		VALUES ($1, 'truck', $2, $3, 'active') RETURNING id::text`, o, tO, dA)
	exec(t, etl, `UPDATE drivers SET current_assignment_id = $1 WHERE id = $2`, rowO, dA)
	uO, uA := user("o@example.test"), user("a@example.test")
	staffO := principal(t, d, uO, o, "tenant_admin", "app.subtenant_ids", a)
	staffA := principal(t, d, uA, a, "operator")
	staffC := principal(t, d, user("c@example.test"), c, "tenant_admin")
	drvA := principal(t, d, user("d@example.test"), a, "driver", "app.driver_id", dA)

	if n := scalar[int64](t, staffA, `SELECT count(*) FROM truck_assignments WHERE id = $1`, rowO); n != 0 {
		t.Fatalf("carrier A reads the own fleet's row (%d)", n)
	}
	if got := scalar[*string](t, staffA, `SELECT driver_active_assignment($1)::text`, dA); got == nil || *got != rowO {
		t.Fatalf("driver_active_assignment for A's staff = %v, want %s", got, rowO)
	}
	for name, cn := range map[string]*pgx.Conn{"staff of C": staffC, "the driver": drvA} {
		if got := scalar[*string](t, cn, `SELECT driver_active_assignment($1)::text`, dA); got != nil {
			t.Errorf("driver_active_assignment for %s = %s, want NULL", name, *got)
		}
	}

	const release = `SELECT assignment_release_driver($1, $2, NULL, 'truck assignment revoked', now())::text`
	wantSQLState(t, staffO, "55000", "still active", release, rowO, uO)
	exec(t, etl, `UPDATE truck_assignments SET status = 'revoked', revoked_at = now() WHERE id = $1`, rowO)
	wantSQLState(t, staffA, "42501", "outside the caller's reach", release, rowO, uA)
	wantSQLState(t, staffC, "42501", "outside the caller's reach", release, rowO, nil)
	wantSQLState(t, drvA, "42501", "outside the caller's reach", release, rowO, nil)
	wantSQLState(t, staffO, "42501", "must name the caller", release, rowO, uA)
	wantSQLState(t, staffO, "42501", "outside the caller's reach", release, "0199c000-0000-7000-8000-0000000000ff", uO)
	if got := scalar[string](t, staffO, release, rowO, uO); got != a {
		t.Fatalf("assignment_release_driver returned %s, want the driver's tenant %s", got, a)
	}
	var status string
	var current *string
	if err := etl.QueryRow(context.Background(), `SELECT status, current_assignment_id::text FROM drivers WHERE id = $1`, dA).
		Scan(&status, &current); err != nil || status != "active" || current != nil {
		t.Fatalf("driver after the release: %s %v (%v)", status, current, err)
	}
	if n := scalar[int64](t, etl, `SELECT count(*) FROM status_history WHERE entity_type = 'driver' AND entity_id = $1
		AND status = 'active' AND previous_status = 'on_duty' AND changed_by_user_id = $2`, dA, uO); n != 1 {
		t.Errorf("status entries of the release: %d", n)
	}
}

// The public holiday calendar (tenant_id NULL, R12) is every principal's to read and only a steward's to write (R60,
// T45 AC); a tenant's own holidays are its staff's (and its contractor's) to write, never another tenant's. The service
// refuses a non-steward before the database; this is the database's own answer.
func TestHolidaysStewardRule(t *testing.T) {
	d, _ := migrated(t)
	etl := connect(t, d, db.RoleETL)
	o := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'O') RETURNING id::text`)
	a := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th, legal_type, contractor_tenant_id) VALUES ('carrier', 'A',
		'company', $1) RETURNING id::text`, o)
	c := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th, legal_type) VALUES ('carrier', 'C', 'company') RETURNING id::text`)
	user := func(email string) string {
		return scalar[string](t, etl, `INSERT INTO users (email, display_name) VALUES ($1::text, $1::text) RETURNING id::text`, email)
	}
	pub := scalar[string](t, etl, `INSERT INTO holidays (holiday_date, holiday_type, name) VALUES ('2026-01-01', 'public', 'ปีใหม่')
		RETURNING id::text`)
	compA := scalar[string](t, etl, `INSERT INTO holidays (tenant_id, holiday_date, holiday_type, name)
		VALUES ($1, '2026-05-01', 'company', 'A') RETURNING id::text`, a)
	steward := principal(t, d, user("o@example.test"), o, "tenant_admin", "app.steward", "on", "app.subtenant_ids", a)
	staffA := principal(t, d, user("a@example.test"), a, "tenant_admin")
	staffC := principal(t, d, user("c@example.test"), c, "tenant_admin")

	for name, cn := range map[string]*pgx.Conn{"steward": steward, "A": staffA, "C": staffC} {
		if n := scalar[int64](t, cn, `SELECT count(*) FROM holidays WHERE id = $1`, pub); n != 1 {
			t.Errorf("%s cannot read the public calendar", name)
		}
	}
	if n := affected(t, staffA, `UPDATE holidays SET name = 'x' WHERE id = $1`, pub); n != 0 {
		t.Errorf("a carrier's admin changed a public holiday")
	}
	wantSQLState(t, staffA, "42501", "row-level security", `INSERT INTO holidays (holiday_date, holiday_type, name)
		VALUES ('2026-01-02', 'public', 'x')`)
	if n := affected(t, staffA, `DELETE FROM holidays WHERE id = $1`, pub); n != 0 {
		t.Errorf("a carrier's admin deleted a public holiday")
	}
	if n := affected(t, steward, `UPDATE holidays SET name = 'วันขึ้นปีใหม่' WHERE id = $1`, pub); n != 1 {
		t.Errorf("the steward cannot change a public holiday")
	}
	exec(t, steward, `INSERT INTO holidays (holiday_date, holiday_type, name) VALUES ('2026-01-02', 'public', 'x')`)
	// A tenant's own rows: its staff and its contractor's write them, another carrier neither reads nor writes them.
	if n := affected(t, staffA, `UPDATE holidays SET name = 'A2' WHERE id = $1`, compA); n != 1 {
		t.Errorf("A cannot change its own holiday")
	}
	if n := affected(t, steward, `UPDATE holidays SET name = 'A3' WHERE id = $1`, compA); n != 1 {
		t.Errorf("the contractor cannot change its carrier's holiday")
	}
	if n := scalar[int64](t, staffC, `SELECT count(*) FROM holidays WHERE id = $1`, compA); n != 0 {
		t.Errorf("C reads A's holiday")
	}
	if n := affected(t, staffC, `UPDATE holidays SET name = 'x' WHERE id = $1`, compA); n != 0 {
		t.Errorf("C changed A's holiday")
	}
	wantSQLState(t, staffC, "42501", "row-level security", `INSERT INTO holidays (tenant_id, holiday_date, holiday_type, name)
		VALUES ($1, '2026-05-02', 'company', 'x')`, a)
}

// The broadcast audience (0022, T48): a carrier's driver reads (and marks read) the own fleet's broadcast addressed to
// it, never one addressed to others, never a voided one; the legacy rows without recipients stay readable as before;
// only a steward (or the system context) lists the platform-wide audience, which leaves out inactive drivers, disabled
// logins, suspended tenants and the quarantine tenant; a platform-wide broadcast is written by a steward only.
func TestBroadcastAudience(t *testing.T) {
	d, _ := migrated(t)
	etl := connect(t, d, db.RoleETL)
	o := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'O') RETURNING id::text`)
	a := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th, legal_type, contractor_tenant_id) VALUES ('carrier', 'A',
		'company', $1) RETURNING id::text`, o)
	s := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th, legal_type, status) VALUES ('carrier', 'S', 'company',
		'suspended') RETURNING id::text`)
	user := func(email, status string) string {
		return scalar[string](t, etl, `INSERT INTO users (email, display_name, status, disabled_at)
			VALUES ($1::text, $1::text, $2, CASE WHEN $2 = 'disabled' THEN now() END) RETURNING id::text`, email, status)
	}
	driver := func(tenant, u, status string) string {
		exec(t, etl, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'driver')`, u, tenant)
		return scalar[string](t, etl, `INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, mobile, status)
			VALUES ($1, 'self', $2, 'ส', 'ข', '0800000000', $3) RETURNING id::text`, tenant, u, status)
	}
	uA, uA2, uO := user("a@example.test", "active"), user("a2@example.test", "active"), user("o@example.test", "active")
	dA, dA2 := driver(a, uA, "active"), driver(a, uA2, "active")
	driver(o, uO, "on_duty")
	driver(o, user("inactive@example.test", "active"), "inactive")
	driver(o, user("disabled@example.test", "disabled"), "active")
	driver(s, user("suspended@example.test", "active"), "active")
	driver("00000000-0000-7000-8000-00000000000f", user("q@example.test", "active"), "active")
	staffO := principal(t, d, user("staff.o@example.test", "active"), o, "tenant_admin", "app.steward", "on", "app.subtenant_ids", a)
	staffA := principal(t, d, user("staff.a@example.test", "active"), a, "tenant_admin")
	drvA := principal(t, d, uA, a, "driver", "app.driver_id", dA)
	drvA2 := principal(t, d, uA2, a, "driver", "app.driver_id", dA2)

	addressed := scalar[string](t, etl, `INSERT INTO broadcasts (tenant_id, message_text) VALUES ($1, 'to A') RETURNING id::text`, o)
	exec(t, etl, `INSERT INTO broadcast_recipients (broadcast_id, user_id) VALUES ($1, $2)`, addressed, uA)
	voided := scalar[string](t, etl, `INSERT INTO broadcasts (tenant_id, message_text, voided_at) VALUES ($1, 'v', now()) RETURNING id::text`, o)
	exec(t, etl, `INSERT INTO broadcast_recipients (broadcast_id, user_id) VALUES ($1, $2)`, voided, uA)
	legacy := scalar[string](t, etl, `INSERT INTO broadcasts (message_text) VALUES ('legacy') RETURNING id::text`)

	for _, tc := range []struct {
		name  string
		cn    *pgx.Conn
		id    string
		reads int64
	}{{"the addressed driver", drvA, addressed, 1}, {"another driver of A", drvA2, addressed, 0}, {"a voided one", drvA, voided, 0},
		{"a legacy row", drvA2, legacy, 1}} {
		if n := scalar[int64](t, tc.cn, `SELECT count(*) FROM broadcasts WHERE id = $1`, tc.id); n != tc.reads {
			t.Errorf("%s: reads %d rows, want %d", tc.name, n, tc.reads)
		}
	}
	if n := affected(t, drvA, `INSERT INTO broadcast_reads (broadcast_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, addressed, uA); n != 1 {
		t.Errorf("the addressed driver's read: %d", n)
	}
	if n := affected(t, drvA, `INSERT INTO broadcast_reads (broadcast_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, addressed, uA); n != 0 {
		t.Errorf("a second read wrote %d rows", n)
	}
	wantSQLState(t, drvA2, "42501", "row-level security", `INSERT INTO broadcast_reads (broadcast_id, user_id) VALUES ($1, $2)`, addressed, uA2)
	if !scalar[bool](t, drvA, `SELECT app_broadcast_recipient($1)`, addressed) || scalar[bool](t, drvA2, `SELECT app_broadcast_recipient($1)`, addressed) {
		t.Error("app_broadcast_recipient answers for someone else")
	}
	if scalar[bool](t, drvA, `SELECT broadcast_unaddressed($1)`, addressed) || !scalar[bool](t, drvA, `SELECT broadcast_unaddressed($1)`, legacy) {
		t.Error("broadcast_unaddressed")
	}

	wantSQLState(t, staffA, "42501", "needs a steward", `SELECT count(*) FROM broadcast_platform_audience()`)
	wantSQLState(t, drvA, "42501", "needs a steward", `SELECT count(*) FROM broadcast_platform_audience()`)
	if got := scalar[[]string](t, staffO, `SELECT array_agg(user_id::text ORDER BY user_id) FROM broadcast_platform_audience()`); strings.Join(got, ",") !=
		strings.Join(sortedStrings(uA, uA2, uO), ",") {
		t.Errorf("platform audience %v, want %v", got, sortedStrings(uA, uA2, uO))
	}
	wantSQLState(t, staffA, "42501", "row-level security", `INSERT INTO broadcasts (message_text) VALUES ('everyone')`)
	exec(t, staffO, `INSERT INTO broadcasts (message_text) VALUES ('everyone')`)
}

func sortedStrings(v ...string) []string {
	out := slices.Clone(v)
	slices.Sort(out)
	return out
}
