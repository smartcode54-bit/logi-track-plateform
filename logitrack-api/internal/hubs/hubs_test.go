package hubs

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs/hubsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/gmaps"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// The network rule of validate/hubSchema.ts:36-49 and fn:core/distances.ts.
func TestNetworkGroup(t *testing.T) {
	for _, c := range []struct{ code, customer, want string }{
		{"SPK890174", "", NetworkSPK}, {"spk-gw", "", NetworkSPK}, {" SPK", "", NetworkSPK},
		{"ALANG-A", "SPK", NetworkSPK}, {"ALANG-A", "j&t", NetworkSPK}, {"ALANG-A", " JT ", NetworkSPK},
		{"BANGNA-SPX", "", NetworkSPX}, {"BANGNA-SPX", "SPX", NetworkSPX}, {"ALANG-A", "TTP", NetworkSPX},
		{"SOCE", "", NetworkSPX}, {"", "", NetworkSPX}, {"XSPK", "", NetworkSPX},
	} {
		if got := NetworkGroup(c.code, c.customer); got != c.want {
			t.Errorf("NetworkGroup(%q, %q) = %s, want %s", c.code, c.customer, got, c.want)
		}
	}
	for code, want := range map[string]bool{"SPX": true, "bangna-spx": true, "A_SPX": true, "A.SPX": true, "ASPX": false, "": false} {
		if HasSPXSuffix(code) != want {
			t.Errorf("HasSPXSuffix(%q) != %v", code, want)
		}
	}
}

