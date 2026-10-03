# Donor domain

The donor product and ImpactGraph transparency capabilities share this repository.
Consumer identity is independent of the organization-bound operator, verifier, and
administrator accounts. The repository remains MIT licensed.

The following R1 rules were agreed with the product owner. They are the implementation
contract, not a claim that every capability is already shipped.

## Giving and identity

Stripe processes USD donations through hosted checkout. Monthly giving is preselected:
$30, $90, or $150, or a custom amount of at least $1 in whole cents. Daily copy says
"about" and the monthly charge has equal visual weight. One-time presets use the same
amounts without daily framing. DAD absorbs fees; there is no cover-fees prompt.

Giving requires an email, not an account. Matching is case-insensitive and does not
remove dots or plus tags. Different addresses are separate identities until an explicit
merge verifies both. Never infer identity from a shared card, device, or postal address.
The initial implementation accepts ASCII mailbox names and IDNA domains.

Email claim and sign-in links last 15 minutes and are single-use. Sessions last at most
30 days and can be revoked. Tokens are stored hashed. Changing email requires fresh
authentication through the existing address and verification of the new address.
Knowing an email or checkout ID never grants access to giving history.

One active monthly subscription per donor; additional one-time gifts are allowed.
Amount changes apply at the next renewal without proration. Cancellation stops future
renewals without refunding past gifts. Pausing skips 1, 2, or 3 renewals, with the exact
automatic-resume date shown before confirmation.

## Referrals

The first valid referral touch wins for 30 days, for both referrer and channel (LINK or
IRL). Later scans, links, and direct visits do not overwrite it or extend its deadline.
An expired window can be replaced by a new referral touch. Checkout stores attribution
before account creation. Conversion requires a successful first gift within the window;
starting checkout is not conversion. Once converted, the donor's direct referral
relationship is permanent. Later gifts add dollars, never additional conversions.

Self-referral credit is removed when giver and referrer match by account or normalized
donor email; the gift itself remains allowed. There is no second-level credit.

Sharing requires a claimed account and an explicitly chosen public first name/nickname.
Do not substitute a billing name or email. Codes are permanent by default and may be
replaced, invalidating the old code for new visits. Existing captured attribution remains
a record of the original valid visit. Screenshots carrying the IRL marker still classify
as IRL: this indicates entry method, not verified physical presence. There is no print
or image-download feature. Link and IRL counts appear equally; no combined count in R1.

## Refunds and recognition

Attributed dollars are collected donations after successful refunds and before processing
fees. Pending refunds are separate; reconcile a later refund failure. Retain original
gifts and record adjustments instead of deleting financial history.

A referral counts while the referred donor has any unrefunded successful gift amount.
Fully refunding all gifts removes the count. A later gift restores the same relationship,
not a second conversion. Historical conversion events remain distinguishable from the
current count. Refund and subscription cancellation are separate staff actions.

A streak month is a successful monthly recurring billing period, starting at one.
One-time gifts do not count. A failed renewal gets 14 days from its scheduled date:
retain the current count as at risk, increment once when recovered within grace, and
reset the active streak to zero after grace. Later recovery starts at one. Retain the
historical best. An intentional pause/cancellation ends continuity at the first skipped
renewal, not immediately. Amount changes do not break continuity. Partial refunds retain
the period if recurring funds remain; full refunds disqualify it unless another qualifying
recurring payment covers it. Recalculate subsequent continuity rather than erasing every
later month. Staff corrections are for documented system errors and must be audited.

Any successful first gift before 2027-04-22 00:00 America/New_York earns Founding Donor
status, including one-time gifts. Use the processor's successful-payment timestamp,
not checkout start or webhook arrival. Claiming after cutoff is allowed. The badge is
permanent, including after refunds/cancellation. Changing the cutoff requires a product
decision; existing awards remain permanent.

## Staff and messages

Support can search/view donors and resend receipts/claim emails. Finance adds refunds,
cancellations, and donor/financial exports. Administrator adds staff access management
and approved content publishing. Require individual staff accounts and MFA. Enforce
named permissions server-side; audit actor, time, target, and result, with reasons for
refunds and staff cancellation. No donor impersonation or access to card details.

Send receipts per successful gift and a prior-year summary showing gifts/refund
adjustments. Send failed-payment recovery at failure, around day 7, and before the
14-day deadline; stop on resolution or cancellation. Optional referral-converted emails
identify the channel without the referred donor's name or amount. Send the initial
account-claim email with replacement-link access, without promotional reminders.
Marketing consent is separate. Receipts/security/essential payment notices are distinct
from optional messages, subject to approved legal classification. Prepare the enrolment
template at R1 and activate it with R2 enrolment. Finance approves summary treatment;
legal approves receipt wording.

The receiving entity's US nonprofit status, EIN, and existing Stripe account remain
unconfirmed. They must be verified before live donations.

