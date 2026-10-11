// Package coexist runs the two transition consumers of the worker (developer-spec.md §7.3, §12.1, §13.7, §13.8; R25,
// R71), the only ones that exist: etl.sync, the FS->PG mirror sweep the scheduler fires every ETL_SYNC_INTERVAL
// (ETL_SYNC_ENABLED), and firestore.shadow, the PG->FS projection of the master data installed 3.x APKs and the Cloud
// Functions still read (COMPAT_PROJECTION_ENABLED, the kill switch). Which collection goes which way follows from
// PG_OWNED_DOMAINS (internal/etl coexist.go: PlanMirror, PlanProjection); ETL_MIRROR_COLLECTIONS and
// COMPAT_PROJECTION_COLLECTIONS only narrow the sets per environment.
//
// The mirror is the ETL load path: it applies documents through the load transforms, writes schema etl and runs with
// app.etl_load, so it logs in as logitrack_etl through ETL_DATABASE_URL (R66; db.WithETL), like cmd/etl. The
// projection reads the projected tables and mints document ids in a system transaction on DATABASE_URL (db.WithSystem).
// Both reach Firestore through the REST client of internal/etl/gcp with the GOOGLE_APPLICATION_CREDENTIALS key of
// ETL_FIRESTORE_PROJECT_ID; tests use the in-process gcptest stand-in. The staging VM runs PostgreSQL only: both
// switches stay off there, the consumers then answer every message without touching Firestore.
package coexist

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/dbq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/webcfg"
)

// Queues and keys of the two consumers (Appendix B §B.5.3, group sync).
const (
	QueueSync   = "etl.sync"
	QueueShadow = "firestore.shadow"
	// TypeSync is the jobs.type of a sweep (the scheduler's cron etl.sync), RouteSync its lt.jobs routing key.
	TypeSync  = "etl.sync"
	RouteSync = "job." + TypeSync
)

// Plan is the coexistence plan of a deployment.
type Plan struct {
	Owned      map[string]bool
	Mirror     etl.MirrorPlan
	Projection []string
}

// ParsePlan builds the plan from PG_OWNED_DOMAINS, ETL_MIRROR_COLLECTIONS and COMPAT_PROJECTION_COLLECTIONS. Errors
// name the variable and the rule, never the value.
func ParsePlan(pgOwned, mirrorPin, projectionPin string) (Plan, []string) {
	var errs []string
	p := Plan{Owned: map[string]bool{}}
	owned, err := webcfg.ParseOwned(pgOwned)
	if err != nil {
		errs = append(errs, "PG_OWNED_DOMAINS: "+err.Error())
	}
	for _, d := range owned {
		p.Owned[d] = true
	}
	if p.Mirror, err = etl.PlanMirror(p.Owned, list(mirrorPin)); err != nil {
		errs = append(errs, "ETL_MIRROR_COLLECTIONS: must be a comma list of "+strings.Join(etl.MirrorCollections(), ", "))
	}
	if p.Projection, err = etl.PlanProjection(p.Owned, list(projectionPin)); err != nil {
		errs = append(errs, "COMPAT_PROJECTION_COLLECTIONS: must be a comma list of "+strings.Join(etl.ProjectedCollections, ", "))
	}
	return p, errs
}

func list(v string) []string {
	var out []string
	for e := range strings.SplitSeq(v, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// Metrics are the instruments of the two consumers (main spec §13.7).
type Metrics struct {
	mirrorApply *prometheus.CounterVec
	projected   *prometheus.CounterVec
	lag         *prometheus.HistogramVec
}

// NewMetrics registers mirror_apply_total{collection,result} (documents a sweep applied, per load outcome),
// projection_writes_total{collection,result} (written, unchanged, skipped, not_projected, disabled) and
// projection_lag_seconds{collection} (event time to projected).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		mirrorApply: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mirror_apply_total", Help: "Firestore documents the etl.sync mirror applied, per collection and load outcome.",
		}, []string{"collection", "result"}),
		projected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "projection_writes_total", Help: "firestore.shadow projections per collection and result.",
		}, []string{"collection", "result"}),
		lag: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "projection_lag_seconds", Help: "Time from the PostgreSQL event to its Firestore projection.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 300},
		}, []string{"collection"}),
	}
	if reg != nil {
		reg.MustRegister(m.mirrorApply, m.projected, m.lag)
	}
	return m
}

func (m *Metrics) projection(coll, result string, n int) {
	if m != nil && n > 0 {
		m.projected.WithLabelValues(coll, result).Add(float64(n))
	}
}

// --- etl.sync ---------------------------------------------------------------------------------------------------

// ETLPool is the logitrack_etl pool of ETL_DATABASE_URL: the sweep's transactions and the one check of its login.
type ETLPool interface {
	db.Beginner
	dbq.DBTX
}

