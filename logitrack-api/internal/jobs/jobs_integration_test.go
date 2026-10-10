//go:build integration

// Tests of the jobs endpoints and the dead-letter replay (issue T10) on postgres:18-alpine, Redis and
// RabbitMQ: GET /v1/jobs (own jobs, platform sees all, keyset paging), GET /v1/jobs/{id}, and
// POST /v1/admin/queues/{queue}/replay -> queue.replay job + queue_replayed audit row -> the
// scheduler's replayer moves {queue}.dead back to {queue} with a fresh retry budget.
package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) {
	code := pgtest.Main(m)
	asynctest.Terminate()
	os.Exit(code)
}

// localKeyspace is the keyspace of APP_ENV=local (prefix lt:local:), as cache.Open builds it.
func localKeyspace(t testing.TB) cache.Keyspace {
	t.Helper()
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

type fixture struct {
	app   *fiber.App
	pool  *pgxpool.Pool
	etl   *pgxpool.Pool
	locks *jobs.RedisLocker
}

type callerKey struct{}

// testAuth stands in for auth.RequireAuth (T05): X-Test-User is the principal, X-Test-Platform its
// platform role.
func testAuth(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Get("X-Test-User"))
	if err != nil {
		return httpx.ErrUnauthenticated()
	}
	role := c.Get("X-Test-Platform")
	c.Locals(callerKey{}, jobs.Caller{UserID: id, PlatformAdmin: role == "platform_admin", PlatformSupport: role == "support"})
	return c.Next()
}

func setup(t *testing.T) *fixture {
	t.Helper()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := &fixture{pool: d.Pool(t, db.RoleApp), etl: d.Pool(t, db.RoleETL)}
	f.locks = jobs.NewRedisLocker(asynctest.SharedRedis(t).Client(t), localKeyspace(t))
	svc := jobs.NewService(f.pool, f.locks)
	f.app = fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	f.app.Use(httpx.RequestID())
	ingress.Mount(f.app, ingress.Internal, svc.Groups(jobs.HTTPOptions{
		Auth: testAuth,
		Caller: func(c fiber.Ctx) (jobs.Caller, bool) {
			cl, ok := c.Locals(callerKey{}).(jobs.Caller)
			return cl, ok
		},
	}), nil)
	return f
}

