package etl

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
)

// The version gate settings('mobile_app') projected to Firestore settings/mobile_app for installed 3.x APKs (T52R,
// issue #72): the document's two halves in the legacy shape, the legacy spellings kept, a removed floor deleted, and
// the gate of another flavor's fleet held.

const (
	gateLink   = "https://logi.showkhun.co/media/app_releases/prod/logitrack-prod-v3.2.0.apk"
	gateSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// gateJSON is settings('mobile_app') as internal/mobileapp writes it: the floor half (PUT /v1/app-releases/floor) and
// the release half (POST /v1/app-releases), instants as RFC 3339 strings. floor is a JSON literal ("null" = removed).
func gateJSON(floor, flavor string) string {
	return `{"minAllowedVersion":` + floor + `,"minAllowedVersionSetAt":"2026-10-11T09:30:00.123456Z",` +
		`"minAllowedVersionSetBy":"owner@example.test","flavor":"` + flavor + `","latestVersion":"3.2.0","latestBuildNumber":"7",` +
		`"apkDownloadUrl":"` + gateLink + `","apkSha256":"` + gateSHA256 + `","apkSizeBytes":52428800,"releaseNotes":"",` +
		`"releasedAt":"2026-10-10T01:00:00Z","releasedBy":"apikey:release-bot"}`
}

func gateDoc(t *testing.T, raw string) ExportDoc {
	t.Helper()
	d, err := mobileAppSettingsDoc([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMobileAppSettingsDocument(t *testing.T) {
	setAt := time.Date(2026, 10, 11, 9, 30, 0, 123456000, time.UTC)
	d := gateDoc(t, gateJSON(`"3.2.0"`, "prod"))
	if d.Path != "settings/mobile_app" {
		t.Fatalf("path %s", d.Path)
	}
	mask := slices.Sorted(slices.Values(d.Mask))
	if strings.Join(mask, ",") != "apkDownloadUrl,apkSha256,apkSizeBytes,flavor,latestBuildNumber,latestVersion,minAllowedVersion,"+
		"minAllowedVersionSetAt,minAllowedVersionSetBy,releaseNotes,releasedAt,releasedBy" {
		t.Fatalf("mask %v", mask)
	}

	// A new document gets every field with a value, in the types the legacy writers stored: the instants as
	// Timestamps (the legacy page reads only a Timestamp), the size as an integer; empty release notes are no field.
	fields, _ := d.Diff(nil)
	if fields["minAllowedVersion"] != "3.2.0" || fields["apkDownloadUrl"] != gateLink || fields["flavor"] != "prod" ||
		fields["minAllowedVersionSetAt"] != (dump.Timestamp{Time: setAt}) || fields["apkSizeBytes"] != int64(52428800) ||
		fields["latestBuildNumber"] != "7" {
		t.Fatalf("new document %v", fields)
	}
	if _, ok := fields["releaseNotes"]; ok {
		t.Errorf("empty release notes written: %v", fields)
	}

	// The legacy spellings of the same values are kept: a numeric build number, an upper-case flavor, the size as a
	// double, a blank release note, the instant as an ISO string.
	legacy := map[string]any{"minAllowedVersion": "3.2.0", "minAllowedVersionSetAt": "2026-10-11T16:30:00.123456+07:00",
		"minAllowedVersionSetBy": "owner@example.test", "flavor": "PROD", "latestVersion": "3.2.0", "latestBuildNumber": int64(7),
		"apkDownloadUrl": gateLink, "apkSha256": gateSHA256, "apkSizeBytes": 52428800.0, "releaseNotes": " ",
		"releasedAt": dump.Timestamp{Time: time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)}, "releasedBy": "apikey:release-bot"}
	if _, mask := d.Diff(legacy); len(mask) != 0 {
		t.Fatalf("an equivalent gate is rewritten: %v", mask)
	}

	// A floor set in Go moves only the floor half.
	older := mapsWith(legacy, map[string]any{"minAllowedVersion": "3.1.0", "minAllowedVersionSetBy": "admin@example.test",
		"minAllowedVersionSetAt": dump.Timestamp{Time: setAt.Add(-time.Hour)}})
	fields, mask = d.Diff(older)
	slices.Sort(mask)
	if strings.Join(mask, ",") != "minAllowedVersion,minAllowedVersionSetAt,minAllowedVersionSetBy" || fields["minAllowedVersion"] != "3.2.0" {
		t.Fatalf("floor change %v %v", mask, fields)
	}

	// A floor removed in Go (null) deletes the field; the legacy page's "" already says no floor.
	removed := gateDoc(t, gateJSON(`null`, "prod"))
	fields, mask = removed.Diff(legacy)
	if !slices.Contains(mask, "minAllowedVersion") || fields["minAllowedVersion"] != nil {
		t.Fatalf("floor removal %v %v", mask, fields)
	}
	if _, mask := removed.Diff(mapsWith(legacy, map[string]any{"minAllowedVersion": ""})); slices.Contains(mask, "minAllowedVersion") {
		t.Errorf("an empty legacy floor is rewritten: %v", mask)
	}

	// A value of another JSON type than the loader writes is left as Firestore has it, neither written nor deleted.
	odd := gateDoc(t, `{"minAllowedVersion":3,"releasedAt":"not a time","apkSizeBytes":"big","flavor":"staging"}`)
	for _, f := range []string{"minAllowedVersion", "releasedAt", "apkSizeBytes"} {
		if slices.Contains(odd.Mask, f) {
			t.Errorf("%s of another type is in the mask", f)
		}
	}
	if odd.Hold != nil {
		t.Error("a gate without a flavor holds the stored document")
	}
	// No flavor in PostgreSQL keeps the stored one; no size matches a stored size the loader reads as none.
	if _, mask := odd.Diff(map[string]any{"flavor": "prod", "apkSizeBytes": int64(0), "latestVersion": "3.2.0"}); slices.Contains(mask, "flavor") {
		t.Errorf("the stored flavor is deleted: %v", mask)
	}
	if !noSize(int64(0), true) || !noSize(nil, false) || noSize(int64(1), true) {
		t.Error("noSize")
	}
	if !sameText("7")(int64(7), true) || sameText("7")("8", true) || !sameText("")(nil, false) || sameText("")("3.1.0", true) {
		t.Error("sameText")
	}
}

// The stored flavor names the fleet whose Firestore project this is: a gate of another flavor is held, never rewritten.
func TestMobileAppSettingsHoldsAnotherFleet(t *testing.T) {
	d := gateDoc(t, gateJSON(`"3.2.0"`, "prod"))
	for stored, held := range map[any]bool{"dev": true, "DEV": true, "prod": false, " Prod ": false, nil: false, "": false} {
		reason := d.Hold(map[string]any{"flavor": stored})
		if (reason != "") != held {
			t.Errorf("stored flavor %v: hold %q", stored, reason)
		}
		if held && (!strings.Contains(reason, "dev") || !strings.Contains(reason, "ETL_FIRESTORE_PROJECT_ID")) {
			t.Errorf("hold reason %q", reason)
		}
	}
}

// Through the Firestore client (the in-process stand-in): the document is created when missing, written only when it
// differs, the floor and its link land in one write, a removed floor is deleted, and a held document is not touched.
func TestProjectorWritesTheVersionGate(t *testing.T) {
	ctx := context.Background()
	b := gcptest.New(t, "logitrack-test")
	p := &Projector{FS: b.Firestore(), Collections: ProjectedCollections, Owned: set("mobile_release"), Log: zerolog.Nop()}
	write := func(raw string) ProjectStats {
		t.Helper()
		st, err := p.write(ctx, "settings", []ExportDoc{gateDoc(t, raw)})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	if st := write(gateJSON(`"3.2.0"`, "prod")); st.Written != 1 {
		t.Fatalf("first projection %+v", st)
	}
	doc, ok := b.Doc("settings/mobile_app")
	if !ok || doc.Fields["minAllowedVersion"] != "3.2.0" || doc.Fields["apkDownloadUrl"] != gateLink {
		t.Fatalf("created gate %v", doc.Fields)
	}
	if w := b.Writes(); len(w) != 1 || !slices.Contains(w[0].Mask, "minAllowedVersion") || !slices.Contains(w[0].Mask, "apkDownloadUrl") {
		t.Fatalf("the floor and its link are not one write: %+v", w)
	}
	if st := write(gateJSON(`"3.2.0"`, "prod")); st.Unchanged != 1 || len(b.Writes()) != 1 {
		t.Fatalf("an unchanged gate is written again: %+v", st)
	}

	// The legacy page saved the same values in its own spellings: still unchanged.
	b.Merge("settings/mobile_app", map[string]any{"flavor": "PROD", "latestBuildNumber": int64(7)})
	writes := len(b.Writes())
	if st := write(gateJSON(`"3.2.0"`, "prod")); st.Unchanged != 1 || len(b.Writes()) != writes {
		t.Fatalf("a legacy spelling is rewritten: %+v", st)
	}

	// The floor removed in Go: the field goes (the app reads no floor and clears its cache), the link stays.
	if st := write(gateJSON(`null`, "prod")); st.Written != 1 {
		t.Fatalf("floor removal %+v", st)
	}
	doc, _ = b.Doc("settings/mobile_app")
	if _, ok := doc.Fields["minAllowedVersion"]; ok || doc.Fields["apkDownloadUrl"] != gateLink {
		t.Fatalf("after the removal %v", doc.Fields)
	}

	// A Firestore project of the dev fleet is not the prod gate's: held, nothing written.
	b.Merge("settings/mobile_app", map[string]any{"flavor": "dev", "minAllowedVersion": "1.0.0"})
	writes = len(b.Writes())
	if st := write(gateJSON(`"3.2.0"`, "prod")); st.Skipped != 1 || st.Written != 0 || len(b.Writes()) != writes {
		t.Fatalf("another fleet's gate: %+v", st)
	}
	if doc, _ := b.Doc("settings/mobile_app"); doc.Fields["minAllowedVersion"] != "1.0.0" {
		t.Errorf("the held gate changed: %v", doc.Fields)
	}
}

// mapsWith returns a copy of m with the entries of over.
func mapsWith(m, over map[string]any) map[string]any {
	out := maps.Clone(m)
	maps.Copy(out, over)
	return out
}
