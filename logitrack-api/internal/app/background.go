package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/email"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/health"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/telemetry"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scheduler"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// DeadLetterPollInterval is how often the worker refreshes mq_dead_letter_depth.
const DeadLetterPollInterval = 30 * time.Second

// background is what a long-running process without HTTP listeners runs: run blocks until ctx ends,
// close releases its connections afterwards, and checks are the dependency probes of its /readyz on
// METRICS_ADDR (Appendix B §B.2.1).
type background struct {
	run    func(ctx context.Context)
	close  func()
	checks []health.Checker
}

// runBackground loads T, starts logging, tracing and /metrics on METRICS_ADDR, runs what build
// returns until ctx ends, and drains within SHUTDOWN_TIMEOUT.
func runBackground[T any](ctx context.Context, process string, stdout, stderr io.Writer,
	common func(*T) (Common, Runtime),
	build func(ctx context.Context, cfg *T, log zerolog.Logger, m *telemetry.Metrics) (background, error)) int {
	cfg, err := config.Load[T]()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", process, err)
		return ExitConfigError
	}
	c, rt := common(cfg)
	log, err := NewLogger(c, process, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", process, err)
		return ExitConfigError
	}
	LogConfig[T](log)

	stopTracing, err := SetupTracing(ctx, c, process)
	if err != nil {
		log.Error().Err(err).Msg("tracing setup failed")
		return ExitRuntimeError
	}
	defer func() {
		tctx, tcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer tcancel()
		if err := stopTracing(tctx); err != nil {
			log.Warn().Err(err).Msg("tracing shutdown")
		}
	}()
	metrics := telemetry.NewMetrics(process)
	state := health.NewState()
	bg, err := build(ctx, cfg, log, metrics)
	if err != nil {
		log.Error().Err(err).Msg("startup failed")
		if _, ok := errors.AsType[*config.Error](err); ok {
			return ExitConfigError
		}
		return ExitRuntimeError
	}
	defer bg.close()
	state.Register(bg.checks...)
	srv, err := telemetry.ListenMetrics(rt.MetricsAddr, metrics.Registry, log, state.Ready)
	if err != nil {
		log.Error().Err(err).Msg("listen METRICS_ADDR")
		return ExitRuntimeError
	}
	state.MarkStarted()
	log.Info().Str("metrics_addr", srv.Addr()).Msg(process + " started")

	runCtx, cancelRun := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() { defer close(done); bg.run(runCtx) }()
	<-ctx.Done()
	state.BeginDrain()
	log.Info().Msg("shutting down")
	cancelRun()
	code := ExitOK
	select {
	case <-done:
	case <-time.After(rt.ShutdownTimeout + 5*time.Second):
		log.Error().Msg("shutdown timed out")
		code = ExitRuntimeError
	}
	sctx, cancel := context.WithTimeout(context.Background(), rt.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		log.Error().Err(err).Msg("metrics shutdown")
		code = ExitRuntimeError
	}
	return code
}

// RunWorker is the worker process (main spec §2.1, §7.2-§7.3): it asserts the RabbitMQ topology and
// consumes the queues of the WORKER_CONSUMERS groups that have a consumer, with the retry ladder of
// Appendix B §B.5.4, until SIGTERM. Readiness needs PostgreSQL, Redis (notify.fcm's idem: keys) and the
// broker session.
func RunWorker(ctx context.Context, stdout, stderr io.Writer) int {
	return runBackground(ctx, "worker", stdout, stderr,
		func(c *WorkerConfig) (Common, Runtime) { return c.Common, c.Runtime },
		func(ctx context.Context, cfg *WorkerConfig, log zerolog.Logger, m *telemetry.Metrics) (background, error) {
			pool, err := db.NewPool(ctx, cfg.DatabaseURL, "logitrack-worker", db.Options{MaxConns: cfg.MaxConns, MinConns: cfg.MinConns})
			if err != nil {
				return background{}, &config.Error{Invalid: []string{"DATABASE_URL: " + err.Error()}}
			}
			// The process's one Redis client and keyspace, built like the api's and the scheduler's (T09).
			cache.RouteDriverLogs(log)
			rdb, ks, err := cache.Open(cache.Options{URL: cfg.RedisURL, Prefix: cfg.RedisKeyPrefix, AppEnv: cfg.AppEnv, TLS: cfg.RedisTLS})
			if err != nil { // never carries the URL
				pool.Close()
				return background{}, &config.Error{Invalid: []string{err.Error()}}
			}
			st, _, err := newStorage(storageBuild{Storage: cfg.Storage}, pool, log)
			if err != nil {
				pool.Close()
				if cerr := rdb.Close(); cerr != nil {
					log.Warn().Err(cerr).Msg("redis close")
				}
				return background{}, err
			}
			closeAll := func() {
				st.Close()
				pool.Close()
				if err := rdb.Close(); err != nil {
					log.Warn().Err(err).Msg("redis close")
				}
			}
			regs, err := workerRegistrations(cfg, pool, st, rdb, ks, log)
			if err != nil {
				closeAll()
				return background{}, err
			}
			opts := mq.ConsumerOptions{
				Topology: mq.Default, Log: log, Metrics: mq.NewConsumerMetrics(m.Registry),
				DefaultPrefetch: cfg.Prefetch, ShutdownTimeout: cfg.ShutdownTimeout,
			}
			dial := func() (*amqp.Connection, error) { return mq.Dial(cfg.RabbitMQURL, "logitrack-worker") }
			probe := &mq.ConnProbe{}
			return background{
				run: func(ctx context.Context) {
					mq.Supervise(ctx, log, dial, func(cctx context.Context, conn *amqp.Connection) error {
						return runConsumers(cctx, conn, regs, opts, log, probe)
					})
				},
				close: closeAll,
				checks: []health.Checker{
					checker{"postgres", pool.Ping},
					checker{"redis", func(ctx context.Context) error { return rdb.Ping(ctx).Err() }},
					checker{"rabbitmq", probe.Check},
				},
			}, nil
		})
}

