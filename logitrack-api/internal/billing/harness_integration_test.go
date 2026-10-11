//go:build integration

// Integration tests of T37 (issue #57): rate tables, statements with invoice numbers and lines, the period-lock read,
// the pricing transaction's rate-table read and the Bangchak fuel snapshots, against PostgreSQL 18 with the full goose
// chain (pgtest; every request as logitrack_app through db.WithPrincipal, so RLS decides), Redis 7 (the rate-card
// bundle, the retail cache and the job lock), the real auth service and RBAC. The Bangchak feed is an in-process fake:
// no request leaves the test. Fixture: own fleet O (owner company, WHT 3 %); carrier A working for O (its own company,
// WHT 1 %); independent carrier C; customers X (code CJSF, plan basis, 30-day terms) and Y (code TTP, delivered basis)
// with their billing parties; staff of O (tenant_admin, user), A and C; a platform admin.
package billing_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/bangchak"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const pw = "a long passphrase 1"

// now is the pinned clock of the service: 2026-10-11 10:00 in Bangkok.
var now = time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) {
	app.DrainGrace = 0
	code := pgtest.Main(m)
	cachetest.TerminateShared()
	os.Exit(code)
}

// fakeFeed is the Bangchak client of the tests: canned prices or a failure, and a call count.
type fakeFeed struct {
	mu     sync.Mutex
	prices bangchak.Prices
	err    error
	calls  int
}

func (f *fakeFeed) RetailPrices(_ context.Context, locale string) (bangchak.Prices, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return bangchak.Prices{}, f.err
	}
	p := f.prices
	p.Locale = locale
	return p, nil
}

func (f *fakeFeed) set(p bangchak.Prices, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prices, f.err = p, err
}

func (f *fakeFeed) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type world struct {
	t        *testing.T
	db       *pgtest.Database
	pool     *pgxpool.Pool
	etl      *pgx.Conn
	rdb      *redis.Client
	ks       cache.Keyspace
	svc      *billing.Service
	feed     *fakeFeed
	internal string
	hash     string
	tokens   map[string]string

	O, A, C                   string // tenants
	customerX, customerY      string // customers
	partyX, partyY            string // their billing parties
	companyO, companyA        string // invoice issuers
	userTAO, userTAA, userTAC string // tenant admins of O, A, C
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, db: d, pool: d.Pool(t, db.RoleApp), tokens: map[string]string{}, feed: &fakeFeed{}}
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
		Policy: policy, Log: zerolog.Nop(), Capabilities: rbac, Authorizer: rbac,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	jobSvc := jobs.NewService(w.pool, jobs.NewRedisLocker(w.rdb, w.ks))
	w.svc, err = billing.New(billing.Deps{Pool: w.pool, Cache: caches, Jobs: jobSvc, Bangchak: w.feed,
		Now: func() time.Time { return now }, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	deps := app.APIDeps{Auth: svc, Jobs: jobSvc, Billing: w.svc}
	groups := append(svc.Groups(), billing.Groups(w.svc, svc.RequireAuth())...)
	groups = append(groups, app.JobGroups(deps)...)
	groups = append(groups, ingress.Group{Prefix: "/v1/billingtest", Mount: w.testRoutes(svc.RequireAuth())})
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		InternalAddr: "127.0.0.1:0", PublicAddr: "127.0.0.1:0", PublicRouteGroups: ingress.PublicPrefixes,
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

func scan[T any](w *world, sql string, args ...any) T {
	w.t.Helper()
	var v T
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func (w *world) user(email string) string {
	return w.id(`INSERT INTO users (email, email_verified, display_name, password_hash)
		VALUES ($1::text, true, $1::text, $2) RETURNING id::text`, email, w.hash)
}

func (w *world) member(name, tenant, role string) string {
	u := w.user(name + "@example.test")
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, u, tenant, role)
	return u
}

func (w *world) carrier(name string, contractor *string) string {
	t := w.id(`INSERT INTO tenants (kind, name_th, name_en, code, legal_type, contractor_tenant_id)
		VALUES ('carrier', $1::text, $1::text, $1::text::citext, 'company', $2) RETURNING id::text`, name, contractor)
	w.exec(`INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', $1)`, t)
	return t
}

func (w *world) company(tenant, kind string, wht float64) string {
	return w.id(`INSERT INTO companies (tenant_id, tenant_source, name_th, tax_id, branch_type, address,
		withholding_tax_rate, company_type) VALUES ($1, 'self', 'บริษัท ' || $2::text, '0105556000001', 'headquarters', 'Bangkok', $3, $2::text)
		RETURNING id::text`, tenant, kind, wht)
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'O', 'O') RETURNING id::text`)
	w.A = w.carrier("A", &w.O)
	w.C = w.carrier("C", nil)
	w.customerX = w.id(`INSERT INTO customers (code, name, payment_terms_days) VALUES ('CJSF', 'CJ Super Fresh', 30) RETURNING id::text`)
	w.partyX = w.id(`INSERT INTO billing_parties (kind, customer_id, billing_date_basis) VALUES ('customer', $1, 'plan')
		RETURNING id::text`, w.customerX)
	w.customerY = w.id(`INSERT INTO customers (code, name) VALUES ('TTP', 'TTP Co.') RETURNING id::text`)
	w.partyY = w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, w.customerY)
	w.companyO = w.company(w.O, "owner", 3)
	w.companyA = w.company(w.A, "subcontractor", 1)
	w.userTAO = w.member("ta.o", w.O, "tenant_admin")
	w.member("us.o", w.O, "user")
	w.userTAA = w.member("ta.a", w.A, "tenant_admin")
	w.userTAC = w.member("ta.c", w.C, "tenant_admin")
	w.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, w.user("pa@example.test"))
}

// trip inserts a delivered trip of tenant (as the ETL or T38 would) and returns its id.
func (w *world) trip(tenant, party, tripNo string, deliveredAt time.Time, billingDate *time.Time, jobCategory *string) string {
	return w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, job_category, billing_party_id,
		billing_date, delivered_at, created_at) VALUES ($1, 'driver', $2, 'delivered', 'line_haul', $3, $4, $5, $6, $6)
		RETURNING id::text`, tenant, tripNo, jobCategory, party, billingDate, deliveredAt)
}

