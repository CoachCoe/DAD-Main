# Implementation Plan — AWS CDK foundation (DAD)

Build the CDK (TypeScript) IaC for the DAD AWS deployment described in
`docs/aws-deployment.md`. Target `us-east-2`, accounts staging `853377042897` /
production `216833405626`, env selected by `-c env=staging|production`. This plan builds
the IaC only. **Do not run `cdk deploy`, `aws`, or any live command.** All verification is
offline: `npm install`, `npm run build` (tsc), `npx cdk synth -c env=staging` and
`-c env=production` with dummy account/region, and unit tests. No real AWS calls.

All work lives under `infra/` in the worktree
`/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/aws-cdk-foundation`. Use absolute
paths; relative paths would land in the parent workspace.

Values below marked **(confirmed from repo)** were read from the actual files during
exploration.

## Confirmed facts the implementation must encode

**Long-running ECS services — exactly 9 (confirmed from `docker-compose.prod.yml`):**

1. `api` — image `dad/api`, CMD `api` (image default), port **8002**, env
   `DONOR_GO_LISTEN=0.0.0.0:8002`, `DONOR_PUBLIC_ORIGIN`, `DONOR_STAFF_ORIGIN`,
   `DONOR_API_DATABASE_URL`, `DONOR_TRUSTED_PROXY_CIDRS`=web subnet CIDR, mail-queue
   (`DONOR_SMTP_HOST`, `DONOR_SMTP_PORT`, `DONOR_EMAIL_FROM`,
   `DONOR_EMAIL_ENCRYPTION_KEY`), `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`,
   `DONOR_STRIPE_REQUESTS_PER_SECOND`, `DONOR_CONTENT_ENCRYPTION_KEY`, and the optional
   secrets (`DONOR_EVENT_CHECKIN_SECRET`, `DONOR_FOUNDING_CLAIM_KEY`, `DONOR_SMS_*`,
   `DONOR_OIDC_*`, `DONOR_WALLET_*`, `DONOR_PUSH_*`, `DONOR_SUPPORT_EMAIL`). Health:
   `/health/live`, `/health/ready`.
2. `web` — image `dad/web`, CMD `node server.js` (image default), port **3000**, env
   `DONOR_API_BASE_URL=http://api:8002`, `DONOR_CLIENT_IP_HEADER=X-Real-IP`. Public/staff
   origins are **build args** of the image, not runtime env.
3. `nginx` — image `dad/nginx` (built from `nginx:1.30.5-alpine` + `deploy/nginx`), listens
   **8080/8443**, health **8081** `/health`, user `101:101`, env
   `INGRESS_MODE=cloudflare`, `DONOR_PUBLIC_ORIGIN`, `DONOR_STAFF_ORIGIN`,
   `NGINX_ENVSUBST_FILTER=^INGRESS_`, `NGINX_ENTRYPOINT_QUIET_LOGS=1`.
4. `stripe-worker` — `dad/api`, CMD `worker --kind stripe`, env `DONOR_GO_WORKERS_ENABLED`,
   `STRIPE_SECRET_KEY`, `DONOR_STRIPE_REQUESTS_PER_SECOND`,
   `DONOR_WORKER_STRIPE_DATABASE_URL`.
5. `projection-worker` — `dad/api`, CMD `worker --kind projections`, env
   `DONOR_GO_WORKERS_ENABLED`, `DONOR_WORKER_PROJECTIONS_DATABASE_URL`.
6. `login-email-worker` — `dad/api`, CMD `worker --kind login-email`, env
   `DONOR_GO_WORKERS_ENABLED`, `DONOR_PUBLIC_ORIGIN`, mail-transport (`DONOR_SMTP_HOST`,
   `DONOR_SMTP_PORT`, `DONOR_EMAIL_FROM`, `DONOR_EMAIL_ENCRYPTION_KEY`,
   `DONOR_SMTP_USERNAME`, `DONOR_SMTP_PASSWORD`, `DONOR_SMTP_CA`),
   `DONOR_WORKER_LOGIN_EMAIL_DATABASE_URL`.
