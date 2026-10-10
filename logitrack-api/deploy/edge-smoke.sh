#!/usr/bin/env bash
# TW2 acceptance checks for the edge (web + Caddy) of the local stack: `make smoke EDGE=1` after
# `make up EDGE=1`. Prints no secret values.
#   --env FILE    env file to read (default .env)
#   --port N      Caddy's HTTP port on 127.0.0.1 (default 80)
#   --no-compose  skip the steps that need the compose project (service health, probe upload, api
#                 log lookup); the probe object must then exist already
set -euo pipefail
cd "$(dirname "$0")/.."
envfile=.env port=80 use_compose=1
while [ $# -gt 0 ]; do
  case "$1" in
    --env) envfile=$2; shift 2 ;;
    --port) port=$2; shift 2 ;;
    --no-compose) use_compose=0; shift ;;
    *) echo "edge-smoke: unknown option $1" >&2; exit 2 ;;
  esac
done
compose=(docker compose -f deploy/docker-compose.yml --env-file "$envfile" --profile edge)
pass=0; fail=0
ok()  { echo "  ok   $*"; pass=$((pass + 1)); }
bad() { echo "  FAIL $*"; fail=$((fail + 1)); }
val() { grep -E "^$1=" "$envfile" | head -1 | cut -d= -f2-; }
host_of() { # local sites are http:// addresses (§15.1)
  case "$1" in http://*) h=${1#http://}; echo "${h%%/*}" ;; *) echo "edge-smoke: $2=$1 is not an http:// site; run against the local stack" >&2; exit 2 ;; esac
}
web=$(host_of "$(val WEB_DOMAIN)" WEB_DOMAIN)
api=$(host_of "$(val API_PUBLIC_DOMAIN)" API_PUBLIC_DOMAIN)
media=$(host_of "$(val MEDIA_DOMAIN)" MEDIA_DOMAIN)
bucket=$(val S3_BUCKET)
# Every request goes to Caddy on 127.0.0.1:$port with the site's own Host header.
req() { local host=$1; shift; curl -s --connect-to "$host:80:127.0.0.1:$port" "$@"; }
hdr() { req "$1" -o /dev/null -D - "http://$1$2" | tr -d '\r' | grep -i "^$3:" | head -1 | cut -d' ' -f2-; }
code() { req "$1" -o /dev/null -w '%{http_code}' "${@:3}" "http://$1$2"; }

if [ "$use_compose" = 1 ]; then
  echo "services"
  for s in web caddy; do
    state=$("${compose[@]}" ps --format '{{.Service}} {{.State}} {{.Health}}' "$s" 2>/dev/null | awk '{print $2"/"$3}')
    [ "$state" = "running/healthy" ] && ok "$s $state" || bad "$s ${state:-absent} (make up EDGE=1)"
  done
fi

echo "web ($web): standalone server behind Caddy"
c=$(code "$web" /api/healthz); [ "$c" = 200 ] && ok "/api/healthz 200" || bad "/api/healthz $c"
id="smoke-$RANDOM$RANDOM"
body=$(req "$web" "http://$web/app/customers/$id")
if grep -q "$id" <<<"$body" && ! grep -q placeholder <<<"$body"; then ok "/app/customers/$id served for the real id (no placeholder rewrite)"; else bad "/app/customers/$id"; fi
for p in "/app/customers/$id/edit" "/app/subcontractors/$id" "/app/subcontractors/$id/edit"; do
  c=$(code "$web" "$p"); [ "$c" = 200 ] && ok "$p 200" || bad "$p $c"
done
want_page="public, max-age=0, must-revalidate" want_app="private, no-cache" want_static="public, max-age=31536000, immutable"
for p in / /login; do
  v=$(hdr "$web" "$p" cache-control); [ "$v" = "$want_page" ] && ok "$p Cache-Control: $v" || bad "$p Cache-Control: $v (want $want_page)"
