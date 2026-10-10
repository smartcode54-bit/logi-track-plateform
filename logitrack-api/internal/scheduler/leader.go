// Package scheduler is the single active scheduler of main spec §7.4 (Appendix B §B.5.6): a leader
// elected with a PostgreSQL session advisory lock runs the Bangkok cron table, the outbox relay and
// the queue replays; the other replicas wait for the lock.
package scheduler

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

// LockName is hashed into the advisory lock key: pg_try_advisory_lock(hashtext('lt-scheduler')).
const LockName = "lt-scheduler"

// Leader timings: how often a standby retries the lock, and how often the leader checks that the
// connection holding it is alive (the lock goes with the connection).
const (
	DefaultRetryInterval = 2 * time.Second
	DefaultPingInterval  = 2 * time.Second
)

// Leader elects one scheduler. The lock is a session lock on a connection outside the pool, so it is
// released by PostgreSQL the moment that connection ends, whatever happened to the process.
type Leader struct {
	pool          *pgxpool.Pool
	log           zerolog.Logger
	retryInterval time.Duration
	pingInterval  time.Duration
	isLeader      prometheus.Gauge
	leading       atomic.Bool
}

// NewLeader builds the election on the logitrack_app pool's connection settings. reg may be nil.
func NewLeader(pool *pgxpool.Pool, log zerolog.Logger, reg prometheus.Registerer) *Leader {
	l := &Leader{pool: pool, log: log, retryInterval: DefaultRetryInterval, pingInterval: DefaultPingInterval,
		isLeader: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "scheduler_leader", Help: "1 while this replica holds the scheduler advisory lock.",
		})}
	if reg != nil {
		reg.MustRegister(l.isLeader)
	}
	return l
}

// Leading reports whether this replica holds the lock now (readiness: only the leader needs the broker).
func (l *Leader) Leading() bool { return l.leading.Load() }

// WithIntervals sets how often a standby retries the lock and how often the leader pings its lock
// connection (tests shorten both).
func (l *Leader) WithIntervals(retry, ping time.Duration) *Leader {
	l.retryInterval, l.pingInterval = retry, ping
	return l
}

// Run campaigns until ctx ends. While this replica holds the lock, lead runs with a context that is
// cancelled when the lock connection fails or ctx ends; Run waits for lead to return before it
// releases the lock or campaigns again.
func (l *Leader) Run(ctx context.Context, lead func(ctx context.Context)) {
	var conn *pgx.Conn
	defer func() {
		if conn != nil {
			_ = conn.Close(context.WithoutCancel(ctx))
		}
	}()
	for ctx.Err() == nil {
		if conn == nil || conn.IsClosed() {
			conn = l.connect(ctx)
		}
		if conn != nil && l.tryLock(ctx, conn) {
			l.hold(ctx, conn, lead)
			_ = conn.Close(context.WithoutCancel(ctx)) // releases the session lock
			conn = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(l.retryInterval):
		}
	}
}

func (l *Leader) connect(ctx context.Context) *pgx.Conn {
	conn, err := pgx.ConnectConfig(ctx, l.pool.Config().ConnConfig.Copy())
	if err != nil {
		if ctx.Err() == nil {
			l.log.Warn().Err(err).Msg("scheduler: cannot reach PostgreSQL for the leader lock")
		}
		return nil
	}
	return conn
}

func (l *Leader) tryLock(ctx context.Context, conn *pgx.Conn) bool {
	var held bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtext($1))", LockName).Scan(&held); err != nil {
		if ctx.Err() == nil {
			l.log.Warn().Err(err).Msg("scheduler: leader lock query failed")
		}
		_ = conn.Close(context.WithoutCancel(ctx))
		return false
	}
	return held
}

func (l *Leader) hold(ctx context.Context, conn *pgx.Conn, lead func(ctx context.Context)) {
	l.log.Info().Msg("scheduler: leader")
	l.isLeader.Set(1)
	l.leading.Store(true)
	defer func() {
		l.leading.Store(false)
		l.isLeader.Set(0)
	}()
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); lead(lctx) }()
	tick := time.NewTicker(l.pingInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return
		case <-done:
			return
		case <-tick.C:
			pctx, pcancel := context.WithTimeout(ctx, l.pingInterval)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil && ctx.Err() == nil {
				l.log.Warn().Err(err).Msg("scheduler: leader lock connection lost; stepping down")
				cancel()
				<-done
				return
			}
		}
	}
}
