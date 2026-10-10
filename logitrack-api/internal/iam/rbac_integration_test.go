//go:build integration

package iam_test

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope"
)

// Contractor reach and tenant isolation as logitrack_app (Appendix C §C.9.2 #1, #3, #4, #18; T07
// acceptance criteria 3 and 4): every read runs in db.WithPrincipal with no tenant predicate, or with a
// crafted one naming the own fleet, so RLS alone decides.
func TestTenantReach(t *testing.T) {
	w := newWorld(t)
	for _, tc := range []struct {
		email, want string
	}{
		{"ta.a@example.test", "A"},     // a carrier tenant_admin reads its own rows only
		{"op.a@example.test", "A"},     // and so does its operator
		{"ta.c@example.test", "C"},     // an independent carrier too
		{"ta.o@example.test", "A B O"}, // own-fleet staff reach the carriers that work for the own fleet
		{"mg.o@example.test", "A B O"},
	} {
		tok := w.login(tc.email)
		for _, crafted := range []string{"", "?tenant=" + w.O} {
			got := named(w, w.perTenant(tok, crafted))
			if keysOf(got) != tc.want {
				t.Errorf("%s%s reads tasks of %v, want %s", tc.email, crafted, got, tc.want)
			}
			if got["O"] > 0 && !strings.HasSuffix(tc.email, ".o@example.test") {
				t.Errorf("%s read own-fleet rows", tc.email)
			}
		}
	}
	who := w.whoami(w.login("ta.o@example.test"))
	if !who.Steward || who.TenantKind != authz.TenantKindOwnFleet || !slices.Equal(sortedCopy(who.Subtenants), sortedCopy([]string{w.A, w.B})) {
		t.Errorf("own-fleet tenant_admin: %+v, want steward of own_fleet with sub-tenants A and B", who)
	}
	if who := w.whoami(w.login("ta.a@example.test")); who.Steward || who.TenantKind != authz.TenantKindCarrier || len(who.Subtenants) != 0 {
		t.Errorf("carrier tenant_admin: %+v, want no steward flag and no sub-tenants", who)
	}
	// A bare transaction, without WithPrincipal or WithSystem, sees nothing (fails closed, #18).
	var n int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM tasks`).Scan(&n); err != nil || n != 0 {
		t.Errorf("bare logitrack_app transaction read %d tasks (err %v), want 0", n, err)
	}
}

// The steward rule (R60; acceptance criterion 5, §C.9.2 #14): a global key granted by override is still
// refused to carrier staff, by RequireCap and, below it, by RLS; own-fleet staff succeed.
func TestStewardRule(t *testing.T) {
	w := newWorld(t)
	pa := w.id(`SELECT id::text FROM users WHERE email = 'pa@example.test'`)
	for _, ov := range []struct {
		tenant *string
		role   string
	}{{&w.A, "tenant_admin"}, {&w.A, "manager"}, {nil, "operator"}} {
		w.exec(`INSERT INTO role_capability_overrides (tenant_id, role, capability, allowed, updated_by)
			VALUES ($1, $2, 'fleet:manage_customers', true, $3)`, ov.tenant, ov.role, pa)
	}
	if _, err := w.rbac.BumpVersion(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"ta.a@example.test", "mg.a@example.test", "op.a@example.test"} {
		tok := w.login(email)
		r := w.post("/v1/rbactest/customers?code=BAD"+email[:2], tok)
		expect(t, r, http.StatusForbidden, authz.CodePermissionDenied)
		details, _ := r.body["error"].(map[string]any)["details"].(map[string]any)
		if missing, _ := details["missingCapability"].([]any); len(missing) != 1 || missing[0] != string(authz.FleetManageCustomers) {
			t.Errorf("%s: details %v", email, details)
		}
		if slices.Contains(w.whoami(tok).Caps, string(authz.FleetManageCustomers)) {
			t.Errorf("%s holds fleet:manage_customers", email)
		}
		// Below the capability layer RLS refuses the write as well (p_steward_write).
		r = w.post("/v1/rbactest/customers-unguarded?code=RLS"+email[:2], tok)
		expect(t, r, http.StatusForbidden, authz.CodePermissionDenied)
		if d, _ := r.body["error"].(map[string]any)["details"].(map[string]any); d["sqlstate"] != "42501" {
			t.Errorf("%s: unguarded customer write refused by %v, want RLS (42501)", email, d)
		}
	}
	if n := w.count(`SELECT count(*) FROM customers WHERE code LIKE 'BAD%' OR code LIKE 'RLS%'`); n != 0 {
		t.Fatalf("carrier staff wrote %d customers", n)
	}
	// Own-fleet staff: tenant_admin and manager by default, operator through the platform-wide override.
	for _, email := range []string{"ta.o@example.test", "mg.o@example.test", "op.o@example.test"} {
		expect(t, w.post("/v1/rbactest/customers?code=OK"+email[:2], w.login(email)), http.StatusCreated, "")
	}
	if n := w.count(`SELECT count(*) FROM customers WHERE code LIKE 'OK%'`); n != 3 {
		t.Fatalf("own-fleet staff wrote %d customers, want 3", n)
	}

	// PUBLIC holidays (tenant_id NULL) are steward rows too (C.9.2 #15): RequireSteward, then RLS p_write.
	taA := w.login("ta.a@example.test")
	expect(t, w.post("/v1/rbactest/public-holiday?date=2026-12-05", taA), http.StatusForbidden, authz.CodePermissionDenied)
	r := w.post("/v1/rbactest/public-holiday-unguarded?date=2026-12-05", taA)
	if d, _ := r.body["error"].(map[string]any)["details"].(map[string]any); r.status != http.StatusForbidden || d["sqlstate"] != "42501" {
		t.Errorf("carrier tenant_admin writing a PUBLIC holiday: %d %s, want an RLS refusal", r.status, r.raw)
	}
	expect(t, w.post("/v1/rbactest/public-holiday?date=2026-12-05", w.login("ta.o@example.test")), http.StatusCreated, "")
	if n := w.count(`SELECT count(*) FROM holidays WHERE tenant_id IS NULL`); n != 1 {
		t.Fatalf("%d public holidays, want the own fleet's one", n)
	}
}

// Dispatcher and customer-scope principals read the scope_* projection only (acceptance criterion 6,
// §C.9.2 #9-#11): the rows of their parties across tenants, never quarantine, no billing / HR column in
// the response, and no carrier-internal row at all.
func TestScopePrincipals(t *testing.T) {
	w := newWorld(t)
	scopeTasks := func(tok string) (labels []string, keys map[string]bool) {
		r := w.get("/v1/rbactest/scope/tasks", tok)
		if r.status != http.StatusOK {
			t.Fatalf("scope tasks: %d %s", r.status, r.raw)
		}
		keys = map[string]bool{}
		for _, row := range r.data().([]any) {
			m := row.(map[string]any)
			for k := range m {
				keys[k] = true
			}
			for label, id := range w.tasks {
				if m["id"] == id {
					labels = append(labels, label)
				}
			}
		}
		slices.Sort(labels)
		return labels, keys
	}
	ds := w.login("ds@example.test")
	got, keys := scopeTasks(ds)
	// X work of every tenant but quarantine, plus every task of the dispatcher's own organisation D.
	if want := []string{"Ax", "Bx", "Cx", "Dx", "Dy", "Ox"}; !slices.Equal(got, want) {
		t.Errorf("dispatcher reads %v, want %v", got, want)
	}
	cu := w.login("cu@example.test")
	if got, ckeys := scopeTasks(cu); !slices.Equal(got, []string{"Ax", "Bx", "Cx", "Dx", "Ox"}) {
		t.Errorf("customer X reads %v", got)
	} else {
		maps.Copy(keys, ckeys)
	}
	if len(keys) == 0 {
		t.Fatal("no response keys to check")
	}
	for k := range keys {
		if scope.IsHidden(snake(k)) {
			t.Errorf("a scope response carries %s", k)
		}
	}
	for _, k := range []string{"billingPartyId", "sourceLinkedPartyId", "destinationLinkedPartyId", "createdBy"} {
		if keys[k] {
			t.Errorf("a scope response carries %s", k)
		}
	}
	for _, tok := range []string{ds, cu} {
		for _, table := range []string{"customer_service_fees", "driver_penalties"} {
			r := w.get("/v1/rbactest/count/"+table, tok)
			if n, _ := r.data().(map[string]any)["count"].(float64); r.status != http.StatusOK || n != 0 {
				t.Errorf("a scope principal reads %v rows of %s (%d)", r.data(), table, r.status)
			}
		}
	}
	who := w.whoami(ds)
	for _, k := range []authz.Cap{authz.DispatchViewOperations, authz.FleetViewLiveMap, authz.OperationsViewFirstMile} {
		if !slices.Contains(who.Caps, string(k)) {
			t.Errorf("dispatcher lacks %s", k)
		}
	}
	if who := w.whoami(cu); !slices.Equal(who.Caps, authz.ScopeDefaults(authz.ScopeCustomer).Strings()) {
		t.Errorf("customer scope holds %v", who.Caps)
	}
}

// X-Act-On-Tenant (acceptance criterion 7, §C.3.9, §C.9.2 #16, #17).
func TestActOnTenant(t *testing.T) {
	w := newWorld(t)
	audit := func() int { return w.securityEvents(iam.EventCrossTenantAccess) }
	const hdr = authz.HeaderActOnTenant

	// Anyone but a platform principal: 403, and no audit row (no access was granted).
	opA := w.login("op.a@example.test")
	expect(t, w.get("/v1/rbactest/whoami", opA, hdr, w.A), http.StatusForbidden, authz.CodePermissionDenied)
	expect(t, w.get("/v1/rbactest/whoami", opA, hdr, "*"), http.StatusForbidden, authz.CodePermissionDenied)
	if n := audit(); n != 0 {
		t.Fatalf("%d audit rows for a refused non-platform principal", n)
	}

	pa := w.login("pa@example.test")
	// <uuid>: platform_admin acts as tenant_admin of A; exactly one committed row per request.
	for i := 1; i <= 2; i++ {
		who := w.whoami(pa, hdr, w.A)
		if who.ActOnTenant != w.A || !who.Steward || !slices.Contains(who.Caps, string(authz.HRManagePayroll)) {
			t.Fatalf("acting as A: %+v", who)
		}
		if n := audit(); n != i {
			t.Fatalf("after %d requests %d audit rows", i, n)
		}
	}
	var tenant, actor, method, path string
	if err := w.etl.QueryRow(context.Background(), `SELECT tenant_id::text, actor_user_id::text, details->>'method',
		details->>'path' FROM security_events WHERE event_type = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
		iam.EventCrossTenantAccess).Scan(&tenant, &actor, &method, &path); err != nil {
		t.Fatal(err)
	}
	if tenant != w.A || actor != w.id(`SELECT id::text FROM users WHERE email = 'pa@example.test'`) ||
		method != http.MethodGet || path != "/v1/rbactest/whoami" {
		t.Errorf("audit row tenant %s actor %s %s %s", tenant, actor, method, path)
	}
	if got := keysOf(named(w, w.perTenant(pa, "", hdr, w.A))); got != "A" {
		t.Errorf("acting as A reads %s", got)
	}
	if got := keysOf(named(w, w.perTenant(pa, "", hdr, w.O))); got != "A B O" {
		t.Errorf("acting as the own fleet reads %s (contractor reach of the target)", got)
	}
	expect(t, w.post("/v1/rbactest/tasks", pa, hdr, w.A), http.StatusNoContent, "") // platform:cross_tenant_write
	if got := keysOf(named(w, w.perTenant(pa, ""))); got != "" {
		t.Errorf("platform_admin without the header reads %s", got)
	}

	// *: read-only bypass, every tenant including quarantine; writes refused, and the attempt is on record.
	before := audit()
	r := w.get("/v1/rbactest/tasks?write=1", pa, hdr, "*")
	if r.status != http.StatusOK {
		t.Fatalf("GET with *: %d %s", r.status, r.raw)
	}
	data := r.data().(map[string]any)
	if got := keysOf(named(w, toInts(data["perTenant"]))); got != "A B C D O Q" {
		t.Errorf("* reads %s", got)
	}
	if data["writeSQLState"] != "25006" {
		t.Errorf("a write inside the * transaction: sqlstate %v, want 25006 read_only_sql_transaction", data["writeSQLState"])
	}
	expect(t, w.post("/v1/rbactest/tasks", pa, hdr, "*"), http.StatusForbidden, authz.CodePermissionDenied)
	if n := audit(); n != before+2 {
		t.Errorf("audit rows %d, want %d (the refused write is recorded too)", n, before+2)
	}
	var nullTenant bool
	if err := w.etl.QueryRow(context.Background(), `SELECT tenant_id IS NULL FROM security_events WHERE event_type = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`, iam.EventCrossTenantAccess).Scan(&nullTenant); err != nil || !nullTenant {
		t.Errorf("the * audit row names a tenant (%v)", err)
	}

	// support: reads with * only.
	su := w.login("su@example.test")
	if got := keysOf(named(w, w.perTenant(su, "", hdr, "*"))); got != "A B C D O Q" {
		t.Errorf("support with * reads %s", got)
	}
	before = audit()
	expect(t, w.get("/v1/rbactest/whoami", su, hdr, w.A), http.StatusForbidden, authz.CodePermissionDenied)
	expect(t, w.post("/v1/rbactest/tasks", su, hdr, "*"), http.StatusForbidden, authz.CodePermissionDenied)
	if n := audit(); n != before+2 {
		t.Errorf("support's refused attempts: %d audit rows, want 2", n-before)
	}

	// Malformed, repeated, unknown; and the public listener.
	expect(t, w.get("/v1/rbactest/whoami", pa, hdr, "not-a-uuid"), http.StatusBadRequest, "bad_request")
	expect(t, w.get("/v1/rbactest/whoami", pa, hdr, w.A, hdr, w.B), http.StatusBadRequest, "bad_request")
	expect(t, w.get("/v1/rbactest/whoami", pa, hdr, "0199c000-0000-7000-8000-0000000000ff"), http.StatusNotFound, "not_found")
	expect(t, w.call(w.public, http.MethodPost, "/v1/auth/logout-all", pa, hdr, w.A), http.StatusBadRequest, "header_not_allowed")
}

