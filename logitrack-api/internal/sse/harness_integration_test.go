//go:build integration

// Integration tests of T12 (main spec §8, Appendix B §B.4, issue #32): PostgreSQL 18 with the goose chain
// (pgtest, requests as logitrack_app), Redis 7 (cachetest), the real auth service and iam.RBAC, and api
// replicas built with app.NewAPI that share both, each with its own realtime.Hub. Events reach Redis the
// way the scheduler's relay writes them: realtime.Writer, or the relay itself draining the outbox.
package sse_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/sse"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const (
	pw = "a long passphrase 1"
	// ping keeps the tests fast: a closed client is noticed at the next heartbeat.
	ping = 200 * time.Millisecond
	wait = 10 * time.Second
)

func TestMain(m *testing.M) {
	app.DrainGrace = 0
	code := pgtest.Main(m)
	cachetest.TerminateShared()
	os.Exit(code)
}

type world struct {
	t      *testing.T
	pool   *pgxpool.Pool
	etl    *pgx.Conn
	rdb    *redis.Client
	ks     cache.Keyspace
	keys   *token.KeySet
	auth   *auth.Service
	writer *realtime.Writer
	hash   string
	// own is the own-fleet tenant, other an independent carrier.
	own, other string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, pool: d.Pool(t, db.RoleApp)}
	var err error
	if w.etl, err = pgx.Connect(ctx, d.URL(db.RoleETL)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.etl.Close(context.Background()) })
	w.exec(`SELECT set_config('app.bypass_tenant', 'on', false)`) // fixtures play the system context
	hasher, err := password.NewHasher(password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	if w.hash, err = hasher.Hash(ctx, pw); err != nil {
		t.Fatal(err)
	}
	w.own = w.id(`INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'O', 'O') RETURNING id::text`)
	w.other = w.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type) VALUES ('carrier', 'C', 'C', 'company') RETURNING id::text`)

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
	w.keys = token.New(priv, "http://localhost:8080", "logitrack-test", 15*time.Minute)
	policy, err := password.NewPolicy(10)
	if err != nil {
		t.Fatal(err)
	}
	if w.auth, err = auth.New(auth.Config{
		RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour, PasswordResetTTL: 30 * time.Minute,
	}, auth.Deps{
		Pool: w.pool, Store: auth.NewStore(w.rdb, w.ks.Prefix()), Limiter: ratelimit.New(w.rdb, w.ks, zerolog.Nop()),
		Keys: w.keys, Hasher: hasher, Policy: policy, Log: zerolog.Nop(), Capabilities: rbac, Authorizer: rbac,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.auth.Close)
	w.writer = realtime.NewWriter(w.rdb, w.ks, 1000, time.Hour)
	return w
}

// --- fixture (logitrack_etl, BYPASSRLS) -----------------------------------------------------------------

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

// member creates an active user signing in with pw and its membership.
func (w *world) member(email, tenant, role string) string {
	u := w.id(`INSERT INTO users (email, email_verified, display_name, password_hash)
		VALUES ($1::text, true, $1::text, $2) RETURNING id::text`, email, w.hash)
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)`, u, tenant, role)
	return u
}

// driver creates a driver user of tenant with its drivers row; it returns the user and driver ids.
func (w *world) driver(email, tenant string) (string, string) {
	u := w.member(email, tenant, "driver")
	d := w.id(`INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, mobile)
		VALUES ($1, 'self', $2, 'สมชาย', 'ใจดี', '0800000001') RETURNING id::text`, tenant, u)
	return u, d
}

// --- replicas -------------------------------------------------------------------------------------------

type replica struct {
	internal, public string
	hub              *realtime.Hub
	stop             func()
}

// replicaDeps replace collaborators of the SSE service, so a test can run a step inside the connect
// window: pool wraps the logitrack_app pool of the chat checks, reader is the replay reader's Redis
// client.
type replicaDeps struct {
	pool   db.Beginner
	reader redis.UniversalClient
}

func (w *world) replica(cfg sse.Config) *replica {
	w.t.Helper()
	return w.replicaWith(cfg, replicaDeps{})
}

