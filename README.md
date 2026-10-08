# ImpactGraph — DAD donor release

The R1 donor application for Dynamic Aid Delivery: guest giving, direct Link and IRL
referrals, a donor dashboard, recurring-gift controls, and an audited staff back office.
The backend is Go, the web application is TypeScript/Next.js, and persistence is
PostgreSQL. Licensed under MIT.

**This build accepts Stripe test payments only.** Live donations, approved tax receipts,
Wallet passes, and release acceptance are not established by running it locally. See
[the product contract](docs/donor-domain.md) and [security](SECURITY.md).
Optional [Wallet signing](docs/wallet.md), [analytics definitions](docs/analytics.md),
and [shared design values](docs/design-system.md) have separate setup and acceptance boundaries.

R1 is a donor product. It does not deliver aid, assess recipients, run AI services, or
publish blockchain transactions. The enrolment export is an inactive R2 stub.

## Local setup

Requires Go 1.27.1, Node 22.13+ (or Node 24), npm, and Docker Compose, which runs
PostgreSQL 16 on 127.0.0.1.
Commands below run from the repository root. The local database uses port 55432.

```sh
cp .env.example .env
# Edit .env, then load it in each terminal that runs an application command:
set -a
. ./.env
set +a
make install
make db-up
make migrate
make api                      # terminal 1
make web                      # terminal 2, with the same environment loaded
```

Open http://localhost:3000. Without Stripe, SMTP, and encryption configuration, public
pages and empty states work; giving and account email are unavailable. The staff back
office runs only on its own host, `DONOR_STAFF_ORIGIN`: locally
http://staff.localhost:3000/staff, which browsers resolve to this machine.
The frontend proxies `/api/donor/*` to `DONOR_API_BASE_URL`; the API listens on 8002.
Do not put backend secrets in `NEXT_PUBLIC_*` variables.

Use two independently generated encryption keys, for email and for content:

```sh
openssl rand -base64 32 | tr '+/' '-_'
```

