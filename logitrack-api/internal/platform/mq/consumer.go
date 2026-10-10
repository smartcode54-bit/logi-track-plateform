package mq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const instrumentation = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"

// Delivery is one message as a Handler sees it. Exchange and RoutingKey are those of the first
// delivery: a retried message arrives from lt.requeue with key {delay}.{queue}, and the worker keeps
// the original pair in headers.
type Delivery struct {
	Queue      string
	MessageID  string // outbox_events.id as decimal text (R57); the consumer_inbox key
	EventID    string // outbox_events.event_id, empty for messages that did not come from the relay
	Exchange   string
	RoutingKey string
	EventType  string
	TenantID   *uuid.UUID // nil for platform events (R12)
	OccurredAt time.Time
	Attempts   int // failed attempts before this delivery: 0 on the first, 5 on the last
	Body       []byte
	Headers    amqp.Table
}

// Decode unmarshals the JSON body. A body that does not decode is a permanent failure: retrying the
// same bytes cannot succeed.
func (d *Delivery) Decode(v any) error {
	if err := json.Unmarshal(d.Body, v); err != nil {
		return Permanent(fmt.Errorf("mq: decode %s body: %w", d.RoutingKey, err))
	}
	return nil
}

// Handler processes one delivery. nil acks it. An error wrapped with Permanent acks it too (the
// handler records the outcome on the entity or job: validation, no_rate, tenant_orphan, missing
// entity, provider 4xx). Any other error is transient (network, 5xx, 429, serialization failure, a
// panic) and schedules a retry on the ladder of Appendix B §B.5.4; after the last rung the message is
// dead-lettered to {queue}.dead.
type Handler func(ctx context.Context, d *Delivery) error

type permanentError struct{ err error }

func (e permanentError) Error() string { return "permanent: " + e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent marks err as not retryable. Permanent(nil) is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// IsPermanent reports whether err (or an error it wraps) was marked with Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// DefaultHandlerTimeout bounds one handler call when the registration sets none.
const DefaultHandlerTimeout = 2 * time.Minute

// Registration binds a handler to a work queue of the topology.
type Registration struct {
	Queue   string
	Handler Handler
	Timeout time.Duration // per delivery; 0 = DefaultHandlerTimeout
}

// ConsumerOptions are shared by every queue of a worker.
type ConsumerOptions struct {
	Topology        Topology
	Log             zerolog.Logger
	Metrics         *ConsumerMetrics
	DefaultPrefetch int           // RABBITMQ_PREFETCH: used only by a queue without its own prefetch
	ShutdownTimeout time.Duration // in-flight handlers get this long after ctx ends
}

// Outcomes recorded in mq_messages_total.
const (
	OutcomeAck       = "ack"       // handled
	OutcomePermanent = "permanent" // acked without retry
	OutcomeRetry     = "retry"     // republished to lt.retry, then acked
	OutcomeDead      = "dead"      // sixth failure, dead-lettered to {queue}.dead
	OutcomeRequeued  = "requeued"  // the retry could not be published: nacked with requeue
)

// ConsumerMetrics are the worker instruments.
type ConsumerMetrics struct {
	messages  *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	deadDepth *prometheus.GaugeVec
}

// NewConsumerMetrics registers mq_messages_total, mq_handler_duration_seconds and
// mq_dead_letter_depth (alert when > 0, main spec §7.2).
func NewConsumerMetrics(reg prometheus.Registerer) *ConsumerMetrics {
	m := &ConsumerMetrics{
		messages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mq_messages_total", Help: "Deliveries handled per work queue and outcome.",
		}, []string{"queue", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "mq_handler_duration_seconds", Help: "Handler latency per work queue.",
			Buckets: prometheus.DefBuckets,
		}, []string{"queue"}),
		deadDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mq_dead_letter_depth", Help: "Messages waiting in {queue}.dead; alert when above 0.",
		}, []string{"queue"}),
	}
	reg.MustRegister(m.messages, m.duration, m.deadDepth)
	return m
}

func (m *ConsumerMetrics) observe(queue, outcome string, d time.Duration) {
	if m == nil {
		return
	}
	m.messages.WithLabelValues(queue, outcome).Inc()
	m.duration.WithLabelValues(queue).Observe(d.Seconds())
}

