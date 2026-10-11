#!/usr/bin/env bash
# provision.sh: set up the shared staging VM showkhun.co for LogiTrack (deploy/vm/README.md, developer-spec.md
# §17.4). Run as root on the VM from a kit (deploy.sh --provision uploads one; deploy.sh --kit DIR builds
# one). Idempotent: every step checks first and changes only what is missing, so a second run reports
# "unchanged" everywhere. It touches only what belongs to LogiTrack: the logitrack user, /opt/logitrack,
# the logitrack database and the five R66 roles, the containers logitrack-redis and logitrack-rabbitmq,
# two nginx vhosts with their snippets, and the pm2-logitrack unit. Other apps' files, users, containers,
# vhosts and databases are never touched.
#
#   provision.sh [--dry-run]
#
# Secrets are generated on this server by tools/vmenv (bin/vmenv of the kit) and written only to
# /opt/logitrack/shared/.env (0600, owner logitrack) and the JWT key file; they reach psql, docker and curl
# through stdin or a temporary 0600 file, never through a process argument, and are never printed.
#
# --dry-run prints the plan and changes nothing (it only reads: user, files, database catalog, containers).
# --prefix DIR and --vmenv PATH exist for the kit's tests (deploy/vm/kit_test.go): every absolute path
# moves under DIR, and the commands are stubs.
set -euo pipefail

usage() {
  echo "usage: provision.sh [--dry-run]" >&2
  exit "${1:-2}"
}

