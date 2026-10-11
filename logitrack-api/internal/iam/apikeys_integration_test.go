//go:build integration

// Acceptance tests of the API-keys slice of issue T32 (#52, T32K): GET / POST / DELETE /v1/api-keys on the
// internal listener (Appendix B §B.2.6, Appendix C §C.4.11), the secret shown once and stored as
// sha256(secret ‖ API_KEY_PEPPER), never listed or logged, a scope outside the CHECK list 422, a revoked key
// 401, and the scope routing of integration, script and release_publisher keys (R82). Requests run as
// logitrack_app (pgtest), so every api_keys access goes through the app role and its RLS.
package iam_test

import (
	"bytes"
	"crypto/sha256"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/apikey"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// syncBuffer collects log lines from concurrent requests.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// keyTestGroups are routes that admit API keys, as later domains will register them: integration and script
// keys on /v1/keytest/* (capability-guarded, SQL under db.WithPrincipal), release_publisher keys on
// /v1/keytest/publish, and a public route that admits integration keys (which the public listener refuses).
func (w *world) keyTestGroups(svc *auth.Service) []ingress.Group {
	machine := svc.RequireAuth(auth.AllowAPIKeys(authz.KeyIntegration, authz.KeyScript))
	publisher := svc.RequireAuth(auth.AllowAPIKeys(authz.KeyReleasePublisher))
	ok := func(c fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) }
	return []ingress.Group{
		{Prefix: "/v1/keytest", Mount: func(r fiber.Router) {
			r.Get("/whoami", machine, func(c fiber.Ctx) error {
				p := authz.PrincipalFrom(c)
				out := map[string]any{"caps": p.Caps.Strings(), "steward": p.Steward, "amr": p.AMR,
					"scope": string(p.APIKeyScope), "tenantKind": p.TenantKind}
				return httpx.JSON(c, http.StatusOK, out)
			})
			r.Get("/users-view", machine, authz.RequireCap(authz.UsersView), ok)
			r.Get("/tasks", machine, authz.RequireCap(authz.OperationsViewFirstMile), func(c fiber.Ctx) error {
				perTenant := map[string]int{}
				err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
					rows, err := tx.Query(c.Context(), `SELECT tenant_id::text, count(*) FROM tasks GROUP BY 1`)
					if err != nil {
						return err
					}
					for rows.Next() {
						var tid string
						var n int
						if err := rows.Scan(&tid, &n); err != nil {
							return err
						}
						perTenant[tid] = n
					}
					return rows.Err()
				})
				if err != nil {
					return err
				}
				return httpx.JSON(c, http.StatusOK, perTenant)
			})
			r.Post("/customers", machine, authz.RequireCap(authz.FleetManageCustomers), func(c fiber.Ctx) error {
				var id string
				err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
					return tx.QueryRow(c.Context(), `INSERT INTO customers (code, name) VALUES ($1::text, $1::text)
						RETURNING id::text`, c.Query("code")).Scan(&id)
				})
				if err != nil {
					return err
				}
				return httpx.JSON(c, http.StatusCreated, map[string]string{"id": id})
			})
			r.Post("/publish", publisher, authz.RequireCap(authz.SecurityManageMobileRel), ok)
		}},
		{Prefix: "/v1/mobile", Public: true, Mount: func(r fiber.Router) {
			r.Get("/keytest", machine, ok)
		}},
	}
}

// keyed is a request with X-Api-Key on the internal listener.
func (w *world) keyed(method, path, secret string, headers ...string) resp {
	w.t.Helper()
	return w.call(w.internal, method, path, "", append([]string{auth.HeaderAPIKey, secret}, headers...)...)
}

// createKey is POST /v1/api-keys as bearer.
func (w *world) createKey(bearer string, body map[string]any, headers ...string) resp {
	w.t.Helper()
	return w.send(http.MethodPost, "/v1/api-keys", bearer, body, headers...)
}

// mustKey creates a key and returns its id and secret.
func (w *world) mustKey(bearer string, body map[string]any) (id, secret string) {
	w.t.Helper()
	r := w.createKey(bearer, body)
	expect(w.t, r, http.StatusCreated, "")
	d := r.data().(map[string]any)
	return d["id"].(string), d["secret"].(string)
}

func keyIDs(r resp) []string {
	var out []string
	for _, v := range r.data().([]any) {
		out = append(out, v.(map[string]any)["id"].(string))
	}
	slices.Sort(out)
	return out
}

