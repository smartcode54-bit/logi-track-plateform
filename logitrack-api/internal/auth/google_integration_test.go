//go:build integration

package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/google/googletest"
)

// Placeholders in the Google client-id shape: the web GIS client and the OAuth client the installed APKs
// pass as serverClientId, both in GOOGLE_OIDC_ALLOWED_CLIENT_IDS (Appendix C §C.4.10).
const (
	webClient = "100000000001-webtest.apps.googleusercontent.com"
	apkClient = "100000000002-apktest.apps.googleusercontent.com"
)

// googleHarness is a harness whose Google verifier talks to an in-process provider: the ID tokens are
// signed with a local RSA key and the JWKS never leaves the test process.
func googleHarness(t *testing.T, opts ...option) (*harness, *googletest.Provider) {
	t.Helper()
	p := googletest.New(t)
	h := newHarnessWith(t, func(now func() time.Time) auth.GoogleVerifier {
		return p.Verifier(now, webClient, apkClient)
	}, opts...)
	return h, p
}

func (h *harness) googleToken(p *googletest.Provider, client, sub, email string, edit func(map[string]any)) string {
	h.t.Helper()
	c := googletest.Claims(client, sub, email, h.clock.Now())
	if edit != nil {
		edit(c)
	}
	return p.Sign(c)
}

func (h *harness) signInGoogle(base string, body map[string]any) resp {
	h.t.Helper()
	return h.call(base, http.MethodPost, "/v1/auth/google", "", body)
}

func (h *harness) nonce() string {
	h.t.Helper()
	r := h.get("/v1/auth/google/nonce", "")
	if r.status != http.StatusOK {
		h.t.Fatalf("nonce: %d %s", r.status, r.raw)
	}
	return r.str("nonce")
}

func (h *harness) linkGoogle(user, sub string) {
	h.exec(`INSERT INTO auth_identities (user_id, provider, provider_subject) VALUES ($1, 'google', $2)`, user, sub)
}

func withNonce(n string) func(map[string]any) { return func(m map[string]any) { m["nonce"] = n } }

// The driver app signs in on the public listener with the id_token of google_sign_in (aud = the APK's
// serverClientId) and no nonce; the account is found by the Google sub. The session is a normal one with
// amr "google": refresh keeps it, last_login_* and the identity's last_used_at are written.
func TestGoogleSignInBySubjectOnMobile(t *testing.T) {
	h, p := googleHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("driver.g@logitrack.test")
	h.member(u, own, "driver")
	drv := h.driver(u, own, "0811110000")
	h.linkGoogle(u, "g-sub-driver")

	tok := h.googleToken(p, apkClient, "g-sub-driver", "driver.g@logitrack.test", func(m map[string]any) {
		m["azp"] = "100000000009-android.apps.googleusercontent.com" // the Android client; aud is the server client
	})
	r := h.signInGoogle(h.public, map[string]any{"idToken": tok, "platform": "android", "installId": "inst-g1", "appVersion": "4.0.0"})
	if r.status != http.StatusOK {
		t.Fatalf("google sign-in: %d %s", r.status, r.raw)
	}
	c := payload(t, r.str("accessToken"))
	if c["sub"] != u || c["amr"] != "google" || c["drv"] != drv || c["tid"] != own || c["rol"] != "driver" {
		t.Fatalf("claims = %v", c)
	}
	if r.data()["defaultTenantId"] != own || len(r.str("refreshToken")) != 43 {
		t.Fatalf("body = %s", r.raw)
	}
	if amr, inst := scalar[string](h, `SELECT amr FROM sessions WHERE id = $1`, c["sid"]),
		scalar[string](h, `SELECT install_id FROM sessions WHERE id = $1`, c["sid"]); amr != "google" || inst != "inst-g1" {
		t.Fatalf("session amr=%s install=%s", amr, inst)
	}
	if scalar[bool](h, `SELECT last_login_at IS NULL FROM users WHERE id = $1`, u) ||
		scalar[bool](h, `SELECT last_used_at IS NULL FROM auth_identities WHERE user_id = $1`, u) {
		t.Fatal("last_login_at or auth_identities.last_used_at not written")
	}
	ev := h.outbox("user.logged_in")
	if len(ev) != 1 || ev[0]["amr"] != "google" || ev[0]["platform"] != "android" {
		t.Fatalf("user.logged_in = %v", ev)
	}
	if n := scalar[int64](h, `SELECT count(*) FROM security_events WHERE event_type = 'google_identity_linked'`); n != 0 {
		t.Fatalf("an existing link wrote %d google_identity_linked rows", n)
	}
	s := h.mustRefresh(session{access: r.str("accessToken"), refresh: r.str("refreshToken"), sid: c["sid"].(string)})
	if a := payload(t, s.access)["amr"]; a != "google" {
		t.Fatalf("refreshed amr = %v", a)
	}
	if me := h.get("/v1/me", s.access); me.status != http.StatusOK {
		t.Fatalf("/v1/me with a Google session: %d %s", me.status, me.raw)
	}
}