dry=0
prefix=""
vmenv=""
while [ $# -gt 0 ]; do
  case $1 in
    --dry-run) dry=1 ;;
    --prefix)
      [ $# -ge 2 ] || usage
      prefix=$2
      shift
      ;;
    --vmenv)
      [ $# -ge 2 ] || usage
      vmenv=$2
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
deploy_dir=$(cd -P "$kit_dir/.." && pwd)
[ -n "$vmenv" ] || vmenv=$(cd -P "$kit_dir/../.." && pwd)/bin/vmenv

root=$prefix$kit_root
shared=$root/shared
env_file=$shared/.env
home_dir=$prefix/home/$kit_user
pm2_home=$home_dir/.pm2
node_dir=$prefix$kit_node_dir
nginx_dir=$prefix/etc/nginx
systemd_dir=$prefix/etc/systemd/system
unit=pm2-$kit_user
marker="Managed by logitrack-api/deploy/vm"
roles="'logitrack_migrator','logitrack_app','logitrack_etl','logitrack_readonly','logitrack_rls_definer'"

say() { printf 'provision: %s\n' "$*"; }
would() { printf 'provision: [dry-run] would %s\n' "$*"; }
die() {
  printf 'provision: %s\n' "$*" >&2
  exit 1
}
# run prints a command and executes it (only prints it under --dry-run). No command of this script carries
# a secret, so the transcript is safe to keep.
run() {
  if [ "$dry" = 1 ]; then
    would "run: $*"
  else
    say "+ $*"
    "$@"
  fi
}
psql_su() { runuser -u postgres -- psql -X -q -v ON_ERROR_STOP=1 "$@"; }
# pm2 as the app user with a clean environment (env -i): pm2 merges the caller's environment into its apps
# and its dump, so nothing of root's session may reach it.
as_user_pm2() {
  runuser -u "$kit_user" -- env -i HOME="$home_dir" PM2_HOME="$pm2_home" LANG=C.UTF-8 \
    PATH="$node_dir/bin:/usr/local/bin:/usr/bin:/bin" pm2 "$@"
}

tmp_files=""
cleanup() {
  # shellcheck disable=SC2086 # a space-separated list of mktemp paths
  [ -z "$tmp_files" ] || rm -f $tmp_files
}
trap cleanup EXIT
secret_tmp() {
  local f
  f=$(umask 077 && mktemp "${TMPDIR:-/tmp}/logitrack-provision.XXXXXX")
  tmp_files="$tmp_files $f"
  printf '%s\n' "$f"
}

# --- preflight ------------------------------------------------------------------------------------------
preflight() {
  [ "$(id -u)" = 0 ] || die "run as root (sudo bash provision.sh)"
  local c
  for c in docker curl runuser systemctl nginx ss psql useradd chown; do
    command -v "$c" >/dev/null 2>&1 || die "missing command: $c"
  done
  for c in "$kit_dir/env.vm.example" "$kit_dir/pm2-logitrack.service" "$deploy_dir/postgres-init/00-roles.sql" \
    "$deploy_dir/rabbitmq-definitions.json" "$kit_dir/nginx/logi.showkhun.co.conf"; do
    [ -r "$c" ] || die "kit file missing: $c"
  done
  [ -x "$vmenv" ] || die "missing $vmenv (the kit's linux/amd64 tools/vmenv; deploy.sh --kit builds it)"
  [ -x "$node_dir/bin/node" ] || die "missing $node_dir/bin/node (the node of the existing pm2 apps)"
  [ -x "$node_dir/bin/pm2" ] || die "missing $node_dir/bin/pm2 (pm2 of $node_dir)"
  [ -r "$prefix$kit_tls_cert" ] || die "missing the origin certificate $prefix$kit_tls_cert"
  [ -r "$prefix$kit_tls_key" ] || die "missing the origin key $prefix$kit_tls_key"
  [ -d "$nginx_dir/sites-enabled" ] || die "missing $nginx_dir/sites-enabled (nginx of Ubuntu's layout)"
  docker info >/dev/null 2>&1 || die "docker daemon not reachable"
  local v
  v=$(psql_su -tAc 'SHOW server_version_num') || die "PostgreSQL not reachable as the postgres superuser"
  [ "${v:-0}" -ge 180000 ] 2>/dev/null || die "PostgreSQL 18 is required (server_version_num $v)"
  say "preflight ok (PostgreSQL $v, node $("$node_dir/bin/node" --version 2>/dev/null || echo ?))"
}

# --- user and tree --------------------------------------------------------------------------------------
ensure_dir() { # path owner mode: created when missing; owner and mode enforced every run
  [ -d "$1" ] || run mkdir -p "$1"
  run chown "$2" "$1"
  run chmod "$3" "$1"
}

user_and_tree() {
  if id "$kit_user" >/dev/null 2>&1; then
    say "user $kit_user exists"
  else
    run useradd --system --user-group --create-home --home-dir "$home_dir" --shell /usr/sbin/nologin "$kit_user"
  fi
  ensure_dir "$root" root:root 0755
  ensure_dir "$root/releases" root:root 0755
  ensure_dir "$shared" "root:$kit_user" 0750
  ensure_dir "$shared/secrets" "$kit_user:$kit_user" 0700
  ensure_dir "$shared/media" "$kit_user:$kit_user" 0750
  ensure_dir "$shared/logs" "$kit_user:$kit_user" 0750
}

# --- .env and the JWT key -------------------------------------------------------------------------------
render_env() {
  local args=(render -template "$kit_dir/env.vm.example" -out "$env_file" -prefix "$prefix"
    -pg-addr "$kit_pg_addr" -pg-db "$kit_pg_db" -redis-addr "127.0.0.1:$kit_redis_port"
    -amqp-addr "127.0.0.1:$kit_amqp_port")
  if [ "$dry" = 1 ]; then
    "$vmenv" "${args[@]}" -dry-run
    return
  fi
  "$vmenv" "${args[@]}"
  chown "$kit_user:$kit_user" "$env_file"
  chmod 0600 "$env_file"
  local key
  key=$(sed -n 's/^JWT_SIGNING_KEY_FILE=//p' "$env_file" | head -n 1)
  if [ -n "$key" ] && [ -e "$prefix$key" ]; then
    chown "$kit_user:$kit_user" "$prefix$key"
    chmod 0600 "$prefix$key"
  fi
}

# --- PostgreSQL: database, roles, passwords -------------------------------------------------------------
pg_setup() {
  local db n
  db=$(psql_su -tAc "SELECT 1 FROM pg_database WHERE datname = '$kit_pg_db'")
  if [ "$db" = 1 ]; then
    say "database $kit_pg_db exists"
  else
    run psql_su -c "CREATE DATABASE $kit_pg_db TEMPLATE template0 ENCODING 'UTF8'"
  fi
  n=$(psql_su -tAc "SELECT count(*) FROM pg_roles WHERE rolname IN ($roles)")
  case $n in
    0)
      # On psql's stdin: the kit may sit where the postgres user cannot read (a temporary directory of root).
      if [ "$dry" = 1 ]; then
        would "run $deploy_dir/postgres-init/00-roles.sql inside $kit_pg_db as the superuser"
      else
        say "+ psql -d $kit_pg_db < 00-roles.sql"
        psql_su -d "$kit_pg_db" <"$deploy_dir/postgres-init/00-roles.sql"
      fi
      ;;
    5) say "the five R66 roles exist" ;;
    *) die "only $n of the five R66 roles exist; inspect pg_roles (logitrack_*) before running again" ;;
  esac
  # 00-roles.sql makes the migrator own the database it runs in; repeat it for a database created later.
  run psql_su -c "ALTER DATABASE $kit_pg_db OWNER TO logitrack_migrator"
  # The cluster is shared: only the LogiTrack roles may connect to this database.
  run psql_su -c "REVOKE CONNECT ON DATABASE $kit_pg_db FROM PUBLIC"
  run psql_su -c "GRANT CONNECT ON DATABASE $kit_pg_db TO logitrack_app, logitrack_etl, logitrack_readonly"
  if [ "$dry" = 1 ]; then
    would "set the LOGIN passwords of logitrack_app, logitrack_migrator, logitrack_etl from the .env URLs (SCRAM verifiers on psql's stdin)"
  else
    "$vmenv" roles-sql -env "$env_file" | psql_su -d "$kit_pg_db"
    say "LOGIN passwords of logitrack_app, logitrack_migrator, logitrack_etl set (SCRAM verifiers, nothing logged)"
  fi
}

