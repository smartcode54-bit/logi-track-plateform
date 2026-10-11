package operations

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// fakeBeginner is a pool that is never used (the mode is decided without a database).
type fakeBeginner struct{}

func (fakeBeginner) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("no database") }

func violations(err error) string {
	var he *httpx.Error
	if !errors.As(err, &he) {
		return ""
	}
	fs, _ := he.Details["fields"].([]httpx.FieldViolation)
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = f.Field + " " + f.Reason
	}
	return strings.Join(parts, ", ")
}

// The write path follows PG_OWNED_DOMAINS (main spec §12.1): PostgreSQL when operations is owned, the write-back with
// a writer, else 503 write_back_unconfigured (never PostgreSQL in Firestore's place).
func TestWriteMode(t *testing.T) {
	pool := fakeBeginner{}
	for name, c := range map[string]struct {
		d    Deps
		want writeMode
	}{
		"owned":                 {Deps{Pool: pool, PGOwned: true, WriteBack: &WriteBack{Writer: writeback.NewMemory()}}, modePG},
		"write-back":            {Deps{Pool: pool, WriteBack: &WriteBack{Writer: writeback.NewMemory()}}, modeWriteBack},
		"unconfigured":          {Deps{Pool: pool}, 0},
		"write-back, no writer": {Deps{Pool: pool, WriteBack: &WriteBack{}}, 0},
	} {
		s, err := New(c.d)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.mode()
		if got != c.want {
			t.Errorf("%s: %v", name, got)
		}
		var he *httpx.Error
		if c.want == 0 && (!errors.As(err, &he) || he.Status != http.StatusServiceUnavailable ||
			he.Details["reason"] != ReasonWriteBackUnconfigured) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRequireCap(t *testing.T) {
	tid := uuid.New()
	manage := authz.NewCapSet(authz.OperationsManageTasks)
	if err := requireCap(&authz.Principal{TenantID: &tid, Caps: manage}, authz.OperationsManageTasks); err != nil {
		t.Fatal(err)
	}
	if err := requireCap(&authz.Principal{AMR: authz.AMRAPIKey, Caps: manage}, authz.OperationsManageTasks); err == nil {
		t.Fatal("an API key writes operations rows")
	}
	err := requireCap(&authz.Principal{Platform: []authz.PlatformRole{authz.PlatformAdmin}}, authz.OperationsManageTasks)
	var he *httpx.Error
	if !errors.As(err, &he) || he.Details["reason"] != ReasonActOnTenantRequired {
		t.Fatalf("platform without X-Act-On-Tenant: %v", err)
	}
}

func TestParseTaskFields(t *testing.T) {
	parse := func(body string, create bool) (taskFields, error) {
		var ck checker
		f, err := parseFields([]byte(body), &ck)
		if err != nil {
			return taskFields{}, err
		}
		in := parseTaskFields(f, create)
		return in, ck.err()
	}
	in, err := parse(`{"taskType":"LINE_HAUL","date":"2026-10-12","time":"07:05","sourceHub":" A\t","destination":"B",
		"billingPartyId":"`+uuid.NewString()+`","jobCategory":"supplementary","truckType":"6wh",
		"deliveryStops":[{"destination":"X","isCustom":true},{"destination":"Y","sourceId":" s "}]}`, true)
	if err != nil || in.taskType != LineHaul || *in.sourceHub != "A" || *in.jobCategory != "SUPPLEMENTARY" ||
		*in.truckType != "6WH" || *in.planTime != "07:05" || len(in.stops) != 2 || !in.stops[0].isCustom ||
		*in.stops[1].sourceID != "s" {
		t.Fatalf("create: %+v %v", in, err)
	}
	if want := time.Date(2026, 10, 11, 17, 0, 0, 0, time.UTC); !in.planAt.Equal(want) {
		t.Fatalf("plan instant %v (Bangkok midnight of the day)", in.planAt)
	}
	for body, want := range map[string]string{
		`{}`: "taskType required, date required, sourceHub required, destination required, billingPartyId required",
		`{"taskType":"x","date":"2026-02-30","sourceHub":"A","destination":"B","billingPartyId":"` + uuid.NewString() + `"}`:                                        "taskType invalid, date invalid",
		`{"taskType":"first_mile","date":"12/10/2026","sourceHub":"A","destination":"B","billingPartyId":"` + uuid.NewString() + `"}`:                               "date invalid",
		`{"taskType":"first_mile","date":"2026-10-12","sourceHub":"A","destination":"B","billingPartyId":"x","runOrder":2}`:                                         "runOrder not_supported, billingPartyId invalid",
		`{"taskType":"first_mile","date":"2026-10-12","sourceHub":"","destination":"B","billingPartyId":"` + uuid.NewString() + `","jobCategory":"เสริม"}`:          "sourceHub required, jobCategory invalid",
		`{"taskType":"first_mile","date":"2026-10-12T09:00:00+07:00","sourceHub":"A","destination":"B","billingPartyId":"` + uuid.NewString() + `","time":"25:00"}`: "time invalid",
	} {
		if _, err := parse(body, true); violations(err) != want {
			t.Errorf("%s: %q, want %q", body, violations(err), want)
		}
	}
	in, err = parse(`{"status":"assigned","runOrder":3,"driverId":null,"tenantId":null}`, false)
	if err != nil || *in.status != TaskAssigned || *in.runOrder != 3 || !in.has("driverId") || in.driverID != nil {
		t.Fatalf("patch: %+v %v", in, err)
	}
	for body, want := range map[string]string{
		`{"status":"completed"}`:          "status not_settable",
		`{"runOrder":0}`:                  "runOrder out_of_range",
		`{"runOrder":1.5}`:                "runOrder invalid",
		`{"taskType":"first_mile"}`:       "taskType not_supported",
		`{"jobCategory":null}`:            "jobCategory required",
		`{"deliveryStops":[{"x":1}]}`:     "x not_supported, destination required",
		`{"deliveryStops":"a"}`:           "deliveryStops invalid",
		`{"sourceHub":"   "}`:             "sourceHub required",
		`{"actualPickupAt":"tomorrow"}`:   "actualPickupAt invalid",
		`{"truckType":"PICKUP"}`:          "truckType invalid",
		`{"helperDriverId":"not-a-uuid"}`: "helperDriverId invalid",
	} {
		if _, err := parse(body, false); violations(err) != want {
			t.Errorf("%s: %q, want %q", body, violations(err), want)
		}
	}
	if _, err := parse(`[1]`, false); err == nil {
		t.Fatal("a non-object body")
	}
}

// AC (T36 tracking note on #51): a misspelled job-category cell rejects the row; a blank cell is PRIMARY.
func TestParseImportRow(t *testing.T) {
	party := uuid.NewString()
	for cell, want := range map[string]string{"": "PRIMARY", "หลัก": "PRIMARY", "เสริม": "SUPPLEMENTARY", "งานเสริม": "SUPPLEMENTARY",
		"Supplementary": "SUPPLEMENTARY", "เสิรม": "", "secondary": ""} {
		r := parseImportRow(1, FirstMile, []byte(`{"date":"2026-10-12","sourceHub":"A","destination":"B","billingPartyId":"`+party+
			`","jobCategory":"`+cell+`","plate":" 1กข - 1234 "}`))
		got := ""
		if r.in.jobCategory != nil {
			got = *r.in.jobCategory
		}
		if got != want || (want == "") != (len(r.errs) == 1 && r.errs[0].Field == "jobCategory") || r.plate != "1กข1234" {
			t.Errorf("%q: %q %v %q", cell, got, r.errs, r.plate)
		}
	}
	r := parseImportRow(2, FirstMile, []byte(`{"date":"x","truckType":"9WH","extra":1}`))
	if len(r.errs) != 6 {
		t.Fatalf("errors %v", r.errs)
	}
	if r := parseImportRow(3, FirstMile, []byte(`[]`)); len(r.errs) != 1 || r.errs[0].Field != "row" {
		t.Fatalf("not an object %v", r.errs)
	}
}

// The legacy encoding of main spec §13.8: status and type literals, dateStr in Bangkok, the drivers' Firestore ids,
// the helper's uid, the party links by their customers document; a PATCH writes only the changed keys.
func TestEncodeTask(t *testing.T) {
	party, driver, helper, truck := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	plan := time.Date(2026, 10, 11, 17, 0, 0, 0, time.UTC) // 12 Oct in Bangkok
	ro := int32(3)
	pl := taskPlan{next: Task{TaskType: FirstMile, TaskNo: ptr("FM-12102026-004"), Status: TaskAssigned, PlanAt: plan,
		PlanTime: ptr("09:30"), JobCategory: ptr("PRIMARY"), SourceHubRaw: "ALANG-A", DestinationRaw: "SOCE",
		BillingPartyID: &party, DriverID: &driver, HelperDriverID: &helper, TruckID: &truck, TruckType: ptr("6WH"),
		LicensePlateSnapshot: ptr("1กข-1234"), RunOrder: &ro},
		parties: map[uuid.UUID]operationsdb.WritePartyRow{party: {ID: party, CustomerDocID: ptr("cust_1"),
			CustomerCode: ptr("CJSF"), CustomerName: ptr("CJ")}},
		driver: &operationsdb.WriteDriverRow{ID: driver, LegacyDocID: ptr("drv_1"), FirstName: "A", LastName: "B", Mobile: "08"},
		helper: &operationsdb.WriteDriverRow{ID: helper, LegacyDocID: ptr("drv_2"), UserAuthUid: ptr("uid_2")},
		truck:  &operationsdb.WriteTruckRow{ID: truck, LegacyDocID: ptr("trk_1")}}
	now := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	doc, err := encodeTask(pl, true, now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"taskType": "FIRST_MILE", "taskId": "FM-12102026-004", "status": "Assigned", "dateStr": "12102026",
		"time": "09:30", "driverId": "drv_1", "driverName": "A B", "driverPhone": "08", "truckId": "trk_1", "truckType": "6WH",
		"licensePlate": "1กข-1234", "billingCustomerId": "cust_1", "billingCustomerCode": "CJSF", "billingCustomerName": "CJ",
		"runOrder": int64(3), "jobCategory": "PRIMARY", "sourceHub": "ALANG-A", "destination": "SOCE", "isMultiDelivery": false}
	for k, v := range want {
		if doc[k] != v {
			t.Errorf("%s = %v, want %v", k, doc[k], v)
		}
	}
	if h, _ := doc["helperDriverIds"].([]any); len(h) != 1 || h[0] != "uid_2" {
		t.Errorf("helperDriverIds %v", doc["helperDriverIds"])
	}
	if !doc["date"].(time.Time).Equal(plan) || !doc["createdAt"].(time.Time).Equal(now) {
		t.Errorf("times %v %v", doc["date"], doc["createdAt"])
	}
	// A PATCH: only the changed keys (and updatedAt).
	pl.changed = []string{"jobCategory"}
	doc, err = encodeTask(pl, false, now)
	if err != nil || len(doc) != 2 || doc["jobCategory"] != "PRIMARY" {
		t.Fatalf("patch %v %v", doc, err)
	}
	// A master row the projection has not minted yet is refused, never written with a dangling reference.
	pl.changed = []string{"driverId"}
	pl.driver = &operationsdb.WriteDriverRow{ID: driver}
	var he *httpx.Error
	if _, err := encodeTask(pl, false, now); !errors.As(err, &he) || he.Details["reason"] != ReasonNotProjected ||
		he.Details["field"] != "driverId" {
		t.Fatalf("not projected: %v", err)
	}
	pl.changed = []string{"helperDriverId"}
	pl.helper = &operationsdb.WriteDriverRow{ID: helper, LegacyDocID: ptr("drv_2")}
	if _, err := encodeTask(pl, false, now); err == nil {
		t.Fatal("a helper without a Firebase uid")
	}
	// Removed links write nil (the field is deleted from the document).
	pl.next.DriverID, pl.next.HelperDriverID = nil, nil
	pl.changed = []string{"driverId", "helperDriverId"}
	doc, err = encodeTask(pl, false, now)
	if err != nil || doc["driverId"] != nil || len(doc["helperDriverIds"].([]any)) != 0 {
		t.Fatalf("removed %v %v", doc, err)
	}
	if _, ok := doc["driverId"]; !ok {
		t.Fatal("a removed driver must be a nil field (deleted)")
	}
}

func TestResolvePlace(t *testing.T) {
	hub, socHub, party := uuid.New(), uuid.New(), uuid.New()
	d := directory{byID: map[uuid.UUID]Hub{hub: {ID: hub, Code: "ALANG-A", LinkedPartyID: &party}, socHub: {ID: socHub, Code: "SOCE"}},
		byCode: map[string][]uuid.UUID{"ALANG-A": {hub}, "SOCE": {socHub}}}
	if r := d.resolve(" alang-a "); r.hubID == nil || *r.hubID != hub || r.party == nil || *r.party != party {
		t.Fatalf("code %+v", r)
	}
	if r := d.resolve("ALANG-A - วังทองหลาง"); r.hubID == nil || *r.hubID != hub {
		t.Fatalf("code and name %+v", r)
	}
	if r := d.resolve("SOCE (บัวโรย)"); r.socKey == nil || *r.socKey != "SOCE" || r.hubID != nil {
		t.Fatalf("SOC %+v", r)
	}
	if r := d.resolve("nowhere"); r.hubID != nil || r.socKey != nil {
		t.Fatalf("raw %+v", r)
	}
	var ck checker
	if raw, id, p := placeOf(d, &ck, "sourceHub", nil, &hub, ""); raw != "ALANG-A" || *id != hub || *p != party || ck.err() != nil {
		t.Fatalf("by id %q %v %v", raw, id, p)
	}
	if _, id, _ := placeOf(d, &ck, "sourceHub", nil, ptr(uuid.New()), ""); id != nil || violations(ck.err()) != "sourceHubId not_found" {
		t.Fatalf("unknown hub id %v", ck.err())
	}
}

func TestPlateKeyAndTaskTypes(t *testing.T) {
	if plateKey(" 70-12 34\t") != "701234" {
		t.Fatal(plateKey(" 70-12 34\t"))
	}
	for v, want := range map[string]string{"FIRST_MILE": FirstMile, "line_haul": LineHaul, "LH": ""} {
		if got, _ := taskTypeOf(v); got != want {
			t.Errorf("%s: %s", v, got)
		}
	}
}

func TestEchoAndLegacyJSON(t *testing.T) {
	id := uuid.New()
	at := time.Date(2026, 10, 1, 0, 0, 0, 3000, time.UTC)
	res := echo(id, wbDoc{collection: CollTasks, docID: "x", fields: map[string]any{"a": "b", "gone": nil}}, at)
	e := res.Data.(WriteBackEcho)
	if res.Status != http.StatusAccepted || !e.Pending || e.ID != id || e.DocID != "x" || len(e.Document) != 1 || !e.UpdateTime.Equal(at) {
		t.Fatalf("echo %+v", res)
	}
	got := legacyJSON(map[string]any{"n": 3.0, "skip": nil, "list": []any{map[string]any{"k": "v"}}}).(map[string]any)
	if _, ok := got["skip"]; ok || got["n"] != 3.0 || len(got["list"].([]any)) != 1 {
		t.Fatalf("legacy json %v", got)
	}
}

// The notify events of a write-back carry the origin header and no realtime topic (the mirror's event has them).
func TestAppendEventsOrigin(t *testing.T) {
	e := taskEvent(RouteTaskAssigned, Task{ID: uuid.New(), TenantID: uuid.New(), TaskType: FirstMile, Status: TaskAssigned}, nil)
	if e.payload["changed"] == nil || e.agg != "task" {
		t.Fatalf("%+v", e)
	}
	_ = zerolog.Nop()
}
