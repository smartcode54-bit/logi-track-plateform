package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
)

// noDB is a pool for tests that never reach PostgreSQL.
type noDB struct{}

func (noDB) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("no database in this test")
}

// limitDeps are the collaborators of a service whose Redis is at url.
func limitDeps(t *testing.T, url string) Deps {
	t.Helper()
	rdb, ks, err := cache.Open(cache.Options{URL: url, AppEnv: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := password.NewHasher(password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := password.NewPolicy(10)
	if err != nil {
		t.Fatal(err)
	}
	return Deps{
		Pool: noDB{}, Store: NewStore(rdb, ks.Prefix()), Limiter: ratelimit.New(rdb, ks, zerolog.Nop()),
		Keys: token.New(priv, "http://localhost", "test", 15*time.Minute), Hasher: hasher, Policy: policy, Log: zerolog.Nop(),
	}
}

func limitConfig(enabled bool, login ratelimit.Limit) Config {
	return Config{RefreshTTLWeb: time.Hour, RefreshTTLMobile: time.Hour, PasswordResetTTL: time.Hour,
		RateLimitEnabled: enabled, LoginIP: login}
}

// The auth buckets run through the shared limiter (Appendix C §C.4.12): New needs it, takes
// RATE_LIMIT_LOGIN as a ratelimit.Limit (zero = the login_ip design default) and refuses a limit the
// limiter would reject, so no auth request can meet ErrInvalidLimit.
func TestNewChecksTheRateLimitWiring(t *testing.T) {
	url := cachetest.Hung(t) // never dialled here
	d := limitDeps(t, url)

	s, err := New(limitConfig(true, ratelimit.Limit{}), d)
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.LoginIP != ratelimit.LoginIP.Default {
		t.Fatalf("zero RATE_LIMIT_LOGIN = %v, want the login_ip default %v", s.cfg.LoginIP, ratelimit.LoginIP.Default)
	}
	for _, bad := range []ratelimit.Limit{{Count: 1001, Window: time.Millisecond}, {Count: -1, Window: time.Minute}, {Count: 5}} {
		if _, err := New(limitConfig(true, bad), d); err == nil || !strings.Contains(err.Error(), "RATE_LIMIT_LOGIN") {
			t.Errorf("LoginIP %v: err = %v", bad, err)
		}
	}
	noLimiter := d
	noLimiter.Limiter = nil
	if _, err := New(limitConfig(true, ratelimit.Limit{}), noLimiter); err == nil || !strings.Contains(err.Error(), "limiter") {
		t.Fatalf("nil limiter: err = %v", err)
	}
}

// A Redis that does not answer lets every auth request bucket through after the limiter's bound
// (fail open, Appendix B §B.6.3); with RATE_LIMIT_ENABLED off the limiter is not asked at all.
func TestAuthBucketsFailOpen(t *testing.T) {
	s, err := New(limitConfig(true, ratelimit.Limit{Count: 1, Window: time.Hour}), limitDeps(t, cachetest.Hung(t)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, b := range []ratelimit.Bucket{ratelimit.LoginIP, ratelimit.ForgotIP, ratelimit.ForgotEmail, ratelimit.ResetIP,
		ratelimit.RefreshSession, ratelimit.SSETicket, ratelimit.GoogleIP, ratelimit.GoogleNonceIP} {
		start := time.Now()
		if err := s.limit(ctx, b, "203.0.113.1"); err != nil {
			t.Fatalf("%s: %v", b.Name, err)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Fatalf("%s: a hung Redis cost %v", b.Name, took)
		}
	}

	hook := &countCalls{}
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	d := limitDeps(t, cachetest.Hung(t))
	d.Limiter = ratelimit.New(countingClient(t, hook), ks, zerolog.Nop())
	off, err := New(limitConfig(false, ratelimit.Limit{}), d)
	if err != nil {
		t.Fatal(err)
	}
	if err := off.limit(ctx, ratelimit.LoginIP, "203.0.113.1"); err != nil || hook.n > 0 {
		t.Fatalf("disabled: err %v, %d Redis calls", err, hook.n)
	}
}

// ipSubject groups IPv6 clients by /64 like ratelimit.ByIP and never skips a request without an
// address.
func TestIPSubject(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":       "203.0.113.7",
		"2001:db8:1:2::5":   "2001:db8:1:2::/64",
		"::ffff:192.0.2.1":  "::ffff:192.0.2.1",
		"":                  "unknown",
		"not an address ok": "not an address ok",
	} {
		if got := ipSubject(in); got != want {
			t.Errorf("ipSubject(%q) = %q, want %q", in, got, want)
		}
	}
}

// countCalls counts the commands a client sends.
type countCalls struct{ n int }

func (c *countCalls) DialHook(next redis.DialHook) redis.DialHook { return next }
func (c *countCalls) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { c.n++; return next(ctx, cmd) }
}
func (c *countCalls) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { c.n += len(cmds); return next(ctx, cmds) }
}

func countingClient(t *testing.T, h redis.Hook) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	rdb.AddHook(h)
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}
