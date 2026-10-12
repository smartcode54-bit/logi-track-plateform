package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/dashboard"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/drivers"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/locations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/forms"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/parties"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/health"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/sse"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// APIDeps are the services BuildAPI wires and cmd/api's newAPI turns into route groups. `api routes`
// passes the zero value: Groups only registers handlers and never reads a service.
type APIDeps struct {
	Auth *auth.Service
	// Admin is the users and tenants administration (T19): /v1/users*, /v1/tenants*, and the carrier profiles
	// /v1/subcontractors* (T20).
	Admin *iam.Admin
	// APIKeys is the API keys administration (T32 slice T32K): /v1/api-keys*.
	APIKeys *iam.APIKeys
	// Parties are the customers and companies of T20: /v1/customers*, /v1/companies*.
	Parties *parties.Service
	// Hubs are the hubs, distances and the distances.compute submission of T21: /v1/hubs*, /v1/distances*,
	// POST /v1/jobs/distances.compute.
	Hubs *hubs.Service
	// Fleet is the truck master of T22: /v1/trucks* (renewals, import), and the truck assignments of T23:
	// /v1/truck-assignments*.
	Fleet *fleet.Service
	// Drivers are the driver master of T23: /v1/drivers*.
	Drivers *drivers.Service
	// Expenses are the vehicle expenses of T40: /v1/expenses* (server filter, audit with the PM check, toll import).
	Expenses *expenses.Service
	// Maintenance is the maintenance records of T40: /v1/maintenance* and POST /v1/trucks/{id}/pm-check.
	Maintenance *maintenance.Service
	// Billing is the billing data layer of T37: /v1/billing/{rate-entries,fuel-adjustments,service-fees,standby-rates,
	// statements}*, /v1/fuel/*, POST /v1/jobs/bangchak.snapshot.
	Billing *billing.Service
	// Operations are the operations routes: PATCH /v1/tasks/{id} with tenantId, the R13 reassignment (T28); the read
	// APIs /v1/tasks*, /v1/trips* (the monitor too), /v1/standby, /v1/incidents (T30); the write-back (T31) joins it.
	Operations *operations.Service
	// HR is driver compensation and payroll (T44): /v1/payroll* (runs, approve, status, config versions),
	// /v1/penalties*, POST /v1/jobs/payroll.run.
	HR      *hr.Service
	Jobs    *jobs.Service
	Storage *storage.Service
	Events  *sse.Service
	// Mobile is the driver app's installations, version gate and releases (T52): /v1/mobile/settings,
	// /v1/mobile/heartbeat, /v1/app-installations*, /v1/app-releases*.
	Mobile *mobileapp.Service
	// Locations are the truck positions of T53: GET /v1/vehicle-locations, GET /v1/admin/cartrack/probe.
	Locations *locations.Service
	// Dashboard is the W8 aggregates of T53: GET /v1/dashboard/summary, GET /v1/badges.
	Dashboard *dashboard.Service
	// Forms are the anonymous landing forms of T53 and the waitlist: /v1/waitlist*, POST /v1/partner-interest.
	Forms *forms.Service
	// Chats are the admin-driver chat of T47: /v1/chats* (staff) and /v1/mobile/chat* (the driver's own chat).
	Chats *chats.Service
	// Limiter and RateLimitEnabled feed the rate-limit rules of route groups (presign_user, T11).
	Limiter          *ratelimit.Limiter
	RateLimitEnabled bool
	// PublicFormsLimit is RATE_LIMIT_PUBLIC_FORMS (the public_form_ip bucket of the forms, T53); zero = the default.
	PublicFormsLimit ratelimit.Limit
}

// EventGroups is GET /v1/events (internal, behind auth.RequireAuth) of T12 (main spec §8, Appendix B §B.2.20). GET
// /v1/mobile/events (public, SSE ticket) is mounted by MobileGroups, the one /v1/mobile group.
func EventGroups(d APIDeps) []ingress.Group {
	var out []ingress.Group
	for _, g := range d.Events.Groups(d.Auth.RequireAuth()) {
		if g.Prefix != MobilePrefix {
			out = append(out, g)
		}
	}
	return out
}

// SSELease is how long an SSE stream holds its slot in rl:sse_conns:{userId} without a renewal: three
// heartbeats, so one late ping does not free it while a crashed replica's slots free themselves.
func SSELease(ping time.Duration) time.Duration { return 3 * ping }

// JobGroups are GET /v1/jobs, GET /v1/jobs/{id} and POST /v1/admin/queues/{queue}/replay (T10) behind
// auth.RequireAuth: owners read their jobs, platform_admin and support read all, staff of a job's tenant
// read the jobs whose type's capability they hold (jobs.TypeCapabilities, T21), platform_admin replays
// (Appendix B §B.2.20).
func JobGroups(d APIDeps) []ingress.Group {
	return d.Jobs.Groups(jobs.HTTPOptions{
		Auth: d.Auth.RequireAuth(),
		Caller: func(c fiber.Ctx) (jobs.Caller, bool) {
			p := auth.PrincipalFrom(c)
			if p == nil {
				return jobs.Caller{}, false
			}
			return jobs.Caller{UserID: p.UserID, PlatformAdmin: p.HasPlatform(authz.PlatformAdmin),
				PlatformSupport: p.HasPlatform(authz.Support), TenantID: p.EffectiveTenant(), Can: p.Can}, true
		},
	})
}

