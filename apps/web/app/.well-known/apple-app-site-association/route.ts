/**
 * Associates the donor iOS app with this site: universal links for the pages donors reach
 * from emails and referrals, and webcredentials so passkeys made here also sign the app in.
 * See https://developer.apple.com/documentation/xcode/supporting-associated-domains
 */

export const dynamic = "force-static";

const APP_ID = /^[A-Z0-9]{10}\.[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$/;

/** The account pages the app handles; giving and the rest of the public site stay in the browser. */
const APP_PATHS = [
  "/r/*",
  "/account/claim",
  "/account/email-change",
  "/account/payment-method",
  "/account/dashboard",
  "/account/profile",
];

/** DONOR_IOS_APP_ID, "<team ID>.<bundle ID>", fixed at build time. */
export function GET() {
  const appID = process.env.DONOR_IOS_APP_ID?.trim();
  if (!appID) return Response.json({ detail: "Not found" }, { status: 404 });
  if (!APP_ID.test(appID)) throw new Error("DONOR_IOS_APP_ID must be <10-character team ID>.<bundle ID>");
  const association = {
    applinks: { details: [{ appIDs: [appID], components: APP_PATHS.map((path) => ({ "/": path })) }] },
    webcredentials: { apps: [appID] },
  };
  return Response.json(association, { headers: { "Cache-Control": "public, max-age=3600" } });
}
