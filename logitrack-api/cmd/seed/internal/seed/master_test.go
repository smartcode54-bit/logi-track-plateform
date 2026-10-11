package seed

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/vehicleclass"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// generatedTables are the tables whose demo and load counts the generators make exact: the identities of T16 and the
// master data of T26 (issue #46). T35 and T43 add their tables here when their generators land.
var generatedTables = []string{"tenants", "billing_parties", "customers", "companies", "users", "memberships",
	"auth_identities", "user_platform_roles", "user_scopes", "role_capability_overrides", "hubs", "hub_name_aliases",
	"hub_soc_distances", "drivers", "trucks", "truck_assignments"}

// specCounts reads the per-profile table of Appendix D §D.1.2: table -> [smoke, demo, load] as written ("~45",
// "1,199"). Rows naming two tables ("chats / chat_messages") are left out.
func specCounts(t *testing.T) map[string][3]string {
	t.Helper()
	doc, err := os.ReadFile(appendixD)
	if err != nil {
		t.Fatalf("Appendix D: %v", err)
	}
	text := string(doc)
	start := strings.Index(text, "### D.1.2 Profiles and volumes")
	end := strings.Index(text, "### D.1.3")
	if start < 0 || end < start {
		t.Fatal("Appendix D has no §D.1.2")
	}
	rowRx := regexp.MustCompile(`(?m)^\| ([a-z_]+) \| ([^|]+) \| ([^|]+) \| ([^|]+) \|`)
	out := map[string][3]string{}
	for _, m := range rowRx.FindAllStringSubmatch(text[start:end], -1) {
		out[m[1]] = [3]string{strings.TrimSpace(m[2]), strings.TrimSpace(m[3]), strings.TrimSpace(m[4])}
	}
	if len(out) < 30 {
		t.Fatalf("read %d rows of the §D.1.2 table", len(out))
	}
	return out
}

// exactCount parses a count of the §D.1.2 table; ok is false for an approximate target ("~45").
func exactCount(v string) (int, bool) {
	if strings.HasPrefix(v, "~") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.ReplaceAll(v, ",", ""))
	return n, err == nil
}

// AC of issue #46: the counts of the plans equal the Appendix D §D.1.2 table: every smoke count, and the demo and load
// counts of the identity and master-data tables, which the generators make exact.
func TestCountsMatchAppendixTable(t *testing.T) {
	spec := specCounts(t)
	for col, prof := range []Profile{ProfileSmoke, ProfileDemo, ProfileLoad} {
		p := plan(t, prof)
		tables := generatedTables
		if prof == ProfileSmoke {
			tables = nil
			for table := range spec {
				tables = append(tables, table)
			}
		}
		for _, table := range tables {
			row, ok := spec[table]
			if !ok {
				t.Errorf("§D.1.2 has no row for %s", table)
				continue
			}
			want, exact := exactCount(row[col])
			if !exact {
				t.Errorf("§D.1.2 %s %s is %q: a generated count is exact", table, prof, row[col])
				continue
			}
			if got, known := p.Expected[table]; !known || got != want {
				t.Errorf("%s %s: the plan has %d rows, §D.1.2 says %d", prof, table, got, want)
			}
		}
	}
}

// Demo and load only append: the smoke rows are unchanged in both, and every demo row is in load unchanged, so a
// database seeded with an older demo takes the new rows with --mode upsert. Two builds are equal (invariant 12), and
// nothing is written for the quarantine tenant.
func TestProfilesOnlyAppend(t *testing.T) {
	smoke, demo, load := plan(t, ProfileSmoke), plan(t, ProfileDemo), plan(t, ProfileLoad)
	for _, table := range LoadOrder {
		s, d, l := smoke.Tables[table], demo.Tables[table], load.Tables[table]
		if len(d) < len(s) || !reflect.DeepEqual(d[:len(s)], s) {
			t.Errorf("demo changes the smoke rows of %s", table)
		}
		if len(l) < len(d) || !reflect.DeepEqual(l[:len(d)], d) {
			t.Errorf("load changes the demo rows of %s", table)
		}
	}
	for _, p := range []Profile{ProfileDemo, ProfileLoad} {
		if !reflect.DeepEqual(plan(t, p).Tables, plan(t, p).Tables) {
			t.Errorf("two builds of %s differ", p)
		}
	}
	// The quarantine tenant comes from migration 0002 (R56): no profile inserts it, and no generated row is stamped
	// with it (the smoke quarantine task TK25 is the one row that is, on purpose).
	q := QuarantineTenantID.String()
	for _, table := range LoadOrder {
		for i, r := range load.Tables[table] {
			if r.Str("id") == q || (i >= len(smoke.Tables[table]) && r.Str("tenant_id") == q) {
				t.Errorf("load %s writes a row of the quarantine tenant", table)
			}
		}
	}
}