done
v=$(hdr "$web" /app/dashboard cache-control); [ "$v" = "$want_app" ] && ok "/app/dashboard Cache-Control: $v" || bad "/app/dashboard Cache-Control: $v (want $want_app)"
asset=$(req "$web" "http://$web/login" | grep -oE '/_next/static/[^"]+\.js' | head -1 || true)
if [ -n "$asset" ]; then
  v=$(hdr "$web" "$asset" cache-control); [ "$v" = "$want_static" ] && ok "/_next/static Cache-Control: $v" || bad "/_next/static Cache-Control: $v (want $want_static)"
  v=$(req "$web" -o /dev/null -D - -H 'Accept-Encoding: gzip' "http://$web$asset" | tr -d '\r' | grep -i '^content-encoding:' | cut -d' ' -f2- || true)
  [ "$v" = gzip ] && ok "Caddy compresses static assets (gzip)" || bad "static asset Content-Encoding: ${v:-none}"
else
  bad "no /_next/static script found in /login"
fi
v=$(hdr "$web" / cross-origin-embedder-policy); [ "$v" = unsafe-none ] && ok "Cross-Origin-Embedder-Policy: $v" || bad "Cross-Origin-Embedder-Policy: ${v:-missing}"
v=$(hdr "$web" /app/users location); [ "$v" = /app/security-center/users ] && ok "/app/users redirects to $v" || bad "/app/users location: ${v:-none}"

echo "api ($api): only the Go public listener"
c=$(code "$api" /healthz); [ "$c" = 200 ] && ok "/healthz 200" || bad "/healthz $c"
if [ "$use_compose" = 1 ]; then
  rid=$(hdr "$api" /healthz x-request-id); sleep 1
  if [ -n "$rid" ] && "${compose[@]}" logs --no-log-prefix api 2>/dev/null | grep -F "\"request_id\":\"$rid\"" | grep -q '"listener":"public"'; then
    ok "request $rid was served by the public listener"
  else
    bad "request ${rid:-?} not found as listener=public in the api log"
  fi
fi
for p in /readyz /v1/customers /v1/users /.well-known/jwks.json /metrics /api/go/v1/me /; do
  b=$(req "$api" -w ' %{http_code}' "http://$api$p")
  if [[ "$b" == *'"code":"not_found"'*' 404' ]]; then ok "$p 404 not_found"; else bad "$p -> ${b: -3}"; fi
done

echo "media ($media): MinIO for presigned and public object URLs"
key=smoke-edge-probe.txt
if [ "$use_compose" = 1 ]; then
  "${compose[@]}" run --rm -T --no-deps --entrypoint sh minio-init -c '
    export MC_HOST_local="http://${MINIO_ROOT_USER}:${MINIO_ROOT_PASSWORD}@minio:9000" &&
    echo edge-probe | mc pipe "local/$S3_BUCKET/'"$key"'" >/dev/null' \
    && ok "probe object written" || bad "cannot write the probe object"
fi
url=$(go run ./tools/presign -env "$envfile" -endpoint "http://$media" -bucket "$bucket" -key "$key")
b=$(req "$media" -w ' %{http_code}' "$url")
[[ "$b" == edge-probe*" 200" ]] && ok "URL signed for http://$media (S3_PRESIGN_ENDPOINT in prod) resolves through the media site" || bad "presigned GET -> ${b: -3}"
other=$(go run ./tools/presign -env "$envfile" -endpoint "http://elsewhere.invalid" -bucket "$bucket" -key "$key")
c=$(code "$media" "/${other#http://elsewhere.invalid/}"); [ "$c" = 403 ] && ok "a URL signed for another host is refused (Host reaches MinIO unchanged)" || bad "foreign-host signature -> $c"
c=$(code "$media" "/$bucket/$key"); [ "$c" = 403 ] && ok "unsigned GET of the private bucket -> 403" || bad "unsigned GET -> $c"
c=$(code "$media" /minio/health/live); [ "$c" = 404 ] && ok "MinIO admin/health paths -> 404" || bad "/minio/health/live -> $c"

echo
echo "edge-smoke: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
