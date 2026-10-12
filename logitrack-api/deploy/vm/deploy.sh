#!/usr/bin/env bash
# deploy.sh: build LogiTrack and ship it to the staging VM showkhun.co (deploy/vm/README.md, developer-spec.md
# §17.4). Run on the operator's machine from a checkout of mv-go; it reaches the VM over ssh as root
# (ssh config host showkhun.co) and never prints a secret.
#
#   deploy.sh --web-build-env FILE [--reseed] [--dry-run] [--allow-dirty] [--host HOST]
#       Build the Go binaries (linux/amd64, CGO off) and the web standalone (logitrack-web/Dockerfile,
#       docker buildx --platform linux/amd64) from a clean export of HEAD (git archive), so the release is
#       exactly the commit it is named after; upload them as /opt/logitrack/releases/<git-sha>, then
#       rollout.sh activate --pending on the VM (migrate up; until a first deploy has gone live: seed demo
#       + bootstrap admin; switch `current`; pm2 startOrReload; on-VM checks incl. the internal /readyz)
#       and the public checks from here; rollout.sh confirm records the release as live once they pass. A
#       failed check switches back to the previous release, which stays the newest live one.
#       A checkout with changes or untracked files under the release paths (logitrack-api, logitrack-web,
#       shared-docs and the workspace manifests) is refused; --allow-dirty builds the working tree as it
#       is, untracked files included, under the id <git-sha>-dirty-<UTC time>.
#       --reseed tops up the demo seed on a later deploy too (rollout.sh activate --reseed: seed --profile
#       demo, upsert, before the switch), for a release whose seed generators add rows.
#       FILE holds the web's NEXT_PUBLIC_* build arguments (KEY=VALUE, gitignored: e.g.
#       deploy/vm/.env.web-build); it is read without echo, and the values reach docker through its
#       environment (`--build-arg NAME`), never through a command line.
#   deploy.sh --rollback [--to ID] [--dry-run] [--host HOST]
#   deploy.sh --status [--host HOST]
#   deploy.sh --provision [--plan | --dry-run] [--host HOST]
#       Upload the provisioning kit to a temporary directory on the VM and run provision.sh there as root
#       (--plan: provision.sh --dry-run; the temporary copy is removed either way).
#   deploy.sh --kit DIR
#       Build the provisioning kit into DIR (bin/vmenv for linux/amd64, deploy/vm, the roles SQL and the
#       RabbitMQ definitions) to copy to the VM by hand: bash DIR/deploy/vm/provision.sh [--dry-run].
#
# --dry-run builds nothing, contacts nothing and prints the plan.
set -euo pipefail

usage() {
  sed -n '2,/^set -euo/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//' >&2
  exit "${1:-2}"
}

mode=deploy
dry=0
plan=0
allow_dirty=0
reseed=0
web_env=""
to=""
kit_out=""
host_flag=""
while [ $# -gt 0 ]; do
  case $1 in
    --web-build-env)
      [ $# -ge 2 ] || usage
      web_env=$2
      shift
      ;;
    --dry-run) dry=1 ;;
    --plan) plan=1 ;;
    --allow-dirty) allow_dirty=1 ;;
    --reseed) reseed=1 ;;
    --host)
      [ $# -ge 2 ] || usage
      host_flag=$2
      shift
      ;;
    --rollback) mode=rollback ;;
    --to)
      [ $# -ge 2 ] || usage
      to=$2
      shift
      ;;
    --status) mode=status ;;
    --provision) mode=provision ;;
    --kit)
      [ $# -ge 2 ] || usage
      mode=kit
      kit_out=$2
      shift
      ;;
    -h | --help) usage 0 ;;
    *) usage ;;
  esac
  shift
done
# --reseed belongs to a deploy (the activation of a new release).
[ "$reseed" = 0 ] || [ "$mode" = deploy ] || usage

