# R1 security controls

Donors authenticate through single-use, 15-minute email proof and hashed, revocable
30-day sessions. Staff use individual Argon2id passwords plus six-digit TOTP, encrypted
MFA secrets, consumed counters to prevent replay, persistent lockout, and eight-hour
hashed sessions. Donor and staff cookies are separate, HttpOnly, and Secure on HTTPS.
Staff cookies use SameSite=Strict; donor cookies use Lax. Anonymous login responses do
not reveal whether an address exists.

The R2 donor app uses neither cookies nor Origin. It sends `X-DAD-Client: native` on
every request and authenticates with `Authorization: Bearer <session token>`. A request
is native only when it has that header and neither an `Origin` nor a `Cookie` header;
any other request is a browser request, whose Authorization header is ignored and whose
mutations need the exact Origin. Browsers cannot add a custom header to a cross-origin
request without a CORS preflight, and the API grants none, so another site cannot forge
a native request. Bearer tokens are the same hashed, revocable 30-day donor sessions,
ended by expiry, sign-out, sign-out everywhere, and email changes, and native requests
are rate limited in the same classes. `POST /donor/claim` returns the token in the body
only to a native request and then sets no cookie. Only donor account routes admit the
app (sign-in, claim, email-change confirmation, sign-out, `/donor/me/*`); giving,
referral capture, partner signup, and all staff routes stay browser-only, and staff
sessions are never accepted as bearer tokens. The app must keep its token in the
platform keychain/keystore. The Next proxy does not forward `Authorization` or
`X-DAD-Client`, so the app calls the API host directly. The bundled nginx serves only the
web hosts; an API host needs its own ingress, listed in `DONOR_TRUSTED_PROXY_CIDRS`, that
overwrites `X-Real-IP`.

Named permissions are checked server-side against current staff state. Ownership comes
from the session and database, never an email or customer ID supplied by the browser.
Financial mutations record their actor, target, reason, immutable command, and result.
Staff recovery/role changes revoke sessions and preserve the last active administrator.

Browser mutations require the configured exact Origin. Requests, pages, uploads,
exports, password verification concurrency, and provider calls have bounded sizes or
timeouts. The Next proxy exposes only `/donor/*`; it is not an arbitrary HTTP proxy.

Browsers reach the web service only through nginx ([edge](edge.md)). It accepts TLS 1.2
and 1.3 for the configured public and staff hosts only, serves back-office paths only on
the staff host and donor paths only on the public host, and marks the staff host
`noindex`. It sets `X-Real-IP` to the client address on every request and drops other
client-chosen address headers; behind Cloudflare it takes the address from
`CF-Connecting-IP` only from Cloudflare's published ranges and serves only connections
with an Authenticated Origin Pulls certificate. Rate limits are layered: Cloudflare's
distributed rules, nginx's per-client limits, and the API's per-instance buckets. Access
logs record method, path, status, and timing, never query strings, cookies, or referral
codes; nginx's error log can include a request line, so treat it as sensitive.

Stripe handles all card entry. Signed webhooks are verified before persistence; only
allowlisted event fields are stored. Retryable commands are recorded before provider
calls, and no donor lock is held during the call. Current remote state is reconciled rather than trusting webhook arrival order.
Logs must exclude donor payloads, credentials, tokens, and remote response bodies; API
failures are logged with route, status, and error type only. Every API response carries
`no-store`, `nosniff`, `no-referrer`, and a `default-src 'none'; frame-ancestors 'none'`
content security policy.

Public content never includes donor data or private source files. Source documents use a
dedicated encryption key and SHA-256 verification. CSV exports use field allowlists,
permission checks, private cache headers, formula neutralization, and audit trails.
Do not publish exports or treat a source document's existence as proof of its accuracy.

The frontend restricts connections to its own origin, denies framing, uses no-referrer,
and disables camera, microphone, and geolocation. Production CSP excludes unsafe-eval.
Account and staff pages render per request with a nonce-based `'strict-dynamic'` script
policy set in `proxy.ts`; public pages keep a static policy that permits inline scripts
so they can be cached. Each response carries exactly one policy. Public links into donor
accounts use full-document navigation to install the nonce policy; staff entry is a
direct document load. Any new public link into account or staff routes must use a native
anchor rather than client routing. User content must never be inserted as
raw HTML. Account tokens use fragments removed before network requests.

Release still needs verified legal/entity details, approved retention and incident
procedures, managed secret storage, backup/restore evidence, the Cloudflare edge
configuration and origin firewall,
provider acceptance, and security/accessibility review. This document describes controls
implemented in code, not a claim of independent certification or production readiness.
