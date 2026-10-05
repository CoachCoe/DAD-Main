#!/usr/bin/env bash
# shellcheck disable=SC2016 # Single-quoted variables expand inside the postgres container.
set -euo pipefail
umask 077
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$root/.env.production}"
[[ "$ENV_FILE" == /* ]] || ENV_FILE="$PWD/$ENV_FILE"
export ENV_FILE
cd "$root"
compose() { docker compose --env-file "$ENV_FILE" -f docker-compose.prod.yml "$@"; }
if [[ $# != 1 || "$1" != /* || -e "$1" ]]; then
  printf '%s\n' 'Usage: scripts/backup.sh /absolute/path/to/new-backup.dump (must not exist)' >&2
  exit 2
fi
backup="$1"
# Noclobber prevents concurrent invocations from replacing an existing backup.
set -C
exec 3>"$backup"
complete=false
cleanup() { if [[ "$complete" != true ]]; then rm -f -- "$backup"; fi; }
trap cleanup EXIT
compose exec -T postgres sh -c 'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner --no-acl' >&3
exec 3>&-
compose exec -T postgres pg_restore --list < "$backup" > /dev/null
complete=true
printf 'Backup created: %s\n' "$backup"