// workerRegistrations are the consumers of the selected groups. Queues of those groups whose
// consumer arrives with a later issue are not consumed (their messages wait in the queue).
func workerRegistrations(cfg *WorkerConfig, pool *pgxpool.Pool, st *storage.Service, rdb *redis.Client, ks cache.Keyspace, log zerolog.Logger) ([]mq.Registration, error) {
	available := map[string]mq.Registration{}
	var sender email.Sender
	if cfg.EmailEnabled {
		s, err := email.New(email.Config{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Password: cfg.SMTPPassword,
			From: cfg.SMTPFrom, FromName: cfg.SMTPFromName, StartTLS: cfg.SMTPStartTLS,
		})
		if err != nil {
			return nil, &config.Error{Invalid: []string{err.Error()}}
		}
		sender = s
	}
	mail := &notify.Email{
		Pool: pool, Sender: sender, Tokens: notify.ResetTokens{ResetTTL: cfg.PasswordResetTTL},
		WebBaseURL: cfg.PublicWebBaseURL, ResetTTL: cfg.PasswordResetTTL, Enabled: cfg.EmailEnabled, Log: log,
	}
	available[notify.QueueEmail] = mail.Registration()
	if st != nil {
		available[storage.QueueGC] = st.GCRegistration()
	}

	fcm := &notify.FCM{Pool: pool, Redis: rdb, Keys: ks, Enabled: cfg.FCMEnabled, Log: log}
	if cfg.FCMEnabled && slices.Contains(cfg.Groups, "notify") {
		// No I/O beyond reading the key file: the access token is fetched on the first send, so the worker
		// starts while Google is unreachable. An unreadable or malformed file names the variable only.
		creds, err := push.LoadCredentials(cfg.FCMServiceAccountJSON)
		if err != nil {
			return nil, &config.Error{Invalid: []string{err.Error()}}
		}
		if p := creds.ProjectID(); p != "" && p != cfg.FCMProjectID {
			log.Warn().Msg("FCM: the FCM_SERVICE_ACCOUNT_JSON service account belongs to another project than FCM_PROJECT_ID")
		}
		client, err := push.New(push.Config{ProjectID: cfg.FCMProjectID, Tokens: creds.TokenSource(nil)})
		if err != nil {
			return nil, &config.Error{Invalid: []string{"FCM_PROJECT_ID: " + err.Error()}}
		}
		fcm.Sender = client
	}
	log.Info().Bool("fcm_enabled", cfg.FCMEnabled).Msg("notify.fcm")
	available[notify.QueueFCM] = fcm.Registration()

	var regs []mq.Registration
	var waiting []string
	for _, q := range mq.Default.Queues {
		if !slices.Contains(cfg.Groups, q.Group) {
			continue
		}
		if r, ok := available[q.Name]; ok {
			regs = append(regs, r)
		} else {
			waiting = append(waiting, q.Name)
		}
	}
	names := make([]string, len(regs))
	for i, r := range regs {
		names[i] = r.Queue
	}
	log.Info().Strs("groups", cfg.Groups).Strs("consuming", names).Strs("no_consumer_yet", waiting).Msg("worker queues")
	return regs, nil
}

// runConsumers asserts the topology and runs every registration plus the dead-letter monitor on conn.
// When one consumer stops while the connection is up (its channel failed), all stop and the
// supervisor reconnects. probe holds conn for readiness from the asserted topology on.
func runConsumers(ctx context.Context, conn *amqp.Connection, regs []mq.Registration, opts mq.ConsumerOptions, log zerolog.Logger,
	probe *mq.ConnProbe) error {
	if err := mq.DeclareTopology(conn, opts.Topology); err != nil {
		return err
	}
	probe.Set(conn)
	defer probe.Clear()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	stop := func(err error) {
		once.Do(func() { first = err; cancel() })
	}
	for _, r := range regs {
		wg.Go(func() {
			if err := mq.Consume(ctx, conn, r, opts); err != nil && ctx.Err() == nil {
				log.Warn().Err(err).Str("queue", r.Queue).Msg("consumer stopped")
				stop(err)
			}
		})
	}
	wg.Go(func() {
		if err := mq.MonitorDeadLetters(ctx, conn, opts.Topology, opts.Metrics, DeadLetterPollInterval); err != nil && ctx.Err() == nil {
			stop(err)
		}
	})
	wg.Wait()
	return first
}

