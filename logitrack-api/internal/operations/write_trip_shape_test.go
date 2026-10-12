package operations

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
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

// Rows the ETL loaded number every array position (completed_seq on pending and failed stops too, Q16): rowStops keeps
// the completion order of a delivered stop only, so the same list sent again is no change (no write, no events, the
// task's stops not rewritten).
func TestRowStopsLoaderOrder(t *testing.T) {
	d, hub, _, _ := shapeDirectory()
	at := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	rows := []TripStop{
		{StopIndex: 1, DestinationRaw: "ALANG-A", DestinationHubID: &hub, Status: "delivered", DeliveredAt: &at, CompletedSeq: ptr(int32(1))},
		{StopIndex: 2, DestinationRaw: "SOCE", Status: "pending", CompletedSeq: ptr(int32(2))},
		{StopIndex: 3, DestinationRaw: "X", Status: "failed", DeliveredAt: &at, CompletedSeq: ptr(int32(3))},
	}
	cur := rowStops(rows)
	if *cur[0].completedSeq != 1 || cur[1].completedSeq != nil || cur[2].completedSeq != nil {
		t.Fatalf("row stops %+v", cur)
	}
	for _, in := range [][]tripStopIn{
		{{destination: "ALANG-A"}, {destination: "SOCE"}, {destination: "X"}},
		{{destination: "ALANG-A", status: ptr("delivered")}, {destination: "SOCE", status: ptr("pending")}, {destination: "X", status: ptr("failed")}},
	} {
		if next := d.mergeStops(cur, in); !sameStops(cur, next) {
			t.Fatalf("an unchanged loaded list is a change: %+v", next)
		}
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

// The Firestore side: deliveryStopsProgress[] reads in plan order with its completion order, and each written stop keeps
// the keys of the document's own entry (its photos) at its position, a re-pointed one loses its sourceId; the task's
// stops keep their billing link.
func TestStopDocs(t *testing.T) {
	d, hub, _, party := shapeDirectory()
	ts := dump.Timestamp{Time: time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)}
	doc := []any{
		map[string]any{"index": int64(2), "destination": "SOCE", "status": "delivered", "deliveredAt": ts,
			"photos": []any{map[string]any{"type": "stop_2_arrived", "url": "u"}}, "sourceId": "S"},
		map[string]any{"index": int64(1), "destination": "ALANG-A", "status": "Delivered"},
		map[string]any{"destination": "no index"},
	}
	cur := docStops(doc) // plan order; the completion order is the array position
	if len(cur) != 2 || cur[0].index != 1 || *cur[0].completedSeq != 2 || cur[0].status != "delivered" || cur[1].index != 2 ||
		*cur[1].completedSeq != 1 || !cur[1].deliveredAt.Equal(ts.Time) {
		t.Fatalf("doc stops %+v", cur)
	}
	next := d.mergeStops(cur, []tripStopIn{{destination: "ALANG-A"}, {destination: "SOCN"}})
	out := tripStopDocs(doc, cur, next)
	// The array keeps its order (index 2 delivered first) and the entry without an index, at its position.
	if len(out) != 3 || out[0].(map[string]any)["index"] != int64(2) || out[1].(map[string]any)["index"] != int64(1) ||
		out[2].(map[string]any)["destination"] != "no index" {
		t.Fatalf("written order %v", out)
	}
	s2 := out[0].(map[string]any)
	if photos, _ := s2["photos"].([]any); len(photos) != 1 || s2["destination"] != "SOCN" || s2["status"] != "delivered" ||
		s2["index"] != int64(2) || s2["sourceId"] != nil {
		t.Fatalf("re-pointed stop %v", s2)
	}
	if photos, _ := out[1].(map[string]any)["photos"].([]any); photos == nil {
		t.Fatalf("a stop always has photos[] %v", out[1])
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

// loaderOrder is the completion order the ETL loader reads from a written deliveryStopsProgress[] (completed_seq = the
// array position, Q16): the indexes of the delivered entries with a usable index, in array order.
func loaderOrder(arr []any) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, it := range arr {
		m, _ := it.(map[string]any)
		idx, ok := m["index"].(int64)
		if !ok || idx < 1 || seen[idx] {
			continue
		}
		seen[idx] = true
		if m["status"] == "delivered" {
			out = append(out, idx)
		}
	}
	return out
}

// mergedOrder is the completion order mergeStops gives: the delivered stops by completedSeq.
func mergedOrder(stops []stopState) []int64 {
	var del []stopState
	for _, s := range stops {
		if s.completedSeq != nil {
			del = append(del, s)
		}
	}
	slices.SortFunc(del, func(a, b stopState) int { return int(*a.completedSeq) - int(*b.completedSeq) })
	out := make([]int64, len(del))
	for i, s := range del {
		out[i] = int64(s.index)
	}
	return out
}

func indexesOf(arr []any) []any {
	out := make([]any, len(arr))
	for i, it := range arr {
		out[i] = it.(map[string]any)["index"]
	}
	return out
}

// The write-back keeps deliveryStopsProgress[] in completion order (Appendix A: completed_seq = array position, Q16;
// the legacy pricing's base stop is the first delivered entry), as the PostgreSQL path keeps completed_seq: a stop
// edit of a trip whose driver delivered stop 2 before stop 1 writes stop 2 first, a stop this edit delivers comes after
// every delivered one, an entry the loader cannot read (no index) survives with its photos, and the loader reads back
// the order mergeStops computed. The same list in plan order is no change.
func TestStopDocsCompletionOrder(t *testing.T) {
	d, _, _, _ := shapeDirectory()
	ts := dump.Timestamp{Time: time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)}
	noIdx := map[string]any{"destination": "NOIDX", "status": "delivered", "photos": []any{map[string]any{"type": "x", "url": "u3"}}}
	doc := []any{
		map[string]any{"index": int64(2), "destination": "SOCN", "status": "delivered", "deliveredAt": ts},
		map[string]any{"index": int64(1), "destination": "X", "status": "delivered", "deliveredAt": ts},
		noIdx,
	}
	cur := docStops(doc)
	if len(cur) != 2 || cur[0].index != 1 || *cur[0].completedSeq != 2 || cur[1].index != 2 || *cur[1].completedSeq != 1 {
		t.Fatalf("doc stops (plan order, array completion order) %+v", cur)
	}
	if next := d.mergeStops(cur, []tripStopIn{{destination: "X"}, {destination: "SOCN"}}); !sameStops(cur, next) {
		t.Fatalf("the same list in plan order is a change: %+v", next)
	}

	next := d.mergeStops(cur, []tripStopIn{{destination: "X"}, {destination: "SOCN"}, {destination: "SOCW"}})
	out := tripStopDocs(doc, cur, next)
	if got := indexesOf(out); !slices.Equal(got, []any{int64(2), int64(1), nil, int64(3)}) {
		t.Fatalf("written indexes %v, want [2 1 <no index> 3]", got)
	}
	if m := out[2].(map[string]any); m["destination"] != "NOIDX" || len(m["photos"].([]any)) != 1 {
		t.Fatalf("the entry without an index lost its photos %v", m)
	}
	if lo, mo := loaderOrder(out), mergedOrder(next); !slices.Equal(lo, mo) || !slices.Equal(lo, []int64{2, 1}) {
		t.Fatalf("loader order %v, merged %v, want [2 1]", lo, mo)
	}

	// Stop 3 delivered now: after both delivered stops, and the loader reads 2, 1, 3.
	next = d.mergeStops(cur, []tripStopIn{{destination: "X"}, {destination: "SOCN"}, {destination: "SOCW", status: ptr("delivered")}})
	out = tripStopDocs(doc, cur, next)
	if lo, mo := loaderOrder(out), mergedOrder(next); !slices.Equal(lo, mo) || !slices.Equal(lo, []int64{2, 1, 3}) {
		t.Fatalf("loader order %v, merged %v, want [2 1 3]", lo, mo)
	}

	// A pending entry the driver had not delivered, now marked delivered: it leaves its position for the end.
	doc2 := []any{
		map[string]any{"index": int64(1), "destination": "X", "status": "pending"},
		map[string]any{"index": int64(2), "destination": "SOCN", "status": "delivered", "deliveredAt": ts},
		map[string]any{"index": int64(3), "destination": "SOCW", "status": "failed"},
	}
	cur2 := docStops(doc2)
	next2 := d.mergeStops(cur2, []tripStopIn{{destination: "X", status: ptr("delivered")}, {destination: "SOCN"}, {destination: "SOCW"}})
	out2 := tripStopDocs(doc2, cur2, next2)
	if got := indexesOf(out2); !slices.Equal(got, []any{int64(2), int64(3), int64(1)}) {
		t.Fatalf("written indexes %v, want [2 3 1]", got)
	}
	if lo, mo := loaderOrder(out2), mergedOrder(next2); !slices.Equal(lo, mo) || !slices.Equal(lo, []int64{2, 1}) {
		t.Fatalf("loader order %v, merged %v, want [2 1]", lo, mo)
	}
	// A removed stop leaves; the others keep their positions.
	next3 := d.mergeStops(cur2, []tripStopIn{{destination: "X"}, {destination: "SOCN"}})
	if got := indexesOf(tripStopDocs(doc2, cur2, next3)); !slices.Equal(got, []any{int64(1), int64(2)}) {
		t.Fatalf("written indexes %v, want [1 2]", got)
	}
}

// An origin edit that re-points the task to a hub of another billing party names sourceLinkedPartyId in changed[] (as
// PATCH /v1/tasks/{id} does), so the realtime projection sends the dispatch copy to the party that now reads the task.
func TestTaskMirrorNamesTheParty(t *testing.T) {
	hubA, hubB, p1, p2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	d := directory{byID: map[uuid.UUID]Hub{hubA: {ID: hubA, Code: "HUBA", LinkedPartyID: &p1, StationType: "HUB"},
		hubB: {ID: hubB, Code: "HUBB", LinkedPartyID: &p2, StationType: "HUB"}},
		byCode: map[string][]uuid.UUID{"HUBA": {hubA}, "HUBB": {hubB}}}
	task := Task{ID: uuid.New(), SourceHubRaw: "HUBA", SourceHubID: &hubA, SourceLinkedPartyID: &p1, TaskType: FirstMile}
	pr := operationsdb.UpdateTripDetailsParams{OriginRaw: ptr("HUBB")}
	m := d.planTaskMirror(task, nil, pr, []string{"origin"}, tripShape{derivedType: FirstMile}, time.Now())
	if !slices.Equal(m.changed, []string{"sourceHub", "sourceLinkedPartyId"}) || m.params.SourceLinkedPartyID == nil || *m.params.SourceLinkedPartyID != p2 {
		t.Fatalf("mirror %v %v", m.changed, m.params.SourceLinkedPartyID)
	}
	if sent, _ := realtimeSent(t, p2, m.changed); !sent {
		t.Fatal("the new party's dispatchers get no copy")
	}
	// The same party: no link change.
	task.SourceLinkedPartyID = &p2
	if m := d.planTaskMirror(task, nil, pr, []string{"origin"}, tripShape{derivedType: FirstMile}, time.Now()); !slices.Equal(m.changed, []string{"sourceHub"}) {
		t.Fatalf("same party %v", m.changed)
	}
}

// realtimeSent projects a task.updated with changed[] on the party's dispatch topic, as the relay does.
func realtimeSent(t *testing.T, party uuid.UUID, changed []string) (bool, json.RawMessage) {
	t.Helper()
	ev := taskEvent(RouteTaskUpdated, Task{ID: uuid.New(), TaskType: FirstMile, Status: TaskAssigned}, changed)
	raw, err := json.Marshal(ev.payload)
	if err != nil {
		t.Fatal(err)
	}
	out, sent := realtime.Project("dispatch:"+party.String()+":tasks", RouteTaskUpdated, raw)
	return sent, out
}
