package etl

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// Field casting (Appendix A §A.3.0). Every reader takes the legacy field name, returns a value ready for pgx (a
// pointer, nil for SQL NULL) and records a field finding when a present value cannot be cast. An absent field, a
// JSON null and a whitespace-only string are all "no value" and raise nothing.

func rawJSON(v any) json.RawMessage {
	var buf bytes.Buffer
	if err := dump.EncodeValue(&buf, v); err != nil {
		return json.RawMessage(`null`)
	}
	return buf.Bytes()
}

// present reports whether the field holds a value.
func present(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	}
	return true
}

func (c *docCtx) get(key string) any { return c.f[key] }

// has reports whether the field holds a value.
func (c *docCtx) has(key string) bool { return present(c.f[key]) }

// text is a string field verbatim; "" and whitespace-only are NULL. A non-string scalar is written in its JSON
// form (a numeric plate or code is still the legacy text).
func (c *docCtx) text(keys ...string) *string {
	for _, k := range keys {
		switch x := c.f[k].(type) {
		case string:
			if strings.TrimSpace(x) != "" {
				return &x
			}
		case int64:
			s := strconv.FormatInt(x, 10)
			return &s
		case float64:
			if !math.IsNaN(x) && !math.IsInf(x, 0) {
				s := strconv.FormatFloat(x, 'f', -1, 64)
				return &s
			}
		case bool:
			s := strconv.FormatBool(x)
			return &s
		}
	}
	return nil
}

// str is the trimmed string value of a field ("" when absent or not a string).
func (c *docCtx) str(key string) string {
	s, _ := c.f[key].(string)
	return strings.TrimSpace(s)
}

// boolean is a bool field; anything else is NULL.
func (c *docCtx) boolean(key string) *bool {
	if b, ok := c.f[key].(bool); ok {
		return &b
	}
	return nil
}

// isTrue reports a field that is the boolean true.
func (c *docCtx) isTrue(key string) bool {
	b, ok := c.f[key].(bool)
	return ok && b
}

// timeLayouts are the string forms legacy writers used; a layout without a zone is Bangkok wall time. RFC 1123
// is the Firebase Auth metadata form (GMT).
var timeLayouts = []struct {
	layout string
	zoned  bool
}{
	{time.RFC3339Nano, true}, {"2006-01-02T15:04:05.999999999", false}, {"2006-01-02 15:04:05", false},
	{time.RFC1123, true}, {time.RFC1123Z, true},
}

var bangkokLocal = regexp.MustCompile(`^(\d{2})-(\d{2})-(\d{4}) (\d{2}):(\d{2}):(\d{2})$`)

// toTime casts a legacy time value: a Firestore Timestamp, an epoch-ms number, an ISO string (with or without a
// zone; without one it is Bangkok wall time), a yyyy-MM-dd string (Bangkok midnight), an RFC 1123 string, or a
// serialised timestamp map {seconds, nanoseconds} / {_seconds, _nanoseconds}.
func toTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case dump.Timestamp:
		return x.UTC(), true
	case int64:
		return time.UnixMilli(x).UTC(), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 8.64e15 {
			return time.Time{}, false
		}
		return time.UnixMilli(int64(x)).UTC(), true
	case string:
		s := strings.TrimSpace(x)
		if t, ok := clock.MidnightFromDateString(s); ok {
			return t.UTC(), true
		}
		for _, l := range timeLayouts {
			if t, err := time.Parse(l.layout, s); err == nil {
				if !l.zoned {
					t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), clock.Bangkok)
				}
				return t.UTC(), true
			}
		}
	case map[string]any:
		secs, ok1 := x["seconds"]
		nanos := x["nanoseconds"]
		if !ok1 {
			secs, ok1 = x["_seconds"]
			nanos = x["_nanoseconds"]
		}
		s, ok2 := secs.(int64)
		if !ok1 || !ok2 {
			return time.Time{}, false
		}
		n, _ := nanos.(int64)
		return time.Unix(s, n).UTC(), true
	}
	return time.Time{}, false
}

// ts is a timestamptz field; a value that is not a time is NULL + bad_timestamp.
func (c *docCtx) ts(key string) *time.Time {
	v := c.f[key]
	if !present(v) {
		return nil
	}
	t, ok := toTime(v)
	if !ok {
		c.find(key, ReasonBadTimestamp, "not a timestamp", v)
		return nil
	}
	t = t.Truncate(time.Microsecond)
	return &t
}

