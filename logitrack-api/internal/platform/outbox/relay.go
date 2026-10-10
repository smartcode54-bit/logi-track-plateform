package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox/outboxdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// Channel is the NOTIFY channel of the outbox_events AFTER INSERT trigger (0001 trg_outbox_notify).
const Channel = "outbox_new"

// Defaults of OUTBOX_RELAY_INTERVAL and OUTBOX_BATCH_SIZE (main spec §16.1).
const (
	DefaultInterval  = 200 * time.Millisecond
	DefaultBatchSize = 500
)

// MaxBackoff caps the wait between rounds while the head row keeps failing: the wait starts at the
// interval and doubles per failed round, and the first successful round resets it.
const MaxBackoff = 30 * time.Second

// Publisher sends one message and waits for the broker's confirm (mq.Publisher).
type Publisher interface {
	Publish(ctx context.Context, exchange, key string, msg amqp.Publishing) error
}

// Realtime fans an event out to its topics (realtime.Writer).
type Realtime interface {
	Publish(ctx context.Context, e realtime.Event) (int64, error)
}

// Hook is idempotent work the relay does for every row of one routing key before publishing it, so
// it happens at least once per committed event: auth re-applies its revocation markers from
// user.sessions_revoked (Appendix C §C.4.7). payload is the row's JSON payload. An error is handled
// like a failed publish: the batch stops at that row and it is retried.
type Hook func(ctx context.Context, payload []byte) error

// RelayOptions configure a Relay.
type RelayOptions struct {
	Interval  time.Duration // OUTBOX_RELAY_INTERVAL: fallback tick behind LISTEN
	BatchSize int           // OUTBOX_BATCH_SIZE
	Hooks     map[string]Hook
	Log       zerolog.Logger
	Metrics   *RelayMetrics
}

// Relay moves committed outbox rows to RabbitMQ and Redis. Run it in one process only (the scheduler
// leader): the single relay keeps the realtime sequence and the per-aggregate order monotonic.
type Relay struct {
	pool      *pgxpool.Pool
	publisher func() (Publisher, error)
	rt        Realtime
	opt       RelayOptions

	pub         Publisher
	lastBacklog time.Time
	// confirmed is the id of the row whose AMQP publish the broker confirmed while a later step
	// (realtime) failed: the retry of that row skips the publish, so an outage of Redis costs no
	// duplicate deliveries. In memory only: a new leader re-publishes it once (at-least-once).
	confirmed int64
}

// NewRelay builds a relay. newPublisher opens a confirm channel (mq.NewPublisher on the leader's
// connection); the relay asks for a new one after a publish error, because a channel-level error
// closes the channel.
func NewRelay(pool *pgxpool.Pool, newPublisher func() (Publisher, error), rt Realtime, opt RelayOptions) *Relay {
	if opt.Interval <= 0 {
		opt.Interval = DefaultInterval
	}
	if opt.BatchSize <= 0 {
		opt.BatchSize = DefaultBatchSize
	}
	return &Relay{pool: pool, publisher: newPublisher, rt: rt, opt: opt}
}