// The web flow (through the BFF, internal listener) needs a nonce from GET /v1/auth/google/nonce that
// the GIS token carries; it is single use and bound to the token, so a nonce-bound token signs in once,
// on whatever platform it is presented.
func TestGoogleWebNonce(t *testing.T) {
	h, p := googleHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("manager.g@logitrack.test")
	h.member(u, own, "manager")
	h.linkGoogle(u, "g-sub-web")
	tok := func(edit func(map[string]any)) string {
		return h.googleToken(p, webClient, "g-sub-web", "manager.g@logitrack.test", edit)
	}

	nr := h.get("/v1/auth/google/nonce", "")
	n := nr.str("nonce")
	if nr.status != http.StatusOK || len(n) != 43 || nr.data()["expiresIn"] != float64(600) ||
		nr.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("nonce: %d %v %s", nr.status, nr.header, nr.raw)
	}
	ttl, err := h.rdb.TTL(context.Background(), h.prefix+"auth:google:nonce:"+n).Result()
	if err != nil || ttl <= 9*time.Minute || ttl > 10*time.Minute {
		t.Fatalf("auth:google:nonce TTL = %v (%v)", ttl, err)
	}

	r := h.signInGoogle(h.internal, map[string]any{"idToken": tok(withNonce(n)), "platform": "web"})
	expectError(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.details()["fields"].([]any)[0].(map[string]any); f["field"] != "nonce" || f["reason"] != "required" {
		t.Fatalf("missing nonce on the web: %v", f)
	}
	r = h.signInGoogle(h.internal, map[string]any{"idToken": tok(nil), "nonce": "not a nonce", "platform": "web"})
	expectError(t, r, http.StatusUnprocessableEntity, "invalid_argument")

	// A nonce the token does not carry, or another issued nonce, is rejected without consuming n.
	expectError(t, h.signInGoogle(h.internal, map[string]any{"idToken": tok(nil), "nonce": n, "platform": "web"}),
		http.StatusUnauthorized, auth.CodeInvalidToken)
	other := h.nonce()
	expectError(t, h.signInGoogle(h.internal, map[string]any{"idToken": tok(withNonce(n)), "nonce": other, "platform": "web"}),
		http.StatusUnauthorized, auth.CodeInvalidToken)

	if r := h.signInGoogle(h.internal, map[string]any{"idToken": tok(withNonce(n)), "nonce": n, "platform": "web"}); r.status != http.StatusOK {
		t.Fatalf("web sign-in: %d %s", r.status, r.raw)
	} else if c := payload(t, r.str("accessToken")); c["amr"] != "google" || c["rol"] != "manager" {
		t.Fatalf("claims = %v", c)
	}
	if web := scalar[int64](h, `SELECT count(*) FROM sessions WHERE user_id = $1 AND platform = 'web' AND install_id IS NULL`, u); web != 1 {
		t.Fatalf("web sessions = %d", web)
	}

	// Used: a second token for the same nonce fails, also when replayed as a driver-app sign-in.
	expectError(t, h.signInGoogle(h.internal, map[string]any{"idToken": tok(withNonce(n)), "nonce": n, "platform": "web"}),
		http.StatusUnauthorized, auth.CodeInvalidToken)
	expectError(t, h.signInGoogle(h.public, map[string]any{"idToken": tok(withNonce(other)), "platform": "android", "installId": "i"}),
		http.StatusUnauthorized, auth.CodeInvalidToken)
	// A well-formed nonce that was never issued.
	never := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	expectError(t, h.signInGoogle(h.internal, map[string]any{"idToken": tok(withNonce(never)), "nonce": never, "platform": "web"}),
		http.StatusUnauthorized, auth.CodeInvalidToken)
	// The other nonce was never consumed by the failures above.
	if r := h.signInGoogle(h.internal, map[string]any{"idToken": tok(withNonce(other)), "nonce": other, "platform": "web"}); r.status != http.StatusOK {
		t.Fatalf("sign-in with the unconsumed nonce: %d %s", r.status, r.raw)
	}
	if n := scalar[int64](h, `SELECT count(*) FROM sessions WHERE user_id = $1`, u); n != 2 {
		t.Fatalf("sessions = %d, want 2", n)
	}
}

