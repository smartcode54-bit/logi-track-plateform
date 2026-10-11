//go:build integration

// Integration tests of T28 (issue #48): the tenancy.orphan-scan consumer end to end against PostgreSQL 18 (pgtest)
// as logitrack_app: the scheduler's command (jobs.Enqueue, as the cron fires it), the consumer (jobs row running ->
// succeeded with the scan result, tenant_orphan_rows_total, outbox security.event tenant_orphans_detected in the same
// transaction), then the security.audit consumer appending that event exactly once (consumer_inbox).
package orphanscan_test

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy/orphanscan"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

var now = time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC) // 03:00 Bangkok, the cron slot

type world struct {
	t    *testing.T
	pool *pgxpool.Pool
	etl  *pgx.Conn
	job  *orphanscan.Job
	reg  *prometheus.Registry
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, pool: d.Pool(t, db.RoleApp), reg: prometheus.NewRegistry()}
	var err error
	if w.etl, err = pgx.Connect(ctx, d.URL(db.RoleETL)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.etl.Close(context.Background()) })
	if w.job, err = orphanscan.New(orphanscan.Deps{Pool: w.pool, Registerer: w.reg, Now: func() time.Time { return now },
		Log: zerolog.Nop()}); err != nil {
		t.Fatal(err)
	}
	return w
}

func scalar[T any](t *testing.T, c *pgx.Conn, sql string, args ...any) T {
	t.Helper()
	var v T
	if err := c.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
	return v
}

// gauge reads tenant_orphan_rows_total{table} from the registry; series is the number of tables it has a value for.
func (w *world) gauge(table string) (value float64, series int) {
	w.t.Helper()
	mf, err := w.reg.Gather()
	if err != nil {
		w.t.Fatal(err)
	}
	value = -1
	for _, f := range mf {
		if f.GetName() != orphanscan.MetricRows {
			continue
		}
		for _, m := range f.GetMetric() {
			series++
			for _, l := range m.GetLabel() {
				if l.GetName() == "table" && l.GetValue() == table {
					value = m.GetGauge().GetValue()
				}
			}
		}
	}
	return value, series
}

// fire enqueues a scan as the scheduler's command cron does and returns the job and the delivery of its command.
func (w *world) fire() (jobs.Job, *mq.Delivery) {
	w.t.Helper()
	ctx := context.Background()
	var j jobs.Job
	if err := db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
		var err error
		j, err = jobs.Enqueue(ctx, tx, jobs.NewInput{Type: orphanscan.JobType, Params: map[string]any{"scheduledFor": now}})
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	return j, w.delivery(orphanscan.Queue, `routing_key = 'job.tenancy.orphan-scan' AND aggregate_id = $1`, j.ID.String())
}

// delivery is the message the relay would publish for the outbox row matching where.
func (w *world) delivery(queue, where string, args ...any) *mq.Delivery {
	w.t.Helper()
	var id int64
	var key string
	var body []byte
	var tenant *uuid.UUID
	if err := w.etl.QueryRow(context.Background(), `SELECT id, routing_key, payload, tenant_id FROM outbox_events WHERE `+where,
		args...).Scan(&id, &key, &body, &tenant); err != nil {
		w.t.Fatalf("outbox row (%s): %v", where, err)
	}
	return &mq.Delivery{Queue: queue, MessageID: strconv.FormatInt(id, 10), RoutingKey: key, Body: body, TenantID: tenant}
}

