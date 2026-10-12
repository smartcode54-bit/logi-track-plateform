//go:build integration

// Integration tests of T47 (issue #67): PostgreSQL 18 with the full goose chain (requests as logitrack_app, so RLS
// decides), Redis 7, the real auth service and RBAC, and the route groups exactly as cmd/api wires them (chats.Groups,
// app.MobileGroups with the driver's own chat) on both listeners; the local storage backend for chat images; the
// notify.fcm consumer in-process over the FCM HTTP v1 stand-in (pushtest) for the push acceptance criterion.
//
// Fixture (logitrack_etl, BYPASSRLS): own fleet O; carrier A working for O (contractor reach); independent carrier C;
// staff of each (tenant_admin; in O also an operator, an operation_staff and a user, who holds no chat capability);
// drivers linked to users with a driver membership (C.1.4) and an app device each.
package chats_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push/pushtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
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
	ctx      context.Context
	pool     *pgxpool.Pool
	etl      *pgx.Conn
	owner    *pgxpool.Pool // logitrack_migrator: DDL of a test
	rdb      redis.UniversalClient
	ks       cache.Keyspace
	svc      *chats.Service
	hook     *hookPool         // the service's pool: a test runs code before one of its transactions
	fs       *writeback.Memory // the Firestore of the write-back mode (worldOpts.writeBack)
	internal string
	public   string
	hash     string
	tokens   map[string]string

	O, A, C string // tenants
	// drivers (and their users): dO, dO2 (O), dA (A), dC (C)
	dO, dO2, dA, dC         string
	uDO, uDO2, uDA, uDC     string
	taO, opO, osO, usO, taC string // staff user ids
}

type worldOpts struct {
	// pgOwned false builds the service without a Firestore writer (comms Firestore-owned, write-back unconfigured).
	notPGOwned bool
	// writeBack builds the service on the Firestore write-back (comms Firestore-owned) over world.fs, without a mirror
	// acknowledgement: every write-back answers 202 and PostgreSQL holds only what the test writes as the mirror.
	writeBack bool
}

// hookPool is the service's pool with a hook that runs before each Begin (n counts the service's transactions from 1
// since the hook was set): a test closes a chat between two transactions of one request, or fails one of them.
type hookPool struct {
	pool   *pgxpool.Pool
	mu     sync.Mutex
	n      int
	before func(n int) error
}

func (h *hookPool) Begin(ctx context.Context) (pgx.Tx, error) {
	h.mu.Lock()
	h.n++
	n, fn := h.n, h.before
	h.mu.Unlock()
	if fn != nil {
		if err := fn(n); err != nil {
			return nil, err
		}
	}
	return h.pool.Begin(ctx)
}

// set installs fn (nil removes it) and restarts the count.
func (h *hookPool) set(fn func(n int) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n, h.before = 0, fn
}

