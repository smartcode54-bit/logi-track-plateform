//go:build integration

// Integration tests of T21 (issue #41): hubs, name maps, distances and the distances.compute job against PostgreSQL 18
// with the full goose chain (pgtest; every request as logitrack_app through db.WithPrincipal, so RLS decides), Redis 7
// (the hub caches and the job lock), the real auth service and RBAC. The Distance Matrix is an in-process fake: no
// request leaves the test. Fixture: own fleet O; carrier A working for O (its manager holds overrides of both steward
// keys); independent carrier C; customers X (code SPK) and Y with their billing parties; staff of O, A and C, a driver
// of O, a customer-scope user over X and a platform admin.
package hubs_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/gmaps"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const pw = "a long passphrase 1"

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
	rdb      *redis.Client
	ks       cache.Keyspace
	svc      *hubs.Service
	internal string
	hash     string
	tokens   map[string]string

	O, A, C        string // tenants
	custX, custY   string // customers
	partyX, partyY string // their billing parties
	partyC         string // carrier C's billing party
}

func newWorld(t *testing.T) *world {
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
	w.svc, err = hubs.New(hubs.Deps{Pool: w.pool, Cache: caches, Jobs: jobSvc, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	deps := app.APIDeps{Auth: svc, Jobs: jobSvc, Hubs: w.svc}
	groups := append(svc.Groups(), hubs.Groups(w.svc, svc.RequireAuth())...)
	groups = append(groups, app.JobGroups(deps)...)
	groups = append(groups, ingress.Group{Prefix: "/v1/hubstest", Mount: w.testRoutes(svc.RequireAuth())})
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

func (w *world) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (w *world) user(email string) string {
	return w.id(`INSERT INTO users (email, email_verified, display_name, password_hash)
		VALUES ($1::text, true, $1::text, $2) RETURNING id::text`, email, w.hash)
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'O', 'O') RETURNING id::text`)
	w.A = w.id(`INSERT INTO tenants (kind, code, name_th, name_en, legal_type, contractor_tenant_id)
		VALUES ('carrier', 'CA', 'A', 'A', 'company', $1) RETURNING id::text`, w.O)
	w.C = w.id(`INSERT INTO tenants (kind, code, name_th, name_en, legal_type) VALUES ('carrier', 'CC', 'ซี', 'C', 'company') RETURNING id::text`)
	w.partyC = w.id(`INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', $1) RETURNING id::text`, w.C)
	w.custX = w.id(`INSERT INTO customers (code, name) VALUES ('SPK', 'J&T Express') RETURNING id::text`)
	w.partyX = w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, w.custX)
	w.custY = w.id(`INSERT INTO customers (code, name) VALUES ('TTP', 'TTP Co.') RETURNING id::text`)
	w.partyY = w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, w.custY)
	users := map[string]string{}
	for _, u := range []struct{ email, tenant, role string }{
		{"ta.o", w.O, "tenant_admin"}, {"ta2.o", w.O, "tenant_admin"}, {"mg.o", w.O, "manager"}, {"op.o", w.O, "operator"},
		{"ta.a", w.A, "tenant_admin"}, {"mg.a", w.A, "manager"}, {"op.a", w.A, "operator"}, {"ta.c", w.C, "tenant_admin"},
		{"dr.o", w.O, "driver"},
	} {
		users[u.email] = w.user(u.email + "@example.test")
		w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, users[u.email], u.tenant, u.role)
	}
	w.exec(`INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, mobile)
		VALUES ($1, 'self', $2, 'สมชาย', 'ใจดี', '0812345678')`, w.O, users["dr.o"])
	// The carrier's manager is granted both steward keys by tenant overrides: the steward rule still strips them (R60).
	for _, c := range []string{"operations:manage_sources", "operations:calculate_distances"} {
		w.exec(`INSERT INTO role_capability_overrides (tenant_id, role, capability, allowed, updated_by)
			VALUES ($1, 'manager', $2, true, $3)`, w.A, c, users["ta.a"])
	}
	cu := w.user("cu@example.test") // customer scope over X, no membership
	w.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'customer', $2)`, cu, w.partyX)
	w.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, w.user("pa@example.test"))
}

// hub inserts a hub as the ETL would (with its Thai-name alias).
func (w *world) hub(code, nameTh, station, network string, lat, lng *float64, party *string) string {
	w.t.Helper()
	id := w.id(`INSERT INTO hubs (source_id, name_th, station_type, network, latitude, longitude, linked_party_id)
		VALUES ($1::text, $2, $3, $4, $5, $6, $7) RETURNING id::text`, code, nameTh, station, network, lat, lng, party)
	w.exec(`INSERT INTO hub_name_aliases (alias, hub_id, source) VALUES ($1::text, $2, 'name_th') ON CONFLICT DO NOTHING`, nameTh, id)
	return id
}

func f(v float64) *float64 { return &v }

// --- test routes: SQL in db.WithPrincipal (RLS alone decides) and the driver's hub ---------------------------

var errRollback = errors.New("rollback")

func (w *world) testRoutes(requireAuth fiber.Handler) func(r fiber.Router) {
	return func(r fiber.Router) {
		r.Use(requireAuth)
		// A hub insert with no capability guard, rolled back: RLS alone answers (steward writes, R60).
		r.Post("/insert-hub", func(c fiber.Ctx) error {
			state := ""
			err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
				_, err := tx.Exec(c.Context(), `INSERT INTO hubs (source_id, name_th, network) VALUES ('RLS1', 'RLS', 'SPX')`)
				var pe *pgconn.PgError
				if errors.As(err, &pe) {
					state = pe.Code
				} else if err != nil {
					return err
				}
				return errRollback
			})
			if err != nil && !errors.Is(err, errRollback) {
				return err
			}
			return httpx.JSON(c, http.StatusOK, map[string]string{"sqlstate": state})
		})
		// Service.Get (the RLS-backed read of one hub, the payload of the writes) with the request's principal.
		r.Get("/hub/:id", func(c fiber.Ctx) error {
			id, err := uuid.Parse(c.Params("id"))
			if err != nil {
				return httpx.ErrNotFound()
			}
			out, err := w.svc.Get(c.Context(), authz.PrincipalFrom(c), id)
			if err != nil {
				return err
			}
			return httpx.JSON(c, http.StatusOK, out)
		})
		// The service of POST /v1/mobile/hubs (mounted at P7a) with the request's principal.
		r.Post("/driver-hub", func(c fiber.Ctx) error {
			var in hubs.DriverHubInput
			if err := httpx.DecodeJSON(c, &in); err != nil {
				return err
			}
			out, err := w.svc.CreateDriverHub(c.Context(), authz.PrincipalFrom(c), httpx.RequestIDFrom(c), in)
			if err != nil {
				return err
			}
			return httpx.JSON(c, http.StatusCreated, out)
		})
	}
}

// --- HTTP ---------------------------------------------------------------------------------------------

type resp struct {
	status int
	header http.Header
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

func (w *world) do(method, path, bearer string, body any, headers ...string) resp {
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
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: raw}
	if len(raw) > 0 && strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			w.t.Fatalf("%s %s: not JSON: %s", method, path, raw)
		}
	}
	return out
}

func (w *world) get(path, bearer string, headers ...string) resp {
	w.t.Helper()
	return w.do(http.MethodGet, path, bearer, nil, headers...)
}

// getRaw sends path exactly as given on the request line (a malformed escape such as %ZZ cannot go through
// http.NewRequest).
func (w *world) getRaw(path, bearer string) resp {
	w.t.Helper()
	req, err := http.NewRequest(http.MethodGet, w.internal+"/", nil)
	if err != nil {
		w.t.Fatal(err)
	}
	req.URL.Opaque = path
	req.Header.Set("Authorization", "Bearer "+bearer)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: raw}
	if len(raw) > 0 && strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}

func (w *world) post(path, bearer string, body any, headers ...string) resp {
	w.t.Helper()
	return w.do(http.MethodPost, path, bearer, body, headers...)
}

func (w *world) patch(path, bearer string, body any) resp {
	w.t.Helper()
	return w.do(http.MethodPatch, path, bearer, body)
}

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

func byCode(items []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, it := range items {
		out[str(it["sourceId"])] = it
	}
	return out
}

// --- AC: UNIQUE(source_id), duplicate -> 409; the single DTO; ETag; cache invalidation ---------------------

func TestHubWritesAndTheSingleDTO(t *testing.T) {
	w := newWorld(t)
	op := w.login("op.o")
	r := w.post("/v1/hubs", op, map[string]any{"sourceId": " alang-a ", "sourceNameTh": "อาลัง เอ", "sourceNameEn": "Alang A",
		"latitude": 13.75, "longitude": 100.6, "stationType": "HUB", "linkedCustomerId": w.custY})
	expect(t, r, http.StatusCreated, "")
	h := r.data()
	id := str(h["id"])
	if h["sourceId"] != "ALANG-A" || h["network"] != "SPX" || h["linkedPartyId"] != w.partyY || h["linkedCustomerId"] != w.custY ||
		h["linkedCustomerKind"] != "customer" || h["linkedCustomerName"] != "TTP — TTP Co." || h["createdByDriver"] != false {
		t.Fatalf("created: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'hubs.changed' AND payload->'ids' ? $1
		AND 'global' = ANY (realtime_topics)`, id); n != 1 {
		t.Fatalf("hubs.changed events: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM hub_name_aliases WHERE hub_id = $1`, id); n != 2 {
		t.Fatalf("aliases: %d", n)
	}
	// UNIQUE(source_id), case-insensitive: 409 already_exists on the field.
	r = w.post("/v1/hubs", op, map[string]any{"sourceId": "Alang-A", "sourceNameTh": "ซ้ำ"})
	expect(t, r, http.StatusConflict, "already_exists")
	if r.detail("field") != "sourceId" {
		t.Fatalf("409 details: %s", r.raw)
	}
	// Validation: Thai name required, coordinates both or neither, unknown station type, unknown party.
	r = w.post("/v1/hubs", op, map[string]any{"sourceId": "B1", "latitude": 13, "stationType": "DEPOT", "linkedPartyId": uuid.NewString()})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if fs := r.fields(); fs["sourceNameTh"] != "required" || fs["stationType"] != "invalid" {
		t.Fatalf("422: %s", r.raw)
	}
	expect(t, w.post("/v1/hubs", op, map[string]any{"sourceId": "B1", "sourceNameTh": "บี", "latitude": 13}), http.StatusUnprocessableEntity, "invalid_argument")
	r = w.post("/v1/hubs", op, map[string]any{"sourceId": "B1", "sourceNameTh": "บี", "linkedPartyId": uuid.NewString()})
	if r.status != http.StatusUnprocessableEntity || r.fields()["linkedPartyId"] != "not_found" {
		t.Fatalf("unknown party: %s", r.raw)
	}

	// GET /v1/hubs: one list with ETag; If-None-Match answers 304.
	r = w.get("/v1/hubs", op)
	expect(t, r, http.StatusOK, "")
	etag := r.header.Get("ETag")
	if etag == "" || len(r.list()) != 1 {
		t.Fatalf("list: %s %v", r.raw, r.header)
	}
	if r2 := w.get("/v1/hubs", op, "If-None-Match", etag); r2.status != http.StatusNotModified || len(r2.raw) != 0 {
		t.Fatalf("revalidation: %d %s", r2.status, r2.raw)
	}
	if w.rdb.Exists(context.Background(), w.ks.HubsAll()).Val() != 1 {
		t.Fatal("cache:hubs:all was not stored")
	}
	// PATCH: renames (the alias follows), links to X (code SPK -> network SPK) and drops the cached list.
	r = w.patch("/v1/hubs/"+id, op, map[string]any{"sourceNameTh": "อาลัง บี", "linkedCustomerId": w.custX})
	expect(t, r, http.StatusOK, "")
	if r.data()["sourceNameTh"] != "อาลัง บี" || r.data()["network"] != "SPK" || r.data()["linkedPartyId"] != w.partyX {
		t.Fatalf("patched: %s", r.raw)
	}
	if w.rdb.Exists(context.Background(), w.ks.HubsAll()).Val() != 0 {
		t.Fatal("cache:hubs:all survived the write")
	}
	r = w.get("/v1/hubs", op, "If-None-Match", etag)
	if r.status != http.StatusOK || r.list()[0]["sourceNameTh"] != "อาลัง บี" {
		t.Fatalf("after PATCH: %d %s", r.status, r.raw)
	}
	if n := w.count(`SELECT count(*) FROM hub_name_aliases WHERE hub_id = $1 AND alias IN ('อาลัง เอ')`, id); n != 0 {
		t.Fatal("the old Thai name still maps to the hub")
	}
	// A PATCH that changes nothing writes nothing; null unlinks.
	before := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'hubs.changed'`)
	expect(t, w.patch("/v1/hubs/"+id, op, map[string]any{"sourceNameTh": "อาลัง บี", "stationType": "hub"}), http.StatusOK, "")
	if w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'hubs.changed'`) != before {
		t.Fatal("a no-op PATCH emitted hubs.changed")
	}
	r = w.patch("/v1/hubs/"+id, op, map[string]any{"linkedCustomerId": nil})
	if r.status != http.StatusOK || r.data()["linkedPartyId"] != nil || r.data()["network"] != "SPX" {
		t.Fatalf("unlink: %s", r.raw)
	}
	// Renaming the code onto a taken one is 409; an unknown id 404.
	w.hub("SOCE", "ศูนย์คัดแยกตะวันออก", "SOC", "SPX", f(13.6), f(100.7), nil)
	expect(t, w.patch("/v1/hubs/"+id, op, map[string]any{"sourceId": "soce"}), http.StatusConflict, "already_exists")
	expect(t, w.patch("/v1/hubs/"+uuid.NewString(), op, map[string]any{"sourceNameTh": "x"}), http.StatusNotFound, "not_found")
	// Filters.
	if r := w.get("/v1/hubs?stationType=soc", op); len(r.list()) != 1 || r.list()[0]["sourceId"] != "SOCE" {
		t.Fatalf("stationType: %s", r.raw)
	}
	if r := w.get("/v1/hubs?q=alang", op); len(r.list()) != 1 {
		t.Fatalf("q: %s", r.raw)
	}
	expect(t, w.get("/v1/hubs?stationType=DEPOT", op), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/hubs?page=2", op), http.StatusBadRequest, "bad_request")
	// Every authenticated principal reads the list; nobody without a token.
	for _, u := range []string{"ta.c", "dr.o", "cu", "pa"} {
		expect(t, w.get("/v1/hubs", w.login(u)), http.StatusOK, "")
	}
	expect(t, w.get("/v1/hubs", ""), http.StatusUnauthorized, "unauthenticated")
}

