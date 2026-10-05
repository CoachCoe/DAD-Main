# Donor domain

This repository contains the R1 donor product. Donor identity and staff identity are
separate. Recipient, partner-portal, aid-delivery, AI, and blockchain functionality is
outside this release. The repository remains MIT licensed.

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

## Implementation and release boundaries

Go implements the donor API, Stripe inbox, recurring controls, projections, account
email, notification outbox, staff MFA and permissions, audited refunds/cancellation,
exports, private source documents, spreadsheet publication, and partner inquiries.
TypeScript implements the public, donor, and staff web surfaces. See [architecture](architecture.md)
and [operations](operations.md) for runtime behavior and instructions.

The receiving entity remains unverified and the application accepts test payments only.
Gift records and summaries are explicitly sandbox records, not approved tax receipts.
Terms, Privacy, and donor consent need approved content. [Wallet issuance](wallet.md) is
implemented behind optional issuer configuration; real-provider/device acceptance remains
open. [Analytics](analytics.md) defines available financial facts and the pending browser
collection contract. App design deliverables and operational release acceptance remain
separate from the backend rewrite. The web referral-code page
offers full screen, keeps the display awake while shown, and gives manual brightness
instructions; it cannot promise hardware brightness control. Email changes require single-use confirmations at both the current
and proposed address, each expiring after 15 minutes. Completing a change preserves the
donor ID, revokes existing sessions and sign-in links, and records identity audit events.
An address already attached to another donor is rejected after verification; accounts
are never merged automatically. Historical checkout and processor billing records retain
their original details.

The inactive enrolment queue and header-only export exist at R1. There is no enrolment
trigger until R2; the [prepared confirmation wording](messaging.md) does not promise funds.
