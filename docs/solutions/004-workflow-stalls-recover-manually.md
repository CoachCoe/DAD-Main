---
title: Background workflow steps stall; detect by file/commit/process signals and recover manually
date: 2026-10-14
tags: [process, workflow, agents, tooling]
area: process
status: known-constraint
---

## Problem

Across several multi-step changes this cycle, background workflow steps stalled
repeatedly — the coder step went quiet with the work substantially written but
uncommitted, no step transition, for 25–30+ minutes. A fully unattended loop
would have sat frozen until a max-iteration timeout and produced nothing usable.
The *work* was consistently sound; the stalls were infrastructure flakiness.

## Root cause / finding

Not diagnosed to a single cause (the workflow runner is external). The reliable
signal that a step has stalled rather than is "thinking":
- no source-file modification for a sustained window (e.g. ~25+ min), AND
- no new commit on the branch, AND
- no active `go`/`sqlc`/`make`/compile process, AND
- `inspect_workflow` shows the same running step across checks.

A separate, recurring annoyance: the bash tool's shell occasionally wedged on a
dangling quote (a `dquote>`/`quote>` continuation). A lone closing quote (`'`)
on its own line flushed the backlog and restored the prompt.

## Resolution

When the signals above indicate a stall: abort the run with `update_workflow`
(this does not need the shell), then recover the uncommitted work manually —
read the diff, verify (build + `-race` tests on an isolated DB), fix any real
bug found, and commit. The uncommitted work stays on the worktree through an
abort. Incremental per-feature commits (as the B3 run did) reduce how much is
at risk when a later step stalls.

Implementing a well-scoped change directly (as with X3), rather than through the
workflow runner, was by far the smoothest path when stalls were frequent.

## Prevention

Keep a human on stall/anomaly watch for autonomous loops until the runner is
proven reliable. This is also the main caution for standing up the Two-Bot QA
loop (`docs/testing/two-bot-qa-loop-plan.md`): "nobody waits on a human" is
aspirational — a silently wedged loop needs someone watching.
