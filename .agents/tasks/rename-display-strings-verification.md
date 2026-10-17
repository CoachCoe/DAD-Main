# Verification — product-facing string rename `ImpactGraph`/`impactgraph` -> `DAD`/`dad`

Branch: `rename/display-strings` (worktree
`/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings`). First
iteration — no `rename-display-strings-review.json` existed.

## Changes made (four files)

1. `README.md` line 1: `# ImpactGraph — DAD donor release` -> `# DAD — donor release`.
   This is the ONLY product-name occurrence in README prose; no other `ImpactGraph`/`impactgraph`
   and no historical URLs exist in README.md.
2. `apps/web/package.json`: `"name": "impactgraph-web"` -> `"name": "dad-web"`. `version` and all
   dependency entries untouched.
3. `apps/web/package-lock.json`: resynced via `npm install --package-lock-only` in `apps/web`
   (npm 11.5.2 / node v24.15.0). Diff touches ONLY the two name fields (top-level `name` and the
   `""` package-entry `name`) -> `dad-web`. No `version`, `lockfileVersion`, or dependency change.
4. `apps/donor-api/api/openapi.json`:
   - `info.title`: `"ImpactGraph donor API"` -> `"DAD donor API"`.
   - `info.description` leading phrase: `The ImpactGraph (Dynamic Aid Delivery) donor API` ->
     `The DAD (Dynamic Aid Delivery) donor API`. Every other byte of the description is identical
     (the `X-DAD-Client` header name and all other text unchanged).

## openapi.json: generated or hand-maintained?

HAND-MAINTAINED (source of truth / input to generation), confirmed against the repo:
- Root `Makefile`: `GENERATED := apps/donor-api/internal/store/queries apps/web/lib/openapi.d.ts`.
  `make generate` runs sqlc (produces Go queries) and `cd apps/web && npm run generate:api`
  (produces `apps/web/lib/openapi.d.ts` FROM `apps/donor-api/api/openapi.json`). So openapi.json
  is an input, not an output. There is no Go source literal for the title/description.
- Action taken: edited `apps/donor-api/api/openapi.json` directly. Did NOT hand-edit any generated
  file and did NOT look for a non-existent Go source.

## Verification commands and results

### Web typecheck + build (in `apps/web`)
- `npm ci` -> added 293 packages, 0 vulnerabilities, exit 0. (Needed because node_modules binaries
  were absent; `tsc` was not on PATH before install. Confirmed the lock diff was still only the two
  name fields after `npm ci`.)
- `npm run typecheck` (`tsc --noEmit`): FIRST run exit 2 — 12 errors, all `Cannot find name
  'PageProps'`/`'LayoutProps'` in app/ route files. These are Next.js 16 build-generated global
  route types that live in `.next/types` and only exist after a build; this is an ordering artifact,
  not caused by the package-name rename.
- `npm run build` (`next build`): exit 0 — compiled successfully, TypeScript checked clean during
  build ("Finished TypeScript"), 37 routes generated.
- `npm run typecheck` RE-RUN after the build generated route types: exit 0 (clean). Confirms the
  rename does not break typecheck or build; the standalone-typecheck failure was purely dependent on
  build-generated types.

### openapi validity + generated-drift check
- `python3 -m json.tool apps/donor-api/api/openapi.json > /dev/null`: exit 0 (valid JSON).
- `make generate` (from worktree root; Docker available, ran sqlc image + `generate:api`): exit 0.
- `git status --porcelain -- apps/donor-api/internal/store/queries apps/web/lib/openapi.d.ts` after
  generate: showed `M apps/web/lib/openapi.d.ts`. Inspected the diff — it adds a `commit` field
  (`The running build's VCS revision, or "unknown".`) to the liveness/readiness response schemas.
  This drift is PRE-EXISTING and UNRELATED to this rename: the committed `openapi.json` at HEAD
  already contains those `commit` fields (2 occurrences of the VCS-revision description), but the
  committed `openapi.d.ts` had not been regenerated for them. My title/description edit produced
  ZERO change in generated types (title/description are not emitted into `openapi.d.ts`, exactly as
  the plan predicted).
- Action: reverted the unrelated generated-file change
  (`git checkout -- apps/web/lib/openapi.d.ts apps/donor-api/internal/store/queries`) so this commit
  contains ONLY the four intended product-name edits. The pre-existing openapi.d.ts drift is out of
  scope for a product-name rename and is left for its own change.

### Sweep
- Target files (`git grep -niI 'impactgraph' -- README.md apps/web/package.json
  apps/web/package-lock.json apps/donor-api/api/openapi.json`): EMPTY (exit 1, no matches). 
- Whole-worktree `git grep -niI 'impactgraph'`: all remaining hits are intentional DO-NOT-TOUCH /
  out-of-scope locations only:
  - `.agents/tasks/*` prior-phase plan/verification docs (out of scope).
  - `.github/workflows/atm.yml:102` — CoachCoe/atm key-file reference
    `atm/integrations/github/impactgraph-evidence.pub.pem` (preserved).
  - `apps/web/e2e/golden-path.spec.ts:73` (`/impactgraph` 404 route) and
    `apps/web/test-support/database.test.ts:7` (invalid-URL test input) — both intentionally kept.
  - `docker-compose.prod.yml:1` (`name: impactgraph` project name) and `:220`
    (`org.impactgraph.ingress-config` label key) — preserved.
  - `docs/atm-ledger.md:12-14` frozen-archive note and `:112-113` historical
    `github.com/CoachCoe/ImpactGraph/actions/runs/...` URLs — preserved.

No product-name `impactgraph`/`ImpactGraph` remains in the four target files.
