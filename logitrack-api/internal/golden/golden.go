// Package golden runs the language-neutral golden vectors in testdata/golden
// against Go code (main spec §6.16). A vector file is exported from the legacy
// TypeScript (testdata/golden/billing/export.mjs) and verified against it on
// the web side; Go tests map each vector's function name onto the Go API with
// a Func and compare results with Equal.
//
// Encoding (testdata/golden/billing/codec.mjs): numbers JSON cannot carry are
// {"$num": "NaN"|"Infinity"|"-Infinity"|"-0"}, dates {"$date": ISO-8601},
// picked local dates {"$local": "2006-01-02T15:04:05"}. Equal compares floats
// bit for bit (NaN equals NaN, -0 differs from +0) and instants with
// time.Time.Equal.
//
// The package is test support only; production code never imports it.
package golden

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// File is one vector file.
type File struct {
	Source       string `json:"source"`
	SourceCommit string `json:"sourceCommit"`
	Note         string `json:"note"`
	Cases        []Case `json:"cases"`
}

// Case is one Vitest `it` block, or a case added for the Go contract
// (Added names the resolution: R15, R16, R19, R20, characterisation, ...).
type Case struct {
	Name   string  `json:"name"`
	Line   int     `json:"line"`
	Added  string  `json:"added,omitempty"`
	Checks []Check `json:"checks"`
}

// Check is one call and its expected result. Legacy holds the TypeScript
// result where the Go contract deliberately differs (Divergence); Go tests
// only compare against Want (and WantReason, when set).
type Check struct {
	Fn         string            `json:"fn"`
	RawArgs    []json.RawMessage `json:"args"`
	RawWant    json.RawMessage   `json:"want"`
	WantReason string            `json:"wantReason,omitempty"`
	Legacy     json.RawMessage   `json:"legacy,omitempty"`
	Divergence string            `json:"divergence,omitempty"`
}

// Ported counts the cases carried over from the Vitest file.
func (f File) Ported() int {
	n := 0
	for _, c := range f.Cases {
		if c.Added == "" {
			n++
		}
	}
	return n
}

// Path is the absolute path of a file under testdata/golden, found by walking
// up from the working directory to the module root.
func Path(tb testing.TB, name string) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "testdata", "golden", filepath.FromSlash(name))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatalf("golden: no go.mod above the working directory")
		}
		dir = parent
	}
}

// Load reads a vector file, e.g. "billing/billingCompute.json".
func Load(tb testing.TB, name string) File {
	tb.Helper()
	raw, err := os.ReadFile(Path(tb, name))
	if err != nil {
		tb.Fatal(err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		tb.Fatalf("golden: %s: %v", name, err)
	}
	return f
}

// Result is what a Func returns: the value compared against want and, when
// the Go API gives one, the reason a price is absent (an unpriced reason or
// an error code).
type Result struct {
	Value  any
	Reason string
}

// Func maps one vector function name onto the Go API.
type Func func(tb testing.TB, args []any) Result

// Run runs every check of f as subtests named after the cases. A function
// name without a Func fails the run, so no vector is silently skipped.
func Run(t *testing.T, f File, fns map[string]Func) {
	t.Helper()
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			for i, k := range c.Checks {
				fn, ok := fns[k.Fn]
				if !ok {
					t.Fatalf("check %d: no Go mapping for %q", i, k.Fn)
				}
				args := make([]any, len(k.RawArgs))
				for j, a := range k.RawArgs {
					args[j] = Decode(t, a)
				}
				want := Decode(t, k.RawWant)
				got := fn(t, args)
				if ok, diff := Equal(got.Value, want); !ok {
					t.Errorf("check %d %s%s: %s", i, k.Fn, compactArgs(k.RawArgs), diff)
				}
				if k.WantReason != "" && got.Reason != k.WantReason {
					t.Errorf("check %d %s%s: reason %q, want %q", i, k.Fn, compactArgs(k.RawArgs), got.Reason, k.WantReason)
				}
			}
		})
	}
}

