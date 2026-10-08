# Operating the R1 sandbox

Follow the README for local setup. All money records are test-mode records; do not mix
these with a later production database or describe sandbox awards as real recognition.

## Database and containers

Use a distinct PostgreSQL database and least-privilege credentials for each environment.
Each process's database pool is sized for its role: the API opens up to 40 connections,
and each worker, the monitor, migrations, and the staff command up to 2. A full
deployment therefore needs about 54 of PostgreSQL's default 100 `max_connections`. When
adding API replicas, raise `max_connections` (or put a pooler such as PgBouncer in front)
so the total stays below it; `pool_max_conns=N` in `DONOR_GO_DATABASE_URL` overrides every
process's size at once.

Back up the database together with its separately stored encryption keys, then verify a
restore. Losing the email or content encryption key makes its encrypted records
unreadable. No automatic key-rotation or backup service is included.

```sh
cp .env.production.example .env.production
# Fill in IMAGE_TAG (for example the git commit), database settings, the public and staff
# HTTPS origins, the ingress mode and certificate directory, and required test-mode
# secrets. Keep the previous IMAGE_TAG for rollback.
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

`up` starts the API, web service, and nginx, not workers. Set `DONOR_GO_WORKERS_ENABLED=true`
only when ready to process test payments and deliver configured email, then run
`./scripts/deploy.sh workers`; the script refuses while the flag is unset. `down` stops
services without deleting database volumes. `logs` follows service logs. No command
pushes images or activates live donations. `ENV_FILE` may be relative to the current
directory. The web container runs with a read-only filesystem and tmpfs mounts for
`/tmp` and `/app/.next/cache`. `up` and `reload-ingress` recreate nginx when its
configuration has changed ([edge](edge.md#modes)), and `reload-ingress` makes it reread
renewed certificates without dropping connections.

The API refuses to start with only one of `STRIPE_SECRET_KEY` and
`STRIPE_WEBHOOK_SECRET`, with a malformed encryption key, with a trusted proxy range
broader than an IPv4 /8 or IPv6 /32, with a `DONOR_STAFF_ORIGIN` that is not an exact
HTTPS origin or shares the public host, or with the retired `DONOR_STAFF_ENCRYPTION_KEY`
set but no staff origin. It logs which optional features are disabled.
Workers that hit a database or provider failure log the error type and retry with
exponential backoff up to one minute; persistent failures show up as queue age.

Browsers reach the app only through nginx, which publishes ports 80 and 443, terminates
TLS, separates the public and staff hosts, and applies per-client limits. In production it
runs behind Cloudflare, which absorbs attacks, applies distributed limits, and caches
static pages; it also runs without Cloudflare. [Edge and ingress](edge.md) covers modes,
certificates, the Cloudflare configuration, and the limits at each layer. The web service
publishes no host port and the API binds to loopback. Set the exact `DONOR_PUBLIC_ORIGIN`
and `DONOR_STAFF_ORIGIN`. Configure database TLS for remote database connections.

The back office has its own hostname, such as `staff.donors.example.org`. Point DNS for it
at the ingress and include it in the TLS certificate; nginx forwards the original `Host`
header, and the web server chooses what to serve by host. Both origins are build arguments
of the web image as well as API settings, so rebuild the web image when either changes.
Staff passkeys belong to the staff hostname; changing it means every staff member enrols
again.

Rate limits are per visitor. nginx overwrites `X-Real-IP` with the client address on every
request (behind Cloudflare, from `CF-Connecting-IP` sent by Cloudflare's ranges only) and
drops other forwarding headers. Production Compose gives the web service the fixed address
`172.30.42.10` on the `172.30.42.0/24` network, sets the API's `DONOR_TRUSTED_PROXY_CIDRS`
to that address, and defaults the web service's `DONOR_CLIENT_IP_HEADER` to `X-Real-IP`.
Setting `DONOR_CLIENT_IP_HEADER` empty makes all visitors share the proxy's quota. Edit
the Compose network if that range clashes on the host.

The in-process limiter keeps a token bucket per client (IPv6 clients per /64) and per
class, so a burst on one surface cannot starve another for visitors sharing an address:
sign-in, claim links, and passkey answers 10 per minute, giving, referral capture, and
partner signup 60, other donor actions 30, and staff actions and passkey challenges 60.
Rejections carry an exact `Retry-After`.
It tracks at most 65,536 buckets and evicts an existing bucket rather than refusing new
clients when full. It is the per-instance last line behind the nginx and Cloudflare
limits. Capacity must still be validated against the agreed launch spike.

`/health/live` checks process availability. `/health/ready` checks the migration history
through PostgreSQL with a deadline. It can be ready while Stripe or SMTP is intentionally
unconfigured. Readiness is not payment-provider acceptance or live-release authorization.

Features that are not configured answer 503 with a `code` (`PAYMENTS_UNCONFIGURED`,
`EMAIL_UNCONFIGURED`, or `SOURCE_STORAGE_UNCONFIGURED`) and are not logged as errors; a
503 without a code is a transient failure and is logged.

### Upgrading an existing deployment

1. Set a new `IMAGE_TAG`, keeping the previous one for rollback, and run
   `./scripts/deploy.sh build`.
2. Run `./scripts/deploy.sh migrate` before starting any new API or worker process, which
   requires the complete migration history.
3. Run `./scripts/deploy.sh up`, then `workers` if they are enabled, then `smoke`.

Some migrations need more:

- `0007_staff_passkeys` replaces staff passwords and authenticator codes with passkeys.
  It deletes the old credentials and ends every staff session and open invitation, so
  every staff member enrols again: run break-glass recovery
  ([staff and content](#staff-and-content)) for one administrator, who creates a passkey
  from the printed invitation and then issues recovery invitations to the remaining staff
  from `/staff`.
- `0008_referral_touch_retention` builds its indexes without `CONCURRENTLY`, blocking
  checkout writes while it runs; on a large database, migrate at a quiet time.
- `0009_subscription_sync` adds the billing anchor that dates paused renewals. A
  subscription synced before it shows no anchor until the Stripe worker next syncs it, so
  start the workers promptly after `up`.

## Stripe

Use the organization's verified Stripe **test** account and snapshot webhook events at
API version `2026-09-30.endive`. Subscribe to:

- `payment_intent.succeeded` for one-time gifts.
- `invoice.payment_succeeded` and `invoice.payment_failed` for monthly periods.
- `refund.created`, `refund.updated`, and `refund.failed`.
- `checkout.session.completed` and `checkout.session.expired`.
- `customer.subscription.updated` and `customer.subscription.deleted`.

Point the endpoint at `/api/donor/stripe/webhook` on the public origin. nginx gives it
its own generous limit ([edge](edge.md#rate-limits)); Cloudflare must skip its bot and
rate rules for it ([edge](edge.md#waf)). Verify signatures over original bytes.
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
Recurring views, card update status, and staff refund and cancellation previews never
call Stripe: they show the state the worker last synced from verified webhooks, and
`observed_at` says when that was. Pending refunds reserve their amount in previews. A
change reads Stripe first and syncs what it finds, even when it then refuses the change,
so the donor's next view is current. While the Stripe worker is behind, views lag by the
age of the oldest unprocessed event, and changes refused as out of date answer 409. A
staff refund whose outcome was lost stays pending until the original action is retried.
A second recurring change submitted while another is still with Stripe receives 409; the
subscription lease expires after two minutes if a writer stops mid-command, and the same
request ID can then resume it. One-time gifts take their paid time from the successful
charge, so a restricted key needs read access to PaymentIntents and Charges.
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
The projection worker also applies referral-touch retention ([donor domain](donor-domain.md#referrals)):
on start and then hourly it deletes touches that led to no attributed gift and were
captured more than 13 months ago, at most 2,000 per transaction and one batch between projection
refreshes, until none remain. Concurrent projection workers share the work. A failed pass
is logged as a worker step failure and retried an hour later without holding up
projections. Retention is not a monitored queue, since a touch past retention waits up
to an hour by design.
Mail workers claim a job, then send outside the donor lock, and Stripe calls for commands,
refunds, card setup, and webhooks likewise run without it, so a slow provider cannot block
payments or staff actions for the same donor.

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

Staff sign in with passkeys on the staff host. Provision the first administrator with
`go run ./cmd/staff --email … --name …` (with the database and `DONOR_STAFF_ORIGIN`
configured); later access uses `/staff`. Require individual accounts and retain two
administrators, each with their own passkey, for recovery. An administrator's recovery
action deletes the account's passkeys, revokes its sessions and invitations, and issues a
private invitation from which the staff member creates a new passkey.

When no administrator can sign in, an operator with database access runs break-glass
recovery for an enabled administrator:

```sh
cd apps/donor-api
go run ./cmd/staff --recover --email named.person@example.org
```

It works like the back-office recovery action and is audited as `staff.recovery_invited`
with no actor. Protect the printed invitation, and review the audit history afterwards.

At `/staff/content`, upload private source documents, then publish complete CSV snapshots.
Sources are limited to 10 MB, encrypted, SHA-256 checked, and permission-protected.
Without `DONOR_CONTENT_ENCRYPTION_KEY`, uploads return 503 before the file is read;
publishing still works. Donor records have linkable URLs (`/staff/donors/{id}`), and
search keeps its query in `/staff?q=`.
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
Referral-touch rows captured more than 13 months ago appear only if they led to an
attributed gift.
The `billing-history` CSV contains verified failed and successful invoice observations.
See [analytics](analytics.md) for its coverage boundary and for timestamp, replay, and
renewal-cohort rules.

The enrolment CSV has headers only until R2. Keep exports private and apply an approved
retention policy. Sensitive source documents have separate authenticated downloads.

## Load testing with representative data

Capacity is measured against a populated staging deployment: `cmd/seed` fills a
disposable database with a synthetic donor population, and `cmd/loadtest` drives a
referral-spike mix at an API serving it. Use the intended database size, network path,
and process sizes; never point either command at production.

The seeder writes only to a database it can show is disposable. The URL comes from
`-database-url`, never from `DONOR_GO_DATABASE_URL`; `-confirm` must repeat the database
name; and it refuses a database holding any donor whose address is not
`@loadtest.invalid`, or one already seeded unless `-append` is set. Create a dedicated
database, migrate it, then seed it from the repository root:

```sh
(cd apps/donor-api && DONOR_GO_DATABASE_URL=postgresql://user:pass@db:5432/dad_load go run ./cmd/migrate)
make seed SEED_DATABASE_URL=postgresql://user:pass@db:5432/dad_load SEED_CONFIRM=dad_load \
  LOAD_FIXTURE=/secure/dad-load.json
```

`SEED_DONORS` (default 50,000) donors are generated deterministically from `-seed`:
mostly monthly gifts at $30, some at $10, $50, $100 or custom amounts, one-time gifts,
abandoned checkouts, up to two years of anniversary renewals with failures recovered
within and after the 14-day grace, unrecovered failures, pauses, cancellations, refunds,
and referral trees in which most donors refer nobody and a few refer hundreds, with Link
and IRL visits that did and did not convert. Rows are written as checkout and webhook
processing store them, including processed Stripe events, and the projection worker's
code then derives every projection. Before the Founding Donor cutoff every giver earns
the badge. Messages queued for seeded history are marked suppressed so no mail worker
sends them, and seeded addresses use the reserved `.invalid` domain.

The fixture is a new mode-0600 file, refused inside a git working tree, holding
`-sessions` (default 2,000) live session tokens that favour active referrers, and
referral codes repeated in proportion to how often each would be visited. Treat it as a
credential. The sessions last 30 days; delete the fixture and drop the database after
the run.

Run the API as for production, and run the projection worker, since projections expire
after a day and dashboards then wait for it. Every scenario reads only the database,
including recurring, so Stripe configuration does not change the result. Checkout is not
in the mix because it calls Stripe.

Referral captures are rate limited per client, and one generator is one client. With
`-client-ips` it sends each user, and each captured visitor, its own `X-Real-IP` from the
198.18.0.0/15 benchmarking range, as the ingress does for real visitors. The API accepts
that header only from `DONOR_TRUSTED_PROXY_CIDRS`, so add the load host's address to the
staging API's list and have it reach the API directly, not through the ingress, which
overwrites the header. Otherwise captures fail with 429.

```sh
make loadtest-seeded LOAD_API=http://api.staging.internal:8002 LOAD_FIXTURE=/secure/dad-load.json
```

This runs a 60-second burst at 256 users with a 250 ms p99 budget for every scenario,
then a five-minute soak at 32 users with 100 ms, and exits nonzero on any failed request
or exceeded budget. For other shapes run `go run ./cmd/loadtest` with `-duration`,
`-concurrency` (up to 2,048), `-mix name=weight,...` and `-max-p99 250ms,dashboard=400ms`.
The default weights are referral-lookup 30, referral-capture 12, referral-missing 3 (an
unknown code, expecting 404), content 15, dashboard 18, gifts 7, sharing 5, profile 4,
recurring 4 and annual-summary 2. Each signed-in read uses a donor drawn from the fixture;
`-native` (default 0.25) of users present it as the donor app's bearer token, the rest as
the browser cookie.

The JSON result gives requests, failures, requests per second, p50/p95/p99/max latency
in milliseconds and statuses overall, and the same per scenario with error kinds
(`transport`, `timeout`, `oversized`). Users wait for each response before sending the
next, so throughput is what the deployment sustained at that concurrency. Latency that
rises evenly across scenarios, including the single-query referral lookup, means
requests are queueing for database connections or API CPU; a tail in one scenario points
at its queries, which can be examined with `EXPLAIN (ANALYZE, BUFFERS)` against the
seeded database. A generator sharing a host with the API or database competes with them
for CPU. Keep results with the run's ticket, not in the repository.

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
