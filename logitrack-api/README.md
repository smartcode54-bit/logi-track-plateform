# logitrack-api

Go backend for the LogiTrack migration off Firebase (`mv-go`). Design: [`developer-spec.md`](../developer-spec.md) §2, routes in [Appendix B](../shared-docs/specs/mv-go/B-api-catalog.md). Branch policy: work lands by PR into `mv-go`, never `main` (R90).

Status: **T01 scaffold + T02 local stack + T03 migrations + T04 core schema + T05 own auth + T36 billing engine + TW2 edge (web container + Caddy) + T14 CI**. One module, seven binaries, shared `internal/`; the first routes are `/v1/auth/*` and `/v1/me*` (T05).

## Layout

```
cmd/api          two HTTP listeners (internal + public) and /metrics
cmd/worker       background consumers (T10); today: /metrics + wait for SIGTERM
cmd/scheduler    cron + outbox relay (T10); today: /metrics + wait for SIGTERM
cmd/migrate      goose chain embedded from migrations/, run as logitrack_migrator (T03)
cmd/seed         seed profiles (T16)           — exits 3 until implemented
cmd/etl          Firestore/GCS → PG/MinIO (T15) — exits 3 until implemented
cmd/release      APK publish CLI (T52)         — exits 3 until implemented
internal/app                 process wiring, configs, API server (BuildAPI: pool, Redis, auth), graceful shutdown
internal/auth                own auth (T05): login, refresh families, revocation, passwords, /v1/me*, RequireAuth
internal/auth/token          Ed25519 access JWT: sign, verify (active + previous key), RFC 7638 kid, JWKS document
internal/auth/password       Argon2id PHC hashing, re-hash on weaker parameters, password policy
internal/auth/firebasescrypt verify-then-rehash of imported Firebase scrypt hashes
internal/authz               request principal (T05 identity half; catalog and RequireCap with T07)
internal/security            the only writer of security_events: security.Append in the caller's transaction (T05)
internal/platform/config     env loading: all missing/invalid names in one error, never values
internal/platform/logx       zerolog + redacting writer (authorization, password, *token, cookie, idCard, ...)
internal/platform/httpx      envelopes, error codes, request id, client IP, access log
internal/platform/ingress    route groups and the public allow-list
internal/platform/health     /healthz, /readyz, /startupz, drain state
internal/platform/telemetry  OpenTelemetry (OTLP/HTTP) and Prometheus
internal/platform/db         pgx pools per role (R66), WithSystem; dbq = sqlc output; pgtest = postgres:18-alpine for tests
internal/platform/migrate    migration rules (R31), goose runner with a session lock; migratetest = round trip
internal/platform/clock      Bangkok (+07:00) calendar: dates, days, months, Bangkok midnight; never the wall clock
internal/platform/jsmath     JavaScript number semantics money code needs: Math.round, Round2, toFixed
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
| `DATABASE_URL` (+ `DATABASE_MAX_CONNS`, `DATABASE_MIN_CONNS`) | api | yes | — (`logitrack_app`; the pool connects lazily, `/readyz` checks it) |
| `REDIS_URL`, `REDIS_KEY_PREFIX`, `REDIS_TLS` | api | URL yes | prefix `lt:{APP_ENV}:` (any other value is refused), TLS off |
| `JWT_SIGNING_KEY_FILE`, `JWT_ACTIVE_KID`, `JWT_ISSUER`, `JWT_AUDIENCE` | api | yes | — (`make dev-keys` writes the local key and kid; a kid that is not the key's thumbprint stops the api, exit 2) |
| `JWT_PREVIOUS_KEY_FILE`, `JWT_ACCESS_TTL` | api | no | none, `15m` |
| `REFRESH_TOKEN_TTL_WEB`, `REFRESH_TOKEN_TTL_MOBILE`, `PASSWORD_RESET_TTL`, `PASSWORD_MIN_LENGTH` | api | no | `168h` (30 d absolute cap), `2160h`, `30m`, `10` |
| `ARGON2_MEMORY_KB`, `ARGON2_ITERATIONS`, `ARGON2_PARALLELISM` | api | no | `65536`, `3`, `2` |
| `FIREBASE_SCRYPT_SIGNER_KEY`, `_SALT_SEPARATOR`, `_ROUNDS`, `_MEM_COST` | api | all four or none | none: legacy hashes cannot sign in (`make env` sets the public firebase/scrypt test set locally) |
| `RATE_LIMIT_ENABLED`, `RATE_LIMIT_LOGIN` | api | no | `true`, `10/1m` (login per IP; the 5-failure lockout always applies) |

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

## Auth (T05, main spec §4, Appendix C §C.4-§C.5)

Go returns tokens in JSON bodies and never sets cookies (the BFF does, TW3). Routes: `/v1/auth/{login,refresh,logout,logout-all,tenant,password/forgot,password/reset,password/change,sse-ticket}` on both listeners and `/v1/me`, `/v1/me/tenants`, `/v1/me/sessions[/{sid}]` on the internal listener only; `app.BuildAPI` hands the auth service to `newAPI` (`cmd/api`), so `api/routes.txt` lists these routes and serving checks them like every other group.

- **Access JWT**: EdDSA, `kid` = RFC 7638 thumbprint, claims exactly `iss sub aud exp nbf iat jti sid ver tid rol plt dsp drv cs amr` (empty optional claims omitted). `token.KeySet.JWKS` renders the JWKS that TW3 mounts at `/.well-known/jwks.json`.
- **Per request** (`auth.Service.RequireAuth`): signature, iss, aud, exp (30 s leeway), then one Redis pipeline (`auth:sess:revoked:{sid}`, `auth:user:ver:{sub}`, bounded to 300 ms); a version miss or a token newer than the cached version reads `users.auth_version` and `sessions.revoked_at` and rebuilds both keys; with Redis down the same check runs in PostgreSQL (`auth_revocation_fallback_total`). Revoked session -> `401 session_revoked`; stale `ver` -> `401 token_expired` `details.reason=claims_changed`; expired -> `reason=expired`.
- **Sessions and refresh families**: one session per login (`active_tenant_id`, `install_id`; a re-login on the same install revokes the older one), refresh tokens rotate in a family; a rotated token presented within 30 s while its successor is unused yields a sibling, any other reuse revokes the family and session, bumps `auth_version` and writes `refresh_token_reuse` in the same transaction.
- **Revocation** (`auth.Service.RevokeInTx` + `Apply`): claims changes bump `auth_version` only; disable, password events, admin revoke, logout and reuse end sessions; every one queues outbox `user.sessions_revoked` on `user:{uid}` and, after COMMIT, raises `auth:user:ver:*` (only upward) and marks `auth:sess:revoked:*`; a failed write is retried in the background until the access tokens it judges expire (`auth_postcommit_failures_total{op}`; `Service.Close` stops the retries). `internal/iam` (T19) calls it for user administration and appends its row with `security.Append`. Lock order: every auth transaction that writes sessions or refresh tokens locks the `users` row first, then `sessions`, then `refresh_tokens`; `RevokeInTx` callers keep it.
- **Passwords**: Argon2id (parameters read back; weaker stored hashes re-hash on login), Firebase scrypt verify-then-rehash, `must_change_password` -> `403 password_change_required` with a single-use `passwordChangeTicket` redeemed at `/v1/auth/password/change`, forgot always `202` (outbox `auth.password_reset_requested`; the `notify.email` consumer of T10 calls `IssuePasswordResetToken`, which stores only the hash), 5 failures / 15 min lock an email (`423 locked`; each attempt is counted before its check). Every failed check costs one Argon2id plus, while `FIREBASE_SCRYPT_*` is set, one scrypt (no timing enumeration); at most `GOMAXPROCS / ARGON2_PARALLELISM` hashes run at once (`503` after 3 s); a login opens its session only while the row still holds the credential it verified, so a racing reset wins.
- Every statement runs in `db.WithSystem` (`app.bypass_tenant=on`); queries are sqlc (`internal/auth/queries` -> `internal/auth/authdb`). Tests: `go test ./internal/auth/... ./internal/security/...` (unit: JWT, the firebase/scrypt public vectors, Argon2id and the hashing gate, policy, equal work per failed check) and `make test-integration` (PostgreSQL 18 + Redis 7 containers, both listeners; `hardening_integration_test.go` covers the races and lost Redis writes).

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
