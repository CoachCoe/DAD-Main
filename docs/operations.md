# Operating the R1 sandbox

Follow the README for local setup. All money records are test-mode records; do not mix
these with a later production database or describe sandbox awards as real recognition.

## Database and containers

Use a distinct PostgreSQL database and least-privilege credentials for each environment.
Back up the database together with its separately stored encryption keys, then verify a
restore. Losing staff, email, or content encryption keys makes their encrypted records
unreadable. No automatic key-rotation or backup service is included.

```sh
cp .env.production.example .env.production
# Fill in IMAGE_TAG (for example the git commit), database settings, the public HTTPS
# origin, and required test-mode secrets. Keep the previous IMAGE_TAG for rollback.
./scripts/deploy.sh build
./scripts/deploy.sh migrate
./scripts/deploy.sh up
./scripts/deploy.sh smoke
```

`migrate` starts PostgreSQL and applies embedded forward SQL migrations. An existing
pre-rewrite database at `0030_donor_public_content` requires `./scripts/deploy.sh adopt`
instead of the initial migrate step. Adoption compares the complete donor schema, records
the baseline checksum, and preserves data. Stop all older API/worker processes before
adoption; never run two versions of financial writers. Unsupported or divergent schemas
require a reviewed migration, not a forced baseline stamp.

`up` starts the API and web service, not workers. Set `DONOR_GO_WORKERS_ENABLED=true`
only when ready to process test payments and deliver configured email, then run
`./scripts/deploy.sh workers`; the script refuses while the flag is unset. `down` stops
services without deleting database volumes. `logs` follows service logs. No command
pushes images or activates live donations. `ENV_FILE` may be relative to the current
directory. The web container runs with a read-only filesystem and tmpfs mounts for
`/tmp` and `/app/.next/cache`.

The API refuses to start with only one of `STRIPE_SECRET_KEY` and
`STRIPE_WEBHOOK_SECRET`, with a malformed encryption key, or with a trusted proxy range
broader than an IPv4 /8 or IPv6 /32. It logs which optional features are disabled.
Workers that hit a database or provider failure log the error type and retry with
exponential backoff up to one minute; persistent failures show up as queue age.

Terminate TLS at a controlled ingress, restrict API access, set the exact
`DONOR_PUBLIC_ORIGIN`, and route browsers through the web service. Compose exposes ports
only on loopback. Configure trusted client abuse limits at ingress; the API's local
rate limiter alone is not a distributed public-release defense. Never trust an arbitrary
client-supplied forwarding header. Configure database TLS for remote database connections.

Rate limits are per visitor. Production Compose gives the web service the fixed
address `172.30.42.10` on the `172.30.42.0/24` network, sets the API's
`DONOR_TRUSTED_PROXY_CIDRS` to that address, and defaults the web service's
`DONOR_CLIENT_IP_HEADER` to `X-Real-IP`. The host ingress **must** overwrite `X-Real-IP`
with the connecting client's address on every request; otherwise clients could choose
their own identity. Setting `DONOR_CLIENT_IP_HEADER` empty makes all visitors share the
proxy's quota. Edit the Compose network if that range clashes on the host. Block direct
browser access around the ingress; the API trusts `X-Real-IP` only from listed peers.

The in-process limiter keeps a token bucket per client (IPv6 clients per /64) and per
class, so a burst on one surface cannot starve another for visitors sharing an address:
sign-in and claim links 10 per minute, giving, referral capture, and partner signup 60,
other donor actions 30, and staff actions 60. Rejections carry an exact `Retry-After`.
It tracks at most 65,536 buckets and evicts an existing bucket rather than refusing new
clients when full. Capacity and distributed limits must still be validated against the
agreed launch spike.

`/health/live` checks process availability. `/health/ready` checks the migration history
through PostgreSQL with a deadline. It can be ready while Stripe or SMTP is intentionally
unconfigured. Readiness is not payment-provider acceptance or live-release authorization.

## Stripe

Use the organization's verified Stripe **test** account and snapshot webhook events at
API version `2026-09-30.endive`. Subscribe to:

- `payment_intent.succeeded` for one-time gifts.
- `invoice.payment_succeeded` and `invoice.payment_failed` for monthly periods.
- `refund.created`, `refund.updated`, and `refund.failed`.
- `checkout.session.completed` and `checkout.session.expired`.
- `customer.subscription.updated` and `customer.subscription.deleted`.

Configure `/api/donor/stripe/webhook` through the web origin, or the Go route
`/donor/stripe/webhook` behind controlled ingress. Verify signatures over original bytes.
Live events, Connect events, and mismatched API versions are rejected. No card details
belong in DAD requests, storage, logs, or support tools.

The Stripe worker persists verified collection, refunds, and billing schedules.
Checkout completion alone is not a gift. Incomplete or unsupported invoices remain
unprocessed rather than becoming guessed financial facts. A single-item monthly USD
subscription with a complete card payment is the supported billing model.

Monitor the oldest unprocessed `donor_stripe_events`, attempt counts, and worker health.
A failed event waits before its next attempt, doubling from one minute up to an hour,
so a provider outage or an event that arrives before its gift is retried later rather
than immediately. Events park after ten failed attempts. Investigate the error class
(`PaymentUnavailable`, `MissingRecord`, `InvalidFacts`, or `ProcessingError`) and current
Stripe state before explicitly retrying:

```sh
cd apps/donor-api
go run ./cmd/worker --kind stripe --retry-event evt_example --once
```

`--once` processes at most one eligible event, which may not be the reset event. Keep the
worker running to drain the queue. Never delete financial facts to make a retry succeed.

