package maintenance

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance/maintenancedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// AC "Legacy maintenance statuses normalised to lower_snake": every literal the legacy web and APKs wrote maps onto
// the canonical vocabulary (the ETL's canon), and nothing outside it is accepted.
func TestNormalizeStatus(t *testing.T) {
	for in, want := range map[string]string{
		"PM Booking": StatusPMBooking, "pm_booking": StatusPMBooking, "Scheduled": StatusScheduled,
		"In-Progress": StatusInProgress, "in_progress": StatusInProgress, " IN PROGRESS ": StatusInProgress,
		"completed": StatusCompleted, "Completed": StatusCompleted, "cancelled": StatusCancelled,
	} {
		got, ok := NormalizeStatus(in)
		if !ok || got != want {
			t.Errorf("NormalizeStatus(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "done", "canceled", "PM-Bookings"} {
		if _, ok := NormalizeStatus(in); ok {
			t.Errorf("NormalizeStatus(%q) accepted", in)
		}
	}
	for status, legacy := range map[string]string{StatusPMBooking: "PM Booking", StatusScheduled: "Scheduled",
		StatusInProgress: "In-Progress", StatusCompleted: "completed", StatusCancelled: "cancelled"} {
		if got := LegacyStatus(status); got != legacy {
			t.Errorf("LegacyStatus(%s) = %q, want %q", status, got, legacy)
		}
		if back, _ := NormalizeStatus(legacy); back != status {
			t.Errorf("the legacy literal %q does not come back as %s", legacy, status)
		}
	}
}

func TestTransitions(t *testing.T) {
	allowed := map[[2]string]bool{}
	for from, tos := range transitions {
		for _, to := range tos {
			allowed[[2]string{from, to}] = true
		}
	}
	for _, from := range Statuses {
		for _, to := range Statuses {
			want := from == to || allowed[[2]string{from, to}]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s, %s) = %v", from, to, got)
			}
		}
	}
	for _, final := range []string{StatusCompleted, StatusCancelled} {
		for _, to := range Statuses {
			if to != final && CanTransition(final, to) {
				t.Errorf("%s is final but may become %s", final, to)
			}
		}
	}
	if CanTransition(StatusInProgress, StatusPMBooking) || CanTransition(StatusInProgress, StatusScheduled) {
		t.Error("a truck in the shop cannot go back to a booking")
	}
}

// isMaintenanceDue (fn:core/maintenance.ts): at most 2000 km left, or past.
func TestIsDue(t *testing.T) {
	for _, tc := range []struct {
		mileage, next int64
		want          bool
	}{
		{98_000, 100_000, true}, {97_999, 100_000, false}, {100_500, 100_000, true}, {0, 2000, true}, {0, 2001, false},
	} {
		if got := IsDue(tc.mileage, tc.next); got != tc.want {
			t.Errorf("IsDue(%d, %d) = %v", tc.mileage, tc.next, got)
		}
	}
}

// The automatic note of a PM booking is the legacy text with toLocaleString() grouping (fn:triggers.ts:490-493).
func TestPMNotes(t *testing.T) {
	if got, want := pmNotes(98_500, 100_000), "ระบบอัตโนมัติ: จากรายการเติมน้ำมัน (98,500 / 100,000 กม. เหลือ 1,500 กม. ≤ 2,000 กม.)"; got != want {
		t.Errorf("due: %q, want %q", got, want)
	}
	if got, want := pmNotes(101_234, 100_000), "ระบบอัตโนมัติ: เลยกำหนดเช็คระยะ (101,234 กม. / รอบ 100,000 กม.)"; got != want {
		t.Errorf("overdue: %q, want %q", got, want)
	}
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -1500: "-1,500", -999: "-999"} {
		if got := groupThousands(n); got != want {
			t.Errorf("groupThousands(%d) = %q, want %q", n, got, want)
		}
	}
}

// maintenanceDisplayCost (features/maintenance/utils/maintenanceDisplayCost.ts).
func TestDisplayCost(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		total, labor, parts, invoice *float64
		want                         float64
	}{
		{f(1500), f(100), f(200), f(999), 1500},
		{f(0), f(100.1), f(200.2), f(999), 300.3},
		{nil, nil, nil, f(450), 450},
		{f(0), f(0), nil, f(0), 0},
		{nil, nil, nil, nil, 0},
	} {
		if got := DisplayCost(tc.total, tc.labor, tc.parts, tc.invoice); got != tc.want {
			t.Errorf("DisplayCost = %v, want %v", got, tc.want)
		}
	}
}

