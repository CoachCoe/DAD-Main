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
`donor_checkouts` are not gifts: there is no paid status or public charge endpoint.
Claiming an account proves email control but does not establish Founding Donor status.
The module neither depends on blockchain RPC nor grants staff permissions.

Call services inside a caller-owned database transaction. Deliver login secrets only
through a trusted email adapter; never return or log them from an anonymous HTTP route.
The future HTTP layer must provide uniform login-request responses, abuse controls,
same-origin protection, secure cookies, and a confirmation step that prevents email
link scanners from consuming credentials. It must not expose these services before
those controls and delivery exist.

The Stripe integration must persist verified events and individual gifts, enforce the
attribution deadline at payment time, establish the permanent first-gift relationship,
and apply it to subsequent gifts. Do not use checkout drafts or aggregated transparency
funding as proof of collection. Payment idempotency, subscription enforcement, refunds,
streaks, badges, staff MFA, and outbound messaging are outside this initial service slice.