// SyncConfig wires the mirror consumer.
type SyncConfig struct {
	// Enabled is ETL_SYNC_ENABLED; off, a sweep command fails its job naming the switch.
	Enabled bool
	// AppPool is the logitrack_app pool of DATABASE_URL: the jobs row of a sweep (RLS-exempt, written in WithSystem).
	AppPool db.Beginner
	// ETLPool is the logitrack_etl pool of ETL_DATABASE_URL (nil when the sync is off): the sweep itself (db.WithETL).
	ETLPool ETLPool
	// Source reads Firestore (gcp.Firestore; gcptest in tests).
	Source etl.DocSource
	Plan   etl.MirrorPlan
	// Bucket and PublicBucket are S3_BUCKET and S3_PUBLIC_BUCKET: the file_objects rows the load transforms register.
	Bucket, PublicBucket string
	Log                  zerolog.Logger
	Metrics              *Metrics
	Now                  func() time.Time
}

// Sync is the etl.sync consumer.
type Sync struct {
	c     SyncConfig
	login sync.Once
	// loginErr is the result of the one check that ETLPool logs in as logitrack_etl (R66).
	loginErr error
}

// NewSync builds the consumer.
func NewSync(c SyncConfig) (*Sync, error) {
	if c.AppPool == nil {
		return nil, errors.New("coexist: the mirror consumer needs the database pool")
	}
	if c.Enabled && (c.ETLPool == nil || c.Source == nil) {
		return nil, errors.New("coexist: the mirror needs the ETL pool and the Firestore client")
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Sync{c: c}, nil
}

// Registration is the consumer of queue etl.sync (prefetch 1; a sweep reads every mirrored collection).
func (s *Sync) Registration() mq.Registration {
	return mq.Registration{Queue: QueueSync, Handler: s.Handle, Timeout: 30 * time.Minute}
}

// ErrTextSyncOff is the job error of a sweep command on a worker whose mirror is off.
const ErrTextSyncOff = "the FS->PG mirror is off on this worker (ETL_SYNC_ENABLED)"

// jobTx runs the bookkeeping of the sweep's jobs row.
func (s *Sync) jobTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return db.WithSystem(ctx, s.c.AppPool, nil, fn)
}

// etlTx is a transaction of the ETL load path.
func (s *Sync) etlTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return db.WithETL(ctx, s.c.ETLPool, fn)
}

// Handle runs one sweep: the jobs row goes running, the plan is applied (etl.Engine.Mirror) and the job ends with
// the per-collection counts. A worker whose mirror is off, an ETL_DATABASE_URL of another login, a database without
// the P1 load and a billable document that cannot be applied (R19) fail the job (acked: the next fire tries again
// once it is fixed); a Firestore or database outage is retried by the ladder (a redelivered command finds the job
// running and sweeps again, which is idempotent).
func (s *Sync) Handle(ctx context.Context, d *mq.Delivery) error {
	if d.RoutingKey != RouteSync {
		return mq.Permanent(fmt.Errorf("etl.sync: unexpected routing key %s", d.RoutingKey))
	}
	cmd, err := jobs.DecodeCommand(d)
	if err != nil {
		return err
	}
	var job jobs.Job
	if err := s.jobTx(ctx, func(tx pgx.Tx) error {
		var err error
		job, err = jobs.Start(ctx, tx, cmd.JobID)
		return err
	}); errors.Is(err, jobs.ErrNotActive) {
		return nil // a duplicate of a finished command
	} else if err != nil {
		return err
	}
	finish := func(cause string, result any) error {
		return s.jobTx(ctx, func(tx pgx.Tx) error {
			var err error
			if cause == "" {
				_, err = jobs.Succeed(ctx, tx, job.ID, result)
			} else {
				_, err = jobs.Fail(ctx, tx, job.ID, cause, result)
			}
			if errors.Is(err, jobs.ErrNotActive) {
				return nil
			}
			return err
		})
	}
	if !s.c.Enabled {
		s.c.Log.Warn().Msg("etl.sync: a sweep command reached a worker whose mirror is off")
		return finish(ErrTextSyncOff, nil)
	}
	s.login.Do(func() {
		l, err := db.CurrentLogin(ctx, s.c.ETLPool)
		switch {
		case err != nil:
			s.loginErr = err
		case l.RoleName != db.RoleETL || l.Superuser:
			s.loginErr = fmt.Errorf("ETL_DATABASE_URL must log in as %s (R66), not as %s", db.RoleETL, l.RoleName)
		}
	})
	if s.loginErr != nil {
		return finish(s.loginErr.Error(), nil)
	}
	var own uuid.UUID
	var found bool
	if err := s.etlTx(ctx, func(tx pgx.Tx) error {
		var err error
		own, found, err = etl.OwnFleet(ctx, tx)
		return err
	}); err != nil {
		return err
	}
	if !found {
		return finish("the database has no own-fleet tenant: the mirror runs after the P1 initial load (etl load)", nil)
	}
	eng, err := etl.New(etl.Config{Tx: s.etlTx, OwnFleetTenantID: own, Bucket: s.c.Bucket, PublicBucket: s.c.PublicBucket,
		Log: s.c.Log, Now: s.c.Now})
	if err != nil {
		return finish(err.Error(), nil)
	}
	rep, err := eng.Mirror(ctx, s.c.Source, s.c.Plan)
	var abort *etl.AbortError
	switch {
	case errors.As(err, &abort):
		s.c.Log.Error().Str("path", abort.Path).Str("reason", string(abort.Reason)).Msg("etl.sync: the sweep stopped on a document to fix at the source")
		return finish(abort.Error(), nil)
	case err != nil:
		return err
	}
	if s.c.Metrics != nil {
		for _, c := range rep.Collections {
			for o, n := range c.Outcomes {
				s.c.Metrics.mirrorApply.WithLabelValues(c.Name, string(o)).Add(float64(n))
			}
		}
	}
	return finish("", sweepResult(rep))
}

