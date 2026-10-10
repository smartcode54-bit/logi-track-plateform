#!/usr/bin/env bash
# T02/T03 acceptance checks against the running local stack (make up first). Prints no secret values.
set -euo pipefail
cd "$(dirname "$0")/.."
compose=(docker compose -f deploy/docker-compose.yml --env-file .env)
pass=0; fail=0
ok()  { echo "  ok   $*"; pass=$((pass + 1)); }
bad() { echo "  FAIL $*"; fail=$((fail + 1)); }
val() { grep -E "^$1=" .env | head -1 | cut -d= -f2-; }

echo "services"
for s in postgres redis rabbitmq minio mailpit api worker scheduler; do
  state=$("${compose[@]}" ps --format '{{.Service}} {{.State}} {{.Health}}' "$s" 2>/dev/null | awk '{print $2"/"$3}')
  case "$state" in running/healthy|running/) ok "$s $state" ;; *) bad "$s ${state:-absent}" ;; esac
done
for s in rabbitmq-init minio-init migrate; do
  code=$("${compose[@]}" ps -a --format '{{.Service}} {{.ExitCode}}' "$s" | awk '{print $2}')
  [ "$code" = "0" ] && ok "$s exited 0" || bad "$s exit=${code:-absent}"
done

echo "postgres"
q() { "${compose[@]}" exec -T postgres sh -c "psql -qAt -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -c \"$1\""; }
v=$(q "SHOW server_version"); [[ "$v" == 18* ]] && ok "server_version $v" || bad "server_version $v"
d=$(q "SHOW data_directory"); [ "$d" = "/var/lib/postgresql/18/docker" ] && ok "data_directory $d" || bad "data_directory $d"
r=$(q "SELECT string_agg(rolname||':'||rolcanlogin||':'||rolbypassrls, ',' ORDER BY rolname) FROM pg_roles WHERE rolname LIKE 'logitrack%'")
[ "$r" = "logitrack_app:true:false,logitrack_etl:true:true,logitrack_migrator:true:false,logitrack_readonly:true:false,logitrack_rls_definer:false:true" ] \
  && ok "five R66 roles with their attributes" || bad "roles: $r"
t=$(q "SELECT count(*) FROM pg_database WHERE datname='logitrack_test'"); [ "$t" = "1" ] && ok "logitrack_test exists" || bad "logitrack_test missing"
# Connect through the service name, not 127.0.0.1: the image trusts loopback, while network
# connections use scram-sha-256, so this proves the password. URLs travel on stdin, never in argv.
login() { printf '%s\n' "$1" | "${compose[@]}" exec -T postgres sh -c 'read -r U; psql "$U" -qAt -c "select current_user"' 2>/dev/null; }
for url in DATABASE_URL MIGRATE_DATABASE_URL ETL_DATABASE_URL; do
  u=$(val "$url"); who=$(login "$u" || true)
  role=$(sed -E 's#^postgres://([^:]+):.*#\1#' <<<"$u")
  [ "$who" = "$role" ] && ok "$url logs in as $role (scram)" || bad "$url cannot log in (run make dev-db)"
done
wrong=$(sed -E 's#^(postgres://[^:]+:)[^@]+@#\1not-the-password@#' <<<"$(val DATABASE_URL)")
[ -z "$(login "$wrong" || true)" ] && ok "a wrong password is rejected" || bad "wrong password accepted"

echo "migrations"
want=$(find migrations -maxdepth 1 -name '[0-9][0-9][0-9][0-9]_*.sql' | wc -l | tr -d ' ')
v=$(q "SELECT coalesce(max(version_id), 0) FROM goose_db_version")
[ "$v" = "$want" ] && ok "schema at version $v (every embedded migration)" || bad "schema version $v, want $want"
# R66: functions belong to logitrack_migrator, except SECURITY DEFINER ones that 0009 hands to
# logitrack_rls_definer; citext's members belong to the bootstrap superuser (trusted extension).
o=$(q "SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname IN ('public','etl') AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e') AND NOT (pg_get_userbyid(p.proowner) = 'logitrack_migrator' OR (p.prosecdef AND pg_get_userbyid(p.proowner) = 'logitrack_rls_definer'))")
[ "$o" = "0" ] && ok "schema functions owned by logitrack_migrator or, if SECURITY DEFINER, logitrack_rls_definer (R66)" || bad "$o functions with an unexpected owner"
"${compose[@]}" run --rm -T migrate status -fail-on-pending >/dev/null 2>&1 \
  && ok "migrate status: nothing pending" || bad "migrate status reports pending migrations"

