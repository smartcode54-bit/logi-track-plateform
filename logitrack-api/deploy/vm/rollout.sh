#!/usr/bin/env bash
# rollout.sh: the VM side of deploy.sh (deploy/vm/README.md). Run as root from inside a release:
#
#   /opt/logitrack/releases/<id>/deploy/vm/rollout.sh activate [--pending] [--dry-run] [--no-auto-rollback]
#       migrate up with this release's binary; while no release has gone live yet, seed --profile demo and
#       seed --bootstrap-admin; switch `current` to this release; pm2 startOrReload; on-VM health checks
#       (a failure switches back to the previous release); then record the release as live and keep the
#       newest kit_keep_releases releases. --pending (deploy.sh) stops after the on-VM checks: the release
#       is recorded only by `confirm` once deploy.sh's public checks pass, so a release that fails them
#       never enters the history.
#   /opt/logitrack/current/deploy/vm/rollout.sh confirm <id>
#       record the current release <id> as live (shared/releases.log; the first one also marks the first
#       deploy as done) and prune.
#   /opt/logitrack/current/deploy/vm/rollout.sh rollback [--to <id>] [--dry-run]
#       switch `current` back to the release that was live before it (or <id>) and reload pm2; no migration
#       runs backwards (expand/contract, developer-spec.md §17.4).
#   rollout.sh status
#   rollout.sh pm2 ARGS...
#       pm2 as the logitrack user with its PM2_HOME, e.g. `pm2 reload logitrack-api` after editing the .env
#       (run-app.sh reads it again on every start), `pm2 ls`, `pm2 logs logitrack-web --lines 100`. pm2's
#       own `reload` takes one name (a second one is silently dropped), so `pm2 reload A B` here reloads A,
#       then B.
#
# Every program runs as the logitrack user through run-app.sh, which reads the .env itself; this script
# never reads a secret. --prefix DIR exists for the kit's tests (deploy/vm/kit_test.go); --health-wait
# SECONDS (default 90) bounds the checks after a switch.
set -euo pipefail

usage() {
  echo "usage: rollout.sh activate [--pending] [--dry-run] [--no-auto-rollback] | confirm ID | rollback [--to ID] [--dry-run] | status | pm2 ARGS..." >&2
  exit "${1:-2}"
}
cmd=""
dry=0
auto_rollback=1
pending=0
to=""
confirm_id=""
prefix=""
health_wait=90
pm2_args=()
while [ $# -gt 0 ]; do
  case $1 in
    activate | rollback | status)
      [ -z "$cmd" ] || usage
      cmd=$1
      ;;
    confirm)
      [ -z "$cmd" ] || usage
      [ $# -ge 2 ] || usage
      cmd=confirm
      confirm_id=$2
      shift
      ;;
    pm2)
      # Everything after `pm2` belongs to pm2; options of this script come before it.
      [ -z "$cmd" ] || usage
      cmd=pm2
      shift
      [ $# -ge 1 ] || usage
      pm2_args=("$@")
      break
      ;;
    --dry-run) dry=1 ;;
    --pending) pending=1 ;;
    --no-auto-rollback) auto_rollback=0 ;;
    --to)
      [ $# -ge 2 ] || usage
      to=$2
      shift
      ;;
    --prefix)
      [ $# -ge 2 ] || usage
      prefix=$2
      shift
      ;;
    --health-wait)
      [ $# -ge 2 ] || usage
      health_wait=$2
      shift
      ;;
    -h | --help) usage 0 ;;
    *) usage ;;
  esac
  shift
done
[ -n "$cmd" ] || usage

kit_dir=$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=kit.conf
. "$kit_dir/kit.conf"
self_release=$(cd -P "$kit_dir/../.." && pwd)
root=$prefix$kit_root
releases=$root/releases
current=$root/current
history=$root/shared/releases.log
first_marker=$root/shared/.first-deploy-done
home_dir=$prefix/home/$kit_user
node_dir=$prefix$kit_node_dir
id_rx='^[0-9a-f]{7,40}(-dirty-[0-9]{14})?$'

say() { printf 'rollout: %s\n' "$*"; }
would() { printf 'rollout: [dry-run] would %s\n' "$*"; }
die() {
  printf 'rollout: %s\n' "$*" >&2
  exit 1
}
# run prints a command and executes it (only prints it under --dry-run); no command here carries a secret.
run() {
  if [ "$dry" = 1 ]; then
    would "run: $*"
  else
    say "+ $*"
    "$@"
  fi
}
# as_user runs a program of a release as the logitrack user with a clean environment (env -i); run-app.sh
# adds the program's names from the .env.
as_user() { # release program args...
  local rel=$1
  shift
  runuser -u "$kit_user" -- env -i HOME="$home_dir" LANG=C.UTF-8 PATH=/usr/local/bin:/usr/bin:/bin \
    "$rel/deploy/vm/run-app.sh" "$@"
}
# pm2 as the app user, also with a clean environment: pm2 merges the caller's environment into its apps and
# its dump (`pm2 save`), so nothing of root's session may reach it.
pm2_user() {
  runuser -u "$kit_user" -- env -i HOME="$home_dir" PM2_HOME="$home_dir/.pm2" LANG=C.UTF-8 \
    PATH="$node_dir/bin:/usr/local/bin:/usr/bin:/bin" pm2 "$@"
}

