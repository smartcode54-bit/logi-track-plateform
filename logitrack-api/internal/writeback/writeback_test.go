package writeback_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The Firestore writer against the in-process fake Google (no network): a masked PATCH that keeps the fields it does
// not name, deletes a field sent as nil, and stores instants as timestamps.
func TestFirestoreMerge(t *testing.T) {
	ctx := context.Background()
	b := gcptest.New(t, "writeback-test")
	at := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	b.Put(dump.Doc{ID: "e1", Path: "vehicle_expenses/e1", Fields: map[string]any{"driverId": "uid1", "status": "PENDING",
		"receiptPhotoUrl": "https://example.test/r.jpg", "adminNote": "old"}})
	w := writeback.Firestore{FS: b.Firestore()}
	if err := w.Merge(ctx, "vehicle_expenses", "e1", map[string]any{"status": "APPROVED", "adminNote": nil,
		"updatedAt": at, "amount": 45.5, "odometer": int32(98500)}); err != nil {
		t.Fatal(err)
	}
	d, ok := b.Doc("vehicle_expenses/e1")
	if !ok {
		t.Fatal("document missing")
	}
	if d.Fields["status"] != "APPROVED" || d.Fields["driverId"] != "uid1" || d.Fields["receiptPhotoUrl"] == nil ||
		d.Fields["amount"] != 45.5 || d.Fields["odometer"] != int64(98500) {
		t.Fatalf("fields %v", d.Fields)
	}
	if _, ok := d.Fields["adminNote"]; ok {
		t.Error("a nil field is deleted")
	}
	if ts, ok := d.Fields["updatedAt"].(dump.Timestamp); !ok || !ts.Equal(at) {
		t.Errorf("updatedAt %#v", d.Fields["updatedAt"])
	}
	// A document that does not exist yet is created (the minted id of a row born in PostgreSQL, R25).
	if err := w.Merge(ctx, "maintenance", "0199-new", map[string]any{"status": "PM Booking"}); err != nil {
		t.Fatal(err)
	}
	if d, ok := b.Doc("maintenance/0199-new"); !ok || d.Fields["status"] != "PM Booking" {
		t.Fatalf("created %v %v", ok, d.Fields)
	}
	if err := w.Merge(ctx, "maintenance", "x", map[string]any{"bad": struct{}{}}); err == nil {
		t.Error("an unsupported value is refused")
	}
	if err := (writeback.Firestore{}).Merge(ctx, "maintenance", "x", nil); err == nil {
		t.Error("no client")
	}
}

