//go:build integration

// Row-level security behaviour of the baseline as principals: logitrack_app sessions carrying the
// request GUCs that db.WithPrincipal will set (T07). These cover the Appendix C §C.3.5 / §C.3.6 rules
// a catalog check cannot see; the layout itself is checked by internal/platform/db/rls_catalog_test.go.
package migrations_test

import (
	"context"
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
			owner_kind, owner_id) VALUES ('private', $1, $2, 'maintenance_receipt', 'committed', now(), 'maintenance', $3)
			RETURNING id::text`, "maintenance/"+m+"/receipt.jpg", tenant, m)
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
// may only commit an upload. The tenant, identity and exposure columns stay as uploaded, and status
// moves only pending -> committed; WithSystem (re-home C.3.10, missing_at_source) is unaffected.
func TestFileObjectsUpdateOnlyCommits(t *testing.T) {
	d, _ := migrated(t)
	etl := connect(t, d, db.RoleETL)
	a := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'A') RETURNING id::text`)
	b := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th, legal_type) VALUES ('carrier', 'B', 'company') RETURNING id::text`)
	u := scalar[string](t, etl, `INSERT INTO users (email) VALUES ('driver@example.test') RETURNING id::text`)
	op := scalar[string](t, etl, `INSERT INTO users (email) VALUES ('ops@example.test') RETURNING id::text`)
	drv := principal(t, d, u, a, "driver", "app.driver_id", "0199c000-0000-7000-8000-0000000000d1")
	upload := func(key string) string {
		return scalar[string](t, drv, `INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, uploaded_by, expires_at)
			VALUES ('private', $1, $2, 'trip_photo', $3, now() + interval '1 hour') RETURNING id::text`, key, a, u)
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