7. `email-change-worker` — `dad/api`, CMD `worker --kind email-changes`, env as
   login-email + `DONOR_WORKER_EMAIL_CHANGES_DATABASE_URL`.
8. `message-worker` — `dad/api`, CMD `worker --kind messages`, env as login-email +
   `DONOR_WORKER_MESSAGES_DATABASE_URL`.
9. `push-worker` — `dad/api`, CMD `worker --kind push`, env `DONOR_GO_WORKERS_ENABLED`,
   push-delivery (`DONOR_PUSH_APNS_KEY_FILE`, `DONOR_PUSH_APNS_KEY_ID`,
   `DONOR_PUSH_APNS_TEAM_ID`, `DONOR_PUSH_APNS_TOPIC`, `DONOR_PUSH_FCM_KEY_FILE`),
   `DONOR_WORKER_PUSH_DATABASE_URL`.

Note (confirmed): service name ≠ `--kind`. `login-email-worker`→`login-email`,
`email-change-worker`→`email-changes`, `message-worker`→`messages`.

**Maintenance one-off tasks (confirmed from Compose `maintenance` profile +
`scripts/deploy.sh`):**

- `migrate` — CMD `migrate`, env `DONOR_GO_DATABASE_URL`. Variant `migrate --adopt-existing`.
- `provision-roles` — CMD `provision`, env owner URL + all per-process `DONOR_*_DATABASE_URL`.
  Runs **after** migrate.
- `staff` — CMD `staff`, env `DONOR_STAFF_ORIGIN`, `DONOR_STAFF_CLI_DATABASE_URL`.
- `rekey` — CMD `rekey`, env `DONOR_EMAIL_ENCRYPTION_KEY`, `DONOR_CONTENT_ENCRYPTION_KEY`,
  `DONOR_REKEY_DATABASE_URL`.
- `monitor` — CMD `monitor`, env `DONOR_MONITOR_DATABASE_URL`.

**Scheduled task (confirmed from Compose + `docs/operations.md`):** `reconcile` — CMD
`reconcile`, env `STRIPE_SECRET_KEY`, `DONOR_STRIPE_REQUESTS_PER_SECOND`,
`DONOR_RECONCILE_DATABASE_URL`. EventBridge Scheduler daily ~00:30 America/New_York;
non-zero exit on discrepancy → CloudWatch alarm → SNS.

**Per-process DB URL (confirmed from `.env.database.example` +
`docs/operations.md#connection-pooling`):**

```
postgresql://<login>:<password>@<rds-proxy-endpoint>:5432/<db>?default_query_exec_mode=exec&sslmode=verify-full
```

- `<db>` default `dad`. Login names (confirmed): `dad_api_login`, `dad_worker_stripe_login`,
  `dad_worker_projections_login`, `dad_worker_login_email_login`,
  `dad_worker_email_changes_login`, `dad_worker_messages_login`, `dad_worker_push_login`,
  `dad_monitor_login`, `dad_staff_cli_login`, `dad_rekey_login`, `dad_reconcile_login`, and
  owner login for `DONOR_GO_DATABASE_URL`.
- URL→var mapping (confirmed from `docs/operations.md#database-roles`): `DONOR_API_DATABASE_URL`,
  `DONOR_WORKER_STRIPE_DATABASE_URL`, `DONOR_WORKER_PROJECTIONS_DATABASE_URL`,
  `DONOR_WORKER_LOGIN_EMAIL_DATABASE_URL`, `DONOR_WORKER_EMAIL_CHANGES_DATABASE_URL`,
  `DONOR_WORKER_MESSAGES_DATABASE_URL`, `DONOR_WORKER_PUSH_DATABASE_URL`,
  `DONOR_MONITOR_DATABASE_URL`, `DONOR_STAFF_CLI_DATABASE_URL`, `DONOR_REKEY_DATABASE_URL`,
  `DONOR_RECONCILE_DATABASE_URL`, `DONOR_GO_DATABASE_URL` (owner, migrate/provision only).

