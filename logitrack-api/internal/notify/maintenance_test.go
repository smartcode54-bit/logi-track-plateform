package notify

import "testing"

// The legacy reminder text of the maintenance page (maintenance.ts:45-52): each part only when it has a value.
func TestMaintenanceReminderBody(t *testing.T) {
	for _, tc := range []struct{ plate, day, at, place, want string }{
		{"กข-1234", "2026-10-12", "09:30", "อู่ A", "รถ กข-1234 วันที่ 2026-10-12 เวลา 09:30 ที่ อู่ A — กรุณาเข้าอู่ตามนัด"},
		{"กข-1234", "2026-10-12", "", "", "รถ กข-1234 วันที่ 2026-10-12 — กรุณาเข้าอู่ตามนัด"},
		{"", "", "", "", "รถ  — กรุณาเข้าอู่ตามนัด"},
	} {
		if got := MaintenanceReminderBody(tc.plate, tc.day, tc.at, tc.place); got != tc.want {
			t.Errorf("MaintenanceReminderBody(%q, %q, %q, %q) = %q, want %q", tc.plate, tc.day, tc.at, tc.place, got, tc.want)
		}
	}
}
