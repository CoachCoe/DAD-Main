#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export ENV_FILE="${ENV_FILE:-.env.production}"
compose() { docker compose --env-file "$ENV_FILE" -f docker-compose.prod.yml "$@"; }
case "${1:-help}" in
  build) compose build api web ;;
  migrate) compose up -d --wait postgres; compose run --rm migrate ;;
  adopt) compose up -d --wait postgres; compose run --rm migrate migrate --adopt-existing ;;
  up) compose up -d --wait api web ;;
  workers) compose --profile workers up -d stripe-worker projection-worker login-email-worker message-worker ;;
  down) compose down ;;
  logs) shift; compose logs -f "$@" ;;
  smoke)
    compose exec -T api wget -q -O - http://127.0.0.1:8002/health/ready
    compose exec -T web node -e 'fetch("http://127.0.0.1:3000/").then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))'
    ;;
  *) printf '%s\n' 'Usage: scripts/deploy.sh {build|migrate|adopt|up|workers|down|logs|smoke}' ;;
esac