// Consume runs reg on conn until ctx ends or the channel fails. With ctx ended it cancels the
// subscription, lets in-flight handlers finish for up to ShutdownTimeout and returns nil; messages
// not acked by then return to the queue when the channel closes.
func Consume(ctx context.Context, conn *amqp.Connection, reg Registration, opt ConsumerOptions) error {
	q, ok := opt.Topology.Queue(reg.Queue)
	if !ok {
		return fmt.Errorf("mq: %s is not a work queue of the topology", reg.Queue)
	}
	if reg.Handler == nil {
		return fmt.Errorf("mq: %s has no handler", reg.Queue)
	}
	prefetch := q.Prefetch
	if prefetch <= 0 {
		prefetch = max(opt.DefaultPrefetch, 1)
	}
	timeout := reg.Timeout
	if timeout <= 0 {
		timeout = DefaultHandlerTimeout
	}
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("mq: open channel for %s: %w", q.Name, err)
	}
	defer func() { _ = ch.Close() }()
	closed := ch.NotifyClose(make(chan *amqp.Error, 1))
	if err := ch.Qos(prefetch, 0, false); err != nil {
		return fmt.Errorf("mq: qos for %s: %w", q.Name, err)
	}
	retry, err := NewPublisher(conn)
	if err != nil {
		return err
	}
	defer func() { _ = retry.Close() }()
	tag := "lt-" + q.Name + "-" + uuid.NewString()
	deliveries, err := ch.Consume(q.Name, tag, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("mq: consume %s: %w", q.Name, err)
	}

	c := &consumer{queue: q, topo: opt.Topology, handler: reg.Handler, timeout: timeout,
		retry: retry, log: opt.Log.With().Str("queue", q.Name).Logger(), metrics: opt.Metrics}
	// Handlers run on a context that outlives ctx by the shutdown grace, so a SIGTERM does not abort
	// a half-done side effect.
	hctx, cancelHandlers := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelHandlers()
	var wg sync.WaitGroup
	for range prefetch {
		wg.Go(func() {
			for d := range deliveries {
				c.process(hctx, d)
			}
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	var runErr error
	select {
	case <-ctx.Done():
		_ = ch.Cancel(tag, false) // closes deliveries once the broker confirms
	case e := <-closed:
		if e != nil {
			runErr = fmt.Errorf("mq: channel of %s closed: %w", q.Name, e)
		} else {
			runErr = fmt.Errorf("mq: channel of %s closed", q.Name)
		}
	case <-done:
		runErr = fmt.Errorf("mq: deliveries of %s ended", q.Name)
	}
	grace := opt.ShutdownTimeout
	if grace <= 0 {
		grace = 30 * time.Second
	}
	select {
	case <-done:
	case <-time.After(grace):
		cancelHandlers()
		<-done
	}
	return runErr
}

type consumer struct {
	queue   Queue
	topo    Topology
	handler Handler
	timeout time.Duration
	retry   *Publisher
	log     zerolog.Logger
	metrics *ConsumerMetrics
}

func (c *consumer) process(ctx context.Context, raw amqp.Delivery) {
	start := time.Now()
	d := toDelivery(c.queue.Name, raw)
	ctx = otel.GetTextMapPropagator().Extract(ctx, headerCarrier(raw.Headers))
	ctx, span := otel.Tracer(instrumentation).Start(ctx, "consume "+c.queue.Name, trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.destination.name", c.queue.Name),
			attribute.String("messaging.message.id", d.MessageID),
			attribute.String("messaging.rabbitmq.destination.routing_key", d.RoutingKey),
			attribute.Int("logitrack.attempts", d.Attempts)))
	defer span.End()
	log := c.log.With().Str("message_id", d.MessageID).Str("routing_key", d.RoutingKey).Int("attempts", d.Attempts).Logger()
	ctx = log.WithContext(ctx)

	err := c.call(ctx, d)
	outcome := OutcomeAck
	switch {
	case err == nil:
		c.ack(raw, &log)
	case IsPermanent(err):
		outcome = OutcomePermanent
		log.Warn().Err(err).Msg("permanent failure: acked without retry")
		c.ack(raw, &log)
	default:
		span.SetStatus(codes.Error, "handler failed")
		outcome = c.fail(ctx, raw, d, err, &log)
	}
	span.SetAttributes(attribute.String("logitrack.outcome", outcome))
	c.metrics.observe(c.queue.Name, outcome, time.Since(start))
}

// call runs the handler with the per-delivery timeout; a panic is a transient failure.
func (c *consumer) call(ctx context.Context, d *Delivery) (err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mq: handler panic: %v", r)
		}
	}()
	return c.handler(ctx, d)
}