func compactArgs(args []json.RawMessage) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = string(a)
	}
	s := "(" + strings.Join(parts, ", ") + ")"
	if len(s) > 300 {
		s = s[:300] + "…)"
	}
	return s
}

// Local is a {"$local"} argument: a calendar date and wall time picked in
// whatever zone the caller replays it in.
type Local struct {
	Year                int
	Month               time.Month
	Day, Hour, Min, Sec int
}

// In is the picked wall time in loc.
func (l Local) In(loc *time.Location) time.Time {
	return time.Date(l.Year, l.Month, l.Day, l.Hour, l.Min, l.Sec, 0, loc)
}

// Decode turns one encoded JSON value into Go values: float64, string, bool,
// nil, []any, map[string]any, time.Time ($date) and Local ($local).
func Decode(tb testing.TB, raw json.RawMessage) any {
	tb.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		tb.Fatalf("golden: decode %s: %v", raw, err)
	}
	return decode(tb, v)
}

func decode(tb testing.TB, v any) any {
	switch x := v.(type) {
	case []any:
		for i := range x {
			x[i] = decode(tb, x[i])
		}
		return x
	case map[string]any:
		if s, ok := x["$num"].(string); ok && len(x) == 1 {
			switch s {
			case "NaN":
				return math.NaN()
			case "Infinity":
				return math.Inf(1)
			case "-Infinity":
				return math.Inf(-1)
			case "-0":
				return math.Copysign(0, -1)
			}
			tb.Fatalf("golden: bad $num %q", s)
		}
		if s, ok := x["$date"].(string); ok && len(x) == 1 {
			t, err := ParseISO(s)
			if err != nil {
				tb.Fatalf("golden: bad $date %q: %v", s, err)
			}
			return t
		}
		if s, ok := x["$local"].(string); ok && len(x) == 1 {
			t, err := time.Parse("2006-01-02T15:04:05", s)
			if err != nil {
				tb.Fatalf("golden: bad $local %q: %v", s, err)
			}
			return Local{t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second()}
		}
		for k := range x {
			x[k] = decode(tb, x[k])
		}
		return x
	}
	return v
}

// ParseISO parses Date.prototype.toISOString output, including the expanded
// years JavaScript prints outside 0000-9999 ("-000001-12-31T17:00:00.000Z").
func ParseISO(s string) (time.Time, error) {
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') && len(s) > 7 && s[7] == '-' {
		var y int
		if _, err := fmt.Sscanf(s[:7], "%d", &y); err != nil {
			return time.Time{}, err
		}
		t, err := time.Parse("2006-01-02T15:04:05.000Z", "2000"+s[7:])
		if err != nil {
			return time.Time{}, err
		}
		return t.AddDate(y-2000, 0, 0), nil
	}
	return time.Parse("2006-01-02T15:04:05.000Z", s)
}

// ISO is Date.prototype.toISOString.
func ISO(t time.Time) string {
	u := t.UTC()
	y := u.Year()
	rest := u.Format("-01-02T15:04:05.000Z")
	switch {
	case y >= 0 && y <= 9999:
		return fmt.Sprintf("%04d%s", y, rest)
	case y < 0:
		return fmt.Sprintf("-%06d%s", -y, rest)
	}
	return fmt.Sprintf("+%06d%s", y, rest)
}

// Equal compares a Go result with a decoded want and describes the first
// difference.
func Equal(got, want any) (bool, string) {
	return equal("$", normalize(got), want)
}

// normalize turns the Go values adapters return into the decoded JSON shapes.
func normalize(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case *float64:
		if x == nil {
			return nil
		}
		return *x
	case *string:
		if x == nil {
			return nil
		}
		return *x
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalize(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	}
	return v
}