// sweepResult is the jobs.result of a sweep: per collection its mode, documents read and the outcomes applied.
func sweepResult(rep *etl.Report) map[string]any {
	colls := map[string]any{}
	for _, c := range rep.Collections {
		out := map[string]int{}
		for o, n := range c.Outcomes {
			out[string(o)] = n
		}
		colls[c.Name] = map[string]any{"mode": c.Mode, "read": c.Total, "unchanged": c.Unchanged, "beforeWatermark": c.BeforeMark,
			"applied": out}
	}
	return map[string]any{"applied": rep.Applied(), "readAt": rep.ExportedAt, "collections": colls}
}

// --- firestore.shadow -------------------------------------------------------------------------------------------

// ShadowConfig wires the projection consumer.
type ShadowConfig struct {
	// Enabled is COMPAT_PROJECTION_ENABLED, the kill switch: off, every event is answered without a projection (turn
	// it back on after a full `etl project` pass, which catches up what was skipped).
	Enabled bool
	// Pool is the logitrack_app pool of DATABASE_URL: the projection reads and mints in db.WithSystem.
	Pool db.Beginner
	// FS writes Firestore (gcp.Firestore; gcptest in tests); nil when the projection is off.
	FS      etl.FirestoreWriter
	Plan    Plan
	Log     zerolog.Logger
	Metrics *Metrics
	Now     func() time.Time
}

// Shadow is the firestore.shadow consumer.
type Shadow struct {
	c    ShadowConfig
	proj *etl.Projector
}

// NewShadow builds the consumer.
func NewShadow(c ShadowConfig) (*Shadow, error) {
	if c.Now == nil {
		c.Now = time.Now
	}
	s := &Shadow{c: c}
	if c.Enabled {
		if c.Pool == nil || c.FS == nil {
			return nil, errors.New("coexist: the projection needs the database pool and the Firestore client")
		}
		s.proj = &etl.Projector{Tx: func(ctx context.Context, fn func(pgx.Tx) error) error { return db.WithSystem(ctx, c.Pool, nil, fn) },
			FS: c.FS, Collections: c.Plan.Projection, Owned: c.Plan.Owned, Log: c.Log}
	}
	return s, nil
}

// Registration is the consumer of queue firestore.shadow (prefetch 4).
func (s *Shadow) Registration() mq.Registration {
	return mq.Registration{Queue: QueueShadow, Handler: s.Handle, Timeout: 10 * time.Minute}
}

// Handle projects the rows one event names (etl.ShadowTargets). A body that does not decode is a permanent failure; a
// Firestore or database failure is retried by the ladder (a projection is idempotent: it re-reads the rows and writes
// only what differs).
func (s *Shadow) Handle(ctx context.Context, d *mq.Delivery) error {
	targets, err := etl.ShadowTargets(d.RoutingKey, d.Body)
	if err != nil {
		return mq.Permanent(err)
	}
	for _, t := range targets {
		if s.proj == nil {
			s.c.Metrics.projection(t.Collection, "disabled", 1)
			continue
		}
		st, err := s.proj.Project(ctx, t)
		if err != nil {
			return err
		}
		s.c.Metrics.projection(t.Collection, "written", st.Written)
		s.c.Metrics.projection(t.Collection, "unchanged", st.Unchanged)
		s.c.Metrics.projection(t.Collection, "skipped", st.Skipped)
		s.c.Metrics.projection(t.Collection, "not_projected", st.NotProjected)
		if s.c.Metrics != nil && st.Written+st.Unchanged > 0 && !d.OccurredAt.IsZero() {
			s.c.Metrics.lag.WithLabelValues(t.Collection).Observe(s.c.Now().Sub(d.OccurredAt).Seconds())
		}
	}
	return nil
}
