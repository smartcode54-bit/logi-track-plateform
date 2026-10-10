# logitrack-api

Go backend for the LogiTrack migration off Firebase (`mv-go`). Design: [`developer-spec.md`](../developer-spec.md) §2, routes in [Appendix B](../shared-docs/specs/mv-go/B-api-catalog.md). Branch policy: work lands by PR into `mv-go`, never `main` (R90).

Status: **T01 scaffold + T02 local stack + T03 migrations + T04 core schema + T05 own auth + T06 Google sign-in + T07 RBAC + T08 Firebase bridge + T36 billing engine + TW2 edge (web container + Caddy) + T14 CI + T17 web flags**. One module, seven binaries, shared `internal/`; the first routes are `/v1/auth/*` and `/v1/me*` (T05), `/v1/roles` (T07) and `GET /v1/config/web-flags` (T17).

## Layout

```
cmd/api          two HTTP listeners (internal + public) and /metrics
cmd/worker       RabbitMQ consumers of WORKER_CONSUMERS: retries 5 -> {queue}.dead, consumer_inbox (T10)
cmd/scheduler    leader-only: outbox relay, Bangkok cron, dead-letter replays (T10)
cmd/migrate      goose chain embedded from migrations/, run as logitrack_migrator (T03)
cmd/seed         seed profiles (T16)           — exits 3 until implemented
cmd/etl          Firestore/GCS → PG/MinIO (T15) — exits 3 until implemented
cmd/release      APK publish CLI (T52)         — exits 3 until implemented
internal/app                 process wiring, configs, API server (BuildAPI: pool, Redis, auth), graceful shutdown
internal/auth                own auth (T05): login, refresh families, revocation, passwords, /v1/me*, RequireAuth
internal/auth/token          Ed25519 access JWT: sign, verify (active + previous key), RFC 7638 kid, JWKS document
internal/auth/password       Argon2id PHC hashing, re-hash on weaker parameters, password policy
internal/auth/firebasescrypt verify-then-rehash of imported Firebase scrypt hashes
internal/auth/google         Google ID-token verifier (go-oidc, lazy discovery, aud allow list; googletest = in-process fake Google)
internal/auth/firebase       Firebase bridge protocol, no Admin SDK (T08): ID-token verifier, RS256 custom tokens, Identity Toolkit accounts; firebasetest = in-process fake Google
internal/authz               request principal; 81-key catalog, role defaults, resolution, web route map, RequireCap/RequireTenant (T07)
internal/authz/tsgen         writes the catalog to ../shared-docs/schemas/capabilities.ts (go generate)
internal/iam                 per-request authorization (RBAC: overrides under rbac:ver, steward, contractor reach, X-Act-On-Tenant) + GET /v1/roles (T07)
internal/scope               dispatcher / customer-scope reads: scope_* views only (repo/scope_*.sql -> scopedb, sqlc vet rule scope-views-only)
internal/security            the only writer of security_events: security.Append in the caller's transaction (T05)
internal/webcfg              runtime web domain flags: GET /v1/config/web-flags from PG_OWNED_DOMAINS + WEB_FLAG_OVERRIDES (T17)
internal/platform/config     env loading: all missing/invalid names in one error, never values
internal/platform/logx       zerolog + redacting writer (authorization, password, *token, cookie, idCard, ...)
internal/platform/httpx      envelopes, error codes, request id, client IP, access log
internal/platform/ingress    route groups and the public allow-list
internal/platform/health     /healthz, /readyz, /startupz, drain state
internal/platform/telemetry  OpenTelemetry (OTLP/HTTP) and Prometheus
internal/platform/db         pgx pools per role (R66), WithPrincipal (T07) and WithSystem; dbq = sqlc output; pgtest = postgres:18-alpine for tests
internal/platform/migrate    migration rules (R31), goose runner with a session lock; migratetest = round trip
internal/platform/clock      Bangkok (+07:00) calendar: dates, days, months, Bangkok midnight; never the wall clock
internal/platform/jsmath     JavaScript number semantics money code needs: Math.round, Round2, toFixed
internal/platform/cache      Redis layer (T09): client, lt:{APP_ENV}: keyspace, read-through caches, invalidation, money-path guard; cachetest = redis:7-alpine for tests
internal/platform/httpx/idempotency  Idempotency-Key middleware: Redis hot copy + idempotency_keys durable claim (R53)
internal/platform/httpx/ratelimit    GCRA buckets of Appendix B §B.6.3 + Fiber middleware (429 resource_exhausted, Retry-After)
internal/platform/mq         RabbitMQ topology (Appendix B §B.5), Declare, confirm Publisher, Consume with the retry ladder (T10)
internal/platform/outbox     outbox.Append (the only way to emit) and the relay: LISTEN + tick, SKIP LOCKED, confirms (T10)
internal/platform/inbox      consumer_inbox claim in the side-effect transaction (T10)
internal/platform/realtime   SSE topics and the Redis writer: INCR rtlog:seq, XADD {seq}-0, PUBLISH rt:{topic} (T10)
internal/platform/email      SMTP sender (multipart, UTF-8) for notify.email (T10)
internal/platform/push       FCM HTTP v1 client (service-account JWT grant, 16 in flight) + pushtest stand-in (T13)
internal/platform/asynctest  test-only RabbitMQ, Redis and Mailpit containers with the compose images (T10)
internal/jobs                jobs table, lock:job / lock:cron, GET /v1/jobs*, queue replay (T10)
internal/scheduler           advisory-lock leader, Bangkok cron table, housekeeping jobs (T10)
internal/notify              notify.email: reset and invite links in th + en (T10); notify.fcm: legacy push payloads, tasks_changed, session_revoked (T13)
internal/billing/compute     the billing engine (T36): pure port of lib/billingCompute.ts + the pure pricing rules
internal/billing/documents   pure invoice layout rules: axis date, price rounds, line items (renderers: T39)
internal/golden              test-only runner for testdata/golden vectors
tools/analyzers/withsystem   fails make lint when db.WithSystem, inbox.Run, a jobs.Submit InTx hook, db.RLS, an authz.Principal literal or raw GUC SQL is used outside its allow-list (T07)
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
| `RABBITMQ_URL` | worker, scheduler | yes | — (`api` never connects; the broker connection is retried with backoff) |
| `OUTBOX_RELAY_INTERVAL`, `OUTBOX_BATCH_SIZE`, `RTLOG_MAXLEN`, `RTLOG_TTL` | scheduler | no | `200ms`, `500`, `1000`, `24h` |
| `WORKER_CONSUMERS`, `RABBITMQ_PREFETCH` | worker | no | `all` (or a comma list of billing, notify, documents, integrations, hr, platform, sync); per-queue prefetch of Appendix B |
| `FCM_ENABLED`, `FCM_PROJECT_ID`, `FCM_SERVICE_ACCOUNT_JSON` | worker | project and key file path when FCM is on | off (pushes are acked unsent locally) |
| `EMAIL_ENABLED`, `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM`, `SMTP_FROM_NAME`, `SMTP_STARTTLS`, `PUBLIC_WEB_BASE_URL`, `PASSWORD_RESET_TTL` | worker (`PASSWORD_RESET_TTL` also api) | host, from and base URL when email is on | off, -, `587`, -, -, -, `LogiTrack`, `true`, -, `30m` |
| `API_INTERNAL_ADDR`, `API_PUBLIC_ADDR` | api | yes | — |
| `PUBLIC_ROUTE_GROUPS` | api | no | all five public groups |
| `TRUSTED_PROXY_CIDRS` | api | no | none (no `X-Forwarded-For` trusted) |
| `DATABASE_URL` (+ `DATABASE_MAX_CONNS`, `DATABASE_MIN_CONNS`) | api, worker, scheduler | yes | — (`logitrack_app`; the pool connects lazily, `/readyz` checks it in the api) |
| `REDIS_URL`, `REDIS_KEY_PREFIX`, `REDIS_TLS` | api, worker, scheduler | URL yes | prefix `lt:{APP_ENV}:` (any other value is refused), TLS off |
| `JWT_SIGNING_KEY_FILE`, `JWT_ACTIVE_KID`, `JWT_ISSUER`, `JWT_AUDIENCE` | api | yes | — (`make dev-keys` writes the local key and kid; a kid that is not the key's thumbprint stops the api, exit 2) |
| `JWT_PREVIOUS_KEY_FILE`, `JWT_ACCESS_TTL` | api | no | none, `15m` |
| `REFRESH_TOKEN_TTL_WEB`, `REFRESH_TOKEN_TTL_MOBILE`, `PASSWORD_RESET_TTL`, `PASSWORD_MIN_LENGTH` | api | no | `168h` (30 d absolute cap), `2160h`, `30m`, `10` |
| `ARGON2_MEMORY_KB`, `ARGON2_ITERATIONS`, `ARGON2_PARALLELISM` | api | no | `65536`, `3`, `2` |
| `FIREBASE_SCRYPT_SIGNER_KEY`, `_SALT_SEPARATOR`, `_ROUNDS`, `_MEM_COST` | api | all four or none | none: legacy hashes cannot sign in (`make env` sets the public firebase/scrypt test set locally) |
| `RATE_LIMIT_ENABLED`, `RATE_LIMIT_LOGIN`, `RATE_LIMIT_PUBLIC_FORMS`, `RATE_LIMIT_EVIDENCE` | api | no | `true`, `10/1m` (login per IP; the 5-failure lockout always applies), `5/1h`, `60/1m`; parsed once by `ratelimit.Config` |
| `AUTH_FIREBASE_BRIDGE_MODE` | api | no | `off` (`off`, `mobile`, `web`, `both`; any mode but `off` needs the next two) |
| `FIREBASE_PROJECT_ID`, `GOOGLE_APPLICATION_CREDENTIALS` | api | with the bridge | none: no Firebase ID-token verification, no custom tokens, no mirror (the key file is read at start-up; unreadable or malformed = exit 2, naming the variable only). The service account needs Firebase Authentication Admin (`roles/firebaseauth.admin`) on the project, and no token-creator role; without the grant every mirrored change is `503 bridge_unavailable` (main spec §16.1) |
| `PG_OWNED_DOMAINS`, `WEB_FLAG_OVERRIDES` | api | no | none (every web domain on Firestore); `.env.example` sets `PG_OWNED_DOMAINS=all` locally. See "Web flags" |

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

Rules (`migrate check`, also part of `make gen-check`): files `NNNN_name.sql` numbered 1..N without gaps; every file has `-- +goose Up` and `-- +goose Down`; an empty Down only in a data migration whose header (before `-- +goose Up`) says `-- irreversible` (the round trip then rolls back only to the highest such version); `-- +goose NO TRANSACTION` exactly when the Up or Down builds or drops an index `CONCURRENTLY`; no empty `StatementBegin` block (goose would silently drop the next statement); ids default to `uuidv7()`, never `gen_random_uuid()`; no goose `ENVSUB`. Comments, strings and quoted identifiers are ignored when matching. Once a migration is applied to a database that is kept (from the P0 deploy on), it is never edited: follow-ups take the next number. Before P0 the baseline may still be corrected in place; goose records versions only (no checksum), so `make migrate` does not re-run an edited file on a database that already applied it, and such a local or dev database is reset (`make reset`).

**Upgrading an existing dev database (T10):** `0009_infra` now also grants `DELETE` on `jobs` to `logitrack_app` (the nightly `jobs.prune`). A database migrated from `mv-go` between T04 and T10 lacks it and every 04:00 `jobs.prune` fails with `permission denied for table jobs` (42501). Run `make reset`, or once as `logitrack_migrator`: `GRANT DELETE ON jobs TO logitrack_app;`.

Baseline (T04): `0001`-`0009` are Appendix A §A.2.0-§A.2.8 with each file's Appendix C policy block (RLS on exactly the 70 tables of Appendix C §C.3.0, generators from 0001), `0009_infra` is the only file of the baseline that grants or revokes (R66) and hands the eight SECURITY DEFINER functions to `logitrack_rls_definer`; `0010_d5_unique_constraints` (`NO TRANSACTION`) builds the three D5 unique indexes `CONCURRENTLY` and stops with an INVALID-index error while a legacy duplicate survives (R88). Local, CI and seeded databases run `up`; production runs `up-to 9` until the T24 runbook. Schema acceptance tests: `migrations/schema_integration_test.go` (tables equal the `CREATE TABLE`s of Appendix A, uuidv7 ids, STORED generated columns, inline `tenant_source`, vocabularies, append-only and void-only rules as `logitrack_app`, the draft-statement cascade, allocators, the D5 guard), `migrations/rls_integration_test.go` (policy behaviour as principals: the tenant-bound driver maintenance gate, the `file_objects` commit guard) and `internal/platform/db/rls_catalog_test.go` (RLS flags, policy names, freeze triggers, and the effective `logitrack_app` table privileges from `has_table_privilege`, so `PUBLIC` and inherited grants count, on `public` and `etl` against Appendix C §C.3.0 and §C.3.2, read from the document; nothing granted to `PUBLIC`).

Integration tests (`-tags=integration`) use `internal/platform/db/pgtest`: one `postgres:18-alpine` container per test binary with `deploy/postgres-init/00-roles.sql` as an init script and a fresh database per test owned by `logitrack_migrator`. `make test-integration` points testcontainers at the active docker context (`DOCKER_HOST`).

sqlc: `make sqlc` regenerates; `make gen-check` runs `sqlc diff` and `sqlc vet`. `omit_unused_structs` keeps each package to the models of the tables its queries use. The first query (`internal/platform/db/queries/login.sql`) is the migrate preflight; domain tasks add one `sql` block per repo package.

## Auth (T05, main spec §4, Appendix C §C.4-§C.5)

Go returns tokens in JSON bodies and never sets cookies (the BFF does, TW3). Routes: `/v1/auth/{login,google/nonce,google,refresh,logout,logout-all,tenant,password/forgot,password/reset,password/change,sse-ticket}` on both listeners and `/v1/me`, `/v1/me/tenants`, `/v1/me/sessions[/{sid}]`, `PUT /v1/me/devices` and `DELETE /v1/me/devices/{installId}` (T13: one FCM token per user and install, at most 10 rows per user; a mobile session registers its own install only; the token moves from the caller's own rows, or from another user's row of the same install with a `device_token_moved` security event, else `409 already_exists`) on the internal listener only; `app.BuildAPI` hands the auth service to `newAPI` (`cmd/api`), so `api/routes.txt` lists these routes and serving checks them like every other group.

- **Access JWT**: EdDSA, `kid` = RFC 7638 thumbprint, claims exactly `iss sub aud exp nbf iat jti sid ver tid rol plt dsp drv cs amr` (empty optional claims omitted). `token.KeySet.JWKS` renders the JWKS that TW3 mounts at `/.well-known/jwks.json`.
- **Per request** (`auth.Service.RequireAuth`): signature, iss, aud, exp (30 s leeway), then one Redis pipeline (`auth:sess:revoked:{sid}`, `auth:user:ver:{sub}`, bounded to 300 ms); a version miss or a token newer than the cached version reads `users.auth_version` and `sessions.revoked_at` and rebuilds both keys; with Redis down the same check runs in PostgreSQL (`auth_revocation_fallback_total`). Revoked session -> `401 session_revoked`; stale `ver` -> `401 token_expired` `details.reason=claims_changed`; expired -> `reason=expired`.
- **Sessions and refresh families**: one session per login (`active_tenant_id`, `install_id`; a re-login on the same install revokes the older one), refresh tokens rotate in a family; a rotated token presented within 30 s while its successor is unused yields a sibling, any other reuse revokes the family and session, bumps `auth_version` and writes `refresh_token_reuse` in the same transaction.
- **Revocation** (`auth.Service.RevokeInTx` + `Apply`): claims changes bump `auth_version` only; disable, password events, admin revoke, logout and reuse end sessions; every one queues outbox `user.sessions_revoked` on `user:{uid}` and, after COMMIT, raises `auth:user:ver:*` (only upward) and marks `auth:sess:revoked:*`; a failed write is retried in the background until the access tokens it judges expire (`auth_postcommit_failures_total{op}`; `Service.Close` stops the retries). `internal/iam` (T19) calls it for user administration and appends its row with `security.Append`. Lock order: every auth transaction that writes sessions or refresh tokens locks the `users` row first, then `sessions`, then `refresh_tokens`; `RevokeInTx` callers keep it.
- **Passwords**: Argon2id (parameters read back; weaker stored hashes re-hash on login), Firebase scrypt verify-then-rehash, `must_change_password` -> `403 password_change_required` with a single-use `passwordChangeTicket` redeemed at `/v1/auth/password/change`, forgot always `202` (outbox `auth.password_reset_requested`; the worker's `notify.email` consumer (T10) creates the token with `auth.IssueResetToken`, which stores only the hash), 5 failures / 15 min lock an email (`423 locked`; each attempt is counted before its check). Every failed check costs one Argon2id plus, while `FIREBASE_SCRYPT_*` is set, one scrypt (no timing enumeration); at most `GOMAXPROCS / ARGON2_PARALLELISM` hashes run at once (`503` after 3 s); a login opens its session only while the row still holds the credential it verified, so a racing reset wins.
- **Google sign-in** (T06, Appendix C §C.4.10): `GET /v1/auth/google/nonce` (web, through the BFF; single use, 10 min) and `POST /v1/auth/google {idToken, nonce?, platform, installId?, appVersion?}` with a GIS or `google_sign_in` ID token; no authorization-code flow (R23). `internal/auth/google` verifies with go-oidc (discovery on the first sign-in, so the api starts without Google; RS256; every `aud` in `GOOGLE_OIDC_ALLOWED_CLIENT_IDS`; `email_verified`). The account is the `auth_identities` Google `sub`, else the user with the verified email when Google is authoritative for it (a Gmail address or a Workspace account with `hd`), linked in the same transaction as `google_identity_linked`; otherwise `403 no_account` (no self-signup). A body nonce must be the token's and unused; a driver-app token (`azp` != `aud`) sent without one may carry the SDK's own nonce (iOS), which is ignored. Bad token or nonce `401 invalid_token`, Google unreachable `503`, variable unset `404`. Locally the variable is empty, so Google sign-in is off; tests use `googletest` (no network).
- Every statement runs in `db.WithSystem` (`app.bypass_tenant=on`); queries are sqlc (`internal/auth/queries` -> `internal/auth/authdb`). Tests: `go test ./internal/auth/... ./internal/security/...` (unit: JWT, the firebase/scrypt public vectors, Argon2id and the hashing gate, policy, equal work per failed check) and `make test-integration` (PostgreSQL 18 + Redis 7 containers, both listeners; `hardening_integration_test.go` covers the races and lost Redis writes).

## RBAC and tenant isolation (T07, main spec §4.5-§4.7, Appendix C §C.2-§C.3)

- **Catalog** (`internal/authz/catalog.go`): 81 colon keys (77 + 4 platform, R73) with module, class (`tenant`, `global`, `self`, `scope`, `platform`) and en/th titles; role, scope and platform default sets of §C.2.4 (`roles.go`). Tests read §C.2.3 / §C.2.4 from the specification and fail on any drift. `go generate ./internal/authz` writes `../shared-docs/schemas/capabilities.ts` (keys, catalog, defaults, steward keys, `ROUTE_CAPABILITIES` of the edge gate), checked by `make gen-check`; GET `/v1/roles` (internal) serves the same catalog.
- **Per request** (`iam.RBAC`, wired as `auth.Deps.Authorizer`, so `auth.RequireAuth` runs it for every authenticated request): `X-Act-On-Tenant` (platform only; `<uuid>` acts as that tenant's tenant_admin, `*` is a read-only bypass for GET/HEAD; one `platform_cross_tenant_access` row per request of a platform principal committed before the handler, refused, malformed and unknown-tenant attempts included (`details.outcome`), `503` when it cannot be written), then the tenant kind, the steward flag (own-fleet staff or platform_admin, R60), contractor reach (`cache:tenant:subtenants:{tid}`, staff only) and the effective set: role default -> platform-wide override -> tenant override (`rbac:caps:{tid}:{role}:{rbac:ver}:{fp}`, 10 min, `fp` = `authz.RoleSetFingerprint` of the compiled-in defaults so no release reads another's sets; tenant_admin is not overridable; platform, scope and tenant-row `users:assign_role` overrides are ignored), global keys only for stewards, ∪ scope sets (the customer-scope set only without a membership) ∪ platform sets. `RBAC.BumpVersion` (`INCR rbac:ver`) after a matrix change makes it effective on the next request. Redis failures fall back to PostgreSQL.
- **Guards** (`internal/authz/http.go`): `RequireCap(any-of...)` -> `403 permission_denied` with `details.missingCapability`; `RequireTenant` -> `403 tenant_required`; `RequirePlatform`, `RequireSteward`. An unresolved principal holds nothing.
- **Transactions**: `db.WithPrincipal(ctx, pool, p, fn)` sets the nine GUCs of §C.3.2 from `authz.Principal.RLS()` in one round trip (READ ONLY for `*`; a writable bypass and a nil principal, typed nil pointer included, are refused before `BEGIN`). `db.WithSystem` is for work without a request principal; `go run ./tools/analyzers/withsystem` (part of `make lint`) fails when another package calls it, or one of the two helpers that hand their caller a `WithSystem` transaction (`inbox.Run`, and `jobs.Service.Submit` given an `InTx` hook: an `InTx` key in a `jobs.SubmitInput` literal or a write of `.InTx`), or names `db.RLS`, builds an `authz.Principal`, writes `ActOnAll`/`ActOnTenant` or carries raw GUC / `set_config` SQL outside its allow-lists.
- **Scope principals** (dispatcher, customer scope) read only through `internal/scope/repo/scope_*.sql` on the `scope_*` views; sqlc vet rule `scope-views-only` fails on any base table or write, and `make gen-check` proves it against `internal/scope/testdata/vetcheck`.

Tests: `go test ./internal/authz/ ./internal/scope/ ./tools/analyzers/...` (catalog vs Appendix C, role x route over the 53 legacy routes (frozen in `internal/authz/testdata`) and every `app/app/**/page.tsx`, resolution, guards, GUC mapping, DTOs vs views, the vet rule) and `make test-integration` (`internal/iam`: real logins against PostgreSQL 18 + Redis 7; carrier staff read 0 own-fleet rows even with a crafted predicate, own-fleet staff reach their carriers, the steward rule at the capability and RLS layers, dispatcher and customer reads through the views only, `X-Act-On-Tenant` audit rows (refused attempts too), override effective on the next request, role sets cached per defaults fingerprint; `internal/platform/db`: the GUCs of `WithPrincipal`).

## Firebase bridge (T08, main spec §4.9, Appendix C §C.6)

`AUTH_FIREBASE_BRIDGE_MODE` decides what the web and the APK may do with Firebase credentials while the strangler runs; no Firebase Admin SDK is linked (`internal/auth/firebase`). Nothing reaches Google at start-up: keys and OAuth2 tokens are fetched on first use.

| Mode | Custom tokens for the web | APK Firebase ID tokens | Account mirror | Phases |
|---|---|---|---|---|
| `off` | no (`/v1/bridge/*` 404) | no (404) | no | local default; after P8 |
| `web` | yes | no | yes | P0-P6, until TW7 |
| `mobile` | no | yes | yes | P7a-P8 |
| `both` | yes | yes | yes | only while P7a overlaps an unfinished TW7 |

- **Web** (`POST /v1/bridge/firebase-token`, internal listener only, bearer): an RS256 custom token signed with the `GOOGLE_APPLICATION_CREDENTIALS` service account for the caller's Firebase uid (`users.legacy_auth_uid`; an own-fleet user created in Go gets `users.id` at the first mint) with the legacy claims of its active context (`admin`, `role`, `driverId`, `customerScopeId`, `partnerScopeId`, Appendix C §C.6.3). Only own-fleet staff, `platform_admin` and users imported from a legacy partner or customer claim (an `auth_identities` `firebase_legacy` row) get one; everyone else `403 permission_denied`. `GET /v1/me` adds `legacyAuthUid` in this mode.
- **Firebase ID tokens** (`auth.Service.VerifyFirebaseIDToken`): go-oidc against `https://securetoken.google.com/{FIREBASE_PROJECT_ID}` with the securetoken JWKS; the principal (`amr` `firebase`, no session) comes from PostgreSQL, never from the token's claims. `FirebaseAPK` (the exchange of T55) needs a mode with `mobile`; `FirebaseShim` (T32) works in every mode. A Firebase token is never a bearer: `401 invalid_token` on every route.
- **Account mirror** (every mode but `off`): password sets (change, reset, ticket change, temporary password), disable / enable, a soft delete (every session revoked with reason `disabled`: the account is disabled), an admin revocation of every session and claims changes are written to Firebase Auth through Identity Toolkit inside the request's transaction, before COMMIT; a failure is `503 bridge_unavailable` and commits nothing in PostgreSQL. A creation that fails after Firebase created the account deletes it again, and a retry adopts an orphan that survived (a uuid uid nobody in PostgreSQL holds); an email held by any other Firebase account is `409 already_exists` (`details.reason` `firebase_account_exists`). The service account needs Firebase Authentication Admin on `FIREBASE_PROJECT_ID`; a 401 / 403 from Identity Toolkit is logged at error level as that misconfiguration. Entry points for T19: `RevokeInTx`, `SetStatusInTx`, `NewTemporaryPassword` + `SetTemporaryPasswordInTx`, `MirrorNewUserInTx`. Metrics `auth_firebase_mirror_failures_total{op}`, `auth_firebase_custom_tokens_total`.

Tests use `internal/auth/firebase/firebasetest` (securetoken keys, OAuth2 token endpoint and Identity Toolkit in-process); nothing reaches Google. Signing a minted token in to the dev project is an owner step (Appendix C §C.9.5).

## Web flags (T17, main spec §10.6, §12.1)

`GET /v1/config/web-flags` (internal listener only, unauthenticated, `Cache-Control: no-store`) answers `{"data":{"domains":{"auth":"go","masterdata":"firebase",...}}}` for the nine domains `auth, masterdata, operations, billing, hr, comms, security, mobile_release, dashboard`. A domain listed in `PG_OWNED_DOMAINS` (empty, `all`, or a comma list) is `go`; each `WEB_FLAG_OVERRIDES` entry `domain=go|firebase` then replaces its domain, e.g. `auth=go` in P0 or `billing=firebase` to roll the billing pages back. Both are parsed at start-up and a bad value stops the api (exit 2, the message names the variable, never its value). The web reads the endpoint through the BFF as TanStack `['webFlags']` and polls it every 60 s, so a rollback is an edit of `WEB_FLAG_OVERRIDES` in `.env` plus `make api-reload`, with no web rebuild (R35, R41). The target runs `docker compose -f deploy/docker-compose.yml --env-file .env up -d --no-deps --wait api` from `logitrack-api/`, which recreates only the api with the new value and waits for it to be healthy; a bare `docker compose up -d api` finds no compose file here and, run from `deploy/`, reads no `.env`, and `docker compose restart` keeps the old environment. `make up` also works but rebuilds and waits on the whole stack. The Redis key `cache:web_flags` stays unused: the value is the api's own env.

## Billing engine (T36, main spec §6)

`internal/billing/compute` prices trips and standby events with no I/O and no clock: the caller loads rate cards, fuel rounds, fees, hub names and period locks in its pricing transaction and passes them in. Money stays float64 and matches V8 bit for bit: `jsmath.Round` rounds ties toward +Inf (and keeps `-0`), every product is wrapped in `float64(...)` so arm64 and amd64 `GOAMD64=v3` cannot fuse it into a multiply-add, and sums stay unrounded in input order. Deliberate differences from the TypeScript: a blank vehicle class is `no_vehicle_class` (R15), equal effective instants are ordered by legacy doc id, `created_at`, `id` whatever the load order (R16), a trip with no plan, delivery or creation instant is `no_billing_date` instead of `Date.now()` (R19), voided standby rates never price (R20). `IsFrozen` / `CarriesFuel` exist once in the module.

Golden vectors: `testdata/golden/billing` (see its README). `go test ./internal/billing/... ./internal/platform/...` checks them against Go; `pnpm test` in `logitrack-web` checks the same files against the TypeScript. Regenerate after a TypeScript change with `node testdata/golden/billing/export.mjs`.

## Async processing (T10, main spec §7, Appendix B §B.5)

- **Emit**: `outbox.Append(ctx, tx, outbox.Event{...})` in the service transaction is the only way to publish (auth's events included; it validates the routing key and topics and carries `requestId` and `traceparent`); `api` never connects to RabbitMQ. Admin jobs: `jobs.Service.Submit` (lock + row + `lt.jobs` command in one system tx after Go authorization, `202 {jobId}`).
- **Relay** (scheduler leader, `pg_try_advisory_lock(hashtext('lt-scheduler'))`): `LISTEN outbox_new` + `OUTBOX_RELAY_INTERVAL`, `OUTBOX_BATCH_SIZE` rows `FOR UPDATE SKIP LOCKED`; per row its hook, then `Cache.OnEvent` (drops the `cache:` keys the event makes stale and announces them on `rt:cache`; nothing for events outside `cache.InvalidatingEvents`), publisher confirms, then one Lua script per realtime event (`INCR rtlog:seq`, `XADD rtlog:{topic} {seq}-0`, `PUBLISH rt:{topic}`), then `published_at`. A failed hook, cache invalidation, publish or realtime step stops the batch at that row (`attempts`, `last_error`); a row whose publish was confirmed before a later step failed is not published again; rounds back off from the interval to 30 s while the head row keeps failing. The scheduler's Redis client and keyspace come from `cache.Open`, like the api's: every key (`rtlog:*`, `rt:*`, `lock:job:*`, `lock:cron:*`) is built by `cache.Keyspace`. Hook: `user.sessions_revoked` re-applies auth's revoked markers and cached version (`auth.RevocationHook`, `JWT_ACCESS_TTL`). Metrics `outbox_pending`, `outbox_lag_seconds`.
- **Consume** (worker): the topology is asserted at every connect; each queue of the `WORKER_CONSUMERS` groups that has a consumer runs with its Appendix B prefetch. A handler returns `nil` (ack), `mq.Permanent(err)` (ack, outcome recorded) or any other error: retry through `lt.retry` (`10s`, `1m`, `5m`, `30m`, `2h`), back to that queue only via `lt.requeue`, and the sixth failure goes to `{queue}.dead`. DB side effects run through `inbox.Run` (claim in `consumer_inbox` first, `db.WithSystem` with the event's tenant). Metrics `mq_messages_total{queue,outcome}`, `mq_dead_letter_depth{queue}`.
- **Consumers today**: `notify.email` (reset and invite links, Mailpit locally; `user.created` mails an invite only with `sendInvite: true`) and `notify.fcm` (T13: task pushes in the legacy format, silent `tasks_changed` once per driver per 30 s, `session_revoked` to the device of a revoked session; `idem:fcm` claims make every push at most once per token: a send whose answer was lost after the request reached FCM is logged `failed` "outcome unknown" and never resent; at most 10 devices per driver; off with `FCM_ENABLED=false`). The other queues fill with their issues; the worker logs which queues have no consumer yet.
- **Readiness** (`/readyz` on `METRICS_ADDR`): worker = PostgreSQL + Redis + its RabbitMQ session; scheduler = PostgreSQL + Redis, plus the broker connection while it is the leader. A failure is `503 unavailable` with `details.checks`.
- **Cron** (Bangkok, leader only, `lock:cron:{job}:{scheduledFor}`): `auth.token-cleanup` every 10 min, and at 04:00 `outbox.prune`, `inbox.prune`, `jobs.prune`, `idempotency.prune`, `rtlog.trim`; the command crons of §7.4 join with their consumers (`scheduler.Pending`).
- **Jobs API**: `GET /v1/jobs`, `GET /v1/jobs/{id}` (owner, or a platform role) and `POST /v1/admin/queues/{queue}/replay` (platform_admin) are mounted by `cmd/api` behind `auth.RequireAuth` (`app.JobGroups`). A replay creates a `queue.replay` job; the leader moves `{queue}.dead` back to `{queue}` with a fresh retry budget.
- **Tests**: `make test-integration` runs `internal/platform/{mq,outbox,inbox}`, `internal/jobs`, `internal/scheduler` and `internal/notify` against postgres:18-alpine, rabbitmq:4-management-alpine, redis:7-alpine and axllent/mailpit (images read from the compose file).

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
- **Rate limits** (`httpx/ratelimit`): `Limiter.Middleware(enabled, rules...)`; a request is counted when it is checked (no peek, so parallel requests cannot all pass); subjects are stored as a sha256 prefix (pseudonymous, not anonymous); `ByIP` limits an IPv6 client by its /64; fail open (logged and counted) when Redis does not answer within 300 ms. A rule without a usable limit panics when the route is built, and `Config.Validate` refuses an unusable `RATE_LIMIT_*` value, so nothing fails open by mistake. The api builds one `Limiter`: `internal/auth` checks its buckets (`login_ip`, `refresh_session`, `sse_ticket`, `forgot_*`, `reset_ip`) through `Limiter.Allow` with the same 429 and fail-open behaviour, so every caller of a bucket spends one budget. `login_fail` is not a GCRA bucket: the sign-in lockout of Appendix C §C.4.12 lives in `internal/auth` (T05).
- **Mirror ack** (`SetMirrorAck`, `WaitMirrorAck`) for read-your-writes in P2-P7a; single-use tickets (`PutTicket`, `TakeTicket` with `GETDEL`) for `auth:pwchg`, `auth:sse`, `auth:google:nonce`.
- Wiring: `app.BuildAPI` (T05) builds the process's one Redis client with `cache.Open`, so the auth store's per-request checks get the same deadlines, and calls `cache.RouteDriverLogs(log)` so go-redis log lines go through the redacting logger; routes adopt the middleware as they land (first `✱` routes: T31, T56).

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
