//go:build integration

package iam_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

func TestGate1MemberAPIKeysAfterTenantSwitchAndRefresh(t *testing.T) {
	w := newWorld(t)
	email := "ta.a@example.test"
	w.member(w.userID(email), w.B, "tenant_admin")
	w.exec(`UPDATE users SET last_tenant_id=$2 WHERE id=$1`, w.userID(email), w.A)
	initial, refresh := w.tokens(email)
	access := w.switchTo(initial, w.B)
	if got := claimsOf(t, access)["scv"]; got != "1" {
		t.Fatalf("context version after tenant switch: %v, want 1", got)
	}
	body := map[string]any{"name": "member-switch", "scope": "script", "tenantId": w.B, "capabilities": []string{"users:view"}}
	stale := w.createKey(initial, body)
	if stale.status != http.StatusUnauthorized || reason(stale) != "claims_changed" {
		t.Fatalf("old context accepted: %d %s", stale.status, stale.raw)
	}
	for _, stage := range []string{"switched", "refreshed"} {
		if stage == "refreshed" {
			r := w.send(http.MethodPost, "/v1/auth/refresh", "", map[string]any{"refreshToken": refresh})
			expect(t, r, http.StatusOK, "")
			access = r.data().(map[string]any)["accessToken"].(string)
		}
		created := w.createKey(access, body)
		expect(t, created, http.StatusCreated, "")
		key := created.data().(map[string]any)["id"].(string)
		expect(t, w.send(http.MethodDelete, "/v1/api-keys/"+key, access, nil), http.StatusNoContent, "")
		if w.count(`SELECT count(*) FROM api_keys WHERE id=$1 AND tenant_id=$2 AND revoked_at IS NOT NULL`, key, w.B) != 1 {
			t.Fatalf("%s key creation/revocation did not commit", stage)
		}
	}
	if w.count(`SELECT count(*) FROM outbox_events WHERE routing_key='tenant.support_admin_action'`) != 0 {
		t.Fatal("ordinary member key actions entered platform D2 notifications")
	}
}

func TestGate1TenantAPIKeysRequireConsentAndNotifyAdmins(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	body := map[string]any{"name": "consent-probe", "scope": "script", "tenantId": w.A,
		"capabilities": []string{"drivers:view_pii", "operations:manage_tasks"}}
	beforeKeys, beforeMail := w.count(`SELECT count(*) FROM api_keys`), w.count(`SELECT count(*) FROM outbox_events WHERE routing_key='tenant.support_admin_action'`)
	expect(t, w.createKey(pa, body), 403, "support_access_required")
	admin := w.userID("ta.a@example.test")
	w.exec(`UPDATE users SET status='disabled',disabled_at=clock_timestamp() WHERE id=$1`, admin)
	expect(t, w.createKey(pa, body), 403, "support_access_required")
	if w.count(`SELECT count(*) FROM api_keys`) != beforeKeys || w.count(`SELECT count(*) FROM outbox_events WHERE routing_key='tenant.support_admin_action'`) != beforeMail {
		t.Fatal("denied key created side effects")
	}
	supportFixtureConsent(w, w.A, true)
	created := w.createKey(pa, body)
	expect(t, created, 201, "")
	key := created.data().(map[string]any)["id"].(string)
	if w.count(`SELECT count(*) FROM outbox_events WHERE routing_key='tenant.support_admin_action' AND payload->>'action'='api_key_create' AND payload->>'targetApiKeyId'=$1 AND payload->'recipientUserIds' @> to_jsonb(ARRAY[$2::text])`, key, admin) != 1 {
		t.Fatal("disabled existing admin was not notified of key creation")
	}
	supportFixtureConsent(w, w.A, false)
	expect(t, w.send(http.MethodDelete, "/v1/api-keys/"+key, pa, nil), 204, "")
	expect(t, w.send(http.MethodDelete, "/v1/api-keys/"+key, pa, nil), 204, "")
	if w.count(`SELECT count(*) FROM api_keys WHERE id=$1 AND revoked_at IS NOT NULL`, key) != 1 {
		t.Fatal("OFF key revocation did not commit")
	}
	if w.count(`SELECT count(*) FROM outbox_events WHERE routing_key='tenant.support_admin_action' AND payload->>'action'='api_key_revoke' AND payload->>'basis'='access_reduction' AND payload->>'targetApiKeyId'=$1 AND payload->'recipientUserIds' @> to_jsonb(ARRAY[$2::text])`, key, admin) != 1 {
		t.Fatal("revocation notification missing or duplicated")
	}
	if w.count(`SELECT count(*) FROM security_events WHERE event_type='api_key_revoked' AND details->>'keyId'=$1`, key) != 1 {
		t.Fatal("revocation security event missing or duplicated")
	}
	// Onboarding and platform-only keys preserve their independent authorization.
	w.exec(`DELETE FROM memberships WHERE tenant_id=$1`, w.B)
	body["tenantId"] = w.B
	expect(t, w.createKey(pa, body), 201, "")
	expect(t, w.createKey(pa, map[string]any{"name": "global", "scope": "script", "capabilities": []string{"waitlist:view"}}), 201, "")
}

