package ratelimit

import (
	"context"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestParseLimit(t *testing.T) {
	ok := map[string]Limit{
		"10/1m": {10, time.Minute}, " 5 / 15m ": {5, 15 * time.Minute}, "60/1m": {60, time.Minute}, "1/24h": {1, 24 * time.Hour},
	}
	for in, want := range ok {
		got, err := ParseLimit(in)
		if err != nil || got != want {
			t.Errorf("ParseLimit(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	// 10/5ns and 10/1ns have an interval below the script's 1µs resolution.
	for _, in := range []string{"", "10", "10/", "/1m", "0/1m", "-1/1m", "10/0s", "10/-1m", "ten/1m", "10/1x", "10/1d", "10/5ns", "10/1ns", "1001/1ms"} {
		if _, err := ParseLimit(in); err == nil {
			t.Errorf("ParseLimit(%q) accepted", in)
		}
	}
}

func TestBucketCatalog(t *testing.T) {
	names := map[string]bool{}
	for _, b := range Buckets {
		if names[b.Name] {
			t.Errorf("duplicate bucket %s", b.Name)
		}
		names[b.Name] = true
		noDefault := b.Name == Webhook.Name || b.Name == LoginFail.Name
		if noDefault == b.Default.valid() {
			t.Errorf("%s: default %v (only webhook and login_fail have none)", b.Name, b.Default)
		}
	}
	// Appendix B §B.6.3 design values.
	for b, want := range map[Bucket]Limit{
		LoginIP: {10, time.Minute}, PublicFormIP: {5, time.Hour},
		PublicFormEmail: {1, 24 * time.Hour}, EvidenceIP: {60, time.Minute}, HeartbeatInstall: {1, 30 * time.Second},
		User: {600, time.Minute}, APIKey: {600, time.Minute},
	} {
		if b.Default != want {
			t.Errorf("%s default %v, want %v", b.Name, b.Default, want)
		}
	}
	var envs []string
	for _, b := range Buckets {
		if b.Env != "" {
			envs = append(envs, b.Env)
		}
	}
	slices.Sort(envs)
	if want := []string{"RATE_LIMIT_EVIDENCE", "RATE_LIMIT_LOGIN", "RATE_LIMIT_PUBLIC_FORMS"}; !slices.Equal(envs, want) {
		t.Errorf("configurable buckets %v, want %v", envs, want)
	}
}

func TestConfig(t *testing.T) {
	cfg, err := config.LoadFrom[Config]([]string{"RATE_LIMIT_LOGIN=20/1m"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Limit(LoginIP) != (Limit{20, time.Minute}) || cfg.Limit(PublicFormIP) != (Limit{5, time.Hour}) ||
		cfg.Limit(EvidenceIP) != (Limit{60, time.Minute}) || cfg.Limit(User) != User.Default {
		t.Fatalf("cfg = %+v", cfg)
	}
	_, err = config.LoadFrom[Config]([]string{"RATE_LIMIT_PUBLIC_FORMS=secret-looking-value", "RATE_LIMIT_ENABLED=false"})
	if err == nil || !strings.Contains(err.Error(), "RATE_LIMIT_PUBLIC_FORMS") || strings.Contains(err.Error(), "secret-looking-value") {
		t.Fatalf("invalid value: %v", err)
	}
	// An interval below 1µs would fail open on every request: refused at start.
	if _, err := config.LoadFrom[Config]([]string{"RATE_LIMIT_LOGIN=10/5ns"}); err == nil || !strings.Contains(err.Error(), "RATE_LIMIT_LOGIN") {
		t.Fatalf("10/5ns accepted: %v", err)
	}
	// Each variable sets one bucket (Appendix B §B.6.3): login_fail and public_form_email are not
	// configuration.
	cfg, err = config.LoadFrom[Config]([]string{"RATE_LIMIT_LOGIN=3/1m", "RATE_LIMIT_PUBLIC_FORMS=7/1m"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limit(LoginIP) != (Limit{3, time.Minute}) || cfg.Limit(LoginFail) != (Limit{}) ||
		cfg.Limit(PublicFormIP) != (Limit{7, time.Minute}) || cfg.Limit(PublicFormEmail) != (Limit{1, 24 * time.Hour}) {
		t.Fatalf("limits: login_ip %v login_fail %v public_form_ip %v public_form_email %v",
			cfg.Limit(LoginIP), cfg.Limit(LoginFail), cfg.Limit(PublicFormIP), cfg.Limit(PublicFormEmail))
	}
}

func TestSubjectKeyIsFixedLengthAndDeterministic(t *testing.T) {
	k := SubjectKey("someone@example.com")
	if len(k) != 32 || strings.Contains(k, "example") || k != SubjectKey("someone@example.com") || k == SubjectKey("other@example.com") {
		t.Fatalf("SubjectKey = %q", k)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	for d, want := range map[time.Duration]int{0: 1, time.Millisecond: 1, time.Second: 1, 1500 * time.Millisecond: 2, time.Minute: 60} {
		if got := (Decision{RetryAfter: d}).RetryAfterSeconds(); got != want {
			t.Errorf("RetryAfterSeconds(%v) = %d, want %d", d, got, want)
		}
	}
}

func TestIPSubjectGroupsIPv6By64(t *testing.T) {
	same := []string{"2001:db8:1:2::1", "2001:db8:1:2:ffff::9", "2001:db8:1:2:abcd:ef01:2345:6789"}
	for _, ip := range same {
		if got := IPSubject(ip); got != "2001:db8:1:2::/64" {
			t.Errorf("IPSubject(%q) = %q, want the /64", ip, got)
		}
	}
	for in, want := range map[string]string{
		"2001:db8:1:3::1":    "2001:db8:1:3::/64",
		"fe80::1%eth0":       "fe80::/64",
		"203.0.113.7":        "203.0.113.7",
		"::ffff:203.0.113.7": "::ffff:203.0.113.7", // never produced by httpx.ClientIP (it unmaps); kept as is
		"":                   "",
		"not-an-ip":          "not-an-ip",
	} {
		if got := IPSubject(in); got != want {
			t.Errorf("IPSubject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInvalidLimitIsAnErrorNotARedisFailure(t *testing.T) {
	l := New(nil, cache.Keyspace{}, zerolog.Nop()) // never reaches Redis
	for _, lim := range []Limit{LoginFail.Default, Webhook.Default, {Count: 10, Window: 5 * time.Nanosecond}} {
		d, err := l.Allow(context.Background(), "b", "s", lim)
		if !errors.Is(err, ErrInvalidLimit) || !d.Allowed {
			t.Errorf("Allow with %v: %+v %v, want ErrInvalidLimit", lim, d, err)
		}
	}
	if _, err := l.AllowN(context.Background(), "b", "s", Limit{2, time.Second}, 3); !errors.Is(err, ErrInvalidLimit) {
		t.Errorf("cost above the burst: %v", err)
	}
}

func TestMiddlewareRejectsUnusableRulesWhenBuilt(t *testing.T) {
	l := New(nil, cache.Keyspace{}, zerolog.Nop())
	panics := func(name string, rules ...Rule) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		l.Middleware(true, rules...)
	}
	panics("webhook without a limit", Rule{Bucket: Webhook, By: ByIP})
	panics("login_fail is not a GCRA bucket", Rule{Bucket: LoginFail, By: ByIP})
	panics("no subject", Rule{Bucket: LoginIP})
	panics("cost above the burst", Rule{Bucket: HeartbeatInstall, Cost: 2, By: ByIP})
	panics("unusable explicit limit", Rule{Bucket: User, Limit: Limit{10, time.Nanosecond}, By: ByIP})
	l.Middleware(true, Rule{Bucket: Webhook, Limit: per(100, time.Minute), By: ByProviderIP("cartrack")}, Rule{Bucket: User, By: ByIP})
}

// TestHungRedisCostsACheckAtMostTheCallTimeout: a Redis that accepts connections but never answers
// (stalled fork or fsync, paused VM, half-open network) must not stall requests: the check gives up
// after callTimeout and the middleware lets the request through.
func TestHungRedisCostsACheckAtMostTheCallTimeout(t *testing.T) {
	rdb, ks, err := cache.Open(cache.Options{URL: cachetest.Hung(t), AppEnv: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	l := New(rdb, ks, zerolog.Nop())
	for range 2 {
		start := time.Now()
		d, err := l.Allow(context.Background(), LoginIP.Name, "203.0.113.7", LoginIP.Default)
		if took := time.Since(start); err == nil || !d.Allowed || took > callTimeout+400*time.Millisecond {
			t.Fatalf("Allow: %+v %v after %v, want a fail-open error within about %v", d, err, took, callTimeout)
		}
	}
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Post("/v1/auth/login", l.Middleware(true, Rule{Bucket: LoginIP, By: func(fiber.Ctx) string { return "203.0.113.7" }}),
		func(c fiber.Ctx) error { return c.SendStatus(204) })
	start := time.Now()
	resp, err := app.Test(httptest.NewRequest("POST", "/v1/auth/login", nil), fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if took := time.Since(start); resp.StatusCode != 204 || took > callTimeout+400*time.Millisecond {
		t.Fatalf("middleware: %d after %v", resp.StatusCode, took)
	}
}
