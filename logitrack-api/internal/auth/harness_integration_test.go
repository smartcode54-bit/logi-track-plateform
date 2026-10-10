//go:build integration

// Integration tests of the auth service (Appendix C §C.9.3): PostgreSQL 18 with the full goose chain
// (pgtest, logitrack_app login), a real Redis 7 container (cachetest: the compose image and flags, a
// logical database per harness), and both HTTP listeners of the api process.
package auth_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const (
	issuer   = "http://localhost:8080"
	audience = "logitrack-test"
	pw       = "a long passphrase 1"
)

// fast keeps Argon2id cheap in tests; the service and the fixtures use the same parameters, so a
// login never re-hashes unless a test wants it to.
var fast = password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1}

func TestMain(m *testing.M) {
	app.DrainGrace = 0
	code := pgtest.Main(m)
	cachetest.TerminateShared()
	os.Exit(code)
}

// clock is the service clock; tests move it forward instead of sleeping.
type clock struct{ offset atomic.Int64 }

func (c *clock) Now() time.Time          { return time.Now().Add(time.Duration(c.offset.Load())) }
func (c *clock) Advance(d time.Duration) { c.offset.Add(int64(d)) }

type harness struct {
	t        *testing.T
	d        *pgtest.Database
	pool     *pgxpool.Pool
	etl      *pgx.Conn
	rdb      *redis.Client
	ks       cache.Keyspace
	prefix   string               // ks.Prefix()
	limiter  *ratelimit.Limiter   // the rate limiter of every service on rdb, as in the api process
	limits   *prometheus.Registry // its metrics
	svc      *auth.Service
	keys     *token.KeySet
	clock    *clock
	internal string
	public   string
	scrypt   firebasescrypt.Params
	google   auth.GoogleVerifier // nil: Google sign-in off
}

type option func(*auth.Config)

func newHarness(t *testing.T, opts ...option) *harness {
	t.Helper()
	return newHarnessWith(t, nil, opts...)
}

// newHarnessWith builds the harness with the Google verifier that google returns for the harness clock
// (google_integration_test.go); a nil google leaves Google sign-in off.
func newHarnessWith(t *testing.T, google func(now func() time.Time) auth.GoogleVerifier, opts ...option) *harness {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, d: d, pool: d.Pool(t, db.RoleApp), clock: &clock{}}
	if google != nil {
		h.google = google(h.clock.Now)
	}
	var err error
	if h.etl, err = pgx.Connect(ctx, d.URL(db.RoleETL)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.etl.Close(context.Background()) })
	// Fixture writes play the system context, as cmd/seed does through db.WithSystem: the column guards
	// (t_users_self_columns) let only bypass sessions change status, flags and hashes.
	if _, err := h.etl.Exec(ctx, `SELECT set_config('app.bypass_tenant', 'on', false)`); err != nil {
		t.Fatal(err)
	}
	// A fresh logical database of the binary's redis:7-alpine container (compose image and flags) per
	// harness, under the process keyspace lt:local:, which the shared rate limiter requires (R26).
	h.rdb, h.ks = cachetest.NewClient(t)
	h.prefix = h.ks.Prefix()
	h.limiter, h.limits = ratelimit.New(h.rdb, h.ks, zerolog.Nop()), prometheus.NewRegistry()
	if err := h.limiter.Register(h.limits); err != nil {
		t.Fatal(err)
	}
	sp, err := firebasescrypt.ParseParams("jxspr8Ki0RYycVU8zykbdLGjFQ3McFUH0uiiTvC8pVMXAn210wjLNmdZJzxUECKbm0QsEmYUSDzZvpjeJ9WmXA==",
		"Bw==", 8, 14)
	if err != nil {
		t.Fatal(err)
	}
	h.scrypt = *sp
	cfg := auth.Config{
		RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour, PasswordResetTTL: 30 * time.Minute,
		Scrypt: sp, RateLimitEnabled: true, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute},
	}
	for _, o := range opts {
		o(&cfg)
	}
	h.svc, h.keys = h.service(cfg, h.rdb)
	h.serve(h.svc)
	return h
}

// redisOptions addresses the harness's Redis database, for a second client with its own hooks.
func (h *harness) redisOptions() *redis.Options {
	o := h.rdb.Options()
	return &redis.Options{Addr: o.Addr, DB: o.DB}
}

