# Edge and ingress

```text
Browser → Cloudflare (WAF, rate limits, cache) → nginx (TLS, origin lock, hosts, limits) → web → API
```

The `nginx` Compose service is the only one with public ports (80 and 443). It runs the
pinned official image as its unprivileged user on a read-only filesystem, terminates TLS,
routes the public and staff hosts to the web service, and sets the client address the
API rate-limits on. The web service publishes no host port; the API binds to loopback.
Configuration lives in `deploy/nginx` and is mounted read-only.

## Modes

`INGRESS_MODE` selects how clients reach nginx. Both modes use the same server blocks;
only `deploy/nginx/conf/<mode>/` differs.

- `cloudflare` (default): Cloudflare is the only client. nginx requires its
  [Authenticated Origin Pulls](#origin-lock) certificate and takes the client address from
  `CF-Connecting-IP`, but only when the peer is in Cloudflare's published ranges.
- `direct`: browsers connect to nginx, and the peer address is the client. nginx is then
  the only abuse defense in front of the app; nothing absorbs a volumetric attack.

At start the container derives the host names from `DONOR_PUBLIC_ORIGIN` and
`DONOR_STAFF_ORIGIN` and refuses to start on an unknown mode, a non-HTTPS origin, or
one host for both.

| Variable | Default | Meaning |
| --- | --- | --- |
| `INGRESS_MODE` | `cloudflare` | `cloudflare` or `direct`, as above. |
| `INGRESS_TLS_DIR` | `/etc/impactgraph/tls` | Host directory with `fullchain.pem` and `privkey.pem`, readable by uid 101. |
| `INGRESS_ORIGIN_PULL_CA` | `deploy/nginx/cloudflare-origin-pull-ca.pem` | CA that signs the edge's client certificate (cloudflare mode). |
| `INGRESS_STAFF_ACCESS` | `deploy/nginx/staff-access.conf` (allows all) | nginx `allow`/`deny` rules for the staff host. |
| `DONOR_STAFF_ORIGIN` | required | Staff origin, also passed to the web build and the API. |

nginx renders its configuration only at start, and a replaced single-file mount keeps
showing the old file, so `./scripts/deploy.sh up` and `reload-ingress` label the container
with a hash of everything it mounts except the certificates. Compose recreates nginx when
that hash changes, which briefly drops connections; `reload-ingress` then reloads it,
which picks up renewed certificates without dropping any. Use those commands rather than
`docker compose up`, which clears the label and so recreates nginx.

## Certificates

- **Cloudflare mode:** a [Cloudflare Origin CA](https://developers.cloudflare.com/ssl/origin-configuration/origin-ca/)
  certificate covering both hosts (SSL/TLS → Origin Server). Only Cloudflare trusts it.
- **Direct mode:** a publicly trusted certificate, such as Let's Encrypt. nginx does not
  serve HTTP-01 challenges, so use DNS-01 (for example certbot with a DNS plugin) and a
  deploy hook that copies the files into `INGRESS_TLS_DIR` and runs `reload-ingress`.

Store the key as `root:101`, mode `0640`. TLS 1.2 and 1.3 with ECDHE suites only. There
is no OCSP stapling: Origin CA certificates have no responder and Let's Encrypt no longer
runs one. HSTS comes from the web app on every response, so nginx does not add a copy.

## Hosts and paths

| Request | Result |
| --- | --- |
| Public host, `/staff*` or `/api/donor/staff*` (any case) | `404 {"detail":"Not found"}` |
| Public host, anything else | web service |
| Staff host, `/`, `/staff*`, `/api/donor/staff/*`, `/_next/*`, `/favicon.ico`, `/robots.txt` | web service |
| Staff host, anything else | `404 {"detail":"Not found"}` |
| Unknown TLS server name | handshake refused |
| Known server name with another `Host` | `421` |
| Port 80 | `301` to HTTPS for known hosts; connection closed otherwise |

Every staff-host response carries one `X-Robots-Tag: noindex, nofollow`, set by nginx in
place of the web app's copy. The web app and API enforce the same split; nginx is defense
in depth. New public pages
need no change; a staff-host path outside those prefixes needs the allowlist in
`templates/ingress.conf.template`.

## Client address

nginx sets `X-Real-IP` and `X-Forwarded-For` to the client address on every request and
drops `Forwarded`, `CF-Connecting-IP`, `True-Client-IP`, and Next's internal
`X-Middleware-Subrequest`. The web service forwards `X-Real-IP` (its
`DONOR_CLIENT_IP_HEADER`) to the API, which trusts it only from the web container's fixed
address (`DONOR_TRUSTED_PROXY_CIDRS=172.30.42.10/32`).

`deploy/nginx/conf/cloudflare/edge-ranges.conf` is generated from
<https://www.cloudflare.com/ips-v4> and <https://www.cloudflare.com/ips-v6>. Run
`./scripts/refresh-cloudflare-ips.sh --check` monthly; when it reports a change, run it
without `--check`, commit the result, and run `reload-ingress`.

The edge network has IPv6 enabled, so in direct mode IPv6 visitors reach nginx with their
own addresses rather than as Docker's gateway, and each keeps its own quota. This needs
Docker Engine 27 or later with its default `ip6tables` setting. In cloudflare mode the
client's IPv6 address arrives in `CF-Connecting-IP`.

## Rate limits

Each layer admits at least what the layer behind it does, so a legitimate client meets
the API's limit, with its exact `Retry-After`, first. The outer layers exist to shed
floods before they cost a Node render or an API call. Reads are generous because whole
carrier-grade NATs, campuses, and event Wi-Fi share one address during a referral spike.
Stripe delivers from a few shared addresses and replays its backlog after an outage, so
the webhook has its own generous zone, which still stops a flood of forged deliveries
before each costs a signature check.

| Class | Paths (POST unless noted) | Cloudflare per IP | nginx per IP | API per client |
| --- | --- | --- | --- | --- |
| Auth | `/api/donor/login`, `/claim`, `/email-change/confirm`; `/api/donor/staff/sign-in`, `/provision`, `/activate` | 30/min, block 10 min | 20/min, burst 20 | 10/min |
| Visitor | `/api/donor/checkouts`, `/api/donor/referrals/{code}`, `/api/donor/public/partner-signup` | 240/min, block 1 min | 120/min, burst 60 | 60/min |
| General | pages, assets, other API calls | 1,200/min uncached, challenge | 30/s, burst 300 | account writes 30/min, staff writes 60/min |
| Stripe webhook | `/api/donor/stripe/webhook` | skipped | 20/s, burst 500 | signature verified |

nginx also caps each client at 100 concurrent requests and answers rejections with
`429 {"detail":"Too many requests"}`, `Retry-After` (60 for auth, otherwise 5), and
`Cache-Control: no-store`. Like the API, nginx counts an IPv4 client by its address and
an IPv6 client by its /64, which one subscriber typically holds whole
(`deploy/nginx/conf/client-key.js`, run by the image's njs module). The general zone tracks
about half a million clients before evicting the least recent. Bodies are
limited to 256 KiB (the API's JSON and webhook limit), except staff source uploads and
publications at 10 MiB. Exports stream unbuffered.

## Cloudflare configuration

Replace `donors.example.org` and `staff.example.org` with the configured hosts. Rule
counts, counting periods, and mitigation timeouts depend on the plan; where only a
10-second period is available, divide the per-minute thresholds by six.

### DNS and TLS

Proxied (orange-cloud) records for both hosts; no unproxied record may
point at the origin. SSL/TLS mode **Full (strict)**, minimum TLS 1.2, Always Use HTTPS
on, Pseudo IPv4 off.

### Origin lock

Enable **Authenticated Origin Pulls**. The
checked-in CA is Cloudflare's shared origin-pull CA from
<https://developers.cloudflare.com/ssl/static/authenticated_origin_pull_ca.pem> (valid
until 2029-11-01). Because every Cloudflare zone presents that certificate, it proves a
request came through Cloudflare, not through this zone; prefer per-zone or per-hostname
pulls with your own certificate and set `INGRESS_ORIGIN_PULL_CA` to its CA. As defense in
depth, allow inbound 443 only from Cloudflare's ranges, ideally in the provider's
firewall. Docker-published ports bypass the host's INPUT chain and ufw, so a host
firewall must filter in `DOCKER-USER`:

```sh
ipset create cloudflare4 hash:net
for range in $(curl -fsS https://www.cloudflare.com/ips-v4); do ipset add cloudflare4 "$range"; done
iptables -I DOCKER-USER -i eth0 -p tcp -m conntrack --ctorigdstport 443 --ctdir ORIGINAL \
  -m set ! --match-set cloudflare4 src -j DROP
```

Repeat with `ip6tables` and the IPv6 list if the host has public IPv6.

### Staff host

Put the whole staff host behind a **Cloudflare Access** self-hosted
application that admits only named staff through the identity provider with MFA, with an
eight-hour session to match staff sessions. Access is in front of, not instead of, the
app's password and TOTP. Without Access, use a custom rule that issues a Managed
Challenge on `http.host eq "staff.example.org" and not ip.src in $staff_networks`, or
`INGRESS_STAFF_ACCESS`.

### WAF

Deploy the Cloudflare Managed Ruleset; run the OWASP Core Ruleset in log mode
until its false positives are understood, then block. Custom rules, in order:

1. Skip for Stripe: `http.host eq "donors.example.org" and http.request.uri.path eq
   "/api/donor/stripe/webhook" and http.request.method eq "POST"` → Skip remaining custom
   rules, rate limiting rules, managed rules, and Super Bot Fight Mode. A configuration
   rule on the same expression turns Browser Integrity Check off. Stripe publishes its
   webhook addresses (<https://stripe.com/files/ips/ips_webhooks.txt>); adding
   `and ip.src in $stripe_webhooks` narrows the skip if that list is kept current.
2. Block back-office paths on the public host: `http.host eq "donors.example.org" and
   (starts_with(lower(http.request.uri.path), "/staff") or
   starts_with(lower(http.request.uri.path), "/api/donor/staff"))`.

The free **Bot Fight Mode** cannot be skipped by any rule and may challenge Stripe's
deliveries, which then retry and eventually fail; leave it off and use Super Bot Fight
Mode or Bot Management with the skip rule above.

### Rate limiting rules

Counted per IP, with a custom JSON response
`{"detail":"Too many requests"}` and status 429 where the plan allows:

1. Auth: `http.request.method eq "POST" and http.request.uri.path in {"/api/donor/login"
   "/api/donor/claim" "/api/donor/email-change/confirm" "/api/donor/staff/sign-in"
   "/api/donor/staff/provision" "/api/donor/staff/activate"}` — 30 per minute, Block for
   10 minutes.
2. Visitor: `http.request.method eq "POST" and (http.request.uri.path in
   {"/api/donor/checkouts" "/api/donor/public/partner-signup"} or
   starts_with(http.request.uri.path, "/api/donor/referrals/"))` — 240 per minute, Block
   for 1 minute.
3. Pages: `http.host eq "donors.example.org" and not starts_with(http.request.uri.path,
   "/api/")` — 1,200 per minute, Managed Challenge, cached assets not counted. API
   requests are left to nginx and the API: a challenge cannot be solved by `fetch`.

### Cache rules

In order:

1. Bypass: `http.host eq "staff.example.org" or starts_with(http.request.uri.path,
   "/api/") or starts_with(http.request.uri.path, "/account") or
   starts_with(http.request.uri.path, "/staff")`.
2. Static pages: `http.host eq "donors.example.org" and http.request.uri.path in {"/"
   "/about" "/donate" "/how-it-works" "/legal" "/partners"} and not
   any(lower(http.request.headers.names[*])[*] == "rsc")` → eligible for cache, Edge TTL
   5 minutes (ignoring the origin), Browser TTL from the origin, cache key ignoring the
   query string.

These pages are prerendered; Next.js marks them `s-maxage=31536000`. The Edge TTL is
short because a cached page outliving a deploy references `/_next/static` files the new
build no longer has; purge these URLs after `./scripts/deploy.sh up`. Next's client
router fetches the same URLs with an `RSC` header for a different payload, which
Cloudflare does not vary on, hence the exclusion. `/_next/static/*` is cached by default
and immutable. `/r/{code}`, `/ledger`, `/initiatives/*`, `/donate/thanks`, `/account`,
and `/staff` are rendered per request (`private, no-store`) and are never cached. A
referral landing (`/r/{code}`) therefore always costs one render and one API lookup at
the origin: it is the hottest origin path during a referral spike.

## Scaling beyond one origin

One Compose host runs nginx, one Next.js process, one API process, and PostgreSQL.
Cloudflare absorbs attacks and static pages, but every referral landing, account page,
and API call still reaches that host; its capacity is unmeasured until load-tested
against the agreed spike. The next limits, in order:

- **PostgreSQL** is a single instance on the same host. Move it to a managed service
  with connection pooling first; read replicas for donor reads require application work.
- **Multiple origins** behind Cloudflare Load Balancing, each running nginx, web, and API.
  Keep each worker kind on one host until concurrent workers are verified.
- **Per-instance limits.** The nginx zones and the API's buckets are per host and per
  process, so N origins grant a client up to N times the quota. Cloudflare's rules are
  then the only shared limit, and they are approximate. Exact global quotas need a
  shared store.