# --- containers -----------------------------------------------------------------------------------------
port_free() { # port
  if [ -n "$(ss -H -ltn "sport = :$1" 2>/dev/null)" ]; then
    die "port $1 is already in use on this host; free it or change kit.conf"
  fi
}

# container_ready name image: 0 when the container runs that image (or was started again), 1 to create it.
container_ready() {
  local state
  state=$(docker inspect -f '{{.Config.Image}} {{.State.Running}}' "$1" 2>/dev/null || true)
  case $state in
    "$2 true")
      say "container $1 is running ($2)"
      return 0
      ;;
    "$2 false")
      run docker start "$1"
      return 0
      ;;
    "") return 1 ;;
    *)
      say "container $1 runs another image; recreating it (its named volume is kept)"
      run docker rm -f "$1"
      return 1
      ;;
  esac
}

wait_healthy() { # name seconds
  [ "$dry" = 1 ] && return 0
  local i=0 status
  while [ "$i" -lt "$2" ]; do
    status=$(docker inspect -f '{{.State.Health.Status}}' "$1" 2>/dev/null || true)
    [ "$status" = healthy ] && {
      say "container $1 is healthy"
      return 0
    }
    sleep 2
    i=$((i + 2))
  done
  die "container $1 is not healthy after $2 s (docker logs $1)"
}

# The Redis password comes from REDISCLI_AUTH (the container's env-file): the shell inside the container
# writes `requirepass` into redis-server's stdin config, so it never appears in a command line, and
# redis-cli (the healthcheck) reads the same variable.
# shellcheck disable=SC2016 # $REDISCLI_AUTH expands in the container's shell, not here
redis_cmd='exec redis-server --appendonly yes --maxmemory-policy noeviction --maxmemory 384mb - <<EOF
requirepass $REDISCLI_AUTH
EOF'

redis_setup() {
  container_ready logitrack-redis "$kit_redis_image" && return 0
  port_free "$kit_redis_port"
  if [ "$dry" = 1 ]; then
    would "start container logitrack-redis ($kit_redis_image) on 127.0.0.1:$kit_redis_port, volume logitrack-redis-data, AOF, noeviction, requirepass from REDIS_URL"
    return 0
  fi
  local envf
  envf=$(secret_tmp)
  "$vmenv" container-env -env "$env_file" -service redis >"$envf"
  docker run -d --name logitrack-redis --hostname logitrack-redis --restart unless-stopped --user redis \
    --memory 512m -p "127.0.0.1:$kit_redis_port:6379" -v logitrack-redis-data:/data --env-file "$envf" \
    --health-cmd 'redis-cli ping | grep -q PONG' --health-interval 10s --health-timeout 5s --health-retries 5 \
    "$kit_redis_image" sh -c "$redis_cmd" >/dev/null
  rm -f "$envf"
  say "container logitrack-redis started"
}

