//go:build integration

// Acceptance tests of issue T16 (#36) against PostgreSQL 18 (pgtest, the R66 roles), Redis 7 (cachetest) and
// MinIO (storagetest): seed --profile smoke and demo load through ETL_DATABASE_URL and pass --verify; two runs
// give the same fingerprint; --verify role-plays isolation through DATABASE_URL and fails when a carrier
// principal can read another tenant's rows (R87); SEED_DEFAULT_PASSWORD never reaches the output.
package main

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/cmd/seed/internal/seed"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) {
	code := pgtest.Main(m)
	cachetest.TerminateShared()
	storagetest.Terminate()
	os.Exit(code)
}

// testPassword is a test-only SEED_DEFAULT_PASSWORD; the tests assert it never reaches the output.
const testPassword = "seed-test-Pa55word-not-secret"

// redisDB hands every test its own logical Redis database.
var redisDB atomic.Int64

// stack is one migrated database with Redis and an object store, and the environment cmd/seed reads.
type stack struct {
	d   *pgtest.Database
	env []string
	rdb *redis.Client
}

// newStack migrates a fresh database and prepares the store of backend ("s3" on MinIO, or "local").
func newStack(t *testing.T, backend string, extra ...string) *stack {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	r := cachetest.Shared(t)
	redisURL := r.URL(int(redisDB.Add(1)))
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	t.Cleanup(func() { _ = rdb.Close() })
	env := []string{
		"APP_ENV=local", "LOG_LEVEL=warn", "LOG_FORMAT=json",
		"ETL_DATABASE_URL=" + d.URL(db.RoleETL), "MIGRATE_DATABASE_URL=" + d.URL(db.RoleMigrator),
		"DATABASE_URL=" + d.URL(db.RoleApp), "REDIS_URL=" + redisURL,
		"SEED_DEFAULT_PASSWORD=" + testPassword, "SEED_RANDOM_SEED=20261009",
		// Argon2id below the production cost keeps the race-instrumented tests fast; the hash format is the same.
		"ARGON2_MEMORY_KB=8192", "ARGON2_ITERATIONS=1", "ARGON2_PARALLELISM=1",
		// The public firebase/scrypt test vectors (Appendix C §C.5.3), never production values.
		"FIREBASE_SCRYPT_SIGNER_KEY=" + publicScryptSigner(t),
		"FIREBASE_SCRYPT_SALT_SEPARATOR=Bw==", "FIREBASE_SCRYPT_ROUNDS=8", "FIREBASE_SCRYPT_MEM_COST=14",
	}
	switch backend {
	case "s3":
		cfg := storagetest.Shared(t).Config("")
		s3, err := storage.NewS3(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := s3.Bootstrap(ctx, zerolog.Nop()); err != nil {
			t.Fatal(err)
		}
		env = append(env, "STORAGE_BACKEND=s3", "S3_ENDPOINT="+cfg.Endpoint, "S3_REGION="+cfg.Region,
			"S3_ACCESS_KEY_ID="+cfg.AccessKeyID, "S3_SECRET_ACCESS_KEY="+cfg.SecretAccessKey, "S3_USE_PATH_STYLE=true",
			"S3_BUCKET="+cfg.Bucket, "S3_PUBLIC_BUCKET="+cfg.PublicBucket, "S3_PUBLIC_BASE_URL="+cfg.PublicBaseURL)
	case "local":
		env = append(env, "STORAGE_BACKEND=local", "LOCAL_MEDIA_DIR="+t.TempDir(),
			"LOCAL_MEDIA_PUBLIC_BASE_URL=http://localhost:8081/media")
	default:
		t.Fatalf("backend %q", backend)
	}
	return &stack{d: d, env: append(env, extra...), rdb: rdb}
}

// publicScryptSigner is the signer of the "01-known-value" set of the public firebase/scrypt vectors that
// internal/auth/firebasescrypt tests with (read from its testdata, so no key-shaped literal sits here).
func publicScryptSigner(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../internal/auth/firebasescrypt/testdata/public-vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "set" && f[1] == "01-known-value" {
			return f[2]
		}
	}
	t.Fatal("no 01-known-value set in the public scrypt vectors")
	return ""
}

// result is one cmd/seed run.
type result struct {
	code           int
	stdout, stderr string
	took           time.Duration
}

func (s *stack) seed(t *testing.T, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	start := time.Now()
	code := run(context.Background(), args, s.env, &out, &errb)
	r := result{code: code, stdout: out.String(), stderr: errb.String(), took: time.Since(start)}
	if strings.Contains(r.stdout, testPassword) || strings.Contains(r.stderr, testPassword) {
		t.Fatalf("seed %v printed SEED_DEFAULT_PASSWORD", args)
	}
	return r
}