// service builds an auth.Service on the harness database with its own key set and Redis client.
func (h *harness) service(cfg auth.Config, rdb redis.UniversalClient) (*auth.Service, *token.KeySet) {
	h.t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		h.t.Fatal(err)
	}
	keys := token.New(priv, issuer, audience, 15*time.Minute)
	if h.keys != nil {
		keys = h.keys // a second service of the same harness verifies the same tokens
	}
	hasher, err := password.NewHasher(fast)
	if err != nil {
		h.t.Fatal(err)
	}
	policy, err := password.NewPolicy(10)
	if err != nil {
		h.t.Fatal(err)
	}
	limiter := h.limiter
	if rdb != redis.UniversalClient(h.rdb) {
		limiter = ratelimit.New(rdb, h.ks, zerolog.Nop()) // a replica with a Redis connection of its own
	}
	svc, err := auth.New(cfg, auth.Deps{
		Pool: h.pool, Store: auth.NewStore(rdb, h.prefix), Limiter: limiter,
		Keys: keys, Hasher: hasher, Policy: policy, Google: h.google, Log: zerolog.Nop(), Now: h.clock.Now,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(svc.Close) // stops the post-commit retries before the Redis client closes
	return svc, keys
}

// serve runs both listeners of the api process with the auth route groups.
func (h *harness) serve(svc *auth.Service) {
	h.t.Helper()
	cfg := &app.APIConfig{
		AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1,
		MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second,
		InternalAddr: "127.0.0.1:0", PublicAddr: "127.0.0.1:0", PublicRouteGroups: ingress.PublicPrefixes,
	}
	a, err := app.NewAPI(cfg, zerolog.Nop(), svc.Groups()...)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := a.Listen(); err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Serve(ctx); close(done) }()
	h.t.Cleanup(func() {
		cancel()
		<-done
	})
	in, pub, _ := a.Addrs()
	h.internal, h.public = "http://"+in, "http://"+pub
}

// --- fixtures (logitrack_etl: BYPASSRLS) -----------------------------------------------------------

func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.etl.Exec(context.Background(), sql, args...); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

