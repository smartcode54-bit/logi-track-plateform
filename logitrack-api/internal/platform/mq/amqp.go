package mq

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Message headers of Appendix B §B.5.2. The relay sets the first four on every message; the worker
// adds the x- headers when it schedules a retry.
const (
	HeaderTenantID    = "tenant_id"   // outbox_events.tenant_id, absent for platform events (R12)
	HeaderEventType   = "event_type"  // outbox_events.event_type
	HeaderOccurredAt  = "occurred_at" // outbox_events.created_at, RFC 3339 UTC
	HeaderTraceParent = "traceparent" // W3C trace context of the producing request
	HeaderEventID     = "event_id"    // outbox_events.event_id (uuid), the envelope id SSE payloads carry

	HeaderAttempts           = "x-attempts"             // failed attempts so far; absent = 0
	HeaderOriginalExchange   = "x-original-exchange"    // the exchange of the first delivery
	HeaderOriginalRoutingKey = "x-original-routing-key" // its routing key ({delay}.{queue} replaces it on retries)
	HeaderLastError          = "x-last-error"           // short text of the failure that scheduled the retry
)

// ContentTypeJSON is the content type of every message: payloads are JSON objects.
const ContentTypeJSON = "application/json"

// DialTimeout bounds the TCP connect and the AMQP handshake of Dial.
const DialTimeout = 10 * time.Second

// Dial opens a connection named name (shown in the management UI). The error never carries the URL,
// which holds the password.
func Dial(url, name string) (*amqp.Connection, error) {
	if _, err := amqp.ParseURI(url); err != nil {
		return nil, errors.New("mq: RABBITMQ_URL is not a valid amqp:// or amqps:// URL")
	}
	cfg := amqp.Config{
		Properties: amqp.Table{"connection_name": name},
		Heartbeat:  10 * time.Second,
		Dial:       amqp.DefaultDial(DialTimeout),
	}
	conn, err := amqp.DialConfig(url, cfg)
	if err != nil {
		return nil, fmt.Errorf("mq: connect: %w", sanitize(err))
	}
	return conn, nil
}

// sanitize drops anything but the AMQP reply text from a dial error: a parse or network error of
// the client can quote the URL.
func sanitize(err error) error {
	var ae *amqp.Error
	if errors.As(err, &ae) {
		return fmt.Errorf("broker refused the connection (%d %s)", ae.Code, ae.Reason)
	}
	var oe interface{ Timeout() bool }
	if errors.As(err, &oe) && oe.Timeout() {
		return errors.New("timed out")
	}
	return errors.New("broker unreachable")
}

// Declare asserts every exchange, queue and binding of d on ch. Declaring is idempotent, and the
// arguments are exactly those of the definitions document compose loads, so a broker initialised
// from deploy/rabbitmq-definitions.json accepts it; a mismatch is a PRECONDITION_FAILED channel error.
func Declare(ch *amqp.Channel, d Definitions) error {
	for _, e := range d.Exchanges {
		if err := ch.ExchangeDeclare(e.Name, e.Type, e.Durable, e.AutoDelete, e.Internal, false, amqp.Table(e.Arguments)); err != nil {
			return fmt.Errorf("mq: declare exchange %s: %w", e.Name, err)
		}
	}
	for _, q := range d.Queues {
		if _, err := ch.QueueDeclare(q.Name, q.Durable, q.AutoDelete, false, false, amqp.Table(q.Arguments)); err != nil {
			return fmt.Errorf("mq: declare queue %s: %w", q.Name, err)
		}
	}
	for _, b := range d.Bindings {
		if err := ch.QueueBind(b.Destination, b.RoutingKey, b.Source, false, amqp.Table(b.Arguments)); err != nil {
			return fmt.Errorf("mq: bind %s to %s (%s): %w", b.Destination, b.Source, b.RoutingKey, err)
		}
	}
	return nil
}

// DeclareTopology opens a channel on conn, asserts t and closes the channel.
func DeclareTopology(conn *amqp.Connection, t Topology) error {
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("mq: open channel: %w", err)
	}
	defer func() { _ = ch.Close() }()
	return Declare(ch, t.Definitions())
}

// ErrNacked is returned when the broker refuses a published message (publisher confirms).
var ErrNacked = errors.New("mq: the broker nacked the message")

// Publisher publishes persistent messages on one confirm-mode channel and waits for each confirm
// (main spec §7.1). It is safe for concurrent use; a closed channel makes every later call fail and
// the owner opens a new Publisher on a new connection.
type Publisher struct {
	mu sync.Mutex
	ch *amqp.Channel
}

// NewPublisher opens a confirm-mode channel on conn.
func NewPublisher(conn *amqp.Connection) (*Publisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("mq: open channel: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("mq: confirm mode: %w", err)
	}
	return &Publisher{ch: ch}, nil
}

// Publish sends msg and blocks until the broker confirms it. Unroutable messages are confirmed too
// (no mandatory flag): an event that no queue binds, such as the realtime-only job.updated, is simply
// dropped by the exchange.
func (p *Publisher) Publish(ctx context.Context, exchange, key string, msg amqp.Publishing) error {
	msg.DeliveryMode = amqp.Persistent
	if msg.ContentType == "" {
		msg.ContentType = ContentTypeJSON
	}
	p.mu.Lock()
	dc, err := p.ch.PublishWithDeferredConfirmWithContext(ctx, exchange, key, false, false, msg)
	p.mu.Unlock()
	if err != nil {
		return fmt.Errorf("mq: publish to %s: %w", exchange, err)
	}
	ok, err := dc.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("mq: confirm from %s: %w", exchange, err)
	}
	if !ok {
		return ErrNacked
	}
	return nil
}

// Close closes the channel.
func (p *Publisher) Close() error { return p.ch.Close() }

// cloneTable copies an AMQP table so a retry never mutates the delivery it came from.
func cloneTable(t amqp.Table) amqp.Table {
	out := make(amqp.Table, len(t)+4)
	maps.Copy(out, t)
	return out
}
