package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/google"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/health"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/sse"
)

// APIDeps are the services BuildAPI wires and cmd/api's newAPI turns into route groups. `api routes`
// passes the zero value: Groups only registers handlers and never reads a service.
type APIDeps struct {
	Auth   *auth.Service
	Jobs   *jobs.Service
	Events *sse.Service
}

// EventGroups are GET /v1/events (internal, behind auth.RequireAuth) and GET /v1/mobile/events (public,
// SSE ticket) of T12 (main spec §8, Appendix B §B.2.20, §B.2.21).
func EventGroups(d APIDeps) []ingress.Group {
	return d.Events.Groups(d.Auth.RequireAuth())
}

// SSELease is how long an SSE stream holds its slot in rl:sse_conns:{userId} without a renewal: three
// heartbeats, so one late ping does not free it while a crashed replica's slots free themselves.
func SSELease(ping time.Duration) time.Duration { return 3 * ping }

// JobGroups are GET /v1/jobs, GET /v1/jobs/{id} and POST /v1/admin/queues/{queue}/replay (T10) behind
// auth.RequireAuth: owners read their jobs, platform_admin and support read all, platform_admin
// replays (Appendix B §B.2.20).
func JobGroups(d APIDeps) []ingress.Group {
	return d.Jobs.Groups(jobs.HTTPOptions{
		Auth: d.Auth.RequireAuth(),
		Caller: func(c fiber.Ctx) (jobs.Caller, bool) {
			p := auth.PrincipalFrom(c)
			if p == nil {
				return jobs.Caller{}, false
			}
			return jobs.Caller{UserID: p.UserID, PlatformAdmin: p.HasPlatform(authz.PlatformAdmin),
				PlatformSupport: p.HasPlatform(authz.Support)}, true
		},
	})
}

// BuildAPI wires the api process: the logitrack_app pool (DATABASE_URL), Redis, the rate limiter, the
// JWT key set, the auth and jobs services, and the readiness checks for PostgreSQL and Redis. build
// turns the services into the API: cmd/api passes its newAPI, the single place where route groups meet
// the listeners, so `api routes` lists and checks the table that is served. Connections are lazy: the
// process starts while a dependency is still coming up and /readyz reports it. A key file that is
// unreadable or does not match JWT_ACTIVE_KID is a *config.Error (exit 2). The returned close function
// releases the connections after Serve returns.
func BuildAPI(ctx context.Context, cfg *APIConfig, log zerolog.Logger, build func(APIDeps) (*API, error)) (*API, func(), error) {
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
	rdb, ks, err := cache.Open(cache.Options{URL: cfg.RedisURL, Prefix: cfg.RedisKeyPrefix, AppEnv: cfg.AppEnv, TLS: cfg.RedisTLS})
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
	store := auth.NewStore(rdb, ks.Prefix())
	// The process's one rate limiter (Appendix B §B.6.3): the auth buckets run through it, and so will
	// the middleware rules of later route groups, so a bucket and subject spend one budget everywhere.
	limiter := ratelimit.New(rdb, ks, log)
	// The cache: layer (T09) and the per-request authorization (T07): every authenticated request is
	// completed by iam.RBAC (X-Act-On-Tenant, steward flag, contractor reach, capabilities from the
	// catalog and rbac:caps), which GET /v1/me also reads.
	caches := cache.New(rdb, ks, cache.WithLogger(log))
	rbac, err := iam.NewRBAC(iam.Deps{Pool: pool, Cache: caches, Redis: rdb, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}

	deps := auth.Deps{Pool: pool, Store: store, Limiter: limiter, Keys: keys, Hasher: hasher, Policy: policy, Log: log,
		Capabilities: rbac, Authorizer: rbac}
	if len(cfg.GoogleClientIDs) > 0 {
		// No I/O here: discovery and keys are fetched on the first Google sign-in, so the api starts
		// while Google is unreachable (Appendix C §C.4.10).
		gv, err := google.New(google.Config{ClientIDs: cfg.GoogleClientIDs})
		if err != nil {
			closeAll()
			return nil, nil, &config.Error{Invalid: []string{"GOOGLE_OIDC_ALLOWED_CLIENT_IDS: " + err.Error()}}
		}
		deps.Google = gv
	} else {
		log.Info().Msg("Google sign-in off: GOOGLE_OIDC_ALLOWED_CLIENT_IDS is unset (/v1/auth/google* answer 404)")
	}
	if deps.Firebase, err = buildFirebase(cfg.Bridge, log); err != nil {
		closeAll()
		return nil, nil, err
	}
	svc, err := auth.New(auth.Config{
		RefreshTTLWeb: cfg.RefreshTTLWeb, RefreshTTLMobile: cfg.RefreshTTLMobile, PasswordResetTTL: cfg.PasswordResetTTL,
		Scrypt: cfg.Scrypt, RateLimitEnabled: cfg.RateLimit.Enabled, LoginIP: cfg.RateLimit.Limit(ratelimit.LoginIP),
	}, deps)
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
	// Realtime (T12): one PSUBSCRIBE rt:* loop per replica feeds the local SSE streams; the replay reads
	// the rtlog: streams the scheduler's relay writes, with the same RTLOG_TTL.
	hub := realtime.NewHub(rdb, ks, log, 0)
	events, err := sse.New(sse.Config{
		PingInterval: cfg.SSEPingInterval, MaxConnPerUser: cfg.SSEMaxConnPerUser, MobileEnabled: cfg.MobileSSEEnabled,
	}, sse.Deps{
		Hub: hub, Reader: realtime.NewReader(rdb, ks, cfg.RTLogTTL),
		Conns:   ratelimit.NewConnLimiter(rdb, ks, cfg.SSEMaxConnPerUser, SSELease(cfg.SSEPingInterval)),
		Tickets: svc, Sessions: svc, Pool: pool, Log: log,
	})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	a, err := build(APIDeps{Auth: svc, Jobs: jobs.NewService(pool, jobs.NewRedisLocker(rdb, ks)), Events: events})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	a.OnServe(func(ctx context.Context) {
		if err := hub.Run(ctx); err != nil {
			log.Error().Err(err).Msg("realtime fan-out stopped")
		}
	})
	a.OnDrain(events.Drain)
	for name, c := range map[string]interface {
		Register(prometheus.Registerer) error
	}{"realtime": hub, "sse": events} {
		if err := c.Register(a.metrics.Registry); err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("register %s metrics: %w", name, err)
		}
	}
	if err := svc.Register(a.metrics.Registry); err != nil {
		closeAll()
		return nil, nil, fmt.Errorf("register auth metrics: %w", err)
	}
	if err := limiter.Register(a.metrics.Registry); err != nil {
		closeAll()
		return nil, nil, fmt.Errorf("register rate-limit metrics: %w", err)
	}
	if err := caches.Register(a.metrics.Registry); err != nil {
		closeAll()
		return nil, nil, fmt.Errorf("register cache metrics: %w", err)
	}
	a.Health.Register(
		checker{"postgres", pool.Ping},
		checker{"redis", store.Ping},
	)
	return a, closeAll, nil
}

