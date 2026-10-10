//go:build integration

// The jobs routes as cmd/api mounts them (T10): JobGroups behind the real auth.RequireAuth of T05, so
// no bearer is 401, a tenant user sees only own jobs, and only platform_admin replays a dead queue.
package app_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) {
	code := pgtest.Main(m)
	asynctest.Terminate()
	os.Exit(code)
}

const testPassword = "a long passphrase 1"

type call struct {
	status int
	body   map[string]any
}

func (c call) code() string {
	e, _ := c.body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func do(t *testing.T, method, url, bearer string, body any) call {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out call
	out.status = resp.StatusCode
	_ = json.NewDecoder(resp.Body).Decode(&out.body)
	return out
}

func TestJobRoutesBehindAuth(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	rdb := asynctest.SharedRedis(t).Client(t)
	params := password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1}
	hasher, err := password.NewHasher(params)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := password.NewPolicy(10)
	if err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(auth.Config{RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour,
		PasswordResetTTL: 30 * time.Minute, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute}},
		auth.Deps{Pool: pool, Store: auth.NewStore(rdb, "lt:local:"), Limiter: ratelimit.New(rdb, ks, zerolog.Nop()), Keys: token.New(priv, "http://localhost", "test", 15*time.Minute),
			Hasher: hasher, Policy: policy, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	deps := app.APIDeps{Auth: svc, Jobs: jobs.NewService(pool, jobs.NewRedisLocker(rdb, "lt:local:"))}
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		InternalAddr: "127.0.0.1:0", PublicAddr: "127.0.0.1:0", PublicRouteGroups: ingress.PublicPrefixes,
	}
	a, err := app.NewAPI(cfg, zerolog.Nop(), append(svc.Groups(), app.JobGroups(deps)...)...)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Listen(); err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = a.Serve(sctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	in, pub, _ := a.Addrs()
	internal, public := "http://"+in, "http://"+pub

	// Fixtures as the system context (cmd/seed's path).
	etl, err := pgx.Connect(ctx, d.URL(db.RoleETL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = etl.Close(ctx) }()
	if _, err := etl.Exec(ctx, `SELECT set_config('app.bypass_tenant', 'on', false)`); err != nil {
		t.Fatal(err)
	}
	hash, err := hasher.Hash(ctx, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	var tenant, admin, alice string
	if err := etl.QueryRow(ctx, `INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'Own', 'Own') RETURNING id::text`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct {
		email string
		id    *string
	}{{"admin@logitrack.test", &admin}, {"alice@logitrack.test", &alice}} {
		if err := etl.QueryRow(ctx, `INSERT INTO users (email, email_verified, display_name, password_hash) VALUES ($1::text, true, $1::text, $2)
			RETURNING id::text`, u.email, hash).Scan(u.id); err != nil {
			t.Fatal(err)
		}
		if _, err := etl.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'operator')`, *u.id, tenant); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := etl.Exec(ctx, `INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, admin); err != nil {
		t.Fatal(err)
	}
	login := func(email string) string {
		t.Helper()
		r := do(t, http.MethodPost, internal+"/v1/auth/login", "", map[string]any{"email": email, "password": testPassword, "platform": "web"})
		at, _ := r.body["data"].(map[string]any)["accessToken"].(string)
		if r.status != http.StatusOK || at == "" {
			t.Fatalf("login %s: %d %v", email, r.status, r.body)
		}
		return at
	}
	adminAT, aliceAT := login("admin@logitrack.test"), login("alice@logitrack.test")

	if r := do(t, http.MethodGet, internal+"/v1/jobs", "", nil); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
		t.Fatalf("no bearer: %d %v", r.status, r.body)
	}
	if r := do(t, http.MethodGet, public+"/v1/jobs", aliceAT, nil); r.status != http.StatusNotFound {
		t.Fatalf("/v1/jobs on the public listener: %d, want 404", r.status)
	}
	if r := do(t, http.MethodPost, internal+"/v1/admin/queues/notify.email/replay", aliceAT, nil); r.status != http.StatusForbidden ||
		r.code() != "permission_denied" {
		t.Fatalf("tenant user replays: %d %v", r.status, r.body)
	}
	r := do(t, http.MethodPost, internal+"/v1/admin/queues/notify.email/replay", adminAT, nil)
	jobID, _ := r.body["data"].(map[string]any)["jobId"].(string)
	if r.status != http.StatusAccepted || jobID == "" {
		t.Fatalf("platform_admin replays: %d %v", r.status, r.body)
	}
	if r := do(t, http.MethodGet, internal+"/v1/jobs/"+jobID, aliceAT, nil); r.status != http.StatusNotFound {
		t.Fatalf("alice reads the admin's job: %d", r.status)
	}
	if r := do(t, http.MethodGet, internal+"/v1/jobs/"+jobID, adminAT, nil); r.status != http.StatusOK ||
		r.body["data"].(map[string]any)["type"] != "queue.replay" {
		t.Fatalf("admin reads own job: %d %v", r.status, r.body)
	}
	if r := do(t, http.MethodGet, internal+"/v1/jobs", aliceAT, nil); r.status != http.StatusOK || len(r.body["data"].([]any)) != 0 {
		t.Fatalf("alice's list: %d %v", r.status, r.body)
	}
}
