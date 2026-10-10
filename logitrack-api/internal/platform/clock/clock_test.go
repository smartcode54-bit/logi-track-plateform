package clock_test

import (
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/golden"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// pickerZones replay a {"$local"} date: the picker's own zone, whatever it is,
// must not move the calendar day.
var pickerZones = []*time.Location{
	time.UTC,
	clock.Bangkok,
	time.FixedZone("UTC-10", -10*3600),
	time.FixedZone("UTC+14", 14*3600),
	time.FixedZone("UTC-03:30", -(3*3600 + 1800)),
}

// TestGolden runs testdata/golden/billing/billingDate.json: the 8 cases of
// logitrack-web/lib/billingDate.test.ts (the picker cases replay the picked
// wall time in several zones) plus the V8 parsing parity case.
func TestGolden(t *testing.T) {
	f := golden.Load(t, "billing/billingDate.json")
	if got := f.Ported(); got != 8 {
		t.Fatalf("%d ported cases, want 8", got)
	}
	midnight := func(s string) any {
		m, ok := clock.MidnightFromDateString(s)
		if !ok {
			return nil
		}
		return m
	}
	// inEveryZone runs fn on the picked wall time in each zone and fails when
	// the zones disagree.
	inEveryZone := func(tb testing.TB, v any, fn func(time.Time) any) golden.Result {
		tb.Helper()
		l, ok := v.(golden.Local)
		if !ok {
			tb.Fatalf("want a $local argument, got %#v", v)
		}
		first := fn(l.In(pickerZones[0]))
		for _, z := range pickerZones[1:] {
			if ok, diff := golden.Equal(fn(l.In(z)), first); !ok {
				tb.Fatalf("zone %s disagrees with %s: %s", z, pickerZones[0], diff)
			}
		}
		return golden.Result{Value: first}
	}
	golden.Run(t, f, map[string]golden.Func{
		"bangkokMidnightFromDateStr": func(_ testing.TB, a []any) golden.Result {
			return golden.Result{Value: midnight(golden.Str(a[0]))}
		},
		"bangkokDateStr": func(_ testing.TB, a []any) golden.Result {
			return golden.Result{Value: clock.DateString(golden.Date(a[0]))}
		},
		"bangkokDateStr(bangkokMidnightFromDateStr)": func(tb testing.TB, a []any) golden.Result {
			m, ok := clock.MidnightFromDateString(golden.Str(a[0]))
			if !ok {
				tb.Fatalf("unparsable %q", a[0])
			}
			return golden.Result{Value: clock.DateString(m)}
		},
		"msBeforeUtcMidnight": func(tb testing.TB, a []any) golden.Result {
			s := golden.Str(a[0])
			m, ok := clock.MidnightFromDateString(s)
			if !ok {
				tb.Fatalf("unparsable %q", s)
			}
			utc, err := time.Parse(time.DateOnly, s)
			if err != nil {
				tb.Fatal(err)
			}
			return golden.Result{Value: float64(utc.Sub(m).Milliseconds())}
		},
		"pickedDateToDateStr": func(tb testing.TB, a []any) golden.Result {
			return inEveryZone(tb, a[0], func(t time.Time) any { return clock.CalendarDayString(t) })
		},
		"bangkokMidnightFromPickedDate": func(tb testing.TB, a []any) golden.Result {
			return inEveryZone(tb, a[0], func(t time.Time) any { return clock.MidnightOfCalendarDay(t) })
		},
		"bangkokDateStr(bangkokMidnightFromPickedDate)": func(tb testing.TB, a []any) golden.Result {
			return inEveryZone(tb, a[0], func(t time.Time) any { return clock.DateString(clock.MidnightOfCalendarDay(t)) })
		},
	})
}

func TestDayMatchesDateString(t *testing.T) {
	// Day must order instants exactly as their Bangkok date strings do,
	// including before the epoch and across the 17:00Z day boundary.
	base := time.Date(1969, 12, 30, 0, 0, 0, 0, time.UTC)
	prev := base.Add(-time.Minute)
	for i := 0; i < 5000; i++ {
		cur := base.Add(time.Duration(i) * 37 * time.Minute)
		sameDay := clock.DateString(prev) == clock.DateString(cur)
		if (clock.Day(prev) == clock.Day(cur)) != sameDay {
			t.Fatalf("Day disagrees with DateString at %s / %s", prev, cur)
		}
		if clock.Day(prev) > clock.Day(cur) {
			t.Fatalf("Day not monotonic at %s", cur)
		}
		prev = cur
	}
	if got := clock.Day(time.Date(1970, 1, 1, 17, 0, 0, 0, time.UTC)); got != 1 {
		t.Fatalf("Day(1970-01-02 00:00 ICT) = %d, want 1", got)
	}
	if got := clock.Day(time.Date(1970, 1, 1, 16, 59, 59, 999e6, time.UTC)); got != 0 {
		t.Fatalf("Day(1970-01-01 23:59:59.999 ICT) = %d, want 0", got)
	}
	if got := clock.Day(time.Date(1969, 12, 31, 16, 59, 59, 0, time.UTC)); got != -1 {
		t.Fatalf("Day(1969-12-31 23:59:59 ICT) = %d, want -1", got)
	}
}

func TestYearMonth(t *testing.T) {
	for _, tc := range []struct {
		at          time.Time
		year, month int
	}{
		{time.Date(2026, 7, 31, 17, 0, 0, 0, time.UTC), 2026, 8},
		{time.Date(2026, 7, 31, 16, 59, 0, 0, time.UTC), 2026, 7},
		{time.Date(2026, 12, 31, 17, 0, 0, 0, time.UTC), 2027, 1},
		{time.Date(2026, 8, 1, 0, 30, 0, 0, clock.Bangkok), 2026, 8},
	} {
		if y, m := clock.YearMonth(tc.at); y != tc.year || m != tc.month {
			t.Errorf("YearMonth(%s) = %d-%d, want %d-%d", tc.at, y, m, tc.year, tc.month)
		}
	}
}
