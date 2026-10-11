//go:build integration

// Integration tests of T53 (issue #73): PostgreSQL 18 with the full goose chain (requests as logitrack_app, so RLS
// decides), Redis 7 (rate limits, job locks), the real auth service and RBAC, and the route groups exactly as cmd/api
// wires them (app.DashboardGroups, app.JobGroups) on both listeners: the dashboard summary and badges, the vehicle
// locations and the Cartrack probe (internal/fleet/locations; the worker's consumer runs in-process with a fake
// Cartrack client), and the anonymous forms with their rate limits (internal/forms).
//
// Fixture (logitrack_etl, BYPASSRLS): own fleet O; carrier A working for O (contractor reach); independent carrier C;
// staff of each (tenant_admin, and in O an operator and a user), a customer-scope user over party X, a platform admin;
// drivers, trucks with GPS ids, delivered trips over the last days, incidents, pending expenses, chats, maintenance.
package dashboard_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strings"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/dashboard"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/locations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/forms"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const pw = "a long passphrase 1"

// now is the pinned dashboard clock: 2026-10-11 10:00 in Bangkok.
var now = time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)

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
	rdb      redis.UniversalClient
	ks       cache.Keyspace
	internal string
	public   string
	hash     string

	O, A, C                uuid.UUID
	partyX                 uuid.UUID
	dO, dA, dC             uuid.UUID
	truckO, truckA, truckC uuid.UUID
	tokens                 map[string]string
}

type worldOpts struct {
	rateLimit bool
	formLimit ratelimit.Limit
	probeWait time.Duration
}

