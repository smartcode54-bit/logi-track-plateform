//go:build integration

// Integration tests of T52 (security-center slice, issue #72): PostgreSQL 18 with the full goose chain (requests as
// logitrack_app, so RLS decides), Redis 7, the real auth service and RBAC, the API keys administration (the
// release_publisher key is created through POST /v1/api-keys), the local storage backend, and the route groups
// exactly as cmd/api wires them (app.MobileGroups, app.MobileReleaseGroups, app.StorageGroups) on both listeners.
package mobileapp_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/apikey"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const pw = "a long passphrase 1"

// testPepper is the API_KEY_PEPPER of the world: a placeholder built at run time.
var testPepper = strings.Repeat("pepper-placeholder-", 3)

func TestMain(m *testing.M) {
	app.DrainGrace = 0
	code := pgtest.Main(m)
	cachetest.TerminateShared()
	os.Exit(code)
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type world struct {
	t        *testing.T
	pool     *pgxpool.Pool
	etl      *pgx.Conn
	mobile   *mobileapp.Service
	caches   *cache.Cache
	internal string
	public   string
	mediaDir string
	logs     *syncBuffer
	hash     string
	now      time.Time

	O, A, C string // own fleet; carrier A working for O; independent carrier C
	D1, D2  string // drivers of O and A (users d1@, d2@)
	D3      string // a driver of C
}

type worldOpts struct {
	rateLimit bool
	// trusted is TRUSTED_PROXY_CIDRS (the VM trusts 127.0.0.1/32: nginx and the BFF).
	trusted []netip.Prefix
}

func newWorld(t *testing.T) *world { return newWorldOpts(t, worldOpts{}) }

func newWorldOpts(t *testing.T, o worldOpts) *world {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, pool: d.Pool(t, db.RoleApp), logs: &syncBuffer{}, mediaDir: t.TempDir(), now: time.Now().UTC()}
	logger := zerolog.New(w.logs).Level(zerolog.DebugLevel)
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

	rdb, ks := cachetest.NewClient(t)
	w.caches = cache.New(rdb, ks)
	rbac, err := iam.NewRBAC(iam.Deps{Pool: w.pool, Cache: w.caches, Redis: rdb, Log: logger})
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
	// STORAGE_BACKEND=local (the staging VM's backend); LOCAL_MEDIA_PUBLIC_BASE_URL reaches /media on the public
	// listener, so the APK link and cmd/release's URL check go through the api like on the VM.
	publicAddr := freeAddr(t)
	local, err := storage.NewLocal(storage.LocalConfig{Dir: w.mediaDir, PublicBaseURL: "http://" + publicAddr + "/media",
		SigningKey: bytes.Repeat([]byte("e"), 40), APIUploadPath: storage.LocalUploadPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	const uploadMax = 1 << 20
	st, err := storage.New(w.pool, storage.Config{Active: storage.BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public",
		PresignPutTTL: 15 * time.Minute, PresignGetTTL: time.Hour, UploadMaxBytes: uploadMax}, logger, local)
	if err != nil {
		t.Fatal(err)
	}
	limiter := ratelimit.New(rdb, ks, logger)
	svc, err := auth.New(auth.Config{
		RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour, PasswordResetTTL: 30 * time.Minute,
		RateLimitEnabled: o.rateLimit, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute}, APIKeyPepper: pepper,
	}, auth.Deps{
		Pool: w.pool, Store: auth.NewStore(rdb, ks.Prefix()), Limiter: limiter,
		Keys: token.New(priv, "http://localhost:8080", "logitrack-test", 15*time.Minute), Hasher: hasher,
		Policy: policy, Log: logger, Capabilities: rbac, Authorizer: rbac, Files: st,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	keys, err := iam.NewAPIKeys(iam.APIKeysDeps{Pool: w.pool, Pepper: pepper, Log: logger})
	if err != nil {
		t.Fatal(err)
	}
	if w.mobile, err = mobileapp.New(mobileapp.Deps{Pool: w.pool, Storage: st, Cache: w.caches, Log: logger}); err != nil {
		t.Fatal(err)
	}
	deps := app.APIDeps{Auth: svc, Storage: st, Mobile: w.mobile, Limiter: limiter, RateLimitEnabled: o.rateLimit}
	groups := append(svc.Groups(), iam.APIKeyGroups(keys, svc.RequireAuth())...)
	groups = append(groups, app.StorageGroups(deps)...)
	groups = append(groups, app.MobileGroups(deps)...)
	groups = append(groups, app.MobileReleaseGroups(deps)...)
	groups = append(groups, ingress.Group{Prefix: "/v1/mobiletest", Mount: w.testRoutes(svc.RequireAuth())})
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		StorageAPI:   app.StorageAPI{UploadMaxBytes: uploadMax},
		InternalAddr: "127.0.0.1:0", PublicAddr: publicAddr, PublicRouteGroups: ingress.PublicPrefixes, TrustedProxies: o.trusted,
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

// --- fixture (logitrack_etl, BYPASSRLS) ----------------------------------------------------------------------------

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

func (w *world) str(sql string, args ...any) string {
	w.t.Helper()
	var v *string
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	if v == nil {
		return ""
	}
	return *v
}

func (w *world) user(email string) string {
	return w.id(`INSERT INTO users (email, email_verified, display_name, password_hash)
		VALUES ($1::text, true, $1::text, $2) RETURNING id::text`, email, w.hash)
}

func (w *world) member(user, tenant, role string) {
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, user, tenant, role)
}

// driver creates a driver of tenant linked to a new user with a driver membership (C.1.4).
func (w *world) driver(email, tenant, nameTh string) string {
	u := w.user(email)
	w.member(u, tenant, "driver")
	return w.id(`INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, full_name_th, mobile)
		VALUES ($1, 'self', $2, 'First', 'Last', $3, '0800000001') RETURNING id::text`, tenant, u, nameTh)
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'โอ', 'O') RETURNING id::text`)
	w.A = w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type, contractor_tenant_id)
		VALUES ('carrier', 'เอ', 'A', 'company', $1) RETURNING id::text`, w.O)
	w.C = w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type) VALUES ('carrier', 'ซี', 'C', 'company') RETURNING id::text`)
	for _, u := range []struct{ email, tenant, role string }{
		{"ta.o@example.test", w.O, "tenant_admin"}, {"op.o@example.test", w.O, "operator"},
		{"ta.a@example.test", w.A, "tenant_admin"}, {"ta.c@example.test", w.C, "tenant_admin"},
	} {
		w.member(w.user(u.email), u.tenant, u.role)
	}
	w.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, w.user("pa@example.test"))
	w.D1 = w.driver("d1@example.test", w.O, "สมชาย ใจดี")
	w.D2 = w.driver("d2@example.test", w.A, "สมหญิง ใจงาม")
	w.D3 = w.driver("d3@example.test", w.C, "")
}

// installation inserts a heartbeat row directly (the system context).
func (w *world) installation(driver, tenant, install, version, flavor string, seen time.Time) {
	w.exec(`INSERT INTO mobile_installations (driver_id, install_id, tenant_id, platform, app_version, flavor, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, 'android', nullif($4, ''), $5, $6, $6)`, driver, install, tenant, version, flavor, seen)
}

// --- test routes: raw SQL under the request principal (RLS alone decides) -------------------------------------------

func (w *world) testRoutes(requireAuth fiber.Handler) func(r fiber.Router) {
	return func(r fiber.Router) {
		r.Use(requireAuth)
		// The driver writes another driver's installation directly: an UPDATE (0 rows) and an INSERT (refused).
		r.Post("/write-other", func(c fiber.Ctx) error {
			var updated int64
			insertState := ""
			err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
				tag, err := tx.Exec(c.Context(), `UPDATE mobile_installations SET app_version = '0.0.1' WHERE driver_id = $1`, c.Query("driver"))
				if err != nil {
					return err
				}
				updated = tag.RowsAffected()
				sp, err := tx.Begin(c.Context())
				if err != nil {
					return err
				}
				_, err = sp.Exec(c.Context(), `INSERT INTO mobile_installations (driver_id, install_id, tenant_id, platform, flavor)
					VALUES ($1, 'planted', $2, 'android', 'prod')`, c.Query("driver"), c.Query("tenant"))
				var pe *pgconn.PgError
				if errors.As(err, &pe) {
					insertState = pe.Code
				}
				return sp.Rollback(c.Context())
			})
			if err != nil {
				return err
			}
			return httpx.JSON(c, http.StatusOK, map[string]any{"updated": updated, "insertState": insertState})
		})
	}
}

// --- HTTP ----------------------------------------------------------------------------------------------------------

type resp struct {
	status int
	raw    []byte
	body   map[string]any
	header http.Header
}

func (r resp) code() string {
	e, _ := r.body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func (r resp) reason() string {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	s, _ := d["reason"].(string)
	return s
}

func (r resp) data() map[string]any {
	m, _ := r.body["data"].(map[string]any)
	return m
}

func (r resp) list() []any {
	l, _ := r.body["data"].([]any)
	return l
}

func (r resp) meta() map[string]any {
	m, _ := r.body["meta"].(map[string]any)
	return m
}

// call sends a request; auth is a bearer token, or "key:<secret>" for X-Api-Key, or "".
func (w *world) call(base, method, path, auth string, body any, headers ...string) resp {
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
	switch {
	case strings.HasPrefix(auth, "key:"):
		req.Header.Set("X-Api-Key", strings.TrimPrefix(auth, "key:"))
	case auth != "":
		req.Header.Set("Authorization", "Bearer "+auth)
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
	out := resp{status: res.StatusCode, raw: raw, header: res.Header}
	if len(raw) > 0 && strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			w.t.Fatalf("%s %s: not JSON: %s", method, path, raw)
		}
	}
	return out
}

func (w *world) get(path, auth string, headers ...string) resp {
	w.t.Helper()
	return w.call(w.internal, http.MethodGet, path, auth, nil, headers...)
}

// login signs in through POST /v1/auth/login; a driver signs in from the app (platform android, an install id).
func (w *world) login(email string) string {
	w.t.Helper()
	body := map[string]any{"email": email, "password": pw, "platform": "web"}
	if strings.HasPrefix(email, "d") {
		body["platform"], body["installId"] = "android", "login-"+email
	}
	r := w.call(w.public, http.MethodPost, "/v1/auth/login", "", body)
	tok, _ := r.data()["accessToken"].(string)
	if r.status != http.StatusOK || tok == "" {
		w.t.Fatalf("login %s: %d %s", email, r.status, r.raw)
	}
	return tok
}

// releaseKey creates a release_publisher key through POST /v1/api-keys as the platform admin and returns its secret.
func (w *world) releaseKey(pa string) string {
	w.t.Helper()
	return w.key(pa, map[string]any{"name": "release-vm", "scope": "release_publisher", "capabilities": []string{"security:manage_mobile_release"}})
}

func (w *world) key(bearer string, body map[string]any) string {
	w.t.Helper()
	r := w.call(w.internal, http.MethodPost, "/v1/api-keys", bearer, body)
	secret, _ := r.data()["secret"].(string)
	if r.status != http.StatusCreated || secret == "" {
		w.t.Fatalf("create key: %d %s", r.status, r.raw)
	}
	return secret
}

func expect(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status || (code != "" && r.code() != code) {
		t.Fatalf("want %d %s, got %d %s", status, code, r.status, r.raw)
	}
}

// settingsRow seeds settings('mobile_app') with a legacy-shaped document (Appendix D §D.4.12).
func (w *world) settingsRow(doc map[string]any) {
	b, _ := json.Marshal(doc)
	w.exec(`INSERT INTO settings (key, value) VALUES ('mobile_app', $1::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, string(b))
	if err := w.caches.Invalidate(context.Background(), w.caches.Keys().Settings(mobileapp.SettingsKey)); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) settingsDoc() map[string]any {
	w.t.Helper()
	var raw []byte
	if err := w.etl.QueryRow(context.Background(), `SELECT value FROM settings WHERE key = 'mobile_app'`).Scan(&raw); err != nil {
		w.t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}
