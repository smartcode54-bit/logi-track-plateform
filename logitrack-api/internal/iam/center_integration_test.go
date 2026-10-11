//go:build integration

// Acceptance tests of issue T51 (#71), pulled forward from M6 at the owner's request (2026-10-11): the role matrix
// over role_capability_overrides (one call with default / effective / overridden per key; a save writes
// role_matrix_saved with the full diff, bumps rbac:ver and emits roles.changed; tenant callers cannot override
// platform:*, dispatch:* or users:assign_role), the security events feed (legacy types plus
// platform_cross_tenant_access and refresh_token_reuse, paged by cursor, RLS deciding the rows), the overview and
// status counters, users by role, and the soft delete of a user. Requests run as logitrack_app through the real
// auth service and RBAC authorizer (harness_integration_test.go).
package iam_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

func decodeData[T any](t *testing.T, r resp) T {
	t.Helper()
	var out T
	b, _ := json.Marshal(r.data())
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode %s: %v", r.raw, err)
	}
	return out
}

func (w *world) rbacVersion() int64 {
	w.t.Helper()
	v, err := w.rdb.Get(context.Background(), w.ks.RBACVersion()).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		w.t.Fatal(err)
	}
	return v
}

func cellOf(t *testing.T, m iam.Matrix, role, key string) iam.MatrixCell {
	t.Helper()
	for _, r := range m.Roles {
		if r.Role == role {
			c, ok := r.Capabilities[key]
			if !ok {
				t.Fatalf("%s has no %s", role, key)
			}
			return c
		}
	}
	t.Fatalf("no role %s", role)
	return iam.MatrixCell{}
}

func overrides(rows ...[3]any) map[string]any {
	list := make([]map[string]any, len(rows))
	for i, r := range rows {
		list[i] = map[string]any{"role": r[0], "capability": r[1], "allowed": r[2]}
	}
	return map[string]any{"overrides": list}
}

// lastEventDetails is the details object of the newest security_events row of eventType.
func (w *world) lastEventDetails(eventType string) (map[string]any, string) {
	w.t.Helper()
	var raw []byte
	var tenant *string
	if err := w.etl.QueryRow(context.Background(), `SELECT details, tenant_id::text FROM security_events WHERE event_type = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`, eventType).Scan(&raw, &tenant); err != nil {
		w.t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		w.t.Fatal(err)
	}
	if tenant == nil {
		return d, ""
	}
	return d, *tenant
}

