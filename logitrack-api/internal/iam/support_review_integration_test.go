//go:build integration

package iam_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

func grantVersion(w *world, tenant, user string) string {
	return w.id(`SELECT grant_version::text FROM memberships WHERE tenant_id=$1 AND user_id=$2`, tenant, user)
}

func TestSupportRatificationAPIAndMemberReadModel(t *testing.T) {
	w := newWorld(t)
	supportFixtureConsent(w, w.A, true)
	pa, actor := w.login("pa@example.test"), w.login("ta.a@example.test")
	target := w.user("ratify-target@example.test")
	path := "/v1/tenants/" + w.A + "/members/" + target
	expect(t, w.send(http.MethodPost, "/v1/tenants/"+w.A+"/members", pa, map[string]any{"email": "ratify-target@example.test", "role": "tenant_admin"}), 201, "")
	version := grantVersion(w, w.A, target)
	read := w.get("/v1/tenants/"+w.A+"/members", actor)
	expect(t, read, 200, "")
	found := false
	for _, v := range read.data().([]any) {
		m := v.(map[string]any)
		u := m["user"].(map[string]any)
		if u["id"] == target {
			found = true
			if m["eligibilityBasis"] != "pending" || m["eligibleToRatify"] != true || m["grantVersion"] != version || m["grantedByPlatform"] != true {
				t.Fatalf("ratification DTO: %s", read.raw)
			}
		}
		if u["id"] == w.userID("ta.a@example.test") && (m["eligibilityBasis"] != "member" || m["eligibleToRatify"] != false) {
			t.Fatalf("self DTO: %s", read.raw)
		}
	}
	if !found {
		t.Fatal("pending grant absent")
	}
	body := map[string]any{"expectedGrantVersion": version}
	for _, tok := range []string{pa, w.login("mg.a@example.test"), w.login("ratify-target@example.test")} {
		expect(t, w.send(http.MethodPost, path+"/ratify", tok, body), 403, "ratification_not_allowed")
	}
	n, _ := strconv.ParseInt(version, 10, 64)
	expect(t, w.send(http.MethodPost, path+"/ratify", actor, map[string]any{"expectedGrantVersion": strconv.FormatInt(n-1, 10)}), 409, "membership_grant_changed")
	before := w.securityEvents("platform_membership_ratified")
	expect(t, w.send(http.MethodPost, path+"/ratify", actor, body), 200, "")
	if w.securityEvents("platform_membership_ratified") != before+1 {
		t.Fatal("ratification audit missing")
	}
	expect(t, w.send(http.MethodPost, path+"/ratify", actor, body), 409, "membership_grant_changed")
	read = w.get("/v1/tenants/"+w.A+"/members", actor)
	expect(t, read, 200, "")
	for _, v := range read.data().([]any) {
		m := v.(map[string]any)
		if m["user"].(map[string]any)["id"] == target && (m["eligibilityBasis"] != "ratified" || m["eligibleToRatify"] != false || m["ratifiedAt"] == nil) {
			t.Fatalf("ratified DTO: %s", read.raw)
		}
	}
}

func TestSupportPlatformEmailReplacementCannotCreateConsentAuthority(t *testing.T) {
	w := newWorld(t)
	supportFixtureConsent(w, w.A, true)
	uid := w.userID("ta.a@example.test")
	pa := w.login("pa@example.test")
	before := w.count(`SELECT count(*) FROM outbox_events`)
	expect(t, w.send(http.MethodPatch, "/v1/users/"+uid, pa, map[string]any{"email": "accomplice@example.test"}), 403, "permission_denied")
	if got := w.id(`SELECT email::text FROM users WHERE id=$1`, uid); got != "ta.a@example.test" {
		t.Fatalf("admin email replaced: %s", got)
	}
	expect(t, w.send(http.MethodPost, "/v1/auth/password/forgot", "", map[string]any{"email": "accomplice@example.test"}), 202, "")
	if w.count(`SELECT count(*) FROM outbox_events`) != before {
		t.Fatal("refused replacement produced an invitation/reset or notification")
	}
	expect(t, w.loginWith("accomplice@example.test", pw), 401, "invalid_credentials")
}

