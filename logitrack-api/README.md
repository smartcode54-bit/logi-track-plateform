# logitrack-api

Go backend for the LogiTrack migration off Firebase (`mv-go`). Design: [`developer-spec.md`](../developer-spec.md) §2, routes in [Appendix B](../shared-docs/specs/mv-go/B-api-catalog.md). Branch policy: work lands by PR into `mv-go`, never `main` (R90).

Status: **T01 scaffold + T02 local stack + T03 migrations + T04 core schema + T36 billing engine + TW2 edge (web container + Caddy) + T14 CI**. One module, seven binaries, shared `internal/`; no domain routes yet.

## Layout

```
cmd/api          two HTTP listeners (internal + public) and /metrics
cmd/worker       background consumers (T10); today: /metrics + wait for SIGTERM
cmd/scheduler    cron + outbox relay (T10); today: /metrics + wait for SIGTERM
cmd/migrate      goose chain embedded from migrations/, run as logitrack_migrator (T03)
cmd/seed         seed profiles (T16)           — exits 3 until implemented
cmd/etl          Firestore/GCS → PG/MinIO (T15) — exits 3 until implemented
cmd/release      APK publish CLI (T52)         — exits 3 until implemented
internal/app                 process wiring, configs, API server, graceful shutdown
internal/platform/config     env loading: all missing/invalid names in one error, never values
internal/platform/logx       zerolog + redacting writer (authorization, password, *token, cookie, idCard, ...)
internal/platform/httpx      envelopes, error codes, request id, client IP, access log
internal/platform/ingress    route groups and the public allow-list
internal/platform/health     /healthz, /readyz, /startupz, drain state
internal/platform/telemetry  OpenTelemetry (OTLP/HTTP) and Prometheus
internal/platform/db         pgx pools per role (R66); dbq = sqlc output; pgtest = postgres:18-alpine for tests
internal/platform/migrate    migration rules (R31), goose runner with a session lock; migratetest = round trip
internal/platform/clock      Bangkok (+07:00) calendar: dates, days, months, Bangkok midnight; never the wall clock
internal/platform/jsmath     JavaScript number semantics money code needs: Math.round, Round2, toFixed
internal/platform/cache      Redis layer (T09): client, lt:{APP_ENV}: keyspace, read-through caches, invalidation, money-path guard; cachetest = redis:7-alpine for tests
internal/platform/httpx/idempotency  Idempotency-Key middleware: Redis hot copy + idempotency_keys durable claim (R53)
internal/platform/httpx/ratelimit    GCRA buckets of Appendix B §B.6.3 + Fiber middleware (429 resource_exhausted, Retry-After)
internal/billing/compute     the billing engine (T36): pure port of lib/billingCompute.ts + the pure pricing rules
internal/billing/documents   pure invoice layout rules: axis date, price rounds, line items (renderers: T39)
internal/golden              test-only runner for testdata/golden vectors
migrations/                  NNNN_name.sql, embedded into cmd/migrate: the Appendix A baseline 0001-0010 (T03, T04)
api/routes.txt               generated route table (method, path, listeners) checked by go-ci gen-check (T14)
sqlc.yaml                    sqlc v1.31.1: schema = migrations/, one block per query package
testdata/golden/             language-neutral golden vectors exported from the TypeScript engines (main spec §6.16)
```

## Listeners (main spec §2.6)

| Listener | Env | Serves |
|---|---|---|
| internal | `API_INTERNAL_ADDR` | every route group, incl. `/readyz`, `/startupz` |
| public | `API_PUBLIC_ADDR` | only groups marked public **and** listed in `PUBLIC_ROUTE_GROUPS` (subset of `/v1/mobile`, `/v1/auth`, `/public/v1`, `/evidence`, `/healthz`); everything else `404 not_found` |
| metrics | `METRICS_ADDR` | `GET /metrics` (Prometheus), private |

`api routes` builds the API through the same `newAPI` as serving (`cmd/api/main.go`; domain groups are added there), refuses a group marked public outside the list and any public route other than `/healthz` or a path below `/v1/mobile/`, `/v1/auth/`, `/public/v1/`, `/evidence/` (exit 1; serving refuses the same at startup), and prints one line per route with the listeners that may serve it. A middleware or sub-app registered with `Use` is listed as `USE PATH` and matches every path below it, so on the public listener it is allowed only at or below `/v1/mobile`, `/v1/auth`, `/public/v1` or `/evidence`, never at or below `/healthz`. The listener-wide middleware (`Use` at `/`, `newFiber`) is not a route and no route check sees what it serves: never add a path-dispatching one (pprof, expvar, static files, a proxy); `TestRootMiddlewareIsPinned` pins its size. `go generate` writes it to `api/routes.txt`, so a route or listener change shows up in review and `make gen-check` fails when the table is stale.