// BuildAPI wires the api process: the logitrack_app pool (DATABASE_URL), Redis, the rate limiter, the
// JWT key set, the storage, auth and jobs services, and the readiness checks for PostgreSQL, Redis and the
// active storage backend. The S3 buckets are re-asserted in the background (main spec §9.1). build
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
	st, s3, err := newStorage(storageBuild{Storage: cfg.Storage, API: &cfg.StorageAPI}, pool, log)
	if err != nil {
		pool.Close()
		_ = rdb.Close()
		return nil, nil, err
	}
	closeAll := func() {
		pool.Close()
		st.Close()
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
		Capabilities: rbac, Authorizer: rbac, Files: st}
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
		APIKeyPepper: cfg.APIKeyPepper,
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
	admin, err := iam.NewAdmin(iam.AdminDeps{Pool: pool, Auth: svc, Files: st, RBAC: rbac, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	if !cfg.APIKeyPepper.Set() {
		log.Info().Msg("API keys off: API_KEY_PEPPER is unset (POST /v1/api-keys answers 503, X-Api-Key 401)")
	}
	apiKeys, err := iam.NewAPIKeys(iam.APIKeysDeps{Pool: pool, Pepper: cfg.APIKeyPepper, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// Customers and companies (T20): RLS decides (db.WithPrincipal); customer writes drop cache:customer:{id}.
	partySvc, err := parties.New(parties.Deps{Pool: pool, Files: st, Cache: caches, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	jobSvc := jobs.NewService(pool, jobs.NewRedisLocker(rdb, ks))
	// Hubs and distances (T21): writes in db.WithPrincipal, the shared hub caches loaded read only, hub writes drop
	// cache:hubs:*; POST /v1/jobs/distances.compute submits through the jobs service (the worker runs it).
	hubSvc, err := hubs.New(hubs.Deps{Pool: pool, Cache: caches, Jobs: jobSvc, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// Trucks, renewals and the truck import (T22): RLS decides (db.WithPrincipal).
	fleetSvc, err := fleet.New(fleet.Deps{Pool: pool, Files: st, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// Drivers (T23): RLS decides (db.WithPrincipal); a tenant move is the administration's (iam.Admin.MoveDriver).
	driverSvc, err := drivers.New(drivers.Deps{Pool: pool, Files: st, Mover: admin, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// Maintenance and vehicle expenses (T40): RLS decides (db.WithPrincipal); an approval runs the PM check in its
	// transaction. Staff writes go to PostgreSQL, as a PostgreSQL-owned domain and a deployment without the FS->PG
	// mirror need; the class C write-back (Firestore first, main spec §11.10) lands with T31 (internal/writeback).
	maintSvc, err := maintenance.New(maintenance.Deps{Pool: pool, Files: st, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	expenseSvc, err := expenses.New(expenses.Deps{Pool: pool, Files: st, PM: maintSvc, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// Billing (T37): rate tables, statements and invoice numbers, fuel snapshots, under RLS (db.WithPrincipal); the
	// rate-card bundle and the Bangchak retail prices through the cache: layer (UI only); POST
	// /v1/jobs/bangchak.snapshot submits through the jobs service (the worker runs it). T39: the read aggregates, and
	// the documents the worker renders, downloaded through signed URLs of the storage service.
	feed, err := cfg.BangchakClient()
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	if feed == nil {
		log.Info().Msg("Bangchak prices off: BANGCHAK_API_BASE_URL is unset (GET /v1/fuel/retail answers 503)")
	}
	// T38: no WriteBack yet. The job-category and standby-customer endpoints write their class C fields (the task's
	// jobCategory, the standby customerId) to PostgreSQL only, which a deployment without the FS->PG mirror (the staging
	// VM) needs; T31 wires the Firestore adapter of internal/writeback here, a precondition of billing in
	// PG_OWNED_DOMAINS on a deployment that runs the mirror (main spec §6.13 as built).
	billingSvc, err := billing.New(billing.Deps{Pool: pool, Cache: caches, Jobs: jobSvc, Bangchak: feed, Files: st, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// Operations (T27 data layer; T28 the R13 task reassignment, performed by iam.TaskMover in a system transaction
	// after Go authorization; T30 the read APIs: photos signed by the storage service, places resolved against the hub
	// master of the hubs service).
	taskMover, err := iam.NewTaskMover(iam.TaskMoverDeps{Pool: pool, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// T31: the operations writes. PostgreSQL-owned (PG_OWNED_DOMAINS holds operations, the staging VM): the routes
	// write PostgreSQL under the caller's RLS. Otherwise they write the Firestore documents first (the class C
	// write-back, main spec §13.8) and wait up to 3 s for the mirror's cache:mirror_ack; without a Firestore project
	// they answer 503 write_back_unconfigured. A trip rename and its audit row run in iam's system transaction.
	tripRenamer, err := iam.NewTripRenamer(iam.TripRenamerDeps{Pool: pool, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	wbFS, err := writeBackFirestore(cfg)
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	wb := operationsWriteBack(cfg, wbFS, caches, log)
	opsSvc, err := operations.New(operations.Deps{Pool: pool, Log: log, Reassigner: taskMover, Files: st,
		Hubs: OperationsHubs{Service: hubSvc}, PGOwned: cfg.OperationsOwned, WriteBack: wb, Renamer: tripRenamer,
		Commits: st})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// Driver compensation and payroll (T44): configs, penalties and runs under RLS (db.WithPrincipal, never Redis on the
	// money path); POST /v1/jobs/payroll.run submits through the jobs service (the worker generates the runs).
	hrSvc, err := hr.New(hr.Deps{Pool: pool, Jobs: jobSvc, Files: st, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// The driver app's fleet (T52): installations under RLS, the version gate through cache:settings:mobile_app, the
	// APKs in the public bucket through the storage service.
	mobile, err := mobileapp.New(mobileapp.Deps{Pool: pool, Storage: st, Cache: caches, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// T53: the truck positions (the probe submits a cartrack.sync job the worker runs: the api holds no Cartrack
	// credential), the dashboard aggregates under RLS, and the anonymous forms (exempt tables, the service writes).
	locSvc, err := locations.New(locations.Deps{Pool: pool, Jobs: jobSvc, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	dashSvc, err := dashboard.New(dashboard.Deps{Pool: pool, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	formSvc, err := forms.New(forms.Deps{Pool: pool, Log: log})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	chatSvc, err := buildChats(cfg, pool, st, wbFS, caches, log)
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	a, err := build(APIDeps{Auth: svc, Admin: admin, APIKeys: apiKeys, Parties: partySvc, Hubs: hubSvc, Fleet: fleetSvc,
		Drivers: driverSvc, Expenses: expenseSvc, Maintenance: maintSvc, Billing: billingSvc, Operations: opsSvc, HR: hrSvc, Jobs: jobSvc,
		Storage: st,
		Events:  events, Mobile: mobile, Locations: locSvc, Dashboard: dashSvc, Forms: formSvc, Chats: chatSvc, Limiter: limiter,
		RateLimitEnabled: cfg.RateLimit.Enabled, PublicFormsLimit: cfg.RateLimit.Limit(ratelimit.PublicFormIP)})
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
		checker{"storage", st.Check},
	)
	bootstrapS3(ctx, s3, log)
	return a, closeAll, nil
}

// writeBackFirestore is the Firestore client of the class C write-back (T31, T47) while operations or comms is not
// PostgreSQL-owned; nil without WRITEBACK_FIRESTORE_PROJECT_ID (those writes then answer 503 write_back_unconfigured)
// or when both domains are PostgreSQL-owned. No I/O: the access token is fetched on the first write, so the api starts
// while Google is unreachable. An unreadable key file is a *config.Error naming the variable only.
func writeBackFirestore(cfg *APIConfig) (*gcp.Firestore, error) {
	if !cfg.writeBackNeeded() || cfg.WriteBack.FirestoreProjectID == "" {
		return nil, nil
	}
	sa, err := firebase.LoadServiceAccount(cfg.GoogleCredentials)
	if err != nil { // names the variable, never the content
		return nil, &config.Error{Invalid: []string{err.Error()}}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	fs, err := gcp.NewFirestore(cfg.WriteBack.FirestoreProjectID, cfg.WriteBack.FirestoreDatabaseID, client,
		sa.TokenSource(client, gcp.FirestoreScope))
	if err != nil {
		return nil, &config.Error{Invalid: []string{err.Error()}}
	}
	return fs, nil
}

// operationsWriteBack builds the Firestore writer of the operations writes while operations is not PostgreSQL-owned
// (T31): nil when operations is PostgreSQL-owned, or when no Firestore project is configured (fs nil: the writes then
// answer 503).
func operationsWriteBack(cfg *APIConfig, fs *gcp.Firestore, acks *cache.Cache, log zerolog.Logger) *operations.WriteBack {
	if cfg.OperationsOwned {
		log.Info().Msg("operations writes: PostgreSQL (operations is in PG_OWNED_DOMAINS)")
		return nil
	}
	if fs == nil {
		log.Info().Msg("operations writes off: operations is Firestore-owned and WRITEBACK_FIRESTORE_PROJECT_ID is unset " +
			"(the write routes answer 503 write_back_unconfigured)")
		return nil
	}
	log.Info().Msg("operations writes: Firestore write-back (operations is not in PG_OWNED_DOMAINS)")
	return &operations.WriteBack{Writer: writeback.Firestore{FS: fs}, Acks: acks, Wait: operations.DefaultMirrorWait,
		RunOrders: acks}
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
