package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// Kind says who does the work of a cron fire.
type Kind int

const (
	// Command fires insert a jobs row and its lt.jobs outbox command; a worker queue does the work.
	Command Kind = iota
	// Local fires run in the scheduler (SQL and Redis housekeeping); the jobs row records the run.
	Local
)

// Job is one entry of the cron table.
type Job struct {
	Name string // jobs.type; Command fires publish job.{Name}
	Spec string // five-field cron (or a robfig descriptor), evaluated in Asia/Bangkok
	Kind Kind
	Run  func(ctx context.Context) (result any, err error) // Local only
}

// LockTTL is the life of lock:cron:{job}:{scheduledFor} (Appendix B §B.6.2).
const LockTTL = 10 * time.Minute

// parser accepts the standard five fields plus descriptors (@hourly, @every 1s in tests).
var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Cron fires the jobs of a table at their Bangkok times while ctx lives. Duplicate fires are stopped
// twice: only the advisory-lock leader runs a Cron, and every fire first takes
// lock:cron:{job}:{scheduledFor} (SET NX), so an old leader that has not noticed its demotion and the
// new one cannot both fire the same slot.
type Cron struct {
	pool     db.Beginner
	locks    jobs.Locker
	table    []Job
	log      zerolog.Logger
	holder   string
	now      func() time.Time
	fires    *prometheus.CounterVec
	location *time.Location
}

// NewCron builds the loop. reg may be nil.
func NewCron(pool db.Beginner, locks jobs.Locker, table []Job, log zerolog.Logger, reg prometheus.Registerer) (*Cron, error) {
	for _, j := range table {
		if _, err := parser.Parse(j.Spec); err != nil {
			return nil, fmt.Errorf("scheduler: cron %s: %w", j.Name, err)
		}
		if j.Kind == Local && j.Run == nil {
			return nil, fmt.Errorf("scheduler: local cron %s has no Run", j.Name)
		}
	}
	c := &Cron{pool: pool, locks: locks, table: table, log: log, holder: uuid.NewString(), now: time.Now,
		location: clock.Bangkok,
		fires: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scheduler_cron_fires_total", Help: "Cron fires per job and outcome (fired, duplicate, failed).",
		}, []string{"job", "outcome"})}
	if reg != nil {
		reg.MustRegister(c.fires)
	}
	return c, nil
}

// Run fires until ctx ends, then waits for the fires in flight.
func (c *Cron) Run(ctx context.Context) {
	type entry struct {
		job   Job
		sched cron.Schedule
		next  time.Time
	}
	now := c.now().In(c.location)
	entries := make([]*entry, 0, len(c.table))
	for _, j := range c.table {
		s, _ := parser.Parse(j.Spec)
		entries = append(entries, &entry{job: j, sched: s, next: s.Next(now)})
	}
	if len(entries) == 0 {
		<-ctx.Done()
		return
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		at := entries[0].next
		for _, e := range entries[1:] {
			if e.next.Before(at) {
				at = e.next
			}
		}
		timer := time.NewTimer(time.Until(at))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		for _, e := range entries {
			if e.next.Equal(at) {
				job, slot := e.job, at
				wg.Go(func() { c.Fire(ctx, job, slot) })
				e.next = e.sched.Next(at)
			}
		}
	}
}

// Fire runs one slot of a job: lock:cron first, then the jobs row (owner NULL) and either the
// lt.jobs command or the local work.
func (c *Cron) Fire(ctx context.Context, j Job, scheduledFor time.Time) {
	log := c.log.With().Str("cron", j.Name).Time("scheduled_for", scheduledFor).Logger()
	ok, _, err := c.locks.Acquire(ctx, c.locks.CronKey(j.Name, scheduledFor), c.holder, LockTTL)
	switch {
	case err != nil:
		// The advisory lock is the primary guard; Redis only backs it up.
		log.Warn().Err(err).Msg("cron lock unavailable; firing under the advisory lock alone")
	case !ok:
		c.fires.WithLabelValues(j.Name, "duplicate").Inc()
		log.Info().Msg("cron slot already fired by another scheduler")
		return
	}
	params := map[string]any{"scheduledFor": scheduledFor.UTC().Format(time.RFC3339)}
	if err := c.fire(ctx, j, params); err != nil {
		c.fires.WithLabelValues(j.Name, "failed").Inc()
		log.Error().Err(err).Msg("cron fire failed")
		return
	}
	c.fires.WithLabelValues(j.Name, "fired").Inc()
}

func (c *Cron) fire(ctx context.Context, j Job, params map[string]any) error {
	in := jobs.NewInput{Type: j.Name, Params: params}
	if j.Kind == Command {
		return db.WithSystem(ctx, c.pool, nil, func(tx pgx.Tx) error {
			_, err := jobs.Enqueue(ctx, tx, in)
			return err
		})
	}
	var job jobs.Job
	if err := db.WithSystem(ctx, c.pool, nil, func(tx pgx.Tx) error {
		var err error
		if job, err = jobs.Insert(ctx, tx, in); err != nil {
			return err
		}
		job, err = jobs.Start(ctx, tx, job.ID)
		return err
	}); err != nil {
		return err
	}
	result, runErr := j.Run(ctx)
	rctx := context.WithoutCancel(ctx) // record the outcome even when shutdown interrupted the run
	err := db.WithSystem(rctx, c.pool, nil, func(tx pgx.Tx) error {
		if runErr != nil {
			_, err := jobs.Fail(rctx, tx, job.ID, runErr.Error(), result)
			return err
		}
		_, err := jobs.Succeed(rctx, tx, job.ID, result)
		return err
	})
	return errors.Join(runErr, err)
}