`X-Forwarded-For` is honoured only when the TCP peer is inside `TRUSTED_PROXY_CIDRS`; the header is walked right to left and the first untrusted hop is the client. `X-Act-On-Tenant` is refused on the public listener (`400 header_not_allowed`).

Errors are always `{"error":{"code","message","details","requestId"}}` and `requestId` equals the `X-Request-Id` response header. Success bodies are `{"data": ...}`. JSON is camelCase.

## Environment (names only; values live in the secret store, main spec §16)

| Name | Processes | Required | Default |
|---|---|---|---|
| `APP_ENV` | all | yes | — (`local`, `dev`, `prod`) |
| `LOG_LEVEL`, `LOG_FORMAT` | all | no | `info`, `json` (`console` for local) |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `OTEL_TRACES_SAMPLER_ARG` | all | no | export off, `logitrack-<process>`, `1` |
| `METRICS_ADDR` | api, worker, scheduler | yes | — |
| `SHUTDOWN_TIMEOUT` | api, worker, scheduler | no | `30s` |
| `API_INTERNAL_ADDR`, `API_PUBLIC_ADDR` | api | yes | — |
| `PUBLIC_ROUTE_GROUPS` | api | no | all five public groups |
| `TRUSTED_PROXY_CIDRS` | api | no | none (no `X-Forwarded-For` trusted) |

A missing or invalid variable stops the process with exit code 2 and a message naming every offending variable (never its value). Startup logs list each variable as `set`/`unset`.

## Lifecycle

`SIGTERM` → `/readyz` answers `503 unavailable` (`details.reason=draining`) → after a grace of `min(5s, SHUTDOWN_TIMEOUT/3)` both listeners stop accepting and in-flight requests drain within the rest of `SHUTDOWN_TIMEOUT` → exit 0. `/healthz` stays 200 until the listener closes.

Exit codes: `0` ok, `1` runtime error, `2` configuration error, `3` not implemented yet.

## Local stack (T02, main spec §15)

```bash
make env
```

```bash
make dev-keys
```

```bash
make up
```

```bash
make smoke
```

- `make env` writes `.env` (mode 0600, gitignored) from `.env.example`: secret names get random local-only values and the three DB URLs are composed for the R66 roles. Integration credentials stay blank, so FCM, LINE, Cartrack, Google and the SMTP relay are off locally (mail goes to mailpit).
- `make dev-keys` writes `deploy/dev-secrets/jwt-ed25519.pem` (gitignored) and sets `JWT_ACTIVE_KID` (RFC 7638 thumbprint).
- `make up` starts PostgreSQL 18, Redis 7, RabbitMQ 4 (+ definitions from `internal/platform/mq`), MinIO (+ buckets), mailpit, sets the role passwords (`make dev-db`), runs `migrate up`, then starts api, worker and scheduler (they wait for migrate to exit 0) and waits until all are healthy. Profiles: `tools` (seed, etl), `mocks` (WireMock), `obs` (Jaeger), `tunnel`; `EDGE=1` adds web and Caddy (see Edge below).
- `make smoke` checks the T02 and T03 acceptance criteria against the running stack; `make smoke EDGE=1` adds the TW2 edge checks.

| Service | Host port (127.0.0.1) | Notes |
|---|---|---|
| postgres | 5432 | PGDATA `/var/lib/postgresql/18/docker`, volume at `/var/lib/postgresql`; roles from `deploy/postgres-init/00-roles.sql`; `logitrack_test` for integration tests |
| redis | 6379 | AOF, `noeviction` |
| rabbitmq | 5672, 15672 | 5 exchanges, 16 work queues, 16 `.dead` queues, 5 retry queues |
| minio | 9000, 9001 | `S3_BUCKET` private; `S3_PUBLIC_BUCKET` anonymous GET on `app_releases/` only; app user limited to both buckets |
| mailpit | 8025, 1025 | SMTP sink |
| api | 8080, 8081 | internal and public listeners (local only; Caddy fronts the public one) |
| web (`EDGE=1`) | 3001 | Next.js standalone, for debugging; browsers use Caddy |
| caddy (`EDGE=1`) | 80, 443 (all interfaces) | `WEB_DOMAIN`, `API_PUBLIC_DOMAIN`, `MEDIA_DOMAIN` |

