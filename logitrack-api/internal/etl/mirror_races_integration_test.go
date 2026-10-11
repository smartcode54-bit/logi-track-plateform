//go:build integration

// Regression tests of the T29 review (issue #49): the mirror's runs overlap (the listeners poll beside the sweep, a
// hand-run `etl mirror` beside the worker's, each reading Firestore before it takes the load lock), the writers'
// updatedAt is a device clock, and the daily reconciliation runs while the 3.x app keeps writing. Each test pins a
// source to the read time of the run that read first (the stand-in serves any past read time, as Firestore does).
package etl

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
)

// pinned reads the Firestore stand-in at one past read time: a run that read before a concurrent write.
type pinned struct {
	f  *gcp.Firestore
	at time.Time
}

func (p pinned) Documents(ctx context.Context, coll string, group bool) ([]dump.Doc, error) {
	ds, _, err := p.f.DocumentsAt(ctx, coll, group, p.at)
	return ds, err
}

func (p pinned) DocumentsAt(ctx context.Context, coll string, group bool, _ time.Time) ([]dump.Doc, time.Time, error) {
	return p.f.DocumentsAt(ctx, coll, group, p.at)
}

func (p pinned) Changed(ctx context.Context, coll, field string, since, _ time.Time) ([]dump.Doc, time.Time, error) {
	return p.f.Changed(ctx, coll, field, since, p.at)
}

func (p pinned) Lookup(ctx context.Context, path string, _ time.Time) (dump.Doc, bool, error) {
	return p.f.Lookup(ctx, path, p.at)
}

// readTime is the stand-in's current read time.
func (h *syncHarness) readTime() time.Time {
	h.t.Helper()
	_, at, err := h.client.DocumentsAt(h.ctx, "holidays", false, time.Time{})
	if err != nil || at.IsZero() {
		h.t.Fatalf("read time %v %v", at, err)
	}
	return at
}

// cursors are a listener's cursors, started from the watermarks.
func (h *syncHarness) cursors(p MirrorPlan) map[string]time.Time {
	h.t.Helper()
	c := map[string]time.Time{}
	for _, coll := range ListenCollections(p) {
		s, err := h.e.ListenStart(h.ctx, coll)
		if err != nil {
			h.t.Fatal(err)
		}
		c[coll] = s
	}
	return c
}

func (h *syncHarness) poll(src ChangeSource, p MirrorPlan, cursors map[string]time.Time) (*Report, map[string]time.Time) {
	h.t.Helper()
	rep, next, err := h.e.MirrorChanges(h.ctx, src, p, cursors)
	if err != nil {
		h.t.Fatalf("poll: %v", err)
	}
	return rep, next
}

func countKeys(evs []opsEvent, key string) int {
	n := 0
	for _, e := range evs {
		if e.key == key {
			n++
		}
	}
	return n
}

// A phone whose clock runs an hour ahead writes a trip: the listener applies it, but its cursor stops at the read time,
// so the correctly-clocked write that follows is applied by the next poll instead of waiting for the sweep.
func TestListenerCursorIgnoresAClockAhead(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	cursors := h.cursors(p)
	server := h.readTime()
	h.fs.Set("trip_records/AHEAD", map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
		"driverId": "uid_own", "createdAt": ts(server.Add(time.Hour)), "updatedAt": ts(server.Add(time.Hour))})
	rep, next := h.poll(h.client, p, cursors)
	if applied(rep, "trip_records") != 1 {
		t.Fatalf("the trip dated ahead: %+v", rep.Collections)
	}
	for coll, c := range next {
		if c.After(rep.ExportedAt) {
			t.Fatalf("cursor %s %v past the read time %v", coll, c, rep.ExportedAt)
		}
	}
	at := rep.ExportedAt
	h.fs.Set("trip_records/ONTIME", map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
		"driverId": "uid_own", "createdAt": ts(at), "updatedAt": ts(at)})
	rep, next = h.poll(h.client, p, next)
	if applied(rep, "trip_records") != 1 || h.scalar(`SELECT count(*) FROM trip_records WHERE legacy_doc_id = 'ONTIME'`) != int64(1) {
		t.Fatalf("the correctly-clocked trip after it: %+v", rep.Collections)
	}
	if next["trip_records"].After(rep.ExportedAt) {
		t.Fatalf("cursor %v past the read time %v", next["trip_records"], rep.ExportedAt)
	}
	// The trip dated ahead is read again until the read time passes it; the update-time guard skips it.
	if rep, _ = h.poll(h.client, p, next); rep.Applied() != 0 {
		t.Fatalf("a third poll applied %+v", rep.Collections)
	}
}

