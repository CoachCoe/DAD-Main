# Shared mobile foundation

DAD-Main owns donor business rules, the canonical OpenAPI contract, design values, and
supported donor links. Mobile applications consume a pinned release of these interfaces.
Their screens and platform interactions are separate from the Go financial domain.

## Versioned bundle

`contracts/mobile/version.json` versions the bundle independently from backend releases.
`make bundle-mobile MOBILE_BUNDLE_DIR=/tmp/dad-foundation` creates:

- `openapi.json`: the complete canonical API.
- `mobile.openapi.json`: native-admitted mutations, donor-account reads, and liveness,
  derived from the canonical API without rewriting schemas or response variants.
- `tokens.json`: platform-neutral design values, with `schema_version`.
- `tokens.ts`, `DADTokens.swift`, `DADTokens.kt`: generated platform values.
- `links.json`: approved account, proof, referral, and notification destinations.
- `manifest.json`: bundle version, source Git revision, and SHA-256 of every other file.

The mobile contract is an integration surface, not a decision to expose every operation
in the first mobile product. Backend authorization remains authoritative. Native requests
call the API host directly with `X-DAD-Client: native`, no cookies or Origin, and a bearer
access token when authenticated. Browser-only giving and staff operations stay on the web.

After checks pass on the release commit, tag it `mobile-foundation-v1.0.0` (using the
version in the file) and push that tag. The mobile-foundation workflow verifies the tag
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
gh release download mobile-foundation-v1.0.0 --repo CoachCoe/DAD-Main --dir /tmp/dad-foundation
```

Use patch versions for equivalent packaging corrections, minor versions for compatible
additions, and major versions for breaking contract/token/link changes. A mobile release
pins its dependency; changing the backend must preserve supported installed clients.
Breaking field removals, type changes, and newly required request fields require a
coordinated compatibility plan. Do not invent financial values for unknown server states.

## Swift generator compatibility

The compile probe in `contracts/mobile/swift` pins Apple's generator 1.14.1, runtime
1.13.1, URLSession transport 1.3.2, and transitive dependencies in `Package.resolved`.
It generates and compiles the actual mobile contract and tests session/profile decoding.
This is a tooling probe, not an approved application SDK or an app-stack selection.

The generator emits unsupported-schema warnings for `anyOf` containing a standalone
`type: null`, used by dashboard and recurring-gift states. It also emits unused public
import warnings with Swift 6.4. No warning is suppressed. The canonical and mobile
contracts retain these states unchanged. Before using this generator for affected
features, resolve the nullable-schema issue with an upstream fix or explicitly tested
client-side type overrides. See [Apple generator issue 817](https://github.com/apple/swift-openapi-generator/issues/817).
Generated response decoding also enforces `additionalProperties: false`; adding response
fields is therefore not automatically safe for installed generated clients. Client
tolerance and supported-version compatibility tests must precede such backend changes.

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
The final mobile feature subset and navigation remain subject to the mobile PRD.

Once chosen, the first implementation should prove an authenticated read, single-flight
refresh and atomic token replacement, cancellation/timeouts, revoked-session handling,
and local credential cleanup on logout. A refresh response arriving after logout must
never restore the session. Secure persistence must avoid logs and backups of credentials;
Android uses encryption keys protected by Keystore, iOS uses Keychain.

Link handling accepts only the configured HTTPS public origin and approved paths.
Proof tokens stay in memory and out of logs. Signed-out destinations wait for account
access; referrals and unsupported product destinations retain their web behavior. Push
payloads select only approved relative destinations, never arbitrary URLs. OS association
files require the final app identifiers and signing certificates, and a reachable direct
API ingress is required for a real-device integration test. Neither is fabricated here.

Analytics, production signing, camera/notification permission timing, offline financial
mutations, and the final feature/navigation set require their own product decisions.