// A role-matrix change is effective on the next request after INCR rbac:ver (acceptance criterion 8,
// Appendix C §C.2.5): the version is part of the rbac:caps key.
func TestOverrideEffectiveOnNextRequest(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	op := w.login("op.o@example.test")
	expect(t, w.get("/v1/rbactest/rate-card", op), http.StatusForbidden, authz.CodePermissionDenied)
	pa := w.id(`SELECT id::text FROM users WHERE email = 'pa@example.test'`)
	w.exec(`INSERT INTO role_capability_overrides (tenant_id, role, capability, allowed, updated_by)
		VALUES ($1, 'operator', 'accounting:view_rate_card', true, $2)`, w.O, pa)
	// Until the version moves the cached set of this version still answers (the writer bumps it after commit).
	expect(t, w.get("/v1/rbactest/rate-card", op), http.StatusForbidden, authz.CodePermissionDenied)
	v1, err := w.rbac.BumpVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, w.get("/v1/rbactest/rate-card", op), http.StatusNoContent, "")
	w.exec(`UPDATE role_capability_overrides SET allowed = false WHERE capability = 'accounting:view_rate_card'`)
	if v2, err := w.rbac.BumpVersion(ctx); err != nil || v2 != v1+1 {
		t.Fatalf("version %d -> %d (%v)", v1, v2, err)
	}
	expect(t, w.get("/v1/rbactest/rate-card", op), http.StatusForbidden, authz.CodePermissionDenied)
	// The override of the own fleet leaves other tenants alone.
	expect(t, w.get("/v1/rbactest/rate-card", w.login("op.a@example.test")), http.StatusForbidden, authz.CodePermissionDenied)
	// Revoking a default key works the same way.
	mg := w.login("mg.o@example.test")
	expect(t, w.get("/v1/rbactest/rate-card", mg), http.StatusNoContent, "")
	w.exec(`INSERT INTO role_capability_overrides (tenant_id, role, capability, allowed, updated_by)
		VALUES (NULL, 'manager', 'accounting:view_rate_card', false, $1)`, pa)
	if _, err := w.rbac.BumpVersion(ctx); err != nil {
		t.Fatal(err)
	}
	expect(t, w.get("/v1/rbactest/rate-card", mg), http.StatusForbidden, authz.CodePermissionDenied)
}

