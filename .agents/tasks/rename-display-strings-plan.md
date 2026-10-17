# Implementation Plan — product-facing string rename: `ImpactGraph`/`impactgraph` → `DAD`/`dad`

Small, precise, product-facing display-string rename. All paths are inside the worktree
`/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings` (branch
`rename/display-strings`, based off `dev`). Use absolute paths. Investigation-and-plan was
done against the actual files; findings and decisions are recorded inline below.

## Key investigation findings (read before starting)

- **openapi.json is HAND-MAINTAINED, NOT generated.** The root `Makefile` declares
  `GENERATED := apps/donor-api/internal/store/queries apps/web/lib/openapi.d.ts`.
  `make generate` runs sqlc (produces the Go queries) and `cd apps/web && npm run generate:api`
  (produces `apps/web/lib/openapi.d.ts` **from** `apps/donor-api/api/openapi.json`).
  So `apps/donor-api/api/openapi.json` is the **source of truth / input** to generation, not an
  output. There is NO Go source that builds this file: `git grep -nI 'ImpactGraph donor API'`
  and `git grep -nI 'Dynamic Aid Delivery) donor API'` across `apps/donor-api/**` match ONLY
  `apps/donor-api/api/openapi.json` itself — no OpenAPI builder, swagger annotations, or
  generator under `internal/httpapi` or `cmd`. **Decision: the coder edits
  `apps/donor-api/api/openapi.json` directly.** Do NOT look for a Go source to edit; there is none.
- **The openapi title/description do NOT propagate into generated code.** The generated web
  types file `apps/web/lib/openapi.d.ts` contains neither `ImpactGraph` nor the title/description
  text (`git grep` on that file is empty). Therefore editing `info.title`/`info.description` will
  NOT cause `make check-generated` to report drift. No regeneration is required for this change
  (openapi regeneration is only needed when the paths/schemas change, which they are not here).
- **README has exactly ONE product-name occurrence**: line 1, the heading. No other prose
  `ImpactGraph`/`impactgraph` occurs in README.md, and there are NO historical GitHub Actions
  URLs or old-repo URLs in README.md to preserve.
- **package-lock.json has exactly two `"name": "impactgraph-web"` lines**: line 2 (top-level) and
  line 8 (the `""` package entry). Both must become `dad-web`.
- **Tooling is available locally**: `npm` 11.5.2, `node` v24.15.0, `python3` all on PATH.
  Docker is available per the task brief (not needed for this change — see openapi finding above).

## Explicit exclusions (DO NOT TOUCH)

These contain `impactgraph`/`ImpactGraph` but are intentionally out of scope. Leave them
byte-for-byte unchanged:

- `docs/atm-ledger.md` — frozen-archive product-rename note (lines 12–14) and the historical
  CoachCoe/ImpactGraph GitHub Actions-run URLs (lines 112–113:
  `https://github.com/CoachCoe/ImpactGraph/actions/runs/38488982545` and `…/38489085019`).
- `.github/workflows/atm.yml` line 102 — the separate CoachCoe/atm key-file reference
  `atm/integrations/github/impactgraph-evidence.pub.pem`.
- `docker-compose.prod.yml` line 1 (`name: impactgraph`, Compose project name) and line 220
  (`org.impactgraph.ingress-config`, label key).
- `apps/web/e2e/golden-path.spec.ts` — asserts `/impactgraph` 404s (a removed route). LEAVE.
- `apps/web/test-support/database.test.ts` — uses `impactgraph` as invalid-URL test input. LEAVE.
- Anything under `docs/requirements/` and the prior-phase plans in `.agents/tasks/*.md`. LEAVE.

---

# Implementation Plan

- [ ] 1. Rename the product name in the README heading (and confirm no other prose occurrences).
      In `README.md` change line 1 from `# ImpactGraph — DAD donor release` to `# DAD — donor release`.
      This is the only `ImpactGraph`/`impactgraph` product-name occurrence in README.md; there are no
      historical URLs in this file to preserve. Make no other edits to README.md.
      Files: `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings/README.md`
      Verify: `git -C /Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings grep -niI 'impactgraph' -- README.md`
      returns nothing (empty output).

- [ ] 2. Rename the web package name in package.json.
      In `apps/web/package.json` change `"name": "impactgraph-web"` (line 2) to `"name": "dad-web"`.
      Do not change `version` or any other field.
      Files: `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings/apps/web/package.json`
      Verify: `git -C /Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings grep -niI 'impactgraph' -- apps/web/package.json`
      returns nothing.

