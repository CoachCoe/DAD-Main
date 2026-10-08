# R1 security controls

Donors authenticate through single-use, 15-minute email proof and hashed, revocable
sessions: a 30-day cookie in browsers, a rotating token pair in the app. Staff sign in with individual passkeys (WebAuthn, verified by
go-webauthn) and receive eight-hour hashed sessions. Donor and staff cookies are
separate, HttpOnly, and Secure on HTTPS. Staff cookies use SameSite=Strict; donor cookies
use Lax. Anonymous login responses do not reveal whether an address exists.

The back office runs on its own host, `DONOR_STAFF_ORIGIN`, which the API requires to
be neither the public host nor a parent domain of it, since a site may use passkeys for
its parent domain. Both origins must be written exactly as browsers send them, because
Origin headers and passkey client data are compared with them byte for byte. The web server answers only the back office and its API
there, marked noindex, and none of it on the public host. The staff cookie is host-only
(`__Host-dad_staff_session` over HTTPS), so it never reaches the public site, and a page
on either host cannot pass the other's Origin check. The staff host is the passkey
relying party: passkeys are bound to it and are useless to any other site. The two
hosts are the same site, so SameSite=Strict alone would still send the staff cookie with
a link followed from the public host; staff routes therefore also refuse any request whose
`Sec-Fetch-Site` is present and neither `same-origin` nor `none`, which covers reads with
effects such as audited export downloads. The web proxy forwards that header to the API.

Staff sign-in is usernameless. The browser offers a discoverable passkey for the staff
host, so there is no email field, no account enumeration, and no password to guess or
lock out. Every challenge is random, held server-side in a single-use ceremony that
expires after five minutes, and deleted when answered. The API verifies the challenge,
origin, relying party, user verification (required), signature, and signature counter.
Synced passkeys always report a counter of zero, which is accepted; any other counter
that fails to advance is treated as a cloned passkey and refused. A disabled account or an
unknown passkey cannot sign in, and an unknown passkey still spends its challenge. Failed
sign-ins with a known passkey are audited as `staff.sign_in_failed` with the reason:
`invalid`, `counter`, or `disabled`. Invitations expire after 24 hours, allow five
attempts, and register a passkey bound to that account; a rejected attestation or a
passkey already registered to an account counts as an attempt and is audited as
`staff.activation_failed` (`invalid` or `credential_in_use`). Only the transports WebAuthn
defines are stored with a passkey. Recovery deletes every passkey of the account, revokes its sessions and
invitations, and issues a new invitation. Attestation is not requested, so the passkey
provider (device, platform, or password manager) is not verified; passkeys are
stored as public keys and need no encryption key.

The R2 donor app uses neither cookies nor Origin. It sends `X-DAD-Client: native` on
every request and authenticates with `Authorization: Bearer <access token>`. A request
is native only when it has that header and neither an `Origin` nor a `Cookie` header;
any other request is a browser request, whose Authorization header is ignored and whose
mutations need the exact Origin. Browsers cannot add a custom header to a cross-origin
request without a CORS preflight, and the API grants none, so another site cannot forge
a native request.

`POST /donor/claim` returns tokens in the body only to a native request and then sets no
cookie: a 15-minute access token and a refresh token, `<family>.<secret>`. Each part is
256 random bits, and only SHA-256 hashes are stored, so lookups by hash leave no secret
to compare in variable time. The family part is constant for the session and finds it;
it reveals nothing about the access token or the session ID. `POST /donor/refresh`
refuses every browser request, even from the public origin, and exchanges the refresh
token for a new pair, retiring the previous access token at once. A refresh token works
once: presenting one its session has already exchanged revokes the whole session and
records `REFRESH_REUSED`, because either the app or a copy of the token is stale. The one
exception is the token exchanged most recently, accepted again for one minute after the
exchange and answered with a pair that supersedes the first, so a response lost on a
mobile connection does not sign the donor out; a stolen token used within that minute
still exposes itself when the app next refreshes with the superseded pair. Concurrent
refreshes are serialized by a lock on the session row, and the later answer wins. Each
refresh extends the session to 60 days after it, never past a year after sign-in. Access
and refresh tokens are looked up in different columns, so neither is accepted as the
other.

