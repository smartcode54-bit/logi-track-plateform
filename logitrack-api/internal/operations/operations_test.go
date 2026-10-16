package operations

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestTaskNumbers(t *testing.T) {
	// 2026-10-10 18:30Z is 11 October 01:30 in Bangkok: the number carries the Bangkok day.
	day := PlanDate(time.Date(2026, 10, 10, 18, 30, 0, 0, time.UTC))
	if day.Format(time.DateOnly) != "2026-10-11" {
		t.Fatalf("PlanDate = %s", day.Format(time.DateOnly))
	}
	for _, c := range []struct {
		typ  string
		seq  int
		want string
	}{
		{FirstMile, 1, "FM-11102026-001"}, {LineHaul, 42, "LH-11102026-042"}, {FirstMile, 999, "FM-11102026-999"},
		{FirstMile, 1000, "FM-11102026-1000"}, // padStart(3) never truncates
	} {
		got := FormatTaskNo(c.typ, day, c.seq)
		if got != c.want {
			t.Errorf("FormatTaskNo(%s, %d) = %s, want %s", c.typ, c.seq, got, c.want)
		}
		typ, d, seq, padded, ok := ParseTaskNo(got)
		if !ok || typ != c.typ || !d.Equal(day) || seq != c.seq || !padded {
			t.Errorf("ParseTaskNo(%s) = %s %s %d %t %t", got, typ, d, seq, padded, ok)
		}
	}
	// Legacy numbers: unpadded (mobile, getNextTaskId) parse but are not padded; other forms are not numbers of record.
	if typ, _, seq, padded, ok := ParseTaskNo("FM-01102026-7"); !ok || typ != FirstMile || seq != 7 || padded {
		t.Errorf("legacy unpadded: %s %d %t %t", typ, seq, padded, ok)
	}
	for _, bad := range []string{"FM-1102026-001", "XX-11102026-001", "FM-31022026-001", "FM-11102026-000", "FM-11102026-",
		"LH-11102026-01a", "20261011-001", "FM-11102026-2147483648"} { // the counter is an int: past it, not a number of record
		if _, _, _, _, ok := ParseTaskNo(bad); ok {
			t.Errorf("ParseTaskNo(%q) accepted", bad)
		}
	}
	if TaskNoPrefix("other") != "" {
		t.Error("an unknown type has a prefix")
	}
}

// The ranks of tripPhotoWorkflowRank (trip_photo_order.dart) and the stable sort.
func TestPhotoRank(t *testing.T) {
	for typ, want := range map[string]int{
		"checkin_app": 500, "runsheet": 1000, "runsheet_extra_3": 1003, "seal": 1006, "truck_release": 1007,
		"arrived": 2000, "runsheet_received": 2004, "stop_1_arrived": 3010, "stop_2_opening": 3022,
		"stop_3_unknown": 3039, "selfie": 9000, " seal ": 1006,
	} {
		if got := PhotoRank(typ); got != want {
			t.Errorf("PhotoRank(%q) = %d, want %d", typ, got, want)
		}
	}
	photos := []TripPhoto{{PhotoType: "x2"}, {PhotoType: "stop_1_arrived"}, {PhotoType: "x1"}, {PhotoType: "seal"}, {PhotoType: "checkin_app"}}
	SortPhotos(photos)
	var got []string
	for _, p := range photos {
		got = append(got, p.PhotoType)
	}
	if want := []string{"checkin_app", "seal", "stop_1_arrived", "x2", "x1"}; !slices.Equal(got, want) {
		t.Errorf("SortPhotos = %v, want %v", got, want)
	}
}

