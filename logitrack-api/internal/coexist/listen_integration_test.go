//go:build integration

// The mirror's listeners and the daily reconciliation as the worker-sync replica runs them (T29): the loop polls the
// operations collections while it holds lock:mirror:listen, applies a change within the poll, records the
// read-your-writes ack the write-back handlers wait for (cache:mirror_ack, R53) and mirror_lag_seconds; the daily
// etl.reconcile job succeeds on a match and fails naming the mismatches.
package coexist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// histogramCount is the sample count of mirror_lag_seconds{collection, path}.
func histogramCount(t *testing.T, reg *prometheus.Registry, name, collection, path string) uint64 {
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
			if labels["collection"] == collection && labels["path"] == path {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func eventsCount(t *testing.T, reg *prometheus.Registry, collection string) float64 {
	t.Helper()
	mf, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range mf {
		if f.GetName() != "mirror_events_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "collection" && l.GetValue() == collection {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func TestRedisLeaseHasOneHolder(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	ctx := context.Background()
	a := NewRedisLease(rdb, ks.MirrorListenLock(), time.Minute)
	b := NewRedisLease(rdb, ks.MirrorListenLock(), time.Minute)
	if ok, err := a.Acquire(ctx); !ok || err != nil {
		t.Fatalf("a: %v %v", ok, err)
	}
	if ok, err := b.Acquire(ctx); ok || err != nil {
		t.Fatalf("b while a holds it: %v %v", ok, err)
	}
	if ok, err := a.Acquire(ctx); !ok || err != nil {
		t.Fatalf("a renews: %v %v", ok, err)
	}
	if err := b.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.Acquire(ctx); ok {
		t.Fatal("b's release freed a's lease")
	}
	if err := a.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Acquire(ctx); !ok || err != nil {
		t.Fatalf("b after a released: %v %v", ok, err)
	}
	if !strings.HasSuffix(ks.MirrorListenLock(), ":lock:mirror:listen") {
		t.Fatalf("key %s", ks.MirrorListenLock())
	}
}

// AC (p95 lag < 5 s, measured in dev: hitl): a change reaches PostgreSQL within one poll of the listener, its ack is
// what the write-back handler waits for, and the lag and events are counted. A second loop started while the first
// holds the lease applies nothing.
func TestListenerAppliesWithinAPollAndAcks(t *testing.T) {
	f := newFixture(t, true)
	p := plan(t, "auth,masterdata")
	rdb, ks := cachetest.NewClient(t)
	acks := cache.New(rdb, ks)
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	newListener := func() *Listener {
		l, err := NewListener(ListenConfig{Interval: 50 * time.Millisecond, ETLPool: f.etl, Source: f.fs.Firestore(), Plan: p.Mirror,
			Bucket: "logitrack", PublicBucket: "logitrack-public", Lease: NewRedisLease(rdb, ks.MirrorListenLock(), ListenLeaseTTL),
			Acks: acks, Log: zerolog.Nop(), Metrics: m})
		if err != nil || l == nil {
			t.Fatalf("listener %v %v", l, err)
		}
		return l
	}
	if off, err := NewListener(ListenConfig{Interval: 0, Plan: p.Mirror}); off != nil || err != nil {
		t.Fatalf("interval 0 must turn the listeners off: %v %v", off, err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{}, 2)
	first, second := newListener(), newListener()
	go func() { first.Run(ctx); done <- struct{}{} }()
	time.Sleep(200 * time.Millisecond) // the first takes the lease
	go func() { second.Run(ctx); done <- struct{}{} }()

	now := time.Now().UTC()
	doc := f.fs.Set("trip_records/TRIP-LIVE", map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
		"driverId": "uid_own", "createdAt": dump.Timestamp{Time: now}, "updatedAt": dump.Timestamp{Time: now}})
	ok, err := acks.WaitMirrorAck(f.ctx, "trip_records", "TRIP-LIVE", doc.UpdateTime, 5*time.Second)
	if err != nil || !ok {
		t.Fatalf("no ack within 5 s: %v %v", ok, err)
	}
	var n int
	if err := f.etl.QueryRow(f.ctx, `SELECT count(*) FROM trip_records WHERE legacy_doc_id = 'TRIP-LIVE'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("row %d %v", n, err)
	}
	time.Sleep(300 * time.Millisecond) // both loops poll a few more times
	if c := histogramCount(t, reg, "mirror_lag_seconds", "trip_records", PathListener); c != 1 {
		t.Errorf("mirror_lag_seconds{path=listener} samples %d (one apply, by one loop)", c)
	}
	if c := histogramCount(t, reg, "mirror_lag_seconds", "trip_records", PathSweep); c != 0 {
		t.Errorf("mirror_lag_seconds{path=sweep} samples %d (no sweep ran)", c)
	}
	if v := eventsCount(t, reg, "trip_records"); v != 1 {
		t.Errorf("mirror_events_total %v", v)
	}
	if v := counter(t, reg, "mirror_apply_total", "trip_records", "loaded"); v != 1 {
		t.Errorf("mirror_apply_total %v", v)
	}
	cancel()
	<-done
	<-done
	if n, err := rdb.Exists(f.ctx, ks.MirrorListenLock()).Result(); err != nil || n != 0 {
		t.Errorf("the lease outlived the loops: %d %v", n, err)
	}
}

// AC: the daily reconciliation of the coexistence exits clean on the fixtures (the job succeeds) and fails naming the
// mismatches when a row differs from its document.
func TestReconcileJob(t *testing.T) {
	f := newFixture(t, true)
	p := plan(t, "auth,masterdata")
	reg := prometheus.NewRegistry()
	s, err := NewSync(SyncConfig{Enabled: true, AppPool: f.app, ETLPool: f.etl, Source: f.fs.Firestore(), Plan: p.Mirror, Projection: p.Projection,
		Bucket: "logitrack", PublicBucket: "logitrack-public", Log: zerolog.Nop(), Metrics: NewMetrics(reg)})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := f.command(t)
	if err := s.Handle(f.ctx, d); err != nil {
		t.Fatal(err)
	}
	// The catch-up projection pass that precedes COMPAT_PROJECTION_ENABLED (main spec §13.9 step 4).
	proj := &etl.Projector{Tx: func(ctx context.Context, fn func(pgx.Tx) error) error { return db.WithSystem(ctx, f.app, nil, fn) },
		FS: f.fs.Firestore(), Collections: p.Projection, Owned: p.Owned, Log: zerolog.Nop()}
	if _, err := proj.ProjectAll(f.ctx); err != nil {
		t.Fatal(err)
	}
	d, _ = f.command(t) // the next sweep records the projected documents
	if err := s.Handle(f.ctx, d); err != nil {
		t.Fatal(err)
	}
	d, id := f.commandOf(t, TypeReconcile)
	if d.RoutingKey != RouteReconcile {
		t.Fatalf("routing key %s", d.RoutingKey)
	}
	if err := s.Handle(f.ctx, d); err != nil {
		t.Fatal(err)
	}
	j := f.job(t, id)
	var result struct {
		Match      bool `json:"match"`
		Checks     int  `json:"checks"`
		Mismatches int  `json:"mismatches"`
	}
	if err := json.Unmarshal(j.Result, &result); err != nil || j.Status != jobs.StatusSucceeded || !result.Match || result.Checks == 0 {
		t.Fatalf("reconcile on the fixtures: %s %s %v", j.Status, j.Result, j.Error)
	}
	if c := histogramCount(t, reg, "mirror_lag_seconds", "trip_records", PathListener); c != 0 {
		t.Errorf("a sweep observed mirror_lag_seconds{path=listener}: %d", c)
	}

	// The 3.x app keeps writing while the job runs (a GPS update every three minutes, an overnight driver's heartbeat,
	// a task edit), during and between the reads of its sweep and of its reconciliation: those writes are after the
	// snapshot the job's sweep applied, which is the one it compares, so the job still succeeds.
	writes := 0
	f.fs.OnRunQuery(func(int) {
		if writes++; writes > 80 {
			return
		}
		now := dump.Timestamp{Time: time.Now().UTC()}
		f.fs.Merge("vehicle_locations/GPS1", map[string]any{"speed": int64(writes), "updatedAt": now})
		f.fs.Merge("drivers/drv_own/mobile_installations/inst_own1", map[string]any{"lastSeenAt": now})
		f.fs.Merge("tasks/task_1", map[string]any{"parcelNote": fmt.Sprint(writes), "updatedAt": now})
	})
	d, id = f.commandOf(t, TypeReconcile)
	if err := s.Handle(f.ctx, d); err != nil {
		t.Fatal(err)
	}
	f.fs.OnRunQuery(nil)
	j = f.job(t, id)
	if err := json.Unmarshal(j.Result, &result); err != nil || j.Status != jobs.StatusSucceeded || !result.Match || result.Mismatches != 0 || writes == 0 {
		t.Fatalf("reconcile while the app writes (%d write rounds): %s %s %v", writes, j.Status, j.Result, j.Error)
	}
	d, _ = f.command(t) // the next sweep catches the writes up, so the tampered row below is not re-applied
	if err := s.Handle(f.ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := f.etl.Exec(f.ctx, `UPDATE tasks SET status = 'cancelled' WHERE legacy_doc_id = 'task_1'`); err != nil {
		t.Fatal(err)
	}
	d, id = f.commandOf(t, TypeReconcile)
	if err := s.Handle(f.ctx, d); err != nil {
		t.Fatal(err)
	}
	j = f.job(t, id)
	if j.Status != jobs.StatusFailed || j.Error == nil || !strings.HasPrefix(*j.Error, "reconcile-sync: 1 mismatch") {
		t.Fatalf("a tampered task: %s %v %s", j.Status, j.Error, j.Result)
	}
	mf, _ := reg.Gather()
	for _, fam := range mf {
		if fam.GetName() == "mirror_reconcile_mismatches" && fam.GetMetric()[0].GetGauge().GetValue() != 1 {
			t.Errorf("gauge %v", fam.GetMetric()[0].GetGauge().GetValue())
		}
	}
}
