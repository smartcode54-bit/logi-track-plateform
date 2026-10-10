// Package cache is the Redis layer of the Go processes (main spec §2.1, Appendix B §B.6): the client,
// the lt:{APP_ENV}: keyspace and its eight namespaces (R26), read-through caches with post-commit and
// outbox-event invalidation, the rt:cache pub/sub that drops in-process copies on other replicas, the
// mirror read-your-writes ack, single-use tickets, and the money-path guard.
//
// Every cache: key is a display or UI hint (R17, R53): pricing, period locks, invoice numbering and
// payroll read PostgreSQL inside their transaction and run under MoneyPath, where this package
// refuses to serve. A Redis failure therefore never fails a request here: reads fall through to the
// loader (PostgreSQL) and the failure is logged and counted.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

// TTLs are the configurable lifetimes of the cache: keys (main spec §16.1). The other lifetimes are
// the constants below (Appendix B §B.6.2).
type TTLs struct {
	Hubs     time.Duration `env:"CACHE_TTL_HUBS" envDefault:"10m"`
	RateCard time.Duration `env:"CACHE_TTL_RATECARD" envDefault:"1h"`
	Settings time.Duration `env:"CACHE_TTL_SETTINGS" envDefault:"5m"`
}

// DefaultTTLs are the design values of Appendix B §B.6.2.
var DefaultTTLs = TTLs{Hubs: 10 * time.Minute, RateCard: time.Hour, Settings: 5 * time.Minute}

// Validate implements config.Validator: every TTL between 1s and 24h.
func (t TTLs) Validate() error {
	var errs []string
	for _, v := range []struct {
		name string
		d    time.Duration
	}{{"CACHE_TTL_HUBS", t.Hubs}, {"CACHE_TTL_RATECARD", t.RateCard}, {"CACHE_TTL_SETTINGS", t.Settings}} {
		if v.d < time.Second || v.d > 24*time.Hour {
			errs = append(errs, config.Invalidf(v.name, "must be between 1s and 24h"))
		}
	}
	if len(errs) > 0 {
		return &config.Error{Invalid: errs}
	}
	return nil
}

// Fixed lifetimes of Appendix B §B.6.2.
const (
	PeriodLocksTTL = 10 * time.Minute
	SubtenantsTTL  = 10 * time.Minute
	OwnFleetTTL    = time.Hour
	MirrorAckTTL   = 10 * time.Minute
	CustomerTTL    = time.Hour
	WebFlagsTTL    = time.Minute
)

// Cache serves the cache: namespace of one keyspace.
type Cache struct {
	rdb     redis.UniversalClient
	ks      Keyspace
	ttl     TTLs
	log     zerolog.Logger
	l1      *l1
	metrics *metrics
}

// Option configures New.
type Option func(*Cache)

// WithTTLs sets the configurable lifetimes (default DefaultTTLs).
func WithTTLs(t TTLs) Option { return func(c *Cache) { c.ttl = t } }

// WithLogger sets the logger for Redis failures (default: disabled).
func WithLogger(l zerolog.Logger) Option { return func(c *Cache) { c.log = l } }

// WithL1 keeps decoded values in process for at most ttl (bounded by maxEntries). Run must then be
// running so deletions on other replicas, published on rt:cache, drop the copies here.
func WithL1(maxEntries int, ttl time.Duration) Option {
	return func(c *Cache) {
		if maxEntries > 0 && ttl > 0 {
			c.l1 = newL1(maxEntries, ttl)
		}
	}
}