// The synthetic route reproduces the metres and seconds of the smoke pair ALANG-A <-> SOCE (Appendix D §D.4.7);
// kilometres and minutes follow the job's Math.round(x * 100) / 100, which gives 29.41 km where the hand-written smoke
// literal says 29.40 (the smoke row is kept as it is). The LINE group ids follow the §D.1.9 rule the smoke customers
// use.
func TestSyntheticRouteAndLineIDsMatchSmoke(t *testing.T) {
	m, s := syntheticRoute(13.7766, 100.6087, 13.6157, 100.7368)
	if m != 29405 || s != 3024 {
		t.Fatalf("ALANG-A -> SOCE: %d m, %d s; the smoke fixture has 29405 m, 3024 s", m, s)
	}
	if km, mins := fmt.Sprintf("%.2f", jsmath.Round2(float64(m)/1000)), fmt.Sprintf("%.2f", jsmath.Round2(float64(s)/60)); km != "29.41" || mins != "50.40" {
		t.Fatalf("km %s, min %s; the job rounds 29405 m and 3024 s to 29.41 and 50.40", km, mins)
	}
	for _, r := range plan(t, ProfileSmoke).Tables["customers"] {
		if got := lineGroupID(r.Str("code")); got != r.Str("line_group_id") {
			t.Errorf("customer %s: lineGroupID %s, fixture %s", r.Str("code"), got, r.Str("line_group_id"))
		}
	}
	if id := legacyDocID("AsgDoc", "D11C"); len(id) != 20 || !strings.HasPrefix(id, "seed") {
		t.Errorf("legacy doc id %q is not 20 characters starting seed", id)
	}
}

// byID indexes rows by id.
func byID(rows []Row) map[string]Row {
	out := make(map[string]Row, len(rows))
	for _, r := range rows {
		out[r.Str("id")] = r
	}
	return out
}

// The master data of demo and load keeps the rules the services and Appendix D §D.3 enforce: invariant 8 (no alias or
// hub name is a hub code, aliases unique), the network rule, the distance rule (both directions, one network, the
// stand-by SOC skipped, values from the synthetic route), the truck form's plates and the vehicle-class map, and
// tenancy (#10a): every assignment is in its truck's tenant and binds a driver of that tenant, or a driver who holds a
// driver membership there (R24).
func TestMasterDataRules(t *testing.T) {
	for _, prof := range []Profile{ProfileDemo, ProfileLoad} {
		t.Run(string(prof), func(t *testing.T) {
			p := plan(t, prof)
			checkHubs(t, p)
			checkDistances(t, p)
			checkFleet(t, p)
		})
	}
}

