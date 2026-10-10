# logitrack-api developer entry points (developer-spec.md §15.4). Run from logitrack-api/.
# First run:  make env dev-keys up smoke
SHELL := /bin/bash
.SHELLFLAGS := -eo pipefail -c
.DEFAULT_GOAL := help

COMPOSE      := docker compose -f deploy/docker-compose.yml --env-file .env
PROFILES     := $(if $(EDGE),--profile edge,)
SEED_PROFILE ?= smoke
# Tool versions are pinned here and nowhere else; go-ci runs these targets (developer-spec.md §17.2).
GOLANGCI     := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
GOVULNCHECK  := go run golang.org/x/vuln/cmd/govulncheck@v1.8.0
SQLC         := go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
# Extra `go test` flags, e.g. TESTFLAGS=-count=1 in CI so a cached result never stands in for a run.
TESTFLAGS    ?=
# testcontainers talks to the daemon of the active docker context (Docker Desktop, OrbStack, colima).
DOCKER_HOST  ?= $(shell docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null)
# The chain is embedded in the image, so every migrate run rebuilds it (cached when nothing changed).
MIGRATE      := $(COMPOSE) run --rm --build migrate
# sqlc output directories (the `out:` of each sqlc.yaml block).
SQLC_OUT      = $(shell awk '$$1 == "out:" {print $$2}' sqlc.yaml)
# Every generated file or directory that is committed; gen-check fails when one is stale or new.
GEN_OUT       = deploy/rabbitmq-definitions.json api/routes.txt $(SQLC_OUT)
GITLEAKS_IMG := zricethezav/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f

.PHONY: help env dev-keys dev-db up down reset ps logs psql redis-cli amqp-ui minio-console tunnel \
        migrate migrate-up-to migrate-new migrate-status migrate-down migrate-check migrate-roundtrip \
        seed seed-verify etl-fixtures etl-fixtures-check \
        test test-integration lint sqlc gen gen-check openapi web env-check secrets-scan smoke

help: ## list targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'

## --- local configuration --------------------------------------------------------------
env: ## create .env from .env.example with random local-only secrets (FORCE=1 overwrites)
	go run ./tools/devenv $(if $(FORCE),-force,)

dev-keys: ## create the local Ed25519 JWT key and set JWT_ACTIVE_KID in .env
	go run ./tools/devkeys

dev-db: .env ## set the LOGIN passwords of the R66 roles from the .env URLs (never printed)
	go run ./tools/devdb -env .env | $(COMPOSE) exec -T postgres sh -c 'psql -q -v ON_ERROR_STOP=1 -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"'
	@echo "dev-db: role passwords set"

.env:
	@echo "missing .env: run 'make env' first" >&2; exit 1

## --- stack -----------------------------------------------------------------------------
up: .env ## start the stack (EDGE=1 adds web + caddy, TW2); waits until every service is healthy
	$(COMPOSE) up -d --wait postgres
	$(MAKE) --no-print-directory dev-db
	$(COMPOSE) $(PROFILES) up -d --build --wait

down: ## stop the stack incl. profiled services, keep volumes
	$(COMPOSE) --profile '*' down

reset: ## drop all volumes and start again (seeding arrives with T16)
	$(COMPOSE) --profile '*' down -v
	$(MAKE) --no-print-directory up

ps: ## service status
	$(COMPOSE) --profile '*' ps

logs: ## follow logs (S=service)
	$(COMPOSE) logs -f --tail=200 $(S)

psql: ## psql as the superuser in the postgres container
	$(COMPOSE) exec postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"'

redis-cli: ## redis-cli in the redis container
	$(COMPOSE) exec redis redis-cli

amqp-ui: ## RabbitMQ management UI
	@echo "http://localhost:15672 (user RABBITMQ_DEFAULT_USER from .env)"

minio-console: ## MinIO console
	@echo "http://localhost:9001 (user MINIO_ROOT_USER from .env)"

tunnel: ## expose only /public/v1/* through Cloudflare (TUNNEL_TOKEN; via the Caddy site :8090)
	$(COMPOSE) --profile tunnel up -d tunnel

## --- database and data -------------------------------------------------------------------
migrate: ## apply every migration (make up runs this first; prod pins up-to 9 until the P1 runbook, R59)
	$(MIGRATE) up

migrate-up-to: ## apply up to a version: make migrate-up-to V=9
	@test -n "$(V)" || { echo "V is required" >&2; exit 2; }
	$(MIGRATE) up-to $(V)

migrate-status: ## list every migration and its state
	$(MIGRATE) status

migrate-down: ## roll back the latest migration (refused when APP_ENV=prod)
	$(MIGRATE) down

migrate-new: ## write the next migration file on the host: make migrate-new NAME=add_x
	@test -n "$(NAME)" || { echo "NAME is required" >&2; exit 2; }
	go run ./cmd/migrate create -dir migrations $(NAME)

