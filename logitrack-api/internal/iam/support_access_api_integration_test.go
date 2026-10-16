//go:build integration

package iam_test

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

func supportFixtureConsent(w *world, tenant string, enabled bool) {
	w.t.Helper()
	err := db.WithSystem(context.Background(), w.pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `UPDATE tenants SET support_access_enabled=$2,support_access_version=support_access_version+1,support_access_changed_at=clock_timestamp(),support_access_changed_by=$3 WHERE id=$1 AND support_access_enabled<>$2`, tenant, enabled, w.userID("pa@example.test"))
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

func TestSupportD2OffBlocksEveryAdminTakeoverPath(t *testing.T) {
	w := newWorld(t)
	// Driver-link routes still require the ordinary drivers:edit capability. Give the
	// platform actor a real admin membership so these cases reach the D2 guard.
	w.member(w.userID("pa@example.test"), w.A, "tenant_admin")
	pa := w.login("pa@example.test")
	uid := w.userID("ta.a@example.test")
	driver := w.id(`SELECT id::text FROM drivers WHERE tenant_id=$1 LIMIT 1`, w.A)
	checks := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/v1/users/" + uid + "/disable", map[string]any{}},
		{http.MethodPost, "/v1/users/" + uid + "/enable", map[string]any{}},
		{http.MethodDelete, "/v1/users/" + uid, nil},
		{http.MethodPatch, "/v1/users/" + uid, map[string]any{"email": "takeover@example.test"}},
		{http.MethodPost, "/v1/users/" + uid + "/password/temporary", map[string]any{}},
		{http.MethodPost, "/v1/users/" + uid + "/invite", map[string]any{}},
		{http.MethodPut, "/v1/users/" + uid + "/driver-link", map[string]any{"driverId": driver}},
		{http.MethodDelete, "/v1/users/" + uid + "/driver-link", nil},
		{http.MethodPut, "/v1/tenants/" + w.A + "/members/" + uid, map[string]any{"role": "manager"}},
		{http.MethodPut, "/v1/tenants/" + w.A + "/members/" + uid, map[string]any{"role": "tenant_admin"}},
		{http.MethodDelete, "/v1/tenants/" + w.A + "/members/" + uid, nil},
		{http.MethodPost, "/v1/tenants/" + w.A + "/members", map[string]any{"email": "extra-admin@example.test", "role": "tenant_admin"}},
		{http.MethodPost, "/v1/users/" + uid + "/platform-roles", map[string]any{"role": "support"}},
	}
	before := w.count(`SELECT count(*) FROM security_events`)
	for _, tc := range checks {
		r := w.send(tc.method, tc.path, pa, tc.body)
		expect(t, r, 403, "support_access_required")
	}
	// Disabling an account never turns its active membership into an onboarding vacancy.
	w.exec(`UPDATE users SET status='disabled' WHERE id=$1`, uid)
	for _, tc := range checks {
		r := w.send(tc.method, tc.path, pa, tc.body)
		expect(t, r, 403, "support_access_required")
	}
	if w.count(`SELECT count(*) FROM security_events`) != before {
		t.Fatal("refused actions appended audit")
	}
	if w.count(`SELECT count(*) FROM outbox_events WHERE routing_key='tenant.support_admin_action'`) != 0 {
		t.Fatal("refused actions enqueued mail")
	}
}

func TestSupportConsentCASNoOpAndEligibility(t *testing.T) {
	w := newWorld(t)
	token := w.login("ta.a@example.test")
	path := "/v1/tenants/" + w.A + "/support-access"
	read := w.get(path, token)
	expect(t, read, 200, "")
	state := read.data().(map[string]any)
	if state["eligibleToToggle"] != true || state["enabled"] != false || state["version"] != "0" {
		t.Fatalf("state %s", read.raw)
	}
	expect(t, w.send(http.MethodPut, path, token, map[string]any{"enabled": true, "expectedVersion": "0"}), 200, "")
	expect(t, w.send(http.MethodPut, path, token, map[string]any{"enabled": false, "expectedVersion": "0"}), 409, "support_access_changed")
	expect(t, w.send(http.MethodPut, path, token, map[string]any{"enabled": true, "expectedVersion": "1"}), 200, "")
	if w.securityEvents("support_access_enabled") != 1 || w.count(`SELECT count(*) FROM outbox_events WHERE routing_key='tenant.support_access_changed'`) != 1 {
		t.Fatal("CAS retry duplicated side effects")
	}
	expect(t, w.send(http.MethodPut, path, w.login("pa@example.test"), map[string]any{"enabled": false, "expectedVersion": "1"}), 403, "permission_denied")
}

