package dump_test

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

func TestFieldsRoundTripKeepFirestoreTypes(t *testing.T) {
	ts := time.Date(2026, 5, 1, 3, 4, 5, 123456789, time.UTC)
	in := map[string]any{
		"int": int64(3), "double": 3.0, "frac": 0.29, "big": 1e21, "neg0": math.Copysign(0, -1),
		"str": "ไทย \"q\"", "null": nil, "bool": true,
		"ts": dump.Timestamp{Time: ts}, "geo": dump.GeoPoint{Lat: 13.75, Lng: 100.5}, "ref": dump.Ref{Path: "drivers/d1"},
		"bytes":   dump.Bytes{0, 1, 2},
		"arr":     []any{int64(1), 1.5, "x", []any{}},
		"map":     map[string]any{"a": int64(1), "b": map[string]any{}},
		"escaped": map[string]any{"$ts": "not a timestamp"},
	}
	raw, err := dump.MarshalFields(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"int":3,`, `"double":3.0,`, `"big":1e+21`, `"neg0":-0.0`, `"escaped":{"$map":{"$ts":"not a timestamp"}}`,
		`"ts":{"$ts":"2026-05-01T03:04:05.123456789Z"}`, `"geo":{"$geo":{"lat":13.75,"lng":100.5}}`, `"bytes":{"$bytes":"AAEC"}`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("encoding %s lacks %s", raw, want)
		}
	}
	out, err := dump.UnmarshalFields(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip:\n in %#v\nout %#v", in, out)
	}
	again, _ := dump.MarshalFields(out)
	if string(again) != string(raw) {
		t.Fatalf("re-encoding differs:\n%s\n%s", raw, again)
	}
}

func TestNonFiniteDoubles(t *testing.T) {
	raw, err := dump.MarshalFields(map[string]any{"nan": math.NaN(), "inf": math.Inf(1), "minf": math.Inf(-1)})
	if err != nil {
		t.Fatal(err)
	}
	out, err := dump.UnmarshalFields(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(out["nan"].(float64)) || !math.IsInf(out["inf"].(float64), 1) || !math.IsInf(out["minf"].(float64), -1) {
		t.Fatalf("got %#v from %s", out, raw)
	}
}

func TestBadTagsAreRejected(t *testing.T) {
	for _, raw := range []string{`{"a":{"$ts":"yesterday"}}`, `{"a":{"$geo":{"lat":1}}}`, `{"a":{"$bytes":"%%"}}`, `{"a":{"$double":"1"}}`, `[1]`} {
		if _, err := dump.UnmarshalFields([]byte(raw)); err == nil {
			t.Errorf("%s: want an error", raw)
		}
	}
}

func TestWriterAndReader(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	w, err := dump.NewWriter(dir, at, "proj", "(default)")
	if err != nil {
		t.Fatal(err)
	}
	docs := []dump.Doc{
		{ID: "b", Path: "trip_records/b", UpdateTime: at, CreateTime: at.Add(-time.Hour), Fields: map[string]any{"x": int64(1)}},
		{ID: "a", Path: "trip_records/a", UpdateTime: at, Fields: map[string]any{}},
	}
	if err := w.WriteCollection("trip_records", false, docs); err != nil {
		t.Fatal(err)
	}
	inst := []dump.Doc{{ID: "i1", Path: "drivers/d1/mobile_installations/i1", UpdateTime: at, Fields: map[string]any{"appVersion": "3.2.0"}}}
	if err := w.WriteCollection("mobile_installations", true, inst); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := dump.OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Read("trip_records")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "trip_records/a" || got[1].CreateTime != at.Add(-time.Hour) || got[1].Fields["x"] != int64(1) {
		t.Fatalf("got %+v", got)
	}
	gi, err := d.Read("mobile_installations")
	if err != nil || gi[0].ParentPath() != "drivers/d1" || gi[0].Collection() != "mobile_installations" {
		t.Fatalf("group: %+v %v", gi, err)
	}
	// A changed file no longer matches its digest.
	f := filepath.Join(dir, "trip_records.ndjson.gz")
	b, _ := os.ReadFile(f)
	b[len(b)-1] ^= 0xff
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read("trip_records"); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("tampered file: %v", err)
	}
	if _, err := dump.NewWriter(dir, at, "", ""); err == nil {
		t.Fatal("a directory with a manifest must not be overwritten")
	}
}

func TestReaderChecksCountsAndPaths(t *testing.T) {
	write := func(t *testing.T, manifest, data string) *dump.Dump {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "c.ndjson"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		d, err := dump.OpenDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	m := func(count int) string {
		return `{"format":"logitrack-etl-dump/1","exportedAt":"2026-10-01T00:00:00Z","collections":[{"name":"c","file":"c.ndjson","count":` +
			string(rune('0'+count)) + `}]}`
	}
	ok := `{"_id":"a","_path":"c/a","_updateTime":"2026-01-01T00:00:00Z","fields":{}}` + "\n"
	if _, err := write(t, m(1), ok).Read("c"); err != nil {
		t.Fatal(err)
	}
	if _, err := write(t, m(2), ok).Read("c"); err == nil {
		t.Error("a count mismatch must fail")
	}
	if _, err := write(t, m(1), `{"_id":"a","_path":"other/a","_updateTime":"2026-01-01T00:00:00Z","fields":{}}`).Read("c"); err == nil {
		t.Error("a document of another collection must fail")
	}
	if _, err := write(t, m(1), `{"_id":"a","_path":"c/a","fields":{}}`).Read("c"); err == nil {
		t.Error("a document without _updateTime must fail")
	}
	if _, err := write(t, m(2), ok+ok).Read("c"); err == nil {
		t.Error("a repeated path must fail")
	}
}