func TestMemory(t *testing.T) {
	ctx := context.Background()
	m := writeback.NewMemory()
	if err := m.Merge(ctx, "maintenance", "m1", map[string]any{"status": "Scheduled", "notes": "a"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Merge(ctx, "maintenance", "m1", map[string]any{"notes": nil, "provider": "อู่ A"}); err != nil {
		t.Fatal(err)
	}
	d, _ := m.Doc("maintenance", "m1")
	if d["status"] != "Scheduled" || d["provider"] != "อู่ A" || d["notes"] != nil || len(d) != 2 {
		t.Fatalf("doc %v", d)
	}
	if len(m.Writes()) != 2 {
		t.Fatalf("writes %v", m.Writes())
	}
	boom := errors.New("firestore down")
	m.Fail(boom)
	if err := m.Merge(ctx, "maintenance", "m1", map[string]any{"status": "x"}); !errors.Is(err, boom) {
		t.Fatalf("fail: %v", err)
	}
	if d, _ := m.Doc("maintenance", "m1"); d["status"] != "Scheduled" {
		t.Error("a failed merge wrote")
	}
	m.Fail(nil)
	if err := m.Merge(ctx, "", "m1", nil); err == nil {
		t.Error("an empty collection is refused")
	}
	if err := m.Merge(ctx, "c", "d", map[string]any{"v": []int{1}}); err == nil {
		t.Error("an unsupported value is refused")
	}
}

// Commit against the in-process fake Google: one atomic batch of a create (refused when the document exists), a
// masked update (refused when the document is gone), a whole-document copy and a delete under the update time read;
// one failed precondition writes nothing. Arrays and maps are written as Firestore arrays and maps.
func TestFirestoreCommit(t *testing.T) {
	ctx := context.Background()
	b := gcptest.New(t, "writeback-test")
	read := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	b.Put(dump.Doc{ID: "OLD", Path: "trip_records/OLD", CreateTime: read, UpdateTime: read,
		Fields: map[string]any{"status": "delivered", "sealCode": "S1"}})
	b.Put(dump.Doc{ID: "inc1", Path: "incidentReport/inc1", CreateTime: read, UpdateTime: read,
		Fields: map[string]any{"tripId": "OLD", "description": "x"}})
	w := writeback.Firestore{FS: b.Firestore()}
	fields, updated, ok, err := w.Get(ctx, "trip_records", "OLD")
	if err != nil || !ok || fields["sealCode"] != "S1" || !updated.Equal(read) {
		t.Fatalf("get: %v %v %v", fields, ok, err)
	}
	copied := map[string]any{"status": "delivered", "sealCode": "S1", "spxTripId": "NEW", "renamedFromTripId": "OLD"}
	at, err := w.Commit(ctx,
		writeback.DocWrite{Collection: "trip_records", DocID: "NEW", Fields: copied, Replace: true, MustNotExist: true},
		writeback.DocWrite{Collection: "incidentReport", DocID: "inc1", Fields: map[string]any{"tripId": "NEW"}, MustExist: true},
		writeback.DocWrite{Collection: "trip_records", DocID: "OLD", Delete: true, UpdateTime: updated},
		writeback.DocWrite{Collection: "tasks", DocID: "t1", MustNotExist: true, Fields: map[string]any{
			"deliveryStops":   []any{map[string]any{"index": int64(1), "destination": "SOCE", "addedAt": time.Unix(0, 0)}},
			"helperDriverIds": []any{"uid1"}, "note": nil}})
	if err != nil || at.IsZero() {
		t.Fatalf("commit: %v %v", at, err)
	}
	if _, ok := b.Doc("trip_records/OLD"); ok {
		t.Fatal("the old document is still there")
	}
	nd, _ := b.Doc("trip_records/NEW")
	inc, _ := b.Doc("incidentReport/inc1")
	task, _ := b.Doc("tasks/t1")
	if nd.Fields["spxTripId"] != "NEW" || !nd.UpdateTime.Equal(at) || inc.Fields["tripId"] != "NEW" || inc.Fields["description"] != "x" {
		t.Fatalf("committed %v %v", nd, inc)
	}
	stops, _ := task.Fields["deliveryStops"].([]any)
	if len(stops) != 1 || stops[0].(map[string]any)["destination"] != "SOCE" || len(task.Fields["helperDriverIds"].([]any)) != 1 {
		t.Fatalf("arrays and maps %v", task.Fields)
	}
	if _, ok := task.Fields["note"]; ok {
		t.Fatal("a nil field is written")
	}
	// A failed precondition: nothing of the batch is written.
	before := len(b.Writes())
	_, err = w.Commit(ctx,
		writeback.DocWrite{Collection: "tasks", DocID: "t2", Fields: map[string]any{"a": "b"}, MustNotExist: true},
		writeback.DocWrite{Collection: "tasks", DocID: "t1", Fields: map[string]any{"a": "b"}, MustNotExist: true})
	if !errors.Is(err, writeback.ErrConflict) || len(b.Writes()) != before {
		t.Fatalf("a refused commit: %v, %d writes", err, len(b.Writes())-before)
	}
	if _, ok := b.Doc("tasks/t2"); ok {
		t.Fatal("a refused commit wrote")
	}
	if _, err := w.Commit(ctx, writeback.DocWrite{Collection: "tasks", DocID: "gone", MustExist: true, Fields: map[string]any{"a": "b"}}); !errors.Is(err, writeback.ErrConflict) {
		t.Fatalf("an update of a missing document: %v", err)
	}
	if _, err := w.Commit(ctx, writeback.DocWrite{Collection: "trip_records", DocID: "NEW", UpdateTime: updated, Fields: map[string]any{"a": "b"}}); !errors.Is(err, writeback.ErrConflict) {
		t.Fatalf("a stale read-modify-write: %v", err)
	}
	if _, _, ok, err := w.Get(ctx, "tasks", "none"); ok || err != nil {
		t.Fatalf("missing get: %v %v", ok, err)
	}
	if _, err := w.Commit(ctx); err == nil {
		t.Fatal("an empty commit")
	}
}

func TestMemoryCommit(t *testing.T) {
	ctx := context.Background()
	m := writeback.NewMemory()
	var hooked []writeback.DocWrite
	m.OnCommit = func(ws []writeback.DocWrite, _ time.Time) { hooked = append(hooked, ws...) }
	at1, err := m.Commit(ctx, writeback.DocWrite{Collection: "tasks", DocID: "a", Fields: map[string]any{"x": "1"}, MustNotExist: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(ctx, writeback.DocWrite{Collection: "tasks", DocID: "a", Fields: map[string]any{"x": "2"}, MustNotExist: true}); !errors.Is(err, writeback.ErrConflict) {
		t.Fatalf("create of an existing document: %v", err)
	}
	if _, err := m.Commit(ctx, writeback.DocWrite{Collection: "tasks", DocID: "a", Fields: map[string]any{"y": "2"}, UpdateTime: at1.Add(time.Second)}); !errors.Is(err, writeback.ErrConflict) {
		t.Fatalf("stale update time: %v", err)
	}
	at2, err := m.Commit(ctx, writeback.DocWrite{Collection: "tasks", DocID: "a", Fields: map[string]any{"y": "2"}, UpdateTime: at1},
		writeback.DocWrite{Collection: "tasks", DocID: "b", Delete: true})
	if err != nil || !at2.After(at1) {
		t.Fatalf("second commit %v %v", at2, err)
	}
	f, at, ok, _ := m.Get(ctx, "tasks", "a")
	if !ok || f["x"] != "1" || f["y"] != "2" || !at.Equal(at2) || len(hooked) != 3 {
		t.Fatalf("doc %v %v hooked %d", f, at, len(hooked))
	}
	m.Fail(errors.New("down"))
	if _, err := m.Commit(ctx, writeback.DocWrite{Collection: "tasks", DocID: "c", Fields: map[string]any{}}); err == nil {
		t.Fatal("a failing memory committed")
	}
}
