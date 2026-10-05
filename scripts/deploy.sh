#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$root/.env.production}"
[[ "$ENV_FILE" == /* ]] || ENV_FILE="$PWD/$ENV_FILE"
export ENV_FILE
cd "$root"
compose() { docker compose --env-file "$ENV_FILE" -f docker-compose.prod.yml "$@"; }
usage() { printf '%s\n' 'Usage: scripts/deploy.sh {build|migrate|adopt|up|workers|monitor [--max-age DURATION]|down|logs [SERVICE...]|smoke}'; }
workers_enabled() {
  compose --profile workers config stripe-worker | grep -q 'DONOR_GO_WORKERS_ENABLED: "true"'
}
case "${1:-help}" in
  build) compose build api web ;;
  migrate) compose up -d --wait postgres; compose run --rm migrate ;;
  adopt) compose up -d --wait postgres; compose run --rm migrate migrate --adopt-existing ;;
  up) compose up -d --wait api web ;;
  workers)
    if ! workers_enabled; then
      printf 'Refusing to start workers: set DONOR_GO_WORKERS_ENABLED=true in %s first.\n' "$ENV_FILE" >&2
      exit 1
    fi
    compose --profile workers up -d stripe-worker projection-worker login-email-worker email-change-worker message-worker
    ;;
  monitor) shift; compose run --rm --no-deps api monitor "$@" ;;
  down) compose down ;;
  logs) shift; compose logs -f "$@" ;;
  smoke)
    compose exec -T api wget -q -O - http://127.0.0.1:8002/health/ready
    compose exec -T web node -e 'fetch("http://127.0.0.1:3000/").then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))'
    ;;
  help | -h | --help) usage ;;
  *) usage >&2; exit 2 ;;
esac
