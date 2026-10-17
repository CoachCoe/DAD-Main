# Implementation Plan — Phase 1: Go module-path rename `impactgraph` → `dad`

Mechanical, low-risk rename of the product identifier in the Go monorepo, verified
by build and test. All work happens in the git worktree at:

```
/Users/shawncoe/Documents/dev/DAD/DAD-Main/.worktrees/phase1-go-module-rename
```

Branch: `rename/phase1-go-module` (already created off `dev`). All paths below are
relative to the worktree root unless absolute. Never merge/rebase/fast-forward `dev`
or `main` from inside this worktree.

## Confirmed occurrence counts (verified read-only before planning)

These are the ground-truth numbers the implementer and reviewer check completeness
against. Discovered via `grep` over the worktree:

- `apps/donor-api/go.mod` line 1: `module impactgraph/donor-api` — **1 module line**.
- `apps/donor-api/tools/go.mod` line 1: `module impactgraph/donor-api/tools` — **1 module line**. `tools` is a **separate Go module** (its own `go.mod`, a `tool` directive for staticcheck). There is **no `go.work`** and **no `replace`** directive anywhere.
- `impactgraph/donor-api` in `.go` files under `apps/donor-api`: **138 files, 310 total occurrences** (brief estimated ~312; the real confirmed count is **310**).
- `impactgraph/donor-api/tools` is imported by **zero `.go` files** — the tools module is tool-directive-only (staticcheck) and is not imported by application code. Only its own `module` line changes.
- `impactgraph/donor-api` appears in exactly **two non-`.go` files**: the two `go.mod` module lines above. No other file contains that string.
- `docker-compose.yml`: named volume `impactgraph_postgres` on **line 11** (service `volumes:` reference) and **line 35** (top-level `volumes:` declaration) — **both** must change.
- `README.md`: container name `impactgraph-go-test-postgres` on **line 106** (`docker run --name`) and **line 109** (`docker exec`) — **both** must change.
- `scripts/check-ingress.sh` line 18: `project="impactgraph-ingress-check-$$"` — **1 occurrence**.

## Why one cohesive change, not multiple features

The module-line edits and the import rewrite are a single atomic unit: renaming a
module line without rewriting imports (or vice versa) leaves the codebase
non-building. The cosmetic dev-name renames are trivial and share the same verify
pass. This is one coherent, buildable change; it is implemented and verified as a
unit.

## DO-NOT-TOUCH (deferred to later phases)

Do not edit any of these in Phase 1:

- `IMAGE_PREFIX` and Makefile image targets — `Makefile` lines 91–96 (`impactgraph/api:check`, `impactgraph/web:check`). The sed in step 2 is scoped to `impactgraph/donor-api` and will not match `impactgraph/api` or `impactgraph/web`; leave these as-is.
- Env files: `.env`, `.env.example`, `.env.database.example`, `.env.production.example`.
- DB name/owner/password inside connection strings and `docker-compose.yml` env: `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` (lines 5–7) and the healthcheck `pg_isready -U impactgraph -d impactgraph` (line 13). ONLY the volume name changes in compose.
- Mailpit service in `docker-compose.yml` — leave entirely as-is.
- Host paths like `/etc/impactgraph`.
- `docker-compose.prod.yml`.
- Anything under `apps/atm-ledger` (and its `package.json` / `wrangler.jsonc`).
- `docs/requirements/product-rename-plan.md` — do not edit.
- Do not run `make generate` to "fix" an `openapi-typescript: command not found` failure — per `apps/donor-api/AGENTS.md` that failure is unrelated to Go sqlc drift and is not caused by this change.

---

# Implementation Plan

