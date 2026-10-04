# ImpactGraph — DAD donor release

The R1 donor application for Dynamic Aid Delivery: guest giving, direct Link and IRL
referrals, a donor dashboard, recurring-gift controls, and an audited staff back office.
The backend is Go, the web application is TypeScript/Next.js, and persistence is
PostgreSQL. Licensed under MIT.

**This build accepts Stripe test payments only.** Live donations, approved tax receipts,
Wallet passes, and release acceptance are not established by running it locally. See
[the product contract](docs/donor-domain.md) and [security](SECURITY.md).

R1 is a donor product. It does not deliver aid, assess recipients, run AI services, or
publish blockchain transactions. The enrolment export is an inactive R2 stub.

## Local setup

Requires Go 1.27.1, Node 22.13+ (or Node 24), npm, Docker Compose, and PostgreSQL 16.
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

Configure a Stripe **test** secret key and webhook signing secret. The receiver accepts
snapshot events at `2026-09-30.endive`, matching stripe-go v87. Card entry and card updates
are hosted by Stripe. Use `/api/donor/stripe/webhook` as the externally reachable route.
See [operations](docs/operations.md) for supported events and recovery behavior.

Workers start only with `DONOR_GO_WORKERS_ENABLED=true`. Run each kind in a separate
terminal with the environment loaded:

```sh
make worker KIND=stripe
make worker KIND=projections
make worker KIND=login-email
make worker KIND=messages
```

SMTP delivery requires STARTTLS and the configured encryption key. Workers send real
email to the configured addresses, even while gifts are test payments. Local tests use
injected transports. Never point synthetic test data at a real mail provider.

## Checks

Integration tests require a dedicated synthetic database named `donor_go`. They must
never run against the application database.

```sh
docker run -d --name impactgraph-go-test-postgres -p 127.0.0.1:55440:5432 \
  -e POSTGRES_USER=go_test -e POSTGRES_PASSWORD=go_test -e POSTGRES_DB=donor_go \
  postgres:16-alpine
# Wait for: docker exec impactgraph-go-test-postgres pg_isready -U go_test -d donor_go
export TEST_DATABASE_URL=postgresql://go_test:go_test@localhost:55440/donor_go
(cd apps/donor-api && DONOR_GO_DATABASE_URL="$TEST_DATABASE_URL" go run ./cmd/migrate)
make test
(cd apps/web && npx playwright install chromium)
make test-browser-e2e
```

`make test` checks Go formatting, vet, race-enabled tests, compilation, frontend lint,
unit tests, generated route types, TypeScript, and the production build. Browser tests
start a local Go API and Next server. Use a synthetic application database and no live
SMTP settings for those tests. CI runs the same checks with PostgreSQL and Chromium.

SQL lives in `apps/donor-api/db`. `make generate` runs pinned sqlc; commit SQL and its
generated Go query code together. Applied migrations are immutable and checksum-checked.

## Containers and deployment

See [operations](docs/operations.md). Docker images run as non-root users; Compose binds
web and API ports to loopback for a separately managed HTTPS ingress. No command deploys
contracts, seeds demonstration accounts, enables workers, or deletes database volumes
implicitly. Test-only readiness is not approval to solicit live donations.

## Layout

- `apps/donor-api/cmd`: API, workers, migrations, administrator bootstrap.
- `apps/donor-api/internal`: HTTP, domain rules, persistence, Stripe, email, credentials.
- `apps/donor-api/db`: versioned SQL migrations and sqlc queries.
- `apps/web`: public website, donor account, staff interface, browser and unit tests.
- `docs`: product rules, architecture, operations, and security guidance.

[Contributing](CONTRIBUTING.md) · [Architecture](docs/architecture.md) · [MIT license](LICENSE)
