//go:build integration

// Acceptance tests of issue T51 (#71) for the ETL step auth-rbac (Appendix C §C.2.6, §C.9.4) on postgres:18-alpine:
// every permissions_config row maps to colon keys, a row is written only where the saved boolean differs from the
// new default, unknown keys reach etl.quarantine with unknown_capability_key, the run records one
// role_matrix_saved with the diff, a re-run changes nothing and a dry run commits nothing.
package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

func permissionsDump(t *testing.T, docs ...dump.Doc) string {
	t.Helper()
	docs = slices.Clone(docs) // the writer orders them in place
	dir := t.TempDir()
	w, err := dump.NewWriter(dir, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), "logitrack-fixtures", "(default)")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCollection("permissions_config", false, docs); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func roleDoc(id string, caps map[string]any) dump.Doc {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return dump.Doc{ID: id, Path: "permissions_config/" + id, CreateTime: at, UpdateTime: at, Fields: map[string]any{"capabilities": caps}}
}

func TestAuthRBAC(t *testing.T) {
	h := newHarness(t)
	docs := []dump.Doc{
		roleDoc("manager", map[string]any{"create_truck": false, "accounting_view_fuel": true, "users_create": false,
			"users_edit": true, "users_assign_role": true, "made_up_key": true, "fleet:manage_customers": false}),
		roleDoc("operator", map[string]any{"assign_driver": true, "security_view_audit": true, "delete_records": true}),
		roleDoc("driver", map[string]any{"create_trip_record": false, "submit_delivery": false, "update_trip_record": false,
			"send_chat": true}),
		roleDoc("admin", map[string]any{"security_manage_roles": false}),
		roleDoc("partner", map[string]any{"chat_view": true, "security_view_mobile_clients": true}),
		roleDoc("customer", map[string]any{"operations_view_first_mile": true, "mystery": true}),
	}
	dir := permissionsDump(t, docs...)

	// The own fleet must exist, and the actor must be a user.
	if code, _, _ := h.run("auth-rbac", "--dump="+dir, "--actor=owner@example.test"); code != 2 {
		t.Fatalf("no own fleet: exit %d, want 2", code)
	}
	if _, err := h.pool.Exec(h.ctx, `INSERT INTO tenants (id, kind, name_th, name_en) VALUES ($1, 'own_fleet', 'O', 'O')`, ownFleet); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := h.run("auth-rbac", "--dump="+dir, "--actor=owner@example.test"); code != 2 {
		t.Fatalf("no actor row: exit %d, want 2", code)
	}
	if code, _, _ := h.run("auth-rbac", "--dump="+dir); code != 2 {
		t.Fatalf("no --actor and no PLATFORM_ADMIN_EMAILS: exit %d, want 2", code)
	}
	var owner string
	if err := h.pool.QueryRow(h.ctx, `INSERT INTO users (email, display_name) VALUES ('owner@example.test', 'Owner') RETURNING id::text`).Scan(&owner); err != nil {
		t.Fatal(err)
	}

	// A dry run writes the report and commits nothing.
	report := filepath.Join(t.TempDir(), "migration_rbac_report.csv")
	before := h.rowVersions()
	h.mustRun("auth-rbac", "--dump="+dir, "--actor=owner@example.test", "--dry-run", "--report="+report)
	if after := h.rowVersions(); len(diffVersions(before, after)) != 0 {
		t.Fatalf("the dry run changed %v", diffVersions(before, after))
	}

	h.extra = []string{"PLATFORM_ADMIN_EMAILS=owner@example.test"} // the default actor
	out := h.mustRun("auth-rbac", "--dump="+dir, "--report="+report)
	if !strings.Contains(out, "5 override row(s) changed") {
		t.Fatalf("stdout: %s", out)
	}
	got := map[string]bool{}
	rows, err := h.pool.Query(h.ctx, `SELECT role || ' ' || capability, allowed FROM role_capability_overrides WHERE tenant_id = $1
		AND updated_by = $2`, ownFleet, owner)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		var v bool
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		got[k] = v
	}
	rows.Close()
	want := map[string]bool{
		"manager fleet:create_truck":     false, // default true
		"manager users:manage":           true,  // users_create OR users_edit; manager's default lacks it
		"manager fleet:manage_customers": false, // a global key: the own fleet's row
		"operator security:view_audit":   true,
		"driver mobile:submit_trip":      false, // all three rows false; default true
	}
	if len(got) != len(want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s = %v (present %t), want %v", k, g, ok, v)
		}
	}
	// Unknown keys of every document reach etl.quarantine.
	findings := map[string]string{}
	frows, err := h.pool.Query(h.ctx, `SELECT doc_path || ' ' || field, reason_code FROM etl.quarantine
		WHERE collection = 'permissions_config' AND resolved_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	for frows.Next() {
		var k, r string
		if err := frows.Scan(&k, &r); err != nil {
			t.Fatal(err)
		}
		findings[k] = r
	}
	frows.Close()
	if len(findings) != 2 || findings["permissions_config/manager capabilities.made_up_key"] != "unknown_capability_key" ||
		findings["permissions_config/customer capabilities.mystery"] != "unknown_capability_key" {
		t.Fatalf("findings %v", findings)
	}
	if n := h.scalar(`SELECT count(*) FROM security_events WHERE event_type = 'role_matrix_saved' AND actor_user_id = $1
		AND tenant_id = $2 AND actor_email = 'owner@example.test'`, owner, ownFleet); n != int64(1) {
		t.Fatalf("%v role_matrix_saved rows (with the actor's email)", n)
	}
	// Every key of every document has a report line with its colon key or the reason it has none.
	f, err := os.Open(report)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := csv.NewReader(f).ReadAll()
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	keys := 0
	for _, d := range docs {
		keys += len(d.Fields["capabilities"].(map[string]any))
	}
	if len(lines) != keys+1 {
		t.Fatalf("%d report lines for %d keys", len(lines)-1, keys)
	}
	actions := map[string]string{}
	for _, l := range lines[1:] {
		actions[l[0]+"/"+l[2]] = l[3] + " " + l[6]
	}
	for k, v := range map[string]string{
		"manager/create_truck": "fleet:create_truck override", "manager/accounting_view_fuel": "accounting:view_fuel same_as_default",
		"manager/users_assign_role": "users:assign_role not_overridable", "manager/made_up_key": " unknown_capability_key",
		"operator/delete_records": " dropped", "operator/assign_driver": "fleet:manage_assignments same_as_default",
		"driver/send_chat": "mobile:chat same_as_default", "admin/security_manage_roles": "security:manage_roles not_overridable",
		"partner/chat_view": " dropped", "partner/security_view_mobile_clients": "security:view_mobile_clients not_overridable",
		"customer/operations_view_first_mile": "operations:view_first_mile scope_fixed",
	} {
		if actions[k] != v {
			t.Errorf("report %s: %q, want %q", k, actions[k], v)
		}
	}

	// A re-run changes nothing.
	before = h.rowVersions()
	out = h.mustRun("auth-rbac", "--dump="+dir)
	if !strings.Contains(out, "0 override row(s) changed") {
		t.Fatalf("re-run: %s", out)
	}
	if n := h.scalar(`SELECT count(*) FROM security_events WHERE event_type = 'role_matrix_saved'`); n != int64(1) {
		t.Fatalf("a re-run recorded another event: %v", n)
	}
	// The open findings are replaced by equal ones; nothing else changed.
	for table := range diffVersions(before, h.rowVersions()) {
		if table != "etl.quarantine" {
			t.Errorf("a re-run changed %s", table)
		}
	}

	// A new dump whose manager now matches the default removes that row.
	next := []dump.Doc{roleDoc("manager", map[string]any{"create_truck": true})}
	for _, d := range docs {
		if d.ID != "manager" {
			next = append(next, d)
		}
	}
	h.mustRun("auth-rbac", "--dump="+permissionsDump(t, next...))
	if n := h.scalar(`SELECT count(*) FROM role_capability_overrides WHERE role = 'manager' AND capability = 'fleet:create_truck'`); n != int64(0) {
		t.Fatalf("the manager row is still there: %v", n)
	}
}

// diffVersions lists the tables whose row fingerprints differ.
func diffVersions(a, b map[string]string) map[string]bool {
	out := map[string]bool{}
	for k, v := range a {
		if b[k] != v {
			out[k] = true
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out[k] = true
		}
	}
	return out
}

// The documented fixture mode: the committed dump carries permissions_config (manager, operator, driver, admin and
// customer documents with an override, keys equal to their default, an unknown key and a non-boolean value), so
// `auth-rbac --fixtures` plans, reports and, as a dry run, commits nothing.
func TestAuthRBACFixtures(t *testing.T) {
	h := newHarness(t)
	if _, err := h.pool.Exec(h.ctx, `INSERT INTO tenants (id, kind, name_th, name_en) VALUES ($1, 'own_fleet', 'O', 'O')`, ownFleet); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(h.ctx, `INSERT INTO users (email, display_name) VALUES ('owner@example.test', 'Owner')`); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "migration_rbac_report.csv")
	before := h.rowVersions()
	h.mustRun("auth-rbac", "--fixtures", "--actor=owner@example.test", "--dry-run", "--report="+report)
	if d := diffVersions(before, h.rowVersions()); len(d) != 0 {
		t.Fatalf("the dry run changed %v", d)
	}
	f, err := os.Open(report)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := csv.NewReader(f).ReadAll()
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]string{}
	for _, l := range lines[1:] {
		actions[l[0]+"/"+l[2]] = l[6]
	}
	want := map[string]string{
		"manager/create_truck": "override", "manager/accounting_view_fuel": "same_as_default",
		"manager/made_up_key": "unknown_capability_key", "operator/assign_driver": "same_as_default",
		"operator/security_view_audit": "not_boolean", "driver/send_chat": "same_as_default", "driver/submit_delivery": "override",
		"admin/security_manage_roles": "not_overridable", "customer/operations_view_first_mile": "scope_fixed",
	}
	if len(actions) != len(want) {
		t.Fatalf("report %v", actions)
	}
	for k, v := range want {
		if actions[k] != v {
			t.Errorf("report %s: %q, want %q", k, actions[k], v)
		}
	}
	// The real run writes the two overrides.
	out := h.mustRun("auth-rbac", "--fixtures", "--actor=owner@example.test")
	if !strings.Contains(out, "2 override row(s) changed") {
		t.Fatalf("stdout: %s", out)
	}
}