- [ ] 1. Rename both Go module declarations.
      Edit line 1 of each go.mod: `module impactgraph/donor-api` → `module dad/donor-api`,
      and `module impactgraph/donor-api/tools` → `module dad/donor-api/tools`.
      Files: `apps/donor-api/go.mod`, `apps/donor-api/tools/go.mod`
      Verify: `grep -n '^module' apps/donor-api/go.mod apps/donor-api/tools/go.mod`
      shows `module dad/donor-api` and `module dad/donor-api/tools`. (Build is verified
      end-to-end in step 5 after imports are rewritten — the module lines alone will not
      build until step 2 runs.)

- [ ] 2. Rewrite every import path `impactgraph/donor-api` → `dad/donor-api` across all `.go` files.
      Run the exact mechanical BSD-sed command (macOS/darwin host, zsh — `sed -i ''` with the
      empty backup suffix as a SEPARATE argument is correct):
      ```sh
      cd apps/donor-api && grep -rl 'impactgraph/donor-api' --include='*.go' . | xargs sed -i '' 's|impactgraph/donor-api|dad/donor-api|g'
      ```
      This matches the 138 files / 310 occurrences confirmed above and, because the pattern
      is the full `impactgraph/donor-api` string, leaves image tags (`impactgraph/api`,
      `impactgraph/web`) and DB identifiers untouched. The tools module has no self-import in
      `.go`, so this pass only rewrites application imports.
      Files: all `.go` files under `apps/donor-api/` that import the module (138 files).
      Verify: `grep -rn 'impactgraph/donor-api' --include='*.go' apps/donor-api` returns
      nothing (zero matches); `grep -rl 'dad/donor-api' --include='*.go' apps/donor-api | wc -l`
      returns `138`.

- [ ] 3. Rename the dev-only Postgres volume in docker-compose.yml.
      Change `impactgraph_postgres` → `dad_postgres` in BOTH places: the service `volumes:`
      reference (line 11, `- impactgraph_postgres:/var/lib/postgresql/data`) and the top-level
      `volumes:` key (line 35). Do NOT change `POSTGRES_DB`/`POSTGRES_USER`/`POSTGRES_PASSWORD`
      (lines 5–7), the healthcheck (line 13), or the Mailpit service.
      Files: `docker-compose.yml`
      Verify: `grep -n 'impactgraph' docker-compose.yml` shows only the DB name/user/password
      and healthcheck lines (5–7, 13) remaining — NO `impactgraph_postgres`; and
      `grep -n 'dad_postgres' docker-compose.yml` shows two matches.
      Then `docker compose config -q` exits 0 (compose file still parses).

- [ ] 4. Rename the test-postgres container name in README.md and the ingress-check project name.
      In `README.md` change `impactgraph-go-test-postgres` → `dad-go-test-postgres` on line 106
      (the `docker run --name` line) and line 109 (the `docker exec ... pg_isready` comment line).
      In `scripts/check-ingress.sh` line 18 change `project="impactgraph-ingress-check-$$"` →
      `project="dad-ingress-check-$$"`.
      Files: `README.md`, `scripts/check-ingress.sh`
      Verify: `grep -n 'impactgraph' README.md scripts/check-ingress.sh` returns nothing;
      `grep -n 'dad-go-test-postgres' README.md` shows 2 matches and
      `grep -n 'dad-ingress-check' scripts/check-ingress.sh` shows 1 match.
      Then `shellcheck scripts/check-ingress.sh` passes (or is skipped if shellcheck absent).

