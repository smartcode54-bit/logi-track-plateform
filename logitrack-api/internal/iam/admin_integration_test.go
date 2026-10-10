//go:build integration

// Acceptance tests of issue T19 (#39) for the users and tenants administration on the internal listener
// (Appendix B §B.2.4, §B.2.5, Appendix C §C.8): keyset paging past 1000 users, claims changes that keep the
// session and status / password events that end it, the not-self rule, every write gated by its capability
// and recorded by exactly one named security_events row in its transaction, 422 for an unknown scope kind,
// the platform reads through X-Act-On-Tenant, and the owner additions: a platform admin onboards a carrier
// tenant and its tenant_admin, who then sees only its tenant (RLS) and switches only among its memberships.
package iam_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase/firebasetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// send is call with a JSON body.
func (w *world) send(method, path, bearer string, body any, headers ...string) resp {
	w.t.Helper()
	var rd io.Reader = bytes.NewReader(nil)
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			w.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, w.internal+path, rd)
	if err != nil {
		w.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, raw: raw}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			w.t.Fatalf("%s %s: not JSON: %s", method, path, raw)
		}
	}
	return out
}

// loginWith signs in with a password and returns the response.
func (w *world) loginWith(email, password string) resp {
	w.t.Helper()
	return w.send(http.MethodPost, "/v1/auth/login", "", map[string]any{"email": email, "password": password, "platform": "web"})
}

// tokens signs in with pw and returns the access and refresh tokens.
func (w *world) tokens(email string) (access, refresh string) {
	w.t.Helper()
	r := w.loginWith(email, pw)
	expect(w.t, r, http.StatusOK, "")
	d := r.data().(map[string]any)
	return d["accessToken"].(string), d["refreshToken"].(string)
}

func (w *world) userID(email string) string {
	w.t.Helper()
	return w.id(`SELECT id::text FROM users WHERE email = $1::citext`, email)
}

// auditCount counts the security_events rows a write appends (the cross-tenant access rows are the reads').
func (w *world) auditCount() int {
	w.t.Helper()
	return w.count(`SELECT count(*) FROM security_events WHERE event_type <> 'platform_cross_tenant_access'`)
}

// oneEvent runs a write and asserts it appended exactly one security_events row, of type eventType.
func (w *world) oneEvent(eventType string, write func() resp, status int) resp {
	w.t.Helper()
	before, typed := w.auditCount(), w.securityEvents(eventType)
	r := write()
	if r.status != status {
		w.t.Fatalf("%s: want %d, got %d %s", eventType, status, r.status, r.raw)
	}
	if got := w.auditCount() - before; got != 1 || w.securityEvents(eventType) != typed+1 {
		w.t.Fatalf("%s: %d security_events rows appended, want exactly one of that type", eventType, got)
	}
	return r
}

func (w *world) noEvent(write func() resp) resp {
	w.t.Helper()
	before := w.auditCount()
	r := write()
	if got := w.auditCount() - before; got != 0 {
		w.t.Fatalf("a refused or empty write appended %d security_events rows: %d %s", got, r.status, r.raw)
	}
	return r
}

func reason(r resp) string {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	s, _ := d["reason"].(string)
	return s
}

func missingCap(r resp) []string {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	var out []string
	for _, v := range d["missingCapability"].([]any) {
		out = append(out, v.(string))
	}
	return out
}