func TestSupportD2SharedAccountUsesEveryAffectedTenant(t *testing.T) {
	w := newWorld(t)
	uid := w.userID("ta.a@example.test")
	w.member(uid, w.B, "manager")
	w.member(w.user("admin-b@example.test"), w.B, "tenant_admin")
	supportFixtureConsent(w, w.A, true)
	pa := w.login("pa@example.test")
	for _, path := range []string{"disable", "enable", "invite", "password/temporary"} {
		expect(t, w.send(http.MethodPost, "/v1/users/"+uid+"/"+path, pa, map[string]any{}), 403, "support_access_required")
	}
	supportFixtureConsent(w, w.B, true)
	expect(t, w.send(http.MethodPost, "/v1/users/"+uid+"/disable", pa, map[string]any{}), 204, "")
	if w.count(`SELECT count(*) FROM security_events WHERE event_type='platform_admin_account_changed' AND target_user_id=$1`, uid) != 2 {
		t.Fatal("missing affected tenant event")
	}
	if w.count(`SELECT count(*) FROM outbox_events WHERE routing_key='tenant.support_admin_action'`) != 2 {
		t.Fatal("missing affected tenant notification")
	}
}

func TestSupportPlatformReadsProjectWithoutSelectedTenant(t *testing.T) {
	w := newWorld(t)
	supportFixtureConsent(w, w.A, true)
	for _, email := range []string{"pa@example.test", "su@example.test"} {
		r := w.get("/v1/platform/users?limit=100", w.login(email))
		expect(t, r, 200, "")
		if !strings.Contains(string(r.raw), "ta.a@example.test") || strings.Contains(string(r.raw), "ta.c@example.test") {
			t.Fatalf("target consent projection %s", r.raw)
		}
	}
}

func TestSupportOptionsSafeSearchBoundsAndCursor(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	for _, q := range []string{"limit=0", "limit=101", "q=" + strings.Repeat("x", 101)} {
		expect(t, w.get("/v1/tenants/support-options?"+q, pa), 422, "invalid_argument")
	}
	// An unreviewed person-derived code and name cannot match while that carrier is OFF.
	w.exec(`UPDATE tenants SET legal_type='individual',name_th='PrivatePerson',code='PrivatePerson' WHERE id=$1`, w.C)
	r := w.get("/v1/tenants/support-options?q=PrivatePerson", pa)
	expect(t, r, 200, "")
	if len(r.data().([]any)) != 0 {
		t.Fatal("masked name matched")
	}
	r = w.get("/v1/tenants/support-options?limit=1", pa)
	expect(t, r, 200, "")
	cur, _ := r.body["nextCursor"].(string)
	if cur == "" {
		t.Fatal("no cursor")
	}
	expect(t, w.get("/v1/tenants/support-options?cursor="+cur+"&q=other", pa), 422, "invalid_argument")
	other := w.user("another-platform@example.test")
	w.exec(`INSERT INTO user_platform_roles(user_id,role)VALUES($1,'platform_admin')`, other)
	expect(t, w.get("/v1/tenants/support-options?cursor="+cur, w.login("another-platform@example.test")), 422, "invalid_argument")
}

func TestSupportCredentialResetBlocksPreviouslyEligibleMember(t *testing.T) {
	w := newWorld(t)
	uid := w.userID("ta.a@example.test")
	token := w.login("ta.a@example.test")
	w.exec(`UPDATE users SET credential_reset_by_platform_at=clock_timestamp(),must_change_password=false WHERE id=$1`, uid)
	path := "/v1/tenants/" + w.A + "/support-access"
	r := w.get(path, token)
	expect(t, r, 200, "")
	if r.data().(map[string]any)["eligibleToToggle"] != false {
		t.Fatal("platform-set credential stayed eligible")
	}
	expect(t, w.send(http.MethodPut, path, token, map[string]any{"enabled": true, "expectedVersion": "0"}), 403, "permission_denied")
	w.exec(`UPDATE users SET credential_self_chosen_at=clock_timestamp()+interval '1 second' WHERE id=$1`, uid)
	expect(t, w.send(http.MethodPut, path, token, map[string]any{"enabled": true, "expectedVersion": "0"}), 200, "")
}