rabbitmq_setup() {
  if ! container_ready logitrack-rabbitmq "$kit_rabbitmq_image"; then
    port_free "$kit_amqp_port"
    port_free "$kit_amqp_mgmt_port"
    if [ "$dry" = 1 ]; then
      would "start container logitrack-rabbitmq ($kit_rabbitmq_image, hostname logitrack-rabbitmq) on 127.0.0.1:$kit_amqp_port and 127.0.0.1:$kit_amqp_mgmt_port, volume logitrack-rabbitmq-data"
    else
      local envf
      envf=$(secret_tmp)
      "$vmenv" container-env -env "$env_file" -service rabbitmq >"$envf"
      # The fixed hostname keeps the node name (rabbit@logitrack-rabbitmq), so a recreated container
      # finds its queues in the volume.
      docker run -d --name logitrack-rabbitmq --hostname logitrack-rabbitmq --restart unless-stopped \
        --memory 1g -p "127.0.0.1:$kit_amqp_port:5672" -p "127.0.0.1:$kit_amqp_mgmt_port:15672" \
        -v logitrack-rabbitmq-data:/var/lib/rabbitmq --env-file "$envf" \
        --health-cmd 'rabbitmq-diagnostics -q ping' --health-interval 10s --health-timeout 10s \
        --health-retries 6 --health-start-period 60s "$kit_rabbitmq_image" >/dev/null
      rm -f "$envf"
      say "container logitrack-rabbitmq started"
    fi
  fi
  wait_healthy logitrack-rabbitmq 180
  # Appendix B §B.5 topology through the management API; credentials on curl's stdin (-K -).
  local api="http://127.0.0.1:$kit_amqp_mgmt_port/api" i=0
  if [ "$dry" = 1 ]; then
    would "import $deploy_dir/rabbitmq-definitions.json through $api/definitions (credentials on curl's stdin)"
    return 0
  fi
  until "$vmenv" amqp-auth -env "$env_file" | curl -fsS -o /dev/null -K - "$api/overview"; do
    i=$((i + 1))
    [ "$i" -ge 30 ] && die "RabbitMQ management API not reachable on 127.0.0.1:$kit_amqp_mgmt_port"
    sleep 2
  done
  "$vmenv" amqp-auth -env "$env_file" | curl -fsS -o /dev/null -K - -H 'content-type: application/json' \
    -X POST --data-binary "@$deploy_dir/rabbitmq-definitions.json" "$api/definitions"
  say "RabbitMQ definitions imported (exchanges, quorum queues, DLQs)"
}

# --- nginx ----------------------------------------------------------------------------------------------
backups=""
installed=""
# foreign dest: fails when dest exists and was not written by this kit.
foreign() {
  if [ -e "$1" ] && ! grep -q "$marker" "$1"; then
    die "$1 exists and is not managed by this kit: refusing to touch it"
  fi
}

# install_managed src dest: refuses a destination this kit does not manage; keeps a backup for rollback.
install_managed() {
  local src=$1 dest=$2
  foreign "$dest"
  if [ -e "$dest" ] && cmp -s "$src" "$dest"; then
    say "$dest unchanged"
    return 0
  fi
  if [ "$dry" = 1 ]; then
    would "install $dest"
    return 0
  fi
  mkdir -p "$(dirname "$dest")"
  if [ -e "$dest" ]; then
    cp -p "$dest" "$dest.logitrack-prev"
    backups="$backups $dest"
  else
    installed="$installed $dest"
  fi
  cp "$src" "$dest"
  chmod 0644 "$dest"
  say "installed $dest"
}

restore_nginx() {
  local f
  for f in $backups; do mv -f "$f.logitrack-prev" "$f"; done
  for f in $installed; do rm -f "$f"; done
  for f in "$nginx_dir/sites-enabled/$kit_web_host" "$nginx_dir/sites-enabled/$kit_api_host"; do
    if [ -L "$f" ] && [ ! -e "$f" ]; then rm -f "$f"; fi
  done
}

