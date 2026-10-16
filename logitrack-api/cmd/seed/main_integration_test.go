//go:build integration

// Acceptance tests of issue T16 (#36) against PostgreSQL 18 (pgtest, the R66 roles), Redis 7 (cachetest) and
// MinIO (storagetest): seed --profile smoke and demo load through ETL_DATABASE_URL and pass --verify; two runs
// give the same fingerprint; --verify role-plays isolation through DATABASE_URL and fails when a carrier
// principal can read another tenant's rows (R87); SEED_DEFAULT_PASSWORD never reaches the output.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
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

// coldMedia gives the loads of a test that asserts a time budget a drawer with an empty memo (main.go: media), so the
// budget counts drawing every placeholder whatever loads ran before in this process. Budgets are asserted without the
// race detector only; under it the tests share the process drawer, which draws the demo's photos once (T35).
func coldMedia() { media = processMedia() }

func manifestCountLine(profile seed.Profile) string {
	tables := append(slices.Clone(seed.LoadOrder), seed.DerivedTable)
	return fmt.Sprintf("counts: %d tables match the %s manifest", len(tables), profile)
}

// The smoke profile loads in under five seconds, passes the twelve invariants, and a second run gives the
// same ids, counts and fingerprint (Appendix D §D.1.7, invariant 12).
func TestSmokeLoadVerifyTwice(t *testing.T) {
	var argon []string
	if !raceEnabled {
		// The production cost of .env.example, so the five-second budget includes real Argon2id hashing.
		argon = []string{"ARGON2_MEMORY_KB=65536", "ARGON2_ITERATIONS=3", "ARGON2_PARALLELISM=2"}
		coldMedia()
	}
	s := newStack(t, "s3", argon...)
	first := s.mustSeed(t, "--profile", "smoke")
	t.Logf("smoke load: %s (%s)", first.took, strings.TrimSpace(first.stdout))
	if !raceEnabled && first.took >= 5*time.Second {
		t.Errorf("seed --profile smoke took %s, want < 5s", first.took)
	}
	if !strings.Contains(first.stdout, ": 286 rows in ") {
		t.Errorf("load summary does not report the 286 seeded rows:\n%s", first.stdout)
	}
	if !strings.Contains(first.stdout, "d7.kitti@logitrack.test was created with a temporary password") {
		t.Errorf("the load does not say that the must-change-password user got a temporary password:\n%s", first.stdout)
	}
	s.temporaryPasswordNotPrinted(t, first)
	v1 := s.mustSeed(t, "--verify")
	if n := len(passLine.FindAllString(v1.stdout, -1)); n != 12 {
		t.Fatalf("%d of 12 invariants pass:\n%s", n, v1.stdout)
	}
	if !strings.Contains(v1.stdout, manifestCountLine(seed.ProfileSmoke)) {
		t.Errorf("count diff missing or failing:\n%s", v1.stdout)
	}
	// nwr.admin, and the TTP drivers: the broker D6 (its NWR history is the one cross-tenant read allowed, R24)
	// and D7.
	if !strings.Contains(v1.stdout, "3 carrier principal(s) (1 staff, 2 driver(s))") || !strings.Contains(v1.stdout,
		"R24 own history in other tenants: d6.amnat@logitrack.test (TTP, driver): memberships 1, tasks 1, trip_records 1, truck_assignments 1") {
		t.Errorf("the carrier drivers are not role-played or D6's R24 history is not reported:\n%s", v1.stdout)
	}
	s.mustSeed(t, "--profile", "smoke")
	v2 := s.mustSeed(t, "--verify")
	if a, b := fingerprintBlock(t, v1.stdout), fingerprintBlock(t, v2.stdout); a != b {
		t.Fatalf("two runs differ:\n%s\nvs\n%s", a, b)
	}
}