func (f *fixture) user(t *testing.T, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.etl.QueryRow(context.Background(), `INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fixture) job(t *testing.T, typ string, owner *uuid.UUID) jobs.Job {
	t.Helper()
	var j jobs.Job
	if err := db.WithSystem(context.Background(), f.pool, nil, func(tx pgx.Tx) error {
		var err error
		j, err = jobs.Enqueue(context.Background(), tx, jobs.NewInput{Type: typ, OwnerUserID: owner, Params: map[string]any{"n": 1}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // distinct created_at for a stable order
	return j
}

type response struct {
	status int
	body   map[string]any
}

func (f *fixture) do(t *testing.T, method, path string, user *uuid.UUID, platform string) response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if user != nil {
		req.Header.Set("X-Test-User", user.String())
	}
	if platform != "" {
		req.Header.Set("X-Test-Platform", platform)
	}
	resp, err := f.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	return response{resp.StatusCode, body}
}

func code(r response) string {
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func ids(r response) []string {
	var out []string
	for _, it := range r.body["data"].([]any) {
		out = append(out, it.(map[string]any)["id"].(string))
	}
	return out
}

func TestListAndGetJobs(t *testing.T) {
	f := setup(t)
	alice, bob := f.user(t, "alice@logitrack.test"), f.user(t, "bob@logitrack.test")
	a1 := f.job(t, "payroll.run", &alice)
	a2 := f.job(t, "billing.backfill-trips", &alice)
	a3 := f.job(t, "payroll.run", &alice)
	b1 := f.job(t, "payroll.run", &bob)
	cron := f.job(t, "storage.gc", nil)

	if r := f.do(t, "GET", "/v1/jobs", nil, ""); r.status != 401 || code(r) != "unauthenticated" {
		t.Fatalf("no principal: %d %v", r.status, r.body)
	}
	r := f.do(t, "GET", "/v1/jobs", &alice, "")
	if got := ids(r); r.status != 200 || strings.Join(got, ",") != strings.Join([]string{a3.ID.String(), a2.ID.String(), a1.ID.String()}, ",") {
		t.Fatalf("alice's list %d %v", r.status, got)
	}
	if _, has := r.body["nextCursor"]; has {
		t.Fatal("a complete page carries nextCursor")
	}
	page1 := f.do(t, "GET", "/v1/jobs?limit=2", &alice, "")
	cursor, _ := page1.body["nextCursor"].(string)
	if got := ids(page1); len(got) != 2 || cursor == "" {
		t.Fatalf("page 1 %v cursor %q", got, cursor)
	}
	page2 := f.do(t, "GET", "/v1/jobs?limit=2&cursor="+cursor, &alice, "")
	if got := ids(page2); len(got) != 1 || got[0] != a1.ID.String() || page2.body["nextCursor"] != nil {
		t.Fatalf("page 2 %v %v", got, page2.body["nextCursor"])
	}
	if got := ids(f.do(t, "GET", "/v1/jobs?type=payroll.run", &alice, "")); len(got) != 2 {
		t.Fatalf("type filter %v", got)
	}
	if got := ids(f.do(t, "GET", "/v1/jobs", &bob, "support")); len(got) != 5 {
		t.Fatalf("a platform principal sees %d jobs, want 5", len(got))
	}
	for _, bad := range []struct {
		path, code string
		status     int
	}{
		{"/v1/jobs?bogus=1", "bad_request", 400},
		{"/v1/jobs?limit=0", "invalid_argument", 422},
		{"/v1/jobs?limit=501", "invalid_argument", 422},
		{"/v1/jobs?cursor=nope", "invalid_argument", 422},
		{"/v1/jobs?type=Bad", "invalid_argument", 422},
	} {
		if r := f.do(t, "GET", bad.path, &alice, ""); r.status != bad.status || code(r) != bad.code {
			t.Fatalf("%s: %d %v", bad.path, r.status, r.body)
		}
	}

	if r := f.do(t, "GET", "/v1/jobs/"+b1.ID.String(), &alice, ""); r.status != 404 || code(r) != "not_found" {
		t.Fatalf("another user's job: %d", r.status)
	}
	if r := f.do(t, "GET", "/v1/jobs/"+cron.ID.String(), &alice, ""); r.status != 404 {
		t.Fatalf("a scheduler job for a tenant user: %d", r.status)
	}
	r = f.do(t, "GET", "/v1/jobs/"+a2.ID.String(), &alice, "")
	data, _ := r.body["data"].(map[string]any)
	if r.status != 200 || data["type"] != "billing.backfill-trips" || data["status"] != "queued" ||
		data["params"].(map[string]any)["n"] != float64(1) || data["createdAt"] == nil {
		t.Fatalf("GET own job: %d %v", r.status, r.body)
	}
	for _, k := range []string{"id", "type", "status", "params", "progress", "createdAt", "startedAt", "finishedAt"} {
		if _, ok := data[k]; !ok {
			t.Fatalf("job body lacks %s: %v", k, data)
		}
	}
	if r := f.do(t, "GET", "/v1/jobs/not-a-uuid", &alice, ""); r.status != 404 {
		t.Fatalf("malformed id: %d", r.status)
	}
	if r := f.do(t, "GET", "/v1/jobs/"+cron.ID.String(), &bob, "platform_admin"); r.status != 200 {
		t.Fatalf("platform sees scheduler jobs: %d", r.status)
	}
}

// Job lifecycle writes job.updated (full body) on user:{owner}.
func TestLifecycleEmitsJobUpdated(t *testing.T) {
	f := setup(t)
	alice := f.user(t, "alice@logitrack.test")
	j := f.job(t, "payroll.run", &alice)
	ctx := context.Background()
	if err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		if _, err := jobs.Start(ctx, tx, j.ID); err != nil {
			return err
		}
		if _, err := jobs.SetProgress(ctx, tx, j.ID, jobs.Progress{Done: 50, Total: 120}); err != nil {
			return err
		}
		_, err := jobs.Succeed(ctx, tx, j.ID, map[string]any{"rows": 120})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	var last string
	if err := f.pool.QueryRow(ctx, `SELECT count(*), max(payload->>'status') FILTER (WHERE id = (SELECT max(id) FROM outbox_events))
		FROM outbox_events WHERE routing_key = 'job.updated' AND realtime_topics = ARRAY['user:' || $1::text]`, alice).Scan(&n, &last); err != nil {
		t.Fatal(err)
	}
	if n != 3 || last != "succeeded" {
		t.Fatalf("%d job.updated rows, last status %q; want 3 ending in succeeded", n, last)
	}
	if _, err := jobs.Start(ctx, mustTx(t, f.pool), j.ID); err != jobs.ErrNotActive {
		t.Fatalf("starting a finished job: %v", err)
	}
}

func mustTx(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func TestQueueReplay(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	admin, staff := f.user(t, "admin@logitrack.test"), f.user(t, "staff@logitrack.test")

	if r := f.do(t, "POST", "/v1/admin/queues/billing.compute/replay", &staff, "support"); r.status != 403 || code(r) != "permission_denied" {
		t.Fatalf("support may not replay: %d %v", r.status, r.body)
	}
	if r := f.do(t, "POST", "/v1/admin/queues/no.such/replay", &admin, "platform_admin"); r.status != 404 {
		t.Fatalf("unknown queue: %d", r.status)
	}
	r := f.do(t, "POST", "/v1/admin/queues/billing.compute/replay", &admin, "platform_admin")
	jobID, _ := r.body["data"].(map[string]any)["jobId"].(string)
	if r.status != http.StatusAccepted || jobID == "" {
		t.Fatalf("replay: %d %v", r.status, r.body)
	}
	if again := f.do(t, "POST", "/v1/admin/queues/billing.compute/replay", &admin, "platform_admin"); again.status != 409 ||
		code(again) != "already_exists" || again.body["error"].(map[string]any)["details"].(map[string]any)["jobId"] != jobID {
		t.Fatalf("second replay while the first is queued: %d %v", again.status, again.body)
	}
	var audits int
	if err := f.etl.QueryRow(ctx, `SELECT count(*) FROM security_events WHERE event_type = 'queue_replayed'
		AND actor_user_id = $1 AND details->>'queue' = 'billing.compute' AND details->>'jobId' = $2`, admin, jobID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("%d queue_replayed rows, want 1 in the same transaction", audits)
	}

	// Three dead letters, as the sixth failure leaves them.
	url, _ := asynctest.SharedRabbit(t).VHost(t)
	conn, err := mq.Dial(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := mq.DeclareTopology(conn, mq.Default); err != nil {
		t.Fatal(err)
	}
	pub, err := mq.NewPublisher(conn)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2", "3"} {
		if err := pub.Publish(ctx, mq.ExchangeDLX, "billing.compute", amqp.Publishing{MessageId: id, Body: []byte(`{}`),
			Headers: amqp.Table{mq.HeaderAttempts: int32(5), mq.HeaderOriginalExchange: mq.ExchangeEvents,
				mq.HeaderOriginalRoutingKey: "trip.delivered"}}); err != nil {
			t.Fatal(err)
		}
	}
	rp := jobs.NewReplayer(f.pool, mq.Default, f.locks, zerolog.Nop())
	if ran, err := rp.RunOnce(ctx, conn); !ran || err != nil {
		t.Fatalf("replay ran %v: %v", ran, err)
	}
	var status, moved string
	if err := f.pool.QueryRow(ctx, `SELECT status, result->>'moved' FROM jobs WHERE id = $1`, jobID).Scan(&status, &moved); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || moved != "3" {
		t.Fatalf("replay job %s moved %s", status, moved)
	}
	if ran, _ := rp.RunOnce(ctx, conn); ran {
		t.Fatal("a second RunOnce found another queued replay")
	}
	if ok, _, _ := f.locks.Acquire(ctx, f.locks.JobKey(jobs.TypeQueueReplay, "billing.compute"), "probe", time.Minute); !ok {
		t.Fatal("the finished replay still holds its job lock")
	}
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	if q, _ := ch.QueueDeclarePassive("billing.compute.dead", true, false, false, false, nil); q.Messages != 0 {
		t.Fatalf("dead queue still holds %d", q.Messages)
	}
	if q, _ := ch.QueueDeclarePassive("notify.line", true, false, false, false, nil); q.Messages != 0 {
		t.Fatalf("the replay reached notify.line (%d), which shares trip.delivered", q.Messages)
	}
	for range 3 {
		d, ok, err := ch.Get("billing.compute", true)
		if err != nil || !ok {
			t.Fatalf("billing.compute: %v %v", ok, err)
		}
		if _, has := d.Headers[mq.HeaderAttempts]; has || d.Headers[mq.HeaderOriginalRoutingKey] != "trip.delivered" ||
			d.RoutingKey != jobs.ReplayKeyPrefix+".billing.compute" {
			t.Fatalf("replayed message key %s headers %v", d.RoutingKey, d.Headers)
		}
	}
}

// cancelOnBegin is the logitrack_app pool, except that the at-th Begin first cancels the leader's
// context: SIGTERM, a step-down or a closed broker connection arriving while a replay runs.
type cancelOnBegin struct {
	pool   *pgxpool.Pool
	at     int32
	n      atomic.Int32
	cancel context.CancelFunc
}

func (b *cancelOnBegin) Begin(ctx context.Context) (pgx.Tx, error) {
	if b.n.Add(1) == b.at {
		b.cancel()
	}
	return b.pool.Begin(ctx)
}

// A replay interrupted by the end of the leader's context still finishes its job row: failed, with
// the count moved so far; the rest stays in {queue}.dead and the job lock is released.
func TestQueueReplayInterruptedIsRecorded(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	admin := f.user(t, "admin@logitrack.test")
	r := f.do(t, "POST", "/v1/admin/queues/billing.compute/replay", &admin, "platform_admin")
	jobID, _ := r.body["data"].(map[string]any)["jobId"].(string)
	if r.status != http.StatusAccepted || jobID == "" {
		t.Fatalf("replay: %d %v", r.status, r.body)
	}

	url, _ := asynctest.SharedRabbit(t).VHost(t)
	conn, err := mq.Dial(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := mq.DeclareTopology(conn, mq.Default); err != nil {
		t.Fatal(err)
	}
	pub, err := mq.NewPublisher(conn)
	if err != nil {
		t.Fatal(err)
	}
	const dead = jobs.ProgressEvery + 10
	for i := range dead {
		if err := pub.Publish(ctx, mq.ExchangeDLX, "billing.compute", amqp.Publishing{MessageId: strconv.Itoa(i + 1), Body: []byte(`{}`),
			Headers: amqp.Table{mq.HeaderAttempts: int32(5), mq.HeaderOriginalExchange: mq.ExchangeEvents,
				mq.HeaderOriginalRoutingKey: "trip.delivered"}}); err != nil {
			t.Fatal(err)
		}
	}

	// Begin 1 claims the job, Begin 2 is the progress write after ProgressEvery messages: the leader's
	// context ends there, so the run stops and the outcome is written on a detached context.
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b := &cancelOnBegin{pool: f.pool, at: 2, cancel: cancel}
	rp := jobs.NewReplayer(b, mq.Default, f.locks, zerolog.Nop())
	ran, err := rp.RunOnce(lctx, conn)
	if !ran || err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted replay: ran %v err %v", ran, err)
	}
	var status, moved string
	var cause *string
	if err := f.pool.QueryRow(ctx, `SELECT status, result->>'moved', error FROM jobs WHERE id = $1`, jobID).Scan(&status, &moved, &cause); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || moved != strconv.Itoa(jobs.ProgressEvery) || cause == nil {
		t.Fatalf("interrupted replay job: status %s moved %s error %v; want failed with moved %d", status, moved, cause, jobs.ProgressEvery)
	}
	if ran, err := jobs.NewReplayer(f.pool, mq.Default, f.locks, zerolog.Nop()).RunOnce(ctx, conn); ran || err != nil {
		t.Fatalf("a finished replay was claimed again: %v %v", ran, err)
	}
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	if q, _ := ch.QueueDeclarePassive("billing.compute.dead", true, false, false, false, nil); q.Messages != dead-jobs.ProgressEvery {
		t.Fatalf("dead queue holds %d, want the %d not moved", q.Messages, dead-jobs.ProgressEvery)
	}
	if ok, _, _ := f.locks.Acquire(ctx, f.locks.JobKey(jobs.TypeQueueReplay, "billing.compute"), "probe", time.Minute); !ok {
		t.Fatal("the interrupted replay still holds its job lock")
	}
}
