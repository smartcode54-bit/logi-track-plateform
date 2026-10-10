//go:build integration

// Acceptance tests of the retry ladder and the topology (issue T10) against rabbitmq:4-management-alpine:
// the worker's Declare is accepted by a broker loaded from the committed definitions, a poison message
// reaches {queue}.dead after five retries, and a retry returns only to the queue that failed.
package mq_test

import (
	"context"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

func TestMain(m *testing.M) {
	code := m.Run()
	asynctest.Terminate()
	os.Exit(code)
}

// fast is the Appendix B topology with millisecond rungs: same names, keys and bindings.
var fast = mq.Topology{Queues: mq.Queues, RetryDelays: []mq.RetryDelay{
	{Label: "10s", TTL: 50 * time.Millisecond}, {Label: "1m", TTL: 60 * time.Millisecond},
	{Label: "5m", TTL: 70 * time.Millisecond}, {Label: "30m", TTL: 80 * time.Millisecond},
	{Label: "2h", TTL: 90 * time.Millisecond},
}}

func connect(t *testing.T, topo mq.Topology) *amqp.Connection {
	t.Helper()
	url, _ := asynctest.SharedRabbit(t).VHost(t)
	conn, err := mq.Dial(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := mq.DeclareTopology(conn, topo); err != nil {
		t.Fatal(err)
	}
	return conn
}

func publish(t *testing.T, conn *amqp.Connection, exchange, key, id string) {
	t.Helper()
	p, err := mq.NewPublisher(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if err := p.Publish(context.Background(), exchange, key, amqp.Publishing{
		MessageId: id, Body: []byte(`{"tripId":"x"}`), Headers: amqp.Table{mq.HeaderEventType: key},
	}); err != nil {
		t.Fatal(err)
	}
}

func depth(t *testing.T, conn *amqp.Connection, queue string) int {
	t.Helper()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	return q.Messages
}

func consume(t *testing.T, conn *amqp.Connection, topo mq.Topology, queue string, h mq.Handler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mq.Consume(ctx, conn, mq.Registration{Queue: queue, Handler: h},
			mq.ConsumerOptions{Topology: topo, Log: zerolog.Nop(), ShutdownTimeout: 5 * time.Second})
	}()
	t.Cleanup(func() { cancel(); <-done })
}

func TestDeclareIsAcceptedByTheCommittedDefinitions(t *testing.T) {
	r := asynctest.SharedRabbit(t)
	url, vhost := r.VHost(t)
	defs, err := os.ReadFile("../../../deploy/rabbitmq-definitions.json")
	if err != nil {
		t.Fatal(err)
	}
	r.ImportDefinitions(t, vhost, defs) // what rabbitmq-init does in compose
	conn, err := mq.Dial(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	for range 2 { // idempotent: the worker asserts the topology at every (re)connect
		if err := mq.DeclareTopology(conn, mq.Default); err != nil {
			t.Fatalf("the worker's Declare disagrees with deploy/rabbitmq-definitions.json: %v", err)
		}
	}
	// A different argument (another TTL) is refused: the assertion is a real check.
	other := mq.Topology{Queues: mq.Queues, RetryDelays: fast.RetryDelays}
	if err := mq.DeclareTopology(conn, other); err == nil {
		t.Fatal("declaring retry.10s with another x-message-ttl succeeded; want PRECONDITION_FAILED")
	}
}

// AC: a poison message lands in {queue}.dead after 5 retries.
func TestPoisonMessageIsDeadLetteredAfterFiveRetries(t *testing.T) {
	conn := connect(t, fast)
	var mu sync.Mutex
	var attempts []int
	consume(t, conn, fast, "billing.compute", func(_ context.Context, d *mq.Delivery) error {
		mu.Lock()
		attempts = append(attempts, d.Attempts)
		mu.Unlock()
		if d.RoutingKey != "trip.delivered" || d.Exchange != mq.ExchangeEvents {
			t.Errorf("attempt %d sees %s/%s, want the original lt.events/trip.delivered", d.Attempts, d.Exchange, d.RoutingKey)
		}
		return context.DeadlineExceeded // transient, every time
	})
	publish(t, conn, mq.ExchangeEvents, "trip.delivered", "41")

	asynctest.Eventually(t, 20*time.Second, "billing.compute.dead to hold the message", func() bool {
		return depth(t, conn, "billing.compute.dead") == 1
	})
	time.Sleep(300 * time.Millisecond) // nothing else may arrive
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(attempts, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatalf("handler saw attempts %v, want 0..5 (the first delivery and five retries)", attempts)
	}
	if n := depth(t, conn, "billing.compute"); n != 0 {
		t.Fatalf("billing.compute still holds %d messages", n)
	}
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	d, ok, err := ch.Get("billing.compute.dead", true)
	if err != nil || !ok {
		t.Fatalf("get dead message: %v %v", ok, err)
	}
	if d.MessageId != "41" || d.Headers[mq.HeaderAttempts] != int32(5) || d.Headers[mq.HeaderLastError] == nil {
		t.Fatalf("dead message id %q headers %v", d.MessageId, d.Headers)
	}
}

// AC: a retried message is redelivered only to the queue that failed, not to the other queues bound
// to the same routing key (trip.delivered is bound to billing.compute and notify.line).
func TestRetryReturnsOnlyToTheFailingQueue(t *testing.T) {
	conn := connect(t, fast)
	var billing, line atomic.Int32
	consume(t, conn, fast, "billing.compute", func(context.Context, *mq.Delivery) error {
		if billing.Add(1) <= 2 {
			return context.DeadlineExceeded
		}
		return nil
	})
	consume(t, conn, fast, "notify.line", func(context.Context, *mq.Delivery) error {
		line.Add(1)
		return nil
	})
	publish(t, conn, mq.ExchangeEvents, "trip.delivered", "42")
	asynctest.Eventually(t, 10*time.Second, "billing.compute to succeed on its third delivery", func() bool {
		return billing.Load() == 3
	})
	time.Sleep(500 * time.Millisecond)
	if b, l := billing.Load(), line.Load(); b != 3 || l != 1 {
		t.Fatalf("billing.compute ran %d times (want 3), notify.line %d times (want 1)", b, l)
	}
	for _, q := range []string{"billing.compute.dead", "notify.line.dead", "billing.compute", "notify.line"} {
		if n := depth(t, conn, q); n != 0 {
			t.Fatalf("%s holds %d messages", q, n)
		}
	}
}

func TestPermanentFailureIsAckedWithoutRetry(t *testing.T) {
	conn := connect(t, fast)
	var calls atomic.Int32
	consume(t, conn, fast, "storage.gc", func(context.Context, *mq.Delivery) error {
		calls.Add(1)
		return mq.Permanent(context.Canceled)
	})
	publish(t, conn, mq.ExchangeJobs, "job.storage.gc", "43")
	asynctest.Eventually(t, 5*time.Second, "the permanent failure", func() bool { return calls.Load() == 1 })
	time.Sleep(400 * time.Millisecond)
	if c := calls.Load(); c != 1 {
		t.Fatalf("handler ran %d times, want 1", c)
	}
	if n := depth(t, conn, "storage.gc.dead"); n != 0 {
		t.Fatalf("storage.gc.dead holds %d", n)
	}
}

func TestPanicIsRetried(t *testing.T) {
	conn := connect(t, fast)
	var calls atomic.Int32
	consume(t, conn, fast, "storage.gc", func(context.Context, *mq.Delivery) error {
		if calls.Add(1) == 1 {
			panic("boom")
		}
		return nil
	})
	publish(t, conn, mq.ExchangeJobs, "job.storage.gc", "44")
	asynctest.Eventually(t, 5*time.Second, "the retry after the panic", func() bool { return calls.Load() == 2 })
}
