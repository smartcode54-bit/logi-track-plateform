package etl

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseClaims(t *testing.T) {
	c, ok := parseClaims(`{"admin":"true","role":" driver ","driverId":"d1","partnerScopeId":"p","customerScopeId":"c","companyId":"x"}`)
	if !ok || !c.admin || c.role != "driver" || c.driverID != "d1" || c.partnerScopeID != "p" || c.customerScopeID != "c" {
		t.Fatalf("claims %+v", c)
	}
	if c, ok := parseClaims(""); !ok || c.admin || c.role != "" {
		t.Fatal("no claims")
	}
	if _, ok := parseClaims("{bad"); ok {
		t.Fatal("bad claims accepted")
	}
}

func TestLegacyHash(t *testing.T) {
	if h, s, ok := legacyHash(ExportUser{PasswordHash: "aGFzaA==", Salt: "c2FsdA"}); !ok || string(h) != "hash" || string(s) != "salt" {
		t.Fatal("standard and raw base64")
	}
	if h, _, ok := legacyHash(ExportUser{}); !ok || h != nil {
		t.Fatal("no hash is fine")
	}
	for _, u := range []ExportUser{{PasswordHash: "aGFzaA=="}, {Salt: "c2FsdA=="}, {PasswordHash: "!!", Salt: "c2FsdA=="}} {
		if _, _, ok := legacyHash(u); ok {
			t.Errorf("%+v accepted", u)
		}
	}
}

func TestReadAuthExport(t *testing.T) {
	e, err := ReadAuthExport(strings.NewReader(`{"users":[{"localId":"u1","createdAt":"1700000000000","lastSignedInAt":1700000001000},
		{"localId":"u2","createdAt":"never"}]}`))
	if err != nil || len(e.Users) != 2 {
		t.Fatal(err)
	}
	if e.Users[0].CreatedAt.t == nil || !e.Users[0].CreatedAt.t.Equal(time.UnixMilli(1700000000000)) ||
		e.Users[0].LastSignedInAt.t == nil || e.Users[1].CreatedAt.t != nil {
		t.Fatalf("instants %+v", e.Users)
	}
	if _, err := ReadAuthExport(strings.NewReader("not json")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestLoginGeo(t *testing.T) {
	g := loginGeo(map[string]any{"lastLoginLocation": map[string]any{"lat": 13.7, "lng": int64(100), "source": "GPS"}})
	if g.lat == nil || *g.lat != 13.7 || g.lng == nil || *g.lng != 100 || g.source == nil || *g.source != "gps" {
		t.Fatalf("legacy nested location %+v", g)
	}
	if g := loginGeo(map[string]any{"lastLoginLat": 91.0, "lastLoginLng": 100.0}); g.lat != nil {
		t.Fatal("an impossible latitude was kept")
	}
	if g := loginGeo(map[string]any{"lastLoginLat": 13.0, "lastLoginLng": 100.0, "lastLoginGeoSource": "wifi"}); g.source != nil || g.lat == nil {
		t.Fatal("an unknown geo source")
	}
}

func TestReportCSV(t *testing.T) {
	r := &AuthImportReport{Lines: []AuthLine{{UID: "u1", UserID: "id", Email: "=cmd@x.test", Outcome: AuthImported,
		Memberships: []string{"own_fleet:user"}, Flags: []string{FlagNoRole, FlagDefaultMemberDomain}}}}
	var b bytes.Buffer
	if err := r.WriteCSV(&b); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "uid,user_id,email,outcome,memberships,scopes,flags,detail\nu1,id,'=cmd@x.test,imported,own_fleet:user,,no_role;default_member_domain,\n" {
		t.Fatalf("csv %q", got)
	}
}

func TestWeakCandidates(t *testing.T) {
	c, err := ReadWeakCandidates(strings.NewReader("# comment\n\nliteral one\r\nUID-1\tpass word\nAdmin@X.test\tp2\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []WeakCandidate{{"*", "literal one"}, {"uid-1", "pass word"}, {"admin@x.test", "p2"}}
	if !slices.Equal(c, want) {
		t.Fatalf("candidates %v", c)
	}
	_, err = ReadWeakCandidates(strings.NewReader("ok\n\tsecret-value\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("error %v (names the line, never the value)", err)
	}
}

func TestMobileForms(t *testing.T) {
	if got := mobileForms("081-234-5678"); !slices.Equal(got, []string{"0812345678", "66812345678"}) {
		t.Fatalf("local %v", got)
	}
	if got := mobileForms("+66 81 234 5678"); !slices.Equal(got, []string{"66812345678", "0812345678"}) {
		t.Fatalf("international %v", got)
	}
	if mobileForms("n/a") != nil {
		t.Fatal("no digits")
	}
}
