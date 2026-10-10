package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

func TestAppendValidates(t *testing.T) {
	ok := Event{Exchange: mq.ExchangeEvents, RoutingKey: "trip.delivered", AggregateType: "trip", AggregateID: "1"}
	for name, e := range map[string]Event{
		"retry exchange":       {Exchange: mq.ExchangeRetry, RoutingKey: "trip.delivered", AggregateType: "trip", AggregateID: "1"},
		"job on lt.events":     {RoutingKey: "job.storage.gc", AggregateType: "job", AggregateID: "1"},
		"event on lt.jobs":     {Exchange: mq.ExchangeJobs, RoutingKey: "trip.delivered", AggregateType: "trip", AggregateID: "1"},
		"bad routing key":      {RoutingKey: "Trip Delivered", AggregateType: "trip", AggregateID: "1"},
		"single word key":      {RoutingKey: "delivered", AggregateType: "trip", AggregateID: "1"},
		"no aggregate":         {RoutingKey: "trip.delivered"},
		"unknown topic":        {RoutingKey: "trip.delivered", AggregateType: "trip", AggregateID: "1", Topics: []string{"tenant:all"}},
		"upper-case uuid":      {RoutingKey: "trip.delivered", AggregateType: "trip", AggregateID: "1", Topics: []string{"user:0199C000-0000-7000-8000-000000000001"}},
		"payload not object":   {RoutingKey: "trip.delivered", AggregateType: "trip", AggregateID: "1", Payload: []string{"a"}},
		"payload null literal": {RoutingKey: "trip.delivered", AggregateType: "trip", AggregateID: "1", Payload: []byte("null")},
	} {
		// Validation happens before the insert, so a nil transaction is never reached.
		if _, err := Append(context.Background(), nil, e); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("%s: %v, want ErrInvalidEvent", name, err)
		}
	}
	if err := validate(ok); err != nil {
		t.Fatal(err)
	}
	if err := validate(Event{Exchange: mq.ExchangeEvents, RoutingKey: "job.updated", AggregateType: "job", AggregateID: "1"}); err != nil {
		t.Fatalf("job.updated is a realtime-only event on lt.events: %v", err)
	}
	if b, err := encodePayload(nil); err != nil || string(b) != "{}" {
		t.Fatalf("nil payload = %s %v", b, err)
	}
}

func TestNextBackoff(t *testing.T) {
	var got []time.Duration
	d := time.Duration(0)
	for range 12 {
		d = nextBackoff(d, 200*time.Millisecond)
		got = append(got, d)
	}
	if got[0] != 200*time.Millisecond || got[1] != 400*time.Millisecond || got[7] != 25600*time.Millisecond ||
		got[8] != MaxBackoff || got[11] != MaxBackoff {
		t.Fatalf("backoff sequence %v", got)
	}
	if d := nextBackoff(time.Minute, time.Minute); d != time.Minute {
		t.Fatalf("an interval above MaxBackoff caps at the interval, got %v", d)
	}
}