// AC: GET /v1/users pages past 1000 users with a cursor, in both orders, without duplicates.
func TestUsersPagePast1000(t *testing.T) {
	w := newWorld(t)
	w.exec(`WITH u AS (
		INSERT INTO users (email, display_name, last_login_at)
		SELECT 'bulk' || g || '@example.test', 'Bulk ' || g,
		       CASE WHEN g % 3 = 0 THEN NULL ELSE now() - g * interval '1 minute' END
		FROM generate_series(1, 1200) g RETURNING id)
		INSERT INTO memberships (user_id, tenant_id, role) SELECT id, $1, 'operator' FROM u`, w.O)
	ta := w.login("ta.o@example.test")
	// The own fleet's reach: its members, the members of A and B (carriers working for it) and, as a steward,
	// the customer-scope account cu; not D's dispatcher, not C's admin, not the platform accounts.
	const want = 1200 + 6 + 1
	for _, sort := range []string{"last_login_at", "created_at"} {
		seen := map[string]bool{}
		cursor, pages := "", 0
		for {
			q := "/v1/users?limit=500&sort=" + sort
			if cursor != "" {
				q += "&cursor=" + cursor
			}
			r := w.get(q, ta)
			expect(t, r, http.StatusOK, "")
			pages++
			for _, it := range r.data().([]any) {
				id := it.(map[string]any)["id"].(string)
				if seen[id] {
					t.Fatalf("%s: user %s on two pages", sort, id)
				}
				seen[id] = true
			}
			next, _ := r.body["nextCursor"].(string)
			if next == "" {
				break
			}
			cursor = next
		}
		if len(seen) != want || pages != 3 {
			t.Errorf("%s: %d users on %d pages, want %d on 3", sort, len(seen), pages, want)
		}
		for _, email := range []string{"ds@example.test", "ta.c@example.test", "pa@example.test"} {
			if seen[w.userID(email)] {
				t.Errorf("%s is outside the own fleet's reach but was listed", email)
			}
		}
	}
	// Filters and the response shape.
	r := w.get("/v1/users?q=bulk12&role=operator&status=active", ta)
	expect(t, r, http.StatusOK, "")
	items := r.data().([]any)
	if len(items) != 12 { // bulk12, bulk120-129, bulk1200
		t.Errorf("q=bulk12: %d users", len(items))
	}
	u := items[0].(map[string]any)
	for _, k := range []string{"id", "email", "displayName", "photoUrl", "status", "mustChangePassword", "lastLoginAt",
		"createdAt", "legacyAuthUid", "memberships", "scopes", "platformRoles", "driver"} {
		if _, ok := u[k]; !ok {
			t.Errorf("a user item lacks %q: %v", k, u)
		}
	}
	expect(t, w.get("/v1/users?page=2", ta), http.StatusBadRequest, "bad_request")
	expect(t, w.get("/v1/users?cursor=bogus", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/users?limit=501", ta), http.StatusUnprocessableEntity, "invalid_argument")
	// A carrier's staff sees its own members only.
	r = w.get("/v1/users?limit=500", w.login("ta.a@example.test"))
	expect(t, r, http.StatusOK, "")
	var emails []string
	for _, it := range r.data().([]any) {
		emails = append(emails, it.(map[string]any)["email"].(string))
	}
	slices.Sort(emails)
	if !slices.Equal(emails, []string{"mg.a@example.test", "op.a@example.test", "ta.a@example.test"}) {
		t.Errorf("carrier A lists %v", emails)
	}
}

// AC: role, scope, driver-link and platform-role changes bump auth_version and keep the session; disable and a
// temporary password end it; admin routes cannot target the caller; every write is gated by its capability and
// appends its named security_events row in the same transaction; an unknown scope kind is 422.
func TestUserAdministration(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")
	pa := w.login("pa@example.test")
	mg, mgRefresh := w.tokens("mg.o@example.test")
	mgID, opID, cuID := w.userID("mg.o@example.test"), w.userID("op.o@example.test"), w.userID("cu@example.test")
	taID := w.userID("ta.o@example.test")
	stillSignedIn := func(access, refresh, label string) {
		t.Helper()
		r := w.get("/v1/me", access)
		if r.status != http.StatusUnauthorized || r.code() != "token_expired" || reason(r) != "claims_changed" {
			t.Fatalf("%s: the old token answers %d %s, want 401 token_expired claims_changed", label, r.status, r.raw)
		}
		r = w.send(http.MethodPost, "/v1/auth/refresh", "", map[string]any{"refreshToken": refresh})
		expect(t, r, http.StatusOK, "")
	}

	// A role change (claims_changed: signed in, new claims after the refresh).
	w.oneEvent("user_role_changed", func() resp {
		return w.send(http.MethodPut, "/v1/tenants/"+w.O+"/members/"+mgID, ta, map[string]any{"role": "operator"})
	}, http.StatusOK)
	if n := w.count(`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, mgID); n == 0 {
		t.Fatal("a role change revoked the session")
	}
	stillSignedIn(mg, mgRefresh, "role change")
	if role := w.id(`SELECT role FROM memberships WHERE user_id = $1 AND tenant_id = $2`, mgID, w.O); role != "operator" {
		t.Fatalf("role %s", role)
	}
	w.noEvent(func() resp { // the same role again changes nothing
		return w.send(http.MethodPut, "/v1/tenants/"+w.O+"/members/"+mgID, ta, map[string]any{"role": "operator"})
	})
	w.oneEvent("user_role_changed", func() resp {
		return w.send(http.MethodPut, "/v1/tenants/"+w.O+"/members/"+mgID, ta, map[string]any{"role": "manager"})
	}, http.StatusOK)

	// A customer scope (steward caller) and the 422 of an unknown kind.
	cu, cuRefresh := w.tokens("cu@example.test")
	w.oneEvent("user_scope_changed", func() resp {
		return w.send(http.MethodPut, "/v1/users/"+cuID+"/scopes/customer", ta, map[string]any{"billingPartyIds": []string{w.X, w.Y}})
	}, http.StatusOK)
	stillSignedIn(cu, cuRefresh, "scope change")
	if n := w.count(`SELECT count(*) FROM user_scopes WHERE user_id = $1 AND kind = 'customer'`, cuID); n != 2 {
		t.Fatalf("%d customer scopes", n)
	}
	for _, kind := range []string{"company", "partner", "tenant"} {
		r := w.noEvent(func() resp {
			return w.send(http.MethodPut, "/v1/users/"+cuID+"/scopes/"+kind, ta, map[string]any{"billingPartyIds": []string{w.X}})
		})
		expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = uuid.NewString()
	}
	expect(t, w.send(http.MethodPut, "/v1/users/"+cuID+"/scopes/customer", ta, map[string]any{"billingPartyIds": many}),
		http.StatusUnprocessableEntity, "too_many_scopes")
	// A carrier admin is no steward: customer scopes are global (R60).
	r := w.send(http.MethodPut, "/v1/users/"+w.userID("op.a@example.test")+"/scopes/customer", w.login("ta.a@example.test"),
		map[string]any{"billingPartyIds": []string{w.X}})
	if r.status != http.StatusForbidden || reason(r) != "steward_only" {
		t.Fatalf("carrier admin sets a customer scope: %d %s", r.status, r.raw)
	}
	// Dispatcher grants are platform-only and need the dispatcher's own membership.
	expect(t, w.send(http.MethodPut, "/v1/users/"+mgID+"/scopes/dispatcher", ta, map[string]any{"billingPartyIds": []string{w.X}}),
		http.StatusForbidden, "permission_denied")
	r = w.send(http.MethodPut, "/v1/users/"+cuID+"/scopes/dispatcher", pa, map[string]any{"billingPartyIds": []string{w.X}})
	if r.status != http.StatusConflict || reason(r) != "membership_required" {
		t.Fatalf("dispatcher grant without membership: %d %s", r.status, r.raw)
	}
	w.oneEvent("user_scope_changed", func() resp { return w.send(http.MethodDelete, "/v1/users/"+cuID+"/scopes/customer", ta, nil) },
		http.StatusNoContent)

	// A driver link moves atomically: membership, drivers.user_id, claims_changed.
	drv := w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile)
		VALUES ($1, 'self', 'Somchai', 'Jaidee', '0811111111') RETURNING id::text`, w.O)
	newbie := w.user("newbie@example.test") // no membership yet: a steward reaches it
	nb, nbRefresh := w.tokens("newbie@example.test")
	w.oneEvent("driver_linked", func() resp {
		return w.send(http.MethodPut, "/v1/users/"+newbie+"/driver-link", ta, map[string]any{"driverId": drv})
	}, http.StatusNoContent)
	stillSignedIn(nb, nbRefresh, "driver link")
	if n := w.count(`SELECT count(*) FROM drivers d JOIN memberships m ON m.user_id = d.user_id AND m.tenant_id = d.tenant_id
		AND m.role = 'driver' WHERE d.id = $1 AND d.user_id = $2`, drv, newbie); n != 1 {
		t.Fatal("the driver link or its membership is missing")
	}
	r = w.send(http.MethodPut, "/v1/tenants/"+w.O+"/members/"+newbie, ta, map[string]any{"role": "manager"})
	if r.status != http.StatusConflict || reason(r) != "driver_linked" {
		t.Fatalf("role change of a linked driver: %d %s", r.status, r.raw)
	}
	w.oneEvent("driver_unlinked", func() resp { return w.send(http.MethodDelete, "/v1/users/"+newbie+"/driver-link", ta, nil) },
		http.StatusNoContent)
	expect(t, w.send(http.MethodDelete, "/v1/users/"+newbie+"/driver-link", ta, nil), http.StatusNotFound, "not_found")

	// Platform roles (platform:manage_platform_roles).
	mg, mgRefresh = w.tokens("mg.o@example.test")
	w.oneEvent("platform_role_granted", func() resp {
		return w.send(http.MethodPost, "/v1/users/"+mgID+"/platform-roles", pa, map[string]any{"role": "support"})
	}, http.StatusNoContent)
	stillSignedIn(mg, mgRefresh, "platform role")
	expect(t, w.send(http.MethodPost, "/v1/users/"+mgID+"/platform-roles", pa, map[string]any{"role": "root"}),
		http.StatusUnprocessableEntity, "invalid_argument")
	w.oneEvent("platform_role_revoked", func() resp {
		return w.send(http.MethodDelete, "/v1/users/"+mgID+"/platform-roles/support", pa, nil)
	}, http.StatusNoContent)
	expect(t, w.send(http.MethodDelete, "/v1/users/"+mgID+"/platform-roles/support", pa, nil), http.StatusNotFound, "not_found")

	// Disable ends every session; enable makes the user usable again.
	op := w.login("op.o@example.test")
	w.oneEvent("user_disabled", func() resp {
		return w.send(http.MethodPost, "/v1/users/"+opID+"/disable", ta, map[string]any{"reason": "left the company"})
	}, http.StatusNoContent)
	expect(t, w.get("/v1/me", op), http.StatusUnauthorized, "session_revoked")
	expect(t, w.loginWith("op.o@example.test", pw), http.StatusForbidden, "account_disabled")
	w.oneEvent("user_enabled", func() resp { return w.send(http.MethodPost, "/v1/users/"+opID+"/enable", ta, map[string]any{}) },
		http.StatusNoContent)

	// A temporary password ends every session; the next sign-in must change it (R79).
	op = w.login("op.o@example.test")
	r = w.oneEvent("user_password_temporary_issued", func() resp {
		return w.send(http.MethodPost, "/v1/users/"+opID+"/password/temporary", ta, map[string]any{})
	}, http.StatusOK)
	tmp, _ := r.data().(map[string]any)["temporaryPassword"].(string)
	if len(tmp) != 12 {
		t.Fatalf("temporary password of %d characters", len(tmp))
	}
	expect(t, w.get("/v1/me", op), http.StatusUnauthorized, "session_revoked")
	expect(t, w.loginWith("op.o@example.test", tmp), http.StatusForbidden, "password_change_required")

	// Admin revocation of sessions.
	mg = w.login("mg.o@example.test")
	r = w.get("/v1/users/"+mgID+"/sessions", ta)
	expect(t, r, http.StatusOK, "")
	if len(r.data().([]any)) == 0 {
		t.Fatal("no live session listed")
	}
	w.oneEvent("user_sessions_revoked", func() resp { return w.send(http.MethodDelete, "/v1/users/"+mgID+"/sessions", ta, nil) },
		http.StatusNoContent)
	expect(t, w.get("/v1/me", mg), http.StatusUnauthorized, "session_revoked")
	expect(t, w.send(http.MethodDelete, "/v1/users/"+mgID+"/sessions/"+uuid.NewString(), ta, nil), http.StatusNotFound, "not_found")

	// Not self: every admin route refuses the caller.
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/users/" + taID + "/disable"}, {http.MethodPost, "/v1/users/" + taID + "/password/temporary"},
		{http.MethodDelete, "/v1/users/" + taID + "/sessions"}, {http.MethodGet, "/v1/users/" + taID + "/sessions"},
		{http.MethodPut, "/v1/tenants/" + w.O + "/members/" + taID}, {http.MethodPatch, "/v1/users/" + taID},
	} {
		r := w.noEvent(func() resp { return w.send(c.method, c.path, ta, map[string]any{"role": "tenant_admin"}) })
		if r.status != http.StatusForbidden || reason(r) != "self" {
			t.Errorf("%s %s on the caller: %d %s", c.method, c.path, r.status, r.raw)
		}
	}

	// Every write is capability-gated: the manager holds users:view only.
	mg = w.login("mg.o@example.test")
	for _, c := range []struct {
		method, path string
		missing      []string
	}{
		{http.MethodPost, "/v1/users", []string{"users:manage"}},
		{http.MethodPatch, "/v1/users/" + opID, []string{"users:manage"}},
		{http.MethodPost, "/v1/users/" + opID + "/invite", []string{"users:manage"}},
		{http.MethodPost, "/v1/users/" + opID + "/disable", []string{"users:manage"}},
		{http.MethodPost, "/v1/users/" + opID + "/password/temporary", []string{"users:manage"}},
		{http.MethodDelete, "/v1/users/" + opID + "/sessions", []string{"users:revoke_sessions"}},
		{http.MethodPut, "/v1/users/" + opID + "/scopes/customer", []string{"users:assign_role", "platform:manage_platform_roles"}},
		{http.MethodPut, "/v1/users/" + opID + "/driver-link", []string{"users:manage"}},
		{http.MethodPost, "/v1/users/" + opID + "/platform-roles", []string{"platform:manage_platform_roles"}},
		{http.MethodPut, "/v1/tenants/" + w.O + "/members/" + opID, []string{"users:assign_role"}},
		{http.MethodPost, "/v1/tenants", []string{"platform:manage_tenants"}},
	} {
		r := w.noEvent(func() resp { return w.send(c.method, c.path, mg, map[string]any{}) })
		if r.status != http.StatusForbidden || r.code() != "permission_denied" || !slices.Equal(missingCap(r), c.missing) {
			t.Errorf("manager %s %s: %d %s, want 403 missing %v", c.method, c.path, r.status, r.raw, c.missing)
		}
	}
	// tenant_admin is granted only by a tenant_admin of that tenant or a platform admin (an own-fleet admin may
	// manage its carriers' members, not their admins).
	r = w.send(http.MethodPut, "/v1/tenants/"+w.A+"/members/"+w.userID("op.a@example.test"), ta, map[string]any{"role": "tenant_admin"})
	if r.status != http.StatusForbidden || reason(r) != "tenant_admin_only" {
		t.Errorf("own-fleet admin grants tenant_admin in A: %d %s", r.status, r.raw)
	}
	w.oneEvent("user_role_changed", func() resp {
		return w.send(http.MethodPut, "/v1/tenants/"+w.A+"/members/"+w.userID("op.a@example.test"), ta, map[string]any{"role": "manager"})
	}, http.StatusOK)
	// Outside the reach a user does not exist; a tenant outside it is refused.
	expect(t, w.send(http.MethodPost, "/v1/users/"+w.userID("ta.c@example.test")+"/disable", ta, map[string]any{}), http.StatusNotFound, "not_found")
	expect(t, w.send(http.MethodPut, "/v1/tenants/"+w.C+"/members/"+opID, ta, map[string]any{"role": "user"}), http.StatusForbidden, "permission_denied")

	// Create, invite, patch.
	r = w.oneEvent("user_created", func() resp {
		return w.send(http.MethodPost, "/v1/users", ta, map[string]any{"email": "New.Staff@Example.test", "displayName": "New Staff",
			"role": "operator"})
	}, http.StatusCreated)
	created := r.data().(map[string]any)
	user := created["user"].(map[string]any)
	if user["email"] != "new.staff@example.test" || len(created["temporaryPassword"].(string)) != 12 ||
		len(user["memberships"].([]any)) != 1 {
		t.Fatalf("created %v", created)
	}
	newID := user["id"].(string)
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'user.created' AND aggregate_id = $1
		AND payload->>'sendInvite' = 'false'`, newID); n != 1 {
		t.Errorf("%d outbox user.created rows", n)
	}
	expect(t, w.send(http.MethodPost, "/v1/users", ta, map[string]any{"email": "new.staff@example.test", "displayName": "Dup",
		"role": "operator"}), http.StatusConflict, "already_exists")
	r = w.oneEvent("user_created", func() resp {
		return w.send(http.MethodPost, "/v1/users", ta, map[string]any{"email": "invited@example.test", "displayName": "Invited",
			"role": "user", "sendInvite": true})
	}, http.StatusCreated)
	if _, ok := r.data().(map[string]any)["temporaryPassword"]; ok {
		t.Error("an invite returned a temporary password")
	}
	w.oneEvent("user_invited", func() resp {
		return w.send(http.MethodPost, "/v1/users/"+newID+"/invite", ta, map[string]any{"locale": "en"})
	}, http.StatusAccepted)
	w.oneEvent("user_updated", func() resp {
		return w.send(http.MethodPatch, "/v1/users/"+newID, ta, map[string]any{"email": "renamed@example.test", "displayName": "Renamed"})
	}, http.StatusOK)
	if v := w.count(`SELECT auth_version FROM users WHERE id = $1`, newID); v != 2 {
		t.Errorf("an email change left auth_version at %d", v)
	}
	expect(t, w.send(http.MethodPost, "/v1/users", ta, map[string]any{"email": "x@example.test", "displayName": "X",
		"role": "operator", "tenantId": w.C}), http.StatusForbidden, "permission_denied")

	// A customer-scope account: steward only, with billingPartyIds, never with a tenant or driver.
	expect(t, w.send(http.MethodPost, "/v1/users", ta, map[string]any{"email": "c2@example.test", "displayName": "C2",
		"role": "customer"}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.send(http.MethodPost, "/v1/users", ta, map[string]any{"email": "c2@example.test", "displayName": "C2",
		"role": "customer", "billingPartyIds": []string{w.X}, "tenantId": w.O}), http.StatusUnprocessableEntity, "invalid_argument")
	r = w.send(http.MethodPost, "/v1/users", w.login("ta.a@example.test"), map[string]any{"email": "c3@example.test",
		"displayName": "C3", "role": "customer", "billingPartyIds": []string{w.X}})
	if r.status != http.StatusForbidden || reason(r) != "steward_only" {
		t.Errorf("carrier admin creates a customer account: %d %s", r.status, r.raw)
	}
	r = w.oneEvent("user_created", func() resp {
		return w.send(http.MethodPost, "/v1/users", ta, map[string]any{"email": "c2@example.test", "displayName": "C2",
			"role": "customer", "billingPartyIds": []string{w.X}})
	}, http.StatusCreated)
	if s := r.data().(map[string]any)["user"].(map[string]any)["scopes"].([]any); len(s) != 1 {
		t.Errorf("customer account scopes %v", s)
	}
}

// Owner additions: a platform admin creates a carrier tenant and its tenant_admin; that admin sees only its
// tenant's rows (RLS) and switches tenants only among its memberships. Platform reads go through X-Act-On-Tenant
// (one platform_cross_tenant_access row each), platform writes need no header.
func TestTenantOnboarding(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	expect(t, w.send(http.MethodPost, "/v1/tenants", pa, map[string]any{"kind": "carrier", "code": "NEW", "nameTh": "ใหม่"}),
		http.StatusUnprocessableEntity, "invalid_argument") // legalType is required for a carrier (0002 CHECK)
	expect(t, w.send(http.MethodPost, "/v1/tenants", pa, map[string]any{"kind": "own_fleet", "code": "OWN2", "nameTh": "x",
		"legalType": "company"}), http.StatusUnprocessableEntity, "invalid_argument")
	r := w.oneEvent("tenant_created", func() resp {
		return w.send(http.MethodPost, "/v1/tenants", pa, map[string]any{"kind": "carrier", "code": "NEW", "nameTh": "ขนส่งใหม่",
			"nameEn": "New Carrier", "legalType": "company", "taxId": "0105536000313"})
	}, http.StatusCreated)
	tn := r.data().(map[string]any)
	newT := tn["id"].(string)
	if tn["kind"] != "carrier" || tn["billingPartyId"] == nil || tn["legalType"] != "company" {
		t.Fatalf("created tenant %v", tn)
	}
	if n := w.count(`SELECT count(*) FROM billing_parties WHERE kind = 'tenant' AND tenant_id = $1`, newT); n != 1 {
		t.Errorf("%d billing parties of the new tenant", n)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'tenant.created' AND aggregate_id = $1`, newT); n != 1 {
		t.Errorf("%d tenant.created events", n)
	}
	expect(t, w.send(http.MethodPost, "/v1/tenants", pa, map[string]any{"kind": "carrier", "code": "new", "nameTh": "dup",
		"legalType": "company"}), http.StatusConflict, "already_exists")
	expect(t, w.send(http.MethodPost, "/v1/tenants", pa, map[string]any{"kind": "carrier", "code": "BADTAX", "nameTh": "x",
		"legalType": "company", "taxId": "0105536000316"}), http.StatusUnprocessableEntity, "invalid_argument")

	// Its tenant_admin, created by the platform admin without X-Act-On-Tenant.
	r = w.oneEvent("user_created", func() resp {
		return w.send(http.MethodPost, "/v1/users", pa, map[string]any{"email": "admin@new.example.test", "displayName": "New Admin",
			"role": "tenant_admin", "tenantId": newT})
	}, http.StatusCreated)
	tmp := r.data().(map[string]any)["temporaryPassword"].(string)
	r = w.loginWith("admin@new.example.test", tmp)
	expect(t, r, http.StatusForbidden, "password_change_required")
	ticket := r.body["error"].(map[string]any)["details"].(map[string]any)["passwordChangeTicket"].(string)
	const newPw = "the new admin passphrase 7"
	expect(t, w.send(http.MethodPost, "/v1/auth/password/change", "", map[string]any{"passwordChangeTicket": ticket,
		"newPassword": newPw}), http.StatusNoContent, "")
	r = w.loginWith("admin@new.example.test", newPw)
	expect(t, r, http.StatusOK, "")
	nta := r.data().(map[string]any)["accessToken"].(string)

	// RLS: only the new tenant's rows.
	w.task("NEWx", newT, &w.X)
	if got := named(w, w.perTenant(nta, "")); len(got) != 1 || got["?"+newT] != 1 {
		t.Errorf("the new tenant_admin reads tasks of %v", got)
	}
	who := w.whoami(nta)
	if who.TenantKind != "carrier" || who.Steward || !slices.Contains(who.Caps, "users:assign_role") {
		t.Errorf("new tenant_admin: %+v", who)
	}
	r = w.get("/v1/users", nta)
	expect(t, r, http.StatusOK, "")
	if items := r.data().([]any); len(items) != 1 || items[0].(map[string]any)["email"] != "admin@new.example.test" {
		t.Errorf("the new tenant_admin lists %v", items)
	}
	expect(t, w.get("/v1/tenants/"+newT, nta), http.StatusOK, "")
	expect(t, w.get("/v1/tenants/"+w.A, nta), http.StatusNotFound, "not_found")

	// Switching: only among memberships. The platform admin adds one in C (no header), then C works, O not.
	ntaID := w.userID("admin@new.example.test")
	expect(t, w.send(http.MethodPost, "/v1/auth/tenant", nta, map[string]any{"tenantId": w.O}), http.StatusForbidden, "permission_denied")
	w.oneEvent("user_role_changed", func() resp {
		return w.send(http.MethodPut, "/v1/tenants/"+w.C+"/members/"+ntaID, pa, map[string]any{"role": "manager"})
	}, http.StatusOK)
	nta = w.login2("admin@new.example.test", newPw)
	r = w.send(http.MethodPost, "/v1/auth/tenant", nta, map[string]any{"tenantId": w.C})
	expect(t, r, http.StatusOK, "")
	cTok := r.data().(map[string]any)["accessToken"].(string)
	if got := named(w, w.perTenant(cTok, "")); len(got) != 1 || got["C"] != 2 {
		t.Errorf("after the switch to C: %v", got)
	}

	// Platform reads: X-Act-On-Tenant (one audit row each); members of the new tenant, every user with *.
	audits := w.securityEvents("platform_cross_tenant_access")
	r = w.get("/v1/tenants/"+newT+"/members?role=tenant_admin", pa, "X-Act-On-Tenant", newT)
	expect(t, r, http.StatusOK, "")
	ms := r.data().([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["user"].(map[string]any)["email"] != "admin@new.example.test" {
		t.Errorf("members %v", ms)
	}
	r = w.get("/v1/users?limit=500", pa, "X-Act-On-Tenant", "*")
	expect(t, r, http.StatusOK, "")
	var all []string
	for _, it := range r.data().([]any) {
		all = append(all, it.(map[string]any)["email"].(string))
	}
	for _, e := range []string{"cu@example.test", "ds@example.test", "ta.c@example.test", "admin@new.example.test"} {
		if !slices.Contains(all, e) {
			t.Errorf("GET /v1/users with * lacks %s", e)
		}
	}
	if got := w.securityEvents("platform_cross_tenant_access") - audits; got != 2 {
		t.Errorf("%d cross-tenant audit rows for two platform reads", got)
	}
	// Without the header the platform admin's read reach holds no tenant.
	r = w.get("/v1/tenants/"+newT+"/members", pa)
	expect(t, r, http.StatusNotFound, "not_found")

	// Tenants list and patch rules.
	r = w.get("/v1/tenants?kind=carrier&limit=2", pa)
	expect(t, r, http.StatusOK, "")
	if next, _ := r.body["nextCursor"].(string); next == "" || len(r.data().([]any)) != 2 {
		t.Errorf("first carriers page: %s", r.raw)
	}
	r = w.get("/v1/tenants", w.login("ta.o@example.test")) // a steward (fleet:manage_subcontractors): carriers only
	expect(t, r, http.StatusOK, "")
	for _, it := range r.data().([]any) {
		if k := it.(map[string]any)["kind"]; k != "carrier" {
			t.Errorf("a steward lists a %v tenant", k)
		}
	}
	expect(t, w.get("/v1/tenants", w.login("ta.a@example.test")), http.StatusForbidden, "permission_denied")
	r = w.noEvent(func() resp {
		return w.send(http.MethodPatch, "/v1/tenants/"+w.O, pa, map[string]any{"status": "suspended"})
	})
	if r.status != http.StatusConflict || r.code() != "failed_precondition" {
		t.Errorf("suspend the own fleet: %d %s", r.status, r.raw)
	}
	expect(t, w.send(http.MethodPatch, "/v1/tenants/"+w.Q, pa, map[string]any{"status": "suspended"}), http.StatusConflict, "failed_precondition")
	expect(t, w.send(http.MethodPatch, "/v1/tenants/"+newT, pa, map[string]any{"kind": "own_fleet"}), http.StatusConflict, "failed_precondition")
	r = w.oneEvent("tenant_updated", func() resp {
		return w.send(http.MethodPatch, "/v1/tenants/"+newT, pa, map[string]any{"nameEn": nil, "nameTh": "ขนส่งใหม่ 2"})
	}, http.StatusOK)
	if d := r.data().(map[string]any); d["nameEn"] != nil || d["nameTh"] != "ขนส่งใหม่ 2" {
		t.Errorf("patched names %v", d)
	}
	w.oneEvent("tenant_updated", func() resp {
		return w.send(http.MethodPatch, "/v1/tenants/"+newT, pa, map[string]any{"status": "suspended", "reason": "contract ended"})
	}, http.StatusOK)
	if n := w.count(`SELECT count(*) FROM status_history WHERE entity_type = 'tenant' AND entity_id = $1`, newT); n != 2 {
		t.Errorf("%d status history rows (created + suspended)", n)
	}
	before := w.securityEvents("tenant_contractor_changed")
	r = w.send(http.MethodPatch, "/v1/tenants/"+newT, pa, map[string]any{"contractorTenantId": w.O})
	expect(t, r, http.StatusOK, "")
	if w.securityEvents("tenant_contractor_changed") != before+1 {
		t.Error("no tenant_contractor_changed row")
	}
	expect(t, w.send(http.MethodPatch, "/v1/tenants/"+newT, pa, map[string]any{"contractorTenantId": w.A}),
		http.StatusUnprocessableEntity, "invalid_argument") // one level only (A works for O)
	r = w.get("/v1/tenants/"+newT, pa)
	expect(t, r, http.StatusOK, "")
	if h := r.data().(map[string]any)["statusHistory"].([]any); len(h) != 2 {
		t.Errorf("status history %v", h)
	}
	if strings.Contains(string(r.raw), `"idCardNumber"`) {
		t.Error("an empty idCardNumber is serialised")
	}
}

// login2 signs in with a password other than the fixture's.
func (w *world) login2(email, password string) string {
	w.t.Helper()
	r := w.loginWith(email, password)
	expect(w.t, r, http.StatusOK, "")
	return r.data().(map[string]any)["accessToken"].(string)
}

// While the Firebase bridge mirrors (Appendix C §C.6.4, T08), the administration writes the user's Firebase
// account before COMMIT: a created own-fleet user gets its account, an email change, a disable and a role change
// follow, a carrier user created in Go gets none, and a mirror failure is 503 bridge_unavailable with nothing
// committed in PostgreSQL and no security event.
func TestAdministrationMirrorsFirebase(t *testing.T) {
	b := firebasetest.New(t, "logitrack-iam-test")
	w := newWorldWith(t, auth.Firebase{Mode: auth.BridgeWeb, Verifier: b.Verifier(time.Now), Signer: b.ServiceAccount(),
		Accounts: b.Accounts()})
	ta := w.login("ta.o@example.test")
	r := w.send(http.MethodPost, "/v1/users", ta, map[string]any{"email": "mirror@example.test", "displayName": "Mirror",
		"role": "operator"})
	expect(t, r, http.StatusCreated, "")
	created := r.data().(map[string]any)
	id := created["user"].(map[string]any)["id"].(string)
	acc, ok := b.Get(id)
	if !ok || acc.Email != "mirror@example.test" || acc.Password != created["temporaryPassword"] || acc.Disabled {
		t.Fatalf("Firebase account of the new own-fleet user: %+v (found %t)", acc, ok)
	}
	if !strings.Contains(acc.CustomAttributes, `"role":"operation_staff"`) && !strings.Contains(acc.CustomAttributes, `"role":"operator"`) {
		t.Errorf("legacy claims %s", acc.CustomAttributes)
	}
	expect(t, w.send(http.MethodPatch, "/v1/users/"+id, ta, map[string]any{"email": "mirror2@example.test"}), http.StatusOK, "")
	if acc, _ = b.Get(id); acc.Email != "mirror2@example.test" {
		t.Errorf("Firebase email %q after the change", acc.Email)
	}
	expect(t, w.send(http.MethodPut, "/v1/tenants/"+w.O+"/members/"+id, ta, map[string]any{"role": "tenant_admin"}), http.StatusOK, "")
	if acc, _ = b.Get(id); !strings.Contains(acc.CustomAttributes, `"admin":true`) {
		t.Errorf("Firebase claims after the role change: %s", acc.CustomAttributes)
	}
	expect(t, w.send(http.MethodPost, "/v1/users/"+id+"/disable", ta, map[string]any{}), http.StatusNoContent, "")
	if acc, _ = b.Get(id); !acc.Disabled || acc.ValidSince == 0 {
		t.Errorf("Firebase account after disable: %+v", acc)
	}
	// A carrier user created in Go never gets a Firebase account (C.6.3).
	r = w.send(http.MethodPost, "/v1/users", w.login("ta.a@example.test"), map[string]any{"email": "carrier.new@example.test",
		"displayName": "Carrier New", "role": "operator"})
	expect(t, r, http.StatusCreated, "")
	if _, ok := b.Get(r.data().(map[string]any)["user"].(map[string]any)["id"].(string)); ok {
		t.Error("a carrier user created in Go got a Firebase account")
	}
	// A failing mirror commits nothing.
	b.SetToolkitError(http.StatusInternalServerError, "INTERNAL")
	op := w.userID("op.o@example.test")
	w.exec(`UPDATE users SET legacy_auth_uid = 'fbOp000000000000000000000001' WHERE id = $1`, op)
	b.Put(firebasetest.Account{UID: "fbOp000000000000000000000001", Email: "op.o@example.test"})
	r = w.noEvent(func() resp { return w.send(http.MethodPost, "/v1/users/"+op+"/disable", ta, map[string]any{}) })
	expect(t, r, http.StatusServiceUnavailable, "bridge_unavailable")
	if s := w.id(`SELECT status FROM users WHERE id = $1`, op); s != "active" {
		t.Errorf("status %s after a failed mirror", s)
	}
	b.SetToolkitError(0, "")
}

// refusedFor asserts a 403 permission_denied with details.reason and no security_events row.
func (w *world) refusedFor(reasonWant, label string, write func() resp) {
	w.t.Helper()
	r := w.noEvent(write)
	if r.status != http.StatusForbidden || r.code() != "permission_denied" || reason(r) != reasonWant {
		w.t.Errorf("%s: %d %s, want 403 permission_denied %s", label, r.status, r.raw, reasonWant)
	}
}

// Review panel (T19): reach alone does not make an account manageable. An admin route acts only on a user the caller
// outranks: never on a platform-role holder (the D1 import gives every platform admin an own-fleet tenant_admin
// membership, so an own-fleet admin would otherwise reset a platform admin's password and take the account), never
// on a user with a membership outside the caller's reach (a carrier admin cannot take an own-fleet admin who also
// belongs to the carrier), and on a tenant_admin only as a tenant_admin of that tenant (an own-fleet admin manages
// its carriers' members, not their admins). Platform admins still manage everyone.
func TestAdminOutranksTarget(t *testing.T) {
	w := newWorld(t)
	paID, taoID, taaID := w.userID("pa@example.test"), w.userID("ta.o@example.test"), w.userID("ta.a@example.test")
	opaID := w.userID("op.a@example.test")
	w.member(paID, w.O, "tenant_admin") // the D1 shape: platform_admin + own-fleet tenant_admin
	pa, tao, taa := w.login("pa@example.test"), w.login("ta.o@example.test"), w.login("ta.a@example.test")
	drv := w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile)
		VALUES ($1, 'self', 'Prasit', 'Dee', '0822222222') RETURNING id::text`, w.O)

	// (1) An own-fleet tenant_admin never acts on the platform admin, on any route that targets a user.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/v1/users/" + paID + "/password/temporary", map[string]any{}},
		{http.MethodPatch, "/v1/users/" + paID, map[string]any{"email": "attacker@evil.test"}},
		{http.MethodPost, "/v1/users/" + paID + "/invite", map[string]any{}},
		{http.MethodPost, "/v1/users/" + paID + "/disable", map[string]any{}},
		{http.MethodPost, "/v1/users/" + paID + "/enable", map[string]any{}},
		{http.MethodGet, "/v1/users/" + paID + "/sessions", nil},
		{http.MethodDelete, "/v1/users/" + paID + "/sessions", nil},
		{http.MethodPut, "/v1/users/" + paID + "/driver-link", map[string]any{"driverId": drv}},
		{http.MethodDelete, "/v1/users/" + paID + "/driver-link", nil},
		{http.MethodPut, "/v1/tenants/" + w.O + "/members/" + paID, map[string]any{"role": "user"}},
		{http.MethodDelete, "/v1/tenants/" + w.O + "/members/" + paID, nil},
	} {
		w.refusedFor("platform_target", "ta.o "+c.method+" "+c.path, func() resp { return w.send(c.method, c.path, tao, c.body) })
	}
	if e := w.id(`SELECT email::text FROM users WHERE id = $1`, paID); e != "pa@example.test" {
		t.Fatalf("the platform admin's email became %s", e)
	}
	expect(t, w.loginWith("pa@example.test", pw), http.StatusOK, "") // its password is unchanged

	// (2) pa and ta.o also hold a user membership in carrier A: A's admin reaches them but outranks neither.
	w.member(paID, w.A, "user")
	w.member(taoID, w.A, "user")
	w.refusedFor("platform_target", "ta.a resets pa", func() resp {
		return w.send(http.MethodPost, "/v1/users/"+paID+"/password/temporary", taa, map[string]any{})
	})
	w.refusedFor("platform_target", "ta.a revokes pa's sessions", func() resp {
		return w.send(http.MethodDelete, "/v1/users/"+paID+"/sessions", taa, nil)
	})
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/v1/users/" + taoID + "/password/temporary", map[string]any{}},
		{http.MethodPatch, "/v1/users/" + taoID, map[string]any{"email": "attacker@evil.test"}},
		{http.MethodPost, "/v1/users/" + taoID + "/disable", map[string]any{}},
		{http.MethodPost, "/v1/users/" + taoID + "/invite", map[string]any{}},
		{http.MethodDelete, "/v1/tenants/" + w.A + "/members/" + taoID, nil},
	} {
		w.refusedFor("outside_reach", "ta.a "+c.method+" "+c.path, func() resp { return w.send(c.method, c.path, taa, c.body) })
	}
	if e := w.id(`SELECT email::text FROM users WHERE id = $1`, taoID); e != "ta.o@example.test" {
		t.Fatalf("the own-fleet admin's email became %s", e)
	}

	// (3) An own-fleet admin manages A's members but not A's admin, on the account routes as on the role routes.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/v1/users/" + taaID + "/password/temporary", map[string]any{}},
		{http.MethodPatch, "/v1/users/" + taaID, map[string]any{"email": "attacker@evil.test"}},
		{http.MethodPost, "/v1/users/" + taaID + "/disable", map[string]any{}},
		{http.MethodPut, "/v1/tenants/" + w.A + "/members/" + taaID, map[string]any{"role": "operator"}},
	} {
		w.refusedFor("tenant_admin_only", "ta.o "+c.method+" "+c.path, func() resp { return w.send(c.method, c.path, tao, c.body) })
	}

	// Scope holders: a dispatcher grant is managed by a platform admin only, a customer scope by a steward.
	tad := w.user("ta.d@example.test")
	w.member(tad, w.D, "tenant_admin")
	w.refusedFor("platform_target", "D's admin resets its dispatcher", func() resp {
		return w.send(http.MethodPost, "/v1/users/"+w.userID("ds@example.test")+"/password/temporary", w.login("ta.d@example.test"), map[string]any{})
	})
	w.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'customer', $2)`, opaID, w.Y)
	w.refusedFor("steward_only", "ta.a resets a member holding a customer scope", func() resp {
		return w.send(http.MethodPost, "/v1/users/"+opaID+"/password/temporary", taa, map[string]any{})
	})

	// Positive controls: ta.o still resets A's operator (a steward manages the customer scope too), and the platform
	// admin manages the own-fleet admin and another platform-role holder.
	w.oneEvent("user_password_temporary_issued", func() resp {
		return w.send(http.MethodPost, "/v1/users/"+opaID+"/password/temporary", tao, map[string]any{})
	}, http.StatusOK)
	suID := w.userID("su@example.test")
	w.oneEvent("user_disabled", func() resp { return w.send(http.MethodPost, "/v1/users/"+suID+"/disable", pa, map[string]any{}) },
		http.StatusNoContent)
	w.oneEvent("user_enabled", func() resp { return w.send(http.MethodPost, "/v1/users/"+suID+"/enable", pa, map[string]any{}) },
		http.StatusNoContent)
	w.oneEvent("user_password_temporary_issued", func() resp {
		return w.send(http.MethodPost, "/v1/users/"+taoID+"/password/temporary", pa, map[string]any{})
	}, http.StatusOK)
	if n := w.count(`SELECT count(*) FROM user_platform_roles WHERE user_id = $1`, taoID); n != 0 {
		t.Fatalf("ta.o holds %d platform roles", n)
	}
}