// bangkokSealTime parses 'dd-MM-yyyy HH:mm:ss' as Asia/Bangkok (sealTime only).
func (c *docCtx) bangkokSealTime(key string) *time.Time {
	v := c.f[key]
	if !present(v) {
		return nil
	}
	if s, ok := v.(string); ok {
		if m := bangkokLocal.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
			n := func(i int) int { x, _ := strconv.Atoi(m[i]); return x }
			t := time.Date(n(3), time.Month(n(2)), n(1), n(4), n(5), n(6), 0, clock.Bangkok)
			if t.Day() == n(1) && int(t.Month()) == n(2) {
				t = t.UTC()
				return &t
			}
		}
	}
	return c.ts(key)
}

// date is a date column: a yyyy-MM-dd string is that day; any other time value is its Bangkok day (bkk_date).
func (c *docCtx) date(key string) *time.Time {
	v := c.f[key]
	if !present(v) {
		return nil
	}
	if s, ok := v.(string); ok {
		if t, ok := clock.MidnightFromDateString(strings.TrimSpace(s)); ok {
			d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			return &d
		}
	}
	t, ok := toTime(v)
	if !ok {
		c.find(key, ReasonBadTimestamp, "not a date", v)
		return nil
	}
	b := t.In(clock.Bangkok)
	d := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return &d
}

// toFloat accepts JSON numbers and numeric strings (legacy distance, totalWeight are strings).
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), ",", ""), 64)
		return f, err == nil
	}
	return 0, false
}

// float is a double precision / numeric column; non-numeric, NaN and infinities are NULL + bad_number.
func (c *docCtx) float(key string) *float64 {
	v := c.f[key]
	if !present(v) {
		return nil
	}
	f, ok := toFloat(v)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		c.find(key, ReasonBadNumber, "not a finite number", v)
		return nil
	}
	return &f
}

// integer is an int column (a fractional value is truncated toward zero, as Dart's toInt and JS parseInt do).
func (c *docCtx) integer(key string) *int64 {
	f := c.float(key)
	if f == nil {
		return nil
	}
	if math.Abs(*f) > 2147483647 {
		c.find(key, ReasonBadNumber, "out of the int range", c.f[key])
		return nil
	}
	n := int64(*f)
	return &n
}

// cents is the money cast of R20: floor(x*100 + 0.5) in float64 (JavaScript Math.round, ties toward +Inf).
func cents(f float64) int64 { return int64(jsmath.Round(float64(f * 100))) }

// numeric2 is a NUMERIC(14,2) value of cents.
func numeric2(c int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(c), Exp: -2, Valid: true}
}

// moneyResult says why a money field is NULL.
type moneyResult int

const (
	moneyOK moneyResult = iota
	moneyAbsent
	moneyBad
	moneyNegative
)

// moneyCents casts a money field without recording anything.
func (c *docCtx) moneyCents(key string, nonNegative bool) (int64, moneyResult) {
	v := c.f[key]
	if !present(v) {
		return 0, moneyAbsent
	}
	f, ok := toFloat(v)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) >= 1e12 {
		return 0, moneyBad
	}
	n := cents(f)
	if nonNegative && n < 0 {
		return n, moneyNegative
	}
	return n, moneyOK
}

// money is a nullable NUMERIC(14,2) column: bad -> NULL + bad_number, negative where the column requires >= 0 ->
// NULL + negative_money.
func (c *docCtx) money(key string, nonNegative bool) *pgtype.Numeric {
	n, r := c.moneyCents(key, nonNegative)
	switch r {
	case moneyOK:
		v := numeric2(n)
		return &v
	case moneyBad:
		c.find(key, ReasonBadNumber, "not a finite amount", c.f[key])
	case moneyNegative:
		c.find(key, ReasonNegativeMoney, "negative amount where the column requires >= 0", c.f[key])
	}
	return nil
}

// geo reads a {lat,lng} pair from a GeoPoint, a map, or a 'lat,lng' string.
func geo(v any) (lat, lng float64, ok bool) {
	switch x := v.(type) {
	case dump.GeoPoint:
		return x.Lat, x.Lng, true
	case map[string]any:
		la, ok1 := toFloat(x["lat"])
		lo, ok2 := toFloat(x["lng"])
		if !ok1 || !ok2 {
			la, ok1 = toFloat(x["latitude"])
			lo, ok2 = toFloat(x["longitude"])
		}
		return la, lo, ok1 && ok2
	case string:
		a, b, found := strings.Cut(x, ",")
		la, ok1 := toFloat(a)
		lo, ok2 := toFloat(b)
		return la, lo, found && ok1 && ok2
	}
	return 0, 0, false
}

// jsonValue turns a legacy value into a jsonb argument (plain JSON, Firestore tags kept for typed values).
func jsonValue(v any) any {
	if !present(v) {
		return nil
	}
	return rawJSON(v)
}
