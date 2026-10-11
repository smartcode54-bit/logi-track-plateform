# Staging VM kit: showkhun.co (pm2 + nginx)

Deploys the mv-go stack to the shared VM `showkhun.co` (Ubuntu 24.04), as the owner decided on 2026-10-10
(issue OPSVM; developer-spec.md §15.8, §17.4, §19 Q8; ADR 0029 notes):

| Host | Serves |
|---|---|
| `https://logi.showkhun.co` | the web (Next.js standalone + BFF, `127.0.0.1:3300`), and `/media/*` (local storage objects, the api's public listener `127.0.0.1:3302`, path unchanged) |
| `https://logi-api.showkhun.co` | the api's public listener only (`127.0.0.1:3302`): `/v1/mobile`, `/v1/auth`, `/public/v1`, `/evidence`, `/media`, `/healthz`; anything else is the Go 404 envelope |

Nothing proxies to the api's internal listener (`127.0.0.1:3301`); only the BFF calls it. TLS is the Cloudflare
origin certificate already on the VM (`/etc/ssl/certs/showkhun.co.pem`, `/etc/ssl/private/showkhun.co.key`, SAN
`*.showkhun.co`); DNS for both names is proxied by Cloudflare, and nginx restores the client address from
`CF-Connecting-IP` for Cloudflare's published ranges.

The VM is shared with other apps (plodpai, lawforwork, wordpress, n8n, gitlab, grafana, ...). The kit touches only
what is LogiTrack's: the `logitrack` user, `/opt/logitrack`, the `logitrack` database and the five R66 roles, the
containers `logitrack-redis` / `logitrack-rabbitmq` and their volumes, the vhosts `logi.showkhun.co` and
`logi-api.showkhun.co` with three `logitrack-*` nginx snippets, and the `pm2-logitrack` unit. It refuses to
overwrite a file it did not write and stops when another vhost already claims one of its host names.

Agents never connect to the VM: the operator runs every command below.

## What runs where

| Piece | Where | Notes |
|---|---|---|
| api | pm2 `logitrack-api`, internal `127.0.0.1:3301`, public `127.0.0.1:3302`, metrics `127.0.0.1:3303` | `APP_ENV=dev` |
| worker | pm2 `logitrack-worker`, metrics `127.0.0.1:3304` | |
| scheduler | pm2 `logitrack-scheduler`, metrics `127.0.0.1:3305` | |
| web | pm2 `logitrack-web`, `127.0.0.1:3300` | `node server.js` of the standalone build; `GO_API_INTERNAL_URL=http://127.0.0.1:3301` |
| PostgreSQL 18 | the server's own cluster, `127.0.0.1:5432`, database `logitrack` | roles `logitrack_app`, `_migrator`, `_etl`, `_readonly`, `_rls_definer` (cluster-wide, R66); `CONNECT` revoked from `PUBLIC` |
| Redis 7 | container `logitrack-redis`, `127.0.0.1:6381` | AOF, `noeviction`, `requirepass`; the server's Redis on 6379 belongs to other apps |
| RabbitMQ 4 | container `logitrack-rabbitmq`, `127.0.0.1:5674`, management `127.0.0.1:15674` | fixed hostname, quorum queues from `deploy/rabbitmq-definitions.json` |
| Files | `/opt/logitrack/shared/media` | `STORAGE_BACKEND=local` until S3-compatible settings arrive (§9.11) |
| pm2 | `/opt/node-v24.14.0` (the node of the other pm2 apps), `PM2_HOME=/home/logitrack/.pm2`, unit `pm2-logitrack` | `pm2-logrotate` for that user |

Tree on the VM:

```
/opt/logitrack/
  releases/<git-sha>/   bin/ (Go, linux/amd64), web/ (standalone), deploy/vm/ (this kit), REVISION
  current -> releases/<git-sha>
  shared/.env           0600 logitrack: every setting and secret (from env.vm.example)
  shared/secrets/       0700 logitrack: jwt-ed25519.pem, and the optional firebase-sa.json, fcm-sa.json
  shared/media/         local storage objects
  shared/logs/          pm2 logs (rotated by pm2-logrotate)
  shared/releases.log   releases that went live, oldest first
```

## Files of the kit

| File | Role |
|---|---|
| `kit.conf` | the user, the tree, the ports, the host names and the pinned images; sourced by the scripts, read by `ecosystem.config.cjs` |
| `env.vm.example` | names-only template of `shared/.env` (secrets blank; checked by `make env-check` against §16.1) |
| `env-names/<program>.txt` | the exact §16.1 names each program receives (checked by `make env-check`) |
| `provision.sh` | one-time, idempotent set-up as root (`--dry-run` prints the plan) |
| `deploy.sh` | build and ship from the operator's machine (`--dry-run`, `--rollback`, `--status`, `--provision`, `--kit`) |
| `rollout.sh` | the VM side of a deploy: migrate, first-deploy seed, switch, pm2, checks, rollback, pruning; `rollout.sh pm2 ...` |
| `run-app.sh` | starts a program with its names from `shared/.env`, read at exec time |
| `ecosystem.config.cjs` | the four pm2 apps and their per-process listener settings |
| `nginx/` | the two vhosts, the Cloudflare real-IP and proxy-header snippets, the access-log format |
| `pm2-logitrack.service` | the systemd unit of the user's pm2 daemon |

Secrets never leave the VM: `provision.sh` generates them there with `bin/vmenv` (`tools/vmenv`): the three role
passwords (inside the database URLs; PostgreSQL receives SCRAM verifiers on psql's stdin), the Redis and RabbitMQ
passwords, `LOCAL_MEDIA_SIGNING_KEY`, `API_KEY_PEPPER`, `BOOTSTRAP_ADMIN_PASSWORD`, `SEED_DEFAULT_PASSWORD`, and
the Ed25519 JWT key with `JWT_ACTIVE_KID` = its RFC 7638 thumbprint (as the api checks it). No script prints a
value or passes one on a command line; pm2 never holds one (`run-app.sh` reads the `.env` when a program
starts, and pm2 is always called with a clean environment), so neither `pm2 show` nor `pm2 save`'s dump carries
a secret. `deploy/vm/kit_test.go` runs the scripts against a temporary tree with stub commands and fails on any
generated value in their output or in a process argument.

## Operator prerequisites

- `ssh showkhun.co` logs in as root with a key (deploy.sh uses `BatchMode`; `--host` picks another ssh name).
- On the operator's machine: git, Go 1.27, Docker with buildx (linux/amd64 builds; on Apple silicon through the
  Docker Desktop or OrbStack emulation), curl. Run the scripts from a checkout of `mv-go`.
- The web's public build values in a gitignored file, e.g. `logitrack-api/deploy/vm/.env.web-build`
  (`.env.*` is gitignored there): the `NEXT_PUBLIC_*` names of `logitrack-web/Dockerfile`, with the six
  `NEXT_PUBLIC_FIREBASE_*` values of the dev Firebase project's web app (`next build` needs them until TW7) and
  optionally `NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID`. These end up in the browser bundle, so only public values
  belong there; deploy.sh refuses any other name and never prints a value.

## 1. Provision (once; safe to repeat)

From the operator's machine, to see the plan without changing anything on the server:

```sh
logitrack-api/deploy/vm/deploy.sh --provision --plan     # uploads the kit to a temp dir, runs provision.sh --dry-run, removes it
```

Then for real:

```sh
logitrack-api/deploy/vm/deploy.sh --provision
```

Without ssh from this machine: `deploy.sh --kit /tmp/logitrack-kit`, copy that directory to the VM, and run
`bash /path/to/kit/deploy/vm/provision.sh --dry-run`, then without `--dry-run`, as root.

`provision.sh` does, each step only when needed:

1. preflight: root, PostgreSQL 18, docker, nginx with `sites-enabled`, `/opt/node-v24.14.0/bin/{node,pm2}`, the
   origin certificate and key;
2. the `logitrack` system user and `/opt/logitrack/{releases,shared/{secrets,media,logs}}`;
3. `/opt/logitrack/shared/.env` from `env.vm.example` (0600, owner logitrack) with every secret generated on the
   server, and the JWT key; on a re-run every existing value is kept and only new template names are added;
4. database `logitrack`, `deploy/postgres-init/00-roles.sql` run inside it (the roles are cluster-wide; no other
   role or database is touched), the migrator as owner, `CONNECT` for the LogiTrack roles only, and the three
   LOGIN passwords as SCRAM verifiers;
5. `logitrack-redis` (redis 7.4.11, pinned by digest) and `logitrack-rabbitmq` (rabbitmq 4.3.6 management, pinned
   by digest) with `--restart unless-stopped` and named volumes, then the RabbitMQ definitions through the
   management API (credentials on curl's stdin);
6. the nginx files, `nginx -t` (on failure the previous files come back and nginx is not reloaded), then
   `systemctl reload nginx`;
7. the `pm2-logitrack` unit (enabled and started) and `pm2-logrotate` (50 MB, 14 files, compressed).

Check the server's `pg_hba.conf` allows `scram-sha-256` from `127.0.0.1/32` (Ubuntu's default does); the first
deploy's `migrate up` fails otherwise.

## 2. Firebase and FCM files (optional)

Until they exist the api runs with `AUTH_FIREBASE_BRIDGE_MODE=off` and the worker with `FCM_ENABLED=false`. The
project ids (`logi-track-wrt-dev`) are already in the `.env`. As root on the VM:

```sh
# the bridge (custom tokens and the account mirror of the dev project)
install -o logitrack -g logitrack -m 0400 firebase-sa.json /opt/logitrack/shared/secrets/firebase-sa.json
# push notifications (messaging-only service account)
install -o logitrack -g logitrack -m 0400 fcm-sa.json /opt/logitrack/shared/secrets/fcm-sa.json
```

Then edit `/opt/logitrack/shared/.env`: `AUTH_FIREBASE_BRIDGE_MODE=web` (the paths in
`GOOGLE_APPLICATION_CREDENTIALS` and `FCM_SERVICE_ACCOUNT_JSON` are already set) and `FCM_ENABLED=true`, and reload
the apps that read them (they read the `.env` again when they start):

```sh
bash /opt/logitrack/current/deploy/vm/rollout.sh pm2 reload logitrack-api logitrack-worker
```

The service account must belong to `FIREBASE_PROJECT_ID`. If the dev project enforces App Check with the reCAPTCHA
key, add `logi.showkhun.co` to that key's domains.

## 3. First deploy

```sh
logitrack-api/deploy/vm/deploy.sh --web-build-env logitrack-api/deploy/vm/.env.web-build --dry-run   # the plan
logitrack-api/deploy/vm/deploy.sh --web-build-env logitrack-api/deploy/vm/.env.web-build
```

deploy.sh builds `bin/` (`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`, every `cmd/` binary plus `vmenv`), builds the web
through `logitrack-web/Dockerfile` (`docker buildx --platform linux/amd64 --target standalone`, with a glibc
`node:24.14.0-bookworm-slim` base, because the standalone output runs on the VM's node rather than in the alpine
image) and uploads the release to `/opt/logitrack/releases/<git-sha>`. A checkout with uncommitted changes is
refused unless `--allow-dirty` (the release id then gets a `-dirty-<time>` suffix). Then on the VM
(`rollout.sh activate`):

1. `migrate up` with the new binary (a fresh staging database: the whole chain, 0010 and 0011+ included);
2. first deploy only (no `shared/.first-deploy-done` yet): `seed --profile demo --allow-shared` (the multi-tenant
   demo profile; `APP_ENV=dev` loads in upsert mode) and `seed --bootstrap-admin` (`ssadmin@showkhun.co`, also in
   `PLATFORM_ADMIN_EMAILS`, so platform admin);
3. `current` switches to the release, `pm2 startOrReload ecosystem.config.cjs`, `pm2 save`;
4. checks on the VM: the internal `/readyz` (3301), the worker's and scheduler's `/readyz` (3304, 3305), the public
   `/healthz` (3302) = 200, the public `/v1/tasks` = 404, the web `/login` = 200;
5. checks from the operator's machine: `https://logi-api.showkhun.co/healthz` = 200,
   `https://logi.showkhun.co/login` = 200, `https://logi-api.showkhun.co/v1/tasks` = 404;
6. the newest three releases are kept.

A failed check switches `current` back to the previous release (none on the very first deploy: fix and deploy
again; the seed is idempotent and runs again until a first deploy succeeds).

Sign in at `https://logi.showkhun.co/login` as `ssadmin@showkhun.co`. Demo users sign in with
`SEED_DEFAULT_PASSWORD`.

## 4. Later deploys

The same command from a newer commit of `mv-go`: no seed, `migrate up` first (expand/contract: the running release
keeps serving on the new schema), then the switch and the checks. Redeploying the commit that is already live is
refused; to apply an edited `.env`, reload instead (`rollout.sh pm2 reload <app>`). `deploy.sh --status` shows the
current release, the kept ones and the last activations.

## 5. Rollback

```sh
logitrack-api/deploy/vm/deploy.sh --rollback              # to the release that was live before the current one
logitrack-api/deploy/vm/deploy.sh --rollback --to <sha>   # to a kept release
```

Rollback switches `current` and reloads pm2; no migration runs backwards. A release that failed its checks never
counts as "live", so a rollback never returns to it.

## 6. Where the admin password is

Only in the VM's `.env`, never printed by any script:

```sh
grep BOOTSTRAP_ADMIN_PASSWORD /opt/logitrack/shared/.env     # as root
```

To change it, edit that value (at least `PASSWORD_MIN_LENGTH` characters) and run the bootstrap again as root: the
same password changes nothing, a new one is set and revokes that user's sessions.

```sh
runuser -u logitrack -- env -i HOME=/home/logitrack PATH=/usr/bin:/bin /opt/logitrack/current/deploy/vm/run-app.sh seed --bootstrap-admin
```

## 7. Backups

Nightly, off the VM (owner's storage), and before risky changes:

```sh
# database (PostgreSQL 18 client on the server), custom format
runuser -u postgres -- pg_dump -Fc logitrack > /var/backups/logitrack-$(date +%F).dump
# local storage objects
tar -C /opt/logitrack/shared -czf /var/backups/logitrack-media-$(date +%F).tgz media
# the .env and the secrets (they hold every credential: keep the copy as private as the VM's own)
tar -C /opt/logitrack/shared -czf /var/backups/logitrack-secrets-$(date +%F).tgz .env secrets
```

Restore into an empty database owned by the migrator: `runuser -u postgres -- pg_restore -d logitrack --no-owner
--role=logitrack_migrator <dump>`, then reload the four apps (`rollout.sh pm2 reload all`). Redis and RabbitMQ are not
backed up: Redis holds caches and hot copies (idempotency keys are also durable rows in PostgreSQL), and RabbitMQ
holds work in flight; inspect its dead-letter queues before a restore if they matter.

## Operations

- Logs: `/opt/logitrack/shared/logs/<program>.{out,err}.log`; `rollout.sh pm2 logs logitrack-api --lines 200`;
  nginx `/var/log/nginx/logitrack-{web,api}.{access,error}.log` (the access log drops query strings, so signed
  media URLs and reset tokens are not logged; evidence paths are not logged at all).
- Settings: edit `/opt/logitrack/shared/.env` as root, then `rollout.sh pm2 reload <app>`. Raising
  `UPLOAD_MAX_BYTES` needs the same `client_max_body_size` in both vhosts (`nginx/`).
- Web domain flags: P0 state, `WEB_FLAG_OVERRIDES=auth=go` with `PG_OWNED_DOMAINS` empty (every other domain on
  the dev Firebase project); change them in the `.env` and reload `logitrack-api` (no web rebuild).
- Cloudflare ranges: `nginx/logitrack-cloudflare-realip.conf` holds the list of
  https://www.cloudflare.com/ips/; update it there and run `deploy.sh --provision`.
- Switching storage to S3 later: fill `S3_*`, set `STORAGE_BACKEND=s3`, keep `LOCAL_MEDIA_*` while local rows
  exist (§9.11), reload api and worker.
- Removing the deployment (only on the owner's request): `systemctl disable --now pm2-logitrack`,
  `docker rm -f logitrack-redis logitrack-rabbitmq` and their volumes, the two vhosts, three snippets and the unit,
  `DROP DATABASE logitrack` and then the five `logitrack_*` roles, `/opt/logitrack` and the `logitrack` user.

## Tests

`go test ./deploy/vm/ ./tools/vmenv/` (part of `make test`): `bash -n`, the template against the real configuration
of api, worker, scheduler, migrate and seed (`STORAGE_BACKEND=local`, empty `S3_*`), `ecosystem.config.cjs` loaded in
node, every script run against a temporary tree with stub commands (dry runs change nothing; no generated secret
in any output or process argument; a second provision changes nothing; deploy, rollback and pruning). With
`-tags=integration` (`make test-integration`, or `make vm-check`): `nginx -t` and the routing of both vhosts under
`nginx:1.24` with a dummy certificate at the real paths, and shellcheck over every script.