## Current service boundary

`impactgraph.donors` implements guest identity, hashed login/session credentials,
explicit sharing activation, first-touch capture, and attributed checkout drafts.
`donor_checkouts` are drafts, not gifts. The sandbox-only donor API creates hosted Stripe
checkout sessions; verified Stripe payment events produce individual `donor_gifts`.
Claiming an account proves email control but does not establish Founding Donor status.
The module neither depends on blockchain RPC nor grants staff permissions.

Call services inside a caller-owned database transaction. Deliver login secrets only
through a trusted email adapter; never return or log them from an anonymous HTTP route.
The future HTTP layer must provide uniform login-request responses, abuse controls,
same-origin protection, secure cookies, and a confirmation step that prevents email
link scanners from consuming credentials. It must not expose these services before
those controls and delivery exist.

The Stripe integration persists verified events and individual gifts. Checkout and event
deduplication, one monthly checkout/subscription reservation per donor, and invoice
renewals are covered. The next domain projection must enforce the attribution deadline
at successful payment time, establish the permanent first-gift relationship, and apply
it to subsequent gifts. Until then, gifts link to the original candidate attribution
on the checkout; those fields are not awarded referral credit. Do not use checkout
drafts or aggregated transparency funding as proof of collection. Refund processing,
subscription editing, streaks, badges, staff MFA, and outbound messaging remain unshipped.

## Running the Stripe sandbox

Install the API dependencies and run migrations through `0017_stripe_checkout`. Set
`STRIPE_SECRET_KEY` to a Stripe test key and `STRIPE_WEBHOOK_SECRET` to the signing
secret of the sandbox endpoint. No secrets belong in git. Live keys/events and Connect
events are refused. The receiving organization and account still require verification.

Run from `apps/api` in separate terminals:

```sh
.venv/bin/uvicorn impactgraph.donor_api:app --port 8001
.venv/bin/python -m impactgraph.stripe_payments
```

Start the existing web app and visit `/donate`. `DONOR_PUBLIC_ORIGIN` must match the
browser origin exactly (default `http://localhost:3000`); Next uses
`DONOR_API_BASE_URL` (default `http://localhost:8001`) for `/api/donor/*`. Card entry
happens on Stripe. This first sandbox surface supports USD card payments from $1 to
$999,999.99; that technical upper bound is not an approved organizational gift-limit
policy. The donor API uses the same database but its health check does not depend on
evidence storage, AI credentials, or blockchain RPC. It is not yet wired into production
Compose or a production ingress.

Configure the sandbox webhook at `/donor/stripe/webhook` directly, or at
`/api/donor/stripe/webhook` through Next. Use snapshot events at API version
`2026-09-30.endive`, matching the pinned Stripe Python SDK 16.0.0. Subscribe to:

- `payment_intent.succeeded` for one-time gifts.
- `invoice.payment_succeeded` for initial and renewed monthly gifts.
- `checkout.session.completed` and `checkout.session.expired` for checkout lifecycle.
- `customer.subscription.deleted` to release the monthly reservation.

The receiver verifies signatures over the raw body, rejects stale signatures and live
events, stores only allowlisted fields, and acknowledges only after commit. The worker
handles duplicate event IDs and duplicate underlying payments. Recurring invoice events
can arrive before checkout completion. Checkout completion alone never records a gift.
Unrelated account events without DAD checkout metadata are ignored.

Monthly invoice credits, manual/offline payment, prorations, changed amounts, and currency
mismatches are not accepted as ordinary gifts. They stay pending for reconciliation.
This is intentionally narrower than all possible Stripe Billing behavior; extend the
adapter and tests before enabling subscription edits or alternative payment methods.

After ten failed processing attempts, an inbox event stays unprocessed with its error
class. Inspect `donor_stripe_events` and reconcile the cause before resetting its budget:

```sh
.venv/bin/python -m impactgraph.stripe_payments --retry-event evt_example --once
```

`--once` processes the oldest eligible event, which may not be the reset event. Keep the
worker running to drain the queue. Event processing uses row locks and supports multiple
PostgreSQL workers; the lightweight SQLite test database is not a deployment target.

Checkout attempts are committed before the Stripe call and retry with frozen parameters
and the same idempotency key. Uncertain attempts older than 23 hours require operator
reconciliation rather than creating another remote session after Stripe's idempotency
retention window. A reserved monthly checkout is released only by confirmed expiry or
subscription cancellation, not by guessing whether a timeout meant creation failed.

The `/donate/thanks` page is a return destination, not proof of payment. No receipt email,
tax claim, or Founding Donor award is produced by this sandbox UI. Real account-claim
delivery/screens, financial adjustments, event monitoring and reconciliation UI,
distributed abuse controls, and the release acceptance gates are required before live
giving. Browser rate limiting here is process-local; configure trusted ingress limits
before deploying it beyond a controlled sandbox.
