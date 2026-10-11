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
