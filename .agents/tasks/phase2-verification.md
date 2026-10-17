# Phase 2 verification — deploy/config rename `impactgraph` -> `dad`

Worktree: `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/phase2-deploy-config`
Branch: `rename/phase2-deploy-config`
Iteration: FIRST (no `phase2-review.json` present at start).
Environment: Docker Desktop 4.41.2 running; Docker 28.1.1 / Compose v2.35.1-desktop.1; macOS arm64.

This file records every command and its result so the reviewer need not re-run the suite.

---

## Files changed (7 tracked + 1 gitignored, outside worktree)

```
 .env.database.example   | 28 ++++++++++++++--------------
 .env.example            |  2 +-
 .env.production.example |  8 ++++----
 Makefile                |  8 ++++----
 docker-compose.prod.yml | 14 +++++++-------
 docker-compose.yml      |  8 ++++----
 docs/edge.md            |  2 +-
 7 files changed, 35 insertions(+), 35 deletions(-)
```

Gitignored, in the MAIN checkout (NOT the worktree, never committed): `/Users/shawncoe/Documents/dev/DAD/DAD-Main/.env`
line 5 `DATABASE_URL=postgresql+psycopg://impactgraph:impactgraph@localhost:55432/impactgraph`
-> `.../dad:dad@localhost:55432/dad` (local-consistency cleanup, item 7).

### What changed, by category

1. Docker image prefix `impactgraph` -> `dad`:
   - `.env.production.example`: `IMAGE_PREFIX=impactgraph` -> `IMAGE_PREFIX=dad`.
   - `Makefile` check-images: `impactgraph/api:check`, `impactgraph/web:check` -> `dad/...` (4 lines).
   - `docker-compose.prod.yml`: both `${IMAGE_PREFIX:-impactgraph}` defaults -> `${IMAGE_PREFIX:-dad}` (lines 7, 166).

2. Top-level DB name / owner role `impactgraph` -> `dad` (per-process `dad_*_login` roles LEFT, `CHANGE_ME` placeholders LEFT):
   - `docker-compose.yml` dev Postgres: `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, healthcheck `pg_isready -U dad -d dad`. Named volume `dad_postgres` already renamed in Phase 1 — left.
   - `.env.example`: owner/password/db-name in `DONOR_GO_DATABASE_URL`.
   - `.env.database.example`: `POSTGRES_USER`, `POSTGRES_DB`, the owner+db-name in `DONOR_GO_DATABASE_URL`, and the trailing `/impactgraph` db-name in all 11 per-process URLs (the `dad_*_login` role prefixes untouched).
   - `docker-compose.prod.yml`: `POSTGRES_DB: ${POSTGRES_DB:-impactgraph}` -> `${POSTGRES_DB:-dad}` (line 26) — the canonical prod DB-name default a future AWS deploy uses; same DB-name category as the rest of item 2. (The plan's edit list did not enumerate this line, but leaving it would both contradict the DB-rename intent and fail the completeness gate, which listed only lines 1 and 220 as allowed-to-remain in this file. No behavior change beyond the rename.)
   - Legacy `.env` (gitignored, outside worktree): `DATABASE_URL` owner/password/db-name.

3. Host paths `/etc/impactgraph/...` -> `/etc/dad/...`:
   - `.env.production.example`: `INGRESS_TLS_DIR` (active) + `INGRESS_ORIGIN_PULL_CA`, `INGRESS_STAFF_ACCESS` (commented).
   - `docker-compose.prod.yml`: active TLS mount default (line 211) + 3 commented secret mounts (lines 47, 48, 162).
   - `docs/edge.md`: `INGRESS_TLS_DIR` doc-row default.

Intentionally LEFT in `docker-compose.prod.yml` (per plan): line 1 `name: impactgraph` (Compose project name) and line 220 `org.impactgraph.ingress-config` (label key).

---

## Verification commands and results

All run from the worktree root with Docker Desktop running. The stateful steps (down -v / up / migrate / seed)
were pre-approved by the owner; they drop and re-seed the disposable local dev Postgres volume.

### 1. Dev compose config resolves to the new identifiers — PASS

`docker compose config | grep POSTGRES/pg_isready/volume` produced:
```
      POSTGRES_DB: dad
      POSTGRES_PASSWORD: dad
      POSTGRES_USER: dad
        - pg_isready -U dad -d dad
        source: dad_postgres
  dad_postgres:
    name: phase2-deploy-config_dad_postgres
```

### 2. `docker compose down -v` then `docker compose up -d --wait postgres` — PASS

Volume `phase2-deploy-config_dad_postgres` created; container reported **Healthy** in ~3.7s.
This exercised the renamed healthcheck `pg_isready -U dad -d dad`.

Confirmed inside the running container:
```
docker compose exec -T postgres psql -U dad -d dad -c "SELECT current_database(), current_user;"
 current_database | current_user
------------------+--------------
 dad              | dad

-- database owner role:
 datname | owner
---------+-------
 dad     | dad
