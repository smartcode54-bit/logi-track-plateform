//go:build integration

// Integration tests of T20 (issue #40): customers, companies and the carrier profiles of /v1/subcontractors against
// PostgreSQL 18 with the full goose chain (pgtest; every request as logitrack_app through db.WithPrincipal, so RLS
// decides), Redis 7, the real auth service and RBAC, and the storage service on the local backend (presign, the
// signed upload, commit, presigned GET). Fixture (Appendix C §C.9.2, trimmed): own fleet O; carrier A working for O;
// independent carrier C; customers X and Y with their billing parties; staff of O and A, the tenant_admin of C, a
// customer-scope user over X and a platform admin.
package parties_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/parties"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/parties/partiesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/listentest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const pw = "a long passphrase 1"

// Thai 13-digit ids: valid checksums and the same numbers one digit off.
const (
	taxOK  = "0105536000313"
	taxBad = "0105536000314"
	idOK   = "3100100123451"
	idBad  = "3100100123452"
)

func TestMain(m *testing.M) {
	app.DrainGrace = 0
	code := pgtest.Main(m)
	cachetest.TerminateShared()
	os.Exit(code)
}

type world struct {
	t        *testing.T
	db       *pgtest.Database
	pool     *pgxpool.Pool
	local    *storage.Local
	stCfg    storage.Config
	etl      *pgx.Conn
	rdb      *redis.Client
	ks       cache.Keyspace
	internal string
	public   string
	hash     string
	tokens   map[string]string

	O, A, C          string // tenants
	custX, custY     string // customers
	partyX, partyY   string // their billing parties
	partyA, partyC   string // the carriers' billing parties
	customerScopeUID string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, db: d, pool: d.Pool(t, db.RoleApp), tokens: map[string]string{}}
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
	// STORAGE_BACKEND=local; LOCAL_MEDIA_PUBLIC_BASE_URL reaches /media on the public listener.
	// Bound up front and served as it is (FLAKE3), so no other process can take the port in between.
	publicLn := listentest.Loopback(t)
	publicAddr := publicLn.Addr().String()
	local, err := storage.NewLocal(storage.LocalConfig{Dir: t.TempDir(), PublicBaseURL: "http://" + publicAddr + "/media",
		SigningKey: bytes.Repeat([]byte("p"), 40), APIUploadPath: storage.LocalUploadPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	w.local = local
	w.stCfg = storage.Config{Active: storage.BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public",
		PresignPutTTL: 15 * time.Minute, PresignGetTTL: time.Hour, UploadMaxBytes: 1 << 20}
	st, err := storage.New(w.pool, w.stCfg, zerolog.Nop(), local)
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
		Policy: policy, Log: zerolog.Nop(), Capabilities: rbac, Authorizer: rbac, Files: st,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	admin, err := iam.NewAdmin(iam.AdminDeps{Pool: w.pool, Auth: svc, Files: st, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := parties.New(parties.Deps{Pool: w.pool, Files: st, Cache: caches, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	deps := app.APIDeps{Auth: svc, Admin: admin, Parties: ps, Storage: st, Jobs: jobs.NewService(w.pool, jobs.NewRedisLocker(w.rdb, w.ks)),
		Limiter: limiter}
	groups := append(svc.Groups(), iam.AdminGroups(admin, svc.RequireAuth())...)
	groups = append(groups, parties.Groups(ps, svc.RequireAuth())...)
	groups = append(groups, app.StorageGroups(deps)...)
	groups = append(groups, ingress.Group{Prefix: "/v1/partiestest", Mount: w.testRoutes(svc.RequireAuth())})
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		StorageAPI:   app.StorageAPI{UploadMaxBytes: 1 << 20},
		InternalAddr: "127.0.0.1:0", PublicAddr: publicAddr, PublicRouteGroups: ingress.PublicPrefixes,
	}
	a, err := app.NewAPI(cfg, zerolog.Nop(), groups...)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CheckRoutes(); err != nil {
		t.Fatal(err)
	}
	if err := a.ListenOn(app.Listeners{Public: publicLn}); err != nil {
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

func (w *world) carrier(name string, contractor *string) (tenant, party string) {
	tenant = w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type, contractor_tenant_id)
		VALUES ('carrier', $1, $1, 'company', $2) RETURNING id::text`, name, contractor)
	party = w.id(`INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', $1) RETURNING id::text`, tenant)
	return tenant, party
}

func (w *world) customer(code string) (customer, party string) {
	customer = w.id(`INSERT INTO customers (code, name) VALUES ($1::text, $1::text || ' Co.') RETURNING id::text`, code)
	party = w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, customer)
	return customer, party
}

func (w *world) user(email string) string {
	return w.id(`INSERT INTO users (email, email_verified, display_name, password_hash)
		VALUES ($1::text, true, $1::text, $2) RETURNING id::text`, email, w.hash)
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'O', 'O') RETURNING id::text`)
	w.A, w.partyA = w.carrier("A", &w.O)
	w.C, w.partyC = w.carrier("C", nil)
	w.custX, w.partyX = w.customer("X")
	w.custY, w.partyY = w.customer("Y")
	w.exec(`INSERT INTO customer_driver_id_types (customer_id, key, label) VALUES ($1, 'workId', 'Work ID')`, w.custY)
	for _, u := range []struct{ email, tenant, role string }{
		{"ta.o", w.O, "tenant_admin"}, {"mg.o", w.O, "manager"}, {"op.o", w.O, "operator"},
		{"ta.a", w.A, "tenant_admin"}, {"mg.a", w.A, "manager"}, {"op.a", w.A, "operator"},
		{"ta.c", w.C, "tenant_admin"},
	} {
		w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, w.user(u.email+"@example.test"), u.tenant, u.role)
	}
	w.customerScopeUID = w.user("cu@example.test") // customer scope over X, no membership
	w.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'customer', $2)`, w.customerScopeUID, w.partyX)
	w.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, w.user("pa@example.test"))
}

// --- test routes: SQL in db.WithPrincipal, so only RLS decides ------------------------------------------

var countSQL = map[string]string{
	"customers":                `SELECT count(*) FROM customers`,
	"billing_parties":          `SELECT count(*) FROM billing_parties`,
	"customer_driver_id_types": `SELECT count(*) FROM customer_driver_id_types`,
	"companies":                `SELECT count(*) FROM companies`,
}

func (w *world) testRoutes(requireAuth fiber.Handler) func(r fiber.Router) {
	return func(r fiber.Router) {
		r.Use(requireAuth)
		r.Get("/count/:table", func(c fiber.Ctx) error {
			q, ok := countSQL[c.Params("table")]
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
		// A customer insert with no capability guard, rolled back: RLS alone answers (steward writes, R60).
		r.Post("/insert-customer", func(c fiber.Ctx) error {
			state := ""
			err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
				_, err := tx.Exec(c.Context(), `INSERT INTO customers (code, name) VALUES ('RLS1', 'RLS')`)
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
	}
}

var errRollback = errors.New("rollback")

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

func (w *world) do(base, method, path, bearer string, body any, headers ...string) resp {
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
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
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
	return w.do(w.internal, http.MethodGet, path, bearer, nil, headers...)
}

func (w *world) post(path, bearer string, body any, headers ...string) resp {
	w.t.Helper()
	return w.do(w.internal, http.MethodPost, path, bearer, body, headers...)
}

func (w *world) patch(path, bearer string, body any, headers ...string) resp {
	w.t.Helper()
	return w.do(w.internal, http.MethodPatch, path, bearer, body, headers...)
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

// upload presigns purpose for the caller, sends the bytes to the signed local URL and returns the key.
func (w *world) upload(bearer, purpose, entityID, variant, contentType string, body []byte) string {
	w.t.Helper()
	r := w.post("/v1/uploads/presign", bearer, map[string]any{"purpose": purpose, "entityId": entityID, "variant": variant,
		"contentType": contentType, "sizeBytes": len(body), "fileName": "doc"})
	expect(w.t, r, http.StatusOK, "")
	key, _ := r.data()["key"].(string)
	u, _ := r.data()["url"].(string)
	req, err := http.NewRequest(http.MethodPut, w.internal+u, bytes.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	for k, v := range r.data()["headers"].(map[string]any) {
		req.Header.Set(k, v.(string))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		w.t.Fatalf("upload %s: %d", key, res.StatusCode)
	}
	return key
}

// fetch GETs an absolute URL without credentials.
func fetch(t *testing.T, rawURL string) (int, []byte) {
	t.Helper()
	res, err := http.Get(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (w *world) rlsCount(bearer, table string) int {
	w.t.Helper()
	r := w.get("/v1/partiestest/count/"+table, bearer)
	expect(w.t, r, http.StatusOK, "")
	return int(r.data()["count"].(float64))
}

func (w *world) securityEvents(eventType, tenant string) []map[string]any {
	w.t.Helper()
	rows, err := w.etl.Query(context.Background(), `SELECT details FROM security_events WHERE event_type = $1 AND tenant_id = $2`,
		eventType, tenant)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var d map[string]any
		if err := rows.Scan(&d); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func str(v any) string { s, _ := v.(string); return s }

var png = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 64)...)

// --- customers ----------------------------------------------------------------------------------------

func TestCustomersCreateAndBillingParty(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")

	// AC: an invalid tax id checksum is 422 with field errors, and nothing is written.
	r := w.post("/v1/customers", taO, map[string]any{"code": "spx", "name": "Shopee", "taxId": taxBad})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["taxId"] != "invalid" || len(f) != 1 {
		t.Fatalf("fields = %v", f)
	}
	if n := w.count(`SELECT count(*) FROM customers`); n != 2 {
		t.Fatalf("a refused create wrote: %d customers", n)
	}

	// AC: a customer is created with exactly one billing_parties row, which carries the billing date basis (R6).
	r = w.post("/v1/customers", taO, map[string]any{"code": "spx", "name": "Shopee Express", "taxId": taxOK,
		"branchType": "branch", "branchNumber": "00001", "billingDateBasis": "plan", "billingEmail": "Billing@Shopee.test",
		"paymentTermsDays": 30, "driverIdTypes": []map[string]string{{"key": "appId", "label": "App ID"}, {"key": "workId", "label": "Work ID"}}})
	expect(t, r, http.StatusCreated, "")
	c := r.data()
	id, party := str(c["id"]), str(c["billingPartyId"])
	if c["code"] != "SPX" || c["billingDateBasis"] != "plan" || c["billingEmail"] != "billing@shopee.test" ||
		len(c["driverIdTypes"].([]any)) != 2 || party == "" {
		t.Fatalf("created: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM billing_parties WHERE customer_id = $1 AND kind = 'customer' AND id = $2
		AND billing_date_basis = 'plan'`, id, party); n != 1 {
		t.Fatalf("billing parties of the new customer: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM billing_parties WHERE customer_id = $1`, id); n != 1 {
		t.Fatalf("exactly one billing party, got %d", n)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'customers.changed' AND aggregate_id = $1
		AND realtime_topics = ARRAY['global'] AND payload->'ids' = to_jsonb(ARRAY[$1::text])`, id); n != 1 {
		t.Fatalf("outbox customers.changed: %d", n)
	}

	// The code is unique case-insensitively: 409 already_exists naming the field.
	r = w.post("/v1/customers", w.login("mg.o"), map[string]any{"code": "Spx", "name": "Again"})
	expect(t, r, http.StatusConflict, "already_exists")
	if r.detail("field") != "code" {
		t.Errorf("conflict details: %s", r.raw)
	}
	// A branch needs its 5-digit number.
	r = w.post("/v1/customers", taO, map[string]any{"code": "BR", "name": "Branch", "branchType": "branch"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["branchNumber"] != "required" {
		t.Errorf("fields = %v", r.fields())
	}
}

func TestCustomersPatchAndCache(t *testing.T) {
	w := newWorld(t)
	mgO := w.login("mg.o")
	ctx := context.Background()
	key := w.ks.Customer(w.custY)
	if err := w.rdb.Set(ctx, key, `{"stale":true}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	r := w.patch("/v1/customers/"+w.custY, mgO, map[string]any{"name": "Y Logistics", "billingDateBasis": "plan",
		"driverIdTypes": []map[string]string{{"key": "appId", "label": "App"}}, "lineGroupId": nil})
	expect(t, r, http.StatusOK, "")
	d := r.data()
	if d["name"] != "Y Logistics" || d["billingDateBasis"] != "plan" || len(d["driverIdTypes"].([]any)) != 1 ||
		str(d["billingPartyId"]) != w.partyY {
		t.Fatalf("patched: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM billing_parties WHERE id = $1 AND billing_date_basis = 'plan'`, w.partyY); n != 1 {
		t.Fatal("billing_date_basis lives on the billing party")
	}
	if n, _ := w.rdb.Exists(ctx, key).Result(); n != 0 {
		t.Fatal("cache:customer:{id} survived the write")
	}
	events := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'customers.changed' AND aggregate_id = $1`, w.custY)
	// The same values again change nothing and write nothing.
	r = w.patch("/v1/customers/"+w.custY, mgO, map[string]any{"name": "Y Logistics", "billingDateBasis": "plan",
		"driverIdTypes": []map[string]string{{"key": "appId", "label": "App"}}})
	expect(t, r, http.StatusOK, "")
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'customers.changed' AND aggregate_id = $1`, w.custY); n != events {
		t.Fatalf("a no-op PATCH emitted (%d -> %d)", events, n)
	}
	expect(t, w.patch("/v1/customers/"+uuid.NewString(), mgO, map[string]any{"name": "x"}), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/customers/not-a-uuid", mgO), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/customers?sort=name", mgO), http.StatusBadRequest, "bad_request")

	// Logo: presign (purpose customer_logo), signed upload, then the PATCH commits it; the payload carries a
	// presigned GET.
	logo := w.upload(mgO, "customer_logo", w.custY, "", "image/png", png)
	r = w.patch("/v1/customers/"+w.custY, mgO, map[string]any{"logoKey": logo})
	expect(t, r, http.StatusOK, "")
	if r.data()["logoKey"] != logo || !strings.Contains(str(r.data()["logoUrl"]), "X-LT-Signature=") {
		t.Fatalf("logo: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM file_objects WHERE object_key = $1 AND status = 'committed' AND owner_kind = 'customer'
		AND owner_id = $2`, logo, w.custY); n != 1 {
		t.Fatal("the logo was not committed to the customer")
	}
	r = w.get("/v1/customers/"+w.custY, mgO)
	expect(t, r, http.StatusOK, "")
	if r.data()["logoKey"] != logo {
		t.Fatalf("GET: %s", r.raw)
	}
}

// AC: a carrier tenant_admin cannot create or edit customers (403): global master data is written by stewards only
// (R60), at the capability layer and, without the guard, by RLS.
func TestCarrierCannotWriteCustomers(t *testing.T) {
	w := newWorld(t)
	for _, u := range []string{"ta.a", "mg.a", "ta.c"} {
		tok := w.login(u)
		r := w.post("/v1/customers", tok, map[string]any{"code": "NEW", "name": "New"})
		expect(t, r, http.StatusForbidden, "permission_denied")
		if !slices.Contains(r.detail("missingCapability").([]any), any("fleet:manage_customers")) {
			t.Errorf("%s: %s", u, r.raw)
		}
		expect(t, w.patch("/v1/customers/"+w.custX, tok, map[string]any{"name": "Hijack"}), http.StatusForbidden, "permission_denied")
		expect(t, w.get("/v1/customers/"+w.custX, tok), http.StatusForbidden, "permission_denied")
		expect(t, w.get("/v1/customers", tok), http.StatusForbidden, "permission_denied") // the full list
		// RLS refuses the write of a non-steward even without the capability guard.
		r = w.post("/v1/partiestest/insert-customer", tok, nil)
		if r.status != http.StatusOK || r.data()["sqlstate"] != "42501" {
			t.Errorf("%s RLS insert: %s", u, r.raw)
		}
	}
	if r := w.post("/v1/partiestest/insert-customer", w.login("op.o"), nil); r.data()["sqlstate"] != "" {
		t.Errorf("own-fleet staff (steward) insert under RLS: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM customers WHERE name = 'Hijack' OR code IN ('NEW', 'RLS1')`); n != 0 {
		t.Fatalf("%d refused writes landed", n)
	}
}

// AC: a customer-scoped user sees only its own billing party: the minimal list, and RLS on the three tables (0012).
func TestCustomerScopeSeesOnlyItsParty(t *testing.T) {
	w := newWorld(t)
	cu := w.login("cu")
	r := w.get("/v1/customers?fields=minimal", cu)
	expect(t, r, http.StatusOK, "")
	items := r.list()
	if len(items) != 1 || items[0]["id"] != w.custX || items[0]["billingPartyId"] != w.partyX || items[0]["code"] != "X" {
		t.Fatalf("customer scope list: %s", r.raw)
	}
	if _, ok := items[0]["taxId"]; ok {
		t.Error("the minimal projection carries a full field")
	}
	expect(t, w.get("/v1/customers", cu), http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/customers/"+w.custX, cu), http.StatusForbidden, "permission_denied")
	for table, want := range map[string]int{"billing_parties": 1, "customers": 1, "customer_driver_id_types": 0} {
		if n := w.rlsCount(cu, table); n != want {
			t.Errorf("customer scope RLS %s = %d, want %d", table, n, want)
		}
	}
	// Everyone else keeps the global read: X, Y and the two carriers' parties; Y's driver id type.
	opA := w.login("op.a")
	for table, want := range map[string]int{"billing_parties": 4, "customers": 2, "customer_driver_id_types": 1} {
		if n := w.rlsCount(opA, table); n != want {
			t.Errorf("carrier staff RLS %s = %d, want %d", table, n, want)
		}
	}
	r = w.get("/v1/customers?fields=minimal", opA)
	expect(t, r, http.StatusOK, "")
	if len(r.list()) != 2 {
		t.Fatalf("carrier staff minimal list: %s", r.raw)
	}
}

func TestCustomersPaging(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")
	expect(t, w.post("/v1/customers", taO, map[string]any{"code": "SPX", "name": "Shopee"}), http.StatusCreated, "")
	var codes []string
	cur := ""
	for range 5 {
		path := "/v1/customers?fields=minimal&limit=1"
		if cur != "" {
			path += "&cursor=" + url.QueryEscape(cur)
		}
		r := w.get(path, taO)
		expect(t, r, http.StatusOK, "")
		for _, it := range r.list() {
			codes = append(codes, str(it["code"]))
		}
		next, _ := r.body["nextCursor"].(string)
		if next == "" {
			break
		}
		cur = next
	}
	if !slices.Equal(codes, []string{"SPX", "X", "Y"}) {
		t.Fatalf("pages by code: %v", codes)
	}
	r := w.get("/v1/customers?q=sp", taO)
	expect(t, r, http.StatusOK, "")
	if len(r.list()) != 1 || r.list()[0]["code"] != "SPX" {
		t.Fatalf("q: %s", r.raw)
	}
	expect(t, w.get("/v1/customers?fields=all", taO), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/customers?cursor=nope", taO), http.StatusUnprocessableEntity, "invalid_argument")
}

// --- companies ----------------------------------------------------------------------------------------

func company(extra map[string]any) map[string]any {
	c := map[string]any{"nameTh": "วันเพ็ญ-รัชดา", "taxId": taxOK, "branchType": "headquarters", "address": "กรุงเทพมหานคร",
		"withholdingTaxRate": 1, "companyType": "subcontractor"}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

// AC: company assets are readable only through a presigned GET: the payload carries short-lived signed URLs, the
// object answers 403 without the signature, and GET /v1/files signs one only for staff in reach with company:view.
func TestCompanyAssetsPresignedOnly(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")
	pending := uuid.NewString() // the company does not exist yet; a foreign entity id in a key grants nothing (T11)
	logo := w.upload(taO, "company_logo", pending, "", "image/png", png)
	stamp := w.upload(taO, "company_stamp", pending, "", "image/png", png)
	sig := w.upload(taO, "company_signature", pending, "", "image/png", png)

	// AC: an invalid tax id is 422 with field errors.
	r := w.post("/v1/companies", taO, company(map[string]any{"taxId": taxBad, "companyType": "owner"}))
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["taxId"] != "invalid" {
		t.Fatalf("fields = %v", r.fields())
	}
	r = w.post("/v1/companies", taO, company(map[string]any{"companyType": "owner", "shortName": "WRT",
		"logoKey": logo, "stampKey": stamp, "signatureKey": sig, "withholdingTaxRate": 3.25}))
	expect(t, r, http.StatusCreated, "")
	c := r.data()
	id := str(c["id"])
	if c["tenantId"] != w.O || c["withholdingTaxRate"] != 3.25 || c["companyType"] != "owner" || c["logoKey"] != logo ||
		c["stampKey"] != stamp || c["signatureKey"] != sig {
		t.Fatalf("created: %s", r.raw)
	}
	for _, f := range []string{"logoUrl", "stampUrl", "signatureUrl"} {
		u := str(c[f])
		if !strings.HasPrefix(u, w.public+"/media/companies/") || !strings.Contains(u, "X-LT-Signature=") ||
			!strings.Contains(u, "X-LT-Expires=") {
			t.Fatalf("%s is not a presigned URL: %s", f, u)
		}
		if s, b := fetch(t, u); s != http.StatusOK || !bytes.Equal(b, png) {
			t.Fatalf("%s: %d", f, s)
		}
		unsigned := strings.Split(u, "?")[0]
		if s, _ := fetch(t, unsigned); s != http.StatusForbidden {
			t.Fatalf("%s without its signature: %d", f, s)
		}
	}
	if n := w.count(`SELECT count(*) FROM file_objects WHERE owner_kind = 'company' AND owner_id = $1 AND status = 'committed'
		AND visibility = 'private'`, id); n != 3 {
		t.Fatalf("committed private company files: %d", n)
	}
	// GET /v1/files: a manager of O (company:view) gets a 302 to a signed URL; an operator of O (no company:view)
	// and the carrier's staff (another tenant reach) get 404.
	r = w.get("/v1/files?key="+url.QueryEscape(stamp), w.login("mg.o"))
	if r.status != http.StatusFound || !strings.Contains(r.header.Get("Location"), "X-LT-Signature=") {
		t.Fatalf("files as mg.o: %d %s", r.status, r.raw)
	}
	for _, u := range []string{"op.o", "ta.a"} {
		expect(t, w.get("/v1/files?key="+url.QueryEscape(stamp), w.login(u)), http.StatusNotFound, "not_found")
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'companies.changed' AND aggregate_id = $1
		AND realtime_topics = ARRAY['global']`, id); n != 1 {
		t.Fatalf("outbox companies.changed: %d", n)
	}
	// Unlinking the stamp keeps the object (main spec §9.3) and drops it from the payload.
	r = w.patch("/v1/companies/"+id, taO, map[string]any{"stampKey": nil})
	expect(t, r, http.StatusOK, "")
	if r.data()["stampKey"] != nil || r.data()["stampUrl"] != nil || r.data()["logoKey"] != logo {
		t.Fatalf("unlink: %s", r.raw)
	}
}

func TestCompaniesReachAndOwner(t *testing.T) {
	w := newWorld(t)
	taO, taA, mgO := w.login("ta.o"), w.login("ta.a"), w.login("mg.o")
	r := w.post("/v1/companies", taO, company(map[string]any{"companyType": "owner"}))
	expect(t, r, http.StatusCreated, "")
	owner := str(r.data()["id"])
	// One owner company, and only the own fleet holds it.
	r = w.post("/v1/companies", mgO, company(map[string]any{"companyType": "owner", "nameTh": "อีกบริษัท"}))
	expect(t, r, http.StatusConflict, "already_exists")
	if r.detail("field") != "companyType" {
		t.Errorf("owner conflict: %s", r.raw)
	}
	r = w.post("/v1/companies", taA, company(map[string]any{"companyType": "owner"}))
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["companyType"] != "owner_own_fleet_only" {
		t.Fatalf("carrier owner: %v", r.fields())
	}
	r = w.post("/v1/companies", taA, company(map[string]any{"nameTh": "บริษัท เอ", "withholdingTaxRate": 3}))
	expect(t, r, http.StatusCreated, "")
	sub := str(r.data()["id"])
	if r.data()["tenantId"] != w.A {
		t.Fatalf("a company is stamped with the caller's tenant: %s", r.raw)
	}
	ids := func(tok string, headers ...string) []string {
		r := w.get("/v1/companies", tok, headers...)
		expect(t, r, http.StatusOK, "")
		var out []string
		for _, it := range r.list() {
			out = append(out, str(it["id"]))
		}
		slices.Sort(out)
		return out
	}
	both := []string{owner, sub}
	slices.Sort(both)
	if got := ids(taO); !slices.Equal(got, both) {
		t.Errorf("own fleet reads its carrier's companies (contractor reach): %v", got)
	}
	if got := ids(taA); !slices.Equal(got, []string{sub}) {
		t.Errorf("carrier A: %v", got)
	}
	if got := ids(w.login("ta.c")); len(got) != 0 {
		t.Errorf("independent carrier C: %v", got)
	}
	r = w.get("/v1/companies/owner", mgO)
	expect(t, r, http.StatusOK, "")
	if r.data()["id"] != owner {
		t.Fatalf("owner: %s", r.raw)
	}
	expect(t, w.get("/v1/companies/owner", taA), http.StatusNotFound, "not_found")
	// Writes follow the same reach: a carrier cannot touch the own fleet's company; the own fleet edits its own.
	expect(t, w.patch("/v1/companies/"+owner, taA, map[string]any{"withholdingTaxRate": 5}), http.StatusNotFound, "not_found")
	r = w.patch("/v1/companies/"+owner, taO, map[string]any{"withholdingTaxRate": 1.5, "isActive": false})
	expect(t, r, http.StatusOK, "")
	if r.data()["withholdingTaxRate"] != 1.5 || r.data()["isActive"] != false {
		t.Fatalf("patched: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM companies WHERE id = $1 AND withholding_tax_rate = 1.50`, owner); n != 1 {
		t.Fatal("withholding_tax_rate is NUMERIC(5,2)")
	}
	expect(t, w.patch("/v1/companies/"+owner, taO, map[string]any{"withholdingTaxRate": 1.234}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.patch("/v1/companies/"+sub, taA, map[string]any{"companyType": "owner"}), http.StatusUnprocessableEntity, "invalid_argument")
	// Capabilities: an operator holds neither company:view nor company:manage; a platform admin acts through
	// X-Act-On-Tenant.
	op := w.login("op.o")
	expect(t, w.get("/v1/companies", op), http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/companies", op, company(nil)), http.StatusForbidden, "permission_denied")
	pa := w.login("pa")
	expect(t, w.get("/v1/companies", pa), http.StatusForbidden, "permission_denied")
	if r := w.get("/v1/companies", pa, authz.HeaderActOnTenant, w.A); r.status != http.StatusOK || len(r.list()) != 1 {
		t.Fatalf("platform acting as A: %d %s", r.status, r.raw)
	}

	// X-Act-On-Tenant: * reads every tenant's companies and the owner company (the read-only bypass).
	r = w.post("/v1/companies", w.login("ta.c"), company(map[string]any{"nameTh": "บริษัท ซี"}))
	expect(t, r, http.StatusCreated, "")
	all := []string{owner, sub, str(r.data()["id"])}
	slices.Sort(all)
	if got := ids(pa, authz.HeaderActOnTenant, "*"); !slices.Equal(got, all) {
		t.Errorf("X-Act-On-Tenant: * lists %v, want %v", got, all)
	}
	if r := w.get("/v1/companies/owner", pa, authz.HeaderActOnTenant, "*"); r.status != http.StatusOK || r.data()["id"] != owner {
		t.Fatalf("owner under X-Act-On-Tenant: *: %d %s", r.status, r.raw)
	}
	if got := ids(taO); !slices.Equal(got, both) {
		t.Errorf("own fleet after C's company: %v", got)
	}
	// The queries name the reach themselves (main spec §4.7, Appendix C §C.3.8): run without RLS (logitrack_etl is
	// BYPASSRLS), they still return only the tenants they are given.
	ctx := context.Background()
	q := partiesdb.New(w.etl)
	rows, err := q.ListCompanies(ctx, partiesdb.ListCompaniesParams{TenantIds: []uuid.UUID{uuid.MustParse(w.A)}, RowLimit: 10})
	if err != nil || len(rows) != 1 || rows[0].ID.String() != sub {
		t.Fatalf("ListCompanies names the reach: %v %+v", err, rows)
	}
	if rows, err = q.ListCompanies(ctx, partiesdb.ListCompaniesParams{TenantIds: []uuid.UUID{}, RowLimit: 10}); err != nil || len(rows) != 0 {
		t.Fatalf("an empty reach reads nothing: %v %d", err, len(rows))
	}
	if rows, err = q.ListCompanies(ctx, partiesdb.ListCompaniesParams{AllTenants: true, TenantIds: []uuid.UUID{}, RowLimit: 10}); err != nil || len(rows) != 3 {
		t.Fatalf("all_tenants: %v %d", err, len(rows))
	}
	if _, err := q.GetOwnerCompany(ctx, partiesdb.GetOwnerCompanyParams{TenantIds: []uuid.UUID{uuid.MustParse(w.A)}}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetOwnerCompany outside the reach: %v", err)
	}
}

// Review of PR #121: a PATCH may send back the record it loaded. An asset key the company already links in that
// slot stays linked without a new commit, whoever uploaded it, so own-fleet staff (contractor reach) and a platform
// admin acting as another tenant can save a carrier's company; a key the company does not link still follows the
// commit rule (an upload of the caller or of its tenant).
func TestCompanyPatchKeepsLinkedKeys(t *testing.T) {
	w := newWorld(t)
	taA, taO, pa := w.login("ta.a"), w.login("ta.o"), w.login("pa")
	pending := uuid.NewString()
	keys := map[string]any{
		"logoKey":      w.upload(taA, "company_logo", pending, "", "image/png", png),
		"stampKey":     w.upload(taA, "company_stamp", pending, "", "image/png", png),
		"signatureKey": w.upload(taA, "company_signature", pending, "", "image/png", png),
	}
	r := w.post("/v1/companies", taA, company(keys))
	expect(t, r, http.StatusCreated, "")
	id := str(r.data()["id"])
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range keys {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	files := func() string {
		return w.id(`SELECT concat_ws(',', logo_file_id, stamp_file_id, signature_file_id) FROM companies WHERE id = $1`, id)
	}
	events := func() int {
		return w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'companies.changed' AND aggregate_id = $1`, id)
	}
	linked, n := files(), events()

	// The own fleet loads A's company through contractor reach and sends it back with its keys.
	r = w.get("/v1/companies", taO)
	expect(t, r, http.StatusOK, "")
	var loaded map[string]any
	for _, it := range r.list() {
		if it["id"] == id {
			loaded = it
		}
	}
	if loaded == nil || loaded["logoKey"] != keys["logoKey"] || loaded["signatureKey"] != keys["signatureKey"] {
		t.Fatalf("own fleet's view of A's company: %s", r.raw)
	}
	for _, who := range []struct {
		name, tok string
		headers   []string
	}{{"ta.o", taO, nil}, {"pa as O", pa, []string{authz.HeaderActOnTenant, w.O}}, {"ta.a", taA, nil}} {
		r = w.patch("/v1/companies/"+id, who.tok, with(nil), who.headers...)
		expect(t, r, http.StatusOK, "")
		if r.data()["logoKey"] != keys["logoKey"] || r.data()["stampKey"] != keys["stampKey"] || r.data()["signatureKey"] != keys["signatureKey"] {
			t.Fatalf("%s: %s", who.name, r.raw)
		}
	}
	if files() != linked || events() != n {
		t.Fatalf("sending the loaded keys back wrote (files %s -> %s, events %d -> %d)", linked, files(), n, events())
	}
	// With another change, the keys still pass and only the change lands.
	r = w.patch("/v1/companies/"+id, taO, with(map[string]any{"bankName": "SCB"}))
	expect(t, r, http.StatusOK, "")
	if r.data()["bankName"] != "SCB" || files() != linked || events() != n+1 {
		t.Fatalf("change with the keys sent back: %s", r.raw)
	}
	// A key the company does not link is committed as before: A's other upload is not the own fleet's to commit.
	other := w.upload(taA, "company_logo", pending, "", "image/png", png)
	r = w.patch("/v1/companies/"+id, taO, with(map[string]any{"logoKey": other}))
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["logoKey"] != "not_found" {
		t.Fatalf("foreign new key: %v", r.fields())
	}
	// The own fleet replaces the logo with its own upload; A's admin then sends the new key back.
	keys["logoKey"] = w.upload(taO, "company_logo", id, "", "image/png", png)
	r = w.patch("/v1/companies/"+id, taO, map[string]any{"logoKey": keys["logoKey"]})
	expect(t, r, http.StatusOK, "")
	if r.data()["logoKey"] != keys["logoKey"] {
		t.Fatalf("replace: %s", r.raw)
	}
	linked, n = files(), events()
	r = w.patch("/v1/companies/"+id, taA, with(nil))
	expect(t, r, http.StatusOK, "")
	if files() != linked || events() != n {
		t.Fatal("A's echo of the own fleet's logo wrote")
	}
	r = w.patch("/v1/companies/"+id, taA, with(map[string]any{"accountName": "บริษัท เอ จำกัด"}))
	expect(t, r, http.StatusOK, "")
	if r.data()["accountName"] != "บริษัท เอ จำกัด" || files() != linked {
		t.Fatalf("A's change with the keys sent back: %s", r.raw)
	}
}

// Review of PR #121: the same for a customer's logo: a steward acting as another tenant than the uploader's, and an
// ETL logo (no tenant, no uploader; RLS hides its row from every principal) sent back by the own fleet.
func TestCustomerPatchKeepsLinkedLogo(t *testing.T) {
	w := newWorld(t)
	mgO, pa := w.login("mg.o"), w.login("pa")
	logo := w.upload(mgO, "customer_logo", w.custY, "", "image/png", png)
	expect(t, w.patch("/v1/customers/"+w.custY, mgO, map[string]any{"logoKey": logo}), http.StatusOK, "")
	events := func(id string) int {
		return w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'customers.changed' AND aggregate_id = $1`, id)
	}
	n := events(w.custY)
	actA := []string{authz.HeaderActOnTenant, w.A}
	r := w.patch("/v1/customers/"+w.custY, pa, map[string]any{"name": "Y Co.", "logoKey": logo}, actA...)
	expect(t, r, http.StatusOK, "")
	if r.data()["logoKey"] != logo || events(w.custY) != n {
		t.Fatalf("pa as A sending Y back: %s (events %d -> %d)", r.raw, n, events(w.custY))
	}
	r = w.patch("/v1/customers/"+w.custY, pa, map[string]any{"name": "Y Holdings", "logoKey": logo}, actA...)
	expect(t, r, http.StatusOK, "")
	if r.data()["name"] != "Y Holdings" || r.data()["logoKey"] != logo || events(w.custY) != n+1 {
		t.Fatalf("pa as A changing Y: %s", r.raw)
	}

	const etlKey = "customers/legacy-x/logo.png"
	fid := w.id(`INSERT INTO file_objects (bucket, object_key, purpose, owner_kind, owner_id, visibility, status, storage_backend)
		VALUES ('logitrack', $1, 'customer_logo', 'customer', $2, 'private', 'missing_at_source', 's3') RETURNING id::text`, etlKey, w.custX)
	w.exec(`UPDATE customers SET logo_file_id = $1 WHERE id = $2`, fid, w.custX)
	r = w.get("/v1/customers/"+w.custX, mgO)
	expect(t, r, http.StatusOK, "")
	if r.data()["logoKey"] != etlKey || r.data()["logoUrl"] != nil {
		t.Fatalf("ETL logo: %s", r.raw)
	}
	n = events(w.custX)
	r = w.patch("/v1/customers/"+w.custX, mgO, map[string]any{"name": "X Co.", "logoKey": etlKey})
	expect(t, r, http.StatusOK, "")
	if r.data()["logoKey"] != etlKey || events(w.custX) != n {
		t.Fatalf("own fleet sending the ETL logo back: %s", r.raw)
	}
	if got := w.id(`SELECT logo_file_id::text FROM customers WHERE id = $1`, w.custX); got != fid {
		t.Fatalf("logo_file_id %s, want %s", got, fid)
	}
}

// Review of PR #121: GetCustomer signed the logo inside its request transaction, and SignedFile opens its own system
// transaction on the same pool, so each customer read or write with a logo held one connection while it waited for a
// second; with as many such requests as connections the pool, and every route with it, hung for good. Every parties
// call now finishes on a one-connection pool, and many concurrent ones on a two-connection pool.
func TestPartiesNeverNestPoolConnections(t *testing.T) {
	w := newWorld(t)
	mgO, taO := w.login("mg.o"), w.login("ta.o")
	logo := w.upload(mgO, "customer_logo", w.custY, "", "image/png", png)
	expect(t, w.patch("/v1/customers/"+w.custY, mgO, map[string]any{"logoKey": logo}), http.StatusOK, "")
	pending := uuid.NewString()
	assets := map[string]any{"logoKey": w.upload(taO, "company_logo", pending, "", "image/png", png),
		"stampKey": w.upload(taO, "company_stamp", pending, "", "image/png", png)}
	r := w.post("/v1/companies", taO, company(map[string]any{"companyType": "owner", "logoKey": assets["logoKey"],
		"stampKey": assets["stampKey"]}))
	expect(t, r, http.StatusCreated, "")
	companyID := uuid.MustParse(str(r.data()["id"]))
	y := uuid.MustParse(w.custY)

	services := func(maxConns int32) *parties.Service {
		pool, err := db.NewPool(context.Background(), w.db.URL(db.RoleApp), "pgtest-small", db.Options{MaxConns: maxConns})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		st, err := storage.New(pool, w.stCfg, zerolog.Nop(), w.local)
		if err != nil {
			t.Fatal(err)
		}
		ps, err := parties.New(parties.Deps{Pool: pool, Files: st, Log: zerolog.Nop()})
		if err != nil {
			t.Fatal(err)
		}
		return ps
	}
	// mg.o as iam.RBAC.Resolve completes it: own-fleet staff are stewards (R60).
	o := uuid.MustParse(w.O)
	p := &authz.Principal{UserID: uuid.MustParse(w.id(`SELECT id::text FROM users WHERE email = 'mg.o@example.test'`)),
		TenantID: &o, TenantRole: authz.Manager, TenantKind: authz.TenantKindOwnFleet, Steward: true,
		Caps: authz.NewCapSet(authz.FleetManageCustomers, authz.CompanyView, authz.CompanyManage)}

	ps := services(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := ps.GetCustomer(ctx, p, y)
	if err != nil || c.LogoKey == nil || *c.LogoKey != logo || c.LogoURL == nil {
		t.Fatalf("GetCustomer on one connection: %v %+v", err, c)
	}
	if _, err := ps.PatchCustomer(ctx, p, "", y, parties.CustomerInput{Name: parties.Field[string]{Set: true, V: "Y Logistics"},
		LogoKey: parties.Field[string]{Set: true, V: logo}}); err != nil {
		t.Fatalf("PatchCustomer on one connection: %v", err)
	}
	if _, _, err := ps.ListCustomers(ctx, p, parties.ListCustomersInput{}); err != nil {
		t.Fatalf("ListCustomers on one connection: %v", err)
	}
	if _, _, err := ps.ListCompanies(ctx, p, parties.ListCompaniesInput{}); err != nil {
		t.Fatalf("ListCompanies on one connection: %v", err)
	}
	if _, err := ps.OwnerCompany(ctx, p); err != nil {
		t.Fatalf("OwnerCompany on one connection: %v", err)
	}
	if _, err := ps.PatchCompany(ctx, p, "", companyID, parties.CompanyInput{BankName: parties.Field[string]{Set: true, V: "KBank"},
		LogoKey: parties.Field[string]{Set: true, V: str(assets["logoKey"])}, StampKey: parties.Field[string]{Set: true, V: str(assets["stampKey"])}}); err != nil {
		t.Fatalf("PatchCompany on one connection: %v", err)
	}

	ps = services(2)
	const callers = 8
	errs := make(chan error, 2*callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			c, err := ps.GetCustomer(ctx, p, y)
			if err == nil && c.LogoURL == nil {
				err = errors.New("no logo URL")
			}
			errs <- err
		})
		wg.Go(func() {
			_, err := ps.PatchCustomer(ctx, p, "", y, parties.CustomerInput{ContactName: parties.Field[string]{Set: true, V: fmt.Sprint("contact ", i)},
				LogoKey: parties.Field[string]{Set: true, V: logo}})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent customer calls on two connections: %v", err)
		}
	}
}

// --- carrier profiles (/v1/subcontractors) ---------------------------------------------------------------

func TestSubcontractorOnboarding(t *testing.T) {
	w := newWorld(t)
	taO := w.login("ta.o")

	// AC: invalid ID checksums are 422 with field errors; the legal type names the id it needs.
	r := w.post("/v1/subcontractors", taO, map[string]any{"nameTh": "นายรถร่วม", "legalType": "individual", "idCardNumber": idBad})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["idCardNumber"] != "invalid" {
		t.Fatalf("fields = %v", r.fields())
	}
	r = w.post("/v1/subcontractors", taO, map[string]any{"nameTh": "บริษัทรถร่วม", "legalType": "company", "taxId": taxBad})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["taxId"] != "invalid" {
		t.Fatalf("fields = %v", r.fields())
	}
	r = w.post("/v1/subcontractors", taO, map[string]any{"nameTh": "บริษัทรถร่วม", "legalType": "company"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["taxId"] != "required" {
		t.Fatalf("fields = %v", r.fields())
	}

	// AC: a carrier is created with exactly one billing_parties row; it works for the own fleet (R60).
	r = w.post("/v1/subcontractors", taO, map[string]any{"code": "NWR", "nameTh": "เอ็นดับบลิวอาร์", "legalType": "company",
		"taxId": taxOK, "billingDateBasis": "plan", "contactPerson": "คุณเอ", "phone": "0800000000", "fleetSize": 12})
	expect(t, r, http.StatusCreated, "")
	d := r.data()
	id := str(d["id"])
	if d["kind"] != "carrier" || d["contractorTenantId"] != w.O || d["billingDateBasis"] != "plan" || str(d["billingPartyId"]) == "" ||
		d["fleetSize"] != float64(12) || len(d["statusHistory"].([]any)) != 1 {
		t.Fatalf("created: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM billing_parties WHERE tenant_id = $1 AND kind = 'tenant' AND billing_date_basis = 'plan'`, id); n != 1 {
		t.Fatalf("billing parties of the new carrier: %d", n)
	}
	ev := w.securityEvents("tenant_created", id)
	if len(ev) != 1 || ev[0]["source"] != "subcontractors" {
		t.Fatalf("tenant_created: %v", ev)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'tenant.created' AND aggregate_id = $1
		AND payload->>'contractorTenantId' = $2`, id, w.O); n != 1 {
		t.Fatalf("outbox tenant.created: %d", n)
	}
	// Carriers onboard nobody (steward key, R60); a platform admin onboards for the own fleet too.
	r = w.post("/v1/subcontractors", w.login("ta.a"), map[string]any{"nameTh": "x", "legalType": "company", "taxId": taxOK})
	expect(t, r, http.StatusForbidden, "permission_denied")
	r = w.post("/v1/subcontractors", w.login("pa"), map[string]any{"nameTh": "แพลตฟอร์ม", "legalType": "individual", "idCardNumber": idOK})
	expect(t, r, http.StatusCreated, "")
	if r.data()["contractorTenantId"] != w.O || r.data()["idCardNumber"] != idOK {
		t.Fatalf("platform onboarding: %s", r.raw)
	}
	// Own-fleet staff without fleet:manage_subcontractors (an operator) cannot either.
	r = w.post("/v1/subcontractors", w.login("op.o"), map[string]any{"nameTh": "y", "legalType": "individual", "idCardNumber": idOK})
	expect(t, r, http.StatusForbidden, "permission_denied")
	if n := w.count(`SELECT count(*) FROM tenants WHERE kind = 'carrier'`); n != 4 {
		t.Fatalf("carriers after the onboarding: %d, want A, C, NWR and the platform's", n)
	}
}

func TestSubcontractorListsAndDetail(t *testing.T) {
	w := newWorld(t)
	names := func(r resp) []string {
		var out []string
		for _, it := range r.list() {
			out = append(out, str(it["nameTh"]))
		}
		return out
	}
	// Minimal: RLS rows. Carrier staff see their own tenant, own-fleet staff (stewards) every carrier, and a
	// customer-scope user the carrier names of the scope_tenants projection (no party id, no status).
	r := w.get("/v1/subcontractors?fields=minimal", w.login("op.a"))
	expect(t, r, http.StatusOK, "")
	if got := names(r); !slices.Equal(got, []string{"A"}) || r.list()[0]["billingPartyId"] != w.partyA {
		t.Fatalf("carrier operator: %s", r.raw)
	}
	opO := w.login("op.o")
	r = w.get("/v1/subcontractors?fields=minimal", opO)
	if got := names(r); !slices.Equal(got, []string{"A", "C"}) {
		t.Fatalf("own-fleet operator: %v", got)
	}
	r = w.get("/v1/subcontractors?fields=minimal", w.login("cu"))
	expect(t, r, http.StatusOK, "")
	if got := names(r); !slices.Equal(got, []string{"A", "C"}) {
		t.Fatalf("customer scope: %v", got)
	}
	for _, it := range r.list() {
		if _, ok := it["billingPartyId"]; ok {
			t.Fatalf("a scope principal got a party id: %s", r.raw)
		}
	}
	// Full list: fleet:manage_subcontractors (an operator lacks it).
	expect(t, w.get("/v1/subcontractors", opO), http.StatusForbidden, "permission_denied")
	taO := w.login("ta.o")
	r = w.get("/v1/subcontractors?limit=1", taO)
	expect(t, r, http.StatusOK, "")
	if names(r)[0] != "A" || r.list()[0]["billingDateBasis"] != "delivered" || str(r.body["nextCursor"]) == "" {
		t.Fatalf("full list page 1: %s", r.raw)
	}
	r = w.get("/v1/subcontractors?limit=1&cursor="+url.QueryEscape(str(r.body["nextCursor"])), taO)
	if got := names(r); !slices.Equal(got, []string{"C"}) || r.body["nextCursor"] != nil {
		t.Fatalf("full list page 2: %s", r.raw)
	}
	// Detail: members and stewards; another carrier 404; the own fleet is no carrier profile.
	expect(t, w.get("/v1/subcontractors/"+w.A, w.login("op.a")), http.StatusOK, "")
	expect(t, w.get("/v1/subcontractors/"+w.A, w.login("ta.c")), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/subcontractors/"+w.O, taO), http.StatusNotFound, "not_found")
}

func TestSubcontractorProfilePatch(t *testing.T) {
	w := newWorld(t)
	taA, opA, taO := w.login("ta.a"), w.login("op.a"), w.login("ta.o")

	// The carrier's own tenant_admin edits its profile; a carrier never edits another; staff without the role
	// are refused.
	r := w.patch("/v1/subcontractors/"+w.A, taA, map[string]any{"contactPerson": "คุณบี", "phone": "021234567",
		"serviceRegions": []string{"BKK", "BKK", "CNX"}})
	expect(t, r, http.StatusOK, "")
	if r.data()["contactPerson"] != "คุณบี" || len(r.data()["serviceRegions"].([]any)) != 2 {
		t.Fatalf("patched: %s", r.raw)
	}
	if ev := w.securityEvents("tenant_updated", w.A); len(ev) != 1 || ev[0]["source"] != "subcontractors" {
		t.Fatalf("tenant_updated: %v", ev)
	}
	expect(t, w.patch("/v1/subcontractors/"+w.C, taA, map[string]any{"phone": "1"}), http.StatusForbidden, "permission_denied")
	expect(t, w.patch("/v1/subcontractors/"+w.A, opA, map[string]any{"phone": "1"}), http.StatusForbidden, "permission_denied")
	// The billing date basis is a setting of the global billing party: stewards only.
	r = w.patch("/v1/subcontractors/"+w.A, taA, map[string]any{"billingDateBasis": "plan"})
	expect(t, r, http.StatusForbidden, "permission_denied")
	if r.detail("reason") != "steward_only" {
		t.Errorf("basis by the carrier: %s", r.raw)
	}
	r = w.patch("/v1/subcontractors/"+w.A, taO, map[string]any{"billingDateBasis": "plan"})
	expect(t, r, http.StatusOK, "")
	if n := w.count(`SELECT count(*) FROM billing_parties WHERE id = $1 AND billing_date_basis = 'plan'`, w.partyA); n != 1 {
		t.Fatal("the steward's basis change missed the billing party")
	}
	// Structural fields stay on PATCH /v1/tenants/{id}; identity numbers keep their checksum and legal-type rule.
	r = w.patch("/v1/subcontractors/"+w.A, taA, map[string]any{"status": "suspended", "code": "X"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["status"] != "not_editable" || f["code"] != "not_editable" {
		t.Fatalf("structural: %v", f)
	}
	r = w.patch("/v1/subcontractors/"+w.A, taA, map[string]any{"taxId": taxBad})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	r = w.patch("/v1/subcontractors/"+w.A, taA, map[string]any{"legalType": "individual"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["idCardNumber"] != "required" {
		t.Fatalf("individual without an ID card: %v", r.fields())
	}
	events := len(w.securityEvents("tenant_updated", w.A))
	expect(t, w.patch("/v1/subcontractors/"+w.A, taA, map[string]any{"contactPerson": "คุณบี"}), http.StatusOK, "")
	if n := len(w.securityEvents("tenant_updated", w.A)); n != events {
		t.Fatal("a no-op PATCH wrote an audit row")
	}

	// Documents: uploads of the tenant's admin are committed to the tenant; the ID card follows the rule of
	// idCardNumber (its admin and stewards) and GET /v1/files has no staff rule for it.
	doc := w.upload(taA, "tenant_document", w.A, "company_doc", "application/pdf", []byte("%PDF-1.4 company"))
	card := w.upload(taA, "tenant_document", w.A, "id_card", "application/pdf", []byte("%PDF-1.4 card"))
	r = w.patch("/v1/subcontractors/"+w.A, taA, map[string]any{"documentKeys": []string{doc, card}})
	expect(t, r, http.StatusOK, "")
	docs := r.data()["documents"].([]any)
	if len(docs) != 2 || docs[0].(map[string]any)["kind"] != "company_doc" || docs[1].(map[string]any)["kind"] != "id_card" ||
		!strings.Contains(str(docs[1].(map[string]any)["url"]), "X-LT-Signature=") {
		t.Fatalf("documents: %s", r.raw)
	}
	r = w.get("/v1/subcontractors/"+w.A, opA)
	expect(t, r, http.StatusOK, "")
	if docs := r.data()["documents"].([]any); len(docs) != 1 || docs[0].(map[string]any)["key"] != doc {
		t.Fatalf("a member without the PII rule: %s", r.raw)
	}
	expect(t, w.get("/v1/files?key="+url.QueryEscape(card), opA), http.StatusNotFound, "not_found")
	r = w.get("/v1/subcontractors/"+w.A, taO)
	if docs := r.data()["documents"].([]any); len(docs) != 2 {
		t.Fatalf("a steward sees the ID card: %s", r.raw)
	}
	// The steward re-saves the list (dropping the card): documents already linked are kept whoever uploaded them.
	r = w.patch("/v1/subcontractors/"+w.A, taO, map[string]any{"documentKeys": []string{doc}})
	expect(t, r, http.StatusOK, "")
	if docs := r.data()["documents"].([]any); len(docs) != 1 {
		t.Fatalf("after dropping the card: %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM file_objects WHERE object_key = $1 AND status = 'committed'`, card); n != 1 {
		t.Fatal("an unlinked document's object must stay")
	}
}
