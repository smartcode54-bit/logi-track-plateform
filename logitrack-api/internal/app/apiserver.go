package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/health"
)

// BuildAPI wires the api process: the logitrack_app pool (DATABASE_URL), Redis, the JWT key set, the
// auth service, and the readiness checks for PostgreSQL and Redis. build turns the auth service into
// the API: cmd/api passes its newAPI, the single place where route groups meet the listeners, so
// `api routes` lists and checks the table that is served. Connections are lazy: the process starts
// while a dependency is still coming up and /readyz reports it. A key file that is unreadable or does
// not match JWT_ACTIVE_KID is a *config.Error (exit 2). The returned close function releases the
// connections after Serve returns.
func BuildAPI(ctx context.Context, cfg *APIConfig, log zerolog.Logger, build func(*auth.Service) (*API, error)) (*API, func(), error) {
	keys, err := token.Load(token.Config{
		SigningKeyFile: cfg.JWTSigningKeyFile, PreviousKeyFile: cfg.JWTPreviousKeyFile, ActiveKID: cfg.JWTActiveKID,
		Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience, TTL: cfg.JWTAccessTTL,
	})
	if err != nil {
		return nil, nil, &config.Error{Invalid: []string{err.Error()}}
	}
	hasher, err := password.NewHasher(password.Params{
		MemoryKB: cfg.Argon2MemoryKB, Iterations: cfg.Argon2Iterations, Parallelism: cfg.Argon2Parallelism,
	})
	if err != nil {
		return nil, nil, &config.Error{Invalid: []string{err.Error()}}
	}
	policy, err := password.NewPolicy(cfg.PasswordMinLength)
	if err != nil {
		return nil, nil, &config.Error{Invalid: []string{err.Error()}}
	}

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, "logitrack-api", db.Options{MaxConns: cfg.MaxConns, MinConns: cfg.MinConns})
	if err != nil {
		return nil, nil, &config.Error{Invalid: []string{"DATABASE_URL: " + err.Error()}}
	}
	// One Redis client for the process, built by cache.Open (T09): it honours context deadlines with
	// short socket timeouts, so an unreachable or hung Redis fails fast and the per-request revocation
	// check answers from PostgreSQL (Appendix C §C.4.3) instead of stalling every request, and it
	// carries the money-path guard (Appendix B §B.6.1). URL query parameters (dial_timeout, ...) still
	// win. go-redis's own log lines go through the redacting logger.
	cache.RouteDriverLogs(log)
	rdb, _, err := cache.Open(cache.Options{URL: cfg.RedisURL, Prefix: cfg.RedisKeyPrefix, AppEnv: cfg.AppEnv, TLS: cfg.RedisTLS})
	if err != nil { // never carries the URL
		pool.Close()
		return nil, nil, &config.Error{Invalid: []string{err.Error()}}
	}
	closeAll := func() {
		pool.Close()
		if err := rdb.Close(); err != nil {
			log.Warn().Err(err).Msg("redis close")
		}
	}
	store := auth.NewStore(rdb, cfg.RedisKeyPrefix)

	svc, err := auth.New(auth.Config{
		RefreshTTLWeb: cfg.RefreshTTLWeb, RefreshTTLMobile: cfg.RefreshTTLMobile, PasswordResetTTL: cfg.PasswordResetTTL,
		Scrypt: cfg.Scrypt, RateLimitEnabled: cfg.RateLimitEnabled, LoginIP: cfg.LoginRate,
	}, auth.Deps{Pool: pool, Store: store, Keys: keys, Hasher: hasher, Policy: policy, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// The post-commit retries of auth write to Redis: stop them before the client closes.
	closeConns := closeAll
	closeAll = func() {
		svc.Close()
		closeConns()
	}
	a, err := build(svc)
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	if err := svc.Register(a.metrics.Registry); err != nil {
		closeAll()
		return nil, nil, fmt.Errorf("register auth metrics: %w", err)
	}
	a.Health.Register(
		checker{"postgres", pool.Ping},
		checker{"redis", store.Ping},
	)
	return a, closeAll, nil
}

// checker adapts a ping function to health.Checker.
type checker struct {
	name string
	ping func(context.Context) error
}

func (c checker) Name() string { return c.name }

// Check never returns the driver error text, which may name hosts: readiness details stay generic.
func (c checker) Check(ctx context.Context) error {
	if err := c.ping(ctx); err != nil {
		return errors.New("unreachable")
	}
	return nil
}

var _ health.Checker = checker{}
