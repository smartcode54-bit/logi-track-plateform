//go:build integration

package notify_test

import (
	"maps"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// leave.decided (T45): leave_decided to the devices of the request's driver (read from the row, never from the
// message), naming the decision the event carries; the PostgreSQL path and the write-back's event (origin=writeback)
// push alike, a request without a driver sends nothing, and an unknown request or decision is a permanent error.
func TestFCMLeavePushes(t *testing.T) {
	e := newFCMEnv(t)
	drv, u := e.driver("leave@logitrack.test", "fsDriverLeave")
	other, uo := e.driver("other@logitrack.test", "")
	e.device(u, "inst-l", "tok-l", "android")
	e.device(uo, "inst-o", "tok-o", "android")
	_ = other
	leave := scan[uuid.UUID](e, `INSERT INTO leave_requests (tenant_id, tenant_source, driver_id, leave_type, status, start_date,
		end_date, legacy_doc_id) VALUES ($1, 'driver', $2, 'business', 'approved', '2026-10-12', '2026-10-13', 'fsLeave1') RETURNING id`,
		e.tenant, drv)

	e.handle(e.delivery(notify.RouteLeaveDecided, map[string]any{"entity": "leave_request", "id": leave, "status": "approved",
		"driverId": drv}))
	got := e.fcm.SentTo("tok-l")
	if len(got) != 1 || len(e.fcm.SentTo("tok-o")) != 0 {
		t.Fatalf("pushes %d / %d", len(got), len(e.fcm.SentTo("tok-o")))
	}
	m := got[0].Message
	want := map[string]string{"type": "leave_decided", "leaveRequestId": "fsLeave1", "driverId": "fsDriverLeave", "status": "approved"}
	if !maps.Equal(m.Data, want) || m.Notification.Title != "อนุมัติการลาแล้ว" || m.Notification.Body != "ลากิจ 12/10/2569 - 13/10/2569" ||
		m.Android.Notification.ChannelID != notify.ChannelTaskAssignments {
		t.Fatalf("push %s", got[0].Raw)
	}
	// The write-back's event (the decision is in Firestore, the mirror has not loaded it yet): pushed for its decision.
	d := e.delivery(notify.RouteLeaveDecided, map[string]any{"id": leave, "status": "cancelled"})
	d.Headers = map[string]any{"origin": "writeback"}
	e.handle(d)
	if got := e.fcm.SentTo("tok-l"); len(got) != 2 || got[1].Message.Notification.Title != "ยกเลิกการลาแล้ว" {
		t.Fatalf("write-back push %+v", got)
	}
	// The mirror's re-telling (origin=etl.sync) never pushes a second time.
	d = e.delivery(notify.RouteLeaveDecided, map[string]any{"id": leave, "status": "cancelled"})
	d.Headers = map[string]any{"origin": "etl.sync"}
	e.handle(d)
	if n := len(e.fcm.SentTo("tok-l")); n != 2 {
		t.Fatalf("a mirror event pushed (%d)", n)
	}
	if count(e.rows(), notify.KindLeaveDecided, notify.StatusSent) != 2 {
		t.Fatalf("delivery rows %+v", e.rows())
	}
	// A quarantined request has no driver: nothing to send.
	orphan := scan[uuid.UUID](e, `INSERT INTO leave_requests (tenant_id, tenant_source, leave_type, status, start_date, end_date)
		VALUES (app_quarantine_tenant_id(), 'quarantine', 'sick', 'pending', '2026-10-01', '2026-10-01') RETURNING id`)
	e.handle(e.delivery(notify.RouteLeaveDecided, map[string]any{"id": orphan, "status": "rejected"}))
	if n := len(e.fcm.Sent()); n != 2 {
		t.Fatalf("an orphan pushed (%d)", n)
	}
	for _, body := range []map[string]any{{"id": uuid.New(), "status": "approved"}, {"id": leave, "status": "pending"}, {"status": "approved"}} {
		if err := e.consumer.Handle(e.ctx, e.delivery(notify.RouteLeaveDecided, body)); !mq.IsPermanent(err) {
			t.Errorf("%v: %v, want a permanent error", body, err)
		}
	}
}
