//go:build integration

// POST /v1/auth/exchange (P7a, R42, Appendix C §C.4.6; spec mobile-auth-exchange AC1-AC6) against PostgreSQL 18,
// Redis 7 and the in-process Firebase stand-in (firebasetest).
package auth_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase/firebasetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

const exDriverUID = "fbExchangeDriverUid00000001"

// exchangeFixture is a carrier driver loaded from Firebase Auth (legacy_auth_uid set), as after cmd/etl.
type exchangeFixture struct {
	h    *harness
	b    *firebasetest.Backend
	user string
}

func newExchangeFixture(t *testing.T, mode auth.BridgeMode) exchangeFixture {
	t.Helper()
	h, b := bridgeHarness(t, mode)
	carrier := h.tenant("carrier", "Carrier")
	u := h.user("ex.driver@logitrack.test")
	h.member(u, carrier, "driver")
	h.driver(u, carrier, "0811111111")
	h.imported(u, exDriverUID)
	return exchangeFixture{h: h, b: b, user: u}
}

// token signs a Firebase ID token of uid whose sign-in happened at authTime with provider.
func (f exchangeFixture) token(uid string, authTime time.Time, provider string) string {
	c := f.b.IDTokenClaims(uid, authTime, f.h.clock.Now())
	c["firebase"] = map[string]any{"identities": map[string]any{}, "sign_in_provider": provider}
	return f.b.SignIDToken(c)
}

func (f exchangeFixture) exchange(body map[string]any) resp {
	f.h.t.Helper()
	return f.h.call(f.h.public, http.MethodPost, "/v1/auth/exchange", "", body)
}

func (f exchangeFixture) body(tok, install string) map[string]any {
	return map[string]any{"idToken": tok, "platform": "android", "installId": install, "appVersion": "4.0.0"}
}

func (f exchangeFixture) sessions() int {
	return scalar[int](f.h, `SELECT count(*) FROM sessions WHERE user_id = $1`, f.user)
}

// AC1: the route exists only while the bridge mode includes mobile; a closed route answers 404 whatever the body.
func TestExchangeGate(t *testing.T) {
	for _, mode := range auth.BridgeModes {
		t.Run(string(mode), func(t *testing.T) {
			f := newExchangeFixture(t, mode)
			good := f.body(f.token(exDriverUID, f.h.clock.Now().Add(-time.Hour), "password"), "inst-gate")
			bad := map[string]any{"platform": "web"}
			if !mode.Mobile() {
				expectError(t, f.exchange(good), http.StatusNotFound, httpx.CodeNotFound)
				expectError(t, f.exchange(bad), http.StatusNotFound, httpx.CodeNotFound)
				expectError(t, f.h.call(f.h.internal, http.MethodPost, "/v1/auth/exchange", "", good), http.StatusNotFound, httpx.CodeNotFound)
				return
			}
			if r := f.exchange(good); r.status != http.StatusOK {
				t.Fatalf("exchange: %d %s", r.status, r.raw)
			}
			expectError(t, f.exchange(bad), http.StatusUnprocessableEntity, "invalid_argument")
		})
	}
}

// AC2 (validation) + AC3: a password or Google Firebase session becomes a Go session of that install with the
// mapped amr and one user.logged_in; other providers are refused.
func TestExchangeSession(t *testing.T) {
	f := newExchangeFixture(t, auth.BridgeMobile)
	now := f.h.clock.Now()

	expectError(t, f.exchange(map[string]any{"idToken": "x", "platform": "web", "installId": "i"}), http.StatusUnprocessableEntity, "invalid_argument")
	expectError(t, f.exchange(map[string]any{"idToken": "x", "platform": "android"}), http.StatusUnprocessableEntity, "invalid_argument")

	r := f.exchange(f.body(f.token(exDriverUID, now.Add(-2*time.Hour), "password"), "inst-pwd"))
	if r.status != http.StatusOK || r.str("accessToken") == "" || r.str("refreshToken") == "" || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("exchange: %d %s", r.status, r.raw)
	}
	claims := payload(t, r.str("accessToken"))
	if !containsAMR(claims["amr"], "pwd") {
		t.Fatalf("amr = %v", claims["amr"])
	}
	if got := scalar[string](f.h, `SELECT amr || '|' || platform || '|' || install_id || '|' || coalesce(app_version, '')
		FROM sessions WHERE user_id = $1 AND install_id = 'inst-pwd'`, f.user); got != "pwd|android|inst-pwd|4.0.0" {
		t.Fatalf("session = %s", got)
	}
	if n := len(f.h.outbox("user.logged_in")); n != 1 {
		t.Fatalf("user.logged_in = %d", n)
	}
	// The Go session is a normal one (the mobile routes are mounted by internal/app, not this harness).
	if me := f.h.get("/v1/me", r.str("accessToken")); me.status != http.StatusOK {
		t.Fatalf("/v1/me: %d %s", me.status, me.raw)
	}

	if r := f.exchange(f.body(f.token(exDriverUID, now.Add(-time.Hour), "google.com"), "inst-google")); r.status != http.StatusOK {
		t.Fatalf("google exchange: %d %s", r.status, r.raw)
	}
	if got := scalar[string](f.h, `SELECT amr FROM sessions WHERE user_id = $1 AND install_id = 'inst-google'`, f.user); got != "google" {
		t.Fatalf("google session amr = %s", got)
	}

	before := f.sessions()
	for _, p := range []string{"phone", "custom", "anonymous"} {
		expectError(t, f.exchange(f.body(f.token(exDriverUID, now.Add(-time.Hour), p), "inst-"+p)), http.StatusUnauthorized, auth.CodeInvalidToken)
	}
	if f.sessions() != before {
		t.Fatal("a refused provider opened a session")
	}
}