func TestSupportNewTableETLSelectGrants(t *testing.T) {
	w := newWorld(t)
	for _, table := range []string{"support_visits", "membership_provenance_history", "support_request_limits"} {
		var n int
		if err := w.etl.QueryRow(context.Background(), fmt.Sprintf("SELECT count(*) FROM %s", pgx.Identifier{table}.Sanitize())).Scan(&n); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSupportFirstAdminRequiresHolderPasswordCompletion(t *testing.T) {
	w := newWorld(t)
	tenant := w.carrier("first-support-admin", nil)
	user := w.user("first-holder@example.test")
	w.member(user, tenant, "manager")
	pa := w.login("pa@example.test")
	expect(t, w.send(http.MethodPut, "/v1/tenants/"+tenant+"/members/"+user, pa, map[string]any{"role": "tenant_admin"}), 200, "")
	r := w.loginWith("first-holder@example.test", pw)
	expect(t, r, 403, "password_change_required")
	ticket := r.body["error"].(map[string]any)["details"].(map[string]any)["passwordChangeTicket"].(string)
	const chosen = "the holder chooses this password 42"
	expect(t, w.send(http.MethodPost, "/v1/auth/password/change", "", map[string]any{"passwordChangeTicket": ticket, "newPassword": chosen}), 204, "")
	r = w.loginWith("first-holder@example.test", chosen)
	expect(t, r, 200, "")
	token := r.data().(map[string]any)["accessToken"].(string)
	path := "/v1/tenants/" + tenant + "/support-access"
	r = w.get(path, token)
	expect(t, r, 200, "")
	if r.data().(map[string]any)["eligibleToToggle"] != true {
		t.Fatalf("holder completion did not activate the recorded first grant: %s", r.raw)
	}
	expect(t, w.send(http.MethodPut, path, token, map[string]any{"enabled": true, "expectedVersion": "0"}), 200, "")
	if w.count(`SELECT count(*) FROM memberships WHERE user_id=$1 AND tenant_id=$2 AND granted_by_platform AND ratified_at IS NULL`, user, tenant) != 1 {
		t.Fatal("bootstrap erased provenance or invented ratification")
	}
}

func TestSupportHistoricalNativeAdminCannotAcquireNewBootstrap(t *testing.T) {
	w := newWorld(t)
	tenant := w.carrier("previous-native-admin", nil)
	user := w.user("previous-native@example.test")
	w.member(user, tenant, "tenant_admin")
	w.exec(`UPDATE memberships SET role='manager' WHERE user_id=$1 AND tenant_id=$2`, user, tenant)
	expect(t, w.send(http.MethodPut, "/v1/tenants/"+tenant+"/members/"+user, w.login("pa@example.test"), map[string]any{"role": "tenant_admin"}), 200, "")
	// Even a later actual holder choice cannot turn a replacement grant into initial onboarding.
	w.exec(`UPDATE users SET must_change_password=false,credential_self_chosen_at=clock_timestamp()+interval '1 second' WHERE id=$1`, user)
	token := w.login("previous-native@example.test")
	r := w.get("/v1/tenants/"+tenant+"/support-access", token)
	expect(t, r, 200, "")
	if r.data().(map[string]any)["eligibleToToggle"] != false {
		t.Fatalf("historical native grant reacquired bootstrap: %s", r.raw)
	}
	version := w.id(`SELECT grant_version::text FROM memberships WHERE user_id=$1 AND tenant_id=$2`, user, tenant)
	expect(t, w.send(http.MethodPost, "/v1/tenants/"+tenant+"/members/"+user+"/ratify", token, map[string]any{"expectedGrantVersion": version}), 403, "ratification_not_allowed")
}