func equal(path string, got, want any) (bool, string) {
	switch w := want.(type) {
	case nil:
		if got != nil {
			return false, fmt.Sprintf("%s: got %#v, want null", path, got)
		}
		return true, ""
	case float64:
		g, ok := got.(float64)
		if !ok {
			return false, fmt.Sprintf("%s: got %#v (%T), want %v", path, got, got, w)
		}
		if math.IsNaN(g) && math.IsNaN(w) {
			return true, ""
		}
		if math.Float64bits(g) != math.Float64bits(w) {
			return false, fmt.Sprintf("%s: got %v (bits %x), want %v (bits %x)", path, g, math.Float64bits(g), w, math.Float64bits(w))
		}
		return true, ""
	case time.Time:
		g, ok := got.(time.Time)
		if !ok || !g.Equal(w) {
			return false, fmt.Sprintf("%s: got %v, want %s", path, got, ISO(w))
		}
		return true, ""
	case []any:
		g, ok := got.([]any)
		if !ok {
			return false, fmt.Sprintf("%s: got %#v (%T), want an array of %d", path, got, got, len(w))
		}
		if len(g) != len(w) {
			return false, fmt.Sprintf("%s: got %d elements, want %d", path, len(g), len(w))
		}
		for i := range w {
			if ok, d := equal(fmt.Sprintf("%s[%d]", path, i), g[i], w[i]); !ok {
				return false, d
			}
		}
		return true, ""
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("%s: got %#v (%T), want an object", path, got, got)
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			gv, gok := g[k]
			wv, wok := w[k]
			switch {
			case !wok:
				return false, fmt.Sprintf("%s.%s: got %#v, want absent", path, k, gv)
			case !gok:
				return false, fmt.Sprintf("%s.%s: absent, want %#v", path, k, wv)
			}
			if ok, d := equal(path+"."+k, gv, wv); !ok {
				return false, d
			}
		}
		return true, ""
	default:
		if got != want {
			return false, fmt.Sprintf("%s: got %#v, want %#v", path, got, want)
		}
		return true, ""
	}
}

// Arg accessors for adapters. A missing or null value reads as the zero
// value, as a NULL column does.

// Obj is an object argument (nil when null).
func Obj(v any) map[string]any { m, _ := v.(map[string]any); return m }

// Arr is an array argument.
func Arr(v any) []any { a, _ := v.([]any); return a }

// Str is a string argument ("" when null or absent).
func Str(v any) string { s, _ := v.(string); return s }

// Num is a number argument (0 when null or absent).
func Num(v any) float64 { f, _ := v.(float64); return f }

// Int is an integral number argument.
func Int(v any) int { return int(Num(v)) }

// Bool is a boolean argument (false when null or absent).
func Bool(v any) bool { b, _ := v.(bool); return b }

// OptNum is a number field that may be absent (nil when absent or null).
func OptNum(m map[string]any, key string) *float64 {
	f, ok := m[key].(float64)
	if !ok {
		return nil
	}
	return &f
}

// OptStr is a string field that may be absent (nil when absent or null).
func OptStr(m map[string]any, key string) *string {
	s, ok := m[key].(string)
	if !ok {
		return nil
	}
	return &s
}

// Millis is an epoch-millisecond argument as an instant; 0, null, NaN and
// absent are the zero time (the legacy timestampLikeToMillis returned 0 for
// a missing value).
func Millis(v any) time.Time {
	f, ok := v.(float64)
	if !ok || f == 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return time.Time{}
	}
	return time.UnixMilli(int64(f)).UTC()
}

// Date is a {"$date"} argument (zero time when absent).
func Date(v any) time.Time { t, _ := v.(time.Time); return t }

// Opt returns nil for a nil pointer and the pointed value otherwise, for
// building optional result fields.
func Opt[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// Object builds a result object, dropping nil fields (JSON.stringify drops
// undefined).
func Object(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == nil {
			continue
		}
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}