func checkHubs(t *testing.T, p *Plan) {
	t.Helper()
	parties := byID(p.Tables["billing_parties"])
	customers := byID(p.Tables["customers"])
	codes := map[string]string{}
	for _, h := range p.Tables["hubs"] {
		code := strings.ToUpper(h.Str("source_id"))
		if codes[code] != "" {
			t.Errorf("hub code %s twice", code)
		}
		codes[code] = h.Str("id")
		cust := ""
		if party := parties[h.Str("linked_party_id")]; party != nil {
			cust = customers[party.Str("customer_id")].Str("code")
		}
		if want := hubs.NetworkGroup(h.Str("source_id"), cust); h.Str("network") != want {
			t.Errorf("hub %s: network %s, NetworkGroup gives %s", code, h.Str("network"), want)
		}
	}
	for _, h := range p.Tables["hubs"] {
		for _, col := range []string{"name_th", "name_en"} {
			if id := codes[strings.ToUpper(strings.TrimSpace(h.Str(col)))]; id != "" && id != h.Str("id") {
				t.Errorf("hub %s: %s %q is another hub's code (invariant 8)", h.Str("source_id"), col, h.Str(col))
			}
		}
	}
	aliases := map[string]bool{}
	for _, a := range p.Tables["hub_name_aliases"] {
		name := strings.ToLower(a.Str("alias"))
		if aliases[name] {
			t.Errorf("alias %q twice (citext primary key)", a.Str("alias"))
		}
		aliases[name] = true
		if codes[strings.ToUpper(a.Str("alias"))] != "" {
			t.Errorf("alias %q equals a hub code (invariant 8)", a.Str("alias"))
		}
		if _, ok := byID(p.Tables["hubs"])[a.Str("hub_id")]; !ok {
			t.Errorf("alias %q names no hub", a.Str("alias"))
		}
	}
}

