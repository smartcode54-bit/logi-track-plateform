//go:build integration

// The coexistence commands of T24S (#44): `etl project` (one full firestore.shadow pass), `etl mirror` (one etl.sync
// sweep) and `etl reconcile-sync` (the daily reconciliation) against the in-process Firestore stand-in.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
)

func TestCoexistenceCommands(t *testing.T) {
	h := newP0Harness(t)
	h.mustRun("load", "--fixtures")
	fake := gcptest.New(t, "logitrack-fixtures")
	key := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(key, fake.ServiceAccountJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	h.extra = []string{"ETL_FIRESTORE_PROJECT_ID=logitrack-fixtures", "GOOGLE_APPLICATION_CREDENTIALS=" + key}
	h.deps.google = fake.Client()
	fixture, err := dump.OpenDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range fixture.Names() {
		docs, err := fixture.Read(n)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range docs {
			fake.Put(d)
		}
	}
	for _, cmd := range []string{"mirror", "project", "reconcile-sync"} {
		if code, _, errb := h.run(cmd); code != 2 || !strings.Contains(errb, "--owned is required") {
			t.Errorf("%s without --owned: exit %d %s", cmd, code, errb)
		}
	}
	if code, _, errb := h.run("mirror", "--owned", "masterdata,nope"); code != 2 || !strings.Contains(errb, "--owned") {
		t.Errorf("a bad --owned: exit %d %s", code, errb)
	}
	if out := h.mustRun("project", "--owned", "none"); !strings.Contains(out, "nothing to project") {
		t.Errorf("project with masterdata on Firestore: %s", out)
	}
	out := h.mustRun("project", "--owned", "auth,masterdata")
	if !strings.Contains(out, "document(s) written") || !strings.Contains(out, "hubs") {
		t.Fatalf("project: %s", out)
	}
	if _, ok := fake.Doc("trucks/trk_own1"); !ok {
		t.Fatal("no truck document")
	}
	fake.Merge("drivers/drv_own", map[string]any{"activeTruck": map[string]any{"truckId": "trk_alpha1", "truckPlate": "71-5678",
		"taskId": "task_1", "startedAt": dump.Timestamp{Time: time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)}}})
	out = h.mustRun("mirror", "--owned", "auth,masterdata")
	if !strings.Contains(out, "drivers") || !strings.Contains(out, "fields") || !strings.Contains(out, "mirror:") {
		t.Fatalf("mirror: %s", out)
	}
	if v := h.scalar(`SELECT t.legacy_doc_id FROM drivers d JOIN trucks t ON t.id = d.active_truck_id WHERE d.legacy_doc_id = 'drv_own'`); v != "trk_alpha1" {
		t.Errorf("active truck %v", v)
	}
	reports := t.TempDir()
	if out := h.mustRun("reconcile-sync", "--owned", "auth,masterdata", "--out", reports); !strings.Contains(out, "reconcile-sync: match") {
		t.Fatalf("reconcile-sync: %s", out)
	}
	if files, _ := filepath.Glob(filepath.Join(reports, "reconcile-sync-*.md")); len(files) != 1 {
		t.Errorf("report files %v", files)
	}
	// T29 (#49): renameTripRecord's batch swept by `etl mirror` is one row with the new number and one history row, and
	// the daily reconciliation (the operations checks included) still exits 0.
	trip := h.scalar(`SELECT id::text FROM trip_records WHERE legacy_doc_id = 'TRIP001'`)
	at := dump.Timestamp{Time: time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC)}
	fake.Rename("trip_records/TRIP001", "trip_records/TRIP001R", map[string]any{"spxTripId": "TRIP001R", "renamedFromTripId": "TRIP001",
		"renamedAt": at, "updatedAt": at})
	h.mustRun("mirror", "--owned", "auth,masterdata")
	if v := h.scalar(`SELECT count(*) FROM trip_records WHERE trip_no IN ('TRIP001', 'TRIP001R')`); v != int64(1) {
		t.Fatalf("rows after the rename: %v", v)
	}
	if v := h.scalar(`SELECT trip_no || ' ' || (SELECT count(*) FROM trip_no_history h WHERE h.trip_id = r.id)::text FROM trip_records r
		WHERE id::text = $1`, trip); v != "TRIP001R 1" {
		t.Fatalf("the renamed trip: %v", v)
	}
	if out := h.mustRun("reconcile-sync", "--owned", "auth,masterdata"); !strings.Contains(out, "reconcile-sync: match") {
		t.Fatalf("reconcile-sync after the rename: %s", out)
	}
	fake.Merge("customers/cust_cjsf", map[string]any{"name": "tampered"})
	code, out, _ := h.run("reconcile-sync", "--owned", "auth,masterdata")
	if code != 2 || !strings.Contains(out, "MISMATCH\tprojection\tcustomers") {
		t.Errorf("drift: exit %d %s", code, out)
	}
}
