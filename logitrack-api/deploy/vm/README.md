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
  shared/.env           0640 root:logitrack: every setting and secret (from env.vm.example)
  shared/secrets/       0750 root:logitrack: jwt-ed25519.pem (0640), and the optional firebase-sa.json, fcm-sa.json
  shared/media/         local storage objects (0750 logitrack)
  shared/logs/          pm2 logs (0750 logitrack, rotated by pm2-logrotate)
  shared/releases.log   releases that passed every check, oldest first
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
| `run-app.sh` | starts a program with its names from `shared/.env`, read at exec time (also `release`, section 8) |
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

The `.env`, `secrets/` and the key belong to root with group `logitrack`: the apps read them, but a process
running as `logitrack` cannot rewrite the settings or plant a file where `provision.sh` (root) writes one.

**Isolation between the programs is by environment only.** `run-app.sh` gives each program exactly its §16.1
names, which keeps the values out of pm2's list and dump and out of the other programs' environments. It is not a
security boundary: the four pm2 apps, migrate and seed all run as the one `logitrack` user, so each of them,
the Next.js web included, can read `shared/.env` (the `logitrack_etl` BYPASSRLS and `logitrack_migrator` URLs,
`BOOTSTRAP_ADMIN_PASSWORD`), the JWT signing key, and `/proc/<pid>/environ` of the others. A file-read or
code-execution bug in the web or the api therefore reaches every credential, which the compose deployment's
per-container split (§16.5 rules 2, 3 and 9, R66, R87) prevents. This is the single-user layout of the staging
VM (issue OPSVM) and a recorded deviation (developer-spec.md §15.8); the hardening that would close it (a
separate `logitrack-web` user with its own pm2 daemon and a names-only `web.env`, the migrator and ETL URLs in a
root-only file handed to migrate and seed per run) is listed for the owner on the OPSVM pull request.

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
3. `/opt/logitrack/shared/.env` from `env.vm.example` (0640 root:logitrack) with every secret generated on the
   server, and the JWT key (0640 root:logitrack, only ever inside `shared/secrets/`); on a re-run every existing
   value is kept and only new template names are added;
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
install -o root -g logitrack -m 0440 firebase-sa.json /opt/logitrack/shared/secrets/firebase-sa.json
# push notifications (messaging-only service account)
install -o root -g logitrack -m 0440 fcm-sa.json /opt/logitrack/shared/secrets/fcm-sa.json
```

Then edit `/opt/logitrack/shared/.env`: `AUTH_FIREBASE_BRIDGE_MODE=web` (the paths in
`GOOGLE_APPLICATION_CREDENTIALS` and `FCM_SERVICE_ACCOUNT_JSON` are already set) and `FCM_ENABLED=true`, and reload
the apps that read them (they read the `.env` again when they start; the api serves the bridge, the worker sends
the pushes):

```sh
bash /opt/logitrack/current/deploy/vm/rollout.sh pm2 reload logitrack-api logitrack-worker
```

pm2's own `reload` takes one name or `all` and silently drops a second name; `rollout.sh pm2 reload` with several
names reloads each of them in turn (`pm2 ls` then shows a fresh uptime for both).

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
image) and uploads the release to `/opt/logitrack/releases/<git-sha>`. Both builds run on a clean export of the
commit (`git archive` of `logitrack-api`, `logitrack-web`, `shared-docs` and the workspace manifests), so the
release is exactly the commit it is named after: no ignored or untracked file of the checkout reaches it. A
checkout with uncommitted changes or untracked files under those paths is refused unless `--allow-dirty`, which
builds the working tree as it is, untracked files included, under the id `<git-sha>-dirty-<time>`; changes
elsewhere (e.g. `.claude/`) do not count. Then on the VM (`rollout.sh activate --pending`):

1. `migrate up` with the new binary (a fresh staging database: the whole chain, 0010 and 0011+ included);
2. until a first deploy has passed every check (no `shared/.first-deploy-done` yet):
   `seed --profile demo --allow-shared` (the multi-tenant demo profile; `APP_ENV=dev` loads in upsert mode) and
   `seed --bootstrap-admin` (`ssadmin@showkhun.co`, also in `PLATFORM_ADMIN_EMAILS`, so platform admin);
3. `current` switches to the release, `pm2 startOrReload ecosystem.config.cjs`, `pm2 save`;
4. checks on the VM: the internal `/readyz` (3301), the worker's and scheduler's `/readyz` (3304, 3305), the public
   `/healthz` (3302) = 200, the public `/v1/tasks` = 404, the web `/login` = 200;
5. checks from the operator's machine: `https://logi-api.showkhun.co/healthz` = 200,
   `https://logi.showkhun.co/login` = 200, `https://logi-api.showkhun.co/v1/tasks` = 404;
