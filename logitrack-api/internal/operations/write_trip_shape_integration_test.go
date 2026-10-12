//go:build integration

package operations_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The two edits of EditTripDetailsDialog.tsx that T31 left without a route (issue #54, T34): PATCH /v1/trips/{id}
// stops[] (the stop progress: a stop marked delivered, added, removed and re-pointed; mirrored on the task's planned
// stops) and the job type an edited origin implies (a hub first_mile, a SOC line_haul; mirrored on the task's type with
// its route, the number kept), on PostgreSQL as logitrack_app.

// changedOf is the changed[] of an event payload.
func changedOf(e event) []string {
	raw, _ := e.payload["changed"].([]any)
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = fmt.Sprint(v)
	}
	return out
}

func TestPatchTripStops(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	task, trip := r.tripOf("SPXTH-0101")
	path := "/v1/trips/" + trip

	// Stop 1 delivered, stop 2 added: the trip's progress and the task's plan.
	d := r.must(200, "staffO", "PATCH", path, map[string]any{"stops": []any{
		map[string]any{"destination": "soce", "status": "delivered", "deliveredAt": "2026-10-11T08:00:00Z", "lat": 13.7, "lng": 100.5},
		map[string]any{"destination": "SPK890146"},
	}})
	stops, _ := d["stops"].([]any)
	if len(stops) != 2 || d["isMultiDelivery"] != true {
		t.Fatalf("patched %v", d)
	}
	if s1 := stops[0].(map[string]any); s1["destination"] != "SOCE" || s1["status"] != "delivered" || s1["completedSeq"] != float64(1) {
		t.Fatalf("stop 1 %v", s1)
	}
	if s2 := stops[1].(map[string]any); s2["status"] != "pending" || s2["deliveredAt"] != nil {
		t.Fatalf("stop 2 %v", s2)
	}
	if r.count(`SELECT count(*) FROM task_delivery_stops WHERE task_id = $1`, task) != 2 ||
		r.count(`SELECT count(*) FROM task_delivery_stops WHERE task_id = $1 AND stop_index = 1 AND destination_raw = 'SOCE'
			AND status = 'delivered' AND delivered_lat = 13.7`, task) != 1 ||
		r.count(`SELECT count(*) FROM task_delivery_stops WHERE task_id = $1 AND stop_index = 2 AND destination_raw = 'SPK890146'
			AND status = 'pending'`, task) != 1 ||
		r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND is_multi_delivery`, task) != 1 {
		t.Fatal("the task's plan does not mirror the stops")
	}
	tripEvs, taskEvs := r.events(trip), r.events(task)
	if last := tripEvs[len(tripEvs)-1]; last.route != "trip.updated" || !slices.Equal(changedOf(last), []string{"stops", "isMultiDelivery"}) {
		t.Fatalf("trip event %v %v", last.route, changedOf(last))
	}
	if last := taskEvs[len(taskEvs)-1]; last.route != "task.updated" || !slices.Equal(changedOf(last), []string{"deliveryStops", "isMultiDelivery"}) ||
		len(last.topics) == 0 {
		t.Fatalf("task event %v %v %v", last.route, changedOf(last), last.topics)
	}

	// A photo of stop 1 keeps its stop when the list changes: stop 2 re-pointed, stop 3 added delivered.
	stop1 := r.id(`SELECT id FROM trip_delivery_stops WHERE trip_id = $1 AND stop_index = 1`, trip)
	r.exec(`INSERT INTO trip_photos (trip_id, stop_id, photo_type, file_id, position) VALUES ($1, $2, 'stop_1_arrived', $3, 0)`,
		trip, stop1, r.files[0])
	r.exec(`UPDATE task_delivery_stops SET estimated_rate_thb = 120, source_id = 'SRC' WHERE task_id = $1 AND destination_raw = 'SOCE'`, task)
	d = r.must(200, "staffO", "PATCH", path, map[string]any{"stops": []any{
		map[string]any{"destination": "SOCE"}, map[string]any{"destination": "SOCN"},
		map[string]any{"destination": "ALANG-A", "status": "delivered"},
	}})
	if got := r.id(`SELECT id FROM trip_delivery_stops WHERE trip_id = $1 AND stop_index = 1`, trip); got != stop1 ||
		r.count(`SELECT count(*) FROM trip_photos WHERE stop_id = $1`, stop1) != 1 {
		t.Fatal("stop 1 lost its id or its photo")
	}
	if s3 := d["stops"].([]any)[2].(map[string]any); s3["status"] != "delivered" || s3["completedSeq"] != float64(2) {
		t.Fatalf("stop 3 %v", s3)
	}
	if r.count(`SELECT count(*) FROM task_delivery_stops WHERE task_id = $1 AND destination_raw = 'SOCE'
			AND estimated_rate_thb = 120 AND source_id = 'SRC'`, task) != 1 ||
		r.count(`SELECT count(*) FROM task_delivery_stops WHERE task_id = $1 AND destination_raw = 'SOCN' AND stop_index = 2`, task) != 1 {
		t.Fatal("the task's kept stop lost its fields, or the re-pointed one is missing")
	}

	// The same list again changes nothing and appends nothing.
	k := len(r.events(trip))
	r.must(200, "staffO", "PATCH", path, map[string]any{"stops": []any{
		map[string]any{"destination": "SOCE"}, map[string]any{"destination": "SOCN"}, map[string]any{"destination": "ALANG-A"},
	}})
	if n := len(r.events(trip)); n != k {
		t.Fatalf("an unchanged list appended %d events", n-k)
	}

	// Stop 3 removed (its photos keep the trip), a single stop left: not multi-drop any more.
	r.must(200, "staffO", "PATCH", path, map[string]any{"stops": []any{map[string]any{"destination": "SOCE"}}})
	if r.count(`SELECT count(*) FROM trip_delivery_stops WHERE trip_id = $1`, trip) != 1 ||
		r.count(`SELECT count(*) FROM trip_records WHERE id = $1 AND NOT is_multi_delivery`, trip) != 1 ||
		r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND NOT is_multi_delivery`, task) != 1 ||
		r.count(`SELECT count(*) FROM task_delivery_stops WHERE task_id = $1`, task) != 1 {
		t.Fatal("the removed stops stayed")
	}

	r.refused(422, "stops.destination duplicate", "staffO", "PATCH", path, map[string]any{"stops": []any{
		map[string]any{"destination": "SOCE"}, map[string]any{"destination": " soce"}}})
	r.refused(422, "stops.status invalid", "staffO", "PATCH", path, map[string]any{"stops": []any{
		map[string]any{"destination": "SOCE", "status": "lost"}}})
	r.refused(403, "permission_denied", "user", "PATCH", path, map[string]any{"stops": []any{}})
	// A task past assigned still takes the admin's correction (it is no new plan).
	r.exec(`UPDATE tasks SET status = 'in_transit' WHERE id = $1`, task)
	r.must(200, "staffO", "PATCH", path, map[string]any{"stops": []any{
		map[string]any{"destination": "SOCE"}, map[string]any{"destination": "SOCW", "status": "failed"}}})
	if r.count(`SELECT count(*) FROM task_delivery_stops WHERE task_id = $1 AND destination_raw = 'SOCW' AND status = 'failed'`, task) != 1 {
		t.Fatal("the in-transit task's plan was not corrected")
	}
}