func TestOrphanScanJob(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	q := uuid.MustParse(tenancy.QuarantineTenantID)
	own := scalar[uuid.UUID](t, w.etl, `INSERT INTO tenants (kind, name_th, code) VALUES ('own_fleet', 'กองรถ', 'OWN') RETURNING id`)
	task := func(tenant uuid.UUID, source string) {
		scalar[uuid.UUID](t, w.etl, `INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw)
			VALUES ($1, $2, 'first_mile', 'pending', '2026-10-11T01:00:00Z', 'ALANG-A', 'SOCE') RETURNING id`, tenant, source)
	}

	t.Run("a clean database raises nothing", func(t *testing.T) {
		task(own, "form")
		j, d := w.fire()
		if err := w.job.Handle(ctx, d); err != nil {
			t.Fatal(err)
		}
		if s := scalar[string](t, w.etl, `SELECT status FROM jobs WHERE id = $1`, j.ID); s != jobs.StatusSucceeded {
			t.Fatalf("job %s", s)
		}
		if n := scalar[int](t, w.etl, `SELECT count(*) FROM outbox_events WHERE routing_key = 'security.event'`); n != 0 {
			t.Fatalf("%d security events for a clean scan", n)
		}
		if v, _ := w.gauge("tasks"); v != 0 {
			t.Fatalf("%s{table=tasks} = %v", orphanscan.MetricRows, v)
		}
	})

	t.Run("quarantine rows: result, gauge and tenant_orphans_detected through security.audit", func(t *testing.T) {
		task(q, "quarantine")
		task(q, "quarantine")
		j, d := w.fire()
		if err := w.job.Handle(ctx, d); err != nil {
			t.Fatal(err)
		}
		var res tenancy.ScanResult
		if err := json.Unmarshal(scalar[[]byte](t, w.etl, `SELECT result FROM jobs WHERE id = $1 AND status = 'succeeded'`, j.ID), &res); err != nil {
			t.Fatal(err)
		}
		if res.QuarantineTotal != 2 || res.QuarantineRows["tasks"] != 2 || len(res.QuarantineRows) != 1 {
			t.Fatalf("job result %+v", res)
		}
		if v, _ := w.gauge("tasks"); v != 2 {
			t.Fatalf("%s{table=tasks} = %v, want 2", orphanscan.MetricRows, v)
		}
		if v, n := w.gauge("trip_records"); v != 0 || n < 10 {
			t.Fatalf("%s{table=trip_records} = %v over %d tables, want 0 and every tenant-stamped table", orphanscan.MetricRows, v, n)
		}
		ev := w.delivery(security.QueueAudit, `routing_key = 'security.event' AND aggregate_type = 'job' AND aggregate_id = $1`, j.ID.String())
		audit := &security.Audit{Pool: w.pool, Log: zerolog.Nop()}
		for range 2 { // a redelivery appends nothing (consumer_inbox)
			if err := audit.Handle(ctx, ev); err != nil {
				t.Fatal(err)
			}
		}
		var sev, summary string
		var tenant *uuid.UUID
		var details []byte
		var at time.Time
		if err := w.etl.QueryRow(ctx, `SELECT severity, summary, tenant_id, details, created_at FROM security_events
			WHERE event_type = 'tenant_orphans_detected'`).Scan(&sev, &summary, &tenant, &details, &at); err != nil {
			t.Fatalf("tenant_orphans_detected row: %v", err)
		}
		if n := scalar[int](t, w.etl, `SELECT count(*) FROM security_events WHERE event_type = 'tenant_orphans_detected'`); n != 1 {
			t.Fatalf("%d tenant_orphans_detected rows after a redelivery", n)
		}
		var det map[string]any
		_ = json.Unmarshal(details, &det)
		if sev != security.SeverityWarning || tenant != nil || !at.Equal(now) || det["quarantineTotal"] != float64(2) ||
			det["jobId"] != j.ID.String() {
			t.Fatalf("row: %s %q tenant %v at %s details %s", sev, summary, tenant, at, details)
		}
		// A duplicate of the finished command is acknowledged and raises nothing more.
		if err := w.job.Handle(ctx, d); err != nil {
			t.Fatal(err)
		}
		if n := scalar[int](t, w.etl, `SELECT count(*) FROM outbox_events WHERE routing_key = 'security.event'`); n != 1 {
			t.Fatalf("%d security events after a duplicate command", n)
		}
		// The rows stay where they are.
		if n := scalar[int](t, w.etl, `SELECT count(*) FROM tasks WHERE tenant_id = $1`, q); n != 2 {
			t.Fatalf("%d quarantined tasks after the scan", n)
		}
	})
}
