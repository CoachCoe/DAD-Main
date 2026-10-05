#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export ENV_FILE="${ENV_FILE:-.env.production}"
compose() { docker compose --env-file "$ENV_FILE" -f docker-compose.prod.yml "$@"; }
if [[ $# != 1 || -e "$1" ]]; then
  printf '%s\n' 'Usage: scripts/backup.sh /absolute/path/to/new-backup.dump (must not exist)' >&2
  exit 2
fi
case "$1" in /*) ;; *) printf '%s\n' 'Use an absolute backup path.' >&2; exit 2 ;; esac
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
