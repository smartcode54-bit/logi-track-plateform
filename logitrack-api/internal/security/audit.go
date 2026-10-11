package security

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/inbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
)

// RouteEvent is the outbox routing key of a consumer-written security event (Appendix B §B.5.7): the payload is the
// camelCase JSON of Event.
const RouteEvent = "security.event"

// QueueAudit is the security.audit queue of the worker (group platform, prefetch 8, Appendix B §B.5.3): bound to
// security.event only (user.logged_in and the other user.* / auth.* keys never become rows, R22).
const QueueAudit = "security.audit"

// auditTimeout bounds one append.
const auditTimeout = 30 * time.Second

// ConsumerTypes are the event types written through the consumer rather than in the transaction of a change
// (Appendix C §C.4.13, Appendix B §B.5.7): failed logins and lockouts (no change to attach them to), reset requests,
// the orphan scan's alert, evidence views and webhook signature failures (both sampled).
var ConsumerTypes = []string{
	"login_failed", "login_lockout", "password_reset_requested", "tenant_orphans_detected",
	"evidence_viewed", "webhook_signature_failed",
}

// Emit queues e as outbox security.event in tx, for the security.audit consumer to append (the consumer-written path
// of Appendix C §C.4.13; an in-transaction event calls Append instead). aggregateType / aggregateID name what the event
// is about for the outbox row (e.g. "job" and the job id of a scan).
func Emit(ctx context.Context, tx pgx.Tx, e Event, aggregateType, aggregateID string) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if !slices.Contains(ConsumerTypes, e.EventType) {
		return fmt.Errorf("security: %s is written in the transaction of its change, not through the consumer", e.EventType)
	}
	var headers map[string]string
	if e.RequestID != "" {
		headers = map[string]string{"requestId": e.RequestID}
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: RouteEvent, AggregateType: aggregateType, AggregateID: aggregateID, TenantID: e.TenantID,
		Payload: e, Headers: headers,
	})
	return err
}

// Audit is the security.audit consumer (main spec §7.7, Appendix B §B.5.3): it appends each outbox security.event as
// one security_events row, exactly once per message (consumer_inbox, in the system transaction of inbox.Run with the
// event's tenant). A malformed payload, an invalid event or a type that is not consumer-written is acknowledged as a
// permanent failure (retrying the same bytes cannot succeed); a database failure is retried by the ladder.
type Audit struct {
	Pool db.Beginner
	Log  zerolog.Logger
}

// Registration binds the consumer to its queue.
func (a *Audit) Registration() mq.Registration {
	return mq.Registration{Queue: QueueAudit, Handler: a.Handle, Timeout: auditTimeout}
}

// Handle appends the event of one delivery.
func (a *Audit) Handle(ctx context.Context, d *mq.Delivery) error {
	if d.RoutingKey != RouteEvent {
		return mq.Permanent(fmt.Errorf("security.audit: unexpected routing key %s", d.RoutingKey))
	}
	var e Event
	if err := d.Decode(&e); err != nil {
		return err
	}
	if err := e.Validate(); err != nil {
		return mq.Permanent(err)
	}
	if !slices.Contains(ConsumerTypes, e.EventType) {
		return mq.Permanent(fmt.Errorf("security.audit: %s is not a consumer-written event", e.EventType))
	}
	if a.Pool == nil {
		return errors.New("security.audit: no pool")
	}
	processed, err := inbox.Run(ctx, a.Pool, d, func(tx pgx.Tx) error { return Append(ctx, tx, e) })
	if err != nil {
		return err
	}
	if processed {
		a.Log.Debug().Str("event_type", e.EventType).Str("severity", e.Severity).Msg("security event appended")
	}
	return nil
}