func sorted(ids ...string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

func fieldReasons(r resp) map[string][]string {
	out := map[string][]string{}
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	fs, _ := d["fields"].([]any)
	for _, f := range fs {
		m := f.(map[string]any)
		out[m["field"].(string)] = append(out[m["field"].(string)], m["reason"].(string))
	}
	return out
}

// AC: the secret is shown once (POST, no-store), stored as sha256(secret ‖ API_KEY_PEPPER), never in a list
// response, in an audit row or in a log line; one api_key_created row per key.
func TestAPIKeySecretShownOnceStoredHashed(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")

	r := w.oneEvent(iam.EventAPIKeyCreated, func() resp {
		return w.createKey(ta, map[string]any{"name": "billing export", "scope": "integration",
			"capabilities": []string{"users:view", "operations:view_first_mile"}, "tenantId": w.O})
	}, http.StatusCreated)
	d := r.data().(map[string]any)
	secret, id, prefix := d["secret"].(string), d["id"].(string), d["keyPrefix"].(string)
	if p, ok := apikey.Prefix(secret); !ok || p != prefix || !strings.HasPrefix(secret, "ltk_"+prefix+"_") {
		t.Fatalf("secret %q does not carry its prefix %q", "[redacted]", prefix)
	}
	if d["status"] != "active" || d["scope"] != "integration" || d["tenantId"] != w.O ||
		strings.Join(toStrings(d["capabilities"]), ",") != "operations:view_first_mile,users:view" {
		t.Fatalf("created key: %s", strings.ReplaceAll(string(r.raw), secret, "[secret]"))
	}

	// Stored hashed: key_hash = sha256(secret || pepper); no column holds the secret.
	var hash []byte
	var row string
	if err := w.etl.QueryRow(t.Context(), `SELECT key_hash, k::text FROM api_keys k WHERE id = $1`, id).Scan(&hash, &row); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(secret + testPepper))
	if !bytes.Equal(hash, want[:]) {
		t.Fatal("key_hash is not sha256(secret || API_KEY_PEPPER)")
	}
	body := secret[len("ltk_")+apikey.PrefixLen+1:]
	if strings.Contains(row, body) {
		t.Fatal("the api_keys row holds the secret")
	}
	if n := w.count(`SELECT count(*) FROM security_events WHERE details::text LIKE '%' || $1 || '%'`, body); n != 0 {
		t.Fatal("an audit row holds the secret")
	}

	// Listed without the secret or its hash.
	l := w.get("/v1/api-keys", ta)
	expect(t, l, http.StatusOK, "")
	if !slices.Contains(keyIDs(l), id) {
		t.Fatalf("the new key is not listed: %s", l.raw)
	}
	for _, leak := range []string{body, "secret", "keyHash", "key_hash"} {
		if bytes.Contains(l.raw, []byte(leak)) {
			t.Fatalf("the list response contains %q", leak)
		}
	}

	// The key works, then a refused use is logged without the secret.
	expect(t, w.keyed(http.MethodGet, "/v1/roles", secret), http.StatusOK, "")
	expect(t, w.keyed(http.MethodGet, "/v1/users", secret), http.StatusForbidden, authz.CodePermissionDenied)
	if strings.Contains(w.logs.String(), body) {
		t.Fatal("a log line holds the secret")
	}
	if !strings.Contains(w.logs.String(), prefix) {
		t.Fatal("the refusal was not logged with the key prefix")
	}
	if cc := w.createKeyHeaders(ta); cc != "no-store" {
		t.Fatalf("POST /v1/api-keys Cache-Control = %q, want no-store", cc)
	}
}