func TestSupportONResetRevokesRatifierAndRepeatedResetsStayBlocked(t *testing.T) {
	w := newWorld(t)
	supportFixtureConsent(w, w.A, true)
	uid := w.userID("ta.a@example.test")
	actor := w.login("ta.a@example.test")
	pa := w.login("pa@example.test")
	target := w.user("pending-ratify@example.test")
	expect(t, w.send(http.MethodPost, "/v1/tenants/"+w.A+"/members", pa, map[string]any{"email": "pending-ratify@example.test", "role": "tenant_admin"}), 201, "")
	path := "/v1/tenants/" + w.A + "/members/" + target + "/ratify"
	for i := 0; i < 2; i++ {
		r := w.send(http.MethodPost, "/v1/users/"+uid+"/password/temporary", pa, map[string]any{})
		expect(t, r, 200, "")
		stale := w.send(http.MethodPost, path, actor, map[string]any{"expectedGrantVersion": grantVersion(w, w.A, target)})
		if stale.status != 401 {
			t.Fatalf("reset ratifier retained session authority: %d %s", stale.status, stale.raw)
		}
		password := r.data().(map[string]any)["temporaryPassword"].(string)
		expect(t, w.loginWith("ta.a@example.test", password), 403, "password_change_required")
		if w.count(`SELECT count(*) FROM memberships WHERE tenant_id=$1 AND user_id=$2 AND ratified_at IS NOT NULL`, w.A, target) != 0 {
			t.Fatal("reset ratified the accomplice")
		}
	}
}

func TestSupportActorEmailUsesActorOwnerConsent(t *testing.T) {
	w := newWorld(t)
	supportFixtureConsent(w, w.A, true)
	shared := w.user("shared-actor@example.test")
	w.member(shared, w.A, "operator")
	w.member(shared, w.B, "operator")
	global := w.user("global-actor@example.test")
	for _, actor := range []struct{ id, email string }{{w.userID("ta.a@example.test"), "ta.a@example.test"}, {w.userID("ta.c@example.test"), "ta.c@example.test"}, {shared, "shared-actor@example.test"}, {global, "global-actor@example.test"}} {
		w.exec(`INSERT INTO security_events(event_type,summary,tenant_id,actor_user_id,actor_email) VALUES('user_updated','safe event',$1,$2,$3)`, w.A, actor.id, actor.email)
	}
	for _, email := range []string{"pa@example.test", "su@example.test"} {
		r := w.get("/v1/platform/security-events?type=user_updated", w.login(email))
		expect(t, r, 200, "")
		if !strings.Contains(string(r.raw), "ta.a@example.test") {
			t.Fatalf("ON actor hidden: %s", r.raw)
		}
		for _, private := range []string{"ta.c@example.test", "shared-actor@example.test", "global-actor@example.test"} {
			if strings.Contains(string(r.raw), private) {
				t.Fatalf("foreign/shared/global actor leaked: %s", r.raw)
			}
		}
	}
}

