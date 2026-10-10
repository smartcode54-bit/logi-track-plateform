//go:build integration

// Regression tests of the review panel of PR #99 (T05): races between requests of one user, lost
// post-commit Redis writes, the password-change form precedence and the lockout under concurrency.
package auth_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
)

// flaky is a go-redis hook that fails every command while down is set: a Redis that cannot take the
// post-commit writes of one api replica (timeout, failover, partition) while the others still reach it.
type flaky struct{ down atomic.Bool }

var errFlaky = errors.New("redis unreachable (test)")

func (f *flaky) DialHook(next redis.DialHook) redis.DialHook { return next }

func (f *flaky) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if f.down.Load() {
			cmd.SetErr(errFlaky)
			return errFlaky
		}
		return next(ctx, cmd)
	}
}

func (f *flaky) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if f.down.Load() {
			for _, c := range cmds {
				c.SetErr(errFlaky)
			}
			return errFlaky
		}
		return next(ctx, cmds)
	}
}

// replica is a second auth.Service on the harness database and Redis whose Redis connection fails
// while f.down is set. Its metrics go to reg.
func (h *harness) replica(cfg auth.Config) (*auth.Service, *flaky, *prometheus.Registry) {
	h.t.Helper()
	f := &flaky{}
	o := h.redisOptions()
	o.MaxRetries = -1
	rdb := redis.NewClient(o)
	rdb.AddHook(f)
	h.t.Cleanup(func() { _ = rdb.Close() }) // registered before the service, so it runs after svc.Close
	svc, _ := h.service(cfg, rdb)
	reg := prometheus.NewRegistry()
	if err := svc.Register(reg); err != nil {
		h.t.Fatal(err)
	}
	return svc, f, reg
}

func (h *harness) cfg() auth.Config {
	return auth.Config{
		RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour, PasswordResetTTL: 30 * time.Minute,
		Scrypt: &h.scrypt, RateLimitEnabled: true, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute},
	}
}

func httpCode(err error) string {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Code
	}
	return fmt.Sprintf("not an httpx error: %v", err)
}