// createKeyHeaders creates a key and returns the Cache-Control header of the response.
func (w *world) createKeyHeaders(bearer string) string {
	w.t.Helper()
	req, err := http.NewRequest(http.MethodPost, w.internal+"/v1/api-keys",
		strings.NewReader(`{"name":"cc","scope":"script","capabilities":["users:view"],"tenantId":"`+w.O+`"}`))
	if err != nil {
		w.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		w.t.Fatalf("create: %d", res.StatusCode)
	}
	return res.Header.Get("Cache-Control")
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// AC: a scope outside the CHECK list is 422, as is every other invalid body; nothing is written.
func TestAPIKeyCreateValidation(t *testing.T) {
	w := newWorld(t)
	ta, pa := w.login("ta.o@example.test"), w.login("pa@example.test")
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	cases := map[string]struct {
		bearer string
		body   map[string]any
		field  string
		reason string
	}{
		"scope outside the CHECK list": {ta, map[string]any{"name": "x", "scope": "admin", "capabilities": []string{"users:view"}, "tenantId": w.O}, "scope", "one_of"},
		"scope missing":                {ta, map[string]any{"name": "x", "capabilities": []string{"users:view"}, "tenantId": w.O}, "scope", "one_of"},
		"name missing":                 {ta, map[string]any{"scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.O}, "name", "required"},
		"name with NUL":                {pa, map[string]any{"name": "a\x00b", "scope": "script", "capabilities": []string{"waitlist:view"}}, "name", "invalid"},
		"tenant key name with NUL":     {ta, map[string]any{"name": "a\x00b", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.O}, "name", "invalid"},
		"name with control character":  {pa, map[string]any{"name": "a\x01b\nc", "scope": "script", "capabilities": []string{"waitlist:view"}}, "name", "invalid"},
		"no capabilities":              {ta, map[string]any{"name": "x", "scope": "script", "capabilities": []string{}, "tenantId": w.O}, "capabilities", "required"},
		"unknown capability":           {ta, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"users:everything"}, "tenantId": w.O}, "capabilities", "unknown"},
		"platform capability":          {pa, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"platform:manage_tenants"}}, "capabilities", "not_allowed"},
		"dispatch capability":          {pa, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"dispatch:view_operations"}}, "capabilities", "not_allowed"},
		"global key on a tenant key":   {ta, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"fleet:manage_customers"}, "tenantId": w.O}, "capabilities", "not_allowed_for_tenant_key"},
		"tenant key on a platform key": {pa, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"users:view"}}, "capabilities", "not_allowed_for_platform_key"},
		"cf_shim tenant key":           {pa, map[string]any{"name": "x", "scope": "cf_shim", "capabilities": []string{"mobile:view_tasks"}, "tenantId": w.O}, "tenantId", "platform_key_required"},
		"cf_shim non-mobile key":       {pa, map[string]any{"name": "x", "scope": "cf_shim", "capabilities": []string{"fleet:manage_customers"}}, "capabilities", "not_allowed_for_scope"},
		"release_publisher other key":  {pa, map[string]any{"name": "x", "scope": "release_publisher", "capabilities": []string{"waitlist:view"}}, "capabilities", "not_allowed_for_scope"},
		"expiry in the past":           {ta, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.O, "expiresAt": past}, "expiresAt", "in_past"},
		"expiry not RFC 3339":          {ta, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.O, "expiresAt": "tomorrow"}, "expiresAt", "invalid"},
		"tenant id not a uuid":         {pa, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": "O"}, "tenantId", "invalid"},
		"unknown tenant":               {pa, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": "01900000-0000-7000-8000-000000000001"}, "tenantId", "not_found"},
		"quarantine tenant":            {pa, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.Q}, "tenantId", "invalid"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := w.noEvent(func() resp { return w.createKey(tc.bearer, tc.body) })
			expect(t, r, http.StatusUnprocessableEntity, httpx.CodeInvalidArgument)
			if !slices.Contains(fieldReasons(r)[tc.field], tc.reason) {
				t.Fatalf("want %s %s, got %s", tc.field, tc.reason, r.raw)
			}
		})
	}
	if n := w.count(`SELECT count(*) FROM api_keys`); n != 0 {
		t.Fatalf("%d keys written by refused requests", n)
	}
	// Each scope of the CHECK list is accepted in its valid form.
	for _, b := range []map[string]any{
		{"name": "i", "scope": "integration", "capabilities": []string{"users:view"}, "tenantId": w.O},
		{"name": "s", "scope": "script", "capabilities": []string{"fleet:manage_customers", "mobile:view_tasks"}},
		{"name": "c", "scope": "cf_shim", "capabilities": []string{"mobile:view_tasks", "mobile:submit_trip"}},
		{"name": "r", "scope": "release_publisher", "capabilities": []string{"security:manage_mobile_release"}},
	} {
		expect(t, w.createKey(pa, b), http.StatusCreated, "")
	}
	// A suspended tenant gets no new key.
	w.exec(`UPDATE tenants SET status = 'suspended' WHERE id = $1`, w.C)
	r := w.noEvent(func() resp {
		return w.createKey(pa, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.C})
	})
	expect(t, r, http.StatusConflict, "failed_precondition")
}