## Ordered implementation items

- [ ] 1. Scaffold the CDK TypeScript app under `infra/`.
      Create `package.json` (deps: `aws-cdk-lib`, `constructs`, dev: `aws-cdk`,
      `typescript`, `ts-node`, `jest`, `ts-jest`, `@types/*`; pin exact versions),
      `tsconfig.json`, `cdk.json` (app `npx ts-node --prefer-ts-exts bin/dad-infra.ts`,
      context defaults), `jest.config.js`, `.gitignore` (`node_modules`, `cdk.out`), and
      `bin/dad-infra.ts` that reads `-c env` and instantiates stacks.
      Files: `infra/package.json`, `infra/tsconfig.json`, `infra/cdk.json`,
      `infra/jest.config.js`, `infra/.gitignore`, `infra/bin/dad-infra.ts`.
      Verify: `cd infra && npm install && npm run build` compiles with no errors.

- [ ] 2. Add the typed per-env config module.
      Define an `EnvConfig` interface (fields: `env`, `account`, `region`, `publicHost`,
      `staffHost`, `publicOrigin`, `staffOrigin`, `dbName`, `multiAz`,
      `deletionProtection`, `logRetentionDays`, desired counts
      `{api, web, nginx, workers}`, task sizes `{cpu, memory}` per tier, `sendingSubdomain`,
      `cloudflareIngressEnabled`) and two concrete objects: staging (account
      `853377042897`, hosts `app.staging.dynamicaid.org`/`staff.staging.dynamicaid.org`,
      `multiAz=false`, `deletionProtection=false`, logRetention 30, all counts 1) and
      production (account `216833405626`, hosts `app.dynamicaid.org`/`staff.dynamicaid.org`,
      `multiAz=true`, `deletionProtection=true`, longer retention). Export a
      `getConfig(env)` with a dummy-account fallback for synth.
      Files: `infra/lib/config.ts`.
      Verify: `npm run build`; add `infra/test/config.test.ts` asserting both envs resolve
      the exact accounts/hosts; `npx jest` passes.

- [ ] 3. Add a DB URL builder helper and its unit test.
      Implement `buildDatabaseUrl({login, password, proxyEndpoint, db})` returning the
      exact string with `?default_query_exec_mode=exec&sslmode=verify-full`, and a map of
      process→(login name, URL env var) covering all 11 process logins + owner.
      Files: `infra/lib/db-url.ts`, `infra/test/db-url.test.ts`.
      Verify: `npx jest` — test asserts the literal query string and every env var name.

- [ ] 4. NetworkStack — VPC per env.
      2+ AZ VPC with public, private-with-egress (NAT), and isolated subnet groups; the SG
      chain NLB→nginx(8080/8443)→web(3000)→api(8002)→RDS Proxy(5432)→RDS(5432); workers SG
      with no inbound. Expose SGs and subnets as stack props.
      Files: `infra/lib/network-stack.ts`, `infra/bin/dad-infra.ts` (wire in).
      Verify: `npx cdk synth -c env=staging` and `-c env=production` (dummy acct/region)
      emit the VPC and SG ingress rules; add `infra/test/network-stack.test.ts` using
      `aws-cdk-lib/assertions` `Template` to assert the SG rules; `npx jest` passes.

