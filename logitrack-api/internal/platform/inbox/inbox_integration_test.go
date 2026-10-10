//go:build integration

// Acceptance test of consumer idempotency (issue T10): a duplicated message_id is processed once, and
// a failed side effect releases the claim so the retry runs it.
package inbox_test

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/inbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) {
	code := pgtest.Main(m)
	asynctest.Terminate()
	os.Exit(code)
}

// database migrates a fresh database and adds a scratch table the consumer writes as its effect.
func database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	d := pgtest.NewDatabase(t)
	ctx := context.Background()
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	owner := d.Pool(t, db.RoleMigrator)
	if _, err := owner.Exec(ctx, `CREATE TABLE test_effects (message_id text NOT NULL);
		GRANT SELECT, INSERT ON test_effects TO logitrack_app`); err != nil {
		t.Fatal(err)
	}
	return d.Pool(t, db.RoleApp)
}

func count(t *testing.T, pool *pgxpool.Pool, sql string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func effect(ctx context.Context, d *mq.Delivery) func(pgx.Tx) error {
	return func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO test_effects (message_id) VALUES ($1)`, d.MessageID)
		return err
	}
}

func TestRunClaimsOncePerConsumer(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	d := &mq.Delivery{Queue: "storage.gc", MessageID: "17"}
	for i, want := range []bool{true, false} {
		processed, err := inbox.Run(ctx, pool, d, effect(ctx, d))
		if err != nil || processed != want {
			t.Fatalf("run %d: processed %v (%v), want %v", i+1, processed, err, want)
		}
	}
	// Another consumer of the same message has its own claim.
	other := &mq.Delivery{Queue: "security.audit", MessageID: "17"}
	if processed, err := inbox.Run(ctx, pool, other, effect(ctx, other)); err != nil || !processed {
		t.Fatalf("second consumer: processed %v (%v)", processed, err)
	}
	// A failing effect rolls the claim back with it.
	failing := &mq.Delivery{Queue: "storage.gc", MessageID: "18"}
	boom := errors.New("boom")
	if _, err := inbox.Run(ctx, pool, failing, func(pgx.Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("failing effect: %v", err)
	}
	if processed, err := inbox.Run(ctx, pool, failing, effect(ctx, failing)); err != nil || !processed {
		t.Fatalf("retry after failure: processed %v (%v)", processed, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM test_effects`); n != 3 {
		t.Fatalf("%d effects, want 3", n)
	}
	if _, err := inbox.Run(ctx, pool, &mq.Delivery{Queue: "storage.gc"}, effect(ctx, d)); !mq.IsPermanent(err) {
		t.Fatalf("empty message id: %v, want a permanent error", err)
	}
}

// AC: a duplicate message_id is processed once, through the broker.
func TestDuplicateMessageIDIsProcessedOnce(t *testing.T) {
	pool := database(t)
	url, _ := asynctest.SharedRabbit(t).VHost(t)
	conn, err := mq.Dial(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := mq.DeclareTopology(conn, mq.Default); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mq.Consume(ctx, conn, mq.Registration{Queue: "storage.gc", Handler: func(ctx context.Context, d *mq.Delivery) error {
			defer calls.Add(1)
			_, err := inbox.Run(ctx, pool, d, effect(ctx, d))
			return err
		}}, mq.ConsumerOptions{Topology: mq.Default, Log: zerolog.Nop()})
	}()
	defer func() { cancel(); <-done }()

	p, err := mq.NewPublisher(conn)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // the relay re-publishing after a lost confirm looks exactly like this
		if err := p.Publish(context.Background(), mq.ExchangeJobs, "job.storage.gc", amqp.Publishing{MessageId: "99", Body: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	asynctest.Eventually(t, 5*time.Second, "both deliveries", func() bool { return calls.Load() == 2 })
	if n := count(t, pool, `SELECT count(*) FROM test_effects WHERE message_id = '99'`); n != 1 {
		t.Fatalf("the effect ran %d times, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM consumer_inbox WHERE consumer = 'storage.gc' AND message_id = '99'`); n != 1 {
		t.Fatalf("consumer_inbox has %d rows, want 1", n)
	}
}
