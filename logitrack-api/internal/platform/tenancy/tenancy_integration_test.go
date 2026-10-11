//go:build integration

// Integration tests of T28 (issue #48) for the tenancy package against PostgreSQL 18 with the full goose chain
// (pgtest): the frozen tenant_id outside a move path (t_freeze_tenant_id), the R13 task move (MoveTask), TenantOf
// under a request principal's RLS, and the read-only orphan scan (Scan) over quarantine rows and the two R24 drift
// buckets. Statements run as logitrack_app (db.WithPrincipal with principals built here, db.WithSystem for the system
// paths), fixture rows as logitrack_etl (BYPASSRLS), as cmd/seed writes them.
package tenancy_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

var quarantine = uuid.MustParse(tenancy.QuarantineTenantID)

type world struct {
	t    *testing.T
	pool *pgxpool.Pool
	etl  *pgx.Conn

	O, A, B, C uuid.UUID // own fleet; carriers A and B working for it; independent carrier C
	dA, dB     uuid.UUID // drivers of A and B
	tA, tB     uuid.UUID // trucks of A and B
	staffO     *authz.Principal
	staffA     *authz.Principal
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, pool: d.Pool(t, db.RoleApp)}
	var err error
	if w.etl, err = pgx.Connect(ctx, d.URL(db.RoleETL)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.etl.Close(context.Background()) })
	w.O = w.id(`INSERT INTO tenants (kind, name_th, code) VALUES ('own_fleet', 'กองรถ', 'OWN') RETURNING id`)
	w.A = w.id(`INSERT INTO tenants (kind, name_th, code, legal_type, contractor_tenant_id) VALUES ('carrier', 'A', 'CA', 'company', $1) RETURNING id`, w.O)
	w.B = w.id(`INSERT INTO tenants (kind, name_th, code, legal_type, contractor_tenant_id) VALUES ('carrier', 'B', 'CB', 'company', $1) RETURNING id`, w.O)
	w.C = w.id(`INSERT INTO tenants (kind, name_th, code, legal_type) VALUES ('carrier', 'C', 'CC', 'company') RETURNING id`)
	w.dA, w.dB = w.driver(w.A, "A1"), w.driver(w.B, "B1")
	w.tA, w.tB = w.truck(w.A, "1กข-1111"), w.truck(w.B, "2กข-2222")
	w.staffO = &authz.Principal{UserID: uuid.New(), TenantID: &w.O, TenantRole: authz.TenantAdmin, TenantKind: "own_fleet",
		Steward: true, SubtenantIDs: []uuid.UUID{w.A, w.B}}
	w.staffA = &authz.Principal{UserID: uuid.New(), TenantID: &w.A, TenantRole: authz.Manager, TenantKind: "carrier"}
	return w
}

func (w *world) id(sql string, args ...any) uuid.UUID {
	w.t.Helper()
	var id uuid.UUID
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		w.t.Fatalf("fixture: %v\n%s", err, sql)
	}
	return id
}

func (w *world) driver(tenant uuid.UUID, name string) uuid.UUID {
	return w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile) VALUES ($1, 'self', $2, 'T', '0800000000') RETURNING id`, tenant, name)
}

func (w *world) truck(tenant uuid.UUID, plate string) uuid.UUID {
	return w.id(`INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand, model, year, color, type_raw, vehicle_class)
		VALUES ($1, 'self', $2, 'กรุงเทพมหานคร', 'active', 'ISUZU', 'FRR', 2020, 'white', '6 Wheels', '6WH') RETURNING id`, tenant, plate)
}

// task inserts a task of tenant with source, driver and truck (nil = none).
func (w *world) task(tenant uuid.UUID, source string, driver, truck *uuid.UUID) uuid.UUID {
	return w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw, driver_id, truck_id)
		VALUES ($1, $2, 'first_mile', 'pending', '2026-10-11T01:00:00Z', 'ALANG-A', 'SOCE', $3, $4) RETURNING id`, tenant, source, driver, truck)
}

// fingerprint is the state of every operations row (xmin changes on any write), so a read-only scan is provable.
func (w *world) fingerprint() string {
	w.t.Helper()
	var s string
	if err := w.etl.QueryRow(context.Background(), `SELECT coalesce(string_agg(x, ',' ORDER BY x), '') FROM (
		SELECT 'task' || id::text || xmin::text AS x FROM tasks UNION ALL SELECT 'trip' || id::text || xmin::text FROM trip_records
		UNION ALL SELECT 'drv' || id::text || xmin::text FROM drivers) y`).Scan(&s); err != nil {
		w.t.Fatal(err)
	}
	return s
}

