//go:build integration

// Acceptance tests of the Idempotency-Key middleware (issue T09, Appendix B §B.1.5, R53) on
// postgres:18-alpine (idempotency_keys of 0009_infra, as logitrack_app) and redis:7-alpine.
package idempotency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/dbq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/idempotency"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) {
	cache.RouteDriverLogs(zerolog.Nop())
	code := pgtest.Main(m)
	cachetest.TerminateShared()
	os.Exit(code)
}

// appPool is a migrated database seen through logitrack_app (DATABASE_URL), plus a superuser pool for
// assertions and clock manipulation.
func appPool(t *testing.T) (app, super *pgxpool.Pool) {
	t.Helper()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	super, err := db.Open(context.Background(), d.SuperURL(), "test-super")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(super.Close)
	return d.Pool(t, db.RoleApp), super
}

type server struct {
	app   *fiber.App
	calls atomic.Int32
	delay time.Duration
}

func newServer(t *testing.T, rdb redis.UniversalClient, ks cache.Keyspace, pool *pgxpool.Pool, hot time.Duration) *server {
	t.Helper()
	s := &server{}
	m, err := idempotency.New(idempotency.Options{
		Redis: rdb, Keys: ks, Store: idempotency.NewPGStore(pool), TTL: 168 * time.Hour, HotTTL: hot,
		Scope: func(c fiber.Ctx) string { return c.Get("X-Test-User") }, Log: zerolog.Nop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.app = fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	s.app.Use(httpx.RequestID())
	s.app.Post("/v1/mobile/incidents", m.Handler(), func(c fiber.Ctx) error {
		n := s.calls.Add(1)
		time.Sleep(s.delay)
		return httpx.JSON(c, 201, map[string]any{"id": n, "zeta": "ไทย", "alpha": []int{3, 1, 2}})
	})
	return s
}

type reply struct {
	status   int
	body     []byte
	replayed bool
	code     string
	reason   string
}

func (s *server) post(t *testing.T, user, key, body string) reply {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/mobile/incidents", strings.NewReader(body))
	req.Header.Set("X-Test-User", user)
	req.Header.Set(idempotency.Header, key)
	resp, err := s.app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	r := reply{status: resp.StatusCode, body: b, replayed: resp.Header.Get(idempotency.HeaderReplayed) == "true"}
	if resp.StatusCode >= 400 {
		var env struct {
			Error struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &env)
		r.code = env.Error.Code
		r.reason, _ = env.Error.Details["reason"].(string)
	}
	return r
}

// The outbox operation id the driver app sends as Idempotency-Key (and clientOpId).
const (
	user = "0192f1c2-0000-7000-8000-000000000001"
	opID = "0192f1c2-7d3e-7a10-8b2c-1234567890ab"
)

// TestReplaySameKeyAndBody: AC "replaying a request with the same key and body returns the identical
// status and body; same key with a different body -> 409 idempotency_conflict".
func TestReplaySameKeyAndBody(t *testing.T) {
	pool, super := appPool(t)
	rdb, ks := cachetest.NewClient(t)
	s := newServer(t, rdb, ks, pool, idempotency.HotTTL)
	ctx := context.Background()

	first := s.post(t, user, opID, `{"type":"accident","note":"ชนท้าย"}`)
	if first.status != 201 || first.replayed {
		t.Fatalf("first: %d %s", first.status, first.body)
	}
	again := s.post(t, user, opID, `{"type":"accident","note":"ชนท้าย"}`)
	if again.status != 201 || !bytes.Equal(again.body, first.body) || !again.replayed {
		t.Fatalf("replay: %d %s, want %d %s", again.status, again.body, first.status, first.body)
	}
	conflict := s.post(t, user, opID, `{"type":"breakdown"}`)
	if conflict.status != 409 || conflict.code != "idempotency_conflict" || conflict.reason != "fingerprint_mismatch" {
		t.Fatalf("different body: %d %s", conflict.status, conflict.body)
	}
	if s.calls.Load() != 1 {
		t.Fatalf("handler ran %d times", s.calls.Load())
	}

	// Redis hot copy for 24 h, durable row until IDEMPOTENCY_TTL.
	if ttl := rdb.PTTL(ctx, ks.IdemHTTP(user, opID)).Val(); ttl < 23*time.Hour || ttl > 24*time.Hour {
		t.Fatalf("idem:http TTL %v, want 24h", ttl)
	}
	if n := rdb.Exists(ctx, ks.IdemLock(user, opID)).Val(); n != 0 {
		t.Fatal("the in-flight lock outlived the request")
	}
	var status string
	var code int
	var life time.Duration
	if err := super.QueryRow(ctx, `SELECT status, response_code, expires_at - created_at FROM idempotency_keys
	                               WHERE scope = $1 AND key = $2`, user, opID).Scan(&status, &code, &life); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || code != 201 || life != 168*time.Hour {
		t.Fatalf("row: %s %d %v", status, code, life)
	}
}

// TestReplayFromPostgresAfterRedisExpiry: AC "after the Redis copy expires, a replay within
// IDEMPOTENCY_TTL is answered from idempotency_keys".
func TestReplayFromPostgresAfterRedisExpiry(t *testing.T) {
	pool, _ := appPool(t)
	rdb, ks := cachetest.NewClient(t)
	s := newServer(t, rdb, ks, pool, 300*time.Millisecond) // the 24 h hot copy, shortened
	ctx := context.Background()

	first := s.post(t, user, opID, `{"n":1}`)
	if first.status != 201 {
		t.Fatalf("first: %d %s", first.status, first.body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for rdb.Exists(ctx, ks.IdemHTTP(user, opID)).Val() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the Redis copy did not expire")
		}
		time.Sleep(50 * time.Millisecond)
	}
	again := s.post(t, user, opID, `{"n":1}`)
	if again.status != 201 || !bytes.Equal(again.body, first.body) || !again.replayed {
		t.Fatalf("replay from PostgreSQL: %d %s, want %s", again.status, again.body, first.body)
	}
	if s.calls.Load() != 1 {
		t.Fatalf("handler ran %d times", s.calls.Load())
	}
	if rdb.Exists(ctx, ks.IdemHTTP(user, opID)).Val() != 1 {
		t.Fatal("the PostgreSQL replay did not re-warm the hot copy")
	}
	if c := s.post(t, user, opID, `{"n":2}`); c.status != 409 || c.code != "idempotency_conflict" {
		t.Fatalf("conflict after expiry: %d %s", c.status, c.body)
	}
}

// TestConcurrentDuplicatesRunOnce: an offline outbox flushing the same request twice in parallel.
func TestConcurrentDuplicatesRunOnce(t *testing.T) {
	pool, _ := appPool(t)
	rdb, ks := cachetest.NewClient(t)
	s := newServer(t, rdb, ks, pool, idempotency.HotTTL)
	s.delay = 300 * time.Millisecond
	var wg sync.WaitGroup
	replies := make([]reply, 8)
	for i := range replies {
		wg.Go(func() { replies[i] = s.post(t, user, opID, `{"n":1}`) })
	}
	wg.Wait()
	created := 0
	for _, r := range replies {
		switch {
		case r.status == 201:
			created++
		case r.status == 409 && r.reason == "in_flight":
		default:
			t.Errorf("unexpected reply %d %s", r.status, r.body)
		}
	}
	if s.calls.Load() != 1 || created < 1 {
		t.Fatalf("handler ran %d times, %d answers 201", s.calls.Load(), created)
	}
	if r := s.post(t, user, opID, `{"n":1}`); r.status != 201 || !r.replayed {
		t.Fatalf("after the burst: %d %s", r.status, r.body)
	}
}

// TestRedisStoppedFallsBackToPostgres: with Redis down the durable claim alone still runs a request
// once and replays it.
func TestRedisStoppedFallsBackToPostgres(t *testing.T) {
	pool, _ := appPool(t)
	srv := cachetest.StartDedicated(t)
	rdb, ks := srv.Client(t)
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := newServer(t, rdb, ks, pool, idempotency.HotTTL)
	first := s.post(t, user, opID, `{"n":1}`)
	again := s.post(t, user, opID, `{"n":1}`)
	conflict := s.post(t, user, opID, `{"n":2}`)
	if first.status != 201 || again.status != 201 || !again.replayed || !bytes.Equal(first.body, again.body) {
		t.Fatalf("Redis stopped: %d %s / %d %s", first.status, first.body, again.status, again.body)
	}
	if conflict.status != 409 || s.calls.Load() != 1 {
		t.Fatalf("conflict %d, calls %d", conflict.status, s.calls.Load())
	}
}

// TestExpiredAndAbandonedRows: past IDEMPOTENCY_TTL the key is free again (client_op_id is then the
// defence, R63); an in_progress row older than the lock (a crashed holder) is taken over; prune
// deletes expired rows.
func TestExpiredAndAbandonedRows(t *testing.T) {
	pool, super := appPool(t)
	ctx := context.Background()
	store := idempotency.NewPGStore(pool)
	fp := idempotency.Fingerprint("POST", "/x", nil)

	at, ok, err := store.Claim(ctx, user, opID, fp, time.Hour, idempotency.LockTTL)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, ok, _ := store.Claim(ctx, user, opID, fp, time.Hour, idempotency.LockTTL); ok {
		t.Fatal("a live claim was taken twice")
	}
	// Abandoned: the holder died 31 s ago.
	if _, err := super.Exec(ctx, `UPDATE idempotency_keys SET created_at = created_at - interval '31 seconds'`); err != nil {
		t.Fatal(err)
	}
	at2, ok, err := store.Claim(ctx, user, opID, fp, time.Hour, idempotency.LockTTL)
	if err != nil || !ok || !at2.After(at) {
		t.Fatalf("takeover: %v %v %v", ok, err, at2)
	}
	// The first holder's late Complete and Release no longer touch the new claim (fencing).
	if err := store.Complete(ctx, user, opID, at, idempotency.NewRecord(fp, 201, "", nil)); err == nil {
		t.Fatal("a stale claim completed the new one")
	}
	if err := store.Release(ctx, user, opID, at); err != nil {
		t.Fatal(err)
	}
	if row, found, _ := store.Get(ctx, user, opID); !found || row.Completed {
		t.Fatalf("after the stale release: %+v %v", row, found)
	}
	if err := store.Complete(ctx, user, opID, at2, idempotency.NewRecord(fp, 201, "application/json", []byte(`{"a":1}`))); err != nil {
		t.Fatal(err)
	}
	row, found, err := store.Get(ctx, user, opID)
	if err != nil || !found || !row.Completed || row.Record.Status != 201 || string(row.Record.Bytes()) != `{"a":1}` {
		t.Fatalf("completed row: %+v %v %v", row, found, err)
	}
	// Expired: Get stops seeing it, Claim takes it, Prune deletes expired rows.
	if _, err := super.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.Get(ctx, user, opID); found {
		t.Fatal("an expired row is still served")
	}
	if _, err := super.Exec(ctx, `INSERT INTO idempotency_keys (scope, key, request_hash, status, created_at, expires_at)
	                               SELECT 'u-old', 'k' || g, 'h', 'in_progress', now() - interval '2 days', now() - interval '1 day'
	                               FROM generate_series(1, 25) g`); err != nil {
		t.Fatal(err)
	}
	n, err := store.Prune(ctx, 10)
	if err != nil || n != 26 {
		t.Fatalf("prune: %d %v, want 26", n, err)
	}
	if _, ok, _ := store.Claim(ctx, user, opID, fp, time.Hour, idempotency.LockTTL); !ok {
		t.Fatal("the key is not free after prune")
	}
}

// TestPruneSkipsARowBeingReclaimed: a Claim taking over an expired row while prune runs keeps its
// fresh claim (prune skips the locked row, and rechecks the expiry on the target row).
func TestPruneSkipsARowBeingReclaimed(t *testing.T) {
	pool, super := appPool(t)
	ctx := context.Background()
	store := idempotency.NewPGStore(pool)
	fp := idempotency.Fingerprint("POST", "/x", nil)
	if _, err := super.Exec(ctx, `INSERT INTO idempotency_keys (scope, key, request_hash, status, created_at, expires_at)
	                               VALUES ($1, $2, 'h', 'in_progress', now() - interval '2 days', now() - interval '1 day')`, user, opID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	at, err := dbq.New(tx).IdempotencyClaim(ctx, dbq.IdempotencyClaimParams{
		Scope: user, Key: opID, RequestHash: fp, TtlSeconds: time.Hour.Seconds(), LockSeconds: idempotency.LockTTL.Seconds(),
	})
	if err != nil {
		t.Fatal(err) // the takeover of the expired row, not committed yet
	}
	pruned := make(chan int64, 1)
	go func() {
		n, err := store.Prune(ctx, 10)
		if err != nil {
			t.Error(err)
		}
		pruned <- n
	}()
	select {
	case n := <-pruned:
		if n != 0 {
			t.Fatalf("prune deleted %d rows, want 0 (the only expired row is being re-claimed)", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prune waited on the row a claim holds")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if row, found, err := store.Get(ctx, user, opID); err != nil || !found || row.Completed {
		t.Fatalf("the fresh claim after prune: %+v %v %v", row, found, err)
	}
	if err := store.Complete(ctx, user, opID, at.Time, idempotency.NewRecord(fp, 201, "", []byte(`{}`))); err != nil {
		t.Fatalf("Complete of the fresh claim: %v", err)
	}
}

// TestBodyWithNULIsStoredDurably: jsonb rejects \u0000, so such a body travels as base64 and still
// replays byte for byte from PostgreSQL.
func TestBodyWithNULIsStoredDurably(t *testing.T) {
	pool, _ := appPool(t)
	ctx := context.Background()
	store := idempotency.NewPGStore(pool)
	fp := idempotency.Fingerprint("POST", "/x", nil)
	at, ok, err := store.Claim(ctx, user, opID, fp, time.Hour, idempotency.LockTTL)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	body := []byte("a\x00b ไทย")
	if err := store.Complete(ctx, user, opID, at, idempotency.NewRecord(fp, 200, "text/plain", body)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	row, found, err := store.Get(ctx, user, opID)
	if err != nil || !found || !row.Completed || !bytes.Equal(row.Record.Bytes(), body) {
		t.Fatalf("stored: %+v %v %v", row, found, err)
	}
}

// TestLateResponseIsNotStoredOverTheTakeover: a route that ignores its context and outlives the lease
// (1 s here, 30 s in production) loses its claim to a retry; when it finally ends, its response is
// stored nowhere, so Redis and PostgreSQL hold the same (the retry's) response.
func TestLateResponseIsNotStoredOverTheTakeover(t *testing.T) {
	pool, super := appPool(t)
	rdb, ks := cachetest.NewClient(t)
	ctx := context.Background()
	m, err := idempotency.New(idempotency.Options{
		Redis: rdb, Keys: ks, Store: idempotency.NewPGStore(pool), TTL: 168 * time.Hour, LockTTL: time.Second,
		Scope: func(fiber.Ctx) string { return user }, Log: zerolog.Nop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Post("/w", m.Handler(), func(c fiber.Ctx) error {
		n := calls.Add(1)
		if n == 1 {
			time.Sleep(2500 * time.Millisecond) // ignores c.Context(): the case the deadline cannot stop
		}
		return httpx.JSON(c, 201, map[string]int32{"run": n})
	})
	send := func() []byte {
		req := httptest.NewRequest("POST", "/w", strings.NewReader(`{}`))
		req.Header.Set(idempotency.Header, opID)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
		if err != nil {
			t.Error(err)
			return nil
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return b
	}
	late := make(chan []byte)
	go func() { late <- send() }()
	time.Sleep(1500 * time.Millisecond) // past the lease
	if b := send(); string(b) != `{"data":{"run":2}}` {
		t.Fatalf("takeover: %s", b)
	}
	if b := <-late; string(b) != `{"data":{"run":1}}` {
		t.Fatalf("late request: %s", b)
	}
	var hot idempotency.Record
	if err := json.Unmarshal([]byte(rdb.Get(ctx, ks.IdemHTTP(user, opID)).Val()), &hot); err != nil {
		t.Fatal(err)
	}
	var durable []byte
	if err := super.QueryRow(ctx, `SELECT response_body->>'body' FROM idempotency_keys WHERE scope = $1 AND key = $2`, user, opID).Scan(&durable); err != nil {
		t.Fatal(err)
	}
	if hot.Body != `{"data":{"run":2}}` || string(durable) != `{"data":{"run":2}}` {
		t.Fatalf("Redis holds %q, PostgreSQL %q: both must hold the takeover's response", hot.Body, durable)
	}
	if b := send(); string(b) != `{"data":{"run":2}}` || calls.Load() != 2 {
		t.Fatalf("replay: %s, calls %d", b, calls.Load())
	}
}
