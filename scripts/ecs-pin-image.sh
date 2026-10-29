#!/usr/bin/env bash
#
# ecs-pin-image.sh — register task-definition revisions pinned to one image tag.
#
# Usage:
#   scripts/ecs-pin-image.sh <image-tag> <family> [<family> ...]
#
# For each family this takes the latest ACTIVE revision, replaces the tag of every
# container image with <image-tag>, registers the result as a new revision, and prints
#
#   <family>=<new task definition ARN>
#
# on stdout (everything else goes to stderr). The file those lines are saved to is what
# ecs-rollout.sh and run-task.sh read through DAD_TASKDEFS_FILE.
#
# Why: the CDK task definitions reference a placeholder tag, and the pipeline pushes
# images tagged with the git SHA. An ECS service runs a specific task-definition revision,
# so deploying a new image means registering a revision that points at it. Pinning to the
# SHA (never a moving tag) also makes a circuit-breaker rollback restore the previous
# image instead of re-pulling the same tag.
#
# Everything except the image tag is copied from the current revision: command,
# environment, secrets, health check, log configuration, roles, sizing, and tags.
set -euo pipefail

usage() {
  echo "usage: ecs-pin-image.sh <image-tag> <family> [<family> ...]" >&2
  exit 2
}

[ "$#" -ge 2 ] || usage
tag="$1"
shift

# Docker tag grammar. Refuses anything that could alter the image reference.
if ! [[ "$tag" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]]; then
  echo "::error::invalid image tag '$tag'" >&2
  exit 2
fi

for family in "$@"; do
  current="$(aws ecs describe-task-definition \
    --task-definition "$family" \
    --include TAGS \
    --output json)"

  # Keep only the fields RegisterTaskDefinition accepts (describe also returns
  # read-only ones such as the ARN, revision, status, and registration time). Refuse an
  # image we cannot re-tag safely: a digest reference, or one with no tag.
  patched="$(printf '%s' "$current" | jq --arg tag "$tag" '
    . as $root
    | .taskDefinition
    | {
        family, taskRoleArn, executionRoleArn, networkMode, containerDefinitions,
        volumes, placementConstraints, requiresCompatibilities, cpu, memory,
        pidMode, ipcMode, proxyConfiguration, ephemeralStorage, runtimePlatform
      }
    | with_entries(select(.value != null))
    | .containerDefinitions |= map(
        .image |= (
          if test("@") or (test(":[^:/@]+$") | not)
          then error("cannot re-tag image reference: " + .)
          else sub(":[^:/@]+$"; ":" + $tag)
          end
        )
      )
    | . + (if (($root.tags // []) | length) > 0 then {tags: $root.tags} else {} end)
  ')"

  arn="$(aws ecs register-task-definition \
    --cli-input-json "$patched" \
    --query 'taskDefinition.taskDefinitionArn' \
    --output text)"
  if [ -z "$arn" ] || [ "$arn" = "None" ]; then
    echo "::error::registering a revision of '$family' returned no ARN" >&2
    exit 1
  fi
  echo "pinned $family -> $arn" >&2
  echo "$family=$arn"
done