// Review panel (T19): GET /v1/users/{id}/sessions is a read, so a platform admin reads it under X-Act-On-Tenant: *
// (one audit row), as GET /v1/users/{id}; without the header the user is outside its read reach (404), and * stays
// read-only for the revocation.
func TestSessionsPlatformRead(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	mgID := w.userID("mg.o@example.test")
	w.login("mg.o@example.test")
	audits := w.securityEvents("platform_cross_tenant_access")
	expect(t, w.get("/v1/users/"+mgID, pa, "X-Act-On-Tenant", "*"), http.StatusOK, "")
	r := w.get("/v1/users/"+mgID+"/sessions", pa, "X-Act-On-Tenant", "*")
	expect(t, r, http.StatusOK, "")
	if len(r.data().([]any)) == 0 {
		t.Fatal("no live session listed under X-Act-On-Tenant: *")
	}
	if got := w.securityEvents("platform_cross_tenant_access") - audits; got != 2 {
		t.Errorf("%d cross-tenant audit rows for two platform reads", got)
	}
	expect(t, w.get("/v1/users/"+mgID, pa), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/users/"+mgID+"/sessions", pa), http.StatusNotFound, "not_found")
	r = w.noEvent(func() resp {
		return w.send(http.MethodDelete, "/v1/users/"+mgID+"/sessions", pa, nil, "X-Act-On-Tenant", "*")
	})
	if r.status != http.StatusForbidden {
		t.Errorf("a revocation under X-Act-On-Tenant: *: %d %s", r.status, r.raw)
	}
}

// Review panel (T19): PUT /v1/users/{id}/driver-link never clears a driver row of a tenant outside the caller's
// reach: a user with a driver membership in carrier C is outside carrier A's admin (403 outside_reach), and a link
// to C's row whose membership is gone (an ETL-loaded link) is 409 linked_in_other_tenant; C's row stays linked.
func TestDriverLinkKeepsOtherTenantRow(t *testing.T) {
	w := newWorld(t)
	taa := w.login("ta.a@example.test")
	u := w.user("two.carriers@example.test")
	w.member(u, w.A, "driver")
	w.member(u, w.C, "driver")
	da := w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile)
		VALUES ($1, 'self', 'Anan', 'A', '0833333333') RETURNING id::text`, w.A)
	dc := w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, user_id)
		VALUES ($1, 'self', 'Chai', 'C', '0844444444', $2) RETURNING id::text`, w.C, u)
	linked := func() string { return w.id(`SELECT coalesce(user_id::text, '') FROM drivers WHERE id = $1`, dc) }
	w.refusedFor("outside_reach", "ta.a links a C driver's user", func() resp {
		return w.send(http.MethodPut, "/v1/users/"+u+"/driver-link", taa, map[string]any{"driverId": da})
	})
	w.exec(`DELETE FROM memberships WHERE user_id = $1 AND tenant_id = $2`, u, w.C)
	r := w.noEvent(func() resp {
		return w.send(http.MethodPut, "/v1/users/"+u+"/driver-link", taa, map[string]any{"driverId": da})
	})
	if r.status != http.StatusConflict || r.code() != "failed_precondition" || reason(r) != "linked_in_other_tenant" ||
		strings.Contains(string(r.raw), dc) {
		t.Errorf("link over another tenant's row: %d %s", r.status, r.raw)
	}
	if got := linked(); got != u {
		t.Errorf("C's driver row is linked to %q after A's refused link", got)
	}
}