// Run relays until ctx ends: on every outbox_new notification and every Interval. While the head row
// keeps failing, rounds back off from Interval to MaxBackoff and notifications do not cut the wait.
func (r *Relay) Run(ctx context.Context) error {
	wake := make(chan struct{}, 1)
	listening := make(chan struct{})
	go func() { defer close(listening); r.listen(ctx, wake) }()
	defer func() { <-listening }()
	tick := time.NewTicker(r.opt.Interval)
	defer tick.Stop()
	var backoff time.Duration
	for {
		_, err := r.Drain(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			backoff = nextBackoff(backoff, r.opt.Interval)
			r.opt.Log.Warn().Err(err).Dur("retry_in", backoff).Msg("outbox relay: batch failed")
		} else {
			backoff = 0
		}
		r.reportBacklog(ctx)
		if backoff > 0 {
			wait := time.NewTimer(backoff)
			for waiting := true; waiting; {
				select {
				case <-ctx.Done():
					wait.Stop()
					return nil
				case <-wait.C:
					waiting = false
				case <-tick.C:
					r.reportBacklog(ctx)
				}
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-tick.C:
		}
	}
}

// nextBackoff is the wait after one more failed round: interval first, then doubling up to MaxBackoff.
func nextBackoff(prev, interval time.Duration) time.Duration {
	if prev <= 0 {
		return interval
	}
	return min(prev*2, max(MaxBackoff, interval))
}

// listen holds a dedicated connection on LISTEN outbox_new and turns notifications into wake-ups
// (coalesced: one pending wake-up is enough). On a lost connection it reconnects after a second;
// the tick keeps the relay going meanwhile.
func (r *Relay) listen(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		conn, err := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
		if err == nil {
			if _, err = conn.Exec(ctx, "LISTEN "+Channel); err == nil {
				for {
					if _, err = conn.WaitForNotification(ctx); err != nil {
						break
					}
					select {
					case wake <- struct{}{}:
					default:
					}
				}
			}
			_ = conn.Close(context.WithoutCancel(ctx))
		}
		if ctx.Err() != nil {
			return
		}
		r.opt.Log.Warn().Err(err).Msg("outbox relay: LISTEN connection lost; falling back to the tick")
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// Drain relays batches until one comes back short or fails, and returns the rows published.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	total := 0
	for {
		n, full, err := r.batch(ctx)
		total += n
		if err != nil || !full {
			return total, err
		}
	}
}

// batch relays at most BatchSize rows in one transaction. A hook, publish or realtime failure stops
// the batch at that row: the rows before it are marked published, the failing row records attempts and
// last_error, and it is retried with everything after it on the next round (at-least-once, id order).
// A row whose publish was confirmed before its realtime step failed is not published again.
func (r *Relay) batch(ctx context.Context) (published int, full bool, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("outbox relay: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := outboxdb.New(tx)
	rows, err := q.LockBatch(ctx, int32(r.opt.BatchSize))
	if err != nil {
		return 0, false, fmt.Errorf("outbox relay: lock batch: %w", err)
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	done := make([]int64, 0, len(rows))
	var failure error
	for _, row := range rows {
		if err := r.relayRow(ctx, row); err != nil {
			failure = fmt.Errorf("outbox relay: event %d (%s): %w", row.ID, row.RoutingKey, err)
			if rerr := q.RecordFailure(ctx, outboxdb.RecordFailureParams{ID: row.ID, LastError: ptr(truncate(err.Error(), 1000))}); rerr != nil {
				return 0, false, errors.Join(failure, rerr)
			}
			break
		}
		done = append(done, row.ID)
	}
	if len(done) > 0 {
		if err := q.MarkPublished(ctx, done); err != nil {
			return 0, false, fmt.Errorf("outbox relay: mark published: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("outbox relay: commit: %w", err)
	}
	r.opt.Metrics.published(len(done))
	if failure != nil {
		r.opt.Metrics.failed()
		return len(done), false, failure
	}
	return len(done), len(rows) == r.opt.BatchSize, nil
}

func (r *Relay) relayRow(ctx context.Context, row outboxdb.LockBatchRow) error {
	if hook := r.opt.Hooks[row.RoutingKey]; hook != nil {
		if err := hook(ctx, row.Payload); err != nil {
			return fmt.Errorf("hook: %w", err)
		}
	}
	if r.confirmed != row.ID {
		if r.pub == nil {
			p, err := r.publisher()
			if err != nil {
				return err
			}
			r.pub = p
		}
		msg := amqp.Publishing{
			MessageId: strconv.FormatInt(row.ID, 10), Timestamp: row.CreatedAt.UTC(), ContentType: mq.ContentTypeJSON,
			Headers: messageHeaders(row), Body: row.Payload,
		}
		if err := r.pub.Publish(ctx, row.Exchange, row.RoutingKey, msg); err != nil {
			r.pub = nil // a channel error closes the channel: open a new one next time
			return err
		}
		r.confirmed = row.ID
	}
	if len(row.RealtimeTopics) > 0 {
		if _, err := r.rt.Publish(ctx, realtime.Event{
			Type: row.EventType, EventID: row.EventID.String(), Topics: row.RealtimeTopics, Data: row.Payload,
		}); err != nil {
			return fmt.Errorf("realtime: %w", err)
		}
	}
	r.confirmed = 0
	return nil
}

// messageHeaders are the producer's headers plus tenant_id, event_type, occurred_at and event_id
// (Appendix B §B.5.2); traceparent comes from the producer's headers.
func messageHeaders(row outboxdb.LockBatchRow) amqp.Table {
	h := amqp.Table{}
	var extra map[string]any
	if err := json.Unmarshal(row.Headers, &extra); err == nil {
		for k, v := range extra {
			if s, ok := v.(string); ok {
				h[k] = s
			}
		}
	}
	if row.TenantID != nil {
		h[mq.HeaderTenantID] = row.TenantID.String()
	}
	h[mq.HeaderEventType] = row.EventType
	h[mq.HeaderOccurredAt] = row.CreatedAt.UTC().Format(time.RFC3339Nano)
	h[mq.HeaderEventID] = row.EventID.String()
	return h
}

// reportBacklog refreshes outbox_pending and outbox_lag_seconds at most once a second.
func (r *Relay) reportBacklog(ctx context.Context) {
	if r.opt.Metrics == nil || time.Since(r.lastBacklog) < time.Second {
		return
	}
	r.lastBacklog = time.Now()
	b, err := outboxdb.New(r.pool).Backlog(ctx)
	if err != nil {
		return
	}
	r.opt.Metrics.pending.Set(float64(b.Pending))
	r.opt.Metrics.lag.Set(b.LagSeconds)
}

// RelayMetrics are the relay instruments of main spec §7.1.
type RelayMetrics struct {
	pending  prometheus.Gauge
	lag      prometheus.Gauge
	relayed  prometheus.Counter
	failures prometheus.Counter
}

// NewRelayMetrics registers outbox_pending, outbox_lag_seconds, outbox_published_total and
// outbox_publish_failures_total.
func NewRelayMetrics(reg prometheus.Registerer) *RelayMetrics {
	m := &RelayMetrics{
		pending:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "outbox_pending", Help: "Outbox rows not yet published."}),
		lag:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "outbox_lag_seconds", Help: "Age of the oldest unpublished outbox row."}),
		relayed:  prometheus.NewCounter(prometheus.CounterOpts{Name: "outbox_published_total", Help: "Outbox rows published."}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{Name: "outbox_publish_failures_total", Help: "Relay batches stopped by a failed publish."}),
	}
	reg.MustRegister(m.pending, m.lag, m.relayed, m.failures)
	return m
}

func (m *RelayMetrics) published(n int) {
	if m != nil && n > 0 {
		m.relayed.Add(float64(n))
	}
}

func (m *RelayMetrics) failed() {
	if m != nil {
		m.failures.Inc()
	}
}

func ptr[T any](v T) *T { return &v }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
