package hr

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
)

// The Firestore write of a decision is what leave-requests/page.tsx:117-129 wrote: upper-case status and updatedAt;
// approverId (when the staff member has a Firebase uid) and approvedAt on an approval or a rejection; the rejection's
// reason; a cancel writes the status alone.
func TestLegacyLeaveDecision(t *testing.T) {
	now := time.Date(2026, 10, 12, 2, 30, 0, 0, time.FixedZone("ICT", 7*3600))
	uid, blank, reason := "uid_admin", "  ", "งานเร่ง"
	cases := []struct {
		name          string
		status        string
		approver, why *string
		want          map[string]any
	}{
		{"approve", LeaveApproved, &uid, nil, map[string]any{"status": "APPROVED", "updatedAt": now.UTC(), "approverId": "uid_admin",
			"approvedAt": now.UTC()}},
		{"approve without a Firebase uid", LeaveApproved, &blank, nil, map[string]any{"status": "APPROVED", "updatedAt": now.UTC(),
			"approvedAt": now.UTC()}},
		{"reject", LeaveRejected, &uid, &reason, map[string]any{"status": "REJECTED", "updatedAt": now.UTC(), "approverId": "uid_admin",
			"approvedAt": now.UTC(), "rejectionReason": "งานเร่ง"}},
		{"cancel", LeaveCancelled, &uid, nil, map[string]any{"status": "CANCELLED", "updatedAt": now.UTC()}},
	}
	for _, tc := range cases {
		if got := LegacyLeaveDecision(tc.status, tc.approver, tc.why, now); !maps.Equal(got, tc.want) {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
	for in, want := range map[any]string{"PENDING": "pending", " Approved ": "approved", "cancelled": "cancelled", "DONE": "", nil: "", 3: ""} {
		if got := LegacyLeaveStatus(in); got != want {
			t.Errorf("LegacyLeaveStatus(%v) = %q", in, got)
		}
	}
}

// FirestoreLeaveDocs over the REST client (the in-process Firestore): a masked PATCH under the update-time precondition,
// timestamps as Firestore timestamps, nil deletes, a stale or missing document is ErrDocumentChanged.
func TestFirestoreLeaveDocs(t *testing.T) {
	ctx := context.Background()
	b := gcptest.New(t, "logitrack-test")
	docs := FirestoreLeaveDocs{FS: b.Firestore()}
	b.Set("leave_requests/lv_1", map[string]any{"status": "PENDING", "driverId": "drv_1", "note": "x"})
	f, at, ok, err := docs.Get(ctx, "lv_1")
	if err != nil || !ok || f["status"] != "PENDING" || at.IsZero() {
		t.Fatalf("get %v %v %v %v", f, at, ok, err)
	}
	if _, _, ok, err := docs.Get(ctx, "lv_none"); ok || err != nil {
		t.Fatalf("missing %v %v", ok, err)
	}
	now := time.Date(2026, 10, 12, 2, 30, 0, 0, time.UTC)
	written, err := docs.Update(ctx, "lv_1", map[string]any{"status": "APPROVED", "approvedAt": now, "note": nil}, at)
	if err != nil || !written.After(at) {
		t.Fatalf("update %v %v", written, err)
	}
	d, _ := b.Doc("leave_requests/lv_1")
	if d.Fields["status"] != "APPROVED" || d.Fields["approvedAt"] != (dump.Timestamp{Time: now}) || d.Fields["driverId"] != "drv_1" {
		t.Errorf("document %v", d.Fields)
	}
	if _, ok := d.Fields["note"]; ok {
		t.Error("a nil value did not delete its field")
	}
	if !d.UpdateTime.Equal(written) {
		t.Errorf("update time %v, written %v", d.UpdateTime, written)
	}
	if _, err := docs.Update(ctx, "lv_1", map[string]any{"status": "REJECTED"}, at); !errors.Is(err, ErrDocumentChanged) {
		t.Errorf("stale update: %v", err)
	}
	if _, err := docs.Update(ctx, "lv_none", map[string]any{"status": "REJECTED"}, at); !errors.Is(err, ErrDocumentChanged) {
		t.Errorf("missing document: %v", err)
	}
	if _, err := docs.Update(ctx, "lv_1", map[string]any{"x": []int{1}}, written); err == nil {
		t.Error("an unsupported value was written")
	}
}
