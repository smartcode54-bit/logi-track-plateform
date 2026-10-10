//go:build integration

// The worker and scheduler processes as cmd/worker and cmd/scheduler run them (T10): configuration from
// the environment, the leader's relay woken by NOTIFY, the worker's topology assertion and the
// notify.email consumer, a clean exit on cancellation.
package app_test

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestWorkerAndSchedulerProcesses(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	amqpURL, _ := asynctest.SharedRabbit(t).VHost(t)
	mp := asynctest.StartMailpit(t)
	for k, v := range map[string]string{
		"APP_ENV": "local", "LOG_LEVEL": "error", "METRICS_ADDR": "127.0.0.1:0", "SHUTDOWN_TIMEOUT": "5s",
		"DATABASE_URL": d.URL(db.RoleApp), "RABBITMQ_URL": amqpURL, "REDIS_URL": asynctest.SharedRedis(t).URL(t),
		"WORKER_CONSUMERS": "notify", "EMAIL_ENABLED": "true", "SMTP_HOST": mp.SMTPHost,
		"SMTP_PORT": strconv.Itoa(mp.SMTPPort), "SMTP_FROM": "no-reply@logitrack.test", "SMTP_STARTTLS": "false",
		"PUBLIC_WEB_BASE_URL": "http://localhost:3000", "OUTBOX_RELAY_INTERVAL": "1m", // only NOTIFY wakes the relay
	} {
		t.Setenv(k, v)
	}
	etl := d.Pool(t, db.RoleETL)
	var user uuid.UUID
	if err := etl.QueryRow(ctx, `INSERT INTO users (email, display_name) VALUES ('ops@logitrack.test', 'Ops') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	// run starts one process and returns a stop function that waits for its exit code. The processes run
	// one after the other: in one test binary they would share logx's process-wide settings.
	run := func(main func(context.Context, io.Writer, io.Writer) int) func() {
		pctx, cancel := context.WithCancel(ctx)
		code := make(chan int, 1)
		go func() { code <- main(pctx, io.Discard, io.Discard) }()
		return func() {
			t.Helper()
			cancel()
			select {
			case c := <-code:
				if c != app.ExitOK {
					t.Fatalf("process exit %d", c)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the process did not stop")
			}
		}
	}
	conn, err := mq.Dial(amqpURL, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := mq.DeclareTopology(conn, mq.Default); err != nil {
		t.Fatal(err)
	}
	queued := func() int {
		ch, err := conn.Channel()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ch.Close() }()
		q, err := ch.QueueDeclarePassive("notify.email", true, false, false, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		return q.Messages
	}

	ready := func(r readiness) bool { return r.status == http.StatusOK }

	// Scheduler: elected leader, its relay wakes on NOTIFY (the tick is a minute) and publishes; ready
	// once PostgreSQL and Redis answer and the leader holds its broker connection.
	saddr := freeAddr(t)
	t.Setenv("METRICS_ADDR", saddr)
	stopScheduler := run(app.RunScheduler)
	waitReadiness(t, saddr, 15*time.Second, ready)
	time.Sleep(time.Second) // LISTEN up
	pool := d.Pool(t, db.RoleApp)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: "auth.password_reset_requested", AggregateType: "user",
		AggregateID: user.String(), Payload: map[string]any{"userId": user, "purpose": "reset", "locale": "en"}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	asynctest.Eventually(t, 10*time.Second, "the relayed event in notify.email", func() bool { return queued() == 1 })
	stopScheduler()

	// Worker: asserts the topology, consumes notify.email and mails the link; ready with PostgreSQL and
	// its broker session up.
	waddr := freeAddr(t)
	t.Setenv("METRICS_ADDR", waddr)
	stopWorker := run(app.RunWorker)
	waitReadiness(t, waddr, 15*time.Second, ready)
	mails := mp.WaitMessages(t, 1, 20*time.Second)
	if len(mails) != 1 || mails[0].Subject != "Reset your LogiTrack password" {
		t.Fatalf("mails %+v", mails)
	}
	stopWorker()
}
