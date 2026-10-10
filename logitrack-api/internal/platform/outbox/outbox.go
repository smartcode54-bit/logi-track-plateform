// Package outbox is the transactional outbox of main spec §7.1 (Appendix A §A.2.8, Appendix B §B.5.5).
//
// Producers call Append inside their service transaction: the event row commits or rolls back with
// the domain write, so a rolled-back transaction publishes nothing and a committed one is delivered at
// least once. Nothing else publishes to RabbitMQ: `api` never connects to the broker, and cron fires
// and admin jobs are outbox rows on lt.jobs too, so every message shares one message-id space
// (message_id = outbox_events.id, R57).
//
// The Relay runs in the leader scheduler: LISTEN outbox_new plus a fallback tick, a batch of
// unpublished rows FOR UPDATE SKIP LOCKED, each published with publisher confirms and, when it has
// realtime topics, fanned out through Redis (package realtime), then marked published.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox/outboxdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// Event is one outbox row. Payloads are thin: consumers and clients re-read state (main spec §7.1).
type Event struct {
	Exchange      string     // mq.ExchangeEvents (default) or mq.ExchangeJobs
	RoutingKey    string     // {aggregate}.{event} on lt.events, job.{type} on lt.jobs (Appendix B §B.5.2)
	AggregateType string     // 'trip', 'task', 'user', 'job', 'settings', ...
	AggregateID   string     // text: uuid, settings key, day key, job id (R14)
	EventType     string     // defaults to RoutingKey
	TenantID      *uuid.UUID // nil = platform-level event (R12)
	Payload       any        // a JSON object; nil = {}
	Topics        []string   // realtime topics of Appendix B §B.4.2; empty = not realtime
	Headers       map[string]string
}

// Appended identifies the inserted row.
type Appended struct {
	ID      int64     // relay order and AMQP message_id
	EventID uuid.UUID // envelope id carried by SSE payloads
}

var (
	routingKeyRx = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_-]+)+$`)

	// ErrInvalidEvent wraps every validation failure of Append.
	ErrInvalidEvent = errors.New("outbox: invalid event")
)

// Append inserts e in tx, the caller's service transaction (outbox.Append(tx, …) is the only way to
// emit, main spec §7.1). A producer emitting for an aggregate holds FOR UPDATE on it first, so the
// ids of one aggregate follow commit order. The current trace context travels as headers.traceparent.
func Append(ctx context.Context, tx outboxdb.DBTX, e Event) (Appended, error) {
	if e.Exchange == "" {
		e.Exchange = mq.ExchangeEvents
	}
	if e.EventType == "" {
		e.EventType = e.RoutingKey
	}
	if err := validate(e); err != nil {
		return Appended{}, err
	}
	payload, err := encodePayload(e.Payload)
	if err != nil {
		return Appended{}, err
	}
	headers := map[string]string{}
	for k, v := range e.Headers {
		headers[k] = v
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(headers))
	h, err := json.Marshal(headers)
	if err != nil {
		return Appended{}, fmt.Errorf("outbox: encode headers: %w", err)
	}
	topics := e.Topics
	if topics == nil {
		topics = []string{}
	}
	row, err := outboxdb.New(tx).InsertEvent(ctx, outboxdb.InsertEventParams{
		Exchange: e.Exchange, RoutingKey: e.RoutingKey, AggregateType: e.AggregateType, AggregateID: e.AggregateID,
		EventType: e.EventType, TenantID: e.TenantID, Payload: payload, Headers: h, RealtimeTopics: topics,
	})
	if err != nil {
		return Appended{}, fmt.Errorf("outbox: insert %s: %w", e.RoutingKey, err)
	}
	return Appended{ID: row.ID, EventID: row.EventID}, nil
}

func validate(e Event) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidEvent, fmt.Sprintf(format, args...))
	}
	switch e.Exchange {
	case mq.ExchangeEvents:
		if strings.HasPrefix(e.RoutingKey, "job.") && e.RoutingKey != "job.updated" {
			return bad("job commands go to %s", mq.ExchangeJobs)
		}
	case mq.ExchangeJobs:
		if !strings.HasPrefix(e.RoutingKey, "job.") {
			return bad("routing keys on %s are job.{type}", mq.ExchangeJobs)
		}
	default:
		return bad("the relay publishes only to %s and %s", mq.ExchangeEvents, mq.ExchangeJobs)
	}
	if !routingKeyRx.MatchString(e.RoutingKey) {
		return bad("routing key %q is not {aggregate}.{event}", e.RoutingKey)
	}
	if e.AggregateType == "" || e.AggregateID == "" {
		return bad("aggregate type and id are required")
	}
	for _, t := range e.Topics {
		if !realtime.ValidTopic(t) {
			return bad("realtime topic %q is not in the catalogue of Appendix B §B.4.2", t)
		}
	}
	return nil
}

func encodePayload(p any) ([]byte, error) {
	if p == nil {
		return []byte("{}"), nil
	}
	var b []byte
	switch v := p.(type) {
	case json.RawMessage:
		b = v
	case []byte:
		b = v
	default:
		var err error
		if b, err = json.Marshal(p); err != nil {
			return nil, fmt.Errorf("outbox: encode payload: %w", err)
		}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("%w: the payload must be a JSON object", ErrInvalidEvent)
	}
	return b, nil
}