func decodeInput(t *testing.T, body string) Input {
	t.Helper()
	var in Input
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestMergeCreate(t *testing.T) {
	var ck checker
	m := row{}
	in := decodeInput(t, `{"type":"pm","serviceType":" เปลี่ยนน้ำมันเครื่อง ","startDate":"2026-10-11","status":"PM Booking",
		"appointmentTime":"09:30","pickupAppointmentAt":"2026-10-12T17:00","costLaborThb":500.5,"paymentMethod":"Cash",
		"currentMileage":98000,"nextServiceMileage":108000}`)
	merge(&ck, &in, &m, true)
	if err := ck.err(); err != nil {
		t.Fatal(err)
	}
	if m.Type != "PM" || m.ServiceType != "เปลี่ยนน้ำมันเครื่อง" || m.Status != StatusPMBooking || *m.PaymentMethod != "cash" ||
		*m.CostLabor != 500.5 || *m.CurrentMileage != 98000 {
		t.Fatalf("merged %+v", m)
	}
	if want := time.Date(2026, 10, 12, 17, 0, 0, 0, clock.Bangkok); !m.PickupAppointmentAt.Equal(want) {
		t.Errorf("pickupAppointmentAt %v, want %v (Bangkok wall time)", m.PickupAppointmentAt, want)
	}

	// The form's default status, and the required fields.
	ck, m = checker{}, row{}
	in = decodeInput(t, `{"type":"CM","serviceType":"ยางแตก","startDate":"2026-10-11"}`)
	merge(&ck, &in, &m, true)
	if ck.err() != nil || m.Status != StatusInProgress {
		t.Fatalf("default status %q, %v", m.Status, ck.err())
	}
	ck, m = checker{}, row{}
	in = decodeInput(t, `{"type":"XX","startDate":"11/10/2026","status":"done","appointmentTime":"25:00",
		"costPartsThb":-1,"totalCostThb":1.234,"currentMileage":3000000,"providerLat":91,"driverSubmitted":true,
		"endDate":"2026-10-01"}`)
	merge(&ck, &in, &m, true)
	got := map[string]string{}
	for _, v := range ck.bad {
		got[v.Field] = v.Reason
	}
	for field, reason := range map[string]string{"type": "invalid", "serviceType": "required", "startDate": "invalid",
		"status": "invalid", "appointmentTime": "invalid", "costPartsThb": "out_of_range", "totalCostThb": "too_precise",
		"currentMileage": "out_of_range", "providerLat": "out_of_range", "driverSubmitted": "not_editable"} {
		if got[field] != reason {
			t.Errorf("%s: %q, want %q (all: %v)", field, got[field], reason, got)
		}
	}
}

func TestMergePatchChanges(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	notes := "เดิม"
	cur := row{Type: "PM", ServiceType: "เช็คระยะ", Status: StatusScheduled, StartDate: start, Notes: &notes}
	var ck checker
	in := decodeInput(t, `{"type":"PM","serviceType":"เช็คระยะ","notes":"เดิม","status":"In-Progress","endDate":"2026-09-30"}`)
	changed := merge(&ck, &in, &cur, false)
	if !strings.Contains(strings.Join(changed, ","), "status") || len(changed) != 2 {
		t.Errorf("changed %v, want status and endDate only", changed)
	}
	if ck.err() == nil || ck.bad[0].Field != "endDate" || ck.bad[0].Reason != "before_start" {
		t.Errorf("an end before the start: %v", ck.bad)
	}
	ck = checker{}
	in = decodeInput(t, `{"notes":null,"provider":"  "}`)
	changed = merge(&ck, &in, &cur, false)
	if cur.Notes != nil || cur.Provider != nil || len(changed) != 1 || changed[0] != "notes" {
		t.Errorf("clearing: notes %v provider %v changed %v", cur.Notes, cur.Provider, changed)
	}
}

func TestCheckFiles(t *testing.T) {
	var ck checker
	checkFiles(&ck, Field[map[string][]string]{Set: true, V: map[string][]string{"images": {"a", "a"}, "photos": {"b"}}})
	got := map[string]string{}
	for _, v := range ck.bad {
		got[v.Field] = v.Reason
	}
	if got["files"] != "unknown_kind" || got["files.images"] != "invalid" {
		t.Errorf("violations %v", got)
	}
	keys := make([]string, MaxFilesPerKind+1)
	for i := range keys {
		keys[i] = uuid.NewString()
	}
	ck = checker{}
	checkFiles(&ck, Field[map[string][]string]{Set: true, V: map[string][]string{"receipts": keys}})
	if len(ck.bad) != 1 || ck.bad[0].Reason != "too_many" {
		t.Errorf("too many: %v", ck.bad)
	}
}

// The write-back shape of a record (main spec §13.8): legacy status literal, yyyy-MM-dd days, the admin form's local
// pickup time, nil for empty fields, createdAt / createdBy only on creation.
func TestLegacyFields(t *testing.T) {
	now := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	plate, brand, notes, appt := "กข-1234", "Isuzu", "ระบบอัตโนมัติ", "09:30"
	pickup := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	mileage := int32(98500)
	cost := 1500.0
	truck := uuid.New()
	r := maintenancedb.MaintenanceRecord{ID: uuid.New(), TruckID: &truck, TruckLicensePlateSnapshot: &plate,
		TruckBrandSnapshot: &brand, MaintenanceType: "PM", ServiceType: pmServiceType,
		StartDate: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), Status: StatusPMBooking, AppointmentTime: &appt,
		PickupAppointmentAt: &pickup, TotalCostThb: &cost, CurrentMileage: &mileage, Notes: &notes}
	f := LegacyFields(r, "trk_legacy", "uid1", now, true)
	for k, want := range map[string]any{"truckId": "trk_legacy", "status": "PM Booking", "type": "PM",
		"startDate": "2026-10-11", "endDate": nil, "pickupAppointment": "2026-10-12T17:00:00", "totalCost": 1500.0,
		"costLabor": nil, "currentMileage": int64(98500), "notes": notes, "createdBy": "uid1", "updatedBy": "uid1",
		"truckLicensePlate": plate, "appointmentTime": "09:30"} {
		if f[k] != want {
			t.Errorf("%s = %#v, want %#v", k, f[k], want)
		}
	}
	if f["createdAt"] != now || f["updatedAt"] != now {
		t.Errorf("timestamps %v %v", f["createdAt"], f["updatedAt"])
	}
	for _, k := range []string{"driverSubmitted", "checkInAt", "checkOutAt", "images", "receipts", "invoiceUrl"} {
		if _, ok := f[k]; ok {
			t.Errorf("%s is the driver's and is never written back", k)
		}
	}
	if _, ok := LegacyFields(r, "", "uid1", now, false)["createdAt"]; ok {
		t.Error("an update writes createdAt")
	}
}