// snapshot inserts the price snapshot of a trip, carried by billingTenant (R61).
func (w *world) snapshot(trip, billingTenant string, estimate *float64, multi bool) {
	w.exec(`INSERT INTO trip_billing_snapshots (trip_id, tenant_id, estimate_thb, unpriced_reason, is_multi_delivery,
		computed_by) VALUES ($1, $2, $3, CASE WHEN $3::numeric IS NULL THEN 'no_rate' END, $4, 'scheduler')`,
		trip, billingTenant, estimate, multi)
}

func (w *world) stop(trip string, idx int, dest string, final float64) {
	w.exec(`INSERT INTO trip_billing_stop_breakdown (trip_id, stop_index, destination_code, base_rate_thb, final_rate_thb)
		VALUES ($1, $2, $3, $4, $4)`, trip, idx, dest, final)
}

// standby inserts a completed, priced standby record of tenant, priced from rateEntry.
func (w *world) standby(tenant, party, rateEntry string, endedAt time.Time, estimate float64) string {
	return w.id(`INSERT INTO standby_records (tenant_id, tenant_source, customer_party_id, ended_at, started_at, status,
		billing_estimate_thb, billing_party_id, billing_rate_source, billing_rate_entry_id, billing_computed_at)
		VALUES ($1, 'driver', $2, $3::timestamptz, $3::timestamptz - interval '2 hours', 'completed', $4, $2, 'standby_rate', $5, $3::timestamptz) RETURNING id::text`,
		tenant, party, endedAt, estimate, rateEntry)
}

// --- test routes: SQL in db.WithPrincipal, so only RLS, triggers and grants decide -------------------------