Each Go service receives exactly the §16.1 names whose consumer column lists it: api, worker and scheduler see only `DATABASE_URL`; seed sees all three DB URLs. `make env-check` verifies `.env.example` and the compose environments against `developer-spec.md` §16; `make secrets-scan` runs gitleaks over the files git would commit.

MinIO: the official `minio/minio` and `minio/mc` images are no longer published, so compose uses Chainguard's source builds (`cgr.dev/chainguard/minio`, `cgr.dev/chainguard/minio-client:latest-dev`) pinned by digest. Any S3-compatible server can replace it later; only the `S3_*` values change.

## Migrations (T03, main spec §3.5)

goose v3.28 runs the files of `migrations/` (embedded into the binary, so the `make migrate*` targets rebuild the image first) as `logitrack_migrator` through `MIGRATE_DATABASE_URL`; `migrate` refuses any other login, and `0001_preamble` repeats the check inside the database, where a refusal rolls the whole file back (R66). A session advisory lock serialises concurrent runs. goose creates `goose_db_version` before the first migration, owned by the login that runs it: if goose was ever run by hand with another login, drop that table before `make migrate`.

| Command | What it does |
|---|---|
| `make migrate` | `migrate up`: every pending migration (local, CI and seeded databases) |
| `make migrate-up-to V=9` | stop at a version; production stays at `up-to 9` until the P1 runbook applies `0010_d5_unique_constraints` (R59, R88) |
| `make migrate-status` | every migration with `applied` / `pending`; `migrate status -fail-on-pending` exits 1 |
| `make migrate-down` | one step back; `down` and `down-to` are refused when `APP_ENV=prod` |
| `make migrate-new NAME=add_x` | writes the next `NNNN_add_x.sql` on the host |
| `make migrate-check` | the R31 rules, no database |
| `make migrate-roundtrip` | up → down-to floor → up on `postgres:18-alpine`, comparing `pg_dump --schema-only` |

Rules (`migrate check`, also part of `make gen-check`): files `NNNN_name.sql` numbered 1..N without gaps; every file has `-- +goose Up` and `-- +goose Down`; an empty Down only in a data migration whose header (before `-- +goose Up`) says `-- irreversible` (the round trip then rolls back only to the highest such version); `-- +goose NO TRANSACTION` exactly when the Up or Down builds or drops an index `CONCURRENTLY`; no empty `StatementBegin` block (goose would silently drop the next statement); ids default to `uuidv7()`, never `gen_random_uuid()`; no goose `ENVSUB`. Comments, strings and quoted identifiers are ignored when matching. Once a migration is applied to a database that is kept (from the P0 deploy on), it is never edited: follow-ups take the next number.

Baseline (T04): `0001`-`0009` are Appendix A §A.2.0-§A.2.8 with each file's Appendix C policy block (RLS on exactly the 70 tables of Appendix C §C.3.0, generators from 0001), `0009_infra` is the only file of the baseline that grants or revokes (R66) and hands the eight SECURITY DEFINER functions to `logitrack_rls_definer`; `0010_d5_unique_constraints` (`NO TRANSACTION`) builds the three D5 unique indexes `CONCURRENTLY` and stops with an INVALID-index error while a legacy duplicate survives (R88). Local, CI and seeded databases run `up`; production runs `up-to 9` until the T24 runbook. Schema acceptance tests: `migrations/schema_integration_test.go` (tables equal the `CREATE TABLE`s of Appendix A, uuidv7 ids, STORED generated columns, inline `tenant_source`, vocabularies, append-only and void-only rules as `logitrack_app`, the draft-statement cascade, allocators, the D5 guard), `migrations/rls_integration_test.go` (policy behaviour as principals: the tenant-bound driver maintenance gate, the `file_objects` commit guard) and `internal/platform/db/rls_catalog_test.go` (RLS flags, policy names, freeze triggers, and the effective `logitrack_app` table privileges from `has_table_privilege`, so `PUBLIC` and inherited grants count, on `public` and `etl` against Appendix C §C.3.0 and §C.3.2, read from the document; nothing granted to `PUBLIC`).

