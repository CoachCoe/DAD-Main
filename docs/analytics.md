# R1 analytics and event contract

This contract separates verified financial facts from browser observations. Reports must
label sandbox data and must never combine it with live donor acquisition. All timestamps
are UTC; receipt tax years use America/New_York. Amounts are integer USD cents. The launch
cutoff is 22 April 2027 at midnight in America/New_York. Reporting periods are half-open
intervals `[start, end)` and must include their observation cutoff and timezone.

## Durable server facts available now

The audited staff CSV export provides these datasets. Access requires `data.export`.
Use stable record IDs for deduplication, not email addresses, names, or webhook count.

| Logical event | Source / identity | Meaning |
|---|---|---|
| `referral_touch_captured.v1` | referral-touches / touch ID | First accepted referral touch; channel and 30-day window are fixed. Further opens in that window do not replace the source. This is not every page view. |
| `checkout_requested.v1` | checkouts / checkout ID | Valid donation request persisted before calling Stripe. Does not prove a hosted page opened or a gift completed. |
| `gift_collected.v1` | gifts / gift ID | Verified processor collection. One-time and recurring are explicit; checkout completion is insufficient. Carries the referrer and LINK/IRL kind valid at the moment of charge. |
| `refund_observed.v1` | refund-history / observation ID | Provider or audited staff observation of a refund transition. Count successful current refund state once per refund ID when calculating net dollars. |
| `billing_period_observed.v1` | billing-periods / period ID | Current billing outcome and due date. Current FAILED state alone is not a permanent history of all failures. |
| `account_claimed.v1` | donors / donor ID, claimed_at | First account claim, separate from first gift. Giving before claim is normal. |
| `referral_recognized.v1` | referrals / referred donor ID | Direct-only referrer/channel and realized net giving from projections. Check freshness before using totals. |
| `recognition_computed.v1` | streaks / donor ID, computed_at | Current and best streak, risk/grace, and permanent founding award. Unknown is not zero. |
| `notification_completed.v1` | messages / message ID | Worker completed the intent; inspect suppression and error fields. Does not prove inbox delivery, opening, or reading. |
| `identity_changed.v1` | identity-events / event ID | Requested, current-address verified, or completed email change. Donor identity remains stable. |

These are event definitions over operational records, not a second event store. Checkout
exports omit visitor hashes, tokens, processor secrets, and payment details. Referral-touch
exports carry `kind` (LINK/IRL) on every row. Email addresses are unnecessary for analytics
joins and must not be copied to an analytics vendor. Deduplicate gifts before joining
refunds or referrals to avoid multiplying collected dollars.

## Metric definitions

| Metric | Definition and caveats |
|---|---|
| Founding donors | Distinct donor IDs whose first successful gift completed before the launch cutoff. A later refund or cancellation does not remove the permanent badge. Account claim is not required. |
| First-gift conversion by source | Distinct eligible landing journeys with a verified first gift divided by eligible landing journeys, separately DIRECT/LINK/IRL. Attribution uses the frozen checkout source; browser campaign parameters cannot override it. Do not substitute checkout requests for landing journeys. |
| Seven-day share rate | Donors with a confirmed native-share handoff or clipboard copy within seven days of first gift divided by donors whose seven-day observation window has elapsed. Label this a share-action proxy, not verified invitation delivery. Cancellation of a share sheet is not a share. |
| IRL conversion share | IRL first-gift conversions divided by LINK plus IRL first-gift conversions in the same cohort and observation window. Direct gifts are excluded from this denominator. |
| Viral coefficient | Converting direct referrals credited to a donor cohort divided by donors in that cohort, with acquisition dates and observation window stated. No second-level credit. Publish gross first conversions separately from currently qualified referrals after full refunds. |
| Gift mix | Successful gift count and collected dollars grouped by MONTHLY/ONE_TIME. Also report first-gift donor mix; do not confuse gifts with unique donors. |
| Month-three/six retention | First-recurring-gift cohort with successful consecutive recurring periods at milestone / cohort old enough to reach that milestone. Grace, pauses, late recovery, and full refunds follow the domain rules. Current streak snapshots alone cannot reconstruct historical retention. |
| Involuntary churn/recovery | Scheduled recurring value lost to payment failure after the retry window, with later recovery count/value against that failed-period cohort. Voluntary cancellation and intentional pauses are separate. Requires historical failure/recovery events, not only current period state. |
| Time to first gift | Median elapsed time from an eligible journey's first landing to its verified first collection; report sample size and missing-journey rate. A receipt page view is not collection time. |
| Referral dollars | Actual collected referred gifts less successful refunds, direct-only, with LINK/IRL split. Never promised recurring value or projections of future gifts. |

Self-referral exclusion, first-touch conflicts, refunds, and streak rules are governed by
[the donor domain](donor-domain.md). Retain both gross first-conversion and currently
qualified-referral measures so refund behavior cannot silently rewrite acquisition reports.
Cohort identity is a donor ID, so a verified email change must not create another donor.

## Browser event contract and collection boundary

Anonymous behavioral collection is not enabled. Approved privacy/consent text, retention,
and an analytics destination have not been provided. The referral cookie serves the
explicit referral function; it is not permission to create a general tracking identifier.
The following schema is the implementation contract once that product/privacy decision is
approved, rather than an implied live collection policy:

- Envelope: schema version, random event UUID, server receipt timestamp, bounded client
  timestamp, consent version, short-lived random journey ID, environment, event name.
- Allowlisted events: `landing_viewed.v1`, `amount_selected.v1`, `checkout_opened.v1`,
  `share_requested.v1`, `share_handed_off.v1`, `invite_copied.v1`, `code_displayed.v1`.
- Allowlisted properties: surface enum, DIRECT/LINK/IRL source, MONTHLY/ONE_TIME frequency,
  amount in cents where relevant, share method enum. No arbitrary property objects.
- Never send names, email, full URLs/query strings, referral codes, login fragments,
  cookies, IP addresses, user-agent fingerprints, free text, or raw Stripe objects.
- Only the authenticated server may join an approved journey to a checkout. Financial
  completion/refund events come from committed server facts, never client claims.
- Drop duplicate event UUIDs; reject unknown names/properties, oversized batches, invalid
  timestamps, and unapproved consent versions. Rate limit ingestion independently from
  financial mutations. Failed analytics must never block giving, sharing, or account access.
- Respect opt-out and applicable browser privacy signals. Do not persist/retry an event
  after consent is withdrawn. Retention and deletion periods require an approved policy.

Acceptance must include both referral channels, direct guest giving, claim after gift,
expired/conflicting touches, retry/replay, share cancellation, full/partial refunds,
failed-and-recovered renewal, and email change. Reconcile acquisition counts and realized
dollars against the financial exports before trusting campaign reports. Missing browser
instrumentation and failure-history collection remain explicit release gaps; their
metrics must be marked unavailable, never displayed as fabricated zeros.
