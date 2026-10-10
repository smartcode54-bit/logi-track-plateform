// Package idempotency is the Idempotency-Key middleware of the replayable driver-app writes (the ✱
// routes of Appendix B §B.2; §B.1.5, R53, R63). Scope is (user id, key) and the fingerprint is the
// sha256 of method, request path (with its query) and body. A completed 2xx response is kept in Redis
// idem:http:{userId}:{key} for 24 h and in idempotency_keys until IDEMPOTENCY_TTL (default 168h); a
// replay with the same fingerprint gets the stored status and body byte for byte (header
// Idempotent-Replayed: true), a different fingerprint gets 409 idempotency_conflict.
//
// Concurrency: idem:lock:{userId}:{key} (SET NX PX 30000) turns a concurrent duplicate away early,
// and the in_progress row of idempotency_keys is the durable claim, so a Redis outage degrades to
// PostgreSQL alone. Both last LockTTL (30 s); an in_progress row older than that is taken over, as
// its holder is presumed dead. So that a live holder is never taken over, the route runs under a
// deadline of HandlerBudget (25 s) counted from before the lock and the claim: ✱ handlers do their
// work under c.Context(), so a request that cannot finish in time has its database work cancelled
// and rolled back, and its claim released, before the lease ends. A handler that ignores its context
// and outlives the lease can run a second time for a retry; the route's natural key (client_op_id,
// R63) then decides, and the late response is not stored over the newer one (ErrTakenOver). A request
// that ends in anything but 2xx committed nothing: its claim is released and the client may retry
// with the same key. The natural keys stay the last line of defence after IDEMPOTENCY_TTL.
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Header names.
const (
	Header         = "Idempotency-Key"
	HeaderReplayed = "Idempotent-Replayed"
)

// Lifetimes of Appendix B §B.6.2.
const (
	HotTTL  = 24 * time.Hour   // idem:http
	LockTTL = 30 * time.Second // idem:lock and the takeover age of an abandoned in_progress row
)

// HandlerBudget is how long a route may run behind the middleware with the default LockTTL: the lease
// minus a sixth of it, kept for storing the response (the budget is LockTTL - LockTTL/6 when tests
// shorten the lease).
const HandlerBudget = LockTTL - LockTTL/6

// redisTimeout bounds each Redis call (the client honours context deadlines, cache.Open). After the
// first Redis failure a request skips Redis, so a slow or hung Redis costs it at most this long before
// the middleware carries on with PostgreSQL alone.
const redisTimeout = 300 * time.Millisecond

// Config is IDEMPOTENCY_TTL (main spec §16.1).
type Config struct {
	TTL time.Duration `env:"IDEMPOTENCY_TTL" envDefault:"168h"`
}

// Validate implements config.Validator.
func (c Config) Validate() error {
	if c.TTL < time.Hour || c.TTL > 30*24*time.Hour {
		return &config.Error{Invalid: []string{config.Invalidf("IDEMPOTENCY_TTL", "must be between 1h and 720h")}}
	}
	return nil
}

// Options configure New.
type Options struct {
	Redis redis.UniversalClient // nil: PostgreSQL only
	Keys  cache.Keyspace
	Store Store
	TTL   time.Duration // durable lifetime, IDEMPOTENCY_TTL
	// Scope returns the authenticated principal's user id; "" answers 401 unauthenticated.
	Scope func(c fiber.Ctx) string
	Log   zerolog.Logger

	HotTTL  time.Duration // default HotTTL (tests shorten it)
	LockTTL time.Duration // default LockTTL
}

// Middleware is the Idempotency-Key handler; mount Handler on each ✱ route after authentication.
type Middleware struct {
	o       Options
	metrics *metrics
}

// New checks the options.
func New(o Options) (*Middleware, error) {
	if o.Store == nil || o.Scope == nil {
		return nil, errors.New("idempotency: Store and Scope are required")
	}
	if o.Redis != nil && o.Keys.Prefix() == "" {
		return nil, errors.New("idempotency: Keys is required with Redis")
	}
	if o.TTL <= 0 {
		o.TTL = 168 * time.Hour
	}
	if o.HotTTL <= 0 {
		o.HotTTL = HotTTL
	}
	if o.LockTTL <= 0 {
		o.LockTTL = LockTTL
	}
	return &Middleware{o: o, metrics: newMetrics()}, nil
}

// Register adds the middleware metrics to a Prometheus registry.
func (m *Middleware) Register(reg prometheus.Registerer) error { return m.metrics.register(reg) }

// Fingerprint is sha256(method + " " + request URI + "\n" + body) in hex (Appendix B §B.1.5).
func Fingerprint(method, requestURI string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{' '})
	h.Write([]byte(requestURI))
	h.Write([]byte{'\n'})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// CodeIdempotencyConflict is the 409 code of Appendix B §B.1.5.
