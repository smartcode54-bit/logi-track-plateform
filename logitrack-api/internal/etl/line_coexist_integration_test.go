//go:build integration

package etl

import (
	"testing"
	"time"
)

// T49: once comms is PostgreSQL-owned (P5), notify.line owns the LINE cards: the flags it sets
// (line_checkin_notified_at, line_delivered_notified_at, line_notified_at) and the evidence tokens it mints live in
// PostgreSQL only, and a later Firestore version of the record (the 3.x app writing the trip, which carries none of
// them) leaves them as they are. Otherwise the next request would send the card again and the gallery link a group
// already holds would answer 404. Before P5 the document's values apply as before (the legacy callable sends and mints).
func TestLineFieldsStayGoOnceCommsOwned(t *testing.T) {
	h := newSyncHarness(t)
	p5 := owned("auth", "masterdata", "comms")
	at := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)

	trip := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP001'`)
	task := h.id(`SELECT id FROM tasks WHERE legacy_doc_id = 'task_1'`)
	sb := h.id(`SELECT id FROM standby_records WHERE legacy_doc_id = 'sb3'`)
	h.exec(`UPDATE trip_records SET line_delivered_notified_at = $2, evidence_token = 'goTripToken' WHERE id = $1`, trip, at)
	h.exec(`UPDATE tasks SET line_checkin_notified_at = $2 WHERE id = $1`, task, at)
	h.exec(`UPDATE standby_records SET line_notified_at = $2, evidence_token = 'goStandbyToken' WHERE id = $1`, sb, at)

	h.fs.Merge("trip_records/TRIP001", map[string]any{"parcelCount": int64(7)})
	h.fs.Merge("tasks/task_1", map[string]any{"cancelReason": "kept"})
	h.fs.Merge("standby_records/sb3", map[string]any{"note": "changed in Firestore"})
	h.sweep(h.plan(p5))
	if v := h.scalar(`SELECT parcel_count FROM trip_records WHERE id = $1`, trip); v != int32(7) {
		t.Fatalf("the trip document was not applied: parcel_count %v", v)
	}
	for _, c := range []struct {
		sql  string
		id   any
		want string
	}{
		{`SELECT coalesce(evidence_token, '') || '|' || (line_delivered_notified_at IS NOT NULL)::text FROM trip_records WHERE id = $1`,
			trip, "goTripToken|true"},
		{`SELECT (line_checkin_notified_at IS NOT NULL)::text FROM tasks WHERE id = $1`, task, "true"},
		{`SELECT coalesce(evidence_token, '') || '|' || (line_notified_at IS NOT NULL)::text || '|' || note FROM standby_records WHERE id = $1`,
			sb, "goStandbyToken|true|changed in Firestore"},
	} {
		if v := h.scalar(c.sql, c.id); v != c.want {
			t.Errorf("P5 mirror: %s = %v, want %s", c.sql, v, c.want)
		}
	}

	// Before P5 Firestore's values apply: the legacy callable's token and flag (here: a token, no flag).
	h.fs.Merge("trip_records/TRIP001", map[string]any{"evidenceToken": "legacyToken16chr", "parcelCount": int64(8)})
	h.sweep(h.plan(owned("auth", "masterdata")))
	if v := h.scalar(`SELECT coalesce(evidence_token, '') || '|' || (line_delivered_notified_at IS NOT NULL)::text
		FROM trip_records WHERE id = $1`, trip); v != "legacyToken16chr|false" {
		t.Fatalf("P4 mirror: %v, want the document's token and flag", v)
	}
}
