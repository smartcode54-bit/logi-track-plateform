//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// orphanScanFile is the expected result of tenancy.orphan-scan after the fixture load (Appendix D §D.5.3), with every
// id written as the legacy document id that loaded it (uuids differ on every load).
const orphanScanFile = "testdata/firestore-fixtures/orphan_scan.json"

// scanFixture is a tenancy.ScanResult in legacy ids.
type scanFixture struct {
	Note                string         `json:"_note"`
	QuarantineRows      map[string]int `json:"quarantineRows"`
	DriverMoved         []driftFixture `json:"driverMoved"`
	TruckTenantMismatch []driftFixture `json:"truckTenantMismatch"`
}

type driftFixture struct {
	Table        string `json:"table"`
	Doc          string `json:"doc"`
	Driver       string `json:"driver"`
	RowTenant    string `json:"rowTenant"`
	DriverTenant string `json:"driverTenant,omitempty"`
	Truck        string `json:"truck,omitempty"`
	TruckTenant  string `json:"truckTenant,omitempty"`
}

// T28 (issue #48), Appendix D §D.5.3: after the P1 load of the fixture dump, the orphan scan (tenancy.Scan as the worker
// runs it: logitrack_app in a system transaction) reports the quarantine rows the load produced and the R24 drift of
// the broker fixture, equal to orphan_scan.json, and changes no row. -update rewrites the file (with the snapshot of
// TestFixtureLoad) after an intended mapping change.
func TestOrphanScanAfterTheFixtureLoad(t *testing.T) {
	h := newP0Harness(t)
	h.mustRun("load", "--fixtures")
	app := h.d.Pool(t, db.RoleApp)
	before := h.rowVersions()
	var res tenancy.ScanResult
	if err := db.WithSystem(h.ctx, app, nil, func(tx pgx.Tx) error {
		var err error
		res, err = tenancy.Scan(h.ctx, tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if after := h.rowVersions(); !maps.Equal(before, after) {
		t.Fatal("the orphan scan changed a row")
	}
	if res.DriverMovedTotal != len(res.DriverMoved) || res.TruckTenantMismatchTotal != len(res.TruckTenantMismatch) {
		t.Fatalf("the fixture's drift lists were cut: %+v", res)
	}

	doc := func(table string, id *uuid.UUID) string {
		t.Helper()
		if id == nil {
			return ""
		}
		var legacy *string
		var kind *string
		q := `SELECT legacy_doc_id, NULL::text FROM ` + pgx.Identifier{table}.Sanitize() + ` WHERE id = $1`
		if table == "tenants" {
			q = `SELECT legacy_doc_id, kind FROM tenants WHERE id = $1`
		}
		if err := h.pool.QueryRow(h.ctx, q, *id).Scan(&legacy, &kind); err != nil {
			t.Fatalf("%s %s: %v", table, id, err)
		}
		switch {
		case kind != nil && *kind != "carrier":
			return *kind // own_fleet, quarantine: no legacy subcontractors document
		case legacy == nil:
			return "(no legacy id) " + id.String()
		}
		return *legacy
	}
	drift := func(ds []tenancy.Drift) []driftFixture {
		out := []driftFixture{}
		for _, d := range ds {
			out = append(out, driftFixture{Table: d.Table, Doc: doc(d.Table, &d.ID), Driver: doc("drivers", &d.DriverID),
				RowTenant: doc("tenants", &d.RowTenant), DriverTenant: doc("tenants", d.DriverTenant),
				Truck: doc("trucks", d.TruckID), TruckTenant: doc("tenants", d.TruckTenant)})
		}
		return out
	}
	got := scanFixture{QuarantineRows: res.QuarantineRows, DriverMoved: drift(res.DriverMoved),
		TruckTenantMismatch: drift(res.TruckTenantMismatch)}
	if *update {
		got.Note = "Expected tenancy.orphan-scan result after `etl load --fixtures` (T28, Appendix D §D.5.3); ids are the legacy " +
			"document ids (tenants: the subcontractors doc id, or own_fleet / quarantine). Rewritten by go test ./cmd/etl -tags=integration -update."
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(orphanScanFile, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(orphanScanFile)
	if err != nil {
		t.Fatalf("%v (run the test once with -update)", err)
	}
	var want scanFixture
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	got.Note = want.Note
	gb, _ := json.MarshalIndent(got, "", "  ")
	wb, _ := json.MarshalIndent(want, "", "  ")
	if !bytes.Equal(gb, wb) {
		t.Fatalf("orphan scan after the fixture load differs from %s:\n%s", orphanScanFile, diff(string(wb), string(gb)))
	}
}