func (s *stack) mustSeed(t *testing.T, args ...string) result {
	t.Helper()
	r := s.seed(t, args...)
	if r.code != 0 {
		t.Fatalf("seed %v: exit %d\nstdout:\n%s\nstderr:\n%s", args, r.code, r.stdout, r.stderr)
	}
	return r
}

// fingerprintBlock is the last block of --verify.
func fingerprintBlock(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "fingerprint:\n")
	if i < 0 {
		t.Fatalf("no fingerprint block in\n%s", out)
	}
	return out[i:]
}

var passLine = regexp.MustCompile(`(?m)^ ?\d+ PASS `)

// The smoke profile loads in under five seconds, passes the twelve invariants, and a second run gives the
// same ids, counts and fingerprint (Appendix D §D.1.7, invariant 12).
func TestSmokeLoadVerifyTwice(t *testing.T) {
	var argon []string
	if !raceEnabled {
		// The production cost of .env.example, so the five-second budget includes real Argon2id hashing.
		argon = []string{"ARGON2_MEMORY_KB=65536", "ARGON2_ITERATIONS=3", "ARGON2_PARALLELISM=2"}
	}
	s := newStack(t, "s3", argon...)
	first := s.mustSeed(t, "--profile", "smoke")
	t.Logf("smoke load: %s (%s)", first.took, strings.TrimSpace(first.stdout))
	if !raceEnabled && first.took >= 5*time.Second {
		t.Errorf("seed --profile smoke took %s, want < 5s", first.took)
	}
	if !strings.Contains(first.stdout, "286") && !strings.Contains(first.stdout, "285 rows") {
		t.Errorf("load summary does not report the 285 seeded rows:\n%s", first.stdout)
	}
	if !strings.Contains(first.stdout, "temporary password of d7.kitti@logitrack.test") {
		t.Errorf("the temporary password of the must-change-password user is not shown once:\n%s", first.stdout)
	}
	v1 := s.mustSeed(t, "--verify")
	if n := len(passLine.FindAllString(v1.stdout, -1)); n != 12 {
		t.Fatalf("%d of 12 invariants pass:\n%s", n, v1.stdout)
	}
	if !strings.Contains(v1.stdout, "counts: 59 tables match the smoke manifest") {
		t.Errorf("count diff missing or failing:\n%s", v1.stdout)
	}
	s.mustSeed(t, "--profile", "smoke")
	v2 := s.mustSeed(t, "--verify")
	if a, b := fingerprintBlock(t, v1.stdout), fingerprintBlock(t, v2.stdout); a != b {
		t.Fatalf("two runs differ:\n%s\nvs\n%s", a, b)
	}
}