// A user without a Google identity is linked by the token's verified email (case-insensitive):
// auth_identities is inserted and google_identity_linked appended in the same transaction (C.4.13). The
// link is used afterwards; another Google account with the same email is no_account.
func TestGoogleLinksVerifiedEmail(t *testing.T) {
	h, p := googleHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("Link.Me@logitrack.test")
	h.member(u, own, "operation_staff")

	n := h.nonce()
	r := h.signInGoogle(h.internal, map[string]any{
		"idToken": h.googleToken(p, webClient, "g-sub-new", "link.me@logitrack.test", withNonce(n)), "nonce": n, "platform": "web",
	})
	if r.status != http.StatusOK {
		t.Fatalf("link sign-in: %d %s", r.status, r.raw)
	}
	if c := payload(t, r.str("accessToken")); c["sub"] != u || c["amr"] != "google" {
		t.Fatalf("claims = %v", c)
	}
	if got := scalar[string](h, `SELECT user_id::text || ' ' || provider || ' ' || provider_subject || ' ' || email_at_link
		FROM auth_identities WHERE user_id = $1`, u); got != u+" google g-sub-new link.me@logitrack.test" {
		t.Fatalf("auth_identities = %s", got)
	}
	ev := scalar[string](h, `SELECT severity || ' ' || target_user_id::text || ' ' || actor_user_id::text || ' ' ||
		coalesce(tenant_id::text, '-') || ' ' || (details->>'linkedBy') || ' ' || (details->>'platform') || ' ' || (request_id IS NOT NULL)::text
		FROM security_events WHERE event_type = 'google_identity_linked'`)
	if ev != "info "+u+" "+u+" - verified_email web true" {
		t.Fatalf("google_identity_linked = %s", ev)
	}

	// The next sign-in uses the link (sub), on the driver app as well; no second event.
	r = h.signInGoogle(h.public, map[string]any{
		"idToken": h.googleToken(p, apkClient, "g-sub-new", "link.me@logitrack.test", nil), "platform": "ios", "installId": "inst-ios",
	})
	if r.status != http.StatusOK {
		t.Fatalf("second sign-in: %d %s", r.status, r.raw)
	}
	if n := scalar[int64](h, `SELECT count(*) FROM security_events WHERE event_type = 'google_identity_linked'`); n != 1 {
		t.Fatalf("google_identity_linked rows = %d", n)
	}
	// Another Google account with the same verified email is not linked over the first one.
	r = h.signInGoogle(h.public, map[string]any{
		"idToken": h.googleToken(p, apkClient, "g-sub-other", "link.me@logitrack.test", nil), "platform": "android",
	})
	expectError(t, r, http.StatusForbidden, auth.CodeNoAccount)
	if n := scalar[int64](h, `SELECT count(*) FROM auth_identities`); n != 1 {
		t.Fatalf("auth_identities rows = %d", n)
	}
	// The retry of a concurrent link keys on these constraint names (GoogleSignIn).
	var names []string
	rows, err := h.etl.Query(context.Background(),
		`SELECT conname FROM pg_constraint WHERE conrelid = 'auth_identities'::regclass AND contype = 'u' ORDER BY conname`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		names = append(names, s)
	}
	if !slices.Equal(names, []string{"auth_identities_provider_provider_subject_key", "auth_identities_user_id_provider_key"}) {
		t.Fatalf("auth_identities unique constraints = %v", names)
	}
}