func TestCursors(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	at := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	for _, c := range []cursor{{Axis: axisCreated, At: &at, ID: id}, {Axis: axisPlan, Day: "2026-10-11", ID: id},
		{Axis: axisDelivered, At: &at, ID: id}, {Axis: axisBilling, Day: "2026-10-01", ID: id}, {Axis: axisEnded, At: &at, ID: id}} {
		got, ok := decodeCursor(c.encode(), c.Axis)
		if !ok || got.ID != id {
			t.Fatalf("round trip of %+v: %+v %t", c, got, ok)
		}
		// A cursor of another axis is refused (a cursor never crosses lists or sorts).
		other := axisCreated
		if c.Axis == axisCreated {
			other = axisDelivered
		}
		if _, ok := decodeCursor(c.encode(), other); ok {
			t.Errorf("%s cursor accepted on %s", c.Axis, other)
		}
	}
	if c, ok := decodeCursor("", axisCreated); !ok || c != nil {
		t.Error("the first page has no cursor")
	}
	// Each malformed cursor is refused on its own axis (an axis mismatch is the loop above): an undecodable value, a day
	// that is not yyyy-MM-dd or missing on a day axis, an instant on a day axis, a missing instant or a day on an
	// instant axis, a nil id.
	for _, bad := range []struct {
		name, s, axis string
	}{
		{"not base64", "%%%", axisCreated},
		{"empty object", "e30", axisCreated},
		{"day not yyyy-MM-dd", cursor{Axis: axisPlan, Day: "11/10/2026", ID: id}.encode(), axisPlan},
		{"day missing", cursor{Axis: axisBilling, ID: id}.encode(), axisBilling},
		{"instant on a day axis", cursor{Axis: axisPlan, Day: "2026-10-11", At: &at, ID: id}.encode(), axisPlan},
		{"instant missing (created)", cursor{Axis: axisCreated, ID: id}.encode(), axisCreated},
		{"instant missing (ended)", cursor{Axis: axisEnded, ID: id}.encode(), axisEnded},
		{"day on an instant axis", cursor{Axis: axisDelivered, At: &at, Day: "2026-10-11", ID: id}.encode(), axisDelivered},
		{"nil id (instant axis)", cursor{Axis: axisCreated, At: &at}.encode(), axisCreated},
		{"nil id (day axis)", cursor{Axis: axisPlan, Day: "2026-10-11"}.encode(), axisPlan},
	} {
		if _, ok := decodeCursor(bad.s, bad.axis); ok {
			t.Errorf("%s: %q accepted on axis %s", bad.name, bad.s, bad.axis)
		}
	}
	rows := []int{1, 2, 3}
	p := page(rows, 2, func(i int) cursor { return cursor{Axis: axisCreated, At: &at, ID: id} })
	if len(p.Items) != 2 || p.NextCursor == "" {
		t.Errorf("page of 3 at limit 2: %+v", p)
	}
	if p := page(rows, 3, nil); len(p.Items) != 3 || p.NextCursor != "" {
		t.Errorf("last page: %+v", p)
	}
}

func TestReachOf(t *testing.T) {
	tid, sub, drv := uuid.New(), uuid.New(), uuid.New()
	staff := &authz.Principal{UserID: uuid.New(), TenantID: &tid, TenantRole: authz.Manager, SubtenantIDs: []uuid.UUID{sub}}
	if r := ReachOf(staff); r.All || !slices.Equal(r.Tenants, []uuid.UUID{tid, sub}) || r.Driver != nil {
		t.Errorf("staff reach %+v", r)
	}
	driver := &authz.Principal{UserID: uuid.New(), TenantID: &tid, TenantRole: authz.Driver, DriverID: &drv}
	if r := ReachOf(driver); r.All || len(r.Tenants) != 0 || r.Driver == nil || *r.Driver != drv {
		t.Errorf("driver reach %+v", r)
	}
	all := &authz.Principal{UserID: uuid.New(), Platform: []authz.PlatformRole{authz.PlatformAdmin}, ActOnAll: true}
	if r := ReachOf(all); r.All || len(r.Tenants) != 0 || r.Driver != nil {
		t.Errorf("retired human override granted reach %+v", r)
	}
	scope := &authz.Principal{UserID: uuid.New(), PartyIDs: []uuid.UUID{uuid.New()}}
	if r := ReachOf(scope); r.All || len(r.Tenants) != 0 || r.Driver != nil {
		t.Errorf("customer scope reach %+v (reads the scope_* views)", r)
	}
	if r := SystemReach(); !r.All || r.tenants() == nil {
		t.Errorf("system reach %+v", r)
	}
}