Every request checks its session in the database, so sign-out, signing out another
session or every other session, sign-out everywhere, refresh-token reuse, and email
changes end a session at once; the access lifetime only bounds the use of a token that
leaks. Donors see each session at `GET /donor/me/sessions`, as a browser, which is not
fingerprinted, or the app with the device name it reports (stripped of control and
formatting characters and capped at 64 characters) and platform, with sign-in and last
use recorded at most every five minutes. They end one by its random session ID, which is
unrelated to any token, and an ID that is not theirs is not found. Sign-ins and
sign-outs of other sessions are recorded in `donor_session_events`. Native requests are
rate limited in the same classes, refreshes in the account class rather than the
sign-in budget. Only donor account routes admit the app (sign-in, claim, refresh,
email-change confirmation, sign-out, `/donor/me/*`); giving, referral capture, partner
signup, and all staff routes stay browser-only, and staff sessions are never accepted as
bearer tokens. The app must keep its tokens in the platform keychain/keystore and send
one refresh at a time. The Next proxy does not forward `Authorization` or
`X-DAD-Client`, so the app calls the API host directly. The bundled nginx serves only the
web hosts; an API host needs its own ingress, listed in `DONOR_TRUSTED_PROXY_CIDRS`, that
overwrites `X-Real-IP`.

Named permissions are checked server-side against current staff state. Ownership comes
from the session and database, never an email or customer ID supplied by the browser.
Financial mutations record their actor, target, reason, immutable command, and result.
Staff recovery/role changes revoke sessions and preserve the last active administrator.

Browser mutations require the exact Origin of their surface: the public origin for donor
and public routes, the staff origin for staff routes. Requests, pages, uploads, passkey
responses, exports, and provider calls have bounded sizes or timeouts. The Next proxy
exposes only `/donor/*`; it is not an arbitrary HTTP proxy.

Browsers reach the web service only through nginx ([edge](edge.md)). It accepts TLS 1.2
and 1.3 for the configured public and staff hosts only, serves back-office paths only on
the staff host and donor paths only on the public host, and marks the staff host
`noindex`. It sets `X-Real-IP` to the client address on every request and drops other
client-chosen address headers; behind Cloudflare it takes the address from
`CF-Connecting-IP` only from Cloudflare's published ranges and serves only connections
with an Authenticated Origin Pulls certificate. Rate limits are layered: Cloudflare's
distributed rules, nginx's per-client limits, and the API's per-instance buckets
([edge](edge.md#rate-limits)). nginx's access log records the time, client address, host,
method, path, status, response bytes, durations, and Cloudflare's `CF-Ray`, never query
strings, cookies, or referral codes. Client addresses are personal data, so keep logs no
longer than the approved retention policy allows ([operations](operations.md#database-and-containers)).
nginx's error log can include a request line, so treat it as sensitive.

Stripe handles all card entry. Signed webhooks are verified before persistence; only
allowlisted event fields are stored. Retryable commands are recorded before provider
calls, and no donor lock is held during the call. Current remote state is reconciled rather than trusting webhook arrival order.
Reads never call Stripe, so page views cannot multiply provider requests.
Logs must exclude donor payloads, credentials, tokens, and remote response bodies; API
failures are logged with route, status, and error type only. Every API response carries
`no-store`, `nosniff`, `no-referrer`, and a `default-src 'none'; frame-ancestors 'none'`
content security policy.

Public content never includes donor data or private source files. Source documents use a
dedicated encryption key and SHA-256 verification. CSV exports use field allowlists,
permission checks, private cache headers, formula neutralization, and audit trails.
Do not publish exports or treat a source document's existence as proof of its accuracy.
Referral touches store only a hash of the visitor token; touches that led to no
attributed gift are deleted 13 months after capture.

The frontend restricts connections to its own origin, denies framing, uses no-referrer,
and disables camera, microphone, and geolocation. Production CSP excludes unsafe-eval.
Account and staff pages render per request with a nonce-based `'strict-dynamic'` script
policy set in `proxy.ts`; public pages keep a static policy that permits inline scripts
so they can be cached. Each response carries exactly one policy. Public links into donor
accounts use full-document navigation to install the nonce policy; staff entry is a
direct document load on the staff host. Any new public link into account routes must use
a native anchor rather than client routing. Both origins are compiled into the web build,
so its host routing and headers always agree. User content must never be inserted as
raw HTML. Account tokens use fragments removed before network requests.

Release still needs verified legal/entity details, approved retention and incident
procedures, managed secret storage, backup/restore evidence, the Cloudflare edge
configuration and origin firewall,
provider acceptance, and security/accessibility review. This document describes controls
implemented in code, not a claim of independent certification or production readiness.
