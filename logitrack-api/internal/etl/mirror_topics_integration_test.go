//go:build integration

// T33 (issue #53): the mirror's events date the write (at = the document's Firestore update time) and reach the
// dispatch topics of every party the row involves after the version and, for an update of the same row, before it, so
// a row whose party changed leaves the boards of the party it no longer involves; that party is named in leftPartyIds
// and its copy is the envelope only. A document that makes a row of its own (a claim of another driver's trip, a Q3
// copy) reaches only its own parties.
package etl

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// project is the copy of e the relay writes on topic (nil when the topic carries none).
func project(t *testing.T, e opsEvent, topic string) map[string]any {
	t.Helper()
	raw, err := json.Marshal(e.payload)
	if err != nil {
		t.Fatal(err)
	}
	out, ok := realtime.Project(topic, e.key, raw)
	if !ok {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// envelopeOnly reports a copy that carries nothing but the envelope and an empty changed[].
func envelopeOnly(m map[string]any) bool {
	for k := range m {
		if !slices.Contains([]string{"entity", "id", "tenantId", "at", "version", "changed"}, k) {
			return false
		}
	}
	ch, ok := m["changed"].([]any)
	return ok && len(ch) == 0 && m["id"] != nil
}

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
	if evs[0].payload[realtime.LeftPartiesKey] != nil {
		t.Errorf("a new trip left a party: %v", evs[0].payload)
	}

	// The trip moves to SPX and is delivered in the same write: the update reaches SPX's dispatchers with its values and
	// still CJSF's, whose board drops the row and reads none of them (the envelope only).
	mark = h.lastEvent()
	h.fs.Merge("trip_records/TRIP300", map[string]any{"billingCustomerId": "cust_spx", "status": "delivered",
		"deliveredTimestamp": ts(at.Add(time.Hour)), "updatedAt": ts(at.Add(time.Hour))})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if !slices.Equal(keys(evs), []string{"trip.delivered"}) || !slices.Contains(payloadStrings(evs[0].payload["changed"]), "billingPartyId") {
		t.Fatalf("the party change: %+v", evs)
	}
	for _, want := range []uuid.UUID{cjsf, spx} {
		if !slices.Contains(evs[0].topics, dispatch(want)) {
			t.Errorf("the party change misses %s: %v", dispatch(want), evs[0].topics)
		}
	}
	if left := payloadStrings(evs[0].payload[realtime.LeftPartiesKey]); !slices.Equal(left, []string{cjsf.String()}) {
		t.Errorf("leftPartyIds %v, want CJSF's", left)
	}
	if m := project(t, evs[0], dispatch(cjsf)); !envelopeOnly(m) {
		t.Errorf("CJSF's dispatchers read %v of a trip they no longer see", m)
	}
	if m := project(t, evs[0], dispatch(spx)); m["status"] != "delivered" || m["tripNo"] != "TRIP300" || m[realtime.LeftPartiesKey] != nil {
		t.Errorf("SPX's dispatchers got %v", m)
	}

	// A later edit reaches only the parties the trip involves now, and leaves none.
	mark = h.lastEvent()
	h.fs.Merge("trip_records/TRIP300", map[string]any{"sealCode": "S-300", "updatedAt": ts(at.Add(2 * time.Hour))})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if len(evs) != 1 || slices.Contains(evs[0].topics, dispatch(cjsf)) || !slices.Contains(evs[0].topics, dispatch(spx)) ||
		evs[0].payload[realtime.LeftPartiesKey] != nil {
		t.Fatalf("an edit after the move: %+v", evs)
	}

	// A task moves from CJSF to SPX with a new driver in one save: the task topics of both parties, CJSF's copy without
	// the new driver or the task's state.
	mark = h.lastEvent()
	h.fs.Merge("tasks/task_2", map[string]any{"billingCustomerId": "cust_spx", "driverId": "drv_own", "updatedAt": ts(at.Add(3 * time.Hour))})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if len(evs) != 1 || evs[0].key != "task.reassigned" || evs[0].payload["driverId"] == nil {
		t.Fatalf("the task's party change: %+v", evs)
	}
	tasks := func(id uuid.UUID) string { return "dispatch:" + id.String() + ":tasks" }
	for _, want := range []uuid.UUID{cjsf, spx} {
		if !slices.Contains(evs[0].topics, tasks(want)) {
			t.Errorf("the task's party change misses %s: %v", tasks(want), evs[0].topics)
		}
	}
	if left := payloadStrings(evs[0].payload[realtime.LeftPartiesKey]); !slices.Equal(left, []string{cjsf.String()}) {
		t.Errorf("the task's leftPartyIds %v, want CJSF's", left)
	}
	if m := project(t, evs[0], tasks(cjsf)); !envelopeOnly(m) {
		t.Errorf("CJSF's dispatchers read %v of a task they no longer see", m)
	}
	if m := project(t, evs[0], tasks(spx)); m["driverId"] != evs[0].payload["driverId"] || m["status"] == nil {
		t.Errorf("SPX's dispatchers got %v", m)
	}
}

// A trip document whose renamedFromTripId names another driver's trip (firestore.rules let any driver create one) and
// a Q3 copy whose original still exists are rows of their own: their events reach their own parties only, never those
// of the trip they name, and name no previous number.
func TestMirrorClaimReachesOnlyItsOwnParties(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	party := func(customer string) uuid.UUID {
		return h.id(`SELECT bp.id FROM billing_parties bp JOIN customers c ON c.id = bp.customer_id WHERE c.legacy_doc_id = $1`, customer)
	}
	cjsf, spx := party("cust_cjsf"), party("cust_spx")
	dispatch := func(id uuid.UUID) string { return "dispatch:" + id.String() + ":trips" }
	at := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)

	h.fs.Set("trip_records/VICTIM1", map[string]any{"status": "in_transit", "jobType": "line_haul", "driverId": "uid_alpha",
		"billingCustomerId": "cust_spx", "createdAt": ts(at), "updatedAt": ts(at)})
	h.sweep(p)
	victim := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = 'VICTIM1'`)

	cases := []struct{ name, doc, driver, from, customer string }{
		{"a claim of another driver's trip", "FORGED1", "uid_own", "VICTIM1", "cust_cjsf"},
		{"a Q3 copy whose original still exists", "TRIP002-COPY", "uid_alpha", "TRIP002", "cust_spx"},
	}
	for i, c := range cases {
		mark := h.lastEvent()
		h.fs.Set("trip_records/"+c.doc, map[string]any{"status": "in_transit", "jobType": "first_mile", "driverId": c.driver,
			"billingCustomerId": c.customer, "renamedFromTripId": c.from, "createdAt": ts(at.Add(time.Duration(i+1) * time.Hour)),
			"updatedAt": ts(at.Add(time.Duration(i+1) * time.Hour))})
		h.sweep(p)
		own := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = $1`, c.doc)
		if own == victim {
			t.Fatalf("%s took the named trip's row", c.name)
		}
		evs := h.eventsAfter(mark)
		if len(evs) != 1 || evs[0].key != "trip.created" || evs[0].aggregate != own.String() {
			t.Fatalf("%s: %+v", c.name, evs)
		}
		mine, theirs := party(c.customer), cjsf
		if mine == cjsf {
			theirs = spx
		}
		if !slices.Contains(evs[0].topics, dispatch(mine)) || slices.Contains(evs[0].topics, dispatch(theirs)) {
			t.Errorf("%s: topics %v, want %s only", c.name, evs[0].topics, dispatch(mine))
		}
		if evs[0].payload["previousTripNo"] != nil || evs[0].payload[realtime.LeftPartiesKey] != nil {
			t.Errorf("%s names the other trip: %v", c.name, evs[0].payload)
		}
	}
}
