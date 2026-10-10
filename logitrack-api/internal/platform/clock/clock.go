// Package clock holds the Bangkok calendar every billing, payroll and period
// rule is written in (main spec §6.3). Thailand is a fixed UTC+07:00 with no
// DST, so a constant offset is exact; the host zone and UTC truncation are
// never used. The package is pure: it never reads the wall clock.
package clock

import (
	"regexp"
	"strconv"
	"time"
)

// Bangkok is the fixed +07:00 zone (ICT). SQL: bkk_date(ts) =
// ((ts AT TIME ZONE 'UTC') + interval '7 hours')::date.
var Bangkok = time.FixedZone("ICT", 7*60*60)

const (
	dayMillis    = 24 * 60 * 60 * 1000
	offsetMillis = 7 * 60 * 60 * 1000
)

// DateString is the yyyy-MM-dd of an instant on the Bangkok calendar
// (bangkokDateStrFromMillis, web:lib/billingCompute.ts:176; bangkokDateStr,
// web:lib/billingDate.ts:21). Not interchangeable with the UTC date: Bangkok
// midnight is 17:00Z on the previous day.
func DateString(t time.Time) string {
	return t.In(Bangkok).Format(time.DateOnly)
}

// Day numbers the Bangkok calendar day of an instant (days since 1970-01-01
// in Bangkok), so two instants compare by day without formatting.
// Day(a) <= Day(b) is DateString(a) <= DateString(b) for every year 0-9999.
func Day(t time.Time) int64 {
	ms := t.UnixMilli() + offsetMillis
	d := ms / dayMillis
	if ms%dayMillis < 0 {
		d--
	}
	return d
}

var dateOnly = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// MidnightFromDateString is Bangkok midnight of a yyyy-MM-dd date
// (bangkokMidnightFromDateStr, web:lib/billingDate.ts:14). It accepts what
// V8 accepts for "<s>T00:00:00+07:00": month 01-12, day 01-31, and a day past
// the end of the month rolls over ("2026-02-30" is 2 March); anything else is
// rejected.
func MidnightFromDateString(s string) (time.Time, bool) {
	if !dateOnly.MatchString(s) {
		return time.Time{}, false
	}
	y, _ := strconv.Atoi(s[0:4])
	m, _ := strconv.Atoi(s[5:7])
	d, _ := strconv.Atoi(s[8:10])
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return time.Time{}, false
	}
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, Bangkok), true
}

// CalendarDayString is the yyyy-MM-dd an instant shows in its own location
// (pickedDateToDateStr, web:lib/billingDate.ts:47). It is the Go form of the
// browser's local getters: a date picker's value carries the zone it was
// picked in.
func CalendarDayString(t time.Time) string {
	return t.Format(time.DateOnly)
}

// MidnightOfCalendarDay is Bangkok midnight of the calendar day an instant
// shows in its own location (bangkokMidnightFromPickedDate,
// web:lib/billingDate.ts:37): the day the picker displayed is kept, whatever
// the time component.
func MidnightOfCalendarDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Bangkok)
}

// YearMonth is the Bangkok year and month (1-12) of an instant
// (bangkokYearMonth, fn:core/billingPeriodLock.ts:43): billing periods and
// their locks are Thai calendar months.
func YearMonth(t time.Time) (year, month int) {
	b := t.In(Bangkok)
	return b.Year(), int(b.Month())
}