// GET /v1/me lists the resolved capabilities; GET /v1/roles serves the catalog.
func TestMeAndRoles(t *testing.T) {
	w := newWorld(t)
	capsOf := func(email string) []string {
		r := w.get("/v1/me", w.login(email))
		if r.status != http.StatusOK {
			t.Fatalf("me: %d %s", r.status, r.raw)
		}
		var out []string
		for _, v := range r.data().(map[string]any)["capabilities"].([]any) {
			out = append(out, v.(string))
		}
		return out
	}
	if got, want := capsOf("mg.o@example.test"), authz.TenantDefaults(authz.Manager).Strings(); !slices.Equal(got, want) {
		t.Errorf("own-fleet manager: %d keys, want the 45 defaults", len(got))
	}
	if got := capsOf("mg.a@example.test"); len(got) != 41 || slices.Contains(got, string(authz.FleetManageCustomers)) {
		t.Errorf("carrier manager: %d keys, want 45 minus the 4 global keys a manager holds", len(got))
	}
	if got := capsOf("pa@example.test"); !slices.Equal(got, authz.PlatformDefaults(authz.PlatformAdmin).Strings()) {
		t.Errorf("platform_admin: %v", got)
	}
	r := w.get("/v1/roles", w.login("op.a@example.test"))
	if r.status != http.StatusOK {
		t.Fatalf("roles: %d %s", r.status, r.raw)
	}
	body := r.data().(map[string]any)
	if n := len(body["capabilities"].([]any)); n != 81 {
		t.Errorf("GET /v1/roles lists %d keys", n)
	}
	if n := len(body["roles"].([]any)); n != 10 {
		t.Errorf("GET /v1/roles lists %d roles", n)
	}
	expect(t, w.get("/v1/roles", ""), http.StatusUnauthorized, "unauthenticated")
	expect(t, w.call(w.public, http.MethodGet, "/v1/roles", w.login("op.a@example.test")), http.StatusNotFound, "not_found")
}

func toInts(v any) map[string]int {
	out := map[string]int{}
	for k, n := range v.(map[string]any) {
		out[k] = int(n.(float64))
	}
	return out
}

func sortedCopy(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

// snake turns a camelCase JSON key back into its column name.
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
