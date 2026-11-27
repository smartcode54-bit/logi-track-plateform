#!/usr/bin/env bash
# prod-sync.sh: bring the staging database up to date with production Firestore (deploy/vm/README.md section 10).
# Run as root by logitrack-prod-sync.timer every 6 hours, or by hand:
#
#   /opt/logitrack/current/deploy/vm/prod-sync.sh [--dry-run | --no-commit] [--reapply COLLECTIONS]
#
# One run, under a lock (a second run while one is going logs "skipped" and exits 0):
#   1. etl dump --collections=all and firebase-tools auth:export, as root, with the read-only key named in
#      /etc/logitrack/prod-sync.env (the source is only read; the key never reaches the logitrack user);
#   2. as the logitrack user through run-app.sh: etl auth-import (accounts staging lacks; existing ones are skipped),
#      etl load (one transaction under the load lock: it upserts the documents whose update time is newer than
#      etl.source_docs keeps, and rolls back whole on any error, so staging keeps its last good load), seed
#      bootstrap-platform-admins (PLATFORM_ADMIN_EMAILS of the .env, idempotent);
#   3. a flush of the read-through cache (cache: namespace of logitrack-redis), etl status, and the stamp
#      shared/prod-sync/last-success.
# Nothing staging-only is wiped: rows made on staging, its users and passwords, the platform_admin grants and the
# support-access consent are not documents of the dump. A staging edit of a row that came from production lasts
# until production changes that document. Documents deleted in production keep their rows here.
#
# --reapply tasks,trip_records (by hand, after a release changed how those collections map) runs a second load of
# just them with --force, so documents production has not changed since the last run are mapped again too.
# --dry-run checks the set-up and prints the plan: no read, no write, no lock, no log. --no-commit reads production
# and runs auth-import and load with --dry-run (nothing committed, no stamp). The dump and the export hold personal
# data: they live in shared/prod-sync/run.* for the length of the run only. Output goes to stdout (the journal under
# systemd) and to shared/logs/prod-sync.log; no step prints the project id or a key. --prefix DIR exists for the
# kit's tests (deploy/vm/kit_test.go). Exit codes: 0 done or skipped, 1 a step failed, 2 the set-up is wrong.
set -euo pipefail

usage() {
  echo "usage: prod-sync.sh [--dry-run | --no-commit] [--reapply COLLECTIONS]" >&2
  exit "${1:-2}"
}
mode=run
prefix=""
reapply=""
while [ $# -gt 0 ]; do
  case $1 in
    --dry-run) mode=dry ;;
    --no-commit) mode=nocommit ;;
    --reapply)
      [ $# -ge 2 ] || usage
      [[ $2 =~ ^[A-Za-z_]+(,[A-Za-z_]+)*$ ]] || usage
      reapply=$2
      shift
      ;;
    --prefix)
      [ $# -ge 2 ] || usage
      prefix=$2
      shift
      ;;
    -h | --help) usage 0 ;;
    *) usage ;;
  esac
  shift
done

kit_dir=$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=kit.conf
. "$kit_dir/kit.conf"
root=$prefix$kit_root
conf=$prefix/etc/logitrack/prod-sync.env
firebase=$prefix/opt/logitrack-sync-tools/node_modules/.bin/firebase
lock=$prefix/run/lock/logitrack-prod-sync.lock
sync_dir=$root/shared/prod-sync
log=$root/shared/logs/prod-sync.log
home_dir=$prefix/home/$kit_user
node_dir=$prefix$kit_node_dir
# dump reads no tenant, but cmd/etl refuses to start without one (R56): any uuid passes.
dump_tenant=00000000-0000-4000-8000-000000000000

say() { printf '%s prod-sync: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() {
  say "$*" >&2
  exit 2
}

# private FILE: refuse a file the group or others can read (the key, the config).
private() {
  [ -f "$1" ] || die "$1 is missing (README section 10, install)"
  # shellcheck disable=SC2012 # the kit's own fixed paths; ls -l prints the mode the same on Linux and macOS
  case $(ls -ln "$1" | cut -c5-10) in
    ------) ;;
    *) die "$1 must be readable by root only (chmod 0400)" ;;
  esac
}

