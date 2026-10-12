//go:build integration

// Acceptance tests of issue T29 (#49, operations mirror hardening) on postgres:18-alpine against the in-process Firestore
// stand-in: the domain events of the mirror's writes, the trip rename, Removed applied only where the legacy app
// deletes, the listeners (and a gap of ten minutes of writes that loses nothing) and the daily reconciliation of the
// operations collections.
package etl

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// opsEvent is one outbox row of the mirror.
type opsEvent struct {
	key, aggregate string
	topics         []string
	payload        map[string]any
	origin         string
}

// eventsAfter are the outbox rows after id, oldest first.
func (h *syncHarness) eventsAfter(id int64) []opsEvent {
	h.t.Helper()
	rows, err := h.pool.Query(h.ctx, `SELECT routing_key, aggregate_id, realtime_topics, payload, headers->>'origin'
		FROM outbox_events WHERE id > $1 ORDER BY id`, id)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []opsEvent
	for rows.Next() {
		var e opsEvent
		var raw []byte
		var origin *string
		if err := rows.Scan(&e.key, &e.aggregate, &e.topics, &raw, &origin); err != nil {
			h.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &e.payload); err != nil {
			h.t.Fatal(err)
		}
		if origin != nil {
			e.origin = *origin
		}
		out = append(out, e)
	}
	return out
}

func (h *syncHarness) lastEvent() int64 {
	h.t.Helper()
	var id int64
	if err := h.pool.QueryRow(h.ctx, `SELECT coalesce(max(id), 0) FROM outbox_events`).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

func keys(evs []opsEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.key
	}
	return out
}

func ts(t time.Time) dump.Timestamp { return dump.Timestamp{Time: t} }