// Rejections (Appendix C §C.9.3): a bad token is 401 invalid_token; an unknown account is 403 no_account
// and creates nothing (no self-signup); a disabled user is 403 account_disabled before any link, ticket or
// token; a must_change_password user gets the R79 ticket and no session.
func TestGoogleRejections(t *testing.T) {
	h, p := googleHarness(t)
	own := h.tenant("own_fleet", "Own")
	mobile := func(tok string) map[string]any {
		return map[string]any{"idToken": tok, "platform": "android", "installId": "inst-x"}
	}

	active := h.user("active.g@logitrack.test")
	h.member(active, own, "manager")
	h.linkGoogle(active, "g-active")
	for name, edit := range map[string]func(map[string]any){
		"aud outside the list": func(m map[string]any) { m["aud"] = "100000000003-other.apps.googleusercontent.com" },
		"email not verified":   func(m map[string]any) { m["email_verified"] = false },
		"expired":              func(m map[string]any) { m["exp"] = h.clock.Now().Add(-time.Second).Unix() },
		"other issuer":         func(m map[string]any) { m["iss"] = "https://securetoken.google.com/some-project" },
	} {
		r := h.signInGoogle(h.public, mobile(h.googleToken(p, apkClient, "g-active", "active.g@logitrack.test", edit)))
		if r.status != http.StatusUnauthorized || r.code() != auth.CodeInvalidToken {
			t.Fatalf("%s: want 401 invalid_token, got %d %s", name, r.status, r.raw)
		}
	}
	foreign := googletest.SignWith(t, googletest.NewKey(t), googletest.KeyID, "RS256",
		googletest.Claims(apkClient, "g-active", "active.g@logitrack.test", h.clock.Now()))
	expectError(t, h.signInGoogle(h.public, mobile(foreign)), http.StatusUnauthorized, auth.CodeInvalidToken)
	if n := scalar[int64](h, `SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("%d sessions after rejected tokens", n)
	}

	// Validation.
	for _, body := range []map[string]any{
		{"platform": "android"},
		{"idToken": "x", "platform": "script"},
		{"idToken": "x"},
	} {
		expectError(t, h.signInGoogle(h.public, body), http.StatusUnprocessableEntity, "invalid_argument")
	}

	users := scalar[int64](h, `SELECT count(*) FROM users`)
	expectError(t, h.signInGoogle(h.public, mobile(h.googleToken(p, apkClient, "g-stranger", "stranger@gmail.test", nil))),
		http.StatusForbidden, auth.CodeNoAccount)
	if scalar[int64](h, `SELECT count(*) FROM users`) != users || scalar[int64](h, `SELECT count(*) FROM auth_identities`) != 1 {
		t.Fatal("an unknown Google account created a user or an identity")
	}

	disabledLinked := h.user("disabled.linked@logitrack.test")
	h.member(disabledLinked, own, "manager")
	h.linkGoogle(disabledLinked, "g-disabled")
	disabledEmail := h.user("disabled.email@logitrack.test")
	h.member(disabledEmail, own, "manager")
	h.exec(`UPDATE users SET status = 'disabled', disabled_at = now() WHERE id = ANY($1::uuid[])`, []string{disabledLinked, disabledEmail})
	expectError(t, h.signInGoogle(h.public, mobile(h.googleToken(p, apkClient, "g-disabled", "disabled.linked@logitrack.test", nil))),
		http.StatusForbidden, auth.CodeAccountDisabled)
	expectError(t, h.signInGoogle(h.public, mobile(h.googleToken(p, apkClient, "g-disabled-2", "disabled.email@logitrack.test", nil))),
		http.StatusForbidden, auth.CodeAccountDisabled)
	if n := scalar[int64](h, `SELECT count(*) FROM auth_identities WHERE user_id = $1`, disabledEmail); n != 0 {
		t.Fatal("a disabled user was linked")
	}

	deleted := h.user("deleted.g@logitrack.test")
	h.exec(`UPDATE users SET status = 'deleted', deleted_at = now() WHERE id = $1`, deleted)
	tail := h.user("tail.g@logitrack.test")
	h.exec(`UPDATE users SET status = 'reset_required', password_hash = NULL WHERE id = $1`, tail)
	for _, email := range []string{"deleted.g@logitrack.test", "tail.g@logitrack.test"} {
		expectError(t, h.signInGoogle(h.public, mobile(h.googleToken(p, apkClient, "g-"+email, email, nil))),
			http.StatusForbidden, auth.CodeNoAccount)
	}

	temp := h.user("temp.g@logitrack.test")
	h.member(temp, own, "manager")
	h.linkGoogle(temp, "g-temp")
	h.exec(`UPDATE users SET must_change_password = true WHERE id = $1`, temp)
	r := h.signInGoogle(h.public, mobile(h.googleToken(p, apkClient, "g-temp", "temp.g@logitrack.test", nil)))
	expectError(t, r, http.StatusForbidden, auth.CodePasswordChangeRequired)
	if ticket, _ := r.details()["passwordChangeTicket"].(string); len(ticket) != 43 {
		t.Fatalf("details = %v", r.details())
	}

	if n := scalar[int64](h, `SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("%d sessions created by rejected sign-ins", n)
	}
	if n := scalar[int64](h, `SELECT count(*) FROM security_events WHERE event_type = 'google_identity_linked'`); n != 0 {
		t.Fatalf("%d google_identity_linked rows from rejected sign-ins", n)
	}
}

