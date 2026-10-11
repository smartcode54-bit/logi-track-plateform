package etl

import (
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
		if _, ok := specOf(n); !ok {
			t.Errorf("%s is not a mapped collection", n)
		}
	}
	for _, c := range ProjectedCollections {
		if _, ok := projectors[c]; !ok {
			t.Errorf("%s has no encoder", c)
		}
	}
}

func TestPlanMirror(t *testing.T) {
	// P0: nothing PostgreSQL-owned but auth: every collection mirrored whole, in the load order.
	p, err := PlanMirror(set("auth"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Names(), MappedCollections()) {
		t.Fatalf("P0 plan %v", p.Names())
	}
	for _, e := range p.Entries {
		if e.Mode != MirrorFull {
			t.Errorf("%s: %s", e.Name, e.Mode)
		}
	}
	// P1: masterdata owned. Its class B collections keep their field mirror, the rest of it is not mirrored.
	p, _ = PlanMirror(set("auth", "masterdata"), nil)
	for coll, want := range map[string]string{"drivers": MirrorFields, "hubs": MirrorFields, "trucks": MirrorFields,
		"customers": "", "subcontractors": "", "truckAssignment": "", "hub_soc_distances": "", "mobile_installations": MirrorFull,
		"transactions": MirrorFull, "tasks": MirrorFull, "users": "", "permissions_config": ""} {
		if got := p.Mode(coll); got != want {
			t.Errorf("P1 %s: %q, want %q", coll, got, want)
		}
	}
	// P3: billing owned too: trucks lose their field mirror (the PM alert is Go's).
	p, _ = PlanMirror(set("auth", "masterdata", "operations", "billing"), nil)
	if p.Mode("trucks") != "" || p.Mode("vehicle_expenses") != "" || p.Mode("transactions") != MirrorFull {
		t.Errorf("P3 plan %+v", p.Entries)
	}
	// P7b: every domain: only the class B field mirrors and the installations remain (the sync is switched off then).
	p, _ = PlanMirror(set("auth", "masterdata", "operations", "billing", "hr", "comms", "security", "mobile_release", "dashboard"), nil)
	if strings.Join(p.Names(), ",") != "hubs,drivers,mobile_installations" {
		t.Errorf("P7b plan %v", p.Names())
	}
	// A pin narrows; an unknown name is refused.
	p, _ = PlanMirror(set("auth"), []string{"tasks", "drivers"})
	if strings.Join(p.Names(), ",") != "drivers,tasks" {
		t.Errorf("pinned plan %v", p.Names())
	}
	if _, err := PlanMirror(nil, []string{"tasks", "users"}); err == nil || !strings.Contains(err.Error(), "ETL_MIRROR_COLLECTIONS") {
		t.Errorf("a pin naming users: %v", err)
	}
}

func TestPlanProjection(t *testing.T) {
	if c, _ := PlanProjection(set("auth"), nil); len(c) != 0 {
		t.Errorf("a Firestore-owned masterdata is projected: %v", c)
	}
	c, _ := PlanProjection(set("auth", "masterdata"), nil)
	if !slices.Equal(c, ProjectedCollections) {
		t.Errorf("P1 projection %v", c)
	}
	c, _ = PlanProjection(set("masterdata"), []string{"hubs", "drivers"})
	if strings.Join(c, ",") != "hubs,drivers" {
		t.Errorf("pinned projection %v", c)
	}
	if _, err := PlanProjection(set("masterdata"), []string{"holidays"}); err == nil {
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
		{"customers.changed", `{"ids":["` + a.String() + `","` + b.String() + `"]}`, []ShadowTarget{{Collection: "customers", IDs: []uuid.UUID{a, b}}}},
		{"companies.changed", `{"ids":["` + a.String() + `"]}`, []ShadowTarget{{Collection: "companies", IDs: []uuid.UUID{a}}}},
		{"truck.renewed", `{"entity":"truck","id":"` + a.String() + `"}`, []ShadowTarget{{Collection: "trucks", IDs: []uuid.UUID{a}}}},
		{"driver.updated", `{"entity":"driver","id":"` + a.String() + `","changed":["mobile"]}`, []ShadowTarget{{Collection: "drivers", IDs: []uuid.UUID{a}}}},
		{"assignment.revoked", `{"entity":"truck_assignment","id":"` + a.String() + `","truckId":"` + b.String() + `","driverId":"` + c.String() + `"}`,
			[]ShadowTarget{{Collection: "truckAssignment", IDs: []uuid.UUID{a}}, {Collection: "trucks", IDs: []uuid.UUID{b}}, {Collection: "drivers", IDs: []uuid.UUID{c}}}},
		{"tenant.updated", `{"tenantId":"` + a.String() + `","fields":["phone"]}`, []ShadowTarget{{Collection: "subcontractors", IDs: []uuid.UUID{a}}}},
		{"settings.changed", `{"key":"distances_last_calculated"}`, []ShadowTarget{{Collection: "hub_soc_distances", All: true},
			{Collection: "soc_hub_distances", All: true}, {Collection: "metadata", All: true}}},
		{"settings.changed", `{"key":"mobile_app"}`, nil},
		{"holidays.changed", `{}`, nil},
		{"trip.priced", `{"id":"x"}`, nil},
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
			if g.Collection != w.Collection || g.All != w.All || !slices.Equal(g.IDs, w.IDs) || !slices.Equal(g.Hubs, w.Hubs) {
				t.Errorf("%s target %d: %+v, want %+v", tc.key, i, g, w)
			}
		}
	}
	for _, bad := range []struct{ key, body string }{{"truck.created", `{"id":"not-a-uuid"}`}, {"customers.changed", `[`},
		{"user.created", `{}`}} {
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
