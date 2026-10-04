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
30 days and can be revoked. Credentials are stored hashed; the email delivery queue temporarily holds an encrypted
copy of the login token until delivery or expiry. Changing email requires fresh
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
The donor HTTP layer provides uniform login-request responses, same-origin protection,
private cookies, basic rate limits, and a confirmation step that prevents ordinary email
link scanners from consuming credentials. Production ingress abuse controls and
controlled-mailbox delivery verification remain release gates.

The Stripe integration persists verified events and individual gifts. Checkout and event
deduplication, one monthly checkout/subscription reservation per donor, and invoice
renewals are covered. Stored donor projections select the successful first gift using
processor timestamps, enforce the attribution deadline, and apply the direct relationship
to subsequent net gifts. They also preserve sandbox recognition and historical bests.
The authenticated sandbox dashboard reads those projections. Do not use checkout drafts
or transparency funding as proof of collection. Issuing refunds, subscription edits and
schedule changes, staff MFA, and messaging beyond account access remain unshipped.

## Running the Stripe sandbox

Install the API dependencies and run migrations through `0020_donor_metrics`. Set
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
- `invoice.payment_failed` for failed renewal billing periods.
- `refund.created`, `refund.updated`, and `refund.failed` for refund reconciliation.
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
tax claim, or Founding Donor award is produced by this sandbox UI. Financial adjustments, event monitoring and reconciliation UI,
distributed abuse controls, and the release acceptance gates are required before live
giving. Browser rate limiting here is process-local; configure trusted ingress limits
before deploying it beyond a controlled sandbox.


## Donor account access and email

`/account/sign-in` requests an email link; `/account/claim` requires an explicit click
before redeeming it. The token travels in the URL fragment (not the server request URL)
and is removed from browser history on page initialization. Never add analytics that
collect fragments or request bodies on these routes. The API uses a separate HttpOnly,
SameSite=Lax donor cookie, Secure on HTTPS; it never returns credentials in JSON.
`/account/giving` shows only the authenticated donor's confirmed sandbox payments, in
pages of 50. Sign-out revokes the server session. Staff identity remains separate.

Set `DONOR_SMTP_HOST`, `DONOR_SMTP_PORT` (587 by default), `DONOR_EMAIL_FROM`, and
`DONOR_EMAIL_ENCRYPTION_KEY`; set username/password if required by your SMTP provider.
Use a dedicated Fernet key from `cryptography.fernet.Fernet.generate_key()` stored in
secret management. This is a vendor-neutral SMTP adapter, not a vendor selection.
TLS with certificate validation is mandatory. No email is sent by the API process.
Run the sender separately after migrating:

```bash
cd apps/api
.venv/bin/python -m impactgraph.donor_email
```

The first recorded successful gift queues a claim email in the same transaction as the
gift, unless the donor has already claimed their account. Repeat webhook delivery and
renewals do not send additional automatic claim emails. The public sign-in endpoint
returns the same accepted response for known and unknown emails, and limits each known
donor to one queued request per minute across API processes. Unknown addresses never
create accounts. Address rate limits still need a trusted ingress before public release.

The sender commits a hashed login credential plus an encrypted delivery token before
contacting SMTP. Retries reuse that token and Message-ID, at most five attempts with a
one-minute delay. Delivery is at least once: an ambiguous SMTP acknowledgement can
produce a duplicate email. Links expire 15 minutes after worker preparation, not queue
creation. Successful or expired jobs discard the encrypted token. Rows reaching five
failures remain parked for operator investigation (`last_error` stores only the exception
class). A donor can request a fresh link. Do not log tokens or SMTP message bodies;
rotate keys only after draining pending jobs. Monitor queue age, parked jobs, and worker
health; automated alerting and a staff retry interface are still required for release.

Email is disabled when settings are absent; the sign-in screen reports unavailability.
Tests use an injected mailbox and do not send external email. Verify delivery with a
controlled mailbox before inviting sandbox users. Account emails are functional access
messages, not donation receipts. Tax receipts, annual summaries, recovery emails, and
referral notifications require their own implementations and approved copy.


## Financial calculation boundary