func TestSupportSubcontractorsPreservePlatformAuthorizationAndMinimalFields(t *testing.T) {
	w := newWorld(t)
	w.exec(`UPDATE tenants SET contact_person='SENTINEL-contact',phone='SENTINEL-phone' WHERE id=$1`, w.A)
	for _, email := range []string{"pa@example.test", "su@example.test"} {
		tok := w.login(email)
		r := w.get("/v1/subcontractors?fields=minimal", tok)
		expect(t, r, 200, "")
		for _, v := range r.data().([]any) {
			m := v.(map[string]any)
			for _, field := range []string{"contactPerson", "phone", "email", "fleetSize", "createdAt"} {
				if _, ok := m[field]; ok {
					t.Fatalf("minimal DTO contains %s: %s", field, r.raw)
				}
			}
		}
		full := w.get("/v1/subcontractors", tok)
		if email == "pa@example.test" {
			expect(t, full, 200, "")
			if strings.Contains(string(full.raw), "SENTINEL") {
				t.Fatalf("OFF contact leaked: %s", full.raw)
			}
		} else {
			expect(t, full, 403, "permission_denied")
		}
	}
	// Visible company labels can be searched; masked individual labels and unreviewed codes cannot.
	w.exec(`UPDATE tenants SET legal_type='individual',name_th='SENTINEL-private-name',code='PrivateCode' WHERE id=$1`, w.C)
	for _, query := range []string{"SENTINEL-private-name", "PrivateCode"} {
		r := w.get("/v1/subcontractors?fields=minimal&q="+url.QueryEscape(query), w.login("pa@example.test"))
		expect(t, r, 200, "")
		if len(r.data().([]any)) != 0 {
			t.Fatalf("masked predicate matched: %s", r.raw)
		}
	}
	r := w.get("/v1/subcontractors?fields=minimal&q=A", w.login("su@example.test"))
	expect(t, r, 200, "")
	if len(r.data().([]any)) != 1 {
		t.Fatalf("visible company search: %s", r.raw)
	}
}

func TestSupportDualRoleMemberKeepsTenantAuditContract(t *testing.T) {
	w := newWorld(t)
	uid := w.userID("pa@example.test")
	w.member(uid, w.A, "tenant_admin")
	tok := w.login("pa@example.test")
	w.exec(`INSERT INTO security_events(event_type,summary,tenant_id)VALUES('support_access_disabled','tenant-visible',$1)`, w.A)
	expect(t, w.get("/v1/security-events?type=support_access_disabled", tok), 200, "")
	// A platform role alone does not substitute for the tenant's own audit permission.
	w.exec(`UPDATE memberships SET role='operator' WHERE user_id=$1 AND tenant_id=$2`, uid, w.A)
	expect(t, w.get("/v1/security-events", tok), 401, "token_expired")
	tok = w.login("pa@example.test")
	expect(t, w.get("/v1/security-events", tok), 403, "permission_denied")
}

func TestSupportPlatformReadEndpointPermissionsForBothRoles(t *testing.T) {
	w := newWorld(t)
	uid := w.userID("ta.a@example.test")
	w.login("ta.a@example.test")
	for _, email := range []string{"pa@example.test", "su@example.test"} {
		tok := w.login(email)
		for _, route := range []struct {
			path    string
			support bool
		}{{"/v1/platform/users", true}, {"/v1/platform/users/" + uid, true}, {"/v1/platform/tenants/" + w.A + "/members", true}, {"/v1/platform/security-events", true}, {"/v1/platform/users/" + uid + "/sessions", false}, {"/v1/platform/api-keys", false}, {"/v1/platform/security/overview", false}, {"/v1/platform/stats/users-by-role", false}} {
			r := w.get(route.path, tok)
			if email == "su@example.test" && !route.support {
				expect(t, r, 403, "permission_denied")
			} else {
				expect(t, r, 200, "")
			}
		}
	}
}

