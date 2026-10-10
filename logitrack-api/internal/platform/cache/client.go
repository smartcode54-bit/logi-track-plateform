package cache

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// Options configure Open. The values come from REDIS_URL, REDIS_KEY_PREFIX, REDIS_TLS and APP_ENV
// (main spec §16.1); the URL carries the password and is never logged or put into an error.
type Options struct {
	URL    string
	Prefix string // empty = lt:{AppEnv}:
	AppEnv string
	TLS    bool
}

// Open builds a client and the keyspace without dialling: the first command connects, so a process
// starts while Redis is still coming up and reports it through its readiness check. The client
// carries the money-path guard (MoneyPath) and honours context deadlines (ContextTimeoutEnabled), so a
// caller's context.WithTimeout bounds every call even when Redis accepts connections but never
// answers; without a deadline a call waits at most ReadTimeout (1 s by default) per try, with one
// retry.
func Open(o Options) (*redis.Client, Keyspace, error) {
	ks, err := ParseKeyspace(o.Prefix, o.AppEnv)
	if err != nil {
		return nil, Keyspace{}, err
	}
	ropts, err := redis.ParseURL(o.URL)
	if err != nil {
		return nil, Keyspace{}, errors.New("cache: REDIS_URL is not a valid redis:// or rediss:// URL")
	}
	if o.TLS && ropts.TLSConfig == nil {
		ropts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	// An unreachable or hung Redis must fail fast: every caller here has a PostgreSQL or fail-open
	// fallback and bounds its calls with a context deadline (300 ms in the rate limiter and the
	// idempotency middleware, callTimeout here). go-redis ignores context deadlines on socket reads and
	// writes unless ContextTimeoutEnabled is set; it would then wait ReadTimeout (5 s) per try, three
	// retries, so a Redis that accepts connections but never answers would stall every request.
	// URL query parameters (dial_timeout, read_timeout, max_retries, ...) still win.
	ropts.ContextTimeoutEnabled = true
	if ropts.DialTimeout == 0 {
		ropts.DialTimeout = 2 * time.Second
	}
	if ropts.DialerRetries == 0 {
		ropts.DialerRetries = 2
	}
	if ropts.ReadTimeout == 0 { // also bounds calls made without a deadline
		ropts.ReadTimeout = time.Second
	}
	if ropts.WriteTimeout == 0 {
		ropts.WriteTimeout = time.Second
	}
	if ropts.PoolTimeout == 0 {
		ropts.PoolTimeout = time.Second
	}
	if ropts.MaxRetries == 0 {
		ropts.MaxRetries = 1
	}
	rdb := redis.NewClient(ropts)
	Guard(rdb)
	return rdb, ks, nil
}

// RouteDriverLogs sends go-redis's own log lines (pool dial failures, reconnects), which otherwise go
// to stderr as plain text, through l as warnings with component "redis". It is process-wide: the
// process wiring calls it once with its redacting logger; tests pass zerolog.Nop().
func RouteDriverLogs(l zerolog.Logger) { redis.SetLogger(driverLogger{l: l}) }

type driverLogger struct{ l zerolog.Logger }

func (d driverLogger) Printf(_ context.Context, format string, v ...any) {
	d.l.Warn().Str("component", "redis").Msg(fmt.Sprintf(format, v...))
}

// ErrMoneyPath is returned for every Redis command issued under a MoneyPath context.
var ErrMoneyPath = errors.New("cache: a pricing, period-lock, invoice-numbering or payroll path never reads Redis (R17)")

type moneyPathKey struct{}

// MoneyPath marks ctx as a pricing, period-lock, invoice-numbering or payroll path (R17, R53,
// Appendix B §B.6.1). Such a path reads rate tables, hub codes, period locks and counters from
// PostgreSQL inside its transaction; under the mark every Redis command of a guarded client fails
// with ErrMoneyPath before it leaves the process, and the read-through caches refuse to serve. A
// stray cache read on a money path is therefore a loud error in tests and never a silent stale
// price, and the path behaves the same whether Redis is up or stopped.
func MoneyPath(ctx context.Context) context.Context {
	return context.WithValue(ctx, moneyPathKey{}, true)
}

// IsMoneyPath reports whether ctx carries the MoneyPath mark.
func IsMoneyPath(ctx context.Context) bool {
	v, _ := ctx.Value(moneyPathKey{}).(bool)
	return v
}

// Guard installs the money-path guard on a client (Open does it; clients built elsewhere call it).
func Guard(rdb redis.UniversalClient) { rdb.AddHook(moneyGuard{}) }

type moneyGuard struct{}

func (moneyGuard) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if IsMoneyPath(ctx) {
			return nil, ErrMoneyPath
		}
		return next(ctx, network, addr)
	}
}

func (moneyGuard) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if IsMoneyPath(ctx) {
			cmd.SetErr(ErrMoneyPath)
			return ErrMoneyPath
		}
		return next(ctx, cmd)
	}
}

func (moneyGuard) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if IsMoneyPath(ctx) {
			for _, c := range cmds {
				c.SetErr(ErrMoneyPath)
			}
			return ErrMoneyPath
		}
		return next(ctx, cmds)
	}
}
