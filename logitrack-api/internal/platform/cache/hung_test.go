package cache_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
)

// TestHungRedisNeverStallsAnything: against a Redis that accepts connections but never answers,
// every read falls back to its loader within the 300 ms call bound, the post-commit invalidation
// gives up within its 1 s bound, and a call without a deadline is still bounded by the client's
// ReadTimeout (cache.Open). Without context deadlines on the socket each of these took 5 s per try.
func TestHungRedisNeverStallsAnything(t *testing.T) {
	rdb, ks, err := cache.Open(cache.Options{URL: cachetest.Hung(t), AppEnv: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	c := cache.New(rdb, ks)
	ctx := context.Background()
	const readBound = 300*time.Millisecond + 400*time.Millisecond // call bound + scheduling slack
	within := func(name string, bound time.Duration, fn func() error, wantErr bool) {
		t.Helper()
		start := time.Now()
		err := fn()
		if took := time.Since(start); took > bound || (err != nil) != wantErr {
			t.Errorf("%s: %v after %v, want error=%v within %v", name, err, took, wantErr, bound)
		}
	}
	within("GetJSON", readBound, func() error {
		v, err := cache.GetJSON(ctx, c, ks.Settings("mobile_app"), time.Minute, func(context.Context) (string, error) { return "from-pg", nil })
		if err == nil && v != "from-pg" {
			t.Errorf("GetJSON = %q, want the loader's value", v)
		}
		return err
	}, false)
	within("HubMaps", readBound, func() error {
		_, err := c.HubMaps(ctx, func(context.Context) (cache.HubMaps, error) { return cache.HubMaps{}, nil })
		return err
	}, false)
	within("PeriodLocks", readBound, func() error {
		got, err := c.PeriodLocks(ctx, "p1", func(context.Context) ([]string, error) { return []string{"2026-09"}, nil })
		if err == nil && !slices.Equal(got, []string{"2026-09"}) {
			t.Errorf("PeriodLocks = %v", got)
		}
		return err
	}, false)
	within("MirrorAck", readBound, func() error { _, _, err := c.MirrorAck(ctx, "trips", "d1"); return err }, true)
	within("TakeTicket", readBound, func() error { _, _, err := c.TakeTicket(ctx, ks.AuthPasswordChange("t")); return err }, true)
	within("Invalidate", time.Second+400*time.Millisecond, func() error { return c.Invalidate(ctx, ks.HubsAll()) }, true)
	payload, _ := json.Marshal(map[string]string{"statementId": "s1", "status": "sent"})
	within("OnEvent sweep", time.Second+400*time.Millisecond, func() error {
		return c.OnEvent(ctx, cache.Event{RoutingKey: "statement.status_changed", Payload: payload})
	}, true)
	// No deadline at all: ReadTimeout (1 s) per try, one retry.
	within("raw GET without a deadline", 3*time.Second, func() error { return rdb.Get(ctx, ks.HubsAll()).Err() }, true)
}
