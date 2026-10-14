# Durable ATM ledger

The migration implementation lives in `apps/atm-ledger`. Deployment and cutover are
separate steps: until the deployed service is initialized and both branch workflows
are updated, `ATM_LEDGER_BACKEND` stays unset and ATM continues to use `atm-state`.
Never delete that branch merely because the Worker builds or its tests pass.

## Storage and integrity

One SQLite-backed Cloudflare Durable Object coordinates this repository's ledger.
Its `events` table is append-only: SQLite triggers reject updates and deletes.
Every event includes its version, previous event hash, before/after ledger digests,
authenticated actor and execution, request digest, and a finding/receipt delta.
The current ledger is a derived projection, compatible with the pinned ATM v1 format.

Each event is also written to a dedicated private R2 bucket under two indexes:
`events/` in ascending version order for recovery, and `history/` in descending order
for independent head checks. Both contain the same hash-bound event envelope. Enable
indefinite bucket locks before migration. A committed event remains pending until both
archive copies are verified. Failed archival blocks acknowledgement and subsequent
reads; retries reuse the event rather than appending another receipt.

Every snapshot verifies the ledger digest, the archive head, and the archived head
event. A restored database behind the archive fails closed. Recovery replays bounded
batches from the ascending archive, validates the chain and projections, then records
an administrative recovery event. It never discards newer archive history.

Account administrators can still deploy new code, remove bucket locks, or delete
resources. This is an append-only application protocol with independent retained
copies, not protection against a compromised Cloudflare account administrator.
Restrict account access and deployment credentials accordingly.

## Access

There are no shared publisher or triage passwords. The Worker verifies GitHub's signed
OIDC tokens, issuer, audience, expiry, repository ID, owner ID, workflow path, event,
hosted-runner claim, and job environment. The audience is
`atm-ledger:github:CoachCoe/ImpactGraph`.

| Job environment | Allowed operations |
|---|---|
| `atm-ledger-reader` | Read the current ledger for validation |
| `atm-ledger-publisher` | Read and publish the pinned verifier's next ledger; no triage |
| `atm-ledger-admin` | Owner-initiated migration, audited triage, verified history export and recovery |

Administrative access additionally requires `workflow_dispatch` on `dev` and an
actor ID in the Worker's `ADMIN_ACTOR_IDS` allowlist. Actors are derived from the signed
token, never from request parameters. Initially only CoachCoe's immutable ID is allowed.
Protect workflow changes and Cloudflare deployments through normal repository and
account review. Existing branch protection is not changed by this migration.

Publication requires the exact version and digest read before candidate execution.
The pinned ATM publisher still verifies candidate, execution, evidence hash, artifact
receipt, finding recurrence and historical triage before submitting a change. The
service independently prevents receipt replacement, finding deletion, publisher triage,
execution substitution, stale updates, and conflicting retries. A concurrent triage
change blocks publication; rerun the full ATM validation instead of rebasing a verdict.

## Install, test and deploy

Requires Node 22.19+ or Node 24, npm, a Cloudflare account with Workers/Durable Objects
and R2 enabled, and permission to create a dedicated Worker and bucket. Wrangler is
pinned in the service lockfile; `jose` supplies JWT verification. The Cloudflare test
plugin executes integration tests in the Workers runtime, including real SQLite/R2.

```sh
cd apps/atm-ledger
npm ci
npm run typecheck
npm test
npm run lint
npm audit --audit-level=high
npm run build
npx wrangler whoami
# If required, authenticate interactively: npx wrangler login
```

Confirm the intended account before creating resources. Do not reuse an application
bucket, expose this bucket publicly, or configure lifecycle deletion. If multiple
accounts are available, set `CLOUDFLARE_ACCOUNT_ID` to the intended account.

```sh
npx wrangler r2 bucket create impactgraph-atm-ledger-history
npx wrangler r2 bucket lock add impactgraph-atm-ledger-history immutable-ledger "" --retention-indefinite
npx wrangler r2 bucket lock list impactgraph-atm-ledger-history
npm run deploy
```

Keep the resulting HTTPS Worker origin; it is configuration, not a secret. Unauthenticated
requests must return 401. Configure the repository variable `ATM_LEDGER_URL` with that
origin, without a path. Create the three named GitHub environments above. Restrict the
admin environment to `dev`; protect environment administration and the actor allowlist.
An environment's protection rules may add human approval if the organization requires it.

## Cutover

1. Commit, test and push the service, adapter and compatibility workflow on `dev`.
   Verify CI and ATM still pass with the Git backend. Deploy the service and verify
   private storage and retention locks. The service intentionally refuses to create
   an empty operational ledger automatically.
2. Update `.github/workflows/atm.yml` and `scripts/atm-ledger.mjs` on **both `dev` and
   `main`** through authorized review. These are the consumer files needed on `main`;
   the running Worker is deployed from the reviewed service revision on `dev`.
   Do not silently promote unrelated application changes.
3. Set repository variable `ATM_LEDGER_BACKEND=paused`. Drain all previously queued or
   running Git-backed ATM executions. Pause branch pushes during the cutover window.
   Maintenance-mode validations do not pass acceptance. Administrative operations use
   the same GitHub concurrency group as publication.