Integration tests (`-tags=integration`) use `internal/platform/db/pgtest`: one `postgres:18-alpine` container per test binary with `deploy/postgres-init/00-roles.sql` as an init script and a fresh database per test owned by `logitrack_migrator`. `make test-integration` points testcontainers at the active docker context (`DOCKER_HOST`).

sqlc: `make sqlc` regenerates; `make gen-check` runs `sqlc diff` and `sqlc vet`. `omit_unused_structs` keeps each package to the models of the tables its queries use. The first query (`internal/platform/db/queries/login.sql`) is the migrate preflight; domain tasks add one `sql` block per repo package.

## Billing engine (T36, main spec §6)

`internal/billing/compute` prices trips and standby events with no I/O and no clock: the caller loads rate cards, fuel rounds, fees, hub names and period locks in its pricing transaction and passes them in. Money stays float64 and matches V8 bit for bit: `jsmath.Round` rounds ties toward +Inf (and keeps `-0`), every product is wrapped in `float64(...)` so arm64 and amd64 `GOAMD64=v3` cannot fuse it into a multiply-add, and sums stay unrounded in input order. Deliberate differences from the TypeScript: a blank vehicle class is `no_vehicle_class` (R15), equal effective instants are ordered by legacy doc id, `created_at`, `id` whatever the load order (R16), a trip with no plan, delivery or creation instant is `no_billing_date` instead of `Date.now()` (R19), voided standby rates never price (R20). `IsFrozen` / `CarriesFuel` exist once in the module.

Golden vectors: `testdata/golden/billing` (see its README). `go test ./internal/billing/... ./internal/platform/...` checks them against Go; `pnpm test` in `logitrack-web` checks the same files against the TypeScript. Regenerate after a TypeScript change with `node testdata/golden/billing/export.mjs`.

## Edge: web + Caddy (TW2, main spec §10.12, §15)

`make up EDGE=1` adds two services (compose profile `edge`):

- `web`: `logitrack-web/Dockerfile` (repository-root context), Next.js `output: "standalone"` run as `node logitrack-web/server.js`, liveness `GET /api/healthz`. Runtime env is exactly the §16.1 `web-server` names; build arguments are `web-public` names only. Until TW7 (P6) the build needs the dev Firebase project's public web config (`NEXT_PUBLIC_FIREBASE_*`) in `.env`, and fails while it is empty.
- `caddy`: `deploy/Caddyfile`, pinned `caddy:2.11.7-alpine`.

| Site (local value) | Upstream | Notes |
|---|---|---|
| `WEB_DOMAIN` (`http://localhost`) | `web:3000` | compressed (zstd, gzip) except `/api/go/v1/events`, which streams unbuffered (`flush_interval -1`); Cache-Control comes from `next.config.ts` |
| `API_PUBLIC_DOMAIN` (`http://api.localhost`) | the api **public** listener (`api` + `API_PUBLIC_ADDR`, so that value is `:port`; the api refuses a host part when `APP_ENV` is `dev` or `prod`) | only `/v1/mobile`, `/v1/auth`, `/public/v1`, `/evidence`, `/healthz`; anything else `404 not_found` in the Go envelope. Never the internal listener |
| `MEDIA_DOMAIN` (`http://media.localhost`) | `minio:9000` | Host passes unchanged, so a URL signed for `S3_PRESIGN_ENDPOINT` = `https://{MEDIA_DOMAIN}` verifies; `/minio/*` is 404 |
| `http://:8090` (not published) | the api public listener | tunnel target: `/public/v1/*` only |

Locally the sites are `http://` (no TLS). In prod they are bare host names with ACME certificates; Caddy then refuses to start until `ACME_EMAIL` is set. `make smoke EDGE=1` runs `deploy/edge-smoke.sh` after the T02 and T03 checks (real id under `/app/customers/<id>`, cache headers, public-listener-only API site, a presigned URL through the media site; `go run ./tools/presign` signs it). `internal/platform/ingress` tests keep the Caddyfile allow-list equal to `ingress.PublicPrefixes`.

## Redis layer (T09, main spec §2.1, Appendix B §B.6)

Every key and channel is `lt:{APP_ENV}:{namespace}:...` with the eight namespaces `cache`, `auth`, `rbac`, `idem`, `rl`, `rt`, `rtlog`, `lock` (R26); keys come only from `cache.Keyspace` builders (one per row of Appendix B §B.6.2 and Appendix C §C.4.14). The server runs AOF with `maxmemory-policy noeviction` (compose and `cachetest`, which reads the image and flags from `deploy/docker-compose.yml`).