6. `rollout.sh confirm <id>`: the release is recorded as live in `shared/releases.log` (the first one also writes
   `shared/.first-deploy-done`), and the newest three releases are kept.

A failed check switches `current` back to the newest release that passed every check; the failed release is never
recorded, so neither a later rollback nor pruning counts it. On the very first deploy there is nothing to switch
back to: fix and deploy again. The seed and the bootstrap run again on every deploy until one passes every check;
both are idempotent (upsert, and an unchanged password changes nothing). To seed a database that was recreated
after the first deploy went live, delete `/opt/logitrack/shared/.first-deploy-done` before the next deploy, or run
the bootstrap by hand (section 6).

Sign in at `https://logi.showkhun.co/login` as `ssadmin@showkhun.co`. Demo users sign in with
`SEED_DEFAULT_PASSWORD`.

## 4. Later deploys

The same command from a newer commit of `mv-go`: no seed, `migrate up` first (expand/contract: the running release
keeps serving on the new schema), then the switch and the checks. Redeploying the commit that is already live is
refused; to apply an edited `.env`, reload instead (`rollout.sh pm2 reload <app>`). `deploy.sh --status` shows the
current release, the kept ones and the last activations, and says so when the current release was never
recorded as live (a deploy interrupted between the on-VM checks and `confirm`).

## 5. Rollback

```sh
logitrack-api/deploy/vm/deploy.sh --rollback              # to the release that was live before the current one
logitrack-api/deploy/vm/deploy.sh --rollback --to <sha>   # to a kept release
```

Rollback switches `current` and reloads pm2; no migration runs backwards. Only a release that passed every check,
the public ones from the operator's machine included, is recorded as live (`deploy.sh` activates with `--pending`
and confirms after the public checks), so a rollback never returns to a release that failed one; `--to` picks any
kept release explicitly.

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

