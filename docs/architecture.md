# Architecture

The R1 application is a Next.js TypeScript web client backed by a Go HTTP service and
PostgreSQL 16. Hosted Stripe Checkout handles USD test payments and card setup. Separate
Go workers reconcile signed Stripe events, refresh donor projections, apply referral-touch
retention, deliver email, and send donor app notifications.

```text
Browser → Next.js /api/donor/* → Go HTTP API → PostgreSQL
             ↓                     ↓               ↑
       Stripe-hosted UI        Stripe API      Go workers
                                                ↓
                                SMTP with STARTTLS, APNs and FCM
```

The browser uses the same origin for API requests. In production it reaches Next.js
through Cloudflare and an nginx ingress ([edge](edge.md)). The Go service owns authorization,
validation, CSRF checks, and business rules. The Next proxy cannot grant permissions.
Donor and staff cookies identify distinct server-side sessions. The web server answers
two hosts: the public site on `DONOR_PUBLIC_ORIGIN` and the staff back office, alone, on
`DONOR_STAFF_ORIGIN`. Staff sign in with individual passkeys for the staff host; roles
map to named permissions checked against current database state.

## HTTP contract and native clients

`apps/donor-api/api/openapi.json` (OpenAPI 3.1) is the HTTP contract for the web app and
the R2 donor apps. Every route is declared once in `internal/httpapi/routes.go` with its
authentication and client policy; the `TestContract*` tests fail when routes, security,
client policy, or JSON shapes drift from the document, including which response fields
are always present. A response whose fields depend on its state documents each state as
a `oneOf` variant. Errors are `{detail}` JSON, with an additive `code` for configuration
states and `errors` for located spreadsheet problems. Money is integer cents and
timestamps are RFC 3339.

The web app's request and response types are generated from the contract into
`apps/web/lib/openapi.d.ts` by pinned openapi-typescript, and pages name them through
`Schema`, `RequestBody`, and `ResponseBody` in `lib/api.ts` rather than declaring their
own. `make generate` refreshes the file and `make check-generated` fails CI when it no
longer matches the contract. The R2 native apps generate their own clients from the same
document.

Staff routes admit mutations only from the staff origin; every other route admits only
the public origin. Passkey ceremonies take two calls each. `POST
/donor/staff/sign-in/options` returns a single-use `ceremony` ID and WebAuthn request
options; `POST /donor/staff/sign-in` takes that ID with the browser's credential JSON and
sets the staff cookie. Activation is the same pair under `/donor/staff/activate`, started
with the invitation token, and also signs the new staff member in. Options and
credentials use the WebAuthn JSON forms, with binary fields in base64url.

The R2 app calls the API host directly with a 15-minute bearer access token instead of
cookies, and renews it with a rotating refresh token at `POST /donor/refresh`, a route
that admits only the app (see [security](security.md)). Every sign-in, in a browser or
the app, is a donor session the donor can list and end through `/donor/me/sessions`,
which the web profile page shows. Only donor account routes admit the app; giving stays
on the web.
Every donor-facing URL is built in `internal/store/links.go`, which is the starting list
for app/universal links (`/account/claim`, `/account/email-change`,
`/account/payment-method`, `/r/{code}`). Donor passkeys are not built yet.

## App notifications

The app registers its installation's push token on its own session with `POST
/donor/me/push-device`; the token is a column of that session, so it lasts exactly as
long as the sign-in, and a token registered by another session moves to the new one.
Each projection refresh queues push intents in the same transaction as the facts
behind them, next to the email outbox: a referral conversion (the referral email's own
condition), a failed latest renewal while the streak is at risk (the decision behind the
recovery email's "your streak is at risk"), and a streak milestone. Stable keys make
each intent once: `push:referral:<referred donor>`, `push:streak-at-risk:<period>`,
and `push:streak-milestone:<donor>:<run start>:<months>`. Only donors with a live
registered device get intents. The push worker delivers each to every live device of the
donor through APNs or FCM (`internal/push`), rechecking preferences and facts first,
and records the sessions reached so a retry after a partial failure reaches only the
rest. Notifications carry generic copy, a category and an app path, and a collapse ID,
so a resend replaces the notification on the device instead of repeating it. `GET
/donor/me/conversions` lists recent conversions with their time and channel for the
app's conversion moment.

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

Reads never call Stripe. The Stripe worker syncs a subscription's current state, including
its billing anchor and whether Stripe holds a schedule or pending update, whenever an
event about it arrives: each `customer.subscription.*` event it accepts, a completed
checkout that binds it, and an invoice while an amount change is pending. When it has no
events to process, it also syncs subscriptions stored before the billing anchor was kept,
which offer no pause dates until then, checking for them hourly. Recurring views, card
update status, and staff previews are served from that state with `observed_at`, the time
of the sync. Of the API's requests only commands call Stripe, and each first syncs the
subscription it acts on, committed on its own so that a command refused as out of date
still leaves the next view current; none depends on a view having refreshed anything.

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