| Env | Default | Used by |
|---|---|---|
| `REDIS_URL`, `REDIS_KEY_PREFIX`, `REDIS_TLS` | prefix `lt:{APP_ENV}:` (anything else is refused), TLS off | `cache.Open` (lazy dial; context deadlines honoured; dial 2 s, read and write 1 s, pool wait 1 s, one retry, unless the URL's query sets them) |
| `CACHE_TTL_HUBS`, `CACHE_TTL_RATECARD`, `CACHE_TTL_SETTINGS` | `10m`, `1h`, `5m` | `cache.TTLs` |
| `IDEMPOTENCY_TTL` | `168h` | `idempotency.Config` |
| `RATE_LIMIT_ENABLED`, `RATE_LIMIT_LOGIN`, `RATE_LIMIT_PUBLIC_FORMS`, `RATE_LIMIT_EVIDENCE` | `true`, `10/1m`, `5/1h`, `60/1m` | `ratelimit.Config`: `login_ip`, `public_form_ip`, `evidence_ip`; other buckets are code constants |

`.env.example` ships exactly these defaults (`TestEnvExampleShipsTheDesignDefaults`). A local `.env` made from an earlier `.env.example` keeps the old values `CACHE_TTL_RATECARD=10m`, `CACHE_TTL_SETTINGS=1m`, `RATE_LIMIT_LOGIN=10/15m`, `RATE_LIMIT_PUBLIC_FORMS=5/1m` until you edit them or rerun `make env FORCE=1`.

Every Redis call is bounded: 300 ms in the caches, the rate limiter and the idempotency middleware, 1 s for an invalidation. A Redis that accepts connections but never answers therefore costs a request at most one such bound before the fallback (loader, fail open, PostgreSQL alone) answers (`TestHungRedis*`, against a listener that never replies).

- **Caches are UI hints.** `cache.GetJSON`, `HubMaps` (two hashes `cache:hubs:n2c` and `cache:hubs:c2n`, written and dropped together, never merged), `PeriodLocks`, `Subtenants` read through to a loader (PostgreSQL) and fall back to it when Redis fails. Writers call `Cache.Invalidate` / `Cache.OnEvent` after commit; the outbox relay (T10) calls `OnEvent` for every event; deletions go out on `rt:cache` so replicas running `Cache.Run` drop their `WithL1` copies. An event that does not name the id (for example `statement.*` without `billingPartyId`) drops every key of its family. Each invalidation first increments `cache:gen:{family}`, and a read-through stores what it loaded only while that counter is unchanged, so a reader that loaded before a commit never writes the old value back.
- **Money paths never read Redis** (R17). Pricing, period locks, invoice numbering and payroll run under `cache.MoneyPath(ctx)`: every command of a guarded client fails with `ErrMoneyPath` before it is sent, and the caches refuse to serve. `go list -deps` of the pure engines must not contain `go-redis` (`TestMoneyEnginesNeverLinkRedis`).
- **Idempotency** (`httpx/idempotency`): mount `Middleware.Handler()` after authentication on each `✱` route; 2xx answers replay byte for byte (`Idempotent-Replayed: true`) from Redis (24 h) or `idempotency_keys` (until `IDEMPOTENCY_TTL`), a different body is `409 idempotency_conflict`; `PGStore.Prune` is the `idempotency.prune` job. The route runs under a 25 s deadline (`HandlerBudget`, inside the 30 s lease): **a `✱` handler must do its database and storage work under `c.Context()`**, so a request that cannot finish is cancelled and rolled back before a retry could take its claim over.
- **Rate limits** (`httpx/ratelimit`): `Limiter.Middleware(enabled, rules...)`; a request is counted when it is checked (no peek, so parallel requests cannot all pass); subjects are stored as a sha256 prefix (pseudonymous, not anonymous); `ByIP` limits an IPv6 client by its /64; fail open (logged and counted) when Redis does not answer within 300 ms. A rule without a usable limit panics when the route is built, and `Config.Validate` refuses an unusable `RATE_LIMIT_*` value, so nothing fails open by mistake. `login_fail` is not a GCRA bucket: the sign-in lockout of Appendix C §C.4.12 lives in `internal/auth` (T05).
- **Mirror ack** (`SetMirrorAck`, `WaitMirrorAck`) for read-your-writes in P2-P7a; single-use tickets (`PutTicket`, `TakeTicket` with `GETDEL`) for `auth:pwchg`, `auth:sse`, `auth:google:nonce`.
- Wiring: the api process builds the Redis client in T05 (`BuildAPI`); routes adopt the middleware as they land (first `✱` routes: T31, T56). Process wiring calls `cache.RouteDriverLogs(log)` so go-redis log lines go through the redacting logger.

Tests: `go test ./internal/platform/cache/... ./internal/platform/httpx/...` (keyspace, events, L1, money-path guard with Redis stopped) and `make test-integration` (redis:7-alpine and postgres:18-alpine: read-through, invalidation over pub/sub, Redis stopped, GCRA, idempotency replay from Redis and from PostgreSQL, and a SCAN that every key is under the prefix and a listed namespace).

## Develop

Go `1.27` with `toolchain go1.27.2` (stdlib security fixes; the go command downloads it automatically).

```bash
go test -race ./...
```

```bash
make test-integration
```

```bash
make lint
```

```bash
make gen-check
```

`make lint` runs gofmt, `go mod tidy -diff`, `go vet`, golangci-lint v2.14.0 and govulncheck v1.8.0 (versions pinned in the Makefile only, gitleaks too: `.github/scripts/secret-scan.sh` reads it through `make print-gitleaks-img`); `make gen-check` the generated-code, route and migration gates.

Run the api locally (any free ports):

```bash
APP_ENV=local LOG_FORMAT=console API_INTERNAL_ADDR=127.0.0.1:8080 API_PUBLIC_ADDR=127.0.0.1:8081 METRICS_ADDR=127.0.0.1:9090 go run ./cmd/api
```

## CI (T14, main spec §17.2)

Three workflows run on pushes and pull requests of `mv-go` and `mv-go-**`. `go-ci` and `secret-scan` have no `main` trigger and deploy nothing; `CI` keeps its `main` entries, and Deploy still follows CI runs whose head branch is `main` (R90). A PR whose head branch is `main` is refused by CI's `refuse-main-head` job, so bring `main` into `mv-go` through a `mv-go-sync-<date>` branch (main spec §17.5). On `mv-go` each push gets its own concurrency group in all three, so no `mv-go` run is cancelled or dropped from the queue, and every `mv-go` push runs every go-ci job and pushes its image.

| Workflow | Jobs |
|---|---|
| `go-ci` (`.github/workflows/go-ci.yml`) | `changes` (skips the Go jobs when no checked path changed); `lint` (`make lint`); `gen-check` (`make gen-check`, `make env-check`, clean tree); `migrate` (`make migrate-check`, `make migrate-roundtrip`); `test` (`make test-integration TESTFLAGS=-count=1`); `goldens` (`make test` on amd64 with `GOAMD64=v3` and on arm64); `stack` (`make env dev-keys up smoke`, then seed smoke + verify once T16 lands); `etl-fixtures` (`make etl-fixtures-check` once T15 adds the fixtures); `build` (image with every binary, pushed to `ghcr.io/smartcode54-bit/logitrack-api:{sha}` only from `mv-go`, `:v{semver}` from an `api-v{semver}` tag on `mv-go`); `web-ci` (waits for the web `CI` run of the same commit, if its path filter started one); `go-ci` (sums them up) |
| `secret-scan` (`.github/workflows/secret-scan.yml`) | gitleaks v8.30.1 over the commits of the push or PR (`.github/scripts/secret-scan.sh BASE HEAD` runs it locally), findings redacted; merge commits are scanned against their first parent; no allow-list counts (a `.gitleaks.toml` or `.gitleaksignore` in the tree fails, in-repo gitleaks config and `gitleaks:allow` are ignored); a gitleaks error or skipped commits fail the scan. `.github/scripts/secret-scan-selftest.sh` proves this on throwaway repositories first |
| `CI` (`.github/workflows/ci.yml`) | the web checks, now also for `mv-go` and `mv-go-**` (path filter and `main` entries unchanged); `refuse-main-head` fails a PR whose head branch is `main` outside `main` |

Branch protection on `mv-go` requires `go-ci` and `secret-scan` (owner setting). Every PostgreSQL image (official or a derivative) in compose, `.github` (workflows and actions, `.yml` and `.yaml`) and the Go code is `postgres:18-alpine` (`pgtest.TestEveryPostgresImageIsTheSame`); a change under `.github/workflows/` or `.github/actions/` therefore runs the Go jobs.