// Appendix C §C.3.4: the billing columns of a standby record only for the billing carrier's principals.
func TestStandbyBillingProjection(t *testing.T) {
	own, carrier := uuid.New(), uuid.New()
	ownStaff := &authz.Principal{UserID: uuid.New(), TenantID: &own, TenantRole: authz.Manager, TenantKind: "own_fleet"}
	carrierStaff := &authz.Principal{UserID: uuid.New(), TenantID: &carrier, TenantRole: authz.TenantAdmin, TenantKind: "carrier"}
	driver := &authz.Principal{UserID: uuid.New(), TenantID: &own, TenantRole: authz.Driver, DriverID: ptr(uuid.New()), TenantKind: "own_fleet"}
	dispatcher := &authz.Principal{UserID: uuid.New(), TenantID: &own, TenantRole: authz.Manager, Dispatcher: true, TenantKind: "own_fleet"}
	all := &authz.Principal{UserID: uuid.New(), ActOnAll: true}

	priced := Standby{BillingEstimateThb: ptr(500.0), BillingRateSource: ptr("standby_rate"), BillingPartyID: ptr(uuid.New()),
		BillingRateEntryID: ptr(uuid.New())}
	unpriced := Standby{BillingUnpricedReason: ptr("no_rate")}
	// A service-fee price names no fee row: the own fleet's, whatever tenant holds the party's fee now.
	serviceFee := Standby{BillingEstimateThb: ptr(500.0), BillingRateSource: ptr("service_fee"), BillingPartyID: ptr(uuid.New())}
	// A legacy standby rate the ETL has not linked yet (billing_rate_entry_id NULL): the own fleet's too.
	unlinked := Standby{BillingEstimateThb: ptr(500.0), BillingRateSource: ptr("standby_rate"), BillingPartyID: ptr(uuid.New())}
	// The carrier stored with the price (0015) decides over the entry and the own-fleet default.
	storedCarrier := Standby{BillingEstimateThb: ptr(500.0), BillingRateSource: ptr("service_fee"), BillingPartyID: ptr(uuid.New()),
		BillingTenantID: ptr(carrier)}
	storedOwn := priced
	storedOwn.BillingTenantID = ptr(own)
	for _, c := range []struct {
		name   string
		p      *authz.Principal
		rec    Standby
		tenant uuid.UUID
		want   bool
	}{
		{"own fleet prices it", ownStaff, priced, own, true},
		{"carrier staff reading a row the own fleet priced", carrierStaff, priced, own, false},
		{"rate outside the reach", carrierStaff, priced, uuid.Nil, false},
		{"carrier priced it", carrierStaff, priced, carrier, true},
		{"unpriced: the own fleet", ownStaff, unpriced, uuid.Nil, true},
		{"unpriced: a carrier", carrierStaff, unpriced, uuid.Nil, false},
		{"service fee: the own fleet", ownStaff, serviceFee, uuid.Nil, true},
		{"service fee: a carrier, even holding the party's fee now", carrierStaff, serviceFee, carrier, false},
		{"unlinked legacy rate: the own fleet", ownStaff, unlinked, uuid.Nil, true},
		{"unlinked legacy rate: a carrier", carrierStaff, unlinked, carrier, false},
		{"stored carrier: that carrier", carrierStaff, storedCarrier, uuid.Nil, true},
		{"stored carrier: not the own fleet", ownStaff, storedCarrier, uuid.Nil, false},
		{"stored own fleet over a carrier's entry", carrierStaff, storedOwn, carrier, false},
		{"stored own fleet", ownStaff, storedOwn, carrier, true},
		{"driver", driver, priced, own, false},
		{"dispatcher", dispatcher, priced, own, false},
		{"legacy all-tenant field grants nothing", all, priced, own, false},
	} {
		v := Project(c.p, c.rec, c.tenant)
		if v.Billing != c.want {
			t.Errorf("%s: billing %t, want %t", c.name, v.Billing, c.want)
		}
		if !v.Billing && (v.Standby.BillingEstimateThb != nil || v.Standby.BillingRateSource != nil ||
			v.Standby.BillingPartyID != nil || v.Standby.BillingUnpricedReason != nil || v.Standby.BillingTenantID != nil) {
			t.Errorf("%s: billing columns left in %+v", c.name, v.Standby)
		}
	}
}

