package operations

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// testDirectory is a hub master of two hubs and the SOC hub row SOCE.
func testDirectory() (directory, uuid.UUID, uuid.UUID, uuid.UUID) {
	alang, spk, soce := uuid.New(), uuid.New(), uuid.New()
	en := "Wang Thonglang"
	cust := "J&T EXPRESS บางปู"
	hubs := []Hub{
		{ID: alang, Code: "ALANG-A", NameTh: "วังทองหลาง", NameEn: &en},
		{ID: spk, Code: "SPK-GW", NameTh: "SPK-GW", LinkedCustomerName: &cust}, // a J&T hub named by its customer
		{ID: soce, Code: "SOCE", NameTh: "บัวโรย"},
	}
	d := directory{byID: map[uuid.UUID]Hub{}, byCode: map[string][]uuid.UUID{}}
	for _, h := range hubs {
		d.byID[h.ID] = h
		d.byCode[h.Code] = append(d.byCode[h.Code], h.ID)
	}
	return d, alang, spk, soce
}

func sp(v string) *string { return &v }

// ADR 0006: the place of a stored value: the hub stored at write, else the SOC key (stored or spelled), else the raw
// text as its own bucket, else none; labels as lib/hubDisplay.ts (Thai, English, the linked customer, the code; the SOC
// names of SOC_DESTINATIONS for a SOC key no hub row carries).
func TestPlaceKeysAndLabels(t *testing.T) {
	d, alang, spk, _ := testDirectory()
	empty := directory{byID: map[uuid.UUID]Hub{}, byCode: map[string][]uuid.UUID{}}
	for _, c := range []struct {
		name     string
		d        directory
		raw      *string
		hub      *uuid.UUID
		soc      *string
		key      string
		label    string
		resolved bool
	}{
		{"stored hub", d, sp("วังทองหลาง"), &alang, nil, "code:ALANG-A", "วังทองหลาง", true},
		{"hub named by its customer", d, sp("SPK-GW"), &spk, nil, "code:SPK-GW", "J&T EXPRESS บางปู", true},
		{"stored SOC key, hub row present", d, sp("SOCE (บัวโรย)"), nil, sp("SOCE"), "code:SOCE", "บัวโรย", true},
		{"SOC spelling", d, sp(" socw (สมุทรสาคร)"), nil, nil, "code:SOCW", "SOCW (สมุทรสาคร)", true},
		{"SOC key without a hub row", empty, sp("SOCN"), nil, nil, "code:SOCN", "SOCN (วังน้อย)", true},
		{"not a SOC", d, sp("SOCEX"), nil, nil, "raw:SOCEX", "SOCEX", false},
		{"unresolved text, trimmed", d, sp("  ALANG-A - วังทองหลาง "), nil, nil, "raw:ALANG-A - วังทองหลาง", "ALANG-A - วังทองหลาง", false},
		{"blank", d, sp("  "), nil, nil, PlaceNone, "", false},
		{"absent", d, nil, nil, nil, PlaceNone, "", false},
		{"a hub the directory does not know", empty, sp("X"), &alang, nil, "hub:" + alang.String(), "X", true},
	} {
		got := c.d.place(c.raw, c.hub, c.soc)
		if got.Key != c.key || got.Label != c.label || got.Resolved != c.resolved {
			t.Errorf("%s: %+v, want %s %q %v", c.name, got, c.key, c.label, c.resolved)
		}
	}
}