// AC: one Firestore update of a delivered trip produces exactly one trip.delivered outbox event; the mirror's writes of
// the four operations collections emit the domain events of Appendix B §B.4.3 (thin payloads, tenant and dispatch
// topics, the drivers' queues for task events), only when the row changed, marked origin etl.sync.
func TestMirrorEmitsOperationsEvents(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	if p.Mode("trip_records") != MirrorFull || p.Mode("tasks") != MirrorFull {
		t.Fatalf("plan %+v", p.Entries)
	}
	h.sweep(p)
	own := h.id(`SELECT id FROM tenants WHERE kind = 'own_fleet'`)
	task1 := h.id(`SELECT id FROM tasks WHERE legacy_doc_id = 'task_1'`)
	at := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)

	// A trip the 3.x app starts: trip.created on the own fleet's trips topic and the task's dispatch topics.
	mark := h.lastEvent()
	h.fs.Set("trip_records/TRIP100", map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
		"driverId": "uid_own", "spxTripId": "TRIP100", "createdAt": ts(at), "updatedAt": ts(at)})
	rep := h.sweep(p)
	evs := h.eventsAfter(mark)
	if !slices.Equal(keys(evs), []string{"trip.created"}) {
		t.Fatalf("a new trip: %v", keys(evs))
	}
	trip := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP100'`)
	e := evs[0]
	if e.aggregate != trip.String() || e.origin != "etl.sync" || e.payload["source"] != "mirror" || e.payload["created"] != true ||
		!slices.Contains(e.topics, "tenant:"+own.String()+":trips") {
		t.Fatalf("trip.created %+v", e)
	}
	var parties []uuid.UUID
	rows, err := h.pool.Query(h.ctx, `SELECT DISTINCT p FROM (SELECT unnest(ARRAY[billing_party_id, source_linked_party_id,
		destination_linked_party_id]) p FROM tasks WHERE id = $1) x WHERE p IS NOT NULL`, task1)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		parties = append(parties, id)
	}
	if len(parties) == 0 {
		t.Fatal("task_1 has no party: the dispatch topics are not exercised")
	}
	for _, party := range parties {
		if !slices.Contains(e.topics, "dispatch:"+party.String()+":trips") {
			t.Errorf("trip.created misses the dispatch topic of party %s: %v", party, e.topics)
		}
	}
	for _, c := range rep.Collections {
		if c.Name == "trip_records" && c.Events != 1 {
			t.Errorf("trip_records events %d", c.Events)
		}
	}

	// AC: the delivering update emits exactly one trip.delivered; sweeping again emits nothing.
	mark = h.lastEvent()
	h.fs.Merge("trip_records/TRIP100", map[string]any{"status": "delivered", "deliveredTimestamp": ts(at.Add(2 * time.Hour)),
		"updatedAt": ts(at.Add(2 * time.Hour))})
	h.sweep(p)
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if !slices.Equal(keys(evs), []string{"trip.delivered"}) {
		t.Fatalf("one Firestore update of a delivered trip: %v", keys(evs))
	}
	if n := h.scalar(`SELECT count(*) FROM outbox_events WHERE routing_key = 'trip.delivered' AND aggregate_id = $1`, trip.String()); n != int64(1) {
		t.Fatalf("trip.delivered rows %v", n)
	}
	// A later edit of the delivered trip is an update; a moved delivery time also tells billing.compute.
	mark = h.lastEvent()
	h.fs.Merge("trip_records/TRIP100", map[string]any{"sealCode": "SEAL-9", "updatedAt": ts(at.Add(3 * time.Hour))})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if !slices.Equal(keys(evs), []string{"trip.updated"}) || !slices.Contains(payloadStrings(evs[0].payload["changed"]), "sealCode") {
		t.Fatalf("an edit of a delivered trip: %+v", evs)
	}
	mark = h.lastEvent()
	h.fs.Merge("trip_records/TRIP100", map[string]any{"deliveredTimestamp": ts(at.Add(150 * time.Minute)), "updatedAt": ts(at.Add(4 * time.Hour))})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if !slices.Equal(keys(evs), []string{"trip.updated", "trip.delivered_at_changed"}) || len(evs[1].topics) != 0 {
		t.Fatalf("a moved delivery time: %+v", evs)
	}
	// A Firestore write that changes nothing PostgreSQL holds (the billing fields the Cloud Function writes live in the
	// snapshot table) emits nothing.
	mark = h.lastEvent()
	h.fs.Merge("trip_records/TRIP100", map[string]any{"someLegacyField": "x"})
	h.sweep(p)
	if evs = h.eventsAfter(mark); len(evs) != 0 {
		t.Fatalf("an unmapped field: %v", keys(evs))
	}

	// Nor does one of a multi-drop trip with stop photos: its stops are replaced (new ids) by every apply and its stop
	// photos re-linked, which is no change of the trip.
	if v := h.scalar(`SELECT count(*) FROM trip_photos p JOIN trip_records r ON r.id = p.trip_id WHERE r.legacy_doc_id = 'TRIP_MD' AND p.stop_id IS NOT NULL`); v == int64(0) {
		t.Fatal("TRIP_MD has no stop photo: the stop digest is not exercised")
	}
	mark = h.lastEvent()
	h.fs.Merge("trip_records/TRIP_MD", map[string]any{"someLegacyField": "x"})
	h.sweep(p)
	h.fs.Merge("trip_records/TRIP_MD", map[string]any{"billingEstimateThb": 1234.5})
	h.sweep(p)
	if evs = h.eventsAfter(mark); len(evs) != 0 {
		t.Fatalf("an unchanged multi-drop trip: %+v", evs)
	}

	// Tasks: a cancellation tells the task's driver queue too; a moved plan day adds task.plan_date_changed.
	task3 := h.id(`SELECT id FROM tasks WHERE legacy_doc_id = 'task_3'`)
	driver := h.id(`SELECT driver_id FROM tasks WHERE id = $1`, task3)
	mark = h.lastEvent()
	h.fs.Merge("tasks/task_3", map[string]any{"status": "Cancelled", "date": ts(time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC))})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if !slices.Equal(keys(evs), []string{"task.cancelled", "task.plan_date_changed"}) {
		t.Fatalf("a cancelled, moved task: %v", keys(evs))
	}
	if !slices.Contains(evs[0].topics, "driver:"+driver.String()) || !slices.Contains(evs[0].topics, "tenant:"+own.String()+":tasks") ||
		evs[0].payload["planDate"] != "2026-09-03" || evs[0].payload["taskType"] != "first_mile" {
		t.Fatalf("task.cancelled %+v", evs[0])
	}
	// A task taken from its driver: the driver who lost it hears of it.
	mark = h.lastEvent()
	h.fs.Merge("tasks/task_dup_a", map[string]any{"driverId": nil})
	h.sweep(p)
	ownDriver := h.id(`SELECT id FROM drivers WHERE legacy_doc_id = 'drv_own'`)
	if evs = h.eventsAfter(mark); !slices.Equal(keys(evs), []string{"task.reassigned"}) || evs[0].payload["previousDriverId"] != ownDriver.String() ||
		!slices.Contains(evs[0].topics, "driver:"+ownDriver.String()) {
		t.Fatalf("a task taken from its driver: %+v", evs)
	}

	// Standby: a new record is completed (billing and the boards); a customer assigned later tells billing only.
	mark = h.lastEvent()
	h.fs.Set("standby_records/sb100", map[string]any{"driverId": "uid_own", "taskId": "task_1", "status": "completed",
		"startedAt": ts(at), "endedAt": ts(at.Add(time.Hour)), "createdAt": ts(at), "updatedAt": ts(at)})
	h.sweep(p)
	if evs = h.eventsAfter(mark); !slices.Equal(keys(evs), []string{"standby.completed"}) {
		t.Fatalf("a new standby record: %v", keys(evs))
	}
	mark = h.lastEvent()
	h.fs.Merge("standby_records/sb100", map[string]any{"customerId": "cust_cjsf", "updatedAt": ts(at.Add(2 * time.Hour))})
	h.sweep(p)
	if evs = h.eventsAfter(mark); !slices.Equal(keys(evs), []string{"standby.customer_assigned"}) || len(evs[0].topics) != 0 {
		t.Fatalf("a customer assigned: %+v", evs)
	}

	// An incident report.
	mark = h.lastEvent()
	h.fs.Set("incidentReport/inc100", map[string]any{"tripId": "TRIP100", "driverId": "uid_own", "delayCause": "incident_cause_traffic",
		"createdAt": ts(at), "updatedAt": ts(at)})
	h.sweep(p)
	if evs = h.eventsAfter(mark); !slices.Equal(keys(evs), []string{"incident.created"}) || evs[0].payload["tripId"] != trip.String() {
		t.Fatalf("a new incident: %+v", evs)
	}

	// A row in the quarantine tenant emits nothing (no tenant principal sees it).
	mark = h.lastEvent()
	h.fs.Set("trip_records/TRIP-Q", map[string]any{"status": "in_transit", "jobType": "first_mile", "driverId": "uid_nobody",
		"createdAt": ts(at), "updatedAt": ts(at)})
	h.sweep(p)
	if v := h.scalar(`SELECT tenant_source FROM trip_records WHERE legacy_doc_id = 'TRIP-Q'`); v != "quarantine" {
		t.Fatalf("TRIP-Q tenant source %v", v)
	}
	if evs = h.eventsAfter(mark); len(evs) != 0 {
		t.Fatalf("a quarantined trip: %v", keys(evs))
	}
}

