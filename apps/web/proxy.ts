import { type NextRequest, NextResponse, type ProxyConfig } from "next/server";
import { contentSecurityPolicy } from "@/lib/csp";
import { UNSERVED_HEADER } from "@/lib/hosts";

// Case-insensitive to agree with next.config.ts, whose header sources always are; a
// case-sensitive matcher would leave /Account/... with no policy at all.
const signedInSurface = /^\/(?:account|staff)(?:\/|$)/i;
const staffSurface = /^\/(?:staff|api\/donor\/staff)(?:\/|$)/i;
// What a staff page loads from Next besides the static assets the matcher already skips.
const framework = /^\/(?:_next|__nextjs)/;

const hostname = (url?: string | null) => {
  try { return url ? new URL(url).hostname.toLowerCase() : ""; } catch { return ""; }
};

/**
 * Splits the site by host. The staff host (DONOR_STAFF_ORIGIN) serves only the back office
 * and its API; the public host serves everything else. Both are fixed at build time.
 *
 * It also gives the signed-in surfaces a per-request nonce policy. Next reads the nonce from
 * the request's CSP header and puts it on its own scripts, so these routes render dynamically.
 */
export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;
  const staffOrigin = process.env.DONOR_STAFF_ORIGIN;
  const staffHost = hostname(staffOrigin);
  const onStaffHost = staffHost !== "" && hostname(`http://${request.headers.get("host") ?? request.nextUrl.host}`) === staffHost;
  if (onStaffHost && pathname === "/") return NextResponse.redirect(`${staffOrigin}/staff`);
  if (onStaffHost && pathname === "/robots.txt") {
    return new NextResponse("User-agent: *\nDisallow: /\n", { headers: { "Content-Type": "text/plain", "X-Robots-Tag": "noindex, nofollow" } });
  }
  const served = onStaffHost ? staffSurface.test(pathname) || framework.test(pathname) : !staffSurface.test(pathname);
  // Staff pages render per request, so Next already marks them uncacheable.
  const staffHeaders = (response: NextResponse) => {
    if (onStaffHost) response.headers.set("X-Robots-Tag", "noindex, nofollow");
    return response;
  };
  const signedIn = signedInSurface.test(pathname);
  if (!served && !signedIn) {
    return staffHeaders(pathname.startsWith("/api/") ? NextResponse.json({ detail: "Not found" }, { status: 404 })
      : new NextResponse("Not found", { status: 404, headers: { "Content-Type": "text/plain" } }));
  }
  if (!signedIn) return staffHeaders(NextResponse.next());
  const policy = contentSecurityPolicy(btoa(crypto.randomUUID()), onStaffHost ? staffOrigin : undefined);
  const headers = new Headers(request.headers);
  headers.set("Content-Security-Policy", policy);
  // The surface's layout answers not found, so the page keeps its path, frame, and policy.
  headers.delete(UNSERVED_HEADER);
  if (!served) headers.set(UNSERVED_HEADER, "1");
  const response = NextResponse.next({ request: { headers } });
  response.headers.set("Content-Security-Policy", policy);
  return staffHeaders(response);
}

export const config: ProxyConfig = {
  matcher: [{
    source: "/((?!_next/static/|_next/image|favicon.ico).*)",
  }],
};