echo "minio"
pub=$(val S3_PUBLIC_BUCKET); priv=$(val S3_BUCKET)
"${compose[@]}" run --rm -T --entrypoint sh minio-init -c '
  export MC_HOST_local="http://${MINIO_ROOT_USER}:${MINIO_ROOT_PASSWORD}@minio:9000" &&
  for k in "$S3_PUBLIC_BUCKET/app_releases/smoke-probe.txt" "$S3_BUCKET/smoke-probe.txt" \
           "$S3_PUBLIC_BUCKET/other/smoke-probe.txt" "$S3_PUBLIC_BUCKET/app_releases_x/smoke-probe.txt"; do
    echo probe | mc pipe "local/$k" >/dev/null || exit 1
  done' \
  && ok "probe objects written" || bad "cannot write probe objects"
code() { curl -s -o /dev/null -w '%{http_code}' "$1"; }
c=$(code "http://localhost:9000/$priv/smoke-probe.txt"); [ "$c" = "403" ] && ok "anonymous GET private bucket -> 403" || bad "private bucket -> $c"
c=$(code "http://localhost:9000/$pub/app_releases/smoke-probe.txt"); [ "$c" = "200" ] && ok "anonymous GET $pub/app_releases/ -> 200" || bad "public app_releases -> $c"
c=$(code "http://localhost:9000/$pub/other/smoke-probe.txt"); [ "$c" = "403" ] && ok "anonymous GET $pub outside app_releases/ -> 403" || bad "public other prefix -> $c"
c=$(code "http://localhost:9000/$pub/app_releases_x/smoke-probe.txt"); [ "$c" = "403" ] && ok "anonymous GET sibling prefix app_releases_x/ -> 403" || bad "sibling prefix -> $c"
c=$(code "http://localhost:9000/$pub?list-type=2&prefix=app_releases/"); [ "$c" = "403" ] && ok "anonymous list of $pub -> 403" || bad "anonymous list -> $c"

echo "rabbitmq"
# Credentials go to curl on stdin (-K -), not in its arguments.
n=$(printf 'user = "%s:%s"\n' "$(val RABBITMQ_DEFAULT_USER)" "$(val RABBITMQ_DEFAULT_PASS)" \
  | curl -s -K - http://localhost:15672/api/queues | grep -o '"name":"[^"]*"' | wc -l | tr -d ' ')
[ "$n" -ge 37 ] && ok "$n queues declared (16 work + 16 dead + 5 retry)" || bad "queues declared: $n"

echo "redis"
# T09: idempotency records and revocations must never be evicted (Appendix B §B.6.1).
rconf() { "${compose[@]}" exec -T redis redis-cli CONFIG GET "$1" 2>/dev/null | sed -n 2p || true; }
v=$(rconf maxmemory-policy); [ "$v" = "noeviction" ] && ok "maxmemory-policy noeviction" || bad "maxmemory-policy ${v:-unknown}"
v=$(rconf appendonly); [ "$v" = "yes" ] && ok "appendonly yes (AOF)" || bad "appendonly ${v:-unknown}"

echo "api"
c=$(code http://localhost:8080/readyz); [ "$c" = "200" ] && ok "internal /readyz 200" || bad "internal /readyz $c"
c=$(code http://localhost:8081/healthz); [ "$c" = "200" ] && ok "public /healthz 200" || bad "public /healthz $c"
c=$(code http://localhost:8081/readyz); [ "$c" = "404" ] && ok "public /readyz 404" || bad "public /readyz $c"

echo "env"
envs() { "${compose[@]}" exec -T "$1" env | cut -d= -f1 | grep -E 'DATABASE_URL$' | sort | tr '\n' ' '; }
for s in api worker scheduler; do
  e=$(envs "$s"); [ "$e" = "DATABASE_URL " ] && ok "$s gets only DATABASE_URL" || bad "$s DB URLs: $e"
done

echo
echo "smoke: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