kit_dir=$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)
api_dir=$(cd -P "$kit_dir/../.." && pwd)
repo_root=$(cd -P "$api_dir/.." && pwd)
# shellcheck source=kit.conf
. "$kit_dir/kit.conf"
host=${host_flag:-$kit_ssh_host}
dockerfile=$repo_root/logitrack-web/Dockerfile
# What a release is built from: the Go module, the web and its Docker context (Dockerfile.dockerignore lets
# only these through), so a change elsewhere in the checkout (e.g. .claude/) does not make it dirty.
release_paths="logitrack-api logitrack-web shared-docs package.json pnpm-lock.yaml pnpm-workspace.yaml"
# The tree the release is built from: the checkout under --allow-dirty and for the kit, else export_head.
src_root=$repo_root
id_rx='^[0-9a-f]{7,40}(-dirty-[0-9]{14})?$'
# The Firebase public config TW7 removed with the Firebase SDK (developer-spec.md §16.1 DROP rows). A build env
# file written before TW7 still sets them: they are skipped with a notice (names only), never passed to docker.
retired_web="NEXT_PUBLIC_FIREBASE_API_KEY NEXT_PUBLIC_FIREBASE_AUTH_DOMAIN NEXT_PUBLIC_FIREBASE_PROJECT_ID NEXT_PUBLIC_FIREBASE_STORAGE_BUCKET NEXT_PUBLIC_FIREBASE_MESSAGING_SENDER_ID NEXT_PUBLIC_FIREBASE_APP_ID NEXT_PUBLIC_FIREBASE_MEASUREMENT_ID NEXT_PUBLIC_APP_CHECK_RECAPTCHA_SITE_KEY NEXT_PUBLIC_APP_CHECK_USE_ENTERPRISE NEXT_PUBLIC_APP_CHECK_DEBUG_TOKEN"

say() { printf 'deploy: %s\n' "$*"; }
would() { printf 'deploy: [dry-run] would %s\n' "$*"; }
die() {
  printf 'deploy: %s\n' "$*" >&2
  exit 1
}
need() {
  local c
  for c in "$@"; do command -v "$c" >/dev/null 2>&1 || die "missing command: $c"; done
}
vm() { ssh -o BatchMode=yes -o ConnectTimeout=20 "$host" "$@"; }

stage=""
export_dir=""
cleanup() {
  [ -z "$stage" ] || rm -rf "$stage"
  [ -z "$export_dir" ] || rm -rf "$export_dir"
}
trap cleanup EXIT

module=$(sed -n 's/^module //p' "$api_dir/go.mod")

# build_go dir targets...: static linux/amd64 binaries into dir, from the module of src_root.
build_go() {
  local out=$1
  shift
  say "building Go binaries for linux/amd64 ($*)"
  (cd "$src_root/logitrack-api" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X $module/internal/app.Version=${release_id:-kit}" -o "$out/" "$@")
}

# tar_create: tar for the upload; on macOS (bsdtar) without extended attributes and AppleDouble files, which
# GNU tar on the VM would warn about.
tar_create() {
  if tar --version 2>/dev/null | grep -q bsdtar; then
    COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata "$@"
  else
    tar "$@"
  fi
}

# copy_kit dir: deploy/vm (without tests, test data and env files) plus the two repository files provision.sh
# reads, from src_root.
copy_kit() {
  mkdir -p "$1"
  (cd "$src_root/logitrack-api" && tar_create -cf - --exclude='*_test.go' --exclude='testdata' --exclude='.env*' --exclude='*.env' \
    deploy/vm deploy/postgres-init/00-roles.sql deploy/rabbitmq-definitions.json) | (cd "$1" && tar -xf -)
}

# web_env_names: the NEXT_PUBLIC_* build arguments logitrack-web/Dockerfile declares.
web_env_names() { sed -n 's/^ARG \(NEXT_PUBLIC_[A-Z0-9_]*\)=.*/\1/p' "$dockerfile"; }
# retired_web_name NAME: whether NAME is one of the build arguments TW7 retired.
retired_web_name() { case " $retired_web " in *" $1 "*) return 0 ;; esac; return 1; }

# check_web_env: validates FILE by name and reports the names it sets; never prints a value.
check_web_env() {
  [ -n "$web_env" ] || die "--web-build-env FILE is required (the web's NEXT_PUBLIC_* build arguments)"
  [ -r "$web_env" ] || die "$web_env is not readable"
  if git -C "$repo_root" ls-files --error-unmatch -- "$web_env" >/dev/null 2>&1; then
    die "$web_env is tracked by git; keep build values in a gitignored file (e.g. deploy/vm/.env.web-build)"
  fi
  local allowed set="" line name value n=0 retired=""
  allowed=" $(web_env_names | tr '\n' ' ') "
  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1))
    case $line in '' | '#'*) continue ;; esac
    line=${line#export }
    case $line in *=*) ;; *) die "$web_env:$n: expected NAME=VALUE" ;; esac
    name=${line%%=*}
    value=${line#*=}
    if retired_web_name "$name"; then
      retired="$retired $name"
      continue
    fi
    case $allowed in
      *" $name "*) ;;
      *) die "$web_env:$n: $name is not a NEXT_PUBLIC_* build argument of logitrack-web/Dockerfile (server values never go into the bundle)" ;;
    esac
    [ -n "$value" ] && set="$set $name"
  done <"$web_env"
  [ -z "$retired" ] || say "ignoring build arguments retired by TW7 (the web has no Firebase SDK; remove them from $web_env):$retired"
  say "web build arguments set in $web_env:${set:- none}"
}