// buildFirebase builds the bridge's collaborators (Appendix C §C.6) without I/O beyond reading the key
// file: Google's keys and tokens are fetched on first use, so the api starts while Google is
// unreachable. An unreadable or malformed key file is a *config.Error (exit 2) naming the variable only.
func buildFirebase(b Bridge, log zerolog.Logger) (auth.Firebase, error) {
	fb := auth.Firebase{Mode: b.Mode}
	if b.FirebaseProjectID != "" {
		v, err := firebase.NewVerifier(firebase.VerifierConfig{ProjectID: b.FirebaseProjectID})
		if err != nil {
			return fb, &config.Error{Invalid: []string{"FIREBASE_PROJECT_ID: " + err.Error()}}
		}
		fb.Verifier = v
	}
	if b.Mode != auth.BridgeOff {
		sa, err := firebase.LoadServiceAccount(b.GoogleCredentials)
		if err != nil {
			return fb, &config.Error{Invalid: []string{err.Error()}}
		}
		if sa.ProjectID() != "" && sa.ProjectID() != b.FirebaseProjectID {
			// Firebase accepts custom tokens only from a service account of its own project.
			log.Warn().Msg("Firebase bridge: the GOOGLE_APPLICATION_CREDENTIALS service account belongs to another project than FIREBASE_PROJECT_ID")
		}
		acc, err := firebase.NewAccounts(firebase.AccountsConfig{ProjectID: b.FirebaseProjectID, Credentials: sa})
		if err != nil {
			return fb, &config.Error{Invalid: []string{"GOOGLE_APPLICATION_CREDENTIALS: " + err.Error()}}
		}
		fb.Signer, fb.Accounts = sa, acc
	}
	log.Info().Bool("web_custom_tokens", b.Mode.Web()).Bool("apk_firebase_tokens", b.Mode.Mobile()).
		Bool("account_mirror", b.Mode.Mirror()).Bool("firebase_id_tokens", fb.Verifier != nil).Msg("Firebase bridge")
	return fb, nil
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
