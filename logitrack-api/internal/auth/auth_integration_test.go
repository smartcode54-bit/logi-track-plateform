//go:build integration

package auth_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
)

// AC1: the access token carries exactly sub, sid, ver, tid, rol, plt, dsp, drv, cs, amr, jti (+ iss, aud,
// iat, nbf, exp) with kid = the active key id; optional claims are absent when empty.
func TestLoginIssuesExactClaimSet(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	carrier := h.tenant("carrier", "Carrier A")
	x, y := h.party("CX"), h.party("CY")

	full := h.user("full@logitrack.test")
	h.member(full, carrier, "driver")
	drv := h.driver(full, carrier, "0811111111")
	h.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'support')`, full)
	h.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'dispatcher', $2), ($1, 'customer', $3)`, full, x, y)

	r := h.login("FULL@logitrack.test", pw, "android", "install-full")
	if r.status != http.StatusOK {
		t.Fatalf("login: %d %s", r.status, r.raw)
	}
	at := r.str("accessToken")
	if hd := header(t, at); hd["kid"] != h.keys.ActiveKID() || hd["alg"] != "EdDSA" {
		t.Fatalf("header = %v", hd)
	}
	c := payload(t, at)
	want := []string{"amr", "aud", "cs", "drv", "dsp", "exp", "iat", "iss", "jti", "nbf", "plt", "rol", "sid", "sub", "tid", "ver"}
	if got := keys(c); !slices.Equal(got, want) {
		t.Fatalf("claims = %v, want %v", got, want)
	}
	cs, _ := c["cs"].([]any)
	if c["sub"] != full || c["tid"] != carrier || c["rol"] != "driver" || c["drv"] != drv || c["dsp"] != true ||
		c["amr"] != "pwd" || c["ver"] != float64(1) || c["iss"] != issuer || len(cs) != 2 ||
		!slices.Contains(cs, any(x)) || !slices.Contains(cs, any(y)) {
		t.Fatalf("claim values = %v", c)
	}
	if plt, _ := c["plt"].([]any); len(plt) != 1 || plt[0] != "support" {
		t.Fatalf("plt = %v", c["plt"])
	}
	if exp, iat := c["exp"].(float64), c["iat"].(float64); exp-iat != 900 {
		t.Fatalf("access TTL = %v s", exp-iat)
	}
	if r.data()["expiresIn"] != float64(900) || len(r.str("refreshToken")) != 43 || r.data()["defaultTenantId"] != carrier {
		t.Fatalf("login body = %s", r.raw)
	}
	if active := scalar[string](h, `SELECT active_tenant_id::text FROM sessions WHERE id = $1`, c["sid"]); active != carrier {
		t.Fatalf("sessions.active_tenant_id = %s", active)
	}
	if n := len(h.outbox("user.logged_in")); n != 1 {
		t.Fatalf("user.logged_in events = %d", n)
	}
	if scalar[bool](h, `SELECT last_login_at IS NULL FROM users WHERE id = $1`, full) {
		t.Fatal("last_login_at not written")
	}

	staff := h.user("staff@logitrack.test")
	h.member(staff, own, "manager")
	s := h.mustLogin("staff@logitrack.test", "web", "")
	if got := keys(payload(t, s.access)); !slices.Equal(got, []string{"amr", "aud", "exp", "iat", "iss", "jti", "nbf", "rol", "sid", "sub", "tid", "ver"}) {
		t.Fatalf("staff claims = %v", got)
	}

	// A customer-scope principal has no tid / rol.
	cust := h.user("customer@logitrack.test")
	h.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'customer', $2)`, cust, x)
	s = h.mustLogin("customer@logitrack.test", "web", "")
	if got := keys(payload(t, s.access)); !slices.Equal(got, []string{"amr", "aud", "cs", "exp", "iat", "iss", "jti", "nbf", "sid", "sub", "ver"}) {
		t.Fatalf("customer claims = %v", got)
	}
}

// AC2: a rotated refresh token presented within 30 s of its rotation, while the successor is unused,
// yields a sibling pair; later it revokes the family and session, bumps auth_version, writes
// refresh_token_reuse and answers 401 session_revoked.
func TestRefreshRotationGraceAndReuse(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("tabs@logitrack.test")
	h.member(u, own, "manager")
	s1 := h.mustLogin("tabs@logitrack.test", "web", "")

	s2 := h.mustRefresh(s1) // tab 1 rotates rt1 -> rt2
	h.clock.Advance(10 * time.Second)
	r := h.refresh(s1.refresh) // tab 2 still holds rt1
	if r.status != http.StatusOK {
		t.Fatalf("grace refresh: %d %s", r.status, r.raw)
	}
	sibling := r.str("refreshToken")
	if sibling == s2.refresh || payload(t, r.str("accessToken"))["sid"] != s1.sid {
		t.Fatal("the grace path must issue a sibling in the same session")
	}
	if reason := scalar[string](h, `SELECT revoked_reason FROM refresh_tokens WHERE token_hash = $1`, sha(s2.refresh)); reason != "superseded" {
		t.Fatalf("earlier successor reason = %s", reason)
	}
	expectError(t, h.refresh(s2.refresh), http.StatusUnauthorized, auth.CodeInvalidToken)

	// 31 s after the first rotation the same token is reuse.
	h.clock.Advance(21 * time.Second)
	expectError(t, h.refresh(s1.refresh), http.StatusUnauthorized, auth.CodeSessionRevoked)
	if reason := scalar[string](h, `SELECT revoked_reason FROM sessions WHERE id = $1`, s1.sid); reason != "refresh_reuse" {
		t.Fatalf("session revoked_reason = %s", reason)
	}
	if live := scalar[int64](h, `SELECT count(*) FROM refresh_tokens WHERE family_id = $1 AND revoked_at IS NULL`, s1.sid); live != 0 {
		t.Fatalf("%d refresh tokens of the family are still live", live)
	}
	if v := scalar[int32](h, `SELECT auth_version FROM users WHERE id = $1`, u); v != 2 {
		t.Fatalf("auth_version = %d", v)
	}
	if sev := scalar[string](h, `SELECT severity FROM security_events WHERE event_type = 'refresh_token_reuse' AND target_user_id = $1`, u); sev != "critical" {
		t.Fatalf("security event severity = %s", sev)
	}
	ev := h.outbox("user.sessions_revoked")
	if len(ev) != 1 || ev[0]["reason"] != "refresh_reuse" || ev[0]["userId"] != u {
		t.Fatalf("outbox = %v", ev)
	}
	if topics := scalar[[]string](h, `SELECT realtime_topics FROM outbox_events WHERE routing_key = 'user.sessions_revoked'`); !slices.Equal(topics, []string{"user:" + u}) {
		t.Fatalf("topics = %v", topics)
	}
	expectError(t, h.get("/v1/me", s1.access), http.StatusUnauthorized, auth.CodeSessionRevoked)
	expectError(t, h.refresh(sibling), http.StatusUnauthorized, auth.CodeSessionRevoked)

	// Inside the 30 s, but the successor was already used: reuse as well.
	sA := h.mustLogin("tabs@logitrack.test", "web", "")
	sB := h.mustRefresh(sA)
	h.mustRefresh(sB)
	expectError(t, h.refresh(sA.refresh), http.StatusUnauthorized, auth.CodeSessionRevoked)
}

// AC3 (first half): tokens issued before a disable or a password event get 401 session_revoked.
func TestDisableAndPasswordEventsEndSessions(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("disable@logitrack.test")
	h.member(u, own, "operator")
	web := h.mustLogin("disable@logitrack.test", "web", "")
	mob := h.mustLogin("disable@logitrack.test", "android", "inst-1")
	for _, s := range []session{web, mob} {
		if r := h.get("/v1/me", s.access); r.status != http.StatusOK {
			t.Fatalf("before: %d %s", r.status, r.raw)
		}
	}
	// A status change plus RevokeInTx(disabled) in one transaction, the soft-delete path of internal/iam
	// (T19/T51, there with status deleted); a disable proper goes through SetStatusInTx. Either way the
	// sessions end here, and with the Firebase bridge on the account is disabled too (C.6.4).
	var pc *auth.PostCommit
	err := db.WithSystem(context.Background(), h.pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `UPDATE users SET status = 'disabled', disabled_at = now() WHERE id = $1`, u); err != nil {
			return err
		}
		var err error
		pc, _, err = h.svc.RevokeInTx(context.Background(), tx, auth.Revocation{UserID: uuid.MustParse(u), Reason: auth.RevokeDisabled, BumpVersion: true})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Apply(context.Background(), pc)
	for _, s := range []session{web, mob} {
		expectError(t, h.get("/v1/me", s.access), http.StatusUnauthorized, auth.CodeSessionRevoked)
		expectError(t, h.refresh(s.refresh), http.StatusUnauthorized, auth.CodeSessionRevoked)
	}
	expectError(t, h.login("disable@logitrack.test", pw, "web", ""), http.StatusForbidden, auth.CodeAccountDisabled)
	expectError(t, h.login("disable@logitrack.test", "wrong password!", "web", ""), http.StatusUnauthorized, auth.CodeInvalidCredentials)

	// Password change by the user: the other session ends, the current one refreshes once and stays.
	u2 := h.user("change@logitrack.test")
	h.member(u2, own, "operator")
	cur := h.mustLogin("change@logitrack.test", "web", "")
	other := h.mustLogin("change@logitrack.test", "ios", "inst-2")
	r := h.post("/v1/auth/password/change", cur.access, map[string]any{"currentPassword": "nope nope nope", "newPassword": "a brand new secret"})
	expectError(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r := h.post("/v1/auth/password/change", cur.access, map[string]any{"currentPassword": pw, "newPassword": "a brand new secret"}); r.status != http.StatusNoContent {
		t.Fatalf("change: %d %s", r.status, r.raw)
	}
	expectError(t, h.get("/v1/me", other.access), http.StatusUnauthorized, auth.CodeSessionRevoked)
	r = h.get("/v1/me", cur.access)
	expectError(t, r, http.StatusUnauthorized, auth.CodeTokenExpired)
	if r.details()["reason"] != auth.ReasonClaimsChanged {
		t.Fatalf("details = %v", r.details())
	}
	next := h.mustRefresh(cur)
	if r := h.get("/v1/me", next.access); r.status != http.StatusOK {
		t.Fatalf("after refresh: %d %s", r.status, r.raw)
	}
	if n := scalar[int64](h, `SELECT count(*) FROM security_events WHERE event_type = 'password_changed' AND target_user_id = $1`, u2); n != 1 {
		t.Fatalf("password_changed events = %d", n)
	}
	if scalar[bool](h, `SELECT password_changed_at IS NULL FROM users WHERE id = $1`, u2) {
		t.Fatal("password_changed_at not set")
	}
	expectError(t, h.login("change@logitrack.test", pw, "web", ""), http.StatusUnauthorized, auth.CodeInvalidCredentials)
	if r := h.login("change@logitrack.test", "a brand new secret", "web", ""); r.status != http.StatusOK {
		t.Fatalf("login with the new password: %d %s", r.status, r.raw)
	}
}

// AC3 (second half): after a role change the next request gets 401 token_expired with
// details.reason claims_changed, the session survives, and the refresh carries the new claims (R50).
func TestRoleChangeIsClaimsChanged(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("role@logitrack.test")
	h.member(u, own, "manager")
	s := h.mustLogin("role@logitrack.test", "web", "")
	if payload(t, s.access)["rol"] != "manager" {
		t.Fatal("rol")
	}
	var pc *auth.PostCommit
	if err := db.WithSystem(context.Background(), h.pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `UPDATE memberships SET role = 'operator' WHERE user_id = $1`, u); err != nil {
			return err
		}
		var err error
		pc, _, err = h.svc.RevokeInTx(context.Background(), tx, auth.Revocation{UserID: uuid.MustParse(u), Reason: auth.RevokeClaimsChanged, BumpVersion: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.svc.Apply(context.Background(), pc)

	r := h.get("/v1/me", s.access)
	expectError(t, r, http.StatusUnauthorized, auth.CodeTokenExpired)
	if r.details()["reason"] != "claims_changed" {
		t.Fatalf("details = %v", r.details())
	}
	if scalar[bool](h, `SELECT revoked_at IS NOT NULL FROM sessions WHERE id = $1`, s.sid) {
		t.Fatal("a claims change must not end the session")
	}
	next := h.mustRefresh(s)
	if c := payload(t, next.access); c["rol"] != "operator" || c["ver"] != float64(2) {
		t.Fatalf("refreshed claims = %v", c)
	}
	me := h.get("/v1/me", next.access)
	if me.status != http.StatusOK {
		t.Fatalf("me: %d %s", me.status, me.raw)
	}
	if tn, _ := me.data()["tenant"].(map[string]any); tn["role"] != "operator" {
		t.Fatalf("me.tenant = %v", me.data()["tenant"])
	}
	ev := h.outbox("user.sessions_revoked")
	if len(ev) != 1 || ev[0]["reason"] != "claims_changed" || len(ev[0]["sessionIds"].([]any)) != 0 {
		t.Fatalf("outbox = %v", ev)
	}
	// An expired token answers reason expired.
	h.clock.Advance(16 * time.Minute)
	r = h.get("/v1/me", next.access)
	expectError(t, r, http.StatusUnauthorized, auth.CodeTokenExpired)
	if r.details()["reason"] != "expired" {
		t.Fatalf("details = %v", r.details())
	}
}

// AC4: POST /v1/auth/tenant persists the tenant that refresh re-issues; a second login with the same
// installId revokes the earlier live session (R83).
func TestTenantSwitchAndDeviceRelogin(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	carrier := h.tenant("carrier", "Carrier A")
	other := h.tenant("carrier", "Carrier B")
	u := h.user("multi@logitrack.test")
	h.member(u, own, "manager")
	h.member(u, carrier, "tenant_admin")

	s := h.mustLogin("multi@logitrack.test", "web", "")
	if payload(t, s.access)["tid"] != own {
		t.Fatal("the own fleet is the first default")
	}
	r := h.post("/v1/auth/tenant", s.access, map[string]any{"tenantId": carrier})
	if r.status != http.StatusOK {
		t.Fatalf("tenant: %d %s", r.status, r.raw)
	}
	if c := payload(t, r.str("accessToken")); c["tid"] != carrier || c["rol"] != "tenant_admin" || c["sid"] != s.sid {
		t.Fatalf("switched claims = %v", c)
	}
	if r.data()["refreshToken"] != nil {
		t.Fatal("the tenant switch keeps the refresh token")
	}
	if got := scalar[string](h, `SELECT active_tenant_id::text FROM sessions WHERE id = $1`, s.sid); got != carrier {
		t.Fatalf("active_tenant_id = %s", got)
	}
	if c := payload(t, h.mustRefresh(s).access); c["tid"] != carrier || c["rol"] != "tenant_admin" {
		t.Fatalf("refresh re-issued %v", c)
	}
	expectError(t, h.post("/v1/auth/tenant", s.access, map[string]any{"tenantId": other}), http.StatusForbidden, auth.CodePermissionDenied)
	expectError(t, h.post("/v1/auth/tenant", s.access, map[string]any{"tenantId": "nope"}), http.StatusUnprocessableEntity, "invalid_argument")
	// The next login starts in the tenant the user worked in last.
	if c := payload(t, h.mustLogin("multi@logitrack.test", "web", "").access); c["tid"] != carrier {
		t.Fatalf("default tenant of a new login = %v", c["tid"])
	}

	first := h.mustLogin("multi@logitrack.test", "android", "install-1")
	second := h.mustLogin("multi@logitrack.test", "android", "install-1")
	if reason := scalar[string](h, `SELECT coalesce(revoked_reason, '') FROM sessions WHERE id = $1`, first.sid); reason != "device_relogin" {
		t.Fatalf("first session reason = %q", reason)
	}
	expectError(t, h.get("/v1/me", first.access), http.StatusUnauthorized, auth.CodeSessionRevoked)
	expectError(t, h.refresh(first.refresh), http.StatusUnauthorized, auth.CodeSessionRevoked)
	if r := h.get("/v1/me", second.access); r.status != http.StatusOK {
		t.Fatalf("second session: %d %s", r.status, r.raw)
	}
	third := h.mustLogin("multi@logitrack.test", "android", "install-2")
	if r := h.get("/v1/me", third.access); r.status != http.StatusOK || h.get("/v1/me", second.access).status != http.StatusOK {
		t.Fatal("another install keeps both sessions")
	}
	if n := scalar[int64](h, `SELECT count(*) FROM sessions WHERE user_id = $1 AND install_id = 'install-1' AND revoked_at IS NULL`, u); n != 1 {
		t.Fatalf("live sessions on install-1 = %d", n)
	}
}

// AC5: a Firebase-scrypt user's first login verifies the legacy hash, stores an Argon2id PHC hash and
// NULLs legacy_scrypt_hash / legacy_scrypt_salt (password_changed_at untouched: the plaintext is the same).
func TestScryptUserIsRehashedOnFirstLogin(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	salt := []byte("0123456789abcdef")
	legacy, err := firebasescrypt.Hash(pw, salt, h.scrypt)
	if err != nil {
		t.Fatal(err)
	}
	u := scalar[string](h, `INSERT INTO users (email, legacy_auth_uid, legacy_scrypt_hash, legacy_scrypt_salt)
		VALUES ('legacy@logitrack.test', 'SEEDUID000000000000000000D1', $1, $2) RETURNING id::text`, legacy, salt)
	h.member(u, own, "operator")

	expectError(t, h.login("legacy@logitrack.test", "not the password", "web", ""), http.StatusUnauthorized, auth.CodeInvalidCredentials)
	if scalar[bool](h, `SELECT password_hash IS NOT NULL FROM users WHERE id = $1`, u) {
		t.Fatal("a failed login must not touch the hash")
	}
	h.mustLogin("legacy@logitrack.test", "web", "")
	var phc string
	var nullHash, nullSalt, nullChanged bool
	if err := h.etl.QueryRow(context.Background(), `SELECT password_hash, legacy_scrypt_hash IS NULL, legacy_scrypt_salt IS NULL,
		password_changed_at IS NULL FROM users WHERE id = $1`, u).Scan(&phc, &nullHash, &nullSalt, &nullChanged); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$") || !nullHash || !nullSalt || !nullChanged {
		t.Fatalf("after rehash: hash %.16s, legacy NULL %v/%v, password_changed_at NULL %v", phc, nullHash, nullSalt, nullChanged)
	}
	h.mustLogin("legacy@logitrack.test", "web", "") // now through Argon2id

	// Without FIREBASE_SCRYPT_* a legacy user cannot sign in with a password.
	h2 := newHarness(t, func(c *auth.Config) { c.Scrypt = nil })
	h2.tenant("own_fleet", "Own")
	scalar[string](h2, `INSERT INTO users (email, legacy_scrypt_hash, legacy_scrypt_salt) VALUES ('legacy@logitrack.test', $1, $2)
		RETURNING id::text`, legacy, salt)
	expectError(t, h2.login("legacy@logitrack.test", pw, "web", ""), http.StatusUnauthorized, auth.CodeInvalidCredentials)
}

// AC6 (first half): 5 failed logins per email within 15 min lock the email (423), even for the right
// password; login_failed / login_lockout go to the security.audit consumer.
func TestLockoutAfterFiveFailures(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("lock@logitrack.test")
	h.member(u, own, "operator")
	for i := range 5 {
		r := h.login("lock@logitrack.test", "wrong password "+string(rune('a'+i)), "web", "")
		expectError(t, r, http.StatusUnauthorized, auth.CodeInvalidCredentials)
	}
	r := h.login("lock@logitrack.test", pw, "web", "")
	expectError(t, r, http.StatusLocked, auth.CodeLocked)
	if ra := r.header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Fatalf("Retry-After = %q", ra)
	}
	var failed, lockout int
	for _, ev := range h.outbox("security.event") {
		switch ev["eventType"] {
		case "login_failed":
			failed++
		case "login_lockout":
			lockout++
		}
	}
	if failed != 5 || lockout != 1 {
		t.Fatalf("security.event: %d login_failed, %d login_lockout", failed, lockout)
	}
	// Unknown emails lock the same way; other emails are unaffected.
	for range 5 {
		expectError(t, h.login("nobody@logitrack.test", "whatever it is", "web", ""), http.StatusUnauthorized, auth.CodeInvalidCredentials)
	}
	expectError(t, h.login("nobody@logitrack.test", "whatever it is", "web", ""), http.StatusLocked, auth.CodeLocked)
	other := h.user("other@logitrack.test")
	h.member(other, own, "operator")
	h.mustLogin("other@logitrack.test", "web", "")
}

// AC6 (second half): forgot-password is always 202 and the api inserts no token; the token issued for
// the email (by notify.email through IssuePasswordResetToken) is stored only as a hash, works once and
// revokes every session.
func TestForgotAndResetPassword(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("forgot@logitrack.test")
	h.member(u, own, "operator")
	dis := h.user("disabled@logitrack.test")
	h.exec(`UPDATE users SET status = 'disabled', disabled_at = now() WHERE id = $1`, dis)
	tail := scalar[string](h, `INSERT INTO users (email, status) VALUES ('tail@logitrack.test', 'reset_required') RETURNING id::text`)
	live := h.mustLogin("forgot@logitrack.test", "web", "")

	for _, email := range []string{"forgot@logitrack.test", "disabled@logitrack.test", "unknown@logitrack.test", "tail@logitrack.test", "", "not an email"} {
		r := h.post("/v1/auth/password/forgot", "", map[string]any{"email": email, "locale": "en"})
		if r.status != http.StatusAccepted {
			t.Fatalf("forgot %q: %d %s", email, r.status, r.raw)
		}
	}
	ev := h.outbox("auth.password_reset_requested")
	if len(ev) != 2 || ev[0]["userId"] != u || ev[1]["userId"] != tail || ev[0]["purpose"] != "reset" || ev[0]["locale"] != "en" {
		t.Fatalf("auth.password_reset_requested = %v", ev)
	}
	if n := scalar[int64](h, `SELECT count(*) FROM password_reset_tokens`); n != 0 {
		t.Fatalf("the api inserted %d reset tokens", n)
	}

	// notify.email (T10) issues the token inside its transaction.
	var tok string
	if err := db.WithSystem(context.Background(), h.pool, nil, func(tx pgx.Tx) error {
		var err error
		tok, err = h.svc.IssuePasswordResetToken(context.Background(), tx, uuid.MustParse(u), auth.PurposeReset, "203.0.113.9", nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := scalar[int64](h, `SELECT count(*) FROM password_reset_tokens WHERE token_hash = $1 AND user_id = $2`, sha(tok), u); n != 1 {
		t.Fatal("the stored value must be sha256(token)")
	}
	if row := scalar[string](h, `SELECT row_to_json(t)::text FROM password_reset_tokens t`); strings.Contains(row, tok) {
		t.Fatal("the token itself was stored")
	}

	expectError(t, h.post("/v1/auth/password/reset", "", map[string]any{"token": tok, "newPassword": "short"}),
		http.StatusUnprocessableEntity, "invalid_argument")
	if r := h.post("/v1/auth/password/reset", "", map[string]any{"token": tok, "newPassword": "my fresh password"}); r.status != http.StatusNoContent {
		t.Fatalf("reset: %d %s", r.status, r.raw)
	}
	r := h.post("/v1/auth/password/reset", "", map[string]any{"token": tok, "newPassword": "my other password"})
	expectError(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	expectError(t, h.get("/v1/me", live.access), http.StatusUnauthorized, auth.CodeSessionRevoked)
	if n := scalar[int64](h, `SELECT count(*) FROM security_events WHERE event_type = 'password_reset_completed' AND target_user_id = $1`, u); n != 1 {
		t.Fatalf("password_reset_completed = %d", n)
	}
	expectError(t, h.login("forgot@logitrack.test", pw, "web", ""), http.StatusUnauthorized, auth.CodeInvalidCredentials)
	if r := h.login("forgot@logitrack.test", "my fresh password", "web", ""); r.status != http.StatusOK {
		t.Fatalf("login after reset: %d %s", r.status, r.raw)
	}

	// An expired token is refused.
	if err := db.WithSystem(context.Background(), h.pool, nil, func(tx pgx.Tx) error {
		var err error
		tok, err = h.svc.IssuePasswordResetToken(context.Background(), tx, uuid.MustParse(tail), auth.PurposeReset, "", nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(31 * time.Minute)
	expectError(t, h.post("/v1/auth/password/reset", "", map[string]any{"token": tok, "newPassword": "my fresh password"}),
		http.StatusUnprocessableEntity, "invalid_argument")
}

// AC7: a must_change_password login gets 403 password_change_required with a single-use ticket and no
// token; the ticket works once at POST /v1/auth/password/change (R79).
func TestMustChangePasswordTicket(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("temp@logitrack.test")
	h.member(u, own, "driver")
	h.driver(u, own, "0899999999")
	h.exec(`UPDATE users SET must_change_password = true WHERE id = $1`, u)

	r := h.login("temp@logitrack.test", pw, "android", "inst-temp")
	expectError(t, r, http.StatusForbidden, auth.CodePasswordChangeRequired)
	ticket, _ := r.details()["passwordChangeTicket"].(string)
	if len(ticket) != 43 || r.details()["expiresIn"] != float64(600) {
		t.Fatalf("details = %v", r.details())
	}
	if n := scalar[int64](h, `SELECT count(*) FROM sessions WHERE user_id = $1`, u); n != 0 {
		t.Fatalf("%d sessions created for a must-change login", n)
	}
	// A rejected password does not burn the ticket.
	r = h.post("/v1/auth/password/change", "", map[string]any{"passwordChangeTicket": ticket, "newPassword": "0899999999"})
	expectError(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	r = h.post("/v1/auth/password/change", "", map[string]any{"passwordChangeTicket": ticket, "newPassword": "short"})
	expectError(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	fields, _ := r.details()["fields"].([]any)
	if f, _ := fields[0].(map[string]any); f["reason"] != "too_short" || f["field"] != "newPassword" {
		t.Fatalf("fields = %v", fields)
	}
	if r := h.post("/v1/auth/password/change", "", map[string]any{"passwordChangeTicket": ticket, "newPassword": "now my own password"}); r.status != http.StatusNoContent {
		t.Fatalf("ticket change: %d %s", r.status, r.raw)
	}
	r = h.post("/v1/auth/password/change", "", map[string]any{"passwordChangeTicket": ticket, "newPassword": "now my own password"})
	expectError(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if must := scalar[bool](h, `SELECT must_change_password FROM users WHERE id = $1`, u); must {
		t.Fatal("must_change_password still set")
	}
	if r := h.login("temp@logitrack.test", "now my own password", "android", "inst-temp"); r.status != http.StatusOK {
		t.Fatalf("login after the change: %d %s", r.status, r.raw)
	}
}

// AC8: PASSWORD_MIN_LENGTH -> 422 on every password-setting route, and the generic outcomes of login.
func TestPolicyAndLoginOutcomes(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("policy@logitrack.test")
	h.member(u, own, "operator")
	s := h.mustLogin("policy@logitrack.test", "web", "")
	r := h.post("/v1/auth/password/change", s.access, map[string]any{"currentPassword": pw, "newPassword": "123456789"})
	expectError(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	fields, _ := r.details()["fields"].([]any)
	if f, _ := fields[0].(map[string]any); f["reason"] != "too_short" {
		t.Fatalf("fields = %v", fields)
	} else if p, _ := f["params"].(map[string]any); p["min"] != float64(10) {
		t.Fatalf("params = %v", f["params"])
	}
	expectError(t, h.post("/v1/auth/password/change", s.access, map[string]any{"currentPassword": pw, "newPassword": "Password123"}),
		http.StatusUnprocessableEntity, "invalid_argument")

	expectError(t, h.login("policy@logitrack.test", pw, "desktop", ""), http.StatusUnprocessableEntity, "invalid_argument")
	expectError(t, h.post("/v1/auth/login", "", "not an object"), http.StatusBadRequest, "bad_request")
	expectError(t, h.login("nobody@logitrack.test", pw, "web", ""), http.StatusUnauthorized, auth.CodeInvalidCredentials)
	h.exec(`UPDATE users SET status = 'reset_required' WHERE id = $1`, u)
	expectError(t, h.login("policy@logitrack.test", pw, "web", ""), http.StatusUnauthorized, auth.CodeInvalidCredentials)

	drv := h.user("noprofile@logitrack.test")
	h.member(drv, own, "driver")
	expectError(t, h.login("noprofile@logitrack.test", pw, "android", "x"), http.StatusForbidden, auth.CodeDriverProfileRequired)

	expectError(t, h.get("/v1/me", ""), http.StatusUnauthorized, "unauthenticated")
	expectError(t, h.get("/v1/me", "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.c2ln"), http.StatusUnauthorized, auth.CodeInvalidToken)
	expectError(t, h.refresh("not-a-token"), http.StatusUnauthorized, auth.CodeInvalidToken)
}

// The request buckets: login_ip from RATE_LIMIT_LOGIN answers 429 with Retry-After and details.bucket,
// like the rate-limit middleware (Appendix B §B.6.3).
func TestLoginIPRateLimit(t *testing.T) {
	h := newHarness(t, func(c *auth.Config) { c.LoginIP = ratelimit.Limit{Count: 2, Window: time.Minute} })
	own := h.tenant("own_fleet", "Own")
	u := h.user("rate@logitrack.test")
	h.member(u, own, "operator")
	h.mustLogin("rate@logitrack.test", "web", "")
	h.mustLogin("rate@logitrack.test", "web", "")
	r := h.login("rate@logitrack.test", pw, "web", "")
	expectError(t, r, http.StatusTooManyRequests, "resource_exhausted")
	if ra := r.header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Fatalf("Retry-After = %q", ra)
	}
	if d := r.details(); d["bucket"] != ratelimit.LoginIP.Name || d["retryAfterSeconds"] == nil {
		t.Fatalf("details = %v", d)
	}
}

// login_ip runs through the process's shared GCRA limiter (Appendix C §C.4.12), not a counter of its
// own: a parallel burst from one client is cut at the bucket's count, its state is one GCRA key under
// the hashed subject, budget another holder of the limiter spent (a replica, a middleware rule of the
// same bucket) counts against the login, IPv6 clients share their /64, and a replica that cannot
// reach Redis lets the login through.
func TestLoginIPBurstGoesThroughTheSharedLimiter(t *testing.T) {
	lim := ratelimit.Limit{Count: 3, Window: time.Minute}
	h := newHarness(t, func(c *auth.Config) { c.LoginIP = lim })
	own := h.tenant("own_fleet", "Own")
	const email = "burst-ip@logitrack.test"
	h.member(h.user(email), own, "operator")
	ctx := context.Background()

	const n = 12
	out := make([]resp, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			out[i], errs[i] = h.do(h.internal, http.MethodPost, "/v1/auth/login", "", map[string]any{
				"email": email, "password": pw, "platform": "web"})
		})
	}
	close(start)
	wg.Wait()
	count := map[int]int{}
	for i, r := range out {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		count[r.status]++
		if r.status == http.StatusTooManyRequests {
			checkEnvelope(t, r)
			if r.code() != "resource_exhausted" || r.details()["bucket"] != ratelimit.LoginIP.Name || r.header.Get("Retry-After") == "" {
				t.Fatalf("429: %s (Retry-After %q)", r.raw, r.header.Get("Retry-After"))
			}
		}
	}
	if count[http.StatusOK] != lim.Count || count[http.StatusTooManyRequests] != n-lim.Count {
		t.Fatalf("answers %v: want %d x 200 and %d x 429", count, lim.Count, n-lim.Count)
	}
	if v := labeledCounter(t, h.limits, "ratelimit_decisions_total", "login_ip", "denied"); v != float64(n-lim.Count) {
		t.Fatalf("limiter denials = %v, want %d", v, n-lim.Count)
	}
	if v := labeledCounter(t, h.limits, "ratelimit_decisions_total", "login_ip", "allowed"); v != float64(lim.Count) {
		t.Fatalf("limiter admissions = %v, want %d", v, lim.Count)
	}

	// One GCRA key (a theoretical arrival time in µs of the Redis clock, not an INCR counter) under a
	// 16-byte sha256-hex subject; no key names the client address.
	keys := h.rdb.Keys(ctx, h.ks.Pattern(cache.NSRateLimit, ratelimit.LoginIP.Name)).Val()
	if len(keys) != 1 {
		t.Fatalf("login_ip keys %v, want one", keys)
	}
	subject := strings.TrimPrefix(keys[0], h.ks.Key(cache.NSRateLimit, ratelimit.LoginIP.Name)+":")
	if len(subject) != 32 || strings.Trim(subject, "0123456789abcdef") != "" {
		t.Fatalf("subject %q is not a 16-byte sha256 hex", subject)
	}
	if tat, err := h.rdb.Get(ctx, keys[0]).Int64(); err != nil || tat < time.Now().Add(-time.Hour).UnixMicro() {
		t.Fatalf("login_ip state %d (%v) is not a GCRA arrival time", tat, err)
	}
	for _, k := range cachetest.Keys(t, h.rdb) {
		if strings.Contains(k, "127.0.0.1") || strings.Contains(k, "::1") {
			t.Fatalf("a key names the client address: %s", k)
		}
	}

	// Budget spent through another holder of the limiter counts: the service keeps no budget of its own.
	other := ratelimit.New(h.rdb, h.ks, zerolog.Nop())
	if d, err := other.AllowN(ctx, ratelimit.LoginIP.Name, ratelimit.IPSubject("2001:db8:7::5"), lim, lim.Count); err != nil || !d.Allowed {
		t.Fatalf("spend: %+v %v", d, err)
	}
	login := func(svc *auth.Service, ip string) error {
		_, err := svc.Login(ctx, auth.LoginInput{Email: email, Password: pw, Platform: "web", IP: ip})
		return err
	}
	if err := login(h.svc, "2001:db8:7::9"); httpCode(err) != "resource_exhausted" { // same /64
		t.Fatalf("same /64 after the budget was spent elsewhere: %v", err)
	}
	if err := login(h.svc, "2001:db8:8::9"); err != nil { // another /64
		t.Fatalf("another /64: %v", err)
	}

	// Fail open: a replica whose Redis fails lets the login through although the budget is spent.
	lost, f, _ := h.replica(h.cfg())
	f.down.Store(true)
	if err := login(lost, "2001:db8:7::9"); err != nil {
		t.Fatalf("the limiter must fail open on a Redis error: %v", err)
	}
}

// labeledCounter is the value of the counter name with labels bucket and outcome.
func labeledCounter(t *testing.T, reg *prometheus.Registry, name, bucket, outcome string) float64 {
	t.Helper()
	mf, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range mf {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["bucket"] == bucket && labels["outcome"] == outcome {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// With Redis unreachable the per-request check runs in PostgreSQL and never fails open (C.4.3).
func TestRevocationFallsBackToPostgres(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("fallback@logitrack.test")
	h.member(u, own, "operator")
	s := h.mustLogin("fallback@logitrack.test", "web", "")

	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1, DialerRetries: 1})
	t.Cleanup(func() { _ = dead.Close() })
	svc, keys := h.service(auth.Config{RefreshTTLWeb: time.Hour, RefreshTTLMobile: time.Hour, PasswordResetTTL: time.Hour}, dead)
	if keys != h.keys {
		t.Fatal("the second service must verify the same tokens")
	}
	reg := prometheus.NewRegistry()
	if err := svc.Register(reg); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.Authenticate(ctx, s.access); err != nil {
		t.Fatalf("live session through PostgreSQL: %v", err)
	}
	// A claims change is seen without Redis...
	if _, err := svc.Revoke(ctx, auth.Revocation{UserID: uuid.MustParse(u), Reason: auth.RevokeClaimsChanged, BumpVersion: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, s.access); err == nil || !strings.Contains(err.Error(), auth.CodeTokenExpired) {
		t.Fatalf("want token_expired, got %v", err)
	}
	// ...and so is a revoked session.
	next := h.mustRefresh(s)
	if _, err := svc.Revoke(ctx, auth.Revocation{UserID: uuid.MustParse(u), Reason: auth.RevokeAdmin}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, next.access); err == nil || !strings.Contains(err.Error(), auth.CodeSessionRevoked) {
		t.Fatalf("want session_revoked, got %v", err)
	}
	if v := counterValue(t, reg, "auth_revocation_fallback_total"); v < 3 {
		t.Fatalf("auth_revocation_fallback_total = %v", v)
	}
	// The dead Redis also lost the post-commit writes of both revocations; they are counted.
	if v := counterValue(t, reg, "auth_postcommit_failures_total"); v < 2 {
		t.Fatalf("auth_postcommit_failures_total = %v", v)
	}
}

// GET/PATCH /v1/me, tenants, sessions, logout, logout-all and the SSE ticket.
func TestMeSessionsLogoutAndSSETicket(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	carrier := h.tenant("carrier", "Carrier A")
	x := h.party("CX")
	u := h.user("me@logitrack.test")
	h.member(u, own, "manager")
	h.member(u, carrier, "operator")
	h.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, u)
	h.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'customer', $2)`, u, x)

	web := h.mustLogin("me@logitrack.test", "web", "")
	mob := h.mustLogin("me@logitrack.test", "android", "inst-me")

	me := h.get("/v1/me", web.access)
	if me.status != http.StatusOK {
		t.Fatalf("me: %d %s", me.status, me.raw)
	}
	d := me.data()
	tn, _ := d["tenant"].(map[string]any)
	if d["id"] != u || d["email"] != "me@logitrack.test" || tn["id"] != own || tn["role"] != "manager" || tn["kind"] != "own_fleet" ||
		d["steward"] != true || d["mustChangePassword"] != false || len(d["tenants"].([]any)) != 2 ||
		len(d["capabilities"].([]any)) != 0 || d["photoUrl"] != nil {
		t.Fatalf("me = %s", me.raw)
	}
	if cs := d["customerScopes"].([]any); len(cs) != 1 || cs[0].(map[string]any)["name"] != "CX Co." {
		t.Fatalf("customerScopes = %v", cs)
	}
	if pr := d["platformRoles"].([]any); len(pr) != 1 || pr[0] != "platform_admin" {
		t.Fatalf("platformRoles = %v", pr)
	}
	if r := h.call(h.public, http.MethodGet, "/v1/me", web.access, nil); r.status != http.StatusNotFound {
		t.Fatalf("/v1/me on the public listener: %d", r.status)
	}
	if r := h.call(h.public, http.MethodPost, "/v1/auth/refresh", "", map[string]any{"refreshToken": "x"}); r.status != http.StatusUnauthorized {
		t.Fatalf("/v1/auth on the public listener: %d", r.status)
	}

	p := h.call(h.internal, http.MethodPatch, "/v1/me", web.access, map[string]any{
		"displayName": "  สมหญิง  ", "lastLoginGeo": map[string]any{"lat": 13.7, "lng": 100.5, "source": "gps", "accuracyM": 12.5},
	})
	if p.status != http.StatusOK || p.data()["displayName"] != "สมหญิง" {
		t.Fatalf("patch: %d %s", p.status, p.raw)
	}
	expectError(t, h.call(h.internal, http.MethodPatch, "/v1/me", web.access, map[string]any{"photoKey": "users/x.jpg"}),
		http.StatusUnprocessableEntity, "invalid_argument")
	expectError(t, h.call(h.internal, http.MethodPatch, "/v1/me", web.access, map[string]any{"lastLoginGeo": map[string]any{"lat": 200, "lng": 1, "source": "gps"}}),
		http.StatusUnprocessableEntity, "invalid_argument")

	ts := h.get("/v1/me/tenants", web.access)
	if list, _ := ts.body["data"].([]any); ts.status != http.StatusOK || len(list) != 2 || list[0].(map[string]any)["status"] != "active" {
		t.Fatalf("tenants: %s", ts.raw)
	}
	ss := h.get("/v1/me/sessions", web.access)
	list, _ := ss.body["data"].([]any)
	if ss.status != http.StatusOK || len(list) != 2 {
		t.Fatalf("sessions: %s", ss.raw)
	}
	for _, it := range list {
		m := it.(map[string]any)
		if (m["id"] == web.sid) != (m["current"] == true) {
			t.Fatalf("current flag: %v", m)
		}
	}

	// SSE tickets are for driver-app sessions only.
	expectError(t, h.post("/v1/auth/sse-ticket", web.access, nil), http.StatusForbidden, auth.CodePermissionDenied)
	tk := h.post("/v1/auth/sse-ticket", mob.access, nil)
	if tk.status != http.StatusOK || tk.data()["expiresIn"] != float64(60) {
		t.Fatalf("sse-ticket: %d %s", tk.status, tk.raw)
	}
	uid, sid, ok, err := h.svc.ConsumeSSETicket(context.Background(), tk.str("ticket"))
	if err != nil || !ok || uid.String() != u || sid.String() != mob.sid {
		t.Fatalf("consume: %v %v %v %v", uid, sid, ok, err)
	}
	if _, _, ok, _ := h.svc.ConsumeSSETicket(context.Background(), tk.str("ticket")); ok {
		t.Fatal("an SSE ticket is single use")
	}

	// DELETE /v1/me/sessions/{sid}: own session only.
	if r := h.call(h.internal, http.MethodDelete, "/v1/me/sessions/"+mob.sid, web.access, nil); r.status != http.StatusNoContent {
		t.Fatalf("delete session: %d %s", r.status, r.raw)
	}
	expectError(t, h.get("/v1/me", mob.access), http.StatusUnauthorized, auth.CodeSessionRevoked)
	expectError(t, h.call(h.internal, http.MethodDelete, "/v1/me/sessions/"+mob.sid, web.access, nil), http.StatusNotFound, "not_found")

	// Logout works with an expired access token through the refresh token, and deletes the push token.
	mob2 := h.mustLogin("me@logitrack.test", "ios", "inst-me-2")
	h.exec(`INSERT INTO device_tokens (user_id, install_id, token, platform) VALUES ($1, 'inst-me-2', 'fcm-token-x', 'ios')`, u)
	h.clock.Advance(20 * time.Minute)
	if r := h.post("/v1/auth/logout", mob2.access, map[string]any{"refreshToken": mob2.refresh}); r.status != http.StatusNoContent {
		t.Fatalf("logout: %d %s", r.status, r.raw)
	}
	if n := scalar[int64](h, `SELECT count(*) FROM device_tokens WHERE user_id = $1`, u); n != 0 {
		t.Fatalf("device token not deleted")
	}
	expectError(t, h.refresh(mob2.refresh), http.StatusUnauthorized, auth.CodeSessionRevoked)
	expectError(t, h.post("/v1/auth/logout", "", nil), http.StatusUnauthorized, "unauthenticated")

	// logout-all ends every session and bumps the version.
	a1 := h.mustLogin("me@logitrack.test", "web", "")
	a2 := h.mustLogin("me@logitrack.test", "android", "inst-me-3")
	ver := scalar[int32](h, `SELECT auth_version FROM users WHERE id = $1`, u)
	if r := h.post("/v1/auth/logout-all", a2.access, nil); r.status != http.StatusNoContent {
		t.Fatalf("logout-all: %d %s", r.status, r.raw)
	}
	for _, s := range []session{a1, a2} {
		expectError(t, h.refresh(s.refresh), http.StatusUnauthorized, auth.CodeSessionRevoked)
	}
	if scalar[int32](h, `SELECT auth_version FROM users WHERE id = $1`, u) != ver+1 {
		t.Fatal("logout-all bumps auth_version")
	}
	if n := scalar[int64](h, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, u); n != 0 {
		t.Fatalf("%d sessions still live", n)
	}
	// Every key the service wrote is under its prefix and in the auth: or rl: namespace (R26).
	written := h.rdb.Keys(context.Background(), h.prefix+"*").Val()
	if len(written) == 0 {
		t.Fatal("no keys under the prefix")
	}
	for _, k := range written {
		rest := strings.TrimPrefix(k, h.prefix)
		if !strings.HasPrefix(rest, "auth:") && !strings.HasPrefix(rest, "rl:") {
			t.Fatalf("key outside the auth and rl namespaces: %s", k)
		}
	}
}