- [ ] 5. DataStack — RDS + RDS Proxy + per-process secrets.
      RDS PostgreSQL 16 in isolated subnets (encrypted, backups on, `multiAz`/
      `deletionProtection` from config), master secret, owner-login secret, and one
      Secrets Manager secret per process login (generated passwords). RDS Proxy in
      transaction mode in front, SG from step 4. Output the proxy endpoint and secret ARNs.
      Do NOT create Postgres roles (migrate/provision do).
      Files: `infra/lib/data-stack.ts`.
      Verify: `npx cdk synth` both envs; `infra/test/data-stack.test.ts` asserts engine
      version 16, proxy present, `multiAz` toggles with env, secret count = logins+owner+
      master; `npx jest` passes.

- [ ] 6. EcsClusterStack — cluster, Service Connect namespace, ECR repos, log groups.
      Fargate cluster per env, Cloud Map / Service Connect namespace, ECR repos `dad/api`,
      `dad/web`, `dad/nginx` with lifecycle policies, and a CloudWatch log group per
      service with env-configured retention.
      Files: `infra/lib/ecs-cluster-stack.ts`.
      Verify: `npx cdk synth` both envs; `infra/test/ecs-cluster-stack.test.ts` asserts 3
      ECR repos and log-group retention from config; `npx jest` passes.

- [ ] 7. ServicesStack — the 9 long-running services + task definitions.
      Define a reusable task-definition factory and create exactly the 9 services from the
      confirmed table above with their exact commands, ports, and env/secret wiring:
      `api` (8002, Service Connect name `api`), `web` (3000, env
      `DONOR_API_BASE_URL=http://api:8002`), `nginx` (8080/8443, health 8081), and the six
      workers with their `worker --kind <kind>` commands. DB URLs via the step-3 builder
      from the step-5 secrets; `DONOR_TRUSTED_PROXY_CIDRS` = web subnet CIDR. Wire
      Service Connect so `api` is reachable from `web` only. Desired counts from config;
      workers gated behind `DONOR_GO_WORKERS_ENABLED`.
      Files: `infra/lib/services-stack.ts`, `infra/lib/task-factory.ts`.
      Verify: `npx cdk synth` both envs; `infra/test/services-stack.test.ts` asserts 9
      services, the exact `worker --kind` commands, container ports 8002/3000/8443, and
      that `DONOR_API_BASE_URL` equals `http://api:8002`; `npx jest` passes.

- [ ] 8. MaintenanceStack — one-off task definitions + scheduled reconcile.
      Register run-task task definitions for `migrate`, `provision-roles` (CMD `provision`),
      `staff`, `rekey`, `monitor` with their exact env from the confirmed list. Add the
      `reconcile` task def and an EventBridge Scheduler rule (cron ~00:30
      America/New_York) invoking it as a Fargate run-task. Add a CloudWatch alarm on the
      reconcile task's non-zero exit → SNS topic.
      Files: `infra/lib/maintenance-stack.ts`, `infra/scripts/run-task.sh`.
      Verify: `npx cdk synth` both envs; `infra/test/maintenance-stack.test.ts` asserts 5
      maintenance task defs + the reconcile schedule expression + the exit-code alarm;
      `npx jest` passes. `run-task.sh` is documented, not executed.

- [ ] 9. EdgeStack — internet-facing NLB + nginx target wiring + Cloudflare toggle.
      NLB (TCP 443→8443, 80→8080) targeting the nginx service; nginx SG inbound restricted
      to Cloudflare ranges when `cloudflareIngressEnabled` (ranges as a documented list
      input, not hardcoded; reference `scripts/refresh-cloudflare-ips.sh`). Output the NLB
      DNS name for the user's out-of-band Cloudflare DNS records.
      Files: `infra/lib/edge-stack.ts`.
      Verify: `npx cdk synth` both envs; `infra/test/edge-stack.test.ts` asserts NLB
      listeners on 443 and 80 and the toggle changes the nginx SG ingress; `npx jest`
      passes.