// The demo profile shows the own fleet and two carriers at once (owner addition to issue #36) and passes
// the invariants on the local disk backend; every carrier principal is role-played.
func TestDemoLoadVerify(t *testing.T) {
	s := newStack(t, "local")
	s.mustSeed(t, "--profile", "demo")
	v := s.mustSeed(t, "--verify", "--profile", "demo")
	t.Log("\n" + v.stdout)
	if n := len(passLine.FindAllString(v.stdout, -1)); n != 12 {
		t.Fatalf("%d of 12 invariants pass:\n%s", n, v.stdout)
	}
	// NWR: tenant_admin, manager, operation_staff, operator; TTP: tenant_admin, operation_staff.
	if !strings.Contains(v.stdout, "6 carrier principal(s)") {
		t.Errorf("the demo carriers are not all role-played:\n%s", v.stdout)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var perTenant string
	if err := conn.QueryRow(ctx, `SELECT string_agg(t.code || ':' || m.role, ',' ORDER BY t.code, m.role)
		FROM (SELECT DISTINCT tenant_id, role FROM memberships WHERE status = 'active') m JOIN tenants t ON t.id = m.tenant_id`).
		Scan(&perTenant); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NWR:tenant_admin", "NWR:operation_staff", "TTP:tenant_admin", "TTP:operation_staff",
		"WRT:tenant_admin", "WRT:operation_staff"} {
		if !strings.Contains(perTenant, want) {
			t.Errorf("demo has no %s (have %s)", want, perTenant)
		}
	}
	var dispatchers, customers int
	if err := conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE kind = 'dispatcher'), count(*) FILTER (WHERE kind = 'customer')
		FROM user_scopes`).Scan(&dispatchers, &customers); err != nil {
		t.Fatal(err)
	}
	if dispatchers < 3 || customers < 3 {
		t.Errorf("demo has %d dispatcher and %d customer scopes, want a dispatcher per tenant and three customers", dispatchers, customers)
	}
}

// --verify fails (exit 1) when a carrier principal can read another tenant's rows: a permissive policy on
// tasks opens every task to logitrack_app (R87).
func TestVerifyFailsOnCarrierLeak(t *testing.T) {
	s := newStack(t, "local")
	s.mustSeed(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `CREATE POLICY seed_test_leak ON tasks FOR SELECT TO logitrack_app USING (true)`); err != nil {
		t.Fatal(err)
	}
	r := s.seed(t, "--verify")
	if r.code != exitViolation {
		t.Fatalf("exit %d, want %d:\n%s\n%s", r.code, exitViolation, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "10 FAIL") || !regexp.MustCompile(`carrier nwr\.admin@logitrack\.test .*other tenants in tasks`).MatchString(r.stdout) {
		t.Fatalf("the leak is not reported as an invariant 10 carrier violation:\n%s", r.stdout)
	}
}

// The seed writes only through ETL_DATABASE_URL: a load succeeds with DATABASE_URL unreachable, while
// --verify needs it for the role-play and answers exit 2 (dependency unreachable).
func TestLoadNeverUsesDatabaseURL(t *testing.T) {
	s := newStack(t, "local")
	s.env = append(s.env, "DATABASE_URL=postgres://logitrack_app:unused@127.0.0.1:1/none?sslmode=disable&connect_timeout=2")
	s.mustSeed(t)
	if r := s.seed(t, "--verify"); r.code != exitUnreachable {
		t.Fatalf("verify without DATABASE_URL: exit %d, want %d", r.code, exitUnreachable)
	}
}

// --reset keeps the quarantine row of migration 0002 and unlinks only keys under lt:{APP_ENV}:.
func TestResetKeepsQuarantineAndForeignKeys(t *testing.T) {
	s := newStack(t, "local")
	ctx := context.Background()
	for k, v := range map[string]string{"lt:local:cache:hubs:n2c": "x", "lt:local:rbac:ver": "3", "other:service:key": "y"} {
		if err := s.rdb.Set(ctx, k, v, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	s.mustSeed(t)
	if n := s.rdb.Exists(ctx, "lt:local:cache:hubs:n2c", "lt:local:rbac:ver").Val(); n != 0 {
		t.Errorf("%d keys under the prefix survived the reset", n)
	}
	if s.rdb.Get(ctx, "other:service:key").Val() != "y" {
		t.Error("a key outside lt:{APP_ENV}: was removed (FLUSHDB?)")
	}
	conn, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var quarantine, total int
	if err := conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE id = $1), count(*) FROM tenants`, seed.QuarantineTenantID).
		Scan(&quarantine, &total); err != nil {
		t.Fatal(err)
	}
	if quarantine != 1 || total != 4 {
		t.Fatalf("tenants after reset + load: quarantine %d, total %d; want 1 and 4", quarantine, total)
	}
}

// A seeded price the engine does not reproduce rolls the whole load back (Appendix D §D.1.4).
func TestEngineMismatchAbortsLoad(t *testing.T) {
	s := newStack(t, "local")
	ctx := context.Background()
	plan, err := seed.Build(ctx, seed.FixtureFS{FS: fixtures, Registry: "testdata/registry.json", Smoke: "testdata/smoke"},
		seed.Options{Profile: seed.ProfileSmoke, Bucket: "logitrack", PublicBucket: "logitrack-public", Backend: "local"})
	if err != nil {
		t.Fatal(err)
	}
	// A plan that is not materialized cannot be loaded: its credentials are unresolved.
	etl := s.d.Pool(t, db.RoleETL)
	if _, err := seed.Load(ctx, etl, plan, seed.ModeInsert); err == nil || !strings.Contains(err.Error(), "not resolved") {
		t.Fatalf("an unmaterialized plan loaded: %v", err)
	}
	for _, r := range plan.Tables["users"] {
		for _, col := range []string{"password_hash", "legacy_scrypt_hash", "legacy_scrypt_salt"} {
			if _, ok := r[col]; ok && r[col] != nil {
				r[col] = nil
			}
		}
	}
	for _, r := range plan.Tables["file_objects"] {
		if r.Str("status") != "missing_at_source" {
			r["size_bytes"], r["sha256"] = "1", strings.Repeat("0", 64)
		}
	}
	tr01 := plan.Symbols.MustID("TR01").String()
	for _, r := range plan.Tables["trip_billing_snapshots"] {
		if r.Str("trip_id") == tr01 {
			r["estimate_thb"] = "1131.00"
		}
	}
	_, err = seed.Load(ctx, etl, plan, seed.ModeInsert)
	if err == nil || !strings.Contains(err.Error(), "billing engine does not reproduce") || !strings.Contains(err.Error(), "estimate_thb stored 1131.00, engine 1130.00") {
		t.Fatalf("load error %v, want the engine mismatch of TR01", err)
	}
	var n int
	if err := etl.QueryRow(ctx, `SELECT count(*) FROM tenants`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d tenants after the aborted load, want only the quarantine row", n)
	}
}

