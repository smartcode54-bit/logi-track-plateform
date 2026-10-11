//go:build integration

// Integration tests of T40 (issue #60): vehicle expenses (the W8 server filter, staff entries, the audit with the PM
// check, the toll import) and maintenance records (transitions with the truck in one transaction, reminders, the PM
// check route), against PostgreSQL 18 with the full goose chain (pgtest; every request as logitrack_app through
// db.WithPrincipal, so RLS decides), Redis 7, the real auth service and RBAC, the storage service on the local backend
// and, where a test asks for it, the class C write-back into an in-memory Firestore stand-in (writeback.Memory).
// Fixture: own fleet O; carrier A working for O; independent carrier C; carrier D, the organisation of a dispatcher
// over customer X; staff of O (every role), A and C; a platform admin. The maintenance routes are tested here too:
// one harness serves both packages (an approval books maintenance).
package expenses_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const pw = "a long passphrase 1"

// now is the pinned clock of the services: 2026-10-11 10:00 in Bangkok.
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
	internal string
	hash     string
	tokens   map[string]string
	wb       *writeback.Memory

	O, A, C, D string // tenants
	// drivers: dO (home truck tO), dO2 (no truck), dA (home truck tA), dC
	dO, dO2, dA, dC string
	// trucks: tO (next service at 100,000 km), tO2, tA, tC
	tO, tO2, tA, tC string
}

type option func(*options)

type options struct{ wb *writeback.Memory }