- [ ] 3. Resync package-lock.json to the new package name (depends on item 2).
      The two `"name": "impactgraph-web"` lines in `apps/web/package-lock.json` (line 2, top-level;
      line 8, the `""` package entry) must become `"dad-web"`, with no other changes.
      **Try FIRST (preferred):** run `npm install --package-lock-only` in
      `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings/apps/web`
      (npm 11.5.2 / node v24.15.0 are installed). This rewrites only the lock's name fields to match
      package.json. **FALLBACK (if offline/registry-unreachable or the command fails):** hand-edit the
      two `"name": "impactgraph-web"` occurrences (lines 2 and 8) to `"name": "dad-web"`.
      After either approach, confirm the diff touches ONLY the two name fields: no `version` change
      and no dependency/`lockfileVersion`/`packages` entry changes
      (`git -C <worktree> diff -- apps/web/package-lock.json`).
      Files: `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings/apps/web/package-lock.json`
      Verify: `git -C /Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings grep -niI 'impactgraph' -- apps/web/package-lock.json`
      returns nothing, AND the diff for this file shows only the two name-field lines changed.

- [ ] 4. Verify the web package rename does not break typecheck or build (depends on items 2–3).
      Run the project's typecheck and production build (scripts confirmed present in
      `apps/web/package.json`: `"typecheck": "tsc --noEmit"`, `"build": "next build"`). If
      `node_modules` is absent, run `npm ci` in `apps/web` first.
      Files: none (verification only).
      Verify: in `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings/apps/web`
      run `npm run typecheck` then `npm run build`; both exit 0 (clean typecheck, successful Next build).

- [ ] 5. Rename the product name in the donor-API OpenAPI title and description.
      `apps/donor-api/api/openapi.json` is the hand-maintained source of truth (NOT generated — see
      findings). Make exactly two product-name edits, changing ONLY the product-name wording and
      leaving every other character byte-for-byte identical:
      (a) `info.title` (line 4): `"ImpactGraph donor API"` → `"DAD donor API"`.
      (b) `info.description` (line 10): the leading phrase `The ImpactGraph (Dynamic Aid Delivery) donor API`
          → `The DAD (Dynamic Aid Delivery) donor API`. Do not alter any other text in the description
          (the rest of the long description string, including the `X-DAD-Client` header name, stays exactly as-is).
      Do NOT touch any Go source (there is none for this string) and do NOT hand-edit any generated file.
      Files: `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings/apps/donor-api/api/openapi.json`
      Verify: `python3 -m json.tool /Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings/apps/donor-api/api/openapi.json > /dev/null`
      exits 0 (still valid JSON), AND
      `git -C /Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings grep -niI 'impactgraph' -- apps/donor-api/api/openapi.json`
      returns nothing.

- [ ] 6. Confirm no OpenAPI-generated drift results from item 5 (depends on item 5).
      The generated web types (`apps/web/lib/openapi.d.ts`) do not include the title/description, so no
      regeneration is expected to be required. Confirm this holds: run `make generate` from the worktree
      root (regenerates sqlc queries in Docker and `apps/web/lib/openapi.d.ts` from the openapi contract),
      then `git status --porcelain -- apps/donor-api/internal/store/queries apps/web/lib/openapi.d.ts`.
      Expected: empty output (no drift), i.e. the title/description change produced no change in generated
      code. If `make generate` cannot run (Docker unavailable for sqlc), this verification may be skipped;
      note it, since the openapi change is known not to affect generated output.
      Files: none (verification only).
      Verify: `make -C /Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings generate`
      succeeds and
      `git -C /Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings status --porcelain -- apps/donor-api/internal/store/queries apps/web/lib/openapi.d.ts`
      is empty.

- [ ] 7. Final product-name sweep across the changed files (depends on items 1–6).
      Confirm no product-name `impactgraph`/`ImpactGraph` strings remain in the four target files and that
      only the intentional exclusions still carry the old name elsewhere.
      Files: none (verification only).
      Verify: `git -C /Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/rename-display-strings grep -niI 'impactgraph' -- README.md apps/web/package.json apps/web/package-lock.json apps/donor-api/api/openapi.json`
      returns EMPTY. (A whole-worktree `git grep -niI impactgraph` should still match ONLY the DO-NOT-TOUCH
      files listed under "Explicit exclusions": `docs/atm-ledger.md` incl. the two historical
      CoachCoe/ImpactGraph Actions-run URLs, `.github/workflows/atm.yml`, `docker-compose.prod.yml`,
      `apps/web/e2e/golden-path.spec.ts`, `apps/web/test-support/database.test.ts`, and the prior-phase
      plans under `.agents/tasks/`.)

## Notes / assumptions

- No design document was supplied; the design decisions above (openapi.json is hand-maintained and
  edited directly; package-lock resynced via `npm install --package-lock-only` with a hand-edit fallback;
  README has a single heading occurrence) are grounded in the files as they exist in the worktree.
- Items are ordered by dependency: package.json (2) before lock resync (3) before web build (4);
  openapi edit (5) before generated-drift check (6); final sweep (7) last. Each item leaves the tree
  buildable.
- Commit the result on branch `rename/display-strings`; never push or merge/rebase `dev` from the worktree.
