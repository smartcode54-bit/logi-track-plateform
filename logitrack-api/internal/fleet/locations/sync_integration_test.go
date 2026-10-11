//go:build integration

// Integration tests of the cartrack.sync consumer (T53, issue #73): PostgreSQL 18 with the full goose chain (pgtest),
// the consumer on the logitrack_app pool (its system transactions), a fake Cartrack client, fixture rows as
// logitrack_etl (BYPASSRLS, as cmd/seed writes). Fixture: own fleet O; carrier A working for O; trucks with GPS ids in
// both, one in the quarantine tenant, one without a GPS id.
package locations_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/locations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cartrack"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

// now is the pinned sync clock: 2026-10-11 10:00 in Bangkok.
var now = time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)

const quarantine = "00000000-0000-7000-8000-00000000000f"

type world struct {
	t    *testing.T
	pool *pgxpool.Pool
	etl  *pgx.Conn

	O, A                   uuid.UUID
	truckO, truckA, truckQ uuid.UUID
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
	w.A = w.id(`INSERT INTO tenants (kind, name_th, code, legal_type, contractor_tenant_id) VALUES ('carrier', 'ผู้รับเหมา A', 'CA', 'company', $1) RETURNING id`, w.O)
	w.truckO = w.truck(w.O, "1กข-1001", "1001")
	w.truckA = w.truck(w.A, "2กข-2002", "2002")
	w.truckQ = w.truck(uuid.MustParse(quarantine), "9กข-9009", "9009")
	w.truck(w.O, "1กข-1003", "")
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

func (w *world) truck(tenant uuid.UUID, plate, gps string) uuid.UUID {
	source := "self"
	if tenant.String() == quarantine {
		source = "quarantine"
	}
	return w.id(`INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand, model, year, color, type_raw,
		vehicle_class, gps_vehicle_id) VALUES ($1, $2, $3, 'กรุงเทพมหานคร', 'active', 'ISUZU', 'FRR', 2020, 'white', '6 Wheels', '6WH',
		nullif($4, '')) RETURNING id`, tenant, source, plate, gps)
}

func scan[T any](w *world, sql string, args ...any) T {
	w.t.Helper()
	var v T
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

// fake is a Cartrack client that answers what it was given and counts its calls.
type fake struct {
	mu     sync.Mutex
	status cartrack.Status
	err    error
	calls  int
}

func (f *fake) VehicleStatus(context.Context) (cartrack.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.status, f.err
}

func fp(v float64) *float64 { return &v }

func at(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func vehicle(id string, lat, lng float64, event string) cartrack.Vehicle {
	return cartrack.Vehicle{VehicleID: id, Lat: fp(lat), Lng: fp(lng), SpeedKmh: fp(42.5), Heading: fp(90), EventAt: at(event)}
}

// enqueue creates a cartrack.sync job with its lt.jobs command, as the cron (params) or the probe does.
func (w *world) enqueue(params map[string]any) (uuid.UUID, *mq.Delivery) {
	w.t.Helper()
	var job jobs.Job
	if err := db.WithSystem(context.Background(), w.pool, nil, func(tx pgx.Tx) error {
		var err error
		job, err = jobs.Enqueue(context.Background(), tx, jobs.NewInput{Type: jobs.TypeCartrackSync, Params: params})
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	body := scan[[]byte](w, `SELECT payload::text::bytea FROM outbox_events WHERE exchange = 'lt.jobs'
		AND routing_key = 'job.cartrack.sync' AND aggregate_id = $1`, job.ID.String())
	return job.ID, &mq.Delivery{Queue: locations.QueueCartrack, RoutingKey: locations.RouteCartrackJob, Body: body}
}

func (w *world) consumer(client cartrack.Client, clock time.Time) *locations.SyncJob {
	w.t.Helper()
	j, err := locations.NewSyncJob(locations.SyncDeps{Pool: w.pool, Cartrack: client, Now: func() time.Time { return clock }, Log: zerolog.Nop()})
	if err != nil {
		w.t.Fatal(err)
	}
	return j
}

func (w *world) job(id uuid.UUID) (status string, result map[string]any, cause string) {
	w.t.Helper()
	var raw []byte
	var errText *string
	if err := w.etl.QueryRow(context.Background(), `SELECT status, coalesce(result, '{}'::jsonb)::text::bytea, error FROM jobs WHERE id = $1`, id).
		Scan(&status, &raw, &errText); err != nil {
		w.t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &result)
	if errText != nil {
		cause = *errText
	}
	return status, result, cause
}

// AC: positions are upserted latest-only per truck. One run maps Cartrack vehicles to trucks by GPS id, skips the
// unknown vehicles, bad coordinates and the quarantined truck, writes one row per truck, and appends one realtime
// vehicle_locations.updated event per tenant (topic tenant:{tid}:vehicle_locations, the batch as payload).
func TestSyncUpsertsLatestOnlyPerTruck(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	f := &fake{status: cartrack.Status{HTTPStatus: 200, Vehicles: []cartrack.Vehicle{
		vehicle("1001", 13.75, 100.50, "2026-10-11T02:55:00Z"),
		vehicle("1001", 13.70, 100.40, "2026-10-11T02:50:00Z"), // an older duplicate of the same vehicle
		vehicle("2002", 13.60, 100.70, "2026-10-11T02:58:00Z"),
		vehicle("9009", 13.00, 100.00, "2026-10-11T02:58:00Z"), // quarantined truck
		vehicle("7777", 14.00, 101.00, "2026-10-11T02:58:00Z"), // not one of ours
		{VehicleID: "1003", Lat: nil, Lng: fp(1)},              // no GPS id on the truck: unmatched
		vehicle("2002", 0, 0, "2026-10-11T02:59:00Z"),          // no fix
	}}}
	f.status.Vehicles[0].DriverName, f.status.Vehicles[0].PositionDescription = "Somchai J", "Rama IV"
	id, d := w.enqueue(map[string]any{"scheduledFor": now.Add(-time.Minute).Format(time.RFC3339)})
	if err := w.consumer(f, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	status, res, cause := w.job(id)
	if status != "succeeded" || cause != "" || res["updated"] != 2.0 || res["unmatched"] != 3.0 || res["badCoordinates"] != 1.0 ||
		res["vehicles"] != 7.0 || res["trucks"] != 2.0 || res["tenants"] != 2.0 {
		t.Fatalf("job %s %v %q", status, res, cause)
	}
	if n := scan[int](w, `SELECT count(*) FROM vehicle_locations`); n != 2 {
		t.Fatalf("%d rows, want one per known truck", n)
	}
	row := func(truck uuid.UUID) (lat float64, tenant uuid.UUID, source, driver string, last time.Time) {
		if err := w.etl.QueryRow(ctx, `SELECT lat, tenant_id, tenant_source, coalesce(driver_name_raw, ''), last_gps_at FROM vehicle_locations
			WHERE truck_id = $1`, truck).Scan(&lat, &tenant, &source, &driver, &last); err != nil {
			t.Fatal(err)
		}
		return
	}
	if lat, tenant, source, driver, last := row(w.truckO); lat != 13.75 || tenant != w.O || source != "truck" || driver != "Somchai J" ||
		!last.Equal(*at("2026-10-11T02:55:00Z")) {
		t.Fatalf("truck O row %v %v %v %v %v", lat, tenant, source, driver, last)
	}
	if lat, tenant, _, _, _ := row(w.truckA); lat != 13.60 || tenant != w.A {
		t.Fatalf("truck A row %v %v", lat, tenant)
	}
	// One realtime-only event per tenant with its batch.
	type event struct {
		Tenant  uuid.UUID
		Topics  []string
		Payload []byte
	}
	rows, err := w.etl.Query(ctx, `SELECT tenant_id, realtime_topics, payload::text::bytea FROM outbox_events
		WHERE routing_key = 'vehicle_locations.updated' ORDER BY tenant_id`)
	if err != nil {
		t.Fatal(err)
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[event])
	if err != nil || len(events) != 2 {
		t.Fatalf("events %v %v", events, err)
	}
	for _, e := range events {
		var body struct {
			Positions []map[string]any `json:"positions"`
		}
		if err := json.Unmarshal(e.Payload, &body); err != nil || len(body.Positions) != 1 {
			t.Fatalf("batch %s", e.Payload)
		}
		batch := body.Positions
		if want := "tenant:" + e.Tenant.String() + ":vehicle_locations"; len(e.Topics) != 1 || e.Topics[0] != want {
			t.Fatalf("topics %v", e.Topics)
		}
		if batch[0]["truckId"] == nil || batch[0]["at"] == nil || batch[0]["speed"] != 42.5 {
			t.Fatalf("batch entry %v", batch[0])
		}
	}

	// A later run with an older fix for truck O keeps the stored one (latest only); a newer one for A replaces it.
	f.status.Vehicles = []cartrack.Vehicle{vehicle("1001", 1, 1, "2026-10-11T02:40:00Z"), vehicle("2002", 13.61, 100.71, "2026-10-11T03:01:00Z")}
	id, d = w.enqueue(map[string]any{"scheduledFor": now.Add(2 * time.Minute).Format(time.RFC3339)})
	if err := w.consumer(f, now.Add(3*time.Minute)).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, res, _ := w.job(id); res["updated"] != 1.0 || res["stale"] != 1.0 || res["tenants"] != 1.0 {
		t.Fatalf("second run %v", res)
	}
	if lat, _, _, _, _ := row(w.truckO); lat != 13.75 {
		t.Fatal("an older fix overwrote the stored position")
	}
	if lat, _, _, _, _ := row(w.truckA); lat != 13.61 {
		t.Fatal("a newer fix did not replace the stored position")
	}
	// A duplicate delivery of a finished command is acknowledged without a second fetch.
	calls := f.calls
	if err := w.consumer(f, now).Handle(ctx, d); err != nil || f.calls != calls {
		t.Fatalf("duplicate: %v, calls %d -> %d", err, calls, f.calls)
	}
}

// A failed fetch (a non-2xx answer, no answer) or the client off ends the job failed, writes nothing and names no
// credential; a stale slot (the worker was away) is skipped without a fetch.
func TestSyncFailuresWriteNothing(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	f := &fake{status: cartrack.Status{HTTPStatus: 401}, err: &cartrack.HTTPError{Status: 401}}
	id, d := w.enqueue(map[string]any{"scheduledFor": now.Format(time.RFC3339)})
	if err := w.consumer(f, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if status, res, cause := w.job(id); status != "failed" || cause != "cartrack: API HTTP 401" || res["httpStatus"] != 401.0 {
		t.Fatalf("401: %s %v %q", status, res, cause)
	}
	f = &fake{err: errors.New("cartrack: request failed: context deadline exceeded")}
	id, d = w.enqueue(map[string]any{"scheduledFor": now.Format(time.RFC3339)})
	if err := w.consumer(f, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if status, _, cause := w.job(id); status != "failed" || !strings.Contains(cause, "deadline") {
		t.Fatalf("timeout: %s %q", status, cause)
	}
	id, d = w.enqueue(map[string]any{"scheduledFor": now.Format(time.RFC3339)})
	if err := w.consumer(nil, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if status, _, cause := w.job(id); status != "failed" || cause != locations.ErrTextDisabled {
		t.Fatalf("off: %s %q", status, cause)
	}
	f = &fake{status: cartrack.Status{HTTPStatus: 200, Vehicles: []cartrack.Vehicle{vehicle("1001", 13, 100, "2026-10-11T02:55:00Z")}}}
	id, d = w.enqueue(map[string]any{"scheduledFor": now.Add(-10 * time.Minute).Format(time.RFC3339)})
	if err := w.consumer(f, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if status, res, _ := w.job(id); status != "succeeded" || res["skipped"] != "stale_slot" || f.calls != 0 {
		t.Fatalf("stale slot: %s %v calls %d", status, res, f.calls)
	}
	if n := scan[int](w, `SELECT count(*) FROM vehicle_locations`); n != 0 {
		t.Fatalf("%d positions written by failed runs", n)
	}
	if n := scan[int](w, `SELECT count(*) FROM outbox_events WHERE routing_key = 'vehicle_locations.updated'`); n != 0 {
		t.Fatalf("%d events of failed runs", n)
	}
}

// A probe run records only the HTTP status and the vehicle count, and writes no position.
func TestProbeRunWritesNothing(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	f := &fake{status: cartrack.Status{HTTPStatus: 200, Vehicles: []cartrack.Vehicle{vehicle("1001", 13, 100, "2026-10-11T02:55:00Z"),
		vehicle("7777", 13, 100, "2026-10-11T02:55:00Z")}}}
	id, d := w.enqueue(map[string]any{"probe": true})
	if err := w.consumer(f, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if status, res, _ := w.job(id); status != "succeeded" || res["probe"] != true || res["httpStatus"] != 200.0 || res["vehicleCount"] != 2.0 {
		t.Fatalf("probe %s %v", status, res)
	}
	// A 401 is still an answer: the probe succeeds and reports it.
	f = &fake{status: cartrack.Status{HTTPStatus: 401}, err: &cartrack.HTTPError{Status: 401}}
	id, d = w.enqueue(map[string]any{"probe": true})
	if err := w.consumer(f, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if status, res, _ := w.job(id); status != "succeeded" || res["httpStatus"] != 401.0 || res["vehicleCount"] != 0.0 {
		t.Fatalf("probe 401 %s %v", status, res)
	}
	// A 2xx answer that cannot be read (an HTML page, a cut-off body) fails the probe as it would fail every sync; the
	// status is kept and no vehicle count is reported.
	f = &fake{status: cartrack.Status{HTTPStatus: 200}, err: errors.New("cartrack: response is not JSON")}
	id, d = w.enqueue(map[string]any{"probe": true})
	if err := w.consumer(f, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if status, res, cause := w.job(id); status != "failed" || res["httpStatus"] != 200.0 || res["vehicleCount"] != nil ||
		cause != "cartrack: response is not JSON" {
		t.Fatalf("probe unreadable 2xx %s %v %q", status, res, cause)
	}
	id, d = w.enqueue(map[string]any{"probe": true})
	if err := w.consumer(nil, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if status, _, cause := w.job(id); status != "failed" || cause != locations.ErrTextDisabled {
		t.Fatalf("probe off %s %q", status, cause)
	}
	if n := scan[int](w, `SELECT count(*) FROM vehicle_locations`); n != 0 {
		t.Fatalf("a probe wrote %d positions", n)
	}
}

// A truck whose GPS id was cleared or moved to another truck drops its old position at the next sync: the list holds
// one row per truck that holds its GPS id, as the legacy document keyed by the GPS id followed its tracker.
func TestSyncRemovesPositionsOfMovedOrClearedGPSIDs(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	f := &fake{status: cartrack.Status{HTTPStatus: 200, Vehicles: []cartrack.Vehicle{
		vehicle("1001", 13.75, 100.50, "2026-10-11T02:55:00Z"), vehicle("2002", 13.60, 100.70, "2026-10-11T02:58:00Z")}}}
	_, d := w.enqueue(map[string]any{"scheduledFor": now.Format(time.RFC3339)})
	if err := w.consumer(f, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if n := scan[int](w, `SELECT count(*) FROM vehicle_locations`); n != 2 {
		t.Fatalf("%d rows after the first sync", n)
	}
	// A steward moves tracker 1001 from truck O to truck Y and clears truck A's id (as PATCH /v1/trucks/{id} would).
	if _, err := w.etl.Exec(ctx, `UPDATE trucks SET gps_vehicle_id = NULL WHERE id IN ($1, $2)`, w.truckO, w.truckA); err != nil {
		t.Fatal(err)
	}
	truckY := w.truck(w.O, "1กข-1004", "1001")
	f.status.Vehicles = []cartrack.Vehicle{vehicle("1001", 14.10, 100.60, "2026-10-11T03:01:00Z"),
		vehicle("2002", 13.61, 100.71, "2026-10-11T03:01:00Z")}
	id, d := w.enqueue(map[string]any{"scheduledFor": now.Add(3 * time.Minute).Format(time.RFC3339)})
	if err := w.consumer(f, now.Add(3*time.Minute)).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if status, res, _ := w.job(id); status != "succeeded" || res["removed"] != 2.0 || res["updated"] != 1.0 || res["unmatched"] != 1.0 {
		t.Fatalf("second sync %s %v", status, res)
	}
	if n := scan[int](w, `SELECT count(*) FROM vehicle_locations`); n != 1 {
		t.Fatalf("%d rows, want only the truck that holds 1001 now", n)
	}
	if truck := scan[uuid.UUID](w, `SELECT truck_id FROM vehicle_locations WHERE gps_vehicle_id = '1001'`); truck != truckY {
		t.Fatalf("1001 is on %s, want %s", truck, truckY)
	}
	// A run where nothing moved removes nothing.
	id, d = w.enqueue(map[string]any{"scheduledFor": now.Add(6 * time.Minute).Format(time.RFC3339)})
	if err := w.consumer(f, now.Add(6*time.Minute)).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, res, _ := w.job(id); res["removed"] != 0.0 {
		t.Fatalf("third sync %v", res)
	}
}

// A NUL in a Cartrack text value (PostgreSQL text refuses it, SQLSTATE 22021) is dropped before the write, so one
// vehicle cannot fail the sync of the whole fleet.
func TestSyncStoresTextWithoutNUL(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	v := vehicle("1001", 13.75, 100.50, "2026-10-11T02:55:00Z")
	v.PositionDescription, v.DriverName = "Rama IV\x00", "Som\x00chai"
	f := &fake{status: cartrack.Status{HTTPStatus: 200, Vehicles: []cartrack.Vehicle{v, vehicle("2002", 13.60, 100.70, "2026-10-11T02:58:00Z")}}}
	id, d := w.enqueue(map[string]any{"scheduledFor": now.Format(time.RFC3339)})
	if err := w.consumer(f, now).Handle(ctx, d); err != nil {
		t.Fatal(err)
	}
	if status, res, cause := w.job(id); status != "succeeded" || res["updated"] != 2.0 || res["tenants"] != 2.0 {
		t.Fatalf("job %s %v %q", status, res, cause)
	}
	if got := scan[string](w, `SELECT position_description || '|' || driver_name_raw FROM vehicle_locations WHERE truck_id = $1`, w.truckO); got != "Rama IV|Somchai" {
		t.Fatalf("stored %q", got)
	}
}
