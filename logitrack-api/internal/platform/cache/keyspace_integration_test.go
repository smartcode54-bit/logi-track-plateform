//go:build integration

package cache_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/idempotency"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// TestKeyScanShowsOnlyThePrefixAndListedNamespaces drives every part of the Redis layer that writes
// keys (read-through caches, hub maps, period locks, contractor reach, mirror ack, tickets, rate
// limits, idempotency records and locks, invalidation) against one database, then SCANs it: every key
// is under lt:{APP_ENV}: and in a namespace of Appendix B §B.6.1.
func TestKeyScanShowsOnlyThePrefixAndListedNamespaces(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	ctx := context.Background()
	c := cache.New(rdb, ks)

	if _, err := cache.GetJSON(ctx, c, ks.RateCard("party-1"), time.Hour, func(context.Context) ([]int, error) { return []int{1}, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.GetJSON(ctx, c, ks.Settings("mobile_app"), time.Minute, func(context.Context) (string, error) { return "x", nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := c.HubMaps(ctx, func(context.Context) (cache.HubMaps, error) {
		return cache.HubMaps{NameToCode: map[string]string{"บางปู": "SPK-GW"}, CodeToName: map[string]string{"SPK-GW": "บางปู"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PeriodLocks(ctx, "party-1", func(context.Context) ([]string, error) { return []string{"2026-09"}, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Subtenants(ctx, "own", func(context.Context) ([]string, error) { return []string{"carrier"}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMirrorAck(ctx, "trip_records", "doc-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutTicket(ctx, ks.AuthPasswordChange("ticket"), []byte(`{"userId":"u"}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.OnEvent(ctx, cache.Event{RoutingKey: "settings.changed", AggregateID: "mobile_app"}); err != nil {
		t.Fatal(err)
	}

	l := ratelimit.New(rdb, ks, zerolog.Nop())
	if _, err := l.Allow(ctx, ratelimit.LoginIP.Name, "203.0.113.9", ratelimit.LoginIP.Default); err != nil {
		t.Fatal(err)
	}

	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := idempotency.New(idempotency.Options{
		Redis: rdb, Keys: ks, Store: idempotency.NewPGStore(d.Pool(t, db.RoleApp)), TTL: 168 * time.Hour,
		Scope: func(fiber.Ctx) string { return "user-1" }, Log: zerolog.Nop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	held := make(chan struct{})
	release := make(chan struct{})
	app.Post("/w", m.Handler(), func(c fiber.Ctx) error {
		if strings.Contains(string(c.Body()), "hold") {
			close(held)
			<-release
		}
		return httpx.JSON(c, 201, map[string]bool{"ok": true})
	})
	send := func(key, body string) int {
		req := httptest.NewRequest("POST", "/w", strings.NewReader(body))
		req.Header.Set(idempotency.Header, key)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
		if err != nil {
			t.Error(err)
			return 0
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := send("0192f1c2-7d3e-7a10-8b2c-000000000001", `{}`); code != 201 {
		t.Fatalf("idempotent write: %d", code)
	}
	done := make(chan int)
	go func() { done <- send("0192f1c2-7d3e-7a10-8b2c-000000000002", `{"hold":true}`) }()
	<-held // the in-flight lock exists now

	keys := cachetest.AssertKeyspace(t, rdb, ks)
	close(release)
	if code := <-done; code != 201 {
		t.Fatalf("held write: %d", code)
	}

	seen := map[cache.Namespace]bool{}
	for _, k := range keys {
		ns, _ := ks.Namespace(k)
		seen[ns] = true
	}
	for _, ns := range []cache.Namespace{cache.NSCache, cache.NSAuth, cache.NSIdem, cache.NSRateLimit} {
		if !seen[ns] {
			t.Errorf("no %s: key written; scanned %v", ns, keys)
		}
	}
	for _, want := range []string{ks.HubsNameToCode(), ks.HubsCodeToName(), ks.IdemLock("user-1", "0192f1c2-7d3e-7a10-8b2c-000000000002"),
		ks.IdemHTTP("user-1", "0192f1c2-7d3e-7a10-8b2c-000000000001")} {
		found := false
		for _, k := range keys {
			found = found || k == want
		}
		if !found {
			t.Errorf("expected key %s in the scan", want)
		}
	}
	if testing.Verbose() {
		b, _ := json.MarshalIndent(keys, "", "  ")
		t.Logf("keys:\n%s", b)
	}
}