// New wraps a client (built by Open, or any client passed through Guard).
func New(rdb redis.UniversalClient, ks Keyspace, opts ...Option) *Cache {
	c := &Cache{rdb: rdb, ks: ks, ttl: DefaultTTLs, log: zerolog.Nop(), metrics: newMetrics()}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Keys returns the keyspace.
func (c *Cache) Keys() Keyspace { return c.ks }

// TTLs returns the configured lifetimes.
func (c *Cache) TTLs() TTLs { return c.ttl }

// Register adds the cache metrics to a Prometheus registry.
func (c *Cache) Register(reg prometheus.Registerer) error { return c.metrics.register(reg) }

// Ping checks the connection (readiness).
func (c *Cache) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

// redisFailed logs and counts a Redis error the caller recovers from.
func (c *Cache) redisFailed(ctx context.Context, op string, err error) {
	c.metrics.errors.WithLabelValues(op).Inc()
	l := zerolog.Ctx(ctx)
	if l.GetLevel() == zerolog.Disabled {
		l = &c.log
	}
	l.Warn().Err(err).Str("component", "cache").Str("op", op).Msg("redis unavailable, serving from source")
}

// GetJSON is a read-through cache of one string key holding JSON: a hit is decoded, a miss (or an
// undecodable value) calls load and stores its result for ttl. When Redis fails the loader answers
// and nothing is stored. Under MoneyPath it returns ErrMoneyPath without calling load.
//
// Values are shared by every caller and tenant: load must read the same data whoever asks (system
// context), and callers authorise access before they ask. With WithL1 the returned value is also the
// in-process copy: callers must not modify maps or slices they get.
func GetJSON[T any](ctx context.Context, c *Cache, key string, ttl time.Duration, load func(context.Context) (T, error)) (T, error) {
	var zero T
	if IsMoneyPath(ctx) {
		return zero, ErrMoneyPath
	}
	if v, ok := c.l1.get(key); ok {
		if t, ok := v.(T); ok {
			c.metrics.lookups.WithLabelValues("l1").Inc()
			return t, nil
		}
	}
	b, err := c.rdb.Get(ctx, key).Bytes()
	switch {
	case err == nil:
		var t T
		if json.Unmarshal(b, &t) == nil {
			c.metrics.lookups.WithLabelValues("hit").Inc()
			c.l1.put(key, t)
			return t, nil
		}
	case errors.Is(err, redis.Nil):
	default:
		c.redisFailed(ctx, "get", err)
		c.metrics.lookups.WithLabelValues("error").Inc()
		return load(ctx)
	}
	c.metrics.lookups.WithLabelValues("miss").Inc()
	t, err := load(ctx)
	if err != nil {
		return zero, err
	}
	enc, err := json.Marshal(t)
	if err != nil {
		return zero, fmt.Errorf("cache: encode %T: %w", t, err)
	}
	if err := c.rdb.Set(ctx, key, enc, ttl).Err(); err != nil {
		c.redisFailed(ctx, "set", err)
		return t, nil
	}
	c.l1.put(key, t)
	return t, nil
}

// Invalidate deletes keys and publishes them on rt:cache so every replica drops its in-process
// copies. Services call it after their transaction commits (post-commit DEL); the outbox relay calls
// OnEvent for the same change once it relays the event, which closes the window in which a reader
// that loaded before the commit stores the old value again. The local copies are dropped even when
// Redis fails; the error is returned for logging and never fails the write that caused it.
func (c *Cache) Invalidate(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	c.l1.drop(keys...)
	if IsMoneyPath(ctx) {
		return ErrMoneyPath
	}
	msg, err := json.Marshal(invalidation{Keys: keys})
	if err != nil {
		return err
	}
	_, err = c.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, keys...)
		p.Publish(ctx, c.ks.CacheChannel(), msg)
		return nil
	})
	if err != nil {
		c.redisFailed(ctx, "invalidate", err)
		return fmt.Errorf("cache: invalidate: %w", err)
	}
	c.metrics.invalidations.Add(float64(len(keys)))
	return nil
}

// invalidation is the rt:cache message: the full keys that were deleted.
type invalidation struct {
	Keys []string `json:"keys"`
}

// Run subscribes to rt:cache and drops the in-process copies other replicas invalidate, until ctx
// ends. Without WithL1 it returns at once. Each (re)subscription flushes the in-process copies,
// because messages published while the subscription was down are lost.
func (c *Cache) Run(ctx context.Context) error {
	if c.l1 == nil {
		return nil
	}
	ps := c.rdb.Subscribe(ctx, c.ks.CacheChannel())
	defer func() { _ = ps.Close() }()
	ch := ps.ChannelWithSubscriptions(redis.WithChannelSize(1024))
	for {
		select {
		case <-ctx.Done():
			return nil
		case m, ok := <-ch:
			if !ok {
				return nil
			}
			switch m := m.(type) {
			case *redis.Subscription:
				if m.Kind == "subscribe" {
					c.l1.flush()
				}
			case *redis.Message:
				var inv invalidation
				if err := json.Unmarshal([]byte(m.Payload), &inv); err != nil {
					c.l1.flush()
					continue
				}
				c.l1.drop(inv.Keys...)
			}
		}
	}
}

// metrics are the cache instruments; registered by Register.
type metrics struct {
	lookups       *prometheus.CounterVec
	errors        *prometheus.CounterVec
	invalidations prometheus.Counter
}

func newMetrics() *metrics {
	return &metrics{
		lookups: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cache_lookups_total",
			Help: "Read-through cache lookups by result (l1, hit, miss, error).",
		}, []string{"result"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cache_redis_errors_total",
			Help: "Redis failures the cache recovered from (served from source or skipped), by operation.",
		}, []string{"op"}),
		invalidations: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cache_invalidated_keys_total",
			Help: "Cache keys deleted by post-commit hooks and outbox events.",
		}),
	}
}

func (m *metrics) register(reg prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{m.lookups, m.errors, m.invalidations} {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}
