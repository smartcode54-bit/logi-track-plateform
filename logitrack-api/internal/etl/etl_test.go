package etl

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// moduleRoot is the logitrack-api directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func reasonStrings() []string {
	out := make([]string, len(Reasons))
	for i, r := range Reasons {
		out[i] = string(r)
	}
	return out
}

// The catalog equals the "Reason codes" table of Appendix A §A.3.0, in order.
func TestReasonsEqualAppendixA(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(moduleRoot(t), "..", "shared-docs", "specs", "mv-go", "A-data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	start := strings.Index(text, "| Code | Level | Raised when |")
	if start < 0 {
		t.Fatal("Appendix A §A.3.0 reason-code table not found")
	}
	table := text[start:]
	table = table[:strings.Index(table, "\n\n")]
	code := regexp.MustCompile("`([a-z_]+)`")
	var got []string
	for _, line := range strings.Split(table, "\n")[2:] {
		cell := strings.Split(line, "|")[1]
		for _, m := range code.FindAllStringSubmatch(cell, -1) {
			got = append(got, m[1])
		}
	}
	if want := reasonStrings(); !slices.Equal(got, want) {
		t.Fatalf("Appendix A lists\n%v\nthe Go catalog is\n%v", got, want)
	}
}

// The catalog equals the CHECK on etl.quarantine.reason_code (0009_infra).
func TestReasonsEqualTheMigrationCheck(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(moduleRoot(t), "migrations", "0009_infra.sql"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	i := strings.Index(text, "reason_code  text NOT NULL CHECK (reason_code IN (")
	if i < 0 {
		t.Fatal("reason_code CHECK not found in 0009_infra.sql")
	}
	body := text[i:]
	body = body[:strings.Index(body, ")),")]
	var got []string
	for _, m := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(body, -1) {
		got = append(got, m[1])
	}
	want := reasonStrings()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("CHECK lists\n%v\nthe Go catalog is\n%v", got, want)
	}
}

func TestMoneyCastIsJavaScriptHalfUp(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want int64
	}{{1234.5, 123450}, {-0.025, -2}, {0.125, 13}, {1.005, 100}, {-2.5, -250}, {0.285, 28}, {1500.255, 150026}} {
		if got := cents(c.in); got != c.want {
			t.Errorf("cents(%v) = %d, want %d", c.in, got, c.want)
		}
	}
	// Ties toward +Inf on the cents value (Math.round): -0.005 THB is -0.5 satang -> -0 satang.
	if cents(-0.005) != 0 {
		t.Errorf("cents(-0.005) = %d", cents(-0.005))
	}
	ctx := &docCtx{f: map[string]any{"nan": math.NaN(), "neg": -5.0, "s": "12.50", "bad": "abc", "big": 1e13}}
	if _, r := ctx.moneyCents("nan", false); r != moneyBad {
		t.Error("NaN must be bad_number")
	}
	if _, r := ctx.moneyCents("neg", true); r != moneyNegative {
		t.Error("a negative amount where >= 0 is required must be negative_money")
	}
	if n, r := ctx.moneyCents("s", false); r != moneyOK || n != 1250 {
		t.Errorf("numeric string: %d %v", n, r)
	}
	if _, r := ctx.moneyCents("bad", false); r != moneyBad {
		t.Error("a non-numeric string must be bad_number")
	}
	if _, r := ctx.moneyCents("big", false); r != moneyBad {
		t.Error("an amount beyond NUMERIC(14,2) must be bad_number")
	}
	if _, r := ctx.moneyCents("absent", false); r != moneyAbsent {
		t.Error("absent")
	}
}

