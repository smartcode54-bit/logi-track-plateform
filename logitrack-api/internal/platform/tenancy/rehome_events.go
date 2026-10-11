package tenancy

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// RehomeEvents are the domain events a re-home owes (Appendix B §B.5.2), in the payloads and topics of the services
// that own the tables: a moved driver, truck or company changes tenant, which its projected Firestore document carries
// (subcontractorId; a row of the quarantine tenant is not projected at all), so firestore.shadow projects it again and
// the fleet pages refresh. The other tables a re-home moves have no projected document (a truck assignment's
// document names no tenant, and its truck and driver follow their own events).
func RehomeEvents(moved []Moved, to uuid.UUID) []outbox.Event {
	fleet := []string{realtime.TenantTopic(to.String(), realtime.FamilyFleet)}
	var out []outbox.Event
	for _, m := range moved {
		id := m.ID.String()
		switch m.Table {
		case "drivers":
			out = append(out, outbox.Event{RoutingKey: "driver.updated", AggregateType: "driver", AggregateID: id, TenantID: &to,
				Payload: map[string]any{"entity": "driver", "id": id, "changed": []string{"tenantId"}}, Topics: fleet})
		case "trucks":
			out = append(out, outbox.Event{RoutingKey: "truck.updated", AggregateType: "truck", AggregateID: id, TenantID: &to,
				Payload: map[string]any{"entity": "truck", "id": id, "changed": []string{"tenantId"}}, Topics: fleet})
		case "companies":
			out = append(out, outbox.Event{RoutingKey: "companies.changed", AggregateType: "company", AggregateID: id, TenantID: &to,
				Payload: map[string]any{"ids": []string{id}}, Topics: []string{"global"}})
		}
	}
	return out
}

// AppendRehomeEvents appends RehomeEvents in the re-home's transaction.
func AppendRehomeEvents(ctx context.Context, tx pgx.Tx, moved []Moved, to uuid.UUID) error {
	for _, e := range RehomeEvents(moved, to) {
		if _, err := outbox.Append(ctx, tx, e); err != nil {
			return fmt.Errorf("tenancy: %s %s: %w", e.RoutingKey, e.AggregateID, err)
		}
	}
	return nil
}