func TestSupportD2RollbackAndIdempotentRetry(t *testing.T) {
	for _, failure := range []string{"audit", "outbox"} {
		t.Run(failure, func(t *testing.T) {
			w := newWorld(t)
			supportFixtureConsent(w, w.A, true)
			uid := w.userID("ta.a@example.test")
			pa := w.login("pa@example.test")
			conn, err := pgx.Connect(context.Background(), w.d.URL(db.RoleMigrator))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close(context.Background()) }()
			table, condition := "security_events", "NEW.event_type='platform_admin_account_changed'"
			if failure == "outbox" {
				table, condition = "outbox_events", "NEW.routing_key='tenant.support_admin_action'"
			}
			_, err = conn.Exec(context.Background(), fmt.Sprintf(`CREATE FUNCTION review_fail_append() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected append failure'; END$$; CREATE TRIGGER review_fail_append BEFORE INSERT ON %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION review_fail_append()`, table, condition))
			if err != nil {
				t.Fatal(err)
			}
			beforeEvents, beforeOutbox := w.count(`SELECT count(*) FROM security_events`), w.count(`SELECT count(*) FROM outbox_events`)
			expect(t, w.send(http.MethodPost, "/v1/users/"+uid+"/disable", pa, map[string]any{}), 500, "internal")
			if w.id(`SELECT status FROM users WHERE id=$1`, uid) != "active" || w.count(`SELECT count(*) FROM security_events`) != beforeEvents || w.count(`SELECT count(*) FROM outbox_events`) != beforeOutbox {
				t.Fatal("failed required append did not roll back IAM")
			}
			if _, err := conn.Exec(context.Background(), "DROP TRIGGER review_fail_append ON "+table); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				expect(t, w.send(http.MethodPost, "/v1/users/"+uid+"/disable", pa, map[string]any{}), 204, "")
			}
			if w.count(`SELECT count(*) FROM security_events WHERE event_type='platform_admin_account_changed' AND target_user_id=$1`, uid) != 1 || w.count(`SELECT count(*) FROM outbox_events WHERE routing_key='tenant.support_admin_action'`) != 1 {
				t.Fatal("retry duplicated D2 audit/outbox")
			}
		})
	}
}

func TestSupportConsentAndGrantSerialize(t *testing.T) {
	w := newWorld(t)
	supportFixtureConsent(w, w.A, true)
	pa, admin := w.login("pa@example.test"), w.login("ta.a@example.test")
	target := w.user("race-grant@example.test")
	start := make(chan struct{})
	result := make(chan resp, 2)
	go func() {
		<-start
		result <- w.send(http.MethodPost, "/v1/tenants/"+w.A+"/members", pa, map[string]any{"email": "race-grant@example.test", "role": "tenant_admin"})
	}()
	go func() {
		<-start
		result <- w.send(http.MethodPut, "/v1/tenants/"+w.A+"/support-access", admin, map[string]any{"enabled": false, "expectedVersion": "1"})
	}()
	close(start)
	one, two := <-result, <-result
	for _, r := range []resp{one, two} {
		if r.status != 200 && r.status != 201 && r.code() != "support_access_required" {
			t.Fatalf("race: %d %s", r.status, r.raw)
		}
	}
	if w.count(`SELECT count(*) FROM tenants WHERE id=$1 AND NOT support_access_enabled`, w.A) != 1 {
		t.Fatal("OFF did not commit")
	}
	if w.count(`SELECT count(*) FROM memberships WHERE user_id=$1 AND tenant_id=$2 AND (NOT granted_by_platform OR ratified_at IS NOT NULL)`, target, w.A) != 0 {
		t.Fatal("race manufactured native or ratified grant")
	}
}

func TestSupportReadsDoNotTakeConsentOrMembershipWriteLocks(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.a@example.test")
	ctx := context.Background()
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.role','system',true),set_config('app.bypass_tenant','on',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM tenants WHERE id=$1 FOR UPDATE`, w.A); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT user_id FROM memberships WHERE tenant_id=$1 FOR UPDATE`, w.A); err != nil {
		t.Fatal(err)
	}
	done := make(chan resp, 1)
	go func() { done <- w.get("/v1/tenants/"+w.A+"/members", tok) }()
	select {
	case r := <-done:
		expect(t, r, 200, "")
	case <-time.After(2 * time.Second):
		_ = tx.Rollback(ctx)
		<-done
		t.Fatal("member list waited on write locks")
	}
	done = make(chan resp, 1)
	go func() { done <- w.get("/v1/tenants/"+w.A+"/support-access", tok) }()
	select {
	case r := <-done:
		expect(t, r, 200, "")
	case <-time.After(2 * time.Second):
		_ = tx.Rollback(ctx)
		<-done
		t.Fatal("consent GET waited on write locks")
	}
}