func TestGate1TenantAPIKeyNotificationFailureRollsBack(t *testing.T) {
	for _, failure := range []string{"audit", "outbox"} {
		t.Run(failure, func(t *testing.T) {
			w := newWorld(t)
			supportFixtureConsent(w, w.A, true)
			pa := w.login("pa@example.test")
			conn, err := pgx.Connect(t.Context(), w.d.URL(db.RoleMigrator))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close(context.Background()) }()
			table, condition := "security_events", "NEW.event_type='platform_admin_account_changed'"
			if failure == "outbox" {
				table, condition = "outbox_events", "NEW.routing_key='tenant.support_admin_action'"
			}
			_, err = conn.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION gate1_fail_key_notification() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected failure'; END$$; CREATE TRIGGER gate1_fail_key_notification BEFORE INSERT ON %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION gate1_fail_key_notification()`, table, condition))
			if err != nil {
				t.Fatal(err)
			}
			before := w.count(`SELECT count(*) FROM api_keys`)
			expect(t, w.createKey(pa, map[string]any{"name": "rollback", "scope": "script", "tenantId": w.A, "capabilities": []string{"users:view"}}), 500, "internal")
			if w.count(`SELECT count(*) FROM api_keys`) != before {
				t.Fatal("key survived failed D2 append")
			}
			// Revocation reduces access even OFF, but still requires atomic audit/notification.
			key, _ := w.mustKey(w.login("ta.a@example.test"), map[string]any{"name": "revoke-rollback", "scope": "script", "tenantId": w.A, "capabilities": []string{"users:view"}})
			supportFixtureConsent(w, w.A, false)
			expect(t, w.send(http.MethodDelete, "/v1/api-keys/"+key, pa, nil), 500, "internal")
			if w.count(`SELECT count(*) FROM api_keys WHERE id=$1 AND revoked_at IS NULL`, key) != 1 || w.count(`SELECT count(*) FROM security_events WHERE event_type='api_key_revoked' AND details->>'keyId'=$1`, key) != 0 {
				t.Fatal("revocation survived failed D2 append")
			}
		})
	}
}

func TestGate1PlatformCredentialOperationsWithoutMembership(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	empty := w.user("membershipless@example.test")
	for _, uid := range []string{empty, w.userID("su@example.test"), w.userID("cu@example.test")} {
		expect(t, w.send(http.MethodPost, "/v1/users/"+uid+"/invite", pa, map[string]any{}), 202, "")
		expect(t, w.send(http.MethodPost, "/v1/users/"+uid+"/password/temporary", pa, map[string]any{}), 200, "")
	}
}

func TestGate1DualRoleNativeAdminKeepsMemberReadsAndConsent(t *testing.T) {
	w := newWorld(t)
	for _, email := range []string{"pa@example.test", "su@example.test"} {
		w.member(w.userID(email), w.A, "tenant_admin")
		tok := w.login(email)
		for _, path := range []string{"/v1/users", "/v1/users/" + w.userID("ta.a@example.test"), "/v1/tenants/" + w.A + "/members"} {
			r := w.get(path, tok)
			expect(t, r, 200, "")
			if !strings.Contains(string(r.raw), "ta.a@example.test") || strings.Contains(string(r.raw), "ta.c@example.test") {
				t.Fatalf("member contract: %s", r.raw)
			}
		}
		// Explicit platform reads retain target-consent masking even for the same native member.
		r := w.get("/v1/platform/tenants/"+w.A+"/members", tok)
		expect(t, r, 200, "")
		if strings.Contains(string(r.raw), "ta.a@example.test") {
			t.Fatal("platform endpoint inherited member authority")
		}
		path := "/v1/tenants/" + w.A + "/support-access"
		state := w.get(path, tok)
		expect(t, state, 200, "")
		if state.data().(map[string]any)["eligibleToToggle"] != true {
			t.Fatalf("native admin cannot toggle: %s", state.raw)
		}
		expect(t, w.send(http.MethodPut, path, tok, map[string]any{"enabled": true, "expectedVersion": state.data().(map[string]any)["version"]}), 200, "")
		supportFixtureConsent(w, w.A, false)
	}
	pa := w.login("pa@example.test")
	expect(t, w.send(http.MethodPost, "/v1/users/"+w.userID("ta.a@example.test")+"/password/temporary", pa, map[string]any{}), 403, "support_access_required")
	// An unratified platform grant cannot borrow either the member read exemption or consent authority.
	w.exec(`UPDATE memberships SET granted_by_platform=true,platform_grantor_user_id=$1,platform_granted_at=clock_timestamp(),ratified_at=NULL,ratified_by_user_id=NULL WHERE user_id=$1 AND tenant_id=$2`, w.userID("pa@example.test"), w.A)
	pa = w.login("pa@example.test")
	r := w.get("/v1/users", pa)
	expect(t, r, 200, "")
	if strings.Contains(string(r.raw), "ta.a@example.test") {
		t.Fatal("unratified platform grant unmasked member data")
	}
	state := w.get("/v1/tenants/"+w.A+"/support-access", pa)
	expect(t, state, 200, "")
	if state.data().(map[string]any)["eligibleToToggle"] != false {
		t.Fatal("unratified platform grant gained consent authority")
	}
}