# build_web dir: the standalone server for linux/amd64, extracted from the Dockerfile's standalone stage.
# The values are exported in a subshell for docker alone; `--build-arg NAME` takes them from there.
build_web() {
  local out=$1
  say "building the web standalone for linux/amd64 (docker buildx, $kit_web_node_image)"
  (
    set --
    while IFS= read -r line || [ -n "$line" ]; do
      case $line in '' | '#'*) continue ;; esac
      line=${line#export }
      name=${line%%=*}
      value=${line#*=}
      retired_web_name "$name" && continue
      case $value in
        \"*\")
          value=${value#\"}
          value=${value%\"}
          ;;
        \'*\')
          value=${value#\'}
          value=${value%\'}
          ;;
      esac
      export "$name=$value"
      set -- "$@" --build-arg "$name"
    done <"$web_env"
    docker buildx build --platform linux/amd64 -f "$src_root/logitrack-web/Dockerfile" --target standalone \
      --build-arg "NODE_IMAGE=$kit_web_node_image" "$@" \
      --output "type=local,dest=$out" "$src_root"
  )
  [ -f "$out/logitrack-web/server.js" ] || die "the web build produced no logitrack-web/server.js"
}

release_id=""
release_sha=""
dirty=0
compute_id() {
  local changes
  release_sha=$(git -C "$repo_root" rev-parse HEAD)
  # Tracked changes and untracked files (a new migration is embedded by go build) under the release paths.
  # shellcheck disable=SC2086 # a space-separated list of paths
  changes=$(git -C "$repo_root" status --porcelain --untracked-files=normal -- $release_paths)
  release_id=$release_sha
  if [ -n "$changes" ]; then
    [ "$allow_dirty" = 1 ] || die "the checkout has uncommitted changes or untracked files under $release_paths (git status -- <those paths>); commit them, or pass --allow-dirty"
    dirty=1
    release_id=$release_sha-dirty-$(date -u +%Y%m%d%H%M%S)
  fi
  [[ $release_id =~ $id_rx ]] || die "unexpected release id"
}

# export_head: a clean release is built from the commit itself (git archive of the release paths into a
# temporary directory beside the stage, which is uploaded as it is), so no ignored or untracked file of the
# checkout reaches the binaries, the embedded migrations or the web build.
export_head() {
  [ "$dirty" = 1 ] && return 0
  export_dir=$(mktemp -d "${TMPDIR:-/tmp}/logitrack-src.XXXXXX")
  src_root=$export_dir
  say "exporting $release_sha ($release_paths)"
  # shellcheck disable=SC2086 # a space-separated list of paths
  git -C "$repo_root" archive --format=tar "$release_sha" -- $release_paths | tar -xf - -C "$src_root"
}

# public_checks: from here, through Cloudflare and nginx; retried for up to 60 s.
public_checks() {
  local c url want got waited=0
  for c in "https://$kit_api_host/healthz=200" "https://$kit_web_host/login=200" "https://$kit_api_host/v1/tasks=404"; do
    url=${c%=*}
    want=${c##*=}
    while :; do
      got=$(curl -s -o /dev/null -m 15 -w '%{http_code}' "$url" 2>/dev/null || true)
      [ "$got" = "$want" ] && break
      if [ "$waited" -ge 60 ]; then
        say "public check failed: $url answered $got, want $want"
        return 1
      fi
      sleep 5
      waited=$((waited + 5))
    done
    say "ok: $url $got"
  done
}

deploy() {
  check_web_env
  compute_id
  local rel=$kit_root/releases/$release_id
  if [ "$dry" = 1 ]; then
    if [ "$dirty" = 1 ]; then
      would "build from the working tree as it is (--allow-dirty), untracked files included"
    else
      would "build from a clean export of $release_sha (git archive of $release_paths)"
    fi
    would "build bin/{api,worker,scheduler,migrate,seed,etl,release,vmenv} for linux/amd64 (CGO_ENABLED=0, Version=$release_id)"
    would "build the web standalone with docker buildx --platform linux/amd64 (target standalone of logitrack-web/Dockerfile, NODE_IMAGE $kit_web_node_image)"
    would "upload the release to $host:$rel"
    if [ "$reseed" = 1 ]; then
      would "run on $host: bash $rel/deploy/vm/rollout.sh activate --pending --reseed (migrate up; until a first deploy has gone live: seed --profile demo --allow-shared and seed --bootstrap-admin, on a later deploy seed --profile demo --allow-shared only; switch current; pm2 startOrReload; checks of the internal /readyz, worker and scheduler /readyz, public /healthz, public /v1/tasks = 404, web /login)"
    else
      would "run on $host: bash $rel/deploy/vm/rollout.sh activate --pending (migrate up; until a first deploy has gone live: seed --profile demo --allow-shared and seed --bootstrap-admin; switch current; pm2 startOrReload; checks of the internal /readyz, worker and scheduler /readyz, public /healthz, public /v1/tasks = 404, web /login)"
    fi
    would "check https://$kit_api_host/healthz = 200, https://$kit_web_host/login = 200, https://$kit_api_host/v1/tasks = 404; then rollout.sh confirm $release_id (record it as live, keep $kit_keep_releases releases); on failure: rollout.sh rollback (to the newest live release)"
    return 0
  fi
  need git go docker tar ssh curl
  stage=$(mktemp -d "${TMPDIR:-/tmp}/logitrack-release.XXXXXX")
  export_head
  build_go "$stage/bin" ./cmd/... ./tools/vmenv
  build_web "$stage/web"
  copy_kit "$stage"
  printf '%s\n' "$release_id" >"$stage/REVISION"
  say "uploading release $release_id to $host:$rel"
  # The release directory appears only once complete; the active release is never overwritten.
  tar_create -C "$stage" -czf - . | vm "set -eu
d=$rel
if [ \"\$(readlink $kit_root/current 2>/dev/null || true)\" = releases/$release_id ]; then
  echo 'release $release_id is already current: pm2 reload restarts it with the current .env (README)' >&2
  exit 3
fi
rm -rf \"\$d.upload\" \"\$d\"
mkdir -p \"\$d.upload\"
tar -xzf - -C \"\$d.upload\" --no-same-owner
mv \"\$d.upload\" \"\$d\""
  if [ "$reseed" = 1 ]; then
    vm "bash $rel/deploy/vm/rollout.sh activate --pending --reseed"
  else
    vm "bash $rel/deploy/vm/rollout.sh activate --pending"
  fi
  if ! public_checks; then
    # The release was never recorded as live, so the rollback goes to the newest live one and a later
    # --rollback never returns to this release.
    say "switching back to the previous release"
    vm "bash $kit_root/current/deploy/vm/rollout.sh rollback" || true
    die "release $release_id failed the public checks"
  fi
  vm "bash $kit_root/current/deploy/vm/rollout.sh confirm $release_id" ||
    die "release $release_id serves but was not recorded as live; run on the VM: bash $kit_root/current/deploy/vm/rollout.sh confirm $release_id"
  say "release $release_id is live on https://$kit_web_host and https://$kit_api_host"
}

rollback() {
  local args="rollback"
  if [ -n "$to" ]; then
    [[ $to =~ $id_rx ]] || die "not a release id: $to"
    args="$args --to $to"
  fi
  [ "$dry" = 1 ] && args="$args --dry-run"
  vm "bash $kit_root/current/deploy/vm/rollout.sh $args"
}

build_kit() { # dir
  release_id=""
  build_go "$1/bin" ./tools/vmenv
  copy_kit "$1"
}

provision() {
  if [ "$dry" = 1 ]; then
    would "build the provisioning kit (bin/vmenv for linux/amd64, deploy/vm, the roles SQL, the RabbitMQ definitions)"
    would "upload it to a temporary directory on $host and run provision.sh there as root, then remove it"
    say "deploy.sh --provision --plan runs provision.sh --dry-run on the VM instead"
    return 0
  fi
  need go tar ssh
  stage=$(mktemp -d "${TMPDIR:-/tmp}/logitrack-kit.XXXXXX")
  build_kit "$stage"
  local flag=""
  [ "$plan" = 1 ] && flag=--dry-run
  say "running provision.sh ${flag:+$flag }on $host"
  tar_create -C "$stage" -czf - . | vm "set -u
d=\$(mktemp -d /root/logitrack-kit.XXXXXX)
tar -xzf - -C \"\$d\" --no-same-owner
bash \"\$d/deploy/vm/provision.sh\" $flag
rc=\$?
rm -rf \"\$d\"
exit \$rc"
}

kit() {
  [ -n "$kit_out" ] || usage
  if [ -e "$kit_out" ] && [ -n "$(ls -A "$kit_out" 2>/dev/null)" ]; then die "$kit_out is not empty"; fi
  need go tar
  build_kit "$kit_out"
  say "kit ready in $kit_out: copy it to the VM and run bash <dir>/deploy/vm/provision.sh [--dry-run] as root"
}

case $mode in
  deploy) deploy ;;
  rollback) rollback ;;
  status) vm "bash $kit_root/current/deploy/vm/rollout.sh status" ;;
  provision) provision ;;
  kit) kit ;;
esac