// RunScheduler is the scheduler process (main spec §7.1, §7.4): every replica campaigns for the
// advisory lock; the leader runs the Bangkok cron table, the outbox relay (with auth's revocation hook
// on user.sessions_revoked, Appendix C §C.4.7, and the cache invalidation of every relayed event,
// Appendix B §B.6.1) and the queue replays. Readiness needs PostgreSQL and Redis on every replica and
// the broker connection on the leader only.
func RunScheduler(ctx context.Context, stdout, stderr io.Writer) int {
	return runBackground(ctx, "scheduler", stdout, stderr,
		func(c *SchedulerConfig) (Common, Runtime) { return c.Common, c.Runtime },
		func(ctx context.Context, cfg *SchedulerConfig, log zerolog.Logger, m *telemetry.Metrics) (background, error) {
			pool, err := db.NewPool(ctx, cfg.DatabaseURL, "logitrack-scheduler", db.Options{MaxConns: cfg.MaxConns, MinConns: cfg.MinConns})
			if err != nil {
				return background{}, &config.Error{Invalid: []string{"DATABASE_URL: " + err.Error()}}
			}
			// The process's one Redis client and keyspace, as in the api (T09): context deadlines honoured
			// with bounded socket and pool timeouts, the money-path guard, and go-redis's own log lines
			// through the redacting logger. Every key below is built by ks.
			cache.RouteDriverLogs(log)
			rdb, ks, err := cache.Open(cache.Options{URL: cfg.RedisURL, Prefix: cfg.RedisKeyPrefix, AppEnv: cfg.AppEnv, TLS: cfg.RedisTLS})
			if err != nil { // never carries the URL
				pool.Close()
				return background{}, &config.Error{Invalid: []string{err.Error()}}
			}
			invalidator := cache.New(rdb, ks, cache.WithLogger(log))
			if err := invalidator.Register(m.Registry); err != nil {
				pool.Close()
				_ = rdb.Close()
				return background{}, fmt.Errorf("register cache metrics: %w", err)
			}
			locks := jobs.NewRedisLocker(rdb, ks)
			rt := realtime.NewWriter(rdb, ks, cfg.RTLogMaxLen, cfg.RTLogTTL)
			cron, err := scheduler.NewCron(pool, locks, scheduler.Table(pool, rt), log, m.Registry)
			if err != nil {
				pool.Close()
				_ = rdb.Close()
				return background{}, err
			}
			pending := make([]string, len(scheduler.Pending))
			for i, p := range scheduler.Pending {
				pending[i] = p.Name + " (" + p.Issue + ")"
			}
			log.Info().Strs("pending_crons", pending).Msg("cron jobs whose consumer lands later are not scheduled")
			relayMetrics := outbox.NewRelayMetrics(m.Registry)
			hooks := map[string]outbox.Hook{
				auth.RouteSessionsRevoked: auth.RevocationHook(pool, auth.NewStore(rdb, ks.Prefix()), cfg.JWTAccessTTL, log),
			}
			probe := &mq.ConnProbe{}
			leader := scheduler.NewLeader(pool, log, m.Registry)
			s := &scheduler.Scheduler{
				Leader: leader,
				Cron:   cron,
				Dial:   func() (*amqp.Connection, error) { return mq.Dial(cfg.RabbitMQURL, "logitrack-scheduler") },
				NewRelay: func(conn *amqp.Connection) *outbox.Relay {
					return outbox.NewRelay(pool, func() (outbox.Publisher, error) { return mq.NewPublisher(conn) }, rt,
						outbox.RelayOptions{Interval: cfg.RelayInterval, BatchSize: cfg.BatchSize, Hooks: hooks, Cache: invalidator,
							Log: log, Metrics: relayMetrics})
				},
				Replayer: jobs.NewReplayer(pool, mq.Default, locks, log),
				AMQP:     probe,
				Log:      log,
			}
			return background{
				run: s.Run,
				close: func() {
					pool.Close()
					if err := rdb.Close(); err != nil {
						log.Warn().Err(err).Msg("redis close")
					}
				},
				checks: []health.Checker{
					checker{"postgres", pool.Ping},
					checker{"redis", func(ctx context.Context) error { return rdb.Ping(ctx).Err() }},
					checker{"rabbitmq", func(ctx context.Context) error {
						if !leader.Leading() {
							return nil // a standby holds no broker connection
						}
						return probe.Check(ctx)
					}},
				},
			}, nil
		})
}
