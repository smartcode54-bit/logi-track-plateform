package notify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
)

// Every routing key bound to notify.line is one the consumer decodes; anything else is permanent. With
// LINE_NOTIFY_ENABLED false an event is acked without a card; enabled without a client is transient. None of these
// paths touch PostgreSQL or Redis.
func TestLineRouting(t *testing.T) {
	ctx := context.Background()
	q, ok := mq.Default.Queue(notify.QueueLine)
	if !ok {
		t.Fatal("notify.line is not in the topology")
	}
	bound := map[string]bool{}
	for _, b := range q.Bindings {
		bound[b.Key] = true
	}
	handled := []string{notify.RouteTaskCheckedIn, notify.RouteTripDelivered, notify.RouteStandbyCompleted, notify.RouteLineForce}
	if len(bound) != len(handled) {
		t.Fatalf("notify.line binds %d keys, the consumer handles %d", len(bound), len(handled))
	}
	for _, k := range handled {
		if !bound[k] {
			t.Fatalf("%s is not bound to notify.line", k)
		}
	}
	event := []byte(`{"id":"` + uuid.NewString() + `"}`)
	off := &notify.LINE{Log: zerolog.Nop()}
	for _, k := range handled[:3] {
		if err := off.Handle(ctx, &mq.Delivery{Queue: notify.QueueLine, MessageID: "1", RoutingKey: k, Body: event}); err != nil {
			t.Errorf("LINE_NOTIFY_ENABLED=false, %s: %v", k, err)
		}
	}
	on := &notify.LINE{Enabled: true, Log: zerolog.Nop()}
	if err := on.Handle(ctx, &mq.Delivery{Queue: notify.QueueLine, MessageID: "1", RoutingKey: "task.updated", Body: event}); !mq.IsPermanent(err) {
		t.Errorf("unbound key: %v", err)
	}
	if err := on.Handle(ctx, &mq.Delivery{Queue: notify.QueueLine, MessageID: "1", RoutingKey: notify.RouteTripDelivered,
		Body: event}); err == nil || mq.IsPermanent(err) {
		t.Errorf("enabled without a client: %v", err)
	}
	for _, body := range []string{`not json`, `{}`, `{"id":"00000000-0000-0000-0000-000000000000"}`} {
		if err := on.Handle(ctx, &mq.Delivery{Queue: notify.QueueLine, MessageID: "1", RoutingKey: notify.RouteTripDelivered,
			Body: []byte(body)}); !mq.IsPermanent(err) {
			t.Errorf("event %s: %v, want permanent", body, err)
		}
	}
}

// T29 / T49: the FS->PG mirror's events (origin=etl.sync) send no LINE card: the Firestore-side writer (the legacy
// callable, or its shim, which forwards to notify.line as a job) owns it. Any other origin reaches the sender.
func TestMirrorEventsSendNoLineCard(t *testing.T) {
	l := &notify.LINE{Enabled: true, Log: zerolog.Nop()} // no client: a send attempt fails
	body := []byte(`{"id":"` + uuid.NewString() + `"}`)
	for _, k := range []string{notify.RouteTaskCheckedIn, notify.RouteTripDelivered, notify.RouteStandbyCompleted} {
		d := &mq.Delivery{Queue: notify.QueueLine, RoutingKey: k, MessageID: "7", Body: body,
			Headers: amqp.Table{outbox.HeaderOrigin: outbox.OriginMirror}}
		if err := l.Handle(context.Background(), d); err != nil {
			t.Fatalf("a mirror event %s: %v", k, err)
		}
		d.Headers = amqp.Table{outbox.HeaderOrigin: outbox.OriginWriteBack}
		if err := l.Handle(context.Background(), d); err == nil {
			t.Fatalf("a write-back event %s reached no client without an error", k)
		}
	}
}

func TestLineForceParams(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		p    notify.LineForceParams
		want string
	}{
		{notify.LineForceParams{Card: notify.LineDelivered, ID: &id, Force: true}, ""},
		{notify.LineForceParams{Card: notify.LineCheckin, Ref: "fsTask"}, ""},
		{notify.LineForceParams{Card: "pickup", ID: &id}, "unknown card"},
		{notify.LineForceParams{Card: notify.LineStandby, Ref: "  "}, "names no record"},
		{notify.LineForceParams{Card: notify.LineStandby, ID: &id, Force: true, DriverID: &id}, "never forced"},
	} {
		err := tc.p.Validate()
		if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%+v: %v, want %q", tc.p, err, tc.want)
		}
	}
}

// The push body is {"to", "messages": [card]} with the card exactly as JSON.stringify writes it.
func TestLinePushBody(t *testing.T) {
	m := notify.BuildCheckinMessage(notify.LineTripCard{OriginLabel: "<A&B>", TripNo: "T1"})
	body := string(notify.LinePushBody("C123", m))
	if !strings.HasPrefix(body, `{"to":"C123","messages":[{"type":"flex","altText":`) || !strings.HasSuffix(body, "}]}") {
		t.Fatalf("body %s", body)
	}
	if !strings.Contains(body, string(m.JSON())) || !strings.Contains(body, "<A&B>") {
		t.Fatalf("the card is not embedded as written: %s", body)
	}
}