// Without GOOGLE_OIDC_ALLOWED_CLIENT_IDS both routes answer 404 on both listeners; with Google
// unreachable a sign-in is 503 (the token was not judged), while the nonce route still works.
func TestGoogleOffAndUnavailable(t *testing.T) {
	off := newHarness(t)
	for _, base := range []string{off.internal, off.public} {
		expectError(t, off.call(base, http.MethodGet, "/v1/auth/google/nonce", "", nil), http.StatusNotFound, "not_found")
		expectError(t, off.call(base, http.MethodPost, "/v1/auth/google", "", map[string]any{"idToken": "x", "platform": "android"}),
			http.StatusNotFound, "not_found")
	}

	h, p := googleHarness(t)
	p.SetDown(true)
	r := h.signInGoogle(h.public, map[string]any{"idToken": h.googleToken(p, apkClient, "s", "a@logitrack.test", nil), "platform": "android"})
	expectError(t, r, http.StatusServiceUnavailable, "unavailable")
	if n := h.nonce(); len(n) != 43 {
		t.Fatalf("nonce while Google is down: %q", n)
	}
}

// rl:google_ip 30/min and rl:google_nonce_ip 60/min per client IP (Appendix B §B.6.3); over budget is
// 429 resource_exhausted with Retry-After.
func TestGoogleRateLimits(t *testing.T) {
	h, _ := googleHarness(t)
	for i := range 30 {
		r := h.signInGoogle(h.public, map[string]any{"idToken": "not-a-jwt", "platform": "android"})
		if r.status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d %s", i+1, r.status, r.raw)
		}
	}
	r := h.signInGoogle(h.public, map[string]any{"idToken": "not-a-jwt", "platform": "android"})
	expectError(t, r, http.StatusTooManyRequests, "resource_exhausted")
	if r.header.Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
	for i := range 60 {
		if r := h.get("/v1/auth/google/nonce", ""); r.status != http.StatusOK {
			t.Fatalf("nonce %d: %d %s", i+1, r.status, r.raw)
		}
	}
	expectError(t, h.get("/v1/auth/google/nonce", ""), http.StatusTooManyRequests, "resource_exhausted")
}

// Concurrent first sign-ins of one Google account (a double tap, two devices) serialise on the user row:
// every one succeeds, and exactly one link and one google_identity_linked row exist.
func TestGoogleConcurrentFirstSignIns(t *testing.T) {
	h, p := googleHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("race.g@logitrack.test")
	h.member(u, own, "manager")
	tok := h.googleToken(p, apkClient, "g-race", "race.g@logitrack.test", nil)

	const n = 8
	statuses := make(chan string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			r, err := h.do(h.public, http.MethodPost, "/v1/auth/google", "",
				map[string]any{"idToken": tok, "platform": "android", "installId": fmt.Sprintf("inst-race-%d", i)})
			if err != nil {
				statuses <- err.Error()
				return
			}
			statuses <- fmt.Sprintf("%d %s", r.status, r.code())
		})
	}
	wg.Wait()
	close(statuses)
	for s := range statuses {
		if s != "200 " {
			t.Fatalf("concurrent sign-in: %s", s)
		}
	}
	if links := scalar[int64](h, `SELECT count(*) FROM auth_identities WHERE user_id = $1`, u); links != 1 {
		t.Fatalf("auth_identities rows = %d", links)
	}
	if ev := scalar[int64](h, `SELECT count(*) FROM security_events WHERE event_type = 'google_identity_linked'`); ev != 1 {
		t.Fatalf("google_identity_linked rows = %d", ev)
	}
	if s := scalar[int64](h, `SELECT count(*) FROM sessions WHERE user_id = $1 AND amr = 'google'`, u); s != n {
		t.Fatalf("sessions = %d, want %d", s, n)
	}
}
