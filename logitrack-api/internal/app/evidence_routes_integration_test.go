//go:build integration

package app_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// The evidence gallery as cmd/api mounts it (app.StorageGroups, T49): GET /evidence/{token} on the public listener
// (and the internal one), spending the evidence_ip budget that RATE_LIMIT_EVIDENCE sets per client address.
func TestEvidenceGalleryRoutes(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	rdb := asynctest.SharedRedis(t).Client(t)
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := password.NewHasher(password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1})
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
	local, err := storage.NewLocal(storage.LocalConfig{Dir: t.TempDir(), PublicBaseURL: "http://media.test/media",
		SigningKey: bytes.Repeat([]byte("e"), 40)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	st, err := storage.New(pool, storage.Config{Active: storage.BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public",
		PresignGetTTL: time.Hour, EvidencePresignTTL: 15 * time.Minute}, zerolog.Nop(), local)
	if err != nil {
		t.Fatal(err)
	}
	limiter := ratelimit.New(rdb, ks, zerolog.Nop())
	svc, err := auth.New(auth.Config{RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour,
		PasswordResetTTL: 30 * time.Minute}, auth.Deps{Pool: pool, Store: auth.NewStore(rdb, ks.Prefix()), Limiter: limiter,
		Keys: token.New(priv, "http://localhost", "test", 15*time.Minute), Hasher: hasher, Policy: policy, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	// RATE_LIMIT_EVIDENCE=3/1m.
	deps := app.APIDeps{Auth: svc, Storage: st, Limiter: limiter, RateLimitEnabled: true,
		EvidenceLimit: ratelimit.Limit{Count: 3, Window: time.Minute}}
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		InternalAddr: "127.0.0.1:0", PublicAddr: "127.0.0.1:0", PublicRouteGroups: ingress.PublicPrefixes,
	}
	a, err := app.NewAPI(cfg, zerolog.Nop(), append(svc.Groups(), app.StorageGroups(deps)...)...)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CheckRoutes(); err != nil {
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

	var tenant string
	if err := d.Pool(t, db.RoleETL).QueryRow(ctx, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'Own') RETURNING id::text`).
		Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	tok, err := storage.NewEvidenceToken(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Pool(t, db.RoleETL).Exec(ctx, `INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type,
		evidence_token) VALUES ($1, 'form', 'TR-RL-1', 'delivered', 'first_mile', $2)`, tenant, tok); err != nil {
		t.Fatal(err)
	}

	// Three pages a minute per client address (both listeners count against it), whatever the token says; the fourth
	// is 429. The gallery is served on the public listener and on the internal one, as every public group.
	for i, c := range []struct {
		base, path string
		want       int
	}{
		{"http://" + in, "/evidence/" + tok, http.StatusOK},
		{"http://" + pub, "/evidence/unknown", http.StatusNotFound},
		{"http://" + pub, "/evidence", http.StatusBadRequest},
		{"http://" + pub, "/evidence/" + tok, http.StatusTooManyRequests},
	} {
		if s, h, b := raw(t, http.MethodGet, c.base+c.path, "", nil, ""); s != c.want {
			t.Fatalf("request %d %s: %d %v %s", i+1, c.path, s, h, b)
		}
	}
}