Each setting also accepts a comma-separated list, newest key first, which is how keys
are rotated ([operations](docs/operations.md#encryption-keys)).

Staff sign in with passkeys. With `DONOR_STAFF_ORIGIN` set, provision the first named
administrator:

```sh
cd apps/donor-api
go run ./cmd/staff --email named.person@example.org --name 'Named Person'
```

The command prints a private, one-use invitation to the staff host, where the
administrator creates a passkey. Protect that output. It refuses if any staff account
already exists. Later invitations and recovery use `/staff` and require administrator
permission. Provision two individual administrators, each with their own passkey.

## Payments and workers

Configure a Stripe **test** secret key and webhook signing secret; the API refuses to
start with only one of them. The receiver accepts
snapshot events at `2026-09-30.endive`, matching stripe-go v87. Card entry and card updates
are hosted by Stripe. Use `/api/donor/stripe/webhook` as the externally reachable route.
See [operations](docs/operations.md) for supported events and recovery behavior.

Workers start only with `DONOR_GO_WORKERS_ENABLED=true`. Run each kind in a separate
terminal with the environment loaded:

```sh
make worker KIND=stripe
make worker KIND=projections
make worker KIND=login-email
make worker KIND=email-changes
make worker KIND=messages
```

SMTP delivery requires STARTTLS and the configured encryption key. Workers send real
email to the configured addresses, even while gifts are test payments. Local tests use
injected transports. Never point synthetic test data at a real mail provider.

## Checks

Integration tests require a dedicated synthetic database named `donor_go` and a test
role with `CREATEDB` permission. Migration tests create and remove isolated databases.
Tests must never run against the application database.

```sh
docker run -d --name impactgraph-go-test-postgres -p 127.0.0.1:55440:5432 \
  -e POSTGRES_USER=go_test -e POSTGRES_PASSWORD=go_test -e POSTGRES_DB=donor_go \
  postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea
# Wait for: docker exec impactgraph-go-test-postgres pg_isready -U go_test -d donor_go
export TEST_DATABASE_URL=postgresql://go_test:go_test@localhost:55440/donor_go
(cd apps/donor-api && DONOR_GO_DATABASE_URL="$TEST_DATABASE_URL" go run ./cmd/migrate)
make test
make lint
make check-ingress smoke-ingress check-images
(cd apps/web && npx playwright install chromium)
make test-browser-e2e
```

Browser tests run against `next dev` by default; set `E2E_PRODUCTION=1` to run them
against a production build (`npm run build && npm start`), where account and staff pages
use the nonce-based CSP. Next allows
one dev server per checkout, so stop any running `make web` first. Staff pages are tested
on `DONOR_STAFF_ORIGIN` (default http://staff.localhost:3000) with Chromium's virtual
authenticator; the staff journey provisions or recovers `e2e.administrator@example.org`
with `cmd/staff`, so point `DONOR_GO_DATABASE_URL` at a synthetic database.

`make test` checks Go formatting, vet, race-enabled tests, compilation, frontend lint,
unit tests, generated route types, TypeScript, and the production build. Browser tests
start a local Go API and Next server. Use a synthetic application database and no live
SMTP settings for those tests. `make lint` runs staticcheck, govulncheck, `npm audit`, and
shellcheck when installed. `make check-ingress` runs `nginx -t` on the production ingress
in both modes; `make smoke-ingress` checks its hosts, client addresses, body sizes, and
rate limits in front of a stub web service; `make check-images` builds both production
images and serves the web image through nginx. CI runs all of these through the same
`make` targets, with PostgreSQL and Chromium, and also validates the production Compose
file. CI runs browser tests in both development and production modes. `npm start` serves
the standalone build and copies its static assets first, matching the container's server.
For a local production preview, run `HOSTNAME=127.0.0.1 PORT=3000 npm start` from
`apps/web` after building.

SQL lives in `apps/donor-api/db`. `make generate` runs pinned sqlc and regenerates the web
app's API types from the OpenAPI contract; commit each source with its generated code.
`make check-generated` fails if either has drifted.
Applied migrations are immutable and checksum-checked.

Capacity is measured against a seeded, disposable deployment: `make seed` fills a
dedicated database with 50,000 synthetic donors and writes a private fixture of session
tokens and referral codes, and `make loadtest-seeded` runs a referral-spike burst and soak
against an API serving it. See [operations](docs/operations.md#load-testing-with-representative-data)
for the guard, the mix, budgets, and reading results. For a quick check of a few
endpoints, `-url` reads a comma-separated list in equal measure:

```sh
cd apps/donor-api
go run ./cmd/loadtest -url "http://localhost:8002/donor/public/content" -duration 30s -concurrency 128 -max-p99 250ms
```

The runner reports throughput, statuses, and latency percentiles overall and per
endpoint as JSON. It fails on non-200 responses, timeouts, oversized bodies, or a p99
above `-max-p99`; `DONOR_LOAD_COOKIE` sends a synthetic donor's session cookie. Results
from an empty database do not establish capacity.

## Containers and deployment

See [operations](docs/operations.md) and [edge and ingress](docs/edge.md). Docker images
run as non-root users. Only nginx publishes ports: it terminates HTTPS in front of the web
service, behind Cloudflare or on its own, and the API binds to loopback. No command
enables workers or deletes database volumes implicitly. Test-only readiness is not approval to solicit live donations.

## Layout

- `apps/donor-api/cmd`: API, workers, migrations, administrator bootstrap, monitoring, key rotation, load testing.
- `apps/donor-api/internal`: HTTP, domain rules, persistence, Stripe, email, credentials.
- `apps/donor-api/db`: versioned SQL migrations and sqlc queries.
- `apps/donor-api/api`: the OpenAPI contract for web and native clients, checked by tests.
- `apps/web`: public website, donor account, staff interface, browser and unit tests.
- `deploy/nginx`: the production ingress configuration.
- `docs`: product rules, architecture, operations, and security guidance.

[Contributing](CONTRIBUTING.md) · [Architecture](docs/architecture.md) · [MIT license](LICENSE)
