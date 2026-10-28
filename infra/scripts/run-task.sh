#!/usr/bin/env bash
#
# run-task.sh — thin wrapper around `aws ecs run-task` for the DAD maintenance
# task definitions created by infra/lib/maintenance-stack.ts.
#
# This script is DOCUMENTED, NOT AUTO-RUN by CDK. An operator invokes it by hand
# (or the app pipeline invokes `aws ecs run-task` directly in the same shape).
#
# Deploy ordering (mirrors scripts/deploy.sh): run `migrate` first, then
# `provision-roles`, before bringing services up. The reconcile task runs on an
# EventBridge Scheduler cron and should not normally be run by hand.
#
# Usage:
#   infra/scripts/run-task.sh <env> <task> [extra container command args...]
#
#   <env>   staging | production
#   <task>  migrate | provision-roles | staff | rekey | monitor | reconcile
#
# Examples:
#   infra/scripts/run-task.sh staging migrate
#   infra/scripts/run-task.sh staging migrate --adopt-existing
#   infra/scripts/run-task.sh staging provision-roles
#
# Required environment / resolved values (read from CDK outputs or SSM, not
# hardcoded here):
#   DAD_CLUSTER            ECS cluster name            (e.g. dad-staging)
#   DAD_SUBNETS            comma-separated subnet ids  (compute tier)
#   DAD_SECURITY_GROUPS    comma-separated SG ids      (workers SG)
#   AWS_REGION             us-east-2
#
set -euo pipefail

env="${1:?usage: run-task.sh <env> <task> [args...]}"
task="${2:?usage: run-task.sh <env> <task> [args...]}"
shift 2 || true

case "$env" in
  staging | production) ;;
  *) printf 'Unknown env %q (expected staging|production)\n' "$env" >&2; exit 2 ;;
esac

case "$task" in
  migrate | provision-roles | staff | rekey | monitor | reconcile) ;;
  *) printf 'Unknown task %q\n' "$task" >&2; exit 2 ;;
esac

: "${DAD_CLUSTER:?set DAD_CLUSTER (ECS cluster name)}"
: "${DAD_SUBNETS:?set DAD_SUBNETS (comma-separated compute subnet ids)}"
: "${DAD_SECURITY_GROUPS:?set DAD_SECURITY_GROUPS (comma-separated workers SG ids)}"
: "${AWS_REGION:?set AWS_REGION (us-east-2)}"

# Task definition family follows the CDK convention: dad-maint-<task>.
family="dad-maint-${task}"

# Build an optional command override when extra args are supplied (e.g.
# `migrate --adopt-existing`). The container name follows the factory id.
overrides=""
if [ "$#" -gt 0 ]; then
  args_json="$(printf '%s\n' "$@" | awk '{ printf "%s\"%s\"", (NR>1?",":""), $0 }')"
  overrides="--overrides {\"containerOverrides\":[{\"name\":\"maint-${task}-Container\",\"command\":[\"${task}\",${args_json}]}]}"
fi

network="awsvpcConfiguration={subnets=[${DAD_SUBNETS}],securityGroups=[${DAD_SECURITY_GROUPS}],assignPublicIp=DISABLED}"

set -x
# shellcheck disable=SC2086
aws ecs run-task \
  --cluster "$DAD_CLUSTER" \
  --launch-type FARGATE \
  --task-definition "$family" \
  --network-configuration "{\"awsvpcConfiguration\":{\"subnets\":[${DAD_SUBNETS}],\"securityGroups\":[${DAD_SECURITY_GROUPS}],\"assignPublicIp\":\"DISABLED\"}}" \
  --region "$AWS_REGION" \
  $overrides
