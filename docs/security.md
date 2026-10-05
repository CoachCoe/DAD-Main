# R1 security controls

Donors authenticate through single-use, 15-minute email proof and hashed, revocable
30-day sessions. Staff use individual Argon2id passwords plus six-digit TOTP, encrypted
MFA secrets, consumed counters to prevent replay, persistent lockout, and eight-hour
hashed sessions. Donor and staff cookies are separate, HttpOnly, and Secure on HTTPS.
Staff cookies use SameSite=Strict; donor cookies use Lax. Anonymous login responses do
not reveal whether an address exists.

Named permissions are checked server-side against current staff state. Ownership comes
from the session and database, never an email or customer ID supplied by the browser.
Financial mutations record their actor, target, reason, immutable command, and result.
Staff recovery/role changes revoke sessions and preserve the last active administrator.

Browser mutations require the configured exact Origin. Requests, pages, uploads,
exports, password verification concurrency, and provider calls have bounded sizes or
timeouts. Local abuse limits are not a substitute for controlled, distributed ingress
limits. The Next proxy exposes only `/donor/*`; it is not an arbitrary HTTP proxy.

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
procedures, managed secret storage, backup/restore evidence, trusted ingress protection,
provider acceptance, and security/accessibility review. This document describes controls
implemented in code, not a claim of independent certification or production readiness.