- [ ] 10. ObservabilityStack + SesStack.
      Observability: SNS topic (optional email param), alarms for ECS service health
      (running < desired) and a monitor queue-age placeholder, wired to SNS. SES: domain
      identity for the per-env sending subdomain (DKIM on), an IAM user with an SES
      sending policy, SMTP credential secret mapped to `DONOR_SMTP_*`, and CfnOutputs for
      the DKIM/verification DNS records + a sandbox-mode reminder.
      Files: `infra/lib/observability-stack.ts`, `infra/lib/ses-stack.ts`.
      Verify: `npx cdk synth` both envs; tests assert the SNS topic + alarms and the SES
      identity/DKIM outputs; `npx jest` passes.

- [ ] 11. PipelineIamStack — GitHub OIDC provider + per-account deploy roles.
      OIDC provider for `token.actions.githubusercontent.com` and env-scoped deploy roles
      (infra-deploy, app-deploy) with least-privilege trust conditions on the repo/branch.
      Documented one-time per-account apply.
      Files: `infra/lib/pipeline-iam-stack.ts`.
      Verify: `npx cdk synth` both envs; `infra/test/pipeline-iam-stack.test.ts` asserts
      the OIDC provider and role trust policy subject condition; `npx jest` passes.

- [ ] 12. GitHub Actions — infra pipeline `.github/workflows/infra.yml`.
      PRs touching `infra/` run `cdk diff`; merge to `dev` → `cdk deploy -c env=staging`;
      release tag / merge to `main` → `cdk deploy -c env=production` gated by a GitHub
      Environment (`production`) protection rule. OIDC auth via the step-11 roles. Pin all
      actions by full SHA with `# vX.Y.Z` comments (match `ci.yml` style); NOTE any tag
      pin. Do not modify `ci.yml`.
      Files: `.github/workflows/infra.yml`.
      Verify: `npx cdk synth` unaffected; run a YAML lint / `node -e` parse to confirm
      valid YAML; confirm `ci.yml` is byte-unchanged.

- [ ] 13. GitHub Actions — app pipeline `.github/workflows/deploy.yml`.
      Build `dad/api`, `dad/web`, `dad/nginx`, push to ECR tagged with the git SHA, then
      direct ECS deploy in the exact `scripts/deploy.sh` order:
      **migrate (run-task) → provision-roles (run-task) → `aws ecs update-service` for
      api/web/nginx → smoke → workers (`update-service` for the six worker services,
      behind `DONOR_GO_WORKERS_ENABLED`)**. Smoke mirrors `deploy.sh smoke` (api
      `/health/ready`, nginx `/health`:8081, web `/`:3000). Staging auto on `dev`,
      production on release tag / `main` gated by manual approval. Read cluster/service
      names from CDK outputs/SSM, not hardcoded. Pin actions by SHA.
      Files: `.github/workflows/deploy.yml`.
      Verify: YAML parses; the job step order literally reads migrate → provision-roles →
      update-service → smoke → workers; `ci.yml` unchanged.

- [ ] 14. infra/README.md — operator documentation.
      Document: `-c env` usage, the one-time owner bootstrap SQL (`CREATE ROLE owner LOGIN
      CREATEROLE` as rds_superuser member), that provision-roles runs after migrate, the
      run-task wrapper, required GitHub Environments/vars/secrets, the SES sandbox-mode +
      DNS-records reminder, that the web image must be rebuilt when origins change, and the
      crown-jewel encryption-key handling. Note chosen production-typical defaults.
      Files: `infra/README.md`.
      Verify: `npm run build` and full `npx jest` still pass; `npx cdk synth -c env=staging`
      and `-c env=production` both succeed with dummy account/region.

## Final verification gate (offline only)

From `infra/`: `npm install`, `npm run build`, `npx jest`, `npx cdk synth -c env=staging`
and `npx cdk synth -c env=production` (dummy `CDK_DEFAULT_ACCOUNT`/`CDK_DEFAULT_REGION`).
No `cdk deploy`, no `aws` calls, no real credentials. Confirm `ci.yml` is unchanged and
both new workflow YAMLs parse.
