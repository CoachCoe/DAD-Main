# DAD AWS infrastructure (CDK)

AWS CDK (TypeScript) infrastructure-as-code for the DAD deployment described in
[`docs/aws-deployment.md`](../docs/aws-deployment.md). Target region is **us-east-2
(Ohio)**. Two environments, selected with `-c env=<name>`:

| env          | AWS account    | public host              | staff host                 | sending subdomain          |
| ------------ | -------------- | ------------------------ | -------------------------- | -------------------------- |
| `staging`    | `853377042897` | `app.staging.dynamicaid.org` | `staff.staging.dynamicaid.org` | `mail.staging.dynamicaid.org` |
| `production` | `216833405626` | `app.dynamicaid.org`         | `staff.dynamicaid.org`         | `mail.dynamicaid.org`         |

The workflow is: build the test on **staging**, then promote to **production** when
ready. CDK owns infrastructure only; the application rollout is a direct ECS
`update-service` (see [CI/CD](#cicd)).

## Layout

```
infra/
  bin/dad-infra.ts        # CDK app entrypoint; reads -c env and wires every stack
  lib/                    # one file per stack + config.ts + db-url.ts + task-factory.ts
  test/                   # jest + aws-cdk-lib/assertions unit tests
  scripts/run-task.sh     # wrapper around `aws ecs run-task` for maintenance tasks
```

## Selecting an environment

Every command takes `-c env=staging` or `-c env=production`. Staging is the default
when `-c env` is omitted. `bin/dad-infra.ts` resolves the account/region from the
selected config, but `CDK_DEFAULT_ACCOUNT` / `CDK_DEFAULT_REGION` win when they are
set — that is what makes offline synth work without real credentials.

## Offline verification (do this before any deploy)

All verification is **offline** — it never calls AWS. From `infra/`:

```bash
npm install
npm run build          # tsc, zero errors
npx jest               # unit tests for every stack + config + db-url
CDK_DEFAULT_ACCOUNT=111111111111 CDK_DEFAULT_REGION=us-east-2 npx cdk synth -c env=staging
CDK_DEFAULT_ACCOUNT=111111111111 CDK_DEFAULT_REGION=us-east-2 npx cdk synth -c env=production
```

The dummy account/region let `cdk synth` run with no AWS credentials. **Never run
`cdk deploy`, `aws`, or any live command as part of verification.** `cdk synth`
writes `infra/cdk.out`, which is gitignored — do not commit it.

## One-time bootstrap (per account)

These are manual, one-time steps an operator performs out of band. CDK does **not**
do them.

1. **CDK bootstrap** — `cdk bootstrap aws://<account>/us-east-2` once per account so
   the deploy roles can assume the `cdk-*` execution roles.

2. **Database owner role.** CDK does **not** create any Postgres roles — the
   `migrate` and `provision-roles` tasks do. But those tasks need an `owner` login
   that already exists. As an `rds_superuser`, run once against the RDS instance:

   ```sql
   -- Run as the RDS master (rds_superuser). Replace the password with the value
   -- stored in the owner-login Secrets Manager secret for this environment.
   CREATE ROLE owner LOGIN CREATEROLE PASSWORD '<owner-secret-password>';
   GRANT rds_superuser TO owner;          -- owner is a member of rds_superuser
   GRANT ALL PRIVILEGES ON DATABASE dad TO owner;
   ```

   `owner` is used only by `migrate`/`provision-roles` via `DONOR_GO_DATABASE_URL`;
   it is intentionally not one of the 11 per-process logins.

3. **GitHub Environments and OIDC roles** — see [CI/CD](#cicd).

## Maintenance tasks and run-task.sh

One-off maintenance tasks are ECS run-task task definitions (family
`dad-maint-<task>`), created by `lib/maintenance-stack.ts`. Invoke them with
`scripts/run-task.sh`, which wraps `aws ecs run-task` with the right network
configuration:

```bash
# Required env (resolve from CDK outputs / SSM, do not hardcode):
export DAD_CLUSTER=dad-staging
export DAD_SUBNETS=subnet-aaa,subnet-bbb        # compute-tier subnets
export DAD_SECURITY_GROUPS=sg-workers           # workers security group
export AWS_REGION=us-east-2

infra/scripts/run-task.sh staging migrate
infra/scripts/run-task.sh staging migrate --adopt-existing
infra/scripts/run-task.sh staging provision-roles
```

**Ordering matters: run `migrate` first, then `provision-roles`.** `migrate`
applies the schema; `provision-roles` then creates and grants the per-process
Postgres roles (all the `DONOR_*_DATABASE_URL` logins). Running provision-roles
before migrate will fail because the objects it grants on do not yet exist.

`reconcile` runs automatically on an EventBridge Scheduler cron (~00:30
America/New_York) and should not normally be run by hand.

## CI/CD

Two GitHub Actions workflows live in `.github/workflows/`. They are separate from
the existing `ci.yml` (which is left untouched). Both pin every action by full
commit SHA with a `# vX.Y.Z` comment, matching `ci.yml`.

### `infra.yml` — infrastructure (CDK)

- **PR touching `infra/**`** → `cdk diff` (read-only).
- **push to `dev`** → `cdk deploy -c env=staging` into the sandbox account.
- **release tag / push to `main`** → `cdk deploy -c env=production`, gated by the
  `production` GitHub Environment.

### `deploy.yml` — application rollout (direct ECS, not CDK)

Builds `dad/api`, `dad/web`, `dad/nginx`, pushes to ECR tagged with the git SHA,
then rolls ECS in the **exact** `scripts/deploy.sh` order:

```
migrate (run-task) → provision-roles (run-task)
  → aws ecs update-service api/web/nginx  (the "up" step)
  → smoke (api /health/ready, nginx /health:8081, web /:3000)
  → workers (update-service for the six worker services), gated on DONOR_GO_WORKERS_ENABLED
```

Cluster and service names are read from CDK CloudFormation outputs (or SSM as a
fallback) at runtime — never hardcoded. ECS service names match
`docker-compose.prod.yml` exactly: `api`, `web`, `nginx`, `stripe-worker`,
`projection-worker`, `login-email-worker`, `email-change-worker`,
`message-worker`, `push-worker`.

Staging deploys automatically on a merge to `dev`. Production deploys on a release
tag or merge to `main`, gated by manual approval on the `production` environment.

### Required GitHub Environments

Create two environments in the repository settings:

| environment  | protection rule          | purpose                              |
| ------------ | ------------------------ | ------------------------------------ |
| `staging`    | none (auto)              | sandbox deploys from `dev`           |
| `production` | **required reviewers** (manual approval) | gates every production deploy |

Add branch/tag restrictions on `production` so only `main` and release tags can
deploy. The manual-approval rule is configured in repo settings, not in the YAML.

### Required environment variables / secrets

Set these as **environment** variables (per environment) so account ids and ARNs
are not hardcoded in the workflows:

| name                     | used by                | value                                          |
| ------------------------ | ---------------------- | ---------------------------------------------- |
| `INFRA_DEPLOY_ROLE_ARN`  | `infra.yml`            | ARN of `dad-<env>-infra-deploy` (OIDC role)    |
| `APP_DEPLOY_ROLE_ARN`    | `deploy.yml`           | ARN of `dad-<env>-app-deploy` (OIDC role)      |
| `DONOR_PUBLIC_ORIGIN`    | `deploy.yml` web build | e.g. `https://app.staging.dynamicaid.org`      |
| `DONOR_STAFF_ORIGIN`     | `deploy.yml` web build | e.g. `https://staff.staging.dynamicaid.org`    |
| `DONOR_GO_WORKERS_ENABLED` | `deploy.yml`         | `true` to roll the six worker services         |

Auth is GitHub OIDC (no static AWS keys): the OIDC provider and the per-env
`infra-deploy` / `app-deploy` roles are created by `lib/pipeline-iam-stack.ts`. The
trust policy scopes staging to `refs/heads/dev` and production to the `production`
environment and release tags. Override the repo slug (default `dynamicaid/dad`)
with `-c githubRepo=owner/repo` when synthesising the pipeline-iam stack.

**Before bootstrap (no OIDC yet):** until `INFRA_DEPLOY_ROLE_ARN` is set, the
`infra.yml` PR job runs an offline `cdk synth` of both environments only — that
synth is the PR signal — and skips the credentialed `cdk diff`. Once the account
is bootstrapped and `INFRA_DEPLOY_ROLE_ARN` is populated, the PR job automatically
also runs a real `cdk diff`. The `deploy-*` jobs never run on a PR, so they do not
require credentials until the first merge/release after bootstrap.

## Email (SES)

`lib/ses-stack.ts` creates an SES domain identity for the per-env sending
subdomain (`mail[.staging].dynamicaid.org`) with Easy DKIM enabled, plus an IAM
user and an SMTP credential secret (`dad/<env>/ses/smtp`) mapped to the
`DONOR_SMTP_*` variables.

- **DNS records (Cloudflare):** after `cdk deploy`, read the stack's CfnOutputs for
  the three DKIM CNAME records (`DkimToken1..3` → `..._domainkey` CNAMEs) and the
  domain-verification record, and add them in Cloudflare. DNS is on Cloudflare; the
  NLB DNS name output from the edge stack is what the public/staff hostnames point
  at (proxied).
- **SES sandbox mode:** new SES identities start in **sandbox mode** — you can only
  send to verified addresses and volume is capped. For the test, verify the
  recipient addresses, or request production SES access before sending to arbitrary
  recipients. The stack emits a sandbox reminder output.
- **SMTP password:** the SMTP password stored in the secret is derived from the IAM
  access key's secret using the SES SMTP-password algorithm at bootstrap; it is not
  the raw secret access key.

## Web image rebuild on origin change

The public and staff origins (`DONOR_PUBLIC_ORIGIN`, `DONOR_STAFF_ORIGIN`) are
**build args of the `dad/web` image**, not runtime environment variables. If either
origin changes (for example promoting from staging to production hostnames), the
`dad/web` image **must be rebuilt** with the new build args and redeployed — a
restart with new env vars is not enough. `deploy.yml` passes these as
`--build-arg` from the environment variables above.

## Crown-jewel: encryption keys

`DONOR_EMAIL_ENCRYPTION_KEY` and `DONOR_CONTENT_ENCRYPTION_KEY` encrypt data at the
application layer. Treat them as crown jewels:

- **Back them up separately** from the database and from the rest of the secrets,
  in a location you control.
- **Rotate them by hand** (with the `rekey` maintenance task), never automatically.
- **Loss is unrecoverable:** if a key is lost, every column encrypted with it
  becomes permanently unreadable. There is no recovery path — the data is gone.

## Chosen production-typical defaults

Where the plan left a detail unspecified, these production-typical defaults were
chosen (see `lib/config.ts` and the FEAT-001 findings):

- **Task sizes:** staging api/web 512 CPU / 1024 MiB, nginx & worker 256 / 512;
  production api/web 1024 / 2048, nginx & worker 512 / 1024.
- **Desired counts:** staging all services 1; production api/web/nginx 2 (HA),
  workers 1.
- **Log retention:** staging 30 days, production 365 days.
- **Cloudflare ingress restriction** (`cloudflareIngressEnabled`): on for both
  environments; the nginx security group only admits Cloudflare ranges (sourced via
  `scripts/refresh-cloudflare-ips.sh`, not a hardcoded list).
- **Toolchain:** TypeScript 5.9.3, jest/ts-jest 29.x, aws-cdk-lib 2.275.0 — a
  known-stable combination for CDK v2.
- **Action pins:** `actions/checkout` v7.0.1, `actions/setup-node` v7.1.0 (matching
  `ci.yml`), `aws-actions/configure-aws-credentials` v6.3.0,
  `aws-actions/amazon-ecr-login` v2.1.7, `docker/setup-buildx-action` v4.4.1 — each
  pinned by full commit SHA.