// AC: a revoked key is 401 from its next request; revoking is audited once and idempotent. Expired keys,
// wrong secrets and malformed values are 401 too; a key with a bearer is not one credential.
func TestAPIKeyRevokedAndInvalid(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")
	id, secret := w.mustKey(ta, map[string]any{"name": "nightly", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.O})
	expect(t, w.keyed(http.MethodGet, "/v1/roles", secret), http.StatusOK, "")
	expect(t, w.keyed(http.MethodGet, "/v1/keytest/users-view", secret), http.StatusNoContent, "")

	w.oneEvent(iam.EventAPIKeyRevoked, func() resp { return w.send(http.MethodDelete, "/v1/api-keys/"+id, ta, nil) }, http.StatusNoContent)
	r := w.keyed(http.MethodGet, "/v1/roles", secret)
	expect(t, r, http.StatusUnauthorized, auth.CodeInvalidToken)
	if reason(r) != auth.ReasonKeyRevoked {
		t.Fatalf("revoked key: %s", r.raw)
	}
	w.noEvent(func() resp {
		r := w.send(http.MethodDelete, "/v1/api-keys/"+id, ta, nil)
		expect(t, r, http.StatusNoContent, "")
		return r
	})
	l := w.get("/v1/api-keys", ta)
	for _, v := range l.data().([]any) {
		if k := v.(map[string]any); k["id"] == id && (k["status"] != "revoked" || k["revokedAt"] == nil || k["revokedBy"] != w.userID("ta.o@example.test")) {
			t.Fatalf("revoked key listed as %v", k)
		}
	}

	// Expired.
	id2, secret2 := w.mustKey(ta, map[string]any{"name": "short-lived", "scope": "script", "capabilities": []string{"users:view"},
		"tenantId": w.O, "expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	expect(t, w.keyed(http.MethodGet, "/v1/roles", secret2), http.StatusOK, "")
	w.exec(`UPDATE api_keys SET expires_at = now() - interval '1 second' WHERE id = $1`, id2)
	if r := w.keyed(http.MethodGet, "/v1/roles", secret2); r.status != http.StatusUnauthorized || reason(r) != auth.ReasonKeyExpired {
		t.Fatalf("expired key: %d %s", r.status, r.raw)
	}

	// A tenant that is no longer active takes its keys with it.
	_, secret3 := w.mustKey(ta, map[string]any{"name": "o-key", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.O})
	w.exec(`UPDATE tenants SET status = 'suspended' WHERE id = $1`, w.O)
	if r := w.keyed(http.MethodGet, "/v1/roles", secret3); r.status != http.StatusUnauthorized || reason(r) != auth.ReasonKeyTenantInactive {
		t.Fatalf("suspended tenant's key: %d %s", r.status, r.raw)
	}
	w.exec(`UPDATE tenants SET status = 'active' WHERE id = $1`, w.O)

	// Wrong secret with a real prefix, garbage, repeated header: 401 without a reason.
	prefix, _ := apikey.Prefix(secret3)
	forged := "ltk_" + prefix + "_" + strings.Repeat("A", apikey.BodyLen)
	for name, v := range map[string]string{"forged": forged, "garbage": "not-a-key", "jwt": ta} {
		r := w.keyed(http.MethodGet, "/v1/roles", v)
		expect(t, r, http.StatusUnauthorized, auth.CodeInvalidToken)
		if reason(r) != "" {
			t.Fatalf("%s: a reason for an unknown key: %s", name, r.raw)
		}
	}
	expect(t, w.keyed(http.MethodGet, "/v1/roles", secret3, auth.HeaderAPIKey, secret3), http.StatusUnauthorized, auth.CodeInvalidToken)
	// One credential per request: a key and a bearer together are refused, even on a route that takes both.
	r = w.call(w.internal, http.MethodGet, "/v1/roles", ta, auth.HeaderAPIKey, secret3)
	if r.status != http.StatusUnauthorized || reason(r) != auth.ReasonMultipleCreds {
		t.Fatalf("key + bearer: %d %s", r.status, r.raw)
	}
	// No credential at all is still unauthenticated.
	expect(t, w.get("/v1/roles", ""), http.StatusUnauthorized, httpx.CodeUnauthenticated)

	// last_used_at is written in the background.
	deadline := time.Now().Add(5 * time.Second)
	for w.count(`SELECT count(*) FROM api_keys WHERE id = $1 AND last_used_at IS NOT NULL`, id) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("last_used_at was never written")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Scope enforcement (R82, Appendix C §C.4.11): integration and script keys on the internal routes that admit
// them, with exactly their capabilities and RLS in their tenant; release_publisher only on the publish routes;
// cf_shim nowhere until its shim targets ship (M2); no key on the public listener, on a route that does not
// admit keys, or with X-Act-On-Tenant.
func TestAPIKeyScopeEnforcement(t *testing.T) {
	w := newWorld(t)
	taA, pa := w.login("ta.a@example.test"), w.login("pa@example.test")

	_, integ := w.mustKey(taA, map[string]any{"name": "a-sync", "scope": "integration",
		"capabilities": []string{"operations:view_first_mile"}, "tenantId": w.A})
	_, script := w.mustKey(taA, map[string]any{"name": "a-script", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.A})
	_, platformKey := w.mustKey(pa, map[string]any{"name": "master-data", "scope": "integration", "capabilities": []string{"fleet:manage_customers"}})
	_, publisher := w.mustKey(pa, map[string]any{"name": "release", "scope": "release_publisher", "capabilities": []string{"security:manage_mobile_release"}})
	shimID, shim := w.mustKey(pa, map[string]any{"name": "callable shims", "scope": "cf_shim", "capabilities": []string{"mobile:view_tasks"}})

	scopeRefusal := func(r resp) {
		t.Helper()
		expect(t, r, http.StatusForbidden, authz.CodePermissionDenied)
		if reason(r) != auth.ReasonKeyScope {
			t.Fatalf("want reason %s: %s", auth.ReasonKeyScope, r.raw)
		}
	}

	// Integration: exactly its capabilities, its tenant's rows only (RLS: role user in A, no contractor reach).
	who := w.keyed(http.MethodGet, "/v1/keytest/whoami", integ)
	expect(t, who, http.StatusOK, "")
	if d := who.data().(map[string]any); strings.Join(toStrings(d["caps"]), ",") != "operations:view_first_mile" ||
		d["steward"] != false || d["amr"] != authz.AMRAPIKey || d["scope"] != "integration" || d["tenantKind"] != "carrier" {
		t.Fatalf("integration principal: %s", who.raw)
	}
	tasks := w.keyed(http.MethodGet, "/v1/keytest/tasks", integ)
	expect(t, tasks, http.StatusOK, "")
	if got := tasks.data().(map[string]any); len(got) != 1 || got[w.A] != float64(2) {
		t.Fatalf("a tenant key of A sees %v", named(w, toCounts(got)))
	}
	r := w.keyed(http.MethodGet, "/v1/keytest/users-view", integ)
	expect(t, r, http.StatusForbidden, authz.CodePermissionDenied)
	if strings.Join(missingCap(r), ",") != "users:view" {
		t.Fatalf("missing capability: %s", r.raw)
	}
	// Script: the same routes, its own capabilities.
	expect(t, w.keyed(http.MethodGet, "/v1/keytest/users-view", script), http.StatusNoContent, "")
	expect(t, w.keyed(http.MethodGet, "/v1/roles", script), http.StatusOK, "")

	// A platform integration key is a steward without a tenant: global master data, under RLS.
	expect(t, w.keyed(http.MethodPost, "/v1/keytest/customers?code=KEYCO", platformKey), http.StatusCreated, "")
	if n := w.count(`SELECT count(*) FROM customers WHERE code = 'KEYCO'`); n != 1 {
		t.Fatalf("customer rows: %d", n)
	}

	// Routes that admit no key (people only): identity, sessions, the key administration itself.
	for _, path := range []string{"/v1/me", "/v1/users", "/v1/api-keys", "/v1/rbactest/whoami"} {
		scopeRefusal(w.keyed(http.MethodGet, path, integ))
	}
	scopeRefusal(w.send(http.MethodPost, "/v1/api-keys", "", map[string]any{"name": "x", "scope": "script",
		"capabilities": []string{"users:view"}, "tenantId": w.A}, auth.HeaderAPIKey, integ))

	// The public listener refuses every key, even on a public route that admits the scope internally.
	expect(t, w.keyed(http.MethodGet, "/v1/mobile/keytest", integ), http.StatusNoContent, "")
	scopeRefusal(w.call(w.public, http.MethodGet, "/v1/mobile/keytest", "", auth.HeaderAPIKey, integ))

	// release_publisher: only the publish routes; integration and script keys are not admitted there.
	expect(t, w.keyed(http.MethodPost, "/v1/keytest/publish", publisher), http.StatusNoContent, "")
	scopeRefusal(w.keyed(http.MethodGet, "/v1/roles", publisher))
	scopeRefusal(w.keyed(http.MethodGet, "/v1/keytest/whoami", publisher))
	scopeRefusal(w.keyed(http.MethodPost, "/v1/keytest/publish", platformKey))

	// cf_shim: created and listed, used nowhere yet; a bearer beside it changes nothing here.
	if !slices.Contains(keyIDs(w.get("/v1/api-keys", pa)), shimID) {
		t.Fatal("the cf_shim key is not listed")
	}
	for _, path := range []string{"/v1/roles", "/v1/keytest/whoami", "/v1/me"} {
		scopeRefusal(w.keyed(http.MethodGet, path, shim))
	}
	scopeRefusal(w.call(w.internal, http.MethodGet, "/v1/roles", pa, auth.HeaderAPIKey, shim))
	scopeRefusal(w.call(w.public, http.MethodGet, "/v1/mobile/keytest", "", auth.HeaderAPIKey, shim))

	// X-Act-On-Tenant is for platform people, never for a key.
	expect(t, w.keyed(http.MethodGet, "/v1/keytest/whoami", platformKey, authz.HeaderActOnTenant, "*"),
		http.StatusForbidden, authz.CodePermissionDenied)
}

func toCounts(m map[string]any) map[string]int {
	out := map[string]int{}
	for k, v := range m {
		out[k] = int(v.(float64))
	}
	return out
}

// Reach (Appendix B §B.2.6): a tenant caller manages its tenant's keys only and grants only what it holds; a
// platform_admin manages every key, creates the platform keys and reads other tenants through X-Act-On-Tenant.
func TestAPIKeyReach(t *testing.T) {
	w := newWorld(t)
	taA, taC, pa := w.login("ta.a@example.test"), w.login("ta.c@example.test"), w.login("pa@example.test")
	mgA, opO := w.login("mg.a@example.test"), w.login("op.o@example.test")
	keyA, _ := w.mustKey(taA, map[string]any{"name": "a", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.A})
	keyC, _ := w.mustKey(taC, map[string]any{"name": "c", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.C})
	keyP, _ := w.mustKey(pa, map[string]any{"name": "p", "scope": "script", "capabilities": []string{"waitlist:view"}})
	keyA2, _ := w.mustKey(pa, map[string]any{"name": "a by pa", "scope": "integration", "capabilities": []string{"drivers:view"}, "tenantId": w.A})

	// Refused creations write nothing.
	for name, tc := range map[string]struct {
		bearer string
		body   map[string]any
		reason string
	}{
		"another tenant": {taA, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.C}, iam.ReasonTenantOutOfReach},
		"platform key":   {taA, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"waitlist:view"}}, iam.ReasonPlatformAdminOnly},
		"cf_shim key":    {taA, map[string]any{"name": "x", "scope": "cf_shim", "capabilities": []string{"mobile:view_tasks"}, "tenantId": nil}, iam.ReasonPlatformAdminOnly},
	} {
		r := w.noEvent(func() resp { return w.createKey(tc.bearer, tc.body) })
		if r.status != http.StatusForbidden || reason(r) != tc.reason {
			t.Fatalf("%s: want 403 %s, got %d %s", name, tc.reason, r.status, r.raw)
		}
	}
	// A caller grants only what it holds: an override gives A's managers security:manage_api_keys, and a
	// manager holds users:view but not users:assign_role.
	w.exec(`INSERT INTO role_capability_overrides (tenant_id, role, capability, allowed, updated_by)
		VALUES ($1, 'manager', 'security:manage_api_keys', true, $2)`, w.A, w.userID("ta.a@example.test"))
	if _, err := w.rbac.BumpVersion(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := w.noEvent(func() resp {
		return w.createKey(mgA, map[string]any{"name": "x", "scope": "script", "capabilities": []string{"users:assign_role"}, "tenantId": w.A})
	})
	if r.status != http.StatusForbidden || reason(r) != iam.ReasonCapabilityNotHeld || strings.Join(missingCap(r), ",") != "users:assign_role" {
		t.Fatalf("manager granting users:assign_role: %d %s", r.status, r.raw)
	}
	expect(t, w.createKey(mgA, map[string]any{"name": "mg", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.A}), http.StatusCreated, "")
	// No security:manage_api_keys: 403 on every route.
	expect(t, w.get("/v1/api-keys", opO), http.StatusForbidden, authz.CodePermissionDenied)

	// Lists.
	all := w.get("/v1/api-keys", pa, authz.HeaderActOnTenant, "*")
	expect(t, all, http.StatusOK, "")
	if n := len(keyIDs(all)); n != 5 {
		t.Fatalf("X-Act-On-Tenant: * lists %d keys, want 5", n)
	}
	if got := keyIDs(w.get("/v1/api-keys", pa)); !slices.Equal(got, sorted(keyP)) {
		t.Fatalf("platform admin without the header lists %v, want the platform key", got)
	}
	if got := keyIDs(w.get("/v1/api-keys", taC)); !slices.Equal(got, sorted(keyC)) {
		t.Fatalf("C lists %v", got)
	}
	actA := keyIDs(w.get("/v1/api-keys", pa, authz.HeaderActOnTenant, w.A))
	if len(actA) != 3 || !slices.Contains(actA, keyA) || !slices.Contains(actA, keyA2) || slices.Contains(actA, keyC) {
		t.Fatalf("acting as A lists %v", actA)
	}
	if got := keyIDs(w.get("/v1/api-keys", taA)); !slices.Equal(got, actA) {
		t.Fatalf("A lists %v, acting as A %v", got, actA)
	}

	// Paging: limit 2 walks the five keys newest first without duplicates.
	var seen []string
	cursor := ""
	for range 5 {
		path := "/v1/api-keys?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		p := w.get(path, pa, authz.HeaderActOnTenant, "*")
		expect(t, p, http.StatusOK, "")
		seen = append(seen, keyIDs(p)...)
		next, _ := p.body["nextCursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 5 || len(slices.Compact(sorted(seen...))) != 5 {
		t.Fatalf("paging saw %v", seen)
	}
	expect(t, w.get("/v1/api-keys?limit=0", pa), http.StatusUnprocessableEntity, httpx.CodeInvalidArgument)
	expect(t, w.get("/v1/api-keys?scope=script", pa), http.StatusBadRequest, httpx.CodeBadRequest)

	// Revocation: another tenant's key does not exist for the caller; a platform admin revokes any.
	w.noEvent(func() resp {
		r := w.send(http.MethodDelete, "/v1/api-keys/"+keyA, taC, nil)
		expect(t, r, http.StatusNotFound, httpx.CodeNotFound)
		return r
	})
	expect(t, w.send(http.MethodDelete, "/v1/api-keys/"+keyP, taA, nil), http.StatusNotFound, httpx.CodeNotFound)
	expect(t, w.send(http.MethodDelete, "/v1/api-keys/not-a-uuid", taA, nil), http.StatusNotFound, httpx.CodeNotFound)
	w.oneEvent(iam.EventAPIKeyRevoked, func() resp { return w.send(http.MethodDelete, "/v1/api-keys/"+keyC, pa, nil) }, http.StatusNoContent)
	w.oneEvent(iam.EventAPIKeyRevoked, func() resp { return w.send(http.MethodDelete, "/v1/api-keys/"+keyA, taA, nil) }, http.StatusNoContent)
	if n := w.count(`SELECT count(*) FROM security_events WHERE event_type = 'api_key_revoked' AND tenant_id = $1`, w.A); n != 1 {
		t.Fatalf("api_key_revoked rows of A: %d", n)
	}
}

// The apikey bucket (Appendix B §B.6.3): api_keys.rate_per_min per key, 429 with Retry-After.
func TestAPIKeyRateLimit(t *testing.T) {
	w := newWorldOpts(t, worldOpts{rateLimit: true})
	ta := w.login("ta.o@example.test")
	id, secret := w.mustKey(ta, map[string]any{"name": "slow", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.O})
	w.exec(`UPDATE api_keys SET rate_per_min = 2 WHERE id = $1`, id)
	expect(t, w.keyed(http.MethodGet, "/v1/roles", secret), http.StatusOK, "")
	expect(t, w.keyed(http.MethodGet, "/v1/roles", secret), http.StatusOK, "")
	req, err := http.NewRequest(http.MethodGet, w.internal+"/v1/roles", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(auth.HeaderAPIKey, secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("third request: %d, Retry-After %q", res.StatusCode, res.Header.Get("Retry-After"))
	}
}

// The two /v1/auth routes that authenticate their caller themselves (logout, password change) refuse an
// X-Api-Key like every RequireAuth route without AllowAPIKeys (Appendix B §B.1.4, Appendix C §C.4.3 (3)):
// a key with a bearer is 401 (multiple_credentials, or the dead key's reason), a key alone 403
// api_key_scope, and the bearer's session and password stay as they were. Anonymous routes ignore it.
func TestAPIKeyRefusedOnSelfAuthenticatingAuthRoutes(t *testing.T) {
	w := newWorld(t)
	const email = "ta.o@example.test"
	ta := w.login(email)
	revokedID, revoked := w.mustKey(ta, map[string]any{"name": "old", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.O})
	expect(t, w.send(http.MethodDelete, "/v1/api-keys/"+revokedID, ta, nil), http.StatusNoContent, "")
	_, secret := w.mustKey(ta, map[string]any{"name": "nightly", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.O})
	change := map[string]any{"currentPassword": pw, "newPassword": "another long passphrase 2"}
	ticket := map[string]any{"passwordChangeTicket": "not-a-ticket", "newPassword": "another long passphrase 2"}

	for _, tc := range []struct {
		name, path, bearer, key string
		body                    map[string]any
		status                  int
		code, reason            string
	}{
		{"logout, bearer + key", "/v1/auth/logout", ta, secret, map[string]any{}, http.StatusUnauthorized, auth.CodeInvalidToken, auth.ReasonMultipleCreds},
		{"logout, bearer + revoked key", "/v1/auth/logout", ta, revoked, map[string]any{}, http.StatusUnauthorized, auth.CodeInvalidToken, auth.ReasonKeyRevoked},
		{"logout, bearer + unknown key", "/v1/auth/logout", ta, "not-a-key", map[string]any{}, http.StatusUnauthorized, auth.CodeInvalidToken, ""},
		{"logout, key alone", "/v1/auth/logout", "", secret, map[string]any{}, http.StatusForbidden, authz.CodePermissionDenied, auth.ReasonKeyScope},
		{"password change, bearer + key", "/v1/auth/password/change", ta, secret, change, http.StatusUnauthorized, auth.CodeInvalidToken, auth.ReasonMultipleCreds},
		{"password change, bearer + revoked key", "/v1/auth/password/change", ta, revoked, change, http.StatusUnauthorized, auth.CodeInvalidToken, auth.ReasonKeyRevoked},
		{"password change, key alone", "/v1/auth/password/change", "", secret, change, http.StatusForbidden, authz.CodePermissionDenied, auth.ReasonKeyScope},
		{"password change ticket form, key alone", "/v1/auth/password/change", "", secret, ticket, http.StatusForbidden, authz.CodePermissionDenied, auth.ReasonKeyScope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := w.send(http.MethodPost, tc.path, tc.bearer, tc.body, auth.HeaderAPIKey, tc.key)
			expect(t, r, tc.status, tc.code)
			if reason(r) != tc.reason {
				t.Fatalf("want reason %q: %s", tc.reason, r.raw)
			}
		})
	}
	// Nothing was done as the bearer's user: the session is live and the password unchanged.
	expect(t, w.get("/v1/me", ta), http.StatusOK, "")
	_ = w.login(email)
	if n := w.securityEvents("password_changed"); n != 0 {
		t.Fatalf("%d password_changed events", n)
	}

	// The public listener refuses the key the same way.
	r := w.call(w.public, http.MethodPost, "/v1/auth/logout", "", auth.HeaderAPIKey, secret)
	expect(t, r, http.StatusForbidden, authz.CodePermissionDenied)
	if reason(r) != auth.ReasonKeyScope {
		t.Fatalf("public listener: %s", r.raw)
	}

	// Anonymous routes ignore the header: a login with a key is a login.
	login := w.send(http.MethodPost, "/v1/auth/login", "", map[string]any{"email": email, "password": pw, "platform": "web"},
		auth.HeaderAPIKey, secret)
	expect(t, login, http.StatusOK, "")

	// Without a key both routes work as before.
	expect(t, w.send(http.MethodPost, "/v1/auth/password/change", ta, change), http.StatusNoContent, "")
	ta2 := w.login2(email, "another long passphrase 2")
	expect(t, w.send(http.MethodPost, "/v1/auth/logout", ta2, map[string]any{}), http.StatusNoContent, "")
	expect(t, w.get("/v1/me", ta2), http.StatusUnauthorized, auth.CodeSessionRevoked)
}
