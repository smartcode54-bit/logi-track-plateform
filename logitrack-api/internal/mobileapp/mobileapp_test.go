package mobileapp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// ADR 0007 two-writer rule: the release writer's half never names a floor field, whatever the input.
func TestReleaseHalfNeverWritesTheFloor(t *testing.T) {
	notes := "n"
	half := releaseHalf(ReleaseInput{Flavor: "prod", Version: "3.6.0", BuildNumber: "1", SHA256: strings.Repeat("a", 64),
		ReleaseNotes: &notes}, "https://x/a.apk", 10, time.Now(), "apikey:r")
	for k := range half {
		if strings.HasPrefix(k, "minAllowedVersion") {
			t.Fatalf("release half writes %s", k)
		}
	}
	for _, k := range floorKeys {
		if _, ok := half[k]; ok {
			t.Fatalf("release half writes %s", k)
		}
	}
	// The publish body has no floor field: a minAllowedVersion sent along is ignored (unknown fields are).
	var in ReleaseInput
	if err := json.Unmarshal([]byte(`{"flavor":"prod","minAllowedVersion":"9.9.9"}`), &in); err != nil || in.Flavor != "prod" {
		t.Fatal(err)
	}
	b, _ := json.Marshal(in)
	if strings.Contains(string(b), "minAllowedVersion") {
		t.Fatalf("ReleaseInput JSON %s", b)
	}
}

func TestDecodeSettingsIsTolerant(t *testing.T) {
	st := decodeSettings([]byte(`{"minAllowedVersion": 3, "latestVersion": "3.5.0", "apkSizeBytes": "big", "releasedAt": "yesterday",
		"minAllowedVersionSetAt": "2026-09-15T10:00:00+07:00", "flavor": "prod", "extra": {"a": 1}}`))
	if st.MinAllowedVersion != "" || st.LatestVersion != "3.5.0" || st.APKSizeBytes != 0 || st.ReleasedAt != nil ||
		st.MinAllowedVersionSetAt == nil || !st.MinAllowedVersionSetAt.Equal(time.Date(2026, 9, 15, 3, 0, 0, 0, time.UTC)) || st.Flavor != "prod" {
		t.Fatalf("%+v", st)
	}
	if decodeSettings([]byte(`not json`)) != (Settings{}) || decodeSettings(nil) != (Settings{}) {
		t.Fatal("garbage is the zero document")
	}
}

func TestPublicView(t *testing.T) {
	st := Settings{Flavor: "prod", MinAllowedVersion: "3.4.0", MinAllowedVersionSetBy: "a@b", LatestVersion: "3.5.0",
		APKDownloadURL: "https://x/a.apk", ReleasedBy: "apikey:r"}
	p := st.Public("")
	if *p.Flavor != "prod" || *p.MinAllowedVersion != "3.4.0" || *p.APKDownloadURL != "https://x/a.apk" || p.ReleaseNotes != nil {
		t.Fatalf("%+v", p)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "a@b") || strings.Contains(string(b), "apikey") {
		t.Fatalf("public view %s", b)
	}
	// Another flavor: no floor without its own link (ADR 0007 §4).
	if d := st.Public("dev"); *d.Flavor != "dev" || d.MinAllowedVersion != nil || d.APKDownloadURL != nil || d.LatestVersion != nil {
		t.Fatalf("%+v", d)
	}
	// A deployment that never published shows its floor (if any) to every flavor.
	if p := (Settings{}).Public("dev"); *p.Flavor != "dev" || p.MinAllowedVersion != nil {
		t.Fatalf("%+v", p)
	}
	if v := st.View(false); v.MinAllowedVersionSetBy != nil || v.ReleasedBy != nil {
		t.Fatal("names without security:manage_mobile_release")
	}
	if v := st.View(true); *v.MinAllowedVersionSetBy != "a@b" || *v.ReleasedBy != "apikey:r" {
		t.Fatal("names with security:manage_mobile_release")
	}
}