const CodeIdempotencyConflict = "idempotency_conflict"

func errMismatch() error {
	return httpx.NewError(http.StatusConflict, CodeIdempotencyConflict,
		"Idempotency-Key was already used with a different request").WithDetails(map[string]any{"reason": "fingerprint_mismatch"})
}

func (m *Middleware) inFlight(c fiber.Ctx) error {
	m.metrics.outcomes.WithLabelValues("in_flight").Inc()
	c.Set(fiber.HeaderRetryAfter, "1")
	return httpx.NewError(http.StatusConflict, CodeIdempotencyConflict,
		"a request with this Idempotency-Key is still in progress").WithDetails(map[string]any{"reason": "in_flight"})
}

// Handler is the fiber middleware.
func (m *Middleware) Handler() fiber.Handler {
	return func(c fiber.Ctx) error {
		raw := c.Get(Header)
		if raw == "" {
			return httpx.ErrBadRequest("Idempotency-Key header is required").
				WithDetails(map[string]any{"header": Header, "reason": "missing"})
		}
		id, err := uuid.Parse(raw)
		if err != nil || len(raw) != 36 {
			return httpx.ErrBadRequest("Idempotency-Key must be a uuid").
				WithDetails(map[string]any{"header": Header, "reason": "malformed"})
		}
		key := id.String() // canonical lower case: one key whatever the client's casing
		scope := m.o.Scope(c)
		if scope == "" {
			return httpx.ErrUnauthenticated()
		}
		fp := Fingerprint(c.Method(), c.OriginalURL(), c.Body())
		ctx := c.Context()
		// The leases (Redis lock, durable claim) start after this instant, so the route's deadline,
		// counted from here, ends before either can be taken over.
		deadline := time.Now().Add(m.o.LockTTL - m.o.LockTTL/6)
		r := &request{m: m, scope: scope, key: key, redis: m.o.Redis != nil}

		// 1. Hot copy.
		if rec, ok := r.hotGet(ctx); ok {
			return m.replay(c, rec, fp, "replayed_redis")
		}
		// 2. In-flight lock; when Redis fails the durable claim below is the only guard.
		if locked, ok := r.lock(ctx); ok && !locked {
			return m.inFlight(c)
		}
		defer r.unlock()
		// 3. Durable claim.
		for range 3 {
			claimedAt, ok, err := m.o.Store.Claim(ctx, scope, key, fp, m.o.TTL, m.o.LockTTL)
			if err != nil {
				return httpx.ErrUnavailable("idempotency store unavailable").Wrap(err)
			}
			if ok {
				return m.execute(c, r, fp, claimedAt, deadline)
			}
			row, found, err := m.o.Store.Get(ctx, scope, key)
			if err != nil {
				return httpx.ErrUnavailable("idempotency store unavailable").Wrap(err)
			}
			if !found {
				continue // expired or released between the two statements: claim again
			}
			if row.Completed {
				r.hotSet(row.Record, time.Until(row.ExpiresAt))
				return m.replay(c, row.Record, fp, "replayed_postgres")
			}
			if row.Record.Fingerprint != fp {
				m.metrics.outcomes.WithLabelValues("conflict").Inc()
				return errMismatch()
			}
			return m.inFlight(c)
		}
		return m.inFlight(c)
	}
}

// replay answers from a stored record.
func (m *Middleware) replay(c fiber.Ctx, rec Record, fp, outcome string) error {
	if rec.Fingerprint != fp {
		m.metrics.outcomes.WithLabelValues("conflict").Inc()
		return errMismatch()
	}
	m.metrics.outcomes.WithLabelValues(outcome).Inc()
	c.Set(HeaderReplayed, "true")
	if rec.ContentType != "" {
		c.Set(fiber.HeaderContentType, rec.ContentType)
	}
	return c.Status(rec.Status).Send(rec.Bytes())
}

// execute runs the route under the deadline and records a 2xx response; anything else (an error, a
// non-2xx status, a streamed body or a panic) releases the claim.
func (m *Middleware) execute(c fiber.Ctx, r *request, fp string, claimedAt, deadline time.Time) error {
	settled := false
	defer func() {
		if !settled { // panic: release, then let the recover middleware answer
			m.release(r.scope, r.key, claimedAt)
		}
	}()
	parent := c.Context()
	hctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	c.SetContext(hctx)
	err := c.Next()
	c.SetContext(parent)
	status := c.Response().StatusCode()
	if err != nil || status < 200 || status > 299 || c.Response().IsBodyStream() {
		settled = true
		m.release(r.scope, r.key, claimedAt)
		return err
	}
	rec := NewRecord(fp, status, string(c.Response().Header.ContentType()), c.Response().Body())
	settled = true
	ctx, cancelStore := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancelStore()
	if err := m.o.Store.Complete(ctx, r.scope, r.key, claimedAt, rec); err != nil {
		if errors.Is(err, ErrTakenOver) {
			// This request outlived its lease and a retry took the key over: the retry owns the key
			// and stores its own response, so this one is stored nowhere (no hot copy either, which
			// would disagree with the durable one).
			m.metrics.outcomes.WithLabelValues("taken_over").Inc()
			m.log(parent).Error().Err(err).Str("component", "idempotency").Msg("the claim was taken over while the route ran; response not stored")
			return nil
		}
		// The response still goes out. The in_progress row turns stale after LockTTL; a replay then
		// runs again and meets the route's own natural key (client_op_id, R63).
		m.log(parent).Error().Err(err).Str("component", "idempotency").Msg("could not store the response")
	}
	r.hotSet(rec, m.o.TTL)
	m.metrics.outcomes.WithLabelValues("executed").Inc()
	return nil
}

