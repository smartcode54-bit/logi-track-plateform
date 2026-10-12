package etl

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

func set(domains ...string) map[string]bool {
	m := map[string]bool{}
	for _, d := range domains {
		m[d] = true
	}
	return m
}

// Every mapped collection but the separately loaded ones has a coexistence domain, so the plans cover the whole load.
func TestEveryMappedCollectionHasADomain(t *testing.T) {
	for _, n := range MappedCollections() {
		if _, ok := collectionDomains[n]; !ok {
			t.Errorf("%s has no coexistence domain", n)
		}
	}
	for n := range collectionDomains {
		_, fields := fieldProjections[n] // the billing fields of trips and standby records (T38): no collection of their own
		if _, ok := specOf(n); !ok && !separateCollections[n] && !fields {
			t.Errorf("%s is not a mapped collection", n)
		}
	}
	for p, c := range fieldProjections {
		if _, ok := specOf(c); !ok || !classC[c] || !slices.Contains(ProjectedCollections, p) {
			t.Errorf("field projection %s writes %s, which must be a mapped class C collection", p, c)
		}
	}
	for _, c := range ProjectedCollections {
		if _, ok := projectors[c]; !ok {
			t.Errorf("%s has no encoder", c)
		}
	}
}

// The class C collections the installed 3.x APKs write until P7b (§12.1).
var classCCollections = []string{"mobile_installations", "tasks", "trip_records", "standby_records", "incidentReport",
	"vehicle_expenses", "maintenance", "chats", "messages", "leave_requests"}

