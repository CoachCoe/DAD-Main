---
title: Denormalized dashboard totals must converge the backfill and the forced recompute, not double-count
date: 2026-10-13
tags: [projections, dashboard, migrations, recognition, performance]
area: donor-api/store
status: resolved
---

## Problem

Scalability fix B5 denormalized the four referral totals onto the referrer's
own `donor_metric_projections` row (so the dashboard reads one row instead of
scanning every referred row). Two forces update those columns: a migration that
backfills existing rows, and a `projectionRules` bump that forces every donor to
recompute. A naive "add my contribution" on recompute would double-count on top
of the backfill. A first implementation also silently dropped credit when the
referrer's own projection row did not exist yet at the moment a referred donor
was projected (the common guest-gives-before-referrer / referrer-claims-later
case) — the `UPDATE ... WHERE donor_id=referrer` hit 0 rows.

## Root cause / finding

- Incremental maintenance must be **new-minus-old delta**, computed from the
  referred donor's previously-stored contribution, so an unchanged donor's
  recompute applies a zero delta and backfill + rules-bump converge.
- A fire-and-forget incremental UPDATE is not enough on its own, because the
  target row may not exist at delta time and nothing guarantees a later retry.

## Resolution

Two mechanisms that converge for every ordering:
- **Fast path:** `applyReferralDelta` applies the signed new-minus-old delta to
  the referrer's row via atomic `col = col + delta` SQL (sibling-safe under
  per-donor locks).
- **Backstop:** the referrer's own `projectFacts` recompute re-derives the four
  columns from the live referred aggregate, so a dropped delta still converges.

Readiness (`ready`) stayed an exact bounded `bool_and` over referred rows — not
denormalized — to preserve the observable "show referrals only when all referred
projections are fresh" behavior. A correctness test asserts the denormalized
totals equal the old FILTER aggregate across orderings, including a full-refund
zero-crossing (count 1→0 while the minor sum follows `net_minor >= 0`).

## Prevention

Rule in `apps/donor-api/AGENTS.md` (Testing): a recognition/projection change
bumps `projectionRules`; when a migration also backfills, make backfill and
recompute converge via new-minus-old deltas. A test now pins identical-numbers
vs the old aggregate.
