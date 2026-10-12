//go:build integration

// Integration tests of T48 (issue #68): PostgreSQL 18 with the full goose chain (requests as logitrack_app, so RLS
// decides), Redis 7, the real auth service and RBAC, and the route groups exactly as cmd/api wires them
// (broadcasts.Groups, app.MobileGroups with the driver's broadcasts and the markBroadcastRead shim target) on both
// listeners; the shim target behind the real auth.RequireShim (a cf_shim API key with the driver's Firebase ID token,
// minted with firebasetest's local keys); the notify.fcm consumer in-process over the FCM HTTP v1 stand-in (pushtest)
// for the push acceptance criterion.
//
// Fixture (logitrack_etl, BYPASSRLS): own fleet O; carrier A working for O (contractor reach); independent carrier C;
// staff of each (tenant_admin; in O also a manager and an operation_staff, who may view but not send); drivers linked
// to users with a driver membership (C.1.4), an app device each and a Firebase uid: dO (O, full time), dOs (O,
// employment subcontractor), dA (A), dC (C), and dOi (O, inactive); a platform admin without a tenant.
package broadcasts_test

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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/apikey"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase/firebasetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/broadcasts"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const pw = "a long passphrase 1"

// testPepper is the API_KEY_PEPPER of the world: a placeholder, built at run time.
var testPepper = strings.Repeat("pepper-placeholder-", 3)

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
	rdb      redis.UniversalClient
	ks       cache.Keyspace
	fb       *firebasetest.Backend
	shimKey  string
	reg      *prometheus.Registry // shim_requests_total
	internal string
	public   string
	hash     string
	tokens   map[string]string

	O, A, C string // tenants
	// drivers and their users
	dO, dOs, dA, dC, dOi      string
	uDO, uDOs, uDA, uDC, uDOi string
	taO, mgO, osO, taA, taC   string // staff user ids
	pa                        string // platform admin
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, ctx: ctx, pool: d.Pool(t, db.RoleApp), tokens: map[string]string{}}
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

	logger := zerolog.Nop()
	rdb, ks := cachetest.NewClient(t)
	w.rdb, w.ks = rdb, ks
	caches := cache.New(rdb, ks)
	rbac, err := iam.NewRBAC(iam.Deps{Pool: w.pool, Cache: caches, Redis: rdb, Log: logger})
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
	pepper, err := apikey.NewPepper(testPepper)
	if err != nil {
		t.Fatal(err)
	}
	w.fb = firebasetest.New(t, "logitrack-broadcasts-test")
	limiter := ratelimit.New(rdb, ks, logger)
	svc, err := auth.New(auth.Config{RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour,
		PasswordResetTTL: 30 * time.Minute, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute}, APIKeyPepper: pepper}, auth.Deps{
		Pool: w.pool, Store: auth.NewStore(rdb, ks.Prefix()), Limiter: limiter,
		Keys: token.New(priv, "http://localhost:8080", "logitrack-test", 15*time.Minute), Hasher: hasher,
		Policy: policy, Log: logger, Capabilities: rbac, Authorizer: rbac, Firebase: auth.Firebase{Verifier: w.fb.Verifier(nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	w.reg = prometheus.NewRegistry()
	if err := svc.Register(w.reg); err != nil {
		t.Fatal(err)
	}
	// The cf_shim key of the markBroadcastRead shim: a platform key (api_keys_cf_shim_platform), as POST /v1/api-keys
	// issues it for a platform admin.
	key, err := apikey.Generate(pepper)
	if err != nil {
		t.Fatal(err)
	}
	w.exec(`INSERT INTO api_keys (name, scope, key_prefix, key_hash, capabilities, created_by)
		VALUES ('callable shims', 'cf_shim', $1, $2, '{mobile:broadcasts}', $3)`, key.Prefix, key.Hash, w.pa)
	w.shimKey = key.Secret

	bc, err := broadcasts.New(broadcasts.Deps{Pool: w.pool, Log: logger})
	if err != nil {
		t.Fatal(err)
	}
	api := app.APIDeps{Auth: svc, Broadcasts: bc, Limiter: limiter}
	groups := append(svc.Groups(), broadcasts.Groups(bc, svc.RequireAuth())...)
	groups = append(groups, app.MobileGroups(api)...)
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
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

// driver creates a driver of tenant linked to a new user with a driver membership (C.1.4), an app device and the
// Firebase uid fb_{email}.
func (w *world) driver(email, tenant, employment, status string) (driver, user string) {
	user = w.member(email, tenant, "driver")
	driver = w.id(`INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, full_name_th, mobile,
		employment_type, status) VALUES ($1, 'self', $2::uuid, 'First', 'Last', $3, '0800000001', $4, $5) RETURNING id::text`,
		tenant, user, email, employment, status)
	w.exec(`UPDATE users SET legacy_auth_uid = $2 WHERE id = $1`, user, firebaseUID(email))
	w.exec(`INSERT INTO device_tokens (user_id, install_id, token, platform, driver_id) VALUES ($1::uuid, 'inst-' || $1::text, 'tok-' || $1::text, 'android', $2)`,
		user, driver)
	return driver, user
}

// firebaseUID is the Firebase uid of a fixture user (28 characters, like the real ones).
func firebaseUID(email string) string {
	s := strings.NewReplacer("@", "", ".", "", "-", "").Replace(email)
	return (s + strings.Repeat("0", 28))[:28]
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'โอ', 'O') RETURNING id::text`)
	w.A = w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type, contractor_tenant_id)
		VALUES ('carrier', 'เอ', 'A', 'company', $1) RETURNING id::text`, w.O)
	w.C = w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type) VALUES ('carrier', 'ซี', 'C', 'company') RETURNING id::text`)
	w.taO = w.member("ta.o@example.test", w.O, "tenant_admin")
	w.exec(`UPDATE users SET display_name = 'ฝ่ายปฏิบัติการ' WHERE id = $1`, w.taO)
	w.mgO = w.member("mg.o@example.test", w.O, "manager")
	w.osO = w.member("os.o@example.test", w.O, "operation_staff")
	w.taA = w.member("ta.a@example.test", w.A, "tenant_admin")
	w.taC = w.member("ta.c@example.test", w.C, "tenant_admin")
	w.pa = w.user("pa@example.test")
	w.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, w.pa)
	w.dO, w.uDO = w.driver("d.o@example.test", w.O, "full_time", "active")
	w.dOs, w.uDOs = w.driver("d.os@example.test", w.O, "subcontractor", "on_duty")
	w.dA, w.uDA = w.driver("d.a@example.test", w.A, "full_time", "active")
	w.dC, w.uDC = w.driver("d.c@example.test", w.C, "full_time", "active")
	w.dOi, w.uDOi = w.driver("d.oi@example.test", w.O, "full_time", "inactive")
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

// fields are the field: reason pairs of a 422.
func (r resp) fields() []string {
	var out []string
	l, _ := r.detail("fields").([]any)
	for _, f := range l {
		m, _ := f.(map[string]any)
		out = append(out, str(m["field"])+":"+str(m["reason"]))
	}
	return out
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

func (w *world) call(base, method, path string, body any, headers ...string) resp {
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
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
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

func bearer(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

func (w *world) get(path, tok string) resp {
	w.t.Helper()
	return w.call(w.internal, http.MethodGet, path, nil, bearer(tok)...)
}

func (w *world) post(path, tok string, body any) resp {
	w.t.Helper()
	return w.call(w.internal, http.MethodPost, path, body, bearer(tok)...)
}

func (w *world) del(path, tok string) resp {
	w.t.Helper()
	return w.call(w.internal, http.MethodDelete, path, nil, bearer(tok)...)
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
	r := w.call(w.public, http.MethodPost, "/v1/auth/login", body)
	tok, _ := r.data()["accessToken"].(string)
	if r.status != http.StatusOK || tok == "" {
		w.t.Fatalf("login %s: %d %s", email, r.status, r.raw)
	}
	w.tokens[email] = tok
	return tok
}

// firebaseToken is the Firebase ID token of a fixture user, as the 3.x app sends it to a callable.
func (w *world) firebaseToken(email string) string {
	now := time.Now()
	return w.fb.SignIDToken(w.fb.IDTokenClaims(firebaseUID(email), now.Add(-5*time.Minute), now))
}

// markRead calls the markBroadcastRead shim target as the shim does: the cf_shim key and the caller's Firebase ID
// token, on the public listener.
func (w *world) markRead(email, ref string) resp {
	w.t.Helper()
	return w.call(w.public, http.MethodPost, "/v1/mobile/broadcasts/"+ref+"/read", map[string]any{},
		append(bearer(w.firebaseToken(email)), auth.HeaderAPIKey, w.shimKey)...)
}

func expect(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status || (code != "" && r.code() != code) {
		t.Fatalf("want %d %s, got %d %s", status, code, r.status, r.raw)
	}
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) float64 { f, _ := v.(float64); return f }

// send posts a broadcast and returns its id.
func (w *world) send(tok string, body map[string]any) string {
	w.t.Helper()
	if body["title"] == nil {
		body["title"] = "ประกาศ"
	}
	if body["messageText"] == nil {
		body["messageText"] = "ข้อความ"
	}
	r := w.post("/v1/broadcasts", tok, body)
	expect(w.t, r, http.StatusCreated, "")
	return str(r.data()["id"])
}

// recipients are the persisted recipients of a broadcast, sorted.
func (w *world) recipients(id string) []string {
	w.t.Helper()
	rows, err := w.etl.Query(w.ctx, `SELECT user_id::text FROM broadcast_recipients WHERE broadcast_id = $1 ORDER BY 1`, id)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

// event is one outbox row of a broadcast.
type event struct {
	ID      int64
	Key     string
	Topics  []string
	Payload map[string]any
	Raw     []byte
}

func (w *world) events(broadcast string) []event {
	w.t.Helper()
	rows, err := w.etl.Query(w.ctx, `SELECT id, routing_key, realtime_topics, payload FROM outbox_events
		WHERE aggregate_type = 'broadcast' AND aggregate_id = $1 ORDER BY id`, broadcast)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.ID, &e.Key, &e.Topics, &e.Raw); err != nil {
			w.t.Fatal(err)
		}
		_ = json.Unmarshal(e.Raw, &e.Payload)
		out = append(out, e)
	}
	return out
}

// deliver hands an outbox row to notify.fcm as the relay would publish it.
func (w *world) deliver(f *notify.FCM, e event) error {
	w.t.Helper()
	var tenant *uuid.UUID
	if s := str(e.Payload["tenantId"]); s != "" {
		id := uuid.MustParse(s)
		tenant = &id
	}
	d := &mq.Delivery{Queue: notify.QueueFCM, MessageID: strconv.FormatInt(e.ID, 10), Exchange: mq.ExchangeEvents,
		RoutingKey: e.Key, EventType: e.Key, TenantID: tenant, Body: e.Raw}
	return f.Handle(w.ctx, d)
}

// fcm is notify.fcm in-process over the FCM stand-in.
func (w *world) fcm() (*pushtest.Server, *notify.FCM) {
	srv := pushtest.New(w.t)
	return srv, &notify.FCM{Pool: w.pool, Sender: srv.Client(0), Redis: w.rdb, Keys: w.ks, Enabled: true, Log: zerolog.Nop()}
}

// shimCalls is shim_requests_total{callable="markBroadcastRead"}: every authenticated call of the shim target (R26).
func (w *world) shimCalls() float64 {
	w.t.Helper()
	mfs, err := w.reg.Gather()
	if err != nil {
		w.t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "shim_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "callable" && l.GetValue() == app.ShimMarkBroadcastRead {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	w.t.Fatal("no shim_requests_total{callable=markBroadcastRead} series")
	return 0
}