func (w *world) rolesChanged(topic string) int {
	w.t.Helper()
	return w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'roles.changed' AND $1 = ANY (realtime_topics)`, topic)
}

// AC 1 and 2 for a tenant: one call with default / effective / overridden per key; a save writes exactly one
// role_matrix_saved with the full diff, bumps rbac:ver and emits roles.changed on tenant:{tid}:config, and takes
// effect on the next request; platform:*, dispatch:*, users:assign_role, global keys outside the own fleet and
// tenant_admin are refused with 422 capability_not_overridable and write nothing.
func TestRoleMatrixTenant(t *testing.T) {
	w := newWorld(t)
	taA, mgA, opA := w.login("ta.a@example.test"), w.login("mg.a@example.test"), w.login("op.a@example.test")
	if !slices.Contains(w.whoami(mgA).Caps, "fleet:create_truck") {
		t.Fatal("manager holds fleet:create_truck by default")
	}

	r := w.get("/v1/roles/matrix", taA)
	expect(t, r, http.StatusOK, "")
	m := decodeData[iam.Matrix](t, r)
	if m.Layer != iam.LayerTenant || m.TenantID == nil || m.TenantID.String() != w.A || m.TenantKind == nil ||
		*m.TenantKind != "carrier" || len(m.Roles) != 6 || len(m.Roles[1].Capabilities) != 76 {
		t.Fatalf("matrix of carrier A: %s", r.raw)
	}
	if c := cellOf(t, m, "manager", "fleet:create_truck"); !c.Default || !c.Effective || c.Overridden || !c.Overridable {
		t.Fatalf("manager fleet:create_truck before the save: %+v", c)
	}

	ver := w.rbacVersion()
	body := overrides([3]any{"manager", "fleet:create_truck", false}, [3]any{"operator", "fleet:view_trucks", true})
	r = w.oneEvent("role_matrix_saved", func() resp { return w.send(http.MethodPut, "/v1/roles/matrix", taA, body) }, http.StatusOK)
	if meta, _ := r.body["meta"].(map[string]any); meta["changes"] != float64(1) {
		t.Fatalf("one row changes (operator fleet:view_trucks equals its default): %s", r.raw)
	}
	m = decodeData[iam.Matrix](t, r)
	if c := cellOf(t, m, "manager", "fleet:create_truck"); !c.Default || c.Effective || !c.Overridden {
		t.Fatalf("manager fleet:create_truck after the save: %+v", c)
	}
	if n := w.count(`SELECT count(*) FROM role_capability_overrides WHERE tenant_id = $1`, w.A); n != 1 {
		t.Fatalf("%d override rows in A", n)
	}
	d, tenant := w.lastEventDetails("role_matrix_saved")
	changes, _ := d["changes"].([]any)
	if tenant != w.A || d["layer"] != "tenant" || len(changes) != 1 {
		t.Fatalf("role_matrix_saved: tenant %s details %v", tenant, d)
	}
	if ch := changes[0].(map[string]any); ch["role"] != "manager" || ch["capability"] != "fleet:create_truck" || ch["from"] != nil || ch["to"] != false {
		t.Fatalf("diff %v", ch)
	}
	if got := w.rbacVersion(); got != ver+1 {
		t.Fatalf("rbac:ver %d -> %d, want one INCR", ver, got)
	}
	if w.rolesChanged("tenant:"+w.A+":config") != 1 || w.rolesChanged("tenant:"+w.O+":config") != 0 {
		t.Fatal("roles.changed goes to tenant:{A}:config only")
	}
	if slices.Contains(w.whoami(mgA).Caps, "fleet:create_truck") {
		t.Fatal("the save is effective on the next request of the same token")
	}

	// The same body again changes nothing: no event, no INCR.
	r = w.noEvent(func() resp { return w.send(http.MethodPut, "/v1/roles/matrix", taA, body) })
	expect(t, r, http.StatusOK, "")
	if meta, _ := r.body["meta"].(map[string]any); meta["changes"] != float64(0) || w.rbacVersion() != ver+1 {
		t.Fatalf("an unchanged save: %s", r.raw)
	}

	// AC 2: tenant callers cannot override platform:*, dispatch:* or users:assign_role; nor a global key outside the
	// own fleet, nor tenant_admin.
	for _, c := range []struct{ role, key, reason string }{
		{"manager", "platform:manage_tenants", iam.ReasonNotGrantable},
		{"operator", "platform:cross_tenant_write", iam.ReasonNotGrantable},
		{"manager", "dispatch:view_operations", iam.ReasonNotGrantable},
		{"manager", "users:assign_role", iam.ReasonPlatformWideOnly},
		{"manager", "fleet:manage_customers", iam.ReasonGlobalOwnFleet},
		{"tenant_admin", "fleet:view_trucks", iam.ReasonTenantAdminFixed},
		{"driver", "fleet:view_trucks", iam.ReasonRoleClass},
	} {
		r := w.noEvent(func() resp {
			return w.send(http.MethodPut, "/v1/roles/matrix", taA, overrides([3]any{c.role, c.key, true}))
		})
		expect(t, r, http.StatusUnprocessableEntity, iam.CodeCapabilityNotOverridable)
		e := r.body["error"].(map[string]any)["details"].(map[string]any)["overrides"].([]any)[0].(map[string]any)
		if e["reason"] != c.reason || e["capability"] != c.key {
			t.Errorf("%s %s: %v", c.role, c.key, e)
		}
	}
	expect(t, w.send(http.MethodPut, "/v1/roles/matrix", taA, overrides([3]any{"manager", "fleet:nope", true})),
		http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.send(http.MethodPut, "/v1/roles/matrix", taA, map[string]any{}), http.StatusUnprocessableEntity, "invalid_argument")
	// Another tenant is for platform admins; the role matrix needs security:manage_roles.
	r = w.send(http.MethodPut, "/v1/roles/matrix?tenantId="+w.C, taA, overrides())
	expect(t, r, http.StatusForbidden, "permission_denied")
	if reason(r) != "platform_only" {
		t.Fatalf("reason %s", r.raw)
	}
	expect(t, w.get("/v1/roles/matrix", opA), http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/roles/matrix?bogus=1", taA), http.StatusBadRequest, "bad_request")

	// The own fleet may override a global key.
	taO := w.login("ta.o@example.test")
	w.oneEvent("role_matrix_saved", func() resp {
		return w.send(http.MethodPut, "/v1/roles/matrix", taO, overrides([3]any{"manager", "fleet:manage_customers", false}))
	}, http.StatusOK)
	if slices.Contains(w.whoami(w.login("mg.o@example.test")).Caps, "fleet:manage_customers") {
		t.Fatal("own-fleet manager still holds fleet:manage_customers")
	}

	// PUT replaces the layer: [] removes A's row, the diff records it and the manager has the key again.
	w.oneEvent("role_matrix_saved", func() resp { return w.send(http.MethodPut, "/v1/roles/matrix", taA, overrides()) }, http.StatusOK)
	d, _ = w.lastEventDetails("role_matrix_saved")
	if ch := d["changes"].([]any)[0].(map[string]any); ch["from"] != false || ch["to"] != nil || d["removed"] != float64(1) {
		t.Fatalf("reset diff %v", d)
	}
	if !slices.Contains(w.whoami(mgA).Caps, "fleet:create_truck") {
		t.Fatal("the reset is effective")
	}
}

// The platform-wide layer (tenant_id NULL, platform_admin only): users:assign_role is overridable there, a save
// reaches every tenant (roles.changed on each non-quarantine tenant:{tid}:config) and a platform admin edits any
// tenant's layer with ?tenantId or through X-Act-On-Tenant.
func TestRoleMatrixPlatform(t *testing.T) {
	w := newWorld(t)
	pa, su, mgA := w.login("pa@example.test"), w.login("su@example.test"), w.login("mg.a@example.test")

	r := w.get("/v1/roles/matrix", pa)
	expect(t, r, http.StatusOK, "")
	if m := decodeData[iam.Matrix](t, r); m.Layer != iam.LayerPlatform || m.TenantID != nil {
		t.Fatalf("a platform admin without a tenant reads the platform-wide layer: %s", r.raw)
	}
	w.oneEvent("role_matrix_saved", func() resp {
		return w.send(http.MethodPut, "/v1/roles/matrix", pa, overrides([3]any{"manager", "users:assign_role", true}))
	}, http.StatusOK)
	if n := w.count(`SELECT count(*) FROM role_capability_overrides WHERE tenant_id IS NULL AND capability = 'users:assign_role'`); n != 1 {
		t.Fatalf("%d platform-wide rows", n)
	}
	if _, tenant := w.lastEventDetails("role_matrix_saved"); tenant != "" {
		t.Fatalf("a platform-wide save is a platform-level event, got tenant %s", tenant)
	}
	for _, tid := range []string{w.O, w.A, w.B, w.C, w.D} {
		if w.rolesChanged("tenant:"+tid+":config") != 1 {
			t.Errorf("roles.changed missing on tenant %s", name(w, tid))
		}
	}
	if w.rolesChanged("tenant:"+w.Q+":config") != 0 {
		t.Error("the quarantine tenant has no members to notify")
	}
	if !slices.Contains(w.whoami(mgA).Caps, "users:assign_role") {
		t.Fatal("the platform-wide grant reaches the carrier's manager")
	}
	// A tenant inherits it: the cell shows the platform-wide row.
	taA := w.login("ta.a@example.test")
	if c := cellOf(t, decodeData[iam.Matrix](t, w.get("/v1/roles/matrix", taA)), "manager", "users:assign_role"); !c.Effective ||
		c.Overridable || c.PlatformOverride == nil || !*c.PlatformOverride {
		t.Fatalf("inherited cell %+v", c)
	}

	// ?tenantId and X-Act-On-Tenant name a tenant layer.
	w.oneEvent("role_matrix_saved", func() resp {
		return w.send(http.MethodPut, "/v1/roles/matrix?tenantId="+w.C, pa, overrides([3]any{"operator", "fleet:create_truck", true}))
	}, http.StatusOK)
	if n := w.count(`SELECT count(*) FROM role_capability_overrides WHERE tenant_id = $1`, w.C); n != 1 {
		t.Fatalf("%d rows in C", n)
	}
	r = w.get("/v1/roles/matrix", pa, "X-Act-On-Tenant", w.C)
	if m := decodeData[iam.Matrix](t, r); m.TenantID == nil || m.TenantID.String() != w.C ||
		!cellOf(t, m, "operator", "fleet:create_truck").Overridden {
		t.Fatalf("matrix through X-Act-On-Tenant: %s", r.raw)
	}
	expect(t, w.get("/v1/roles/matrix?tenantId="+w.Q, pa), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/roles/matrix?tenantId=nope", pa), http.StatusUnprocessableEntity, "invalid_argument")
	// support holds no security:manage_roles.
	expect(t, w.get("/v1/roles/matrix", su, "X-Act-On-Tenant", "*"), http.StatusForbidden, "permission_denied")

	// A platform admin who is also a member reaches its tenant by default and the platform-wide layer with
	// ?layer=platform; nobody else does.
	w.member(w.userID("pa@example.test"), w.O, "manager")
	pa = w.login("pa@example.test")
	if m := decodeData[iam.Matrix](t, w.get("/v1/roles/matrix", pa)); m.Layer != iam.LayerTenant || m.TenantID.String() != w.O {
		t.Fatalf("a member platform admin's default layer: %+v", m.TenantID)
	}
	r = w.get("/v1/roles/matrix?layer=platform", pa)
	if m := decodeData[iam.Matrix](t, r); m.Layer != iam.LayerPlatform || !cellOf(t, m, "manager", "users:assign_role").Overridden {
		t.Fatalf("?layer=platform: %s", r.raw)
	}
	r = w.send(http.MethodPut, "/v1/roles/matrix?layer=platform", taA, overrides())
	expect(t, r, http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/roles/matrix?layer=platform&tenantId="+w.A, pa), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/roles/matrix?layer=world", pa), http.StatusUnprocessableEntity, "invalid_argument")
}

// AC 3: the feed returns the legacy types plus platform_cross_tenant_access and refresh_token_reuse, newest first,
// paged by cursor; a tenant sees only its tenant's rows (RLS), a platform principal everything through *.
func TestSecurityEventsFeed(t *testing.T) {
	w := newWorld(t)
	base := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	ins := func(typ, sev string, tenant *string, minutes int, legacy string) {
		var leg *string
		if legacy != "" {
			leg = &legacy
		}
		w.exec(`INSERT INTO security_events (event_type, severity, summary, details, tenant_id, created_at, legacy_doc_id)
			VALUES ($1, $2, $1, '{"k":1}', $3, $4, $5)`, typ, sev, tenant, base.Add(time.Duration(minutes)*time.Minute), leg)
	}
	ins("role_matrix_saved", "info", &w.A, 1, "legacyA1") // the four legacy Firestore types (ETL rows)
	ins("user_role_changed", "info", &w.A, 2, "legacyA2")
	ins("user_sessions_revoked", "warning", &w.A, 3, "legacyA3")
	ins("user_created", "info", &w.O, 4, "legacyO1")
	ins("refresh_token_reuse", "critical", nil, 5, "")
	ins("login_failed", "info", nil, 6, "")

	taA := w.login("ta.a@example.test")
	r := w.get("/v1/security-events", taA)
	expect(t, r, http.StatusOK, "")
	rows := decodeData[[]security.Row](t, r)
	if len(rows) != 3 || rows[0].Type != "user_sessions_revoked" || rows[2].Type != "role_matrix_saved" {
		t.Fatalf("tenant A sees its three rows, newest first: %s", r.raw)
	}
	meta, _ := r.body["meta"].(map[string]any)
	types, _ := meta["types"].([]any)
	if !slices.Contains(types, any("platform_cross_tenant_access")) || !slices.Contains(types, any("refresh_token_reuse")) ||
		!slices.Contains(types, any("role_matrix_saved")) {
		t.Fatalf("meta.types %v", types)
	}
	// Keyset paging.
	r = w.get("/v1/security-events?limit=2", taA)
	page1, next := decodeData[[]security.Row](t, r), r.body["nextCursor"]
	if len(page1) != 2 || next == nil {
		t.Fatalf("page 1: %s", r.raw)
	}
	r = w.get("/v1/security-events?limit=2&cursor="+url.QueryEscape(next.(string)), taA)
	if page2 := decodeData[[]security.Row](t, r); len(page2) != 1 || page2[0].Type != "role_matrix_saved" || r.body["nextCursor"] != nil {
		t.Fatalf("page 2: %s", r.raw)
	}
	// Filters.
	if rows := decodeData[[]security.Row](t, w.get("/v1/security-events?type=user_role_changed,role_matrix_saved", taA)); len(rows) != 2 {
		t.Fatalf("type filter: %d rows", len(rows))
	}
	if rows := decodeData[[]security.Row](t, w.get("/v1/security-events?severity=warning", taA)); len(rows) != 1 {
		t.Fatalf("severity filter: %d rows", len(rows))
	}
	from, to := base.Add(90*time.Second).Format(time.RFC3339), base.Add(150*time.Second).Format(time.RFC3339)
	if rows := decodeData[[]security.Row](t, w.get("/v1/security-events?from="+from+"&to="+to, taA)); len(rows) != 1 || rows[0].Type != "user_role_changed" {
		t.Fatalf("range filter: %+v", rows)
	}
	for _, q := range []string{"type=Bad", "severity=error", "from=yesterday", "from=" + to + "&to=" + from, "limit=501", "cursor=xx"} {
		expect(t, w.get("/v1/security-events?"+q, taA), http.StatusUnprocessableEntity, "invalid_argument")
	}
	expect(t, w.get("/v1/security-events?q=x", taA), http.StatusBadRequest, "bad_request")
	expect(t, w.get("/v1/security-events", w.login("mg.a@example.test")), http.StatusForbidden, "permission_denied")

	// Platform: a tenant context is required; * reads every row, the platform-level ones included.
	pa := w.login("pa@example.test")
	expect(t, w.get("/v1/security-events", pa), http.StatusForbidden, "tenant_required")
	r = w.get("/v1/security-events?limit=500", pa, "X-Act-On-Tenant", "*")
	expect(t, r, http.StatusOK, "")
	seen := map[string]bool{}
	for _, row := range decodeData[[]security.Row](t, r) {
		seen[row.Type] = true
	}
	for _, want := range []string{"role_matrix_saved", "user_role_changed", "user_created", "user_sessions_revoked",
		"refresh_token_reuse", "login_failed", "platform_cross_tenant_access"} {
		if !seen[want] {
			t.Errorf("X-Act-On-Tenant: * does not list %s", want)
		}
	}
	// support reads the audit too (read-only platform role).
	expect(t, w.get("/v1/security-events", w.login("su@example.test"), "X-Act-On-Tenant", "*"), http.StatusOK, "")
}

// The overview counters and the status page section, per reach; users by role.
func TestSecurityOverview(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	now := time.Now()
	opA, opO := w.userID("op.a@example.test"), w.userID("op.o@example.test")
	w.exec(`INSERT INTO security_events (event_type, severity, summary, target_user_id, created_at) VALUES
		('login_failed', 'info', 'f', $1, $3), ('login_failed', 'info', 'f', $2, $3), ('login_lockout', 'warning', 'l', $1, $3),
		('login_failed', 'info', 'f', NULL, $3), ('login_failed', 'info', 'old', $1, $4)`, opA, opO, now.Add(-time.Hour), now.Add(-48*time.Hour))
	w.exec(`INSERT INTO security_events (event_type, severity, summary, tenant_id, created_at) VALUES
		('user_created', 'info', 'c', $1, $2), ('user_disabled', 'warning', 'd', $1, $3)`, w.A, now.Add(-30*time.Minute), now.Add(-50*time.Hour))
	drv := w.id(`SELECT id::text FROM drivers WHERE tenant_id = $1 LIMIT 1`, w.A)
	w.exec(`INSERT INTO mobile_installations (driver_id, install_id, tenant_id, platform, app_version, last_seen_at) VALUES
		($1, 'i1', $2, 'android', '1.9.9', now()), ($1, 'i2', $2, 'android', '3.2.0', now() - interval '30 days'),
		($1, 'i3', $2, 'android', NULL, now())`, drv, w.A)
	w.exec(`INSERT INTO settings (key, value) VALUES ('mobile_app', '{"minAllowedVersion": "2.0.0", "latestVersion": "3.2.0"}')`)

	taA := w.login("ta.a@example.test")
	r := w.get("/v1/security/overview", taA)
	expect(t, r, http.StatusOK, "")
	o := decodeData[iam.Overview](t, r)
	if o.Scope.All || o.Scope.TenantID == nil || *o.Scope.TenantID != w.A {
		t.Fatalf("scope %+v", o.Scope)
	}
	if o.Users.Total != 3 || o.Users.ByStatus["active"] != 3 || o.Users.ByStatus["deleted"] != 0 {
		t.Errorf("users of A: %+v", o.Users)
	}
	if o.Sessions.Active != 1 { // ta.a's own login
		t.Errorf("sessions %+v", o.Sessions)
	}
	if o.Logins.Failed24h != 1 || o.Logins.Lockouts24h != 1 {
		t.Errorf("logins of A's users in 24 h: %+v", o.Logins)
	}
	if o.Events.Last24h != 1 || o.Events.Last7d != 2 || o.Events.Hourly24h[23] != 1 || o.Events.Daily7d[4] != 1 {
		t.Errorf("events of A: %+v", o.Events)
	}
	if o.Installations == nil || o.Installations.Total != 3 || o.Installations.Seen7d != 2 || o.Installations.BelowFloor != 1 ||
		o.Installations.BelowLatest != 1 || o.Installations.UnknownVersion != 1 || *o.Installations.MinAllowedVersion != "2.0.0" {
		t.Errorf("installations %+v", o.Installations)
	}
	if o.System != nil || o.Quarantine != nil {
		t.Errorf("a carrier admin sees neither the status (global key) nor the quarantine: %s", r.raw)
	}

	// The own fleet's admin: contractor reach (O, A, B) and stewardship (accounts without a tenant, the status page).
	taO := w.login("ta.o@example.test")
	o = decodeData[iam.Overview](t, w.get("/v1/security/overview", taO))
	if o.Users.Total != 7 || o.Logins.Failed24h != 2 {
		t.Errorf("own fleet reach: users %+v logins %+v", o.Users, o.Logins)
	}
	if o.System == nil || o.System.Database != "ok" || o.System.Redis != "ok" || o.System.Scheduler.LeaderPresent {
		t.Fatalf("status %+v", o.System)
	}
	// A scheduler replica holding the leader lock (a session lock, as internal/scheduler takes it).
	lock, err := pgx.Connect(ctx, w.d.URL(db.RoleApp))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close(ctx) }()
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1))`, iam.SchedulerLockName); err != nil {
		t.Fatal(err)
	}
	w.exec(`INSERT INTO jobs (type, status, owner_user_id) VALUES ('billing.backfill-trips', 'queued', $1::uuid)`, w.userID("ta.o@example.test"))
	o = decodeData[iam.Overview](t, w.get("/v1/security/overview", taO))
	if !o.System.Scheduler.LeaderPresent || o.System.Jobs.Queued != 1 {
		t.Errorf("status with a leader and a queued job: %+v", o.System)
	}

	// Platform: a tenant context is required; * covers everyone and adds the quarantine rows.
	pa := w.login("pa@example.test")
	expect(t, w.get("/v1/security/overview", pa), http.StatusForbidden, "tenant_required")
	r = w.get("/v1/security/overview", pa, "X-Act-On-Tenant", "*")
	expect(t, r, http.StatusOK, "")
	o = decodeData[iam.Overview](t, r)
	if !o.Scope.All || o.Users.Total != 11 || o.Logins.Failed24h != 3 || o.Quarantine == nil || o.Quarantine.ByTable["tasks"] != 2 {
		t.Errorf("platform overview: %s", r.raw)
	}
	expect(t, w.get("/v1/security/overview", w.login("mg.a@example.test")), http.StatusForbidden, "permission_denied")

	// Users by role.
	r = w.get("/v1/stats/users-by-role", taO)
	expect(t, r, http.StatusOK, "")
	u := decodeData[iam.UsersByRole](t, r)
	if u.Total != 7 || u.TenantRoles["tenant_admin"] != 2 || u.TenantRoles["manager"] != 2 || u.TenantRoles["operator"] != 2 ||
		u.TenantRoles["driver"] != 0 || u.Scopes["customer"] != 1 || u.PlatformRoles["platform_admin"] != 0 || u.None != 0 {
		t.Errorf("own fleet users by role: %s", r.raw)
	}
	u = decodeData[iam.UsersByRole](t, w.get("/v1/stats/users-by-role", pa, "X-Act-On-Tenant", "*"))
	if u.Total != 11 || u.TenantRoles["operator"] != 3 || u.Scopes["dispatcher"] != 1 || u.PlatformRoles["platform_admin"] != 1 ||
		u.PlatformRoles["support"] != 1 {
		t.Errorf("platform users by role: %+v", u)
	}
}

