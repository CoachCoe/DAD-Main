#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$root/.env.production}"
[[ "$ENV_FILE" == /* ]] || ENV_FILE="$PWD/$ENV_FILE"
DATABASE_ENV_FILE="${DATABASE_ENV_FILE:-$root/.env.database}"
[[ "$DATABASE_ENV_FILE" == /* ]] || DATABASE_ENV_FILE="$PWD/$DATABASE_ENV_FILE"
export ENV_FILE
cd "$root"
compose() { docker compose --env-file "$ENV_FILE" --env-file "$DATABASE_ENV_FILE" -f docker-compose.prod.yml "$@"; }
usage() { printf '%s\n' 'Usage: scripts/deploy.sh {build|migrate|adopt|provision-roles|up|workers|monitor [--max-age DURATION]|reconcile [--date YYYY-MM-DD] [--max-lag DURATION]|rekey --key KEY [--dry-run]|staff ARGS...|reload-ingress|down|logs [SERVICE...]|smoke}'; }
# Every donor container receives ENV_FILE, so a database credential there would reach
# every process. Credentials belong in DATABASE_ENV_FILE, which Compose only interpolates.
check_env_files() {
  [[ -f "$DATABASE_ENV_FILE" ]] || { printf 'Missing %s; copy .env.database.example.\n' "$DATABASE_ENV_FILE" >&2; exit 1; }
  if grep -Eq '^[[:space:]]*(export[[:space:]]+)?(POSTGRES_PASSWORD|DONOR_[A-Z_]*DATABASE_URL)=' "$ENV_FILE"; then
    printf 'Move database credentials from %s to %s.\n' "$ENV_FILE" "$DATABASE_ENV_FILE" >&2
    exit 1
  fi
}
# nginx reads its bind-mounted files and renders its templates only at start, and a
# replaced single-file mount keeps showing the old file, so a reload can mix old and new
# configuration. Labelling nginx with a hash of what it mounts (certificates aside, which a
# reload picks up) makes Compose recreate it exactly when that changes.
ingress_config() {
  local sources=()
  mapfile -t sources < <(compose config nginx | awk '
    /^  [^ ]/ { nginx = $1 == "nginx:" }
    nginx && $2 == "type:" { bind = $3 == "bind" }
    nginx && bind && $1 == "source:" { sub(/^ *source: /, ""); source = $0 }
    nginx && bind && $1 == "target:" && $2 != "/etc/nginx/tls" { print source }')
  ((${#sources[@]})) || { echo 'Could not list the nginx mounts.' >&2; return 1; }
  INGRESS_CONFIG_HASH="$(find "${sources[@]}" -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum | sha256sum)"
  export INGRESS_CONFIG_HASH="${INGRESS_CONFIG_HASH%% *}"
}
workers_enabled() {
  compose --profile workers config stripe-worker | grep -q 'DONOR_GO_WORKERS_ENABLED: "true"'
}
[[ "${1:-help}" =~ ^(help|-h|--help)$ ]] || check_env_files
case "${1:-help}" in
  build) compose build api web ;;
  migrate) compose up -d --wait postgres; compose run --rm migrate ;;
  adopt) compose up -d --wait postgres; compose run --rm migrate migrate --adopt-existing ;;
  provision-roles) compose up -d --wait postgres; compose run --rm provision ;;
  up) ingress_config; compose up -d --wait api web nginx ;;
  workers)
    if ! workers_enabled; then
      printf 'Refusing to start workers: set DONOR_GO_WORKERS_ENABLED=true in %s first.\n' "$ENV_FILE" >&2
      exit 1
    fi
    compose --profile workers up -d stripe-worker projection-worker login-email-worker email-change-worker message-worker push-worker
    ;;
  monitor) shift; compose run --rm --no-deps monitor monitor "$@" ;;
  reconcile) shift; compose run --rm --no-deps reconcile reconcile "$@" ;;
  rekey) shift; compose run --rm --no-deps rekey rekey "$@" ;;
  staff) shift; compose run --rm --no-deps staff staff "$@" ;;
  # Recreates nginx if its configuration changed, then reloads it for renewed certificates.
  reload-ingress)
    ingress_config
    compose up -d --wait --no-deps nginx
    compose exec -T nginx sh -c 'nginx -t -q && nginx -s reload'
    ;;
  down) compose down ;;
  logs) shift; compose logs -f "$@" ;;
  smoke)
    compose exec -T api wget -q -O - http://127.0.0.1:8002/health/ready
    compose exec -T nginx wget -q -O /dev/null http://127.0.0.1:8081/health
    compose exec -T web node -e 'fetch("http://127.0.0.1:3000/").then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))'
    ;;
  help | -h | --help) usage ;;
  *) usage >&2; exit 2 ;;
esac
