# Shared mobile foundation

DAD-Main owns donor business rules, the canonical OpenAPI contract, design values, and
supported donor links. Mobile applications consume a pinned release of these interfaces.
Their screens and platform interactions are separate from the Go financial domain.

## Versioned bundle

`contracts/mobile/version.json` versions the bundle independently from backend releases.
`make bundle-mobile MOBILE_BUNDLE_DIR=/tmp/dad-foundation` creates:

- `openapi.json`: the complete canonical API.
- `mobile.openapi.json`: native-admitted mutations, donor-account and public
  initiative/event reads, and liveness,
  derived from the canonical API without rewriting schemas or response variants.
- `swift.openapi.json`: the mobile API adapted for the pinned Apple generator, with
  equivalent nullable object/scalar states and an explicit literal-null type override.
- `DADNullValue.swift`: the strict Codable implementation for that override.
- `tokens.json`: platform-neutral design values, with `schema_version`.
- `tokens.ts`, `DADTokens.swift`, `DADTokens.kt`: generated platform values.
- `links.json`: approved account, proof, referral, and notification destinations.
- `manifest.json`: bundle version, source Git revision, and SHA-256 of every other file.

The mobile contract is an integration surface, not a decision to expose every operation
in the first mobile product. Backend authorization remains authoritative. Native requests
call the API host directly with `X-DAD-Client: native`, no cookies or Origin, and a bearer
access token when authenticated. Browser-only giving and staff operations stay on the web.

After checks pass on the release commit, tag it `mobile-foundation-v<version>` (using the
version in the file, for example `mobile-foundation-v2.3.0`) and push that tag. The mobile-foundation workflow verifies the tag
matches the declared version and publishes the individual files as GitHub Release assets.
Existing release assets are never overwritten. The workflow also retains a candidate
bundle as an Actions artifact on normal branch and PR builds; those artifacts are not
released versions. A release must be built from committed, clean sources.

Each app records the exact release tag and manifest hash, vendors that release's files,
and verifies both the manifest and file hashes before generation. Updating is an explicit
reviewed change, not a fetch of `dev` or `latest` during a build. CI can regenerate from
the vendored contract without network access to this private repository. An authenticated
maintainer can obtain a release with:

```sh
gh release download mobile-foundation-v2.3.0 --repo CoachCoe/DAD-Main --dir /tmp/dad-foundation
```

Use patch versions for equivalent packaging corrections, minor versions for compatible
additions, and major versions for breaking contract/token/link changes. A mobile release
pins its dependency; changing the backend must preserve supported installed clients.
Breaking field removals, type changes, and newly required request fields require a
coordinated compatibility plan. Do not invent financial values for unknown server states.

## Compatible additions in version 2.3

Version 2.3 adds response fields only; no operation is added or removed. The partner
overview carries `user_id`, the calling partner user's own id, so a client can recognise
itself in the users list and withhold self-removal (the server also refuses it). The
donor dashboard carries `rankings_opted_out`. Clients must ignore response fields they
do not know.

## Compatible additions in version 2.2

Version 2.2 adds donor signup and password authentication, optional checkout signup
fields, badge artwork, and analytics event/journey UUIDs. Existing financial request
shapes remain accepted. Staff-only co-funding, spend publication and recognition
controls remain outside the native-admitted contract. Native clients should emit the
shared analytics taxonomy only after consent and use stable event UUIDs on retries.

## Compatible additions in version 2.1

Version 2.1 adds authenticated invite-only event calendar downloads, partner device
registration and push delivery, and application-status receipts. Existing version 2.0
request and response bodies remain unchanged. Partner notifications open `/partner`
and use the partner session, whose existing 12-hour lifetime also bounds registration.
Applications that expose pending application status must securely retain their separate
receipt; it does not authenticate a partner user. See `docs/operations.md` for lifecycle
and recovery behavior. The web donor sign-in page also offers the existing phone-code
API, alongside email and passkeys.

## Moving the apps from foundation 1 to 2

Version 2 contains the expanded R1 donor and partner API. Both native foundations
pin its manifest and payload hashes, generate their scoped session clients, and admit
`/account/gifts` for gift notifications. The bundle includes public initiative detail/spend
and event detail/calendar reads for the next product screens.

Version 1.0.0 at `5e237101c0ad81aa4675d16a94eb64863c5276b9` predates the identity,
volunteer, ranking, and notification fields now returned by the profile endpoint. Its
Swift decoder rejects those additional fields. Updating the foundation fixes new builds;
it cannot repair an installed version 1 decoder. If a version 1 app has been distributed
against a real environment, preserve its response contract through an explicitly
versioned API during migration. Future response additions also need installed-client
compatibility tests before deployment.

