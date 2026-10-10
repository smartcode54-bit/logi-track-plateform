//go:build integration

// Acceptance tests of the Redis layer (issue T09) against redis:7-alpine with the compose flags
// (cachetest): read-through caches and their invalidation, the two hub-map keys, the period-lock
// hint, contractor reach, the mirror ack, single-use tickets, the rt:cache pub/sub, Redis stopped, and
// the key scan over every namespace.
package cache_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
)

func TestMain(m *testing.M) {
	code := pgtest.Main(m) // the keyspace scan also drives the idempotency middleware (PostgreSQL)
	cachetest.TerminateShared()
	os.Exit(code)
}

func newCache(t *testing.T, opts ...cache.Option) (*cache.Cache, *redis.Client) {
	t.Helper()
	rdb, ks := cachetest.NewClient(t)
	return cache.New(rdb, ks, opts...), rdb
}

func TestServerRunsAOFAndNoeviction(t *testing.T) {
	_, rdb := newCache(t)
	ctx := context.Background()
	got := map[string]string{}
	for _, p := range []string{"appendonly", "maxmemory-policy"} {
		v, err := rdb.ConfigGet(ctx, p).Result()
		if err != nil {
			t.Fatal(err)
		}
		got[p] = v[p]
	}
	if got["appendonly"] != "yes" || got["maxmemory-policy"] != "noeviction" {
		t.Fatalf("CONFIG GET = %v, want appendonly yes and maxmemory-policy noeviction", got)
	}
}

