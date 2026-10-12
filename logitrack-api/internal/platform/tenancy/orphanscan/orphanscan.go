// Package orphanscan is the tenancy.orphan-scan consumer of the worker (T28; main spec §7.4, Appendix B §B.5.3,
// Appendix C §C.3.10): the scheduler fires the command cron daily at 03:00 Asia/Bangkok (job.tenancy.orphan-scan on
// lt.jobs, group platform, prefetch 1), and the worker runs tenancy.Scan in a system transaction, exports the
// quarantine counts as tenant_orphan_rows_total{table}, stores the result on the jobs row (quarantine counts and the R24
// broker-drift report) and, when the quarantine tenant holds any row, queues tenant_orphans_detected (warning) for the
// security.audit consumer in the same transaction as the job's success. The scan is read-only: it never moves a row
// (quarantined rows wait for a platform admin's re-home, drifted rows keep their frozen tenant by design).
package orphanscan

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// Names of the job (jobs.type, the cron entry), its queue and its lt.jobs routing key.
const (
	JobType = "tenancy.orphan-scan"
	Queue   = "tenancy.orphan-scan"
	Route   = "job." + JobType
)

// EventOrphansDetected is the security event of a scan that found quarantine rows (Appendix C §C.4.13, consumer-written).
const EventOrphansDetected = "tenant_orphans_detected"

// MetricRows is the gauge of the quarantine rows per tenant-stamped table, set by every scan (zero included).
const MetricRows = "tenant_orphan_rows_total"

// handlerTimeout bounds one scan (a count per table and two drift queries per driver-stamped table).
const handlerTimeout = 10 * time.Minute

// Deps are the collaborators of the consumer.
type Deps struct {
	// Pool is the logitrack_app pool: the scan has no request principal and reads every tenant in db.WithSystem.
	Pool db.Beginner
	// Registerer receives the tenant_orphan_rows_total gauge (the worker's /metrics); nil keeps it unregistered.
	Registerer prometheus.Registerer
	// Now dates the security event (default time.Now).
	Now func() time.Time
	Log zerolog.Logger
}

// Job is the tenancy.orphan-scan consumer.
type Job struct {
	pool db.Beginner
	rows *prometheus.GaugeVec
	now  func() time.Time
	log  zerolog.Logger
}

// New builds the consumer and registers its gauge.
func New(d Deps) (*Job, error) {
	if d.Pool == nil {
		return nil, errors.New("orphanscan: the job needs a pool")
	}
	j := &Job{pool: d.Pool, now: d.Now, log: d.Log,
		rows: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: MetricRows,
			Help: "Rows in the quarantine tenant per tenant-stamped table at the last tenancy.orphan-scan (R11)."}, []string{"table"})}
	if j.now == nil {
		j.now = time.Now
	}
	if d.Registerer != nil {
		if err := d.Registerer.Register(j.rows); err != nil {
			return nil, fmt.Errorf("orphanscan: register %s: %w", MetricRows, err)
		}
	}
	return j, nil
}

// Rows is the gauge (tests read it).
func (j *Job) Rows() *prometheus.GaugeVec { return j.rows }

// Registration binds the consumer to its queue.
func (j *Job) Registration() mq.Registration {
	return mq.Registration{Queue: Queue, Handler: j.Handle, Timeout: handlerTimeout}
}

// Handle runs one job.tenancy.orphan-scan command: the jobs row goes running, the scan runs, the gauge is set, then the
// row ends succeeded with the result and, when the quarantine tenant holds rows, tenant_orphans_detected is queued in
// the same transaction. A duplicate of a finished command is acknowledged; a database failure is retried by the ladder
// (the scan is read-only, so a retry is harmless).
func (j *Job) Handle(ctx context.Context, d *mq.Delivery) error {
	if d.RoutingKey != Route {
		return mq.Permanent(fmt.Errorf("orphanscan: unexpected routing key %s", d.RoutingKey))
	}
	cmd, err := jobs.DecodeCommand(d)
	if err != nil {
		return err
	}
	if err := db.WithSystem(ctx, j.pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.Start(ctx, tx, cmd.JobID)
		return err
	}); errors.Is(err, jobs.ErrNotActive) {
		return nil
	} else if err != nil {
		return err
	}
	res, err := j.Scan(ctx)
	if err != nil {
		return err
	}
	return db.WithSystem(ctx, j.pool, nil, func(tx pgx.Tx) error {
		if _, err := jobs.Succeed(ctx, tx, cmd.JobID, res); errors.Is(err, jobs.ErrNotActive) {
			return nil
		} else if err != nil {
			return err
		}
		return j.alert(ctx, tx, res, cmd.JobID.String())
	})
}

// Scan runs tenancy.Scan in a system transaction and sets the gauge for every tenant-stamped table.
func (j *Job) Scan(ctx context.Context) (tenancy.ScanResult, error) {
	var res tenancy.ScanResult
	err := db.WithSystem(ctx, j.pool, nil, func(tx pgx.Tx) error {
		var err error
		res, err = tenancy.Scan(ctx, tx)
		return err
	})
	if err != nil {
		return tenancy.ScanResult{}, err
	}
	for _, t := range res.Tables {
		j.rows.WithLabelValues(t).Set(float64(res.QuarantineRows[t]))
	}
	ev := j.log.Info()
	if res.QuarantineTotal > 0 {
		ev = j.log.Warn()
	}
	ev.Int("quarantine_rows", res.QuarantineTotal).Strs("quarantine_tables", res.SortedQuarantine()).
		Int("driver_moved", res.DriverMovedTotal).Int("truck_tenant_mismatch", res.TruckTenantMismatchTotal).
		Msg(JobType)
	return res, nil
}

// alert queues tenant_orphans_detected (warning, platform-level: tenant NULL) when the quarantine tenant holds rows.
// The drift totals travel in details for context; drift alone raises nothing, since drifted rows keep their tenant
// forever by design (R24) and would alert every night.
func (j *Job) alert(ctx context.Context, tx pgx.Tx, res tenancy.ScanResult, jobID string) error {
	if res.QuarantineTotal == 0 {
		return nil
	}
	return security.Emit(ctx, tx, security.Event{
		EventType: EventOrphansDetected, Severity: security.SeverityWarning,
		Summary: fmt.Sprintf("%d rows wait in the quarantine tenant for a re-home", res.QuarantineTotal),
		Details: map[string]any{
			"jobId": jobID, "quarantineRows": res.QuarantineRows, "quarantineTotal": res.QuarantineTotal,
			"driverMovedTotal": res.DriverMovedTotal, "truckTenantMismatchTotal": res.TruckTenantMismatchTotal,
		},
		OccurredAt: j.now().UTC(),
	}, "job", jobID)
}