// containsAMR accepts an amr claim written as a string or a list.
func containsAMR(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return x == want
	case []any:
		for _, e := range x {
			if e == want {
				return true
			}
		}
	}
	return false
}

// AC4, AC5: the refusals of C.6.2 reach the exchange unchanged; must_change_password gets a ticket and no session.
func TestExchangeRefusals(t *testing.T) {
	f := newExchangeFixture(t, auth.BridgeMobile)
	now := f.h.clock.Now()
	fresh := f.token(exDriverUID, now.Add(-time.Minute), "password")

	expectError(t, f.exchange(f.body(f.token("fbNobody", now.Add(-time.Minute), "password"), "i1")), http.StatusForbidden, auth.CodeNoAccount)
	expectError(t, f.exchange(f.body("not-a-jwt", "i1")), http.StatusUnauthorized, auth.CodeInvalidToken)

	f.h.exec(`UPDATE users SET status = 'disabled', disabled_at = now() WHERE id = $1`, f.user)
	expectError(t, f.exchange(f.body(fresh, "i1")), http.StatusForbidden, auth.CodeAccountDisabled)
	f.h.exec(`UPDATE users SET status = 'active', disabled_at = NULL WHERE id = $1`, f.user)

	f.h.exec(`UPDATE users SET password_changed_at = $2 WHERE id = $1`, f.user, now.Add(-30*time.Second))
	expectError(t, f.exchange(f.body(f.token(exDriverUID, now.Add(-time.Hour), "password"), "i1")), http.StatusUnauthorized, auth.CodeInvalidToken)
	f.h.exec(`UPDATE users SET password_changed_at = NULL WHERE id = $1`, f.user)

	f.h.exec(`UPDATE users SET must_change_password = true WHERE id = $1`, f.user)
	r := f.exchange(f.body(fresh, "i1"))
	expectError(t, r, http.StatusForbidden, "password_change_required")
	if tk, _ := r.details()["passwordChangeTicket"].(string); tk == "" {
		t.Fatalf("no ticket: %s", r.raw)
	}
	if n := f.sessions(); n != 0 {
		t.Fatalf("sessions = %d after refusals", n)
	}
}

// AC6 (R9): the session transaction re-checks the uid and the password change under the row lock, so a change made
// after the token was verified is not missed.
func TestExchangeRace(t *testing.T) {
	f := newExchangeFixture(t, auth.BridgeMobile)
	now := f.h.clock.Now()
	tok := f.token(exDriverUID, now.Add(-time.Minute), "password")

	restore := auth.SetExchangeBeforeSession(func() {
		f.h.exec(`UPDATE users SET legacy_auth_uid = NULL WHERE id = $1`, f.user)
	})
	expectError(t, f.exchange(f.body(tok, "race-1")), http.StatusForbidden, auth.CodeNoAccount)
	restore()
	f.h.exec(`UPDATE users SET legacy_auth_uid = $2 WHERE id = $1`, f.user, exDriverUID)

	restore = auth.SetExchangeBeforeSession(func() {
		f.h.exec(`UPDATE users SET password_changed_at = $2 WHERE id = $1`, f.user, now)
	})
	expectError(t, f.exchange(f.body(tok, "race-2")), http.StatusUnauthorized, auth.CodeInvalidToken)
	restore()

	if n := f.sessions(); n != 0 {
		t.Fatalf("sessions = %d", n)
	}
}

// AC2: rl:exchange_ip 30/min per client IP; over budget is 429 with Retry-After.
func TestExchangeRateLimit(t *testing.T) {
	f := newExchangeFixture(t, auth.BridgeMobile)
	for i := range 30 {
		if r := f.exchange(f.body("not-a-jwt", "rl")); r.status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d %s", i+1, r.status, r.raw)
		}
	}
	r := f.exchange(f.body("not-a-jwt", "rl"))
	expectError(t, r, http.StatusTooManyRequests, "resource_exhausted")
	if r.header.Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
	if strings.Contains(string(r.raw), "not-a-jwt") {
		t.Fatal("the token is echoed")
	}
}
