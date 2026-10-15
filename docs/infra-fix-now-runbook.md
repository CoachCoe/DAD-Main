# Infra FIX-NOW runbook

The scalability analysis (`docs/requirements/scalability-analysis-100m.md`)
tagged several items FIX-NOW. The **code** ones (B3, B5, X3) are shipped. The
remaining FIX-NOW items are **infrastructure, ops, and business actions** that
cannot be done in the repo — they are executed in AWS, DNS, and vendor consoles.
This runbook makes each one turnkey: what to do, with what values, in what
order, and why the timing matters. None require an application code change (the
app already speaks SMTP/STARTTLS and runs behind a pooler-friendly pool).

Owner: _assign_. Status column: update as each is done.

| # | Item | Urgency | Status |
|---|------|---------|--------|
| X8-SES | SES production access + rate increase + domain warming | **Time-sensitive** | ☐ not started |
| X8-ACU | Confirm Aurora Serverless v2 scaling ceiling | Low (Year 1) | ☐ not started |
| X2/B4 | Connection pooler (PgBouncer / RDS Proxy) before API replicas | Before Year-1 scale-out | ☐ not started |
| X1 | Shared rate-limit store (Redis) before adding a 2nd origin | Before multi-origin | ☐ not started |
| B6 | AWS migration plan (execute later) | Plan now, move post-R1 | ☐ not started |

---

## X8-SES — the time-sensitive one

**Why now.** New SES accounts start in a **sandbox** (can only send to verified
addresses, tiny quotas). Moving to production and warming a sending domain has
real lead time: the PRD §15 gives "transactional email vendor and domain
warming" a **2–4 week** lead time, and the architecture doc flags a **latest
warming start of 30 Oct**. Receipts are effectively non-optional for a 501(c)(3),
and failed-payment/dunning email is the highest-value message in R1 — so email
delivery is on the critical path, not a nice-to-have.

**Horizon target to size against.** ~4M sends/day at 100M donors, with dedicated
IPs. R1 volume is far lower (~2K/day), but production access + a warmed domain
must exist before the first real receipt goes out.

**No app code change.** The mail workers already use SMTP + STARTTLS; SES is
drop-in via its SMTP endpoint. (The `DONOR_SMTP_CA` option added for dev mail
capture also covers any private-CA relay case, but SES uses public certs, so
nothing special is needed there.)

### Steps

1. **Pick the AWS account + region** that will host production (same region as
   the planned ECS/Aurora footprint, per architecture doc §2). SES reputation
   and verified identities are per-region.