func checkDistances(t *testing.T, p *Plan) {
	t.Helper()
	hubRows := byID(p.Tables["hubs"])
	type pt struct {
		id, net  string
		lat, lng float64
	}
	var hubPts []pt
	socs := map[string]pt{} // network|key
	for _, h := range p.Tables["hubs"] {
		lat, ok1 := floatOf(h["latitude"])
		lng, ok2 := floatOf(h["longitude"])
		if !ok1 || !ok2 {
			continue
		}
		x := pt{h.Str("id"), h.Str("network"), lat, lng}
		if h.Str("station_type") == hubs.StationHub {
			hubPts = append(hubPts, x)
		} else if !strings.HasPrefix(h.Str("source_id"), "0") {
			socs[x.net+"|"+hubs.NormalizeSocKey(h.Str("source_id"))] = x
		}
	}
	want := map[string]bool{}
	for _, h := range hubPts {
		for k, s := range socs {
			if s.net == h.net {
				key := strings.SplitN(k, "|", 2)[1]
				want[h.id+"|"+key+"|"+hubs.DirectionHubToSOC], want[h.id+"|"+key+"|"+hubs.DirectionSOCToHub] = true, true
			}
		}
	}
	got := map[string]bool{}
	for _, d := range p.Tables["hub_soc_distances"] {
		k := d.Str("hub_id") + "|" + d.Str("soc_key") + "|" + d.Str("direction")
		if got[k] {
			t.Errorf("distance %s twice", k)
		}
		got[k] = true
		h := hubRows[d.Str("hub_id")]
		s := socs[h.Str("network")+"|"+d.Str("soc_key")]
		if d.Str("network") != h.Str("network") || s.id == "" {
			t.Errorf("distance %s crosses networks or names no SOC", k)
			continue
		}
		hl, _ := floatOf(h["latitude"])
		hg, _ := floatOf(h["longitude"])
		m, sec := syntheticRoute(hl, hg, s.lat, s.lng)
		if d["distance_m"] != intNum(m) || d["duration_s"] != intNum(sec) {
			t.Errorf("distance %s: %v m, %v s; the synthetic route gives %d m, %d s", k, d["distance_m"], d["duration_s"], m, sec)
		}
		generated := p.Symbols.SymbolOf(uuid.MustParse(d.Str("id"))) == "" // the smoke literal km is hand-written
		if generated && (d.Str("distance_km") != fmt.Sprintf("%.2f", jsmath.Round2(float64(m)/1000)) ||
			d.Str("duration_min") != fmt.Sprintf("%.2f", jsmath.Round2(float64(sec)/60))) {
			t.Errorf("distance %s: %s km, %s min; the job rounds %d m, %d s otherwise", k, d.Str("distance_km"), d.Str("duration_min"), m, sec)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%d distance rows, the distance rule gives %d pairs", len(got), len(want))
	}
}

func checkFleet(t *testing.T, p *Plan) {
	t.Helper()
	trucks, drivers := byID(p.Tables["trucks"]), byID(p.Tables["drivers"])
	type key struct{ user, tenant string }
	members := map[key]string{} // driver memberships -> status
	roles := map[string]map[string]bool{}
	for _, m := range p.Tables["memberships"] {
		if m.Str("role") == "driver" {
			members[key{m.Str("user_id"), m.Str("tenant_id")}] = m.Str("status")
		}
		if m.Str("status") == "active" {
			if roles[m.Str("tenant_id")] == nil {
				roles[m.Str("tenant_id")] = map[string]bool{}
			}
			roles[m.Str("tenant_id")][m.Str("role")] = true
		}
	}
	plates := map[string]bool{}
	for _, tr := range p.Tables["trucks"] {
		plate := tr.Str("license_plate")
		if plates[tr.Str("tenant_id")+plate] {
			t.Errorf("plate %s twice in one tenant", plate)
		}
		plates[tr.Str("tenant_id")+plate] = true
		if !fleet.ValidPlate(plate) {
			t.Errorf("plate %s fails the truck form's rule", plate)
		}
		var want any
		if c := vehicleclass.Of(tr.Str("type_raw")); c != nil {
			want = *c
		}
		if tr["vehicle_class"] != want {
			t.Errorf("truck %s: class %v, vehicleclass.Of(%q) gives %v", plate, tr["vehicle_class"], tr.Str("type_raw"), want)
		}
	}
	for _, d := range p.Tables["drivers"] {
		if u := d.Str("user_id"); u != "" && members[key{u, d.Str("tenant_id")}] != "active" {
			t.Errorf("driver %s signs in without an active driver membership in its tenant", d.Str("id_card"))
		}
		if thaiID(d.Str("id_card")[:12]) != d.Str("id_card") {
			t.Errorf("driver %s: national ID fails the check digit", d.Str("id_card"))
		}
	}
	activeOf := map[string]string{}
	trucksActive := map[string]bool{}
	for _, a := range p.Tables["truck_assignments"] {
		tr, d := trucks[a.Str("truck_id")], drivers[a.Str("driver_id")]
		if tr == nil || d == nil {
			t.Fatalf("assignment %s names no truck or driver", a.Str("id"))
		}
		if a.Str("tenant_id") != tr.Str("tenant_id") {
			t.Errorf("assignment %s is not in its truck's tenant", a.Str("id"))
		}
		if d.Str("tenant_id") != a.Str("tenant_id") && members[key{d.Str("user_id"), a.Str("tenant_id")}] == "" {
			t.Errorf("assignment %s binds a driver of another tenant without a driver membership there (#10a)", a.Str("id"))
		}
		if a.Str("status") == "active" {
			if activeOf[a.Str("driver_id")] != "" || trucksActive[a.Str("truck_id")] {
				t.Errorf("assignment %s: a second active assignment of its driver or truck", a.Str("id"))
			}
			activeOf[a.Str("driver_id")], trucksActive[a.Str("truck_id")] = a.Str("id"), true
		}
	}
	for _, d := range p.Tables["drivers"] {
		if cur := d.Str("current_assignment_id"); cur != activeOf[d.Str("id")] {
			t.Errorf("driver %s: current assignment %q, its active assignment %q", d.Str("id_card"), cur, activeOf[d.Str("id")])
		}
	}
	// Users of every role: the own fleet and both carriers have staff and driver logins (T16 personas and T26
	// drivers); every load carrier has a tenant_admin, managers, operators and drivers.
	s := p.Symbols
	want := map[string][]string{
		s.MustID("TN_OWN").String(): {"tenant_admin", "manager", "operation_staff", "operator", "driver"},
		s.MustID("TN_NWR").String(): {"tenant_admin", "manager", "operation_staff", "operator", "driver"},
		s.MustID("TN_TTP").String(): {"tenant_admin", "operation_staff", "driver"},
	}
	if p.Profile == ProfileLoad {
		for c := 1; c <= loadCarriers; c++ {
			want[ID(DefaultNamespace, "tenants", fmt.Sprintf("L%02d", c)).String()] = []string{"tenant_admin", "manager", "operator", "driver"}
		}
	}
	for tenant, rs := range want {
		for _, r := range rs {
			if !roles[tenant][r] {
				t.Errorf("tenant %s has no active %s", tenant, r)
			}
		}
	}
}

// AC of issue #46: the broker driver who moved tenant is present (R24): smoke's D6 (NWR -> TTP) and the demo's d13
// (TTP -> NWR), each with its suspended driver membership and revoked assignment in the old tenant and its active
// membership, driver row and assignment in the new one.
func TestBrokerDriversWhoMovedTenant(t *testing.T) {
	for _, prof := range []Profile{ProfileSmoke, ProfileDemo, ProfileLoad} {
		p := plan(t, prof)
		s := p.Symbols
		cases := []struct{ nid, from, to string }{{"1999900000064", "TN_NWR", "TN_TTP"}}
		if prof != ProfileSmoke {
			cases = append(cases, struct{ nid, from, to string }{thaiID("199990000013"), "TN_TTP", "TN_NWR"})
		}
		for _, c := range cases {
			var d Row
			for _, r := range p.Tables["drivers"] {
				if r.Str("id_card") == c.nid {
					d = r
				}
			}
			from, to := s.MustID(c.from).String(), s.MustID(c.to).String()
			if d == nil || d.Str("tenant_id") != to || d.Str("user_id") == "" {
				t.Fatalf("%s: no linked broker driver %s in %s", prof, c.nid, c.to)
			}
			status := map[string]string{}
			for _, m := range p.Tables["memberships"] {
				if m.Str("user_id") == d.Str("user_id") && m.Str("role") == "driver" {
					status[m.Str("tenant_id")] = m.Str("status")
				}
			}
			if status[from] != "suspended" || status[to] != "active" {
				t.Errorf("%s: broker %s memberships %v, want %s suspended and %s active", prof, c.nid, status, c.from, c.to)
			}
			asg := map[string]string{}
			for _, a := range p.Tables["truck_assignments"] {
				if a.Str("driver_id") == d.Str("id") {
					asg[a.Str("tenant_id")] = a.Str("status")
				}
			}
			if asg[from] != "revoked" || asg[to] != "active" {
				t.Errorf("%s: broker %s assignments %v, want revoked in %s and active in %s", prof, c.nid, asg, c.from, c.to)
			}
		}
	}
}

// The generator refuses what the services refuse, before anything is written: a hub name that is a hub code or another
// hub's name (invariant 8, the hubs API's nameRule), a code that is a name, and a plate, national ID or GPS id twice.
func TestMasterGeneratorRefusesCollisions(t *testing.T) {
	p := plan(t, ProfileDemo)
	g := newMasterGen(p, Options{Namespace: DefaultNamespace, Anchor: DefaultAnchor})
	at := DefaultAnchor
	for _, tc := range []struct {
		h    hubSpec
		want string
	}{
		{hubSpec{code: "NEW-1", th: "SOCE"}, "is a hub code"},
		{hubSpec{code: "NEW-2", th: "ห้วยขวาง"}, "already a name"},
		{hubSpec{code: "NEW-3", th: "ใหม่", en: "Prawet 18"}, "already a name"},
		{hubSpec{code: "bang kapi", th: "ใหม่ 4"}, "is a name of"},
		{hubSpec{code: "socn", th: "ใหม่ 5"}, "exists in the plan"},
	} {
		if _, err := g.hub(tc.h, at); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("hub %+v: %v, want an error naming %q", tc.h, err, tc.want)
		}
	}
	own := &tenantRef{id: p.Symbols.MustID("TN_OWN").String(), code: "WRT", own: true}
	if err := g.truck(own, truckSpec{key: "X", plate: "6กท-2045"}, at); err == nil {
		t.Error("a plate the plan already has was accepted")
	}
	if err := g.truck(own, truckSpec{key: "Y", plate: orphanPlate}, at); err == nil {
		t.Error("the orphan plate got a trucks row")
	}
	if err := g.truck(own, truckSpec{key: "Z", plate: "9ฮฮ-9999", gps: "CT-SEED-0001"}, at); err == nil {
		t.Error("a GPS vehicle id the plan already has was accepted")
	}
	if err := g.driver(own, driverSpec{key: "D", nid: "1999900000013"}); err == nil {
		t.Error("a national ID the plan already has was accepted")
	}
}
