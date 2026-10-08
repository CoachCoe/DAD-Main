# Security policy

This R1 build is test-mode software. It must not receive live donations until its release
requirements and external acceptance gates have been satisfied.

Report vulnerabilities privately through the repository's GitHub Security Advisories
when enabled, or directly to the repository owner. Do not include credentials or donor
records in a public issue. Include the tested commit, reproduction steps, and impact.

In scope: donor/staff authentication and authorization, financial correctness and
idempotency, webhook verification, privacy, exports, encrypted content, dependency
security, and resource exhaustion. Staff passkeys, object ownership, and refund controls
are security boundaries, not optional user-interface behavior.

Never commit filled environment files, secret keys, account links, staff invitations,
SMTP passwords, database credentials, or donor exports. Test fixtures must contain only
synthetic identities and clearly non-production secrets. If a real secret enters Git
history, revoke it and report the incident; deleting the current file does not revoke it.

See [security controls and limitations](docs/security.md) and
[operations](docs/operations.md). MIT licensing does not imply a security certification.
