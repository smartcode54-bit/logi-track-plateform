package hr

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
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

// racer plays a concurrent writer (the driver's app, another staff member): between runs after every Get that found
// the document, before the decision commits.
type racer struct {
	writeback.Writer
	between func(docID string)
}

func (r racer) Get(ctx context.Context, collection, docID string) (map[string]any, time.Time, bool, error) {
	f, at, ok, err := r.Writer.Get(ctx, collection, docID)
	if ok && err == nil && r.between != nil {
		r.between(docID)
	}
	return f, at, ok, err
}

// refusal returns the status and the details of an API error.
func refusal(t *testing.T, err error) (int, map[string]any) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) {
		t.Fatalf("not an API error: %v", err)
	}
	return he.Status, he.Details
}

// The write of a decision through T31's writer (writeback.Memory): the transition is checked against the document's own
// status, the legacy fields are committed under a precondition on the update time read, and the answer maps the
// writer's outcomes: a document changed or vanished between the read and the commit (writeback.ErrConflict) and a
// missing document are 409 document_changed with nothing written, a refused transition 409 transition_not_allowed,
// Firestore down 503 write_back.
func TestLeaveWriteBackWrite(t *testing.T) {
	ctx := context.Background()
	m := writeback.NewMemory()
	var commits []writeback.DocWrite
	m.OnCommit = func(ws []writeback.DocWrite, _ time.Time) { commits = append(commits, ws...) }
	read := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	app := map[string]any{"driverId": "drv_1", "type": "SICK", "status": "PENDING", "reason": "ไข้"}
	uid, why := "uid_admin", "งานเร่ง"
	now := time.Date(2026, 10, 12, 2, 30, 0, 0, time.UTC)
	approve, reject, cancel := leaveMoves[ActionApprove], leaveMoves[ActionReject], leaveMoves[ActionCancel]
	wb := &LeaveWriteBack{Writer: m}

	// A rejection: the legacy fields merged into the app's document, under the update time read.
	m.Put(LeaveCollection, "lv_1", app, read)
	from, fields, at, err := wb.write(ctx, "lv_1", LeavePending, reject, &uid, &why, now)
	if err != nil || from != LeavePending || at.IsZero() {
		t.Fatalf("reject: %q %v %v", from, at, err)
	}
	if !maps.Equal(fields, LegacyLeaveDecision(LeaveRejected, &uid, &why, now)) {
		t.Errorf("fields %v", fields)
	}
	if len(commits) != 1 || commits[0].Collection != LeaveCollection || commits[0].DocID != "lv_1" || !commits[0].UpdateTime.Equal(read) ||
		commits[0].Replace || commits[0].Delete {
		t.Fatalf("commit %+v", commits)
	}
	d, upd, _, _ := m.Get(ctx, LeaveCollection, "lv_1")
	if d["status"] != "REJECTED" || d["rejectionReason"] != why || d["approverId"] != uid || d["driverId"] != "drv_1" ||
		d["reason"] != "ไข้" || d["approvedAt"] != now || !upd.Equal(at) {
		t.Errorf("document %v at %v", d, upd)
	}

	// The document's status decides, not the row's: REJECTED refuses an approval (the row may still say pending).
	if _, _, _, err := wb.write(ctx, "lv_1", LeavePending, approve, &uid, nil, now); err == nil {
		t.Fatal("approved a rejected document")
	} else if st, det := refusal(t, err); st != http.StatusConflict || det["reason"] != ReasonTransitionNotAllowed ||
		det["status"] != LeaveRejected || det["to"] != LeaveApproved {
		t.Errorf("transition %d %v", st, det)
	}

	// Changed between the read and the commit: the precondition refuses it, nothing is written.
	m.Put(LeaveCollection, "lv_2", app, read)
	race := &LeaveWriteBack{Writer: racer{Writer: m, between: func(id string) {
		f, _ := m.Doc(LeaveCollection, id)
		f["note"] = "app"
		m.Put(LeaveCollection, id, f, read.Add(time.Second))
	}}}
	before := len(commits)
	if _, _, _, err := race.write(ctx, "lv_2", LeavePending, approve, &uid, nil, now); err == nil {
		t.Fatal("a raced write was accepted")
	} else if st, det := refusal(t, err); st != http.StatusConflict || det["reason"] != ReasonDocumentChanged || det["status"] != LeavePending {
		t.Errorf("raced %d %v", st, det)
	}
	if f, _ := m.Doc(LeaveCollection, "lv_2"); f["status"] != "PENDING" || f["note"] != "app" || len(commits) != before {
		t.Errorf("a raced write was applied: %v", f)
	}

	// Vanished between the read and the commit: document_changed as well, and nothing is created.
	m.Put(LeaveCollection, "lv_3", app, read)
	gone := &LeaveWriteBack{Writer: racer{Writer: m, between: func(id string) {
		if _, err := m.Commit(ctx, writeback.DocWrite{Collection: LeaveCollection, DocID: id, Delete: true}); err != nil {
			t.Fatal(err)
		}
	}}}
	before = len(commits)
	if _, _, _, err := gone.write(ctx, "lv_3", LeavePending, approve, &uid, nil, now); err == nil {
		t.Fatal("a write to a vanished document was accepted")
	} else if st, det := refusal(t, err); st != http.StatusConflict || det["reason"] != ReasonDocumentChanged {
		t.Errorf("vanished %d %v", st, det)
	}
	if _, ok := m.Doc(LeaveCollection, "lv_3"); ok || len(commits) != before+1 { // the racer's delete only
		t.Errorf("a vanished document was written: %v, %d commits", ok, len(commits)-before)
	}

	// Missing when read: document_changed with the row's status.
	if _, _, _, err := wb.write(ctx, "lv_none", LeaveApproved, cancel, &uid, nil, now); err == nil {
		t.Fatal("a missing document was written")
	} else if st, det := refusal(t, err); st != http.StatusConflict || det["reason"] != ReasonDocumentChanged || det["status"] != LeaveApproved {
		t.Errorf("missing %d %v", st, det)
	}

	// A read without an update time would write without a precondition: refused (503), nothing written.
	m.Put(LeaveCollection, "lv_4", app, time.Time{})
	before = len(commits)
	if _, _, _, err := wb.write(ctx, "lv_4", LeavePending, approve, &uid, nil, now); err == nil {
		t.Fatal("wrote without a precondition")
	} else if st, det := refusal(t, err); st != http.StatusServiceUnavailable || det["reason"] != "write_back" || len(commits) != before {
		t.Errorf("no update time %d %v", st, det)
	}

	// Firestore down: 503 unavailable, write_back.
	m.Put(LeaveCollection, "lv_5", app, read)
	m.Fail(errors.New("firestore down"))
	if _, _, _, err := wb.write(ctx, "lv_5", LeavePending, approve, &uid, nil, now); err == nil {
		t.Fatal("wrote while Firestore is down")
	} else if st, det := refusal(t, err); st != http.StatusServiceUnavailable || det["reason"] != "write_back" {
		t.Errorf("down %d %v", st, det)
	}
	m.Fail(nil)

	// A cancel writes the status alone (and updatedAt), as the legacy page did.
	m.Put(LeaveCollection, "lv_6", map[string]any{"status": "APPROVED", "approverId": "uid_x"}, read)
	if _, fields, _, err := wb.write(ctx, "lv_6", LeaveApproved, cancel, &uid, nil, now); err != nil || len(fields) != 2 {
		t.Fatalf("cancel %v %v", fields, err)
	}
	if f, _ := m.Doc(LeaveCollection, "lv_6"); f["status"] != "CANCELLED" || f["approverId"] != "uid_x" {
		t.Errorf("cancelled document %v", f)
	}
}

