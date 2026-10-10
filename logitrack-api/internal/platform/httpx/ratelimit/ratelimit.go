// Package ratelimit is the GCRA rate limiter of the rl: namespace (Appendix B §B.6.3, Appendix C
// §C.4.12): one Redis key per bucket and subject, updated atomically by a Lua script that reads the
// Redis server clock, so every api replica shares one limit and host clock skew does not matter. A
// request is counted when it is checked (there is no separate "peek"), so concurrent requests cannot
// all pass a check made before any of them was counted.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
)

// Limit allows Count requests per Window: a burst of up to Count, then one every Window/Count.
type Limit struct {
	Count  int
	Window time.Duration
}

// ParseLimit reads the "count/window" form of RATE_LIMIT_* (window a Go duration, e.g. 10/1m). The
// interval window/count must be at least 1µs, the resolution of the script.
func ParseLimit(s string) (Limit, error) {
	c, w, ok := strings.Cut(strings.TrimSpace(s), "/")
	n, err1 := strconv.Atoi(strings.TrimSpace(c))
	d, err2 := time.ParseDuration(strings.TrimSpace(w))
	lim := Limit{Count: n, Window: d}
	if !ok || err1 != nil || err2 != nil || !lim.valid() {
		return Limit{}, errors.New("ratelimit: want count/window, e.g. 10/1m")
	}
	return lim, nil
}

func (l Limit) String() string { return strconv.Itoa(l.Count) + "/" + l.Window.String() }

// valid: a positive count and an emission interval window/count of at least 1µs.
func (l Limit) valid() bool {
	return l.Count > 0 && l.Window > 0 && l.Window/time.Duration(l.Count) >= time.Microsecond
}

// ErrInvalidLimit is returned for an invalid limit or a cost outside 1..Count: a programming or
// configuration error, never a Redis condition. Config.Validate and Middleware reject such limits
// before a request arrives.
var ErrInvalidLimit = errors.New("ratelimit: invalid limit or cost")

// Decision is the outcome of one request against a bucket.
type Decision struct {
	Allowed    bool
	Remaining  int           // requests still allowed right now
	RetryAfter time.Duration // when denied: wait at least this long
	ResetAfter time.Duration // until the bucket is full again
}

// callTimeout bounds one check: a slow, hung or stopped Redis costs a request at most this long (the
// client honours context deadlines, cache.Open).
const callTimeout = 300 * time.Millisecond

// Limiter checks buckets of one keyspace.
type Limiter struct {
	rdb     redis.UniversalClient
	ks      cache.Keyspace
	log     zerolog.Logger
	metrics *metrics
}

// New builds a limiter; log receives fail-open warnings.
func New(rdb redis.UniversalClient, ks cache.Keyspace, log zerolog.Logger) *Limiter {
	return &Limiter{rdb: rdb, ks: ks, log: log, metrics: newMetrics()}
}

// Register adds the limiter metrics to a Prometheus registry.
func (l *Limiter) Register(reg prometheus.Registerer) error { return l.metrics.register(reg) }

// SubjectKey is the subject part of rl:{bucket}:{subject}: the first 16 bytes of an unkeyed sha256 in
// hex, so every key has the same length and no raw IP address or email appears in a key name
// (Appendix C §C.4.12 hashes the email). This pseudonymises and does not anonymise: an IPv4 subject is
// recovered by hashing all 2^32 addresses, an email by hashing candidates, so rl: keys are personal
// data like the rest of Redis.
func SubjectKey(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:16])
}

// Allow counts one request of subject against bucket.
func (l *Limiter) Allow(ctx context.Context, bucket, subject string, lim Limit) (Decision, error) {
	return l.AllowN(ctx, bucket, subject, lim, 1)
}

// AllowN counts n requests at once (1 <= n <= lim.Count). A denied request consumes nothing.
// Errors come back with Allowed = true: a Redis failure, which the caller may fail open on (the
// middleware does, Appendix B §B.6.3), or ErrInvalidLimit, a bug the caller must not treat as one.
func (l *Limiter) AllowN(ctx context.Context, bucket, subject string, lim Limit, n int) (Decision, error) {
	if !lim.valid() || n < 1 || n > lim.Count {
		return Decision{Allowed: true}, fmt.Errorf("%w: %s, cost %d, bucket %s", ErrInvalidLimit, lim, n, bucket)
	}
	emission := max(1, (lim.Window / time.Duration(lim.Count)).Microseconds())
	tolerance := emission * int64(lim.Count)
	key := l.ks.RateLimit(bucket, SubjectKey(subject))
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	res, err := gcraScript.Run(ctx, l.rdb, []string{key}, emission, tolerance, n).Int64Slice()
	if err == nil && len(res) != 4 {
		err = errors.New("ratelimit: unexpected script reply")
	}
	if err != nil {
		l.metrics.errors.Inc()
		return Decision{Allowed: true}, err
	}
	d := Decision{
		Allowed:    res[0] == 1,
		Remaining:  int(res[1]),
		RetryAfter: time.Duration(res[2]) * time.Microsecond,
		ResetAfter: time.Duration(res[3]) * time.Microsecond,
	}
	outcome := "allowed"
	if !d.Allowed {
		outcome = "denied"
	}
	l.metrics.decisions.WithLabelValues(bucket, outcome).Inc()
	return d, nil
}

// Reset forgets a subject's state in a bucket.
func (l *Limiter) Reset(ctx context.Context, bucket, subject string) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return l.rdb.Del(ctx, l.ks.RateLimit(bucket, SubjectKey(subject))).Err()
}

// RetryAfterSeconds is the Retry-After header value of a denied decision (at least 1).
func (d Decision) RetryAfterSeconds() int {
	return max(1, int(math.Ceil(d.RetryAfter.Seconds())))
}

// gcraScript is the generic cell rate algorithm. State: the theoretical arrival time (TAT) in
// microseconds of the Redis clock. ARGV: emission interval T (µs), delay tolerance τ = T·Count (µs),
// cost n. A request of cost n is allowed when max(TAT, now) + n·T − τ ≤ now; it is counted in the same
// step (TAT becomes max(TAT, now) + n·T and the key lives until TAT).
// Reply: {allowed, remaining, retry_after_us, reset_after_us}.
var gcraScript = redis.NewScript(`
local emission = tonumber(ARGV[1])
local tolerance = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
local tat = tonumber(redis.call('GET', KEYS[1]))
if (not tat) or tat < now then
  tat = now
end
local new_tat = tat + emission * cost
local diff = now - (new_tat - tolerance)
if diff < 0 then
  return {0, 0, -diff, tat - now}
end
-- string.format keeps the integer exact (tostring would use %.14g)
redis.call('SET', KEYS[1], string.format('%.0f', new_tat), 'PX', string.format('%.0f', math.max(1, math.ceil((new_tat - now) / 1000))))
return {1, math.floor(diff / emission), 0, new_tat - now}`)

type metrics struct {
	decisions *prometheus.CounterVec
	errors    prometheus.Counter
}

func newMetrics() *metrics {
	return &metrics{
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratelimit_decisions_total",
			Help: "Rate-limit decisions by bucket and outcome (allowed, denied).",
		}, []string{"bucket", "outcome"}),
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ratelimit_errors_total",
			Help: "Rate-limit checks that could not reach Redis in time (the middleware then fails open).",
		}),
	}
}

func (m *metrics) register(reg prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{m.decisions, m.errors} {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}