func TestSupportCrossTenantMembersDocumentsAndIDCards(t *testing.T) {
	w := newWorld(t)
	supportFixtureConsent(w, w.A, true)
	shared := w.user("SENTINEL-shared-member@example.test")
	w.member(shared, w.A, "operator")
	w.member(shared, w.B, "operator")
	w.exec(`UPDATE tenants SET legal_type='individual',id_card_number='1101700203450',contact_person='SENTINEL-carrier-contact' WHERE id=$1`, w.C)
	file := w.id(`INSERT INTO file_objects(bucket,object_key,tenant_id,purpose,owner_kind,owner_id,status,committed_at,storage_backend) VALUES('private','SENTINEL-object-key',$1,'tenant_document','tenant',$1,'committed',clock_timestamp(),'s3') RETURNING id::text`, w.C)
	w.exec(`INSERT INTO tenant_files(tenant_id,file_id,kind,position)VALUES($1,$2,'id_card',0)`, w.C, file)
	pa := w.login("pa@example.test")
	for _, email := range []string{"pa@example.test", "su@example.test"} {
		r := w.get("/v1/platform/tenants/"+w.A+"/members", w.login(email))
		expect(t, r, 200, "")
		if strings.Contains(string(r.raw), "SENTINEL-shared-member") {
			t.Fatalf("shared member leaked under A ON: %s", r.raw)
		}
	}
	for _, path := range []string{"/v1/tenants/" + w.C, "/v1/subcontractors/" + w.C} {
		r := w.get(path, pa)
		expect(t, r, 200, "")
		if strings.Contains(string(r.raw), "1101700203450") || strings.Contains(string(r.raw), "SENTINEL") {
			t.Fatalf("OFF tenant projection: %s", r.raw)
		}
	}
	supportFixtureConsent(w, w.C, true)
	r := w.get("/v1/tenants/"+w.C, pa)
	expect(t, r, 200, "")
	if r.data().(map[string]any)["idCardNumber"] != "1101700203450" {
		t.Fatalf("authorized ON id card hidden: %s", r.raw)
	}
	r = w.get("/v1/subcontractors/"+w.C, pa)
	expect(t, r, 200, "")
	for _, v := range r.data().(map[string]any)["documents"].([]any) {
		doc := v.(map[string]any)
		if doc["url"] != nil || doc["key"] != nil {
			t.Fatalf("public document bearer issued: %s", r.raw)
		}
	}
}

func TestSupportSessionRevocationDoesNotRequireD2Consent(t *testing.T) {
	w := newWorld(t)
	uid := w.userID("ta.a@example.test")
	old := w.login("ta.a@example.test")
	pa := w.login("pa@example.test")
	expect(t, w.send(http.MethodDelete, "/v1/users/"+uid+"/sessions", pa, nil), 204, "")
	expect(t, w.get("/v1/me", old), 401, "session_revoked")
	if w.count(`SELECT count(*) FROM outbox_events WHERE routing_key='tenant.support_admin_action'`) != 0 {
		t.Fatal("force logout entered D2 action list")
	}
}

func TestSupportStaleSessionCannotToggleOrRatify(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.a@example.test")
	uid := w.userID("ta.a@example.test")
	w.exec(`UPDATE sessions SET context_version=context_version+1 WHERE user_id=$1`, uid)
	expect(t, w.send(http.MethodPut, "/v1/tenants/"+w.A+"/support-access", tok, map[string]any{"enabled": true, "expectedVersion": "0"}), 401, "token_expired")
	if w.count(`SELECT count(*) FROM tenants WHERE id=$1 AND support_access_enabled`, w.A) != 0 {
		t.Fatal("stale session changed consent")
	}
}

