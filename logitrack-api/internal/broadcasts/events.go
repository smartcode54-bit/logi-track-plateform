package broadcasts

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/broadcasts/broadcastsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// Outbox routing keys of the broadcasts (Appendix B §B.4.3, §B.5.3):
//
//   - broadcast.created (POST /v1/broadcasts): notify.fcm sends "broadcast" to the devices of every recipient (read
//     from broadcast_recipients, never from the message), firestore.shadow projects the Firestore document, and the
//     relay writes the thin payload {entity, id, tenantId, at, title} on user:{uid} of each recipient (the 4.x app
//     refetches GET /v1/mobile/broadcasts/latest);
//   - broadcast.voided (DELETE /v1/broadcasts/{id}): firestore.shadow deletes the Firestore document, so the 3.x
//     APKs' list loses it as the legacy delete did. No realtime topic: the staff history refetches after its own
//     mutation, and a driver's list leaves voided rows out on its next fetch.
const (
	RouteCreated = "broadcast.created"
	RouteVoided  = "broadcast.voided"
)

// AggregateBroadcast is the outbox aggregate of the broadcast events: the void of a broadcast locks it, so its events
// follow commit order.
const AggregateBroadcast = "broadcast"

// EntityBroadcast is the entity of the payloads.
const EntityBroadcast = "broadcast"

func headers(rid string) map[string]string {
	if rid == "" {
		return nil
	}
	return map[string]string{"requestId": rid}
}

// payload is the thin payload of both events: consumers re-read the row (main spec §7.1).
func payload(b broadcastsdb.Broadcast, at any) map[string]any {
	return map[string]any{"entity": EntityBroadcast, "id": b.ID, "tenantId": b.TenantID, "at": at, "title": b.Title}
}

// createdEvent appends broadcast.created with the user topic of each recipient.
func createdEvent(ctx context.Context, tx pgx.Tx, rid string, b broadcastsdb.Broadcast, recipients []uuid.UUID) error {
	topics := make([]string, 0, len(recipients))
	for _, u := range recipients {
		topics = append(topics, realtime.UserTopic(u.String()))
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteCreated, AggregateType: AggregateBroadcast,
		AggregateID: b.ID.String(), TenantID: b.TenantID, Payload: payload(b, b.SentAt.UTC()), Headers: headers(rid),
		Topics: topics})
	return err
}

// voidedEvent appends broadcast.voided (no realtime topic).
func voidedEvent(ctx context.Context, tx pgx.Tx, rid string, b broadcastsdb.Broadcast) error {
	var at any
	if b.VoidedAt != nil {
		at = b.VoidedAt.UTC()
	}
	p := payload(b, at)
	p["legacyDocId"] = b.LegacyDocID
	_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteVoided, AggregateType: AggregateBroadcast,
		AggregateID: b.ID.String(), TenantID: b.TenantID, Payload: p, Headers: headers(rid)})
	return err
}
