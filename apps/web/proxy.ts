import { type NextRequest, NextResponse, type ProxyConfig } from "next/server";
import { contentSecurityPolicy } from "@/lib/csp";

// Case-insensitive to agree with next.config.ts, whose header sources always are; a
// case-sensitive matcher would leave /Account/... with no policy at all.
const signedInSurface = /^\/(?:account|staff)(?:\/|$)/i;

/**
 * Gives the signed-in surfaces a per-request nonce policy. Next reads the nonce from the
 * request's CSP header and puts it on its own scripts, so these routes render dynamically.
 */
export function proxy(request: NextRequest) {
  if (!signedInSurface.test(request.nextUrl.pathname)) return NextResponse.next();
  const policy = contentSecurityPolicy(btoa(crypto.randomUUID()));
  const headers = new Headers(request.headers);
  headers.set("Content-Security-Policy", policy);
  const response = NextResponse.next({ request: { headers } });
  response.headers.set("Content-Security-Policy", policy);
  return response;
}

export const config: ProxyConfig = {
  matcher: [{
    source: "/((?!api/|_next/static/|_next/image|favicon.ico).*)",
    missing: [{ type: "header", key: "next-router-prefetch" }, { type: "header", key: "purpose", value: "prefetch" }],
  }],
};
