# Referral Wallet passes

R1 issues static Apple Wallet and Google Wallet passes from the authenticated web
referral-code page. Both encode the same public referral URL with `?via=irl`; native
camera scans need no account or app. A pass carries no donor email, internal account ID,
financial history, session proof, or mutable dashboard figures. The public referral URL
is deliberately shareable and is not an authentication credential.

Issuance is disabled unless configured. Both providers require a public HTTPS origin.
Partial configuration fails API startup. Configuration files are read once, limited to
1 MB, and must be private files supplied by the deployment secret manager or read-only
secret mounts. Never commit issuer keys or copy them into container images.

Apple requires `DONOR_WALLET_APPLE_CERT_FILE`, `DONOR_WALLET_APPLE_KEY_FILE`, and
`DONOR_WALLET_APPLE_WWDR_FILE`. Supply the Pass Type ID certificate, matching RSA private
key (PKCS#1 or PKCS#8 PEM, at least 2048 bits), and issuing WWDR intermediate. The pass
identifier and team are taken from the certificate. Key match, issuer signature, and
certificate validity are checked; validity is rechecked on every issuance. Rotate files
and restart the API before expiry. The bundle includes icons, pass JSON, SHA-1 manifest
hashes as specified by Apple, and a detached SHA-256 PKCS#7 signature with the intermediate.
The focused `smallstep/pkcs7` dependency supplies CMS signing rather than bespoke ASN.1
cryptography. Only a Wallet bundle is offered, never a downloadable standalone QR image.

Google requires `DONOR_WALLET_GOOGLE_KEY_FILE` (service-account JSON) and
`DONOR_WALLET_GOOGLE_CLASS_ID` (an existing approved Generic class, issuer ID plus class
suffix). Authorize that service account in the Wallet console. The API signs RS256
Save to Wallet claims locally using the standard library; the browser follows Google's
save link only after the donor requests it. No server-side Google API call or additional
SDK is needed. The pass ID is deterministic for the public referral link, so repeated
issuance does not create arbitrary new pass identities.

Passes explicitly say sandbox and carry the $30/month disclosure and April 2027 launch
message. They do not attest that a real gift was made. If a donor replaces their referral
link, old pass codes stop working for new visitors just like the old web link. Passes
instruct donors to remove the old pass and add the replacement. There is no automatic
pass update/push service in R1. Test reissue and removal on both platforms.

Local tests verify manifest integrity, cryptographic signatures, required claims,
matching IRL payloads, expiry, URL constraints, authentication, and cross-origin rejection.
They do not prove Apple/Google acceptance. Release acceptance requires real issuer
accounts/certificates, approved visual assets/button treatment, installation on actual
iOS/Android devices, lock-screen access, and native-camera scans under glare, low
brightness, and at angles. A website cannot reliably change system screen brightness;
the web code page provides a high-contrast full-screen code and brightness instructions.

Provider references:

- [Apple pass design and signing](https://developer.apple.com/library/archive/documentation/UserExperience/Conceptual/PassKit_PG/Creating.html)
- [Apple identifiers and certificates](https://developer.apple.com/help/account/capabilities/create-wallet-identifiers-and-certificates)
- [Google signed Save to Wallet claims](https://developers.google.com/wallet/generic/use-cases/jwt)
- [Google Generic object fields](https://developers.google.com/wallet/reference/rest/v1/genericobject)