func TestPatchTripOriginJobType(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	socHub := r.id(`INSERT INTO hubs (source_id, name_th, network, station_type, linked_party_id)
		VALUES ('BKK-SOC1', 'กรุงเทพ', 'SPX', 'SOC', $1) RETURNING id`, r.partyY)
	task, trip := r.tripOf("SPXTH-0201")
	path := "/v1/trips/" + trip
	taskNo := func() string {
		var no string
		if err := r.etl.QueryRow(t.Context(), `SELECT task_no FROM tasks WHERE id = $1`, task).Scan(&no); err != nil {
			t.Fatal(err)
		}
		return no
	}
	no := taskNo()

	// Hub (first_mile) to a SOC key: line_haul on the trip and the task, whose route follows; its number stays.
	r.must(200, "staffO", "PATCH", path, map[string]any{"origin": "SOCN"})
	if r.count(`SELECT count(*) FROM trip_records WHERE id = $1 AND job_type = 'line_haul'`, trip) != 1 ||
		r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND task_type = 'line_haul' AND source_hub_raw = 'SOCN'`, task) != 1 ||
		taskNo() != no {
		t.Fatal("the origin's job type did not flip the trip and the task")
	}
	evs := r.events(trip)
	if last := evs[len(evs)-1]; !slices.Equal(changedOf(last), []string{"origin", "jobType"}) {
		t.Fatalf("trip changed %v", changedOf(last))
	}
	tevs := r.events(task)
	if last := tevs[len(tevs)-1]; last.route != "task.updated" || !slices.Equal(changedOf(last), []string{"sourceHub", "taskType"}) ||
		last.payload["taskType"] != "line_haul" {
		t.Fatalf("task event %v %v", changedOf(last), last.payload)
	}

	// Back to a hub: first_mile; then a hub of station type SOC (with its billing link copied to the task): line_haul.
	r.must(200, "staffO", "PATCH", path, map[string]any{"origin": "ALANG-A"})
	if r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND task_type = 'first_mile' AND source_hub_id = $2`, task, r.hubSrc) != 1 {
		t.Fatal("a hub origin is first_mile")
	}
	r.must(200, "staffO", "PATCH", path, map[string]any{"origin": "BKK-SOC1", "destination": "SOCW"})
	if r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND task_type = 'line_haul' AND source_hub_id = $2
			AND source_linked_party_id = $3 AND destination_raw = 'SOCW' AND destination_soc_key = 'SOCW'`, task, socHub, r.partyY) != 1 ||
		r.count(`SELECT count(*) FROM trip_records WHERE id = $1 AND job_type = 'line_haul'`, trip) != 1 {
		t.Fatal("a SOC station is line_haul, its link and the destination mirrored")
	}
	// An origin that resolves to no hub leaves the type as it is (the route still follows).
	r.must(200, "staffO", "PATCH", path, map[string]any{"origin": "ลานจอดรถ"})
	if r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND task_type = 'line_haul' AND source_hub_raw = 'ลานจอดรถ' AND source_hub_id IS NULL`, task) != 1 ||
		r.count(`SELECT count(*) FROM trip_records WHERE id = $1 AND job_type = 'line_haul'`, trip) != 1 {
		t.Fatal("an unresolved origin changed the type")
	}
}