func code(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// AC: an UPDATE ... SET tenant_id outside a move path fails (t_freeze_tenant_id), for a request principal whose RLS
// would admit the target and for a system transaction; MoveTask, the R13 path, moves the task, its standby records and
// its files, and the deferred link checks accept the result at COMMIT.
func TestFrozenTenantAndMoveTask(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	task := w.task(w.A, "driver", &w.dA, nil)

	t.Run("staff whose reach holds both tenants", func(t *testing.T) {
		err := db.WithPrincipal(ctx, w.pool, w.staffO, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE tasks SET tenant_id = $1 WHERE id = $2`, w.B, task)
			return err
		})
		if code(err) != "23000" {
			t.Fatalf("UPDATE tasks SET tenant_id as staff: %v, want the frozen-tenant trigger (23000)", err)
		}
	})
	t.Run("system transaction without app.tenant_move", func(t *testing.T) {
		err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE tasks SET tenant_id = $1 WHERE id = $2`, w.B, task)
			return err
		})
		if code(err) != "23000" {
			t.Fatalf("UPDATE tasks SET tenant_id in a system transaction: %v, want 23000", err)
		}
	})

	standby := w.id(`INSERT INTO standby_records (tenant_id, tenant_source, task_id, driver_id) VALUES ($1, 'task', $2, $3) RETURNING id`, w.A, task, w.dA)
	file := w.id(`INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, status, committed_at, storage_backend, owner_kind, owner_id)
		VALUES ('logitrack', 'tasks/x/checkin.jpg', $1, 'task_checkin', 'committed', now(), 'local', 'task', $2) RETURNING id`, w.A, task)
	foreign := w.id(`INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, status, committed_at, storage_backend, owner_kind, owner_id)
		VALUES ('logitrack', 'tasks/x/other.jpg', $1, 'task_checkin', 'committed', now(), 'local', 'task', $2) RETURNING id`, w.C, task)
	var moved []tenancy.Moved
	if err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
		var err error
		moved, err = tenancy.MoveTask(ctx, tx, task, w.A, w.B)
		return err
	}); err != nil {
		t.Fatalf("MoveTask: %v", err)
	}
	if len(moved) != 2 || moved[0] != (tenancy.Moved{Table: "standby_records", ID: standby}) ||
		moved[1] != (tenancy.Moved{Table: tenancy.FileObjects, ID: file}) {
		t.Fatalf("moved %+v", moved)
	}
	var tenant uuid.UUID
	var source string
	if err := w.etl.QueryRow(ctx, `SELECT tenant_id, tenant_source FROM tasks WHERE id = $1`, task).Scan(&tenant, &source); err != nil || tenant != w.B || source != "form" {
		t.Fatalf("task after the move: %s %s %v", tenant, source, err)
	}
	if err := w.etl.QueryRow(ctx, `SELECT tenant_id, tenant_source FROM standby_records WHERE id = $1`, standby).Scan(&tenant, &source); err != nil || tenant != w.B || source != "task" {
		t.Fatalf("standby after the move: %s %s %v", tenant, source, err)
	}
	if err := w.etl.QueryRow(ctx, `SELECT tenant_id FROM file_objects WHERE id = $1`, foreign).Scan(&tenant); err != nil || tenant != w.C {
		t.Fatalf("a file another tenant registered moved: %s %v", tenant, err)
	}
	// The GUC is off again inside the same transaction: a second UPDATE after the move is frozen.
	err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
		if _, err := tenancy.MoveTask(ctx, tx, task, w.B, w.A); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE tasks SET tenant_id = $1 WHERE id = $2`, w.C, task)
		return err
	})
	if code(err) != "23000" {
		t.Fatalf("UPDATE after MoveTask in the same transaction: %v, want 23000", err)
	}

	t.Run("refusals", func(t *testing.T) {
		withTrip := w.task(w.A, "driver", &w.dA, nil)
		w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, task_id) VALUES ($1, 'task', 'TRIP-T28-1', 'in_transit', 'first_mile', $2) RETURNING id`, w.A, withTrip)
		for name, c := range map[string]struct {
			task, from, to uuid.UUID
			want           error
		}{
			"trip records":   {withTrip, w.A, w.B, tenancy.ErrTaskHasTrips},
			"quarantine":     {task, w.B, quarantine, tenancy.ErrBadTarget},
			"unknown target": {task, w.B, uuid.New(), tenancy.ErrBadTarget},
			"wrong from":     {task, w.C, w.A, tenancy.ErrNotInTenant},
		} {
			err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
				_, err := tenancy.MoveTask(ctx, tx, c.task, c.from, c.to)
				return err
			})
			if !errors.Is(err, c.want) {
				t.Errorf("%s: %v, want %v", name, err, c.want)
			}
		}
	})
}

