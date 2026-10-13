# Implementation Plan — Daily Stripe Reconciliation Job (`cmd/reconcile`)

Source of truth: `apps/donor-api/.agents/tasks/stripe-reconciliation-scope.md` plus the six
user-confirmed decisions in the task brief (window = yesterday full calendar day in
America/New_York, run ~00:30 NY; 30m grace; charges + refunds only for R1; no report
retention; non-zero exit on ANY discrepancy > 0; manual remediation only; strictly
read-only against Stripe and DB).

All paths are absolute. ALL work happens inside the worktree
`/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile`. A step's `cwd` is
NOT the worktree, so every command below `cd`s into the Go app first. The Go module lives at
`/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api`
(import path `impactgraph/donor-api`).

## Design decisions recorded during exploration (grounded in code read)

- **Role var name — do NOT call it `Reconcile`.** The scoping doc suggested a `Role` var
  named `Reconcile`, but `internal/dbroles/privileges.go` already declares a package-level
  function `func Reconcile(ctx, tx)` (the privilege reconciler). A `var Reconcile` in the
  same package would be a duplicate-symbol compile error. Name the role var `ReconcileJob`
  (`ReconcileJob = Role{"dad_reconcile", 2}`). The database role NAME stays `dad_reconcile`;
  only the Go identifier differs. Rationale: least surprise, no rename of existing symbols.
- **Env file location correction.** The task brief names
  `apps/donor-api/.env.database.example` / `.env.production.example`. Those files do NOT
  exist under `apps/donor-api`. The real role-URL file is the REPO-ROOT
  `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/.env.database.example`
  (every other `DONOR_*_DATABASE_URL` lives there; `scripts/deploy.sh` loads it as
  `DATABASE_ENV_FILE`). Role URLs must NOT go in `.env.production.example` — that file is
  shipped to every container and `scripts/deploy.sh check_env_files` REFUSES any
  `DONOR_*DATABASE_URL` in it. So the new `DONOR_RECONCILE_DATABASE_URL` goes ONLY in
  `.env.database.example`. `STRIPE_SECRET_KEY` already lives in `.env.production.example`
  and reaches every `*donor` service via `env_file`, so the reconcile service inherits it
  with no change there.
- **Role URL variable name.** `Role.Variable()` derives the env var from the role name:
  `dad_reconcile` → `DONOR_RECONCILE_DATABASE_URL`. Confirmed by
  `internal/dbroles/roles.go` `Variable()` and `TestEachProcessReadsItsOwnURL`.
- **Refund DB keys.** `donor_refunds` (migration `0001_r1.sql`) keys on
  `stripe_refund_id` (UNIQUE) and timestamps on `observed_at`; it has NO `payment_intent`
  column and links to its gift via `gift_id`. So the DB-side refund query keys the window on
  `observed_at`, and the diff matches Stripe refund id ↔ `stripe_refund_id`. Gifts key the
  window on `paid_at` and match Stripe charge id ↔ `stripe_object_id` (fallback
  `stripe_payment_intent_id`).
- **Pure-Go diff package pattern.** `internal/recognition` is HTTP/Stripe-free and imports
  `_ "time/tzdata"` so `America/New_York` resolves inside the stripped container. The new
  `internal/reconcile` package follows the same shape and also imports `_ "time/tzdata"`
  (the NY-day window math lives here or in `cmd/reconcile`; put the window-bounds helper in
  `internal/reconcile` so it is unit-testable without a DB or Stripe).
- **Livemode discipline.** The gateway accepts only `sk_test_` keys and the webhook requires
  `livemode:false`. The reconcile list methods must treat any Stripe object with
  `livemode:true` as an error/ignore, using `Object.BoolIs("livemode", false)` from
  `internal/payments/facts.go`.
- **Stripe SDK (v87) list APIs confirmed.** `ChargeListParams` and `RefundListParams` both
  carry `CreatedRange *RangeQueryParams` (fields `GreaterThanOrEqual`, `LesserThan`, etc.)
  and embed `ListParams` (`Limit`, `StartingAfter`). The client exposes `V1Charges.List` and
  `V1Refunds.List` returning an iterator with `.Err()` / `.LastResponse()`, exactly as the
  existing `ListPaymentRefunds` uses `V1Refunds.List`.
