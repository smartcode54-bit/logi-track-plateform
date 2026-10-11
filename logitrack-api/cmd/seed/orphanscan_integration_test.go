//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/cmd/seed/internal/seed"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// T28 (issue #48) acceptance criterion: the orphan scan reports the seeded broker-driver drift without moving rows.
// After seed --profile smoke, tenancy.Scan (the work of the tenancy.orphan-scan job, run as logitrack_app in a system
// transaction as the worker runs it) returns the result the smoke fixture records for its scan job JB2
// (testdata/smoke/jobs.json, Appendix D §D.4 scenario 19): the quarantined legacy task TK25, and TK02, stamped from
// the broker driver D6 on NWR before D6 moved to TTP (R24). No row of the database changes.
func TestOrphanScanReportsTheSeededBrokerDrift(t *testing.T) {
	s := newStack(t, "local")
	s.mustSeed(t, "--profile", "smoke")
	ctx := context.Background()

	var jb2 struct {
		ID     string `json:"id"`
		Result struct {
			QuarantineRows map[string]int `json:"quarantineRows"`
			DriverMoved    []struct {
				Table, ID, DriverID, RowTenant, DriverTenant string
			} `json:"driverMoved"`
			TruckTenantMismatch []json.RawMessage `json:"truckTenantMismatch"`
		} `json:"result"`
	}
	b, err := os.ReadFile("testdata/smoke/jobs.json")
	if err != nil {
		t.Fatal(err)
	}
	var jobsFixture []json.RawMessage
	if err := json.Unmarshal(b, &jobsFixture); err != nil {
		t.Fatal(err)
	}
	for _, j := range jobsFixture {
		if strings.Contains(string(j), `"@JB2"`) {
			if err := json.Unmarshal(j, &jb2); err != nil {
				t.Fatal(err)
			}
		}
	}
	if jb2.ID != "@JB2" || len(jb2.Result.DriverMoved) == 0 {
		t.Fatalf("no JB2 orphan-scan result in the smoke fixture: %+v", jb2)
	}
	reg, err := seed.ReadRegistry(os.DirFS("testdata"), "registry.json")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]uuid.UUID{"@TN_QUAR": uuid.MustParse(tenancy.QuarantineTenantID)}
	for _, e := range reg {
		ids["@"+e.Symbol] = seed.ID(seed.DefaultNamespace, e.Table, e.Key)
	}
	sym := func(s string) uuid.UUID {
		id, ok := ids[s]
		if !ok {
			t.Fatalf("symbol %s is not in the registry", s)
		}
		return id
	}

	pool := s.d.Pool(t, db.RoleApp)
	fingerprint := func() string {
		var f string
		if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT coalesce(string_agg(x, ',' ORDER BY x), '') FROM (
				SELECT id::text || xmin::text AS x FROM tasks UNION ALL SELECT id::text || xmin::text FROM trip_records
				UNION ALL SELECT id::text || xmin::text FROM drivers UNION ALL SELECT id::text || xmin::text FROM standby_records) y`).Scan(&f)
		}); err != nil {
			t.Fatal(err)
		}
		return f
	}
	before := fingerprint()
	var res tenancy.ScanResult
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		var err error
		res, err = tenancy.Scan(ctx, tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if fingerprint() != before {
		t.Fatal("the orphan scan changed a row")
	}

	if len(res.QuarantineRows) != len(jb2.Result.QuarantineRows) {
		t.Errorf("quarantineRows %v, JB2 records %v", res.QuarantineRows, jb2.Result.QuarantineRows)
	}
	for table, n := range jb2.Result.QuarantineRows {
		if res.QuarantineRows[table] != n {
			t.Errorf("quarantineRows[%s] = %d, JB2 records %d", table, res.QuarantineRows[table], n)
		}
	}
	if res.DriverMovedTotal != len(jb2.Result.DriverMoved) || len(res.DriverMoved) != len(jb2.Result.DriverMoved) {
		t.Fatalf("driverMoved %+v, JB2 records %+v", res.DriverMoved, jb2.Result.DriverMoved)
	}
	for _, want := range jb2.Result.DriverMoved {
		i := slices.IndexFunc(res.DriverMoved, func(d tenancy.Drift) bool { return d.ID == sym(want.ID) })
		if i < 0 {
			t.Errorf("driverMoved misses %s", want.ID)
			continue
		}
		got := res.DriverMoved[i]
		if got.Table != want.Table || got.DriverID != sym(want.DriverID) || got.RowTenant != sym(want.RowTenant) ||
			got.DriverTenant == nil || *got.DriverTenant != sym(want.DriverTenant) {
			t.Errorf("driverMoved %s = %+v, JB2 records %+v", want.ID, got, want)
		}
	}
	if res.TruckTenantMismatchTotal != len(jb2.Result.TruckTenantMismatch) {
		t.Errorf("truckTenantMismatch %+v, JB2 records %d", res.TruckTenantMismatch, len(jb2.Result.TruckTenantMismatch))
	}
}