[ "$(id -u)" = 0 ] || die "run as root"
private "$conf"
project="" creds=""
while IFS= read -r line || [ -n "$line" ]; do
  case $line in
    PROD_SYNC_PROJECT_ID=*) project=${line#*=} ;;
    PROD_SYNC_CREDENTIALS=*) creds=${line#*=} ;;
  esac
done <"$conf"
unset line
# A Google Cloud project id; never printed.
[[ $project =~ ^[a-z][a-z0-9-]{4,28}[a-z0-9]$ ]] || die "$conf: PROD_SYNC_PROJECT_ID is not a project id"
case $creds in /*) ;; *) die "$conf: PROD_SYNC_CREDENTIALS must be an absolute path" ;; esac
creds=$prefix$creds
private "$creds"
[ -x "$firebase" ] || die "$firebase is missing (README section 10, install)"
[ -L "$root/current" ] || die "$root/current is missing (deploy first)"
rel=$(cd -P "$root/current" && pwd)
[ -x "$rel/bin/etl" ] || die "$rel/bin/etl is missing"

steps="dump auth-export auth-import load ${reapply:+reapply }platform-admins cache-flush status stamp"
if [ "$mode" = dry ]; then
  say "[dry-run] set-up ok: release $(basename "$rel"), config, key and firebase-tools in place"
  for s in $steps; do say "[dry-run] would run step $s"; done
  exit 0
fi

# as_user runs a program of the release as the logitrack user with a clean environment (rollout.sh); run-app.sh adds
# the program's names from the .env, which never holds the production key.
as_user() {
  runuser -u "$kit_user" -- env -i HOME="$home_dir" LANG=C.UTF-8 PATH=/usr/local/bin:/usr/bin:/bin \
    "$rel/deploy/vm/run-app.sh" "$@"
}

# finish (EXIT trap of sync_run): the run's personal data goes, whatever happened.
finish() {
  local rc=$?
  rm -rf "$work"
  if [ "$rc" = 0 ]; then
    say "done ($mode)"
  else
    say "FAILED at step $step (exit $rc); the database keeps its last good load"
  fi
}

sync_run() {
  mkdir -p "$(dirname "$lock")"
  exec 9>"$lock"
  if ! flock -n 9; then
    say "another run holds $lock; skipped"
    return 0
  fi
  umask 027
  mkdir -p "$sync_dir"
  chown root:"$kit_user" "$sync_dir"
  chmod 0750 "$sync_dir"
  # A run that died (reboot, kill -9) leaves its personal data behind; the lock says none is in use.
  rm -rf "$sync_dir"/run.*
  work=$(mktemp -d "$sync_dir/run.XXXXXX")
  step=start
  trap finish EXIT
  say "start ($mode): release $(basename "$rel")"
  local commit=""
  [ "$mode" = nocommit ] && commit=--dry-run

  step=dump
  say "== $step"
  env -i HOME="$work" LANG=C.UTF-8 PATH=/usr/bin:/bin APP_ENV=dev OWN_FLEET_TENANT_ID="$dump_tenant" \
    ETL_FIRESTORE_PROJECT_ID="$project" GOOGLE_APPLICATION_CREDENTIALS="$creds" \
    "$rel/bin/etl" dump --collections=all --out="$work/dump"

  step=auth-export
  say "== $step"
  # An empty home: firebase-tools would prefer a cached `firebase login` over the key.
  mkdir -m 0700 "$work/firebase-home"
  env -i HOME="$work/firebase-home" XDG_CONFIG_HOME="$work/firebase-home" LANG=C.UTF-8 NO_UPDATE_NOTIFIER=1 \
    PATH="$node_dir/bin:/usr/bin:/bin" GOOGLE_APPLICATION_CREDENTIALS="$creds" \
    "$firebase" auth:export "$work/auth-export.json" --format=json --project "$project" --non-interactive
  rm -rf "$work/firebase-home"
  # logitrack reads the dump and the export; nobody else does.
  chown -R root:"$kit_user" "$work"
  chmod -R u=rwX,g=rX,o= "$work"

  step=auth-import
  say "== $step"
  as_user etl auth-import --export="$work/auth-export.json" --dump="$work/dump" ${commit:+"$commit"}

  step=load
  say "== $step"
  as_user etl load --dump="$work/dump" ${commit:+"$commit"}

  if [ -n "$reapply" ]; then
    step=reapply
    say "== $step $reapply"
    as_user etl load --dump="$work/dump" --collections="$reapply" --force ${commit:+"$commit"}
  fi

  if [ "$mode" = nocommit ]; then
    step=status
    say "== $step"
    as_user etl status
    return 0
  fi

  step=platform-admins
  say "== $step"
  as_user seed bootstrap-platform-admins

  step=cache-flush
  say "== $step"
  # The read-through copies only (cache: namespace); sessions, rate limits and locks stay.
  docker exec logitrack-redis sh -c "redis-cli --scan --pattern 'lt:*:cache:*' | xargs -r redis-cli del >/dev/null"

  step=status
  say "== $step"
  as_user etl status

  step=stamp
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(basename "$rel")" >"$sync_dir/last-success"
}

mkdir -p "$(dirname "$log")"
# errexit holds inside sync_run (a pipeline element runs in its own shell); pipefail makes its status the script's.
# The explicit subshell makes bash 3.2 run sync_run's EXIT trap on success too.
(
  sync_run
) 2>&1 | tee -a "$log"
