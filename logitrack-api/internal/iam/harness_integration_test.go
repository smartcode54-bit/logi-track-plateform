//go:build integration

// Integration tests of T07 (Appendix C §C.9.1, §C.9.2): PostgreSQL 18 with the full goose chain
// (pgtest, requests as logitrack_app), Redis 7 (cachetest), the real auth service issuing tokens, and the
// RBAC authorizer completing every request. Test routes under /v1/rbactest run their SQL in
// db.WithPrincipal, so RLS decides with the GUCs a real request sets.
package iam_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const pw = "a long passphrase 1"

func TestMain(m *testing.M) {
	app.DrainGrace = 0
	code := pgtest.Main(m)
	cachetest.TerminateShared()
	os.Exit(code)
}

// world is one database with the fixture of Appendix C §C.9.2 (trimmed to what T07 asserts): own fleet
// O; carriers A and B working for O (contractor reach); independent carrier C; dispatcher organisation
// D; quarantine Q; customers X and Y; one task per tenant and party, plus carrier-internal rows.
type world struct {
	t        *testing.T
	d        *pgtest.Database
	pool     *pgxpool.Pool
	etl      *pgx.Conn
	rdb      *redis.Client
	rbac     *iam.RBAC
	internal string
	public   string
	hash     string

	O, A, B, C, D, Q string // tenant ids
	X, Y             string // billing party ids
	tasks            map[string]string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, d: d, pool: d.Pool(t, db.RoleApp), tasks: map[string]string{}}
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

	var ks cache.Keyspace
	w.rdb, ks = cachetest.NewClient(t)
	caches := cache.New(w.rdb, ks)
	if w.rbac, err = iam.NewRBAC(iam.Deps{Pool: w.pool, Cache: caches, Redis: w.rdb, Log: zerolog.Nop()}); err != nil {
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
	svc, err := auth.New(auth.Config{
		RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour, PasswordResetTTL: 30 * time.Minute,
		RateLimitEnabled: false,
	}, auth.Deps{
		Pool: w.pool, Store: auth.NewStore(w.rdb, ks.Prefix()), Limiter: ratelimit.New(w.rdb, ks, zerolog.Nop()),
		Keys: token.New(priv, "http://localhost:8080", "logitrack-test", 15*time.Minute), Hasher: hasher,
		Policy: policy, Log: zerolog.Nop(), Capabilities: w.rbac, Authorizer: w.rbac,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	groups := append(svc.Groups(), iam.RoleGroups(svc.RequireAuth())...)
	groups = append(groups, ingress.Group{Prefix: "/v1/rbactest", Mount: w.testRoutes(svc.RequireAuth())})
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		InternalAddr: "127.0.0.1:0", PublicAddr: "127.0.0.1:0", PublicRouteGroups: ingress.PublicPrefixes,
	}
	a, err := app.NewAPI(cfg, zerolog.Nop(), groups...)
	if err != nil {
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

func (w *world) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (w *world) carrier(name string, contractor *string) string {
	return w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type, contractor_tenant_id)
		VALUES ('carrier', $1, $1, 'company', $2) RETURNING id::text`, name, contractor)
}

func (w *world) party(code string) string {
	c := w.id(`INSERT INTO customers (code, name) VALUES ($1::text, $1::text || ' Co.') RETURNING id::text`, code)
	return w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, c)
}

func (w *world) task(label, tenant string, party *string) {
	w.tasks[label] = w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw,
		destination_raw, billing_party_id) VALUES ($1, 'form', 'first_mile', 'pending', now(), 'SRC', 'DST', $2)
		RETURNING id::text`, tenant, party)
}

// user creates an active user that signs in with pw; roles are memberships tenant -> role.
func (w *world) user(email string) string {
	return w.id(`INSERT INTO users (email, email_verified, display_name, password_hash)
		VALUES ($1::text, true, $1::text, $2) RETURNING id::text`, email, w.hash)
}

func (w *world) member(user, tenant, role string) {
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, user, tenant, role)
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'O', 'O') RETURNING id::text`)
	w.A, w.B = w.carrier("A", &w.O), w.carrier("B", &w.O)
	w.C, w.D = w.carrier("C", nil), w.carrier("D", nil)
	w.Q = w.id(`SELECT app_quarantine_tenant_id()::text`)
	w.X, w.Y = w.party("X"), w.party("Y")
	for _, tn := range []struct{ label, tenant string }{{"O", w.O}, {"A", w.A}, {"B", w.B}, {"C", w.C}, {"D", w.D}, {"Q", w.Q}} {
		w.task(tn.label+"x", tn.tenant, &w.X)
		w.task(tn.label+"y", tn.tenant, &w.Y)
	}
	// Carrier-internal rows of A in scope X: a service fee and a penalty (never readable by a scope principal).
	w.exec(`INSERT INTO customer_service_fees (tenant_id, billing_party_id, fee_type, amount_thb, unit)
		VALUES ($1, $2, 'extra_stop', 50, 'per_stop')`, w.A, w.X)
	drv := w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile)
		VALUES ($1, 'self', 'สมชาย', 'ใจดี', '0800000001') RETURNING id::text`, w.A)
	w.exec(`INSERT INTO driver_penalties (tenant_id, driver_id, type_code, total_thb, remaining_thb, incurred_at)
		VALUES ($1, $2, 'late', 100, 100, now())`, w.A, drv)

	for _, u := range []struct{ email, tenant, role string }{
		{"ta.o@example.test", w.O, "tenant_admin"}, {"mg.o@example.test", w.O, "manager"},
		{"op.o@example.test", w.O, "operator"}, {"ta.a@example.test", w.A, "tenant_admin"},
		{"mg.a@example.test", w.A, "manager"}, {"op.a@example.test", w.A, "operator"},
		{"ta.c@example.test", w.C, "tenant_admin"},
	} {
		w.member(w.user(u.email), u.tenant, u.role)
	}
	ds := w.user("ds@example.test") // dispatcher: operator of its own organisation D + a grant over X (R13)
	w.member(ds, w.D, "operator")
	w.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'dispatcher', $2)`, ds, w.X)
	cu := w.user("cu@example.test") // customer scope over X, no membership
	w.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'customer', $2)`, cu, w.X)
	w.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, w.user("pa@example.test"))
	w.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'support')`, w.user("su@example.test"))
}

// --- test routes (handlers run their SQL in db.WithPrincipal) -------------------------------------------

type whoami struct {
	Caps        []string `json:"caps"`
	Steward     bool     `json:"steward"`
	TenantKind  string   `json:"tenantKind"`
	Subtenants  []string `json:"subtenants"`
	ActOnTenant string   `json:"actOnTenant"`
	ActOnAll    bool     `json:"actOnAll"`
}

// tableCounts are SELECT count(*) of tables a test may name (no caller-supplied SQL).
var tableCounts = map[string]string{
	"tasks":                 `SELECT count(*) FROM tasks`,
	"customer_service_fees": `SELECT count(*) FROM customer_service_fees`,
	"driver_penalties":      `SELECT count(*) FROM driver_penalties`,
	"customers":             `SELECT count(*) FROM customers`,
}

func (w *world) testRoutes(requireAuth fiber.Handler) func(r fiber.Router) {
	return func(r fiber.Router) {
		r.Use(requireAuth)
		r.Get("/whoami", func(c fiber.Ctx) error {
			p := authz.PrincipalFrom(c)
			out := whoami{Caps: p.Caps.Strings(), Steward: p.Steward, TenantKind: p.TenantKind, ActOnAll: p.ActOnAll}
			for _, s := range p.SubtenantIDs {
				out.Subtenants = append(out.Subtenants, s.String())
			}
			if p.ActOnTenant != nil {
				out.ActOnTenant = p.ActOnTenant.String()
			}
			return httpx.JSON(c, http.StatusOK, out)
		})
		// tasks per tenant, read with no predicate at all (and with ?tenant= a crafted predicate naming one
		// tenant): only RLS decides. ?write=1 also tries an UPDATE in the same transaction.
		r.Get("/tasks", func(c fiber.Ctx) error {
			out := map[string]any{}
			err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
				rows, err := tx.Query(c.Context(), `SELECT tenant_id::text, count(*) FROM tasks
					WHERE $1::text = '' OR tenant_id = nullif($1, '')::uuid OR true GROUP BY 1`, c.Query("tenant"))
				if err != nil {
					return err
				}
				perTenant := map[string]int{}
				for rows.Next() {
					var tid string
					var n int
					if err := rows.Scan(&tid, &n); err != nil {
						return err
					}
					perTenant[tid] = n
				}
				if err := rows.Err(); err != nil {
					return err
				}
				out["perTenant"] = perTenant
				if c.Query("write") == "1" { // in a savepoint, so the refused write leaves the transaction usable
					sp, err := tx.Begin(c.Context())
					if err != nil {
						return err
					}
					_, err = sp.Exec(c.Context(), `UPDATE tasks SET updated_at = now()`)
					out["writeSQLState"] = sqlState(err)
					if err := sp.Rollback(c.Context()); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			return httpx.JSON(c, http.StatusOK, out)
		})
		r.Get("/count/:table", func(c fiber.Ctx) error {
			q, ok := tableCounts[c.Params("table")]
			if !ok {
				return httpx.ErrNotFound()
			}
			var n int
			err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
				return tx.QueryRow(c.Context(), q).Scan(&n)
			})
			if err != nil {
				return err
			}
			return httpx.JSON(c, http.StatusOK, map[string]int{"count": n})
		})
		insertCustomer := func(c fiber.Ctx) error {
			var id string
			err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
				return tx.QueryRow(c.Context(), `INSERT INTO customers (code, name) VALUES ($1::text, $1::text) RETURNING id::text`,
					c.Query("code")).Scan(&id)
			})
			if s := sqlState(err); s == "42501" {
				return authz.ErrPermissionDenied("row-level security").WithDetails(map[string]any{"sqlstate": s})
			} else if err != nil {
				return err
			}
			return httpx.JSON(c, http.StatusCreated, map[string]string{"id": id})
		}
		r.Post("/customers", authz.RequireCap(authz.FleetManageCustomers), insertCustomer)
		r.Post("/customers-unguarded", insertCustomer) // RLS alone (C.9.2 #14)
		insertHoliday := func(c fiber.Ctx) error {     // a PUBLIC holiday: tenant_id NULL (C.9.2 #15)
			err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
				_, err := tx.Exec(c.Context(), `INSERT INTO holidays (tenant_id, holiday_date, holiday_type, name)
					VALUES (NULL, $1::date, 'public', 'Public')`, c.Query("date"))
				return err
			})
			if s := sqlState(err); s == "42501" {
				return authz.ErrPermissionDenied("row-level security").WithDetails(map[string]any{"sqlstate": s})
			} else if err != nil {
				return err
			}
			return httpx.JSON(c, http.StatusCreated, map[string]bool{"created": true})
		}
		r.Post("/public-holiday", authz.RequireTenant(), authz.RequireCap(authz.HRManageHolidays), authz.RequireSteward(), insertHoliday)
		r.Post("/public-holiday-unguarded", insertHoliday)
		r.Get("/rate-card", authz.RequireTenant(), authz.RequireCap(authz.AccountingViewRateCard), func(c fiber.Ctx) error {
			return c.SendStatus(http.StatusNoContent)
		})
		r.Post("/tasks", authz.RequireTenant(), authz.RequireCap(authz.OperationsManageTasks), func(c fiber.Ctx) error {
			return c.SendStatus(http.StatusNoContent)
		})
		r.Get("/scope/tasks", func(c fiber.Ctx) error {
			var rows []scopedb.ScopeTask
			err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) (err error) {
				rows, err = scopedb.New(tx).ListScopeTasks(c.Context(), scopedb.ListScopeTasksParams{RowLimit: 100})
				return err
			})
			if err != nil {
				return err
			}
			return httpx.JSON(c, http.StatusOK, rows)
		})
	}
}

func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
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

func (r resp) data() any { return r.body["data"] }

func (w *world) call(base, method, path, bearer string, headers ...string) resp {
	w.t.Helper()
	req, err := http.NewRequest(method, base+path, bytes.NewReader(nil))
	if err != nil {
		w.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, raw: raw}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			w.t.Fatalf("%s %s: not JSON: %s", method, path, raw)
		}
	}
	return out
}

func (w *world) get(path, bearer string, headers ...string) resp {
	w.t.Helper()
	return w.call(w.internal, http.MethodGet, path, bearer, headers...)
}

func (w *world) post(path, bearer string, headers ...string) resp {
	w.t.Helper()
	return w.call(w.internal, http.MethodPost, path, bearer, headers...)
}

// login signs in through POST /v1/auth/login and returns the access token.
func (w *world) login(email string) string {
	w.t.Helper()
	b, _ := json.Marshal(map[string]any{"email": email, "password": pw, "platform": "web"})
	res, err := http.Post(w.internal+"/v1/auth/login", "application/json", bytes.NewReader(b))
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || json.Unmarshal(raw, &body) != nil || body.Data.AccessToken == "" {
		w.t.Fatalf("login %s: %d %s", email, res.StatusCode, raw)
	}
	return body.Data.AccessToken
}

func (w *world) whoami(bearer string, headers ...string) whoami {
	w.t.Helper()
	r := w.get("/v1/rbactest/whoami", bearer, headers...)
	if r.status != http.StatusOK {
		w.t.Fatalf("whoami: %d %s", r.status, r.raw)
	}
	b, _ := json.Marshal(r.data())
	var out whoami
	_ = json.Unmarshal(b, &out)
	return out
}

// perTenant reads GET /v1/rbactest/tasks as tenant id -> visible tasks.
func (w *world) perTenant(bearer, query string, headers ...string) map[string]int {
	w.t.Helper()
	r := w.get("/v1/rbactest/tasks"+query, bearer, headers...)
	if r.status != http.StatusOK {
		w.t.Fatalf("tasks: %d %s", r.status, r.raw)
	}
	m, _ := r.data().(map[string]any)["perTenant"].(map[string]any)
	out := map[string]int{}
	for k, v := range m {
		out[k] = int(v.(float64))
	}
	return out
}

func (w *world) securityEvents(eventType string) int {
	w.t.Helper()
	return w.count(`SELECT count(*) FROM security_events WHERE event_type = $1`, eventType)
}

func expect(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status || (code != "" && r.code() != code) {
		t.Fatalf("want %d %s, got %d %s", status, code, r.status, r.raw)
	}
}

func name(w *world, tid string) string {
	for n, id := range map[string]string{"O": w.O, "A": w.A, "B": w.B, "C": w.C, "D": w.D, "Q": w.Q} {
		if id == tid {
			return n
		}
	}
	return fmt.Sprintf("?%s", tid)
}

// named turns a per-tenant map into tenant letters -> count, for messages and comparisons.
func named(w *world, m map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range m {
		out[name(w, k)] = v
	}
	return out
}

// keysOf lists the tenant letters with visible rows, sorted.
func keysOf(m map[string]int) string {
	return strings.Join(slices.Sorted(maps.Keys(m)), " ")
}
