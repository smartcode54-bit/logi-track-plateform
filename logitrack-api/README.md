# logitrack-api

Go backend for the LogiTrack migration off Firebase (`mv-go`). Design: [`developer-spec.md`](../developer-spec.md) §2, routes in [Appendix B](../shared-docs/specs/mv-go/B-api-catalog.md). Branch policy: work lands by PR into `mv-go`, never `main` (R90).

Status: **T01 scaffold + T02 local stack + T03 migrations + T04 core schema + T14 CI**. One module, seven binaries, shared `internal/`; no domain routes yet.

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
migrations/                  NNNN_name.sql, embedded into cmd/migrate: the Appendix A baseline 0001-0010 (T03, T04)
api/routes.txt               generated route table (method, path, listeners) checked by go-ci gen-check (T14)
sqlc.yaml                    sqlc v1.31.1: schema = migrations/, one block per query package
```

## Listeners (main spec §2.6)

| Listener | Env | Serves |
|---|---|---|
| internal | `API_INTERNAL_ADDR` | every route group, incl. `/readyz`, `/startupz` |
| public | `API_PUBLIC_ADDR` | only groups marked public **and** listed in `PUBLIC_ROUTE_GROUPS` (subset of `/v1/mobile`, `/v1/auth`, `/public/v1`, `/evidence`, `/healthz`); everything else `404 not_found` |
| metrics | `METRICS_ADDR` | `GET /metrics` (Prometheus), private |

`api routes` builds the API through the same `newAPI` as serving (`cmd/api/main.go`; domain groups are added there), refuses a group marked public outside the list and any public route other than `/healthz` or a path below `/v1/mobile/`, `/v1/auth/`, `/public/v1/`, `/evidence/` (exit 1; serving refuses the same at startup), and prints one line per route with the listeners that may serve it. `go generate` writes it to `api/routes.txt`, so a route or listener change shows up in review and `make gen-check` fails when the table is stale.

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
- `make up` starts PostgreSQL 18, Redis 7, RabbitMQ 4 (+ definitions from `internal/platform/mq`), MinIO (+ buckets), mailpit, sets the role passwords (`make dev-db`), runs `migrate up`, then starts api, worker and scheduler (they wait for migrate to exit 0) and waits until all are healthy. Profiles: `tools` (seed, etl), `mocks` (WireMock), `obs` (Jaeger), `tunnel`; `EDGE=1` adds web and Caddy once TW2 lands.
- `make smoke` checks the T02 and T03 acceptance criteria against the running stack.

| Service | Host port (127.0.0.1) | Notes |
|---|---|---|
| postgres | 5432 | PGDATA `/var/lib/postgresql/18/docker`, volume at `/var/lib/postgresql`; roles from `deploy/postgres-init/00-roles.sql`; `logitrack_test` for integration tests |
| redis | 6379 | AOF, `noeviction` |
| rabbitmq | 5672, 15672 | 5 exchanges, 16 work queues, 16 `.dead` queues, 5 retry queues |
| minio | 9000, 9001 | `S3_BUCKET` private; `S3_PUBLIC_BUCKET` anonymous GET on `app_releases/` only; app user limited to both buckets |
| mailpit | 8025, 1025 | SMTP sink |
| api | 8080, 8081 | internal and public listeners (local only; Caddy fronts public from TW2) |

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

`make lint` runs gofmt, `go mod tidy -diff`, `go vet`, golangci-lint v2.14.0 and govulncheck v1.8.0 (versions pinned in the Makefile only); `make gen-check` the generated-code, route and migration gates.

Run the api locally (any free ports):

```bash
APP_ENV=local LOG_FORMAT=console API_INTERNAL_ADDR=127.0.0.1:8080 API_PUBLIC_ADDR=127.0.0.1:8081 METRICS_ADDR=127.0.0.1:9090 go run ./cmd/api
```

## CI (T14, main spec §17.2)

Three workflows run on pushes and pull requests of `mv-go` and `mv-go-**`; none has a `main` trigger and none deploys (R90).

| Workflow | Jobs |
|---|---|
| `go-ci` (`.github/workflows/go-ci.yml`) | `changes` (skips the Go jobs when no checked path changed); `lint` (`make lint`); `gen-check` (`make gen-check`, `make env-check`, clean tree); `migrate` (`make migrate-check`, `make migrate-roundtrip`); `test` (`make test-integration TESTFLAGS=-count=1`); `goldens` (`make test` on amd64 with `GOAMD64=v3` and on arm64); `stack` (`make env dev-keys up smoke`, then seed smoke + verify once T16 lands); `etl-fixtures` (`make etl-fixtures-check` once T15 adds the fixtures); `build` (image with every binary, pushed to `ghcr.io/smartcode54-bit/logitrack-api:{sha}` only from `mv-go`, `:v{semver}` from an `api-v{semver}` tag on `mv-go`); `web-ci` (waits for the web `CI` run of the same commit, if its path filter started one); `go-ci` (sums them up) |
| `secret-scan` (`.github/workflows/secret-scan.yml`) | gitleaks v8.30.1 over the commits of the push or PR (`.github/scripts/secret-scan.sh BASE HEAD` runs it locally), findings redacted |
| `CI` (`.github/workflows/ci.yml`) | the web checks, now also for `mv-go` and `mv-go-**` (path filter and `main` entries unchanged) |

Branch protection on `mv-go` requires `go-ci` and `secret-scan` (owner setting). Every PostgreSQL image in compose, the workflows and the Go code is `postgres:18-alpine` (`pgtest.TestEveryPostgresImageIsTheSame`).