// DELETE /v1/users/{id}: platform_admin only, a soft delete that revokes every session, removes every grant,
// unlinks the driver, frees the email and records user_deleted; not self; a deleted user is 404.
func TestUserSoftDelete(t *testing.T) {
	w := newWorld(t)
	pa, taA := w.login("pa@example.test"), w.login("ta.a@example.test")
	mgA := w.login("mg.a@example.test")
	id := w.userID("mg.a@example.test")
	// A driver account of A, linked.
	drvUser := w.user("drv.a@example.test")
	drv := w.id(`SELECT id::text FROM drivers WHERE tenant_id = $1 LIMIT 1`, w.A)
	w.member(drvUser, w.A, "driver")
	w.exec(`UPDATE drivers SET user_id = $1 WHERE id = $2`, drvUser, drv)

	expect(t, w.send(http.MethodDelete, "/v1/users/"+id, taA, nil), http.StatusForbidden, "permission_denied")
	expect(t, w.send(http.MethodDelete, "/v1/users/"+w.userID("pa@example.test"), pa, nil), http.StatusForbidden, "permission_denied")

	w.oneEvent("user_deleted", func() resp { return w.send(http.MethodDelete, "/v1/users/"+id, pa, nil) }, http.StatusNoContent)
	if w.id(`SELECT status FROM users WHERE id = $1`, id) != "deleted" ||
		w.count(`SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NOT NULL`, id) != 1 ||
		w.count(`SELECT count(*) FROM memberships WHERE user_id = $1`, id) != 0 ||
		w.count(`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_reason = 'disabled'`, id) != 1 {
		t.Fatal("soft delete: status, grants or sessions")
	}
	d, _ := w.lastEventDetails("user_deleted")
	if ms, _ := d["memberships"].([]any); len(ms) != 1 || d["previousStatus"] != "active" {
		t.Fatalf("user_deleted details %v", d)
	}
	expect(t, w.get("/v1/me", mgA), http.StatusUnauthorized, "session_revoked")
	expect(t, w.loginWith("mg.a@example.test", pw), http.StatusUnauthorized, "invalid_credentials")
	expect(t, w.send(http.MethodDelete, "/v1/users/"+id, pa, nil), http.StatusNotFound, "not_found")
	// The address can go to a new account.
	w.user("mg.a@example.test")
	// The deleted user is listed only on request.
	r := w.get("/v1/users?status=deleted", pa, "X-Act-On-Tenant", "*")
	if users := decodeData[[]iam.User](t, r); len(users) != 1 || users[0].ID.String() != id {
		t.Fatalf("deleted users: %s", r.raw)
	}

	// A driver account: the driver row is unlinked, so the driver can get another account.
	w.oneEvent("user_deleted", func() resp { return w.send(http.MethodDelete, "/v1/users/"+drvUser, pa, nil) }, http.StatusNoContent)
	if w.count(`SELECT count(*) FROM drivers WHERE id = $1 AND user_id IS NULL`, drv) != 1 {
		t.Fatal("the driver row is still linked")
	}
	if d, _ := w.lastEventDetails("user_deleted"); d["driverId"] != drv {
		t.Fatalf("details %v", d)
	}
}
