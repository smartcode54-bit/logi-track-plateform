# logitrack-api developer entry points (developer-spec.md §15.4). Run from logitrack-api/.
# First run:  make env dev-keys up smoke
SHELL := /bin/bash
.SHELLFLAGS := -eo pipefail -c
.DEFAULT_GOAL := help

COMPOSE      := docker compose -f deploy/docker-compose.yml --env-file .env
PROFILES     := $(if $(EDGE),--profile edge,)
SEED_PROFILE ?= smoke
GOLANGCI     := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
GOVULNCHECK  := go run golang.org/x/vuln/cmd/govulncheck@latest
GITLEAKS_IMG := zricethezav/gitleaks:v8.30.1

.PHONY: help env dev-keys dev-db up down reset ps logs psql redis-cli amqp-ui minio-console tunnel \
        migrate migrate-new migrate-status migrate-down seed seed-verify etl-fixtures \
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
up: .env ## start the stack (EDGE=1 adds web + caddy from TW2); waits until every service is healthy
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

tunnel: ## expose only /public/v1/* through Cloudflare (TUNNEL_TOKEN; Caddy site :8090 arrives with TW2)
	$(COMPOSE) --profile tunnel up -d tunnel

## --- database and data -------------------------------------------------------------------
migrate: ## goose up (cmd/migrate lands in T03)
	$(COMPOSE) --profile tools run --rm migrate up

migrate-status: ## goose status
	$(COMPOSE) --profile tools run --rm migrate status

migrate-down: ## goose down one step
	$(COMPOSE) --profile tools run --rm migrate down

migrate-new: ## create a migration: make migrate-new NAME=...
	@test -n "$(NAME)" || { echo "NAME is required" >&2; exit 2; }
	$(COMPOSE) --profile tools run --rm migrate create $(NAME) sql

seed: ## seed a profile (SEED_PROFILE=smoke|demo|load; cmd/seed lands in T16)
	$(COMPOSE) --profile tools run --rm seed --profile $(SEED_PROFILE)

seed-verify: ## seed --verify invariants (T16)
	$(COMPOSE) --profile tools run --rm seed --verify

etl-fixtures: ## dry-run ETL over the fixtures against logitrack_test (T15)
	$(COMPOSE) --profile tools run --rm etl load --dry-run --fixtures

## --- code ---------------------------------------------------------------------------------
test: ## unit tests with the race detector
	go test -race ./...

test-integration: ## integration tests against the compose stack or testcontainers
	go test -race -tags=integration ./...

lint: ## golangci-lint (incl. staticcheck), go vet, govulncheck
	go vet ./...
	$(GOLANGCI) run ./...
	$(GOVULNCHECK) ./...

gen: ## regenerate generated files (RabbitMQ definitions; sqlc and OpenAPI join in T03 and later)
	go generate ./...

gen-check: gen ## fail when generated files are stale
	git diff --exit-code -- deploy/rabbitmq-definitions.json

sqlc: ## sqlc generate (T03)
	@echo "sqlc lands in issue T03" >&2; exit 3

openapi: ## OpenAPI -> web types and Dart models (R27; first routes in T05)
	@echo "the OpenAPI pipeline lands with the first routes (T05)" >&2; exit 3

web: ## run the Next.js dev server against the local api (internal listener)
	cd ../logitrack-web && GO_API_INTERNAL_URL=http://localhost:8080 pnpm --filter logi-track dev

## --- checks ---------------------------------------------------------------------------------
env-check: ## .env.example and compose environments match developer-spec.md §16
	docker compose -f deploy/docker-compose.yml --env-file .env.example --profile '*' config --format json \
	  | go run ./tools/envcheck -spec ../developer-spec.md -env .env.example -compose -

secrets-scan: ## gitleaks over the files git would commit here (.env and dev-secrets are ignored)
	@tmp=$$(mktemp -d); \
	git ls-files --cached --others --exclude-standard -z . | while IFS= read -r -d '' f; do \
	  mkdir -p "$$tmp/$$(dirname "$$f")" && cp "$$f" "$$tmp/$$f"; done; \
	docker run --rm -v "$$tmp:/scan:ro" $(GITLEAKS_IMG) dir /scan --no-banner --redact; rc=$$?; \
	rm -rf "$$tmp"; exit $$rc

smoke: .env ## verify the T02 acceptance checks against the running stack
	./deploy/smoke.sh
