# Architecture

The R1 application is a Next.js TypeScript web client backed by a Go HTTP service and
PostgreSQL 16. Hosted Stripe Checkout handles USD test payments and card setup. Separate
Go workers reconcile signed Stripe events, refresh donor projections, and deliver email.

```text
Browser → Next.js /api/donor/* → Go HTTP API → PostgreSQL
             ↓                     ↓               ↑
       Stripe-hosted UI        Stripe API      Go workers
                                                ↓
                                          SMTP with STARTTLS
```

The browser uses the same origin for API requests. The Go service owns authorization,
validation, CSRF checks, and business rules. The Next proxy cannot grant permissions.
Donor and staff cookies identify distinct server-side sessions. Staff require individual
passwords and MFA; roles map to named permissions checked against current database state.

## HTTP contract and native clients

`apps/donor-api/api/openapi.json` (OpenAPI 3.1) is the HTTP contract for the web app and
the R2 donor apps. Every route is declared once in `internal/httpapi/routes.go` with its
authentication and client policy; the `TestContract*` tests fail when routes, security,
client policy, or JSON shapes drift from the document. Errors are `{detail}` JSON, with
an additive `code` for configuration states and `errors` for located spreadsheet
problems. Money is integer cents and timestamps are RFC 3339.

The R2 app calls the API host directly with a bearer session instead of cookies (see
[security](security.md)). Only donor account routes admit it; giving stays on the web.
Every donor-facing URL is built in `internal/store/links.go`, which is the starting list
for app/universal links (`/account/claim`, `/account/email-change`,
`/account/payment-method`, `/r/{code}`). Passkeys and push-notification device
registration are not built yet and need new store work.

## Financial consistency

Guest checkouts capture first-touch attribution before accounts exist. Durable commands
are committed before external mutations and carry frozen parameters and stable processor
idempotency keys. An ambiguous provider response is reconciled before another mutation;
old unknown commands require operator review rather than a new blind request.

The webhook verifies the raw request signature and persists allowlisted fields before
acknowledging. Workers fetch current provider state before taking the donor lock, then
lock and apply it, fetching again only if another writer recorded newer state meanwhile;
this reconciles out-of-order events and keeps ordinary provider reads outside the lock.
Donor and staff commands work in three steps: under the donor lock they validate and
record the command and take a per-subscription lease, the processor is called with no
lock held, and the result is applied under the lock again. The lease allows one
outstanding change per subscription, including identical retries. After a crash, a retry
can reconcile an already-applied result immediately; another processor mutation must
wait for lease expiry. Checkout completion is not collection. Gifts, billing periods,
refund state, append-only observations, projections, and notification intents commit
together. Money is integer USD cents. Referral credit is one level only.

Domain calculations in `internal/recognition` are independent of HTTP and Stripe. Queries in
`db` generate the typed store layer with sqlc. Donor reads use indexed projections;
financial replay runs in event/projection workers. Projection expiry makes incomplete
streaks explicitly unavailable instead of showing invented zeroes.

SMTP is at least once. Login payloads are encrypted; financial message bodies are stored
in the access-controlled database. Stable Message-IDs survive retries;
provider acknowledgement loss can still cause duplicate mail. Optional messages check
current consent before delivery. Logs avoid payloads and donor identifiers.

## Public content

Staff publish a pair of spreadsheet CSV snapshots. Source documents are private,
encrypted, and integrity-checked; public data contains no source bytes or private donor
information. Ledger values carry an as-of date and visibly mark illustrative figures.
Publishing is manual; there is no CMS or live outcome pipeline.

## Database lifecycle

Embedded SQL migrations are applied under a transaction advisory lock and recorded with
SHA-256 checksums. The API and workers require the complete known migration history.
`migrate --adopt-existing` supports a database at the former `0030_donor_public_content`
revision only after comparing the donor schema to the embedded baseline. It preserves all
existing records and leaves unrelated legacy tables untouched. Back up and restore-test
before adoption; removal of obsolete deployed data is a separate operational decision.

## Boundaries

Only the donor product, staff operations, marketing content, and partner contact form run
at R1. The recipient queue/export is inactive. There are no AI, evidence-processing,
blockchain, recipient eligibility, aid-payment, or partner-portal services.

This build is test-mode only. Readiness confirms a usable, migrated database; it does not
certify legal approval, Wallet integration, delivery operations, or production capacity.
