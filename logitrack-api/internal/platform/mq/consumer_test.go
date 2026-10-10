package mq

import (
	"errors"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestRetryLadder(t *testing.T) {
	want := []string{"10s", "1m", "5m", "30m", "2h"}
	for failed := 1; failed <= MaxAttempts; failed++ {
		r, ok := Default.RetryDelay(failed)
		if !ok || r.Label != want[failed-1] {
			t.Fatalf("failure %d -> %v %v", failed, r, ok)
		}
	}
	if _, ok := Default.RetryDelay(MaxAttempts + 1); ok {
		t.Fatal("the sixth failure must dead-letter")
	}
	if _, ok := Default.RetryDelay(0); ok {
		t.Fatal("no rung before a failure")
	}
	if q, ok := Default.Queue("notify.email"); !ok || q.Group != "notify" || q.Prefetch != 4 {
		t.Fatalf("notify.email = %+v", q)
	}
}

func TestPermanent(t *testing.T) {
	base := errors.New("no_rate")
	err := Permanent(base)
	if !IsPermanent(err) || !errors.Is(err, base) || IsPermanent(base) || Permanent(nil) != nil {
		t.Fatal("Permanent does not wrap")
	}
	d := &Delivery{RoutingKey: "trip.delivered", Body: []byte("{")}
	var v map[string]any
	if err := d.Decode(&v); !IsPermanent(err) {
		t.Fatalf("an undecodable body is %v, want permanent", err)
	}
}

func TestToDeliveryReadsOriginalKeyAndHeaders(t *testing.T) {
	at := time.Date(2026, 10, 10, 1, 2, 3, 0, time.UTC)
	d := toDelivery("billing.compute", amqp.Delivery{
		MessageId: "9", Exchange: ExchangeRequeue, RoutingKey: RetryDelays[1].Label + ".billing.compute",
		Headers: amqp.Table{
			HeaderAttempts: int64(2), HeaderOriginalExchange: ExchangeEvents, HeaderOriginalRoutingKey: "trip.delivered",
			HeaderTenantID: "0199c000-0000-7000-8000-000000000001", HeaderOccurredAt: at.Format(time.RFC3339Nano),
			HeaderEventType: "trip.delivered",
		},
	})
	if d.Exchange != ExchangeEvents || d.RoutingKey != "trip.delivered" || d.Attempts != 2 || d.TenantID == nil ||
		!d.OccurredAt.Equal(at) || d.EventType != "trip.delivered" || d.Queue != "billing.compute" {
		t.Fatalf("%+v", d)
	}
	for _, v := range []any{int8(3), int16(3), int32(3), int64(3), uint8(3), "3"} {
		if intHeader(v) != 3 {
			t.Fatalf("%T", v)
		}
	}
}
