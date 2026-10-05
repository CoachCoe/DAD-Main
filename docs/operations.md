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
# Fill in database settings, public HTTPS origin, and required test-mode secrets.
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
`./scripts/deploy.sh workers`. `down` stops services without deleting database volumes.
`logs` follows service logs. No command pushes images or activates live donations.

Terminate TLS at a controlled ingress, restrict API access, set the exact
`DONOR_PUBLIC_ORIGIN`, and route browsers through the web service. Compose exposes ports
only on loopback. Configure trusted client abuse limits at ingress; the API's local
rate limiter alone is not a distributed public-release defense. Never trust an arbitrary
client-supplied forwarding header. Configure database TLS for remote database connections.

For per-visitor API limits behind Next, configure ingress to overwrite a single-IP
header (for example `X-Real-IP`), then set `DONOR_CLIENT_IP_HEADER` on the web service
to that header and `DONOR_TRUSTED_PROXY_CIDRS` on the API to the web proxy's exact
address or isolated network. Block direct browser access around that ingress. The web
service strips incoming forwarding headers unless configured, and the API trusts
`X-Real-IP` only from those peers. Without this configuration, all proxied requests
share the proxy's quota. The in-process limiter permits 20 mutations per rolling minute
per address, retains at most 65,536 addresses, and fails closed when full; capacity
and distributed limits must be validated against the agreed launch spike.

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
Events park after ten failed attempts. Investigate the sanitized error class and current
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
Projection refresh handles
grace expiry, recurring boundaries, permanent recognition, and prior-year summaries.
Unknown streak state displays Updating; it must not silently become zero.

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

Run `monitor` from the API image with its database environment (or
`go run ./cmd/monitor` from `apps/donor-api`). It emits only aggregate JSON counts,
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

The backup uses PostgreSQL's consistent custom-format dump and creates a private file
without overwriting an existing path. Keep dumps outside the repository on encrypted
storage with restricted access, retention, and independent copies. The drill creates a
random disposable database, restores with fail-on-error, reads core donor/gift/identity
tables, and drops only that temporary database. It never restores over the running app.
A successful drill proves the dump restores, not disaster recovery time or key recovery.
Rehearse the restored app in an isolated environment with preserved encryption keys and
outbound Stripe/email disabled. Establish recovery-point and recovery-time objectives,
backup scheduling, retention, alerting, and off-host recovery ownership before launch.
