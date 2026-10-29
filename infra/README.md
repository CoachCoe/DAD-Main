# infra/ moved to the DAD-infra monorepo

The CDK infrastructure that used to live here was extracted into the shared
**DAD-infra** monorepo: https://github.com/CoachCoe/DAD-infra

- CDK app: `apps/dad-main/` within that repo (stacks, bin, tests).
- Shared config and GitHub OIDC primitives: `packages/config`, `packages/pipeline-iam`.
- Infra CI (`cdk synth`/`deploy`) now runs from DAD-infra's
  `.github/workflows/dad-main-infra.yml` (path-filtered to `apps/dad-main/**`).

What stays in DAD-Main:

- The application rollout pipeline `.github/workflows/deploy.yml` (ECR build/push
  + `aws ecs update-service`) — it is not CDK.
- The maintenance task runner moved from `infra/scripts/run-task.sh` to
  `scripts/run-task.sh`.
