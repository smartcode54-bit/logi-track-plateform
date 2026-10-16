//go:build integration

// Integration tests of T27 (issue #47): the operations data layer against PostgreSQL 18 with the full goose chain
// (pgtest). Every request statement runs as logitrack_app through db.WithPrincipal with a principal built here (the
// GUCs RLS reads are what matter; the HTTP routes and their capability guards ship with T30 / T31), the fixture rows as
// logitrack_etl (BYPASSRLS, as cmd/seed and cmd/etl write). Fixture: own fleet O; carrier A working for O (contractor
// reach); independent carrier C; drivers of O and A; customers X and Y; a hub pair; one truck per tenant; committed
// file objects for the photos.
package operations_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// now is the pinned clock: 2026-10-11 10:00 in Bangkok.
var now = time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

type world struct {
	t     *testing.T
	db    *pgtest.Database
	pool  *pgxpool.Pool // logitrack_app, 24 connections (the concurrency tests)
	etl   *pgx.Conn     // logitrack_etl: fixtures (BYPASSRLS)
	owner *pgx.Conn     // logitrack_migrator: ANALYZE, catalog reads
	svc   *operations.Service

	O, A, C          uuid.UUID // tenants: own fleet, its carrier, an independent carrier
	userO, userA     uuid.UUID // staff accounts (created_by, renamed_by)
	dO1, dO2, dA1    uuid.UUID // drivers
	truckO, truckA   uuid.UUID
	hubSrc, hubDst   uuid.UUID
	partyX, partyY   uuid.UUID // customer billing parties
	files            []uuid.UUID
	staffO, staffA   *authz.Principal
	staffC, driverO1 *authz.Principal
	driverO2, all    *authz.Principal
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := db.NewPool(ctx, d.URL(db.RoleApp), "t27", db.Options{MaxConns: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	w := &world{t: t, db: d, pool: pool}
	if w.etl, err = pgx.Connect(ctx, d.URL(db.RoleETL)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.etl.Close(context.Background()) })
	if w.owner, err = pgx.Connect(ctx, d.URL(db.RoleMigrator)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.owner.Close(context.Background()) })
	if w.svc, err = operations.New(operations.Deps{Pool: pool, Log: zerolog.Nop(), Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	w.fixture()
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

func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.etl.Exec(context.Background(), sql, args...); err != nil {
		w.t.Fatalf("fixture: %v\n%s", err, sql)
	}
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, code) VALUES ('own_fleet', 'กองรถ', 'OWN') RETURNING id`)
	w.A = w.id(`INSERT INTO tenants (kind, name_th, code, legal_type, contractor_tenant_id) VALUES ('carrier', 'ผู้รับเหมา A', 'CA', 'company', $1) RETURNING id`, w.O)
	w.C = w.id(`INSERT INTO tenants (kind, name_th, code, legal_type) VALUES ('carrier', 'อิสระ C', 'CC', 'company') RETURNING id`)
	w.userO = w.id(`INSERT INTO users (email, display_name) VALUES ('o@example.test', 'Staff O') RETURNING id`)
	w.userA = w.id(`INSERT INTO users (email, display_name) VALUES ('a@example.test', 'Staff A') RETURNING id`)
	driver := func(tenant uuid.UUID, name string) uuid.UUID {
		return w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile) VALUES ($1, 'self', $2, 'T', '0800000000') RETURNING id`, tenant, name)
	}
	w.dO1, w.dO2, w.dA1 = driver(w.O, "O1"), driver(w.O, "O2"), driver(w.A, "A1")
	truck := func(tenant uuid.UUID, plate string) uuid.UUID {
		return w.id(`INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand, model, year, color, type_raw, vehicle_class)
			VALUES ($1, 'self', $2, 'กรุงเทพมหานคร', 'active', 'ISUZU', 'FRR', 2020, 'white', '6 Wheels', '6WH') RETURNING id`, tenant, plate)
	}
	w.truckO, w.truckA = truck(w.O, "1กข-1234"), truck(w.A, "2กข-5678")
	w.hubSrc = w.id(`INSERT INTO hubs (source_id, name_th, network) VALUES ('ALANG-A', 'วังทองหลาง', 'SPX') RETURNING id`)
	w.hubDst = w.id(`INSERT INTO hubs (source_id, name_th, network, station_type) VALUES ('SOCE', 'บัวโรย', 'SPX', 'SOC') RETURNING id`)
	party := func(code string) uuid.UUID {
		c := w.id(`INSERT INTO customers (code, name) VALUES ($1::text, $1::text) RETURNING id`, code)
		return w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id`, c)
	}
	w.partyX, w.partyY = party("CJSF"), party("SPX")
	for i := range 8 {
		w.files = append(w.files, w.id(`INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, status, committed_at, storage_backend, content_type)
			VALUES ('logitrack', $1, $2, 'trip_photo', 'committed', now(), 'local', 'image/jpeg') RETURNING id`,
			fmt.Sprintf("trips/fixture/%d.jpg", i), w.O))
	}

	w.staffO = &authz.Principal{UserID: w.userO, TenantID: &w.O, TenantRole: authz.TenantAdmin, TenantKind: "own_fleet",
		Steward: true, SubtenantIDs: []uuid.UUID{w.A}}
	w.staffA = &authz.Principal{UserID: w.userA, TenantID: &w.A, TenantRole: authz.TenantAdmin, TenantKind: "carrier"}
	w.staffC = &authz.Principal{UserID: uuid.New(), TenantID: &w.C, TenantRole: authz.Manager, TenantKind: "carrier"}
	w.driverO1 = &authz.Principal{UserID: uuid.New(), TenantID: &w.O, TenantRole: authz.Driver, DriverID: &w.dO1, TenantKind: "own_fleet"}
	w.driverO2 = &authz.Principal{UserID: uuid.New(), TenantID: &w.O, TenantRole: authz.Driver, DriverID: &w.dO2, TenantKind: "own_fleet"}
	w.all = &authz.Principal{UserID: uuid.New(), Platform: []authz.PlatformRole{authz.PlatformAdmin}}
}

// tx runs fn in a request transaction of p (db.WithPrincipal on the app pool; operations.MapError on the result).
func (w *world) tx(p *authz.Principal, fn func(tx pgx.Tx) error) error {
	return w.svc.Tx(context.Background(), p, fn)
}

// newTask is a valid first-mile task of tenant on the plan instant.
func (w *world) newTask(tenant uuid.UUID, planAt time.Time) operations.NewTask {
	return operations.NewTask{TaskInsert: operations.TaskInsert{TenantID: tenant, TenantSource: "form",
		TaskType: operations.FirstMile, Status: operations.TaskPending, PlanAt: planAt, PlanTime: ptr("09:00"),
		SourceHubRaw: "ALANG-A", SourceHubID: &w.hubSrc, DestinationRaw: "SOCE", DestinationHubID: &w.hubDst,
		BillingPartyID: &w.partyX, CreatedBy: &w.userO}}
}

// createTask creates a task as p and fails the test on an error.
func (w *world) createTask(p *authz.Principal, in operations.NewTask) operations.Task {
	w.t.Helper()
	var task operations.Task
	if err := w.tx(p, func(tx pgx.Tx) error {
		var err error
		task, _, err = operations.CreateTask(context.Background(), tx, in)
		return err
	}); err != nil {
		w.t.Fatalf("create task: %v", err)
	}
	return task
}

// newTrip is an in-transit trip of the task.
func (w *world) newTrip(task operations.Task, tripNo string) operations.NewTrip {
	return operations.NewTrip{TripInsert: operations.TripInsert{TenantID: task.TenantID, TenantSource: "task",
		TripNo: tripNo, Status: operations.TripInTransit, JobType: task.TaskType, TaskID: &task.ID,
		DriverID: task.DriverID, OriginRaw: ptr(task.SourceHubRaw), DestinationRaw: ptr(task.DestinationRaw),
		BillingPartyID: task.BillingPartyID}}
}

func (w *world) createTrip(p *authz.Principal, in operations.NewTrip) operations.Trip {
	w.t.Helper()
	var trip operations.Trip
	if err := w.tx(p, func(tx pgx.Tx) error {
		var err error
		trip, err = operations.CreateTrip(context.Background(), tx, in)
		return err
	}); err != nil {
		w.t.Fatalf("create trip: %v", err)
	}
	return trip
}

func ptr[T any](v T) *T { return &v }
