package cache_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
)

const module = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api"

func init() { cache.RouteDriverLogs(zerolog.Nop()) }

// closedClient points at a port nothing listens on: Redis is "stopped".
func closedClient(t *testing.T) *cache.Cache {
	t.Helper()
	rdb, ks, err := cache.Open(cache.Options{URL: "redis://127.0.0.1:1/0?dial_timeout=1s", AppEnv: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return cache.New(rdb, ks)
}

// TestMoneyPathNeverReachesRedis: under cache.MoneyPath every command of a guarded client fails with
// ErrMoneyPath before it is sent, so a money path behaves the same with Redis stopped, and the
// read-through caches refuse instead of serving (AC: no pricing, period-lock, invoice-numbering or
// payroll path reads a Redis key).
func TestMoneyPathNeverReachesRedis(t *testing.T) {
	c := closedClient(t)
	ctx := cache.MoneyPath(context.Background())
	if !cache.IsMoneyPath(ctx) || cache.IsMoneyPath(context.Background()) {
		t.Fatal("IsMoneyPath")
	}
	loaded := false
	load := func(context.Context) ([]string, error) { loaded = true; return []string{"2026-09"}, nil }

	start := time.Now()
	if _, err := c.PeriodLocks(ctx, "p1", load); !errors.Is(err, cache.ErrMoneyPath) {
		t.Errorf("PeriodLocks: %v", err)
	}
	if _, err := c.HubMaps(ctx, func(context.Context) (cache.HubMaps, error) { loaded = true; return cache.HubMaps{}, nil }); !errors.Is(err, cache.ErrMoneyPath) {
		t.Errorf("HubMaps: %v", err)
	}
	if _, err := cache.GetJSON(ctx, c, c.Keys().RateCard("p1"), time.Minute, load); !errors.Is(err, cache.ErrMoneyPath) {
		t.Errorf("GetJSON: %v", err)
	}
	if _, err := c.Subtenants(ctx, "t1", load); !errors.Is(err, cache.ErrMoneyPath) {
		t.Errorf("Subtenants: %v", err)
	}
	if _, _, err := c.MirrorAck(ctx, "trips", "d1"); !errors.Is(err, cache.ErrMoneyPath) {
		t.Errorf("MirrorAck: %v", err)
	}
	if err := c.Invalidate(ctx, c.Keys().HubsAll()); !errors.Is(err, cache.ErrMoneyPath) {
		t.Errorf("Invalidate: %v", err)
	}
	if loaded {
		t.Error("a loader ran under MoneyPath: the cache served a money path")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("guarded calls took %v: they reached the network", d)
	}

	// The raw client is guarded too (a stray rdb.Get in money code).
	rdb, _, err := cache.Open(cache.Options{URL: "redis://127.0.0.1:1/0", AppEnv: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rdb.Close() }()
	if err := rdb.Get(ctx, "lt:local:cache:hubs:all").Err(); !errors.Is(err, cache.ErrMoneyPath) {
		t.Errorf("raw GET: %v", err)
	}
	p := rdb.Pipeline()
	get := p.Get(ctx, "lt:local:cache:hubs:all")
	if _, err := p.Exec(ctx); !errors.Is(err, cache.ErrMoneyPath) || !errors.Is(get.Err(), cache.ErrMoneyPath) {
		t.Errorf("pipeline: %v / %v", err, get.Err())
	}
	// Without the mark the same client does try the network (Redis is stopped).
	if err := rdb.Get(context.Background(), "x").Err(); err == nil || errors.Is(err, cache.ErrMoneyPath) {
		t.Errorf("unmarked GET against a stopped Redis: %v", err)
	}
}

// TestUIReadsFallBackToTheSourceWhenRedisIsStopped: cache failures never fail a UI read.
func TestUIReadsFallBackToTheSourceWhenRedisIsStopped(t *testing.T) {
	c := closedClient(t)
	ctx := context.Background()
	got, err := c.PeriodLocks(ctx, "p1", func(context.Context) ([]string, error) { return []string{"2026-09", "2026-08"}, nil })
	if err != nil || !slices.Equal(got, []string{"2026-08", "2026-09"}) {
		t.Fatalf("PeriodLocks = %v, %v", got, err)
	}
	m, err := c.HubMaps(ctx, func(context.Context) (cache.HubMaps, error) {
		return cache.HubMaps{NameToCode: map[string]string{"บางปู": "SPK-GW"}, CodeToName: map[string]string{"SPK-GW": "บางปู"}}, nil
	})
	if err != nil || m.NameToCode["บางปู"] != "SPK-GW" {
		t.Fatalf("HubMaps = %v, %v", m, err)
	}
}

// TestMoneyEnginesNeverLinkRedis: the pure money engines (and their future siblings) cannot even
// link the Redis client or this package; TestImportsArePure (T36) restricts their direct imports, this
// checks the transitive closure.
func TestMoneyEnginesNeverLinkRedis(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not on PATH")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "..")) // the module root
	if err != nil {
		t.Fatal(err)
	}
	patterns := []string{"./internal/billing/compute/...", "./internal/billing/documents/...", "./internal/hr/compute/..."}
	cmd := exec.Command(gobin, append([]string{"list", "-e", "-deps", "-f", "{{.ImportPath}}"}, patterns...)...)
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr // "matched no packages" for engines that do not exist yet
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	deps := strings.Fields(string(out))
	if !slices.Contains(deps, module+"/internal/billing/compute") {
		t.Fatalf("go list did not resolve the billing engine: %s", out)
	}
	for _, d := range deps {
		if strings.HasPrefix(d, "github.com/redis/") || d == module+"/internal/platform/cache" ||
			strings.HasPrefix(d, module+"/internal/platform/httpx/") {
			t.Errorf("a money engine depends on %s", d)
		}
	}
}

// TestComposeRedisIsAOFAndNoeviction: the compose service runs with AOF and noeviction (Appendix B
// §B.6.1: idempotency records and revocations must never be evicted); cachetest starts the same flags.
func TestComposeRedisIsAOFAndNoeviction(t *testing.T) {
	image, cmd, err := cachetest.ComposeRedis()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(image, "redis:7") {
		t.Errorf("image %q, want redis:7-alpine (main spec §2.1)", image)
	}
	flags := map[string]string{}
	for i := 0; i+1 < len(cmd); i++ {
		if strings.HasPrefix(cmd[i], "--") {
			flags[cmd[i]] = cmd[i+1]
		}
	}
	if flags["--appendonly"] != "yes" {
		t.Errorf("--appendonly = %q, want yes", flags["--appendonly"])
	}
	if flags["--maxmemory-policy"] != "noeviction" {
		t.Errorf("--maxmemory-policy = %q, want noeviction", flags["--maxmemory-policy"])
	}
}
