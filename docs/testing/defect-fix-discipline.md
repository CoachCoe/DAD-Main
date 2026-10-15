# Defect-fix discipline

The standard for turning a discovered defect into a durable fix. It matches the
practice already used in the R1 audit (`docs/requirements/Audit-strategy.md`)
and the fixer rules in the Two-Bot QA playbook, and it is how fixes were made
during the scalability work. Follow it for any bug fix, whether found by a
human, a test, a reviewer, or (later) a QA bot.

## The loop

1. **Reproduce first.** Confirm the defect on a real build before changing code.
   For a silent/"nothing happened" failure, don't trust the absence of output —
   replay the request and inspect the actual response/state. A failure you
   can't reproduce is either not a defect or not yet understood; say which.

2. **Write a failing test.** Add a test that fails because of the defect, in the
   right layer (unit for logic, integration for DB/role/transaction behavior,
   e2e for a user journey). The test encodes the *intended* behavior.

3. **Fix the cause, not the symptom.** Make the failing test pass by correcting
   the underlying problem.

4. **Run the full relevant suite.** `gofmt`/`vet`/`build` plus `go test -race`
   on the affected packages against a synthetic DB (see
   `apps/donor-api/AGENTS.md`). Never commit a red suite.

5. **Commit with the why.** The message explains the cause and the fix, not just
   the what.

6. **Compound it.** If the defect revealed a reusable lesson or a wrong
   assumption, write a `docs/solutions/` entry and, if a rule would prevent
   recurrence, add it to the relevant `AGENTS.md`.

## Non-negotiables

- **Never weaken, skip, or delete a test to reach green.** If a test fails after
  a change, it is either a real regression (fix the code) or a stale expectation
  that must be updated to the new *intended* behavior — and that update must be
  justified in the commit, never a quiet relaxation. A test loosened to pass is
  worse than no test, because it looks like coverage.
- **A failing expectation on a seeded/oracle test is a signal, not a nuisance.**
  When the seed oracle or a golden test disagrees with new behavior, decide
  deliberately which side is correct and bring the other in line with evidence.
- **Correctness-first on money paths.** For financial facts, recognition rules,
  migrations, auth, and the Founding Donor cutoff, the fix must be correct, not
  merely green. Prefer a slower correct path over a clever one.

## Severity and ownership (for triaged findings)

When findings come in batches (reviews, audits, QA-bot runs), label by priority
and resolve highest first:

- **P1 / critical** — security, data integrity, availability, money correctness.
  Must fix before merge.
- **P2 / important** — real defects without immediate blast radius.
- **P3 / minor** — polish, readability.

Hand to a human (don't auto-resolve) anything touching credentials, live money,
real email, migrations to shared state, or a product/business decision — with a
one-line reason.
