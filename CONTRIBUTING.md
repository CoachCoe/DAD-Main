# Contributing

Use `dev` for the current R1 work. Keep commits focused; do not rewrite shared history.
Read [the product contract](docs/donor-domain.md) before changing financial behavior.

Go owns backend behavior, TypeScript owns the web application, and SQL owns persistence.
Keep recipient workflows, AI, blockchain, and aid disbursement outside this release.
Do not add dependencies without a concrete need. Do not commit working notes, progress
trackers, generated build output, credentials, or editor files.

Follow the [README](README.md) setup, then run `make test`, `make lint`, and
`make test-browser-e2e`. Database integration tests use only the dedicated `donor_go`
database. They do not replace Stripe test-account acceptance or real-device checks.
Never weaken assertions, skip a failing test, or fabricate production facts to pass CI.

Change SQL sources and regenerate with `make generate`; never hand-edit generated query
files. Add a new forward migration for schema changes. Existing migration checksums must
remain stable. Review transaction boundaries, authorization, idempotency, refund effects,
and out-of-order provider events in every financial change.

Read `apps/web/AGENTS.md` and the installed Next documentation before frontend changes.
Check mobile and desktop, keyboard interaction, loading/error/empty states, and safe retry
behavior. A cancelled network request is not proof that a payment command failed.

Keep documentation accurate. Report test limitations and external acceptance still
required. Never print donor data, tokens, passwords, keys, or provider response bodies in
logs, tests, screenshots, or pull requests.