func scalar[T any](h *harness, sql string, args ...any) T {
	h.t.Helper()
	var v T
	if err := h.etl.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func (h *harness) tenant(kind, name string) string {
	h.t.Helper()
	if kind == "carrier" {
		return scalar[string](h, `INSERT INTO tenants (kind, name_th, legal_type, contractor_tenant_id)
			VALUES ('carrier', $1, 'company', (SELECT id FROM tenants WHERE kind = 'own_fleet')) RETURNING id::text`, name)
	}
	return scalar[string](h, `INSERT INTO tenants (kind, name_th, name_en) VALUES ($1, $2, $2) RETURNING id::text`, kind, name)
}

// party creates a customer and its billing party.
func (h *harness) party(code string) string {
	h.t.Helper()
	c := scalar[string](h, `INSERT INTO customers (code, name) VALUES ($1::text, $1::text || ' Co.') RETURNING id::text`, code)
	return scalar[string](h, `INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, c)
}

func (h *harness) hash(p string) string {
	h.t.Helper()
	hasher, _ := password.NewHasher(fast)
	s, err := hasher.Hash(context.Background(), p)
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

// user inserts an active user with password pw.
func (h *harness) user(email string) string {
	h.t.Helper()
	return scalar[string](h, `INSERT INTO users (email, email_verified, display_name, password_hash)
		VALUES ($1::text, true, $1::text, $2) RETURNING id::text`, email, h.hash(pw))
}

func (h *harness) member(user, tenant, role string) {
	h.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, user, tenant, role)
}

func (h *harness) driver(user, tenant, mobile string) string {
	h.t.Helper()
	return scalar[string](h, `INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, mobile)
		VALUES ($1, 'self', $2, 'สมชาย', 'ใจดี', $3) RETURNING id::text`, tenant, user, mobile)
}

// --- HTTP ---------------------------------------------------------------------------------------------

type resp struct {
	status int
	header http.Header
	raw    []byte
	body   map[string]any
}

func (r resp) data() map[string]any {
	m, _ := r.body["data"].(map[string]any)
	return m
}

func (r resp) code() string {
	e, _ := r.body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func (r resp) details() map[string]any {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	return d
}

func (r resp) str(key string) string {
	s, _ := r.data()[key].(string)
	return s
}

// call sends a JSON request to base+path. Every error response is checked against the R48 envelope:
// exactly {"error":{"code","message","details","requestId"}} with requestId = X-Request-Id.
func (h *harness) call(base, method, path, bearer string, body any) resp {
	h.t.Helper()
	out, err := h.do(base, method, path, bearer, body)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	if out.status >= 400 {
		checkEnvelope(h.t, out)
	}
	return out
}

// do is call without the test assertions, safe from any goroutine.
func (h *harness) do(base, method, path, bearer string, body any) (resp, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return resp{}, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		return resp{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "auth-integration-test")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return resp{}, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: raw}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			return out, fmt.Errorf("not JSON: %s", raw)
		}
	}
	return out, nil
}

func checkEnvelope(t *testing.T, r resp) {
	t.Helper()
	if len(r.body) != 1 {
		t.Fatalf("error body must have exactly the error key: %s", r.raw)
	}
	e, ok := r.body["error"].(map[string]any)
	if !ok || len(e) != 4 {
		t.Fatalf("error envelope must have exactly four fields: %s", r.raw)
	}
	for _, k := range []string{"code", "message", "requestId"} {
		if s, _ := e[k].(string); s == "" {
			t.Fatalf("error.%s missing: %s", k, r.raw)
		}
	}
	if _, ok := e["details"].(map[string]any); !ok {
		t.Fatalf("error.details must be an object: %s", r.raw)
	}
	if e["requestId"] != r.header.Get("X-Request-Id") {
		t.Fatalf("requestId differs from X-Request-Id: %s", r.raw)
	}
	if strings.Contains(string(r.raw), "accessToken") || strings.Contains(string(r.raw), "refreshToken") {
		t.Fatalf("an error carries a token: %s", r.raw)
	}
}

func (h *harness) post(path, bearer string, body any) resp {
	h.t.Helper()
	return h.call(h.internal, http.MethodPost, path, bearer, body)
}

func (h *harness) get(path, bearer string) resp {
	h.t.Helper()
	return h.call(h.internal, http.MethodGet, path, bearer, nil)
}

type session struct {
	access, refresh string
	sid             string
}

func (h *harness) login(email, password, platform, install string) resp {
	h.t.Helper()
	body := map[string]any{"email": email, "password": password, "platform": platform}
	if install != "" {
		body["installId"] = install
	}
	return h.post("/v1/auth/login", "", body)
}

// mustLogin logs in and returns the session tokens.
func (h *harness) mustLogin(email, platform, install string) session {
	h.t.Helper()
	r := h.login(email, pw, platform, install)
	if r.status != http.StatusOK {
		h.t.Fatalf("login %s: %d %s", email, r.status, r.raw)
	}
	s := session{access: r.str("accessToken"), refresh: r.str("refreshToken")}
	s.sid, _ = payload(h.t, s.access)["sid"].(string)
	return s
}

func (h *harness) refresh(rt string) resp {
	h.t.Helper()
	return h.post("/v1/auth/refresh", "", map[string]any{"refreshToken": rt})
}

func (h *harness) mustRefresh(s session) session {
	h.t.Helper()
	r := h.refresh(s.refresh)
	if r.status != http.StatusOK {
		h.t.Fatalf("refresh: %d %s", r.status, r.raw)
	}
	return session{access: r.str("accessToken"), refresh: r.str("refreshToken"), sid: s.sid}
}

func expectError(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status || r.code() != code {
		t.Fatalf("want %d %s, got %d %s", status, code, r.status, r.raw)
	}
}

// payload decodes the claims of a JWT without verifying it.
func payload(t *testing.T, jwt string) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", jwt)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func header(t *testing.T, jwt string) map[string]any {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(strings.Split(jwt, ".")[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func sha(v string) []byte {
	s := sha256.Sum256([]byte(v))
	return s[:]
}

// outbox returns the payloads of the outbox rows with routing key rk, oldest first.
func (h *harness) outbox(rk string) []map[string]any {
	h.t.Helper()
	rows, err := h.etl.Query(context.Background(), `SELECT payload FROM outbox_events WHERE routing_key = $1 ORDER BY id`, rk)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []map[string]any
	for rows.Next() {
		var m map[string]any
		if err := rows.Scan(&m); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// counterValue sums every series of the counter family name in reg (0 when it has none yet).
func counterValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	mf, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var v float64
	for _, f := range mf {
		if f.GetName() == name {
			for _, m := range f.GetMetric() {
				v += m.GetCounter().GetValue()
			}
		}
	}
	return v
}