- [ ] 5. Verify the Go build, vet, format, and race tests against an isolated per-worktree test DB.
      First stand up an isolated Postgres for THIS worktree so it never collides with other
      worktrees (per `apps/donor-api/AGENTS.md`: "Use an isolated database per worktree"), using
      the container-name convention renamed in step 4:
      ```sh
      docker run -d --name dad-go-test-postgres -p 127.0.0.1:55440:5432 \
        -e POSTGRES_USER=go_test -e POSTGRES_PASSWORD=go_test -e POSTGRES_DB=donor_go_phase1 \
        postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea
      # wait until ready:
      until docker exec dad-go-test-postgres pg_isready -U go_test -d donor_go_phase1; do sleep 1; done
      export TEST_DATABASE_URL=postgresql://go_test:go_test@localhost:55440/donor_go_phase1
      (cd apps/donor-api && DONOR_GO_DATABASE_URL="$TEST_DATABASE_URL" go run ./cmd/migrate)
      ```
      Then run the donor-api verification bundle (equivalent to `make test-donor-go`):
      ```sh
      cd apps/donor-api && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./...
      ```
      Files: none (verification only).
      Verify: `gofmt -l .` prints nothing (clean format); `go vet ./...` exits 0;
      `go build ./...` exits 0; `go test -race ./...` exits 0 with all packages passing.
      Clean up the container when done: `docker rm -f dad-go-test-postgres`.

- [ ] 6. Verify the tools module builds and staticcheck runs under its renamed module path.
      The tools module is exercised by the lint-go target, which invokes staticcheck via the
      tools modfile; this confirms `tools/go.mod` with its new `module dad/donor-api/tools`
      line resolves and builds.
      ```sh
      cd apps/donor-api && go build -modfile=tools/go.mod ./... ; go tool -modfile=tools/go.mod staticcheck ./...
      ```
      Files: none (verification only).
      Verify: `go tool -modfile=tools/go.mod staticcheck ./...` exits 0 (staticcheck runs and
      reports no errors attributable to the rename). If `go run $(GOVULNCHECK)` from `make
      lint-go` is unavailable offline, note that it could not be run and why.

- [ ] 7. Verify generated code is unaffected by the rename.
      Run sqlc + openapi generation and confirm no generated files drift from their sources.
      ```sh
      make generate && make check-generated
      ```
      Files: none (verification only).
      Verify: `make check-generated` exits 0 (no diff in `apps/donor-api/internal/store/queries`
      or `apps/web/lib/openapi.d.ts`). KNOWN caveat from `apps/donor-api/AGENTS.md`: the web
      `generate:api` step fails with `openapi-typescript: command not found` when that binary is
      absent — that failure is environmental and unrelated to this Go rename; if it occurs,
      record it explicitly and confirm the Go sqlc portion is clean rather than editing anything.

- [ ] 8. Final completeness sweep.
      Confirm no stray `impactgraph/donor-api` remains anywhere and the only remaining
      `impactgraph` strings are the intentional DO-NOT-TOUCH ones (DB identifiers in
      docker-compose, image tags in Makefile, env/prod/ledger files, host paths).
      Files: none (verification only).
      Verify: `grep -rn 'impactgraph/donor-api' --exclude-dir=.git .` returns nothing;
      `grep -rl 'impactgraph' --exclude-dir=.git .` lists ONLY DO-NOT-TOUCH files
      (`.env*`, `docker-compose.yml` [DB env only], `docker-compose.prod.yml`, `Makefile`,
      `.github/workflows/atm.yml`, `apps/atm-ledger/*`, `apps/web/*`, `docs/*`) — and NOT the
      two go.mod files, README.md, or scripts/check-ingress.sh.

## Notes / assumptions

- The brief estimated ~312 import occurrences; the confirmed count is **310** across **138
  files**. Completeness is checked by the zero-match grep in step 2 and step 8, not by the
  raw count, so the small delta does not affect correctness.
- Isolated test DB name chosen as `donor_go_phase1` (worktree is `phase1-go-module-rename`)
  on port `55440` matching the README pattern, to avoid colliding with the dev DB (port
  55432) or other worktrees.
- Steps 1–4 are the edits; steps 5–8 are verification and must all pass before the change is
  considered complete. The reviewer should confirm the DO-NOT-TOUCH list was honored (notably
  that `impactgraph/api:check` / `impactgraph/web:check`, the compose DB identifiers, and the
  Mailpit service are unchanged).

---

## Verification evidence (implementer, iteration 1)

Host: macOS/darwin, zsh. Go 1.27.2. Run from
`apps/donor-api` unless noted. All commands run from the worktree.