- **This job does NOT decompose into separate features.** It is one tightly
  dependency-ordered unit: the role must exist before its migration/grants verify; the
  gateway, diff package, and queries must exist before the CLI can call them; deployment
  wiring comes last. The workflow's existing implement-and-review loop implements this plan
  as a single stream.

## Verification prerequisite (one-time, for any step whose Verify runs integration tests)

The `dbroles` integration tests and `go test -race ./...` need a synthetic `donor_go`
database (README "Checks"). If `TEST_DATABASE_URL` is not already exported and migrated,
bring it up once:

```sh
docker run -d --name impactgraph-go-test-postgres -p 127.0.0.1:55440:5432 \
  -e POSTGRES_USER=go_test -e POSTGRES_PASSWORD=go_test -e POSTGRES_DB=donor_go \
  postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea
export TEST_DATABASE_URL=postgresql://go_test:go_test@localhost:55440/donor_go
(cd /Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api \
  && DONOR_GO_DATABASE_URL="$TEST_DATABASE_URL" go run ./cmd/migrate)
```

Re-run the `cmd/migrate` line after step 2 (new migration) so the test DB has the
`dad_reconcile` role before the `dbroles` tests run.

---

# Implementation Plan

- [ ] 1. Register the `dad_reconcile` role in `internal/dbroles/roles.go`.
      Add `ReconcileJob = Role{"dad_reconcile", 2}` to the `var (...)` block (pool 2, like
      Monitor/StaffCLI/Rekey) and append `ReconcileJob` to the `Roles` slice. Do NOT add it
      to the `Workers` map (it is not a worker). Do NOT name the var `Reconcile` (collides
      with the existing `Reconcile` function in `privileges.go`).
      Files: `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/internal/dbroles/roles.go`
      Verify: `cd /Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api && go build ./...` — compiles with no duplicate-symbol error.

- [ ] 2. Declare `dad_reconcile`'s SELECT-only grants and add the migration.
      In `internal/dbroles/privileges.go` add `ReconcileJob: "SELECT"` to the `grants` map
      entry for each of these eight relations: `donor_gifts`, `donor_refunds`,
      `donor_billing_periods`, `donor_billing_observations`, `donor_refund_observations`,
      `donor_stripe_events`, `donor_checkouts`, `donor_schema_migrations`. Add NOTHING else
      (SELECT-only means no `lock`, so the role cannot even take row locks — correct). Create
      a new, essentially empty migration `db/migrations/0014_reconcile_role.sql` (next number
      after 0013; migrations are immutable + checksum-checked — never edit an existing one).
      The role and grants are applied by `migrate`'s privilege step (`dbroles.Reconcile` →
      `ensureRole` + `Drift`), so the SQL body needs no DDL; include only a header comment
      explaining that the role/grants are declared in Go and applied by the privilege
      reconciler (follow the comment style of `0013_projection_rules_version.sql`). A role
      with no table DDL still gets created because it is in `Roles`.
      Files:
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/internal/dbroles/privileges.go`,
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/db/migrations/0014_reconcile_role.sql`
      Verify: re-run the test-DB migrate line from the prerequisite, then
      `cd /Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api && go test ./internal/dbroles/...` — `TestCatalogMatchesTheDeclaration`, `TestNewRelationsNeedDeclaredPrivileges`, `TestRolesHoldOnlyTheirPrivileges`, and `TestMigrationsRestoreTheDeclaration` pass (zero drift, no undeclared relation).