// --- AC: the steward rule (R60) --------------------------------------------------------------------------

func TestStewardRuleOnHubWritesAndDistanceJobs(t *testing.T) {
	w := newWorld(t)
	x := w.hub("X1", "เอ็กซ์", "HUB", "SPX", nil, nil, nil)
	for _, u := range []string{"ta.a", "mg.a", "op.a", "ta.c", "dr.o", "cu", "pa"} {
		tok := w.login(u)
		r := w.post("/v1/hubs", tok, map[string]any{"sourceId": "NEW-" + u, "sourceNameTh": "ใหม่"})
		expect(t, r, http.StatusForbidden, "permission_denied")
		if !slices.Contains(r.detail("missingCapability").([]any), any("operations:manage_sources")) {
			t.Errorf("%s: %s", u, r.raw)
		}
		expect(t, w.patch("/v1/hubs/"+x, tok, map[string]any{"sourceNameTh": "ยึด"}), http.StatusForbidden, "permission_denied")
		expect(t, w.post("/v1/hubs/import", tok, map[string]any{"rows": []any{map[string]any{"sourceId": "X1", "sourceNameTh": "ยึด"}}}),
			http.StatusForbidden, "permission_denied")
		r = w.post("/v1/jobs/distances.compute", tok, nil)
		expect(t, r, http.StatusForbidden, "permission_denied")
		if !slices.Contains(r.detail("missingCapability").([]any), any("operations:calculate_distances")) {
			t.Errorf("%s job: %s", u, r.raw)
		}
	}
	// The carrier manager holds both keys by override, yet /v1/me does not list them (stripped for non-stewards).
	r := w.get("/v1/me", w.login("mg.a"))
	if caps, _ := r.data()["capabilities"].([]any); slices.Contains(caps, any("operations:manage_sources")) ||
		slices.Contains(caps, any("operations:calculate_distances")) {
		t.Fatalf("a carrier manager holds a steward key: %s", r.raw)
	}
	// RLS refuses the write of a non-steward even without the capability guard.
	for _, u := range []string{"ta.a", "mg.a", "ta.c"} {
		if r := w.post("/v1/hubstest/insert-hub", w.login(u), nil); r.data()["sqlstate"] != "42501" {
			t.Errorf("%s RLS insert: %s", u, r.raw)
		}
	}
	if r := w.post("/v1/hubstest/insert-hub", w.login("op.o"), nil); r.data()["sqlstate"] != "" {
		t.Errorf("own-fleet staff (steward) insert under RLS: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM hubs WHERE source_id LIKE 'NEW-%' OR source_id = 'RLS1' OR name_th = 'ยึด'`); n != 0 {
		t.Fatalf("%d refused writes landed", n)
	}
	// A platform admin writes by acting as a tenant (X-Act-On-Tenant), as tenant_admin with the steward flag.
	r = w.post("/v1/hubs", w.login("pa"), map[string]any{"sourceId": "PA1", "sourceNameTh": "แพลตฟอร์ม"}, "X-Act-On-Tenant", w.O)
	expect(t, r, http.StatusCreated, "")
	// Own-fleet staff without calculate_distances (manager) may not start the job; the own-fleet tenant_admin may.
	expect(t, w.post("/v1/jobs/distances.compute", w.login("mg.o"), nil), http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/jobs/distances.compute", w.login("ta.o"), nil), http.StatusAccepted, "")
}

// --- AC: SPK890174 resolves to a code, never back to a name --------------------------------------------------

func TestMapsKeepCodesAsCodes(t *testing.T) {
	w := newWorld(t)
	w.hub("SPK890174", "ห้วยขวาง10", "HUB", "SPK", nil, nil, nil)
	w.hub("SPK890146", "ประเวศ18", "HUB", "SPK", nil, nil, nil)
	w.hub("SPK-GW", "J&T บางปู", "HUB", "SPK", nil, nil, nil)
	op := w.login("op.o")
	// The API refuses a name that is another hub's code (invariant 8, TestHubWritesKeepInvariant8); legacy data loaded by
	// the ETL may still hold one: the read filter keeps the code resolving to itself.
	expect(t, w.post("/v1/hubs", op, map[string]any{"sourceId": "ODD1", "sourceNameTh": "แปลก", "sourceNameEn": "SPK890174"}),
		http.StatusUnprocessableEntity, "invalid_argument")
	odd := w.hub("ODD1", "แปลก", "HUB", "SPX", nil, nil, nil)
	w.exec(`UPDATE hubs SET name_en = 'SPK890174' WHERE id = $1`, odd)
	w.exec(`INSERT INTO hub_name_aliases (alias, hub_id, source) VALUES ('SPK890174', $1, 'name_en')`, odd)
	r := w.get("/v1/hubs/maps", op)
	expect(t, r, http.StatusOK, "")
	var m hubs.Maps
	if err := json.Unmarshal(mustJSON(t, r.body["data"]), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.NameToCode["SPK890174"]; ok {
		t.Fatalf("a code is a nameToCode key: %v", m.NameToCode)
	}
	for in, want := range map[string]string{"SPK890174": "SPK890174", "ห้วยขวาง10": "SPK890174", "ประเวศ18": "SPK890146",
		"J&T บางปู": "SPK-GW", "แปลก": "ODD1"} {
		if got := m.Resolve(in); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
	if m.CodeToName["SPK890174"] != "ห้วยขวาง10" || m.CodeToName["ODD1"] != "แปลก" {
		t.Fatalf("codeToName: %v", m.CodeToName)
	}
	// Two Redis hashes, never one merged map.
	ctx := context.Background()
	n2c, c2n := w.rdb.HGetAll(ctx, w.ks.HubsNameToCode()).Val(), w.rdb.HGetAll(ctx, w.ks.HubsCodeToName()).Val()
	if n2c["ห้วยขวาง10"] != "SPK890174" || c2n["SPK890174"] != "ห้วยขวาง10" || n2c["SPK890174"] != "" {
		t.Fatalf("redis: n2c %v c2n %v", n2c, c2n)
	}
	// ETag on the maps too.
	if r2 := w.get("/v1/hubs/maps", op, "If-None-Match", r.header.Get("ETag")); r2.status != http.StatusNotModified {
		t.Fatalf("maps revalidation: %d", r2.status)
	}
	// A name given up goes to the next hub carrying it (first writer wins again); the maps follow the write.
	w.hub("SPK890999", "ประเวศ19", "HUB", "SPK", nil, nil, nil)
	w.exec(`UPDATE hubs SET name_en = 'ประเวศ18' WHERE source_id = 'SPK890999'`)
	id146 := w.id(`SELECT id::text FROM hubs WHERE source_id = 'SPK890146'`)
	expect(t, w.patch("/v1/hubs/"+id146, op, map[string]any{"sourceNameTh": "ประเวศ18 ใหม่"}), http.StatusOK, "")
	r = w.get("/v1/hubs/maps", op)
	if err := json.Unmarshal(mustJSON(t, r.body["data"]), &m); err != nil {
		t.Fatal(err)
	}
	if m.Resolve("ประเวศ18") != "SPK890999" || m.Resolve("ประเวศ18 ใหม่") != "SPK890146" {
		t.Fatalf("after the rename: %v", m.NameToCode)
	}
	// Editing the legacy hub without touching its names is not refused, and does not store the code as an alias again;
	// renaming its English name drops the legacy alias, which no hub reclaims (it is a code).
	w.exec(`DELETE FROM hub_name_aliases WHERE alias = 'SPK890174'`)
	expect(t, w.patch("/v1/hubs/"+odd, op, map[string]any{"latitude": 13.7, "longitude": 100.6, "sourceNameEn": "SPK890174"}),
		http.StatusOK, "")
	if n := w.count(inv8Aliases); n != 0 {
		t.Fatalf("a write stored a code as an alias: %d", n)
	}
	w.exec(`INSERT INTO hub_name_aliases (alias, hub_id, source) VALUES ('SPK890174', $1, 'name_en')`, odd)
	expect(t, w.patch("/v1/hubs/"+odd, op, map[string]any{"sourceNameEn": "Odd One"}), http.StatusOK, "")
	if n := w.count(inv8Aliases); n != 0 {
		t.Fatalf("the legacy code alias survived or was reclaimed: %d", n)
	}
	if got := w.pricingMaps().ResolveNameToCode("SPK890174"); got != "SPK890174" {
		t.Fatalf("pricing resolves SPK890174 to %s", got)
	}

	// A rename that changes only the case: the current spelling is the nameToCode key (citext kept the old one).
	r = w.post("/v1/hubs", op, map[string]any{"sourceId": "ALANG1", "sourceNameTh": "อลัง", "sourceNameEn": "Alang Hub"})
	expect(t, r, http.StatusCreated, "")
	expect(t, w.patch("/v1/hubs/"+str(r.data()["id"]), op, map[string]any{"sourceNameEn": "ALANG HUB"}), http.StatusOK, "")
	r = w.get("/v1/hubs/maps", op)
	m = hubs.Maps{}
	if err := json.Unmarshal(mustJSON(t, r.body["data"]), &m); err != nil {
		t.Fatal(err)
	}
	if m.Resolve("ALANG HUB") != "ALANG1" || m.NameToCode["Alang Hub"] != "" {
		t.Fatalf("case-only rename: %v", m.NameToCode)
	}
}

// inv8Aliases is the alias half of seed --verify invariant 8 (cmd/seed/internal/seed/invariants.go inv8), as a count.
const inv8Aliases = `SELECT count(*) FROM hub_name_aliases a JOIN hubs h ON upper(h.source_id::text) = upper(a.alias::text)`

// namesAreCodes counts the hub names that are another hub's code (pricing reads hubs.name_th / name_en directly).
const namesAreCodes = `SELECT count(*) FROM hubs h JOIN hubs o ON o.id <> h.id
	AND (lower(btrim(h.name_th)) = lower(o.source_id::text) OR lower(btrim(coalesce(h.name_en, ''))) = lower(o.source_id::text))`

// pricingMaps loads the pricing hub maps exactly as the seed engine does (cmd/seed/internal/seed/engine.go, R53).
func (w *world) pricingMaps() compute.HubMaps {
	w.t.Helper()
	rows, err := w.etl.Query(context.Background(), `SELECT h.source_id::text, h.name_th, coalesce(h.name_en, ''),
		coalesce(array_agg(a.alias::text ORDER BY a.created_at, a.alias) FILTER (WHERE a.alias IS NOT NULL), '{}')
		FROM hubs h LEFT JOIN hub_name_aliases a ON a.hub_id = h.id
		GROUP BY h.id ORDER BY h.created_at, h.id`)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var hs []compute.Hub
	for rows.Next() {
		var h compute.Hub
		if err := rows.Scan(&h.Code, &h.NameTH, &h.NameEN, &h.Aliases); err != nil {
			w.t.Fatal(err)
		}
		hs = append(hs, h)
	}
	if err := rows.Err(); err != nil {
		w.t.Fatal(err)
	}
	return compute.NewHubMaps(hs)
}

// --- invariant 8 at write time: no name is another hub's code, no code another hub's name or alias ----------

func TestHubWritesKeepInvariant8(t *testing.T) {
	w := newWorld(t)
	w.hub("SPK890174", "ห้วยขวาง10", "HUB", "SPK", nil, nil, nil)
	w.hub("SPK890146", "ประเวศ18", "HUB", "SPK", nil, nil, nil)
	w.hub("H2", "HUBX", "HUB", "SPX", nil, nil, nil) // the Thai name HUBX
	op, dr := w.login("op.o"), w.login("dr.o")
	isHubCode := func(r resp, field string) {
		t.Helper()
		if r.status != http.StatusUnprocessableEntity || r.code() != "invalid_argument" || r.fields()[field] != "is_hub_code" {
			t.Fatalf("want 422 %s is_hub_code, got %d %s", field, r.status, r.raw)
		}
	}
	takenAsName := func(r resp, field string) {
		t.Helper()
		if r.status != http.StatusConflict || r.code() != "already_exists" || r.detail("field") != field ||
			r.detail("reason") != "taken_as_name" {
			t.Fatalf("want 409 %s taken_as_name, got %d %s", field, r.status, r.raw)
		}
	}
	// POST: a name that is another hub's code, whatever its case; a code that is another hub's name.
	isHubCode(w.post("/v1/hubs", op, map[string]any{"sourceId": "ODD1", "sourceNameTh": "แปลก", "sourceNameEn": "SPK890174"}), "sourceNameEn")
	isHubCode(w.post("/v1/hubs", op, map[string]any{"sourceId": "ODD1", "sourceNameTh": " spk890174 "}), "sourceNameTh")
	takenAsName(w.post("/v1/hubs", op, map[string]any{"sourceId": "hubx", "sourceNameTh": "ใหม่"}), "sourceId")
	// A taken code stays a plain 409 taken even when the name repeats it; a name equal to the hub's own code is fine.
	r := w.post("/v1/hubs", op, map[string]any{"sourceId": "SPK890174", "sourceNameTh": "SPK890174"})
	if r.status != http.StatusConflict || r.detail("reason") != "taken" {
		t.Fatalf("taken code: %s", r.raw)
	}
	r = w.post("/v1/hubs", op, map[string]any{"sourceId": "ODD1", "sourceNameTh": "แปลก", "sourceNameEn": "ODDX"})
	expect(t, r, http.StatusCreated, "")
	odd := str(r.data()["id"])
	// PATCH: a changed name, a renamed code; renaming onto its own name drops that alias.
	isHubCode(w.patch("/v1/hubs/"+odd, op, map[string]any{"sourceNameTh": "SPK890146"}), "sourceNameTh")
	takenAsName(w.patch("/v1/hubs/"+odd, op, map[string]any{"sourceId": "HUBX"}), "sourceId")
	takenAsName(w.patch("/v1/hubs/"+odd, op, map[string]any{"sourceId": "ห้วยขวาง10"}), "sourceId")
	expect(t, w.patch("/v1/hubs/"+odd, op, map[string]any{"sourceId": "oddx"}), http.StatusOK, "")
	if n := w.count(`SELECT count(*) FROM hub_name_aliases WHERE hub_id = $1`, odd); n != 1 {
		t.Fatalf("aliases of the renamed hub: %d", n)
	}
	// Import: a new row's name, an existing row's changed name, a new code that is a name (409 on rows[i].sourceId).
	isHubCode(w.post("/v1/hubs/import", op, map[string]any{"rows": []any{map[string]any{"sourceId": "IMP1", "sourceNameTh": "H2"}}}),
		"rows[0].sourceNameTh")
	isHubCode(w.post("/v1/hubs/import", op, map[string]any{"rows": []any{map[string]any{"sourceId": "IMP1", "sourceNameEn": "h2"}}}),
		"rows[0].sourceNameEn")
	isHubCode(w.post("/v1/hubs/import", op, map[string]any{"rows": []any{map[string]any{"sourceId": "IMP1", "sourceNameTh": "ไอ"},
		map[string]any{"sourceId": "ODDX", "sourceNameEn": "SPK890174"}}}), "rows[1].sourceNameEn")
	takenAsName(w.post("/v1/hubs/import", op, map[string]any{"rows": []any{map[string]any{"sourceId": "IMP1", "sourceNameTh": "IMP2"},
		map[string]any{"sourceId": "imp2", "sourceNameTh": "ไอสอง"}}}), "rows[1].sourceId")
	// The driver's hub.
	isHubCode(w.post("/v1/hubstest/driver-hub", dr, map[string]any{"sourceId": "DRV8", "sourceName": "SPK890174"}), "sourceName")
	takenAsName(w.post("/v1/hubstest/driver-hub", dr, map[string]any{"sourceId": "HUBX", "sourceName": "ใหม่"}), "sourceId")
	if n := w.count(`SELECT count(*) FROM hubs WHERE source_id IN ('IMP1', 'IMP2', 'DRV8', 'HUBX')`); n != 0 {
		t.Fatalf("%d refused hubs landed", n)
	}
	// Valid writes still pass, and the stored master data keeps invariant 8 for the seed check and for pricing.
	expect(t, w.post("/v1/hubs/import", op, map[string]any{"rows": []any{map[string]any{"sourceId": "IMP1", "sourceNameTh": "ไอ"}}}),
		http.StatusOK, "")
	expect(t, w.post("/v1/hubstest/driver-hub", dr, map[string]any{"sourceId": "DRV8", "sourceName": "จุดคนขับ"}), http.StatusCreated, "")
	if n := w.count(inv8Aliases); n != 0 {
		t.Fatalf("seed --verify invariant 8: %d aliases equal a hub code", n)
	}
	if n := w.count(namesAreCodes); n != 0 {
		t.Fatalf("%d hub names are another hub's code", n)
	}
	pm := w.pricingMaps()
	for _, code := range []string{"SPK890174", "SPK890146", "H2", "ODDX", "IMP1", "DRV8"} {
		if got := pm.ResolveNameToCode(code); got != code {
			t.Errorf("pricing resolves %s to %s", code, got)
		}
	}
	if pm.ResolveNameToCode("HUBX") != "H2" {
		t.Error("a name no longer resolves")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- the linked party as each principal may see it -------------------------------------------------------

func TestLinkedPartyMaskedLikeRLS(t *testing.T) {
	w := newWorld(t)
	w.hub("HX", "เอ็กซ์", "HUB", "SPK", nil, nil, &w.partyX)
	w.hub("HY", "วาย", "HUB", "SPX", nil, nil, &w.partyY)
	w.hub("HC", "ซี", "HUB", "SPX", nil, nil, &w.partyC)
	name := func(u, code string) any {
		r := w.get("/v1/hubs", w.login(u))
		expect(t, r, http.StatusOK, "")
		return byCode(r.list())[code]["linkedCustomerName"]
	}
	if name("op.o", "HY") != "TTP — TTP Co." || name("op.o", "HC") != "CC — ซี" {
		t.Fatal("a steward sees every party")
	}
	if name("cu", "HX") != "SPK — J&T Express" || name("cu", "HY") != nil || name("cu", "HC") != nil {
		t.Fatal("a customer-scope user sees only its own party, of any kind (0012)")
	}
	if name("op.a", "HY") != "TTP — TTP Co." || name("op.a", "HC") != nil {
		t.Fatal("carrier staff read customers but not another carrier (tenants.p_read)")
	}
	if name("ta.c", "HC") != "CC — ซี" {
		t.Fatal("a carrier reads its own name")
	}
	// The cached list equals the RLS-backed read (Service.Get) of every hub, for every kind of principal: the mask is
	// the RLS outcome, linkedCustomerKind included (hidden with the party row for a customer scope).
	ids := map[string]string{}
	for _, c := range []string{"HX", "HY", "HC"} {
		ids[c] = w.id(`SELECT id::text FROM hubs WHERE source_id = $1`, c)
	}
	for _, u := range []string{"cu", "op.a", "ta.c", "op.o", "dr.o", "pa"} {
		tok := w.login(u)
		list := byCode(w.get("/v1/hubs", tok).list())
		for code, id := range ids {
			r := w.get("/v1/hubstest/hub/"+id, tok)
			expect(t, r, http.StatusOK, "")
			for _, k := range []string{"linkedPartyId", "linkedCustomerId", "linkedCustomerName", "linkedCustomerKind"} {
				if list[code][k] != r.data()[k] {
					t.Errorf("%s %s %s: list %v, RLS %v", u, code, k, list[code][k], r.data()[k])
				}
			}
		}
	}
	if k := byCode(w.get("/v1/hubs", w.login("cu")).list()); k["HC"]["linkedCustomerKind"] != nil || k["HY"]["linkedCustomerKind"] != nil ||
		k["HX"]["linkedCustomerKind"] != "customer" {
		t.Fatalf("customer scope kinds: %v %v %v", k["HX"]["linkedCustomerKind"], k["HY"]["linkedCustomerKind"], k["HC"]["linkedCustomerKind"])
	}
}

// --- import ------------------------------------------------------------------------------------------------

func TestImportUpsertsBySourceID(t *testing.T) {
	w := newWorld(t)
	w.hub("ALANG-A", "อาลัง", "HUB", "SPX", f(13.7), f(100.6), nil)
	op := w.login("op.o")
	rows := []any{
		map[string]any{"sourceId": "alang-a", "sourceNameEn": "Alang A", "latitude": 13.71, "longitude": 100.61},
		map[string]any{"sourceId": "SOCN", "sourceNameEn": "ศูนย์เหนือ", "stationType": "RETURN_CENTER", "latitude": 14.0, "longitude": 100.5},
		map[string]any{"sourceId": "SPK890103", "sourceNameTh": "ลาดกระบัง26", "linkedCustomerId": w.custY},
	}
	r := w.post("/v1/hubs/import", op, map[string]any{"rows": rows})
	expect(t, r, http.StatusOK, "")
	if d := r.data(); d["created"] != float64(2) || d["updated"] != float64(1) || d["unchanged"] != float64(0) {
		t.Fatalf("import: %s", r.raw)
	}
	got := byCode(w.get("/v1/hubs", op).list())
	if got["ALANG-A"]["sourceNameTh"] != "อาลัง" || got["ALANG-A"]["sourceNameEn"] != "Alang A" ||
		got["SOCN"]["stationType"] != "SOC" || got["SOCN"]["sourceNameTh"] != "ศูนย์เหนือ" || got["SPK890103"]["network"] != "SPK" {
		t.Fatalf("after import: %v", got)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'hubs.changed' AND jsonb_array_length(payload->'ids') = 3`); n != 1 {
		t.Fatalf("one hubs.changed with every changed id: %d", n)
	}
	// The same rows again: unchanged, no event.
	r = w.post("/v1/hubs/import", op, map[string]any{"rows": rows})
	if r.data()["unchanged"] != float64(3) {
		t.Fatalf("re-import: %s", r.raw)
	}
	// One bad row refuses the whole import, field by field; a code twice too.
	r = w.post("/v1/hubs/import", op, map[string]any{"rows": []any{
		map[string]any{"sourceId": "NEW1", "sourceNameTh": "ใหม่"},
		map[string]any{"sourceId": "NEW2", "latitude": 200},
		map[string]any{"sourceId": "new1", "sourceNameTh": "ซ้ำ"},
		map[string]any{"sourceId": "NEW3"},
	}})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if fs := r.fields(); fs["rows[1].latitude"] != "out_of_range" || fs["rows[2].sourceId"] != "duplicate" {
		t.Fatalf("422: %s", r.raw)
	}
	r = w.post("/v1/hubs/import", op, map[string]any{"rows": []any{map[string]any{"sourceId": "NEW1", "sourceNameTh": "ใหม่"},
		map[string]any{"sourceId": "NEW3"}}})
	if r.status != http.StatusUnprocessableEntity || r.fields()["rows[1].sourceNameTh"] != "required" {
		t.Fatalf("a new row without a name: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM hubs WHERE source_id LIKE 'NEW%'`); n != 0 {
		t.Fatalf("a refused import wrote %d rows", n)
	}
	expect(t, w.post("/v1/hubs/import", op, map[string]any{"rows": []any{}}), http.StatusUnprocessableEntity, "invalid_argument")

	// Blank cells (a workbook's empty cells arrive as "") keep the stored values: import never clears. LINKED1 is SPK
	// only through its link to X (code SPK); unlinking it would flip it to SPX.
	r = w.post("/v1/hubs", op, map[string]any{"sourceId": "LINKED1", "sourceNameTh": "ลิงก์", "sourceNameEn": "Linked One",
		"latitude": 13.7, "longitude": 100.6, "stationType": "HUB", "linkedCustomerId": w.custX})
	expect(t, r, http.StatusCreated, "")
	if r.data()["network"] != "SPK" {
		t.Fatalf("linked hub: %s", r.raw)
	}
	before := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'hubs.changed'`)
	r = w.post("/v1/hubs/import", op, map[string]any{"rows": []any{map[string]any{"sourceId": "linked1", "sourceNameTh": "",
		"sourceNameEn": "", "stationType": "", "linkedPartyId": "", "linkedCustomerId": " ", "linkedCustomerKind": ""}}})
	expect(t, r, http.StatusOK, "")
	if r.data()["unchanged"] != float64(1) {
		t.Fatalf("blank cells: %s", r.raw)
	}
	if w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'hubs.changed'`) != before {
		t.Fatal("blank cells emitted hubs.changed")
	}
	h := byCode(w.get("/v1/hubs", op).list())["LINKED1"]
	if h["sourceNameTh"] != "ลิงก์" || h["sourceNameEn"] != "Linked One" || h["stationType"] != "HUB" || h["linkedPartyId"] != w.partyX ||
		h["network"] != "SPK" {
		t.Fatalf("after blank cells: %v", h)
	}
}

// --- the driver's hub (mobile:create_hub) -----------------------------------------------------------------

func TestDriverCreatesAHub(t *testing.T) {
	w := newWorld(t)
	dr := w.login("dr.o")
	r := w.post("/v1/hubstest/driver-hub", dr, map[string]any{"sourceId": "drv-1", "sourceName": "จุดใหม่", "stationType": "SOC"})
	expect(t, r, http.StatusCreated, "")
	if r.data()["createdByDriver"] != true || r.data()["sourceId"] != "DRV-1" || r.data()["sourceNameEn"] != "จุดใหม่" ||
		r.data()["stationType"] != "SOC" {
		t.Fatalf("driver hub: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM hub_name_aliases a JOIN hubs h ON h.id = a.hub_id WHERE h.source_id = 'DRV-1'`); n != 1 {
		t.Fatalf("driver hub aliases: %d", n)
	}
	expect(t, w.post("/v1/hubstest/driver-hub", dr, map[string]any{"sourceId": "DRV-1", "sourceName": "ซ้ำ"}), http.StatusConflict, "already_exists")
	// Staff are refused on the driver path (they use POST /v1/hubs), and a driver on the staff one.
	expect(t, w.post("/v1/hubstest/driver-hub", w.login("op.o"), map[string]any{"sourceId": "S1", "sourceName": "x"}),
		http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/hubs", dr, map[string]any{"sourceId": "S2", "sourceNameTh": "x"}), http.StatusForbidden, "permission_denied")

	// Coordinates are steward-only: a driver SOC sent with them is stored without, so it stays out of the billed
	// Distance Matrix run until a steward sets them (PATCH /v1/hubs/{id}).
	w.hub("ALANG-A", "อาลัง", "HUB", "SPX", f(13.75), f(100.60), nil)
	w.hub("SOCE", "ศูนย์ตะวันออก", "SOC", "SPX", f(13.62), f(100.75), nil)
	r = w.post("/v1/hubstest/driver-hub", dr, map[string]any{"sourceId": "DRVSOC", "sourceName": "โซคนขับ", "stationType": "SOC",
		"latitude": 13.1, "longitude": 100.1})
	expect(t, r, http.StatusCreated, "")
	if r.data()["latitude"] != nil || r.data()["longitude"] != nil {
		t.Fatalf("a driver set coordinates: %s", r.raw)
	}
	drvSoc := str(r.data()["id"])
	ta := w.login("ta.o")
	run := func() *fakeMatrix {
		t.Helper()
		r := w.post("/v1/jobs/distances.compute", ta, nil)
		expect(t, r, http.StatusAccepted, "")
		fake := &fakeMatrix{}
		w.runJob(t, w.newJob(t, fake), str(r.data()["jobId"]))
		return fake
	}
	fake := run()
	for _, c := range fake.calls {
		for _, side := range c {
			for _, p := range side {
				if p.Lat == 13.1 {
					t.Fatal("the driver's SOC was sent to the Distance Matrix")
				}
			}
		}
	}
	if n := w.count(`SELECT count(*) FROM hub_soc_distances WHERE soc_key = 'DRVSOC'`); n != 0 {
		t.Fatalf("driver SOC pairs before a steward set its coordinates: %d", n)
	}
	expect(t, w.patch("/v1/hubs/"+drvSoc, w.login("op.o"), map[string]any{"latitude": 13.1, "longitude": 100.1}), http.StatusOK, "")
	run()
	if n := w.count(`SELECT count(*) FROM hub_soc_distances WHERE soc_key = 'DRVSOC'`); n != 2 {
		t.Fatalf("driver SOC pairs after the steward set its coordinates: %d", n)
	}
}

// --- AC: the distances.compute job ------------------------------------------------------------------------

// fakeMatrix answers every cell from the points' coordinates and records the requests; fail makes it refuse, failAt
// makes it refuse only its failAt-th call (1-based) with an UNKNOWN_ERROR, after the earlier ones succeeded.
type fakeMatrix struct {
	mu     sync.Mutex
	calls  [][2][]gmaps.LatLng
	fail   error
	failAt int
}

func (f *fakeMatrix) DistanceMatrix(_ context.Context, origins, destinations []gmaps.LatLng) (gmaps.Matrix, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, [2][]gmaps.LatLng{origins, destinations})
	if f.fail != nil {
		return gmaps.Matrix{}, f.fail
	}
	if f.failAt > 0 && len(f.calls) == f.failAt {
		return gmaps.Matrix{}, &gmaps.Error{HTTPStatus: 200, Status: "UNKNOWN_ERROR"}
	}
	if err := gmaps.CheckLimits(len(origins), len(destinations)); err != nil {
		return gmaps.Matrix{}, err
	}
	m := gmaps.Matrix{Rows: make([][]gmaps.Element, len(origins))}
	for i, o := range origins {
		for _, d := range destinations {
			meters := int(math.Round(math.Hypot(o.Lat-d.Lat, o.Lng-d.Lng)*111_000)) + 5 // 5 m: no zero distance
			m.Rows[i] = append(m.Rows[i], gmaps.Element{Status: "OK", OK: true, DistanceMeters: meters, DurationSeconds: meters / 10})
		}
	}
	return m, nil
}

// runJob delivers the job's lt.jobs command (as the relay publishes it) to the consumer.
func (w *world) runJob(t *testing.T, j *hubs.DistanceJob, jobID string) {
	t.Helper()
	var body []byte
	if err := w.etl.QueryRow(context.Background(), `SELECT payload::text FROM outbox_events
		WHERE exchange = 'lt.jobs' AND routing_key = 'job.distances.compute' AND aggregate_id = $1`, jobID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	reg := j.Registration()
	if reg.Queue != "distances.compute" {
		t.Fatal(reg.Queue)
	}
	if err := reg.Handler(context.Background(), &mq.Delivery{Queue: reg.Queue, RoutingKey: hubs.RouteDistancesJob, Body: body}); err != nil {
		t.Fatal(err)
	}
}

func (w *world) newJob(t *testing.T, m gmaps.DistanceMatrix) *hubs.DistanceJob {
	t.Helper()
	j, err := hubs.NewDistanceJob(hubs.JobDeps{Pool: w.pool, Matrix: m, Locks: jobs.NewRedisLocker(w.rdb, w.ks), Spacing: time.Millisecond,
		Now: func() time.Time { return time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC) }, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestDistanceJob(t *testing.T) {
	w := newWorld(t)
	// SPX: 3 hubs, SOCE (twice by key: the first wins) and the stand-by SOC 0STANDBY; SPK: 2 hubs and its own SOC.
	spx := []string{w.hub("ALANG-A", "อาลัง", "HUB", "SPX", f(13.75), f(100.60), nil),
		w.hub("BANGNA-SPX", "บางนา", "HUB", "SPX", f(13.67), f(100.61), nil),
		w.hub("WTL12", "วังทองหลาง12", "HUB", "SPX", f(13.78), f(100.61), nil)}
	soce := w.hub("SOCE", "ศูนย์ตะวันออก", "SOC", "SPX", f(13.62), f(100.75), nil)
	w.hub("SOCE (บัวโรย)", "บัวโรย", "SOC", "SPX", f(13.50), f(100.90), nil)
	w.hub("0STANDBY", "สแตนด์บาย", "SOC", "SPX", f(13.00), f(100.00), nil)
	spk := []string{w.hub("SPK890174", "ห้วยขวาง10", "HUB", "SPK", f(13.77), f(100.57), nil),
		w.hub("SPK890146", "ประเวศ18", "HUB", "SPK", f(13.72), f(100.68), nil)}
	spkSoc := w.hub("SPK-GW", "J&T บางปู", "SOC", "SPK", f(13.52), f(100.66), nil)
	w.hub("NOCOORD", "ไม่มีพิกัด", "HUB", "SPX", nil, nil, nil)

	ta := w.login("ta.o")
	r := w.post("/v1/jobs/distances.compute", ta, nil)
	expect(t, r, http.StatusAccepted, "")
	jobID := str(r.data()["jobId"])
	// lock:job:distances.compute:all: a second run while one is queued is 409 with the running job.
	r = w.post("/v1/jobs/distances.compute", w.login("ta2.o"), nil)
	expect(t, r, http.StatusConflict, "already_exists")
	if r.detail("jobId") != jobID {
		t.Fatalf("held lock: %s", r.raw)
	}
	fake := &fakeMatrix{}
	w.runJob(t, w.newJob(t, fake), jobID)

	// No SPX x SPK pair: every request stays inside one network; the stand-by SOC and the duplicate key never travel;
	// every request is inside the API limits.
	spkLat := map[float64]bool{13.77: true, 13.72: true, 13.52: true}
	for _, c := range fake.calls {
		if err := gmaps.CheckLimits(len(c[0]), len(c[1])); err != nil {
			t.Fatal(err)
		}
		mixed := map[bool]bool{}
		for _, side := range c {
			for _, p := range side {
				mixed[spkLat[p.Lat]] = true
				if p.Lat == 13.00 || p.Lat == 13.50 {
					t.Fatalf("a skipped SOC was sent: %v", p)
				}
			}
		}
		if len(mixed) != 1 {
			t.Fatalf("a request mixes SPX and SPK: %v", c)
		}
	}
	if len(fake.calls) != 4 { // per network: one hub_to_soc and one soc_to_hub request
		t.Fatalf("requests: %d", len(fake.calls))
	}
	// Both directions stored with direction (R14): 3 SPX hubs x SOCE + 2 SPK hubs x SPK-GW, twice.
	if n := w.count(`SELECT count(*) FROM hub_soc_distances`); n != 10 {
		t.Fatalf("pairs: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM hub_soc_distances d JOIN hubs h ON h.id = d.hub_id
		WHERE d.network <> h.network OR (d.network = 'SPX' AND d.soc_key <> 'SOCE') OR (d.network = 'SPK' AND d.soc_key <> 'SPK-GW')`); n != 0 {
		t.Fatalf("%d cross-network pairs", n)
	}
	for _, dir := range []string{"hub_to_soc", "soc_to_hub"} {
		if n := w.count(`SELECT count(*) FROM hub_soc_distances WHERE direction = $1`, dir); n != 5 {
			t.Fatalf("%s: %d", dir, n)
		}
	}
	var status string
	var result []byte
	if err := w.etl.QueryRow(context.Background(), `SELECT status, result::text FROM jobs WHERE id = $1`, jobID).Scan(&status, &result); err != nil {
		t.Fatal(err)
	}
	var res hubs.Result
	_ = json.Unmarshal(result, &res)
	if status != jobs.StatusSucceeded || res.Written != 10 || res.HubsCount != 5 || res.SocsCount != 2 || res.Requests != 4 ||
		res.Networks["SPK"].Pairs != 4 || res.Networks["SPX"].Pairs != 6 {
		t.Fatalf("job: %s %s", status, result)
	}
	if n := w.count(`SELECT count(*) FROM settings WHERE key = 'distances_last_calculated' AND value->>'pairs' = '10'`); n != 1 {
		t.Fatal("settings distances_last_calculated")
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'settings.changed' AND payload->>'key' = 'distances_last_calculated'
		AND cardinality(realtime_topics) = 0`); n != 1 {
		t.Fatal("settings.changed")
	}

	// Reads: meta, the rows of a hub, of a SOC, and one pair each way.
	r = w.get("/v1/distances/meta", ta)
	if r.status != http.StatusOK || r.data()["lastCalculatedAt"] != "2026-10-11T03:00:00Z" {
		t.Fatalf("meta: %s", r.raw)
	}
	r = w.get("/v1/distances?hubId="+spx[0], ta)
	if items := r.list(); r.status != http.StatusOK || len(items) != 2 || items[0]["hubSourceId"] != "ALANG-A" {
		t.Fatalf("hub rows: %s", r.raw)
	}
	if r := w.get("/v1/distances?hubId=soce&direction=soc_to_hub", ta); len(r.list()) != 3 {
		t.Fatalf("SOC rows: %s", r.raw)
	}
	r = w.get("/v1/distances?limit=4", ta)
	if len(r.list()) != 4 || str(r.body["nextCursor"]) == "" {
		t.Fatalf("page: %s", r.raw)
	}
	r = w.get("/v1/distances?limit=4&cursor="+str(r.body["nextCursor"]), ta)
	if len(r.list()) != 4 {
		t.Fatalf("page 2: %s", r.raw)
	}
	r = w.get("/v1/distances/"+spx[0]+"/"+soce, ta)
	if r.status != http.StatusOK || r.data()["direction"] != "hub_to_soc" || r.data()["distanceKm"].(float64) <= 0 {
		t.Fatalf("pair: %s", r.raw)
	}
	r = w.get("/v1/distances/SOCE/ALANG-A", w.login("dr.o")) // codes, a driver reads too
	if r.status != http.StatusOK || r.data()["direction"] != "soc_to_hub" {
		t.Fatalf("pair by codes: %s", r.raw)
	}
	r = w.get("/v1/distances/"+spkSoc+"/"+spk[1], ta)
	if r.status != http.StatusOK || r.data()["direction"] != "soc_to_hub" {
		t.Fatalf("SPK pair: %s", r.raw)
	}
	expect(t, w.get("/v1/distances/"+spx[0]+"/"+spx[1], ta), http.StatusNotFound, "not_found") // HUB -> HUB
	expect(t, w.get("/v1/distances/"+spk[0]+"/"+soce, ta), http.StatusNotFound, "not_found")   // never computed
	expect(t, w.get("/v1/distances/NOPE/SOCE", ta), http.StatusNotFound, "not_found")

	// The job's readers: owner, staff of its tenant holding calculate_distances, platform; nobody else.
	for u, want := range map[string]int{"ta.o": 200, "ta2.o": 200, "pa": 200, "mg.o": 404, "ta.a": 404, "op.a": 404} {
		if r := w.get("/v1/jobs/"+jobID, w.login(u)); r.status != want {
			t.Errorf("%s reads the job: %d", u, r.status)
		}
	}
	if r := w.get("/v1/jobs?type=distances.compute", w.login("ta2.o")); len(r.list()) != 1 {
		t.Fatalf("ta2.o lists: %s", r.raw)
	}
	if r := w.get("/v1/jobs", w.login("mg.o")); len(r.list()) != 0 {
		t.Fatalf("mg.o lists: %s", r.raw)
	}

	// The lock is released: a new run starts, and computing again keeps every pair once (upsert; stale pairs kept).
	w.exec(`DELETE FROM hubs WHERE source_id = 'NOCOORD'`)
	r = w.post("/v1/jobs/distances.compute", ta, nil)
	expect(t, r, http.StatusAccepted, "")
	w.exec(`UPDATE hubs SET latitude = NULL, longitude = NULL WHERE source_id = 'WTL12'`) // no longer computed
	w.runJob(t, w.newJob(t, &fakeMatrix{}), str(r.data()["jobId"]))
	if n := w.count(`SELECT count(*) FROM hub_soc_distances`); n != 10 {
		t.Fatalf("after the second run: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM hub_soc_distances d JOIN hubs h ON h.id = d.hub_id WHERE h.source_id = 'WTL12'`); n != 2 {
		t.Fatal("the stale pairs of a hub without coordinates were dropped")
	}
	// A duplicate delivery of a finished command is acknowledged without work.
	again := &fakeMatrix{}
	w.runJob(t, w.newJob(t, again), jobID)
	if len(again.calls) != 0 {
		t.Fatal("a finished job ran again")
	}
}

func TestDistanceJobFailsWithoutWriting(t *testing.T) {
	w := newWorld(t)
	w.hub("ALANG-A", "อาลัง", "HUB", "SPX", f(13.75), f(100.60), nil)
	w.hub("SOCE", "ศูนย์ตะวันออก", "SOC", "SPX", f(13.62), f(100.75), nil)
	ta := w.login("ta.o")
	for name, c := range map[string]struct {
		m     gmaps.DistanceMatrix
		cause string
	}{
		"disabled": {nil, hubs.ErrTextDisabled},
		"refused":  {&fakeMatrix{fail: &gmaps.Error{HTTPStatus: 200, Status: "REQUEST_DENIED"}}, "Distance Matrix API: REQUEST_DENIED"},
	} {
		t.Run(name, func(t *testing.T) {
			r := w.post("/v1/jobs/distances.compute", ta, nil)
			expect(t, r, http.StatusAccepted, "")
			id := str(r.data()["jobId"])
			w.runJob(t, w.newJob(t, c.m), id)
			var status, cause string
			if err := w.etl.QueryRow(context.Background(), `SELECT status, coalesce(error, '') FROM jobs WHERE id = $1`, id).Scan(&status, &cause); err != nil {
				t.Fatal(err)
			}
			if status != jobs.StatusFailed || cause != c.cause {
				t.Fatalf("%s %q", status, cause)
			}
			if n := w.count(`SELECT count(*) FROM hub_soc_distances`) + w.count(`SELECT count(*) FROM settings WHERE key = 'distances_last_calculated'`); n != 0 {
				t.Fatalf("a failed run wrote %d rows", n)
			}
			// The lock is released with the failure.
			if w.rdb.Exists(context.Background(), w.ks.JobLock("distances.compute", "all")).Val() != 0 {
				t.Fatal("the lock outlived the job")
			}
		})
	}
	if r := w.get("/v1/distances/meta", ta); r.status != http.StatusOK || r.data()["lastCalculatedAt"] != nil {
		t.Fatalf("meta before any run: %s", r.raw)
	}
}

// AC (job limits of the Hub-SOC Distance section): at most 4 runs a Bangkok month; a failed run that completed no
// request (refused on its first) and earlier months do not count, and a platform admin is not capped.
func TestDistanceRunsCappedPerMonth(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	add := func(status, age string) {
		w.exec(`INSERT INTO jobs (id, type, status, tenant_id, created_at) VALUES (uuidv7(), 'distances.compute', $1, $2, now() - $3::interval)`,
			status, w.O, age)
	}
	for range 3 {
		add("succeeded", "0 seconds")
	}
	add("failed", "0 seconds")
	add("succeeded", "40 days")
	r := w.post("/v1/jobs/distances.compute", ta, nil)
	expect(t, r, http.StatusAccepted, "") // the fourth run of the month
	w.exec(`UPDATE jobs SET status = 'succeeded' WHERE id = $1`, str(r.data()["jobId"]))
	_ = w.rdb.Del(context.Background(), w.ks.JobLock("distances.compute", "all")).Err()
	r = w.post("/v1/jobs/distances.compute", ta, nil)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "monthly_run_limit" || r.detail("limit") != float64(hubs.MaxRunsPerMonth) {
		t.Fatalf("cap: %s", r.raw)
	}
	r = w.post("/v1/jobs/distances.compute", w.login("pa"), nil, "X-Act-On-Tenant", w.O)
	expect(t, r, http.StatusAccepted, "")
}

// A failed run that completed billed requests before failing counts against the monthly cap, so retries of a run that
// keeps failing late cannot spend without bound (the fifth such run is refused).
func TestFailedRunsThatSpentRequestsCount(t *testing.T) {
	w := newWorld(t)
	// 2 SPX hubs and one SOC: 2 requests per run (one per direction); the second fails.
	w.hub("ALANG-A", "อาลัง", "HUB", "SPX", f(13.75), f(100.60), nil)
	w.hub("BANGNA-SPX", "บางนา", "HUB", "SPX", f(13.67), f(100.61), nil)
	w.hub("SOCE", "ศูนย์ตะวันออก", "SOC", "SPX", f(13.62), f(100.75), nil)
	ta := w.login("ta.o")
	for i := range hubs.MaxRunsPerMonth {
		r := w.post("/v1/jobs/distances.compute", ta, nil)
		expect(t, r, http.StatusAccepted, "")
		id := str(r.data()["jobId"])
		fake := &fakeMatrix{failAt: 2}
		w.runJob(t, w.newJob(t, fake), id)
		var status, result string
		if err := w.etl.QueryRow(context.Background(), `SELECT status, coalesce(result::text, '') FROM jobs WHERE id = $1`, id).
			Scan(&status, &result); err != nil {
			t.Fatal(err)
		}
		if status != jobs.StatusFailed || len(fake.calls) != 2 || !strings.Contains(result, `"requests": 1`) {
			t.Fatalf("run %d: %s %s, %d calls", i, status, result, len(fake.calls))
		}
	}
	if n := w.count(`SELECT count(*) FROM hub_soc_distances`); n != 0 {
		t.Fatalf("failed runs wrote %d pairs", n)
	}
	r := w.post("/v1/jobs/distances.compute", ta, nil)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "monthly_run_limit" || r.detail("runs") != float64(hubs.MaxRunsPerMonth) {
		t.Fatalf("cap after %d late failures: %s", hubs.MaxRunsPerMonth, r.raw)
	}
}

// GET /v1/distances/{originId}/{destinationId} decodes its path segments: a code with Thai text or a space resolves as
// it does in ?hubId; a malformed escape names nothing.
func TestDistancePairByEncodedCodes(t *testing.T) {
	w := newWorld(t)
	thai := "SPK890103-ลาดกระบัง26"
	w.hub(thai, "ลาดกระบัง26", "HUB", "SPK", f(13.72), f(100.75), nil)
	plain := w.hub("PLAIN1", "ธรรมดา", "HUB", "SPK", f(13.77), f(100.57), nil)
	socA := w.hub("SOC A", "โซคเอ", "SOC", "SPK", f(13.52), f(100.66), nil)
	w.hub("ALANG-A", "อาลัง", "HUB", "SPX", f(13.75), f(100.60), nil)
	w.hub("SOCE (บัวโรย)", "บัวโรย", "SOC", "SPX", f(13.50), f(100.90), nil)
	ta := w.login("ta.o")
	r := w.post("/v1/jobs/distances.compute", ta, nil)
	expect(t, r, http.StatusAccepted, "")
	w.runJob(t, w.newJob(t, &fakeMatrix{}), str(r.data()["jobId"]))
	if n := w.count(`SELECT count(*) FROM hub_soc_distances`); n != 6 {
		t.Fatalf("pairs: %d", n)
	}
	for _, c := range []struct{ origin, destination, direction string }{
		{url.PathEscape(thai), socA, "hub_to_soc"},
		{plain, url.PathEscape("SOC A"), "hub_to_soc"},
		{url.PathEscape("SOC A"), url.PathEscape(thai), "soc_to_hub"},
		{"ALANG-A", url.PathEscape("SOCE (บัวโรย)"), "hub_to_soc"},
		{url.PathEscape("SOCE (บัวโรย)"), "ALANG-A", "soc_to_hub"},
	} {
		r := w.get("/v1/distances/"+c.origin+"/"+c.destination, ta)
		if r.status != http.StatusOK || r.data()["direction"] != c.direction {
			t.Errorf("%s -> %s: %d %s", c.origin, c.destination, r.status, r.raw)
		}
	}
	if r := w.get("/v1/distances?hubId="+url.QueryEscape(thai), ta); len(r.list()) != 2 {
		t.Fatalf("?hubId: %s", r.raw)
	}
	expect(t, w.getRaw("/v1/distances/%ZZ/SOCE", ta), http.StatusNotFound, "not_found")
}