nginx_sites() {
  local host f other
  for host in "$kit_web_host" "$kit_api_host"; do
    # Another vhost already serving the name would make one of them unreachable.
    for f in "$nginx_dir"/sites-enabled/* "$nginx_dir"/conf.d/*.conf; do
      [ -e "$f" ] || continue
      case $(basename "$f") in "$kit_web_host" | "$kit_api_host") continue ;; esac
      if grep -Eq "server_name[^;]*[[:space:]]${host//./\\.}([[:space:];]|$)" "$f"; then
        other=$f
        die "$other already declares server_name $host; resolve that before provisioning"
      fi
    done
  done
  # Every destination is checked before the first file is written.
  local pairs="logitrack-cloudflare-realip.conf:snippets/logitrack-cloudflare-realip.conf
logitrack-proxy.conf:snippets/logitrack-proxy.conf
logitrack-log-format.conf:conf.d/logitrack-log-format.conf
$kit_web_host.conf:sites-available/$kit_web_host
$kit_api_host.conf:sites-available/$kit_api_host" pair
  for pair in $pairs; do foreign "$nginx_dir/${pair#*:}"; done
  for host in "$kit_web_host" "$kit_api_host"; do
    f=$nginx_dir/sites-enabled/$host
    if [ -e "$f" ] && [ ! -L "$f" ]; then die "$f exists and is not a symlink: refusing to touch it"; fi
  done
  for pair in $pairs; do install_managed "$kit_dir/nginx/${pair%%:*}" "$nginx_dir/${pair#*:}"; done
  for host in "$kit_web_host" "$kit_api_host"; do
    f=$nginx_dir/sites-enabled/$host
    if [ -L "$f" ]; then
      say "$f enabled"
    else
      run ln -s "$nginx_dir/sites-available/$host" "$f"
      [ "$dry" = 1 ] || installed="$installed $f"
    fi
  done
  if [ "$dry" = 1 ]; then
    would "run nginx -t, then systemctl reload nginx"
    return 0
  fi
  if [ -z "$backups$installed" ]; then
    say "nginx configuration unchanged"
    return 0
  fi
  if ! nginx -t; then
    restore_nginx
    die "nginx -t failed: the previous logitrack files are restored and nginx was not reloaded"
  fi
  systemctl reload nginx
  for f in $backups; do rm -f "$f.logitrack-prev"; done
  say "nginx reloaded"
}

# --- pm2 ------------------------------------------------------------------------------------------------
pm2_unit() {
  local rendered
  rendered=$(mktemp)
  tmp_files="$tmp_files $rendered"
  sed -e "s#@kit_user@#$kit_user#g" -e "s#@kit_node_dir@#$kit_node_dir#g" "$kit_dir/pm2-logitrack.service" >"$rendered"
  local before=$installed$backups
  install_managed "$rendered" "$systemd_dir/$unit.service"
  # An empty dump lets `pm2 resurrect` (ExecStart) start a daemon before the first deploy saves one.
  if [ ! -e "$pm2_home/dump.pm2" ]; then
    run mkdir -p "$pm2_home"
    if [ "$dry" = 1 ]; then would "write an empty $pm2_home/dump.pm2"; else printf '[]\n' >"$pm2_home/dump.pm2"; fi
    run chown -R "$kit_user:$kit_user" "$pm2_home"
  fi
  if [ "$installed$backups" != "$before" ]; then
    run systemctl daemon-reload
  fi
  run systemctl enable --now "$unit"
  if [ -d "$pm2_home/modules/pm2-logrotate" ]; then
    say "pm2-logrotate is installed for $kit_user"
  else
    run as_user_pm2 install pm2-logrotate
  fi
  run as_user_pm2 set pm2-logrotate:max_size 50M
  run as_user_pm2 set pm2-logrotate:retain 14
  run as_user_pm2 set pm2-logrotate:compress true
}

# The app ports are taken by the LogiTrack apps once deployed; before the first deploy any listener there
# belongs to something else and would stop the matching pm2 app.
check_app_ports() {
  local p used
  for p in "$kit_web_port" "$kit_api_internal_port" "$kit_api_public_port" "$kit_api_metrics_port" \
    "$kit_worker_metrics_port" "$kit_scheduler_metrics_port"; do
    used=$(ss -H -ltnp "sport = :$p" 2>/dev/null || true)
    [ -z "$used" ] || say "port $p is in use (${used##* }); fine if that is a LogiTrack app"
  done
}

main() {
  # psql and pm2 run as other users, which may not enter root's working directory.
  cd /
  [ "$dry" = 1 ] && say "dry run: nothing is changed"
  preflight
  user_and_tree
  render_env
  pg_setup
  redis_setup
  wait_healthy logitrack-redis 60
  rabbitmq_setup
  nginx_sites
  pm2_unit
  check_app_ports
  say "done. Next: place the optional Firebase/FCM files, then deploy.sh --web-build-env FILE (README)."
  say "the bootstrap admin password: grep BOOTSTRAP_ADMIN_PASSWORD $kit_root/shared/.env (as root)"
}

main