// fail applies the retry ladder (Appendix B §B.5.4): failed attempt n (1-5) goes to lt.retry with key
// {delay}.{queue} and x-attempts = n, then the delivery is acked; the sixth failure is nacked without
// requeue, so the queue's dead-letter exchange lt.dlx routes it to {queue}.dead.
func (c *consumer) fail(ctx context.Context, raw amqp.Delivery, d *Delivery, herr error, log *zerolog.Logger) string {
	failed := d.Attempts + 1
	rung, ok := c.topo.RetryDelay(failed)
	if !ok {
		log.Error().Err(herr).Msg("retries exhausted: dead-lettered")
		if err := raw.Nack(false, false); err != nil {
			log.Error().Err(err).Msg("nack")
		}
		return OutcomeDead
	}
	h := cloneTable(raw.Headers)
	h[HeaderAttempts] = int32(failed)
	if _, ok := h[HeaderOriginalRoutingKey]; !ok {
		h[HeaderOriginalExchange] = raw.Exchange
		h[HeaderOriginalRoutingKey] = raw.RoutingKey
	}
	h[HeaderLastError] = truncate(herr.Error(), 500)
	delete(h, "x-death") // the broker's own dead-letter history of the previous rung
	msg := amqp.Publishing{
		Headers: h, ContentType: raw.ContentType, MessageId: raw.MessageId, Timestamp: raw.Timestamp,
		Type: raw.Type, AppId: raw.AppId, Body: raw.Body,
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := c.retry.Publish(pctx, ExchangeRetry, rung.Label+"."+c.queue.Name, msg); err != nil {
		log.Error().Err(err).AnErr("handler_error", herr).Msg("retry publish failed: requeued")
		if err := raw.Nack(false, true); err != nil {
			log.Error().Err(err).Msg("nack")
		}
		return OutcomeRequeued
	}
	log.Warn().Err(herr).Str("retry_in", rung.Label).Msg("transient failure: retry scheduled")
	c.ack(raw, log)
	return OutcomeRetry
}

func (c *consumer) ack(raw amqp.Delivery, log *zerolog.Logger) {
	if err := raw.Ack(false); err != nil {
		log.Error().Err(err).Msg("ack failed: the message will be redelivered")
	}
}

func toDelivery(queue string, raw amqp.Delivery) *Delivery {
	d := &Delivery{
		Queue: queue, MessageID: raw.MessageId, Exchange: raw.Exchange, RoutingKey: raw.RoutingKey,
		Body: raw.Body, Headers: raw.Headers,
	}
	if v, ok := raw.Headers[HeaderOriginalExchange].(string); ok {
		d.Exchange = v
	}
	if v, ok := raw.Headers[HeaderOriginalRoutingKey].(string); ok {
		d.RoutingKey = v
	}
	d.EventType, _ = raw.Headers[HeaderEventType].(string)
	d.EventID, _ = raw.Headers[HeaderEventID].(string)
	if v, ok := raw.Headers[HeaderTenantID].(string); ok && v != "" {
		if id, err := uuid.Parse(v); err == nil {
			d.TenantID = &id
		}
	}
	if v, ok := raw.Headers[HeaderOccurredAt].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			d.OccurredAt = t
		}
	}
	d.Attempts = intHeader(raw.Headers[HeaderAttempts])
	return d
}

// intHeader reads an AMQP integer of any width (the broker and other clients may re-encode it).
func intHeader(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int8:
		return int(n)
	case int16:
		return int(n)
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint8:
		return int(n)
	case uint16:
		return int(n)
	case uint32:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// headerCarrier adapts AMQP headers to the OpenTelemetry propagator (traceparent, R57 headers).
type headerCarrier amqp.Table

func (h headerCarrier) Get(k string) string { v, _ := h[k].(string); return v }
func (h headerCarrier) Set(k, v string)     { h[k] = v }
func (h headerCarrier) Keys() []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	return keys
}

// MonitorDeadLetters sets mq_dead_letter_depth for every {queue}.dead of t every interval until ctx
// ends or the connection fails.
func MonitorDeadLetters(ctx context.Context, conn *amqp.Connection, t Topology, m *ConsumerMetrics, interval time.Duration) error {
	if m == nil {
		return nil
	}
	var ch *amqp.Channel
	defer func() {
		if ch != nil {
			_ = ch.Close()
		}
	}()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		for _, q := range t.Queues {
			if ch == nil || ch.IsClosed() {
				var err error
				if ch, err = conn.Channel(); err != nil {
					return fmt.Errorf("mq: dead-letter monitor channel: %w", err)
				}
			}
			info, err := ch.QueueDeclarePassive(DeadQueue(q.Name), true, false, false, false, nil)
			if err != nil {
				continue // the channel is closed by the broker; reopened on the next queue
			}
			m.deadDepth.WithLabelValues(q.Name).Set(float64(info.Messages))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