- [ ] 3. Add the role URL to the database env example and wire its Compose services.
      In the REPO-ROOT `.env.database.example` add a `DONOR_RECONCILE_DATABASE_URL=postgresql://dad_reconcile_login:CHANGE_ME@postgres:5432/impactgraph`
      line alongside `DONOR_MONITOR_DATABASE_URL` (same comment conventions). In
      `docker-compose.prod.yml`: (a) add a new `reconcile` service modeled on the `monitor`
      service — `<<: *donor`, `command: [reconcile]`, `profiles: [maintenance]`,
      `environment: { DONOR_RECONCILE_DATABASE_URL: ${DONOR_RECONCILE_DATABASE_URL:?set DONOR_RECONCILE_DATABASE_URL} }`
      (STRIPE_SECRET_KEY is inherited via `env_file`, as it is for every `*donor` service);
      and (b) add `DONOR_RECONCILE_DATABASE_URL: ${DONOR_RECONCILE_DATABASE_URL:?set DONOR_RECONCILE_DATABASE_URL}`
      to the `provision` service's `environment` block so `provision-roles` creates the
      login. Do NOT put the URL in `.env.production.example` (deploy.sh refuses DB creds
      there). Keep `donor_billing_*` SELECT grants from step 2 — the phase-2 seam.
      Files:
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/.env.database.example`,
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/docker-compose.prod.yml`
      Verify: `cd /Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile && docker compose -f docker-compose.prod.yml --env-file .env.production.example --env-file .env.database.example --profile maintenance config >/dev/null` — Compose parses the file and resolves the new `reconcile` and updated `provision` services with no error. (If `docker compose` is unavailable in the sandbox, fall back to `python3 -c "import yaml,sys;yaml.safe_load(open('docker-compose.prod.yml'))"` to confirm valid YAML.)