func TestBecomesLatestAndGate(t *testing.T) {
	in := func(f, v string, sw bool) ReleaseInput { return ReleaseInput{Flavor: f, Version: v, SwitchFlavor: sw} }
	cases := []struct {
		cur  Settings
		in   ReleaseInput
		want bool
	}{
		{Settings{}, in("dev", "3.6.0", false), true}, // the first publish of a deployment
		{Settings{Flavor: "prod", LatestVersion: "3.5.0"}, in("prod", "3.6.0", false), true},
		{Settings{Flavor: "prod", LatestVersion: "3.10.0"}, in("prod", "3.9.0", false), false}, // numeric, not lexical
		{Settings{Flavor: "prod", LatestVersion: "3.6.0"}, in("prod", "3.6.0", false), true},   // a document without its row
		{Settings{Flavor: "prod", LatestVersion: "3.5.0"}, in("dev", "3.6.0", false), false},
		{Settings{Flavor: "prod", LatestVersion: "3.9.0"}, in("dev", "3.6.0", true), true}, // a switch takes over
		{Settings{Flavor: "prod", LatestVersion: "garbage"}, in("prod", "1.0.0", false), true},
	}
	for i, c := range cases {
		if got := becomesLatest(c.cur, c.in); got != c.want {
			t.Errorf("case %d: %t", i, got)
		}
	}
}