// A version bump whose post-commit raise never reached Redis leaves auth:user:ver below
// users.auth_version. Tokens issued afterwards carry the new version and must still work: a token
// newer than the cache re-reads PostgreSQL and raises the cache, and every issue raises it too. The
// old token then answers claims_changed.
func TestStaleVersionCacheHeals(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("stale@logitrack.test")
	h.member(u, own, "manager")
	ctx := context.Background()
	verKey := h.prefix + "auth:user:ver:" + u

	lost, f, _ := h.replica(h.cfg())
	f.down.Store(true)
	roleChangeOnLostReplica := func(role string) {
		t.Helper()
		var pc *auth.PostCommit
		if err := db.WithSystem(ctx, h.pool, nil, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE memberships SET role = $2 WHERE user_id = $1`, u, role); err != nil {
				return err
			}
			var err error
			pc, _, err = lost.RevokeInTx(ctx, tx, auth.Revocation{UserID: uuid.MustParse(u), Reason: auth.RevokeClaimsChanged, BumpVersion: true})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		lost.Apply(ctx, pc) // fails: the raise never lands
	}

	// 1. A token issued while the cache is stale (a login on the replica that cannot reach Redis).
	s := h.mustLogin("stale@logitrack.test", "web", "")
	if r := h.get("/v1/me", s.access); r.status != http.StatusOK {
		t.Fatalf("me: %d %s", r.status, r.raw)
	}
	roleChangeOnLostReplica("operator")
	if v := h.rdb.Get(ctx, verKey).Val(); v != "1" {
		t.Fatalf("cached version = %q, want the stale 1", v)
	}
	res, err := lost.Login(ctx, auth.LoginInput{Email: "stale@logitrack.test", Password: pw, Platform: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if c := payload(t, res.AccessToken); c["ver"] != float64(2) || c["rol"] != "operator" {
		t.Fatalf("fresh login claims = %v", c)
	}
	if r := h.get("/v1/me", res.AccessToken); r.status != http.StatusOK {
		t.Fatalf("a token of the current version is rejected: %d %s", r.status, r.raw)
	}
	if v := h.rdb.Get(ctx, verKey).Val(); v != "2" {
		t.Fatalf("cached version after the newer token = %q, want 2", v)
	}
	r := h.get("/v1/me", s.access)
	expectError(t, r, http.StatusUnauthorized, auth.CodeTokenExpired)
	if r.details()["reason"] != auth.ReasonClaimsChanged {
		t.Fatalf("details = %v", r.details())
	}

	// 2. The refresh itself raises the cache after COMMIT, before any request presents the new token.
	roleChangeOnLostReplica("manager")
	if v := h.rdb.Get(ctx, verKey).Val(); v != "2" {
		t.Fatalf("cached version = %q, want the stale 2", v)
	}
	next := h.mustRefresh(s)
	if v := h.rdb.Get(ctx, verKey).Val(); v != "3" {
		t.Fatalf("cached version after the refresh = %q, want 3", v)
	}
	if ttl := h.rdb.PTTL(ctx, verKey).Val(); ttl <= 0 {
		t.Fatalf("cached version TTL = %v", ttl)
	}
	if r := h.get("/v1/me", next.access); r.status != http.StatusOK {
		t.Fatalf("refreshed token: %d %s", r.status, r.raw)
	}
	expectError(t, h.get("/v1/me", res.AccessToken), http.StatusUnauthorized, auth.CodeTokenExpired)

	// A version newer than users.auth_version itself was never issued.
	forged := h.mustLogin("stale@logitrack.test", "web", "")
	h.exec(`UPDATE users SET auth_version = auth_version - 1 WHERE id = $1`, u)
	h.rdb.Del(ctx, verKey)
	expectError(t, h.get("/v1/me", forged.access), http.StatusUnauthorized, auth.CodeInvalidToken)
}

// The security-relevant post-commit writes are retried until they land, and a version miss rebuilds a
// revoked marker from sessions.revoked_at: a revocation that commits while Redis cannot take the
// marker is enforced without waiting for the access token to expire.
func TestLostPostCommitWritesAreRetried(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("lost@logitrack.test")
	h.member(u, own, "operator")
	ctx := context.Background()
	writer, f, reg := h.replica(h.cfg())

	// Warm version cache: only the marker can tell this request that the session ended.
	s := h.mustLogin("lost@logitrack.test", "web", "")
	if r := h.get("/v1/me", s.access); r.status != http.StatusOK {
		t.Fatalf("me: %d %s", r.status, r.raw)
	}
	f.down.Store(true)
	if _, err := writer.Revoke(ctx, auth.Revocation{UserID: uuid.MustParse(u), Reason: auth.RevokeAdmin}); err != nil {
		t.Fatal(err)
	}
	if v := counterValue(t, reg, "auth_postcommit_failures_total"); v < 1 {
		t.Fatalf("auth_postcommit_failures_total = %v", v)
	}
	f.down.Store(false)
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := h.get("/v1/me", s.access)
		if r.status == http.StatusUnauthorized && r.code() == auth.CodeSessionRevoked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the revoked marker never landed: %d %s", r.status, r.raw)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Cold version cache: the miss reads sessions.revoked_at and rebuilds the marker at once.
	s2 := h.mustLogin("lost@logitrack.test", "android", "inst-lost")
	f.down.Store(true)
	if _, err := writer.Revoke(ctx, auth.Revocation{UserID: uuid.MustParse(u), Reason: auth.RevokeLogout, SessionIDs: []uuid.UUID{uuid.MustParse(s2.sid)}}); err != nil {
		t.Fatal(err)
	}
	h.rdb.Del(ctx, h.prefix+"auth:user:ver:"+u)
	expectError(t, h.get("/v1/me", s2.access), http.StatusUnauthorized, auth.CodeSessionRevoked)
	if n := h.rdb.Exists(ctx, h.prefix+"auth:sess:revoked:"+s2.sid).Val(); n != 1 {
		t.Fatal("the miss did not rebuild the revoked marker")
	}
	f.down.Store(false)
}

// With a passwordChangeTicket in the body the ticket form is used and any Authorization header is
// ignored: the BFF proxy forwards lt_at whenever the cookie exists (Appendix C §C.4.5).
func TestPasswordChangeTicketWinsOverBearer(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("temp2@logitrack.test")
	h.member(u, own, "operator")
	other := h.user("neighbour@logitrack.test")
	h.member(other, own, "operator")
	ctx := context.Background()

	// An admin issues a temporary password: must_change_password and every session ends, but the
	// browser still holds the revoked lt_at.
	stale := h.mustLogin("temp2@logitrack.test", "web", "")
	var pc *auth.PostCommit
	if err := db.WithSystem(ctx, h.pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, u); err != nil {
			return err
		}
		var err error
		pc, _, err = h.svc.RevokeInTx(ctx, tx, auth.Revocation{UserID: uuid.MustParse(u), Reason: auth.RevokePasswordReset, BumpVersion: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.svc.Apply(ctx, pc)
	ticket := func() string {
		t.Helper()
		r := h.login("temp2@logitrack.test", pw, "web", "")
		expectError(t, r, http.StatusForbidden, auth.CodePasswordChangeRequired)
		tk, _ := r.details()["passwordChangeTicket"].(string)
		return tk
	}
	tk := ticket()
	if r := h.post("/v1/auth/password/change", stale.access, map[string]any{"passwordChangeTicket": tk, "newPassword": "first own password"}); r.status != http.StatusNoContent {
		t.Fatalf("ticket with a revoked bearer: %d %s", r.status, r.raw)
	}
	r := h.post("/v1/auth/password/change", stale.access, map[string]any{"passwordChangeTicket": tk, "newPassword": "first own password"})
	expectError(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f, _ := r.details()["fields"].([]any); len(f) != 1 || f[0].(map[string]any)["reason"] != "invalid_or_expired" {
		t.Fatalf("second use: %s", r.raw)
	}

	// A shared depot PC: another user is still signed in and the proxy sends that user's bearer.
	neighbour := h.mustLogin("neighbour@logitrack.test", "web", "")
	h.exec(`UPDATE users SET must_change_password = true, password_hash = $2 WHERE id = $1`, u, h.hash(pw))
	tk = ticket()
	if r := h.post("/v1/auth/password/change", neighbour.access, map[string]any{"passwordChangeTicket": tk, "newPassword": "second own password"}); r.status != http.StatusNoContent {
		t.Fatalf("ticket with another user's bearer: %d %s", r.status, r.raw)
	}
	if r := h.get("/v1/me", neighbour.access); r.status != http.StatusOK {
		t.Fatalf("the bearer's user was touched: %d %s", r.status, r.raw)
	}
	h.mustLogin("neighbour@logitrack.test", "web", "")
	if r := h.login("temp2@logitrack.test", "second own password", "web", ""); r.status != http.StatusOK {
		t.Fatalf("login with the new password: %d %s", r.status, r.raw)
	}
	expectError(t, h.post("/v1/auth/password/change", "", map[string]any{"newPassword": "third own password"}),
		http.StatusUnprocessableEntity, "invalid_argument")
}

// A must-change ticket is bound to the auth_version it was issued at: a reset in between voids it.
func TestPasswordChangeTicketVoidAfterReset(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("bound@logitrack.test")
	h.member(u, own, "operator")
	h.exec(`UPDATE users SET must_change_password = true WHERE id = $1`, u)
	r := h.login("bound@logitrack.test", pw, "web", "")
	expectError(t, r, http.StatusForbidden, auth.CodePasswordChangeRequired)
	tk, _ := r.details()["passwordChangeTicket"].(string)

	var tok string
	if err := db.WithSystem(context.Background(), h.pool, nil, func(tx pgx.Tx) error {
		var err error
		tok, err = h.svc.IssuePasswordResetToken(context.Background(), tx, uuid.MustParse(u), auth.PurposeReset, "", nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if r := h.post("/v1/auth/password/reset", "", map[string]any{"token": tok, "newPassword": "the reset password"}); r.status != http.StatusNoContent {
		t.Fatalf("reset: %d %s", r.status, r.raw)
	}
	expectError(t, h.post("/v1/auth/password/change", "", map[string]any{"passwordChangeTicket": tk, "newPassword": "the ticket password"}),
		http.StatusUnprocessableEntity, "invalid_argument")
	if r := h.login("bound@logitrack.test", "the reset password", "web", ""); r.status != http.StatusOK {
		t.Fatalf("the reset password must stay: %d %s", r.status, r.raw)
	}
}

// pause is a go-redis hook that holds the first DEL of an rl:login_fail key once armed: a login is
// stopped between its password check and its session (the sign-in clears its failure count there).
type pause struct {
	armed            atomic.Bool
	reached, release chan struct{}
}

func (p *pause) DialHook(next redis.DialHook) redis.DialHook { return next }

func (p *pause) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if args := cmd.Args(); cmd.Name() == "del" && len(args) > 1 && strings.Contains(fmt.Sprint(args[1]), "login_fail") &&
			p.armed.CompareAndSwap(true, false) {
			close(p.reached)
			<-p.release
		}
		return next(ctx, cmd)
	}
}

func (p *pause) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// A login whose password check overlaps a reset loses: it neither re-hashes the old password over the
// new one nor opens a session after the reset revoked every session.
func TestLoginRacingResetLoses(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	salt := []byte("fedcba9876543210")
	legacy, err := firebasescrypt.Hash(pw, salt, h.scrypt)
	if err != nil {
		t.Fatal(err)
	}
	racers := []struct{ name, email, id string }{
		{"legacy (verify-then-rehash)", "racer-legacy@logitrack.test", scalar[string](h, `INSERT INTO users (email, legacy_scrypt_hash, legacy_scrypt_salt)
			VALUES ('racer-legacy@logitrack.test', $1, $2) RETURNING id::text`, legacy, salt)},
		{"argon2id", "racer-argon@logitrack.test", h.user("racer-argon@logitrack.test")},
	}
	for _, rc := range racers {
		h.member(rc.id, own, "operator")
		p := &pause{reached: make(chan struct{}), release: make(chan struct{})}
		rdb := redis.NewClient(h.redisOptions())
		rdb.AddHook(p)
		t.Cleanup(func() { _ = rdb.Close() })
		racer, _ := h.service(h.cfg(), rdb)

		p.armed.Store(true)
		type result struct {
			res *auth.LoginResult
			err error
		}
		done := make(chan result, 1)
		go func() {
			res, err := racer.Login(context.Background(), auth.LoginInput{Email: rc.email, Password: pw, Platform: "web", IP: "203.0.113.7"})
			done <- result{res, err}
		}()
		select {
		case <-p.reached:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the login never reached its session step", rc.name)
		}
		var tok string
		if err := db.WithSystem(context.Background(), h.pool, nil, func(tx pgx.Tx) error {
			var err error
			tok, err = h.svc.IssuePasswordResetToken(context.Background(), tx, uuid.MustParse(rc.id), auth.PurposeReset, "", nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if r := h.post("/v1/auth/password/reset", "", map[string]any{"token": tok, "newPassword": "the password after the reset"}); r.status != http.StatusNoContent {
			t.Fatalf("%s: reset: %d %s", rc.name, r.status, r.raw)
		}
		close(p.release)
		got := <-done
		if got.err == nil || httpCode(got.err) != auth.CodeInvalidCredentials {
			t.Fatalf("%s: the racing login must lose: res=%v err=%v", rc.name, got.res != nil, got.err)
		}
		if n := scalar[int64](h, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, rc.id); n != 0 {
			t.Fatalf("%s: %d sessions survive the reset", rc.name, n)
		}
		expectError(t, h.login(rc.email, pw, "web", ""), http.StatusUnauthorized, auth.CodeInvalidCredentials)
		if r := h.login(rc.email, "the password after the reset", "web", ""); r.status != http.StatusOK {
			t.Fatalf("%s: the reset password: %d %s", rc.name, r.status, r.raw)
		}
	}
}

// Concurrent wrong passwords for one email cannot pass a lockout check made before any of them
// failed: exactly 5 are checked, the rest are locked.
func TestConcurrentWrongLoginsHonourLockout(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("burst@logitrack.test")
	h.member(u, own, "operator")
	const n = 20
	statuses := make([]int, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			r, err := h.do(h.internal, http.MethodPost, "/v1/auth/login", "", map[string]any{
				"email": "burst@logitrack.test", "password": fmt.Sprintf("wrong password %02d", i), "platform": "web"})
			statuses[i], errs[i] = r.status, err
		})
	}
	close(start)
	wg.Wait()
	count := map[int]int{}
	for i, st := range statuses {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		count[st]++
	}
	if count[http.StatusUnauthorized] != auth.LockoutThreshold || count[http.StatusLocked] != n-auth.LockoutThreshold {
		t.Fatalf("answers %v: want %d x 401 and %d x 423", count, auth.LockoutThreshold, n-auth.LockoutThreshold)
	}
	var lockouts int
	for _, ev := range h.outbox("security.event") {
		if ev["eventType"] == "login_lockout" {
			lockouts++
		}
	}
	if lockouts != 1 {
		t.Fatalf("login_lockout events = %d", lockouts)
	}
}

// Every auth transaction locks users -> sessions -> refresh_tokens, so a refresh racing a revocation,
// a password change or a tenant switch of the same user waits instead of deadlocking (40P01 -> 500).
func TestRefreshNeverDeadlocks(t *testing.T) {
	h := newHarness(t, func(c *auth.Config) { c.RateLimitEnabled = false })
	own := h.tenant("own_fleet", "Own")
	carrier := h.tenant("carrier", "Carrier A")
	u := h.user("race@logitrack.test")
	h.member(u, own, "manager")
	h.member(u, carrier, "operator")
	ctx := context.Background()

	// Deterministic: the T19 disable path holds the users row while a refresh arrives.
	mob := h.mustLogin("race@logitrack.test", "android", "inst-race")
	held := make(chan struct{})
	refreshed := make(chan resp, 1)
	go func() {
		<-held
		r, _ := h.do(h.internal, http.MethodPost, "/v1/auth/refresh", "", map[string]any{"refreshToken": mob.refresh})
		refreshed <- r
	}()
	var pc *auth.PostCommit
	if err := db.WithSystem(ctx, h.pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET status = 'disabled', disabled_at = now() WHERE id = $1`, u); err != nil {
			return err
		}
		close(held)
		deadline := time.Now().Add(10 * time.Second)
		// pg_locks is readable by every role (pg_stat_activity hides other roles' wait events).
		for scalar[int64](h, `SELECT count(*) FROM pg_locks WHERE NOT granted`) == 0 {
			if time.Now().After(deadline) {
				return errors.New("the refresh never waited for the users row")
			}
			time.Sleep(10 * time.Millisecond)
		}
		var err error
		pc, _, err = h.svc.RevokeInTx(ctx, tx, auth.Revocation{UserID: uuid.MustParse(u), Reason: auth.RevokeDisabled, BumpVersion: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.svc.Apply(ctx, pc)
	if r := <-refreshed; r.status != http.StatusUnauthorized || r.code() != auth.CodeSessionRevoked {
		t.Fatalf("refresh racing a disable: %d %s", r.status, r.raw)
	}
	h.exec(`UPDATE users SET status = 'active', disabled_at = NULL WHERE id = $1`, u)

	// Races of ordinary requests: any outcome but a 5xx is fine.
	password := pw
	race := func(name string, rounds int, other func(web, phone session) (resp, error), allowed ...int) {
		t.Helper()
		login := func(platform, install string) session {
			t.Helper()
			r := h.login("race@logitrack.test", password, platform, install)
			if r.status != http.StatusOK {
				t.Fatalf("%s: login: %d %s", name, r.status, r.raw)
			}
			s := session{access: r.str("accessToken"), refresh: r.str("refreshToken")}
			s.sid, _ = payload(t, s.access)["sid"].(string)
			return s
		}
		for i := range rounds {
			web, phone := login("web", ""), login("android", "inst-race")
			{
				var a, b resp
				var ea, eb error
				var wg sync.WaitGroup
				start := make(chan struct{})
				wg.Go(func() {
					<-start
					a, ea = h.do(h.internal, http.MethodPost, "/v1/auth/refresh", "", map[string]any{"refreshToken": phone.refresh})
				})
				wg.Go(func() { <-start; b, eb = other(web, phone) })
				close(start)
				wg.Wait()
				if ea != nil || eb != nil {
					t.Fatalf("%s round %d: %v %v", name, i, ea, eb)
				}
				revoked := a.status == http.StatusUnauthorized && a.code() == auth.CodeSessionRevoked
				if a.status != http.StatusOK && !revoked {
					t.Fatalf("%s round %d: refresh %d %s", name, i, a.status, a.raw)
				}
				ok := false
				for _, st := range allowed {
					ok = ok || b.status == st
				}
				if !ok {
					t.Fatalf("%s round %d: %d %s", name, i, b.status, b.raw)
				}
			}
		}
	}
	race("tenant switch of the refreshing session", 10, func(_, phone session) (resp, error) {
		return h.do(h.internal, http.MethodPost, "/v1/auth/tenant", phone.access, map[string]any{"tenantId": carrier})
	}, http.StatusOK)
	race("logout-all", 10, func(web, _ session) (resp, error) {
		return h.do(h.internal, http.MethodPost, "/v1/auth/logout-all", web.access, nil)
	}, http.StatusNoContent)
	race("delete the refreshing session", 10, func(web, phone session) (resp, error) {
		return h.do(h.internal, http.MethodDelete, "/v1/me/sessions/"+phone.sid, web.access, nil)
	}, http.StatusNoContent)
	race("password change on another session", 10, func(web, _ session) (resp, error) {
		next := fmt.Sprintf("rotated password %d", time.Now().UnixNano())
		r, err := h.do(h.internal, http.MethodPost, "/v1/auth/password/change", web.access, map[string]any{"currentPassword": password, "newPassword": next})
		if err == nil && r.status == http.StatusNoContent {
			password = next
		}
		return r, err
	}, http.StatusNoContent)
}
