package operations

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

func shapeDirectory() (directory, uuid.UUID, uuid.UUID, uuid.UUID) {
	hub, socHub, party := uuid.New(), uuid.New(), uuid.New()
	d := directory{byID: map[uuid.UUID]Hub{hub: {ID: hub, Code: "ALANG-A", LinkedPartyID: &party, StationType: "HUB"},
		socHub: {ID: socHub, Code: "BKK-SOC1", StationType: "SOC"}},
		byCode: map[string][]uuid.UUID{"ALANG-A": {hub}, "BKK-SOC1": {socHub}}}
	return d, hub, socHub, party
}

// stops[] of PATCH /v1/trips/{id} (T34): the whole new list, each stop's destination required and unique (the task's
// mirror keeps UNIQUE (task_id, destination_raw)), statuses of the 0004 vocabulary, violations named by position.
func TestParseTripStops(t *testing.T) {
	parse := func(body string) ([]tripStopIn, bool, string) {
		var ck checker
		f, err := parseFields([]byte(body), &ck)
		if err != nil {
			t.Fatal(err)
		}
		stops, set := parseTripStops(f)
		return stops, set, violations(ck.err())
	}
	if _, set, v := parse(`{}`); set || v != "" {
		t.Fatalf("absent: %v %q", set, v)
	}
	if stops, set, v := parse(`{"stops": []}`); !set || len(stops) != 0 || v != "" {
		t.Fatalf("empty: %v %v %q", stops, set, v)
	}
	stops, set, v := parse(`{"stops": [{"destination": " soce "}, {"destination": "ALANG-A", "status": "Delivered",
		"deliveredAt": "2026-10-11T08:00:00Z", "lat": 13.7, "lng": null}]}`)
	if !set || v != "" || len(stops) != 2 || stops[0].destination != "SOCE" || stops[0].status != nil ||
		*stops[1].status != "delivered" || stops[1].deliveredAt == nil || !stops[1].latSet || !stops[1].lngSet || stops[1].lng != nil {
		t.Fatalf("parsed %+v %q", stops, v)
	}
	for body, want := range map[string]string{
		`{"stops": null}`:                                          "stops invalid",
		`{"stops": {"destination": "A"}}`:                          "stops invalid",
		`{"stops": [{"destination": ""}]}`:                         "stops.destination required",
		`{"stops": [{"destination": "A", "status": "lost"}]}`:      "stops.status invalid",
		`{"stops": [{"destination": "A"}, {"destination": "a "}]}`: "stops.destination duplicate",
		`{"stops": [{"destination": "A", "photos": []}]}`:          "stops.photos not_supported",
		`{"stops": ["A"]}`:                                         "stops.destination invalid",
	} {
		if _, _, got := parse(body); got != want {
			t.Errorf("%s: %q, want %q", body, got, want)
		}
	}
	many := `{"stops": [`
	for i := 0; i <= MaxTripStops; i++ {
		if i > 0 {
			many += ","
		}
		many += `{"destination": "D` + string(rune('A'+i%26)) + string(rune('A'+i/26)) + `"}`
	}
	if _, _, got := parse(many + `]}`); got != "stops too_many" {
		t.Fatalf("too many: %q", got)
	}
}

// mergeStops: a stop keeps its own progress unless the body names it, a pending stop has none, a delivered stop keeps
// its completion order and a newly delivered one comes last; sameStops sees an unchanged list.
func TestMergeStops(t *testing.T) {
	d, hub, _, _ := shapeDirectory()
	at := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	lat := 13.7
	cur := []stopState{
		{index: 1, destination: "ALANG-A", hubID: &hub, status: "delivered", deliveredAt: &at, lat: &lat, completedSeq: ptr(int32(2))},
		{index: 2, destination: "SOCE", status: "pending"},
		{index: 3, destination: "X", status: "delivered", deliveredAt: &at, completedSeq: ptr(int32(1))},
	}
	in := []tripStopIn{{destination: "ALANG-A"}, {destination: "SOCN", status: ptr("delivered")}}
	next := d.mergeStops(cur, in)
	if len(next) != 2 || next[0].status != "delivered" || !sameTime(next[0].deliveredAt, &at) || *next[0].completedSeq != 2 ||
		next[0].hubID == nil || *next[0].hubID != hub {
		t.Fatalf("kept stop %+v", next[0])
	}
	if next[1].destination != "SOCN" || next[1].status != "delivered" || next[1].deliveredAt != nil || *next[1].completedSeq != 3 {
		t.Fatalf("delivered stop %+v", next[1])
	}
	if sameStops(cur, next) {
		t.Fatal("a shorter list is a change")
	}
	back := d.mergeStops(cur, []tripStopIn{{destination: "ALANG-A"}, {destination: "SOCE"}, {destination: "X"}})
	if !sameStops(cur, back) {
		t.Fatalf("the same list is no change: %+v", back)
	}
	reset := d.mergeStops(cur, []tripStopIn{{destination: "ALANG-A", status: ptr("pending")}})
	if reset[0].deliveredAt != nil || reset[0].lat != nil || reset[0].completedSeq != nil {
		t.Fatalf("a pending stop has no progress: %+v", reset[0])
	}
}

