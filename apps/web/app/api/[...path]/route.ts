import type { NextRequest } from "next/server";
import { trustedClientIp } from "@/lib/clientIp";
import { donorApiBase } from "@/lib/donorApi";

/**
 * Runtime proxy to the donor API.
 *
 * This was a `rewrites()` entry in next.config.ts, but with `output: standalone` the
 * destination is resolved when the config is evaluated and baked into server.js -- so the
 * image always pointed at whatever API_BASE_URL was set to at *build* time, and setting it
 * at runtime silently did nothing. A route handler reads it per request, which keeps one
 * image usable across environments.
 *
 * The proxy exists so the session cookie is first-party. Sent cross-origin it would need
 * SameSite=None, which requires HTTPS and would make the cookie usable from any site.
 */
export const dynamic = "force-dynamic";

// Only what the API reads crosses the boundary; anything else a browser or an
// intermediary adds (including spoofable forwarding headers) is dropped.
const REQUEST_HEADERS = ["accept", "accept-language", "content-type", "cookie", "idempotency-key", "origin", "stripe-signature", "user-agent"];

// Response headers not relayed as they are: hop-by-hop ones, a length and encoding that
// fetch has already undone, and Set-Cookie, which is appended separately below.
const HOP_BY_HOP = new Set([
  "connection",
  "keep-alive",
  "transfer-encoding",
  "upgrade",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "content-length",
  "content-encoding",
  "set-cookie",
]);

async function proxy(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  const { path } = await context.params;
  if (path[0] !== "donor" || path.some((segment) => segment === "." || segment === ".." || /[\\/?#%]/.test(segment))) {
    return Response.json({ detail: "Not found" }, { status: 404 });
  }
  const target = new URL(`${donorApiBase()}/${path.map(encodeURIComponent).join("/")}`);
  target.search = request.nextUrl.search;

  const hasBody = request.method !== "GET" && request.method !== "HEAD";
  const requestHeaders = new Headers();
  for (const name of REQUEST_HEADERS) {
    const value = request.headers.get(name);
    if (value !== null) requestHeaders.set(name, value);
  }
  const client = trustedClientIp(request.headers);
  if (client) requestHeaders.set("x-real-ip", client);
  let response: Response;
  try {
    response = await fetch(target, {
      method: request.method,
      headers: requestHeaders,
      // Stream source documents without buffering them in the web server.
      body: hasBody ? request.body : undefined,
      redirect: "manual",
      signal: AbortSignal.any([request.signal, AbortSignal.timeout(28_000)]),
      // Required by undici when the body is a stream.
      ...(hasBody ? { duplex: "half" } : {}),
    } as RequestInit);
  } catch {
    return Response.json(
      {
        detail: {
          code: "API_UNREACHABLE",
          message: "The requested service could not be reached.",
        },
      },
      { status: 502 },
    );
  }

  const headers = new Headers();
  response.headers.forEach((value, key) => {
    if (!HOP_BY_HOP.has(key)) headers.set(key, value);
  });
  // Appended one by one: a plain Headers copy would fold several Set-Cookie values into one.
  for (const cookie of response.headers.getSetCookie()) headers.append("set-cookie", cookie);

  return new Response(response.body, { status: response.status, headers });
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const PATCH = proxy;
export const DELETE = proxy;
export const HEAD = proxy;
export const OPTIONS = proxy;
