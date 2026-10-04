import type { NextRequest } from "next/server";
import { isIP } from "node:net";

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

const apiBase = () => process.env.DONOR_API_BASE_URL ?? "http://localhost:8002";

// Headers that describe a single hop and must not be forwarded.
const HOP_BY_HOP = new Set([
  "connection",
  "keep-alive",
  "transfer-encoding",
  "upgrade",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "host",
  "content-length",
]);

function forwardHeaders(source: Headers): Headers {
  const headers = new Headers();
  source.forEach((value, key) => {
    if (!HOP_BY_HOP.has(key.toLowerCase())) headers.set(key, value);
  });
  return headers;
}

async function proxy(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  const { path } = await context.params;
  if (path[0] !== "donor" || path.some((segment) => segment === "." || segment === ".." || /[\\/?#%]/.test(segment))) {
    return Response.json({ detail: "Not found" }, { status: 404 });
  }
  const target = new URL(`${apiBase()}/${path.map(encodeURIComponent).join("/")}`);
  target.search = request.nextUrl.search;

  const hasBody = request.method !== "GET" && request.method !== "HEAD";
  const requestHeaders = forwardHeaders(request.headers);
  requestHeaders.delete("x-real-ip");
  requestHeaders.delete("x-forwarded-for");
  // Enable only behind ingress that overwrites this header and blocks direct access.
  const clientHeader = process.env.DONOR_CLIENT_IP_HEADER;
  const clientIP = clientHeader ? request.headers.get(clientHeader) : null;
  if (clientIP && isIP(clientIP)) requestHeaders.set("x-real-ip", clientIP);
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

  const headers = forwardHeaders(response.headers);
  // Preserve every Set-Cookie, which a plain Headers copy would collapse into one.
  headers.delete("set-cookie");
  const cookies = response.headers.getSetCookie?.() ?? [];
  for (const cookie of cookies) headers.append("set-cookie", cookie);
  headers.delete("content-encoding");

  return new Response(response.body, { status: response.status, headers });
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const PATCH = proxy;
export const DELETE = proxy;
export const HEAD = proxy;
export const OPTIONS = proxy;