```
Evidence the dev stack came up on the new `dad` DB name and `dad` owner role.

### 3. `make migrate` — PASS

`DONOR_GO_DATABASE_URL=postgresql://dad:dad@localhost:55432/dad make migrate`:
```
cd apps/donor-api && go run ./cmd/migrate
{"...","level":"INFO","msg":"database migrations complete"}
```
Migrations applied cleanly against DB `dad` with no connection/auth error.

### 4. `make seed` — PASS

`make seed SEED_DATABASE_URL=postgresql://dad:dad@localhost:55432/dad SEED_CONFIRM=dad SEED_DONORS=200 LOAD_FIXTURE=<tmpfile outside repo>`:
```
{"donors":200,...,"rows":{...,"donors":200},"sessions":113,...,"total_seconds":0.71...}
```
Re-seeded dev data into DB `dad`. `-confirm dad` passed the seed tool's disposable-DB name check
(it requires the confirm token to equal the DB name in the URL), which itself proves the DB name is now `dad`.
Note: used `SEED_DONORS=200` instead of the 50000 default to keep the unattended run fast; this
exercises the full seed/copy/projection path against the renamed DB. Fixture written to a mktemp
path outside any git working tree (seed refuses a path inside a repo), then removed.

### 5. `make check-images` — PASS

Built both images and served the web image through nginx:
```
#18 naming to docker.io/dad/web:check
./scripts/check-ingress.sh web dad/web:check
ok    public home page is HTML
ok    staff sign-in page is HTML
ok    public.test serves the tab icon
ok    staff.test serves the tab icon
ok    public host hides the staff sign-in page
ok    staff host hides public pages
ok    staff host sends / to the back office
ok    staff robots.txt disallows everything
ok    staff page carries one X-Robots-Tag
All ingress checks passed.
```
Images tagged `dad/api:check` and `dad/web:check` (item 1/2), served via nginx successfully.

### 6. `make check-ingress` — PASS

```
./scripts/check-ingress.sh config
nginx -t (cloudflare mode): ok
nginx -t (direct mode): ok
unknown INGRESS_MODE rejected: ok
```
nginx config valid in both modes with the new `/etc/dad` TLS path convention.

### 7. Prod compose renders with the new defaults — PASS

`docker compose -f docker-compose.prod.yml config` with required vars set and a stub `ENV_FILE`
resolved successfully (exit 0). Relevant resolved lines:
```
1:name: impactgraph                      # intentional project name (left)
29:    image: dad/api:x                   # IMAGE_PREFIX default now dad
70:      org.impactgraph.ingress-config: ""  # intentional label key (left)
118:        source: /etc/dad/tls          # TLS mount default now /etc/dad
137:      POSTGRES_DB: dad                 # POSTGRES_DB default now dad
175:    image: dad/web:x                   # IMAGE_PREFIX default now dad
```
No `/etc/impactgraph` path remains in the resolved prod config. (Before all required vars were
supplied, config emitted only `required variable ... is missing a value` / missing `.env.production`
errors — never a YAML/syntax error — exactly as the plan anticipated.)

### 8. Teardown — done

`docker compose down -v` removed the dev container, network, and the `dad_postgres` volume.

---

## Completeness sweep

`git -C <worktree> grep -niI impactgraph` remaining hits are ALL in the DO-NOT-TOUCH / out-of-phase set:

- `.agents/tasks/phase1-plan.md` — descriptive plan text (Phase 1 doc).
- `docker-compose.prod.yml:1` `name: impactgraph` (Compose project name) and `:220`
  `org.impactgraph.ingress-config` (label key) — intentional, per plan.
- `README.md:1` `# ImpactGraph — DAD donor release` — product title, out of scope.
- `apps/donor-api/api/openapi.json` — API title/description, out of scope.
- `apps/web/e2e/golden-path.spec.ts:73` `/impactgraph` route, and
  `apps/web/test-support/database.test.ts:7` test URL — out of scope.
- `apps/web/package.json`, `apps/web/package-lock.json` — web package name `impactgraph-web`.
  Not a deploy/config DB/image/host identifier; not listed in Phase 2 edits. Treated as out of
  scope (consistent with Phase 1 leaving package names alone). Flagged here for the reviewer.
- Phase 3 (ATM) surface: `.github/workflows/atm.yml`, `apps/atm-ledger/**`
  (`package.json`, `package-lock.json`, `wrangler.jsonc`, `test/*.ts`), `docs/atm-ledger.md`,
  `scripts/atm-ledger.mjs`.

No `impactgraph` remains in any Phase 2 target: `.env.example`, `.env.database.example`,
`.env.production.example`, `Makefile`, `docker-compose.yml`, `docs/edge.md`, and the
image-prefix + DB-name + host-path surfaces of `docker-compose.prod.yml`.

Per-process role check: `git grep -nI 'dad_[a-z_]*_login' .env.database.example` -> all 11 roles
intact (none damaged by the DB-name rename).

---

## Result

All six required verify steps (down -v, up --wait postgres, migrate, seed, check-images,
check-ingress) PASSED against the renamed `dad` DB name/owner and `dad/*` image tags, plus the
prod-compose render check. Completeness sweep clean within scope.