func (w *world) replicaWith(cfg sse.Config, deps replicaDeps) *replica {
	w.t.Helper()
	if deps.pool == nil {
		deps.pool = w.pool
	}
	if deps.reader == nil {
		deps.reader = w.rdb
	}
	if cfg.PingInterval == 0 {
		cfg.PingInterval = ping
	}
	if cfg.MaxConnPerUser == 0 {
		cfg.MaxConnPerUser = 5
	}
	hub := realtime.NewHub(w.rdb, w.ks, zerolog.Nop(), 0)
	events, err := sse.New(cfg, sse.Deps{
		Hub: hub, Reader: realtime.NewReader(deps.reader, w.ks, time.Hour),
		Conns:   ratelimit.NewConnLimiter(w.rdb, w.ks, cfg.MaxConnPerUser, app.SSELease(cfg.PingInterval)),
		Tickets: w.auth, Sessions: w.auth, Pool: deps.pool, Log: zerolog.Nop(),
	})
	if err != nil {
		w.t.Fatal(err)
	}
	groups := append(w.auth.Groups(), events.Groups(w.auth.RequireAuth())...)
	a, err := app.NewAPI(&app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 5 * time.Second},
		InternalAddr: "127.0.0.1:0", PublicAddr: "127.0.0.1:0", PublicRouteGroups: ingress.PublicPrefixes,
	}, zerolog.New(zerolog.NewTestWriter(w.t)).Level(zerolog.WarnLevel), groups...)
	if err != nil {
		w.t.Fatal(err)
	}
	a.OnServe(func(ctx context.Context) { _ = hub.Run(ctx) })
	a.OnDrain(events.Drain)
	if err := a.Listen(); err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Serve(ctx); close(done) }()
	var once sync.Once
	r := &replica{hub: hub, stop: func() { once.Do(func() { cancel(); <-done }) }}
	w.t.Cleanup(r.stop)
	in, pub, _ := a.Addrs()
	r.internal, r.public = "http://"+in, "http://"+pub
	deadline := time.Now().Add(wait)
	for !hub.Ready() {
		if time.Now().After(deadline) {
			w.t.Fatal("the hub never confirmed its PSUBSCRIBE")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return r
}

// --- HTTP -----------------------------------------------------------------------------------------------

type resp struct {
	status int
	header http.Header
	body   map[string]any
	raw    []byte
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

func (w *world) call(method, url, bearer string, body any) resp {
	w.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
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
	out := resp{status: res.StatusCode, header: res.Header}
	out.raw, _ = io.ReadAll(res.Body)
	if len(out.raw) > 0 {
		_ = json.Unmarshal(out.raw, &out.body)
	}
	return out
}

// session is one sign-in: the access token and its session id.
type session struct {
	access string
	sid    string
}

func (w *world) login(base, email, platform string) session {
	w.t.Helper()
	r := w.call(http.MethodPost, base+"/v1/auth/login", "", map[string]any{
		"email": email, "password": pw, "platform": platform, "installId": "install-" + uuid.NewString(),
	})
	data, _ := r.body["data"].(map[string]any)
	access, _ := data["accessToken"].(string)
	if r.status != http.StatusOK || access == "" {
		w.t.Fatalf("login %s: %d %s", email, r.status, r.raw)
	}
	c, err := w.keys.Parse(access, time.Now())
	if err != nil {
		w.t.Fatal(err)
	}
	return session{access: access, sid: c.SessionID}
}

func (w *world) ticket(base string, s session) string {
	w.t.Helper()
	r := w.call(http.MethodPost, base+"/v1/auth/sse-ticket", s.access, nil)
	data, _ := r.body["data"].(map[string]any)
	tk, _ := data["ticket"].(string)
	if r.status != http.StatusOK || tk == "" || data["expiresIn"] != float64(60) {
		w.t.Fatalf("sse-ticket: %d %s", r.status, r.raw)
	}
	return tk
}

// publish writes an event the way the relay does and returns its sequence id.
func (w *world) publish(typ, data string, topics ...string) int64 {
	w.t.Helper()
	n, err := w.writer.Publish(context.Background(), realtime.Event{
		Type: typ, EventID: uuid.NewString(), Topics: topics, Data: json.RawMessage(data),
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return n
}

// --- SSE client -----------------------------------------------------------------------------------------

type frame struct {
	ID    string
	HasID bool
	Event string
	Data  string
	Retry string
}

// envelope decodes the data of an event frame.
func (f frame) envelope(t *testing.T) (typ, topic string, data map[string]any) {
	t.Helper()
	var e struct {
		Type    string         `json:"type"`
		Topic   string         `json:"topic"`
		EventID string         `json:"eventId"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(f.Data), &e); err != nil || e.EventID == "" && f.Event != "reconnect" && f.Event != "resync" {
		t.Fatalf("frame data %q: %v", f.Data, err)
	}
	return e.Type, e.Topic, e.Data
}

func (f frame) reason(t *testing.T) string {
	t.Helper()
	var r struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(f.Data), &r); err != nil {
		t.Fatalf("control data %q: %v", f.Data, err)
	}
	return r.Reason
}

type client struct {
	t      *testing.T
	frames chan frame
	eof    chan struct{}
	cancel context.CancelFunc
	header http.Header
}

// open dials a stream; any answer but 200 comes back as a resp and a nil client.
func (w *world) open(url string, headers ...string) (*client, resp) {
	w.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		w.t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		defer cancel()
		defer func() { _ = res.Body.Close() }()
		out := resp{status: res.StatusCode, header: res.Header}
		out.raw, _ = io.ReadAll(res.Body)
		_ = json.Unmarshal(out.raw, &out.body)
		return nil, out
	}
	c := &client{t: w.t, frames: make(chan frame, 4096), eof: make(chan struct{}), cancel: cancel, header: res.Header}
	go func() {
		defer close(c.eof)
		defer func() { _ = res.Body.Close() }()
		r := bufio.NewReader(res.Body)
		var f frame
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if f != (frame{}) {
					c.frames <- f
				}
				f = frame{}
				continue
			}
			if strings.HasPrefix(line, ":") {
				continue // heartbeat comment
			}
			k, v, _ := strings.Cut(line, ":")
			v = strings.TrimPrefix(v, " ")
			switch k {
			case "id":
				f.ID, f.HasID = v, true
			case "event":
				f.Event = v
			case "data":
				f.Data = v
			case "retry":
				f.Retry = v
			}
		}
	}()
	w.t.Cleanup(c.close)
	return c, resp{status: res.StatusCode, header: res.Header}
}

// mustOpen is open for a stream that must start.
func (w *world) mustOpen(url string, headers ...string) *client {
	w.t.Helper()
	c, r := w.open(url, headers...)
	if c == nil {
		w.t.Fatalf("open %s: %d %s", url, r.status, r.raw)
	}
	return c
}

func (c *client) close() { c.cancel() }

// next returns the next frame of any kind.
func (c *client) next() frame {
	c.t.Helper()
	select {
	case f := <-c.frames:
		return f
	case <-c.eof:
		select {
		case f := <-c.frames:
			return f
		default:
		}
		c.t.Fatal("the stream ended")
	case <-time.After(wait):
		c.t.Fatal("no frame in time")
	}
	return frame{}
}

// event returns the next frame that carries an event (skipping retry and id-only frames).
func (c *client) event() frame {
	c.t.Helper()
	for {
		if f := c.next(); f.Event != "" {
			return f
		}
	}
}

// start reads the opening frames: retry, then the id-only frame naming the current sequence.
func (c *client) start() string {
	c.t.Helper()
	if f := c.next(); f.Retry != "3000" {
		c.t.Fatalf("first frame %+v, want retry: 3000", f)
	}
	f := c.next()
	if !f.HasID || f.Event != "" {
		c.t.Fatalf("second frame %+v, want the id-only frame", f)
	}
	return f.ID
}

// ends waits until the server closed the stream.
func (c *client) ends() {
	c.t.Helper()
	select {
	case <-c.eof:
	case <-time.After(wait):
		c.t.Fatal("the stream did not end")
	}
}

func expect(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status || r.code() != code {
		t.Fatalf("got %d %q, want %d %q: %s", r.status, r.code(), status, code, r.raw)
	}
}