func TestGetJSONReadThroughAndInvalidate(t *testing.T) {
	c, rdb := newCache(t)
	ctx := context.Background()
	key := c.Keys().Settings("mobile_app")
	var loads atomic.Int32
	type settings struct {
		MinAllowedVersion string `json:"minAllowedVersion"`
	}
	load := func(context.Context) (settings, error) {
		loads.Add(1)
		return settings{MinAllowedVersion: "3.2.0"}, nil
	}
	for range 3 {
		v, err := cache.GetJSON(ctx, c, key, c.TTLs().Settings, load)
		if err != nil || v.MinAllowedVersion != "3.2.0" {
			t.Fatalf("GetJSON = %+v, %v", v, err)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("loader ran %d times, want 1 (read-through)", loads.Load())
	}
	if ttl := rdb.PTTL(ctx, key).Val(); ttl <= 0 || ttl > c.TTLs().Settings {
		t.Fatalf("PTTL = %v, want (0, %v]", ttl, c.TTLs().Settings)
	}
	if err := c.OnEvent(ctx, cache.Event{RoutingKey: "settings.changed", AggregateID: "mobile_app"}); err != nil {
		t.Fatal(err)
	}
	if rdb.Exists(ctx, key).Val() != 0 {
		t.Fatal("settings.changed left the key")
	}
	if _, err := cache.GetJSON(ctx, c, key, c.TTLs().Settings, load); err != nil || loads.Load() != 2 {
		t.Fatalf("after invalidation: loads %d, err %v", loads.Load(), err)
	}
	// A loader error is returned and nothing is cached.
	boom := errors.New("pg down")
	if _, err := cache.GetJSON(ctx, c, c.Keys().Settings("x"), time.Minute, func(context.Context) (settings, error) {
		return settings{}, boom
	}); !errors.Is(err, boom) || rdb.Exists(ctx, c.Keys().Settings("x")).Val() != 0 {
		t.Fatalf("loader error: %v", err)
	}
}

// TestHubMapsAreTwoKeysDroppedTogether: cache:hubs:n2c and cache:hubs:c2n are distinct hashes, never
// merged, and both are dropped on hubs.changed (with cache:hubs:all).
func TestHubMapsAreTwoKeysDroppedTogether(t *testing.T) {
	c, rdb := newCache(t)
	ctx := context.Background()
	ks := c.Keys()
	var loads atomic.Int32
	load := func(context.Context) (cache.HubMaps, error) {
		loads.Add(1)
		return cache.HubMaps{
			NameToCode: map[string]string{"ห้วยขวาง10": "SPK890174", "J&T EXPRESS บางปู": "SPK-GW"},
			CodeToName: map[string]string{"SPK890174": "ห้วยขวาง10", "SPK-GW": "J&T EXPRESS บางปู"},
		}, nil
	}
	m, err := c.HubMaps(ctx, load)
	if err != nil {
		t.Fatal(err)
	}
	if m.NameToCode["ห้วยขวาง10"] != "SPK890174" || m.CodeToName["SPK-GW"] != "J&T EXPRESS บางปู" {
		t.Fatalf("maps = %+v", m)
	}
	for _, k := range []string{ks.HubsNameToCode(), ks.HubsCodeToName()} {
		if typ := rdb.Type(ctx, k).Val(); typ != "hash" {
			t.Fatalf("%s is %q, want a hash", k, typ)
		}
	}
	n2c := rdb.HGetAll(ctx, ks.HubsNameToCode()).Val()
	c2n := rdb.HGetAll(ctx, ks.HubsCodeToName()).Val()
	if _, merged := n2c["SPK890174"]; merged {
		t.Fatal("n2c holds a code -> name entry: the maps were merged")
	}
	if _, merged := c2n["ห้วยขวาง10"]; merged {
		t.Fatal("c2n holds a name -> code entry: the maps were merged")
	}
	if _, err := c.HubMaps(ctx, load); err != nil || loads.Load() != 1 {
		t.Fatalf("second read: loads %d, err %v", loads.Load(), err)
	}
	if err := rdb.Set(ctx, ks.HubsAll(), `[]`, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.OnEvent(ctx, cache.Event{RoutingKey: "hubs.changed", AggregateID: "SPK-GW"}); err != nil {
		t.Fatal(err)
	}
	if n := rdb.Exists(ctx, ks.HubsNameToCode(), ks.HubsCodeToName(), ks.HubsAll()).Val(); n != 0 {
		t.Fatalf("%d of n2c, c2n, all survived hubs.changed", n)
	}
	if _, err := c.HubMaps(ctx, load); err != nil || loads.Load() != 2 {
		t.Fatalf("after hubs.changed: loads %d, err %v", loads.Load(), err)
	}
	// One key missing is a miss for both (they are always written together).
	rdb.Del(ctx, ks.HubsCodeToName())
	if _, err := c.HubMaps(ctx, load); err != nil || loads.Load() != 3 {
		t.Fatalf("half-present maps: loads %d, err %v", loads.Load(), err)
	}
	// Empty maps are cached too.
	rdb.Del(ctx, ks.HubsNameToCode(), ks.HubsCodeToName())
	empty := func(context.Context) (cache.HubMaps, error) { loads.Add(1); return cache.HubMaps{}, nil }
	for range 2 {
		m, err := c.HubMaps(ctx, empty)
		if err != nil || m.NameToCode == nil || len(m.NameToCode)+len(m.CodeToName) != 0 {
			t.Fatalf("empty maps = %+v, %v", m, err)
		}
	}
	if loads.Load() != 4 {
		t.Fatalf("empty maps were not cached: loads %d", loads.Load())
	}
}

func TestPeriodLocksHint(t *testing.T) {
	c, rdb := newCache(t)
	ctx := context.Background()
	var loads atomic.Int32
	load := func(context.Context) ([]string, error) {
		loads.Add(1)
		return []string{"2026-09", "2026-08", "2026-09"}, nil
	}
	for range 2 {
		got, err := c.PeriodLocks(ctx, "party-1", load)
		if err != nil || !slices.Equal(got, []string{"2026-08", "2026-09"}) {
			t.Fatalf("PeriodLocks = %v, %v", got, err)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("loads = %d", loads.Load())
	}
	key := c.Keys().PeriodLocks("party-1")
	if typ := rdb.Type(ctx, key).Val(); typ != "set" {
		t.Fatalf("type %q, want set", typ)
	}
	if ttl := rdb.PTTL(ctx, key).Val(); ttl <= 0 || ttl > cache.PeriodLocksTTL {
		t.Fatalf("PTTL %v", ttl)
	}
	payload, _ := json.Marshal(map[string]string{"billingPartyId": "party-1"})
	if err := c.OnEvent(ctx, cache.Event{RoutingKey: "statement.status_changed", AggregateID: "st-1", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if rdb.Exists(ctx, key).Val() != 0 {
		t.Fatal("statement.status_changed left the hint")
	}
	if _, err := c.PeriodLocks(ctx, "party-2", func(context.Context) ([]string, error) { return []string{"2026-13"}, nil }); !errors.Is(err, cache.ErrBadPeriod) {
		t.Fatalf("bad period: %v", err)
	}
}

func TestSubtenantsDroppedOnTenantEvents(t *testing.T) {
	c, rdb := newCache(t)
	ctx := context.Background()
	ks := c.Keys()
	reach := map[string][]string{"own": {"carrier-a", "carrier-b"}, "dispatcher": {"carrier-c"}}
	for id, want := range reach {
		got, err := c.Subtenants(ctx, id, func(context.Context) ([]string, error) { return want, nil })
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("Subtenants(%s) = %v, %v", id, got, err)
		}
	}
	payload, _ := json.Marshal(map[string]string{"contractorTenantId": "own"})
	if err := c.OnEvent(ctx, cache.Event{RoutingKey: "tenant.updated", AggregateID: "carrier-a", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if rdb.Exists(ctx, ks.TenantSubtenants("own")).Val() != 0 || rdb.Exists(ctx, ks.TenantSubtenants("dispatcher")).Val() != 1 {
		t.Fatal("tenant.updated with a contractor id must drop exactly that reach list")
	}
	if err := c.OnEvent(ctx, cache.Event{RoutingKey: "tenant.created", AggregateID: "carrier-z"}); err != nil {
		t.Fatal(err)
	}
	if n := len(cachetest.Keys(t, rdb)); n != 0 {
		t.Fatalf("tenant.created without contractor ids left %d keys", n)
	}
}

func TestMirrorAckOnlyMovesForward(t *testing.T) {
	c, _ := newCache(t)
	ctx := context.Background()
	t1 := time.Date(2026, 10, 10, 3, 0, 0, 123456789, time.UTC)
	t2 := t1.Add(time.Second)
	if _, ok, err := c.MirrorAck(ctx, "trip_records", "doc1"); ok || err != nil {
		t.Fatalf("unknown doc: %v %v", ok, err)
	}
	if err := c.SetMirrorAck(ctx, "trip_records", "doc1", t2); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMirrorAck(ctx, "trip_records", "doc1", t1); err != nil { // late, older change
		t.Fatal(err)
	}
	got, ok, err := c.MirrorAck(ctx, "trip_records", "doc1")
	if err != nil || !ok || !got.Equal(t2.Truncate(time.Microsecond)) {
		t.Fatalf("MirrorAck = %v %v %v, want %v", got, ok, err, t2)
	}
	if ok, err := c.WaitMirrorAck(ctx, "trip_records", "doc1", t1, time.Second); !ok || err != nil {
		t.Fatalf("wait for an applied change: %v %v", ok, err)
	}
	t3 := t2.Add(time.Second)
	start := time.Now()
	if ok, err := c.WaitMirrorAck(ctx, "trip_records", "doc1", t3, 300*time.Millisecond); ok || err != nil {
		t.Fatalf("wait for a change not applied: %v %v", ok, err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 2*time.Second {
		t.Fatalf("timed out after %v, want about 300ms", d)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = c.SetMirrorAck(context.Background(), "trip_records", "doc1", t3)
	}()
	if ok, err := c.WaitMirrorAck(ctx, "trip_records", "doc1", t3, 3*time.Second); !ok || err != nil {
		t.Fatalf("wait until the mirror applies: %v %v", ok, err)
	}
}

func TestTicketsAreSingleUse(t *testing.T) {
	c, _ := newCache(t)
	ctx := context.Background()
	key := c.Keys().AuthPasswordChange("ticket-1")
	if ok, err := c.PutTicket(ctx, key, []byte(`{"userId":"u1"}`), 10*time.Minute); !ok || err != nil {
		t.Fatalf("put: %v %v", ok, err)
	}
	if ok, _ := c.PutTicket(ctx, key, []byte(`{"userId":"u2"}`), 10*time.Minute); ok {
		t.Fatal("a second put replaced the ticket")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if b, ok, err := c.TakeTicket(ctx, key); err == nil && ok && string(b) == `{"userId":"u1"}` {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d concurrent redemptions succeeded, want 1 (GETDEL)", wins.Load())
	}
}

// TestInvalidationReachesOtherReplicas: a delete on one replica drops the in-process copy on another
// through the rt:cache channel.
func TestInvalidationReachesOtherReplicas(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	a := cache.New(rdb, ks, cache.WithL1(100, time.Minute))
	otherClient := redis.NewClient(rdb.Options()) // a second replica's connection to the same database
	t.Cleanup(func() { _ = otherClient.Close() })
	cache.Guard(otherClient)
	b := cache.New(otherClient, ks, cache.WithL1(100, time.Minute))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, c := range []*cache.Cache{a, b} {
		go func() { _ = c.Run(ctx) }()
	}
	waitFor(t, func() bool {
		n := rdb.PubSubNumSub(ctx, ks.CacheChannel()).Val()[ks.CacheChannel()]
		return n == 2
	}, "both replicas subscribed to rt:cache")

	key := ks.Customer("c1")
	var loads atomic.Int32
	load := func(context.Context) (string, error) { return "v" + strconv.Itoa(int(loads.Add(1))), nil }
	if v, _ := cache.GetJSON(ctx, a, key, time.Minute, load); v != "v1" {
		t.Fatalf("a = %q", v)
	}
	if v, _ := cache.GetJSON(ctx, b, key, time.Minute, load); v != "v1" {
		t.Fatalf("b = %q", v)
	}
	// Remove the Redis copy behind b's back: b still answers from its in-process copy.
	rdb.Del(ctx, key)
	if v, _ := cache.GetJSON(ctx, b, key, time.Minute, load); v != "v1" || loads.Load() != 1 {
		t.Fatalf("b without the l1 copy: %q, loads %d", v, loads.Load())
	}
	if err := a.Invalidate(ctx, key); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		v, _ := cache.GetJSON(ctx, b, key, time.Minute, load)
		return v != "v1"
	}, "b dropped its copy after a's invalidation")
}

// TestRedisStoppedServesUIFromSourceAndMoneyPathsNeverDial stops a dedicated Redis: UI reads come
// from the loader (PostgreSQL in the services), invalidation fails without failing, and money paths
// get ErrMoneyPath at once instead of a dial timeout. The cached copy written before the stop held
// different values, so a stale Redis read would show.
func TestRedisStoppedServesUIFromSourceAndMoneyPathsNeverDial(t *testing.T) {
	srv := cachetest.StartDedicated(t)
	rdb, ks := srv.Client(t)
	c := cache.New(rdb, ks)
	ctx := context.Background()
	stale := func(context.Context) ([]string, error) { return []string{"2026-01"}, nil }
	if _, err := c.PeriodLocks(ctx, "p1", stale); err != nil {
		t.Fatal(err)
	}
	if err := srv.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	truth := func(context.Context) ([]string, error) { return []string{"2026-01", "2026-02"}, nil }
	got, err := c.PeriodLocks(ctx, "p1", truth)
	if err != nil || !slices.Equal(got, []string{"2026-01", "2026-02"}) {
		t.Fatalf("with Redis stopped: %v, %v", got, err)
	}
	if err := c.Invalidate(ctx, ks.HubsAll()); err == nil {
		t.Fatal("Invalidate reported success with Redis stopped")
	}
	start := time.Now()
	if _, err := c.PeriodLocks(cache.MoneyPath(ctx), "p1", truth); !errors.Is(err, cache.ErrMoneyPath) {
		t.Fatalf("money path: %v", err)
	}
	if err := rdb.Get(cache.MoneyPath(ctx), ks.PeriodLocks("p1")).Err(); !errors.Is(err, cache.ErrMoneyPath) {
		t.Fatalf("money path raw GET: %v", err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("money-path calls took %v: they dialled the stopped Redis", d)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting until %s", what)
}