`impactgraph.donor_metrics` is the side-effect-free R1 calculation layer. Its inputs
are successful `Gift` facts, the current verified state of each `Refund`, and explicit
monthly `BillingPeriod` facts, plus an aware `as_of` timestamp. The Stripe worker now persists billing periods and reconciled refunds;
`donor_financial_facts.load_history` translates owner-scoped records to these inputs.
The sandbox dashboard now reads durable projections of those facts. Legacy gifts may still lack
billing-period and payment-link information until reconciled.

- `relationships` selects the earliest successful payment per donor by processor
  timestamp, with stable payment ID breaking exact timestamp ties. The first gift can
  establish a direct arrival with no referrer; later gifts cannot create attribution.
  The referral expiry is exclusive, consistent with captured-touch expiry. Earlier
  facts arriving late correct derived history; webhook arrival order is never a rule.
- `referral_totals` applies that relationship to subsequent collected gifts, subtracts
  successful refunds, and produces separate Link/IRL counts and USD cents. It never
  follows the referrer's own relationship. Full refunds remove the current count but
  leave the relationship; later collection restores the same count.
- `founding_donor` tests successful payment against the Eastern-time launch cutoff,
  excluding exactly midnight on launch day. The adapter must persist an award and pass
  `previously_awarded` on subsequent calculation, including after cutoff changes.
- `streak` counts distinct billing periods. Renewal grace ends exactly 14 days after
  the scheduled date; a charge at that boundary starts a new streak. Intentional skips
  break continuity when due, not when requested. A fully refunded period is disqualified
  immediately, without failed-payment grace; another unrefunded recurring payment for
  the same period can qualify it. Subsequent periods are recalculated.

The adapter must preserve the monotonically increasing historical best and pass it as
`previous_best`. Reconstructing best-ever streaks by ignoring all refunds is incorrect:
a refund before a later renewal may have prevented that longer streak from ever being
achieved. Initial historical backfills must replay verified changes in chronological
order and retain the best at each step. Award and best persistence are still pending.

Duplicate identical facts are idempotent. Conflicting versions of the same ID, excess
successful refunds, unknown refund targets, overlapping periods, and missing current
renewal data are errors, not zero-valued fallbacks. Refund inputs are a state snapshot,
not a stream of updates: the ingestion adapter must resolve retries and out-of-order
status changes first. A snapshot containing a refund state later than `as_of` is rejected;
historical reporting must supply the state actually known at that time. Facts must use
USD integer cents and aware timestamps. Do not infer monthly billing dates from charge
dates, assume every missing invoice was a cancellation, or feed checkout drafts here.

Remaining integration requires intentional-skip/schedule facts. The calculation module
operates on supplied history; the dashboard reads indexed projections rather than loading
all donor financial history in each request.
Sandbox calculations must stay visibly labeled and cannot confer live donor recognition.


## Stripe billing and refund facts

Migration `0019_donor_financial_facts` adds invoice billing periods, payment links on
gifts, reconciled refund records, and append-only refund observations. The worker reads
periods from the invoice's subscription line, not the invoice creation or webhook date.
It accepts one complete, non-prorated subscription line and one complete paid test
PaymentIntent covering the gift. Truncated, split, offline, or inconsistent invoice
payments stay in the inbox for reconciliation. Failed invoices record their scheduled
period without creating a gift. A delayed failure cannot overwrite a paid period.