[ "$(id -u)" = 0 ] || die "run as root"
[ -d "$releases" ] || die "$releases is missing (run provision.sh first)"
[ -r "$root/shared/.env" ] || die "$root/shared/.env is missing (run provision.sh first)"

# release_ids: the release directories, oldest name first, on one line.
release_ids() {
  local d out=""
  for d in "$releases"/*/; do
    [ -d "$d" ] || continue
    out="$out $(basename "$d")"
  done
  printf '%s\n' "${out# }"
}

current_id() {
  if [ -L "$current" ]; then basename "$(readlink "$current")"; fi
}

# switch_to id: atomically repoint `current` (relative link, so the tree can move).
switch_to() {
  local id=$1
  if [ "$dry" = 1 ]; then
    would "point $current at releases/$id"
    return 0
  fi
  ln -sfn "releases/$id" "$current.next"
  if mv --version >/dev/null 2>&1; then mv -T -f "$current.next" "$current"; else mv -f -h "$current.next" "$current"; fi
  say "current -> releases/$id"
}

# record_live id: the release passed every check (or was rolled back to); rollback and prune read this
# history. A release that failed a check never enters it (deploy.sh activates with --pending and confirms
# only after its public checks), so a rollback never returns to it. The first entry also marks the first
# deploy as done: until then every activation seeds again (the seed is idempotent), so a first deploy that
# failed a check, even after its seed, is seeded again by the next one.
record_live() {
  if [ "$dry" = 1 ]; then
    would "record $1 as live in $history"
    [ -e "$first_marker" ] || would "write $first_marker"
    return 0
  fi
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" >>"$history"
  [ -e "$first_marker" ] || date -u +%Y-%m-%dT%H:%M:%SZ >"$first_marker"
}

pm2_reload() {
  run pm2_user startOrReload "$current/deploy/vm/ecosystem.config.cjs" --update-env
  run pm2_user save
}

# http_code url: the status code of a GET (000 when nothing answers).
http_code() { curl -s -o /dev/null -m 10 -w '%{http_code}' "$1" 2>/dev/null || true; }

# health: the on-VM checks of deploy.sh's list; retried for up to --health-wait seconds while the processes start.
health() {
  if [ "$dry" = 1 ]; then
    would "check 127.0.0.1:$kit_api_internal_port/readyz, :$kit_worker_metrics_port/readyz, :$kit_scheduler_metrics_port/readyz = 200, public :$kit_api_public_port/healthz = 200, public /v1/tasks = 404, web :$kit_web_port/login = 200"
    return 0
  fi
  local checks="$kit_api_internal_port/readyz=200 $kit_worker_metrics_port/readyz=200 $kit_scheduler_metrics_port/readyz=200 $kit_api_public_port/healthz=200 $kit_api_public_port/v1/tasks=404 $kit_web_port/login=200"
  local c path want got waited=0
  for c in $checks; do
    path=${c%=*}
    want=${c##*=}
    while :; do
      got=$(http_code "http://127.0.0.1:$path")
      [ "$got" = "$want" ] && break
      if [ "$waited" -ge "$health_wait" ]; then
        say "check failed: http://127.0.0.1:$path answered $got, want $want"
        return 1
      fi
      sleep 3
      waited=$((waited + 3))
    done
    say "ok: http://127.0.0.1:$path $got"
  done
}

previous_id() { # the release active before the current one that still exists
  local cur line id
  cur=$(current_id)
  [ -r "$history" ] || return 0
  # Newest first; skip the current release and missing ones.
  while read -r line; do
    id=${line##* }
    if [ "$id" != "$cur" ] && [ -d "$releases/$id" ]; then
      printf '%s\n' "$id"
      return 0
    fi
  done < <(sed '1!G;h;$!d' "$history")
}

prune() {
  local keep id line n=0
  keep=" $(current_id) "
  if [ -r "$history" ]; then
    while read -r line; do
      id=${line##* }
      case $keep in *" $id "*) continue ;; esac
      [ "$n" -ge $((kit_keep_releases - 1)) ] && break
      keep="$keep$id "
      n=$((n + 1))
    done < <(sed '1!G;h;$!d' "$history")
  fi
  local d
  for d in "$releases"/*/; do
    id=$(basename "$d")
    [[ $id =~ $id_rx ]] || continue
    case $keep in *" $id "*) continue ;; esac
    run rm -rf "${releases:?}/$id"
    [ "$dry" = 1 ] || say "removed releases/$id"
  done
}

