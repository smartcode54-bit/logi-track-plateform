//go:build integration

package auth_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scheduler"
)

func supportHarness(t *testing.T) (*harness, string, string, session) {
	t.Helper()
	h := newHarness(t, func(c *auth.Config) { c.SupportEntryEnabled = true })
	own := h.tenant("own_fleet", "Own")
	uid := h.user("support-admin@example.test")
	h.member(uid, own, "manager")
	h.exec(`INSERT INTO user_platform_roles(user_id,role)VALUES($1,'platform_admin')`, uid)
	return h, uid, own, h.mustLogin("support-admin@example.test", "web", "")
}

func supportSwitch(t *testing.T, h *harness, s session, mode, target string) session {
	t.Helper()
	in := map[string]any{"mode": mode}
	if target != "" {
		in["tenantId"] = target
	}
	r := h.post("/v1/auth/tenant", s.access, in)
	if r.status != 200 {
		t.Fatalf("%s: %d %s", mode, r.status, r.raw)
	}
	s.access = r.str("accessToken")
	return s
}

func TestSupportContextsAndVisitLifetime(t *testing.T) {
	h, uid, own, s := supportHarness(t)
	a, b := h.tenant("carrier", "A"), h.tenant("carrier", "B")
	old := s
	s = supportSwitch(t, h, s, "support", a)
	c := payload(t, s.access)
	if c["sm"] != "support" || c["rol"] != "support_readonly" || c["scv"] != "1" || c["sacv"] != "0" {
		t.Fatalf("support claims: %v", c)
	}
	if scalar[int](h, `SELECT count(*) FROM memberships WHERE user_id=$1`, uid) != 1 || scalar[string](h, `SELECT last_tenant_id::text FROM users WHERE id=$1`, uid) != own {
		t.Fatal("support changed membership or preference")
	}
	expectError(t, h.call(h.internal, http.MethodPatch, "/v1/me", old.access, map[string]any{"displayName": "stale write"}), 401, "token_expired")
	if r := h.get("/v1/me", s.access); r.status != 200 || r.data()["steward"] != false || r.data()["dispatcher"] != false {
		t.Fatalf("support me: %d %s", r.status, r.raw)
	}
	s = supportSwitch(t, h, s, "support", a)
	if scalar[int](h, `SELECT count(*) FROM support_visits WHERE actor_user_id=$1`, uid) != 1 {
		t.Fatal("retry duplicated entry")
	}
	s = h.mustRefresh(s)
	if payload(t, s.access)["sm"] != "support" {
		t.Fatal("refresh lost support")
	}
	s = supportSwitch(t, h, s, "support", b)
	if scalar[int](h, `SELECT count(*) FROM support_visits WHERE actor_user_id=$1 AND ended_at IS NULL`, uid) != 1 {
		t.Fatal("open visits not unique")
	}
	s = supportSwitch(t, h, s, "leave", "")
	if c := payload(t, s.access); c["tid"] != own || c["sm"] != nil || c["scv"] != "3" {
		t.Fatalf("leave context: %v", c)
	}
	if scalar[int](h, `SELECT count(*) FROM support_visits WHERE actor_user_id=$1 AND ended_at IS NOT NULL`, uid) != 2 {
		t.Fatal("leave failed to close visits")
	}
	s = supportSwitch(t, h, s, "support", a)
	h.clock.Advance(2 * time.Hour)
	if _, err := scheduler.TokenCleanup(context.Background(), h.pool, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if scalar[int](h, `SELECT count(*) FROM support_visits WHERE actor_user_id=$1 AND ended_at IS NULL`, uid) != 1 {
		t.Fatal("inactivity manufactured leave")
	}
	h.clock.Advance(8 * 24 * time.Hour)
	if _, err := scheduler.TokenCleanup(context.Background(), h.pool, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if scalar[int](h, `SELECT count(*) FROM support_visits WHERE actor_user_id=$1 AND end_reason='expired' AND end_recorded_at>ended_at`, uid) != 1 {
		t.Fatal("missed expiry not backfilled")
	}
}

func TestSupportEntryStatusesAndRefusals(t *testing.T) {
	h, uid, _, s := supportHarness(t)
	h.exec(`DELETE FROM memberships WHERE user_id=$1`, uid)
	// Refresh updates the real context before entry; no membership is manufactured.
	s = h.mustRefresh(s)
	for _, status := range []string{"active", "pending", "suspended"} {
		tid := h.tenant("carrier", status)
		h.exec(`UPDATE tenants SET status=$2 WHERE id=$1`, tid, status)
		s = supportSwitch(t, h, s, "support", tid)
	}
	if scalar[int](h, `SELECT count(*) FROM memberships WHERE user_id=$1`, uid) != 0 {
		t.Fatal("virtual membership inserted")
	}
	s = supportSwitch(t, h, s, "leave", "")
	if payload(t, s.access)["tid"] != nil {
		t.Fatal("nonmember leave did not restore platform-only")
	}
	expectError(t, h.post("/v1/auth/tenant", s.access, map[string]any{"mode": "support", "tenantId": "00000000-0000-7000-8000-00000000000f"}), 404, "not_found")
	h.exec(`DELETE FROM user_platform_roles WHERE user_id=$1`, uid)
	h.exec(`INSERT INTO user_platform_roles(user_id,role)VALUES($1,'support')`, uid)
	s = h.mustRefresh(s)
	expectError(t, h.post("/v1/auth/tenant", s.access, map[string]any{"mode": "support", "tenantId": h.tenant("carrier", "Refused")}), 403, "permission_denied")
}

func TestSupportAuditRollbackLimitsAndLogout(t *testing.T) {
	h, uid, _, s := supportHarness(t)
	a, b := h.tenant("carrier", "A"), h.tenant("carrier", "B")
	ctx := context.Background()
	migrator, err := pgx.Connect(ctx, h.d.URL(db.RoleMigrator))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrator.Close(ctx) }()
	ddl := func(sql string) {
		t.Helper()
		if _, err := migrator.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	ddl(`CREATE FUNCTION reject_support_event()RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.event_type='platform_support_entered' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW;END$$`)
	ddl(`CREATE TRIGGER reject_support_event BEFORE INSERT ON security_events FOR EACH ROW EXECUTE FUNCTION reject_support_event()`)
	r := h.post("/v1/auth/tenant", s.access, map[string]any{"mode": "support", "tenantId": a})
	if r.status < 500 {
		t.Fatalf("failed audit admitted: %d", r.status)
	}
	if scalar[int](h, `SELECT count(*) FROM support_visits WHERE actor_user_id=$1`, uid) != 0 || scalar[string](h, `SELECT access_mode FROM sessions WHERE id=$1`, s.sid) != "member" {
		t.Fatal("audit failure left session/visit changes")
	}
	ddl(`DROP TRIGGER reject_support_event ON security_events`)
	for i := 0; i < 9; i++ {
		target := a
		if i%2 != 0 {
			target = b
		}
		s = supportSwitch(t, h, s, "support", target)
	}
	r = h.post("/v1/auth/tenant", s.access, map[string]any{"mode": "support", "tenantId": b})
	expectError(t, r, 429, "resource_exhausted")
	if r.header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
	s = supportSwitch(t, h, s, "leave", "")
	h.clock.Advance(time.Minute)
	// GCRA uses Redis server time, independently of the auth fixture clock.
	if err := h.limiter.Reset(ctx, "support_switch", uid); err != nil {
		t.Fatal(err)
	}
	s = supportSwitch(t, h, s, "support", a)
	if r := h.post("/v1/auth/logout", s.access, map[string]any{"refreshToken": s.refresh}); r.status != 204 {
		t.Fatalf("logout: %d %s", r.status, r.raw)
	}
	if scalar[int](h, `SELECT count(*) FROM support_visits WHERE actor_user_id=$1 AND ended_at IS NULL`, uid) != 0 {
		t.Fatal("logout kept visit")
	}
}

func TestSupportRefreshRoleRemovalAndRevocationRace(t *testing.T) {
	h, uid, _, s := supportHarness(t)
	a := h.tenant("carrier", "A")
	s = supportSwitch(t, h, s, "support", a)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := h.svc.Refresh(ctx, auth.RefreshInput{RefreshToken: s.refresh})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := h.svc.Revoke(ctx, auth.Revocation{UserID: uuid.MustParse(uid), Reason: auth.RevokeAdmin})
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && httpCode(err) != auth.CodeSessionRevoked {
			t.Fatal(err)
		}
	}
	if scalar[int](h, `SELECT count(*) FROM support_visits WHERE actor_user_id=$1 AND ended_at IS NULL`, uid) != 0 {
		t.Fatal("refresh/revocation race reopened visit")
	}
	s = h.mustLogin("support-admin@example.test", "web", "")
	s = supportSwitch(t, h, s, "support", a)
	var pc *auth.PostCommit
	err := db.WithSystem(ctx, h.pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1`, uid); err != nil {
			return err
		}
		var err error
		pc, _, err = h.svc.RevokeInTx(ctx, tx, auth.Revocation{UserID: uuid.MustParse(uid), Reason: auth.RevokeClaimsChanged, BumpVersion: true})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Apply(ctx, pc)
	if scalar[int](h, `SELECT count(*) FROM support_visits WHERE actor_user_id=$1 AND ended_at IS NULL`, uid) != 0 {
		t.Fatal("role removal kept visit")
	}
	s = h.mustRefresh(s)
	if payload(t, s.access)["sm"] != nil {
		t.Fatal("role removal refresh kept support")
	}
}

func TestLegacyCredentialsAndSupportOwnAccountIsolation(t *testing.T) {
	h, uid, _, s := supportHarness(t)
	for _, platform := range []string{"web", "android", "ios"} {
		sess := h.mustLogin("support-admin@example.test", platform, "legacy-"+platform)
		c, err := h.keys.Parse(sess.access, h.clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		c.ContextVersion = ""
		legacy, err := h.keys.Sign(*c, h.clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.svc.Authenticate(context.Background(), legacy); err != nil {
			t.Fatalf("legacy %s: %v", platform, err)
		}
		sess = h.mustRefresh(sess)
		if payload(t, sess.access)["scv"] != "0" {
			t.Fatal("refresh omitted explicit version")
		}
	}
	a := h.tenant("carrier", "Person Name")
	h.exec(`UPDATE tenants SET legal_type='individual',code='PERSON-NAME' WHERE id=$1`, a)
	s = supportSwitch(t, h, s, "support", a)
	r := h.get("/v1/me", s.access)
	if r.status != 200 {
		t.Fatalf("me: %d %s", r.status, r.raw)
	}
	tenant := r.data()["tenant"].(map[string]any)
	if tenant["nameTh"] != "****" || tenant["code"] != nil || tenant["reference"] != "TEN-"+a {
		t.Fatalf("unsafe individual label: %v", tenant)
	}
	other := h.user("other@example.test")
	otherSession := h.mustLogin("other@example.test", "web", "")
	expectError(t, h.call(h.internal, http.MethodDelete, "/v1/me/sessions/"+otherSession.sid, s.access, nil), 404, "not_found")
	r = h.call(h.internal, http.MethodPatch, "/v1/me", s.access, map[string]any{"displayName": "Own Name"})
	if r.status != 200 {
		t.Fatalf("own patch: %d %s", r.status, r.raw)
	}
	if r := h.call(h.internal, http.MethodPut, "/v1/me/devices", otherSession.access, map[string]any{"installId": "other-install", "token": "held-token", "platform": "web"}); r.status != 204 {
		t.Fatalf("other device: %d %s", r.status, r.raw)
	}
	expectError(t, h.call(h.internal, http.MethodPut, "/v1/me/devices", s.access, map[string]any{"installId": "own-install", "token": "held-token", "platform": "web"}), 409, "already_exists")
	if r := h.call(h.internal, http.MethodDelete, "/v1/me/devices/other-install", s.access, nil); r.status != 204 {
		t.Fatalf("device deletion: %d %s", r.status, r.raw)
	}
	if scalar[int](h, `SELECT count(*) FROM device_tokens WHERE user_id=$1 AND install_id='other-install'`, other) != 1 {
		t.Fatal("support deleted another account's device")
	}
	if scalar[string](h, `SELECT display_name FROM users WHERE id=$1`, uid) != "Own Name" || scalar[string](h, `SELECT display_name FROM users WHERE id=$1`, other) != "other@example.test" {
		t.Fatal("own-account patch crossed ownership")
	}
}

func TestHolderPasswordCompletionAndLaterPlatformReset(t *testing.T) {
	h, actor, own, _ := supportHarness(t)
	uid := h.user("holder@example.test")
	h.member(uid, own, "tenant_admin")
	ctx := context.Background()
	actorID := uuid.MustParse(actor)
	set := func(value string) {
		t.Helper()
		var pc *auth.PostCommit
		err := db.WithSystem(ctx, h.pool, nil, func(tx pgx.Tx) error {
			var err error
			pc, err = h.svc.SetTemporaryPasswordInTx(ctx, tx, auth.TemporaryPassword{UserID: uuid.MustParse(uid), Password: value, Hash: h.hash(value), ActorID: &actorID})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		h.svc.Apply(ctx, pc)
	}
	set("platform temporary passphrase")
	if !scalar[bool](h, `SELECT credential_reset_by_platform_at IS NOT NULL AND credential_self_chosen_at IS NULL AND must_change_password FROM users WHERE id=$1`, uid) {
		t.Fatal("platform password missing provenance")
	}
	r := h.login("holder@example.test", "platform temporary passphrase", "web", "")
	expectError(t, r, 403, auth.CodePasswordChangeRequired)
	ticket, _ := r.details()["passwordChangeTicket"].(string)
	h.clock.Advance(time.Second)
	unchanged := h.post("/v1/auth/password/change", "", map[string]any{"passwordChangeTicket": ticket, "newPassword": "platform temporary passphrase"})
	expectError(t, unchanged, 422, "invalid_argument")
	if !scalar[bool](h, `SELECT must_change_password AND credential_self_chosen_at IS NULL FROM users WHERE id=$1`, uid) {
		t.Fatal("unchanged platform password became holder-chosen")
	}
	// Rejection must leave the ticket usable for a genuinely different password.
	if r := h.post("/v1/auth/password/change", "", map[string]any{"passwordChangeTicket": ticket, "newPassword": "holder self chosen passphrase"}); r.status != 204 {
		t.Fatalf("holder completion: %d %s", r.status, r.raw)
	}
	if !scalar[bool](h, `SELECT NOT must_change_password AND credential_self_chosen_at>credential_reset_by_platform_at FROM users WHERE id=$1`, uid) {
		t.Fatal("holder completion missing")
	}
	h.clock.Advance(time.Second)
	set("later platform temporary passphrase")
	if !scalar[bool](h, `SELECT must_change_password AND credential_reset_by_platform_at>credential_self_chosen_at FROM users WHERE id=$1`, uid) {
		t.Fatal("later platform reset did not reblock")
	}
}

func TestSupportRLSAndPooledGUCCleanup(t *testing.T) {
	h, _, _, s := supportHarness(t)
	tid := h.tenant("carrier", "RLS")
	s = supportSwitch(t, h, s, "support", tid)
	ctx := context.Background()
	p, err := h.svc.Authenticate(ctx, s.access)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	err = db.WithPrincipal(ctx, conn.Conn(), p, func(tx pgx.Tx) error {
		var tenant, role, steward, bypass, subs, dispatcher, readOnly string
		err := tx.QueryRow(ctx, `SELECT current_setting('app.tenant_id'),current_setting('app.role'),current_setting('app.steward'),current_setting('app.bypass_tenant'),current_setting('app.subtenant_ids'),current_setting('app.dispatcher'),current_setting('transaction_read_only')`).Scan(&tenant, &role, &steward, &bypass, &subs, &dispatcher, &readOnly)
		if err != nil {
			return err
		}
		if tenant != tid || role != "tenant_admin" || steward != "off" || bypass != "off" || subs != "" || dispatcher != "off" || readOnly != "on" {
			t.Fatalf("support GUCs: %s %s %s %s %s %s %s", tenant, role, steward, bypass, subs, dispatcher, readOnly)
		}
		_, err = tx.Exec(ctx, `UPDATE tenants SET name_th=name_th WHERE id=$1`, tid)
		return err
	})
	if err == nil {
		t.Fatal("support transaction admitted a write")
	}
	var tenant, role, steward, bypass string
	if err := conn.QueryRow(ctx, `SELECT coalesce(current_setting('app.tenant_id',true),''),coalesce(current_setting('app.role',true),''),coalesce(current_setting('app.steward',true),''),coalesce(current_setting('app.bypass_tenant',true),'')`).Scan(&tenant, &role, &steward, &bypass); err != nil {
		t.Fatal(err)
	}
	if tenant != "" || role != "" || steward == "on" || bypass == "on" {
		t.Fatalf("pooled GUCs leaked: %q %q %q %q", tenant, role, steward, bypass)
	}
}

func TestSupportConsentRefreshAndLostRedisNotification(t *testing.T) {
	h, uid, _, s := supportHarness(t)
	target := h.tenant("carrier", "Consent")
	s = supportSwitch(t, h, s, "support", target)
	ctx := context.Background()
	err := db.WithSystem(ctx, h.pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenants SET support_access_enabled=true,support_access_version=1,support_access_changed_at=$2,support_access_changed_by=$3 WHERE id=$1`, target, h.clock.Now(), uid)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	stale := h.get("/v1/me", s.access)
	expectError(t, stale, 401, auth.CodeTokenExpired)
	if stale.details()["reason"] != auth.ReasonClaimsChanged {
		t.Fatal("missing claims_changed")
	}
	s = h.mustRefresh(s)
	if payload(t, s.access)["sacv"] != "1" {
		t.Fatal("refresh omitted current consent version")
	}
	if scalar[int](h, `SELECT count(*) FROM support_visits WHERE actor_user_id=$1`, uid) != 1 {
		t.Fatal("consent refresh created a visit")
	}
	cfg := h.cfg()
	cfg.SupportEntryEnabled = true
	replica, broken, _ := h.replica(cfg)
	broken.down.Store(true)
	p, err := replica.Authenticate(ctx, s.access)
	if err != nil {
		t.Fatal(err)
	}
	if p.AccessMode != "support" || !p.SupportAccessEnabled {
		t.Fatal("Redis outage lost verified context")
	}
	// No realtime notification is published; the next admission must still observe the change.
	err = db.WithSystem(ctx, h.pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenants SET support_access_enabled=false,support_access_version=2,support_access_changed_at=$2 WHERE id=$1`, target, h.clock.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replica.Authenticate(ctx, s.access); httpCode(err) != auth.CodeTokenExpired {
		t.Fatalf("lost notification preserved disclosure: %v", err)
	}
}

func TestSupportTenantLabelCodesAndMetadata(t *testing.T) {
	h, uid, own, s := supportHarness(t)
	company, individual := h.tenant("carrier", "Company"), h.tenant("carrier", "Person")
	h.exec(`UPDATE tenants SET legal_type='company',code='PERSON-DERIVED-COMPANY' WHERE id=$1`, company)
	h.exec(`UPDATE tenants SET legal_type='individual',code='PERSON-DERIVED-INDIVIDUAL' WHERE id=$1`, individual)
	h.exec(`UPDATE tenants SET code='PERSON-DERIVED-OWN' WHERE id=$1`, own)
	for _, target := range []string{own, company, individual} {
		s = supportSwitch(t, h, s, "support", target)
		r := h.get("/v1/me", s.access)
		if r.status != 200 {
			t.Fatalf("me: %d %s", r.status, r.raw)
		}
		label := r.data()["tenant"].(map[string]any)
		if code, exists := label["code"]; !exists || code != nil || label["reference"] != "TEN-"+target {
			t.Fatalf("unreviewed code OFF: %v", label)
		}
		wantName := "Own"
		if target == company {
			wantName = "Company"
		}
		if target == individual {
			wantName = "****"
		}
		if label["nameTh"] != wantName {
			t.Fatalf("label name: %v", label)
		}
		meta := r.body["meta"].(map[string]any)
		found := false
		for _, field := range meta["redacted"].([]any) {
			if field.(map[string]any)["path"] == "/data/tenant/code" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing code redaction: %s", r.raw)
		}
		err := db.WithSystem(context.Background(), h.pool, nil, func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `UPDATE tenants SET support_access_enabled=true,support_access_version=1,support_access_changed_at=$2,support_access_changed_by=$3 WHERE id=$1`, target, h.clock.Now(), uid)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		s = h.mustRefresh(s)
		r = h.get("/v1/me", s.access)
		if r.status != 200 {
			t.Fatalf("consenting me: %d %s", r.status, r.raw)
		}
		wantCode := map[string]string{own: "PERSON-DERIVED-OWN", company: "PERSON-DERIVED-COMPANY", individual: "PERSON-DERIVED-INDIVIDUAL"}[target]
		if r.data()["tenant"].(map[string]any)["code"] != wantCode {
			t.Fatalf("consenting code: %s", r.raw)
		}
	}
}
