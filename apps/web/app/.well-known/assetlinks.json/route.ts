/**
 * Associates the donor Android app with this site: app links, for the paths the app's
 * manifest claims, and login credentials so passkeys made here also sign the app in.
 * See https://developer.android.com/training/app-links/verify-android-applinks
 */

export const dynamic = "force-static";

const PACKAGE = /^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$/;
const FINGERPRINT = /^[0-9A-F]{2}(:[0-9A-F]{2}){31}$/;

/** DONOR_ANDROID_APP_PACKAGE and DONOR_ANDROID_APP_CERT_SHA256 (comma-separated), fixed at build time. */
export function GET() {
  const packageName = process.env.DONOR_ANDROID_APP_PACKAGE?.trim();
  const fingerprints = (process.env.DONOR_ANDROID_APP_CERT_SHA256 ?? "").split(",").map((value) => value.trim().toUpperCase()).filter(Boolean);
  if (!packageName && fingerprints.length === 0) return Response.json({ detail: "Not found" }, { status: 404 });
  if (!packageName || !PACKAGE.test(packageName)) throw new Error("DONOR_ANDROID_APP_PACKAGE must be the app's package name");
  if (fingerprints.length === 0 || !fingerprints.every((value) => FINGERPRINT.test(value))) {
    throw new Error("DONOR_ANDROID_APP_CERT_SHA256 must list SHA-256 fingerprints as colon-separated hex");
  }
  const statements = [{
    relation: ["delegate_permission/common.handle_all_urls", "delegate_permission/common.get_login_creds"],
    target: { namespace: "android_app", package_name: packageName, sha256_cert_fingerprints: fingerprints },
  }];
  return Response.json(statements, { headers: { "Cache-Control": "public, max-age=3600" } });
}
