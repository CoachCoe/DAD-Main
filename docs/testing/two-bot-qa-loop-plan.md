# Two-Bot QA Loop — adoption plan (not yet built)

**Status:** Planned, not started. A deliberate investment for R1 test-hardening
or R2 — not to be stood up mid-feature. Captured here so it isn't lost.

**Source:** `docs/requirements/Two-Bot QA Loop Playbook.pdf` (the Coil project's
battle-tested pattern). This doc records *why it fits DAD*, *what to reuse*,
*what to change for a money/regulated app*, and *the build steps*.

## Why this fits DAD specifically

This is not a speculative adoption — the DRAFT Testing Strategy (§6, "Autonomous
AI Testing Agents") already *requires* goal-driven agents in staging that
explore real user workflows with explicit success criteria and reproducible
evidence. The Two-Bot loop is a concrete implementation of that requirement, and
the architecture is already most of the way there:

- **Multi-tenant isolation exists** (per-donor data, staff host split) → maps
  directly to "one isolated test tenant per bot".
- **A build/readiness endpoint exists** (`/health/ready` reports migration
  state) → the "every copy reports its commit, pin each batch to it" rule is
  half-built; add version+commit to the health payload.
- **Mail already uses injected transports in tests**, so "no real email leaves
  a test copy" is already true for the automated suite. **Capture inbox is a
  prerequisite that needs a small code change, not just config** — see below.
- **Stripe is test-mode-only by construction** (`sk_test_` enforced, livemode
  rejected) → "payments on test keys" is already guaranteed in code.
- **The giving → referral → dashboard journey** is exactly the stateful,
  multi-step flow unit tests can't cover, and it carries the §07 "attribution
  rides the touch" acceptance criterion the PRD says to treat as explicit.

## What to reuse as-is

- **GitHub issues as the work queue + defect tracker**, with `qa:` labels
  (`reported`/`reproduced`/`fixed`/`verified`/`not-a-defect`/`needs-owner`/
  `blocked`). No new infra; gives an audit trail (valuable for a regulated
  product). One long-running queue issue per bot.
- **The environment rules** (hard-won in the source): one test copy per bot +
  a demo copy; every deploy to all copies at the same commit; health endpoint
  reports version+commit and a bot stops if it changes mid-batch; capture inbox
  for mail; test-record prefixes (`QA<YYYYMMDD>`) with each bot deleting only
  its own; a fixture do-not-touch list in every handoff; a response header that
  repeats flash messages so automation doesn't miss a toast.
- **The batch-writing rules**: 10–15 cases/handoff, at least half acting
  (create/edit/send then verify), ≥2 edge cases, end with cleanup + "what
  remains", and read routes/forms/permissions in code before writing cases so a
  case never expects behavior the code never had.
- **The defect→failing-test→fix→full-suite fixer discipline** — already adopted
  as `docs/testing/defect-fix-discipline.md`.
- **The coordination file + seats model** for multiple agents in one repo
  (claim files before editing; never deploy another agent's uncommitted work;
  one coordinator per queue).

## What to change for a money / regulated app

The playbook comes from a lower-stakes context (a practice manager). Keep the
loop; tighten the risk posture:

- **The autonomous fixer must NOT auto-deploy changes to money paths.** Any fix
  touching financial-fact handling, recognition rules, migrations, auth, or the
  Founding Donor cutoff is labelled `qa:needs-owner` for human sign-off before
  deploy — extend the playbook's existing "credentials / live money / real
  email / product decisions go to the owner" carve-out to these.
- **Keep a human on stall/anomaly watch.** "Nobody waits on a human" is
  aspirational. Our own workflow runs stalled repeatedly and needed manual
  recovery (`docs/solutions/004-workflow-stalls-recover-manually.md`); a
  silently wedged loop produces nothing. Build the loop, but watch it until
  proven.
- **Demo copy carries no card flows and no real anything.** Per the playbook,
  but worth restating for a donation product that solicits nationally.
- **Staff/back-office journeys run on the isolated staff host** with the virtual
  authenticator (passkeys), matching how the existing browser e2e tests drive
  staff pages.
- **Never echo a secret to diagnose it** (the playbook's pasted-key incident):
  inspect config with length/hash checks only; logins stay in root-only files.

## Acceptance gates (the "are we done" phase)

Endless batches find defects but don't decide done. After defects slow, switch
to a finite set of gates, each with an owner, a definition of done, and a
required evidence packet (browser screenshots mapped to cases + exact build and
record IDs). Candidate gates for DAD, adapted from the source:

- **Baseline** — what each test firm looks like; what's configurable.
- **Giving journey** — guest gift (one-time and recurring) end to end, receipt,
  account claim; attribution frozen at touch and surfaced on the dashboard.
- **Referral journey** — Link vs IRL first-touch, conversion credit, dashboard
  counts/dollars match, same-numbers web parity.
- **Recognition** — streak increment, 14-day grace, pause-freeze, Founding badge
  before cutoff; failed-payment recovery emails at the right offsets.
- **Staff back office** — search, refund, cancel with audit; exports; permission
  and host isolation.
- **Reconciliation / ops** — the daily reconcile job surfaces an injected
  missing-webhook; backup/restore; monitoring.
- **Isolation** — data isolation between donors and between staff roles.

Rule: engineering work goes to a coding agent on a disposable copy; *verification*
goes to the bots against that exact release. Evidence, not claims.

## Prerequisite enablers (code/config)

Three small enablers the loop depends on. Status as of 2026-10-15:

1. **Health endpoint reports version+commit** — DONE
   (`feat/health-reports-build`: `internal/buildinfo` + `/health/live` and
   `/health/ready` now return `commit`; the OpenAPI contract was updated to
   match). This is what lets a bot pin a batch to a build and stop if the commit
   changes mid-batch.
2. **Mail capture inbox (e.g. Mailpit)** — DONE (enabler). The SMTP transport
   mandates STARTTLS and verifies the server cert against the system trust store
   with no cleartext / `InsecureSkipVerify` fallback — a good security property
   for a money app that was preserved. Added an optional `DONOR_SMTP_CA` env var
   that loads a PEM into the transport's `RootCAs` when set (keeps STARTTLS
   mandatory and verification on; also useful in prod for a private mail CA),
   plus a Mailpit dev compose service and a one-time local-cert step. The dev
   env points `DONOR_SMTP_CA` at the Mailpit cert to capture mail without real
   delivery. No cleartext/skip-verify shortcut was added.
3. **Flash-message response header** — NOT DONE; deferred. QA-bot-only value
   (lets a browser agent read a message it might miss on screen). Also needs a
   design decision about what counts as a "flash" in this Next.js app, best
   answered when wiring the bot that consumes it.

## Build steps (when it's time)

1. Stand up 2 test tenants + a demo copy (health endpoint already reports
   version+commit, enabler 1); do enabler 2 (the `DONOR_SMTP_CA` change +
   Mailpit) and enabler 3 (flash-repeat header) as part of this.
2. Create the GitHub pieces: one queue issue per bot, the `qa:` labels, a
   finding issue template, and a queue file in the repo for written batches.
3. Give each bot an identity: its own app login on its own tenant, a GitHub
   token to post with, a capture-inbox address; store logins outside git.
4. Write the bot brief; test with one hand-posted batch.
5. Write + hand-run the coordinator; then schedule it (launchd/cron) with a lock.
6. Write the fixer with the money-path guardrails above; schedule it once the
   suite is fast and trustworthy.
7. Add the coordination file and the "read it first" rule.
8. When defects slow, switch to the acceptance gates.

## Decisions to make before building

- Which browser-capable agent (the source used Grok); must drive a browser, read
  a GitHub issue, post a comment.
- Where the test tenants are hosted and who owns their credentials.
- The scheduler host (watch its disk — a full disk silently killed both loops in
  the source).
- Confirm the money-path guardrails above with whoever owns release/compliance.