func (m *Middleware) release(scope, key string, claimedAt time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.metrics.outcomes.WithLabelValues("released").Inc()
	if err := m.o.Store.Release(ctx, scope, key, claimedAt); err != nil {
		m.o.Log.Warn().Err(err).Str("component", "idempotency").Msg("could not release a claim (it expires after the lock TTL)")
	}
}

func (m *Middleware) log(ctx context.Context) *zerolog.Logger {
	if l := zerolog.Ctx(ctx); l.GetLevel() != zerolog.Disabled {
		return l
	}
	return &m.o.Log
}

func (m *Middleware) redisFailed(op string, err error) {
	m.metrics.redisErrors.WithLabelValues(op).Inc()
	m.o.Log.Warn().Err(err).Str("component", "idempotency").Str("op", op).Msg("redis unavailable, using PostgreSQL only")
}

// request carries one request's Redis state: after the first Redis failure the rest of the request
// skips Redis, so a hung Redis costs it one redisTimeout.
type request struct {
	m          *Middleware
	scope, key string
	redis      bool   // Redis configured and not failed in this request
	token      string // lock token while the lock is held
}

func (r *request) failed(op string, err error) {
	r.redis = false
	r.m.redisFailed(op, err)
}

func (r *request) hotGet(ctx context.Context) (Record, bool) {
	if !r.redis {
		return Record{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()
	b, err := r.m.o.Redis.Get(ctx, r.m.o.Keys.IdemHTTP(r.scope, r.key)).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			r.failed("get", err)
		}
		return Record{}, false
	}
	var rec Record
	if json.Unmarshal(b, &rec) != nil || rec.Fingerprint == "" || rec.Status == 0 {
		return Record{}, false // damaged entry: PostgreSQL decides
	}
	return rec, true
}

// hotSet stores the hot copy for min(HotTTL, remaining durable life).
func (r *request) hotSet(rec Record, remaining time.Duration) {
	if !r.redis {
		return
	}
	ttl := min(r.m.o.HotTTL, remaining)
	if ttl <= 0 {
		return
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()
	if err := r.m.o.Redis.Set(ctx, r.m.o.Keys.IdemHTTP(r.scope, r.key), b, ttl).Err(); err != nil {
		r.failed("set", err)
	}
}

// lock takes the in-flight lock; ok is false when Redis did not answer (the durable claim decides).
func (r *request) lock(ctx context.Context) (locked, ok bool) {
	if !r.redis {
		return false, false
	}
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()
	token := uuid.NewString()
	locked, err := r.m.o.Redis.SetNX(ctx, r.m.o.Keys.IdemLock(r.scope, r.key), token, r.m.o.LockTTL).Result()
	if err != nil {
		r.failed("lock", err)
		return false, false
	}
	if locked {
		r.token = token
	}
	return locked, true
}

// unlockScript deletes the lock only while it still holds this request's token.
var unlockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`)

func (r *request) unlock() {
	if r.token == "" || !r.redis {
		return // not held, or Redis failed meanwhile: the lock expires after LockTTL
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()
	if err := unlockScript.Run(ctx, r.m.o.Redis, []string{r.m.o.Keys.IdemLock(r.scope, r.key)}, r.token).Err(); err != nil {
		r.failed("unlock", err)
	}
}

type metrics struct {
	outcomes    *prometheus.CounterVec
	redisErrors *prometheus.CounterVec
}

func newMetrics() *metrics {
	return &metrics{
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "idempotency_requests_total",
			Help: "Idempotency-Key requests by outcome (executed, replayed_redis, replayed_postgres, conflict, in_flight, released, taken_over).",
		}, []string{"outcome"}),
		redisErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "idempotency_redis_errors_total",
			Help: "Redis failures of the idempotency middleware (PostgreSQL then decides alone), by operation.",
		}, []string{"op"}),
	}
}

func (m *metrics) register(reg prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{m.outcomes, m.redisErrors} {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}
