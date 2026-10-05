// @vitest-environment node
import { NextRequest } from "next/server";
import { afterEach, expect, it, vi } from "vitest";
import { GET, POST } from "./route";

afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); });

it("strips untrusted client address headers at the browser boundary", async () => {
  vi.stubEnv("DONOR_CLIENT_IP_HEADER", "");
  const fetch = vi.fn().mockResolvedValue(Response.json({ ok: true }));
  vi.stubGlobal("fetch", fetch);
  await GET(new NextRequest("http://localhost:3000/api/donor/public/content", {
    headers: { "X-Real-IP": "203.0.113.1", "X-Forwarded-For": "203.0.113.2" },
  }), { params: Promise.resolve({ path: ["donor", "public", "content"] }) });
  const headers = new Headers(fetch.mock.calls[0][1].headers);
  expect(headers.has("x-real-ip")).toBe(false);
  expect(headers.has("x-forwarded-for")).toBe(false);
});

it.each(["203.0.113.1", "2001:db8::1", "203.0.113.1, 203.0.113.2", "unknown"])(
  "forwards only a single IP from the configured ingress header: %s", async (address) => {
    vi.stubEnv("DONOR_CLIENT_IP_HEADER", "x-ingress-client");
    const fetch = vi.fn().mockResolvedValue(Response.json({ ok: true }));
    vi.stubGlobal("fetch", fetch);
    await GET(new NextRequest("http://localhost:3000/api/donor/public/content", {
      headers: { "X-Real-IP": "192.0.2.99", "x-ingress-client": address },
    }), { params: Promise.resolve({ path: ["donor", "public", "content"] }) });
    expect(new Headers(fetch.mock.calls[0][1].headers).get("x-real-ip")).toBe(
      ["203.0.113.1", "2001:db8::1"].includes(address) ? address : null,
    );
  },
);

const context = (...path: string[]) => ({ params: Promise.resolve({ path }) });

it.each([
  [["claims"]],
  [["staff", "donor"]],
  [["donor", "..", "admin"]],
  [["donor", "."]],
  [["donor", "me%2F..%2Fadmin"]],
  [["donor", "me/../admin"]],
  [["donor", "a%2Fb"]],
  [["donor", "a\\b"]],
])("refuses %j without contacting the API", async (path) => {
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  const response = await GET(new NextRequest("http://localhost:3000/api/x"), context(...path));
  expect(response.status).toBe(404);
  expect(fetch).not.toHaveBeenCalled();
});

it("forwards only the allowlisted request headers", async () => {
  vi.stubEnv("DONOR_CLIENT_IP_HEADER", "");
  vi.stubEnv("DONOR_API_BASE_URL", "http://api.test:8002");
  const fetch = vi.fn().mockResolvedValue(Response.json({ ok: true }));
  vi.stubGlobal("fetch", fetch);
  await POST(new NextRequest("http://localhost:3000/api/donor/checkouts", {
    method: "POST", body: "{}",
    headers: {
      cookie: "dad_session=s", "content-type": "application/json", accept: "application/json", "accept-language": "en-US",
      "idempotency-key": "k", origin: "http://localhost:3000", "user-agent": "browser", "stripe-signature": "t=1,v1=x",
      authorization: "Bearer stolen", "x-forwarded-host": "evil.example", "x-staff-role": "ADMIN", forwarded: "for=1.2.3.4", host: "evil.example",
    },
  }), context("donor", "checkouts"));
  const sent = new Headers(fetch.mock.calls[0][1].headers);
  expect(Object.fromEntries(sent)).toEqual({
    cookie: "dad_session=s", "content-type": "application/json", accept: "application/json", "accept-language": "en-US",
    "idempotency-key": "k", origin: "http://localhost:3000", "user-agent": "browser", "stripe-signature": "t=1,v1=x",
  });
  expect(fetch.mock.calls[0][0].href).toBe("http://api.test:8002/donor/checkouts");
});

it("preserves every Set-Cookie and drops hop-by-hop response headers", async () => {
  const upstream = new Headers({ "content-type": "application/json", connection: "keep-alive", "x-request-id": "r1" });
  upstream.append("set-cookie", "dad_session=a; Path=/; HttpOnly");
  upstream.append("set-cookie", "dad_referral=b; Path=/; HttpOnly");
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("{}", { status: 201, headers: upstream })));
  const response = await GET(new NextRequest("http://localhost:3000/api/donor/me"), context("donor", "me"));
  expect(response.status).toBe(201);
  expect(response.headers.getSetCookie()).toEqual(["dad_session=a; Path=/; HttpOnly", "dad_referral=b; Path=/; HttpOnly"]);
  expect(response.headers.get("x-request-id")).toBe("r1");
  expect(response.headers.has("connection")).toBe(false);
});

it("answers 502 with a structured error when the API is unreachable", async () => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("fetch failed")));
  const response = await GET(new NextRequest("http://localhost:3000/api/donor/me"), context("donor", "me"));
  expect(response.status).toBe(502);
  expect(await response.json()).toEqual({ detail: { code: "API_UNREACHABLE", message: "The requested service could not be reached." } });
});
