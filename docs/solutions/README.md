# Solutions — institutional knowledge

The "compound" step of our engineering loop (plan → work → review → **compound**).
When a non-obvious problem is solved — a subtle bug, a wrong assumption
corrected, a constraint discovered — capture it here so the next person (or
agent) finds the answer instead of re-discovering it. Each solved problem that
cost real time should become a short, searchable doc.

Tracked in git on purpose: this is team knowledge, unlike the local working
notes under `docs/requirements/`.

## When to write one

Write a solution doc when any of these is true:
- A bug took real effort to diagnose, or its cause was surprising.
- You discovered a constraint or invariant that is not obvious from the code.
- You tried an approach that *looked* right but was wrong, and the reason
  matters for the next attempt.
- You made a judgment call a future contributor would otherwise have to re-make.

Don't write one for routine, self-evident changes.

## Format

Each file is `NNN-short-slug.md` with YAML frontmatter for retrieval:

```markdown
---
title: Short descriptive title
date: 2026-10-15
tags: [stripe, webhooks, reconciliation]
area: donor-api/payments
status: resolved        # resolved | known-constraint | wontfix
---

## Problem
What went wrong or what was unclear, concretely.

## Root cause / finding
Why — the underlying reason, with file:line where useful.

## Resolution
What we did, or the rule to follow.

## Prevention
Would the system catch this next time? If a rule belongs in an AGENTS.md or a
test, note it (and add it there).
```

## Keeping it useful

- Prefer linking: if a rule belongs in `apps/donor-api/AGENTS.md` or a test,
  put it there and let the solution doc explain the *why*.
- Keep each doc skimmable. The frontmatter is the index; the body is the story.
- Update `status` to `known-constraint` for things that are not bugs but
  permanent realities (e.g. a provider limitation).