// gateRefusal: a publish never changes what the floor does (ADR 0007 §1), whatever the principal.
func TestGateRefusal(t *testing.T) {
	in := func(f, v string, sw bool) ReleaseInput { return ReleaseInput{Flavor: f, Version: v, SwitchFlavor: sw} }
	refused := func(cur Settings, r ReleaseInput, reason string) {
		t.Helper()
		err := gateRefusal(cur, r)
		e, ok := err.(*httpx.Error)
		if !ok || e.Status != http.StatusConflict || e.Code != CodeFailedPrecondition || e.Details["reason"] != reason {
			t.Fatalf("%+v %+v: %v, want %s", cur, r, err, reason)
		}
	}
	err := gateRefusal(Settings{Flavor: "prod"}, in("dev", "3.6.0", false))
	if e, ok := err.(*httpx.Error); !ok || e.Details["reason"] != "flavor_mismatch" || e.Details["flavor"] != "prod" {
		t.Fatalf("%v", err)
	}
	// A switch while a floor is set would carry it onto the other fleet, above or below the new version alike.
	dev39 := Settings{Flavor: "dev", LatestVersion: "3.9.0", MinAllowedVersion: "3.9.0"}
	refused(dev39, in("prod", "3.6.0", true), "floor_set")
	refused(Settings{Flavor: "prod", LatestVersion: "3.5.0", MinAllowedVersion: "3.4.0"}, in("dev", "3.6.0", true), "floor_set")
	refused(Settings{Flavor: "prod", MinAllowedVersion: "garbage"}, in("dev", "3.6.0", true), "floor_set")
	if e := gateRefusal(dev39, in("prod", "3.6.0", true)).(*httpx.Error); e.Details["minAllowedVersion"] != "3.9.0" || e.Details["flavor"] != "dev" {
		t.Fatalf("details %v", e.Details)
	}
	// A new latest below the floor: a legacy document (floor above latest, or a latest that does not parse) and the
	// first publish of a deployment whose document has a floor.
	refused(Settings{Flavor: "prod", LatestVersion: "3.5.0", MinAllowedVersion: "3.7.0"}, in("prod", "3.6.0", false), "floor_above_version")
	refused(Settings{Flavor: "prod", LatestVersion: "garbage", MinAllowedVersion: "3.7.0"}, in("prod", "3.6.0", false), "floor_above_version")
	refused(Settings{MinAllowedVersion: "3.7.0"}, in("dev", "3.6.0", false), "floor_above_version")
	for i, c := range []struct {
		cur Settings
		in  ReleaseInput
	}{
		{Settings{}, in("dev", "3.6.0", false)},                                      // a fresh deployment
		{Settings{Flavor: "prod", LatestVersion: "3.9.0"}, in("dev", "3.6.0", true)}, // a switch without a floor
		{Settings{Flavor: "prod", LatestVersion: "3.5.0", MinAllowedVersion: "3.5.0"}, in("prod", "3.5.0", false)},
		{Settings{Flavor: "prod", LatestVersion: "3.5.0", MinAllowedVersion: "3.4.0"}, in("prod", "3.6.0", false)},
		// An older hotfix below the floor is recorded only (not the latest), so it locks nobody out.
		{Settings{Flavor: "prod", LatestVersion: "3.9.0", MinAllowedVersion: "3.9.0"}, in("prod", "3.6.0", false)},
		{Settings{Flavor: "prod", LatestVersion: "3.5.0", MinAllowedVersion: "garbage"}, in("prod", "3.6.0", false)},
	} {
		if err := gateRefusal(c.cur, c.in); err != nil {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func TestReleaseInputValidation(t *testing.T) {
	good := ReleaseInput{Flavor: "prod", Version: "3.6.0", BuildNumber: "12", SHA256: strings.Repeat("0", 64), SizeBytes: 1}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("ก", MaxReleaseNotes+1)
	nul := "a\x00b"
	for name, mut := range map[string]func(*ReleaseInput){
		"flavor":      func(r *ReleaseInput) { r.Flavor = "staging" },
		"version":     func(r *ReleaseInput) { r.Version = "3.6.0+12" },
		"build":       func(r *ReleaseInput) { r.BuildNumber = "1.2" },
		"sha":         func(r *ReleaseInput) { r.SHA256 = strings.Repeat("A", 64) },
		"size":        func(r *ReleaseInput) { r.SizeBytes = 200<<20 + 1 },
		"notes":       func(r *ReleaseInput) { r.ReleaseNotes = &long },
		"notes nul":   func(r *ReleaseInput) { r.ReleaseNotes = &nul },
		"size absent": func(r *ReleaseInput) { r.SizeBytes = 0 },
	} {
		in := good
		mut(&in)
		if in.validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if APKKey("prod", "3.6.0") != "app_releases/prod/logitrack-prod-v3.6.0.apk" {
		t.Fatal(APKKey("prod", "3.6.0"))
	}
}

func TestDriverOf(t *testing.T) {
	d, tid := uuid.New(), uuid.New()
	if _, _, err := driverOf(&authz.Principal{TenantRole: authz.Driver, DriverID: &d, TenantID: &tid}); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]*authz.Principal{
		"staff":     {TenantRole: authz.Operator, TenantID: &tid},
		"no driver": {TenantRole: authz.Driver, TenantID: &tid},
		"api key":   {AMR: authz.AMRAPIKey, TenantRole: authz.Driver, DriverID: &d, TenantID: &tid},
	} {
		if _, _, err := driverOf(p); err == nil {
			t.Errorf("%s passed", name)
		}
	}
}

func TestSinceAndETag(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{"7d": now.Add(-7 * 24 * time.Hour), "24h": now.Add(-24 * time.Hour),
		"2026-10-01T00:00:00Z": time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)} {
		if got, ok := parseSince(in, now); !ok || !got.Equal(want) {
			t.Errorf("%s: %v %t", in, got, ok)
		}
	}
	for _, in := range []string{"0d", "7w", "-1d", "yesterday", "10000d"} {
		if _, ok := parseSince(in, now); ok {
			t.Errorf("%s accepted", in)
		}
	}
	if !etagMatches(`W/"abc", "def"`, `"abc"`) || !etagMatches("*", `"x"`) || etagMatches(`"abd"`, `"abc"`) || etagMatches("", `"abc"`) {
		t.Fatal("If-None-Match")
	}
}