- [ ] 4. Add the two read-only Stripe list methods to the gateway.
      In `internal/payments/gateway.go` add `ListChargesByCreated(ctx, start, end time.Time) ([]Object, error)`
      and `ListRefundsByCreated(ctx, start, end time.Time) ([]Object, error)`, each modeled
      EXACTLY on `ListPaymentRefunds`: bounded `for range N` page loop, `Limit: 100`,
      `StartingAfter` cursor, `CreatedRange: &stripe.RangeQueryParams{GreaterThanOrEqual: start.Unix(), LesserThanOrEqual: end.Unix()}`,
      `has_more` termination, the non-advancing-cursor guard, and the `len(rows) > 100` /
      empty-page `ErrFacts` guards. Use `g.client.V1Charges.List` and `g.client.V1Refunds.List`.
      For each returned Object, reject the whole call with `ErrFacts` (or skip the object) if
      `!obj.BoolIs("livemode", false)` — never reconcile a livemode object. Use ONLY
      List/Retrieve; never a mutating method. Expose both methods behind a new small
      read-only interface in the same file, e.g.
      `type ReconcileGateway interface { ListChargesByCreated(context.Context, time.Time, time.Time) ([]Object, error); ListRefundsByCreated(context.Context, time.Time, time.Time) ([]Object, error) }`,
      alongside the existing `FinancialGateway`/`StaffFinanceGateway` interfaces, so tests and
      `cmd/reconcile` can inject a fake. Add `"time"` to imports if not present.
      Files:
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/internal/payments/gateway.go`,
      new `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/internal/payments/reconcile_gateway_test.go`
      Verify: `cd /Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api && go test ./internal/payments/...` — new tests pass. Test with an injected `httptest.NewServer` + `stripe.NewBackendsWithConfig{URL: server.URL, HTTPClient: server.Client()}` exactly as `checkout_test.go` does: assert the request path (`/v1/charges`, `/v1/refunds`), the `created[gte]`/`created[lte]` query params and `limit=100`, serve a two-page `has_more` response to exercise pagination, and assert a `livemode:true` object is rejected.

- [ ] 5. Add the pure-Go diff/report package `internal/reconcile`.
      New package `internal/reconcile` (HTTP- and Stripe-free, like `internal/recognition`;
      import `_ "time/tzdata"`). It exposes: (a) a `Window` helper that, given "now" and the
      `-date`/`-since`/`-until` + `-max-lag` inputs, computes the UTC `[start, end)` bounds of
      "yesterday as a full calendar day in America/New_York" and applies the 30m grace margin
      so an object whose Stripe `created` is within the last 30m of the window edge is NOT
      flagged; (b) a `Report` struct with integer count fields `missing_gifts`,
      `missing_refunds`, `amount_mismatches`, `status_mismatches`, `db_without_stripe`, the
      window bounds, and bounded `[]string` id lists per class (IDS ONLY — never donor PII or
      payloads, per `docs/architecture.md` Financial consistency); (c) a `Diff(stripeCharges, stripeRefunds []payments.Object, dbGifts, dbRefunds <row types>) Report`
      that classifies each discrepancy class from §2 of the scope doc. Match Stripe charge id
      ↔ gift `stripe_object_id` (fallback `stripe_payment_intent_id`), Stripe refund id ↔
      `stripe_refund_id`; compare `charge.amount`→`amount_minor` and refund
      `amount`/`status` (map Stripe lowercase status to the uppercase DB enum). An
      unattributed-but-present gift is NOT a discrepancy (attribution is a projection
      concern). Leave an explicit commented seam for phase-2 invoice/billing-period/
      subscription reconciliation but build none of it. Add `Report.HasDiscrepancies() bool`
      (any count > 0) for the CLI's exit decision.
      Files:
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/internal/reconcile/reconcile.go`,
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/internal/reconcile/reconcile_test.go`
      Verify: `cd /Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api && go test ./internal/reconcile/...` — table-driven unit tests pass, one case per discrepancy class plus a clean-day case (empty report, `HasDiscrepancies()==false`), a window case asserting the 30m grace suppresses a boundary object, and a case asserting an unattributed present gift is NOT flagged.

- [ ] 6. Add the sqlc SELECT queries and regenerate.
      Create `db/reconcile.sql` with read-only `:many` queries keyed on the window: e.g.
      `GiftsPaidBetween` — `SELECT stripe_object_id, stripe_payment_intent_id, amount_minor, currency, paid_at FROM donor_gifts WHERE paid_at >= sqlc.arg(start)::timestamptz AND paid_at < sqlc.arg(end_at)::timestamptz`;
      and `RefundsObservedBetween` — selecting `stripe_refund_id, amount_minor, status, observed_at`
      from `donor_refunds` filtered on `observed_at` within the window (follow the SELECT and
      `sqlc.arg` style in `db/financial.sql`/`db/monitor.sql`). Register `"db/reconcile.sql"`
      in the `queries:` list in `sqlc.yaml`. Regenerate with `make generate` (pinned sqlc in
      Docker) — never hand-edit `internal/store/queries/*`.
      Files:
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/db/reconcile.sql`,
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/sqlc.yaml`,
      generated `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/internal/store/queries/*`
      Verify: `cd /Users/shawncoe/Documents/dev/ImpactGraph && make generate && make check-generated` — generated code regenerates and `check-generated` reports no drift (fails if generated code was hand-edited or stale).

- [ ] 7. Add the `cmd/reconcile` CLI.
      New `cmd/reconcile/main.go` copying the `cmd/monitor/main.go` skeleton:
      `flag.NewFlagSet("reconcile", flag.ContinueOnError)` with flags `-date` (and/or
      `-since`/`-until`) defaulting to "yesterday in America/New_York" and `-max-lag`
      (`flags.Duration`, default `30*time.Minute`); `flags.NArg()!=0` guard;
      `dsn := dbroles.ReconcileJob.URL(getenv)` with empty-check;
      `store.Open(ctx, dsn, dbroles.ReconcileJob.Pool)`; `migrations.Verify`;
      `dbroles.ReconcileJob.Check(ctx, pool, getenv(dbroles.AllowOwnerVariable)=="true")`;
      build the gateway with `payments.NewStripeGateway(getenv("STRIPE_SECRET_KEY"))`; compute
      the window via `internal/reconcile`'s `Window`; call `ListChargesByCreated` /
      `ListRefundsByCreated` and the two sqlc queries via `queries.New(pool)`; run
      `reconcile.Diff`; `json.NewEncoder(out).Encode(report)`; and `return` a non-nil error
      (→ `os.Exit(1)` in `main`) when `report.HasDiscrepancies()` (ANY class count > 0). A
      clean day prints JSON and exits 0. The job writes nothing to Stripe or the DB.
      Files:
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/cmd/reconcile/main.go`,
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api/cmd/reconcile/main_test.go`
      Verify: `cd /Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/apps/donor-api && go build ./... && go test ./cmd/reconcile/...` — tests pass. Drive the Stripe side with an injected `httptest.NewServer` + `stripe.NewBackendsWithConfig` (as `checkout_test.go`); drive the DB side against the synthetic `donor_go` test DB using `dbrolestest.Open(t, dbroles.ReconcileJob)`; assert a clean day returns nil (exit 0) and prints the JSON report, and a seeded missing-webhook case (Stripe charge with no gift row) returns a non-nil error (exit 1) with `missing_gifts == 1`.

- [ ] 8. Wire deployment entry point and documentation.
      Add a `reconcile` case to `scripts/deploy.sh`'s command dispatch alongside
      `monitor`/`rekey`/`staff` — `reconcile) shift; compose run --rm --no-deps reconcile reconcile "$@" ;;`
      — and add `reconcile` (with its `[--date ...]`/`[--max-lag ...]` options) to the
      `usage()` string. Optionally add a `Makefile` target mirroring `make provision`/`monitor`
      style (e.g. `reconcile: cd apps/donor-api && go run ./cmd/reconcile`). Document the job
      in `docs/operations.md`: a new role row for `dad_reconcile` in the database-roles table
      ("Reads financial facts to reconcile Stripe charges and refunds; no writes"), and a
      short section covering the schedule (~00:30 America/New_York on yesterday's closed
      day), the 30m grace margin, read-only guarantee, non-zero-exit alarm policy, charges+
      refunds-only R1 scope with the invoice/subscription phase-2 seam, and the MANUAL
      remediation path (re-send the event in Stripe, then
      `worker --kind stripe --retry-event <evt>`; never delete financial facts). IDs-only in
      the report — no donor PII.
      Files:
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/scripts/deploy.sh`,
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/Makefile`,
      `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/docs/operations.md`
      Verify: `cd /Users/shawncoe/Documents/dev/ImpactGraph && make lint` — shellcheck passes
      on `scripts/deploy.sh` (and staticcheck/govulncheck on the Go changes). If shellcheck is
      not installed, fall back to `bash -n /Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/scripts/deploy.sh`.

- [ ] 9. Full-suite green check (integration of all steps).
      No new code — run the project's complete gate to confirm every piece builds, formats,
      vets, and passes race tests together, and that generated code is in sync.
      Files: none.
      Verify: with `TEST_DATABASE_URL` set and migrated (prerequisite above, re-migrated after
      step 2), run `cd /Users/shawncoe/Documents/dev/ImpactGraph && make test-donor-go && make check-generated` — formatting, `go vet`, `go test -race ./...`, `go build ./...` all pass and generated code shows no drift.

## Notes / known gaps and assumptions

- The invoice/billing-period/subscription reconciliation is explicitly OUT of scope for R1
  (user decision 3). Steps 2 and 5 keep a seam (SELECT grants on `donor_billing_*` already
  declared; a commented phase-2 hook in `internal/reconcile`) but build none of it.
- No report retention / state (decision 4): the job is stateless — stdout JSON + exit code
  only. No S3, no table, no migration beyond the role.
- If `go test -race ./...` or the `dbroles` tests cannot run because no synthetic Postgres is
  available in the sandbox, that is an environment limitation, not a plan defect — bring up
  the test DB per the prerequisite; the per-step Verify commands are the real gate.
- Convergence/stop contract for the implement-and-review loop is unchanged: the reviewer
  writes `/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile/.agents/tasks/reconcile-review.json`
  with `verdict: APPROVED` when the whole diff and the per-step verifications pass.

---

## Verification evidence (first iteration)

All commands were run from
`/Users/shawncoe/Documents/dev/ImpactGraph/.worktrees/stripe-reconcile` or its
`apps/donor-api`. A synthetic Postgres WAS reachable
(`impactgraph-go-test-postgres`, host port 55440, database `donor_go`), so the
`dbroles` integration tests, the DB-backed `cmd/reconcile` tests, and the full
`make test-donor-go` race suite all ran for real.

Test DB prepared once (plan prerequisite, re-migrated after the 0014 migration):

```
TEST_DATABASE_URL=postgresql://go_test:go_test@localhost:55440/donor_go
DONOR_GO_DATABASE_URL=$TEST_DATABASE_URL go run ./cmd/migrate   # applied 0014, created dad_reconcile + its 8 SELECT grants
```

Verified in the catalog after migrate: role `dad_reconcile` exists; its only grants are
SELECT on donor_billing_observations, donor_billing_periods, donor_checkouts,
donor_gifts, donor_refund_observations, donor_refunds, donor_schema_migrations,
donor_stripe_events (8 rows, SELECT only — no write grant).

- `go build ./...` → PASS (`BUILD_OK`).
- `gofmt -l` over the new/changed Go dirs → clean (no files listed).
- `go vet ./...` → PASS (`VET_OK`).
- `make generate` → the pinned-sqlc Docker step regenerated
  `internal/store/queries/reconcile.sql.go`; a re-run left it byte-identical, so the Go
  generated code shows NO drift (the only reported path is the new, untracked
  `reconcile.sql.go`). NOTE: the Makefile's `generate`/`check-generated` targets also run
  the web `cd apps/web && npm run generate:api` step, which fails in this environment with
  `openapi-typescript: command not found` — a missing web toolchain unrelated to this
  change. The sqlc (Go) half of generation and its drift check both pass; the web half was
  not runnable here.
- `go test ./internal/payments/... ./internal/reconcile/... ./cmd/reconcile/...` → PASS.
  - `internal/payments`: `TestReconcileListPagesByCreatedWindow` (charges + refunds, asserts
    `/v1/charges`,`/v1/refunds`, `created[gte]`/`created[lte]`, `limit=100`, two-page
    `has_more` pagination), `TestReconcileListRejectsLivemodeObject`. Stripe is driven by an
    injected `httptest.NewServer` + `stripe.NewBackendsWithConfig{URL:...}` exactly as
    `checkout_test.go` — NO live Stripe.
  - `internal/reconcile`: table-driven cases for every discrepancy class, a clean-day case
    (`HasDiscrepancies()==false`), the 30m-grace boundary-suppression case, the
    unattributed-present-gift-not-a-discrepancy case, window math, and an IDs-only
    report-hygiene test.
  - `cmd/reconcile`: `TestWindowResolution` (default yesterday-NY, `-date`, `-since/-until`),
    and `TestReconcileWindowAgainstSyntheticDB` against the real `donor_go` DB as the
    `dad_reconcile` role via `dbrolestest.Open` — clean day returns nil (exit 0), seeded
    missing-webhook (Stripe charge, no gift row) returns `missing_gifts==1` and
    `HasDiscrepancies()`.
- `go test ./internal/dbroles/...` (with the test DB) → PASS:
  `TestCatalogMatchesTheDeclaration`, `TestNewRelationsNeedDeclaredPrivileges`,
  `TestRolesHoldOnlyTheirPrivileges`, `TestMigrationsRestoreTheDeclaration` — zero drift,
  no undeclared relation.
- `make test-donor-go` (full gate: `gofmt -l` empty, `go vet ./...`, `go test -race ./...`,
  `go build ./...`, with `TEST_DATABASE_URL` set) → PASS, every package `ok`, EXIT=0.
- `shellcheck scripts/deploy.sh` → PASS (`SHELLCHECK_OK`).
- `docker compose -f docker-compose.prod.yml --env-file .env.production.example
  --env-file .env.database.example --profile maintenance config` with the normally-required
  deploy vars supplied (IMAGE_TAG/ENV_FILE/POSTGRES_PASSWORD/DONOR_*_ORIGIN) → EXIT=0,
  renders the new `reconcile` service (`command: [reconcile]`, `profiles: [maintenance]`,
  `DONOR_RECONCILE_DATABASE_URL`) and the `provision` service now also carrying
  `DONOR_RECONCILE_DATABASE_URL`.

Not run / not applicable here:
- A live `go run ./cmd/reconcile -date <yesterday>` end-to-end was intentionally NOT run,
  because it would call real Stripe (no `sk_test_` stub server in a bare `go run`). The
  equivalent clean-day exit-0 and missing-webhook exit-1 behaviors are proven by
  `TestReconcileWindowAgainstSyntheticDB` against the synthetic DB with an injected gateway.
- The web `generate:api` step (see above) is blocked by a missing `openapi-typescript`
  binary in this environment; it is unrelated to the reconcile change.