func newWorld(t *testing.T, opts ...worldOpts) *world {
	t.Helper()
	var o worldOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, ctx: ctx, pool: d.Pool(t, db.RoleApp), owner: d.Pool(t, db.RoleMigrator), tokens: map[string]string{}}
	var err error
	if w.etl, err = pgx.Connect(ctx, d.URL(db.RoleETL)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.etl.Close(context.Background()) })
	hasher, err := password.NewHasher(password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	if w.hash, err = hasher.Hash(ctx, pw); err != nil {
		t.Fatal(err)
	}
	w.fixture()

	logger := zerolog.Nop()
	rdb, ks := cachetest.NewClient(t)
	w.rdb, w.ks = rdb, ks
	caches := cache.New(rdb, ks)
	rbac, err := iam.NewRBAC(iam.Deps{Pool: w.pool, Cache: caches, Redis: rdb, Log: logger})
	if err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(storage.LocalConfig{Dir: t.TempDir(), PublicBaseURL: "http://127.0.0.1:1/media",
		SigningKey: bytes.Repeat([]byte("f"), 40), APIUploadPath: storage.LocalUploadPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	st, err := storage.New(w.pool, storage.Config{Active: storage.BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public",
		PresignPutTTL: 15 * time.Minute, PresignGetTTL: time.Hour, UploadMaxBytes: 1 << 20}, logger, local)
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
	limiter := ratelimit.New(rdb, ks, logger)
	svc, err := auth.New(auth.Config{RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour,
		PasswordResetTTL: 30 * time.Minute, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute}}, auth.Deps{
		Pool: w.pool, Store: auth.NewStore(rdb, ks.Prefix()), Limiter: limiter,
		Keys: token.New(priv, "http://localhost:8080", "logitrack-test", 15*time.Minute), Hasher: hasher,
		Policy: policy, Log: logger, Capabilities: rbac, Authorizer: rbac, Files: st,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	w.hook = &hookPool{pool: w.pool}
	deps := chats.Deps{Pool: w.hook, Files: st, Roles: rbac, PGOwned: !o.notPGOwned && !o.writeBack, Log: logger}
	if o.writeBack {
		w.fs = writeback.NewMemory()
		deps.WriteBack = &chats.WriteBack{Writer: w.fs}
	}
	if w.svc, err = chats.New(deps); err != nil {
		t.Fatal(err)
	}
	api := app.APIDeps{Auth: svc, Storage: st, Chats: w.svc, Jobs: jobs.NewService(w.pool, jobs.NewRedisLocker(rdb, ks)),
		Limiter: limiter}
	groups := append(svc.Groups(), chats.Groups(w.svc, svc.RequireAuth())...)
	groups = append(groups, app.MobileGroups(api)...)
	groups = append(groups, app.StorageGroups(api)...)
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		StorageAPI:   app.StorageAPI{UploadMaxBytes: 1 << 20},
		InternalAddr: "127.0.0.1:0", PublicAddr: "127.0.0.1:0", PublicRouteGroups: ingress.PublicPrefixes,
	}
	a, err := app.NewAPI(cfg, logger, groups...)
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
	in, pub, _ := a.Addrs()
	w.internal, w.public = "http://"+in, "http://"+pub
	return w
}

// --- fixture (logitrack_etl, BYPASSRLS) ----------------------------------------------------------------------------

func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.etl.Exec(w.ctx, sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

func (w *world) id(sql string, args ...any) string {
	w.t.Helper()
	var v string
	if err := w.etl.QueryRow(w.ctx, sql, args...).Scan(&v); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func (w *world) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.etl.QueryRow(w.ctx, sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (w *world) user(email string) string {
	return w.id(`INSERT INTO users (email, email_verified, display_name, password_hash)
		VALUES ($1::text, true, $1::text, $2) RETURNING id::text`, email, w.hash)
}

func (w *world) member(email, tenant, role string) string {
	u := w.user(email)
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, u, tenant, role)
	return u
}

// driver creates a driver of tenant linked to a new user with a driver membership (C.1.4) and an app device.
func (w *world) driver(email, tenant, nameTh string) (driver, user string) {
	user = w.member(email, tenant, "driver")
	driver = w.id(`INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, full_name_th, mobile, legacy_auth_uid)
		VALUES ($1, 'self', $2::uuid, 'First', 'Last', $3, '0800000001', 'uid_' || $2::text) RETURNING id::text`, tenant, user, nameTh)
	w.exec(`INSERT INTO device_tokens (user_id, install_id, token, platform, driver_id) VALUES ($1::uuid, 'inst-' || $1::text, 'tok-' || $1::text, 'android', $2)`,
		user, driver)
	return driver, user
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'โอ', 'O') RETURNING id::text`)
	w.A = w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type, contractor_tenant_id)
		VALUES ('carrier', 'เอ', 'A', 'company', $1) RETURNING id::text`, w.O)
	w.C = w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type) VALUES ('carrier', 'ซี', 'C', 'company') RETURNING id::text`)
	w.taO = w.member("ta.o@example.test", w.O, "tenant_admin")
	w.opO = w.member("op.o@example.test", w.O, "operator")
	w.osO = w.member("os.o@example.test", w.O, "operation_staff")
	w.usO = w.member("us.o@example.test", w.O, "user")
	w.member("ta.a@example.test", w.A, "tenant_admin")
	w.taC = w.member("ta.c@example.test", w.C, "tenant_admin")
	w.dO, w.uDO = w.driver("d.o@example.test", w.O, "สมชาย ใจดี")
	w.dO2, w.uDO2 = w.driver("d.o2@example.test", w.O, "สมศักดิ์ รักงาน")
	w.dA, w.uDA = w.driver("d.a@example.test", w.A, "เอ ขับดี")
	w.dC, w.uDC = w.driver("d.c@example.test", w.C, "ซี ขับเร็ว")
}

// --- HTTP ----------------------------------------------------------------------------------------------------------

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

func (r resp) detail(k string) any {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	return d[k]
}

func (r resp) data() map[string]any {
	m, _ := r.body["data"].(map[string]any)
	return m
}

func (r resp) list() []any {
	l, _ := r.body["data"].([]any)
	return l
}

func (r resp) next() string {
	s, _ := r.body["nextCursor"].(string)
	return s
}

func (w *world) call(base, method, path, bearer string, body any) resp {
	w.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			w.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		w.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
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

func (w *world) get(path, bearer string) resp {
	w.t.Helper()
	return w.call(w.internal, http.MethodGet, path, bearer, nil)
}

func (w *world) post(path, bearer string, body any) resp {
	w.t.Helper()
	return w.call(w.internal, http.MethodPost, path, bearer, body)
}

// login signs in through POST /v1/auth/login; a driver signs in from the app (platform android, an install id).
func (w *world) login(email string) string {
	w.t.Helper()
	if tok, ok := w.tokens[email]; ok {
		return tok
	}
	body := map[string]any{"email": email, "password": pw, "platform": "web"}
	if strings.HasPrefix(email, "d.") {
		body["platform"], body["installId"] = "android", "login-"+email
	}
	r := w.call(w.public, http.MethodPost, "/v1/auth/login", "", body)
	tok, _ := r.data()["accessToken"].(string)
	if r.status != http.StatusOK || tok == "" {
		w.t.Fatalf("login %s: %d %s", email, r.status, r.raw)
	}
	w.tokens[email] = tok
	return tok
}

func expect(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status || (code != "" && r.code() != code) {
		t.Fatalf("want %d %s, got %d %s", status, code, r.status, r.raw)
	}
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) float64 { f, _ := v.(float64); return f }

// openChat opens (or gets) the driver's chat as staff and returns its id.
func (w *world) openChat(bearer, driver string) string {
	w.t.Helper()
	r := w.post("/v1/chats", bearer, map[string]any{"driverId": driver})
	if r.status != http.StatusOK && r.status != http.StatusCreated {
		w.t.Fatalf("open chat: %d %s", r.status, r.raw)
	}
	return str(r.data()["id"])
}

// say sends a staff message and returns the response.
func (w *world) say(bearer, chat, text string) resp {
	w.t.Helper()
	return w.post("/v1/chats/"+chat+"/messages", bearer, map[string]any{"text": text, "clientMessageId": uuid.NewString()})
}

// driverSays sends a message from the driver app.
func (w *world) driverSays(bearer, text string) resp {
	w.t.Helper()
	return w.call(w.public, http.MethodPost, "/v1/mobile/chat/messages", bearer,
		map[string]any{"text": text, "clientMessageId": uuid.NewString()})
}

// events lists the outbox rows of a chat (routing key, topics, payload), in commit order.
type event struct {
	ID      int64
	Key     string
	Topics  []string
	Payload map[string]any
	Raw     []byte
	Headers map[string]string
}

func (w *world) events(chat string) []event {
	w.t.Helper()
	rows, err := w.etl.Query(w.ctx, `SELECT id, routing_key, realtime_topics, payload, headers FROM outbox_events
		WHERE aggregate_type = 'chat' AND aggregate_id = $1 ORDER BY id`, chat)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []event
	for rows.Next() {
		var e event
		var hdr []byte
		if err := rows.Scan(&e.ID, &e.Key, &e.Topics, &e.Raw, &hdr); err != nil {
			w.t.Fatal(err)
		}
		_ = json.Unmarshal(e.Raw, &e.Payload)
		_ = json.Unmarshal(hdr, &e.Headers)
		out = append(out, e)
	}
	return out
}

func keys(es []event, key string) []event {
	var out []event
	for _, e := range es {
		if e.Key == key {
			out = append(out, e)
		}
	}
	return out
}

// pusher runs notify.fcm in-process over the FCM stand-in.
type pusher struct {
	fcm      *pushtest.Server
	consumer *notify.FCM
}

func (w *world) pusher(adminPush bool) *pusher {
	w.t.Helper()
	srv := pushtest.New(w.t)
	return &pusher{fcm: srv, consumer: &notify.FCM{Pool: w.pool, Sender: srv.Client(0), Redis: w.rdb, Keys: w.ks, Enabled: true,
		NotifyAdminOnDriverMessage: adminPush, Log: zerolog.Nop()}}
}

// deliver hands an outbox row to notify.fcm as the relay would publish it.
func (w *world) deliver(p *pusher, e event) {
	w.t.Helper()
	var tenant *uuid.UUID
	if s := str(e.Payload["tenantId"]); s != "" {
		id := uuid.MustParse(s)
		tenant = &id
	}
	hdr := map[string]any{}
	for k, v := range e.Headers {
		hdr[k] = v
	}
	d := &mq.Delivery{Queue: notify.QueueFCM, MessageID: strconv.FormatInt(e.ID, 10), Exchange: mq.ExchangeEvents,
		RoutingKey: e.Key, EventType: e.Key, TenantID: tenant, Body: e.Raw, Headers: hdr}
	if err := p.consumer.Handle(w.ctx, d); err != nil {
		w.t.Fatalf("notify.fcm %s %d: %v", e.Key, e.ID, err)
	}
}