## Swift generator compatibility

The compile probe in `contracts/mobile/swift` pins Apple's generator 1.14.1, runtime
1.13.1, URLSession transport 1.3.2, and transitive dependencies in `Package.resolved`.
It generates and compiles the bundled Swift input and tests session/profile decoding,
unknown and populated financial states, recurring subscriptions, event reservations,
and literal-null completed payment URLs.

The canonical and mobile contracts preserve their original schemas. Apple's pinned
generator skips standalone null branches, so `swift.openapi.json` expresses nullable
object/scalar unions as nullable types. Referenced object fields and constraints are
retained; nullable enums explicitly include null. A null-only payment URL uses the
`DADLiteralNull: DADNullValue` type override from the probe's configuration. Its supported
schema placeholder is only a generator input: the custom Codable type accepts and emits
literal null and rejects every non-null value. Apps adding payment-update operations must
include that override and the bundled source. The iOS session client currently filters
out those unrelated operations.

The probe no longer skips nullable fields. Unused public-import warnings emitted by the
pinned generator with Swift 6.4 remain visible; no warning is suppressed.

## Real native client acceptance

With both native repos alongside this checkout, the normal native tooling installed,
and a migrated synthetic database, run:

```sh
TEST_DATABASE_URL=postgresql://go_test:go_test@localhost:55440/donor_go make test-native-clients
```

`DAD_IOS_DIR`, `DAD_ANDROID_DIR`, and `DAD_SWIFT_BUILD` override the checkout/build paths.
The command runs the backend's database-backed session lifecycle test, then holds open
its test-only HTTPS server on loopback for the actual Swift URLSession and Kotlin Retrofit
clients. Private temporary fixtures contain separate one-use proofs and the test server's
certificate. The native test transports trust only that certificate; production trust
configuration remains unchanged. The server exists only in the Go test binary, inherits
the synthetic-database guard, uses the least-privilege API role, and calls no providers.

Each client proves claim and profile decoding, single-use claim rejection, locally
expired access triggering refresh and atomic replacement, rejection of replaced access,
remote revocation clearing local credentials, and logout revoking both credentials.
The opt-in Swift build flag and Gradle property compile these integration suites only
for this command; ordinary unit tests retain their existing scope without skipped tests.
The command stops the server and removes credential fixtures on completion or failure.
Cross-repo CI needs authenticated access to all three pinned revisions before this can
be a required hosted check. This local acceptance does not establish provider delivery,
OS app-link association, or physical-device acceptance.

## Design source

Edit `design/tokens.json`, then run `make generate-mobile`. The web adapter consumes its
generated TypeScript representation. `make check-mobile` fails on drift. Native lengths
are reference values for points/dp; text adapters must support Dynamic Type/font scaling,
and platform font families remain platform decisions. Colors retain their original
hex/RGBA strings so adapters can handle opacity consistently. This foundation does not
establish approved native layouts or visual/accessibility acceptance.

## App foundation boundary

The mobile implementation decision is separate SwiftUI/iOS and Kotlin/Compose/Android
applications in `DAD-iOS` and `DAD-Android`, approved on 18 October 2026. This supersedes
the historical PRD's single cross-platform codebase requirement. DAD-Main continues to
own business rules, API contracts, design values, and server integrations; client code
owns native presentation, lifecycle, secure credential storage, and platform interaction.
The R1 PRD defines the required donor and partner features; the native foundations
are the transport and session starting point for those screens.

The native foundations implement an authenticated read, single-flight
refresh and atomic token replacement, cancellation/timeouts, revoked-session handling,
and local credential cleanup on logout against synthetic transports. Real API and device
acceptance must prove the same behavior. A refresh response arriving after logout must
never restore the session. Secure persistence must avoid logs and backups of credentials;
Android uses encryption keys protected by Keystore, iOS uses Keychain.

Link handling accepts only the configured HTTPS public origin and approved paths.
Proof tokens stay in memory and out of logs. Signed-out destinations wait for account
access; referrals and unsupported product destinations retain their web behavior. Push
payloads select only approved relative destinations, never arbitrary URLs. OS association
files require the final app identifiers and signing certificates, and a reachable direct
API ingress is required for a real-device integration test. Neither is fabricated here.

Production signing, camera/notification permission timing, analytics activation, and
real-device/provider acceptance remain open. The R1 PRD defines donor and partner scope;
the native foundations do not yet implement those product journeys. Offline financial
mutations require a separate design rather than replaying commands blindly.