func TestSupportPasswordCompletionRacingRepeatedReset(t *testing.T) {
	w := newWorld(t)
	supportFixtureConsent(w, w.A, true)
	uid := w.userID("ta.a@example.test")
	pa := w.login("pa@example.test")
	reset := w.send(http.MethodPost, "/v1/users/"+uid+"/password/temporary", pa, map[string]any{})
	expect(t, reset, 200, "")
	login := w.loginWith("ta.a@example.test", reset.data().(map[string]any)["temporaryPassword"].(string))
	expect(t, login, 403, "password_change_required")
	ticket := login.body["error"].(map[string]any)["details"].(map[string]any)["passwordChangeTicket"].(string)
	start := make(chan struct{})
	results := make(chan resp, 2)
	go func() {
		<-start
		results <- w.send(http.MethodPost, "/v1/auth/password/change", "", map[string]any{"passwordChangeTicket": ticket, "newPassword": "holder chooses a new password 42"})
	}()
	go func() {
		<-start
		results <- w.send(http.MethodPost, "/v1/users/"+uid+"/password/temporary", pa, map[string]any{})
	}()
	close(start)
	for i := 0; i < 2; i++ {
		r := <-results
		if r.status != 200 && r.status != 204 && r.status != 422 {
			t.Fatalf("completion/reset race: %d %s", r.status, r.raw)
		}
	}
	if w.count(`SELECT count(*) FROM users WHERE id=$1 AND must_change_password AND (credential_self_chosen_at IS NULL OR credential_reset_by_platform_at>credential_self_chosen_at)`, uid) != 1 {
		t.Fatal("an older completion restored authority after the latest reset")
	}
}

func TestSupportUserPagesSplitByNestedTenantOwnerBudget(t *testing.T) {
	w := newWorld(t)
	for i := 0; i < 60; i++ {
		uid := w.user(fmt.Sprintf("owner-budget-%02d@example.test", i))
		for j := 0; j < 2; j++ {
			tenant := w.carrier(fmt.Sprintf("Owner %02d-%d", i, j), nil)
			w.member(uid, tenant, "operator")
		}
	}
	tok := w.login("pa@example.test")
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 5; page++ {
		path := "/v1/platform/users?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		r := w.get(path, tok)
		expect(t, r, 200, "")
		owners := map[string]bool{}
		for _, v := range r.data().([]any) {
			u := v.(map[string]any)
			id := u["id"].(string)
			if seen[id] {
				t.Fatal("owner-budget split repeated user")
			}
			seen[id] = true
			for _, m := range u["memberships"].([]any) {
				owners[m.(map[string]any)["tenantId"].(string)] = true
			}
		}
		if len(owners) > 100 {
			t.Fatalf("page has %d distinct nested owners", len(owners))
		}
		cursor, _ = r.body["nextCursor"].(string)
		if cursor == "" {
			break
		}
	}
	if cursor != "" || len(seen) != w.count(`SELECT count(*) FROM users WHERE status<>'deleted'`) {
		t.Fatalf("owner-budget continuation lost users: %d", len(seen))
	}
}

func TestSupportAPIKeyCreatorAndDriverIdentityUseTheirTenant(t *testing.T) {
	w := newWorld(t)
	supportFixtureConsent(w, w.A, true)
	ta := w.login("ta.a@example.test")
	pa := w.login("pa@example.test")
	expect(t, w.createKey(ta, map[string]any{"name": "Tenant key", "scope": "script", "capabilities": []string{"users:view"}, "tenantId": w.A}), 201, "")
	uid := w.user("driver-ref@example.test")
	w.member(uid, w.A, "driver")
	w.exec(`UPDATE drivers SET user_id=$2 WHERE id=(SELECT id FROM drivers WHERE tenant_id=$1 LIMIT 1)`, w.A, uid)
	r := w.get("/v1/platform/api-keys?tenantId="+w.A, pa)
	expect(t, r, 200, "")
	if !strings.Contains(string(r.raw), "ta.a@example.test") {
		t.Fatalf("ON creator hard-masked: %s", r.raw)
	}
	r = w.get("/v1/platform/users/"+uid, pa)
	expect(t, r, 200, "")
	driver := r.data().(map[string]any)["driver"].(map[string]any)
	if driver["displayName"] == nil || driver["displayName"] == "****" {
		t.Fatalf("ON driver owner missing: %s", r.raw)
	}
	supportFixtureConsent(w, w.A, false)
	r = w.get("/v1/platform/api-keys?tenantId="+w.A, pa)
	expect(t, r, 200, "")
	if strings.Contains(string(r.raw), "ta.a@example.test") {
		t.Fatalf("OFF creator leaked: %s", r.raw)
	}
}