func newWorld(t *testing.T, o worldOpts) *world {
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
	limiter := ratelimit.New(rdb, ks, logger)
	svc, err := auth.New(auth.Config{
		RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour, PasswordResetTTL: 30 * time.Minute,
		RateLimitEnabled: o.rateLimit, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute},
	}, auth.Deps{
		Pool: w.pool, Store: auth.NewStore(rdb, ks.Prefix()), Limiter: limiter,
		Keys: token.New(priv, "http://localhost:8080", "logitrack-test", 15*time.Minute), Hasher: hasher,
		Policy: policy, Log: logger, Capabilities: rbac, Authorizer: rbac,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	jobSvc := jobs.NewService(w.pool, jobs.NewRedisLocker(rdb, ks))
	loc, err := locations.New(locations.Deps{Pool: w.pool, Jobs: jobSvc, Log: logger, ProbeWait: o.probeWait, ProbePoll: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	dash, err := dashboard.New(dashboard.Deps{Pool: w.pool, Log: logger, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	form, err := forms.New(forms.Deps{Pool: w.pool, Log: logger})
	if err != nil {
		t.Fatal(err)
	}
	deps := app.APIDeps{Auth: svc, Jobs: jobSvc, Locations: loc, Dashboard: dash, Forms: form, Limiter: limiter,
		RateLimitEnabled: o.rateLimit, PublicFormsLimit: o.formLimit}
	groups := append(svc.Groups(), app.JobGroups(deps)...)
	groups = append(groups, app.DashboardGroups(deps)...)
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		InternalAddr: "127.0.0.1:0", PublicAddr: "127.0.0.1:0", PublicRouteGroups: ingress.PublicPrefixes,
		// The VM trusts 127.0.0.1/32 (nginx and the BFF): the visitor's address comes from X-Forwarded-For.
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
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
	if _, err := w.etl.Exec(context.Background(), sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

func (w *world) id(sql string, args ...any) uuid.UUID {
	w.t.Helper()
	var v uuid.UUID
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

func (w *world) user(email string) uuid.UUID {
	return w.id(`INSERT INTO users (email, email_verified, display_name, password_hash)
		VALUES ($1::text, true, $1::text, $2) RETURNING id`, email, w.hash)
}

func (w *world) member(email string, tenant uuid.UUID, role string) {
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, w.user(email), tenant, role)
}

func (w *world) driver(tenant uuid.UUID, first, status string) uuid.UUID {
	return w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, status)
		VALUES ($1, 'self', $2, 'T', '0800000000', $3) RETURNING id`, tenant, first, status)
}

func (w *world) truck(tenant uuid.UUID, plate, gps string) uuid.UUID {
	return w.id(`INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand, model, year, color, type_raw,
		vehicle_class, gps_vehicle_id) VALUES ($1, 'self', $2, 'กรุงเทพมหานคร', 'active', 'ISUZU', 'FRR', 2020, 'white', '6 Wheels', '6WH',
		nullif($3, '')) RETURNING id`, tenant, plate, gps)
}

// trip inserts a delivered trip of tenant (delivered at; parcels; billing party, may be nil).
func (w *world) trip(tenant uuid.UUID, no string, at time.Time, parcels int, party *uuid.UUID) uuid.UUID {
	return w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, origin_raw, destination_raw,
		truck_license_plate_snapshot, parcel_count, delivered_at, billing_party_id, created_at)
		VALUES ($1, 'self', $2, 'delivered', 'first_mile', 'ALANG-A', 'SOCE', '1กข-1001', $3, $4, $5, $4) RETURNING id`,
		tenant, no, parcels, at, party)
}

func (w *world) incident(tenant, trip uuid.UUID, at time.Time) {
	w.exec(`INSERT INTO incident_reports (tenant_id, tenant_source, trip_id, delay_cause, created_at)
		VALUES ($1, 'trip', $2, 'incident_cause_traffic', $3)`, tenant, trip, at)
}

func bkk(y, m, d, h int) time.Time {
	return time.Date(y, time.Month(m), d, h, 0, 0, 0, time.FixedZone("ICT", 7*3600))
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, code) VALUES ('own_fleet', 'กองรถ', 'OWN') RETURNING id`)
	w.A = w.id(`INSERT INTO tenants (kind, name_th, code, legal_type, contractor_tenant_id) VALUES ('carrier', 'ผู้รับเหมา A', 'CA', 'company', $1) RETURNING id`, w.O)
	w.C = w.id(`INSERT INTO tenants (kind, name_th, code, legal_type) VALUES ('carrier', 'อิสระ C', 'CC', 'company') RETURNING id`)
	w.member("ta.o@example.test", w.O, "tenant_admin")
	w.member("op.o@example.test", w.O, "operator")
	w.member("us.o@example.test", w.O, "user")
	w.member("ta.a@example.test", w.A, "tenant_admin")
	w.member("ta.c@example.test", w.C, "tenant_admin")
	w.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, w.user("pa@example.test"))
	cust := w.id(`INSERT INTO customers (code, name) VALUES ('CJSF', 'CJSF') RETURNING id`)
	w.partyX = w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id`, cust)
	w.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'customer', $2)`, w.user("cs@example.test"), w.partyX)

	w.dO = w.driver(w.O, "สมชาย", "active")
	dO2 := w.driver(w.O, "สมศักดิ์", "on_duty")
	dO3 := w.driver(w.O, "สมพร", "inactive")
	w.dA = w.driver(w.A, "เอ", "active")
	w.dC = w.driver(w.C, "ซี", "active")
	w.truckO = w.truck(w.O, "1กข-1001", "1001")
	w.truckA = w.truck(w.A, "2กข-2002", "2002")
	w.truckC = w.truck(w.C, "3กข-3003", "3003")
	// Compliance: O's truck tax due in 5 days, insurance far away; A's truck (a carrier's) due too, but it is not O's own.
	w.exec(`UPDATE trucks SET tax_expiry_date = '2026-10-16', insurance_expiry_date = '2027-06-01', next_service_mileage = 100500,
		current_mileage = 100000 WHERE id = $1`, w.truckO)
	w.exec(`UPDATE trucks SET tax_expiry_date = '2026-10-13' WHERE id = $1`, w.truckA)

	// Trips: O three in the last week (one with an incident, one for party X), one the week before; A one; C two.
	t1 := w.trip(w.O, "O-1", bkk(2026, 10, 9, 9), 10, &w.partyX)
	w.trip(w.O, "O-2", bkk(2026, 10, 10, 9), 20, nil)
	t3 := w.trip(w.O, "O-3", bkk(2026, 10, 11, 8), 5, nil)
	w.trip(w.O, "O-old", bkk(2026, 10, 2, 9), 7, nil)
	w.trip(w.A, "A-1", bkk(2026, 10, 10, 12), 3, nil)
	w.trip(w.C, "C-1", bkk(2026, 10, 10, 13), 100, nil)
	c2 := w.trip(w.C, "C-2", bkk(2026, 10, 11, 7), 100, nil)
	w.incident(w.O, t3, bkk(2026, 10, 11, 9))
	w.incident(w.C, c2, bkk(2026, 10, 11, 8))
	_ = t1

	// Expenses awaiting the audit: O one fuel and one other, A one fuel, C one other; an approved one of O.
	for _, e := range []struct {
		tenant uuid.UUID
		driver uuid.UUID
		typ    string
		status string
	}{{w.O, w.dO, "fuel", "pending"}, {w.O, w.dO, "other", "pending"}, {w.A, w.dA, "fuel", "pending"}, {w.C, w.dC, "other", "pending"},
		{w.O, w.dO, "fuel", "approved"}} {
		w.exec(`INSERT INTO vehicle_expenses (tenant_id, tenant_source, driver_id, expense_type, expense_at, amount_thb, status)
			VALUES ($1, 'driver', $2, $3, now(), 100, $4)`, e.tenant, e.driver, e.typ, e.status)
	}
	// Chats (one open chat per driver, chats_one_open_per_driver): O unassigned urgent, O unassigned normal, O assigned,
	// O closed unassigned; C unassigned.
	staff := w.id(`SELECT id FROM users WHERE email = 'ta.o@example.test'`)
	w.exec(`INSERT INTO chats (tenant_id, driver_id, priority) VALUES ($1, $2, 'urgent')`, w.O, w.dO)
	w.exec(`INSERT INTO chats (tenant_id, driver_id, priority) VALUES ($1, $2, 'normal')`, w.O, dO2)
	w.exec(`INSERT INTO chats (tenant_id, driver_id, assigned_admin_user_id, assigned_at) VALUES ($1, $2, $3, now())`, w.O, dO3, staff)
	w.exec(`INSERT INTO chats (tenant_id, driver_id, status, closed_at) VALUES ($1, $2, 'closed', now())`, w.O, w.dO)
	w.exec(`INSERT INTO chats (tenant_id, driver_id) VALUES ($1, $2)`, w.C, w.dC)
	// Maintenance: one completed for O's truck.
	w.exec(`INSERT INTO maintenance_records (tenant_id, tenant_source, truck_id, maintenance_type, service_type, start_date, status)
		VALUES ($1, 'truck', $2, 'PM', 'เปลี่ยนถ่ายน้ำมันเครื่อง', '2026-10-01', 'completed')`, w.O, w.truckO)
}

// location stores a position for a truck (as the sync would).
func (w *world) location(truck, tenant uuid.UUID, gps string, lat float64) {
	w.exec(`INSERT INTO vehicle_locations (truck_id, tenant_id, tenant_source, gps_vehicle_id, lat, lng, last_gps_at)
		VALUES ($1, $2, 'truck', $3, $4, 100.5, now())`, truck, tenant, gps, lat)
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

// call sends a request; bearer may be ""; headers are name/value pairs.
func (w *world) call(base, method, path, bearer string, body any, headers ...string) resp {
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

func (w *world) get(path, bearer string) resp {
	w.t.Helper()
	return w.call(w.internal, http.MethodGet, path, bearer, nil)
}

// login signs in through POST /v1/auth/login (cached per email).
func (w *world) login(email string) string {
	w.t.Helper()
	if tok, ok := w.tokens[email]; ok {
		return tok
	}
	r := w.call(w.public, http.MethodPost, "/v1/auth/login", "", map[string]any{"email": email, "password": pw, "platform": "web"})
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

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