// The load profile passes the legacy listUsers(1000) cap and the invariants.
func TestLoadProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("load profile in -short mode")
	}
	s := newStack(t, "local")
	r := s.mustSeed(t, "--profile", "load")
	t.Logf("load profile: %s", r.took)
	v := s.mustSeed(t, "--verify", "--profile", "load")
	if n := len(passLine.FindAllString(v.stdout, -1)); n != 12 {
		t.Fatalf("%d of 12 invariants pass:\n%s", n, v.stdout)
	}
	ctx := context.Background()
	var users int
	if err := s.d.Pool(t, db.RoleETL).QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users <= 1000 {
		t.Fatalf("%d users, want more than the legacy 1000-user listing cap", users)
	}
}

// Upsert on an already seeded database inserts nothing and keeps the fingerprint.
func TestUpsertIsIdempotent(t *testing.T) {
	s := newStack(t, "local")
	s.mustSeed(t)
	before := fingerprintBlock(t, s.mustSeed(t, "--verify").stdout)
	r := s.mustSeed(t, "--mode", "upsert")
	if !strings.Contains(r.stdout, ": 0 rows") {
		t.Errorf("upsert on a seeded database wrote rows:\n%s", r.stdout)
	}
	if after := fingerprintBlock(t, s.mustSeed(t, "--verify").stdout); after != before {
		t.Fatalf("upsert changed the fingerprint:\n%s\nvs\n%s", before, after)
	}
}

// The role-play of --verify opens exactly the request context Appendix D §D.3 #10c and #10d spell out as
// set_config calls: the principal comes from the users' rows through auth and iam, as in a request.
func TestRolePlayMatchesAppendixContext(t *testing.T) {
	s := newStack(t, "local")
	s.mustSeed(t)
	ctx := context.Background()
	appPool := s.d.Pool(t, db.RoleApp)
	plan, err := seed.Build(ctx, seed.FixtureFS{FS: fixtures, Registry: "testdata/registry.json", Smoke: "testdata/smoke"},
		seed.Options{Profile: seed.ProfileSmoke, Bucket: "logitrack", PublicBucket: "logitrack-public", Backend: "local"})
	if err != nil {
		t.Fatal(err)
	}
	sym := plan.Symbols.MustID
	rbac, err := iam.NewRBAC(iam.Deps{Pool: appPool, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(user string, tenant *uuid.UUID) db.RLS {
		t.Helper()
		p, err := auth.RolePlayPrincipal(ctx, appPool, sym(user), tenant)
		if err != nil {
			t.Fatal(err)
		}
		if err := rbac.Resolve(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p.RLS()
	}
	ttp, own := sym("TN_TTP"), sym("TN_OWN")
	disp := resolve("U_TTP_DISP", &ttp)
	if disp.UserID != sym("U_TTP_DISP") || *disp.TenantID != ttp || disp.Role != "operation_staff" || disp.DriverID != nil ||
		len(disp.CustomerIDs) != 1 || disp.CustomerIDs[0] != sym("BP_TTP") || len(disp.SubtenantIDs) != 0 ||
		!disp.Dispatcher || disp.Steward || disp.Bypass {
		t.Errorf("10c dispatcher context %+v", disp)
	}
	ops := resolve("U_WRT_OPS", &own)
	if *ops.TenantID != own || ops.Role != "operation_staff" || len(ops.CustomerIDs) != 0 ||
		len(ops.SubtenantIDs) != 1 || ops.SubtenantIDs[0] != sym("TN_NWR") || ops.Dispatcher || !ops.Steward || ops.Bypass {
		t.Errorf("10d own-fleet context %+v", ops)
	}
	cust := resolve("U_CJSF_CUST", nil)
	if cust.TenantID != nil || cust.Role != "customer" || len(cust.CustomerIDs) != 1 || cust.CustomerIDs[0] != sym("BP_CJSF") {
		t.Errorf("10c customer context %+v", cust)
	}
	if _, err := auth.RolePlayPrincipal(ctx, appPool, sym("U_NWR_ADMIN"), &ttp); err == nil {
		t.Error("a role-play in a tenant without a membership succeeded")
	}
}
