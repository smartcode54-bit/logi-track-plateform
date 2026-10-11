//go:build integration

// The projection of the version gate settings('mobile_app') to Firestore settings/mobile_app (T52R, issue #72) on
// postgres:18-alpine, against the in-process Firestore stand-in: the fixture's legacy gate round-trips unchanged, the
// writes of internal/mobileapp (the floor half, the release half, the floor removed) reach the document field by
// field, the projection runs only once mobile_release is PostgreSQL-owned, and the reconciliation sees a held gate.
package etl

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// mergeGate merges one writer's half into settings('mobile_app') exactly as mobileapp's MergeMobileAppSettings does.
func (h *syncHarness) mergeGate(half string) {
	h.t.Helper()
	h.exec(`INSERT INTO settings (key, value) VALUES ('mobile_app', $1::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = settings.value || EXCLUDED.value`, half)
}

func (h *syncHarness) projectGate(p *Projector) ProjectStats {
	h.t.Helper()
	st, err := p.Project(h.ctx, ShadowTarget{Collection: "settings", All: true})
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

func (h *syncHarness) reconcileGate(o map[string]bool) Check {
	h.t.Helper()
	docs, err := h.client.Documents(h.ctx, "settings", false)
	if err != nil {
		h.t.Fatal(err)
	}
	rec := &Reconciliation{Match: true}
	if err := h.e.cfg.Tx(h.ctx, func(tx pgx.Tx) error { return reconcileProjection(h.ctx, tx, "settings", docs, o, rec) }); err != nil {
		h.t.Fatal(err)
	}
	if len(rec.Checks) != 1 {
		h.t.Fatalf("checks %+v", rec.Checks)
	}
	return rec.Checks[0]
}

func TestProjectionOfTheVersionGate(t *testing.T) {
	h := newSyncHarness(t)
	p6 := owned("auth", "masterdata", "mobile_release")
	proj := h.projector(p6)

	// While mobile_release is Firestore-owned (P1-P5) the gate is mirrored, never projected.
	if st := h.projectGate(h.projector(owned("auth", "masterdata"))); st.NotProjected != 1 || st.Written != 0 {
		t.Fatalf("projected while Firestore-owned: %+v", st)
	}
	if h.plan(p6).Mode("settings") != "" {
		t.Fatal("the gate is mirrored back while it is projected")
	}

	// The legacy gate loaded at P1 is what Firestore already says: nothing to write, and the reconciliation matches
	// (the collection's other settings documents are not reported).
	writes := len(h.fs.Writes())
	if st := h.projectGate(proj); st.Unchanged != 1 || len(h.fs.Writes()) != writes {
		t.Fatalf("the loaded gate is rewritten: %+v", st)
	}
	if c := h.reconcileGate(p6); !c.OK || c.Source != "1" || c.Target != "1" {
		t.Fatalf("reconciliation of the loaded gate %+v", c)
	}

	// PUT /v1/app-releases/floor: the floor half only. The document gets the new floor (installed APKs block below it)
	// next to the link it already had, and the release half is not touched.
	setAt := time.Date(2026, 10, 11, 9, 30, 0, 123456000, time.UTC)
	h.mergeGate(`{"minAllowedVersion":"3.2.0","minAllowedVersionSetAt":"2026-10-11T09:30:00.123456Z","minAllowedVersionSetBy":"owner@example.test"}`)
	if st := h.projectGate(proj); st.Written != 1 {
		t.Fatalf("floor projection %+v", st)
	}
	w := h.fs.Writes()
	if mask := slices.Sorted(slices.Values(w[len(w)-1].Mask)); strings.Join(mask, ",") != "minAllowedVersion,minAllowedVersionSetAt,minAllowedVersionSetBy" {
		t.Fatalf("floor write mask %v", mask)
	}
	f := h.doc("settings/mobile_app")
	if f["minAllowedVersion"] != "3.2.0" || f["minAllowedVersionSetAt"] != (dump.Timestamp{Time: setAt}) ||
		!strings.HasPrefix(f["apkDownloadUrl"].(string), "https://firebasestorage.googleapis.com/") || f["latestVersion"] != "3.2.0" {
		t.Fatalf("gate after the floor %v", f)
	}
	if st := h.projectGate(proj); st.Unchanged != 1 {
		t.Fatalf("the floor is written twice: %+v", st)
	}

	// POST /v1/app-releases: the release half only; the floor stays, and the new link reaches the APKs with it.
	link := "https://logi.showkhun.co/media/app_releases/prod/logitrack-prod-v3.3.0.apk"
	h.mergeGate(`{"flavor":"prod","latestVersion":"3.3.0","latestBuildNumber":"9","apkDownloadUrl":"` + link + `",` +
		`"apkSha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","apkSizeBytes":52428800,"releaseNotes":"",` +
		`"releasedAt":"2026-10-12T01:00:00Z","releasedBy":"apikey:release-bot"}`)
	if st := h.projectGate(proj); st.Written != 1 {
		t.Fatalf("release projection %+v", st)
	}
	f = h.doc("settings/mobile_app")
	if f["apkDownloadUrl"] != link || f["latestVersion"] != "3.3.0" || f["apkSizeBytes"] != int64(52428800) || f["minAllowedVersion"] != "3.2.0" {
		t.Fatalf("gate after the release %v", f)
	}
	if _, ok := f["releaseNotes"]; ok {
		t.Errorf("the previous release's notes stay: %v", f["releaseNotes"])
	}

	// The floor removed in Go (null): the field goes, which the 3.x app reads as no floor (and clears its cached one).
	h.mergeGate(`{"minAllowedVersion":null,"minAllowedVersionSetAt":"2026-10-12T02:00:00Z","minAllowedVersionSetBy":"owner@example.test"}`)
	if st := h.projectGate(proj); st.Written != 1 {
		t.Fatalf("removal projection %+v", st)
	}
	f = h.doc("settings/mobile_app")
	if _, ok := f["minAllowedVersion"]; ok || f["apkDownloadUrl"] != link {
		t.Fatalf("gate after the removal %v", f)
	}
	if c := h.reconcileGate(p6); !c.OK {
		t.Fatalf("reconciliation after the projections %+v", c)
	}

	// A Firestore project of another flavor's fleet: held (never written), reported by the reconciliation.
	h.fs.Merge("settings/mobile_app", map[string]any{"flavor": "dev", "minAllowedVersion": "1.0.0"})
	writes = len(h.fs.Writes())
	if st := h.projectGate(proj); st.Skipped != 1 || len(h.fs.Writes()) != writes {
		t.Fatalf("another fleet's gate: %+v", st)
	}
	if c := h.reconcileGate(p6); c.OK || !strings.Contains(c.Note, "held") {
		t.Fatalf("a held gate is not reported: %+v", c)
	}

	// No gate in PostgreSQL (a deployment before its first publish): nothing projected, nothing deleted.
	h.fs.Merge("settings/mobile_app", map[string]any{"flavor": "prod"})
	h.exec(`DELETE FROM settings WHERE key = 'mobile_app'`)
	writes = len(h.fs.Writes())
	if st := h.projectGate(proj); st.Written+st.Unchanged+st.Skipped != 0 || len(h.fs.Writes()) != writes {
		t.Fatalf("no gate: %+v", st)
	}
}