// withWriteBack runs the services with the class C write-back into an in-memory Firestore stand-in.
func withWriteBack(m *writeback.Memory) option { return func(o *options) { o.wb = m } }

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func newWorld(t *testing.T, opts ...option) *world {
	t.Helper()
	var o options
	for _, f := range opts {
		f(&o)
	}
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, pool: d.Pool(t, db.RoleApp), tokens: map[string]string{}, wb: o.wb}
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
	publicAddr := freeAddr(t)
	local, err := storage.NewLocal(storage.LocalConfig{Dir: t.TempDir(), PublicBaseURL: "http://" + publicAddr + "/media",
		SigningKey: bytes.Repeat([]byte("f"), 40), APIUploadPath: storage.LocalUploadPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	st, err := storage.New(w.pool, storage.Config{Active: storage.BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public",
		PresignPutTTL: 15 * time.Minute, PresignGetTTL: time.Hour, UploadMaxBytes: 1 << 20}, zerolog.Nop(), local)
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
	clock := func() time.Time { return now }
	fs, err := fleet.New(fleet.Deps{Pool: w.pool, Files: st, Log: zerolog.Nop(), Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	var wb writeback.Writer
	if o.wb != nil {
		wb = o.wb
	}
	ms, err := maintenance.New(maintenance.Deps{Pool: w.pool, Files: st, WriteBack: wb, Log: zerolog.Nop(), Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	es, err := expenses.New(expenses.Deps{Pool: w.pool, Files: st, PM: ms, WriteBack: wb, Log: zerolog.Nop(), Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	deps := app.APIDeps{Auth: svc, Fleet: fs, Storage: st, Jobs: jobs.NewService(w.pool, jobs.NewRedisLocker(rdb, ks)),
		Limiter: limiter}
	groups := append(svc.Groups(), fleet.Groups(fs, svc.RequireAuth(), maintenance.TruckRoutes(ms, svc.RequireAuth()))...)
	groups = append(groups, expenses.Groups(es, svc.RequireAuth())...)
	groups = append(groups, maintenance.Groups(ms, svc.RequireAuth())...)
	groups = append(groups, app.StorageGroups(deps)...)
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

func (w *world) str(sql string, args ...any) *string {
	w.t.Helper()
	var v *string
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return v
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

func (w *world) driver(tenant, first string) string {
	return w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, full_name_th, mobile, legacy_auth_uid)
		VALUES ($1, 'self', $2, 'Dee', $2 || ' ดี', '0812345678', 'uid_' || $2) RETURNING id::text`, tenant, first)
}

func (w *world) truck(tenant, plate string, extra ...string) string {
	own := "own"
	if tenant != w.O {
		own = "subcontractor"
	}
	id := w.id(`INSERT INTO trucks (tenant_id, tenant_source, ownership_type, license_plate, province, status, brand, model,
		year, color, type_raw, vehicle_class, legacy_doc_id)
		VALUES ($1, 'self', $2, $3, 'กรุงเทพมหานคร', 'active', 'Isuzu', 'D-Max', 2022, 'ขาว', '4 Wheels Jumbo', '4WJ', 'trk_' || $3)
		RETURNING id::text`, tenant, own, plate)
	for i := 0; i+1 < len(extra); i += 2 {
		w.exec(`UPDATE trucks SET `+extra[i]+` = `+extra[i+1]+` WHERE id = $1`, id)
	}
	return id
}

func (w *world) assign(truck, drv string) {
	a := w.id(`INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status)
		SELECT tenant_id, 'truck', id, $2, 'active' FROM trucks WHERE id = $1 RETURNING id::text`, truck, drv)
	w.exec(`UPDATE drivers SET current_assignment_id = $2, status = 'on_duty' WHERE id = $1`, drv, a)
}

func (w *world) fixture() {
	w.O = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'O', 'O') RETURNING id::text`)
	w.A = w.carrier("A", &w.O)
	w.C = w.carrier("C", nil)
	w.D = w.carrier("D", nil)
	x := w.id(`INSERT INTO customers (code, name) VALUES ('X', 'X Co.') RETURNING id::text`)
	partyX := w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, x)
	for _, u := range []struct{ email, tenant, role string }{
		{"ta.o", w.O, "tenant_admin"}, {"mg.o", w.O, "manager"}, {"os.o", w.O, "operation_staff"},
		{"op.o", w.O, "operator"}, {"us.o", w.O, "user"}, {"ta.a", w.A, "tenant_admin"}, {"ta.c", w.C, "tenant_admin"},
	} {
		w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, w.user(u.email+"@example.test"), u.tenant, u.role)
	}
	ds := w.user("ds@example.test") // dispatcher: operator of its own organisation D + a grant over X (R13)
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'operator')`, ds, w.D)
	w.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'dispatcher', $2)`, ds, partyX)
	pa := w.user("pa@example.test")
	w.exec(`INSERT INTO user_platform_roles (user_id, role) VALUES ($1, 'platform_admin')`, pa)
	// A tenant override: users of O read fuel expenses only (accounting:view_fuel), so the per-type rule shows.
	w.exec(`INSERT INTO role_capability_overrides (tenant_id, role, capability, allowed, updated_by)
		VALUES ($1, 'user', 'accounting:view_fuel', true, $2)`, w.O, pa)

	w.dO, w.dO2, w.dA, w.dC = w.driver(w.O, "Somchai"), w.driver(w.O, "Somsak"), w.driver(w.A, "Anan"), w.driver(w.C, "Chai")
	w.tO = w.truck(w.O, "กข-1001", "next_service_mileage", "100000", "current_mileage", "95000")
	w.tO2 = w.truck(w.O, "กข-1002")
	w.tA = w.truck(w.A, "กค-2001", "next_service_mileage", "50000")
	w.tC = w.truck(w.C, "กง-3001")
	w.assign(w.tO, w.dO)
	w.assign(w.tA, w.dA)
}

// expense inserts an expense as the mirror or the ETL would (no API rules); extra are column / SQL literal pairs.
func (w *world) expense(tenant, typ, drv, truck string, at time.Time, amount float64, status string, extra ...string) string {
	cols := []string{"tenant_id", "tenant_source", "driver_id", "truck_id", "truck_license_plate_snapshot", "expense_type",
		"expense_at", "amount_thb", "status"}
	vals := map[string]string{"tenant_id": "$1", "tenant_source": "'driver'", "driver_id": "$2::uuid", "truck_id": "$3::uuid",
		"truck_license_plate_snapshot": "(SELECT license_plate FROM trucks WHERE id = $3::uuid)", "expense_type": "$4",
		"expense_at": "$5", "amount_thb": "$6", "status": "$7"}
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
	var d, tr any
	if drv != "" {
		d = drv
	}
	if truck != "" {
		tr = truck
	}
	return w.id(`INSERT INTO vehicle_expenses (`+strings.Join(cols, ", ")+`) VALUES (`+strings.Join(list, ", ")+`) RETURNING id::text`,
		tenant, d, tr, typ, at, amount, status)
}

// events counts the outbox rows of a routing key.
func (w *world) events(key string) int {
	return w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = $1`, key)
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

func (r resp) meta() map[string]any {
	m, _ := r.body["meta"].(map[string]any)
	return m
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

// upload presigns purpose for the caller, sends the bytes to the signed local URL and returns the key.
func (w *world) upload(bearer, purpose, entityID, contentType, variant string, body []byte) string {
	w.t.Helper()
	req := map[string]any{"purpose": purpose, "entityId": entityID, "contentType": contentType, "sizeBytes": len(body),
		"fileName": "photo"}
	if variant != "" {
		req["variant"] = variant
	}
	r := w.post("/v1/uploads/presign", bearer, req)
	expect(w.t, r, http.StatusOK, "")
	key, _ := r.data()["key"].(string)
	u, _ := r.data()["url"].(string)
	put, err := http.NewRequest(http.MethodPut, w.internal+u, bytes.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	for k, v := range r.data()["headers"].(map[string]any) {
		put.Header.Set(k, v.(string))
	}
	res, err := http.DefaultClient.Do(put)
	if err != nil {
		w.t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		w.t.Fatalf("upload %s: %d", key, res.StatusCode)
	}
	return key
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) float64 { f, _ := v.(float64); return f }

var jpeg = append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), bytes.Repeat([]byte{7}, 64)...)