func payloadStrings(v any) []string {
	var out []string
	if a, ok := v.([]any); ok {
		for _, e := range a {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// T31 review: the operations write-back writes the caller's Firebase uid in reportedByUid and renamedBy, else (a login
// born in Go) its users.id; the mirror resolves both to the reporter and the renamer. An id that is no user still
// raises user_unresolved.
func TestMirrorResolvesAGoUserID(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	goUser := h.id(`INSERT INTO users (email, display_name) VALUES ('go-admin@example.test', 'Go admin') RETURNING id`)
	fbUser := h.id(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('fb-admin@example.test', 'FB admin', 'uid_fbadmin') RETURNING id`)
	created := ts(time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC))
	h.fs.Set("incidentReport/inc_go", map[string]any{"tripId": "TRIP001", "driverId": "uid_own", "description": "x",
		"reportedBy": "admin", "reportedByUid": goUser.String(), "createdAt": created})
	h.fs.Set("incidentReport/inc_fb", map[string]any{"tripId": "TRIP001", "driverId": "uid_own", "description": "x",
		"reportedBy": "admin", "reportedByUid": "uid_fbadmin", "createdAt": created})
	stranger := uuid.Must(uuid.NewV7()).String()
	h.fs.Set("incidentReport/inc_none", map[string]any{"tripId": "TRIP001", "driverId": "uid_own", "description": "x",
		"reportedBy": "admin", "reportedByUid": stranger, "createdAt": created})
	h.sweep(p)
	if v := h.scalar(`SELECT reported_by_user_id::text FROM incident_reports WHERE legacy_doc_id = 'inc_go'`); v != goUser.String() {
		t.Fatalf("a Go user's id: %v", v)
	}
	if v := h.scalar(`SELECT reported_by_user_id::text FROM incident_reports WHERE legacy_doc_id = 'inc_fb'`); v != fbUser.String() {
		t.Fatalf("a Firebase uid: %v", v)
	}
	if v := h.scalar(`SELECT reported_by_user_id IS NULL FROM incident_reports WHERE legacy_doc_id = 'inc_none'`); v != true {
		t.Fatalf("an unknown id: %v", v)
	}
	if n := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE reason_code = 'user_unresolved' AND resolved_at IS NULL
		AND doc_path IN ('incidentReport/inc_go', 'incidentReport/inc_none')`); n != int64(1) {
		t.Fatalf("user_unresolved findings %v, want the unknown id's only", n)
	}
}

// AC: a rename fixture yields one trip row with the new trip_no and one history row. renameTripRecord's batch (copy,
// re-point the incident reports, delete) becomes an UPDATE of the trip's row: same id, children and links, one
// trip_no_history row, the old document settled, one trip.updated; a copy whose original still exists is its own row.
func TestMirrorRenameUpdatesTripNo(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	trip := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP001'`)
	photos := h.scalar(`SELECT count(*) FROM trip_photos WHERE trip_id = $1`, trip)
	h.fs.Set("incidentReport/inc_r", map[string]any{"tripId": "TRIP001", "driverId": "uid_own", "delayCause": "incident_cause_traffic",
		"createdAt": ts(time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC))})
	h.sweep(p)
	if v := h.scalar(`SELECT trip_id::text FROM incident_reports WHERE legacy_doc_id = 'inc_r'`); v != trip.String() {
		t.Fatalf("incident before the rename: %v", v)
	}
	if n := h.scalar(`SELECT count(*) FROM trip_no_history WHERE trip_id = $1`, trip); n != int64(0) {
		t.Fatalf("history before the rename: %v", n)
	}

	renamedAt := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	mark := h.lastEvent()
	h.fs.Rename("trip_records/TRIP001", "trip_records/TRIP001X", map[string]any{"spxTripId": "TRIP001X", "renamedFromTripId": "TRIP001",
		"renamedAt": ts(renamedAt), "renamedBy": "uid_admin", "updatedAt": ts(renamedAt)})
	h.fs.Merge("incidentReport/inc_r", map[string]any{"tripId": "TRIP001X"})
	h.sweep(p)

	if n := h.scalar(`SELECT count(*) FROM trip_records WHERE trip_no IN ('TRIP001', 'TRIP001X') OR legacy_doc_id IN ('TRIP001', 'TRIP001X')`); n != int64(1) {
		t.Fatalf("rows of the renamed trip: %v", n)
	}
	if v := h.scalar(`SELECT id::text || ' ' || trip_no || ' ' || legacy_doc_id FROM trip_records WHERE trip_no = 'TRIP001X'`); v !=
		trip.String()+" TRIP001X TRIP001X" {
		t.Fatalf("the renamed row: %v", v)
	}
	if v := h.scalar(`SELECT string_agg(old_trip_no || '>' || new_trip_no || ' ' || to_char(renamed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI'), ',')
		FROM trip_no_history WHERE trip_id = $1`, trip); v != "TRIP001>TRIP001X 2026-10-01T09:00" {
		t.Fatalf("history: %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM trip_photos WHERE trip_id = $1`, trip); v != photos {
		t.Errorf("photos %v -> %v", photos, v)
	}
	if v := h.scalar(`SELECT trip_id::text FROM incident_reports WHERE legacy_doc_id = 'inc_r'`); v != trip.String() {
		t.Errorf("the re-pointed incident: %v", v)
	}
	if v := h.scalar(`SELECT status || ' ' || (target_id IS NULL)::text FROM etl.source_docs WHERE doc_path = 'trip_records/TRIP001'`); v != "renamed true" {
		t.Errorf("the old document's entry: %v", v)
	}
	evs := h.eventsAfter(mark)
	var tripEvents []opsEvent
	for _, e := range evs {
		if strings.HasPrefix(e.key, "trip.") {
			tripEvents = append(tripEvents, e)
		}
	}
	if len(tripEvents) != 1 || tripEvents[0].key != "trip.updated" || tripEvents[0].payload["previousTripNo"] != "TRIP001" ||
		tripEvents[0].payload["tripNo"] != "TRIP001X" {
		t.Fatalf("rename events: %+v", tripEvents)
	}
	if rep := h.sweep(p); applied(rep, "trip_records") != 0 {
		t.Errorf("a second sweep re-applied: %+v", rep.Collections)
	}

	// A copy whose original is still in Firestore (rename-trip-doc.js, Q3) loads as its own row; the original keeps its.
	h.fs.Set("trip_records/TRIP002-COPY", map[string]any{"status": "in_transit", "jobType": "line_haul", "driverId": "uid_alpha",
		"renamedFromTripId": "TRIP002", "createdAt": ts(renamedAt)})
	h.sweep(p)
	if n := h.scalar(`SELECT count(*) FROM trip_records WHERE legacy_doc_id IN ('TRIP002', 'TRIP002-COPY')`); n != int64(2) {
		t.Fatalf("copy rows %v", n)
	}

	// The daily reconciliation sees no trip deleted at source and every row equal to its document.
	rec, err := h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.Checks {
		if c.Key == "trip_records" && (c.Name == "deleted_at_source" || c.Name == "ops_rows_without_document") {
			t.Errorf("after the rename: %+v", c)
		}
	}
	if !rec.Match {
		t.Fatalf("reconcile-sync after the rename: %+v", rec.Mismatches())
	}
}

// Removed is applied only where the legacy app deletes: a draft statement, a holiday, a service fee are removed with
// their documents; a sent statement and an operations document are kept and reported.
func TestMirrorRemovedOnlyWhereLegacyDeletes(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	// A third statement, so the statements collection is not read empty (an empty read removes nothing).
	july := h.doc("billing_statements/ST-SPX-2026-08")
	period := maps.Clone(july["period"].(map[string]any))
	period["month"] = int64(7)
	july["period"] = period
	h.fs.Set("billing_statements/ST-SPX-2026-07", july)
	h.sweep(p)
	if n := h.scalar(`SELECT count(*) FROM billing_statements WHERE legacy_doc_id = 'ST-SPX-2026-07'`); n != int64(1) {
		t.Fatalf("the July draft: %v %v %v", n, h.scalar(`SELECT coalesce(max(status), 'none') FROM etl.source_docs WHERE doc_path = 'billing_statements/ST-SPX-2026-07'`),
			h.scalar(`SELECT coalesce(string_agg(reason_code || ':' || coalesce(field, '') || ':' || coalesce(detail, ''), '; '), '') FROM etl.quarantine WHERE doc_path = 'billing_statements/ST-SPX-2026-07'`))
	}
	for _, path := range []string{"billing_statements/ST-SPX-2026-08", "billing_statements/st_cjsf_202609", "holidays/ho_2",
		"customer_service_fees/fee_custom", "tasks/task_4", "standby_records/sb2"} {
		h.fs.Delete(path)
	}
	rep := h.sweep(p)
	stats := map[string]CollectionStats{}
	for _, c := range rep.Collections {
		stats[c.Name] = c
	}
	if s := stats["billing_statements"]; s.Removed != 1 || s.Kept != 1 {
		t.Errorf("statements %+v", s)
	}
	if s := stats["holidays"]; s.Removed != 1 {
		t.Errorf("holidays %+v", s)
	}
	if s := stats["customer_service_fees"]; s.Removed != 1 {
		t.Errorf("fees %+v", s)
	}
	if s := stats["tasks"]; s.Removed != 0 || s.Kept != 1 {
		t.Errorf("tasks %+v", s)
	}
	for q, want := range map[string]int64{
		`SELECT count(*) FROM billing_statements WHERE legacy_doc_id = 'ST-SPX-2026-08'`:               0,
		`SELECT count(*) FROM billing_statements WHERE legacy_doc_id = 'st_cjsf_202609'`:               1,
		`SELECT count(*) FROM holidays WHERE legacy_doc_id = 'ho_2'`:                                   0,
		`SELECT count(*) FROM customer_service_fees WHERE legacy_doc_id = 'fee_custom'`:                0,
		`SELECT count(*) FROM tasks WHERE legacy_doc_id = 'task_4'`:                                    1,
		`SELECT count(*) FROM standby_records WHERE legacy_doc_id = 'sb2'`:                             1,
		`SELECT count(*) FROM etl.source_docs WHERE doc_path = 'holidays/ho_2' AND status = 'removed'`: 1,
		`SELECT count(*) FROM etl.source_docs WHERE doc_path = 'billing_statements/st_cjsf_202609'`:    1,
	} {
		if v := h.scalar(q); v != want {
			t.Errorf("%s = %v, want %d", q, v, want)
		}
	}
	// A second sweep removes nothing more; the kept ones are reported, not failed.
	rep = h.sweep(p)
	for _, c := range rep.Collections {
		if c.Removed != 0 {
			t.Errorf("second sweep removed %+v", c)
		}
	}
	rec, err := h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p})
	if err != nil {
		t.Fatal(err)
	}
	reported := map[string]bool{}
	for _, c := range rec.Checks {
		if c.Name == "ops_rows_without_document" || c.Name == "deleted_at_source" {
			reported[c.Key] = true
		}
	}
	if !reported["tasks"] || !reported["standby_records"] || !reported["billing_statements"] {
		t.Errorf("kept documents reported: %v", reported)
	}
}

// AC: stopping the mirror for 10 minutes of writes and restarting loses no row. The listeners start again from the
// collection's watermark minus the overlap and apply every change of the gap; the sweep behind them finds nothing left.
// A listener applies a change without moving the watermark, and a document it could not apply is left to the sweep.
func TestListenersAfterAGapLoseNothing(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	if got := ListenCollections(p); !slices.Equal(got, OpsCollections) {
		t.Fatalf("listened %v", got)
	}
	start := func() map[string]time.Time {
		c := map[string]time.Time{}
		for _, coll := range ListenCollections(p) {
			s, err := h.e.ListenStart(h.ctx, coll)
			if err != nil {
				t.Fatal(err)
			}
			c[coll] = s
		}
		return c
	}
	start() // the cursors of a listener running before the gap (the restart below reads them again)
	marks := h.scalar(`SELECT string_agg(collection || '=' || last_update_time::text, ',' ORDER BY collection) FROM etl.watermarks`)

	// Ten minutes of writes while the mirror is stopped: 20 new trips, 10 task edits, a standby record.
	gap := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := range 20 {
		at := gap.Add(time.Duration(i) * 30 * time.Second)
		h.fs.Set(fmt.Sprintf("trip_records/GAP%02d", i), map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
			"driverId": "uid_own", "createdAt": ts(at), "updatedAt": ts(at)})
	}
	for i := range 10 {
		h.fs.Merge("tasks/task_1", map[string]any{"parcelNote": fmt.Sprint(i), "updatedAt": ts(gap.Add(time.Duration(i) * time.Minute))})
	}
	h.fs.Set("standby_records/sbgap", map[string]any{"driverId": "uid_own", "taskId": "task_1", "status": "completed",
		"startedAt": ts(gap), "endedAt": ts(gap.Add(time.Hour)), "createdAt": ts(gap), "updatedAt": ts(gap.Add(10 * time.Minute))})
	// A write without updatedAt is invisible to the listeners: the sweep applies it.
	h.fs.Set("trip_records/GAP-NOFIELD", map[string]any{"status": "in_transit", "jobType": "first_mile", "driverId": "uid_own",
		"createdAt": ts(gap)})

	// Restart: fresh cursors from the watermarks.
	cursors := start()
	rep, next, err := h.e.MirrorChanges(h.ctx, h.client, p, cursors)
	if err != nil {
		t.Fatal(err)
	}
	if n := h.scalar(`SELECT count(*) FROM trip_records WHERE legacy_doc_id LIKE 'GAP__'`); n != int64(20) {
		t.Fatalf("trips after the restart: %v", n)
	}
	if n := h.scalar(`SELECT count(*) FROM standby_records WHERE legacy_doc_id = 'sbgap'`); n != int64(1) {
		t.Fatalf("standby after the restart: %v", n)
	}
	// The gap's updatedAt values run up to ten minutes ahead of the stand-in's clock: the cursors stop at the read time.
	if !next["trip_records"].Equal(rep.ExportedAt) || !next["tasks"].Equal(rep.ExportedAt) || next["standby_records"].After(rep.ExportedAt) {
		t.Errorf("next cursors %v, read at %v", next, rep.ExportedAt)
	}
	for _, d := range rep.Docs {
		if d.DocID == "" || d.UpdateTime.IsZero() {
			t.Fatalf("a result without the version applied: %+v", d)
		}
	}
	if v := h.scalar(`SELECT string_agg(collection || '=' || last_update_time::text, ',' ORDER BY collection) FROM etl.watermarks`); v != marks {
		t.Errorf("a listener moved the watermarks: %v -> %v", marks, v)
	}
	// A second poll from the next cursors applies nothing (the overlap meets the update-time guard).
	rep, _, err = h.e.MirrorChanges(h.ctx, h.client, p, next)
	if err != nil || rep.Applied() != 0 {
		t.Fatalf("a second poll applied %d (%v)", rep.Applied(), err)
	}
	if n := h.scalar(`SELECT count(*) FROM trip_records WHERE legacy_doc_id = 'GAP-NOFIELD'`); n != int64(0) {
		t.Fatalf("a listener saw a write without updatedAt")
	}
	// The sweep behind: only the document the listeners could not see.
	if rep := h.sweep(p); applied(rep, "trip_records") != 1 || applied(rep, "tasks") != 0 || applied(rep, "standby_records") != 0 {
		t.Fatalf("the sweep after the listeners: %+v", rep.Collections)
	}
	// A listener defers what it cannot decide alone (a trip it would reject): the sweep applies the verdict.
	h.fs.Set("trip_records/GAP-BAD", map[string]any{"status": "loading", "jobType": "first_mile", "driverId": "uid_own",
		"updatedAt": ts(gap.Add(20 * time.Minute))})
	rep, _, err = h.e.MirrorChanges(h.ctx, h.client, p, next)
	if err != nil {
		t.Fatal(err)
	}
	deferred := 0
	for _, c := range rep.Collections {
		deferred += c.Deferred
	}
	if deferred != 1 || h.scalar(`SELECT count(*) FROM etl.source_docs WHERE doc_path = 'trip_records/GAP-BAD'`) != int64(0) {
		t.Fatalf("deferred %d", deferred)
	}
	h.sweep(p)
	if v := h.scalar(`SELECT status FROM etl.source_docs WHERE doc_path = 'trip_records/GAP-BAD'`); v != "rejected" {
		t.Fatalf("the sweep's verdict: %v", v)
	}
}

// AC: the daily reconciliation of the operations collections exits 0 on the fixtures and catches a row that differs
// from its document (and a delivered trip the mirror missed).
func TestReconcileOperationsOnFixtures(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	rec, err := h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Match {
		t.Fatalf("fixtures: %+v", rec.Mismatches())
	}
	seen := map[string]bool{}
	for _, c := range rec.Checks {
		if c.Name == "ops_fields" {
			seen[c.Key] = true
		}
	}
	for _, coll := range OpsCollections {
		if !seen[coll] {
			t.Errorf("no ops_fields check for %s", coll)
		}
	}
	// A row changed behind the mirror's back is a mismatch naming the document and the field.
	h.exec(`UPDATE trip_records SET status = 'cancelled' WHERE legacy_doc_id = 'TRIP001'`)
	rec, err = h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range rec.Mismatches() {
		if c.Name == "ops_fields" && c.Key == "trip_records" && strings.Contains(c.Note, "trip_records/TRIP001 (status)") {
			found = true
		}
	}
	if rec.Match || !found {
		t.Fatalf("a tampered row: %+v", rec.Mismatches())
	}
}
