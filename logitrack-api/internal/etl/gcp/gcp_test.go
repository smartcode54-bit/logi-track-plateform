package gcp_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

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

// The listeners' query (T29): the documents whose timestamp field is at or after an instant, ordered by it and paged with
// a (timestamp, name) cursor, all at one read time; a document without the field, or with another type in it, is not
// returned. DocumentsAt and Lookup read at a given read time, so a delete after it is not seen.
func TestChangedDocumentsAtAndLookup(t *testing.T) {
	b := gcptest.New(t, "logitrack-test")
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := range 700 {
		b.Set(fmt.Sprintf("tasks/t%03d", i), map[string]any{"updatedAt": dump.Timestamp{Time: base.Add(time.Duration(i%7) * time.Minute)}})
	}
	b.Set("tasks/nofield", map[string]any{"x": int64(1)})
	b.Set("tasks/text", map[string]any{"updatedAt": "2026-10-01"})
	f := b.Firestore()
	ctx := context.Background()
	docs, read, err := f.Changed(ctx, "tasks", "updatedAt", base.Add(3*time.Minute), time.Time{})
	if err != nil || read.IsZero() {
		t.Fatal(err, read)
	}
	want := 0
	for i := range 700 {
		if i%7 >= 3 {
			want++
		}
	}
	if len(docs) != want || b.RunQueryCalls() < 2 {
		t.Fatalf("changed: %d docs in %d calls, want %d", len(docs), b.RunQueryCalls(), want)
	}
	seen := map[string]bool{}
	var prev time.Time
	for _, d := range docs {
		ts := d.Fields["updatedAt"].(dump.Timestamp).Time
		if seen[d.Path] || ts.Before(prev) || ts.Before(base.Add(3*time.Minute)) {
			t.Fatalf("order or repeat at %s (%v after %v)", d.Path, ts, prev)
		}
		seen[d.Path], prev = true, ts
	}
	// Read at the earlier read time, a later delete is not seen.
	all, at, err := f.DocumentsAt(ctx, "tasks", false, time.Time{})
	if err != nil || len(all) != 702 {
		t.Fatalf("documents %d %v", len(all), err)
	}
	b.Delete("tasks/t000")
	if _, ok, err := f.Lookup(ctx, "tasks/t000", at); !ok || err != nil {
		t.Fatalf("lookup at the read time: %v %v", ok, err)
	}
	if _, ok, err := f.Lookup(ctx, "tasks/t000", time.Time{}); ok || err != nil {
		t.Fatalf("lookup now: %v %v", ok, err)
	}
	again, _, err := f.DocumentsAt(ctx, "tasks", false, at)
	if err != nil || len(again) != 702 {
		t.Fatalf("documents at the earlier read time: %d %v", len(again), err)
	}
	now, _, err := f.DocumentsAt(ctx, "tasks", false, time.Time{})
	if err != nil || len(now) != 701 {
		t.Fatalf("documents now: %d %v", len(now), err)
	}
	// Rename: one tick copies and deletes.
	nd := b.Rename("tasks/t001", "tasks/t001x", map[string]any{"renamedFrom": "t001"})
	if _, ok := b.Doc("tasks/t001"); ok || nd.Fields["renamedFrom"] != "t001" {
		t.Fatalf("rename %+v", nd)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// T31 review: real Firestore answers a commit whose currentDocument.exists=true names a missing document with 404
// NOT_FOUND ("No document to update", "No document to delete"), and a create of an existing one with 409
// ALREADY_EXISTS; Commit reports both as ErrPrecondition (the write-back's 409 document_changed). Any other 404 (a
// database or project that does not exist) stays ErrNotFound (a 503 of the write-back).
func TestCommitPreconditionsAsFirestoreAnswersThem(t *testing.T) {
	b := gcptest.New(t, "logitrack-test")
	b.Put(dump.Doc{ID: "t1", Path: "tasks/t1", CreateTime: at(1), UpdateTime: at(2), Fields: map[string]any{"a": "x"}})
	f := b.Firestore()
	ctx := context.Background()
	yes, no := true, false
	for name, w := range map[string]gcp.Write{
		"update of a missing document": {Path: "tasks/gone", Fields: map[string]any{"a": "b"}, Mask: []string{"a"}, Pre: gcp.Precondition{Exists: &yes}},
		"delete of a missing document": {Path: "tasks/gone", Delete: true, Pre: gcp.Precondition{Exists: &yes}},
		"create of an existing one":    {Path: "tasks/t1", Fields: map[string]any{"a": "b"}, Pre: gcp.Precondition{Exists: &no}},
		"a stale update time":          {Path: "tasks/t1", Fields: map[string]any{"a": "b"}, Mask: []string{"a"}, Pre: gcp.Precondition{UpdateTime: at(1)}},
	} {
		if _, err := f.Commit(ctx, []gcp.Write{w}); !errors.Is(err, gcp.ErrPrecondition) || errors.Is(err, gcp.ErrNotFound) {
			t.Errorf("%s: %v, want ErrPrecondition", name, err)
		}
	}
	missingDB := roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": {"application/json"}}, Request: r,
			Body: io.NopCloser(strings.NewReader(`{"error":{"code":404,"status":"NOT_FOUND",` +
				`"message":"The database (default) does not exist for project logitrack-test"}}`))}, nil
	})
	f2, err := gcp.NewFirestore("logitrack-test", "", &http.Client{Transport: missingDB}, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f2.Commit(ctx, []gcp.Write{{Path: "tasks/t1", Fields: map[string]any{"a": "b"}, Mask: []string{"a"}, Pre: gcp.Precondition{Exists: &yes}}})
	if !errors.Is(err, gcp.ErrNotFound) || errors.Is(err, gcp.ErrPrecondition) || strings.Contains(err.Error(), "does not exist for project") {
		t.Fatalf("a missing database: %v, want ErrNotFound without the message", err)
	}
}