func TestTimeCasts(t *testing.T) {
	bkk := func(s string) time.Time {
		tt, err := time.Parse("2006-01-02T15:04:05Z07:00", s)
		if err != nil {
			t.Fatal(err)
		}
		return tt.UTC()
	}
	cases := []struct {
		in   any
		want time.Time
	}{
		{dump.Timestamp{Time: bkk("2026-09-01T00:00:00Z")}, bkk("2026-09-01T00:00:00Z")},
		{int64(1756684800000), time.UnixMilli(1756684800000).UTC()},
		{"2026-09-01T02:00:00.000Z", bkk("2026-09-01T02:00:00Z")},
		{"2026-09-01", bkk("2026-09-01T00:00:00+07:00")},
		{"2026-09-01 08:00:00", bkk("2026-09-01T08:00:00+07:00")},
		{"Tue, 01 Sep 2026 02:00:00 GMT", bkk("2026-09-01T02:00:00Z")},
		{map[string]any{"_seconds": int64(1756684800), "_nanoseconds": int64(0)}, time.Unix(1756684800, 0).UTC()},
	}
	for _, c := range cases {
		got, ok := toTime(c.in)
		if !ok || !got.Equal(c.want) {
			t.Errorf("toTime(%#v) = %v %v, want %v", c.in, got, ok, c.want)
		}
	}
	for _, bad := range []any{"yesterday", true, math.NaN(), map[string]any{"x": 1}} {
		if _, ok := toTime(bad); ok {
			t.Errorf("toTime(%#v) must fail", bad)
		}
	}
	c := &docCtx{f: map[string]any{"seal": "01-09-2026 08:30:00", "bad": "31-02-2026 08:30:00", "d": dump.Timestamp{Time: bkk("2026-08-31T18:00:00Z")}}}
	if got := c.bangkokSealTime("seal"); got == nil || !got.Equal(bkk("2026-09-01T08:30:00+07:00")) {
		t.Errorf("sealTime: %v", got)
	}
	if got := c.bangkokSealTime("bad"); got != nil || len(c.findings) != 1 || c.findings[0].Reason != ReasonBadTimestamp {
		t.Errorf("an impossible seal time must be bad_timestamp: %v %v", got, c.findings)
	}
	// 18:00Z on 31 Aug is 01:00 on 1 Sep in Bangkok: the date column takes the Bangkok day (bkk_date).
	if got := c.date("d"); got == nil || got.Format(time.DateOnly) != "2026-09-01" {
		t.Errorf("date: %v", got)
	}
}

func TestParseStorageURL(t *testing.T) {
	b, key, legacy, ok := ParseStorageURL("https://firebasestorage.googleapis.com/v0/b/proj.appspot.com/o/trip_records%2FT1%2Fa%20b.jpg?alt=media&token=secret")
	if !ok || b != "proj.appspot.com" || key != "trip_records/T1/a b.jpg" || strings.Contains(legacy, "secret") || !strings.Contains(legacy, "alt=media") {
		t.Fatalf("got %q %q %q %v", b, key, legacy, ok)
	}
	for _, bad := range []string{"https://lh3.googleusercontent.com/a.jpg", "http://firebasestorage.googleapis.com/v0/b/x/o/y",
		"https://firebasestorage.googleapis.com/v0/b/x/o/", "data:image/png;base64,AAA", "https://firebasestorage.googleapis.com/v0/b/x/o/a%2F%2Fb"} {
		if _, _, _, ok := ParseStorageURL(bad); ok {
			t.Errorf("%s must not parse", bad)
		}
	}
}

func TestVocabulariesAndTripNumbers(t *testing.T) {
	for in, want := range map[string]string{"Checked in": "checked_in", "In-Transit": "in_transit", "On-Duty": "on_duty",
		"insurance-claim": "insurance_claim", "PM Booking": "pm_booking", "FULL_TIME": "full_time", "APPROVED": "approved"} {
		if got := canon(in); got != want {
			t.Errorf("canon(%q) = %q, want %q", in, got, want)
		}
	}
	for _, ok := range []string{"TRIP001", "SPX-TRIP-4", "a.b", "ไทย"} {
		if !validTripNo(ok) {
			t.Errorf("%q is a valid trip number", ok)
		}
	}
	for _, bad := range []string{"", "a/b", ".", "..", "__x__", strings.Repeat("x", 1501)} {
		if validTripNo(bad) {
			t.Errorf("%q must fail the trip_no CHECK", bad)
		}
	}
	for typ, known := range map[string]bool{"seal": true, "stop_2_opening": true, "stop_x_opening": false, "selfie": false, "checkin_app": true} {
		if knownTripPhotoType(typ) != known {
			t.Errorf("knownTripPhotoType(%q) != %v", typ, known)
		}
	}
}

// Every mapped or deferred collection is a collection of Appendix A, and no name is in two lists.
func TestCollectionListsAreDisjoint(t *testing.T) {
	for _, n := range MappedCollections() {
		if laterCollections[n] || droppedCollections[n] {
			t.Errorf("%s is mapped and also listed as later or dropped", n)
		}
	}
	for n := range droppedCollections {
		if laterCollections[n] {
			t.Errorf("%s is dropped and later", n)
		}
	}
}