// Review panel (T19): suspending a carrier changes every active member's claims at once (auth_version++,
// claims_changed: the old token is refused, the refresh drops the tid); reactivating it restores the tid after the
// next refresh. Exactly one tenant_updated row each time.
func TestTenantSuspensionClaims(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	access, refresh := w.tokens("ta.a@example.test")
	if who := w.whoami(access); who.TenantKind != "carrier" {
		t.Fatalf("before: %+v", who)
	}
	w.oneEvent("tenant_updated", func() resp {
		return w.send(http.MethodPatch, "/v1/tenants/"+w.A, pa, map[string]any{"status": "suspended", "reason": "contract ended"})
	}, http.StatusOK)
	if n := w.count(`SELECT (details->>'membersNotified')::int FROM security_events WHERE event_type = 'tenant_updated'`); n != 3 {
		t.Errorf("membersNotified %d, want A's three members", n)
	}
	r := w.get("/v1/me", access)
	if r.status != http.StatusUnauthorized || r.code() != "token_expired" || reason(r) != "claims_changed" {
		t.Fatalf("the suspended tenant's token answers %d %s", r.status, r.raw)
	}
	r = w.send(http.MethodPost, "/v1/auth/refresh", "", map[string]any{"refreshToken": refresh})
	expect(t, r, http.StatusOK, "")
	access, refresh = r.data().(map[string]any)["accessToken"].(string), r.data().(map[string]any)["refreshToken"].(string)
	if who := w.whoami(access); who.TenantKind != "" || slices.Contains(who.Caps, "users:manage") {
		t.Errorf("after the suspension: %+v", who)
	}
	expect(t, w.get("/v1/users", access), http.StatusForbidden, "")
	w.oneEvent("tenant_updated", func() resp {
		return w.send(http.MethodPatch, "/v1/tenants/"+w.A, pa, map[string]any{"status": "active"})
	}, http.StatusOK)
	r = w.get("/v1/me", access)
	if r.status != http.StatusUnauthorized || reason(r) != "claims_changed" {
		t.Fatalf("after the reactivation the old token answers %d %s", r.status, r.raw)
	}
	r = w.send(http.MethodPost, "/v1/auth/refresh", "", map[string]any{"refreshToken": refresh})
	expect(t, r, http.StatusOK, "")
	if who := w.whoami(r.data().(map[string]any)["accessToken"].(string)); who.TenantKind != "carrier" {
		t.Errorf("after the reactivation: %+v", who)
	}
	// A rename changes no claim: nobody is bumped.
	v := w.count(`SELECT auth_version FROM users WHERE email = 'op.a@example.test'`)
	expect(t, w.send(http.MethodPatch, "/v1/tenants/"+w.A, pa, map[string]any{"nameTh": "เอ"}), http.StatusOK, "")
	if got := w.count(`SELECT auth_version FROM users WHERE email = 'op.a@example.test'`); got != v {
		t.Errorf("a rename bumped auth_version %d -> %d", v, got)
	}
}

