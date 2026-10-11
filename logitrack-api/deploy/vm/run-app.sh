#!/usr/bin/env bash
# run-app.sh: exec one program of a release with its environment from the VM's shared .env
# (deploy/vm/README.md). pm2 starts api, worker, scheduler and web through it (ecosystem.config.cjs);
# rollout.sh runs migrate and seed through it; the operator runs cmd/release through it (README "Publishing a
# driver-app APK"), which gets the internal listener's URL from kit.conf.
#
#   run-app.sh api|worker|scheduler|migrate|seed|release [ARGS...]
#   run-app.sh web
#
# The .env (0640 root:logitrack) is read here, at exec time: only the names of env-names/<program>.txt
# are exported, every other name of the .env is unset, and an allowed name the .env lacks keeps the value
# pm2 gave the app (the per-process listener settings of ecosystem.config.cjs). So no value of the .env
# lands in pm2's process list or dump, and no program finds another program's names in its environment.
# That is no security boundary: every program runs as the one logitrack user, which can read the .env, the
# JWT key and /proc/<pid>/environ of the others (README "Isolation between the programs", developer-spec.md
# §15.8). Values are never printed: no `set -x`, no echo of a line, and `export` is a builtin (no process
# argument holds a value). Works with bash 3.2 (macOS, tests) and bash 5 (the VM).
set -euo pipefail

usage() {
  echo "usage: run-app.sh api|worker|scheduler|migrate|seed|release|web [ARGS...]" >&2
  exit 2
}
[ $# -ge 1 ] || usage
program=$1
shift
case $program in
  api | worker | scheduler | migrate | seed | release | web) ;;
  *) usage ;;
esac

# The release is two levels above this file, the deployment root two levels above the release
# (<root>/releases/<sha>/deploy/vm); -P resolves the `current` symlink to the release it points at.
kit_dir=$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)
release=$(cd -P "$kit_dir/../.." && pwd)
root=$(cd -P "$release/../.." && pwd)
env_file=$root/shared/.env
names_file=$kit_dir/env-names/$program.txt

if [ ! -r "$env_file" ]; then
  echo "run-app.sh: $env_file is missing or unreadable (provision.sh writes it; run as $(id -un 2>/dev/null || echo ?))" >&2
  exit 2
fi
if [ ! -r "$names_file" ]; then
  echo "run-app.sh: $names_file is missing" >&2
  exit 2
fi

nl='
'
allowed=$nl
while IFS= read -r line || [ -n "$line" ]; do
  case $line in '' | '#'*) continue ;; esac
  allowed=$allowed$line$nl
done <"$names_file"

n=0
while IFS= read -r line || [ -n "$line" ]; do
  n=$((n + 1))
  case $line in '' | '#'*) continue ;; esac
  name=${line%%=*}
  case $line in
    *=*) ;;
    *)
      echo "run-app.sh: $env_file:$n: expected NAME=VALUE" >&2
      exit 2
      ;;
  esac
  case $name in
    '' | [0-9]* | *[!A-Za-z0-9_]*)
      echo "run-app.sh: $env_file:$n: not a variable name" >&2
      exit 2
      ;;
  esac
  case $allowed in
    *"$nl$name$nl"*) export "$name=${line#*=}" ;;
    *) unset "$name" ;;
  esac
done <"$env_file"
unset line name allowed n

# cmd/release calls the api's internal listener (R43): its URL is per-process like the web's (ecosystem.config.cjs),
# so it comes from the kit's port unless the caller set one.
if [ "$program" = release ] && [ -z "${GO_API_INTERNAL_URL:-}" ]; then
  # shellcheck source=kit.conf
  . "$kit_dir/kit.conf"
  export GO_API_INTERNAL_URL="http://127.0.0.1:${kit_api_internal_port}"
fi

case $program in
  web)
    # Next's standalone server serves .next/static and public/ from its own directory.
    cd "$release/web/logitrack-web"
    exec node server.js "$@"
    ;;
  *)
    exec "$release/bin/$program" "$@"
    ;;
esac