func TestPlanMirror(t *testing.T) {
	// P0: nothing PostgreSQL-owned but auth: every collection mirrored whole, in the load order, and the 3.x users
	// fields (users are PostgreSQL's from P0) right after the drivers.
	p, err := PlanMirror(set("auth"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(MappedCollections())
	want = slices.Insert(want, slices.Index(want, "drivers")+1, "users")
	if !slices.Equal(p.Names(), want) {
		t.Fatalf("P0 plan %v", p.Names())
	}
	for _, e := range p.Entries {
		if (e.Name == "users") != (e.Mode == MirrorFields) {
			t.Errorf("%s: %s", e.Name, e.Mode)
		}
	}
	// Before P0 (auth not owned) the users documents are auth-import's: never mirrored.
	if p, _ := PlanMirror(set(), nil); p.Mode("users") != "" || p.Mode("tasks") != MirrorFull {
		t.Errorf("before P0: %+v", p.Entries)
	}
	// P1: masterdata owned. Its class B collections keep their field mirror, the rest of it is not mirrored.
	p, _ = PlanMirror(set("auth", "masterdata"), nil)
	for coll, want := range map[string]string{"drivers": MirrorFields, "hubs": MirrorFields, "trucks": MirrorFields,
		"customers": "", "subcontractors": "", "truckAssignment": "", "hub_soc_distances": "", "mobile_installations": MirrorFull,
		"transactions": MirrorFull, "tasks": MirrorFull, "users": MirrorFields, "permissions_config": ""} {
		if got := p.Mode(coll); got != want {
			t.Errorf("P1 %s: %q, want %q", coll, got, want)
		}
	}
	// P3 (billing), P4 (hr), P5 (comms), P6 (all but operations): trucks lose their field mirror once billing is
	// owned (the PM alert is Go's), but every class C collection stays mirrored whole whatever its domain flag says:
	// the 3.x APKs write expenses, maintenance, leave requests and chats in Firestore until P7b (§12.1, §12.2).
	for name, owned := range map[string]map[string]bool{
		"P3": set("auth", "masterdata", "billing"),
		"P4": set("auth", "masterdata", "billing", "hr"),
		"P5": set("auth", "masterdata", "billing", "hr", "comms"),
		"P6": set("auth", "masterdata", "billing", "hr", "comms", "security", "mobile_release", "dashboard"),
		"P2": set("auth", "masterdata", "operations"),
	} {
		p, _ := PlanMirror(owned, nil)
		for _, c := range classCCollections {
			if p.Mode(c) != MirrorFull {
				t.Errorf("%s %s: %q, want full", name, c, p.Mode(c))
			}
		}
		if owned["billing"] && (p.Mode("trucks") != "" || p.Mode("customer_rate_entries") != "") {
			t.Errorf("%s plan %+v", name, p.Entries)
		}
	}
	if p, _ := PlanMirror(set("auth", "masterdata", "billing"), nil); p.Mode("transactions") != MirrorFull || p.Mode("payroll") != MirrorFull {
		t.Errorf("P3 plan %+v", p.Entries)
	}
	// P7b: every domain: the class B field mirrors, the users fields and class C remain listed; the sync itself is
	// switched off then (ETL_SYNC_ENABLED), which stops them all.
	p, _ = PlanMirror(set("auth", "masterdata", "operations", "billing", "hr", "comms", "security", "mobile_release", "dashboard"), nil)
	if strings.Join(p.Names(), ",") != "hubs,drivers,users,mobile_installations,tasks,trip_records,standby_records,incidentReport,"+
		"vehicle_expenses,maintenance,chats,messages,leave_requests" {
		t.Errorf("P7b plan %v", p.Names())
	}
	// A pin narrows; an unknown name is refused.
	p, _ = PlanMirror(set("auth"), []string{"tasks", "drivers", "users"})
	if strings.Join(p.Names(), ",") != "drivers,users,tasks" {
		t.Errorf("pinned plan %v", p.Names())
	}
	if _, err := PlanMirror(nil, []string{"tasks", "permissions_config"}); err == nil || !strings.Contains(err.Error(), "ETL_MIRROR_COLLECTIONS") {
		t.Errorf("a pin naming permissions_config: %v", err)
	}
}

func TestPlanProjection(t *testing.T) {
	if c, _ := PlanProjection(set("auth"), nil); len(c) != 0 {
		t.Errorf("a Firestore-owned masterdata is projected: %v", c)
	}
	// P1: the master data; the version gate settings/mobile_app, the holidays, the billing fields of trips and standby
	// records and the broadcasts stay Firestore's (mobile_release, hr, billing and comms are not owned).
	without := func(names ...string) []string {
		return slices.DeleteFunc(slices.Clone(ProjectedCollections), func(s string) bool { return slices.Contains(names, s) })
	}
	billing := []string{ProjectionTripBilling, ProjectionStandbyBilling}
	c, _ := PlanProjection(set("auth", "masterdata"), nil)
	if !slices.Equal(c, without(append([]string{"settings", "holidays", "broadcasts"}, billing...)...)) {
		t.Errorf("P1 projection %v", c)
	}
	// P3 (billing owned): the price fields join (T38), while trips and standby records stay mirrored whole (class C).
	c, _ = PlanProjection(set("auth", "masterdata", "billing"), nil)
	if !slices.Equal(c, without("settings", "holidays", "broadcasts")) {
		t.Errorf("P3 projection %v", c)
	}
	if p, _ := PlanMirror(set("auth", "masterdata", "billing"), nil); p.Mode("trip_records") != MirrorFull || p.Mode("standby_records") != MirrorFull {
		t.Errorf("P3 mirror of trips %q, standby %q", p.Mode("trip_records"), p.Mode("standby_records"))
	}
	// P4 (hr owned, T45): the holidays join, alone when hr is the only owned domain.
	if c, _ := PlanProjection(set("auth", "masterdata", "billing", "hr"), nil); !slices.Equal(c, without("settings", "broadcasts")) {
		t.Errorf("P4 projection %v", c)
	}
	// P5 (comms owned, T48): the broadcasts join, alone when comms is the only owned domain; chats and messages stay
	// mirrored whole (class C), broadcasts are no longer mirrored (no second writer).
	if c, _ := PlanProjection(set("auth", "masterdata", "billing", "hr", "comms"), nil); !slices.Equal(c, without("settings")) {
		t.Errorf("P5 projection %v", c)
	}
	if c, _ := PlanProjection(set("comms"), nil); strings.Join(c, ",") != "broadcasts" {
		t.Errorf("comms projection %v", c)
	}
	if p, _ := PlanMirror(set("auth", "comms"), nil); p.Mode("broadcasts") != "" || p.Mode("chats") != MirrorFull || p.Mode("messages") != MirrorFull {
		t.Errorf("comms owned: broadcasts %q, chats %q, messages %q", p.Mode("broadcasts"), p.Mode("chats"), p.Mode("messages"))
	}
	if c, _ := PlanProjection(set("hr"), nil); strings.Join(c, ",") != "holidays" {
		t.Errorf("hr projection %v", c)
	}
	// Once hr is PostgreSQL's, holidays are no longer mirrored (no second writer, no echo); leave requests (class C) are.
	if p, _ := PlanMirror(set("auth", "hr"), nil); p.Mode("holidays") != "" || p.Mode("leave_requests") != MirrorFull {
		t.Errorf("hr owned: holidays %q, leave_requests %q", p.Mode("holidays"), p.Mode("leave_requests"))
	}
	// P6 (mobile_release owned): the version gate joins; alone, it is the only projected collection.
	if c, _ := PlanProjection(set("auth", "masterdata", "mobile_release"), nil); !slices.Equal(c, without(append([]string{"holidays", "broadcasts"}, billing...)...)) {
		t.Errorf("P6 projection %v", c)
	}
	if c, _ := PlanProjection(set("auth", "masterdata", "billing", "hr", "comms", "mobile_release"), nil); !slices.Equal(c, ProjectedCollections) {
		t.Errorf("P6 projection with billing, hr and comms %v", c)
	}
	if c, _ := PlanProjection(set("mobile_release"), nil); strings.Join(c, ",") != "settings" {
		t.Errorf("mobile_release projection %v", c)
	}
	// Once the gate is PostgreSQL's, the settings collection is no longer mirrored (no second writer, no echo).
	if p, _ := PlanMirror(set("auth", "mobile_release"), nil); p.Mode("settings") != "" {
		t.Errorf("settings mirrored while projected: %q", p.Mode("settings"))
	}
	c, _ = PlanProjection(set("masterdata"), []string{"hubs", "drivers"})
	if strings.Join(c, ",") != "hubs,drivers" {
		t.Errorf("pinned projection %v", c)
	}
	if _, err := PlanProjection(set("masterdata"), []string{"chats"}); err == nil {
		t.Error("a pin outside the projected collections was accepted")
	}
	if f := ForeignFields("trucks", set("masterdata")); strings.Join(f, ",") != "activeMaintenanceId,currentMileage,lastAlertMileage,lastServiceDate,nextServiceMileage,truckStatus" {
		t.Errorf("trucks foreign fields %v", f)
	}
	if f := ForeignFields("trucks", set("masterdata", "billing")); len(f) != 0 {
		t.Errorf("trucks foreign fields with billing owned %v", f)
	}
}

func TestShadowTargets(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	cases := []struct {
		key, body string
		want      []ShadowTarget
	}{
		{"hubs.changed", `{"ids":["` + a.String() + `"]}`, []ShadowTarget{{Collection: "hubs", IDs: []uuid.UUID{a}},
			{Collection: "hub_soc_distances", IDs: []uuid.UUID{}, Hubs: []uuid.UUID{a}}, {Collection: "soc_hub_distances", IDs: []uuid.UUID{}, Hubs: []uuid.UUID{a}}}},
		{"customers.changed", `{"ids":["` + a.String() + `","` + b.String() + `"]}`, []ShadowTarget{{Collection: "customers", IDs: []uuid.UUID{a, b}},
			{Collection: "hubs", Customers: []uuid.UUID{a, b}}, {Collection: "drivers", Customers: []uuid.UUID{a, b}}}},
		{"companies.changed", `{"ids":["` + a.String() + `"]}`, []ShadowTarget{{Collection: "companies", IDs: []uuid.UUID{a}}}},
		{"truck.renewed", `{"entity":"truck","id":"` + a.String() + `"}`, []ShadowTarget{{Collection: "trucks", IDs: []uuid.UUID{a}}}},
		{"driver.updated", `{"entity":"driver","id":"` + a.String() + `","changed":["mobile"]}`, []ShadowTarget{{Collection: "drivers", IDs: []uuid.UUID{a}}}},
		{"assignment.revoked", `{"entity":"truck_assignment","id":"` + a.String() + `","truckId":"` + b.String() + `","driverId":"` + c.String() + `"}`,
			[]ShadowTarget{{Collection: "truckAssignment", IDs: []uuid.UUID{a}}, {Collection: "trucks", IDs: []uuid.UUID{b}}, {Collection: "drivers", IDs: []uuid.UUID{c}}}},
		{"tenant.updated", `{"tenantId":"` + a.String() + `","fields":["phone"]}`, []ShadowTarget{{Collection: "subcontractors", IDs: []uuid.UUID{a}},
			{Collection: "hubs", Tenants: []uuid.UUID{a}}}},
		{"tenant.created", `{"tenantId":"` + a.String() + `"}`, []ShadowTarget{{Collection: "subcontractors", IDs: []uuid.UUID{a}}}},
		{"settings.changed", `{"key":"distances_last_calculated"}`, []ShadowTarget{{Collection: "hub_soc_distances", All: true},
			{Collection: "soc_hub_distances", All: true}, {Collection: "metadata", All: true}}},
		{"settings.changed", `{"key":"mobile_app","flavor":"prod"}`, []ShadowTarget{{Collection: "settings", All: true}}},
		{"settings.changed", `{"key":"branding"}`, nil},
		{"holidays.changed", `{"entity":"holiday","year":2026,"ids":["` + a.String() + `"],"deletedDocIds":["hol_legacy","` + b.String() + `"]}`,
			[]ShadowTarget{{Collection: "holidays", IDs: []uuid.UUID{a}, DeletedDocIDs: []string{"hol_legacy", b.String()}}}},
		{"holidays.changed", `{}`, []ShadowTarget{{Collection: "holidays", IDs: []uuid.UUID{}}}},
		{"broadcast.created", `{"entity":"broadcast","id":"` + a.String() + `","title":"t"}`,
			[]ShadowTarget{{Collection: "broadcasts", IDs: []uuid.UUID{a}}}},
		{"broadcast.voided", `{"entity":"broadcast","id":"` + b.String() + `","legacyDocId":"bc_1"}`,
			[]ShadowTarget{{Collection: "broadcasts", IDs: []uuid.UUID{b}}}},
		{"trip.priced", `{"id":"` + a.String() + `","priced":true}`, []ShadowTarget{{Collection: ProjectionTripBilling, IDs: []uuid.UUID{a}}}},
		{"trip.repriced", `{"id":"` + b.String() + `","billingDateMoved":true}`, []ShadowTarget{{Collection: ProjectionTripBilling, IDs: []uuid.UUID{b}}}},
		{"standby.priced", `{"id":"` + c.String() + `"}`, []ShadowTarget{{Collection: ProjectionStandbyBilling, IDs: []uuid.UUID{c}}}},
	}
	for _, tc := range cases {
		got, err := ShadowTargets(tc.key, []byte(tc.body))
		if err != nil {
			t.Errorf("%s: %v", tc.key, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: %+v", tc.key, got)
			continue
		}
		for i := range got {
			g, w := got[i], tc.want[i]
			if g.Collection != w.Collection || g.All != w.All || !sameIDs(g.IDs, w.IDs) || !sameIDs(g.Hubs, w.Hubs) ||
				!sameIDs(g.Customers, w.Customers) || !sameIDs(g.Tenants, w.Tenants) ||
				strings.Join(g.DeletedDocIDs, ",") != strings.Join(w.DeletedDocIDs, ",") {
				t.Errorf("%s target %d: %+v, want %+v", tc.key, i, g, w)
			}
		}
	}
	for _, bad := range []struct{ key, body string }{{"truck.created", `{"id":"not-a-uuid"}`}, {"customers.changed", `[`},
		{"user.created", `{}`}, {"trip.priced", `{"id":"x"}`}, {"broadcast.created", `{"id":"x"}`}} {
		if _, err := ShadowTargets(bad.key, []byte(bad.body)); err == nil {
			t.Errorf("%s %s accepted", bad.key, bad.body)
		}
	}
}

func TestDiffKeepsLegacySpellingsAndWritesOnlyChanges(t *testing.T) {
	at := time.Date(2026, 8, 1, 3, 0, 0, 123000000, time.UTC)
	b := newDoc("customers/c1").
		set("code", "SPX").eq("code", func(cur any, has bool) bool { s, _ := cur.(string); return has && strings.ToUpper(s) == "SPX" }).
		set("name", "Shopee").set("paymentTermsDays", int64(30)).set("distanceKm", jsNumber(12)).set("updatedAt", at).
		set("description", nil).set("serviceRegions", anyStrings(nil)).set("logoUrl", "https://firebasestorage.googleapis.com/v0/b/x/o/a.png?alt=media").
		eq("logoUrl", sameFile("https://firebasestorage.googleapis.com/v0/b/x/o/a.png?alt=media")).
		set("probationPassed", false).eq("probationPassed", sameBool(false, false)).created(at)
	d := b.doc()
	cur := map[string]any{"code": "spx", "name": "Shopee", "paymentTermsDays": 30.0, "distanceKm": "12", "updatedAt": "2026-08-01T03:00:00.123Z",
		"description": "", "serviceRegions": []any{}, "logoUrl": "https://firebasestorage.googleapis.com/v0/b/x/o/a.png?alt=media&token=t",
		"fcmToken": "not ours"}
	if fields, mask := d.Diff(cur); len(mask) != 0 {
		t.Fatalf("an equivalent document is rewritten: %v %v", mask, fields)
	}
	cur["name"], cur["description"] = "Shopee Express", "stale"
	fields, mask := d.Diff(cur)
	slices.Sort(mask)
	if strings.Join(mask, ",") != "description,name" || fields["name"] != "Shopee" || len(fields) != 1 {
		t.Fatalf("diff %v %v", mask, fields)
	}
	// A new document gets every field with a value, defaults included.
	fields, mask = d.Diff(nil)
	if fields["probationPassed"] != false || fields["createdAt"] != (dump.Timestamp{Time: at}) || slices.Contains(mask, "description") {
		t.Fatalf("new document %v %v", mask, fields)
	}
	// legacyEqual: numbers by value, times by instant, arrays and maps deeply with blank-equal absences.
	for _, c := range []struct {
		want, cur any
		same      bool
	}{
		{int64(5), 5.0, true}, {5.5, "5.5", true}, {5.5, 5.4, false}, {"a", "b", false}, {true, true, true},
		{dump.Timestamp{Time: at}, at.UnixMilli(), true}, {dump.Timestamp{Time: at}, "2026-08-01T10:00:00.123+07:00", true},
		{[]any{"a"}, []any{"a"}, true}, {[]any{"a"}, []any{"b"}, false}, {[]any{}, nil, true},
		{map[string]any{"k": "v"}, map[string]any{"k": "v", "x": ""}, true}, {map[string]any{"k": "v"}, map[string]any{"k": "v", "x": "y"}, false},
	} {
		if got := legacyEqual(c.want, c.cur); got != c.same {
			t.Errorf("legacyEqual(%v, %v) = %v", c.want, c.cur, got)
		}
	}
	if jsNumber(3) != int64(3) || jsNumber(2.5) != 2.5 {
		t.Error("jsNumber")
	}
	if d := newDoc("trucks/t").set("a", "x").set("currentMileage", int64(1)).doc().without([]string{"currentMileage"}); strings.Join(d.Mask, ",") != "a" {
		t.Errorf("without %v", d.Mask)
	}
}

// Keep rules leave a value the load could not resolve as Firestore holds it, and only then.
func TestKeepUnresolvedValues(t *testing.T) {
	d := newDoc("hubs/h").set("source_id", "H1").set("linkedCustomerId", nil).set("customerLinkKind", nil).set("linkedCustomerName", nil).
		keepIf("linkedCustomerId", "", unknownTo(map[string]bool{"cust_known": true}), "linkedCustomerId", "customerLinkKind", "linkedCustomerName").
		set("currentAssignment", nil).keepIf("currentAssignment", "assignmentId", unknownTo(map[string]bool{"ta_1": true}), "currentAssignment").doc()
	unresolved := map[string]any{"source_id": "H1", "linkedCustomerId": "cust_gone", "customerLinkKind": "customer", "linkedCustomerName": "Gone",
		"currentAssignment": map[string]any{"assignmentId": "ta_bad"}}
	if _, mask := d.Diff(unresolved); len(mask) != 0 {
		t.Errorf("unresolved references were rewritten: %v", mask)
	}
	unlinked := map[string]any{"source_id": "H1", "linkedCustomerId": "cust_known", "customerLinkKind": "customer",
		"currentAssignment": map[string]any{"assignmentId": "ta_1"}}
	_, mask := d.Diff(unlinked)
	slices.Sort(mask)
	if strings.Join(mask, ",") != "currentAssignment,customerLinkKind,linkedCustomerId" {
		t.Errorf("a link removed in PostgreSQL is not removed: %v", mask)
	}
}

func TestEquivalences(t *testing.T) {
	day := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	same := sameDay(day)
	for cur, want := range map[any]bool{"2026-03-04": true, "2026-03-05": false,
		dump.Timestamp{Time: time.Date(2026, 3, 3, 17, 0, 0, 0, time.UTC)}: true, // Bangkok midnight
		dump.Timestamp{Time: time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)}:  true, // UTC midnight: 07:00 in Bangkok
		"not a date": false} {
		if same(cur, true) != want {
			t.Errorf("sameDay(%v) != %v", cur, want)
		}
	}
	status := sameCanon("on_duty", "active", nil)
	if !status("On-Duty", true) || status("Active", true) || status(nil, false) {
		t.Error("sameCanon on_duty")
	}
	if !sameCanon("active", "active", nil)(nil, false) {
		t.Error("an absent status is the loader's default")
	}
	if !truckStatusLiteralSame("active", "Available") || truckStatusLiteral("insurance_claim", nil) != "insurance-claim" {
		t.Error("truck status")
	}
	legacy := "Available"
	if truckStatusLiteral("active", &legacy) != "Available" || truckStatusLiteral("sold", &legacy) != "sold" {
		t.Error("a stale legacy status is not kept")
	}
	if !keepSnapshot("70-1234", true) || keepSnapshot("", true) || keepSnapshot(nil, false) {
		t.Error("keepSnapshot")
	}
	hist := []historyRow{{status: "Active", at: time.Date(2026, 1, 10, 3, 0, 0, 0, time.UTC)}}
	cur := []any{map[string]any{"status": "Active", "date": "2026-01-10T03:00:00.000Z", "changedBy": ""},
		map[string]any{"status": "Active", "date": "not a date", "changedBy": "x"}}
	if !sameTruckHistory(hist)(cur, true) {
		t.Error("an unloadable entry makes the history differ")
	}
	if sameTruckHistory(nil)(cur, true) {
		t.Error("a removed entry is equivalent")
	}
}

func truckStatusLiteralSame(status, cur string) bool { return truckStatusCanon(cur) == status }

// sameIDs: nil and empty are the same selector.
func sameIDs(a, b []uuid.UUID) bool { return len(a) == len(b) && (len(a) == 0 || slices.Equal(a, b)) }

// A distance pair keeps any legacy id that still names it; an id of a former hub code does not.
func TestNamesPair(t *testing.T) {
	for _, c := range []struct {
		id, dir, code, soc string
		want               bool
	}{
		{"HUBA_SOCE", "hub_to_soc", "HUBA", "SOCE", true},
		{"huba_SOCE", "hub_to_soc", "HUBA", "SOCE", true},         // the legacy lower-case code
		{"HUB_A_1_SOCE", "hub_to_soc", "HUB_A_1", "SOCE", true},   // a code holding '_'
		{"HUBA_SOCE (บางนา)", "hub_to_soc", "HUBA", "SOCE", true}, // the SOC key's fold
		{"SOCE_huba", "soc_to_hub", "HUBA", "SOCE", true},
		{"HUBA_SOCE", "hub_to_soc", "HUBQ", "SOCE", false}, // the hub's code changed
		{"HUBA_SOCN", "hub_to_soc", "HUBA", "SOCE", false},
		{"SOCE_HUBA", "hub_to_soc", "HUBA", "SOCE", false}, // the other direction's shape
		{"x", "hub_to_soc", "HUBA", "SOCE", false},
	} {
		if got := namesPair(c.id, c.dir, c.code, c.soc); got != c.want {
			t.Errorf("namesPair(%q, %s, %s, %s) = %v", c.id, c.dir, c.code, c.soc, got)
		}
	}
	for id, want := range map[string]bool{"HUBA_SOCE": true, "A/B_SOCE": false, ".": false, "..": false, "__x__": false, "": false,
		"__x": true, strings.Repeat("a", 1501): false} {
		if validDocID(id) != want {
			t.Errorf("validDocID(%q) != %v", id, want)
		}
	}
	for path, want := range map[string]bool{"hub_soc_distances/HUBA_SOCE": true, "hub_soc_distances/A/B_SOCE": false,
		"drivers/d1/mobile_installations/i1": true, "hubs/..": false} {
		if validDocPath(path) != want {
			t.Errorf("validDocPath(%q) != %v", path, want)
		}
	}
}

// customerDriverIds: an entry is compared and written under its stored key whatever its case, an entry of a code no
// customer has stays, a code PostgreSQL holds and the map lacks is added, and a code changed in Go moves the entry.
func TestMergeCodes(t *testing.T) {
	known := map[string]bool{"SPX": true, "CJSF": true}
	want := map[string]any{"SPX": map[string]any{"appId": "A-001"}}
	stored := map[string]any{"spx": map[string]any{"appId": "A-001", "blank": ""}, "NOPE": map[string]any{"x": "1"}}
	d := newDoc("drivers/d").set("customerDriverIds", want)
	d.d.Merge["customerDriverIds"] = mergeCodes(want, known)
	if _, mask := d.doc().Diff(map[string]any{"customerDriverIds": stored}); len(mask) != 0 {
		t.Errorf("an equivalent map is rewritten: %v", mask)
	}
	// A Go edit of the value: written under the stored key, the unresolved entry kept.
	want2 := map[string]any{"SPX": map[string]any{"appId": "A-002"}, "CJSF": map[string]any{"empId": "E-9"}}
	got := mergeCodes(want2, known)(stored).(map[string]any)
	if fmt.Sprint(got) != fmt.Sprint(map[string]any{"spx": map[string]any{"appId": "A-002", "blank": ""}, "NOPE": map[string]any{"x": "1"},
		"CJSF": map[string]any{"empId": "E-9"}}) {
		t.Errorf("merged %v", got)
	}
	// Every value removed in Go: the entry goes; the unresolved one stays.
	got = mergeCodes(map[string]any{}, known)(map[string]any{"SPX": map[string]any{"appId": "A-001"}, "NOPE": map[string]any{"x": "1"}}).(map[string]any)
	if fmt.Sprint(got) != fmt.Sprint(map[string]any{"NOPE": map[string]any{"x": "1"}}) {
		t.Errorf("after removal %v", got)
	}
	// The customer's code changed in Go (SPX -> SPXN): the entry moves to the new code.
	known2 := map[string]bool{"SPXN": true}
	got = mergeCodes(map[string]any{"SPXN": map[string]any{"appId": "A-001"}}, known2)(map[string]any{"SPX": map[string]any{"appId": "A-001"},
		"NOPE": map[string]any{"x": "1"}}).(map[string]any)
	if fmt.Sprint(got) != fmt.Sprint(map[string]any{"SPXN": map[string]any{"appId": "A-001"}, "NOPE": map[string]any{"x": "1"}}) {
		t.Errorf("after the code change %v", got)
	}
	if mergeCodes(map[string]any{}, known)(nil) != nil {
		t.Error("no codes on either side is no field")
	}
}

// linkedCustomerName: the legacy "code — Name" (any case of the code) or the bare name are kept; a renamed party is not.
func TestLinkedPartyLabel(t *testing.T) {
	if linkedPartyLabel("SPX", "Shopee Express") != "SPX — Shopee Express" || linkedPartyLabel("", "N") != "N" || linkedPartyLabel("C", "") != "C" {
		t.Error("label")
	}
	same := sameLinkedPartyLabel("SPX", "Shopee Express")
	for cur, want := range map[any]bool{"SPX — Shopee Express": true, "spx — Shopee Express": true, "Shopee Express": true,
		"SPX — Shopee": false, "Shopee": false, nil: false} {
		if got := same(cur, cur != nil); got != want {
			t.Errorf("same(%v) = %v", cur, got)
		}
	}
}
