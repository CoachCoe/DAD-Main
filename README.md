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
pages and empty states work; giving, account email, and staff operations are unavailable.
The frontend proxies `/api/donor/*` to `DONOR_API_BASE_URL`; the API listens on 8002.
Do not put backend secrets in `NEXT_PUBLIC_*` variables.

Use three independently generated encryption keys for email, staff MFA, and content:

```sh
openssl rand -base64 32 | tr '+/' '-_'
```

After setting `DONOR_STAFF_ENCRYPTION_KEY`, provision the first named administrator:

```sh
cd apps/donor-api
go run ./cmd/staff --email named.person@example.org --name 'Named Person'
```

The command prints a private, one-use invitation. Protect that output. It refuses if
any staff account already exists. Later invitations and recovery use `/staff` and require
administrator permission. Provision two individual administrators; do not share MFA.

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
  postgres:16-alpine
# Wait for: docker exec impactgraph-go-test-postgres pg_isready -U go_test -d donor_go
export TEST_DATABASE_URL=postgresql://go_test:go_test@localhost:55440/donor_go
(cd apps/donor-api && DONOR_GO_DATABASE_URL="$TEST_DATABASE_URL" go run ./cmd/migrate)
make test
make lint
(cd apps/web && npx playwright install chromium)
make test-browser-e2e
```

Browser tests run against `next dev` by default; set `E2E_PRODUCTION=1` to run them
against a production build (`next build && next start`) with its stricter CSP. Next allows
one dev server per checkout, so stop any running `make web` first.

`make test` checks Go formatting, vet, race-enabled tests, compilation, frontend lint,
unit tests, generated route types, TypeScript, and the production build. Browser tests
start a local Go API and Next server. Use a synthetic application database and no live
SMTP settings for those tests. `make lint` runs staticcheck, govulncheck, `npm audit`, and
shellcheck when installed. CI runs all of these with PostgreSQL and Chromium, and also
validates the production Compose file and builds both images.

SQL lives in `apps/donor-api/db`. `make generate` runs pinned sqlc; commit SQL and its
generated Go query code together. `make check-generated` fails if they have drifted.
Applied migrations are immutable and checksum-checked.

Exercise a running synthetic deployment with read-only burst and soak tests. `-url`
takes a comma-separated mix of endpoints requested round-robin, such as the public
content and a referral landing lookup:

```sh
cd apps/donor-api
API=http://localhost:8002
go run ./cmd/loadtest -url "$API/donor/public/content,$API/donor/referrals/CODE" -duration 30s -concurrency 128 -max-p99 250ms
go run ./cmd/loadtest -url "$API/donor/public/content" -duration 5m -concurrency 16 -max-p99 100ms
```

The runner reports request counts, failures, throughput, and latency percentiles. It
fails on non-200 responses (a missing referral code returns 404), timeouts, oversized
bodies, or a p99 above `-max-p99`. For authenticated reads, set `DONOR_LOAD_COOKIE` to a
synthetic donor's session cookie and target endpoints such as `/donor/me/dashboard`.
Public empty-state results do not establish capacity for 50,000 donors: repeat against
representative data, authenticated dashboards, and the intended deployment before launch.

## Containers and deployment

See [operations](docs/operations.md). Docker images run as non-root users; Compose binds
web and API ports to loopback for a separately managed HTTPS ingress. No command
enables workers or deletes database volumes implicitly. Test-only readiness is not approval to solicit live donations.

## Layout

- `apps/donor-api/cmd`: API, workers, migrations, administrator bootstrap.
- `apps/donor-api/internal`: HTTP, domain rules, persistence, Stripe, email, credentials.
- `apps/donor-api/db`: versioned SQL migrations and sqlc queries.
- `apps/web`: public website, donor account, staff interface, browser and unit tests.
- `docs`: product rules, architecture, operations, and security guidance.

[Contributing](CONTRIBUTING.md) · [Architecture](docs/architecture.md) · [MIT license](LICENSE)