// The same write through writeback.Firestore, the writer the api builds, over the REST client (the in-process
// Firestore): a masked commit under currentDocument.updateTime, timestamps as Firestore timestamps, the app's fields
// kept, the commit time the document's update time; a stale or vanished document is document_changed (Firestore
// answers FAILED_PRECONDITION, or NOT_FOUND "No document to update", both writeback.ErrConflict).
func TestLeaveWriteBackWriteFirestore(t *testing.T) {
	ctx := context.Background()
	b := gcptest.New(t, "logitrack-test")
	w := writeback.Firestore{FS: b.Firestore()}
	wb := &LeaveWriteBack{Writer: w}
	uid, why := "uid_admin", "งานเร่ง"
	now := time.Date(2026, 10, 12, 2, 30, 0, 0, time.UTC)
	b.Set("leave_requests/lv_1", map[string]any{"status": "PENDING", "driverId": "drv_1", "note": "x"})
	from, _, at, err := wb.write(ctx, "lv_1", LeavePending, leaveMoves[ActionReject], &uid, &why, now)
	if err != nil || from != LeavePending {
		t.Fatalf("reject %q %v", from, err)
	}
	d, _ := b.Doc("leave_requests/lv_1")
	if d.Fields["status"] != "REJECTED" || d.Fields["approvedAt"] != (dump.Timestamp{Time: now}) ||
		d.Fields["updatedAt"] != (dump.Timestamp{Time: now}) || d.Fields["rejectionReason"] != why || d.Fields["approverId"] != uid ||
		d.Fields["driverId"] != "drv_1" || d.Fields["note"] != "x" {
		t.Errorf("document %v", d.Fields)
	}
	if !d.UpdateTime.Equal(at) {
		t.Errorf("update time %v, written %v", d.UpdateTime, at)
	}
	// Stale: another writer between the read and the commit.
	b.Set("leave_requests/lv_2", map[string]any{"status": "PENDING"})
	race := &LeaveWriteBack{Writer: racer{Writer: w, between: func(id string) { b.Merge("leave_requests/"+id, map[string]any{"note": "app"}) }}}
	if _, _, _, err := race.write(ctx, "lv_2", LeavePending, leaveMoves[ActionApprove], &uid, nil, now); err == nil {
		t.Fatal("a stale write was accepted")
	} else if st, det := refusal(t, err); st != http.StatusConflict || det["reason"] != ReasonDocumentChanged {
		t.Errorf("stale %d %v", st, det)
	}
	if d, _ := b.Doc("leave_requests/lv_2"); d.Fields["status"] != "PENDING" {
		t.Errorf("a stale write was applied: %v", d.Fields)
	}
	// Vanished: deleted between the read and the commit; nothing is created.
	b.Set("leave_requests/lv_3", map[string]any{"status": "PENDING"})
	gone := &LeaveWriteBack{Writer: racer{Writer: w, between: func(id string) { b.Delete("leave_requests/" + id) }}}
	if _, _, _, err := gone.write(ctx, "lv_3", LeavePending, leaveMoves[ActionApprove], &uid, nil, now); err == nil {
		t.Fatal("a write to a vanished document was accepted")
	} else if st, det := refusal(t, err); st != http.StatusConflict || det["reason"] != ReasonDocumentChanged {
		t.Errorf("vanished %d %v", st, det)
	}
	if _, ok := b.Doc("leave_requests/lv_3"); ok {
		t.Error("a vanished document was created again")
	}
	// Missing when read.
	if _, _, _, err := wb.write(ctx, "lv_none", LeavePending, leaveMoves[ActionApprove], &uid, nil, now); err == nil {
		t.Fatal("a missing document was written")
	} else if st, det := refusal(t, err); st != http.StatusConflict || det["reason"] != ReasonDocumentChanged {
		t.Errorf("missing %d %v", st, det)
	}
}