// Review panel (T19): seed bootstrap-platform-admins grants platform_admin only to an account whose address is
// proven (imported from Firebase, set by the bootstrap or by a platform admin) and that belongs to no carrier and
// holds no scope: a carrier tenant_admin that creates, or renames a user to, an address listed in
// PLATFORM_ADMIN_EMAILS before its owner has an account gets nothing.
func TestBootstrapEligibility(t *testing.T) {
	w := newWorld(t)
	tac, pa := w.login("ta.c@example.test"), w.login("pa@example.test")
	created := func(bearer string, body map[string]any) string {
		t.Helper()
		r := w.send(http.MethodPost, "/v1/users", bearer, body)
		expect(t, r, http.StatusCreated, "")
		return r.data().(map[string]any)["user"].(map[string]any)["id"].(string)
	}
	// Claimed by carrier C's admin: created with the address, or renamed to it.
	claimed := created(tac, map[string]any{"email": "new.ops@example.test", "displayName": "x", "role": "operator"})
	renamed := created(tac, map[string]any{"email": "harmless@example.test", "displayName": "y", "role": "operator"})
	expect(t, w.send(http.MethodPatch, "/v1/users/"+renamed, tac, map[string]any{"email": "ops2@example.test"}), http.StatusOK, "")
	// Set by the platform admin: an own-fleet account (eligible) and a carrier account (refused).
	byPlatform := created(pa, map[string]any{"email": "ops3@example.test", "displayName": "z", "role": "operator", "tenantId": w.O})
	inCarrier := created(pa, map[string]any{"email": "ops4@example.test", "displayName": "c", "role": "operator", "tenantId": w.C})
	// Renamed by the platform admin after a carrier admin created it in C, then moved to the own fleet: eligible.
	moved := created(tac, map[string]any{"email": "draft@example.test", "displayName": "m", "role": "operator"})
	expect(t, w.send(http.MethodPatch, "/v1/users/"+moved, pa, map[string]any{"email": "ops5@example.test"}), http.StatusOK, "")
	expect(t, w.send(http.MethodPut, "/v1/tenants/"+w.O+"/members/"+moved, pa, map[string]any{"role": "operator"}), http.StatusOK, "")
	expect(t, w.send(http.MethodDelete, "/v1/tenants/"+w.C+"/members/"+moved, pa, nil), http.StatusNoContent, "")

	var res iam.PlatformAdminsResult
	if err := db.WithSystem(context.Background(), w.pool, nil, func(tx pgx.Tx) (err error) {
		res, err = iam.BootstrapPlatformAdmins(context.Background(), tx, []string{"new.ops@example.test", "ops2@example.test",
			"ops3@example.test", "ops4@example.test", "ops5@example.test", "pa@example.test"}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	refused := map[string]string{}
	for _, r := range res.Refused {
		refused[r.UserID.String()] = r.Reason
	}
	if !slices.Equal(res.Granted, []string{"ops3@example.test", "ops5@example.test"}) || !slices.Equal(res.Held, []string{"pa@example.test"}) ||
		refused[claimed] != iam.RefuseAddressUnproven || refused[renamed] != iam.RefuseAddressUnproven ||
		refused[inCarrier] != iam.RefuseOtherTenant || len(refused) != 3 {
		t.Fatalf("bootstrap: %+v", res)
	}
	for _, id := range []string{claimed, renamed, inCarrier} {
		if n := w.count(`SELECT count(*) FROM user_platform_roles WHERE user_id = $1`, id); n != 0 {
			t.Errorf("the refused user %s holds %d platform roles", id, n)
		}
	}
	if n := w.count(`SELECT count(*) FROM user_platform_roles WHERE user_id = ANY ($1::uuid[])`, []string{byPlatform, moved}); n != 2 {
		t.Errorf("%d platform roles for the two eligible users", n)
	}
}