Nightly, off the VM (owner's storage), and before risky changes. As root, into a directory only root can enter:
the dump holds every tenant's data and the password hashes, the media archive the private photos, and the secrets
archive every credential, the JWT signing key included. `umask 077` makes every file 0600 (root's default umask
would leave them readable by every account of this shared VM).

```sh
install -d -m 0700 -o root -g root /var/backups/logitrack
( umask 077
  d=/var/backups/logitrack; t=$(date +%F)
  # database (PostgreSQL 18 client on the server), custom format
  runuser -u postgres -- pg_dump -Fc logitrack > "$d/logitrack-$t.dump"
  # local storage objects
  tar -C /opt/logitrack/shared -czf "$d/logitrack-media-$t.tgz" media
  # the .env and the secrets
  tar -C /opt/logitrack/shared -czf "$d/logitrack-secrets-$t.tgz" .env secrets )
```

Encrypt the secrets archive to the owner's key before it leaves the VM (e.g. `age -r <owner's public key>` or
`gpg -e -r <owner>`), keep every copy off the VM as private (0600), and prune the local copies
(`find /var/backups/logitrack -type f -mtime +7 -delete`).

Restore as the superuser and keep the owners the dump records. The five R66 roles are cluster-wide and still
exist, and a dump made without `-C` carries neither the database's owner nor its `CONNECT` grants, so the database
is created again the way `provision.sh` creates it. Never pass `--no-owner` or `--role` to `pg_restore`: the
SECURITY DEFINER helpers (task and invoice numbering, the scope helpers of the RLS policies, the driver-link
trigger) must stay owned by `logitrack_rls_definer` (BYPASSRLS). Owned by the migrator they run under RLS, and
numbering fails with `new row violates row-level security policy` (42501) although the restore itself succeeds.

```sh
d=/var/backups/logitrack; t=2026-10-11          # the dump to restore
bash /opt/logitrack/current/deploy/vm/rollout.sh pm2 stop all
runuser -u postgres -- psql -X -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS logitrack" \
  -c "CREATE DATABASE logitrack TEMPLATE template0 ENCODING 'UTF8'" \
  -c "ALTER DATABASE logitrack OWNER TO logitrack_migrator" \
  -c "REVOKE CONNECT ON DATABASE logitrack FROM PUBLIC" \
  -c "GRANT CONNECT ON DATABASE logitrack TO logitrack_app, logitrack_etl, logitrack_readonly"
runuser -u postgres -- pg_restore --single-transaction -d logitrack < "$d/logitrack-$t.dump"
# two equal numbers, not 0 (8|8 at migration 0011): every SECURITY DEFINER helper is logitrack_rls_definer's
runuser -u postgres -- psql -X -d logitrack -tAc "SELECT count(*) FILTER (WHERE proowner = 'logitrack_rls_definer'::regrole), count(*) FROM pg_proc WHERE prosecdef AND pronamespace = 'public'::regnamespace"
bash /opt/logitrack/current/deploy/vm/rollout.sh pm2 restart all
```

The dump replaces the database: take a dump of the current one first if it may still matter. Media come back
with `tar -C /opt/logitrack/shared -xzf <media archive>`. On a rebuilt VM, put `.env` and `secrets/` back
(`tar -C /opt/logitrack/shared -xzf <secrets archive>`) before `deploy.sh --provision`, which keeps their values
and sets the role passwords from them. Redis and RabbitMQ are not backed up: Redis holds caches and hot copies
(idempotency keys are also durable rows in PostgreSQL), and RabbitMQ holds work in flight; inspect its
dead-letter queues before a restore if they matter.

## 8. Publishing a driver-app APK

`bin/release` (cmd/release, T52) replaces `logitrack-web/scripts/publish-mobile-release.mjs`. It runs here, on the
private network: it reads only `GO_API_INTERNAL_URL` (`run-app.sh` gives it the internal listener of `kit.conf`)
and `RELEASE_API_KEY`, uploads the APK to the api's internal listener (`STORAGE_BACKEND=local`: the file lands
under `shared/media/public/app_releases/`), checks that `https://logi.showkhun.co/media/app_releases/...` serves
it, and records the release. **Publishing never changes the version floor** (ADR 0007): a person raises it on the
Security Center's mobile-release page (`PUT /v1/app-releases/floor`, which refuses API keys).

Once: a platform admin creates the key, `{"name": "release-vm", "scope": "release_publisher", "capabilities":
["security:manage_mobile_release"]}` posted to `POST /v1/api-keys` (the Security Center's API-keys page, or that
call from the admin's signed-in browser through the BFF, `/api/go/v1/api-keys`). The secret is shown once: put it
in the `.env` as root (`RELEASE_API_KEY=`, an editor, never a command line). Revoking the key (`DELETE
/v1/api-keys/{id}`) or rotating `API_KEY_PEPPER` stops publishing until a new key is set.

Each release (the APK readable by the `logitrack` user, e.g. copied to `/tmp`): `release status` shows the gate
this deployment serves, a dry run hashes the APK and prints what the publish would do to that gate (it exits 1
with the reason and the next step when the api would refuse the publish), then the publish.

```sh
runuser -u logitrack -- env -i HOME=/home/logitrack PATH=/usr/bin:/bin /opt/logitrack/current/deploy/vm/run-app.sh release status
runuser -u logitrack -- env -i HOME=/home/logitrack PATH=/usr/bin:/bin /opt/logitrack/current/deploy/vm/run-app.sh release publish --flavor=dev --apk=/tmp/app-dev-release.apk --version=3.6.0 --build-number=1 --switch-flavor --dry-run
runuser -u logitrack -- env -i HOME=/home/logitrack PATH=/usr/bin:/bin /opt/logitrack/current/deploy/vm/run-app.sh release publish --flavor=dev --apk=/tmp/app-dev-release.apk --version=3.6.0 --build-number=1 --switch-flavor --notes="..."
```

The deployment serves one flavor (the first publish sets it). Publishing the other one is refused
(`flavor_mismatch`) unless `--switch-flavor`, and a switch is refused while a version floor is set (`floor_set`):
the floor applies to the served flavor only, so the switch would put it on the other fleet without anyone
choosing to. **The demo seed serves `prod` with the floor 3.4.0**, so the first `dev` publish above takes two
steps: a platform admin removes the floor (`PUT /v1/app-releases/floor` with `{"minAllowedVersion": null}`, from
the signed-in browser through the BFF at `/api/go/v1/app-releases/floor`, as for the key; the mobile-release page
does it once it reads Go, T54), then the publish with `--switch-flavor` (once: later `dev` publishes drop it). Set
a floor for the `dev` fleet afterwards, when it is ready. A publish never writes the floor; it is also refused when
the new version would become the latest below the floor (`floor_above_version`). `release status --flavor=prod`
on a `dev` deployment shows the empty gate `prod` devices see and names the flavor served.

With the Gradle `output-metadata.json` next to the APK, `--metadata=FILE` replaces `--apk`, `--version` and
`--build-number` and refuses a split-per-ABI build, another version and the other flavor's applicationId. A
published version is immutable (bump the version); `--force` only replaces an upload that was never published.
If a publish is cut off before it answers (an SSH drop), rerun the same command: a build that was published
already answers "Already published" without uploading again. Until the coexistence projection of
`settings/mobile_app` exists (T24), installed 3.x APKs keep reading their floor and link from Firestore; this
publish reaches `GET /v1/mobile/settings` only.

## Operations

- Logs: `/opt/logitrack/shared/logs/<program>.{out,err}.log`; `rollout.sh pm2 logs logitrack-api --lines 200`;
  nginx `/var/log/nginx/logitrack-{web,api}.{access,error}.log`. The access log drops query strings (signed
  media URLs, SSE tickets), and evidence requests appear there as `/evidence/-` with their status only. The error
  log writes the full request line, query included, when an upstream fails (connection refused while pm2
  restarts an app, a timeout, a 413), so the locations whose URI carries a credential (`/evidence`, `/media` and
  `/v1/mobile/events` on logi-api; `/media/` and the BFF's `/api/go/v1/uploads/local/` on logi) log errors at
  `crit` only: their failures show as 502, 504 or 413 in the access log and in the apps' own logs. Other upstream
  errors keep their full request line. (Password-reset tokens travel in the URL fragment and never reach nginx.)
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
in any output or process argument; a second provision changes nothing; a JWT key path outside `shared/secrets/`
is refused; deploy from a clean export, refusal of untracked files, rollback, the public-check failure path,
the first-deploy seed, pruning; the section 2 reload; the backup commands' modes). With `-tags=integration`
(`make test-integration`, or `make vm-check`): `nginx -t`, the routing of both vhosts and the error logs of an
unreachable upstream (no credential in them) under `nginx:1.24` with a dummy certificate at the real paths,
shellcheck over every script, and section 7's restore run as written on PostgreSQL 18 (definer owners kept,
`next_task_seq` works as `logitrack_app`).