This mapping follows Stripe's [invoice line item](https://docs.stripe.com/api/invoice-line-item/object),
[invoice payment](https://docs.stripe.com/api/invoice-payment/object), and
[refund](https://docs.stripe.com/api/refunds/object) object definitions. The integration
still pins the SDK and snapshot event version described above.

Refund webhook payloads are reduced to ID, PaymentIntent, amount, currency, and status.
The worker links the PaymentIntent to a gift, locks that gift, then retrieves the current
refund through the test-key Stripe client. It validates identity, amount, and currency
before storing the state and an observation linked to the inbox event. The event's old
status never overrides the retrieved current state. Only successful refunds reduce
collection; pending, requires-action, canceled, or failed refunds do not. Excess refunds
roll back both the state update and observation. This path observes processor refunds;
it does not yet provide a staff action to issue one.

The receiving Stripe account may contain unrelated refunds. Unknown PaymentIntents,
refunds that arrive before their gifts, and provider failures remain unprocessed. The
existing ten-attempt inbox limit and explicit retry procedure apply. Inspect and classify
unrelated events instead of attaching them by email or guessing a checkout. Provider
lookups are read-only and have the existing bounded network timeout/retries. Operations
must monitor parked events; automatic dependency-aware retry/backoff is still pending.

The migration backfills one-time PaymentIntent IDs from existing gifts. Legacy recurring
gifts need a verified complete invoice redelivery to fill their period/payment links;
a new event ID is required because already-processed event IDs are deduplicated. Merely
replaying the old minimized inbox row cannot recover fields it never captured. Enrichment
checks ownership and amount, and never collects the gift twice. Financial facts block a
destructive downgrade. Do not delete old gifts or fabricate period dates to migrate.

`load_history` returns calculation inputs for one donor, including refund observation
times. This is a current-state view, not a tax-reporting history: observations record
when reconciliation happened, not a guessed processor status-transition timestamp.
Cancellation and fixed-length pause schedules are supplied by the adapters below. A missing current billing period
remains explicitly unavailable rather than an invented zero streak. No live giving or
live donor badges are enabled by these sandbox facts.


## Sandbox donor dashboard

Migration `0020_donor_metrics` adds `donor_metric_projections`. `/account/dashboard`
shows the five R1 elements: Link Referrals, IRL Referrals, net attributed dollars with a
channel breakdown, recurring streak, and simulated Founding Donor recognition. All are
visibly sandbox-only. Account claim leads here; My Giving remains a separate screen.
The authenticated `/donor/me/dashboard` endpoint derives its owner from the donor session,
accepts no donor selection, exposes no referred-donor identities, and is non-cacheable.
The same endpoint can serve the future mobile app.

A projection stores one donor's direct relationship and net collected dollars. The
read endpoint aggregates these indexed rows for the referrer, counting each referred
donor only while net collection is positive. It never recursively follows referrals.
An earlier successful payment delivered late corrects the derived relationship using
payment time; later gifts and refunds do not reassign it. Missing contributions during
backfill are unavailable, not zero. The financial event and affected donor's projection
commit in the same transaction. Workers lock the donor before payment/attempt rows to
serialize refreshes and preserve recognition.

Founding recognition is awarded once and retained through refreshes, refunds, and later
cutoff changes. Historical best streaks only increase. Backfill replays known payment
and refund-observation times to recover prior bests, preserving any existing best. Refund
observation times do not prove an earlier processor transition; reconciliation of missing
historical facts still requires operator review. These are sandbox recognition records;
a production launch must use production data, never treat test awards as real awards.

Start the projection worker alongside the Stripe and email workers:

```bash
cd apps/api
.venv/bin/python -m impactgraph.donor_projections
```

It backfills donors missing projections and refreshes due records. `--once` processes at
most one due donor; repeated invocation drains the backlog. Refreshes are scheduled at
known future payment/period boundaries, the grace deadline, or a daily safety interval.
Incomplete streaks are retried every five minutes. Financial events update immediately;
the separate worker handles time passing without an event. Run it continuously and
monitor worker health/backlog before inviting sandbox donors.

The dashboard suppresses expired streak values until refreshed. Missing billing periods
show “Updating,” while a verified zero remains zero. Pending projection creation, partial
data, failed connection, and logged-out states are distinct. Cancellation and pauses
still require verified schedule facts; they are not guessed from a missing invoice.
A downgrade with recognition rows is refused because those rows contain permanent awards
and historical bests. Preserve and migrate recognition explicitly rather than dropping
or truncating the projection table. Load/spike validation and production operations
remain release gates; the dashboard itself does no financial-history replay.

## Referral sharing and landing pages

`/account/share` lets a claimed donor choose the public first name/nickname before
sharing. The screen explains that anyone opening the link or scanning the code can see
that name. It never substitutes an email or billing name. The share action uses the
native share sheet when available and otherwise copies the invitation; editable
messaging and manual copying remain available in the user's chosen messaging app.
No message is sent automatically. The sharing screen contains no QR image.

`/account/code` displays an authenticated, non-cacheable SVG code with the same referral
URL plus `?via=irl`. The server generates a plain black-on-white QR with a four-module
quiet zone and high error correction using pinned `qrcode` 8.2. The page offers browser
full-screen mode and manual brightness instructions. No print/download action exists;
print CSS hides the code. Replacing a link requires an explicit confirmation in the UI
and also changes the code. Old codes stop accepting new visits; already-captured valid
first-touch attribution remains intact. QR generation stays server-side and does not
send donor information to a third-party image service.

`/r/[code]` saves the referral through a same-origin POST before enabling the normal
checkout link. The IRL query marker selects IRL; other entries use Link. An existing
valid first touch retains both its referrer and channel, including its original expiry.
The landing page names the saved referrer using only their public nickname. Thus opening
a different person's link does not display that person while silently crediting someone
else. Unavailable codes and failed capture have separate states and an explicit path to
continue giving even when capture cannot be confirmed. The existing opaque first-party cookie carries
the saved attribution into guest checkout and the payment record before account claim.

The API exposes authenticated GET/POST `/donor/me/sharing` and GET
`/donor/me/referral-code.svg`; mutations require the expected Origin and the existing
rate limits. Shared URLs use configured `DONOR_PUBLIC_ORIGIN`, never a supplied Host
header. The public capture response exposes only capture status and the public name,
not donor email, IDs, or financial details. Sharing and landing copy disclose $30/month
alongside approximate daily framing and label the flow as a sandbox.

Wallet pass issuance is not implemented; it requires Apple/Google account setup,
certificates, and pass integrations. This web slice does not claim automatic hardware
brightness control. Full-screen fallback, manual brightness instructions, and an
unobstructed code are implemented; reliable scanning under glare, low brightness, angles,
and damaged screens still requires physical-device acceptance tests. Analytics for share
attempts/completion and conversion funnels also remains a separate required integration.

## Recurring cancellation (sandbox)

`/account/recurring`, linked from My Giving, reads the donor's monthly subscription
from Stripe and offers an explicit cancellation confirmation with the billing boundary.
GET `/donor/me/recurring` and POST `/donor/me/recurring/cancel` require a claimed donor
session. The POST also requires the configured Origin, a UUID v4 Idempotency-Key, and
the displayed subscription attempt ID and period-end timestamp. Ownership is resolved
through the donor's checkout, never from a supplied customer ID or email.

Migration 0021 stores the latest verified schedule and durable cancellation commands.
Each command records the authenticated donor, subscription attempt, confirmed billing
boundary, request time, and completion time. Authorization commits before the external
mutation. Retries first retrieve Stripe's current state; a lost success response can be
reconciled without issuing another mutation. An unresolved command older than 23 hours
requires reconciliation instead of risking reuse after Stripe's idempotency retention.
A changed billing boundary requires a new confirmation. Stripe receives the fixed
confirmed `cancel_at` timestamp with `proration_behavior=none`, rather than a moving
"end of whichever period is current" instruction. No refund or immediate cancellation
is requested. Outstanding invoices are not forgiven by this action.

Subscribe the inbox to `customer.subscription.updated` as well as
`customer.subscription.deleted`. Both retrieve current Stripe state under the donor
lock, so delayed events cannot restore an obsolete cancellation schedule. The adapter
accepts the verified single-item monthly USD schedule and rejects unsupported changes
for reconciliation. Only terminal cancellation releases the monthly reservation;
scheduling a future cancellation does not allow a second active subscription.
The projection worker evaluates the verified stop boundary: a paid month retains its
streak until the first skipped renewal, then current streak becomes zero. Best streak,
gift history, referral money, and an earned Founding Donor badge remain intact.

Run both inbox and projection workers; schedule accuracy depends on consuming Stripe
updates, with account visits also reconciling current state. There is not yet a periodic
Stripe-wide reconciliation job. Pending commands are retained for explicit retry and
operational inspection; a staff reconciliation interface is still required. The migration
refuses to discard nonempty schedule/audit tables. Amount changes, staff cancellation permissions, and receipts remain
unfinished surfaces. Payment-method updates and fixed-length pauses are described below. This remains test-mode only and requires real Stripe sandbox acceptance before
production activation.

## Payment-method updates (sandbox)

`/account/payment-method` is the destination for payment recovery links and is linked
from My Giving, monthly-gift management, and the successful account-claim screen.
A signed-out visitor requests the normal email sign-in link; no customer identity or
saved card is exposed based on a supplied email. GET `/donor/me/payment-method` returns
only the authenticated donor's latest update status. POST
`/donor/me/payment-method/setup` requires the donor session, configured Origin, and a
UUID v4 Idempotency-Key. It resolves the Stripe customer through the donor's owned
subscription, not through browser input.

Migration 0022 stores durable setup attempts and frozen request parameters. Stripe
Checkout runs in `setup` mode, accepts cards only, and attaches the verified setup to
the subscription's customer. The donor enters card details entirely on Stripe. The
server stores customer/session IDs and attempt status, never card details, payment-method
details, or SetupIntent secrets. Setup URLs are restricted to HTTPS checkout.stripe.com.
They expire after one hour; uncertain creation cannot be reissued with a changed expiry.
An unresolved creation older than 30 minutes requires operator reconciliation.

The existing signed checkout.session.completed/expired inbox recognizes payment-update
metadata separately from donation checkouts. Processing retrieves both the Checkout
Session and SetupIntent, verifies ownership, test mode, successful off-session card setup,
and the current subscription, then sets only that subscription's default payment method
with a stable idempotency key and no proration. It re-reads the subscription before
marking the update APPLIED. A redirect, an open setup, or an incomplete authentication
challenge cannot confer success. This follows Stripe's
[hosted payment-details update flow](https://docs.stripe.com/payments/checkout/subscriptions/update-payment-details).

One unfinished setup per donor prevents competing devices from applying cards in event
arrival order. Terminal attempts are never reapplied, so an old webhook cannot overwrite
a newer card. Lost responses reconcile against Stripe; unresolved mutations older than
23 hours are parked for explicit reconciliation. Expired sessions can be replaced and a
subscription cancelled during setup is never restarted. The usual inbox retry command
handles failed jobs. A retried start can also reconcile a completed session without
requiring a new card entry. Returning to the page reads stored status; the refresh action
is available while the inbox catches up.

A card update creates no gift, forgives no outstanding invoice, and grants no streak
month. Failed-payment retry/recovery messages and invoice recovery orchestration remain
separate work, alongside amount changes. The existing production and
real-Stripe sandbox acceptance gates still apply; no live keys are enabled by this slice.

## Fixed-length pauses (sandbox)

`/account/pause`, linked from monthly-gift management, previews skipping 1, 2, or 3
renewals. GET `/donor/me/recurring/pause` retrieves the owned Stripe subscription and
returns the first skipped renewal and automatic-resume date. Dates preserve Stripe's
original UTC billing-cycle anchor, including January 31 → February 28/29 → March 31.
Only a single monthly subscription with no conflicting scheduled change is supported.
The API returns timestamps with explicit UTC offsets on both SQLite and PostgreSQL.

POST to the same path requires a claimed session, configured Origin, UUID v4 idempotency
key, owned subscription attempt, duration, and both displayed dates. Migration 0023
stores this immutable confirmation and its acknowledgement. A changed billing period,
conflicting cancellation, or overlapping pause cannot silently change the confirmation.
Requests commit before contacting Stripe, and retries reconcile an already-applied pause
rather than extending it. Unresolved commands older than 23 hours require reconciliation.
The screen can retry a saved pending command after a browser refresh.

Stripe receives `pause_collection` with `behavior=void` and the fixed `resumes_at`
timestamp, plus no proration. This skips future invoices rather than accumulating drafts
to charge on return. It does not forgive previously issued invoices or refund gifts.
Stripe automatically resumes collection at the confirmed date; no DAD timer is needed
for that mutation. See Stripe's [pause collection documentation](https://docs.stripe.com/billing/subscriptions/pause-payment).
Early resumption at the next renewal is described below. Extending an existing pause's
duration is not implemented. Pausing a gift with a failed current payment is also unavailable until
that payment is recovered; the existing payment-method flow remains available.

Subscription reads and updated/deleted webhooks recognize only a matching locally
authorized pause. An unexpected external pause or an early external unpause is held for
reconciliation. A confirmed pause supplies a bounded gap after the last verified billing
period; the dashboard worker changes the streak to zero at the first skipped renewal,
not at confirmation. This does not fabricate paid or skipped invoice records. At the
resume boundary, a missing renewal fact produces Updating rather than an invented
payment. A successfully collected resumed period restarts the streak at one; best streak,
referral money, and the Founding Donor badge remain intact. Cancellation during a pause
is supported and cannot make the dashboard promise automatic resumption afterwards.

Real Stripe sandbox acceptance must exercise invoice voiding and automatic resumption at
the exact billing boundary, especially with month-end anchors, before production use.
The automated adapter tests use a fake provider and do not replace this acceptance gate.
The migration refuses to discard nonempty pause/audit records. Staff reconciliation UI,
amount changes, receipts, and recovery messaging remain separate work.

## Early resumption at the next renewal (sandbox)

`/account/resume`, linked from My Pause, lets a donor shorten a confirmed pause to the
next regular renewal date. This action preserves the billing anchor and does not charge
immediately, pay/finalize old invoices, or collect skipped renewals. If the original
pause already ends at the next renewal, there is nothing to shorten. The donor confirms
the displayed date; amount changes remain separate.

GET `/donor/me/recurring/resume` resolves the owned subscription and latest pause and
returns a preview or the saved command. POST requires a claimed session, configured
Origin, UUID v4 Idempotency-Key, owned pause ID, and the previewed renewal date. A changed
renewal date, cancellation, or replacement pause cannot silently alter that confirmation.
Migration 0024 preserves the original pause and records its early-resumption command,
actor, requested date, and acknowledgement separately. One command per pause prevents
competing devices from shortening it differently. The screen can retry a saved pending
command after refresh. The migration refuses to discard nonempty audit records.

Stripe receives the existing `pause_collection` behavior `void` with a shorter, fixed
`resumes_at`, plus no proration. This retains the handling of already-skipped invoices.
The adapter re-reads Stripe and only acknowledges a matching authorized schedule. Lost
responses reconcile without extending or reissuing an already-applied change; unresolved
commands older than 23 hours require operator reconciliation. The original pause date
cannot overwrite a verified early-resumption date. Updated/deleted webhooks continue to
retrieve current state rather than trusting event arrival order.

Projections use the confirmed shortened pause window. If no renewal was skipped,
successful giving continues the existing streak. If a renewal was skipped, the next
successful recurring period starts at one. The resumption request itself creates no
gift or streak month, and an absent renewal fact remains Updating. Cancellation wins
over resumption and cannot trigger a restarted subscription.

The real Stripe sandbox acceptance gate includes early resumption before and after a
skipped renewal, month-end dates, cancellation, and invoice behavior at the exact resume
boundary. Test doubles exercise orchestration and calculations but do not establish
Stripe end-to-end acceptance. Immediate charging and retroactive collection are outside
this donor control. Amount changes, operational reconciliation, receipts and recovery
messaging remain unfinished.

## Monthly amount changes (sandbox)

`/account/amount` previews and confirms a new USD monthly amount at the next renewal.
GET and POST `/donor/me/recurring/amount` require an owned claimed session; POST also
requires the configured Origin, a UUID v4 idempotency key, the previous amount and the
exact previewed renewal date. Only integer cents from $1 through $999,999.99 are accepted.
Active pauses and conflicting schedules must finish before an amount change.

Migration 0025 retains each donor-authorized command and provider acknowledgement.
The original checkout and collected gifts remain immutable. Invoice validation uses
its billing-period start to select the applicable confirmed amount, so delayed original
invoices retain their original value. Multiple changes before one renewal use the last
confirmed amount. A subscription price outside that history requires reconciliation.

The adapter updates the existing Stripe subscription item with the same currency and
monthly interval, disables proration, and does not change the billing anchor. It retrieves
the subscription to confirm the result. Lost responses reconcile before another mutation;
unresolved commands older than 23 hours require operator reconciliation. Changes in the
final minute before renewal are refused; uncertain changes spanning renewal require
invoice reconciliation. Real Stripe sandbox testing at the billing boundary is required
before activation. Local adapter tests do not establish provider end-to-end acceptance.

## Profile and notification consent

`/account/profile` exposes the authenticated donor's account email, existing public
nickname, optional notification choices, and a confirmed sign-out on every device.
Email is read-only: changing a financial identity requires a separately verified workflow.
Public nickname changes continue through explicit sharing consent.

Referral-converted and marketing email choices default off and are independent of
receipts, access messages, and essential payment notices. POST `/donor/me/preferences`
requires strict booleans, a same-origin claimed session, and the current preference
version. Stale tabs receive 409 and must refresh, preventing an older tab from silently
reversing an opt-out. Migration 0026 stores current preferences and append-only versioned
consent events; the migration refuses to discard consent history. Saving consent alone
does not send any email. Optional delivery must recheck current consent at send time.
POST `/donor/logout-all` revokes every current session for the authenticated donor only.