// The demo profile shows the own fleet and two carriers at once (owner addition to issue #36), with master data and
// driver logins in every tenant (T26, issue #46), and passes the invariants on the local disk backend; every carrier
// principal is role-played, the two R24 brokers included.
func TestDemoLoadVerify(t *testing.T) {
	s := newStack(t, "local")
	if !raceEnabled {
		coldMedia()
	}
	load := s.mustSeed(t, "--profile", "demo")
	t.Logf("demo load: %s (%s)", load.took, strings.TrimSpace(load.stdout))
	if !raceEnabled && load.took >= 2*time.Minute { // Appendix D §D.1.2 budget, its 3,676 objects included (T35, T46)
		t.Errorf("seed --profile demo took %s, want < 2 min", load.took)
	}
	v := s.mustSeed(t, "--verify", "--profile", "demo")
	t.Log("\n" + v.stdout)
	if n := len(passLine.FindAllString(v.stdout, -1)); n != 12 {
		t.Fatalf("%d of 12 invariants pass:\n%s", n, v.stdout)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var readable, systemOnly int
	var systemNames string
	if err := conn.QueryRow(ctx, `WITH readable AS (
		SELECT c.relname, (SELECT count(*) = 1 AND bool_and(p.polname = 'p_system')
			FROM pg_policy p WHERE p.polrelid = c.oid) AS system_only
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND c.relrowsecurity
			AND has_table_privilege('logitrack_app', c.oid, 'SELECT'))
		SELECT count(*), count(*) FILTER (WHERE system_only),
			coalesce(string_agg(relname, ', ' ORDER BY relname) FILTER (WHERE system_only), '') FROM readable`).
		Scan(&readable, &systemOnly, &systemNames); err != nil {
		t.Fatal(err)
	}
	// Staff, one per tenant and role: NWR tenant_admin, manager, operation_staff, operator, user (nwr.user, T26);
	// TTP tenant_admin, operation_staff. Drivers, one per tenant: d12 (NWR) and d14 (TTP); and the brokers D6 (NWR ->
	// TTP) and d13 (TTP -> NWR, T26), each with its own history in the tenant it left: d13's TTP work of the 42 days
	// before its move (T35: tasks, trips, photos and their objects, an incident, a standby wait; T43: its expenses on the
	// TTP truck and their receipts). Every readable RLS table is classified.
	for _, want := range []string{
		fmt.Sprintf("11 carrier principal(s) (7 staff, 4 driver(s)) across %d readable RLS tables "+
			"(%d role-played: 33 by tenant_id, 29 by owner, %d system-only (%s); 6 shared master tables skipped)",
			readable, readable-6, systemOnly, systemNames),
		"d13.thawisak@logitrack.test (NWR, driver): file_objects 190, incident_reports 1, memberships 1, standby_records 1, " +
			"tasks 42, trip_photos 183, trip_records 42, truck_assignments 1, vehicle_expenses 10",
		"d6.amnat@logitrack.test (TTP, driver): memberships 1, tasks 1, trip_records 1, truck_assignments 1",
	} {
		if !strings.Contains(v.stdout, want) {
			t.Errorf("the demo carriers are not all role-played over every RLS table (no %q):\n%s", want, v.stdout)
		}
	}
	var perTenant string
	if err := conn.QueryRow(ctx, `SELECT string_agg(t.code || ':' || m.role, ',' ORDER BY t.code, m.role)
		FROM (SELECT DISTINCT tenant_id, role FROM memberships WHERE status = 'active') m JOIN tenants t ON t.id = m.tenant_id`).
		Scan(&perTenant); err != nil {
		t.Fatal(err)
	}
	// The roles of Appendix D §D.1.2: the own fleet and NWR hold all six tenant roles, TTP three.
	want := "NWR:driver,NWR:manager,NWR:operation_staff,NWR:operator,NWR:tenant_admin,NWR:user," +
		"TTP:driver,TTP:operation_staff,TTP:tenant_admin," +
		"WRT:driver,WRT:manager,WRT:operation_staff,WRT:operator,WRT:tenant_admin,WRT:user"
	if perTenant != want {
		t.Errorf("demo active roles per tenant\n have %s\n want %s", perTenant, want)
	}
	var dispatchers, customers int
	if err := conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE kind = 'dispatcher'), count(*) FILTER (WHERE kind = 'customer')
		FROM user_scopes`).Scan(&dispatchers, &customers); err != nil {
		t.Fatal(err)
	}
	if dispatchers < 3 || customers < 3 {
		t.Errorf("demo has %d dispatcher and %d customer scopes, want a dispatcher per tenant and three customers", dispatchers, customers)
	}
	// Master data as the API reads it (T26): every truck carries the class of its type, every active assignment is
	// its driver's home truck, every driver holding one is on duty,
	// and every hub with coordinates has its SOC pairs in both directions.
	var badClass, badHome, onDuty, missingPairs int
	if err := conn.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM trucks WHERE vehicle_class IS NULL),
		(SELECT count(*) FROM truck_assignments a JOIN drivers d ON d.id = a.driver_id
		  WHERE a.status = 'active' AND d.current_assignment_id IS DISTINCT FROM a.id),
		(SELECT count(*) FROM drivers d JOIN truck_assignments a ON a.id = d.current_assignment_id
		  WHERE a.status = 'active' AND d.status = 'on_duty'),
		(SELECT count(*) FROM hubs h CROSS JOIN (SELECT DISTINCT soc_key FROM hub_soc_distances) s
		   CROSS JOIN (VALUES ('hub_to_soc'), ('soc_to_hub')) dir(d)
		  WHERE h.station_type = 'HUB' AND h.network = 'SPX' AND h.latitude IS NOT NULL
		    AND NOT EXISTS (SELECT 1 FROM hub_soc_distances x WHERE x.hub_id = h.id AND x.soc_key = s.soc_key AND x.direction = dir.d))`).
		Scan(&badClass, &badHome, &onDuty, &missingPairs); err != nil {
		t.Fatal(err)
	}
	if badClass != 0 || badHome != 0 || onDuty != 16 || missingPairs != 0 {
		t.Errorf("demo master data: %d trucks without a class, %d active assignments that are not the home truck, "+
			"%d drivers on duty (want 16 drivers with a truck), %d missing hub-SOC pairs",
			badClass, badHome, onDuty, missingPairs)
	}
	// Operations as the API reads them (T35, issue #55): today's first-mile board of Appendix D §D.2 #1 on the anchor
	// day, the drivers with a task under way pointing at it (back-filled after the load), every delivered trip with
	// its snapshot, and the task counters past every padded number of a day.
	var board string
	if err := conn.QueryRow(ctx, `SELECT string_agg(status || ':' || n, ',' ORDER BY status) FROM (
		SELECT status, count(*) AS n FROM tasks WHERE task_type = 'first_mile' AND plan_date = DATE '2026-09-30' GROUP BY status) x`).
		Scan(&board); err != nil {
		t.Fatal(err)
	}
	if want := "assigned:8,cancelled:2,checked_in:2,completed:4,in_transit:3,pending:6"; board != want {
		t.Errorf("today's first-mile board\n have %s\n want %s", board, want)
	}
	var active, badActive, unsnapped, behind int
	if err := conn.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM drivers WHERE active_task_id IS NOT NULL),
		(SELECT count(*) FROM drivers d LEFT JOIN tasks k ON k.id = d.active_task_id LEFT JOIN trucks tr ON tr.id = k.truck_id
		  WHERE (d.active_task_id IS NULL) <> (d.active_truck_id IS NULL)
		     OR (d.active_task_id IS NULL) <> (d.active_started_at IS NULL)
		     OR (d.active_task_id IS NOT NULL AND (k.driver_id <> d.id OR k.status NOT IN ('checked_in','in_transit')
		         OR d.active_truck_id IS DISTINCT FROM k.truck_id OR d.active_truck_plate IS DISTINCT FROM tr.license_plate
		         OR d.active_started_at IS DISTINCT FROM k.check_in_at))),
		(SELECT count(*) FROM trip_records t WHERE t.status = 'delivered'
		  AND NOT EXISTS (SELECT 1 FROM trip_billing_snapshots s WHERE s.trip_id = t.id)),
		(SELECT count(*) FROM tasks k JOIN task_number_counters c ON c.task_type = k.task_type AND c.plan_date = k.plan_date
		  WHERE k.task_no ~ '-[0-9]{3,}$' AND (regexp_match(k.task_no, '-([0-9]+)$'))[1]::int > c.last_seq)`).
		Scan(&active, &badActive, &unsnapped, &behind); err != nil {
		t.Fatal(err)
	}
	// Smoke D1, D2 and D7 hold TK17, TK18 and TK24; the demo adds the four drivers under way today (the board's
	// second check-in is the smoke TK24). Each holds its task's truck as its active truck (activeTruck: truck, plate,
	// task and start set together at check-in, as GET /v1/drivers/{id} reads them), and no other driver holds one.
	if active != 7 || badActive != 0 || unsnapped != 0 || behind != 0 {
		t.Errorf("demo operations: %d drivers with a task under way (want 7), %d whose active task or truck is not its task "+
			"under way, %d delivered trips without a snapshot, %d task numbers past their counter", active, badActive, unsnapped, behind)
	}
	// HR as the API reads it (T46, issue #66; invariant 6 passed above): runs in every status (the generated ones through
	// the compensation engine, each approved or paid one with its payout row), leave in every status with the evidence
	// photos stored, and the holidays of the anchor's year.
	var runs, leave string
	var payouts, attachments, evidence, holidays int
	if err := conn.QueryRow(ctx, `SELECT
		(SELECT string_agg(status || ':' || n, ',' ORDER BY status) FROM (SELECT status, count(*) AS n FROM payroll_runs GROUP BY status) x),
		(SELECT string_agg(status || ':' || n, ',' ORDER BY status) FROM (SELECT status, count(*) AS n FROM leave_requests GROUP BY status) y),
		(SELECT count(*) FROM transactions WHERE tx_type = 'driver_payout'),
		(SELECT count(*) FROM leave_request_attachments),
		(SELECT count(*) FROM leave_request_attachments a JOIN file_objects f ON f.id = a.file_id
		  WHERE f.purpose = 'leave_evidence' AND f.status = 'committed' AND f.owner_id = a.leave_request_id),
		(SELECT count(*) FROM holidays)`).Scan(&runs, &leave, &payouts, &attachments, &evidence, &holidays); err != nil {
		t.Fatal(err)
	}
	if runs != "approved:4,draft:3,paid:13,pending_approval:1" || leave != "approved:2,cancelled:1,pending:3,rejected:2" ||
		payouts != 17 || attachments != 2 || evidence != 2 || holidays != 17 {
		t.Errorf("demo HR: runs %s, leave %s, %d payout rows (want 17), %d leave attachments (%d committed evidence, want 2), %d holidays (want 17)",
			runs, leave, payouts, attachments, evidence, holidays)
	}
	checkDemoBilling(t, s, conn)
}

// checkDemoBilling is the AC of issue #63 on the loaded demo (T43): every statement with lines carries their exact sums,
// round2(total x rate) withholding and total - withholding net; and the period lock holds against the pricing the API
// runs (billing.PriceTrip, POST /v1/trips/{id}/billing/compute with forceRecompute): a forced recompute of a trip on the
// generated paid and sent statements is refused with that statement's invoice number and writes nothing, one on the
// draft statement of the same carrier runs.
func checkDemoBilling(t *testing.T, s *stack, conn *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	var statements, bad int
	if err := conn.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE b.total_amount <> l.total OR b.trip_count <> l.n
		OR b.withholding_tax <> round(b.total_amount * b.withholding_tax_rate, 2) OR b.net_amount <> b.total_amount - b.withholding_tax)
		FROM billing_statements b JOIN (SELECT statement_id, sum(amount_thb) AS total, count(*) AS n FROM billing_statement_lines
		GROUP BY 1) l ON l.statement_id = b.id`).Scan(&statements, &bad); err != nil {
		t.Fatal(err)
	}
	if statements != 7 || bad != 0 {
		t.Errorf("%d statements with lines (want 7: ST1, ST3, ST4 and the generated four), %d whose totals are not their lines'", statements, bad)
	}
	app := s.d.Pool(t, db.RoleApp)
	errRollback := errors.New("rolled back by the test")
	for _, c := range []struct {
		invoice string
		blocked bool
	}{{"SPK-202607-002", true}, {"SPK-202608-001", true}, {"SPX-202608-001", false}} {
		var trip uuid.UUID
		var before string
		if err := conn.QueryRow(ctx, `SELECT t.id, sn.updated_at::text FROM billing_statement_lines l
			JOIN billing_statements b ON b.id = l.statement_id JOIN trip_records t ON t.id = l.trip_id
			JOIN trip_billing_snapshots sn ON sn.trip_id = t.id
			WHERE b.invoice_number = $1 AND l.row_type = 'trip' AND NOT sn.manual_override
			ORDER BY t.trip_no LIMIT 1`, c.invoice).Scan(&trip, &before); err != nil {
			t.Fatalf("%s: a priced trip line: %v", c.invoice, err)
		}
		var out billing.TripOutcome
		err := db.WithSystem(ctx, app, nil, func(tx pgx.Tx) error {
			var err error
			if out, err = billing.PriceTrip(ctx, tx, trip, billing.TripRequest{Mode: billing.ModeForce,
				ComputedBy: billing.ComputedByAdminForce, At: time.Now()}); err != nil {
				return err
			}
			if !c.blocked {
				return errRollback // the reprice ran: leave the seeded row as it was
			}
			return nil
		})
		if err != nil && !errors.Is(err, errRollback) {
			t.Fatalf("%s: forced recompute: %v", c.invoice, err)
		}
		switch {
		case c.blocked && (out.Outcome != billing.OutcomeBlocked || out.Lock == nil || out.Lock.InvoiceNumber != c.invoice ||
			out.Result.BlockedInvoiceNumber != c.invoice):
			t.Errorf("forced recompute of a trip on %s: %s %+v, want blocked by that invoice", c.invoice, out.Outcome, out.Result)
		case !c.blocked && out.Outcome == billing.OutcomeBlocked:
			t.Errorf("forced recompute of a trip on the draft %s was blocked by %+v", c.invoice, out.Lock)
		}
		var after string
		if err := conn.QueryRow(ctx, `SELECT updated_at::text FROM trip_billing_snapshots WHERE trip_id = $1`, trip).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Errorf("%s: the trip's snapshot changed (%s -> %s)", c.invoice, before, after)
		}
	}
}

