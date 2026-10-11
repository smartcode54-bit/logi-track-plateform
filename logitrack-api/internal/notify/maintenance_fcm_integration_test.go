//go:build integration

package notify_test

import (
	"maps"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
)

// maintenance.created / maintenance.reminder_requested (T40): maintenance_scheduled to the drivers of the record's
// truck, read server-side (the drivers of its active assignments and the driver holding it as the active truck; the
// legacy callable took the driver id from the browser), with the legacy title and body built from the record, or the
// staff's text. A PM booking (no appointment yet) and a record closed by the time the message is read push nothing.
func TestFCMMaintenancePushes(t *testing.T) {
	e := newFCMEnv(t)
	home, uh := e.driver("home@logitrack.test", "fsDriverHome")
	active, ua := e.driver("active@logitrack.test", "")
	other, uo := e.driver("other@logitrack.test", "")
	e.device(uh, "inst-h", "tok-h", "android")
	e.device(ua, "inst-a", "tok-a", "android")
	e.device(uo, "inst-o", "tok-o", "android")
	truck := scan[uuid.UUID](e, `INSERT INTO trucks (tenant_id, tenant_source, ownership_type, license_plate, province, status,
		brand, model, year, color, type_raw) VALUES ($1, 'self', 'own', 'กข-1234', 'กรุงเทพมหานคร', 'active', 'Isuzu', 'D-Max',
		2022, 'ขาว', '4 Wheels Jumbo') RETURNING id`, e.tenant)
	e.exec(`INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status)
		VALUES ($1, 'truck', $2, $3, 'active')`, e.tenant, truck, home)
	e.exec(`UPDATE drivers SET active_truck_id = $2 WHERE id = $1`, active, truck)
	_ = other
	record := func(status, legacy string) uuid.UUID {
		var leg any
		if legacy != "" {
			leg = legacy
		}
		return scan[uuid.UUID](e, `INSERT INTO maintenance_records (tenant_id, truck_id, maintenance_type, service_type,
			start_date, status, appointment_time, provider, legacy_doc_id)
			VALUES ($1, $2, 'PM', 'เช็คระยะ', '2026-10-12', $3, '09:30', 'อู่ A', $4) RETURNING id`, e.tenant, truck, status, leg)
	}

	booking := record("pm_booking", "")
	e.handle(e.delivery(notify.RouteMaintenanceCreated, map[string]any{"id": booking}))
	if n := len(e.fcm.Sent()); n != 0 {
		t.Fatalf("a PM booking pushed %d", n)
	}

	scheduled := record("scheduled", "fsMnt1")
	e.handle(e.delivery(notify.RouteMaintenanceCreated, map[string]any{"id": scheduled}))
	sent := e.fcm.Sent()
	if len(sent) != 2 || len(e.fcm.SentTo("tok-o")) != 0 {
		t.Fatalf("%d pushes, want one to each driver of the truck", len(sent))
	}
	for _, tok := range []string{"tok-h", "tok-a"} {
		got := e.fcm.SentTo(tok)
		if len(got) != 1 {
			t.Fatalf("%s: %d pushes", tok, len(got))
		}
		m := got[0].Message
		ref := "fsDriverHome"
		if tok == "tok-a" {
			ref = active.String()
		}
		want := map[string]string{"type": "maintenance_scheduled", "maintenanceId": "fsMnt1", "driverId": ref}
		if !maps.Equal(m.Data, want) || m.Notification.Title != "นัดเช็คระยะ" ||
			m.Notification.Body != "รถ กข-1234 วันที่ 2026-10-12 เวลา 09:30 ที่ อู่ A — กรุณาเข้าอู่ตามนัด" ||
			m.Android.Notification.ChannelID != notify.ChannelTaskAssignments {
			t.Fatalf("%s: %s", tok, got[0].Raw)
		}
	}

	// The staff's text; a record closed by then pushes nothing.
	e.handle(e.delivery(notify.RouteMaintenanceReminder, map[string]any{"id": booking, "title": "เข้าอู่", "body": "พรุ่งนี้"}))
	if got := e.fcm.SentTo("tok-h"); len(got) != 2 || got[1].Message.Notification.Title != "เข้าอู่" ||
		got[1].Message.Notification.Body != "พรุ่งนี้" || got[1].Message.Data["maintenanceId"] != booking.String() {
		t.Fatalf("reminder %+v", got)
	}
	e.exec(`UPDATE maintenance_records SET status = 'completed' WHERE id = $1`, scheduled)
	e.handle(e.delivery(notify.RouteMaintenanceReminder, map[string]any{"id": scheduled}))
	if n := len(e.fcm.Sent()); n != 4 {
		t.Fatalf("a closed record pushed (%d pushes)", n)
	}
	rows := e.rows()
	if count(rows, notify.KindMaintenanceScheduled, notify.StatusSent) != 4 {
		t.Fatalf("delivery rows %+v", rows)
	}
	if err := e.consumer.Handle(e.ctx, e.delivery(notify.RouteMaintenanceReminder, map[string]any{"id": uuid.New()})); err == nil {
		t.Fatal("an unknown record is not an error")
	}
}