2. **Verify the sending domain** (the dynamicaid.org sending subdomain — decide
   the exact subdomain, e.g. `mail.` or `send.`). In SES → Verified identities,
   add the domain identity and publish the DNS records it generates:
   - **DKIM** (SES Easy DKIM: 3 CNAME records) — required.
   - **SPF**: a `TXT` record authorizing `amazonses.com` (or a custom
     MAIL FROM subdomain's MX + SPF for alignment).
   - **DMARC**: a `TXT` record at `_dmarc.<domain>` — start at `p=none` for
     monitoring, tighten to `quarantine`/`reject` once aligned.
   - **Custom MAIL FROM domain** (recommended) for SPF alignment: an MX record
     to the regional SES MAIL FROM endpoint plus its SPF TXT.
3. **Request production access** (SES → Account dashboard → "Request production
   access"). Use the request details below.
4. **Request a sending-rate / daily-quota increase** appropriate to the launch
   plan (start modest; raise toward the ~4M/day horizon as volume grows). This
   is a Service Quotas / support request tied to the production-access case.
5. **Create two configuration sets** — `receipts` and `dunning` — so a
   reputation dip on one message class does not affect the other (architecture
   doc's explicit guidance). Each worker sends with its class's configuration
   set. Enable per-config-set event publishing (bounces/complaints/deliveries)
   to CloudWatch or SNS.
6. **Plan dedicated IPs + warming for horizon** (not R1): a dedicated IP pool
   warms over weeks by ramping volume. Only needed as volume approaches the
   millions/day tier; begin the warming ramp before you need the capacity, per
   the 30 Oct latest-start note. For R1's low volume, the shared pool is fine —
   but get **production access** out of the sandbox now regardless.
7. **Set the app's SMTP env to the SES SMTP endpoint** when ready
   (`DONOR_SMTP_HOST=email-smtp.<region>.amazonaws.com`, port 587,
   SES SMTP username/password credentials, `DONOR_EMAIL_FROM` on the verified
   domain). STARTTLS is already enforced; no `DONOR_SMTP_CA` needed (public CA).
8. **Test** to the SES mailbox simulator and a controlled real mailbox before
   inviting donors (per `docs/operations.md`: "Test controlled mailboxes before
   inviting donors").

### Production-access request — ready to paste/adapt

> **Use case:** Transactional email for a 501(c)(3) donation platform (Dynamic
> Aid Delivery). Messages are per-recipient and triggered by the recipient's own
> actions: donation tax receipts, annual giving summaries, failed-payment
> recovery notices, sign-in/account-claim links, email-change confirmations, and
> referral-converted notices. No marketing or bulk sends.
>
> **Recipients:** individual donors who transacted with us; each message is a
> direct consequence of their own activity.
>
> **List hygiene / bounces & complaints:** we process SES bounce and complaint
> notifications and suppress addresses automatically; the app has a mail
> suppression and consent model, and optional messages re-check consent before
> each send.
>
> **Volume:** low at launch (hundreds–thousands/day), growing with donor count;
> request an initial daily quota of [choose, e.g. 50,000] and a send rate of
> [e.g. 14/s], to be raised as volume grows.
>
> **Unsubscribe/opt-out:** transactional receipts are effectively non-optional
> for tax purposes (IRS); optional notices carry opt-out and honor it.

### Open decisions before submitting

- Exact sending subdomain of dynamicaid.org.
- AWS account + region for production.
- Initial daily quota / send-rate numbers to request.
- Who owns DNS for dynamicaid.org (to publish DKIM/SPF/DMARC).

---

## X8-ACU — Aurora Serverless v2 ceiling (low urgency)

Confirm the production Aurora Serverless v2 cluster's **max ACU** is set with
headroom; the architecture doc notes the 256-ACU ceiling is fine for the
~38 writes/s average at 100M **once B3/projection load is bounded** (done). This
is a cluster-config confirmation, not a request with lead time. Pair it with the
B4 partitioning plan when the AWS move is scoped. No action needed before the
AWS migration.

---

## X2 / B4 — connection pooler before API replicas

`docs/operations.md` is explicit: a full deployment already uses ~58 of the
default 100 Postgres connections (API pool alone is 40), so **a pooler
(PgBouncer, or RDS Proxy on AWS) must sit in front of the API pool before adding
any API replica.** Also raise each process role's connection limit in
`cmd/provision` when replicas are added (provisioning assumes one instance per
process). This is a Year-1 scale-out prerequisite, not an R1 blocker. B4's
table-partition *design* can be written now; executing partitioning waits for
Year-1 row growth.

---

## X1 — shared rate-limit store before a second origin

nginx and API rate-limit buckets are **per process**, so N origins behind a load
balancer give a client N× the intended quota (e.g. the 60/min visitor limit
becomes 60×N). Before adding a second origin, stand up a shared rate-limit store
(Redis / ElastiCache) that all origins consult. Not needed while single-origin.

---

## B6 — AWS migration plan (plan now, move post-R1)

The architecture doc §2 already sketches the lift: Cloudflare stays, nginx →
ALB, each Compose service → ECS Fargate service, Postgres → Aurora Serverless v2
+ RDS Proxy, SMTP → SES, secrets → Secrets Manager, `cmd/monitor` → scheduled
task + CloudWatch alarms. The FIX-NOW is to write the migration **plan and
runbook**; the move itself is deliberately post-R1. The single-host Compose
setup is honestly fine for the R1 launch.

---

## Suggested order

1. **X8-SES production access + domain verification/DNS** — start immediately;
   it has external lead time and is on the launch critical path.
2. Everything else is Year-1 / horizon and can follow the AWS migration
   planning (B6), which gathers X2/B4/X1/X8-ACU into one move.
