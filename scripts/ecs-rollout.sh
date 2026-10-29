#!/usr/bin/env bash
#
# ecs-rollout.sh — force a new deployment of ECS services and wait for it to succeed.
#
# Usage:
#   scripts/ecs-rollout.sh <cluster> <service> [<service> ...]
#
# Environment (optional):
#   ROLLOUT_TIMEOUT_SECONDS  give up on a service after this long   (default 1200)
#   ROLLOUT_POLL_SECONDS     seconds between status checks          (default 15)
#
# For each service this starts a new deployment, remembers that deployment's id, then
# polls until THAT deployment's rolloutState is COMPLETED. It fails if the deployment
# ends FAILED or does not finish in time.
#
# Why the specific id, not "the current deployment": the services use the ECS
# deployment circuit breaker with rollback (DAD-infra apps/dad-main). When a rollout
# cannot reach a steady state, ECS rolls back by creating a NEW deployment of the
# previous task definition, and that rollback deployment is the one that reports
# COMPLETED. Asking "is the primary deployment COMPLETED" would pass after a failed
# rollout. The deployment this script started ends FAILED, which is what it checks.
#
# The health that decides COMPLETED vs FAILED comes from the container health checks in
# the task definitions, so no shell into a running task is needed.
set -euo pipefail

usage() {
  echo "usage: ecs-rollout.sh <cluster> <service> [<service> ...]" >&2
  exit 2
}

[ "$#" -ge 2 ] || usage
cluster="$1"
shift
services=("$@")

timeout="${ROLLOUT_TIMEOUT_SECONDS:-1200}"
interval="${ROLLOUT_POLL_SECONDS:-15}"

# Start every deployment first so they roll out in parallel, then wait on each.
deployment_ids=()
for svc in "${services[@]}"; do
  id="$(aws ecs update-service \
    --cluster "$cluster" \
    --service "$svc" \
    --force-new-deployment \
    --query "service.deployments[?status=='PRIMARY'] | [0].id" \
    --output text)"
  if [ -z "$id" ] || [ "$id" = "None" ]; then
    echo "::error::update-service for '$svc' returned no PRIMARY deployment id" >&2
    exit 1
  fi
  echo "started $svc deployment $id"
  deployment_ids+=("$id")
done

failed=0
for i in "${!services[@]}"; do
  svc="${services[$i]}"
  id="${deployment_ids[$i]}"
  deadline=$(($(date +%s) + timeout))
  while :; do
    state="$(aws ecs describe-services \
      --cluster "$cluster" \
      --services "$svc" \
      --query "services[0].deployments[?id=='$id'] | [0].rolloutState" \
      --output text)"
    case "$state" in
      COMPLETED)
        echo "$svc: deployment $id COMPLETED"
        break
        ;;
      FAILED)
        reason="$(aws ecs describe-services \
          --cluster "$cluster" \
          --services "$svc" \
          --query "services[0].deployments[?id=='$id'] | [0].rolloutStateReason" \
          --output text)"
        echo "::error::$svc: deployment $id FAILED and was rolled back: $reason" >&2
        failed=1
        break
        ;;
      *)
        # IN_PROGRESS, or not listed yet ("None").
        if [ "$(date +%s)" -ge "$deadline" ]; then
          echo "::error::$svc: deployment $id still '$state' after ${timeout}s" >&2
          failed=1
          break
        fi
        sleep "$interval"
        ;;
    esac
  done
done

exit "$failed"
