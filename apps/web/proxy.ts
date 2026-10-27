import { type NextRequest, NextResponse, type ProxyConfig } from "next/server";
import { contentSecurityPolicy } from "@/lib/csp";
import { isStaffHost, UNSERVED_HEADER } from "@/lib/hosts";

// Case-insensitive to agree with next.config.ts, whose header sources always are; a
// case-sensitive matcher would leave /Account/... with no policy at all.
const signedInSurface = /^\/(?:account|partner|staff)(?:\/|$)/i;
// The staff host serves the back office's routes exactly as written, since routes are
// case-sensitive; the public host refuses any spelling of them.
const staffSurface = /^\/(?:staff|api\/donor\/staff)(?:\/|$)/;
const staffSurfaceSpelled = new RegExp(staffSurface.source, "i");
// What a staff page loads from Next besides the static assets the matcher already skips.
const framework = /^\/(?:_next|__nextjs)/;

const decoded = (pathname: string) => {
  try {
    return decodeURIComponent(pathname);
  } catch {
    return null;
  }
};

/**
 * Splits the site by host. The staff host (DONOR_STAFF_ORIGIN) serves only the back office
 * and its API; the public host serves everything else. Both are fixed at build time. The
 * split is decided on the decoded path, as the pages and the API proxy receive it, so an
 * encoded /api/donor/%73taff cannot pass for a public path. An unserved page keeps its
 * address but renders the answering host's not-found page, in that host's frame.
 *
 * It also gives the signed-in surfaces a per-request nonce policy. Next reads the nonce from
 * the request's CSP header and puts it on its own scripts, so these routes render dynamically.
 */
export function proxy(request: NextRequest) {
  const staffOrigin = process.env.DONOR_STAFF_ORIGIN;
  const onStaffHost = isStaffHost(request.headers.get("host") ?? request.nextUrl.host);
  const pathname = decoded(request.nextUrl.pathname);
  if (pathname === null) return NextResponse.json({ detail: "Not found" }, { status: 404 });
  if (onStaffHost && pathname === "/") return NextResponse.redirect(`${staffOrigin}/staff`);
  if (onStaffHost && pathname === "/robots.txt") {
    return new NextResponse("User-agent: *\nDisallow: /\n", {
      headers: { "Content-Type": "text/plain", "X-Robots-Tag": "noindex, nofollow" },
    });
  }
  const served = onStaffHost
    ? staffSurface.test(pathname) || framework.test(pathname) || pathname === "/icon.svg"
    : !staffSurfaceSpelled.test(pathname);
  // Staff pages render per request, so Next already marks them uncacheable.
  const staffHeaders = (response: NextResponse) => {
    if (onStaffHost) response.headers.set("X-Robots-Tag", "noindex, nofollow");
    return response;
  };
  if (!served && pathname.startsWith("/api/"))
    return staffHeaders(NextResponse.json({ detail: "Not found" }, { status: 404 }));
  const headers = new Headers(request.headers);
  headers.delete(UNSERVED_HEADER);
  if (!served) headers.set(UNSERVED_HEADER, onStaffHost ? "staff" : "public");
  // next.config.ts gives every other path the static policy.
  if (!signedInSurface.test(pathname)) return staffHeaders(NextResponse.next({ request: { headers } }));
  // The public not-found page is prerendered, so it has no nonce to carry.
  const policy =
    served || onStaffHost
      ? contentSecurityPolicy(btoa(crypto.randomUUID()), onStaffHost ? staffOrigin : undefined)
      : contentSecurityPolicy();
  headers.set("Content-Security-Policy", policy);
  const response = NextResponse.next({ request: { headers } });
  response.headers.set("Content-Security-Policy", policy);
  return staffHeaders(response);
}

export const config: ProxyConfig = {
  matcher: [
    {
      source: "/((?!_next/static/|_next/image|favicon.ico).*)",
    },
  ],
};
