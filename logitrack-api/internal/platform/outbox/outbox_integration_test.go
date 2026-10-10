//go:build integration

// Acceptance tests of the outbox and its relay (issue T10) on postgres:18-alpine, RabbitMQ and Redis:
// a committed event is delivered and a rolled-back one never is, the relay wakes on NOTIFY, a failed
// publish stops the batch and is retried, and realtime ids are {seq}-0 and strictly increasing across
// topics.
package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) {
	code := pgtest.Main(m)
	asynctest.Terminate()
	os.Exit(code)
}

const prefix = "lt:local:"

type env struct {
	pool  *pgxpool.Pool
	conn  *amqp.Connection
	rdb   *redis.Client
	rt    *realtime.Writer
	relay *outbox.Relay
}

// setup migrates a fresh database, opens a vhost with the Appendix B topology plus a tap queue that
// sees every lt.events message, and builds a relay on them.
func setup(t *testing.T, interval time.Duration) *env {
	t.Helper()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := &env{pool: d.Pool(t, db.RoleApp), rdb: asynctest.SharedRedis(t).Client(t)}
	url, _ := asynctest.SharedRabbit(t).VHost(t)
	conn, err := mq.Dial(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	e.conn = conn
	if err := mq.DeclareTopology(conn, mq.Default); err != nil {
		t.Fatal(err)
	}
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	if _, err := ch.QueueDeclare("tap", true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := ch.QueueBind("tap", "#", mq.ExchangeEvents, false, nil); err != nil {
		t.Fatal(err)
	}
	e.rt = realtime.NewWriter(e.rdb, realtime.NewKeys(prefix), 1000, time.Hour)
	e.relay = outbox.NewRelay(e.pool, func() (outbox.Publisher, error) { return mq.NewPublisher(conn) }, e.rt,
		outbox.RelayOptions{Interval: interval, Log: zerolog.Nop(), Metrics: outbox.NewRelayMetrics(prometheus.NewRegistry())})
	return e
}

func appendIn(t *testing.T, pool *pgxpool.Pool, commit bool, events ...outbox.Event) []outbox.Appended {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []outbox.Appended
	for _, ev := range events {
		a, err := outbox.Append(ctx, tx, ev)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	if commit {
		err = tx.Commit(ctx)
	} else {
		err = tx.Rollback(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func trip(id string, topics ...string) outbox.Event {
	return outbox.Event{RoutingKey: "trip.delivered", AggregateType: "trip", AggregateID: id,
		Payload: map[string]string{"tripId": id}, Topics: topics}
}

func drainTap(t *testing.T, conn *amqp.Connection) []amqp.Delivery {
	t.Helper()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	var out []amqp.Delivery
	for {
		d, ok, err := ch.Get("tap", true)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return out
		}
		out = append(out, d)
	}
}

// AC: an event inserted in the same transaction as a write is delivered at least once; a
// rolled-back transaction publishes nothing.
func TestCommittedEventIsDeliveredRolledBackIsNot(t *testing.T) {
	e := setup(t, time.Hour)
	ctx := context.Background()
	committed := appendIn(t, e.pool, true, trip("t-committed"))
	appendIn(t, e.pool, false, trip("t-rolled-back"))

	if n, err := e.relay.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("drain published %d (%v), want exactly the committed event", n, err)
	}
	got := drainTap(t, e.conn)
	if len(got) != 1 {
		t.Fatalf("broker received %d messages, want 1", len(got))
	}
	m := got[0]
	if m.MessageId != strconv.FormatInt(committed[0].ID, 10) || m.RoutingKey != "trip.delivered" ||
		string(m.Body) != `{"tripId": "t-committed"}` && string(m.Body) != `{"tripId":"t-committed"}` {
		t.Fatalf("message id %q key %q body %s", m.MessageId, m.RoutingKey, m.Body)
	}
	if m.DeliveryMode != amqp.Persistent || m.ContentType != mq.ContentTypeJSON ||
		m.Headers[mq.HeaderEventType] != "trip.delivered" || m.Headers[mq.HeaderEventID] != committed[0].EventID.String() ||
		m.Headers[mq.HeaderOccurredAt] == nil {
		t.Fatalf("message properties: mode %d type %q headers %v", m.DeliveryMode, m.ContentType, m.Headers)
	}
	var published, total int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE published_at IS NOT NULL), count(*) FROM outbox_events`).
		Scan(&published, &total); err != nil {
		t.Fatal(err)
	}
	if published != 1 || total != 1 {
		t.Fatalf("outbox has %d rows, %d published; want 1 and 1", total, published)
	}
	// The bound work queues hold it too (billing.compute and notify.line bind trip.delivered).
	ch, err := e.conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	for _, q := range []string{"billing.compute", "notify.line"} {
		info, err := ch.QueueDeclarePassive(q, true, false, false, false, nil)
		if err != nil || info.Messages != 1 {
			t.Fatalf("%s holds %d messages (%v), want 1", q, info.Messages, err)
		}
	}
	// A second drain publishes nothing more.
	if n, err := e.relay.Drain(ctx); err != nil || n != 0 {
		t.Fatalf("second drain published %d (%v)", n, err)
	}
}

// The relay wakes on NOTIFY outbox_new, long before its fallback tick.
func TestRelayWakesOnNotify(t *testing.T) {
	e := setup(t, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = e.relay.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(300 * time.Millisecond) // LISTEN is up
	appendIn(t, e.pool, true, trip("t-notify"))
	var got []amqp.Delivery
	asynctest.Eventually(t, 3*time.Second, "the notified event on the broker", func() bool {
		got = append(got, drainTap(t, e.conn)...)
		return len(got) == 1
	})
}

// failingPublisher fails the n-th publish once.
type failingPublisher struct {
	next   outbox.Publisher
	failAt int
	calls  int
}

func (p *failingPublisher) Publish(ctx context.Context, exchange, key string, msg amqp.Publishing) error {
	p.calls++
	if p.calls == p.failAt {
		return errors.New("broker went away")
	}
	return p.next.Publish(ctx, exchange, key, msg)
}

// A failed publish stops the batch at that row (attempts, last_error) and the rest follows on the
// next round: at-least-once, in id order.
func TestFailedPublishStopsTheBatchAndIsRetried(t *testing.T) {
	e := setup(t, time.Hour)
	ctx := context.Background()
	real, err := mq.NewPublisher(e.conn)
	if err != nil {
		t.Fatal(err)
	}
	fp := &failingPublisher{next: real, failAt: 2}
	relay := outbox.NewRelay(e.pool, func() (outbox.Publisher, error) { return fp, nil }, e.rt,
		outbox.RelayOptions{Log: zerolog.Nop()})
	ids := appendIn(t, e.pool, true, trip("a"), trip("b"), trip("c"))
	n, err := relay.Drain(ctx)
	if err == nil || n != 1 {
		t.Fatalf("first drain published %d (%v), want 1 and an error", n, err)
	}
	var attempts int
	var lastErr *string
	if err := e.pool.QueryRow(ctx, `SELECT attempts, last_error FROM outbox_events WHERE id = $1`, ids[1].ID).
		Scan(&attempts, &lastErr); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastErr == nil || !strings.Contains(*lastErr, "broker went away") {
		t.Fatalf("failed row attempts %d last_error %v", attempts, lastErr)
	}
	if n, err := relay.Drain(ctx); err != nil || n != 2 {
		t.Fatalf("second drain published %d (%v), want the remaining 2", n, err)
	}
	var order []string
	for _, d := range drainTap(t, e.conn) {
		order = append(order, d.MessageId)
	}
	want := []string{strconv.FormatInt(ids[0].ID, 10), strconv.FormatInt(ids[1].ID, 10), strconv.FormatInt(ids[2].ID, 10)}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("broker order %v, want %v", order, want)
	}
}

// AC: realtime stream ids are {seq}-0 and strictly increasing across topics (one global sequence,
// R52); PUBLISH carries the same id; ephemeral topics get no replay log.
func TestRealtimeIDsAreGlobalAndIncreasing(t *testing.T) {
	e := setup(t, time.Hour)
	ctx := context.Background()
	const tenant = "0199c000-0000-7000-8000-00000000a001"
	const user = "0199c000-0000-7000-8000-00000000b001"
	trips, usr := "tenant:"+tenant+":trips", "user:"+user
	loc := "tenant:" + tenant + ":vehicle_locations"
	sub := e.rdb.PSubscribe(ctx, prefix+"rt:*")
	defer func() { _ = sub.Close() }()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	appendIn(t, e.pool, true,
		trip("r1", trips),
		outbox.Event{RoutingKey: "user.sessions_revoked", AggregateType: "user", AggregateID: user,
			Payload: map[string]any{"reason": "logout_all"}, Topics: []string{usr}},
		trip("r3", trips, usr),
		outbox.Event{RoutingKey: "vehicle_locations.updated", AggregateType: "tenant", AggregateID: tenant,
			Payload: map[string]any{"n": 1}, Topics: []string{loc}},
	)
	if _, err := e.relay.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	ids := func(topic string) []int64 {
		msgs, err := e.rdb.XRange(ctx, prefix+"rtlog:"+topic, "-", "+").Result()
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, m := range msgs {
			seq, sfx, ok := strings.Cut(m.ID, "-")
			n, err := strconv.ParseInt(seq, 10, 64)
			if !ok || sfx != "0" || err != nil {
				t.Fatalf("stream id %q is not {seq}-0", m.ID)
			}
			if m.Values["type"] == "" || m.Values["data"] == "" || m.Values["event_id"] == "" {
				t.Fatalf("stream entry fields %v", m.Values)
			}
			out = append(out, n)
		}
		return out
	}
	tripIDs, userIDs := ids(trips), ids(usr)
	if len(tripIDs) != 2 || len(userIDs) != 2 {
		t.Fatalf("trips log %v, user log %v; want two entries each", tripIDs, userIDs)
	}
	r1, r2, r3 := tripIDs[0], userIDs[0], tripIDs[1]
	if r1 >= r2 || r2 >= r3 || userIDs[1] != r3 {
		t.Fatalf("ids r1=%d r2=%d r3=%d (user log %v): want one strictly increasing global sequence", r1, r2, r3, userIDs)
	}
	if n, _ := e.rdb.Exists(ctx, prefix+"rtlog:"+loc).Result(); n != 0 {
		t.Fatal("the ephemeral vehicle_locations topic got a replay log")
	}
	seq, err := e.rdb.Get(ctx, prefix+"rtlog:seq").Int64()
	if err != nil || seq != r3+1 {
		t.Fatalf("rtlog:seq = %d (%v), want %d", seq, err, r3+1)
	}
	if ttl, _ := e.rdb.PTTL(ctx, prefix+"rtlog:"+trips).Result(); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("replay log TTL %v, want RTLOG_TTL", ttl)
	}
	// Five PUBLISHes: r1, r2, r3 twice (two topics), the vehicle batch; each carries its sequence id.
	seen := map[string][]int64{}
	for range 5 {
		msg, err := sub.ReceiveMessage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m realtime.Message
		if err := json.Unmarshal([]byte(msg.Payload), &m); err != nil {
			t.Fatalf("payload %s: %v", msg.Payload, err)
		}
		if msg.Channel != prefix+"rt:"+m.Topic || m.EventID == "" || len(m.Data) == 0 {
			t.Fatalf("channel %s message %+v", msg.Channel, m)
		}
		seen[m.Topic] = append(seen[m.Topic], m.ID)
	}
	if got := seen[trips]; len(got) != 2 || got[0] != r1 || got[1] != r3 {
		t.Fatalf("published ids on %s: %v", trips, got)
	}
	if got := seen[loc]; len(got) != 1 || got[0] != seq {
		t.Fatalf("published ids on %s: %v, want [%d]", loc, got, seq)
	}
}

// A replay log whose top id is ahead of the counter (Redis restored from an older snapshot) moves the
// counter past it instead of failing every later XADD.
func TestRealtimeSequenceSkipsPastAStreamAhead(t *testing.T) {
	rdb := asynctest.SharedRedis(t).Client(t)
	ctx := context.Background()
	w := realtime.NewWriter(rdb, realtime.NewKeys(prefix), 1000, time.Hour)
	if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: prefix + "rtlog:global", ID: "100-0", Values: []string{"type", "x"}}).Err(); err != nil {
		t.Fatal(err)
	}
	rdb.Set(ctx, prefix+"rtlog:seq", 5, 0)
	n, err := w.Publish(ctx, realtime.Event{Type: "hubs.changed", EventID: "e", Topics: []string{"global"}, Data: []byte(`{}`)})
	if err != nil || n != 101 {
		t.Fatalf("publish = %d (%v), want 101", n, err)
	}
	if _, err := w.Publish(ctx, realtime.Event{Type: "x", Topics: []string{"no-such-topic"}}); !errors.Is(err, realtime.ErrInvalidTopic) {
		t.Fatalf("invalid topic: %v", err)
	}
}

// flakyRealtime fails its first fails calls (Redis down), then writes through.
type flakyRealtime struct {
	next  outbox.Realtime
	fails int
	calls int
}

func (f *flakyRealtime) Publish(ctx context.Context, e realtime.Event) (int64, error) {
	f.calls++
	if f.calls <= f.fails {
		return 0, errors.New("dial tcp: connection refused")
	}
	return f.next.Publish(ctx, e)
}

// A realtime failure after a confirmed publish retries only the realtime step: the broker gets one
// copy of the row, the rows behind it follow in id order once Redis is back.
func TestRealtimeFailureDoesNotRepublish(t *testing.T) {
	e := setup(t, time.Hour)
	ctx := context.Background()
	const tenant = "0199c000-0000-7000-8000-00000000a001"
	rt := &flakyRealtime{next: e.rt, fails: 3}
	relay := outbox.NewRelay(e.pool, func() (outbox.Publisher, error) { return mq.NewPublisher(e.conn) }, rt,
		outbox.RelayOptions{Log: zerolog.Nop()})
	ids := appendIn(t, e.pool, true, trip("rt-1", "tenant:"+tenant+":trips"), trip("rt-2"))
	for i := range 3 {
		if n, err := relay.Drain(ctx); err == nil || n != 0 || !strings.Contains(err.Error(), "realtime") {
			t.Fatalf("drain %d during the outage published %d (%v)", i, n, err)
		}
	}
	if n, err := relay.Drain(ctx); err != nil || n != 2 {
		t.Fatalf("drain after recovery published %d (%v), want 2", n, err)
	}
	var order []string
	for _, d := range drainTap(t, e.conn) {
		order = append(order, d.MessageId)
	}
	want := []string{strconv.FormatInt(ids[0].ID, 10), strconv.FormatInt(ids[1].ID, 10)}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("broker received %v, want exactly %v", order, want)
	}
	var attempts int
	if err := e.pool.QueryRow(ctx, `SELECT attempts FROM outbox_events WHERE id = $1`, ids[0].ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("row 1 attempts %d, want 3", attempts)
	}
	if n, _ := e.rdb.XLen(ctx, prefix+"rtlog:tenant:"+tenant+":trips").Result(); n != 1 {
		t.Fatalf("replay log holds %d entries, want 1", n)
	}
}

// A hook runs before the publish of its routing key; its failure stops the batch like a failed
// publish, and nothing of that row reaches the broker until the hook succeeds.
func TestRelayHookRunsBeforePublish(t *testing.T) {
	e := setup(t, time.Hour)
	ctx := context.Background()
	const user = "0199c000-0000-7000-8000-00000000b001"
	var seen []string
	fail := true
	relay := outbox.NewRelay(e.pool, func() (outbox.Publisher, error) { return mq.NewPublisher(e.conn) }, e.rt,
		outbox.RelayOptions{Log: zerolog.Nop(), Hooks: map[string]outbox.Hook{
			"user.sessions_revoked": func(_ context.Context, payload []byte) error {
				if fail {
					return errors.New("redis down")
				}
				seen = append(seen, string(payload))
				return nil
			},
		}})
	appendIn(t, e.pool, true,
		outbox.Event{RoutingKey: "user.sessions_revoked", AggregateType: "user", AggregateID: user,
			Payload: map[string]any{"userId": user}, Topics: []string{"user:" + user}},
		trip("after-hook"))
	if n, err := relay.Drain(ctx); err == nil || n != 0 || !strings.Contains(err.Error(), "hook") {
		t.Fatalf("drain with a failing hook published %d (%v)", n, err)
	}
	if got := drainTap(t, e.conn); len(got) != 0 {
		t.Fatalf("the broker received %d messages before the hook succeeded", len(got))
	}
	fail = false
	if n, err := relay.Drain(ctx); err != nil || n != 2 {
		t.Fatalf("drain published %d (%v), want 2", n, err)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], user) {
		t.Fatalf("hook saw %v", seen)
	}
	if got := drainTap(t, e.conn); len(got) != 2 || got[0].RoutingKey != "user.sessions_revoked" {
		t.Fatalf("broker received %d messages", len(got))
	}
}
