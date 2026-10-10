//go:build integration

package ratelimit_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
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

func TestPeekAndReset(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	l := ratelimit.New(rdb, ks, zerolog.Nop())
	ctx := context.Background()
	lim := ratelimit.LoginFail.Default // 5 failures / 15 min
	subject := "sha256-of-email"
	for i := range 5 {
		if d, err := l.Peek(ctx, ratelimit.LoginFail.Name, subject, lim); err != nil || !d.Allowed || d.Remaining != 5-i {
			t.Fatalf("peek before failure %d: %+v %v", i+1, d, err)
		}
		if d, _ := l.Allow(ctx, ratelimit.LoginFail.Name, subject, lim); !d.Allowed {
			t.Fatalf("failure %d denied", i+1)
		}
	}
	d, err := l.Peek(ctx, ratelimit.LoginFail.Name, subject, lim)
	if err != nil || d.Allowed || d.RetryAfter < 2*time.Minute {
		t.Fatalf("peek after 5 failures: %+v %v (want locked)", d, err)
	}
	if d2, _ := l.Peek(ctx, ratelimit.LoginFail.Name, subject, lim); d2.Allowed {
		t.Fatal("a peek consumed or released something")
	}
	if err := l.Reset(ctx, ratelimit.LoginFail.Name, subject); err != nil {
		t.Fatal(err)
	}
	if d, _ := l.Peek(ctx, ratelimit.LoginFail.Name, subject, lim); !d.Allowed || d.Remaining != 5 {
		t.Fatalf("after reset: %+v", d)
	}
	if _, err := l.AllowN(ctx, "x", "y", ratelimit.Limit{Count: 2, Window: time.Second}, 3); err == nil {
		t.Fatal("a cost above the burst was accepted")
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
