//go:build integration

// Integration tests of T62 (#139): one user in several organisations. Carriers A and B of the world both work for
// the own fleet O, so ta.a reaches A only and ta.b (made here) B only; every request runs as logitrack_app.
package iam_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// loginBody signs in with pw and returns the data of POST /v1/auth/login.
func (w *world) loginBody(email string) map[string]any {
	w.t.Helper()
	r := w.loginWith(email, pw)
	expect(w.t, r, http.StatusOK, "")
	return r.data().(map[string]any)
}

// claimsOf decodes the payload of an access token (no verification: the API verified it).
func claimsOf(t *testing.T, access string) map[string]any {
	t.Helper()
	parts := strings.Split(access, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", access)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// switchTo is POST /v1/auth/tenant; it returns the new access token.
func (w *world) switchTo(access, tenant string) string {
	w.t.Helper()
	r := w.send(http.MethodPost, "/v1/auth/tenant", access, map[string]any{"tenantId": tenant})
	expect(w.t, r, http.StatusOK, "")
	return r.data().(map[string]any)["accessToken"].(string)
}

// onlyTenant asserts that RLS returns the tasks of tenant alone to access (two per tenant in the fixture).
func (w *world) onlyTenant(access, tenant, label string) {
	w.t.Helper()
	got := w.perTenant(access, "")
	if len(got) != 1 || got[tenant] != 2 {
		w.t.Fatalf("%s: RLS returns %v, want the 2 tasks of %s only", label, named(w, got), name(w, tenant))
	}
}

// AC1: a user with memberships in A and B lands in the remembered tenant (else the earliest active membership),
// switches without signing out, and each switch changes what RLS returns; a suspended membership or tenant is never
// chosen; every switch records tenant_switched on the new tenant only.
func TestMultiOrgSignInAndSwitch(t *testing.T) {
	w := newWorld(t)
	sh := w.user("shared@example.test")
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role, created_at) VALUES
		($1, $2, 'operator', now() - interval '2 days'), ($1, $3, 'user', now() - interval '1 day')`, sh, w.B, w.A)

	// First sign-in: nothing remembered (no session yet), so the earliest membership: B.
	body := w.loginBody("shared@example.test")
	if body["defaultTenantId"] != w.B {
		t.Fatalf("first sign-in opens %v, want B (the earliest membership)", body["defaultTenantId"])
	}
	if ts, _ := body["tenants"].([]any); len(ts) != 2 {
		t.Fatalf("login lists %d tenants, want 2: %v", len(ts), body["tenants"])
	}
	access := body["accessToken"].(string)
	w.onlyTenant(access, w.B, "signed in to B")

	// Switch to A without signing out: tid, the session and RLS follow; tenant_switched names A only.
	before := w.securityEvents("tenant_switched")
	access = w.switchTo(access, w.A)
	if c := claimsOf(t, access); c["tid"] != w.A || c["rol"] != "user" {
		t.Fatalf("after the switch tid=%v rol=%v", c["tid"], c["rol"])
	}
	w.onlyTenant(access, w.A, "switched to A")
	if n := w.securityEvents("tenant_switched"); n != before+1 {
		t.Fatalf("%d tenant_switched rows appended, want 1", n-before)
	}
	if got := w.id(`SELECT tenant_id::text FROM security_events WHERE event_type = 'tenant_switched' ORDER BY created_at DESC, id DESC LIMIT 1`); got != w.A {
		t.Fatalf("tenant_switched is filed under %s, want A", name(w, got))
	}
	if strings.Contains(w.id(`SELECT details::text FROM security_events WHERE event_type = 'tenant_switched' ORDER BY created_at DESC, id DESC LIMIT 1`), w.B) {
		t.Fatal("tenant_switched names the tenant left")
	}
	// Switching to the active tenant again records nothing; a tenant without a membership is 403.
	access = w.switchTo(access, w.A)
	if n := w.securityEvents("tenant_switched"); n != before+1 {
		t.Fatal("a switch to the active tenant recorded tenant_switched")
	}
	expect(t, w.send(http.MethodPost, "/v1/auth/tenant", access, map[string]any{"tenantId": w.C}), http.StatusForbidden, "permission_denied")

	// The next sign-in (a new session) opens in the remembered tenant A, and switches back to B.
	body = w.loginBody("shared@example.test")
	if body["defaultTenantId"] != w.A {
		t.Fatalf("second sign-in opens %v, want the remembered A", body["defaultTenantId"])
	}
	second := w.switchTo(body["accessToken"].(string), w.B)
	w.onlyTenant(second, w.B, "switched back to B")
	w.onlyTenant(access, w.A, "the first session stays in A") // sessions keep their own tenant
	if got := w.id(`SELECT last_tenant_id::text FROM users WHERE id = $1`, sh); got != w.B {
		t.Fatalf("users.last_tenant_id = %s, want B", name(w, got))
	}

	// A suspended membership is never chosen: B's admin suspends the B membership, the remembered B is skipped.
	tab := w.user("ta.b@example.test")
	w.member(tab, w.B, "tenant_admin")
	w.oneEvent("membership_role_changed", func() resp {
		return w.send(http.MethodPut, "/v1/tenants/"+w.B+"/members/"+sh, w.login("ta.b@example.test"), map[string]any{"status": "suspended"})
	}, http.StatusOK)
	if st := w.id(`SELECT status FROM memberships WHERE user_id = $1 AND tenant_id = $2`, sh, w.B); st != "suspended" {
		t.Fatalf("B membership %s", st)
	}
	body = w.loginBody("shared@example.test")
	if body["defaultTenantId"] != w.A {
		t.Fatalf("with B suspended the sign-in opens %v, want A", body["defaultTenantId"])
	}
	if ts, _ := body["tenants"].([]any); len(ts) != 1 {
		t.Fatalf("a suspended membership is listed: %v", body["tenants"])
	}
	expect(t, w.send(http.MethodPost, "/v1/auth/tenant", body["accessToken"].(string), map[string]any{"tenantId": w.B}),
		http.StatusForbidden, "permission_denied")

	// Reactivated, then the tenant A suspended: the remembered A is skipped for B.
	expect(t, w.send(http.MethodPut, "/v1/tenants/"+w.B+"/members/"+sh, w.login("ta.b@example.test"), map[string]any{"status": "active"}),
		http.StatusOK, "")
	w.exec(`UPDATE tenants SET status = 'suspended' WHERE id = $1`, w.A)
	if got := w.loginBody("shared@example.test")["defaultTenantId"]; got != w.B {
		t.Fatalf("with A suspended the sign-in opens %v, want B", got)
	}
}

// AC2: a tenant_admin of B adds by email an existing user of A, and an unknown address; both answers look the same and
// neither reveals A. Plus the refusals of the route.
func TestMultiOrgAddMemberByEmail(t *testing.T) {
	w := newWorld(t)
	tab := w.user("ta.b@example.test")
	w.member(tab, w.B, "tenant_admin")
	taB := w.login("ta.b@example.test")
	opa := w.userID("op.a@example.test")
	path := "/v1/tenants/" + w.B + "/members"

	existing := w.oneEvent("membership_added", func() resp {
		return w.send(http.MethodPost, path, taB, map[string]any{"email": " OP.A@example.test ", "role": "manager"})
	}, http.StatusCreated)
	// A new account: membership_added (B's row, the same as above) and a platform-level user_created.
	added, created := w.securityEvents("membership_added"), w.securityEvents("user_created")
	fresh := w.send(http.MethodPost, path, taB, map[string]any{"email": "newcomer@example.test", "role": "operator", "locale": "th"})
	expect(t, fresh, http.StatusCreated, "")
	if w.securityEvents("membership_added") != added+1 || w.securityEvents("user_created") != created+1 {
		t.Fatal("a new account is not recorded as membership_added + user_created")
	}
	keysOfData := func(r resp) []string {
		d := r.data().(map[string]any)
		var ks []string
		for k := range d {
			ks = append(ks, k)
		}
		slices.Sort(ks)
		return ks
	}
	if a, b := keysOfData(existing), keysOfData(fresh); !slices.Equal(a, b) || !slices.Equal(a, []string{"role", "status", "tenantId", "userId"}) {
		t.Fatalf("the two answers differ in shape: %v vs %v", a, b)
	}
	for _, r := range []resp{existing, fresh} {
		if strings.Contains(string(r.raw), w.A) || strings.Contains(string(r.raw), "op.a") {
			t.Fatalf("the answer reveals A or the account: %s", r.raw)
		}
	}
	if d := existing.data().(map[string]any); d["userId"] != opa || d["tenantId"] != w.B || d["role"] != "manager" || d["status"] != "active" {
		t.Fatalf("existing: %v", d)
	}
	if role := w.id(`SELECT role FROM memberships WHERE user_id = $1 AND tenant_id = $2`, opa, w.B); role != "manager" {
		t.Fatalf("B membership %s", role)
	}
	if role := w.id(`SELECT role FROM memberships WHERE user_id = $1 AND tenant_id = $2`, opa, w.A); role != "operator" {
		t.Fatalf("the A membership changed: %s", role)
	}
	// The existing account's clients refresh (claims_changed): auth_version moved; no account was created or invited.
	if v := w.count(`SELECT auth_version FROM users WHERE id = $1`, opa); v != 2 {
		t.Fatalf("auth_version %d", v)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'user.created' AND aggregate_id = $1`, opa); n != 0 {
		t.Fatal("an existing account was invited")
	}
	// The new account: no password, an invite (reset link) queued, a platform-level user_created.
	newID := fresh.data().(map[string]any)["userId"].(string)
	if n := w.count(`SELECT count(*) FROM users WHERE id = $1 AND password_hash IS NULL AND NOT must_change_password
		AND display_name = 'newcomer'`, newID); n != 1 {
		t.Fatal("the new account is not a password-less invite account")
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'user.created' AND aggregate_id = $1
		AND payload->>'purpose' = 'invite' AND (payload->>'sendInvite')::boolean AND payload->>'locale' = 'th'`, newID); n != 1 {
		t.Fatal("no invite queued for the new account")
	}
	if n := w.count(`SELECT count(*) FROM security_events WHERE event_type = 'user_created' AND target_user_id = $1
		AND tenant_id IS NULL`, newID); n != 1 {
		t.Fatal("the new account has no platform-level user_created row")
	}
	// What B's staff read about the shared user: B only.
	r := w.get("/v1/users/"+opa, taB)
	expect(t, r, http.StatusOK, "")
	u := r.data().(map[string]any)
	if ms := u["memberships"].([]any); len(ms) != 1 || ms[0].(map[string]any)["tenantId"] != w.B {
		t.Fatalf("B reads the memberships %v", ms)
	}
	if strings.Contains(string(r.raw), w.A) {
		t.Fatalf("GET /v1/users/{id} reveals A to B: %s", r.raw)
	}
	if u["manageAccount"] != false || u["accountReason"] != "outside_reach" {
		t.Fatalf("manageAccount=%v accountReason=%v for a shared user", u["manageAccount"], u["accountReason"])
	}
	if r := w.get("/v1/users/"+newID, taB); r.data().(map[string]any)["manageAccount"] != true {
		t.Fatalf("B does not manage the account it created: %s", r.raw)
	}
	if strings.Contains(w.id(`SELECT coalesce(string_agg(details::text, ' '), '') FROM security_events WHERE tenant_id = $1`, w.B), w.A) {
		t.Fatal("B's security events name A")
	}
	// B's security feed lists the additions.
	r = w.get("/v1/security-events?type=membership_added", taB)
	expect(t, r, http.StatusOK, "")
	if n := len(r.data().([]any)); n != 2 {
		t.Fatalf("B's feed lists %d membership_added rows, want 2", n)
	}

	// Refusals: already a member (409), a driver (422), the caller (403), a tenant outside the reach (403), a caller
	// without users:manage (403), tenant_admin granted by an own-fleet admin in B (403 tenant_admin_only).
	expect(t, w.noEvent(func() resp {
		return w.send(http.MethodPost, path, taB, map[string]any{"email": "op.a@example.test", "role": "user"})
	}), http.StatusConflict, "already_exists")
	expect(t, w.noEvent(func() resp {
		return w.send(http.MethodPost, path, taB, map[string]any{"email": "x@example.test", "role": "driver"})
	}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.noEvent(func() resp {
		return w.send(http.MethodPost, path, taB, map[string]any{"email": "not-an-email", "role": "user"})
	}), http.StatusUnprocessableEntity, "invalid_argument")
	w.refusedFor("self", "ta.o adds itself to B", func() resp {
		return w.send(http.MethodPost, "/v1/tenants/"+w.B+"/members", w.login("ta.o@example.test"), map[string]any{"email": "ta.o@example.test", "role": "user"})
	})
	expect(t, w.noEvent(func() resp {
		return w.send(http.MethodPost, "/v1/tenants/"+w.C+"/members", taB, map[string]any{"email": "y@example.test", "role": "user"})
	}), http.StatusForbidden, "permission_denied")
	expect(t, w.noEvent(func() resp {
		return w.send(http.MethodPost, "/v1/tenants/"+w.A+"/members", w.login("mg.a@example.test"), map[string]any{"email": "z@example.test", "role": "user"})
	}), http.StatusForbidden, "permission_denied")
	w.refusedFor("tenant_admin_only", "ta.o grants tenant_admin in B", func() resp {
		return w.send(http.MethodPost, path, w.login("ta.o@example.test"), map[string]any{"email": "w@example.test", "role": "tenant_admin"})
	})
	if n := w.count(`SELECT count(*) FROM users WHERE email IN ('x@example.test','y@example.test','z@example.test','w@example.test')`); n != 0 {
		t.Fatalf("a refused add created %d accounts", n)
	}
}

// AC3 and the per-organisation administration: B's admin manages the B membership of a user who also belongs to A
// (role, suspend, remove) but none of the account-wide actions (403 outside_reach); A's admin likewise.
func TestMultiOrgPerTenantAdministration(t *testing.T) {
	w := newWorld(t)
	tab := w.user("ta.b@example.test")
	w.member(tab, w.B, "tenant_admin")
	taB, taA := w.login("ta.b@example.test"), w.login("ta.a@example.test")
	sh := w.userID("op.a@example.test")
	expect(t, w.send(http.MethodPost, "/v1/tenants/"+w.B+"/members", taB, map[string]any{"email": "op.a@example.test", "role": "operator"}),
		http.StatusCreated, "")

	for _, admin := range []struct{ label, token string }{{"ta.b", taB}, {"ta.a", taA}} {
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{http.MethodPost, "/v1/users/" + sh + "/password/temporary", map[string]any{}},
			{http.MethodPatch, "/v1/users/" + sh, map[string]any{"email": "attacker@evil.test"}},
			{http.MethodPost, "/v1/users/" + sh + "/disable", map[string]any{}},
			{http.MethodPost, "/v1/users/" + sh + "/invite", map[string]any{}},
			{http.MethodDelete, "/v1/users/" + sh + "/sessions", nil},
		} {
			w.refusedFor("outside_reach", admin.label+" "+c.method+" "+c.path, func() resp { return w.send(c.method, c.path, admin.token, c.body) })
		}
		r := w.get("/v1/users/"+sh, admin.token)
		if u := r.data().(map[string]any); u["manageAccount"] != false || u["accountReason"] != "outside_reach" {
			t.Errorf("%s reads manageAccount=%v reason=%v", admin.label, u["manageAccount"], u["accountReason"])
		}
	}
	if e := w.id(`SELECT email::text FROM users WHERE id = $1`, sh); e != "op.a@example.test" {
		t.Fatalf("the shared user's email became %s", e)
	}
	expect(t, w.loginWith("op.a@example.test", pw), http.StatusOK, "") // password and status unchanged
	// The own fleet reaches A and B, so its admin keeps the account-wide actions.
	if u := w.get("/v1/users/"+sh, w.login("ta.o@example.test")).data().(map[string]any); u["manageAccount"] != true {
		t.Errorf("the own-fleet admin reads manageAccount=%v reason=%v", u["manageAccount"], u["accountReason"])
	}

	// The B membership is B's: role, suspend, reactivate, remove; A's membership never moves.
	w.oneEvent("membership_role_changed", func() resp {
		return w.send(http.MethodPut, "/v1/tenants/"+w.B+"/members/"+sh, taB, map[string]any{"role": "manager"})
	}, http.StatusOK)
	w.oneEvent("membership_role_changed", func() resp {
		return w.send(http.MethodPut, "/v1/tenants/"+w.B+"/members/"+sh, taB, map[string]any{"status": "suspended"})
	}, http.StatusOK)
	if got := w.id(`SELECT role || '/' || status FROM memberships WHERE user_id = $1 AND tenant_id = $2`, sh, w.B); got != "manager/suspended" {
		t.Fatalf("B membership %s", got)
	}
	expect(t, w.send(http.MethodPut, "/v1/tenants/"+w.B+"/members/"+sh, taB, map[string]any{"status": "paused"}),
		http.StatusUnprocessableEntity, "invalid_argument")
	// A cannot touch the B membership, nor B the A membership.
	expect(t, w.send(http.MethodPut, "/v1/tenants/"+w.B+"/members/"+sh, taA, map[string]any{"role": "user"}), http.StatusForbidden, "permission_denied")
	expect(t, w.send(http.MethodDelete, "/v1/tenants/"+w.A+"/members/"+sh, taB, nil), http.StatusForbidden, "permission_denied")
	w.oneEvent("membership_removed", func() resp {
		return w.send(http.MethodDelete, "/v1/tenants/"+w.B+"/members/"+sh, taB, nil)
	}, http.StatusNoContent)
	if got := w.id(`SELECT role || '/' || status FROM memberships WHERE user_id = $1 AND tenant_id = $2`, sh, w.A); got != "operator/active" {
		t.Fatalf("A membership %s", got)
	}
	// Once the user belongs to A alone, A's admin manages the account again.
	if u := w.get("/v1/users/"+sh, taA).data().(map[string]any); u["manageAccount"] != true {
		t.Errorf("after the removal ta.a reads manageAccount=%v reason=%v", u["manageAccount"], u["accountReason"])
	}
	// A platform role holder stays a platform admin's: B's admin cannot change its B membership.
	pa := w.userID("pa@example.test")
	w.member(pa, w.B, "user")
	w.refusedFor("platform_target", "ta.b suspends pa in B", func() resp {
		return w.send(http.MethodPut, "/v1/tenants/"+w.B+"/members/"+pa, taB, map[string]any{"status": "suspended"})
	})
}

// AC4: removing the last active membership leaves the account signed out of tenant data, with no tenant claims.
func TestMultiOrgLastMembershipRemoved(t *testing.T) {
	w := newWorld(t)
	tab := w.user("ta.b@example.test")
	w.member(tab, w.B, "tenant_admin")
	taB := w.login("ta.b@example.test")
	solo := w.user("solo@example.test")
	w.member(solo, w.B, "operator")
	access, refresh := w.tokens("solo@example.test")
	w.onlyTenant(access, w.B, "signed in to B")

	w.oneEvent("membership_removed", func() resp {
		return w.send(http.MethodDelete, "/v1/tenants/"+w.B+"/members/"+solo, taB, nil)
	}, http.StatusNoContent)
	r := w.get("/v1/me", access)
	if r.status != http.StatusUnauthorized || reason(r) != "claims_changed" {
		t.Fatalf("the old token answers %d %s, want 401 claims_changed", r.status, r.raw)
	}
	r = w.send(http.MethodPost, "/v1/auth/refresh", "", map[string]any{"refreshToken": refresh})
	expect(t, r, http.StatusOK, "")
	fresh := r.data().(map[string]any)["accessToken"].(string)
	c := claimsOf(t, fresh)
	for _, k := range []string{"tid", "rol", "drv"} {
		if _, ok := c[k]; ok {
			t.Fatalf("the refreshed token still carries %s: %v", k, c)
		}
	}
	if got := w.perTenant(fresh, ""); len(got) != 0 {
		t.Fatalf("RLS returns %v to an account without a membership", named(w, got))
	}
	expect(t, w.get("/v1/rbactest/rate-card", fresh), http.StatusForbidden, "")
	if ts := w.loginBody("solo@example.test"); ts["defaultTenantId"] != nil || len(ts["tenants"].([]any)) != 0 {
		t.Fatalf("a sign-in without memberships: %v", ts)
	}
}