func TestSocKey(t *testing.T) {
	for raw, want := range map[string]string{"SOCE": "SOCE", "soce": "SOCE", "SOCE (บัวโรย)": "SOCE", "SOCN(x)": "SOCN",
		" SOCW ": "SOCW", "SOCEX": "", "SOC": "", "": "", "ALANG-A": ""} {
		if got := socKey(raw); got != want {
			t.Errorf("socKey(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The place filter values: a code names its hubs and, for SOCE/SOCN/SOCW, the SOC key; an unknown code matches
// nothing; a raw value is matched verbatim (trimmed); anything else is 422.
func TestParsePlace(t *testing.T) {
	d, alang, _, soce := testDirectory()
	for _, c := range []struct {
		v    string
		want placeFilter
		bad  bool
	}{
		{"", placeFilter{}, false},
		{"all", placeFilter{}, false},
		{"__none__", placeFilter{on: true, hubs: []uuid.UUID{}, none: true}, false},
		{"code:alang-a", placeFilter{on: true, hubs: []uuid.UUID{alang}}, false},
		{"code:SOCE", placeFilter{on: true, hubs: []uuid.UUID{soce}, soc: sp("SOCE")}, false},
		{"code:SOCN", placeFilter{on: true, hubs: []uuid.UUID{}, soc: sp("SOCN")}, false},
		{"code:NOPE", placeFilter{on: true, hubs: []uuid.UUID{}}, false},
		{"raw: ประเวศ18 ", placeFilter{on: true, hubs: []uuid.UUID{}, raw: sp("ประเวศ18")}, false},
		{"hub:" + alang.String(), placeFilter{on: true, hubs: []uuid.UUID{alang}}, false},
		{"SOCE", placeFilter{}, true},
		{"code:", placeFilter{}, true},
		{"raw:", placeFilter{}, true},
		{"raw:a\x01\nb", placeFilter{on: true, hubs: []uuid.UUID{}, raw: sp("a\x01\nb")}, false},     // stored text, listed as is
		{"raw:\tBangna\u00a0", placeFilter{on: true, hubs: []uuid.UUID{}, raw: sp("Bangna")}, false}, // the JavaScript trim
		{"raw:a\x00", placeFilter{}, true}, // PostgreSQL text holds no NUL
		{"raw:\xff", placeFilter{}, true},  // nor invalid UTF-8
		{"raw:\u00a0\u3000", placeFilter{}, true},
		{"hub:x", placeFilter{}, true},
	} {
		var ck checker
		got := d.parsePlace(&ck, "origin", c.v)
		if (ck.err() != nil) != c.bad {
			t.Errorf("%q: error %v", c.v, ck.err())
			continue
		}
		if got.on != c.want.on || got.none != c.want.none || !slices.Equal(got.hubs, c.want.hubs) ||
			deref2(got.soc) != deref2(c.want.soc) || deref2(got.raw) != deref2(c.want.raw) {
			t.Errorf("%q: %+v, want %+v", c.v, got, c.want)
		}
	}
}

// ADR 0005: the truck of a trip is its snapshot, else its task's (never the driver's live truck); a row without a truck
// is keyed by its plate (trimmed, never normalised); a row with neither is the none bucket.
func TestMonitorTruckKey(t *testing.T) {
	d, _, _, _ := testDirectory()
	truck, taskTruck := uuid.New(), uuid.New()
	for _, c := range []struct {
		name              string
		cols              monitorCols
		key, plate, klass string
	}{
		{"trip truck", monitorCols{truckID: &truck, taskTruck: &taskTruck, plate: sp("70-1"), taskPlate: sp("80-2"),
			vehicleClass: sp("6WH"), taskClass: sp("4W")}, "id:" + truck.String(), "70-1", "6WH"},
		{"the task's truck", monitorCols{taskTruck: &taskTruck, taskPlate: sp("80-2"), taskClass: sp("4W")},
			"id:" + taskTruck.String(), "80-2", "4W"},
		{"orphan plate", monitorCols{plate: sp(" 70 - 1234 "), taskPlate: sp("80-2")}, "plate:70 - 1234", "70 - 1234", BucketNone},
		{"the task's plate", monitorCols{plate: sp(" "), taskPlate: sp("80-2")}, "plate:80-2", "80-2", BucketNone},
		{"nothing", monitorCols{}, BucketNone, "", BucketNone},
	} {
		row := monitorRow(d, c.cols, nil)
		if row.Truck.Key != c.key || deref2(row.Truck.Plate) != c.plate || row.Truck.VehicleClassKey != c.klass {
			t.Errorf("%s: %+v", c.name, row.Truck)
		}
		if row.Stops == nil {
			t.Errorf("%s: stops must be an empty list, not null", c.name)
		}
	}
}

// The options: a hub row and a SOC key of one code are one option; resolved places first by label, then the
// unresolved raw values, then none; orphan plates flagged; none last everywhere.
func TestFacets(t *testing.T) {
	d, alang, _, soce := testDirectory()
	truck := uuid.New()
	got := d.facets([]facetRow{
		{facet: "total", n: 9}, {facet: "incident", n: 2}, {facet: "status", value: "delivered", n: 5},
		{facet: "origin", ref: &alang, n: 3}, {facet: "origin", value: "zz junk", n: 1}, {facet: "origin", value: "aa junk", n: 1},
		{facet: "origin", value: "", n: 4},
		{facet: "destination", ref: &soce, n: 2}, {facet: "destination", value: "SOCE", n: 3}, {facet: "destination", value: "SOCN", n: 1},
		{facet: "plate", ref: &truck, label: "70-1", n: 4}, {facet: "plate", value: "ZZ-9", n: 2}, {facet: "plate", value: "", n: 3},
		{facet: "vehicle_class", value: "", n: 2}, {facet: "vehicle_class", value: "6WH", n: 7},
		{facet: "partner", value: "SPX", n: 1}, {facet: "partner", value: "", n: 8},
	})
	if got.Total != 9 || got.Incident != 2 || got.StatusCounts["delivered"] != 5 {
		t.Errorf("counts %+v", got)
	}
	var origins []string
	for _, o := range got.Origins {
		origins = append(origins, o.Value)
	}
	if !slices.Equal(origins, []string{"code:ALANG-A", "raw:aa junk", "raw:zz junk", PlaceNone}) {
		t.Errorf("origins %v", origins)
	}
	if !got.Origins[1].IsUnresolved || got.Origins[0].IsUnresolved || got.Origins[0].Label != "วังทองหลาง" {
		t.Errorf("origin flags %+v", got.Origins)
	}
	if len(got.Destinations) != 2 || got.Destinations[0].Value != "code:SOCN" || got.Destinations[1].Value != "code:SOCE" ||
		got.Destinations[1].Count != 5 {
		t.Errorf("destinations %+v (SOCE as a hub row and as a key is one option)", got.Destinations)
	}
	if len(got.Plates) != 3 || got.Plates[0].Value != "id:"+truck.String() || got.Plates[1].Value != "plate:ZZ-9" ||
		!got.Plates[1].IsOrphan || got.Plates[0].IsOrphan || got.Plates[2].Value != BucketNone || got.Plates[2].Label != nil {
		t.Errorf("plates %+v", got.Plates)
	}
	if got.VehicleClasses[0].Value != "6WH" || got.VehicleClasses[1].Value != BucketNone {
		t.Errorf("classes %+v", got.VehicleClasses)
	}
	if got.Partners[0].Value != "SPX" || got.Partners[1].Value != BucketNone {
		t.Errorf("partners %+v", got.Partners)
	}
}

func staff(caps ...authz.Cap) *authz.Principal {
	t := uuid.New()
	return &authz.Principal{UserID: uuid.New(), TenantID: &t, TenantRole: authz.Manager, Caps: authz.NewCapSet(caps...)}
}

func fieldReason(err error) string {
	he := httpx.AsError(err)
	if he == nil {
		return ""
	}
	if f, ok := he.Details["fields"].([]httpx.FieldViolation); ok && len(f) > 0 {
		return f[0].Field + " " + f[0].Reason
	}
	return he.Code
}

func TestMonitorQuery(t *testing.T) {
	d, alang, _, _ := testDirectory()
	p := staff(authz.OperationsViewDriverMon)
	m, err := d.monitorQuery(p, MonitorInput{From: "2026-09-01", To: "2026-09-30", Statuses: []string{"incident", "delivered"},
		Plate: "plate: 70-1 ", VehicleClass: "6wh", Origin: "code:ALANG-A", Partner: "__none__", Q: " TRIP "})
	if err != nil {
		t.Fatal(err)
	}
	wantFrom := time.Date(2026, 8, 31, 17, 0, 0, 0, time.UTC)
	if !m.from.Equal(wantFrom) || !m.to.Equal(wantFrom.AddDate(0, 0, 30)) {
		t.Errorf("range %v - %v: Bangkok days, both inclusive", m.from, m.to)
	}
	if !m.incident || deref2(m.plateRaw) != "70-1" || deref2(m.vehicleClass) != "6WH" || !m.partnerNone ||
		!slices.Equal(m.origin.hubs, []uuid.UUID{alang}) || deref2(m.q) != "TRIP" || m.limit != MonitorLimit || !m.facets {
		t.Errorf("query %+v", m)
	}
	m, err = d.monitorQuery(p, MonitorInput{Month: "2026-02", Cursor: cursor{Axis: axisCreated, At: ptr(time.Now()), ID: uuid.New()}.encode()})
	if err != nil || !m.from.Equal(time.Date(2026, 1, 31, 17, 0, 0, 0, time.UTC)) || !m.to.Equal(time.Date(2026, 2, 28, 17, 0, 0, 0, time.UTC)) || m.facets {
		t.Errorf("month %v - %v facets %v: %v", m.from, m.to, m.facets, err)
	}
	m, err = d.monitorQuery(p, MonitorInput{From: "2026-09-01T00:00:00Z", To: "2026-09-01T12:00:00+07:00"})
	if err != nil || m.to.Sub(m.from) != 5*time.Hour {
		t.Errorf("instants: %v - %v %v", m.from, m.to, err)
	}
	scopeP := &authz.Principal{UserID: uuid.New(), PartyIDs: []uuid.UUID{uuid.New()}}
	for _, c := range []struct {
		p    *authz.Principal
		in   MonitorInput
		want string
	}{
		{p, MonitorInput{To: "2026-09-01"}, "from required"},
		{p, MonitorInput{From: "2026-09-01"}, "to required"},
		{p, MonitorInput{From: "2026-13-01", To: "2026-09-01"}, "from invalid"},
		{p, MonitorInput{From: "2026-09-02", To: "2026-09-01"}, "to before_from"},
		{p, MonitorInput{From: "2025-01-01", To: "2026-01-02"}, "to range_too_long"},
		{p, MonitorInput{Month: "2026-9"}, "month invalid"},
		{p, MonitorInput{Month: "2026-09", From: "2026-09-01"}, "month exclusive"},
		{p, MonitorInput{Month: "2026-09", Limit: 201}, "limit out_of_range"},
		{p, MonitorInput{Month: "2026-09", Plate: "70-1234"}, "plate invalid"},
		{p, MonitorInput{Month: "2026-09", Plate: "id:nope"}, "plate invalid"},
		{p, MonitorInput{Month: "2026-09", VehicleClass: "TRUCK"}, "vehicleClass invalid"},
		{p, MonitorInput{Month: "2026-09", JobType: "air"}, "jobType invalid"},
		{p, MonitorInput{Month: "2026-09", Statuses: []string{"lost"}}, "status invalid"},
		{p, MonitorInput{Month: "2026-09", Q: strings.Repeat("ก", 101)}, "q too_long"},
		{p, MonitorInput{Month: "2026-09", Cursor: "eyJhIjoiZCJ9"}, "cursor invalid"},
		{scopeP, MonitorInput{Month: "2026-09", CustomerID: uuid.NewString()}, "customerId not_available"},
	} {
		_, err := d.monitorQuery(c.p, c.in)
		if got := fieldReason(err); got != c.want {
			t.Errorf("%+v: %q, want %q", c.in, got, c.want)
		}
	}
}

// The task types a principal lists (operations:view_first_mile / view_line_haul; dispatch:view_operations both): a type
// it does not read is 403 with the missing capability; without taskType a one-type reader lists its type.
func TestTaskFilterTypes(t *testing.T) {
	fm := staff(authz.OperationsViewFirstMile)
	f, err := taskFilter(fm, TaskListInput{})
	if err != nil || f.TaskType != FirstMile {
		t.Fatalf("first-mile reader: %+v %v", f, err)
	}
	_, err = taskFilter(fm, TaskListInput{TaskType: LineHaul})
	if he := httpx.AsError(err); he == nil || he.Status != 403 ||
		!slices.Equal(he.Details["missingCapability"].([]string), []string{string(authz.OperationsViewLineHaul)}) {
		t.Errorf("line haul for a first-mile reader: %v", err)
	}
	both := staff(authz.DispatchViewOperations)
	if f, err := taskFilter(both, TaskListInput{}); err != nil || f.TaskType != "" {
		t.Errorf("dispatcher: %+v %v", f, err)
	}
	f, err = taskFilter(both, TaskListInput{Date: "2026-10-05", Statuses: []string{"pending", "assigned", "pending"}})
	if err != nil || !f.From.Equal(*f.To) || f.From.Format(time.DateOnly) != "2026-10-05" || len(f.Statuses) != 2 {
		t.Errorf("date: %+v %v", f, err)
	}
	for _, c := range []struct {
		in   TaskListInput
		want string
	}{
		{TaskListInput{TaskType: "air"}, "taskType invalid"},
		{TaskListInput{Date: "2026-10-05", From: "2026-10-01"}, "date exclusive"},
		{TaskListInput{Statuses: []string{"Pending"}}, "status invalid"},
		{TaskListInput{DriverID: "x"}, "driverId invalid"},
	} {
		if _, err := taskFilter(both, c.in); fieldReason(err) != c.want {
			t.Errorf("%+v: %q, want %q", c.in, fieldReason(err), c.want)
		}
	}
}

func TestBound(t *testing.T) {
	var ck checker
	from, to := bound(&ck, "from", "2026-10-01", false), bound(&ck, "to", "2026-10-01", true)
	if ck.err() != nil || to.Sub(*from) != 24*time.Hour || from.UTC().Format(time.RFC3339) != "2026-09-30T17:00:00Z" {
		t.Errorf("day bounds %v %v %v", from, to, ck.err())
	}
	at := bound(&ck, "from", "2026-10-01T10:00:00.5+07:00", false)
	if at == nil || at.UTC().Format(time.RFC3339Nano) != "2026-10-01T03:00:00.5Z" {
		t.Errorf("instant %v", at)
	}
	if bound(&ck, "from", "", false) != nil {
		t.Error("empty is no bound")
	}
	bound(&ck, "to", "yesterday", true)
	if fieldReason(ck.err()) != "to invalid" {
		t.Errorf("invalid: %v", ck.err())
	}
}

// A scope principal's payload types carry the projection's keys only: no staff field is a key of TaskBase, TripBase,
// StandbyBase or IncidentBase, and the monitor row's staff fields are absent when unset.
func TestScopePayloadKeys(t *testing.T) {
	staffOnly := []string{"billing", "billingPartyId", "sourceLinkedPartyId", "destinationLinkedPartyId", "customerPartyId",
		"reviewStatus", "needsAdminReview", "ocrData", "tenantSource", "price", "deliveredVia", "legacyTripRef", "renames"}
	for _, v := range []any{TaskBase{Stops: []TaskStopRow{{}}}, TripBase{}, TripScopeDetail{}, StandbyBase{}, IncidentBase{},
		MonitorRow{Stops: []MonitorStop{{}}}} {
		b, _ := json.Marshal(v)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got := map[string]bool{}
		var walk func(any)
		walk = func(x any) {
			switch y := x.(type) {
			case map[string]any:
				for k, val := range y {
					got[k] = true
					walk(val)
				}
			case []any:
				for _, val := range y {
					walk(val)
				}
			}
		}
		walk(m)
		for _, k := range staffOnly {
			if got[k] {
				t.Errorf("%T carries %s", v, k)
			}
		}
	}
}

func TestPhotoOrder(t *testing.T) {
	rows := photoRows([]TripPhoto{{PhotoType: "stop_1_arrived"}, {PhotoType: "weird"}, {PhotoType: "seal"},
		{PhotoType: "checkin_app"}, {PhotoType: "arrived"}}, nil)
	var got []string
	for _, r := range rows {
		got = append(got, r.typ)
	}
	if !slices.Equal(got, []string{"checkin_app", "seal", "arrived", "stop_1_arrived", "weird"}) {
		t.Errorf("order %v (ADR 0018)", got)
	}
}