// A sweep that read Firestore before a rename commits after a listener applied it: the old document's copy it holds
// is not newer than the 'renamed' tombstone, so it never loads as a second row nor emits a second trip.delivered.
func TestRenameAppliedByAListenerBeforeAnOlderSweep(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	trip := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP001'`)
	// The trip was written within the sweep's look-back, so the older sweep compares it.
	now := h.readTime()
	h.fs.Merge("trip_records/TRIP001", map[string]any{"sealCode": "SEAL-R", "updatedAt": ts(now)})
	h.sweep(p)
	cursors := h.cursors(p)
	before := h.readTime() // the older sweep reads here

	h.fs.Rename("trip_records/TRIP001", "trip_records/TRIP001X", map[string]any{"spxTripId": "TRIP001X", "renamedFromTripId": "TRIP001",
		"renamedAt": ts(now), "renamedBy": "uid_admin", "updatedAt": ts(now)})
	mark := h.lastEvent()
	h.poll(h.client, p, cursors)
	if v := h.scalar(`SELECT id::text || ' ' || trip_no FROM trip_records WHERE legacy_doc_id = 'TRIP001X'`); v != trip.String()+" TRIP001X" {
		t.Fatalf("the listener's rename: %v", v)
	}
	if _, err := h.e.Mirror(h.ctx, pinned{h.client, before}, p); err != nil {
		t.Fatal(err)
	}
	h.sweep(p)

	if n := h.scalar(`SELECT count(*) FROM trip_records WHERE legacy_doc_id IN ('TRIP001', 'TRIP001X') OR trip_no IN ('TRIP001', 'TRIP001X')`); n != int64(1) {
		t.Fatalf("rows of the renamed trip: %v", n)
	}
	if n := h.scalar(`SELECT count(*) FROM trip_no_history WHERE trip_id = $1`, trip); n != int64(1) {
		t.Fatalf("history rows %v", n)
	}
	evs := h.eventsAfter(mark)
	if countKeys(evs, "trip.created") != 0 || countKeys(evs, "trip.delivered") != 0 || countKeys(evs, "trip.updated") != 1 {
		t.Fatalf("events of one rename: %v", keys(evs))
	}
	rec, err := h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.Checks {
		if c.Key == "trip_records" && (c.Name == "deleted_at_source" || c.Name == "ops_rows_without_document") {
			t.Errorf("a stray row or document: %+v", c)
		}
	}
	if !rec.Match {
		t.Fatalf("reconcile-sync: %+v", rec.Mismatches())
	}
}

// The other way round: a listener read the delivering update before the rename, and the sweep that applied the rename
// commits first. One delivering update is still exactly one trip.delivered and one row.
func TestRenameAppliedByASweepBeforeAnOlderPoll(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	now := h.readTime()
	h.fs.Set("trip_records/TRIPW", map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
		"driverId": "uid_own", "createdAt": ts(now), "updatedAt": ts(now)})
	h.sweep(p)
	trip := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIPW'`)
	cursors := h.cursors(p)
	h.fs.Merge("trip_records/TRIPW", map[string]any{"status": "delivered", "deliveredTimestamp": ts(now), "updatedAt": ts(now)})
	before := h.readTime() // the listener reads here: the trip delivered, not yet renamed
	h.fs.Rename("trip_records/TRIPW", "trip_records/TRIPWX", map[string]any{"spxTripId": "TRIPWX", "renamedFromTripId": "TRIPW",
		"renamedAt": ts(now), "renamedBy": "uid_admin", "updatedAt": ts(now)})
	mark := h.lastEvent()
	h.sweep(p)
	h.poll(pinned{h.client, before}, p, cursors)

	if n := h.scalar(`SELECT count(*) FROM trip_records WHERE legacy_doc_id IN ('TRIPW', 'TRIPWX') OR trip_no IN ('TRIPW', 'TRIPWX')`); n != int64(1) {
		t.Fatalf("rows of the renamed trip: %v", n)
	}
	if v := h.scalar(`SELECT trip_no || ' ' || status FROM trip_records WHERE id = $1`, trip); v != "TRIPWX delivered" {
		t.Fatalf("the trip: %v", v)
	}
	evs := h.eventsAfter(mark)
	if countKeys(evs, "trip.delivered") != 1 || countKeys(evs, "trip.created") != 0 {
		t.Fatalf("one delivering update: %v", keys(evs))
	}
}

