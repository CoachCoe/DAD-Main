# Implementation Plan — Phase 2: deploy/config name rename `impactgraph` → `dad`

Worktree (ALL work happens here): `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/phase2-deploy-config`
Branch: `rename/phase2-deploy-config` (based off `dev`). Working tree is clean at plan time.

## Scope and confirmed facts (verified by reading each file)

Phase 2 changes three categories: (1) Docker image prefix, (2) top-level DB name/owner,
(3) host-path conventions `/etc/impactgraph/...` → `/etc/dad/...`. All targets below were
confirmed with `git -C <worktree> grep -niI impactgraph` and by reading each file.

CRITICAL distinction for the DB rename — change ONLY the top-level DB **name** and **owner
role** (`impactgraph`). LEAVE every already-correct per-process role (`dad_api_login`,
`dad_worker_stripe_login`, … — all already `dad_*`). The password token in the example
files is literally `CHANGE_ME` (a placeholder) and is left as-is; the dev-only
`docker-compose.yml` sets `POSTGRES_PASSWORD: impactgraph`, which DOES change (it is the
dev owner's password, not a placeholder).

DO NOT TOUCH (out of scope — other phases / intentional):
- Go module/imports `impactgraph/donor-api` (Phase 1, already done).
- Anything under `apps/atm-ledger`, `.github/workflows/atm.yml`, `scripts/atm-ledger.mjs`,
  `docs/atm-ledger.md` (Phase 3).
- `README.md` line 1 (`# ImpactGraph — DAD donor release`) — product title, not a
  deploy/config name; not in Phase 2 scope.
- `docs/requirements/product-rename-plan.md`, `docs/requirements/product-rename-progress.md`.
- The `dad_*` per-process role names (already correct).
- `apps/web/e2e/golden-path.spec.ts` `/impactgraph` route, `apps/web/test-support/database.test.ts`
  test URL, `openapi.json` title — not deploy/config names, out of scope.

Assumption on the legacy `.env` (see item 7): the brief says to update the Python-era
gitignored `.env`. That file does NOT exist in this worktree. It exists only in the main
checkout at `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.env` (line 5:
`DATABASE_URL=postgresql+psycopg://impactgraph:impactgraph@localhost:55432/impactgraph`).
It is gitignored and never committed. Item 7 handles it explicitly and separately so it
cannot pollute the branch; if the implementer must stay strictly inside the worktree,
item 7 is a no-op and that is acceptable (the file is stale and unused by the Go app).

---

# Implementation Plan

- [ ] 1. **Docker image prefix in `.env.production.example`.**
      Change the single IMAGE_PREFIX assignment.
      File: `.env.production.example`
      - Line 2: `IMAGE_PREFIX=impactgraph` → `IMAGE_PREFIX=dad`
      Verify: `git grep -nI 'IMAGE_PREFIX' .env.production.example` shows only `IMAGE_PREFIX=dad`.

- [ ] 2. **Docker image prefix in `Makefile` (check-images target).**
      Replace the `impactgraph/api` and `impactgraph/web` image tags with `dad/…` (4 lines).
      File: `Makefile`
      - Line 91: `docker build -f apps/donor-api/Dockerfile -t impactgraph/api:check .`
        → `… -t dad/api:check .`
      - Line 92: `docker run --rm --entrypoint sh impactgraph/api:check -c \`
        → `docker run --rm --entrypoint sh dad/api:check -c \`
      - Line 94: `docker build -f apps/web/Dockerfile -t impactgraph/web:check \`
        → `… -t dad/web:check \`
      - Line 96: `./scripts/check-ingress.sh web impactgraph/web:check`
        → `./scripts/check-ingress.sh web dad/web:check`
      Verify: `git grep -nI 'impactgraph/\(api\|web\):check' Makefile` returns nothing;
      `make -n check-images` prints commands referencing `dad/api:check` and `dad/web:check`.

- [ ] 3. **Docker image prefix defaults in `docker-compose.prod.yml`.**
      Change both `${IMAGE_PREFIX:-impactgraph}` defaults. Do NOT change `name: impactgraph`
      (line 1, Compose project name) or `org.impactgraph.ingress-config` (line 220, label
      key) in THIS item — those are handled nowhere in Phase 2 scope; leave them. Only the
      two image-prefix defaults change here.
      File: `docker-compose.prod.yml`
      - Line 7: `  image: ${IMAGE_PREFIX:-impactgraph}/api:${IMAGE_TAG:?set IMAGE_TAG}`
        → `  image: ${IMAGE_PREFIX:-dad}/api:${IMAGE_TAG:?set IMAGE_TAG}`
      - Line 166: `    image: ${IMAGE_PREFIX:-impactgraph}/web:${IMAGE_TAG:?set IMAGE_TAG}`
        → `    image: ${IMAGE_PREFIX:-dad}/web:${IMAGE_TAG:?set IMAGE_TAG}`
      Verify: `git grep -nI 'IMAGE_PREFIX:-' docker-compose.prod.yml` shows both defaults as
      `:-dad`; `git grep -nI 'IMAGE_PREFIX:-impactgraph' docker-compose.prod.yml` returns nothing.

- [ ] 4. **Dev Postgres DB name/owner/password in `docker-compose.yml`.**
      Change the four dev Postgres identifiers to `dad`. LEAVE the named volume `dad_postgres`
      (already renamed in Phase 1, lines 11 and 35) untouched.
      File: `docker-compose.yml`
      - Line 5: `      POSTGRES_DB: impactgraph`        → `      POSTGRES_DB: dad`
      - Line 6: `      POSTGRES_USER: impactgraph`      → `      POSTGRES_USER: dad`
      - Line 7: `      POSTGRES_PASSWORD: impactgraph`  → `      POSTGRES_PASSWORD: dad`
      - Line 13: `      test: ["CMD-SHELL", "pg_isready -U impactgraph -d impactgraph"]`
        → `      test: ["CMD-SHELL", "pg_isready -U dad -d dad"]`
      Verify: `git grep -nI impactgraph docker-compose.yml` returns nothing (the volume is
      already `dad_postgres`); `docker compose config` parses without error (confirms valid YAML).

- [ ] 5. **Top-level DB name/owner in `.env.example` connection string.**
      One line; the owner role, its password, and the DB name are all literally `impactgraph`
      here (dev convenience). All three `impactgraph` tokens on this line are owner/db-name
      occurrences, so change all three → `dad`. Do NOT touch the `?role=dad_api` example in the
      comment block (line 6) — it is already `dad_*` and correct.
      File: `.env.example`
      - Line 3: `DONOR_GO_DATABASE_URL=postgresql://impactgraph:impactgraph@localhost:55432/impactgraph`
        → `DONOR_GO_DATABASE_URL=postgresql://dad:dad@localhost:55432/dad`
      Verify: `git grep -nI impactgraph .env.example` returns nothing.

- [ ] 6. **DB name/owner in `.env.database.example` — per-reference, change DB-name/owner only, LEAVE `dad_*` roles.**
      This file has 14 `impactgraph` references across lines 4–23. Classification of EACH
      (change = DB-name/owner occurrence; leave = already-correct `dad_*` role). The
      `CHANGE_ME` password placeholders are NOT `impactgraph` and are left untouched.
      File: `.env.database.example`
      - Line 4: `POSTGRES_USER=impactgraph` — **owner role → CHANGE** to `POSTGRES_USER=dad`.
      - Line 6: `POSTGRES_DB=impactgraph` — **DB name → CHANGE** to `POSTGRES_DB=dad`.
      - Line 9: `DONOR_GO_DATABASE_URL=postgresql://impactgraph:CHANGE_ME@postgres:5432/impactgraph`
        — the leading `impactgraph` is the **owner role → CHANGE**; the trailing `/impactgraph`
        is the **DB name → CHANGE**; `CHANGE_ME` is the password placeholder → LEAVE.
        Result: `DONOR_GO_DATABASE_URL=postgresql://dad:CHANGE_ME@postgres:5432/dad`
      - Lines 13–23 (11 per-process role URLs) — the role prefix before `:CHANGE_ME` is
        `dad_*_login` (already correct → **LEAVE the role**); only the trailing `/impactgraph`
        is the **DB name → CHANGE** to `/dad`. Apply the SAME pattern to all 11:
        - Line 13 `DONOR_API_DATABASE_URL=postgresql://dad_api_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
        - Line 14 `DONOR_WORKER_STRIPE_DATABASE_URL=postgresql://dad_worker_stripe_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
        - Line 15 `DONOR_WORKER_PROJECTIONS_DATABASE_URL=postgresql://dad_worker_projections_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
        - Line 16 `DONOR_WORKER_LOGIN_EMAIL_DATABASE_URL=postgresql://dad_worker_login_email_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
        - Line 17 `DONOR_WORKER_EMAIL_CHANGES_DATABASE_URL=postgresql://dad_worker_email_changes_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
        - Line 18 `DONOR_WORKER_MESSAGES_DATABASE_URL=postgresql://dad_worker_messages_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
        - Line 19 `DONOR_WORKER_PUSH_DATABASE_URL=postgresql://dad_worker_push_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
        - Line 20 `DONOR_MONITOR_DATABASE_URL=postgresql://dad_monitor_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
        - Line 21 `DONOR_STAFF_CLI_DATABASE_URL=postgresql://dad_staff_cli_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
        - Line 22 `DONOR_REKEY_DATABASE_URL=postgresql://dad_rekey_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
        - Line 23 `DONOR_RECONCILE_DATABASE_URL=postgresql://dad_reconcile_login:CHANGE_ME@postgres:5432/impactgraph` → `…/dad`
      Implementation note: the trailing-DB-name edits on lines 9 and 13–23 can be done safely
      with `s|@postgres:5432/impactgraph|@postgres:5432/dad|g` (matches only the DB-name
      occurrence, never a role prefix). Lines 4 and 6 are separate `=impactgraph` assignments.
      Verify: `git grep -nI impactgraph .env.database.example` returns nothing, AND
      `git grep -nI 'dad_[a-z_]*_login' .env.database.example` still returns all 11 role lines
      (confirms no `dad_*` role was damaged).

- [ ] 7. **Legacy Python-era `.env` (gitignored, lives in the MAIN checkout, not the worktree).**
      The file is `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.env` (absolute; it is NOT inside
      the worktree and is gitignored, so it will never be committed). Update for local
      consistency only.
      File (absolute, outside worktree): `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.env`
      - Line 5: `DATABASE_URL=postgresql+psycopg://impactgraph:impactgraph@localhost:55432/impactgraph`
        → `DATABASE_URL=postgresql+psycopg://dad:dad@localhost:55432/dad`
      Note: all three `impactgraph` tokens are owner / password / DB-name (none are `dad_*`
      roles), so change all three. Because this path is outside the worktree and gitignored,
      this edit has no effect on the branch or any verification; it is purely local cleanup.
      If the implementer is constrained to the worktree, SKIP this item (acceptable — the file
      is stale and unused by the Go app's per-role URLs).
      Verify: `grep -n impactgraph /Users/shawncoe/Documents/dev/DAD/DAD-Main/.env` returns
      nothing (or the file is absent / skipped).

- [ ] 8. **Host-path conventions in `.env.production.example` (`/etc/impactgraph` → `/etc/dad`).**
      One active value plus two commented examples.
      File: `.env.production.example`
      - Line 18: `INGRESS_TLS_DIR=/etc/impactgraph/tls` → `INGRESS_TLS_DIR=/etc/dad/tls`
      - Line 20: `# INGRESS_ORIGIN_PULL_CA=/etc/impactgraph/origin-pull-ca.pem`
        → `# INGRESS_ORIGIN_PULL_CA=/etc/dad/origin-pull-ca.pem`
      - Line 22: `# INGRESS_STAFF_ACCESS=/etc/impactgraph/staff-access.conf`
        → `# INGRESS_STAFF_ACCESS=/etc/dad/staff-access.conf`
      Verify: `git grep -nI '/etc/impactgraph' .env.production.example` returns nothing.
      (After items 1 and 8, `git grep -nI impactgraph .env.production.example` returns nothing.)

- [ ] 9. **Host-path conventions in `docker-compose.prod.yml` (`/etc/impactgraph` → `/etc/dad`).**
      One active TLS mount default plus three commented secret-mount examples. Leave the
      Compose project name (line 1) and the `org.impactgraph.*` label key (line 220) untouched —
      they are not host paths and not in scope.
      File: `docker-compose.prod.yml`
      - Line 47 (commented): `    #   - /etc/impactgraph/wallet:/run/secrets/wallet:ro`
        → `    #   - /etc/dad/wallet:/run/secrets/wallet:ro`
      - Line 48 (commented): `    #   - /etc/impactgraph/push:/run/secrets/push:ro`
        → `    #   - /etc/dad/push:/run/secrets/push:ro`
      - Line 162 (commented): `    #   - /etc/impactgraph/push:/run/secrets/push:ro`
        → `    #   - /etc/dad/push:/run/secrets/push:ro`
      - Line 211 (active): `      - ${INGRESS_TLS_DIR:-/etc/impactgraph/tls}:/etc/nginx/tls:ro`
        → `      - ${INGRESS_TLS_DIR:-/etc/dad/tls}:/etc/nginx/tls:ro`
      Verify: `git grep -nI '/etc/impactgraph' docker-compose.prod.yml` returns nothing;
      `docker compose -f docker-compose.prod.yml config` parses (set a dummy `IMAGE_TAG=x` and
      required vars, or expect only unset-var errors, not YAML/syntax errors). After items 3 and
      9, the only remaining `impactgraph` strings in this file are the intentional project name
      (line 1) and the `org.impactgraph.ingress-config` label key (line 220).

- [ ] 10. **Host-path doc row in `docs/edge.md`.**
      One table row default value.
      File: `docs/edge.md`
      - Line 31: `| \`INGRESS_TLS_DIR\` | \`/etc/impactgraph/tls\` | Host directory with \`fullchain.pem\` and \`privkey.pem\`, readable by uid 101. |`
        → change the default cell to `` `/etc/dad/tls` ``.
      Verify: `git grep -nI '/etc/impactgraph' docs/edge.md` returns nothing.

- [ ] 11. **Final scope audit — confirm only intentional `impactgraph` strings remain.**
      No file edits; this is the completeness gate for the branch.
      Run: `git -C <worktree> grep -niI impactgraph -- . ':!.agents' ':!apps/atm-ledger' ':!docs/atm-ledger.md' ':!.github/workflows/atm.yml' ':!scripts/atm-ledger.mjs' ':!docs/requirements'`
      Verify: the ONLY remaining matches are the DO-NOT-TOUCH set:
      - `docker-compose.prod.yml:1` `name: impactgraph` (Compose project name) and `:220`
        `org.impactgraph.ingress-config` (label key).
      - `README.md:1` `# ImpactGraph — DAD donor release` (product title).
      - `apps/web/e2e/golden-path.spec.ts` `/impactgraph` route, `apps/web/test-support/database.test.ts`
        test URL, `apps/donor-api/api/openapi.json` title.
      - `.agents/tasks/phase1-plan.md` and this plan (`.agents/tasks/phase2-plan.md`) — descriptive text.
      No `impactgraph` may remain in: `.env.example`, `.env.database.example`,
      `.env.production.example`, `Makefile`, `docker-compose.yml`, `docs/edge.md`, and the
      image-prefix defaults of `docker-compose.prod.yml`.

---

## Verification procedure (from the Phase 2 brief / product-rename-plan.md "Verify")

Run after all edits, from the worktree root
`/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/phase2-deploy-config`. Requires Docker
Desktop running. These commands come from CONTRIBUTING.md and the plan's Verify block.

```sh
# 1. Dev stack comes up clean with the new DB name (drops the disposable dev volume):
docker compose down -v           # removes dad_postgres volume (disposable dev data)
docker compose up -d --wait postgres   # must become healthy with POSTGRES_DB=dad / -U dad

# 2. Migrate + seed against the new `dad` DB:
make migrate                     # runs apps/donor-api ./cmd/migrate
make seed                        # re-seeds dev data

# 3. Image build + ingress config checks (required after deploy/Dockerfile/compose edits):
make check-images                # builds dad/api:check and dad/web:check, serves via nginx
make check-ingress               # nginx config valid with the new /etc/dad paths
```

Expected outcomes:
- `docker compose up -d --wait postgres` reports the postgres service **healthy** — this
  exercises the renamed `pg_isready -U dad -d dad` healthcheck (item 4).
- `make migrate` and `make seed` complete without connection/auth errors against DB `dad`.
- `make check-images` builds images tagged `dad/api:check` and `dad/web:check` (item 2) and
  the ingress web check passes.
- `make check-ingress` validates nginx config successfully.

Notes / limitations the implementer must record:
- If Docker Desktop is not running, `docker compose` and the `make` targets that depend on it
  cannot run; record that these were not executed rather than claiming success.
- `make test` / `make lint` / `make test-browser-e2e` are not required to change for a pure
  config/compose rename, but CONTRIBUTING.md asks that after changing `deploy/`, `scripts/`,
  or a Dockerfile you run `make check-ingress smoke-ingress check-images`. This phase does not
  touch `deploy/`, `scripts/`, or Dockerfiles, so `check-images` + `check-ingress` (above)
  are the targeted checks; `smoke-ingress` requires a running stack and may be run if the
  environment allows.

## Rollback

All edits are config/compose on the branch — `git restore <file>` reverts any file. The only
stateful effect is `docker compose down -v` dropping the local dev Postgres volume (disposable);
`make seed` restores dev data. The legacy `.env` (item 7) is gitignored and outside the
worktree, so it has no bearing on the branch.
