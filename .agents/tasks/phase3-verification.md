# Phase 3 — ATM ledger rebrand (code/config) verification evidence

Branch: `rename/phase3-atm-code`
Scope: source/config/test/doc edits ONLY. No live wrangler/gh/deploy operations were run.

## Rename applied (from -> to)

| Item | From | To |
|---|---|---|
| Worker name | `impactgraph-atm-ledger` | `dad-atm-ledger` |
| R2 bucket | `impactgraph-atm-ledger-history` | `dad-atm-ledger-history` |
| OIDC audience | `atm-ledger:github:CoachCoe/ImpactGraph` | `atm-ledger:github:CoachCoe/DAD-Main` |
| SOURCE var | `github:CoachCoe/ImpactGraph` | `github:CoachCoe/DAD-Main` |

Immutable values left unchanged: `REPOSITORY_ID` = `1378692308`, `OWNER_ID` = `17258192`.

## Files changed

- `apps/atm-ledger/wrangler.jsonc` — `name`, r2 `bucket_name`, `vars.SOURCE`.
- `apps/atm-ledger/package.json` — private package `name` (human-readable identity within the ATM surface; `"private": true`, no external/behavioral effect).
- `apps/atm-ledger/package-lock.json` — mirrored package `name` fields (top-level and root package entry) to match package.json; `npm ci` confirmed the lockfile is otherwise intact.
- `apps/atm-ledger/test/auth.test.ts` — repository, workflow_ref (dev/other/untrusted), negative attacker repo, and audience string.
- `apps/atm-ledger/test/store.test.ts` — `source` constant.
- `scripts/atm-ledger.mjs` — `SOURCE` constant and the `--repo` arg of the `gh workflow run` dispatch.
- `docs/atm-ledger.md` — active ledger URL + bucket, OIDC audience, `wrangler r2 bucket create/lock` commands, and a new frozen-archive note.

## Not changed (deliberate)

- `apps/atm-ledger/src/auth.ts` and `src/index.ts` — the OIDC audience is derived at
  runtime as `atm-ledger:${env.SOURCE}` and the authorized repo as `env.SOURCE` with the
  `github:` prefix stripped. There is no hardcoded audience/repo constant in src; the
  `wrangler.jsonc` SOURCE change flows through to both audience and authorization.
- `.github/workflows/atm.yml` — contains NO reference to the renamed product repo
  identity. SOURCE is computed as `github:${{ github.repository }}`. The three
  `repository: CoachCoe/atm` checkouts are the SEPARATE external ATM verifier/tooling
  repo and were left unchanged. The path `atm/integrations/github/impactgraph-evidence.pub.pem`
  (line 102) is a filename INSIDE that external `CoachCoe/atm` checkout, not the product
  repo identity, so it was left unchanged.
- `REPOSITORY_ID`, `OWNER_ID` — immutable, unchanged.
- `docs/requirements/product-rename-plan.md`, `docs/requirements/product-rename-progress.md` — out of scope, untouched.

## Commands run (in apps/atm-ledger) and results

- `npm ci` — PASS. "added 234 packages, and audited 235 packages ... found 0 vulnerabilities". (node_modules was absent; dependencies installed from the pinned lockfile to enable verification.)
- `npm run typecheck` (`wrangler types && tsc --noEmit`) — PASS. Runtime types generated; `tsc --noEmit` reported no errors (exit 0).
- `npm test` (`vitest run`) — PASS. Test Files 3 passed (3); Tests 35 passed (35).
- `npm run lint` (`biome lint --error-on-warnings src test`) — PASS. "Checked 7 files ... No fixes applied." (exit 0).
- `npm run build` (`wrangler deploy --dry-run --outdir dist`) — PASS. Dry run only, no deploy. Bindings confirm the new identity:
  - `env.ARCHIVE (dad-atm-ledger-history)` R2 Bucket
  - `env.SOURCE ("github:CoachCoe/DAD-Main")` Environment Variable
  - `env.REPOSITORY_ID ("1378692308")`, `env.OWNER_ID ("17258192")` unchanged.

## Completeness sweep

From the worktree root, grep for remaining `impactgraph`/`ImpactGraph` under the ATM
surface (excluding node_modules/dist/generated types):

- `scripts/atm-ledger.mjs` — none remaining.
- `apps/atm-ledger` (ts/jsonc/json, excluding package-lock noise) — none remaining
  (package.json and package-lock.json name fields were updated to `dad-atm-ledger`).
- `docs/atm-ledger.md` — remaining occurrences are INTENTIONAL historical references to
  the frozen old ledger:
  - Lines ~12-14: the new frozen-archive note describing the retired `impactgraph-atm-ledger`
    Worker and `impactgraph-atm-ledger-history` bucket (22 revisions) and the preserving
    tag `archive/atm-state-20261013`.
  - Lines ~112-113: the historical migration Actions-run URLs on
    `github.com/CoachCoe/ImpactGraph` — these record events that actually occurred under
    the old repo identity and must stay accurate as historical records.

No unintended `impactgraph`/`ImpactGraph` occurrences remain on the ATM ledger surface.
