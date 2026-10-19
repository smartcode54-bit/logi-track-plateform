//go:build integration

// Integration tests of T22 (issue #42): trucks, their status history, files, renewals with their ledger rows, the
// renewal overview and the import, against PostgreSQL 18 with the full goose chain (pgtest; every request as
// logitrack_app through db.WithPrincipal, so RLS decides), Redis 7, the real auth service and RBAC, and the storage
// service on the local backend. Fixture (Appendix C §C.9.2, trimmed): own fleet O; carrier A working for O; independent
// carrier C; carrier D, the organisation of a dispatcher over customer X; staff of O (every role), A and C; a platform
// admin.
package fleet_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
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

// now is the pinned clock of the fleet service: 2026-10-11 10:00 in Bangkok, so the renewal overview's "today" is
// 2026-10-11.
var now = time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)

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
	internal string
	hash     string
	tokens   map[string]string

	O, A, C, D string // tenants
	partyX     string // the customer X's billing party (the dispatcher's grant)
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

	rdb, ks := cachetest.NewClient(t)
	caches := cache.New(rdb, ks)
	rbac, err := iam.NewRBAC(iam.Deps{Pool: w.pool, Cache: caches, Redis: rdb, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	// The public listener is bound up front and served as it is (FLAKE3): the local storage backend needs its
	// address before the API is built, and a port released here could be taken before the API binds it again.
	publicLn := listentest.Loopback(t)
	publicAddr := publicLn.Addr().String()
	local, err := storage.NewLocal(storage.LocalConfig{Dir: t.TempDir(), PublicBaseURL: "http://" + publicAddr + "/media",
		SigningKey: bytes.Repeat([]byte("f"), 40), APIUploadPath: storage.LocalUploadPath})
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
	limiter := ratelimit.New(rdb, ks, zerolog.Nop())
	svc, err := auth.New(auth.Config{RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour,
		PasswordResetTTL: 30 * time.Minute}, auth.Deps{
		Pool: w.pool, Store: auth.NewStore(rdb, ks.Prefix()), Limiter: limiter,
		Keys: token.New(priv, "http://localhost:8080", "logitrack-test", 15*time.Minute), Hasher: hasher,
		Policy: policy, Log: zerolog.Nop(), Capabilities: rbac, Authorizer: rbac, Files: st,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	fs, err := fleet.New(fleet.Deps{Pool: w.pool, Files: st, Log: zerolog.Nop(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	deps := app.APIDeps{Auth: svc, Fleet: fs, Storage: st, Jobs: jobs.NewService(w.pool, jobs.NewRedisLocker(rdb, ks)),
		Limiter: limiter}
	groups := append(svc.Groups(), fleet.Groups(fs, svc.RequireAuth())...)
	groups = append(groups, app.StorageGroups(deps)...)
	groups = append(groups, ingress.Group{Prefix: "/v1/fleettest", Mount: w.testRoutes(svc.RequireAuth())})
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

func (w *world) carrier(name string, contractor *string) string {
	t := w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type, contractor_tenant_id)
		VALUES ('carrier', $1, $1, 'company', $2) RETURNING id::text`, name, contractor)
	w.exec(`INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', $1)`, t)
	return t
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'O', 'O') RETURNING id::text`)
	w.A = w.carrier("A", &w.O)
	w.C = w.carrier("C", nil)
	w.D = w.carrier("D", nil)
	x := w.id(`INSERT INTO customers (code, name) VALUES ('X', 'X Co.') RETURNING id::text`)
	w.partyX = w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, x)
	for _, u := range []struct{ email, tenant, role string }{
		{"ta.o", w.O, "tenant_admin"}, {"mg.o", w.O, "manager"}, {"op.o", w.O, "operator"}, {"us.o", w.O, "user"},
		{"ta.a", w.A, "tenant_admin"}, {"ta.c", w.C, "tenant_admin"},
	} {
		w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, w.user(u.email+"@example.test"), u.tenant, u.role)
	}
	ds := w.user("ds@example.test") // dispatcher: operator of its own organisation D + a grant over X (R13)
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'operator')`, ds, w.D)
	w.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'dispatcher', $2)`, ds, w.partyX)
	w.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, w.user("pa@example.test"))
}

// truck inserts a truck as the ETL would (no API rules), created at the given instant; extra are column / SQL
// literal pairs that add or override columns.
func (w *world) truck(tenant, plate string, created time.Time, extra ...string) string {
	own := "'own'"
	if tenant != w.O {
		own = "'subcontractor'"
	}
	cols := []string{"tenant_id", "tenant_source", "ownership_type", "license_plate", "province", "status", "brand", "model",
		"year", "color", "type_raw", "vehicle_class", "created_at"}
	vals := map[string]string{"tenant_id": "$1", "tenant_source": "'self'", "ownership_type": own, "license_plate": "$2",
		"province": "'กรุงเทพมหานคร'", "status": "'active'", "brand": "'Isuzu'", "model": "'D-Max'", "year": "2022",
		"color": "'ขาว'", "type_raw": "'4 Wheels Jumbo'", "vehicle_class": "'4WJ'", "created_at": "$3"}
	for i := 0; i+1 < len(extra); i += 2 {
		if _, ok := vals[extra[i]]; !ok {
			cols = append(cols, extra[i])
		}
		vals[extra[i]] = extra[i+1]
	}
	list := make([]string, len(cols))
	for i, c := range cols {
		list[i] = vals[c]
	}
	return w.id(`INSERT INTO trucks (`+strings.Join(cols, ", ")+`) VALUES (`+strings.Join(list, ", ")+`) RETURNING id::text`,
		tenant, plate, created)
}

// --- test routes: SQL in db.WithPrincipal, so only RLS and the grants decide ------------------------------------

var mutations = map[string]string{
	"update-status-history": `UPDATE status_history SET reason = 'rewritten' WHERE entity_type = 'truck'`,
	"delete-status-history": `DELETE FROM status_history WHERE entity_type = 'truck'`,
	"update-transactions":   `UPDATE transactions SET amount_thb = 0`,
	"delete-transactions":   `DELETE FROM transactions`,
}

func (w *world) testRoutes(requireAuth fiber.Handler) func(r fiber.Router) {
	return func(r fiber.Router) {
		r.Use(requireAuth)
		// A write the API never makes, rolled back: the sqlstate PostgreSQL answers for the caller.
		r.Post("/mutate/:what", func(c fiber.Ctx) error {
			q, ok := mutations[c.Params("what")]
			if !ok {
				return httpx.ErrNotFound()
			}
			state := ""
			err := db.WithPrincipal(c.Context(), w.pool, authz.PrincipalFrom(c), func(tx pgx.Tx) error {
				_, err := tx.Exec(c.Context(), q)
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

func (w *world) send(method, path, bearer, contentType string, body io.Reader, headers ...string) resp {
	w.t.Helper()
	req, err := http.NewRequest(method, w.internal+path, body)
	if err != nil {
		w.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
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
	out := resp{status: res.StatusCode, raw: raw}
	if len(raw) > 0 && strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			w.t.Fatalf("%s %s: not JSON: %s", method, path, raw)
		}
	}
	return out
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
	return w.send(method, path, bearer, "application/json", rd, headers...)
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

// multipart posts a file (field "file") and form fields.
func (w *world) multipart(path, bearer, fileName string, file []byte, fields map[string]string) resp {
	w.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			w.t.Fatal(err)
		}
	}
	if file != nil {
		fw, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			w.t.Fatal(err)
		}
		if _, err := fw.Write(file); err != nil {
			w.t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		w.t.Fatal(err)
	}
	return w.send(http.MethodPost, path, bearer, mw.FormDataContentType(), &buf)
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

// upload presigns purpose for the caller, sends bytes through the web broker or legacy signed URL and returns the key.
func (w *world) upload(bearer, purpose, entityID, contentType string, body []byte) string {
	w.t.Helper()
	return w.uploadNamed(bearer, purpose, entityID, contentType, "doc", body)
}

// uploadNamed is upload with a file name (keys carry a millisecond stamp and the name: uploads in a loop name
// their files apart).
func (w *world) uploadNamed(bearer, purpose, entityID, contentType, fileName string, body []byte) string {
	w.t.Helper()
	r := w.post("/v1/uploads/presign", bearer, map[string]any{"purpose": purpose, "entityId": entityID,
		"contentType": contentType, "sizeBytes": len(body), "fileName": fileName})
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
	// Web-session grants use the authenticated broker; legacy signed PUTs stay unsigned.
	if strings.HasPrefix(u, "/v1/uploads/") && strings.HasSuffix(u, "/content") {
		req.Header.Set("Authorization", "Bearer "+bearer)
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

func str(v any) string { s, _ := v.(string); return s }

var png = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 64)...)

var pdf = append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{9}, 64)...)

// newTruck is a valid POST /v1/trucks body with overrides.
func newTruck(plate string, extra map[string]any) map[string]any {
	b := map[string]any{"licensePlate": plate, "province": "กรุงเทพมหานคร", "status": "active", "brand": "Isuzu",
		"model": "D-Max", "year": 2022, "color": "ขาว", "type": "4 Wheels Jumbo"}
	for k, v := range extra {
		b[k] = v
	}
	return b
}