// An origin implies the job type (EditTripDetailsDialog.tsx:365-370): a SOC key or a SOC station is line_haul,
// another hub first_mile, an unresolved place nothing.
func TestJobTypeOf(t *testing.T) {
	d, _, _, _ := shapeDirectory()
	for raw, want := range map[string]string{"ALANG-A": FirstMile, "SOCE": LineHaul, "socw (สมุทรสาคร)": LineHaul, "BKK-SOC1": LineHaul,
		"bkk-soc1 - กรุงเทพ": LineHaul, "nowhere": ""} {
		got, ok := d.jobTypeOf(ptr(raw))
		if got != want || ok != (want != "") {
			t.Errorf("%q: %q %v, want %q", raw, got, ok, want)
		}
	}
	if _, ok := d.jobTypeOf(nil); ok {
		t.Fatal("no origin implies nothing")
	}
}

// The Firestore side: deliveryStopsProgress[] reads with its completion order, and each written stop keeps the keys of
// the document's own entry (its photos), a re-pointed one loses its sourceId; the task's stops keep their billing link.
func TestStopDocs(t *testing.T) {
	d, hub, _, party := shapeDirectory()
	ts := dump.Timestamp{Time: time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)}
	doc := []any{
		map[string]any{"index": int64(2), "destination": "SOCE", "status": "delivered", "deliveredAt": ts,
			"photos": []any{map[string]any{"type": "stop_2_arrived", "url": "u"}}, "sourceId": "S"},
		map[string]any{"index": int64(1), "destination": "ALANG-A", "status": "Delivered"},
		map[string]any{"destination": "no index"},
	}
	cur := docStops(doc)
	if len(cur) != 2 || *cur[0].completedSeq != 1 || *cur[1].completedSeq != 2 || cur[1].status != "delivered" ||
		!cur[0].deliveredAt.Equal(ts.Time) {
		t.Fatalf("doc stops %+v", cur)
	}
	next := d.mergeStops(cur, []tripStopIn{{destination: "ALANG-A"}, {destination: "SOCN"}})
	out := tripStopDocs(next)
	s2 := out[1].(map[string]any)
	if photos, _ := s2["photos"].([]any); len(photos) != 1 || s2["destination"] != "SOCN" || s2["status"] != "delivered" ||
		s2["index"] != int64(2) || s2["sourceId"] != nil {
		t.Fatalf("re-pointed stop %v", s2)
	}
	if photos, _ := out[0].(map[string]any)["photos"].([]any); photos == nil {
		t.Fatalf("a stop always has photos[] %v", out[0])
	}
	_ = hub
	_ = party
	taskDoc := []any{map[string]any{"index": int64(1), "destination": "alang-a", "destinationLinkedCustomerId": "cust_doc"}}
	stops, err := taskStopDocs(d, taskDoc, next, nil, ts.Time)
	if err != nil {
		t.Fatal(err)
	}
	if m := stops[0].(map[string]any); m["destinationLinkedCustomerId"] != "cust_doc" || m["destination"] != "ALANG-A" {
		t.Fatalf("kept task stop %v", m)
	}
	if m := stops[1].(map[string]any); m["sequence"] != int64(1) || m["addedAt"] != ts.Time || m["destinationLinkedCustomerId"] != nil {
		t.Fatalf("new task stop %v", m)
	}
}
