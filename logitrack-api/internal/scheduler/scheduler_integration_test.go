//go:build integration

// Acceptance tests of the scheduler (issue T10) on postgres:18-alpine and Redis: with two replicas
// every cron slot fires once (advisory lock, failover, and the lock:cron guard on its own), command
// fires enqueue a job and its lt.jobs outbox row, and the housekeeping jobs delete what §7.4 says.
package scheduler_test

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/dbq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scheduler"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) {
	code := pgtest.Main(m)
	asynctest.Terminate()
	os.Exit(code)
}

const prefix = "lt:local:"

// localKeyspace is the keyspace of APP_ENV=local (prefix lt:local:), as cache.Open builds it.
func localKeyspace(t testing.TB) cache.Keyspace {
	t.Helper()
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func migrated(t *testing.T) *pgtest.Database {
	t.Helper()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d
}

func scalar[T any](t *testing.T, pool *pgxpool.Pool, sql string, args ...any) T {
	t.Helper()
	var v T
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

// replica is one scheduler process running only its cron loop, with a tick job that counts its fires.
type replica struct {
	sched  *scheduler.Scheduler
	fires  atomic.Int32
	cancel context.CancelFunc
	done   chan struct{}
}

func startReplica(t *testing.T, pool *pgxpool.Pool, rdb *redis.Client, withLeader bool) *replica {
	t.Helper()
	r := &replica{done: make(chan struct{})}
	table := []scheduler.Job{{Name: "test.tick", Spec: "@every 1s", Kind: scheduler.Local,
		Run: func(context.Context) (any, error) { r.fires.Add(1); return nil, nil }}}
	cron, err := scheduler.NewCron(pool, jobs.NewRedisLocker(rdb, localKeyspace(t)), table, zerolog.Nop(), prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	if withLeader {
		r.sched = &scheduler.Scheduler{
			Leader: scheduler.NewLeader(pool, zerolog.Nop(), nil).WithIntervals(200*time.Millisecond, 200*time.Millisecond),
			Cron:   cron, Log: zerolog.Nop(),
		}
		go func() { defer close(r.done); r.sched.Run(ctx) }()
	} else {
		go func() { defer close(r.done); cron.Run(ctx) }()
	}
	t.Cleanup(r.stop)
	return r
}

func (r *replica) stop() {
	r.cancel()
	<-r.done
}

// slots returns (fires, distinct scheduledFor values) of the test.tick jobs.
func slots(t *testing.T, pool *pgxpool.Pool) (int, int) {
	t.Helper()
	var n, distinct int
	if err := pool.QueryRow(context.Background(), `SELECT count(*), count(DISTINCT params->>'scheduledFor')
		FROM jobs WHERE type = 'test.tick'`).Scan(&n, &distinct); err != nil {
		t.Fatal(err)
	}
	return n, distinct
}

// AC: two scheduler replicas, each cron fires once; the standby takes over when the leader goes.
func TestTwoReplicasFireEachSlotOnce(t *testing.T) {
	d := migrated(t)
	pool := d.Pool(t, db.RoleApp)
	rdb := asynctest.SharedRedis(t).Client(t)
	a := startReplica(t, pool, rdb, true)
	b := startReplica(t, pool, rdb, true)
	time.Sleep(3500 * time.Millisecond)
	leader, standby := a, b
	if b.fires.Load() > 0 {
		leader, standby = b, a
	}
	if leader.fires.Load() == 0 || standby.fires.Load() != 0 {
		t.Fatalf("before failover: fires %d and %d, want only the leader firing", a.fires.Load(), b.fires.Load())
	}
	leader.stop() // its lock connection closes, PostgreSQL releases the advisory lock
	before := standby.fires.Load()
	time.Sleep(3500 * time.Millisecond)
	if standby.fires.Load() <= before {
		t.Fatal("the standby never took over")
	}
	standby.stop()
	n, distinct := slots(t, pool)
	if n < 5 || n != distinct {
		t.Fatalf("%d fires over %d distinct slots: every slot must fire exactly once", n, distinct)
	}
	succeeded := scalar[int](t, pool, `SELECT count(*) FROM jobs WHERE type = 'test.tick' AND status = 'succeeded'`)
	if got := int(a.fires.Load() + b.fires.Load()); got != succeeded {
		t.Fatalf("replicas ran %d times, jobs has %d succeeded rows", got, succeeded)
	}
	// A slot has a jobs row only if its replica took lock:cron (Redis is up here), and that row always
	// ends succeeded or failed: failed only when the replica took the lock and then stopped before
	// the run, with the shutdown cause. A shutdown before the lock is taken leaves no row at all.
	if bad := scalar[int](t, pool, `SELECT count(*) FROM jobs WHERE type = 'test.tick' AND (owner_user_id IS NOT NULL OR
		(status <> 'succeeded' AND NOT (status = 'failed' AND error LIKE 'cron: shutdown before the run started%')))`); bad != 0 {
		t.Fatalf("%d scheduled runs are neither succeeded nor shutdown-failed rows without an owner", bad)
	}
}

// lock:cron:{job}:{scheduledFor} alone stops a double fire, e.g. an old leader that has not yet
// noticed its demotion running beside the new one.
func TestCronLockStopsADoubleFire(t *testing.T) {
	d := migrated(t)
	pool := d.Pool(t, db.RoleApp)
	rdb := asynctest.SharedRedis(t).Client(t)
	a := startReplica(t, pool, rdb, false)
	b := startReplica(t, pool, rdb, false)
	time.Sleep(3200 * time.Millisecond)
	a.stop()
	b.stop()
	n, distinct := slots(t, pool)
	// A slot has a jobs row only if one replica took its lock:cron key (so one row and one key per
	// slot), and that row always ends succeeded or failed: a replica that took the lock and then
	// stopped before the run fails the row without running; one that stopped before taking the lock
	// writes nothing. Every succeeded row is exactly one fire, and no row is left running.
	succeeded := scalar[int](t, pool, `SELECT count(*) FROM jobs WHERE type = 'test.tick' AND status = 'succeeded'`)
	running := scalar[int](t, pool, `SELECT count(*) FROM jobs WHERE type = 'test.tick' AND status NOT IN ('succeeded', 'failed')`)
	if n < 2 || n != distinct || int(a.fires.Load()+b.fires.Load()) != succeeded || running != 0 {
		t.Fatalf("%d rows (%d succeeded, %d unfinished) over %d slots, fires %d + %d: want one row per slot, one fire per succeeded row",
			n, succeeded, running, distinct, a.fires.Load(), b.fires.Load())
	}
	keys, err := rdb.Keys(context.Background(), prefix+"lock:cron:test.tick:*").Result()
	if err != nil || len(keys) != n {
		t.Fatalf("lock:cron keys %v (%v), want one per slot", keys, err)
	}
}

// A shutdown that arrives before the slot is claimed writes nothing: go-redis refuses SET NX on a
// cancelled context, so the replica never took lock:cron, and a jobs row there would record a slot
// it does not own (regression of FLAKE2: it used to write a failed row with no lock behind it).
func TestCronFireAfterShutdownWritesNothing(t *testing.T) {
	d := migrated(t)
	pool := d.Pool(t, db.RoleApp)
	rdb := asynctest.SharedRedis(t).Client(t)
	var ran atomic.Bool
	job := scheduler.Job{Name: "test.cancelled", Spec: "@every 1s", Kind: scheduler.Local,
		Run: func(context.Context) (any, error) { ran.Store(true); return nil, nil }}
	cron, err := scheduler.NewCron(pool, jobs.NewRedisLocker(rdb, localKeyspace(t)), []scheduler.Job{job}, zerolog.Nop(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cron.Fire(ctx, job, time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC))
	rows := scalar[int](t, pool, `SELECT count(*) FROM jobs WHERE type = 'test.cancelled'`)
	keys, err := rdb.Keys(context.Background(), prefix+"lock:cron:test.cancelled:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if rows != 0 || len(keys) != 0 || ran.Load() {
		t.Fatalf("fire on a cancelled context: %d jobs rows, lock:cron keys %v, ran %v; want nothing", rows, keys, ran.Load())
	}
}

// A command cron inserts the jobs row and the lt.jobs command in one transaction.
func TestCommandCronEnqueuesJobAndOutboxRow(t *testing.T) {
	d := migrated(t)
	pool := d.Pool(t, db.RoleApp)
	rdb := asynctest.SharedRedis(t).Client(t)
	cron, err := scheduler.NewCron(pool, jobs.NewRedisLocker(rdb, localKeyspace(t)),
		[]scheduler.Job{{Name: "storage.gc", Spec: "0 * * * *", Kind: scheduler.Command}}, zerolog.Nop(), nil)
	if err != nil {
		t.Fatal(err)
	}
	slot := time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC)
	cron.Fire(context.Background(), scheduler.Job{Name: "storage.gc", Spec: "0 * * * *", Kind: scheduler.Command}, slot)
	cron.Fire(context.Background(), scheduler.Job{Name: "storage.gc", Spec: "0 * * * *", Kind: scheduler.Command}, slot)
	if n := scalar[int](t, pool, `SELECT count(*) FROM jobs WHERE type = 'storage.gc' AND status = 'queued'
		AND owner_user_id IS NULL AND params->>'scheduledFor' = '2026-10-10T03:00:00Z'`); n != 1 {
		t.Fatalf("%d queued storage.gc jobs for the slot, want 1", n)
	}
	if n := scalar[int](t, pool, `SELECT count(*) FROM outbox_events o JOIN jobs j ON o.aggregate_id = j.id::text
		WHERE o.exchange = 'lt.jobs' AND o.routing_key = 'job.storage.gc' AND o.payload->>'jobId' = j.id::text`); n != 1 {
		t.Fatalf("%d lt.jobs commands, want 1", n)
	}
}

func TestTokenCleanup(t *testing.T) {
	d := migrated(t)
	etl := d.Pool(t, db.RoleETL)
	app := d.Pool(t, db.RoleApp)
	ctx := context.Background()
	now := time.Now()
	day := 24 * time.Hour
	user := scalar[string](t, etl, `INSERT INTO users (email) VALUES ('cleanup@logitrack.test') RETURNING id::text`)
	session := func(revokedAgo, absAgo time.Duration) string {
		var revoked any
		reason := any(nil)
		if revokedAgo > 0 {
			revoked, reason = now.Add(-revokedAgo), "logout"
		}
		return scalar[string](t, etl, `INSERT INTO sessions (user_id, platform, amr, absolute_expires_at, revoked_at, revoked_reason)
			VALUES ($1, 'web', 'pwd', $2, $3, $4) RETURNING id::text`, user, now.Add(-absAgo), revoked, reason)
	}
	oldRevoked := session(31*day, -day) // revoked 31 days ago: deleted
	oldExpired := session(0, 31*day)    // absolute expiry 31 days ago: deleted
	recent := session(2*day, -day)      // revoked 2 days ago: kept
	live := session(0, -30*day)         // live: kept
	token := func(sess string, n byte, expiresAgo time.Duration, replacedBy any) string {
		hash := make([]byte, 32)
		hash[0] = n
		var rotated any
		if replacedBy != nil {
			rotated = now
		}
		return scalar[string](t, etl, `INSERT INTO refresh_tokens (session_id, family_id, token_hash, expires_at, rotated_at, replaced_by)
			VALUES ($1, '0199c000-0000-7000-8000-0000000000f1', $2, $3, $4, $5) RETURNING id::text`,
			sess, hash, now.Add(-expiresAgo), rotated, replacedBy)
	}
	newest := token(live, 1, 2*day, nil)
	_ = token(live, 2, 3*day, newest)   // rotated into newest; both expired over a day ago: deleted together
	fresh := token(live, 3, -day, nil)  // not expired: kept
	_ = token(oldRevoked, 4, -day, nil) // goes with its session
	resetToken := func(n byte, used bool, expiresIn time.Duration) {
		hash := make([]byte, 32)
		hash[0] = n
		var usedAt any
		if used {
			usedAt = now
		}
		if _, err := etl.Exec(ctx, `INSERT INTO password_reset_tokens (user_id, token_hash, expires_at, used_at)
			VALUES ($1, $2, $3, $4)`, user, hash, now.Add(expiresIn), usedAt); err != nil {
			t.Fatal(err)
		}
	}
	resetToken(1, true, time.Hour)   // used: deleted
	resetToken(2, false, -time.Hour) // expired: deleted
	resetToken(3, false, time.Hour)  // live: kept

	got, err := scheduler.TokenCleanup(ctx, app, now)
	if err != nil {
		t.Fatal(err)
	}
	if got["sessions"] != 2 || got["refreshTokens"] != 2 || got["passwordResetTokens"] != 2 {
		t.Fatalf("deleted %v", got)
	}
	left := scalar[[]string](t, etl, `SELECT array_agg(id::text ORDER BY id) FROM sessions`)
	if len(left) != 2 || !contains(left, recent) || !contains(left, live) || contains(left, oldRevoked) || contains(left, oldExpired) {
		t.Fatalf("sessions left %v", left)
	}
	if ids := scalar[[]string](t, etl, `SELECT array_agg(id::text) FROM refresh_tokens`); len(ids) != 1 || ids[0] != fresh {
		t.Fatalf("refresh tokens left %v, want only the fresh one", ids)
	}
	if n := scalar[int](t, etl, `SELECT count(*) FROM password_reset_tokens`); n != 1 {
		t.Fatalf("%d reset tokens left, want 1", n)
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// The 04:00 housekeeping: outbox (7 d, published only), consumer_inbox and jobs (30 d), expired
// idempotency keys (never one a request is re-claiming meanwhile), and rtlog.trim.
func TestHousekeeping(t *testing.T) {
	d := migrated(t)
	app := d.Pool(t, db.RoleApp)
	ctx := context.Background()
	rdb := asynctest.SharedRedis(t).Client(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := app.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`INSERT INTO outbox_events (routing_key, aggregate_type, aggregate_id, event_type, payload, created_at, published_at)
		VALUES ('a.old', 'a', '1', 'a.old', '{}', now() - interval '9 days', now() - interval '8 days'),
		       ('a.new', 'a', '2', 'a.new', '{}', now() - interval '1 day', now() - interval '1 day'),
		       ('a.stuck', 'a', '3', 'a.stuck', '{}', now() - interval '9 days', NULL)`)
	exec(`INSERT INTO consumer_inbox (consumer, message_id, processed_at) VALUES
		('q', '1', now() - interval '31 days'), ('q', '2', now() - interval '1 day')`)
	exec(`INSERT INTO jobs (type, status, created_at) VALUES ('old.job', 'queued', now() - interval '31 days'),
		('new.job', 'queued', now())`)
	exec(`INSERT INTO idempotency_keys (scope, key, request_hash, status, created_at, expires_at) VALUES
		('u', 'old', 'h', 'in_progress', now() - interval '8 days', now() - interval '1 day'),
		('u', 'new', 'h', 'in_progress', now(), now() + interval '1 day')`)
	rt := realtime.NewWriter(rdb, localKeyspace(t), 10, time.Hour)
	for i := 1; i <= 30; i++ {
		rdb.XAdd(ctx, &redis.XAddArgs{Stream: prefix + "rtlog:global", ID: "", Values: []string{"type", "x"}})
	}
	table := map[string]scheduler.Job{}
	for _, j := range scheduler.Table(app, rt) {
		table[j.Name] = j
	}
	for _, name := range []string{"outbox.prune", "inbox.prune", "jobs.prune", "idempotency.prune", "rtlog.trim"} {
		j, ok := table[name]
		if !ok || j.Kind != scheduler.Local || j.Spec != "0 4 * * *" {
			t.Fatalf("%s is not a 04:00 local job: %+v", name, j)
		}
		if _, err := j.Run(ctx); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if keys := scalar[[]string](t, app, `SELECT array_agg(routing_key ORDER BY id) FROM outbox_events`); len(keys) != 2 ||
		keys[0] != "a.new" || keys[1] != "a.stuck" {
		t.Fatalf("outbox left %v, want the recent and the unpublished rows", keys)
	}
	if n := scalar[int](t, app, `SELECT count(*) FROM consumer_inbox`); n != 1 {
		t.Fatalf("consumer_inbox left %d", n)
	}
	if n := scalar[string](t, app, `SELECT string_agg(type, ',') FROM jobs`); n != "new.job" {
		t.Fatalf("jobs left %s", n)
	}
	if n := scalar[string](t, app, `SELECT string_agg(key, ',') FROM idempotency_keys`); n != "new" {
		t.Fatalf("idempotency keys left %s", n)
	}
	if n, _ := rdb.XLen(ctx, prefix+"rtlog:global").Result(); n != 10 {
		t.Fatalf("rtlog:global still has %d entries after the trim", n)
	}
	if ttl, _ := rdb.PTTL(ctx, prefix+"rtlog:global").Result(); ttl <= 0 {
		t.Fatalf("rtlog:global has no TTL after the trim (%v)", ttl)
	}
	if _, ok := table["auth.token-cleanup"]; !ok || table["auth.token-cleanup"].Spec != "*/10 * * * *" {
		t.Fatal("auth.token-cleanup is not every 10 minutes")
	}

	// idempotency.prune runs T09's IdempotencyPrune: an expired key that a request is re-claiming
	// (IdempotencyClaim takes over exactly the expired rows the prune targets) is skipped, not deleted
	// once the claim commits; a delete there would let a duplicate with that key run again. Other
	// expired keys still go.
	exec(`INSERT INTO idempotency_keys (scope, key, request_hash, status, response_code, created_at, expires_at) VALUES
		('u', 'reclaimed', 'h', 'completed', 201, now() - interval '8 days', now() - interval '1 day'),
		('u', 'expired', 'h', 'completed', 201, now() - interval '8 days', now() - interval '1 day')`)
	claim, err := app.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = claim.Rollback(ctx) }()
	claimedAt, err := dbq.New(claim).IdempotencyClaim(ctx, dbq.IdempotencyClaimParams{
		Scope: "u", Key: "reclaimed", RequestHash: "h2", TtlSeconds: 86400, LockSeconds: 30})
	if err != nil || !claimedAt.Valid {
		t.Fatalf("re-claim of the expired key: %v", err)
	}
	pruned := make(chan error, 1)
	go func() {
		_, err := table["idempotency.prune"].Run(ctx)
		pruned <- err
	}()
	select {
	case err := <-pruned:
		if err != nil {
			t.Fatalf("idempotency.prune: %v", err)
		}
	case <-time.After(5 * time.Second):
		// The prune waits on the claim's row lock: commit, and see what it deletes then.
		if err := claim.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		<-pruned
		t.Fatal("idempotency.prune blocked on a key a live request is claiming instead of skipping it")
	}
	if err := claim.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n := scalar[string](t, app, `SELECT string_agg(key || ':' || request_hash, ',' ORDER BY key) FROM idempotency_keys`); n != "new:h,reclaimed:h2" {
		t.Fatalf("idempotency keys left %s, want the live key and the re-claimed one", n)
	}
}
