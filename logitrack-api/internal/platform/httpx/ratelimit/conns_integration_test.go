//go:build integration

package ratelimit_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
)

// rl:sse_conns:{userId} (T12): the cap holds across concurrent acquirers, a renewal never counts twice,
// a released stream frees its slot, and the leases of a replica that stopped renewing (a crash) expire.
func TestSSEConnectionLeases(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	ctx := context.Background()
	l := ratelimit.NewConnLimiter(rdb, ks, 5, 800*time.Millisecond)

	var granted atomic.Int32
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			ok, err := l.Acquire(ctx, "u1", fmt.Sprint("s", i))
			if err != nil {
				t.Error(err)
			}
			if ok {
				granted.Add(1)
			}
		})
	}
	wg.Wait()
	if n := granted.Load(); n != 5 {
		t.Fatalf("%d of 20 concurrent streams got a lease, want 5", n)
	}
	if ok, _ := l.Acquire(ctx, "u2", "other"); !ok {
		t.Fatal("another user is capped by u1's streams")
	}
	if n, _ := rdb.ZCard(ctx, ks.SSEConnections("u1")).Result(); n != 5 {
		t.Fatalf("%d leases", n)
	}

	// Renewals of held leases never count again; a release frees a slot.
	held := []string{}
	members, _ := rdb.ZRange(ctx, ks.SSEConnections("u1"), 0, -1).Result()
	held = append(held, members...)
	for _, s := range held {
		if ok, err := l.Renew(ctx, "u1", s); !ok || err != nil {
			t.Fatalf("renew %s: %v %v", s, ok, err)
		}
	}
	if ok, _ := l.Acquire(ctx, "u1", "late"); ok {
		t.Fatal("a sixth lease after the renewals")
	}
	if err := l.Release(ctx, "u1", held[0]); err != nil {
		t.Fatal(err)
	}
	if ok, _ := l.Acquire(ctx, "u1", "late"); !ok {
		t.Fatal("a released slot was not reused")
	}

	// A replica that crashed stops renewing: its leases run out and free the slots.
	time.Sleep(time.Second)
	for i := range 5 {
		if ok, _ := l.Acquire(ctx, "u1", fmt.Sprint("after-crash-", i)); !ok {
			t.Fatalf("lease %d after the old ones expired was refused", i)
		}
	}
	if ttl, _ := rdb.PTTL(ctx, ks.SSEConnections("u1")).Result(); ttl <= 0 || ttl > 800*time.Millisecond {
		t.Fatalf("key TTL %v, want at most one lease", ttl)
	}
}
