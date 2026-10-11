//go:build integration

// T33 (issue #53): the mirror's events date the write (at = the document's Firestore update time) and reach the
// dispatch topics of every party the row involves after the version and before it, so a row whose party changed
// leaves the boards of the party it no longer involves.
package etl

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMirrorEventsReachThePartiesBeforeAndAfter(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	party := func(customer string) uuid.UUID {
		return h.id(`SELECT bp.id FROM billing_parties bp JOIN customers c ON c.id = bp.customer_id WHERE c.legacy_doc_id = $1`, customer)
	}
	cjsf, spx := party("cust_cjsf"), party("cust_spx")
	dispatch := func(id uuid.UUID) string { return "dispatch:" + id.String() + ":trips" }
	at := time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)

	// A trip billed to CJSF: its event reaches CJSF's dispatchers and is dated by the write.
	mark := h.lastEvent()
	doc := h.fs.Set("trip_records/TRIP300", map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
		"driverId": "uid_own", "billingCustomerId": "cust_cjsf", "createdAt": ts(at), "updatedAt": ts(at)})
	h.sweep(p)
	evs := h.eventsAfter(mark)
	if !slices.Equal(keys(evs), []string{"trip.created"}) || !slices.Contains(evs[0].topics, dispatch(cjsf)) {
		t.Fatalf("a trip billed to CJSF: %+v", evs)
	}
	if got, _ := time.Parse(time.RFC3339Nano, evs[0].payload["at"].(string)); !got.Equal(doc.UpdateTime) {
		t.Errorf("at %v, want the document's update time %v", evs[0].payload["at"], doc.UpdateTime)
	}

	// The trip moves to SPX: the update reaches SPX's dispatchers and still CJSF's, whose board drops the row.
	mark = h.lastEvent()
	h.fs.Merge("trip_records/TRIP300", map[string]any{"billingCustomerId": "cust_spx", "updatedAt": ts(at.Add(time.Hour))})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if !slices.Equal(keys(evs), []string{"trip.updated"}) || !slices.Contains(payloadStrings(evs[0].payload["changed"]), "billingPartyId") {
		t.Fatalf("the party change: %+v", evs)
	}
	for _, want := range []uuid.UUID{cjsf, spx} {
		if !slices.Contains(evs[0].topics, dispatch(want)) {
			t.Errorf("the party change misses %s: %v", dispatch(want), evs[0].topics)
		}
	}

	// A later edit reaches only the parties the trip involves now.
	mark = h.lastEvent()
	h.fs.Merge("trip_records/TRIP300", map[string]any{"sealCode": "S-300", "updatedAt": ts(at.Add(2 * time.Hour))})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if len(evs) != 1 || slices.Contains(evs[0].topics, dispatch(cjsf)) || !slices.Contains(evs[0].topics, dispatch(spx)) {
		t.Fatalf("an edit after the move: %+v", evs)
	}

	// A task whose billing customer changes: the task topics of both parties.
	mark = h.lastEvent()
	h.fs.Merge("tasks/task_2", map[string]any{"billingCustomerId": "cust_spx", "updatedAt": ts(at.Add(3 * time.Hour))})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if len(evs) != 1 || evs[0].key != "task.updated" {
		t.Fatalf("the task's party change: %+v", evs)
	}
	for _, want := range []uuid.UUID{cjsf, spx} {
		if topic := "dispatch:" + want.String() + ":tasks"; !slices.Contains(evs[0].topics, topic) {
			t.Errorf("the task's party change misses %s: %v", topic, evs[0].topics)
		}
	}
}