func TestSocKeysAndStationTypes(t *testing.T) {
	for in, want := range map[string]string{
		"SOCE": "SOCE", "soce": "SOCE", "SOCE (บัวโรย)": "SOCE", "SOCN(x)": "SOCN", " socw ": "SOCW",
		"SOCEX": "SOCEX", "spk-soc": "SPK-SOC", "": "",
	} {
		if got := NormalizeSocKey(in); got != want {
			t.Errorf("NormalizeSocKey(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"SOC": "SOC", "return_center": "SOC", "HUB": "HUB", "FM_HUB": "HUB", "": "HUB", "x": "HUB"} {
		if NormalizeStationType(in) != want {
			t.Errorf("NormalizeStationType(%q)", in)
		}
	}
	for in, want := range map[string]string{"hub": "HUB", "SOC": "SOC", "LH_HUB": "HUB", "RETURN_CENTER": "SOC"} {
		if got, ok := ParseStationType(in); !ok || got != want {
			t.Errorf("ParseStationType(%q) = %q %v", in, got, ok)
		}
	}
	if _, ok := ParseStationType("DEPOT"); ok {
		t.Error("an unknown station type was accepted")
	}
}

// AC: a destination already stored as a code (SPK890174) resolves through nameToCode to itself, never back to a name
// (the "No rate: SPK-GW → ห้วยขวาง10" bug of a merged map).
func TestMapsResolveNeverTurnsACodeIntoAName(t *testing.T) {
	m := Maps{
		NameToCode: map[string]string{"ห้วยขวาง10": "SPK890174", "ประเวศ18": "SPK890146"},
		CodeToName: map[string]string{"SPK890174": "ห้วยขวาง10", "SPK890146": "ประเวศ18"},
	}
	for in, want := range map[string]string{"SPK890174": "SPK890174", " ห้วยขวาง10 ": "SPK890174", "ประเวศ18": "SPK890146",
		"unknown": "unknown", "": ""} {
		if got := m.Resolve(in); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
	b, _ := json.Marshal(m)
	var back map[string]map[string]string
	if err := json.Unmarshal(b, &back); err != nil || len(back) != 2 || back["nameToCode"]["SPK890174"] != "" {
		t.Fatalf("two separate objects: %s", b)
	}
}

func pts(prefix, network string, n int) []point {
	out := make([]point, n)
	for i := range out {
		out[i] = point{id: uuid.New(), code: fmt.Sprintf("%s%03d", prefix, i), key: fmt.Sprintf("%s%03d", prefix, i),
			network: network, lat: 13 + float64(i)/1000, lng: 100}
	}
	return out
}

// The job limits of the Hub-SOC Distance section (.vibe-rules.md:1423-1449 as of commit 4f552099): 167 hubs x 3 SOCs
// are 7 requests of 25 origins x 3 destinations per direction; no request is over 25 origins, 25 destinations or 100
// elements.
func TestPlanBatchesLikeTheCallable(t *testing.T) {
	reqs, warn := plan(pts("H", NetworkSPX, 167), pts("S", NetworkSPX, 3))
	if warn != "" {
		t.Fatal(warn)
	}
	per := map[string]int{}
	for _, r := range reqs {
		per[r.direction]++
		if err := gmaps.CheckLimits(len(r.origins), len(r.destinations)); err != nil {
			t.Fatal(err)
		}
	}
	if per[DirectionHubToSOC] != 7 || per[DirectionSOCToHub] != 7 {
		t.Fatalf("requests per direction: %v", per)
	}
	if first := reqs[0]; len(first.origins) != 25 || len(first.destinations) != 3 {
		t.Fatalf("first hub_to_soc request: %d x %d", len(first.origins), len(first.destinations))
	}
	// Every pair is planned exactly once per direction.
	for _, c := range []struct{ hubs, socs int }{{1, 1}, {24, 25}, {26, 25}, {100, 7}, {5, 2}} {
		reqs, _ := plan(pts("H", NetworkSPX, c.hubs), pts("S", NetworkSPX, c.socs))
		cells := map[string]int{}
		for _, r := range reqs {
			if err := gmaps.CheckLimits(len(r.origins), len(r.destinations)); err != nil {
				t.Fatalf("%v: %v", c, err)
			}
			for _, o := range r.origins {
				for _, d := range r.destinations {
					cells[r.direction+o.code+d.code]++
				}
			}
		}
		if len(cells) != 2*c.hubs*c.socs {
			t.Fatalf("%v: %d cells", c, len(cells))
		}
		for k, n := range cells {
			if n != 1 {
				t.Fatalf("%v: %s planned %d times", c, k, n)
			}
		}
	}
	if _, warn := plan(pts("H", NetworkSPX, 3), pts("S", NetworkSPX, 26)); warn != ErrTextManySocs {
		t.Fatalf("26 SOCs: %q", warn)
	}
	if reqs, _ := plan(nil, pts("S", NetworkSPX, 3)); len(reqs) != 0 {
		t.Fatal("a network without hubs makes requests")
	}
}

// getHubsAndSocs: SOC ids starting with 0 are skipped, one SOC per network and key (the first wins), RETURN_CENTER is
// a SOC.
func TestPointsSkipsStandbyAndDuplicateSocs(t *testing.T) {
	row := func(code, station, network string) hubsdb.ListDistancePointsRow {
		return hubsdb.ListDistancePointsRow{ID: uuid.New(), SourceID: code, StationType: station, Network: network, Latitude: 13, Longitude: 100}
	}
	hubs, socs := points([]hubsdb.ListDistancePointsRow{
		row("ALANG-A", "HUB", "SPX"), row("SOCE", "SOC", "SPX"), row("SOCE (บัวโรย)", "SOC", "SPX"),
		row("0STANDBY", "SOC", "SPX"), row("SOCE", "SOC", "SPK"), row("RC1", "RETURN_CENTER", "SPX"),
		row("SPK890174", "HUB", "SPK"),
	})
	if len(hubs) != 2 {
		t.Fatalf("hubs: %+v", hubs)
	}
	var got []string
	for _, s := range socs {
		got = append(got, s.network+":"+s.key+":"+s.code)
	}
	want := []string{"SPX:SOCE:SOCE", "SPK:SOCE:SOCE", "SPX:RC1:RC1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("socs: %v, want %v", got, want)
	}
}

func TestCollectReadsBothDirections(t *testing.T) {
	h, s := pts("H", NetworkSPX, 2), pts("S", NetworkSPX, 1)
	ok := gmaps.Element{OK: true, Status: "OK", DistanceMeters: 1000, DurationSeconds: 60}
	got := collect(request{direction: DirectionHubToSOC, origins: h, destinations: s},
		gmaps.Matrix{Rows: [][]gmaps.Element{{ok}, {{Status: "NOT_FOUND"}}}})
	if len(got) != 1 || got[0].hub.code != h[0].code || got[0].soc.code != s[0].code {
		t.Fatalf("hub_to_soc: %+v", got)
	}
	got = collect(request{direction: DirectionSOCToHub, origins: s, destinations: h}, gmaps.Matrix{Rows: [][]gmaps.Element{{ok, ok}}})
	if len(got) != 2 || got[1].hub.code != h[1].code || got[1].soc.code != s[0].code || got[1].direction != DirectionSOCToHub {
		t.Fatalf("soc_to_hub: %+v", got)
	}
}

// The DTO masks the customer or carrier behind a hub's party where the caller's RLS would not read it.
func TestLinkMaskMirrorsRLS(t *testing.T) {
	own, carrierA, carrierC := uuid.New(), uuid.New(), uuid.New()
	partyX, partyY := uuid.New(), uuid.New()
	cust := func(party uuid.UUID) cachedHub {
		k := kindCustomer
		id, name := uuid.New(), "X — X Co."
		return cachedHub{Hub: Hub{LinkedPartyID: &party, LinkedCustomerID: &id, LinkedCustomerName: &name, LinkedCustomerKind: &k},
			PartyKind: partyCustomer}
	}
	carrier := func(tenant uuid.UUID) cachedHub {
		k, party, name := kindPartner, uuid.New(), "C"
		return cachedHub{Hub: Hub{LinkedPartyID: &party, LinkedCustomerID: &tenant, LinkedCustomerName: &name, LinkedCustomerKind: &k},
			PartyKind: partyTenant, PartyTenantID: &tenant}
	}
	ownCarrierParty := carrier(carrierC) // a customer scope whose own party is a carrier's (tenant kind)
	ownCarrierParty.LinkedPartyID = &partyX
	steward := &authz.Principal{TenantID: &own, TenantRole: authz.Operator, Steward: true}
	carrierStaff := &authz.Principal{TenantID: &carrierA, TenantRole: authz.Manager}
	ownStaffReach := &authz.Principal{TenantID: &own, TenantRole: authz.Manager, SubtenantIDs: []uuid.UUID{carrierA}}
	customerScope := &authz.Principal{PartyIDs: []uuid.UUID{partyX}}
	support := &authz.Principal{Platform: []authz.PlatformRole{authz.Support}}
	for _, c := range []struct {
		name string
		p    *authz.Principal
		h    cachedHub
		show bool
	}{
		{"steward, customer", steward, cust(partyY), true},
		{"steward, carrier", steward, carrier(carrierC), true},
		{"carrier staff, customer", carrierStaff, cust(partyY), true},
		{"carrier staff, own carrier", carrierStaff, carrier(carrierA), true},
		{"carrier staff, other carrier", carrierStaff, carrier(carrierC), false},
		{"contractor reach", ownStaffReach, carrier(carrierA), true},
		{"customer scope, own party", customerScope, cust(partyX), true},
		{"customer scope, other party", customerScope, cust(partyY), false},
		{"customer scope, carrier", customerScope, carrier(carrierC), false}, // 0012 hides another party, of any kind
		{"customer scope, own carrier party", customerScope, ownCarrierParty, true},
		{"support, carrier", support, carrier(carrierC), false},
		{"support, customer", support, cust(partyY), true},
	} {
		got := c.h.forPrincipal(c.p)
		if shown := got.LinkedCustomerName != nil && got.LinkedCustomerID != nil; shown != c.show {
			t.Errorf("%s: shown %v", c.name, shown)
		}
		if got.LinkedPartyID == nil {
			t.Errorf("%s: the hub's own column linkedPartyId was masked", c.name)
		}
		// The kind comes from the billing party row: hidden exactly where RLS hides that row (a customer scope's other
		// parties); a carrier's name hidden from staff keeps the kind, as the RLS read does.
		if kindHidden := got.LinkedCustomerKind == nil; kindHidden != (c.p == customerScope && !c.show) {
			t.Errorf("%s: linkedCustomerKind hidden %v", c.name, kindHidden)
		}
	}
}

func TestInputValidation(t *testing.T) {
	str := func(v string) Field[string] { return Field[string]{Set: true, V: v} }
	in := HubInput{SourceID: str(" alang-a "), SourceNameTh: str("อาลัง")}
	if _, err := in.validate(true); err != nil || in.SourceID.V != "ALANG-A" {
		t.Fatalf("%v %q", err, in.SourceID.V)
	}
	bad := HubInput{SourceID: str(""), SourceNameTh: Field[string]{Set: true, Null: true}, StationType: str("DEPOT"),
		Latitude: Field[float64]{Set: true, V: 91}, LinkedPartyID: str("nope")}
	_, err := bad.validate(true)
	fields := map[string]string{}
	if e, ok := err.(*httpx.Error); ok {
		for _, f := range e.Details["fields"].([]httpx.FieldViolation) {
			fields[f.Field] = f.Reason
		}
	}
	want := map[string]string{"sourceId": "required", "sourceNameTh": "required", "stationType": "invalid",
		"latitude": "out_of_range", "linkedPartyId": "invalid"}
	if fmt.Sprint(fields) != fmt.Sprint(want) {
		t.Fatalf("fields %v, want %v", fields, want)
	}
	// Link forms: a party id wins; a customer id defaults to kind customer; a kind alone names nothing; null unlinks.
	id := uuid.New().String()
	if l, _ := parseLink(str(id), Field[string]{}, Field[string]{}); l.party == nil {
		t.Error("linkedPartyId")
	}
	if l, _ := parseLink(Field[string]{}, str(id), Field[string]{}); l.customer == nil || l.tenant != nil {
		t.Error("linkedCustomerId")
	}
	if l, _ := parseLink(Field[string]{}, str(id), str("partner")); l.tenant == nil {
		t.Error("partner")
	}
	if _, bad := parseLink(Field[string]{}, Field[string]{}, str("partner")); len(bad) != 1 || bad[0].Field != "linkedCustomerId" {
		t.Errorf("kind alone: %v", bad)
	}
	if l, _ := parseLink(Field[string]{Set: true, Null: true}, Field[string]{}, Field[string]{}); !l.set || !l.clear {
		t.Error("null unlinks")
	}
	if _, bad := parseLink(Field[string]{}, str(id), str("vendor")); len(bad) != 1 || bad[0].Field != "linkedCustomerKind" {
		t.Errorf("bad kind: %v", bad)
	}
}

// A blank import cell (a workbook's empty cell) keeps the stored value, as an absent or null one does: import never
// clears (clearing is a PATCH).
func TestImportBlankCellsKeepValues(t *testing.T) {
	blank, spaces, v := "", "  ", "x"
	in := ImportRow{SourceID: "H1", SourceNameTh: &blank, SourceNameEn: &spaces, StationType: &blank,
		LinkedPartyID: &blank, LinkedCustomerID: &spaces, LinkedCustomerKind: &blank}.input()
	if in.SourceNameTh.Set || in.SourceNameEn.Set || in.StationType.Set || in.LinkedPartyID.Set || in.LinkedCustomerID.Set ||
		in.LinkedCustomerKind.Set {
		t.Fatalf("a blank cell is set: %+v", in)
	}
	l, err := in.validate(false)
	if err != nil || l.set {
		t.Fatalf("blank cells: %v %+v", err, l)
	}
	if in := (ImportRow{SourceID: "H1", SourceNameEn: &v}).input(); !in.SourceNameEn.Set || in.SourceNameEn.V != "x" {
		t.Fatalf("a filled cell: %+v", in.SourceNameEn)
	}
}

func TestAliasNamesSkipBlankDuplicateAndOwnCode(t *testing.T) {
	en := "spk890174"
	a, s := aliasNames("SPK890174", " ห้วยขวาง10 ", &en)
	if fmt.Sprint(a) != "[ห้วยขวาง10]" || fmt.Sprint(s) != "[name_th]" {
		t.Fatalf("%v %v", a, s)
	}
	same := "Bang Na"
	if a, _ := aliasNames("BANGNA", "bang na", &same); len(a) != 1 {
		t.Fatalf("case-insensitive duplicate: %v", a)
	}
}

func TestETagMatching(t *testing.T) {
	tag := `"abc"`
	for h, want := range map[string]bool{`"abc"`: true, `W/"abc"`: true, `"x", "abc"`: true, `*`: true, `"abd"`: false, ``: false} {
		if etagMatches(h, tag) != want {
			t.Errorf("If-None-Match %q", h)
		}
	}
}