migrate-check: ## lint migrations/ (R31): numbering, Up and Down, NO TRANSACTION, uuidv7
	go run ./cmd/migrate check -dir migrations

migrate-roundtrip: ## up -> down-to floor -> up on postgres:18-alpine via testcontainers (R31)
	DOCKER_HOST=$(DOCKER_HOST) go test -race -count=1 -tags=integration -run 'RoundTrip|UpToNine' ./internal/platform/migrate/ ./cmd/migrate/

seed: ## seed a profile (SEED_PROFILE=smoke|demo|load; cmd/seed lands in T16)
	$(COMPOSE) --profile tools run --rm seed --profile $(SEED_PROFILE)

seed-verify: ## seed --verify invariants (T16)
	$(COMPOSE) --profile tools run --rm seed --verify

etl-fixtures: ## dry-run ETL over the fixtures against logitrack_test (T15)
	$(COMPOSE) --profile tools run --rm etl load --dry-run --fixtures

etl-fixtures-check: ## go-ci etl-fixtures: integration tests of cmd/etl over cmd/etl/testdata/firestore-fixtures (T15)
	DOCKER_HOST=$(DOCKER_HOST) go test -race -count=1 -tags=integration $(TESTFLAGS) ./cmd/etl/...

## --- code ---------------------------------------------------------------------------------
test: ## unit tests with the race detector
	go test -race $(TESTFLAGS) ./...

test-integration: ## unit + integration tests (testcontainers: postgres:18-alpine with 00-roles.sql)
	DOCKER_HOST=$(DOCKER_HOST) go test -race -tags=integration $(TESTFLAGS) ./...

lint: ## gofmt, go mod tidy, go vet, golangci-lint (incl. staticcheck), govulncheck; integration-tagged files included
	@unformatted=$$(gofmt -l .); if [ -n "$$unformatted" ]; then echo "gofmt -w needed:" >&2; echo "$$unformatted" >&2; exit 1; fi
	go mod tidy -diff
	go vet -tags=integration ./...
	$(GOLANGCI) run --build-tags=integration ./...
	$(GOVULNCHECK) -tags=integration ./...

gen: ## regenerate generated files (RabbitMQ definitions, api/routes.txt, sqlc; OpenAPI joins with T05)
	go generate ./...
	$(SQLC) generate

gen-check: ## fail when generated files are stale, orphaned or uncommitted, a public route leaves the allow-list, or the migration rules break (go-ci gen-check)
	$(SQLC) diff
	$(SQLC) vet
	go run ./cmd/migrate check -dir migrations
	@# go generate also runs `api routes`, which exits 1 on a public route outside the allow-list.
	go generate ./...
	@# A query file that was removed leaves its .sql.go behind; regenerate from scratch to expose it.
	rm -f $(addsuffix /*.sql.go,$(SQLC_OUT))
	$(SQLC) generate
	git diff --exit-code -- $(GEN_OUT)
	@new=$$(git ls-files --others --exclude-standard -- $(GEN_OUT)); \
	  if [ -n "$$new" ]; then echo "generated but not committed:" >&2; echo "$$new" >&2; exit 1; fi

sqlc: ## sqlc generate (schema = migrations/, queries next to their package)
	$(SQLC) generate

openapi: ## OpenAPI -> web types and Dart models (R27; first routes in T05)
	@echo "the OpenAPI pipeline lands with the first routes (T05)" >&2; exit 3

web: ## run the Next.js dev server against the local api (internal listener)
	cd ../logitrack-web && GO_API_INTERNAL_URL=http://localhost:8080 pnpm --filter logi-track dev

## --- checks ---------------------------------------------------------------------------------
env-check: ## .env.example and compose environments match developer-spec.md §16
	docker compose -f deploy/docker-compose.yml --env-file .env.example --profile '*' config --format json \
	  | go run ./tools/envcheck -spec ../developer-spec.md -env .env.example -compose - -compose-file deploy/docker-compose.yml

secrets-scan: ## gitleaks over the files git would commit here (.env and dev-secrets are ignored)
	@tmp=$$(mktemp -d); \
	git ls-files --cached --others --exclude-standard -z . | while IFS= read -r -d '' f; do \
	  mkdir -p "$$tmp/$$(dirname "$$f")" && cp "$$f" "$$tmp/$$f"; done; \
	docker run --rm -v "$$tmp:/scan:ro" $(GITLEAKS_IMG) dir /scan --no-banner --redact; rc=$$?; \
	rm -rf "$$tmp"; exit $$rc

smoke: .env ## verify the T02 and T03 acceptance checks against the running stack (EDGE=1 adds the TW2 edge checks)
	./deploy/smoke.sh
	$(if $(EDGE),./deploy/edge-smoke.sh)
