//go:build integration

package ratelimit_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
)

func TestMain(m *testing.M) {
	cache.RouteDriverLogs(zerolog.Nop())
	os.Exit(cachetest.Main(m))
}

func TestGCRABurstThenSteadyRate(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	l := ratelimit.New(rdb, ks, zerolog.Nop())
	ctx := context.Background()
	// Burst 5, then one per second: slow enough that no request regenerates between the calls below.
	lim := ratelimit.Limit{Count: 5, Window: 5 * time.Second}
	for i := range 5 {
		d, err := l.Allow(ctx, "test", "10.0.0.1", lim)
		if err != nil || !d.Allowed || d.Remaining != 4-i {
			t.Fatalf("request %d: %+v %v", i+1, d, err)
		}
	}
	d, err := l.Allow(ctx, "test", "10.0.0.1", lim)
	if err != nil || d.Allowed {
		t.Fatalf("6th request allowed: %+v %v", d, err)
	}
	if d.RetryAfter <= 0 || d.RetryAfter > time.Second {
		t.Fatalf("RetryAfter %v, want (0, 1s]", d.RetryAfter)
	}
	// Another subject has its own bucket.
	if d, _ := l.Allow(ctx, "test", "10.0.0.2", lim); !d.Allowed {
		t.Fatal("a second subject was limited")
	}
	time.Sleep(d.RetryAfter + 50*time.Millisecond)
	if d, err := l.Allow(ctx, "test", "10.0.0.1", lim); err != nil || !d.Allowed {
		t.Fatalf("after RetryAfter: %+v %v", d, err)
	}
	if d, _ := l.Allow(ctx, "test", "10.0.0.1", lim); d.Allowed {
		t.Fatal("only one request regenerated per emission interval")
	}
	// The key is rl:{bucket}:{hashed subject} and expires once the bucket is full again.
	key := ks.RateLimit("test", ratelimit.SubjectKey("10.0.0.1"))
	if ttl := rdb.PTTL(ctx, key).Val(); ttl <= 0 || ttl > lim.Window+50*time.Millisecond {
		t.Fatalf("PTTL %v, want about the window", ttl)
	}
	for _, k := range cachetest.AssertKeyspace(t, rdb, ks) {
		if strings.Contains(k, "10.0.0") {
			t.Fatalf("subject in clear text: %s", k)
		}
	}
}

func TestResetAndCostAboveBurst(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	l := ratelimit.New(rdb, ks, zerolog.Nop())
	ctx := context.Background()
	lim := ratelimit.ForgotEmail.Default // 3/h
	for i := range 3 {
		if d, err := l.Allow(ctx, ratelimit.ForgotEmail.Name, "sha256-of-email", lim); err != nil || !d.Allowed {
			t.Fatalf("request %d: %+v %v", i+1, d, err)
		}
	}
	d, err := l.Allow(ctx, ratelimit.ForgotEmail.Name, "sha256-of-email", lim)
	if err != nil || d.Allowed || d.RetryAfter < 19*time.Minute {
		t.Fatalf("4th request: %+v %v, want denied for about 20 min", d, err)
	}
	if err := l.Reset(ctx, ratelimit.ForgotEmail.Name, "sha256-of-email"); err != nil {
		t.Fatal(err)
	}
	if d, _ := l.Allow(ctx, ratelimit.ForgotEmail.Name, "sha256-of-email", lim); !d.Allowed || d.Remaining != 2 {
		t.Fatalf("after reset: %+v", d)
	}
	if _, err := l.AllowN(ctx, "x", "y", ratelimit.Limit{Count: 2, Window: time.Second}, 3); !errors.Is(err, ratelimit.ErrInvalidLimit) {
		t.Fatalf("a cost above the burst: %v", err)
	}
	// login_fail is not a GCRA bucket (its lockout lives in internal/auth): refused, nothing written.
	if _, err := l.Allow(ctx, ratelimit.LoginFail.Name, "e", ratelimit.LoginFail.Default); !errors.Is(err, ratelimit.ErrInvalidLimit) {
		t.Fatalf("login_fail through GCRA: %v", err)
	}
	if n := rdb.Exists(ctx, ks.RateLimit(ratelimit.LoginFail.Name, ratelimit.SubjectKey("e"))).Val(); n != 0 {
		t.Fatal("login_fail wrote a GCRA key")
	}
}

