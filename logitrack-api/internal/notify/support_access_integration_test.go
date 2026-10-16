//go:build integration

package notify_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/email"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

type supportMailSink struct {
	mu       sync.Mutex
	messages []email.Message
}

func (s *supportMailSink) Send(_ context.Context, m email.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	return nil
}
func (s *supportMailSink) snapshot() []email.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]email.Message(nil), s.messages...)
}

func TestSupportAdminActionOutboxRoutesToEmailAndDeduplicatesRecipients(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	tenant, actor, target, one, two := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	req := notify.SupportAdminAction{TenantID: tenant, ActorUserID: actor, TargetUserID: target, Action: "temporary_password", Basis: "consent", Locale: "en", RecipientUserIDs: []uuid.UUID{two, one, one}}
	err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tenants(id,kind,name_th,legal_type)VALUES($1,'carrier','Recognizable Company','company')`, tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,email,status,disabled_at)VALUES($1,'admin-one@example.test','active',NULL),($2,'admin-two@example.test','disabled',clock_timestamp())`, one, two); err != nil {
			return err
		}
		_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: notify.RouteSupportAdminAction, AggregateType: "tenant", AggregateID: tenant.String(), TenantID: &tenant, Payload: req})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	broker, _ := asynctest.SharedRabbit(t).VHost(t)
	conn, err := mq.Dial(broker, "support-email-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := mq.DeclareTopology(conn, mq.Default); err != nil {
		t.Fatal(err)
	}
	sink := &supportMailSink{}
	consumer := &notify.Email{Pool: pool, Sender: sink, Enabled: true}
	completed := make(chan mq.Delivery, 4)
	failures := make(chan error, 4)
	reg := consumer.Registration()
	reg.Handler = func(ctx context.Context, d *mq.Delivery) error {
		err := consumer.Handle(ctx, d)
		if err != nil {
			failures <- err
		} else {
			completed <- *d
		}
		return err
	}
	runctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mq.Consume(runctx, conn, reg, mq.ConsumerOptions{Topology: mq.Default, Log: zerolog.Nop()})
	}()
	defer func() { cancel(); <-done }()
	relay := outbox.NewRelay(pool, func() (outbox.Publisher, error) { return mq.NewPublisher(conn) }, nil, outbox.RelayOptions{Log: zerolog.Nop()})
	if n, err := relay.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("relay count=%d err=%v", n, err)
	}
	var delivered mq.Delivery
	select {
	case delivered = <-completed:
	case err := <-failures:
		t.Fatal(err)
	case <-time.After(15 * time.Second):
		t.Fatal("D2 outbox message did not reach notify.email through broker topology")
	}
	if err := consumer.Handle(ctx, &delivered); err != nil {
		t.Fatal(err)
	}
	mails := sink.snapshot()
	if len(mails) != 2 {
		t.Fatalf("sent %d mails, want one per pre-operation recipient", len(mails))
	}
	seen := map[string]bool{}
	for _, m := range mails {
		if seen[m.To] {
			t.Fatal("recipient duplicated")
		}
		seen[m.To] = true
		if !strings.Contains(m.Subject, "Recognizable Company") || strings.Contains(m.Text, "temporary_password") {
			t.Fatalf("unlocalized notification: %+v", m)
		}
	}
	if !seen["admin-one@example.test"] || !seen["admin-two@example.test"] {
		t.Fatal("disabled pre-operation admin lost its notification")
	}
}
