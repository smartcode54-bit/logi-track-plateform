//go:build integration

// The two coexistence consumers of the worker (T24S) as cmd/worker runs them: etl.sync on its logitrack_etl pool with
// its jobs row on the logitrack_app pool, firestore.shadow as logitrack_app in db.WithSystem (minting document ids on
// RLS-forced tables), both against the in-process Firestore stand-in.
package coexist

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

type fixture struct {
	ctx      context.Context
	app, etl *pgxpool.Pool
	fs       *gcptest.Backend
}

// newFixture migrates every version, loads the fixture dump (as `etl load` does) and fills the Firestore stand-in
// with the same documents.
func newFixture(t *testing.T, loaded bool) *fixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	f := &fixture{ctx: ctx, app: d.Pool(t, db.RoleApp), etl: d.Pool(t, db.RoleETL), fs: gcptest.New(t, "logitrack-test")}
	if !loaded {
		return f
	}
	fx, err := dump.OpenDir(filepath.Join("..", "..", "cmd", "etl", "testdata", "firestore-fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := etl.New(etl.Config{Tx: func(ctx context.Context, fn func(pgx.Tx) error) error { return db.WithETL(ctx, f.etl, fn) },
		OwnFleetTenantID: uuid.MustParse("01900000-0000-7000-8000-000000000001"), Bucket: "logitrack", Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Load(ctx, fx, etl.LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, n := range fx.Names() {
		docs, err := fx.Read(n)
		if err != nil {
			t.Fatal(err)
		}
		for _, doc := range docs {
			f.fs.Put(doc)
		}
	}
	return f
}

// command enqueues an etl.sync job as the scheduler's cron does and returns its delivery.
func (f *fixture) command(t *testing.T) (*mq.Delivery, uuid.UUID) {
	t.Helper()
	var job jobs.Job
	if err := db.WithSystem(f.ctx, f.app, nil, func(tx pgx.Tx) error {
		var err error
		job, err = jobs.Enqueue(f.ctx, tx, jobs.NewInput{Type: TypeSync})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(jobs.Command{JobID: job.ID, Type: TypeSync})
	return &mq.Delivery{Queue: QueueSync, RoutingKey: RouteSync, Body: body}, job.ID
}

func (f *fixture) job(t *testing.T, id uuid.UUID) jobs.Job {
	t.Helper()
	j, ok, err := jobs.Get(f.ctx, f.app, id)
	if err != nil || !ok {
		t.Fatalf("job %s: %v %v", id, ok, err)
	}
	return j
}

// counter reads the series {collection, result} of a counter family.
func counter(t *testing.T, reg *prometheus.Registry, name, collection, result string) float64 {
	t.Helper()
	mf, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range mf {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["collection"] == collection && labels["result"] == result {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func plan(t *testing.T, owned string) Plan {
	t.Helper()
	p, errs := ParsePlan(owned, "", "")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return p
}

// A sweep command runs the mirror on the ETL login and ends its job with the counts; a Firestore change reaches
// PostgreSQL. A worker whose mirror is off, an ETL_DATABASE_URL of another login and a database without the P1 load
// fail the job instead.
func TestSyncConsumer(t *testing.T) {
	f := newFixture(t, true)
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)
	p := plan(t, "auth")
	s, err := NewSync(SyncConfig{Enabled: true, AppPool: f.app, ETLPool: f.etl, Source: f.fs.Firestore(), Plan: p.Mirror,
		Bucket: "logitrack", PublicBucket: "logitrack-public", Log: zerolog.Nop(), Metrics: metrics})
	if err != nil {
		t.Fatal(err)
	}
	f.fs.Merge("drivers/drv_own", map[string]any{"mobile": "0899999999"})
	d, id := f.command(t)
	if err := s.Handle(f.ctx, d); err != nil {
		t.Fatal(err)
	}
	j := f.job(t, id)
	var result struct {
		Applied     int `json:"applied"`
		Collections map[string]struct {
			Mode    string         `json:"mode"`
			Applied map[string]int `json:"applied"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(j.Result, &result); err != nil || j.Status != jobs.StatusSucceeded || result.Applied != 1 ||
		result.Collections["drivers"].Applied["loaded"] != 1 || result.Collections["drivers"].Mode != "full" {
		t.Fatalf("job %s %s %v %v", j.Status, j.Result, j.Error, err)
	}
	var mobile string
	if err := f.etl.QueryRow(f.ctx, `SELECT mobile FROM drivers WHERE legacy_doc_id = 'drv_own'`).Scan(&mobile); err != nil || mobile != "0899999999" {
		t.Errorf("mobile %q %v", mobile, err)
	}
	if v := counter(t, reg, "mirror_apply_total", "drivers", "loaded"); v != 1 {
		t.Errorf("mirror_apply_total %v", v)
	}
	// A redelivery of the finished command does nothing.
	if err := s.Handle(f.ctx, d); err != nil {
		t.Fatal(err)
	}

	off, _ := NewSync(SyncConfig{AppPool: f.app, Log: zerolog.Nop()})
	d, id = f.command(t)
	if err := off.Handle(f.ctx, d); err != nil {
		t.Fatal(err)
	}
	if j := f.job(t, id); j.Status != jobs.StatusFailed || j.Error == nil || *j.Error != ErrTextSyncOff {
		t.Errorf("mirror off: %s %v", j.Status, j.Error)
	}

	wrong, _ := NewSync(SyncConfig{Enabled: true, AppPool: f.app, ETLPool: f.app, Source: f.fs.Firestore(), Plan: p.Mirror, Log: zerolog.Nop()})
	d, id = f.command(t)
	if err := wrong.Handle(f.ctx, d); err != nil {
		t.Fatal(err)
	}
	if j := f.job(t, id); j.Status != jobs.StatusFailed || j.Error == nil || !strings.Contains(*j.Error, "logitrack_etl") {
		t.Errorf("app login as the ETL pool: %s %v", j.Status, j.Error)
	}

	empty := newFixture(t, false)
	s2, _ := NewSync(SyncConfig{Enabled: true, AppPool: empty.app, ETLPool: empty.etl, Source: empty.fs.Firestore(), Plan: p.Mirror, Log: zerolog.Nop()})
	d, id = empty.command(t)
	if err := s2.Handle(empty.ctx, d); err != nil {
		t.Fatal(err)
	}
	if j := empty.job(t, id); j.Status != jobs.StatusFailed || j.Error == nil || !strings.Contains(*j.Error, "P1 initial load") {
		t.Errorf("before the P1 load: %s %v", j.Status, j.Error)
	}
	if err := s.Handle(f.ctx, &mq.Delivery{RoutingKey: "job.other"}); !mq.IsPermanent(err) {
		t.Errorf("another routing key: %v", err)
	}
}

// The projection runs as logitrack_app in db.WithSystem: it mints document ids on the RLS-forced tables (tenants
// included, whose legacy_doc_id is platform-only) and writes each event's rows; with the kill switch off it writes
// nothing.
func TestShadowConsumerAsTheAppLogin(t *testing.T) {
	f := newFixture(t, true)
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)
	s, err := NewShadow(ShadowConfig{Enabled: true, Pool: f.app, FS: f.fs.Firestore(), Plan: plan(t, "auth,masterdata"), Log: zerolog.Nop(),
		Metrics: metrics})
	if err != nil {
		t.Fatal(err)
	}
	var carrier, hub, driver, truck, assignment uuid.UUID
	err = db.WithSystem(f.ctx, f.app, nil, func(tx pgx.Tx) error {
		if err := tx.QueryRow(f.ctx, `INSERT INTO tenants (kind, name_th, legal_type, contractor_tenant_id)
			VALUES ('carrier', 'รถร่วมใหม่', 'individual', '01900000-0000-7000-8000-000000000001') RETURNING id`).Scan(&carrier); err != nil {
			return err
		}
		if _, err := tx.Exec(f.ctx, `INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', $1)`, carrier); err != nil {
			return err
		}
		if err := tx.QueryRow(f.ctx, `INSERT INTO hubs (source_id, name_th, network, station_type) VALUES ('SOCZ', 'ศูนย์ใหม่', 'SPX', 'SOC')
			RETURNING id`).Scan(&hub); err != nil {
			return err
		}
		if err := tx.QueryRow(f.ctx, `INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, status)
			VALUES ($1, 'self', 'New', 'Driver', '0800000000', 'active') RETURNING id`, carrier).Scan(&driver); err != nil {
			return err
		}
		if err := tx.QueryRow(f.ctx, `SELECT id FROM trucks WHERE legacy_doc_id = 'trk_own1'`).Scan(&truck); err != nil {
			return err
		}
		return tx.QueryRow(f.ctx, `INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status)
			SELECT tenant_id, 'truck', $1, $2, 'active' FROM trucks WHERE id = $1 RETURNING id`, truck, driver).Scan(&assignment)
	})
	if err != nil {
		t.Fatal(err)
	}
	events := []struct{ key, body string }{
		{"tenant.created", `{"tenantId":"` + carrier.String() + `"}`},
		{"hubs.changed", `{"ids":["` + hub.String() + `"]}`},
		{"driver.created", `{"entity":"driver","id":"` + driver.String() + `"}`},
		{"assignment.created", `{"entity":"truck_assignment","id":"` + assignment.String() + `","truckId":"` + truck.String() +
			`","driverId":"` + driver.String() + `"}`},
		{"holidays.changed", `{}`},
	}
	for _, e := range events {
		if err := s.Handle(f.ctx, &mq.Delivery{RoutingKey: e.key, Body: []byte(e.body)}); err != nil {
			t.Fatalf("%s: %v", e.key, err)
		}
	}
	for _, path := range []string{"subcontractors/" + carrier.String(), "hubs/" + hub.String(), "drivers/" + driver.String(),
		"truckAssignment/" + assignment.String()} {
		if _, ok := f.fs.Doc(path); !ok {
			t.Errorf("no document %s", path)
		}
	}
	var minted int
	if err := f.etl.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM tenants WHERE id = $1 AND legacy_doc_id = id::text)
		+ (SELECT count(*) FROM hubs WHERE id = $2 AND legacy_doc_id = id::text) + (SELECT count(*) FROM drivers WHERE id = $3 AND legacy_doc_id = id::text)
		+ (SELECT count(*) FROM truck_assignments WHERE id = $4 AND legacy_doc_id = id::text)`, carrier, hub, driver, assignment).Scan(&minted); err != nil || minted != 4 {
		t.Errorf("minted ids %d %v", minted, err)
	}
	tr, _ := f.fs.Doc("trucks/trk_own1")
	if list, _ := tr.Fields["currentAssignments"].([]any); len(list) != 2 {
		t.Errorf("the truck's current assignments: %v", tr.Fields["currentAssignments"])
	}
	drv, _ := f.fs.Doc("drivers/" + driver.String())
	if drv.Fields["subcontractorId"] != carrier.String() || drv.Fields["status"] != "Active" {
		t.Errorf("driver document %v", drv.Fields)
	}
	if v := counter(t, reg, "projection_writes_total", "hubs", "written"); v != 1 {
		t.Errorf("projection_writes_total %v", v)
	}

	writes := len(f.fs.Writes())
	off, _ := NewShadow(ShadowConfig{Plan: plan(t, "auth,masterdata"), Log: zerolog.Nop()})
	if err := off.Handle(f.ctx, &mq.Delivery{RoutingKey: "hubs.changed", Body: []byte(`{"ids":["` + hub.String() + `"]}`)}); err != nil {
		t.Fatal(err)
	}
	if len(f.fs.Writes()) != writes {
		t.Error("the kill switch did not stop the projection")
	}
}
