//go:build integration

package iam_test

import (
	"net/http"
	"testing"
)

// Maintenance with an unresolved truck remains reviewable without the retired tenant override.
func TestMaintenanceQuarantineReview(t *testing.T) {
	w := newWorld(t)
	id := w.id(`INSERT INTO maintenance_records (tenant_id, tenant_source, truck_id, truck_license_plate_snapshot,
		maintenance_type, service_type, start_date, status, legacy_doc_id)
		VALUES ($1, 'quarantine', NULL, 'zz-9999', 'CM', 'เบรก', '2026-09-02', 'in_progress', 'mnt2')
		RETURNING id::text`, w.Q)
	r := w.get("/v1/tenants/quarantine/rows?table=maintenance_records", w.login("pa@example.test"))
	expect(t, r, http.StatusOK, "")
	rows := r.data().([]any)
	if len(rows) != 1 {
		t.Fatalf("quarantined maintenance: %s", r.raw)
	}
	row := rows[0].(map[string]any)
	trace := row["trace"].(map[string]any)
	steps := trace["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("maintenance truck trace: %s", r.raw)
	}
	step := steps[0].(map[string]any)
	if row["id"] != id || row["legacyDocId"] != "mnt2" || row["label"] != "เบรก" ||
		trace["outcome"] != "chain_exhausted" || step["link"] != "truck" || step["ref"] != "zz-9999" ||
		step["rowId"] != nil || step["tenantId"] != nil || step["quarantined"] != false {
		t.Fatalf("maintenance snapshot or unresolved truck link lost: %s", r.raw)
	}
	expect(t, w.get("/v1/tenants/quarantine/rows?table=maintenance_records", w.login("ta.o@example.test")),
		http.StatusForbidden, "permission_denied")
}