// A trip_records document that names another driver's trip in renamedFromTripId is no rename of it (firestore.rules
// let any driver create a trip document with their own driverId): it loads as its own row, the trip keeps its row,
// its id and its history, also when it races a genuine rename of that trip in the same sweep.
func TestRenameClaimOfAnotherDriversTrip(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	trip1 := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP001'`) // uid_own's
	at := h.readTime()

	// An orphaned row (its document deleted in the console) claimed by another driver.
	h.fs.Delete("trip_records/TRIP001")
	h.sweep(p)
	h.fs.Set("trip_records/X1", map[string]any{"status": "in_transit", "jobType": "line_haul", "driverId": "uid_alpha",
		"renamedFromTripId": "TRIP001", "createdAt": ts(at), "updatedAt": ts(at)})
	h.sweep(p)
	if v := h.scalar(`SELECT legacy_doc_id || ' ' || trip_no || ' ' || legacy_driver_ref FROM trip_records WHERE id = $1`, trip1); v != "TRIP001 TRIP001 uid_own" {
		t.Fatalf("the orphaned trip: %v", v)
	}
	if n := h.scalar(`SELECT count(*) FROM trip_records WHERE legacy_doc_id = 'X1' AND id <> $1`, trip1); n != int64(1) {
		t.Fatalf("the claim's own row: %v", n)
	}
	if n := h.scalar(`SELECT count(*) FROM trip_no_history WHERE old_trip_no = 'TRIP001' OR trip_id = $1`, trip1); n != int64(0) {
		t.Fatalf("history written for a claim: %v", n)
	}
	if n := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE doc_path = 'trip_records/X1' AND field = 'renamedFromTripId'
		AND reason_code = 'trip_no_mismatch' AND resolved_at IS NULL`); n != int64(1) {
		t.Fatalf("the claim's finding: %v", n)
	}

	// A genuine rename of uid_alpha's TRIP002, and in the same sweep a claim on it by uid_own that sorts first.
	trip2 := h.id(`SELECT id FROM trip_records WHERE legacy_doc_id = 'TRIP002'`)
	h.fs.Rename("trip_records/TRIP002", "trip_records/TRIP002X", map[string]any{"spxTripId": "TRIP002X", "renamedFromTripId": "TRIP002",
		"renamedAt": ts(at), "renamedBy": "uid_admin", "updatedAt": ts(at)})
	h.fs.Set("trip_records/AAA1", map[string]any{"status": "in_transit", "jobType": "first_mile", "driverId": "uid_own",
		"renamedFromTripId": "TRIP002", "createdAt": ts(at), "updatedAt": ts(at)})
	h.sweep(p)
	if v := h.scalar(`SELECT legacy_doc_id || ' ' || trip_no FROM trip_records WHERE id = $1`, trip2); v != "TRIP002X TRIP002X" {
		t.Fatalf("the genuine rename: %v", v)
	}
	if v := h.scalar(`SELECT string_agg(old_trip_no || '>' || new_trip_no, ',') FROM trip_no_history WHERE trip_id = $1`, trip2); v != "TRIP002>TRIP002X" {
		t.Fatalf("history of the genuine rename: %v", v)
	}
	if n := h.scalar(`SELECT count(*) FROM trip_records WHERE legacy_doc_id = 'AAA1' AND id <> $1`, trip2); n != int64(1) {
		t.Fatalf("the racing claim's own row: %v", n)
	}
	if n := h.scalar(`SELECT count(*) FROM trip_no_history h JOIN trip_records r ON r.id = h.trip_id WHERE r.legacy_doc_id IN ('X1', 'AAA1')`); n != int64(0) {
		t.Fatalf("history of the claims: %v", n)
	}
}

// Removed is judged only on what the sweep's snapshot can see: a sweep that read before a holiday was created does not
// remove the row a newer sweep committed, and one that read before a holiday was deleted does not bring back the row a
// newer sweep removed.
func TestRemovedFromAnOlderSnapshot(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	// ho_2 is written within the look-back, so the older sweep below compares it.
	h.fs.Merge("holidays/ho_2", map[string]any{"name": "Company outing (moved)"})
	h.sweep(p)

	before := h.readTime()
	h.fs.Set("holidays/ho_new", map[string]any{"name": "Office move", "date": ts(time.Date(2026, 12, 4, 17, 0, 0, 0, time.UTC)),
		"type": "COMPANY", "status": "DRAFT", "isRecurring": false})
	h.fs.Delete("holidays/ho_2")
	rep := h.sweep(p)
	removed := 0
	for _, c := range rep.Collections {
		if c.Name == "holidays" {
			removed = c.Removed
		}
	}
	if removed != 1 {
		t.Fatalf("the newer sweep: %+v", rep.Collections)
	}
	id := h.scalar(`SELECT id::text FROM holidays WHERE legacy_doc_id = 'ho_new'`)

	rep, err := h.e.Mirror(h.ctx, pinned{h.client, before}, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Collections {
		if c.Name == "holidays" && (c.Removed != 0 || applied(rep, "holidays") != 0) {
			t.Fatalf("the older sweep: %+v", c)
		}
	}
	if v := h.scalar(`SELECT coalesce(string_agg(id::text, ','), '') FROM holidays WHERE legacy_doc_id = 'ho_new'`); v != id {
		t.Fatalf("ho_new after the older sweep: %v (was %v)", v, id)
	}
	if n := h.scalar(`SELECT count(*) FROM holidays WHERE legacy_doc_id = 'ho_2'`); n != int64(0) {
		t.Fatalf("ho_2 came back: %v", n)
	}
	// A holiday created again at the removed path is newer than the tombstone and loads.
	h.fs.Set("holidays/ho_2", map[string]any{"name": "Company outing", "date": ts(time.Date(2026, 12, 11, 17, 0, 0, 0, time.UTC)),
		"type": "COMPANY", "status": "DRAFT", "isRecurring": false})
	h.sweep(p)
	if v := h.scalar(`SELECT (SELECT count(*) FROM holidays WHERE legacy_doc_id = 'ho_2')::text || ' ' || status FROM etl.source_docs
		WHERE doc_path = 'holidays/ho_2'`); v != "1 loaded" {
		t.Fatalf("ho_2 created again: %v", v)
	}
}

// The daily reconciliation compares the snapshot its sweep applied: the 3.x app's writes after that read (a GPS
// update, a heartbeat, a task edit) are neither "not mirrored yet" nor differences, while a row changed behind the
// mirror's back still is. A hand-run reconciliation without the read time stays strict.
func TestReconcileComparesTheSweepsSnapshot(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	rep := h.sweep(p)
	if rep.ReadAt.IsZero() {
		t.Fatal("a sweep over the Firestore client has a read time")
	}
	now := h.readTime()
	h.fs.Merge("vehicle_locations/GPS1", map[string]any{"speed": int64(55), "updatedAt": ts(now)})
	h.fs.Merge("drivers/drv_own/mobile_installations/inst_own1", map[string]any{"lastSeenAt": ts(now)})
	h.fs.Merge("tasks/task_1", map[string]any{"parcelNote": "late", "updatedAt": ts(now)})

	rec, err := h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Match {
		t.Fatal("a strict reconciliation now must see the writes after the sweep as not mirrored yet")
	}
	rec, err = h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p, ReadAt: rep.ReadAt})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Match {
		t.Fatalf("the sweep's snapshot: %+v", rec.Mismatches())
	}
	h.exec(`UPDATE trip_records SET status = 'cancelled' WHERE legacy_doc_id = 'TRIP001'`)
	rec, err = h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p, ReadAt: rep.ReadAt})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range rec.Mismatches() {
		found = found || (c.Name == "ops_fields" && strings.Contains(c.Note, "trip_records/TRIP001 (status)"))
	}
	if rec.Match || !found {
		t.Fatalf("a tampered row at the snapshot: %+v", rec.Mismatches())
	}
	if _, err := h.e.ReconcileSync(h.ctx, DumpSource{}, SyncReconcileOptions{Mirror: p, ReadAt: rep.ReadAt}); err == nil {
		t.Fatal("a read time over a source without one must be refused")
	}
}

// A listener that applies a newer version while the reconciliation runs (between its read and its comparison) moves
// the row past the snapshot: the document is skipped, not reported as a difference.
func TestReconcileSkipsARowAListenerMovedOn(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	now := h.readTime()
	h.fs.Set("trip_records/TRIPR", map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
		"driverId": "uid_own", "createdAt": ts(now), "updatedAt": ts(now)})
	rep := h.sweep(p)
	cursors := h.cursors(p)
	h.fs.Merge("trip_records/TRIPR", map[string]any{"status": "incident", "updatedAt": ts(now.Add(time.Second))})
	if r, _ := h.poll(h.client, p, cursors); applied(r, "trip_records") != 1 {
		t.Fatalf("the listener: %+v", r.Collections)
	}
	rec, err := h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p, ReadAt: rep.ReadAt})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Match {
		t.Fatalf("a row moved on since the read: %+v", rec.Mismatches())
	}
	i := slices.IndexFunc(rec.Checks, func(c Check) bool { return c.Name == "ops_fields" && c.Key == "trip_records" })
	if i < 0 || !strings.Contains(rec.Checks[i].Note, "1 moved on since the read") {
		t.Fatalf("ops_fields trip_records: %+v", rec.Checks)
	}
}
