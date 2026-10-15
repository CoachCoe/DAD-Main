---
title: Two recurring test pitfalls — writing financial facts under the wrong DB role, and cross-worktree DB collisions
date: 2026-10-14
tags: [testing, dbroles, postgres, worktree]
area: donor-api
status: resolved
---

## Problem

Two distinct test failures cost real time this cycle, both environmental rather
than logic bugs:

1. A new test set up a refund via the fixture while acting as the
   projections-worker role and failed with `permission denied for table
   donor_gifts` / `donor_stripe_events`.
2. `go run ./cmd/migrate` failed with "database has unknown migration" when a
   second feature worktree ran its tests against the same shared Postgres a
   different worktree had already migrated to a different set.

## Root cause / finding

1. Per-process least privilege is enforced in tests: the projections role holds
   only SELECT on financial tables; the fixture's gift/refund writes
   (`recordGift`/`recordRefund`, plus a `donor_stripe_events` insert) require
   the owner or the stripe-worker role. The `donor_stripe_events` INSERT grant
   belongs to the API role, and gift/refund writes to the stripe-worker role —
   so facts spanning both tables are cleanest written as the **owner** via
   `RESET ROLE`, which is how the existing fixtures write them before switching
   role with `actAs`.
2. Worktrees share one Postgres instance. The migrate tool refuses a DB that
   holds a migration the current code doesn't know (and vice versa), so two
   branches with different migration sets collide on a shared `donor_go`.

## Resolution

1. Write setup facts under the owner (`RESET ROLE`, as the fixtures do) or the
   stripe-worker role, then `actAs(ProjectionWorker)` for the production-path
   recompute.
2. Give each worktree its own database:
   `CREATE DATABASE donor_go_<name>`, migrate it, point `TEST_DATABASE_URL` at
   it; drop it when done.

## Prevention

Both are now rules in `apps/donor-api/AGENTS.md` (Testing): write financial-fact
test setup under the owner/stripe-worker role, and use an isolated DB per
worktree.