Edits applied:
- `apps/donor-api/go.mod`: `module impactgraph/donor-api` → `module dad/donor-api` ✅
- `apps/donor-api/tools/go.mod`: `module impactgraph/donor-api/tools` → `module dad/donor-api/tools` ✅
- Import rewrite via the exact BSD-sed command: 138 files, 310 import occurrences →
  `grep -rn 'impactgraph/donor-api' --include='*.go'` now returns 0; `grep -rl 'dad/donor-api'
  --include='*.go'` returns 138. ✅
- `docker-compose.yml`: volume `impactgraph_postgres` → `dad_postgres` on both the service
  reference (line 11) and the top-level declaration (line 35). DB name/user/password (5–7),
  healthcheck (13), and Mailpit left unchanged. ✅
- `README.md`: `impactgraph-go-test-postgres` → `dad-go-test-postgres` on both the `docker run`
  and `docker exec` lines. ✅
- `scripts/check-ingress.sh`: `impactgraph-ingress-check-$$` → `dad-ingress-check-$$`. ✅

Results (pass/fail per command):
- `gofmt -l .` — initially listed ~75 files because `dad/donor-api/...` sorts before
  `github.com/...` where `impactgraph/donor-api/...` had sorted after; this is the expected
  mechanical fallout of the rename. Ran `gofmt -w .` to re-sort the import blocks (import-line
  changes only — verified the full `.go` diff contains no non-import line changes). `gofmt -l .`
  now outputs **nothing**. ✅ PASS
- `go build ./...` — **PASS** (proves every import resolved). ✅
- `go vet ./...` — **PASS**. ✅
- `go tool -modfile=tools/go.mod staticcheck ./...` (the `make lint-go` staticcheck step) —
  **PASS**; confirms `tools/go.mod` resolves under its new `module dad/donor-api/tools` path.
  `(cd tools && go list -m)` prints `dad/donor-api/tools`. ✅
- `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` (the `make lint-go` govulncheck step) —
  **PASS**; 0 vulnerabilities called by our code (1 in a required-but-uncalled module,
  unrelated to this rename). ✅
- `go test -race ./...` — **NOT RUN**: Docker daemon is not running in this environment, so the
  isolated per-worktree Postgres (`dad-go-test-postgres`, DB `donor_go_phase1`, port 55440) and
  the `go run ./cmd/migrate` step per `apps/donor-api/AGENTS.md` could not be started. This is an
  environmental blocker, not a defect of the rename. The rename is import-path-only; `go build`
  and `go vet` across all packages (incl. all `_test.go` files, which `go vet` type-checks)
  passing proves every rewritten import resolves. ⚠️ BLOCKED (environment)
- `make generate && make check-generated` — **NOT RUN**: `make generate` invokes
  `sqlc/sqlc:1.31.1` in Docker, which failed with a Docker daemon 500/ping error (daemon down),
  not the `openapi-typescript: command not found` case. No sqlc drift is possible from this
  change regardless: the generated files under `internal/store/queries/*` had **zero** diff
  (they do not import the module path), so the import rewrite did not touch generated code. ⚠️
  BLOCKED (environment); drift ruled out by zero-diff on the generated dir.

Completeness sweep:
- `grep -rn 'impactgraph/donor-api' --exclude-dir=.git .` → only matches are descriptive
  mentions inside this plan doc (`.agents/tasks/phase1-plan.md`); zero in code/config. ✅
- `grep -rl 'impactgraph' --exclude-dir=.git .` (excluding this plan doc) lists ONLY
  DO-NOT-TOUCH files: `Makefile`, `.env*`, `docs/*`, `.github/workflows/atm.yml`,
  `docker-compose.yml` (DB env/healthcheck only), `docker-compose.prod.yml`, `apps/web/*`,
  `apps/atm-ledger/*`. The two go.mod files, README.md, and scripts/check-ingress.sh are NOT in
  the list. ✅