func TestNewTaskValidation(t *testing.T) {
	ok := NewTask{TaskInsert: TaskInsert{TenantID: uuid.New(), TenantSource: "form", TaskType: FirstMile,
		Status: TaskPending, PlanAt: time.Now(), SourceHubRaw: "ALANG-A", DestinationRaw: "SOCE"}}
	if err := ok.validate(); err != nil {
		t.Fatalf("valid task refused: %v", err)
	}
	drv := uuid.New()
	for name, mut := range map[string]func(*NewTask){
		"task type":  func(n *NewTask) { n.TaskType = "FIRST_MILE" },
		"status":     func(n *NewTask) { n.Status = "Pending" },
		"source":     func(n *NewTask) { n.SourceHubRaw = "  " },
		"helper":     func(n *NewTask) { n.DriverID, n.HelperDriverID = &drv, &drv },
		"truck type": func(n *NewTask) { n.TruckType = ptr("4WH") },
		"one stop":   func(n *NewTask) { n.IsMultiDelivery, n.Stops = true, []NewStop{{Destination: "A"}} },
		// Planned stops make the task multi-drop (CreateTask), so one stop without the flag is too few as well.
		"one stop without flag": func(n *NewTask) { n.Stops = []NewStop{{Destination: "A"}} },
		"blank stop":            func(n *NewTask) { n.Stops = []NewStop{{Destination: "A"}, {Destination: " "}} },
		"tenant":                func(n *NewTask) { n.TenantID = uuid.Nil },
		"job":                   func(n *NewTask) { n.JobCategory = ptr("primary") },
		"empty number":          func(n *NewTask) { n.TaskNo = ptr(" ") },
	} {
		n := ok
		mut(&n)
		var he *httpx.Error
		if err := n.validate(); !errors.As(err, &he) || he.Status != http.StatusUnprocessableEntity {
			t.Errorf("%s: %v", name, err)
		}
	}
	n := ok
	n.Stops = []NewStop{{Destination: "spk-gw"}, {Destination: " SPK-GW "}}
	var he *httpx.Error
	if err := n.validate(); !errors.As(err, &he) || he.Code != CodeDuplicateDestination {
		t.Errorf("duplicate stop destinations: %v", err)
	}
}

func TestMapError(t *testing.T) {
	for _, c := range []struct {
		pe     *pgconn.PgError
		status int
		code   string
	}{
		{&pgconn.PgError{Code: "23505", ConstraintName: "trip_records_trip_no"}, 409, CodeDuplicateTripID},
		{&pgconn.PgError{Code: "23505", ConstraintName: "task_delivery_stops_task_id_destination_raw_key"}, 409, CodeDuplicateDestination},
		{&pgconn.PgError{Code: "23505", ConstraintName: "tasks_legacy"}, 409, "already_exists"},
		{&pgconn.PgError{Code: "23514", ConstraintName: "trip_records_trip_no_check"}, 422, "invalid_argument"},
		{&pgconn.PgError{Code: "23503"}, 422, "invalid_argument"},
		{&pgconn.PgError{Code: "23000"}, 409, "failed_precondition"},
		{&pgconn.PgError{Code: "42501"}, 403, "permission_denied"},
	} {
		var he *httpx.Error
		if err := MapError(c.pe); !errors.As(err, &he) || he.Status != c.status || he.Code != c.code {
			t.Errorf("%s %s: %v", c.pe.Code, c.pe.ConstraintName, err)
		}
	}
	plain := errors.New("x")
	if MapError(plain) != plain || MapError(nil) != nil {
		t.Error("a non-database error changed")
	}
}