4. Dispatch the verified import:

   ```sh
   gh workflow run atm.yml --repo CoachCoe/ImpactGraph --ref dev -f ledger_operation=migrate
   ```

   The admin job reads every committed `ledger.json` revision from the current
   `atm-state` tip, imports them in order, and verifies that the service's final digest
   equals the original ledger. Existing findings, triage audit arrays, receipts and
   source commit anchors are preserved. A different second import is refused.
5. Dispatch `ledger_operation=history`. Download its private export and verify its
   hash chain, imported revision count, original receipts, findings and classifications.
   Check that `atm-state` has not advanced beyond the imported tip. If it has, stop
   cutover and investigate; do not discard the extra history or initialize another ledger.
6. Set `ATM_LEDGER_BACKEND=durable`. Dispatch normal validation on `dev` and `main`:

   ```sh
   gh workflow run atm.yml --repo CoachCoe/ImpactGraph --ref dev -f ledger_operation=validate
   gh workflow run atm.yml --repo CoachCoe/ImpactGraph --ref main -f ledger_operation=validate
   ```

   Both must pass validation, retained evidence, durable publication and acceptance.
   Verify each execution appears once in the durable ledger with the intended code SHA.
7. Preserve the final Git ledger tip as an annotated archive tag, then detach any clean
   state worktree and remove the local/remote `atm-state` branch. Remove the compatibility
   checkout/push path and its `contents: write` permission from both consumer workflows.
   Normal validation must have no Git fallback after this point.

Cloudflare authentication, resource provisioning, main-branch authorization and passing
end-to-end cutover checks are prerequisites, not steps that local tests can establish.

## Audited triage

Review the retained encrypted evidence and write a private review note outside Git.
Then request owner-authenticated triage from the repository root:

```sh
node scripts/atm-ledger.mjs triage-request /private/path/review.txt FINDING_ID medium resolved
```

The client dispatches only the note's SHA-256 and classification; the note itself stays
private. Retain the note and relevant encrypted evidence. The service records the signed
GitHub actor and execution and appends an audit event. Passing tests do not silently
close a finding; a recurrence reopens it and retains its triage history.

Use `ledger_operation=history` for a verified paginated export. GitHub evidence artifacts
still expire after 30 days; the durable ledger/R2 event archive does not extend encrypted
evidence retention. Download needed evidence before expiry, as with the previous gate.

## Outages and recovery

Storage, issuer, integrity and concurrency errors fail acceptance. Investigate before
retrying; do not delete history, clear findings, change an eligible flag, or fall back
to the retired Git ledger. Identical HTTP publication retries are idempotent. Rerun all
ATM jobs after a failed validation/publication attempt.

If a point-in-time database restore leaves SQLite behind its archive, dispatch
`ledger_operation=recover`. Only the authorized admin workflow can do this. It replays
up to 10 missing archived events per request. Larger recoveries make durable progress
and return a retryable error until replay is complete; repeat the administrative job
if its bounded retries are exhausted. The final replay verifies the archive head and
records recovery without modifying original events or receipts.

Do not roll back Worker code to a revision that initializes a missing ledger or omits
the integrity checks. Preserve the Durable Object namespace and R2 bucket when rolling
back code. A contradictory archive or corrupted history requires operator investigation;
the recovery endpoint does not guess which conflicting record is correct.

Requests are bounded at 16 MiB, ledger projections at 1 MiB and individual events at
512 KiB (below SQLite row limits). Migration is bounded at
1,000 legacy revisions, and history exports at 100 events per page. The administrative
CLI bounds an interactive export at 100,000 events. Exceeding a bound blocks the operation
and requires an explicitly reviewed capacity change; it never returns a partial green verdict.


## Cost controls

The service limits authenticated operations to 1,000 per UTC day, new events to 200
per UTC day, and both retained R2 copies combined to 100 MiB. SQLite counters reserve
archive bytes in the event transaction; failed transactions and identical publication
retries do not reserve twice. Recovery accounts for restored events. Daily resets never
reset retained bytes. Exceeding a quota blocks acceptance, preserves history, and requires
operator review or a reviewed capacity change. Each archival/recovery pass handles ten
events so initial import can drain through bounded, idempotent retries.

An ingress limiter rejects excess traffic before JWT verification or storage work:
60 requests per minute per Cloudflare location using a service-wide key. This is an
approximate local abuse limit, not a global request or dollar cap. Invalid requests
still invoke the Worker. Start with Workers Free's provider-enforced limits; do not
upgrade the account to Paid as part of this migration. If Free cannot support the
verified workload, review the failure and pricing before any upgrade. On Free, the
platform's CPU limit applies; configuring a higher paid CPU limit is not required.

Configure account budget alerts at $1, $5 and $10 in Billing > Billable Usage. Alerts
are informational, may be delayed and exclude recurring plan fees; they are not a
spending cap. Quotas apply only to this application's activity, and retained objects
continue consuming storage after writes stop. Other applications share account free
allowances. R2 remains private and uses Standard storage. No automatic expiry deletes
the append-only history. Recheck account usage after import and the first billing cycle.