// A database seeded with a smaller profile takes the demo rows with --mode upsert, as the staging VM seeded with an
// older demo takes the T26 master data: the top-up inserts only what is missing, passes --verify, and ends with the
// fingerprint of a fresh demo load. A database whose demo trucks predate T43's maintenance history (the staging VM:
// its trucks hold their T26 mileage and service state, and none of the generated records) takes the records, keeps
// the truck rows and says so.
func TestDemoUpsertOverSmaller(t *testing.T) {
	s := newStack(t, "s3") // MinIO: the local backend's fsync per object is slow on macOS (TestDemoLoadVerify covers it)
	s.mustSeed(t, "--profile", "smoke")
	r := s.mustSeed(t, "--profile", "demo", "--mode", "upsert")
	if strings.Contains(r.stdout, "created with a temporary password") {
		t.Errorf("the top-up inserted the must-change-password user again:\n%s", r.stdout)
	}
	topped := s.mustSeed(t, "--verify", "--profile", "demo")
	if n := len(passLine.FindAllString(topped.stdout, -1)); n != 12 || !strings.Contains(topped.stdout, manifestCountLine(seed.ProfileDemo)) {
		t.Fatalf("after the top-up:\n%s", topped.stdout)
	}
	if again := s.mustSeed(t, "--profile", "demo", "--mode", "upsert"); !strings.Contains(again.stdout, ": 0 rows") {
		t.Errorf("a second top-up wrote rows:\n%s", again.stdout)
	}
	fresh := newStack(t, "s3")
	fresh.mustSeed(t, "--profile", "demo")
	if a, b := fingerprintBlock(t, topped.stdout), fingerprintBlock(t, fresh.mustSeed(t, "--verify", "--profile", "demo").stdout); a != b {
		t.Fatalf("a topped-up and a fresh demo differ:\n%s\nvs\n%s", a, b)
	}

	// Back to a pre-T43 fleet: the generated maintenance records gone and every truck they name with another state.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	fx := seed.FixtureFS{FS: fixtures, Registry: "testdata/registry.json", Smoke: "testdata/smoke"}
	smoke, err := seed.Build(ctx, fx, seed.Options{Profile: seed.ProfileSmoke, Bucket: "logitrack", PublicBucket: "logitrack-public", Backend: "local"})
	if err != nil {
		t.Fatal(err)
	}
	var smokeTrucks []uuid.UUID
	for _, r := range smoke.Tables["trucks"] {
		smokeTrucks = append(smokeTrucks, uuid.MustParse(r.Str("id")))
	}
	var plates []string
	if err := conn.QueryRow(ctx, `SELECT coalesce(array_agg(DISTINCT license_plate ORDER BY license_plate), '{}') FROM trucks
		WHERE id IN (SELECT truck_id FROM maintenance_records) AND NOT id = ANY($1::uuid[])`, smokeTrucks).Scan(&plates); err != nil {
		t.Fatal(err)
	}
	var records, removed int64
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM maintenance_records`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE trucks SET active_maintenance_id = NULL, last_alert_mileage = NULL, last_service_date = NULL,
		status = 'active', current_mileage = current_mileage - 1000 WHERE id IN (SELECT truck_id FROM maintenance_records)
		AND NOT id = ANY($1::uuid[])`, `DELETE FROM maintenance_records WHERE NOT truck_id = ANY($1::uuid[])`} {
		tag, err := conn.Exec(ctx, q, smokeTrucks)
		if err != nil {
			t.Fatal(err)
		}
		removed = tag.RowsAffected()
	}
	if len(plates) < 3 || removed == 0 {
		t.Fatalf("the demo's maintenance history names %v (%d records)", plates, removed)
	}
	r = s.mustSeed(t, "--profile", "demo", "--mode", "upsert")
	var after int64
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM maintenance_records`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != records {
		t.Errorf("after the top-up %d maintenance records, want the %d of a demo (%d inserted again)", after, records, removed)
	}
	if want := fmt.Sprintf("%d truck row(s) kept as the database holds them", len(plates)); !strings.Contains(r.stderr, want) {
		t.Errorf("the top-up does not report the %d kept trucks (%q):\n%s", len(plates), want, r.stderr)
	}
	shown := plates
	if len(shown) > 10 {
		shown = shown[:10]
		if want := fmt.Sprintf(" and %d more", len(plates)-len(shown)); !strings.Contains(r.stderr, want) {
			t.Errorf("the kept-truck warning does not report its omitted plates (%q):\n%s", want, r.stderr)
		}
	}
	for _, plate := range shown {
		if !strings.Contains(r.stderr, plate) {
			t.Errorf("the kept-truck warning does not name %s:\n%s", plate, r.stderr)
		}
	}
}

// A demo top-up of a shared database where the application already generated payroll (POST /v1/jobs/payroll.run,
// uuidv7 ids) for a driver, period and round the plan also holds keeps the application's runs and skips those drivers'
// seeded payroll chains, instead of rolling the whole load back on the payout row's or the penalty applications'
// foreign key (T46 review): the staging VM's rollout --reseed after someone clicked "Generate payroll". Both kinds of
// held key: a paid R2 run that deducts a penalty installment, and an approved R1 run of another driver.
func TestDemoUpsertKeepsApplicationPayrollRuns(t *testing.T) {
	s := newStack(t, "s3") // MinIO: the fast backend for the demo's objects (TestDemoUpsertOverSmaller)
	s.mustSeed(t, "--profile", "demo")
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	fx := seed.FixtureFS{FS: fixtures, Registry: "testdata/registry.json", Smoke: "testdata/smoke"}
	smoke, err := seed.Build(ctx, fx, seed.Options{Profile: seed.ProfileSmoke, Bucket: "logitrack", PublicBucket: "logitrack-public", Backend: "local"})
	if err != nil {
		t.Fatal(err)
	}
	idsOf := func(rows []seed.Row) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Str("id"))
		}
		return out
	}
	smokeRuns, smokePens := idsOf(smoke.Tables["payroll_runs"]), idsOf(smoke.Tables["driver_penalties"])

	type key struct {
		tenant, driver, period, round string
		start, end                    time.Time
	}
	var r2, r1 key
	if err := conn.QueryRow(ctx, `SELECT tenant_id::text, driver_id::text, pay_period, pay_round, period_start, period_end
		FROM payroll_runs r WHERE status = 'paid' AND pay_round = 'R2' AND id <> ALL($1::uuid[])
		  AND EXISTS (SELECT 1 FROM payroll_penalty_applications a WHERE a.payroll_run_id = r.id)
		ORDER BY pay_period DESC, id LIMIT 1`, smokeRuns).Scan(&r2.tenant, &r2.driver, &r2.period, &r2.round, &r2.start, &r2.end); err != nil {
		t.Fatalf("a generated paid R2 run with penalty applications: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT tenant_id::text, driver_id::text, pay_period, pay_round, period_start, period_end
		FROM payroll_runs WHERE status = 'approved' AND pay_round = 'R1' AND id <> ALL($1::uuid[]) AND driver_id <> $2::uuid
		ORDER BY pay_period DESC, id LIMIT 1`, smokeRuns, r2.driver).Scan(&r1.tenant, &r1.driver, &r1.period, &r1.round, &r1.start, &r1.end); err != nil {
		t.Fatalf("a generated approved R1 run of another driver: %v", err)
	}

	// The staging database before T46 (no generated payroll, penalty or payout row; replica mode lets the frozen-lines
	// trigger and the foreign keys pass), then "Generate payroll" of the two keys: drafts under the application's ids.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, st := range []struct {
		sql  string
		args []any
	}{
		{`SET LOCAL session_replication_role = replica`, nil},
		{`DELETE FROM payroll_penalty_applications WHERE payroll_run_id <> ALL($1::uuid[])`, []any{smokeRuns}},
		{`DELETE FROM payroll_line_items WHERE payroll_run_id <> ALL($1::uuid[])`, []any{smokeRuns}},
		{`DELETE FROM transactions WHERE payroll_run_id IS NOT NULL AND payroll_run_id <> ALL($1::uuid[])`, []any{smokeRuns}},
		{`DELETE FROM payroll_runs WHERE id <> ALL($1::uuid[])`, []any{smokeRuns}},
		{`DELETE FROM driver_penalties WHERE id <> ALL($1::uuid[])`, []any{smokePens}},
	} {
		if _, err := tx.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatalf("%s: %v", st.sql, err)
		}
	}
	appRuns := map[string]bool{}
	for _, k := range []key{r2, r1} {
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO payroll_runs (tenant_id, driver_id, pay_period, pay_round, period_start, period_end)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`, k.tenant, k.driver, k.period, k.round, k.start, k.end).Scan(&id); err != nil {
			t.Fatal(err)
		}
		appRuns[id] = true
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	r := s.mustSeed(t, "--profile", "demo", "--mode", "upsert")
	if !strings.Contains(r.stdout, "seed: kept 2 payroll run(s) written by the application") {
		t.Errorf("the top-up does not say it kept the application's runs:\n%s", r.stdout)
	}
	// The two drivers keep only the application's drafts (no seeded run, penalty or payout row of theirs); the third paid
	// driver's chain is in.
	var held, heldPens, heldPayouts, others int
	if err := conn.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM payroll_runs WHERE driver_id = ANY($1::uuid[])),
		(SELECT count(*) FROM driver_penalties WHERE driver_id = ANY($1::uuid[]) AND id <> ALL($3::uuid[])),
		(SELECT count(*) FROM transactions WHERE driver_id = ANY($1::uuid[]) AND payroll_run_id IS NOT NULL),
		(SELECT count(*) FROM payroll_runs WHERE driver_id <> ALL($1::uuid[]) AND id <> ALL($2::uuid[]))`,
		[]string{r2.driver, r1.driver}, smokeRuns, smokePens).Scan(&held, &heldPens, &heldPayouts, &others); err != nil {
		t.Fatal(err)
	}
	if held != 2 || heldPens != 0 || heldPayouts != 0 || others != 6 {
		t.Errorf("after the top-up: %d runs of the two drivers (want the 2 drafts), %d seeded penalties and %d payout rows of theirs "+
			"(want 0), %d seeded runs of the third driver (want 6)", held, heldPens, heldPayouts, others)
	}
	rows, err := conn.Query(ctx, `SELECT id::text FROM payroll_runs WHERE driver_id = ANY($1::uuid[])`, []string{r2.driver, r1.driver})
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		if !appRuns[id] {
			t.Errorf("run %s of a held driver is not the application's", id)
		}
	}
	rows.Close()
	// The database now differs from the demo manifest by the application's rows: the counts, and invariant 12, which
	// names exactly the application's two uuidv7 runs. Every other invariant holds, run totals, ledger and penalty
	// balances (6) included.
	v := s.seed(t, "--verify", "--profile", "demo")
	if n := len(passLine.FindAllString(v.stdout, -1)); n != 11 || !strings.Contains(v.stdout, " 6 PASS payroll totals, net and ledger; penalty balances") {
		t.Fatalf("%d of the 11 invariants a shared database can pass hold after the top-up:\n%s\n%s", n, v.stdout, v.stderr)
	}
	notV5 := regexp.MustCompile(`(?m)^ +- (\S+) ([0-9a-f-]{36}): not a uuid v5$`).FindAllStringSubmatch(v.stdout, -1)
	if len(notV5) != 2 {
		t.Fatalf("invariant 12 names %d rows, want the application's 2 runs:\n%s", len(notV5), v.stdout)
	}
	for _, m := range notV5 {
		if m[1] != "payroll_runs" || !appRuns[m[2]] {
			t.Errorf("invariant 12 names %s %s, not an application run", m[1], m[2])
		}
	}
	if again := s.mustSeed(t, "--profile", "demo", "--mode", "upsert"); !strings.Contains(again.stdout, ": 0 rows") ||
		!strings.Contains(again.stdout, "seed: kept 2 payroll run(s)") {
		t.Errorf("a second top-up wrote rows, or no longer says what it kept:\n%s", again.stdout)
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

// leakVerify seeds smoke, applies setup as the superuser and returns --verify, which must exit 1 with an
// invariant 10 failure.
func leakVerify(t *testing.T, setup ...string) result {
	t.Helper()
	s := newStack(t, "local")
	s.mustSeed(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, q := range setup {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	r := s.seed(t, "--verify")
	if r.code != exitViolation || !strings.Contains(r.stdout, "10 FAIL") {
		t.Fatalf("exit %d, want %d with 10 FAIL:\n%s\n%s", r.code, exitViolation, r.stdout, r.stderr)
	}
	return r
}

// A leak through a table without its own tenant_id fails --verify too: a child whose owner is its parent
// (payroll lines of the own fleet's runs) and users, whose tenant is a membership (other tenants' e-mails).
func TestVerifyFailsOnChildLeak(t *testing.T) {
	r := leakVerify(t,
		`CREATE POLICY seed_test_leak ON payroll_line_items FOR SELECT TO logitrack_app USING (true)`,
		`CREATE POLICY seed_test_leak ON users FOR SELECT TO logitrack_app USING (true)`)
	for _, want := range []string{
		`carrier nwr\.admin@logitrack\.test \(NWR, tenant_admin\): reads 6 row\(s\) of other tenants in payroll_line_items \(owner through payroll_runs\)`,
		`carrier nwr\.admin@logitrack\.test \(NWR, tenant_admin\): reads 10 row\(s\) of other tenants in users \(owner through memberships\)`,
	} {
		if !regexp.MustCompile(want).MatchString(r.stdout) {
			t.Errorf("no %q in\n%s", want, r.stdout)
		}
	}
}

// A driver policy that loses its driver_id predicate fails --verify: every carrier driver is role-played, and
// only its own rows count as its history (R24).
func TestVerifyFailsOnDriverLeak(t *testing.T) {
	r := leakVerify(t, `CREATE POLICY seed_test_leak ON tasks FOR SELECT TO logitrack_app USING (app_role() = 'driver')`)
	if !regexp.MustCompile(`carrier d7\.kitti@logitrack\.test \(TTP, driver\): reads \d+ row\(s\) of other tenants in tasks`).MatchString(r.stdout) {
		t.Errorf("the driver leak is not reported for d7:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "nwr.admin@logitrack.test (NWR, tenant_admin): reads") {
		t.Errorf("a driver-only policy is reported for a staff principal:\n%s", r.stdout)
	}
}

// A readable RLS table that no rule classifies fails --verify instead of passing unchecked.
func TestVerifyFailsOnUnclassifiedTable(t *testing.T) {
	r := leakVerify(t,
		`CREATE TABLE seed_test_unclassified (id uuid PRIMARY KEY)`,
		`ALTER TABLE seed_test_unclassified ENABLE ROW LEVEL SECURITY`,
		`GRANT SELECT ON seed_test_unclassified TO logitrack_app`)
	if !strings.Contains(r.stdout, "10e: RLS table seed_test_unclassified is readable by logitrack_app but has no isolation rule") {
		t.Errorf("the unclassified table is not reported:\n%s", r.stdout)
	}
}

// Even a carrier's own private support rows must stay invisible. Extra SELECT policies must not turn
// these tables into ordinary tenant-stamped tables or remove them from verification.
func TestVerifyFailsOnSystemOnlyLeak(t *testing.T) {
	r := leakVerify(t,
		`INSERT INTO sessions (user_id, platform, amr, absolute_expires_at)
			SELECT user_id, 'web', 'pwd', now() + interval '1 day' FROM user_platform_roles
			WHERE role = 'platform_admin' ORDER BY user_id LIMIT 1`,
		`INSERT INTO support_visits (session_id, actor_user_id, tenant_id, consent_version_at_entry)
			SELECT s.id, s.user_id, t.id, 0 FROM sessions s JOIN user_platform_roles p ON p.user_id = s.user_id
			CROSS JOIN tenants t WHERE p.role = 'platform_admin' AND t.code = 'NWR' ORDER BY s.id LIMIT 1`,
		`CREATE POLICY seed_test_leak ON membership_provenance_history FOR SELECT TO logitrack_app USING (tenant_id = app_tenant_id())`,
		`CREATE POLICY seed_test_leak ON support_visits FOR SELECT TO logitrack_app USING (tenant_id = app_tenant_id())`)
	for _, table := range []string{"membership_provenance_history", "support_visits"} {
		if !regexp.MustCompile(`carrier nwr\.admin@logitrack\.test .*reads [1-9][0-9]* row\(s\).*in ` + table + ` \(owner through system-only\)`).MatchString(r.stdout) {
			t.Errorf("private rows in %s were not reported as a carrier leak:\n%s", table, r.stdout)
		}
	}
}

// Newly added p_system-only tables need no registry update, including tables without tenant_id.
func TestVerifyDiscoversSystemOnlyTable(t *testing.T) {
	s := newStack(t, "local")
	s.mustSeed(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `CREATE TABLE seed_test_system_only (id uuid PRIMARY KEY);
		ALTER TABLE seed_test_system_only ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
		GRANT SELECT ON seed_test_system_only TO logitrack_app;
		CREATE POLICY p_system ON seed_test_system_only USING (app_role() = 'system' AND app_bypass());
		INSERT INTO seed_test_system_only VALUES (uuidv7())`); err != nil {
		t.Fatal(err)
	}
	s.mustSeed(t, "--verify")
	if _, err := conn.Exec(ctx, `ALTER POLICY p_system ON seed_test_system_only USING (true)`); err != nil {
		t.Fatal(err)
	}
	r := s.seed(t, "--verify")
	if r.code != exitViolation || !strings.Contains(r.stdout, "10 FAIL") ||
		!strings.Contains(r.stdout, "in seed_test_system_only (owner through system-only)") {
		t.Fatalf("a visible row in a discovered system-only table must fail verification:\n%s\n%s", r.stdout, r.stderr)
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
	// T61: the hot queries of the loaded volume are served by their indexes (planshape_integration_test.go).
	t.Run("plan shapes", func(t *testing.T) { checkPlanShapes(t, s) })
}

// Upsert on an already seeded database inserts nothing and keeps the fingerprint; no load prints the temporary
// password (owner addition to T19: stdout reaches CI logs).
func TestUpsertIsIdempotent(t *testing.T) {
	s := newStack(t, "local")
	first := s.mustSeed(t)
	if !strings.Contains(first.stdout, "d7.kitti@logitrack.test was created with a temporary password") {
		t.Errorf("the load that inserts d7.kitti does not say so:\n%s", first.stdout)
	}
	s.temporaryPasswordNotPrinted(t, first)
	before := fingerprintBlock(t, s.mustSeed(t, "--verify").stdout)
	r := s.mustSeed(t, "--mode", "upsert")
	if !strings.Contains(r.stdout, ": 0 rows") {
		t.Errorf("upsert on a seeded database wrote rows:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "created with a temporary password") || !strings.Contains(r.stdout, "d7.kitti@logitrack.test already exists: its password is unchanged") {
		t.Errorf("an upsert that inserts no user printed a temporary password, or said nothing:\n%s", r.stdout)
	}
	if after := fingerprintBlock(t, s.mustSeed(t, "--verify").stdout); after != before {
		t.Fatalf("upsert changed the fingerprint:\n%s\nvs\n%s", before, after)
	}
}

// An upsert recomputes only the prices it writes: a shared database where someone edited a seeded price by hand, or
// imported a later rate round, still takes a top-up (the app never re-prices those rows either). The demo's generated
// operations rows are priced from what that database holds (Appendix D §D.1.7): after the later RE01 round and a rate
// card for one of the demo's "need a rate card" routes, the staging VM's top-up (seed --profile demo, upsert) lands,
// and its trips take both.
func TestUpsertLeavesExistingPricesAlone(t *testing.T) {
	s := newStack(t, "s3") // the demo top-up writes its objects: MinIO is the fast backend (TestDemoUpsertOverSmaller)
	s.mustSeed(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	fx := seed.FixtureFS{FS: fixtures, Registry: "testdata/registry.json", Smoke: "testdata/smoke"}
	plan, err := seed.Build(ctx, fx, seed.Options{Profile: seed.ProfileSmoke, Bucket: "logitrack", PublicBucket: "logitrack-public", Backend: "local"})
	if err != nil {
		t.Fatal(err)
	}
	sym := plan.Symbols.MustID
	// PUT /v1/trips/{id}/billing/manual on the unlocked September trip TR14 (Appendix B).
	if _, err := conn.Exec(ctx, `UPDATE trip_billing_snapshots SET estimate_thb = 1999.00, manual_override = true,
		computed_by = 'manual_edit' WHERE trip_id = $1`, sym("TR14")); err != nil {
		t.Fatal(err)
	}
	s.mustSeed(t, "--verify")
	if r := s.mustSeed(t, "--mode", "upsert"); !strings.Contains(r.stdout, ": 0 rows") {
		t.Errorf("upsert after a manual price edit wrote rows:\n%s", r.stdout)
	}
	// A later rate round for RE01's route (a rate import): the seeded snapshots would now price differently.
	if _, err := conn.Exec(ctx, `INSERT INTO customer_rate_entries (tenant_id, tenant_source, billing_party_id, import_id,
		hub_code, raw_hub_name, destination_code, vehicle_class, rate_thb, job_category, effective_from_date, effective_from_at,
		imported_at, created_by, created_at, voided)
		SELECT tenant_id, tenant_source, billing_party_id, 'rc_1783213200000', hub_code, raw_hub_name, destination_code,
		       vehicle_class, 1300.00, job_category, '2026-07-05', '2026-07-05T00:00:00+07:00', '2026-07-04T09:00:00+07:00',
		       created_by, '2026-07-04T09:00:00+07:00', false
		FROM customer_rate_entries WHERE id = $1`, sym("RE01")); err != nil {
		t.Fatal(err)
	}
	if r := s.mustSeed(t, "--mode", "upsert"); !strings.Contains(r.stdout, ": 0 rows") {
		t.Errorf("upsert after a rate import wrote rows:\n%s", r.stdout)
	}

	// A rate card, effective after the window, for a route the demo leaves unpriced (no_rate): the engine's oldest-entry
	// fallback prices the window's trips of that route with it.
	demo, err := seed.Build(ctx, fx, seed.Options{Profile: seed.ProfileDemo, RandomSeed: 20261009, Bucket: "logitrack",
		PublicBucket: "logitrack-public", Backend: "s3"})
	if err != nil {
		t.Fatal(err)
	}
	party, hub, dest, class := noRateRoute(t, ctx, conn, demo)
	if _, err := conn.Exec(ctx, `INSERT INTO customer_rate_entries (tenant_id, tenant_source, billing_party_id, import_id,
		hub_code, raw_hub_name, destination_code, vehicle_class, rate_thb, job_category, effective_from_date, effective_from_at,
		imported_at, created_by, created_at, voided)
		SELECT tenant_id, tenant_source, $2, 'rc_1791090000000', $3, $3, $4, $5, 1234.00, 'PRIMARY', '2026-10-05',
		       '2026-10-05T00:00:00+07:00', '2026-10-04T09:00:00+07:00', created_by, '2026-10-04T09:00:00+07:00', false
		FROM customer_rate_entries WHERE id = $1`, sym("RE01"), party, hub, dest, class); err != nil {
		t.Fatal(err)
	}
	// A later SPK fuel round from 20 August (an announcement through the API): the smoke TR16 of 26 August is now priced
	// otherwise than it stores. The top-up writes no sent statement over it (T43): SPK August waits for the backfill,
	// SPK July lands.
	if _, err := conn.Exec(ctx, `INSERT INTO customer_fuel_rate_adjustments (tenant_id, tenant_source, billing_party_id,
		effective_from_date, effective_from_at, rate_multiplier, add_thb_per_trip, announcement_note, created_by, created_at, voided)
		SELECT tenant_id, tenant_source, billing_party_id, '2026-08-20', '2026-08-20T00:00:00+07:00', 1.000000, -20.00,
		       'รอบ 20 ส.ค.', created_by, '2026-08-18T10:00:00+07:00', false
		FROM customer_fuel_rate_adjustments WHERE id = $1`, sym("FA06")); err != nil {
		t.Fatal(err)
	}
	// The staging VM numbers statements through the API (T37): an SPX August draft is already there, with its counter,
	// so the top-up leaves that month alone (T43) and writes the other generated statements from what the database holds.
	for _, q := range []string{`INSERT INTO billing_counters (billing_party_id, period_year, period_month, tenant_id, last_seq,
		customer_code) VALUES ($1, 2026, 8, $2, 1, 'SPX')`, `INSERT INTO billing_statements (tenant_id, billing_party_id,
		invoice_number, period_year, period_month, total_amount, withholding_tax_rate, withholding_tax, net_amount, status,
		generated_at) VALUES ($2, $1, 'SPX-202608-001', 2026, 8, 0, 0.0100, 0, 0, 'draft', '2026-10-01T10:00:00+07:00')`} {
		if _, err := conn.Exec(ctx, q, sym("BP_SPX"), sym("TN_OWN")); err != nil {
			t.Fatal(err)
		}
	}
	top := s.mustSeed(t, "--profile", "demo", "--mode", "upsert")
	if !strings.Contains(top.stderr, "1 generated statement month(s) already numbered in the database") {
		t.Errorf("the top-up does not report the SPX August month the database numbered:\n%s", top.stderr)
	}
	if !strings.Contains(top.stderr, "statement SPK-202608 (2026-08) not written") || !strings.Contains(top.stderr, "ZXJB26082600116") {
		t.Errorf("the top-up does not leave SPK August (the stale TR16) for after the backfill:\n%s", top.stderr)
	}
	var spx, spk, badTotals int
	if err := conn.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM billing_statements WHERE invoice_number LIKE 'SPX-202608-%'),
		(SELECT count(*) FROM billing_statements WHERE invoice_number IN ('SPK-202607-001','SPK-202607-002','SPK-202608-001')
		   AND uuid_extract_version(id) = 5),
		(SELECT count(*) FROM billing_statements b JOIN (SELECT statement_id, sum(amount_thb) AS total FROM billing_statement_lines
		   GROUP BY 1) l ON l.statement_id = b.id WHERE b.total_amount <> l.total)`).Scan(&spx, &spk, &badTotals); err != nil {
		t.Fatal(err)
	}
	if spx != 1 || spk != 2 || badTotals != 0 {
		t.Errorf("after the top-up: %d SPX August statements (want the API's one), %d generated SPK statements (want July's 2), "+
			"%d statements whose totals are not their lines'", spx, spk, badTotals)
	}
	// The backfill the deploy notes give for such a database (deploy/vm/README.md §4: every unpriced delivered trip,
	// and a forced recompute of the open periods' priced ones) leaves no price after a sent or paid statement's sent_at:
	// the generated statements locked no period whose rows the database prices otherwise.
	backfill(t, s, conn, sym("BP_SPK"), "2026-08-01")
	if v := s.seed(t, "--verify", "--profile", "demo"); !strings.Contains(v.stdout, " 4 PASS") || !strings.Contains(v.stdout, " 5 PASS") {
		t.Errorf("invariants 4 and 5 after the top-up and the backfill:\n%s", v.stdout)
	}
	var trips, laterRound, newCard int
	if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM trip_records),
		(SELECT count(*) FROM trip_billing_snapshots s JOIN customer_rate_entries e ON e.id = s.rate_entry_id
		  WHERE e.import_id = 'rc_1783213200000'),
		(SELECT count(*) FROM trip_billing_snapshots s JOIN customer_rate_entries e ON e.id = s.rate_entry_id
		  WHERE e.import_id = 'rc_1791090000000' AND s.estimate_thb IS NOT NULL)`).Scan(&trips, &laterRound, &newCard); err != nil {
		t.Fatal(err)
	}
	if trips != 790 || laterRound == 0 || newCard == 0 {
		t.Errorf("after the top-up: %d trips (want 790), %d priced on the later RE01 round, %d on the new card (want both > 0)",
			trips, laterRound, newCard)
	}
}

// backfill runs what POST /v1/jobs/billing.backfill-trips writes (billing.PriceTrip): every unpriced delivered trip in
// auto mode, whatever its period (a first price is not period-locked), and a forced recompute of the party's priced
// trips delivered from openFrom on (refused in a sent or paid period).
func backfill(t *testing.T, s *stack, conn *pgx.Conn, party uuid.UUID, openFrom string) {
	t.Helper()
	ctx := context.Background()
	rows, err := conn.Query(ctx, `SELECT t.id, s.estimate_thb IS NULL FROM trip_records t JOIN trip_billing_snapshots s ON s.trip_id = t.id
		WHERE t.status = 'delivered' AND (s.estimate_thb IS NULL OR (t.billing_party_id = $2 AND t.delivered_at >= $1::date
		  AND s.computed_by NOT IN ('etl','manual_edit')))
		ORDER BY t.id`, openFrom, party)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id       uuid.UUID
		unpriced bool
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.unpriced); err != nil {
			t.Fatal(err)
		}
		todo = append(todo, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	app := s.d.Pool(t, db.RoleApp)
	for _, r := range todo {
		req := billing.TripRequest{Mode: billing.ModeAuto, ComputedBy: billing.ComputedByScheduler, At: time.Now()}
		if !r.unpriced {
			req.Mode, req.ComputedBy = billing.ModeForce, billing.ComputedByAdminForce
		}
		if err := db.WithSystem(ctx, app, nil, func(tx pgx.Tx) error {
			_, err := billing.PriceTrip(ctx, tx, r.id, req)
			return err
		}); err != nil {
			t.Fatalf("backfill of %s: %v", r.id, err)
		}
	}
}

// noRateRoute is a route the demo's generated trips leave unpriced (no_rate) for a billing party the smoke database
// already holds: party, the lookup hub and destination codes, and the vehicle class.
func noRateRoute(t *testing.T, ctx context.Context, conn *pgx.Conn, demo *seed.Plan) (party, hub, dest, class string) {
	t.Helper()
	parties := map[string]bool{}
	rows, err := conn.Query(ctx, `SELECT id::text FROM billing_parties`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		parties[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	tasks, trips := map[string]seed.Row{}, map[string]seed.Row{}
	for _, r := range demo.Tables["tasks"] {
		tasks[r.Str("id")] = r
	}
	for _, r := range demo.Tables["trip_records"] {
		trips[r.Str("id")] = r
	}
	generatedTrip := regexp.MustCompile(`^ZX[JZ]B[0-9]{6}2[0-9]{4}$`) // the T35 range, outside the fixture's numbers
	for _, sn := range demo.Tables["trip_billing_snapshots"] {
		tr := trips[sn.Str("trip_id")]
		if !generatedTrip.MatchString(tr.Str("trip_no")) {
			continue
		}
		k := tasks[tr.Str("task_id")]
		p := k.Str("billing_party_id")
		for _, c := range []string{"source_linked_party_id", "destination_linked_party_id"} {
			if p == "" {
				p = k.Str(c)
			}
		}
		if sn.Str("unpriced_reason") == "no_rate" && k.Str("job_category") == "PRIMARY" && k.Str("truck_type") != "" &&
			sn.Str("lookup_hub_code") != "" && parties[p] {
			return p, sn.Str("lookup_hub_code"), sn.Str("lookup_destination_code"), k.Str("truck_type")
		}
	}
	t.Fatal("the demo plan has no unpriced no_rate route of a smoke billing party")
	return "", "", "", ""
}

// A database seeded in another SEED_NAMESPACE is refused with a clear message: the natural keys and the
// own-fleet singleton are the same in every namespace, so two datasets never share a database.
func TestOtherNamespaceIsRefused(t *testing.T) {
	s := newStack(t, "local")
	s.mustSeed(t)
	other := *s
	other.env = append(append([]string(nil), s.env...), "SEED_NAMESPACE=6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	r := other.seed(t, "--mode", "upsert")
	if r.code != 1 || !strings.Contains(r.stderr, "seeded in another SEED_NAMESPACE") {
		t.Fatalf("exit %d, want 1 with the namespace message:\n%s\n%s", r.code, r.stdout, r.stderr)
	}
	s.mustSeed(t, "--verify")
}

// An upsert never leaves a file_objects row describing other bytes than the store holds: when the drawn
// bytes differ from what an existing row records (another toolchain or renderer), the stored object stays.
func TestUpsertKeepsObjectsWithOtherBytes(t *testing.T) {
	s := newStack(t, "local")
	s.mustSeed(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, s.d.SuperURL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var key string
	if err := conn.QueryRow(ctx, `SELECT object_key FROM file_objects WHERE content_type = 'image/jpeg' AND status = 'committed'
		AND bucket = 'logitrack' ORDER BY object_key LIMIT 1`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	var dir string
	for _, e := range s.env {
		if v, ok := strings.CutPrefix(e, "LOCAL_MEDIA_DIR="); ok {
			dir = v
		}
	}
	local, err := storage.NewLocal(storage.LocalConfig{Dir: dir, PublicBaseURL: "http://localhost:8081/media",
		APIUploadPath: storage.LocalUploadPath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	old := []byte("bytes an older toolchain drew")
	if err := local.Put(ctx, storage.Object{Bucket: "logitrack", Key: key}, bytes.NewReader(old), int64(len(old)), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(old)
	if _, err := conn.Exec(ctx, `UPDATE file_objects SET size_bytes = $1, sha256 = $2 WHERE object_key = $3`,
		len(old), hex.EncodeToString(sum[:]), key); err != nil {
		t.Fatal(err)
	}
	s.mustSeed(t, "--verify")
	r := s.mustSeed(t, "--mode", "upsert")
	if !strings.Contains(r.stdout, "(1 existing kept: their rows record other bytes)") {
		t.Errorf("upsert does not report the kept object:\n%s", r.stdout)
	}
	if v := s.mustSeed(t, "--verify"); !strings.Contains(v.stdout, " 9 PASS") {
		t.Fatalf("the upsert broke invariant 9:\n%s", v.stdout)
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
