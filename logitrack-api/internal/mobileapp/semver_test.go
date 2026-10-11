package mobileapp_test

import (
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp"
)

// The cases of logitrack-web/lib/mobileVersion.test.ts, one for one (issue #72: "Blocked count by semver (ported
// lib/mobileVersion.test.ts)"). JavaScript's null and undefined and non-string inputs have no Go counterpart: every
// input is a string here, and the empty string stands for a missing value.

func TestParseVersion(t *testing.T) {
	ok := map[string]mobileapp.Version{
		"2.9.3":                {2, 9, 3}, // a plain MAJOR.MINOR.PATCH
		"2.9.3+1":              {2, 9, 3}, // the pubspec +build suffix is dropped
		"2.9.3+42":             {2, 9, 3},
		"  2.9.3  ":            {2, 9, 3}, // surrounding white space
		"02.9.3":               {2, 9, 3}, // parseInt accepts leading zeros, and so does pub_semver
		"2.9.3+":               {2, 9, 3},
		"9007199254740991.0.0": {9007199254740991, 0, 0},
	}
	for in, want := range ok {
		if got, parsed := mobileapp.ParseVersion(in); !parsed || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", in, got, parsed, want)
		}
	}
	// Anything that is not three numeric parts.
	for _, in := range []string{"v2.9.3", "2.9", "2.9.3.1", "2.9.x", "", "   ", "—", "2.9.3-beta", "-1.0.0", "1..0",
		"9007199254740992.0.0", "99999999999999999999.0.0", "２.9.3"} {
		if v, parsed := mobileapp.ParseVersion(in); parsed {
			t.Errorf("ParseVersion(%q) = %v, want no version", in, v)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cmp := func(a, b string) int {
		t.Helper()
		c, ok := mobileapp.CompareVersions(a, b)
		if !ok {
			t.Fatalf("CompareVersions(%q, %q) did not parse", a, b)
		}
		return c
	}
	// 2.10.0 above 2.9.3: the string-compare trap this module exists for.
	if cmp("2.10.0", "2.9.3") <= 0 || cmp("2.9.3", "2.10.0") >= 0 {
		t.Error("2.10.0 must order above 2.9.3")
	}
	if "2.10.0" >= "2.9.3" {
		t.Error("the lexicographic trap is gone: the guard above proves nothing")
	}
	// Equality, the build suffix ignored.
	if cmp("2.9.3", "2.9.3") != 0 || cmp("2.9.3+1", "2.9.3+7") != 0 {
		t.Error("equal versions")
	}
	// Major, then minor, then patch.
	if cmp("3.0.0", "2.99.99") <= 0 || cmp("2.10.0", "2.9.99") <= 0 || cmp("2.9.4", "2.9.3") <= 0 {
		t.Error("major, minor, patch order")
	}
	// Unknown, not 0, when either side does not parse.
	for _, p := range [][2]string{{"2.9.3", ""}, {"", "2.9.3"}, {"", ""}} {
		if c, ok := mobileapp.CompareVersions(p[0], p[1]); ok {
			t.Errorf("CompareVersions(%q, %q) = %d, want unknown", p[0], p[1], c)
		}
	}
}

func TestStatusOf(t *testing.T) {
	cases := []struct {
		name, installed, latest, floor string
		want                           mobileapp.VersionStatus
	}{
		{"below the floor is blocked, though also outdated", "2.8.0", "2.9.3", "2.9.0", mobileapp.StatusBlocked},
		{"behind the latest, above the floor", "2.9.1", "2.9.3", "2.9.0", mobileapp.StatusOutdated},
		{"on the latest", "2.9.3", "2.9.3", "2.9.0", mobileapp.StatusCurrent},
		{"on the latest with a build suffix", "2.9.3+1", "2.9.3", "2.9.0", mobileapp.StatusCurrent},
		{"newer than published", "2.10.0", "2.9.3", "2.9.0", mobileapp.StatusAhead},
		{"exactly on the floor is not blocked", "2.9.0", "2.9.3", "2.9.0", mobileapp.StatusOutdated},
		{"empty installed version", "", "2.9.3", "2.9.0", mobileapp.StatusUnknown},
		{"unparseable installed version", "v2.9.3", "2.9.3", "2.9.0", mobileapp.StatusUnknown},
		{"latest missing degrades to unknown", "2.9.3", "", "2.9.0", mobileapp.StatusUnknown},
		{"latest and floor missing", "2.9.3", "", "", mobileapp.StatusUnknown},
		{"latest missing still blocks below the floor", "2.8.0", "", "2.9.0", mobileapp.StatusBlocked},
		{"no floor skips the floor check", "2.8.0", "2.9.3", "", mobileapp.StatusOutdated},
	}
	for _, c := range cases {
		if got := mobileapp.StatusOf(c.installed, c.latest, c.floor); got != c.want {
			t.Errorf("%s: StatusOf(%q, %q, %q) = %s, want %s", c.name, c.installed, c.latest, c.floor, got, c.want)
		}
	}
}

func TestCountStatuses(t *testing.T) {
	got := mobileapp.CountStatuses([]string{"2.9.3", "2.9.1", "2.8.0", "2.10.0", "not-a-version"}, "2.9.3", "2.9.0")
	if want := (mobileapp.StatusCounts{Current: 1, Outdated: 1, Blocked: 1, Ahead: 1, Unknown: 1}); got != want {
		t.Errorf("every bucket: %+v, want %+v", got, want)
	}
	if got := mobileapp.CountStatuses(nil, "2.9.3", "2.9.0"); got != (mobileapp.StatusCounts{}) {
		t.Errorf("an empty fleet: %+v", got)
	}
}

func TestCountBlockedByFloor(t *testing.T) {
	cases := []struct {
		name      string
		installed []string
		target    string
		want      int
	}{
		{"strictly below the target", []string{"2.8.0", "2.9.2", "2.9.3", "2.10.0"}, "2.9.3", 2},
		{"already on the target", []string{"2.9.3", "2.9.3+1"}, "2.9.3", 0},
		{"unparseable versions never count: the dialog must not overstate the impact", []string{"", "—", "v2.8.0"}, "2.9.3", 0},
		{"an unparseable target counts nothing", []string{"2.8.0", "2.9.0"}, "", 0},
	}
	for _, c := range cases {
		if got := mobileapp.CountBlockedByFloor(c.installed, c.target); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

func TestValidReleaseVersion(t *testing.T) {
	for _, v := range []string{"0.0.0", "3.5.0", "10.20.30", "999999999.0.1"} {
		if !mobileapp.ValidReleaseVersion(v) {
			t.Errorf("%q refused", v)
		}
	}
	for _, v := range []string{"", "3.5", "03.5.0", "3.05.0", "3.5.0+1", "v3.5.0", "3.5.0-beta", " 3.5.0", "1000000000.0.0"} {
		if mobileapp.ValidReleaseVersion(v) {
			t.Errorf("%q accepted", v)
		}
	}
}
