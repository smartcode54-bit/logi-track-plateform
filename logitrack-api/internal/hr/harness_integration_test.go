//go:build integration

// Integration tests of T44 (issue #64): compensation config versions, penalties, payroll runs (list, detail, approve,
// status) and the payroll.run job, against PostgreSQL 18 with the full goose chain (pgtest; every request as
// logitrack_app through db.WithPrincipal, so RLS decides; the consumer in db.WithSystem), Redis 7 (the job lock), the
// real auth service and RBAC and the storage service on the local backend.
// Fixture: own fleet O; carrier A working for O; independent carrier C; staff of O (tenant_admin, manager, user), of A
// and of C; drivers of O (dO with a login, dO2 without), of A and of C.
package hr_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const pw = "a long passphrase 1"

func TestMain(m *testing.M) {
	app.DrainGrace = 0
	code := pgtest.Main(m)
	cachetest.TerminateShared()
	os.Exit(code)
}

type world struct {
	t        *testing.T
	pool     *pgxpool.Pool
	etl      *pgx.Conn
	rdb      *redis.Client
	ks       cache.Keyspace
	internal string
	hash     string
	tokens   map[string]string

	O, A, C string // tenants
	// drivers: dO (a login), dO2 (no login), dA (carrier A), dC (carrier C)
	dO, dO2, dA, dC string
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, pool: d.Pool(t, db.RoleApp), tokens: map[string]string{}}
	var err error
	if w.etl, err = pgx.Connect(ctx, d.URL(db.RoleETL)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.etl.Close(context.Background()) })
	w.exec(`SELECT set_config('app.bypass_tenant', 'on', false)`) // fixtures play the system context, as cmd/seed does
	hasher, err := password.NewHasher(password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	if w.hash, err = hasher.Hash(ctx, pw); err != nil {
		t.Fatal(err)
	}
	w.fixture()

	w.rdb, w.ks = cachetest.NewClient(t)
	caches := cache.New(w.rdb, w.ks)
	rbac, err := iam.NewRBAC(iam.Deps{Pool: w.pool, Cache: caches, Redis: w.rdb, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	publicAddr := freeAddr(t)
	local, err := storage.NewLocal(storage.LocalConfig{Dir: t.TempDir(), PublicBaseURL: "http://" + publicAddr + "/media",
		SigningKey: bytes.Repeat([]byte("f"), 40), APIUploadPath: storage.LocalUploadPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	st, err := storage.New(w.pool, storage.Config{Active: storage.BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public",
		PresignPutTTL: 15 * time.Minute, PresignGetTTL: time.Hour, UploadMaxBytes: 1 << 20}, zerolog.Nop(), local)
	if err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := password.NewPolicy(10)
	if err != nil {
		t.Fatal(err)
	}
	limiter := ratelimit.New(w.rdb, w.ks, zerolog.Nop())
	svc, err := auth.New(auth.Config{RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour,
		PasswordResetTTL: 30 * time.Minute}, auth.Deps{
		Pool: w.pool, Store: auth.NewStore(w.rdb, w.ks.Prefix()), Limiter: limiter,
		Keys: token.New(priv, "http://localhost:8080", "logitrack-test", 15*time.Minute), Hasher: hasher,
		Policy: policy, Log: zerolog.Nop(), Capabilities: rbac, Authorizer: rbac, Files: st,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	jobSvc := jobs.NewService(w.pool, jobs.NewRedisLocker(w.rdb, w.ks))
	hs, err := hr.New(hr.Deps{Pool: w.pool, Jobs: jobSvc, Files: st, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	deps := app.APIDeps{Auth: svc, Jobs: jobSvc, HR: hs, Storage: st, Limiter: limiter}
	groups := append(svc.Groups(), hr.Groups(hs, svc.RequireAuth())...)
	groups = append(groups, app.JobGroups(deps)...)
	groups = append(groups, app.StorageGroups(deps)...)
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		StorageAPI:   app.StorageAPI{UploadMaxBytes: 1 << 20},
		InternalAddr: "127.0.0.1:0", PublicAddr: publicAddr, PublicRouteGroups: ingress.PublicPrefixes,
	}
	a, err := app.NewAPI(cfg, zerolog.Nop(), groups...)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CheckRoutes(); err != nil {
		t.Fatal(err)
	}
	if err := a.Listen(); err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Serve(sctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	in, _, _ := a.Addrs()
	w.internal = "http://" + in
	return w
}

// --- fixture (logitrack_etl, BYPASSRLS) ----------------------------------------------------------------

func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.etl.Exec(context.Background(), sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

func (w *world) id(sql string, args ...any) string {
	w.t.Helper()
	var v string
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func (w *world) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (w *world) user(email string) string {
	return w.id(`INSERT INTO users (email, email_verified, display_name, password_hash)
		VALUES ($1::text, true, $1::text, $2) RETURNING id::text`, email, w.hash)
}

func (w *world) carrier(name string, contractor *string) string {
	t := w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type, contractor_tenant_id)
		VALUES ('carrier', $1, $1, 'company', $2) RETURNING id::text`, name, contractor)
	w.exec(`INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', $1)`, t)
	return t
}

// driver inserts a driver; extra are column / SQL literal pairs.
func (w *world) driver(tenant, first string, extra ...string) string {
	id := w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, full_name_th, mobile, legacy_auth_uid)
		VALUES ($1, 'self', $2, 'Dee', $2 || ' ดี', '0812345678', 'uid_' || $2) RETURNING id::text`, tenant, first)
	for i := 0; i+1 < len(extra); i += 2 {
		w.exec(`UPDATE drivers SET `+extra[i]+` = `+extra[i+1]+` WHERE id = $1`, id)
	}
	return id
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'O', 'O') RETURNING id::text`)
	w.A = w.carrier("A", &w.O)
	w.C = w.carrier("C", nil)
	for _, u := range []struct{ email, tenant, role string }{
		{"ta.o", w.O, "tenant_admin"}, {"ta2.o", w.O, "tenant_admin"}, {"mg.o", w.O, "manager"}, {"us.o", w.O, "user"},
		{"ta.a", w.A, "tenant_admin"}, {"ta.c", w.C, "tenant_admin"},
	} {
		w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, w.user(u.email+"@example.test"), u.tenant, u.role)
	}
	// dO: hired 2025 (the existing SSO base), past probation, born 1990.
	w.dO = w.driver(w.O, "Somchai", "hire_date", "'2025-05-01'", "birth_date", "'1990-01-01'", "probation_passed", "true")
	login := w.user("somchai.driver@example.test")
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'driver')`, login, w.O)
	w.exec(`UPDATE drivers SET user_id = $2 WHERE id = $1`, w.dO, login)
	w.dO2 = w.driver(w.O, "Somsak") // no login, in probation: no SSO
	w.dA = w.driver(w.A, "Anan")
	w.dC = w.driver(w.C, "Chai")
}

// trip inserts a delivered trip of drv run by tenant at the Bangkok wall time.
func (w *world) trip(tenant, drv, no string, at time.Time, multi bool) string {
	return w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, driver_id, delivered_at, is_multi_delivery)
		VALUES ($1, 'trip', $2, 'delivered', 'first_mile', $3, $4, $5) RETURNING id::text`, tenant, no, drv, at, multi)
}

// task inserts a task of tenant planned at plan, with the driver and helper and check-in it names ("" / zero = none).
func (w *world) task(tenant, drv, helper string, plan time.Time, checkIn *time.Time, status string) string {
	var d, h any
	if drv != "" {
		d = drv
	}
	if helper != "" {
		h = helper
	}
	return w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw, driver_id,
		helper_driver_id, check_in_at) VALUES ($1, 'self', 'first_mile', $2, $3, 'SRC', 'DST', $4, $5, $6) RETURNING id::text`,
		tenant, status, plan, d, h, checkIn)
}

// --- HTTP ---------------------------------------------------------------------------------------------

type resp struct {
	status int
	raw    []byte
	body   map[string]any
}

func (r resp) code() string {
	e, _ := r.body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func (r resp) data() map[string]any {
	d, _ := r.body["data"].(map[string]any)
	return d
}

func (r resp) list() []map[string]any {
	items, _ := r.body["data"].([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any))
	}
	return out
}

func (r resp) next() string {
	s, _ := r.body["nextCursor"].(string)
	return s
}

func (r resp) meta() map[string]any {
	m, _ := r.body["meta"].(map[string]any)
	return m
}

// fields are the field -> reason pairs of a 422 invalid_argument.
func (r resp) fields() map[string]string {
	out := map[string]string{}
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	fs, _ := d["fields"].([]any)
	for _, f := range fs {
		m := f.(map[string]any)
		out[m["field"].(string)] = m["reason"].(string)
	}
	return out
}

func (r resp) detail(k string) any {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	return d[k]
}

// noRedirect answers GET /v1/files with its 302 instead of following it.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (w *world) send(method, path, bearer, contentType string, body io.Reader) resp {
	w.t.Helper()
	req, err := http.NewRequest(method, w.internal+path, body)
	if err != nil {
		w.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := noRedirect.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, raw: raw}
	if len(raw) > 0 && strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			w.t.Fatalf("%s %s: not JSON: %s", method, path, raw)
		}
	}
	return out
}

func (w *world) do(method, path, bearer string, body any) resp {
	w.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			w.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	return w.send(method, path, bearer, "application/json", rd)
}

func (w *world) get(path, bearer string) resp {
	w.t.Helper()
	return w.do(http.MethodGet, path, bearer, nil)
}

func (w *world) post(path, bearer string, body any) resp {
	w.t.Helper()
	return w.do(http.MethodPost, path, bearer, body)
}

// login signs in through POST /v1/auth/login (once per user) and returns the access token.
func (w *world) login(name string) string {
	w.t.Helper()
	if tok, ok := w.tokens[name]; ok {
		return tok
	}
	r := w.post("/v1/auth/login", "", map[string]any{"email": name + "@example.test", "password": pw, "platform": "web"})
	tok, _ := r.data()["accessToken"].(string)
	if r.status != http.StatusOK || tok == "" {
		w.t.Fatalf("login %s: %d %s", name, r.status, r.raw)
	}
	w.tokens[name] = tok
	return tok
}

func expect(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status || (code != "" && r.code() != code) {
		t.Fatalf("want %d %s, got %d %s", status, code, r.status, r.raw)
	}
}

// upload presigns purpose for the caller, sends the bytes to the signed local URL and returns the key.
func (w *world) upload(bearer, purpose, entityID, contentType string, body []byte) string {
	w.t.Helper()
	r := w.post("/v1/uploads/presign", bearer, map[string]any{"purpose": purpose, "entityId": entityID, "contentType": contentType,
		"sizeBytes": len(body), "fileName": "evidence"})
	expect(w.t, r, http.StatusOK, "")
	key, _ := r.data()["key"].(string)
	u, _ := r.data()["url"].(string)
	put, err := http.NewRequest(http.MethodPut, w.internal+u, bytes.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	for k, v := range r.data()["headers"].(map[string]any) {
		put.Header.Set(k, v.(string))
	}
	res, err := http.DefaultClient.Do(put)
	if err != nil {
		w.t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		w.t.Fatalf("upload %s: %d", key, res.StatusCode)
	}
	return key
}

// --- the job ------------------------------------------------------------------------------------------

func (w *world) newJob(t *testing.T) *hr.PayrollJob {
	t.Helper()
	j, err := hr.NewPayrollJob(hr.JobDeps{Pool: w.pool, Locks: jobs.NewRedisLocker(w.rdb, w.ks), Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// runJob delivers the job's lt.jobs command (as the relay publishes it) to the consumer.
func (w *world) runJob(t *testing.T, jobID string) {
	t.Helper()
	var body []byte
	if err := w.etl.QueryRow(context.Background(), `SELECT payload::text FROM outbox_events
		WHERE exchange = 'lt.jobs' AND routing_key = 'job.payroll.run' AND aggregate_id = $1`, jobID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	reg := w.newJob(t).Registration()
	if reg.Queue != "payroll.run" {
		t.Fatal(reg.Queue)
	}
	if err := reg.Handler(context.Background(), &mq.Delivery{Queue: reg.Queue, RoutingKey: hr.RoutePayrollJob, Body: body}); err != nil {
		t.Fatal(err)
	}
}

// generate submits payroll.run as bearer and runs it; it returns the finished job.
func (w *world) generate(t *testing.T, bearer, period, round string) map[string]any {
	t.Helper()
	r := w.post("/v1/jobs/payroll.run", bearer, map[string]any{"period": period, "round": round})
	expect(t, r, http.StatusAccepted, "")
	id := str(r.data()["jobId"])
	w.runJob(t, id)
	j := w.get("/v1/jobs/"+id, bearer)
	expect(t, j, http.StatusOK, "")
	return j.data()
}

// --- small helpers --------------------------------------------------------------------------------------

func str(v any) string { s, _ := v.(string); return s }

func num(v any) float64 { f, _ := v.(float64); return f }

// bkk is a Bangkok wall time.
func bkk(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, clock.Bangkok)
}

func at(t time.Time) *time.Time { return &t }

var pdf = append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{'x'}, 64)...)