// TenantOf reads under the caller's RLS: a row outside the reach, and a row of the quarantine tenant, are not found.
func TestTenantOf(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	q := w.task(quarantine, "quarantine", nil, nil)
	read := func(p *authz.Principal, table string, id uuid.UUID) (uuid.UUID, bool) {
		var tenant uuid.UUID
		var found bool
		if err := db.WithPrincipal(ctx, w.pool, p, func(tx pgx.Tx) error {
			var err error
			tenant, found, err = tenancy.TenantOf(ctx, tx, table, id)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return tenant, found
	}
	if tenant, ok := read(w.staffO, "drivers", w.dB); !ok || tenant != w.B {
		t.Fatalf("own-fleet staff read a contractor's driver: %s %v", tenant, ok)
	}
	if _, ok := read(w.staffA, "drivers", w.dB); ok {
		t.Fatal("carrier A read a driver of B")
	}
	if _, ok := read(w.staffO, "trucks", uuid.New()); ok {
		t.Fatal("an unknown truck was found")
	}
	var found bool
	if err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
		var err error
		_, found, err = tenancy.TenantOf(ctx, tx, "tasks", q)
		return err
	}); err != nil || found {
		t.Fatalf("a quarantined task handed on its tenant: %v %v", found, err)
	}
	if err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
		_, _, err := tenancy.TenantOf(ctx, tx, "users", uuid.New())
		return err
	}); err == nil {
		t.Fatal("TenantOf accepted a table no chain links to")
	}
}

// AC: the orphan scan reports quarantine rows per table and the two drift buckets of R24, and moves nothing.
func TestScan(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	scan := func() tenancy.ScanResult {
		var res tenancy.ScanResult
		if err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
			var err error
			res, err = tenancy.Scan(ctx, tx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return res
	}
	empty := scan()
	if empty.QuarantineTotal != 0 || len(empty.QuarantineRows) != 0 || len(empty.DriverMoved) != 0 || len(empty.TruckTenantMismatch) != 0 {
		t.Fatalf("a clean database: %+v", empty)
	}
	for _, table := range []string{"tasks", "trip_records", "standby_records", "incident_reports", "drivers", "trucks",
		"vehicle_expenses", "truck_assignments", "companies", "mobile_installations"} {
		if !slices.Contains(empty.Tables, table) {
			t.Errorf("the scan does not count %s (tables %v)", table, empty.Tables)
		}
	}
	if slices.Contains(empty.Tables, "billing_counters") {
		t.Error("the scan read a table logitrack_app holds no privilege on")
	}

	qTask := w.task(quarantine, "quarantine", nil, nil)
	w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, task_id) VALUES ($1, 'quarantine', 'TRIP-Q-1', 'in_transit', 'first_mile', $2) RETURNING id`, quarantine, qTask)
	moved := w.task(w.A, "driver", &w.dB, nil)      // stamped from a driver who is in B now
	mismatch := w.task(w.A, "driver", &w.dA, &w.tB) // stamped from A's driver, B's truck
	w.task(w.A, "form", &w.dB, nil)                 // form: the tenant was chosen, not derived from the driver
	w.task(w.A, "driver", &w.dA, &w.tA)             // consistent
	w.task(quarantine, "quarantine", &w.dB, &w.tA)  // quarantine rows are never drift
	movedTrip := w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, driver_id, truck_id)
		VALUES ($1, 'driver', 'TRIP-D-1', 'delivered', 'first_mile', $2, $3) RETURNING id`, w.A, w.dB, w.tB)
	before := w.fingerprint()
	res := scan()
	if after := w.fingerprint(); after != before {
		t.Fatal("the scan wrote a row")
	}
	if res.QuarantineTotal != 3 || res.QuarantineRows["tasks"] != 2 || res.QuarantineRows["trip_records"] != 1 || len(res.QuarantineRows) != 2 {
		t.Fatalf("quarantine rows %v total %d", res.QuarantineRows, res.QuarantineTotal)
	}
	want := map[uuid.UUID]tenancy.Drift{
		moved:     {Table: "tasks", ID: moved, DriverID: w.dB, RowTenant: w.A, DriverTenant: &w.B},
		movedTrip: {Table: "trip_records", ID: movedTrip, DriverID: w.dB, RowTenant: w.A, DriverTenant: &w.B},
	}
	if res.DriverMovedTotal != 2 || len(res.DriverMoved) != 2 {
		t.Fatalf("driverMoved %+v (total %d)", res.DriverMoved, res.DriverMovedTotal)
	}
	for _, d := range res.DriverMoved {
		if wd, ok := want[d.ID]; !ok || d.Table != wd.Table || d.DriverID != wd.DriverID || d.RowTenant != wd.RowTenant ||
			d.DriverTenant == nil || *d.DriverTenant != w.B || d.TruckID != nil {
			t.Errorf("driverMoved entry %+v", d)
		}
	}
	wantMismatch := map[uuid.UUID]string{mismatch: "tasks", movedTrip: "trip_records"}
	if res.TruckTenantMismatchTotal != 2 || len(res.TruckTenantMismatch) != 2 {
		t.Fatalf("truckTenantMismatch %+v (total %d)", res.TruckTenantMismatch, res.TruckTenantMismatchTotal)
	}
	for _, d := range res.TruckTenantMismatch {
		if wantMismatch[d.ID] != d.Table || d.RowTenant != w.A || d.TruckID == nil || *d.TruckID != w.tB ||
			d.TruckTenant == nil || *d.TruckTenant != w.B || d.DriverTenant != nil {
			t.Errorf("truckTenantMismatch entry %+v", d)
		}
	}
}