activate() {
  local id rel prev
  rel=$self_release
  id=$(basename "$rel")
  [[ $id =~ $id_rx ]] || die "release directory name $id is not a release id"
  [ "$rel" = "$(cd -P "$releases" && pwd)/$id" ] || die "run rollout.sh from $releases/<id>/deploy/vm"
  local f
  for f in bin/api bin/worker bin/scheduler bin/migrate bin/seed web/logitrack-web/server.js deploy/vm/run-app.sh \
    deploy/vm/ecosystem.config.cjs; do
    [ -e "$rel/$f" ] || die "release $id has no $f"
  done
  prev=$(current_id)
  say "activating $id (current: ${prev:-none})"

  # Code is root-owned and read-only for the app user; Next's runtime cache is the one writable directory.
  run chown -R root:root "$rel"
  run chmod -R u=rwX,go=rX "$rel"
  run mkdir -p "$rel/web/logitrack-web/.next/cache"
  run chown "$kit_user:$kit_user" "$rel/web/logitrack-web/.next/cache"

  # The schema first, with the new binary; the running release keeps serving (expand/contract).
  run as_user "$rel" migrate up
  if [ -e "$first_marker" ]; then
    say "not the first deploy: no seed"
  else
    # Until a release has gone live (record_live writes the marker), so a first deploy that failed a check
    # is seeded again by the next one.
    run as_user "$rel" seed --profile demo --allow-shared
    run as_user "$rel" seed --bootstrap-admin
  fi

  switch_to "$id"
  pm2_reload
  if ! health; then
    if [ "$auto_rollback" = 1 ] && [ -n "$prev" ] && [ "$prev" != "$id" ] && [ -d "$releases/$prev" ]; then
      say "health checks failed: switching back to $prev"
      switch_to "$prev"
      pm2_reload
      health || true
    fi
    die "release $id failed its health checks (pm2 logs: runuser -u $kit_user -- env PM2_HOME=$home_dir/.pm2 $kit_node_dir/bin/pm2 logs)"
  fi
  if [ "$pending" = 1 ]; then
    say "release $id passed the on-VM checks; it is recorded as live by rollout.sh confirm $id"
    return 0
  fi
  record_live "$id"
  prune
  say "release $id is live"
}

# confirm id: deploy.sh's public checks passed for the current release.
confirm() {
  local cur
  cur=$(current_id)
  [[ $confirm_id =~ $id_rx ]] || die "not a release id: $confirm_id"
  [ "$confirm_id" = "$cur" ] || die "release $confirm_id is not current (current: ${cur:-none}); nothing recorded"
  record_live "$confirm_id"
  prune
  say "release $confirm_id is live"
}

rollback() {
  local target cur
  cur=$(current_id)
  target=${to:-$(previous_id)}
  [ -n "$target" ] || die "no earlier release to roll back to"
  [[ $target =~ $id_rx ]] || die "not a release id: $target"
  [ -d "$releases/$target" ] || die "releases/$target does not exist (kept: $(release_ids))"
  [ "$target" != "$cur" ] || die "$target is already current"
  say "rolling back from ${cur:-none} to $target (no migration runs backwards)"
  switch_to "$target"
  pm2_reload
  health || die "the rolled-back release $target failed its health checks"
  record_live "$target"
  say "release $target is live"
}

status() {
  local cur last=""
  cur=$(current_id)
  say "current: $cur"
  [ -r "$history" ] && last=$(tail -n 1 "$history" | sed 's/.* //')
  if [ -n "$cur" ] && [ "$cur" != "$last" ]; then
    say "current release $cur is not recorded as live (its deploy did not pass the public checks or was interrupted)"
  fi
  say "releases: $(release_ids)"
  if [ -e "$first_marker" ]; then say "first deploy done: $(cat "$first_marker")"; else say "first deploy pending"; fi
  [ -r "$history" ] && tail -n 5 "$history" | sed 's/^/rollout: history: /'
  return 0
}

# pm2_cmd: the `pm2` passthrough. pm2 declares `reload <id|name|namespace|all>` with one argument and drops
# any further name without an error, so a reload of several names runs once per name.
pm2_cmd() {
  local a names=() opts=()
  if [ "${pm2_args[0]}" = reload ] && [ "${#pm2_args[@]}" -gt 2 ]; then
    for a in "${pm2_args[@]:1}"; do
      case $a in -*) opts+=("$a") ;; *) names+=("$a") ;; esac
    done
    if [ "${#names[@]}" -gt 1 ]; then
      # An option value (e.g. --parallel 2) would read as a name: refuse rather than guess.
      [ "${#opts[@]}" = 0 ] || die "pm2 reload takes one name; run rollout.sh pm2 reload once per app, or use restart"
      for a in "${names[@]}"; do pm2_user reload "$a"; done
      return 0
    fi
  fi
  pm2_user "${pm2_args[@]}"
}

case $cmd in
  activate) activate ;;
  confirm) confirm ;;
  rollback) rollback ;;
  status) status ;;
  pm2) pm2_cmd ;;
  *) usage ;;
esac
