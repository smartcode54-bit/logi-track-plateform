package gcp_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
)

func at(s int) time.Time { return time.Date(2026, 1, 1, 0, 0, s, 0, time.UTC) }

func TestDocumentsPagesAndKeepsTypes(t *testing.T) {
	b := gcptest.New(t, "logitrack-test")
	for i := range 301 {
		b.Put(dump.Doc{ID: fmt.Sprintf("t%03d", i), Path: fmt.Sprintf("trip_records/t%03d", i), CreateTime: at(1), UpdateTime: at(2),
			Fields: map[string]any{"n": int64(i)}})
	}
	b.Put(dump.Doc{ID: "i1", Path: "drivers/d1/mobile_installations/i1", CreateTime: at(1), UpdateTime: at(2), Fields: map[string]any{}})
	b.Put(dump.Doc{ID: "i2", Path: "drivers/d2/mobile_installations/i2", CreateTime: at(1), UpdateTime: at(2), Fields: map[string]any{}})
	b.Put(dump.Doc{ID: "d1", Path: "drivers/d1", CreateTime: at(1), UpdateTime: at(3), Fields: map[string]any{
		"ts": dump.Timestamp{Time: at(9)}, "geo": dump.GeoPoint{Lat: 1.5, Lng: -2}, "ref": dump.Ref{Path: "trucks/x"},
		"nan": math.Inf(1), "arr": []any{int64(1), 2.5, nil}, "m": map[string]any{"b": dump.Bytes("hi"), "d": 1.0},
	}})
	f := b.Firestore()
	ctx := context.Background()
	docs, err := f.Documents(ctx, "trip_records", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 301 || docs[300].Path != "trip_records/t300" || docs[7].Fields["n"] != int64(7) || b.RunQueryCalls() != 2 {
		t.Fatalf("got %d docs, last %v, %d calls", len(docs), docs[len(docs)-1].Path, b.RunQueryCalls())
	}
	inst, err := f.Documents(ctx, "mobile_installations", true)
	if err != nil || len(inst) != 2 || inst[0].ParentPath() != "drivers/d1" {
		t.Fatalf("group: %v %v", inst, err)
	}
	top, err := f.Documents(ctx, "mobile_installations", false)
	if err != nil || len(top) != 0 {
		t.Fatalf("a non-group query must not descend: %v %v", top, err)
	}
	d, err := f.Get(ctx, "drivers/d1")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := b.Doc("drivers/d1")
	if !reflect.DeepEqual(d.Fields, want.Fields) || !d.UpdateTime.Equal(at(3)) {
		t.Fatalf("get: %#v", d)
	}
	if _, err := f.Get(ctx, "drivers/none"); !errors.Is(err, gcp.ErrNotFound) {
		t.Fatalf("missing doc: %v", err)
	}
	ids, err := f.CollectionIDs(ctx)
	if err != nil || !reflect.DeepEqual(ids, []string{"drivers", "trip_records"}) {
		t.Fatalf("collection ids %v %v", ids, err)
	}
}

// A paged read is one snapshot (review T15): writes that commit between the pages are in no page, even when the
// written document's page comes later, so the newest updateTime of the read never passes a write the read missed.
func TestDocumentsReadsOneSnapshot(t *testing.T) {
	b := gcptest.New(t, "logitrack-test")
	for i := range 301 {
		b.Put(dump.Doc{ID: fmt.Sprintf("c%03d", i), Path: fmt.Sprintf("customers/c%03d", i), CreateTime: at(1), UpdateTime: at(2),
			Fields: map[string]any{"name": "old"}})
	}
	b.OnRunQuery(func(call int) {
		if call != 1 {
			return
		}
		// After page 1 (c000-c299): c000 was already read, c300 not yet.
		b.Put(dump.Doc{ID: "c000", Path: "customers/c000", CreateTime: at(1), UpdateTime: at(60), Fields: map[string]any{"name": "new"}})
		b.Put(dump.Doc{ID: "c300", Path: "customers/c300", CreateTime: at(1), UpdateTime: at(120), Fields: map[string]any{"name": "new"}})
		b.Put(dump.Doc{ID: "c301", Path: "customers/c301", CreateTime: at(120), UpdateTime: at(120), Fields: map[string]any{"name": "new"}})
	})
	f := b.Firestore()
	docs, err := f.Documents(context.Background(), "customers", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 301 || b.RunQueryCalls() != 2 {
		t.Fatalf("got %d docs in %d calls", len(docs), b.RunQueryCalls())
	}
	for _, d := range docs {
		if d.Fields["name"] != "old" || !d.UpdateTime.Equal(at(2)) {
			t.Fatalf("%s: a write after the read started leaked into the snapshot: %#v %v", d.Path, d.Fields, d.UpdateTime)
		}
	}
	b.OnRunQuery(nil)
	docs, err = f.Documents(context.Background(), "customers", false)
	if err != nil || len(docs) != 302 || docs[0].Fields["name"] != "new" || docs[300].Fields["name"] != "new" {
		t.Fatalf("the next read sees the writes: %d docs, %v", len(docs), err)
	}
}

func TestPatchPreconditionsAndMask(t *testing.T) {
	b := gcptest.New(t, "logitrack-test")
	b.Put(dump.Doc{ID: "c1", Path: "customers/c1", CreateTime: at(1), UpdateTime: at(2), Fields: map[string]any{"name": "A", "keep": true, "gone": "x"}})
	f := b.Firestore()
	ctx := context.Background()
	no := false
	if _, err := f.Patch(ctx, "customers/c1", map[string]any{"name": "B"}, []string{"name"}, gcp.Precondition{Exists: &no}); !errors.Is(err, gcp.ErrPrecondition) {
		t.Fatalf("create-only on an existing doc: %v", err)
	}
	if _, err := f.Patch(ctx, "customers/c1", map[string]any{"name": "B"}, []string{"name"}, gcp.Precondition{UpdateTime: at(1)}); !errors.Is(err, gcp.ErrPrecondition) {
		t.Fatalf("stale updateTime: %v", err)
	}
	got, err := f.Patch(ctx, "customers/c1", map[string]any{"name": "B", "odd key": int64(1)}, []string{"name", "gone", "odd key"}, gcp.Precondition{UpdateTime: at(2)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Fields, map[string]any{"name": "B", "keep": true, "odd key": int64(1)}) || !got.UpdateTime.After(at(2)) {
		t.Fatalf("masked patch: %#v", got)
	}
}

func TestGCSDownload(t *testing.T) {
	b := gcptest.New(t, "logitrack-test")
	b.PutObject("legacy-bucket", "trips/T1/a b.jpg", "image/jpeg", []byte("jpeg"))
	g := b.GCS()
	o, err := g.Open(context.Background(), "legacy-bucket", "trips/T1/a b.jpg")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(o.Body)
	_ = o.Body.Close()
	if string(data) != "jpeg" || o.Size != 4 || o.ContentType != "image/jpeg" {
		t.Fatalf("got %q %d %q", data, o.Size, o.ContentType)
	}
	if _, err := g.Open(context.Background(), "legacy-bucket", "missing"); !errors.Is(err, gcp.ErrNotFound) {
		t.Fatalf("missing object: %v", err)
	}
}