Financial commands retain immutable inputs and processor idempotency keys. Unknown
requests older than 23 hours require reconciliation. Hosted card-setup retries stop
before their fixed expiry; an old unknown request is not permission to create a new one.
Staff refund previews reconcile current provider state and reserve pending refunds.
Refund and recurring cancellation are separate actions requiring named permissions,
confirmation, reasons, and audit records. Never edit processor identifiers by hand.

## Email and projections

Run projection, login-email, email-changes, and message workers separately. Email changes
use the same mail configuration, require current-address proof before new-address proof,
and park delivery after five failures. Requesting a replacement invalidates prior proofs.
Projection refresh handles grace expiry, recurring boundaries, permanent recognition,
and prior-year summaries. A donor whose facts cannot be projected is deferred with
backoff, records an error class, and counts as parked in the monitor; other donors keep
refreshing. Unknown streak state displays Updating; it must not silently become zero.
Mail workers claim a job, then send outside the donor lock, so a slow SMTP server cannot
block payments or staff actions.

SMTP requires STARTTLS, certificate validation, and configured sender credentials.
Delivery is at least once; acknowledgement loss may cause a duplicate. Stable Message-IDs
support retries, not exactly-once guarantees. Login payloads are encrypted; financial
message bodies require the database's access and backup protections. Login links expire
15 minutes after preparation. Jobs park after five failures. Optional referral notices
recheck consent before each send; recovery stops after payment or cancellation.

Staff can inspect delivery history and request audited receipt/summary resends or a
replacement claim email. A queued response does not mean delivery succeeded. Monitor
queue age and parked jobs; automated paging and provider acceptance remain operational
work. Test controlled mailboxes before inviting donors.

## Staff and content

Provision the first administrator with `go run ./cmd/staff`; later access uses `/staff`.
Require individual accounts and retain two administrators for recovery. Recovery revokes
sessions and replaces credentials through a private invitation; it never disables MFA.

At `/staff/content`, upload private source documents, then publish complete CSV snapshots.
Sources are limited to 10 MB, encrypted, SHA-256 checked, and permission-protected.
Each CSV is limited to 512 KB and 500 rows. Exact column order:

```csv
slug,title,summary,partner,planned_start,board_approval_source_id,approved_on
```

```csv
metric,value,unit,source_id,illustrative
```

Dates are YYYY-MM-DD. Every initiative references an uploaded board-approval source.
Slugs are unique lowercase words separated by hyphens. Ledger values are nonnegative
integers with unit `USD_CENTS`, `PEOPLE`, `INITIATIVES`, or `PARTNERS`. Real figures require
a source; examples require `illustrative=true`. Supply an as-of date and stated update
cadence. Invalid publications leave the current snapshot intact. Staff are responsible
for actual accuracy and board approval; uploading a source cannot establish either.

Exports use fixed field allowlists, omit credentials and message bodies, neutralize CSV
formula prefixes, and audit completion/interruption. Referral rows retain channel.
The enrolment CSV has headers only until R2. Keep exports private and apply an approved
retention policy. Sensitive source documents have separate authenticated downloads.

## External acceptance

Before live solicitation, verify the receiving entity, EIN, processor account, approved
receipt and policy text, registrations, Wallet accounts/certificates, delivery reputation,
backup/restore and incident ownership. Exercise the full Stripe sandbox journey including
out-of-order webhooks, refund transitions, failed renewals, pauses, early resumption,
month-end boundaries, and ambiguous responses. Local test doubles do not prove these
provider behaviors. Physical QR scanning, assistive-technology checks, and viral-load
acceptance also require explicit evidence.

## Queue alerts and incident response

Run `./scripts/deploy.sh monitor [--max-age 10m]` (or `go run ./cmd/monitor` from
`apps/donor-api` with the database environment loaded). It emits only aggregate JSON counts,
parked counts, and oldest due age for Stripe, projections, financial mail, login mail,
and email changes. It exits nonzero if any job is parked or a due item is older than
five minutes; `--max-age 10m` changes the age threshold. Future scheduled sends are
excluded. Schema/database failure is also nonzero. Run this every minute from your
private scheduler, alert on two consecutive failures, and send recovery notification
when it returns to zero. Monitor process/container restarts and both health URLs too:
queue age alone cannot detect an idle stopped worker. No public monitoring endpoint or
third-party telemetry is enabled by this command.

An alert owner must distinguish unavailable dependencies, invalid data, and a halted
worker before retrying. Stop only the affected writer if facts may be wrong; preserve
inbox entries, command keys, audit records, and database evidence. Check current Stripe
state before ambiguous financial retries. Never reset all attempts or delete rows as a
repair. For mail incidents, restore delivery configuration before replay and expect
possible duplicate delivery after acknowledgement loss. Escalate incorrect recognition
or money totals to the finance owner; publish corrections only after reconciliation.
Assign primary/backup incident owners and test paging in the chosen hosting environment.

## Backup and restore drill

With PostgreSQL running and `ENV_FILE` pointing to the Compose environment:

```sh
./scripts/backup.sh /secure/backups/dad-2026-10-04.dump
./scripts/verify-restore.sh /secure/backups/dad-2026-10-04.dump
```

Both scripts require an absolute dump path. The backup uses PostgreSQL's consistent
custom-format dump and creates a private file without overwriting an existing path. Keep dumps outside the repository on encrypted
storage with restricted access, retention, and independent copies. The drill creates a
random disposable database, restores with fail-on-error, reads core donor/gift/identity
tables, and drops only that temporary database. It never restores over the running app.
A successful drill proves the dump restores, not disaster recovery time or key recovery.
Rehearse the restored app in an isolated environment with preserved encryption keys and
outbound Stripe/email disabled. Establish recovery-point and recovery-time objectives,
backup scheduling, retention, alerting, and off-host recovery ownership before launch.