// racingWriter plays a concurrent change of a document once: before the next commit, race runs (and is cleared).
type racingWriter struct {
	*writeback.Memory
	race func()
}

func (w *racingWriter) Commit(ctx context.Context, writes ...writeback.DocWrite) (time.Time, error) {
	if f := w.race; f != nil {
		w.race = nil
		f()
	}
	return w.Memory.Commit(ctx, writes...)
}

// The write-back (operations Firestore-owned): one commit over the trip and task documents, read-modify-write under
// the update times read, each stop's photos and the task's billing links kept; PostgreSQL untouched; the task's
// notify event; a document changed between the read and the commit is read again.
func TestWriteBackTripStops(t *testing.T) {
	mem := writeback.NewMemory()
	rw := &racingWriter{Memory: mem}
	r := newWriteWorld(t, writeOptions{writeBack: &operations.WriteBack{Writer: rw, Acks: &acks{}}})
	task := r.createTask(r.staffO, func() operations.NewTask {
		n := r.newTask(r.O, now)
		n.LegacyDocID = ptr("task_doc")
		return n
	}())
	in := r.newTrip(task, "SPXTH-WS1")
	in.LegacyDocID = ptr("SPXTH-WS1")
	trip := r.createTrip(r.staffO, in)
	at := dump.Timestamp{Time: now}
	mem.Put("trip_records", "SPXTH-WS1", map[string]any{"status": "in_transit", "jobType": "first_mile",
		"deliveryStopsProgress": []any{map[string]any{"index": int64(1), "destination": "SOCE", "status": "delivered", "deliveredAt": at,
			"photos": []any{map[string]any{"type": "stop_1_arrived", "url": "https://x/1"}}}}}, now)
	mem.Put("tasks", "task_doc", map[string]any{"status": "Pending", "taskType": "FIRST_MILE",
		"deliveryStops": []any{map[string]any{"index": int64(1), "destination": "SOCE", "destinationLinkedCustomerId": "cust_doc"}}}, now)

	before := len(mem.Writes())
	path := "/v1/trips/" + trip.ID.String()
	d := r.must(202, "staffO", "PATCH", path, map[string]any{"origin": "SOCN", "stops": []any{map[string]any{"destination": "SOCE"},
		map[string]any{"destination": "SOCW", "status": "delivered"}}})
	if d["collection"] != "trip_records" || d["docId"] != "SPXTH-WS1" {
		t.Fatalf("echo %v", d)
	}
	ws := mem.Writes()[before:]
	if len(ws) != 2 || ws[0].DocID != "SPXTH-WS1" || ws[1].DocID != "task_doc" {
		t.Fatalf("writes %v", ws)
	}
	tripDoc, _ := mem.Doc("trip_records", "SPXTH-WS1")
	progress, _ := tripDoc["deliveryStopsProgress"].([]any)
	if len(progress) != 2 || tripDoc["isMultiDelivery"] != true || tripDoc["totalDeliveryStops"] != int64(2) ||
		tripDoc["jobType"] != "line_haul" || tripDoc["origin"] != "SOCN" {
		t.Fatalf("trip document %v", tripDoc)
	}
	s1 := progress[0].(map[string]any)
	if photos, _ := s1["photos"].([]any); len(photos) != 1 || s1["status"] != "delivered" || s1["deliveredAt"] != now {
		t.Fatalf("stop 1 lost its photos or progress %v", s1)
	}
	taskDoc, _ := mem.Doc("tasks", "task_doc")
	planned, _ := taskDoc["deliveryStops"].([]any)
	if len(planned) != 2 || planned[0].(map[string]any)["destinationLinkedCustomerId"] != "cust_doc" ||
		planned[1].(map[string]any)["destination"] != "SOCW" || taskDoc["taskType"] != "LINE_HAUL" ||
		taskDoc["sourceHub"] != "SOCN" || taskDoc["isMultiDelivery"] != true {
		t.Fatalf("task document %v", taskDoc)
	}
	if r.count(`SELECT count(*) FROM trip_delivery_stops WHERE trip_id = $1`, trip.ID) != 0 ||
		r.count(`SELECT count(*) FROM trip_records WHERE id = $1 AND job_type = 'first_mile'`, trip.ID) != 1 {
		t.Fatal("the write-back changed PostgreSQL")
	}
	evs := r.events(task.ID.String())
	if last := evs[len(evs)-1]; last.route != "task.updated" || last.headers["origin"] != "writeback" ||
		last.payload["taskType"] != "line_haul" || !slices.Equal(changedOf(last), []string{"sourceHub", "taskType", "deliveryStops"}) {
		t.Fatalf("task notify %v %v %v", last.route, last.headers, changedOf(last))
	}

	// The app writes stop 2's position between the read and the commit: the commit is refused, the document read
	// again, and the third stop lands over the app's write.
	rw.race = func() {
		doc, _ := mem.Doc("trip_records", "SPXTH-WS1")
		p := slices.Clone(doc["deliveryStopsProgress"].([]any))
		s2 := maps.Clone(p[1].(map[string]any))
		s2["deliveredLat"] = 14.1
		p[1] = s2
		doc["deliveryStopsProgress"] = p
		mem.Put("trip_records", "SPXTH-WS1", doc, now.Add(time.Hour))
	}
	k := len(mem.Writes())
	r.must(202, "staffO", "PATCH", path, map[string]any{"stops": []any{map[string]any{"destination": "SOCE"},
		map[string]any{"destination": "SOCW"}, map[string]any{"destination": "ALANG-A"}}})
	tripDoc, _ = mem.Doc("trip_records", "SPXTH-WS1")
	progress = tripDoc["deliveryStopsProgress"].([]any)
	if rw.race != nil || len(progress) != 3 || progress[1].(map[string]any)["deliveredLat"] != 14.1 || len(mem.Writes())-k != 2 {
		t.Fatalf("after the race %v (%d writes)", progress, len(mem.Writes())-k)
	}
	// A refused edit writes nothing.
	k = len(mem.Writes())
	r.refused(422, "stops.destination duplicate", "staffO", "PATCH", path, map[string]any{"stops": []any{
		map[string]any{"destination": "SOCE"}, map[string]any{"destination": "SOCE"}}})
	if len(mem.Writes()) != k {
		t.Fatal("a refused edit wrote")
	}
}