var mutations = map[string]string{
	"update-rate":       `UPDATE customer_rate_entries SET rate_thb = rate_thb + 1`,
	"void-rate-and-pay": `UPDATE customer_rate_entries SET voided = true, voided_at = now(), voided_by = app_user_id(), voided_reason = 'x', rate_thb = 0`,
	"void-rate":         `UPDATE customer_rate_entries SET voided = true, voided_at = now(), voided_by = app_user_id(), voided_reason = 'x', updated_at = now() WHERE NOT voided`,
	"void-rate-no-by":   `UPDATE customer_rate_entries SET voided = true, voided_at = now(), voided_reason = 'x' WHERE NOT voided`,
	"unvoid-rate":       `UPDATE customer_rate_entries SET voided = false, voided_at = NULL, voided_by = NULL WHERE voided`,
	"delete-rate":       `DELETE FROM customer_rate_entries`,
	"update-fuel":       `UPDATE customer_fuel_rate_adjustments SET add_thb_per_trip = add_thb_per_trip + 1`,
	"delete-fuel":       `DELETE FROM customer_fuel_rate_adjustments`,
	"delete-standby":    `DELETE FROM standby_rate_entries`,
	"update-line":       `UPDATE billing_statement_lines SET amount_thb = 0`,
	"read-counters":     `SELECT * FROM billing_counters`,
	"bump-counter":      `UPDATE billing_counters SET last_seq = 0`,
}

func (w *world) testRoutes(requireAuth fiber.Handler) func(r fiber.Router) {
	return func(r fiber.Router) {
		r.Use(requireAuth)
		// A write the API never makes, rolled back: the sqlstate PostgreSQL answers for the caller, and the rows it touched.
		r.Post("/mutate/:what", func(c fiber.Ctx) error {
			q, ok := mutations[c.Params("what")]
			if !ok {
				return httpx.ErrNotFound()
			}
			state, n := "", int64(0)
			err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
				tag, err := tx.Exec(c.Context(), q)
				var pe *pgconn.PgError
				if errors.As(err, &pe) {
					state = pe.Code
				} else if err != nil {
					return err
				}
				n = tag.RowsAffected()
				return errRollback
			})
			if err != nil && !errors.Is(err, errRollback) {
				return err
			}
			return httpx.JSON(c, http.StatusOK, map[string]any{"sqlstate": state, "rows": n})
		})
	}
}

var errRollback = errors.New("rollback")

// mutate runs a named mutation as user and returns the SQLSTATE ("" = accepted) and the rows touched.
func (w *world) mutate(user, what string) (string, int) {
	w.t.Helper()
	r := w.post("/v1/billingtest/mutate/"+what, w.login(user), nil)
	expect(w.t, r, http.StatusOK, "")
	rows, _ := r.data()["rows"].(float64)
	return str(r.data()["sqlstate"]), int(rows)
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
	req, err := http.NewRequest(method, w.internal+path, rd)
	if err != nil {
		w.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
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
	return w.do(http.MethodGet, path, bearer, nil)
}

func (w *world) post(path, bearer string, body any) resp {
	w.t.Helper()
	return w.do(http.MethodPost, path, bearer, body)
}

func (w *world) patch(path, bearer string, body any) resp {
	w.t.Helper()
	return w.do(http.MethodPatch, path, bearer, body)
}

func (w *world) del(path, bearer string) resp {
	w.t.Helper()
	return w.do(http.MethodDelete, path, bearer, nil)
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

func str(v any) string { s, _ := v.(string); return s }

func num(v any) float64 { f, _ := v.(float64); return f }

func ptr[T any](v T) *T { return &v }

// importRows imports rows for party as user and returns the import id.
func (w *world) importRows(user, party, day string, rows ...map[string]any) string {
	w.t.Helper()
	r := w.post("/v1/billing/rate-entries/import", w.login(user), map[string]any{"customerId": party,
		"effectiveFromDateStr": day, "rows": rows})
	expect(w.t, r, http.StatusCreated, "")
	if int(num(r.data()["count"])) != len(rows) {
		w.t.Fatalf("import count %s, want %d", r.raw, len(rows))
	}
	return str(r.data()["importId"])
}

func rateRow(hub, dest, class string, rate float64, cat string) map[string]any {
	return map[string]any{"hubId": hub, "rawHubName": hub, "destinationCode": dest, "vehicleClass": class,
		"rateThb": rate, "jobCategory": cat}
}