// TestConcurrentChecksAreCountedAtomically: a check counts the request in the same script, so a burst
// of parallel requests for one subject gets exactly Count through, never a check-then-count race.
func TestConcurrentChecksAreCountedAtomically(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	l := ratelimit.New(rdb, ks, zerolog.Nop())
	lim := ratelimit.Limit{Count: 5, Window: 15 * time.Minute}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if d, err := l.Allow(context.Background(), "burst", "victim@example.com", lim); err == nil && d.Allowed {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != int32(lim.Count) {
		t.Fatalf("%d of 50 parallel requests allowed, want %d", allowed.Load(), lim.Count)
	}
}

// TestIPv6ClientsAreLimitedPerSlash64: rotating through the addresses of one /64 does not escape a
// per-IP bucket.
func TestIPv6ClientsAreLimitedPerSlash64(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	l := ratelimit.New(rdb, ks, zerolog.Nop())
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Post("/v1/auth/login", l.Middleware(true, ratelimit.Rule{
		Bucket: ratelimit.LoginIP, By: func(c fiber.Ctx) string { return ratelimit.IPSubject(c.Get("X-Test-IP")) },
	}), func(c fiber.Ctx) error { return c.SendStatus(204) })
	denied := 0
	for i := range 50 {
		if code, _, _ := post(t, app, fmt.Sprintf("2001:db8:1234:5678::%x", i+1)); code == 429 {
			denied++
		}
	}
	if denied != 40 {
		t.Fatalf("%d of 50 requests from one /64 denied, want 40 (10/min)", denied)
	}
	if code, _, _ := post(t, app, "2001:db8:1234:5679::1"); code != 204 {
		t.Fatalf("the neighbouring /64 was limited: %d", code)
	}
}

func newApp(l *ratelimit.Limiter, enabled bool, lim ratelimit.Limit) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Use(httpx.RequestID())
	app.Post("/v1/auth/login", l.Middleware(enabled, ratelimit.Rule{
		Bucket: ratelimit.LoginIP, Limit: lim, By: func(c fiber.Ctx) string { return c.Get("X-Test-IP") },
	}), func(c fiber.Ctx) error { return httpx.JSON(c, 200, map[string]bool{"ok": true}) })
	return app
}

func post(t *testing.T, app *fiber.App, ip string) (int, string, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/auth/login", nil)
	req.Header.Set("X-Test-IP", ip)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, resp.Header.Get("Retry-After"), body
}

func TestMiddlewareAnswers429WithRetryAfter(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	app := newApp(ratelimit.New(rdb, ks, zerolog.Nop()), true, ratelimit.Limit{Count: 2, Window: time.Minute})
	for range 2 {
		if code, _, _ := post(t, app, "203.0.113.7"); code != 200 {
			t.Fatalf("status %d", code)
		}
	}
	code, retry, body := post(t, app, "203.0.113.7")
	if code != 429 || retry == "" || retry == "0" {
		t.Fatalf("status %d Retry-After %q", code, retry)
	}
	e, _ := body["error"].(map[string]any)
	details, _ := e["details"].(map[string]any)
	if e["code"] != "resource_exhausted" || details["bucket"] != "login_ip" || e["requestId"] == "" || len(e) != 4 {
		t.Fatalf("envelope %v", body)
	}
	if code, _, _ := post(t, app, ""); code != 200 {
		t.Fatalf("an empty subject must skip the rule, got %d", code)
	}
}

func TestMiddlewareDisabledAndFailOpen(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	lim := ratelimit.Limit{Count: 1, Window: time.Minute}
	off := newApp(ratelimit.New(rdb, ks, zerolog.Nop()), false, lim)
	for range 3 {
		if code, _, _ := post(t, off, "198.51.100.1"); code != 200 {
			t.Fatalf("disabled limiter answered %d", code)
		}
	}
	if n := len(cachetest.Keys(t, rdb)); n != 0 {
		t.Fatalf("disabled limiter wrote %d keys", n)
	}

	srv := cachetest.StartDedicated(t)
	down, dks := srv.Client(t)
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	open := newApp(ratelimit.New(down, dks, zerolog.Nop()), true, lim)
	for range 3 {
		if code, _, _ := post(t, open, "198.51.100.1"); code != 200 {
			t.Fatalf("Redis stopped: answered %d, want the request let through", code)
		}
	}
}
