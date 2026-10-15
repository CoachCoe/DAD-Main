---
title: Stripe webhooks can't carry the expanded charge, so the one-time "trust-payload" fast path is impossible
date: 2026-10-14
tags: [stripe, webhooks, reconciliation, performance, worker]
area: donor-api/payments
status: known-constraint
---

## Problem

While reducing Stripe fetches in the worker (scalability finding B3), the plan
included a fast path for `payment_intent.succeeded`: trust the event's own
expanded `latest_charge` to read the charge-succeeded time instead of calling
`RetrievePaymentIntent`. It was implemented and looked correct, but it never
fired — every event still fell through to the fetch.

## Root cause / finding

Two compounding reasons, both permanent:

1. **Stripe `expand` is an API-request parameter; webhooks cannot be expanded.**
   A `payment_intent.succeeded` webhook carries `latest_charge` as a bare id
   string, not the expanded object with `created`/`status`/`paid`.
2. **The webhook receiver strips to an allowlist.** `minimalObject`
   (`internal/payments/webhook.go`) keeps only the fields the worker verifies;
   `latest_charge` is dropped entirely. Verified empirically: in the worker's
   event data, `data.Absent("latest_charge")` is always true.

The charge-succeeded time feeds the Founding Donor cutoff, which lives on the
expanded charge — so it cannot come from the webhook payload.

## Resolution

Removed the trust-payload code as dead; kept the two parts of B3 that work (the
staff rate reservation and per-subscription read coalescing). The one-time
charge time still comes from `RetrievePaymentIntent`.

The only way to drop that fetch would be to trust the event's own `created`
timestamp as the charge-succeeded time (as the invoice path trusts
`status_transitions.paid_at`). That was **declined** on a product call: event-
created time differs from charge-succeeded time by seconds, and on the Founding
Donor midnight-NY cutoff a few seconds could flip badge eligibility — not worth
shifting a legally-sensitive value to save one read. If revisited, it needs
explicit product sign-off on the cutoff tolerance.

## Prevention

Rule added to `apps/donor-api/AGENTS.md` (Payments): webhooks are allowlist-
stripped and cannot be expanded; don't try to trust payload fields that live on
an expanded sub-object. Fuller context: `docs/requirements/scale-B3-findings.md`.
