package locations

import (
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cartrack"
)

func f(v float64) *float64 { return &v }

func TestCoordinates(t *testing.T) {
	for _, c := range []struct {
		lat, lng *float64
		ok       bool
	}{
		{f(13.75), f(100.5), true},
		{f(-90), f(180), true},
		{nil, f(100.5), false},
		{f(13.75), nil, false},
		{f(0), f(0), false}, // a tracker without a fix
		{f(91), f(100), false},
		{f(13), f(-180.5), false},
	} {
		_, _, ok := coordinates(cartrack.Vehicle{Lat: c.lat, Lng: c.lng})
		if ok != c.ok {
			t.Fatalf("%v,%v: %v", c.lat, c.lng, ok)
		}
	}
}

func TestFixTime(t *testing.T) {
	now := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	gps := now.Add(-2 * time.Minute)
	if got := fixTime(&gps, now); !got.Equal(gps) {
		t.Fatal(got)
	}
	if got := fixTime(nil, now); !got.Equal(now) {
		t.Fatal("a missing event time is the sync time (fn:cartrack.ts:175-177)")
	}
	ahead := now.Add(time.Hour)
	if got := fixTime(&ahead, now); !got.Equal(now) {
		t.Fatal("a clock far ahead must not lock later fixes out")
	}
	near := now.Add(time.Minute)
	if got := fixTime(&near, now); !got.Equal(near) {
		t.Fatal("small skew is kept")
	}
}

func TestWithin(t *testing.T) {
	if within(nil, 0, 1) != nil || within(f(-1), 0, 360) != nil || within(f(361), 0, 360) != nil || *within(f(360), 0, 360) != 360 {
		t.Fatal("within")
	}
}

func TestStale(t *testing.T) {
	now := time.Date(2026, 10, 11, 3, 10, 0, 0, time.UTC)
	for s, want := range map[string]bool{
		"":                     false,
		"2026-10-11T03:09:00Z": false,
		"2026-10-11T03:04:00Z": false, // 6 min: at the limit
		"2026-10-11T03:03:00Z": true,
		"garbage":              false,
	} {
		if got := stale(s, now); got != want {
			t.Fatalf("%q: %v", s, got)
		}
	}
}
