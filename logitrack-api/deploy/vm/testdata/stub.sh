#!/bin/sh
# Test double of every external command the VM kit calls (deploy/vm/kit_test.go): kit_test.go links it
# under each command name in a directory put first on PATH. Every call is appended to
# $STUB_STATE/calls.log as "name arg..." (the process arguments a `ps` on the VM would show), stdin of
# psql, curl -K and ssh is kept in its own file, and small state files stand in for the VM.
name=$(basename "$0")
# Under `env -i` (the kit's pm2 and run-app calls) STUB_STATE is gone: the sandbox keeps state/ next to
# the directory tree the stub was linked into.
if [ -z "${STUB_STATE:-}" ]; then
  d=$(dirname "$0")
  while [ "$d" != / ] && [ "$d" != . ]; do
    if [ -d "$d/state" ]; then
      STUB_STATE=$d/state
      break
    fi
    d=$(dirname "$d")
  done
fi
state=${STUB_STATE:?STUB_STATE is not set}
{
  printf '%s' "$name"
  for a in "$@"; do printf ' %s' "$a"; done
  printf '\n'
} >>"$state/calls.log"

last() { eval "printf '%s' \"\${$#}\""; }

case $name in
  id)
    case "$*" in
      -u) echo 0 ;;
      -un) echo root ;;
      *) [ -e "$state/user" ] || exit 1 ;;
    esac
    ;;
  useradd) : >"$state/user" ;;
  chown) ;;
  runuser)
    while [ $# -gt 0 ] && [ "$1" != -- ]; do shift; done
    shift
    exec "$@"
    ;;
  psql)
    case "$*" in
      *server_version_num*) echo 180006 ;;
      *pg_database*) if [ -e "$state/db" ]; then echo 1; fi ;;
      *pg_roles*) if [ -e "$state/roles" ]; then echo 5; else echo 0; fi ;;
      *"CREATE DATABASE"*) : >"$state/db" ;;
      *" -c "*) ;;
      *)
        sql=$(cat)
        printf '%s\n' "$sql" >>"$state/psql.stdin"
        case $sql in *"CREATE ROLE logitrack_migrator"*) : >"$state/roles" ;; esac
        ;;
    esac
    ;;
  docker)
    case $1 in
      info) ;;
      inspect)
        ctr=$(last "$@")
        [ -e "$state/ctr-$ctr" ] || exit 1
        case "$*" in
          *Health*) echo healthy ;;
          *) echo "$(cat "$state/ctr-$ctr") true" ;;
        esac
        ;;
      run)
        ctr="" envf="" image=""
        while [ $# -gt 0 ]; do
          case $1 in
            --name) ctr=$2; shift ;;
            --env-file) envf=$2; shift ;;
            *@sha256:*) [ -n "$image" ] || image=$1 ;;
          esac
          shift
        done
        cp "$envf" "$state/envfile-$ctr"
        printf '%s' "$image" >"$state/ctr-$ctr"
        echo 0123456789ab
        ;;
      buildx)
        dest=""
        for a in "$@"; do
          case $a in type=local,dest=*) dest=${a#type=local,dest=} ;; esac
        done
        mkdir -p "$dest/logitrack-web/.next/static" "$dest/node_modules"
        echo 'require("http")' >"$dest/logitrack-web/server.js"
        env | grep '^NEXT_PUBLIC_' | sort >"$state/docker-build.env"
        # The build context (the last argument): what the release is built from.
        last "$@" >"$state/docker.context"
        ;;
      *) ;;
    esac
    ;;
  curl)
    case "$*" in
      *" -K -"*) cat >>"$state/curl.stdin" ;;
    esac
    case "$*" in
      *"-w %{http_code}"*)
        url=$(last "$@")
        code=200
        case $url in */v1/tasks) code=404 ;; esac
        if [ -e "$state/fail" ]; then
          while IFS= read -r pat; do
            case $url in *"$pat"*) code=503 ;; esac
          done <"$state/fail"
        fi
        printf '%s' "$code"
        ;;
    esac
    ;;
  ssh)
    cmd=$(last "$@")
    case $cmd in
      *"tar -xzf -"*) cat >"$state/upload.$(wc -l <"$state/calls.log" | tr -d ' ').tgz" ;;
      *"rollout.sh activate"*) echo "rollout: release is live" ;;
    esac
    ;;
  go)
    out=""
    prev=""
    for a in "$@"; do
      [ "$prev" = -o ] && out=$a
      prev=$a
    done
    mkdir -p "$out"
    for a in "$@"; do
      case $a in
        ./cmd/...) for b in api worker scheduler migrate seed etl release; do printf '#!/bin/sh\n' >"$out/$b"; done ;;
        ./tools/vmenv) printf '#!/bin/sh\n' >"$out/vmenv" ;;
      esac
    done
    env | grep '^GO\|^CGO' | sort >"$state/go.env"
    # Where the binaries are built from, and the migrations go:embed would take from there.
    pwd -P >"$state/go.cwd"
    ls migrations >"$state/go.migrations" 2>/dev/null || :
    ;;
  pm2 | systemctl | nginx | ss | node) ;;
  *)
    echo "stub: unexpected command $name" >&2
    exit 97
    ;;
esac
